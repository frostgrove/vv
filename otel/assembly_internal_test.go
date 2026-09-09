package vvotel

import (
	"reflect"
	"testing"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestAssembly_LegacyDisableSetsAreExact(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		want   Signals
	}{
		{name: "command traces", config: Config{CommandTracesDisabled: true}, want: Signals{SignalCommandSpan}},
		{name: "command metrics", config: Config{CommandMetricsDisabled: true}, want: Signals{SignalCommandDuration}},
		{name: "storage traces", config: Config{StorageTracesDisabled: true}, want: Signals{SignalStorageSpan, SignalStorageStreamSpan}},
		{name: "cache metrics", config: Config{CacheMetricsDisabled: true}, want: Signals{
			SignalCacheOperations,
			SignalCacheEvents,
			SignalCacheItems,
			SignalCacheEncodedBytes,
			SignalCachePayloadBytes,
			SignalCacheValueBytes,
			SignalCacheChargedBytes,
			SignalCacheMemoryEntries,
			SignalCacheMemoryBytes,
			SignalCacheMemoryEntryLimit,
			SignalCacheMemoryByteLimit,
			SignalCacheMemoryActive,
			SignalCacheMemoryClosed,
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			disabled := map[Signal]struct{}{}
			applyLegacyDisable(disabled, tc.config)
			got := make(Signals, 0, len(disabled))
			for _, signal := range AllSignals() {
				if _, ok := disabled[signal]; ok {
					got = append(got, signal)
				}
			}
			want := make(Signals, 0, len(tc.want))
			for _, signal := range AllSignals() {
				if tc.want.Has(signal) {
					want = append(want, signal)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("legacy disabled set=%v, want %v", got, want)
			}
		})
	}
}

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
