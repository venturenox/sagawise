package instance_engine

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
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

// reapExpiredDeadlines performs one reaper tick against e.Clock.Now(). Every
// overdue member is handed to the transition script as a `reap`: marking the
// task FAILED and removing its deadline happen in that one atomic step, so
// the deadline is never spent before the failure is recorded. A Redis error
// leaves the member in place for the next tick. (#4, TO5, TO6)
//
// The reaper never calls a webhook itself; the script enqueues it and the
// webhook worker delivers it, so one slow endpoint cannot stall the tick. (#5)
func (e *Engine) reapExpiredDeadlines(ctx context.Context) {
	now := strconv.FormatInt(e.Clock.Now().UnixMilli(), 10)
	members, err := e.RDB.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key: deadlinesKey, Start: "-inf", Stop: now, ByScore: true, Offset: 0, Count: reaperBatch,
	}).Result()
	if err != nil {
		log.Printf("Reaper: error reading deadlines: %v", err)
		return
	}

	for _, member := range members {
		id, index, ok := splitMember(member)
		if !ok {
			log.Printf("Reaper: malformed deadline member %q; dropping it", member)
			e.dropDeadline(ctx, member)
			continue
		}
		res, err := e.transition(ctx, id, "reap", false, []int{index}, "")
		if err != nil {
			// Nothing was decided; the deadline is still there next tick.
			log.Printf("Reaper: %s: %v (will retry)", member, err)
			continue
		}
		switch res.Code {
		case "OK":
			log.Printf("Reaper: timeout for %s task %d", id, index)
			e.Webhooks.Nudge()
			if res.TerminalNow {
				e.Archiver.Nudge()
			}
		case "STALE":
			// The task moved on or the instance is terminal; the script
			// dropped the member.
		case "NOT_FOUND", "BAD_SCHEMA", "TASK_NOT_FOUND":
			// The document is gone or unreadable; the deadline can never
			// resolve, so it is dropped rather than retried forever.
			log.Printf("Reaper: %s: instance %s (%s); dropping deadline", member, res.Code, id)
			e.dropDeadline(ctx, member)
		default:
			log.Printf("Reaper: %s: unexpected result %s", member, res.Code)
		}
	}
}

func (e *Engine) dropDeadline(ctx context.Context, member string) {
	if err := e.RDB.ZRem(ctx, deadlinesKey, member).Err(); err != nil {
		log.Printf("Reaper: drop %s: %v", member, err)
	}
}
