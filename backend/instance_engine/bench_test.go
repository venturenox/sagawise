//go:build integration

package instance_engine

import (
	"fmt"
	"testing"
	"time"
)

// Micro-benchmarks against real redis-stack + postgres, through the handlers
// (no HTTP server). Run with:
//
//	go test -tags integration -run '^$' -bench . -benchmem -count 6 ./instance_engine/
//
// `make bench` records them into docs/benchmarks/runs/<run>/go-bench.txt;
// `make bench-compare` diffs two runs with benchstat.

func BenchmarkStartInstance(b *testing.B) {
	e := newEnv(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.start()
	}
}

func BenchmarkPublish(b *testing.B) {
	e := newEnv(b)
	ids := make([]string, b.N)
	for i := range ids {
		ids[i] = e.start()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if w := e.publish(ids[i], "it_order_created"); !accepted(w) {
			b.Fatalf("publish: %d %s", w.Code, w.Body.String())
		}
	}
}

func BenchmarkConsume(b *testing.B) {
	e := newEnv(b)
	ids := make([]string, b.N)
	for i := range ids {
		ids[i] = e.start()
		e.mustOK(e.publish(ids[i], "it_order_created"), "publish")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if w := e.consume(ids[i], "it_order_created", "it_payments"); !accepted(w) {
			b.Fatalf("consume: %d %s", w.Code, w.Body.String())
		}
	}
}

// BenchmarkSaga is one full two-task saga: start, publish, consume, publish,
// consume. The instance ends COMPLETED and is archived asynchronously.
func BenchmarkSaga(b *testing.B) {
	e := newEnv(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := e.start()
		e.mustOK(e.publish(id, "it_order_created"), "publish 0")
		e.mustOK(e.consume(id, "it_order_created", "it_payments"), "consume 0")
		e.mustOK(e.publish(id, "it_payment_done"), "publish 1")
		e.mustOK(e.consume(id, "it_payment_done", "it_shipping"), "consume 1")
	}
}

// BenchmarkReaperTick measures one reaper pass over N overdue tasks, each of
// which is failed, webhooked (to the local sink) and archived.
func BenchmarkReaperTick(b *testing.B) {
	for _, n := range []int{10, 50} {
		b.Run(fmt.Sprintf("overdue=%d", n), func(b *testing.B) {
			e := newEnv(b)
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				for j := 0; j < n; j++ {
					id := e.start()
					e.mustOK(e.publish(id, "it_order_created"), "publish")
				}
				e.clock.Advance(21 * time.Second)
				b.StartTimer()
				e.tick()
			}
		})
	}
}
