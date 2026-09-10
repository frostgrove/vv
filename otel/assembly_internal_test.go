package vvotel

import (
	"testing"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestAssembly_ActiveSetAndInstrumentMapsArePrivateSnapshots(t *testing.T) {
	disable := Signals{SignalCommandSpan, SignalCacheOperations}
	tel, err := New(Config{
		TracerProvider: tracenoop.NewTracerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
		Disable:        disable,
	})
	if err != nil {
		t.Fatal(err)
	}
	disable[0] = SignalCommandDuration
	disable[1] = SignalStorageSpan
	if tel.signalEnabled(SignalCommandSpan) || tel.signalEnabled(SignalCacheOperations) {
		t.Fatal("disabled signal became active after caller mutation")
	}
	if !tel.signalEnabled(SignalCommandDuration) || !tel.signalEnabled(SignalStorageSpan) {
		t.Fatal("caller mutation changed the assembled active set")
	}
	if len(tel.float64Histograms) != 14 || len(tel.int64Histograms) != 13 || len(tel.int64Counters) != 11 || len(tel.int64ObservableGauge) != 6 {
		t.Fatalf("instrument maps: float hist=%d int hist=%d counters=%d gauges=%d", len(tel.float64Histograms), len(tel.int64Histograms), len(tel.int64Counters), len(tel.int64ObservableGauge))
	}
}
