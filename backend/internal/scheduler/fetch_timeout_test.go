package scheduler

import (
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/cache"
)

// shortDBTimeout makes dbQueryTimeout 50 ms for one test. Tests that call
// it must not be parallel: they change a package variable.
func shortDBTimeout(t *testing.T) {
	t.Helper()
	old := dbQueryTimeout
	dbQueryTimeout = 50 * time.Millisecond
	t.Cleanup(func() { dbQueryTimeout = old })
}

// #86: a fetch slower than the DB timeout must not skip the symbol's
// rules. The fetch has its own deadline, and the DB context starts after it.
func TestProcessSymbol_SlowFetchStillEvaluatesRules(t *testing.T) {
	shortDBTimeout(t)
	w, mock, _ := newMockAssetWorker(t, 3)
	w.cache = &fakeMarket{delay: 100 * time.Millisecond, data: map[string]*cache.MarketData{"BTC": {Symbol: "BTC", PriceUSD: 150}}}
	btcRuleRows(mock)
	batches := map[uint64]*UserAlertBatch{}

	res, err := w.processSymbol(SymbolInfo{Symbol: "BTC", AssetType: "crypto"}, runScope{}, batches)

	require.NoError(t, err)
	assert.Equal(t, symbolResult{checked: 1, triggered: 1}, res)
	require.Len(t, batches[1].Alerts, 1, "the rule was loaded and evaluated")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// #86: a slow 52-week fetch runs under the fetch context, and the rule
// state writes after it still get a full DB deadline.
func TestProcessSymbol_SlowWeek52StillWritesRuleState(t *testing.T) {
	shortDBTimeout(t)
	w, mock, _ := newMockAssetWorker(t, 3)
	w.cache = &fakeMarket{delay: 100 * time.Millisecond, data: map[string]*cache.MarketData{"BTC": {Symbol: "BTC", PriceUSD: 90}}}
	btcRuleRows(mock)
	mock.ExpectExec(`UPDATE asset_rules SET last_beyond = \?`).WithArgs(false, 1, 1).WillReturnResult(sqlmock.NewResult(0, 1))

	res, err := w.processSymbol(SymbolInfo{Symbol: "BTC", AssetType: "crypto", NeedsWeek52: true}, runScope{}, map[uint64]*UserAlertBatch{})

	require.NoError(t, err)
	assert.Equal(t, symbolResult{checked: 1}, res, "no data error: the 52-week range was fetched")
	assert.NoError(t, mock.ExpectationsWereMet(), "the rule state write must not hit an expired context")
}

// #86: the status report's fetch has its own deadline too, so a fetch
// slower than the DB timeout still reports the symbol's rules.
func TestReportSymbol_SlowFetchStillLoadsRules(t *testing.T) {
	shortDBTimeout(t)
	w, mock, _, _ := newRecordingAssetWorker(t)
	w.cache = &fakeMarket{delay: 100 * time.Millisecond, data: map[string]*cache.MarketData{"BTC": {Symbol: "BTC", PriceUSD: 150}}}
	expectReportRule(mock, "BTC", 1)

	item, rows, err := w.reportSymbol(SymbolInfo{Symbol: "BTC", AssetType: "crypto"}, 7)

	require.NoError(t, err)
	assert.False(t, item.Unavailable, "the price must not be reported as unavailable")
	assert.Len(t, rows, 1)
	require.Len(t, item.Rules, 1, "the rule is listed with its distance")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMarketFetchTimeout(t *testing.T) {
	// Two 8 s pacer waits, one 60 s rate-limit retry and the closes fetch.
	assert.Equal(t, 150*time.Second, marketFetchTimeout, "worst case: 8 s pacer + 30 s HTTP + 60 s retry + 8 s pacer + 30 s HTTP = 136 s")
}
