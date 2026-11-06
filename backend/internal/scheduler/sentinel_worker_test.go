package scheduler

import (
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/notifier"
	"github.com/karvin-nanda/watchtower/internal/sentinel"
)

type mockSentinelNotifier struct {
	failChatIDs map[int64]bool
	calls       []mockAssetNotifierCall
}

func (m *mockSentinelNotifier) SendSentinelAlert(chatID int64, message string) error {
	m.calls = append(m.calls, mockAssetNotifierCall{ChatID: chatID, Message: message})
	if m.failChatIDs[chatID] {
		return assert.AnError
	}
	return nil
}

func newMockSentinelWorker(t *testing.T) (*SentinelWorker, sqlmock.Sqlmock, *mockSentinelNotifier) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	n := &mockSentinelNotifier{failChatIDs: map[int64]bool{}}
	return &SentinelWorker{db: db, notifier: n, notifLogs: notifier.NewLogRepository(db), loc: time.UTC}, mock, n
}

func TestProcessItem_MatchedItemIsAlwaysBatched(t *testing.T) {
	w, mock, _ := newMockSentinelWorker(t)
	item := sentinel.SentinelItem{
		SourceType: "cisa_kev", Identifier: "CVE-2024-3094", Title: "XZ Utils backdoor",
		Description: "Embedded malicious code in xz on linux", URL: "https://nvd.nist.gov/vuln/detail/CVE-2024-3094",
		CVEID: "CVE-2024-3094", KnownExploited: true, RequiredAction: "Downgrade xz.",
	}

	mock.ExpectQuery(`FROM keyword_subscriptions ks`).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "keyword", "telegram_sentinel_chat_id", "preferred_language"}).
			AddRow(1, "xz", 100, "en").
			AddRow(1, "linux", 100, "en"))
	mock.ExpectExec(`INSERT INTO sentinel_seen_items`).
		WithArgs("cisa_kev", "CVE-2024-3094").
		WillReturnResult(sqlmock.NewResult(1, 1))

	counters := &ProcessCounters{}
	batches := map[uint64]*SentinelBatch{}
	require.NoError(t, w.processItem(item, counters, batches))

	require.Contains(t, batches, uint64(1))
	require.Len(t, batches[1].Items, 1, "one item matching two keywords of one user appears once")
	got := batches[1].Items[0]
	assert.ElementsMatch(t, []string{"xz", "linux"}, got.MatchedKeywords)
	assert.True(t, got.KnownExploited)
	assert.Equal(t, "Downgrade xz.", got.RequiredAction)
	assert.Equal(t, 1, counters.Matched)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSentinelDispatch_SentToUserOnlyCountsDelivered(t *testing.T) {
	w, mock, n := newMockSentinelWorker(t)
	n.failChatIDs[200] = true
	mock.ExpectExec(`INSERT INTO notification_logs`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO notification_logs`).WillReturnResult(sqlmock.NewResult(1, 1))

	batches := map[uint64]*SentinelBatch{
		1: {UserID: 1, ChatID: 100, Language: "en", Items: []SentinelBatchItem{
			{SourceType: "rss", Title: "a", URL: "https://a"}, {SourceType: "rss", Title: "b", URL: "https://b"}}},
		2: {UserID: 2, ChatID: 200, Language: "en", Items: []SentinelBatchItem{
			{SourceType: "rss", Title: "c", URL: "https://c"}}},
	}

	sentToUser, usersNotified := w.dispatchBatches(batches)
	assert.Equal(t, map[uint64]int{1: 2}, sentToUser)
	assert.Equal(t, 2, usersNotified)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSentinelDispatch_RendersWithAlertmsgInInstanceTimezone(t *testing.T) {
	w, mock, n := newMockSentinelWorker(t)
	w.loc = time.FixedZone("TST", 3*60*60)
	mock.ExpectExec(`INSERT INTO notification_logs`).WillReturnResult(sqlmock.NewResult(1, 1))

	w.dispatchBatches(map[uint64]*SentinelBatch{
		1: {UserID: 1, ChatID: 100, Language: "en", Items: []SentinelBatchItem{
			{SourceType: "rss", Title: "a", URL: "https://a.example"},
			{SourceType: "cisa_kev", Title: "b", CVEID: "CVE-2026-1", KnownExploited: true, URL: "https://b.example"}}},
	})

	require.Len(t, n.calls, 1)
	msg := n.calls[0].Message
	assert.True(t, strings.HasPrefix(msg, "🛡️ *Sentinel* · 2 items \\(1 actively exploited\\)\n"), msg)
	assert.Regexp(t, `⏰ \d{2} \w{3} \d{4} \d{2}:\d{2} TST\n`, msg)
	assert.Less(t, strings.Index(msg, "CVE\\-2026\\-1"), strings.Index(msg, "*a*"), "KEV first")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func expectSeenLookup(mock sqlmock.Sqlmock, source, id string, seen bool) {
	rows := sqlmock.NewRows([]string{"id"})
	if seen {
		rows.AddRow(1)
	}
	mock.ExpectQuery(`SELECT id FROM sentinel_seen_items`).WithArgs(source, id).WillReturnRows(rows)
}

func expectMatchAndInserts(mock sqlmock.Sqlmock, id string, sources ...string) {
	mock.ExpectQuery(`FROM keyword_subscriptions ks`).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "keyword", "telegram_sentinel_chat_id", "preferred_language"}).
			AddRow(1, "x", 100, "en"))
	for _, s := range sources {
		mock.ExpectExec(`INSERT INTO sentinel_seen_items`).
			WithArgs(s, id).
			WillReturnResult(sqlmock.NewResult(1, 1))
	}
}

func mergedItem(id string) sentinel.SentinelItem {
	return sentinel.SentinelItem{SourceType: "cve", Identifier: id, CVEID: id, Title: "t",
		Severity: "HIGH", CVSSScore: 8, KnownExploited: true}
}

func TestFetchAndProcess_MergedItemSeenAsCVEButNotKEV_IsSent(t *testing.T) {
	w, mock, _ := newMockSentinelWorker(t)
	w.fetcher = &fakeSentinelSource{items: []sentinel.SentinelItem{mergedItem("CVE-2026-0030")}}

	// Only the KEV key is looked up, and (cve, id) is never queried.
	expectSeenLookup(mock, "cisa_kev", "CVE-2026-0030", false)
	expectMatchAndInserts(mock, "CVE-2026-0030", "cve", "cisa_kev")

	batches := map[uint64]*SentinelBatch{}
	counters := &ProcessCounters{}
	require.NoError(t, w.fetchAndProcess(counters, batches))
	require.Contains(t, batches, uint64(1))
	assert.Len(t, batches[1].Items, 1)
	assert.Equal(t, 1, counters.New)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFetchAndProcess_MergedItemSeenAsKEV_IsSkipped(t *testing.T) {
	w, mock, _ := newMockSentinelWorker(t)
	w.fetcher = &fakeSentinelSource{items: []sentinel.SentinelItem{mergedItem("CVE-2026-0031")}}

	expectSeenLookup(mock, "cisa_kev", "CVE-2026-0031", true)

	batches := map[uint64]*SentinelBatch{}
	counters := &ProcessCounters{}
	require.NoError(t, w.fetchAndProcess(counters, batches))
	assert.Empty(t, batches)
	assert.Equal(t, 0, counters.New)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProcessItem_MergedItemInsertsBothKeysOnEveryPath(t *testing.T) {
	t.Run("no match", func(t *testing.T) {
		w, mock, _ := newMockSentinelWorker(t)
		mock.ExpectQuery(`FROM keyword_subscriptions ks`).
			WillReturnRows(sqlmock.NewRows([]string{"user_id", "keyword", "telegram_sentinel_chat_id", "preferred_language"}))
		mock.ExpectExec(`INSERT INTO sentinel_seen_items`).WithArgs("cve", "CVE-2026-0032").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(`INSERT INTO sentinel_seen_items`).WithArgs("cisa_kev", "CVE-2026-0032").WillReturnResult(sqlmock.NewResult(1, 1))
		require.NoError(t, w.processItem(mergedItem("CVE-2026-0032"), &ProcessCounters{}, map[uint64]*SentinelBatch{}))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("matched", func(t *testing.T) {
		w, mock, _ := newMockSentinelWorker(t)
		expectMatchAndInserts(mock, "CVE-2026-0033", "cve", "cisa_kev")
		batches := map[uint64]*SentinelBatch{}
		require.NoError(t, w.processItem(mergedItem("CVE-2026-0033"), &ProcessCounters{}, batches))
		assert.Len(t, batches, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestProcessItem_PlainCVEItemInsertsOneKey(t *testing.T) {
	w, mock, _ := newMockSentinelWorker(t)
	item := mergedItem("CVE-2026-0034")
	item.KnownExploited = false
	expectMatchAndInserts(mock, "CVE-2026-0034", "cve")
	require.NoError(t, w.processItem(item, &ProcessCounters{}, map[uint64]*SentinelBatch{}))
	assert.NoError(t, mock.ExpectationsWereMet())
}
