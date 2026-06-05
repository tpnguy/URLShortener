package main

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/suite"
)

// Requires TEST_POSTGRES_CONN env var, e.g.:
//   TEST_POSTGRES_CONN="host=localhost user=myuser password=mypassword dbname=mydb sslmode=disable" go test ./...

type DBSuite struct {
	suite.Suite
	db *sql.DB
}

func (s *DBSuite) SetupSuite() {
	conn := os.Getenv("TEST_POSTGRES_CONN")
	if conn == "" {
		s.T().Skip("TEST_POSTGRES_CONN not set — skipping DB tests")
	}

	db, err := sql.Open("postgres", conn)
	s.Require().NoError(err)
	s.Require().NoError(db.Ping())

	s.Require().NoError(createTable(db, "users", "id SERIAL PRIMARY KEY, email TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, created_at TIMESTAMP DEFAULT NOW()"))
	s.Require().NoError(createTable(db, "urls", "id SERIAL PRIMARY KEY, user_id INTEGER REFERENCES users(id), long_url TEXT NOT NULL, short_url VARCHAR(10) UNIQUE NOT NULL, created_at TIMESTAMP DEFAULT NOW(), expires_at TIMESTAMP"))
	s.db = db
}

func (s *DBSuite) TearDownSuite() {
	if s.db != nil {
		s.db.Close()
	}
}

func (s *DBSuite) SetupTest() {
	// Reset tables and sequences before each test so IDs are predictable
	s.db.Exec("TRUNCATE urls, users RESTART IDENTITY CASCADE")
}

// --- registerUser ---

func (s *DBSuite) TestRegisterUserReturnsID() {
	id, err := registerUser(s.db, "alice@example.com", "hash")
	s.NoError(err)
	s.Greater(id, 0)
}

func (s *DBSuite) TestRegisterDuplicateEmailFails() {
	registerUser(s.db, "alice@example.com", "hash")
	_, err := registerUser(s.db, "alice@example.com", "hash2")
	s.Error(err)
}

// --- getUserByEmail ---

func (s *DBSuite) TestGetUserByEmailReturnsHashAndID() {
	hash, _ := hashPassword("secret")
	id, _ := registerUser(s.db, "alice@example.com", hash)

	gotID, gotHash, err := getUserByEmail(s.db, "alice@example.com")
	s.NoError(err)
	s.Equal(id, gotID)
	s.True(checkPassword(gotHash, "secret"))
}

func (s *DBSuite) TestGetUserByEmailNotFound() {
	_, _, err := getUserByEmail(s.db, "nobody@example.com")
	s.Error(err)
}

// --- addURL ---

func (s *DBSuite) TestAddURLReturnsShortCode() {
	userID, _ := registerUser(s.db, "alice@example.com", "hash")
	shortCode, err := addURL(s.db, "https://example.com", userID, nil)
	s.NoError(err)
	s.NotEmpty(shortCode)
}

func (s *DBSuite) TestAddURLRejectsFTPScheme() {
	userID, _ := registerUser(s.db, "alice@example.com", "hash")
	_, err := addURL(s.db, "ftp://example.com", userID, nil)
	s.EqualError(err, "invalid URL")
}

func (s *DBSuite) TestAddURLRejectsNoScheme() {
	userID, _ := registerUser(s.db, "alice@example.com", "hash")
	_, err := addURL(s.db, "not-a-url", userID, nil)
	s.EqualError(err, "invalid URL")
}

func (s *DBSuite) TestAddURLRejectsEmpty() {
	userID, _ := registerUser(s.db, "alice@example.com", "hash")
	_, err := addURL(s.db, "", userID, nil)
	s.EqualError(err, "invalid URL")
}

// --- getURL ---

func (s *DBSuite) TestGetURLReturnsLongURL() {
	userID, _ := registerUser(s.db, "alice@example.com", "hash")
	shortCode, _ := addURL(s.db, "https://example.com", userID, nil)

	data, err := getURL(s.db, shortCode)
	s.NoError(err)
	s.Equal("https://example.com", data.LongURL)
}

func (s *DBSuite) TestGetURLNotFound() {
	_, err := getURL(s.db, "zzzzzz")
	s.Error(err)
}

func TestDBSuite(t *testing.T) {
	suite.Run(t, new(DBSuite))
}
