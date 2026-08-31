package cache

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type activationBackend struct {
	*coordinationBackend
	name string
}

func newActivationBackend(name string) *activationBackend {
	return &activationBackend{coordinationBackend: newCoordinationBackend(), name: name}
}

func (backend *activationBackend) DescribeBackend() BackendDescription {
	return BackendDescription{
		Name:              backend.name,
		Topology:          ProcessBackend,
		ExpiryClock:       ProcessExpiryClock,
		MaxItemBytes:      32 << 20,
		RelativeExpiry:    true,
		MaxRelativeExpiry: 365 * 24 * time.Hour,
		CapacityBounded:   true,
	}
}

func TestAutoDefaultsAndRejectsInvalidArguments(t *testing.T) {
	automatic := Auto[string, string]()
	descriptor := automatic.Describe()
	if descriptor.Profile != Hot.Name() || descriptor.ProviderKind != MemoryProviderKind || descriptor.Activated {
		t.Fatalf("default descriptor = %+v", descriptor)
	}
	explicit := Auto[string, string](Warm)
	if descriptor := explicit.Describe(); descriptor.Profile != Warm.Name() || descriptor.ProviderKind != PostgreSQLProviderKind {
		t.Fatalf("explicit descriptor = %+v", descriptor)
	}
	assertActivationPanic(t, func() { Auto[string, string](Hot.With(nil)) })
	assertActivationPanic(t, func() { Auto[string, string](Hot, Warm) })
}

func TestNewBuildsStandaloneActivatedCache(t *testing.T) {
	backend := newCoordinationBackend()
	instance := newCoordinationCache(t, backend, String(ValueSchema(1)))
	if instance.automatic.Load() != nil || instance.definition.Load() != nil {
		t.Fatal("standalone cache acquired automatic declaration state")
	}
	descriptor := instance.Describe()
	if !descriptor.Activated || descriptor.Purpose != "coordination" || descriptor.Backend.Name != "coordination-test" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	if err := instance.Put(context.Background(), "key", "value"); err != nil {
		t.Fatal(err)
	}
	result, err := instance.Lookup(context.Background(), "key")
	if err != nil || result.Value != "value" || result.State != Hit {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}

func TestDefineRejectsDuplicateAndNonAutomaticTargets(t *testing.T) {
	target, definition := defineActivationCache(t, "first", Hot, "")
	if _, err := Define(target, activationDefinitionSpec("second", "")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate definition error = %v", err)
	}
	if target.definition.Load() != definition || target.Describe().LogicalName != "first" {
		t.Fatal("duplicate definition replaced the original")
	}
	standalone := newCoordinationCache(t, newCoordinationBackend(), String(ValueSchema(1)))
	if _, err := Define(standalone, activationDefinitionSpec("standalone", "")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("standalone definition error = %v", err)
	}
	if _, err := Define(new(Cache[string, string]), activationDefinitionSpec("zero", "")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero target definition error = %v", err)
	}
}

func TestUnactivatedDefinitionFailsBeforeUsingDependencies(t *testing.T) {
	target, _ := defineActivationCache(t, "inactive", Hot, "")
	ctx := context.Background()
	if _, err := target.Lookup(ctx, "key"); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("lookup error = %v", err)
	}
	if err := target.Put(ctx, "key", "value"); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("put error = %v", err)
	}
	if err := target.Forget(ctx, "key"); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("forget error = %v", err)
	}
	var loaderCalls atomic.Int32
	_, err := target.Resolve(ctx, "key", func(context.Context, string) (LoadResult[string], error) {
		loaderCalls.Add(1)
		return Present("value"), nil
	})
	if !errors.Is(err, ErrNotActivated) || loaderCalls.Load() != 0 {
		t.Fatalf("resolve error = %v, loader calls = %d", err, loaderCalls.Load())
	}
	if descriptor := target.Describe(); descriptor.Activated || descriptor.LogicalName != "inactive" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
}

func TestProviderSelectionPreferredFallbackMissingAndAmbiguous(t *testing.T) {
	tests := []struct {
		name        string
		providers   []Provider
		wantID      ProviderID
		wantBackend string
		wantError   string
	}{
		{
			name: "preferred beats fallback",
			providers: []Provider{
				activationProvider("fallback", true),
				activationProvider("preferred", false),
			},
			wantID: "preferred", wantBackend: "backend-preferred",
		},
		{
			name: "single fallback",
			providers: []Provider{
				activationProvider("fallback", true),
			},
			wantID: "fallback", wantBackend: "backend-fallback",
		},
		{name: "missing", wantError: "provider kind \"memory\" is missing"},
		{
			name: "ambiguous preferred",
			providers: []Provider{
				activationProvider("preferred-a", false),
				activationProvider("preferred-b", false),
				activationProvider("fallback", true),
			},
			wantError: "provider kind \"memory\" is ambiguous",
		},
		{
			name: "ambiguous fallback",
			providers: []Provider{
				activationProvider("fallback-a", true),
				activationProvider("fallback-b", true),
			},
			wantError: "fallback provider kind \"memory\" is ambiguous",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, definition := defineActivationCache(t, "selected", Hot, "")
			set := MustSet(definition)
			err := Activate(context.Background(), activationSpec([]Set{set}, test.providers))
			if test.wantError != "" {
				if !errors.Is(err, ErrInvalid) || !strings.Contains(fmt.Sprint(err), test.wantError) {
					t.Fatalf("activation error = %v", err)
				}
				if target.inner.Load() != nil || target.Describe().Activated {
					t.Fatal("failed activation published the target")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			descriptor := target.Describe()
			if !descriptor.Activated || descriptor.ProviderID != test.wantID || descriptor.Backend.Name != test.wantBackend {
				t.Fatalf("descriptor = %+v", descriptor)
			}
		})
	}
}

type activationBarrierDeclaration struct {
	name      string
	key       *struct{}
	entered   chan struct{}
	release   chan struct{}
	committed atomic.Bool
	mu        sync.Mutex
}

func newActivationBarrierDeclaration(name string) *activationBarrierDeclaration {
	return &activationBarrierDeclaration{
		name:    name,
		key:     &struct{}{},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (declaration *activationBarrierDeclaration) Describe() Descriptor {
	return Descriptor{LogicalName: declaration.name, Profile: Disabled.Name(), ProviderKind: NoProviderKind}
}

func (declaration *activationBarrierDeclaration) declarationName() string { return declaration.name }
func (declaration *activationBarrierDeclaration) declarationKey() any     { return declaration.key }
func (*activationBarrierDeclaration) declarationMarker()                  {}
func (declaration *activationBarrierDeclaration) lockActivation()         { declaration.mu.Lock() }
func (declaration *activationBarrierDeclaration) unlockActivation()       { declaration.mu.Unlock() }
func (declaration *activationBarrierDeclaration) isActivated() bool {
	return declaration.committed.Load()
}

func (declaration *activationBarrierDeclaration) prepareActivation(activationInput, Provider) (activationPlan, error) {
	return activationPlan{commit: func() {
		close(declaration.entered)
		<-declaration.release
		declaration.committed.Store(true)
	}}, nil
}

func TestActivationVisibilityIsAllOrNothing(t *testing.T) {
	alpha, alphaDefinition := defineActivationCache(t, "alpha", Hot, "")
	omega, omegaDefinition := defineActivationCache(t, "omega", Hot, "")
	barrier := newActivationBarrierDeclaration("middle")
	set := MustSet(omegaDefinition, barrier, alphaDefinition)
	activationDone := make(chan error, 1)
	go func() {
		activationDone <- Activate(context.Background(), activationSpec([]Set{set}, []Provider{activationProvider("memory", false)}))
	}()
	waitSignal(t, barrier.entered, "middle activation commit")
	if alpha.inner.Load() == nil || omega.inner.Load() != nil {
		t.Fatalf("unexpected mid-commit cores: alpha=%p omega=%p", alpha.inner.Load(), omega.inner.Load())
	}
	for name, target := range map[string]*Cache[string, string]{"alpha": alpha, "omega": omega} {
		if _, err := target.Lookup(context.Background(), "key"); !errors.Is(err, ErrNotActivated) {
			t.Fatalf("%s lookup error = %v", name, err)
		}
		if target.Describe().Activated {
			t.Fatalf("%s became visible before the shared commit", name)
		}
	}
	close(barrier.release)
	if err := receiveError(t, activationDone); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]*Cache[string, string]{"alpha": alpha, "omega": omega} {
		result, err := target.Lookup(context.Background(), "key")
		if err != nil || result.State != Miss || !target.Describe().Activated {
			t.Fatalf("%s result = %+v, err = %v, descriptor = %+v", name, result, err, target.Describe())
		}
	}
}

func TestConcurrentActivationPublishesExactlyOnce(t *testing.T) {
	target, definition := defineActivationCache(t, "concurrent", Hot, "")
	set := MustSet(definition)
	spec := activationSpec([]Set{set}, []Provider{activationProvider("memory", false)})
	const workers = 32
	start := make(chan struct{})
	results := make(chan error, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results <- Activate(context.Background(), spec)
		}()
	}
	close(start)
	group.Wait()
	close(results)
	successes := 0
	failures := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "already activated") {
			t.Fatalf("unexpected activation error = %v", err)
		}
		failures++
	}
	if successes != 1 || failures != workers-1 || !target.Describe().Activated {
		t.Fatalf("successes=%d failures=%d descriptor=%+v", successes, failures, target.Describe())
	}
}

func TestDuplicateNamesAcrossSetsFailWithoutPublication(t *testing.T) {
	first, firstDefinition := defineActivationCache(t, "duplicate", Hot, "")
	second, secondDefinition := defineActivationCache(t, "duplicate", Hot, "")
	firstSet := MustSet(firstDefinition)
	secondSet := MustSet(secondDefinition)
	err := Activate(context.Background(), activationSpec(
		[]Set{secondSet, firstSet},
		[]Provider{activationProvider("memory", false)},
	))
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "duplicate cache name \"duplicate\"") {
		t.Fatalf("activation error = %v", err)
	}
	if first.inner.Load() != nil || second.inner.Load() != nil {
		t.Fatal("duplicate names caused partial publication")
	}
}

func TestDisabledDeclarationNeedsNoProvider(t *testing.T) {
	target := Auto[string, string](Disabled)
	invalidSpec := activationDefinitionSpec("disabled", "forbidden")
	if _, err := Define(target, invalidSpec); !errors.Is(err, ErrInvalid) {
		t.Fatalf("provider-bearing disabled definition error = %v", err)
	}
	definition, err := Define(target, activationDefinitionSpec("disabled", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := Activate(context.Background(), activationSpec([]Set{MustSet(definition)}, nil)); err != nil {
		t.Fatal(err)
	}
	descriptor := target.Describe()
	if !descriptor.Activated || !descriptor.Policy.Disabled || descriptor.ProviderKind != NoProviderKind || descriptor.ProviderID != "" || descriptor.Backend != (BackendDescription{}) {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	result, err := target.Lookup(context.Background(), "key")
	if err != nil || result.State != Miss {
		t.Fatalf("lookup result = %+v, err = %v", result, err)
	}
	var loads atomic.Int32
	result, err = target.Resolve(context.Background(), "key", func(context.Context, string) (LoadResult[string], error) {
		loads.Add(1)
		return Present("value"), nil
	})
	if err != nil || result.Value != "value" || result.State != Loaded || loads.Load() != 1 {
		t.Fatalf("resolve result = %+v, err = %v, loads = %d", result, err, loads.Load())
	}
}

func TestActivationDiagnosticsAreSortedAndOwned(t *testing.T) {
	first, firstDefinition := defineActivationCache(t, "duplicate", Hot, "")
	second, secondDefinition := defineActivationCache(t, "duplicate", Hot, "")
	provider := activationProvider("same", false)
	err := Activate(context.Background(), ActivationSpec{
		Application: "",
		Environment: " bad",
		Sets:        []Set{MustSet(secondDefinition), MustSet(firstDefinition)},
		Providers:   []Provider{provider, provider},
	})
	var activationErr *ActivationError
	if !errors.As(err, &activationErr) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("activation error = %v", err)
	}
	want := []string{
		"application is invalid",
		"duplicate cache name \"duplicate\"",
		"duplicate provider id \"same\"",
		"environment is invalid",
	}
	problems := activationErr.Problems()
	if !sort.StringsAreSorted(problems) || !reflect.DeepEqual(problems, want) {
		t.Fatalf("problems = %#v", problems)
	}
	problems[0] = "mutated"
	if !reflect.DeepEqual(activationErr.Problems(), want) {
		t.Fatal("Problems returned aliased storage")
	}
	if first.inner.Load() != nil || second.inner.Load() != nil {
		t.Fatal("invalid activation published a target")
	}
}

func TestDescribeDoesNotExposeRawKeysOrValues(t *testing.T) {
	target, definition := defineActivationCache(t, "described", Hot, "")
	set := MustSet(definition)
	if err := Activate(context.Background(), activationSpec([]Set{set}, []Provider{activationProvider("memory", false)})); err != nil {
		t.Fatal(err)
	}
	const rawKey = "raw-secret-cache-key"
	const rawValue = "raw-secret-cache-value"
	if err := target.Put(context.Background(), rawKey, rawValue); err != nil {
		t.Fatal(err)
	}
	descriptor := target.Describe()
	if !descriptor.Activated || descriptor.LogicalName != "described" || descriptor.Application != "application" || descriptor.Environment != "test" {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	rendered := fmt.Sprintf("%+v %+v %+v", descriptor, definition.Describe(), set.Describe())
	if strings.Contains(rendered, rawKey) || strings.Contains(rendered, rawValue) {
		t.Fatalf("descriptor exposed raw cache material: %s", rendered)
	}
}

func defineActivationCache(t *testing.T, name string, profile Profile, provider ProviderID) (*Cache[string, string], *Definition[string, string]) {
	t.Helper()
	target := Auto[string, string](profile)
	definition, err := Define(target, activationDefinitionSpec(name, provider))
	if err != nil {
		t.Fatal(err)
	}
	return target, definition
}

func activationDefinitionSpec(name string, provider ProviderID) DefinitionSpec[string, string] {
	return DefinitionSpec[string, string]{
		Name:      name,
		Namespace: NamespaceTemplate{Purpose: name + "-entries", Generation: 1},
		Scope:     GlobalPlan[string](),
		Keys: MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) {
			return []byte(key), nil
		}),
		Values:   String(ValueSchema(1)),
		Provider: provider,
	}
}

func activationProvider(id ProviderID, fallback bool) Provider {
	return Provider{
		ID:       id,
		Kind:     MemoryProviderKind,
		Backend:  newActivationBackend("backend-" + string(id)),
		Fallback: fallback,
	}
}

func activationSpec(sets []Set, providers []Provider) ActivationSpec {
	return ActivationSpec{
		Application: "application",
		Environment: "test",
		Sets:        sets,
		Providers:   providers,
	}
}

func assertActivationPanic(t *testing.T, operation func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("operation did not panic")
		}
	}()
	operation()
}
