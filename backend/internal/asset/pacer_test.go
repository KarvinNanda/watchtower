package asset

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock advances only when the pacer sleeps.
type fakeClock struct {
	t     time.Time
	slept []time.Duration
}

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) sleep(d time.Duration)   { c.slept = append(c.slept, d); c.t = c.t.Add(d) }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestPacer_KeepsTheMinimumGap(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}
	p := newPacer(7500 * time.Millisecond)
	p.now, p.sleep = clk.now, clk.sleep

	p.wait() // first call: no wait
	clk.advance(2 * time.Second)
	p.wait() // 2 s after the first: waits the remaining 5.5 s
	clk.advance(10 * time.Second)
	p.wait() // 10 s after the second: no wait

	assert.Equal(t, []time.Duration{5500 * time.Millisecond}, clk.slept)
}

func TestNewAssetFetcher_PacesTwelveData(t *testing.T) {
	f := NewAssetFetcher("k")
	if assert.NotNil(t, f.stockPacer) {
		assert.Equal(t, twelveDataMinGap, f.stockPacer.gap)
	}
	// #48: 8 s, not 7.5 s, so network jitter cannot put 9 calls in Twelve
	// Data's 60 s window.
	assert.Equal(t, 8*time.Second, twelveDataMinGap)
}

// #48: CoinGecko calls share one pacer (24 per minute at most), so a Check
// now with many stale crypto symbols cannot push the next run into a 429.
func TestFetcher_PacesCoinGeckoAcrossCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "market_chart") {
			_, _ = w.Write([]byte(`{"prices":[[1,1],[2,2]]}`))
			return
		}
		_, _ = w.Write([]byte(`[{"current_price":1}]`))
	}))
	defer srv.Close()
	old := CoinGeckoBaseURL
	CoinGeckoBaseURL = srv.URL
	t.Cleanup(func() { CoinGeckoBaseURL = old })

	f := NewAssetFetcher("")
	require.NotNil(t, f.cryptoPacer)
	assert.Equal(t, 2500*time.Millisecond, f.cryptoPacer.gap)
	clk := &fakeClock{t: time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)}
	f.cryptoPacer.now, f.cryptoPacer.sleep = clk.now, clk.sleep

	_, err := f.FetchCrypto("BTC")
	require.NoError(t, err)
	_, _, err = f.FetchCryptoWeek52("BTC")
	require.NoError(t, err)
	_, err = f.FetchCrypto("ETH")
	require.NoError(t, err)

	assert.Equal(t, []time.Duration{2500 * time.Millisecond, 2500 * time.Millisecond}, clk.slept,
		"prices and 52-week ranges share the pacer")
}
