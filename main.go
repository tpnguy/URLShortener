package main

import (
	"context"
	"fmt"
	"net/http"

	// "encoding/json"
	// "errors"
	"database/sql"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

// FLow
// User puts their own URL
// URL gets randominzed into a shorter one
// The shorter one gets stored on the DB
// The server sends back the shorter URL

type URL struct {
	ID          int        `json:"id"`
	ShortURL    string     `json:"short_url"`
	LongURL     string     `json:"long_url"`
	DateCreated time.Time  `json:"date_created"`
	DateExpires *time.Time `json:"date_expires"`
}

type App struct {
	DB  *sql.DB
	RDB *redis.Client
}

func main() {
	connStr := os.Getenv("POSTGRES_CONN")
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err = db.Ping(); err != nil {
		log.Fatal(err)
	}

	if err = createTable(db, "users", "id SERIAL PRIMARY KEY, email TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, created_at TIMESTAMP DEFAULT NOW()"); err != nil {
		log.Fatal("createTable:", err)
	}

	if err = createTable(db, "urls", "id SERIAL PRIMARY KEY, user_id INTEGER REFERENCES users(id), long_url TEXT NOT NULL, short_url VARCHAR(10) UNIQUE NOT NULL, created_at TIMESTAMP DEFAULT NOW(), expires_at TIMESTAMP"); err != nil {
		log.Fatal("createTable:", err)
	}

	if err = createTable(db, "clicks", "id SERIAL PRIMARY KEY, url_id INTEGER REFERENCES urls(id), clicked_at TIMESTAMP NOT NULL"); err != nil {
		log.Fatal("createTable:", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     os.Getenv("REDIS_ADDR"),
		Password: "",
		DB:       0,
	})

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal("redis:", err)
	}
	
	if os.Getenv("JWT_SECRET") == "" {
		log.Fatal("JWT_SECRET environment variable is required")
	}

	app := &App{
		DB:  db,
		RDB: rdb,
	}

	// See current table
	fmt.Println(getTable(db, "urls"))

	// Goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			flushClicks(db, rdb)
		}
	}()

	// All Endpoints
	mux := http.NewServeMux()
	rl := rateLimitMiddleware(rdb, 20, time.Minute)

	mux.HandleFunc("POST /register", rl(http.HandlerFunc(app.registerUser)))
	mux.HandleFunc("POST /login", rl(http.HandlerFunc(app.loginUser)))
	mux.HandleFunc("GET /{shortCode}", rl(http.HandlerFunc(app.redirectHandler)))
	mux.HandleFunc("GET /health", app.getHealth)
	mux.HandleFunc("POST /urls", rl(authMiddleware(http.HandlerFunc(app.postURLHandler))))
	mux.HandleFunc("GET /urls", authMiddleware(http.HandlerFunc(app.listURLs)))
	mux.HandleFunc("DELETE /urls/{shortCode}", authMiddleware(http.HandlerFunc(app.deleteURL)))

	log.Println("Server running on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
