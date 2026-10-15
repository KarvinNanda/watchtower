package asset

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	assert.Equal(t, 7500*time.Millisecond, twelveDataMinGap, "8 calls per minute on the free tier")
}
