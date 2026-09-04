package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var endpointOrder = []string{"start", "publish", "consume"}

func renderReport(res *Results, goBench string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Benchmark: %s\n\n", res.Label)
	fmt.Fprintf(&b, "- **Date:** %s\n- **Commit:** `%s`", res.Date, res.Commit)
	if res.Dirty {
		b.WriteString(" (working tree dirty)")
	}
	fmt.Fprintf(&b, "\n- **Machine:** %s, %s CPUs, %s, %s\n", res.Env["cpu"], res.Env["cpus"], res.Env["mem"], res.Env["os_arch"])
	fmt.Fprintf(&b, "- **Stack:** Go %s, Redis %s at %s, Postgres %s at %s\n", res.Env["go"], res.Env["redis"], res.Env["redis_addr"], res.Env["postgres"], res.Env["postgres_addr"])
	fmt.Fprintf(&b, "- **Config:** rates %s sagas/s, %s per rate, %s reaper-lag tasks, go-bench count %s × %s\n\n",
		res.Config["rates"], res.Config["duration"], res.Config["lag_tasks"], res.Config["bench_count"], res.Config["bench_time"])

	b.WriteString("Method: the real server binary on a free port, driven open-loop over HTTP. One saga = 5 requests (start, publish, consume, publish, consume) on a two-task workflow. Archive completeness counts `instance_history` rows for completed sagas after the rate finishes. Reaper lag is deadline → failure-webhook arrival for tasks published with a 2s timeout and never consumed. See `docs/benchmarks/README.md`.\n\n")

	b.WriteString("## Load\n\n")
	b.WriteString("| target sagas/s | achieved | requests | errors | start p50/p99 ms | publish p50/p99 ms | consume p50/p99 ms | redis cmds/saga | archived / completed |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range res.Rates {
		fmt.Fprintf(&b, "| %.0f | %.1f | %d | %d (%.2f%%) |", r.TargetSagasPerSec, r.AchievedSagasPS, r.Requests, r.Errors, r.ErrorRate*100)
		for _, ep := range endpointOrder {
			e := r.Endpoints[ep]
			fmt.Fprintf(&b, " %.1f / %.1f |", e.P50ms, e.P99ms)
		}
		fmt.Fprintf(&b, " %.1f | %d / %d", r.RedisCmdsPerSaga, r.ArchiveRows, r.ArchiveExpected)
		if r.ArchiveMissing > 0 {
			fmt.Fprintf(&b, " (**%d missing**)", r.ArchiveMissing)
		}
		b.WriteString(" |\n")
	}
	b.WriteString("\nFull percentiles (ms):\n\n| rate | endpoint | n | p50 | p95 | p99 | max |\n|---|---|---|---|---|---|---|\n")
	for _, r := range res.Rates {
		for _, ep := range endpointOrder {
			e := r.Endpoints[ep]
			fmt.Fprintf(&b, "| %.0f | %s | %d | %.1f | %.1f | %.1f | %.1f |\n", r.TargetSagasPerSec, ep, e.Count, e.P50ms, e.P95ms, e.P99ms, e.MaxMs)
		}
	}

	lag := res.ReaperLag
	b.WriteString("\n## Reaper lag (deadline → webhook)\n\n")
	fmt.Fprintf(&b, "| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |\n|---|---|---|---|---|---|---|\n| %d | %d | %d | %.0f | %.0f | %.0f | %.0f |\n",
		lag.Tasks, lag.Received, lag.Missing, lag.MinMs, lag.P50ms, lag.P99ms, lag.MaxMs)
	b.WriteString("\nThe reaper ticks once per second, so a lag between 0 and ~1000 ms is the design floor; anything above that is queueing inside the tick.\n")

	b.WriteString("\n## Go micro-benchmarks\n\nHandlers called in-process against the same Redis and Postgres (`go-bench.txt`, compare with `benchstat`).\n\n```\n")
	for _, line := range strings.Split(strings.TrimSpace(goBench), "\n") {
		if strings.HasPrefix(line, "Benchmark") || strings.HasPrefix(line, "goos") || strings.HasPrefix(line, "goarch") || strings.HasPrefix(line, "cpu:") || strings.HasPrefix(line, "pkg:") {
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("```\n")
	return b.String()
}

// ---- compare ----

func loadRun(dir string) (*Results, error) {
	data, err := os.ReadFile(filepath.Join(dir, "load.json")) // #nosec G304 -- operator-supplied run directory
	if err != nil {
		return nil, err
	}
	var r Results
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	return &r, nil
}

func compareRuns(dirA, dirB, out string) (string, error) {
	a, err := loadRun(dirA)
	if err != nil {
		return "", err
	}
	b, err := loadRun(dirB)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		return "", err
	}
	path := filepath.Join(out, fmt.Sprintf("%s_%s_vs_%s.md", time.Now().Format("2006-01-02"), a.Label, b.Label))

	var w strings.Builder
	fmt.Fprintf(&w, "# %s vs %s\n\n", a.Label, b.Label)
	fmt.Fprintf(&w, "| | A: %s | B: %s |\n|---|---|---|\n", a.Label, b.Label)
	fmt.Fprintf(&w, "| run | `%s` | `%s` |\n", filepath.Base(dirA), filepath.Base(dirB))
	fmt.Fprintf(&w, "| commit | `%s` | `%s` |\n", a.Commit, b.Commit)
	fmt.Fprintf(&w, "| date | %s | %s |\n", a.Date, b.Date)
	fmt.Fprintf(&w, "| machine | %s | %s |\n\n", a.Env["cpu"], b.Env["cpu"])
	if a.Env["cpu"] != b.Env["cpu"] || a.Env["cpus"] != b.Env["cpus"] {
		w.WriteString("**Warning:** the two runs are from different machines; compare with care.\n\n")
	}

	pct := func(x, y float64) string {
		if x == 0 {
			return "n/a"
		}
		d := (y - x) / x * 100
		sign := "+"
		if d < 0 {
			sign = ""
		}
		return fmt.Sprintf("%s%.0f%%", sign, d)
	}

	w.WriteString("## Load (A → B; negative latency change is better)\n\n")
	w.WriteString("| rate | metric | A | B | change |\n|---|---|---|---|---|\n")
	bRates := map[float64]RateResult{}
	for _, r := range b.Rates {
		bRates[r.TargetSagasPerSec] = r
	}
	for _, ra := range a.Rates {
		rb, ok := bRates[ra.TargetSagasPerSec]
		if !ok {
			continue
		}
		row := func(metric string, x, y float64, fmtS string) {
			fmt.Fprintf(&w, "| %.0f | %s | "+fmtS+" | "+fmtS+" | %s |\n", ra.TargetSagasPerSec, metric, x, y, pct(x, y))
		}
		row("achieved sagas/s", ra.AchievedSagasPS, rb.AchievedSagasPS, "%.1f")
		row("error rate %", ra.ErrorRate*100, rb.ErrorRate*100, "%.2f")
		for _, ep := range endpointOrder {
			row(ep+" p50 ms", ra.Endpoints[ep].P50ms, rb.Endpoints[ep].P50ms, "%.1f")
			row(ep+" p99 ms", ra.Endpoints[ep].P99ms, rb.Endpoints[ep].P99ms, "%.1f")
		}
		row("redis cmds/saga", ra.RedisCmdsPerSaga, rb.RedisCmdsPerSaga, "%.1f")
		fmt.Fprintf(&w, "| %.0f | archive missing | %d | %d | |\n", ra.TargetSagasPerSec, ra.ArchiveMissing, rb.ArchiveMissing)
	}

	w.WriteString("\n## Reaper lag\n\n| metric | A | B | change |\n|---|---|---|---|\n")
	fmt.Fprintf(&w, "| received / tasks | %d / %d | %d / %d | |\n", a.ReaperLag.Received, a.ReaperLag.Tasks, b.ReaperLag.Received, b.ReaperLag.Tasks)
	fmt.Fprintf(&w, "| p50 ms | %.0f | %.0f | %s |\n", a.ReaperLag.P50ms, b.ReaperLag.P50ms, pct(a.ReaperLag.P50ms, b.ReaperLag.P50ms))
	fmt.Fprintf(&w, "| p99 ms | %.0f | %.0f | %s |\n", a.ReaperLag.P99ms, b.ReaperLag.P99ms, pct(a.ReaperLag.P99ms, b.ReaperLag.P99ms))
	fmt.Fprintf(&w, "| max ms | %.0f | %.0f | %s |\n", a.ReaperLag.MaxMs, b.ReaperLag.MaxMs, pct(a.ReaperLag.MaxMs, b.ReaperLag.MaxMs))

	w.WriteString("\n## Go micro-benchmarks (benchstat)\n\n")
	if bs, err := exec.LookPath("benchstat"); err == nil {
		out, err := exec.Command(bs, filepath.Join(dirA, "go-bench.txt"), filepath.Join(dirB, "go-bench.txt")).CombinedOutput() // #nosec G204 -- fixed argv
		if err != nil {
			fmt.Fprintf(&w, "benchstat failed: %v\n\n```\n%s\n```\n", err, out)
		} else {
			fmt.Fprintf(&w, "```\n%s\n```\n", strings.TrimSpace(string(out)))
		}
	} else {
		w.WriteString("benchstat not installed (`go install golang.org/x/perf/cmd/benchstat@latest`); raw files: `go-bench.txt` in each run directory.\n")
	}

	if err := os.WriteFile(path, []byte(w.String()), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
