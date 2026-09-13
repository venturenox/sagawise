//go:build integration

package instance_engine

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"wtfsaga/internal/testx"

	promclient "github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Phase 9: operations. Metrics, structured logs with the instance id, and
// probes that reflect the real state (docs/operations.md).

// withMetrics gives the engine real instruments read by a ManualReader.
func withMetrics(t testx.T, e *env) *metric.ManualReader {
	t.Helper()
	reader := metric.NewManualReader()
	if err := e.eng.UseMeterProvider(metric.NewMeterProvider(metric.WithReader(reader))); err != nil {
		t.Fatal(err)
	}
	return reader
}

// collect returns every metric by name.
func collect(t testx.T, reader *metric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

// sum adds the data points of a counter or gauge whose attributes contain
// every key=value in want.
func sum(m metricdata.Metrics, want map[string]string) (total int64, found bool) {
	check := func(kvs []string) bool {
		got := map[string]string{}
		for i := 0; i+1 < len(kvs); i += 2 {
			got[kvs[i]] = kvs[i+1]
		}
		for k, v := range want {
			if got[k] != v {
				return false
			}
		}
		return true
	}
	switch d := m.Data.(type) {
	case metricdata.Sum[int64]:
		for _, p := range d.DataPoints {
			var kvs []string
			for _, kv := range p.Attributes.ToSlice() {
				kvs = append(kvs, string(kv.Key), kv.Value.AsString())
			}
			if check(kvs) {
				total += p.Value
				found = true
			}
		}
	case metricdata.Gauge[int64]:
		for _, p := range d.DataPoints {
			var kvs []string
			for _, kv := range p.Attributes.ToSlice() {
				kvs = append(kvs, string(kv.Key), kv.Value.AsString())
			}
			if check(kvs) {
				total += p.Value
				found = true
			}
		}
	}
	return total, found
}

func expectSum(t testx.T, ms map[string]metricdata.Metrics, name string, attrs map[string]string, want int64) {
	t.Helper()
	m, ok := ms[name]
	if !ok {
		t.Errorf("metric %s not recorded", name)
		return
	}
	got, found := sum(m, attrs)
	if !found && want != 0 {
		t.Errorf("%s%v: no data point", name, attrs)
		return
	}
	if got != want {
		t.Errorf("%s%v = %d, want %d", name, attrs, got, want)
	}
}

// A saga that times out is visible end to end in the metrics: the report
// counters, the deadline gauge, the reaper counters and lag, the queue jobs.
func TestOps_MetricsFollowASaga(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		reader := withMetrics(t, e)

		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")
		e.publish(id, "it_order_created") // TASK_ALREADY_PUBLISHED, counted as a refusal

		ms := collect(t, reader)
		expectSum(t, ms, "sagawise.instances.started", nil, 1)
		expectSum(t, ms, "sagawise.reports", map[string]string{"action": "publish", "result": "OK"}, 1)
		expectSum(t, ms, "sagawise.reports", map[string]string{"action": "publish", "result": "TASK_ALREADY_PUBLISHED"}, 1)
		if n, _ := sum(ms["sagawise.deadlines.pending"], nil); n < 1 {
			t.Errorf("deadlines.pending = %d with one PUBLISHED task", n)
		}
		expectSum(t, ms, "sagawise.store.up", map[string]string{"store": "redis"}, 1)
		expectSum(t, ms, "sagawise.store.up", map[string]string{"store": "postgres"}, 1)

		// 20 s timeout, checked 25 s later: 5 s of lag on the record.
		e.clock.Advance(25 * time.Second)
		e.tick()
		if got := e.taskState(id, "0"); got != "FAILED" {
			t.Fatalf("task state = %q after the deadline, want FAILED", got)
		}

		ms = collect(t, reader)
		expectSum(t, ms, "sagawise.reaper.ticks", map[string]string{"result": "ok"}, 1)
		expectSum(t, ms, "sagawise.tasks.timed_out", nil, 1)
		expectSum(t, ms, "sagawise.instances.terminal", map[string]string{"state": "FAILED"}, 1)
		expectSum(t, ms, "sagawise.queue.jobs", map[string]string{"queue": "webhook", "result": "done"}, 1)
		expectSum(t, ms, "sagawise.queue.jobs", map[string]string{"queue": "archive", "result": "done"}, 1)
		lag, ok := ms["sagawise.reaper.lag"].Data.(metricdata.Histogram[float64])
		if !ok || len(lag.DataPoints) != 1 {
			t.Fatalf("reaper.lag = %#v, want one histogram point", ms["sagawise.reaper.lag"].Data)
		}
		if p := lag.DataPoints[0]; p.Count != 1 || p.Sum < 4.9 || p.Sum > 5.1 {
			t.Errorf("reaper.lag count=%d sum=%v, want 1 point of ~5 s", p.Count, p.Sum)
		}
		if got, _ := sum(ms["sagawise.reaper.last_tick"], nil); got != e.clock.Now().Unix() {
			t.Errorf("reaper.last_tick = %d, want the tick's time %d", got, e.clock.Now().Unix())
		}
	})
}

// Archive failures are counted while Postgres is down, and stop when it
// returns; the queue gauge shows the backlog in between.
func TestOps_ArchiveFailureMetrics(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		reader := withMetrics(t, e)
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")
		e.postgresDown()
		e.mustOK(e.fail(id, "it_order_created", "it_payments"), "fail during outage")

		ms := collect(t, reader)
		expectSum(t, ms, "sagawise.queue.jobs", map[string]string{"queue": "archive", "result": "failed"}, 1)
		expectSum(t, ms, "sagawise.queue.pending", map[string]string{"queue": "archive"}, 1)
		expectSum(t, ms, "sagawise.store.up", map[string]string{"store": "postgres"}, 0)

		e.postgresUp()
		e.clock.Advance(2 * time.Second)
		e.drain()
		ms = collect(t, reader)
		expectSum(t, ms, "sagawise.queue.jobs", map[string]string{"queue": "archive", "result": "done"}, 1)
		expectSum(t, ms, "sagawise.queue.pending", map[string]string{"queue": "archive"}, 0)
		if got := e.archived(id); got != "FAILED" {
			t.Errorf("archived = %q, want FAILED", got)
		}
	})
}

// Readiness: ok with both stores; degraded (still 200) with Postgres down;
// unavailable with Redis down. Liveness ignores the stores.
func TestOps_ReadinessReflectsStores(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		e.eng.reaperBeat.Store(e.clock.Now().UnixNano())
		e.eng.Archiver.beat()
		e.eng.Webhooks.beat()

		rep := e.eng.Readiness(e.ctx)
		if rep.Status != StatusOK || rep.Checks["redis"] != CheckOK || rep.Checks["postgres"] != CheckOK {
			t.Errorf("healthy stack: %+v", rep)
		}

		e.postgresDown()
		rep = e.eng.Readiness(e.ctx)
		if rep.Status != StatusDegraded || !rep.Healthy() || rep.Checks["postgres"] != CheckError || rep.Checks["redis"] != CheckOK {
			t.Errorf("postgres down: %+v (want degraded, still healthy)", rep)
		}
		e.postgresUp()

		e.faults.FailNext("ping", 1)
		rep = e.eng.Readiness(e.ctx)
		if rep.Status != StatusUnavailable || rep.Healthy() || rep.Checks["redis"] != CheckError {
			t.Errorf("redis down: %+v (want unavailable)", rep)
		}
		if live := e.eng.Liveness(); live.Status != StatusOK {
			t.Errorf("liveness with a store fault: %+v (must not depend on stores)", live)
		}
	})
}

// Every line the engine logs about an instance carries instance_id: the
// report, the timeout, the webhook, the archive.
func TestOps_LogsCarryInstanceID(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		var buf bytes.Buffer
		e.eng.Log = slog.New(slog.NewJSONHandler(&buf, nil))

		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")
		e.clock.Advance(25 * time.Second)
		e.tick()

		want := map[string]bool{"task timed out": false, "webhook delivered": false, "instance archived": false, "reaper tick": false}
		for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			var line map[string]any
			if err := json.Unmarshal([]byte(raw), &line); err != nil {
				t.Fatalf("log line %q is not JSON: %v", raw, err)
			}
			msg, _ := line["msg"].(string)
			if _, tracked := want[msg]; tracked {
				want[msg] = true
			}
			if msg == "reaper tick" {
				continue // the per-tick summary has no single instance
			}
			if line["instance_id"] != id {
				t.Errorf("line %q has instance_id %v, want %q", msg, line["instance_id"], id)
			}
			if msg == "task timed out" {
				if line["task_index"] != float64(0) {
					t.Errorf("task timed out: task_index = %v", line["task_index"])
				}
				if lag, _ := line["lag_ms"].(float64); lag < 4900 || lag > 5100 {
					t.Errorf("task timed out: lag_ms = %v, want ~5000", line["lag_ms"])
				}
			}
		}
		for msg, seen := range want {
			if !seen {
				t.Errorf("no %q line logged:\n%s", msg, buf.String())
			}
		}
	})
}

// The stack the tests run against reports its persistence setting.
func TestOps_RedisAppendOnlyIsKnown(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		_, known, err := RedisAppendOnly(e.ctx, e.eng.RDB)
		if err != nil {
			t.Fatal(err)
		}
		if !known {
			t.Error("CONFIG GET appendonly refused by the test Redis; the startup check cannot be verified here")
		}
	})
}

// deploy/alerts.yml and the chart's PrometheusRule alert only on series this
// binary exports, so a renamed metric breaks the build rather than silently
// disarming an alert.
func TestOps_AlertRulesReferenceExportedMetrics(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		reg := promclient.NewRegistry()
		exporter, err := prometheus.New(prometheus.WithRegisterer(reg))
		if err != nil {
			t.Fatal(err)
		}
		if err := e.eng.UseMeterProvider(metric.NewMeterProvider(metric.WithReader(exporter))); err != nil {
			t.Fatal(err)
		}
		// Touch every counter and histogram once so it has a series: one
		// instance fails during a Postgres outage (archive failed, then
		// done), another times out (reaper counters and lag).
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")
		e.postgresDown()
		e.mustOK(e.fail(id, "it_order_created", "it_payments"), "fail")
		e.postgresUp()
		late := e.start()
		e.mustOK(e.publish(late, "it_order_created"), "publish")
		e.clock.Advance(25 * time.Second)
		e.tick()
		if got := e.taskState(late, "0"); got != "FAILED" {
			t.Fatalf("task state = %q after the deadline, want FAILED", got)
		}

		families, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		exported := map[string]bool{}
		for _, f := range families {
			exported[f.GetName()] = true
			// Prometheus exposes histograms and summaries as _bucket/_sum/_count.
			exported[f.GetName()+"_bucket"] = true
			exported[f.GetName()+"_sum"] = true
			exported[f.GetName()+"_count"] = true
		}
		if !exported["sagawise_reaper_lag_seconds_bucket"] || !exported["sagawise_queue_jobs_total"] {
			t.Fatalf("unexpected export names: %v", keys(exported))
		}

		names := regexp.MustCompile(`sagawise_[a-z0-9_]+`)
		for _, file := range []string{
			filepath.Join("..", "..", "deploy", "alerts.yml"),
			filepath.Join("..", "..", "charts", "sagawise", "templates", "prometheusrule.yaml"),
		} {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			for _, n := range names.FindAllString(string(data), -1) {
				if !exported[n] {
					t.Errorf("%s refers to %s, which the binary does not export", file, n)
				}
			}
		}
	})
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		if strings.HasPrefix(k, "sagawise_") {
			out = append(out, k)
		}
	}
	return out
}
