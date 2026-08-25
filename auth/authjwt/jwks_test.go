package authjwt_test

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/shardit-io/vv/auth/authjwt"
)

// jwkOf renders an RSA public key the way a provider publishes one.
func jwkOf(kid string, pub *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// keySet serves a set the test can rewrite, and counts what it was asked for.
type keySet struct {
	fetches atomic.Int64
	keys    atomic.Value // []map[string]any
}

func (k *keySet) set(keys ...map[string]any) { k.keys.Store(keys) }

func (k *keySet) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		k.fetches.Add(1)
		keys, _ := k.keys.Load().([]map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func signKid(t *testing.T, kid string, key *rsa.PrivateKey) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims())
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("signing the fixture: %v", err)
	}
	return s
}

func TestAKeySetVerifiesATokenAndIsFetchedOnce(t *testing.T) {
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	url := set.serve(t)

	p := parser[MyClaims](t, authjwt.JWKS(url))
	tok := signKid(t, "k1", rsaKey)

	for range 3 {
		if _, err := p.Parse(t.Context(), tok); err != nil {
			t.Fatalf("a token signed by a published key was refused: %v", err)
		}
	}
	if n := set.fetches.Load(); n != 1 {
		t.Fatalf("the key set was fetched %d times for three tokens, want 1", n)
	}
}

// A rotation is a new kid the cached set does not have. It must be picked up,
// and the old key must stop working once the provider stops publishing it.
func TestARotatedKidIsPickedUp(t *testing.T) {
	next := newRSAKey(t)
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	url := set.serve(t)

	p := parser[MyClaims](t, authjwt.JWKS(url, authjwt.JWKSMinRefreshEvery(time.Nanosecond)))
	if _, err := p.Parse(t.Context(), signKid(t, "k1", rsaKey)); err != nil {
		t.Fatalf("the first key did not work: %v", err)
	}

	set.set(jwkOf("k2", &next.PublicKey))
	if _, err := p.Parse(t.Context(), signKid(t, "k2", next)); err != nil {
		t.Fatalf("the rotated key was not picked up: %v", err)
	}

	// The control: rotation is not "accept everything". A key the provider no
	// longer publishes must stop verifying.
	if _, err := p.Parse(t.Context(), signKid(t, "k1", rsaKey)); err == nil {
		t.Fatal("a retired key still verified after the set stopped publishing it")
	}
}

// The rate limit is what stops one forged token per fetch from becoming a
// denial-of-service against the identity provider.
func TestUnknownKidsDoNotBecomeOneFetchEach(t *testing.T) {
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	url := set.serve(t)

	p := parser[MyClaims](t, authjwt.JWKS(url))
	for i := range 20 {
		_, _ = p.Parse(t.Context(), signKid(t, "unknown", rsaKey))
		_ = i
	}
	if n := set.fetches.Load(); n > 1 {
		t.Fatalf("twenty tokens naming an unknown kid cost %d fetches; a forged token per fetch is a DoS on the provider", n)
	}
}

// A key set is a public document, so an HMAC entry in one is a shared secret
// published to the internet. It must never be usable.
func TestASymmetricKeyInAKeySetIsNotUsable(t *testing.T) {
	set := &keySet{}
	set.set(map[string]any{
		"kty": "oct",
		"kid": "k1",
		"use": "sig",
		"k":   base64.RawURLEncoding.EncodeToString(secret),
	})
	url := set.serve(t)

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims())
	tok.Header["kid"] = "k1"
	signed, err := tok.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := parser[MyClaims](t, authjwt.JWKS(url)).Parse(t.Context(), signed); err == nil {
		t.Fatal("an HMAC key published in a key set was used to verify a token")
	}
}

func TestAKeySetThatCannotBeReachedIsARefusalAndNotAPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	if _, err := parser[MyClaims](t, authjwt.JWKS(srv.URL)).Parse(t.Context(), signKid(t, "k1", rsaKey)); err == nil {
		t.Fatal("a key set that answered 500 was treated as a successful verification")
	}
}

func TestAnEntryTheSetCannotRenderDoesNotCostTheWholeSet(t *testing.T) {
	set := &keySet{}
	set.set(
		map[string]any{"kty": "EC", "kid": "broken", "use": "sig", "crv": "P-999", "x": "AA", "y": "AA"},
		jwkOf("k1", &rsaKey.PublicKey),
	)
	url := set.serve(t)

	if _, err := parser[MyClaims](t, authjwt.JWKS(url)).Parse(t.Context(), signKid(t, "k1", rsaKey)); err != nil {
		t.Fatalf("one unusable entry cost the whole key set: %v", err)
	}
}
