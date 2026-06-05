package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/suite"
)

// --- Encode / Decode (pure, no dependencies) ---

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
		id      int
		encoded string
	}{
		{1, "1"},
		{10, "a"},
		{61, "Z"},
		{62, "10"},
	}
	for _, c := range cases {
		if got := encode(c.id); got != c.encoded {
			t.Errorf("encode(%d) = %q, want %q", c.id, got, c.encoded)
		}
		if got := decode(c.encoded); got != c.id {
			t.Errorf("decode(%q) = %d, want %d", c.encoded, got, c.id)
		}
	}
}

// --- Auth suite (hash + JWT) ---

type AuthSuite struct {
	suite.Suite
}

func (s *AuthSuite) SetupSuite() {
	os.Setenv("JWT_SECRET", "test-secret-key")
}

func (s *AuthSuite) TestHashAndVerifyCorrectPassword() {
	hash, err := hashPassword("mysecret")
	s.NoError(err)
	s.NotEmpty(hash)
	s.True(checkPassword(hash, "mysecret"))
}

func (s *AuthSuite) TestHashRejectsWrongPassword() {
	hash, _ := hashPassword("mysecret")
	s.False(checkPassword(hash, "wrongpassword"))
}

func (s *AuthSuite) TestGenerateAndParseToken() {
	token, err := generateToken(42)
	s.NoError(err)
	s.NotEmpty(token)

	userID, err := parseToken(token)
	s.NoError(err)
	s.Equal(42, userID)
}

func (s *AuthSuite) TestParseGarbageToken() {
	_, err := parseToken("garbage.token.value")
	s.Error(err)
}

func (s *AuthSuite) TestParseTokenSignedWithWrongSecret() {
	// Token signed with a different secret should be rejected
	os.Setenv("JWT_SECRET", "other-secret")
	token, _ := generateToken(1)
	os.Setenv("JWT_SECRET", "test-secret-key")

	_, err := parseToken(token)
	s.Error(err)
}

func TestAuthSuite(t *testing.T) {
	suite.Run(t, new(AuthSuite))
}
