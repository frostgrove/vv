package cache

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type declaringBackend struct {
	*seamBackend
	declared []Capability
	panics   bool
}

func (this *declaringBackend) DeclaredCapabilities() []Capability {
	if this.panics {
		panic("declared capabilities")
	}
	return this.declared
}

type fullCapabilityBackend struct {
	*seamBackend
	swapped     bool
	maintained  bool
	healthy     error
	invalidated Tag
	transacted  bool
}

func (this *fullCapabilityBackend) CompareAndSwap(context.Context, Address, []byte, []byte, Expiry) (bool, error) {
	this.swapped = true
	return true, nil
}

func (this *fullCapabilityBackend) DeleteExpired(context.Context, MaintenanceLimit) (MaintenanceReport, error) {
	this.maintained = true
	return MaintenanceReport{}, nil
}

func (this *fullCapabilityBackend) CheckBackend(context.Context) error { return this.healthy }

func (this *fullCapabilityBackend) InvalidateTag(_ context.Context, _ Namespace, tag Tag) error {
	this.invalidated = tag
	return nil
}

func (this *fullCapabilityBackend) InTransaction(ctx context.Context, run func(context.Context, Backend) error) error {
	this.transacted = true
	return run(ctx, this)
}

type passthroughBackend struct {
	Backend
}

func (this *passthroughBackend) Next() Backend { return this.Backend }

func TestADriverPublishesACapabilityTheCoreNeverHeardOf(t *testing.T) {
	backend := &declaringBackend{seamBackend: newSeamBackend(newCacheTestPolicy(64)), declared: []Capability{"vector_search"}}
	wrapped := &passthroughBackend{Backend: backend}

	if !Supports(wrapped, Capability("vector_search")) {
		t.Fatal("a capability the driver declared was invisible to Supports")
	}
	if Supports(wrapped, Capability("nobody_declared_this")) {
		t.Fatal("Supports answered yes for a capability nobody declared")
	}
}

func TestADeclaredNameNeverGrantsABuiltInCapability(t *testing.T) {
	backend := &declaringBackend{
		seamBackend: newSeamBackend(newCacheTestPolicy(64)),
		declared:    []Capability{BatchReadCapability, HealthCapability},
	}

	if Supports(backend, BatchReadCapability) {
		t.Fatal("a backend without GetMany claimed batch reads by declaration")
	}
	if Supports(backend, HealthCapability) {
		t.Fatal("a backend without CheckBackend claimed health by declaration")
	}
	if declared := DeclaredCapabilitiesOf(backend); len(declared) != 0 {
		t.Fatalf("built-in names survived the declared set: %v", declared)
	}
}

func TestEveryBuiltInCapabilityIsFoundThroughADecorator(t *testing.T) {
	base := &fullCapabilityBackend{seamBackend: newSeamBackend(newCacheTestPolicy(64))}
	wrapped := &passthroughBackend{Backend: &passthroughBackend{Backend: base}}

	for _, capability := range []Capability{
		CompareAndSwapCapability,
		MaintenanceCapability,
		HealthCapability,
		TagInvalidationCapability,
		TransactionCapability,
	} {
		if !Supports(wrapped, capability) {
			t.Fatalf("capability %q was lost behind a decorator", capability)
		}
	}
	if Supports(wrapped, BatchReadCapability) {
		t.Fatal("a backend without GetMany reported batch reads")
	}
}

func TestADeclaringBackendThatMisbehavesContributesNothing(t *testing.T) {
	tests := map[string]*declaringBackend{
		"panicking": {seamBackend: newSeamBackend(newCacheTestPolicy(64)), panics: true},
		"unbounded": {seamBackend: newSeamBackend(newCacheTestPolicy(64)), declared: manyCapabilities(MaxDeclaredCapabilities + 1)},
		"malformed": {seamBackend: newSeamBackend(newCacheTestPolicy(64)), declared: []Capability{"Upper Case", "", "_leading"}},
	}
	for name, backend := range tests {
		t.Run(name, func(t *testing.T) {
			if declared := DeclaredCapabilitiesOf(backend); len(declared) != 0 {
				t.Fatalf("declared = %v, want none", declared)
			}
		})
	}
}

func manyCapabilities(count int) []Capability {
	values := make([]Capability, 0, count)
	for index := 0; index < count; index++ {
		values = append(values, Capability(fmt.Sprintf("capability_%d", index)))
	}
	return values
}

func requiringDefinition(t *testing.T, required ...Capability) (*Cache[string, string], *Definition[string, string]) {
	t.Helper()
	target := Auto[string, string](Hot)
	definition, err := Define(target, DefinitionSpec[string, string]{
		Name:      "required-health",
		Namespace: NamespaceTemplate{Purpose: "required-health-values", Generation: 1},
		Scope:     GlobalPlan[string](),
		Keys: MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) {
			return []byte(key), nil
		}),
		Values:   String(1),
		Provider: "provider",
		Requires: required,
	})
	if err != nil {
		t.Fatalf("Define() error = %v", err)
	}
	return target, definition
}

func TestARequirementBeyondBatchReadReachesActivation(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		target, definition := requiringDefinition(t, HealthCapability)
		err := Activate(context.Background(), ActivationSpec{
			Application: "seam",
			Environment: "test",
			Sets:        []Set{MustSet(definition)},
			Providers: []Provider{{
				ID:      "provider",
				Kind:    MemoryProviderKind,
				Backend: reviewProcessBackend("without-health"),
			}},
		})
		if !errors.Is(err, ErrInvalid) || target.Describe().Activated {
			t.Fatalf("activation error = %v, descriptor = %+v", err, target.Describe())
		}
	})
	t.Run("present", func(t *testing.T) {
		target, definition := requiringDefinition(t, HealthCapability)
		base := &fullCapabilityBackend{seamBackend: newSeamBackend(newCacheTestPolicy(64))}
		base.description.MaxItemBytes = 32 << 20
		err := Activate(context.Background(), ActivationSpec{
			Application: "seam",
			Environment: "test",
			Sets:        []Set{MustSet(definition)},
			Providers: []Provider{{
				ID:      "provider",
				Kind:    MemoryProviderKind,
				Backend: base,
			}},
		})
		if err != nil {
			t.Fatalf("Activate() error = %v", err)
		}
		descriptor := target.Describe()
		if !descriptor.Activated || len(descriptor.Requires) != 1 || descriptor.Requires[0] != HealthCapability {
			t.Fatalf("descriptor = %+v", descriptor)
		}
	})
}

func TestATagIsAValidatedValue(t *testing.T) {
	if _, err := NewTag(""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewTag(\"\") error = %v", err)
	}
	if _, err := NewTag("tenant\tone"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewTag() accepted a control character: %v", err)
	}
	tag, err := NewTag("tenant:one")
	if err != nil || tag.Value() != "tenant:one" || tag.IsZero() {
		t.Fatalf("NewTag() = %v, %v", tag.Value(), err)
	}
}
