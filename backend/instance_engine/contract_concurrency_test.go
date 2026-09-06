//go:build integration

package instance_engine

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"wtfsaga/internal/testx"
)

// Contract T4: two concurrent reports on one task see exactly one winner.
// consume vs fail on the same PUBLISHED task, repeated. (#1)
func TestContract_ConcurrentConsumeVsFail(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		const rounds = 30
		bothWon, failWins := 0, 0
		for i := 0; i < rounds; i++ {
			id := e.start()
			e.mustOK(e.publish(id, "it_order_created"), "publish")

			var wg sync.WaitGroup
			start := make(chan struct{})
			var wc, wf *httptest.ResponseRecorder
			wg.Add(2)
			go func() { defer wg.Done(); <-start; wc = e.consume(id, "it_order_created", "it_payments") }()
			go func() { defer wg.Done(); <-start; wf = e.fail(id, "it_order_created", "it_payments") }()
			close(start)
			wg.Wait()

			c, f := accepted(wc), accepted(wf)
			if c && f {
				bothWon++
			}
			if !c && !f {
				t.Errorf("round %d: neither consume nor fail was accepted (%d / %d)", i, wc.Code, wf.Code)
			}
			state := e.taskState(id, "0")
			switch {
			case f && state != "FAILED":
				t.Errorf("round %d: fail accepted but task is %s", i, state)
			case c && !f && state != "COMPLETED":
				t.Errorf("round %d: consume accepted but task is %s", i, state)
			}
			if f {
				failWins++
			}
		}
		if bothWon > 0 {
			t.Errorf("both consume and fail were accepted in %d/%d rounds; exactly one must win", bothWon, rounds)
		}
		if got := len(e.sink.Calls()); got != failWins {
			t.Errorf("webhook calls = %d, want one per accepted fail (%d)", got, failWins)
		}
	})
}

// Contract TO4 + guarantee 4: consume of the last task vs the reaper on the
// same overdue task. One outcome, and the archive row agrees with Redis.
func TestContract_ReaperVsConsumeLastTask(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		const rounds = 20
		for i := 0; i < rounds; i++ {
			id := e.start()
			e.mustOK(e.publish(id, "it_order_created"), "publish 0")
			e.mustOK(e.consume(id, "it_order_created", "it_payments"), "consume 0")
			e.mustOK(e.publish(id, "it_payment_done"), "publish 1")
			e.clock.Advance(21 * time.Second) // task 1 overdue

			var wg sync.WaitGroup
			start := make(chan struct{})
			var wc *httptest.ResponseRecorder
			wg.Add(2)
			go func() { defer wg.Done(); <-start; e.tick() }()
			go func() { defer wg.Done(); <-start; wc = e.consume(id, "it_payment_done", "it_shipping") }()
			close(start)
			wg.Wait()

			inst := e.instanceState(id)
			task := e.taskState(id, "1")
			switch {
			case inst == "COMPLETED" && task == "COMPLETED" && accepted(wc):
			case inst == "FAILED" && task == "FAILED" && !accepted(wc):
			default:
				t.Errorf("round %d: inconsistent outcome: instance=%s task1=%s consume=%d", i, inst, task, wc.Code)
			}
			if got := e.waitArchived(id, 5*time.Second); got != inst {
				t.Errorf("round %d: archived %q but redis %q", i, got, inst)
			}
			if n := e.archivedRows(id); n != 1 {
				t.Errorf("round %d: %d archive rows", i, n)
			}
		}
	})
}

// Contract I3/I5/W1: two sibling tasks failed concurrently. Exactly one
// moves the instance to FAILED, exactly one webhook fires, the other task
// stays frozen. (#1, #3)
func TestContract_ConcurrentSiblingFailures(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		const rounds = 20
		for i := 0; i < rounds; i++ {
			id := e.start()
			e.mustOK(e.publish(id, "it_order_created"), "publish 0")
			e.mustOK(e.publish(id, "it_payment_done"), "publish 1")
			before := len(e.sink.Calls())

			var wg sync.WaitGroup
			start := make(chan struct{})
			var w0, w1 *httptest.ResponseRecorder
			wg.Add(2)
			go func() { defer wg.Done(); <-start; w0 = e.fail(id, "it_order_created", "it_payments") }()
			go func() { defer wg.Done(); <-start; w1 = e.fail(id, "it_payment_done", "it_shipping") }()
			close(start)
			wg.Wait()

			if accepted(w0) && accepted(w1) {
				t.Errorf("round %d: both sibling fails accepted; exactly one may move the instance to FAILED", i)
			}
			failed := 0
			for _, idx := range []string{"0", "1"} {
				if e.taskState(id, idx) == "FAILED" {
					failed++
				}
			}
			if failed != 1 {
				t.Errorf("round %d: %d tasks FAILED, want exactly 1 (the other freezes)", i, failed)
			}
			if got := len(e.sink.Calls()) - before; got != 1 {
				t.Errorf("round %d: %d webhooks, want 1 per failed instance", i, got)
			}
		}
	})
}
