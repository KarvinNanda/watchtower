package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
)

const (
	PipelineAsset    = "asset"
	PipelineSentinel = "sentinel"

	// DueCheckInterval is how often a Pipeline asks "am I due?". It bounds
	// how late a catch-up run can start after the machine wakes up.
	DueCheckInterval = time.Minute

	// RetryAfterFailure is how soon a run that failed outright (for example
	// a catch-up run right after wake, before Wi-Fi reconnects) is retried,
	// instead of waiting a full interval.
	RetryAfterFailure = 10 * time.Minute
)

// StateStore persists when each pipeline last ran (table scheduler_state),
// so the schedule survives restarts and laptop sleep.
type StateStore struct {
	db *sql.DB
}

func NewStateStore(db *sql.DB) *StateStore {
	return &StateStore{db: db}
}

// LastRun returns when pipeline last started and that run's status
// ("ok", "failed", or "" while unfinished), and false if it never ran.
func (s *StateStore) LastRun(ctx context.Context, pipeline string) (time.Time, string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, dbQueryTimeout)
	defer cancel()

	var (
		t      sql.NullTime
		status sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT last_started_at, last_status FROM scheduler_state WHERE pipeline = ?`, pipeline).Scan(&t, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, "", false, nil
	}
	if err != nil {
		return time.Time{}, "", false, fmt.Errorf("read scheduler_state for %s: %w", pipeline, err)
	}
	if !t.Valid {
		return time.Time{}, "", false, nil
	}
	return t.Time, status.String, true, nil
}

func (s *StateStore) RecordStart(ctx context.Context, pipeline string, at time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, dbQueryTimeout)
	defer cancel()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO scheduler_state (pipeline, last_started_at) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE last_started_at = VALUES(last_started_at)`,
		pipeline, at)
	if err != nil {
		return fmt.Errorf("record start for %s: %w", pipeline, err)
	}
	return nil
}

func (s *StateStore) RecordFinish(ctx context.Context, pipeline string, at time.Time, status string) error {
	ctx, cancel := context.WithTimeout(ctx, dbQueryTimeout)
	defer cancel()

	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduler_state SET last_finished_at = ?, last_status = ? WHERE pipeline = ?`,
		at, status, pipeline)
	if err != nil {
		return fmt.Errorf("record finish for %s: %w", pipeline, err)
	}
	return nil
}

// Pipeline runs RunFn whenever Interval has passed since the last recorded
// start. It replaces time.Ticker: a ticker loses ticks while the Docker VM
// is paused (laptop sleep) and restarts its phase on every process start.
type Pipeline struct {
	Name     string
	Interval time.Duration
	RunFn    func() error
	State    *StateStore
	Now      func() time.Time

	// RetryAfterFailure, when > 0, makes a failed run due again after this
	// long rather than after Interval.
	RetryAfterFailure time.Duration
}

// Due reports whether the pipeline should run now. A pipeline that has
// never run is due; a failed run is due again after RetryAfterFailure.
func (p *Pipeline) Due(ctx context.Context) (bool, error) {
	last, status, ok, err := p.State.LastRun(ctx, p.Name)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	wait := p.Interval
	if status == "failed" && p.RetryAfterFailure > 0 && p.RetryAfterFailure < wait {
		wait = p.RetryAfterFailure
	}
	return p.Now().Sub(last) >= wait, nil
}

// RunAndRecord runs RunFn and records it in scheduler_state.
func (p *Pipeline) RunAndRecord(ctx context.Context) error {
	return p.RunAndRecordWith(ctx, p.RunFn)
}

// RunAndRecordWith runs fn as this pipeline's run. Manual global runs (the
// sentinel "Check now") use it so they also reset the schedule. If the
// start cannot be recorded, fn is not run: otherwise the due-check would
// fire again every minute.
func (p *Pipeline) RunAndRecordWith(ctx context.Context, fn func() error) error {
	if err := p.State.RecordStart(ctx, p.Name, p.Now()); err != nil {
		return err
	}

	runErr := fn()

	status := "ok"
	if runErr != nil {
		status = "failed"
	}
	// Record the finish even if ctx was cancelled mid-run (shutdown).
	if err := p.State.RecordFinish(context.WithoutCancel(ctx), p.Name, p.Now(), status); err != nil {
		log.Printf("[ERROR] Pipeline %s: %v", p.Name, err)
	}
	return runErr
}

// Loop checks immediately, then every checkEvery, until ctx is cancelled.
func (p *Pipeline) Loop(ctx context.Context, checkEvery time.Duration) {
	log.Printf("[INFO] Pipeline %s: runs every %s, checked every %s", p.Name, p.Interval, checkEvery)
	p.tick(ctx)

	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("[INFO] Pipeline %s: stopping", p.Name)
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

func (p *Pipeline) tick(ctx context.Context) {
	due, err := p.Due(ctx)
	if err != nil {
		log.Printf("[ERROR] Pipeline %s: due check: %v", p.Name, err)
		return
	}
	if !due {
		return
	}
	if err := p.RunAndRecord(ctx); err != nil {
		log.Printf("[ERROR] Pipeline %s: run failed: %v", p.Name, err)
	}
}
