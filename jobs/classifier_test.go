package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type classifierTypedError struct{ secret string }

func (err *classifierTypedError) Error() string {
	if err == nil {
		return "typed nil"
	}
	return err.secret
}

type classifierPanicPayload struct {
	formats *atomic.Int32
	secret  string
}

func (payload classifierPanicPayload) String() string {
	payload.formats.Add(1)
	return payload.secret
}

func TestHandlerFailureContainsErrorsAndErasesPanicPayloads(t *testing.T) {
	sentinel := errors.New("handler-private-sentinel")
	typed := &classifierTypedError{secret: "handler-private-typed"}
	result := invokeHandlerContained(func() error {
		return fmt.Errorf("handler-private-wrapper: %w: %w", sentinel, typed)
	})
	failure, ok := result.(HandlerFailure)
	if !ok || failure.IsZero() || failure.Panicked() || !errors.Is(failure, sentinel) {
		t.Fatalf("normal failure = (%T, zero=%v, panicked=%v)", result, failure.IsZero(), failure.Panicked())
	}
	var extracted *classifierTypedError
	if !errors.As(failure, &extracted) || extracted != typed {
		t.Fatal("handler error identity was not preserved")
	}
	assertHandlerFailureRedacted(t, failure, "handler-private")

	var typedNil *classifierTypedError
	typedNilResult := invokeHandlerContained(func() error { return typedNil })
	typedNilFailure, ok := typedNilResult.(HandlerFailure)
	if !ok || typedNilFailure.Panicked() || typedNilFailure.Unwrap() == nil {
		t.Fatalf("typed nil error = (%T, panicked=%v, unwrap=%#v)", typedNilResult, typedNilFailure.Panicked(), typedNilFailure.Unwrap())
	}
	extracted = typed
	if !errors.As(typedNilFailure, &extracted) || extracted != nil {
		t.Fatalf("typed nil identity = %#v", extracted)
	}

	var formats atomic.Int32
	panicked := invokeHandlerContained(func() error {
		panic(classifierPanicPayload{formats: &formats, secret: "panic-private"})
	})
	panicFailure, ok := panicked.(HandlerFailure)
	if !ok || !panicFailure.Panicked() || panicFailure.Unwrap() != nil || formats.Load() != 0 {
		t.Fatalf("panic failure = (%T, panicked=%v, unwrap=%#v, formats=%d)", panicked, panicFailure.Panicked(), panicFailure.Unwrap(), formats.Load())
	}
	assertHandlerFailureRedacted(t, panicFailure, "panic-private")

	zero := HandlerFailure{}
	if !zero.IsZero() || zero.Panicked() || zero.Unwrap() != nil {
		t.Fatalf("zero failure = (zero=%v, panicked=%v, unwrap=%#v)", zero.IsZero(), zero.Panicked(), zero.Unwrap())
	}
}

func TestClassifierDefaultsSkipsSuccessAndRunsExactlyOnce(t *testing.T) {
	var calls atomic.Int32
	classifier := ErrorClassifier(func(failure HandlerFailure) Disposition {
		calls.Add(1)
		if failure.Panicked() || !errors.Is(failure, ErrConflict) {
			t.Fatal("classifier received the wrong normal failure")
		}
		disposition, err := PermanentFailureDisposition(ReasonHandlerFailure, PublicFailure{})
		if err != nil {
			t.Fatal(err)
		}
		return disposition
	})
	if disposition := classifyHandlerResult(classifier, nil); disposition.Kind() != DispositionSucceeded || calls.Load() != 0 {
		t.Fatalf("nil result = (%v, calls=%d)", disposition.Kind(), calls.Load())
	}
	normal := invokeHandlerContained(func() error { return ErrConflict })
	defaultNormal := classifyHandlerResult(nil, normal)
	assertClassifierDisposition(t, defaultNormal, DispositionRetry, ReasonHandlerFailure, RetryCostCharged)
	classified := classifyHandlerResult(classifier, normal)
	assertClassifierDisposition(t, classified, DispositionPermanentFailure, ReasonHandlerFailure, RetryCostNone)
	if calls.Load() != 1 {
		t.Fatalf("classifier calls = %d", calls.Load())
	}

	panicked := invokeHandlerContained(func() error { panic("panic-private") })
	defaultPanic := classifyHandlerResult(nil, panicked)
	assertClassifierDisposition(t, defaultPanic, DispositionRetry, ReasonPanic, RetryCostCharged)
	var panicCalls atomic.Int32
	panicClassifier := ErrorClassifier(func(failure HandlerFailure) Disposition {
		panicCalls.Add(1)
		if !failure.Panicked() || failure.Unwrap() != nil {
			t.Fatal("classifier received panic internals")
		}
		disposition, err := QuarantineDisposition(ReasonPanic, PublicFailure{})
		if err != nil {
			t.Fatal(err)
		}
		return disposition
	})
	panicDisposition := classifyHandlerResult(panicClassifier, panicked)
	assertClassifierDisposition(t, panicDisposition, DispositionQuarantine, ReasonPanic, RetryCostNone)
	if panicCalls.Load() != 1 {
		t.Fatalf("panic classifier calls = %d", panicCalls.Load())
	}

	var successCalls atomic.Int32
	successClassifier := ErrorClassifier(func(HandlerFailure) Disposition {
		successCalls.Add(1)
		return SuccessDisposition()
	})
	if disposition := classifyHandlerResult(successClassifier, normal); disposition.Kind() != DispositionSucceeded || successCalls.Load() != 1 {
		t.Fatalf("classified success = (%v, calls=%d)", disposition.Kind(), successCalls.Load())
	}
}

func TestClassifierFailsClosedForPanicInvalidAndControlPlaneResults(t *testing.T) {
	normal := HandlerFailure{cause: errors.New("classifier-input-private"), initialized: true}
	panicked := HandlerFailure{panicked: true, initialized: true}
	retry := func(reason Reason, cost RetryCost) Disposition {
		disposition, err := RetryDisposition(reason, PublicFailure{}, 0, cost)
		if err != nil {
			t.Fatal(err)
		}
		return disposition
	}
	cancelled, _ := CancelledDisposition(ReasonCancelRequested)
	compatibility, _ := PermanentFailureDisposition(ReasonCompatibility, PublicFailure{})
	deferred, _ := DeferredDisposition(PublicFailure{}, MinRetryDelay)
	tests := []struct {
		name       string
		failure    HandlerFailure
		classifier ErrorClassifier
	}{
		{name: "panic", failure: normal, classifier: func(HandlerFailure) Disposition { panic("classifier-private") }},
		{name: "zero", failure: normal, classifier: func(HandlerFailure) Disposition { return Disposition{} }},
		{name: "invalid", failure: normal, classifier: func(HandlerFailure) Disposition {
			return Disposition{kind: DispositionRetry, reason: ReasonHandlerFailure}
		}},
		{name: "cancelled", failure: normal, classifier: func(HandlerFailure) Disposition { return cancelled }},
		{name: "terminated", failure: normal, classifier: func(HandlerFailure) Disposition { return TerminatedDisposition() }},
		{name: "attempt timeout", failure: normal, classifier: func(HandlerFailure) Disposition { return retry(ReasonAttemptTimeout, RetryCostCharged) }},
		{name: "progress timeout", failure: normal, classifier: func(HandlerFailure) Disposition { return retry(ReasonProgressTimeout, RetryCostCharged) }},
		{name: "shutdown", failure: normal, classifier: func(HandlerFailure) Disposition { return retry(ReasonShutdown, RetryCostNone) }},
		{name: "lease lost", failure: normal, classifier: func(HandlerFailure) Disposition { return retry(ReasonLeaseLost, RetryCostNone) }},
		{name: "compatibility", failure: normal, classifier: func(HandlerFailure) Disposition { return compatibility }},
		{name: "classifier reason", failure: normal, classifier: func(HandlerFailure) Disposition { return retry(ReasonClassifier, RetryCostCharged) }},
		{name: "normal as panic", failure: normal, classifier: func(HandlerFailure) Disposition { return retry(ReasonPanic, RetryCostCharged) }},
		{name: "panic as normal", failure: panicked, classifier: func(HandlerFailure) Disposition { return retry(ReasonHandlerFailure, RetryCostCharged) }},
		{name: "panic as success", failure: panicked, classifier: func(HandlerFailure) Disposition { return SuccessDisposition() }},
		{name: "panic as dependency", failure: panicked, classifier: func(HandlerFailure) Disposition { return deferred }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			classifier := ErrorClassifier(func(failure HandlerFailure) Disposition {
				calls.Add(1)
				return test.classifier(failure)
			})
			disposition := classifyHandlerFailure(classifier, test.failure)
			assertClassifierDisposition(t, disposition, DispositionRetry, ReasonClassifier, RetryCostCharged)
			if calls.Load() != 1 || !disposition.Failure().IsZero() || disposition.RetryAfter() != 0 {
				t.Fatalf("fallback = (%v, calls=%d, failure=%v, delay=%s)", disposition, calls.Load(), disposition.Failure(), disposition.RetryAfter())
			}
		})
	}
	var calls atomic.Int32
	disposition := classifyHandlerFailure(func(HandlerFailure) Disposition {
		calls.Add(1)
		return SuccessDisposition()
	}, HandlerFailure{})
	assertClassifierDisposition(t, disposition, DispositionRetry, ReasonClassifier, RetryCostCharged)
	if calls.Load() != 0 {
		t.Fatalf("zero failure reached classifier %d times", calls.Load())
	}
}

func TestClassifyOptionIsValidatedVisibleAndPureDuringPlanning(t *testing.T) {
	definition := testQueueDefinition(t, "workers.classifier", String(1))
	catalog := MustCatalog(definition)
	beforeFingerprint := catalog.Fingerprint()
	beforeDescription := catalog.Describe()
	var calls atomic.Int32
	classifier := ErrorClassifier(func(HandlerFailure) Disposition {
		calls.Add(1)
		return SuccessDisposition()
	})
	consumer := On(definition, Handler[string](func(context.Context, string) error { return ErrConflict }), Concurrency(1), Classify(classifier))
	plan, err := NewWorkerPlan(catalog, consumer)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || !plan.Describe().Bindings[0].CustomClassifier || plan.Describe().Bindings[0].Adapter {
		t.Fatalf("planned classifier = (calls=%d, description=%#v)", calls.Load(), plan.Describe().Bindings[0])
	}
	if catalog.Fingerprint() != beforeFingerprint || !reflect.DeepEqual(catalog.Describe(), beforeDescription) {
		t.Fatal("classifier binding changed the durable catalog")
	}
	binding := plan.workerBindings()[0]
	encoded, err := definition.Encode("payload")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := binding.decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	disposition := classifyHandlerResult(binding.classifier, binding.handle(context.Background(), decoded))
	if disposition.Kind() != DispositionSucceeded || calls.Load() != 1 {
		t.Fatalf("bound classifier = (%v, calls=%d)", disposition.Kind(), calls.Load())
	}
	var nilClassifier ErrorClassifier
	if _, err := NewWorkerPlan(catalog, On(definition, Handler[string](func(context.Context, string) error { return nil }), Concurrency(1), Classify(nilClassifier))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil classifier = %v", err)
	}
	if _, err := NewWorkerPlan(catalog, On(definition, Handler[string](func(context.Context, string) error { return nil }), Concurrency(1), Classify(classifier), Classify(classifier))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate classifier = %v", err)
	}
}

func assertHandlerFailureRedacted(t *testing.T, failure HandlerFailure, secret string) {
	t.Helper()
	values := []string{
		failure.Error(),
		failure.String(),
		fmt.Sprint(failure),
		fmt.Sprintf("%+v", failure),
		fmt.Sprintf("%#v", failure),
		failure.LogValue().String(),
		slog.AnyValue(failure).Resolve().String(),
	}
	for _, value := range values {
		if strings.Contains(value, secret) {
			t.Fatalf("handler failure leaked secret: %q", value)
		}
	}
	encoded, err := json.Marshal(failure)
	if !errors.Is(err, ErrUnsupported) || len(encoded) != 0 || strings.Contains(fmt.Sprint(err), secret) {
		t.Fatalf("handler failure JSON = (%q, %v)", encoded, err)
	}
}

func assertClassifierDisposition(t *testing.T, disposition Disposition, kind DispositionKind, reason Reason, cost RetryCost) {
	t.Helper()
	if !disposition.valid() || disposition.Kind() != kind || disposition.Reason() != reason || disposition.RetryCost() != cost {
		t.Fatalf("disposition = (kind=%v, reason=%v, cost=%v, valid=%v)", disposition.Kind(), disposition.Reason(), disposition.RetryCost(), disposition.valid())
	}
}
