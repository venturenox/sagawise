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
	"wtfsaga/httpsec"
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
	sec securityConfig
}

// httpTracing logs the request path. CORS and authentication are no longer
// done here: they are the httpsec middleware installed in handler().
func httpTracing(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Request Received: %s\n", r.URL.Path)
		next(w, r)
	}
}

// securityConfig is what phase 8 reads from the environment. Every field
// has a fail-closed default: no key means the API refuses everything, no
// origin means no cross-origin browser access, no secret means unsigned
// webhooks (allowed, but warned about). docs/threat-model.md.
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

	// Order, outermost first: body cap, CORS (so a refused preflight and a
	// 401 both carry the right headers), then authentication, then the
	// routes. The probes are exempt from the key so Kubernetes can reach
	// them; nothing else is.
	var h http.Handler = mux
	if !s.sec.authOff {
		h = httpsec.NewAPIKeys(s.sec.apiKeys, "/live", "/ready", "/health").Wrap(h)
	}
	h = httpsec.NewCORS(s.sec.corsOrigins).Wrap(h)
	h = httpsec.MaxBody(s.sec.maxBody, h)
	return otelhttp.NewHandler(h, "/")
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

	// Configuration is checked before any connection is made, so a bad
	// value fails in under a second rather than after the Postgres retry.
	sec, err := loadSecurityConfig()
	if err != nil {
		return err
	}
	if sec.authOff {
		log.Println("WARNING: SAGAWISE_AUTH=off: the API accepts unauthenticated requests")
	}
	if sec.webhookSecret == "" {
		log.Println("WARNING: SAGAWISE_WEBHOOK_SECRET is empty: failure webhooks are not signed")
	}
	if len(sec.corsOrigins) == 0 {
		log.Println("CORS: no origins allowed (SAGAWISE_CORS_ORIGINS is empty)")
	}

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
	eng.WebhookSecret = []byte(sec.webhookSecret)
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
	s := &server{eng: eng, sec: sec}
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
