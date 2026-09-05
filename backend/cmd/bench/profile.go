package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"wtfsaga/db_connect"
	"wtfsaga/utils"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Profile mode: instead of holding fixed rates, push the system to its knee,
// profile it there, and walk the scaling axes that the design is sensitive
// to. The output is a bottleneck report, not a regression number.

type profileConfig struct {
	label        string
	out          string
	rampStart    float64
	rampMax      float64
	rampHold     time.Duration
	instances    string
	payloads     string
	timeouts     string
	pprofSeconds int
}

var profileTaskCounts = []int{2, 10, 50}

func chainName(n int) string { return fmt.Sprintf("bench_chain%d", n) }

// chainWorkflow is an n-task saga where every task is published by bench_a
// and consumed by bench_b under a distinct topic.
func chainWorkflow(n int) utils.Workflow {
	wf := utils.Workflow{Name: chainName(n), Version: "1.0", Schema_version: "1.0"}
	for i := 0; i < n; i++ {
		wf.Tasks = append(wf.Tasks, utils.Task{Topic: fmt.Sprintf("bench_c%d_t%d", n, i), From: "bench_a", To: "bench_b", Timeout: 60000})
	}
	return wf
}

func chainFlow(n int) flowSpec {
	spec := flowSpec{workflow: chainName(n), payload: `{"bench":1}`}
	for i := 0; i < n; i++ {
		topic := fmt.Sprintf("bench_c%d_t%d", n, i)
		spec.steps = append(spec.steps, flowStep{"publish", topic, ""}, flowStep{"consume", topic, "bench_b"})
	}
	return spec
}

// ---- results ----

type Profile struct {
	Label  string            `json:"label"`
	Date   string            `json:"date"`
	Commit string            `json:"commit"`
	Dirty  bool              `json:"dirty"`
	Env    map[string]string `json:"env"`
	Config map[string]string `json:"config"`

	Ramp       []RampStep `json:"ramp"`
	Knee       float64    `json:"knee_sagas_per_sec"`
	KneeReason string     `json:"knee_reason"`

	Pprof map[string]string `json:"pprof_top"` // profile name -> `go tool pprof -top` text

	Commands []CommandRow `json:"commands"`

	Instances        []ScaleRow       `json:"instances_in_redis"`
	TasksPerWorkflow []ScaleRow       `json:"tasks_per_workflow"`
	PayloadSize      []ScaleRow       `json:"payload_size"`
	Timeouts         []LagResult      `json:"simultaneous_timeouts"`
	Contention       ContentionResult `json:"contention"`

	Findings []string `json:"findings"`
}

type RampStep struct {
	Target      float64       `json:"target_sagas_per_sec"`
	Achieved    float64       `json:"achieved_sagas_per_sec"`
	RequestsPS  float64       `json:"requests_per_sec"`
	Requests    int           `json:"requests"`
	Errors      int           `json:"errors"`
	ErrorRate   float64       `json:"error_rate"`
	Start       LatencyResult `json:"start"`
	Publish     LatencyResult `json:"publish"`
	Consume     LatencyResult `json:"consume"`
	RedisCPUPct float64       `json:"redis_cpu_pct"`
	PingP50ms   float64       `json:"redis_ping_p50_ms"`
	PingP99ms   float64       `json:"redis_ping_p99_ms"`
	Pass        bool          `json:"pass"`
}

type CommandRow struct {
	Endpoint        string  `json:"endpoint"`
	Command         string  `json:"command"`
	CallsPerRequest float64 `json:"calls_per_request"`
	UsecPerCall     float64 `json:"usec_per_call"`
}

type ScaleRow struct {
	Level     string                   `json:"level"`
	Endpoints map[string]LatencyResult `json:"endpoints"`
	Extra     map[string]float64       `json:"extra"`
}

type ContentionResult struct {
	Rounds int           `json:"rounds"`
	Same   LatencyResult `json:"same_instance"`
	Spread LatencyResult `json:"separate_instances"`
}

// ---- SLO used to define the knee ----

const (
	sloErrorRate = 0.01
	sloP99ms     = 50.0
	sloAchieved  = 0.9
)

func (r RampStep) sloOK() (bool, string) {
	switch {
	case r.ErrorRate > sloErrorRate:
		return false, fmt.Sprintf("error rate %.2f%% > %.0f%%", r.ErrorRate*100, sloErrorRate*100)
	case r.Consume.P99ms > sloP99ms || r.Publish.P99ms > sloP99ms:
		return false, fmt.Sprintf("p99 %.0f ms > %.0f ms", maxf(r.Consume.P99ms, r.Publish.P99ms), sloP99ms)
	case r.Achieved < sloAchieved*r.Target:
		return false, fmt.Sprintf("achieved %.0f < %.0f%% of target %.0f (load generator or server saturated)", r.Achieved, sloAchieved*100, r.Target)
	}
	return true, ""
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// ---- run ----

func runProfile(cfg profileConfig) (string, error) {
	setDefault("REDIS_HOST", "localhost")
	setDefault("REDIS_PORT", "6379")
	setDefault("POSTGRES_HOST", "localhost")
	setDefault("POSTGRES_PORT", "5432")
	setDefault("POSTGRES_USERNAME", "postgres")
	setDefault("POSTGRES_PASSWORD", "venturenox")
	setDefault("POSTGRES_DATABASE", "sagawise")
	_ = os.Setenv("REDIS_CONNECTION_STRING", "")

	ctx := context.Background()
	rdb, err := db_connect.DBConnect(ctx)
	if err != nil {
		return "", fmt.Errorf("redis: %w", err)
	}
	db, err := db_connect.ConnectPostgres(ctx)
	if err != nil {
		return "", fmt.Errorf("postgres: %w", err)
	}
	defer db.Close()
	defer func() { _ = rdb.Close() }()

	base := &Results{Env: map[string]string{}}
	captureEnv(ctx, base, rdb, db)
	p := &Profile{Label: "profile-" + cfg.label, Date: time.Now().Format(time.RFC3339), Commit: base.Commit, Dirty: base.Dirty, Env: base.Env,
		Pprof: map[string]string{}, Config: map[string]string{
			"ramp_start": fmt.Sprint(cfg.rampStart), "ramp_max": fmt.Sprint(cfg.rampMax), "ramp_hold": cfg.rampHold.String(),
			"instances": cfg.instances, "payloads": cfg.payloads, "timeouts": cfg.timeouts, "pprof_seconds": strconv.Itoa(cfg.pprofSeconds),
		}}

	runDir := filepath.Join(cfg.out, fmt.Sprintf("%s_%s_%s", time.Now().Format("2006-01-02_1504"), p.Commit, p.Label))
	if err := os.MkdirAll(filepath.Join(runDir, "pprof"), 0o750); err != nil {
		return "", err
	}
	fmt.Fprintln(os.Stderr, "run dir:", runDir)

	work, err := os.MkdirTemp("", "sagawise-profile")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(work) }()
	bin := filepath.Join(work, "sagawise")
	fmt.Fprintln(os.Stderr, "building server...")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil { // #nosec G204 -- fixed argv; bin is a temp path we created
		return "", fmt.Errorf("go build: %v\n%s", err, out)
	}

	hooks := newWebhookReceiver()
	defer hooks.Close()
	dslDir := filepath.Join(work, "dsl")
	if err := os.MkdirAll(dslDir, 0o750); err != nil {
		return "", err
	}
	workflows := benchWorkflows()
	for _, n := range profileTaskCounts {
		workflows = append(workflows, chainWorkflow(n))
	}
	for _, wf := range workflows {
		data, _ := json.Marshal(utils.WorkflowData{Workflow: wf})
		if err := os.WriteFile(filepath.Join(dslDir, wf.Name+".json"), data, 0o600); err != nil {
			return "", err
		}
	}
	services, _ := json.Marshal([]utils.Service{
		{ServiceName: "bench_a", FailureUrl: hooks.URL()}, {ServiceName: "bench_b", FailureUrl: hooks.URL()}, {ServiceName: "bench_t", FailureUrl: hooks.URL()},
	})
	servicesFile := filepath.Join(work, "services.json")
	if err := os.WriteFile(servicesFile, services, 0o600); err != nil {
		return "", err
	}

	pprofAddr := freeAddr()
	_ = os.Setenv("SAGAWISE_PPROF_ADDR", pprofAddr)
	srv, err := launchServer(bin, dslDir, servicesFile)
	if err != nil {
		return "", err
	}
	defer srv.stop()
	defer cleanupBenchData(ctx, rdb, db)
	fmt.Fprintln(os.Stderr, "server up at", srv.addr, "pprof at", pprofAddr)

	l := &loader{base: "http://" + srv.addr, client: &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{MaxIdleConns: 4000, MaxIdleConnsPerHost: 4000, IdleConnTimeout: 90 * time.Second},
	}}
	l.runRate(ctx, defaultFlow(), 50, 3*time.Second, nil, nil)
	cleanupBenchData(ctx, rdb, db)

	// 1. Saturation ramp.
	fmt.Fprintln(os.Stderr, "1/7 saturation ramp...")
	p.Knee, p.KneeReason = 0, "never breached SLO within ramp_max"
	for rate := cfg.rampStart; rate <= cfg.rampMax; rate = float64(int(rate * 1.5)) {
		step := l.rampStep(ctx, rdb, rate, cfg.rampHold)
		ok, why := step.sloOK()
		step.Pass = ok
		p.Ramp = append(p.Ramp, step)
		fmt.Fprintf(os.Stderr, "   %6.0f sagas/s: achieved %6.0f, %5.0f req/s, err %.2f%%, consume p99 %6.1f ms, redis cpu %3.0f%%, ping p99 %.2f ms %s\n",
			rate, step.Achieved, step.RequestsPS, step.ErrorRate*100, step.Consume.P99ms, step.RedisCPUPct, step.PingP99ms, map[bool]string{true: "ok", false: "BREACH: " + why}[ok])
		cleanupBenchData(ctx, rdb, db)
		if !ok {
			p.KneeReason = why
			break
		}
		p.Knee = rate
	}
	if p.Knee == 0 && len(p.Ramp) > 0 {
		p.Knee = cfg.rampStart / 2
	}

	// 2. Profiles at the knee.
	fmt.Fprintf(os.Stderr, "2/7 pprof at %.0f sagas/s for %ds...\n", p.Knee, cfg.pprofSeconds)
	p.capturePprof(ctx, l, pprofAddr, bin, runDir, cfg.pprofSeconds)
	cleanupBenchData(ctx, rdb, db)

	// 3. Redis command breakdown per endpoint.
	fmt.Fprintln(os.Stderr, "3/7 redis command breakdown...")
	p.Commands = l.commandBreakdown(ctx, rdb)
	cleanupBenchData(ctx, rdb, db)

	// 4. Instances already in Redis.
	fmt.Fprintln(os.Stderr, "4/7 instances in redis...")
	populated := 0
	memBefore := redisUsedMemory(ctx, rdb)
	for _, lvl := range append([]string{"0"}, strings.Split(cfg.instances, ",")...) {
		n, _ := strconv.Atoi(strings.TrimSpace(lvl))
		if n > populated {
			populateInstances(ctx, rdb, populated, n)
			populated = n
		}
		row := ScaleRow{Level: lvl, Extra: map[string]float64{}}
		rr := l.runRate(ctx, defaultFlow(), 100, 8*time.Second, nil, nil)
		row.Endpoints = rr.Endpoints
		row.Extra["list_p50_ms"], row.Extra["list_p99_ms"] = l.endpointLatency("/workflow_instances/list?workflow_name="+flowName, 20)
		row.Extra["get_p50_ms"], _ = l.endpointLatency("/workflow_instances/get?workflow_instance_id=bp0", 20)
		if n > 0 {
			row.Extra["redis_bytes_per_instance"] = float64(redisUsedMemory(ctx, rdb)-memBefore) / float64(n)
		}
		p.Instances = append(p.Instances, row)
		fmt.Fprintf(os.Stderr, "   %7s instances: consume p50 %.1f ms p99 %.1f ms, list p50 %.1f ms\n", lvl, row.Endpoints["consume"].P50ms, row.Endpoints["consume"].P99ms, row.Extra["list_p50_ms"])
		cleanupFlowInstances(ctx, rdb, db)
	}
	cleanupBenchData(ctx, rdb, db)

	// 5. Tasks per workflow, at a constant ~1000 requests/s.
	fmt.Fprintln(os.Stderr, "5/7 tasks per workflow...")
	for _, n := range profileTaskCounts {
		reqPerSaga := float64(2*n + 1)
		rr := l.runRate(ctx, chainFlow(n), 1000/reqPerSaga, 8*time.Second, nil, nil)
		row := ScaleRow{Level: strconv.Itoa(n), Endpoints: rr.Endpoints, Extra: map[string]float64{"sagas_per_sec": rr.AchievedSagasPS, "error_rate": rr.ErrorRate}}
		p.TasksPerWorkflow = append(p.TasksPerWorkflow, row)
		fmt.Fprintf(os.Stderr, "   %3d tasks: publish p50 %.1f ms, consume p50 %.1f ms, p99 %.1f ms, err %.2f%%\n", n, row.Endpoints["publish"].P50ms, row.Endpoints["consume"].P50ms, row.Endpoints["consume"].P99ms, rr.ErrorRate*100)
		cleanupBenchData(ctx, rdb, db)
	}

	// 6. Payload size.
	fmt.Fprintln(os.Stderr, "6/7 payload size...")
	for _, sz := range strings.Split(cfg.payloads, ",") {
		bytes, _ := strconv.Atoi(strings.TrimSpace(sz))
		spec := defaultFlow()
		spec.payload = `{"bench":1,"blob":"` + strings.Repeat("x", bytes) + `"}`
		rr := l.runRate(ctx, spec, 50, 8*time.Second, nil, nil)
		row := ScaleRow{Level: sz + "B", Endpoints: rr.Endpoints, Extra: map[string]float64{"error_rate": rr.ErrorRate}}
		p.PayloadSize = append(p.PayloadSize, row)
		fmt.Fprintf(os.Stderr, "   %8s: publish p50 %.1f ms, consume p50 %.1f ms, p99 %.1f ms, err %.2f%%\n", sz+"B", row.Endpoints["publish"].P50ms, row.Endpoints["consume"].P50ms, row.Endpoints["consume"].P99ms, rr.ErrorRate*100)
		cleanupBenchData(ctx, rdb, db)
	}

	// 7. Simultaneous timeouts.
	fmt.Fprintln(os.Stderr, "7/7 simultaneous timeouts...")
	for _, t := range strings.Split(cfg.timeouts, ",") {
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		lag := l.measureReaperLag(ctx, hooks, n, time.Duration(n)*15*time.Millisecond+10*time.Second)
		p.Timeouts = append(p.Timeouts, lag)
		fmt.Fprintf(os.Stderr, "   %5d timeouts: received %d, lag p50 %.0f ms, p99 %.0f ms, max %.0f ms\n", n, lag.Received, lag.P50ms, lag.P99ms, lag.MaxMs)
		cleanupBenchData(ctx, rdb, db)
	}

	// Contention: same instance vs separate instances.
	fmt.Fprintln(os.Stderr, "contention...")
	p.Contention = l.contention(ctx, 20, 10)
	fmt.Fprintf(os.Stderr, "   same instance p50 %.1f ms / p99 %.1f ms; separate p50 %.1f ms / p99 %.1f ms\n",
		p.Contention.Same.P50ms, p.Contention.Same.P99ms, p.Contention.Spread.P50ms, p.Contention.Spread.P99ms)
	cleanupBenchData(ctx, rdb, db)
	srv.stop()

	p.Findings = findings(p)

	data, _ := json.MarshalIndent(p, "", "  ")
	if err := os.WriteFile(filepath.Join(runDir, "profile.json"), data, 0o600); err != nil {
		return "", err
	}
	envRes := &Results{Label: p.Label, Date: p.Date, Commit: p.Commit, Dirty: p.Dirty, Env: p.Env, Config: p.Config}
	if err := os.WriteFile(filepath.Join(runDir, "env.txt"), []byte(envText(envRes)), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(runDir, "report.md"), []byte(renderProfile(p)), 0o600); err != nil {
		return "", err
	}
	return runDir, nil
}

func freeAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

// ---- ramp ----

func (l *loader) rampStep(ctx context.Context, rdb *redis.Client, rate float64, hold time.Duration) RampStep {
	cpuBefore := redisCPUSeconds(ctx, rdb)
	stopPing := make(chan struct{})
	var pings []time.Duration
	var pingWG sync.WaitGroup
	pingWG.Add(1)
	go func() {
		defer pingWG.Done()
		t := time.NewTicker(10 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stopPing:
				return
			case <-t.C:
				s := time.Now()
				_ = rdb.Ping(ctx).Err()
				pings = append(pings, time.Since(s))
			}
		}
	}()
	begin := time.Now()
	rr := l.runRate(ctx, defaultFlow(), rate, hold, nil, nil)
	elapsed := time.Since(begin)
	close(stopPing)
	pingWG.Wait()
	ping := latencyStats(pings)
	return RampStep{
		Target: rate, Achieved: rr.AchievedSagasPS, RequestsPS: float64(rr.Requests) / elapsed.Seconds(),
		Requests: rr.Requests, Errors: rr.Errors, ErrorRate: rr.ErrorRate,
		Start: rr.Endpoints["start"], Publish: rr.Endpoints["publish"], Consume: rr.Endpoints["consume"],
		RedisCPUPct: (redisCPUSeconds(ctx, rdb) - cpuBefore) / elapsed.Seconds() * 100,
		PingP50ms:   ping.P50ms, PingP99ms: ping.P99ms,
	}
}

// ---- pprof ----

func (p *Profile) capturePprof(ctx context.Context, l *loader, pprofAddr, bin, runDir string, seconds int) {
	done := make(chan struct{})
	go func() {
		l.runRate(ctx, defaultFlow(), p.Knee, time.Duration(seconds+6)*time.Second, nil, nil)
		close(done)
	}()
	time.Sleep(2 * time.Second) // let load settle before sampling
	fetch := func(name, path string) {
		resp, err := http.Get("http://" + pprofAddr + path) // #nosec G107 -- loopback address we chose
		if err != nil {
			p.Pprof[name] = "fetch failed: " + err.Error()
			return
		}
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		file := filepath.Join(runDir, "pprof", name+".pprof")
		if err := os.WriteFile(file, data, 0o600); err != nil {
			p.Pprof[name] = "write failed: " + err.Error()
			return
		}
		out, err := exec.Command("go", "tool", "pprof", "-top", "-nodecount=25", bin, file).CombinedOutput() // #nosec G204 -- fixed argv, paths we created
		if err != nil {
			p.Pprof[name] = fmt.Sprintf("pprof failed: %v\n%s", err, out)
			return
		}
		p.Pprof[name] = string(out)
		if name == "cpu" {
			// Cumulative view: which engine functions the time flows through.
			out, err := exec.Command("go", "tool", "pprof", "-top", "-cum", "-nodecount=40", bin, file).CombinedOutput() // #nosec G204 -- fixed argv, paths we created
			if err == nil {
				p.Pprof["cpu-cumulative"] = string(out)
			}
		}
	}
	fetch("cpu", fmt.Sprintf("/debug/pprof/profile?seconds=%d", seconds))
	fetch("heap", "/debug/pprof/heap")
	fetch("block", "/debug/pprof/block")
	fetch("mutex", "/debug/pprof/mutex")
	fetch("goroutine", "/debug/pprof/goroutine")
	<-done
}

// ---- redis command breakdown ----

type cmdStat struct{ calls, usec int64 }

func commandStats(ctx context.Context, rdb *redis.Client) map[string]cmdStat {
	out := map[string]cmdStat{}
	info, err := rdb.Info(ctx, "commandstats").Result()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "cmdstat_") {
			continue
		}
		name, rest, _ := strings.Cut(strings.TrimPrefix(line, "cmdstat_"), ":")
		var st cmdStat
		for _, kv := range strings.Split(rest, ",") {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "calls":
				st.calls, _ = strconv.ParseInt(v, 10, 64)
			case "usec":
				st.usec, _ = strconv.ParseInt(v, 10, 64)
			}
		}
		out[name] = st
	}
	return out
}

// commandBreakdown runs N requests of each endpoint in isolation and
// attributes the Redis commandstats delta to that endpoint.
func (l *loader) commandBreakdown(ctx context.Context, rdb *redis.Client) []CommandRow {
	const n = 200
	var rows []CommandRow
	phase := func(endpoint string, do func()) {
		before := commandStats(ctx, rdb)
		do()
		after := commandStats(ctx, rdb)
		for cmd, a := range after {
			b := before[cmd]
			calls := a.calls - b.calls
			if calls <= 0 || cmd == "info" || cmd == "ping" {
				continue
			}
			rows = append(rows, CommandRow{Endpoint: endpoint, Command: cmd, CallsPerRequest: float64(calls) / n, UsecPerCall: float64(a.usec-b.usec) / float64(calls)})
		}
	}
	ids := make([]string, 0, n)
	phase("start", func() {
		for i := 0; i < n; i++ {
			body, _, ok := l.call(http.MethodPost, "/start_instance?workflow_name="+flowName, "")
			if !ok {
				continue
			}
			var r struct {
				ID string `json:"workflow_instance_id"`
			}
			_ = json.Unmarshal([]byte(body), &r)
			ids = append(ids, r.ID)
		}
	})
	phase("publish", func() {
		for _, id := range ids {
			l.call(http.MethodPost, "/update_instance?workflow_instance_id="+id+"&action_type=publish&event_name=bench_t0&is_retry=false", `{"bench":1}`)
		}
	})
	phase("consume", func() {
		for _, id := range ids {
			l.call(http.MethodPost, "/update_instance?workflow_instance_id="+id+"&action_type=consume&event_name=bench_t0&service_name=bench_b&is_retry=false", "")
		}
	})
	// Publish the second task outside any phase so the final consume is
	// measured alone. That consume also stamps the instance and archives it.
	for _, id := range ids {
		l.call(http.MethodPost, "/update_instance?workflow_instance_id="+id+"&action_type=publish&event_name=bench_t1&is_retry=false", `{"bench":1}`)
	}
	phase("consume(final)", func() {
		for _, id := range ids {
			l.call(http.MethodPost, "/update_instance?workflow_instance_id="+id+"&action_type=consume&event_name=bench_t1&service_name=bench_c&is_retry=false", "")
		}
		time.Sleep(500 * time.Millisecond) // the archive worker drains the queue
	})
	return rows
}

// ---- instances in redis ----

func populateInstances(ctx context.Context, rdb *redis.Client, from, to int) {
	now := time.Now().Unix()
	pipe := rdb.Pipeline()
	for i := from; i < to; i++ {
		// Schema 2 layout (docs/design-phase-6.md §1): tasks under $.tasks.
		doc := map[string]interface{}{
			"schema": 2, "name": flowName, "version": "1.0", "schema_version": "1.0", "state": "PENDING",
			"startedAt": now, "completedAt": 0, "failedAt": 0,
			"tasks": []map[string]interface{}{
				{"topic": "bench_t0", "from": "bench_a", "to": "bench_b", "timeout": 60000, "state": "PENDING", "publishedAt": 0, "consumedAt": 0, "failedAt": 0},
				{"topic": "bench_t1", "from": "bench_b", "to": "bench_c", "timeout": 60000, "state": "PENDING", "publishedAt": 0, "consumedAt": 0, "failedAt": 0},
			},
		}
		pipe.JSONSet(ctx, fmt.Sprintf("workflow_instance:bp%d", i), "$", doc)
		if (i+1)%500 == 0 {
			_, _ = pipe.Exec(ctx)
		}
	}
	_, _ = pipe.Exec(ctx)
	// Let RediSearch index the new documents before measuring.
	time.Sleep(time.Duration(to-from) * 20 * time.Microsecond)
}

// cleanupFlowInstances removes instances created by load (not the bp*
// pre-populated ones) so the population level stays what the row says.
func cleanupFlowInstances(ctx context.Context, rdb *redis.Client, db *pgxpool.Pool) {
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, "workflow_instance:*", 5000).Result()
		if err != nil {
			break
		}
		var del []string
		for _, k := range keys {
			if !strings.HasPrefix(k, "workflow_instance:bp") {
				if name, _ := rdb.JSONGet(ctx, k, "$.name").Result(); strings.Contains(name, "bench_") {
					del = append(del, k)
				}
			}
		}
		if len(del) > 0 {
			_ = rdb.Unlink(ctx, del...).Err()
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	_, _ = db.Exec(ctx, `DELETE FROM instance_history WHERE name LIKE 'bench_%'`)
}

func (l *loader) endpointLatency(path string, n int) (p50, p99 float64) {
	var ds []time.Duration
	for i := 0; i < n; i++ {
		_, d, _ := l.call(http.MethodGet, path, "")
		ds = append(ds, d)
	}
	st := latencyStats(ds)
	return st.P50ms, st.P99ms
}

// ---- contention ----

// contention compares `workers` goroutines each driving its own task of
// ONE 50-task instance against the same goroutines each driving task 0 of
// its own instance.
func (l *loader) contention(ctx context.Context, workers, rounds int) ContentionResult {
	res := ContentionResult{Rounds: rounds}
	var same, spread []time.Duration
	var mu sync.Mutex
	rec := func(dst *[]time.Duration) func(sample) {
		return func(s sample) {
			if s.endpoint == "start" || !s.ok {
				return
			}
			mu.Lock()
			*dst = append(*dst, s.dur)
			mu.Unlock()
		}
	}
	n := profileTaskCounts[len(profileTaskCounts)-1] // 50
	for r := 0; r < rounds; r++ {
		// same instance
		body, _, ok := l.call(http.MethodPost, "/start_instance?workflow_name="+chainName(n), "")
		if !ok {
			continue
		}
		var resp struct {
			ID string `json:"workflow_instance_id"`
		}
		_ = json.Unmarshal([]byte(body), &resp)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				topic := fmt.Sprintf("bench_c%d_t%d", n, i)
				_, d, ok := l.call(http.MethodPost, "/update_instance?workflow_instance_id="+resp.ID+"&action_type=publish&event_name="+topic+"&is_retry=false", `{"bench":1}`)
				rec(&same)(sample{"publish", d, ok})
				_, d, ok = l.call(http.MethodPost, "/update_instance?workflow_instance_id="+resp.ID+"&action_type=consume&event_name="+topic+"&service_name=bench_b&is_retry=false", "")
				rec(&same)(sample{"consume", d, ok})
			}(w)
		}
		wg.Wait()
		// separate instances
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				spec := chainFlow(n)
				spec.steps = spec.steps[:2] // task 0 only
				l.saga(spec, rec(&spread))
			}()
		}
		wg.Wait()
	}
	res.Same, res.Spread = latencyStats(same), latencyStats(spread)
	return res
}

// ---- redis helpers ----

func redisInfoFloat(ctx context.Context, rdb *redis.Client, section, key string) float64 {
	info, err := rdb.Info(ctx, section).Result()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, key+":") {
			v, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, key+":")), 64)
			return v
		}
	}
	return 0
}

func redisCPUSeconds(ctx context.Context, rdb *redis.Client) float64 {
	return redisInfoFloat(ctx, rdb, "cpu", "used_cpu_sys") + redisInfoFloat(ctx, rdb, "cpu", "used_cpu_user")
}

func redisUsedMemory(ctx context.Context, rdb *redis.Client) int64 {
	return int64(redisInfoFloat(ctx, rdb, "memory", "used_memory"))
}
