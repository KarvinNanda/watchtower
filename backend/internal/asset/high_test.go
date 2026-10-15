package asset

import (
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
	assert.Equal(t, now.Add(-24*time.Hour), *res.HighSince)
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
