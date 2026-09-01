package jobs

import (
	"errors"
	"testing"
	"time"
)

func TestShutdownReleaseReturnsClaimWithoutChangingInvocation(t *testing.T) {
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	lease := deliveryTestLease(t, invocation.ID(), []byte("shutdown"))
	command, err := ReleaseForShutdownCommand(lease, MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	application, err := ApplyDeliveryCommand(invocation, command, invocation.EligibleAt())
	release, ok := application.Release()
	if err != nil || application.Changed() || !ok || release.Reason() != ReasonShutdown || !release.ExcludedBinding().IsZero() || !release.ExcludedBuild().IsZero() || release.AvailableAt() != invocation.EligibleAt().Add(MinRetryDelay) {
		t.Fatalf("shutdown release = (%v, %v, %v)", application, release, err)
	}
	if application.Invocation().Outcome() != invocation.Outcome() || application.Invocation().AttemptOrdinal() != invocation.AttemptOrdinal() || application.Invocation().DeliveryDeferrals() != invocation.DeliveryDeferrals() || len(application.Invocation().History()) != len(invocation.History()) {
		t.Fatal("shutdown release changed invocation")
	}
}

func TestShutdownReleaseTerminalOutcomeRoundTrips(t *testing.T) {
	catalog, _, invocation, payload, _ := deliveryRecordFixture(t, PlacementRegular)
	lease := deliveryTestLease(t, invocation.ID(), []byte("shutdown-deadline"))
	command, err := ReleaseForShutdownCommand(lease, MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	application, err := ApplyDeliveryCommand(invocation, command, invocation.MaxElapsedAt().Add(-MinRetryDelay/2))
	if err != nil || !application.Changed() || application.Invocation().State() != InvocationDead || application.Invocation().Outcome().Reason() != ReasonShutdown || application.Invocation().Outcome().TerminalReason() != ReasonMaxElapsed || application.Invocation().Outcome().AvailableAt().IsZero() {
		t.Fatalf("deadline shutdown release = (%v, %v)", application, err)
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

func TestReleaseAtCanonicalUpperDeadlineDoesNotMaterializeDelay(t *testing.T) {
	policy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(2*time.Minute), RetryBackoff(Exponential(time.Second, time.Minute, NoJitter)))
	spec := testInvocationSpec(t, policy)
	spec.CreatedAt = time.Date(9999, 12, 31, 23, 57, 0, 0, time.UTC)
	spec.EligibleAt = spec.CreatedAt
	invocation, err := NewInvocation(spec)
	if err != nil {
		t.Fatal(err)
	}
	lease := deliveryTestLease(t, invocation.ID(), []byte("upper-deadline"))
	commands := []DeliveryCommand{}
	compatibility, err := ReleaseUnchangedCommand(lease, testBindingName(t), testBuildID(t), MaxRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	shutdown, err := ReleaseForShutdownCommand(lease, MaxRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	commands = append(commands, compatibility, shutdown)
	for _, command := range commands {
		application, applyErr := ApplyDeliveryCommand(invocation, command, invocation.MaxElapsedAt())
		outcome := application.Invocation().Outcome()
		if applyErr != nil || !application.Changed() || application.Invocation().State() != InvocationDead || outcome.Reason() != command.Reason() || outcome.TerminalReason() != ReasonMaxElapsed || !outcome.AvailableAt().IsZero() {
			t.Fatalf("upper deadline %s = (%v, %v)", command.Reason(), application, applyErr)
		}
	}
}

func TestReleaseCrossingCanonicalUpperDeadlineUsesDeadline(t *testing.T) {
	for _, terminal := range []Reason{ReasonMaxElapsed, ReasonStartBefore} {
		t.Run(terminal.String(), func(t *testing.T) {
			policy := testPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(3*time.Minute), RetryBackoff(Exponential(time.Second, time.Minute, NoJitter)))
			snapshot, err := NewPolicySnapshot(policy)
			if err != nil {
				t.Fatal(err)
			}
			spec := testInvocationSpec(t, snapshot)
			spec.CreatedAt = time.Date(9999, 12, 31, 23, 56, 50, 0, time.UTC)
			spec.EligibleAt = spec.CreatedAt
			if terminal == ReasonStartBefore {
				spec.StartBefore = time.Date(9999, 12, 31, 23, 59, 30, 0, time.UTC)
			}
			invocation, err := NewInvocation(spec)
			if err != nil {
				t.Fatal(err)
			}
			definition := MustDefine(DefinitionSpec[string]{Name: spec.Definition, Codec: String(1), Policy: policy, Partition: PartitionGlobal})
			catalog := MustCatalog(definition)
			payload, err := definition.Encode("upper payload")
			if err != nil {
				t.Fatal(err)
			}
			at := time.Date(9999, 12, 31, 23, 59, 0, 0, time.UTC)
			deadline := invocation.MaxElapsedAt()
			if terminal == ReasonStartBefore {
				deadline = invocation.StartBefore()
			}
			lease := deliveryTestLease(t, invocation.ID(), []byte("crossing-upper-deadline"))
			commands := make([]DeliveryCommand, 0, 2)
			compatibility, err := ReleaseUnchangedCommand(lease, testBindingName(t), testBuildID(t), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			shutdown, err := ReleaseForShutdownCommand(lease, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			commands = append(commands, compatibility, shutdown)
			for _, command := range commands {
				application, applyErr := ApplyDeliveryCommand(invocation, command, at)
				outcome := application.Invocation().Outcome()
				if applyErr != nil || !application.Changed() || application.Invocation().State() != InvocationDead || outcome.Reason() != command.Reason() || outcome.TerminalReason() != terminal || outcome.AvailableAt() != deadline {
					t.Fatalf("crossing upper deadline %s = (%v, %v)", command.Reason(), application, applyErr)
				}
				request, requestErr := NewApplyRequest(command)
				status, statusErr := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlNone)
				result, resultErr := NewApplyResult(at, status, application)
				if requestErr != nil || statusErr != nil || resultErr != nil {
					t.Fatalf("driver result construction = (%v, %v, %v)", requestErr, statusErr, resultErr)
				}
				if _, validateErr := ValidateApplyResult(queueTestBackendDescription(1), request, result); validateErr != nil {
					t.Fatalf("driver validation = %v", validateErr)
				}
				record, recordErr := NewDeliveryRecord(application.Invocation(), payload, digestWirePayload(payload), PayloadDigest{})
				if recordErr != nil {
					t.Fatal(recordErr)
				}
				restored, restoreErr := RestoreDeliveryRecord(catalog, record)
				if restoreErr != nil || restored.Invocation().Outcome() != outcome {
					t.Fatalf("terminal replay = (%v, %v)", restored, restoreErr)
				}
			}
		})
	}
}

func TestReleaseCrossingCanonicalUpperDeadlineWithinMinimumDelay(t *testing.T) {
	for _, terminal := range []Reason{ReasonMaxElapsed, ReasonStartBefore} {
		t.Run(terminal.String(), func(t *testing.T) {
			policy := testPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(3*time.Minute), RetryBackoff(Exponential(time.Second, time.Minute, NoJitter)))
			snapshot, err := NewPolicySnapshot(policy)
			if err != nil {
				t.Fatal(err)
			}
			spec := testInvocationSpec(t, snapshot)
			spec.CreatedAt = time.Date(9999, 12, 31, 23, 56, 59, 949_999_999, time.UTC)
			spec.EligibleAt = spec.CreatedAt
			if terminal == ReasonStartBefore {
				spec.CreatedAt = time.Date(9999, 12, 31, 23, 56, 59, 999_999_999, time.UTC)
				spec.EligibleAt = spec.CreatedAt
				spec.StartBefore = time.Date(9999, 12, 31, 23, 59, 59, 949_999_999, time.UTC)
			}
			invocation, err := NewInvocation(spec)
			if err != nil {
				t.Fatal(err)
			}
			definition := MustDefine(DefinitionSpec[string]{Name: spec.Definition, Codec: String(1), Policy: policy, Partition: PartitionGlobal})
			catalog := MustCatalog(definition)
			payload, err := definition.Encode("upper payload")
			if err != nil {
				t.Fatal(err)
			}
			deadline := invocation.MaxElapsedAt()
			if terminal == ReasonStartBefore {
				deadline = invocation.StartBefore()
			}
			at := deadline.Add(-MinRetryDelay / 2)
			lease := deliveryTestLease(t, invocation.ID(), []byte("crossing-upper-minimum"))
			compatibility, err := ReleaseUnchangedCommand(lease, testBindingName(t), testBuildID(t), MinRetryDelay+time.Nanosecond)
			if err != nil {
				t.Fatal(err)
			}
			shutdown, err := ReleaseForShutdownCommand(lease, MinRetryDelay+time.Nanosecond)
			if err != nil {
				t.Fatal(err)
			}
			for _, command := range []DeliveryCommand{compatibility, shutdown} {
				application, applyErr := ApplyDeliveryCommand(invocation, command, at)
				outcome := application.Invocation().Outcome()
				request, requestErr := NewApplyRequest(command)
				status, statusErr := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlNone)
				result, resultErr := NewApplyResult(at, status, application)
				_, validateErr := ValidateApplyResult(queueTestBackendDescription(1), request, result)
				record, recordErr := NewDeliveryRecord(application.Invocation(), payload, digestWirePayload(payload), PayloadDigest{})
				restored, restoreErr := RestoreDeliveryRecord(catalog, record)
				if applyErr != nil || requestErr != nil || statusErr != nil || resultErr != nil || validateErr != nil || recordErr != nil || restoreErr != nil || !application.Changed() || application.Invocation().State() != InvocationDead || outcome.Reason() != command.Reason() || outcome.TerminalReason() != terminal || outcome.AvailableAt() != deadline || restored.Invocation().Outcome() != outcome {
					t.Fatalf("minimum crossing %s = (application=%v, request=%v, status=%v, result=%v, validate=%v, record=%v, restore=%v)", command.Reason(), applyErr, requestErr, statusErr, resultErr, validateErr, recordErr, restoreErr)
				}
			}
		})
	}
}

func TestRestoreRejectsUnclippedMinimumReleaseDeadline(t *testing.T) {
	for _, terminal := range []Reason{ReasonMaxElapsed, ReasonStartBefore} {
		t.Run(terminal.String(), func(t *testing.T) {
			catalog, _, invocation, payload, record := deliveryRecordFixture(t, PlacementRegular)
			if terminal == ReasonStartBefore {
				record.Genesis.StartBefore = record.Genesis.EligibleAt.Add(time.Second)
				restored, err := RestoreDeliveryRecord(catalog, record)
				if err != nil {
					t.Fatal(err)
				}
				invocation = restored.Invocation()
			}
			deadline := invocation.MaxElapsedAt()
			if terminal == ReasonStartBefore {
				deadline = invocation.StartBefore()
			}
			lease := deliveryTestLease(t, invocation.ID(), []byte("forged-minimum-release"))
			compatibility, err := ReleaseUnchangedCommand(lease, testBindingName(t), testBuildID(t), MinRetryDelay)
			if err != nil {
				t.Fatal(err)
			}
			shutdown, err := ReleaseForShutdownCommand(lease, MinRetryDelay)
			if err != nil {
				t.Fatal(err)
			}
			for _, command := range []DeliveryCommand{compatibility, shutdown} {
				application, applyErr := ApplyDeliveryCommand(invocation, command, deadline.Add(-MinRetryDelay/2))
				if applyErr != nil || !application.Changed() {
					t.Fatalf("baseline = (%v, %v)", application, applyErr)
				}
				for _, availableAt := range []time.Time{deadline, deadline.Add(MinRetryDelay / 4)} {
					forged := application.Invocation()
					ledger := *forged.history
					ledger.value.availableAt = availableAt
					forged.history = &ledger
					persisted, persistErr := NewDeliveryRecord(forged, payload, digestWirePayload(payload), PayloadDigest{})
					if persistErr != nil {
						t.Fatal(persistErr)
					}
					if _, restoreErr := RestoreDeliveryRecord(catalog, persisted); !errors.Is(restoreErr, ErrCorrupt) {
						t.Fatalf("forged replay %s at %s = %v", command.Reason(), availableAt, restoreErr)
					}
				}
			}
		})
	}
}

func TestZeroAvailabilityReleaseTerminalOutcomesRoundTrip(t *testing.T) {
	for _, terminal := range []Reason{ReasonMaxElapsed, ReasonStartBefore} {
		t.Run(terminal.String(), func(t *testing.T) {
			catalog, _, base, payload, record := deliveryRecordFixture(t, PlacementRegular)
			if terminal == ReasonStartBefore {
				record.Genesis.StartBefore = record.Genesis.EligibleAt.Add(time.Second)
				var err error
				base, err = func() (Invocation, error) {
					restored, restoreErr := RestoreDeliveryRecord(catalog, record)
					return restored.Invocation(), restoreErr
				}()
				if err != nil {
					t.Fatal(err)
				}
			}
			at := base.MaxElapsedAt()
			if terminal == ReasonStartBefore {
				at = base.StartBefore()
			}
			lease := deliveryTestLease(t, base.ID(), []byte("zero-availability"))
			for _, shutdown := range []bool{false, true} {
				var command DeliveryCommand
				var err error
				if shutdown {
					command, err = ReleaseForShutdownCommand(lease, MinRetryDelay)
				} else {
					command, err = ReleaseUnchangedCommand(lease, testBindingName(t), testBuildID(t), MinRetryDelay)
				}
				if err != nil {
					t.Fatal(err)
				}
				application, err := ApplyDeliveryCommand(base, command, at)
				if err != nil || !application.Invocation().Outcome().AvailableAt().IsZero() {
					t.Fatalf("terminal application = (%v, %v)", application, err)
				}
				persisted, err := NewDeliveryRecord(application.Invocation(), payload, digestWirePayload(payload), PayloadDigest{})
				if err != nil {
					t.Fatal(err)
				}
				restored, err := RestoreDeliveryRecord(catalog, persisted)
				if err != nil || restored.Invocation().Outcome() != application.Invocation().Outcome() {
					t.Fatalf("round trip shutdown=%v = (%v, %v)", shutdown, restored, err)
				}
			}
		})
	}
}

func TestShutdownReleaseRejectsCompatibilityExclusions(t *testing.T) {
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	lease := deliveryTestLease(t, invocation.ID(), []byte("shutdown-invalid"))
	command := DeliveryCommand{kind: DeliveryCommandReleaseUnchanged, lease: lease, binding: testBindingName(t), build: testBuildID(t), reason: ReasonShutdown, delay: MinRetryDelay}
	if _, err := validateDeliveryCommand(command); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mixed shutdown release = %v", err)
	}
	command = DeliveryCommand{kind: DeliveryCommandReleaseUnchanged, lease: lease, reason: ReasonCompatibility, delay: MinRetryDelay}
	if _, err := validateDeliveryCommand(command); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty compatibility release = %v", err)
	}
}

func TestDeliveryReleaseZeroValueIncludesShutdownReason(t *testing.T) {
	if !(DeliveryRelease{}).IsZero() || (DeliveryRelease{reason: ReasonShutdown}).IsZero() {
		t.Fatal("delivery release zero value ignored hidden state")
	}
}

func TestDriverValidatesShutdownReleasePostcondition(t *testing.T) {
	description := queueTestBackendDescription(1)
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	lease := deliveryTestLease(t, invocation.ID(), []byte("shutdown-driver"))
	command, err := ReleaseForShutdownCommand(lease, MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewApplyRequest(command)
	if err != nil {
		t.Fatal(err)
	}
	application, err := ApplyDeliveryCommand(invocation, command, invocation.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	resultStatus, err := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlNone)
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewApplyResult(invocation.EligibleAt(), resultStatus, application)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateApplyResult(description, request, result); err != nil {
		t.Fatalf("valid shutdown release = %v", err)
	}
	wrong := application
	wrong.release.reason = ReasonCompatibility
	wrongResult, err := NewApplyResult(invocation.EligibleAt(), resultStatus, wrong)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateApplyResult(description, request, wrongResult); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("wrong shutdown release = %v", err)
	}
}

func TestDriverRejectsForgedTerminalReleaseAvailability(t *testing.T) {
	description := queueTestBackendDescription(1)
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	lease := deliveryTestLease(t, invocation.ID(), []byte("shutdown-forged-deadline"))
	command, err := ReleaseForShutdownCommand(lease, MinRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewApplyRequest(command)
	if err != nil {
		t.Fatal(err)
	}
	status, err := NewDeliveryCommandResult(DeliveryMutationApplied, DeliveryControlNone)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		at     time.Time
		mutate func(*InvocationOutcome)
	}{
		{name: "zero after crossing", at: invocation.MaxElapsedAt().Add(-MinRetryDelay / 2), mutate: func(outcome *InvocationOutcome) { outcome.availableAt = time.Time{} }},
		{name: "nonzero after elapsed", at: invocation.MaxElapsedAt(), mutate: func(outcome *InvocationOutcome) { outcome.availableAt = outcome.occurredAt.Add(MinRetryDelay) }},
		{name: "wrong crossing", at: invocation.MaxElapsedAt().Add(-MinRetryDelay / 2), mutate: func(outcome *InvocationOutcome) { outcome.availableAt = outcome.occurredAt.Add(MinRetryDelay / 2) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application, applyErr := ApplyDeliveryCommand(invocation, command, test.at)
			if applyErr != nil || !application.Changed() {
				t.Fatalf("baseline = (%v, %v)", application, applyErr)
			}
			ledger := *application.invocation.history
			test.mutate(&ledger.value)
			application.invocation.history = &ledger
			result, resultErr := NewApplyResult(test.at, status, application)
			if resultErr != nil {
				t.Fatal(resultErr)
			}
			if _, validateErr := ValidateApplyResult(description, request, result); !errors.Is(validateErr, ErrDriverContract) {
				t.Fatalf("forged terminal availability = %v", validateErr)
			}
		})
	}
}
