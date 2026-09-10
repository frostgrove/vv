package otelnative

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
)

var (
	ErrNilRoutes    = errors.New("otelnative: HTTP routes are required")
	ErrRoutesSealed = errors.New("otelnative: HTTP routes are sealed")
)

const (
	httpManagementMetricAttribute = "vv.http.management"
	httpManagementMetricValue     = "true"
	httpApplicationMetricValue    = "false"
)

type HTTPRoutes struct {
	mu      sync.Mutex
	mux     *http.ServeMux
	entries map[string]*registeredHTTPHandler
	sealed  bool
}

type HTTPServerOption func(*httpServerSettings)

type httpServerSettings struct {
	excludeManagementSignals bool
}

func ExcludeManagementSignals() HTTPServerOption {
	return func(settings *httpServerSettings) {
		settings.excludeManagementSignals = true
	}
}

func NewHTTPRoutes() *HTTPRoutes {
	return &HTTPRoutes{
		mux:     http.NewServeMux(),
		entries: make(map[string]*registeredHTTPHandler),
	}
}

func (r *HTTPRoutes) Handle(pattern string, handler http.Handler) (err error) {
	if r == nil {
		return ErrNilRoutes
	}
	if !validNativeRoutePattern(pattern) || nilInterface(handler) {
		return ErrInvalidRoute
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return ErrRoutesSealed
	}
	r.initialize()
	if len(r.entries) >= maxNativeRoutes {
		return ErrInvalidRoute
	}
	registered := &registeredHTTPHandler{
		handler: handler,
		token:   &routeToken{},
	}
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = recover()
		err = ErrInvalidRoute
	}()
	r.mux.Handle(pattern, registered)
	r.entries[pattern] = registered
	completed = true
	return nil
}

func (r *HTTPRoutes) HandleFunc(pattern string, handler http.HandlerFunc) error {
	return r.Handle(pattern, handler)
}

func HTTPServer(providers Providers, routes *HTTPRoutes, mode IngressMode, options ...HTTPServerOption) (http.Handler, error) {
	if err := providers.validate(); err != nil {
		return nil, err
	}
	if err := mode.validate(); err != nil {
		return nil, err
	}
	if routes == nil {
		return nil, ErrNilRoutes
	}
	return runNativeAssembly(func() (http.Handler, error) {
		settings := httpServerSettings{}
		for _, option := range options {
			if option != nil {
				option(&settings)
			}
		}
		routes.mu.Lock()
		defer routes.mu.Unlock()
		if routes.sealed {
			return nil, ErrRoutesSealed
		}
		routes.initialize()
		entries := make(map[string]*registeredHTTPHandler, len(routes.entries))
		names := make(map[string]string, len(routes.entries))
		for pattern, handler := range routes.entries {
			entries[pattern] = handler
			names[pattern] = pattern
		}
		policy := &httpRoutePolicy{mux: routes.mux, entries: entries, names: names}
		serverOptions := []otelhttp.Option{
			otelhttp.WithTracerProvider(providers.Tracer),
			otelhttp.WithMeterProvider(providers.Meter),
			otelhttp.WithPropagators(propagatorFor(mode)),
			otelhttp.WithPublicEndpointFn(func(*http.Request) bool { return mode == PublicIngress }),
			otelhttp.WithSpanNameFormatter(policy.spanName),
			otelhttp.WithMetricAttributesFn(policy.metricAttributes),
		}
		if settings.excludeManagementSignals {
			serverOptions = append(serverOptions, otelhttp.WithFilter(policy.includeSignals))
		}
		handler := otelhttp.NewHandler(policy, fallbackHTTPName, serverOptions...)
		if nilInterface(handler) {
			return nil, ErrNativeAssembly
		}
		routes.sealed = true
		return handler, nil
	})
}

func (r *HTTPRoutes) initialize() {
	if r.mux == nil {
		r.mux = http.NewServeMux()
	}
	if r.entries == nil {
		r.entries = make(map[string]*registeredHTTPHandler)
	}
}

func HTTPTransport(providers Providers, base http.RoundTripper) (http.RoundTripper, error) {
	if err := providers.validate(); err != nil {
		return nil, err
	}
	if nilInterface(base) {
		base = nil
	}
	return runNativeAssembly(func() (http.RoundTripper, error) {
		return otelhttp.NewTransport(
			base,
			otelhttp.WithTracerProvider(providers.Tracer),
			otelhttp.WithMeterProvider(providers.Meter),
			otelhttp.WithPropagators(propagation.TraceContext{}),
			otelhttp.WithSpanNameFormatter(func(_ string, request *http.Request) string {
				return "HTTP " + normalizeHTTPMethod(request.Method)
			}),
		), nil
	})
}

type routeToken struct{}

type dispatchState struct {
	executed *routeToken
}

type dispatchStateKey struct{}

type registeredHTTPHandler struct {
	handler http.Handler
	token   *routeToken
}

func (h *registeredHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	state, _ := request.Context().Value(dispatchStateKey{}).(*dispatchState)
	if state != nil {
		state.executed = h.token
	}
	h.handler.ServeHTTP(writer, request)
}

type httpRoutePolicy struct {
	mux     *http.ServeMux
	entries map[string]*registeredHTTPHandler
	names   map[string]string
}

func (p *httpRoutePolicy) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	selected, pattern := p.mux.Handler(request)
	registered, declared := p.entries[pattern]
	selectedRegistered, selectedIsRegistered := selected.(*registeredHTTPHandler)
	admitted := declared && selectedIsRegistered && selectedRegistered == registered
	state := &dispatchState{}
	withState := request.WithContext(context.WithValue(request.Context(), dispatchStateKey{}, state))
	*request = *withState
	p.mux.ServeHTTP(writer, request)
	if admitted && state.executed == registered.token {
		request.Pattern = pattern
	} else {
		request.Pattern = ""
	}
}

func (p *httpRoutePolicy) spanName(_ string, request *http.Request) string {
	state, _ := request.Context().Value(dispatchStateKey{}).(*dispatchState)
	if state == nil || state.executed == nil {
		return fallbackHTTPName
	}
	name, ok := p.names[request.Pattern]
	if !ok {
		return fallbackHTTPName
	}
	return name
}

func (p *httpRoutePolicy) includeSignals(request *http.Request) bool {
	selected, pattern := p.mux.Handler(request)
	if pattern != "/live" && pattern != "/ready" {
		return true
	}
	registered, declared := p.entries[pattern]
	selectedRegistered, selectedIsRegistered := selected.(*registeredHTTPHandler)
	return !(declared && selectedIsRegistered && selectedRegistered == registered)
}

func (p *httpRoutePolicy) metricAttributes(request *http.Request) []attribute.KeyValue {
	value := httpApplicationMetricValue
	if request.Pattern == "/live" || request.Pattern == "/ready" {
		value = httpManagementMetricValue
	}
	return []attribute.KeyValue{attribute.String(httpManagementMetricAttribute, value)}
}
