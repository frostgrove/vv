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
		Cache:        "private-cache-name",
		Operation:    cache.LookupOperation,
		Outcome:      cache.HitOutcome,
		Items:        1,
		EncodedBytes: 64,
		PayloadBytes: 0,
		Memoized:     true,
	})
	span.End()

	if len(mp.metrics) != 5 {
		t.Fatalf("expected 5 metrics, got %d", len(mp.metrics))
	}
	wantMetrics := map[string]int64{
		vvotel.MetricCacheOperations:   1,
		vvotel.MetricCacheEvents:       1,
		vvotel.MetricCacheItems:        1,
		vvotel.MetricCacheEncodedBytes: 64,
		vvotel.MetricCachePayloadBytes: 0,
	}
	for _, measurement := range mp.metrics {
		value, ok := measurement.value.(int64)
		if !ok || wantMetrics[measurement.name] != value {
			t.Errorf("metric %q = %v", measurement.name, measurement.value)
		}
		delete(wantMetrics, measurement.name)
		if layer := measurement.attributes[vvotel.AttrCacheLayer].AsString(); layer != vvotel.CacheLayerFacade {
			t.Errorf("got layer %q, want %q", layer, vvotel.CacheLayerFacade)
		}
		if !measurement.attributes[vvotel.AttrMemoized].AsBool() {
			t.Error("memoized attribute was not recorded")
		}
		for _, value := range measurement.attributes {
			if value.AsString() == "private-cache-name" {
				t.Fatal("cache name leaked into metric attributes")
			}
		}
	}
	if len(wantMetrics) != 0 {
		t.Fatalf("missing metrics: %v", wantMetrics)
	}

	if len(tp.spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(tp.spans))
	}
	s := tp.spans[0]
	if len(s.events) != 1 || s.events[0] != vvotel.EventCache {
		t.Errorf("expected %q, got %v", vvotel.EventCache, s.events)
	}
	if !s.eventAttributes[0][vvotel.AttrMemoized].AsBool() {
		t.Fatal("span event did not carry memoized state")
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
		Operation:    cachememory.PutOperation,
		Outcome:      cachememory.RejectedOutcome,
		Reason:       cachememory.MaxBytesReason,
		Items:        1,
		ValueBytes:   5,
		ChargedBytes: cachememory.FixedEntryChargeBytes + 5,
	})
	span.End()

	if len(mp.metrics) != 5 {
		t.Fatalf("expected 5 metrics, got %d", len(mp.metrics))
	}
	wantMetrics := map[string]int64{
		vvotel.MetricCacheOperations:   1,
		vvotel.MetricCacheEvents:       1,
		vvotel.MetricCacheItems:        1,
		vvotel.MetricCacheValueBytes:   5,
		vvotel.MetricCacheChargedBytes: cachememory.FixedEntryChargeBytes + 5,
	}
	for _, measurement := range mp.metrics {
		value, ok := measurement.value.(int64)
		if !ok || wantMetrics[measurement.name] != value {
			t.Errorf("metric %q = %v", measurement.name, measurement.value)
		}
		delete(wantMetrics, measurement.name)
		if layer := measurement.attributes[vvotel.AttrCacheLayer].AsString(); layer != vvotel.CacheBackendLayerMemoryBackend {
			t.Errorf("got layer %q, want %q", layer, vvotel.CacheBackendLayerMemoryBackend)
		}
		if op := measurement.attributes[vvotel.AttrOperationName].AsString(); op != vvotel.OpCacheBackendPut {
			t.Errorf("got op %q, want %q", op, vvotel.OpCacheBackendPut)
		}
	}
	if len(wantMetrics) != 0 {
		t.Fatalf("missing metrics: %v", wantMetrics)
	}

	if len(tp.spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(tp.spans))
	}
	s := tp.spans[0]
	if len(s.events) != 1 || s.events[0] != vvotel.EventCacheBackend {
		t.Errorf("expected %q, got %v", vvotel.EventCacheBackend, s.events)
	}
	if reason := s.eventAttributes[0][vvotel.AttrReason].AsString(); reason != string(cachememory.MaxBytesReason) {
		t.Fatalf("span event reason = %q", reason)
	}
}

func TestCache_RichReasonsAndInvalidValuesAreBounded(t *testing.T) {
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	observer := vvotel.Cache(tel)
	events := []cache.Event{
		{Operation: cache.LookupOperation, Outcome: cache.ErrorOutcome, Reason: cache.BackendReason},
		{Operation: cache.LookupOperation, Outcome: cache.ErrorOutcome, Reason: cache.CorruptReason},
		{Operation: cache.LookupManyOperation, Outcome: cache.ErrorOutcome, Reason: cache.LimitReason},
		{Operation: cache.LookupOperation, Outcome: cache.ErrorOutcome, Reason: cache.RuntimeReason, Memoized: true},
	}
	for _, event := range events {
		before := len(mp.metrics)
		observer.Observe(context.Background(), event)
		if len(mp.metrics)-before != 3 {
			t.Fatalf("event %+v emitted %d metrics", event, len(mp.metrics)-before)
		}
		for _, measurement := range mp.metrics[before:] {
			if measurement.attributes[vvotel.AttrReason].AsString() != string(event.Reason) || measurement.attributes[vvotel.AttrMemoized].AsBool() != event.Memoized {
				t.Fatalf("event %+v attributes = %v", event, measurement.attributes)
			}
		}
	}

	before := len(mp.metrics)
	observer.Observe(context.Background(), cache.Event{Operation: cache.LookupManyOperation, Outcome: cache.CompleteOutcome, Items: -1, EncodedBytes: -1, PayloadBytes: -1})
	if len(mp.metrics)-before != 2 {
		t.Fatalf("negative measurements emitted %d metrics", len(mp.metrics)-before)
	}
	observer.Observe(context.Background(), cache.Event{Operation: cache.Operation("private"), Outcome: cache.HitOutcome})
	observer.Observe(context.Background(), cache.Event{Operation: cache.LookupOperation, Outcome: cache.HitOutcome, Reason: cache.Reason("private")})
	if len(mp.metrics)-before != 2 {
		t.Fatal("unknown cache vocabulary emitted telemetry")
	}
}

func TestCacheMemory_RichReasonsAndInvalidValuesAreBounded(t *testing.T) {
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	observer := vvotel.CacheMemory(tel)
	events := []cachememory.Event{
		{Operation: cachememory.GetOperation, Outcome: cachememory.MissOutcome, Reason: cachememory.ExpiredReason},
		{Operation: cachememory.GetOperation, Outcome: cachememory.RejectedOutcome, Reason: cachememory.ReadLimitReason, Items: 1, ValueBytes: 7},
		{Operation: cachememory.GetManyOperation, Outcome: cachememory.RejectedOutcome, Reason: cachememory.BatchItemLimitReason, Items: 2, ValueBytes: 7},
		{Operation: cachememory.GetManyOperation, Outcome: cachememory.RejectedOutcome, Reason: cachememory.BatchTotalLimitReason, Items: 2, ValueBytes: 7},
		{Operation: cachememory.EvictOperation, Outcome: cachememory.EvictedOutcome, Reason: cachememory.MaxEntriesReason, Items: 1, ValueBytes: 7, ChargedBytes: 359},
		{Operation: cachememory.EvictOperation, Outcome: cachememory.EvictedOutcome, Reason: cachememory.MaxBytesReason, Items: 1, ValueBytes: 7, ChargedBytes: 359},
		{Operation: cachememory.ResetOperation, Outcome: cachememory.CompleteOutcome, Reason: cachememory.ResetReason, Items: 1, ChargedBytes: 359},
		{Operation: cachememory.CloseOperation, Outcome: cachememory.CompleteOutcome, Reason: cachememory.CloseReason, Items: 1, ChargedBytes: 359},
	}
	for _, event := range events {
		before := len(mp.metrics)
		observer.Observe(context.Background(), event)
		if len(mp.metrics) == before {
			t.Fatalf("event %+v emitted no telemetry", event)
		}
		for _, measurement := range mp.metrics[before:] {
			if measurement.attributes[vvotel.AttrReason].AsString() != string(event.Reason) {
				t.Fatalf("event %+v attributes = %v", event, measurement.attributes)
			}
		}
	}

	before := len(mp.metrics)
	observer.Observe(context.Background(), cachememory.Event{Operation: cachememory.GetOperation, Outcome: cachememory.HitOutcome, Items: -1, ValueBytes: -1, ChargedBytes: -1})
	if len(mp.metrics)-before != 2 {
		t.Fatalf("negative measurements emitted %d metrics", len(mp.metrics)-before)
	}
	observer.Observe(context.Background(), cachememory.Event{Operation: cachememory.Operation("private"), Outcome: cachememory.HitOutcome})
	observer.Observe(context.Background(), cachememory.Event{Operation: cachememory.GetOperation, Outcome: cachememory.HitOutcome, Reason: cachememory.Reason("private")})
	if len(mp.metrics)-before != 2 {
		t.Fatal("unknown backend vocabulary emitted telemetry")
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
