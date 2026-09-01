package jobs

import (
	"fmt"
	"slices"
	"time"
)

type InvocationRestoreSpec struct {
	Genesis  InvocationSpec
	Outcomes []InvocationOutcome
	Attempts []AttemptRecord
}

func (s InvocationRestoreSpec) String() string { return "[job invocation restore]" }
func (s InvocationRestoreSpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}

func RestoreInvocation(spec InvocationRestoreSpec) (Invocation, error) {
	if len(spec.Outcomes) == 0 || len(spec.Outcomes) > MaxInvocationOutcomes || len(spec.Attempts) > MaxAttemptOrdinal {
		return Invocation{}, corruptInvocationLedger()
	}
	current, err := NewInvocation(spec.Genesis)
	if err != nil || spec.Outcomes[0] != current.Outcome() {
		return Invocation{}, corruptInvocationLedger()
	}
	var pendingProgress *AttemptRecord
	for index := 1; index < len(spec.Outcomes); index++ {
		event := spec.Outcomes[index]
		if pendingProgress != nil && !pendingProgress.ProgressedAt.After(event.occurredAt) {
			current, err = restoreAttemptProgress(current, *pendingProgress)
			if err != nil {
				return Invocation{}, corruptInvocationLedger()
			}
			pendingProgress = nil
		}
		var next Invocation
		switch event.kind {
		case InvocationOutcomeAttemptActive:
			record, ok := restoreAttemptRecord(spec.Attempts, event.attempt)
			if !ok {
				return Invocation{}, corruptInvocationLedger()
			}
			var attempt Attempt
			next, attempt, err = current.BeginAttempt(BeginAttemptSpec{Binding: record.Binding, Build: record.Build, StartedAt: record.StartedAt})
			if err == nil {
				if !sameAttemptStart(attempt.Record(), record) || validateAttemptProgress(record, attempt, current.policy.ProgressTimeout()) != nil {
					err = ErrCorrupt
				} else {
					pending := record
					pendingProgress = &pending
				}
			}
		case InvocationOutcomeAttemptFinished:
			record, ok := restoreAttemptRecord(spec.Attempts, event.attempt)
			if !ok || current.attempts == nil {
				return Invocation{}, corruptInvocationLedger()
			}
			var finished Attempt
			if event.disposition.kind == DispositionTerminated {
				next, err = current.Terminate(event.occurredAt)
				if err == nil {
					finished = next.attempts.value
				}
			} else {
				next, finished, err = current.FinishAttempt(current.attempts.value, FinishAttemptSpec{FinishedAt: event.occurredAt, Disposition: event.disposition, AvailableAt: event.availableAt})
			}
			if err == nil && finished.Record() != record {
				err = ErrCorrupt
			}
		case InvocationOutcomeDeliveryDeferred:
			next, err = current.DeferDelivery(DeferDeliverySpec{Reason: event.reason, Failure: event.failure, ObservedAt: event.occurredAt, AvailableAt: event.availableAt})
		case InvocationOutcomeCancelRequested:
			next, err = current.RequestCancel(event.occurredAt)
		case InvocationOutcomeDeliveryTerminal:
			next, err = restoreTerminalDelivery(current, event)
		default:
			return Invocation{}, corruptInvocationLedger()
		}
		if err != nil || next.Outcome() != event {
			return Invocation{}, corruptInvocationLedger()
		}
		current = next
	}
	if pendingProgress != nil {
		current, err = restoreAttemptProgress(current, *pendingProgress)
		if err != nil {
			return Invocation{}, corruptInvocationLedger()
		}
	}
	if !slices.Equal(current.History(), spec.Outcomes) || !slices.Equal(current.AttemptRecords(), spec.Attempts) {
		return Invocation{}, corruptInvocationLedger()
	}
	return current, nil
}

func restoreTerminalDelivery(current Invocation, event InvocationOutcome) (Invocation, error) {
	switch event.terminalReason {
	case ReasonCancelRequested:
		return current.RequestCancel(event.occurredAt)
	case ReasonOperatorTerminated:
		return current.Terminate(event.occurredAt)
	case ReasonPayload, ReasonCompatibility:
		return current.FinishDelivery(FinishDeliverySpec{State: event.terminalState, Reason: event.reason, Failure: event.failure, ObservedAt: event.occurredAt})
	case ReasonMaxElapsed, ReasonStartBefore:
		switch {
		case (event.reason == ReasonCompatibility || event.reason == ReasonShutdown) && event.failure.IsZero():
			return current.releaseUnchanged(event.reason, event.occurredAt, event.availableAt)
		case deliveryDeferralReason(event.reason) && !event.availableAt.IsZero():
			return current.DeferDelivery(DeferDeliverySpec{Reason: event.reason, Failure: event.failure, ObservedAt: event.occurredAt, AvailableAt: event.availableAt})
		case event.reason == ReasonPayload || event.reason == ReasonCompatibility:
			state := InvocationDiscarded
			if event.reason == ReasonCompatibility {
				state = InvocationQuarantined
			}
			return current.FinishDelivery(FinishDeliverySpec{State: state, Reason: event.reason, Failure: event.failure, ObservedAt: event.occurredAt})
		case deliveryDeferralReason(event.reason):
			availableAt := event.availableAt
			if availableAt.IsZero() {
				availableAt = event.occurredAt.Add(MinRetryDelay)
			}
			return current.DeferDelivery(DeferDeliverySpec{Reason: event.reason, Failure: event.failure, ObservedAt: event.occurredAt, AvailableAt: availableAt})
		default:
			return current.Expire(event.occurredAt)
		}
	case ReasonDeferralsExhausted, ReasonAttemptsExhausted:
		return current.DeferDelivery(DeferDeliverySpec{Reason: event.reason, Failure: event.failure, ObservedAt: event.occurredAt, AvailableAt: event.occurredAt.Add(MinRetryDelay)})
	default:
		return Invocation{}, ErrCorrupt
	}
}

func restoreAttemptRecord(records []AttemptRecord, ordinal AttemptOrdinal) (AttemptRecord, bool) {
	if ordinal.IsZero() || int(ordinal.Value()) > len(records) {
		return AttemptRecord{}, false
	}
	return records[int(ordinal.Value())-1], true
}

func sameAttemptStart(left, right AttemptRecord) bool {
	return left.Invocation == right.Invocation && left.Ordinal == right.Ordinal && left.Binding == right.Binding && left.Build == right.Build && left.StartedAt == right.StartedAt && left.Deadline == right.Deadline
}

func validateAttemptProgress(record AttemptRecord, attempt Attempt, timeout time.Duration) error {
	if timeout == 0 {
		if !record.ProgressedAt.IsZero() || !record.ProgressDeadline.IsZero() {
			return ErrCorrupt
		}
		return nil
	}
	progressedAt, err := requiredTime(record.ProgressedAt, "attempt progress time")
	if err != nil || progressedAt != record.ProgressedAt || progressedAt.Before(attempt.startedAt) || !progressedAt.Before(attempt.deadline) {
		return ErrCorrupt
	}
	progressDeadline, err := requiredTime(record.ProgressDeadline, "attempt progress deadline")
	if err != nil || progressDeadline != record.ProgressDeadline {
		return ErrCorrupt
	}
	want, err := requiredTime(progressedAt.Add(timeout), "attempt progress deadline")
	if err != nil {
		return ErrCorrupt
	}
	if want.After(attempt.deadline) {
		want = attempt.deadline
	}
	if progressDeadline != want {
		return ErrCorrupt
	}
	return nil
}

func restoreAttemptProgress(invocation Invocation, record AttemptRecord) (Invocation, error) {
	if invocation.attempts == nil || !sameAttemptToken(invocation.attempts.value, attemptFromRecord(record)) {
		return Invocation{}, ErrCorrupt
	}
	if err := validateAttemptProgress(record, invocation.attempts.value, invocation.policy.ProgressTimeout()); err != nil {
		return Invocation{}, err
	}
	progressed := invocation.attempts.value
	progressed.progressedAt = record.ProgressedAt
	progressed.progressDeadline = record.ProgressDeadline
	invocation.attempts = &attemptLedger{previous: invocation.attempts.previous, value: progressed, length: invocation.attempts.length}
	return invocation, nil
}

func corruptInvocationLedger() error {
	return fmt.Errorf("%w: invocation ledger", ErrCorrupt)
}
