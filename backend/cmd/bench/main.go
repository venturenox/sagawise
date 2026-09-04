// Command bench is the sagawise benchmark runner (roadmap phase 4).
//
//	go run ./cmd/bench run -label baseline -out ../docs/benchmarks/runs
//	go run ./cmd/bench compare <runDirA> <runDirB> -out ../docs/benchmarks/comparisons
//
// `run` builds the server binary, launches it on a free port against the
// Redis and Postgres named by REDIS_*/POSTGRES_* (defaults: localhost), and
// drives it over real HTTP with its own DSL and its own webhook receiver.
// Every run writes a new directory <date>_<commit>_<label>/ containing
// report.md, load.json, go-bench.txt and env.txt. Nothing is overwritten.
//
// `compare` reads two run directories and writes a side-by-side markdown
// report, including benchstat on the Go micro-benchmarks when installed.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		cfg := runConfig{}
		fs.StringVar(&cfg.label, "label", "baseline", "run label, e.g. baseline, after-phase-5")
		fs.StringVar(&cfg.out, "out", "../docs/benchmarks/runs", "directory that receives one sub-directory per run")
		fs.StringVar(&cfg.rates, "rates", "50,100,200", "saga start rates per second to test, comma separated")
		fs.DurationVar(&cfg.duration, "duration", 20e9, "time to hold each rate")
		fs.IntVar(&cfg.lagTasks, "lag-tasks", 200, "tasks to time out for the reaper-lag measurement")
		fs.IntVar(&cfg.benchCount, "bench-count", 4, "go test -count for the micro-benchmarks")
		fs.StringVar(&cfg.benchTime, "bench-time", "50x", "go test -benchtime for the micro-benchmarks")
		fs.BoolVar(&cfg.skipGoBench, "skip-gobench", false, "skip the Go micro-benchmarks")
		_ = fs.Parse(os.Args[2:])
		dir, err := runBenchmark(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bench run:", err)
			os.Exit(1)
		}
		fmt.Println(dir)
	case "profile":
		fs := flag.NewFlagSet("profile", flag.ExitOnError)
		cfg := profileConfig{}
		fs.StringVar(&cfg.label, "label", "baseline", "run label; the run is stored as profile-<label>")
		fs.StringVar(&cfg.out, "out", "../docs/benchmarks/runs", "directory that receives one sub-directory per run")
		fs.Float64Var(&cfg.rampStart, "ramp-start", 200, "first ramp rate in sagas/s (×1.5 per step)")
		fs.Float64Var(&cfg.rampMax, "ramp-max", 8000, "stop the ramp at this rate even if the SLO holds")
		fs.DurationVar(&cfg.rampHold, "ramp-hold", 6e9, "time to hold each ramp step")
		fs.StringVar(&cfg.instances, "instances", "10000,100000", "pre-populated instance counts to measure at")
		fs.StringVar(&cfg.payloads, "payloads", "100,10000,500000", "publish payload sizes in bytes")
		fs.StringVar(&cfg.timeouts, "timeouts", "100,500,2000", "simultaneous timeout counts for reaper lag")
		fs.IntVar(&cfg.pprofSeconds, "pprof-seconds", 10, "CPU profile length at the knee")
		_ = fs.Parse(os.Args[2:])
		dir, err := runProfile(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bench profile:", err)
			os.Exit(1)
		}
		fmt.Println(dir)
	case "compare":
		fs := flag.NewFlagSet("compare", flag.ExitOnError)
		out := fs.String("out", "../docs/benchmarks/comparisons", "directory that receives the comparison report")
		// Accept flags before or after the two run paths.
		_ = fs.Parse(os.Args[2:])
		args := fs.Args()
		if len(args) > 2 {
			_ = fs.Parse(args[2:])
			args = args[:2]
		}
		if len(args) != 2 {
			usage()
		}
		path, err := compareRuns(args[0], args[1], *out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bench compare:", err)
			os.Exit(1)
		}
		fmt.Println(path)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:\n  bench run [-label L] [-out DIR] [-rates 50,100,200] [-duration 20s] [-lag-tasks 200]\n  bench profile [-label L] [-out DIR] [-ramp-start 200] [-ramp-max 8000] [-instances 10000,100000] [-payloads 100,10000,500000] [-timeouts 100,500,2000]\n  bench compare RUN_A RUN_B [-out DIR]")
	os.Exit(2)
}
