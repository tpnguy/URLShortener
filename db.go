package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)


// SQL Functions
func createTable(db *sql.DB, name string, columns string) error {
	fmt.Println("Creating table:", name)
	query := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", name, columns)
	_, err := db.Exec(query)
	if err != nil {
		return err
	}
	return nil
}

func getTable(db *sql.DB, table string) error {
	query := fmt.Sprintf("SELECT id, long_url, short_url, created_at FROM %s", table)
	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var longURL, shortURL, createdAt string
		if err := rows.Scan(&id, &longURL, &shortURL, &createdAt); err != nil {
			return err
		}
		fmt.Printf("id: %d | long_url: %s | short_url: %s | created_at: %s\n", id, longURL, shortURL, createdAt)
	}

	return rows.Err()
}

func registerUser(db *sql.DB, email string, passwordHash string) (int, error) {
	var id int
	err := db.QueryRow(
		"INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id", email, passwordHash,
	).Scan(&id)
	return id, err
}

func getUserByEmail(db *sql.DB, email string) (int, string, error){
	var id int
	var hash string
	err := db.QueryRow(
		"SELECT id, password_hash FROM users WHERE email = $1", email,
	).Scan(&id, &hash)
	return id, hash, err
}


// Utility Functions 
func encode(id int) string {
	alphabet := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	if id == 0 {
		return "0"
	}
	var result []byte
	for id > 0 {
		result = append([]byte{alphabet[id%62]}, result...)
		id = id / 62
	}
	return string(result)
}
func decode(url string) int {
	alphabet := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	result := 0
	for _, char := range url{
		result = result * 62 + strings.IndexRune(alphabet, char)
	}
	return result

}

// URL Operations
func addURL(db *sql.DB, long_url string, userID int, expiresAt *time.Time) (string, error) {
	path, err := url.Parse(long_url)
	if err != nil || (path.Scheme != "http" && path.Scheme != "https") || path.Host == "" {
		return "", fmt.Errorf("invalid URL")
	}

	var nextID int
	if err = db.QueryRow("SELECT nextval('urls_id_seq')").Scan(&nextID); err != nil {
		return "", err
	}

	encoded := encode(nextID)

	_, err = db.Exec("INSERT INTO urls (id, user_id, long_url, short_url, expires_at) VALUES ($1, $2, $3, $4, $5)", nextID, userID, long_url, encoded, expiresAt)
	if err != nil {
		return "", err
	}
	return encoded, nil
}

func getURL(db *sql.DB, shortCode string) (URL, error) {
	var data URL	
	decoded := decode(shortCode)
	err := db.QueryRow("SELECT long_url, created_at, expires_at FROM urls WHERE id = $1", decoded).Scan(&data.LongURL, &data.DateCreated, &data.DateExpires)
	if err != nil {
		return URL{}, err
	}
	return data, nil
}



// Goroutine
func flushClicks(db *sql.DB, rdb *redis.Client) {
	ctx := context.Background()

	// Find every Redis key that looks like "clicks:123"
	keys, err := rdb.Keys(ctx, "clicks:*").Result()
	if err != nil {
		return
	}

	for _, key := range keys {
		// How many timestamps are queued for this URL right now?
		count, err := rdb.LLen(ctx, key).Result()
		if err != nil || count == 0 {
			continue
		}

		// Read exactly those entries from 0 to last
		timestamps, err := rdb.LRange(ctx, key, 0, count-1).Result()
		if err != nil {
			continue
		}

		// Trim the list to remove only the entries we just read.
		// Any new clicks that arrived during this loop stay safe in Redis.
		rdb.LTrim(ctx, key, count, -1)

		// "clicks:123" → 123
		urlID, err := strconv.Atoi(strings.TrimPrefix(key, "clicks:"))
		if err != nil {
			continue
		}

		// One DB row per timestamp
		for _, ts := range timestamps {
			clickedAt, err := time.Parse(time.RFC3339, ts)
			if err != nil {
				continue
			}
			db.Exec("INSERT INTO clicks (url_id, clicked_at) VALUES ($1, $2)", urlID, clickedAt)
		}
	}
}