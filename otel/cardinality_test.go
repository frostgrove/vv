package vvotel_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/storage"
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
	series := map[string]map[string]bool{}
	for _, observation := range mp.metrics {
		parts := make([]string, 0, len(observation.attributes))
		for key, value := range observation.attributes {
			parts = append(parts, fmt.Sprintf("%s=%s", key, value.AsString()))
		}
		sort.Strings(parts)
		if series[observation.name] == nil {
			series[observation.name] = map[string]bool{}
		}
		series[observation.name][strings.Join(parts, ";")] = true
	}
	bounds := map[string]int{}
	for _, descriptor := range vvotel.SignalDescriptors() {
		if descriptor.Component == "cache" || descriptor.Component == "cache_backend" {
			bounds[descriptor.Name] = descriptor.CardinalityBound
		}
	}
	for name, values := range series {
		if len(values) == 0 || bounds[name] == 0 || len(values) > bounds[name] {
			t.Fatalf("cache metric %q produced %d unique attribute sets against bound %d", name, len(values), bounds[name])
		}
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
		svc := vvotel.Service[dummyModel, string, dummyModel](tel, vvotel.WithServiceResource(vvotel.ApprovedName(name)))(&fakePortService{})
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

func TestCardinality_ApprovedNamesShareOneConcurrentTelemetryBudget(t *testing.T) {
	tp := newTestTracerProvider()
	tel, err := vvotel.New(vvotel.Config{
		TracerProvider: tp,
		ResourceName:   "configured_default",
	})
	if err != nil {
		t.Fatal(err)
	}
	key, err := storage.ParseKey("object")
	if err != nil {
		t.Fatal(err)
	}

	service := vvotel.Service[dummyModel, string, dummyModel](tel, vvotel.WithServiceResource("service_first"))(&fakePortService{})
	_, _ = service.Get(context.Background(), port.GetCommand[string]{ID: "id"})
	store := vvotel.Store(tel, vvotel.WithStorageResource("storage_first"))(&fakeStorageStore{})
	_, _ = store.Head(context.Background(), key)
	sharedService := vvotel.Service[dummyModel, string, dummyModel](tel, vvotel.WithServiceResource("shared_name"))(&fakePortService{})
	_, _ = sharedService.Get(context.Background(), port.GetCommand[string]{ID: "id"})
	sharedStore := vvotel.Store(tel, vvotel.WithStorageResource("shared_name"))(&fakeStorageStore{})
	_, _ = sharedStore.Head(context.Background(), key)

	var group sync.WaitGroup
	for i := 2; i < vvotel.MaxResourceNameValues+32; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			name := vvotel.MustApproveName(fmt.Sprintf("resource_%d", index))
			if index%2 == 0 {
				wrapped := vvotel.Service[dummyModel, string, dummyModel](tel, vvotel.WithServiceResource(name))(&fakePortService{})
				_, _ = wrapped.Get(context.Background(), port.GetCommand[string]{ID: "id"})
				return
			}
			wrapped := vvotel.Store(tel, vvotel.WithStorageResource(name))(&fakeStorageStore{})
			_, _ = wrapped.Head(context.Background(), key)
		}(i)
	}
	group.Wait()

	defaultService := vvotel.Service[dummyModel, string, dummyModel](tel)(&fakePortService{})
	_, _ = defaultService.Get(context.Background(), port.GetCommand[string]{ID: "id"})

	seen := make(map[string]struct{})
	withResource := 0
	for _, span := range tp.spans {
		value, ok := span.attributes[vvotel.AttrResourceName]
		if !ok {
			continue
		}
		withResource++
		seen[value.AsString()] = struct{}{}
	}
	if withResource != vvotel.MaxResourceNameValues+1 || len(seen) != vvotel.MaxResourceNameValues {
		t.Fatalf("admitted points=%d distinct=%d, want %d/%d", withResource, len(seen), vvotel.MaxResourceNameValues+1, vvotel.MaxResourceNameValues)
	}
	if _, ok := seen["service_first"]; !ok {
		t.Fatal("service declaration did not enter the shared budget")
	}
	if _, ok := seen["storage_first"]; !ok {
		t.Fatal("storage declaration did not enter the shared budget")
	}
	if _, ok := seen["shared_name"]; !ok {
		t.Fatal("duplicate service/storage declaration did not share one budget entry")
	}
	last := tp.spans[len(tp.spans)-1]
	if _, ok := last.attributes[vvotel.AttrResourceName]; ok {
		t.Fatal("configured default was pre-reserved or escaped the exhausted shared budget")
	}
}

func TestCardinality_TelemetryValueCopiesShareOneConcurrentApprovedNameBudget(t *testing.T) {
	tp := newTestTracerProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp})
	if err != nil {
		t.Fatal(err)
	}
	copyOfTelemetry := *tel

	var group sync.WaitGroup
	for i := 0; i < vvotel.MaxResourceNameValues*4; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			selected := tel
			if index%2 != 0 {
				selected = &copyOfTelemetry
			}
			name := vvotel.MustApproveName(fmt.Sprintf("copied_resource_%d", index))
			wrapped := vvotel.Service[dummyModel, string, dummyModel](selected, vvotel.WithServiceResource(name))(&fakePortService{})
			_, _ = wrapped.Get(context.Background(), port.GetCommand[string]{ID: "id"})
		}(i)
	}
	group.Wait()

	seen := make(map[string]struct{})
	for _, span := range tp.spans {
		if value, ok := span.attributes[vvotel.AttrResourceName]; ok {
			seen[value.AsString()] = struct{}{}
		}
	}
	if len(seen) != vvotel.MaxResourceNameValues {
		t.Fatalf("copied telemetry admitted %d distinct names, want shared bound %d", len(seen), vvotel.MaxResourceNameValues)
	}
}
