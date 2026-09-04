package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
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
	fmt.Fprintln(w, "Golang Server is up and running...!")
}

// The `shutdown` function responds kubernetes graceful shutdown endpoint.
func (s *server) shutdown(w http.ResponseWriter, r *http.Request) {
	log.Println("Gracefully Shutting Down...!")

	// Disconnect Redis (rdb)
	db_connect.RDBDisconnect(s.eng.RDB)

	// Shutdown Redis (rueidis)
	db_connect.DisconnectRueidis(s.eng.Search)

	// Shutdown Postgres
	db_connect.DisconnectPostgres(s.eng.DB)

	// Shutdown HTTP Server
	err := s.srv.Shutdown(context.Background())
	if err != nil {
		log.Println("HTTP Server shutdown error: ", err)
		return
	}

	log.Println("Graceful shutdown Successfull")
	w.WriteHeader(200)
}

// The `live` function responds kubernetes live endpoint.
func (s *server) live(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check RDB (go-redis)
	res := s.eng.RDB.Ping(ctx).String()
	if res == "ping: PONG" {
		log.Println("Redis (go-redis) ping Successfully")
	} else {
		log.Println("Redis (go-redis) ping Error: ", res)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	// Check Rueidis
	cmd := s.eng.Search.B().Ping().Build()
	result := s.eng.Search.Do(ctx, cmd)
	pong, err := result.ToString()
	if err != nil {
		log.Println("Ping Redis (Rueidis) Error: ", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	} else if pong == "PONG" {
		log.Println("Redis (rueidis) ping Successfully")
	}

	// Check Postgres
	err = s.eng.DB.Ping(ctx)
	if err != nil {
		log.Println("Ping Postgres Error: ", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	log.Println("Postgres ping Successfully")

	log.Println("Server is ready")
	w.WriteHeader(200)
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

	handleFunc("/shutdown", httpTracing(s.shutdown))
	handleFunc("/live", httpTracing(s.live))
	handleFunc("/ready", httpTracing(s.live))
	handleFunc("/health", httpTracing(s.live))

	return otelhttp.NewHandler(mux, "/")
}

// The main function connects the stores, loads the DSL, starts the reaper,
// and serves HTTP on SAGAWISE_ADDR (default :5000) until interrupted.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	rdb := db_connect.DBConnect(ctx)
	search := db_connect.ConnectRueidis()
	db := db_connect.ConnectPostgres(ctx)

	templating.ParseDSL(ctx, rdb, db, envOr("SAGAWISE_DSL_DIR", "/sagawise"))

	eng := instance_engine.New(rdb, search, db)
	eng.Services = instance_engine.FileRegistry{Path: envOr("SAGAWISE_SERVICES_FILE", "services.json")}
	eng.StartDeadlineReaper(ctx, time.Second)

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

	addr := envOr("SAGAWISE_ADDR", ":5000")
	s := &server{eng: eng}
	s.srv = &http.Server{
		Addr:              addr,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       time.Second,
		WriteTimeout:      10 * time.Second,
		Handler:           s.handler(),
	}
	srvErr := make(chan error, 1)
	go func() {
		srvErr <- s.srv.ListenAndServe()
	}()

	log.Println("Server started listening on " + addr)

	select {
	case err := <-srvErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Println("HTTP server error: ", err)
		}
		return
	case <-ctx.Done():
		stop()
	}

	if err := s.srv.Shutdown(context.Background()); err != nil {
		log.Println("HTTP server shutdown error: ", err)
	}
}
