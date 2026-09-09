package oteltelemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/health"
	vvotel "github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/remote"
	vvruntime "github.com/frostgrove/vv/runtime"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type baseSDKContextKey struct{}

type baseSDKTransport struct {
	calls    int
	ctx      context.Context
	call     *remote.Call
	response json.RawMessage
	err      error
}

func (transport *baseSDKTransport) Do(ctx context.Context, call *remote.Call) (json.RawMessage, error) {
	transport.calls++
	transport.ctx = ctx
	transport.call = call
	return transport.response, transport.err
}

type baseSDKRunner struct {
	runContext   chan context.Context
	drainContext chan context.Context
}

func (*baseSDKRunner) Name() string { return "runtime-runner-secret-19471" }

func (runner *baseSDKRunner) Run(ctx context.Context) error {
	runner.runContext <- ctx
	<-ctx.Done()
	return ctx.Err()
}

func (runner *baseSDKRunner) Drain(ctx context.Context) error {
	runner.drainContext <- ctx
	return nil
}

func (*baseSDKRunner) Declaration() vvruntime.Declaration {
	return vvruntime.Declaration{Placement: vvruntime.Singleton, Durability: vvruntime.Durable}
}

func TestRealSDKBaseIntegrationsPreserveEffectsAndCorrelateEverySignal(t *testing.T) {
	fixture := newSDKFixture(t, sdktrace.AlwaysSample())
	tel := vvotel.Must(vvotel.Config{
		TracerProvider: fixture.tracerProvider,
		MeterProvider:  fixture.meterProvider,
		ResourceName:   vvotel.MustApproveName("base-integrations"),
	})
	appTracer := fixture.tracerProvider.Tracer("application")
	callerContext := context.WithValue(context.Background(), baseSDKContextKey{}, "caller-value")
	ctx, parent := appTracer.Start(callerContext, "base request", trace.WithSpanKind(trace.SpanKindServer))

	authSecret := "auth-token-secret-20481"
	refusalSecret := "auth-refusal-secret-81942"
	credential := auth.Credential{Scheme: auth.SchemeBearer, Token: authSecret}
	principal := auth.Claims{Sub: "principal-secret-70315"}
	var authContext context.Context
	authCalls := 0
	authenticator := vvotel.Authenticator(tel, auth.AuthenticatorFunc(func(next context.Context, got auth.Credential) (auth.Principal, error) {
		authCalls++
		authContext = next
		if got != credential {
			t.Fatalf("credential = %#v, want original", got)
		}
		reason := auth.Reason{Kind: auth.ReasonRejected, Detail: refusalSecret, Err: errors.New(refusalSecret)}
		vvotel.Auth(tel).Refused(next, reason)
		vvotel.AuthEvents(tel).Refused(next, reason)
		return principal, nil
	}))
	gotPrincipal, err := authenticator.Authenticate(ctx, credential)
	if err != nil || !reflect.DeepEqual(gotPrincipal, principal) || authCalls != 1 {
		t.Fatalf("Authenticate = %#v, %v, calls=%d", gotPrincipal, err, authCalls)
	}
	assertDerivedSDKContext(t, ctx, authContext)

	healthSecret := errors.New("health-error-secret-31647")
	healthCalls := 0
	var healthContext context.Context
	contribution := health.Contribution{
		Name:       "health-name-secret-51289",
		Code:       "health-code-secret-60231",
		Importance: health.Required,
		Timeout:    73 * time.Millisecond,
		Probe: health.ProbeFunc(func(next context.Context) error {
			healthCalls++
			healthContext = next
			return healthSecret
		}),
	}
	wrappedContribution := vvotel.Health(tel, contribution)
	if wrappedContribution.Name != contribution.Name || wrappedContribution.Code != contribution.Code || wrappedContribution.Importance != contribution.Importance || wrappedContribution.Timeout != contribution.Timeout {
		t.Fatalf("health contribution changed: %#v", wrappedContribution)
	}
	if got := wrappedContribution.Probe.Check(ctx); got != healthSecret || healthCalls != 1 {
		t.Fatalf("health Check = %v, calls=%d", got, healthCalls)
	}
	assertDerivedSDKContext(t, ctx, healthContext)

	remoteResponse := json.RawMessage(`{"kept":true}`)
	transport := &baseSDKTransport{response: remoteResponse}
	remoteCall := &remote.Call{Method: remote.MethodGet, ID: "remote-id-secret-40812", Body: json.RawMessage(`{"secret":"remote-body-secret-52148"}`)}
	response, err := vvotel.Remote(tel, transport).Do(ctx, remoteCall)
	if err != nil || !bytes.Equal(response, remoteResponse) || transport.calls != 1 || transport.call != remoteCall {
		t.Fatalf("remote Do = %s, %v, calls=%d", response, err, transport.calls)
	}
	assertDerivedSDKContext(t, ctx, transport.ctx)

	periodicSecret := errors.New("periodic-error-secret-77126")
	periodicCalls := 0
	var periodicContext context.Context
	periodic := vvotel.Periodic(tel, func(next context.Context) error {
		periodicCalls++
		periodicContext = next
		return periodicSecret
	})
	if got := periodic(ctx); got != periodicSecret || periodicCalls != 1 {
		t.Fatalf("periodic pass = %v, calls=%d", got, periodicCalls)
	}
	assertDerivedSDKContext(t, ctx, periodicContext)

	runner := &baseSDKRunner{runContext: make(chan context.Context, 1), drainContext: make(chan context.Context, 1)}
	supervisor, err := vvruntime.NewSupervisor(vvruntime.Spec{
		Runners:    []vvruntime.Runner{runner},
		Observer:   vvotel.Runtime(tel),
		DrainGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Start(ctx); err != nil {
		t.Fatal(err)
	}
	runContext := <-runner.runContext
	if runContext.Value(baseSDKContextKey{}) != "caller-value" || !trace.SpanContextFromContext(runContext).Equal(trace.SpanContextFromContext(ctx)) {
		t.Fatal("runtime Run context lost caller values or trace context")
	}
	if err := supervisor.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	drainContext := <-runner.drainContext
	if drainContext.Value(baseSDKContextKey{}) != "caller-value" || !trace.SpanContextFromContext(drainContext).Equal(trace.SpanContextFromContext(ctx)) {
		t.Fatal("runtime Drain context lost caller values or trace context")
	}
	parent.End()

	spans := fixture.spans.Ended()
	parentSpan := findSpan(t, spans, "base request")
	authSpan := findSpan(t, spans, vvotel.SpanAuthentication)
	healthSpan := findSpan(t, spans, vvotel.SpanHealth)
	remoteSpan := findSpan(t, spans, mustRemoteSpanName(t, vvotel.OpRemoteGet))
	periodicSpan := findSpan(t, spans, vvotel.SpanRuntimePeriodic)
	for _, span := range []sdktrace.ReadOnlySpan{authSpan, healthSpan, remoteSpan, periodicSpan} {
		if span.SpanKind() != trace.SpanKindInternal || !span.Parent().Equal(parentSpan.SpanContext()) {
			t.Fatalf("span %q kind/parent = %s/%s", span.Name(), span.SpanKind(), span.Parent().SpanID())
		}
	}
	if len(authSpan.Events()) != 1 || authSpan.Events()[0].Name != vvotel.EventAuthRefusal {
		t.Fatalf("authentication events = %#v", authSpan.Events())
	}

	metrics := collectMetricData(t, fixture.metrics)
	assertFloatMetricExemplar(t, metrics, vvotel.MetricAuthenticationDuration, authSpan.SpanContext())
	assertIntMetricExemplar(t, metrics, vvotel.MetricAuthRefusals, authSpan.SpanContext())
	assertFloatMetricExemplar(t, metrics, vvotel.MetricHealthDuration, healthSpan.SpanContext())
	assertIntMetricExemplar(t, metrics, vvotel.MetricHealthChecks, healthSpan.SpanContext())
	assertFloatMetricExemplar(t, metrics, vvotel.MetricRemoteDuration, remoteSpan.SpanContext())
	assertFloatMetricExemplar(t, metrics, vvotel.MetricRuntimePeriodicDuration, periodicSpan.SpanContext())
	assertFloatMetricExemplar(t, metrics, vvotel.MetricRuntimeDuration, parentSpan.SpanContext())
	assertIntMetricExemplar(t, metrics, vvotel.MetricRuntimeOperations, parentSpan.SpanContext())
	findMetricData(t, metrics, vvotel.MetricRuntimeTransitions)
	assertSDKPrivacy(t, spans, metrics, []string{
		authSecret,
		refusalSecret,
		principal.Sub,
		contribution.Name,
		contribution.Code,
		healthSecret.Error(),
		remoteCall.ID,
		"remote-body-secret-52148",
		periodicSecret.Error(),
		runner.Name(),
	})
}

func assertDerivedSDKContext(t *testing.T, parent context.Context, child context.Context) {
	t.Helper()
	if child == nil || child.Value(baseSDKContextKey{}) != "caller-value" {
		t.Fatal("derived context lost caller value")
	}
	parentSpan := trace.SpanContextFromContext(parent)
	childSpan := trace.SpanContextFromContext(child)
	if !childSpan.IsValid() || childSpan.Equal(parentSpan) || childSpan.TraceID() != parentSpan.TraceID() {
		t.Fatalf("derived span context = %s/%s, parent = %s/%s", childSpan.TraceID(), childSpan.SpanID(), parentSpan.TraceID(), parentSpan.SpanID())
	}
}

func mustRemoteSpanName(t *testing.T, operation string) string {
	t.Helper()
	name, ok := vvotel.SpanRemoteName(operation)
	if !ok {
		t.Fatalf("no remote span name for %q", operation)
	}
	return name
}

func assertFloatMetricExemplar(t *testing.T, data metricdata.ResourceMetrics, name string, spanContext trace.SpanContext) {
	t.Helper()
	measurement := findMetricData(t, data, name)
	histogram, ok := measurement.Data.(metricdata.Histogram[float64])
	if !ok || len(histogram.DataPoints) == 0 {
		t.Fatalf("metric %q data = %T", name, measurement.Data)
	}
	for _, point := range histogram.DataPoints {
		for _, exemplar := range point.Exemplars {
			if exemplarMatches(exemplar.TraceID, exemplar.SpanID, spanContext) {
				return
			}
		}
	}
	t.Fatalf("metric %q has no exemplar for %s/%s", name, spanContext.TraceID(), spanContext.SpanID())
}

func assertIntMetricExemplar(t *testing.T, data metricdata.ResourceMetrics, name string, spanContext trace.SpanContext) {
	t.Helper()
	measurement := findMetricData(t, data, name)
	sum, ok := measurement.Data.(metricdata.Sum[int64])
	if !ok || len(sum.DataPoints) == 0 {
		t.Fatalf("metric %q data = %T", name, measurement.Data)
	}
	for _, point := range sum.DataPoints {
		for _, exemplar := range point.Exemplars {
			if exemplarMatches(exemplar.TraceID, exemplar.SpanID, spanContext) {
				return
			}
		}
	}
	t.Fatalf("metric %q has no exemplar for %s/%s", name, spanContext.TraceID(), spanContext.SpanID())
}

func exemplarMatches(traceID []byte, spanID []byte, spanContext trace.SpanContext) bool {
	wantTraceID := spanContext.TraceID()
	wantSpanID := spanContext.SpanID()
	return bytes.Equal(traceID, wantTraceID[:]) && bytes.Equal(spanID, wantSpanID[:])
}
