package auth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/shardit-io/vv/auth"
)

// headers turns a map into the getter a Guard takes, so a test does not need a
// transport to exercise one.
func headers(kv map[string]string) func(string) string {
	h := http.Header{}
	for k, v := range kv {
		h.Set(k, v)
	}
	return h.Get
}

func TestAGuardPutsTheAuthenticatedCallerIntoTheContext(t *testing.T) {
	g := auth.NewGuard(yes("u-1"))

	ctx, err := g.Authenticate(t.Context(), headers(map[string]string{"Authorization": "Bearer t"}))
	if err != nil {
		t.Fatalf("a valid credential was refused: %v", err)
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok || p.Subject() != "u-1" {
		t.Fatalf("the guard did not put the principal into the context: %v %v", p, ok)
	}
}

func TestAMissingCredentialIsA401UnlessTheGuardIsOptional(t *testing.T) {
	none := headers(nil)

	t.Run("required by default", func(t *testing.T) {
		_, err := auth.NewGuard(yes("u-1")).Authenticate(t.Context(), none)
		if !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("a request with no credential answered %v, want a 401", err)
		}
	})

	t.Run("Optional lets it through with no principal", func(t *testing.T) {
		ctx, err := auth.NewGuard(yes("u-1"), auth.Optional()).Authenticate(t.Context(), none)
		if err != nil {
			t.Fatalf("an optional guard refused a request with no credential: %v", err)
		}
		if _, ok := auth.PrincipalFrom(ctx); ok {
			t.Fatal("an optional guard invented a principal for a request that presented none")
		}
	})
}

// The arm that makes Optional safe. A token that does not verify must not
// downgrade to anonymous: a client with a stale session would then see the
// public view instead of a prompt to sign in again.
func TestAnOptionalGuardStillRefusesABadCredential(t *testing.T) {
	g := auth.NewGuard(no("signature does not verify"), auth.Optional())

	_, err := g.Authenticate(t.Context(), headers(map[string]string{"Authorization": "Bearer forged"}))
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("an optional guard accepted a credential that failed to verify: %v", err)
	}
}

func TestASecondGuardDoesNotAuthenticateAgain(t *testing.T) {
	calls := 0
	counting := auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		calls++
		return auth.Claims{Sub: "u-1"}, nil
	})
	g := auth.NewGuard(counting)
	get := headers(map[string]string{"Authorization": "Bearer t"})

	ctx, err := g.Authenticate(t.Context(), get)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Authenticate(ctx, get); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("the authenticator ran %d times for one principal; a double install pays twice", calls)
	}
}

func TestHeaderAndLookupReplaceWhereTheCredentialComesFrom(t *testing.T) {
	t.Run("Header moves it", func(t *testing.T) {
		g := auth.NewGuard(yes("u-1"), auth.Header("X-Auth"))
		if _, err := g.Authenticate(t.Context(), headers(map[string]string{"X-Auth": "Bearer t"})); err != nil {
			t.Fatalf("the credential was not read from the named header: %v", err)
		}
	})

	// The control. Without it the test above passes for a guard that ignores
	// the header entirely and authenticates everybody.
	t.Run("control: the default header is then not read", func(t *testing.T) {
		g := auth.NewGuard(yes("u-1"), auth.Header("X-Auth"))
		if _, err := g.Authenticate(t.Context(), headers(map[string]string{"Authorization": "Bearer t"})); err == nil {
			t.Fatal("Header did not move the lookup — Authorization was still accepted")
		}
	})

	t.Run("Lookup replaces the whole rule", func(t *testing.T) {
		g := auth.NewGuard(yes("u-1"), auth.Lookup(func(get func(string) string) (auth.Credential, bool) {
			if k := get("X-Api-Key"); k != "" {
				return auth.Credential{Scheme: "ApiKey", Token: k}, true
			}
			return auth.Credential{}, false
		}))
		if _, err := g.Authenticate(t.Context(), headers(map[string]string{"X-Api-Key": "k-1"})); err != nil {
			t.Fatalf("the custom lookup did not find the credential: %v", err)
		}
	})
}

func TestAnAuthenticatorThatAnswersNothingIsARefusal(t *testing.T) {
	empty := auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		return nil, nil
	})
	ctx, err := auth.NewGuard(empty).Authenticate(t.Context(), headers(map[string]string{"Authorization": "Bearer t"}))
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("an authenticator answering (nil, nil) was treated as a success: %v", err)
	}
	if _, ok := auth.PrincipalFrom(ctx); ok {
		t.Fatal("a nil principal reached the context")
	}
}

func TestANilAuthenticatorRefusesToStart(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewGuard accepted a nil authenticator, so the process starts and refuses every request at run time instead")
		}
	}()
	auth.NewGuard(nil)
}
