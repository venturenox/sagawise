package instance_engine

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"wtfsaga/utils"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// WebhookTimeout bounds one failure-webhook delivery attempt (contract W3).
const WebhookTimeout = 5 * time.Second

// Queue keys. Members are added by transition.lua in the same atomic step as
// the state change they follow from; the workers drain them.
const (
	archiveQueueKey    = "archive_pending"
	archiveAttemptsKey = "archive_attempts"
	webhookQueueKey    = "webhook_pending"
	webhookAttemptsKey = "webhook_attempts"
)

// Worker tuning (design note §5).
const (
	workerInterval = time.Second
	workerLease    = 30 * time.Second
	workerBatch    = 1000

	archiveTimeout = 10 * time.Second

	// Webhook delivery: 2 s tripling, capped at 5 min, 8 attempts (≈15 min).
	webhookMaxAttempts = 8
	webhookParallel    = 16
)

// Clock is the engine's source of time. Production uses RealClock; tests
// inject a fixed or advanceable clock so deadlines, stamps and queue
// scheduling are deterministic.
type Clock interface {
	Now() time.Time
}

// RealClock is the wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// ServiceRegistry resolves a service name to the webhook Sagawise POSTs to
// when one of that service's published tasks fails. An empty URL with a nil
// error means "no webhook registered".
type ServiceRegistry interface {
	FailureURL(service string) (string, error)
}

// FileRegistry reads a services.json file on every lookup, so edits to the
// file are picked up without a restart. Caching is a roadmap phase 7 item.
type FileRegistry struct {
	Path string
}

func (f FileRegistry) FailureURL(service string) (string, error) {
	data, err := os.ReadFile(f.Path) // #nosec G304 -- path is operator configuration, not request input
	if err != nil {
		return "", fmt.Errorf("read %s: %w", f.Path, err)
	}
	var services []utils.Service
	if err := json.Unmarshal(data, &services); err != nil {
		return "", fmt.Errorf("parse %s: %w", f.Path, err)
	}
	for _, s := range services {
		if s.ServiceName == service {
			return s.FailureUrl, nil
		}
	}
	return "", nil
}

// MapRegistry is an in-memory ServiceRegistry, mainly for tests.
type MapRegistry map[string]string

func (m MapRegistry) FailureURL(service string) (string, error) {
	return m[service], nil
}

// ValidateServices checks that every publishing (`from`) service in the
// workflows has a failure_url in the registry (contract W5). A missing
// webhook is a startup error, not a runtime surprise. (#8)
func ValidateServices(reg ServiceRegistry, workflows []utils.Workflow) error {
	for _, wf := range workflows {
		for i, task := range wf.Tasks {
			url, err := reg.FailureURL(task.From)
			if err != nil {
				return fmt.Errorf("service registry: %w", err)
			}
			if url == "" {
				return fmt.Errorf("workflow %q task %d (%s): publishing service %q has no failure_url in the service registry", wf.Name, i, task.Topic, task.From)
			}
		}
	}
	return nil
}

//go:embed transition.lua
var transitionSource string

// Engine owns every dependency the saga bookkeeper needs. Construct it with
// New and override fields before serving; nothing in this package reads
// package-level state.
type Engine struct {
	// RDB is the go-redis client used for RedisJSON, FT.SEARCH and scripts.
	RDB *redis.Client
	// DB is the Postgres pool holding the instance_history archive.
	DB *pgxpool.Pool

	Clock      Clock
	Services   ServiceRegistry
	HTTPClient *http.Client

	// WebhookSecret, when set, makes every failure webhook carry an HMAC
	// signature the receiver can verify (contract W6, package webhooksig).
	// Empty means unsigned deliveries; main warns about that at startup.
	WebhookSecret []byte

	// Archiver drains archive_pending into Postgres; Webhooks drains
	// webhook_pending to the publishers' failure_urls. Start them with
	// StartWorkers; tests drive their ticks directly instead.
	Archiver *Worker
	Webhooks *Worker

	script *redis.Script
}

// New returns an Engine with production defaults: wall clock, services.json
// in the working directory, a webhook client bounded by WebhookTimeout, and
// the two queue workers configured but not started.
func New(rdb *redis.Client, db *pgxpool.Pool) *Engine {
	e := &Engine{
		RDB:        rdb,
		DB:         db,
		Clock:      RealClock{},
		Services:   FileRegistry{Path: "services.json"},
		HTTPClient: &http.Client{Timeout: WebhookTimeout},
		script:     redis.NewScript(transitionSource),
	}
	e.Archiver = &Worker{
		Name: "archive", Interval: workerInterval, Lease: workerLease, Batch: workerBatch,
		Parallel: 1, Timeout: archiveTimeout,
		Backoff: expBackoff(time.Second, 2, 30*time.Second),
		Work:    e.archiveJob,
		queue:   &zqueue{rdb: rdb, key: archiveQueueKey, attempts: archiveAttemptsKey},
		wake:    make(chan struct{}, 1),
	}
	e.Webhooks = &Worker{
		Name: "webhook", Interval: workerInterval, Lease: workerLease, Batch: workerBatch,
		Parallel: webhookParallel, Timeout: WebhookTimeout + time.Second,
		Backoff:     expBackoff(2*time.Second, 3, 5*time.Minute),
		MaxAttempts: webhookMaxAttempts,
		Work:        e.webhookJob,
		queue:       &zqueue{rdb: rdb, key: webhookQueueKey, attempts: webhookAttemptsKey},
		wake:        make(chan struct{}, 1),
	}
	// The workers read the clock through the engine so a test that swaps
	// e.Clock after New still drives them.
	e.Archiver.clock = clockFunc(func() time.Time { return e.Clock.Now() })
	e.Webhooks.clock = e.Archiver.clock
	return e
}

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }

// StartWorkers starts the archive and webhook workers; they drain whatever
// a previous process left queued. StopWorkers waits for them after ctx is
// cancelled.
func (e *Engine) StartWorkers(ctx context.Context) {
	e.Archiver.Start(ctx)
	e.Webhooks.Start(ctx)
}

func (e *Engine) StopWorkers() {
	e.Archiver.Stop()
	e.Webhooks.Stop()
}

// LoadScripts loads the transition script into Redis so the first report
// does not pay for an EVAL fallback, and fails fast on a script that does
// not compile.
func (e *Engine) LoadScripts(ctx context.Context) error {
	if err := e.script.Load(ctx, e.RDB).Err(); err != nil {
		return fmt.Errorf("load transition script: %w", err)
	}
	if err := claimScript.Load(ctx, e.RDB).Err(); err != nil {
		return fmt.Errorf("load claim script: %w", err)
	}
	return nil
}

// instanceKeyPrefix is the key namespace of workflow instance documents.
// reap_batch rebuilds an instance key from a deadline member, so the prefix
// is passed to the script rather than duplicated in Lua.
const instanceKeyPrefix = "workflow_instance:"

// instanceKey is the RedisJSON key of a workflow instance document.
func instanceKey(id string) string {
	return instanceKeyPrefix + id
}
