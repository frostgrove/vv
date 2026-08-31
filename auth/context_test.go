package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/auth"
)

func TestAPrincipalSurvivesTheContext(t *testing.T) {
	want := auth.Claims{Sub: "u-1", Roles: []auth.Role{"editor"}}
	ctx := auth.WithPrincipal(t.Context(), want)

	got, ok := auth.PrincipalFrom(ctx)
	if !ok {
		t.Fatal("the principal did not come back out, so no policy would ever see one")
	}
	if got.Subject() != "u-1" {
		t.Fatalf("the context answered subject %q, want u-1", got.Subject())
	}
}

func TestAnAbsentPrincipalFailsClosed(t *testing.T) {
	t.Run("PrincipalFrom reports absence", func(t *testing.T) {
		if _, ok := auth.PrincipalFrom(t.Context()); ok {
			t.Fatal("a context nobody authenticated reported a principal")
		}
	})

	t.Run("Require answers the 401 sentinel", func(t *testing.T) {
		_, err := auth.Require(t.Context())
		if !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("Require answered %v, want something matching auth.ErrUnauthenticated", err)
		}
	})

	t.Run("control: Require succeeds once a principal is there", func(t *testing.T) {
		ctx := auth.WithPrincipal(t.Context(), auth.Claims{Sub: "u-1"})
		if _, err := auth.Require(ctx); err != nil {
			t.Fatalf("Require refused an authenticated context: %v", err)
		}
	})
}

func TestANilPrincipalIsNotStored(t *testing.T) {
	var pointer *typedNilPrincipal
	for _, principal := range []auth.Principal{nil, pointer} {
		ctx := auth.WithPrincipal(t.Context(), principal)
		if _, ok := auth.PrincipalFrom(ctx); ok {
			t.Fatal("a nil-like principal was stored, so PrincipalFrom reports an identity every caller can panic on")
		}
	}
}

func TestANilContextIsNotAPanic(t *testing.T) {
	if _, ok := auth.PrincipalFrom(context.Context(nil)); ok {
		t.Fatal("a nil context reported a principal")
	}
}

type typedNilPrincipal struct{}

func (*typedNilPrincipal) Subject() string          { panic("typed-nil principal was called") }
func (*typedNilPrincipal) In(auth.Role) bool        { panic("typed-nil principal was called") }
func (*typedNilPrincipal) Has(auth.Permission) bool { panic("typed-nil principal was called") }
func (*typedNilPrincipal) Attr(string) (any, bool)  { panic("typed-nil principal was called") }
