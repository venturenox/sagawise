package instance_engine

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wtfsaga/utils"
)

// fakeClock is a Clock tests can set and advance explicitly.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{t: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestFakeClockAdvances(t *testing.T) {
	start := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	c := newFakeClock(start)
	c.Advance(90 * time.Second)
	if got := c.Now(); !got.Equal(start.Add(90 * time.Second)) {
		t.Fatalf("Now() = %v, want %v", got, start.Add(90*time.Second))
	}
}

func TestNewEngineDefaults(t *testing.T) {
	e := New(nil, nil)
	if _, ok := e.Clock.(RealClock); !ok {
		t.Errorf("Clock = %T, want RealClock", e.Clock)
	}
	if reg, ok := e.Services.(FileRegistry); !ok || reg.Path != "services.json" {
		t.Errorf("Services = %#v, want FileRegistry{services.json}", e.Services)
	}
	if e.HTTPClient == nil || e.HTTPClient.Timeout != WebhookTimeout {
		t.Errorf("HTTPClient = %+v, want a client with Timeout %v (#5)", e.HTTPClient, WebhookTimeout)
	}
}

func TestValidateServices(t *testing.T) {
	wfs := []utils.Workflow{{Name: "f", Tasks: []utils.Task{{Topic: "t", From: "a", To: "b", Timeout: 1}, {Topic: "u", From: "b", To: "c", Timeout: 1}}}}
	if err := ValidateServices(MapRegistry{"a": "http://a/fail", "b": "http://b/fail"}, wfs); err != nil {
		t.Errorf("all publishers registered: %v", err)
	}
	err := ValidateServices(MapRegistry{"a": "http://a/fail"}, wfs)
	if err == nil || !strings.Contains(err.Error(), `"b"`) {
		t.Errorf("unregistered publisher b: err = %v", err)
	}
	if err := ValidateServices(FileRegistry{Path: filepath.Join(t.TempDir(), "nope.json")}, wfs); err == nil {
		t.Error("missing services file: want error")
	}
}

func TestParseRetry(t *testing.T) {
	for _, v := range []string{"true", "TRUE", "True"} {
		if got, ok := parseRetry(v); !ok || !got {
			t.Errorf("parseRetry(%q) = %v, %v", v, got, ok)
		}
	}
	for _, v := range []string{"false", "FALSE", "False"} {
		if got, ok := parseRetry(v); !ok || got {
			t.Errorf("parseRetry(%q) = %v, %v", v, got, ok)
		}
	}
	for _, v := range []string{"", "1", "0", "t", "f", "yes", "maybe"} {
		if _, ok := parseRetry(v); ok {
			t.Errorf("parseRetry(%q) accepted", v)
		}
	}
}

func TestEscapeTag(t *testing.T) {
	cases := map[string]string{
		"order_flow":     "order_flow",
		"order-flow":     `order\-flow`,
		"a b.c":          `a\ b\.c`,
		"x{y}|z":         `x\{y\}\|z`,
		"üñí":            "üñí",
		"COMPLETED":      "COMPLETED",
		"it_test_flow-1": `it_test_flow\-1`,
	}
	for in, want := range cases {
		if got := escapeTag(in); got != want {
			t.Errorf("escapeTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPageParam(t *testing.T) {
	if n, err := pageParam("", 50, 1000); err != nil || n != 50 {
		t.Errorf("default: %d, %v", n, err)
	}
	if n, err := pageParam("7", 50, 1000); err != nil || n != 7 {
		t.Errorf("explicit: %d, %v", n, err)
	}
	for _, bad := range []string{"-1", "x", "1001", "1.5"} {
		if _, err := pageParam(bad, 50, 1000); err == nil {
			t.Errorf("pageParam(%q) accepted", bad)
		}
	}
	if n, err := pageParam("5000", 0, 0); err != nil || n != 5000 {
		t.Errorf("unbounded: %d, %v", n, err)
	}
}

func TestGetWorkflowInstanceRejectsKeys(t *testing.T) {
	e := New(nil, nil)
	for _, bad := range []string{"workflow_template:x", "a:b", "../x", "", "x y"} {
		r := httptest.NewRequest(http.MethodGet, "/workflow_instances/get?workflow_instance_id="+url.QueryEscape(bad), nil)
		w := httptest.NewRecorder()
		e.GetWorkflowInstance(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("id %q: status %d, want 400 before any Redis access", bad, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/workflow_instances/get?doc_key=workflow_instance:abc", nil)
	w := httptest.NewRecorder()
	e.GetWorkflowInstance(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("doc_key: status %d, want 400 (D7: doc_key is gone)", w.Code)
	}
}

func TestFileRegistry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "services.json")
	if err := os.WriteFile(path, []byte(`[{"service_name":"orders","failure_url":"http://orders:4010/fail"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := FileRegistry{Path: path}

	if got, err := reg.FailureURL("orders"); err != nil || got != "http://orders:4010/fail" {
		t.Errorf("FailureURL(orders) = %q, %v", got, err)
	}
	if got, err := reg.FailureURL("unknown"); err != nil || got != "" {
		t.Errorf("FailureURL(unknown) = %q, %v; want empty, nil", got, err)
	}

	missing := FileRegistry{Path: filepath.Join(dir, "nope.json")}
	if _, err := missing.FailureURL("orders"); err == nil {
		t.Error("missing file: want error, got nil")
	}

	if err := os.WriteFile(path, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.FailureURL("orders"); err == nil {
		t.Error("malformed file: want error, got nil")
	}
}

func TestMapRegistry(t *testing.T) {
	reg := MapRegistry{"orders": "http://x/fail"}
	if got, _ := reg.FailureURL("orders"); got != "http://x/fail" {
		t.Errorf("got %q", got)
	}
	if got, _ := reg.FailureURL("nope"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestGenerateID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := generateID()
		if len(id) != 20 {
			t.Fatalf("len(%q) = %d, want 20", id, len(id))
		}
		for _, r := range id {
			if !strings.ContainsRune(string(letters), r) {
				t.Fatalf("id %q contains %q outside the alphabet", id, r)
			}
		}
		if seen[id] {
			t.Fatalf("duplicate id %q in 100 draws", id)
		}
		seen[id] = true
	}
}

func TestDeadlineMemberAndInstanceKey(t *testing.T) {
	if got := deadlineMember("abc", "3"); got != "abc:3" {
		t.Errorf("deadlineMember = %q", got)
	}
	if got := instanceKey("abc"); got != "workflow_instance:abc" {
		t.Errorf("instanceKey = %q", got)
	}
}

func TestRequireParams(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/update_instance?action_type=publish", nil)
	w := httptest.NewRecorder()

	if requireParams(r, w, "action_type", "workflow_instance_id", "event_name") {
		t.Fatal("requireParams returned true with missing params")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "workflow_instance_id required") || !strings.Contains(body, "event_name required") {
		t.Errorf("body = %q, want both missing params listed", body)
	}
	if strings.Contains(body, "action_type required") {
		t.Errorf("body = %q lists a param that was present", body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options: nosniff")
	}

	w = httptest.NewRecorder()
	if !requireParams(r, w, "action_type") {
		t.Fatal("requireParams returned false with all params present")
	}
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Errorf("success path wrote status %d body %q", w.Code, w.Body.String())
	}
}
