package authnet

import (
	"net/http"
	"strings"

	"github.com/frostgrove/vv/auth/http/authhttp"
)

type Surface struct {
	mux    *http.ServeMux
	routes []authhttp.Route
}

const AnyMethod = "*"

func Over(mux *http.ServeMux) *Surface {
	if mux == nil {
		mux = http.NewServeMux()
	}
	return &Surface{mux: mux}
}

func (this *Surface) Mux() *http.ServeMux { return this.mux }

func (this *Surface) Handler() http.Handler { return sealed{this.mux} }

type sealed struct{ http.Handler }

func (this *Surface) Handle(pattern string, handler http.Handler) {
	this.mux.Handle(pattern, handler)
	this.routes = append(this.routes, routeOf(pattern))
}

func (this *Surface) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	this.mux.HandleFunc(pattern, handler)
	this.routes = append(this.routes, routeOf(pattern))
}

func (this *Surface) Routes() []authhttp.Route {
	return append([]authhttp.Route(nil), this.routes...)
}

func (this *Surface) Verify(declared []authhttp.Endpoint, options ...authhttp.VerifyOption) error {
	return authhttp.Verify(declared, this.Routes(), options...)
}

func (this *Surface) VerifyAreas(areas ...authhttp.Area) error {
	return authhttp.VerifyAreas(this.Routes(), areas...)
}

// A net/http pattern may carry a host — "admin.example.com/reports" is a
// different route from "/reports", and only the first one answers on that host.
// Stripping the host made the two the same route to the gate, so a declaration
// written for "/reports" silently covered the admin host's endpoint as well, and
// an endpoint that was never declared at all inherited whatever the bare path
// had been given.
//
// The host is therefore kept. The gate has no host of its own to compare against
// ([[D-073]] names a method and a path), so a host-scoped route simply does not
// match a bare declaration: it reads as undeclared and the start-up gate refuses
// it. A deployment that means to mount one declares it with the host in the
// path, which is the only form that says which route it is talking about.
func routeOf(pattern string) authhttp.Route {
	method := AnyMethod
	if verb, rest, found := strings.Cut(pattern, " "); found {
		method = strings.ToUpper(strings.TrimSpace(verb))
		pattern = strings.TrimSpace(rest)
	}
	pattern = strings.TrimSuffix(pattern, "{$}")
	return authhttp.Route{Method: method, Path: pattern}
}
