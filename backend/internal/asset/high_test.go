package asset

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeCoinGeckoMarkets_HighCoversTheLast24Hours(t *testing.T) {
	f, err := os.Open("testdata/coingecko_markets_btc.json")
	require.NoError(t, err)
	defer f.Close()
	now := time.Date(2026, 10, 15, 7, 42, 0, 0, time.UTC)

	res, err := decodeCoinGeckoMarkets(f, "BTC", now)

	require.NoError(t, err)
	assert.Equal(t, 81211.0, res.PriceUSD)
	require.NotNil(t, res.HighUSD)
	assert.Equal(t, 84025.0, *res.HighUSD)
	require.NotNil(t, res.HighSince)
	// 25 h, not 24 h: CoinGecko's window is relative to its own (possibly
	// lagging) last_updated, so start a margin earlier, on the safe side.
	assert.Equal(t, now.Add(-25*time.Hour), *res.HighSince)
	assert.Equal(t, now, res.FetchedAt)
}

func TestDecodeCoinGeckoMarkets_NullHighIsUnknown(t *testing.T) {
	res, err := decodeCoinGeckoMarkets(strings.NewReader(`[{"current_price":1,"high_24h":null}]`), "BTC", time.Now())
	require.NoError(t, err)
	assert.Nil(t, res.HighUSD)
	assert.Nil(t, res.HighSince)
}

func TestDecodeTwelveDataQuote_HighStartsAtTheSession(t *testing.T) {
	f, err := os.Open("testdata/twelvedata_quote_aapl.json")
	require.NoError(t, err)
	defer f.Close()

	res, err := decodeTwelveDataQuote(f, "AAPL")

	require.NoError(t, err)
	require.NotNil(t, res.HighUSD)
	assert.Equal(t, 347.089996, *res.HighUSD)
	require.NotNil(t, res.HighSince)
	// timestamp 1791984600 = 2026-10-14 13:30 UTC = 09:30 New York (session open).
	assert.Equal(t, time.Date(2026, 10, 14, 13, 30, 0, 0, time.UTC), *res.HighSince)
}

func TestDecodeTwelveDataQuote_MissingHighOrTimestampIsUnknown(t *testing.T) {
	res, err := decodeTwelveDataQuote(strings.NewReader(`{"close":"1","high":"","timestamp":1791984600}`), "X")
	require.NoError(t, err)
	assert.Nil(t, res.HighUSD)
	assert.Nil(t, res.HighSince)

	res, err = decodeTwelveDataQuote(strings.NewReader(`{"close":"1","high":"2"}`), "X")
	require.NoError(t, err)
	assert.Nil(t, res.HighUSD, "a high without its window start cannot be used safely")
	assert.Nil(t, res.HighSince)
}

// #48: a timestamp that is not a JSON number must not lose the price.
func TestDecodeTwelveDataQuote_LenientTimestamp(t *testing.T) {
	res, err := decodeTwelveDataQuote(strings.NewReader(`{"close":"5","high":"6","timestamp":"1791984600"}`), "X")
	require.NoError(t, err)
	assert.Equal(t, 5.0, res.PriceUSD)
	require.NotNil(t, res.HighSince, "a numeric string timestamp is read")
	assert.Equal(t, time.Date(2026, 10, 14, 13, 30, 0, 0, time.UTC), *res.HighSince)

	for _, raw := range []string{`""`, `"soon"`, `null`, `true`} {
		res, err := decodeTwelveDataQuote(strings.NewReader(`{"close":"5","high":"6","timestamp":`+raw+`}`), "X")
		require.NoError(t, err, raw)
		assert.Equal(t, 5.0, res.PriceUSD, raw)
		assert.Nil(t, res.HighUSD, "%s: an unknown window start makes the high unknown", raw)
	}
}

// #48: a high window that starts after the fetch is impossible; treat it as
// unknown instead of letting it raise a trailing stop's peak.
func TestDecodeTwelveDataQuote_FutureWindowIsUnknown(t *testing.T) {
	res, err := decodeTwelveDataQuote(strings.NewReader(`{"close":"5","high":"6","timestamp":4102444800}`), "X")
	require.NoError(t, err)
	assert.Equal(t, 5.0, res.PriceUSD)
	assert.Nil(t, res.HighUSD)
	assert.Nil(t, res.HighSince)
}

// A day's quota cannot come back in 60 s: retrying only holds the run
// (and its lock) for a minute per symbol (final review, Important 1).
func TestDecodeTwelveDataQuote_DailyQuotaIsNotTheMinuteLimit(t *testing.T) {
	_, err := decodeTwelveDataQuote(strings.NewReader(`{"code":429,"message":"You have run out of API credits for the day. 801 API credits were used, with the current limit being 800.","status":"error"}`), "AAPL")
	require.Error(t, err)
	assert.ErrorIs(t, err, errTwelveDataDailyLimit)
	assert.NotErrorIs(t, err, errTwelveDataRateLimit)

	_, err = decodeTwelveDataQuote(strings.NewReader(`{"code":429,"message":"You have run out of API credits for the current minute. 9 API credits were used, with the current limit being 8.","status":"error"}`), "AAPL")
	assert.ErrorIs(t, err, errTwelveDataRateLimit, "the per-minute limit is still retried")
}

func TestFetchStock_DoesNotRetryTheDailyQuota(t *testing.T) {
	rt := &recordingTransport{body: []byte(`{"code":429,"message":"You have run out of API credits for the day.","status":"error"}`)}
	f := NewAssetFetcher("k")
	f.SetHTTPClient(&http.Client{Transport: rt})
	done := make(chan error, 1)
	go func() { _, err := f.FetchStock("AAPL"); done <- err }()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, errTwelveDataDailyLimit)
		assert.Len(t, rt.urls, 1)
	case <-time.After(5 * time.Second):
		t.Fatal("FetchStock retried (slept) on the daily quota")
	}
}
