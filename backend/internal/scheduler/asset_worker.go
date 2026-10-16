// Package scheduler ties the asset and sentinel pipelines' fetchers,
// analyzers, and notifier together into periodic jobs.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/karvin-nanda/watchtower/internal/alertmsg"
	"github.com/karvin-nanda/watchtower/internal/asset"
	"github.com/karvin-nanda/watchtower/internal/cache"
	"github.com/karvin-nanda/watchtower/internal/currency"
	"github.com/karvin-nanda/watchtower/internal/notifier"
	"github.com/karvin-nanda/watchtower/internal/quota"
	"github.com/karvin-nanda/watchtower/internal/rules"
)

const (
	dbQueryTimeout  = 30 * time.Second
	telegramSendGap = 50 * time.Millisecond
)

// NotifierInterface is the subset of notifier.Notifier's behavior
// AssetWorker depends on, allowing tests to inject a mock sender.
type NotifierInterface interface {
	SendAssetAlert(chatID int64, message string) error
}

// marketSource is the subset of cache.Cache the worker reads from,
// allowing tests to inject fake market data.
type marketSource interface {
	GetMarketData(ctx context.Context, symbol, assetType string, opts cache.FetchOptions) (*cache.MarketData, error)
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

	// loc is the instance timezone (APP_TIMEZONE) for message timestamps.
	loc *time.Location
	// rateFn returns the USD/IDR rate; currency.Current outside tests.
	rateFn func() currency.Rate
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
	loc *time.Location,
	manualDailyCredits int,
) *AssetWorker {
	return &AssetWorker{
		db:                db,
		redis:             redisClient,
		notifier:          notif,
		cache:             cache.New(redisClient, db, cacheTTLHours, fetcher, quota.NewBudget(redisClient, manualDailyCredits)),
		notifLogs:         notifier.NewLogRepository(db),
		maxUniqueSymbols:  maxUniqueSymbols,
		alertPriceMovePct: alertPriceMovePct,
		loc:               loc,
		rateFn:            currency.Current,
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
	// NativeIDR is the source's own IDR price (gold only); 0 otherwise.
	NativeIDR float64
	ChangePct *float64
	Trigger   rules.Trigger
	OnSent    rules.Patch
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
	// Stale counts symbols evaluated on cached data because a manual
	// refresh was refused or failed.
	Stale int
	// IDRWaiting counts IDR price levels skipped because the fetch had no
	// real IDR price (#41, #61).
	IDRWaiting int
}

// symbolResult is processSymbol's per-symbol tally. dataErrors counts
// optional data that could not be fetched (crypto 52-week range) while the
// symbol's other rules were still evaluated. stale counts a symbol
// evaluated on cached data (manual runs only). idrWaiting counts IDR price
// levels that wait for a real USD/IDR rate.
type symbolResult struct {
	checked, triggered, dataErrors, stale, idrWaiting int
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
		result.Stale += res.stale
		result.IDRWaiting += res.idrWaiting
	}

	alertsSent, usersNotified := w.dispatchBatches(batches)
	result.Sent = alertsSent

	log.Printf("[INFO] AssetWorker.run: completed in %s (user=%d) — symbols: %d, rules checked: %d, "+
		"triggered: %d, users notified: %d, alerts sent: %d, errors: %d, stale: %d, idr waiting: %d",
		time.Since(start), scope.userID, len(symbols), result.RulesChecked, result.Triggered,
		usersNotified, result.Sent, result.Errors, result.Stale, result.IDRWaiting)

	return result, nil
}

// getUniqueActiveSymbols returns every distinct symbol with at least one
// active rule (only userID's when userID != 0), capped at maxUniqueSymbols.
// Past the cap it keeps the symbols whose earliest active rule is oldest,
// so the cut is deterministic, and logs the skipped ones (#56).
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
	query += `
		GROUP BY asset_symbol
		ORDER BY MIN(created_at), asset_symbol`

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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(symbols) > w.maxUniqueSymbols {
		skipped := make([]string, 0, len(symbols)-w.maxUniqueSymbols)
		for _, s := range symbols[w.maxUniqueSymbols:] {
			skipped = append(skipped, s.Symbol)
		}
		log.Printf("[WARN] AssetWorker: %d active symbol(s) over MAX_UNIQUE_SYMBOLS=%d are not checked: %s",
			len(skipped), w.maxUniqueSymbols, strings.Join(skipped, ", "))
		symbols = symbols[:w.maxUniqueSymbols]
	}
	return symbols, nil
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

// manualFetch is what a user's Check now or status report asks the cache
// for: prices at most cache.ManualMaxAge old (#20).
var manualFetch = cache.FetchOptions{MaxAge: cache.ManualMaxAge, Manual: true}

// marketFromData is the engine's view of one fetch. IDR price levels (#40)
// compare against price_idr from the same fetch; 0 means no real rate was
// known then (#41), so it stays unknown.
func marketFromData(d *cache.MarketData) rules.Market {
	m := rules.Market{
		PriceUSD:     d.PriceUSD,
		ChangePct24h: d.ChangePct24h,
		Week52High:   d.Week52HighUSD,
		Week52Low:    d.Week52LowUSD,
		HighUSD:      d.HighUSD,
		HighSince:    d.HighSince,
	}
	if d.PriceIDR > 0 {
		idr := d.PriceIDR
		m.PriceIDR = &idr
	}
	return m
}

// nativeIDRFor is the source's own IDR price: only gold is quoted in IDR.
// Every other IDR amount is converted at send time with one rate.
func nativeIDRFor(assetType string, d *cache.MarketData) float64 {
	if assetType == "gold" {
		return d.PriceIDR
	}
	return 0
}

// countIDRWaiting counts the IDR price levels the engine will skip because
// the fetch had no real IDR price (#41), and says so in the log once per
// symbol (#61), so a missing rate is not silent.
func countIDRWaiting(symbol string, rows []ruleRow, market rules.Market) int {
	if market.PriceIDR != nil {
		return 0
	}
	var ids []uint64
	for _, row := range rows {
		if row.Rule.Type == rules.TypePriceLevel && row.Rule.Currency == rules.CurrencyIDR {
			ids = append(ids, row.Rule.ID)
		}
	}
	if len(ids) > 0 {
		log.Printf("[WARN] processSymbol: %s has no real IDR price; IDR rules %v wait for a USD/IDR rate", symbol, ids)
	}
	return len(ids)
}

// processSymbol fetches market data for symbol via the cache layer, loads
// its active rules, and evaluates each one — appending fired alerts to the
// owner's UserAlertBatch instead of sending anything directly.
func (w *AssetWorker) processSymbol(symbol SymbolInfo, scope runScope, batches map[uint64]*UserAlertBatch) (symbolResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	// Periodic runs accept the cache TTL; a user's Check now wants prices
	// at most cache.ManualMaxAge old (#20).
	opts := cache.FetchOptions{}
	if scope.userID != 0 {
		opts = manualFetch
	}
	marketData, err := w.cache.GetMarketData(ctx, symbol.Symbol, symbol.AssetType, opts)
	if err != nil {
		return symbolResult{}, fmt.Errorf("fetch market data for %s: %w", symbol.Symbol, err)
	}
	// A missing price (e.g. CoinGecko "current_price": null decodes to 0)
	// would fire every armed trailing stop and "below" level.
	if marketData.PriceUSD <= 0 {
		return symbolResult{}, fmt.Errorf("invalid price %v for %s", marketData.PriceUSD, symbol.Symbol)
	}

	rows, err := w.loadRules(ctx, symbol.Symbol, scope.userID)
	if err != nil {
		return symbolResult{}, fmt.Errorf("load rules for %s: %w", symbol.Symbol, err)
	}
	markGoldTrailing(rows, symbol.AssetType)
	if len(rows) == 0 {
		return symbolResult{}, nil
	}

	res := symbolResult{checked: len(rows)}
	if marketData.Stale {
		res.stale = 1
	}
	market := marketFromData(marketData)
	res.idrWaiting = countIDRWaiting(symbol.Symbol, rows, market)
	if symbol.NeedsWeek52 && symbol.AssetType == "crypto" {
		high, low, wErr := w.cache.GetCryptoWeek52(ctx, symbol.Symbol)
		if wErr != nil {
			res.dataErrors++
			log.Printf("[ERROR] processSymbol: 52-week range for %s: %v", symbol.Symbol, wErr)
		} else {
			market.Week52High, market.Week52Low = &high, &low
		}
	}

	nativeIDR := nativeIDRFor(symbol.AssetType, marketData)

	res.triggered = w.evaluateRules(ctx, rows, symbol.Symbol, market, nativeIDR, scope, batches)
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
	nativeIDR float64,
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
			NativeIDR:     nativeIDR,
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
// past Telegram's 4096-character limit — see alertmsg.AssetMessages), applies
// OnSent for every delivered alert, and records one notification_logs
// entry per user for the run.
func (w *AssetWorker) dispatchBatches(batches map[uint64]*UserAlertBatch) (alertsSent, usersNotified int) {
	if len(batches) == 0 {
		return 0, 0
	}

	at := time.Now().In(w.loc)
	rate := w.messageRate()

	for _, batch := range batches {
		if len(batch.Alerts) == 0 {
			continue
		}
		if w.dispatchUserBatch(batch, at, rate) {
			alertsSent += len(batch.Alerts)
		}
		usersNotified++
	}
	return alertsSent, usersNotified
}

// messageRate is the one USD/IDR rate used for every message of a run.
// The currency package's hardcoded fallback is never shown: it becomes
// "rate unavailable" and IDR amounts are hidden.
func (w *AssetWorker) messageRate() alertmsg.Rate {
	r := w.rateFn()
	if r.Fallback {
		log.Printf("[WARN] AssetWorker: no real USD/IDR rate available; IDR amounts are hidden in this run's messages")
		return alertmsg.Rate{}
	}
	return alertmsg.Rate{Value: r.Value, UpdatedAt: r.UpdatedAt}
}

// dispatchUserBatch sends batch, then — only if every message part was
// delivered — applies each alert's OnSent state. A failed send leaves the
// rules untouched, so the next run fires them again.
func (w *AssetWorker) dispatchUserBatch(batch *UserAlertBatch, at time.Time, rate alertmsg.Rate) bool {
	alerts := make([]alertmsg.AssetAlert, 0, len(batch.Alerts))
	for _, a := range batch.Alerts {
		alerts = append(alerts, alertmsg.AssetAlert{
			RuleID: a.RuleID, Symbol: a.Symbol, PriceUSD: a.PriceUSD,
			NativeIDR: a.NativeIDR, ChangePct: a.ChangePct, Trigger: a.Trigger,
		})
	}
	messages := alertmsg.AssetMessages(batch.Language, at, rate, alerts)

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
