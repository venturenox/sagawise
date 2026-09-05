//go:build integration

package instance_engine

import (
	"net/http"
	"testing"

	"wtfsaga/internal/testx"
)

// Contract §9 (D4, D6): status codes and JSON bodies with stable error codes.
func TestContract_StatusCodesAndErrorBodies(t *testing.T) {
	testx.XFail(t, "D4/D6", func(t testx.T) {
		e := newEnv(t)
		id := e.start()

		check := func(what string, w interface {
			Result() *http.Response
		}, wantCode int, wantErr string) {
			t.Helper()
			res := w.Result()
			if res.StatusCode != wantCode {
				t.Errorf("%s: status %d, want %d", what, res.StatusCode, wantCode)
			}
			if ct := res.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("%s: Content-Type %q, want application/json", what, ct)
			}
		}

		w := e.report("nope", "publish", "it_order_created", "", "false", `{}`)
		check("unknown instance", w, http.StatusNotFound, "INSTANCE_NOT_FOUND")
		if got := jsonBody(t, w)["error"]; got != "INSTANCE_NOT_FOUND" {
			t.Errorf("unknown instance: error = %v", got)
		}

		w = e.consume(id, "it_order_created", "it_payments")
		check("consume before publish", w, http.StatusConflict, "TASK_NOT_PUBLISHED")
		if got := jsonBody(t, w)["error"]; got != "TASK_NOT_PUBLISHED" {
			t.Errorf("consume before publish: error = %v", got)
		}

		w = e.publish(id, "it_order_created")
		check("publish", w, http.StatusOK, "")
		body := jsonBody(t, w)
		if body["workflow_instance_id"] != id || body["idempotent"] != false {
			t.Errorf("publish success body = %v", body)
		}

		w = e.publish(id, "it_order_created")
		check("duplicate publish", w, http.StatusConflict, "TASK_ALREADY_PUBLISHED")
		if got := jsonBody(t, w)["error"]; got != "TASK_ALREADY_PUBLISHED" {
			t.Errorf("duplicate publish: error = %v", got)
		}

		w = e.consume(id, "it_order_created", "nobody")
		check("unknown task", w, http.StatusNotFound, "TASK_NOT_FOUND")

		w = e.report(id, "explode", "it_order_created", "", "false", "")
		check("bad action", w, http.StatusBadRequest, "INVALID_PARAM")

		w = e.report(id, "publish", "it_order_created", "", "false", `not json`)
		check("bad body", w, http.StatusBadRequest, "INVALID_BODY")
	})
}

// Contract §4: is_retry must parse strictly.
func TestContract_IsRetryStrictParsing(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		for _, bad := range []string{"maybe", "1", "0", "t", "f", "yes"} {
			w := e.report(id, "publish", "it_order_created", "", bad, `{}`)
			if w.Code != http.StatusBadRequest {
				t.Errorf("is_retry=%q: status %d, want 400", bad, w.Code)
			}
			if got := e.taskState(id, "0"); got != "PENDING" {
				t.Errorf("is_retry=%q changed state to %q", bad, got)
			}
		}
		for _, ok := range []string{"true", "TRUE", "True", "false", "FALSE", "False"} {
			fresh := e.start()
			if w := e.report(fresh, "publish", "it_order_created", "", ok, `{}`); !accepted(w) {
				t.Errorf("is_retry=%q rejected: %d %s", ok, w.Code, w.Body.String())
			}
		}
	})
}

// Contract D5: an infrastructure error is a 500, never a business answer. (#7)
func TestContract_InfraErrorIs500(t *testing.T) {
	testx.XFail(t, "#7", func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")

		e.faults.FailNext("json.get", 1)
		w := e.consume(id, "it_order_created", "it_payments")
		if e.faults.Hits() != 1 {
			t.Fatalf("fault not exercised")
		}
		if w.Code != http.StatusInternalServerError {
			t.Errorf("consume during redis error: %d %s, want 500", w.Code, w.Body.String())
		}
		if got := e.taskState(id, "0"); got != "PUBLISHED" {
			t.Errorf("task state = %q after a failed request, want PUBLISHED", got)
		}
	})
}

// Contract D7: the get endpoint takes an instance id, not a Redis key.
func TestContract_GetByInstanceID(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := e.start()

		w := e.do(e.eng.GetWorkflowInstance, http.MethodGet, "/workflow_instances/get?workflow_instance_id="+id, "")
		if w.Code != http.StatusOK {
			t.Errorf("get by id: %d %s", w.Code, w.Body.String())
		} else if got := jsonBody(t, w)["name"]; got != itWorkflow {
			t.Errorf("get by id: name = %v", got)
		}

		w = e.do(e.eng.GetWorkflowInstance, http.MethodGet, "/workflow_instances/get?doc_key=workflow_template:"+itWorkflow, "")
		if w.Code == http.StatusOK {
			t.Errorf("arbitrary key read succeeded: %s", w.Body.String())
		}
	})
}
