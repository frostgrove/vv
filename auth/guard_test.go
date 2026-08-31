package auth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/frostgrove/vv/auth"
)

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

func TestADifferentGuardAuthenticatesAgain(t *testing.T) {
	firstCalls, secondCalls := 0, 0
	first := auth.NewGuard(auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		firstCalls++
		return auth.Claims{Sub: "ordinary"}, nil
	}))
	second := auth.NewGuard(auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		secondCalls++
		return auth.Claims{Sub: "step-up"}, nil
	}))
	get := headers(map[string]string{"Authorization": "Bearer t"})

	ctx, err := first.Authenticate(t.Context(), get)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = second.Authenticate(ctx, get)
	if err != nil {
		t.Fatalf("a different, stricter guard was bypassed: %v", err)
	}

	if firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("guards authenticated %d and %d times, want once per instance", firstCalls, secondCalls)
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok || p.Subject() != "step-up" {
		t.Fatalf("the stricter guard's principal was replaced after its check: %v %v", p, ok)
	}
}

func TestAReenteredGuardAfterAnotherIdentityBoundaryFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name         string
		firstSubject string
		lastSubject  string
	}{
		{"ordinary -> step-up -> ordinary", "ordinary", "step-up"},
		{"strict -> weak -> strict", "strict", "weak"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			firstCalls, middleCalls := 0, 0
			first := auth.NewGuard(auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
				firstCalls++
				return auth.Claims{Sub: tc.firstSubject}, nil
			}))
			middle := auth.NewGuard(auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
				middleCalls++
				return auth.Claims{Sub: tc.lastSubject}, nil
			}))
			get := headers(map[string]string{"Authorization": "Bearer t"})

			ctx, err := first.Authenticate(t.Context(), get)
			if err != nil {
				t.Fatal(err)
			}
			ctx, err = middle.Authenticate(ctx, get)
			if err != nil {
				t.Fatal(err)
			}
			failedCtx, err := first.Authenticate(ctx, get)
			if !errors.Is(err, auth.ErrAmbiguousGuardOrder) {
				t.Fatalf("an assurance-ambiguous re-entry answered %v", err)
			}
			if errors.Is(err, auth.ErrUnauthenticated) {
				t.Fatal("a deployment-order ambiguity was blamed on the caller as a 401")
			}
			if firstCalls != 1 || middleCalls != 1 {
				t.Fatalf("ambiguous re-entry called authenticators %d and %d times", firstCalls, middleCalls)
			}
			principal, ok := auth.PrincipalFrom(failedCtx)
			if !ok || principal.Subject() != tc.lastSubject {
				t.Fatalf("the refusing path rewrote the last verified principal: %v %v", principal, ok)
			}
		})
	}
}

func TestTheSameGuardDoesNotTrustAPrincipalReplacedAfterItsMark(t *testing.T) {
	guard := auth.NewGuard(yes("verified"))
	get := headers(map[string]string{"Authorization": "Bearer t"})
	ctx, err := guard.Authenticate(t.Context(), get)
	if err != nil {
		t.Fatal(err)
	}
	ctx = auth.WithPrincipal(ctx, auth.Claims{Sub: "replacement"})

	if _, err = guard.Authenticate(ctx, get); !errors.Is(err, auth.ErrAmbiguousGuardOrder) {
		t.Fatalf("the guard trusted a principal written after its own marker: %v", err)
	}
}

func TestGuardValidateRejectsNilAndZeroValuesBeforeARequest(t *testing.T) {
	var nilGuard *auth.Guard
	for _, tc := range []struct {
		name  string
		guard *auth.Guard
	}{
		{"nil", nilGuard},
		{"zero", new(auth.Guard)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.guard.Validate(); !errors.Is(err, auth.ErrGuardNotReady) {
				t.Fatalf("Validate answered %v, want ErrGuardNotReady", err)
			}
			if _, err := tc.guard.Authenticate(t.Context(), headers(nil)); !errors.Is(err, auth.ErrGuardNotReady) {
				t.Fatalf("direct Authenticate answered %v instead of failing without a request panic", err)
			}
		})
	}
	if err := auth.NewGuard(yes("ready")).Validate(); err != nil {
		t.Fatalf("NewGuard built an invalid Guard: %v", err)
	}
}

func TestHeaderAndLookupReplaceWhereTheCredentialComesFrom(t *testing.T) {
	t.Run("Header moves it", func(t *testing.T) {
		g := auth.NewGuard(yes("u-1"), auth.Header("X-Auth"))
		if _, err := g.Authenticate(t.Context(), headers(map[string]string{"X-Auth": "Bearer t"})); err != nil {
			t.Fatalf("the credential was not read from the named header: %v", err)
		}
	})

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
	var pointer *typedNilPrincipal
	for _, principal := range []auth.Principal{nil, pointer} {
		empty := auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
			return principal, nil
		})
		ctx, err := auth.NewGuard(empty).Authenticate(t.Context(), headers(map[string]string{"Authorization": "Bearer t"}))
		if !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("an authenticator answering a nil-like principal was treated as a success: %v", err)
		}
		if _, ok := auth.PrincipalFrom(ctx); ok {
			t.Fatal("a nil-like principal reached the context")
		}
	}
}

func TestANilAuthenticatorRefusesToStart(t *testing.T) {
	var pointer *typedNilAuthenticator
	var function auth.AuthenticatorFunc
	for _, tc := range []struct {
		name  string
		authn auth.Authenticator
	}{
		{"an untyped nil", nil},
		{"a typed-nil pointer", pointer},
		{"a typed-nil function", function},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("NewGuard accepted a nil authenticator, so the process starts and fails on its first request")
				}
			}()
			auth.NewGuard(tc.authn)
		})
	}
}

type typedNilAuthenticator struct{}

func (*typedNilAuthenticator) Authenticate(context.Context, auth.Credential) (auth.Principal, error) {
	return auth.Claims{Sub: "should-not-run"}, nil
}

func TestAnEmptyCredentialSourceRefusesToStart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		option auth.Option
	}{
		{"an empty header", auth.Header("")},
		{"a whitespace header", auth.Header(" \t")},
		{"a nil lookup", auth.Lookup(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("NewGuard accepted a credential source that cannot find a credential")
				}
			}()
			auth.NewGuard(yes("u-1"), tc.option)
		})
	}
}

func TestANilOptionRemainsANoOp(t *testing.T) {
	guard := auth.NewGuard(yes("u-1"), nil)
	if _, err := guard.Authenticate(t.Context(), headers(map[string]string{"Authorization": "Bearer t"})); err != nil {
		t.Fatalf("a nil optional declaration changed or broke the guard: %v", err)
	}
}

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

	_, err := auth.Chain(refuses, refuses).Authenticate(context.Background(), auth.Credential{Token: "t"})
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("a chain that only refused answered %v, want a refusal", err)
	}
}
