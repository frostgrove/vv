package vvotel_test

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
	"github.com/frostgrove/vv/otel"
)

func TestCache_CounterRecordingAndSpanEvents(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()

	tel, err := vvotel.New(vvotel.Config{
		TracerProvider: tp,
		MeterProvider:  mp,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	obs := vvotel.Cache(tel, vvotel.WithCacheSpanEvents(true), nil)

	ctx, span := tp.Tracer("test").Start(context.Background(), "parent")
	obs.Observe(ctx, cache.Event{
		Operation: cache.LookupOperation,
		Outcome:   cache.HitOutcome,
	})
	span.End()

	if len(mp.metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(mp.metrics))
	}
	m := mp.metrics[0]
	if m.name != vvotel.MetricCacheOperations {
		t.Errorf("got metric %q, want %q", m.name, vvotel.MetricCacheOperations)
	}
	if val, ok := m.value.(int64); !ok || val != 1 {
		t.Errorf("got value %v, want 1", m.value)
	}
	if layer := m.attributes[vvotel.AttrCacheLayer].AsString(); layer != vvotel.CacheLayerFacade {
		t.Errorf("got layer %q, want %q", layer, vvotel.CacheLayerFacade)
	}

	if len(tp.spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(tp.spans))
	}
	s := tp.spans[0]
	if len(s.events) != 1 || s.events[0] != vvotel.EventCache {
		t.Errorf("expected %q, got %v", vvotel.EventCache, s.events)
	}
}

func TestCacheMemory_CounterRecordingAndSpanEvents(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, _ := vvotel.New(vvotel.Config{
		TracerProvider: tp,
		MeterProvider:  mp,
	})

	obs := vvotel.CacheMemory(tel, vvotel.WithCacheMemorySpanEvents(true), nil)

	ctx, span := tp.Tracer("test").Start(context.Background(), "parent")
	obs.Observe(ctx, cachememory.Event{
		Operation: cachememory.PutOperation,
		Outcome:   cachememory.StoredOutcome,
	})
	span.End()

	if len(mp.metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(mp.metrics))
	}
	m := mp.metrics[0]
	if layer := m.attributes[vvotel.AttrCacheLayer].AsString(); layer != vvotel.CacheBackendLayerMemoryBackend {
		t.Errorf("got layer %q, want %q", layer, vvotel.CacheBackendLayerMemoryBackend)
	}
	if op := m.attributes[vvotel.AttrOperationName].AsString(); op != vvotel.OpCacheBackendPut {
		t.Errorf("got op %q, want %q", op, vvotel.OpCacheBackendPut)
	}

	if len(tp.spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(tp.spans))
	}
	s := tp.spans[0]
	if len(s.events) != 1 || s.events[0] != vvotel.EventCacheBackend {
		t.Errorf("expected %q, got %v", vvotel.EventCacheBackend, s.events)
	}
}

func TestCache_NilTelemetrySafe(t *testing.T) {
	obs := vvotel.Cache(nil)
	obs.Observe(context.Background(), cache.Event{
		Operation: cache.LookupOperation,
		Outcome:   cache.HitOutcome,
	})

	obsMem := vvotel.CacheMemory(nil)
	obsMem.Observe(context.Background(), cachememory.Event{
		Operation: cachememory.GetOperation,
		Outcome:   cachememory.HitOutcome,
	})
}
