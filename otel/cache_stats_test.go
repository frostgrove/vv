package vvotel_test

import (
	"testing"

	"github.com/frostgrove/vv/cache/cachememory"
	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

func TestCacheMemoryStatsPublicRegistrationHidesNativeMarker(t *testing.T) {
	tel := vvotel.Must(vvotel.Config{MeterProvider: metricnoop.NewMeterProvider()})
	backend, err := cachememory.New(cachememory.Limits{MaxEntries: 1, MaxBytes: 1024, MaxItemBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	registration := vvotel.MustCacheMemoryStats(tel, backend)
	if _, exposed := registration.(metric.Registration); exposed {
		t.Fatal("framework registration exposes the native embedded-marker interface")
	}
	if err := registration.Unregister(); err != nil {
		t.Fatal(err)
	}
}
