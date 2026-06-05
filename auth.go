package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

type contextKey string
const contextKeyUserID contextKey = "userID"

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

func checkPassword(hashedPass string, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hashedPass), []byte(password)) == nil
}

func generateToken(userID int) (string, error){
	claims := jwt.MapClaims{
		"user_id": userID,
		"exp": time.Now().Add(30 * time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(os.Getenv("JWT_SECRET")))
}

func parseToken(tokenStr string) (int, error){
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(os.Getenv("JWT_SECRET")), nil
	})
	if err != nil || !token.Valid {
		return 0, fmt.Errorf("invalid token")
	}
	claims := token.Claims.(jwt.MapClaims)
	userID := int(claims["user_id"].(float64))
	return userID, nil
}

func authMiddleware(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request){
		header := r.Header.Get("Authorization")
		if header == "" || len(header) < 8 || header[:7] != "Bearer " {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		userID, err := parseToken(header[7:])
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), contextKeyUserID, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func rateLimitMiddleware(rdb *redis.Client, limit int, window time.Duration) func(http.Handler) http.HandlerFunc {
	return func(next http.Handler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// Prefer X-Real-IP set by nginx; fall back to RemoteAddr for direct connections
			ip := r.Header.Get("X-Real-IP")
			if ip == "" {
				ip, _, _ = net.SplitHostPort(r.RemoteAddr)
				if ip == "" {
					ip = r.RemoteAddr
				}
			}

			key := "ratelimit:" + ip
			ctx := r.Context()

			count, err := rdb.Incr(ctx, key).Result()
			if err != nil {
				http.Error(w, "server error", http.StatusInternalServerError)
				return
			}
			// Only set the expiry on the first increment so the window starts fresh
			if count == 1 {
				rdb.Expire(ctx, key, window)
			}
			if count > int64(limit) {
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		}
	}
}