package auth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/frostgrove/vv/auth"
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

// An outage is an outage whichever order the authenticators are wired in.
//
// An authenticator distinguishes "this credential is wrong" from "I could not
// tell": apikey.Store has three results for exactly that, so a store outage
// renders as a 500 rather than a 401 ([[D-056]]). Chain returned the *last*
// error, so the distinction survived only when the failing authenticator
// happened to be wired last — Chain(keys, jwt) turned a database outage into
// "your key is invalid", which is wrong for the client and invisible to whoever
// watches the 5xx rate.
func TestAnOutageAnywhereInAChainBeatsARefusal(t *testing.T) {
	outage := errors.New("connection refused")
	broken := auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		return nil, outage
	})
	refuses := auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		return nil, auth.Unauthenticated("bad token")
	})

	for _, tc := range []struct {
		name  string
		chain auth.Authenticator
	}{
		{"the failing one first", auth.Chain(broken, refuses)},
		{"the failing one last", auth.Chain(refuses, broken)},
		{"and buried in the middle", auth.Chain(refuses, broken, refuses)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.chain.Authenticate(context.Background(), auth.Credential{Token: "t"})
			if !errors.Is(err, outage) {
				t.Fatalf("the outage was reported as %v — a client is told its credential is wrong and nothing alerts", err)
			}
			if errors.Is(err, auth.ErrUnauthenticated) {
				t.Fatal("the outage was classified as a refusal, so it renders as 401 rather than 500")
			}
		})
	}

	// The control. All of that would hold for a Chain that never answered
	// ErrUnauthenticated at all — so a chain where every authenticator genuinely
	// refuses must still refuse.
	_, err := auth.Chain(refuses, refuses).Authenticate(context.Background(), auth.Credential{Token: "t"})
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("a chain that only refused answered %v, want a refusal", err)
	}
}
