package jobs

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAutomaticDeclarationRequiresHandlerAndNeverInfersWireName(t *testing.T) {
	var nilHandler Handler[string]
	assertPanicError(t, ErrInvalid, func() { Auto(nilHandler) })
	assertPanicError(t, ErrInvalid, func() {
		Auto(Handler[string](func(context.Context, string) error { return nil }), Default, Heavy)
	})
	automatic := Auto(Handler[string](func(context.Context, string) error { return nil }), Heavy)
	if !automatic.Name().IsZero() || automatic.Describe().Resolved || automatic.Describe().Name != (Name{}) || automatic.Describe().Policy.Profile != "Heavy" {
		t.Fatalf("Auto inferred a runtime wire identity: %#v", automatic.Describe())
	}
	if _, err := automatic.Encode("value"); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("unwired Auto encoded payload: %v", err)
	}
}

func TestWiredAutomaticUsesTypedContractAndHandler(t *testing.T) {
	var calls atomic.Int64
	automatic := Auto(Handler[string](func(_ context.Context, value string) error {
		if value != "payload" {
			t.Fatalf("unexpected handler payload %q", value)
		}
		calls.Add(1)
		return nil
	}), Interactive)
	wired, err := Wire(automatic, WireSpec[string]{Name: testJobName(t, "documents.automatic"), Codec: String(1)})
	if err != nil {
		t.Fatal(err)
	}
	if wired != automatic || !automatic.Describe().Resolved || !automatic.Describe().Automatic {
		t.Fatalf("automatic declaration did not resolve: %#v", automatic.Describe())
	}
	payload, err := automatic.Encode("payload")
	if err != nil {
		t.Fatal(err)
	}
	value, err := automatic.Decode(payload)
	if err != nil || value != "payload" {
		t.Fatalf("automatic codec failed: %q, %v", value, err)
	}
	if err := automatic.Handler()(context.Background(), value); err != nil || calls.Load() != 1 {
		t.Fatalf("typed handler failed: %v, calls=%d", err, calls.Load())
	}
	if resolved, ok := automatic.Definition(); !ok || resolved.Name() != automatic.Name() {
		t.Fatal("resolved definition is not stable")
	}
}

func TestWireIsAtomicUnderConcurrentConfiguration(t *testing.T) {
	automatic := Auto(Handler[string](func(context.Context, string) error { return nil }))
	spec := WireSpec[string]{Name: testJobName(t, "documents.concurrent"), Codec: String(1)}
	const contenders = 32
	var successes atomic.Int64
	var conflicts atomic.Int64
	var unexpected atomic.Int64
	var group sync.WaitGroup
	group.Add(contenders)
	for range contenders {
		go func() {
			defer group.Done()
			_, err := Wire(automatic, spec)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrConflict):
				conflicts.Add(1)
			default:
				unexpected.Add(1)
			}
		}()
	}
	group.Wait()
	if successes.Load() != 1 || conflicts.Load() != contenders-1 || unexpected.Load() != 0 {
		t.Fatalf("wire outcomes: success=%d conflict=%d unexpected=%d", successes.Load(), conflicts.Load(), unexpected.Load())
	}
}

func TestOnBindsTypedHandlerWithoutMutatingDefinition(t *testing.T) {
	definition := testQueueDefinition(t, "documents.explicit-consumer", String(1))
	before := definition.Describe()
	var handled atomic.Int64
	consumer := On(definition, Handler[string](func(_ context.Context, value string) error {
		if value != "payload" {
			t.Fatalf("payload = %q", value)
		}
		handled.Add(1)
		return nil
	}), Concurrency(1))
	if consumer.Declaration() != declarationOf(definition) || !reflect.DeepEqual(definition.Describe(), before) {
		t.Fatal("On changed or replaced its definition")
	}
	binding := consumer.consumerBinding()
	payload, err := definition.Encode("payload")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := binding.decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.handle(context.Background(), decoded); err != nil {
		t.Fatal(err)
	}
	if handled.Load() != 1 {
		t.Fatal("bound handler was not invoked")
	}
}

func TestAutoAndOnUseTheSameConsumerContract(t *testing.T) {
	var automaticCalls atomic.Int64
	automatic := Auto(Handler[string](func(context.Context, string) error {
		automaticCalls.Add(1)
		return nil
	}))
	MustWire(automatic, WireSpec[string]{Name: testJobName(t, "documents.auto-consumer"), Codec: String(1)})
	catalog := MustCatalog(automatic)
	if err := validateConsumers(catalog, automatic); err != nil {
		t.Fatal(err)
	}
	var explicitCalls atomic.Int64
	explicit := On(automatic, Handler[string](func(context.Context, string) error {
		explicitCalls.Add(1)
		return nil
	}))
	if err := validateConsumers(catalog, explicit); err != nil {
		t.Fatal(err)
	}
	if err := automatic.Handler()(context.Background(), "payload"); err != nil {
		t.Fatal(err)
	}
	binding := explicit.consumerBinding()
	payload, err := automatic.Encode("payload")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := binding.decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.handle(context.Background(), decoded); err != nil {
		t.Fatal(err)
	}
	if automaticCalls.Load() != 1 || explicitCalls.Load() != 1 {
		t.Fatal("On mutated Auto's existing handler")
	}
}

func TestConsumerValidationRejectsNilDuplicateAndNonmemberBindings(t *testing.T) {
	definition := testQueueDefinition(t, "documents.member", String(1))
	catalog := MustCatalog(definition)
	handler := Handler[string](func(context.Context, string) error { return nil })
	var nilConsumer Consumer
	if err := validateConsumers(catalog, nilConsumer); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil consumer = %v", err)
	}
	if err := validateConsumers(catalog, On(definition, Handler[string](nil), Concurrency(1))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil handler = %v", err)
	}
	var nilDefinition *Definition[string]
	if err := validateConsumers(catalog, On(nilDefinition, handler, Concurrency(1))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil definition = %v", err)
	}
	if err := validateConsumers(catalog, On(definition, handler, Concurrency(1)), On(definition, handler, Concurrency(1))); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate consumer = %v", err)
	}
	nonmember := testQueueDefinition(t, "documents.member", String(1))
	if err := validateConsumers(catalog, On(nonmember, handler, Concurrency(1))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("same-name nonmember = %v", err)
	}
}

func TestOnCanPrecedeAutomaticWiring(t *testing.T) {
	automatic := Declare[string]()
	consumer := On(automatic, Handler[string](func(context.Context, string) error { return nil }))
	if consumer.Declaration() != automatic {
		t.Fatal("On did not preserve the automatic declaration")
	}
	MustWire(automatic, WireSpec[string]{Name: testJobName(t, "documents.late-consumer"), Codec: String(1)})
	if err := validateConsumers(MustCatalog(automatic), consumer); err != nil {
		t.Fatal(err)
	}
}

func TestConsumerPreservesNilInterfacePayload(t *testing.T) {
	definition := testQueueDefinition(t, "documents.nil-interface", TrustedJSON[any](1))
	var called atomic.Bool
	consumer := On[any](definition, func(_ context.Context, value any) error {
		if value != nil {
			t.Fatalf("payload = %#v", value)
		}
		called.Store(true)
		return nil
	}, Concurrency(1))
	payload, err := definition.Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := consumer.consumerBinding()
	decoded, err := binding.decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.handle(context.Background(), decoded); err != nil {
		t.Fatal(err)
	}
	if !called.Load() {
		t.Fatal("nil interface payload was not handled")
	}
}

func assertPanicError(t *testing.T, want error, fn func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		err, ok := recovered.(error)
		if !ok || !errors.Is(err, want) {
			t.Fatalf("expected panic %v, got %#v", want, recovered)
		}
	}()
	fn()
}
