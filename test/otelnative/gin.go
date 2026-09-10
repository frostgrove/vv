package otelnative

import (
	"errors"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

var ErrInvalidServerName = errors.New("otelnative: server name is required")

func GinMiddleware(providers Providers, routes RouteTable, mode IngressMode, serverName string) (gin.HandlerFunc, error) {
	if err := providers.validate(); err != nil {
		return nil, err
	}
	if err := mode.validate(); err != nil {
		return nil, err
	}
	if serverName == "" {
		return nil, ErrInvalidServerName
	}
	tracerProvider := scopedTracerProvider{
		TracerProvider: providers.Tracer,
		scope:          otelgin.ScopeName,
		public:         mode == PublicIngress,
		normalizeName: func(name string) string {
			if routes.containsSpanName(name) {
				return name
			}
			return fallbackHTTPName
		},
	}
	return otelgin.Middleware(
		serverName,
		otelgin.WithTracerProvider(tracerProvider),
		otelgin.WithMeterProvider(providers.Meter),
		otelgin.WithPropagators(propagatorFor(mode)),
		otelgin.WithSpanNameFormatter(func(ctx *gin.Context) string {
			return routes.SpanName(ctx.Request.Method, ctx.FullPath())
		}),
	), nil
}
