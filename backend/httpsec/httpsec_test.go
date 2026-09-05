package httpsec

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
})

func get(h http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Threat model T1: every state-changing and read endpoint needs a key; the
// probes do not.
func TestAPIKeys(t *testing.T) {
	a := NewAPIKeys([]string{"secret-one", " ", "secret-two"}, "/live", "/ready")
	if a.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (blank entries ignored)", a.Len())
	}
	h := a.Wrap(ok)

	cases := []struct {
		name   string
		path   string
		auth   string
		status int
	}{
		{"no header", "/start_instance", "", 401},
		{"wrong key", "/start_instance", "Bearer nope", 401},
		{"prefix of a key", "/start_instance", "Bearer secret-on", 401},
		{"key with a suffix", "/start_instance", "Bearer secret-one!", 401},
		{"wrong scheme", "/start_instance", "Basic secret-one", 401},
		{"bare token without scheme", "/start_instance", "secret-one", 401},
		{"first key", "/start_instance", "Bearer secret-one", 200},
		{"second key", "/update_instance", "Bearer secret-two", 200},
		{"scheme is case-insensitive", "/workflows/list", "bearer secret-two", 200},
		{"probe without key", "/live", "", 200},
		{"probe with wrong key", "/ready", "Bearer nope", 200},
		{"health is not exempt here", "/health", "", 401},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hdr := map[string]string{}
			if c.auth != "" {
				hdr["Authorization"] = c.auth
			}
			w := get(h, http.MethodPost, c.path, hdr)
			if w.Code != c.status {
				t.Fatalf("status %d, want %d (body %s)", w.Code, c.status, w.Body.String())
			}
			if c.status == 401 {
				if got := w.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
					t.Errorf("WWW-Authenticate = %q", got)
				}
				if ct := w.Header().Get("Content-Type"); ct != "application/json" {
					t.Errorf("Content-Type = %q", ct)
				}
				if !strings.Contains(w.Body.String(), `"error":"UNAUTHORIZED"`) {
					t.Errorf("body = %s", w.Body.String())
				}
				if strings.Contains(w.Body.String(), "secret") {
					t.Errorf("body leaks key material: %s", w.Body.String())
				}
			}
		})
	}
}

// With no usable key the middleware fails closed rather than open.
func TestAPIKeys_NoKeysRefusesEverything(t *testing.T) {
	h := NewAPIKeys(nil).Wrap(ok)
	if w := get(h, http.MethodGet, "/workflows/list", map[string]string{"Authorization": "Bearer "}); w.Code != 401 {
		t.Fatalf("status %d, want 401", w.Code)
	}
}

// Threat model T3: a browser page on an unlisted origin gets no allowance.
func TestCORS(t *testing.T) {
	c := NewCORS([]string{" https://UI.example.com ", "*", ""})
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1 (wildcard and blanks refused)", c.Len())
	}
	h := c.Wrap(ok)

	t.Run("no origin passes through untouched", func(t *testing.T) {
		w := get(h, http.MethodPost, "/start_instance", nil)
		if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Vary") != "" {
			t.Fatalf("code %d headers %v", w.Code, w.Header())
		}
	})
	t.Run("allowed origin is echoed, never a wildcard", func(t *testing.T) {
		w := get(h, http.MethodPost, "/start_instance", map[string]string{"Origin": "https://ui.example.com"})
		if w.Code != 200 {
			t.Fatalf("code %d", w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://ui.example.com" {
			t.Errorf("Allow-Origin = %q", got)
		}
		if w.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("credentials must never be allowed")
		}
		if w.Header().Get("Vary") != "Origin" {
			t.Errorf("Vary = %q", w.Header().Get("Vary"))
		}
	})
	t.Run("allowed preflight", func(t *testing.T) {
		w := get(h, http.MethodOptions, "/update_instance", map[string]string{"Origin": "https://ui.example.com"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("code %d", w.Code)
		}
		if !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
			t.Errorf("Allow-Headers = %q must include Authorization", w.Header().Get("Access-Control-Allow-Headers"))
		}
		if w.Body.String() == "ok" {
			t.Errorf("preflight reached the handler")
		}
	})
	t.Run("unknown origin gets no allowance", func(t *testing.T) {
		w := get(h, http.MethodPost, "/start_instance", map[string]string{"Origin": "https://evil.example.com"})
		if w.Code != 200 {
			t.Fatalf("code %d (the request itself is still served; the browser blocks the response)", w.Code)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("Allow-Origin leaked: %q", w.Header().Get("Access-Control-Allow-Origin"))
		}
		if w.Header().Get("Vary") != "Origin" {
			t.Errorf("Vary = %q", w.Header().Get("Vary"))
		}
	})
	t.Run("unknown origin preflight is refused", func(t *testing.T) {
		w := get(h, http.MethodOptions, "/start_instance", map[string]string{"Origin": "https://evil.example.com"})
		if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("code %d headers %v", w.Code, w.Header())
		}
	})
}

// Threat model T5: a body past the cap fails the handler's read.
func TestMaxBody(t *testing.T) {
	var readErr error
	var n int
	h := MaxBody(16, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		n, readErr = len(b), err
		w.WriteHeader(200)
	}))

	r := httptest.NewRequest(http.MethodPost, "/update_instance", strings.NewReader(strings.Repeat("x", 16)))
	h.ServeHTTP(httptest.NewRecorder(), r)
	if readErr != nil || n != 16 {
		t.Fatalf("body at the cap: n=%d err=%v", n, readErr)
	}

	r = httptest.NewRequest(http.MethodPost, "/update_instance", strings.NewReader(strings.Repeat("x", 17)))
	h.ServeHTTP(httptest.NewRecorder(), r)
	var mbe *http.MaxBytesError
	if readErr == nil || !errorAs(readErr, &mbe) {
		t.Fatalf("body past the cap: err=%v, want *http.MaxBytesError", readErr)
	}
}

func errorAs(err error, target **http.MaxBytesError) bool {
	e, ok := err.(*http.MaxBytesError)
	if ok {
		*target = e
	}
	return ok
}

func TestParseHelpers(t *testing.T) {
	if got := ParseList(" a, ,b ,, c"); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("ParseList = %q", got)
	}
	if got := ParseList(""); got != nil {
		t.Errorf("ParseList(\"\") = %q", got)
	}
	for in, want := range map[string]int64{"1024": 1024, "64k": 65536, "1M": 1 << 20, " 2m ": 2 << 20} {
		if got, err := ParseBytes(in); err != nil || got != want {
			t.Errorf("ParseBytes(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "-1", "abc", "1G"} {
		if _, err := ParseBytes(in); err == nil {
			t.Errorf("ParseBytes(%q) accepted", in)
		}
	}
}
