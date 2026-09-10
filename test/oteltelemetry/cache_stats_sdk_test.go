package oteltelemetry_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestRealSDKCacheMemoryStatsCollectAndUnregisterRace(t *testing.T) {
	fixture := newSDKFixture(t, sdktrace.NeverSample())
	tel := vvotel.Must(vvotel.Config{MeterProvider: fixture.meterProvider})
	active := cacheStatsSDKBackend(t, cachememory.Limits{MaxEntries: 3, MaxBytes: 4096, MaxItemBytes: 64})
	closed := cacheStatsSDKBackend(t, cachememory.Limits{MaxEntries: 7, MaxBytes: 8192, MaxItemBytes: 64})
	var address cache.Address
	address.KeyDigest[0] = 1
	if err := active.Put(context.Background(), address, []byte("cache-stats-private-payload"), cache.Expiry{Mode: cache.CapacityOnlyExpiry}); err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	registration := vvotel.MustCacheMemoryStats(tel, active, closed)

	data := collectMetricData(t, fixture.metrics)
	assertCacheStatsSDKGauge(t, data, vvotel.MetricCacheMemoryEntries, 1)
	assertCacheStatsSDKGauge(t, data, vvotel.MetricCacheMemoryBytes, cachememory.FixedEntryChargeBytes+int64(len("cache-stats-private-payload")))
	assertCacheStatsSDKGauge(t, data, vvotel.MetricCacheMemoryEntryLimit, 3)
	assertCacheStatsSDKGauge(t, data, vvotel.MetricCacheMemoryByteLimit, 4096)
	assertCacheStatsSDKGauge(t, data, vvotel.MetricCacheMemoryActive, 1)
	assertCacheStatsSDKGauge(t, data, vvotel.MetricCacheMemoryClosed, 1)

	start := make(chan struct{})
	results := make(chan error, 24)
	var wait sync.WaitGroup
	for index := range 24 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			ctx := context.Background()
			cancel := func() {}
			switch index % 3 {
			case 1:
				var cancelContext context.CancelFunc
				ctx, cancelContext = context.WithCancel(ctx)
				cancelContext()
			case 2:
				var cancelContext context.CancelFunc
				ctx, cancelContext = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				cancel = cancelContext
			}
			defer cancel()
			var collected metricdata.ResourceMetrics
			results <- fixture.metrics.Collect(ctx, &collected)
		}()
	}
	close(start)
	unregisterResult := make(chan error, 1)
	go func() { unregisterResult <- registration.Unregister() }()
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("concurrent Collect: %v", err)
		}
	}
	if err := <-unregisterResult; err != nil {
		t.Fatal(err)
	}
	if err := registration.Unregister(); err != nil {
		t.Fatal(err)
	}

	var after metricdata.ResourceMetrics
	if err := fixture.metrics.Collect(context.Background(), &after); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		vvotel.MetricCacheMemoryEntries,
		vvotel.MetricCacheMemoryBytes,
		vvotel.MetricCacheMemoryEntryLimit,
		vvotel.MetricCacheMemoryByteLimit,
		vvotel.MetricCacheMemoryActive,
		vvotel.MetricCacheMemoryClosed,
	} {
		if cacheStatsSDKMetricPresent(after, name) {
			t.Fatalf("metric %q remained after unregister", name)
		}
	}

	replacement, err := vvotel.CacheMemoryStats(tel, active)
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Unregister(); err != nil {
		t.Fatal(err)
	}
}

func cacheStatsSDKBackend(t *testing.T, limits cachememory.Limits) *cachememory.Backend {
	t.Helper()
	backend, err := cachememory.New(limits)
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func assertCacheStatsSDKGauge(t *testing.T, data metricdata.ResourceMetrics, name string, want int64) {
	t.Helper()
	metric := findMetricData(t, data, name)
	gauge, ok := metric.Data.(metricdata.Gauge[int64])
	if !ok || len(gauge.DataPoints) != 1 || gauge.DataPoints[0].Value != want {
		t.Fatalf("gauge %q = %#v (%T), want %d", name, metric.Data, metric.Data, want)
	}
	attributes := attributesByName(gauge.DataPoints[0].Attributes.ToSlice())
	if attributes[string(vvotel.AttrComponent)] != vvotel.ComponentCacheMemory || len(attributes) != 1 {
		t.Fatalf("gauge %q attributes = %#v", name, attributes)
	}
}

func cacheStatsSDKMetricPresent(data metricdata.ResourceMetrics, name string) bool {
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == name {
				return true
			}
		}
	}
	return false
}
