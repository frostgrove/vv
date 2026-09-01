package jobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDeliveryRecordRestoresCanonicalStateAndCopiesStorageSlices(t *testing.T) {
	catalog, _, invocation, _, record := deliveryRecordFixture(t, PlacementRegular)
	restored, err := RestoreDeliveryRecord(catalog, record)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Invocation().ID() != invocation.ID() || restored.Invocation().State() != InvocationQueued || string(restored.Payload().Bytes()) != "secret payload" || restored.WireDigest() != record.WireDigest || !restored.PayloadDigest().IsZero() || !restored.Compatible() || restored.RequireCompatible() != nil {
		t.Fatalf("restored delivery = %+v", restored)
	}
	record.Payload.Data[0] = 'X'
	record.Genesis.Context.Token = append(record.Genesis.Context.Token[:0], byte(9))
	record.Outcomes[0] = InvocationOutcome{}
	if string(restored.Payload().Bytes()) != "secret payload" || restored.Invocation().Context().Record().Binding == [32]byte{} || restored.Invocation().History()[0].Kind() != InvocationOutcomeInitial {
		t.Fatal("restored delivery retained storage slices")
	}
	first := restored.Record()
	first.Payload.Data[0] = 'Y'
	first.Genesis.Context.Token = append(first.Genesis.Context.Token[:0], byte(8))
	first.Outcomes[0] = InvocationOutcome{}
	second := restored.Record()
	if string(second.Payload.Data) != "secret payload" || second.Outcomes[0].Kind() != InvocationOutcomeInitial || second.Genesis.Context.Binding == [32]byte{} {
		t.Fatal("record output retained caller slices")
	}
	for _, value := range []any{record, record.Genesis, record.Payload, restored} {
		if bytes.Contains([]byte(fmt.Sprintf("%+v", value)), []byte("secret payload")) {
			t.Fatalf("format exposed payload: %T", value)
		}
		if _, err := json.Marshal(value); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("JSON %T = %v", value, err)
		}
	}
}

func TestOwnedDeliveryRestoreTransfersPayloadStorage(t *testing.T) {
	catalog, _, _, _, record := deliveryRecordFixture(t, PlacementRegular)
	payloadPointer := &record.Payload.Data[0]
	restored, err := restoreOwnedDeliveryRecord(catalog, record)
	if err != nil {
		t.Fatal(err)
	}
	if &restored.payloadValue().encodedBytes()[0] != payloadPointer {
		t.Fatal("owned restore copied payload storage")
	}
	public, err := RestoreDeliveryRecord(catalog, record)
	if err != nil {
		t.Fatal(err)
	}
	if &public.payloadValue().encodedBytes()[0] == payloadPointer {
		t.Fatal("public restore did not preserve defensive ownership")
	}
}

func TestRestoreDeliveryRecordRejectsBoundsBeforeCatalogLookup(t *testing.T) {
	_, _, _, _, base := deliveryRecordFixture(t, PlacementRegular)
	tests := []DeliveryRecord{
		func() DeliveryRecord {
			record := cloneDeliveryRecord(base)
			record.Outcomes = make([]InvocationOutcome, MaxInvocationOutcomes+1)
			return record
		}(),
		func() DeliveryRecord {
			record := cloneDeliveryRecord(base)
			record.Attempts = make([]AttemptRecord, MaxAttemptOrdinal+1)
			return record
		}(),
		func() DeliveryRecord {
			record := cloneDeliveryRecord(base)
			record.Payload.Data = make([]byte, MaxPayloadBytes+1)
			return record
		}(),
		func() DeliveryRecord {
			record := cloneDeliveryRecord(base)
			record.Genesis.Context.Token = make([]byte, MaxIdentityTokenBytes+1)
			return record
		}(),
		func() DeliveryRecord {
			record := cloneDeliveryRecord(base)
			record.Genesis.Context.Trace.Correlations = make([]CorrelationRecord, MaxCorrelationFields+1)
			return record
		}(),
		func() DeliveryRecord {
			record := cloneDeliveryRecord(base)
			record.Genesis.CreatedAt = record.Genesis.CreatedAt.In(time.FixedZone("noncanonical", 6*60*60))
			return record
		}(),
		func() DeliveryRecord {
			record := cloneDeliveryRecord(base)
			record.Genesis.Policy.trace.length = MaxCorrelationFields + 1
			return record
		}(),
		func() DeliveryRecord {
			record := cloneDeliveryRecord(base)
			record.Genesis.Policy.trace.keys[MaxCorrelationFields-1].value = strings.Repeat("x", MaxCorrelationKeyBytes+1)
			return record
		}(),
		func() DeliveryRecord {
			record := cloneDeliveryRecord(base)
			record.Outcomes[0].failure.message = strings.Repeat("x", MaxPublicFailureBytes+1)
			return record
		}(),
	}
	for index, record := range tests {
		if restored, err := RestoreDeliveryRecord(Catalog{}, record); !errors.Is(err, ErrCorrupt) || !restored.IsZero() {
			t.Fatalf("case %d = (%v, %v)", index, restored, err)
		}
	}
}

func TestRestoreDeliveryRecordSeparatesCorruptionFromCompatibility(t *testing.T) {
	catalog, definition, _, _, base := deliveryRecordFixture(t, PlacementRegular)
	corrupt := []DeliveryRecord{
		func() DeliveryRecord {
			record := cloneDeliveryRecord(base)
			record.Payload.Data[0] ^= 1
			return record
		}(),
		func() DeliveryRecord {
			record := cloneDeliveryRecord(base)
			record.WireDigest = WireDigest{value: [32]byte{1}}
			return record
		}(),
		func() DeliveryRecord {
			record := cloneDeliveryRecord(base)
			record.Outcomes[0] = InvocationOutcome{}
			return record
		}(),
	}
	for index, record := range corrupt {
		if _, err := RestoreDeliveryRecord(catalog, record); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("corrupt case %d = %v", index, err)
		}
	}
	tamperedWithoutCatalog := cloneDeliveryRecord(base)
	tamperedWithoutCatalog.Payload.Data[0] ^= 1
	if restored, err := RestoreDeliveryRecord(Catalog{}, tamperedWithoutCatalog); !errors.Is(err, ErrCorrupt) || !restored.IsZero() {
		t.Fatalf("corruption before compatibility = (%v, %v)", restored, err)
	}
	missing, err := RestoreDeliveryRecord(Catalog{}, base)
	if err != nil || missing.IsZero() || missing.Compatibility() != DeliveryDefinitionUnavailable || !errors.Is(missing.RequireCompatible(), ErrUnsupported) {
		t.Fatalf("missing definition = (%v, %v)", missing, err)
	}
	currentPolicy := definition.Policy()
	currentPolicy.Priority++
	changedDefinition := MustDefine(DefinitionSpec[string]{Name: definition.Name(), Codec: String(1), Policy: currentPolicy, Partition: PartitionGlobal})
	if _, err := RestoreDeliveryRecord(MustCatalog(changedDefinition), base); err != nil {
		t.Fatalf("persisted policy after catalog policy change = %v", err)
	}
	unsupportedRevision := cloneDeliveryRecord(base)
	unsupportedRevision.Payload.Version++
	payload, err := NewEncodedPayload(unsupportedRevision.Payload.Codec, unsupportedRevision.Payload.Version, unsupportedRevision.Payload.Data)
	if err != nil {
		t.Fatal(err)
	}
	unsupportedRevision.WireDigest = digestWirePayload(payload)
	unsupported, err := RestoreDeliveryRecord(catalog, unsupportedRevision)
	if err != nil || unsupported.IsZero() || unsupported.Compatibility() != DeliveryPayloadRevisionUnsupported || !errors.Is(unsupported.RequireCompatible(), ErrUnsupported) {
		t.Fatalf("unsupported revision = (%v, %v)", unsupported, err)
	}
	tenantDefinition := MustDefine(DefinitionSpec[string]{Name: definition.Name(), Codec: String(1), Policy: definition.Policy(), Partition: PartitionTenantRequired})
	partitioned, err := RestoreDeliveryRecord(MustCatalog(tenantDefinition), base)
	if err != nil || partitioned.Compatibility() != DeliveryPartitionIncompatible {
		t.Fatalf("changed partition = (%v, %v)", partitioned, err)
	}
}

func TestRestoreDeliveryRecordVerifiesAutomaticSemanticDigest(t *testing.T) {
	catalog, _, _, _, base := deliveryRecordFixture(t, PlacementOnce)
	if _, err := RestoreDeliveryRecord(catalog, base); err != nil {
		t.Fatal(err)
	}
	tampered := cloneDeliveryRecord(base)
	value := tampered.PayloadDigest.Bytes()
	value[0] ^= 1
	tampered.PayloadDigest, _ = NewPayloadDigest(tampered.PayloadDigest.Identity(), tampered.PayloadDigest.Version(), value)
	if _, err := RestoreDeliveryRecord(catalog, tampered); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("tampered semantic digest = %v", err)
	}
	missing := cloneDeliveryRecord(base)
	missing.PayloadDigest = PayloadDigest{}
	if _, err := RestoreDeliveryRecord(catalog, missing); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("missing semantic digest = %v", err)
	}
}

func TestNewDeliveryRecordRejectsWireMismatchAndReturnsDetachedRecord(t *testing.T) {
	_, _, invocation, payload, base := deliveryRecordFixture(t, PlacementRegular)
	if _, err := NewDeliveryRecord(invocation, payload, WireDigest{value: [32]byte{1}}, PayloadDigest{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wire mismatch = %v", err)
	}
	record, err := NewDeliveryRecord(invocation, payload, base.WireDigest, PayloadDigest{})
	if err != nil {
		t.Fatal(err)
	}
	record.Payload.Data[0] = 'X'
	if string(payload.Bytes()) != "secret payload" {
		t.Fatal("record retained encoded payload")
	}
}

func TestDeliveryRecordSizeIsDeterministicAndBoundedForClaimBudgets(t *testing.T) {
	_, _, _, _, record := deliveryRecordFixture(t, PlacementRegular)
	first, err := DeliveryRecordSize(record)
	if err != nil || first <= len(record.Payload.Data) || first > MaxDeliveryRecordBytes {
		t.Fatalf("record size = (%d, %v)", first, err)
	}
	second, err := record.Size()
	if err != nil || second != first {
		t.Fatalf("repeated record size = (%d, %v)", second, err)
	}
	oversized := cloneDeliveryRecord(record)
	oversized.Payload.Data = make([]byte, MaxPayloadBytes+1)
	if size, err := DeliveryRecordSize(oversized); size != 0 || !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized record size = (%d, %v)", size, err)
	}
	code, err := ParseFailureCode("dual.failure")
	if err != nil {
		t.Fatal(err)
	}
	failure, err := NewPublicFailure(code, "bounded")
	if err != nil {
		t.Fatal(err)
	}
	malformed := cloneDeliveryRecord(record)
	malformed.Outcomes[0].failure = failure
	malformed.Outcomes[0].disposition.failure = failure
	charged, err := DeliveryRecordSize(malformed)
	want := first + 2*(len(code.Value())+len(failure.Message()))
	if err != nil || charged != want {
		t.Fatalf("dual raw failure charge = (%d, %v), want %d", charged, err, want)
	}
	unusedTraceKey := cloneDeliveryRecord(record)
	unusedTraceKey.Genesis.Policy.trace.keys[MaxCorrelationFields-1].value = "hidden"
	traceSize, err := DeliveryRecordSize(unusedTraceKey)
	if err != nil || traceSize != first+len("hidden") {
		t.Fatalf("unused raw trace key charge = (%d, %v)", traceSize, err)
	}
	disposition, err := PermanentFailureDisposition(ReasonHandlerFailure, failure)
	if err != nil {
		t.Fatal(err)
	}
	withAttempt := cloneDeliveryRecord(record)
	withAttempt.Attempts = []AttemptRecord{{Binding: testBindingName(t), Build: testBuildID(t), Disposition: disposition}}
	attemptSize, err := DeliveryRecordSize(withAttempt)
	want = first + deliveryAttemptFixedBytes + len(testBindingName(t).Value()) + len(testBuildID(t).Value()) + len(code.Value()) + len(failure.Message())
	if err != nil || attemptSize != want {
		t.Fatalf("raw attempt failure charge = (%d, %v), want %d", attemptSize, err, want)
	}
}

func TestMaxDeliveryRecordBytesCoversEveryDynamicRecordComponent(t *testing.T) {
	failure := PublicFailure{code: FailureCode{value: strings.Repeat("c", MaxFailureCodeBytes)}, message: strings.Repeat("m", MaxPublicFailureBytes)}
	outcomes := make([]InvocationOutcome, MaxInvocationOutcomes)
	for index := range outcomes {
		outcomes[index].failure = failure
	}
	attempts := make([]AttemptRecord, MaxAttemptOrdinal)
	for index := range attempts {
		attempts[index] = AttemptRecord{
			Binding:     BindingName{value: strings.Repeat("b", MaxBindingNameBytes)},
			Build:       BuildID{value: strings.Repeat("u", MaxBuildIDBytes)},
			Disposition: Disposition{failure: failure},
		}
	}
	var trace TracePolicy
	trace.length = MaxCorrelationFields
	for index := range trace.keys {
		trace.keys[index].value = strings.Repeat("k", MaxCorrelationKeyBytes)
	}
	var semantic [32]byte
	semantic[0] = 1
	record := DeliveryRecord{
		Genesis: InvocationGenesisRecord{
			Namespace: Namespace{
				application: Name{value: strings.Repeat("a", MaxNameBytes)},
				environment: Name{value: strings.Repeat("e", MaxNameBytes)},
			},
			Definition:   Name{value: strings.Repeat("d", MaxNameBytes)},
			Queue:        QueueName{value: strings.Repeat("q", MaxQueueNameBytes)},
			LegacyIntent: LegacyIntent{value: strings.Repeat("i", MaxIntentBytes)},
			Policy: PolicySnapshot{
				queue: QueueName{value: strings.Repeat("p", MaxQueueNameBytes)},
				trace: trace,
			},
			Context: DurableContextRecord{
				Token:      bytes.Repeat([]byte{1}, MaxIdentityTokenBytes),
				Provenance: strings.Repeat("v", MaxIdentityProvenanceBytes),
				Trace: TraceCarrierRecord{
					TraceParent: strings.Repeat("t", MaxTraceParentBytes),
					TraceState:  strings.Repeat("s", MaxTraceStateBytes),
					Correlations: []CorrelationRecord{
						{Key: strings.Repeat("x", MaxCorrelationKeyBytes), Value: strings.Repeat("y", 94)},
						{Key: strings.Repeat("z", MaxCorrelationKeyBytes), Value: strings.Repeat("w", 94)},
					},
				},
			},
		},
		Payload: EncodedPayloadRecord{
			Codec: CodecID{value: strings.Repeat("o", MaxCodecIDBytes)},
			Data:  bytes.Repeat([]byte{2}, MaxPayloadBytes),
		},
		PayloadDigest: PayloadDigest{identity: CodecID{value: strings.Repeat("h", MaxCodecIDBytes)}, version: 1, value: semantic},
		Outcomes:      outcomes,
		Attempts:      attempts,
	}
	size, err := DeliveryRecordSize(record)
	if err != nil || size != MaxDeliveryRecordBytes || size > DefaultClaimBytes || size > DefaultWorkerInFlightBytes {
		t.Fatalf("maximum claimable record size = (%d, %v), max=%d claim=%d in-flight=%d", size, err, MaxDeliveryRecordBytes, DefaultClaimBytes, DefaultWorkerInFlightBytes)
	}
}

func deliveryRecordFixture(t *testing.T, mode PlacementMode) (Catalog, *Definition[string], Invocation, EncodedPayload, DeliveryRecord) {
	t.Helper()
	policy := testPolicy(t)
	name := testJobName(t, "tests.delivery-record")
	definition := MustDefine(DefinitionSpec[string]{Name: name, Codec: String(1), Policy: policy, Partition: PartitionGlobal})
	catalog := MustCatalog(definition)
	snapshot, err := NewPolicySnapshot(policy)
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := NamespaceOf("tests", "test")
	if err != nil {
		t.Fatal(err)
	}
	partition := partitionKey(namespace, ProducerPartition{})
	id := queueTestInvocationID(t, 73)
	var intent IntentKey
	switch mode {
	case PlacementRegular:
		intents, digestErr := digestRegularIntents(CurrentIntentDigestPlan(), namespace, partition, name, id)
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		intent = intents.Current()
	case PlacementOnce:
		intents, digestErr := digestProducerIntents(CurrentIntentDigestPlan(), namespace, partition, name, IntentOnce, Intent("delivery-once"))
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		intent = intents.Current()
	default:
		t.Fatalf("unsupported fixture mode %v", mode)
	}
	createdAt := time.Date(2032, 3, 4, 5, 6, 7, 8, time.UTC)
	invocation, err := NewInvocation(InvocationSpec{
		ID:         id,
		Namespace:  namespace,
		Partition:  partition,
		Definition: name,
		Queue:      snapshot.Queue(),
		Mode:       mode,
		Intent:     intent,
		Priority:   snapshot.Priority(),
		CreatedAt:  createdAt,
		EligibleAt: createdAt.Add(time.Minute),
		Policy:     snapshot,
		Context:    mustTestDurableContext(t, namespace, partition, name, snapshot.Trace()),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, semantic, err := definition.preparePayload("secret payload", mode == PlacementOnce)
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewDeliveryRecord(invocation, payload, digestWirePayload(payload), semantic)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, definition, invocation, payload, record
}
