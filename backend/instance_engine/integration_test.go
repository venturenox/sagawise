//go:build integration

// Integration tests run against a real redis-stack and Postgres, addressed by
// the same REDIS_*/POSTGRES_* variables the binary reads (defaults below
// target the local docker compose stack). Run with:
//
//	go test -tags integration ./...
//
// or `make test-integration`.
package instance_engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wtfsaga/db_connect"
	"wtfsaga/templating"
)

const itWorkflow = "it_test_flow"

// itDSL is a two-task saga mirroring examples/order_flow, under a name no
// real DSL uses so the test can clean up after itself.
var itDSL = `{"workflow":{"version":"1.0","schema_version":"1.0","name":"` + itWorkflow + `","tasks":[
  {"topic":"it_order_created","from":"it_orders","to":"it_payments","timeout":20000},
  {"topic":"it_payment_done","from":"it_payments","to":"it_shipping","timeout":20000}]}}`

func setDefault(t *testing.T, name, def string) {
	t.Helper()
	if os.Getenv(name) == "" {
		t.Setenv(name, def)
	}
}

// webhookSink records failure webhooks the engine delivers.
type webhookSink struct {
	mu    sync.Mutex
	calls []webhookCall
}

type webhookCall struct {
	Query string
	Body  map[string]interface{}
}

func (s *webhookSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)
	s.mu.Lock()
	s.calls = append(s.calls, webhookCall{Query: r.URL.RawQuery, Body: body})
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *webhookSink) Calls() []webhookCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]webhookCall(nil), s.calls...)
}

type itEnv struct {
	eng   *Engine
	clock *fakeClock
	sink  *webhookSink
	ctx   context.Context
}

// newITEnv connects to the stores, loads the test DSL through the real
// templating path, and returns an Engine with a fake clock and a webhook
// sink. Every instance it creates is deleted on cleanup.
func newITEnv(t *testing.T) *itEnv {
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
	db := db_connect.ConnectPostgres(ctx)
	if err := db.Ping(ctx); err != nil {
		t.Fatalf("postgres not reachable: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, itWorkflow+".json"), []byte(itDSL), 0o600); err != nil {
		t.Fatal(err)
	}
	templating.ParseDSL(ctx, rdb, db, dir)

	sink := &webhookSink{}
	hook := httptest.NewServer(sink)

	clock := newFakeClock(time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	eng := New(rdb, nil, db)
	eng.Clock = clock
	eng.Services = MapRegistry{"it_orders": hook.URL + "/fail", "it_payments": hook.URL + "/fail"}
	eng.HTTPClient = hook.Client()

	t.Cleanup(func() {
		hook.Close()
		ids := rdb.Do(ctx, "FT.SEARCH", "workflows_index", "@workflow_name:"+itWorkflow, "NOCONTENT", "LIMIT", "0", "1000").Val()
		if m, ok := ids.(map[interface{}]interface{}); ok {
			if results, ok := m["results"].([]interface{}); ok {
				for _, r := range results {
					if rm, ok := r.(map[interface{}]interface{}); ok {
						if id, ok := rm["id"].(string); ok {
							rdb.Del(ctx, id)
						}
					}
				}
			}
		}
		rdb.Del(ctx, "workflow_template:"+itWorkflow)
		_, _ = db.Exec(ctx, `DELETE FROM instance_history WHERE name = $1`, itWorkflow)
		db.Close()
		rdb.Close()
	})
	return &itEnv{eng: eng, clock: clock, sink: sink, ctx: ctx}
}

func (env *itEnv) do(t *testing.T, handler http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rd)
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func (env *itEnv) start(t *testing.T) string {
	t.Helper()
	w := env.do(t, env.eng.StartInstance, http.MethodPost, "/start_instance?workflow_name="+itWorkflow, "")
	if w.Code != 200 {
		t.Fatalf("start_instance: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp["workflow_instance_id"]
}

func (env *itEnv) update(t *testing.T, id, action, topic, service, payload string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/update_instance?workflow_instance_id=" + id + "&action_type=" + action +
		"&event_name=" + topic + "&service_name=" + service + "&is_retry=false"
	return env.do(t, env.eng.UpdateInstance, http.MethodPost, target, payload)
}

func (env *itEnv) doc(t *testing.T, id string) map[string]interface{} {
	t.Helper()
	d, ok := jsonFirstMatch[map[string]interface{}](env.ctx, env.eng.RDB, instanceKey(id), "$")
	if !ok {
		t.Fatalf("instance %s not found in redis", id)
	}
	return d
}

func taskState(doc map[string]interface{}, index string) string {
	task, _ := doc[index].(map[string]interface{})
	s, _ := task["state"].(string)
	return s
}

// archivedState polls Postgres for the archive row, since archiving is a
// detached goroutine.
func (env *itEnv) archivedState(t *testing.T, id string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		err := env.eng.DB.QueryRow(env.ctx, `SELECT instance_data->>'state' FROM instance_history WHERE id = $1`, id).Scan(&state)
		if err == nil {
			return state
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("instance %s never archived", id)
	return ""
}

func TestIntegration_PublishConsumeArchive(t *testing.T) {
	env := newITEnv(t)
	id := env.start(t)

	steps := []struct{ action, topic, service string }{
		{"publish", "it_order_created", "it_orders"},
		{"consume", "it_order_created", "it_payments"},
		{"publish", "it_payment_done", "it_payments"},
		{"consume", "it_payment_done", "it_shipping"},
	}
	for _, s := range steps {
		w := env.update(t, id, s.action, s.topic, s.service, `{"order_id":42}`)
		if w.Code != 200 {
			t.Fatalf("%s %s by %s: %d %s", s.action, s.topic, s.service, w.Code, w.Body.String())
		}
	}

	// Duplicate consume without retry is rejected.
	if w := env.update(t, id, "consume", "it_payment_done", "it_shipping", ""); w.Code != http.StatusForbidden {
		t.Errorf("duplicate consume: %d %s, want 403", w.Code, w.Body.String())
	}

	doc := env.doc(t, id)
	if got := doc["state"]; got != "COMPLETED" {
		t.Errorf("workflow state = %v, want COMPLETED", got)
	}
	for _, idx := range []string{"0", "1"} {
		if got := taskState(doc, idx); got != "COMPLETED" {
			t.Errorf("task %s state = %q, want COMPLETED", idx, got)
		}
	}
	// Timestamps come from the injected clock, not the wall clock.
	if got, want := doc["completedAt"].(float64), float64(env.clock.Now().Unix()); got != want {
		t.Errorf("completedAt = %v, want fake clock %v", got, want)
	}
	if got := env.archivedState(t, id); got != "COMPLETED" {
		t.Errorf("archived state = %q, want COMPLETED", got)
	}
	// Deadlines were consumed.
	for _, idx := range []string{"0", "1"} {
		if err := env.eng.RDB.ZScore(env.ctx, deadlinesKey, deadlineMember(id, idx)).Err(); err == nil {
			t.Errorf("deadline for task %s still present after consume", idx)
		}
	}
	if calls := env.sink.Calls(); len(calls) != 0 {
		t.Errorf("unexpected failure webhooks: %+v", calls)
	}
}

func TestIntegration_TimeoutReaperWebhookArchive(t *testing.T) {
	env := newITEnv(t)
	id := env.start(t)

	if w := env.update(t, id, "publish", "it_order_created", "it_orders", `{"order_id":99}`); w.Code != 200 {
		t.Fatalf("publish: %d %s", w.Code, w.Body.String())
	}
	// Deadline is scheduled from the fake clock: now + 20000ms.
	score, err := env.eng.RDB.ZScore(env.ctx, deadlinesKey, deadlineMember(id, "0")).Result()
	if err != nil {
		t.Fatalf("deadline not scheduled: %v", err)
	}
	if want := float64(env.clock.Now().UnixMilli() + 20000); score != want {
		t.Fatalf("deadline score = %v, want %v", score, want)
	}

	// One tick before the deadline: nothing happens.
	env.clock.Advance(19 * time.Second)
	env.eng.reapExpiredDeadlines(env.ctx)
	if got := taskState(env.doc(t, id), "0"); got != "PUBLISHED" {
		t.Fatalf("task 0 state before deadline = %q, want PUBLISHED", got)
	}

	// Past the deadline: task FAILED, workflow FAILED, webhook to the publisher, archived.
	env.clock.Advance(2 * time.Second)
	env.eng.reapExpiredDeadlines(env.ctx)

	doc := env.doc(t, id)
	if got := taskState(doc, "0"); got != "FAILED" {
		t.Errorf("task 0 state = %q, want FAILED", got)
	}
	if got := doc["state"]; got != "FAILED" {
		t.Errorf("workflow state = %v, want FAILED", got)
	}
	if err := env.eng.RDB.ZScore(env.ctx, deadlinesKey, deadlineMember(id, "0")).Err(); err == nil {
		t.Error("deadline still present after reaping")
	}

	calls := env.sink.Calls()
	if len(calls) != 1 {
		t.Fatalf("webhook calls = %d, want 1: %+v", len(calls), calls)
	}
	if calls[0].Query != "service=it_payments" {
		t.Errorf("webhook query = %q, want service=it_payments", calls[0].Query)
	}
	if calls[0].Body["order_id"] != float64(99) {
		t.Errorf("webhook body = %v, want the published payload", calls[0].Body)
	}
	if got := env.archivedState(t, id); got != "FAILED" {
		t.Errorf("archived state = %q, want FAILED", got)
	}

	// A second tick is a no-op: nothing left to reap, no second webhook.
	env.eng.reapExpiredDeadlines(env.ctx)
	if calls := env.sink.Calls(); len(calls) != 1 {
		t.Errorf("webhook calls after second tick = %d, want 1", len(calls))
	}
}
