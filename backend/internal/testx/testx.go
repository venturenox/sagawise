// Package testx holds the expected-failure harness for contract tests.
//
// The test suite is written against docs/contract.md, not against current
// behavior. Tests that today's code cannot pass are wrapped in XFail with the
// audit finding they depend on. They run on every CI build, show up as
// skipped with the finding number, and flip to a hard failure the moment the
// fix lands so the wrapper is removed and the finding ticked.
package testx

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// T is the subset of *testing.T a contract test body may use. Both
// *testing.T and the XFail recorder satisfy it, so a body runs unchanged
// whether it is expected to pass or expected to fail.
type T interface {
	Helper()
	Name() string
	Log(args ...any)
	Logf(format string, args ...any)
	Error(args ...any)
	Errorf(format string, args ...any)
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Fail()
	FailNow()
	Skip(args ...any)
	Skipf(format string, args ...any)
	Cleanup(func())
	TempDir() string
	Setenv(key, value string)
}

// Run runs body as an ordinary test. It exists so a body can be switched
// between Run and XFail by changing one word.
func Run(t *testing.T, body func(T)) {
	t.Helper()
	body(t)
}

// XFail runs body and expects it to fail. A failing body is reported as
// skipped with the finding; a passing body is a test failure, because the
// fix has landed and the wrapper must go.
func XFail(t *testing.T, finding string, body func(T)) {
	t.Helper()
	xfail(t, finding, body, true)
}

// XFailFlaky is XFail for bodies whose failure is nondeterministic (races).
// A passing body is logged, not failed, so a lucky run does not break CI.
func XFailFlaky(t *testing.T, finding string, body func(T)) {
	t.Helper()
	xfail(t, finding, body, false)
}

func xfail(t *testing.T, finding string, body func(T), strict bool) {
	t.Helper()
	r := &recorder{T: t}
	func() {
		defer func() {
			if x := recover(); x != nil {
				if _, ok := x.(stopBody); !ok {
					panic(x)
				}
			}
		}()
		body(r)
	}()

	r.mu.Lock()
	failures := append([]string(nil), r.failures...)
	failed := r.failed
	r.mu.Unlock()

	if len(failures) == 0 && !failed {
		if strict {
			t.Errorf("XFAIL %s now PASSES: the fix landed. Replace testx.XFail with testx.Run and tick %s in docs/TODO.md.", finding, finding)
		} else {
			t.Logf("XFAIL %s passed this run (flaky by nature); leave the wrapper until the fix lands.", finding)
		}
		return
	}
	t.Skipf("XFAIL %s (known failing until fixed):\n    %s", finding, strings.Join(failures, "\n    "))
}

// stopBody is the panic value Fatal/FailNow use to abort the body.
type stopBody struct{}

// recorder collects failures instead of failing the real test.
type recorder struct {
	*testing.T
	mu       sync.Mutex
	failures []string
	failed   bool
}

func (r *recorder) record(msg string) {
	r.mu.Lock()
	r.failures = append(r.failures, msg)
	r.failed = true
	r.mu.Unlock()
}

func (r *recorder) Error(args ...any)                 { r.record(fmt.Sprint(args...)) }
func (r *recorder) Errorf(format string, args ...any) { r.record(fmt.Sprintf(format, args...)) }
func (r *recorder) Fail()                             { r.mu.Lock(); r.failed = true; r.mu.Unlock() }
func (r *recorder) FailNow()                          { r.Fail(); panic(stopBody{}) }
func (r *recorder) Fatal(args ...any)                 { r.Error(args...); panic(stopBody{}) }
func (r *recorder) Fatalf(format string, args ...any) { r.Errorf(format, args...); panic(stopBody{}) }
