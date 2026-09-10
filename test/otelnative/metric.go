package otelnative

import (
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

var (
	httpServerMetricAttributes = map[attribute.Key]struct{}{
		"http.request.method":         {},
		"http.response.status_code":   {},
		"http.route":                  {},
		httpManagementMetricAttribute: {},
	}
	httpActiveMetricAttributes = map[attribute.Key]struct{}{
		"http.request.method": {},
	}
	httpClientMetricAttributes = map[attribute.Key]struct{}{
		"http.request.method":       {},
		"http.response.status_code": {},
	}
	rpcMetricAttributes = map[attribute.Key]struct{}{
		"rpc.system.name":          {},
		"rpc.method":               {},
		"rpc.response.status_code": {},
	}
)

type transportMetricView struct {
	scope      string
	instrument string
	keys       map[attribute.Key]struct{}
}

func TransportMetricOptions(policy TraceProjectionPolicy) ([]sdkmetric.Option, error) {
	compiled, err := compileTracePolicy(policy)
	if err != nil {
		return nil, err
	}
	specs := []transportMetricView{
		{otelhttp.ScopeName, "http.server.request.body.size", httpServerMetricAttributes},
		{otelhttp.ScopeName, "http.server.response.body.size", httpServerMetricAttributes},
		{otelhttp.ScopeName, "http.server.request.duration", httpServerMetricAttributes},
		{otelhttp.ScopeName, "http.client.request.body.size", httpClientMetricAttributes},
		{otelhttp.ScopeName, "http.client.request.duration", httpClientMetricAttributes},
		{otelgin.ScopeName, "http.server.request.body.size", httpServerMetricAttributes},
		{otelgin.ScopeName, "http.server.response.body.size", httpServerMetricAttributes},
		{otelgin.ScopeName, "http.server.request.duration", httpServerMetricAttributes},
		{fiberScopeName, "http.server.active_requests", httpActiveMetricAttributes},
		{fiberScopeName, "http.server.request.body.size", httpServerMetricAttributes},
		{fiberScopeName, "http.server.response.body.size", httpServerMetricAttributes},
		{fiberScopeName, "http.server.request.duration", httpServerMetricAttributes},
		{otelgrpc.ScopeName, "rpc.client.call.duration", rpcMetricAttributes},
		{otelgrpc.ScopeName, "rpc.server.call.duration", rpcMetricAttributes},
	}
	options := make([]sdkmetric.Option, 0, len(specs))
	for _, spec := range specs {
		filter := transportMetricFilter(spec.scope, spec.keys, compiled)
		metricSpec := transportMetricSpecs(spec.scope, compiled)[spec.instrument]
		options = append(options, sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{
				Name:  spec.instrument,
				Scope: instrumentation.Scope{Name: spec.scope},
			},
			nativeMetricStream(filter, metricSpec),
		)))
	}
	return options, nil
}

func transportMetricFilter(scope string, keys map[attribute.Key]struct{}, policy compiledTracePolicy) attribute.Filter {
	return func(candidate attribute.KeyValue) bool {
		if _, allowed := keys[candidate.Key]; !allowed {
			return false
		}
		projected := projectTransportMetricAttributes(scope, []attribute.KeyValue{candidate}, policy)
		return len(projected) == 1 && projected[0].Key == candidate.Key && projected[0].Value.Type() == candidate.Value.Type() && projected[0].Value.Emit() == candidate.Value.Emit()
	}
}
