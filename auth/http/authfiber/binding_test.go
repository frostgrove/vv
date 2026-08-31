package authfiber_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authfiber"
	"github.com/frostgrove/vv/auth/http/authhttp"
)

func TestTheHeadFiberGeneratesNeedsNoDeclaration(t *testing.T) {
	app := mountedApp(func(r fiber.Router) { r.Get("/things", nothing) })

	if _, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)); err != nil {
		t.Fatalf("serving one request: %v", err)
	}

	var head bool
	for _, route := range app.GetRoutes(true) {
		if route.Method == http.MethodHead && route.Path == "/api/v1/things" {
			head = true
		}
	}
	if !head {
		t.Fatal("Fiber no longer generates a HEAD for a GET, so the exemption below proves nothing")
	}

	if err := authfiber.Verify(app, []authhttp.Endpoint{
		authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read")),
	}, authhttp.UnderPrefix(apiPrefix)); err != nil {
		t.Fatalf("the HEAD Fiber generated was demanded of the module: %v", err)
	}
}

func TestAMiddlewareIsNotARouteToDeclare(t *testing.T) {
	app := fiber.New()
	api := app.Group(apiPrefix)
	api.Use(func(c fiber.Ctx) error { return c.Next() })
	api.Get("/things", nothing)

	if err := authfiber.Verify(app, []authhttp.Endpoint{
		authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read")),
	}, authhttp.UnderPrefix(apiPrefix)); err != nil {
		t.Fatalf("a middleware was treated as an endpoint that must declare access: %v", err)
	}
}
