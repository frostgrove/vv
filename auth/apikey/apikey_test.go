package apikey_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/apikey"
)

var batch = auth.Claims{Sub: "batch", Permissions: []auth.Permission{"article:read"}}

func store() apikey.Store {
	return apikey.Static(map[string]auth.Principal{"k-1": batch})
}

func cred(token string) auth.Credential {
	return auth.Credential{Scheme: apikey.DefaultScheme, Token: token}
}

func TestAKnownKeyAuthenticatesAndAnUnknownOneDoesNot(t *testing.T) {
	a := apikey.New(store())

	// The control comes first here on purpose: without it the refusal below
	// passes for an authenticator that refuses everything.
	t.Run("control: a known key answers its principal", func(t *testing.T) {
		p, err := a.Authenticate(t.Context(), cred("k-1"))
		if err != nil {
			t.Fatalf("a key that was issued was refused: %v", err)
		}
		if p.Subject() != "batch" {
			t.Fatalf("the store answered subject %q, want batch", p.Subject())
		}
	})

	t.Run("an unknown key is refused", func(t *testing.T) {
		_, err := a.Authenticate(t.Context(), cred("k-2"))
		if !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("an unissued key answered %v, want a refusal", err)
		}
	})

	t.Run("a key that is a prefix of a real one is refused", func(t *testing.T) {
		if _, err := a.Authenticate(t.Context(), cred("k-")); err == nil {
			t.Fatal("a prefix of a real key was accepted")
		}
	})

	t.Run("no key at all is refused", func(t *testing.T) {
		if _, err := a.Authenticate(t.Context(), cred("")); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatal("an empty key was accepted")
		}
	})
}

func TestTheSchemeIsCheckedUnlessItIsWaived(t *testing.T) {
	t.Run("another scheme is refused by default", func(t *testing.T) {
		a := apikey.New(store())
		_, err := a.Authenticate(t.Context(), auth.Credential{Scheme: "Bearer", Token: "k-1"})
		if err == nil {
			t.Fatal("a bearer token was handed to the key store, so every expired JWT becomes a candidate key")
		}
	})

	t.Run("AnyScheme waives it", func(t *testing.T) {
		a := apikey.New(store(), apikey.AnyScheme())
		if _, err := a.Authenticate(t.Context(), auth.Credential{Scheme: "Bearer", Token: "k-1"}); err != nil {
			t.Fatalf("AnyScheme still refused on the scheme: %v", err)
		}
	})

	t.Run("Scheme replaces it", func(t *testing.T) {
		a := apikey.New(store(), apikey.Scheme("X-Key"))
		if _, err := a.Authenticate(t.Context(), auth.Credential{Scheme: "x-key", Token: "k-1"}); err != nil {
			t.Fatalf("the replaced scheme was not accepted case-insensitively: %v", err)
		}
	})
}

// This is the distinction the three-result Lookup exists for. A store that
// cannot answer must not be reported as a bad key.
func TestAStoreFailureIsNotARefusal(t *testing.T) {
	down := errors.New("dial tcp: connection refused")
	a := apikey.New(apikey.StoreFunc(func(context.Context, string) (auth.Principal, bool, error) {
		return nil, false, down
	}))

	_, err := a.Authenticate(t.Context(), cred("k-1"))
	if !errors.Is(err, down) {
		t.Fatalf("a store outage answered %v, want the store's own error", err)
	}
	if errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("a store outage was reported as a bad key, so every caller would rotate their keys during an incident")
	}
}

func TestStaticCopiesTheMapItWasGiven(t *testing.T) {
	m := map[string]auth.Principal{"k-1": batch}
	a := apikey.New(apikey.Static(m))
	delete(m, "k-1")

	if _, err := a.Authenticate(t.Context(), cred("k-1")); err != nil {
		t.Fatal("mutating the caller's map changed who may call")
	}
}

func TestANilStoreRefusesToStart(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New accepted a nil store, so the process starts and refuses every request at run time instead")
		}
	}()
	apikey.New(nil)
}
