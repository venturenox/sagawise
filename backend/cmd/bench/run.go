package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"wtfsaga/db_connect"
	"wtfsaga/utils"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type runConfig struct {
	label       string
	out         string
	rates       string
	duration    time.Duration
	lagTasks    int
	benchCount  int
	benchTime   string
	skipGoBench bool
}

const (
	flowName    = "bench_flow"    // two tasks, long timeout: the load workload
	timeoutName = "bench_timeout" // one task, 2s timeout: the reaper-lag workload
	lagTimeout  = 2000            // ms
	// lagPublishParallel bounds the concurrent publish burst that arms the
	// deadlines, so they all fall due together rather than across the loop.
	lagPublishParallel = 64
)

func setDefault(name, def string) {
	if os.Getenv(name) == "" {
		_ = os.Setenv(name, def)
	}
}

func runBenchmark(cfg runConfig) (string, error) {
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

	res := &Results{Label: cfg.label, Date: time.Now().Format(time.RFC3339), Env: map[string]string{}, Config: map[string]string{
		"rates": cfg.rates, "duration": cfg.duration.String(), "lag_tasks": strconv.Itoa(cfg.lagTasks),
		"bench_count": strconv.Itoa(cfg.benchCount), "bench_time": cfg.benchTime,
	}}
	captureEnv(ctx, res, rdb, db)

	runDir := filepath.Join(cfg.out, fmt.Sprintf("%s_%s_%s", time.Now().Format("2006-01-02_1504"), res.Commit, cfg.label))
	if err := os.MkdirAll(runDir, 0o750); err != nil {
		return "", err
	}
	fmt.Fprintln(os.Stderr, "run dir:", runDir)

	// --- server under test ---
	work, err := os.MkdirTemp("", "sagawise-bench")
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
	for _, wf := range benchWorkflows() {
		data, _ := json.Marshal(utils.WorkflowData{Workflow: wf})
		if err := os.WriteFile(filepath.Join(dslDir, wf.Name+".json"), data, 0o600); err != nil {
			return "", err
		}
	}
	services, _ := json.Marshal([]utils.Service{
		{ServiceName: "bench_a", FailureUrl: hooks.URL()},
		{ServiceName: "bench_b", FailureUrl: hooks.URL()},
		{ServiceName: "bench_t", FailureUrl: hooks.URL()},
	})
	servicesFile := filepath.Join(work, "services.json")
	if err := os.WriteFile(servicesFile, services, 0o600); err != nil {
		return "", err
	}

	srv, err := launchServer(bin, dslDir, servicesFile)
	if err != nil {
		return "", err
	}
	defer srv.stop()
	defer cleanupBenchData(ctx, rdb, db)
	fmt.Fprintln(os.Stderr, "server up at", srv.addr)

	l := &loader{base: "http://" + srv.addr, client: &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{MaxIdleConns: 1000, MaxIdleConnsPerHost: 1000, IdleConnTimeout: 90 * time.Second},
	}}

	// --- warm-up ---
	fmt.Fprintln(os.Stderr, "warm-up...")
	l.runRate(ctx, defaultFlow(), 20, 3*time.Second, nil, nil)
	cleanupBenchData(ctx, rdb, db)

	// --- held rates ---
	for _, r := range strings.Split(cfg.rates, ",") {
		rate, err := strconv.ParseFloat(strings.TrimSpace(r), 64)
		if err != nil {
			return "", fmt.Errorf("bad rate %q", r)
		}
		fmt.Fprintf(os.Stderr, "rate %.0f sagas/s for %s...\n", rate, cfg.duration)
		rr := l.runRate(ctx, defaultFlow(), rate, cfg.duration, rdb, db)
		res.Rates = append(res.Rates, rr)
		fmt.Fprintf(os.Stderr, "  achieved %.1f sagas/s, %d requests, %d errors, consume p99 %.1fms, archive missing %d\n",
			rr.AchievedSagasPS, rr.Requests, rr.Errors, rr.Endpoints["consume"].P99ms, rr.ArchiveMissing)
		cleanupBenchData(ctx, rdb, db)
	}

	// --- reaper lag ---
	fmt.Fprintf(os.Stderr, "reaper lag over %d timed-out tasks...\n", cfg.lagTasks)
	res.ReaperLag = l.measureReaperLag(ctx, hooks, cfg.lagTasks, 20*time.Second)
	fmt.Fprintf(os.Stderr, "  received %d/%d, p50 %.0fms, p99 %.0fms, max %.0fms\n",
		res.ReaperLag.Received, res.ReaperLag.Tasks, res.ReaperLag.P50ms, res.ReaperLag.P99ms, res.ReaperLag.MaxMs)
	cleanupBenchData(ctx, rdb, db)
	srv.stop()

	// --- go micro-benchmarks (server stopped: no competing reaper) ---
	goBench := "(skipped)"
	if !cfg.skipGoBench {
		fmt.Fprintln(os.Stderr, "go micro-benchmarks...")
		cmd := exec.Command("go", "test", "-tags", "integration", "-run", "^$", "-bench", ".", "-benchmem",
			"-benchtime", cfg.benchTime, "-count", strconv.Itoa(cfg.benchCount), "./instance_engine/") // #nosec G204 -- fixed argv, operator-supplied numbers
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("go test -bench: %v\n%s", err, out)
		}
		goBench = string(out)
	}

	// --- write the run ---
	if err := os.WriteFile(filepath.Join(runDir, "go-bench.txt"), []byte(goBench), 0o600); err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(filepath.Join(runDir, "load.json"), data, 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(runDir, "env.txt"), []byte(envText(res)), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(runDir, "report.md"), []byte(renderReport(res, goBench)), 0o600); err != nil {
		return "", err
	}
	return runDir, nil
}

// benchWorkflowNames lists every workflow any bench mode may load, for cleanup.
func benchWorkflowNames() []string {
	names := []string{flowName, timeoutName}
	for _, n := range profileTaskCounts {
		names = append(names, chainName(n))
	}
	return names
}

func benchWorkflows() []utils.Workflow {
	return []utils.Workflow{
		{Name: flowName, Version: "1.0", Schema_version: "1.0", Tasks: []utils.Task{
			{Topic: "bench_t0", From: "bench_a", To: "bench_b", Timeout: 60000},
			{Topic: "bench_t1", From: "bench_b", To: "bench_c", Timeout: 60000},
		}},
		{Name: timeoutName, Version: "1.0", Schema_version: "1.0", Tasks: []utils.Task{
			{Topic: "bench_tt", From: "bench_t", To: "bench_nobody", Timeout: lagTimeout},
		}},
	}
}

// ---- environment ----

func captureEnv(ctx context.Context, res *Results, rdb *redis.Client, db *pgxpool.Pool) {
	if out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output(); err == nil {
		res.Commit = strings.TrimSpace(string(out))
	} else {
		res.Commit = "unknown"
	}
	if out, err := exec.Command("git", "status", "--porcelain").Output(); err == nil && len(bytes.TrimSpace(out)) > 0 {
		res.Dirty = true
	}
	res.Env["go"] = runtime.Version()
	res.Env["os_arch"] = runtime.GOOS + "/" + runtime.GOARCH
	res.Env["cpus"] = strconv.Itoa(runtime.NumCPU())
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "model name") {
				res.Env["cpu"] = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
				break
			}
		}
	}
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemTotal") {
				res.Env["mem"] = strings.TrimSpace(strings.TrimPrefix(line, "MemTotal:"))
				break
			}
		}
	}
	if info, err := rdb.Info(ctx, "server").Result(); err == nil {
		for _, line := range strings.Split(info, "\n") {
			if strings.HasPrefix(line, "redis_version:") {
				res.Env["redis"] = strings.TrimSpace(strings.TrimPrefix(line, "redis_version:"))
			}
		}
	}
	var pgv string
	if err := db.QueryRow(ctx, "SELECT version()").Scan(&pgv); err == nil {
		res.Env["postgres"] = strings.Join(strings.Fields(pgv)[:2], " ")
	}
	res.Env["redis_addr"] = os.Getenv("REDIS_HOST") + ":" + os.Getenv("REDIS_PORT")
	res.Env["postgres_addr"] = os.Getenv("POSTGRES_HOST") + ":" + os.Getenv("POSTGRES_PORT")
}

func envText(res *Results) string {
	var b strings.Builder
	fmt.Fprintf(&b, "label=%s\ndate=%s\ncommit=%s\ndirty=%v\n", res.Label, res.Date, res.Commit, res.Dirty)
	for _, k := range sortedKeys(res.Env) {
		fmt.Fprintf(&b, "%s=%s\n", k, res.Env[k])
	}
	for _, k := range sortedKeys(res.Config) {
		fmt.Fprintf(&b, "config.%s=%s\n", k, res.Config[k])
	}
	return b.String()
}

// ---- server process ----

type server struct {
	cmd  *exec.Cmd
	addr string
	done chan struct{}
}

func launchServer(bin, dslDir, servicesFile string) (*server, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	addr := l.Addr().String()
	_ = l.Close()

	s := &server{addr: addr, done: make(chan struct{})}
	s.cmd = exec.Command(bin) // #nosec G204 -- binary we just built
	s.cmd.Env = append(os.Environ(),
		"SAGAWISE_ADDR="+addr, "SAGAWISE_DSL_DIR="+dslDir, "SAGAWISE_SERVICES_FILE="+servicesFile, "OTEL_SDK_DISABLED=true",
		"SAGAWISE_METRICS_ADDR=off", "SAGAWISE_LOG_LEVEL=warn",
		"SAGAWISE_API_KEYS="+benchAPIKey, "SAGAWISE_WEBHOOK_SECRET=bench-webhook-secret")
	s.cmd.Stdout, s.cmd.Stderr = io.Discard, io.Discard
	if err := s.cmd.Start(); err != nil {
		return nil, err
	}
	go func() { _ = s.cmd.Wait(); close(s.done) }()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/live") // #nosec G107 -- local address chosen above
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return s, nil
			}
		}
		select {
		case <-s.done:
			return nil, errors.New("server exited during startup")
		case <-time.After(100 * time.Millisecond):
		}
	}
	s.stop()
	return nil, errors.New("server did not become live within 30s")
}

func (s *server) stop() {
	select {
	case <-s.done:
		return
	default:
	}
	_ = s.cmd.Process.Signal(syscall.SIGINT)
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		_ = s.cmd.Process.Kill()
		<-s.done
	}
}

// ---- webhook receiver ----

type webhookReceiver struct {
	srv *http.Server
	ln  net.Listener
	mu  sync.Mutex
	got map[string]time.Time // bench_id -> arrival
}

func newWebhookReceiver() *webhookReceiver {
	r := &webhookReceiver{got: map[string]time.Time{}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	r.ln = ln
	r.srv = &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		now := time.Now()
		var body struct {
			BenchID string `json:"bench_id"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		r.mu.Lock()
		if _, dup := r.got[body.BenchID]; !dup {
			r.got[body.BenchID] = now
		}
		r.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = r.srv.Serve(ln) }()
	return r
}

func (r *webhookReceiver) URL() string { return "http://" + r.ln.Addr().String() + "/fail" }

func (r *webhookReceiver) arrival(id string) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.got[id]
	return t, ok
}

func (r *webhookReceiver) Close() { _ = r.srv.Close() }

// ---- cleanup ----

func cleanupBenchData(ctx context.Context, rdb *redis.Client, db *pgxpool.Pool) {
	// Pre-populated instances (profile mode) are keyed workflow_instance:bp*.
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, "workflow_instance:bp*", 5000).Result()
		if err != nil {
			break
		}
		if len(keys) > 0 {
			_ = rdb.Unlink(ctx, keys...).Err()
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	for _, name := range benchWorkflowNames() {
		for {
			// workflow_name is a TAG field since phase 5; bench names are
			// [a-z_] so no escaping is needed inside the braces.
			res := rdb.Do(ctx, "FT.SEARCH", "workflows_index", "@workflow_name:{"+name+"}", "NOCONTENT", "LIMIT", "0", "10000").Val()
			m, _ := res.(map[interface{}]interface{})
			results, _ := m["results"].([]interface{})
			if len(results) == 0 {
				break
			}
			pipe := rdb.Pipeline()
			for _, r := range results {
				if rm, ok := r.(map[interface{}]interface{}); ok {
					if key, ok := rm["id"].(string); ok {
						pipe.Del(ctx, key)
					}
				}
			}
			_, _ = pipe.Exec(ctx)
		}
		_, _ = db.Exec(ctx, `DELETE FROM instance_history WHERE name = $1`, name)
	}
	// Deadlines and queued jobs of deleted instances: drop members whose
	// instance is gone (the workers would drop them one by one otherwise).
	for _, key := range []string{"task_deadlines", "webhook_pending", "archive_pending"} {
		members, _ := rdb.ZRange(ctx, key, 0, -1).Result()
		if len(members) == 0 {
			continue
		}
		pipe := rdb.Pipeline()
		for _, m := range members {
			id, _, _ := strings.Cut(m, ":")
			if n, _ := rdb.Exists(ctx, "workflow_instance:"+id).Result(); n == 0 {
				pipe.ZRem(ctx, key, m)
				pipe.HDel(ctx, strings.TrimSuffix(key, "_pending")+"_attempts", m)
			}
		}
		_, _ = pipe.Exec(ctx)
	}
}
