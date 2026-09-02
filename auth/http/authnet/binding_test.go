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

	recorded := authnet.Over(nil)
	recorded.HandleFunc("GET /api/v1/things", nothing)
	recorded.HandleFunc("DELETE /api/v1/things/{id}", nothing)
	if err := recorded.Verify(declared, authhttp.UnderPrefix(apiPrefix)); err == nil {
		t.Fatal("an undeclared route registered through the Surface was accepted, so this binding's gate catches nothing at all")
	}
}

func TestTheSurfaceCanBeServedWithoutHoldingTheEscapeHatch(t *testing.T) {
	surface := authnet.Over(nil)
	surface.HandleFunc("GET /api/v1/things", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	handler := surface.Handler()
	if _, registers := handler.(*http.ServeMux); registers {
		t.Fatal("what a consumer serves is the mux itself, so serving and registering past the gate are the same value")
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/things", nil))
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("the sealed handler answered %d, so it does not serve what the surface recorded", recorder.Code)
	}
}

func TestATypedNilPreflightAnswerIsStillNothingToAnswerWith(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a nil *corsAnswer was accepted as the thing that answers a preflight")
		}
	}()
	authnet.AnswerPreflight(authnet.Middleware(auth.NewGuard(accepts())), (*corsAnswer)(nil))
}
