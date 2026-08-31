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

func TestARouteOutsideThePrefixIsNotPartOfTheSurface(t *testing.T) {
	declared := []authhttp.Endpoint{
		authhttp.Requires(http.MethodGet, "/things", auth.Permission("thing.read")),
	}
	routes := mounted("GET /", "GET /favicon.ico", "GET /api/v1/things")

	if err := authhttp.Verify(declared, routes, authhttp.UnderPrefix(prefix)); err != nil {
		t.Fatalf("a route outside %s was checked: %v", prefix, err)
	}

	if err := authhttp.Verify(declared, routes); err == nil {
		t.Fatal("with no prefix the health check and the favicon were still exempt, so the case above proves nothing")
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
