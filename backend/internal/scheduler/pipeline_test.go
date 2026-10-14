package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const lastRunQuery = `SELECT last_started_at, last_status FROM scheduler_state WHERE pipeline = \?`

var pipelineNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func newTestPipeline(t *testing.T, runs *int) (*Pipeline, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &Pipeline{
		Name:     PipelineAsset,
		Interval: 4 * time.Hour,
		RunFn:    func() error { *runs++; return nil },
		State:    NewStateStore(db),
		Now:      func() time.Time { return pipelineNow },

		RetryAfterFailure: 10 * time.Minute,
	}, mock
}

func expectLastStarted(mock sqlmock.Sqlmock, at *time.Time) {
	expectLastRun(mock, at, "ok")
}

func expectLastRun(mock sqlmock.Sqlmock, at *time.Time, status string) {
	rows := sqlmock.NewRows([]string{"last_started_at", "last_status"})
	if at != nil {
		rows.AddRow(*at, status)
	}
	mock.ExpectQuery(lastRunQuery).WithArgs(PipelineAsset).WillReturnRows(rows)
}

func expectRecordedRun(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`INSERT INTO scheduler_state`).WithArgs(PipelineAsset, pipelineNow).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`UPDATE scheduler_state`).WithArgs(pipelineNow, "ok", PipelineAsset).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestPipelineTick_EmptyTableRunsImmediately(t *testing.T) {
	runs := 0
	p, mock := newTestPipeline(t, &runs)
	expectLastStarted(mock, nil)
	expectRecordedRun(mock)

	p.tick(context.Background())

	assert.Equal(t, 1, runs)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPipelineTick_NotDueDoesNotRun(t *testing.T) {
	runs := 0
	p, mock := newTestPipeline(t, &runs)
	recent := pipelineNow.Add(-1 * time.Hour)
	expectLastStarted(mock, &recent)

	p.tick(context.Background())

	assert.Equal(t, 0, runs, "a restart shortly after a run must not re-run")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPipelineTick_ExactlyDueRuns(t *testing.T) {
	runs := 0
	p, mock := newTestPipeline(t, &runs)
	due := pipelineNow.Add(-4 * time.Hour)
	expectLastStarted(mock, &due)
	expectRecordedRun(mock)

	p.tick(context.Background())

	assert.Equal(t, 1, runs)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPipelineTick_OverdueRunsOnceThenWaits(t *testing.T) {
	runs := 0
	p, mock := newTestPipeline(t, &runs)
	asleep := pipelineNow.Add(-12 * time.Hour) // three intervals missed
	expectLastStarted(mock, &asleep)
	expectRecordedRun(mock)
	justRan := pipelineNow
	expectLastStarted(mock, &justRan)

	p.tick(context.Background()) // first check after wake
	p.tick(context.Background()) // one minute later

	assert.Equal(t, 1, runs, "missed intervals collapse into a single catch-up run")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPipelineTick_DoesNotRunWhenStartCannotBeRecorded(t *testing.T) {
	runs := 0
	p, mock := newTestPipeline(t, &runs)
	expectLastStarted(mock, nil)
	mock.ExpectExec(`INSERT INTO scheduler_state`).WillReturnError(errors.New("db down"))

	p.tick(context.Background())

	assert.Equal(t, 0, runs, "without a recorded start the loop would re-run every minute")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPipelineRunAndRecordWith_RecordsFailure(t *testing.T) {
	runs := 0
	p, mock := newTestPipeline(t, &runs)
	mock.ExpectExec(`INSERT INTO scheduler_state`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`UPDATE scheduler_state`).WithArgs(pipelineNow, "failed", PipelineAsset).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := p.RunAndRecordWith(context.Background(), func() error { return errors.New("boom") })

	assert.EqualError(t, err, "boom")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPipelineTick_FailedRunRetriesAfterBackoff(t *testing.T) {
	runs := 0
	p, mock := newTestPipeline(t, &runs)
	failedAt := pipelineNow.Add(-11 * time.Minute) // e.g. woke up before Wi-Fi reconnected
	expectLastRun(mock, &failedAt, "failed")
	expectRecordedRun(mock)

	p.tick(context.Background())

	assert.Equal(t, 1, runs, "a failed catch-up run is retried after the backoff, not after a full interval")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPipelineTick_FailedRunWaitsForBackoff(t *testing.T) {
	runs := 0
	p, mock := newTestPipeline(t, &runs)
	failedAt := pipelineNow.Add(-5 * time.Minute)
	expectLastRun(mock, &failedAt, "failed")

	p.tick(context.Background())

	assert.Equal(t, 0, runs)
	assert.NoError(t, mock.ExpectationsWereMet())
}
