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

type KeySource struct {
	methods []string
	keyfunc Keyfunc
	warm    func(context.Context) error
}

type Keyfunc func(ctx context.Context, t *jwt.Token) (any, error)

func HMAC(secret []byte) KeySource {
	return HMAC256(secret)
}

func HMAC256(secret []byte) KeySource {
	return hmac(jwt.SigningMethodHS256.Alg(), 32, secret)
}

func HMAC384(secret []byte) KeySource {
	return hmac(jwt.SigningMethodHS384.Alg(), 48, secret)
}

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

func Custom(methods []string, keyfunc Keyfunc) KeySource {
	return KeySource{methods: append([]string(nil), methods...), keyfunc: keyfunc}
}

func (this KeySource) valid() bool { return this.keyfunc != nil && len(this.methods) > 0 }

const (
	maxRSAExponent    = 1<<31 - 1
	maxRSAModulusBits = 16384
)

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

	if n.ProbablyPrime(0) {
		return nil, errors.New("the RSA modulus must be composite")
	}
	return &rsa.PublicKey{N: n, E: pub.E}, nil
}

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
