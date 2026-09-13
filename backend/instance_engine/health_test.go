package instance_engine

import (
	"errors"
	"testing"
	"time"
)

// The liveness probe reflects the loops' heartbeats against the engine
// clock: never beaten is not_running, recent is ok, older than
// LoopStallTimeout is stalled. It touches no store.
func TestLiveness(t *testing.T) {
	e := New(nil, nil)
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	e.Clock = clock

	rep := e.Liveness()
	if rep.Status != StatusUnavailable || rep.Healthy() {
		t.Errorf("before any loop runs: %+v", rep)
	}
	for _, c := range []string{"reaper", "archive_worker", "webhook_worker"} {
		if rep.Checks[c] != CheckNotRunning {
			t.Errorf("%s = %q, want %q", c, rep.Checks[c], CheckNotRunning)
		}
	}

	e.reaperBeat.Store(clock.Now().UnixNano())
	e.Archiver.beat()
	e.Webhooks.beat()
	if rep := e.Liveness(); rep.Status != StatusOK || !rep.Healthy() {
		t.Errorf("after beats: %+v", rep)
	}

	clock.Advance(LoopStallTimeout + time.Second)
	rep = e.Liveness()
	if rep.Status != StatusUnavailable || rep.Checks["reaper"] != CheckStalled {
		t.Errorf("after %v of silence: %+v", LoopStallTimeout, rep)
	}

	// One fresh loop does not hide two stalled ones.
	e.reaperBeat.Store(clock.Now().UnixNano())
	rep = e.Liveness()
	if rep.Status != StatusUnavailable || rep.Checks["reaper"] != CheckOK || rep.Checks["archive_worker"] != CheckStalled {
		t.Errorf("mixed: %+v", rep)
	}
}

func TestIsConfigForbidden(t *testing.T) {
	for _, tc := range []struct {
		err  string
		want bool
	}{
		{"ERR unknown command 'config', with args beginning with: 'get'", true},
		{"NOPERM this user has no permissions to run the 'config|get' command", true},
		{"ERR CONFIG GET is not allowed on this instance", true},
		{"dial tcp 127.0.0.1:6379: connect: connection refused", false},
		{"i/o timeout", false},
	} {
		if got := isConfigForbidden(errors.New(tc.err)); got != tc.want {
			t.Errorf("isConfigForbidden(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestLagSeconds(t *testing.T) {
	now := time.UnixMilli(10_000)
	if lag, ok := lagSeconds(now, "7500"); !ok || lag != 2.5 {
		t.Errorf("lag = %v, %v; want 2.5", lag, ok)
	}
	if lag, ok := lagSeconds(now, "12000"); !ok || lag != 0 {
		t.Errorf("future deadline: lag = %v, %v; want 0", lag, ok)
	}
	if _, ok := lagSeconds(now, "x"); ok {
		t.Error("garbage score accepted")
	}
}
