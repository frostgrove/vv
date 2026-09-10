package vvotel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/port"
)

func TestTelemetry_ConfigurationAndEagerInstruments(t *testing.T) {
	_, err := vvotel.New(vvotel.Config{})
	if !errors.Is(err, vvotel.ErrNilProvider) {
		t.Fatalf("expected ErrNilProvider, got %v", err)
	}

	disabledTel, err := vvotel.New(vvotel.Config{
		Disabled: true,
	})
	if err != nil {
		t.Fatalf("unexpected error for disabled config: %v", err)
	}
	if disabledTel == nil {
		t.Fatal("expected disabled telemetry handle")
	}

	tp := newTestTracerProvider()
	mp := newTestMeterProvider()

	_, err = vvotel.New(vvotel.Config{
		TracerProvider:         tp,
		MeterProvider:          mp,
		ResourceName:           "inventory",
		CommandTracesDisabled:  true,
		StorageTracesDisabled:  false,
		CommandMetricsDisabled: false,
		CacheMetricsDisabled:   false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mp.meters != 1 || mp.histogramCreations != 14 || mp.counterCreations != 12 {
		t.Fatalf("unexpected eager assembly: meters=%d float64_histograms=%d int64_counters=%d", mp.meters, mp.histogramCreations, mp.counterCreations)
	}
}

func TestTelemetry_ResourceNameIsBoundedAndTraceOnly(t *testing.T) {
	tp := newTestTracerProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, ResourceName: "bad resource with PII"})
	if err != nil {
		t.Fatal(err)
	}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(&fakePortService{})
	_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
	if _, ok := tp.spans[0].attributes[vvotel.AttrResourceName]; ok {
		t.Fatal("invalid resource name was exported")
	}

	tp = newTestTracerProvider()
	tel, err = vvotel.New(vvotel.Config{TracerProvider: tp})
	if err != nil {
		t.Fatal(err)
	}
	tooLong := "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnop"
	svc = vvotel.Service[dummyModel, string, dummyModel](tel, vvotel.WithServiceResource(vvotel.ApprovedName(tooLong)))(&fakePortService{})
	_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
	if _, ok := tp.spans[0].attributes[vvotel.AttrResourceName]; ok {
		t.Fatal("oversized resource name was exported")
	}
}

func TestTelemetry_AdaptersNeverReconstructInstruments(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(&fakePortService{})
	_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
	_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
	if mp.histogramCreations != 14 || mp.counterCreations != 12 {
		t.Fatalf("adapter reconstructed instruments: histograms=%d counters=%d", mp.histogramCreations, mp.counterCreations)
	}
	vvotel.Cache(tel).Observe(context.Background(), cache.Event{Operation: cache.LookupOperation, Outcome: cache.HitOutcome})
	if mp.histogramCreations != 14 || mp.counterCreations != 12 {
		t.Fatalf("observer reconstructed instruments: histograms=%d counters=%d", mp.histogramCreations, mp.counterCreations)
	}
}

func TestTelemetry_InstrumentCreationPanicFailsAssembly(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	mp.panicHistogramCreate = true
	mp.panicCounterCreate = true
	_, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if !errors.Is(err, vvotel.ErrAssembly) || !errors.Is(err, vvotel.ErrInstrument) || !errors.Is(err, vvotel.ErrProviderPanic) {
		t.Fatalf("constructor panic was not classified as an assembly instrument failure: %v", err)
	}
}
