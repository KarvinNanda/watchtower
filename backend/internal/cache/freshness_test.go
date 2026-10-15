package cache

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/asset"
	"github.com/karvin-nanda/watchtower/internal/currency"
)

type fakeFetcher struct {
	calls int
	res   *asset.FetchResult
	err   error
}

func (f *fakeFetcher) FetchAsset(symbol, _ string) (*asset.FetchResult, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	r := *f.res
	r.Symbol = symbol
	return &r, nil
}

func (f *fakeFetcher) FetchCryptoWeek52(string) (float64, float64, error) {
	return 0, 0, errors.New("unused")
}

type fakeBudget struct {
	allow bool
	err   error
	calls int
}

func (b *fakeBudget) TryUse(context.Context, int) (bool, error) {
	b.calls++
	return b.allow && b.err == nil, b.err
}

var marketColumns = []string{"symbol", "price_usd", "price_idr", "change_pct_24h", "week52_high_usd", "week52_low_usd", "high_usd", "high_since", "last_fetched", "source"}

// freshnessCache has `cached` (if not nil) in Redis, a 4 h TTL, a fixed
// clock and rate, and no MySQL row unless the test adds one.
func freshnessCache(t *testing.T, cached *MarketData, f *fakeFetcher, b *fakeBudget) (*Cache, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mr := miniredis.RunT(t)
	if cached != nil {
		raw, err := json.Marshal(cached)
		require.NoError(t, err)
		require.NoError(t, mr.Set(marketKey(cached.Symbol), string(raw)))
	}
	c := &Cache{
		redis: redis.NewClient(&redis.Options{Addr: mr.Addr()}), db: db, ttlHours: 4,
		fetcher: f, budget: b,
		now:    func() time.Time { return now },
		rateFn: func() currency.Rate { return currency.Rate{Value: 17908} },
	}
	return c, mock
}

func entry(symbol string, age time.Duration) *MarketData {
	return &MarketData{Symbol: symbol, PriceUSD: 100, PriceIDR: 1790800, LastFetched: now.Add(-age), Source: "test"}
}

var manual = FetchOptions{MaxAge: ManualMaxAge, Manual: true}

func expectUpsert(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`INSERT INTO market_cache`).WillReturnResult(sqlmock.NewResult(1, 1))
}

func TestGetMarketData_FreshEntryIsServedWithoutFetching(t *testing.T) {
	f := &fakeFetcher{res: &asset.FetchResult{PriceUSD: 200}}
	c, _ := freshnessCache(t, entry("BTC", 5*time.Minute), f, &fakeBudget{})
	d, err := c.GetMarketData(context.Background(), "BTC", "crypto", manual)
	require.NoError(t, err)
	assert.Equal(t, 100.0, d.PriceUSD)
	assert.Zero(t, f.calls)
	assert.False(t, d.Stale)
}

func TestGetMarketData_ManualRefreshesOldCryptoWithoutBudget(t *testing.T) {
	f := &fakeFetcher{res: &asset.FetchResult{PriceUSD: 200, FetchedAt: now}}
	b := &fakeBudget{}
	c, mock := freshnessCache(t, entry("BTC", 20*time.Minute), f, b)
	expectUpsert(mock)
	d, err := c.GetMarketData(context.Background(), "BTC", "crypto", manual)
	require.NoError(t, err)
	assert.Equal(t, 200.0, d.PriceUSD)
	assert.Equal(t, 1, f.calls)
	assert.Zero(t, b.calls, "only Twelve Data (stocks) spends the manual budget")
}

func TestGetMarketData_ManualStockWithBudgetRefreshes(t *testing.T) {
	f := &fakeFetcher{res: &asset.FetchResult{PriceUSD: 200, FetchedAt: now}}
	b := &fakeBudget{allow: true}
	c, mock := freshnessCache(t, entry("AAPL", 20*time.Minute), f, b)
	expectUpsert(mock)
	d, err := c.GetMarketData(context.Background(), "AAPL", "stock", manual)
	require.NoError(t, err)
	assert.Equal(t, 200.0, d.PriceUSD)
	assert.Equal(t, 1, b.calls)
}

func TestGetMarketData_ManualStockWithoutBudgetUsesCacheMarkedStale(t *testing.T) {
	f := &fakeFetcher{res: &asset.FetchResult{PriceUSD: 200}}
	c, _ := freshnessCache(t, entry("AAPL", 20*time.Minute), f, &fakeBudget{allow: false})
	d, err := c.GetMarketData(context.Background(), "AAPL", "stock", manual)
	require.NoError(t, err)
	assert.Equal(t, 100.0, d.PriceUSD)
	assert.True(t, d.Stale)
	assert.Zero(t, f.calls)
}

func TestGetMarketData_ManualStockBudgetErrorFallsBackToCache(t *testing.T) {
	f := &fakeFetcher{res: &asset.FetchResult{PriceUSD: 200}}
	c, _ := freshnessCache(t, entry("AAPL", 20*time.Minute), f, &fakeBudget{allow: true, err: errors.New("redis down")})
	d, err := c.GetMarketData(context.Background(), "AAPL", "stock", manual)
	require.NoError(t, err)
	assert.True(t, d.Stale, "Redis trouble must not spend unaccounted credits (fail closed)")
	assert.Zero(t, f.calls)
}

func TestGetMarketData_ManualStockWithoutBudgetOrUsableCacheIsAnError(t *testing.T) {
	f := &fakeFetcher{res: &asset.FetchResult{PriceUSD: 200}}
	c, mock := freshnessCache(t, nil, f, &fakeBudget{allow: false})
	mock.ExpectQuery(`FROM market_cache`).WithArgs("AAPL").WillReturnRows(sqlmock.NewRows(marketColumns))
	_, err := c.GetMarketData(context.Background(), "AAPL", "stock", manual)
	assert.Error(t, err)
	assert.Zero(t, f.calls)
}

func TestGetMarketData_ManualFetchErrorUsesCacheMarkedStale(t *testing.T) {
	f := &fakeFetcher{err: errors.New("coingecko 429")}
	c, _ := freshnessCache(t, entry("BTC", 20*time.Minute), f, &fakeBudget{})
	d, err := c.GetMarketData(context.Background(), "BTC", "crypto", manual)
	require.NoError(t, err)
	assert.True(t, d.Stale)
	assert.Equal(t, 100.0, d.PriceUSD)
}

func TestGetMarketData_PeriodicNeverUsesBudget(t *testing.T) {
	f := &fakeFetcher{res: &asset.FetchResult{PriceUSD: 200, FetchedAt: now}}
	b := &fakeBudget{allow: false}
	// 20 min old is fresh for a periodic run (TTL 4 h).
	c, _ := freshnessCache(t, entry("AAPL", 20*time.Minute), f, b)
	d, err := c.GetMarketData(context.Background(), "AAPL", "stock", FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, 100.0, d.PriceUSD)
	assert.Zero(t, b.calls)

	// An expired entry is refetched with no budget check at all.
	c2, mock := freshnessCache(t, nil, f, b)
	mock.ExpectQuery(`FROM market_cache`).WithArgs("AAPL").WillReturnRows(sqlmock.NewRows(marketColumns).
		AddRow("AAPL", 100.0, 1790800.0, nil, nil, nil, nil, nil, now.Add(-5*time.Hour), "test"))
	expectUpsert(mock)
	d, err = c2.GetMarketData(context.Background(), "AAPL", "stock", FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, 200.0, d.PriceUSD)
	assert.Zero(t, b.calls, "periodic runs never spend or wait for the manual budget")
}

func TestGetMarketData_PeriodicFetchErrorStaysAnError(t *testing.T) {
	f := &fakeFetcher{err: errors.New("down")}
	c, mock := freshnessCache(t, nil, f, &fakeBudget{})
	mock.ExpectQuery(`FROM market_cache`).WithArgs("BTC").WillReturnRows(sqlmock.NewRows(marketColumns).
		AddRow("BTC", 100.0, 1790800.0, nil, nil, nil, nil, nil, now.Add(-5*time.Hour), "test"))
	_, err := c.GetMarketData(context.Background(), "BTC", "crypto", FetchOptions{})
	assert.Error(t, err)
}

func TestGetMarketData_StoresTheHighAndItsWindow(t *testing.T) {
	high, since := 84025.0, now.Add(-24*time.Hour)
	f := &fakeFetcher{res: &asset.FetchResult{PriceUSD: 81211, HighUSD: &high, HighSince: &since, FetchedAt: now}}
	c, mock := freshnessCache(t, entry("BTC", 20*time.Minute), f, &fakeBudget{})
	mock.ExpectExec(`INSERT INTO market_cache`).
		WithArgs("BTC", 81211.0, 81211*17908.0, nil, nil, nil, high, since, now, "").
		WillReturnResult(sqlmock.NewResult(1, 1))

	d, err := c.GetMarketData(context.Background(), "BTC", "crypto", manual)

	require.NoError(t, err)
	require.NotNil(t, d.HighUSD)
	assert.Equal(t, high, *d.HighUSD)
	assert.Equal(t, since, *d.HighSince)
	assert.NoError(t, mock.ExpectationsWereMet())

	raw, err := c.redis.Get(context.Background(), marketKey("BTC")).Bytes()
	require.NoError(t, err)
	var back MarketData
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, high, *back.HighUSD)
	assert.False(t, back.Stale)
	assert.NotContains(t, string(raw), "stale", "Stale is never persisted")
}

func TestGetMarketData_ReadsTheHighFromMySQL(t *testing.T) {
	c, mock := freshnessCache(t, nil, &fakeFetcher{}, &fakeBudget{})
	since := now.Add(-3 * time.Hour)
	mock.ExpectQuery(`FROM market_cache`).WithArgs("BTC").WillReturnRows(sqlmock.NewRows(marketColumns).
		AddRow("BTC", 100.0, 1790800.0, nil, nil, nil, 105.0, since, now.Add(-time.Hour), "coingecko"))
	d, err := c.GetMarketData(context.Background(), "BTC", "crypto", FetchOptions{})
	require.NoError(t, err)
	require.NotNil(t, d.HighUSD)
	assert.Equal(t, 105.0, *d.HighUSD)
	assert.Equal(t, since, *d.HighSince)
}
