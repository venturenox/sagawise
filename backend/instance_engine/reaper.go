package instance_engine

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

// DeadlinesKey is a Redis sorted set. Score = unix-millisecond deadline,
// member = "<workflow_instance_id>:<task_index>". A task appears here from
// the moment it is PUBLISHED until it is COMPLETED or FAILED.
const DeadlinesKey = "task_deadlines"

// DeadlineMember builds the sorted-set member for a task.
func DeadlineMember(workflowInstanceID string, taskIndex string) string {
	return workflowInstanceID + ":" + taskIndex
}

// StartDeadlineReaper runs a background loop that fails any PUBLISHED task
// whose deadline has passed. It is stateless: it re-reads Redis on every
// tick, so it picks up deadlines written by a previous process.
func StartDeadlineReaper(ctx context.Context, rdb *redis.Client, conn *pgx.Conn, interval time.Duration) {
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
				reapExpiredDeadlines(ctx, rdb, conn)
			}
		}
	}()
}

func reapExpiredDeadlines(ctx context.Context, rdb *redis.Client, conn *pgx.Conn) {
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)

	members, err := rdb.ZRangeByScore(ctx, DeadlinesKey, &redis.ZRangeBy{
		Min: "-inf",
		Max: now,
	}).Result()
	if err != nil {
		log.Printf("Reaper: error reading deadlines: %v", err)
		return
	}

	for _, member := range members {
		// Atomic claim: ZREM returns 1 for exactly one caller.
		removed, err := rdb.ZRem(ctx, DeadlinesKey, member).Result()
		if err != nil || removed == 0 {
			continue
		}

		parts := strings.SplitN(member, ":", 2)
		if len(parts) != 2 {
			log.Printf("Reaper: malformed deadline member %q", member)
			continue
		}
		workflowInstanceID, taskIndex := parts[0], parts[1]
		key := "workflow_instance:" + workflowInstanceID

		// The deadline is a hint; the task state is the truth.
		var states []string
		res, err := rdb.JSONGet(ctx, key, "$."+taskIndex+".state").Result()
		if err != nil {
			log.Printf("Reaper: error reading %s task %s: %v", key, taskIndex, err)
			continue
		}
		json.Unmarshal([]byte(res), &states)
		if len(states) == 0 || states[0] != "PUBLISHED" {
			continue
		}

		log.Printf("Reaper: timeout for %s task %s", workflowInstanceID, taskIndex)
		ReportFailure(ctx, rdb, conn, key, workflowInstanceID, taskIndex)
	}
}