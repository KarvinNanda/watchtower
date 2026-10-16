// Package runs implements manual "Check now" runs: the API enqueues a job
// in Redis, the scheduler consumes it, and the web UI polls its status.
package runs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	TypeAsset    = "asset"
	TypeSentinel = "sentinel"
	// TypeAssetReport sends a status report of the user's active rules (#7).
	TypeAssetReport = "asset_report"

	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"

	HeartbeatInterval = 20 * time.Second
	HeartbeatTTL      = 60 * time.Second

	queueKey      = "wt:runs:queue"
	heartbeatKey  = "wt:scheduler:heartbeat"
	runKeyPrefix  = "wt:run:"
	lockKeyPrefix = "wt:run:lock:"

	runTTL     = 24 * time.Hour
	lockTTL    = 5 * time.Minute
	staleAfter = 15 * time.Minute
)

var (
	ErrNotFound    = errors.New("runs: run not found")
	ErrInvalidType = errors.New(`runs: type must be "asset", "asset_report" or "sentinel"`)
)

// RateLimitedError means the user already started a run of this type
// within the last lockTTL.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("runs: rate limited, retry after %s", e.RetryAfter)
}

// Run is one manual run as stored in Redis and returned to the owner.
type Run struct {
	ID         string         `json:"run_id"`
	UserID     uint64         `json:"-"`
	Type       string         `json:"type"`
	Status     string         `json:"status"`
	CreatedAt  time.Time      `json:"created_at"`
	StartedAt  *time.Time     `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at"`
	Summary    map[string]int `json:"summary,omitempty"`
	Error      string         `json:"error,omitempty"`
}

func ValidType(t string) bool {
	return t == TypeAsset || t == TypeSentinel || t == TypeAssetReport
}

type Store struct {
	rdb *redis.Client
	now func() time.Time
}

func NewStore(rdb *redis.Client) *Store {
	return &Store{rdb: rdb, now: time.Now}
}

// SchedulerAlive reports whether the scheduler has beaten within HeartbeatTTL.
func (s *Store) SchedulerAlive(ctx context.Context) (bool, error) {
	n, err := s.rdb.Exists(ctx, heartbeatKey).Result()
	if err != nil {
		return false, fmt.Errorf("runs: check heartbeat: %w", err)
	}
	return n == 1, nil
}

func (s *Store) Heartbeat(ctx context.Context) error {
	return s.rdb.Set(ctx, heartbeatKey, s.now().UTC().Format(time.RFC3339), HeartbeatTTL).Err()
}

func (s *Store) ClearHeartbeat(ctx context.Context) error {
	return s.rdb.Del(ctx, heartbeatKey).Err()
}

// Enqueue creates a queued run for userID unless one of the same type was
// started within lockTTL. The lock doubles as the per-user rate limit and
// as protection against queuing duplicates (double-click).
func (s *Store) Enqueue(ctx context.Context, userID uint64, runType string) (*Run, error) {
	if !ValidType(runType) {
		return nil, ErrInvalidType
	}

	lockKey := fmt.Sprintf("%s%d:%s", lockKeyPrefix, userID, runType)
	ok, err := s.rdb.SetNX(ctx, lockKey, "1", lockTTL).Result()
	if err != nil {
		return nil, fmt.Errorf("runs: acquire lock: %w", err)
	}
	if !ok {
		ttl, ttlErr := s.rdb.TTL(ctx, lockKey).Result()
		if ttlErr != nil || ttl <= 0 {
			ttl = time.Second
		}
		return nil, &RateLimitedError{RetryAfter: ttl}
	}

	id, err := newRunID()
	if err != nil {
		_ = s.rdb.Del(ctx, lockKey).Err()
		return nil, err
	}
	run := &Run{ID: id, UserID: userID, Type: runType, Status: StatusQueued, CreatedAt: s.now().UTC()}

	key := runKeyPrefix + id
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, key, map[string]interface{}{
		"user_id":    strconv.FormatUint(userID, 10),
		"type":       runType,
		"status":     StatusQueued,
		"created_at": run.CreatedAt.Format(time.RFC3339Nano),
	})
	pipe.Expire(ctx, key, runTTL)
	pipe.LPush(ctx, queueKey, id)
	if _, err := pipe.Exec(ctx); err != nil {
		_ = s.rdb.Del(ctx, lockKey).Err()
		return nil, fmt.Errorf("runs: enqueue: %w", err)
	}
	return run, nil
}

// Get returns run id if it belongs to userID. Another user's run is
// reported as ErrNotFound so its existence is not revealed. A run still
// queued/running after staleAfter is reported as failed ("timed out"),
// which covers a scheduler that died mid-job.
func (s *Store) Get(ctx context.Context, id string, userID uint64) (*Run, error) {
	run, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if run.UserID != userID {
		return nil, ErrNotFound
	}
	if (run.Status == StatusQueued || run.Status == StatusRunning) && s.now().Sub(run.CreatedAt) > staleAfter {
		run.Status = StatusFailed
		run.Error = "timed out"
	}
	return run, nil
}

// Pop waits up to timeout for the next queued run. It returns (nil, nil)
// when the queue stayed empty or the run's hash already expired.
func (s *Store) Pop(ctx context.Context, timeout time.Duration) (*Run, error) {
	res, err := s.rdb.BRPop(ctx, timeout, queueKey).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runs: pop: %w", err)
	}
	run, err := s.load(ctx, res[1])
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return run, err
}

// markScript updates a run hash only while it still exists. A plain HSET on
// an expired run would recreate the hash without TTL, user_id or created_at,
// leaving an orphan key that GET /api/runs/:id cannot decode.
var markScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
  return 0
end
redis.call('HSET', KEYS[1], unpack(ARGV))
return 1
`)

// mark writes field/value pairs to run id, or returns ErrNotFound if the
// run has already expired.
func (s *Store) mark(ctx context.Context, id string, fields ...string) error {
	args := make([]interface{}, len(fields))
	for i, f := range fields {
		args[i] = f
	}
	updated, err := markScript.Run(ctx, s.rdb, []string{runKeyPrefix + id}, args...).Int()
	if err != nil {
		return fmt.Errorf("runs: update run %s: %w", id, err)
	}
	if updated == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkRunning(ctx context.Context, id string) error {
	return s.mark(ctx, id,
		"status", StatusRunning,
		"started_at", s.now().UTC().Format(time.RFC3339Nano),
	)
}

func (s *Store) MarkDone(ctx context.Context, id string, summary map[string]int) error {
	b, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("runs: encode summary: %w", err)
	}
	return s.mark(ctx, id,
		"status", StatusDone,
		"finished_at", s.now().UTC().Format(time.RFC3339Nano),
		"summary", string(b),
	)
}

func (s *Store) MarkFailed(ctx context.Context, id, reason string) error {
	return s.mark(ctx, id,
		"status", StatusFailed,
		"finished_at", s.now().UTC().Format(time.RFC3339Nano),
		"error", reason,
	)
}

func (s *Store) load(ctx context.Context, id string) (*Run, error) {
	vals, err := s.rdb.HGetAll(ctx, runKeyPrefix+id).Result()
	if err != nil {
		return nil, fmt.Errorf("runs: load %s: %w", id, err)
	}
	if len(vals) == 0 {
		return nil, ErrNotFound
	}
	return decodeRun(id, vals)
}

func decodeRun(id string, v map[string]string) (*Run, error) {
	userID, err := strconv.ParseUint(v["user_id"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("runs: decode user_id of %s: %w", id, err)
	}
	created, err := time.Parse(time.RFC3339Nano, v["created_at"])
	if err != nil {
		return nil, fmt.Errorf("runs: decode created_at of %s: %w", id, err)
	}
	run := &Run{
		ID:         id,
		UserID:     userID,
		Type:       v["type"],
		Status:     v["status"],
		CreatedAt:  created,
		StartedAt:  parseOptionalTime(v["started_at"]),
		FinishedAt: parseOptionalTime(v["finished_at"]),
		Error:      v["error"],
	}
	if raw := v["summary"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &run.Summary); err != nil {
			return nil, fmt.Errorf("runs: decode summary of %s: %w", id, err)
		}
	}
	return run, nil
}

func parseOptionalTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	return &t
}

func newRunID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("runs: generate id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
