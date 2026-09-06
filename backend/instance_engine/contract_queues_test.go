//go:build integration

package instance_engine

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"wtfsaga/internal/testx"
	"wtfsaga/utils"
)

// Phase 6 design note §5: the archive and webhook side effects are durable
// queues in Redis, drained by workers with a lease and backoff.

// A2: an archive that failed in one process is picked up by the next one.
// The queue lives in Redis, so a fresh Engine on the same stores drains it.
func TestContract_ArchiveBacklogDrainsAfterRestart(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")
		e.postgresDown()
		e.mustOK(e.fail(id, "it_order_created", "it_payments"), "fail during outage")
		if got := e.archived(id); got != "" {
			t.Fatalf("archived %q during the outage", got)
		}
		e.postgresUp()

		// "Restart": a second engine over the same Redis and Postgres, with
		// its own clock past the retry backoff.
		fresh := New(e.eng.RDB, e.eng.DB)
		fresh.Clock = e.clock
		e.clock.Advance(2 * time.Second)
		fresh.Archiver.tick(e.ctx)

		if got := e.archived(id); got != "FAILED" {
			t.Errorf("archived = %q after the new process drained the queue, want FAILED", got)
		}
		if n, _ := fresh.Archiver.Pending(e.ctx); n != 0 {
			t.Errorf("archive_pending still holds %d job(s)", n)
		}
	})
}

// flakySink answers 500 to the first `failures` calls, then 200.
type flakySink struct {
	failures int32
	calls    atomic.Int32
	inner    *webhookSink
}

func (f *flakySink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.calls.Add(1) <= f.failures {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	f.inner.ServeHTTP(w, r)
}

// W3/D3: a webhook that fails is retried with backoff (2 s, then 6 s) and
// delivered once the endpoint answers; the attempt count is visible while
// it is pending and gone once delivered.
func TestContract_WebhookRetriesWithBackoff(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		flaky := &flakySink{failures: 2, inner: e.sink}
		srv := httptest.NewServer(flaky)
		t.Cleanup(srv.Close)
		e.eng.Services.(MapRegistry)["it_orders"] = srv.URL + "/fail"

		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")
		e.mustOK(e.fail(id, "it_order_created", "it_payments"), "fail") // attempt 1 fails (drained by report)

		if got := len(e.sink.Calls()); got != 0 {
			t.Fatalf("delivered %d webhook(s) although the endpoint answered 500", got)
		}
		if n, _ := e.eng.RDB.HGet(e.ctx, webhookAttemptsKey, id+":0").Int(); n != 1 {
			t.Errorf("attempts after first failure = %d, want 1", n)
		}

		e.drain() // not due yet: 2 s backoff
		if got := flaky.calls.Load(); got != 1 {
			t.Errorf("endpoint called %d times before the backoff elapsed, want 1", got)
		}

		e.clock.Advance(2 * time.Second)
		e.drain() // attempt 2 fails
		if n, _ := e.eng.RDB.HGet(e.ctx, webhookAttemptsKey, id+":0").Int(); n != 2 {
			t.Errorf("attempts after second failure = %d, want 2", n)
		}

		e.clock.Advance(6 * time.Second)
		e.drain() // attempt 3 delivers
		if got := len(e.sink.Calls()); got != 1 {
			t.Errorf("delivered %d webhook(s) after the endpoint recovered, want 1", got)
		}
		if n, _ := e.eng.Webhooks.Pending(e.ctx); n != 0 {
			t.Errorf("webhook_pending still holds %d job(s)", n)
		}
		if err := e.eng.RDB.HGet(e.ctx, webhookAttemptsKey, id+":0").Err(); err == nil {
			t.Errorf("attempt count not cleared after delivery")
		}
		if got := e.instanceState(id); got != "FAILED" {
			t.Errorf("instance state = %q; delivery must not change state", got)
		}
	})
}

// W3/W4: after the bounded retries the webhook is dropped, logged and
// counted. The instance stays FAILED and archived.
func TestContract_WebhookGivesUpAfterBoundedRetries(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		dead := &flakySink{failures: 1 << 30, inner: e.sink}
		srv := httptest.NewServer(dead)
		t.Cleanup(srv.Close)
		e.eng.Services.(MapRegistry)["it_orders"] = srv.URL + "/fail"

		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")
		e.mustOK(e.fail(id, "it_order_created", "it_payments"), "fail") // attempt 1

		for i := 0; i < webhookMaxAttempts; i++ {
			e.clock.Advance(5 * time.Minute) // past any backoff
			e.drain()
		}
		if got := dead.calls.Load(); got != webhookMaxAttempts {
			t.Errorf("endpoint called %d times, want exactly %d attempts", got, webhookMaxAttempts)
		}
		if n, _ := e.eng.Webhooks.Pending(e.ctx); n != 0 {
			t.Errorf("webhook_pending still holds %d job(s) after giving up", n)
		}
		if got := e.eng.Webhooks.GiveUps.Load(); got != 1 {
			t.Errorf("GiveUps = %d, want 1", got)
		}
		if got := e.instanceState(id); got != "FAILED" {
			t.Errorf("instance state = %q, want FAILED", got)
		}
		if got := e.waitArchived(id, 5*time.Second); got != "FAILED" {
			t.Errorf("archived = %q, want FAILED", got)
		}
	})
}

// §5: a job claimed by a worker that died comes back after the lease.
func TestContract_LeasedJobReturnsAfterLease(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")

		// Fail the task without draining, then claim the webhook job the way
		// a worker does, and "die" before delivering.
		res, err := e.eng.transition(e.ctx, id, "fail", false, []int{0}, "")
		if err != nil || res.Code != "OK" || !res.WebhookQueued {
			t.Fatalf("fail transition: %+v %v", res, err)
		}
		claimed, err := e.eng.Webhooks.queue.claim(e.ctx, e.clock.Now(), workerLease, 10)
		if err != nil || len(claimed) != 1 || claimed[0] != id+":0" {
			t.Fatalf("claim = %v, %v", claimed, err)
		}

		e.drain()
		if got := len(e.sink.Calls()); got != 0 {
			t.Fatalf("a leased job was delivered by another tick (%d calls)", got)
		}
		e.clock.Advance(workerLease + time.Second)
		e.drain()
		if got := len(e.sink.Calls()); got != 1 {
			t.Errorf("webhook calls after the lease expired = %d, want 1", got)
		}
	})
}

// T2/P6: a publish over several tasks sharing a topic is all-or-nothing.
// With one of them already COMPLETED, a plain publish is refused for the
// whole report and the other task's deadline is untouched; a retry publish
// is idempotent for the completed task and re-arms the published one (D2).
func TestContract_SharedTopicPublishIsAllOrNothing(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		shared := utils.Workflow{
			Name: "it_shared_topic", Version: "1.0", Schema_version: "1.0",
			Tasks: []utils.Task{
				{Topic: "it_broadcast", From: "it_pub", To: "it_sub_a", Timeout: 20000},
				{Topic: "it_broadcast", From: "it_pub", To: "it_sub_b", Timeout: 20000},
			},
		}
		e := newEnv(t, shared)
		id := e.start(shared.Name)

		w := e.publish(id, "it_broadcast")
		e.mustOK(w, "publish")
		body := jsonBody(t, w)
		if idx, ok := body["task_index"].([]interface{}); !ok || len(idx) != 2 {
			t.Errorf("task_index = %v, want [0 1]", body["task_index"])
		}
		for _, i := range []string{"0", "1"} {
			if got := e.taskState(id, i); got != "PUBLISHED" {
				t.Errorf("task %s = %q, want PUBLISHED", i, got)
			}
		}
		e.mustOK(e.consume(id, "it_broadcast", "it_sub_a"), "consume a")
		before, _ := e.deadline(id, "1")
		e.clock.Advance(time.Second)

		w = e.publish(id, "it_broadcast")
		if w.Code != http.StatusConflict || jsonBody(t, w)["error"] != "TASK_ALREADY_COMPLETED" {
			t.Errorf("mixed publish: %d %s, want 409 TASK_ALREADY_COMPLETED", w.Code, w.Body.String())
		}
		if after, _ := e.deadline(id, "1"); after != before {
			t.Errorf("refused publish changed task 1's deadline %v -> %v", before, after)
		}
		if got := e.taskState(id, "0"); got != "COMPLETED" {
			t.Errorf("task 0 = %q after refused publish, want COMPLETED", got)
		}

		w = e.report(id, "publish", "it_broadcast", "", "true", `{"n":2}`)
		e.mustOK(w, "retry publish")
		if jsonBody(t, w)["idempotent"] != true {
			t.Errorf("retry publish body = %s, want idempotent true", w.Body.String())
		}
		if after, _ := e.deadline(id, "1"); after <= before {
			t.Errorf("retry publish did not re-arm task 1's deadline (%v -> %v)", before, after)
		}
		if got := e.taskState(id, "0"); got != "COMPLETED" {
			t.Errorf("task 0 = %q after retry publish, want COMPLETED (T1)", got)
		}
	})
}

// P4: a schema 1 document (tasks as top-level "0", "1" keys) is refused with
// a 500, never misread as an instance with no tasks.
func TestContract_SchemaOneDocumentIsInternalError(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := "itSchemaOne" + generateID()[:8]
		e.mu.Lock()
		e.ids = append(e.ids, id)
		e.mu.Unlock()
		old := map[string]interface{}{
			"name": itWorkflow, "version": "1.0", "state": "PENDING", "startedAt": e.clock.Now().Unix(),
			"0": map[string]interface{}{"topic": "it_order_created", "from": "it_orders", "to": "it_payments", "state": "PENDING", "timeout": 20000, "index": "0"},
		}
		if err := e.eng.RDB.JSONSet(e.ctx, instanceKey(id), "$", old).Err(); err != nil {
			t.Fatal(err)
		}
		w := e.publish(id, "it_order_created")
		if w.Code != http.StatusInternalServerError || jsonBody(t, w)["error"] != "INTERNAL" {
			t.Errorf("publish on a schema 1 document: %d %s, want 500 INTERNAL", w.Code, w.Body.String())
		}
	})
}
