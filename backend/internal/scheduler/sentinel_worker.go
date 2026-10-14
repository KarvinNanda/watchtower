package scheduler

import (
	"context"
	"database/sql"
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
// batched message. Every field comes from the source feed itself.
type SentinelBatchItem struct {
	SourceType      string
	Title           string
	Description     string
	URL             string
	CVEID           string
	Severity        string
	CVSSScore       float64
	KnownExploited  bool
	RequiredAction  string
	DueDate         string
	Ransomware      bool
	MatchedKeywords []string
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
// deduplicates items globally via sentinel_seen_items, and notifies users
// whose keyword subscriptions match a new item via the Sentinel Telegram
// bot. Every item matched for a given user within one run is batched into
// a single Telegram message (see SentinelBatch) rather than sent as
// separate messages per item, to avoid spamming a user whose keywords
// match several items in the same run.
type SentinelWorker struct {
	db       *sql.DB
	fetcher  *sentinel.SentinelFetcher
	notifier SentinelNotifierInterface

	notifLogs *notifier.LogRepository
}

// NewSentinelWorker wires together every dependency needed to run the
// sentinel threat-intel pipeline.
func NewSentinelWorker(
	db *sql.DB,
	fetcher *sentinel.SentinelFetcher,
	notif SentinelNotifierInterface,
) *SentinelWorker {
	return &SentinelWorker{
		db:        db,
		fetcher:   fetcher,
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
		if err := w.insertSeenItem(item); err != nil {
			return fmt.Errorf("insert seen item (no match): %w", err)
		}
		return nil
	}

	counters.Matched++

	if err := w.insertSeenItem(item); err != nil {
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
			SourceType:      item.SourceType,
			Title:           item.Title,
			Description:     item.Description,
			URL:             item.URL,
			CVEID:           item.CVEID,
			Severity:        item.Severity,
			CVSSScore:       item.CVSSScore,
			KnownExploited:  item.KnownExploited,
			RequiredAction:  item.RequiredAction,
			DueDate:         item.DueDate,
			Ransomware:      item.Ransomware,
			MatchedKeywords: u.Keywords,
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

// matchRow is one (user, matched keyword) pair returned by the matching query.
type matchRow struct {
	UserID         uint64
	Keyword        string
	TelegramChatID int64
	PreferredLang  string
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
			u.telegram_sentinel_chat_id,
			u.preferred_language
		FROM keyword_subscriptions ks
		JOIN users u ON u.id = ks.user_id
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
		if err := rows.Scan(&m.UserID, &m.Keyword, &m.TelegramChatID, &m.PreferredLang); err != nil {
			return nil, fmt.Errorf("scan matching user: %w", err)
		}
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

// userMatchInfo deduplicates matchRow by user, since one item can match
// several of a user's keyword subscriptions but should only appear once in
// that user's batch — with every matched keyword listed.
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

// insertSeenItem records item in sentinel_seen_items for global dedup, so
// it isn't reprocessed on the next run. The legacy ai_analysis_* columns
// are left NULL.
func (w *SentinelWorker) insertSeenItem(item sentinel.SentinelItem) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	_, err := w.db.ExecContext(ctx, `
		INSERT INTO sentinel_seen_items (source_type, item_identifier, first_seen_at, expires_at)
		VALUES (?, ?, NOW(), DATE_ADD(NOW(), INTERVAL 7 DAY))`,
		item.SourceType, item.Identifier,
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
		titles = append(titles, truncateRunes(item.Title, sentinelTitleTruncateLen))
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

const sentinelExcerptLen = 200

type sentinelLabels struct {
	Severity, Status, Source, Action, Due, Ransomware, Keyword string
}

var (
	sentinelLabelsEN = sentinelLabels{"SEVERITY", "STATUS", "SOURCE", "ACTION", "DUE", "RANSOMWARE", "KEYWORD"}
	sentinelLabelsID = sentinelLabels{"TINGKAT BAHAYA", "STATUS", "SUMBER", "AKSI", "BATAS WAKTU", "RANSOMWARE", "KEYWORD"}
)

var sentinelSourceNames = map[string]string{
	"cve":             "NVD",
	"cisa_kev":        "CISA KEV",
	"github_advisory": "GitHub Advisory",
	"github":          "GitHub",
	"exploit_db":      "Exploit-DB",
	"rss":             "RSS",
}

// buildSentinelItemBlock formats one matched item's section within a
// batched sentinel report. Labels follow lang ('en' for English, anything
// else for Indonesian); source content stays as published (English).
// Every dynamic field is escaped for Telegram MarkdownV2.
func buildSentinelItemBlock(lang string, item SentinelBatchItem) string {
	labels := sentinelLabelsID
	if lang == "en" {
		labels = sentinelLabelsEN
	}
	esc := notifier.EscapeTelegramMarkdown

	lines := []string{sentinelDivider}

	switch {
	case item.KnownExploited:
		lines = append(lines, labels.Status+": ACTIVELY EXPLOITED")
	case item.Severity != "" && item.CVSSScore > 0:
		lines = append(lines, labels.Severity+": "+esc(fmt.Sprintf("%s (%.1f)", item.Severity, item.CVSSScore)))
	case item.Severity != "":
		lines = append(lines, labels.Severity+": "+esc(item.Severity))
	case item.SourceType == "cve" || item.SourceType == "github_advisory":
		lines = append(lines, labels.Severity+": N/A")
	}

	source := sentinelSourceNames[item.SourceType]
	if source == "" {
		source = item.SourceType
	}
	lines = append(lines, labels.Source+": "+esc(source))

	if item.CVEID != "" {
		lines = append(lines, "CVE: "+esc(item.CVEID))
	}
	lines = append(lines, esc(truncateRunes(item.Title, sentinelTitleTruncateLen)))
	if item.Description != "" && item.Description != item.Title {
		lines = append(lines, esc(truncateRunes(item.Description, sentinelExcerptLen)))
	}
	if item.RequiredAction != "" {
		lines = append(lines, labels.Action+": "+esc(item.RequiredAction))
	}
	if item.DueDate != "" {
		lines = append(lines, labels.Due+": "+esc(item.DueDate))
	}
	if item.Ransomware {
		lines = append(lines, labels.Ransomware+": known campaign use")
	}
	if len(item.MatchedKeywords) > 0 {
		lines = append(lines, labels.Keyword+": "+esc(strings.Join(item.MatchedKeywords, ", ")))
	}
	lines = append(lines, "🔗 "+esc(item.URL), sentinelDivider)

	return strings.Join(lines, "\n")
}

// truncateRunes bounds s to n runes, appending an ellipsis when truncated.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
