package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/karvin-nanda/watchtower/internal/notifier"
	"github.com/karvin-nanda/watchtower/internal/sentinel"
)

const (
	mysqlDuplicateEntryCode = 1062
	// sentinelItemRetention matches the architecture spec: expires_at =
	// first_seen_at + 7 days.
	sentinelItemRetention = 7 * 24 * time.Hour
)

// SentinelNotifierInterface is the subset of notifier.Notifier's behavior
// SentinelWorker depends on, allowing tests to inject a mock sender. Named
// distinctly from asset_worker.go's NotifierInterface (SendAssetAlert)
// since Go doesn't allow two interfaces of the same name in one package.
type SentinelNotifierInterface interface {
	SendSentinelAlert(chatID int64, message string) error
}

// ProcessCounters tallies one Run's activity for the summary log.
type ProcessCounters struct {
	Fetched int
	New     int
	Matched int
	Sent    int
}

// SentinelWorker periodically polls all six threat-intel sources,
// deduplicates items globally via sentinel_seen_items, runs one DeepSeek
// analysis per new item that matches at least one active keyword
// subscription (shared across every matching user), and notifies matching
// users via the Sentinel Telegram bot.
type SentinelWorker struct {
	db       *sql.DB
	fetcher  *sentinel.SentinelFetcher
	analyzer *sentinel.SentinelAnalyzer
	notifier SentinelNotifierInterface

	notifLogs *notifier.LogRepository
}

// NewSentinelWorker wires together every dependency needed to run the
// sentinel threat-intel pipeline.
func NewSentinelWorker(
	db *sql.DB,
	fetcher *sentinel.SentinelFetcher,
	analyzer *sentinel.SentinelAnalyzer,
	notif SentinelNotifierInterface,
) *SentinelWorker {
	return &SentinelWorker{
		db:        db,
		fetcher:   fetcher,
		analyzer:  analyzer,
		notifier:  notif,
		notifLogs: notifier.NewLogRepository(db),
	}
}

// Run is the scheduler entry point: it cleans up expired sentinel_seen_items,
// fetches and processes every source, and logs a summary. It never panics —
// any recovered panic is logged and returned as an error.
func (w *SentinelWorker) Run() (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] SentinelWorker.Run: recovered from panic: %v", r)
			err = fmt.Errorf("scheduler: recovered from panic: %v", r)
		}
	}()

	start := time.Now()
	log.Printf("[INFO] SentinelWorker.Run: starting run at %s", start.Format(time.RFC3339))

	if cleanupErr := w.cleanupExpiredItems(); cleanupErr != nil {
		log.Printf("[ERROR] SentinelWorker.Run: cleanup expired items: %v", cleanupErr)
	}

	counters := &ProcessCounters{}
	if procErr := w.fetchAndProcess(counters); procErr != nil {
		log.Printf("[ERROR] SentinelWorker.Run: fetch and process: %v", procErr)
		return fmt.Errorf("scheduler: fetch and process: %w", procErr)
	}

	log.Printf("[INFO] SentinelWorker.Run: completed in %s — fetched: %d, new: %d, matched: %d, sent: %d",
		time.Since(start), counters.Fetched, counters.New, counters.Matched, counters.Sent)

	return nil
}

// Start runs Run() immediately, then again on every tick of interval, until
// ctx is cancelled — at which point it returns, allowing the caller to shut
// down gracefully.
func (w *SentinelWorker) Start(ctx context.Context, interval time.Duration) {
	log.Printf("[INFO] SentinelWorker.Start: running immediately, then every %s", interval)

	if err := w.Run(); err != nil {
		log.Printf("[ERROR] SentinelWorker.Start: initial run failed: %v", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[INFO] SentinelWorker.Start: context cancelled, stopping gracefully")
			return
		case <-ticker.C:
			if err := w.Run(); err != nil {
				log.Printf("[ERROR] SentinelWorker.Start: run failed: %v", err)
			}
		}
	}
}

// cleanupExpiredItems deletes sentinel_seen_items rows past their
// expires_at, run at the start of every scheduler tick.
func (w *SentinelWorker) cleanupExpiredItems() error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	res, err := w.db.ExecContext(ctx, `DELETE FROM sentinel_seen_items WHERE expires_at < NOW()`)
	if err != nil {
		return fmt.Errorf("delete expired sentinel_seen_items: %w", err)
	}

	affected, _ := res.RowsAffected()
	log.Printf("[INFO] cleanupExpiredItems: removed %d expired sentinel_seen_items rows", affected)

	return nil
}

// fetchAndProcess fetches every source and processes each item, tallying
// results into counters. One item's failure never stops the others.
func (w *SentinelWorker) fetchAndProcess(counters *ProcessCounters) error {
	items, err := w.fetcher.FetchAll()
	if err != nil {
		return fmt.Errorf("fetch all sentinel sources: %w", err)
	}
	counters.Fetched = len(items)

	for _, item := range items {
		if err := w.processItem(item, counters); err != nil {
			log.Printf("[ERROR] fetchAndProcess: process item %s/%s failed: %v", item.SourceType, item.Identifier, err)
		}
	}

	return nil
}

// processItem implements the full dedup -> match -> analyze -> notify flow
// for a single item. It never panics — any recovered panic is logged and
// returned as an error, and the caller continues to the next item.
func (w *SentinelWorker) processItem(item sentinel.SentinelItem, counters *ProcessCounters) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] processItem: recovered from panic for %s/%s: %v", item.SourceType, item.Identifier, r)
			err = fmt.Errorf("recovered from panic: %v", r)
		}
	}()

	seen, err := w.itemAlreadySeen(item)
	if err != nil {
		return fmt.Errorf("check sentinel_seen_items: %w", err)
	}
	if seen {
		return nil
	}

	counters.New++

	matches, err := w.findMatchingUsers(item)
	if err != nil {
		return fmt.Errorf("find matching users: %w", err)
	}

	if len(matches) == 0 {
		if err := w.insertSeenItem(item, nil); err != nil {
			return fmt.Errorf("insert seen item (no match): %w", err)
		}
		return nil
	}

	counters.Matched++

	userContext := buildAggregateUserContext(matches)

	analysis, analyzeErr := w.analyzer.AnalyzeItem(item, userContext)
	if analyzeErr != nil {
		log.Printf("[ERROR] processItem: analyze %s/%s failed: %v", item.SourceType, item.Identifier, analyzeErr)
		if err := w.insertSeenItem(item, nil); err != nil {
			log.Printf("[ERROR] processItem: insert seen item after failed analysis: %v", err)
		}
		return nil
	}

	if err := w.insertSeenItem(item, analysis); err != nil {
		return fmt.Errorf("insert seen item: %w", err)
	}

	for _, u := range groupMatchesByUser(matches) {
		message := buildSentinelMessage(u.PreferredLang, item, analysis, u.Keywords)

		sendErr := w.notifier.SendSentinelAlert(u.TelegramChatID, message)

		status := notifier.StatusSent
		if sendErr != nil {
			status = notifier.StatusFailed
			log.Printf("[ERROR] processItem: send telegram alert to user %d failed: %v", u.UserID, sendErr)
		} else {
			counters.Sent++
		}

		if logErr := w.logNotification(u.UserID, u.Keywords[0], message, status); logErr != nil {
			log.Printf("[ERROR] processItem: log notification for user %d failed: %v", u.UserID, logErr)
		}

		// Telegram rate limit: stay under ~30 messages/second.
		time.Sleep(telegramSendGap)
	}

	return nil
}

func (w *SentinelWorker) itemAlreadySeen(item sentinel.SentinelItem) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	var id uint64
	err := w.db.QueryRowContext(ctx, `
		SELECT id FROM sentinel_seen_items
		WHERE source_type = ? AND item_identifier = ?`,
		item.SourceType, item.Identifier,
	).Scan(&id)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("query sentinel_seen_items: %w", err)
	}
	return true, nil
}

// matchRow is one (user, matched keyword) pair returned by the matching
// query, joined with the fields needed to notify that user and to
// aggregate a UserContext for the DeepSeek analysis.
type matchRow struct {
	UserID         uint64
	Keyword        string
	ContextNote    sql.NullString
	TelegramChatID int64
	PreferredLang  string
	Devices        sql.NullString
	OSList         sql.NullString
	ExpertiseLevel sql.NullString
}

// findMatchingUsers returns one row per (user, keyword_subscription) whose
// keyword appears in item's title/description, for every active subscriber
// with an active account and a configured Telegram sentinel chat ID.
func (w *SentinelWorker) findMatchingUsers(item sentinel.SentinelItem) ([]matchRow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	searchText := item.Title + " " + item.Description

	rows, err := w.db.QueryContext(ctx, `
		SELECT
			ks.user_id,
			ks.keyword,
			ks.context_note,
			u.telegram_sentinel_chat_id,
			u.preferred_language,
			up.devices,
			up.os_list,
			up.expertise_level
		FROM keyword_subscriptions ks
		JOIN users u ON u.id = ks.user_id
		LEFT JOIN user_profiles up ON up.user_id = ks.user_id
		WHERE ks.is_active = true
			AND u.is_active = true
			AND u.telegram_sentinel_chat_id IS NOT NULL
			AND LOWER(?) LIKE CONCAT('%', LOWER(ks.keyword), '%')`,
		searchText,
	)
	if err != nil {
		return nil, fmt.Errorf("query matching users: %w", err)
	}
	defer rows.Close()

	var matches []matchRow
	for rows.Next() {
		var m matchRow
		if err := rows.Scan(
			&m.UserID, &m.Keyword, &m.ContextNote, &m.TelegramChatID, &m.PreferredLang,
			&m.Devices, &m.OSList, &m.ExpertiseLevel,
		); err != nil {
			return nil, fmt.Errorf("scan matching user: %w", err)
		}
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

// buildAggregateUserContext combines every matched subscriber's keywords,
// per-keyword context notes, devices, OS list, and expertise into a single
// UserContext. Since AnalyzeItem is called once per item — not once per
// matched user — this represents the combined audience for the item.
// Expertise takes the highest level found (advanced > intermediate >
// beginner), so the analysis can be as technically detailed as the most
// capable subscriber can use.
func buildAggregateUserContext(matches []matchRow) *sentinel.UserContext {
	uc := &sentinel.UserContext{
		ContextNote: make(map[string]string),
	}

	keywordSet := make(map[string]struct{})
	deviceSet := make(map[string]struct{})
	osSet := make(map[string]struct{})
	expertiseRank := map[string]int{"beginner": 0, "intermediate": 1, "advanced": 2}
	highestExpertise := ""

	for _, m := range matches {
		keywordSet[m.Keyword] = struct{}{}
		if m.ContextNote.Valid && m.ContextNote.String != "" {
			if _, exists := uc.ContextNote[m.Keyword]; !exists {
				uc.ContextNote[m.Keyword] = m.ContextNote.String
			}
		}

		if m.Devices.Valid && m.Devices.String != "" {
			var devices []string
			if jsonErr := json.Unmarshal([]byte(m.Devices.String), &devices); jsonErr == nil {
				for _, d := range devices {
					deviceSet[d] = struct{}{}
				}
			}
		}

		if m.OSList.Valid && m.OSList.String != "" {
			var osList []string
			if jsonErr := json.Unmarshal([]byte(m.OSList.String), &osList); jsonErr == nil {
				for _, o := range osList {
					osSet[o] = struct{}{}
				}
			}
		}

		expertise := "beginner"
		if m.ExpertiseLevel.Valid && m.ExpertiseLevel.String != "" {
			expertise = m.ExpertiseLevel.String
		}
		if highestExpertise == "" || expertiseRank[expertise] > expertiseRank[highestExpertise] {
			highestExpertise = expertise
		}
	}

	for kw := range keywordSet {
		uc.Keywords = append(uc.Keywords, kw)
	}
	for d := range deviceSet {
		uc.Devices = append(uc.Devices, d)
	}
	for o := range osSet {
		uc.OSList = append(uc.OSList, o)
	}

	uc.Expertise = "beginner"
	if highestExpertise != "" {
		uc.Expertise = highestExpertise
	}

	return uc
}

// userMatchInfo groups every keyword that matched for one specific user, so
// their notification can list only their own matched keywords.
type userMatchInfo struct {
	UserID         uint64
	TelegramChatID int64
	PreferredLang  string
	Keywords       []string
}

func groupMatchesByUser(matches []matchRow) []userMatchInfo {
	order := make([]uint64, 0)
	byUser := make(map[uint64]*userMatchInfo)

	for _, m := range matches {
		info, ok := byUser[m.UserID]
		if !ok {
			info = &userMatchInfo{
				UserID:         m.UserID,
				TelegramChatID: m.TelegramChatID,
				PreferredLang:  m.PreferredLang,
			}
			byUser[m.UserID] = info
			order = append(order, m.UserID)
		}
		info.Keywords = append(info.Keywords, m.Keyword)
	}

	result := make([]userMatchInfo, 0, len(order))
	for _, id := range order {
		result = append(result, *byUser[id])
	}
	return result
}

// insertSeenItem records item in sentinel_seen_items for global dedup.
// analysis may be nil (no matching subscribers, or analysis failed) — the
// item is still recorded so it isn't reprocessed on the next run.
func (w *SentinelWorker) insertSeenItem(item sentinel.SentinelItem, analysis *sentinel.SentinelAnalysis) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	var analysisID, analysisEN interface{}
	if analysis != nil {
		analysisID = analysis.AnalysisID
		analysisEN = analysis.AnalysisEN
	}

	_, err := w.db.ExecContext(ctx, `
		INSERT INTO sentinel_seen_items (
			source_type, item_identifier,
			ai_analysis_id, ai_analysis_en,
			first_seen_at, expires_at
		) VALUES (?, ?, ?, ?, NOW(), DATE_ADD(NOW(), INTERVAL 7 DAY))`,
		item.SourceType, item.Identifier, analysisID, analysisEN,
	)
	if err != nil {
		var mysqlErr *mysqldriver.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlDuplicateEntryCode {
			// Another concurrent run already inserted this item between our
			// existence check and this insert — not an error.
			return nil
		}
		return fmt.Errorf("insert sentinel_seen_items: %w", err)
	}
	return nil
}

func (w *SentinelWorker) logNotification(userID uint64, keyword, message string, status notifier.Status) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	kw := keyword
	return w.notifLogs.Record(ctx, notifier.LogEntry{
		UserID:         userID,
		NotifType:      notifier.TypeSentinel,
		Keyword:        &kw,
		ContentSummary: message,
		Status:         status,
	})
}

// buildSentinelMessage formats the Telegram alert text in the subscriber's
// preferred language ('en' for English, anything else — including the 'id'
// default — for Indonesian), listing only that user's own matched keywords.
func buildSentinelMessage(lang string, item sentinel.SentinelItem, analysis *sentinel.SentinelAnalysis, userKeywords []string) string {
	analysisText := analysis.AnalysisID
	if lang == "en" {
		analysisText = analysis.AnalysisEN
	}

	timestamp := time.Now().In(time.FixedZone("WIB", wibOffsetSeconds)).Format("02 Jan 2006 15:04") + " WIB"
	keywordsText := strings.Join(userKeywords, ", ")

	// Every dynamic field must be escaped for Telegram's MarkdownV2 parse
	// mode — fetched titles/descriptions and AI-generated analysis text
	// routinely contain raw underscores, asterisks, parentheses, etc. that
	// would otherwise break Telegram's entity parser (e.g. an unescaped
	// "_" in "wp_ajax_nopriv_..." is read as an unterminated italic marker).
	// The literal "*" around the bold header below is template formatting,
	// not dynamic data, so it is intentionally left unescaped.
	return fmt.Sprintf(
		"🛡️ *WATCHTOWER SENTINEL*\n"+
			"📌 Source: %s\n"+
			"🔍 Item: %s\n"+
			"🔗 %s\n\n"+
			"%s\n\n"+
			"🏷️ Keywords: %s\n"+
			"⏰ %s",
		notifier.EscapeTelegramMarkdown(item.SourceType),
		notifier.EscapeTelegramMarkdown(item.Title),
		notifier.EscapeTelegramMarkdown(item.URL),
		notifier.EscapeTelegramMarkdown(analysisText),
		notifier.EscapeTelegramMarkdown(keywordsText),
		notifier.EscapeTelegramMarkdown(timestamp),
	)
}
