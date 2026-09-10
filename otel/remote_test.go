package vvotel_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/remote"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type remoteTransportFunc func(context.Context, *remote.Call) (json.RawMessage, error)

func (f remoteTransportFunc) Do(ctx context.Context, call *remote.Call) (json.RawMessage, error) {
	return f(ctx, call)
}

type remoteContextKey struct{}

func TestRemotePreservesTheCallResultAndDerivedContext(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	secret := "secret-id-query-body"
	call := &remote.Call{
		Method: remote.MethodGet,
		ID:     secret,
		IDs:    json.RawMessage(`[` + `"` + secret + `"` + `]`),
		Body:   json.RawMessage(`{"secret":"` + secret + `"}`),
	}
	response := json.RawMessage(`{"ok":true}`)
	incoming := context.WithValue(context.Background(), remoteContextKey{}, "preserved")
	var receivedContext context.Context
	var receivedCall *remote.Call
	calls := 0
	next := remoteTransportFunc(func(ctx context.Context, got *remote.Call) (json.RawMessage, error) {
		calls++
		receivedContext = ctx
		receivedCall = got
		return response, nil
	})

	got, gotErr := vvotel.Remote(tel, next).Do(incoming, call)
	if gotErr != nil || calls != 1 || receivedCall != call {
		t.Fatalf("error/calls/call = %v/%d/%p, want nil/1/%p", gotErr, calls, receivedCall, call)
	}
	if len(got) != len(response) || &got[0] != &response[0] {
		t.Fatal("remote response slice identity changed")
	}
	if receivedContext == incoming || receivedContext.Value(remoteContextKey{}) != "preserved" || receivedContext.Value(spanKey{}) == nil {
		t.Fatal("transport did not receive the derived span context with incoming values")
	}
	if len(tp.spans) != 1 || len(mp.metrics) != 1 {
		t.Fatalf("spans/metrics = %d/%d, want 1/1", len(tp.spans), len(mp.metrics))
	}
	span := tp.spans[0]
	if span.name != "vv.remote get" || span.kind != trace.SpanKindInternal || !span.ended {
		t.Fatalf("unexpected remote span: %+v", span)
	}
	assertRemoteAttributes(t, span.attributes, vvotel.OpRemoteGet, vvotel.OutcomeOk, "")
	if mp.metrics[0].name != vvotel.MetricRemoteDuration || mp.metrics[0].context != receivedContext {
		t.Fatalf("unexpected remote metric: %+v", mp.metrics[0])
	}
	assertRemoteAttributes(t, mp.metrics[0].attributes, vvotel.OpRemoteGet, vvotel.OutcomeOk, "")
	assertRemoteTelemetryHasNoSecret(t, secret, span, mp.metrics[0])
}

func TestRemoteMapsOnlyClosedMethodsAndKeepsUnknownCallsUninstrumented(t *testing.T) {
	methods := []struct {
		method remote.Method
		op     string
	}{
		{remote.MethodList, vvotel.OpRemoteList},
		{remote.MethodCount, vvotel.OpRemoteCount},
		{remote.MethodGet, vvotel.OpRemoteGet},
		{remote.MethodCreate, vvotel.OpRemoteCreate},
		{remote.MethodUpdate, vvotel.OpRemoteUpdate},
		{remote.MethodReplace, vvotel.OpRemoteReplace},
		{remote.MethodDelete, vvotel.OpRemoteDelete},
		{remote.MethodBulkDelete, vvotel.OpRemoteDeleteMany},
	}
	for _, test := range methods {
		t.Run(string(test.method), func(t *testing.T) {
			tp := newTestTracerProvider()
			mp := newTestMeterProvider()
			tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
			if err != nil {
				t.Fatal(err)
			}
			wrapped := vvotel.Remote(tel, remoteTransportFunc(func(context.Context, *remote.Call) (json.RawMessage, error) {
				return nil, nil
			}))
			_, _ = wrapped.Do(context.Background(), &remote.Call{Method: test.method})
			if len(tp.spans) != 1 || len(mp.metrics) != 1 {
				t.Fatalf("spans/metrics = %d/%d, want 1/1", len(tp.spans), len(mp.metrics))
			}
			assertRemoteAttributes(t, tp.spans[0].attributes, test.op, vvotel.OutcomeOk, "")
			assertRemoteAttributes(t, mp.metrics[0].attributes, test.op, vvotel.OutcomeOk, "")
		})
	}

	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	incoming := context.WithValue(context.Background(), remoteContextKey{}, "exact")
	unknown := &remote.Call{Method: remote.Method("secret-unbounded-method")}
	calls := 0
	wrapped := vvotel.Remote(tel, remoteTransportFunc(func(ctx context.Context, call *remote.Call) (json.RawMessage, error) {
		calls++
		if ctx != incoming || call != unknown {
			t.Fatal("unknown call or context changed")
		}
		return nil, nil
	}))
	_, _ = wrapped.Do(incoming, unknown)
	if calls != 1 || len(tp.spans) != 0 || len(mp.metrics) != 0 {
		t.Fatalf("unknown calls/spans/metrics = %d/%d/%d, want 1/0/0", calls, len(tp.spans), len(mp.metrics))
	}
}

func TestRemotePreservesErrorsAndPanicsWithoutLeakingInputs(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	secret := "secret remote failure and body"
	wantErr := fmt.Errorf("%s: %w", secret, crud.ErrNotFound)
	call := &remote.Call{Method: remote.MethodDelete, ID: secret, Body: json.RawMessage(secret)}
	calls := 0
	wrapped := vvotel.Remote(tel, remoteTransportFunc(func(context.Context, *remote.Call) (json.RawMessage, error) {
		calls++
		return json.RawMessage("partial"), wantErr
	}))
	result, gotErr := wrapped.Do(context.Background(), call)
	if gotErr != wantErr || string(result) != "partial" || calls != 1 {
		t.Fatalf("result/error/calls = %q/%v/%d", result, gotErr, calls)
	}
	assertRemoteAttributes(t, tp.spans[0].attributes, vvotel.OpRemoteDelete, vvotel.OutcomeError, vvotel.ErrorTypeNotFound)
	assertRemoteAttributes(t, mp.metrics[0].attributes, vvotel.OpRemoteDelete, vvotel.OutcomeError, vvotel.ErrorTypeNotFound)
	assertRemoteTelemetryHasNoSecret(t, secret, tp.spans[0], mp.metrics[0])

	tp = newTestTracerProvider()
	mp = newTestMeterProvider()
	tel, err = vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	panicValue := &struct{ value string }{value: secret}
	wrapped = vvotel.Remote(tel, remoteTransportFunc(func(context.Context, *remote.Call) (json.RawMessage, error) {
		panic(panicValue)
	}))
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = wrapped.Do(context.Background(), &remote.Call{Method: remote.MethodList})
	}()
	if recovered != panicValue {
		t.Fatalf("panic = %v, want original pointer", recovered)
	}
	assertRemoteAttributes(t, tp.spans[0].attributes, vvotel.OpRemoteList, vvotel.OutcomeError, vvotel.ErrorTypePanic)
	assertRemoteAttributes(t, mp.metrics[0].attributes, vvotel.OpRemoteList, vvotel.OutcomeError, vvotel.ErrorTypePanic)
}

func TestRemoteContainsTelemetryFaults(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	tp.panicStart = true
	mp.panicHistogramRecord = true
	want := json.RawMessage("exact response")
	incoming := context.WithValue(context.Background(), remoteContextKey{}, "incoming")
	result, gotErr := vvotel.Remote(tel, remoteTransportFunc(func(ctx context.Context, _ *remote.Call) (json.RawMessage, error) {
		if ctx != incoming {
			t.Fatal("trace failure did not restore the incoming context")
		}
		return want, nil
	})).Do(incoming, &remote.Call{Method: remote.MethodCount})
	if gotErr != nil || len(result) != len(want) || &result[0] != &want[0] || mp.histogramRecordCalls != 1 {
		t.Fatalf("faulted telemetry changed result/error or skipped metric: %q, %v, calls=%d", result, gotErr, mp.histogramRecordCalls)
	}
}

func assertRemoteAttributes(t *testing.T, attributes map[attribute.Key]attribute.Value, operation string, outcome string, errorType string) {
	t.Helper()
	want := 3
	if errorType != "" {
		want++
	}
	if len(attributes) != want ||
		attributes[vvotel.AttrComponent].AsString() != vvotel.ComponentRemote ||
		attributes[vvotel.AttrOperationName].AsString() != operation ||
		attributes[vvotel.AttrOperationOutcome].AsString() != outcome {
		t.Fatalf("unexpected remote attributes: %v", attributes)
	}
	value, present := attributes[vvotel.AttrErrorType]
	if present != (errorType != "") || present && value.AsString() != errorType {
		t.Fatalf("error.type = %v/%v, want %q", value, present, errorType)
	}
}

func assertRemoteTelemetryHasNoSecret(t *testing.T, secret string, span *recordedSpan, metric *recordedMetric) {
	t.Helper()
	values := []string{span.name, metric.name}
	for key, value := range span.attributes {
		values = append(values, string(key), value.Emit())
	}
	for key, value := range metric.attributes {
		values = append(values, string(key), value.Emit())
	}
	for _, value := range values {
		if strings.Contains(value, secret) {
			t.Fatalf("secret appeared in telemetry: %q", value)
		}
	}
}
