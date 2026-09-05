package vvotel_test

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/port"
)

func TestSchema_CacheMappingsAreTotal(t *testing.T) {
	cacheOperations := []string{
		"lookup", "lookup_many", "load", "load_many", "put", "forget",
	}
	for _, value := range cacheOperations {
		if mapped, ok := vvotel.CacheOperationName(value); !ok || mapped != value {
			t.Errorf("cache operation %q mapped to %q, %v", value, mapped, ok)
		}
	}
	cacheOutcomes := []string{
		"hit", "miss", "negative", "stale", "loaded", "stored", "deleted",
		"superseded", "complete", "error",
	}
	for _, value := range cacheOutcomes {
		if mapped, ok := vvotel.CacheOutcomeName(value); !ok || mapped != value {
			t.Errorf("cache outcome %q mapped to %q, %v", value, mapped, ok)
		}
	}

	backendOperations := []string{
		"get", "get_many", "put", "delete", "evict", "reset", "close",
	}
	for _, value := range backendOperations {
		if mapped, ok := vvotel.CacheBackendOperationName(value); !ok || mapped != value {
			t.Errorf("backend operation %q mapped to %q, %v", value, mapped, ok)
		}
	}
	backendOutcomes := []string{
		"hit", "miss", "stored", "replaced", "deleted", "evicted", "rejected", "complete",
	}
	for _, value := range backendOutcomes {
		if mapped, ok := vvotel.CacheBackendOutcomeName(value); !ok || mapped != value {
			t.Errorf("backend outcome %q mapped to %q, %v", value, mapped, ok)
		}
	}
}

func TestSchema_UnknownValuesAreDropped(t *testing.T) {
	checks := []func(string) (string, bool){
		vvotel.CacheOperationName,
		vvotel.CacheOutcomeName,
		vvotel.CacheBackendOperationName,
		vvotel.CacheBackendOutcomeName,
		vvotel.AllowedErrorCode,
	}
	for _, check := range checks {
		if mapped, ok := check("tenant-secret-or-id"); ok || mapped != "" {
			t.Fatalf("unknown value mapped to %q, %v", mapped, ok)
		}
	}
}

func TestSchema_StandardErrorCodesAreAllowed(t *testing.T) {
	for _, code := range []string{
		"unique", "not_unique", "foreign_key", "restrict", "required", "check",
		"exclusion", "too_long", "out_of_range", "invalid_format", "invalid_enum",
		"stale_version", "malformed_body", "invalid_id", "unknown_field", "bad_query",
		"too_large", "conflict", "not_found", "forbidden", "method_not_allowed",
		"unauthenticated", "deadlock", "serialization_failure", "lock_timeout",
		"transaction_aborted", "unavailable", "schema_not_ready", "internal",
	} {
		if mapped, ok := vvotel.AllowedErrorCode(code); !ok || mapped != code {
			t.Errorf("error code %q mapped to %q, %v", code, mapped, ok)
		}
	}
}

func TestSchema_RuntimeMetadataIsGenerated(t *testing.T) {
	attributeMetadata, ok := vvotel.AttributeMetadataByKey["resource_name"]
	if !ok || attributeMetadata.MetricEligible {
		t.Fatalf("resource metadata missing or metric-eligible: %+v", attributeMetadata)
	}
	metricMetadata, ok := vvotel.MetricMetadataByKey["command_duration"]
	if !ok || metricMetadata.Unit != "s" || metricMetadata.Type != "histogram" {
		t.Fatalf("command duration metadata missing or incomplete: %+v", metricMetadata)
	}
	if vvotel.MigrationStatus == "" || vvotel.MigrationSince == "" || vvotel.MigrationPolicy == "" {
		t.Fatal("migration metadata was not generated")
	}
}

func TestSchema_UnknownCacheValuesAreDroppedEndToEnd(t *testing.T) {
	tp := newTestTracerProvider()
	parentCtx, span := tp.Tracer("test").Start(context.Background(), "parent")
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	vvotel.Cache(tel, vvotel.WithCacheSpanEvents(true)).Observe(parentCtx, cache.Event{Operation: cache.Operation("tenant-secret"), Outcome: cache.HitOutcome})
	vvotel.CacheMemory(tel, vvotel.WithCacheMemorySpanEvents(true)).Observe(parentCtx, cachememory.Event{Operation: cachememory.GetOperation, Outcome: cachememory.Outcome("tenant-secret")})
	if tp.eventCalls() != 0 || mp.counterAdds() != 0 {
		t.Fatalf("unknown cache value reached telemetry: events=%d adds=%d", tp.eventCalls(), mp.counterAdds())
	}
	span.End()
}

func TestSchema_UnknownErrorCodeIsDroppedEndToEnd(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	want := &errs.Fault{Kind: errs.KindInternal, Code: errs.Code("tenant-secret-or-id")}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(&fakePortService{err: want})
	_, got := svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
	if got != want {
		t.Fatalf("business error identity changed: got %p, want %p", got, want)
	}
	if len(tp.spans) != 1 {
		t.Fatalf("expected one span, got %d", len(tp.spans))
	}
	if _, ok := tp.spans[0].attributes[vvotel.AttrErrorCode]; ok {
		t.Fatal("unknown error code was exported")
	}
	if mp.hasMetricAttribute(vvotel.AttrErrorCode) {
		t.Fatal("error code was exported as a metric attribute")
	}
}
