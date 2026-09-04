package instance_engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"wtfsaga/utils"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/redis/rueidis"
)

// Clock is the engine's source of time. Production uses RealClock; tests
// inject a fixed or advanceable clock so deadlines and timestamps are
// deterministic.
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

// Engine owns every dependency the saga bookkeeper needs. Construct it with
// New and override fields before serving; nothing in this package reads
// package-level state.
type Engine struct {
	// RDB is the go-redis client used for RedisJSON and FT.SEARCH commands.
	RDB *redis.Client
	// Search is the rueidis client used by ListWorkflowInstances. May be nil
	// if that endpoint is not served.
	Search rueidis.Client
	// DB is the Postgres pool holding the instance_history archive.
	DB *pgxpool.Pool

	Clock      Clock
	Services   ServiceRegistry
	HTTPClient *http.Client
}

// New returns an Engine with production defaults: wall clock, services.json
// in the working directory, and http.DefaultClient for failure webhooks.
func New(rdb *redis.Client, search rueidis.Client, db *pgxpool.Pool) *Engine {
	return &Engine{
		RDB:        rdb,
		Search:     search,
		DB:         db,
		Clock:      RealClock{},
		Services:   FileRegistry{Path: "services.json"},
		HTTPClient: http.DefaultClient,
	}
}

// instanceKey is the RedisJSON key of a workflow instance document.
func instanceKey(id string) string {
	return "workflow_instance:" + id
}
