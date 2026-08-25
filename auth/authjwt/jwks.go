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
func JWKS(url string, opts ...JWKSOption) KeySource {
	s := &jwks{
		url:        url,
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

	mu      sync.Mutex
	keys    map[string]any
	fetched time.Time
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
	// Deliberately not "no key with kid abc123": the kid is the caller's own
	// input echoed back, and a message naming it turns the endpoint into a
	// reflector.
	return nil, errors.New("authjwt: the key set has no key for this token")
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

// refresh refetches the set, at most once per minRefresh.
//
// The rate limit is inside the lock and checked against the last *successful*
// fetch, so a burst of tokens naming unknown kids costs one request rather than
// one each.
func (s *jwks) refresh(ctx context.Context) error {
	s.mu.Lock()
	if !s.fetched.IsZero() && time.Since(s.fetched) < s.minRefresh {
		s.mu.Unlock()
		return errors.New("authjwt: the key set has no key for this token")
	}
	s.mu.Unlock()

	keys, err := s.fetch(ctx)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.keys = keys
	s.fetched = time.Now()
	s.mu.Unlock()
	return nil
}

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
