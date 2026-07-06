// Package scheduler ties the asset and sentinel pipelines' fetchers,
// analyzers, and notifier together into periodic jobs.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/karvin-nanda/watchtower/internal/asset"
	"github.com/karvin-nanda/watchtower/internal/cache"
	"github.com/karvin-nanda/watchtower/internal/currency"
	"github.com/karvin-nanda/watchtower/internal/notifier"
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

// AssetAnalyzerInterface is the subset of asset.DeepSeekAnalyzer's behavior
// AssetWorker depends on, allowing tests to inject a mock analyzer.
type AssetAnalyzerInterface interface {
	AnalyzeAsset(data *asset.FetchResult, subscribers []asset.SubscriberContext, usdToIDR float64) (*asset.AnalysisResult, error)
}

// AssetWorker periodically fetches market data (via the Redis/MySQL cache
// layer), evaluates every active asset subscription for alert conditions,
// and notifies subscribers whose thresholds are breached. Every triggered
// alert for a given user within one run is batched into a single Telegram
// message (see UserAlertBatch) rather than sent as separate messages per
// symbol, to avoid spamming a user who subscribes to several assets that
// trigger in the same run.
type AssetWorker struct {
	db       *sql.DB
	redis    *redis.Client
	notifier NotifierInterface
	analyzer AssetAnalyzerInterface

	cache             *cache.Cache
	notifLogs         *notifier.LogRepository
	maxUniqueSymbols  int
	alertPriceMovePct float64
	interval          time.Duration
}

// NewAssetWorker wires together every dependency needed to run the asset
// price alert pipeline. fetcher is used internally to build the market data
// cache layer; cacheTTLHours should be ASSET_SCHEDULER_INTERVAL_HOURS.
func NewAssetWorker(
	db *sql.DB,
	redisClient *redis.Client,
	fetcher *asset.AssetFetcher,
	notif NotifierInterface,
	analyzer AssetAnalyzerInterface,
	cacheTTLHours int,
	maxUniqueSymbols int,
	alertPriceMovePct float64,
	interval time.Duration,
) *AssetWorker {
	return &AssetWorker{
		db:                db,
		redis:             redisClient,
		notifier:          notif,
		analyzer:          analyzer,
		cache:             cache.New(redisClient, db, cacheTTLHours, fetcher),
		notifLogs:         notifier.NewLogRepository(db),
		maxUniqueSymbols:  maxUniqueSymbols,
		alertPriceMovePct: alertPriceMovePct,
		interval:          interval,
	}
}

// SymbolInfo identifies a distinct subscribed asset symbol and its type.
type SymbolInfo struct {
	Symbol    string
	AssetType string
}

// SubscriberData is one user's subscription to a symbol, joined with their
// notification preferences and their current alert cooldown state.
type SubscriberData struct {
	UserID              uint64
	AlertType           string
	PriceLowerUSD       *float64
	PriceUpperUSD       *float64
	PctChangeThreshold  *float64
	TelegramAssetChatID int64
	AlertCooldownHours  int
	PreferredLanguage   string
	LastAlertType       *string
	LastAlertedPriceUSD *float64
	CooldownUntil       *time.Time
}

// AlertItem is one triggered symbol alert for a single user, collected
// during a run and rendered as one section of that user's batched message.
type AlertItem struct {
	Symbol      string
	PriceUSD    float64
	PriceIDR    float64
	ChangePct   float64
	TriggerType string
	AnalysisID  string
	AnalysisEN  string
}

// UserAlertBatch accumulates every AlertItem triggered for one user during
// a single AssetWorker.Run, so they can be delivered as one combined
// Telegram message instead of one message per symbol.
type UserAlertBatch struct {
	UserID   uint64
	ChatID   int64
	Language string
	Alerts   []AlertItem
}

// Run is the scheduler entry point: it evaluates every distinct active
// symbol, collecting triggered alerts per user, then sends each user at
// most one combined message for the run. It never panics — any error or
// recovered panic is logged and does not stop the run for other symbols.
func (w *AssetWorker) Run() (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] AssetWorker.Run: recovered from panic: %v", r)
			err = fmt.Errorf("scheduler: recovered from panic: %v", r)
		}
	}()

	start := time.Now()
	log.Printf("[INFO] AssetWorker.Run: starting run at %s", start.Format(time.RFC3339))

	symbols, err := w.getUniqueActiveSymbols()
	if err != nil {
		log.Printf("[ERROR] AssetWorker.Run: get unique active symbols: %v", err)
		return fmt.Errorf("scheduler: get unique active symbols: %w", err)
	}

	batches := make(map[uint64]*UserAlertBatch)
	cooldownHoursByUser := make(map[uint64]int)

	var totalTriggered, totalErrors int
	for _, symbol := range symbols {
		triggered, procErr := w.processSymbolSafe(symbol, batches, cooldownHoursByUser)
		if procErr != nil {
			totalErrors++
			log.Printf("[ERROR] AssetWorker.Run: process symbol %s failed: %v", symbol.Symbol, procErr)
			continue
		}
		totalTriggered += triggered
	}

	alertsSent, usersNotified := w.dispatchBatches(batches, cooldownHoursByUser)

	log.Printf("[INFO] AssetWorker.Run: completed in %s — symbols processed: %d, alerts triggered: %d, "+
		"users notified: %d, alerts sent: %d, errors: %d",
		time.Since(start), len(symbols), totalTriggered, usersNotified, alertsSent, totalErrors)

	return nil
}

// Start runs Run() immediately, then again on every tick of the worker's
// configured interval, until ctx is cancelled — at which point it returns,
// allowing the caller to shut down gracefully.
func (w *AssetWorker) Start(ctx context.Context) {
	log.Printf("[INFO] AssetWorker.Start: running immediately, then every %s", w.interval)

	if err := w.Run(); err != nil {
		log.Printf("[ERROR] AssetWorker.Start: initial run failed: %v", err)
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[INFO] AssetWorker.Start: context cancelled, stopping gracefully")
			return
		case <-ticker.C:
			if err := w.Run(); err != nil {
				log.Printf("[ERROR] AssetWorker.Start: run failed: %v", err)
			}
		}
	}
}

// getUniqueActiveSymbols returns every distinct symbol with at least one
// active subscription, capped at maxUniqueSymbols.
func (w *AssetWorker) getUniqueActiveSymbols() ([]SymbolInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	rows, err := w.db.QueryContext(ctx, `
		SELECT DISTINCT asset_symbol, asset_type
		FROM asset_subscriptions
		WHERE is_active = true
		LIMIT ?`, w.maxUniqueSymbols)
	if err != nil {
		return nil, fmt.Errorf("query unique active symbols: %w", err)
	}
	defer rows.Close()

	var symbols []SymbolInfo
	for rows.Next() {
		var s SymbolInfo
		if err := rows.Scan(&s.Symbol, &s.AssetType); err != nil {
			return nil, fmt.Errorf("scan symbol info: %w", err)
		}
		symbols = append(symbols, s)
	}
	return symbols, rows.Err()
}

// processSymbolSafe wraps processSymbol with panic recovery so a failure
// processing one symbol never stops the run for the remaining symbols.
func (w *AssetWorker) processSymbolSafe(
	symbol SymbolInfo,
	batches map[uint64]*UserAlertBatch,
	cooldownHoursByUser map[uint64]int,
) (alertsTriggered int, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] processSymbolSafe: recovered from panic processing %s: %v", symbol.Symbol, r)
			err = fmt.Errorf("recovered from panic: %v", r)
		}
	}()
	return w.processSymbol(symbol, batches, cooldownHoursByUser)
}

// processSymbol fetches fresh market data for symbol via the cache layer,
// loads every active subscriber, runs one shared DeepSeek analysis for the
// symbol, and evaluates each subscriber's alert conditions — appending a
// triggered alert to that subscriber's UserAlertBatch in batches rather
// than sending anything directly. Actual delivery happens once per user
// after every symbol has been processed, via dispatchBatches.
func (w *AssetWorker) processSymbol(
	symbol SymbolInfo,
	batches map[uint64]*UserAlertBatch,
	cooldownHoursByUser map[uint64]int,
) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	marketData, err := w.cache.GetMarketData(ctx, symbol.Symbol, symbol.AssetType)
	if err != nil {
		return 0, fmt.Errorf("fetch market data for %s: %w", symbol.Symbol, err)
	}

	subscribers, err := w.loadSubscribers(ctx, symbol.Symbol)
	if err != nil {
		return 0, fmt.Errorf("load subscribers for %s: %w", symbol.Symbol, err)
	}
	if len(subscribers) == 0 {
		return 0, nil
	}

	fetchResult := &asset.FetchResult{
		Symbol:       marketData.Symbol,
		PriceUSD:     marketData.PriceUSD,
		ChangePct24h: marketData.ChangePct24h,
		Source:       marketData.Source,
		FetchedAt:    marketData.LastFetched,
	}

	subscriberContexts := make([]asset.SubscriberContext, 0, len(subscribers))
	for _, s := range subscribers {
		subscriberContexts = append(subscriberContexts, asset.SubscriberContext{
			AlertType:          s.AlertType,
			PriceLowerUSD:      s.PriceLowerUSD,
			PriceUpperUSD:      s.PriceUpperUSD,
			PctChangeThreshold: s.PctChangeThreshold,
		})
	}

	usdToIDR, rateErr := currency.GetUSDToIDR()
	if rateErr != nil {
		log.Printf("[ERROR] processSymbol: get USD/IDR rate for %s: %v", symbol.Symbol, rateErr)
	}

	analysis, err := w.analyzer.AnalyzeAsset(fetchResult, subscriberContexts, usdToIDR)
	if err != nil {
		log.Printf("[WARN] processSymbol: AnalyzeAsset failed for %s, alerting without AI commentary: %v", symbol.Symbol, err)
		analysis = nil
	}

	priceIDR := fetchResult.PriceUSD * usdToIDR

	triggered := 0
	for _, sub := range subscribers {
		item, ok, evalErr := w.evaluateSubscriberSafe(sub, fetchResult, priceIDR, analysis)
		if evalErr != nil {
			log.Printf("[ERROR] processSymbol: evaluate alert for user %d symbol %s failed: %v", sub.UserID, symbol.Symbol, evalErr)
			continue
		}
		if !ok {
			continue
		}

		cooldownHoursByUser[sub.UserID] = sub.AlertCooldownHours

		batch, exists := batches[sub.UserID]
		if !exists {
			batch = &UserAlertBatch{
				UserID:   sub.UserID,
				ChatID:   sub.TelegramAssetChatID,
				Language: sub.PreferredLanguage,
			}
			batches[sub.UserID] = batch
		}

		// asset_subscriptions has no uniqueness constraint on
		// (user_id, asset_symbol) — a user can end up with two active
		// subscription rows for the same symbol (e.g. subscribing twice
		// from the UI), so loadSubscribers can return the same user twice
		// for this symbol, each independently triggering here. Without
		// this guard that produces two AlertItems for the same symbol in
		// one user's batch, i.e. the symbol appears twice in a single
		// Telegram message.
		alreadyInBatch := false
		for _, existing := range batch.Alerts {
			if existing.Symbol == item.Symbol {
				alreadyInBatch = true
				break
			}
		}
		if alreadyInBatch {
			continue
		}

		batch.Alerts = append(batch.Alerts, item)
		triggered++
	}

	return triggered, nil
}

// loadSubscribers returns every active subscriber for symbol who has an
// active account with a Telegram asset chat ID configured, joined with
// their most recent alert_states row if one exists.
func (w *AssetWorker) loadSubscribers(ctx context.Context, symbol string) ([]SubscriberData, error) {
	rows, err := w.db.QueryContext(ctx, `
		SELECT
			sub.user_id,
			sub.alert_type,
			sub.price_lower_usd,
			sub.price_upper_usd,
			sub.pct_change_threshold,
			u.telegram_asset_chat_id,
			u.alert_cooldown_hours,
			u.preferred_language,
			al.last_alert_type,
			al.last_alerted_price_usd,
			al.cooldown_until
		FROM asset_subscriptions sub
		JOIN users u ON u.id = sub.user_id
		LEFT JOIN alert_states al
			ON al.user_id = sub.user_id
			AND al.asset_symbol = sub.asset_symbol
		WHERE sub.asset_symbol = ?
			AND sub.is_active = true
			AND u.is_active = true
			AND u.telegram_asset_chat_id IS NOT NULL`, symbol)
	if err != nil {
		return nil, fmt.Errorf("query subscribers for %s: %w", symbol, err)
	}
	defer rows.Close()

	var subscribers []SubscriberData
	for rows.Next() {
		var (
			s                  SubscriberData
			priceLower         sql.NullFloat64
			priceUpper         sql.NullFloat64
			pctChangeThreshold sql.NullFloat64
			lastAlertType      sql.NullString
			lastAlertedPrice   sql.NullFloat64
			cooldownUntil      sql.NullTime
		)

		if err := rows.Scan(
			&s.UserID, &s.AlertType, &priceLower, &priceUpper, &pctChangeThreshold,
			&s.TelegramAssetChatID, &s.AlertCooldownHours, &s.PreferredLanguage,
			&lastAlertType, &lastAlertedPrice, &cooldownUntil,
		); err != nil {
			return nil, fmt.Errorf("scan subscriber row for %s: %w", symbol, err)
		}

		if priceLower.Valid {
			s.PriceLowerUSD = &priceLower.Float64
		}
		if priceUpper.Valid {
			s.PriceUpperUSD = &priceUpper.Float64
		}
		if pctChangeThreshold.Valid {
			s.PctChangeThreshold = &pctChangeThreshold.Float64
		}
		if lastAlertType.Valid {
			s.LastAlertType = &lastAlertType.String
		}
		if lastAlertedPrice.Valid {
			s.LastAlertedPriceUSD = &lastAlertedPrice.Float64
		}
		if cooldownUntil.Valid {
			s.CooldownUntil = &cooldownUntil.Time
		}

		subscribers = append(subscribers, s)
	}

	return subscribers, rows.Err()
}

// evaluateSubscriberSafe wraps evaluateSubscriber with panic recovery so a
// failure evaluating one subscriber never stops evaluation of the rest.
func (w *AssetWorker) evaluateSubscriberSafe(
	subscriber SubscriberData, fetchResult *asset.FetchResult, priceIDR float64, analysis *asset.AnalysisResult,
) (item AlertItem, triggered bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] evaluateSubscriberSafe: recovered from panic for user %d: %v", subscriber.UserID, r)
			err = fmt.Errorf("recovered from panic: %v", r)
		}
	}()
	return w.evaluateSubscriber(subscriber, fetchResult, priceIDR, analysis)
}

// evaluateSubscriber implements the cooldown + threshold evaluation for a
// single subscriber against fetchResult:
//
//  1. If the subscriber is still within cooldown AND the price hasn't
//     moved by at least ALERT_PRICE_MOVE_PCT since the last alert, skip.
//  2. Otherwise evaluate the subscription's threshold(s); if none are
//     breached, skip.
//  3. Return the AlertItem to be appended to this subscriber's batch —
//     delivery and cooldown persistence happen later, once per user, in
//     dispatchBatches.
func (w *AssetWorker) evaluateSubscriber(
	subscriber SubscriberData, fetchResult *asset.FetchResult, priceIDR float64, analysis *asset.AnalysisResult,
) (AlertItem, bool, error) {
	if subscriber.CooldownUntil != nil && time.Now().Before(*subscriber.CooldownUntil) {
		priceMovePct := 0.0
		if subscriber.LastAlertedPriceUSD != nil && *subscriber.LastAlertedPriceUSD != 0 {
			priceMovePct = math.Abs(fetchResult.PriceUSD-*subscriber.LastAlertedPriceUSD) / *subscriber.LastAlertedPriceUSD * 100
		}
		if priceMovePct < w.alertPriceMovePct {
			return AlertItem{}, false, nil
		}
	}

	triggerType, triggered := evaluateThreshold(subscriber, fetchResult)
	if !triggered {
		return AlertItem{}, false, nil
	}

	item := AlertItem{
		Symbol:      fetchResult.Symbol,
		PriceUSD:    fetchResult.PriceUSD,
		PriceIDR:    priceIDR,
		ChangePct:   fetchResult.ChangePct24h,
		TriggerType: triggerType,
	}
	if analysis != nil {
		item.AnalysisID = analysis.AnalysisID
		item.AnalysisEN = analysis.AnalysisEN
	}

	return item, true, nil
}

// evaluateThreshold checks subscriber's alert_type against fetchResult and
// returns the triggered alert type ("lower", "upper", or "pct_change") and
// whether any threshold was actually breached.
func evaluateThreshold(subscriber SubscriberData, fetchResult *asset.FetchResult) (string, bool) {
	checkPriceThreshold := subscriber.AlertType == "price_threshold" || subscriber.AlertType == "both"
	checkPctChange := subscriber.AlertType == "pct_change" || subscriber.AlertType == "both"

	if checkPriceThreshold {
		if subscriber.PriceLowerUSD != nil && fetchResult.PriceUSD < *subscriber.PriceLowerUSD {
			return "lower", true
		}
		if subscriber.PriceUpperUSD != nil && fetchResult.PriceUSD > *subscriber.PriceUpperUSD {
			return "upper", true
		}
	}

	if checkPctChange && subscriber.PctChangeThreshold != nil {
		if math.Abs(fetchResult.ChangePct24h) > *subscriber.PctChangeThreshold {
			return "pct_change", true
		}
	}

	return "", false
}

// dispatchBatches sends one combined Telegram message per user in batches
// (splitting into multiple messages only if the combined text would exceed
// Telegram's 4096-character limit — see buildBatchedMessages), persists
// alert_states for every alert successfully delivered, and records exactly
// one notification_logs entry per user for the run.
func (w *AssetWorker) dispatchBatches(batches map[uint64]*UserAlertBatch, cooldownHoursByUser map[uint64]int) (alertsSent, usersNotified int) {
	if len(batches) == 0 {
		return 0, 0
	}

	now := time.Now()
	timestamp := now.In(time.FixedZone("WIB", wibOffsetSeconds)).Format("02 Jan 2006 15:04") + " WIB"
	timestampEscaped := notifier.EscapeTelegramMarkdown(timestamp)

	for _, batch := range batches {
		if len(batch.Alerts) == 0 {
			continue
		}
		w.dispatchUserBatch(batch, cooldownHoursByUser[batch.UserID], timestampEscaped, now)
		alertsSent += len(batch.Alerts)
		usersNotified++
	}

	return alertsSent, usersNotified
}

// dispatchUserBatch sends batch as one or more Telegram messages (per
// buildBatchedMessages), then — only if every message part sent
// successfully — upserts alert_states for each included alert so the next
// run's cooldown check has the correct baseline. Exactly one
// notification_logs row is recorded for the whole batch either way.
func (w *AssetWorker) dispatchUserBatch(batch *UserAlertBatch, cooldownHours int, timestampEscaped string, now time.Time) {
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
		cooldownUntil := now.Add(time.Duration(cooldownHours) * time.Hour)
		for _, alert := range batch.Alerts {
			if err := w.upsertAlertState(ctx, batch.UserID, alert.Symbol, alert.TriggerType, alert.PriceUSD, now, cooldownUntil); err != nil {
				log.Printf("[ERROR] dispatchUserBatch: upsert alert state for user %d symbol %s failed: %v", batch.UserID, alert.Symbol, err)
			}
		}
	}

	symbols := make([]string, 0, len(batch.Alerts))
	for _, alert := range batch.Alerts {
		symbols = append(symbols, alert.Symbol)
	}
	summary := strings.Join(symbols, ", ") + " alert triggered"

	if err := w.notifLogs.Record(ctx, notifier.LogEntry{
		UserID:         batch.UserID,
		NotifType:      notifier.TypeAsset,
		ContentSummary: summary,
		Status:         status,
	}); err != nil {
		log.Printf("[ERROR] dispatchUserBatch: log notification for user %d failed: %v", batch.UserID, err)
	}
}

// upsertAlertState records the alert just sent so the next run's cooldown
// check has the correct baseline.
func (w *AssetWorker) upsertAlertState(ctx context.Context, userID uint64, symbol, alertType string, price float64, alertedAt, cooldownUntil time.Time) error {
	_, err := w.db.ExecContext(ctx, `
		INSERT INTO alert_states (user_id, asset_symbol, last_alert_type, last_alerted_price_usd, last_alerted_at, cooldown_until)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			last_alert_type = VALUES(last_alert_type),
			last_alerted_price_usd = VALUES(last_alerted_price_usd),
			last_alerted_at = VALUES(last_alerted_at),
			cooldown_until = VALUES(cooldown_until)`,
		userID, symbol, alertType, price, alertedAt, cooldownUntil,
	)
	if err != nil {
		return fmt.Errorf("upsert alert_states: %w", err)
	}
	return nil
}

// buildAssetItemBlock formats a single triggered symbol's section within a
// batched alert message, in the subscriber's preferred language ('en' for
// English, anything else — including the 'id' default — for Indonesian).
// Every dynamic field is escaped for Telegram's MarkdownV2 parse mode via
// notifier.EscapeTelegramMarkdown, including the formatted price/percentage
// text — formatUSD/formatIDR's thousands-separator commas are harmless to
// escape (comma isn't a MarkdownV2 special character) but their decimal
// points are. The leading "*"/trailing bold marker around the symbol and
// the "(", ")" wrapping the IDR amount are static template characters, not
// dynamic data — they're hardcoded with their own required MarkdownV2
// backslash escapes directly in the format string below.
func buildAssetItemBlock(lang string, item AlertItem) string {
	symbol := notifier.EscapeTelegramMarkdown(item.Symbol)
	priceUSDText := notifier.EscapeTelegramMarkdown(formatUSD(item.PriceUSD))
	priceIDRText := notifier.EscapeTelegramMarkdown(formatIDR(item.PriceIDR))
	changePctText := notifier.EscapeTelegramMarkdown(fmt.Sprintf("%.2f", item.ChangePct))

	priceLabel := "💰 Harga"
	triggerLabel := triggerLabelID(item.TriggerType)
	analysisText := item.AnalysisID
	if lang == "en" {
		priceLabel = "💰 Price"
		triggerLabel = triggerLabelEN(item.TriggerType)
		analysisText = item.AnalysisEN
	}

	block := fmt.Sprintf(
		"📊 *%s*\n%s: %s \\(%s\\)\n📈 24h: %s%%\n⚠️ Trigger: %s",
		symbol, priceLabel, priceUSDText, priceIDRText, changePctText, notifier.EscapeTelegramMarkdown(triggerLabel),
	)

	// When AnalyzeAsset fails (DeepSeek down/timed out/rate-limited),
	// analysisText is empty and the alert still needs to go out — the
	// price/threshold breach is the important, time-sensitive part; the AI
	// commentary is a nice-to-have. Omit the AI line entirely instead of
	// leaving a blank line where it would have gone.
	if analysisText != "" {
		block += "\n" + notifier.EscapeTelegramMarkdown(analysisText)
	}

	return block
}

func triggerLabelID(triggerType string) string {
	switch triggerType {
	case "lower":
		return "Lower Bound"
	case "upper":
		return "Upper Bound"
	case "pct_change":
		return "Perubahan %"
	default:
		return triggerType
	}
}

func triggerLabelEN(triggerType string) string {
	switch triggerType {
	case "lower":
		return "Lower Bound"
	case "upper":
		return "Upper Bound"
	case "pct_change":
		return "% Change"
	default:
		return triggerType
	}
}
