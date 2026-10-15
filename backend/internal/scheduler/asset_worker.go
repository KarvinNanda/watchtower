// Package scheduler ties the asset and sentinel pipelines' fetchers,
// analyzers, and notifier together into periodic jobs.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/karvin-nanda/watchtower/internal/asset"
	"github.com/karvin-nanda/watchtower/internal/cache"
	"github.com/karvin-nanda/watchtower/internal/currency"
	"github.com/karvin-nanda/watchtower/internal/notifier"
	"github.com/karvin-nanda/watchtower/internal/rules"
)

const (
	dbQueryTimeout   = 30 * time.Second
	telegramSendGap  = 50 * time.Millisecond
	wibOffsetSeconds = 7 * 60 * 60
)

// NotifierInterface is the subset of notifier.Notifier's behavior
// AssetWorker depends on, allowing tests to inject a mock sender.
type NotifierInterface interface {
	SendAssetAlert(chatID int64, message string) error
}

// marketSource is the subset of cache.Cache the worker reads from,
// allowing tests to inject fake market data.
type marketSource interface {
	GetMarketData(ctx context.Context, symbol, assetType string) (*cache.MarketData, error)
	GetCryptoWeek52(ctx context.Context, symbol string) (high, low float64, err error)
}

// AssetWorker periodically fetches market data (via the Redis/MySQL cache
// layer), evaluates every active asset rule with rules.Evaluate, and
// notifies users whose rules fire. Every alert for a given user within one
// run is batched into a single Telegram message (see UserAlertBatch).
type AssetWorker struct {
	db       *sql.DB
	redis    *redis.Client
	notifier NotifierInterface

	mu sync.Mutex // serializes periodic and manual asset runs

	cache             marketSource
	notifLogs         *notifier.LogRepository
	maxUniqueSymbols  int
	alertPriceMovePct float64
}

// NewAssetWorker wires together every dependency needed to run the asset
// price alert pipeline. fetcher is used internally to build the market data
// cache layer; cacheTTLHours should be ASSET_SCHEDULER_INTERVAL_HOURS.
func NewAssetWorker(
	db *sql.DB,
	redisClient *redis.Client,
	fetcher *asset.AssetFetcher,
	notif NotifierInterface,
	cacheTTLHours int,
	maxUniqueSymbols int,
	alertPriceMovePct float64,
) *AssetWorker {
	return &AssetWorker{
		db:                db,
		redis:             redisClient,
		notifier:          notif,
		cache:             cache.New(redisClient, db, cacheTTLHours, fetcher),
		notifLogs:         notifier.NewLogRepository(db),
		maxUniqueSymbols:  maxUniqueSymbols,
		alertPriceMovePct: alertPriceMovePct,
	}
}

// SymbolInfo identifies a distinct symbol with at least one active rule.
// NeedsWeek52 is true when one of those rules is a week52_break.
type SymbolInfo struct {
	Symbol      string
	AssetType   string
	NeedsWeek52 bool
}

// AlertItem is one fired rule for a single user, collected during a run
// and rendered as one block of that user's batched message. OnSent is
// applied to the rule only after the message was delivered.
type AlertItem struct {
	RuleID        uint64
	ParamsVersion uint32
	Symbol        string
	PriceUSD      float64
	PriceIDR      float64
	ChangePct     *float64
	Trigger       rules.Trigger
	OnSent        rules.Patch
}

// UserAlertBatch accumulates every AlertItem fired for one user during a
// single run, so they can be delivered as one combined Telegram message.
type UserAlertBatch struct {
	UserID   uint64
	ChatID   int64
	Language string
	Alerts   []AlertItem
}

// runScope narrows a run to one user (manual "Check now") or leaves it
// global (userID 0, periodic run).
type runScope struct {
	userID       uint64
	skipCooldown bool
}

// AssetRunResult summarizes one asset run for the manual-run API.
type AssetRunResult struct {
	RulesChecked int
	Triggered    int
	Sent         int
	Errors       int
}

// symbolResult is processSymbol's per-symbol tally. dataErrors counts
// optional data that could not be fetched (crypto 52-week range) while the
// symbol's other rules were still evaluated.
type symbolResult struct {
	checked, triggered, dataErrors int
}

// Run is the periodic entry point: every active rule, cooldowns respected.
// A run where symbols failed and no rule was checked (typically no
// network) returns an error so the Pipeline records it as failed and
// retries soon instead of after a full interval.
func (w *AssetWorker) Run() error {
	res, err := w.run(runScope{})
	if err != nil {
		return err
	}
	if res.Errors > 0 && res.RulesChecked == 0 {
		return fmt.Errorf("scheduler: all %d asset symbol(s) failed", res.Errors)
	}
	return nil
}

// RunForUser evaluates only userID's rules and ignores cooldown (not
// crossing or one-shot). Rule state is still written on send, so the next
// periodic run does not repeat the same alert.
func (w *AssetWorker) RunForUser(userID uint64) (AssetRunResult, error) {
	return w.run(runScope{userID: userID, skipCooldown: true})
}

// run holds w.mu for its whole duration so a periodic run and a manual run
// never interleave (which could double-send and race on rule state). It
// never panics — any recovered panic is returned as an error.
func (w *AssetWorker) run(scope runScope) (result AssetRunResult, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] AssetWorker.run: recovered from panic: %v", r)
			err = fmt.Errorf("scheduler: recovered from panic: %v", r)
		}
	}()

	start := time.Now()
	log.Printf("[INFO] AssetWorker.run: starting (user=%d) at %s", scope.userID, start.Format(time.RFC3339))

	symbols, err := w.getUniqueActiveSymbols(scope.userID)
	if err != nil {
		return result, fmt.Errorf("scheduler: get unique active symbols: %w", err)
	}

	batches := make(map[uint64]*UserAlertBatch)
	for _, symbol := range symbols {
		res, procErr := w.processSymbolSafe(symbol, scope, batches)
		if procErr != nil {
			result.Errors++
			log.Printf("[ERROR] AssetWorker.run: process symbol %s failed: %v", symbol.Symbol, procErr)
			continue
		}
		result.RulesChecked += res.checked
		result.Triggered += res.triggered
		result.Errors += res.dataErrors
	}

	alertsSent, usersNotified := w.dispatchBatches(batches)
	result.Sent = alertsSent

	log.Printf("[INFO] AssetWorker.run: completed in %s (user=%d) — symbols: %d, rules checked: %d, "+
		"triggered: %d, users notified: %d, alerts sent: %d, errors: %d",
		time.Since(start), scope.userID, len(symbols), result.RulesChecked, result.Triggered,
		usersNotified, result.Sent, result.Errors)

	return result, nil
}

// getUniqueActiveSymbols returns every distinct symbol with at least one
// active rule (only userID's when userID != 0), capped at maxUniqueSymbols.
func (w *AssetWorker) getUniqueActiveSymbols(userID uint64) ([]SymbolInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	query := `
		SELECT asset_symbol, MIN(asset_type), MAX(rule_type = 'week52_break') AS needs_week52
		FROM asset_rules
		WHERE is_active = true`
	args := []interface{}{}
	if userID != 0 {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	query += ` GROUP BY asset_symbol LIMIT ?`
	args = append(args, w.maxUniqueSymbols)

	rows, err := w.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query unique active symbols: %w", err)
	}
	defer rows.Close()

	var symbols []SymbolInfo
	for rows.Next() {
		var s SymbolInfo
		if err := rows.Scan(&s.Symbol, &s.AssetType, &s.NeedsWeek52); err != nil {
			return nil, fmt.Errorf("scan symbol info: %w", err)
		}
		symbols = append(symbols, s)
	}
	return symbols, rows.Err()
}

// processSymbolSafe wraps processSymbol with panic recovery so a failure
// processing one symbol never stops the run for the remaining symbols.
func (w *AssetWorker) processSymbolSafe(symbol SymbolInfo, scope runScope, batches map[uint64]*UserAlertBatch) (res symbolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] processSymbolSafe: recovered from panic processing %s: %v", symbol.Symbol, r)
			err = fmt.Errorf("recovered from panic: %v", r)
		}
	}()
	return w.processSymbol(symbol, scope, batches)
}

// processSymbol fetches market data for symbol via the cache layer, loads
// its active rules, and evaluates each one — appending fired alerts to the
// owner's UserAlertBatch instead of sending anything directly.
func (w *AssetWorker) processSymbol(symbol SymbolInfo, scope runScope, batches map[uint64]*UserAlertBatch) (symbolResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	marketData, err := w.cache.GetMarketData(ctx, symbol.Symbol, symbol.AssetType)
	if err != nil {
		return symbolResult{}, fmt.Errorf("fetch market data for %s: %w", symbol.Symbol, err)
	}

	rows, err := w.loadRules(ctx, symbol.Symbol, scope.userID)
	if err != nil {
		return symbolResult{}, fmt.Errorf("load rules for %s: %w", symbol.Symbol, err)
	}
	if len(rows) == 0 {
		return symbolResult{}, nil
	}

	res := symbolResult{checked: len(rows)}
	market := rules.Market{
		PriceUSD:     marketData.PriceUSD,
		ChangePct24h: marketData.ChangePct24h,
		Week52High:   marketData.Week52HighUSD,
		Week52Low:    marketData.Week52LowUSD,
	}
	if symbol.NeedsWeek52 && symbol.AssetType == "crypto" {
		high, low, wErr := w.cache.GetCryptoWeek52(ctx, symbol.Symbol)
		if wErr != nil {
			res.dataErrors++
			log.Printf("[ERROR] processSymbol: 52-week range for %s: %v", symbol.Symbol, wErr)
		} else {
			market.Week52High, market.Week52Low = &high, &low
		}
	}

	priceIDR := marketData.PriceIDR
	if priceIDR == 0 {
		if rate, rateErr := currency.GetUSDToIDR(); rateErr == nil {
			priceIDR = marketData.PriceUSD * rate
		} else {
			log.Printf("[ERROR] processSymbol: get USD/IDR rate for %s: %v", symbol.Symbol, rateErr)
		}
	}

	res.triggered = w.evaluateRules(ctx, rows, symbol.Symbol, market, priceIDR, scope, batches)
	return res, nil
}

// evaluateRules evaluates every row against market, persists each rule's
// Observed state, and appends fired rules to their owner's batch. batches
// is created once per run, so alerts from different symbols for the same
// user accumulate into one message.
func (w *AssetWorker) evaluateRules(
	ctx context.Context,
	rows []ruleRow,
	symbol string,
	market rules.Market,
	priceIDR float64,
	scope runScope,
	batches map[uint64]*UserAlertBatch,
) int {
	now := time.Now()
	triggered := 0
	for _, row := range rows {
		out, err := evaluateRuleSafe(row.Rule, market, now, rules.Options{
			SkipCooldown:      scope.skipCooldown,
			AlertPriceMovePct: w.alertPriceMovePct,
			CooldownHours:     row.AlertCooldownHours,
		})
		if err != nil {
			log.Printf("[ERROR] evaluateRules: rule %d (%s): %v", row.Rule.ID, symbol, err)
			continue
		}
		if err := w.applyPatch(ctx, row.Rule.ID, row.ParamsVersion, out.Observed); err != nil {
			log.Printf("[ERROR] evaluateRules: persist state for rule %d: %v", row.Rule.ID, err)
		}
		if !out.Fire {
			continue
		}

		batch, exists := batches[row.UserID]
		if !exists {
			batch = &UserAlertBatch{UserID: row.UserID, ChatID: row.TelegramAssetChatID, Language: row.PreferredLanguage}
			batches[row.UserID] = batch
		}
		batch.Alerts = append(batch.Alerts, AlertItem{
			RuleID:        row.Rule.ID,
			ParamsVersion: row.ParamsVersion,
			Symbol:        symbol,
			PriceUSD:      market.PriceUSD,
			PriceIDR:      priceIDR,
			ChangePct:     market.ChangePct24h,
			Trigger:       out.Trigger,
			OnSent:        out.OnSent,
		})
		triggered++
	}
	return triggered
}

// evaluateRuleSafe keeps one bad rule from stopping the others.
func evaluateRuleSafe(r rules.Rule, m rules.Market, now time.Time, opt rules.Options) (out rules.Outcome, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("recovered from panic: %v", rec)
		}
	}()
	return rules.Evaluate(r, m, now, opt), nil
}

// dispatchBatches sends one combined Telegram message per user (split only
// past Telegram's 4096-character limit — see buildBatchedMessages), applies
// OnSent for every delivered alert, and records one notification_logs
// entry per user for the run.
func (w *AssetWorker) dispatchBatches(batches map[uint64]*UserAlertBatch) (alertsSent, usersNotified int) {
	if len(batches) == 0 {
		return 0, 0
	}

	timestamp := time.Now().In(time.FixedZone("WIB", wibOffsetSeconds)).Format("02 Jan 2006 15:04") + " WIB"
	timestampEscaped := notifier.EscapeTelegramMarkdown(timestamp)

	for _, batch := range batches {
		if len(batch.Alerts) == 0 {
			continue
		}
		if w.dispatchUserBatch(batch, timestampEscaped) {
			alertsSent += len(batch.Alerts)
		}
		usersNotified++
	}
	return alertsSent, usersNotified
}

// dispatchUserBatch sends batch, then — only if every message part was
// delivered — applies each alert's OnSent state. A failed send leaves the
// rules untouched, so the next run fires them again.
func (w *AssetWorker) dispatchUserBatch(batch *UserAlertBatch, timestampEscaped string) bool {
	blocks := make([]string, 0, len(batch.Alerts))
	for _, alert := range batch.Alerts {
		blocks = append(blocks, buildAssetItemBlock(batch.Language, alert))
	}

	messages := buildBatchedMessages("🔔 *WATCHTOWER ASSET ALERT*", timestampEscaped, blocks)

	allSent := true
	for _, msg := range messages {
		if err := w.notifier.SendAssetAlert(batch.ChatID, msg); err != nil {
			allSent = false
			log.Printf("[ERROR] dispatchUserBatch: send telegram alert to user %d failed: %v", batch.UserID, err)
			break
		}
		// Telegram rate limit: stay under ~30 messages/second.
		time.Sleep(telegramSendGap)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	status := notifier.StatusSent
	if !allSent {
		status = notifier.StatusFailed
	} else {
		for _, alert := range batch.Alerts {
			if err := w.applyPatch(ctx, alert.RuleID, alert.ParamsVersion, alert.OnSent); err != nil {
				log.Printf("[ERROR] dispatchUserBatch: persist sent state for rule %d: %v", alert.RuleID, err)
			}
		}
	}

	seen := make(map[string]bool)
	symbols := make([]string, 0, len(batch.Alerts))
	for _, alert := range batch.Alerts {
		if !seen[alert.Symbol] {
			seen[alert.Symbol] = true
			symbols = append(symbols, alert.Symbol)
		}
	}

	if err := w.notifLogs.Record(ctx, notifier.LogEntry{
		UserID:         batch.UserID,
		NotifType:      notifier.TypeAsset,
		ContentSummary: strings.Join(symbols, ", ") + " alert triggered",
		Status:         status,
	}); err != nil {
		log.Printf("[ERROR] dispatchUserBatch: log notification for user %d failed: %v", batch.UserID, err)
	}

	return allSent
}

// buildAssetItemBlock formats one fired rule as a 4-line block in the
// user's language ('en' for English, anything else — including the 'id'
// default — for Indonesian). Every dynamic field is escaped for Telegram's
// MarkdownV2 via notifier.EscapeTelegramMarkdown; the "*" bold markers and
// the escaped "(", ")" around the IDR amount are static template text.
func buildAssetItemBlock(lang string, item AlertItem) string {
	symbol := notifier.EscapeTelegramMarkdown(item.Symbol)
	priceUSDText := notifier.EscapeTelegramMarkdown(formatUSD(item.PriceUSD))
	priceIDRText := notifier.EscapeTelegramMarkdown(formatIDR(item.PriceIDR))

	changeText := "n/a"
	if item.ChangePct != nil {
		changeText = fmt.Sprintf("%.2f%%", *item.ChangePct)
	}

	priceLabel := "💰 Harga"
	if lang == "en" {
		priceLabel = "💰 Price"
	}

	return fmt.Sprintf(
		"📊 *%s*\n%s: %s \\(%s\\)\n📈 24h: %s\n⚠️ Trigger: %s",
		symbol, priceLabel, priceUSDText, priceIDRText,
		notifier.EscapeTelegramMarkdown(changeText),
		notifier.EscapeTelegramMarkdown(triggerLine(lang, item.Trigger)),
	)
}

// triggerLine describes why a rule fired. Intent wording repeats the
// user's own plan; it is never advice (spec §6.4).
func triggerLine(lang string, t rules.Trigger) string {
	en := lang == "en"
	pick := func(enText, idText string) string {
		if en {
			return enText
		}
		return idText
	}
	level := formatUSD(t.LevelUSD)
	pct := formatPct(t.Pct)

	switch t.Type {
	case rules.TypePriceLevel:
		switch t.Intent {
		case rules.IntentBuyTarget:
			return pick("Reached your buy target ("+level+")", "Mencapai buy target kamu ("+level+")")
		case rules.IntentTakeProfit:
			return pick("Reached your take-profit level ("+level+")", "Mencapai level take-profit kamu ("+level+")")
		case rules.IntentStopLoss:
			return pick("Reached your stop-loss level ("+level+")", "Mencapai level stop-loss kamu ("+level+")")
		}
		if t.Direction == rules.DirectionAbove {
			return pick("Above "+level, "Di atas "+level)
		}
		return pick("Below "+level, "Di bawah "+level)
	case rules.TypePctChange24h:
		return pick("24h change beyond ±"+pct, "Perubahan 24 jam melewati ±"+pct)
	case rules.TypeTrailingStop:
		peak := formatUSD(t.PeakPriceUSD)
		return pick("Down "+pct+" or more from peak "+peak, "Turun "+pct+" atau lebih dari peak "+peak)
	case rules.TypeWeek52Break:
		prev := formatUSD(t.PrevRefUSD)
		if t.Direction == rules.DirectionBelow {
			return pick("New 52-week low (previous "+prev+")", "52-week low baru (sebelumnya "+prev+")")
		}
		return pick("New 52-week high (previous "+prev+")", "52-week high baru (sebelumnya "+prev+")")
	}
	return string(t.Type)
}

// formatPct renders a threshold without trailing zeros: 5 -> "5%", 2.5 -> "2.5%".
func formatPct(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64) + "%"
}
