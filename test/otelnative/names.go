package otelnative

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalidRoute = errors.New("otelnative: route method and pattern are required")
	ErrInvalidRPC   = errors.New("otelnative: RPC method must use /service/method form")
)

const (
	fallbackHTTPName          = "HTTP _OTHER"
	fallbackRPCName           = "_OTHER/_OTHER"
	maxNativeRoutes           = 4096
	maxNativeRouteLength      = 1024
	maxNativeMethodLength     = 16
	maxNativeRPCs             = 4096
	maxNativeRPCLength        = 1024
	maxNativeServerNameLength = 255
)

type Route struct {
	Method  string
	Pattern string
}

type RouteTable struct {
	names    map[string]string
	patterns map[string]struct{}
}

func NewRouteTable(routes ...Route) (RouteTable, error) {
	if len(routes) > maxNativeRoutes {
		return RouteTable{}, ErrInvalidRoute
	}
	table := RouteTable{
		names:    make(map[string]string, len(routes)),
		patterns: make(map[string]struct{}, len(routes)),
	}
	for _, route := range routes {
		method := normalizeHTTPMethod(route.Method)
		if method == "_OTHER" || !validNativeRoutePattern(route.Pattern) {
			return RouteTable{}, ErrInvalidRoute
		}
		key := routeKey(method, route.Pattern)
		if _, exists := table.names[key]; exists {
			return RouteTable{}, ErrInvalidRoute
		}
		table.names[key] = method + " " + route.Pattern
		table.patterns[route.Pattern] = struct{}{}
	}
	return table, nil
}

func (t RouteTable) SpanName(method, pattern string) string {
	name, ok := t.names[routeKey(normalizeHTTPMethod(method), pattern)]
	if !ok {
		return fallbackHTTPName
	}
	return name
}

func (t RouteTable) containsPattern(pattern string) bool {
	_, ok := t.patterns[pattern]
	return ok
}

func (t RouteTable) containsSpanName(name string) bool {
	for _, candidate := range t.names {
		if candidate == name {
			return true
		}
	}
	return false
}

func routeKey(method, pattern string) string {
	return method + "\x00" + pattern
}

func normalizeHTTPMethod(method string) string {
	if len(method) == 0 || len(method) > maxNativeMethodLength || !utf8.ValidString(method) {
		return "_OTHER"
	}
	switch strings.ToUpper(method) {
	case http.MethodConnect:
		return http.MethodConnect
	case http.MethodDelete:
		return http.MethodDelete
	case http.MethodGet:
		return http.MethodGet
	case http.MethodHead:
		return http.MethodHead
	case http.MethodOptions:
		return http.MethodOptions
	case http.MethodPatch:
		return http.MethodPatch
	case http.MethodPost:
		return http.MethodPost
	case http.MethodPut:
		return http.MethodPut
	case http.MethodTrace:
		return http.MethodTrace
	default:
		return "_OTHER"
	}
}

type RPCTable struct {
	methods map[string]string
}

func NewRPCTable(fullMethods ...string) (RPCTable, error) {
	if len(fullMethods) > maxNativeRPCs {
		return RPCTable{}, ErrInvalidRPC
	}
	table := RPCTable{methods: make(map[string]string, len(fullMethods))}
	for _, fullMethod := range fullMethods {
		if len(fullMethod) > maxNativeRPCLength || !utf8.ValidString(fullMethod) {
			return RPCTable{}, ErrInvalidRPC
		}
		name, ok := rpcSpanName(fullMethod)
		if !ok {
			return RPCTable{}, ErrInvalidRPC
		}
		if _, exists := table.methods[fullMethod]; exists {
			return RPCTable{}, ErrInvalidRPC
		}
		table.methods[fullMethod] = name
	}
	return table, nil
}

func validNativeRoutePattern(pattern string) bool {
	return pattern != "" && len(pattern) <= maxNativeRouteLength && utf8.ValidString(pattern)
}

func (t RPCTable) boundedFullMethod(fullMethod string) string {
	if _, ok := t.methods[fullMethod]; ok {
		return fullMethod
	}
	return "/" + fallbackRPCName
}

func (t RPCTable) containsSpanName(name string) bool {
	for _, candidate := range t.methods {
		if candidate == name {
			return true
		}
	}
	return false
}

func rpcSpanName(fullMethod string) (string, bool) {
	if len(fullMethod) == 0 || len(fullMethod) > maxNativeRPCLength || !utf8.ValidString(fullMethod) || !strings.HasPrefix(fullMethod, "/") {
		return "", false
	}
	name := strings.TrimPrefix(fullMethod, "/")
	parts := strings.Split(name, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return name, true
}
