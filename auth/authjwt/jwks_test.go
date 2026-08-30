package authjwt_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/authjwt"
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

func ecJWK(kid, alg string, pub *ecdsa.PublicKey) map[string]any {
	curve := map[string]string{
		elliptic.P256().Params().Name: "P-256",
		elliptic.P384().Params().Name: "P-384",
		elliptic.P521().Params().Name: "P-521",
	}[pub.Curve.Params().Name]
	return map[string]any{
		"kty": "EC", "kid": kid, "use": "sig", "alg": alg, "crv": curve,
		"x": base64.RawURLEncoding.EncodeToString(pub.X.Bytes()),
		"y": base64.RawURLEncoding.EncodeToString(pub.Y.Bytes()),
	}
}

func edJWK(kid, alg string, pub ed25519.PublicKey) map[string]any {
	return map[string]any{
		"kty": "OKP", "kid": kid, "use": "sig", "alg": alg, "crv": "Ed25519",
		"x": base64.RawURLEncoding.EncodeToString(pub),
	}
}

func signECDSAWithoutLibraryCurveCheck(t *testing.T, method *jwt.SigningMethodECDSA, key *ecdsa.PrivateKey) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims())
	signingString, err := token.SigningString()
	if err != nil {
		t.Fatal(err)
	}
	h := method.Hash.New()
	_, _ = h.Write([]byte(signingString))
	r, s, err := ecdsa.Sign(rand.Reader, key, h.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, 2*method.KeySize)
	r.FillBytes(signature[:method.KeySize])
	s.FillBytes(signature[method.KeySize:])
	return signingString + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func receiveDegraded(t *testing.T, ch <-chan authjwt.JWKSDegraded) authjwt.JWKSDegraded {
	t.Helper()
	select {
	case state := <-ch:
		return state
	case <-time.After(time.Second):
		t.Fatal("the degraded observer was not called")
		return authjwt.JWKSDegraded{}
	}
}

// keySet serves a set the test can rewrite, and counts what it was asked for.
type keySet struct {
	fetches atomic.Int64
	status  atomic.Int64
	keys    atomic.Value // []map[string]any
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (this *keySet) set(keys ...map[string]any) { this.keys.Store(keys) }
func (this *keySet) fail(status int)            { this.status.Store(int64(status)) }
func (this *keySet) recover()                   { this.status.Store(0) }

func (this *keySet) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		this.fetches.Add(1)
		if status := this.status.Load(); status != 0 {
			w.WriteHeader(int(status))
			return
		}
		keys, _ := this.keys.Load().([]map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0)} }

func (this *fakeClock) Now() time.Time {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.now
}

func (this *fakeClock) Advance(d time.Duration) {
	this.mu.Lock()
	this.now = this.now.Add(d)
	this.mu.Unlock()
}

func signKid(t *testing.T, kid string, key *rsa.PrivateKey) string {
	return signRSAKid(t, jwt.SigningMethodRS256, kid, key)
}

func signRSAKid(t *testing.T, method jwt.SigningMethod, kid string, key *rsa.PrivateKey) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, claims())
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

func TestARetiredCachedKidStopsVerifyingAtTheFreshnessBoundary(t *testing.T) {
	const freshFor = 2 * time.Minute
	next := newRSAKey(t)
	clock := newFakeClock()
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	p := parser[MyClaims](t, authjwt.JWKS(set.serve(t),
		authjwt.JWKSClock(clock.Now),
		authjwt.JWKSStaleAfter(freshFor),
	))

	oldToken := signKid(t, "k1", rsaKey)
	if _, err := p.Parse(t.Context(), oldToken); err != nil {
		t.Fatalf("the initially published key did not verify: %v", err)
	}
	set.set(jwkOf("k2", &next.PublicKey))

	clock.Advance(freshFor - time.Nanosecond)
	if _, err := p.Parse(t.Context(), oldToken); err != nil {
		t.Fatalf("the key was retired before its documented freshness boundary: %v", err)
	}
	if n := set.fetches.Load(); n != 1 {
		t.Fatalf("a fresh cache was fetched %d times, want 1", n)
	}

	clock.Advance(time.Nanosecond)
	if _, err := p.Parse(t.Context(), oldToken); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("a key withdrawn at the provider answered %v at the freshness boundary, want a credential refusal", err)
	}
	if n := set.fetches.Load(); n != 2 {
		t.Fatalf("the stale cache was fetched %d times, want the boundary refresh", n)
	}

	// The control: the successful refresh did not disable verification. It
	// replaced the whole trust set, and the provider's current key works.
	if _, err := p.Parse(t.Context(), signKid(t, "k2", next)); err != nil {
		t.Fatalf("the provider's replacement key did not verify: %v", err)
	}
}

func TestStaleOnErrorIsBoundedAndSignalsTheDegradedDecision(t *testing.T) {
	const (
		freshFor = 2 * time.Minute
		maxStale = 3 * time.Minute
	)
	clock := newFakeClock()
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	degraded := make(chan authjwt.JWKSDegraded, 2)
	p := parser[MyClaims](t, authjwt.JWKS(set.serve(t),
		authjwt.JWKSClock(clock.Now),
		authjwt.JWKSStaleAfter(freshFor),
		authjwt.JWKSServeStaleFor(maxStale, func(_ context.Context, state authjwt.JWKSDegraded) {
			degraded <- state
		}),
	))
	token := signKid(t, "k1", rsaKey)

	if _, err := p.Parse(t.Context(), token); err != nil {
		t.Fatalf("warming the key set: %v", err)
	}
	set.fail(http.StatusServiceUnavailable)

	// The control: an outage does not trigger a fetch or a degraded decision
	// while the cached set is still fresh.
	if _, err := p.Parse(t.Context(), token); err != nil {
		t.Fatalf("a fresh cached key stopped working during an outage: %v", err)
	}
	select {
	case <-degraded:
		t.Fatal("a fresh hit produced a degraded signal")
	default:
	}
	if set.fetches.Load() != 1 {
		t.Fatalf("a fresh hit produced %d fetches", set.fetches.Load())
	}

	clock.Advance(freshFor)
	if _, err := p.Parse(t.Context(), token); err != nil {
		t.Fatalf("the explicitly bounded stale-on-error window was not used: %v", err)
	}
	state := receiveDegraded(t, degraded)
	if state.Cause == nil || state.Age != freshFor || state.FreshFor != freshFor || state.MaxStaleFor != maxStale {
		t.Fatalf("the degraded descriptor does not describe the trust decision: %+v", state)
	}

	clock.Advance(maxStale)
	_, err := p.Parse(t.Context(), token)
	if !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
		t.Fatalf("an outage beyond max stale answered %v, want ErrKeySourceUnavailable", err)
	}
	if errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("an outage beyond max stale was reported as a bad credential: %v", err)
	}
	select {
	case <-degraded:
		t.Fatal("an expired stale policy emitted another degraded decision")
	default:
	}
}

func TestAStaleCacheDoesNotHideAnOutageByDefault(t *testing.T) {
	clock := newFakeClock()
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	p := parser[MyClaims](t, authjwt.JWKS(set.serve(t), authjwt.JWKSClock(clock.Now)))
	token := signKid(t, "k1", rsaKey)
	if _, err := p.Parse(t.Context(), token); err != nil {
		t.Fatalf("warming the key set: %v", err)
	}

	set.fail(http.StatusServiceUnavailable)
	clock.Advance(authjwt.JWKSFreshness)
	_, err := p.Parse(t.Context(), token)
	if !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
		t.Fatalf("the default freshness policy hid an outage behind a stale key: %v", err)
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
	if n := set.fetches.Load(); n != 1 {
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

func TestAKeySetThatCannotBeReachedIsUnavailableAndNotARefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	_, err := parser[MyClaims](t, authjwt.JWKS(srv.URL)).Parse(t.Context(), signKid(t, "k1", rsaKey))
	if !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
		t.Fatalf("a key set that answered 500 produced %v, want ErrKeySourceUnavailable", err)
	}
	if errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("a provider outage was reported as a bad credential: %v", err)
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

func TestAnEmptyOrDuplicateKidRefusesTheWholeKeySet(t *testing.T) {
	next := newRSAKey(t)
	for _, tc := range []struct {
		name string
		keys []map[string]any
	}{
		{
			name: "empty kid beside a usable key",
			keys: []map[string]any{jwkOf("k1", &rsaKey.PublicKey), jwkOf("", &next.PublicKey)},
		},
		{
			name: "duplicate kid",
			keys: []map[string]any{jwkOf("k1", &rsaKey.PublicKey), jwkOf("k1", &next.PublicKey)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := &keySet{}
			set.set(tc.keys...)
			_, err := parser[MyClaims](t, authjwt.JWKS(set.serve(t))).Parse(t.Context(), signKid(t, "k1", rsaKey))
			if !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
				t.Fatalf("an ambiguous key set answered %v, want ErrKeySourceUnavailable", err)
			}
			if errors.Is(err, auth.ErrUnauthenticated) {
				t.Fatalf("an invalid provider document was reported as a bad credential: %v", err)
			}
		})
	}

	// The control: rejecting the whole ambiguous document is not a parser that
	// rejects the same key when it is published exactly once with a real id.
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	if _, err := parser[MyClaims](t, authjwt.JWKS(set.serve(t))).Parse(t.Context(), signKid(t, "k1", rsaKey)); err != nil {
		t.Fatalf("one unambiguous key did not verify: %v", err)
	}
}

func TestANonPositiveMinRefreshRefusesToStart(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Nanosecond} {
		t.Run(d.String(), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("JWKSMinRefreshEvery(%s) silently removed the outbound request bound", d)
				}
			}()
			authjwt.JWKS("https://issuer.example/jwks", authjwt.JWKSMinRefreshEvery(d))
		})
	}

	// The control: removing the bound is still possible, but the unsafe choice
	// is named where the provider request rate is configured.
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	p := parser[MyClaims](t, authjwt.JWKS(set.serve(t), authjwt.UnsafeJWKSNoMinRefresh()))
	unknown := signKid(t, "unknown", rsaKey)
	_, _ = p.Parse(t.Context(), unknown)
	_, _ = p.Parse(t.Context(), unknown)
	if n := set.fetches.Load(); n != 2 {
		t.Fatalf("the explicit unsafe waiver made %d fetches for two misses, want 2", n)
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
		_, err := p.Parse(t.Context(), signKid(t, "unknown", rsaKey))
		if !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
			t.Fatalf("a rate-limited provider outage became %v, want ErrKeySourceUnavailable", err)
		}
		if errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("a repeated provider outage was reported as a bad credential: %v", err)
		}
	}
	if n := fetches.Load(); n != 1 {
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
	leaderDone := make(chan error, 1)
	go func() {
		_, err := p.Parse(leaderCtx, tok) // the initiator, about to disconnect
		leaderDone <- err
	}()

	// Let the leader reach the server, then abandon it.
	for served.Load() == 0 {
		runtime.Gosched()
	}
	giveUp()
	select {
	case err := <-leaderDone:
		if err != context.Canceled {
			t.Fatalf("the cancelled initiator answered %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the fetch initiator did not return promptly on its own cancellation")
	}

	// A waiter arrives while the fetch is in flight, and must still get its key.
	var wg sync.WaitGroup
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
	if n := served.Load(); n != 1 {
		t.Fatalf("initiator cancellation caused %d provider fetches, want the one shared fetch", n)
	}
}

func TestAWaiterCanCancelWithoutStoppingTheSharedFetch(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var fetches atomic.Int64
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		close(started)
		<-release
		keys, _ := set.keys.Load().([]map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	}))
	t.Cleanup(srv.Close)

	p := parser[MyClaims](t, authjwt.JWKS(srv.URL))
	token := signKid(t, "k1", rsaKey)
	leaderDone := make(chan error, 1)
	go func() {
		_, err := p.Parse(context.Background(), token)
		leaderDone <- err
	}()
	<-started

	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		_, err := p.Parse(waiterCtx, token)
		waiterDone <- err
	}()
	cancelWaiter()
	select {
	case err := <-waiterDone:
		if err != context.Canceled {
			t.Fatalf("the cancelled waiter answered %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("a waiter remained parked behind shared work after its context was cancelled")
	}

	close(release)
	if err := <-leaderDone; err != nil {
		t.Fatalf("a waiter's cancellation stopped the shared fetch: %v", err)
	}
	if n := fetches.Load(); n != 1 {
		t.Fatalf("one shared fetch became %d provider requests", n)
	}
}

func TestAConcurrentFailedRefreshSharesOneErrorAndOneFetch(t *testing.T) {
	providerDown := errors.New("provider transport is down")
	started := make(chan struct{})
	release := make(chan struct{})
	var fetches atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		if fetches.Add(1) == 1 {
			close(started)
		}
		<-release
		return nil, providerDown
	})}
	p := parser[MyClaims](t, authjwt.JWKS("https://issuer.example/jwks", authjwt.JWKSClient(client)))
	token := signKid(t, "unknown", rsaKey)

	const callers = 32
	begin := make(chan struct{})
	errs := make(chan error, callers)
	for range callers {
		go func() {
			<-begin
			_, err := p.Parse(context.Background(), token)
			errs <- err
		}()
	}
	close(begin)
	<-started
	// Keep the transport at the barrier long enough for the rest of the burst to
	// join. Even a late scheduler still observes the failed-attempt rate limit.
	for range callers {
		runtime.Gosched()
	}
	close(release)

	for range callers {
		err := <-errs
		if !errors.Is(err, authjwt.ErrKeySourceUnavailable) || !errors.Is(err, providerDown) {
			t.Fatalf("a waiter received %v, want the shared typed provider error", err)
		}
		if errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("a failed shared refresh became a credential refusal: %v", err)
		}
	}
	if n := fetches.Load(); n != 1 {
		t.Fatalf("one concurrent failed refresh made %d transport calls", n)
	}
}

func TestAZeroOriginClockStillRecordsFetchAndAttemptState(t *testing.T) {
	t.Run("successful fetch becomes stale", func(t *testing.T) {
		const freshFor = 2 * time.Minute
		clock := &fakeClock{}
		next := newRSAKey(t)
		set := &keySet{}
		set.set(jwkOf("k1", &rsaKey.PublicKey))
		p := parser[MyClaims](t, authjwt.JWKS(set.serve(t),
			authjwt.JWKSClock(clock.Now),
			authjwt.JWKSStaleAfter(freshFor),
		))
		old := signKid(t, "k1", rsaKey)
		if _, err := p.Parse(t.Context(), old); err != nil {
			t.Fatalf("initial fetch at the zero time: %v", err)
		}
		set.set(jwkOf("k2", &next.PublicKey))
		clock.Advance(freshFor)
		if _, err := p.Parse(t.Context(), old); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("a zero-time successful fetch never aged: %v", err)
		}
		if n := set.fetches.Load(); n != 2 {
			t.Fatalf("the zero-time freshness boundary made %d fetches, want 2", n)
		}
	})

	t.Run("failed attempt arms the rate limit", func(t *testing.T) {
		clock := &fakeClock{}
		set := &keySet{}
		set.set(jwkOf("k1", &rsaKey.PublicKey))
		set.fail(http.StatusServiceUnavailable)
		p := parser[MyClaims](t, authjwt.JWKS(set.serve(t), authjwt.JWKSClock(clock.Now)))
		token := signKid(t, "k1", rsaKey)
		for range 2 {
			if _, err := p.Parse(t.Context(), token); !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
				t.Fatalf("the failed zero-time attempt answered %v", err)
			}
		}
		if n := set.fetches.Load(); n != 1 {
			t.Fatalf("a failed attempt at the zero time made %d fetches, want 1", n)
		}
	})
}

func TestDetachedFetchTimeoutCanUseTheExplicitStaleWindow(t *testing.T) {
	const freshFor = time.Minute
	clock := newFakeClock()
	var fetches atomic.Int64
	degraded := make(chan authjwt.JWKSDegraded, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fetches.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{jwkOf("k1", &rsaKey.PublicKey)}})
			return
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	p := parser[MyClaims](t, authjwt.JWKS(srv.URL,
		authjwt.JWKSClient(&http.Client{Timeout: 25 * time.Millisecond}),
		authjwt.JWKSClock(clock.Now),
		authjwt.JWKSStaleAfter(freshFor),
		authjwt.JWKSServeStaleFor(time.Minute, func(_ context.Context, state authjwt.JWKSDegraded) {
			degraded <- state
		}),
	))
	token := signKid(t, "k1", rsaKey)
	if _, err := p.Parse(t.Context(), token); err != nil {
		t.Fatalf("warming the set: %v", err)
	}
	clock.Advance(freshFor)
	if _, err := p.Parse(t.Context(), token); err != nil {
		t.Fatalf("the detached provider timeout did not use the bounded stale key: %v", err)
	}
	state := receiveDegraded(t, degraded)
	if state.Cause == nil || !errors.Is(state.Cause, context.DeadlineExceeded) {
		t.Fatalf("the degraded signal did not retain the fetch timeout: %+v", state)
	}
	if n := fetches.Load(); n != 2 {
		t.Fatalf("the timeout path made %d fetches, want initial plus refresh", n)
	}

	clock.Advance(time.Minute)
	_, err := p.Parse(t.Context(), token)
	if !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
		t.Fatalf("a detached timeout beyond the stale window answered %v, want ErrKeySourceUnavailable", err)
	}
	if errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("a detached timeout became a credential refusal: %v", err)
	}
	if n := fetches.Load(); n != 3 {
		t.Fatalf("the expired stale window made %d fetches, want initial plus two refreshes", n)
	}
}

func TestTransportOutageHasTheSameStaleSemanticsAsHTTPFailure(t *testing.T) {
	providerDown := errors.New("network route unavailable")
	body, err := json.Marshal(map[string]any{"keys": []map[string]any{jwkOf("k1", &rsaKey.PublicKey)}})
	if err != nil {
		t.Fatal(err)
	}
	var fail atomic.Bool
	var fetches atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		fetches.Add(1)
		if fail.Load() {
			return nil, providerDown
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})}
	clock := newFakeClock()
	observed := make(chan authjwt.JWKSDegraded, 1)
	p := parser[MyClaims](t, authjwt.JWKS("https://issuer.example/jwks",
		authjwt.JWKSClient(client),
		authjwt.JWKSClock(clock.Now),
		authjwt.JWKSStaleAfter(time.Minute),
		authjwt.JWKSServeStaleFor(time.Minute, func(_ context.Context, state authjwt.JWKSDegraded) {
			observed <- state
		}),
	))
	token := signKid(t, "k1", rsaKey)
	if _, err := p.Parse(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	clock.Advance(time.Minute)
	if _, err := p.Parse(t.Context(), token); err != nil {
		t.Fatalf("a transport outage did not use explicit stale-on-error: %v", err)
	}
	if state := receiveDegraded(t, observed); !errors.Is(state.Cause, providerDown) {
		t.Fatalf("the transport cause was lost: %v", state.Cause)
	}
	if n := fetches.Load(); n != 2 {
		t.Fatalf("the transport path made %d fetches, want 2", n)
	}
}

func TestDegradedObserverCannotHoldOrReenterTheSingleflight(t *testing.T) {
	const freshFor = time.Minute
	clock := newFakeClock()
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	token := signKid(t, "k1", rsaKey)

	t.Run("blocking observer", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		completed := make(chan struct{}, 2)
		var (
			calls       atomic.Int64
			startedOnce sync.Once
			releaseOnce sync.Once
		)
		t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
		p := parser[MyClaims](t, authjwt.JWKS(set.serve(t),
			authjwt.JWKSClock(clock.Now),
			authjwt.JWKSStaleAfter(freshFor),
			authjwt.UnsafeJWKSNoMinRefresh(),
			authjwt.JWKSServeStaleFor(time.Minute, func(context.Context, authjwt.JWKSDegraded) {
				calls.Add(1)
				startedOnce.Do(func() { close(started) })
				// Deliberately violate the observer contract and ignore its
				// deadline. Even this extension code must not own a request or
				// start one goroutine per refresh.
				<-release
				completed <- struct{}{}
			}),
		))
		if _, err := p.Parse(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		set.fail(http.StatusServiceUnavailable)
		clock.Advance(freshFor)

		done := make(chan error, 1)
		go func() {
			_, err := p.Parse(context.Background(), token)
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("stale request failed while the observer blocked: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("a blocking observer held the completed singleflight")
		}
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("the observer was never started")
		}

		// Later refreshes neither join observer work nor start another blocked
		// observer. They only coalesce the newest pending descriptor.
		for range 3 {
			if _, err := p.Parse(t.Context(), token); err != nil {
				t.Fatalf("a blocked observer poisoned a later request: %v", err)
			}
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("%d observers blocked concurrently, want exactly one", got)
		}

		releaseOnce.Do(func() { close(release) })
		for range 2 {
			select {
			case <-completed:
			case <-time.After(time.Second):
				t.Fatal("the serial observer loop did not drain its coalesced notice")
			}
		}
		if got := calls.Load(); got != 2 {
			t.Fatalf("observer delivered %d states, want active plus one coalesced state", got)
		}
	})

	t.Run("reentrant observer", func(t *testing.T) {
		clock := newFakeClock()
		set := &keySet{}
		set.set(jwkOf("k1", &rsaKey.PublicKey))
		var p *authjwt.Parser[MyClaims]
		reentered := make(chan error, 1)
		p = parser[MyClaims](t, authjwt.JWKS(set.serve(t),
			authjwt.JWKSClock(clock.Now),
			authjwt.JWKSStaleAfter(freshFor),
			authjwt.JWKSServeStaleFor(time.Minute, func(ctx context.Context, _ authjwt.JWKSDegraded) {
				_, err := p.Parse(ctx, token)
				reentered <- err
			}),
		))
		if _, err := p.Parse(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		set.fail(http.StatusServiceUnavailable)
		clock.Advance(freshFor)
		if _, err := p.Parse(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-reentered:
			if err != nil {
				t.Fatalf("observer re-entry failed: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("observer deadlocked re-entering the completed flight")
		}
	})

	t.Run("panicking observer", func(t *testing.T) {
		clock := newFakeClock()
		set := &keySet{}
		set.set(jwkOf("k1", &rsaKey.PublicKey))
		p := parser[MyClaims](t, authjwt.JWKS(set.serve(t),
			authjwt.JWKSClock(clock.Now),
			authjwt.JWKSStaleAfter(freshFor),
			authjwt.JWKSServeStaleFor(time.Minute, func(context.Context, authjwt.JWKSDegraded) {
				panic("broken metrics sink")
			}),
		))
		if _, err := p.Parse(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		set.fail(http.StatusServiceUnavailable)
		clock.Advance(freshFor)
		if _, err := p.Parse(t.Context(), token); err != nil {
			t.Fatalf("an observer panic escaped the detached worker: %v", err)
		}
		if _, err := p.Parse(t.Context(), token); err != nil {
			t.Fatalf("an observer panic poisoned subsequent requests: %v", err)
		}
	})
}

func TestWarmRefusesAStaleCacheWhileParseMayUseItsExplicitWindow(t *testing.T) {
	clock := newFakeClock()
	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	observed := make(chan authjwt.JWKSDegraded, 1)
	p := parser[MyClaims](t, authjwt.JWKS(set.serve(t),
		authjwt.JWKSClock(clock.Now),
		authjwt.JWKSStaleAfter(time.Minute),
		authjwt.JWKSServeStaleFor(time.Minute, func(_ context.Context, state authjwt.JWKSDegraded) {
			observed <- state
		}),
	))
	if err := p.Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	set.fail(http.StatusServiceUnavailable)
	clock.Advance(time.Minute)
	if err := p.Warm(t.Context()); !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
		t.Fatalf("stale trust made readiness healthy during an outage: %v", err)
	}
	receiveDegraded(t, observed)
	if _, err := p.Parse(t.Context(), signKid(t, "k1", rsaKey)); err != nil {
		t.Fatalf("request traffic could not use its explicit stale window: %v", err)
	}
}

func TestJWKSMethodsAndOperationsBelongToEachKey(t *testing.T) {
	t.Run("RSA alg is exact", func(t *testing.T) {
		set := &keySet{}
		set.set(jwkOf("k1", &rsaKey.PublicKey))
		p := parser[MyClaims](t, authjwt.JWKS(set.serve(t)))
		if _, err := p.Parse(t.Context(), signKid(t, "k1", rsaKey)); err != nil {
			t.Fatalf("RS256 declared by the JWK did not verify: %v", err)
		}
		ps := jwt.NewWithClaims(jwt.SigningMethodPS256, claims())
		ps.Header["kid"] = "k1"
		forged, err := ps.SignedString(rsaKey)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Parse(t.Context(), forged); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("PS256 selected another method for an RS256 JWK: %v", err)
		}
		if n := set.fetches.Load(); n != 1 {
			t.Fatalf("an alg mismatch triggered %d provider fetches, want the warm fetch only", n)
		}
	})

	t.Run("a stale method mismatch refreshes provider policy", func(t *testing.T) {
		const freshFor = time.Minute
		clock := newFakeClock()
		set := &keySet{}
		set.set(jwkOf("k1", &rsaKey.PublicKey))
		p := parser[MyClaims](t, authjwt.JWKS(set.serve(t),
			authjwt.JWKSClock(clock.Now),
			authjwt.JWKSStaleAfter(freshFor),
		))
		oldToken := signKid(t, "k1", rsaKey)
		if _, err := p.Parse(t.Context(), oldToken); err != nil {
			t.Fatalf("warming the original RS256 policy: %v", err)
		}

		rotated := jwkOf("k1", &rsaKey.PublicKey)
		rotated["alg"] = "PS256"
		set.set(rotated)
		clock.Advance(freshFor)
		newToken := signRSAKid(t, jwt.SigningMethodPS256, "k1", rsaKey)
		if _, err := p.Parse(t.Context(), newToken); err != nil {
			t.Fatalf("a valid token could not refresh stale method policy for the same kid: %v", err)
		}
		if n := set.fetches.Load(); n != 2 {
			t.Fatalf("stale method policy made %d fetches, want the warm fetch plus one refresh", n)
		}

		if _, err := p.Parse(t.Context(), oldToken); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("the provider's fresh PS256 policy still accepted RS256: %v", err)
		}
		if n := set.fetches.Load(); n != 2 {
			t.Fatalf("a mismatch against fresh policy triggered %d fetches, want 2", n)
		}
	})

	t.Run("a stale method mismatch cannot hide a provider outage as 401", func(t *testing.T) {
		const freshFor = time.Minute
		clock := newFakeClock()
		set := &keySet{}
		set.set(jwkOf("k1", &rsaKey.PublicKey))
		p := parser[MyClaims](t, authjwt.JWKS(set.serve(t),
			authjwt.JWKSClock(clock.Now),
			authjwt.JWKSStaleAfter(freshFor),
		))
		if _, err := p.Parse(t.Context(), signKid(t, "k1", rsaKey)); err != nil {
			t.Fatalf("warming the original RS256 policy: %v", err)
		}

		set.fail(http.StatusServiceUnavailable)
		clock.Advance(freshFor)
		_, err := p.Parse(t.Context(), signRSAKid(t, jwt.SigningMethodPS256, "k1", rsaKey))
		if !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
			t.Fatalf("stale method metadata plus an outage answered %v, want ErrKeySourceUnavailable", err)
		}
		if errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("stale method metadata turned a provider outage into a credential refusal: %v", err)
		}
		if n := set.fetches.Load(); n != 2 {
			t.Fatalf("the outage path made %d fetches, want the warm fetch plus one refresh", n)
		}
	})

	t.Run("EC curve and alg are exact", func(t *testing.T) {
		private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		set := &keySet{}
		set.set(ecJWK("ec", "ES256", &private.PublicKey))
		p := parser[MyClaims](t, authjwt.JWKS(set.serve(t)))
		good := jwt.NewWithClaims(jwt.SigningMethodES256, claims())
		good.Header["kid"] = "ec"
		signed, err := good.SignedString(private)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Parse(t.Context(), signed); err != nil {
			t.Fatalf("ES256 declared by P-256 did not verify: %v", err)
		}
		crossMethod := signECDSAWithoutLibraryCurveCheck(t, jwt.SigningMethodES384, private)
		parsed, err := jwt.Parse(crossMethod, func(*jwt.Token) (any, error) { return &private.PublicKey, nil })
		if err != nil || !parsed.Valid {
			t.Fatalf("the cross-method fixture no longer reaches the dependency's verifier: %v", err)
		}
		if _, err := p.Parse(t.Context(), crossMethod); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("a P-256 JWK accepted a token-selected ES384 method: %v", err)
		}

		// RFC 7517 makes alg optional. For EC the curve itself determines the
		// exact JWT method, so actual omission is still unambiguous and remains
		// usable. An explicit empty or null value is tested below as a malformed
		// declaration, not reinterpreted as omission.
		withoutAlg := ecJWK("ec", "", &private.PublicKey)
		delete(withoutAlg, "alg")
		set = &keySet{}
		set.set(withoutAlg)
		p = parser[MyClaims](t, authjwt.JWKS(set.serve(t)))
		if _, err := p.Parse(t.Context(), signed); err != nil {
			t.Fatalf("P-256 without alg did not derive ES256: %v", err)
		}
		if _, err := p.Parse(t.Context(), crossMethod); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("P-256 without alg did not pin its curve-derived method: %v", err)
		}
	})

	t.Run("key_ops must be a real array allowing verify", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			ops  any
		}{
			{"explicit null", nil},
			{"empty array", []string{}},
			{"sign only", []string{"sign"}},
			{"null array member", []any{"verify", nil}},
			{"string instead of array", "verify"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				jwk := jwkOf("k1", &rsaKey.PublicKey)
				jwk["key_ops"] = tc.ops
				set := &keySet{}
				set.set(jwk)
				if err := parser[MyClaims](t, authjwt.JWKS(set.serve(t))).Warm(t.Context()); !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
					t.Fatalf("key_ops=%#v became verification trust: %v", tc.ops, err)
				}
			})
		}

		jwk := jwkOf("k1", &rsaKey.PublicKey)
		jwk["key_ops"] = []string{"verify"}
		set := &keySet{}
		set.set(jwk)
		if err := parser[MyClaims](t, authjwt.JWKS(set.serve(t))).Warm(t.Context()); err != nil {
			t.Fatalf("a verify key operation was refused: %v", err)
		}
	})

	edPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edWithoutAlg := edJWK("ed", "", edPublic)
	delete(edWithoutAlg, "alg")
	edSet := &keySet{}
	edSet.set(edWithoutAlg)
	if err := parser[MyClaims](t, authjwt.JWKS(edSet.serve(t))).Warm(t.Context()); err != nil {
		t.Fatalf("Ed25519 without alg did not derive EdDSA: %v", err)
	}
	for _, tc := range []struct {
		name string
		jwk  map[string]any
	}{
		{"RSA without alg", func() map[string]any { j := jwkOf("k1", &rsaKey.PublicKey); delete(j, "alg"); return j }()},
		{"RSA with empty alg", func() map[string]any { j := jwkOf("k1", &rsaKey.PublicKey); j["alg"] = ""; return j }()},
		{"RSA with null alg", func() map[string]any { j := jwkOf("k1", &rsaKey.PublicKey); j["alg"] = nil; return j }()},
		{"curve and alg disagree", func() map[string]any {
			private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			return ecJWK("ec", "ES384", &private.PublicKey)
		}()},
		{"EC with empty alg", func() map[string]any {
			private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			return ecJWK("ec", "", &private.PublicKey)
		}()},
		{"EC with null alg", func() map[string]any {
			private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			j := ecJWK("ec", "ES256", &private.PublicKey)
			j["alg"] = nil
			return j
		}()},
		{"Ed25519 and alg disagree", edJWK("ed", "ES256", edPublic)},
		{"Ed25519 with empty alg", edJWK("ed", "", edPublic)},
		{"Ed25519 with null alg", func() map[string]any { j := edJWK("ed", "EdDSA", edPublic); j["alg"] = nil; return j }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := &keySet{}
			set.set(tc.jwk)
			if err := parser[MyClaims](t, authjwt.JWKS(set.serve(t))).Warm(t.Context()); !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
				t.Fatalf("an ambiguous method declaration answered %v", err)
			}
		})
	}
}

func TestJWKSRejectsLowOrderEd25519AndMalformedTokenKids(t *testing.T) {
	identity := make(ed25519.PublicKey, ed25519.PublicKeySize)
	identity[0] = 1
	nonCanonical := append(ed25519.PublicKey(nil), identity...)
	nonCanonical[ed25519.PublicKeySize-1] = 0x80
	for _, tc := range []struct {
		name string
		key  ed25519.PublicKey
	}{
		{"low-order identity", identity},
		{"non-canonical point encoding", nonCanonical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := &keySet{}
			set.set(edJWK("ed", "EdDSA", tc.key))
			if err := parser[MyClaims](t, authjwt.JWKS(set.serve(t))).Warm(t.Context()); !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
				t.Fatalf("JWKS installed invalid Ed25519 trust material: %v", err)
			}
		})
	}

	set := &keySet{}
	set.set(jwkOf("k1", &rsaKey.PublicKey))
	p := parser[MyClaims](t, authjwt.JWKS(set.serve(t)))
	withoutKid := jwt.NewWithClaims(jwt.SigningMethodRS256, claims())
	signed, err := withoutKid.SignedString(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Parse(t.Context(), signed); err != nil {
		t.Fatalf("a genuinely absent kid did not select the sole key: %v", err)
	}
	for _, malformed := range []any{"", "   ", 7, []string{"k1"}} {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims())
		token.Header["kid"] = malformed
		signed, err := token.SignedString(rsaKey)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Parse(t.Context(), signed); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("present malformed kid %#v selected the sole key: %v", malformed, err)
		}
	}
}

func TestWarmFetchesAndValidatesJWKSBeforeTraffic(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		set := &keySet{}
		set.set(jwkOf("k1", &rsaKey.PublicKey))
		p := parser[MyClaims](t, authjwt.JWKS(set.serve(t)))
		if err := p.Warm(t.Context()); err != nil {
			t.Fatalf("warming a healthy key set: %v", err)
		}
		if _, err := p.Parse(t.Context(), signKid(t, "k1", rsaKey)); err != nil {
			t.Fatalf("the warmed parser refused the published key: %v", err)
		}
		if n := set.fetches.Load(); n != 1 {
			t.Fatalf("Warm followed by Parse made %d fetches, want 1", n)
		}
	})

	t.Run("invalid document", func(t *testing.T) {
		set := &keySet{}
		set.set(map[string]any{"kty": "oct", "kid": "shared", "use": "sig", "k": "AA"})
		p := parser[MyClaims](t, authjwt.JWKS(set.serve(t)))
		if err := p.Warm(t.Context()); !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
			t.Fatalf("an unusable readiness document answered %v", err)
		}
		if n := set.fetches.Load(); n != 1 {
			t.Fatalf("one readiness check made %d fetches", n)
		}
	})

	t.Run("static source", func(t *testing.T) {
		p := parser[MyClaims](t, authjwt.HMAC(secret))
		if err := p.Warm(t.Context()); err != nil {
			t.Fatalf("a prevalidated static source was not ready: %v", err)
		}
	})

	t.Run("caller cancellation", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		var fetches atomic.Int64
		var startedOnce sync.Once
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fetches.Add(1)
			startedOnce.Do(func() { close(started) })
			<-release
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{jwkOf("k1", &rsaKey.PublicKey)}})
		}))
		t.Cleanup(srv.Close)
		p := parser[MyClaims](t, authjwt.JWKS(srv.URL))

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- p.Warm(ctx) }()
		<-started
		cancel()
		select {
		case err := <-done:
			if err != context.Canceled {
				t.Fatalf("cancelled Warm answered %v, want context.Canceled", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Warm did not stop waiting on its caller's context")
		}

		close(release)
		if err := p.Warm(context.Background()); err != nil {
			t.Fatalf("caller cancellation stopped the shared readiness fetch: %v", err)
		}
		if n := fetches.Load(); n != 1 {
			t.Fatalf("two Warm callers made %d provider fetches, want one", n)
		}
	})
}

func TestJWKSUsesTheSameRSAStrengthRulesAsAStaticSource(t *testing.T) {
	weakModulus := new(big.Int).Lsh(big.NewInt(1), 1023)
	evenModulus := new(big.Int).Lsh(big.NewInt(1), 2047)
	oversizedModulus := new(big.Int).Lsh(big.NewInt(1), 16384)
	sharesExponent := new(big.Int).Mul(rsaKey.N, big.NewInt(65537))
	primeModulus := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 2203), big.NewInt(1))
	jwk := func(n *big.Int, exponent int64) map[string]any {
		return map[string]any{
			"kty": "RSA", "kid": "weak", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(n.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(exponent).Bytes()),
		}
	}
	for _, tc := range []struct {
		name string
		jwk  map[string]any
	}{
		{"1024-bit modulus", jwk(weakModulus, 65537)},
		{"even modulus", jwk(evenModulus, 65537)},
		{"oversized modulus", jwk(oversizedModulus, 65537)},
		{"modulus sharing exponent", jwk(sharesExponent, 65537)},
		{"prime modulus", jwk(primeModulus, 65537)},
		{"even exponent", jwk(rsaKey.N, 4)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := &keySet{}
			set.set(tc.jwk)
			p := parser[MyClaims](t, authjwt.JWKS(set.serve(t)))
			if err := p.Warm(t.Context()); !errors.Is(err, authjwt.ErrKeySourceUnavailable) {
				t.Fatalf("the weak remote trust declaration answered %v", err)
			}
			if n := set.fetches.Load(); n != 1 {
				t.Fatalf("validating one weak document made %d fetches", n)
			}
		})
	}
}
