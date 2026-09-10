package otelnative

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/frostgrove/vv/health"
	vvotel "github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestHTTPManagementRoutesAreIncludedByDefault(t *testing.T) {
	routes := NewHTTPRoutes()
	if err := routes.HandleFunc("/live", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}); err != nil {
		t.Fatal(err)
	}
	telemetry := newTelemetryFixture(t, TraceProjectionPolicy{
		HTTPRoutes: routes,
		ResourceAttributes: []attribute.KeyValue{
			attribute.String("service.name", allowedServiceName),
		},
	}, nil)
	handler, err := HTTPServer(telemetry.providers, routes, TrustedIngress)
	if err != nil {
		t.Fatal(err)
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://service/live", nil))
	spans := telemetry.nativeSpans()
	if len(spans) != 1 {
		t.Fatalf("default management spans = %d", len(spans))
	}
	for _, item := range spans[0].Attributes {
		if string(item.Key) == httpManagementMetricAttribute {
			t.Fatalf("metric-only marker reached request span: %#v", spans[0].Attributes)
		}
	}
	metrics := telemetry.flushMetrics(t)
	if count := httpServerDurationCount(metrics); count != 1 {
		t.Fatalf("default management duration count = %d", count)
	}
	points := httpServerDurationAttributes(metrics)
	if len(points) != 1 || points[0]["http.route"] != "/live" || points[0][httpManagementMetricAttribute] != httpManagementMetricValue {
		t.Fatalf("default management attributes = %#v", points)
	}
	if count := httpSLODurationCount(metrics); count != 0 {
		t.Fatalf("default management SLO duration count = %d", count)
	}
}

func TestHTTPManagementMarkerOverridesLabelerSpoof(t *testing.T) {
	tests := []struct {
		name       string
		pattern    string
		spoof      string
		wantMarker string
		wantSLO    uint64
	}{
		{name: "qualified route cannot claim management", pattern: "GET /live", spoof: httpManagementMetricValue, wantMarker: httpApplicationMetricValue, wantSLO: 1},
		{name: "exact management route cannot opt back in", pattern: "/live", spoof: httpApplicationMetricValue, wantMarker: httpManagementMetricValue},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			routes := NewHTTPRoutes()
			if err := routes.HandleFunc(testCase.pattern, func(writer http.ResponseWriter, request *http.Request) {
				labeler, ok := otelhttp.LabelerFromContext(request.Context())
				if !ok {
					t.Fatal("otelhttp labeler is unavailable")
				}
				labeler.Add(attribute.String(httpManagementMetricAttribute, testCase.spoof))
				writer.WriteHeader(http.StatusNoContent)
			}); err != nil {
				t.Fatal(err)
			}
			telemetry := newTelemetryFixture(t, TraceProjectionPolicy{
				HTTPRoutes: routes,
				ResourceAttributes: []attribute.KeyValue{
					attribute.String("service.name", allowedServiceName),
				},
			}, nil)
			handler, err := HTTPServer(telemetry.providers, routes, TrustedIngress)
			if err != nil {
				t.Fatal(err)
			}
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://service/live", nil))
			metrics := telemetry.flushMetrics(t)
			points := httpServerDurationAttributes(metrics)
			if len(points) != 1 || points[0][httpManagementMetricAttribute] != testCase.wantMarker {
				t.Fatalf("duration attributes = %#v", points)
			}
			if got := httpSLODurationCount(metrics); got != testCase.wantSLO {
				t.Fatalf("SLO duration count = %d, want %d", got, testCase.wantSLO)
			}
			for _, span := range telemetry.nativeSpans() {
				for _, item := range span.Attributes {
					if string(item.Key) == httpManagementMetricAttribute {
						t.Fatalf("metric-only marker reached request span: %#v", span.Attributes)
					}
				}
			}
		})
	}
}

func TestHTTPManagementClassificationUsesSelectedPatternWhenRoutesCoexist(t *testing.T) {
	tests := []struct {
		name      string
		options   []HTTPServerOption
		wantSpans int
		wantFalse uint64
		wantTrue  uint64
	}{
		{name: "default", wantSpans: 4, wantFalse: 2, wantTrue: 2},
		{name: "both signals exclusion", options: []HTTPServerOption{ExcludeManagementSignals()}, wantSpans: 2, wantFalse: 2},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			routes := NewHTTPRoutes()
			calls := map[string]int{}
			inner := http.NewServeMux()
			inner.HandleFunc("/", func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			})
			for pattern, name := range map[string]string{
				"/live":                "exact live",
				"GET /live":            "method live",
				"/ready":               "exact ready",
				"status.example/ready": "host ready",
			} {
				name := name
				if err := routes.HandleFunc(pattern, func(writer http.ResponseWriter, request *http.Request) {
					calls[name]++
					inner.ServeHTTP(writer, request)
				}); err != nil {
					t.Fatal(err)
				}
			}
			telemetry := newTelemetryFixture(t, TraceProjectionPolicy{
				HTTPRoutes: routes,
				ResourceAttributes: []attribute.KeyValue{
					attribute.String("service.name", allowedServiceName),
				},
			}, nil)
			handler, err := HTTPServer(telemetry.providers, routes, TrustedIngress, testCase.options...)
			if err != nil {
				t.Fatal(err)
			}
			for _, request := range []*http.Request{
				httptest.NewRequest(http.MethodGet, "http://service/live", nil),
				httptest.NewRequest(http.MethodPost, "http://service/live", nil),
				httptest.NewRequest(http.MethodGet, "http://status.example/ready", nil),
				httptest.NewRequest(http.MethodGet, "http://service/ready", nil),
			} {
				handler.ServeHTTP(httptest.NewRecorder(), request)
			}
			for name, count := range calls {
				if count != 1 {
					t.Errorf("%s handler calls = %d, want 1", name, count)
				}
			}
			if len(calls) != 4 {
				t.Fatalf("executed handler kinds = %d, want 4", len(calls))
			}
			if got := len(telemetry.nativeSpans()); got != testCase.wantSpans {
				t.Fatalf("native spans = %d, want %d", got, testCase.wantSpans)
			}
			metrics := telemetry.flushMetrics(t)
			markers := httpServerDurationMarkerCounts(metrics)
			if markers[httpApplicationMetricValue] != testCase.wantFalse || markers[httpManagementMetricValue] != testCase.wantTrue {
				t.Fatalf("duration marker counts = %#v, want false=%d true=%d", markers, testCase.wantFalse, testCase.wantTrue)
			}
			if got := httpSLODurationCount(metrics); got != testCase.wantFalse {
				t.Fatalf("SLO duration count = %d, want %d", got, testCase.wantFalse)
			}
		})
	}
}

func TestHTTPManagementSignalExclusionMatchesOnlyRegisteredPathPatterns(t *testing.T) {
	cases := []struct {
		name        string
		pattern     string
		requestURL  string
		method      string
		wantSignals bool
	}{
		{name: "live", pattern: "/live", requestURL: "http://service/live", method: http.MethodPost},
		{name: "ready", pattern: "/ready", requestURL: "http://service/ready", method: http.MethodGet},
		{name: "method qualified", pattern: "GET /live", requestURL: "http://service/live", method: http.MethodGet, wantSignals: true},
		{name: "host qualified", pattern: "status.example/ready", requestURL: "http://status.example/ready", method: http.MethodGet, wantSignals: true},
		{name: "redirect", pattern: "/live/", requestURL: "http://service/live", method: http.MethodGet, wantSignals: true},
		{name: "unmatched", pattern: "/other", requestURL: "http://service/missing", method: http.MethodGet, wantSignals: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			routes := NewHTTPRoutes()
			handlerCalls := 0
			if err := routes.HandleFunc(testCase.pattern, func(writer http.ResponseWriter, _ *http.Request) {
				handlerCalls++
				writer.WriteHeader(http.StatusNoContent)
			}); err != nil {
				t.Fatal(err)
			}
			telemetry := newTelemetryFixture(t, TraceProjectionPolicy{
				HTTPRoutes: routes,
				ResourceAttributes: []attribute.KeyValue{
					attribute.String("service.name", allowedServiceName),
				},
			}, nil)
			handler, err := HTTPServer(telemetry.providers, routes, TrustedIngress, ExcludeManagementSignals())
			if err != nil {
				t.Fatal(err)
			}
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(testCase.method, testCase.requestURL, nil))
			wantCount := 0
			if testCase.wantSignals {
				wantCount = 1
			}
			if got := len(telemetry.nativeSpans()); got != wantCount {
				t.Fatalf("native spans = %d, want %d", got, wantCount)
			}
			metrics := telemetry.flushMetrics(t)
			if got := httpServerDurationCount(metrics); got != uint64(wantCount) {
				t.Fatalf("request duration count = %d, want %d", got, wantCount)
			}
			if got := httpSLODurationCount(metrics); got != uint64(wantCount) {
				t.Fatalf("SLO duration count = %d, want %d", got, wantCount)
			}
			points := httpServerDurationAttributes(metrics)
			if testCase.wantSignals {
				if len(points) != 1 {
					t.Fatalf("duration attributes = %#v", points)
				}
				if points[0][httpManagementMetricAttribute] != httpApplicationMetricValue {
					t.Fatalf("non-management route marker = %#v", points[0])
				}
				if (testCase.name == "method qualified" && points[0]["http.route"] != "/live") || (testCase.name == "host qualified" && points[0]["http.route"] != "/ready") {
					t.Fatalf("upstream route projection = %#v", points[0])
				}
			} else if len(points) != 0 {
				t.Fatalf("excluded route produced attributes = %#v", points)
			}
			if !testCase.wantSignals && handlerCalls != 1 {
				t.Fatalf("excluded handler calls = %d, want 1", handlerCalls)
			}
		})
	}
}

func TestHTTPManagementModesPreserveHealthProbeSignalsAndParentage(t *testing.T) {
	tests := []struct {
		name        string
		server      []HTTPServerOption
		metricsOnly bool
		wantRequest bool
	}{
		{name: "default", wantRequest: true},
		{name: "metrics only exclusion", metricsOnly: true, wantRequest: true},
		{name: "both signals exclusion", server: []HTTPServerOption{ExcludeManagementSignals()}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			routes := NewHTTPRoutes()
			telemetry := newTelemetryFixture(t, TraceProjectionPolicy{
				HTTPRoutes: routes,
				ResourceAttributes: []attribute.KeyValue{
					attribute.String("service.name", allowedServiceName),
				},
			}, nil)
			frost := vvotel.Must(vvotel.Config{
				TracerProvider: telemetry.providers.Tracer,
				MeterProvider:  telemetry.providers.Meter,
			})
			probeCalls := 0
			registry, err := health.Auto(vvotel.Health(frost, health.Contribution{
				Name:       "dependency",
				Importance: health.Required,
				Timeout:    time.Second,
				Probe: health.ProbeFunc(func(context.Context) error {
					probeCalls++
					return nil
				}),
			}))
			if err != nil {
				t.Fatal(err)
			}
			if err := routes.HandleFunc("/ready", func(writer http.ResponseWriter, request *http.Request) {
				if report := registry.Ready(request.Context()); report.Status != health.StatusReady {
					t.Errorf("ready status = %s", report.Status)
				}
				writer.WriteHeader(http.StatusNoContent)
			}); err != nil {
				t.Fatal(err)
			}
			handler, err := HTTPServer(telemetry.providers, routes, TrustedIngress, testCase.server...)
			if err != nil {
				t.Fatal(err)
			}
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://service/ready", nil))
			if probeCalls != 1 {
				t.Fatalf("probe calls = %d, want 1", probeCalls)
			}

			metrics := telemetry.flushMetrics(t)
			if metricObservationCount(metrics, vvotel.MetricHealthChecks) != 1 || metricObservationCount(metrics, vvotel.MetricHealthDuration) != 1 {
				t.Fatalf("health metric counts = %d/%d", metricObservationCount(metrics, vvotel.MetricHealthChecks), metricObservationCount(metrics, vvotel.MetricHealthDuration))
			}
			transportCount := httpServerDurationCount(metrics)
			if testCase.metricsOnly {
				transportCount = httpSLODurationCount(metrics)
			}
			wantTransport := uint64(0)
			if testCase.wantRequest && !testCase.metricsOnly {
				wantTransport = 1
			}
			if transportCount != wantTransport {
				t.Fatalf("selected transport metric count = %d, want %d", transportCount, wantTransport)
			}

			requestSpan, probeSpan, requestFound, probeFound := managementSpans(telemetry.spans.GetSpans())
			if !probeFound || requestFound != testCase.wantRequest {
				t.Fatalf("request/probe spans found = %v/%v", requestFound, probeFound)
			}
			if testCase.wantRequest {
				if probeSpan.Parent.TraceID() != requestSpan.SpanContext.TraceID() || probeSpan.Parent.SpanID() != requestSpan.SpanContext.SpanID() {
					t.Fatalf("probe parent = %v, request = %v", probeSpan.Parent, requestSpan.SpanContext)
				}
			} else if probeSpan.Parent.IsValid() {
				t.Fatalf("excluded request fabricated probe parent %v", probeSpan.Parent)
			}
		})
	}
}

func httpServerDurationAttributes(metrics metricdata.ResourceMetrics) []map[string]string {
	var result []map[string]string
	for _, scope := range metrics.ScopeMetrics {
		if scope.Scope.Name != otelhttp.ScopeName {
			continue
		}
		for _, measurement := range scope.Metrics {
			if measurement.Name != "http.server.request.duration" {
				continue
			}
			histogram, ok := measurement.Data.(metricdata.Histogram[float64])
			if !ok {
				continue
			}
			for _, point := range histogram.DataPoints {
				attributes := make(map[string]string)
				for _, item := range point.Attributes.ToSlice() {
					attributes[string(item.Key)] = item.Value.Emit()
				}
				result = append(result, attributes)
			}
		}
	}
	return result
}

func httpServerDurationCount(metrics metricdata.ResourceMetrics) uint64 {
	var count uint64
	for _, scope := range metrics.ScopeMetrics {
		if scope.Scope.Name != otelhttp.ScopeName {
			continue
		}
		for _, measurement := range scope.Metrics {
			if measurement.Name != "http.server.request.duration" {
				continue
			}
			histogram, ok := measurement.Data.(metricdata.Histogram[float64])
			if !ok {
				continue
			}
			for _, point := range histogram.DataPoints {
				count += point.Count
			}
		}
	}
	return count
}

func httpServerDurationMarkerCounts(metrics metricdata.ResourceMetrics) map[string]uint64 {
	counts := make(map[string]uint64)
	for _, scope := range metrics.ScopeMetrics {
		if scope.Scope.Name != otelhttp.ScopeName {
			continue
		}
		for _, measurement := range scope.Metrics {
			if measurement.Name != "http.server.request.duration" {
				continue
			}
			histogram, ok := measurement.Data.(metricdata.Histogram[float64])
			if !ok {
				continue
			}
			for _, point := range histogram.DataPoints {
				value, ok := point.Attributes.Value(attribute.Key(httpManagementMetricAttribute))
				if ok {
					counts[value.AsString()] += point.Count
				}
			}
		}
	}
	return counts
}

func httpSLODurationCount(metrics metricdata.ResourceMetrics) uint64 {
	var count uint64
	for _, scope := range metrics.ScopeMetrics {
		if scope.Scope.Name != otelhttp.ScopeName {
			continue
		}
		for _, measurement := range scope.Metrics {
			if measurement.Name != "http.server.request.duration" {
				continue
			}
			histogram, ok := measurement.Data.(metricdata.Histogram[float64])
			if !ok {
				continue
			}
			for _, point := range histogram.DataPoints {
				management := false
				for _, item := range point.Attributes.ToSlice() {
					management = management || string(item.Key) == httpManagementMetricAttribute && item.Value.Emit() == httpManagementMetricValue
				}
				if !management {
					count += point.Count
				}
			}
		}
	}
	return count
}

func metricObservationCount(metrics metricdata.ResourceMetrics, name string) uint64 {
	var count uint64
	for _, scope := range metrics.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if measurement.Name != name {
				continue
			}
			switch data := measurement.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					count += uint64(point.Value)
				}
			case metricdata.Histogram[float64]:
				for _, point := range data.DataPoints {
					count += point.Count
				}
			}
		}
	}
	return count
}

func managementSpans(spans tracetest.SpanStubs) (tracetest.SpanStub, tracetest.SpanStub, bool, bool) {
	var request tracetest.SpanStub
	var probe tracetest.SpanStub
	var requestFound bool
	var probeFound bool
	for _, span := range spans {
		if span.InstrumentationScope.Name == otelhttp.ScopeName {
			request = span
			requestFound = true
		}
		if span.InstrumentationScope.Name == vvotel.ScopeName && span.Name == vvotel.SpanHealth {
			probe = span
			probeFound = true
		}
	}
	return request, probe, requestFound, probeFound
}
