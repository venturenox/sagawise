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

// The `http_tracing` function logs the received request URL path and then calls the next HTTP handler
// function.
func http_tracing(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Request Received: %s\n", r.URL.Path)
		next(w, r)
	}
}

// The `ping` function in Go responds with a message indicating that the Golang Server is up and
// running.
func ping(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Golang Server is up and running...!")
}

// The function "start_instance" initiates an instance using the instance_engine package and a Redis
// database connection.
func start_instance(w http.ResponseWriter, r *http.Request) {
	instance_engine.Start_instance(r, w, rdb)
}

// The function Update_instance updates an instance using the instance_engine package.
func Update_instance(w http.ResponseWriter, r *http.Request) {
	instance_engine.Update_instance(r, w, rdb, conn)
}

// The function List_workflows handles HTTP requests to list workflows using an instance engine and a
// database connection.
func List_workflows(w http.ResponseWriter, r *http.Request) {
	instance_engine.List_workflows(r, w, rdb)
}

// The function List_workflow_instances lists workflow instances using an instance engine and client.
func List_workflow_instances(w http.ResponseWriter, r *http.Request) {
	instance_engine.List_workflow_instances(r, w, client)
}

// The function `Get_workflow_instance` retrieves a workflow instance using the instance engine and
// database connection.
func Get_workflow_instance(w http.ResponseWriter, r *http.Request) {
	instance_engine.Get_workflow_instance(r, w, rdb)
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

	handleFunc("/ping", http_tracing(ping))
	handleFunc("/start_instance", http_tracing(start_instance))
	handleFunc("/update_instance", http_tracing(Update_instance))
	handleFunc("/workflows/list", http_tracing(List_workflows))
	handleFunc("/workflow_instances/list", http_tracing(List_workflow_instances))
	handleFunc("/workflow_instances/get", http_tracing(Get_workflow_instance))

	handleFunc("/shutdown", http_tracing(shutdown))
	handleFunc("/live", http_tracing(live))
	handleFunc("/ready", http_tracing(live))
	handleFunc("/health", http_tracing(live))

	handler := otelhttp.NewHandler(mux, "/")
	return handler
}

// The main function sets up an HTTP server, handles signals for graceful shutdown, and starts
// listening on port 5000.
func main() {

	templating.ParseDSL(rdb, conn)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	otelShutdown, err := otel.SetupOTelSDK(ctx)
	defer func() {
		err = errors.Join(err, otelShutdown(ctx))
	}()

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

	select {
	case err = <-srvErr:
		return
	case <-ctx.Done():
		stop()
	}

	log.Println("Server started listening on port 5000")

	err = srv.Shutdown(context.Background())
}
