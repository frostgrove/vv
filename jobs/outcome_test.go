package jobs

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInvocationLimitsAndDeliveryDeferralsAreDistinctAndBounded(t *testing.T) {
	types := []any{DeliveryDeferrals{}, RetryLimit{}, HandlerDeferralLimit{}, DeliveryDeferralLimit{}}
	for left := range types {
		for right := left + 1; right < len(types); right++ {
			if reflect.TypeOf(types[left]) == reflect.TypeOf(types[right]) {
				t.Fatalf("types %d and %d are equal", left, right)
			}
		}
	}

	delivery, err := NewDeliveryDeferrals(MaximumDeliveryDeferrals)
	if err != nil || delivery.Value() != MaximumDeliveryDeferrals || delivery.IsZero() || !delivery.valid() {
		t.Fatalf("delivery deferrals = (%v, %v)", delivery, err)
	}
	if _, err := NewDeliveryDeferrals(MaximumDeliveryDeferrals + 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("delivery deferrals bound = %v", err)
	}
	retries, err := NewRetryLimit(MaximumRetries)
	if err != nil || retries.Value() != MaximumRetries || retries.IsZero() || !retries.valid() {
		t.Fatalf("retry limit = (%v, %v)", retries, err)
	}
	if _, err := NewRetryLimit(MaximumRetries + 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("retry limit bound = %v", err)
	}
	handler, err := NewHandlerDeferralLimit(MaximumHandlerDeferrals)
	if err != nil || handler.Value() != MaximumHandlerDeferrals || handler.IsZero() || !handler.valid() {
		t.Fatalf("handler limit = (%v, %v)", handler, err)
	}
	if _, err := NewHandlerDeferralLimit(MaximumHandlerDeferrals + 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("handler limit bound = %v", err)
	}
	deliveryLimit, err := NewDeliveryDeferralLimit(MaximumDeliveryDeferrals)
	if err != nil || deliveryLimit.Value() != MaximumDeliveryDeferrals || deliveryLimit.IsZero() || !deliveryLimit.valid() {
		t.Fatalf("delivery limit = (%v, %v)", deliveryLimit, err)
	}
	if _, err := NewDeliveryDeferralLimit(MaximumDeliveryDeferrals + 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("delivery limit bound = %v", err)
	}
	if value, err := NewDeliveryDeferrals(0); err != nil || !value.IsZero() || !value.valid() {
		t.Fatalf("zero delivery deferrals = (%v, %v)", value, err)
	}
	if value, err := NewRetryLimit(0); err != nil || !value.IsZero() || !value.valid() {
		t.Fatalf("zero retry limit = (%v, %v)", value, err)
	}
	if value, err := NewHandlerDeferralLimit(0); err != nil || !value.IsZero() || !value.valid() {
		t.Fatalf("zero handler limit = (%v, %v)", value, err)
	}
	if value, err := NewDeliveryDeferralLimit(0); err != nil || !value.IsZero() || !value.valid() {
		t.Fatalf("zero delivery limit = (%v, %v)", value, err)
	}
}

func TestInvocationOutcomeKindsAreClosed(t *testing.T) {
	tests := []struct {
		kind InvocationOutcomeKind
		name string
	}{
		{InvocationOutcomeInitial, "initial"},
		{InvocationOutcomeAttemptActive, "attempt_active"},
		{InvocationOutcomeAttemptFinished, "attempt_finished"},
		{InvocationOutcomeDeliveryDeferred, "delivery_deferred"},
		{InvocationOutcomeCancelRequested, "cancel_requested"},
		{InvocationOutcomeDeliveryTerminal, "delivery_terminal"},
	}
	for _, test := range tests {
		if !test.kind.Valid() || test.kind.String() != test.name {
			t.Fatalf("kind %d = %q", test.kind, test.kind)
		}
	}
	if InvocationOutcomeKind(0).Valid() || InvocationOutcomeKind(255).Valid() || InvocationOutcomeKind(255).String() != "unknown" {
		t.Fatal("unknown invocation outcome kind is valid")
	}
}

func TestInvocationOutcomeFactoriesPreserveTypedProvenance(t *testing.T) {
	ordinal, _ := NewAttemptOrdinal(1)
	failure := testPublicFailure(t)
	zone := time.FixedZone("test", 6*60*60)
	observed := time.Date(2026, 9, 1, 12, 0, 0, 123, zone)
	available := observed.Add(MinRetryDelay)

	initial := InitialInvocationOutcome()
	if initial.Kind() != InvocationOutcomeInitial || initial.IsZero() || !initial.valid() {
		t.Fatalf("initial = %v", initial)
	}
	active, err := ActiveAttemptOutcome(ordinal, observed)
	if err != nil || active.AttemptOrdinal() != ordinal || active.OccurredAt().Location() != time.UTC || active.TerminalReason() != ReasonNone || !active.valid() {
		t.Fatalf("active = (%v, %v)", active, err)
	}
	retry, _ := RetryDisposition(ReasonHandlerFailure, failure, 0, RetryCostCharged)
	finished, err := FinishedAttemptOutcome(ordinal, retry, ReasonNone, observed, available)
	if err != nil || finished.Disposition().Kind() != DispositionRetry || finished.Reason() != ReasonHandlerFailure || finished.Failure() != failure || finished.AvailableAt().Location() != time.UTC || finished.TerminalReason() != ReasonNone || !finished.valid() {
		t.Fatalf("finished = (%v, %v)", finished, err)
	}
	deferred, err := DeferredDeliveryOutcome(ReasonAdmission, failure, observed, available)
	if err != nil || deferred.Reason() != ReasonAdmission || deferred.Failure() != failure || deferred.AttemptOrdinal() != (AttemptOrdinal{}) || !deferred.valid() {
		t.Fatalf("deferred = (%v, %v)", deferred, err)
	}
	cancelRequested, err := CancelRequestedOutcome(ordinal, observed)
	if err != nil || cancelRequested.Kind() != InvocationOutcomeCancelRequested || cancelRequested.AttemptOrdinal() != ordinal || cancelRequested.Reason() != ReasonCancelRequested || !cancelRequested.valid() {
		t.Fatalf("cancel requested = (%v, %v)", cancelRequested, err)
	}
	terminal, err := TerminalDeliveryOutcome(InvocationDiscarded, ReasonPayload, ReasonPayload, failure, observed, time.Time{})
	if err != nil || terminal.Reason() != ReasonPayload || terminal.TerminalReason() != ReasonPayload || terminal.TerminalState() != InvocationDiscarded || terminal.Failure() != failure || !terminal.AvailableAt().IsZero() || !terminal.valid() {
		t.Fatalf("terminal = (%v, %v)", terminal, err)
	}
	if !(InvocationOutcome{}).IsZero() || (InvocationOutcome{}).valid() {
		t.Fatal("zero invocation outcome is valid")
	}
}

func TestInvocationOutcomeFormattingDoesNotExposeFailureOrDisposition(t *testing.T) {
	ordinal, _ := NewAttemptOrdinal(1)
	failure := testPublicFailure(t)
	disposition, _ := PermanentFailureDisposition(ReasonHandlerFailure, failure)
	outcome, err := FinishedAttemptOutcome(ordinal, disposition, ReasonNone, time.Now(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		formatted := fmt.Sprintf(format, outcome)
		if strings.Contains(formatted, failure.Code().Value()) || strings.Contains(formatted, failure.Message()) || !strings.Contains(formatted, "job invocation outcome") {
			t.Fatalf("format %q = %q", format, formatted)
		}
	}
}

func TestDeliveryDeferralAcceptsOnlyBoundedPreHandlerReasons(t *testing.T) {
	observed := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, reason := range []Reason{ReasonAdmission, ReasonCompatibility, ReasonDependency, ReasonShutdown, ReasonLeaseLost} {
		outcome, err := DeferredDeliveryOutcome(reason, PublicFailure{}, observed, observed.Add(MinRetryDelay))
		if err != nil || !outcome.valid() {
			t.Fatalf("reason %v = (%v, %v)", reason, outcome, err)
		}
	}
	for _, reason := range []Reason{ReasonNone, ReasonHandlerFailure, ReasonMaxElapsed, ReasonCancelRequested, ReasonRetryExhausted} {
		if outcome, err := DeferredDeliveryOutcome(reason, PublicFailure{}, observed, observed.Add(MinRetryDelay)); !errors.Is(err, ErrInvalid) || !outcome.IsZero() {
			t.Fatalf("reason %v = (%v, %v)", reason, outcome, err)
		}
	}
	for _, available := range []time.Time{observed, observed.Add(MinRetryDelay - 1), observed.Add(MaxRetryDelay + 1)} {
		if _, err := DeferredDeliveryOutcome(ReasonAdmission, PublicFailure{}, observed, available); !errors.Is(err, ErrInvalid) {
			t.Fatalf("available %v = %v", available, err)
		}
	}
}

func TestTerminalOutcomeCausePairsAreClosed(t *testing.T) {
	observed := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	valid := []struct {
		state    InvocationState
		reason   Reason
		terminal Reason
	}{
		{InvocationDead, ReasonMaxElapsed, ReasonMaxElapsed},
		{InvocationDead, ReasonAdmission, ReasonMaxElapsed},
		{InvocationDead, ReasonStartBefore, ReasonStartBefore},
		{InvocationDead, ReasonShutdown, ReasonStartBefore},
		{InvocationDiscarded, ReasonPayload, ReasonPayload},
		{InvocationQuarantined, ReasonCompatibility, ReasonCompatibility},
		{InvocationCancelled, ReasonCancelRequested, ReasonCancelRequested},
		{InvocationTerminated, ReasonOperatorTerminated, ReasonOperatorTerminated},
		{InvocationDead, ReasonDependency, ReasonDeferralsExhausted},
		{InvocationDead, ReasonLeaseLost, ReasonDeferralsExhausted},
		{InvocationDead, ReasonAdmission, ReasonAttemptsExhausted},
	}
	for _, test := range valid {
		if outcome, err := TerminalDeliveryOutcome(test.state, test.reason, test.terminal, PublicFailure{}, observed, time.Time{}); err != nil || !outcome.valid() {
			t.Fatalf("case %v = (%v, %v)", test, outcome, err)
		}
	}
	invalid := []struct {
		state    InvocationState
		reason   Reason
		terminal Reason
	}{
		{InvocationDead, ReasonNone, ReasonNone},
		{InvocationDead, ReasonAdmission, ReasonPayload},
		{InvocationDead, ReasonPayload, ReasonDeferralsExhausted},
		{InvocationCancelled, ReasonCancelRequested, ReasonMaxElapsed},
		{InvocationDead, ReasonHandlerFailure, ReasonMaxElapsed},
		{InvocationSucceeded, ReasonPayload, ReasonPayload},
	}
	for _, test := range invalid {
		if outcome, err := TerminalDeliveryOutcome(test.state, test.reason, test.terminal, PublicFailure{}, observed, time.Time{}); !errors.Is(err, ErrInvalid) || !outcome.IsZero() {
			t.Fatalf("case %v = (%v, %v)", test, outcome, err)
		}
	}
}

func TestFinishedOutcomeRejectsFakeAndAmbiguousAttempts(t *testing.T) {
	ordinal, _ := NewAttemptOrdinal(1)
	failure := testPublicFailure(t)
	observed := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	preHandler, _ := PermanentFailureDisposition(ReasonCompatibility, failure)
	retry, _ := RetryDisposition(ReasonHandlerFailure, failure, 0, RetryCostCharged)
	deferred, _ := DeferredDisposition(failure, MinRetryDelay)
	tests := []struct {
		ordinal     AttemptOrdinal
		disposition Disposition
		terminal    Reason
		available   time.Time
	}{
		{AttemptOrdinal{}, SuccessDisposition(), ReasonNone, time.Time{}},
		{ordinal, Disposition{}, ReasonNone, time.Time{}},
		{ordinal, preHandler, ReasonNone, time.Time{}},
		{ordinal, SuccessDisposition(), ReasonRetryExhausted, time.Time{}},
		{ordinal, retry, ReasonNone, time.Time{}},
		{ordinal, retry, ReasonRetryExhausted, observed.Add(MinRetryDelay)},
		{ordinal, deferred, ReasonRetryExhausted, time.Time{}},
		{ordinal, deferred, ReasonAttemptsExhausted, observed.Add(MinRetryDelay)},
	}
	for index, test := range tests {
		if outcome, err := FinishedAttemptOutcome(test.ordinal, test.disposition, test.terminal, observed, test.available); !errors.Is(err, ErrInvalid) || !outcome.IsZero() {
			t.Fatalf("case %d = (%v, %v)", index, outcome, err)
		}
	}
}
