package vvotel_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/storage"
)

type failsafeContextKey string

func TestTelemetry_DisabledDoesNotCallProviders(t *testing.T) {
	tp := newTestTracerProvider()
	tp.panicTracer = true
	mp := newTestMeterProvider()
	mp.panicMeter = true
	if _, err := vvotel.New(vvotel.Config{Disabled: true, TracerProvider: tp, MeterProvider: mp}); err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if tp.tracerCalls != 0 || tp.spans != nil || mp.metrics != nil || mp.meters != 0 {
		t.Fatal("disabled telemetry touched providers")
	}
}

func TestTelemetry_TypedNilProviderIsRejected(t *testing.T) {
	var tp *testTracerProvider
	if _, err := vvotel.New(vvotel.Config{TracerProvider: tp}); !errors.Is(err, vvotel.ErrNilProvider) {
		t.Fatalf("expected ErrNilProvider, got %v", err)
	}
}

func TestLegacyPanicNilModePreservesPanicAndGoexitSemantics(t *testing.T) {
	targets := []string{
		"TestService_PanicNilIsNotSuppressed",
		"TestStorage_PanicNilIsNotSuppressed",
		"TestService_GoexitPreservesGoroutineTermination",
		"TestStorage_GoexitPreservesGoroutineTermination",
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable,
		"-test.run=^("+strings.Join(targets, "|")+")$",
		"-test.count=1",
		"-test.v",
	)
	environment := make([]string, 0, len(os.Environ())+1)
	for _, variable := range os.Environ() {
		key, _, found := strings.Cut(variable, "=")
		if found && strings.EqualFold(key, "GODEBUG") {
			continue
		}
		environment = append(environment, variable)
	}
	command.Env = append(environment, "GODEBUG=panicnil=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("legacy panicnil child failed: %v\n%s", err, output)
	}
	transcript := string(output)
	for _, target := range targets {
		if !strings.Contains(transcript, "=== RUN   "+target) || !strings.Contains(transcript, "--- PASS: "+target) {
			t.Fatalf("legacy panicnil child did not execute and pass %s:\n%s", target, transcript)
		}
	}
}

func TestTelemetryFaultsNeverChangeBusinessResults(t *testing.T) {
	type fault struct {
		name               string
		configure          func(*testTracerProvider, *testMeterProvider)
		resource           bool
		businessError      bool
		serviceOnly        bool
		fallbackContext    bool
		wantSpans          int
		wantAttributes     int
		wantStatus         int
		wantEnds           int
		wantRecordedEnd    bool
		wantServiceMetrics int
	}
	faults := []fault{
		{
			name: "provider_is_not_queried_again_after_assembly",
			configure: func(tp *testTracerProvider, _ *testMeterProvider) {
				tp.panicTracer = true
			},
			wantSpans:          1,
			wantAttributes:     1,
			wantEnds:           1,
			wantRecordedEnd:    true,
			wantServiceMetrics: 1,
		},
		{
			name: "tracer_start_panics",
			configure: func(tp *testTracerProvider, _ *testMeterProvider) {
				tp.panicStart = true
			},
			fallbackContext:    true,
			wantServiceMetrics: 1,
		},
		{
			name: "tracer_returns_typed_nil_span",
			configure: func(tp *testTracerProvider, _ *testMeterProvider) {
				tp.typedNilSpan = true
			},
			fallbackContext:    true,
			wantServiceMetrics: 1,
		},
		{
			name: "tracer_returns_typed_nil_context",
			configure: func(tp *testTracerProvider, _ *testMeterProvider) {
				tp.typedNilContext = true
			},
			fallbackContext:    true,
			wantSpans:          1,
			wantEnds:           1,
			wantRecordedEnd:    true,
			wantServiceMetrics: 1,
		},
		{
			name: "span_setup_attributes_panics",
			configure: func(tp *testTracerProvider, _ *testMeterProvider) {
				tp.panicAttributes = true
			},
			resource:           true,
			fallbackContext:    true,
			wantSpans:          1,
			wantAttributes:     1,
			wantEnds:           1,
			wantRecordedEnd:    true,
			wantServiceMetrics: 1,
		},
		{
			name: "span_terminal_attributes_panics",
			configure: func(tp *testTracerProvider, _ *testMeterProvider) {
				tp.panicAttributes = true
			},
			wantSpans:          1,
			wantAttributes:     1,
			wantEnds:           1,
			wantRecordedEnd:    true,
			wantServiceMetrics: 1,
		},
		{
			name: "span_status_panics",
			configure: func(tp *testTracerProvider, _ *testMeterProvider) {
				tp.panicStatus = true
			},
			businessError:      true,
			wantSpans:          1,
			wantAttributes:     1,
			wantStatus:         1,
			wantEnds:           1,
			wantRecordedEnd:    true,
			wantServiceMetrics: 1,
		},
		{
			name: "span_end_panics",
			configure: func(tp *testTracerProvider, _ *testMeterProvider) {
				tp.panicEnd = true
			},
			wantSpans:          1,
			wantAttributes:     1,
			wantEnds:           1,
			wantServiceMetrics: 1,
		},
		{
			name: "histogram_record_panics",
			configure: func(_ *testTracerProvider, mp *testMeterProvider) {
				mp.panicHistogramRecord = true
			},
			serviceOnly:     true,
			wantSpans:       1,
			wantAttributes:  1,
			wantEnds:        1,
			wantRecordedEnd: true,
		},
	}

	type observation struct {
		ctx            context.Context
		calls          int
		operationCalls int
		err            error
	}
	type adapter struct {
		name      string
		histogram bool
		exercise  func(*testing.T, *vvotel.Telemetry, context.Context, bool, error) observation
	}
	adapters := []adapter{
		{
			name:      "service",
			histogram: true,
			exercise: func(t *testing.T, tel *vvotel.Telemetry, ctx context.Context, resource bool, wantErr error) observation {
				t.Helper()
				raw := &fakePortService{err: wantErr}
				var options []vvotel.ServiceOption
				if resource {
					options = append(options, vvotel.WithServiceResource("fault_test"))
				}
				result, err := vvotel.Service[dummyModel, string, dummyModel](tel, options...)(raw).Get(ctx, port.GetCommand[string]{ID: "business"})
				if err == nil && result.ID != fakeGetResultID {
					t.Fatalf("service result changed: %+v", result)
				}
				return observation{ctx: raw.lastCtx, calls: raw.calls, operationCalls: raw.operationCalls[vvotel.OpCommandGet], err: err}
			},
		},
		{
			name:      "storage",
			histogram: true,
			exercise: func(t *testing.T, tel *vvotel.Telemetry, ctx context.Context, resource bool, wantErr error) observation {
				t.Helper()
				raw := &fakeStorageStore{err: wantErr}
				var options []vvotel.StorageOption
				if resource {
					options = append(options, vvotel.WithStorageResource("fault_test"))
				}
				key, err := storage.ParseKey("fault/item")
				if err != nil {
					t.Fatal(err)
				}
				result, err := vvotel.Store(tel, options...)(raw).Head(ctx, key)
				if err == nil && result.Size != 10 {
					t.Fatalf("storage result changed: %+v", result)
				}
				return observation{ctx: raw.lastCtx, calls: raw.calls, operationCalls: raw.operationCalls[vvotel.OpStorageHead], err: err}
			},
		},
	}

	for _, adapter := range adapters {
		for _, fault := range faults {
			if fault.serviceOnly && !adapter.histogram {
				continue
			}
			t.Run(adapter.name+"/"+fault.name, func(t *testing.T) {
				tp := newTestTracerProvider()
				mp := newTestMeterProvider()
				tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
				if err != nil {
					t.Fatal(err)
				}
				fault.configure(tp, mp)
				incoming := context.WithValue(context.Background(), failsafeContextKey("incoming"), "preserved")
				var businessErr error
				if fault.businessError {
					businessErr = errors.New("business error")
				}
				got := adapter.exercise(t, tel, incoming, fault.resource, businessErr)
				if got.err != businessErr {
					t.Fatalf("business error identity changed: got %v, want %v", got.err, businessErr)
				}
				if got.calls != 1 || got.operationCalls != 1 {
					t.Fatalf("business calls=%d operation_calls=%d, want one each", got.calls, got.operationCalls)
				}
				if got.ctx == nil || got.ctx.Value(failsafeContextKey("incoming")) != "preserved" {
					t.Fatal("business context was lost")
				}
				if fault.fallbackContext != (got.ctx == incoming) {
					t.Fatalf("fallback context=%t, want %t", got.ctx == incoming, fault.fallbackContext)
				}
				if tp.tracerCalls != 1 || tp.startCalls != 1 {
					t.Fatalf("provider tracer calls=%d start calls=%d, want one assembly call and one operation call", tp.tracerCalls, tp.startCalls)
				}
				if len(tp.spans) != fault.wantSpans || tp.attributeCalls != fault.wantAttributes || tp.statusCalls != fault.wantStatus || tp.endCalls != fault.wantEnds {
					t.Fatalf("span calls: spans=%d attributes=%d status=%d end=%d", len(tp.spans), tp.attributeCalls, tp.statusCalls, tp.endCalls)
				}
				if fault.wantSpans == 1 && tp.spans[0].ended != fault.wantRecordedEnd {
					t.Fatalf("recorded span ended=%t, want %t", tp.spans[0].ended, fault.wantRecordedEnd)
				}
				wantRecordCalls := 0
				wantMetrics := 0
				if adapter.histogram {
					wantRecordCalls = 1
					wantMetrics = fault.wantServiceMetrics
				}
				if mp.histogramRecordCalls != wantRecordCalls || mp.metricCount() != wantMetrics {
					t.Fatalf("histogram record calls=%d metrics=%d, want %d/%d", mp.histogramRecordCalls, mp.metricCount(), wantRecordCalls, wantMetrics)
				}
				if adapter.histogram && wantMetrics == 1 && mp.metrics[0].context != got.ctx {
					t.Fatal("histogram did not receive the exact business context")
				}
			})
		}
	}
}

func TestService_ProviderPanicsDoNotReplaceBusinessError(t *testing.T) {
	tp := newTestTracerProvider()
	tp.panicStatus = true
	tp.panicEnd = true
	mp := newTestMeterProvider()
	mp.panicHistogramRecord = true
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("business error")
	raw := &fakePortService{err: want}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)
	_, got := svc.Get(context.Background(), port.GetCommand[string]{ID: "business"})
	if !errors.Is(got, want) {
		t.Fatalf("got error %v, want %v", got, want)
	}
}

func TestCacheAndCacheMemory_FaultsKeepSignalsIndependent(t *testing.T) {
	adapters := []struct {
		name    string
		observe func(*vvotel.Telemetry, context.Context)
	}{
		{
			name: "cache",
			observe: func(tel *vvotel.Telemetry, ctx context.Context) {
				vvotel.Cache(tel, vvotel.WithCacheSpanEvents(true)).Observe(ctx, cache.Event{Operation: cache.LookupOperation, Outcome: cache.HitOutcome})
			},
		},
		{
			name: "cache_memory",
			observe: func(tel *vvotel.Telemetry, ctx context.Context) {
				vvotel.CacheMemory(tel, vvotel.WithCacheMemorySpanEvents(true)).Observe(ctx, cachememory.Event{Operation: cachememory.GetOperation, Outcome: cachememory.HitOutcome})
			},
		},
	}
	faults := []struct {
		name            string
		configure       func(*testTracerProvider, *testMeterProvider)
		wantIsRecording int
		wantAddEvent    int
		wantEvents      int
		wantMetrics     int
	}{
		{
			name: "span_is_recording_panics",
			configure: func(tp *testTracerProvider, _ *testMeterProvider) {
				tp.panicIsRecording = true
			},
			wantIsRecording: 1,
			wantMetrics:     1,
		},
		{
			name: "span_add_event_panics",
			configure: func(tp *testTracerProvider, _ *testMeterProvider) {
				tp.panicAddEvent = true
			},
			wantIsRecording: 1,
			wantAddEvent:    1,
			wantMetrics:     1,
		},
		{
			name: "counter_add_panics",
			configure: func(_ *testTracerProvider, mp *testMeterProvider) {
				mp.panicCounterAdd = true
			},
			wantIsRecording: 1,
			wantAddEvent:    1,
			wantEvents:      1,
		},
	}

	for _, adapter := range adapters {
		for _, fault := range faults {
			t.Run(adapter.name+"/"+fault.name, func(t *testing.T) {
				tp := newTestTracerProvider()
				mp := newTestMeterProvider()
				tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
				if err != nil {
					t.Fatal(err)
				}
				ctx, span := tp.Tracer("test").Start(context.Background(), "parent")
				fault.configure(tp, mp)
				adapter.observe(tel, ctx)
				if tp.isRecordingCalls != fault.wantIsRecording || tp.eventCalls() != fault.wantAddEvent || len(tp.spans[0].events) != fault.wantEvents {
					t.Fatalf("span calls: is_recording=%d add_event=%d recorded_events=%d", tp.isRecordingCalls, tp.eventCalls(), len(tp.spans[0].events))
				}
				if mp.counterAdds() != 1 || mp.metricCount() != fault.wantMetrics {
					t.Fatalf("counter calls=%d recorded_metrics=%d, want 1/%d", mp.counterAdds(), mp.metricCount(), fault.wantMetrics)
				}
				span.End()
			})
		}
	}
}

func TestTelemetry_DisabledObserversDoNotTouchRecordingSpan(t *testing.T) {
	tp := newTestTracerProvider()
	parentCtx, span := tp.Tracer("test").Start(context.Background(), "parent")
	tel, err := vvotel.New(vvotel.Config{Disabled: true, TracerProvider: tp})
	if err != nil {
		t.Fatal(err)
	}
	vvotel.Cache(tel, vvotel.WithCacheSpanEvents(true)).Observe(parentCtx, cache.Event{Operation: cache.LookupOperation, Outcome: cache.HitOutcome})
	vvotel.CacheMemory(tel, vvotel.WithCacheMemorySpanEvents(true)).Observe(parentCtx, cachememory.Event{Operation: cachememory.GetOperation, Outcome: cachememory.HitOutcome})
	if tp.eventCalls() != 0 {
		t.Fatalf("disabled observers added %d events", tp.eventCalls())
	}
	span.End()
}

func TestService_BusinessPanicIsPreservedWhenTelemetryPanics(t *testing.T) {
	tp := newTestTracerProvider()
	tp.panicStatus = true
	tp.panicEnd = true
	mp := newTestMeterProvider()
	mp.panicHistogramRecord = true
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	want := &fakeBusinessPanic{operation: "service telemetry fault"}
	raw := &fakePortService{panicValue: want}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("business panic identity changed: got %v, want %p", got, want)
		}
		if raw.calls != 1 || raw.operationCalls[vvotel.OpCommandGet] != 1 {
			t.Fatalf("business calls=%d by_operation=%v, want Get once", raw.calls, raw.operationCalls)
		}
	}()
	_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "business"})
}
