package authjwt

import (
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
// Only asymmetric methods are accepted. A key set is a public document, so an
// HMAC entry in one would be a shared secret published to the internet; the
// pinning [KeySource] describes is what makes that unreachable here.
//
// # Refetching
//
// The set is fetched once and kept. A token naming a kid the cached set does
// not have triggers exactly one refetch, rate-limited by [JWKSMinRefresh] — so
// a rotation is picked up within that window without a stream of tokens naming
// nonexistent kids turning into a stream of requests to the provider. That is
// the shape of the denial-of-service a naive implementation hands an attacker:
// one forged token per fetch.
func JWKS(rawURL string, opts ...JWKSOption) KeySource {
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
	}
	for _, o := range opts {
		if o != nil {
			o(s)
		}
	}
	return KeySource{
		methods: []string{
			jwt.SigningMethodRS256.Alg(), jwt.SigningMethodRS384.Alg(), jwt.SigningMethodRS512.Alg(),
			jwt.SigningMethodPS256.Alg(), jwt.SigningMethodPS384.Alg(), jwt.SigningMethodPS512.Alg(),
			jwt.SigningMethodES256.Alg(), jwt.SigningMethodES384.Alg(), jwt.SigningMethodES512.Alg(),
			jwt.SigningMethodEdDSA.Alg(),
		},
		keyfunc: s.key,
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

// JWKSMinRefreshEvery replaces the rate limit on refetching.
func JWKSMinRefreshEvery(d time.Duration) JWKSOption {
	return func(s *jwks) { s.minRefresh = d }
}

type jwks struct {
	url        string
	client     *http.Client
	minRefresh time.Duration

	mu   sync.Mutex
	keys map[string]any
	// fetched is the last *successful* fetch; attempted is the last fetch of any
	// kind. The rate limit reads attempted, and the two are separate because
	// only one of them is what a caller wants to know about the keys it holds.
	fetched   time.Time
	attempted time.Time
	// inflight is closed when the fetch currently running finishes, and nil when
	// none is. It is what makes a burst of concurrent misses one request.
	inflight chan struct{}
}

// key answers the verification key for one token.
func (s *jwks) key(ctx context.Context, t *jwt.Token) (any, error) {
	kid, _ := t.Header["kid"].(string)

	if k, ok := s.cached(kid); ok {
		return k, nil
	}
	if err := s.refresh(ctx); err != nil {
		return nil, err
	}
	if k, ok := s.cached(kid); ok {
		return k, nil
	}
	return nil, errNoKeyForToken
}

// cached answers a key already held. A token with no kid matches only when the
// set holds exactly one key — anything else would be this package choosing
// which key to trust on the caller's behalf.
func (s *jwks) cached(kid string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kid != "" {
		k, ok := s.keys[kid]
		return k, ok
	}
	if len(s.keys) != 1 {
		return nil, false
	}
	for _, k := range s.keys {
		return k, true
	}
	return nil, false
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
func (s *jwks) refresh(ctx context.Context) error {
	s.mu.Lock()
	if wait := s.inflight; wait != nil {
		s.mu.Unlock()
		select {
		case <-wait:
			// The fetch that was already running has finished. Whether it found
			// this kid is the caller's next question, not ours.
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if !s.attempted.IsZero() && time.Since(s.attempted) < s.minRefresh {
		s.mu.Unlock()
		return errNoKeyForToken
	}
	done := make(chan struct{})
	s.inflight = done
	s.attempted = time.Now()
	s.mu.Unlock()

	// The fetch runs on a context of its own, not the leader's.
	//
	// The waiters share whatever the leader does, and under net/http the leader's
	// context is cancelled the moment *that one client* disconnects — so one
	// abandoned request failed every request parked behind it and, because the
	// attempt is recorded either way, suppressed the refetch for the whole
	// minRefresh window. A shared piece of work must not belong to whoever
	// happened to ask for it first.
	//
	// WithoutCancel keeps the values the caller put on the context — a trace id,
	// a logger — and drops only the cancellation. The deadline is the fetch's
	// own, so the leader cannot park the waiters indefinitely either.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), JWKSFetchTimeout)
	defer cancel()
	keys, err := s.fetch(fctx)

	s.mu.Lock()
	if err == nil {
		s.keys = keys
		s.fetched = time.Now()
	}
	s.inflight = nil
	s.mu.Unlock()
	close(done)

	return err
}

// errNoKeyForToken is deliberately not "no key with kid abc123": the kid is the
// caller's own input echoed back, and a message naming it turns the endpoint
// into a reflector ([[D-056]]).
var errNoKeyForToken = errors.New("authjwt: the key set has no key for this token")

func (s *jwks) fetch(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("authjwt: the key set answered %s", resp.Status)
	}

	var set struct {
		Keys []jsonWebKey `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, JWKSMaxBody)).Decode(&set); err != nil {
		return nil, fmt.Errorf("authjwt: reading the key set: %w", err)
	}

	out := make(map[string]any, len(set.Keys))
	for _, k := range set.Keys {
		// A key published for encryption is not a key to verify signatures
		// with, and a set that says so must be believed.
		if k.Use != "" && k.Use != "sig" {
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
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (k jsonWebKey) public() (any, error) {
	switch k.Kty {
	case "RSA":
		n, err := b64uint(k.N)
		if err != nil {
			return nil, err
		}
		e, err := b64uint(k.E)
		if err != nil {
			return nil, err
		}
		if !e.IsInt64() || e.Int64() <= 0 || e.Int64() > 1<<31 {
			return nil, errors.New("authjwt: implausible RSA exponent")
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil

	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("authjwt: unsupported curve %q", k.Crv)
		}
		x, err := b64uint(k.X)
		if err != nil {
			return nil, err
		}
		y, err := b64uint(k.Y)
		if err != nil {
			return nil, err
		}
		pub := &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
		if !curve.IsOnCurve(x, y) {
			return nil, errors.New("authjwt: the EC key is not a point on its curve")
		}
		return pub, nil

	case "OKP":
		if k.Crv != "Ed25519" {
			return nil, fmt.Errorf("authjwt: unsupported curve %q", k.Crv)
		}
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		if len(x) != ed25519.PublicKeySize {
			return nil, errors.New("authjwt: the Ed25519 key is the wrong length")
		}
		return ed25519.PublicKey(x), nil

	default:
		return nil, fmt.Errorf("authjwt: unsupported key type %q", k.Kty)
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
