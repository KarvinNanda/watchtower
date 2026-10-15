package asset

import (
	"sync"
	"time"
)

// twelveDataMinGap spaces Twelve Data calls to stay under the free tier's
// 8 credits per minute, so a run never hits a predictable 429 (spec Q4).
const twelveDataMinGap = 7500 * time.Millisecond

// pacer makes consecutive calls wait until at least gap has passed since
// the previous one. It is per process; only the scheduler calls Twelve Data.
type pacer struct {
	mu    sync.Mutex
	gap   time.Duration
	last  time.Time
	now   func() time.Time
	sleep func(time.Duration)
}

func newPacer(gap time.Duration) *pacer {
	return &pacer{gap: gap, now: time.Now, sleep: time.Sleep}
}

// wait blocks until the next call may start, then records it.
func (p *pacer) wait() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.last.IsZero() {
		if d := p.gap - p.now().Sub(p.last); d > 0 {
			p.sleep(d)
		}
	}
	p.last = p.now()
}
