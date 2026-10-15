package quota

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var day = time.Date(2026, 10, 15, 23, 59, 0, 0, time.FixedZone("WIB", 7*3600))

func newTestBudget(t *testing.T, limit int) (*Budget, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	b := NewBudget(redis.NewClient(&redis.Options{Addr: mr.Addr()}), limit)
	b.now = func() time.Time { return day }
	return b, mr
}

func TestTryUse_UnderAndExactlyAtTheLimit(t *testing.T) {
	b, mr := newTestBudget(t, 2)
	for i := 0; i < 2; i++ {
		ok, err := b.TryUse(context.Background(), 1)
		require.NoError(t, err)
		assert.True(t, ok, "use %d", i+1)
	}
	// 23:59 WIB is 16:59 UTC on the same date; the key uses the UTC date.
	got, err := mr.Get(KeyPrefix + "2026-10-15")
	require.NoError(t, err)
	assert.Equal(t, "2", got)
	assert.Equal(t, 48*time.Hour, mr.TTL(KeyPrefix+"2026-10-15"))
}

func TestTryUse_OverLimitIsRefusedAndRestored(t *testing.T) {
	b, mr := newTestBudget(t, 1)
	ok, err := b.TryUse(context.Background(), 1)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = b.TryUse(context.Background(), 1)
	require.NoError(t, err)
	assert.False(t, ok)
	got, _ := mr.Get(KeyPrefix + "2026-10-15")
	assert.Equal(t, "1", got, "a refused reservation must not stay counted")
}

func TestTryUse_UsesTheUTCDate(t *testing.T) {
	b, mr := newTestBudget(t, 5)
	b.now = func() time.Time { return time.Date(2026, 10, 16, 6, 0, 0, 0, time.FixedZone("WIB", 7*3600)) } // 23:00 UTC on the 15th
	_, err := b.TryUse(context.Background(), 1)
	require.NoError(t, err)
	assert.True(t, mr.Exists(KeyPrefix+"2026-10-15"))
}

func TestTryUse_ZeroLimitAlwaysRefuses(t *testing.T) {
	b, mr := newTestBudget(t, 0)
	ok, err := b.TryUse(context.Background(), 1)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, mr.Keys())
}

func TestTryUse_RedisErrorFailsClosed(t *testing.T) {
	b, mr := newTestBudget(t, 10)
	mr.Close()
	ok, err := b.TryUse(context.Background(), 1)
	assert.Error(t, err)
	assert.False(t, ok)
}

func TestTryUse_NilBudgetRefuses(t *testing.T) {
	var b *Budget
	ok, err := b.TryUse(context.Background(), 1)
	require.NoError(t, err)
	assert.False(t, ok)
}
