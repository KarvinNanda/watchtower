package asset

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func utcDay(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestDecodeCoinGeckoMarkets_ChangePct7d(t *testing.T) {
	fixture, err := os.ReadFile("testdata/coingecko_markets_btc.json")
	require.NoError(t, err)

	res, err := decodeCoinGeckoMarkets(strings.NewReader(string(fixture)), "BTC", time.Now())
	require.NoError(t, err)
	require.NotNil(t, res.ChangePct7d)
	assert.Equal(t, -3.0276, *res.ChangePct7d)

	res, err = decodeCoinGeckoMarkets(strings.NewReader(`[{"current_price":1}]`), "BTC", time.Now())
	require.NoError(t, err)
	assert.Nil(t, res.ChangePct7d)
}

func TestFetchCrypto_Requests7d(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(`[{"current_price":1}]`))
	}))
	t.Cleanup(srv.Close)
	old := CoinGeckoBaseURL
	CoinGeckoBaseURL = srv.URL
	t.Cleanup(func() { CoinGeckoBaseURL = old })

	_, err := NewAssetFetcher("k").FetchCrypto("BTC")

	require.NoError(t, err)
	vals, err := parseQuery(query)
	require.NoError(t, err)
	assert.Equal(t, "7d", vals.Get("price_change_percentage"))
	assert.Equal(t, "usd", vals.Get("vs_currency"))
	assert.Equal(t, "bitcoin", vals.Get("ids"))
}

func TestDecodeTwelveDataTimeSeries(t *testing.T) {
	fixture, err := os.ReadFile("testdata/twelvedata_timeseries_aapl.json")
	require.NoError(t, err)

	closes, err := decodeTwelveDataTimeSeries(strings.NewReader(string(fixture)), "AAPL")

	require.NoError(t, err)
	require.Len(t, closes, 10)
	assert.Equal(t, DailyClose{Day: utcDay(2026, 11, 5), Close: 344.17001}, closes[0])
	assert.Equal(t, utcDay(2026, 10, 23), closes[9].Day)
	assert.Equal(t, time.UTC, closes[0].Day.Location())
}

func TestDecodeTwelveDataTimeSeries_SkipsBadCloses(t *testing.T) {
	body := `{"status":"ok","values":[
		{"datetime":"2026-11-05","close":"10.5"},
		{"datetime":"2026-11-04","close":"abc"},
		{"datetime":"2026-11-03","close":"0"},
		{"datetime":"2026-11-02","close":"-4"},
		{"datetime":"bad","close":"9"}]}`

	closes, err := decodeTwelveDataTimeSeries(strings.NewReader(body), "AAPL")

	require.NoError(t, err)
	assert.Equal(t, []DailyClose{{Day: utcDay(2026, 11, 5), Close: 10.5}}, closes)
}

func TestDecodeTwelveDataTimeSeries_ErrorBody(t *testing.T) {
	body := `{"code":429,"status":"error","message":"limit"}`

	_, err := decodeTwelveDataTimeSeries(strings.NewReader(body), "AAPL")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "429")
	assert.Contains(t, err.Error(), "limit")
}

func TestPickRef7d(t *testing.T) {
	// Weekdays Mon 2026-10-26 .. Fri 2026-11-06, newest first.
	var closes []DailyClose
	for d := utcDay(2026, 11, 6); !d.Before(utcDay(2026, 10, 26)); d = d.AddDate(0, 0, -1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		closes = append(closes, DailyClose{Day: d, Close: float64(d.Day())})
	}

	tests := []struct {
		name string
		now  time.Time
		want time.Time
		ok   bool
	}{
		{"monday", time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC), utcDay(2026, 11, 2), true},
		{"sunday falls back past the weekend", time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC), utcDay(2026, 10, 30), true},
		{"friday", time.Date(2026, 11, 6, 20, 0, 0, 0, time.UTC), utcDay(2026, 10, 30), true},
		{"nothing old enough", time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC), time.Time{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickRef7d(closes, tc.now)
			assert.Equal(t, tc.ok, ok)
			if tc.ok {
				assert.Equal(t, tc.want, got.Day)
			}
		})
	}
	_, ok := pickRef7d(nil, time.Now())
	assert.False(t, ok)
}

func TestFetchStockRef7d_KeyInHeaderOnly(t *testing.T) {
	fixture, err := os.ReadFile("testdata/twelvedata_timeseries_aapl.json")
	require.NoError(t, err)
	var auth, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, query = r.Header.Get("Authorization"), r.URL.RawQuery
		_, _ = w.Write(fixture)
	}))
	t.Cleanup(srv.Close)
	old := twelveDataTimeSeriesURL
	twelveDataTimeSeriesURL = srv.URL
	t.Cleanup(func() { twelveDataTimeSeriesURL = old })

	f := NewAssetFetcher("k")
	ref, err := f.FetchStockRef7d("AAPL")

	require.NoError(t, err)
	assert.Equal(t, "apikey k", auth)
	vals, err := parseQuery(query)
	require.NoError(t, err)
	assert.Empty(t, vals.Get("apikey"))
	assert.Equal(t, "AAPL", vals.Get("symbol"))
	assert.Equal(t, "1day", vals.Get("interval"))
	assert.Equal(t, "10", vals.Get("outputsize"))
	assert.False(t, ref.Day.IsZero())

	// Server down: the error must not carry the key.
	srv.Close()
	f2 := NewAssetFetcher("k-secret")
	_, err = f2.FetchStockRef7d("AAPL")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "k-secret")
}

func parseQuery(raw string) (url.Values, error) { return url.ParseQuery(raw) }
