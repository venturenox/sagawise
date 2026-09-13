package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof" // #nosec G108 -- registers on DefaultServeMux, which only the opt-in SAGAWISE_PPROF_ADDR listener serves; the API uses its own mux
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
	"wtfsaga/db_connect"
	"wtfsaga/httpsec"
	"wtfsaga/instance_engine"
	"wtfsaga/logging"
	"wtfsaga/otel"
	"wtfsaga/templating"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	otelapi "go.opentelemetry.io/otel"
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
	sec securityConfig
	log *slog.Logger
}

// securityConfig is what phase 8 reads from the environment. Every field
// has a fail-closed default: no key means the API refuses everything, no
// origin means no cross-origin browser access, no secret means unsigned
// webhooks (allowed, but warned about). docs/security.md.
type securityConfig struct {
	authOff       bool     // SAGAWISE_AUTH=off: serve without API keys
	apiKeys       []string // SAGAWISE_API_KEYS: comma-separated bearer tokens
	corsOrigins   []string // SAGAWISE_CORS_ORIGINS: comma-separated exact origins
	webhookSecret string   // SAGAWISE_WEBHOOK_SECRET: HMAC key for failure webhooks
	maxBody       int64    // SAGAWISE_MAX_BODY_BYTES: cap on a request body (default 1M)
}

// loadSecurityConfig reads and validates the phase 8 settings. The process
// must not serve an unauthenticated API by accident: with no key and no
// explicit SAGAWISE_AUTH=off it refuses to start.
func loadSecurityConfig() (securityConfig, error) {
	c := securityConfig{
		apiKeys:       httpsec.ParseList(os.Getenv("SAGAWISE_API_KEYS")),
		corsOrigins:   httpsec.ParseList(os.Getenv("SAGAWISE_CORS_ORIGINS")),
		webhookSecret: os.Getenv("SAGAWISE_WEBHOOK_SECRET"),
	}
	switch mode := os.Getenv("SAGAWISE_AUTH"); mode {
	case "", "api-key":
		if len(c.apiKeys) == 0 {
			return c, errors.New("SAGAWISE_API_KEYS is empty: set at least one API key, or SAGAWISE_AUTH=off to serve an unauthenticated API (development only)")
		}
	case "off":
		c.authOff = true
	default:
		return c, fmt.Errorf("SAGAWISE_AUTH=%q: want \"api-key\" (default) or \"off\"", mode)
	}
	for _, o := range c.corsOrigins {
		if o == "*" {
			return c, errors.New("SAGAWISE_CORS_ORIGINS: \"*\" is not an origin; list the exact origins that may call the API from a browser")
		}
	}
	n, err := httpsec.ParseBytes(envOr("SAGAWISE_MAX_BODY_BYTES", "1M"))
	if err != nil {
		return c, fmt.Errorf("SAGAWISE_MAX_BODY_BYTES: %w", err)
	}
	c.maxBody = n
	return c, nil
}

// The `ping` function in Go responds with a message indicating that the Golang Server is up and
// running.
func ping(w http.ResponseWriter, r *http.Request) {
	if _, err := fmt.Fprintln(w, "Golang Server is up and running...!"); err != nil {
		logging.From(r.Context(), nil).Warn("ping: write error", "err", err)
	}
}

// writeHealth answers a probe: 200 with the report when it is healthy, 503
// otherwise. The body names each check and its state (phase 9).
func writeHealth(w http.ResponseWriter, rep instance_engine.HealthReport) {
	w.Header().Set("Content-Type", "application/json")
	if !rep.Healthy() {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(rep)
}

// live is the liveness probe: the reaper and the queue workers are ticking.
// It does not touch the stores, so a store outage never restarts the pod.
func (s *server) live(w http.ResponseWriter, r *http.Request) {
	writeHealth(w, s.eng.Liveness())
}

// ready is the readiness probe (and /health): liveness plus the stores.
// Redis down is 503; Postgres down is 200 "degraded" because the API keeps
// working and the archive queue catches up (contract A2, A3).
func (s *server) ready(w http.ResponseWriter, r *http.Request) {
	writeHealth(w, s.eng.Readiness(r.Context()))
}

// handler builds the HTTP mux with tracing for every endpoint.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()

	// otelhttp.NewHandler reads the matched ServeMux pattern from r.Pattern
	// (Go 1.23+) and records it as http.route, so no per-route wrapper is needed.
	mux.HandleFunc("/ping", ping)
	mux.HandleFunc("/start_instance", s.eng.StartInstance)
	mux.HandleFunc("/update_instance", s.eng.UpdateInstance)
	mux.HandleFunc("/workflows/list", s.eng.ListWorkflows)
	mux.HandleFunc("/workflow_instances/list", s.eng.ListWorkflowInstances)
	mux.HandleFunc("/workflow_instances/get", s.eng.GetWorkflowInstance)

	// Shutdown is driven by SIGTERM/SIGINT only (Kubernetes sends SIGTERM);
	// there is no HTTP endpoint for it. (#14)
	mux.HandleFunc("/live", s.live)
	mux.HandleFunc("/ready", s.ready)
	mux.HandleFunc("/health", s.ready)

	// Order, outermost first: access log (so refusals by the layers below
	// are logged too), body cap, CORS (so a refused preflight and a 401
	// both carry the right headers), then authentication, then the routes.
	// The probes are exempt from the key so Kubernetes can reach them;
	// nothing else is.
	var h http.Handler = mux
	if !s.sec.authOff {
		h = httpsec.NewAPIKeys(s.sec.apiKeys, "/live", "/ready", "/health").Wrap(h)
	}
	h = httpsec.NewCORS(s.sec.corsOrigins).Wrap(h)
	h = httpsec.MaxBody(s.sec.maxBody, h)
	h = logging.Middleware{
		Logger:        s.log,
		InstanceParam: "workflow_instance_id",
		Params:        []string{"workflow_name", "action_type", "event_name", "service_name"},
		Quiet:         []string{"/live", "/ready", "/health"},
	}.Wrap(h)
	return otelhttp.NewHandler(h, "/")
}

// checkRedisPersistence enforces SAGAWISE_REDIS_AOF: "require" (default)
// exits when Redis reports appendonly no, because task_deadlines and the
// queues would not survive a Redis restart (contract T5, A2); "warn" logs
// instead; "off" skips the check. A server that refuses CONFIG GET (managed
// Redis) cannot be checked and gets a warning under every mode.
func checkRedisPersistence(ctx context.Context, l *slog.Logger, eng *instance_engine.Engine) error {
	mode := envOr("SAGAWISE_REDIS_AOF", "require")
	switch mode {
	case "require", "warn":
	case "off":
		return nil
	default:
		return fmt.Errorf("SAGAWISE_REDIS_AOF=%q: want require (default), warn or off", mode)
	}
	on, known, err := instance_engine.RedisAppendOnly(ctx, eng.RDB)
	if err != nil {
		return fmt.Errorf("check redis persistence: %w", err)
	}
	switch {
	case !known:
		l.Warn("redis persistence unknown: CONFIG GET appendonly is not permitted; make sure AOF is on (docs/operations.md)")
	case on:
		l.Info("redis persistence: appendonly yes")
	case mode == "require":
		return errors.New("redis has appendonly no: task deadlines and queues would not survive a restart; enable AOF (REDIS_ARGS=\"--appendonly yes\" for redis-stack) or set SAGAWISE_REDIS_AOF=warn (docs/operations.md)")
	default:
		l.Warn("redis has appendonly no: task deadlines and queues will not survive a Redis restart (docs/operations.md)")
	}
	return nil
}

// main connects the stores, loads the DSL, starts the reaper, and serves
// HTTP on SAGAWISE_ADDR (default :5000) until interrupted. Any startup
// failure exits non-zero so the orchestrator restarts the process instead
// of letting it serve half-initialized. (#8)
func main() {
	// The logger comes first so every later line, including a fatal one,
	// is structured. A bad logging setting is the one error that cannot be
	// logged structurally; it goes to stderr as is.
	l, err := logging.Setup(os.Stdout, os.Getenv("SAGAWISE_LOG_FORMAT"), os.Getenv("SAGAWISE_LOG_LEVEL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
	if err := run(l); err != nil {
		l.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(l *slog.Logger) error {
	// ctx is the process lifetime: canceled by SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Configuration is checked before any connection is made, so a bad
	// value fails in under a second rather than after the Postgres retry.
	sec, err := loadSecurityConfig()
	if err != nil {
		return err
	}
	if sec.authOff {
		l.Warn("SAGAWISE_AUTH=off: the API accepts unauthenticated requests")
	}
	if sec.webhookSecret == "" {
		l.Warn("SAGAWISE_WEBHOOK_SECRET is empty: failure webhooks are not signed")
	}
	if len(sec.corsOrigins) == 0 {
		l.Info("CORS: no origins allowed (SAGAWISE_CORS_ORIGINS is empty)")
	}
	metricsAddr := envOr("SAGAWISE_METRICS_ADDR", ":9464")

	// The OpenTelemetry providers go in before anything creates an
	// instrument: redisotel in DBConnect, otelhttp, the engine's metrics.
	sdk, err := otel.Setup(ctx)
	if err != nil {
		return fmt.Errorf("OpenTelemetry setup: %w", err)
	}
	defer func() {
		if err := sdk.Shutdown(context.Background()); err != nil {
			l.Warn("OpenTelemetry shutdown error", "err", err)
		}
	}()

	rdb, err := db_connect.DBConnect(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := rdb.Close(); err != nil {
			l.Warn("redis close error", "err", err)
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
	eng.Log = l
	eng.Services = &instance_engine.FileRegistry{Path: envOr("SAGAWISE_SERVICES_FILE", "services.json")}
	eng.WebhookSecret = []byte(sec.webhookSecret)
	if err := instance_engine.ValidateServices(eng.Services, workflows); err != nil {
		return err
	}
	if err := eng.LoadScripts(ctx); err != nil {
		return err
	}
	if err := eng.UseMeterProvider(otelapi.GetMeterProvider()); err != nil {
		return err
	}
	if err := checkRedisPersistence(ctx, l, eng); err != nil {
		return err
	}

	// Metrics are served on their own listener (default :9464, "off" to
	// disable), never on the API port: the endpoint needs no key, so it
	// must not be reachable through the ingress (threat model).
	if metricsAddr != "off" {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", sdk.Metrics)
		metricsSrv := &http.Server{Addr: metricsAddr, Handler: metricsMux, ReadHeaderTimeout: 5 * time.Second}
		metricsLn, err := net.Listen("tcp", metricsAddr)
		if err != nil {
			return fmt.Errorf("metrics listener on %s: %w (set SAGAWISE_METRICS_ADDR to another address, or off)", metricsAddr, err)
		}
		go func() {
			l.Info("metrics listening", "addr", metricsLn.Addr().String(), "path", "/metrics")
			if err := metricsSrv.Serve(metricsLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
				l.Error("metrics server error", "err", err)
			}
		}()
		defer func() { _ = metricsSrv.Close() }()
	}

	// Opt-in profiling endpoint for benchmarks (make bench-profile). Never set
	// this in production: it exposes /debug/pprof on the given address.
	if pprofAddr := os.Getenv("SAGAWISE_PPROF_ADDR"); pprofAddr != "" {
		runtime.SetBlockProfileRate(10000)
		runtime.SetMutexProfileFraction(10)
		pprofSrv := &http.Server{Addr: pprofAddr, Handler: http.DefaultServeMux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			l.Info("pprof listening", "addr", pprofAddr)
			if err := pprofSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				l.Error("pprof server error", "err", err)
			}
		}()
	}

	// Request contexts derive from srvCtx, not the signal context, so a
	// SIGTERM lets in-flight handlers finish during Shutdown instead of
	// cancelling them mid-write.
	srvCtx, srvCancel := context.WithCancel(context.Background())
	defer srvCancel()

	addr := envOr("SAGAWISE_ADDR", ":5000")
	s := &server{eng: eng, sec: sec, log: l}
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
	l.Info("server listening", "addr", addr)

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
	l.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		l.Warn("HTTP server shutdown error", "err", err)
	}
	stopReaper()
	stopWorkers()
	eng.StopWorkers()
	l.Info("shutdown complete")
	return nil
}
