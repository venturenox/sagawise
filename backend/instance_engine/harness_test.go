//go:build integration

// Shared harness for the integration and contract tests. It runs against a
// real redis-stack and Postgres addressed by the same REDIS_*/POSTGRES_*
// variables the binary reads (defaults target the local `make start` stack).
package instance_engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wtfsaga/db_connect"
	"wtfsaga/internal/testx"
	"wtfsaga/templating"
	"wtfsaga/utils"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// fakeClock lives in engine_test.go (no build tag) so unit tests share it.

// ---- webhook sink ----

type webhookCall struct {
	Service string // the `service=` query value (the consuming service)
	Path    string
	Body    map[string]interface{}
}

type webhookSink struct {
	mu    sync.Mutex
	calls []webhookCall
}

func (s *webhookSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)
	s.mu.Lock()
	s.calls = append(s.calls, webhookCall{Service: r.URL.Query().Get("service"), Path: r.URL.Path, Body: body})
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *webhookSink) Calls() []webhookCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]webhookCall(nil), s.calls...)
}

// ---- redis fault injection ----

// redisFaults is a go-redis hook that fails the next n commands with a given
// name (lowercase, e.g. "json.get", "zrem") with an injected error.
type redisFaults struct {
	mu        sync.Mutex
	cmd       string
	remaining int
	hits      int
}

var errInjected = errors.New("injected redis fault")

func (f *redisFaults) FailNext(cmd string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmd, f.remaining, f.hits = strings.ToLower(cmd), n, 0
}

func (f *redisFaults) Hits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

func (f *redisFaults) DialHook(next redis.DialHook) redis.DialHook { return next }
func (f *redisFaults) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (f *redisFaults) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		f.mu.Lock()
		inject := f.remaining > 0 && strings.ToLower(cmd.Name()) == f.cmd
		if inject {
			f.remaining--
			f.hits++
		}
		f.mu.Unlock()
		if inject {
			cmd.SetErr(errInjected)
			return errInjected
		}
		return next(ctx, cmd)
	}
}

// ---- workflows ----

const itWorkflow = "it_test_flow"

// twoTaskFlow mirrors examples/order_flow under names no real DSL uses:
// it_orders --it_order_created--> it_payments --it_payment_done--> it_shipping.
func twoTaskFlow() utils.Workflow {
	return utils.Workflow{
		Name: itWorkflow, Version: "1.0", Schema_version: "1.0",
		Tasks: []utils.Task{
			{Topic: "it_order_created", From: "it_orders", To: "it_payments", Timeout: 20000},
			{Topic: "it_payment_done", From: "it_payments", To: "it_shipping", Timeout: 20000},
		},
	}
}

// ---- environment ----

type env struct {
	t      testx.T
	ctx    context.Context
	eng    *Engine
	clock  *fakeClock
	sink   *webhookSink
	hook   *httptest.Server
	faults *redisFaults
	pgDown atomic.Bool

	mu    sync.Mutex
	names []string
	ids   []string
}

func setDefault(t testx.T, name, def string) {
	t.Helper()
	if os.Getenv(name) == "" {
		t.Setenv(name, def)
	}
}

// newEnv connects to the stores, loads the given workflows (default:
// twoTaskFlow) through the real templating path, and returns an Engine with a
// fake clock anchored to now, a webhook sink registered for every `from`
// service, and fault hooks. Everything it creates is removed on cleanup.
func newEnv(t testx.T, workflows ...utils.Workflow) *env {
	t.Helper()
	setDefault(t, "REDIS_HOST", "localhost")
	setDefault(t, "REDIS_PORT", "6379")
	setDefault(t, "POSTGRES_HOST", "localhost")
	setDefault(t, "POSTGRES_PORT", "5432")
	setDefault(t, "POSTGRES_USERNAME", "postgres")
	setDefault(t, "POSTGRES_PASSWORD", "venturenox")
	setDefault(t, "POSTGRES_DATABASE", "sagawise")
	t.Setenv("REDIS_CONNECTION_STRING", "")

	ctx := context.Background()
	rdb := db_connect.DBConnect(ctx)
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis not reachable: %v", err)
	}
	faults := &redisFaults{}
	rdb.AddHook(faults)

	e := &env{t: t, ctx: ctx, clock: newFakeClock(time.Now().Truncate(time.Second)), sink: &webhookSink{}, faults: faults}

	cfg, err := pgxpool.ParseConfig(db_connect.PostgresURL())
	if err != nil {
		t.Fatalf("postgres config: %v", err)
	}
	cfg.BeforeConnect = func(context.Context, *pgx.ConnConfig) error {
		if e.pgDown.Load() {
			return errors.New("injected postgres outage")
		}
		return nil
	}
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("postgres pool: %v", err)
	}
	if err := db.Ping(ctx); err != nil {
		t.Fatalf("postgres not reachable: %v", err)
	}

	e.hook = httptest.NewServer(e.sink)
	e.eng = New(rdb, db_connect.ConnectRueidis(), db)
	e.eng.Clock = e.clock
	e.eng.Services = MapRegistry{}
	e.eng.HTTPClient = e.hook.Client()

	if len(workflows) == 0 {
		workflows = []utils.Workflow{twoTaskFlow()}
	}
	e.loadDSL(workflows...)

	t.Cleanup(e.cleanup)
	return e
}

// loadDSL writes the workflows to a temp dir, runs ParseDSL on it, and
// registers the webhook sink for every publishing service.
func (e *env) loadDSL(workflows ...utils.Workflow) {
	e.t.Helper()
	dir := e.t.TempDir()
	for _, wf := range workflows {
		data, err := json.Marshal(utils.WorkflowData{Workflow: wf})
		if err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, wf.Name+".json"), data, 0o600); err != nil {
			e.t.Fatal(err)
		}
		e.mu.Lock()
		e.names = append(e.names, wf.Name)
		e.mu.Unlock()
		for _, task := range wf.Tasks {
			e.eng.Services.(MapRegistry)[task.From] = e.hook.URL + "/fail"
		}
	}
	templating.ParseDSL(e.ctx, e.eng.RDB, e.eng.DB, dir)
}

func (e *env) cleanup() {
	e.hook.Close()
	e.mu.Lock()
	names, ids := e.names, e.ids
	e.mu.Unlock()

	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	// Belt and braces: anything indexed under our workflow names.
	for _, name := range names {
		res := e.eng.RDB.Do(e.ctx, "FT.SEARCH", "workflows_index", "@workflow_name:"+name, "NOCONTENT", "LIMIT", "0", "10000").Val()
		if m, ok := res.(map[interface{}]interface{}); ok {
			if results, ok := m["results"].([]interface{}); ok {
				for _, r := range results {
					if rm, ok := r.(map[interface{}]interface{}); ok {
						if key, ok := rm["id"].(string); ok {
							seen[strings.TrimPrefix(key, "workflow_instance:")] = true
						}
					}
				}
			}
		}
	}
	members, _ := e.eng.RDB.ZRange(e.ctx, deadlinesKey, 0, -1).Result()
	for id := range seen {
		e.eng.RDB.Del(e.ctx, instanceKey(id))
		for _, m := range members {
			if strings.HasPrefix(m, id+":") {
				e.eng.RDB.ZRem(e.ctx, deadlinesKey, m)
			}
		}
		_, _ = e.eng.DB.Exec(e.ctx, `DELETE FROM instance_history WHERE id = $1`, id)
	}
	for _, name := range names {
		e.eng.RDB.Del(e.ctx, "workflow_template:"+name)
		_, _ = e.eng.DB.Exec(e.ctx, `DELETE FROM instance_history WHERE name = $1`, name)
	}
	e.eng.DB.Close()
	e.eng.Search.Close()
	e.eng.RDB.Close()
}

// ---- driving the engine ----

func (e *env) do(handler http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rd)
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

// start creates an instance of the named workflow (default itWorkflow).
func (e *env) start(name ...string) string {
	e.t.Helper()
	wf := itWorkflow
	if len(name) > 0 {
		wf = name[0]
	}
	w := e.do(e.eng.StartInstance, http.MethodPost, "/start_instance?workflow_name="+wf, "")
	if w.Code != 200 {
		e.t.Fatalf("start_instance(%s): %d %s", wf, w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		e.t.Fatalf("start_instance body %q: %v", w.Body.String(), err)
	}
	id := resp["workflow_instance_id"]
	e.mu.Lock()
	e.ids = append(e.ids, id)
	e.mu.Unlock()
	return id
}

// report sends one /update_instance call. service may be empty for publish.
// retry is passed verbatim so tests can send malformed values.
func (e *env) report(id, action, topic, service, retry, payload string) *httptest.ResponseRecorder {
	target := "/update_instance?workflow_instance_id=" + id + "&action_type=" + action +
		"&event_name=" + topic + "&is_retry=" + retry
	if service != "" {
		target += "&service_name=" + service
	}
	return e.do(e.eng.UpdateInstance, http.MethodPost, target, payload)
}

func (e *env) publish(id, topic string) *httptest.ResponseRecorder {
	return e.report(id, "publish", topic, "", "false", `{"n":1}`)
}
func (e *env) consume(id, topic, service string) *httptest.ResponseRecorder {
	return e.report(id, "consume", topic, service, "false", "")
}
func (e *env) fail(id, topic, service string) *httptest.ResponseRecorder {
	return e.report(id, "fail", topic, service, "false", "")
}

// mustOK fails the test unless the response is 2xx.
func (e *env) mustOK(w *httptest.ResponseRecorder, what string) {
	e.t.Helper()
	if w.Code < 200 || w.Code >= 300 {
		e.t.Fatalf("%s: %d %s", what, w.Code, w.Body.String())
	}
}

func accepted(w *httptest.ResponseRecorder) bool { return w.Code >= 200 && w.Code < 300 }

// tick runs one reaper pass at the fake clock's current time.
func (e *env) tick() { e.eng.reapExpiredDeadlines(e.ctx) }

// ---- reading state ----

func (e *env) doc(id string) map[string]interface{} {
	e.t.Helper()
	d, ok := jsonFirstMatch[map[string]interface{}](e.ctx, e.eng.RDB, instanceKey(id), "$")
	if !ok {
		e.t.Fatalf("instance %s not found in redis", id)
	}
	return d
}

func (e *env) taskState(id, index string) string {
	task, _ := e.doc(id)[index].(map[string]interface{})
	s, _ := task["state"].(string)
	return s
}

func (e *env) taskPayload(id, index string) map[string]interface{} {
	task, _ := e.doc(id)[index].(map[string]interface{})
	p, _ := task["payload"].(map[string]interface{})
	return p
}

func (e *env) instanceState(id string) string {
	s, _ := e.doc(id)["state"].(string)
	return s
}

// deadline returns the scheduled deadline (unix ms) for a task, if any.
func (e *env) deadline(id, index string) (float64, bool) {
	score, err := e.eng.RDB.ZScore(e.ctx, deadlinesKey, deadlineMember(id, index)).Result()
	if err != nil {
		return 0, false
	}
	return score, true
}

// archived returns the archived state for id, or "" if no row exists.
func (e *env) archived(id string) string {
	var state string
	if err := e.eng.DB.QueryRow(e.ctx, `SELECT instance_data->>'state' FROM instance_history WHERE id = $1`, id).Scan(&state); err != nil {
		return ""
	}
	return state
}

// waitArchived polls for the archive row up to timeout and returns its
// state, or "" if it never appears.
func (e *env) waitArchived(id string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s := e.archived(id); s != "" {
			return s
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ""
}

func (e *env) archivedRows(id string) int {
	var n int
	_ = e.eng.DB.QueryRow(e.ctx, `SELECT count(*) FROM instance_history WHERE id = $1`, id).Scan(&n)
	return n
}

// ---- fault control ----

// postgresDown makes every new Postgres connection fail and drops the pool's
// existing ones, so the next query errors.
func (e *env) postgresDown() {
	e.pgDown.Store(true)
	e.eng.DB.Reset()
}

func (e *env) postgresUp() { e.pgDown.Store(false) }

// hangingWebhook returns a URL whose handler blocks until the test ends.
func (e *env) hangingWebhook() string {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-done
	}))
	e.t.Cleanup(func() {
		close(done)
		srv.Close()
	})
	return srv.URL + "/hang"
}

// ---- JSON helpers ----

func jsonBody(t testx.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Errorf("response is not a JSON object: %d %q", w.Code, w.Body.String())
		return map[string]interface{}{}
	}
	return m
}
