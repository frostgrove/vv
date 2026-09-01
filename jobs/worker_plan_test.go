package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPlanResolvesEffectiveBindingsWithoutLifecycleEffects(t *testing.T) {
	var automaticCalls atomic.Int64
	automatic := Auto(Handler[string](func(_ context.Context, value string) error {
		if value != "automatic" {
			t.Fatalf("automatic payload = %q", value)
		}
		automaticCalls.Add(1)
		return nil
	}))
	var declaredCalls atomic.Int64
	declared := Declare[string](Heavy)
	declaredConsumer := On(declared, Handler[string](func(_ context.Context, value string) error {
		if value != "declared" {
			t.Fatalf("declared payload = %q", value)
		}
		declaredCalls.Add(1)
		return nil
	}), Binding("workers.declared"))
	var explicitCalls atomic.Int64
	explicit := testQueueDefinition(t, "workers.explicit", String(1))
	explicitConsumer := On(explicit, Handler[string](func(_ context.Context, value string) error {
		if value != "explicit" {
			t.Fatalf("explicit payload = %q", value)
		}
		explicitCalls.Add(1)
		return nil
	}), Binding("workers.primary"), Concurrency(4))
	MustWire(automatic, WireSpec[string]{Name: testJobName(t, "workers.automatic"), Codec: String(1)})
	MustWire(declared, WireSpec[string]{Name: testJobName(t, "workers.declared"), Codec: String(1)})
	catalog := MustCatalog(explicit, declared, automatic)
	beforeFingerprint := catalog.Fingerprint()
	beforeCatalog := catalog.Describe()
	plan, err := NewWorkerPlan(catalog, explicitConsumer, declaredConsumer, automatic)
	if err != nil {
		t.Fatal(err)
	}
	if automaticCalls.Load() != 0 || declaredCalls.Load() != 0 || explicitCalls.Load() != 0 {
		t.Fatal("worker planning invoked a handler")
	}
	description := plan.Describe()
	want := []WorkerBindingDescription{
		{Definition: automatic.Name(), Binding: mustWorkerBinding(t, automatic.Name().String()), Concurrency: Default.workerConcurrency},
		{Definition: declared.Name(), Binding: mustWorkerBinding(t, "workers.declared"), Concurrency: Heavy.workerConcurrency},
		{Definition: explicit.Name(), Binding: mustWorkerBinding(t, "workers.primary"), Concurrency: 4},
	}
	if plan.Len() != 3 || plan.TotalConcurrency() != Default.workerConcurrency+Heavy.workerConcurrency+4 || !reflect.DeepEqual(description.Bindings, want) || description.TotalConcurrency != plan.TotalConcurrency() || plan.CatalogFingerprint() != beforeFingerprint || description.CatalogFingerprint != beforeFingerprint {
		t.Fatalf("worker plan = %#v", description)
	}
	if catalog.Fingerprint() != beforeFingerprint || !reflect.DeepEqual(catalog.Describe(), beforeCatalog) {
		t.Fatal("worker deployment changed the durable catalog")
	}
	values := map[Name]string{automatic.Name(): "automatic", declared.Name(): "declared", explicit.Name(): "explicit"}
	for _, binding := range plan.workerBindings() {
		definition := binding.declaration.declarationName()
		declaration, ok := catalog.Lookup(definition)
		if !ok {
			t.Fatalf("definition %q disappeared", definition)
		}
		encoded, err := declaration.(DefinitionOf[string]).Encode(values[definition])
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := binding.decode(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if err := binding.handle(context.Background(), decoded); err != nil {
			t.Fatal(err)
		}
	}
	if automaticCalls.Load() != 1 || declaredCalls.Load() != 1 || explicitCalls.Load() != 1 {
		t.Fatalf("handler calls = %d, %d, %d", automaticCalls.Load(), declaredCalls.Load(), explicitCalls.Load())
	}
}

func TestWorkerPlanRequiresExplicitConcurrencyOnlyForExplicitDefinition(t *testing.T) {
	explicit := testQueueDefinition(t, "workers.explicit-required", String(1))
	handler := Handler[string](func(context.Context, string) error { return nil })
	if _, err := NewWorkerPlan(MustCatalog(explicit), On(explicit, handler)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing explicit concurrency = %v", err)
	}
	automatic := Auto(handler, Interactive)
	MustWire(automatic, WireSpec[string]{Name: testJobName(t, "workers.profile-default"), Codec: String(1)})
	plan, err := NewWorkerPlan(MustCatalog(automatic), automatic)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Describe().Bindings[0].Concurrency != Interactive.workerConcurrency {
		t.Fatalf("profile concurrency = %d", plan.Describe().Bindings[0].Concurrency)
	}
	overridden, err := NewWorkerPlan(MustCatalog(automatic), On(automatic, handler, Concurrency(7)))
	if err != nil {
		t.Fatal(err)
	}
	if overridden.Describe().Bindings[0].Concurrency != 7 {
		t.Fatalf("overridden concurrency = %d", overridden.Describe().Bindings[0].Concurrency)
	}
}

func TestWorkerPlanValidatesOptionsAndCopiesOnInput(t *testing.T) {
	definition := testQueueDefinition(t, "workers.options", String(1))
	catalog := MustCatalog(definition)
	handler := Handler[string](func(context.Context, string) error { return nil })
	var nilOption WorkerOption
	tests := []struct {
		name    string
		options []WorkerOption
		want    error
	}{
		{name: "nil", options: []WorkerOption{nilOption, Concurrency(1)}, want: ErrInvalid},
		{name: "duplicate binding", options: []WorkerOption{Binding("workers.one"), Binding("workers.two"), Concurrency(1)}, want: ErrInvalid},
		{name: "duplicate concurrency", options: []WorkerOption{Concurrency(1), Concurrency(2)}, want: ErrInvalid},
		{name: "empty binding", options: []WorkerOption{Binding(""), Concurrency(1)}, want: ErrInvalid},
		{name: "large binding", options: []WorkerOption{Binding(strings.Repeat("a", MaxBindingNameBytes+1)), Concurrency(1)}, want: ErrTooLarge},
		{name: "zero concurrency", options: []WorkerOption{Concurrency(0)}, want: ErrInvalid},
		{name: "negative concurrency", options: []WorkerOption{Concurrency(-1)}, want: ErrInvalid},
		{name: "large concurrency", options: []WorkerOption{Concurrency(MaxBindingConcurrency + 1)}, want: ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewWorkerPlan(catalog, On(definition, handler, test.options...)); !errors.Is(err, test.want) {
				t.Fatalf("worker options = %v", err)
			}
		})
	}
	options := []WorkerOption{Binding("workers.copied"), Concurrency(2)}
	consumer := On(definition, handler, options...)
	options[0] = Binding("workers.mutated")
	options[1] = Concurrency(3)
	plan, err := NewWorkerPlan(catalog, consumer)
	if err != nil {
		t.Fatal(err)
	}
	binding := plan.Describe().Bindings[0]
	if binding.Binding.String() != "workers.copied" || binding.Concurrency != 2 {
		t.Fatalf("On options were aliased: %#v", binding)
	}
}

func TestWorkerPlanConfiguresDynamicAdmissionExplicitly(t *testing.T) {
	definition := testQueueDefinition(t, "workers.admission", String(1))
	catalog := MustCatalog(definition)
	handler := Handler[string](func(context.Context, string) error { return nil })
	first, err := NewAdmissionSnapshot(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewAdmissionSnapshot(2 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var zeroReader AdmissionReader
	tests := []struct {
		name    string
		options []WorkerOption
	}{
		{name: "zero reader", options: []WorkerOption{Concurrency(1), WithAdmission(zeroReader)}},
		{name: "fabricated reader", options: []WorkerOption{Concurrency(1), WithAdmission(AdmissionReader{initialized: true})}},
		{name: "duplicate", options: []WorkerOption{Concurrency(1), WithAdmission(first.Reader()), WithAdmission(second.Reader())}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewWorkerPlan(catalog, On(definition, handler, test.options...)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("worker admission options = %v", err)
			}
		})
	}

	options := []WorkerOption{Concurrency(2), WithAdmission(first.Reader())}
	consumer := On(definition, handler, options...)
	options[1] = WithAdmission(second.Reader())
	plan, err := NewWorkerPlan(catalog, consumer)
	if err != nil {
		t.Fatal(err)
	}
	description := plan.Describe()
	bindings := plan.workerBindings()
	if len(bindings) != 1 || bindings[0].admission.cell != first.Reader().cell || bindings[0].admission.Freshness() != time.Minute || bindings[0].admissionGroup != description.Bindings[0].AdmissionGroup || !description.Bindings[0].AdmissionGroup.valid() || !description.Bindings[0].DynamicAdmission {
		t.Fatalf("dynamic admission was not retained: %#v", description.Bindings[0])
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := first.Publisher().Update(1, HeldReason{}, now); err != nil {
		t.Fatal(err)
	}
	decision := bindings[0].admission.Evaluate(bindings[0].concurrency, now)
	if decision.Signal() != AdmissionReady || decision.Limit() != 1 {
		t.Fatalf("stored admission reader lost its snapshot: signal=%v limit=%d", decision.Signal(), decision.Limit())
	}
	bindings[0].admission = second.Reader()
	fresh := plan.workerBindings()
	if fresh[0].admission.cell != first.Reader().cell || fresh[0].admission.Freshness() != time.Minute {
		t.Fatal("worker binding copy mutated the plan")
	}
}

func TestAutomaticWorkerWithoutAdmissionRemainsStatic(t *testing.T) {
	automatic := Auto(Handler[string](func(context.Context, string) error { return nil }))
	MustWire(automatic, WireSpec[string]{Name: testJobName(t, "workers.static-admission"), Codec: String(1)})
	plan := MustWorkerPlan(MustCatalog(automatic), automatic)
	binding := plan.workerBindings()[0]
	if binding.admission != (AdmissionReader{}) || !binding.admissionGroup.IsZero() || !plan.Describe().Bindings[0].AdmissionGroup.IsZero() || plan.Describe().Bindings[0].DynamicAdmission {
		t.Fatal("automatic worker unexpectedly configured dynamic admission")
	}
	if binding.concurrency != Default.workerConcurrency {
		t.Fatalf("automatic static concurrency = %d", binding.concurrency)
	}
}

func TestWorkerPlanRequiresExactUniqueCatalogMembersAndBindings(t *testing.T) {
	left := testQueueDefinition(t, "workers.left", String(1))
	right := testQueueDefinition(t, "workers.right", String(1))
	catalog := MustCatalog(left, right)
	handler := Handler[string](func(context.Context, string) error { return nil })
	nonmember := testQueueDefinition(t, "workers.left", String(1))
	if _, err := NewWorkerPlan(catalog, On(nonmember, handler, Concurrency(1))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("same-name nonmember = %v", err)
	}
	if _, err := NewWorkerPlan(catalog,
		On(left, handler, Binding("workers.left.one"), Concurrency(1)),
		On(left, handler, Binding("workers.left.two"), Concurrency(1)),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate definition = %v", err)
	}
	if _, err := NewWorkerPlan(catalog,
		On(left, handler, Binding("workers.shared"), Concurrency(1)),
		On(right, handler, Binding("workers.shared"), Concurrency(1)),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate binding = %v", err)
	}
}

func TestWorkerPlanEnforcesOverflowSafeTotalConcurrency(t *testing.T) {
	definitions := make([]Declaration, MaxWorkerConcurrency/MaxBindingConcurrency+1)
	consumers := make([]Consumer, len(definitions))
	handler := Handler[string](func(context.Context, string) error { return nil })
	for index := range definitions {
		definition := testQueueDefinition(t, fmt.Sprintf("workers.total-%02d", index), String(1))
		definitions[index] = definition
		consumers[index] = On(definition, handler, Concurrency(MaxBindingConcurrency))
	}
	catalog := MustCatalog(definitions...)
	plan, err := NewWorkerPlan(catalog, consumers[:len(consumers)-1]...)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TotalConcurrency() != MaxWorkerConcurrency {
		t.Fatalf("maximum total = %d", plan.TotalConcurrency())
	}
	if _, err := NewWorkerPlan(catalog, consumers...); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("total overflow = %v", err)
	}
}

func TestWorkerPlanDescriptionIsDetachedDeterministicAndRedacted(t *testing.T) {
	alpha := testQueueDefinition(t, "workers.alpha", String(1))
	beta := testQueueDefinition(t, "workers.beta", String(1))
	handler := Handler[string](func(context.Context, string) error { return nil })
	catalog := MustCatalog(beta, alpha)
	left := MustWorkerPlan(catalog, On(beta, handler, Concurrency(2)), On(alpha, handler, Concurrency(1)))
	right := MustWorkerPlan(catalog, On(alpha, handler, Concurrency(1)), On(beta, handler, Concurrency(2)))
	if !reflect.DeepEqual(left.Describe(), right.Describe()) {
		t.Fatalf("worker plan order is unstable: %#v %#v", left.Describe(), right.Describe())
	}
	mutated := left.Describe()
	mutated.Bindings[0].Concurrency = MaxBindingConcurrency
	mutated.Bindings[0].DynamicAdmission = true
	mutated.Bindings = nil
	mutated.CatalogFingerprint = "mutated"
	fresh := left.Describe()
	if len(fresh.Bindings) != 2 || fresh.Bindings[0].Concurrency != 1 || fresh.Bindings[0].DynamicAdmission || fresh.TotalConcurrency != 3 || fresh.CatalogFingerprint != catalog.Fingerprint() || left.CatalogFingerprint() != catalog.Fingerprint() {
		t.Fatalf("worker description was mutable: %#v", fresh)
	}
	secrets := []string{"workers.alpha", catalog.Fingerprint()}
	values := []string{
		fmt.Sprint(left),
		fmt.Sprintf("%+v", left),
		fmt.Sprintf("%#v", left),
		left.LogValue().String(),
		fmt.Sprint(fresh),
		fmt.Sprintf("%+v", fresh),
		fmt.Sprintf("%#v", fresh),
		fresh.LogValue().String(),
		fmt.Sprint(fresh.Bindings[0]),
		fmt.Sprintf("%+v", fresh.Bindings[0]),
		fresh.Bindings[0].LogValue().String(),
	}
	for _, value := range values {
		for _, secret := range secrets {
			if strings.Contains(value, secret) {
				t.Fatalf("worker plan formatting leaked stable internals: %q", value)
			}
		}
	}
	if got := slog.AnyValue(left).Resolve().String(); strings.Contains(got, secrets[0]) || strings.Contains(got, secrets[1]) {
		t.Fatalf("structured log leaked stable internals: %q", got)
	}
}

func TestWorkerPlanRetainsExactWholeCatalogFingerprint(t *testing.T) {
	alpha := testQueueDefinition(t, "workers.fingerprint-alpha", String(1))
	beta := testQueueDefinition(t, "workers.fingerprint-beta", String(1))
	handler := Handler[string](func(context.Context, string) error { return nil })
	whole := MustCatalog(alpha, beta)
	partial := MustCatalog(alpha)
	plan := MustWorkerPlan(whole, On(alpha, handler, Concurrency(1)))
	if plan.CatalogFingerprint() != whole.Fingerprint() || plan.Describe().CatalogFingerprint != whole.Fingerprint() {
		t.Fatalf("worker plan fingerprint = %q, want %q", plan.CatalogFingerprint(), whole.Fingerprint())
	}
	if plan.CatalogFingerprint() == partial.Fingerprint() {
		t.Fatal("worker plan fingerprint was derived from consumer subset")
	}
}

func TestWorkerPlanRejectsEmptyInputsAndMustPanics(t *testing.T) {
	definition := testQueueDefinition(t, "workers.inputs", String(1))
	if _, err := NewWorkerPlan(Catalog{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero catalog = %v", err)
	}
	if _, err := NewWorkerPlan(MustCatalog(definition)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero workers = %v", err)
	}
	assertPanicError(t, ErrInvalid, func() {
		MustWorkerPlan(MustCatalog(definition), On(definition, Handler[string](func(context.Context, string) error { return nil })))
	})
}

func mustWorkerBinding(t *testing.T, raw string) BindingName {
	t.Helper()
	value, err := ParseBindingName(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
