// Package logging is the phase 9 structured-logging setup: one slog logger
// for the process, a request-scoped logger carried in the context, and the
// HTTP access-log middleware that creates it.
//
// Every line is a JSON object (or logfmt-style text in development) with a
// stable set of keys. Anything about a workflow instance carries
// `instance_id`; anything inside a request carries `request_id`. Query
// values reach the log only as attribute values, which the handler encodes,
// so a newline or control character in a parameter cannot forge a line
// (gosec G706).
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Setup builds the process logger from SAGAWISE_LOG_FORMAT ("json", the
// default, or "text") and SAGAWISE_LOG_LEVEL ("debug", "info" (default),
// "warn", "error"), installs it as slog's default and routes the standard
// library's log package through it. An unknown value is an error, so a typo
// fails startup rather than silently logging at the wrong level.
func Setup(w io.Writer, format, level string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "", "info":
		lvl = slog.LevelInfo
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("SAGAWISE_LOG_LEVEL=%q: want debug, info, warn or error", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	switch strings.ToLower(format) {
	case "", "json":
		h = slog.NewJSONHandler(w, opts)
	case "text":
		h = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("SAGAWISE_LOG_FORMAT=%q: want json or text", format)
	}
	l := slog.New(h)
	slog.SetDefault(l) // also redirects the log package (log.Printf) through l
	return l, nil
}

// Discard is a logger that writes nothing, for tests and benchmarks.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type ctxKey struct{}

// NewContext returns a context carrying l. Handlers and the code they call
// retrieve it with From, so a line logged deep inside the engine still has
// the request's id and instance id.
func NewContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// From returns the logger carried by ctx, or fallback when there is none
// (a background loop, a test that calls a handler directly).
func From(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	if fallback != nil {
		return fallback
	}
	return slog.Default()
}

// HeaderRequestID is the header a caller may set to correlate its own logs
// with Sagawise's; the response always carries it, generated if absent.
const HeaderRequestID = "X-Request-Id"

// maxAttrLen bounds a query value copied into a log attribute, so a huge
// parameter cannot bloat the log.
const maxAttrLen = 128

// Middleware writes one access-log line per request and puts a request
// logger in the context. Params lists the query parameters copied into the
// line (and into the request logger) when present; the value of
// InstanceParam is logged under the key "instance_id" so that key is the
// same on every line about an instance, wherever it is logged from.
type Middleware struct {
	Logger        *slog.Logger
	InstanceParam string   // e.g. "workflow_instance_id"
	Params        []string // other parameters worth a column, e.g. action_type
	// Quiet paths (the probes) are logged at debug when they succeed, so
	// a healthcheck every few seconds does not fill the log.
	Quiet []string
}

// Wrap installs the middleware around next.
func (m Middleware) Wrap(next http.Handler) http.Handler {
	base := m.Logger
	if base == nil {
		base = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := sanitize(r.Header.Get(HeaderRequestID))
		if reqID == "" {
			reqID = newRequestID()
		}
		w.Header().Set(HeaderRequestID, reqID)

		attrs := []any{"request_id", reqID}
		q := r.URL.Query()
		if m.InstanceParam != "" {
			if v := q.Get(m.InstanceParam); v != "" {
				attrs = append(attrs, "instance_id", sanitize(v))
			}
		}
		for _, p := range m.Params {
			if v := q.Get(p); v != "" {
				attrs = append(attrs, p, sanitize(v))
			}
		}
		l := base.With(attrs...)

		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(NewContext(r.Context(), l)))

		level := slog.LevelInfo
		switch {
		case rec.status >= 500:
			level = slog.LevelError
		case rec.status >= 400:
			level = slog.LevelWarn
		case m.quiet(r.URL.Path):
			level = slog.LevelDebug
		}
		l.Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", float64(time.Since(start).Microseconds())/1000,
			"remote", r.RemoteAddr,
		)
	})
}

func (m Middleware) quiet(path string) bool {
	for _, q := range m.Quiet {
		if q == path {
			return true
		}
	}
	return false
}

// recorder captures the status and size of a response for the access line.
type recorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *recorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// sanitize bounds a caller-supplied string and strips control characters.
// The JSON handler would escape them anyway; this keeps the text handler
// safe too and makes the intent visible to a reviewer.
func sanitize(v string) string {
	if len(v) > maxAttrLen {
		v = v[:maxAttrLen]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
