package appfiber_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"

	"github.com/frostgrove/vv/app/http/appfiber"
	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
)

const prefix = "/api/v1"

type route struct {
	mount   func(fiber.Router)
	declare []authhttp.Endpoint
}

func (this route) Mount(r fiber.Router)        { this.mount(r) }
func (this route) Access() []authhttp.Endpoint { return this.declare }
func newRoute(r route) func() appfiber.Route   { return func() appfiber.Route { return r } }
func handler(fiber.Ctx) error                  { return nil }
func provide(constructors ...any) fx.Option    { return fx.Provide(constructors...) }
func mounting() fx.Option                      { return appfiber.Mounting(appfiber.Spec{Prefix: prefix}) }
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
		mounting(),
		fx.Populate(&mounted),
	).Err()
	if err != nil {
		t.Fatalf("a well-formed module did not start: %v", err)
	}

	if got := request(t, mounted, "/api/v1/things"); got == http.StatusNotFound {
		t.Fatal("the route was not mounted under the prefix, so a module's paths depend on where the transport put it")
	}
}

func TestStartUpFailsWhenARouteDeclaresNothing(t *testing.T) {
	err := fx.New(
		provide(newFiber),
		provide(appfiber.AsRoute(newRoute(route{
			mount: func(r fiber.Router) {
				r.Get("/things", handler)

				r.Delete("/things/:id", handler)
			},
			declare: []authhttp.Endpoint{authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read"))},
		}))),
		mounting(),
	).Err()

	if err == nil {
		t.Fatal("an application with an undeclared endpoint started; the whole point of the gate is that it does not")
	}
	if !strings.Contains(err.Error(), "DELETE /api/v1/things/:id") {
		t.Fatalf("the failure does not name the undeclared endpoint: %v", err)
	}
}

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
		mounting(),
	).Err()

	if err == nil {
		t.Fatal("a declaration whose route no longer exists was accepted")
	}
	if !strings.Contains(err.Error(), "declared and mounts nothing") {
		t.Fatalf("the failure does not say the declaration is stale: %v", err)
	}
}

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

		provide(appfiber.AsMiddleware(records("handler-assumes-a-caller", 200))),
		provide(appfiber.AsMiddleware(records("guard", 100))),
		provide(appfiber.AsRoute(newRoute(route{
			mount:   func(r fiber.Router) { r.Get("/things", handler) },
			declare: []authhttp.Endpoint{authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read"))},
		}))),
		mounting(),
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
		mounting(),
	).Err()
	if err != nil {
		t.Fatalf("a middleware was treated as an endpoint that must declare access: %v", err)
	}
}

func TestAMiddlewareThatCameOutWithoutAHandlerStopsTheStart(t *testing.T) {
	err := fx.New(
		provide(newFiber),
		provide(appfiber.AsMiddleware(func() appfiber.Middleware {
			return appfiber.Middleware{Name: "guard", Order: 100}
		})),
		provide(appfiber.AsRoute(newRoute(route{
			mount:   func(r fiber.Router) { r.Get("/things", handler) },
			declare: []authhttp.Endpoint{authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read"))},
		}))),
		mounting(),
	).Err()

	if err == nil {
		t.Fatal("a named guard was contributed without a handler and the application started anyway; the surface it protects now answers unauthenticated")
	}
	if !strings.Contains(err.Error(), "guard") {
		t.Fatalf("the refusal does not name the middleware that lost its handler: %v", err)
	}
}

func TestServingWithoutAnAddressNamesTheOptionThatOnlyMounts(t *testing.T) {
	err := fx.New(provide(newFiber), appfiber.Serving(appfiber.Spec{Prefix: prefix})).Err()

	if err == nil {
		t.Fatal("Serving accepted a spec with no address, so whether it listens is something a caller reads the source for")
	}
	if !strings.Contains(err.Error(), "Mounting") {
		t.Fatalf("the refusal does not name the option that mounts without listening: %v", err)
	}
}

func TestMountingIsRefusedAnAddressItWouldNeverUse(t *testing.T) {
	err := fx.New(provide(newFiber), appfiber.Mounting(appfiber.Spec{Prefix: prefix, Addr: "127.0.0.1:0"})).Err()

	if err == nil {
		t.Fatal("Mounting took an address and dropped it in silence")
	}
	if !strings.Contains(err.Error(), "Serving") {
		t.Fatalf("the refusal does not name the option that also listens: %v", err)
	}
}

func TestServingAnswersOnTheAddressItWasGiven(t *testing.T) {
	address := freeAddress(t)

	served := fx.New(
		provide(newFiber),
		provide(appfiber.AsRoute(newRoute(route{
			mount:   func(r fiber.Router) { r.Get("/things", func(c fiber.Ctx) error { return c.SendString("ok") }) },
			declare: []authhttp.Endpoint{authhttp.Public(http.MethodGet, "/things", "a probe of the listener")},
		}))),
		appfiber.Serving(appfiber.Spec{Prefix: prefix, Addr: address}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := served.Start(ctx); err != nil {
		t.Fatalf("the application did not start: %v", err)
	}
	defer func() { _ = served.Stop(ctx) }()

	if status := answered(t, "http://"+address+prefix+"/things"); status != http.StatusOK {
		t.Fatalf("Serving mounted the route and answered %d on %s, so it mounted without listening", status, address)
	}
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no free port to serve on: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("releasing the port: %v", err)
	}
	return address
}

func answered(t *testing.T, url string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := http.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			return response.StatusCode
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing ever answered on %s: %v", url, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
