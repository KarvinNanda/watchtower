package scheduler

import (
	"testing"

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
	return &SentinelWorker{db: db, notifier: n, notifLogs: notifier.NewLogRepository(db)}, mock, n
}

func TestProcessItem_MatchedItemIsAlwaysBatched(t *testing.T) {
	w, mock, _ := newMockSentinelWorker(t)
	item := sentinel.SentinelItem{
		SourceType: "cisa_kev", Identifier: "CVE-2024-3094", Title: "XZ Utils backdoor",
		Description: "Embedded malicious code in xz on linux", URL: "https://nvd.nist.gov/vuln/detail/CVE-2024-3094",
		CVEID: "CVE-2024-3094", KnownExploited: true, RequiredAction: "Downgrade xz.",
	}

	mock.ExpectQuery(`SELECT id FROM sentinel_seen_items`).
		WithArgs("cisa_kev", "CVE-2024-3094").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
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

func TestBuildSentinelItemBlock_KEV(t *testing.T) {
	block := buildSentinelItemBlock("en", SentinelBatchItem{
		SourceType: "cisa_kev", Title: "Ivanti Connect Secure Auth Bypass", Description: "Remote unauthenticated attacker.",
		URL: "https://nvd.nist.gov/vuln/detail/CVE-2026-104935", CVEID: "CVE-2026-104935",
		KnownExploited: true, RequiredAction: "Apply mitigations.", DueDate: "2026-10-16", Ransomware: true,
		MatchedKeywords: []string{"ivanti"},
	})
	assert.Contains(t, block, "ACTIVELY EXPLOITED")
	assert.Contains(t, block, "CVE\\-2026\\-104935")
	assert.Contains(t, block, "ACTION: Apply mitigations\\.")
	assert.Contains(t, block, "RANSOMWARE")
	assert.Contains(t, block, "KEYWORD: ivanti")
	assert.NotContains(t, block, "DAMPAK")
}

func TestBuildSentinelItemBlock_UnscoredCVEShowsNA(t *testing.T) {
	block := buildSentinelItemBlock("en", SentinelBatchItem{
		SourceType: "cve", Title: "CVE-2026-99999: new flaw", URL: "https://nvd.nist.gov/vuln/detail/CVE-2026-99999",
		CVEID: "CVE-2026-99999", MatchedKeywords: []string{"flaw"},
	})
	assert.Contains(t, block, "SEVERITY: N/A")
}

func TestBuildSentinelItemBlock_ScoredAndIndonesianLabels(t *testing.T) {
	block := buildSentinelItemBlock("id", SentinelBatchItem{
		SourceType: "cve", Title: "xz backdoor", URL: "https://example.com", CVEID: "CVE-2024-3094",
		Severity: "CRITICAL", CVSSScore: 10, MatchedKeywords: []string{"xz"},
	})
	assert.Contains(t, block, "TINGKAT BAHAYA: CRITICAL \\(10\\.0\\)")
}

func TestBuildSentinelItemBlock_RSSHasNoSeverityLine(t *testing.T) {
	block := buildSentinelItemBlock("en", SentinelBatchItem{
		SourceType: "rss", Title: "Some news", URL: "https://example.com/a", MatchedKeywords: []string{"news"},
	})
	assert.NotContains(t, block, "SEVERITY")
	assert.Contains(t, block, "SOURCE: RSS")
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
