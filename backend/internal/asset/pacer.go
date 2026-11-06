package asset

import (
	"context"
	"sync"
	"time"
)

// twelveDataMinGap spaces Twelve Data calls to stay under the free tier's
// 8 credits per minute, so a run never hits a predictable 429 (spec Q4).
// 8 s rather than 7.5 s: network jitter could otherwise put 9 calls in
// Twelve Data's 60 s window (#48).
const twelveDataMinGap = 8 * time.Second

// coinGeckoMinGap spaces every CoinGecko call (prices and 52-week ranges)
// to at most 24 per minute on the public API, which has no key (#48).
const coinGeckoMinGap = 2500 * time.Millisecond

// coinGeckoKeyedMinGap applies with a Demo API key (#65): at most 50 calls
// per minute, half the Demo plan's 100, leaving room for jitter.
const coinGeckoKeyedMinGap = 1200 * time.Millisecond

// pacer makes consecutive calls wait until at least gap has passed since
// the previous one. It is per process; only the scheduler calls the paced
// providers.
type pacer struct {
	mu   sync.Mutex
	gap  time.Duration
	last time.Time
	now  func() time.Time
	// after is time.After; tests replace it with a fake clock.
	after func(time.Duration) <-chan time.Time
}

func newPacer(gap time.Duration) *pacer {
	return &pacer{gap: gap, now: time.Now, after: time.After}
}

// wait blocks until the next call may start, then records it. When ctx
// ends first it returns ctx.Err() and records nothing, since no call is
// made (#86).
func (p *pacer) wait(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	// The caller may have given up while it queued for the lock.
	if err := ctx.Err(); err != nil {
		return err
	}
	if !p.last.IsZero() {
		if d := p.gap - p.now().Sub(p.last); d > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-p.after(d):
			}
		}
	}
	p.last = p.now()
	return nil
}
