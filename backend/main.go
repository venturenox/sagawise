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

var srv = &http.Server{}
var rdb = db_connect.DBConnect()
var client = db_connect.ConnectRueidis()
var conn = db_connect.ConnectPostgres()

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

// The function "startInstance" initiates an instance using the instance_engine package and a Redis
// database connection.
func startInstance(w http.ResponseWriter, r *http.Request) {
	instance_engine.StartInstance(r, w, rdb)
}

// The function updateInstance updates an instance using the instance_engine package.
func updateInstance(w http.ResponseWriter, r *http.Request) {
	instance_engine.UpdateInstance(r, w, rdb, conn)
}

// The function listWorkflows handles HTTP requests to list workflows using an instance engine and a
// database connection.
func listWorkflows(w http.ResponseWriter, r *http.Request) {
	instance_engine.ListWorkflows(w, rdb)
}

// The function listWorkflowInstances lists workflow instances using an instance engine and client.
func listWorkflowInstances(w http.ResponseWriter, r *http.Request) {
	instance_engine.ListWorkflowInstances(r, w, client)
}

// The function `getWorkflowInstance` retrieves a workflow instance using the instance engine and
// database connection.
func getWorkflowInstance(w http.ResponseWriter, r *http.Request) {
	instance_engine.GetWorkflowInstance(r, w, rdb)
}

// The `shutdown` function responds kubernetes graceful shutdown endpoint.
func shutdown(w http.ResponseWriter, r *http.Request) {
	log.Println("Gracefully Shitting Down...!")

	// Disconnect Redis (rdb)
	db_connect.RDBDisconnect(rdb)

	// Shutdown Redis (rueidis)
	db_connect.DisconnectRueidis(client)

	// Shutdown Postgres
	db_connect.DisconnectPostgres(conn)

	// Shutdown HTTP Server
	err := srv.Shutdown(context.Background())
	if err != nil {
		log.Println("HTTP Server shutdown error: ", err)
		return
	}

	log.Println("Graceful shutdown Successfull")
	w.WriteHeader(200)
}

// The `live` function responds kubernetes live endpoint.
func live(w http.ResponseWriter, r *http.Request) {

	// Check RDB (go-redis)
	res := rdb.Ping(context.Background()).String()
	if res == "ping: PONG" {
		log.Println("Redis (go-redis) ping Successfully")
	} else {
		log.Println("Redis (go-redis) ping Error: ", res)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	// Check Rueidis
	cmd := client.B().Ping().Build()
	result := client.Do(context.Background(), cmd)
	pong, err := result.ToString()
	if err != nil {
		log.Println("Ping Redis (Rueidis) Error: ", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	} else if pong == "PONG" {
		log.Println("Redis (rueidis) ping Successfully")
	}

	// Check Postgres
	err = conn.Ping(context.Background())
	if err != nil {
		log.Println("Ping Postgres Error: ", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	log.Println("Postgres ping Successfully")

	log.Println("Server is ready")
	w.WriteHeader(200)
}

// The function `newHTTPHandler` creates a new HTTP handler with tracing for various endpoints.
func newHTTPHandler() http.Handler {
	mux := http.NewServeMux()

	handleFunc := func(pattern string, handlerFunc func(http.ResponseWriter, *http.Request)) {
		handler := otelhttp.WithRouteTag(pattern, http.HandlerFunc(handlerFunc))
		mux.Handle(pattern, handler)
	}

	handleFunc("/ping", httpTracing(ping))
	handleFunc("/start_instance", httpTracing(startInstance))
	handleFunc("/update_instance", httpTracing(updateInstance))
	handleFunc("/workflows/list", httpTracing(listWorkflows))
	handleFunc("/workflow_instances/list", httpTracing(listWorkflowInstances))
	handleFunc("/workflow_instances/get", httpTracing(getWorkflowInstance))

	handleFunc("/shutdown", httpTracing(shutdown))
	handleFunc("/live", httpTracing(live))
	handleFunc("/ready", httpTracing(live))
	handleFunc("/health", httpTracing(live))

	handler := otelhttp.NewHandler(mux, "/")
	return handler
}

// The main function sets up an HTTP server, handles signals for graceful shutdown, and starts
// listening on port 5000.
func main() {

	templating.ParseDSL(rdb, conn)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	instance_engine.StartDeadlineReaper(ctx, rdb, conn, time.Second)

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

	srv = &http.Server{
		Addr:         ":5000",
		BaseContext:  func(_ net.Listener) context.Context { return ctx },
		ReadTimeout:  time.Second,
		WriteTimeout: 10 * time.Second,
		Handler:      newHTTPHandler(),
	}
	srvErr := make(chan error, 1)
	go func() {
		srvErr <- srv.ListenAndServe()
	}()

	log.Println("Server started listening on port 5000")

	select {
	case err := <-srvErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Println("HTTP server error: ", err)
		}
		return
	case <-ctx.Done():
		stop()
	}

	if err := srv.Shutdown(context.Background()); err != nil {
		log.Println("HTTP server shutdown error: ", err)
	}
}
