//go:build integration

package instance_engine

import (
	"net/url"
	"testing"

	"wtfsaga/internal/testx"
)

// Contract T2: task resolution never looks inside payloads and query values
// are data, not query syntax. (#12, #13)

// A consume whose event_name carries JSONPath syntax must not match any task.
func TestContract_JSONPathInjection_EventName(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")

		inject := url.QueryEscape("it_zzz' || @.topic!='zzz")
		w := e.report(id, "consume", inject, "it_payments", "false", "")
		if accepted(w) {
			t.Errorf("injected consume accepted: %d %s", w.Code, w.Body.String())
		}
		if got := e.taskState(id, "0"); got != "PUBLISHED" {
			t.Errorf("task 0 state = %q, want PUBLISHED (untouched)", got)
		}
	})
}

func TestContract_JSONPathInjection_ServiceName(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")

		inject := url.QueryEscape("nobody' || @.to!='zzz")
		w := e.report(id, "consume", "it_order_created", inject, "false", "")
		if accepted(w) {
			t.Errorf("injected consume accepted: %d %s", w.Code, w.Body.String())
		}
		if got := e.taskState(id, "0"); got != "PUBLISHED" {
			t.Errorf("task 0 state = %q, want PUBLISHED (untouched)", got)
		}
	})
}

// An apostrophe in a real-looking name must be a clean 404, not a swallowed
// query error with side effects. Passes today.
func TestContract_ApostropheInEventNameIsNotFound(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")

		w := e.report(id, "consume", url.QueryEscape("it_order_created'"), "it_payments", "false", "")
		if accepted(w) {
			t.Errorf("consume with apostrophe accepted: %d", w.Code)
		}
		if got := e.taskState(id, "0"); got != "PUBLISHED" {
			t.Errorf("task 0 state = %q, want PUBLISHED", got)
		}
		if _, has := e.deadline(id, "0"); !has {
			t.Errorf("deadline lost on a rejected report")
		}
	})
}

// A payload that carries task-shaped keys must not be resolvable as a task.
// The bogus (topic, to) pair matches no real task, so only the payload
// matches the recursive-descent query, and a consume for a task that does
// not exist completes task 0.
func TestContract_PayloadShadowing_StringIndex(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.report(id, "publish", "it_order_created", "", "false",
			`{"topic":"it_bogus","to":"it_nobody","index":"0"}`), "publish")

		w := e.consume(id, "it_bogus", "it_nobody")
		if accepted(w) {
			t.Errorf("consume of a nonexistent task accepted via payload: %d %s", w.Code, w.Body.String())
		}
		if got := e.taskState(id, "0"); got != "PUBLISHED" {
			t.Errorf("task 0 state = %q, want PUBLISHED (hijacked)", got)
		}
		if got := e.taskState(id, "1"); got != "PENDING" {
			t.Errorf("task 1 state = %q, want PENDING", got)
		}
	})
}

// A non-string index in such a payload used to decode to "" and produce the
// path `$..state`, flipping every task and the workflow once the retry flag
// bypassed the state gate. Since phase 5 a decode error is an error, not an
// empty string, so this is a 404 with no side effects. The string-index
// variant above still needs task resolution in Go (#12, phase 6).
func TestContract_PayloadShadowing_NonStringIndex(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.report(id, "publish", "it_order_created", "", "false",
			`{"topic":"it_bogus","to":"it_nobody","index":0}`), "publish")

		w := e.report(id, "consume", "it_bogus", "it_nobody", "true", "")
		if accepted(w) {
			t.Errorf("consume of a nonexistent task accepted: %d %s", w.Code, w.Body.String())
		}
		if got := e.taskState(id, "0"); got != "PUBLISHED" {
			t.Errorf("task 0 state = %q, want PUBLISHED", got)
		}
		if got := e.taskState(id, "1"); got != "PENDING" {
			t.Errorf("task 1 state = %q, want PENDING", got)
		}
		if got := e.instanceState(id); got != "PENDING" {
			t.Errorf("instance state = %q, want PENDING", got)
		}
		if got := e.archived(id); got != "" {
			t.Errorf("a bogus instance was archived as %q", got)
		}
	})
}

// ---- fuzzers ----
// Invariant that must hold on any input, today and after the rewrite: the
// engine never answers 5xx to a well-formed-but-hostile query value, never
// panics, and the instance document stays readable. Contract-level outcomes
// for hostile values are asserted by the tests above.

func FuzzUpdateInstanceQueryValues(f *testing.F) {
	for _, s := range []string{
		"it_order_created", "it_payments", "x' || @.topic!='zzz", "it_order_created'", "*", "$..state",
		"'", "\"", "\\", "(", ")", "[?(@.topic)]", "a b", "üñí", "",
	} {
		f.Add(s, s)
	}
	f.Fuzz(func(t *testing.T, topic, service string) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")
		for _, action := range []string{"publish", "consume", "fail"} {
			w := e.report(id, action, url.QueryEscape(topic), url.QueryEscape(service), "false", `{"n":1}`)
			if w.Code >= 500 {
				t.Errorf("%s(%q, %q) -> %d %s", action, topic, service, w.Code, w.Body.String())
			}
		}
		_ = e.doc(id) // still a readable document
	})
}

func FuzzPublishBody(f *testing.F) {
	for _, s := range []string{
		`{}`, `{"n":1}`, `not json`, `[]`, `null`, `{"index":"0"}`, `{"index":0}`,
		`{"topic":"it_payment_done","to":"it_shipping","index":"0"}`, `{"state":"COMPLETED"}`, `{"$..state":1}`, "",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		e := newEnv(t)
		id := e.start()
		w := e.report(id, "publish", "it_order_created", "", "false", body)
		if w.Code >= 500 {
			t.Errorf("publish body %q -> %d %s", body, w.Code, w.Body.String())
		}
		_ = e.doc(id)
	})
}
