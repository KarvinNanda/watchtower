package runs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeExecutor struct {
	summary map[string]int
	err     error
	panics  bool
}

func (f fakeExecutor) Execute(context.Context, *Run) (map[string]int, error) {
	if f.panics {
		panic("boom")
	}
	return f.summary, f.err
}

func runConsumer(t *testing.T, s *Store, exec Executor) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := NewConsumer(s, exec)
	c.popTimeout = time.Second
	stopped := make(chan struct{})
	go func() { c.Start(ctx); close(stopped) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(3 * time.Second):
			t.Error("consumer did not stop after cancel")
		}
	})
	return cancel
}

func waitStatus(t *testing.T, s *Store, id string, want string) *Run {
	t.Helper()
	var got *Run
	require.Eventually(t, func() bool {
		r, err := s.Get(context.Background(), id, 7)
		if err != nil {
			return false
		}
		got = r
		return r.Status == want
	}, 3*time.Second, 20*time.Millisecond)
	return got
}

func TestConsumer_MarksRunDoneWithSummary(t *testing.T) {
	s, _, _ := newTestStore(t)
	s.now = time.Now
	run, err := s.Enqueue(context.Background(), 7, TypeAsset)
	require.NoError(t, err)

	runConsumer(t, s, fakeExecutor{summary: map[string]int{"sent": 1}})

	got := waitStatus(t, s, run.ID, StatusDone)
	assert.Equal(t, map[string]int{"sent": 1}, got.Summary)
	assert.NotNil(t, got.StartedAt)
}

func TestConsumer_ExecutorErrorMarksFailedWithGenericMessage(t *testing.T) {
	s, _, _ := newTestStore(t)
	s.now = time.Now
	run, err := s.Enqueue(context.Background(), 7, TypeAsset)
	require.NoError(t, err)

	runConsumer(t, s, fakeExecutor{err: errors.New("dial tcp 10.0.0.5:3306: secret detail")})

	got := waitStatus(t, s, run.ID, StatusFailed)
	assert.Equal(t, "run failed; check the scheduler logs", got.Error, "internal errors must not reach the browser")
}

func TestConsumer_PanicMarksFailed(t *testing.T) {
	s, _, _ := newTestStore(t)
	s.now = time.Now
	run, err := s.Enqueue(context.Background(), 7, TypeSentinel)
	require.NoError(t, err)

	runConsumer(t, s, fakeExecutor{panics: true})

	waitStatus(t, s, run.ID, StatusFailed)
}

func TestStartHeartbeat_BeatsAndClearsOnShutdown(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { StartHeartbeat(ctx, s, 10*time.Millisecond); close(done) }()

	require.Eventually(t, func() bool {
		alive, _ := s.SchedulerAlive(context.Background())
		return alive
	}, time.Second, 10*time.Millisecond)

	cancel()
	<-done
	alive, err := s.SchedulerAlive(context.Background())
	require.NoError(t, err)
	assert.False(t, alive, "a clean shutdown must report the scheduler offline immediately")
}
