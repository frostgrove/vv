package vvotel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/health"
	"github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/codes"
)

type healthContextKey struct{}

type healthTestProbe struct {
	check func(context.Context) error
}

func (probe *healthTestProbe) Check(ctx context.Context) error {
	return probe.check(ctx)
}

func TestHealthObservesTheActualProbeAndPreservesTheContribution(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("database password=secret was rejected")
	calls := 0
	var received context.Context
	original := health.ProbeFunc(func(ctx context.Context) error {
		calls++
		received = ctx
		return wantErr
	})
	contribution := health.Contribution{
		Name:       "database-primary",
		Code:       "database",
		Importance: health.Required,
		Timeout:    73 * time.Millisecond,
		Probe:      original,
	}

	wrapped := vvotel.Health(tel, contribution)
	if wrapped.Name != contribution.Name || wrapped.Code != contribution.Code ||
		wrapped.Importance != contribution.Importance || wrapped.Timeout != contribution.Timeout {
		t.Fatalf("contribution changed: got %+v, want %+v", wrapped, contribution)
	}
	ctx := context.WithValue(t.Context(), healthContextKey{}, "kept")
	if got := wrapped.Probe.Check(ctx); got != wantErr {
		t.Fatalf("probe error identity changed: got %v, want same value", got)
	}
	if calls != 1 {
		t.Fatalf("probe calls=%d, want one", calls)
	}
	if received.Value(healthContextKey{}) != "kept" || !hasTestSpan(received) {
		t.Fatal("probe did not receive the derived span context with caller values")
	}
	if len(tp.spans) != 1 || !tp.spans[0].ended || tp.spans[0].name != vvotel.SpanHealth {
		t.Fatalf("health spans=%+v, want one ended %q span", tp.spans, vvotel.SpanHealth)
	}
	if tp.spans[0].status != codes.Error {
		t.Fatalf("health span status=%v, want error", tp.spans[0].status)
	}
	if got := tp.spans[0].attributes[vvotel.AttrImportance].AsString(); got != string(health.Required) {
		t.Fatalf("importance=%q, want %q", got, health.Required)
	}
	if mp.metricCount() != 2 || mp.histogramRecordCalls != 1 || mp.counterAddCalls != 1 {
		t.Fatalf("metrics=%d histogram=%d counter=%d, want 2/1/1", mp.metricCount(), mp.histogramRecordCalls, mp.counterAddCalls)
	}
	for _, measurement := range mp.metrics {
		if got := measurement.attributes[vvotel.AttrState].AsString(); got != "failing" {
			t.Fatalf("metric %q state=%q, want failing", measurement.name, got)
		}
		if got := measurement.attributes[vvotel.AttrErrorType].AsString(); got != vvotel.ErrorTypeInternal {
			t.Fatalf("metric %q error.type=%q, want internal", measurement.name, got)
		}
	}
}

func TestHealthLeavesDisabledContributionsUntouched(t *testing.T) {
	tel := vvotel.Must(vvotel.Config{Disabled: true})
	probe := &healthTestProbe{check: func(context.Context) error {
		t.Fatal("disabled probe was invoked")
		return nil
	}}
	contribution := health.Contribution{
		Name:       "offline-import",
		Code:       "import",
		Importance: health.Disabled,
		Timeout:    time.Second,
		Probe:      probe,
	}

	got := vvotel.Health(tel, contribution)
	if got.Name != contribution.Name || got.Code != contribution.Code ||
		got.Importance != contribution.Importance || got.Timeout != contribution.Timeout || got.Probe != contribution.Probe {
		t.Fatalf("disabled contribution changed: got %+v, want %+v", got, contribution)
	}
}
