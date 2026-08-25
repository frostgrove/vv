package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shardit-io/vv/auth"
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

	// The control. Without it the test above passes for a Require that refuses
	// unconditionally, which would fail every authenticated request too.
	t.Run("control: Require succeeds once a principal is there", func(t *testing.T) {
		ctx := auth.WithPrincipal(t.Context(), auth.Claims{Sub: "u-1"})
		if _, err := auth.Require(ctx); err != nil {
			t.Fatalf("Require refused an authenticated context: %v", err)
		}
	})
}

func TestANilPrincipalIsNotStored(t *testing.T) {
	ctx := auth.WithPrincipal(t.Context(), nil)
	if _, ok := auth.PrincipalFrom(ctx); ok {
		t.Fatal("a nil principal was stored, so PrincipalFrom answers (nil, true) and every caller dereferences nothing")
	}
}

func TestANilContextIsNotAPanic(t *testing.T) {
	//lint:ignore SA1012 the point of the test is the nil
	if _, ok := auth.PrincipalFrom(context.Context(nil)); ok {
		t.Fatal("a nil context reported a principal")
	}
}
