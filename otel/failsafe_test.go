package vvotel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/port"
)

func TestTelemetry_DisabledDoesNotCallProviders(t *testing.T) {
	tp := newTestTracerProvider()
	tp.panicTracer = true
	mp := newTestMeterProvider()
	mp.panicMeter = true
	if _, err := vvotel.New(vvotel.Config{Disabled: true, TracerProvider: tp, MeterProvider: mp}); err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if tp.spans != nil || mp.metrics != nil || mp.meters != 0 {
		t.Fatal("disabled telemetry touched providers")
	}
}

func TestTelemetry_TypedNilProviderIsRejected(t *testing.T) {
	var tp *testTracerProvider
	if _, err := vvotel.New(vvotel.Config{TracerProvider: tp}); !errors.Is(err, vvotel.ErrNilProvider) {
		t.Fatalf("expected ErrNilProvider, got %v", err)
	}
}

func TestService_ProviderPanicsPreserveBusinessCall(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*testTracerProvider, *testMeterProvider)
		resource  bool
	}{
		{name: "start", configure: func(tp *testTracerProvider, _ *testMeterProvider) { tp.panicStart = true }},
		{name: "setup_attributes", configure: func(tp *testTracerProvider, _ *testMeterProvider) { tp.panicAttributes = true }, resource: true},
		{name: "final_attributes", configure: func(tp *testTracerProvider, _ *testMeterProvider) { tp.panicAttributes = true }},
		{name: "status", configure: func(tp *testTracerProvider, _ *testMeterProvider) { tp.panicStatus = true }},
		{name: "end", configure: func(tp *testTracerProvider, _ *testMeterProvider) { tp.panicEnd = true }},
		{name: "record", configure: func(_ *testTracerProvider, mp *testMeterProvider) { mp.panicHistogramRecord = true }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestTracerProvider()
			mp := newTestMeterProvider()
			tc.configure(tp, mp)
			tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
			if err != nil {
				t.Fatal(err)
			}
			raw := &fakePortService{}
			if tc.name == "status" {
				raw.err = errors.New("business error")
			}
			options := []vvotel.ServiceOption(nil)
			if tc.resource {
				options = append(options, vvotel.WithServiceResource("resource"))
			}
			svc := vvotel.Service[dummyModel, string, dummyModel](tel, options...)(raw)
			result, callErr := svc.Get(context.Background(), port.GetCommand[string]{ID: "business"})
			if tc.name == "status" {
				if !errors.Is(callErr, raw.err) {
					t.Fatalf("business error changed: %v", callErr)
				}
			} else if callErr != nil || result.ID != "business" {
				t.Fatalf("business call changed: result=%+v err=%v", result, callErr)
			}
			switch tc.name {
			case "start":
				if len(tp.spans) != 0 {
					t.Fatalf("start panic unexpectedly recorded a span")
				}
			case "setup_attributes", "final_attributes":
				if tp.attributeCalls != 1 {
					t.Fatalf("expected one SetAttributes call, got %d", tp.attributeCalls)
				}
			case "status":
				if tp.statusCalls != 1 {
					t.Fatalf("expected one SetStatus call, got %d", tp.statusCalls)
				}
			case "end":
				if tp.endCalls != 1 {
					t.Fatalf("expected one End call, got %d", tp.endCalls)
				}
			case "record":
				if mp.histogramRecordCalls != 1 {
					t.Fatalf("expected one Histogram.Record call, got %d", mp.histogramRecordCalls)
				}
			}
		})
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

func TestCache_ProviderPanicsAreIsolated(t *testing.T) {
	tp := newTestTracerProvider()
	tp.panicAddEvent = true
	mp := newTestMeterProvider()
	mp.panicCounterAdd = true
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	obs := vvotel.Cache(tel, vvotel.WithCacheSpanEvents(true))
	ctx, span := tp.Tracer("test").Start(context.Background(), "parent")
	obs.Observe(ctx, cache.Event{Operation: cache.LookupOperation, Outcome: cache.HitOutcome})
	if tp.eventCalls() != 1 {
		t.Fatalf("expected one isolated AddEvent call, got %d", tp.eventCalls())
	}
	if mp.counterAdds() != 1 {
		t.Fatalf("expected one isolated Counter.Add call, got %d", mp.counterAdds())
	}
	span.End()
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
	raw := &fakePortService{panic: true}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)
	defer func() {
		if got := recover(); got != "database crashed" {
			t.Fatalf("got panic %v, want database crashed", got)
		}
	}()
	_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "business"})
}
