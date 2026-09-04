package main

import (
	"sort"
	"time"
)

// Results is the machine-readable record of one run (load.json).
type Results struct {
	Label     string            `json:"label"`
	Date      string            `json:"date"`
	Commit    string            `json:"commit"`
	Dirty     bool              `json:"dirty"`
	Env       map[string]string `json:"env"`
	Config    map[string]string `json:"config"`
	Rates     []RateResult      `json:"rates"`
	ReaperLag LagResult         `json:"reaper_lag"`
}

// RateResult is one held rate: latency per endpoint, errors, Redis cost,
// and whether every completed saga was archived.
type RateResult struct {
	TargetSagasPerSec float64                  `json:"target_sagas_per_sec"`
	Duration          string                   `json:"duration"`
	SagasStarted      int                      `json:"sagas_started"`
	SagasCompleted    int                      `json:"sagas_completed"`
	AchievedSagasPS   float64                  `json:"achieved_sagas_per_sec"`
	Requests          int                      `json:"requests"`
	Errors            int                      `json:"errors"`
	ErrorRate         float64                  `json:"error_rate"`
	Endpoints         map[string]LatencyResult `json:"endpoints"`
	RedisCmdsPerSaga  float64                  `json:"redis_cmds_per_saga"`
	ArchiveExpected   int                      `json:"archive_expected"`
	ArchiveRows       int                      `json:"archive_rows"`
	ArchiveMissing    int                      `json:"archive_missing"`
}

type LatencyResult struct {
	Count int     `json:"count"`
	P50ms float64 `json:"p50_ms"`
	P95ms float64 `json:"p95_ms"`
	P99ms float64 `json:"p99_ms"`
	MaxMs float64 `json:"max_ms"`
}

// LagResult is the reaper-lag measurement: time from a task's deadline to
// the arrival of its failure webhook.
type LagResult struct {
	Tasks    int     `json:"tasks"`
	Received int     `json:"received"`
	Missing  int     `json:"missing"`
	P50ms    float64 `json:"p50_ms"`
	P99ms    float64 `json:"p99_ms"`
	MaxMs    float64 `json:"max_ms"`
	MinMs    float64 `json:"min_ms"`
}

func latencyStats(samples []time.Duration) LatencyResult {
	if len(samples) == 0 {
		return LatencyResult{}
	}
	s := append([]time.Duration(nil), samples...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	pct := func(q float64) float64 {
		i := int(q * float64(len(s)-1))
		return float64(s[i]) / 1e6
	}
	return LatencyResult{Count: len(s), P50ms: pct(0.5), P95ms: pct(0.95), P99ms: pct(0.99), MaxMs: float64(s[len(s)-1]) / 1e6}
}
