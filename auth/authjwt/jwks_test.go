package authjwt_test

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
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

// The refetch limit holds while the provider is failing, which is the case it
// exists for.
//
// It used to be armed only by a *successful* fetch, so with the provider down
// nothing was ever recorded and every token naming an unknown kid cost a request
// to a service that was already failing. A kid is the caller's own input, so the
// rate of those requests was the attacker's to choose — the limiter did its job
// exactly when it did not matter and stopped when it did.
func TestAFailingProviderIsStillOnlyFetchedOnce(t *testing.T) {
	var fetches atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := parser[MyClaims](t, authjwt.JWKS(srv.URL))
	for range 20 {
		_, _ = p.Parse(t.Context(), signKid(t, "unknown", rsaKey))
	}
	if n := fetches.Load(); n > 1 {
		t.Fatalf("twenty tokens against a failing provider cost %d fetches; the limit is armed by success only", n)
	}

	// The control. Everything above would hold for a JWKS that never fetched at
	// all, so a healthy provider has to be reached — once — and its key used.
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	ok := parser[MyClaims](t, authjwt.JWKS(set.serve(t)))
	if _, err := ok.Parse(t.Context(), signKid(t, "k1", rsaKey)); err != nil {
		t.Fatalf("a healthy provider's key was not used: %v", err)
	}
	if n := set.fetches.Load(); n != 1 {
		t.Fatalf("a healthy provider was fetched %d times for one token, want 1", n)
	}
}

// A burst of concurrent misses is one fetch, not one each.
//
// The lock has to be dropped across the HTTP call, so before the in-flight guard
// every goroutine in a burst passed the rate check before any of them had
// recorded an attempt. The sequential loop above cannot see this: it is the same
// hole, and only concurrency reaches it.
func TestAConcurrentBurstOfMissesIsOneFetch(t *testing.T) {
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	p := parser[MyClaims](t, authjwt.JWKS(set.serve(t)))

	tok := signKid(t, "unknown", rsaKey)
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = p.Parse(t.Context(), tok)
		}()
	}
	wg.Wait()

	if n := set.fetches.Load(); n != 1 {
		t.Fatalf("a burst of 24 concurrent misses cost %d fetches, want 1", n)
	}
}

// A key-set URL that is not there fails where it is written.
//
// It is the hardest misconfiguration in this package to diagnose from outside:
// the refusal's reason stays inside the process ([[D-056]]), so an empty URL
// answers every request with exactly the 401 a forged token gets, and nothing in
// the response or the logs says the key set was never fetched. New already
// panics on three misconfigurations for the same reason ([[D-021]]).
func TestAJWKSWithNoURLRefusesToStart(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("JWKS(%q) was accepted; every token would be refused for a reason nothing reports", raw)
				}
			}()
			authjwt.JWKS(raw)
		}()
	}

	// The control: a real URL is still accepted without contacting it. The panic
	// is about the declaration, not about the provider being up — New would
	// refuse a key source that verifies nothing, and this one does not.
	authjwt.New[MyClaims](authjwt.JWKS("https://issuer.example/.well-known/jwks.json"),
		authjwt.Issuer(issuer), authjwt.Audience(audience))
}

// One client giving up does not fail the requests parked behind it.
//
// The single-flight elects a leader to fetch and parks the rest on its result.
// The leader used to fetch on *its own* request context — and under net/http
// that is cancelled the moment that one client disconnects, so an abandoned
// request failed every waiter and, because the attempt is recorded either way,
// suppressed the refetch for the whole minRefresh window. One client walking
// away took the key set down for a minute.
func TestALeaderThatDisconnectsDoesNotFailTheWaiters(t *testing.T) {
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))

	// The server holds the first fetch until the leader has certainly gone away.
	release := make(chan struct{})
	var served atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if served.Add(1) == 1 {
			<-release
		}
		keys, _ := set.keys.Load().([]map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	}))
	defer srv.Close()

	p := parser[MyClaims](t, authjwt.JWKS(srv.URL))
	tok := signKid(t, "k1", rsaKey)

	leaderCtx, giveUp := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = p.Parse(leaderCtx, tok) // the leader, about to disconnect
	}()

	// Let the leader reach the server, then abandon it.
	for served.Load() == 0 {
		runtime.Gosched()
	}
	giveUp()

	// A waiter arrives while the fetch is in flight, and must still get its key.
	wg.Add(1)
	var waiterErr error
	go func() {
		defer wg.Done()
		_, waiterErr = p.Parse(context.Background(), tok)
	}()

	close(release)
	wg.Wait()

	if waiterErr != nil {
		t.Fatalf("a request behind an abandoned one was refused: %v — one client giving up took the key set down", waiterErr)
	}
}
