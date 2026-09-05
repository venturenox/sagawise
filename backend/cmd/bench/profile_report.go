package main

import (
	"fmt"
	"sort"
	"strings"
)

// ---- findings: simple, explicit heuristics over the measured curves ----

func findings(p *Profile) []string {
	var f []string
	if len(p.Ramp) > 0 {
		last := p.Ramp[len(p.Ramp)-1]
		if last.Pass {
			f = append(f, fmt.Sprintf("No knee found: the SLO held up to %.0f sagas/s (~%.0f requests/s), the top of the ramp. Raise -ramp-max to find it.", p.Knee, p.Knee*5))
		} else {
			f = append(f, fmt.Sprintf("Knee: %.0f sagas/s (~%.0f requests/s). The next step, %.0f sagas/s, breached: %s.", p.Knee, p.Knee*5, last.Target, p.KneeReason))
		}
		if last.RedisCPUPct < 60 {
			f = append(f, fmt.Sprintf("Redis is not the limit: %.0f%% of one core at the breach step, ping p99 %.2f ms. The knee is in the server (per-request round-trips) or the load generator.", last.RedisCPUPct, last.PingP99ms))
		} else {
			f = append(f, fmt.Sprintf("Redis is near saturation at the knee: %.0f%% of one core, ping p99 %.2f ms. Reducing commands per request (phase 7) raises the ceiling directly.", last.RedisCPUPct, last.PingP99ms))
		}
	}
	// Per-endpoint command counts and the most expensive command.
	perEp := map[string]float64{}
	var worst CommandRow
	for _, c := range p.Commands {
		perEp[c.Endpoint] += c.CallsPerRequest
		if c.UsecPerCall*c.CallsPerRequest > worst.UsecPerCall*worst.CallsPerRequest {
			worst = c
		}
	}
	if len(perEp) > 0 {
		f = append(f, fmt.Sprintf("Redis commands per request: start %.1f, publish %.1f, consume %.1f, final consume %.1f (INFO commandstats; commands run inside the transition script count too, so this is Redis work, not client round-trips, which are 2 per report since phase 6).",
			perEp["start"], perEp["publish"], perEp["consume"], perEp["consume(final)"]))
		if worst.Command != "" {
			f = append(f, fmt.Sprintf("Most expensive Redis work per request: %s on %s (%.1f calls × %.0f µs).", worst.Command, worst.Endpoint, worst.CallsPerRequest, worst.UsecPerCall))
		}
		var setUs, getUs float64
		for _, c := range p.Commands {
			if c.Command == "json.set" && c.UsecPerCall > setUs {
				setUs = c.UsecPerCall
			}
			if c.Command == "json.get" && c.UsecPerCall > getUs {
				getUs = c.UsecPerCall
			}
		}
		if getUs > 0 && setUs > 3*getUs {
			f = append(f, fmt.Sprintf("JSON.SET costs %.0f µs against %.0f µs for JSON.GET (%.0f×). Every state write re-indexes the whole document in RediSearch (`workflows_index` covers `$.tasks[*].topic/from/to` and the instance stamps), so write count, not read count, is what Redis CPU pays for. A final consume does %.0f writes.", setUs, getUs, setUs/getUs, countCalls(p.Commands, "consume(final)", "json.set")))
		}
	}
	rel := func(rows []ScaleRow, ep string) (first, last float64) {
		if len(rows) < 2 {
			return 0, 0
		}
		return rows[0].Endpoints[ep].P50ms, rows[len(rows)-1].Endpoints[ep].P50ms
	}
	if a, b := rel(p.Instances, "consume"); a > 0 {
		f = append(f, fmt.Sprintf("Instances already in Redis: consume p50 %.1f → %.1f ms from %s to %s instances (%+.0f%%). List endpoint p50 %.1f → %.1f ms.",
			a, b, p.Instances[0].Level, p.Instances[len(p.Instances)-1].Level, (b-a)/a*100, p.Instances[0].Extra["list_p50_ms"], p.Instances[len(p.Instances)-1].Extra["list_p50_ms"]))
	}
	if a, b := rel(p.TasksPerWorkflow, "consume"); a > 0 {
		f = append(f, fmt.Sprintf("Tasks per workflow: consume p50 %.1f → %.1f ms from %s to %s tasks (%+.0f%%) at a constant ~1000 req/s. Growth here is document size: the script reads every task's state and each JSON.SET re-indexes the document.",
			a, b, p.TasksPerWorkflow[0].Level, p.TasksPerWorkflow[len(p.TasksPerWorkflow)-1].Level, (b-a)/a*100))
	}
	if a, b := rel(p.PayloadSize, "consume"); a > 0 {
		f = append(f, fmt.Sprintf("Payload size: consume p50 %.1f → %.1f ms from %s to %s (%+.0f%%). The payload travels with the publish and is written by the script's JSON.SET; a consume never reads it.",
			a, b, p.PayloadSize[0].Level, p.PayloadSize[len(p.PayloadSize)-1].Level, (b-a)/a*100))
	}
	if len(p.Timeouts) > 1 {
		a, b := p.Timeouts[0], p.Timeouts[len(p.Timeouts)-1]
		perTask := (b.MaxMs - a.MaxMs) / float64(b.Tasks-a.Tasks)
		f = append(f, fmt.Sprintf("Simultaneous timeouts: max lag %.0f ms at %d → %.0f ms at %d, i.e. ~%.1f ms per overdue task. The reaper runs one script call per overdue member, sequentially; lag grows linearly with the number of tasks that expire together. Missing webhooks: %d.",
			a.MaxMs, a.Tasks, b.MaxMs, b.Tasks, perTask, b.Missing))
	}
	if p.Contention.Same.Count > 0 && p.Contention.Spread.Count > 0 {
		f = append(f, fmt.Sprintf("Contention: %d concurrent reports on one instance p50 %.1f ms vs on separate instances %.1f ms (%+.0f%%). Redis runs one transition script at a time, so reports on one instance queue behind each other; this is the per-instance ceiling.",
			20, p.Contention.Same.P50ms, p.Contention.Spread.P50ms, (p.Contention.Same.P50ms-p.Contention.Spread.P50ms)/p.Contention.Spread.P50ms*100))
	}
	if cum, ok := p.Pprof["cpu-cumulative"]; ok {
		if top := engineFrames(cum, 6); top != "" {
			f = append(f, "Server CPU at the knee, cumulative share by engine function: "+top)
		}
	}
	return f
}

func countCalls(rows []CommandRow, endpoint, cmd string) float64 {
	for _, r := range rows {
		if r.Endpoint == endpoint && r.Command == cmd {
			return r.CallsPerRequest
		}
	}
	return 0
}

// engineFrames pulls the first n wtfsaga/* functions (with their cum%) from
// a `pprof -top -cum` listing.
func engineFrames(top string, n int) string {
	var names []string
	for _, line := range strings.Split(top, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || !strings.HasSuffix(fields[4], "%") {
			continue
		}
		fn := fields[len(fields)-1]
		if !strings.Contains(fn, "wtfsaga/") {
			continue
		}
		names = append(names, strings.TrimPrefix(fn, "wtfsaga/")+" "+fields[4])
		if len(names) == n {
			break
		}
	}
	return strings.Join(names, ", ")
}

// ---- render ----

func renderProfile(p *Profile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Profile: %s\n\n", p.Label)
	fmt.Fprintf(&b, "- **Date:** %s\n- **Commit:** `%s`", p.Date, p.Commit)
	if p.Dirty {
		b.WriteString(" (working tree dirty)")
	}
	fmt.Fprintf(&b, "\n- **Machine:** %s, %s CPUs, %s, %s\n", p.Env["cpu"], p.Env["cpus"], p.Env["mem"], p.Env["os_arch"])
	fmt.Fprintf(&b, "- **Stack:** Go %s, Redis %s, Postgres %s\n", p.Env["go"], p.Env["redis"], p.Env["postgres"])
	fmt.Fprintf(&b, "- **Config:** ramp %s→%s sagas/s ×1.5, hold %s; instances %s; payloads %s bytes; timeouts %s; pprof %ss\n\n",
		p.Config["ramp_start"], p.Config["ramp_max"], p.Config["ramp_hold"], p.Config["instances"], p.Config["payloads"], p.Config["timeouts"], p.Config["pprof_seconds"])
	b.WriteString("Purpose: find where the time goes and how it scales, not to produce a regression number (that is `make bench`). The load generator runs on the same machine as the server, Redis and Postgres; at the top of the ramp the generator itself can be the limit, which the SLO check reports as \"achieved < target\".\n\n")

	b.WriteString("## Findings\n\n")
	for _, f := range p.Findings {
		b.WriteString("- " + f + "\n")
	}

	fmt.Fprintf(&b, "\n## 1. Saturation ramp\n\nSLO: error rate ≤ %.0f%%, p99 ≤ %.0f ms, achieved ≥ %.0f%% of target. **Knee: %.0f sagas/s.** Breach: %s.\n\n", sloErrorRate*100, sloP99ms, sloAchieved*100, p.Knee, p.KneeReason)
	b.WriteString("| target sagas/s | achieved | req/s | errors | start p50/p99 | publish p50/p99 | consume p50/p99 | redis cpu % | redis ping p50/p99 ms | SLO |\n|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range p.Ramp {
		status := "ok"
		if !r.Pass {
			status = "**breach**"
		}
		fmt.Fprintf(&b, "| %.0f | %.0f | %.0f | %d (%.2f%%) | %.1f / %.1f | %.1f / %.1f | %.1f / %.1f | %.0f | %.2f / %.2f | %s |\n",
			r.Target, r.Achieved, r.RequestsPS, r.Errors, r.ErrorRate*100, r.Start.P50ms, r.Start.P99ms, r.Publish.P50ms, r.Publish.P99ms, r.Consume.P50ms, r.Consume.P99ms, r.RedisCPUPct, r.PingP50ms, r.PingP99ms, status)
	}

	fmt.Fprintf(&b, "\n## 2. Server profiles at the knee (%.0f sagas/s)\n\nRaw profiles in `pprof/`; open with `go tool pprof -http=: <binary> pprof/cpu.pprof`.\n", p.Knee)
	for _, name := range []string{"cpu", "cpu-cumulative", "heap", "block", "mutex", "goroutine"} {
		if top, ok := p.Pprof[name]; ok {
			fmt.Fprintf(&b, "\n### %s\n\n```\n%s\n```\n", name, strings.TrimSpace(top))
		}
	}

	b.WriteString("\n## 3. Redis commands per request\n\nEach endpoint run in isolation; `INFO commandstats` delta divided by requests.\n\n| endpoint | command | calls / request | µs / call | µs / request |\n|---|---|---|---|---|\n")
	rows := append([]CommandRow(nil), p.Commands...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Endpoint != rows[j].Endpoint {
			return endpointRank(rows[i].Endpoint) < endpointRank(rows[j].Endpoint)
		}
		return rows[i].CallsPerRequest*rows[i].UsecPerCall > rows[j].CallsPerRequest*rows[j].UsecPerCall
	})
	totals := map[string][2]float64{}
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s | %.2f | %.0f | %.0f |\n", r.Endpoint, r.Command, r.CallsPerRequest, r.UsecPerCall, r.CallsPerRequest*r.UsecPerCall)
		t := totals[r.Endpoint]
		t[0] += r.CallsPerRequest
		t[1] += r.CallsPerRequest * r.UsecPerCall
		totals[r.Endpoint] = t
	}
	b.WriteString("\n| endpoint | round-trips / request | redis µs / request |\n|---|---|---|\n")
	for _, ep := range []string{"start", "publish", "consume", "consume(final)"} {
		if t, ok := totals[ep]; ok {
			fmt.Fprintf(&b, "| %s | %.1f | %.0f |\n", ep, t[0], t[1])
		}
	}

	scaleTable := func(title, levelName string, rows []ScaleRow, extras []string) {
		fmt.Fprintf(&b, "\n## %s\n\n| %s | start p50/p99 | publish p50/p99 | consume p50/p99 |", title, levelName)
		for _, x := range extras {
			fmt.Fprintf(&b, " %s |", x)
		}
		b.WriteString("\n|---|---|---|---|")
		for range extras {
			b.WriteString("---|")
		}
		b.WriteString("\n")
		for _, r := range rows {
			fmt.Fprintf(&b, "| %s | %.1f / %.1f | %.1f / %.1f | %.1f / %.1f |", r.Level,
				r.Endpoints["start"].P50ms, r.Endpoints["start"].P99ms, r.Endpoints["publish"].P50ms, r.Endpoints["publish"].P99ms, r.Endpoints["consume"].P50ms, r.Endpoints["consume"].P99ms)
			for _, x := range extras {
				fmt.Fprintf(&b, " %.1f |", r.Extra[x])
			}
			b.WriteString("\n")
		}
	}
	scaleTable("4. Instances already in Redis (100 sagas/s)", "instances", p.Instances, []string{"list_p50_ms", "list_p99_ms", "get_p50_ms", "redis_bytes_per_instance"})
	scaleTable("5. Tasks per workflow (~1000 requests/s)", "tasks", p.TasksPerWorkflow, []string{"sagas_per_sec", "error_rate"})
	scaleTable("6. Payload size (50 sagas/s)", "payload", p.PayloadSize, []string{"error_rate"})

	b.WriteString("\n## 7. Simultaneous timeouts (reaper lag, deadline → webhook)\n\n| tasks | received | missing | min ms | p50 ms | p99 ms | max ms |\n|---|---|---|---|---|---|---|\n")
	for _, t := range p.Timeouts {
		fmt.Fprintf(&b, "| %d | %d | %d | %.0f | %.0f | %.0f | %.0f |\n", t.Tasks, t.Received, t.Missing, t.MinMs, t.P50ms, t.P99ms, t.MaxMs)
	}

	c := p.Contention
	fmt.Fprintf(&b, "\n## 8. Contention (20 concurrent reports, %d rounds)\n\n| | n | p50 ms | p95 ms | p99 ms | max ms |\n|---|---|---|---|---|---|\n| same instance | %d | %.1f | %.1f | %.1f | %.1f |\n| separate instances | %d | %.1f | %.1f | %.1f | %.1f |\n",
		c.Rounds, c.Same.Count, c.Same.P50ms, c.Same.P95ms, c.Same.P99ms, c.Same.MaxMs, c.Spread.Count, c.Spread.P50ms, c.Spread.P95ms, c.Spread.P99ms, c.Spread.MaxMs)
	return b.String()
}

func endpointRank(ep string) int {
	switch ep {
	case "start":
		return 0
	case "publish":
		return 1
	case "consume":
		return 2
	default:
		return 3
	}
}

// ---- compare ----

func compareProfiles(a, b *Profile) string {
	var w strings.Builder
	pct := func(x, y float64) string {
		if x == 0 {
			return "n/a"
		}
		return fmt.Sprintf("%+.0f%%", (y-x)/x*100)
	}
	fmt.Fprintf(&w, "## Profile: knee\n\n| | A | B | change |\n|---|---|---|---|\n| knee sagas/s | %.0f | %.0f | %s |\n| breach reason | %s | %s | |\n",
		a.Knee, b.Knee, pct(a.Knee, b.Knee), a.KneeReason, b.KneeReason)

	w.WriteString("\n## Profile: ramp at common rates (consume p99 ms, redis cpu %)\n\n| rate | A p99 | B p99 | change | A redis cpu | B redis cpu |\n|---|---|---|---|---|---|\n")
	bm := map[float64]RampStep{}
	for _, s := range b.Ramp {
		bm[s.Target] = s
	}
	for _, s := range a.Ramp {
		if t, ok := bm[s.Target]; ok {
			fmt.Fprintf(&w, "| %.0f | %.1f | %.1f | %s | %.0f | %.0f |\n", s.Target, s.Consume.P99ms, t.Consume.P99ms, pct(s.Consume.P99ms, t.Consume.P99ms), s.RedisCPUPct, t.RedisCPUPct)
		}
	}

	sum := func(rows []CommandRow) map[string]float64 {
		m := map[string]float64{}
		for _, r := range rows {
			m[r.Endpoint] += r.CallsPerRequest
		}
		return m
	}
	ca, cb := sum(a.Commands), sum(b.Commands)
	w.WriteString("\n## Profile: Redis round-trips per request\n\n| endpoint | A | B | change |\n|---|---|---|---|\n")
	for _, ep := range []string{"start", "publish", "consume", "consume(final)"} {
		fmt.Fprintf(&w, "| %s | %.1f | %.1f | %s |\n", ep, ca[ep], cb[ep], pct(ca[ep], cb[ep]))
	}

	scale := func(title string, ra, rb []ScaleRow) {
		fmt.Fprintf(&w, "\n## Profile: %s (consume p50 / p99 ms)\n\n| level | A | B | p50 change |\n|---|---|---|---|\n", title)
		bm := map[string]ScaleRow{}
		for _, r := range rb {
			bm[r.Level] = r
		}
		for _, r := range ra {
			if t, ok := bm[r.Level]; ok {
				fmt.Fprintf(&w, "| %s | %.1f / %.1f | %.1f / %.1f | %s |\n", r.Level, r.Endpoints["consume"].P50ms, r.Endpoints["consume"].P99ms, t.Endpoints["consume"].P50ms, t.Endpoints["consume"].P99ms, pct(r.Endpoints["consume"].P50ms, t.Endpoints["consume"].P50ms))
			}
		}
	}
	scale("instances in Redis", a.Instances, b.Instances)
	scale("tasks per workflow", a.TasksPerWorkflow, b.TasksPerWorkflow)
	scale("payload size", a.PayloadSize, b.PayloadSize)

	w.WriteString("\n## Profile: simultaneous timeouts (max lag ms)\n\n| tasks | A | B | change |\n|---|---|---|---|\n")
	tb := map[int]LagResult{}
	for _, t := range b.Timeouts {
		tb[t.Tasks] = t
	}
	for _, t := range a.Timeouts {
		if u, ok := tb[t.Tasks]; ok {
			fmt.Fprintf(&w, "| %d | %.0f | %.0f | %s |\n", t.Tasks, t.MaxMs, u.MaxMs, pct(t.MaxMs, u.MaxMs))
		}
	}
	fmt.Fprintf(&w, "\n## Profile: contention (same-instance p50 ms)\n\n| A | B | change |\n|---|---|---|\n| %.1f | %.1f | %s |\n", a.Contention.Same.P50ms, b.Contention.Same.P50ms, pct(a.Contention.Same.P50ms, b.Contention.Same.P50ms))
	return w.String()
}
