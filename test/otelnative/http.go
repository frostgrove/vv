package otelnative

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
)

var (
	ErrNilRoutes    = errors.New("otelnative: HTTP routes are required")
	ErrRoutesSealed = errors.New("otelnative: HTTP routes are sealed")
)

type HTTPRoutes struct {
	mu      sync.Mutex
	mux     *http.ServeMux
	entries map[string]*registeredHTTPHandler
	sealed  bool
}

func NewHTTPRoutes() *HTTPRoutes {
	return &HTTPRoutes{
		mux:     http.NewServeMux(),
		entries: make(map[string]*registeredHTTPHandler),
	}
}

func (r *HTTPRoutes) Handle(pattern string, handler http.Handler) error {
	if r == nil {
		return ErrNilRoutes
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return ErrRoutesSealed
	}
	registered := &registeredHTTPHandler{
		handler: handler,
		token:   &routeToken{},
	}
	r.mux.Handle(pattern, registered)
	r.entries[pattern] = registered
	return nil
}

func (r *HTTPRoutes) HandleFunc(pattern string, handler http.HandlerFunc) error {
	return r.Handle(pattern, handler)
}

func HTTPServer(providers Providers, routes *HTTPRoutes, mode IngressMode) (http.Handler, error) {
	if err := providers.validate(); err != nil {
		return nil, err
	}
	if err := mode.validate(); err != nil {
		return nil, err
	}
	policy, err := routes.seal()
	if err != nil {
		return nil, err
	}
	return otelhttp.NewHandler(
		policy,
		fallbackHTTPName,
		otelhttp.WithTracerProvider(providers.Tracer),
		otelhttp.WithMeterProvider(providers.Meter),
		otelhttp.WithPropagators(propagatorFor(mode)),
		otelhttp.WithPublicEndpointFn(func(*http.Request) bool { return mode == PublicIngress }),
		otelhttp.WithSpanNameFormatter(policy.spanName),
	), nil
}

func HTTPTransport(providers Providers, base http.RoundTripper) (http.RoundTripper, error) {
	if err := providers.validate(); err != nil {
		return nil, err
	}
	return otelhttp.NewTransport(
		base,
		otelhttp.WithTracerProvider(providers.Tracer),
		otelhttp.WithMeterProvider(providers.Meter),
		otelhttp.WithPropagators(propagation.TraceContext{}),
		otelhttp.WithSpanNameFormatter(func(_ string, request *http.Request) string {
			return "HTTP " + normalizeHTTPMethod(request.Method)
		}),
	), nil
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

func (r *HTTPRoutes) seal() (*httpRoutePolicy, error) {
	if r == nil {
		return nil, ErrNilRoutes
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sealed = true
	entries := make(map[string]*registeredHTTPHandler, len(r.entries))
	names := make(map[string]string, len(r.entries))
	for pattern, handler := range r.entries {
		entries[pattern] = handler
		names[pattern] = pattern
	}
	return &httpRoutePolicy{mux: r.mux, entries: entries, names: names}, nil
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
	if !admitted || state.executed != registered.token || request.Pattern != pattern {
		request.Pattern = ""
	}
}

func (p *httpRoutePolicy) spanName(_ string, request *http.Request) string {
	name, ok := p.names[request.Pattern]
	if !ok {
		return fallbackHTTPName
	}
	return name
}
