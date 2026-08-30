package authjwt

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWKS verifies against a key set published by an identity provider, and is
// what an OIDC deployment wires instead of a key of its own.
//
//	authjwt.JWKS("https://id.example.com/.well-known/jwks.json")
//
// Only asymmetric methods are accepted. Every usable key owns one exact method:
// EC derives it from crv, Ed25519 derives EdDSA, and RSA must publish alg. A
// present alg must be a non-null, non-empty match, and a present key_ops must be
// a non-null string array containing verify. A key set is a public document, so
// an HMAC entry in one would be a shared secret published to the internet; the
// pinning [KeySource] describes is what makes that unreachable here.
//
// # Refetching
//
// A cached set is refetched after [JWKSFreshness], including on a hit, so a key
// the provider withdrew does not remain trusted for the life of the process. A
// token naming a kid the cached set does not have also triggers a refetch. Both
// paths are rate-limited by [JWKSMinRefresh] — otherwise one forged token per
// fetch is a denial-of-service against the provider.
func JWKS(rawURL string, options ...JWKSOption) KeySource {
	// An empty URL is the hardest misconfiguration in this package to diagnose
	// from outside: every request answers the same reasonless 401 a forged token
	// does ([[D-056]] keeps the reason inside the process), so nothing in the logs
	// or the response tells anyone the key set was never fetched. New already
	// panics on three misconfigurations for exactly this reason ([[D-021]]).
	if strings.TrimSpace(rawURL) == "" {
		panic("authjwt: JWKS needs the provider's key-set URL; with none, every token is refused for a reason nothing reports")
	}
	s := &jwks{
		url:        rawURL,
		client:     http.DefaultClient,
		minRefresh: JWKSMinRefresh,
		staleAfter: JWKSFreshness,
		now:        time.Now,
	}
	for _, o := range options {
		if o != nil {
			o(s)
		}
	}
	if s.staleAfter > 0 && s.minRefresh > 0 && s.staleAfter < s.minRefresh {
		panic("authjwt: JWKSStaleAfter must be at least JWKSMinRefreshEvery; freshness cannot promise a fetch the request bound forbids")
	}
	if s.staleAfter == 0 && s.maxStale > 0 {
		panic("authjwt: JWKSServeStaleFor cannot be combined with UnsafeJWKSNoFreshness")
	}
	return KeySource{
		methods: []string{
			jwt.SigningMethodRS256.Alg(), jwt.SigningMethodRS384.Alg(), jwt.SigningMethodRS512.Alg(),
			jwt.SigningMethodPS256.Alg(), jwt.SigningMethodPS384.Alg(), jwt.SigningMethodPS512.Alg(),
			jwt.SigningMethodES256.Alg(), jwt.SigningMethodES384.Alg(), jwt.SigningMethodES512.Alg(),
			jwt.SigningMethodEdDSA.Alg(),
		},
		keyfunc: s.key,
		warm:    s.warm,
	}
}

// JWKSFetchTimeout bounds one key-set fetch.
//
// It belongs to the fetch and not to whoever triggered it. The waiters parked
// behind a refresh share the leader's work, so a leader that could wait forever
// could park them forever — and the leader's own request context is the wrong
// bound in the other direction, because that one is cancelled when a single
// client disconnects. A deadline of its own is the only one that is nobody's.
//
// It is deliberately not configurable. A deployment that wants a different
// number wants a different HTTP client, and [JWKSClient] is where that goes.
const JWKSFetchTimeout = 10 * time.Second

// JWKSMinRefresh is how long a key set is trusted before an unknown kid may
// trigger another fetch.
const JWKSMinRefresh = time.Minute

// JWKSFreshness is the default maximum age of a successfully fetched key set.
// A hit after this age lazily refreshes the whole set before it is trusted.
const JWKSFreshness = 5 * time.Minute

// JWKSMaxBody is the largest key set that will be read. A provider's key set is
// a few kilobytes; without a limit, a compromised or misbehaving endpoint is an
// unbounded allocation on the request path.
const JWKSMaxBody = 1 << 20

// A JWKSOption configures [JWKS].
type JWKSOption func(*jwks)

// JWKSClient replaces the HTTP client — for a proxy, a timeout, or a test
// server. The default is http.DefaultClient, which has no timeout of its own,
// so a deployment that cares supplies one.
func JWKSClient(c *http.Client) JWKSOption {
	return func(s *jwks) {
		if c != nil {
			s.client = c
		}
	}
}

// JWKSMinRefreshEvery replaces the rate limit on refetching. The duration must
// be positive; use [UnsafeJWKSNoMinRefresh] when an unbounded provider request
// rate is a deliberate deployment decision.
func JWKSMinRefreshEvery(d time.Duration) JWKSOption {
	if d <= 0 {
		panic("authjwt: JWKSMinRefreshEvery needs a positive duration; use UnsafeJWKSNoMinRefresh to waive the outbound request bound")
	}
	return func(s *jwks) { s.minRefresh = d }
}

// UnsafeJWKSNoMinRefresh removes the outbound request-rate bound.
//
// An unknown kid is caller input, so this lets one forged token cause one HTTP
// request to the identity provider. It exists for controlled tests and unusual
// providers; the unsafe name is the compatibility boundary.
func UnsafeJWKSNoMinRefresh() JWKSOption {
	return func(s *jwks) { s.minRefresh = 0 }
}

// JWKSStaleAfter replaces the maximum age of a key set before the next token,
// including one naming a cached kid, must refresh it. The duration must be
// positive and no shorter than the configured minimum refresh interval.
func JWKSStaleAfter(d time.Duration) JWKSOption {
	if d <= 0 {
		panic("authjwt: JWKSStaleAfter needs a positive duration; use UnsafeJWKSNoFreshness to waive key withdrawal freshness")
	}
	return func(s *jwks) { s.staleAfter = d }
}

// UnsafeJWKSNoFreshness trusts a successfully fetched key set until a cache
// miss or process restart. A provider cannot withdraw a cached key from such a
// process, which is why the unsafe name is part of the call site.
func UnsafeJWKSNoFreshness() JWKSOption {
	return func(s *jwks) { s.staleAfter = 0 }
}

// JWKSDegraded describes one failed refresh for which cached keys remain
// eligible under an explicitly bounded stale-on-error policy.
//
// Cause is operational detail and must not be rendered to a caller. The
// descriptor deliberately carries no URL or kid.
type JWKSDegraded struct {
	Cause       error
	FetchedAt   time.Time
	Age         time.Duration
	FreshFor    time.Duration
	MaxStaleFor time.Duration
}

// JWKSDegradedObserver receives degraded-state descriptors after completed
// fetch work has released its waiters. Calls are serialised and coalesced to the
// latest descriptor if an earlier call is still running. The supplied context
// has a one-second deadline; request processing never waits for the observer.
type JWKSDegradedObserver func(context.Context, JWKSDegraded)

// JWKSServeStaleFor keeps a cached key eligible for at most d beyond its
// freshness deadline when a refresh fails. observe is mandatory: accepting a
// stale trust anchor without a typed operational signal is not a supported
// state. Delivery is queued after fetch waiters are released and never blocks
// request processing. At the boundary the parser returns
// [ErrKeySourceUnavailable].
func JWKSServeStaleFor(d time.Duration, observe JWKSDegradedObserver) JWKSOption {
	if d <= 0 {
		panic("authjwt: JWKSServeStaleFor needs a positive, bounded duration")
	}
	if observe == nil {
		panic("authjwt: JWKSServeStaleFor needs an observer for the degraded trust decision")
	}
	return func(s *jwks) {
		s.maxStale = d
		s.observeDegraded = observe
	}
}

// JWKSClock replaces the clock used for cache age and refresh-rate decisions.
// It does not replace HTTP timeouts. The option exists for deterministic tests
// and simulations; production code normally uses the default time.Now.
func JWKSClock(now func() time.Time) JWKSOption {
	if now == nil {
		panic("authjwt: JWKSClock needs a clock")
	}
	return func(s *jwks) { s.now = now }
}

type jwks struct {
	url             string
	client          *http.Client
	minRefresh      time.Duration
	staleAfter      time.Duration
	maxStale        time.Duration
	now             func() time.Time
	observeDegraded JWKSDegradedObserver

	mu   sync.Mutex
	keys map[string]verificationKey
	// fetched is the last *successful* fetch; attempted is the last fetch of any
	// kind. The rate limit reads attempted, and the two are separate because
	// only one of them is what a caller wants to know about the keys it holds.
	fetched      time.Time
	hasFetched   bool
	attempted    time.Time
	hasAttempted bool
	attemptErr   error
	// inflight is closed when the fetch currently running finishes, and nil when
	// none is. It is what makes a burst of concurrent misses one request.
	inflight *jwksFetch

	observeMu sync.Mutex
	observing bool
	pending   *degradedNotice
}

type jwksFetch struct {
	done chan struct{}
	err  error
}

// verificationKey keeps one immutable key beside the one method the provider
// declared for it. The parser-level method list rejects non-asymmetric
// families; this per-key check is what prevents a token from selecting another
// method that happens to consume the same Go key type.
type verificationKey struct {
	key    any
	method string
}

type degradedNotice struct {
	ctx   context.Context
	state JWKSDegraded
}

// key answers the verification key for one token.
func (this *jwks) key(ctx context.Context, t *jwt.Token) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var kid string
	if rawKid, present := t.Header["kid"]; present {
		var ok bool
		kid, ok = rawKid.(string)
		if !ok || strings.TrimSpace(kid) == "" {
			return nil, errNoKeyForToken
		}
	}
	method := t.Method.Alg()

	if k, found, allowed, fresh, _ := this.cached(kid, method); found && fresh {
		if !allowed {
			return nil, errNoKeyForToken
		}
		return k, nil
	}
	if err := this.refresh(ctx); err != nil {
		// Cancellation belongs to this caller and is never converted into either
		// stale acceptance or provider unavailability. A deadline on the detached
		// provider fetch is different: while this caller remains live it is an
		// outage, and an explicitly bounded stale policy may cover it.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if k, found, allowed, _, staleAllowed := this.cached(kid, method); found && allowed && staleAllowed {
			return k, nil
		}
		if errors.Is(err, errNoKeyForToken) {
			return nil, err
		}
		return nil, keySourceUnavailable(err)
	}
	if k, found, allowed, _, _ := this.cached(kid, method); found && allowed {
		return k, nil
	}
	return nil, errNoKeyForToken
}

// cached answers a key already held, whether its exact method matches, and the
// two cache-policy states its caller needs. A token with no kid matches only
// when the set holds exactly one key — anything else would be this package
// choosing which key to trust on the caller's behalf.
func (this *jwks) cached(kid, method string) (key any, found, allowed, fresh, staleAllowed bool) {
	now := this.now()
	this.mu.Lock()
	defer this.mu.Unlock()
	if !this.hasFetched {
		return nil, false, false, false, false
	}
	var selected verificationKey
	if kid != "" {
		selected, found = this.keys[kid]
	} else {
		if len(this.keys) != 1 {
			return nil, false, false, false, false
		}
		for _, selected = range this.keys {
			found = true
		}
	}
	if !found {
		return nil, false, false, false, false
	}
	key = selected.key
	allowed = selected.method == method
	age := elapsed(now, this.fetched)
	fresh = this.staleAfter == 0 || age < this.staleAfter
	staleAllowed = !fresh && this.maxStale > 0 && age-this.staleAfter < this.maxStale
	return key, true, allowed, fresh, staleAllowed
}

func elapsed(now, then time.Time) time.Duration {
	if now.Before(then) {
		return 0
	}
	return now.Sub(then)
}

// warm fetches and validates the remote trust set without requiring a token.
// A stale-on-error policy is deliberately not a readiness policy: if the set
// is stale and its provider cannot refresh it, Warm reports the outage.
func (this *jwks) warm(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	now := this.now()
	this.mu.Lock()
	fresh := this.hasFetched && (this.staleAfter == 0 || elapsed(now, this.fetched) < this.staleAfter)
	this.mu.Unlock()
	if fresh {
		return nil
	}
	if err := this.refresh(ctx); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return keySourceUnavailable(err)
	}
	return nil
}

// refresh refetches the set, at most once per minRefresh and never twice at once.
//
// A kid is the caller's own input, and any unknown one reaches here — so without
// both of those bounds, a token naming a kid nobody has is an outbound request
// this process makes because somebody asked it to.
//
// **The limit reads the last attempt, not the last success.** Arming it only on
// success is what it used to do, and it meant the limit did nothing in exactly
// the case it exists for: while the provider is down, every fetch fails, nothing
// is ever recorded, and each token costs a request to a service that is already
// failing. The stated purpose — "a burst of tokens naming unknown kids costs one
// request rather than one each" — was only true when the provider was healthy,
// which is when it does not matter.
//
// **And one fetch at a time.** The lock has to be dropped across the HTTP call,
// so a burst arriving together all passed the check before any of them recorded
// an attempt. Waiters share the in-flight fetch rather than being refused,
// because after a key rotation they are asking for a kid that really is about to
// exist, and refusing them would turn one rotation into a wave of 401s.
func (this *jwks) refresh(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	this.mu.Lock()
	if flight := this.inflight; flight != nil {
		this.mu.Unlock()
		return waitForJWKSFetch(ctx, flight)
	}
	now := this.now()
	if this.minRefresh > 0 && this.hasAttempted && elapsed(now, this.attempted) < this.minRefresh {
		err := this.attemptErr
		this.mu.Unlock()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return err
		}
		return errNoKeyForToken
	}
	flight := &jwksFetch{done: make(chan struct{})}
	this.inflight = flight
	this.attempted = now
	this.hasAttempted = true
	this.mu.Unlock()

	// The fetch runs in its own goroutine and on a context of its own, not the
	// initiator's. The initiator is a waiter just like every request behind it:
	// each may leave promptly while the bounded shared work continues for the
	// callers that still need it.
	go this.runFetch(context.WithoutCancel(ctx), flight)
	return waitForJWKSFetch(ctx, flight)
}

func waitForJWKSFetch(ctx context.Context, flight *jwksFetch) error {
	select {
	case <-flight.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		return flight.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (this *jwks) runFetch(seed context.Context, flight *jwksFetch) {
	// The fetch runs on a context of its own, not the initiating request's.
	//
	// The waiters share the work, and under net/http a request context is
	// cancelled the moment *that one client* disconnects — so one
	// abandoned request failed every request parked behind it and, because the
	// attempt is recorded either way, suppressed the refetch for the whole
	// minRefresh window. A shared piece of work must not belong to whoever
	// happened to ask for it first.
	//
	// seed has already kept values and dropped request cancellation. The deadline
	// is the fetch's own, so shared work cannot park all waiters indefinitely.
	fctx, cancel := context.WithTimeout(seed, JWKSFetchTimeout)
	keys, err := this.fetch(fctx)
	cancel()

	finishedAt := this.now()
	this.mu.Lock()
	if err == nil {
		this.keys = keys
		this.fetched = finishedAt
		this.hasFetched = true
	}
	this.attemptErr = err
	flight.err = err
	var degraded *JWKSDegraded
	if err != nil && this.observeDegraded != nil && len(this.keys) > 0 {
		age := elapsed(finishedAt, this.fetched)
		if age >= this.staleAfter && age-this.staleAfter < this.maxStale {
			degraded = &JWKSDegraded{
				Cause:       err,
				FetchedAt:   this.fetched,
				Age:         age,
				FreshFor:    this.staleAfter,
				MaxStaleFor: this.maxStale,
			}
		}
	}
	if this.inflight == flight {
		this.inflight = nil
	}
	close(flight.done)
	this.mu.Unlock()

	// Publish only after the singleflight is released. The observer is extension
	// code: it may re-enter Parse/Warm, panic, or fail to honour its context. None
	// of those behaviours may keep requests parked behind a completed provider
	// call. Delivery is serialised and coalesced, so a callback that ignores its
	// deadline can consume at most this one detached goroutine rather than one
	// additional goroutine per refresh.
	if degraded != nil && this.queueDegraded(seed, *degraded) {
		this.drainDegraded()
	}
}

// queueDegraded keeps only the newest pending state. It answers whether this
// caller owns the one observer-delivery loop.
func (this *jwks) queueDegraded(ctx context.Context, state JWKSDegraded) bool {
	this.observeMu.Lock()
	defer this.observeMu.Unlock()
	this.pending = &degradedNotice{ctx: ctx, state: state}
	if this.observing {
		return false
	}
	this.observing = true
	return true
}

func (this *jwks) drainDegraded() {
	for {
		this.observeMu.Lock()
		notice := this.pending
		this.pending = nil
		if notice == nil {
			this.observing = false
			this.observeMu.Unlock()
			return
		}
		this.observeMu.Unlock()

		func() {
			defer func() { _ = recover() }()
			observerCtx, stop := context.WithTimeout(notice.ctx, time.Second)
			defer stop()
			this.observeDegraded(observerCtx, notice.state)
		}()
	}
}

// errNoKeyForToken is deliberately not "no key with kid abc123": the kid is the
// caller's own input echoed back, and a message naming it turns the endpoint
// into a reflector ([[D-056]]).
var errNoKeyForToken = errors.New("authjwt: the key set has no key for this token")

// ErrKeySourceUnavailable marks a key-provider failure. It is not an
// authentication refusal: a valid token may have been presented and the
// verifier could not obtain the trust material needed to decide.
var ErrKeySourceUnavailable = errors.New("authjwt: verification key source unavailable")

func keySourceUnavailable(err error) error {
	if errors.Is(err, ErrKeySourceUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrKeySourceUnavailable, err)
}

func (this *jwks) fetch(ctx context.Context) (map[string]verificationKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, this.url, nil)
	if err != nil {
		return nil, err
	}
	response, err := this.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("authjwt: the key set answered %s", response.Status)
	}

	var set struct {
		Keys []jsonWebKey `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, JWKSMaxBody)).Decode(&set); err != nil {
		return nil, fmt.Errorf("authjwt: reading the key set: %w", err)
	}

	seen := make(map[string]struct{}, len(set.Keys))
	for _, k := range set.Keys {
		if strings.TrimSpace(k.Kid) == "" {
			return nil, errors.New("authjwt: every key-set entry needs a non-empty key identifier")
		}
		if _, duplicate := seen[k.Kid]; duplicate {
			return nil, errors.New("authjwt: the key set carries duplicate key identifiers")
		}
		seen[k.Kid] = struct{}{}
	}

	out := make(map[string]verificationKey, len(set.Keys))
	for _, k := range set.Keys {
		// A key published for encryption is not a key to verify signatures
		// with, and a set that says so must be believed.
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if !k.allowsVerify() {
			continue
		}
		pub, err := k.public()
		if err != nil {
			// One unusable entry — an unsupported curve, a future key type —
			// must not cost the whole set.
			continue
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return nil, errors.New("authjwt: the key set carries no usable verification key")
	}
	return out, nil
}

// jsonWebKey is the subset of RFC 7517 this package reads.
type jsonWebKey struct {
	Kty    string                                 `json:"kty"`
	Kid    string                                 `json:"kid"`
	Use    string                                 `json:"use"`
	Alg    jsonWebKeyMember[string]               `json:"alg"`
	KeyOps jsonWebKeyMember[jsonWebKeyOperations] `json:"key_ops"`
	Crv    string                                 `json:"crv"`
	N      string                                 `json:"n"`
	E      string                                 `json:"e"`
	X      string                                 `json:"x"`
	Y      string                                 `json:"y"`
}

// jsonWebKeyMember preserves the distinction encoding/json otherwise erases
// for scalar and slice fields: a member which is absent, explicitly null, or
// carries a value. JWK policy treats only actual absence as unspecified.
// Explicit null is still a declaration and therefore cannot waive alg or
// key_ops validation.
type jsonWebKeyMember[T any] struct {
	present bool
	null    bool
	value   T
}

func (this *jsonWebKeyMember[T]) UnmarshalJSON(data []byte) error {
	this.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		this.null = true
		var zero T
		this.value = zero
		return nil
	}
	this.null = false
	return json.Unmarshal(data, &this.value)
}

type jsonWebKeyOperations []string

func (this *jsonWebKeyOperations) UnmarshalJSON(data []byte) error {
	var members []json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	ops := make(jsonWebKeyOperations, len(members))
	for i, member := range members {
		if bytes.Equal(bytes.TrimSpace(member), []byte("null")) {
			return errors.New("authjwt: a key operation must be a string")
		}
		if err := json.Unmarshal(member, &ops[i]); err != nil {
			return err
		}
	}
	*this = ops
	return nil
}

func (this jsonWebKey) allowsVerify() bool {
	if !this.KeyOps.present {
		return true
	}
	if this.KeyOps.null {
		return false
	}
	for _, op := range this.KeyOps.value {
		if op == "verify" {
			return true
		}
	}
	return false
}

func (this jsonWebKey) public() (verificationKey, error) {
	switch this.Kty {
	case "RSA":
		if !this.Alg.present || this.Alg.null || !rsaJWTMethod(this.Alg.value) {
			return verificationKey{}, errors.New("authjwt: an RSA key needs one supported alg")
		}
		n, err := b64uint(this.N)
		if err != nil {
			return verificationKey{}, err
		}
		e, err := b64uint(this.E)
		if err != nil {
			return verificationKey{}, err
		}
		if !e.IsInt64() || e.Int64() > int64(maxRSAExponent) {
			return verificationKey{}, errors.New("authjwt: implausible RSA exponent")
		}
		pub, err := rsaPublicKey(&rsa.PublicKey{N: n, E: int(e.Int64())})
		return verificationKey{key: pub, method: this.Alg.value}, err

	case "EC":
		var curve elliptic.Curve
		var method string
		switch this.Crv {
		case "P-256":
			curve, method = elliptic.P256(), jwt.SigningMethodES256.Alg()
		case "P-384":
			curve, method = elliptic.P384(), jwt.SigningMethodES384.Alg()
		case "P-521":
			curve, method = elliptic.P521(), jwt.SigningMethodES512.Alg()
		default:
			return verificationKey{}, fmt.Errorf("authjwt: unsupported curve %q", this.Crv)
		}
		if this.Alg.present && (this.Alg.null || this.Alg.value != method) {
			return verificationKey{}, fmt.Errorf("authjwt: the declared alg does not match curve %s", this.Crv)
		}
		x, err := b64uint(this.X)
		if err != nil {
			return verificationKey{}, err
		}
		y, err := b64uint(this.Y)
		if err != nil {
			return verificationKey{}, err
		}
		pub := &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
		if !curve.IsOnCurve(x, y) {
			return verificationKey{}, errors.New("authjwt: the EC key is not a point on its curve")
		}
		return verificationKey{key: pub, method: method}, nil

	case "OKP":
		if this.Crv != "Ed25519" {
			return verificationKey{}, fmt.Errorf("authjwt: unsupported curve %q", this.Crv)
		}
		method := jwt.SigningMethodEdDSA.Alg()
		if this.Alg.present && (this.Alg.null || this.Alg.value != method) {
			return verificationKey{}, fmt.Errorf("authjwt: the declared alg does not match curve %s", this.Crv)
		}
		x, err := base64.RawURLEncoding.DecodeString(this.X)
		if err != nil {
			return verificationKey{}, err
		}
		pub, err := ed25519PublicKey(ed25519.PublicKey(x))
		if err != nil {
			return verificationKey{}, err
		}
		return verificationKey{key: pub, method: method}, nil

	default:
		return verificationKey{}, fmt.Errorf("authjwt: unsupported key type %q", this.Kty)
	}
}

func rsaJWTMethod(method string) bool {
	switch method {
	case jwt.SigningMethodRS256.Alg(), jwt.SigningMethodRS384.Alg(), jwt.SigningMethodRS512.Alg(),
		jwt.SigningMethodPS256.Alg(), jwt.SigningMethodPS384.Alg(), jwt.SigningMethodPS512.Alg():
		return true
	default:
		return false
	}
}

func b64uint(s string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, errors.New("authjwt: empty key parameter")
	}
	return new(big.Int).SetBytes(b), nil
}
