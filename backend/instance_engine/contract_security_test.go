//go:build integration

package instance_engine

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"wtfsaga/httpsec"
	"wtfsaga/internal/testx"
	"wtfsaga/webhooksig"
)

// Phase 8 (docs/threat-model.md). Authentication and CORS are middleware in
// package httpsec with their own unit tests and a startup test against the
// real binary; here are the pieces the engine itself owes the contract.

// W6: with a secret configured, a failure webhook carries a signature the
// receiver can verify against the raw body and its own clock.
func TestContract_WebhookIsSigned(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		secret := []byte("it-webhook-secret")
		e.eng.WebhookSecret = secret
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")
		e.mustOK(e.fail(id, "it_order_created", "it_payments"), "fail")

		calls := e.sink.Calls()
		if len(calls) != 1 {
			t.Fatalf("webhook calls = %d, want 1", len(calls))
		}
		c := calls[0]
		ts := c.Header.Get(webhooksig.HeaderTimestamp)
		sig := c.Header.Get(webhooksig.HeaderSignature)
		if ts == "" || !strings.HasPrefix(sig, "v1=") {
			t.Fatalf("headers: timestamp=%q signature=%q", ts, sig)
		}
		// The receiver's clock is the fake clock, which stamped the delivery.
		if err := webhooksig.Verify(secret, ts, sig, c.Raw, e.clock.Now(), 0); err != nil {
			t.Errorf("signature does not verify: %v", err)
		}
		if err := webhooksig.Verify([]byte("other"), ts, sig, c.Raw, e.clock.Now(), 0); err == nil {
			t.Errorf("signature verifies under a different secret")
		}
		if err := webhooksig.Verify(secret, ts, sig, c.Raw, e.clock.Now().Add(10*time.Minute), 0); err == nil {
			t.Errorf("a 10-minute-old delivery still verifies (replay)")
		}
	})
}

// No secret configured: the delivery is unsigned and carries no signature
// headers at all (the receiver must not be given something that looks
// verifiable but is not).
func TestContract_WebhookUnsignedWithoutSecret(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")
		e.mustOK(e.fail(id, "it_order_created", "it_payments"), "fail")
		calls := e.sink.Calls()
		if len(calls) != 1 {
			t.Fatalf("webhook calls = %d, want 1", len(calls))
		}
		for _, h := range []string{webhooksig.HeaderTimestamp, webhooksig.HeaderSignature} {
			if v := calls[0].Header.Get(h); v != "" {
				t.Errorf("%s = %q on an unsigned delivery", h, v)
			}
		}
	})
}

// §9: a publish body past the cap is 413 PAYLOAD_TOO_LARGE, not stored,
// and the task stays PENDING so a correctly sized retry works.
func TestContract_PublishBodyCap(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		capped := httpsec.MaxBody(64, http.HandlerFunc(e.eng.UpdateInstance))

		big := `{"pad":"` + strings.Repeat("x", 100) + `"}`
		w := e.do(capped.ServeHTTP, http.MethodPost,
			"/update_instance?workflow_instance_id="+id+"&event_name=it_order_created&action_type=publish&is_retry=false", big)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized publish: %d %s, want 413", w.Code, w.Body.String())
		}
		if got := jsonBody(t, w)["error"]; got != "PAYLOAD_TOO_LARGE" {
			t.Errorf("error = %v, want PAYLOAD_TOO_LARGE", got)
		}
		if st := e.taskState(id, "0"); st != "PENDING" {
			t.Errorf("task state after a refused publish = %s, want PENDING", st)
		}

		w = e.do(capped.ServeHTTP, http.MethodPost,
			"/update_instance?workflow_instance_id="+id+"&event_name=it_order_created&action_type=publish&is_retry=false", `{"ok":1}`)
		if w.Code != http.StatusOK {
			t.Fatalf("publish under the cap: %d %s", w.Code, w.Body.String())
		}
	})
}
