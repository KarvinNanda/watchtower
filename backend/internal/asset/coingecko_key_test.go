package asset

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coinGeckoStub records the Demo key header and raw query of every call.
func coinGeckoStub(t *testing.T, status int) (headers, queries *[]string) {
	t.Helper()
	var mu sync.Mutex
	h, q := []string{}, []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		h = append(h, r.Header.Get("x-cg-demo-api-key"))
		q = append(q, r.URL.RawQuery)
		mu.Unlock()
		w.WriteHeader(status)
		if strings.Contains(r.URL.Path, "market_chart") {
			_, _ = w.Write([]byte(`{"prices":[[1,1],[2,2]]}`))
			return
		}
		_, _ = w.Write([]byte(`[{"current_price":1}]`))
	}))
	t.Cleanup(srv.Close)
	old := CoinGeckoBaseURL
	CoinGeckoBaseURL = srv.URL
	t.Cleanup(func() { CoinGeckoBaseURL = old })
	return &h, &q
}

// #65: an optional Demo key goes in the x-cg-demo-api-key header on every
// CoinGecko call, never in the URL.
func TestCoinGeckoKey_SentAsHeaderOnEveryCall(t *testing.T) {
	headers, queries := coinGeckoStub(t, http.StatusOK)
	f := NewAssetFetcher("")
	f.SetCoinGeckoAPIKey("demo-key-123")
	f.cryptoPacer.sleep = func(time.Duration) {}

	_, err := f.FetchCrypto("BTC")
	require.NoError(t, err)
	_, _, err = f.FetchCryptoWeek52("BTC")
	require.NoError(t, err)

	assert.Equal(t, []string{"demo-key-123", "demo-key-123"}, *headers)
	for _, q := range *queries {
		assert.NotContains(t, q, "demo-key-123")
	}
}

func TestCoinGeckoKey_NoKeyKeepsTheKeylessRequest(t *testing.T) {
	headers, _ := coinGeckoStub(t, http.StatusOK)
	f := NewAssetFetcher("")
	f.SetCoinGeckoAPIKey("")

	_, err := f.FetchCrypto("BTC")
	require.NoError(t, err)

	assert.Equal(t, []string{""}, *headers)
	assert.Equal(t, coinGeckoMinGap, f.cryptoPacer.gap)
}

// With a key, CoinGecko calls are 1.2 s apart: at most 50 per minute, half
// the Demo plan's 100 per minute.
func TestCoinGeckoKey_ShortensTheGap(t *testing.T) {
	f := NewAssetFetcher("")
	f.SetCoinGeckoAPIKey("demo-key-123")
	assert.Equal(t, 1200*time.Millisecond, f.cryptoPacer.gap)
	assert.Equal(t, 1200*time.Millisecond, coinGeckoKeyedMinGap)
}

func TestCoinGeckoKey_NeverInErrors(t *testing.T) {
	coinGeckoStub(t, http.StatusTooManyRequests)
	f := NewAssetFetcher("")
	f.SetCoinGeckoAPIKey("demo-key-123")
	f.cryptoPacer.sleep = func(time.Duration) {}

	_, err := f.FetchCrypto("BTC")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "demo-key-123")
	_, _, err = f.FetchCryptoWeek52("BTC")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "demo-key-123")
}
