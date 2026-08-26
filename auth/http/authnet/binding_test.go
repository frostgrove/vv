package authnet_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shardit-io/vv/auth"
	"github.com/shardit-io/vv/auth/http/authnet"
	"github.com/shardit-io/vv/crud/http/crudnet"
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
	req := httptest.NewRequest(http.MethodGet, "/articles", nil)
	req.Header.Set("Authorization", "Bearer forged")
	w := httptest.NewRecorder()

	crudnet.Errors()(authnet.Middleware(auth.NewGuard(refuses()))(h)).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("the refusal answered %d under the error middleware, want 401", w.Code)
	}
	if n := strings.Count(w.Body.String(), `"type"`); n != 1 {
		t.Fatalf("the body carries %d envelopes, want 1: %s", n, w.Body.String())
	}
}
