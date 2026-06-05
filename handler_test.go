package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/suite"
)

// Requires TEST_POSTGRES_CONN env var. Redis is handled in-process via miniredis.

type HandlerSuite struct {
	suite.Suite
	app     *App
	db      *sql.DB
	miniRDB *miniredis.Miniredis
}

func (s *HandlerSuite) SetupSuite() {
	conn := os.Getenv("TEST_POSTGRES_CONN")
	if conn == "" {
		s.T().Skip("TEST_POSTGRES_CONN not set — skipping handler tests")
	}
	os.Setenv("JWT_SECRET", "handler-test-secret")

	db, err := sql.Open("postgres", conn)
	s.Require().NoError(err)
	s.Require().NoError(db.Ping())

	s.Require().NoError(createTable(db, "users", "id SERIAL PRIMARY KEY, email TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, created_at TIMESTAMP DEFAULT NOW()"))
	s.Require().NoError(createTable(db, "urls", "id SERIAL PRIMARY KEY, user_id INTEGER REFERENCES users(id), long_url TEXT NOT NULL, short_url VARCHAR(10) UNIQUE NOT NULL, created_at TIMESTAMP DEFAULT NOW(), expires_at TIMESTAMP"))
	s.db = db

	mr, err := miniredis.Run()
	s.Require().NoError(err)
	s.miniRDB = mr

	s.app = &App{DB: db, RDB: redis.NewClient(&redis.Options{Addr: mr.Addr()})}
}

func (s *HandlerSuite) TearDownSuite() {
	if s.db != nil {
		s.db.Close()
	}
	if s.miniRDB != nil {
		s.miniRDB.Close()
	}
}

func (s *HandlerSuite) SetupTest() {
	s.db.Exec("TRUNCATE urls, users RESTART IDENTITY CASCADE")
	s.miniRDB.FlushAll()
}

// --- helpers ---

// jsonReq builds a POST request with a JSON body and optional Bearer token.
func (s *HandlerSuite) jsonReq(method, path string, body any, token string) (*httptest.ResponseRecorder, *http.Request) {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return httptest.NewRecorder(), req
}

// seed creates a user and returns their (id, token) without going through the HTTP layer.
func (s *HandlerSuite) seed(email, password string) (int, string) {
	hash, _ := hashPassword(password)
	id, _ := registerUser(s.db, email, hash)
	token, _ := generateToken(id)
	return id, token
}

// withUserID injects a userID into the request context, simulating what authMiddleware does.
// Use this when calling a handler directly (not through a mux) to avoid a nil context panic.
func withUserID(r *http.Request, userID int) *http.Request {
	ctx := context.WithValue(r.Context(), contextKeyUserID, userID)
	return r.WithContext(ctx)
}

// routeWith builds a one-route mux so path values (e.g. {shortCode}) are populated correctly.
func (s *HandlerSuite) routeWith(pattern string, handler http.HandlerFunc) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc(pattern, handler)
	return mux
}

// --- POST /register ---

func (s *HandlerSuite) TestRegisterReturns201AndToken() {
	rr, req := s.jsonReq("POST", "/register", map[string]string{"email": "alice@example.com", "password": "secret"}, "")
	s.app.registerUser(rr, req)

	s.Equal(http.StatusCreated, rr.Code)
	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	s.NotEmpty(resp["token"])
}

func (s *HandlerSuite) TestRegisterDuplicateEmailReturns409() {
	s.seed("alice@example.com", "secret")

	rr, req := s.jsonReq("POST", "/register", map[string]string{"email": "alice@example.com", "password": "other"}, "")
	s.app.registerUser(rr, req)
	s.Equal(http.StatusConflict, rr.Code)
}

func (s *HandlerSuite) TestRegisterBadBodyReturns400() {
	req := httptest.NewRequest("POST", "/register", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.app.registerUser(rr, req)
	s.Equal(http.StatusBadRequest, rr.Code)
}

// --- POST /login ---

func (s *HandlerSuite) TestLoginReturnsToken() {
	s.seed("alice@example.com", "secret")

	rr, req := s.jsonReq("POST", "/login", map[string]string{"email": "alice@example.com", "password": "secret"}, "")
	s.app.loginUser(rr, req)

	s.Equal(http.StatusOK, rr.Code)
	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	s.NotEmpty(resp["token"])
}

func (s *HandlerSuite) TestLoginWrongPasswordReturns401() {
	s.seed("alice@example.com", "secret")

	rr, req := s.jsonReq("POST", "/login", map[string]string{"email": "alice@example.com", "password": "wrong"}, "")
	s.app.loginUser(rr, req)
	s.Equal(http.StatusUnauthorized, rr.Code)
}

func (s *HandlerSuite) TestLoginUnknownEmailReturns401() {
	rr, req := s.jsonReq("POST", "/login", map[string]string{"email": "ghost@example.com", "password": "x"}, "")
	s.app.loginUser(rr, req)
	s.Equal(http.StatusUnauthorized, rr.Code)
}

// --- POST /urls ---

func (s *HandlerSuite) TestPostURLReturnsShortCode() {
	id, _ := s.seed("alice@example.com", "secret")

	rr, req := s.jsonReq("POST", "/urls", map[string]string{"long_url": "https://example.com"}, "")
	s.app.postURLHandler(rr, withUserID(req, id))

	s.Equal(http.StatusCreated, rr.Code)
	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	s.NotEmpty(resp["short_url"])
}

func (s *HandlerSuite) TestPostURLNoTokenReturns401() {
	mux := s.routeWith("POST /urls", authMiddleware(http.HandlerFunc(s.app.postURLHandler)))
	b, _ := json.Marshal(map[string]string{"long_url": "https://example.com"})
	req := httptest.NewRequest("POST", "/urls", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	s.Equal(http.StatusUnauthorized, rr.Code)
}

func (s *HandlerSuite) TestPostURLInvalidURLReturns400() {
	id, _ := s.seed("alice@example.com", "secret")

	rr, req := s.jsonReq("POST", "/urls", map[string]string{"long_url": "ftp://bad"}, "")
	s.app.postURLHandler(rr, withUserID(req, id))
	s.Equal(http.StatusBadRequest, rr.Code)
}

// --- GET /urls ---

func (s *HandlerSuite) TestListURLsReturnsOwnURLs() {
	id, _ := s.seed("alice@example.com", "secret")
	addURL(s.db, "https://example.com", id, nil)

	rr, req := s.jsonReq("GET", "/urls", nil, "")
	s.app.listURLs(rr, withUserID(req, id))

	s.Equal(http.StatusOK, rr.Code)
	var urls []map[string]any
	json.NewDecoder(rr.Body).Decode(&urls)
	s.Len(urls, 1)
	s.Equal("https://example.com", urls[0]["long_url"])
}

func (s *HandlerSuite) TestListURLsDoesNotReturnOtherUsersURLs() {
	idA, _ := s.seed("alice@example.com", "secret")
	idB, _ := s.seed("bob@example.com", "secret")
	addURL(s.db, "https://example.com", idA, nil)

	rr, req := s.jsonReq("GET", "/urls", nil, "")
	s.app.listURLs(rr, withUserID(req, idB))

	var urls []map[string]any
	json.NewDecoder(rr.Body).Decode(&urls)
	s.Len(urls, 0)
}

// --- DELETE /urls/{shortCode} ---
// Route through a mux (without auth middleware) so r.PathValue("shortCode") is populated.

func (s *HandlerSuite) TestDeleteOwnURLReturns204() {
	id, _ := s.seed("alice@example.com", "secret")
	short, _ := addURL(s.db, "https://example.com", id, nil)

	mux := s.routeWith("DELETE /urls/{shortCode}", http.HandlerFunc(s.app.deleteURL))
	req := httptest.NewRequest("DELETE", "/urls/"+short, nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, withUserID(req, id))

	s.Equal(http.StatusNoContent, rr.Code)
}

func (s *HandlerSuite) TestDeleteOtherUsersURLReturns403() {
	idA, _ := s.seed("alice@example.com", "secret")
	idB, _ := s.seed("bob@example.com", "secret")
	short, _ := addURL(s.db, "https://example.com", idA, nil)

	mux := s.routeWith("DELETE /urls/{shortCode}", http.HandlerFunc(s.app.deleteURL))
	req := httptest.NewRequest("DELETE", "/urls/"+short, nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, withUserID(req, idB))

	s.Equal(http.StatusForbidden, rr.Code)
}

func (s *HandlerSuite) TestDeleteNonExistentURLReturns404() {
	id, _ := s.seed("alice@example.com", "secret")

	mux := s.routeWith("DELETE /urls/{shortCode}", http.HandlerFunc(s.app.deleteURL))
	req := httptest.NewRequest("DELETE", "/urls/zzzzzz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, withUserID(req, id))

	s.Equal(http.StatusNotFound, rr.Code)
}

func TestHandlerSuite(t *testing.T) {
	suite.Run(t, new(HandlerSuite))
}
