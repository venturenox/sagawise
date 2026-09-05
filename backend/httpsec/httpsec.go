// Package httpsec holds the HTTP security middleware of the Sagawise API:
// API-key authentication, a CORS allowlist, and a request body cap. Each
// piece is a plain http.Handler wrapper with no package-level state, so
// main wires them and tests exercise them in isolation. Roadmap phase 8;
// threat model in docs/threat-model.md.
package httpsec

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// writeError answers with the contract's error body shape (§9): every
// response Sagawise sends is JSON with a stable code.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": msg})
}

// ---- API keys ----

// APIKeys authenticates requests with a bearer token: the Authorization
// header must be `Bearer <key>` where key is one of the configured keys.
// Keys are compared in constant time against their SHA-256 digests, so
// neither a key's length nor a shared prefix leaks through timing. Paths in
// exempt are served without a key (the Kubernetes probes).
type APIKeys struct {
	hashes [][32]byte
	exempt map[string]bool
}

// NewAPIKeys builds the middleware. Empty keys are ignored; with no usable
// key every non-exempt request is refused, which is the fail-closed
// default. Callers that want an open API must not install the middleware
// at all (main does that only under SAGAWISE_AUTH=off).
func NewAPIKeys(keys []string, exemptPaths ...string) *APIKeys {
	a := &APIKeys{exempt: map[string]bool{}}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		a.hashes = append(a.hashes, sha256.Sum256([]byte(k)))
	}
	for _, p := range exemptPaths {
		a.exempt[p] = true
	}
	return a
}

// Len is the number of usable keys.
func (a *APIKeys) Len() int { return len(a.hashes) }

// Allowed reports whether the presented key is one of the configured keys.
func (a *APIKeys) Allowed(key string) bool {
	if key == "" {
		return false
	}
	sum := sha256.Sum256([]byte(key))
	ok := false
	// Check every key so the time taken does not reveal which one matched.
	for i := range a.hashes {
		if subtle.ConstantTimeCompare(sum[:], a.hashes[i][:]) == 1 {
			ok = true
		}
	}
	return ok
}

// Wrap installs the check in front of next. A missing or unknown key is
// 401 UNAUTHORIZED with a WWW-Authenticate challenge; the body names no
// key and does not say whether one was presented.
func (a *APIKeys) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.exempt[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		if !a.Allowed(bearer(r.Header.Get("Authorization"))) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="sagawise"`)
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "a valid API key is required: Authorization: Bearer <key>")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearer extracts the token of an `Authorization: Bearer <token>` header.
// The scheme is matched case-insensitively as RFC 9110 requires; the token
// is returned as sent.
func bearer(h string) string {
	const scheme = "bearer "
	if len(h) <= len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return ""
	}
	return strings.TrimSpace(h[len(scheme):])
}

// ---- CORS ----

// CORS answers cross-origin browser requests only for origins in the
// allowlist. A request whose Origin is not listed gets no CORS headers at
// all, so the browser blocks it; a request without an Origin header (every
// server-to-server call) is passed through untouched. Credentials are
// never allowed: the API is authenticated by a bearer token, not cookies.
type CORS struct {
	origins map[string]bool
}

// NewCORS builds the allowlist from exact origins ("https://ui.example.com").
// Entries are trimmed and lower-cased for the comparison; an empty entry is
// ignored. "*" is refused by construction: it is not an origin.
func NewCORS(origins []string) *CORS {
	c := &CORS{origins: map[string]bool{}}
	for _, o := range origins {
		o = strings.ToLower(strings.TrimSpace(o))
		if o == "" || o == "*" {
			continue
		}
		c.origins[o] = true
	}
	return c
}

// Len is the number of allowed origins.
func (c *CORS) Len() int { return len(c.origins) }

// Wrap installs the policy in front of next.
func (c *CORS) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		// The response depends on Origin, whether or not it is allowed, so
		// a shared cache must not serve one origin's answer to another.
		w.Header().Add("Vary", "Origin")
		if !c.origins[strings.ToLower(origin)] {
			if r.Method == http.MethodOptions {
				// A preflight from an unknown origin is refused outright;
				// the actual request would carry no Origin allowance anyway.
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- Body cap ----

// MaxBody caps every request body at n bytes. Reading past the cap makes
// the handler's io.ReadAll fail with *http.MaxBytesError, which the engine
// answers as 413 PAYLOAD_TOO_LARGE; the connection is closed afterwards so
// the client cannot keep streaming. A publish payload is stored in Redis
// and replayed in the failure webhook, so an unbounded body is both a
// memory and a storage exposure.
func MaxBody(n int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, n)
		}
		next.ServeHTTP(w, r)
	})
}

// ParseList splits a comma-separated configuration value into its trimmed,
// non-empty items.
func ParseList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseBytes parses a byte count with an optional K/M suffix (KiB/MiB).
func ParseBytes(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "M"):
		mult, s = 1<<20, strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "K"):
		mult, s = 1<<10, strings.TrimSuffix(s, "K")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, &strconv.NumError{Func: "ParseBytes", Num: s, Err: strconv.ErrSyntax}
	}
	return n * mult, nil
}
