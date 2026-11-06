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
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// weekdayCloses returns one close per weekday from `to` back to `from`,
// newest first, with Close = 100 + day of month so each day is unique.
func weekdayCloses(from, to time.Time) []asset.DailyClose {
	var out []asset.DailyClose
	for d := to; !d.Before(from); d = d.AddDate(0, 0, -1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		out = append(out, asset.DailyClose{Day: d, Close: float64(100 + d.Day())})
	}
	return out
}

// twoCloses has a 7-day reference of 100 for session 2026-10-14.
func twoCloses() *fakeFetcher {
	return &fakeFetcher{closes: []asset.DailyClose{
		{Day: day(2026, 10, 14), Close: 110},
		{Day: day(2026, 10, 7), Close: 100},
	}}
}

var session = day(2026, 10, 14)

func cacheCloses(t *testing.T, c *Cache, symbol string, closes []asset.DailyClose) {
	t.Helper()
	raw, err := json.Marshal(closes)
	require.NoError(t, err)
	require.NoError(t, c.redis.Set(context.Background(), closes7dKey(symbol), raw, time.Hour).Err())
}

func TestStockChange7d_CachesClosesUntilMidnight(t *testing.T) {
	f := twoCloses()
	c, _ := freshnessCache(t, nil, f, &fakeBudget{})
	got := c.stockChange7d(context.Background(), "AAPL", 103.5, session, false)
	require.NotNil(t, got)
	assert.Equal(t, 1, f.closesCalls)

	ttl, err := c.redis.TTL(context.Background(), closes7dKey("AAPL")).Result()
	require.NoError(t, err)
	assert.Equal(t, untilNextUTCMidnight(now), ttl)

	again := c.stockChange7d(context.Background(), "AAPL", 103.5, session, false)
	require.NotNil(t, again)
	assert.Equal(t, 1, f.closesCalls, "second call is served from Redis")
	assert.Equal(t, *got, *again)
}

func TestStockChange7d_IgnoresOldRef7dKey(t *testing.T) {
	f := twoCloses()
	c, _ := freshnessCache(t, nil, f, &fakeBudget{})
	require.NoError(t, c.redis.Set(context.Background(), "asset:ref7d:AAPL", `{"close":50,"day":"2026-10-07T00:00:00Z"}`, time.Hour).Err())
	got := c.stockChange7d(context.Background(), "AAPL", 103.5, session, false)
	require.NotNil(t, got)
	assert.InDelta(t, 3.5, *got, 1e-9)
	assert.Equal(t, 1, f.closesCalls)
}

func TestStockChange7d_PreMarketUsesPreviousSession(t *testing.T) {
	f := &fakeFetcher{}
	c, _ := freshnessCache(t, nil, f, &fakeBudget{})
	c.now = func() time.Time { return time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC) }
	cacheCloses(t, c, "AAPL", weekdayCloses(day(2026, 10, 26), day(2026, 11, 9)))

	// Tue 10:00 UTC is before the US open: the price is Monday's close.
	got := c.stockChange7d(context.Background(), "AAPL", 110, day(2026, 11, 9), false)

	require.NotNil(t, got)
	assert.InDelta(t, (110.0-102.0)/102.0*100, *got, 1e-9, "reference is Mon 2026-11-02, not Tue 2026-11-03")
	assert.Zero(t, f.closesCalls)
}

func TestStockChange7d_InSession(t *testing.T) {
	f := &fakeFetcher{}
	c, _ := freshnessCache(t, nil, f, &fakeBudget{})
	c.now = func() time.Time { return time.Date(2026, 11, 10, 16, 0, 0, 0, time.UTC) }
	cacheCloses(t, c, "AAPL", weekdayCloses(day(2026, 10, 26), day(2026, 11, 9)))

	got := c.stockChange7d(context.Background(), "AAPL", 110, day(2026, 11, 10), false)

	require.NotNil(t, got)
	assert.InDelta(t, (110.0-103.0)/103.0*100, *got, 1e-9, "reference is Tue 2026-11-03")
	assert.Zero(t, f.closesCalls)
}

func TestStockChange7d_ZeroSessionUsesNewestBar(t *testing.T) {
	c, _ := freshnessCache(t, nil, twoCloses(), &fakeBudget{})
	got := c.stockChange7d(context.Background(), "AAPL", 103.5, time.Time{}, false)
	require.NotNil(t, got)
	assert.InDelta(t, 3.5, *got, 1e-9)
}

func TestStockChange7d_NoQualifyingClose(t *testing.T) {
	f := &fakeFetcher{closes: []asset.DailyClose{{Day: session, Close: 110}}}
	c, _ := freshnessCache(t, nil, f, &fakeBudget{})
	assert.Nil(t, c.stockChange7d(context.Background(), "AAPL", 103.5, session, false))
}

func TestStockChange7d_RedisReadErrorNoFetch(t *testing.T) {
	f := twoCloses()
	c, _ := freshnessCache(t, nil, f, &fakeBudget{})
	mr := miniredis.RunT(t)
	mr.SetError("LOADING Redis is loading the dataset in memory")
	c.redis = redis.NewClient(&redis.Options{Addr: mr.Addr()})

	assert.Nil(t, c.stockChange7d(context.Background(), "AAPL", 103.5, session, false))
	assert.Zero(t, f.closesCalls, "Redis down must not spend a Twelve Data credit on every refresh")
}

func TestStockChange7d_OwnContext(t *testing.T) {
	f := twoCloses()
	c, _ := freshnessCache(t, nil, f, &fakeBudget{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := c.stockChange7d(ctx, "AAPL", 103.5, session, false)

	require.NotNil(t, got)
	assert.InDelta(t, 3.5, *got, 1e-9)
	assert.Equal(t, 1, f.closesCalls)
	n, err := c.redis.Exists(context.Background(), closes7dKey("AAPL")).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "the Redis write does not use the caller's context either")
}

func TestStockChange7d_ManualBudgetRefused(t *testing.T) {
	f := twoCloses()
	b := &fakeBudget{allow: false}
	c, _ := freshnessCache(t, nil, f, b)
	assert.Nil(t, c.stockChange7d(context.Background(), "AAPL", 103.5, session, true))
	assert.Equal(t, 1, b.calls)
	assert.Zero(t, f.closesCalls)
}

func TestStockChange7d_ManualSpendsItsOwnCredit(t *testing.T) {
	f := twoCloses()
	b := &fakeBudget{allow: true}
	c, _ := freshnessCache(t, nil, f, b)
	assert.NotNil(t, c.stockChange7d(context.Background(), "AAPL", 103.5, session, true))
	assert.Equal(t, 1, b.calls)
	assert.Equal(t, 1, f.closesCalls)
}

func TestStockChange7d_FetchFails(t *testing.T) {
	f := &fakeFetcher{closesErr: errors.New("twelvedata 429")}
	c, _ := freshnessCache(t, nil, f, &fakeBudget{})
	assert.Nil(t, c.stockChange7d(context.Background(), "AAPL", 103.5, session, false))
	n, err := c.redis.Exists(context.Background(), closes7dKey("AAPL")).Result()
	require.NoError(t, err)
	assert.Zero(t, n, "a failed fetch stores nothing so the next refresh retries")
}

func TestStockChange7d_Value(t *testing.T) {
	c, _ := freshnessCache(t, nil, twoCloses(), &fakeBudget{})
	got := c.stockChange7d(context.Background(), "AAPL", 103.5, session, false)
	require.NotNil(t, got)
	assert.InDelta(t, 3.5, *got, 1e-9)
}

func TestRefresh_StockPassesSessionDay(t *testing.T) {
	f := &fakeFetcher{
		res:    &asset.FetchResult{PriceUSD: 110, FetchedAt: now, SessionDay: day(2026, 11, 9)},
		closes: weekdayCloses(day(2026, 10, 26), day(2026, 11, 9)),
	}
	c, mock := freshnessCache(t, nil, f, &fakeBudget{})
	c.now = func() time.Time { return time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC) }
	expectUpsert(mock)

	d, err := c.refresh(context.Background(), "AAPL", "stock", false)

	require.NoError(t, err)
	require.NotNil(t, d.ChangePct7d)
	assert.InDelta(t, (110.0-102.0)/102.0*100, *d.ChangePct7d, 1e-9)
}

func TestPickWeekSnapshot(t *testing.T) {
	day := 24 * time.Hour
	at := func(d float64) time.Time { return now.Add(-time.Duration(d * float64(day))) }
	snaps := []Snapshot{
		{Price: 1, ObservedAt: at(5.9)}, // too recent
		{Price: 2, ObservedAt: at(6.2)},
		{Price: 3, ObservedAt: at(7.1)}, // closest to 7 d
		{Price: 4, ObservedAt: at(8.1)}, // too old
	}
	got, ok := pickWeekSnapshot(snaps, now)
	require.True(t, ok)
	assert.Equal(t, 3.0, got.Price)

	_, ok = pickWeekSnapshot([]Snapshot{snaps[0], snaps[3]}, now)
	assert.False(t, ok)
}

func TestGoldChangeFromSnapshots_DayAndWeek(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	c := &Cache{db: db}

	mock.ExpectExec(`INSERT INTO price_snapshots`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`DELETE FROM price_snapshots`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT price, observed_at FROM price_snapshots`).
		WithArgs("ANTAM", now.Add(-weekWindowMax), now.Add(-changeWindowMin)).
		WillReturnRows(sqlmock.NewRows([]string{"price", "observed_at"}).
			AddRow(2300000.0, now.Add(-7*24*time.Hour)).
			AddRow(2370000.0, now.Add(-24*time.Hour)))

	day, week, err := c.goldChangeFromSnapshots(context.Background(), "ANTAM", 2400000, now)
	require.NoError(t, err)
	require.NotNil(t, day)
	require.NotNil(t, week)
	assert.InDelta(t, (2400000.0-2370000.0)/2370000.0*100, *day, 1e-9)
	assert.InDelta(t, (2400000.0-2300000.0)/2300000.0*100, *week, 1e-9)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRefresh_CryptoCopiesChangePct7d(t *testing.T) {
	w := 4.2
	f := &fakeFetcher{res: &asset.FetchResult{PriceUSD: 200, FetchedAt: now, ChangePct7d: &w}}
	c, mock := freshnessCache(t, nil, f, &fakeBudget{})
	expectUpsert(mock)
	d, err := c.refresh(context.Background(), "BTC", "crypto", false)
	require.NoError(t, err)
	require.NotNil(t, d.ChangePct7d)
	assert.Equal(t, 4.2, *d.ChangePct7d)
	assert.Zero(t, f.closesCalls)
}

func TestUntilNextUTCMidnight(t *testing.T) {
	d := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, 24*time.Hour, untilNextUTCMidnight(d))
	assert.Equal(t, time.Minute, untilNextUTCMidnight(d.Add(23*time.Hour+59*time.Minute+30*time.Second)))
	assert.Equal(t, 6*time.Hour, untilNextUTCMidnight(d.Add(18*time.Hour)))
}
