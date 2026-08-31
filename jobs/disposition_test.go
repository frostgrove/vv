package jobs

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPublicFailureIsBoundedValidatedAndExplicitlyReadable(t *testing.T) {
	code, err := ParseFailureCode("dependency.timeout")
	if err != nil {
		t.Fatal(err)
	}
	exact := strings.Repeat("界", 682) + "ab"
	failure, err := NewPublicFailure(code, exact)
	if err != nil || failure.Code() != code || failure.Message() != exact || failure.IsZero() || !failure.valid() {
		t.Fatalf("failure = (%v, %v)", failure, err)
	}
	if _, err := NewPublicFailure(code, exact+"c"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
	for _, message := range []string{" leading", "trailing ", "line\nbreak", "line\u2028break", string([]byte{0xff})} {
		if _, err := NewPublicFailure(code, message); !errors.Is(err, ErrInvalid) {
			t.Fatalf("message %q error = %v", message, err)
		}
	}
	if empty, err := NewPublicFailure(code, ""); err != nil || empty.Message() != "" || !empty.valid() {
		t.Fatalf("code-only failure = (%v, %v)", empty, err)
	}
	if _, err := NewPublicFailure(FailureCode{}, "message"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero code error = %v", err)
	}
	for _, format := range []string{"%s", "%q", "%v", "%+v", "%#v"} {
		formatted := fmt.Sprintf(format, failure)
		if strings.Contains(formatted, exact) || strings.Contains(formatted, code.Value()) || !strings.Contains(formatted, "job failure") {
			t.Fatalf("format %q = %q", format, formatted)
		}
	}
	if !(PublicFailure{}).IsZero() || (PublicFailure{}).valid() {
		t.Fatal("zero public failure is valid")
	}
}

func TestFailureCodeUsesTheRegistryAlphabetAndByteBound(t *testing.T) {
	exact := "a" + strings.Repeat("b", MaxFailureCodeBytes-1)
	code, err := ParseFailureCode(exact)
	if err != nil || code.Value() != exact || code.String() != exact || code.IsZero() || !code.valid() {
		t.Fatalf("exact failure code = (%q, %v)", code.Value(), err)
	}
	if _, err := ParseFailureCode(exact + "c"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
	for _, raw := range []string{"", "Upper", "bad code", "bad..code", "ошибка"} {
		if _, err := ParseFailureCode(raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ParseFailureCode(%q) error = %v", raw, err)
		}
	}
}

func TestReasonKindAndRetryCostNamesAreClosed(t *testing.T) {
	reasons := []struct {
		value Reason
		name  string
	}{
		{ReasonNone, "none"},
		{ReasonHandlerFailure, "handler_failure"},
		{ReasonPanic, "panic"},
		{ReasonAttemptTimeout, "attempt_timeout"},
		{ReasonProgressTimeout, "progress_timeout"},
		{ReasonMaxElapsed, "max_elapsed"},
		{ReasonStartBefore, "start_before"},
		{ReasonDependency, "dependency"},
		{ReasonAdmission, "admission"},
		{ReasonCompatibility, "compatibility"},
		{ReasonShutdown, "shutdown"},
		{ReasonLeaseLost, "lease_lost"},
		{ReasonCancelRequested, "cancel_requested"},
		{ReasonOperatorTerminated, "operator_terminated"},
		{ReasonPayload, "payload"},
		{ReasonClassifier, "classifier"},
		{ReasonRetryExhausted, "retry_exhausted"},
		{ReasonDeferralsExhausted, "deferrals_exhausted"},
		{ReasonAttemptsExhausted, "attempts_exhausted"},
	}
	for _, test := range reasons {
		if !test.value.Valid() || test.value.String() != test.name {
			t.Fatalf("reason %d = %q", test.value, test.value)
		}
	}
	if Reason(255).Valid() || Reason(255).String() != "unknown" {
		t.Fatal("unknown reason is valid")
	}
	kinds := []struct {
		value DispositionKind
		name  string
	}{
		{DispositionSucceeded, "succeeded"},
		{DispositionRetry, "retry"},
		{DispositionPermanentFailure, "permanent_failure"},
		{DispositionDiscard, "discard"},
		{DispositionQuarantine, "quarantine"},
		{DispositionDeferred, "deferred"},
		{DispositionCancelled, "cancelled"},
		{DispositionTerminated, "terminated"},
	}
	for _, test := range kinds {
		if !test.value.Valid() || test.value.String() != test.name {
			t.Fatalf("kind %d = %q", test.value, test.value)
		}
	}
	if DispositionKind(0).Valid() || DispositionKind(255).Valid() || DispositionKind(255).String() != "unknown" {
		t.Fatal("unknown disposition kind is valid")
	}
	if !RetryCostNone.Valid() || RetryCostNone.String() != "none" || !RetryCostCharged.Valid() || RetryCostCharged.String() != "charged" || RetryCost(2).Valid() || RetryCost(2).String() != "unknown" {
		t.Fatal("retry cost set is not closed")
	}
}

func TestDispositionFactoriesPreserveExactAccounting(t *testing.T) {
	failure := testPublicFailure(t)
	tests := []struct {
		name   string
		build  func() (Disposition, error)
		kind   DispositionKind
		reason Reason
		after  time.Duration
		cost   RetryCost
	}{
		{"success", func() (Disposition, error) { return SuccessDisposition(), nil }, DispositionSucceeded, ReasonNone, 0, RetryCostNone},
		{"handler retry", func() (Disposition, error) {
			return RetryDisposition(ReasonHandlerFailure, failure, 0, RetryCostCharged)
		}, DispositionRetry, ReasonHandlerFailure, 0, RetryCostCharged},
		{"lease retry", func() (Disposition, error) {
			return RetryDisposition(ReasonLeaseLost, PublicFailure{}, MinRetryDelay, RetryCostNone)
		}, DispositionRetry, ReasonLeaseLost, MinRetryDelay, RetryCostNone},
		{"permanent", func() (Disposition, error) { return PermanentFailureDisposition(ReasonHandlerFailure, failure) }, DispositionPermanentFailure, ReasonHandlerFailure, 0, RetryCostNone},
		{"discard", func() (Disposition, error) { return DiscardDisposition(ReasonClassifier, failure) }, DispositionDiscard, ReasonClassifier, 0, RetryCostNone},
		{"quarantine", func() (Disposition, error) { return QuarantineDisposition(ReasonCompatibility, failure) }, DispositionQuarantine, ReasonCompatibility, 0, RetryCostNone},
		{"dependency", func() (Disposition, error) { return DeferredDisposition(failure, MinRetryDelay) }, DispositionDeferred, ReasonDependency, MinRetryDelay, RetryCostNone},
		{"cancelled", func() (Disposition, error) { return CancelledDisposition(ReasonCancelRequested) }, DispositionCancelled, ReasonCancelRequested, 0, RetryCostNone},
		{"terminated", func() (Disposition, error) { return TerminatedDisposition(), nil }, DispositionTerminated, ReasonOperatorTerminated, 0, RetryCostNone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			disposition, err := test.build()
			if err != nil || !disposition.valid() || disposition.IsZero() {
				t.Fatalf("build = (%v, %v)", disposition, err)
			}
			if disposition.Kind() != test.kind || disposition.Reason() != test.reason || disposition.RetryAfter() != test.after || disposition.RetryCost() != test.cost {
				t.Fatalf("fields = (%v, %v, %s, %v)", disposition.Kind(), disposition.Reason(), disposition.RetryAfter(), disposition.RetryCost())
			}
			if disposition.Failure() != (PublicFailure{}) && disposition.Failure() != failure {
				t.Fatalf("failure = %v", disposition.Failure())
			}
		})
	}
}

func TestDispositionFormattingDoesNotExposeItsFailure(t *testing.T) {
	code, err := ParseFailureCode("private.failure")
	if err != nil {
		t.Fatal(err)
	}
	failure, err := NewPublicFailure(code, "private operator failure detail")
	if err != nil {
		t.Fatal(err)
	}
	disposition, err := PermanentFailureDisposition(ReasonHandlerFailure, failure)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		formatted := fmt.Sprintf(format, disposition)
		if strings.Contains(formatted, code.Value()) || strings.Contains(formatted, failure.Message()) || !strings.Contains(formatted, "job disposition") {
			t.Fatalf("format %q = %q", format, formatted)
		}
	}
}

func TestDispositionRejectsCounterBypassesAndHotLoops(t *testing.T) {
	failure := testPublicFailure(t)
	tests := []DispositionSpec{
		{},
		{Kind: DispositionKind(255)},
		{Kind: DispositionSucceeded, Reason: ReasonHandlerFailure},
		{Kind: DispositionSucceeded, Failure: failure},
		{Kind: DispositionSucceeded, RetryCost: RetryCostCharged},
		{Kind: DispositionSucceeded, RetryAfter: MinRetryDelay},
		{Kind: DispositionRetry, Reason: ReasonNone, RetryCost: RetryCostCharged},
		{Kind: DispositionRetry, Reason: ReasonDependency, RetryCost: RetryCostNone},
		{Kind: DispositionRetry, Reason: ReasonCancelRequested, RetryCost: RetryCostNone},
		{Kind: DispositionRetry, Reason: ReasonHandlerFailure, RetryCost: RetryCostNone},
		{Kind: DispositionRetry, Reason: ReasonPanic, RetryCost: RetryCostNone},
		{Kind: DispositionRetry, Reason: ReasonAttemptTimeout, RetryCost: RetryCostNone},
		{Kind: DispositionRetry, Reason: ReasonProgressTimeout, RetryCost: RetryCostNone},
		{Kind: DispositionRetry, Reason: ReasonClassifier, RetryCost: RetryCostNone},
		{Kind: DispositionRetry, Reason: ReasonShutdown, RetryCost: RetryCostCharged},
		{Kind: DispositionRetry, Reason: ReasonLeaseLost, RetryCost: RetryCostCharged},
		{Kind: DispositionRetry, Reason: ReasonHandlerFailure, RetryCost: RetryCostCharged, RetryAfter: -time.Second},
		{Kind: DispositionRetry, Reason: ReasonHandlerFailure, RetryCost: RetryCostCharged, RetryAfter: MinRetryDelay - 1},
		{Kind: DispositionRetry, Reason: ReasonHandlerFailure, RetryCost: RetryCostCharged, RetryAfter: MaxRetryDelay + 1},
		{Kind: DispositionPermanentFailure, Reason: ReasonNone},
		{Kind: DispositionPermanentFailure, Reason: ReasonOperatorTerminated},
		{Kind: DispositionDiscard, Reason: ReasonCancelRequested},
		{Kind: DispositionQuarantine, Reason: ReasonPayload, RetryCost: RetryCostCharged},
		{Kind: DispositionDeferred, Reason: ReasonDependency, RetryAfter: 0},
		{Kind: DispositionDeferred, Reason: ReasonDependency, RetryAfter: MinRetryDelay, RetryCost: RetryCostCharged},
		{Kind: DispositionDeferred, Reason: ReasonHandlerFailure, RetryAfter: MinRetryDelay},
		{Kind: DispositionCancelled, Reason: ReasonOperatorTerminated},
		{Kind: DispositionCancelled, Reason: ReasonCancelRequested, Failure: failure},
		{Kind: DispositionCancelled, Reason: ReasonCancelRequested, RetryAfter: MinRetryDelay},
		{Kind: DispositionCancelled, Reason: ReasonCancelRequested, RetryCost: RetryCostCharged},
		{Kind: DispositionTerminated, Reason: ReasonNone},
		{Kind: DispositionTerminated, Reason: ReasonCancelRequested},
		{Kind: DispositionTerminated, Reason: ReasonOperatorTerminated, Failure: failure},
		{Kind: DispositionTerminated, Reason: ReasonOperatorTerminated, RetryAfter: MinRetryDelay},
		{Kind: DispositionTerminated, Reason: ReasonOperatorTerminated, RetryCost: RetryCostCharged},
		{Kind: DispositionPermanentFailure, Reason: ReasonHandlerFailure, Failure: PublicFailure{message: "unbounded"}},
		{Reason: ReasonHandlerFailure},
	}
	for index, spec := range tests {
		if disposition, err := NewDisposition(spec); !errors.Is(err, ErrInvalid) || !disposition.IsZero() {
			t.Fatalf("case %d = (%v, %v)", index, disposition, err)
		}
	}
}

func TestPreHandlerReasonsCannotBecomeAttempts(t *testing.T) {
	failure := testPublicFailure(t)
	for _, reason := range []Reason{ReasonStartBefore, ReasonAdmission, ReasonCompatibility, ReasonPayload, ReasonRetryExhausted, ReasonDeferralsExhausted, ReasonAttemptsExhausted} {
		disposition, err := PermanentFailureDisposition(reason, failure)
		if err != nil {
			t.Fatalf("reason %v: %v", reason, err)
		}
		if disposition.allowedForAttempt() {
			t.Fatalf("reason %v was accepted for an attempt", reason)
		}
	}
	if disposition, err := PermanentFailureDisposition(ReasonMaxElapsed, failure); !errors.Is(err, ErrInvalid) || !disposition.IsZero() {
		t.Fatalf("maximum elapsed disposition = (%v, %v)", disposition, err)
	}
	for _, reason := range []Reason{ReasonHandlerFailure, ReasonPanic, ReasonAttemptTimeout, ReasonProgressTimeout, ReasonClassifier, ReasonCancelRequested} {
		var disposition Disposition
		var err error
		if reason == ReasonCancelRequested {
			disposition, err = CancelledDisposition(reason)
		} else {
			disposition, err = PermanentFailureDisposition(reason, failure)
		}
		if err != nil || !disposition.allowedForAttempt() {
			t.Fatalf("reason %v = (%v, %v)", reason, disposition, err)
		}
	}
	terminated := TerminatedDisposition()
	if !terminated.valid() || !terminated.allowedForAttempt() {
		t.Fatalf("system termination = %v", terminated)
	}
}

func testPublicFailure(t *testing.T) PublicFailure {
	t.Helper()
	code, err := ParseFailureCode("job.failed")
	if err != nil {
		t.Fatal(err)
	}
	failure, err := NewPublicFailure(code, "safe operator message")
	if err != nil {
		t.Fatal(err)
	}
	return failure
}
