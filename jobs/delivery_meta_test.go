package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type recordingProgressReporter struct {
	calls atomic.Int32
	seen  atomic.Pointer[context.Context]
}

type recordingLeaseFence struct {
	lease LeaseRef
	err   error
}

func (fence *recordingLeaseFence) Fence(_ context.Context, lease LeaseRef) error {
	fence.lease = lease
	return fence.err
}

func (reporter *recordingProgressReporter) Guard(ctx context.Context, fence LeaseFence) error {
	if fence == nil {
		return ErrInvalid
	}
	return fence.Fence(ctx, LeaseRef{})
}

func (reporter *recordingProgressReporter) Pulse(ctx context.Context) error {
	reporter.calls.Add(1)
	reporter.seen.Store(&ctx)
	return nil
}

func TestDeliveryMetaCanonicalizesBoundedValueData(t *testing.T) {
	spec := deliveryMetaFixture(t)
	meta, err := NewDeliveryMeta(spec)
	if err != nil {
		t.Fatal(err)
	}
	if meta.InvocationID() != spec.Invocation || meta.Definition() != spec.Definition || meta.Binding() != spec.Binding || meta.Build() != spec.Build || meta.AttemptOrdinal() != spec.Attempt {
		t.Fatalf("delivery identity = %#v", meta)
	}
	times := []struct {
		got  time.Time
		want time.Time
	}{
		{meta.CreatedAt(), spec.CreatedAt.Round(0).UTC()},
		{meta.EligibleAt(), spec.EligibleAt.Round(0).UTC()},
		{meta.StartedAt(), spec.StartedAt.Round(0).UTC()},
		{meta.AttemptDeadline(), spec.AttemptDeadline.Round(0).UTC()},
		{meta.MaxElapsedAt(), spec.MaxElapsedAt.Round(0).UTC()},
		{meta.ProgressDeadline(), spec.ProgressDeadline.Round(0).UTC()},
	}
	for _, value := range times {
		if value.got != value.want || value.got.Location() != time.UTC {
			t.Fatalf("canonical time = (%v, want %v)", value.got, value.want)
		}
	}
	if meta.IsZero() || !meta.valid() || (DeliveryMeta{}).valid() || !(DeliveryMeta{}).IsZero() {
		t.Fatalf("delivery validity = (zero=%v, valid=%v)", meta.IsZero(), meta.valid())
	}
}

func TestDeliveryMetaRejectsUnboundedAndIncoherentValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DeliveryMetaSpec)
		want   error
	}{
		{name: "invocation", mutate: func(spec *DeliveryMetaSpec) { spec.Invocation = InvocationID{} }, want: ErrInvalid},
		{name: "definition", mutate: func(spec *DeliveryMetaSpec) { spec.Definition = Name{} }, want: ErrInvalid},
		{name: "binding", mutate: func(spec *DeliveryMetaSpec) { spec.Binding = BindingName{} }, want: ErrInvalid},
		{name: "build", mutate: func(spec *DeliveryMetaSpec) { spec.Build = BuildID{} }, want: ErrInvalid},
		{name: "attempt zero", mutate: func(spec *DeliveryMetaSpec) { spec.Attempt = AttemptOrdinal{} }, want: ErrInvalid},
		{name: "attempt large", mutate: func(spec *DeliveryMetaSpec) { spec.Attempt = AttemptOrdinal{value: uint16(MaxAttemptOrdinal + 1)} }, want: ErrInvalid},
		{name: "creation zero", mutate: func(spec *DeliveryMetaSpec) { spec.CreatedAt = time.Time{} }, want: ErrInvalid},
		{name: "eligible before creation", mutate: func(spec *DeliveryMetaSpec) { spec.EligibleAt = spec.CreatedAt.Add(-time.Nanosecond) }, want: ErrInvalid},
		{name: "eligible delay", mutate: func(spec *DeliveryMetaSpec) { spec.EligibleAt = spec.CreatedAt.Add(MaxRetention + time.Nanosecond) }, want: ErrInvalid},
		{name: "start before eligible", mutate: func(spec *DeliveryMetaSpec) { spec.StartedAt = spec.EligibleAt.Add(-time.Nanosecond) }, want: ErrInvalid},
		{name: "deadline at start", mutate: func(spec *DeliveryMetaSpec) { spec.AttemptDeadline = spec.StartedAt }, want: ErrInvalid},
		{name: "attempt duration", mutate: func(spec *DeliveryMetaSpec) {
			spec.AttemptDeadline = spec.StartedAt.Add(MaximumAttemptTimeout + time.Nanosecond)
			spec.MaxElapsedAt = spec.AttemptDeadline.Add(time.Second)
		}, want: ErrInvalid},
		{name: "elapsed at eligible", mutate: func(spec *DeliveryMetaSpec) { spec.MaxElapsedAt = spec.EligibleAt }, want: ErrInvalid},
		{name: "elapsed duration", mutate: func(spec *DeliveryMetaSpec) {
			spec.MaxElapsedAt = spec.EligibleAt.Add(MaximumMaxElapsed + time.Nanosecond)
		}, want: ErrInvalid},
		{name: "deadline after elapsed", mutate: func(spec *DeliveryMetaSpec) { spec.MaxElapsedAt = spec.AttemptDeadline.Add(-time.Nanosecond) }, want: ErrInvalid},
		{name: "progress at start", mutate: func(spec *DeliveryMetaSpec) { spec.ProgressDeadline = spec.StartedAt }, want: ErrInvalid},
		{name: "progress after deadline", mutate: func(spec *DeliveryMetaSpec) { spec.ProgressDeadline = spec.AttemptDeadline.Add(time.Nanosecond) }, want: ErrInvalid},
		{name: "year out of range", mutate: func(spec *DeliveryMetaSpec) { spec.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }, want: ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := deliveryMetaFixture(t)
			test.mutate(&spec)
			if _, err := NewDeliveryMeta(spec); !errors.Is(err, test.want) {
				t.Fatalf("NewDeliveryMeta = %v", err)
			}
		})
	}
	spec := deliveryMetaFixture(t)
	spec.ProgressDeadline = time.Time{}
	meta, err := NewDeliveryMeta(spec)
	if err != nil || !meta.ProgressDeadline().IsZero() {
		t.Fatalf("optional progress deadline = (%v, %v)", meta.ProgressDeadline(), err)
	}
}

func TestDeliveryMetaSurfaceAndFormattingExcludeDeliveryControls(t *testing.T) {
	allowedSpec := map[string]bool{
		"Invocation": true, "Definition": true, "Binding": true, "Build": true, "Attempt": true,
		"CreatedAt": true, "EligibleAt": true, "StartedAt": true, "AttemptDeadline": true, "MaxElapsedAt": true, "ProgressDeadline": true,
	}
	allowedValue := map[string]bool{
		"invocation": true, "definition": true, "binding": true, "build": true, "attempt": true,
		"createdAt": true, "eligibleAt": true, "startedAt": true, "attemptDeadline": true, "maxElapsedAt": true, "progressDeadline": true,
	}
	assertExactDeliveryMetaFields(t, reflect.TypeFor[DeliveryMetaSpec](), allowedSpec)
	assertExactDeliveryMetaFields(t, reflect.TypeFor[DeliveryMeta](), allowedValue)
	reporterType := reflect.TypeOf((*ProgressReporter)(nil)).Elem()
	if reporterType.NumMethod() != 1 || reporterType.Method(0).Name != "Pulse" {
		t.Fatalf("progress reporter methods = %v", reporterType)
	}
	controllerType := reflect.TypeOf((*AttemptController)(nil)).Elem()
	if controllerType.NumMethod() != 2 {
		t.Fatalf("attempt controller methods = %v", controllerType)
	}
	spec := deliveryMetaFixture(t)
	meta, err := NewDeliveryMeta(spec)
	if err != nil {
		t.Fatal(err)
	}
	secrets := []string{spec.Invocation.String(), spec.Definition.String(), spec.Binding.String(), spec.Build.String(), spec.StartedAt.Format(time.RFC3339Nano)}
	values := []string{
		fmt.Sprint(spec), fmt.Sprintf("%+v", spec), fmt.Sprintf("%#v", spec), spec.LogValue().String(), slog.AnyValue(spec).Resolve().String(),
		fmt.Sprint(meta), fmt.Sprintf("%+v", meta), fmt.Sprintf("%#v", meta), meta.LogValue().String(), slog.AnyValue(meta).Resolve().String(),
	}
	for _, value := range values {
		for _, secret := range secrets {
			if strings.Contains(value, secret) {
				t.Fatalf("delivery metadata formatting leaked %q in %q", secret, value)
			}
		}
	}
	for _, value := range []any{spec, meta} {
		encoded, err := json.Marshal(value)
		if !errors.Is(err, ErrUnsupported) || len(encoded) != 0 {
			t.Fatalf("delivery metadata JSON = (%q, %v)", encoded, err)
		}
	}
}

func TestAttemptControllerGuardsTheCurrentRotatingLease(t *testing.T) {
	invocation, err := NewInvocationID()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := NewLeaseRef(queueTestBackendID(1), invocation, []byte("current-token"))
	if err != nil {
		t.Fatal(err)
	}
	delivery := &activeWorkerDelivery{lease: lease}
	controller := workerAttemptController{delivery: delivery}
	fence := &recordingLeaseFence{}
	if err := controller.Guard(context.Background(), fence); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fence.lease, lease) {
		t.Fatal("guard did not use the current lease")
	}
	delivery.closed = true
	if err := controller.Guard(context.Background(), fence); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("closed delivery guard = %v", err)
	}
}

func TestOnAdapterRetainsTypedModeWithoutLifecycleEffects(t *testing.T) {
	standardDefinition := testQueueDefinition(t, "workers.adapter-standard", String(1))
	adapterDefinition := testQueueDefinition(t, "workers.adapter-advanced", String(1))
	var standardCalls atomic.Int32
	standard := On(standardDefinition, Handler[string](func(context.Context, string) error {
		standardCalls.Add(1)
		return nil
	}), Concurrency(1))
	var adapterCalls atomic.Int32
	var classifierCalls atomic.Int32
	reporter := &recordingProgressReporter{}
	metaSpec := deliveryMetaFixture(t)
	metaSpec.Definition = adapterDefinition.Name()
	metaSpec.Binding = mustWorkerBinding(t, adapterDefinition.Name().String())
	meta, err := NewDeliveryMeta(metaSpec)
	if err != nil {
		t.Fatal(err)
	}
	adapter := OnAdapter(adapterDefinition, AdapterHandler[string](func(ctx context.Context, payload string, provided DeliveryMeta, progress AttemptController) error {
		adapterCalls.Add(1)
		if payload != "adapter" || provided != meta || progress != reporter {
			t.Fatalf("adapter inputs = (%q, %#v, %T)", payload, provided, progress)
		}
		return progress.Pulse(ctx)
	}), Concurrency(2), Classify(func(HandlerFailure) Disposition {
		classifierCalls.Add(1)
		return SuccessDisposition()
	}))
	catalog := MustCatalog(standardDefinition, adapterDefinition)
	beforeFingerprint := catalog.Fingerprint()
	plan, err := NewWorkerPlan(catalog, standard, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if standardCalls.Load() != 0 || adapterCalls.Load() != 0 || classifierCalls.Load() != 0 || reporter.calls.Load() != 0 || catalog.Fingerprint() != beforeFingerprint {
		t.Fatalf("planning effects = (standard=%d adapter=%d classifier=%d progress=%d)", standardCalls.Load(), adapterCalls.Load(), classifierCalls.Load(), reporter.calls.Load())
	}
	var standardBinding, adapterBinding consumerBinding
	for _, binding := range plan.workerBindings() {
		if binding.mode == consumerHandlerAdapter {
			adapterBinding = binding
		} else {
			standardBinding = binding
		}
	}
	if standardBinding.handle == nil || standardBinding.handleAdapter != nil || standardBinding.classifier != nil || adapterBinding.handle != nil || adapterBinding.handleAdapter == nil || adapterBinding.classifier == nil {
		t.Fatal("worker plan mixed standard and adapter handler modes")
	}
	descriptions := plan.Describe().Bindings
	var adapterDescription WorkerBindingDescription
	for _, description := range descriptions {
		if description.Adapter {
			adapterDescription = description
		}
	}
	if adapterDescription.Definition != adapterDefinition.Name() || !adapterDescription.CustomClassifier || adapterDescription.Concurrency != 2 {
		t.Fatalf("adapter description = %#v", adapterDescription)
	}
	encoded, err := adapterDefinition.Encode("adapter")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := adapterBinding.decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), struct{}{}, "context-value")
	if result := adapterBinding.handleAdapter(ctx, decoded, meta, reporter); result != nil {
		t.Fatal(result)
	}
	if adapterCalls.Load() != 1 || reporter.calls.Load() != 1 || reporter.seen.Load() == nil || *reporter.seen.Load() != ctx || classifierCalls.Load() != 0 {
		t.Fatalf("adapter effects = (handler=%d progress=%d classifier=%d)", adapterCalls.Load(), reporter.calls.Load(), classifierCalls.Load())
	}
}

func TestOnAdapterRejectsInvalidInputsAndContainsPanics(t *testing.T) {
	definition := testQueueDefinition(t, "workers.adapter-invalid", String(1))
	catalog := MustCatalog(definition)
	var nilHandler AdapterHandler[string]
	if _, err := NewWorkerPlan(catalog, OnAdapter(definition, nilHandler, Concurrency(1))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil adapter handler = %v", err)
	}
	var calls atomic.Int32
	consumer := OnAdapter(definition, AdapterHandler[string](func(context.Context, string, DeliveryMeta, AttemptController) error {
		calls.Add(1)
		panic("adapter-panic-private")
	}), Concurrency(1))
	binding := consumer.consumerBinding()
	encoded, err := definition.Encode("payload")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := binding.decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	metaSpec := deliveryMetaFixture(t)
	metaSpec.Definition = definition.Name()
	metaSpec.Binding = mustWorkerBinding(t, definition.Name().String())
	meta, err := NewDeliveryMeta(metaSpec)
	if err != nil {
		t.Fatal(err)
	}
	var nilReporter *recordingProgressReporter
	if result := binding.handleAdapter(context.Background(), decoded, meta, nilReporter); !errors.Is(result, ErrInvalid) || calls.Load() != 0 {
		t.Fatalf("nil progress reporter = (%v, calls=%d)", result, calls.Load())
	}
	if result := binding.handleAdapter(context.Background(), decoded, DeliveryMeta{}, &recordingProgressReporter{}); !errors.Is(result, ErrInvalid) || calls.Load() != 0 {
		t.Fatalf("invalid metadata = (%v, calls=%d)", result, calls.Load())
	}
	wrongDefinition := meta
	wrongDefinition.definition = testJobName(t, "workers.adapter-wrong")
	if result := binding.handleAdapter(context.Background(), decoded, wrongDefinition, &recordingProgressReporter{}); !errors.Is(result, ErrInvalid) || calls.Load() != 0 {
		t.Fatalf("wrong metadata definition = (%v, calls=%d)", result, calls.Load())
	}
	wrongBinding := meta
	wrongBinding.binding = mustWorkerBinding(t, "workers.adapter-wrong")
	if result := binding.handleAdapter(context.Background(), decoded, wrongBinding, &recordingProgressReporter{}); !errors.Is(result, ErrInvalid) || calls.Load() != 0 {
		t.Fatalf("wrong metadata binding = (%v, calls=%d)", result, calls.Load())
	}
	result := binding.handleAdapter(context.Background(), decoded, meta, &recordingProgressReporter{})
	failure, ok := result.(HandlerFailure)
	if !ok || !failure.Panicked() || failure.Unwrap() != nil || calls.Load() != 1 {
		t.Fatalf("adapter panic = (%T, panicked=%v, calls=%d)", result, failure.Panicked(), calls.Load())
	}
	assertHandlerFailureRedacted(t, failure, "adapter-panic-private")
}

func deliveryMetaFixture(t *testing.T) DeliveryMetaSpec {
	t.Helper()
	invocation, err := NewInvocationID()
	if err != nil {
		t.Fatal(err)
	}
	definition := testJobName(t, "delivery.metadata")
	binding, err := ParseBindingName("delivery.metadata")
	if err != nil {
		t.Fatal(err)
	}
	build, err := ParseBuildID("build.delivery@1")
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := NewAttemptOrdinal(2)
	if err != nil {
		t.Fatal(err)
	}
	location := time.FixedZone("fixture", 6*60*60)
	createdAt := time.Date(2026, 9, 1, 12, 0, 0, 123, location)
	eligibleAt := createdAt.Add(time.Second)
	startedAt := eligibleAt.Add(time.Second)
	return DeliveryMetaSpec{
		Invocation:       invocation,
		Definition:       definition,
		Binding:          binding,
		Build:            build,
		Attempt:          attempt,
		CreatedAt:        createdAt,
		EligibleAt:       eligibleAt,
		StartedAt:        startedAt,
		AttemptDeadline:  startedAt.Add(time.Minute),
		MaxElapsedAt:     eligibleAt.Add(time.Hour),
		ProgressDeadline: startedAt.Add(30 * time.Second),
	}
}

func assertExactDeliveryMetaFields(t *testing.T, typ reflect.Type, allowed map[string]bool) {
	t.Helper()
	if typ.NumField() != len(allowed) {
		t.Fatalf("%s fields = %d, want %d", typ, typ.NumField(), len(allowed))
	}
	for index := range typ.NumField() {
		if !allowed[typ.Field(index).Name] {
			t.Fatalf("%s exposes forbidden field %q", typ, typ.Field(index).Name)
		}
	}
}
