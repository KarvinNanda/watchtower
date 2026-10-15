package scheduler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/runs"
)

type fakeAssetRunner struct {
	gotUser uint64
	res     AssetRunResult
}

func (f *fakeAssetRunner) RunForUser(userID uint64) (AssetRunResult, error) {
	f.gotUser = userID
	return f.res, nil
}

type fakeSentinelRunner struct{ res SentinelRunResult }

func (f *fakeSentinelRunner) RunWithResult() (SentinelRunResult, error) { return f.res, nil }

type fakeRecorder struct{ calls int }

func (f *fakeRecorder) RunAndRecordWith(_ context.Context, fn func() error) error {
	f.calls++
	return fn()
}

func TestRunExecutor_Asset(t *testing.T) {
	a := &fakeAssetRunner{res: AssetRunResult{RulesChecked: 5, Triggered: 1, Sent: 1, Stale: 2}}
	rec := &fakeRecorder{}
	e := NewRunExecutor(a, &fakeSentinelRunner{}, rec)

	summary, err := e.Execute(context.Background(), &runs.Run{Type: runs.TypeAsset, UserID: 7})
	require.NoError(t, err)
	assert.Equal(t, uint64(7), a.gotUser)
	assert.Equal(t, map[string]int{"rules_checked": 5, "triggered": 1, "sent": 1, "errors": 0, "stale": 2}, summary)
	assert.Equal(t, 0, rec.calls, "a single-user asset run must not reset the global schedule")
}

func TestRunExecutor_SentinelRecordsScheduleAndReportsCallerOnly(t *testing.T) {
	sr := &fakeSentinelRunner{res: SentinelRunResult{Fetched: 40, New: 3, SentToUser: map[uint64]int{7: 2, 8: 1}}}
	rec := &fakeRecorder{}
	e := NewRunExecutor(&fakeAssetRunner{}, sr, rec)

	summary, err := e.Execute(context.Background(), &runs.Run{Type: runs.TypeSentinel, UserID: 7})
	require.NoError(t, err)
	assert.Equal(t, 1, rec.calls)
	assert.Equal(t, map[string]int{"items_fetched": 40, "new_items": 3, "sent_to_you": 2}, summary)
}

func TestRunExecutor_UnknownType(t *testing.T) {
	e := NewRunExecutor(&fakeAssetRunner{}, &fakeSentinelRunner{}, &fakeRecorder{})
	_, err := e.Execute(context.Background(), &runs.Run{Type: "portfolio"})
	assert.EqualError(t, err, `scheduler: unknown run type "portfolio"`)
}
