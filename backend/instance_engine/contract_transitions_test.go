//go:build integration

package instance_engine

import (
	"fmt"
	"testing"
	"time"

	"wtfsaga/internal/testx"
)

// Contract §2, §3, §4: every (state, action, is_retry) cell of the task
// state machine, plus the terminal-instance rows of I4/I5. One instance per
// row. Rows today's code cannot pass carry the audit finding in `known`.

type setup string

const (
	pending    setup = "task0 PENDING"
	published  setup = "task0 PUBLISHED"
	completed  setup = "task0 COMPLETED"
	failedTask setup = "task0 FAILED (instance FAILED)"
	// sibling rows act on task 1 after task 0 failed the instance
	sibPending   setup = "instance FAILED, task1 PENDING"
	sibPublished setup = "instance FAILED, task1 PUBLISHED"
	// instance completed: act on task 1
	instCompleted setup = "instance COMPLETED"
)

type trow struct {
	setup    setup
	action   string
	retry    bool
	accept   bool
	end      string // expected state of the target task afterwards
	deadline string // "armed" (present), "rearmed" (present, later than before), "same", "none"
	webhooks int    // expected total webhook calls afterwards
	known    string // audit finding if today's code fails this row
}

// prime drives a fresh instance into the row's setup and returns the
// instance id, the target task index, its topic and consuming service.
func prime(e *env, s setup) (id, index, topic, service string) {
	id = e.start()
	switch s {
	case pending:
	case published:
		e.mustOK(e.publish(id, "it_order_created"), "prime publish")
	case completed:
		e.mustOK(e.publish(id, "it_order_created"), "prime publish")
		e.mustOK(e.consume(id, "it_order_created", "it_payments"), "prime consume")
	case failedTask:
		e.mustOK(e.publish(id, "it_order_created"), "prime publish")
		e.mustOK(e.fail(id, "it_order_created", "it_payments"), "prime fail")
	case sibPending:
		e.mustOK(e.publish(id, "it_order_created"), "prime publish")
		e.mustOK(e.fail(id, "it_order_created", "it_payments"), "prime fail")
		return id, "1", "it_payment_done", "it_shipping"
	case sibPublished:
		e.mustOK(e.publish(id, "it_order_created"), "prime publish 0")
		e.mustOK(e.publish(id, "it_payment_done"), "prime publish 1")
		e.mustOK(e.fail(id, "it_order_created", "it_payments"), "prime fail")
		return id, "1", "it_payment_done", "it_shipping"
	case instCompleted:
		e.mustOK(e.publish(id, "it_order_created"), "prime publish 0")
		e.mustOK(e.consume(id, "it_order_created", "it_payments"), "prime consume 0")
		e.mustOK(e.publish(id, "it_payment_done"), "prime publish 1")
		e.mustOK(e.consume(id, "it_payment_done", "it_shipping"), "prime consume 1")
		return id, "1", "it_payment_done", "it_shipping"
	}
	return id, "0", "it_order_created", "it_payments"
}

func transitionRows() []trow {
	f, t := false, true
	return []trow{
		// ---- task 0 PENDING ----
		{pending, "publish", f, true, "PUBLISHED", "armed", 0, ""},
		{pending, "publish", t, true, "PUBLISHED", "armed", 0, ""},
		{pending, "consume", f, false, "PENDING", "none", 0, ""},
		{pending, "consume", t, false, "PENDING", "none", 0, "#2"},
		{pending, "fail", f, false, "PENDING", "none", 0, ""},
		{pending, "fail", t, false, "PENDING", "none", 0, "#2"},
		// ---- task 0 PUBLISHED ----
		{published, "publish", f, false, "PUBLISHED", "same", 0, ""},
		{published, "publish", t, true, "PUBLISHED", "rearmed", 0, ""}, // D2
		{published, "consume", f, true, "COMPLETED", "none", 0, ""},
		{published, "consume", t, true, "COMPLETED", "none", 0, ""},
		{published, "fail", f, true, "FAILED", "none", 1, ""},
		{published, "fail", t, true, "FAILED", "none", 1, ""},
		// ---- task 0 COMPLETED ----
		{completed, "publish", f, false, "COMPLETED", "none", 0, ""},
		{completed, "publish", t, true, "COMPLETED", "none", 0, "#2"},
		{completed, "consume", f, false, "COMPLETED", "none", 0, ""},
		{completed, "consume", t, true, "COMPLETED", "none", 0, ""},
		{completed, "fail", f, false, "COMPLETED", "none", 0, ""},
		{completed, "fail", t, false, "COMPLETED", "none", 0, "#2"},
		// ---- task 0 FAILED (instance terminal) ----
		{failedTask, "publish", f, false, "FAILED", "none", 1, ""},
		{failedTask, "publish", t, true, "FAILED", "none", 1, "#2"},
		{failedTask, "consume", f, false, "FAILED", "none", 1, ""},
		{failedTask, "consume", t, false, "FAILED", "none", 1, "#2"},
		{failedTask, "fail", f, false, "FAILED", "none", 1, ""},
		{failedTask, "fail", t, true, "FAILED", "none", 1, "#2"},
		// ---- instance FAILED, sibling task 1 PENDING (I4) ----
		{sibPending, "publish", f, false, "PENDING", "none", 1, "#3"},
		{sibPending, "publish", t, false, "PENDING", "none", 1, "#3"},
		{sibPending, "consume", f, false, "PENDING", "none", 1, ""},
		{sibPending, "consume", t, false, "PENDING", "none", 1, "#2"},
		{sibPending, "fail", f, false, "PENDING", "none", 1, ""},
		{sibPending, "fail", t, false, "PENDING", "none", 1, "#2"},
		// ---- instance FAILED, sibling task 1 PUBLISHED and frozen (I4, I5) ----
		{sibPublished, "publish", f, false, "PUBLISHED", "none", 1, "#3"},
		{sibPublished, "publish", t, true, "PUBLISHED", "none", 1, "#3"},
		{sibPublished, "consume", f, false, "PUBLISHED", "none", 1, "#3"},
		{sibPublished, "consume", t, false, "PUBLISHED", "none", 1, "#3"},
		{sibPublished, "fail", f, false, "PUBLISHED", "none", 1, "#3"},
		{sibPublished, "fail", t, false, "PUBLISHED", "none", 1, "#3"},
		// ---- instance COMPLETED, task 1 COMPLETED (I4 + duplicates) ----
		{instCompleted, "publish", f, false, "COMPLETED", "none", 0, ""},
		{instCompleted, "publish", t, true, "COMPLETED", "none", 0, "#2"},
		{instCompleted, "consume", f, false, "COMPLETED", "none", 0, ""},
		{instCompleted, "consume", t, true, "COMPLETED", "none", 0, ""},
		{instCompleted, "fail", f, false, "COMPLETED", "none", 0, ""},
		{instCompleted, "fail", t, false, "COMPLETED", "none", 0, "#2"},
	}
}

func TestContract_Transitions(t *testing.T) {
	for _, row := range transitionRows() {
		row := row
		name := fmt.Sprintf("%s/%s/retry=%v", row.setup, row.action, row.retry)
		t.Run(name, func(t *testing.T) {
			body := func(t testx.T) {
				e := newEnv(t)
				id, index, topic, service := prime(e, row.setup)
				before, hadDeadline := e.deadline(id, index)
				e.clock.Advance(time.Second) // so a re-armed deadline is distinguishable

				retry := "false"
				if row.retry {
					retry = "true"
				}
				svc := service
				if row.action == "publish" {
					svc = ""
				}
				w := e.report(id, row.action, topic, svc, retry, `{"n":2}`)

				if got := accepted(w); got != row.accept {
					t.Errorf("accepted = %v (%d %s), want %v", got, w.Code, w.Body.String(), row.accept)
				}
				if got := e.taskState(id, index); got != row.end {
					t.Errorf("task %s state = %q, want %q", index, got, row.end)
				}
				after, has := e.deadline(id, index)
				switch row.deadline {
				case "none":
					if has {
						t.Errorf("deadline present, want none")
					}
				case "armed":
					if !has {
						t.Errorf("deadline absent, want armed")
					}
				case "same":
					if !has || !hadDeadline || after != before {
						t.Errorf("deadline = %v (had %v before=%v), want unchanged", after, hadDeadline, before)
					}
				case "rearmed":
					if !has || after <= before {
						t.Errorf("deadline = %v, want later than %v", after, before)
					}
					if p := e.taskPayload(id, index); p["n"] != float64(2) {
						t.Errorf("payload = %v, want replaced by the retried publish", p)
					}
				}
				if got := len(e.sink.Calls()); got != row.webhooks {
					t.Errorf("webhook calls = %d, want %d", got, row.webhooks)
				}
				// The instance never leaves a terminal state.
				switch row.setup {
				case failedTask, sibPending, sibPublished:
					if got := e.instanceState(id); got != "FAILED" {
						t.Errorf("instance state = %q, want FAILED", got)
					}
				case instCompleted:
					if got := e.instanceState(id); got != "COMPLETED" {
						t.Errorf("instance state = %q, want COMPLETED", got)
					}
				}
			}
			if row.known != "" {
				testx.XFail(t, row.known, body)
			} else {
				testx.Run(t, body)
			}
		})
	}
}
