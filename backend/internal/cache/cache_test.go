package cache

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/asset"
)

var now = time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)

func TestPickDaySnapshot(t *testing.T) {
	snaps := []Snapshot{
		{Price: 1, ObservedAt: now.Add(-30 * time.Hour)}, // too old
		{Price: 2, ObservedAt: now.Add(-21 * time.Hour)},
		{Price: 3, ObservedAt: now.Add(-25 * time.Hour)}, // closest to 24 h
		{Price: 4, ObservedAt: now.Add(-2 * time.Hour)},  // too recent
	}
	got, ok := pickDaySnapshot(snaps, now)
	require.True(t, ok)
	assert.Equal(t, 3.0, got.Price)

	_, ok = pickDaySnapshot(snaps[3:], now)
	assert.False(t, ok, "nothing in the 20-28 h window means the change is unknown")
}

func TestGoldChangeFromSnapshots_ComputesInIDR(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	c := &Cache{db: db}

	mock.ExpectExec(`INSERT INTO price_snapshots`).WithArgs("ANTAM", 2392157.0, now).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`DELETE FROM price_snapshots`).WithArgs("ANTAM", now.Add(-snapshotRetention)).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectQuery(`SELECT price, observed_at FROM price_snapshots`).
		WithArgs("ANTAM", now.Add(-changeWindowMax), now.Add(-changeWindowMin)).
		WillReturnRows(sqlmock.NewRows([]string{"price", "observed_at"}).
			AddRow(2370000.0, now.Add(-24*time.Hour)).
			AddRow(2380000.0, now.Add(-21*time.Hour)))

	change, err := c.goldChangeFromSnapshots(context.Background(), "ANTAM", 2392157, now)
	require.NoError(t, err)
	require.NotNil(t, change)
	assert.InDelta(t, (2392157.0-2370000.0)/2370000.0*100, *change, 1e-9)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGoldChangeFromSnapshots_UnknownWithoutHistory(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	c := &Cache{db: db}

	mock.ExpectExec(`INSERT INTO price_snapshots`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`DELETE FROM price_snapshots`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT price, observed_at FROM price_snapshots`).
		WillReturnRows(sqlmock.NewRows([]string{"price", "observed_at"}))

	change, err := c.goldChangeFromSnapshots(context.Background(), "ANTAM", 2392157, now)
	require.NoError(t, err)
	assert.Nil(t, change)
}

func TestGetFromMySQL_NullChangeAndWeek52(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	c := &Cache{db: db, ttlHours: 4}

	mock.ExpectQuery(`FROM market_cache`).WithArgs("ANTAM").WillReturnRows(sqlmock.NewRows(
		[]string{"symbol", "price_usd", "price_idr", "change_pct_24h", "week52_high_usd", "week52_low_usd", "last_fetched", "source"}).
		AddRow("ANTAM", 146.0, 2392157.0, nil, nil, nil, time.Now(), "harga-emas.org"))

	d, fresh, err := c.getFromMySQL(context.Background(), "ANTAM")
	require.NoError(t, err)
	assert.True(t, fresh)
	assert.Nil(t, d.ChangePct24h)
	assert.Nil(t, d.Week52HighUSD)
}

func TestMarketData_DecodesLegacyRedisJSON(t *testing.T) {
	legacy := `{"symbol":"BTC","price_usd":81457,"price_idr":1300000000,"change_pct_24h":-2.29,"last_fetched":"2026-10-14T00:00:00Z","source":"coingecko"}`
	var d MarketData
	require.NoError(t, json.Unmarshal([]byte(legacy), &d))
	require.NotNil(t, d.ChangePct24h)
	assert.Equal(t, -2.29, *d.ChangePct24h)
	assert.Nil(t, d.Week52HighUSD)
}

func TestGetCryptoWeek52_CachedFor24Hours(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"prices":[[1,100],[2,50]]}`))
	}))
	defer srv.Close()
	old := asset.CoinGeckoBaseURL
	asset.CoinGeckoBaseURL = srv.URL
	t.Cleanup(func() { asset.CoinGeckoBaseURL = old })

	mr := miniredis.RunT(t)
	c := &Cache{redis: redis.NewClient(&redis.Options{Addr: mr.Addr()}), fetcher: asset.NewAssetFetcher("")}

	for i := 0; i < 2; i++ {
		high, low, err := c.GetCryptoWeek52(context.Background(), "BTC")
		require.NoError(t, err)
		assert.Equal(t, 100.0, high)
		assert.Equal(t, 50.0, low)
	}
	assert.Equal(t, 1, hits, "the second call must be served from Redis")
	assert.Equal(t, 24*time.Hour, mr.TTL(week52KeyPrefix+"BTC"))
}

// Redis entries written before migration 000013 carry a fake 0 change for
// gold. They must be ignored, so the fixed MySQL row (change NULL) is used.
func TestGetMarketData_IgnoresPreUpgradeRedisEntries(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mr := miniredis.RunT(t)
	c := &Cache{redis: redis.NewClient(&redis.Options{Addr: mr.Addr()}), db: db, ttlHours: 4}
	require.NoError(t, mr.Set("market:ANTAM",
		`{"symbol":"ANTAM","price_usd":146,"price_idr":2392157,"change_pct_24h":0,"last_fetched":"2026-10-15T00:00:00Z","source":"harga-emas.org"}`))

	mock.ExpectQuery(`FROM market_cache`).WithArgs("ANTAM").WillReturnRows(sqlmock.NewRows(
		[]string{"symbol", "price_usd", "price_idr", "change_pct_24h", "week52_high_usd", "week52_low_usd", "last_fetched", "source"}).
		AddRow("ANTAM", 146.0, 2392157.0, nil, nil, nil, time.Now(), "harga-emas.org"))

	d, err := c.GetMarketData(context.Background(), "ANTAM", "gold")
	require.NoError(t, err)
	assert.Nil(t, d.ChangePct24h, "the legacy Redis entry's fake 0% must not be served")
	assert.NoError(t, mock.ExpectationsWereMet())
}
