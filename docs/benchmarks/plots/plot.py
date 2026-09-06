#!/usr/bin/env python3
"""Draw the benchmark plots under docs/benchmarks/plots/ from the run directories.

Usage:  python3 docs/benchmarks/plots/plot.py        (needs matplotlib)

Every figure reads runs/*/load.json, runs/*/profile.json and runs/*/go-bench.txt
directly, so re-running the script after a new `make bench` refreshes the PNGs.
Which run stands for which phase is pinned in RUNS below; see README.md in this
directory for why (harness fixes make some runs incomparable).
"""
import json
import os
import re
import statistics
from collections import defaultdict

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402
from matplotlib.ticker import FuncFormatter  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
RUNS = os.path.join(HERE, "..", "runs")

# Phase -> run directory. Load runs (load.json + go-bench.txt).
LOAD_RUNS = {
    "baseline": "2026-09-05_0517_8f8e27c_baseline",
    "phase 5": "2026-09-05_0543_2637bcc_after-phase-5",
    "phase 6": "2026-09-05_0738_84161db_after-phase-6",
    "phase 7": "2026-09-05_0811_b974cf0_after-phase-7",
}
# Profile runs (profile.json). Phase 6 uses the original run for the ramp and
# the scaling curves (those sections were not affected by the commandstats
# pollution); the round-trip and timeout figures use the re-run of the phase 6
# server under the phase 7 harness, which is the honest A-side (README).
PROFILE_RUNS = {
    "baseline": "2026-09-05_0521_8f8e27c_profile-baseline",
    "phase 5": "2026-09-05_0546_2637bcc_profile-after-phase-5",
    "phase 6": "2026-09-05_0741_84161db_profile-after-phase-6",
    "phase 7": "2026-09-05_0818_a89cf09_profile-after-phase-7",
}
P6_NEWHARNESS = "2026-09-05_0821_a89cf09_profile-p6server-newharness"

PHASES = list(LOAD_RUNS)
# Categorical slots 1-4 of the reference palette (validated: adjacent pairs
# pass CVD and normal-vision floors in light mode).
COLORS = {
    "baseline": "#2a78d6",
    "phase 5": "#eb6834",
    "phase 6": "#1baf7a",
    "phase 7": "#eda100",
}
PHASE_LABEL = {
    "baseline": "baseline (8f8e27c)",
    "phase 5": "after phase 5, quick wins (2637bcc)",
    "phase 6": "after phase 6, state machine (84161db)",
    "phase 7": "after phase 7, efficiency (b974cf0 / a89cf09)",
}
INK = "#0b0b0b"
INK2 = "#52514e"
GRID = "#e6e5e1"
SURFACE = "#fcfcfb"

plt.rcParams.update({
    "font.family": "sans-serif",
    "font.size": 10,
    "axes.edgecolor": GRID,
    "axes.labelcolor": INK2,
    "axes.titlecolor": INK,
    "axes.titleweight": "bold",
    "axes.titlesize": 11,
    "axes.spines.top": False,
    "axes.spines.right": False,
    "axes.grid": True,
    "axes.grid.axis": "y",
    "grid.color": GRID,
    "grid.linewidth": 0.8,
    "xtick.color": INK2,
    "ytick.color": INK2,
    "xtick.labelsize": 9,
    "ytick.labelsize": 9,
    "figure.facecolor": SURFACE,
    "axes.facecolor": SURFACE,
    "legend.frameon": False,
    "legend.fontsize": 9,
})


def load(name):
    return json.load(open(os.path.join(RUNS, name, "load.json")))


def profile(name):
    return json.load(open(os.path.join(RUNS, name, "profile.json")))


def gobench(name):
    """Mean ns/op, B/op, allocs/op per benchmark across the -count repeats."""
    acc = defaultdict(lambda: defaultdict(list))
    for line in open(os.path.join(RUNS, name, "go-bench.txt")):
        m = re.match(r"^(Benchmark\S+?)-\d+\s+\d+\s+(\d+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op", line)
        if m:
            b = m.group(1).replace("Benchmark", "")
            acc[b]["ns"].append(int(m.group(2)))
            acc[b]["bytes"].append(int(m.group(3)))
            acc[b]["allocs"].append(int(m.group(4)))
    return {b: {k: statistics.mean(v) for k, v in d.items()} for b, d in acc.items()}


def save(fig, name):
    fig.savefig(os.path.join(HERE, name), dpi=150, bbox_inches="tight", facecolor=SURFACE)
    plt.close(fig)
    print("wrote", name)


def legend(fig, phases=PHASES, extra=None):
    handles = [plt.Line2D([], [], color=COLORS[p], lw=6, label=PHASE_LABEL[p]) for p in phases]
    if extra:
        handles += extra
    fig.legend(handles=handles, loc="lower center", ncol=2, bbox_to_anchor=(0.5, -0.02))


def grouped_bars(ax, groups, series, values, fmt="{:.2f}", ylabel=""):
    """groups: x categories; series: phase keys; values[series][group]."""
    n = len(series)
    width = 0.8 / n
    for i, s in enumerate(series):
        xs = [g + (i - (n - 1) / 2) * width for g in range(len(groups))]
        ys = [values[s][g] for g in groups]
        bars = ax.bar(xs, ys, width=width * 0.92, color=COLORS[s], label=PHASE_LABEL[s])
        for b, y in zip(bars, ys):
            ax.text(b.get_x() + b.get_width() / 2, y, fmt.format(y), ha="center", va="bottom",
                    fontsize=7.5, color=INK, rotation=90 if n > 3 else 0)
    ax.set_xticks(range(len(groups)))
    ax.set_xticklabels(groups)
    ax.set_ylabel(ylabel)
    top = max(values[s][g] for s in series for g in groups)
    ax.set_ylim(0, top * (1.45 if n > 3 else 1.25))


# ---------------------------------------------------------------- 1. latency
def fig_latency():
    loads = {p: load(r) for p, r in LOAD_RUNS.items()}
    endpoints = ["start", "publish", "consume"]
    fig, axes = plt.subplots(2, 3, figsize=(12, 7))
    for row, pct in enumerate(["p50_ms", "p99_ms"]):
        row_top = max(r["endpoints"][ep][pct] for p in PHASES for r in loads[p]["rates"] for ep in endpoints)
        for col, ep in enumerate(endpoints):
            ax = axes[row][col]
            vals = {p: {} for p in PHASES}
            rates = [r["target_sagas_per_sec"] for r in loads["baseline"]["rates"]]
            for p in PHASES:
                for r in loads[p]["rates"]:
                    vals[p][r["target_sagas_per_sec"]] = r["endpoints"][ep][pct]
            grouped_bars(ax, rates, PHASES, vals, ylabel="ms" if col == 0 else "")
            ax.set_ylim(0, row_top * 1.45)
            ax.set_title(f"{ep}  {pct[:-3]}", pad=10)
            ax.set_xlabel("target sagas / s")
    fig.suptitle("HTTP latency per endpoint under open-loop load (20 s per rate). Lower is better.",
                 fontsize=12, fontweight="bold", x=0.01, ha="left")
    legend(fig)
    fig.tight_layout(rect=(0, 0.07, 1, 0.96))
    save(fig, "01-latency-by-phase.png")


# ------------------------------------------------------ 2. redis cmds / saga
def fig_cmds_per_saga():
    loads = {p: load(r) for p, r in LOAD_RUNS.items()}
    fig, ax = plt.subplots(figsize=(7, 4))
    vals = [statistics.mean(r["redis_cmds_per_saga"] for r in loads[p]["rates"]) for p in PHASES]
    bars = ax.bar(range(len(PHASES)), vals, color=[COLORS[p] for p in PHASES], width=0.6)
    for b, v in zip(bars, vals):
        ax.text(b.get_x() + b.get_width() / 2, v + 0.4, f"{v:.1f}", ha="center", fontsize=10, color=INK)
    ax.set_xticks(range(len(PHASES)))
    ax.set_xticklabels(PHASES)
    ax.set_ylabel("Redis commands per saga (5 HTTP requests)")
    ax.set_ylim(0, max(vals) * 1.2)
    ax.set_title("Redis commands per saga, mean over the 50/100/200 sagas/s runs. Lower is better.\n"
                 "Phase 6 counts the commands executed inside the Lua script as well.",
                 loc="left", fontsize=10)
    fig.tight_layout()
    save(fig, "02-redis-commands-per-saga.png")


# ------------------------------------------------------------ 3. ramp curves
def fig_ramp():
    profs = {p: profile(r) for p, r in PROFILE_RUNS.items()}
    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(12, 4.8))
    for p in PHASES:
        ramp = profs[p]["ramp"]
        xs = [r["target_sagas_per_sec"] for r in ramp]
        ax1.plot(xs, [r["consume"]["p99_ms"] for r in ramp], marker="o", ms=5, lw=2, color=COLORS[p])
        ax2.plot(xs, [r["redis_cpu_pct"] for r in ramp], marker="o", ms=5, lw=2, color=COLORS[p])
        knee = profs[p]["knee_sagas_per_sec"]
        for ax, key in ((ax1, lambda r: r["consume"]["p99_ms"]), (ax2, lambda r: r["redis_cpu_pct"])):
            kr = next(r for r in ramp if r["target_sagas_per_sec"] == knee)
            ax.plot([knee], [key(kr)], marker="o", ms=11, mfc="none", mec=COLORS[p], mew=2)
    knees = "\n".join(f"{p}: {profs[p]['knee_sagas_per_sec']} sagas/s" for p in PHASES)
    ax1.text(0.03, 0.97, "knee (last passing rate)\n" + knees, transform=ax1.transAxes, va="top",
             fontsize=8, color=INK2, bbox=dict(boxstyle="round,pad=0.4", fc=SURFACE, ec=GRID))
    for ax in (ax1, ax2):
        ax.set_xscale("log")
        ax.set_xticks([200, 300, 450, 675, 1012, 1518, 2277, 3415])
        ax.get_xaxis().set_major_formatter(FuncFormatter(lambda v, _: f"{int(v)}"))
        ax.minorticks_off()
        ax.set_xlabel("target sagas / s (×1.5 per step)")
    ax1.set_yscale("log")
    ax1.set_yticks([1, 2, 5, 10, 20, 50, 100, 200, 500])
    ax1.get_yaxis().set_major_formatter(FuncFormatter(lambda v, _: f"{v:g}"))
    ax1.axhline(50, color=INK2, lw=1, ls="--")
    ax1.text(1050, 56, "SLO: p99 ≤ 50 ms", fontsize=8, color=INK2)
    ax1.set_ylabel("consume p99 (ms, log)")
    ax1.set_title("Consume p99 across the saturation ramp")
    ax2.set_ylabel("Redis CPU (% of one core)")
    ax2.set_ylim(0, 105)
    ax2.set_title("Redis CPU during the same ramp")
    fig.suptitle("Saturation ramp: rate ×1.5 per step until the SLO breaks. Ring = last passing rate (the knee).",
                 fontsize=12, fontweight="bold", x=0.01, ha="left")
    legend(fig)
    fig.tight_layout(rect=(0, 0.1, 1, 0.94))
    save(fig, "03-saturation-ramp.png")


# ------------------------------------------------- 4. round-trips per request
def fig_roundtrips():
    runs = dict(PROFILE_RUNS)
    runs["phase 6"] = P6_NEWHARNESS
    profs = {p: profile(r) for p, r in runs.items()}
    endpoints = ["start", "publish", "consume", "consume(final)"]
    vals = {p: {e: sum(c["calls_per_request"] for c in profs[p]["commands"] if c["endpoint"] == e)
                for e in endpoints} for p in PHASES}
    fig, ax = plt.subplots(figsize=(9, 4.5))
    grouped_bars(ax, endpoints, PHASES, vals, fmt="{:.1f}", ylabel="Redis commands per request")
    ax.set_title("Redis commands per request by endpoint (INFO commandstats delta, run in isolation). Lower is better.\n"
                 "Phase 6 is the phase 6 server re-run under the phase 7 harness; the original run's counts were polluted.",
                 loc="left", fontsize=10)
    legend(fig)
    fig.tight_layout(rect=(0, 0.12, 1, 1))
    save(fig, "04-redis-commands-per-request.png")


# -------------------------------------------------------- 5. go benchmarks
def fig_gobench():
    benches = {p: gobench(r) for p, r in LOAD_RUNS.items()}
    names = ["StartInstance", "Publish", "Consume", "Saga", "ReaperTick/overdue=10", "ReaperTick/overdue=50"]
    fig, axes = plt.subplots(1, 3, figsize=(15, 5.2))
    for ax, (key, unit, title) in zip(axes, [("ns", "ms / op", "Time per op"),
                                             ("bytes", "KB / op", "Bytes allocated per op"),
                                             ("allocs", "allocs / op", "Allocations per op")]):
        base = {n: benches["baseline"][n][key] for n in names}
        n = len(PHASES)
        width = 0.8 / n
        for i, p in enumerate(PHASES):
            xs = [g + (i - (n - 1) / 2) * width for g in range(len(names))]
            ys = [benches[p][nm][key] / base[nm] * 100 for nm in names]
            bars = ax.bar(xs, ys, width=width * 0.92, color=COLORS[p])
            for b, nm in zip(bars, names):
                raw = benches[p][nm][key]
                txt = f"{raw/1e6:.2f}" if key == "ns" else f"{raw/1024:.0f}" if key == "bytes" else f"{raw:.0f}"
                ax.text(b.get_x() + b.get_width() / 2, b.get_height() + 1, txt, ha="center", va="bottom",
                        fontsize=6.5, color=INK, rotation=90)
        ax.axhline(100, color=INK2, lw=1, ls="--")
        ax.set_xticks(range(len(names)))
        ax.set_xticklabels(["Start\nInstance", "Publish", "Consume", "Saga", "Reaper tick\n10 overdue",
                            "Reaper tick\n50 overdue"], fontsize=8)
        ax.set_ylabel("% of baseline")
        ax.set_ylim(0, 160)
        ax.set_title(f"{title}\n(bar labels: absolute {unit})", pad=8)
    fig.suptitle("Go micro-benchmarks (instance_engine/bench_test.go), relative to baseline = 100 %. Lower is better.",
                 fontsize=12, fontweight="bold", x=0.01, ha="left")
    legend(fig)
    fig.tight_layout(rect=(0, 0.1, 1, 0.93))
    save(fig, "05-go-microbenchmarks.png")


# ------------------------------------------------------- 6. scaling curves
def fig_scaling():
    profs = {p: profile(r) for p, r in PROFILE_RUNS.items()}
    sections = [("instances_in_redis", "Instances already in Redis", "instances", "consume"),
                ("tasks_per_workflow", "Tasks per workflow", "tasks", "consume"),
                ("payload_size", "Publish payload size", "payload", "publish")]
    fig, axes = plt.subplots(2, 3, figsize=(13, 7.5))
    for col, (sec, title, xlabel, ep) in enumerate(sections):
        for row, pct in enumerate(["p50_ms", "p99_ms"]):
            ax = axes[row][col]
            row_top = max(e["endpoints"][ep_][pct] for p in PHASES for sec_, _, _, ep_ in sections
                          for e in profs[p][sec_])
            levels = [e["level"] for e in profs["baseline"][sec]]
            vals = {p: {e["level"]: e["endpoints"][ep][pct] for e in profs[p][sec]} for p in PHASES}
            grouped_bars(ax, levels, PHASES, vals, ylabel="ms" if col == 0 else "")
            ax.set_ylim(0, row_top * 1.45)
            ax.set_title(f"{title}: {ep} {pct[:-3]}", pad=10)
            ax.set_xlabel(xlabel)
    fig.suptitle("Scaling curves: latency as the data grows (profile runs). Lower and flatter is better.",
                 fontsize=12, fontweight="bold", x=0.01, ha="left")
    legend(fig)
    fig.tight_layout(rect=(0, 0.07, 1, 0.95))
    save(fig, "06-scaling-curves.png")


# ------------------------------------------------------- 7. reaper timeouts
def fig_timeouts():
    a = profile(P6_NEWHARNESS)
    b = profile(PROFILE_RUNS["phase 7"])
    fig, ax = plt.subplots(figsize=(8, 4.5))
    tasks = [t["tasks"] for t in a["simultaneous_timeouts"]]
    for p, prof in (("phase 6", a), ("phase 7", b)):
        ys_max = [t["max_ms"] for t in prof["simultaneous_timeouts"]]
        ys_p50 = [t["p50_ms"] for t in prof["simultaneous_timeouts"]]
        ax.plot(tasks, ys_max, marker="o", ms=6, lw=2, color=COLORS[p])
        ax.plot(tasks, ys_p50, marker="s", ms=5, lw=1.5, ls=":", color=COLORS[p])
        for x, y in zip(tasks, ys_max):
            above = p == "phase 6"
            ax.annotate(f"{y:.0f}", (x, y), textcoords="offset points", xytext=(0, 7 if above else -13),
                        ha="center", fontsize=8, color=INK)
    ax.axhspan(0, 1000, color=GRID, alpha=0.5, lw=0)
    ax.text(105, 1020, "0–1000 ms: design floor (reaper ticks once a second)", fontsize=8, color=INK2)
    ax.set_xscale("log")
    ax.set_xticks(tasks)
    ax.get_xaxis().set_major_formatter(FuncFormatter(lambda v, _: f"{int(v)}"))
    ax.minorticks_off()
    ax.set_xlabel("tasks expiring at the same moment")
    ax.set_ylabel("deadline → failure webhook (ms)")
    ax.set_ylim(0, 2600)
    ax.set_title("Simultaneous timeouts: reaper lag. Solid = max, dotted = p50. Lower is better.\n"
                 "Only runs on the fixed harness (a89cf09) are shown; earlier lag figures are unreliable (README).",
                 loc="left", fontsize=10)
    legend(fig, phases=["phase 6", "phase 7"],
           extra=[plt.Line2D([], [], color=INK2, lw=2, label="max"),
                  plt.Line2D([], [], color=INK2, lw=1.5, ls=":", label="p50")])
    fig.tight_layout(rect=(0, 0.14, 1, 1))
    save(fig, "07-reaper-lag-simultaneous-timeouts.png")


# ---------------------------------------------------------- 8. contention
def fig_contention():
    profs = {p: profile(r) for p, r in PROFILE_RUNS.items()}
    groups = ["same instance", "20 separate instances"]
    fig, axes = plt.subplots(1, 2, figsize=(10, 4.2), sharey=True)
    for ax, pct in zip(axes, ["p50_ms", "p99_ms"]):
        vals = {p: {"same instance": profs[p]["contention"]["same_instance"][pct],
                    "20 separate instances": profs[p]["contention"]["separate_instances"][pct]} for p in PHASES}
        grouped_bars(ax, groups, PHASES, vals, ylabel="ms")
        ax.set_title(f"20 concurrent reports, {pct[:-3]}", pad=10)
    fig.suptitle("Contention: 20 concurrent reports on one instance vs on 20 instances. Lower is better.",
                 fontsize=12, fontweight="bold", x=0.01, ha="left")
    legend(fig)
    fig.tight_layout(rect=(0, 0.12, 1, 0.93))
    save(fig, "08-contention.png")


if __name__ == "__main__":
    fig_latency()
    fig_cmds_per_saga()
    fig_ramp()
    fig_roundtrips()
    fig_gobench()
    fig_scaling()
    fig_timeouts()
    fig_contention()
