package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetup(t *testing.T) {
	var buf bytes.Buffer
	l, err := Setup(&buf, "json", "warn")
	if err != nil {
		t.Fatal(err)
	}
	l.Info("hidden")
	l.Warn("shown", "k", "v")
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("not one JSON line: %q: %v", buf.String(), err)
	}
	if line["msg"] != "shown" || line["k"] != "v" || line["level"] != "WARN" {
		t.Errorf("line = %v", line)
	}

	// The standard library's log package is routed through the handler.
	buf.Reset()
	if _, err := Setup(&buf, "json", "info"); err != nil {
		t.Fatal(err)
	}
	slog.Default().Info("via slog")
	if !strings.Contains(buf.String(), `"msg":"via slog"`) {
		t.Errorf("default logger not installed: %q", buf.String())
	}

	buf.Reset()
	if _, err := Setup(&buf, "text", ""); err != nil {
		t.Fatal(err)
	}
	slog.Info("text line")
	if !strings.Contains(buf.String(), "msg=\"text line\"") {
		t.Errorf("text format: %q", buf.String())
	}

	for _, bad := range [][2]string{{"xml", "info"}, {"json", "loud"}} {
		if _, err := Setup(&buf, bad[0], bad[1]); err == nil {
			t.Errorf("Setup(%q, %q) accepted", bad[0], bad[1])
		}
	}
}

func TestContext(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, nil)).With("instance_id", "abc")
	ctx := NewContext(context.Background(), l)
	From(ctx, nil).Info("hello")
	if !strings.Contains(buf.String(), `"instance_id":"abc"`) {
		t.Errorf("logger from context lost its attributes: %q", buf.String())
	}
	fallback := slog.New(slog.NewJSONHandler(&buf, nil)).With("fallback", true)
	buf.Reset()
	From(context.Background(), fallback).Info("x")
	if !strings.Contains(buf.String(), `"fallback":true`) {
		t.Errorf("fallback not used: %q", buf.String())
	}
	if From(context.Background(), nil) == nil {
		t.Error("nil fallback must yield slog.Default()")
	}
}

// One access line per request, with request_id and instance_id, at a level
// that follows the status; the handler sees the same logger through its
// context.
func TestMiddleware(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	m := Middleware{Logger: l, InstanceParam: "workflow_instance_id", Params: []string{"action_type"}, Quiet: []string{"/live"}}

	h := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		From(r.Context(), nil).Info("inside")
		switch r.URL.Path {
		case "/refuse":
			http.Error(w, "no", http.StatusConflict)
		case "/boom":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))

	lines := func() []map[string]any {
		var out []map[string]any
		for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			var m map[string]any
			if err := json.Unmarshal([]byte(raw), &m); err != nil {
				t.Fatalf("line %q is not JSON: %v", raw, err)
			}
			out = append(out, m)
		}
		return out
	}

	// Instance id and listed params are on the access line and on lines
	// logged inside the handler; the request id is generated and echoed.
	r := httptest.NewRequest(http.MethodPost, "/update_instance?workflow_instance_id=abc123&action_type=publish&event_name=x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	got := lines()
	if len(got) != 2 {
		t.Fatalf("lines = %d, want 2 (inside + access): %s", len(got), buf.String())
	}
	inside, access := got[0], got[1]
	reqID := w.Header().Get(HeaderRequestID)
	if reqID == "" {
		t.Fatal("no X-Request-Id on the response")
	}
	for name, line := range map[string]map[string]any{"inside": inside, "access": access} {
		if line["request_id"] != reqID {
			t.Errorf("%s line request_id = %v, want %q", name, line["request_id"], reqID)
		}
		if line["instance_id"] != "abc123" {
			t.Errorf("%s line instance_id = %v", name, line["instance_id"])
		}
		if line["action_type"] != "publish" {
			t.Errorf("%s line action_type = %v", name, line["action_type"])
		}
		if _, ok := line["event_name"]; ok {
			t.Errorf("%s line carries event_name, which was not listed", name)
		}
	}
	if access["msg"] != "request" || access["level"] != "INFO" || access["status"] != float64(200) ||
		access["method"] != "POST" || access["path"] != "/update_instance" || access["bytes"] != float64(11) {
		t.Errorf("access line = %v", access)
	}
	if _, ok := access["duration_ms"]; !ok {
		t.Errorf("access line has no duration_ms: %v", access)
	}

	// A caller's request id is kept; 4xx is WARN, 5xx is ERROR.
	for _, tc := range []struct {
		path, level string
		status      float64
	}{{"/refuse", "WARN", 409}, {"/boom", "ERROR", 500}} {
		buf.Reset()
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.Header.Set(HeaderRequestID, "caller-7")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		access := lines()[1]
		if access["level"] != tc.level || access["status"] != tc.status || access["request_id"] != "caller-7" {
			t.Errorf("%s: access line = %v", tc.path, access)
		}
		if w.Header().Get(HeaderRequestID) != "caller-7" {
			t.Errorf("%s: request id not echoed", tc.path)
		}
	}

	// A successful probe is a debug line; a failing one is not hidden.
	buf.Reset()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/live", nil))
	if access := lines()[1]; access["level"] != "DEBUG" {
		t.Errorf("quiet path logged at %v, want DEBUG", access["level"])
	}

	// A query value cannot forge a line: control characters are dropped and
	// the value is bounded.
	buf.Reset()
	long := strings.Repeat("x", 500)
	r = httptest.NewRequest(http.MethodGet, "/x?workflow_instance_id=a%0Ab%0D%7F"+long, nil)
	h.ServeHTTP(httptest.NewRecorder(), r)
	got = lines()
	if len(got) != 2 {
		t.Fatalf("a newline in a parameter produced %d lines: %s", len(got), buf.String())
	}
	id, _ := got[1]["instance_id"].(string)
	if strings.ContainsAny(id, "\n\r\x7f") || len(id) > maxAttrLen {
		t.Errorf("instance_id not sanitized: %q", id)
	}
}
