// Package scheduler ties the asset and sentinel pipelines' fetchers,
// analyzers, and notifier together into periodic jobs.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
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
	AnalyzeAsset(data *asset.FetchResult, subscribers []asset.SubscriberContext) (*asset.AnalysisResult, error)
}

// AssetWorker periodically fetches market data (via the Redis/MySQL cache
// layer), evaluates every active asset subscription for alert conditions,
// and notifies subscribers whose thresholds are breached.
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

// Run is the scheduler entry point: it processes every distinct active
// symbol, sending alerts as needed, and never panics — any error or
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

	var totalAlerts, totalErrors int
	for _, symbol := range symbols {
		alerts, procErr := w.processSymbolSafe(symbol)
		if procErr != nil {
			totalErrors++
			log.Printf("[ERROR] AssetWorker.Run: process symbol %s failed: %v", symbol.Symbol, procErr)
			continue
		}
		totalAlerts += alerts
	}

	log.Printf("[INFO] AssetWorker.Run: completed in %s — symbols processed: %d, alerts sent: %d, errors: %d",
		time.Since(start), len(symbols), totalAlerts, totalErrors)

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
func (w *AssetWorker) processSymbolSafe(symbol SymbolInfo) (alertsSent int, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] processSymbolSafe: recovered from panic processing %s: %v", symbol.Symbol, r)
			err = fmt.Errorf("recovered from panic: %v", r)
		}
	}()
	return w.processSymbol(symbol)
}

// processSymbol fetches fresh market data for symbol via the cache layer,
// loads every active subscriber, runs one shared DeepSeek analysis for the
// symbol, and evaluates each subscriber's alert conditions.
func (w *AssetWorker) processSymbol(symbol SymbolInfo) (int, error) {
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

	analysis, err := w.analyzer.AnalyzeAsset(fetchResult, subscriberContexts)
	if err != nil {
		log.Printf("[WARN] processSymbol: AnalyzeAsset failed for %s, sending alert without AI commentary: %v", symbol.Symbol, err)
		analysis = nil
	}

	alertsSent := 0
	for _, sub := range subscribers {
		sent, evalErr := w.evaluateAndAlertSafe(sub, fetchResult, analysis)
		if evalErr != nil {
			log.Printf("[ERROR] processSymbol: evaluate alert for user %d symbol %s failed: %v", sub.UserID, symbol.Symbol, evalErr)
			continue
		}
		if sent {
			alertsSent++
			// Telegram rate limit: stay under ~30 messages/second.
			time.Sleep(telegramSendGap)
		}
	}

	return alertsSent, nil
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

// evaluateAndAlertSafe wraps evaluateAndAlert with panic recovery so a
// failure evaluating one subscriber never stops evaluation of the rest.
func (w *AssetWorker) evaluateAndAlertSafe(subscriber SubscriberData, fetchResult *asset.FetchResult, analysis *asset.AnalysisResult) (sent bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] evaluateAndAlertSafe: recovered from panic for user %d: %v", subscriber.UserID, r)
			err = fmt.Errorf("recovered from panic: %v", r)
		}
	}()
	return w.evaluateAndAlert(subscriber, fetchResult, analysis)
}

// evaluateAndAlert implements the full cooldown + threshold + notify +
// persist flow for a single subscriber:
//
//  1. If the subscriber is still within cooldown AND the price hasn't
//     moved by at least ALERT_PRICE_MOVE_PCT since the last alert, skip.
//  2. Otherwise evaluate the subscription's threshold(s); if none are
//     breached, skip.
//  3. Send the Telegram alert in the subscriber's preferred language.
//  4. On successful send, upsert alert_states (cooldown, last alerted
//     price/type/time) and record the notification either way.
func (w *AssetWorker) evaluateAndAlert(subscriber SubscriberData, fetchResult *asset.FetchResult, analysis *asset.AnalysisResult) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	if subscriber.CooldownUntil != nil && time.Now().Before(*subscriber.CooldownUntil) {
		priceMovePct := 0.0
		if subscriber.LastAlertedPriceUSD != nil && *subscriber.LastAlertedPriceUSD != 0 {
			priceMovePct = math.Abs(fetchResult.PriceUSD-*subscriber.LastAlertedPriceUSD) / *subscriber.LastAlertedPriceUSD * 100
		}
		if priceMovePct < w.alertPriceMovePct {
			return false, nil
		}
	}

	triggerType, triggered := evaluateThreshold(subscriber, fetchResult)
	if !triggered {
		return false, nil
	}

	message := buildAlertMessage(subscriber.PreferredLanguage, fetchResult, triggerType, analysis)

	sendErr := w.notifier.SendAssetAlert(subscriber.TelegramAssetChatID, message)

	status := notifier.StatusSent
	if sendErr != nil {
		status = notifier.StatusFailed
		log.Printf("[ERROR] evaluateAndAlert: send telegram alert to user %d failed: %v", subscriber.UserID, sendErr)
	} else {
		now := time.Now()
		cooldownUntil := now.Add(time.Duration(subscriber.AlertCooldownHours) * time.Hour)
		if err := w.upsertAlertState(ctx, subscriber.UserID, fetchResult.Symbol, triggerType, fetchResult.PriceUSD, now, cooldownUntil); err != nil {
			log.Printf("[ERROR] evaluateAndAlert: upsert alert state for user %d failed: %v", subscriber.UserID, err)
		}
	}

	if err := w.logNotification(ctx, subscriber.UserID, fetchResult.Symbol, message, status); err != nil {
		log.Printf("[ERROR] evaluateAndAlert: log notification for user %d failed: %v", subscriber.UserID, err)
	}

	if sendErr != nil {
		return false, fmt.Errorf("send telegram alert: %w", sendErr)
	}

	return true, nil
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

func (w *AssetWorker) logNotification(ctx context.Context, userID uint64, symbol, message string, status notifier.Status) error {
	assetSymbol := symbol
	return w.notifLogs.Record(ctx, notifier.LogEntry{
		UserID:         userID,
		NotifType:      notifier.TypeAsset,
		AssetSymbol:    &assetSymbol,
		ContentSummary: message,
		Status:         status,
	})
}

// buildAlertMessage formats the Telegram alert text in the subscriber's
// preferred language ('en' for English, anything else — including the
// 'id' default — for Indonesian).
func buildAlertMessage(lang string, fetchResult *asset.FetchResult, triggerType string, analysis *asset.AnalysisResult) string {
	priceIDR, err := currency.ConvertToIDR(fetchResult.PriceUSD)
	if err != nil {
		log.Printf("[ERROR] buildAlertMessage: convert %s to IDR: %v", fetchResult.Symbol, err)
	}

	timestamp := time.Now().In(time.FixedZone("WIB", wibOffsetSeconds)).Format("02 Jan 2006 15:04") + " WIB"

	var analysisText string
	if analysis != nil {
		if lang == "en" {
			analysisText = analysis.AnalysisEN
		} else {
			analysisText = analysis.AnalysisID
		}
	}

	// Every dynamic field must be escaped for Telegram's MarkdownV2 parse
	// mode, including the formatted numbers below — a decimal point like
	// the one in "60123.45" is itself a MarkdownV2 special character.
	// Numbers are formatted to strings first so EscapeTelegramMarkdown has
	// something to operate on. The literal "*" around the bold header is
	// template formatting, not dynamic data, so it stays unescaped.
	symbol := notifier.EscapeTelegramMarkdown(fetchResult.Symbol)
	priceUSDText := notifier.EscapeTelegramMarkdown(fmt.Sprintf("%.2f", fetchResult.PriceUSD))
	priceIDRText := notifier.EscapeTelegramMarkdown(fmt.Sprintf("%.0f", priceIDR))
	changePctText := notifier.EscapeTelegramMarkdown(fmt.Sprintf("%.2f", fetchResult.ChangePct24h))
	timestampEscaped := notifier.EscapeTelegramMarkdown(timestamp)

	// When AnalyzeAsset fails (DeepSeek down/timed out/rate-limited),
	// analysisText is empty and the alert still needs to go out — the
	// price/threshold breach is the important, time-sensitive part; the AI
	// commentary is a nice-to-have. Omit the AI section entirely instead of
	// leaving a blank line where it would have gone.
	var aiSection string
	if analysisText != "" {
		aiSection = "\n\n" + notifier.EscapeTelegramMarkdown(analysisText)
	}

	if lang == "en" {
		return fmt.Sprintf(
			"🔔 *WATCHTOWER ASSET ALERT*\n"+
				"📊 Symbol: %s\n"+
				"💰 Price: $%s (Rp %s)\n"+
				"📈 24h: %s%%\n"+
				"⚠️ Trigger: %s"+
				"%s\n\n"+
				"⏰ %s",
			symbol, priceUSDText, priceIDRText, changePctText,
			notifier.EscapeTelegramMarkdown(triggerLabelEN(triggerType)), aiSection, timestampEscaped,
		)
	}

	return fmt.Sprintf(
		"🔔 *WATCHTOWER ASSET ALERT*\n"+
			"📊 Symbol: %s\n"+
			"💰 Harga: $%s (Rp %s)\n"+
			"📈 24h: %s%%\n"+
			"⚠️ Trigger: %s"+
			"%s\n\n"+
			"⏰ %s",
		symbol, priceUSDText, priceIDRText, changePctText,
		notifier.EscapeTelegramMarkdown(triggerLabelID(triggerType)), aiSection, timestampEscaped,
	)
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
