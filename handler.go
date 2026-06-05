package main

import (
	"encoding/json"
	"net/http"
	"time"
	"fmt"
)

// System Health
func (a *App) getHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	postgresStatus := "ok"
	if err := a.DB.PingContext(ctx); err != nil {
		postgresStatus = "unreachable"
	}

	redisStatus := "ok"
	if err := a.RDB.Ping(ctx).Err(); err != nil {
		redisStatus = "unreachable"
	}

	status := "ok"
	code := http.StatusOK
	if postgresStatus != "ok" || redisStatus != "ok" {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(struct {
		Status   string `json:"status"`
		Postgres string `json:"postgres"`
		Redis    string `json:"redis"`
	}{
		Status:   status,
		Postgres: postgresStatus,
		Redis:    redisStatus,
	})
}

// URL Endpoints
func (a *App) postURLHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// LongURL parsed from request
	var req struct {
		LongURL   string  `json:"long_url"`
		ExpiresAt *string `json:"expires_at"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Parse optional expiry string (e.g. "2026-12-31T00:00:00Z") into *time.Time
	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			http.Error(w, "expires_at must be RFC3339 format, e.g. 2026-12-31T00:00:00Z", http.StatusBadRequest)
			return
		}
		expiresAt = &t
	}

	// Adding to the redis and database
	userID := r.Context().Value(contextKeyUserID).(int)
	shortCode, err := addURL(a.DB, req.LongURL, userID, expiresAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.RDB.Set(ctx, "short:" + shortCode, req.LongURL, 24*time.Hour)

	// Creating JSON to send back
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(struct {
		ShortURL string `json:"short_url"`
	}{
		ShortURL: shortCode,
	})
}

func (a *App) redirectHandler(w http.ResponseWriter, r *http.Request) {

	// Generate Redis
	ctx := r.Context()
	shortCode := r.PathValue("shortCode")
	cacheKey := "short:" + shortCode

	// Tries to hit redis cache
	longURL, err := a.RDB.Get(ctx, cacheKey).Result()
	if err == nil {
		a.RDB.RPush(r.Context(), fmt.Sprintf("clicks:%d", decode(shortCode)), time.Now().UTC().Format(time.RFC3339))
		http.Redirect(w, r, longURL, http.StatusFound)
		return
	}

	// If redis cache miss
	data, err := getURL(a.DB, shortCode)
	if err != nil {
		http.Error(w, "URL not found", http.StatusNotFound)
		return
	}
	if data.DateExpires != nil && time.Now().After(*data.DateExpires) {
		http.Error(w, "Link has expired", http.StatusGone)
		return
	}
	a.RDB.RPush(r.Context(), fmt.Sprintf("clicks:%d", decode(shortCode)), time.Now().UTC().Format(time.RFC3339))
	a.RDB.Set(ctx, cacheKey, data.LongURL, 24*time.Hour)
	http.Redirect(w, r, data.LongURL, http.StatusMovedPermanently)
}

// Auth Endpoints
func (a *App) registerUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	id, err := registerUser(a.DB, req.Email, hash)
	if err != nil {
		http.Error(w, "email already in use", http.StatusConflict)
		return
	}

	token, err := generateToken(id)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(struct {
		Token string `json:"token"`
	}{Token: token})
}

func (a *App) loginUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	id, hash, err := getUserByEmail(a.DB, req.Email)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	if !checkPassword(hash, req.Password) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	token, err := generateToken(id)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Token string `json:"token"`
	}{Token: token})
}

func (a *App) listURLs(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(contextKeyUserID).(int)

	rows, err := a.DB.Query("SELECT short_url, long_url, created_at FROM urls WHERE user_id = $1", userID)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var urls []URL
	for rows.Next() {
		var u URL
		if err := rows.Scan(&u.ShortURL, &u.LongURL, &u.DateCreated); err != nil {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		urls = append(urls, u)
	}
	if urls == nil {
		urls = []URL{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(urls)
}

func (a *App) deleteURL(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(contextKeyUserID).(int)
	shortCode := r.PathValue("shortCode")
	urlID := decode(shortCode)

	var ownerID *int
	err := a.DB.QueryRow("SELECT user_id FROM urls WHERE id = $1", urlID).Scan(&ownerID)
	if err != nil {
		http.Error(w, "URL not found", http.StatusNotFound)
		return
	}
	if ownerID == nil || *ownerID != userID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if _, err = a.DB.Exec("DELETE FROM urls WHERE id = $1", urlID); err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	a.RDB.Del(r.Context(), "short:"+shortCode)

	w.WriteHeader(http.StatusNoContent)
}

