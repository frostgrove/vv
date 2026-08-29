package authnet_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/auth/http/authnet"
)

// The gate exists because a route with no access check and a route that is
// deliberately public look the same from the inside. These tests break an
// application on purpose and assert that start-up notices.

const apiPrefix = "/api/v1"

func nothing(http.ResponseWriter, *http.Request) {}

func TestTheGatePassesWhenEveryMountedRouteIsDeclared(t *testing.T) {
	surface := authnet.Over(nil)
	surface.HandleFunc("POST /api/v1/auth/login", nothing)
	surface.HandleFunc("GET /api/v1/users/{id}", nothing)

	err := surface.Verify([]authhttp.Endpoint{
		authhttp.Public(http.MethodPost, "/auth/login", "there is no credential to present yet"),
		authhttp.Requires(http.MethodGet, "/users/{id}", auth.Permission("user.read")),
	}, authhttp.UnderPrefix(apiPrefix))
	if err != nil {
		t.Fatalf("a router that matches its declaration was rejected: %v", err)
	}
}

func TestTheGateRefusesARouteNobodyDeclared(t *testing.T) {
	surface := authnet.Over(nil)
	surface.HandleFunc("POST /api/v1/auth/login", nothing)
	// The one somebody added in a hurry.
	surface.HandleFunc("DELETE /api/v1/users/{id}", nothing)

	err := surface.Verify([]authhttp.Endpoint{
		authhttp.Public(http.MethodPost, "/auth/login", "there is no credential to present yet"),
	}, authhttp.UnderPrefix(apiPrefix))
	if err == nil {
		t.Fatal("an endpoint nobody declared was accepted; the whole point of this check is that it is not")
	}
	if !strings.Contains(err.Error(), "DELETE /api/v1/users/{id}") {
		t.Fatalf("the failure does not name the undeclared endpoint: %v", err)
	}
}

func TestTheGateRefusesADeclarationThatMountsNothing(t *testing.T) {
	surface := authnet.Over(nil)
	surface.HandleFunc("POST /api/v1/auth/login", nothing)

	// The route was renamed and this line was left behind.
	err := surface.Verify([]authhttp.Endpoint{
		authhttp.Public(http.MethodPost, "/auth/login", "there is no credential to present yet"),
		authhttp.Requires(http.MethodGet, "/users/{id}", auth.Permission("user.read")),
	}, authhttp.UnderPrefix(apiPrefix))
	if err == nil {
		t.Fatal("a declaration whose route no longer exists was accepted")
	}
	if !strings.Contains(err.Error(), "declared and mounts nothing") {
		t.Fatalf("the failure does not say the declaration is stale: %v", err)
	}
}
