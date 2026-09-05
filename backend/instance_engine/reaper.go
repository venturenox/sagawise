package instance_engine

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"
)

// deadlinesKey is a Redis sorted set. Score = unix-millisecond deadline,
// member = "<workflow_instance_id>:<task_index>". A task appears here from
// the moment it is PUBLISHED until it is COMPLETED or FAILED, or until its
// instance goes terminal (contract T5, I5).
const deadlinesKey = "task_deadlines"

// reaperBatch bounds one tick; a larger backlog drains over several ticks.
const reaperBatch = 1000

func deadlineMember(workflowInstanceID string, taskIndex int) string {
	return workflowInstanceID + ":" + strconv.Itoa(taskIndex)
}

// StartDeadlineReaper runs a background loop that fails any PUBLISHED task
// whose deadline has passed. It is stateless: it re-reads Redis on every
// tick, so it picks up deadlines written by a previous process.
func (e *Engine) StartDeadlineReaper(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		log.Println("Deadline reaper started")

		for {
			select {
			case <-ctx.Done():
				log.Println("Deadline reaper stopped")
				return
			case <-ticker.C:
				e.reapExpiredDeadlines(ctx)
			}
		}
	}()
}

// reapBatchResult is the counter tuple reap_batch returns.
type reapBatchResult struct {
	Reaped, Webhooks, Archives, Dropped int64
}

// reapExpiredDeadlines performs one reaper tick against e.Clock.Now(). The
// whole tick is a single script call: reap_batch reads the overdue members
// of task_deadlines and reaps each one inside Redis, so a tick costs one
// round-trip rather than one per expired task and the reaper's lag stops
// growing linearly with the number of tasks that expire together. (phase 7)
//
// Each member still goes through the same `run` path as an HTTP report, so
// marking the task FAILED and removing its deadline remain one atomic step
// and the deadline is never spent before the failure is recorded. A member
// whose instance is gone or unreadable is dropped inside the script; a
// Redis error aborts the tick and leaves every member for the next one.
// (#4, TO5, TO6)
//
// The reaper never calls a webhook itself; the script enqueues them and the
// webhook worker delivers them, so one slow endpoint cannot stall the tick. (#5)
func (e *Engine) reapExpiredDeadlines(ctx context.Context) {
	res, err := e.reapBatch(ctx)
	if err != nil {
		// Nothing was decided; the deadlines are still there next tick.
		log.Printf("Reaper: %v (will retry)", err)
		return
	}
	if res.Dropped > 0 {
		log.Printf("Reaper: dropped %d unresolvable deadline(s)", res.Dropped)
	}
	if res.Reaped == 0 {
		return
	}
	log.Printf("Reaper: timed out %d task(s)", res.Reaped)
	if res.Webhooks > 0 {
		e.Webhooks.Nudge()
	}
	if res.Archives > 0 {
		e.Archiver.Nudge()
	}
}

// reapBatch runs one reap_batch call and decodes its counters.
func (e *Engine) reapBatch(ctx context.Context) (reapBatchResult, error) {
	now := e.Clock.Now()
	// KEYS[1] is unused by reap_batch but keeps the script's key list (and
	// therefore its Cluster key routing) identical for every action.
	keys := []string{instanceKeyPrefix, deadlinesKey, archiveQueueKey, webhookQueueKey}
	raw, err := e.script.Run(ctx, e.RDB, keys,
		"reap_batch", 0, reaperBatch, now.Unix(), now.UnixMilli(), "", "", instanceKeyPrefix).Result()
	if err != nil {
		return reapBatchResult{}, fmt.Errorf("reap batch: %w", err)
	}
	parts, ok := raw.([]interface{})
	if !ok || len(parts) != 4 {
		return reapBatchResult{}, fmt.Errorf("reap batch: unexpected reply %#v", raw)
	}
	var res reapBatchResult
	out := []*int64{&res.Reaped, &res.Webhooks, &res.Archives, &res.Dropped}
	for i, p := range parts {
		n, ok := p.(int64)
		if !ok {
			return reapBatchResult{}, fmt.Errorf("reap batch: counter %d is %#v", i, p)
		}
		*out[i] = n
	}
	return res, nil
}
