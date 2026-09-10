package otelnative

import (
	"errors"

	fiberotel "github.com/gofiber/contrib/v3/otel"
	"github.com/gofiber/fiber/v3"
)

var ErrInvalidPort = errors.New("otelnative: port must be between 1 and 65535")

const fiberScopeName = "github.com/gofiber/contrib/v3/otel"

func FiberMiddleware(providers Providers, routes RouteTable, mode IngressMode, port int) (fiber.Handler, error) {
	if err := providers.validate(); err != nil {
		return nil, err
	}
	if err := mode.validate(); err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 {
		return nil, ErrInvalidPort
	}
	tracerProvider := scopedTracerProvider{
		TracerProvider: providers.Tracer,
		scope:          fiberScopeName,
		public:         mode == PublicIngress,
		normalizeName: func(name string) string {
			if routes.containsSpanName(name) {
				return name
			}
			return fallbackHTTPName
		},
	}
	return runNativeAssembly(func() (fiber.Handler, error) {
		return fiberotel.Middleware(
			fiberotel.WithTracerProvider(tracerProvider),
			fiberotel.WithMeterProvider(providers.Meter),
			fiberotel.WithPropagators(propagatorFor(mode)),
			fiberotel.WithPort(port),
			fiberotel.WithClientIP(false),
			fiberotel.WithSpanNameFormatter(func(ctx fiber.Ctx) string {
				return routes.SpanName(ctx.Method(), ctx.Route().Path)
			}),
		), nil
	})
}
