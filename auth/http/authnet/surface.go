package authnet

import (
	"net/http"
	"strings"

	"github.com/frostgrove/vv/auth/http/authhttp"
)

// A Surface is a mux that remembers what was registered on it.
//
// This is the one place the three HTTP bindings do not do the same thing, and
// the difference is not a choice. authfiber and authgin read the router's own
// table, so the declaration is compared against something arrived at
// independently. An [http.ServeMux] cannot be asked what it holds — there is no
// accessor and no way to walk it — so the second statement has to be recorded as
// it is made.
//
// What that costs is real and worth stating plainly: a handler registered
// straight on the wrapped mux is invisible here, so on net/http the gate catches
// a stale declaration and an endpoint added *through the Surface* without one,
// and cannot catch an endpoint that went around it. Registering through the
// Surface is therefore the thing to keep true; [Surface.Mux] exists for handing
// the finished mux to a server, not for registering more on it.
type Surface struct {
	mux    *http.ServeMux
	routes []authhttp.Route
}

// AnyMethod is the method recorded for a pattern that names none.
//
// A ServeMux pattern without a verb answers every verb, which is a decision and
// not an omission — so it is declared as one, with its own spelling, rather than
// silently matching the GET somebody meant:
//
//	authhttp.Requires(authnet.AnyMethod, "/things/", perm)
const AnyMethod = "*"

// Over starts recording on a mux. A nil mux gets a new one, so the ordinary case
// is one line.
func Over(mux *http.ServeMux) *Surface {
	if mux == nil {
		mux = http.NewServeMux()
	}
	return &Surface{mux: mux}
}

// Mux answers the mux being recorded, for handing to a server.
func (this *Surface) Mux() *http.ServeMux { return this.mux }

// Handle registers a handler and records the pattern.
func (this *Surface) Handle(pattern string, handler http.Handler) {
	this.mux.Handle(pattern, handler)
	this.routes = append(this.routes, routeOf(pattern))
}

// HandleFunc registers a handler function and records the pattern.
func (this *Surface) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	this.mux.HandleFunc(pattern, handler)
	this.routes = append(this.routes, routeOf(pattern))
}

// Routes answers what was registered, for [authhttp.Verify] to compare against
// what the modules declared.
func (this *Surface) Routes() []authhttp.Route {
	return append([]authhttp.Route(nil), this.routes...)
}

// Verify is the whole gate in one call, for the ordinary case of an application
// that mounts everything under one prefix.
//
// Returning the error rather than logging it is the difference between a
// deployment that refuses to start and one that serves an undeclared endpoint
// with a warning nobody reads.
func (this *Surface) Verify(declared []authhttp.Endpoint, options ...authhttp.VerifyOption) error {
	return authhttp.Verify(declared, this.Routes(), options...)
}

// routeOf splits a ServeMux pattern into the method and the path.
//
// The syntax is `[METHOD ][HOST]/[PATH]`: a space means everything before it is
// the verb, and the path starts at the first slash — so a pattern registered for
// one host is compared on its path, which is what a declaration is about.
//
// A trailing `{$}` is dropped. It is how a pattern says "this path exactly" and
// not part of the path, and leaving it in would make the declaration spell an
// anchor rather than a route.
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
