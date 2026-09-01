package jobs

import (
	"fmt"
	"time"
)

type DeliveryDeferrals struct{ value uint16 }

func NewDeliveryDeferrals(value uint16) (DeliveryDeferrals, error) {
	if value > MaximumDeliveryDeferrals {
		return DeliveryDeferrals{}, tooLarge("delivery deferrals")
	}
	return DeliveryDeferrals{value: value}, nil
}

func (d DeliveryDeferrals) Value() uint16 { return d.value }
func (d DeliveryDeferrals) IsZero() bool  { return d.value == 0 }
func (d DeliveryDeferrals) valid() bool   { return d.value <= MaximumDeliveryDeferrals }

type RetryLimit struct{ value uint16 }

func NewRetryLimit(value uint16) (RetryLimit, error) {
	if value > MaximumRetries {
		return RetryLimit{}, tooLarge("retry limit")
	}
	return RetryLimit{value: value}, nil
}

func (l RetryLimit) Value() uint16 { return l.value }
func (l RetryLimit) IsZero() bool  { return l.value == 0 }
func (l RetryLimit) valid() bool   { return l.value <= MaximumRetries }

type HandlerDeferralLimit struct{ value uint16 }

func NewHandlerDeferralLimit(value uint16) (HandlerDeferralLimit, error) {
	if value > MaximumHandlerDeferrals {
		return HandlerDeferralLimit{}, tooLarge("handler deferral limit")
	}
	return HandlerDeferralLimit{value: value}, nil
}

func (l HandlerDeferralLimit) Value() uint16 { return l.value }
func (l HandlerDeferralLimit) IsZero() bool  { return l.value == 0 }
func (l HandlerDeferralLimit) valid() bool   { return l.value <= MaximumHandlerDeferrals }

type DeliveryDeferralLimit struct{ value uint16 }

func NewDeliveryDeferralLimit(value uint16) (DeliveryDeferralLimit, error) {
	if value > MaximumDeliveryDeferrals {
		return DeliveryDeferralLimit{}, tooLarge("delivery deferral limit")
	}
	return DeliveryDeferralLimit{value: value}, nil
}

func (l DeliveryDeferralLimit) Value() uint16 { return l.value }
func (l DeliveryDeferralLimit) IsZero() bool  { return l.value == 0 }
func (l DeliveryDeferralLimit) valid() bool   { return l.value <= MaximumDeliveryDeferrals }

type InvocationOutcomeKind uint8

const (
	InvocationOutcomeInitial InvocationOutcomeKind = iota + 1
	InvocationOutcomeAttemptActive
	InvocationOutcomeAttemptFinished
	InvocationOutcomeDeliveryDeferred
	InvocationOutcomeCancelRequested
	InvocationOutcomeDeliveryTerminal
)

func (k InvocationOutcomeKind) Valid() bool {
	return k >= InvocationOutcomeInitial && k <= InvocationOutcomeDeliveryTerminal
}

func (k InvocationOutcomeKind) String() string {
	switch k {
	case InvocationOutcomeInitial:
		return "initial"
	case InvocationOutcomeAttemptActive:
		return "attempt_active"
	case InvocationOutcomeAttemptFinished:
		return "attempt_finished"
	case InvocationOutcomeDeliveryDeferred:
		return "delivery_deferred"
	case InvocationOutcomeCancelRequested:
		return "cancel_requested"
	case InvocationOutcomeDeliveryTerminal:
		return "delivery_terminal"
	default:
		return "unknown"
	}
}

type InvocationOutcome struct {
	kind           InvocationOutcomeKind
	attempt        AttemptOrdinal
	disposition    Disposition
	reason         Reason
	failure        PublicFailure
	terminalReason Reason
	terminalState  InvocationState
	occurredAt     time.Time
	availableAt    time.Time
}

func InitialInvocationOutcome() InvocationOutcome {
	return InvocationOutcome{kind: InvocationOutcomeInitial}
}

func ActiveAttemptOutcome(ordinal AttemptOrdinal, startedAt time.Time) (InvocationOutcome, error) {
	startedAt, err := requiredTime(startedAt, "active attempt start")
	if err != nil {
		return InvocationOutcome{}, err
	}
	outcome := InvocationOutcome{kind: InvocationOutcomeAttemptActive, attempt: ordinal, occurredAt: startedAt}
	if !outcome.valid() {
		return InvocationOutcome{}, invalid("active attempt outcome")
	}
	return outcome, nil
}

func FinishedAttemptOutcome(ordinal AttemptOrdinal, disposition Disposition, terminalReason Reason, finishedAt, availableAt time.Time) (InvocationOutcome, error) {
	finishedAt, err := requiredTime(finishedAt, "finished attempt time")
	if err != nil {
		return InvocationOutcome{}, err
	}
	availableAt, err = optionalTime(availableAt, "finished attempt availability")
	if err != nil {
		return InvocationOutcome{}, err
	}
	outcome := InvocationOutcome{kind: InvocationOutcomeAttemptFinished, attempt: ordinal, disposition: disposition, terminalReason: terminalReason, occurredAt: finishedAt, availableAt: availableAt}
	if !outcome.valid() {
		return InvocationOutcome{}, invalid("finished attempt outcome")
	}
	return outcome, nil
}

func DeferredDeliveryOutcome(reason Reason, failure PublicFailure, observedAt, availableAt time.Time) (InvocationOutcome, error) {
	observedAt, err := requiredTime(observedAt, "deferred delivery time")
	if err != nil {
		return InvocationOutcome{}, err
	}
	availableAt, err = requiredTime(availableAt, "deferred delivery availability")
	if err != nil {
		return InvocationOutcome{}, err
	}
	outcome := InvocationOutcome{kind: InvocationOutcomeDeliveryDeferred, reason: reason, failure: failure, occurredAt: observedAt, availableAt: availableAt}
	if !outcome.valid() {
		return InvocationOutcome{}, invalid("deferred delivery outcome")
	}
	return outcome, nil
}

func CancelRequestedOutcome(ordinal AttemptOrdinal, requestedAt time.Time) (InvocationOutcome, error) {
	requestedAt, err := requiredTime(requestedAt, "cancellation request time")
	if err != nil {
		return InvocationOutcome{}, err
	}
	outcome := InvocationOutcome{kind: InvocationOutcomeCancelRequested, attempt: ordinal, reason: ReasonCancelRequested, occurredAt: requestedAt}
	if !outcome.valid() {
		return InvocationOutcome{}, invalid("cancellation request outcome")
	}
	return outcome, nil
}

func TerminalDeliveryOutcome(state InvocationState, reason, terminalReason Reason, failure PublicFailure, observedAt, availableAt time.Time) (InvocationOutcome, error) {
	observedAt, err := requiredTime(observedAt, "terminal delivery time")
	if err != nil {
		return InvocationOutcome{}, err
	}
	availableAt, err = optionalTime(availableAt, "terminal delivery availability")
	if err != nil {
		return InvocationOutcome{}, err
	}
	outcome := InvocationOutcome{kind: InvocationOutcomeDeliveryTerminal, reason: reason, failure: failure, terminalReason: terminalReason, terminalState: state, occurredAt: observedAt, availableAt: availableAt}
	if !outcome.valid() {
		return InvocationOutcome{}, invalid("terminal delivery outcome")
	}
	return outcome, nil
}

func (o InvocationOutcome) Kind() InvocationOutcomeKind { return o.kind }
func (o InvocationOutcome) AttemptOrdinal() AttemptOrdinal {
	return o.attempt
}
func (o InvocationOutcome) Disposition() Disposition { return o.disposition }
func (o InvocationOutcome) Reason() Reason {
	if o.kind == InvocationOutcomeAttemptFinished {
		return o.disposition.Reason()
	}
	return o.reason
}
func (o InvocationOutcome) TerminalReason() Reason         { return o.terminalReason }
func (o InvocationOutcome) TerminalState() InvocationState { return o.terminalState }
func (o InvocationOutcome) Failure() PublicFailure {
	if o.kind == InvocationOutcomeAttemptFinished {
		return o.disposition.Failure()
	}
	return o.failure
}
func (o InvocationOutcome) OccurredAt() time.Time  { return o.occurredAt }
func (o InvocationOutcome) AvailableAt() time.Time { return o.availableAt }
func (o InvocationOutcome) IsZero() bool           { return o.kind == 0 }
func (o InvocationOutcome) String() string         { return "[job invocation outcome]" }
func (o InvocationOutcome) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, o.String())
}

func (o InvocationOutcome) valid() bool {
	if !o.kind.Valid() {
		return false
	}
	switch o.kind {
	case InvocationOutcomeInitial:
		return o.attempt.IsZero() && o.disposition.IsZero() && o.reason == ReasonNone && o.failure.IsZero() && o.terminalReason == ReasonNone && o.terminalState == 0 && o.occurredAt.IsZero() && o.availableAt.IsZero()
	case InvocationOutcomeAttemptActive:
		return !o.attempt.IsZero() && o.attempt.valid() && o.disposition.IsZero() && o.reason == ReasonNone && o.failure.IsZero() && o.terminalReason == ReasonNone && o.terminalState == 0 && validOutcomeTime(o.occurredAt) && o.availableAt.IsZero()
	case InvocationOutcomeAttemptFinished:
		return !o.attempt.IsZero() && o.attempt.valid() && o.disposition.valid() && o.disposition.allowedForAttempt() && o.reason == ReasonNone && o.failure.IsZero() && o.terminalState == 0 && attemptTerminalReason(o.disposition, o.terminalReason, o.availableAt) && validOutcomeTime(o.occurredAt) && validAttemptAvailability(o.disposition, o.availableAt)
	case InvocationOutcomeDeliveryDeferred:
		return o.attempt.IsZero() && o.disposition.IsZero() && deliveryDeferralReason(o.reason) && validOptionalFailure(o.failure) && o.terminalReason == ReasonNone && o.terminalState == 0 && validOutcomeTime(o.occurredAt) && validDeferralWindow(o.occurredAt, o.availableAt)
	case InvocationOutcomeCancelRequested:
		return !o.attempt.IsZero() && o.attempt.valid() && o.disposition.IsZero() && o.reason == ReasonCancelRequested && o.failure.IsZero() && o.terminalReason == ReasonNone && o.terminalState == 0 && validOutcomeTime(o.occurredAt) && o.availableAt.IsZero()
	case InvocationOutcomeDeliveryTerminal:
		return o.attempt.IsZero() && o.disposition.IsZero() && o.terminalState.Terminal() && deliveryTerminalState(o.terminalState, o.terminalReason) && deliveryTerminalPair(o.reason, o.terminalReason) && validOptionalFailure(o.failure) && validOutcomeTime(o.occurredAt) && validDeliveryTerminalAvailability(o) && (o.terminalReason != ReasonCancelRequested && o.terminalReason != ReasonOperatorTerminated || o.failure.IsZero())
	default:
		return false
	}
}

func validOutcomeTime(value time.Time) bool {
	canonical, err := requiredTime(value, "outcome time")
	return err == nil && canonical == value
}

func validAttemptAvailability(disposition Disposition, availableAt time.Time) bool {
	if disposition.kind != DispositionRetry && disposition.kind != DispositionDeferred {
		return availableAt.IsZero()
	}
	return availableAt.IsZero() || validDeferralWindow(time.Time{}, availableAt)
}

func attemptTerminalReason(disposition Disposition, reason Reason, availableAt time.Time) bool {
	if !availableAt.IsZero() {
		return reason == ReasonNone || reason == ReasonMaxElapsed || reason == ReasonStartBefore
	}
	switch disposition.kind {
	case DispositionRetry:
		return reason == ReasonMaxElapsed || reason == ReasonStartBefore || reason == ReasonRetryExhausted || reason == ReasonAttemptsExhausted
	case DispositionDeferred:
		return reason == ReasonMaxElapsed || reason == ReasonStartBefore || reason == ReasonDeferralsExhausted || reason == ReasonAttemptsExhausted
	case DispositionSucceeded, DispositionPermanentFailure, DispositionDiscard, DispositionQuarantine:
		return reason == ReasonNone || reason == ReasonMaxElapsed
	case DispositionCancelled, DispositionTerminated:
		return reason == ReasonNone
	default:
		return false
	}
}

func validDeferralWindow(observedAt, availableAt time.Time) bool {
	if !validOutcomeTime(availableAt) {
		return false
	}
	if observedAt.IsZero() {
		return true
	}
	delay := availableAt.Sub(observedAt)
	return delay >= MinRetryDelay && delay <= MaxRetryDelay
}

func validOptionalFailure(failure PublicFailure) bool {
	return failure.IsZero() || failure.valid()
}

func deliveryDeferralReason(reason Reason) bool {
	switch reason {
	case ReasonAdmission, ReasonCompatibility, ReasonDependency, ReasonShutdown, ReasonLeaseLost:
		return true
	default:
		return false
	}
}

func deliveryTerminalPair(reason, terminalReason Reason) bool {
	switch terminalReason {
	case ReasonMaxElapsed:
		return reason == ReasonMaxElapsed || reason == ReasonStartBefore || reason == ReasonPayload || reason == ReasonCompatibility || deliveryDeferralReason(reason)
	case ReasonStartBefore:
		return reason == ReasonStartBefore || reason == ReasonPayload || reason == ReasonCompatibility || deliveryDeferralReason(reason)
	case ReasonPayload:
		return reason == ReasonPayload
	case ReasonCompatibility:
		return reason == ReasonCompatibility
	case ReasonCancelRequested:
		return reason == ReasonCancelRequested
	case ReasonOperatorTerminated:
		return reason == ReasonOperatorTerminated
	case ReasonDeferralsExhausted:
		return deliveryDeferralReason(reason)
	case ReasonAttemptsExhausted:
		return deliveryDeferralReason(reason)
	default:
		return false
	}
}

func deliveryTerminalState(state InvocationState, reason Reason) bool {
	switch reason {
	case ReasonMaxElapsed, ReasonStartBefore, ReasonDeferralsExhausted, ReasonAttemptsExhausted:
		return state == InvocationDead
	case ReasonPayload:
		return state == InvocationDiscarded || state == InvocationQuarantined
	case ReasonCompatibility:
		return state == InvocationDiscarded || state == InvocationQuarantined
	case ReasonCancelRequested:
		return state == InvocationCancelled
	case ReasonOperatorTerminated:
		return state == InvocationTerminated
	default:
		return false
	}
}

func validDeliveryTerminalAvailability(outcome InvocationOutcome) bool {
	if outcome.availableAt.IsZero() {
		return true
	}
	if outcome.terminalReason != ReasonMaxElapsed && outcome.terminalReason != ReasonStartBefore || !deliveryDeferralReason(outcome.reason) {
		return false
	}
	if !validOutcomeTime(outcome.availableAt) {
		return false
	}
	delay := outcome.availableAt.Sub(outcome.occurredAt)
	return delay >= 0 && delay <= MaxRetryDelay
}
