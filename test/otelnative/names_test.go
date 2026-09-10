package otelnative

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestNativeRouteConfigurationRejectsHostileInputs(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	routes := NewHTTPRoutes()
	for _, test := range []struct {
		pattern string
		handler http.Handler
	}{
		{pattern: "[", handler: handler},
		{pattern: "/nil"},
		{pattern: "/typed-nil", handler: http.HandlerFunc(nil)},
		{pattern: "/" + strings.Repeat("x", maxNativeRouteLength), handler: handler},
		{pattern: string([]byte{'/', 0xff}), handler: handler},
	} {
		if err := routes.Handle(test.pattern, test.handler); !errors.Is(err, ErrInvalidRoute) {
			t.Fatalf("pattern %q error=%v", test.pattern, err)
		}
	}
	if err := routes.Handle("/duplicate", handler); err != nil {
		t.Fatal(err)
	}
	if err := routes.Handle("/duplicate", handler); !errors.Is(err, ErrInvalidRoute) {
		t.Fatalf("duplicate error=%v", err)
	}
}

func TestNativeNameTablesHaveClosedConfigurationBounds(t *testing.T) {
	routes := make([]Route, maxNativeRoutes+1)
	for index := range routes {
		routes[index] = Route{Method: http.MethodGet, Pattern: "/" + strconv.Itoa(index)}
	}
	if _, err := NewRouteTable(routes...); !errors.Is(err, ErrInvalidRoute) {
		t.Fatalf("route count error=%v", err)
	}
	if _, err := NewRouteTable(
		Route{Method: http.MethodGet, Pattern: "/same"},
		Route{Method: http.MethodGet, Pattern: "/same"},
	); !errors.Is(err, ErrInvalidRoute) {
		t.Fatalf("duplicate route error=%v", err)
	}
	if _, err := NewRouteTable(Route{Method: http.MethodGet, Pattern: "/" + strings.Repeat("x", maxNativeRouteLength)}); !errors.Is(err, ErrInvalidRoute) {
		t.Fatalf("route length error=%v", err)
	}
	if _, err := NewRouteTable(Route{Method: strings.Repeat("G", maxNativeMethodLength+1), Pattern: "/route"}); !errors.Is(err, ErrInvalidRoute) {
		t.Fatalf("method length error=%v", err)
	}
	if _, err := NewRouteTable(Route{Method: string([]byte{0xff}), Pattern: "/route"}); !errors.Is(err, ErrInvalidRoute) {
		t.Fatalf("method UTF-8 error=%v", err)
	}

	methods := make([]string, maxNativeRPCs+1)
	for index := range methods {
		methods[index] = "/service/method" + strconv.Itoa(index)
	}
	if _, err := NewRPCTable(methods...); !errors.Is(err, ErrInvalidRPC) {
		t.Fatalf("RPC count error=%v", err)
	}
	if _, err := NewRPCTable("/service/method", "/service/method"); !errors.Is(err, ErrInvalidRPC) {
		t.Fatalf("duplicate RPC error=%v", err)
	}
	if _, err := NewRPCTable("/service/" + strings.Repeat("x", maxNativeRPCLength)); !errors.Is(err, ErrInvalidRPC) {
		t.Fatalf("RPC length error=%v", err)
	}
	if _, err := NewRPCTable("/service/" + string([]byte{0xff})); !errors.Is(err, ErrInvalidRPC) {
		t.Fatalf("RPC UTF-8 error=%v", err)
	}
}
