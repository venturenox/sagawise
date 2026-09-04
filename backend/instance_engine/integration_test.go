//go:build integration

package instance_engine

import (
	"testing"
	"time"
)

// The two phase-1 smoke flows. They pass on today's code and stay as the
// baseline the contract tests build on.

func TestIntegration_PublishConsumeArchive(t *testing.T) {
	e := newEnv(t)
	id := e.start()

	e.mustOK(e.publish(id, "it_order_created"), "publish task 0")
	e.mustOK(e.consume(id, "it_order_created", "it_payments"), "consume task 0")
	e.mustOK(e.publish(id, "it_payment_done"), "publish task 1")
	e.mustOK(e.consume(id, "it_payment_done", "it_shipping"), "consume task 1")

	if w := e.consume(id, "it_payment_done", "it_shipping"); accepted(w) {
		t.Errorf("duplicate consume accepted: %d %s", w.Code, w.Body.String())
	}

	doc := e.doc(id)
	if got := doc["state"]; got != "COMPLETED" {
		t.Errorf("workflow state = %v, want COMPLETED", got)
	}
	for _, idx := range []string{"0", "1"} {
		if got := e.taskState(id, idx); got != "COMPLETED" {
			t.Errorf("task %s state = %q, want COMPLETED", idx, got)
		}
		if _, ok := e.deadline(id, idx); ok {
			t.Errorf("deadline for task %s still present after consume", idx)
		}
	}
	if got, want := doc["completedAt"].(float64), float64(e.clock.Now().Unix()); got != want {
		t.Errorf("completedAt = %v, want fake clock %v", got, want)
	}
	if got := e.waitArchived(id, 5*time.Second); got != "COMPLETED" {
		t.Errorf("archived state = %q, want COMPLETED", got)
	}
	if calls := e.sink.Calls(); len(calls) != 0 {
		t.Errorf("unexpected failure webhooks: %+v", calls)
	}
}

func TestIntegration_TimeoutReaperWebhookArchive(t *testing.T) {
	e := newEnv(t)
	id := e.start()

	e.mustOK(e.report(id, "publish", "it_order_created", "", "false", `{"order_id":99}`), "publish")
	score, ok := e.deadline(id, "0")
	if !ok {
		t.Fatal("deadline not scheduled")
	}
	if want := float64(e.clock.Now().UnixMilli() + 20000); score != want {
		t.Fatalf("deadline score = %v, want %v", score, want)
	}

	e.clock.Advance(19 * time.Second)
	e.tick()
	if got := e.taskState(id, "0"); got != "PUBLISHED" {
		t.Fatalf("task 0 before deadline = %q, want PUBLISHED", got)
	}

	e.clock.Advance(2 * time.Second)
	e.tick()
	if got := e.taskState(id, "0"); got != "FAILED" {
		t.Errorf("task 0 state = %q, want FAILED", got)
	}
	if got := e.instanceState(id); got != "FAILED" {
		t.Errorf("workflow state = %v, want FAILED", got)
	}
	if _, ok := e.deadline(id, "0"); ok {
		t.Error("deadline still present after reaping")
	}

	calls := e.sink.Calls()
	if len(calls) != 1 {
		t.Fatalf("webhook calls = %d, want 1: %+v", len(calls), calls)
	}
	if calls[0].Service != "it_payments" {
		t.Errorf("webhook service = %q, want it_payments", calls[0].Service)
	}
	if calls[0].Body["order_id"] != float64(99) {
		t.Errorf("webhook body = %v, want the published payload", calls[0].Body)
	}
	if got := e.waitArchived(id, 5*time.Second); got != "FAILED" {
		t.Errorf("archived state = %q, want FAILED", got)
	}

	e.tick()
	if calls := e.sink.Calls(); len(calls) != 1 {
		t.Errorf("webhook calls after second tick = %d, want 1", len(calls))
	}
}
