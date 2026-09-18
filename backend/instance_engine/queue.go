package instance_engine

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// A zqueue is a durable work queue in a Redis sorted set: the member is the
// job, the score is the unix-millisecond time it is due. The transition
// script enqueues jobs in the same atomic step as the state change they
// follow from, so a job can never be lost between the two. The pattern is
// the one task_deadlines already uses. (#5, #9)
type zqueue struct {
	rdb      *redis.Client
	key      string
	attempts string // hash: member -> attempts so far
}

// claimScript leases every due member: its score moves to now+lease so no
// other worker (or tick) picks it up until the lease expires. A worker that
// dies mid-job therefore hands the job back automatically; consumers are
// idempotent, so at-least-once is the guarantee.
var claimScript = redis.NewScript(`
local due = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, ARGV[3])
for _, m in ipairs(due) do
  redis.call('ZADD', KEYS[1], ARGV[2], m)
end
return due
`)

func (q *zqueue) claim(ctx context.Context, now time.Time, lease time.Duration, n int) ([]string, error) {
	res, err := claimScript.Run(ctx, q.rdb, []string{q.key},
		now.UnixMilli(), now.Add(lease).UnixMilli(), n).StringSlice()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	return res, nil
}

// done removes a finished job and its attempt count.
func (q *zqueue) done(ctx context.Context, member string) error {
	pipe := q.rdb.Pipeline()
	pipe.ZRem(ctx, q.key, member)
	pipe.HDel(ctx, q.attempts, member)
	_, err := pipe.Exec(ctx)
	return err
}

// bump counts one more failed attempt and returns the new count.
func (q *zqueue) bump(ctx context.Context, member string) (int64, error) {
	return q.rdb.HIncrBy(ctx, q.attempts, member, 1).Result()
}

// reschedule sets when a job is next due.
func (q *zqueue) reschedule(ctx context.Context, member string, due time.Time) error {
	return q.rdb.ZAdd(ctx, q.key, redis.Z{Score: float64(due.UnixMilli()), Member: member}).Err()
}

// Worker drains a zqueue: every tick (or nudge) it claims the due jobs and
// runs Work on each. A nil result is done; an error schedules a retry after
// Backoff, and after MaxAttempts (0 = never) the job is dropped, logged and
// counted. The engine clock is the source of "now" so tests drive it
// deterministically.
type Worker struct {
	Name     string
	Interval time.Duration // tick period
	Lease    time.Duration // how long a claimed job is invisible to other claims
	Batch    int           // jobs per claim
	Parallel int           // concurrent jobs per tick (1 = sequential)
	Timeout  time.Duration // per-job context timeout
	// Backoff returns the delay before attempt n+1 after n failures.
	Backoff     func(attempts int64) time.Duration
	MaxAttempts int64
	Work        func(ctx context.Context, member string) error
	// Describe turns a job member into the log attributes that identify it
	// (instance_id, task_index), so every line about a job names its
	// instance.
	Describe func(member string) []any

	queue    *zqueue
	clock    Clock
	log      func() *slog.Logger
	onResult func(ctx context.Context, result string) // metrics hook, see New
	wake     chan struct{}
	wg       sync.WaitGroup

	// lastBeat is the unix-nanosecond time of the last tick or finished job:
	// the liveness signal (health.go).
	lastBeat atomic.Int64

	// GiveUps counts jobs dropped after MaxAttempts; also on the
	// sagawise.queue.jobs metric as result="gave_up".
	GiveUps atomic.Int64
}

func (w *Worker) beat() { w.lastBeat.Store(w.clock.Now().UnixNano()) }

func (w *Worker) logger() *slog.Logger {
	if w.log != nil {
		return w.log()
	}
	return slog.Default()
}

func (w *Worker) jobAttrs(member string) []any {
	if w.Describe != nil {
		return w.Describe(member)
	}
	return []any{"job", member}
}

func (w *Worker) result(ctx context.Context, r string) {
	if w.onResult != nil {
		w.onResult(ctx, r)
	}
}

// Nudge asks the worker to run a tick now rather than at the next interval.
// It never blocks; a nudge that finds one already pending is redundant.
func (w *Worker) Nudge() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Start runs the worker loop until ctx is cancelled. In-flight jobs finish
// (bounded by Timeout) before Stop returns.
func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.Interval)
		defer ticker.Stop()
		w.beat()
		w.logger().Info("worker started")
		for {
			select {
			case <-ctx.Done():
				w.logger().Info("worker stopped")
				return
			case <-ticker.C:
			case <-w.wake:
			}
			w.tick(ctx)
		}
	}()
}

// Stop waits for the loop (and the tick it may be in) to finish. Cancel the
// Start context first.
func (w *Worker) Stop() { w.wg.Wait() }

// Pending returns the number of queued jobs, due or leased.
func (w *Worker) Pending(ctx context.Context) (int64, error) {
	return w.queue.rdb.ZCard(ctx, w.queue.key).Result()
}

// tick claims one batch and runs it. It is safe to call concurrently with
// itself: the claim is atomic and every job is leased to one caller.
func (w *Worker) tick(ctx context.Context) {
	w.beat()
	members, err := w.queue.claim(ctx, w.clock.Now(), w.Lease, w.Batch)
	if err != nil {
		w.logger().Error("claim failed", "err", err)
		return
	}
	if len(members) == 0 {
		return
	}
	parallel := w.Parallel
	if parallel < 1 {
		parallel = 1
	}
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, m := range members {
		sem <- struct{}{}
		wg.Add(1)
		go func(member string) {
			defer wg.Done()
			defer func() { <-sem }()
			w.run(ctx, member)
		}(m)
	}
	wg.Wait()
}

func (w *Worker) run(loopCtx context.Context, member string) {
	// The job outlives a cancelled loop (shutdown) up to its own timeout, so
	// an insert or a delivery in progress is not cut off mid-way.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(loopCtx), w.Timeout)
	defer cancel()
	defer w.beat()
	l := w.logger().With(w.jobAttrs(member)...)

	err := w.Work(ctx, member)
	if errors.Is(err, errDropJob) {
		// Unresolvable, not failed: removed without a retry and counted apart.
		w.result(ctx, jobDropped)
		err = nil
	}
	if err != nil {
		w.result(ctx, jobFailed)
		n, berr := w.queue.bump(ctx, member)
		if berr != nil {
			// The lease still expires; the job comes back on its own.
			l.Error("job failed; attempt count not recorded", "err", err, "bump_err", berr)
			return
		}
		if w.MaxAttempts > 0 && n >= w.MaxAttempts {
			w.GiveUps.Add(1)
			w.result(ctx, jobGaveUp)
			l.Error("giving up on job", "attempts", n, "err", err)
			if derr := w.queue.done(ctx, member); derr != nil {
				l.Error("drop failed", "err", derr)
			}
			return
		}
		delay := w.Backoff(n)
		if rerr := w.queue.reschedule(ctx, member, w.clock.Now().Add(delay)); rerr != nil {
			l.Error("reschedule failed", "err", rerr)
		}
		l.Warn("job failed; will retry", "attempt", n, "retry_in", delay.String(), "err", err)
		return
	}
	w.result(ctx, jobDone)
	if err := w.queue.done(ctx, member); err != nil {
		l.Error("ack failed", "err", err)
	}
}

// errDropJob is returned (wrapped) by a Work function for a job that can
// never succeed and should be removed without counting as a failure: an
// instance that no longer exists, a malformed member.
var errDropJob = errors.New("drop job")

// expBackoff returns base × factor^(attempts-1), capped.
func expBackoff(base time.Duration, factor float64, cap time.Duration) func(int64) time.Duration {
	return func(attempts int64) time.Duration {
		d := base
		for i := int64(1); i < attempts && d < cap; i++ {
			d = time.Duration(float64(d) * factor)
		}
		if d > cap {
			d = cap
		}
		return d
	}
}

// splitMember parses "<id>:<index>".
func splitMember(member string) (id string, index int, ok bool) {
	for i := len(member) - 1; i >= 0; i-- {
		if member[i] == ':' {
			n, err := strconv.Atoi(member[i+1:])
			if err != nil || n < 0 {
				return "", 0, false
			}
			return member[:i], n, true
		}
	}
	return "", 0, false
}
