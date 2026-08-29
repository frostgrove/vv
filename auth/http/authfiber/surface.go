package authfiber

import (
	"net/http"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth/http/authhttp"
)

// Routes answers what this application actually serves, for [authhttp.Verify]
// to compare against what the modules declared.
//
// It reads Fiber's own routing table rather than a list kept alongside it. That
// is the entire point of the gate: a declaration is only worth checking against
// a second statement that was arrived at independently, and a recording wrapper
// around registration would agree with the declaration exactly when both were
// wrong.
//
// HEAD and OPTIONS are left out. Fiber registers a HEAD for every GET itself, so
// demanding a declaration for one would be demanding a module declare something
// it did not write — and a HEAD-only route cannot be declared here for the same
// reason it cannot be mounted alone. OPTIONS is a CORS middleware's answer, not
// an endpoint.
//
// The `true` is Fiber's filterUseOption: a middleware installed with Use is not
// a route and has no access to declare.
func Routes(app *fiber.App) []authhttp.Route {
	registered := app.GetRoutes(true)
	out := make([]authhttp.Route, 0, len(registered))
	for _, route := range registered {
		if route.Method == http.MethodHead || route.Method == http.MethodOptions {
			continue
		}
		out = append(out, authhttp.Route{Method: route.Method, Path: route.Path})
	}
	return out
}

// Verify is the whole gate in one call, for the ordinary case of an application
// that mounts everything under one prefix.
//
//	if err := authfiber.Verify(app, declared, authhttp.UnderPrefix("/api/v1")); err != nil {
//		return err
//	}
//
// Returning the error rather than logging it is the difference between a
// deployment that refuses to start and one that serves an undeclared endpoint
// with a warning nobody reads.
func Verify(app *fiber.App, declared []authhttp.Endpoint, options ...authhttp.VerifyOption) error {
	return authhttp.Verify(declared, Routes(app), options...)
}
