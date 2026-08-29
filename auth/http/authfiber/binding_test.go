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

// What this binding does that the other two do not.
//
// Fiber registers a HEAD for every GET and answers OPTIONS out of its own
// routing table, so its table carries entries no module wrote. Neither of the
// other two bindings has anything to mirror this to, so it lives here rather
// than making the triplet's test names disagree. [[FL-019]] carries the
// difference.

// A module that writes a GET did not write the HEAD Fiber added beside it, and
// demanding a declaration for one is demanding a declaration for something
// nobody can point at in the source.
//
// The generated route only appears once Fiber has run its start-up process, so
// an application that verifies before it listens never sees one. It is filtered
// anyway: the flag that marks a route as generated is unexported, so from out
// here a generated HEAD and a hand-written one are the same value — and the
// version that starts filtering earlier or later must not be the version every
// deployment fails to start on.
func TestTheHeadFiberGeneratesNeedsNoDeclaration(t *testing.T) {
	app := mountedApp(func(r fiber.Router) { r.Get("/things", nothing) })

	// Test runs Fiber's start-up process, which is what adds the HEAD.
	if _, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)); err != nil {
		t.Fatalf("serving one request: %v", err)
	}

	// The control. Fiber really did register that HEAD, so the case below is
	// exempting something rather than passing because there was nothing there.
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

// A middleware is not an endpoint and has no access to declare. Fiber lists Use
// registrations in the same table as routes, so the gate has to filter them out
// or every application would have to declare its own CORS handler.
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
