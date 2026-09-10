package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"reflect"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/port"
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

type panicNilEntropyReader struct{}

func (panicNilEntropyReader) Read([]byte) (int, error) { panic(nil) }

type panicNilClock struct{}

func (panicNilClock) Now() time.Time { panic(nil) }

func (panicNilClock) NewTimerAt(time.Time) Timer { panic("unexpected timer") }

type panicNilCodec struct{ phase string }

func (panicNilCodec) ID() CodecID            { return builtinCodecID("panicnil") }
func (panicNilCodec) Version() SchemaVersion { return 1 }
func (codec panicNilCodec) Encode(value string, _ PayloadLimit) ([]byte, error) {
	if codec.phase == "encode" {
		panic(nil)
	}
	return []byte(value), nil
}
func (codec panicNilCodec) Decode(value []byte, _ PayloadLimit) (string, error) {
	if codec.phase == "decode" {
		panic(nil)
	}
	return string(value), nil
}

type panicNilLeaseFence struct{}

func (panicNilLeaseFence) Fence(context.Context, LeaseRef) error { panic(nil) }

type panicNilTimerClock struct{ timer Timer }

func (panicNilTimerClock) Now() time.Time { return time.Date(2035, 1, 2, 3, 0, 0, 0, time.UTC) }
func (clock panicNilTimerClock) NewTimerAt(time.Time) Timer {
	if clock.timer == nil {
		panic(nil)
	}
	return clock.timer
}

type panicNilTimer struct {
	phase string
	stops *atomic.Int32
}

func (timer *panicNilTimer) C() <-chan time.Time {
	if timer.phase == "channel" {
		panic(nil)
	}
	return make(chan time.Time)
}

func (timer *panicNilTimer) Stop() bool {
	if timer.phase == "stop" {
		panic(nil)
	}
	timer.stops.Add(1)
	return true
}

type panicNilErrContext struct{ context.Context }

func (panicNilErrContext) Err() error { panic(nil) }

type panicNilDescriptionDriver struct{ DeliveryDriver }

func (panicNilDescriptionDriver) Description() BackendDescription { panic(nil) }

func (payload classifierPanicPayload) String() string {
	payload.formats.Add(1)
	return payload.secret
}

func TestHandlerFailureContainsErrorsAndErasesPanicPayloads(t *testing.T) {
	sentinel := errors.New("handler-private-sentinel")
	typed := &classifierTypedError{secret: "handler-private-typed"}
	result := invokeHandlerContained(context.Background(), func() error {
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
	typedNilResult := invokeHandlerContained(context.Background(), func() error { return typedNil })
	typedNilFailure, ok := typedNilResult.(HandlerFailure)
	if !ok || typedNilFailure.Panicked() || typedNilFailure.Unwrap() == nil {
		t.Fatalf("typed nil error = (%T, panicked=%v, unwrap=%#v)", typedNilResult, typedNilFailure.Panicked(), typedNilFailure.Unwrap())
	}
	extracted = typed
	if !errors.As(typedNilFailure, &extracted) || extracted != nil {
		t.Fatalf("typed nil identity = %#v", extracted)
	}

	var formats atomic.Int32
	panicked := invokeHandlerContained(context.Background(), func() error {
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
	normal := invokeHandlerContained(context.Background(), func() error { return ErrConflict })
	defaultNormal := classifyHandlerResult(nil, normal)
	assertClassifierDisposition(t, defaultNormal, DispositionRetry, ReasonHandlerFailure, RetryCostCharged)
	classified := classifyHandlerResult(classifier, normal)
	assertClassifierDisposition(t, classified, DispositionPermanentFailure, ReasonHandlerFailure, RetryCostNone)
	if calls.Load() != 1 {
		t.Fatalf("classifier calls = %d", calls.Load())
	}

	panicked := invokeHandlerContained(context.Background(), func() error { panic("panic-private") })
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

func TestJobsPanicNilBoundaries(t *testing.T) {
	if os.Getenv("VV_JOBS_PANICNIL_HELPER") == "1" {
		result := invokeHandlerContained(context.Background(), func() error { panic(nil) })
		failure, ok := result.(HandlerFailure)
		if !ok || !failure.Panicked() || failure.Unwrap() != nil {
			t.Fatalf("panic(nil) handler result = %#v", result)
		}
		disposition := classifyHandlerResult(nil, result)
		assertClassifierDisposition(t, disposition, DispositionRetry, ReasonPanic, RetryCostCharged)

		classifierResult := classifyHandlerFailure(func(HandlerFailure) Disposition { panic(nil) }, HandlerFailure{cause: ErrConflict, initialized: true})
		assertClassifierDisposition(t, classifierResult, DispositionRetry, ReasonClassifier, RetryCostCharged)

		definition := MustDefine(DefinitionSpec[string]{
			Name:   testJobName(t, "maintenance.panicnil"),
			Codec:  String(1),
			Policy: testPolicy(t),
		})
		sender := &scheduleSender{description: queueTestBackendDescription(1)}
		queue, err := NewQueue(QueueSpec{
			Namespace: queueTestNamespace(t, "scheduler-panicnil"),
			Catalog:   MustCatalog(definition),
			Sender:    sender,
		})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2035, 1, 2, 3, 0, 0, 0, time.UTC)
		schedule, err := DefineSchedule(ScheduleSpec[string]{
			Name:     testJobName(t, "maintenance.panicnil.hourly"),
			Revision: 1,
			Cadence:  At(now),
			Job:      definition,
			Payload:  func(time.Time) (string, error) { panic(nil) },
		})
		if err != nil {
			t.Fatal(err)
		}
		scheduler, err := NewScheduler(SchedulerSpec{Queue: queue, Clock: scheduleClock{now: now}}, schedule)
		if err != nil {
			t.Fatal(err)
		}
		scheduleResult, err := scheduler.RunDue(context.Background())
		if !errors.Is(err, ErrInvalid) || scheduleResult != (ScheduleRunResult{Due: 1}) {
			t.Fatalf("panic(nil) schedule result = (%+v, %v)", scheduleResult, err)
		}
		sender.mu.Lock()
		placements := len(sender.placements)
		sender.mu.Unlock()
		if placements != 0 {
			t.Fatalf("panic(nil) payload placed %d jobs", placements)
		}

		entropyQueue, err := NewQueue(QueueSpec{
			Namespace: queueTestNamespace(t, "entropy-panicnil"),
			Catalog:   MustCatalog(definition),
			Sender:    sender,
			Entropy:   panicNilEntropyReader{},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Enqueue(context.Background(), entropyQueue, definition, "payload"); !errors.Is(err, ErrEntropy) {
			t.Fatalf("panic(nil) entropy error = %v", err)
		}

		clockScheduler, err := NewScheduler(SchedulerSpec{Queue: queue, Clock: panicNilClock{}}, schedule)
		if err != nil {
			t.Fatal(err)
		}
		clockResult, err := clockScheduler.RunDue(context.Background())
		if !errors.Is(err, ErrInvalid) || clockResult != (ScheduleRunResult{}) {
			t.Fatalf("panic(nil) clock result = (%+v, %v)", clockResult, err)
		}
		sender.mu.Lock()
		placements = len(sender.placements)
		sender.mu.Unlock()
		if placements != 0 {
			t.Fatalf("panic(nil) entropy or clock placed %d jobs", placements)
		}

		encodeDefinition := MustDefine(DefinitionSpec[string]{
			Name:   testJobName(t, "codec.panicnil.encode"),
			Codec:  panicNilCodec{phase: "encode"},
			Policy: testPolicy(t),
		})
		encodeQueue, err := NewQueue(QueueSpec{
			Namespace: queueTestNamespace(t, "codec-encode-panicnil"),
			Catalog:   MustCatalog(encodeDefinition),
			Sender:    sender,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Enqueue(context.Background(), encodeQueue, encodeDefinition, "payload"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("panic(nil) codec encode = %v", err)
		}
		sender.mu.Lock()
		placements = len(sender.placements)
		sender.mu.Unlock()
		if placements != 0 {
			t.Fatalf("panic(nil) codec encode placed %d jobs", placements)
		}

		decodeDefinition := MustDefine(DefinitionSpec[string]{
			Name:   testJobName(t, "codec.panicnil.decode"),
			Codec:  panicNilCodec{phase: "decode"},
			Policy: testPolicy(t),
		})
		encoded, err := NewEncodedPayload(panicNilCodec{}.ID(), 1, []byte("payload"))
		if err != nil {
			t.Fatal(err)
		}
		if decoded, err := decodeDefinition.Decode(encoded); !errors.Is(err, ErrInvalid) || decoded != "" {
			t.Fatalf("panic(nil) codec decode = (%q, %v)", decoded, err)
		}
		if decoded, err := decodeClaimedPayloadOwned(consumerBinding{decodeOwned: func(EncodedPayload) (any, error) { panic(nil) }}, encoded); !errors.Is(err, ErrInvalid) || decoded != nil {
			t.Fatalf("panic(nil) claimed decode = (%#v, %v)", decoded, err)
		}

		upcastDefinition := MustDefine(DefinitionSpec[string]{
			Name:  testJobName(t, "codec.panicnil.upcast"),
			Codec: String(2),
			Upcasters: []Upcaster{Upcast(String(1), String(2), func(string) (string, error) {
				panic(nil)
			})},
			Policy: testPolicy(t),
		})
		upcastPayload, err := NewEncodedPayload(String(1).ID(), 1, []byte("payload"))
		if err != nil {
			t.Fatal(err)
		}
		if decoded, err := upcastDefinition.Decode(upcastPayload); !errors.Is(err, ErrInvalid) || decoded != "" {
			t.Fatalf("panic(nil) upcast = (%q, %v)", decoded, err)
		}

		_, _, _, _, _, request := identityRestoreFixture(t)
		if restored, err := RestoreTrustedIdentity(context.Background(), TrustedIdentityRestorerFunc(func(context.Context, IdentityRestoreRequest) (RestoredIdentity, error) {
			panic(nil)
		}), request); !errors.Is(err, ErrDriver) || restored != nil {
			t.Fatalf("panic(nil) identity restore = (%v, %v)", restored, err)
		}

		delivery := &activeWorkerDelivery{}
		if err := (workerAttemptController{delivery: delivery}).Guard(context.Background(), panicNilLeaseFence{}); !errors.Is(err, ErrDriver) {
			t.Fatalf("panic(nil) fence = %v", err)
		}

		if inner, channel, err := callWorkerClockTimer(panicNilTimerClock{}, time.Now()); !errors.Is(err, ErrInvalid) || inner != nil || channel != nil {
			t.Fatalf("panic(nil) timer constructor = (%v, %v, %v)", inner, channel, err)
		}
		var timerStops atomic.Int32
		channelTimer := &panicNilTimer{phase: "channel", stops: &timerStops}
		if inner, channel, err := callWorkerClockTimer(panicNilTimerClock{timer: channelTimer}, time.Now()); !errors.Is(err, ErrInvalid) || inner != nil || channel != nil || timerStops.Load() != 1 {
			t.Fatalf("panic(nil) timer channel = (%v, %v, %v, stops=%d)", inner, channel, err, timerStops.Load())
		}
		if stopped, valid := stopWorkerTimerChecked(&panicNilTimer{phase: "stop", stops: &timerStops}); stopped || valid {
			t.Fatalf("panic(nil) timer stop = (%t, %t)", stopped, valid)
		}

		if description, err := ValidateDeliveryDriver(panicNilDescriptionDriver{}); !errors.Is(err, ErrInvalid) || description.valid() {
			t.Fatalf("panic(nil) driver description = (%+v, %v)", description, err)
		}

		fatal := newWorkerFailureLatch()
		if value, err, failure := callWorkerDriver(fatal, context.Background(), func(context.Context) (int, error) { panic(nil) }); value != 0 || !errors.Is(err, ErrDriver) || failure != WorkerFailureDriverPanic {
			t.Fatalf("panic(nil) driver call = (%d, %v, %v)", value, err, failure)
		}
		fatal = newWorkerFailureLatch()
		if value, failure := validateWorkerDriverResult(fatal, 1, func(int) (int, error) { panic(nil) }); value != 0 || failure != WorkerFailureRuntime {
			t.Fatalf("panic(nil) driver validation = (%d, %v)", value, failure)
		}

		spec, consumer, _, _ := workersConfigFixture(t, "workers.driver-envelope-panicnil")
		workers, err := NewWorkers(spec, consumer)
		if err != nil {
			t.Fatal(err)
		}
		value, call := invokeWorkerDriver(workers.driverBoundary(), panicNilErrContext{Context: context.Background()}, func(context.Context) (int, error) {
			t.Fatal("driver callback ran after the parent boundary panicked")
			return 1, nil
		}, func(value int) (int, error) { return value, nil })
		if value != 0 || call.failure != WorkerFailureRuntime || !errors.Is(call.err, ErrInvalid) {
			t.Fatalf("panic(nil) worker envelope = (%d, %+v)", value, call)
		}
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestJobsPanicNilBoundaries$")
	command.Env = jobsPanicNilEnvironment("VV_JOBS_PANICNIL_HELPER=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("panic(nil) subprocess: %v\n%s", err, output)
	}
}

func jobsPanicNilEnvironment(marker string) []string {
	environment := make([]string, 0, len(os.Environ())+2)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GODEBUG=") {
			environment = append(environment, value)
		}
	}
	return append(environment, "GODEBUG=panicnil=1", marker)
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

// A panic is contained and turned into a disposition, so nothing above ever sees
// it. That used to make it invisible: the value and the stack were dropped and an
// operator was left with "jobs: handler failed" and no way to find out what
// failed or where.
func TestAContainedPanicKeepsItsValueAndStackAndLogsNeitherVerbatim(t *testing.T) {
	var formats atomic.Int32
	var written bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&written, nil))

	failed := invokeHandlerContained(port.WithLogger(context.Background(), logger), func() error {
		panic(classifierPanicPayload{formats: &formats, secret: "panic-private"})
	})
	failure, ok := failed.(HandlerFailure)
	if !ok || !failure.Panicked() {
		t.Fatalf("failure = %#v", failed)
	}

	recovered, present := failure.Recovered()
	if !present {
		t.Fatal("the panic value was discarded, so nothing can say what failed")
	}
	if _, isPayload := recovered.(classifierPanicPayload); !isPayload {
		t.Fatalf("recovered = %T, want the value the handler panicked with", recovered)
	}
	if len(failure.Stack()) == 0 || !strings.Contains(string(failure.Stack()), "invokeHandlerContained") {
		t.Fatal("the stack was discarded, so nothing can say where it failed")
	}

	line := written.String()
	if !strings.Contains(line, "handler panicked") || !strings.Contains(line, "classifierPanicPayload") {
		t.Fatalf("the caller's logger was told nothing useful: %s", line)
	}
	// The panic payload is application data on a path that is already unwinding:
	// its Format must not run, and its contents must not reach the line.
	if formats.Load() != 0 {
		t.Fatalf("the panic payload was formatted %d times", formats.Load())
	}
	if strings.Contains(line, "panic-private") {
		t.Fatalf("the panic payload's contents reached the log line: %s", line)
	}
	assertHandlerFailureRedacted(t, failure, "panic-private")
}

func TestHandlerGoexitIsNotLoggedAsAPanic(t *testing.T) {
	var written bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&written, nil))
	returned := atomic.Bool{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = invokeHandlerContained(port.WithLogger(context.Background(), logger), func() error {
			goruntime.Goexit()
			return nil
		})
		returned.Store(true)
	}()
	<-done
	if returned.Load() {
		t.Fatal("Goexit returned from the handler boundary")
	}
	if written.Len() != 0 {
		t.Fatalf("Goexit was logged as a panic: %s", written.String())
	}
}
