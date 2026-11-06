package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/asset"
)

func refFetcher(closePrice float64) *fakeFetcher {
	return &fakeFetcher{ref: asset.DailyClose{Day: now.AddDate(0, 0, -7), Close: closePrice}}
}

func TestStockChange7d_CachesReferenceUntilMidnight(t *testing.T) {
	f := refFetcher(100)
	c, _ := freshnessCache(t, nil, f, &fakeBudget{})
	got := c.stockChange7d(context.Background(), "AAPL", 103.5, false)
	require.NotNil(t, got)
	assert.Equal(t, 1, f.refCalls)

	ttl, err := c.redis.TTL(context.Background(), ref7dKey("AAPL")).Result()
	require.NoError(t, err)
	assert.Equal(t, untilNextUTCMidnight(now), ttl)

	again := c.stockChange7d(context.Background(), "AAPL", 103.5, false)
	require.NotNil(t, again)
	assert.Equal(t, 1, f.refCalls, "second call is served from Redis")
	assert.Equal(t, *got, *again)
}

func TestStockChange7d_ManualBudgetRefused(t *testing.T) {
	f := refFetcher(100)
	b := &fakeBudget{allow: false}
	c, _ := freshnessCache(t, nil, f, b)
	assert.Nil(t, c.stockChange7d(context.Background(), "AAPL", 103.5, true))
	assert.Equal(t, 1, b.calls)
	assert.Zero(t, f.refCalls)
}

func TestStockChange7d_ManualSpendsItsOwnCredit(t *testing.T) {
	f := refFetcher(100)
	b := &fakeBudget{allow: true}
	c, _ := freshnessCache(t, nil, f, b)
	assert.NotNil(t, c.stockChange7d(context.Background(), "AAPL", 103.5, true))
	assert.Equal(t, 1, b.calls)
	assert.Equal(t, 1, f.refCalls)
}

func TestStockChange7d_FetchFails(t *testing.T) {
	f := &fakeFetcher{refErr: errors.New("twelvedata 429")}
	c, _ := freshnessCache(t, nil, f, &fakeBudget{})
	assert.Nil(t, c.stockChange7d(context.Background(), "AAPL", 103.5, false))
	n, err := c.redis.Exists(context.Background(), ref7dKey("AAPL")).Result()
	require.NoError(t, err)
	assert.Zero(t, n, "a failed fetch stores nothing so the next refresh retries")
}

func TestStockChange7d_Value(t *testing.T) {
	c, _ := freshnessCache(t, nil, refFetcher(100), &fakeBudget{})
	got := c.stockChange7d(context.Background(), "AAPL", 103.5, false)
	require.NotNil(t, got)
	assert.InDelta(t, 3.5, *got, 1e-9)
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
	assert.Zero(t, f.refCalls)
}

func TestUntilNextUTCMidnight(t *testing.T) {
	d := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, 24*time.Hour, untilNextUTCMidnight(d))
	assert.Equal(t, time.Minute, untilNextUTCMidnight(d.Add(23*time.Hour+59*time.Minute+30*time.Second)))
	assert.Equal(t, 6*time.Hour, untilNextUTCMidnight(d.Add(18*time.Hour)))
}
