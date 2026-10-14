package runs

import (
	"context"
	"fmt"
	"log"
	"time"
)

// failedRunMessage is what the browser sees for a failed run. The real
// error is only logged, so DB/network details never reach the client.
const failedRunMessage = "run failed; check the scheduler logs"

// Executor runs one manual job and returns its summary.
type Executor interface {
	Execute(ctx context.Context, run *Run) (map[string]int, error)
}

// Consumer pulls manual runs off the Redis queue one at a time.
type Consumer struct {
	store      *Store
	exec       Executor
	popTimeout time.Duration
}

func NewConsumer(store *Store, exec Executor) *Consumer {
	return &Consumer{store: store, exec: exec, popTimeout: 5 * time.Second}
}

// Start blocks until ctx is cancelled. Shutdown is noticed within
// popTimeout; a job already running is allowed to finish.
func (c *Consumer) Start(ctx context.Context) {
	log.Println("[INFO] runs.Consumer: waiting for manual runs")
	for {
		if ctx.Err() != nil {
			log.Println("[INFO] runs.Consumer: stopping")
			return
		}
		run, err := c.store.Pop(ctx, c.popTimeout)
		if err != nil {
			if ctx.Err() != nil {
				log.Println("[INFO] runs.Consumer: stopping")
				return
			}
			log.Printf("[ERROR] runs.Consumer: %v", err)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
			continue
		}
		if run == nil {
			continue
		}
		c.process(ctx, run)
	}
}

func (c *Consumer) process(ctx context.Context, run *Run) {
	// Status writes must land even if shutdown starts mid-run.
	bg := context.WithoutCancel(ctx)

	// The web UI already reported a run this old as "timed out" (see
	// Store.Get). Running it now — e.g. after a scheduler restart, since
	// Redis keeps the queue — would send an alert the user no longer
	// expects and could duplicate a retry they started meanwhile.
	if c.store.now().Sub(run.CreatedAt) > staleAfter {
		log.Printf("[WARN] runs.Consumer: skipping run %s queued at %s: older than %s", run.ID, run.CreatedAt.Format(time.RFC3339), staleAfter)
		if err := c.store.MarkFailed(bg, run.ID, "timed out"); err != nil {
			log.Printf("[ERROR] runs.Consumer: mark %s failed: %v", run.ID, err)
		}
		return
	}

	if err := c.store.MarkRunning(bg, run.ID); err != nil {
		log.Printf("[ERROR] runs.Consumer: mark %s running: %v", run.ID, err)
	}
	log.Printf("[INFO] runs.Consumer: run %s (%s) for user %d started", run.ID, run.Type, run.UserID)

	summary, err := c.executeSafe(ctx, run)
	if err != nil {
		log.Printf("[ERROR] runs.Consumer: run %s failed: %v", run.ID, err)
		if markErr := c.store.MarkFailed(bg, run.ID, failedRunMessage); markErr != nil {
			log.Printf("[ERROR] runs.Consumer: mark %s failed: %v", run.ID, markErr)
		}
		return
	}
	if err := c.store.MarkDone(bg, run.ID, summary); err != nil {
		log.Printf("[ERROR] runs.Consumer: mark %s done: %v", run.ID, err)
	}
	log.Printf("[INFO] runs.Consumer: run %s done: %v", run.ID, summary)
}

func (c *Consumer) executeSafe(ctx context.Context, run *Run) (summary map[string]int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("run panicked: %v", r)
		}
	}()
	return c.exec.Execute(ctx, run)
}

// StartHeartbeat refreshes the heartbeat every `every` until ctx is
// cancelled, then deletes it so the API reports the scheduler offline
// right away instead of after HeartbeatTTL.
func StartHeartbeat(ctx context.Context, store *Store, every time.Duration) {
	beat := func() {
		if err := store.Heartbeat(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[ERROR] runs.Heartbeat: %v", err)
		}
	}
	beat()

	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := store.ClearHeartbeat(context.WithoutCancel(ctx)); err != nil {
				log.Printf("[ERROR] runs.Heartbeat: clear: %v", err)
			}
			return
		case <-ticker.C:
			beat()
		}
	}
}
