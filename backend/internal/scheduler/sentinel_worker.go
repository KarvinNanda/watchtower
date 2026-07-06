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
	// sentinelTitleTruncateLen bounds how much of an item's title appears in
	// a batched message section and in the notification_logs summary.
	sentinelTitleTruncateLen = 60
)

// SentinelNotifierInterface is the subset of notifier.Notifier's behavior
// SentinelWorker depends on, allowing tests to inject a mock sender. Named
// distinctly from asset_worker.go's NotifierInterface (SendAssetAlert)
// since Go doesn't allow two interfaces of the same name in one package.
type SentinelNotifierInterface interface {
	SendSentinelAlert(chatID int64, message string) error
}

// ProcessCounters tallies one Run's activity for the summary log. Sent
// counts users whose batched message was delivered successfully (not
// individual items — one user can receive many matched items in one send).
type ProcessCounters struct {
	Fetched int
	New     int
	Matched int
	Sent    int
}

// SentinelBatchItem is one matched sentinel item for a single user,
// collected during a run and rendered as one section of that user's
// batched message.
type SentinelBatchItem struct {
	SourceType   string
	Title        string
	URL          string
	StatusBahaya string
	CVE          string
	Kategori     string
	DampakID     string
	AksiID       string
	DampakEN     string
	AksiEN       string
}

// SentinelBatch accumulates every SentinelBatchItem matched for one user
// during a single SentinelWorker.Run, so they can be delivered as one
// combined Telegram message instead of one message per matched item.
type SentinelBatch struct {
	UserID   uint64
	ChatID   int64
	Language string
	Items    []SentinelBatchItem
}

// SentinelWorker periodically polls all six threat-intel sources,
// deduplicates items globally via sentinel_seen_items, runs one DeepSeek
// analysis per new item that matches at least one active keyword
// subscription (shared across every matching user), and notifies matching
// users via the Sentinel Telegram bot. Every item matched for a given user
// within one run is batched into a single Telegram message (see
// SentinelBatch) rather than sent as separate messages per item, to avoid
// spamming a user whose keywords match several items in the same run.
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
// fetches and processes every source (collecting matched items per user),
// then sends each user at most one combined message for the run, and logs
// a summary. It never panics — any recovered panic is logged and returned
// as an error.
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
	batches := make(map[uint64]*SentinelBatch)
	if procErr := w.fetchAndProcess(counters, batches); procErr != nil {
		log.Printf("[ERROR] SentinelWorker.Run: fetch and process: %v", procErr)
		return fmt.Errorf("scheduler: fetch and process: %w", procErr)
	}

	sent, usersNotified := w.dispatchBatches(batches)
	counters.Sent = sent

	log.Printf("[INFO] SentinelWorker.Run: completed in %s — fetched: %d, new: %d, matched: %d, "+
		"users notified: %d, sent: %d",
		time.Since(start), counters.Fetched, counters.New, counters.Matched, usersNotified, counters.Sent)

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
// results into counters and accumulating matched items per user into
// batches. One item's failure never stops the others.
func (w *SentinelWorker) fetchAndProcess(counters *ProcessCounters, batches map[uint64]*SentinelBatch) error {
	items, err := w.fetcher.FetchAll()
	if err != nil {
		return fmt.Errorf("fetch all sentinel sources: %w", err)
	}
	counters.Fetched = len(items)

	for _, item := range items {
		if err := w.processItem(item, counters, batches); err != nil {
			log.Printf("[ERROR] fetchAndProcess: process item %s/%s failed: %v", item.SourceType, item.Identifier, err)
		}
	}

	return nil
}

// processItem implements the dedup -> match -> analyze flow for a single
// item, appending a SentinelBatchItem to every matched user's batch rather
// than sending anything directly — delivery happens once per user after
// every item has been processed, via dispatchBatches. It never panics —
// any recovered panic is logged and returned as an error, and the caller
// continues to the next item.
func (w *SentinelWorker) processItem(item sentinel.SentinelItem, counters *ProcessCounters, batches map[uint64]*SentinelBatch) (err error) {
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
		batch, ok := batches[u.UserID]
		if !ok {
			batch = &SentinelBatch{
				UserID:   u.UserID,
				ChatID:   u.TelegramChatID,
				Language: u.PreferredLang,
			}
			batches[u.UserID] = batch
		}
		batch.Items = append(batch.Items, SentinelBatchItem{
			SourceType:   item.SourceType,
			Title:        item.Title,
			URL:          item.URL,
			StatusBahaya: analysis.StatusBahaya,
			CVE:          analysis.CVE,
			Kategori:     analysis.Kategori,
			DampakID:     analysis.DampakID,
			AksiID:       analysis.AksiID,
			DampakEN:     analysis.DampakEN,
			AksiEN:       analysis.AksiEN,
		})
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

// userMatchInfo deduplicates matchRow by user, since one item can match
// several of a user's keyword subscriptions but should only appear once in
// that user's batch.
type userMatchInfo struct {
	UserID         uint64
	TelegramChatID int64
	PreferredLang  string
}

func groupMatchesByUser(matches []matchRow) []userMatchInfo {
	order := make([]uint64, 0)
	byUser := make(map[uint64]*userMatchInfo)

	for _, m := range matches {
		if _, ok := byUser[m.UserID]; ok {
			continue
		}
		info := &userMatchInfo{
			UserID:         m.UserID,
			TelegramChatID: m.TelegramChatID,
			PreferredLang:  m.PreferredLang,
		}
		byUser[m.UserID] = info
		order = append(order, m.UserID)
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

	// ai_analysis_id/ai_analysis_en are a plain-text audit record of what
	// was generated for this item — nothing re-parses them back out — so
	// the now-structured SentinelAnalysis fields are composed into one
	// readable line per language rather than needing new columns.
	var analysisID, analysisEN interface{}
	if analysis != nil {
		analysisID = fmt.Sprintf("[%s] CVE: %s | DAMPAK: %s | AKSI: %s",
			analysis.StatusBahaya, analysis.CVE, analysis.DampakID, analysis.AksiID)
		analysisEN = fmt.Sprintf("[%s] CVE: %s | IMPACT: %s | ACTION: %s",
			analysis.StatusBahaya, analysis.CVE, analysis.DampakEN, analysis.AksiEN)
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

// dispatchBatches sends one combined Telegram message per user in batches
// (splitting into multiple messages only if the combined text would exceed
// Telegram's 4096-character limit — see buildBatchedMessages), and records
// exactly one notification_logs entry per user for the run.
func (w *SentinelWorker) dispatchBatches(batches map[uint64]*SentinelBatch) (sent, usersNotified int) {
	if len(batches) == 0 {
		return 0, 0
	}

	timestamp := time.Now().In(time.FixedZone("WIB", wibOffsetSeconds)).Format("02 Jan 2006 15:04") + " WIB"
	timestampEscaped := notifier.EscapeTelegramMarkdown(timestamp)

	for _, batch := range batches {
		if len(batch.Items) == 0 {
			continue
		}
		if w.dispatchUserBatch(batch, timestampEscaped) {
			sent++
		}
		usersNotified++
	}

	return sent, usersNotified
}

// dispatchUserBatch sends batch as one or more Telegram messages (per
// buildBatchedMessages) and records exactly one notification_logs row for
// the whole batch, reporting whether every message part sent successfully.
func (w *SentinelWorker) dispatchUserBatch(batch *SentinelBatch, timestampEscaped string) bool {
	blocks := make([]string, 0, len(batch.Items))
	for _, item := range batch.Items {
		blocks = append(blocks, buildSentinelItemBlock(batch.Language, item))
	}

	messages := buildBatchedMessages("🛡️ *WATCHTOWER SENTINEL REPORT*", timestampEscaped, blocks)

	allSent := true
	for _, msg := range messages {
		if err := w.notifier.SendSentinelAlert(batch.ChatID, msg); err != nil {
			allSent = false
			log.Printf("[ERROR] dispatchUserBatch: send telegram alert to user %d failed: %v", batch.UserID, err)
			break
		}
		// Telegram rate limit: stay under ~30 messages/second.
		time.Sleep(telegramSendGap)
	}

	status := notifier.StatusSent
	if !allSent {
		status = notifier.StatusFailed
	}

	titles := make([]string, 0, len(batch.Items))
	for _, item := range batch.Items {
		titles = append(titles, truncateTitle(item.Title))
	}
	summary := strings.Join(titles, ", ") + " sentinel alert triggered"

	if err := w.logNotification(batch.UserID, summary, status); err != nil {
		log.Printf("[ERROR] dispatchUserBatch: log notification for user %d failed: %v", batch.UserID, err)
	}

	return allSent
}

func (w *SentinelWorker) logNotification(userID uint64, summary string, status notifier.Status) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	return w.notifLogs.Record(ctx, notifier.LogEntry{
		UserID:         userID,
		NotifType:      notifier.TypeSentinel,
		ContentSummary: summary,
		Status:         status,
	})
}

// sentinelDivider frames each item's section as a compact, scannable card
// (see buildSentinelItemBlock) — computed once since EscapeTelegramMarkdown
// is a pure function of a constant string.
var sentinelDivider = notifier.EscapeTelegramMarkdown(strings.Repeat("-", 50))

// buildSentinelItemBlock formats a single matched item's section within a
// batched sentinel report, in the subscriber's preferred language ('en' for
// English, anything else — including the 'id' default — for Indonesian).
// Every dynamic field is escaped for Telegram's MarkdownV2 parse mode —
// fetched titles/URLs and AI-generated dampak/aksi text routinely contain
// raw underscores, asterisks, parentheses, etc. that would otherwise break
// Telegram's entity parser (e.g. an unescaped "_" in "wp_ajax_nopriv_..." is
// read as an unterminated italic marker).
func buildSentinelItemBlock(lang string, item SentinelBatchItem) string {
	statusBahaya := notifier.EscapeTelegramMarkdown(item.StatusBahaya)
	cve := notifier.EscapeTelegramMarkdown(item.CVE)
	title := notifier.EscapeTelegramMarkdown(truncateTitle(item.Title))
	url := notifier.EscapeTelegramMarkdown(item.URL)

	impactLabel, actionLabel := "DAMPAK", "AKSI"
	impactText, actionText := item.DampakID, item.AksiID
	if lang == "en" {
		impactLabel, actionLabel = "IMPACT", "ACTION"
		impactText, actionText = item.DampakEN, item.AksiEN
	}

	return fmt.Sprintf(
		"%s\nSTATUS: %s\nCVE: %s\n%s\n🔗 %s\n%s: %s\n%s: %s\n%s",
		sentinelDivider, statusBahaya, cve, title, url,
		impactLabel, notifier.EscapeTelegramMarkdown(impactText),
		actionLabel, notifier.EscapeTelegramMarkdown(actionText),
		sentinelDivider,
	)
}

// truncateTitle bounds title to sentinelTitleTruncateLen runes, appending an
// ellipsis when truncated so it's clear the text was cut off.
func truncateTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= sentinelTitleTruncateLen {
		return title
	}
	return string(runes[:sentinelTitleTruncateLen]) + "…"
}
