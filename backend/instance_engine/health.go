package instance_engine

import (
	"context"
	"time"
)

// LoopStallTimeout is how long a background loop (reaper, archive worker,
// webhook worker) may go without a heartbeat before the liveness probe
// calls it stalled. Every loop beats at the top of each tick and after
// each job, and no job runs longer than its own timeout (10 s archive, 6 s
// webhook), so a healthy loop never approaches this. (phase 9)
const LoopStallTimeout = 30 * time.Second

// Check results as they appear in the probe body. The body names the check
// and its state only: an error string could leak store addresses to an
// unauthenticated caller (threat model), so the detail goes to the log.
const (
	CheckOK         = "ok"
	CheckError      = "error"
	CheckStalled    = "stalled"
	CheckNotRunning = "not_running"
)

// Overall statuses.
const (
	StatusOK          = "ok"
	StatusDegraded    = "degraded"    // serving, but something needs attention (Postgres down: archives queue up)
	StatusUnavailable = "unavailable" // must not receive traffic (Redis down or a loop dead)
)

// HealthReport is the JSON body of /live, /ready and /health.
type HealthReport struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Healthy reports whether the probe should answer 200.
func (h HealthReport) Healthy() bool { return h.Status != StatusUnavailable }

// beat records a heartbeat for a loop; check compares it with the clock.
func (e *Engine) loopCheck(lastBeat int64) string {
	if lastBeat == 0 {
		return CheckNotRunning
	}
	if e.Clock.Now().Sub(time.Unix(0, lastBeat)) > LoopStallTimeout {
		return CheckStalled
	}
	return CheckOK
}

// Liveness answers "is this process still doing its job": the reaper and
// the two queue workers have each ticked within LoopStallTimeout. It never
// touches a store, so an outage in Redis or Postgres does not make the
// orchestrator restart a process that is behaving correctly.
func (e *Engine) Liveness() HealthReport {
	r := HealthReport{Status: StatusOK, Checks: map[string]string{
		"reaper":         e.loopCheck(e.reaperBeat.Load()),
		"archive_worker": e.loopCheck(e.Archiver.lastBeat.Load()),
		"webhook_worker": e.loopCheck(e.Webhooks.lastBeat.Load()),
	}}
	for _, c := range r.Checks {
		if c != CheckOK {
			r.Status = StatusUnavailable
		}
	}
	return r
}

// Readiness answers "may this process receive traffic": everything
// Liveness checks, plus the stores. Redis unreachable is unavailable (every
// report would be a 500). Postgres unreachable is degraded, not
// unavailable: the API keeps serving, terminal instances queue in Redis and
// are archived when Postgres returns (contract A2, A3), and the runbook
// says what to watch while that lasts.
func (e *Engine) Readiness(ctx context.Context) HealthReport {
	r := e.Liveness()
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := e.RDB.Ping(ctx).Err(); err != nil {
		e.Log.Warn("readiness: redis ping failed", "err", err)
		r.Checks["redis"] = CheckError
		r.Status = StatusUnavailable
	} else {
		r.Checks["redis"] = CheckOK
	}
	if err := e.DB.Ping(ctx); err != nil {
		e.Log.Warn("readiness: postgres ping failed", "err", err)
		r.Checks["postgres"] = CheckError
		if r.Status == StatusOK {
			r.Status = StatusDegraded
		}
	} else {
		r.Checks["postgres"] = CheckOK
	}
	return r
}
