package scheduler

import (
	"context"
	"fmt"

	"github.com/karvin-nanda/watchtower/internal/runs"
)

type assetRunner interface {
	RunForUser(userID uint64) (AssetRunResult, error)
}

type sentinelRunner interface {
	RunWithResult() (SentinelRunResult, error)
}

type pipelineRecorder interface {
	RunAndRecordWith(ctx context.Context, fn func() error) error
}

// RunExecutor executes manual runs popped by runs.Consumer.
type RunExecutor struct {
	asset            assetRunner
	sentinel         sentinelRunner
	sentinelPipeline pipelineRecorder
}

func NewRunExecutor(asset assetRunner, sentinel sentinelRunner, sentinelPipeline pipelineRecorder) *RunExecutor {
	return &RunExecutor{asset: asset, sentinel: sentinel, sentinelPipeline: sentinelPipeline}
}

// Execute runs an asset job for run.UserID only, or a global sentinel job
// (recorded in scheduler_state, since it is a full run) and reports how
// many items reached the caller.
func (e *RunExecutor) Execute(ctx context.Context, run *runs.Run) (map[string]int, error) {
	switch run.Type {
	case runs.TypeAsset:
		res, err := e.asset.RunForUser(run.UserID)
		if err != nil {
			return nil, err
		}
		return map[string]int{
			"rules_checked": res.RulesChecked,
			"triggered":     res.Triggered,
			"sent":          res.Sent,
			"errors":        res.Errors,
			"stale":         res.Stale,
		}, nil

	case runs.TypeSentinel:
		var res SentinelRunResult
		err := e.sentinelPipeline.RunAndRecordWith(ctx, func() error {
			var runErr error
			res, runErr = e.sentinel.RunWithResult()
			return runErr
		})
		if err != nil {
			return nil, err
		}
		return map[string]int{
			"items_fetched": res.Fetched,
			"new_items":     res.New,
			"sent_to_you":   res.SentToUser[run.UserID],
		}, nil
	}
	return nil, fmt.Errorf("scheduler: unknown run type %q", run.Type)
}
