package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof" // #nosec G108 -- registers on DefaultServeMux, which only the opt-in SAGAWISE_PPROF_ADDR listener serves; the API uses its own mux
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
	"wtfsaga/db_connect"
	"wtfsaga/instance_engine"
	"wtfsaga/otel"
	"wtfsaga/templating"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// envOr returns the environment variable or a default when it is unset.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// server wires the engine and its connections to the HTTP surface. It is the
// composition root: nothing here is package-level state.
type server struct {
	eng *instance_engine.Engine
	srv *http.Server
}

// The `httpTracing` function logs the received request URL path and then calls the next HTTP handler
// function.
func httpTracing(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Request Received: %s\n", r.URL.Path)

		w.Header().Set("Access-Control-Allow-Origin", "*") // Replace with the React app's URL
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

// The `ping` function in Go responds with a message indicating that the Golang Server is up and
// running.
func ping(w http.ResponseWriter, r *http.Request) {
	if _, err := fmt.Fprintln(w, "Golang Server is up and running...!"); err != nil {
		log.Printf("ping: write error: %v", err)
	}
}

// live serves the Kubernetes liveness and readiness probes: 200 when both
// stores answer a ping, 503 otherwise.
func (s *server) live(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := s.eng.RDB.Ping(ctx).Err(); err != nil {
		log.Println("Redis ping Error: ", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if err := s.eng.DB.Ping(ctx); err != nil {
		log.Println("Ping Postgres Error: ", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handler builds the HTTP mux with tracing for every endpoint.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()

	// otelhttp.NewHandler reads the matched ServeMux pattern from r.Pattern
	// (Go 1.23+) and records it as http.route, so no per-route wrapper is needed.
	handleFunc := func(pattern string, handlerFunc func(http.ResponseWriter, *http.Request)) {
		mux.HandleFunc(pattern, handlerFunc)
	}

	handleFunc("/ping", httpTracing(ping))
	handleFunc("/start_instance", httpTracing(s.eng.StartInstance))
	handleFunc("/update_instance", httpTracing(s.eng.UpdateInstance))
	handleFunc("/workflows/list", httpTracing(s.eng.ListWorkflows))
	handleFunc("/workflow_instances/list", httpTracing(s.eng.ListWorkflowInstances))
	handleFunc("/workflow_instances/get", httpTracing(s.eng.GetWorkflowInstance))

	// Shutdown is driven by SIGTERM/SIGINT only (Kubernetes sends SIGTERM);
	// there is no HTTP endpoint for it. (#14)
	handleFunc("/live", httpTracing(s.live))
	handleFunc("/ready", httpTracing(s.live))
	handleFunc("/health", httpTracing(s.live))

	return otelhttp.NewHandler(mux, "/")
}

// main connects the stores, loads the DSL, starts the reaper, and serves
// HTTP on SAGAWISE_ADDR (default :5000) until interrupted. Any startup
// failure exits non-zero so the orchestrator restarts the process instead
// of letting it serve half-initialized. (#8)
func main() {
	if err := run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

func run() error {
	// ctx is the process lifetime: canceled by SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rdb, err := db_connect.DBConnect(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := rdb.Close(); err != nil {
			log.Println("Redis connection close error: ", err)
		}
	}()
	db, err := db_connect.ConnectPostgres(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	workflows, err := templating.ParseDSL(ctx, rdb, db, envOr("SAGAWISE_DSL_DIR", "/sagawise"))
	if err != nil {
		return fmt.Errorf("load DSL: %w", err)
	}

	eng := instance_engine.New(rdb, db)
	eng.Services = &instance_engine.FileRegistry{Path: envOr("SAGAWISE_SERVICES_FILE", "services.json")}
	if err := instance_engine.ValidateServices(eng.Services, workflows); err != nil {
		return err
	}
	if err := eng.LoadScripts(ctx); err != nil {
		return err
	}

	otelShutdown, err := otel.SetupOTelSDK(ctx)
	if err != nil {
		log.Println("OpenTelemetry setup error: ", err)
	}
	if otelShutdown != nil {
		defer func() {
			if err := otelShutdown(context.Background()); err != nil {
				log.Println("OpenTelemetry shutdown error: ", err)
			}
		}()
	}

	// Opt-in profiling endpoint for benchmarks (make bench-profile). Never set
	// this in production: it exposes /debug/pprof on the given address.
	if pprofAddr := os.Getenv("SAGAWISE_PPROF_ADDR"); pprofAddr != "" {
		runtime.SetBlockProfileRate(10000)
		runtime.SetMutexProfileFraction(10)
		pprofSrv := &http.Server{Addr: pprofAddr, Handler: http.DefaultServeMux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			log.Println("pprof listening on " + pprofAddr)
			if err := pprofSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Println("pprof server error: ", err)
			}
		}()
	}

	// Request contexts derive from srvCtx, not the signal context, so a
	// SIGTERM lets in-flight handlers finish during Shutdown instead of
	// cancelling them mid-write.
	srvCtx, srvCancel := context.WithCancel(context.Background())
	defer srvCancel()

	addr := envOr("SAGAWISE_ADDR", ":5000")
	s := &server{eng: eng}
	s.srv = &http.Server{
		Addr:        addr,
		BaseContext: func(_ net.Listener) context.Context { return srvCtx },
		// ReadHeaderTimeout stays tight (a slowloris sends headers slowly),
		// but ReadTimeout covers the whole body: a 1 s budget cut off large
		// publish payloads on a slow link, which surfaced as a client-side
		// EOF rather than an error the caller could act on. (phase 7)
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      10 * time.Second,
		Handler:           s.handler(),
	}

	// Everything is valid and reachable: start the reaper and the queue
	// workers (which drain anything a previous process left behind), then
	// serve. Both loops are stopped explicitly below; the contexts are
	// independent of the signal context so the order of teardown is ours.
	reaperCtx, stopReaper := context.WithCancel(context.Background())
	defer stopReaper()
	eng.StartDeadlineReaper(reaperCtx, time.Second)
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	eng.StartWorkers(workerCtx)

	srvErr := make(chan error, 1)
	go func() {
		srvErr <- s.srv.ListenAndServe()
	}()
	log.Println("Server started listening on " + addr)

	select {
	case err := <-srvErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP server: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	// One ordered teardown path: drain the server, stop the reaper, stop the
	// workers (in-flight jobs finish, bounded by their timeouts; anything
	// still queued is leased in Redis and resumes on the next start), then
	// the deferred closes release the clients. (#14, design note §7)
	log.Println("Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		log.Println("HTTP server shutdown error: ", err)
	}
	stopReaper()
	stopWorkers()
	eng.StopWorkers()
	log.Println("Shutdown complete")
	return nil
}
