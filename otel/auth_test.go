package vvotel_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/attribute"
)

func TestAuthRecordsOnlyClosedRefusalReasons(t *testing.T) {
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	secret := "secret credential and refusal detail"
	observer := vvotel.Auth(tel)
	observer.Refused(context.Background(), auth.Reason{
		Kind:   auth.ReasonRejected,
		Detail: secret,
		Err:    errors.New(secret),
	})
	observer.Refused(context.Background(), auth.Reason{Kind: auth.ReasonKind(secret)})

	if len(mp.metrics) != 1 {
		t.Fatalf("recorded metrics = %d, want 1", len(mp.metrics))
	}
	record := mp.metrics[0]
	if record.name != vvotel.MetricAuthRefusals || record.value != int64(1) {
		t.Fatalf("metric = %q %v, want %q 1", record.name, record.value, vvotel.MetricAuthRefusals)
	}
	assertAuthRefusalAttributes(t, record.attributes, "rejected")
	assertAuthTelemetryHasNoSecret(t, secret, nil, record)
}

func TestAuthEventsAnnotatesOnlyTheActiveSpan(t *testing.T) {
	tp := newTestTracerProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp})
	if err != nil {
		t.Fatal(err)
	}
	ctx, parent := tp.Tracer("test").Start(context.Background(), "parent")
	secret := "secret observer detail"
	observer := vvotel.AuthEvents(tel)
	observer.Refused(ctx, auth.Reason{
		Kind:   auth.ReasonNoCredential,
		Detail: secret,
		Err:    errors.New(secret),
	})
	observer.Refused(ctx, auth.Reason{Kind: auth.ReasonKind(secret)})
	parent.End()

	if len(tp.spans) != 1 {
		t.Fatalf("spans = %d, want only the caller span", len(tp.spans))
	}
	span := tp.spans[0]
	if len(span.events) != 1 || span.events[0] != vvotel.EventAuthRefusal {
		t.Fatalf("events = %v, want [%s]", span.events, vvotel.EventAuthRefusal)
	}
	assertAuthRefusalAttributes(t, span.eventAttributes[0], "no_credential")
	assertAuthTelemetryHasNoSecret(t, secret, span, nil)
}

func TestAuthObserversContainTelemetryPanics(t *testing.T) {
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	mp.panicCounterAdd = true
	vvotel.Auth(tel).Refused(context.Background(), auth.Reason{Kind: auth.ReasonRejected})
	if mp.counterAdds() != 1 || mp.metricCount() != 0 {
		t.Fatalf("counter calls/records = %d/%d, want 1/0", mp.counterAdds(), mp.metricCount())
	}

	tp := newTestTracerProvider()
	tel, err = vvotel.New(vvotel.Config{TracerProvider: tp})
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := tp.Tracer("test").Start(context.Background(), "parent")
	tp.panicAddEvent = true
	vvotel.AuthEvents(tel).Refused(ctx, auth.Reason{Kind: auth.ReasonRejected})
	span.End()
	if tp.eventCalls() != 1 {
		t.Fatalf("event calls = %d, want 1", tp.eventCalls())
	}
}

func assertAuthRefusalAttributes(t *testing.T, attributes map[attribute.Key]attribute.Value, reason string) {
	t.Helper()
	if len(attributes) != 4 ||
		attributes[vvotel.AttrComponent].AsString() != vvotel.ComponentAuthRefusal ||
		attributes[vvotel.AttrOperationName].AsString() != vvotel.OpAuthRefusalRefuse ||
		attributes[vvotel.AttrOperationOutcome].AsString() != "refused" ||
		attributes[vvotel.AttrReason].AsString() != reason {
		t.Fatalf("unexpected refusal attributes: %v", attributes)
	}
}

func assertAuthTelemetryHasNoSecret(t *testing.T, secret string, span *recordedSpan, metric *recordedMetric) {
	t.Helper()
	var values []string
	if span != nil {
		values = append(values, span.name)
		for key, value := range span.attributes {
			values = append(values, string(key), value.Emit())
		}
		for _, eventAttributes := range span.eventAttributes {
			for key, value := range eventAttributes {
				values = append(values, string(key), value.Emit())
			}
		}
	}
	if metric != nil {
		values = append(values, metric.name)
		for key, value := range metric.attributes {
			values = append(values, string(key), value.Emit())
		}
	}
	for _, value := range values {
		if strings.Contains(value, secret) {
			t.Fatalf("secret appeared in telemetry: %q", value)
		}
	}
}
