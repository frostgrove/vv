package authnet_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/auth/http/authnet"
)

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

func TestAHandMountedHeadOrOptionsRouteMustDeclareItsAccess(t *testing.T) {
	surface := authnet.Over(nil)
	surface.HandleFunc("GET /api/v1/things", nothing)
	surface.HandleFunc("HEAD /api/v1/health", nothing)
	surface.HandleFunc("OPTIONS /api/v1/things", nothing)

	err := surface.Verify([]authhttp.Endpoint{
		authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read")),
	}, authhttp.UnderPrefix(apiPrefix))
	if err == nil {
		t.Fatal("a HEAD and an OPTIONS handler somebody mounted by hand answer without ever having been considered")
	}
	for _, want := range []string{"HEAD /api/v1/health", "OPTIONS /api/v1/things"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the failure does not name %s: %v", want, err)
		}
	}
}

func TestTheGateRefusesARouteMountedOutsideEveryVerifiedSurface(t *testing.T) {
	surface := authnet.Over(nil)
	surface.HandleFunc("GET /api/v1/things", nothing)
	surface.HandleFunc("GET /live", nothing)

	declared := authhttp.Under(apiPrefix,
		authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read")))

	err := surface.VerifyAreas(declared)
	if err == nil {
		t.Fatal("a route mounted outside the verified prefix answered without declaring anything")
	}
	if !strings.Contains(err.Error(), "GET /live") {
		t.Fatalf("the failure does not name the route nobody verified: %v", err)
	}

	if err := surface.VerifyAreas(declared, authhttp.Rooted(
		authhttp.Public(http.MethodGet, "/live", "a load balancer cannot present a credential"),
	)); err != nil {
		t.Fatalf("the probe was declared as its own surface and the gate still refused: %v", err)
	}
}

func TestAPrefixIsNotAnExemptionForTheRoutesOutsideIt(t *testing.T) {
	surface := authnet.Over(nil)
	surface.HandleFunc("GET /api/v1/things", nothing)
	surface.HandleFunc("GET /live", nothing)
	surface.HandleFunc("GET /favicon.ico", nothing)

	declared := []authhttp.Endpoint{
		authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read")),
	}

	err := surface.Verify(declared, authhttp.UnderPrefix(apiPrefix))
	if err == nil {
		t.Fatalf("the probe and the favicon answer outside %s and configuring that prefix was enough to stop anybody looking at them", apiPrefix)
	}
	for _, want := range []string{"GET /live", "GET /favicon.ico"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the failure does not name %s: %v", want, err)
		}
	}

	declared = append(declared,
		authhttp.AtRoot(authhttp.Public(http.MethodGet, "/live", "a load balancer cannot present a credential")),
		authhttp.AtRoot(authhttp.Public(http.MethodGet, "/favicon.ico", "a browser asks for it before anybody signs in")),
	)
	if err := surface.Verify(declared, authhttp.UnderPrefix(apiPrefix)); err != nil {
		t.Fatalf("every route outside the prefix was declared where it answers and the gate still refused: %v", err)
	}
}
