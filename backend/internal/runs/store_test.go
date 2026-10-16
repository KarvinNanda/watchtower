package runs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var storeNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func newTestStore(t *testing.T) (*Store, *miniredis.Miniredis, *time.Time) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	clock := storeNow
	s := NewStore(rdb)
	s.now = func() time.Time { return clock }
	return s, mr, &clock
}

func TestEnqueue_CreatesRunAndQueuesIt(t *testing.T) {
	s, mr, _ := newTestStore(t)
	run, err := s.Enqueue(context.Background(), 7, TypeAsset)
	require.NoError(t, err)

	assert.Regexp(t, `^[0-9a-f]{32}$`, run.ID)
	assert.Equal(t, StatusQueued, run.Status)
	assert.True(t, mr.Exists(runKeyPrefix+run.ID))
	assert.Equal(t, 24*time.Hour, mr.TTL(runKeyPrefix+run.ID))
	queued, err := mr.List(queueKey)
	require.NoError(t, err)
	assert.Equal(t, []string{run.ID}, queued)
}

func TestEnqueue_SecondCallIsRateLimited(t *testing.T) {
	s, mr, _ := newTestStore(t)
	_, err := s.Enqueue(context.Background(), 7, TypeAsset)
	require.NoError(t, err)

	_, err = s.Enqueue(context.Background(), 7, TypeAsset)
	var rl *RateLimitedError
	require.True(t, errors.As(err, &rl), "double-click must not queue a second job")
	assert.Equal(t, 5*time.Minute, rl.RetryAfter)
	queued, _ := mr.List(queueKey)
	assert.Len(t, queued, 1)
}

func TestEnqueue_TypesAndUsersAreIndependent(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	_, err := s.Enqueue(ctx, 7, TypeAsset)
	require.NoError(t, err)
	_, err = s.Enqueue(ctx, 7, TypeSentinel)
	assert.NoError(t, err)
	_, err = s.Enqueue(ctx, 8, TypeAsset)
	assert.NoError(t, err)
}

func TestEnqueue_LockExpires(t *testing.T) {
	s, mr, _ := newTestStore(t)
	ctx := context.Background()
	_, err := s.Enqueue(ctx, 7, TypeAsset)
	require.NoError(t, err)

	mr.FastForward(5*time.Minute + time.Second)

	_, err = s.Enqueue(ctx, 7, TypeAsset)
	assert.NoError(t, err)
}

func TestEnqueue_InvalidType(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, err := s.Enqueue(context.Background(), 7, "portfolio")
	assert.ErrorIs(t, err, ErrInvalidType)
}

func TestGet_OwnRun(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	created, err := s.Enqueue(ctx, 7, TypeAsset)
	require.NoError(t, err)

	got, err := s.Get(ctx, created.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, uint64(7), got.UserID)
	assert.Equal(t, TypeAsset, got.Type)
	assert.Equal(t, StatusQueued, got.Status)
	assert.True(t, got.CreatedAt.Equal(storeNow))
}

func TestGet_OtherUsersRunIsNotFound(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	created, err := s.Enqueue(ctx, 7, TypeAsset)
	require.NoError(t, err)

	_, err = s.Get(ctx, created.ID, 8)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGet_UnknownRunIsNotFound(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, err := s.Get(context.Background(), "0123456789abcdef0123456789abcdef", 7)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGet_StaleRunReportedAsTimedOut(t *testing.T) {
	s, _, clock := newTestStore(t)
	ctx := context.Background()
	created, err := s.Enqueue(ctx, 7, TypeAsset)
	require.NoError(t, err)

	*clock = storeNow.Add(16 * time.Minute)

	got, err := s.Get(ctx, created.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, got.Status)
	assert.Equal(t, "timed out", got.Error)
}

func TestGet_FinishedRunIsNeverStale(t *testing.T) {
	s, _, clock := newTestStore(t)
	ctx := context.Background()
	created, err := s.Enqueue(ctx, 7, TypeAsset)
	require.NoError(t, err)
	require.NoError(t, s.MarkRunning(ctx, created.ID))
	require.NoError(t, s.MarkDone(ctx, created.ID, map[string]int{"sent": 2}))

	*clock = storeNow.Add(16 * time.Minute)

	got, err := s.Get(ctx, created.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, StatusDone, got.Status)
	assert.Equal(t, map[string]int{"sent": 2}, got.Summary)
	require.NotNil(t, got.StartedAt)
	require.NotNil(t, got.FinishedAt)
}

func TestMarkFailed(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	created, err := s.Enqueue(ctx, 7, TypeSentinel)
	require.NoError(t, err)
	require.NoError(t, s.MarkFailed(ctx, created.ID, "run failed; check the scheduler logs"))

	got, err := s.Get(ctx, created.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, got.Status)
	assert.Equal(t, "run failed; check the scheduler logs", got.Error)
}

func TestPop_ReturnsQueuedRun(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	created, err := s.Enqueue(ctx, 7, TypeAsset)
	require.NoError(t, err)

	popped, err := s.Pop(ctx, time.Second)
	require.NoError(t, err)
	require.NotNil(t, popped)
	assert.Equal(t, created.ID, popped.ID)
	assert.Equal(t, uint64(7), popped.UserID)
}

func TestPop_EmptyQueueReturnsNil(t *testing.T) {
	s, _, _ := newTestStore(t)
	popped, err := s.Pop(context.Background(), time.Second)
	assert.NoError(t, err)
	assert.Nil(t, popped)
}

func TestHeartbeat(t *testing.T) {
	s, mr, _ := newTestStore(t)
	ctx := context.Background()

	alive, err := s.SchedulerAlive(ctx)
	require.NoError(t, err)
	assert.False(t, alive)

	require.NoError(t, s.Heartbeat(ctx))
	alive, _ = s.SchedulerAlive(ctx)
	assert.True(t, alive)

	mr.FastForward(61 * time.Second)
	alive, _ = s.SchedulerAlive(ctx)
	assert.False(t, alive, "heartbeat must expire when the scheduler stops beating")

	require.NoError(t, s.Heartbeat(ctx))
	require.NoError(t, s.ClearHeartbeat(ctx))
	alive, _ = s.SchedulerAlive(ctx)
	assert.False(t, alive)
}

func TestMark_ExpiredRunIsNotRecreated(t *testing.T) {
	s, mr, _ := newTestStore(t)
	ctx := context.Background()
	run, err := s.Enqueue(ctx, 7, TypeAsset)
	require.NoError(t, err)
	mr.FastForward(runTTL + time.Second)
	require.False(t, mr.Exists(runKeyPrefix+run.ID))

	assert.ErrorIs(t, s.MarkRunning(ctx, run.ID), ErrNotFound)
	assert.ErrorIs(t, s.MarkDone(ctx, run.ID, map[string]int{"sent": 1}), ErrNotFound)
	assert.ErrorIs(t, s.MarkFailed(ctx, run.ID, "boom"), ErrNotFound)
	assert.False(t, mr.Exists(runKeyPrefix+run.ID),
		"an HSET on an expired run would recreate the hash without TTL, user_id or created_at")
}

func TestMark_LiveRunKeepsItsTTL(t *testing.T) {
	s, mr, _ := newTestStore(t)
	ctx := context.Background()
	run, err := s.Enqueue(ctx, 7, TypeAsset)
	require.NoError(t, err)

	require.NoError(t, s.MarkRunning(ctx, run.ID))
	require.NoError(t, s.MarkDone(ctx, run.ID, map[string]int{"sent": 1}))

	got, err := s.Get(ctx, run.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, StatusDone, got.Status)
	assert.Equal(t, 24*time.Hour, mr.TTL(runKeyPrefix+run.ID))
}

// Review focus 4: Check now and Send report have separate locks.
func TestEnqueue_ReportHasItsOwnLock(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	_, err := s.Enqueue(ctx, 7, TypeAsset)
	require.NoError(t, err)
	_, err = s.Enqueue(ctx, 7, TypeAssetReport)
	require.NoError(t, err, "a report right after Check now is allowed")

	_, err = s.Enqueue(ctx, 7, TypeAssetReport)
	var rl *RateLimitedError
	assert.ErrorAs(t, err, &rl, "a second report within the lock window is refused")
}
