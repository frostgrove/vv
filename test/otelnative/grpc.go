package otelnative

import (
	"context"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc/stats"
)

func GRPCServerStats(providers Providers, methods RPCTable, mode IngressMode) (stats.Handler, error) {
	if err := providers.validate(); err != nil {
		return nil, err
	}
	if err := mode.validate(); err != nil {
		return nil, err
	}
	options := []otelgrpc.Option{
		otelgrpc.WithTracerProvider(providers.Tracer),
		otelgrpc.WithMeterProvider(providers.Meter),
		otelgrpc.WithPropagators(propagatorFor(mode)),
	}
	if mode == PublicIngress {
		options = append(options, otelgrpc.WithPublicEndpoint())
	}
	return runNativeAssembly(func() (stats.Handler, error) {
		return boundedStatsHandler{
			Handler: otelgrpc.NewServerHandler(options...),
			methods: methods,
		}, nil
	})
}

func GRPCClientStats(providers Providers, methods RPCTable) (stats.Handler, error) {
	if err := providers.validate(); err != nil {
		return nil, err
	}
	return runNativeAssembly(func() (stats.Handler, error) {
		return boundedStatsHandler{
			Handler: otelgrpc.NewClientHandler(
				otelgrpc.WithTracerProvider(providers.Tracer),
				otelgrpc.WithMeterProvider(providers.Meter),
				otelgrpc.WithPropagators(propagation.TraceContext{}),
			),
			methods: methods,
		}, nil
	})
}

type boundedStatsHandler struct {
	stats.Handler
	methods RPCTable
}

func (h boundedStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	if info == nil {
		return h.Handler.TagRPC(ctx, info)
	}
	bounded := *info
	bounded.FullMethodName = h.methods.boundedFullMethod(info.FullMethodName)
	return h.Handler.TagRPC(ctx, &bounded)
}
