package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

type workerDeliveryFixture struct {
	namespace  Namespace
	catalog    Catalog
	definition *Definition[string]
	invocation Invocation
	payload    EncodedPayload
	record     DeliveryRecord
	binding    consumerBinding
	build      BuildID
	target     ClaimTarget
	lease      LeaseRef
	delivery   ClaimedDelivery
}

func newWorkerDeliveryFixture(t *testing.T, mode PlacementMode) workerDeliveryFixture {
	t.Helper()
	catalog, definition, invocation, payload, record := deliveryRecordFixture(t, mode)
	binding := On(definition, Handler[string](func(context.Context, string) error { return nil }), Binding("worker.primary"), Concurrency(1)).consumerBinding()
	build := testBuildID(t)
	revision, err := NewPayloadRevision(payload.Codec(), payload.Version())
	if err != nil {
		t.Fatal(err)
	}
	target, err := NewClaimTarget(ClaimTargetSpec{
		Definition:         definition.Name(),
		Binding:            binding.binding,
		Build:              build,
		SupportedRevisions: []PayloadRevision{revision},
		Available:          1,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease := deliveryTestLease(t, invocation.ID(), []byte("worker delivery lease secret"))
	delivery, err := NewClaimedDelivery(target, lease, record)
	if err != nil {
		t.Fatal(err)
	}
	return workerDeliveryFixture{
		namespace:  invocation.Namespace(),
		catalog:    catalog,
		definition: definition,
		invocation: invocation,
		payload:    payload,
		record:     record,
		binding:    binding,
		build:      build,
		target:     target,
		lease:      lease,
		delivery:   delivery,
	}
}

func replaceWorkerDeliveryDefinition(t *testing.T, fixture workerDeliveryFixture, definition *Definition[string]) workerDeliveryFixture {
	t.Helper()
	binding := On(definition, Handler[string](func(context.Context, string) error { return nil }), Binding("worker.primary"), Concurrency(1)).consumerBinding()
	revision, err := NewPayloadRevision(fixture.payload.Codec(), fixture.payload.Version())
	if err != nil {
		t.Fatal(err)
	}
	target, err := NewClaimTarget(ClaimTargetSpec{Definition: definition.Name(), Binding: binding.binding, Build: fixture.build, SupportedRevisions: []PayloadRevision{revision}, Available: 1})
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := NewClaimedDelivery(target, fixture.lease, fixture.record)
	if err != nil {
		t.Fatal(err)
	}
	fixture.catalog = MustCatalog(definition)
	fixture.definition = definition
	fixture.binding = binding
	fixture.target = target
	fixture.delivery = delivery
	return fixture
}

func workerDeliveryIdentityRestorer(t *testing.T) TrustedIdentityRestorer {
	t.Helper()
	return TrustedIdentityRestorerFunc(func(ctx context.Context, _ IdentityRestoreRequest) (RestoredIdentity, error) {
		return NewRestoredIdentity(ctx, ProducerPartition{}, ProducerActor{})
	})
}

func prepareWorkerDelivery(t *testing.T, fixture workerDeliveryFixture, restorer TrustedIdentityRestorer) claimedDeliveryPreparation {
	t.Helper()
	prepared, err := prepareClaimedDelivery(context.Background(), fixture.namespace, fixture.catalog, fixture.binding, fixture.build, restorer, fixture.delivery)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func assertWorkerDeliveryCommand(t *testing.T, prepared claimedDeliveryPreparation, kind DeliveryCommandKind) DeliveryCommand {
	t.Helper()
	if !prepared.commanded() || prepared.ready() || prepared.command.Kind() != kind {
		t.Fatalf("preparation = %+v, command=%s", prepared, prepared.command.Kind())
	}
	return prepared.command
}

func TestPrepareClaimedDeliveryRejectsStructuralCorruptionBeforeCompatibility(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	record := cloneDeliveryRecord(fixture.record)
	record.Genesis.Definition = testJobName(t, "tests.unavailable")
	record.WireDigest = WireDigest{}
	delivery, err := NewClaimedDelivery(fixture.target, fixture.lease, record)
	if err != nil {
		t.Fatal(err)
	}
	decodeCalls := 0
	fixture.binding.decode = func(EncodedPayload) (any, error) {
		decodeCalls++
		return nil, nil
	}
	fixture.binding.decodeOwned = func(EncodedPayload) (any, error) {
		decodeCalls++
		return nil, nil
	}
	restoreCalls := 0
	restorer := TrustedIdentityRestorerFunc(func(context.Context, IdentityRestoreRequest) (RestoredIdentity, error) {
		restoreCalls++
		return RestoredIdentity{}, errors.New("must not restore")
	})
	fixture.delivery = delivery
	command := assertWorkerDeliveryCommand(t, prepareWorkerDelivery(t, fixture, restorer), DeliveryCommandRejectCorrupt)
	if command.Lease().InvocationID() != fixture.invocation.ID() || decodeCalls != 0 || restoreCalls != 0 {
		t.Fatalf("reject = (%v, decode=%d, restore=%d)", command, decodeCalls, restoreCalls)
	}
}

func TestPrepareClaimedDeliveryReleasesUnsupportedRevisionWithoutAttempt(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	unsupported, err := NewEncodedPayload(fixture.payload.Codec(), fixture.payload.Version()+1, fixture.payload.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	record := cloneDeliveryRecord(fixture.record)
	record.Payload.Version = unsupported.Version()
	record.WireDigest = digestWirePayload(unsupported)
	delivery, err := NewClaimedDelivery(fixture.target, fixture.lease, record)
	if err != nil {
		t.Fatal(err)
	}
	fixture.delivery = delivery
	command := assertWorkerDeliveryCommand(t, prepareWorkerDelivery(t, fixture, workerDeliveryIdentityRestorer(t)), DeliveryCommandReleaseUnchanged)
	if command.Reason() != ReasonCompatibility || command.Delay() != DefaultRetryDelay || command.Binding() != fixture.binding.binding || command.Build() != fixture.build {
		t.Fatalf("release command = %v", command)
	}
	application, err := ApplyDeliveryCommand(fixture.invocation, command, fixture.invocation.EligibleAt())
	release, ok := application.Release()
	if err != nil || application.Changed() || !ok || release.Reason() != ReasonCompatibility || application.Invocation().AttemptOrdinal().Value() != 0 || len(application.Invocation().Attempts()) != 0 || application.Invocation().DeliveryDeferrals().Value() != 0 || application.Invocation().Outcome() != fixture.invocation.Outcome() {
		t.Fatalf("release application = (%v, %v)", application, err)
	}
}

func TestPrepareClaimedDeliveryHonorsTargetRevisionSnapshot(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	revision, err := NewPayloadRevision(fixture.payload.Codec(), fixture.payload.Version()+1)
	if err != nil {
		t.Fatal(err)
	}
	target, err := NewClaimTarget(ClaimTargetSpec{
		Definition:         fixture.definition.Name(),
		Binding:            fixture.binding.binding,
		Build:              fixture.build,
		SupportedRevisions: []PayloadRevision{revision},
		Available:          1,
	})
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := NewClaimedDelivery(target, fixture.lease, fixture.record)
	if err != nil {
		t.Fatal(err)
	}
	decodeCalls := 0
	fixture.binding.decodeOwned = func(EncodedPayload) (any, error) {
		decodeCalls++
		return decodedConsumerValue[string]{value: "unexpected"}, nil
	}
	restoreCalls := 0
	restorer := TrustedIdentityRestorerFunc(func(context.Context, IdentityRestoreRequest) (RestoredIdentity, error) {
		restoreCalls++
		return RestoredIdentity{}, errors.New("must not restore")
	})
	fixture.target = target
	fixture.delivery = delivery
	command := assertWorkerDeliveryCommand(t, prepareWorkerDelivery(t, fixture, restorer), DeliveryCommandReleaseUnchanged)
	if command.Reason() != ReasonCompatibility || command.Binding() != fixture.binding.binding || command.Build() != fixture.build || decodeCalls != 0 || restoreCalls != 0 {
		t.Fatalf("target release = (%v, decode=%d, restore=%d)", command, decodeCalls, restoreCalls)
	}
}

func TestPrepareClaimedDeliveryRejectsClaimIdentityMismatches(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*testing.T, *workerDeliveryFixture)
		invalid bool
	}{
		{name: "runtime namespace", mutate: func(t *testing.T, fixture *workerDeliveryFixture) {
			fixture.namespace, _ = NamespaceOf("tests", "other")
		}},
		{name: "lease invocation", mutate: func(t *testing.T, fixture *workerDeliveryFixture) {
			fixture.lease = deliveryTestLease(t, queueTestInvocationID(t, 74), []byte("mismatched invocation"))
			fixture.delivery, _ = NewClaimedDelivery(fixture.target, fixture.lease, fixture.record)
		}},
		{name: "target definition", invalid: true, mutate: func(t *testing.T, fixture *workerDeliveryFixture) {
			fixture.target.definition = testJobName(t, "tests.other-definition")
			fixture.delivery, _ = NewClaimedDelivery(fixture.target, fixture.lease, fixture.record)
		}},
		{name: "target binding", invalid: true, mutate: func(t *testing.T, fixture *workerDeliveryFixture) {
			fixture.target.binding, _ = ParseBindingName("worker.other")
			fixture.delivery, _ = NewClaimedDelivery(fixture.target, fixture.lease, fixture.record)
		}},
		{name: "runtime build", invalid: true, mutate: func(t *testing.T, fixture *workerDeliveryFixture) {
			fixture.build, _ = ParseBuildID("git:OTHER")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkerDeliveryFixture(t, PlacementRegular)
			test.mutate(t, &fixture)
			prepared, err := prepareClaimedDelivery(context.Background(), fixture.namespace, fixture.catalog, fixture.binding, fixture.build, workerDeliveryIdentityRestorer(t), fixture.delivery)
			if test.invalid {
				if !errors.Is(err, ErrInvalid) || prepared.ready() || prepared.commanded() || fixture.delivery.Record().Genesis.ID != fixture.invocation.ID() {
					t.Fatalf("local target mismatch = (%v, %v)", prepared, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertWorkerDeliveryCommand(t, prepared, DeliveryCommandRejectCorrupt)
		})
	}
}

func TestPrepareClaimedDeliveryFailsOnDriverClaimingNonQueuedInvocation(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	running, _, err := fixture.invocation.BeginAttempt(BeginAttemptSpec{Binding: fixture.binding.binding, Build: fixture.build, StartedAt: fixture.invocation.EligibleAt()})
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewDeliveryRecord(running, fixture.payload, digestWirePayload(fixture.payload), fixture.record.PayloadDigest)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := NewClaimedDelivery(fixture.target, fixture.lease, record)
	if err != nil {
		t.Fatal(err)
	}
	fixture.delivery = delivery
	prepared, err := prepareClaimedDelivery(context.Background(), fixture.namespace, fixture.catalog, fixture.binding, fixture.build, workerDeliveryIdentityRestorer(t), fixture.delivery)
	if !errors.Is(err, ErrDriver) || !errors.Is(err, ErrDriverContract) || prepared.ready() || prepared.commanded() {
		t.Fatalf("non-queued claim = (%v, %v)", prepared, err)
	}
}

func TestPrepareClaimedDeliveryClassifiesDecodeFailures(t *testing.T) {
	tests := []struct {
		name        string
		decode      func(EncodedPayload) (any, error)
		commandKind DeliveryCommandKind
		want        error
	}{
		{name: "corrupt", decode: func(EncodedPayload) (any, error) { return nil, ErrCorrupt }, commandKind: DeliveryCommandFinishDelivery},
		{name: "too large", decode: func(EncodedPayload) (any, error) { return nil, ErrTooLarge }, commandKind: DeliveryCommandReleaseUnchanged},
		{name: "unsupported", decode: func(EncodedPayload) (any, error) { return nil, ErrUnsupported }, commandKind: DeliveryCommandReleaseUnchanged},
		{name: "invalid", decode: func(EncodedPayload) (any, error) { return nil, ErrInvalid }, want: ErrInvalid},
		{name: "private error", decode: func(EncodedPayload) (any, error) { return nil, errors.New("private decode detail") }, want: ErrInvalid},
		{name: "panic", decode: func(EncodedPayload) (any, error) { panic("private decode panic") }, want: ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkerDeliveryFixture(t, PlacementRegular)
			fixture.binding.decodeOwned = test.decode
			restoreCalls := 0
			restorer := TrustedIdentityRestorerFunc(func(context.Context, IdentityRestoreRequest) (RestoredIdentity, error) {
				restoreCalls++
				return RestoredIdentity{}, errors.New("must not restore")
			})
			prepared, err := prepareClaimedDelivery(context.Background(), fixture.namespace, fixture.catalog, fixture.binding, fixture.build, restorer, fixture.delivery)
			if test.want != nil {
				if !errors.Is(err, test.want) || prepared.ready() || prepared.commanded() || restoreCalls != 0 || strings.Contains(fmt.Sprint(err), "private") {
					t.Fatalf("decode failure = (%v, %v, restore=%d)", prepared, err, restoreCalls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			command := assertWorkerDeliveryCommand(t, prepared, test.commandKind)
			if restoreCalls != 0 {
				t.Fatalf("identity restored after decode failure: %d", restoreCalls)
			}
			if test.commandKind == DeliveryCommandReleaseUnchanged {
				if command.Reason() != ReasonCompatibility || command.Delay() != DefaultRetryDelay || command.Binding() != fixture.binding.binding || command.Build() != fixture.build {
					t.Fatalf("compatibility release = %v", command)
				}
				return
			}
			if command.State() != InvocationQuarantined || command.Reason() != ReasonPayload || !command.Failure().IsZero() {
				t.Fatalf("quarantine = %v", command)
			}
			application, err := ApplyDeliveryCommand(fixture.invocation, command, fixture.invocation.EligibleAt())
			if err != nil || application.Invocation().State() != InvocationQuarantined || application.Invocation().AttemptOrdinal().Value() != 0 || len(application.Invocation().Attempts()) != 0 {
				t.Fatalf("quarantine application = (%v, %v)", application, err)
			}
		})
	}
}

func TestPrepareClaimedDeliveryPreservesTypedRuntimeFailureProvenance(t *testing.T) {
	const secret = "private-worker-codec-detail"
	tests := []struct {
		name        string
		upcast      bool
		panic       bool
		callbackErr error
		commandKind DeliveryCommandKind
		want        error
	}{
		{name: "codec panic", panic: true, want: ErrInvalid},
		{name: "codec unknown", callbackErr: errors.New(secret), want: ErrInvalid},
		{name: "codec invalid", callbackErr: fmt.Errorf("%w: %s", ErrInvalid, secret), want: ErrInvalid},
		{name: "codec corrupt", callbackErr: fmt.Errorf("%w: %s", ErrCorrupt, secret), commandKind: DeliveryCommandFinishDelivery},
		{name: "codec too large", callbackErr: fmt.Errorf("%w: %s", ErrTooLarge, secret), commandKind: DeliveryCommandReleaseUnchanged},
		{name: "codec unsupported", callbackErr: fmt.Errorf("%w: %s", ErrUnsupported, secret), commandKind: DeliveryCommandReleaseUnchanged},
		{name: "upcaster panic", upcast: true, panic: true, want: ErrInvalid},
		{name: "upcaster unknown", upcast: true, callbackErr: errors.New(secret), want: ErrInvalid},
		{name: "upcaster invalid", upcast: true, callbackErr: fmt.Errorf("%w: %s", ErrInvalid, secret), want: ErrInvalid},
		{name: "upcaster corrupt", upcast: true, callbackErr: fmt.Errorf("%w: %s", ErrCorrupt, secret), commandKind: DeliveryCommandFinishDelivery},
		{name: "upcaster too large", upcast: true, callbackErr: fmt.Errorf("%w: %s", ErrTooLarge, secret), commandKind: DeliveryCommandReleaseUnchanged},
		{name: "upcaster unsupported", upcast: true, callbackErr: fmt.Errorf("%w: %s", ErrUnsupported, secret), commandKind: DeliveryCommandReleaseUnchanged},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkerDeliveryFixture(t, PlacementRegular)
			if test.upcast {
				transform := func(value string) (string, error) {
					if test.panic {
						panic(secret)
					}
					return value, test.callbackErr
				}
				definition := MustDefine(DefinitionSpec[string]{
					Name:      fixture.definition.Name(),
					Codec:     String(2),
					Upcasters: []Upcaster{Upcast(String(1), String(2), transform)},
					Policy:    fixture.definition.Policy(),
					Partition: fixture.definition.Partition(),
				})
				fixture = replaceWorkerDeliveryDefinition(t, fixture, definition)
			} else {
				panicAt := ""
				if test.panic {
					panicAt = "decode"
				}
				fixture.definition.codec = secretStringCodec{id: fixture.payload.Codec(), version: fixture.payload.Version(), secret: secret, decodeErr: test.callbackErr, panicAt: panicAt}
			}
			restoreCalls := 0
			restorer := TrustedIdentityRestorerFunc(func(context.Context, IdentityRestoreRequest) (RestoredIdentity, error) {
				restoreCalls++
				return RestoredIdentity{}, errors.New("must not restore")
			})
			prepared, err := prepareClaimedDelivery(context.Background(), fixture.namespace, fixture.catalog, fixture.binding, fixture.build, restorer, fixture.delivery)
			if test.want != nil {
				if !errors.Is(err, test.want) || prepared.ready() || prepared.commanded() || restoreCalls != 0 || strings.Contains(fmt.Sprint(err), secret) {
					t.Fatalf("typed runtime failure = (%v, %v, restore=%d)", prepared, err, restoreCalls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			command := assertWorkerDeliveryCommand(t, prepared, test.commandKind)
			if restoreCalls != 0 {
				t.Fatalf("identity restored after typed runtime failure: %d", restoreCalls)
			}
			if test.commandKind == DeliveryCommandFinishDelivery && (command.State() != InvocationQuarantined || command.Reason() != ReasonPayload) {
				t.Fatalf("typed corruption command = %v", command)
			}
			if test.commandKind == DeliveryCommandReleaseUnchanged && command.Reason() != ReasonCompatibility {
				t.Fatalf("typed compatibility command = %v", command)
			}
		})
	}
}

func TestPrepareClaimedDeliveryDefersIdentityFailureAndPanic(t *testing.T) {
	tests := []struct {
		name     string
		restorer TrustedIdentityRestorer
	}{
		{name: "error", restorer: TrustedIdentityRestorerFunc(func(context.Context, IdentityRestoreRequest) (RestoredIdentity, error) {
			return RestoredIdentity{}, errors.New("private identity outage")
		})},
		{name: "panic", restorer: TrustedIdentityRestorerFunc(func(context.Context, IdentityRestoreRequest) (RestoredIdentity, error) {
			panic("private identity panic")
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkerDeliveryFixture(t, PlacementRegular)
			command := assertWorkerDeliveryCommand(t, prepareWorkerDelivery(t, fixture, test.restorer), DeliveryCommandDeferDelivery)
			if command.Reason() != ReasonDependency || command.Delay() != DefaultRetryDelay || !command.Failure().IsZero() {
				t.Fatalf("dependency defer = %v", command)
			}
			application, err := ApplyDeliveryCommand(fixture.invocation, command, fixture.invocation.EligibleAt())
			if err != nil || application.Invocation().State() != InvocationQueued || application.Invocation().DeliveryDeferrals().Value() != 1 || application.Invocation().AttemptOrdinal().Value() != 0 || len(application.Invocation().Attempts()) != 0 || application.Invocation().Outcome().AvailableAt() != fixture.invocation.EligibleAt().Add(DefaultRetryDelay) {
				t.Fatalf("dependency application = (%v, %v)", application, err)
			}
		})
	}
}

type workerDeliveryContextKey string

func TestPrepareClaimedDeliveryReturnsVerifiedContextAndDecodedPayloadWithoutExecution(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementOnce)
	handlerCalls := 0
	classifierCalls := 0
	consumer := On(fixture.definition, Handler[string](func(context.Context, string) error {
		handlerCalls++
		return nil
	}), Binding("worker.primary"), Concurrency(1), Classify(func(HandlerFailure) Disposition {
		classifierCalls++
		return SuccessDisposition()
	}))
	fixture.binding = consumer.consumerBinding()
	base := context.WithValue(context.Background(), workerDeliveryContextKey("base"), "base-context-secret")
	restoreCalls := 0
	restorer := TrustedIdentityRestorerFunc(func(ctx context.Context, request IdentityRestoreRequest) (RestoredIdentity, error) {
		restoreCalls++
		if request.Namespace() != fixture.namespace || request.Partition() != fixture.invocation.Partition() || request.Definition() != fixture.definition.Name() {
			return RestoredIdentity{}, errors.New("identity request mismatch")
		}
		return NewRestoredIdentity(context.WithValue(ctx, workerDeliveryContextKey("identity"), "restored-context-secret"), ProducerPartition{}, ProducerActor{})
	})
	prepared, err := prepareClaimedDelivery(base, fixture.namespace, fixture.catalog, fixture.binding, fixture.build, restorer, fixture.delivery)
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := prepared.decoded.(decodedConsumerValue[string])
	if !prepared.ready() || prepared.commanded() || !ok || decoded.value != "secret payload" || prepared.invocation.ID() != fixture.invocation.ID() || prepared.invocation.State() != InvocationQueued || prepared.payloadDigest != fixture.record.PayloadDigest || prepared.payloadDigest.IsZero() || prepared.context.Value(workerDeliveryContextKey("base")) != "base-context-secret" || prepared.context.Value(workerDeliveryContextKey("identity")) != "restored-context-secret" || handlerCalls != 0 || classifierCalls != 0 || restoreCalls != 1 {
		t.Fatalf("successful preparation = (%v, decoded=%v, handler=%d, classifier=%d, restore=%d)", prepared, decoded, handlerCalls, classifierCalls, restoreCalls)
	}
}

func TestPrepareClaimedDeliveryPreservesTakenRecordAndRedactsPreparation(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementOnce)
	source := cloneDeliveryRecord(fixture.record)
	dataPointer := &source.Payload.Data[0]
	delivery, err := TakeClaimedDelivery(fixture.target, fixture.lease, &source)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source, DeliveryRecord{}) || &delivery.record.value.Payload.Data[0] != dataPointer {
		t.Fatal("claimed delivery did not take record ownership")
	}
	var decodedPointer *byte
	decodedLength := 0
	decodedCapacity := 0
	fixture.binding.decode = func(EncodedPayload) (any, error) {
		panic("public decoder must not be called")
	}
	fixture.binding.decodeOwned = func(payload EncodedPayload) (any, error) {
		wire := payload.encodedBytes()
		decodedPointer = &wire[0]
		decodedLength = len(wire)
		decodedCapacity = cap(wire)
		return decodedConsumerValue[string]{value: string(wire)}, nil
	}
	envelope := delivery.record
	fixture.delivery = delivery
	prepared := prepareWorkerDelivery(t, fixture, workerDeliveryIdentityRestorer(t))
	if !prepared.ready() || decodedPointer != dataPointer || decodedCapacity != decodedLength || delivery.record != envelope || !delivery.record.taken || !delivery.Record().Genesis.ID.IsZero() {
		t.Fatal("preparation did not consume the claimed record")
	}
	for _, rendered := range []string{
		fmt.Sprintf("%v", prepared),
		fmt.Sprintf("%+v", prepared),
		fmt.Sprintf("%#v", prepared),
		prepared.LogValue().String(),
		slog.AnyValue(prepared).String(),
	} {
		if strings.Contains(rendered, "secret payload") || strings.Contains(rendered, "lease secret") {
			t.Fatalf("preparation leaked secret: %q", rendered)
		}
	}
	if _, err := json.Marshal(prepared); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("preparation JSON = %v", err)
	}
	typeOfPreparation := reflect.TypeFor[claimedDeliveryPreparation]()
	for index := 0; index < typeOfPreparation.NumField(); index++ {
		fieldType := typeOfPreparation.Field(index).Type
		if fieldType == reflect.TypeFor[LeaseRef]() || fieldType == reflect.TypeFor[DeliveryRecord]() || fieldType == reflect.TypeFor[EncodedPayload]() {
			t.Fatalf("preparation exposes raw delivery field %s", typeOfPreparation.Field(index).Name)
		}
	}
}

func TestPrepareClaimedDeliveryFailsClosedForInvalidInputs(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	tests := []struct {
		name   string
		mutate func(*workerDeliveryFixture)
	}{
		{name: "binding error", mutate: func(fixture *workerDeliveryFixture) { fixture.binding.err = errors.New("invalid binding") }},
		{name: "binding decoder", mutate: func(fixture *workerDeliveryFixture) { fixture.binding.decode = nil }},
		{name: "binding owned decoder", mutate: func(fixture *workerDeliveryFixture) { fixture.binding.decodeOwned = nil }},
		{name: "binding declaration", mutate: func(fixture *workerDeliveryFixture) { fixture.binding.declaration = nil }},
		{name: "build", mutate: func(fixture *workerDeliveryFixture) { fixture.build = BuildID{} }},
		{name: "delivery", mutate: func(fixture *workerDeliveryFixture) { fixture.delivery = ClaimedDelivery{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copy := fixture
			test.mutate(&copy)
			prepared, err := prepareClaimedDelivery(context.Background(), copy.namespace, copy.catalog, copy.binding, copy.build, workerDeliveryIdentityRestorer(t), copy.delivery)
			if !errors.Is(err, ErrInvalid) || prepared.ready() || prepared.commanded() {
				t.Fatalf("invalid preparation = (%v, %v)", prepared, err)
			}
		})
	}
}

func TestPrepareClaimedDeliveryReturnsCancelledIdentityContext(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t, PlacementRegular)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prepared, err := prepareClaimedDelivery(ctx, fixture.namespace, fixture.catalog, fixture.binding, fixture.build, workerDeliveryIdentityRestorer(t), fixture.delivery)
	if !errors.Is(err, context.Canceled) || prepared.ready() || prepared.commanded() {
		t.Fatalf("cancelled preparation = (%v, %v)", prepared, err)
	}
}

func TestPrepareClaimedDeliveryCancellationPrecedesDecodeAndDisposition(t *testing.T) {
	t.Run("before preparation", func(t *testing.T) {
		fixture := newWorkerDeliveryFixture(t, PlacementRegular)
		decodeCalls := 0
		fixture.binding.decodeOwned = func(EncodedPayload) (any, error) {
			decodeCalls++
			return nil, ErrCorrupt
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		prepared, err := prepareClaimedDelivery(ctx, fixture.namespace, fixture.catalog, fixture.binding, fixture.build, workerDeliveryIdentityRestorer(t), fixture.delivery)
		if !errors.Is(err, context.Canceled) || prepared.ready() || prepared.commanded() || decodeCalls != 0 || fixture.delivery.Record().Genesis.ID != fixture.invocation.ID() {
			t.Fatalf("pre-cancelled preparation = (%v, %v, decode=%d)", prepared, err, decodeCalls)
		}
	})
	for _, test := range []struct {
		name    string
		decoded any
		err     error
	}{
		{name: "during corrupt decode", err: ErrCorrupt},
		{name: "during oversized decode", err: ErrTooLarge},
		{name: "during unsupported decode", err: ErrUnsupported},
		{name: "during successful decode", decoded: decodedConsumerValue[string]{value: "decoded"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkerDeliveryFixture(t, PlacementRegular)
			ctx, cancel := context.WithCancel(context.Background())
			fixture.binding.decodeOwned = func(EncodedPayload) (any, error) {
				cancel()
				return test.decoded, test.err
			}
			restoreCalls := 0
			restorer := TrustedIdentityRestorerFunc(func(context.Context, IdentityRestoreRequest) (RestoredIdentity, error) {
				restoreCalls++
				return RestoredIdentity{}, errors.New("must not restore")
			})
			prepared, err := prepareClaimedDelivery(ctx, fixture.namespace, fixture.catalog, fixture.binding, fixture.build, restorer, fixture.delivery)
			if !errors.Is(err, context.Canceled) || prepared.ready() || prepared.commanded() || restoreCalls != 0 {
				t.Fatalf("decode-cancelled preparation = (%v, %v, restore=%d)", prepared, err, restoreCalls)
			}
		})
	}
}

func TestClaimedDeliveryPreparationHasNoTimeOrFunctionFields(t *testing.T) {
	typeOfPreparation := reflect.TypeFor[claimedDeliveryPreparation]()
	for index := 0; index < typeOfPreparation.NumField(); index++ {
		field := typeOfPreparation.Field(index)
		if field.Type == reflect.TypeFor[time.Time]() || field.Type.Kind() == reflect.Func {
			t.Fatalf("preparation field %s has runtime behavior or wall clock", field.Name)
		}
	}
}
