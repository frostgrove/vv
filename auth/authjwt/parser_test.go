package authjwt_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/authjwt"
)

// MyClaims is a consumer's own claims type: no embedded type of ours, none of
// golang-jwt's. That it parses at all is the point of the parser being generic.
type MyClaims struct {
	Subject string `json:"sub"`
	Tenant  int64  `json:"tenant"`
	Scope   string `json:"scope"`
}

func parser[C any](t *testing.T, k authjwt.KeySource, opts ...authjwt.Option) *authjwt.Parser[C] {
	t.Helper()
	return authjwt.New[C](k, append([]authjwt.Option{
		authjwt.Issuer(issuer), authjwt.Audience(audience),
	}, opts...)...)
}

// The control every refusal below depends on. Without it a parser that refused
// everything would pass the whole file.
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

// alg=none: the forgery where a token declares it needs no signature at all.
//
// Two things refuse it and both are worth having: golang-jwt will not verify a
// none-signed token unless the keyfunc hands back its UnsafeAllowNoneSignature
// sentinel, and the method pinning refuses it before a key is asked for at all.
// This test passes with either in place, which is what defence in depth means —
// TestOnlyTheMethodsTheKeyDeclaresAreAccepted is the one that isolates the
// pinning.
func TestAnUnsignedTokenIsRefused(t *testing.T) {
	tok, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims()).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("building the fixture: %v", err)
	}
	if _, err := parser[MyClaims](t, authjwt.HMAC(secret)).Parse(t.Context(), tok); err == nil {
		t.Fatal("a token signed with alg=none was accepted; anybody can mint one")
	}
}

// Key confusion: an RSA verifier handed a token signed HS256 with the RSA
// *public* key as the HMAC secret. The public key is public, so accepting this
// means anybody can mint tokens.
//
// golang-jwt's own key typing already refuses it — SigningMethodHMAC wants a
// []byte and gets an *rsa.PublicKey — and the method pinning refuses it one
// step earlier. The assertion here is the externally visible behaviour, which
// must hold whichever layer delivers it.
func TestAnRSAParserRefusesAnHMACTokenSignedWithItsPublicKey(t *testing.T) {
	// The attacker's secret is the PEM of the public key, exactly as it is
	// published — that is what makes this forgery free to mount.
	der, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		t.Fatalf("building the attack fixture: %v", err)
	}
	pub := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	forged := sign(t, jwt.SigningMethodHS256, pub, claims())

	if _, err := parser[MyClaims](t, authjwt.RSA(&rsaKey.PublicKey)).Parse(t.Context(), forged); err == nil {
		t.Fatal("an RSA parser verified an HS256 token using its own public key as the secret")
	}

	// The control: the same parser must still accept a genuine RS256 token.
	good := sign(t, jwt.SigningMethodRS256, rsaKey, claims())
	if _, err := parser[MyClaims](t, authjwt.RSA(&rsaKey.PublicKey)).Parse(t.Context(), good); err != nil {
		t.Fatalf("the RSA parser refused a genuine RS256 token: %v", err)
	}
}

// The mirror of the case above: an HMAC parser must not accept an RS256 token
// either. Pinning runs in both directions.
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
	// Fault.Error is classification only, so the diagnostic cannot reach a log
	// line that prints the error — and it must not reach a body either.
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

	// The control: waiving them deliberately is allowed, and that is the whole
	// difference between an omission and a decision.
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

// This is the test that isolates the method pinning, and it is the reason
// [authjwt.KeySource] carries methods at all.
//
// PS256 and RS256 both verify with an *rsa.PublicKey, so golang-jwt's key
// typing cannot tell them apart: a keyfunc handing back the key accepts either.
// Only the declared method list refuses the one the deployment did not choose.
// Remove jwt.WithValidMethods from the parser and this test is the one that
// fails.
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

// Naming two audiences means the token must carry both.
//
// This is the one place in the package where the underlying library's option
// does the opposite of what this one promises. golang-jwt's WithAudience
// *assigns* the expected set and means "any of", so calling it once per audience
// left only the last one expected — `Audience("a", "b")` accepted a token
// audienced to "b" alone and **rejected** one audienced to "a" alone, which is
// wrong in both directions at once.
//
// Every other test in this package passes exactly one audience, which is why
// three review passes did not see it. The three-arm table is the point: a
// single-audience test passes under either quantifier.
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

	// The control: the same parser with one audience still accepts a token
	// carrying it, so the table above is not passing because this parser refuses
	// everything.
	one := authjwt.New[MyClaims](authjwt.HMAC(secret),
		authjwt.Issuer(issuer), authjwt.Audience(audience))
	if _, err := one.Parse(t.Context(), withAud(audience)); err != nil {
		t.Fatalf("a single-audience parser refused its own audience: %v", err)
	}
}
