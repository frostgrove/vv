package jobs

import (
	"errors"
	"fmt"
	"testing"
)

func TestDefaultClassifierRecognizesHandlerIntent(t *testing.T) {
	cause := errors.New("dependency failed")
	tests := []struct {
		name   string
		err    error
		kind   DispositionKind
		reason Reason
		after  bool
	}{
		{name: "permanent", err: Permanent(cause), kind: DispositionPermanentFailure, reason: ReasonHandlerFailure},
		{name: "wrapped permanent", err: fmt.Errorf("work: %w", Permanent(cause)), kind: DispositionPermanentFailure, reason: ReasonHandlerFailure},
		{name: "deferred", err: Deferred(cause), kind: DispositionDeferred, reason: ReasonDependency, after: true},
		{name: "wrapped deferred", err: fmt.Errorf("work: %w", Deferred(cause)), kind: DispositionDeferred, reason: ReasonDependency, after: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			disposition := classifyHandlerResult(nil, invokeHandlerContained(func() error { return test.err }))
			if disposition.Kind() != test.kind || disposition.Reason() != test.reason {
				t.Fatalf("disposition = (%s, %s), want (%s, %s)", disposition.Kind(), disposition.Reason(), test.kind, test.reason)
			}
			if test.after != (disposition.RetryAfter() == DefaultRetryDelay) {
				t.Fatalf("retry delay = %s", disposition.RetryAfter())
			}
		})
	}
}

func TestHandlerIntentHelpersPreserveCausesAndNil(t *testing.T) {
	cause := errors.New("failure")
	if Permanent(nil) != nil || Deferred(nil) != nil {
		t.Fatal("nil error was classified")
	}
	if err := Permanent(cause); !errors.Is(err, cause) || !IsPermanent(err) || IsDeferred(err) {
		t.Fatalf("permanent error = %v", err)
	}
	if err := Deferred(cause); !errors.Is(err, cause) || !IsDeferred(err) || IsPermanent(err) {
		t.Fatalf("deferred error = %v", err)
	}
}

func TestCustomClassifierStillOwnsClassifiedErrors(t *testing.T) {
	custom, _ := RetryDisposition(ReasonHandlerFailure, PublicFailure{}, 0, RetryCostCharged)
	disposition := classifyHandlerResult(func(HandlerFailure) Disposition { return custom }, Permanent(errors.New("stop")))
	if disposition.Kind() != DispositionRetry {
		t.Fatalf("custom classifier result = %s", disposition.Kind())
	}
}

type panickingHandlerError struct{}

func (panickingHandlerError) Error() string { return "failure" }
func (panickingHandlerError) As(any) bool   { panic("broken As") }

func TestDefaultClassifierContainsBrokenErrorChains(t *testing.T) {
	disposition := classifyHandlerResult(nil, panickingHandlerError{})
	if disposition.Kind() != DispositionRetry || disposition.Reason() != ReasonClassifier {
		t.Fatalf("disposition = (%s, %s)", disposition.Kind(), disposition.Reason())
	}
}

func TestOutermostHandlerIntentWins(t *testing.T) {
	cause := errors.New("failure")
	tests := []struct {
		err  error
		kind DispositionKind
	}{
		{err: Permanent(Deferred(cause)), kind: DispositionPermanentFailure},
		{err: Deferred(Permanent(cause)), kind: DispositionDeferred},
	}
	for _, test := range tests {
		disposition := classifyHandlerResult(nil, test.err)
		if disposition.Kind() != test.kind {
			t.Fatalf("disposition = %s, want %s", disposition.Kind(), test.kind)
		}
	}
}
