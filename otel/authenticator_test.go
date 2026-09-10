package vvotel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type authenticationContextKey struct{}

func TestAuthenticatorPreservesTheCompleteCallAndUsesTheDerivedContext(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	secret := "secret bearer token and principal"
	credential := auth.Credential{Scheme: auth.SchemeBearer, Token: secret}
	principal := &auth.Claims{Sub: secret}
	incoming := context.WithValue(context.Background(), authenticationContextKey{}, "preserved")
	var receivedContext context.Context
	var receivedCredential auth.Credential
	calls := 0
	next := auth.AuthenticatorFunc(func(ctx context.Context, got auth.Credential) (auth.Principal, error) {
		calls++
		receivedContext = ctx
		receivedCredential = got
		return principal, nil
	})

	got, gotErr := vvotel.Authenticator(tel, next).Authenticate(incoming, credential)
	if gotErr != nil || got != principal {
		t.Fatalf("result = %T/%v, error = %v", got, got, gotErr)
	}
	if calls != 1 || receivedCredential != credential {
		t.Fatalf("calls/credential = %d/%+v, want 1/%+v", calls, receivedCredential, credential)
	}
	if receivedContext == incoming || receivedContext.Value(authenticationContextKey{}) != "preserved" || !hasTestSpan(receivedContext) {
		t.Fatal("authenticator did not receive the derived span context with incoming values")
	}
	if len(tp.spans) != 1 || len(mp.metrics) != 1 {
		t.Fatalf("spans/metrics = %d/%d, want 1/1", len(tp.spans), len(mp.metrics))
	}
	span := tp.spans[0]
	if span.name != vvotel.SpanAuthentication || span.kind != trace.SpanKindInternal || !span.ended || span.status != codes.Unset {
		t.Fatalf("unexpected authentication span: %+v", span)
	}
	assertAuthenticationAttributes(t, span.attributes, vvotel.OutcomeOk, "")
	metric := mp.metrics[0]
	if metric.name != vvotel.MetricAuthenticationDuration || metric.context != receivedContext {
		t.Fatalf("unexpected authentication metric: %+v", metric)
	}
	assertAuthenticationAttributes(t, metric.attributes, vvotel.OutcomeOk, "")
	assertAuthTelemetryHasNoSecret(t, secret, span, metric)
}

func TestAuthenticatorClassifiesRefusalAndInfrastructureErrorWithoutLeakingThem(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		outcome       string
		errorType     string
		wantSpanState codes.Code
	}{
		{name: "refused", err: auth.Unauthenticated("secret refusal"), outcome: "refused", wantSpanState: codes.Unset},
		{name: "infrastructure", err: errors.New("secret infrastructure failure"), outcome: vvotel.OutcomeError, errorType: vvotel.ErrorTypeInternal, wantSpanState: codes.Error},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tp := newTestTracerProvider()
			mp := newTestMeterProvider()
			tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			next := auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
				calls++
				return nil, test.err
			})
			_, gotErr := vvotel.Authenticator(tel, next).Authenticate(context.Background(), auth.Credential{Token: "secret token"})
			if gotErr != test.err || calls != 1 {
				t.Fatalf("error/calls = %v/%d, want exact error and one call", gotErr, calls)
			}
			if len(tp.spans) != 1 || len(mp.metrics) != 1 {
				t.Fatalf("spans/metrics = %d/%d, want 1/1", len(tp.spans), len(mp.metrics))
			}
			if tp.spans[0].status != test.wantSpanState {
				t.Fatalf("span status = %v, want %v", tp.spans[0].status, test.wantSpanState)
			}
			assertAuthenticationAttributes(t, tp.spans[0].attributes, test.outcome, test.errorType)
			assertAuthenticationAttributes(t, mp.metrics[0].attributes, test.outcome, test.errorType)
			assertAuthTelemetryHasNoSecret(t, "secret", tp.spans[0], mp.metrics[0])
		})
	}
}

func TestAuthenticatorPreservesPanicIdentityAndContainsTelemetryFaults(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	panicValue := &struct{ value string }{value: "business panic"}
	panicking := vvotel.Authenticator(tel, auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		panic(panicValue)
	}))
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = panicking.Authenticate(context.Background(), auth.Credential{})
	}()
	if recovered != panicValue {
		t.Fatalf("panic = %v, want original pointer", recovered)
	}
	if len(tp.spans) != 1 || len(mp.metrics) != 1 {
		t.Fatalf("panic spans/metrics = %d/%d, want 1/1", len(tp.spans), len(mp.metrics))
	}
	assertAuthenticationAttributes(t, tp.spans[0].attributes, vvotel.OutcomeError, vvotel.ErrorTypePanic)
	assertAuthenticationAttributes(t, mp.metrics[0].attributes, vvotel.OutcomeError, vvotel.ErrorTypePanic)

	tp = newTestTracerProvider()
	mp = newTestMeterProvider()
	tel, err = vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	tp.panicStart = true
	mp.panicHistogramRecord = true
	principal := &auth.Claims{Sub: "kept"}
	result, gotErr := vvotel.Authenticator(tel, auth.AuthenticatorFunc(func(ctx context.Context, _ auth.Credential) (auth.Principal, error) {
		if ctx.Value(authenticationContextKey{}) != "incoming" {
			t.Fatal("trace failure did not fall back to the incoming context")
		}
		return principal, nil
	})).Authenticate(context.WithValue(context.Background(), authenticationContextKey{}, "incoming"), auth.Credential{})
	if gotErr != nil || result != principal || mp.histogramRecordCalls != 1 {
		t.Fatalf("faulted telemetry changed result/error or skipped metric: %T %v, %v, calls=%d", result, result, gotErr, mp.histogramRecordCalls)
	}
}

func assertAuthenticationAttributes(t *testing.T, attributes map[attribute.Key]attribute.Value, outcome string, errorType string) {
	t.Helper()
	want := 3
	if errorType != "" {
		want++
	}
	if len(attributes) != want ||
		attributes[vvotel.AttrComponent].AsString() != vvotel.ComponentAuthentication ||
		attributes[vvotel.AttrOperationName].AsString() != vvotel.OpAuthenticationAuthenticate ||
		attributes[vvotel.AttrOperationOutcome].AsString() != outcome {
		t.Fatalf("unexpected authentication attributes: %v", attributes)
	}
	value, present := attributes[vvotel.AttrErrorType]
	if present != (errorType != "") || present && value.AsString() != errorType {
		t.Fatalf("error.type = %v/%v, want %q", value, present, errorType)
	}
}
