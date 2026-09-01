package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type deliveryDriverTestDouble struct {
	description BackendDescription
	panic       bool
}

func (d *deliveryDriverTestDouble) Description() BackendDescription {
	if d.panic {
		panic("driver secret")
	}
	return d.description
}
func (*deliveryDriverTestDouble) Claim(context.Context, ClaimRequest) (ClaimBatch, error) {
	return ClaimBatch{}, nil
}
func (*deliveryDriverTestDouble) Renew(context.Context, RenewRequest) (RenewResult, error) {
	return RenewResult{}, nil
}
func (*deliveryDriverTestDouble) Apply(context.Context, ApplyRequest) (ApplyResult, error) {
	return ApplyResult{}, nil
}
func (*deliveryDriverTestDouble) Recover(context.Context, RecoverRequest) (RecoverResult, error) {
	return RecoverResult{}, nil
}

func TestDeliveryDriverDescriptionFailsClosed(t *testing.T) {
	description := queueTestBackendDescription(1)
	driver := &deliveryDriverTestDouble{description: description}
	if got, err := ValidateDeliveryDriver(driver); err != nil || got != description {
		t.Fatalf("description = (%v, %v)", got, err)
	}
	var typedNil *deliveryDriverTestDouble
	if _, err := ValidateDeliveryDriver(typedNil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("typed nil = %v", err)
	}
	if _, err := ValidateDeliveryDriver(&deliveryDriverTestDouble{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero description = %v", err)
	}
	if _, err := ValidateDeliveryDriver(&deliveryDriverTestDouble{panic: true}); !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("panic description = %v", err)
	}
	var _ DeliveryDriver = driver
}

func TestClaimTargetAndRequestAreCanonicalBoundedAndImmutable(t *testing.T) {
	_, definition, _, _, record := deliveryRecordFixture(t, PlacementRegular)
	incarnation := driverTestWorkerIncarnation(t)
	oldCodec, _ := ParseCodecID("legacy.string")
	oldRevision, _ := NewPayloadRevision(oldCodec, 1)
	currentRevision, _ := NewPayloadRevision(record.Payload.Codec, 2)
	revisions := []PayloadRevision{currentRevision, oldRevision}
	target, err := NewClaimTarget(ClaimTargetSpec{Definition: definition.Name(), Binding: testBindingName(t), Build: testBuildID(t), SupportedRevisions: revisions, Available: 2})
	if err != nil {
		t.Fatal(err)
	}
	revisions[0] = PayloadRevision{}
	gotRevisions := target.SupportedRevisions()
	if len(gotRevisions) != 2 || gotRevisions[0] != oldRevision || gotRevisions[1] != currentRevision {
		t.Fatalf("canonical revisions = %+v", gotRevisions)
	}
	gotRevisions[0] = PayloadRevision{}
	if target.SupportedRevisions()[0] != oldRevision {
		t.Fatal("target retained returned revisions")
	}
	request, err := NewClaimRequest(ClaimRequestSpec{Namespace: record.Genesis.Namespace, Incarnation: incarnation, Targets: []ClaimTarget{target}, MaxItems: 2, MaxBytes: MaxDeliveryRecordBytes, LeaseTTL: DefaultLeaseTTL})
	if err != nil || request.Incarnation() != incarnation || request.MaxItems() != 2 || request.MaxBytes() != MaxDeliveryRecordBytes || request.LeaseTTL() != DefaultLeaseTTL {
		t.Fatalf("request = (%v, %v)", request, err)
	}
	returnedTargets := request.Targets()
	returnedTargets[0].revisions[0] = PayloadRevision{}
	if request.Targets()[0].SupportedRevisions()[0] != oldRevision {
		t.Fatal("request retained returned targets")
	}
	if _, err := NewClaimRequest(ClaimRequestSpec{Namespace: record.Genesis.Namespace, Targets: []ClaimTarget{target}, MaxItems: 1, MaxBytes: MaxDeliveryRecordBytes, LeaseTTL: DefaultLeaseTTL}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero incarnation = %v", err)
	}
	if _, err := NewClaimRequest(ClaimRequestSpec{Namespace: record.Genesis.Namespace, Incarnation: incarnation, Targets: []ClaimTarget{target}, MaxItems: 3, MaxBytes: MaxDeliveryRecordBytes, LeaseTTL: DefaultLeaseTTL}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("items beyond availability = %v", err)
	}
	if _, err := NewClaimRequest(ClaimRequestSpec{Namespace: record.Genesis.Namespace, Incarnation: incarnation, Targets: []ClaimTarget{target}, MaxItems: 1, MaxBytes: MaxDeliveryRecordBytes - 1, LeaseTTL: DefaultLeaseTTL}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unclaimable byte cap = %v", err)
	}
	if _, err := NewClaimRequest(ClaimRequestSpec{Namespace: record.Genesis.Namespace, Incarnation: incarnation, Targets: []ClaimTarget{target}, MaxItems: 1, MaxBytes: MaxDeliveryRecordBytes, LeaseTTL: MinimumLeaseTTL - time.Nanosecond}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short lease = %v", err)
	}
	duplicate, _ := NewClaimTarget(ClaimTargetSpec{Definition: definition.Name(), Binding: mustDriverBinding(t, "worker.other"), Build: testBuildID(t), SupportedRevisions: target.SupportedRevisions(), Available: 1})
	if _, err := NewClaimRequest(ClaimRequestSpec{Namespace: record.Genesis.Namespace, Incarnation: incarnation, Targets: []ClaimTarget{target, duplicate}, MaxItems: 1, MaxBytes: MaxDeliveryRecordBytes, LeaseTTL: DefaultLeaseTTL}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate definition = %v", err)
	}
	if _, err := NewClaimTarget(ClaimTargetSpec{Definition: definition.Name(), Binding: testBindingName(t), Build: testBuildID(t), SupportedRevisions: make([]PayloadRevision, MaxSupportedRevisions+1), Available: 1}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("revision preallocation bound = %v", err)
	}
	if _, err := NewClaimRequest(ClaimRequestSpec{Targets: make([]ClaimTarget, MaxDefinitions+1)}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("target preallocation bound = %v", err)
	}
	for _, value := range []any{oldRevision, target, request} {
		if _, err := json.Marshal(value); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("JSON %T = %v", value, err)
		}
	}
}

func TestClaimBatchValidatesTransportBeforeRestoredSemantics(t *testing.T) {
	_, definition, invocation, _, record := deliveryRecordFixture(t, PlacementRegular)
	description := queueTestBackendDescription(1)
	target := driverClaimTarget(t, definition.Name(), record.Payload.Codec, record.Payload.Version, 1, "worker.primary")
	request := mustClaimRequest(t, record.Genesis.Namespace, []ClaimTarget{target}, 1, MaxDeliveryRecordBytes)
	lease := deliveryTestLease(t, invocation.ID(), []byte("claim-token"))
	now := record.Genesis.EligibleAt
	tests := []struct {
		name   string
		mutate func(*DeliveryRecord)
	}{
		{name: "zero genesis id", mutate: func(value *DeliveryRecord) { value.Genesis.ID = InvocationID{} }},
		{name: "zero namespace", mutate: func(value *DeliveryRecord) { value.Genesis.Namespace = Namespace{} }},
		{name: "zero definition", mutate: func(value *DeliveryRecord) { value.Genesis.Definition = Name{} }},
		{name: "empty outcomes", mutate: func(value *DeliveryRecord) { value.Outcomes = nil }},
		{name: "unsupported revision", mutate: func(value *DeliveryRecord) { value.Payload.Version++ }},
		{name: "bad digest", mutate: func(value *DeliveryRecord) { value.WireDigest = WireDigest{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			corrupt := cloneDeliveryRecord(record)
			test.mutate(&corrupt)
			item, err := NewClaimedDelivery(target, lease, corrupt)
			if err != nil {
				t.Fatal(err)
			}
			batch, err := NewClaimBatch(now, []ClaimedDelivery{item})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateClaimBatch(description, request, batch); err != nil {
				t.Fatalf("transport rejected bounded raw record: %v", err)
			}
		})
	}
}

func TestClaimBatchEnforcesStableIdentityTargetCapacityAndCopies(t *testing.T) {
	_, definition, invocation, _, record := deliveryRecordFixture(t, PlacementRegular)
	description := queueTestBackendDescription(1)
	first := driverClaimTarget(t, definition.Name(), record.Payload.Codec, record.Payload.Version, 1, "worker.primary")
	secondName, _ := ParseName("tests.delivery-other")
	second := driverClaimTarget(t, secondName, record.Payload.Codec, record.Payload.Version, 10, "worker.secondary")
	request := mustClaimRequest(t, record.Genesis.Namespace, []ClaimTarget{first, second}, 11, MaxClaimBytes)
	firstLease := deliveryTestLease(t, invocation.ID(), []byte("first"))
	secondLease := deliveryTestLease(t, queueTestInvocationID(t, 74), []byte("second"))
	safeSource := cloneDeliveryRecord(record)
	safeItem, _ := NewClaimedDelivery(first, firstLease, safeSource)
	safeSource.Payload.Data[0] = 'X'
	safeSource.Outcomes[0] = InvocationOutcome{}
	if string(safeItem.Record().Payload.Data) != "secret payload" || safeItem.Record().Outcomes[0].Kind() != InvocationOutcomeInitial {
		t.Fatal("safe claimed delivery retained source storage")
	}
	sourceRecord := cloneDeliveryRecord(record)
	transferredPayload := sourceRecord.Payload.Data
	firstItem, err := TakeClaimedDelivery(first, firstLease, &sourceRecord)
	if err != nil || !sourceRecord.Genesis.ID.IsZero() || sourceRecord.Payload.Data != nil || sourceRecord.Outcomes != nil || sourceRecord.Attempts != nil || &transferredPayload[0] != &firstItem.record.value.Payload.Data[0] {
		t.Fatal("claimed delivery copied transferred record")
	}
	secondItem, _ := NewClaimedDelivery(first, secondLease, record)
	batch, _ := NewClaimBatch(record.Genesis.EligibleAt, []ClaimedDelivery{firstItem, secondItem})
	if _, err := ValidateClaimBatch(description, request, batch); !errors.Is(err, ErrDriver) || !errors.Is(err, ErrTooLarge) {
		t.Fatalf("per-target availability = %v", err)
	}
	validBatch, _ := NewClaimBatch(record.Genesis.EligibleAt, []ClaimedDelivery{firstItem})
	validated, err := ValidateClaimBatch(description, request, validBatch)
	if err != nil || validated.Len() != 1 {
		t.Fatalf("valid batch = (%v, %v)", validated, err)
	}
	forgedRequest := request
	forgedRequest.incarnation = WorkerIncarnation{}
	if _, err := ValidateClaimBatch(description, forgedRequest, validBatch); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged zero incarnation = %v", err)
	}
	if firstItem.record != validBatch.items[0].record || firstItem.record != validated.items[0].record || firstItem.record != validated.Items()[0].record {
		t.Fatal("claim boundary amplified delivery record copies")
	}
	items := validated.Items()
	publicRecord := items[0].Record()
	publicRecord.Payload.Data[0] = 'X'
	publicLease := items[0].Lease()
	publicLease.token[0] = 'X'
	if string(validated.Items()[0].Record().Payload.Data) != "secret payload" || string(validated.Items()[0].Lease().DriverToken()) != "first" {
		t.Fatal("validated batch retained returned storage or token slices")
	}
	duplicateSecond, _ := NewClaimedDelivery(second, firstLease, record)
	duplicateBatch, _ := NewClaimBatch(record.Genesis.EligibleAt, []ClaimedDelivery{firstItem, duplicateSecond})
	if _, err := ValidateClaimBatch(description, request, duplicateBatch); !errors.Is(err, ErrDriver) || !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate invocation = %v", err)
	}
	wrongDescription := queueTestBackendDescription(2)
	if _, err := ValidateClaimBatch(wrongDescription, request, validBatch); !errors.Is(err, ErrDriver) {
		t.Fatalf("wrong backend = %v", err)
	}
	for _, value := range []any{firstItem, validBatch} {
		if _, err := json.Marshal(value); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("JSON %T = %v", value, err)
		}
	}
	oversizedPayload := cloneDeliveryRecord(record)
	oversizedPayload.Payload.Data = make([]byte, MaxPayloadBytes+1)
	if _, err := NewClaimedDelivery(first, firstLease, oversizedPayload); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized claimed payload = %v", err)
	}
	if _, err := TakeClaimedDelivery(first, firstLease, &oversizedPayload); !errors.Is(err, ErrTooLarge) || len(oversizedPayload.Payload.Data) != MaxPayloadBytes+1 {
		t.Fatalf("failed ownership transfer = %v", err)
	}
	if _, err := TakeClaimedDelivery(first, firstLease, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil ownership transfer = %v", err)
	}
	oversizedOutcomes := cloneDeliveryRecord(record)
	oversizedOutcomes.Outcomes = make([]InvocationOutcome, MaxInvocationOutcomes+1)
	if _, err := NewRecoveredDelivery(firstLease, oversizedOutcomes); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized recovered outcomes = %v", err)
	}
}

func TestClaimedDeliveryRecordCanBeConsumedOnce(t *testing.T) {
	_, definition, invocation, _, record := deliveryRecordFixture(t, PlacementRegular)
	target := driverClaimTarget(t, definition.Name(), record.Payload.Codec, record.Payload.Version, 1, "worker.primary")
	description := queueTestBackendDescription(1)
	request := mustClaimRequest(t, record.Genesis.Namespace, []ClaimTarget{target}, 1, MaxDeliveryRecordBytes)
	lease := deliveryTestLease(t, invocation.ID(), []byte("consume-record"))
	source := cloneDeliveryRecord(record)
	payloadPointer := &source.Payload.Data[0]
	delivery, err := TakeClaimedDelivery(target, lease, &source)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := NewClaimBatch(record.Genesis.EligibleAt, []ClaimedDelivery{delivery})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateClaimBatch(description, request, batch); err != nil {
		t.Fatal(err)
	}
	owned, ok := delivery.takeRecordValue()
	if !ok || &owned.Payload.Data[0] != payloadPointer {
		t.Fatal("claimed delivery did not transfer record ownership")
	}
	if second, secondOK := delivery.takeRecordValue(); secondOK || !second.Genesis.ID.IsZero() {
		t.Fatal("claimed delivery allowed a second ownership transfer")
	}
	if public := delivery.Record(); !public.Genesis.ID.IsZero() || public.Payload.Data != nil || public.Outcomes != nil || public.Attempts != nil {
		t.Fatal("consumed claimed delivery retained record storage")
	}
	if _, err := ValidateClaimBatch(description, request, batch); !errors.Is(err, ErrDriver) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("consumed claim batch validation = %v", err)
	}
}

func TestClaimedDeliveryPublicRecordIsolatedFromConcurrentConsumption(t *testing.T) {
	_, definition, invocation, _, record := deliveryRecordFixture(t, PlacementRegular)
	record.Payload.Data = bytes.Repeat([]byte{'a'}, MaxPayloadBytes)
	target := driverClaimTarget(t, definition.Name(), record.Payload.Codec, record.Payload.Version, 1, "worker.primary")
	lease := deliveryTestLease(t, invocation.ID(), []byte("concurrent-consume"))
	delivery, err := NewClaimedDelivery(target, lease, record)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	readerDone := make(chan error, 1)
	go func() {
		close(started)
		for index := 0; index < 100; index++ {
			snapshot := delivery.Record()
			if snapshot.Payload.Data != nil && bytes.Count(snapshot.Payload.Data, []byte{'a'}) != len(snapshot.Payload.Data) {
				readerDone <- errors.New("public record observed transferred mutation")
				return
			}
		}
		readerDone <- nil
	}()
	<-started
	owned, ok := delivery.takeRecordValue()
	if !ok {
		t.Fatal("record was not consumed")
	}
	for index := range owned.Payload.Data {
		owned.Payload.Data[index] = 'b'
	}
	if readerErr := <-readerDone; readerErr != nil {
		t.Fatal(readerErr)
	}
}

func TestRecoveredDeliveryRecordCanBeConsumedOnce(t *testing.T) {
	_, _, invocation, _, record := deliveryRecordFixture(t, PlacementRegular)
	description := queueTestBackendDescription(1)
	if _, err := NewRecoverRequest(RecoverRequestSpec{Namespace: record.Genesis.Namespace, MaxItems: 1, MaxBytes: MaxDeliveryRecordBytes, LeaseTTL: DefaultLeaseTTL}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero incarnation = %v", err)
	}
	request, err := NewRecoverRequest(RecoverRequestSpec{Namespace: record.Genesis.Namespace, Incarnation: driverTestWorkerIncarnation(t), MaxItems: 1, MaxBytes: MaxDeliveryRecordBytes, LeaseTTL: DefaultLeaseTTL})
	if err != nil {
		t.Fatal(err)
	}
	if request.Incarnation().IsZero() {
		t.Fatal("recover request lost worker incarnation")
	}
	lease := deliveryTestLease(t, invocation.ID(), []byte("consume-recovered-record"))
	source := cloneDeliveryRecord(record)
	payloadPointer := &source.Payload.Data[0]
	delivery, err := TakeRecoveredDelivery(lease, &source)
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewRecoverResult(record.Genesis.EligibleAt, []RecoveredDelivery{delivery}, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRecoverResult(description, request, result); err != nil {
		t.Fatal(err)
	}
	forgedRequest := request
	forgedRequest.incarnation = WorkerIncarnation{}
	if _, err := ValidateRecoverResult(description, forgedRequest, result); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged zero incarnation = %v", err)
	}
	owned, ok := delivery.takeRecordValue()
	if !ok || &owned.Payload.Data[0] != payloadPointer {
		t.Fatal("recovered delivery did not transfer record ownership")
	}
	if second, secondOK := delivery.takeRecordValue(); secondOK || !second.Genesis.ID.IsZero() {
		t.Fatal("recovered delivery allowed a second ownership transfer")
	}
	if public := delivery.Record(); !public.Genesis.ID.IsZero() || public.Payload.Data != nil || public.Outcomes != nil || public.Attempts != nil {
		t.Fatal("consumed recovered delivery retained record storage")
	}
	if _, err := ValidateRecoverResult(description, request, result); !errors.Is(err, ErrDriver) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("consumed recover result validation = %v", err)
	}
}

func TestClaimBatchChargesEveryRecordBeforeItemSemantics(t *testing.T) {
	_, definition, invocation, _, record := deliveryRecordFixture(t, PlacementRegular)
	target := driverClaimTarget(t, definition.Name(), record.Payload.Codec, record.Payload.Version, 2, "worker.primary")
	request := mustClaimRequest(t, record.Genesis.Namespace, []ClaimTarget{target}, 2, MaxClaimBytes)
	invalidFirst := ClaimedDelivery{record: newDeliveryRecordEnvelope(record, true)}
	oversized := cloneDeliveryRecord(record)
	oversized.Payload.Data = make([]byte, MaxPayloadBytes+1)
	secondLease := deliveryTestLease(t, queueTestInvocationID(t, 74), []byte("second"))
	oversizedSecond := ClaimedDelivery{target: target, lease: secondLease, record: newDeliveryRecordEnvelope(oversized, true)}
	batch := ClaimBatch{observedAt: record.Genesis.EligibleAt, items: []ClaimedDelivery{invalidFirst, oversizedSecond}}
	if _, err := ValidateClaimBatch(queueTestBackendDescription(1), request, batch); !errors.Is(err, ErrDriver) || !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size-first validation = %v", err)
	}
	_ = invocation
}

func TestClaimBatchEnforcesAggregateByteBudget(t *testing.T) {
	_, definition, _, _, record := deliveryRecordFixture(t, PlacementRegular)
	record.Payload.Data = make([]byte, MaxPayloadBytes)
	record.Outcomes = make([]InvocationOutcome, MaxInvocationOutcomes)
	record.Attempts = make([]AttemptRecord, MaxAttemptOrdinal)
	size, err := DeliveryRecordSize(record)
	if err != nil || size <= 0 {
		t.Fatalf("large record size = (%d, %v)", size, err)
	}
	count := MaxDeliveryRecordBytes/size + 1
	if count > MaxClaimItems || count > MaxBindingConcurrency {
		t.Fatalf("aggregate fixture count = %d", count)
	}
	target := driverClaimTarget(t, definition.Name(), record.Payload.Codec, record.Payload.Version, count, "worker.primary")
	request := mustClaimRequest(t, record.Genesis.Namespace, []ClaimTarget{target}, count, MaxDeliveryRecordBytes)
	items := make([]ClaimedDelivery, count)
	envelope := newDeliveryRecordEnvelope(record, true)
	for index := range items {
		lease := deliveryTestLease(t, queueTestInvocationID(t, byte(80+index)), []byte(fmt.Sprintf("lease-%d", index)))
		items[index] = ClaimedDelivery{target: target, lease: lease, record: envelope}
	}
	batch := ClaimBatch{observedAt: record.Genesis.EligibleAt, items: items}
	if _, err := ValidateClaimBatch(queueTestBackendDescription(1), request, batch); !errors.Is(err, ErrDriver) || !errors.Is(err, ErrTooLarge) {
		t.Fatalf("aggregate bytes = %v", err)
	}
}

func TestRenewContractIsOrderedUniqueRotatingAndControlAware(t *testing.T) {
	first := deliveryTestLease(t, queueTestInvocationID(t, 81), []byte("first-old"))
	second := deliveryTestLease(t, queueTestInvocationID(t, 82), []byte("second-old"))
	request, err := NewRenewRequest([]LeaseRef{first, second}, DefaultLeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	firstNew := deliveryTestLease(t, first.InvocationID(), []byte("first-new"))
	firstResult, err := NewLeaseRenewal(first, firstNew, DeliveryMutationApplied, DeliveryControlCancelRequested)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := NewLeaseRenewal(second, LeaseRef{}, DeliveryMutationLeaseLost, DeliveryControlTerminated)
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewRenewResult(time.Date(2032, 1, 1, 0, 0, 0, 0, time.UTC), []LeaseRenewal{firstResult, secondResult})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := ValidateRenewResult(queueTestBackendDescription(1), request, result)
	if err != nil || validated.Len() != 2 || validated.Items()[0].Control() != DeliveryControlCancelRequested {
		t.Fatalf("renew result = (%v, %v)", validated, err)
	}
	copyOut := validated.Items()
	copyOut[0].current.token[0] = 'X'
	if string(validated.Items()[0].Current().DriverToken()) != "first-new" {
		t.Fatal("renew result retained returned token")
	}
	if _, err := NewLeaseRenewal(first, first, DeliveryMutationApplied, DeliveryControlNone); err != nil {
		t.Fatalf("stable renewal token = %v", err)
	}
	if _, err := NewLeaseRenewal(first, firstNew, DeliveryMutationApplied, DeliveryControlTerminated); !errors.Is(err, ErrInvalid) {
		t.Fatalf("renewed terminated lease = %v", err)
	}
	if _, err := NewLeaseRenewal(first, LeaseRef{}, DeliveryMutationAmbiguous, DeliveryControlCancelRequested); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ambiguous control = %v", err)
	}
	duplicateInvocation := deliveryTestLease(t, first.InvocationID(), []byte("other-token"))
	if _, err := NewRenewRequest([]LeaseRef{first, duplicateInvocation}, DefaultLeaseTTL); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate invocation = %v", err)
	}
	if _, err := NewRenewRequest(make([]LeaseRef, MaxClaimItems+1), DefaultLeaseTTL); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("renew preallocation bound = %v", err)
	}
	reversed, _ := NewRenewResult(result.ObservedAt(), []LeaseRenewal{secondResult, firstResult})
	if _, err := ValidateRenewResult(queueTestBackendDescription(1), request, reversed); !errors.Is(err, ErrDriver) {
		t.Fatalf("reordered renewal = %v", err)
	}
	missing, _ := NewRenewResult(result.ObservedAt(), []LeaseRenewal{firstResult})
	if _, err := ValidateRenewResult(queueTestBackendDescription(1), request, missing); !errors.Is(err, ErrDriver) {
		t.Fatalf("missing renewal = %v", err)
	}
	for _, value := range []any{request, firstResult, result} {
		if _, err := json.Marshal(value); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("JSON %T = %v", value, err)
		}
	}
}

func TestApplyResultRequiresAuthoritativeAppliedBegin(t *testing.T) {
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	description := queueTestBackendDescription(1)
	lease := deliveryTestLease(t, invocation.ID(), []byte("apply"))
	command, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	request, _ := NewApplyRequest(command)
	application, err := ApplyDeliveryCommand(invocation, command, invocation.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	applied, _ := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlNone)
	result, err := NewApplyResult(invocation.EligibleAt(), applied, application)
	if err != nil {
		t.Fatal(err)
	}
	if result.HandlerReady() {
		t.Fatal("unvalidated apply result authorized handler")
	}
	terminatedApplied, _ := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlTerminated)
	if _, err := NewApplyResult(invocation.EligibleAt(), terminatedApplied, application); !errors.Is(err, ErrInvalid) {
		t.Fatalf("applied terminated = %v", err)
	}
	validated, err := ValidateApplyResult(description, request, result)
	if err != nil || !validated.HandlerReady() {
		t.Fatalf("active begin = (%v, %v)", validated, err)
	}
	expiredApplication, err := ApplyDeliveryCommand(invocation, command, invocation.MaxElapsedAt())
	if err != nil {
		t.Fatal(err)
	}
	expiredResult, _ := NewApplyResult(invocation.MaxElapsedAt(), applied, expiredApplication)
	validated, err = ValidateApplyResult(description, request, expiredResult)
	if err != nil || validated.HandlerReady() || !validated.Application().Invocation().State().Terminal() {
		t.Fatalf("expired begin = (%v, %v)", validated, err)
	}
	lost, _ := NewDeliveryCommandResult(DeliveryMutationLeaseLost, DeliveryControlNone)
	lostResult, err := NewApplyResult(invocation.EligibleAt(), lost, DeliveryApplication{})
	if err != nil {
		t.Fatal(err)
	}
	if validated, err := ValidateApplyResult(description, request, lostResult); err != nil || validated.HandlerReady() {
		t.Fatalf("lease lost = (%v, %v)", validated, err)
	}
	lostTerminated, _ := NewDeliveryCommandResult(DeliveryMutationLeaseLost, DeliveryControlTerminated)
	lostTerminatedResult, err := NewApplyResult(invocation.EligibleAt(), lostTerminated, DeliveryApplication{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateApplyResult(description, request, lostTerminatedResult); err != nil {
		t.Fatalf("terminated lease loss = %v", err)
	}
	ambiguousCancel, _ := NewDeliveryCommandResult(DeliveryMutationAmbiguous, DeliveryControlCancelRequested)
	if _, err := NewApplyResult(time.Time{}, ambiguousCancel, DeliveryApplication{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ambiguous cancellation = %v", err)
	}
	if _, err := NewApplyResult(invocation.EligibleAt(), lost, DeliveryApplication{kind: DeliveryCommandBeginAttempt}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("lease lost application = %v", err)
	}
	if _, err := NewApplyResult(invocation.EligibleAt(), applied, DeliveryApplication{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("applied zero application = %v", err)
	}
	forged := result
	forged.application.attempt.binding = mustDriverBinding(t, "worker.forged")
	if _, err := ValidateApplyResult(description, request, forged); !errors.Is(err, ErrDriver) || !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged begin = %v", err)
	}
	if _, err := ValidateApplyResult(queueTestBackendDescription(2), request, result); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong apply backend = %v", err)
	}
	for _, value := range []any{request, result} {
		if _, err := json.Marshal(value); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("JSON %T = %v", value, err)
		}
	}
}

func TestApplyResultObservationTimeTracksMutationCertainty(t *testing.T) {
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	description := queueTestBackendDescription(1)
	lease := deliveryTestLease(t, invocation.ID(), []byte("observation"))
	command, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	request, _ := NewApplyRequest(command)
	application, err := ApplyDeliveryCommand(invocation, command, invocation.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	applied, _ := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlNone)
	lost, _ := NewDeliveryCommandResult(DeliveryMutationLeaseLost, DeliveryControlNone)
	ambiguous, _ := NewDeliveryCommandResult(DeliveryMutationAmbiguous, DeliveryControlNone)
	if _, err := NewApplyResult(time.Time{}, applied, application); !errors.Is(err, ErrInvalid) {
		t.Fatalf("applied without observation = %v", err)
	}
	if _, err := NewApplyResult(time.Time{}, lost, DeliveryApplication{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("lease loss without observation = %v", err)
	}
	if _, err := NewApplyResult(invocation.EligibleAt(), ambiguous, DeliveryApplication{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ambiguous authoritative observation = %v", err)
	}
	ambiguousResult, err := NewApplyResult(time.Time{}, ambiguous, DeliveryApplication{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateApplyResult(description, request, ambiguousResult); err != nil {
		t.Fatalf("zero ambiguous observation = %v", err)
	}
	forgedAmbiguous := ambiguousResult
	forgedAmbiguous.observedAt = invocation.EligibleAt()
	if _, err := ValidateApplyResult(description, request, forgedAmbiguous); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged ambiguous observation = %v", err)
	}
	local := invocation.EligibleAt().In(time.FixedZone("local", 6*60*60))
	lostResult, err := NewApplyResult(invocation.EligibleAt(), lost, DeliveryApplication{})
	if err != nil {
		t.Fatal(err)
	}
	lostResult.observedAt = local
	if _, err := ValidateApplyResult(description, request, lostResult); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("noncanonical lease loss observation = %v", err)
	}
	canonical, err := NewApplyResult(local, applied, application)
	if err != nil || canonical.ObservedAt() != invocation.EligibleAt() {
		t.Fatalf("canonical observation = (%v, %v)", canonical.ObservedAt(), err)
	}
	shifted := canonical
	shifted.observedAt = shifted.observedAt.Add(time.Nanosecond)
	if _, err := ValidateApplyResult(description, request, shifted); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("shifted observation = %v", err)
	}
	noncanonical := canonical
	noncanonical.observedAt = local
	if _, err := ValidateApplyResult(description, request, noncanonical); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("noncanonical observation = %v", err)
	}
}

func TestApplyResultValidatesDeadlineArbitrationAndCancellationFence(t *testing.T) {
	policy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(10*time.Minute), RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
	invocation := testInvocationForPolicy(t, policy)
	description := queueTestBackendDescription(1)
	lease := deliveryTestLease(t, invocation.ID(), []byte("deadline-result"))
	begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	started, err := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	running := started.Invocation()
	attempt, _ := started.Attempt()
	command, _ := ArbitrateAttemptDeadlineCommand(lease, time.Second)
	request, _ := NewApplyRequest(command)
	applied, _ := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlNone)
	earlyAt := attempt.Deadline().Add(-time.Nanosecond)
	early, err := ApplyDeliveryCommand(running, command, earlyAt)
	if err != nil {
		t.Fatal(err)
	}
	earlyResult, _ := NewApplyResult(earlyAt, applied, early)
	validated, err := ValidateApplyResult(description, request, earlyResult)
	if err != nil || validated.HandlerReady() || validated.Application().Changed() {
		t.Fatalf("early deadline result = (%v, %v)", validated, err)
	}
	forgedEarly := earlyResult
	forgedEarly.application.invocation.cancelRequestedAt = attempt.StartedAt()
	if _, err := ValidateApplyResult(description, request, forgedEarly); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged early cancellation field = %v", err)
	}
	forgedEarly = earlyResult
	forgedEarly.application.invocation.finishedAt = attempt.StartedAt()
	if _, err := ValidateApplyResult(description, request, forgedEarly); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged early finish field = %v", err)
	}
	forgedEarly = earlyResult
	earlyHistory := *forgedEarly.application.invocation.history
	earlyHistory.value.reason = ReasonLeaseLost
	forgedEarly.application.invocation.history = &earlyHistory
	if _, err := ValidateApplyResult(description, request, forgedEarly); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged early outcome = %v", err)
	}
	forgedEarly = earlyResult
	forgedEarly.observedAt = attempt.Deadline()
	if _, err := ValidateApplyResult(description, request, forgedEarly); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("early result at deadline = %v", err)
	}
	due, err := ApplyDeliveryCommand(running, command, attempt.Deadline())
	if err != nil {
		t.Fatal(err)
	}
	dueResult, _ := NewApplyResult(attempt.Deadline(), applied, due)
	if _, err := ValidateApplyResult(description, request, dueResult); err != nil {
		t.Fatalf("due deadline result = %v", err)
	}
	forgedDue := dueResult
	forgedDue.application.invocation.finishedAt = attempt.Deadline()
	if _, err := ValidateApplyResult(description, request, forgedDue); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged queued finish field = %v", err)
	}
	forgedSchedule := dueResult
	forgedSchedule.application.attempt.deadline = forgedSchedule.application.attempt.deadline.Add(-time.Second)
	attempts := *forgedSchedule.application.invocation.attempts
	attempts.value = forgedSchedule.application.attempt
	forgedSchedule.application.invocation.attempts = &attempts
	if _, err := ValidateApplyResult(description, request, forgedSchedule); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged attempt schedule = %v", err)
	}
	forgedOutcome := dueResult
	outcomes := *forgedOutcome.application.invocation.history
	outcomes.value.reason = ReasonLeaseLost
	forgedOutcome.application.invocation.history = &outcomes
	if _, err := ValidateApplyResult(description, request, forgedOutcome); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged timeout outcome = %v", err)
	}
	forgedPredecessor := dueResult
	outcomes = *forgedPredecessor.application.invocation.history
	predecessor := *outcomes.previous
	predecessor.value.occurredAt = predecessor.value.occurredAt.Add(time.Nanosecond)
	outcomes.previous = &predecessor
	forgedPredecessor.application.invocation.history = &outcomes
	if _, err := ValidateApplyResult(description, request, forgedPredecessor); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged active predecessor = %v", err)
	}
	requested, err := running.RequestCancel(attempt.StartedAt().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	earlyCancel, err := ApplyDeliveryCommand(requested, command, earlyAt)
	if err != nil {
		t.Fatal(err)
	}
	appliedCancel, _ := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlCancelRequested)
	earlyCancelResult, _ := NewApplyResult(earlyAt, appliedCancel, earlyCancel)
	if _, err := ValidateApplyResult(description, request, earlyCancelResult); err != nil {
		t.Fatalf("early cancellation result = %v", err)
	}
	terminated, err := ApplyDeliveryCommand(requested, command, attempt.Deadline())
	if err != nil {
		t.Fatal(err)
	}
	appliedTerminated, _ := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlTerminated)
	terminatedResult, err := NewApplyResult(attempt.Deadline(), appliedTerminated, terminated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateApplyResult(description, request, terminatedResult); err != nil {
		t.Fatalf("terminated cancellation result = %v", err)
	}
	forgedTerminationField := terminatedResult
	forgedTerminationField.application.invocation.finishedAt = time.Time{}
	if _, err := ValidateApplyResult(description, request, forgedTerminationField); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged termination finish field = %v", err)
	}
	forged := terminated
	history := *forged.invocation.history
	predecessor = *history.previous
	predecessor.value.reason = ReasonLeaseLost
	history.previous = &predecessor
	forged.invocation.history = &history
	forgedResult, err := NewApplyResult(attempt.Deadline(), appliedTerminated, forged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateApplyResult(description, request, forgedResult); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged cancellation predecessor = %v", err)
	}
	finish, _ := FinishAttemptCommand(lease, SuccessDisposition(), 0, time.Second)
	cancelled, err := ApplyDeliveryCommand(requested, finish, attempt.Deadline())
	if err != nil {
		t.Fatal(err)
	}
	cancelled.kind = DeliveryCommandArbitrateAttemptDeadline
	cancelled.proof = digestDeliveryCommand(command)
	cancelledResult, err := NewApplyResult(attempt.Deadline(), applied, cancelled)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateApplyResult(description, request, cancelledResult); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged cooperative arbitration = %v", err)
	}
	shifted := terminatedResult
	shifted.observedAt = shifted.observedAt.Add(time.Nanosecond)
	if _, err := ValidateApplyResult(description, request, shifted); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("shifted termination observation = %v", err)
	}
}

func TestApplyResultRejectsForgedTimeoutLimitPrecedence(t *testing.T) {
	policy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(time.Minute+time.Second), Retries(0), RetryBackoff(Exponential(2*time.Second, 2*time.Second, NoJitter)))
	invocation := testInvocationForPolicy(t, policy)
	description := queueTestBackendDescription(1)
	lease := deliveryTestLease(t, invocation.ID(), []byte("timeout-precedence"))
	begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	running, err := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	attempt, _ := running.Attempt()
	command, _ := ArbitrateAttemptDeadlineCommand(lease, 2*time.Second)
	request, _ := NewApplyRequest(command)
	application, err := ApplyDeliveryCommand(running.Invocation(), command, attempt.Deadline())
	if err != nil || application.Invocation().Outcome().TerminalReason() != ReasonRetryExhausted {
		t.Fatalf("baseline precedence = (%v, %v)", application, err)
	}
	ledger := *application.invocation.history
	ledger.value.terminalReason = ReasonMaxElapsed
	ledger.value.availableAt = invocation.MaxElapsedAt()
	application.invocation.history = &ledger
	applied, _ := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlNone)
	result, err := NewApplyResult(attempt.Deadline(), applied, application)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateApplyResult(description, request, result); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged max elapsed precedence = %v", err)
	}
}

func TestAppliedResultValidationReadsLatestAttemptWithoutLedgerCopy(t *testing.T) {
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	running, attempt, err := invocation.BeginAttempt(BeginAttemptSpec{Binding: testBindingName(t), Build: testBuildID(t), StartedAt: invocation.EligibleAt()})
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	allocations := testing.AllocsPerRun(100, func() {
		matched = latestAttemptIs(running, attempt)
	})
	if !matched || allocations != 0 {
		t.Fatalf("latest attempt lookup = (%v, %v allocations)", matched, allocations)
	}
}

func TestApplyResultAcceptsOnlyClosedFinishArbitrations(t *testing.T) {
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	description := queueTestBackendDescription(1)
	lease := deliveryTestLease(t, invocation.ID(), []byte("finish"))
	begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	started, err := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	running := started.Invocation()
	attempt, ok := started.Attempt()
	if !ok {
		t.Fatal("begin did not mint attempt")
	}
	success, _ := FinishAttemptCommand(lease, SuccessDisposition(), 0, DefaultRetryDelay)
	request, _ := NewApplyRequest(success)
	applied, _ := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlNone)
	succeeded, err := ApplyDeliveryCommand(running, success, attempt.StartedAt().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	succeededResult, _ := NewApplyResult(attempt.StartedAt().Add(time.Second), applied, succeeded)
	if _, err := ValidateApplyResult(description, request, succeededResult); err != nil {
		t.Fatalf("success = %v", err)
	}
	retryDisposition, _ := RetryDisposition(ReasonHandlerFailure, PublicFailure{}, DefaultRetryDelay, RetryCostCharged)
	retry, _ := FinishAttemptCommand(lease, retryDisposition, DefaultRetryDelay, DefaultRetryDelay)
	retried, err := ApplyDeliveryCommand(running, retry, attempt.StartedAt().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	wrongResult, _ := NewApplyResult(attempt.StartedAt().Add(time.Second), applied, retried)
	if _, err := ValidateApplyResult(description, request, wrongResult); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("wrong same-kind finish = %v", err)
	}
	timedOut, err := ApplyDeliveryCommand(running, success, attempt.Deadline())
	if err != nil {
		t.Fatal(err)
	}
	timeoutResult, _ := NewApplyResult(attempt.Deadline(), applied, timedOut)
	validated, err := ValidateApplyResult(description, request, timeoutResult)
	timeoutAttempt, timeoutOK := validated.Application().Attempt()
	if err != nil || !timeoutOK || timeoutAttempt.Disposition().Kind() != DispositionRetry || timeoutAttempt.Disposition().Reason() != ReasonAttemptTimeout || timeoutAttempt.Disposition().RetryCost() != RetryCostCharged {
		t.Fatalf("timeout arbitration = (%v, %v)", validated, err)
	}
	cancelledInvocation, err := running.RequestCancel(attempt.StartedAt().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := ApplyDeliveryCommand(cancelledInvocation, success, attempt.StartedAt().Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	appliedCancel, _ := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlCancelRequested)
	cancelledResult, _ := NewApplyResult(attempt.StartedAt().Add(2*time.Second), appliedCancel, cancelled)
	validated, err = ValidateApplyResult(description, request, cancelledResult)
	cancelledAttempt, cancelledOK := validated.Application().Attempt()
	if err != nil || !cancelledOK || cancelledAttempt.Disposition().Kind() != DispositionCancelled || validated.Application().Invocation().State() != InvocationCancelled {
		t.Fatalf("cancellation arbitration = (%v, %v)", validated, err)
	}
	hiddenCancelResult, _ := NewApplyResult(attempt.StartedAt().Add(2*time.Second), applied, cancelled)
	if _, err := ValidateApplyResult(description, request, hiddenCancelResult); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("hidden cancellation = %v", err)
	}
}

func TestApplyResultValidatesProgressAndDeliveryPostconditions(t *testing.T) {
	description := queueTestBackendDescription(1)
	applied, _ := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlNone)
	progressInvocation := testInvocationForPolicy(t, testInvocationPolicy(t, ProgressTimeout(time.Minute)))
	progressLease := deliveryTestLease(t, progressInvocation.ID(), []byte("progress"))
	begin, _ := BeginAttemptCommand(progressLease, testBindingName(t), testBuildID(t))
	started, err := ApplyDeliveryCommand(progressInvocation, begin, progressInvocation.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	progress, _ := ProgressCommand(progressLease)
	progressRequest, _ := NewApplyRequest(progress)
	progressed, err := ApplyDeliveryCommand(started.Invocation(), progress, progressInvocation.EligibleAt().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	progressResult, _ := NewApplyResult(progressInvocation.EligibleAt().Add(time.Second), applied, progressed)
	if _, err := ValidateApplyResult(description, progressRequest, progressResult); err != nil {
		t.Fatalf("progress = %v", err)
	}
	forgedProgress := progressResult
	forgedProgress.application.attempt.progressDeadline = forgedProgress.application.attempt.progressDeadline.Add(time.Second)
	if _, err := ValidateApplyResult(description, progressRequest, forgedProgress); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("forged progress = %v", err)
	}
	cancelRequested, err := started.Invocation().RequestCancel(progressInvocation.EligibleAt().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	cancelProgressed, err := ApplyDeliveryCommand(cancelRequested, progress, progressInvocation.EligibleAt().Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	appliedCancel, _ := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlCancelRequested)
	cancelProgressResult, _ := NewApplyResult(progressInvocation.EligibleAt().Add(2*time.Second), appliedCancel, cancelProgressed)
	if _, err := ValidateApplyResult(description, progressRequest, cancelProgressResult); err != nil {
		t.Fatalf("cancel progress = %v", err)
	}
	hiddenCancelProgress, _ := NewApplyResult(progressInvocation.EligibleAt().Add(2*time.Second), applied, cancelProgressed)
	if _, err := ValidateApplyResult(description, progressRequest, hiddenCancelProgress); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("hidden progress cancellation = %v", err)
	}

	queued := testInvocationForPolicy(t, testInvocationPolicy(t, MaxDeliveryDeferrals(1)))
	lease := deliveryTestLease(t, queued.ID(), []byte("delivery"))
	deferCommand, _ := DeferDeliveryCommand(lease, ReasonAdmission, PublicFailure{}, MinRetryDelay)
	deferRequest, _ := NewApplyRequest(deferCommand)
	deferred, err := ApplyDeliveryCommand(queued, deferCommand, queued.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	deferResult, _ := NewApplyResult(queued.EligibleAt(), applied, deferred)
	if _, err := ValidateApplyResult(description, deferRequest, deferResult); err != nil {
		t.Fatalf("defer = %v", err)
	}
	otherDefer, _ := DeferDeliveryCommand(lease, ReasonDependency, PublicFailure{}, MinRetryDelay)
	wrongDeferred, err := ApplyDeliveryCommand(queued, otherDefer, queued.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	wrongDeferResult, _ := NewApplyResult(queued.EligibleAt(), applied, wrongDeferred)
	if _, err := ValidateApplyResult(description, deferRequest, wrongDeferResult); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("wrong defer source = %v", err)
	}

	finishCommand, _ := FinishDeliveryCommand(lease, InvocationQuarantined, ReasonCompatibility, PublicFailure{})
	finishRequest, _ := NewApplyRequest(finishCommand)
	finished, err := ApplyDeliveryCommand(queued, finishCommand, queued.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	finishResult, _ := NewApplyResult(queued.EligibleAt(), applied, finished)
	if _, err := ValidateApplyResult(description, finishRequest, finishResult); err != nil {
		t.Fatalf("finish delivery = %v", err)
	}
	deadlineFinished, err := ApplyDeliveryCommand(queued, finishCommand, queued.MaxElapsedAt())
	if err != nil {
		t.Fatal(err)
	}
	deadlineResult, _ := NewApplyResult(queued.MaxElapsedAt(), applied, deadlineFinished)
	validated, err := ValidateApplyResult(description, finishRequest, deadlineResult)
	if err != nil || validated.Application().Invocation().State() != InvocationDead || validated.Application().Invocation().Outcome().TerminalReason() != ReasonMaxElapsed {
		t.Fatalf("finish deadline arbitration = (%v, %v)", validated, err)
	}
	otherFinish, _ := FinishDeliveryCommand(lease, InvocationDiscarded, ReasonPayload, PublicFailure{})
	wrongFinished, err := ApplyDeliveryCommand(queued, otherFinish, queued.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	wrongFinishResult, _ := NewApplyResult(queued.EligibleAt(), applied, wrongFinished)
	if _, err := ValidateApplyResult(description, finishRequest, wrongFinishResult); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("wrong delivery terminal = %v", err)
	}

	release, _ := ReleaseUnchangedCommand(lease, testBindingName(t), testBuildID(t), MinRetryDelay)
	releaseRequest, _ := NewApplyRequest(release)
	released, err := ApplyDeliveryCommand(queued, release, queued.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	releaseResult, _ := NewApplyResult(queued.EligibleAt(), applied, released)
	if _, err := ValidateApplyResult(description, releaseRequest, releaseResult); err != nil {
		t.Fatalf("release = %v", err)
	}
	deadlineReleased, err := ApplyDeliveryCommand(queued, release, queued.MaxElapsedAt())
	if err != nil {
		t.Fatal(err)
	}
	deadlineReleaseResult, _ := NewApplyResult(queued.MaxElapsedAt(), applied, deadlineReleased)
	validated, err = ValidateApplyResult(description, releaseRequest, deadlineReleaseResult)
	if err != nil || validated.Application().Invocation().State() != InvocationDead || validated.Application().Invocation().Outcome().Reason() != ReasonCompatibility {
		t.Fatalf("release deadline arbitration = (%v, %v)", validated, err)
	}
}

func TestRecoverReturnsBoundedNewlyFencedRecordsForCoreSettlement(t *testing.T) {
	_, _, invocation, _, record := deliveryRecordFixture(t, PlacementRegular)
	request, err := NewRecoverRequest(RecoverRequestSpec{Namespace: record.Genesis.Namespace, Incarnation: driverTestWorkerIncarnation(t), MaxItems: 2, MaxBytes: MaxDeliveryRecordBytes, LeaseTTL: DefaultLeaseTTL})
	if err != nil {
		t.Fatal(err)
	}
	corrupt := cloneDeliveryRecord(record)
	corrupt.Outcomes = nil
	lease := deliveryTestLease(t, invocation.ID(), []byte("recovery-fence"))
	transferredPayload := corrupt.Payload.Data
	item, err := TakeRecoveredDelivery(lease, &corrupt)
	if err != nil || !corrupt.Genesis.ID.IsZero() || corrupt.Payload.Data != nil || &transferredPayload[0] != &item.record.value.Payload.Data[0] {
		t.Fatalf("recovery ownership transfer = %v", err)
	}
	result, err := NewRecoverResult(record.Genesis.EligibleAt, []RecoveredDelivery{item}, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := ValidateRecoverResult(queueTestBackendDescription(1), request, result)
	if err != nil || len(validated.Items()) != 1 || validated.Released() != 1 || !validated.More() {
		t.Fatalf("recovery = (%v, %v)", validated, err)
	}
	if item.record != result.items[0].record || item.record != validated.items[0].record || item.record != validated.Items()[0].record {
		t.Fatal("recover boundary amplified delivery record copies")
	}
	items := validated.Items()
	publicRecord := items[0].Record()
	publicRecord.Payload.Data[0] = 'X'
	publicLease := items[0].Lease()
	publicLease.token[0] = 'X'
	if string(validated.Items()[0].Record().Payload.Data) != "secret payload" || string(validated.Items()[0].Lease().DriverToken()) != "recovery-fence" {
		t.Fatal("recovery retained returned record or token")
	}
	secondLease := deliveryTestLease(t, queueTestInvocationID(t, 84), []byte("second-fence"))
	second, _ := NewRecoveredDelivery(secondLease, record)
	overBudget, _ := NewRecoverResult(result.ObservedAt(), []RecoveredDelivery{item, second}, 1, false)
	if _, err := ValidateRecoverResult(queueTestBackendDescription(1), request, overBudget); !errors.Is(err, ErrDriver) {
		t.Fatalf("recovery touched-row budget = %v", err)
	}
	if _, err := NewRecoverResult(result.ObservedAt(), nil, 0, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-progressing more = %v", err)
	}
	wrongBackendLease, _ := NewLeaseRef(queueTestBackendID(2), invocation.ID(), []byte("wrong"))
	wrongBackend, _ := NewRecoveredDelivery(wrongBackendLease, record)
	wrongResult, _ := NewRecoverResult(result.ObservedAt(), []RecoveredDelivery{wrongBackend}, 0, false)
	if _, err := ValidateRecoverResult(queueTestBackendDescription(1), request, wrongResult); !errors.Is(err, ErrDriver) {
		t.Fatalf("recovery backend = %v", err)
	}
	if _, err := NewRecoverRequest(RecoverRequestSpec{Namespace: record.Genesis.Namespace, Incarnation: driverTestWorkerIncarnation(t), MaxItems: 1, MaxBytes: MaxDeliveryRecordBytes - 1, LeaseTTL: DefaultLeaseTTL}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unrecoverable byte cap = %v", err)
	}
	if _, err := TakeRecoveredDelivery(lease, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil recovery transfer = %v", err)
	}
	for _, value := range []any{request, item, result} {
		if _, err := json.Marshal(value); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("JSON %T = %v", value, err)
		}
	}
}

func driverClaimTarget(t *testing.T, name Name, codec CodecID, version SchemaVersion, available int, binding string) ClaimTarget {
	t.Helper()
	revision, err := NewPayloadRevision(codec, version)
	if err != nil {
		t.Fatal(err)
	}
	target, err := NewClaimTarget(ClaimTargetSpec{Definition: name, Binding: mustDriverBinding(t, binding), Build: testBuildID(t), SupportedRevisions: []PayloadRevision{revision}, Available: available})
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func mustClaimRequest(t *testing.T, namespace Namespace, targets []ClaimTarget, maxItems, maxBytes int) ClaimRequest {
	t.Helper()
	request, err := NewClaimRequest(ClaimRequestSpec{Namespace: namespace, Incarnation: driverTestWorkerIncarnation(t), Targets: targets, MaxItems: maxItems, MaxBytes: maxBytes, LeaseTTL: DefaultLeaseTTL})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func mustDriverBinding(t *testing.T, raw string) BindingName {
	t.Helper()
	binding, err := ParseBindingName(raw)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func driverTestWorkerIncarnation(t *testing.T) WorkerIncarnation {
	t.Helper()
	var value [WorkerIncarnationBytes]byte
	value[0] = 1
	incarnation, err := WorkerIncarnationFromBytes(value)
	if err != nil {
		t.Fatal(err)
	}
	return incarnation
}
