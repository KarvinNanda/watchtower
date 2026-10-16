package asset

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeTwelveDataQuote_ReadsFiftyTwoWeek(t *testing.T) {
	f, err := os.Open("testdata/twelvedata_quote_aapl.json")
	require.NoError(t, err)
	defer f.Close()

	res, err := decodeTwelveDataQuote(f, "AAPL")
	require.NoError(t, err)
	assert.InDelta(t, 342.95999, res.PriceUSD, 1e-9)
	require.NotNil(t, res.ChangePct24h)
	assert.InDelta(t, 0.4952098, *res.ChangePct24h, 1e-6)
	require.NotNil(t, res.Week52HighUSD)
	require.NotNil(t, res.Week52LowUSD)
	assert.InDelta(t, 348.14999, *res.Week52HighUSD, 1e-9)
	assert.InDelta(t, 243.42, *res.Week52LowUSD, 1e-9)
}

func TestDecodeTwelveDataQuote_MissingFieldsAreNil(t *testing.T) {
	res, err := decodeTwelveDataQuote(strings.NewReader(`{"close":"10.5","percent_change":""}`), "AAPL")
	require.NoError(t, err)
	assert.Nil(t, res.ChangePct24h, "an unparsable change must be unknown, not 0")
	assert.Nil(t, res.Week52HighUSD)
	assert.Nil(t, res.Week52LowUSD)
}

func TestDecodeTwelveDataQuote_RateLimit(t *testing.T) {
	_, err := decodeTwelveDataQuote(strings.NewReader(`{"status":"error","code":429,"message":"run out of API credits"}`), "AAPL")
	assert.True(t, errors.Is(err, errTwelveDataRateLimit))
}

func TestDecodeMarketChartRange(t *testing.T) {
	high, low, err := decodeMarketChartRange(strings.NewReader(`{"prices":[[1,100.5],[2,90],[3],[4,120.25]]}`))
	require.NoError(t, err)
	assert.Equal(t, 120.25, high)
	assert.Equal(t, 90.0, low)

	_, _, err = decodeMarketChartRange(strings.NewReader(`{"prices":[]}`))
	assert.Error(t, err)
}

func TestFetchCryptoWeek52_CallsMarketChart(t *testing.T) {
	var gotPath, gotDays string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotDays = r.URL.Path, r.URL.Query().Get("days")
		_, _ = w.Write([]byte(`{"prices":[[1,58566.09],[2,114419.22]]}`))
	}))
	defer srv.Close()
	old := CoinGeckoBaseURL
	CoinGeckoBaseURL = srv.URL
	t.Cleanup(func() { CoinGeckoBaseURL = old })

	high, low, err := NewAssetFetcher("").FetchCryptoWeek52("BTC")
	require.NoError(t, err)
	assert.Equal(t, "/coins/bitcoin/market_chart", gotPath)
	assert.Equal(t, "365", gotDays)
	assert.Equal(t, 114419.22, high)
	assert.Equal(t, 58566.09, low)
}

func TestFetchCryptoWeek52_RejectsUnknownSymbol(t *testing.T) {
	_, _, err := NewAssetFetcher("").FetchCryptoWeek52("../../etc")
	assert.Error(t, err)
}

func TestSymbolAssetType(t *testing.T) {
	for sym, want := range map[string]string{"BTC": "crypto", "aapl": "stock", "SPY": "stock", "ANTAM": "gold"} {
		got, ok := SymbolAssetType(sym)
		assert.True(t, ok, sym)
		assert.Equal(t, want, got, sym)
	}
	_, ok := SymbolAssetType("BRK-B")
	assert.False(t, ok)
	_, ok = SymbolAssetType("XAU")
	assert.False(t, ok, "XAU looked like international spot gold but returned the Antam price (#24)")
}

func TestFetchAsset_RejectsTypeMismatch(t *testing.T) {
	_, err := NewAssetFetcher("").FetchAsset("BTC", "stock")
	assert.Error(t, err)
}
