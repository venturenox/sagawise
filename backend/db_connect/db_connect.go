package db_connect

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

// DBConnect builds the go-redis client from REDIS_CONNECTION_STRING or
// REDIS_HOST/REDIS_PORT and pings it once. A client that cannot reach Redis
// is an error, not a warning: the process must not serve without its store.
func DBConnect(ctx context.Context) (*redis.Client, error) {
	var rdb *redis.Client

	if os.Getenv("REDIS_CONNECTION_STRING") == "" {
		rdb = redis.NewClient(&redis.Options{
			Addr:     os.Getenv("REDIS_HOST") + ":" + os.Getenv("REDIS_PORT"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       0,
		})
	} else {
		options, err := redis.ParseURL(os.Getenv("REDIS_CONNECTION_STRING"))
		if err != nil {
			return nil, fmt.Errorf("parse REDIS_CONNECTION_STRING: %w", err)
		}

		rdb = redis.NewClient(&redis.Options{
			Addr:     options.Addr,
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       0,
		})
	}

	if err := redisotel.InstrumentTracing(rdb); err != nil {
		log.Printf("Redis tracing instrumentation error: %v", err)
	}
	if err := redisotel.InstrumentMetrics(rdb); err != nil {
		log.Printf("Redis metrics instrumentation error: %v", err)
	}

	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis at %s: %w", rdb.Options().Addr, err)
	}
	log.Println("Redis connected Successfully")
	return rdb, nil
}

// ConnectPostgres builds the pgx pool from POSTGRES_* and waits (up to 30
// attempts, one per second) for a successful ping. It returns an error if
// Postgres never answers.
func ConnectPostgres(ctx context.Context) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, PostgresURL())
	if err != nil {
		return nil, fmt.Errorf("postgres config: %w", err)
	}

	// The pool dials lazily, but startup (templating.ParseDSL) needs Postgres to
	// actually be up; under docker compose it may still be booting, so wait for
	// a successful ping before proceeding.
	const attempts = 30
	for attempt := 1; ; attempt++ {
		err := pool.Ping(ctx)
		if err == nil {
			log.Println("Postgres connected Successfully")
			return pool, nil
		}
		if attempt >= attempts {
			pool.Close()
			return nil, fmt.Errorf("postgres not reachable after %d attempts: %w", attempt, err)
		}
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, fmt.Errorf("postgres connect: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// PostgresURL builds the connection string from the POSTGRES_* variables.
func PostgresURL() string {
	return "postgres://" + os.Getenv("POSTGRES_USERNAME") + ":" +
		url.QueryEscape(os.Getenv("POSTGRES_PASSWORD")) + "@" + os.Getenv("POSTGRES_HOST") +
		":" + os.Getenv("POSTGRES_PORT") + "/" + os.Getenv("POSTGRES_DATABASE")
}
