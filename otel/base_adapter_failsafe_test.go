package vvotel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/jobs"
	vvotel "github.com/frostgrove/vv/otel"
)

func TestNewAdaptersKeepMetricsAndIncomingContextWhenTraceStartFails(t *testing.T) {
	for _, mode := range []string{"panic", "typed_nil_span", "typed_nil_context"} {
		t.Run(mode, func(t *testing.T) {
			t.Run("crud", func(t *testing.T) {
				tp := newTestTracerProvider()
				mp := newTestMeterProvider()
				tel := vvotel.Must(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
				setTraceStartFailure(tp, mode)
				want := crud.Result{RowsAffected: 37, LastInsertID: 41, HasLastInsertID: true}
				source := &crudRecordingSource{dialect: crud.Postgres{}, execResult: want}
				wrapped := vvotel.Source(tel, source)
				incoming := context.WithValue(context.Background(), struct{ name string }{"crud"}, new(int))

				got, err := wrapped.Exec(incoming, "private statement", "private argument")
				if err != nil || got != want || source.execCalls != 1 || source.lastCtx != incoming {
					t.Fatalf("CRUD fallback = %#v/%v calls=%d context=%v", got, err, source.execCalls, source.lastCtx == incoming)
				}
				assertFallbackMetricContexts(t, mp, incoming, 1)
			})

			t.Run("enqueue", func(t *testing.T) {
				tp := newTestTracerProvider()
				mp := newTestMeterProvider()
				tel := vvotel.Must(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
				setTraceStartFailure(tp, mode)
				queue, definition, sender, _ := jobsEnqueueTestFixture(t)
				incoming := context.WithValue(context.Background(), struct{ name string }{"enqueue"}, new(int))

				id, err := vvotel.Enqueue(incoming, tel, queue, definition, "private payload")
				if err != nil || id.IsZero() || sender.calls != 1 || len(sender.contexts) != 1 || sender.contexts[0] != incoming {
					t.Fatalf("enqueue fallback = %v/%v calls=%d context=%v", id, err, sender.calls, len(sender.contexts) == 1 && sender.contexts[0] == incoming)
				}
				assertFallbackMetricContexts(t, mp, incoming, 1)
			})

			t.Run("handler", func(t *testing.T) {
				tp := newTestTracerProvider()
				mp := newTestMeterProvider()
				tel := vvotel.Must(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
				setTraceStartFailure(tp, mode)
				incoming := context.WithValue(context.Background(), struct{ name string }{"handler"}, new(int))
				wantErr := errors.New("private handler failure")
				wantMeta := jobsHandlerTestMeta(t)
				wantController := &jobsHandlerController{}
				calls := 0
				var received context.Context
				handler := vvotel.JobAdapter(tel, jobs.AdapterHandler[string](func(ctx context.Context, payload string, gotMeta jobs.DeliveryMeta, gotController jobs.AttemptController) error {
					calls++
					received = ctx
					if payload != "private payload" || gotMeta != wantMeta || gotController != wantController {
						t.Fatal("handler fallback changed inputs")
					}
					return wantErr
				}))

				err := handler(incoming, "private payload", wantMeta, wantController)
				if err != wantErr || calls != 1 || received != incoming {
					t.Fatalf("handler fallback = %v calls=%d context=%v", err, calls, received == incoming)
				}
				assertFallbackMetricContexts(t, mp, incoming, 3)
			})
		})
	}
}

func setTraceStartFailure(provider *testTracerProvider, mode string) {
	switch mode {
	case "panic":
		provider.panicStart = true
	case "typed_nil_span":
		provider.typedNilSpan = true
	case "typed_nil_context":
		provider.typedNilContext = true
	}
}

func assertFallbackMetricContexts(t *testing.T, provider *testMeterProvider, incoming context.Context, count int) {
	t.Helper()
	if provider.metricCount() != count {
		t.Fatalf("fallback metrics = %d, want %d", provider.metricCount(), count)
	}
	for _, measurement := range provider.metrics {
		if measurement.context != incoming {
			t.Fatalf("metric %q did not use exact incoming context", measurement.name)
		}
	}
}
