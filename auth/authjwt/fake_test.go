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

// newRSAKey mints a fresh key for the tests that need a second one.
func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	return k
}

// rsaKey is generated once: 2048-bit keygen is slow enough that one per subtest
// would dominate the suite.
var rsaKey = func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
}()

// claims is a well-formed payload: the right issuer, the right audience, and an
// hour left to run. Every test starts from it and breaks one thing.
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

// signHS is the ordinary token every control case uses.
func signHS(t *testing.T, c jwt.MapClaims) string {
	return sign(t, jwt.SigningMethodHS256, secret, c)
}
