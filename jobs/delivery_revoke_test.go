package jobs

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRevokeAttemptRequeuesWithoutChargingRetryBudget(t *testing.T) {
	for _, reason := range []Reason{ReasonShutdown, ReasonLeaseLost} {
		t.Run(reason.String(), func(t *testing.T) {
			policy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(10*time.Minute), RetryBackoff(Exponential(time.Second, 4*time.Second, NoJitter)))
			invocation := testInvocationForPolicy(t, policy)
			lease := deliveryTestLease(t, invocation.ID(), []byte("revoke-running"))
			begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
			running, err := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
			if err != nil {
				t.Fatal(err)
			}
			observedAt := invocation.EligibleAt().Add(time.Second)
			command, err := RevokeAttemptCommand(lease, reason, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			application, err := ApplyDeliveryCommand(running.Invocation(), command, observedAt)
			attempt, ok := application.Attempt()
			if err != nil || !application.Changed() || !ok || application.Invocation().State() != InvocationQueued || application.Invocation().RetrySpent().Value() != 0 || attempt.Disposition().Kind() != DispositionRetry || attempt.Disposition().Reason() != reason || attempt.Disposition().RetryCost() != RetryCostNone || application.Invocation().Outcome().AvailableAt() != observedAt.Add(time.Second) {
				t.Fatalf("revoke = (%v, %v)", application, err)
			}
			request, _ := NewApplyRequest(command)
			result, _ := NewApplyResult(observedAt, mustRevokeCommandResult(t, DeliveryMutationApplied, DeliveryControlNone), application)
			validated, err := ValidateApplyResult(queueTestBackendDescription(1), request, result)
			if err != nil || !reflect.DeepEqual(validated.Application().Invocation(), application.Invocation()) {
				t.Fatalf("validated revoke = (%v, %v)", validated, err)
			}
			assertInvocationRestoreRoundTrip(t, invocation, application.Invocation())
		})
	}
}

func TestRevokeAttemptTerminatesRequestedCancellationBeforeBusinessDeadline(t *testing.T) {
	policy := testInvocationPolicy(t, AttemptTimeout(10*time.Minute), MaxElapsed(time.Hour), RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
	invocation := testInvocationForPolicy(t, policy)
	lease := deliveryTestLease(t, invocation.ID(), []byte("revoke-cancelled"))
	begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	running, err := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	cancelAt := invocation.EligibleAt().Add(time.Second)
	cancelled, err := running.Invocation().RequestCancel(cancelAt)
	if err != nil {
		t.Fatal(err)
	}
	observedAt := cancelAt.Add(time.Second)
	command, _ := RevokeAttemptCommand(lease, ReasonShutdown, time.Second)
	application, err := ApplyDeliveryCommand(cancelled, command, observedAt)
	attempt, ok := application.Attempt()
	if err != nil || !application.Changed() || !ok || application.Invocation().State() != InvocationTerminated || attempt.Disposition() != cancellationTerminatedDisposition() || application.Invocation().FinishedAt() != observedAt || application.Invocation().RetrySpent().Value() != 0 {
		t.Fatalf("cancel revocation = (%v, %v)", application, err)
	}
	request, _ := NewApplyRequest(command)
	result, _ := NewApplyResult(observedAt, mustRevokeCommandResult(t, DeliveryMutationApplied, DeliveryControlTerminated), application)
	if _, err := ValidateApplyResult(queueTestBackendDescription(1), request, result); err != nil {
		t.Fatalf("validated cancellation revocation = %v", err)
	}
	assertInvocationRestoreRoundTrip(t, invocation, application.Invocation())
}

func TestRevokeAttemptRejectsObservationBeforeCancellationProgress(t *testing.T) {
	policy := testInvocationPolicy(t, AttemptTimeout(10*time.Minute), ProgressTimeout(time.Minute), MaxElapsed(time.Hour), RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
	invocation := testInvocationForPolicy(t, policy)
	lease := deliveryTestLease(t, invocation.ID(), []byte("revoke-progress-order"))
	begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	running, _ := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
	cancelled, err := running.Invocation().RequestCancel(invocation.EligibleAt().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	attempt, _ := running.Attempt()
	progressed, _, err := cancelled.RecordProgress(attempt, invocation.EligibleAt().Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	command, _ := RevokeAttemptCommand(lease, ReasonShutdown, time.Second)
	if _, err := ApplyDeliveryCommand(progressed, command, invocation.EligibleAt().Add(2*time.Second)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("revoke before progress = %v", err)
	}
}

func TestRevokeAttemptPreservesAuthoritativeDeadlinePrecedence(t *testing.T) {
	policy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(10*time.Minute), RetryBackoff(Exponential(2*time.Second, 2*time.Second, NoJitter)))
	invocation := testInvocationForPolicy(t, policy)
	lease := deliveryTestLease(t, invocation.ID(), []byte("revoke-deadline"))
	begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	running, err := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	attempt, _ := running.Attempt()
	command, _ := RevokeAttemptCommand(lease, ReasonLeaseLost, 2*time.Second)
	application, err := ApplyDeliveryCommand(running.Invocation(), command, attempt.Deadline())
	finished, ok := application.Attempt()
	if err != nil || !ok || finished.Disposition().Reason() != ReasonAttemptTimeout || finished.Disposition().RetryCost() != RetryCostCharged || application.Invocation().RetrySpent().Value() != 1 {
		t.Fatalf("deadline revocation = (%v, %v)", application, err)
	}
	request, _ := NewApplyRequest(command)
	result, _ := NewApplyResult(attempt.Deadline(), mustRevokeCommandResult(t, DeliveryMutationApplied, DeliveryControlNone), application)
	if _, err := ValidateApplyResult(queueTestBackendDescription(1), request, result); err != nil {
		t.Fatalf("validated deadline revocation = %v", err)
	}
}

func TestRevokeAttemptUsesProgressDeadlineAndMaxElapsedPrecedence(t *testing.T) {
	t.Run("progress timeout", func(t *testing.T) {
		policy := testInvocationPolicy(t, AttemptTimeout(5*time.Minute), ProgressTimeout(time.Minute), MaxElapsed(10*time.Minute), RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
		invocation := testInvocationForPolicy(t, policy)
		lease := deliveryTestLease(t, invocation.ID(), []byte("revoke-progress-timeout"))
		begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
		running, _ := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
		attempt, _ := running.Attempt()
		command, _ := RevokeAttemptCommand(lease, ReasonShutdown, time.Second)
		application, err := ApplyDeliveryCommand(running.Invocation(), command, attempt.ProgressDeadline())
		finished, ok := application.Attempt()
		if err != nil || !ok || finished.Disposition().Reason() != ReasonProgressTimeout || finished.Disposition().RetryCost() != RetryCostCharged || application.Invocation().RetrySpent().Value() != 1 {
			t.Fatalf("progress timeout = (%v, %v)", application, err)
		}
		assertInvocationRestoreRoundTrip(t, invocation, application.Invocation())
	})

	t.Run("observed max elapsed", func(t *testing.T) {
		policy := testInvocationPolicy(t, AttemptTimeout(2*time.Minute), MaxElapsed(2*time.Minute), RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
		invocation := testInvocationForPolicy(t, policy)
		lease := deliveryTestLease(t, invocation.ID(), []byte("revoke-observed-max"))
		begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
		running, _ := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
		command, _ := RevokeAttemptCommand(lease, ReasonShutdown, time.Second)
		application, err := ApplyDeliveryCommand(running.Invocation(), command, invocation.MaxElapsedAt())
		finished, ok := application.Attempt()
		outcome := application.Invocation().Outcome()
		if err != nil || !ok || application.Invocation().State() != InvocationDead || finished.Disposition().Reason() != ReasonShutdown || finished.Disposition().RetryCost() != RetryCostNone || outcome.TerminalReason() != ReasonMaxElapsed || !outcome.AvailableAt().IsZero() {
			t.Fatalf("observed max elapsed = (%v, %v)", application, err)
		}
		assertInvocationRestoreRoundTrip(t, invocation, application.Invocation())
	})

	t.Run("retry availability crosses max elapsed", func(t *testing.T) {
		policy := testInvocationPolicy(t, AttemptTimeout(2*time.Minute), MaxElapsed(2*time.Minute), RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
		invocation := testInvocationForPolicy(t, policy)
		lease := deliveryTestLease(t, invocation.ID(), []byte("revoke-cross-max"))
		begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
		running, _ := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
		observedAt := invocation.MaxElapsedAt().Add(-MinRetryDelay)
		command, _ := RevokeAttemptCommand(lease, ReasonLeaseLost, time.Second)
		application, err := ApplyDeliveryCommand(running.Invocation(), command, observedAt)
		finished, ok := application.Attempt()
		outcome := application.Invocation().Outcome()
		if err != nil || !ok || application.Invocation().State() != InvocationDead || finished.Disposition().Reason() != ReasonLeaseLost || outcome.TerminalReason() != ReasonMaxElapsed || outcome.AvailableAt() != observedAt.Add(time.Second) {
			t.Fatalf("crossed max elapsed = (%v, %v)", application, err)
		}
		assertInvocationRestoreRoundTrip(t, invocation, application.Invocation())
	})
}

func TestRevokeAttemptIgnoresChargedRetryExhaustionAndValidatesJitterSample(t *testing.T) {
	policy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(10*time.Minute), Retries(0), RetryBackoff(Exponential(time.Second, 4*time.Second, FullJitter)))
	invocation := testInvocationForPolicy(t, policy)
	lease := deliveryTestLease(t, invocation.ID(), []byte("revoke-free-retry"))
	begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	running, _ := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
	for _, sample := range []time.Duration{MinRetryDelay, 500 * time.Millisecond, time.Second} {
		command, _ := RevokeAttemptCommand(lease, ReasonLeaseLost, sample)
		application, err := ApplyDeliveryCommand(running.Invocation(), command, invocation.EligibleAt().Add(time.Second))
		if err != nil || application.Invocation().State() != InvocationQueued || application.Invocation().RetrySpent().Value() != 0 || application.Invocation().Outcome().AvailableAt() != invocation.EligibleAt().Add(time.Second+sample) {
			t.Fatalf("sample %s = (%v, %v)", sample, application, err)
		}
		assertInvocationRestoreRoundTrip(t, invocation, application.Invocation())
	}
	invalid, _ := RevokeAttemptCommand(lease, ReasonLeaseLost, time.Second+time.Nanosecond)
	if _, err := ApplyDeliveryCommand(running.Invocation(), invalid, invocation.EligibleAt().Add(time.Second)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sample above policy cap = %v", err)
	}
}

func TestRevokeAttemptHonorsAbsoluteAttemptOrdinalLimit(t *testing.T) {
	policy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(10*time.Minute), RetryBackoff(Exponential(MinRetryDelay, MinRetryDelay, NoJitter)))
	genesis := testInvocationForPolicy(t, policy)
	current := genesis
	lease := deliveryTestLease(t, current.ID(), []byte("revoke-attempt-limit"))
	command, _ := RevokeAttemptCommand(lease, ReasonShutdown, MinRetryDelay)
	for ordinal := uint16(1); ordinal <= MaxAttemptOrdinal; ordinal++ {
		begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
		running, err := ApplyDeliveryCommand(current, begin, current.readyAt())
		if err != nil {
			t.Fatalf("begin %d = %v", ordinal, err)
		}
		revoked, err := ApplyDeliveryCommand(running.Invocation(), command, current.readyAt())
		if err != nil {
			t.Fatalf("revoke %d = %v", ordinal, err)
		}
		current = revoked.Invocation()
		if ordinal < MaxAttemptOrdinal && current.State() != InvocationQueued {
			t.Fatalf("state at %d = %s", ordinal, current.State())
		}
	}
	if current.State() != InvocationDead || current.Outcome().TerminalReason() != ReasonAttemptsExhausted || current.RetrySpent().Value() != 0 || current.AttemptOrdinal().Value() != MaxAttemptOrdinal {
		t.Fatalf("attempt limit = %v", current)
	}
	assertInvocationRestoreRoundTrip(t, genesis, current)
}

func TestValidateRevokeRejectsImpossibleTerminalCauses(t *testing.T) {
	policy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(10*time.Minute), RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
	invocation := testInvocationForPolicy(t, policy)
	lease := deliveryTestLease(t, invocation.ID(), []byte("revoke-forgery"))
	begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	running, _ := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
	observedAt := invocation.EligibleAt().Add(time.Second)
	command, _ := RevokeAttemptCommand(lease, ReasonShutdown, time.Second)
	valid, _ := ApplyDeliveryCommand(running.Invocation(), command, observedAt)
	for _, reason := range []Reason{ReasonRetryExhausted, ReasonAttemptsExhausted, ReasonMaxElapsed} {
		forged := forgeRevokeTerminal(t, valid, observedAt, reason)
		result, err := NewApplyResult(observedAt, mustRevokeCommandResult(t, DeliveryMutationApplied, DeliveryControlNone), forged)
		if err != nil {
			t.Fatal(err)
		}
		request, _ := NewApplyRequest(command)
		if _, validateErr := ValidateApplyResult(queueTestBackendDescription(1), request, result); !errors.Is(validateErr, ErrDriverContract) {
			t.Fatalf("forged %s terminal = %v", reason, validateErr)
		}
	}
}

func TestValidateRevokeRejectsTimeoutDispositionAtMaxElapsed(t *testing.T) {
	policy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(2*time.Minute), RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
	invocation := testInvocationForPolicy(t, policy)
	lease := deliveryTestLease(t, invocation.ID(), []byte("revoke-max-timeout"))
	begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	running, _ := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
	deadline, _ := ArbitrateAttemptDeadlineCommand(lease, time.Second)
	application, err := ApplyDeliveryCommand(running.Invocation(), deadline, invocation.MaxElapsedAt())
	if err != nil {
		t.Fatal(err)
	}
	command, _ := RevokeAttemptCommand(lease, ReasonLeaseLost, time.Second)
	application.kind = DeliveryCommandRevokeAttempt
	application.proof = digestDeliveryCommand(command)
	request, _ := NewApplyRequest(command)
	result, err := NewApplyResult(invocation.MaxElapsedAt(), mustRevokeCommandResult(t, DeliveryMutationApplied, DeliveryControlNone), application)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateApplyResult(queueTestBackendDescription(1), request, result); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("timeout disposition at max elapsed = %v", err)
	}
}

func TestValidateRevokeRejectsImpossibleChronology(t *testing.T) {
	policy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(10*time.Minute), RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
	invocation := testInvocationForPolicy(t, policy)
	lease := deliveryTestLease(t, invocation.ID(), []byte("revoke-chronology"))
	begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	running, _ := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
	command, _ := RevokeAttemptCommand(lease, ReasonShutdown, time.Second)
	application, _ := ApplyDeliveryCommand(running.Invocation(), command, invocation.EligibleAt().Add(time.Second))
	forgedAt := invocation.EligibleAt().Add(-time.Nanosecond)
	forged := application
	forged.attempt.finishedAt = forgedAt
	forged.invocation.finishedAt = time.Time{}
	forged.invocation.attempts = &attemptLedger{previous: forged.invocation.attempts.previous, value: forged.attempt, length: forged.invocation.attempts.length}
	forgedOutcome, _ := FinishedAttemptOutcome(forged.attempt.Ordinal(), forged.attempt.Disposition(), ReasonNone, forgedAt, forgedAt.Add(time.Second))
	forged.invocation.history = &invocationOutcomeLedger{previous: forged.invocation.history.previous, value: forgedOutcome, length: forged.invocation.history.length}
	result, err := NewApplyResult(forgedAt, mustRevokeCommandResult(t, DeliveryMutationApplied, DeliveryControlNone), forged)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := NewApplyRequest(command)
	if _, err := ValidateApplyResult(queueTestBackendDescription(1), request, result); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("finish before start = %v", err)
	}

	cancelled, _ := running.Invocation().RequestCancel(invocation.EligibleAt().Add(time.Second))
	terminated, _ := ApplyDeliveryCommand(cancelled, command, invocation.EligibleAt().Add(2*time.Second))
	forgedCancel, _ := CancelRequestedOutcome(terminated.attempt.Ordinal(), invocation.EligibleAt().Add(-time.Nanosecond))
	terminated.invocation.history.previous = &invocationOutcomeLedger{previous: terminated.invocation.history.previous.previous, value: forgedCancel, length: terminated.invocation.history.previous.length}
	terminatedResult, err := NewApplyResult(invocation.EligibleAt().Add(2*time.Second), mustRevokeCommandResult(t, DeliveryMutationApplied, DeliveryControlTerminated), terminated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateApplyResult(queueTestBackendDescription(1), request, terminatedResult); !errors.Is(err, ErrDriverContract) {
		t.Fatalf("cancel before start = %v", err)
	}
}

func TestRevokeAttemptCommandRejectsInvalidReasonsAndDelays(t *testing.T) {
	_, _, invocation, _, _ := deliveryRecordFixture(t, PlacementRegular)
	lease := deliveryTestLease(t, invocation.ID(), []byte("invalid-revoke"))
	for _, test := range []struct {
		reason Reason
		delay  time.Duration
	}{
		{reason: ReasonHandlerFailure, delay: MinRetryDelay},
		{reason: ReasonShutdown, delay: 0},
		{reason: ReasonShutdown, delay: MaxRetryDelay + time.Nanosecond},
	} {
		if _, err := RevokeAttemptCommand(lease, test.reason, test.delay); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid revoke (%v, %s) = %v", test.reason, test.delay, err)
		}
	}
	first, _ := RevokeAttemptCommand(lease, ReasonShutdown, MinRetryDelay)
	same, _ := RevokeAttemptCommand(lease, ReasonShutdown, MinRetryDelay)
	changed, _ := RevokeAttemptCommand(lease, ReasonLeaseLost, MinRetryDelay)
	if digestDeliveryCommand(first) != digestDeliveryCommand(same) || digestDeliveryCommand(first) == digestDeliveryCommand(changed) {
		t.Fatal("revoke provenance is not bound to command proof")
	}
	forged := first
	forged.deadlineDelay += time.Nanosecond
	if _, err := validateDeliveryCommand(forged); !errors.Is(err, ErrInvalid) {
		t.Fatalf("split retry sample = %v", err)
	}
}

func mustRevokeCommandResult(t *testing.T, mutation DeliveryMutationStatus, control DeliveryControlStatus) DeliveryCommandResult {
	t.Helper()
	result, err := NewDeliveryCommandResult(mutation, control)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func forgeRevokeTerminal(t *testing.T, application DeliveryApplication, observedAt time.Time, reason Reason) DeliveryApplication {
	t.Helper()
	availableAt := time.Time{}
	if reason == ReasonMaxElapsed {
		availableAt = observedAt.Add(time.Second)
	}
	outcome, err := FinishedAttemptOutcome(application.attempt.Ordinal(), application.attempt.Disposition(), reason, observedAt, availableAt)
	if err != nil {
		t.Fatal(err)
	}
	application.invocation.state = InvocationDead
	application.invocation.finishedAt = observedAt
	application.invocation.history = &invocationOutcomeLedger{previous: application.invocation.history.previous, value: outcome, length: application.invocation.history.length}
	return application
}

func assertInvocationRestoreRoundTrip(t *testing.T, genesis, invocation Invocation) {
	t.Helper()
	legacy, _ := genesis.LegacyIntent()
	restored, err := RestoreInvocation(InvocationRestoreSpec{Genesis: InvocationSpec{
		ID:           genesis.ID(),
		Namespace:    genesis.Namespace(),
		Partition:    genesis.Partition(),
		Definition:   genesis.Definition(),
		Queue:        genesis.Queue(),
		Mode:         genesis.Mode(),
		Intent:       genesis.Intent(),
		LegacyIntent: legacy,
		Priority:     genesis.Priority(),
		CreatedAt:    genesis.CreatedAt(),
		EligibleAt:   genesis.EligibleAt(),
		StartBefore:  genesis.StartBefore(),
		Policy:       genesis.Policy(),
		Context:      genesis.Context(),
	}, Outcomes: invocation.History(), Attempts: invocation.AttemptRecords()})
	if err != nil || !reflect.DeepEqual(restored, invocation) {
		t.Fatalf("restore round trip = (%v, %v)", restored, err)
	}
}
