//go:build integration

package instance_engine

import (
	"sync"
	"testing"
	"time"

	"wtfsaga/internal/testx"
	"wtfsaga/utils"
)

// Contract TO5/TO6: the reaper never loses a deadline. A Redis error while
// deciding leaves the deadline in place for the next tick. The reaper reads
// the state and fails the task inside one transition script, so the
// injected fault is on that script call: nothing before it spends the
// deadline. (#4)
func TestContract_ReaperSurvivesRedisError(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish")
		e.clock.Advance(21 * time.Second)

		e.faults.FailNext("evalsha", 1)
		e.tick()
		if e.faults.Hits() != 1 {
			t.Fatalf("fault was not exercised (hits=%d)", e.faults.Hits())
		}
		if got := e.taskState(id, "0"); got != "PUBLISHED" {
			t.Errorf("after faulted tick: task state = %q, want PUBLISHED (nothing decided yet)", got)
		}
		if _, has := e.deadline(id, "0"); !has {
			t.Errorf("after faulted tick: deadline was lost; the task can now never time out")
		}

		e.tick()
		if got := e.taskState(id, "0"); got != "FAILED" {
			t.Errorf("after healthy tick: task state = %q, want FAILED", got)
		}
		if got := len(e.sink.Calls()); got != 1 {
			t.Errorf("webhook calls = %d, want 1", got)
		}
	})
}

// Contract A2: a Postgres outage between the terminal transition and the
// insert must not lose the archive row. (#9)
func TestContract_ArchiveSurvivesPostgresOutage(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		e := newEnv(t)
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish 0")
		e.mustOK(e.consume(id, "it_order_created", "it_payments"), "consume 0")
		e.mustOK(e.publish(id, "it_payment_done"), "publish 1")

		e.postgresDown()
		e.mustOK(e.consume(id, "it_payment_done", "it_shipping"), "consume 1 during outage")
		if got := e.instanceState(id); got != "COMPLETED" {
			t.Fatalf("instance state = %q, want COMPLETED regardless of Postgres", got)
		}
		if got := e.archived(id); got != "" {
			t.Fatalf("archived %q during the outage; the insert should have failed", got)
		}
		if n, _ := e.eng.Archiver.Pending(e.ctx); n < 1 {
			t.Errorf("archive_pending is empty after a failed insert; the row would be lost")
		}
		e.postgresUp()
		e.clock.Advance(2 * time.Second) // past the first retry backoff

		if got := e.waitArchived(id, 10*time.Second); got != "COMPLETED" {
			t.Errorf("archived state after outage = %q, want COMPLETED (row was lost)", got)
		}
	})
}

// Contract TO7/W3: one hanging failure_url must not stall the reaping of
// other tasks. (#5)
func TestContract_SlowWebhookDoesNotStallReaper(t *testing.T) {
	testx.Run(t, func(t testx.T) {
		hang := utils.Workflow{
			Name: "it_hang_flow", Version: "1.0", Schema_version: "1.0",
			Tasks: []utils.Task{{Topic: "it_hang_topic", From: "it_hang", To: "it_hang_consumer", Timeout: 20000}},
		}
		e := newEnv(t, twoTaskFlow(), hang)

		var wg sync.WaitGroup
		t.Cleanup(wg.Wait) // registered before the hanging server's cleanup, so it runs after it
		e.eng.Services.(MapRegistry)["it_hang"] = e.hangingWebhook()

		// The hanging instance gets the earlier deadline, so a sequential
		// reaper meets it first.
		slow := e.start(hang.Name)
		e.mustOK(e.publish(slow, "it_hang_topic"), "publish slow")
		e.clock.Advance(time.Second)
		fast := e.start()
		e.mustOK(e.publish(fast, "it_order_created"), "publish fast")
		e.clock.Advance(21 * time.Second)

		wg.Add(1)
		go func() { defer wg.Done(); e.tick() }()

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && e.taskState(fast, "0") != "FAILED" {
			time.Sleep(50 * time.Millisecond)
		}
		if got := e.taskState(fast, "0"); got != "FAILED" {
			t.Errorf("task behind a hanging webhook was not reaped within 3s (state %q)", got)
		}
	})
}
