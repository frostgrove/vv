package jobs

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestInvocationGenesisDerivesImmutableBoundedState(t *testing.T) {
	policy := testInvocationPolicy(t)
	spec := testInvocationSpec(t, policy)
	invocation, err := NewInvocation(spec)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.ID() != spec.ID || invocation.Namespace() != spec.Namespace || invocation.Partition() != spec.Partition || invocation.Definition() != spec.Definition || invocation.Queue() != spec.Queue || invocation.Mode() != spec.Mode || invocation.Intent() != spec.Intent || invocation.IntentDigest() != spec.Intent.Digest() || invocation.Priority() != spec.Priority || invocation.State() != InvocationQueued || invocation.Policy() != policy {
		t.Fatalf("identity = %+v", invocation)
	}
	if !invocation.CreatedAt().Equal(spec.CreatedAt) || invocation.CreatedAt().Location() != time.UTC || !invocation.EligibleAt().Equal(spec.EligibleAt) || !invocation.MaxElapsedAt().Equal(spec.EligibleAt.Add(policy.MaxElapsed())) {
		t.Fatalf("times = %v/%v/%v", invocation.CreatedAt(), invocation.EligibleAt(), invocation.MaxElapsedAt())
	}
	if invocation.IsTerminal() || !invocation.FinishedAt().IsZero() || !invocation.AttemptOrdinal().IsZero() || !invocation.RetrySpent().IsZero() || !invocation.HandlerDeferrals().IsZero() || !invocation.DeliveryDeferrals().IsZero() || !invocation.CancelRequestedAt().IsZero() {
		t.Fatal("genesis carries execution state")
	}
	if history := invocation.History(); len(history) != 1 || history[0].Kind() != InvocationOutcomeInitial || len(invocation.Attempts()) != 0 || len(invocation.AttemptRecords()) != 0 {
		t.Fatalf("genesis ledger = %v/%v", history, invocation.Attempts())
	}
	history := invocation.History()
	history[0] = InvocationOutcome{}
	if invocation.Outcome().Kind() != InvocationOutcomeInitial {
		t.Fatal("returned history mutated invocation")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		formatted := fmt.Sprintf(format, invocation)
		if !strings.Contains(formatted, "job invocation") || strings.Contains(formatted, "private") {
			t.Fatalf("format %q = %q", format, formatted)
		}
	}
}

func TestInvocationGenesisRejectsFabricatedIdentityAndTime(t *testing.T) {
	policy := testInvocationPolicy(t)
	base := testInvocationSpec(t, policy)
	tests := []func(*InvocationSpec){
		func(spec *InvocationSpec) { spec.ID = InvocationID{} },
		func(spec *InvocationSpec) { spec.Namespace = Namespace{} },
		func(spec *InvocationSpec) {
			spec.Namespace, _ = NamespaceOf("tests", "other")
		},
		func(spec *InvocationSpec) { spec.Partition = PartitionKey{} },
		func(spec *InvocationSpec) { spec.Definition = Name{} },
		func(spec *InvocationSpec) { spec.Queue = QueueName{} },
		func(spec *InvocationSpec) { spec.Mode = 0 },
		func(spec *InvocationSpec) { spec.Intent = IntentKey{} },
		func(spec *InvocationSpec) { spec.LegacyIntent = protectLegacyIntent(Intent("legacy-key")) },
		func(spec *InvocationSpec) {
			other := testJobName(t, "tests.other")
			spec.Intent, _ = NewIntentKey(intentScopeBinding(spec.Namespace, spec.Partition, other, IntentRegular), spec.Intent.Revision(), IntentRegular, spec.Intent.Digest())
		},
		func(spec *InvocationSpec) {
			var raw [IntentDigestBytes]byte
			raw[0] = 9
			digest, _ := IntentDigestFromBytes(raw)
			spec.Intent, _ = NewIntentKey(spec.Intent.Scope(), spec.Intent.Revision(), IntentRegular, digest)
		},
		func(spec *InvocationSpec) { spec.Mode = PlacementOnce },
		func(spec *InvocationSpec) { spec.Priority = 0 },
		func(spec *InvocationSpec) { spec.Priority = MaximumPriority + 1 },
		func(spec *InvocationSpec) { spec.CreatedAt = time.Time{} },
		func(spec *InvocationSpec) { spec.EligibleAt = spec.CreatedAt.Add(-time.Nanosecond) },
		func(spec *InvocationSpec) { spec.EligibleAt = spec.CreatedAt.Add(MaxRetention + time.Nanosecond) },
		func(spec *InvocationSpec) { spec.StartBefore = spec.EligibleAt.Add(-time.Nanosecond) },
		func(spec *InvocationSpec) {
			spec.StartBefore = spec.EligibleAt.Add(policy.MaxElapsed() + time.Nanosecond)
		},
		func(spec *InvocationSpec) { spec.Queue = queueMustQueueName("other") },
	}
	for index, mutate := range tests {
		spec := base
		mutate(&spec)
		if invocation, err := NewInvocation(spec); !errors.Is(err, ErrInvalid) || !invocation.IsZero() {
			t.Fatalf("case %d = (%v, %v)", index, invocation, err)
		}
	}
}

func TestInvocationPreservesExplicitLegacyIntentCompatibility(t *testing.T) {
	policy := testInvocationPolicy(t)
	spec := testInvocationSpec(t, policy)
	spec.Mode = PlacementOnce
	spec.LegacyIntent = protectLegacyIntent(Intent("legacy-key"))
	intents, err := digestProducerIntents(CurrentIntentDigestPlan(), spec.Namespace, spec.Partition, spec.Definition, IntentOnce, Intent("legacy-key"))
	if err != nil {
		t.Fatal(err)
	}
	spec.Intent = intents.Current()
	invocation, err := NewInvocation(spec)
	if err != nil {
		t.Fatal(err)
	}
	legacy, ok := invocation.LegacyIntent()
	if !ok || legacy.Value() != "legacy-key" {
		t.Fatalf("legacy intent = %+v", legacy)
	}
}

func TestInvocationDerivesRetryAndDeferralAccountingWithoutReuse(t *testing.T) {
	policy := testInvocationPolicy(t,
		Retries(1),
		MaxHandlerDeferrals(1),
		MaxDeliveryDeferrals(1),
		RetryBackoff(Exponential(time.Second, 4*time.Second, NoJitter)),
	)

	delivery := testInvocationForPolicy(t, policy)
	observed := delivery.EligibleAt()
	deferred, err := delivery.DeferDelivery(DeferDeliverySpec{Reason: ReasonAdmission, ObservedAt: observed, AvailableAt: observed.Add(MinRetryDelay)})
	if err != nil || deferred.DeliveryDeferrals().Value() != 1 || delivery.DeliveryDeferrals().Value() != 0 {
		t.Fatalf("first delivery deferral = (%v, %v)", deferred.DeliveryDeferrals().Value(), err)
	}
	exhaustedDelivery, err := deferred.DeferDelivery(DeferDeliverySpec{Reason: ReasonAdmission, ObservedAt: deferred.Outcome().AvailableAt()})
	if err != nil || exhaustedDelivery.State() != InvocationDead || exhaustedDelivery.Outcome().TerminalReason() != ReasonDeferralsExhausted || exhaustedDelivery.DeliveryDeferrals().Value() != 1 {
		t.Fatalf("delivery exhaustion = (%v, %v, %v)", exhaustedDelivery.State(), exhaustedDelivery.Outcome().TerminalReason(), err)
	}

	retrying := testInvocationForPolicy(t, policy)
	running, firstAttempt := beginTestAttempt(t, retrying, retrying.EligibleAt())
	retry, err := RetryDisposition(ReasonHandlerFailure, PublicFailure{}, 0, RetryCostCharged)
	if err != nil {
		t.Fatal(err)
	}
	finishedAt := firstAttempt.StartedAt().Add(time.Millisecond)
	queued, _, err := running.FinishAttempt(firstAttempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: retry, AvailableAt: finishedAt.Add(time.Second)})
	if err != nil || queued.State() != InvocationQueued || queued.RetrySpent().Value() != 1 || running.RetrySpent().Value() != 0 {
		t.Fatalf("first retry = (%v, %d, %v)", queued.State(), queued.RetrySpent().Value(), err)
	}
	if _, _, err := queued.FinishAttempt(firstAttempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: retry, AvailableAt: finishedAt.Add(time.Second)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused attempt error = %v", err)
	}
	running, secondAttempt := beginTestAttempt(t, queued, queued.Outcome().AvailableAt())
	exhaustedRetry, _, err := running.FinishAttempt(secondAttempt, FinishAttemptSpec{FinishedAt: secondAttempt.StartedAt().Add(time.Millisecond), Disposition: retry})
	if err != nil || exhaustedRetry.State() != InvocationDead || exhaustedRetry.Outcome().TerminalReason() != ReasonRetryExhausted || exhaustedRetry.RetrySpent().Value() != 1 {
		t.Fatalf("retry exhaustion = (%v, %v, %d, %v)", exhaustedRetry.State(), exhaustedRetry.Outcome().TerminalReason(), exhaustedRetry.RetrySpent().Value(), err)
	}

	handler := testInvocationForPolicy(t, policy)
	running, firstAttempt = beginTestAttempt(t, handler, handler.EligibleAt())
	dependency, err := DeferredDisposition(PublicFailure{}, 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	finishedAt = firstAttempt.StartedAt().Add(time.Millisecond)
	queued, _, err = running.FinishAttempt(firstAttempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: dependency, AvailableAt: finishedAt.Add(250 * time.Millisecond)})
	if err != nil || queued.HandlerDeferrals().Value() != 1 {
		t.Fatalf("handler deferral = (%d, %v)", queued.HandlerDeferrals().Value(), err)
	}
	running, secondAttempt = beginTestAttempt(t, queued, queued.Outcome().AvailableAt())
	exhaustedHandler, _, err := running.FinishAttempt(secondAttempt, FinishAttemptSpec{FinishedAt: secondAttempt.StartedAt().Add(time.Millisecond), Disposition: dependency})
	if err != nil || exhaustedHandler.State() != InvocationDead || exhaustedHandler.Outcome().TerminalReason() != ReasonDeferralsExhausted || exhaustedHandler.HandlerDeferrals().Value() != 1 {
		t.Fatalf("handler exhaustion = (%v, %v, %d, %v)", exhaustedHandler.State(), exhaustedHandler.Outcome().TerminalReason(), exhaustedHandler.HandlerDeferrals().Value(), err)
	}
}

func TestInvocationConsumesExactBackoffSnapshot(t *testing.T) {
	policy := testInvocationPolicy(t, Retries(4), RetryBackoff(Exponential(time.Second, 4*time.Second, NoJitter)))
	invocation := testInvocationForPolicy(t, policy)
	retry, _ := RetryDisposition(ReasonHandlerFailure, PublicFailure{}, 0, RetryCostCharged)
	running, attempt := beginTestAttempt(t, invocation, invocation.EligibleAt())
	finishedAt := attempt.StartedAt().Add(time.Millisecond)
	for _, delay := range []time.Duration{MinRetryDelay, 2 * time.Second, 4 * time.Second} {
		if _, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: retry, AvailableAt: finishedAt.Add(delay)}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("first no-jitter delay %s error = %v", delay, err)
		}
	}
	queued, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: retry, AvailableAt: finishedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	running, attempt = beginTestAttempt(t, queued, queued.Outcome().AvailableAt())
	finishedAt = attempt.StartedAt().Add(time.Millisecond)
	if _, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: retry, AvailableAt: finishedAt.Add(time.Second)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("second no-jitter delay error = %v", err)
	}
	if _, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: retry, AvailableAt: finishedAt.Add(2 * time.Second)}); err != nil {
		t.Fatalf("second no-jitter delay = %v", err)
	}

	jitterPolicy := testInvocationPolicy(t, RetryBackoff(Exponential(time.Second, 4*time.Second, FullJitter)))
	jitter := testInvocationForPolicy(t, jitterPolicy)
	running, attempt = beginTestAttempt(t, jitter, jitter.EligibleAt())
	finishedAt = attempt.StartedAt().Add(time.Millisecond)
	if _, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: retry, AvailableAt: finishedAt.Add(500 * time.Millisecond)}); err != nil {
		t.Fatalf("bounded full jitter = %v", err)
	}
	if _, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: retry, AvailableAt: finishedAt.Add(2 * time.Second)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("full jitter above current cap = %v", err)
	}
}

func TestInvocationEnforcesAttemptElapsedAndStartDeadlines(t *testing.T) {
	policy := testInvocationPolicy(t,
		AttemptTimeout(time.Minute),
		MaxElapsed(10*time.Minute),
		RetryBackoff(Exponential(time.Second, time.Second, NoJitter)),
	)
	spec := testInvocationSpec(t, policy)
	spec.StartBefore = spec.EligibleAt.Add(2 * time.Minute)
	invocation, err := NewInvocation(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := invocation.BeginAttempt(BeginAttemptSpec{Binding: testBindingName(t), Build: testBuildID(t), StartedAt: spec.StartBefore}); !errors.Is(err, ErrConflict) {
		t.Fatalf("attempt at start deadline = %v", err)
	}
	if _, err := invocation.Expire(spec.StartBefore.Add(-time.Nanosecond)); !errors.Is(err, ErrConflict) {
		t.Fatalf("early expiry = %v", err)
	}
	expired, err := invocation.Expire(spec.StartBefore)
	if err != nil || expired.State() != InvocationDead || expired.Outcome().TerminalReason() != ReasonStartBefore {
		t.Fatalf("start expiry = (%v, %v, %v)", expired.State(), expired.Outcome().TerminalReason(), err)
	}
	deferred, err := invocation.DeferDelivery(DeferDeliverySpec{Reason: ReasonAdmission, ObservedAt: invocation.EligibleAt(), AvailableAt: spec.StartBefore})
	if err != nil || deferred.State() != InvocationDead || deferred.Outcome().TerminalReason() != ReasonStartBefore || !deferred.Outcome().AvailableAt().Equal(spec.StartBefore) {
		t.Fatalf("future start deadline = (%v, %v, %v)", deferred.State(), deferred.Outcome(), err)
	}
	terminal, err := invocation.FinishDelivery(FinishDeliverySpec{State: InvocationDiscarded, Reason: ReasonPayload, ObservedAt: spec.StartBefore})
	if err != nil || terminal.State() != InvocationDead || terminal.Outcome().TerminalReason() != ReasonStartBefore {
		t.Fatalf("payload after start deadline = (%v, %v, %v)", terminal.State(), terminal.Outcome().TerminalReason(), err)
	}

	running, attempt := beginTestAttempt(t, invocation, invocation.EligibleAt())
	if _, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: attempt.Deadline(), Disposition: SuccessDisposition()}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("success after attempt deadline = %v", err)
	}
	timeoutRetry, _ := RetryDisposition(ReasonAttemptTimeout, PublicFailure{}, 0, RetryCostCharged)
	queued, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: attempt.Deadline(), Disposition: timeoutRetry, AvailableAt: attempt.Deadline().Add(time.Second)})
	if err != nil || queued.State() != InvocationQueued {
		t.Fatalf("attempt timeout retry = (%v, %v)", queued.State(), err)
	}

	maxPolicy := testInvocationPolicy(t, AttemptTimeout(10*time.Minute), MaxElapsed(10*time.Minute))
	maxElapsed := testInvocationForPolicy(t, maxPolicy)
	running, attempt = beginTestAttempt(t, maxElapsed, maxElapsed.EligibleAt())
	dead, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: maxElapsed.MaxElapsedAt(), Disposition: SuccessDisposition()})
	if err != nil || dead.State() != InvocationDead || dead.Outcome().TerminalReason() != ReasonMaxElapsed || !dead.FinishedAt().Equal(maxElapsed.MaxElapsedAt()) {
		t.Fatalf("maximum elapsed = (%v, %v, %v)", dead.State(), dead.Outcome().TerminalReason(), err)
	}
}

func TestInvocationStartBeforeOnlyGatesTheFirstPhysicalAttempt(t *testing.T) {
	policy := testInvocationPolicy(t,
		AttemptTimeout(5*time.Minute),
		MaxElapsed(10*time.Minute),
		Retries(2),
		RetryBackoff(Exponential(time.Second, time.Second, NoJitter)),
	)
	spec := testInvocationSpec(t, policy)
	spec.StartBefore = spec.EligibleAt.Add(2 * time.Second)
	invocation, err := NewInvocation(spec)
	if err != nil {
		t.Fatal(err)
	}

	running, attempt := beginTestAttempt(t, invocation, spec.StartBefore.Add(-time.Nanosecond))
	succeeded, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: spec.StartBefore, Disposition: SuccessDisposition()})
	if err != nil || succeeded.State() != InvocationSucceeded {
		t.Fatalf("attempt crossing start deadline = (%v, %v)", succeeded.State(), err)
	}

	running, attempt = beginTestAttempt(t, invocation, invocation.EligibleAt())
	retry, _ := RetryDisposition(ReasonHandlerFailure, PublicFailure{}, 0, RetryCostCharged)
	finishedAt := spec.StartBefore.Add(-500 * time.Millisecond)
	availableAt := finishedAt.Add(time.Second)
	queued, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: retry, AvailableAt: availableAt})
	if err != nil || queued.State() != InvocationQueued || !queued.Outcome().AvailableAt().After(spec.StartBefore) {
		t.Fatalf("retry crossing start deadline = (%v, %v, %v)", queued.State(), queued.Outcome(), err)
	}
	if _, err := queued.Expire(spec.StartBefore); !errors.Is(err, ErrConflict) {
		t.Fatalf("post-attempt start expiry = %v", err)
	}
	secondRunning, second := beginTestAttempt(t, queued, availableAt)
	secondResult, _, err := secondRunning.FinishAttempt(second, FinishAttemptSpec{FinishedAt: availableAt.Add(time.Millisecond), Disposition: SuccessDisposition()})
	if err != nil || secondResult.State() != InvocationSucceeded {
		t.Fatalf("second attempt after start deadline = (%v, %v)", secondResult.State(), err)
	}
	dead, err := queued.Expire(queued.MaxElapsedAt())
	if err != nil || dead.State() != InvocationDead || dead.Outcome().TerminalReason() != ReasonMaxElapsed {
		t.Fatalf("post-attempt maximum elapsed = (%v, %v, %v)", dead.State(), dead.Outcome().TerminalReason(), err)
	}
}

func TestInvocationProgressHeartbeatBoundariesAndTimeoutTaxonomy(t *testing.T) {
	disabled := testGenesisInvocation(t)
	disabledRunning, disabledAttempt := beginTestAttempt(t, disabled, disabled.EligibleAt())
	if !disabledAttempt.ProgressedAt().IsZero() || !disabledAttempt.ProgressDeadline().IsZero() {
		t.Fatalf("disabled progress fields = %v/%v", disabledAttempt.ProgressedAt(), disabledAttempt.ProgressDeadline())
	}
	if _, _, err := disabledRunning.RecordProgress(disabledAttempt, disabledAttempt.StartedAt()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("disabled progress = %v", err)
	}

	policy := testInvocationPolicy(t,
		AttemptTimeout(4*time.Minute),
		ProgressTimeout(time.Minute),
		MaxElapsed(10*time.Minute),
		Retries(4),
		RetryBackoff(Exponential(time.Second, time.Second, NoJitter)),
	)
	invocation := testInvocationForPolicy(t, policy)
	running, token := beginTestAttempt(t, invocation, invocation.EligibleAt())
	if token.ProgressedAt() != token.StartedAt() || token.ProgressDeadline() != token.StartedAt().Add(time.Minute) {
		t.Fatalf("initial progress window = %v/%v", token.ProgressedAt(), token.ProgressDeadline())
	}
	idempotent, same, err := running.RecordProgress(token, token.StartedAt())
	if err != nil || same.Record() != token.Record() || idempotent.AttemptRecords()[0] != token.Record() {
		t.Fatalf("idempotent initial progress = (%v, %v)", same.Record(), err)
	}

	progressed := running
	for _, offset := range []time.Duration{59 * time.Second, 118 * time.Second, 177 * time.Second, 210 * time.Second} {
		var active Attempt
		progressed, active, err = progressed.RecordProgress(token, token.StartedAt().Add(offset))
		if err != nil || active.ProgressedAt() != token.StartedAt().Add(offset) {
			t.Fatalf("progress at %s = (%v, %v)", offset, active.Record(), err)
		}
	}
	active := progressed.Attempts()[0]
	if active.ProgressDeadline() != active.Deadline() {
		t.Fatalf("capped progress deadline = %v/%v", active.ProgressDeadline(), active.Deadline())
	}
	if _, _, err := progressed.RecordProgress(token, active.ProgressedAt().Add(-time.Nanosecond)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("backward progress = %v", err)
	}
	if _, _, err := progressed.RecordProgress(token, active.ProgressDeadline()); !errors.Is(err, ErrConflict) {
		t.Fatalf("progress at deadline = %v", err)
	}
	if _, _, err := progressed.FinishAttempt(token, FinishAttemptSpec{FinishedAt: active.ProgressedAt().Add(-time.Nanosecond), Disposition: SuccessDisposition()}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("finish before progress = %v", err)
	}

	progressRetry, _ := RetryDisposition(ReasonProgressTimeout, PublicFailure{}, 0, RetryCostCharged)
	earlyRunning, earlyAttempt := beginTestAttempt(t, invocation, invocation.EligibleAt())
	if _, _, err := earlyRunning.FinishAttempt(earlyAttempt, FinishAttemptSpec{FinishedAt: earlyAttempt.StartedAt().Add(30 * time.Second), Disposition: progressRetry, AvailableAt: earlyAttempt.StartedAt().Add(31 * time.Second)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("early progress timeout = %v", err)
	}
	if _, _, err := earlyRunning.FinishAttempt(earlyAttempt, FinishAttemptSpec{FinishedAt: earlyAttempt.ProgressDeadline(), Disposition: SuccessDisposition()}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("success at progress deadline = %v", err)
	}
	queued, _, err := earlyRunning.FinishAttempt(earlyAttempt, FinishAttemptSpec{FinishedAt: earlyAttempt.ProgressDeadline(), Disposition: progressRetry, AvailableAt: earlyAttempt.ProgressDeadline().Add(time.Second)})
	if err != nil || queued.State() != InvocationQueued || queued.Outcome().Reason() != ReasonProgressTimeout {
		t.Fatalf("progress timeout retry = (%v, %v, %v)", queued.State(), queued.Outcome(), err)
	}

	attemptRetry, _ := RetryDisposition(ReasonAttemptTimeout, PublicFailure{}, 0, RetryCostCharged)
	if _, _, err := progressed.FinishAttempt(token, FinishAttemptSpec{FinishedAt: active.Deadline(), Disposition: progressRetry, AvailableAt: active.Deadline().Add(time.Second)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("progress timeout at attempt deadline = %v", err)
	}
	attemptQueued, _, err := progressed.FinishAttempt(token, FinishAttemptSpec{FinishedAt: active.Deadline(), Disposition: attemptRetry, AvailableAt: active.Deadline().Add(time.Second)})
	if err != nil || attemptQueued.State() != InvocationQueued || attemptQueued.Outcome().Reason() != ReasonAttemptTimeout {
		t.Fatalf("attempt timeout precedence = (%v, %v, %v)", attemptQueued.State(), attemptQueued.Outcome(), err)
	}
}

func TestInvocationCancellationAndTerminationRequireExactProvenance(t *testing.T) {
	queued := testGenesisInvocation(t)
	cancelled, err := queued.RequestCancel(queued.CreatedAt().Add(time.Second))
	if err != nil || cancelled.State() != InvocationCancelled || cancelled.Outcome().TerminalReason() != ReasonCancelRequested {
		t.Fatalf("queued cancellation = (%v, %v)", cancelled.State(), err)
	}
	if _, err := cancelled.RequestCancel(cancelled.FinishedAt()); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal cancellation = %v", err)
	}

	running, attempt := beginTestAttempt(t, queued, queued.EligibleAt())
	cancelDisposition, _ := CancelledDisposition(ReasonCancelRequested)
	if _, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: attempt.StartedAt().Add(time.Second), Disposition: cancelDisposition}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unrequested cancellation = %v", err)
	}
	requestedAt := attempt.StartedAt().Add(time.Second)
	requested, err := running.RequestCancel(requestedAt)
	if err != nil || requested.State() != InvocationCancelRequested || !requested.CancelRequestedAt().Equal(requestedAt) || requested.Outcome().Kind() != InvocationOutcomeCancelRequested {
		t.Fatalf("running cancellation request = (%v, %v)", requested.State(), err)
	}
	if _, _, err := requested.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: requestedAt.Add(time.Second), Disposition: SuccessDisposition()}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unacknowledged cancellation = %v", err)
	}
	cancelled, finished, err := requested.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: requestedAt.Add(time.Second), Disposition: cancelDisposition})
	if err != nil || cancelled.State() != InvocationCancelled || finished.Disposition().Kind() != DispositionCancelled {
		t.Fatalf("cooperative cancellation = (%v, %v, %v)", cancelled.State(), finished.Disposition().Kind(), err)
	}

	policy := testInvocationPolicy(t, Retries(1), RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
	invocation := testInvocationForPolicy(t, policy)
	running, attempt = beginTestAttempt(t, invocation, invocation.EligibleAt())
	retry, _ := RetryDisposition(ReasonHandlerFailure, PublicFailure{}, 0, RetryCostCharged)
	finishedAt := attempt.StartedAt().Add(time.Millisecond)
	invocation, _, err = running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: retry, AvailableAt: finishedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	running, attempt = beginTestAttempt(t, invocation, invocation.Outcome().AvailableAt())
	requestedAt = attempt.StartedAt().Add(time.Second)
	requested, err = running.RequestCancel(requestedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := requested.Terminate(requestedAt.Add(-time.Nanosecond)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("termination before request = %v", err)
	}
	terminated, err := requested.Terminate(requestedAt.Add(time.Second))
	if err != nil || terminated.State() != InvocationTerminated || terminated.RetrySpent().Value() != 1 || terminated.Attempts()[1].Disposition().Kind() != DispositionTerminated {
		t.Fatalf("termination = (%v, %d, %v)", terminated.State(), terminated.RetrySpent().Value(), err)
	}
}

func TestInvocationCancellationAndTerminationAreMonotonicAcrossLedgers(t *testing.T) {
	policy := testInvocationPolicy(t,
		AttemptTimeout(5*time.Minute),
		ProgressTimeout(time.Minute),
		Retries(2),
		RetryBackoff(Exponential(time.Second, time.Second, NoJitter)),
	)
	genesis := testInvocationSpec(t, policy)
	invocation, err := NewInvocation(genesis)
	if err != nil {
		t.Fatal(err)
	}
	running, attempt := beginTestAttempt(t, invocation, invocation.EligibleAt())
	running, _, err = running.RecordProgress(attempt, attempt.StartedAt().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	beforeProgress := attempt.StartedAt().Add(30*time.Second - time.Nanosecond)
	if _, err := running.RequestCancel(beforeProgress); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cancellation before progress = %v", err)
	}
	if _, err := running.Terminate(beforeProgress); !errors.Is(err, ErrInvalid) {
		t.Fatalf("termination before progress = %v", err)
	}

	retry, _ := RetryDisposition(ReasonHandlerFailure, PublicFailure{}, 0, RetryCostCharged)
	finishedAt := attempt.StartedAt().Add(40 * time.Second)
	queued, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: retry, AvailableAt: finishedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queued.RequestCancel(finishedAt.Add(-time.Nanosecond)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("queued cancellation before outcome = %v", err)
	}
	if _, err := queued.Terminate(finishedAt.Add(-time.Nanosecond)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("queued termination before outcome = %v", err)
	}
	cancelled, err := queued.RequestCancel(finishedAt)
	if err != nil || cancelled.State() != InvocationCancelled {
		t.Fatalf("queued cancellation at outcome = (%v, %v)", cancelled.State(), err)
	}
	terminated, err := queued.Terminate(finishedAt)
	if err != nil || terminated.State() != InvocationTerminated {
		t.Fatalf("queued termination at outcome = (%v, %v)", terminated.State(), err)
	}

	for name, terminal := range map[string]Invocation{"cancel": cancelled, "terminate": terminated} {
		outcomes := terminal.History()
		outcomes[len(outcomes)-1].occurredAt = finishedAt.Add(-time.Nanosecond)
		if restored, err := RestoreInvocation(InvocationRestoreSpec{Genesis: genesis, Outcomes: outcomes, Attempts: terminal.AttemptRecords()}); !errors.Is(err, ErrCorrupt) || !restored.IsZero() {
			t.Fatalf("%s nonmonotonic restore = (%v, %v)", name, restored, err)
		}
	}
}

func TestRestoreInvocationValidatesCoalescedProgressSnapshots(t *testing.T) {
	policy := testInvocationPolicy(t,
		AttemptTimeout(5*time.Minute),
		ProgressTimeout(time.Minute),
		MaxElapsed(10*time.Minute),
	)
	genesis := testInvocationSpec(t, policy)
	invocation, err := NewInvocation(genesis)
	if err != nil {
		t.Fatal(err)
	}
	running, token := beginTestAttempt(t, invocation, invocation.EligibleAt())
	running, _, err = running.RecordProgress(token, token.StartedAt().Add(50*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	requested, err := running.RequestCancel(token.StartedAt().Add(70 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	requested, _, err = requested.RecordProgress(token, token.StartedAt().Add(100*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	activeOutcomes := requested.History()
	activeAttempts := requested.AttemptRecords()
	activeRestored, err := RestoreInvocation(InvocationRestoreSpec{Genesis: genesis, Outcomes: activeOutcomes, Attempts: activeAttempts})
	if err != nil || activeRestored.State() != InvocationCancelRequested || !slices.Equal(activeRestored.AttemptRecords(), activeAttempts) {
		t.Fatalf("active progress restore = (%v, %v)", activeRestored.State(), err)
	}

	cancelledDisposition, _ := CancelledDisposition(ReasonCancelRequested)
	finishedAt := token.StartedAt().Add(110 * time.Second)
	finished, _, err := requested.FinishAttempt(token, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: cancelledDisposition})
	if err != nil {
		t.Fatal(err)
	}
	validOutcomes := finished.History()
	validAttempts := finished.AttemptRecords()
	restored, err := RestoreInvocation(InvocationRestoreSpec{Genesis: genesis, Outcomes: validOutcomes, Attempts: validAttempts})
	if err != nil || restored.State() != InvocationCancelled || !slices.Equal(restored.AttemptRecords(), validAttempts) {
		t.Fatalf("finished progress restore = (%v, %v)", restored.State(), err)
	}

	tests := []func([]AttemptRecord){
		func(records []AttemptRecord) { records[0].ProgressedAt = time.Time{} },
		func(records []AttemptRecord) { records[0].ProgressDeadline = time.Time{} },
		func(records []AttemptRecord) {
			records[0].ProgressDeadline = records[0].ProgressDeadline.Add(time.Nanosecond)
		},
		func(records []AttemptRecord) {
			records[0].ProgressedAt = records[0].StartedAt.Add(-time.Nanosecond)
			records[0].ProgressDeadline = records[0].ProgressedAt.Add(time.Minute)
		},
		func(records []AttemptRecord) {
			records[0].ProgressedAt = records[0].Deadline
			records[0].ProgressDeadline = records[0].Deadline
		},
		func(records []AttemptRecord) {
			records[0].ProgressedAt = records[0].ProgressedAt.In(time.FixedZone("tampered", int(time.Hour/time.Second)))
			records[0].ProgressDeadline = records[0].ProgressDeadline.In(time.FixedZone("tampered", int(time.Hour/time.Second)))
		},
		func(records []AttemptRecord) {
			records[0].ProgressedAt = records[0].FinishedAt.Add(time.Nanosecond)
			records[0].ProgressDeadline = records[0].ProgressedAt.Add(time.Minute)
		},
	}
	for index, mutate := range tests {
		attempts := append([]AttemptRecord(nil), validAttempts...)
		mutate(attempts)
		if value, err := RestoreInvocation(InvocationRestoreSpec{Genesis: genesis, Outcomes: validOutcomes, Attempts: attempts}); !errors.Is(err, ErrCorrupt) || !value.IsZero() {
			t.Fatalf("progress tamper %d = (%v, %v)", index, value, err)
		}
	}

	disabledGenesis := testInvocationSpec(t, testInvocationPolicy(t))
	disabled, err := NewInvocation(disabledGenesis)
	if err != nil {
		t.Fatal(err)
	}
	disabled, _ = beginTestAttempt(t, disabled, disabled.EligibleAt())
	disabledAttempts := disabled.AttemptRecords()
	disabledAttempts[0].ProgressedAt = disabledAttempts[0].StartedAt
	disabledAttempts[0].ProgressDeadline = disabledAttempts[0].StartedAt.Add(time.Minute)
	if value, err := RestoreInvocation(InvocationRestoreSpec{Genesis: disabledGenesis, Outcomes: disabled.History(), Attempts: disabledAttempts}); !errors.Is(err, ErrCorrupt) || !value.IsZero() {
		t.Fatalf("disabled progress tamper = (%v, %v)", value, err)
	}
}

func TestInvocationGlobalAttemptCapCannotBeBypassedByFreeRetries(t *testing.T) {
	policy := testInvocationPolicy(t, RetryBackoff(Exponential(time.Second, time.Second, NoJitter)))
	invocation := testInvocationForPolicy(t, policy)
	retry, _ := RetryDisposition(ReasonLeaseLost, PublicFailure{}, 0, RetryCostNone)
	startedAt := invocation.EligibleAt()
	for ordinal := uint16(1); ordinal <= MaxAttemptOrdinal; ordinal++ {
		running, attempt := beginTestAttempt(t, invocation, startedAt)
		finishedAt := startedAt.Add(time.Millisecond)
		availableAt := finishedAt.Add(time.Second)
		if ordinal == MaxAttemptOrdinal {
			availableAt = time.Time{}
		}
		next, _, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: retry, AvailableAt: availableAt})
		if err != nil {
			t.Fatalf("ordinal %d: %v", ordinal, err)
		}
		invocation = next
		if ordinal < MaxAttemptOrdinal {
			if invocation.State() != InvocationQueued {
				t.Fatalf("ordinal %d state = %v", ordinal, invocation.State())
			}
			startedAt = availableAt
		}
	}
	if invocation.State() != InvocationDead || invocation.Outcome().TerminalReason() != ReasonAttemptsExhausted || invocation.AttemptOrdinal().Value() != MaxAttemptOrdinal || invocation.RetrySpent().Value() != 0 || len(invocation.Attempts()) != int(MaxAttemptOrdinal) {
		t.Fatalf("attempt cap = (%v, %v, %d, %d)", invocation.State(), invocation.Outcome().TerminalReason(), invocation.AttemptOrdinal().Value(), len(invocation.Attempts()))
	}
}

func TestRestoreInvocationReplaysAndRejectsEveryLedgerMismatch(t *testing.T) {
	policy := testInvocationPolicy(t,
		Retries(2),
		MaxDeliveryDeferrals(2),
		RetryBackoff(Exponential(time.Second, 2*time.Second, NoJitter)),
	)
	genesis := testInvocationSpec(t, policy)
	invocation, err := NewInvocation(genesis)
	if err != nil {
		t.Fatal(err)
	}
	observedAt := invocation.EligibleAt()
	invocation, err = invocation.DeferDelivery(DeferDeliverySpec{Reason: ReasonAdmission, ObservedAt: observedAt, AvailableAt: observedAt.Add(MinRetryDelay)})
	if err != nil {
		t.Fatal(err)
	}
	running, attempt := beginTestAttempt(t, invocation, invocation.Outcome().AvailableAt())
	retry, _ := RetryDisposition(ReasonHandlerFailure, PublicFailure{}, 0, RetryCostCharged)
	finishedAt := attempt.StartedAt().Add(time.Millisecond)
	invocation, _, err = running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: retry, AvailableAt: finishedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	running, attempt = beginTestAttempt(t, invocation, invocation.Outcome().AvailableAt())
	requestedAt := attempt.StartedAt().Add(time.Second)
	invocation, err = running.RequestCancel(requestedAt)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, _ := CancelledDisposition(ReasonCancelRequested)
	invocation, _, err = invocation.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: requestedAt.Add(time.Second), Disposition: cancelled})
	if err != nil {
		t.Fatal(err)
	}
	outcomes := invocation.History()
	attempts := invocation.AttemptRecords()
	restored, err := RestoreInvocation(InvocationRestoreSpec{Genesis: genesis, Outcomes: outcomes, Attempts: attempts})
	if err != nil {
		t.Fatal(err)
	}
	if restored.State() != invocation.State() || restored.RetrySpent() != invocation.RetrySpent() || restored.DeliveryDeferrals() != invocation.DeliveryDeferrals() || !slices.Equal(restored.History(), outcomes) || !slices.Equal(restored.AttemptRecords(), attempts) {
		t.Fatalf("restored = %+v", restored)
	}
	outcomes[0] = InvocationOutcome{}
	attempts[0] = AttemptRecord{}
	if restored.History()[0].Kind() != InvocationOutcomeInitial || restored.AttemptRecords()[0].Invocation.IsZero() {
		t.Fatal("restore retained caller slices")
	}

	validOutcomes := invocation.History()
	validAttempts := invocation.AttemptRecords()
	tests := []InvocationRestoreSpec{
		{Genesis: genesis},
		{Genesis: genesis, Outcomes: append([]InvocationOutcome(nil), validOutcomes[:len(validOutcomes)-1]...), Attempts: validAttempts},
		{Genesis: genesis, Outcomes: append([]InvocationOutcome(nil), validOutcomes...), Attempts: append([]AttemptRecord(nil), validAttempts[:len(validAttempts)-1]...)},
		{Genesis: genesis, Outcomes: append([]InvocationOutcome(nil), validOutcomes...), Attempts: append(append([]AttemptRecord(nil), validAttempts...), validAttempts[len(validAttempts)-1])},
	}
	wrongGenesis := genesis
	wrongGenesis.Namespace, _ = NamespaceOf("tests", "other")
	wrongGenesis.Partition = partitionKey(wrongGenesis.Namespace, ProducerPartition{})
	tests = append(tests, InvocationRestoreSpec{Genesis: wrongGenesis, Outcomes: validOutcomes, Attempts: validAttempts})
	delayedGenesis := genesis
	delayedGenesis.EligibleAt = delayedGenesis.CreatedAt.Add(MaxRetention + time.Nanosecond)
	tests = append(tests, InvocationRestoreSpec{Genesis: delayedGenesis, Outcomes: validOutcomes, Attempts: validAttempts})
	tamperedOutcome := append([]InvocationOutcome(nil), validOutcomes...)
	tamperedOutcome[1].availableAt = tamperedOutcome[1].availableAt.Add(time.Second)
	tests = append(tests, InvocationRestoreSpec{Genesis: genesis, Outcomes: tamperedOutcome, Attempts: validAttempts})
	tamperedAttempt := append([]AttemptRecord(nil), validAttempts...)
	tamperedAttempt[0].Deadline = tamperedAttempt[0].Deadline.Add(time.Second)
	tests = append(tests, InvocationRestoreSpec{Genesis: genesis, Outcomes: validOutcomes, Attempts: tamperedAttempt})
	for index, spec := range tests {
		if value, err := RestoreInvocation(spec); !errors.Is(err, ErrCorrupt) || !value.IsZero() {
			t.Fatalf("case %d = (%v, %v)", index, value, err)
		}
	}
	formatted := fmt.Sprintf("%+v", InvocationRestoreSpec{Genesis: genesis, Outcomes: validOutcomes, Attempts: validAttempts})
	if formatted != "[job invocation restore]" {
		t.Fatalf("restore format = %q", formatted)
	}
}

func testGenesisInvocation(t *testing.T) Invocation {
	t.Helper()
	return testInvocationForPolicy(t, testInvocationPolicy(t))
}

func testInvocationForPolicy(t *testing.T, policy PolicySnapshot) Invocation {
	t.Helper()
	invocation, err := NewInvocation(testInvocationSpec(t, policy))
	if err != nil {
		t.Fatal(err)
	}
	return invocation
}

func testInvocationPolicy(t *testing.T, options ...Option) PolicySnapshot {
	t.Helper()
	policy, err := NewPolicySnapshot(testPolicy(t, options...))
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func testInvocationSpec(t *testing.T, policy PolicySnapshot) InvocationSpec {
	t.Helper()
	namespace, err := NamespaceOf("tests", "test")
	if err != nil {
		t.Fatal(err)
	}
	id := testModelInvocationID(t)
	definition := testJobName(t, "tests.invocation")
	partition := partitionKey(namespace, ProducerPartition{})
	durable := mustTestDurableContext(t, namespace, partition, definition, policy.Trace())
	intents, err := digestRegularIntents(CurrentIntentDigestPlan(), namespace, partition, definition, id)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2030, 4, 5, 6, 7, 8, 9, time.FixedZone("test", 6*60*60))
	return InvocationSpec{
		ID:         id,
		Namespace:  namespace,
		Partition:  partition,
		Definition: definition,
		Queue:      policy.Queue(),
		Mode:       PlacementRegular,
		Intent:     intents.Current(),
		Priority:   policy.Priority(),
		CreatedAt:  createdAt,
		EligibleAt: createdAt.Add(time.Minute),
		Policy:     policy,
		Context:    durable,
	}
}

func beginTestAttempt(t *testing.T, invocation Invocation, startedAt time.Time) (Invocation, Attempt) {
	t.Helper()
	running, attempt, err := invocation.BeginAttempt(BeginAttemptSpec{Binding: testBindingName(t), Build: testBuildID(t), StartedAt: startedAt})
	if err != nil {
		t.Fatal(err)
	}
	return running, attempt
}
