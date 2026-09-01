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

func TestBeginAttemptCommandAtomicallyExpiresAtAuthoritativeDeadline(t *testing.T) {
	catalog, _, maxElapsed, payload, record := deliveryRecordFixture(t, PlacementRegular)
	startBeforeRecord := cloneDeliveryRecord(record)
	startBeforeRecord.Genesis.StartBefore = startBeforeRecord.Genesis.EligibleAt.Add(time.Minute)
	startBeforeDelivery, err := RestoreDeliveryRecord(catalog, startBeforeRecord)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		invocation Invocation
		payload    EncodedPayload
		wire       WireDigest
		semantic   PayloadDigest
		at         time.Time
		reason     Reason
	}{
		{name: "start before", invocation: startBeforeDelivery.Invocation(), payload: startBeforeDelivery.Payload(), wire: startBeforeDelivery.WireDigest(), semantic: startBeforeDelivery.PayloadDigest(), at: startBeforeRecord.Genesis.StartBefore, reason: ReasonStartBefore},
		{name: "max elapsed", invocation: maxElapsed, payload: payload, wire: record.WireDigest, semantic: record.PayloadDigest, at: maxElapsed.MaxElapsedAt(), reason: ReasonMaxElapsed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lease := deliveryTestLease(t, test.invocation.ID(), []byte("deadline-race"))
			command, commandErr := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
			if commandErr != nil {
				t.Fatal(commandErr)
			}
			application, applyErr := ApplyDeliveryCommand(test.invocation, command, test.at)
			attempt, hasAttempt := application.Attempt()
			if applyErr != nil || application.Kind() != DeliveryCommandBeginAttempt || !application.Changed() || application.Invocation().State() != InvocationDead || application.Invocation().Outcome().TerminalReason() != test.reason || hasAttempt || !attempt.IsZero() || application.Invocation().AttemptOrdinal().Value() != 0 || len(application.Invocation().Attempts()) != 0 {
				t.Fatalf("deadline begin = (%v, %v, %v, %v)", application, attempt, hasAttempt, applyErr)
			}
			persisted, persistErr := NewDeliveryRecord(application.Invocation(), test.payload, test.wire, test.semantic)
			if persistErr != nil {
				t.Fatal(persistErr)
			}
			restored, restoreErr := RestoreDeliveryRecord(catalog, persisted)
			if restoreErr != nil || restored.Invocation().State() != InvocationDead || restored.Invocation().Outcome() != application.Invocation().Outcome() || restored.Invocation().AttemptOrdinal().Value() != 0 || len(restored.Invocation().Attempts()) != 0 {
				t.Fatalf("deadline restore = (%v, %v)", restored, restoreErr)
			}
			otherLease := deliveryTestLease(t, queueTestInvocationID(t, 75), []byte("stale-deadline-race"))
			otherCommand, otherErr := BeginAttemptCommand(otherLease, testBindingName(t), testBuildID(t))
			if otherErr != nil {
				t.Fatal(otherErr)
			}
			if _, otherErr = ApplyDeliveryCommand(test.invocation, otherCommand, test.at); !errors.Is(otherErr, ErrLeaseLost) {
				t.Fatalf("cross-invocation deadline lease = %v", otherErr)
			}
		})
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

func TestFinishAttemptCommandAtomicallyArbitratesAuthoritativeTimeouts(t *testing.T) {
	tests := []struct {
		name     string
		policy   PolicySnapshot
		boundary func(Attempt) time.Time
		reason   Reason
	}{
		{
			name:     "attempt timeout",
			policy:   testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(10*time.Minute), RetryBackoff(Exponential(2*time.Second, 8*time.Second, FullJitter))),
			boundary: func(attempt Attempt) time.Time { return attempt.Deadline() },
			reason:   ReasonAttemptTimeout,
		},
		{
			name:     "progress timeout",
			policy:   testInvocationPolicy(t, AttemptTimeout(5*time.Minute), ProgressTimeout(time.Minute), MaxElapsed(10*time.Minute), RetryBackoff(Exponential(2*time.Second, 8*time.Second, FullJitter))),
			boundary: func(attempt Attempt) time.Time { return attempt.ProgressDeadline() },
			reason:   ReasonProgressTimeout,
		},
		{
			name:     "attempt timeout wins equal progress deadline",
			policy:   testInvocationPolicy(t, AttemptTimeout(time.Minute), ProgressTimeout(time.Minute), MaxElapsed(10*time.Minute), RetryBackoff(Exponential(2*time.Second, 8*time.Second, FullJitter))),
			boundary: func(attempt Attempt) time.Time { return attempt.Deadline() },
			reason:   ReasonAttemptTimeout,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocation := testInvocationForPolicy(t, test.policy)
			lease := deliveryTestLease(t, invocation.ID(), []byte("finish-timeout"))
			begin, err := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
			if err != nil {
				t.Fatal(err)
			}
			started, err := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
			if err != nil {
				t.Fatal(err)
			}
			active, ok := started.Attempt()
			if !ok {
				t.Fatal("begin did not return an attempt")
			}
			finish, err := FinishAttemptCommand(lease, SuccessDisposition(), 0)
			if err != nil {
				t.Fatal(err)
			}
			finishedAt := test.boundary(active)
			application, err := ApplyDeliveryCommand(started.Invocation(), finish, finishedAt)
			finished, ok := application.Attempt()
			disposition := finished.Disposition()
			wantAvailable := finishedAt.Add(retryBackoffCap(started.Invocation().Policy().Backoff(), started.Invocation().RetrySpent().Value()))
			if err != nil || !application.Changed() || !ok || application.Invocation().State() != InvocationQueued || disposition.Kind() != DispositionRetry || disposition.Reason() != test.reason || disposition.RetryCost() != RetryCostCharged || !disposition.Failure().IsZero() || application.Invocation().Outcome().AvailableAt() != wantAvailable || application.Invocation().RetrySpent().Value() != 1 {
				t.Fatalf("authoritative timeout = (%v, %v, %v, %v)", application, finished, ok, err)
			}
		})
	}
}

func TestFinishAttemptCommandTerminalizesAuthoritativeTimeoutLimits(t *testing.T) {
	tests := []struct {
		name           string
		policy         PolicySnapshot
		terminal       Reason
		available      bool
		wantRetrySpent uint16
	}{
		{
			name:           "retry budget exhausted",
			policy:         testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(10*time.Minute), Retries(0), RetryBackoff(Exponential(2*time.Second, 2*time.Second, NoJitter))),
			terminal:       ReasonRetryExhausted,
			wantRetrySpent: 0,
		},
		{
			name:           "timeout backoff crosses max elapsed",
			policy:         testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(time.Minute+time.Second), Retries(1), RetryBackoff(Exponential(2*time.Second, 2*time.Second, NoJitter))),
			terminal:       ReasonMaxElapsed,
			available:      true,
			wantRetrySpent: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocation := testInvocationForPolicy(t, test.policy)
			lease := deliveryTestLease(t, invocation.ID(), []byte("timeout-limit"))
			begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
			started, err := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
			if err != nil {
				t.Fatal(err)
			}
			attempt, ok := started.Attempt()
			if !ok {
				t.Fatal("timeout limit begin did not return an attempt")
			}
			finish, _ := FinishAttemptCommand(lease, SuccessDisposition(), 0)
			application, err := ApplyDeliveryCommand(started.Invocation(), finish, attempt.Deadline())
			effective, ok := application.Attempt()
			outcome := application.Invocation().Outcome()
			if err != nil || !ok || application.Invocation().State() != InvocationDead || outcome.TerminalReason() != test.terminal || effective.Disposition().Kind() != DispositionRetry || effective.Disposition().Reason() != ReasonAttemptTimeout || effective.Disposition().RetryCost() != RetryCostCharged || application.Invocation().RetrySpent().Value() != test.wantRetrySpent || !outcome.AvailableAt().IsZero() != test.available {
				t.Fatalf("timeout terminal = (%v, %v, %v)", application, effective, err)
			}
		})
	}
}

func TestFinishAttemptCommandKeepsMaxElapsedAndCancellationPrecedence(t *testing.T) {
	maxPolicy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(time.Minute), RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
	maxInvocation := testInvocationForPolicy(t, maxPolicy)
	maxLease := deliveryTestLease(t, maxInvocation.ID(), []byte("max-elapsed"))
	maxBegin, _ := BeginAttemptCommand(maxLease, testBindingName(t), testBuildID(t))
	maxRunning, err := ApplyDeliveryCommand(maxInvocation, maxBegin, maxInvocation.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	maxAttempt, ok := maxRunning.Attempt()
	if !ok {
		t.Fatal("max elapsed begin did not return an attempt")
	}
	success, _ := FinishAttemptCommand(maxLease, SuccessDisposition(), 0)
	maxFinished, err := ApplyDeliveryCommand(maxRunning.Invocation(), success, maxAttempt.Deadline())
	effective, ok := maxFinished.Attempt()
	if err != nil || !ok || maxFinished.Invocation().State() != InvocationDead || maxFinished.Invocation().Outcome().TerminalReason() != ReasonMaxElapsed || effective.Disposition().Kind() != DispositionSucceeded {
		t.Fatalf("max elapsed precedence = (%v, %v, %v)", maxFinished, effective, err)
	}

	cancelPolicy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(time.Minute), RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
	cancelInvocation := testInvocationForPolicy(t, cancelPolicy)
	cancelLease := deliveryTestLease(t, cancelInvocation.ID(), []byte("cancel-requested"))
	cancelBegin, _ := BeginAttemptCommand(cancelLease, testBindingName(t), testBuildID(t))
	cancelRunning, err := ApplyDeliveryCommand(cancelInvocation, cancelBegin, cancelInvocation.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	cancelAttempt, ok := cancelRunning.Attempt()
	if !ok {
		t.Fatal("cancellation begin did not return an attempt")
	}
	requested, err := cancelRunning.Invocation().RequestCancel(cancelAttempt.StartedAt().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	cancelFinish, err := FinishAttemptCommand(cancelLease, SuccessDisposition(), 0)
	if err != nil {
		t.Fatal(err)
	}
	beforeOutcomes := len(requested.History())
	cancelFinished, err := ApplyDeliveryCommand(requested, cancelFinish, cancelAttempt.Deadline())
	cancelEffective, ok := cancelFinished.Attempt()
	if err != nil || !ok || cancelFinished.Invocation().State() != InvocationCancelled || cancelEffective.Disposition().Kind() != DispositionCancelled || cancelEffective.Disposition().Reason() != ReasonCancelRequested || cancelFinished.Invocation().Outcome().TerminalReason() != ReasonNone || !cancelFinished.Invocation().Outcome().AvailableAt().IsZero() || cancelFinished.Invocation().RetrySpent().Value() != 0 || cancelFinished.Invocation().HandlerDeferrals().Value() != 0 || len(cancelFinished.Invocation().History()) != beforeOutcomes+1 {
		t.Fatalf("cancellation precedence = (%v, %v, %v)", cancelFinished, cancelEffective, err)
	}
	if _, err := ApplyDeliveryCommand(cancelFinished.Invocation(), cancelFinish, cancelAttempt.Deadline()); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeated cancellation finish = %v", err)
	}
}

func TestFinishAttemptCommandCancellationClearsProposedReschedule(t *testing.T) {
	retry, err := RetryDisposition(ReasonHandlerFailure, PublicFailure{}, 2*time.Second, RetryCostCharged)
	if err != nil {
		t.Fatal(err)
	}
	deferred, err := DeferredDisposition(PublicFailure{}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		disposition Disposition
	}{
		{name: "success", disposition: SuccessDisposition()},
		{name: "retry", disposition: retry},
		{name: "deferred", disposition: deferred},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(10*time.Minute), RetryBackoff(Exponential(2*time.Second, 2*time.Second, NoJitter)))
			invocation := testInvocationForPolicy(t, policy)
			lease := deliveryTestLease(t, invocation.ID(), []byte("cancel-reschedule"))
			begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
			running, applyErr := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
			if applyErr != nil {
				t.Fatal(applyErr)
			}
			attempt, ok := running.Attempt()
			if !ok {
				t.Fatal("cancellation reschedule begin did not return an attempt")
			}
			requested, requestErr := running.Invocation().RequestCancel(attempt.StartedAt().Add(time.Second))
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			delay := time.Duration(0)
			if test.disposition.Kind() == DispositionRetry || test.disposition.Kind() == DispositionDeferred {
				delay = 2 * time.Second
			}
			finish, commandErr := FinishAttemptCommand(lease, test.disposition, delay)
			if commandErr != nil {
				t.Fatal(commandErr)
			}
			application, applyErr := ApplyDeliveryCommand(requested, finish, attempt.StartedAt().Add(2*time.Second))
			effective, ok := application.Attempt()
			outcome := application.Invocation().Outcome()
			if applyErr != nil || !ok || application.Invocation().State() != InvocationCancelled || effective.Disposition().Kind() != DispositionCancelled || effective.Disposition().Reason() != ReasonCancelRequested || !outcome.AvailableAt().IsZero() || application.Invocation().RetrySpent().Value() != 0 || application.Invocation().HandlerDeferrals().Value() != 0 {
				t.Fatalf("cancelled reschedule = (%v, %v, %v)", application, effective, applyErr)
			}
		})
	}
}

func TestFinishAttemptCommandDoesNotMaterializeSupersededOverflowingDelay(t *testing.T) {
	policy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(2*time.Minute), RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
	spec := testInvocationSpec(t, policy)
	spec.CreatedAt = time.Date(9999, 12, 31, 23, 57, 0, 0, time.UTC)
	spec.EligibleAt = spec.CreatedAt
	invocation, err := NewInvocation(spec)
	if err != nil {
		t.Fatal(err)
	}
	lease := deliveryTestLease(t, invocation.ID(), []byte("overflowing-delay"))
	begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
	running, err := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	retry, err := RetryDisposition(ReasonHandlerFailure, PublicFailure{}, MaxRetryDelay, RetryCostCharged)
	if err != nil {
		t.Fatal(err)
	}
	finish, err := FinishAttemptCommand(lease, retry, MaxRetryDelay)
	if err != nil {
		t.Fatal(err)
	}
	application, err := ApplyDeliveryCommand(running.Invocation(), finish, invocation.MaxElapsedAt())
	effective, ok := application.Attempt()
	if err != nil || !ok || application.Invocation().State() != InvocationDead || application.Invocation().Outcome().TerminalReason() != ReasonMaxElapsed || !application.Invocation().Outcome().AvailableAt().IsZero() || effective.Disposition() != retry {
		t.Fatalf("overflowing superseded delay = (%v, %v, %v)", application, effective, err)
	}
}

func TestFinishAttemptCommandTerminalizesOverflowingTimeoutBackoff(t *testing.T) {
	for _, test := range []struct {
		name       string
		retries    uint16
		terminal   Reason
		retrySpent uint16
	}{
		{name: "max elapsed", retries: 1, terminal: ReasonMaxElapsed},
		{name: "retry exhausted", retries: 0, terminal: ReasonRetryExhausted},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := testInvocationPolicy(t, AttemptTimeout(time.Minute), MaxElapsed(2*time.Minute), Retries(int(test.retries)), RetryBackoff(Exponential(2*time.Minute, 2*time.Minute, NoJitter)))
			spec := testInvocationSpec(t, policy)
			spec.CreatedAt = time.Date(9999, 12, 31, 23, 57, 0, 0, time.UTC)
			spec.EligibleAt = spec.CreatedAt
			invocation, err := NewInvocation(spec)
			if err != nil {
				t.Fatal(err)
			}
			lease := deliveryTestLease(t, invocation.ID(), []byte("overflowing-timeout"))
			begin, _ := BeginAttemptCommand(lease, testBindingName(t), testBuildID(t))
			running, err := ApplyDeliveryCommand(invocation, begin, invocation.EligibleAt())
			if err != nil {
				t.Fatal(err)
			}
			attempt, ok := running.Attempt()
			if !ok {
				t.Fatal("overflowing timeout begin did not return an attempt")
			}
			finish, _ := FinishAttemptCommand(lease, SuccessDisposition(), 0)
			application, err := ApplyDeliveryCommand(running.Invocation(), finish, attempt.Deadline())
			finished, ok := application.Attempt()
			outcome := application.Invocation().Outcome()
			wantAvailable := time.Time{}
			if test.terminal == ReasonMaxElapsed {
				wantAvailable = invocation.MaxElapsedAt()
			}
			if err != nil || !ok || application.Invocation().State() != InvocationDead || outcome.TerminalReason() != test.terminal || outcome.AvailableAt() != wantAvailable || application.Invocation().RetrySpent().Value() != test.retrySpent || finished.Disposition().Kind() != DispositionRetry || finished.Disposition().Reason() != ReasonAttemptTimeout || finished.Disposition().RetryCost() != RetryCostCharged {
				t.Fatalf("overflowing timeout = (%v, %v, %v)", application, finished, err)
			}
		})
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
