package authjwt_test

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	issuer   = "https://id.example.com"
	audience = "articles-api"
)

var secret = []byte("a shared secret long enough to be one")

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	return k
}

var rsaKey = func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
}()

func claims() jwt.MapClaims {
	return jwt.MapClaims{
		"sub":  "u-1",
		"iss":  issuer,
		"aud":  audience,
		"exp":  time.Now().Add(time.Hour).Unix(),
		"iat":  time.Now().Unix(),
		"role": "editor",
	}
}

func sign(t *testing.T, method jwt.SigningMethod, key any, c jwt.MapClaims) string {
	t.Helper()
	tok, err := jwt.NewWithClaims(method, c).SignedString(key)
	if err != nil {
		t.Fatalf("signing the fixture: %v", err)
	}
	return tok
}

func signHS(t *testing.T, c jwt.MapClaims) string {
	return sign(t, jwt.SigningMethodHS256, secret, c)
}
