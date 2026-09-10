package vvotel_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/storage"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

type storageMetricRecord struct {
	name       string
	value      any
	context    context.Context
	attributes map[attribute.Key]attribute.Value
}

type storageMetricProvider struct {
	metricnoop.MeterProvider
	mu      sync.Mutex
	records []storageMetricRecord
}

func (p *storageMetricProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return &storageMetricMeter{provider: p}
}

func (p *storageMetricProvider) named(name string) []storageMetricRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	var found []storageMetricRecord
	for _, record := range p.records {
		if record.name == name {
			found = append(found, record)
		}
	}
	return found
}

func (p *storageMetricProvider) append(name string, value any, ctx context.Context, options []metric.RecordOption) {
	config := metric.NewRecordConfig(options)
	attributes := make(map[attribute.Key]attribute.Value)
	set := config.Attributes()
	for _, current := range set.ToSlice() {
		attributes[current.Key] = current.Value
	}
	p.mu.Lock()
	p.records = append(p.records, storageMetricRecord{
		name:       name,
		value:      value,
		context:    ctx,
		attributes: attributes,
	})
	p.mu.Unlock()
}

type storageMetricMeter struct {
	metricnoop.Meter
	provider *storageMetricProvider
}

func (m *storageMetricMeter) Float64Histogram(name string, _ ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	return &storageFloat64Histogram{name: name, provider: m.provider}, nil
}

func (m *storageMetricMeter) Int64Histogram(name string, _ ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	return &storageInt64Histogram{name: name, provider: m.provider}, nil
}

type storageFloat64Histogram struct {
	metricnoop.Float64Histogram
	name     string
	provider *storageMetricProvider
}

func (h *storageFloat64Histogram) Record(ctx context.Context, value float64, options ...metric.RecordOption) {
	h.provider.append(h.name, value, ctx, options)
}

type storageInt64Histogram struct {
	metricnoop.Int64Histogram
	name     string
	provider *storageMetricProvider
}

func (h *storageInt64Histogram) Record(ctx context.Context, value int64, options ...metric.RecordOption) {
	h.provider.append(h.name, value, ctx, options)
}

func TestStorageDurationRecordsAllNineOperations(t *testing.T) {
	mp := &storageMetricProvider{}
	tp := newTestTracerProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp, ResourceName: "assets"})
	if err != nil {
		t.Fatal(err)
	}
	raw := newExactFakeStorageStore(t)
	store := vvotel.Store(tel)(raw)
	fixture := newStorageEffectFixture(t)

	for index, operation := range fixture.operations() {
		result, callErr := fixture.execute(context.Background(), operation, store)
		if callErr != nil {
			t.Fatalf("%s: %v", operation, callErr)
		}
		assertStorageResultPreserved(t, result, storageExpectedResult(raw, operation), operation)
		fixture.assertCaptured(t, raw, operation)
		durations := mp.named(vvotel.MetricStorageDuration)
		if len(durations) != index+1 {
			t.Fatalf("%s duration records=%d, want %d", operation, len(durations), index+1)
		}
		record := durations[index]
		if record.context != raw.lastCtx {
			t.Fatalf("%s duration did not use the Store context", operation)
		}
		if value, ok := record.value.(float64); !ok || value < 0 {
			t.Fatalf("%s duration=%v", operation, record.value)
		}
		assertStorageOperationAttributes(t, record.attributes, operation, vvotel.OutcomeOk, "")
		switch operation {
		case vvotel.OpStoragePut, vvotel.OpStorageStage:
			persisted := mp.named(vvotel.MetricStorageOperationBytes)
			wantPersisted := 1
			if operation == vvotel.OpStorageStage {
				wantPersisted = 2
			}
			if len(persisted) != wantPersisted {
				t.Fatalf("%s persisted-size records=%d, want %d", operation, len(persisted), wantPersisted)
			}
			if persisted[len(persisted)-1].context != raw.lastCtx {
				t.Fatalf("%s persisted-size metric did not use the Store context", operation)
			}
		case vvotel.OpStorageCleanupExpired:
			cleanup := mp.named(vvotel.MetricStorageCleanupRemoved)
			if len(cleanup) != 1 || cleanup[0].context != raw.lastCtx {
				t.Fatal("cleanup-result metric did not use the Store context")
			}
		}
	}

	if raw.calls != 9 || len(raw.operationCalls) != 9 {
		t.Fatalf("Store calls=%d operations=%v", raw.calls, raw.operationCalls)
	}
	if len(tp.spans) != 9 || tp.endCalls != 9 {
		t.Fatalf("spans=%d end_calls=%d", len(tp.spans), tp.endCalls)
	}
	if raw.openReader.(*fakeStorageReadCloser).closeCalls != 0 {
		t.Fatal("Open duration waited for or closed the returned stream")
	}
}

func TestStorageDurationRecordsRepresentativeError(t *testing.T) {
	mp := &storageMetricProvider{}
	tel, err := vvotel.New(vvotel.Config{MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("backend unavailable")
	raw := newExactFakeStorageStore(t)
	raw.err = wantErr
	result, gotErr := vvotel.Store(tel)(raw).Head(context.Background(), mustStorageKey(t, "private/key"))
	if gotErr != wantErr || raw.calls != 1 {
		t.Fatalf("business effect changed: result=%+v err=%v calls=%d", result, gotErr, raw.calls)
	}
	assertStorageResultPreserved(t, result, raw.headResult, vvotel.OpStorageHead)
	durations := mp.named(vvotel.MetricStorageDuration)
	if len(durations) != 1 {
		t.Fatalf("duration records=%d", len(durations))
	}
	assertStorageOperationAttributes(t, durations[0].attributes, vvotel.OpStorageHead, vvotel.OutcomeError, vvotel.ErrorTypeInternal)
}

func TestStorageOperationBytesUsesSuccessfulReturnedSizesWithoutReadingSources(t *testing.T) {
	mp := &storageMetricProvider{}
	tel, err := vvotel.New(vvotel.Config{
		MeterProvider: mp,
		Disable:       vvotel.Signals{vvotel.SignalStorageDuration, vvotel.SignalStorageCleanupRemoved},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := newExactFakeStorageStore(t)
	store := vvotel.Store(tel)(raw)
	fixture := newStorageEffectFixture(t)

	put, putErr := store.Put(context.Background(), fixture.putKey, fixture.putSource, fixture.putOptions)
	if putErr != nil {
		t.Fatalf("Put result changed: result=%+v err=%v", put, putErr)
	}
	assertStorageResultPreserved(t, put, raw.putResult, vvotel.OpStoragePut)
	fixture.assertCaptured(t, raw, vvotel.OpStoragePut)
	stage, stageErr := store.Stage(context.Background(), fixture.stageSource, fixture.stageOptions)
	if stageErr != nil || stage.ID != raw.stageResult.ID || stage.Info.Size != raw.stageResult.Info.Size {
		t.Fatalf("Stage result changed: result=%+v err=%v", stage, stageErr)
	}
	assertStorageResultPreserved(t, stage, raw.stageResult, vvotel.OpStorageStage)
	fixture.assertCaptured(t, raw, vvotel.OpStorageStage)

	records := mp.named(vvotel.MetricStorageOperationBytes)
	if len(records) != 2 {
		t.Fatalf("persisted-size records=%d", len(records))
	}
	want := []struct {
		operation string
		size      int64
	}{
		{operation: vvotel.OpStoragePut, size: raw.putResult.Size},
		{operation: vvotel.OpStorageStage, size: raw.stageResult.Info.Size},
	}
	for index, record := range records {
		if record.value != want[index].size {
			t.Fatalf("record %d value=%v, want %d", index, record.value, want[index].size)
		}
		assertStorageOperationAttributes(t, record.attributes, want[index].operation, vvotel.OutcomeOk, "")
	}

	raw.err = errors.New("write failed")
	raw.putResult.Size = 2048
	_, _ = store.Put(context.Background(), fixture.putKey, fixture.putSource, fixture.putOptions)
	raw.err = nil
	raw.putResult.Size = -1
	negative, negativeErr := store.Put(context.Background(), fixture.putKey, fixture.putSource, fixture.putOptions)
	if negativeErr != nil || negative.Size != -1 {
		t.Fatalf("negative successful result changed: result=%+v err=%v", negative, negativeErr)
	}
	assertStorageResultPreserved(t, negative, raw.putResult, vvotel.OpStoragePut)
	fixture.assertCaptured(t, raw, vvotel.OpStoragePut)
	if raw.calls != 4 || raw.operationCalls[vvotel.OpStoragePut] != 3 || raw.operationCalls[vvotel.OpStorageStage] != 1 {
		t.Fatalf("Store calls=%d operations=%v", raw.calls, raw.operationCalls)
	}
	if got := len(mp.named(vvotel.MetricStorageOperationBytes)); got != 2 {
		t.Fatalf("error or negative result emitted persisted size: records=%d", got)
	}
	if got := len(mp.named(vvotel.MetricStorageDuration)); got != 0 {
		t.Fatalf("disabled duration emitted %d records", got)
	}
}

func TestStorageCleanupRemovedUsesExactBoundedSuccessfulResult(t *testing.T) {
	mp := &storageMetricProvider{}
	tel, err := vvotel.New(vvotel.Config{
		MeterProvider: mp,
		Disable:       vvotel.Signals{vvotel.SignalStorageDuration, vvotel.SignalStorageOperationBytes},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		removed int
		more    bool
		err     error
		emit    bool
	}{
		{removed: 0, more: false, emit: true},
		{removed: 1, more: true, emit: true},
		{removed: storage.MaxCleanupLimit, more: false, emit: true},
		{removed: -1, more: true},
		{removed: storage.MaxCleanupLimit + 1},
		{removed: 17, more: true, err: errors.New("cleanup failed")},
	}
	wantRecords := 0
	for _, current := range cases {
		raw := newExactFakeStorageStore(t)
		raw.cleanupResult = storage.CleanupResult{Removed: current.removed, More: current.more}
		raw.err = current.err
		options := storage.CleanupOptions{Limit: 613}
		result, callErr := vvotel.Store(tel)(raw).CleanupExpired(context.Background(), options)
		if callErr != current.err || result != raw.cleanupResult || raw.cleanupOptions != options || raw.calls != 1 {
			t.Fatalf("cleanup effect changed for %+v: result=%+v err=%v calls=%d options=%+v", current, result, callErr, raw.calls, raw.cleanupOptions)
		}
		if current.emit {
			wantRecords++
		}
		if got := len(mp.named(vvotel.MetricStorageCleanupRemoved)); got != wantRecords {
			t.Fatalf("cleanup records=%d, want %d after %+v", got, wantRecords, current)
		}
	}

	records := mp.named(vvotel.MetricStorageCleanupRemoved)
	for index, record := range records {
		if record.value != int64(cases[index].removed) {
			t.Fatalf("record %d value=%v, want %d", index, record.value, cases[index].removed)
		}
		assertStorageCleanupAttributes(t, record.attributes, cases[index].more)
	}
}

func assertStorageOperationAttributes(t *testing.T, attributes map[attribute.Key]attribute.Value, operation string, outcome string, errorType string) {
	t.Helper()
	wantCount := 3
	if errorType != "" {
		wantCount++
	}
	if len(attributes) != wantCount || attributes[vvotel.AttrComponent].AsString() != vvotel.ComponentStorage || attributes[vvotel.AttrOperationName].AsString() != operation || attributes[vvotel.AttrOperationOutcome].AsString() != outcome || attributes[vvotel.AttrErrorType].AsString() != errorType {
		t.Fatalf("storage attributes=%v", attributes)
	}
}

func assertStorageCleanupAttributes(t *testing.T, attributes map[attribute.Key]attribute.Value, more bool) {
	t.Helper()
	if len(attributes) != 4 || attributes[vvotel.AttrComponent].AsString() != vvotel.ComponentStorage || attributes[vvotel.AttrOperationName].AsString() != vvotel.OpStorageCleanupExpired || attributes[vvotel.AttrOperationOutcome].AsString() != vvotel.OutcomeOk || attributes[vvotel.AttrMore].AsBool() != more {
		t.Fatalf("cleanup attributes=%v", attributes)
	}
}
