package authjwt

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"

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
}

// A Keyfunc answers the verification key for one token.
//
// It takes a context where golang-jwt's own jwt.Keyfunc does not, because a key
// source that fetches — [JWKS] — needs the request's deadline and cancellation.
// A source that holds its key in memory ignores it.
type Keyfunc func(ctx context.Context, t *jwt.Token) (any, error)

// HMAC verifies tokens signed with a shared secret.
//
// The secret is the whole of the security here: anything that can verify can
// also sign. Prefer a public-key method for a token issued by something other
// than this process — with [RSA] or [ECDSA] a leaked verification key mints
// nothing.
func HMAC(secret []byte) KeySource {
	key := append([]byte(nil), secret...)
	return KeySource{
		methods: []string{jwt.SigningMethodHS256.Alg(), jwt.SigningMethodHS384.Alg(), jwt.SigningMethodHS512.Alg()},
		keyfunc: func(context.Context, *jwt.Token) (any, error) { return key, nil },
	}
}

// RSA verifies tokens signed with an RSA private key, in both the PKCS#1 v1.5
// and the PSS families.
func RSA(pub *rsa.PublicKey) KeySource {
	return KeySource{
		methods: []string{
			jwt.SigningMethodRS256.Alg(), jwt.SigningMethodRS384.Alg(), jwt.SigningMethodRS512.Alg(),
			jwt.SigningMethodPS256.Alg(), jwt.SigningMethodPS384.Alg(), jwt.SigningMethodPS512.Alg(),
		},
		keyfunc: func(context.Context, *jwt.Token) (any, error) { return pub, nil },
	}
}

// ECDSA verifies tokens signed with an elliptic-curve key.
func ECDSA(pub *ecdsa.PublicKey) KeySource {
	return KeySource{
		methods: []string{jwt.SigningMethodES256.Alg(), jwt.SigningMethodES384.Alg(), jwt.SigningMethodES512.Alg()},
		keyfunc: func(context.Context, *jwt.Token) (any, error) { return pub, nil },
	}
}

// EdDSA verifies tokens signed with an Ed25519 key.
func EdDSA(pub ed25519.PublicKey) KeySource {
	return KeySource{
		methods: []string{jwt.SigningMethodEdDSA.Alg()},
		keyfunc: func(context.Context, *jwt.Token) (any, error) { return pub, nil },
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
func (k KeySource) valid() bool { return k.keyfunc != nil && len(k.methods) > 0 }
