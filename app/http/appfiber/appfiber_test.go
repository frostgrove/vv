package appfiber_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"

	"github.com/frostgrove/vv/app/http/appfiber"
	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
)

const prefix = "/api/v1"

// a route is one contributor, with whatever it mounts and whatever it declares
// held apart — because the whole point of the gate is that the two can disagree.
type route struct {
	mount   func(fiber.Router)
	declare []authhttp.Endpoint
}

func (this route) Mount(r fiber.Router)        { this.mount(r) }
func (this route) Access() []authhttp.Endpoint { return this.declare }
func newRoute(r route) func() appfiber.Route   { return func() appfiber.Route { return r } }
func handler(fiber.Ctx) error                  { return nil }
func provide(constructors ...any) fx.Option    { return fx.Provide(constructors...) }
func serving() fx.Option                       { return appfiber.Serving(appfiber.Spec{Prefix: prefix}) }
func newFiber() *fiber.App                     { return fiber.New() }
func request(t *testing.T, a *fiber.App, target string) int {
	t.Helper()
	response, err := a.Test(httptest.NewRequest(http.MethodGet, target, nil))
	if err != nil {
		t.Fatalf("serving %s: %v", target, err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode
}

func TestAContributedRouteIsMountedUnderThePrefix(t *testing.T) {
	var mounted *fiber.App
	err := fx.New(
		provide(newFiber),
		provide(appfiber.AsRoute(newRoute(route{
			mount:   func(r fiber.Router) { r.Get("/things", handler) },
			declare: []authhttp.Endpoint{authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read"))},
		}))),
		serving(),
		fx.Populate(&mounted),
	).Err()
	if err != nil {
		t.Fatalf("a well-formed module did not start: %v", err)
	}

	if got := request(t, mounted, "/api/v1/things"); got == http.StatusNotFound {
		t.Fatal("the route was not mounted under the prefix, so a module's paths depend on where the transport put it")
	}
}

// The gate is the reason a module declares anything at all. A route added
// without a check looks exactly like a route that is deliberately public.
func TestStartUpFailsWhenARouteDeclaresNothing(t *testing.T) {
	err := fx.New(
		provide(newFiber),
		provide(appfiber.AsRoute(newRoute(route{
			mount: func(r fiber.Router) {
				r.Get("/things", handler)
				// The one somebody added in a hurry.
				r.Delete("/things/:id", handler)
			},
			declare: []authhttp.Endpoint{authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read"))},
		}))),
		serving(),
	).Err()

	if err == nil {
		t.Fatal("an application with an undeclared endpoint started; the whole point of the gate is that it does not")
	}
	if !strings.Contains(err.Error(), "DELETE /api/v1/things/:id") {
		t.Fatalf("the failure does not name the undeclared endpoint: %v", err)
	}
}

// The other half, and the one that is easy to leave out: a declaration that
// outlives its handler is what makes the list look complete while it covers less
// every month.
func TestStartUpFailsWhenADeclarationMountsNothing(t *testing.T) {
	err := fx.New(
		provide(newFiber),
		provide(appfiber.AsRoute(newRoute(route{
			mount: func(r fiber.Router) { r.Get("/things", handler) },
			declare: []authhttp.Endpoint{
				authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read")),
				authhttp.Requires(http.MethodGet, "/gone", auth.Permission("thing.read")),
			},
		}))),
		serving(),
	).Err()

	if err == nil {
		t.Fatal("a declaration whose route no longer exists was accepted")
	}
	if !strings.Contains(err.Error(), "declared and mounts nothing") {
		t.Fatalf("the failure does not say the declaration is stale: %v", err)
	}
}

// A value group has no order of its own, and "the guard runs before the handler"
// decided by which provider fx happened to visit first is a security property
// decided by luck — one that every test mounting a single module still passes.
func TestMiddlewareRunsInTheOrderItDeclared(t *testing.T) {
	var ran []string
	records := func(name string, order int) func() appfiber.Middleware {
		return func() appfiber.Middleware {
			return appfiber.Middleware{Name: name, Order: order, Handler: func(c fiber.Ctx) error {
				ran = append(ran, name)
				return c.Next()
			}}
		}
	}

	var mounted *fiber.App
	err := fx.New(
		provide(newFiber),
		// Registered in the wrong order on purpose: what decides the chain has
		// to be the number, not the order fx walked the group in.
		provide(appfiber.AsMiddleware(records("handler-assumes-a-caller", 200))),
		provide(appfiber.AsMiddleware(records("guard", 100))),
		provide(appfiber.AsRoute(newRoute(route{
			mount:   func(r fiber.Router) { r.Get("/things", handler) },
			declare: []authhttp.Endpoint{authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read"))},
		}))),
		serving(),
		fx.Populate(&mounted),
	).Err()
	if err != nil {
		t.Fatalf("the application did not start: %v", err)
	}

	request(t, mounted, "/api/v1/things")

	if want := []string{"guard", "handler-assumes-a-caller"}; !slices.Equal(ran, want) {
		t.Fatalf("the chain ran %v, want %v", ran, want)
	}
}

// A middleware is not an endpoint and has nothing to declare. If the gate
// counted one, every application would have to declare its own CORS handler.
func TestAMiddlewareDeclaresNoAccess(t *testing.T) {
	err := fx.New(
		provide(newFiber),
		provide(appfiber.AsMiddleware(func() appfiber.Middleware {
			return appfiber.Middleware{Name: "cors", Order: 50, Handler: func(c fiber.Ctx) error { return c.Next() }}
		})),
		provide(appfiber.AsRoute(newRoute(route{
			mount:   func(r fiber.Router) { r.Get("/things", handler) },
			declare: []authhttp.Endpoint{authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read"))},
		}))),
		serving(),
	).Err()
	if err != nil {
		t.Fatalf("a middleware was treated as an endpoint that must declare access: %v", err)
	}
}
