package authjwt_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"math/big"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/frostgrove/vv/auth/authjwt"
)

func TestStaticAsymmetricKeysAreValidatedAtDeclaration(t *testing.T) {
	weakN := new(big.Int).Lsh(big.NewInt(1), 1023)
	evenN := new(big.Int).Lsh(big.NewInt(1), 2047)
	tooLargeN := new(big.Int).Lsh(big.NewInt(1), 16384)
	sharesExponent := new(big.Int).Mul(rsaKey.N, big.NewInt(65537))
	// 2^2203-1 is a known Mersenne prime. As an RSA modulus it makes phi(N)
	// public and lets anybody derive the private exponent.
	primeN := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 2203), big.NewInt(1))
	identity := make(ed25519.PublicKey, ed25519.PublicKeySize)
	identity[0] = 1
	nonCanonical := append(ed25519.PublicKey(nil), identity...)
	// Edwards decoders commonly accept the sign bit for x=0 even though its
	// canonical encoding is clear. This reaches the canonical round-trip check,
	// rather than merely being an arbitrary byte string that is not a point.
	nonCanonical[ed25519.PublicKeySize-1] = 0x80
	for _, tc := range []struct {
		name string
		make func()
	}{
		{"nil RSA key", func() { authjwt.RSA(nil) }},
		{"nil RSA modulus", func() { authjwt.RSA(&rsa.PublicKey{E: 65537}) }},
		{"weak RSA modulus", func() { authjwt.RSA(&rsa.PublicKey{N: weakN, E: 65537}) }},
		{"even RSA modulus", func() { authjwt.RSA(&rsa.PublicKey{N: evenN, E: 65537}) }},
		{"oversized RSA modulus", func() { authjwt.RSA(&rsa.PublicKey{N: tooLargeN, E: 65537}) }},
		{"RSA modulus sharing the exponent", func() { authjwt.RSA(&rsa.PublicKey{N: sharesExponent, E: 65537}) }},
		{"prime RSA modulus", func() { authjwt.RSA(&rsa.PublicKey{N: primeN, E: 65537}) }},
		{"even RSA exponent", func() { authjwt.RSA(&rsa.PublicKey{N: rsaKey.N, E: 4}) }},
		{"small RSA exponent", func() { authjwt.RSA(&rsa.PublicKey{N: rsaKey.N, E: 1}) }},
		{"nil ECDSA key", func() { authjwt.ECDSA(nil) }},
		{"nil ECDSA coordinate", func() {
			authjwt.ECDSA(&ecdsa.PublicKey{Curve: elliptic.P256(), X: nil, Y: big.NewInt(1)})
		}},
		{"unsupported ECDSA curve", func() {
			authjwt.ECDSA(&ecdsa.PublicKey{
				Curve: &elliptic.CurveParams{Name: "P-999", BitSize: 999},
				X:     big.NewInt(1),
				Y:     big.NewInt(1),
			})
		}},
		{"ECDSA point off curve", func() {
			authjwt.ECDSA(&ecdsa.PublicKey{Curve: elliptic.P256(), X: big.NewInt(1), Y: big.NewInt(1)})
		}},
		{"empty Ed25519 key", func() { authjwt.EdDSA(nil) }},
		{"short Ed25519 key", func() { authjwt.EdDSA(make(ed25519.PublicKey, ed25519.PublicKeySize-1)) }},
		{"long Ed25519 key", func() { authjwt.EdDSA(make(ed25519.PublicKey, ed25519.PublicKeySize+1)) }},
		{"low-order Ed25519 identity", func() { authjwt.EdDSA(identity) }},
		{"non-canonical Ed25519 point", func() { authjwt.EdDSA(nonCanonical) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("the invalid trust declaration was accepted")
				}
			}()
			tc.make()
		})
	}
}

func TestLowOrderEd25519TrustWouldAcceptAUniversalJWTForgery(t *testing.T) {
	identity := make(ed25519.PublicKey, ed25519.PublicKeySize)
	identity[0] = 1
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims())
	signingString, err := token.SigningString()
	if err != nil {
		t.Fatal(err)
	}
	// For A=identity, R=identity and S=0 satisfy [S]B=R+[k]A for every
	// signing string. This is a complete attacker-created JWT signature.
	signature := make([]byte, ed25519.SignatureSize)
	signature[0] = 1
	forged := signingString + "." + base64.RawURLEncoding.EncodeToString(signature)
	if !ed25519.Verify(identity, []byte(signingString), signature) {
		t.Fatal("the Go verifier no longer demonstrates the low-order-key forgery; revisit the declaration check")
	}

	// Custom is the deliberately caller-owned escape hatch and demonstrates the
	// impact. The safe constructor below must make this parser unreachable.
	unsafeSource := authjwt.Custom([]string{jwt.SigningMethodEdDSA.Alg()}, func(context.Context, *jwt.Token) (any, error) {
		return identity, nil
	})
	if _, err := parser[MyClaims](t, unsafeSource).Parse(t.Context(), forged); err != nil {
		t.Fatalf("the attack fixture was not a valid universal forgery: %v", err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("EdDSA accepted the low-order trust key used by the universal forgery")
		}
	}()
	authjwt.EdDSA(identity)
}

func TestStaticAsymmetricKeysAreSnapshottedAtDeclaration(t *testing.T) {
	t.Run("RSA", func(t *testing.T) {
		private := newRSAKey(t)
		source := authjwt.RSA(&private.PublicKey)
		token := sign(t, jwt.SigningMethodRS256, private, claims())

		private.PublicKey.N.SetInt64(3)
		private.PublicKey.E = 3
		if _, err := parser[MyClaims](t, source).Parse(t.Context(), token); err != nil {
			t.Fatalf("mutating the caller's RSA key changed the parser: %v", err)
		}
	})

	t.Run("ECDSA", func(t *testing.T) {
		private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		source := authjwt.ECDSA(&private.PublicKey)
		token := sign(t, jwt.SigningMethodES256, private, claims())

		private.PublicKey.X.SetInt64(1)
		private.PublicKey.Y.SetInt64(1)
		if _, err := parser[MyClaims](t, source).Parse(t.Context(), token); err != nil {
			t.Fatalf("mutating the caller's ECDSA key changed the parser: %v", err)
		}
	})

	t.Run("Ed25519", func(t *testing.T) {
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		source := authjwt.EdDSA(public)
		token := sign(t, jwt.SigningMethodEdDSA, private, claims())

		clear(public)
		if _, err := parser[MyClaims](t, source).Parse(t.Context(), token); err != nil {
			t.Fatalf("mutating the caller's Ed25519 key changed the parser: %v", err)
		}
	})
}

func TestECDSAPinsTheAlgorithmToTheDeclaredCurve(t *testing.T) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p := parser[MyClaims](t, authjwt.ECDSA(&private.PublicKey))
	if _, err := p.Parse(t.Context(), sign(t, jwt.SigningMethodES256, private, claims())); err != nil {
		t.Fatalf("P-256 did not verify ES256: %v", err)
	}
}
