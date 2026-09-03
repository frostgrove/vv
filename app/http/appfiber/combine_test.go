package appfiber_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"

	"github.com/frostgrove/vv/app/http/appfiber"
	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
)

func bundle(path string, needs ...auth.Permission) appfiber.Route {
	return route{
		mount:   func(r fiber.Router) { r.Get(path, handler) },
		declare: []authhttp.Endpoint{authhttp.Requires(http.MethodGet, path, needs...)},
	}
}

func combinedWith(t *testing.T, parts ...appfiber.Route) func() appfiber.Route {
	t.Helper()
	route, err := appfiber.Combine(parts...)
	if err != nil {
		t.Fatalf("the combination was refused: %v", err)
	}
	return func() appfiber.Route { return route }
}

func TestEveryPartOfACombinedContributionIsMountedAndDeclared(t *testing.T) {
	registered := false
	set := appfiber.Routes("/ops/jobs").GET("/dead", appfiber.Public("an operator page"), answering(&registered))

	served := servedBy(t,
		provide(appfiber.AsMiddleware(func() appfiber.Middleware {
			return signedInAs(operator{permissions: []auth.Permission{permRead}})
		})),
		provide(appfiber.AsRoute(combinedWith(t, routeFrom(t, set)(), bundle("/things", permRead)))),
	)

	if got := request(t, served, prefix+"/ops/jobs/dead"); got != http.StatusOK {
		t.Fatalf("the registrar half of the combination answered %d, so combining lost what it mounted", got)
	}
	if got := request(t, served, prefix+"/things"); got != http.StatusOK {
		t.Fatalf("the hand-mounted half of the combination answered %d, so combining lost what it mounted", got)
	}
	if !registered {
		t.Fatal("the handler the registrar recorded never ran")
	}
}

func TestAnOperationInsideACombinationStillRefusesThePrincipalItsPolicyExcludes(t *testing.T) {
	ran := false
	set := appfiber.Routes("/ops/jobs").GET("/dead", appfiber.Requires(permRead), answering(&ran))

	served := servedBy(t,
		provide(appfiber.AsMiddleware(func() appfiber.Middleware { return signedInAs(operator{}) })),
		provide(appfiber.AsRoute(combinedWith(t, routeFrom(t, set)(), bundle("/things", permRead)))),
	)

	if got := request(t, served, prefix+"/ops/jobs/dead"); got != http.StatusForbidden {
		t.Fatalf("a caller without %s got %d from an operation that was combined with another route; the check the policy mounts did not survive the combination", permRead, got)
	}
	if ran {
		t.Fatal("the handler ran for a caller the policy excludes")
	}
}

func TestACombinationNamesThePartThatLeftAnEndpointUnchecked(t *testing.T) {
	set := appfiber.Routes("/ops/jobs").GET("/dead", appfiber.Requires(permRead), handler)

	err := fx.New(
		provide(newFiber),
		provide(appfiber.AsRoute(combinedWith(t, routeFrom(t, set)(), bundle("/things", permRead)))),
		refusingUnchecked(),
		mounting(),
	).Err()

	if err == nil {
		t.Fatal("a combination carrying a declaration nothing checks started the application")
	}
	if !strings.Contains(err.Error(), "appfiber_test.route") {
		t.Fatalf("the refusal blames the wrapper instead of the part that declared the endpoint: %v", err)
	}
	if strings.Contains(err.Error(), "/ops/jobs/dead") {
		t.Fatalf("the part whose check the registrar mounted was reported as unchecked: %v", err)
	}
}

func TestTwoPartsOfOneContributionCannotDeclareTheSameOperation(t *testing.T) {
	_, err := appfiber.Combine(bundle("/things", permRead), bundle("/things", permRead))

	if err == nil {
		t.Fatal("two parts declaring one operation were combined, so the second declaration answers nothing and nobody is told")
	}
	if !errors.Is(err, appfiber.ErrCombine) {
		t.Fatalf("the refusal is not reachable as a combination refusal: %v", err)
	}
	if !strings.Contains(err.Error(), "GET /things") {
		t.Fatalf("the refusal does not name the operation both parts declare: %v", err)
	}
}

func TestACombinationOfNothingIsRefusedRatherThanMountedAsAnEmptyRoute(t *testing.T) {
	if _, err := appfiber.Combine(); err == nil {
		t.Fatal("combining nothing produced a route, so a contributor that computed no part looks like one that has none to declare")
	}
	if _, err := appfiber.Combine(bundle("/things", permRead), nil); err == nil {
		t.Fatal("a nil part was combined, and the panic it causes arrives at mount time instead of here")
	}
}

func TestMustCombineIsTheSameConstructorWithoutTheError(t *testing.T) {
	route := appfiber.MustCombine(bundle("/things", permRead))

	if len(route.Access()) != 1 {
		t.Fatalf("the combination declares %d endpoints, not the one its part does", len(route.Access()))
	}

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("a combination the error-returning constructor refuses was handed back by the panicking one")
		}
	}()
	appfiber.MustCombine()
}

func TestMustRouteSetIsTheSameConstructorWithoutTheError(t *testing.T) {
	set := appfiber.MustRouteSet(appfiber.RouteSetSpec{Prefix: "/ops/jobs"})
	if _, err := set.GET("/dead", appfiber.Requires(permRead), handler).Route(); err != nil {
		t.Fatalf("a well-formed set was refused: %v", err)
	}

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("a prefix NewRouteSet refuses was handed back as a usable set")
		}
	}()
	appfiber.MustRouteSet(appfiber.RouteSetSpec{Prefix: "ops/jobs"})
}
