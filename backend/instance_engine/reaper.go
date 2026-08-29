package instance_engine

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// deadlinesKey is a Redis sorted set. Score = unix-millisecond deadline,
// member = "<workflow_instance_id>:<task_index>". A task appears here from
// the moment it is PUBLISHED until it is COMPLETED or FAILED.
const deadlinesKey = "task_deadlines"

func deadlineMember(workflowInstanceID, taskIndex string) string {
	return workflowInstanceID + ":" + taskIndex
}

// StartDeadlineReaper runs a background loop that fails any PUBLISHED task
// whose deadline has passed. It is stateless: it re-reads Redis on every
// tick, so it picks up deadlines written by a previous process.
func StartDeadlineReaper(ctx context.Context, rdb *redis.Client, conn *pgxpool.Pool, interval time.Duration) {
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

func reapExpiredDeadlines(ctx context.Context, rdb *redis.Client, conn *pgxpool.Pool) {
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	members, err := rdb.ZRangeByScore(ctx, deadlinesKey, &redis.ZRangeBy{Min: "-inf", Max: now}).Result()
	if err != nil {
		log.Printf("Reaper: error reading deadlines: %v", err)
		return
	}

	for _, member := range members {
		// Atomic claim: ZREM returns 1 for exactly one caller.
		removed, err := rdb.ZRem(ctx, deadlinesKey, member).Result()
		if err != nil || removed == 0 {
			continue
		}

		id, index, ok := strings.Cut(member, ":")
		if !ok {
			log.Printf("Reaper: malformed deadline member %q", member)
			continue
		}
		key := "workflow_instance:" + id

		// The deadline is a hint; the task state is the truth.
		if state, _ := jsonFirstMatch[string](rdb, key, "$."+index+".state"); state != "PUBLISHED" {
			continue
		}

		log.Printf("Reaper: timeout for %s task %s", id, index)
		reportFailure(rdb, conn, key, id, index)
	}
}
