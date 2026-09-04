package db_connect

import (
	"context"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"github.com/redis/rueidis"
)

// DBConnect builds the go-redis client from REDIS_CONNECTION_STRING or
// REDIS_HOST/REDIS_PORT and pings it once.
func DBConnect(ctx context.Context) *redis.Client {
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
			log.Println("Error: ", err)
		}

		rdb = redis.NewClient(&redis.Options{
			Addr:     options.Addr,
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       0,
		})
	}

	redisotel.InstrumentTracing(rdb)
	redisotel.InstrumentMetrics(rdb)

	if rdb.Ping(ctx).String() == "ping: PONG" {
		log.Println("Redis connected Successfully")
	}

	return rdb
}

func RDBDisconnect(rdb *redis.Client) {
	err := rdb.Close()
	if err != nil {
		log.Println("Redis (go-redis) connection close error: ", err)
	} else {
		log.Println("Redis (go-redis) connection closed successfully")
	}
}

// ConnectRueidis builds the rueidis client from REDIS_HOST/REDIS_PORT.
func ConnectRueidis() rueidis.Client {

	// Connect to a single redis node:
	client, _ := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{os.Getenv("REDIS_HOST") + ":" + os.Getenv("REDIS_PORT")},
		SelectDB:    0,
		Password:    os.Getenv("REDIS_PASSWORD"),
	})

	return client
}

func DisconnectRueidis(client rueidis.Client) {
	client.Close()
	log.Println("Redis (Rueidis) connection closed successfully")
}

// ConnectPostgres builds the pgx pool from POSTGRES_* and waits (up to 30
// attempts, one per second) for a successful ping.
func ConnectPostgres(ctx context.Context) *pgxpool.Pool {

	pool, err := pgxpool.New(ctx, PostgresURL())
	if err != nil {
		log.Printf("Unable to connect to database: %v\n", err)
		return pool
	}

	// The pool dials lazily, but startup (templating.ParseDSL) needs Postgres to
	// actually be up; under docker compose it may still be booting, so wait for
	// a successful ping before proceeding.
	for attempt := 1; ; attempt++ {
		if err := pool.Ping(ctx); err == nil {
			log.Println("Postgres connected Successfully")
			break
		} else if attempt >= 30 {
			log.Printf("Postgres not reachable after %d attempts: %v", attempt, err)
			break
		}
		time.Sleep(time.Second)
	}

	return pool
}

// PostgresURL builds the connection string from the POSTGRES_* variables.
func PostgresURL() string {
	return "postgres://" + os.Getenv("POSTGRES_USERNAME") + ":" +
		url.QueryEscape(os.Getenv("POSTGRES_PASSWORD")) + "@" + os.Getenv("POSTGRES_HOST") +
		":" + os.Getenv("POSTGRES_PORT") + "/" + os.Getenv("POSTGRES_DATABASE")
}

func DisconnectPostgres(pool *pgxpool.Pool) {
	pool.Close()
	log.Println("Postgres connection pool closed successfully")
}
