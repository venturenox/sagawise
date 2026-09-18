package instance_engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// Metrics are the engine's OpenTelemetry instruments (phase 9). They are
// exported through the Prometheus reader main installs, so the names below
// appear as, for example, sagawise_reaper_lag_seconds and
// sagawise_queue_jobs_total{queue="archive",result="failed"}.
// deploy/alerts.yml alerts on them; docs/operations.md explains each.
//
// An Engine always has a Metrics (New installs a no-op one), so nothing
// needs a nil check; UseMeterProvider swaps in the real instruments.
type Metrics struct {
	// Reports and instances.
	reports           metric.Int64Counter // sagawise.reports {action, result}
	instancesStarted  metric.Int64Counter // sagawise.instances.started
	instancesTerminal metric.Int64Counter // sagawise.instances.terminal {state}

	// Reaper.
	reaperTicks      metric.Int64Counter     // sagawise.reaper.ticks {result}
	tasksTimedOut    metric.Int64Counter     // sagawise.tasks.timed_out
	deadlinesDropped metric.Int64Counter     // sagawise.reaper.deadlines_dropped
	reaperLag        metric.Float64Histogram // sagawise.reaper.lag (s): how late each timeout fired

	// Queues.
	queueJobs metric.Int64Counter // sagawise.queue.jobs {queue, result}
}

// Job results recorded on sagawise.queue.jobs.
const (
	jobDone    = "done"
	jobFailed  = "failed"
	jobGaveUp  = "gave_up"
	jobDropped = "dropped" // malformed or unresolvable; not an error
)

// pingTimeout bounds each store ping made while observing sagawise.store.up.
const pingTimeout = 2 * time.Second

func (e *Engine) noopMetrics() {
	m, err := e.newMetrics(noop.NewMeterProvider())
	if err != nil {
		panic("noop meter provider refused an instrument: " + err.Error())
	}
	e.Metrics = m
}

// UseMeterProvider creates the engine's instruments on mp and registers the
// observable gauges that read Redis at scrape time. main calls it with the
// global provider once the Prometheus reader is installed; tests call it
// with a provider backed by a ManualReader.
func (e *Engine) UseMeterProvider(mp metric.MeterProvider) error {
	m, err := e.newMetrics(mp)
	if err != nil {
		return err
	}
	e.Metrics = m
	return nil
}

func (e *Engine) newMetrics(mp metric.MeterProvider) (*Metrics, error) {
	meter := mp.Meter("wtfsaga/instance_engine")
	m := &Metrics{}
	var errs []error
	counter := func(name, desc string) metric.Int64Counter {
		c, err := meter.Int64Counter(name, metric.WithDescription(desc))
		errs = append(errs, err)
		return c
	}
	m.reports = counter("sagawise.reports", "Reports received on /update_instance, by action and outcome code.")
	m.instancesStarted = counter("sagawise.instances.started", "Workflow instances created.")
	m.instancesTerminal = counter("sagawise.instances.terminal", "Workflow instances that reached COMPLETED or FAILED.")
	m.reaperTicks = counter("sagawise.reaper.ticks", "Reaper ticks, by result (ok or error).")
	m.tasksTimedOut = counter("sagawise.tasks.timed_out", "Tasks the reaper marked FAILED because their deadline passed.")
	m.deadlinesDropped = counter("sagawise.reaper.deadlines_dropped", "Deadlines discarded because their instance no longer exists or is unreadable.")
	m.queueJobs = counter("sagawise.queue.jobs", "Queue jobs finished, by queue (archive, webhook) and result (done, failed, gave_up, dropped).")

	var err error
	m.reaperLag, err = meter.Float64Histogram("sagawise.reaper.lag",
		metric.WithDescription("How long after its deadline each task was failed by the reaper."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 300))
	errs = append(errs, err)

	// Observable gauges: each callback is one cheap Redis call at scrape
	// time. When Redis is down the callback returns the error and the
	// series is simply absent for that scrape; sagawise_store_up says why.
	gauge := func(name, desc, unit string, observe func(context.Context, metric.Int64Observer) error) {
		g, err := meter.Int64ObservableGauge(name, metric.WithDescription(desc), metric.WithUnit(unit),
			metric.WithInt64Callback(observe))
		errs = append(errs, err)
		_ = g
	}
	gauge("sagawise.deadlines.pending", "Armed task deadlines (PUBLISHED tasks waiting to be consumed).", "{task}",
		func(ctx context.Context, o metric.Int64Observer) error {
			n, err := e.RDB.ZCard(ctx, deadlinesKey).Result()
			if err != nil {
				return err
			}
			o.Observe(n)
			return nil
		})
	gauge("sagawise.queue.pending", "Jobs in a queue, due or leased.", "{job}",
		func(ctx context.Context, o metric.Int64Observer) error {
			for _, w := range []*Worker{e.Archiver, e.Webhooks} {
				n, err := w.Pending(ctx)
				if err != nil {
					return err
				}
				o.Observe(n, metric.WithAttributes(attribute.String("queue", w.Name)))
			}
			return nil
		})
	gauge("sagawise.reaper.last_tick", "Unix time of the reaper's last completed tick.", "s",
		func(_ context.Context, o metric.Int64Observer) error {
			if t := e.reaperBeat.Load(); t > 0 {
				o.Observe(t / int64(time.Second))
			}
			return nil
		})
	gauge("sagawise.store.up", "1 when the store answers a ping, 0 when it does not.", "",
		func(ctx context.Context, o metric.Int64Observer) error {
			ctx, cancel := context.WithTimeout(ctx, pingTimeout)
			defer cancel()
			o.Observe(boolGauge(e.RDB.Ping(ctx).Err() == nil), metric.WithAttributes(attribute.String("store", "redis")))
			o.Observe(boolGauge(e.DB.Ping(ctx) == nil), metric.WithAttributes(attribute.String("store", "postgres")))
			return nil
		})
	gauge("sagawise.redis.appendonly", "1 when Redis reports appendonly yes (deadlines survive a restart), 0 when not. Absent when CONFIG GET is not permitted.", "",
		func(ctx context.Context, o metric.Int64Observer) error {
			on, known, err := RedisAppendOnly(ctx, e.RDB)
			if err != nil || !known {
				return err
			}
			o.Observe(boolGauge(on))
			return nil
		})

	overdue, err := meter.Float64ObservableGauge("sagawise.reaper.overdue",
		metric.WithDescription("Age of the oldest deadline the reaper has not yet failed; 0 when nothing is overdue. Grows when the reaper is stalled or behind."),
		metric.WithUnit("s"),
		metric.WithFloat64Callback(func(ctx context.Context, o metric.Float64Observer) error {
			zs, err := e.RDB.ZRangeWithScores(ctx, deadlinesKey, 0, 0).Result()
			if err != nil {
				return err
			}
			lag := 0.0
			if len(zs) == 1 {
				if age := float64(e.Clock.Now().UnixMilli()) - zs[0].Score; age > 0 {
					lag = age / 1000
				}
			}
			o.Observe(lag)
			return nil
		}))
	errs = append(errs, err)
	_ = overdue

	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("create metrics: %w", err)
	}
	return m, nil
}

func boolGauge(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// RedisAppendOnly reports whether Redis has AOF persistence on. known is
// false when the server refuses CONFIG GET (managed services often do), in
// which case the caller cannot tell and should say so rather than guess.
func RedisAppendOnly(ctx context.Context, rdb *redis.Client) (on, known bool, err error) {
	res, err := rdb.ConfigGet(ctx, "appendonly").Result()
	if err != nil {
		if isConfigForbidden(err) {
			return false, false, nil
		}
		return false, false, err
	}
	v, ok := res["appendonly"]
	if !ok {
		return false, false, nil
	}
	return v == "yes", true, nil
}

// isConfigForbidden recognises a CONFIG command that the server disables
// (rename-command, managed services) or the ACL denies, as opposed to a
// connectivity error.
func isConfigForbidden(err error) bool {
	s := strings.ToLower(err.Error())
	for _, marker := range []string{"unknown command", "noperm", "not allowed", "err config"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// attrs helpers keep the call sites short.
func actionResult(action, result string) metric.MeasurementOption {
	return metric.WithAttributes(attribute.String("action", action), attribute.String("result", result))
}

func queueResult(queue, result string) metric.MeasurementOption {
	return metric.WithAttributes(attribute.String("queue", queue), attribute.String("result", result))
}

func resultAttr(result string) metric.MeasurementOption {
	return metric.WithAttributes(attribute.String("result", result))
}

func stateAttr(state string) metric.MeasurementOption {
	return metric.WithAttributes(attribute.String("state", state))
}

// lagSeconds converts a deadline score (unix ms) to how late `now` is.
func lagSeconds(now time.Time, score string) (float64, bool) {
	ms, err := strconv.ParseFloat(score, 64)
	if err != nil {
		return 0, false
	}
	lag := float64(now.UnixMilli()) - ms
	if lag < 0 {
		lag = 0
	}
	return lag / 1000, true
}
