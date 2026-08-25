package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shardit-io/vv/auth"
)

func TestParseAuthorizationSplitsSchemeFromToken(t *testing.T) {
	cases := []struct {
		name, header, scheme, token string
		ok                          bool
	}{
		{"a bearer token", "Bearer abc.def.ghi", "Bearer", "abc.def.ghi", true},
		{"another scheme is still parsed", "ApiKey k-1", "ApiKey", "k-1", true},
		{"extra whitespace is trimmed", "  Bearer   abc  ", "Bearer", "abc", true},
		{"a bare token is not a credential", "abc.def.ghi", "", "", false},
		{"a scheme with nothing under it", "Bearer ", "", "", false},
		{"an empty header", "", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := auth.ParseAuthorization(c.header)
			if ok != c.ok {
				t.Fatalf("%q parsed=%v, want %v", c.header, ok, c.ok)
			}
			if ok && (got.Scheme != c.scheme || got.Token != c.token) {
				t.Fatalf("%q gave scheme %q token %q, want %q and %q", c.header, got.Scheme, got.Token, c.scheme, c.token)
			}
		})
	}
}

func TestBearerAcceptsOnlyItsOwnSchemeAndIsCaseInsensitive(t *testing.T) {
	t.Run("the scheme is compared case-insensitively", func(t *testing.T) {
		if _, ok := auth.Bearer("bearer abc"); !ok {
			t.Fatal("a lowercase scheme was refused, though RFC 7235 says it is case-insensitive")
		}
	})

	t.Run("another scheme is refused", func(t *testing.T) {
		if _, ok := auth.Bearer("ApiKey k-1"); ok {
			t.Fatal("Bearer accepted an ApiKey credential, so the scheme check does nothing")
		}
	})
}

// yes and no are the two authenticators every Chain case is built from.
func yes(sub string) auth.Authenticator {
	return auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		return auth.Claims{Sub: sub}, nil
	})
}

func no(reason string) auth.Authenticator {
	return auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		return nil, auth.Unauthenticated(reason)
	})
}

func TestChainAnswersTheFirstAuthenticatorThatSucceeds(t *testing.T) {
	t.Run("a later one still gets its turn", func(t *testing.T) {
		p, err := auth.Chain(no("not a jwt"), yes("u-2")).Authenticate(t.Context(), auth.Credential{})
		if err != nil {
			t.Fatalf("the chain refused though the second authenticator accepted: %v", err)
		}
		if p.Subject() != "u-2" {
			t.Fatalf("the chain answered subject %q, want u-2", p.Subject())
		}
	})

	t.Run("the first one wins", func(t *testing.T) {
		p, err := auth.Chain(yes("u-1"), yes("u-2")).Authenticate(t.Context(), auth.Credential{})
		if err != nil || p.Subject() != "u-1" {
			t.Fatalf("the chain did not stop at the first success: %v %v", p, err)
		}
	})

	t.Run("all refusing is one refusal", func(t *testing.T) {
		_, err := auth.Chain(no("a"), no("b")).Authenticate(t.Context(), auth.Credential{})
		if !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("the chain answered %v, want something matching auth.ErrUnauthenticated", err)
		}
	})

	t.Run("an empty chain refuses rather than admitting everyone", func(t *testing.T) {
		_, err := auth.Chain().Authenticate(t.Context(), auth.Credential{})
		if !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("a chain with no authenticators answered %v, want a refusal", err)
		}
	})

	t.Run("a nil member is skipped rather than crashing", func(t *testing.T) {
		if _, err := auth.Chain(nil, yes("u-1")).Authenticate(t.Context(), auth.Credential{}); err != nil {
			t.Fatalf("a nil member broke the chain: %v", err)
		}
	})
}
