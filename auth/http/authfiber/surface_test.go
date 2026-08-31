package authfiber_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authfiber"
	"github.com/frostgrove/vv/auth/http/authhttp"
)

const apiPrefix = "/api/v1"

func mountedApp(register func(fiber.Router)) *fiber.App {
	app := fiber.New()
	register(app.Group(apiPrefix))
	return app
}

func nothing(fiber.Ctx) error { return nil }

func TestTheGatePassesWhenEveryMountedRouteIsDeclared(t *testing.T) {
	app := mountedApp(func(r fiber.Router) {
		r.Post("/auth/login", nothing)
		r.Get("/users/:id", nothing)
	})

	err := authfiber.Verify(app, []authhttp.Endpoint{
		authhttp.Public(http.MethodPost, "/auth/login", "there is no credential to present yet"),
		authhttp.Requires(http.MethodGet, "/users/:id", auth.Permission("user.read")),
	}, authhttp.UnderPrefix(apiPrefix))
	if err != nil {
		t.Fatalf("a router that matches its declaration was rejected: %v", err)
	}
}

func TestTheGateRefusesARouteNobodyDeclared(t *testing.T) {
	app := mountedApp(func(r fiber.Router) {
		r.Post("/auth/login", nothing)

		r.Delete("/users/:id", nothing)
	})

	err := authfiber.Verify(app, []authhttp.Endpoint{
		authhttp.Public(http.MethodPost, "/auth/login", "there is no credential to present yet"),
	}, authhttp.UnderPrefix(apiPrefix))
	if err == nil {
		t.Fatal("an endpoint nobody declared was accepted; the whole point of this check is that it is not")
	}
	if !strings.Contains(err.Error(), "DELETE /api/v1/users/:id") {
		t.Fatalf("the failure does not name the undeclared endpoint: %v", err)
	}
}

func TestTheGateRefusesADeclarationThatMountsNothing(t *testing.T) {
	app := mountedApp(func(r fiber.Router) { r.Post("/auth/login", nothing) })

	err := authfiber.Verify(app, []authhttp.Endpoint{
		authhttp.Public(http.MethodPost, "/auth/login", "there is no credential to present yet"),
		authhttp.Requires(http.MethodGet, "/users/:id", auth.Permission("user.read")),
	}, authhttp.UnderPrefix(apiPrefix))
	if err == nil {
		t.Fatal("a declaration whose route no longer exists was accepted")
	}
	if !strings.Contains(err.Error(), "declared and mounts nothing") {
		t.Fatalf("the failure does not say the declaration is stale: %v", err)
	}
}
