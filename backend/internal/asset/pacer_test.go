package asset

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock advances only when the pacer waits.
type fakeClock struct {
	t     time.Time
	slept []time.Duration
}

func (c *fakeClock) now() time.Time { return c.t }

// after records the wait, moves the clock and returns a channel that is
// already ready, so the pacer never really blocks.
func (c *fakeClock) after(d time.Duration) <-chan time.Time {
	c.slept = append(c.slept, d)
	c.t = c.t.Add(d)
	return readyAt(c.t)
}

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// readyAt returns a channel that already holds t.
func readyAt(t time.Time) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- t
	return ch
}

// noWait is an after seam that never blocks.
func noWait(time.Duration) <-chan time.Time { return readyAt(time.Time{}) }

func TestPacerWait_NoContextChangeForNormalWait(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}
	p := newPacer(7500 * time.Millisecond)
	p.now, p.after = clk.now, clk.after
	ctx := context.Background()

	require.NoError(t, p.wait(ctx)) // first call: no wait
	clk.advance(2 * time.Second)
	require.NoError(t, p.wait(ctx)) // 2 s after the first: waits the remaining 5.5 s
	clk.advance(10 * time.Second)
	require.NoError(t, p.wait(ctx)) // 10 s after the second: no wait

	assert.Equal(t, []time.Duration{5500 * time.Millisecond}, clk.slept)
	assert.Equal(t, clk.t, p.last, "a normal wait records the call")
}

// #86: a cancelled caller stops waiting for the gap, and since it makes no
// call, the next caller's gap is still measured from the previous call.
// The after seam signals when the wait starts and never fires, so the
// cancel always ends the select.
func TestPacerWait_ReturnsWhenContextCancelled(t *testing.T) {
	p := newPacer(time.Hour)
	require.NoError(t, p.wait(context.Background())) // a prior call
	prior := p.last
	waiting := make(chan time.Duration, 1)
	p.after = func(d time.Duration) <-chan time.Time {
		waiting <- d
		return make(chan time.Time)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.wait(ctx) }()
	select {
	case d := <-waiting:
		assert.Greater(t, d, time.Duration(0))
	case <-time.After(2 * time.Second):
		t.Fatal("wait never started waiting for the gap")
	}
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("wait kept blocking after the context was cancelled")
	}
	assert.Equal(t, prior, p.last, "a cancelled wait must not record a call")
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
	f.cryptoPacer.now, f.cryptoPacer.after = clk.now, clk.after

	_, err := f.FetchCrypto(context.Background(), "BTC")
	require.NoError(t, err)
	_, _, err = f.FetchCryptoWeek52(context.Background(), "BTC")
	require.NoError(t, err)
	_, err = f.FetchCrypto(context.Background(), "ETH")
	require.NoError(t, err)

	assert.Equal(t, []time.Duration{2500 * time.Millisecond, 2500 * time.Millisecond}, clk.slept,
		"prices and 52-week ranges share the pacer")
}
