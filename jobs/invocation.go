package jobs

import (
	"fmt"
	"time"
)

type InvocationState uint8

const (
	InvocationQueued InvocationState = iota + 1
	InvocationRunning
	InvocationSucceeded
	InvocationFailed
	InvocationDead
	InvocationDiscarded
	InvocationQuarantined
	InvocationCancelRequested
	InvocationCancelled
	InvocationTerminated
)

func (s InvocationState) Valid() bool {
	return s >= InvocationQueued && s <= InvocationTerminated
}

func (s InvocationState) Terminal() bool {
	switch s {
	case InvocationSucceeded, InvocationFailed, InvocationDead, InvocationDiscarded, InvocationQuarantined, InvocationCancelled, InvocationTerminated:
		return true
	default:
		return false
	}
}

func (s InvocationState) String() string {
	switch s {
	case InvocationQueued:
		return "queued"
	case InvocationRunning:
		return "running"
	case InvocationSucceeded:
		return "succeeded"
	case InvocationFailed:
		return "failed"
	case InvocationDead:
		return "dead"
	case InvocationDiscarded:
		return "discarded"
	case InvocationQuarantined:
		return "quarantined"
	case InvocationCancelRequested:
		return "cancel_requested"
	case InvocationCancelled:
		return "cancelled"
	case InvocationTerminated:
		return "terminated"
	default:
		return "unknown"
	}
}

type InvocationSpec struct {
	ID           InvocationID
	Namespace    Namespace
	Partition    PartitionKey
	Definition   Name
	Queue        QueueName
	Mode         PlacementMode
	Intent       IntentKey
	LegacyIntent LegacyIntent
	Priority     int
	CreatedAt    time.Time
	EligibleAt   time.Time
	StartBefore  time.Time
	Policy       PolicySnapshot
	Context      DurableContext
}

func (s InvocationSpec) String() string { return "[job invocation specification]" }
func (s InvocationSpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}

type DeferDeliverySpec struct {
	Reason      Reason
	Failure     PublicFailure
	ObservedAt  time.Time
	AvailableAt time.Time
}

func (s DeferDeliverySpec) String() string { return "[job delivery deferral]" }
func (s DeferDeliverySpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}

type FinishDeliverySpec struct {
	State      InvocationState
	Reason     Reason
	Failure    PublicFailure
	ObservedAt time.Time
}

func (s FinishDeliverySpec) String() string { return "[job delivery finish]" }
func (s FinishDeliverySpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}

type invocationOutcomeLedger struct {
	previous *invocationOutcomeLedger
	value    InvocationOutcome
	length   int
}

type attemptLedger struct {
	previous *attemptLedger
	value    Attempt
	length   int
}

type Invocation struct {
	id                InvocationID
	namespace         Namespace
	partition         PartitionKey
	definition        Name
	queue             QueueName
	mode              PlacementMode
	intent            IntentKey
	legacyIntent      LegacyIntent
	priority          int
	state             InvocationState
	createdAt         time.Time
	eligibleAt        time.Time
	startBefore       time.Time
	maxElapsedAt      time.Time
	finishedAt        time.Time
	attemptOrdinal    AttemptOrdinal
	retrySpent        RetrySpent
	handlerDeferrals  HandlerDeferrals
	deliveryDeferrals DeliveryDeferrals
	cancelRequestedAt time.Time
	policy            PolicySnapshot
	context           DurableContext
	history           *invocationOutcomeLedger
	attempts          *attemptLedger
}

func NewInvocation(spec InvocationSpec) (Invocation, error) {
	if !spec.ID.valid() || !spec.Namespace.valid() || !spec.Partition.validFor(spec.Namespace) || !spec.Definition.valid() || !spec.Queue.valid() || !spec.Mode.Valid() || !spec.Intent.validFor(spec.Namespace, spec.Partition, spec.Definition) || spec.Priority <= 0 || spec.Priority > MaximumPriority || !spec.Policy.valid() || !spec.Context.validFor(spec.Namespace, spec.Partition, spec.Definition, spec.Policy.Trace()) || spec.Queue != spec.Policy.Queue() {
		return Invocation{}, invalid("invocation identity or policy")
	}
	purpose := spec.Intent.Purpose()
	if spec.Mode == PlacementRegular && purpose != IntentRegular || spec.Mode == PlacementOnce && purpose != IntentOnce || (spec.Mode == PlacementCollapse || spec.Mode == PlacementDebounce) && purpose != IntentCollapse {
		return Invocation{}, invalid("invocation placement intent")
	}
	if spec.Mode == PlacementRegular {
		value := spec.ID.Bytes()
		if spec.Intent.Digest() != digestIntentValue(spec.Intent.Revision(), IntentRegular, spec.Namespace, spec.Partition, spec.Definition, value[:]) {
			return Invocation{}, invalid("invocation regular intent")
		}
	}
	if !spec.LegacyIntent.IsZero() {
		intents, err := NewIntentDigests(spec.Intent)
		if err != nil || spec.Mode == PlacementRegular || !spec.LegacyIntent.valid() || !producerIntentDigestsMatch(spec.Namespace, spec.Partition, spec.Definition, spec.LegacyIntent, intents) {
			return Invocation{}, invalid("invocation legacy intent")
		}
	}
	createdAt, err := requiredTime(spec.CreatedAt, "invocation creation")
	if err != nil {
		return Invocation{}, err
	}
	eligibleAt, err := requiredTime(spec.EligibleAt, "invocation eligibility")
	if err != nil {
		return Invocation{}, err
	}
	if delay := eligibleAt.Sub(createdAt); delay < 0 || delay > MaxRetention {
		return Invocation{}, invalid("invocation eligibility")
	}
	maxElapsedAt, err := requiredTime(eligibleAt.Add(spec.Policy.MaxElapsed()), "invocation elapsed deadline")
	if err != nil {
		return Invocation{}, err
	}
	startBefore, err := optionalTime(spec.StartBefore, "invocation start deadline")
	if err != nil {
		return Invocation{}, err
	}
	if !startBefore.IsZero() && (startBefore.Before(eligibleAt) || startBefore.After(maxElapsedAt)) {
		return Invocation{}, invalid("invocation start deadline")
	}
	initial := InitialInvocationOutcome()
	return Invocation{
		id:           spec.ID,
		namespace:    spec.Namespace,
		partition:    spec.Partition,
		definition:   spec.Definition,
		queue:        spec.Queue,
		mode:         spec.Mode,
		intent:       spec.Intent,
		legacyIntent: spec.LegacyIntent,
		priority:     spec.Priority,
		state:        InvocationQueued,
		createdAt:    createdAt,
		eligibleAt:   eligibleAt,
		startBefore:  startBefore,
		maxElapsedAt: maxElapsedAt,
		policy:       spec.Policy,
		context:      spec.Context,
		history:      &invocationOutcomeLedger{value: initial, length: 1},
	}, nil
}

func (i Invocation) ID() InvocationID           { return i.id }
func (i Invocation) Namespace() Namespace       { return i.namespace }
func (i Invocation) Partition() PartitionKey    { return i.partition }
func (i Invocation) Definition() Name           { return i.definition }
func (i Invocation) Queue() QueueName           { return i.queue }
func (i Invocation) Mode() PlacementMode        { return i.mode }
func (i Invocation) Intent() IntentKey          { return i.intent }
func (i Invocation) IntentDigest() IntentDigest { return i.intent.Digest() }
func (i Invocation) LegacyIntent() (LegacyIntent, bool) {
	return i.legacyIntent, !i.legacyIntent.IsZero()
}
func (i Invocation) Priority() int                        { return i.priority }
func (i Invocation) State() InvocationState               { return i.state }
func (i Invocation) CreatedAt() time.Time                 { return i.createdAt }
func (i Invocation) EligibleAt() time.Time                { return i.eligibleAt }
func (i Invocation) StartBefore() time.Time               { return i.startBefore }
func (i Invocation) MaxElapsedAt() time.Time              { return i.maxElapsedAt }
func (i Invocation) FinishedAt() time.Time                { return i.finishedAt }
func (i Invocation) AttemptOrdinal() AttemptOrdinal       { return i.attemptOrdinal }
func (i Invocation) RetrySpent() RetrySpent               { return i.retrySpent }
func (i Invocation) HandlerDeferrals() HandlerDeferrals   { return i.handlerDeferrals }
func (i Invocation) DeliveryDeferrals() DeliveryDeferrals { return i.deliveryDeferrals }
func (i Invocation) CancelRequestedAt() time.Time         { return i.cancelRequestedAt }
func (i Invocation) Policy() PolicySnapshot               { return i.policy }
func (i Invocation) Context() DurableContext              { return i.context }
func (i Invocation) IsTerminal() bool                     { return i.state.Terminal() }
func (i Invocation) IsZero() bool                         { return i.id.IsZero() }
func (i Invocation) Outcome() InvocationOutcome {
	if i.history == nil {
		return InvocationOutcome{}
	}
	return i.history.value
}
func (i Invocation) History() []InvocationOutcome {
	if i.history == nil {
		return nil
	}
	result := make([]InvocationOutcome, i.history.length)
	for current, index := i.history, len(result)-1; current != nil; current, index = current.previous, index-1 {
		result[index] = current.value
	}
	return result
}
func (i Invocation) Attempts() []Attempt {
	if i.attempts == nil {
		return nil
	}
	result := make([]Attempt, i.attempts.length)
	for current, index := i.attempts, len(result)-1; current != nil; current, index = current.previous, index-1 {
		result[index] = current.value
	}
	return result
}
func (i Invocation) AttemptRecords() []AttemptRecord {
	attempts := i.Attempts()
	result := make([]AttemptRecord, len(attempts))
	for index := range attempts {
		result[index] = attempts[index].Record()
	}
	return result
}
func (i Invocation) String() string { return "[job invocation]" }
func (i Invocation) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, i.String())
}

func (i Invocation) BeginAttempt(spec BeginAttemptSpec) (Invocation, Attempt, error) {
	if i.IsZero() || i.state != InvocationQueued {
		return Invocation{}, Attempt{}, transitionConflict("invocation cannot begin an attempt")
	}
	if !spec.Binding.valid() || !spec.Build.valid() {
		return Invocation{}, Attempt{}, invalid("attempt binding or build")
	}
	startedAt, err := requiredTime(spec.StartedAt, "attempt start")
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	if startedAt.Before(i.readyAt()) || i.deadlineReason(startedAt) != ReasonNone || i.attemptOrdinal.Value() >= MaxAttemptOrdinal {
		return Invocation{}, Attempt{}, transitionConflict("attempt start is outside invocation bounds")
	}
	ordinal, _ := NewAttemptOrdinal(i.attemptOrdinal.Value() + 1)
	deadline, err := requiredTime(startedAt.Add(i.policy.AttemptTimeout()), "attempt deadline")
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	if deadline.After(i.maxElapsedAt) {
		deadline = i.maxElapsedAt
	}
	progressedAt := time.Time{}
	progressDeadline := time.Time{}
	if i.policy.ProgressTimeout() > 0 {
		progressedAt = startedAt
		progressDeadline, err = requiredTime(startedAt.Add(i.policy.ProgressTimeout()), "attempt progress deadline")
		if err != nil {
			return Invocation{}, Attempt{}, err
		}
		if progressDeadline.After(deadline) {
			progressDeadline = deadline
		}
	}
	attempt := Attempt{
		invocation:       i.id,
		ordinal:          ordinal,
		binding:          spec.Binding,
		build:            spec.Build,
		state:            AttemptRunning,
		startedAt:        startedAt,
		deadline:         deadline,
		progressedAt:     progressedAt,
		progressDeadline: progressDeadline,
	}
	outcome, err := ActiveAttemptOutcome(ordinal, startedAt)
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	result := i
	result.state = InvocationRunning
	result.attemptOrdinal = ordinal
	result.cancelRequestedAt = time.Time{}
	result.attempts = &attemptLedger{previous: i.attempts, value: attempt, length: int(ordinal.Value())}
	result, err = result.appendOutcome(outcome)
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	return result, attempt, nil
}

func (i Invocation) beginAttemptOrExpire(spec BeginAttemptSpec) (Invocation, Attempt, error) {
	startedAt, err := requiredTime(spec.StartedAt, "attempt start")
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	if i.IsZero() || i.state != InvocationQueued || startedAt.Before(i.readyAt()) {
		return Invocation{}, Attempt{}, transitionConflict("invocation cannot begin an attempt")
	}
	if i.deadlineReason(startedAt) == ReasonNone {
		return i.BeginAttempt(spec)
	}
	expired, err := i.Expire(startedAt)
	return expired, Attempt{}, err
}

func (i Invocation) FinishAttempt(attempt Attempt, spec FinishAttemptSpec) (Invocation, Attempt, error) {
	if i.IsZero() || i.attempts == nil || attempt.state != AttemptRunning || !sameAttemptToken(i.attempts.value, attempt) || attempt.invocation != i.id || attempt.ordinal != i.attemptOrdinal {
		return Invocation{}, Attempt{}, transitionConflict("attempt token is not active")
	}
	if i.state != InvocationRunning && i.state != InvocationCancelRequested {
		return Invocation{}, Attempt{}, transitionConflict("invocation has no finishable attempt")
	}
	if !spec.Disposition.valid() || !spec.Disposition.allowedForAttempt() || spec.Disposition.kind == DispositionTerminated {
		return Invocation{}, Attempt{}, invalid("attempt disposition")
	}
	finishedAt, err := requiredTime(spec.FinishedAt, "attempt finish")
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	active := i.attempts.value
	if finishedAt.Before(active.startedAt) || !active.progressedAt.IsZero() && finishedAt.Before(active.progressedAt) {
		return Invocation{}, Attempt{}, invalid("attempt finish")
	}
	availableAt, err := optionalTime(spec.AvailableAt, "attempt availability")
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	decision, err := i.decideAttempt(active, spec.Disposition, finishedAt, availableAt)
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	return i.applyAttemptDecision(active, spec.Disposition, finishedAt, decision)
}

func (i Invocation) finishAttemptAuthoritatively(attempt Attempt, disposition Disposition, finishedAt time.Time, proposedDelay, deadlineRetryDelay time.Duration) (Invocation, Attempt, error) {
	if err := i.validateTimeoutRetryDelay(deadlineRetryDelay); err != nil {
		return Invocation{}, Attempt{}, err
	}
	if i.state == InvocationCancelRequested {
		cancelled, err := CancelledDisposition(ReasonCancelRequested)
		if err != nil {
			return Invocation{}, Attempt{}, err
		}
		return i.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: cancelled})
	}
	if i.state != InvocationRunning {
		return i.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: disposition})
	}
	reason := i.attemptFinishDeadlineReason(attempt, disposition, finishedAt)
	if reason == ReasonMaxElapsed {
		return i.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: disposition})
	}
	if reason != ReasonAttemptTimeout && reason != ReasonProgressTimeout {
		availableAt, err := relativeDeliveryTime(finishedAt, proposedDelay)
		if err != nil {
			return Invocation{}, Attempt{}, err
		}
		return i.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: disposition, AvailableAt: availableAt})
	}
	timeout, err := RetryDisposition(reason, PublicFailure{}, 0, RetryCostCharged)
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	decision, terminal := i.attemptRescheduleLimit(timeout, finishedAt, attemptDecision{retrySpent: i.retrySpent, handlerDeferrals: i.handlerDeferrals})
	if terminal {
		return i.applyAttemptDecision(attempt, timeout, finishedAt, decision)
	}
	delay := deadlineRetryDelay
	if delay >= i.maxElapsedAt.Sub(finishedAt) {
		decision.state = InvocationDead
		decision.terminalReason = ReasonMaxElapsed
		decision.availableAt = i.maxElapsedAt
		return i.applyAttemptDecision(attempt, timeout, finishedAt, decision)
	}
	availableAt, err := requiredTime(finishedAt.Add(delay), "attempt timeout availability")
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	return i.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: finishedAt, Disposition: timeout, AvailableAt: availableAt})
}

func (i Invocation) arbitrateAttemptDeadline(attempt Attempt, observedAt time.Time, deadlineRetryDelay time.Duration) (Invocation, Attempt, bool, error) {
	if i.IsZero() || i.attempts == nil || attempt.state != AttemptRunning || !sameAttemptToken(i.attempts.value, attempt) || i.state != InvocationRunning && i.state != InvocationCancelRequested {
		return Invocation{}, Attempt{}, false, transitionConflict("attempt token is not active")
	}
	if err := i.validateTimeoutRetryDelay(deadlineRetryDelay); err != nil {
		return Invocation{}, Attempt{}, false, err
	}
	if observedAt.Before(i.latestOccurredAt()) {
		return Invocation{}, Attempt{}, false, invalid("attempt deadline observation")
	}
	deadline, reason := attemptRuntimeDeadline(attempt)
	if observedAt.Before(deadline) {
		return i, attempt, false, nil
	}
	if i.state == InvocationCancelRequested {
		result, finished, err := i.terminateCancelledAttemptAtDeadline(attempt, observedAt)
		if err != nil {
			return Invocation{}, Attempt{}, false, err
		}
		return result, finished, true, nil
	}
	timeout, err := RetryDisposition(reason, PublicFailure{}, 0, RetryCostCharged)
	if err != nil {
		return Invocation{}, Attempt{}, false, err
	}
	result, finished, err := i.finishAttemptAuthoritatively(attempt, timeout, observedAt, 0, deadlineRetryDelay)
	if err != nil {
		return Invocation{}, Attempt{}, false, err
	}
	return result, finished, true, nil
}

func (i Invocation) terminateCancelledAttemptAtDeadline(attempt Attempt, observedAt time.Time) (Invocation, Attempt, error) {
	if i.IsZero() || i.state != InvocationCancelRequested || i.attempts == nil || attempt.state != AttemptRunning || !sameAttemptToken(i.attempts.value, attempt) || attempt.invocation != i.id || attempt.ordinal != i.attemptOrdinal {
		return Invocation{}, Attempt{}, transitionConflict("attempt token is not active")
	}
	observedAt, err := requiredTime(observedAt, "attempt deadline observation")
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	deadline, _ := attemptRuntimeDeadline(attempt)
	if observedAt.Before(deadline) || observedAt.Before(i.latestOccurredAt()) {
		return Invocation{}, Attempt{}, transitionConflict("attempt termination deadline has not elapsed")
	}
	return i.applyAttemptDecision(attempt, cancellationTerminatedDisposition(), observedAt, attemptDecision{state: InvocationTerminated, retrySpent: i.retrySpent, handlerDeferrals: i.handlerDeferrals})
}

func (i Invocation) RecordProgress(attempt Attempt, at time.Time) (Invocation, Attempt, error) {
	if i.IsZero() || i.attempts == nil || attempt.state != AttemptRunning || !sameAttemptToken(i.attempts.value, attempt) || i.state != InvocationRunning && i.state != InvocationCancelRequested {
		return Invocation{}, Attempt{}, transitionConflict("attempt token is not active")
	}
	active := i.attempts.value
	if active.progressDeadline.IsZero() || i.policy.ProgressTimeout() == 0 {
		return Invocation{}, Attempt{}, ErrUnsupported
	}
	at, err := requiredTime(at, "attempt progress time")
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	lowerBound := active.progressedAt
	if i.state == InvocationCancelRequested && i.cancelRequestedAt.After(lowerBound) {
		lowerBound = i.cancelRequestedAt
	}
	if at.Before(lowerBound) {
		return Invocation{}, Attempt{}, invalid("attempt progress time")
	}
	if at.Equal(active.progressedAt) {
		return i, active, nil
	}
	if !at.Before(active.progressDeadline) || !at.Before(active.deadline) {
		return Invocation{}, Attempt{}, transitionConflict("attempt progress deadline elapsed")
	}
	progressDeadline, err := requiredTime(at.Add(i.policy.ProgressTimeout()), "attempt progress deadline")
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	if progressDeadline.After(active.deadline) {
		progressDeadline = active.deadline
	}
	progressed := active
	progressed.progressedAt = at
	progressed.progressDeadline = progressDeadline
	result := i
	result.attempts = &attemptLedger{previous: i.attempts.previous, value: progressed, length: i.attempts.length}
	return result, progressed, nil
}

func (i Invocation) DeferDelivery(spec DeferDeliverySpec) (Invocation, error) {
	if i.IsZero() || i.state != InvocationQueued {
		return Invocation{}, transitionConflict("invocation cannot defer delivery")
	}
	if !deliveryDeferralReason(spec.Reason) || !validOptionalFailure(spec.Failure) {
		return Invocation{}, invalid("delivery deferral")
	}
	observedAt, err := requiredTime(spec.ObservedAt, "delivery deferral time")
	if err != nil {
		return Invocation{}, err
	}
	if observedAt.Before(i.readyAt()) {
		return Invocation{}, transitionConflict("delivery deferral precedes eligibility")
	}
	reason := i.deadlineReason(observedAt)
	if reason != ReasonNone {
		return i.finishDeliveryDecision(InvocationDead, spec.Reason, reason, spec.Failure, observedAt, time.Time{})
	}
	if i.attemptOrdinal.Value() == MaxAttemptOrdinal {
		return i.finishDeliveryDecision(InvocationDead, spec.Reason, ReasonAttemptsExhausted, spec.Failure, observedAt, time.Time{})
	}
	if i.deliveryDeferrals.Value() == i.policy.DeliveryDeferralLimit().Value() {
		return i.finishDeliveryDecision(InvocationDead, spec.Reason, ReasonDeferralsExhausted, spec.Failure, observedAt, time.Time{})
	}
	availableAt, err := requiredTime(spec.AvailableAt, "delivery deferral availability")
	if err != nil {
		return Invocation{}, err
	}
	if err := validateBoundedDelay(observedAt, availableAt); err != nil {
		return Invocation{}, err
	}
	if reason = i.deadlineReason(availableAt); reason != ReasonNone {
		return i.finishDeliveryDecision(InvocationDead, spec.Reason, reason, spec.Failure, observedAt, availableAt)
	}
	outcome, err := DeferredDeliveryOutcome(spec.Reason, spec.Failure, observedAt, availableAt)
	if err != nil {
		return Invocation{}, err
	}
	deferrals, _ := NewDeliveryDeferrals(i.deliveryDeferrals.Value() + 1)
	result := i
	result.deliveryDeferrals = deferrals
	result, err = result.appendOutcome(outcome)
	if err != nil {
		return Invocation{}, err
	}
	return result, nil
}

func (i Invocation) releaseUnchanged(source Reason, observedAt, availableAt time.Time) (Invocation, error) {
	if i.IsZero() || i.state != InvocationQueued {
		return Invocation{}, transitionConflict("invocation cannot release unchanged")
	}
	if source != ReasonCompatibility && source != ReasonShutdown {
		return Invocation{}, invalid("unchanged release reason")
	}
	observedAt, err := requiredTime(observedAt, "unchanged release time")
	if err != nil {
		return Invocation{}, err
	}
	if observedAt.Before(i.readyAt()) {
		return Invocation{}, transitionConflict("unchanged release precedes eligibility")
	}
	if reason := i.deadlineReason(observedAt); reason != ReasonNone {
		return i.finishDeliveryDecision(InvocationDead, source, reason, PublicFailure{}, observedAt, time.Time{})
	}
	availableAt, err = requiredTime(availableAt, "unchanged release availability")
	if err != nil {
		return Invocation{}, err
	}
	if reason := i.deadlineReason(availableAt); reason != ReasonNone {
		if err := i.validateReleaseDeadlineAvailability(observedAt, availableAt); err != nil {
			return Invocation{}, err
		}
		return i.finishDeliveryDecision(InvocationDead, source, reason, PublicFailure{}, observedAt, availableAt)
	}
	if err := validateBoundedDelay(observedAt, availableAt); err != nil {
		return Invocation{}, err
	}
	return i, nil
}

func (i Invocation) validateReleaseDeadlineAvailability(observedAt, availableAt time.Time) error {
	delay := availableAt.Sub(observedAt)
	if delay >= MinRetryDelay && delay <= MaxRetryDelay {
		return nil
	}
	deadline, _ := i.deliveryDeadline()
	if delay < 0 || delay >= MinRetryDelay || availableAt != deadline {
		return invalid("unchanged release deadline")
	}
	if _, err := requiredTime(observedAt.Add(MaxRetryDelay), "unchanged release maximum availability"); err == nil {
		return invalid("unchanged release deadline")
	}
	return nil
}

func (i Invocation) FinishDelivery(spec FinishDeliverySpec) (Invocation, error) {
	if i.IsZero() || i.state != InvocationQueued {
		return Invocation{}, transitionConflict("invocation cannot finish delivery")
	}
	if spec.State != InvocationDiscarded && spec.State != InvocationQuarantined || spec.Reason != ReasonPayload && spec.Reason != ReasonCompatibility || !validOptionalFailure(spec.Failure) {
		return Invocation{}, invalid("delivery terminal result")
	}
	observedAt, err := requiredTime(spec.ObservedAt, "delivery terminal time")
	if err != nil {
		return Invocation{}, err
	}
	if observedAt.Before(i.readyAt()) {
		return Invocation{}, transitionConflict("delivery terminal result precedes eligibility")
	}
	if reason := i.deadlineReason(observedAt); reason != ReasonNone {
		return i.finishDeliveryDecision(InvocationDead, spec.Reason, reason, spec.Failure, observedAt, time.Time{})
	}
	return i.finishDeliveryDecision(spec.State, spec.Reason, spec.Reason, spec.Failure, observedAt, time.Time{})
}

func (i Invocation) RequestCancel(at time.Time) (Invocation, error) {
	if i.IsZero() || i.state.Terminal() || i.state == InvocationCancelRequested {
		return Invocation{}, transitionConflict("invocation cannot request cancellation")
	}
	at, err := requiredTime(at, "cancellation request time")
	if err != nil {
		return Invocation{}, err
	}
	if at.Before(i.latestOccurredAt()) {
		return Invocation{}, invalid("cancellation request time")
	}
	if i.state == InvocationQueued {
		return i.finishDeliveryDecision(InvocationCancelled, ReasonCancelRequested, ReasonCancelRequested, PublicFailure{}, at, time.Time{})
	}
	if i.state != InvocationRunning || i.attempts == nil {
		return Invocation{}, transitionConflict("invocation cannot request cancellation")
	}
	outcome, err := CancelRequestedOutcome(i.attemptOrdinal, at)
	if err != nil {
		return Invocation{}, err
	}
	result := i
	result.state = InvocationCancelRequested
	result.cancelRequestedAt = at
	result, err = result.appendOutcome(outcome)
	if err != nil {
		return Invocation{}, err
	}
	return result, nil
}

func (i Invocation) Terminate(at time.Time) (Invocation, error) {
	if i.IsZero() || i.state.Terminal() {
		return Invocation{}, transitionConflict("invocation cannot be terminated")
	}
	at, err := requiredTime(at, "termination time")
	if err != nil {
		return Invocation{}, err
	}
	if at.Before(i.latestOccurredAt()) {
		return Invocation{}, invalid("termination time")
	}
	if i.state == InvocationQueued {
		return i.finishDeliveryDecision(InvocationTerminated, ReasonOperatorTerminated, ReasonOperatorTerminated, PublicFailure{}, at, time.Time{})
	}
	if i.attempts == nil {
		return Invocation{}, invalid("termination time")
	}
	result, _, err := i.applyAttemptDecision(i.attempts.value, TerminatedDisposition(), at, attemptDecision{state: InvocationTerminated, retrySpent: i.retrySpent, handlerDeferrals: i.handlerDeferrals})
	return result, err
}

func (i Invocation) Expire(at time.Time) (Invocation, error) {
	if i.IsZero() || i.state != InvocationQueued {
		return Invocation{}, transitionConflict("invocation cannot expire")
	}
	at, err := requiredTime(at, "expiration time")
	if err != nil {
		return Invocation{}, err
	}
	reason := i.deadlineReason(at)
	if reason == ReasonNone {
		return Invocation{}, transitionConflict("invocation deadline has not elapsed")
	}
	return i.finishDeliveryDecision(InvocationDead, reason, reason, PublicFailure{}, at, time.Time{})
}

type attemptDecision struct {
	state            InvocationState
	terminalReason   Reason
	availableAt      time.Time
	retrySpent       RetrySpent
	handlerDeferrals HandlerDeferrals
}

func (i Invocation) decideAttempt(attempt Attempt, disposition Disposition, finishedAt, availableAt time.Time) (attemptDecision, error) {
	decision := attemptDecision{retrySpent: i.retrySpent, handlerDeferrals: i.handlerDeferrals}
	if i.state == InvocationCancelRequested {
		if disposition.kind != DispositionCancelled || finishedAt.Before(i.cancelRequestedAt) || !availableAt.IsZero() {
			return attemptDecision{}, transitionConflict("attempt did not acknowledge cancellation")
		}
		decision.state = InvocationCancelled
		return decision, nil
	}
	if disposition.kind == DispositionCancelled {
		return attemptDecision{}, transitionConflict("attempt cancellation was not requested")
	}
	if reason := i.attemptFinishDeadlineReason(attempt, disposition, finishedAt); reason != ReasonNone {
		if reason == ReasonAttemptTimeout || reason == ReasonProgressTimeout {
			if disposition.reason != reason {
				return attemptDecision{}, invalid("attempt completed after its deadline")
			}
		} else {
			if disposition.reason == ReasonAttemptTimeout || disposition.reason == ReasonProgressTimeout {
				_, timeoutReason := attemptRuntimeDeadline(attempt)
				if disposition.reason != timeoutReason {
					return attemptDecision{}, invalid("attempt timeout reason")
				}
			}
			decision.state = InvocationDead
			decision.terminalReason = reason
			return decision, nil
		}
	} else if disposition.reason == ReasonAttemptTimeout || disposition.reason == ReasonProgressTimeout {
		return attemptDecision{}, invalid("attempt timeout occurred before its deadline")
	}
	switch disposition.kind {
	case DispositionSucceeded:
		if !availableAt.IsZero() {
			return attemptDecision{}, invalid("successful attempt availability")
		}
		decision.state = InvocationSucceeded
	case DispositionPermanentFailure:
		if !availableAt.IsZero() {
			return attemptDecision{}, invalid("failed attempt availability")
		}
		decision.state = InvocationFailed
	case DispositionDiscard:
		if !availableAt.IsZero() {
			return attemptDecision{}, invalid("discarded attempt availability")
		}
		decision.state = InvocationDiscarded
	case DispositionQuarantine:
		if !availableAt.IsZero() {
			return attemptDecision{}, invalid("quarantined attempt availability")
		}
		decision.state = InvocationQuarantined
	case DispositionRetry, DispositionDeferred:
		return i.decideAttemptReschedule(disposition, finishedAt, availableAt, decision)
	default:
		return attemptDecision{}, invalid("attempt disposition")
	}
	return decision, nil
}

func (i Invocation) decideAttemptReschedule(disposition Disposition, finishedAt, availableAt time.Time, decision attemptDecision) (attemptDecision, error) {
	if limited, terminal := i.attemptRescheduleLimit(disposition, finishedAt, decision); terminal {
		return limited, nil
	}
	if availableAt.IsZero() {
		return attemptDecision{}, invalid("rescheduled attempt availability")
	}
	if err := i.validateAttemptDelay(disposition, finishedAt, availableAt); err != nil {
		return attemptDecision{}, err
	}
	if reason := i.deadlineReason(availableAt); reason != ReasonNone {
		decision.state = InvocationDead
		decision.terminalReason = reason
		decision.availableAt = availableAt
		return decision, nil
	}
	decision.state = InvocationQueued
	decision.availableAt = availableAt
	if disposition.kind == DispositionRetry && disposition.retryCost == RetryCostCharged {
		decision.retrySpent, _ = NewRetrySpent(i.retrySpent.Value() + 1)
	}
	if disposition.kind == DispositionDeferred {
		decision.handlerDeferrals, _ = NewHandlerDeferrals(i.handlerDeferrals.Value() + 1)
	}
	return decision, nil
}

func (i Invocation) attemptRescheduleLimit(disposition Disposition, finishedAt time.Time, decision attemptDecision) (attemptDecision, bool) {
	if reason := i.deadlineReason(finishedAt); reason != ReasonNone {
		decision.state = InvocationDead
		decision.terminalReason = reason
		return decision, true
	}
	if i.attemptOrdinal.Value() == MaxAttemptOrdinal {
		decision.state = InvocationDead
		decision.terminalReason = ReasonAttemptsExhausted
		return decision, true
	}
	if disposition.kind == DispositionRetry && disposition.retryCost == RetryCostCharged && i.retrySpent.Value() == i.policy.RetryLimit().Value() {
		decision.state = InvocationDead
		decision.terminalReason = ReasonRetryExhausted
		return decision, true
	}
	if disposition.kind == DispositionDeferred && i.handlerDeferrals.Value() == i.policy.HandlerDeferralLimit().Value() {
		decision.state = InvocationDead
		decision.terminalReason = ReasonDeferralsExhausted
		return decision, true
	}
	return decision, false
}

func (i Invocation) applyAttemptDecision(attempt Attempt, disposition Disposition, finishedAt time.Time, decision attemptDecision) (Invocation, Attempt, error) {
	finished := attempt
	finished.state = AttemptFinished
	finished.finishedAt = finishedAt
	finished.disposition = disposition
	outcome, err := FinishedAttemptOutcome(attempt.ordinal, disposition, decision.terminalReason, finishedAt, decision.availableAt)
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	result := i
	result.state = decision.state
	result.retrySpent = decision.retrySpent
	result.handlerDeferrals = decision.handlerDeferrals
	result.cancelRequestedAt = time.Time{}
	result.attempts = &attemptLedger{previous: i.attempts.previous, value: finished, length: i.attempts.length}
	if decision.state.Terminal() {
		result.finishedAt = finishedAt
	}
	result, err = result.appendOutcome(outcome)
	if err != nil {
		return Invocation{}, Attempt{}, err
	}
	return result, finished, nil
}

func (i Invocation) finishDeliveryDecision(state InvocationState, source, terminalReason Reason, failure PublicFailure, observedAt, availableAt time.Time) (Invocation, error) {
	outcome, err := TerminalDeliveryOutcome(state, source, terminalReason, failure, observedAt, availableAt)
	if err != nil {
		return Invocation{}, err
	}
	result := i
	result.state = state
	result.finishedAt = observedAt
	result.cancelRequestedAt = time.Time{}
	result, err = result.appendOutcome(outcome)
	if err != nil {
		return Invocation{}, err
	}
	return result, nil
}

func (i Invocation) appendOutcome(outcome InvocationOutcome) (Invocation, error) {
	length := 0
	if i.history != nil {
		length = i.history.length
	}
	if length >= MaxInvocationOutcomes {
		return Invocation{}, tooLarge("invocation outcome ledger")
	}
	i.history = &invocationOutcomeLedger{previous: i.history, value: outcome, length: length + 1}
	return i, nil
}

func (i Invocation) readyAt() time.Time {
	if i.history == nil {
		return time.Time{}
	}
	switch i.history.value.kind {
	case InvocationOutcomeInitial:
		return i.eligibleAt
	case InvocationOutcomeAttemptFinished, InvocationOutcomeDeliveryDeferred:
		return i.history.value.availableAt
	default:
		return time.Time{}
	}
}

func (i Invocation) latestOccurredAt() time.Time {
	latest := i.createdAt
	if i.history != nil && !i.history.value.occurredAt.IsZero() {
		latest = i.history.value.occurredAt
	}
	if i.attempts != nil && i.attempts.value.progressedAt.After(latest) {
		latest = i.attempts.value.progressedAt
	}
	return latest
}

func (i Invocation) deadlineReason(at time.Time) Reason {
	if !at.Before(i.maxElapsedAt) {
		return ReasonMaxElapsed
	}
	if i.attemptOrdinal.IsZero() && !i.startBefore.IsZero() && !at.Before(i.startBefore) {
		return ReasonStartBefore
	}
	return ReasonNone
}

func (i Invocation) deliveryDeadline() (time.Time, Reason) {
	deadline := i.maxElapsedAt
	reason := ReasonMaxElapsed
	if i.attemptOrdinal.IsZero() && !i.startBefore.IsZero() && i.startBefore.Before(deadline) {
		deadline = i.startBefore
		reason = ReasonStartBefore
	}
	return deadline, reason
}

func (i Invocation) attemptFinishDeadlineReason(attempt Attempt, disposition Disposition, finishedAt time.Time) Reason {
	if !finishedAt.Before(i.maxElapsedAt) {
		return ReasonMaxElapsed
	}
	deadline, reason := attemptRuntimeDeadline(attempt)
	if !finishedAt.Before(deadline) {
		return reason
	}
	if disposition.kind == DispositionRetry || disposition.kind == DispositionDeferred {
		return i.deadlineReason(finishedAt)
	}
	return ReasonNone
}

func attemptRuntimeDeadline(attempt Attempt) (time.Time, Reason) {
	if !attempt.progressDeadline.IsZero() && attempt.progressDeadline.Before(attempt.deadline) {
		return attempt.progressDeadline, ReasonProgressTimeout
	}
	return attempt.deadline, ReasonAttemptTimeout
}

func sameAttemptToken(left, right Attempt) bool {
	return left.invocation == right.invocation && left.ordinal == right.ordinal && left.binding == right.binding && left.build == right.build && left.startedAt == right.startedAt && left.deadline == right.deadline
}

func (i Invocation) validateAttemptDelay(disposition Disposition, observedAt, availableAt time.Time) error {
	delay := availableAt.Sub(observedAt)
	if disposition.kind == DispositionDeferred {
		if delay != disposition.retryAfter {
			return invalid("handler deferral delay")
		}
		return nil
	}
	cap := retryBackoffCap(i.policy.Backoff(), i.retrySpent.Value())
	if disposition.retryAfter > cap {
		cap = disposition.retryAfter
	}
	if i.policy.Backoff().Jitter == NoJitter {
		if delay != cap {
			return invalid("retry backoff delay")
		}
		return nil
	}
	minimum := MinRetryDelay
	if disposition.retryAfter > minimum {
		minimum = disposition.retryAfter
	}
	if delay < minimum || delay > cap {
		return invalid("retry backoff delay")
	}
	return nil
}

func (i Invocation) validateTimeoutRetryDelay(delay time.Duration) error {
	if !validTimeoutRetryDelay(i.policy.Backoff(), i.retrySpent.Value(), delay) {
		return invalid("attempt timeout retry delay")
	}
	return nil
}

func validTimeoutRetryDelay(backoff BackoffPolicy, retrySpent uint16, delay time.Duration) bool {
	cap := retryBackoffCap(backoff, retrySpent)
	if backoff.Jitter == NoJitter {
		return delay == cap
	}
	return delay >= MinRetryDelay && delay <= cap
}

func retryBackoffCap(backoff BackoffPolicy, spent uint16) time.Duration {
	result := backoff.Initial
	for current := uint16(0); current < spent && result < backoff.Maximum; current++ {
		if result > backoff.Maximum/2 {
			return backoff.Maximum
		}
		result *= 2
	}
	if result > backoff.Maximum {
		return backoff.Maximum
	}
	return result
}

func validateBoundedDelay(observedAt, availableAt time.Time) error {
	delay := availableAt.Sub(observedAt)
	if delay < MinRetryDelay || delay > MaxRetryDelay {
		return invalid("delivery deferral delay")
	}
	return nil
}

func transitionConflict(reason string) error {
	return fmt.Errorf("%w: %s", ErrConflict, reason)
}

func requiredTime(value time.Time, field string) (time.Time, error) {
	if value.IsZero() {
		return time.Time{}, invalid(field)
	}
	value = value.Round(0).UTC()
	if value.Year() < 1 || value.Year() > 9999 {
		return time.Time{}, invalid(field)
	}
	return value, nil
}

func optionalTime(value time.Time, field string) (time.Time, error) {
	if value.IsZero() {
		return time.Time{}, nil
	}
	return requiredTime(value, field)
}
