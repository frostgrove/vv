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

func routeOf(pattern string) authhttp.Route {
	method := AnyMethod
	if verb, rest, found := strings.Cut(pattern, " "); found {
		method = strings.ToUpper(strings.TrimSpace(verb))
		pattern = strings.TrimSpace(rest)
	}
	if slash := strings.Index(pattern, "/"); slash > 0 {
		pattern = pattern[slash:]
	}
	pattern = strings.TrimSuffix(pattern, "{$}")
	return authhttp.Route{Method: method, Path: pattern}
}
