package cache

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func evictionDomainSpec(t *testing.T, resource ResourceID, declarations ...ResourceDeclaration) (*Cache[string, string], ActivationSpec) {
	t.Helper()
	target, definition := defineActivationCache(t, "cards", Hot, "")
	spec := activationSpec([]Set{MustSet(definition)}, []Provider{{
		ID:       "provider",
		Resource: resource,
		Kind:     MemoryProviderKind,
		Backend:  newActivationBackend(string(resource)),
	}})
	spec.Resources = declarations
	return target, spec
}

func activationProblems(t *testing.T, err error) []string {
	t.Helper()
	var activationErr *ActivationError
	if !errors.As(err, &activationErr) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("activation error = %v, want a cache activation refusal", err)
	}
	return activationErr.Problems()
}

func mentions(problems []string, fragment string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, fragment) {
			return true
		}
	}
	return false
}

func TestACacheOnAnUndeclaredResourceIsRefusedOnlyWhereTheRootAsksForDeclarations(t *testing.T) {
	tests := map[string]struct {
		declarations []ResourceDeclaration
		required     bool
		refused      bool
	}{
		"nobody asked, so silence is accepted": {},
		"the root asks and nobody declared":    {required: true, refused: true},
		"the root asks and the cache owns it": {
			declarations: []ResourceDeclaration{{Resource: "redis-main", Tenants: []ResourceTenant{CacheTenant}}},
			required:     true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			target, spec := evictionDomainSpec(t, "redis-main", test.declarations...)
			spec.RequireDeclaredResources = test.required

			err := Activate(context.Background(), spec)

			if !test.refused {
				if err != nil {
					t.Fatalf("Activate() error = %v, want the cache published", err)
				}
				return
			}
			problems := activationProblems(t, err)
			if !mentions(problems, "resource \"redis-main\" declares no tenant") {
				t.Fatalf("problems = %#v, want the undeclared resource named", problems)
			}
			if target.inner.Load() != nil {
				t.Fatal("the cache was published onto a resource nothing proved separate")
			}
		})
	}
}

func TestACacheNeverSharesAnEvictionDomainWithDurableState(t *testing.T) {
	tests := map[string]struct {
		tenants []ResourceTenant
		waiver  SharedResourceWaiver
		refused bool
	}{
		"revoked sessions live there":         {tenants: []ResourceTenant{DurableSecurityTenant}, refused: true},
		"queued jobs live there":              {tenants: []ResourceTenant{DurableWorkTenant}, refused: true},
		"a waiver is offered for the sharing": {tenants: []ResourceTenant{DurableSecurityTenant}, waiver: SharedDurableSecurity("staging runs one redis"), refused: true},
		"the resource is the cache's own":     {tenants: []ResourceTenant{CacheTenant}},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			target, spec := evictionDomainSpec(t, "redis-main", ResourceDeclaration{
				Resource: "redis-main",
				Tenants:  test.tenants,
				Waiver:   test.waiver,
			})

			err := Activate(context.Background(), spec)

			if !test.refused {
				if err != nil {
					t.Fatalf("Activate() error = %v, want the cache admitted to a resource it does not share", err)
				}
				return
			}
			if !mentions(activationProblems(t, err), "shares the eviction domain of resource \"redis-main\"") {
				t.Fatalf("problems = %#v, want the shared eviction domain named", activationProblems(t, err))
			}
			if target.inner.Load() != nil {
				t.Fatal("the refused cache was published onto the shared resource anyway")
			}
		})
	}
}

func TestDurableWorkAndDurableSecurityShareOneResourceOnlyWithAWrittenReason(t *testing.T) {
	tests := map[string]struct {
		tenants  []ResourceTenant
		waiver   SharedResourceWaiver
		fragment string
	}{
		"no waiver at all": {
			tenants:  []ResourceTenant{DurableWorkTenant, DurableSecurityTenant},
			fragment: "without a SharedDurableSecurity waiver",
		},
		"a waiver with a reason": {
			tenants: []ResourceTenant{DurableWorkTenant, DurableSecurityTenant},
			waiver:  SharedDurableSecurity("one redis until the jobs cluster lands"),
		},
		"a waiver with a blank reason": {
			tenants:  []ResourceTenant{DurableWorkTenant, DurableSecurityTenant},
			waiver:   SharedDurableSecurity("   "),
			fragment: "without a usable reason",
		},
		"a waiver that excuses nothing": {
			tenants:  []ResourceTenant{DurableWorkTenant},
			waiver:   SharedDurableSecurity("copied from another environment"),
			fragment: "waives a sharing it does not declare",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, spec := evictionDomainSpec(t, "cache-redis", ResourceDeclaration{
				Resource: "durable-redis",
				Tenants:  test.tenants,
				Waiver:   test.waiver,
			})

			err := Activate(context.Background(), spec)

			if test.fragment == "" {
				if err != nil {
					t.Fatalf("Activate() error = %v, want the waived sharing accepted", err)
				}
				return
			}
			if problems := activationProblems(t, err); !mentions(problems, test.fragment) {
				t.Fatalf("problems = %#v, want %q", problems, test.fragment)
			}
		})
	}
}

func TestAResourceDeclarationThatNamesNothingUsableIsRefused(t *testing.T) {
	tests := map[string]struct {
		declarations []ResourceDeclaration
		fragment     string
	}{
		"an empty identity": {
			declarations: []ResourceDeclaration{{Tenants: []ResourceTenant{DurableWorkTenant}}},
			fragment:     "resource declaration 0 has an invalid identity",
		},
		"no tenant": {
			declarations: []ResourceDeclaration{{Resource: "durable-redis"}},
			fragment:     "resource \"durable-redis\" declares no tenant",
		},
		"a tenant the cache never heard of": {
			declarations: []ResourceDeclaration{{Resource: "durable-redis", Tenants: []ResourceTenant{"analytics"}}},
			fragment:     "declares unknown tenant \"analytics\"",
		},
		"the same resource twice": {
			declarations: []ResourceDeclaration{
				{Resource: "durable-redis", Tenants: []ResourceTenant{DurableWorkTenant}},
				{Resource: "durable-redis", Tenants: []ResourceTenant{DurableSecurityTenant}},
			},
			fragment: "duplicate resource declaration \"durable-redis\"",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, spec := evictionDomainSpec(t, "cache-redis", test.declarations...)

			err := Activate(context.Background(), spec)

			if problems := activationProblems(t, err); !mentions(problems, test.fragment) {
				t.Fatalf("problems = %#v, want %q", problems, test.fragment)
			}
		})
	}
}
