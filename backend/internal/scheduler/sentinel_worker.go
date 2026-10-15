package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/karvin-nanda/watchtower/internal/alertmsg"
	"github.com/karvin-nanda/watchtower/internal/notifier"
	"github.com/karvin-nanda/watchtower/internal/sentinel"
)

const (
	mysqlDuplicateEntryCode = 1062
	// sentinelItemRetention matches the architecture spec: expires_at =
	// first_seen_at + 7 days.
	sentinelItemRetention = 7 * 24 * time.Hour
	// sentinelTitleTruncateLen bounds how much of an item's title appears in
	// the notification_logs summary (alertmsg truncates the message itself).
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

// SentinelBatchItem is one matched item in a user's batch. It is the
// alertmsg entry type itself, so the batch needs no conversion to render.
type SentinelBatchItem = alertmsg.SentinelEntry

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

	mu sync.Mutex // serializes periodic and manual sentinel runs

	notifLogs *notifier.LogRepository

	// loc is the instance timezone (APP_TIMEZONE) for message timestamps.
	loc *time.Location
}

// NewSentinelWorker wires together every dependency needed to run the
// sentinel threat-intel pipeline.
func NewSentinelWorker(
	db *sql.DB,
	fetcher *sentinel.SentinelFetcher,
	notif SentinelNotifierInterface,
	loc *time.Location,
) *SentinelWorker {
	return &SentinelWorker{
		db:        db,
		fetcher:   fetcher,
		notifier:  notif,
		notifLogs: notifier.NewLogRepository(db),
		loc:       loc,
	}
}

// SentinelRunResult summarizes one sentinel run. SentToUser counts items
// actually delivered per user (failed sends are not counted).
type SentinelRunResult struct {
	Fetched    int
	New        int
	Matched    int
	SentToUser map[uint64]int
}

// Run is the periodic entry point. A run that fetched nothing from any
// source (typically no network) returns an error so the Pipeline records
// it as failed and retries soon instead of after a full interval.
func (w *SentinelWorker) Run() error {
	res, err := w.RunWithResult()
	if err != nil {
		return err
	}
	if res.Fetched == 0 {
		return errors.New("scheduler: no sentinel items fetched from any source")
	}
	return nil
}

// RunWithResult runs the global sentinel pipeline (dedup is global, so a
// manual run cannot be scoped to one user without starving others) and
// reports per-user delivery. It holds w.mu for its whole duration so
// periodic and manual runs never overlap, and never panics — any recovered
// panic is returned as an error.
func (w *SentinelWorker) RunWithResult() (result SentinelRunResult, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] SentinelWorker.RunWithResult: recovered from panic: %v", r)
			err = fmt.Errorf("scheduler: recovered from panic: %v", r)
		}
	}()

	start := time.Now()
	log.Printf("[INFO] SentinelWorker.RunWithResult: starting run at %s", start.Format(time.RFC3339))

	if cleanupErr := w.cleanupExpiredItems(); cleanupErr != nil {
		log.Printf("[ERROR] SentinelWorker.RunWithResult: cleanup expired items: %v", cleanupErr)
	}

	counters := &ProcessCounters{}
	batches := make(map[uint64]*SentinelBatch)
	if procErr := w.fetchAndProcess(counters, batches); procErr != nil {
		return result, fmt.Errorf("scheduler: fetch and process: %w", procErr)
	}

	sentToUser, usersNotified := w.dispatchBatches(batches)
	counters.Sent = len(sentToUser)

	log.Printf("[INFO] SentinelWorker.RunWithResult: completed in %s — fetched: %d, new: %d, matched: %d, "+
		"users notified: %d, delivered: %d",
		time.Since(start), counters.Fetched, counters.New, counters.Matched, usersNotified, counters.Sent)

	return SentinelRunResult{
		Fetched:    counters.Fetched,
		New:        counters.New,
		Matched:    counters.Matched,
		SentToUser: sentToUser,
	}, nil
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
// it isn't reprocessed on the next run.
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
// Telegram's 4096-character limit — see alertmsg.SentinelMessages), and records
// exactly one notification_logs entry per user for the run.
func (w *SentinelWorker) dispatchBatches(batches map[uint64]*SentinelBatch) (sentToUser map[uint64]int, usersNotified int) {
	sentToUser = make(map[uint64]int)
	if len(batches) == 0 {
		return sentToUser, 0
	}

	at := time.Now().In(w.loc)

	for _, batch := range batches {
		if len(batch.Items) == 0 {
			continue
		}
		if w.dispatchUserBatch(batch, at) {
			sentToUser[batch.UserID] = len(batch.Items)
		}
		usersNotified++
	}
	return sentToUser, usersNotified
}

// dispatchUserBatch sends batch as one or more Telegram messages (per
// alertmsg.SentinelMessages) and records exactly one notification_logs row for
// the whole batch, reporting whether every message part sent successfully.
func (w *SentinelWorker) dispatchUserBatch(batch *SentinelBatch, at time.Time) bool {
	messages := alertmsg.SentinelMessages(batch.Language, at, batch.Items)

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
		titles = append(titles, alertmsg.TruncateRunes(item.Title, sentinelTitleTruncateLen))
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
