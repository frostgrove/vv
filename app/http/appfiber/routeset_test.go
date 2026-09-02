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

const permRead = auth.Permission("jobs.dead.read")

func routeFrom(t *testing.T, set *appfiber.RouteSet) func() appfiber.Route {
	t.Helper()
	route, err := set.Route()
	if err != nil {
		t.Fatalf("the route set was refused: %v", err)
	}
	return func() appfiber.Route { return route }
}

func answering(ran *bool) fiber.Handler {
	return func(fiberContext fiber.Ctx) error {
		*ran = true
		return fiberContext.SendString("ok")
	}
}

func servedBy(t *testing.T, options ...fx.Option) *fiber.App {
	t.Helper()
	var mounted *fiber.App
	err := fx.New(append(options, provide(newFiber), mounting(), fx.Populate(&mounted))...).Err()
	if err != nil {
		t.Fatalf("the application did not start: %v", err)
	}
	return mounted
}

func TestOneRegistrationBothMountsTheOperationAndDeclaresIt(t *testing.T) {
	var ran bool
	set := appfiber.Routes("/ops/jobs").
		GET("/dead", appfiber.Public("a demo endpoint nobody has to be anybody to read"), answering(&ran))

	mounted := servedBy(t, provide(appfiber.AsRoute(routeFrom(t, set))))

	if got := request(t, mounted, "/api/v1/ops/jobs/dead"); got != http.StatusOK {
		t.Fatalf("the registered operation answered %d; one call was supposed to both mount it and declare it", got)
	}
	if !ran {
		t.Fatal("the handler behind the registered operation never ran")
	}
}

func TestTheDeclaredPathIsTheMountedPath(t *testing.T) {
	var ran bool
	set := appfiber.Routes("/ops/jobs").
		GET("/dead", appfiber.Public("a probe of the registrar"), answering(&ran)).
		POST("/dead/:id/restart", appfiber.Public("a probe of the registrar"), answering(&ran))

	route, err := set.Route()
	if err != nil {
		t.Fatalf("the route set was refused: %v", err)
	}

	var paths []string
	for _, endpoint := range route.Access() {
		paths = append(paths, endpoint.Method+" "+endpoint.Path)
	}
	want := []string{"GET /ops/jobs/dead", "POST /ops/jobs/dead/:id/restart"}
	if strings.Join(paths, ", ") != strings.Join(want, ", ") {
		t.Fatalf("the declaration says %v, the mount says %v; the registrar exists so the two cannot say different things", paths, want)
	}

	servedBy(t, provide(appfiber.AsRoute(func() appfiber.Route { return route })))
}

func TestAnOperationRefusesAPrincipalWithoutThePermissionItDeclares(t *testing.T) {
	var ran bool
	set := appfiber.Routes("/ops/jobs").GET("/dead", appfiber.Requires(permRead), answering(&ran))

	mounted := servedBy(t,
		provide(appfiber.AsMiddleware(func() appfiber.Middleware { return signedInAs(operator{}) })),
		provide(appfiber.AsRoute(routeFrom(t, set))),
	)

	if got := request(t, mounted, "/api/v1/ops/jobs/dead"); got != http.StatusForbidden {
		t.Fatalf("a caller without %s got %d; declaring the permission is supposed to be the same act as enforcing it", permRead, got)
	}
	if ran {
		t.Fatal("the handler ran for a caller that holds none of the permissions the operation declares")
	}
}

func TestAnOperationAdmitsAPrincipalHoldingThePermissionItDeclares(t *testing.T) {
	var ran bool
	set := appfiber.Routes("/ops/jobs").GET("/dead", appfiber.Requires(permRead), answering(&ran))

	mounted := servedBy(t,
		provide(appfiber.AsMiddleware(func() appfiber.Middleware {
			return signedInAs(operator{permissions: []auth.Permission{permRead}})
		})),
		provide(appfiber.AsRoute(routeFrom(t, set))),
	)

	if got := request(t, mounted, "/api/v1/ops/jobs/dead"); got != http.StatusOK {
		t.Fatalf("a caller holding %s got %d; the outer check refuses what it should let through", permRead, got)
	}
	if !ran {
		t.Fatal("the handler never ran for a caller holding every permission the operation declares")
	}
}

func TestAnOperationThatNamesPermissionsRefusesAnAnonymousCaller(t *testing.T) {
	var ran bool
	set := appfiber.Routes("/ops/jobs").GET("/dead", appfiber.Requires(permRead), answering(&ran))

	mounted := servedBy(t, provide(appfiber.AsRoute(routeFrom(t, set))))

	if got := request(t, mounted, "/api/v1/ops/jobs/dead"); got != http.StatusUnauthorized {
		t.Fatalf("a caller with no principal at all got %d, want 401", got)
	}
	if ran {
		t.Fatal("the handler ran for a caller nobody authenticated")
	}
}

func TestAPublicOperationIsMountedWithoutAPermissionCheck(t *testing.T) {
	var ran bool
	set := appfiber.Routes("").
		GET("/status", appfiber.Public("a status page the load balancer reads and it has no account"), answering(&ran))

	mounted := servedBy(t, provide(appfiber.AsRoute(routeFrom(t, set))))

	if got := request(t, mounted, "/api/v1/status"); got != http.StatusOK {
		t.Fatalf("a public operation answered %d for an anonymous caller", got)
	}
	if !ran {
		t.Fatal("the handler behind a public operation never ran")
	}
}

func TestAnOperationThatStatesNoPolicyIsRefusedAtTheBuild(t *testing.T) {
	_, err := appfiber.Routes("/ops/jobs").GET("/dead", appfiber.Policy{}, handler).Route()

	if err == nil {
		t.Fatal("an operation with no access policy was accepted; forgetting must not read as 'no permission needed'")
	}
	if !errors.Is(err, appfiber.ErrRouteSet) {
		t.Fatalf("the refusal does not wrap ErrRouteSet: %v", err)
	}
	if !strings.Contains(err.Error(), "GET /ops/jobs/dead") {
		t.Fatalf("the refusal does not name the operation: %v", err)
	}
}

func TestRequiringNothingIsNotAWayToSayPublic(t *testing.T) {
	_, err := appfiber.Routes("/ops/jobs").GET("/dead", appfiber.Requires(), handler).Route()

	if err == nil {
		t.Fatal("Requires() with no permission was accepted, which is an unguarded operation that reads as a guarded one")
	}
}

func TestAPublicOperationMustSayWhyItIsOpen(t *testing.T) {
	_, err := appfiber.Routes("/ops/jobs").GET("/dead", appfiber.Public(""), handler).Route()

	if err == nil {
		t.Fatal("a public operation with no reason was accepted")
	}
}

func TestTheSameOperationCannotBeRegisteredTwice(t *testing.T) {
	_, err := appfiber.Routes("/ops/jobs").
		GET("/dead", appfiber.Requires(permRead), handler).
		GET("/dead", appfiber.Public("second thoughts"), handler).
		Route()

	if err == nil {
		t.Fatal("one path was registered twice with two different policies and the set built anyway")
	}
	if !strings.Contains(err.Error(), "registered twice") {
		t.Fatalf("the refusal does not say the operation is registered twice: %v", err)
	}
}

func TestNewRouteSetRefusesAPrefixThatIsNotAPath(t *testing.T) {
	set, err := appfiber.NewRouteSet(appfiber.RouteSetSpec{Prefix: "ops/jobs"})

	if err == nil {
		t.Fatal("the explicit constructor accepted a prefix that does not start with /")
	}
	if set != nil {
		t.Fatal("the explicit constructor returned a set it had just refused")
	}
}

func TestRoutesCarriesAPrefixMistakeToTheBuild(t *testing.T) {
	_, err := appfiber.Routes("ops/jobs").GET("/dead", appfiber.Requires(permRead), handler).Route()

	if err == nil {
		t.Fatal("the shorthand swallowed a prefix the explicit constructor refuses")
	}
	if !strings.Contains(err.Error(), "ops/jobs") {
		t.Fatalf("the refusal does not name the prefix: %v", err)
	}
}

func TestARouteMountedPastTheRegistrarStillBreaksStartUp(t *testing.T) {
	set := appfiber.Routes("/ops/jobs").GET("/dead", appfiber.Requires(permRead), handler)

	err := fx.New(
		provide(newFiber),
		provide(appfiber.AsRoute(routeFrom(t, set))),
		fx.Provide(fx.Annotate(
			func() appfiber.Route {
				return route{
					mount:   func(r fiber.Router) { r.Get("/smuggled", handler) },
					declare: []authhttp.Endpoint{},
				}
			},
			fx.As(new(appfiber.Route)),
			fx.ResultTags(`group:"vv.appfiber.routes"`),
		)),
		mounting(),
	).Err()

	if err == nil {
		t.Fatal("a route mounted without going through the registrar started the application; the router is still the independent witness")
	}
	if !strings.Contains(err.Error(), "GET /api/v1/smuggled") {
		t.Fatalf("the failure does not name the smuggled route: %v", err)
	}
}

func TestAnOperationWithoutAHandlerIsRefused(t *testing.T) {
	_, err := appfiber.Routes("/ops/jobs").GET("/dead", appfiber.Requires(permRead), nil).Route()

	if err == nil {
		t.Fatal("an operation with no handler was accepted, and it would have declared an endpoint that answers nothing")
	}
}

func TestASignedInOperationTakesAnyPrincipalAndRefusesNone(t *testing.T) {
	var ran bool
	build := func() *appfiber.RouteSet {
		ran = false
		return appfiber.Routes("/ops/jobs").
			GET("/mine", appfiber.Authenticated("the answer is the caller's own and names no permission"), answering(&ran))
	}

	anonymous := servedBy(t, provide(appfiber.AsRoute(routeFrom(t, build()))))
	if got := request(t, anonymous, "/api/v1/ops/jobs/mine"); got != http.StatusUnauthorized {
		t.Fatalf("a caller nobody authenticated got %d from a signed-in-only operation", got)
	}
	if ran {
		t.Fatal("the handler ran for a caller nobody authenticated")
	}

	signedIn := servedBy(t,
		provide(appfiber.AsMiddleware(func() appfiber.Middleware { return signedInAs(operator{}) })),
		provide(appfiber.AsRoute(routeFrom(t, build()))),
	)
	if got := request(t, signedIn, "/api/v1/ops/jobs/mine"); got != http.StatusOK {
		t.Fatalf("a signed-in caller holding no permission got %d, and the operation asked for none", got)
	}
	if !ran {
		t.Fatal("the handler never ran for a signed-in caller")
	}
}

func TestASignedInOperationMustSayWhyBeingSignedInIsEnough(t *testing.T) {
	_, err := appfiber.Routes("/ops/jobs").GET("/mine", appfiber.Authenticated(""), handler).Route()

	if err == nil {
		t.Fatal("an operation that asks only for a signed-in caller was accepted without saying why that is enough")
	}
}
