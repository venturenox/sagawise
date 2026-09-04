//go:build integration

package instance_engine

import (
	"testing"
	"time"

	"wtfsaga/internal/testx"
)

// Contract I5: when an instance fails, siblings freeze. A PUBLISHED sibling
// loses its deadline in the same step, so the reaper never touches it and no
// second webhook fires. (#3)
func TestContract_SiblingFreezeOnInstanceFailure(t *testing.T) {
	testx.XFail(t, "#3", func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish 0")
		e.mustOK(e.publish(id, "it_payment_done"), "publish 1")
		e.mustOK(e.fail(id, "it_order_created", "it_payments"), "fail 0")

		if got := e.instanceState(id); got != "FAILED" {
			t.Fatalf("instance state = %q, want FAILED", got)
		}
		if got := e.taskState(id, "1"); got != "PUBLISHED" {
			t.Errorf("sibling state = %q, want PUBLISHED (frozen)", got)
		}
		if _, has := e.deadline(id, "1"); has {
			t.Errorf("sibling deadline still armed after instance failed")
		}

		// Even if a stale deadline existed, the reaper must not act on a
		// terminal instance.
		e.clock.Advance(30 * time.Second)
		e.tick()
		if got := e.taskState(id, "1"); got != "PUBLISHED" {
			t.Errorf("sibling state after reaper = %q, want PUBLISHED", got)
		}
		if got := len(e.sink.Calls()); got != 1 {
			t.Errorf("webhook calls = %d, want exactly 1 per failed instance", got)
		}
	})
}

// Contract A1: the archive row's state equals the final Redis state, and
// there is exactly one row. Passes today for the sequential case.
func TestContract_ArchiveMatchesRedis(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)

		done := e.start()
		e.mustOK(e.publish(done, "it_order_created"), "publish")
		e.mustOK(e.consume(done, "it_order_created", "it_payments"), "consume")
		e.mustOK(e.publish(done, "it_payment_done"), "publish")
		e.mustOK(e.consume(done, "it_payment_done", "it_shipping"), "consume")

		broken := e.start()
		e.mustOK(e.publish(broken, "it_order_created"), "publish")
		e.mustOK(e.fail(broken, "it_order_created", "it_payments"), "fail")

		for _, id := range []string{done, broken} {
			got := e.waitArchived(id, 5*time.Second)
			if want := e.instanceState(id); got != want {
				t.Errorf("%s: archived %q, redis %q", id, got, want)
			}
			if n := e.archivedRows(id); n != 1 {
				t.Errorf("%s: %d archive rows, want 1", id, n)
			}
		}
	})
}

// Contract I3/I4: after the instance is COMPLETED, the terminal state is
// final. A late fail report on a completed task is refused and fires nothing.
func TestContract_CompletedInstanceRefusesLateFail(t *testing.T) {
	testx.XFail(t, "#2", func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")
		e.mustOK(e.consume(id, "it_order_created", "it_payments"), "consume")
		e.mustOK(e.publish(id, "it_payment_done"), "publish")
		e.mustOK(e.consume(id, "it_payment_done", "it_shipping"), "consume")
		if got := e.waitArchived(id, 5*time.Second); got != "COMPLETED" {
			t.Fatalf("archived = %q", got)
		}

		if w := e.report(id, "fail", "it_payment_done", "it_shipping", "true", ""); accepted(w) {
			t.Errorf("late retry fail accepted: %d %s", w.Code, w.Body.String())
		}
		if got := e.instanceState(id); got != "COMPLETED" {
			t.Errorf("instance state = %q, want COMPLETED", got)
		}
		if got := e.archived(id); got != "COMPLETED" {
			t.Errorf("archived state = %q, want COMPLETED", got)
		}
		if got := len(e.sink.Calls()); got != 0 {
			t.Errorf("webhook calls = %d, want 0", got)
		}
	})
}
