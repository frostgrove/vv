package apikey_test

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"sync"
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

	for _, tc := range []struct {
		name, scheme string
	}{
		{"an empty Scheme refuses to start", ""},
		{"a whitespace Scheme refuses to start", " \t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("Scheme accepted an empty name as an implicit AnyScheme waiver")
				}
			}()
			apikey.New(store(), apikey.Scheme(tc.scheme))
		})
	}
}

func TestHeaderReadsABareKeyWithoutChangingTheAuthorizationAPI(t *testing.T) {
	a := apikey.New(store())

	t.Run("a bare key is read from the named header", func(t *testing.T) {
		g := auth.NewGuard(a, apikey.Header("X-Api-Key"))
		ctx, err := g.Authenticate(t.Context(), keyHeaders(map[string]string{"X-Api-Key": "k-1"}))
		if err != nil {
			t.Fatalf("a bare API key was refused: %v", err)
		}
		p, ok := auth.PrincipalFrom(ctx)
		if !ok || p.Subject() != "batch" {
			t.Fatalf("the bare key did not produce its principal: %v %v", p, ok)
		}
	})

	// The control: Header must be the thing that made the bare value usable,
	// and Lookup semantics say it replaces rather than augments Authorization.
	t.Run("control: the helper does not silently keep reading Authorization", func(t *testing.T) {
		g := auth.NewGuard(a, apikey.Header("X-Api-Key"))
		_, err := g.Authenticate(t.Context(), keyHeaders(map[string]string{"Authorization": "ApiKey k-1"}))
		if !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("the bare-header helper still read Authorization: %v", err)
		}
	})

	t.Run("the original Authorization scheme API still works", func(t *testing.T) {
		g := auth.NewGuard(a)
		if _, err := g.Authenticate(t.Context(), keyHeaders(map[string]string{"Authorization": "ApiKey k-1"})); err != nil {
			t.Fatalf("adding the bare-header helper broke Authorization scheme credentials: %v", err)
		}
	})
}

func keyHeaders(values map[string]string) func(string) string {
	h := http.Header{}
	for name, value := range values {
		h.Set(name, value)
	}
	return h.Get
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

func TestStaticSnapshotsClaimsAndReturnsAPerRequestCopy(t *testing.T) {
	nested := map[string]any{"region": "north"}
	groups := []any{"editors"}
	declared := auth.Claims{
		Sub:         "batch",
		Roles:       []auth.Role{"editor"},
		Permissions: []auth.Permission{"article:read"},
		Attrs: map[string]any{
			"tenant": "original",
			"nested": nested,
			"groups": groups,
		},
	}
	static := apikey.Static(map[string]auth.Principal{"k-1": declared})

	// Mutating every reference-bearing part of the declaration after Static
	// returns must not rewrite the identity it stored.
	declared.Roles[0] = "admin"
	declared.Permissions[0] = "article:delete"
	declared.Attrs["tenant"] = "changed"
	nested["region"] = "south"
	groups[0] = "admins"

	first, ok, err := static.Lookup(t.Context(), "k-1")
	if err != nil || !ok {
		t.Fatalf("the frozen key was not found: %v %v", ok, err)
	}
	firstClaims, ok := first.(auth.Claims)
	if !ok {
		t.Fatalf("Static changed the Claims concrete type to %T", first)
	}
	assertFrozenClaims(t, firstClaims)

	// A caller owns the value returned for its request. These mutations must not
	// leak back into the store or into the next request.
	firstClaims.Roles[0] = "owner"
	firstClaims.Permissions[0] = "root"
	firstClaims.Attrs["tenant"] = "request-one"
	firstClaims.Attrs["nested"].(map[string]any)["region"] = "west"
	firstClaims.Attrs["groups"].([]any)[0] = "owners"

	second, ok, err := static.Lookup(t.Context(), "k-1")
	if err != nil || !ok {
		t.Fatalf("the frozen key was not found a second time: %v %v", ok, err)
	}
	assertFrozenClaims(t, second.(auth.Claims))
}

func TestStaticClaimsAreIndependentAcrossConcurrentRequests(t *testing.T) {
	static := apikey.Static(map[string]auth.Principal{
		"k-1": auth.Claims{
			Sub:         "batch",
			Permissions: []auth.Permission{"article:read"},
			Attrs:       map[string]any{"nested": map[string]any{"n": 1}},
		},
	})
	ctx := t.Context()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for i := 0; i < 500; i++ {
				principal, ok, err := static.Lookup(ctx, "k-1")
				if err != nil || !ok {
					t.Errorf("worker %d lookup failed: %v %v", worker, ok, err)
					return
				}
				claims := principal.(auth.Claims)
				claims.Permissions[0] = auth.Permission("worker")
				claims.Attrs["nested"].(map[string]any)["n"] = worker
			}
		}(worker)
	}
	close(start)
	wg.Wait()
}

func TestStaticDoesNotRetainTheMutableClaimsDeclaration(t *testing.T) {
	roles := []auth.Role{"editor"}
	nested := map[string]any{"region": "north"}
	static := apikey.Static(map[string]auth.Principal{
		"k-1": auth.Claims{Sub: "batch", Roles: roles, Attrs: map[string]any{"nested": nested}},
	})
	ctx := t.Context()

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 500; i++ {
			roles[0] = auth.Role("declaration")
			nested["region"] = i
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 500; i++ {
			principal, ok, err := static.Lookup(ctx, "k-1")
			if err != nil || !ok {
				t.Errorf("lookup failed while the declaration was mutated: %v %v", ok, err)
				return
			}
			claims := principal.(auth.Claims)
			if !claims.In("editor") || claims.Attrs["nested"].(map[string]any)["region"] != "north" {
				t.Errorf("lookup observed caller-owned declaration mutation: %#v", claims)
				return
			}
		}
	}()
	close(start)
	wg.Wait()
}

func TestStaticPreservesPointerClaimsWithoutSharingThem(t *testing.T) {
	declared := &auth.Claims{Sub: "batch", Attrs: map[string]any{"tenant": "a"}}
	static := apikey.Static(map[string]auth.Principal{"k-1": declared})
	first, _, _ := static.Lookup(t.Context(), "k-1")
	second, _, _ := static.Lookup(t.Context(), "k-1")
	firstClaims, firstOK := first.(*auth.Claims)
	secondClaims, secondOK := second.(*auth.Claims)
	if !firstOK || !secondOK {
		t.Fatalf("Static changed pointer Claims into %T and %T", first, second)
	}
	if firstClaims == secondClaims {
		t.Fatal("two requests received the same *Claims")
	}
	firstClaims.Attrs["tenant"] = "changed"
	if tenant, _ := secondClaims.Attr("tenant"); tenant != "a" {
		t.Fatalf("one request changed another request's pointer Claims: %v", tenant)
	}
}

func TestStaticCanSnapshotACyclicAttributeContainer(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	static := apikey.Static(map[string]auth.Principal{
		"k-1": auth.Claims{Sub: "batch", Attrs: map[string]any{"cycle": cycle}},
	})
	principal, ok, err := static.Lookup(t.Context(), "k-1")
	if err != nil || !ok {
		t.Fatalf("the key with cyclic attributes was not found: %v %v", ok, err)
	}
	cloned := principal.(auth.Claims).Attrs["cycle"].(map[string]any)
	self := cloned["self"].(map[string]any)
	self["request"] = true
	if _, leaked := cycle["request"]; leaked {
		t.Fatal("the cyclic attribute still points into the declaration")
	}
}

func TestTryStaticCopiesSupportedStructAttributesForEveryRequest(t *testing.T) {
	declared := exportedAttribute{
		Bytes:  []byte("original"),
		Labels: map[string]string{"region": "north"},
	}
	store, err := apikey.TryStatic(map[string]auth.Principal{
		"k-1": auth.Claims{Sub: "batch", Attrs: map[string]any{"value": declared}},
	})
	if err != nil {
		t.Fatalf("TryStatic refused a fully copyable exported struct: %v", err)
	}
	declared.Bytes[0] = 'X'
	declared.Labels["region"] = "south"

	first, ok, err := store.Lookup(t.Context(), "k-1")
	if err != nil || !ok {
		t.Fatalf("first lookup failed: %v %v", ok, err)
	}
	firstValue := first.(auth.Claims).Attrs["value"].(exportedAttribute)
	if string(firstValue.Bytes) != "original" || firstValue.Labels["region"] != "north" {
		t.Fatalf("the declaration changed the snapshot: %#v", firstValue)
	}
	firstValue.Bytes[0] = 'Y'
	firstValue.Labels["region"] = "west"

	second, ok, err := store.Lookup(t.Context(), "k-1")
	if err != nil || !ok {
		t.Fatalf("second lookup failed: %v %v", ok, err)
	}
	secondValue := second.(auth.Claims).Attrs["value"].(exportedAttribute)
	if string(secondValue.Bytes) != "original" || secondValue.Labels["region"] != "north" {
		t.Fatalf("one request changed the next request's struct attribute: %#v", secondValue)
	}
}

func TestTryStaticRejectsMutableStateItCannotCopySoundly(t *testing.T) {
	bufferValue := *bytes.NewBufferString("mutable")
	integerValue := *big.NewInt(42)
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"bytes.Buffer value", bufferValue},
		{"bytes.Buffer pointer", bytes.NewBufferString("mutable")},
		{"big.Int value", integerValue},
		{"big.Int pointer", big.NewInt(42)},
		{"custom value with unexported state", hiddenMutableAttribute{values: []string{"a"}}},
		{"custom pointer with unexported state", &hiddenMutableAttribute{values: []string{"a"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const secretKey = "credential-that-must-not-enter-an-error"
			_, err := apikey.TryStatic(map[string]auth.Principal{
				secretKey: auth.Claims{Sub: "batch", Attrs: map[string]any{"unsafe": tc.value}},
			})
			if !errors.Is(err, apikey.ErrUnsupportedStaticAttribute) {
				t.Fatalf("TryStatic answered %v, want ErrUnsupportedStaticAttribute", err)
			}
			if strings.Contains(err.Error(), secretKey) {
				t.Fatal("the configuration error disclosed an API key")
			}
		})
	}
}

func TestStaticPanicsAtDeclarationWhenClaimsCannotBeSnapshotted(t *testing.T) {
	defer func() {
		panicked, ok := recover().(error)
		if !ok || !errors.Is(panicked, apikey.ErrUnsupportedStaticAttribute) {
			t.Fatalf("Static panic was %v, want ErrUnsupportedStaticAttribute", panicked)
		}
	}()
	apikey.Static(map[string]auth.Principal{
		"k-1": auth.Claims{Sub: "batch", Attrs: map[string]any{"buffer": bytes.NewBuffer(nil)}},
	})
}

type exportedAttribute struct {
	Bytes  []byte
	Labels map[string]string
}

type hiddenMutableAttribute struct {
	values []string
}

func TestAStaticTypedNilPrincipalIsNotAKnownIdentity(t *testing.T) {
	var principal *nilPrincipal
	static := apikey.Static(map[string]auth.Principal{"k-1": principal})
	if got, ok, err := static.Lookup(t.Context(), "k-1"); err != nil || ok || got != nil {
		t.Fatalf("a key mapped to a typed-nil identity answered %v, %v, %v", got, ok, err)
	}
}

func assertFrozenClaims(t *testing.T, claims auth.Claims) {
	t.Helper()
	if !claims.In("editor") || claims.In("admin") {
		t.Fatalf("roles were not frozen: %v", claims.Roles)
	}
	if !claims.Has("article:read") || claims.Has("article:delete") {
		t.Fatalf("permissions were not frozen: %v", claims.Permissions)
	}
	if tenant, _ := claims.Attr("tenant"); tenant != "original" {
		t.Fatalf("the top-level attribute was not frozen: %v", tenant)
	}
	if region := claims.Attrs["nested"].(map[string]any)["region"]; region != "north" {
		t.Fatalf("a nested map was not frozen: %v", region)
	}
	if group := claims.Attrs["groups"].([]any)[0]; group != "editors" {
		t.Fatalf("a nested slice was not frozen: %v", group)
	}
}

type nilPrincipal struct{}

func (*nilPrincipal) Subject() string          { panic("typed-nil principal was called") }
func (*nilPrincipal) In(auth.Role) bool        { panic("typed-nil principal was called") }
func (*nilPrincipal) Has(auth.Permission) bool { panic("typed-nil principal was called") }
func (*nilPrincipal) Attr(string) (any, bool)  { panic("typed-nil principal was called") }

func TestANilStoreRefusesToStart(t *testing.T) {
	var pointer *typedNilStore
	var function apikey.StoreFunc
	for _, tc := range []struct {
		name  string
		store apikey.Store
	}{
		{"an untyped nil", nil},
		{"a typed-nil pointer", pointer},
		{"a typed-nil function", function},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("New accepted a nil store, so the process starts and fails on its first request")
				}
			}()
			apikey.New(tc.store)
		})
	}
}

type typedNilStore struct{}

func (*typedNilStore) Lookup(context.Context, string) (auth.Principal, bool, error) {
	return batch, true, nil
}

func TestAnEmptyBareHeaderRefusesToStart(t *testing.T) {
	for _, tc := range []struct {
		name, header string
	}{
		{"an empty name", ""},
		{"a whitespace name", " \t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("Header accepted an empty name, so the guard can never find a key")
				}
			}()
			auth.NewGuard(apikey.New(store()), apikey.Header(tc.header))
		})
	}
}
