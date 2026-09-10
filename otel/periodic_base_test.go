package vvotel_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/codes"
)

type periodicContextKey struct{}

func TestPeriodicObservesOnePassWithoutChangingItsContract(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("periodic secret detail")
	calls := 0
	var received context.Context
	pass := vvotel.Periodic(tel, func(ctx context.Context) error {
		calls++
		received = ctx
		return wantErr
	})
	ctx := context.WithValue(t.Context(), periodicContextKey{}, "kept")
	if got := pass(ctx); got != wantErr {
		t.Fatalf("pass error identity changed: got %v, want same value", got)
	}
	if calls != 1 {
		t.Fatalf("pass calls=%d, want one", calls)
	}
	if received.Value(periodicContextKey{}) != "kept" || !hasTestSpan(received) {
		t.Fatal("pass did not receive the derived span context with caller values")
	}
	if len(tp.spans) != 1 || !tp.spans[0].ended || tp.spans[0].name != vvotel.SpanRuntimePeriodic {
		t.Fatalf("periodic spans=%+v, want one ended %q span", tp.spans, vvotel.SpanRuntimePeriodic)
	}
	if tp.spans[0].status != codes.Error {
		t.Fatalf("periodic status=%v, want error", tp.spans[0].status)
	}
	if mp.metricCount() != 1 || mp.histogramRecordCalls != 1 {
		t.Fatalf("metrics=%d histogram=%d, want one", mp.metricCount(), mp.histogramRecordCalls)
	}
	metric := mp.metrics[0]
	if metric.name != vvotel.MetricRuntimePeriodicDuration {
		t.Fatalf("metric name=%q, want %q", metric.name, vvotel.MetricRuntimePeriodicDuration)
	}
	if got := metric.attributes[vvotel.AttrErrorType].AsString(); got != vvotel.ErrorTypeInternal {
		t.Fatalf("metric error.type=%q, want internal", got)
	}
}

func TestPeriodicReturnsTheOriginalFunctionWhenTelemetryIsDisabled(t *testing.T) {
	original := func(context.Context) error { return nil }
	wrapped := vvotel.Periodic(vvotel.Must(vvotel.Config{Disabled: true}), original)
	if reflect.ValueOf(wrapped).Pointer() != reflect.ValueOf(original).Pointer() {
		t.Fatal("disabled telemetry replaced the pass")
	}
}
