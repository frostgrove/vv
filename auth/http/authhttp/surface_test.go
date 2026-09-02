package authhttp_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
)

const prefix = "/api/v1"

func mounted(routes ...string) []authhttp.Route {
	out := make([]authhttp.Route, 0, len(routes))
	for _, route := range routes {
		method, path, _ := strings.Cut(route, " ")
		out = append(out, authhttp.Route{Method: method, Path: path})
	}
	return out
}

func TestARouterThatMatchesItsDeclarationIsAccepted(t *testing.T) {
	err := authhttp.Verify(
		[]authhttp.Endpoint{
			authhttp.Public(http.MethodPost, "/auth/login", "there is no credential to present yet"),
			authhttp.Requires(http.MethodGet, "/users/:id", auth.Permission("user.read")),
		},
		mounted("POST /api/v1/auth/login", "GET /api/v1/users/:id"),
		authhttp.UnderPrefix(prefix),
	)
	if err != nil {
		t.Fatalf("a router that matches its declaration was rejected: %v", err)
	}
}

func TestAMountedRouteThatDeclaresNothingIsRefused(t *testing.T) {
	err := authhttp.Verify(
		[]authhttp.Endpoint{
			authhttp.Public(http.MethodPost, "/auth/login", "there is no credential to present yet"),
		},

		mounted("POST /api/v1/auth/login", "DELETE /api/v1/users/:id"),
		authhttp.UnderPrefix(prefix),
	)
	if err == nil {
		t.Fatal("an endpoint nobody declared was accepted; the whole point of this check is that it is not")
	}
	if !strings.Contains(err.Error(), "DELETE /api/v1/users/:id") {
		t.Fatalf("the failure does not name the undeclared endpoint: %v", err)
	}
}

func TestADeclarationThatMountsNothingIsRefused(t *testing.T) {
	err := authhttp.Verify(
		[]authhttp.Endpoint{
			authhttp.Public(http.MethodPost, "/auth/login", "there is no credential to present yet"),

			authhttp.Requires(http.MethodGet, "/users/:id", auth.Permission("user.read")),
		},
		mounted("POST /api/v1/auth/login"),
		authhttp.UnderPrefix(prefix),
	)
	if err == nil {
		t.Fatal("a declaration whose route no longer exists was accepted")
	}
	if !strings.Contains(err.Error(), "declared and mounts nothing") {
		t.Fatalf("the failure does not say the declaration is stale: %v", err)
	}
}

func TestADeclarationMustNameEitherPermissionsOrAReason(t *testing.T) {
	err := authhttp.Verify(
		[]authhttp.Endpoint{{Method: http.MethodGet, Path: "/things"}},
		mounted("GET /api/v1/things"),
		authhttp.UnderPrefix(prefix),
	)
	if err == nil {
		t.Fatal("a declaration naming neither permissions nor a reason was accepted, which makes 'I forgot' indistinguishable from 'this is open'")
	}
	if !strings.Contains(err.Error(), "declares neither") {
		t.Fatalf("unexpected failure: %v", err)
	}
}

func TestTheSameEndpointDeclaredTwiceIsRefused(t *testing.T) {
	err := authhttp.Verify(
		[]authhttp.Endpoint{
			authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read")),
			authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.write")),
		},
		mounted("GET /api/v1/things"),
		authhttp.UnderPrefix(prefix),
	)
	if err == nil {
		t.Fatal("two declarations for one endpoint were accepted; whichever the map happened to keep would then be the documented one")
	}
}

func TestATrailingSlashIsNotADisagreement(t *testing.T) {
	err := authhttp.Verify(
		[]authhttp.Endpoint{authhttp.Requires(http.MethodGet, "/roles", auth.Permission("role.read"))},
		mounted("GET /api/v1/roles/"),
		authhttp.UnderPrefix(prefix),
	)
	if err != nil {
		t.Fatalf("a trailing slash was treated as a different endpoint: %v", err)
	}
}

func TestARouteOutsideThePrefixStillHasToDeclareItsAccess(t *testing.T) {
	declared := []authhttp.Endpoint{
		authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read")),
	}
	routes := mounted("GET /", "GET /favicon.ico", "GET /api/v1/things")

	err := authhttp.Verify(declared, routes, authhttp.UnderPrefix(prefix))
	if err == nil {
		t.Fatalf("the root and the favicon answer the world and configuring %s was enough to stop anybody looking at them", prefix)
	}
	for _, want := range []string{"GET /", "GET /favicon.ico"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the failure does not name %s: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "/api/v1/things") {
		t.Fatalf("a declared route under the prefix was reported as well: %v", err)
	}
}

func TestAnEndpointDeclaredAtRootIsCheckedByItsAbsolutePath(t *testing.T) {
	declared := []authhttp.Endpoint{
		authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read")),
		authhttp.AtRoot(authhttp.Public(http.MethodGet, "/live", "a load balancer cannot present a credential")),
	}

	if err := authhttp.Verify(declared, mounted("GET /api/v1/things", "GET /live"), authhttp.UnderPrefix(prefix)); err != nil {
		t.Fatalf("the probe was declared where it actually answers and the gate still refused: %v", err)
	}

	stale := authhttp.Verify(declared, mounted("GET /api/v1/things"), authhttp.UnderPrefix(prefix))
	if stale == nil {
		t.Fatal("the root declaration was accepted without the route existing, so it is not being matched by its absolute path")
	}
	if !strings.Contains(stale.Error(), "GET /live") {
		t.Fatalf("the stale root declaration is reported under some other path: %v", stale)
	}
}

func TestEveryDisagreementIsReportedAtOnce(t *testing.T) {
	err := authhttp.Verify(
		[]authhttp.Endpoint{
			authhttp.Requires(http.MethodGet, "/gone", auth.Permission("thing.read")),
			{Method: http.MethodGet, Path: "/silent"},
		},
		mounted("GET /api/v1/silent", "DELETE /api/v1/undeclared"),
		authhttp.UnderPrefix(prefix),
	)
	if err == nil {
		t.Fatal("three separate disagreements were accepted")
	}
	for _, want := range []string{"declares neither", "declared and mounts nothing", "is mounted and declares no access"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the failure stops before %q, so fixing one problem only reveals the next: %v", want, err)
		}
	}
}

func TestARefusalIsReachableWithoutReadingItsText(t *testing.T) {
	err := authhttp.Verify(
		[]authhttp.Endpoint{},
		mounted("GET /api/v1/things"),
		authhttp.UnderPrefix(prefix),
	)
	if !errors.Is(err, authhttp.ErrSurface) {
		t.Fatalf("the gate's refusal does not wrap ErrSurface, so a caller has to match on the message: %v", err)
	}
}

func TestAnAuthenticatedEndpointRecordsThatItIsOpen(t *testing.T) {
	endpoint := authhttp.Authenticated(http.MethodGet, "/auth/me", "reading your own principal")
	if !endpoint.Declares() {
		t.Fatal("an endpoint open to any signed-in caller declared nothing, so the gate would refuse it")
	}
	if len(endpoint.Needs) != 0 {
		t.Fatal("'any signed-in caller' was turned into a permission requirement")
	}
	if !strings.Contains(endpoint.Why, "reading your own principal") {
		t.Fatalf("the reason a reviewer reads was dropped: %q", endpoint.Why)
	}
}

func TestANeighbouringPrefixIsNotPartOfThisSurface(t *testing.T) {
	declared := []authhttp.Endpoint{
		authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read")),
		authhttp.AtRoot(authhttp.Public(http.MethodGet, "/api/v10/things", "the next version answers for itself")),
		authhttp.AtRoot(authhttp.Public(http.MethodGet, "/api/v1evil", "a path that merely starts with the prefix")),
	}
	neighbours := mounted("GET /api/v1/things", "GET /api/v10/things", "GET /api/v1evil")

	if err := authhttp.Verify(declared, neighbours, authhttp.UnderPrefix(prefix)); err != nil {
		t.Fatalf("a route in a neighbouring tree was pulled into %s and judged against its declarations: %v", prefix, err)
	}

	sameTree := []authhttp.Endpoint{
		authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read")),
	}
	if err := authhttp.Verify(sameTree, mounted("GET /api/v1/things", "GET /api/v1/other"), authhttp.UnderPrefix(prefix)); err == nil {
		t.Fatal("a route genuinely under the prefix was skipped too, so the case above proves nothing")
	}
}

func TestARouteMountedOutsideEveryVerifiedSurfaceIsRefused(t *testing.T) {
	err := authhttp.VerifyAreas(
		mounted("GET /api/v1/things", "GET /live", "GET /favicon.ico"),
		authhttp.Under(prefix, authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read"))),
	)
	if err == nil {
		t.Fatal("the health probe and the favicon answer to the world and nobody had to say why")
	}
	for _, want := range []string{"GET /live", "GET /favicon.ico"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the failure does not name %s: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "/api/v1/things") {
		t.Fatalf("a declared route inside a verified surface was reported as well: %v", err)
	}
}

func TestARootSurfaceDeclaresWhatLivesOutsideThePrefix(t *testing.T) {
	err := authhttp.VerifyAreas(
		mounted("GET /api/v1/things", "GET /live", "GET /favicon.ico"),
		authhttp.Under(prefix, authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read"))),
		authhttp.Rooted(
			authhttp.Public(http.MethodGet, "/live", "a load balancer cannot present a credential"),
			authhttp.Public(http.MethodGet, "/favicon.ico", "a browser asks for it before anybody signs in"),
		),
	)
	if err != nil {
		t.Fatalf("every mounted route was declared and the gate still refused: %v", err)
	}

	stale := authhttp.VerifyAreas(
		mounted("GET /api/v1/things", "GET /live", "GET /ready"),
		authhttp.Under(prefix, authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read"))),
		authhttp.Rooted(authhttp.Public(http.MethodGet, "/live", "a load balancer cannot present a credential")),
	)
	if stale == nil {
		t.Fatal("a probe nobody declared passed the root surface, so the case above proves nothing")
	}
}

func TestTwoVerifiedSurfacesThatOverlapAreRefused(t *testing.T) {
	err := authhttp.VerifyAreas(
		mounted("GET /api/v1/things"),
		authhttp.Under("/api", authhttp.Requires(http.MethodGet, "/v1/things", auth.Permission("thing.read"))),
		authhttp.Under(prefix, authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read"))),
	)
	if err == nil {
		t.Fatal("two surfaces claim the same routes and which one checks them is whichever the loop reached first")
	}
	if !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("the failure does not say the surfaces overlap: %v", err)
	}
}

func TestARouteOutsideThePrefixIsToldWhereToDeclareItself(t *testing.T) {
	err := authhttp.Verify(
		[]authhttp.Endpoint{authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read"))},
		mounted("GET /api/v1/things", "GET /live"),
		authhttp.UnderPrefix(prefix),
	)
	if err == nil {
		t.Fatal("the probe answered outside the prefix and the gate said nothing")
	}
	if !strings.Contains(err.Error(), "AtRoot") {
		t.Fatalf("the refusal names the route but not the one seam that declares it, so it reads as a wall: %v", err)
	}

	inside := authhttp.Verify(nil, mounted("GET /api/v1/things"), authhttp.UnderPrefix(prefix))
	if inside == nil {
		t.Fatal("an undeclared route under the prefix was accepted, so the case above proves nothing")
	}
	if strings.Contains(inside.Error(), "AtRoot") {
		t.Fatalf("a route under the prefix is told to declare itself at the root, which would move it out of the surface it belongs to: %v", inside)
	}
}
