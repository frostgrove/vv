package jobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestLeaseRefIsBoundedCopiedAndRedacted(t *testing.T) {
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	token := []byte("driver lease secret")
	lease := deliveryTestLease(t, invocation.ID(), token)
	token[0] = 'X'
	copyOut := lease.DriverToken()
	copyOut[0] = 'Y'
	if string(lease.DriverToken()) != "driver lease secret" || lease.Backend().IsZero() || lease.InvocationID() != invocation.ID() {
		t.Fatalf("lease = %+v", lease)
	}
	if bytes.Contains([]byte(fmt.Sprintf("%+v", lease)), []byte("secret")) {
		t.Fatal("lease formatting exposed driver token")
	}
	if _, err := json.Marshal(lease); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("lease JSON = %v", err)
	}
	if _, err := NewLeaseRef(lease.Backend(), invocation.ID(), nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty token = %v", err)
	}
	if _, err := NewLeaseRef(lease.Backend(), invocation.ID(), make([]byte, MaxLeaseTokenBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized token = %v", err)
	}
	tampered := cloneLeaseRef(lease)
	tampered.token[0] ^= 1
	if tampered.valid() {
		t.Fatal("tampered lease retained its binding")
	}
}

func TestDeliveryCommandsAreClosedFencedAndTimestampFree(t *testing.T) {
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	lease := deliveryTestLease(t, invocation.ID(), []byte("lease"))
	binding := testBindingName(t)
	build := testBuildID(t)
	success := SuccessDisposition()
	retry, err := RetryDisposition(ReasonHandlerFailure, PublicFailure{}, DefaultRetryDelay, RetryCostCharged)
	if err != nil {
		t.Fatal(err)
	}
	commands := []DeliveryCommand{}
	constructors := []func() (DeliveryCommand, error){
		func() (DeliveryCommand, error) { return BeginAttemptCommand(lease, binding, build) },
		func() (DeliveryCommand, error) { return ProgressCommand(lease) },
		func() (DeliveryCommand, error) { return FinishAttemptCommand(lease, success, 0) },
		func() (DeliveryCommand, error) { return FinishAttemptCommand(lease, retry, DefaultRetryDelay) },
		func() (DeliveryCommand, error) {
			return DeferDeliveryCommand(lease, ReasonAdmission, PublicFailure{}, MinRetryDelay)
		},
		func() (DeliveryCommand, error) {
			return FinishDeliveryCommand(lease, InvocationQuarantined, ReasonCompatibility, PublicFailure{})
		},
		func() (DeliveryCommand, error) {
			return ReleaseUnchangedCommand(lease, binding, build, MinRetryDelay)
		},
		func() (DeliveryCommand, error) { return RejectCorruptCommand(lease) },
	}
	for index, constructor := range constructors {
		command, commandErr := constructor()
		if commandErr != nil || !command.Kind().Valid() || command.Lease().InvocationID() != invocation.ID() {
			t.Fatalf("command %d = (%v, %v)", index, command, commandErr)
		}
		commands = append(commands, command)
		if _, marshalErr := json.Marshal(command); !errors.Is(marshalErr, ErrUnsupported) {
			t.Fatalf("command %d JSON = %v", index, marshalErr)
		}
	}
	kinds := make(map[DeliveryCommandKind]struct{}, len(commands))
	for _, command := range commands {
		kinds[command.Kind()] = struct{}{}
	}
	if len(kinds) != int(DeliveryCommandRejectCorrupt) {
		t.Fatalf("closed command kinds = %d", len(kinds))
	}
	timeType := reflect.TypeFor[time.Time]()
	commandType := reflect.TypeFor[DeliveryCommand]()
	for index := 0; index < commandType.NumField(); index++ {
		if commandType.Field(index).Type == timeType {
			t.Fatalf("command carries absolute time in field %s", commandType.Field(index).Name)
		}
	}
	if _, err := FinishAttemptCommand(lease, success, MinRetryDelay); !errors.Is(err, ErrInvalid) {
		t.Fatalf("successful delay = %v", err)
	}
	if _, err := FinishAttemptCommand(lease, retry, MinRetryDelay); !errors.Is(err, ErrInvalid) {
		t.Fatalf("retry below RetryAfter = %v", err)
	}
	deferred, err := DeferredDisposition(PublicFailure{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FinishAttemptCommand(lease, deferred, 2*time.Second); !errors.Is(err, ErrInvalid) {
		t.Fatalf("handler deferral mismatch = %v", err)
	}
	if _, err := DeferDeliveryCommand(lease, ReasonAdmission, PublicFailure{}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero delivery deferral = %v", err)
	}
}

func TestApplyDeliveryCommandUsesAuthoritativeRelativeTimeAndStateMachine(t *testing.T) {
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	lease := deliveryTestLease(t, invocation.ID(), []byte("lease"))
	begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	startedAt := invocation.EligibleAt()
	started, err := ApplyDeliveryCommand(invocation, begin, startedAt)
	if err != nil || !started.Changed() || started.Invocation().State() != InvocationRunning {
		t.Fatalf("begin = (%v, %v)", started, err)
	}
	attempt, ok := started.Attempt()
	if !ok || attempt.StartedAt() != startedAt {
		t.Fatalf("attempt = (%v, %v)", attempt, ok)
	}
	success, _ := FinishAttemptCommand(lease, SuccessDisposition(), 0)
	finishedAt := startedAt.Add(time.Second)
	finished, err := ApplyDeliveryCommand(started.Invocation(), success, finishedAt)
	if err != nil || !finished.Changed() || finished.Invocation().State() != InvocationSucceeded || finished.Invocation().FinishedAt() != finishedAt {
		t.Fatalf("finish = (%v, %v)", finished, err)
	}
	if _, err := ApplyDeliveryCommand(invocation, begin, time.Time{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero authoritative time = %v", err)
	}
	otherID := queueTestInvocationID(t, 74)
	otherLease := deliveryTestLease(t, otherID, []byte("other"))
	otherBegin, _ := BeginAttemptCommand(otherLease, testBindingName(t), testBuildID(t))
	if _, err := ApplyDeliveryCommand(invocation, otherBegin, startedAt); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("cross-invocation lease = %v", err)
	}
}

func TestApplyDeliveryCommandSupportsRelativeRescheduleAndStorageDecisions(t *testing.T) {
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	lease := deliveryTestLease(t, invocation.ID(), []byte("lease"))
	begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	started, err := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	retryDisposition, err := RetryDisposition(ReasonHandlerFailure, PublicFailure{}, DefaultRetryDelay, RetryCostCharged)
	if err != nil {
		t.Fatal(err)
	}
	retry, _ := FinishAttemptCommand(lease, retryDisposition, DefaultRetryDelay)
	finishedAt := invocation.EligibleAt().Add(time.Second)
	rescheduled, err := ApplyDeliveryCommand(started.Invocation(), retry, finishedAt)
	if err != nil || rescheduled.Invocation().State() != InvocationQueued || rescheduled.Invocation().EligibleAt() != invocation.EligibleAt() || rescheduled.Invocation().Outcome().AvailableAt() != finishedAt.Add(DefaultRetryDelay) {
		t.Fatalf("retry = (%v, %v)", rescheduled, err)
	}

	_, _, queued, _, _ := deliveryRecordFixture(t, PlacementRegular)
	queuedLease := deliveryTestLease(t, queued.ID(), []byte("defer"))
	deferCommand, _ := DeferDeliveryCommand(queuedLease, ReasonAdmission, PublicFailure{}, MinRetryDelay)
	deferred, err := ApplyDeliveryCommand(queued, deferCommand, queued.EligibleAt())
	if err != nil || deferred.Invocation().Outcome().AvailableAt() != queued.EligibleAt().Add(MinRetryDelay) {
		t.Fatalf("defer = (%v, %v)", deferred, err)
	}
	release, _ := ReleaseUnchangedCommand(queuedLease, testBindingName(t), testBuildID(t), MinRetryDelay)
	released, err := ApplyDeliveryCommand(queued, release, queued.EligibleAt())
	physicalRelease, releasedOK := released.Release()
	if err != nil || released.Changed() || !released.RequiresFence() || !releasedOK || physicalRelease.AvailableAt() != queued.EligibleAt().Add(MinRetryDelay) || physicalRelease.ExcludedBinding() != testBindingName(t) || physicalRelease.ExcludedBuild() != testBuildID(t) || released.Invocation().Outcome() != queued.Outcome() || released.Invocation().AttemptOrdinal().Value() != 0 {
		t.Fatalf("release = (%v, %v)", released, err)
	}
	terminal, _ := FinishDeliveryCommand(queuedLease, InvocationQuarantined, ReasonCompatibility, PublicFailure{})
	quarantined, err := ApplyDeliveryCommand(queued, terminal, queued.EligibleAt())
	if err != nil || quarantined.Invocation().State() != InvocationQuarantined {
		t.Fatalf("terminal = (%v, %v)", quarantined, err)
	}
	reject, _ := RejectCorruptCommand(queuedLease)
	rejected, err := ApplyDeliveryCommand(Invocation{}, reject, queued.EligibleAt())
	if err != nil || !rejected.Changed() || rejected.Kind() != DeliveryCommandRejectCorrupt || !rejected.Invocation().IsZero() {
		t.Fatalf("reject = (%v, %v)", rejected, err)
	}
	if _, err := ApplyDeliveryCommand(queued, reject, queued.EligibleAt()); !errors.Is(err, ErrConflict) {
		t.Fatalf("reject restored delivery = %v", err)
	}
}

func TestCompatibilityReleasePersistsTerminalDeadlineInsteadOfReleasing(t *testing.T) {
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	lease := deliveryTestLease(t, invocation.ID(), []byte("lease"))
	release, err := ReleaseUnchangedCommand(lease, testBindingName(t), testBuildID(t), MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	application, err := ApplyDeliveryCommand(invocation, release, invocation.MaxElapsedAt())
	if err != nil || !application.Changed() || application.Invocation().State() != InvocationDead || application.Invocation().Outcome().TerminalReason() != ReasonMaxElapsed {
		t.Fatalf("deadline release = (%v, %v)", application, err)
	}
	if physical, ok := application.Release(); ok || !physical.IsZero() {
		t.Fatalf("terminal release exposed physical decision: %v", physical)
	}
}

func TestCompatibilityReleaseTerminalOutcomeRoundTripsAcrossDeadlines(t *testing.T) {
	catalog, _, invocation, payload, base := deliveryRecordFixture(t, PlacementRegular)
	tests := []struct {
		name       string
		invocation Invocation
		now        time.Time
		reason     Reason
	}{
		{name: "max elapsed", invocation: invocation, now: invocation.MaxElapsedAt().Add(-MinRetryDelay / 2), reason: ReasonMaxElapsed},
		func() struct {
			name       string
			invocation Invocation
			now        time.Time
			reason     Reason
		} {
			record := cloneDeliveryRecord(base)
			record.Genesis.StartBefore = record.Genesis.EligibleAt.Add(MinRetryDelay / 2)
			restored, err := RestoreDeliveryRecord(catalog, record)
			if err != nil {
				t.Fatal(err)
			}
			return struct {
				name       string
				invocation Invocation
				now        time.Time
				reason     Reason
			}{name: "start before", invocation: restored.Invocation(), now: record.Genesis.EligibleAt, reason: ReasonStartBefore}
		}(),
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lease := deliveryTestLease(t, test.invocation.ID(), []byte("lease"))
			command, err := ReleaseUnchangedCommand(lease, testBindingName(t), testBuildID(t), MinRetryDelay)
			if err != nil {
				t.Fatal(err)
			}
			application, err := ApplyDeliveryCommand(test.invocation, command, test.now)
			if err != nil || !application.Changed() || application.Invocation().State() != InvocationDead || application.Invocation().Outcome().TerminalReason() != test.reason || application.Invocation().Outcome().AvailableAt().IsZero() {
				t.Fatalf("application = (%v, %v)", application, err)
			}
			record, err := NewDeliveryRecord(application.Invocation(), payload, digestWirePayload(payload), PayloadDigest{})
			if err != nil {
				t.Fatal(err)
			}
			restored, err := RestoreDeliveryRecord(catalog, record)
			if err != nil || restored.Invocation().State() != InvocationDead || restored.Invocation().Outcome() != application.Invocation().Outcome() {
				t.Fatalf("round trip = (%v, %v)", restored, err)
			}
		})
	}
}

func TestCompatibilityDeferralFailureRoundTripsAcrossDeadline(t *testing.T) {
	catalog, _, invocation, payload, _ := deliveryRecordFixture(t, PlacementRegular)
	code, err := ParseFailureCode("compatibility.defer")
	if err != nil {
		t.Fatal(err)
	}
	failure, err := NewPublicFailure(code, "public compatibility failure")
	if err != nil {
		t.Fatal(err)
	}
	lease := deliveryTestLease(t, invocation.ID(), []byte("lease"))
	command, err := DeferDeliveryCommand(lease, ReasonCompatibility, failure, MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	now := invocation.MaxElapsedAt().Add(-MinRetryDelay / 2)
	application, err := ApplyDeliveryCommand(invocation, command, now)
	if err != nil || application.Invocation().State() != InvocationDead || application.Invocation().Outcome().Failure() != failure || application.Invocation().Outcome().AvailableAt().IsZero() {
		t.Fatalf("application = (%v, %v)", application, err)
	}
	record, err := NewDeliveryRecord(application.Invocation(), payload, digestWirePayload(payload), PayloadDigest{})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreDeliveryRecord(catalog, record)
	if err != nil || restored.Invocation().Outcome() != application.Invocation().Outcome() {
		t.Fatalf("round trip = (%v, %v)", restored, err)
	}
}

func TestCompatibilityReleaseDoesNotConsumeDeliveryDeferralBudget(t *testing.T) {
	policy := testInvocationPolicy(t, MaxDeliveryDeferrals(0))
	invocation := testInvocationForPolicy(t, policy)
	lease := deliveryTestLease(t, invocation.ID(), []byte("lease"))
	release, err := ReleaseUnchangedCommand(lease, testBindingName(t), testBuildID(t), MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	application, err := ApplyDeliveryCommand(invocation, release, invocation.EligibleAt())
	physical, ok := application.Release()
	if err != nil || application.Changed() || !ok || physical.AvailableAt() != invocation.EligibleAt().Add(MinRetryDelay) || application.Invocation().DeliveryDeferrals().Value() != 0 || len(application.Invocation().History()) != 1 {
		t.Fatalf("zero-budget compatibility release = (%v, %v)", application, err)
	}
}

func TestIncompatibleRestoredDeliveryCanBeFencedWithoutStartingAttempt(t *testing.T) {
	_, _, _, _, record := deliveryRecordFixture(t, PlacementRegular)
	restored, err := RestoreDeliveryRecord(Catalog{}, record)
	if err != nil || restored.IsZero() || restored.Compatibility() != DeliveryDefinitionUnavailable {
		t.Fatalf("restore = (%v, %v)", restored, err)
	}
	invocation := restored.Invocation()
	lease := deliveryTestLease(t, invocation.ID(), []byte("lease"))
	command, err := ReleaseUnchangedCommand(lease, testBindingName(t), testBuildID(t), MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	application, err := ApplyDeliveryCommand(invocation, command, invocation.EligibleAt())
	release, ok := application.Release()
	if err != nil || application.Changed() || !ok || release.AvailableAt() != invocation.EligibleAt().Add(MinRetryDelay) || application.Invocation().AttemptOrdinal().Value() != 0 || len(application.Invocation().Attempts()) != 0 || application.Invocation().DeliveryDeferrals().Value() != 0 || len(application.Invocation().History()) != len(invocation.History()) || application.Invocation().Outcome() != invocation.Outcome() {
		t.Fatalf("compatibility release = (%v, %v)", application, err)
	}
}

func TestDeliveryCommandResultIsClosedAndRedacted(t *testing.T) {
	mutations := []DeliveryMutationStatus{DeliveryMutationApplied, DeliveryMutationLeaseLost, DeliveryMutationAmbiguous}
	controls := []DeliveryControlStatus{DeliveryControlNone, DeliveryControlCancelRequested, DeliveryControlTerminated}
	for _, mutation := range mutations {
		for _, control := range controls {
			result, err := NewDeliveryCommandResult(mutation, control)
			if err != nil || result.Mutation() != mutation || result.Control() != control || result.IsZero() || mutation.String() == "unknown" || control.String() == "unknown" {
				t.Fatalf("result %d/%d = (%v, %v)", mutation, control, result, err)
			}
			if _, err := json.Marshal(result); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("result JSON = %v", err)
			}
		}
	}
	if _, err := NewDeliveryCommandResult(0, DeliveryControlNone); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero mutation = %v", err)
	}
	if _, err := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlStatus(99)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid control = %v", err)
	}
}

func deliveryTestLease(t *testing.T, invocation InvocationID, token []byte) LeaseRef {
	t.Helper()
	var backendValue [BackendIDBytes]byte
	backendValue[0] = 1
	backend, err := BackendIDFromBytes(backendValue)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := NewLeaseRef(backend, invocation, token)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}
