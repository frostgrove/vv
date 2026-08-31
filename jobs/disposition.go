package jobs

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type FailureCode struct{ value string }

func ParseFailureCode(raw string) (FailureCode, error) {
	value, err := parseRegistryName(raw, MaxFailureCodeBytes, "failure code")
	if err != nil {
		return FailureCode{}, err
	}
	return FailureCode{value: value}, nil
}

func (c FailureCode) Value() string  { return c.value }
func (c FailureCode) String() string { return c.value }
func (c FailureCode) IsZero() bool   { return c.value == "" }
func (c FailureCode) valid() bool    { return validRegistryName(c.value, MaxFailureCodeBytes) }

type PublicFailure struct {
	code    FailureCode
	message string
}

func NewPublicFailure(code FailureCode, message string) (PublicFailure, error) {
	if !code.valid() {
		return PublicFailure{}, invalid("public failure code")
	}
	if len(message) > MaxPublicFailureBytes {
		return PublicFailure{}, tooLarge("public failure message")
	}
	if !validPublicFailureText(message) {
		return PublicFailure{}, invalid("public failure message")
	}
	return PublicFailure{code: code, message: strings.Clone(message)}, nil
}

func (f PublicFailure) Code() FailureCode { return f.code }
func (f PublicFailure) Message() string   { return f.message }
func (f PublicFailure) IsZero() bool      { return f.code.IsZero() && f.message == "" }
func (f PublicFailure) String() string    { return "[job failure]" }
func (f PublicFailure) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, f.String())
}
func (f PublicFailure) valid() bool {
	return f.code.valid() && len(f.message) <= MaxPublicFailureBytes && validPublicFailureText(f.message)
}

func validPublicFailureText(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) || current == '\u2028' || current == '\u2029' {
			return false
		}
	}
	return true
}

type Reason uint8

const (
	ReasonNone Reason = iota
	ReasonHandlerFailure
	ReasonPanic
	ReasonAttemptTimeout
	ReasonProgressTimeout
	ReasonMaxElapsed
	ReasonStartBefore
	ReasonDependency
	ReasonAdmission
	ReasonCompatibility
	ReasonShutdown
	ReasonLeaseLost
	ReasonCancelRequested
	ReasonOperatorTerminated
	ReasonPayload
	ReasonClassifier
	ReasonRetryExhausted
	ReasonDeferralsExhausted
	ReasonAttemptsExhausted
)

func (r Reason) Valid() bool { return r >= ReasonNone && r <= ReasonAttemptsExhausted }
func (r Reason) String() string {
	switch r {
	case ReasonNone:
		return "none"
	case ReasonHandlerFailure:
		return "handler_failure"
	case ReasonPanic:
		return "panic"
	case ReasonAttemptTimeout:
		return "attempt_timeout"
	case ReasonProgressTimeout:
		return "progress_timeout"
	case ReasonMaxElapsed:
		return "max_elapsed"
	case ReasonStartBefore:
		return "start_before"
	case ReasonDependency:
		return "dependency"
	case ReasonAdmission:
		return "admission"
	case ReasonCompatibility:
		return "compatibility"
	case ReasonShutdown:
		return "shutdown"
	case ReasonLeaseLost:
		return "lease_lost"
	case ReasonCancelRequested:
		return "cancel_requested"
	case ReasonOperatorTerminated:
		return "operator_terminated"
	case ReasonPayload:
		return "payload"
	case ReasonClassifier:
		return "classifier"
	case ReasonRetryExhausted:
		return "retry_exhausted"
	case ReasonDeferralsExhausted:
		return "deferrals_exhausted"
	case ReasonAttemptsExhausted:
		return "attempts_exhausted"
	default:
		return "unknown"
	}
}

type DispositionKind uint8

const (
	DispositionSucceeded DispositionKind = iota + 1
	DispositionRetry
	DispositionPermanentFailure
	DispositionDiscard
	DispositionQuarantine
	DispositionDeferred
	DispositionCancelled
	DispositionTerminated
)

func (k DispositionKind) Valid() bool {
	return k >= DispositionSucceeded && k <= DispositionTerminated
}

func (k DispositionKind) String() string {
	switch k {
	case DispositionSucceeded:
		return "succeeded"
	case DispositionRetry:
		return "retry"
	case DispositionPermanentFailure:
		return "permanent_failure"
	case DispositionDiscard:
		return "discard"
	case DispositionQuarantine:
		return "quarantine"
	case DispositionDeferred:
		return "deferred"
	case DispositionCancelled:
		return "cancelled"
	case DispositionTerminated:
		return "terminated"
	default:
		return "unknown"
	}
}

type RetryCost uint8

const (
	RetryCostNone RetryCost = iota
	RetryCostCharged
)

func (c RetryCost) Valid() bool { return c <= RetryCostCharged }
func (c RetryCost) String() string {
	switch c {
	case RetryCostNone:
		return "none"
	case RetryCostCharged:
		return "charged"
	default:
		return "unknown"
	}
}

type DispositionSpec struct {
	Kind       DispositionKind
	Reason     Reason
	RetryAfter time.Duration
	RetryCost  RetryCost
	Failure    PublicFailure
}

type Disposition struct {
	kind       DispositionKind
	reason     Reason
	retryAfter time.Duration
	retryCost  RetryCost
	failure    PublicFailure
}

func NewDisposition(spec DispositionSpec) (Disposition, error) {
	if !spec.Kind.Valid() || !spec.Reason.Valid() || !spec.RetryCost.Valid() {
		return Disposition{}, invalid("disposition kind, reason, or retry cost")
	}
	if spec.RetryAfter < 0 || spec.RetryAfter > MaxRetryDelay || spec.RetryAfter > 0 && spec.RetryAfter < MinRetryDelay {
		return Disposition{}, invalid("disposition retry delay")
	}
	if !spec.Failure.IsZero() && !spec.Failure.valid() {
		return Disposition{}, invalid("disposition public failure")
	}
	if err := validateDispositionCombination(spec); err != nil {
		return Disposition{}, err
	}
	return Disposition{
		kind:       spec.Kind,
		reason:     spec.Reason,
		retryAfter: spec.RetryAfter,
		retryCost:  spec.RetryCost,
		failure:    spec.Failure,
	}, nil
}

func SuccessDisposition() Disposition {
	disposition, _ := NewDisposition(DispositionSpec{Kind: DispositionSucceeded})
	return disposition
}

func RetryDisposition(reason Reason, failure PublicFailure, after time.Duration, cost RetryCost) (Disposition, error) {
	return NewDisposition(DispositionSpec{Kind: DispositionRetry, Reason: reason, RetryAfter: after, RetryCost: cost, Failure: failure})
}

func PermanentFailureDisposition(reason Reason, failure PublicFailure) (Disposition, error) {
	return NewDisposition(DispositionSpec{Kind: DispositionPermanentFailure, Reason: reason, Failure: failure})
}

func DiscardDisposition(reason Reason, failure PublicFailure) (Disposition, error) {
	return NewDisposition(DispositionSpec{Kind: DispositionDiscard, Reason: reason, Failure: failure})
}

func QuarantineDisposition(reason Reason, failure PublicFailure) (Disposition, error) {
	return NewDisposition(DispositionSpec{Kind: DispositionQuarantine, Reason: reason, Failure: failure})
}

func DeferredDisposition(failure PublicFailure, after time.Duration) (Disposition, error) {
	return NewDisposition(DispositionSpec{Kind: DispositionDeferred, Reason: ReasonDependency, RetryAfter: after, Failure: failure})
}

func CancelledDisposition(reason Reason) (Disposition, error) {
	return NewDisposition(DispositionSpec{Kind: DispositionCancelled, Reason: reason})
}

func TerminatedDisposition() Disposition {
	disposition, _ := NewDisposition(DispositionSpec{Kind: DispositionTerminated, Reason: ReasonOperatorTerminated})
	return disposition
}

func (d Disposition) Kind() DispositionKind     { return d.kind }
func (d Disposition) Reason() Reason            { return d.reason }
func (d Disposition) RetryAfter() time.Duration { return d.retryAfter }
func (d Disposition) RetryCost() RetryCost      { return d.retryCost }
func (d Disposition) Failure() PublicFailure    { return d.failure }
func (d Disposition) IsZero() bool {
	return d.kind == 0 && d.reason == ReasonNone && d.retryAfter == 0 && d.retryCost == RetryCostNone && d.failure.IsZero()
}
func (d Disposition) String() string { return "[job disposition]" }
func (d Disposition) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, d.String())
}
func (d Disposition) valid() bool {
	if d.IsZero() {
		return false
	}
	_, err := NewDisposition(DispositionSpec{Kind: d.kind, Reason: d.reason, RetryAfter: d.retryAfter, RetryCost: d.retryCost, Failure: d.failure})
	return err == nil
}

func (d Disposition) allowedForAttempt() bool {
	switch d.reason {
	case ReasonStartBefore, ReasonAdmission, ReasonCompatibility, ReasonPayload, ReasonRetryExhausted, ReasonDeferralsExhausted, ReasonAttemptsExhausted:
		return false
	default:
		return true
	}
}

func validateDispositionCombination(spec DispositionSpec) error {
	switch spec.Kind {
	case DispositionSucceeded:
		if spec.Reason != ReasonNone || spec.RetryAfter != 0 || spec.RetryCost != RetryCostNone || !spec.Failure.IsZero() {
			return invalid("successful disposition carries failure state")
		}
	case DispositionRetry:
		if !retryReason(spec.Reason) {
			return invalid("retry disposition reason")
		}
		if chargedRetryReason(spec.Reason) != (spec.RetryCost == RetryCostCharged) {
			return invalid("retry disposition cost")
		}
	case DispositionPermanentFailure, DispositionDiscard, DispositionQuarantine:
		if spec.Reason == ReasonNone || spec.Reason == ReasonMaxElapsed || controlPlaneReason(spec.Reason) || spec.RetryAfter != 0 || spec.RetryCost != RetryCostNone {
			return invalid("terminal disposition state")
		}
	case DispositionDeferred:
		if spec.Reason != ReasonDependency || spec.RetryAfter == 0 || spec.RetryCost != RetryCostNone {
			return invalid("dependency deferral state")
		}
	case DispositionCancelled:
		if !cancellationReason(spec.Reason) || spec.RetryAfter != 0 || spec.RetryCost != RetryCostNone || !spec.Failure.IsZero() {
			return invalid("cancellation disposition state")
		}
	case DispositionTerminated:
		if spec.Reason != ReasonOperatorTerminated || spec.RetryAfter != 0 || spec.RetryCost != RetryCostNone || !spec.Failure.IsZero() {
			return invalid("termination disposition state")
		}
	}
	return nil
}

func retryReason(reason Reason) bool {
	switch reason {
	case ReasonHandlerFailure, ReasonPanic, ReasonAttemptTimeout, ReasonProgressTimeout, ReasonShutdown, ReasonLeaseLost, ReasonClassifier:
		return true
	default:
		return false
	}
}

func chargedRetryReason(reason Reason) bool {
	switch reason {
	case ReasonHandlerFailure, ReasonPanic, ReasonAttemptTimeout, ReasonProgressTimeout, ReasonClassifier:
		return true
	default:
		return false
	}
}

func cancellationReason(reason Reason) bool {
	return reason == ReasonCancelRequested
}

func controlPlaneReason(reason Reason) bool {
	return reason == ReasonCancelRequested || reason == ReasonOperatorTerminated
}
