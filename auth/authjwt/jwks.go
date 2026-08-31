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

func JWKS(rawURL string, options ...JWKSOption) KeySource {
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

const JWKSFetchTimeout = 10 * time.Second

const JWKSMinRefresh = time.Minute

const JWKSFreshness = 5 * time.Minute

const JWKSMaxBody = 1 << 20

type JWKSOption func(*jwks)

func JWKSClient(c *http.Client) JWKSOption {
	return func(s *jwks) {
		if c != nil {
			s.client = c
		}
	}
}

func JWKSMinRefreshEvery(d time.Duration) JWKSOption {
	if d <= 0 {
		panic("authjwt: JWKSMinRefreshEvery needs a positive duration; use UnsafeJWKSNoMinRefresh to waive the outbound request bound")
	}
	return func(s *jwks) { s.minRefresh = d }
}

func UnsafeJWKSNoMinRefresh() JWKSOption {
	return func(s *jwks) { s.minRefresh = 0 }
}

func JWKSStaleAfter(d time.Duration) JWKSOption {
	if d <= 0 {
		panic("authjwt: JWKSStaleAfter needs a positive duration; use UnsafeJWKSNoFreshness to waive key withdrawal freshness")
	}
	return func(s *jwks) { s.staleAfter = d }
}

func UnsafeJWKSNoFreshness() JWKSOption {
	return func(s *jwks) { s.staleAfter = 0 }
}

type JWKSDegraded struct {
	Cause       error
	FetchedAt   time.Time
	Age         time.Duration
	FreshFor    time.Duration
	MaxStaleFor time.Duration
}

type JWKSDegradedObserver func(context.Context, JWKSDegraded)

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

	fetched      time.Time
	hasFetched   bool
	attempted    time.Time
	hasAttempted bool
	attemptErr   error

	inflight *jwksFetch

	observeMu sync.Mutex
	observing bool
	pending   *degradedNotice
}

type jwksFetch struct {
	done chan struct{}
	err  error
}

type verificationKey struct {
	key    any
	method string
}

type degradedNotice struct {
	ctx   context.Context
	state JWKSDegraded
}

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

	if degraded != nil && this.queueDegraded(seed, *degraded) {
		this.drainDegraded()
	}
}

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

var errNoKeyForToken = errors.New("authjwt: the key set has no key for this token")

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
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if !k.allowsVerify() {
			continue
		}
		pub, err := k.public()
		if err != nil {
			continue
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return nil, errors.New("authjwt: the key set carries no usable verification key")
	}
	return out, nil
}

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
