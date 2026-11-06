package asset

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubTwelveDataQuote points the /quote URL at handler for one test.
func stubTwelveDataQuote(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	old := twelveDataQuoteURL
	twelveDataQuoteURL = srv.URL
	t.Cleanup(func() { twelveDataQuoteURL = old })
}

// #86: a slow /quote must not outlive the caller's deadline, and the error
// must still not carry the key.
func TestFetchStock_HonoursContextDeadline(t *testing.T) {
	stubTwelveDataQuote(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
			_, _ = w.Write([]byte(`{"close":"10.5"}`))
		}
	})
	f := NewAssetFetcher("secret-key-86")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := f.FetchStock(ctx, "AAPL")

	require.Error(t, err)
	assert.Less(t, time.Since(start), time.Second, "the fetch waited for the slow server")
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)
	assert.NotContains(t, err.Error(), "secret-key-86")
}

// #86: the 60 s rate-limit retry stops as soon as the caller gives up.
func TestFetchStock_RateLimitRetryStopsOnCancel(t *testing.T) {
	var requests atomic.Int32
	stubTwelveDataQuote(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"code":429,"message":"You have run out of API credits for the current minute. 9 API credits were used, with the current limit being 8.","status":"error"}`))
	})
	f := NewAssetFetcher("k")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { _, err := f.FetchStock(ctx, "AAPL"); done <- err }()
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), requests.Load(), "no retry after the cancel")
	case <-time.After(5 * time.Second):
		t.Fatal("FetchStock kept waiting for the rate-limit retry after the cancel")
	}
}

// #86: a cancelled caller makes no /time_series call at all.
func TestFetchStockCloses_UsesContext(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"status":"ok","values":[]}`))
	}))
	t.Cleanup(srv.Close)
	old := twelveDataTimeSeriesURL
	twelveDataTimeSeriesURL = srv.URL
	t.Cleanup(func() { twelveDataTimeSeriesURL = old })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewAssetFetcher("secret-key-86").FetchStockCloses(ctx, "AAPL")

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotContains(t, err.Error(), "secret-key-86")
	assert.Zero(t, requests.Load(), "no request may reach the server")
}

// #86: the CoinGecko calls take the same context.
func TestFetchCrypto_UsesContext(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`[{"current_price":1}]`))
	}))
	t.Cleanup(srv.Close)
	old := CoinGeckoBaseURL
	CoinGeckoBaseURL = srv.URL
	t.Cleanup(func() { CoinGeckoBaseURL = old })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := NewAssetFetcher("")

	_, err := f.FetchCrypto(ctx, "BTC")
	assert.ErrorIs(t, err, context.Canceled)
	_, _, err = f.FetchCryptoWeek52(ctx, "BTC")
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, requests.Load(), "no request may reach the server")
}
