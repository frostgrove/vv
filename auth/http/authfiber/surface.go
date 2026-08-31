package authfiber

import (
	"net/http"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth/http/authhttp"
)

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

func Verify(app *fiber.App, declared []authhttp.Endpoint, options ...authhttp.VerifyOption) error {
	return authhttp.Verify(declared, Routes(app), options...)
}
