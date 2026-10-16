package scheduler

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/cache"
	"github.com/karvin-nanda/watchtower/internal/notifier"
)

// sqlSeen records every statement the code sends, including ones no
// expectation matched: sqlmock only returns an error for those, so a caller
// that ignores the error would hide a write from ExpectationsWereMet.
type sqlSeen struct {
	mu    sync.Mutex
	stmts []string
}

func (s *sqlSeen) Match(expected, actual string) error {
	s.mu.Lock()
	s.stmts = append(s.stmts, actual)
	s.mu.Unlock()
	return sqlmock.QueryMatcherRegexp.Match(expected, actual)
}

func newRecordingAssetWorker(t *testing.T) (*AssetWorker, sqlmock.Sqlmock, *mockAssetNotifier, *sqlSeen) {
	t.Helper()
	seen := &sqlSeen{}
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(seen))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	w, _, notif := newMockAssetWorker(t, 3)
	w.db, w.notifLogs = db, notifier.NewLogRepository(db)
	return w, mock, notif, seen
}

func expectReportSymbols(mock sqlmock.Sqlmock, symbols ...[2]string) {
	rows := sqlmock.NewRows([]string{"asset_symbol", "asset_type", "needs_week52"})
	for _, s := range symbols {
		rows.AddRow(s[0], s[1], false)
	}
	mock.ExpectQuery(`FROM asset_rules[\s\S]*AND user_id = \?`).WithArgs(7, 100).WillReturnRows(rows)
}

// One active "above 100" USD rule of user 7; last_beyond false, so a price
// of 150 is a crossing that Check now would fire.
func expectReportRule(mock sqlmock.Sqlmock, symbol string, ruleID int64) {
	mock.ExpectQuery(`FROM asset_rules r`).WithArgs(symbol, 7).WillReturnRows(sqlmock.NewRows(ruleColumns).
		AddRow(ruleID, int64(7), "price_level", "above", nil, 100.0, nil, int64(1), false, false, nil, nil, nil, nil, nil, int64(1000), int64(4), "en", "USD", nil))
}

// Review focus 1: the report never evaluates, so a crossed level stays
// unreported in rule state and the next Check now still fires it.
func TestReportForUser_SendsOneReportAndWritesNoRuleState(t *testing.T) {
	w, mock, notif, seen := newRecordingAssetWorker(t)
	w.cache = &fakeMarket{data: map[string]*cache.MarketData{
		"BTC": {Symbol: "BTC", PriceUSD: 150},
		"ETH": {Symbol: "ETH", PriceUSD: 90},
	}}
	expectReportSymbols(mock, [2]string{"BTC", "crypto"}, [2]string{"ETH", "crypto"})
	expectReportRule(mock, "BTC", 1)
	expectReportRule(mock, "ETH", 2)
	mock.ExpectExec(`INSERT INTO notification_logs`).
		WithArgs(7, string(notifier.TypeAsset), nil, nil, "status report: 2 symbols", string(notifier.StatusSent)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	res, err := w.ReportForUser(7)

	require.NoError(t, err)
	assert.Equal(t, AssetReportResult{Symbols: 2, Rules: 2, Sent: 1}, res)
	require.Len(t, notif.calls, 1)
	assert.Equal(t, int64(1000), notif.calls[0].ChatID)
	assert.Contains(t, notif.calls[0].Message, "Asset status report")
	assert.Contains(t, notif.calls[0].Message, "passed")
	assert.NoError(t, mock.ExpectationsWereMet())
	for _, stmt := range seen.stmts {
		assert.NotContains(t, strings.ToUpper(stmt), "UPDATE", "the report must never write rule state")
	}
}

func TestReportForUser_UnavailablePriceIsListed(t *testing.T) {
	w, mock, notif := newMockAssetWorker(t, 3)
	w.cache = &fakeMarket{data: map[string]*cache.MarketData{"BTC": {Symbol: "BTC", PriceUSD: 150}}}
	expectReportSymbols(mock, [2]string{"BTC", "crypto"}, [2]string{"ETH", "crypto"})
	expectReportRule(mock, "BTC", 1)
	expectReportRule(mock, "ETH", 2)
	mock.ExpectExec(`INSERT INTO notification_logs`).WillReturnResult(sqlmock.NewResult(1, 1))

	res, err := w.ReportForUser(7)

	require.NoError(t, err)
	// Rules counts only rendered rules, like the Telegram header: the
	// unavailable symbol lists none.
	assert.Equal(t, AssetReportResult{Symbols: 2, Rules: 1, Sent: 1, Errors: 1}, res)
	require.Len(t, notif.calls, 1)
	assert.Contains(t, notif.calls[0].Message, "price unavailable")
}

func TestReportForUser_StaleDataIsMarked(t *testing.T) {
	w, mock, notif := newMockAssetWorker(t, 3)
	w.cache = &fakeMarket{stale: true, data: map[string]*cache.MarketData{
		"BTC": {Symbol: "BTC", PriceUSD: 150, LastFetched: time.Now().Add(-2 * time.Hour)},
	}}
	expectReportSymbols(mock, [2]string{"BTC", "crypto"})
	expectReportRule(mock, "BTC", 1)
	mock.ExpectExec(`INSERT INTO notification_logs`).WillReturnResult(sqlmock.NewResult(1, 1))

	res, err := w.ReportForUser(7)

	require.NoError(t, err)
	assert.Equal(t, 1, res.Stale)
	require.Len(t, notif.calls, 1)
	assert.Contains(t, notif.calls[0].Message, `data from 2 hour\(s\) ago`, "MarkdownV2-escaped")
}

func TestReportForUser_AsksForFreshData(t *testing.T) {
	w, mock, _ := newMockAssetWorker(t, 3)
	fm := &fakeMarket{data: map[string]*cache.MarketData{"BTC": {Symbol: "BTC", PriceUSD: 150}}}
	w.cache = fm
	expectReportSymbols(mock, [2]string{"BTC", "crypto"})
	expectReportRule(mock, "BTC", 1)
	mock.ExpectExec(`INSERT INTO notification_logs`).WillReturnResult(sqlmock.NewResult(1, 1))

	_, err := w.ReportForUser(7)

	require.NoError(t, err)
	assert.Equal(t, cache.FetchOptions{MaxAge: cache.ManualMaxAge, Manual: true}, fm.lastOpts)
}

// Review focus 2: a chat unlinked after the click means loadRules finds
// nothing (it requires a chat id), so nothing is sent.
func TestReportForUser_NoLinkedRulesSendsNothing(t *testing.T) {
	w, mock, notif := newMockAssetWorker(t, 3)
	w.cache = &fakeMarket{data: map[string]*cache.MarketData{"BTC": {Symbol: "BTC", PriceUSD: 150}}}
	expectReportSymbols(mock, [2]string{"BTC", "crypto"})
	mock.ExpectQuery(`FROM asset_rules r`).WithArgs("BTC", 7).WillReturnRows(sqlmock.NewRows(ruleColumns))

	res, err := w.ReportForUser(7)

	require.NoError(t, err)
	// Active rules exist but none could be loaded: an error, so the panel
	// does not claim there are no active rules.
	assert.Equal(t, AssetReportResult{Errors: 1}, res)
	assert.Empty(t, notif.calls)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReportForUser_NoActiveRulesSendsNothing(t *testing.T) {
	w, mock, notif := newMockAssetWorker(t, 3)
	expectReportSymbols(mock)

	res, err := w.ReportForUser(7)

	require.NoError(t, err)
	assert.Equal(t, AssetReportResult{}, res)
	assert.Empty(t, notif.calls)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReportForUser_FailedSendIsLogged(t *testing.T) {
	w, mock, notif := newMockAssetWorker(t, 3)
	notif.err = assert.AnError
	w.cache = &fakeMarket{data: map[string]*cache.MarketData{"BTC": {Symbol: "BTC", PriceUSD: 150}}}
	expectReportSymbols(mock, [2]string{"BTC", "crypto"})
	expectReportRule(mock, "BTC", 1)
	mock.ExpectExec(`INSERT INTO notification_logs`).
		WithArgs(7, string(notifier.TypeAsset), nil, nil, "status report: 1 symbols", string(notifier.StatusFailed)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	res, err := w.ReportForUser(7)

	require.NoError(t, err)
	assert.Equal(t, 0, res.Sent)
	assert.Equal(t, 1, res.Symbols)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReportForUser_WaitsForTheRunLock(t *testing.T) {
	w, mock, _ := newMockAssetWorker(t, 3)
	expectReportSymbols(mock)

	w.mu.Lock()
	done := make(chan struct{})
	go func() {
		_, err := w.ReportForUser(7)
		assert.NoError(t, err)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("ReportForUser ran while an asset run held the lock")
	case <-time.After(100 * time.Millisecond):
	}
	w.mu.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ReportForUser never ran after the lock was released")
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}
