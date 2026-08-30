package auth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/frostgrove/vv/auth"
)

func TestAListAwareGuardRefusesEveryDuplicateCredential(t *testing.T) {
	for _, source := range []struct {
		name    string
		header  string
		options []auth.Option
	}{
		{name: "Authorization", header: auth.HeaderAuthorization},
		{name: "configured header", header: "X-Credential", options: []auth.Option{auth.Header("X-Credential")}},
		{
			name:   "custom lookup",
			header: "X-Api-Key",
			options: []auth.Option{auth.Lookup(func(get func(string) string) (auth.Credential, bool) {
				key := get("X-Api-Key")
				if key == "" {
					return auth.Credential{}, false
				}
				return auth.Credential{Scheme: "ApiKey", Token: key}, true
			})},
		},
	} {
		t.Run(source.name, func(t *testing.T) {
			for _, duplicate := range []struct {
				name   string
				values []string
			}{
				{name: "different values", values: []string{"Bearer first", "Bearer second"}},
				{name: "identical values", values: []string{"Bearer same", "Bearer same"}},
			} {
				t.Run(duplicate.name, func(t *testing.T) {
					calls := 0
					authenticator := auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
						calls++
						return auth.Claims{Sub: "must-not-run"}, nil
					})
					guard := auth.NewGuard(authenticator, source.options...)
					header := http.Header{source.header: append([]string(nil), duplicate.values...)}

					_, err := guard.AuthenticateValues(t.Context(), header.Values)
					if !errors.Is(err, auth.ErrCredentialCardinality) || !errors.Is(err, auth.ErrUnauthenticated) {
						t.Fatalf("duplicate credential answered %v, want a typed authentication refusal", err)
					}
					if calls != 0 {
						t.Fatalf("an ambiguous credential reached the authenticator %d times", calls)
					}
				})
			}
		})
	}
}

func TestAListAwareOptionalGuardDistinguishesAbsenceFromDuplicates(t *testing.T) {
	calls := 0
	authenticator := auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		calls++
		return auth.Claims{Sub: "must-not-run"}, nil
	})
	guard := auth.NewGuard(authenticator, auth.Optional())

	ctx, err := guard.AuthenticateValues(t.Context(), http.Header{}.Values)
	if err != nil {
		t.Fatalf("an optional guard refused an absent credential: %v", err)
	}
	if _, found := auth.PrincipalFrom(ctx); found {
		t.Fatal("an optional guard invented a principal for an absent credential")
	}

	header := http.Header{auth.HeaderAuthorization: []string{"Bearer same", "Bearer same"}}
	_, err = guard.AuthenticateValues(t.Context(), header.Values)
	if !errors.Is(err, auth.ErrCredentialCardinality) || !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("an optional guard treated duplicate credentials as absence: %v", err)
	}
	if calls != 0 {
		t.Fatalf("an ambiguous credential reached the authenticator %d times", calls)
	}
}
