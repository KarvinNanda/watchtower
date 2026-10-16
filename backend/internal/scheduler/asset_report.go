package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/karvin-nanda/watchtower/internal/alertmsg"
	"github.com/karvin-nanda/watchtower/internal/notifier"
	"github.com/karvin-nanda/watchtower/internal/rules"
)

// AssetReportResult summarizes one status report (#7) for the manual-run API.
// Errors counts symbols whose price could not be fetched; Stale those shown
// with cached data; Sent is 1 when every message part was delivered.
type AssetReportResult struct {
	Symbols int
	Rules   int
	Sent    int
	Errors  int
	Stale   int
}

// ReportForUser sends userID one status report: every symbol with an active
// rule, its price, and how far the price is from each active rule. It only
// reads: no rule is evaluated and no rule state is written, so the report
// never changes what Check now or the periodic run will send.
func (w *AssetWorker) ReportForUser(userID uint64) (result AssetReportResult, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] AssetWorker.ReportForUser: recovered from panic: %v", r)
			err = fmt.Errorf("scheduler: recovered from panic: %v", r)
		}
	}()

	symbols, err := w.getUniqueActiveSymbols(userID)
	if err != nil {
		return result, fmt.Errorf("scheduler: get unique active symbols: %w", err)
	}

	var (
		items  []alertmsg.ReportSymbol
		chatID int64
		lang   string
	)
	for _, symbol := range symbols {
		item, rows, symErr := w.reportSymbol(symbol, userID)
		if symErr != nil {
			result.Errors++
			log.Printf("[ERROR] AssetWorker.ReportForUser: %v", symErr)
			continue
		}
		if len(rows) == 0 {
			continue
		}
		chatID, lang = rows[0].TelegramAssetChatID, rows[0].PreferredLanguage
		result.Symbols++
		// Only rendered rules count, so the panel matches the Telegram header.
		result.Rules += len(item.Rules)
		if item.Unavailable {
			result.Errors++
		}
		if item.StaleSince != nil {
			result.Stale++
		}
		items = append(items, item)
	}

	// Active rules exist but none loaded (typically the chat was unlinked
	// after the click): count it so the panel does not say "no active rules".
	if len(symbols) > 0 && len(items) == 0 && result.Errors == 0 {
		result.Errors++
		log.Printf("[WARN] AssetWorker.ReportForUser: user=%d has active rules but none could be loaded (Telegram chat unlinked?)", userID)
	}
	if len(items) > 0 && w.sendReport(userID, chatID, lang, items) {
		result.Sent = 1
	}
	log.Printf("[INFO] AssetWorker.ReportForUser: user=%d symbols=%d rules=%d sent=%d errors=%d stale=%d",
		userID, result.Symbols, result.Rules, result.Sent, result.Errors, result.Stale)
	return result, nil
}

// reportSymbol loads userID's active rules on symbol and measures each one
// against fresh market data. No rows means nothing to report there (or no
// linked chat). A price that cannot be fetched still lists the symbol.
func (w *AssetWorker) reportSymbol(symbol SymbolInfo, userID uint64) (alertmsg.ReportSymbol, []ruleRow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	item := alertmsg.ReportSymbol{Symbol: symbol.Symbol}
	rows, err := w.loadRules(ctx, symbol.Symbol, userID)
	if err != nil {
		return item, nil, fmt.Errorf("load rules for %s: %w", symbol.Symbol, err)
	}
	if len(rows) == 0 {
		return item, nil, nil
	}

	data, err := w.cache.GetMarketData(ctx, symbol.Symbol, symbol.AssetType, manualFetch)
	if err == nil && data.PriceUSD <= 0 {
		err = fmt.Errorf("invalid price %v", data.PriceUSD)
	}
	if err != nil {
		log.Printf("[ERROR] reportSymbol: market data for %s: %v", symbol.Symbol, err)
		item.Unavailable = true
		return item, rows, nil
	}

	market := marketFromData(data)
	item.PriceUSD, item.NativeIDR, item.ChangePct = data.PriceUSD, nativeIDRFor(symbol.AssetType, data), data.ChangePct24h
	if data.Stale {
		since := data.LastFetched
		item.StaleSince = &since
	}
	for _, row := range rows {
		item.Rules = append(item.Rules, alertmsg.ReportRule{
			RuleID:   row.Rule.ID,
			Trigger:  rules.TriggerOf(row.Rule),
			Distance: rules.DistanceTo(row.Rule, market),
		})
	}
	return item, rows, nil
}

// sendReport sends the report parts and records one notification_logs row.
func (w *AssetWorker) sendReport(userID uint64, chatID int64, lang string, items []alertmsg.ReportSymbol) bool {
	messages := alertmsg.AssetReportMessages(lang, time.Now().In(w.loc), w.messageRate(), items)

	allSent := true
	for _, msg := range messages {
		if err := w.notifier.SendAssetAlert(chatID, msg); err != nil {
			allSent = false
			log.Printf("[ERROR] sendReport: send status report to user %d failed: %v", userID, err)
			break
		}
		// Telegram rate limit: stay under ~30 messages/second.
		time.Sleep(telegramSendGap)
	}

	status := notifier.StatusSent
	if !allSent {
		status = notifier.StatusFailed
	}
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()
	if err := w.notifLogs.Record(ctx, notifier.LogEntry{
		UserID:         userID,
		NotifType:      notifier.TypeAsset,
		ContentSummary: fmt.Sprintf("%s %d symbols", notifier.StatusReportSummaryPrefix, len(items)),
		Status:         status,
	}); err != nil {
		log.Printf("[ERROR] sendReport: log notification for user %d failed: %v", userID, err)
	}
	return allSent
}
