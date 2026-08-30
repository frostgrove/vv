package authjwt

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"errors"
	"fmt"
	"math/big"

	"filippo.io/edwards25519"
	"github.com/golang-jwt/jwt/v5"
)

// A KeySource is a verification key together with the signing methods it can
// verify.
//
// The two travel together and cannot be separated, and that is the whole design
// of this type. A key alone leaves the token's own alg header to decide how it
// is used, which is the key-confusion forgery: hand an RS256 verifier a token
// signed HS256 and it verifies the signature using the RSA *public* key as an
// HMAC secret — so anybody who can read a public key, which is everybody, can
// mint tokens. Pinning the methods at the key is what makes that unreachable
// rather than merely discouraged.
//
// It is a struct rather than an interface for the same reason. An interface
// invites an implementation that answers a key and no methods, and there would
// then be a supported way to build the unsafe parser.
type KeySource struct {
	methods []string
	keyfunc Keyfunc
	warm    func(context.Context) error
}

// A Keyfunc answers the verification key for one token.
//
// It takes a context where golang-jwt's own jwt.Keyfunc does not, because a key
// source that fetches — [JWKS] — needs the request's deadline and cancellation.
// A source that holds its key in memory ignores it.
type Keyfunc func(ctx context.Context, t *jwt.Token) (any, error)

// HMAC verifies HS256 tokens with a shared secret of at least 32 bytes.
//
// The secret is the whole of the security here: anything that can verify can
// also sign. Prefer a public-key method for a token issued by something other
// than this process — with [RSA] or [ECDSA] a leaked verification key mints
// nothing.
//
// HMAC is the short, safe default. Use [HMAC384] or [HMAC512] when the issuer
// signs with another algorithm; one source never accepts a family selected by
// the token's alg header.
func HMAC(secret []byte) KeySource {
	return HMAC256(secret)
}

// HMAC256 verifies only HS256 tokens and requires at least 32 secret bytes.
func HMAC256(secret []byte) KeySource {
	return hmac(jwt.SigningMethodHS256.Alg(), 32, secret)
}

// HMAC384 verifies only HS384 tokens and requires at least 48 secret bytes.
func HMAC384(secret []byte) KeySource {
	return hmac(jwt.SigningMethodHS384.Alg(), 48, secret)
}

// HMAC512 verifies only HS512 tokens and requires at least 64 secret bytes.
func HMAC512(secret []byte) KeySource {
	return hmac(jwt.SigningMethodHS512.Alg(), 64, secret)
}

func hmac(method string, minimum int, secret []byte) KeySource {
	if len(secret) < minimum {
		panic(fmt.Sprintf("authjwt: %s needs a secret of at least %d bytes; got %d", method, minimum, len(secret)))
	}
	key := append([]byte(nil), secret...)
	return KeySource{
		methods: []string{method},
		keyfunc: func(context.Context, *jwt.Token) (any, error) { return key, nil },
	}
}

// RSA verifies tokens signed with an RSA private key, in both the PKCS#1 v1.5
// and the PSS families. It requires an odd composite modulus from 2048 through
// 16384 bits, coprime to a sane exponent, and snapshots the public key at
// declaration.
func RSA(pub *rsa.PublicKey) KeySource {
	key, err := rsaPublicKey(pub)
	if err != nil {
		panic(fmt.Sprintf("authjwt: RSA needs a valid public key: %v", err))
	}
	return KeySource{
		methods: []string{
			jwt.SigningMethodRS256.Alg(), jwt.SigningMethodRS384.Alg(), jwt.SigningMethodRS512.Alg(),
			jwt.SigningMethodPS256.Alg(), jwt.SigningMethodPS384.Alg(), jwt.SigningMethodPS512.Alg(),
		},
		keyfunc: func(context.Context, *jwt.Token) (any, error) { return key, nil },
	}
}

// ECDSA verifies the one JWT method matching a P-256, P-384 or P-521 key. It
// validates the point and snapshots its coordinates at declaration.
func ECDSA(pub *ecdsa.PublicKey) KeySource {
	key, method, err := ecdsaPublicKey(pub)
	if err != nil {
		panic(fmt.Sprintf("authjwt: ECDSA needs a valid public key: %v", err))
	}
	return KeySource{
		methods: []string{method},
		keyfunc: func(context.Context, *jwt.Token) (any, error) { return key, nil },
	}
}

// EdDSA verifies tokens signed with a canonical, non-low-order Ed25519 public
// point, snapshotted at declaration.
func EdDSA(pub ed25519.PublicKey) KeySource {
	key, err := ed25519PublicKey(pub)
	if err != nil {
		panic(fmt.Sprintf("authjwt: EdDSA needs a valid Ed25519 public key: %v", err))
	}
	return KeySource{
		methods: []string{jwt.SigningMethodEdDSA.Alg()},
		keyfunc: func(context.Context, *jwt.Token) (any, error) { return key, nil },
	}
}

// Custom is the escape hatch, for a key this package has no constructor for —
// several issuers, a key held in an HSM, a rotation scheme of your own.
//
// The methods are yours to get right, and they are the reason this is a
// separate constructor rather than an exported field: an empty list is an
// unpinned parser, and it should be visible in a diff that somebody wrote one.
// Never derive them from the token.
func Custom(methods []string, keyfunc Keyfunc) KeySource {
	return KeySource{methods: append([]string(nil), methods...), keyfunc: keyfunc}
}

// valid reports whether the source can be used at all.
func (this KeySource) valid() bool { return this.keyfunc != nil && len(this.methods) > 0 }

const (
	maxRSAExponent    = 1<<31 - 1
	maxRSAModulusBits = 16384
)

// rsaPublicKey validates the security properties needed by both a static RSA
// declaration and a key decoded from JWKS, then returns an immutable snapshot.
// Keeping this in one function prevents the two trust paths from drifting.
func rsaPublicKey(pub *rsa.PublicKey) (*rsa.PublicKey, error) {
	if pub == nil || pub.N == nil {
		return nil, errors.New("the key or its modulus is nil")
	}
	n := new(big.Int).Set(pub.N)
	if n.Sign() <= 0 || n.BitLen() < 2048 {
		return nil, fmt.Errorf("the RSA modulus needs at least 2048 bits; got %d", n.BitLen())
	}
	if n.BitLen() > maxRSAModulusBits {
		return nil, fmt.Errorf("the RSA modulus may not exceed %d bits; got %d", maxRSAModulusBits, n.BitLen())
	}
	if n.Bit(0) == 0 {
		return nil, errors.New("the RSA modulus must be odd")
	}
	if pub.E < 3 || pub.E > maxRSAExponent || pub.E%2 == 0 {
		return nil, errors.New("the RSA exponent must be odd and between 3 and 2147483647")
	}
	e := big.NewInt(int64(pub.E))
	if new(big.Int).GCD(nil, nil, n, e).Cmp(big.NewInt(1)) != 0 {
		return nil, errors.New("the RSA modulus and exponent must be coprime")
	}
	// An RSA modulus is a product of secret primes. A prime public modulus makes
	// phi(N) public as N-1, so anybody can derive the private exponent. The
	// Baillie-PSW test is deterministic work bounded by the 16384-bit cap above;
	// it is not used to prove arbitrary composites strong, only to reject this
	// immediately forgeable declaration.
	if n.ProbablyPrime(0) {
		return nil, errors.New("the RSA modulus must be composite")
	}
	return &rsa.PublicKey{N: n, E: pub.E}, nil
}

// ed25519PublicKey strictly decodes and snapshots public trust material.
// crypto/ed25519 deliberately accepts some non-canonical encodings and does
// not reject every low-order public point at construction. In particular the
// identity point admits the signature R=identity,S=0 for every message. A
// trust declaration has to reject that before request verification begins.
func ed25519PublicKey(pub ed25519.PublicKey) (ed25519.PublicKey, error) {
	if len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("the key needs exactly %d bytes; got %d", ed25519.PublicKeySize, len(pub))
	}
	key := append(ed25519.PublicKey(nil), pub...)
	point, err := new(edwards25519.Point).SetBytes(key)
	if err != nil {
		return nil, errors.New("the key is not an Edwards25519 point")
	}
	if !bytes.Equal(point.Bytes(), key) {
		return nil, errors.New("the key is not canonically encoded")
	}
	if new(edwards25519.Point).MultByCofactor(point).Equal(edwards25519.NewIdentityPoint()) == 1 {
		return nil, errors.New("the key is a low-order point")
	}
	return key, nil
}

// ecdsaPublicKey normalises the curve to one of the immutable standard curves,
// validates the point against that curve and snapshots both coordinates. A
// caller-owned custom curve therefore cannot mutate a parser after startup.
func ecdsaPublicKey(pub *ecdsa.PublicKey) (*ecdsa.PublicKey, string, error) {
	if pub == nil || pub.Curve == nil || pub.X == nil || pub.Y == nil {
		return nil, "", errors.New("the key, curve, or coordinates are nil")
	}
	params := pub.Curve.Params()
	if params == nil {
		return nil, "", errors.New("the curve has no parameters")
	}

	var (
		curve  elliptic.Curve
		method string
	)
	switch {
	case params.Name == elliptic.P256().Params().Name && params.BitSize == elliptic.P256().Params().BitSize:
		curve, method = elliptic.P256(), jwt.SigningMethodES256.Alg()
	case params.Name == elliptic.P384().Params().Name && params.BitSize == elliptic.P384().Params().BitSize:
		curve, method = elliptic.P384(), jwt.SigningMethodES384.Alg()
	case params.Name == elliptic.P521().Params().Name && params.BitSize == elliptic.P521().Params().BitSize:
		curve, method = elliptic.P521(), jwt.SigningMethodES512.Alg()
	default:
		return nil, "", fmt.Errorf("unsupported curve %q", params.Name)
	}
	if !curve.IsOnCurve(pub.X, pub.Y) {
		return nil, "", errors.New("the public point is not on the declared curve")
	}
	return &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).Set(pub.X),
		Y:     new(big.Int).Set(pub.Y),
	}, method, nil
}
