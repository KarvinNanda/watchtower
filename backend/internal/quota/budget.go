// Package quota guards shared external API quotas. Budget caps the Twelve
// Data credits that manual "Check now" runs may spend per UTC day, so
// clicking cannot exhaust the free tier for everyone. Periodic runs spend
// stock symbols x runs per day on top of it; keeping the sum under the
// provider's daily limit is the operator's job (see README).
package quota

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// KeyPrefix + the UTC date is the Redis counter for one day. Twelve Data
// resets its daily quota at 00:00 UTC.
const KeyPrefix = "quota:twelvedata:manual:"

const keyTTL = 48 * time.Hour

// Budget is a per-UTC-day credit allowance stored in Redis.
type Budget struct {
	rdb   *redis.Client
	limit int
	now   func() time.Time
}

// NewBudget allows dailyLimit credits per UTC day; 0 allows none.
func NewBudget(rdb *redis.Client, dailyLimit int) *Budget {
	return &Budget{rdb: rdb, limit: dailyLimit, now: time.Now}
}

// TryUse reserves credits from today's budget. It returns false when the
// reservation would exceed the limit (the reservation is undone), when the
// limit is 0, or when Redis fails: a quota we cannot count is a quota we
// do not spend (fail closed). The reservation is not refunded if the call
// it pays for fails, since the provider may have counted it.
func (b *Budget) TryUse(ctx context.Context, credits int) (bool, error) {
	if b == nil || b.limit <= 0 || credits <= 0 {
		return false, nil
	}
	key := KeyPrefix + b.now().UTC().Format("2006-01-02")

	var incr *redis.IntCmd
	if _, err := b.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		incr = p.IncrBy(ctx, key, int64(credits))
		p.Expire(ctx, key, keyTTL)
		return nil
	}); err != nil {
		return false, fmt.Errorf("quota: reserve %d credit(s): %w", credits, err)
	}

	if incr.Val() > int64(b.limit) {
		if err := b.rdb.DecrBy(ctx, key, int64(credits)).Err(); err != nil {
			return false, fmt.Errorf("quota: release %d credit(s): %w", credits, err)
		}
		return false, nil
	}
	return true, nil
}
