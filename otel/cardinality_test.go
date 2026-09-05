package vvotel_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/port"
)

func TestCardinalityAndPrivacy_CanaryNeverEmitted(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()

	tel, err := vvotel.New(vvotel.Config{
		TracerProvider: tp,
		MeterProvider:  mp,
		ResourceName:   "safe_resource",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	canaryPII := "user_secret_password_12345"
	canarySQL := "SELECT * FROM users WHERE ssn = '123-45-6789'"

	raw := &fakePortService{
		err: fmt.Errorf("custom error containing %s and %s", canaryPII, canarySQL),
	}

	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(raw)
	_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: canaryPII})

	if len(tp.spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(tp.spans))
	}

	for k, v := range tp.spans[0].attributes {
		valStr := v.AsString()
		if valStr == canaryPII || valStr == canarySQL {
			t.Fatalf("canary leaked into span attribute %s: %s", k, valStr)
		}
	}

	if len(mp.metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(mp.metrics))
	}

	for k, v := range mp.metrics[0].attributes {
		valStr := v.AsString()
		if valStr == canaryPII || valStr == canarySQL {
			t.Fatalf("canary leaked into metric attribute %s: %s", k, valStr)
		}
	}
}

func TestCardinality_CacheMetricSeriesStayWithinRegistryBound(t *testing.T) {
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	cacheObserver := vvotel.Cache(tel)
	for _, operation := range []string{"lookup", "lookup_many", "load", "load_many", "put", "forget"} {
		for _, outcome := range []string{"hit", "miss", "negative", "stale", "loaded", "stored", "deleted", "superseded", "complete", "error"} {
			cacheObserver.Observe(context.Background(), cache.Event{Operation: cache.Operation(operation), Outcome: cache.Outcome(outcome)})
		}
	}
	backendObserver := vvotel.CacheMemory(tel)
	for _, operation := range []string{"get", "get_many", "put", "delete", "evict", "reset", "close"} {
		for _, outcome := range []string{"hit", "miss", "stored", "replaced", "deleted", "evicted", "rejected", "complete"} {
			backendObserver.Observe(context.Background(), cachememory.Event{Operation: cachememory.Operation(operation), Outcome: cachememory.Outcome(outcome)})
		}
	}
	if got := mp.metricCount(); got != 116 {
		t.Fatalf("got %d cache metric observations, want the 116-series registry bound", got)
	}
}

func TestCardinality_ResourceNamesStayWithinRegistryBound(t *testing.T) {
	tp := newTestTracerProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < vvotel.MaxResourceNameValues+8; i++ {
		name := fmt.Sprintf("resource_%d", i)
		svc := vvotel.Service[dummyModel, string, dummyModel](tel, vvotel.WithServiceResource(name))(&fakePortService{})
		_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
	}
	withResource := 0
	for _, span := range tp.spans {
		if _, ok := span.attributes[vvotel.AttrResourceName]; ok {
			withResource++
		}
	}
	if withResource != vvotel.MaxResourceNameValues {
		t.Fatalf("got %d resource names, want bound %d", withResource, vvotel.MaxResourceNameValues)
	}
}
