package authnet_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/auth/http/authnet"
	"github.com/frostgrove/vv/crud/http/crudnet"
)

// What this binding tests that the other two cannot.
//
// The composition below needs both halves of the same stack — the auth
// middleware and the CRUD error middleware — in one test binary. On net/http
// both live in the root module, so that is free. On Gin and Fiber it would mean
// authgin's tests requiring crudgin, and [[D-051]] is exactly the rule against
// that: a consumer mounting auth on Gin must not be made to take the CRUD
// binding with it, and a test dependency is still a dependency in the module
// graph the consumer resolves.
//
// So the behaviour is the same on all three and only one of them can say so.
// [[FL-019]] carries the difference rather than the triplet's names disagreeing
// about it.

// The auth middleware writes its own refusal, so it has to compose with the
// error middleware a consumer already mounts for the CRUD routes. Rendering
// twice produces a body with two JSON documents in it.
func TestARefusalIsNotRenderedTwiceUnderTheErrorMiddleware(t *testing.T) {
	h := &seen{}
	request := httptest.NewRequest(http.MethodGet, "/articles", nil)
	request.Header.Set("Authorization", "Bearer forged")
	w := httptest.NewRecorder()

	crudnet.Errors()(authnet.Middleware(auth.NewGuard(refuses()))(h)).ServeHTTP(w, request)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("the refusal answered %d under the error middleware, want 401", w.Code)
	}
	if n := strings.Count(w.Body.String(), `"type"`); n != 1 {
		t.Fatalf("the body carries %d envelopes, want 1: %s", n, w.Body.String())
	}
}

// A ServeMux cannot be asked what it holds, so this binding records what was
// registered instead of reading a table. Neither of the other two bindings has
// anything to mirror that to, so what it costs and what it still catches are
// pinned here. [[FL-019]] carries the difference.

// A pattern with no verb answers every verb, which is a decision somebody made
// and not an omission — so it is declared as one rather than quietly matching
// the GET they had in mind.
func TestAPatternWithNoMethodIsDeclaredAsAnyMethod(t *testing.T) {
	surface := authnet.Over(nil)
	surface.HandleFunc("/api/v1/things/", nothing)

	if err := surface.Verify([]authhttp.Endpoint{
		authhttp.Requires(http.MethodGet, "/things/", auth.Permission("thing.read")),
	}, authhttp.UnderPrefix(apiPrefix)); err == nil {
		t.Fatal("a route that answers every verb was covered by a declaration naming only GET")
	}

	if err := surface.Verify([]authhttp.Endpoint{
		authhttp.Requires(authnet.AnyMethod, "/things/", auth.Permission("thing.read")),
	}, authhttp.UnderPrefix(apiPrefix)); err != nil {
		t.Fatalf("a route that answers every verb could not be declared as one: %v", err)
	}
}

// What this binding cannot do, asserted rather than left to be discovered. A
// handler registered straight on the wrapped mux is not recorded, so the gate
// does not see it — and somebody reading the gate's guarantees has to be told
// that in a form that fails when it stops being true.
func TestARouteRegisteredPastTheSurfaceIsInvisibleToTheGate(t *testing.T) {
	surface := authnet.Over(nil)
	surface.HandleFunc("GET /api/v1/things", nothing)
	surface.Mux().HandleFunc("DELETE /api/v1/things/{id}", nothing)

	declared := []authhttp.Endpoint{
		authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read")),
	}
	if err := surface.Verify(declared, authhttp.UnderPrefix(apiPrefix)); err != nil {
		t.Fatalf("the recorded half of the surface stopped verifying: %v", err)
	}

	// The control. Registered through the Surface, the very same route is
	// caught — so the gap above is the mux being unreadable and not the gate
	// being switched off.
	recorded := authnet.Over(nil)
	recorded.HandleFunc("GET /api/v1/things", nothing)
	recorded.HandleFunc("DELETE /api/v1/things/{id}", nothing)
	if err := recorded.Verify(declared, authhttp.UnderPrefix(apiPrefix)); err == nil {
		t.Fatal("an undeclared route registered through the Surface was accepted, so this binding's gate catches nothing at all")
	}
}
