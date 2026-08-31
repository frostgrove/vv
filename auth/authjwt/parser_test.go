package authjwt_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/authjwt"
)

type MyClaims struct {
	Subject string `json:"sub"`
	Tenant  int64  `json:"tenant"`
	Scope   string `json:"scope"`
}

func parser[C any](t *testing.T, k authjwt.KeySource, options ...authjwt.Option) *authjwt.Parser[C] {
	t.Helper()
	return authjwt.New[C](k, append([]authjwt.Option{
		authjwt.Issuer(issuer), authjwt.Audience(audience),
	}, options...)...)
}

func TestAValidTokenParsesIntoTheCallersOwnType(t *testing.T) {
	c := claims()
	c["tenant"] = 7
	c["scope"] = "article:read article:write"

	got, err := parser[MyClaims](t, authjwt.HMAC(secret)).Parse(t.Context(), signHS(t, c))
	if err != nil {
		t.Fatalf("a well-formed token was refused: %v", err)
	}
	if got.Subject != "u-1" {
		t.Fatalf("sub decoded as %q, want u-1", got.Subject)
	}
	if got.Tenant != 7 {
		t.Fatalf("tenant decoded as %d, want 7", got.Tenant)
	}
	if got.Scope != "article:read article:write" {
		t.Fatalf("scope decoded as %q", got.Scope)
	}
}

func TestHMACRefusesShortSecretsAtDeclaration(t *testing.T) {
	for _, size := range []int{0, 1, 16, 31} {
		t.Run(fmt.Sprintf("HS256 with %d bytes", size), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("HMAC accepted a %d-byte secret; HS256 needs at least 32", size)
				}
			}()
			authjwt.HMAC(bytes.Repeat([]byte{'k'}, size))
		})
	}

	for _, tc := range []struct {
		name string
		make func([]byte) authjwt.KeySource
		min  int
	}{
		{"HS256", authjwt.HMAC256, 32},
		{"HS384", authjwt.HMAC384, 48},
		{"HS512", authjwt.HMAC512, 64},
	} {
		t.Run(tc.name+" exact boundary", func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("the minimum %s secret was refused: %v", tc.name, recovered)
				}
			}()
			tc.make(bytes.Repeat([]byte{'k'}, tc.min))
		})
		t.Run(tc.name+" one byte short", func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s accepted a %d-byte secret", tc.name, tc.min-1)
				}
			}()
			tc.make(bytes.Repeat([]byte{'k'}, tc.min-1))
		})
	}
}

func TestEachHMACConstructorPinsOneAlgorithm(t *testing.T) {
	key := bytes.Repeat([]byte{'k'}, 64)
	hs384 := sign(t, jwt.SigningMethodHS384, key, claims())
	hs512 := sign(t, jwt.SigningMethodHS512, key, claims())

	if _, err := parser[MyClaims](t, authjwt.HMAC(key)).Parse(t.Context(), hs384); err == nil {
		t.Fatal("the default HS256 source accepted an HS384 token chosen by its alg header")
	}
	if _, err := parser[MyClaims](t, authjwt.HMAC384(key)).Parse(t.Context(), hs384); err != nil {
		t.Fatalf("the explicit HS384 source refused an HS384 token: %v", err)
	}
	if _, err := parser[MyClaims](t, authjwt.HMAC384(key)).Parse(t.Context(), hs512); err == nil {
		t.Fatal("an HS384 source accepted an HS512 token chosen by its alg header")
	}
	if _, err := parser[MyClaims](t, authjwt.HMAC512(key)).Parse(t.Context(), hs512); err != nil {
		t.Fatalf("the explicit HS512 source refused an HS512 token: %v", err)
	}
}

func TestAnUnsignedTokenIsRefused(t *testing.T) {
	tok, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims()).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("building the fixture: %v", err)
	}
	if _, err := parser[MyClaims](t, authjwt.HMAC(secret)).Parse(t.Context(), tok); err == nil {
		t.Fatal("a token signed with alg=none was accepted; anybody can mint one")
	}
}

func TestAnRSAParserRefusesAnHMACTokenSignedWithItsPublicKey(t *testing.T) {
	der, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		t.Fatalf("building the attack fixture: %v", err)
	}
	pub := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	forged := sign(t, jwt.SigningMethodHS256, pub, claims())

	if _, err := parser[MyClaims](t, authjwt.RSA(&rsaKey.PublicKey)).Parse(t.Context(), forged); err == nil {
		t.Fatal("an RSA parser verified an HS256 token using its own public key as the secret")
	}

	good := sign(t, jwt.SigningMethodRS256, rsaKey, claims())
	if _, err := parser[MyClaims](t, authjwt.RSA(&rsaKey.PublicKey)).Parse(t.Context(), good); err != nil {
		t.Fatalf("the RSA parser refused a genuine RS256 token: %v", err)
	}
}

func TestAnHMACParserRefusesAnRSAToken(t *testing.T) {
	tok := sign(t, jwt.SigningMethodRS256, rsaKey, claims())
	if _, err := parser[MyClaims](t, authjwt.HMAC(secret)).Parse(t.Context(), tok); err == nil {
		t.Fatal("an HMAC parser accepted a token signed with a method it does not verify")
	}
}

func TestTheRegisteredClaimsAreChecked(t *testing.T) {
	for _, tc := range []struct {
		name string
		with func(jwt.MapClaims)
	}{
		{"a wrong signature", nil},
		{"an expired token", func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() }},
		{"a token not yet valid", func(c jwt.MapClaims) { c["nbf"] = time.Now().Add(time.Hour).Unix() }},
		{"another issuer", func(c jwt.MapClaims) { c["iss"] = "https://evil.example.com" }},
		{"another audience", func(c jwt.MapClaims) { c["aud"] = "some-other-api" }},
		{"no expiry at all", func(c jwt.MapClaims) { delete(c, "exp") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := claims()
			tok := signHS(t, c)
			if tc.with != nil {
				tc.with(c)
				tok = signHS(t, c)
			} else {
				tok = sign(t, jwt.SigningMethodHS256, []byte("the wrong secret entirely"), c)
			}
			if _, err := parser[MyClaims](t, authjwt.HMAC(secret)).Parse(t.Context(), tok); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
		})
	}
}

func TestLeewayWidensTheWindowAndOnlyByWhatItSays(t *testing.T) {
	c := claims()
	c["exp"] = time.Now().Add(-10 * time.Second).Unix()
	tok := signHS(t, c)

	if _, err := parser[MyClaims](t, authjwt.HMAC(secret)).Parse(t.Context(), tok); err == nil {
		t.Fatal("a token that expired ten seconds ago was accepted with no leeway")
	}
	if _, err := parser[MyClaims](t, authjwt.HMAC(secret), authjwt.Leeway(time.Minute)).Parse(t.Context(), tok); err != nil {
		t.Fatalf("a minute of leeway did not cover ten seconds of skew: %v", err)
	}
}

func TestEveryRefusalIsTheSameAnswerToAClient(t *testing.T) {
	c := claims()
	c["exp"] = time.Now().Add(-time.Hour).Unix()
	_, err := parser[MyClaims](t, authjwt.HMAC(secret)).Parse(t.Context(), signHS(t, c))

	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("a rejected token answered %v, want auth.ErrUnauthenticated", err)
	}

	if strings.Contains(err.Error(), "expired") {
		t.Fatalf("the refusal names which check failed: %s", err.Error())
	}
}

func TestAParserThatWouldOverTrustRefusesToStart(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func()
	}{
		{"no issuer and no waiver", func() {
			authjwt.New[MyClaims](authjwt.HMAC(secret), authjwt.Audience(audience))
		}},
		{"no audience and no waiver", func() {
			authjwt.New[MyClaims](authjwt.HMAC(secret), authjwt.Issuer(issuer))
		}},
		{"a key source that verifies nothing", func() {
			authjwt.New[MyClaims](authjwt.Custom(nil, nil), authjwt.Issuer(issuer), authjwt.Audience(audience))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("the parser was built anyway, so the process starts and over-trusts at run time instead")
				}
			}()
			tc.make()
		})
	}

	t.Run("control: the waivers build a parser", func(t *testing.T) {
		p := authjwt.New[MyClaims](authjwt.HMAC(secret), authjwt.AllowAnyIssuer(), authjwt.AllowAnyAudience())
		c := claims()
		delete(c, "iss")
		delete(c, "aud")
		if _, err := p.Parse(t.Context(), signHS(t, c)); err != nil {
			t.Fatalf("a deliberately unchecked parser refused a token: %v", err)
		}
	})
}

func TestAnEmptyTokenIsRefusedWithoutParsing(t *testing.T) {
	if _, err := parser[MyClaims](t, authjwt.HMAC(secret)).Parse(t.Context(), ""); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("an empty token answered %v", err)
	}
}

func TestOnlyTheMethodsTheKeyDeclaresAreAccepted(t *testing.T) {
	rs256Only := authjwt.Custom([]string{"RS256"}, func(context.Context, *jwt.Token) (any, error) {
		return &rsaKey.PublicKey, nil
	})

	t.Run("the declared method is accepted", func(t *testing.T) {
		tok := sign(t, jwt.SigningMethodRS256, rsaKey, claims())
		if _, err := parser[MyClaims](t, rs256Only).Parse(t.Context(), tok); err != nil {
			t.Fatalf("the declared method was refused: %v", err)
		}
	})

	t.Run("another method on the same key type is refused", func(t *testing.T) {
		tok := sign(t, jwt.SigningMethodPS256, rsaKey, claims())
		if _, err := parser[MyClaims](t, rs256Only).Parse(t.Context(), tok); err == nil {
			t.Fatal("a PS256 token verified against a key that declared only RS256; the method list does nothing")
		}
	})
}

func TestNamingTwoAudiencesRequiresBothOfThem(t *testing.T) {
	const other = "reports-api"
	p := authjwt.New[MyClaims](authjwt.HMAC(secret),
		authjwt.Issuer(issuer), authjwt.Audience(audience, other))

	withAud := func(aud any) string {
		c := claims()
		c["aud"] = aud
		return sign(t, jwt.SigningMethodHS256, secret, c)
	}

	for _, tc := range []struct {
		name string
		aud  any
		ok   bool
	}{
		{"both", []string{audience, other}, true},
		{"both, in the other order", []string{other, audience}, true},
		{"both and a third", []string{other, "extra", audience}, true},
		{"only the first", audience, false},
		{"only the second", other, false},
		{"neither", "somewhere-else", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Parse(t.Context(), withAud(tc.aud))
			switch {
			case tc.ok && err != nil:
				t.Fatalf("a token carrying %v was refused: %v", tc.aud, err)
			case !tc.ok && err == nil:
				t.Fatalf("a token carrying only %v was accepted by a parser that requires both", tc.aud)
			}
		})
	}

	one := authjwt.New[MyClaims](authjwt.HMAC(secret),
		authjwt.Issuer(issuer), authjwt.Audience(audience))
	if _, err := one.Parse(t.Context(), withAud(audience)); err != nil {
		t.Fatalf("a single-audience parser refused its own audience: %v", err)
	}
}
