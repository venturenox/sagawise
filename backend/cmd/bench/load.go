package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// loader drives sagas over HTTP. It is open-loop: sagas start on a fixed
// schedule regardless of how the server keeps up, so latency under load is
// measured, not hidden by back-pressure.
// benchAPIKey is the key the launched server is configured with and the
// loader sends; the bench measures the authenticated path, as production
// runs it.
const benchAPIKey = "bench-api-key" // #nosec G101 -- throwaway key for the server the bench launches on loopback

type loader struct {
	base   string
	client *http.Client
}

type sample struct {
	endpoint string
	dur      time.Duration
	ok       bool
}

// call performs one request and returns the body and whether it was 2xx.
func (l *loader) call(method, path, body string) (string, time.Duration, bool) {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	start := time.Now()
	req, err := http.NewRequest(method, l.base+path, rd)
	if err != nil {
		return "", time.Since(start), false
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+benchAPIKey)
	resp, err := l.client.Do(req)
	if err != nil {
		return "", time.Since(start), false
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return string(data), time.Since(start), resp.StatusCode >= 200 && resp.StatusCode < 300
}

// flowSpec describes one saga shape the loader can drive.
type flowSpec struct {
	workflow string
	steps    []flowStep
	payload  string // publish body
}

type flowStep struct{ action, topic, service string }

// defaultFlow is the two-task bench_flow: 5 requests per saga.
func defaultFlow() flowSpec {
	return flowSpec{workflow: flowName, payload: `{"bench":1}`, steps: []flowStep{
		{"publish", "bench_t0", ""}, {"consume", "bench_t0", "bench_b"},
		{"publish", "bench_t1", ""}, {"consume", "bench_t1", "bench_c"},
	}}
}

// saga runs one full flow and reports each request. It returns true if
// every request succeeded.
func (l *loader) saga(spec flowSpec, record func(sample)) bool {
	body, d, ok := l.call(http.MethodPost, "/start_instance?workflow_name="+spec.workflow, "")
	record(sample{"start", d, ok})
	if !ok {
		return false
	}
	var resp struct {
		ID string `json:"workflow_instance_id"`
	}
	if json.Unmarshal([]byte(body), &resp) != nil || resp.ID == "" {
		return false
	}
	for _, s := range spec.steps {
		path := "/update_instance?workflow_instance_id=" + resp.ID + "&action_type=" + s.action +
			"&event_name=" + s.topic + "&is_retry=false"
		payload := ""
		if s.service != "" {
			path += "&service_name=" + s.service
		} else {
			payload = spec.payload
		}
		_, d, ok := l.call(http.MethodPost, path, payload)
		record(sample{s.action, d, ok})
		if !ok {
			return false
		}
	}
	return true
}

// runRate starts sagas at `rate` per second for `duration`, waits for them
// to drain, and measures. rdb/db may be nil (warm-up) to skip the Redis and
// archive accounting.
func (l *loader) runRate(ctx context.Context, spec flowSpec, rate float64, duration time.Duration, rdb *redis.Client, db *pgxpool.Pool) RateResult {
	var (
		mu        sync.Mutex
		samples   = map[string][]time.Duration{}
		requests  int
		errs      int
		completed int
		started   int
		wg        sync.WaitGroup
	)
	record := func(s sample) {
		mu.Lock()
		requests++
		if s.ok {
			samples[s.endpoint] = append(samples[s.endpoint], s.dur)
		} else {
			errs++
		}
		mu.Unlock()
	}

	cmdsBefore := redisCommandCalls(ctx, rdb)
	rowsBefore := archiveRows(ctx, db)

	begin := time.Now()
	ticker := time.NewTicker(time.Duration(float64(time.Second) / rate))
	for time.Since(begin) < duration {
		<-ticker.C
		started++
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.saga(spec, record) {
				mu.Lock()
				completed++
				mu.Unlock()
			}
		}()
	}
	ticker.Stop()
	wg.Wait()
	elapsed := time.Since(begin)

	rr := RateResult{
		TargetSagasPerSec: rate, Duration: duration.String(), SagasStarted: started, SagasCompleted: completed,
		AchievedSagasPS: float64(completed) / elapsed.Seconds(), Requests: requests, Errors: errs,
		Endpoints: map[string]LatencyResult{},
	}
	if requests > 0 {
		rr.ErrorRate = float64(errs) / float64(requests)
	}
	for ep, s := range samples {
		rr.Endpoints[ep] = latencyStats(s)
	}
	if rdb != nil && started > 0 {
		rr.RedisCmdsPerSaga = float64(redisCommandCalls(ctx, rdb)-cmdsBefore) / float64(started)
	}
	if db != nil {
		// Archiving is asynchronous; wait until the row count stops moving.
		rr.ArchiveExpected = completed
		last := -1
		for i := 0; i < 100; i++ {
			n := archiveRows(ctx, db) - rowsBefore
			if n == last && n >= completed {
				break
			}
			if n == last && i > 20 { // stable for 2s and still short: rows are gone
				break
			}
			last = n
			time.Sleep(100 * time.Millisecond)
		}
		rr.ArchiveRows = archiveRows(ctx, db) - rowsBefore
		rr.ArchiveMissing = completed - rr.ArchiveRows
		if rr.ArchiveMissing < 0 {
			rr.ArchiveMissing = 0
		}
	}
	return rr
}

// measureReaperLag publishes n tasks with a 2s timeout and never consumes
// them, then measures deadline -> webhook arrival.
//
// The instances are created first and only then published, concurrently, so
// that all n deadlines really do fall due together -- which is the thing
// this measurement is named after. Publishing them one at a time in a single
// loop spread the deadlines across the whole publish phase: at n=500 the
// first task's deadline expired while the loop was still publishing the
// last, so the reaper fired those before the client-side stamp was reached
// and the reported lag went negative. The number was then a measure of the
// harness's own publish rate, not of the reaper.
//
// Each task is also stamped individually, from the return of its own publish
// call, rather than from one reading shared by the whole burst: at n=2000
// even a concurrent burst outlasts the 2 s timeout, so a shared stamp taken
// after the last publish overshoots the earliest deadlines and the lag goes
// negative again. A per-task stamp is at most one request latency later than
// the deadline the server actually armed, in either direction.
func (l *loader) measureReaperLag(ctx context.Context, hooks *webhookReceiver, n int, wait time.Duration) LagResult {
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		body, _, ok := l.call(http.MethodPost, "/start_instance?workflow_name="+timeoutName, "")
		if !ok {
			continue
		}
		var resp struct {
			ID string `json:"workflow_instance_id"`
		}
		if json.Unmarshal([]byte(body), &resp) != nil || resp.ID == "" {
			continue
		}
		ids = append(ids, resp.ID)
	}

	var (
		mu        sync.Mutex
		wg        sync.WaitGroup
		deadlines = make(map[string]time.Time, len(ids))
		sem       = make(chan struct{}, lagPublishParallel)
	)
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			payload := fmt.Sprintf(`{"bench_id":%q}`, id)
			_, _, ok := l.call(http.MethodPost, "/update_instance?workflow_instance_id="+id+
				"&action_type=publish&event_name=bench_tt&is_retry=false", payload)
			// The server armed this task's deadline from its own clock during
			// the call, so reading the clock right after it returns is within
			// one request latency of the real deadline.
			if ok {
				at := time.Now().Add(lagTimeout * time.Millisecond)
				mu.Lock()
				deadlines[id] = at
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()

	res := LagResult{Tasks: len(deadlines)}
	until := time.Now().Add(lagTimeout*time.Millisecond + wait)
	var lags []time.Duration
	for time.Now().Before(until) {
		lags = lags[:0]
		for id, dl := range deadlines {
			if at, ok := hooks.arrival(id); ok {
				lags = append(lags, at.Sub(dl))
			}
		}
		if len(lags) == len(deadlines) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	res.Received = len(lags)
	res.Missing = res.Tasks - res.Received
	if len(lags) > 0 {
		st := latencyStats(lags)
		res.P50ms, res.P99ms, res.MaxMs = st.P50ms, st.P99ms, st.MaxMs
		min := lags[0]
		for _, d := range lags {
			if d < min {
				min = d
			}
		}
		res.MinMs = float64(min) / 1e6
	}
	return res
}

// redisCommandCalls sums total_calls across INFO commandstats.
func redisCommandCalls(ctx context.Context, rdb *redis.Client) int64 {
	if rdb == nil {
		return 0
	}
	info, err := rdb.Info(ctx, "commandstats").Result()
	if err != nil {
		return 0
	}
	var total int64
	for _, line := range strings.Split(info, "\n") {
		if !strings.HasPrefix(line, "cmdstat_") {
			continue
		}
		for _, kv := range strings.Split(strings.SplitN(line, ":", 2)[1], ",") {
			if strings.HasPrefix(kv, "calls=") {
				var n int64
				_, _ = fmt.Sscanf(kv, "calls=%d", &n)
				total += n
			}
		}
	}
	return total
}

func archiveRows(ctx context.Context, db *pgxpool.Pool) int {
	if db == nil {
		return 0
	}
	var n int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM instance_history WHERE name LIKE 'bench_%'`).Scan(&n)
	return n
}
