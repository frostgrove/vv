package authfiber

import (
	"net/http"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth/http/authhttp"
)

// Fiber answers HEAD for every GET and does not export the flag that says which
// HEAD it invented, so the generated half is recognised by its shape: a HEAD
// whose path also carries a GET. Everything else is surface and must declare.
func Routes(app *fiber.App) []authhttp.Route {
	registered := app.GetRoutes(true)

	gets := make(map[string]struct{}, len(registered))
	for _, route := range registered {
		if route.Method == http.MethodGet {
			gets[route.Path] = struct{}{}
		}
	}

	out := make([]authhttp.Route, 0, len(registered))
	for _, route := range registered {
		if route.Method == http.MethodHead {
			if _, generated := gets[route.Path]; generated {
				continue
			}
		}
		out = append(out, authhttp.Route{Method: route.Method, Path: route.Path})
	}
	return out
}

func Verify(app *fiber.App, declared []authhttp.Endpoint, options ...authhttp.VerifyOption) error {
	return authhttp.Verify(declared, Routes(app), options...)
}

func VerifyAreas(app *fiber.App, areas ...authhttp.Area) error {
	return authhttp.VerifyAreas(Routes(app), areas...)
}
