package db_connect

import (
	"context"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"github.com/redis/rueidis"
)

var ctx = context.Background()
var rdb *redis.Client

func DBConnect() *redis.Client {

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

func ConnectPostgres() *pgx.Conn {

	time.Sleep(3 * time.Second)
	var conn *pgx.Conn
	var err error

	conn_str := "postgres://" + os.Getenv("POSTGRES_USERNAME") + ":" +
		url.QueryEscape(os.Getenv("POSTGRES_PASSWORD")) + "@" + os.Getenv("POSTGRES_HOST") +
		":" + os.Getenv("POSTGRES_PORT") + "/" + os.Getenv("POSTGRES_DB")

	conn, err = pgx.Connect(context.Background(), conn_str)
	if err != nil {
		log.Printf("Unable to connect to database: %v\n", err)
	}

	return conn
}

func DisconnectPostgres(conn *pgx.Conn) {
	err := conn.Close(context.Background())
	if err != nil {
		log.Println("Postgres connection close error: ", err)
	} else {
		log.Println("Postgres connection closed successfully")
	}
}
