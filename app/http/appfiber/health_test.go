package appfiber_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/app/http/appfiber"
	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/health"
)

const operatorPermission = auth.Permission("ops.health.read")

type operator struct {
	permissions []auth.Permission
}

func (this operator) Subject() string { return "operator" }

func (this operator) In(auth.Role) bool { return false }

func (this operator) Has(permission auth.Permission) bool {
	for _, held := range this.permissions {
		if held == permission {
			return true
		}
	}
	return false
}

func (this operator) Attr(string) (any, bool) { return nil, false }

func signedInAs(principal auth.Principal) appfiber.Middleware {
	return appfiber.Middleware{Name: "test.principal", Order: 1, Handler: func(fiberContext fiber.Ctx) error {
		if principal != nil {
			fiberContext.SetContext(auth.WithPrincipal(fiberContext.Context(), principal))
		}
		return fiberContext.Next()
	}}
}

func healthy(t *testing.T, contributions ...health.Contribution) *health.Registry {
	t.Helper()
	registry, err := health.New(health.Spec{Contributions: contributions})
	if err != nil {
		t.Fatalf("the health registry was refused: %v", err)
	}
	return registry
}

func database(importance health.Importance) health.Contribution {
	return health.Contribution{
		Name:       "postgres.primary",
		Code:       "database",
		Importance: importance,
		Probe: health.ProbeFunc(func(context.Context) error {
			return errors.New("dial tcp 10.0.0.7:5432: connection refused")
		}),
	}
}

func serve(t *testing.T, registry *health.Registry, principal auth.Principal, permissions ...auth.Permission) *fiber.App {
	t.Helper()
	route, err := appfiber.Health(appfiber.HealthSpec{Registry: registry, Operator: permissions})
	if err != nil {
		t.Fatalf("a well-formed health route was refused: %v", err)
	}
	fiberApp := fiber.New()
	mounted := appfiber.Mounted{
		Routes:      []appfiber.Route{route},
		Middlewares: []appfiber.Middleware{signedInAs(principal)},
	}
	if err := appfiber.Mount(fiberApp, mounted, prefix); err != nil {
		t.Fatalf("the health route did not survive the boot access gate: %v", err)
	}
	return fiberApp
}

func get(t *testing.T, fiberApp *fiber.App, target string) (int, string) {
	t.Helper()
	response, err := fiberApp.Test(httptest.NewRequest(http.MethodGet, target, nil))
	if err != nil {
		t.Fatalf("serving %s: %v", target, err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", target, err)
	}
	return response.StatusCode, string(body)
}

func TestAReplicaWhoseRequiredDependencyIsDownAsksToBeTakenOutOfRotation(t *testing.T) {
	fiberApp := serve(t, healthy(t, database(health.Required)), nil)

	status, body := get(t, fiberApp, prefix+"/health/ready")

	if status != http.StatusServiceUnavailable {
		t.Fatalf("a replica that cannot reach its database answered %d, so the load balancer keeps sending traffic", status)
	}
	if !strings.Contains(body, `"database"`) {
		t.Fatalf("the readiness body does not carry the stable code an operator greps for: %s", body)
	}
}

func TestADegradedReplicaStaysInRotation(t *testing.T) {
	fiberApp := serve(t, healthy(t, database(health.Degrading)), nil)

	status, body := get(t, fiberApp, prefix+"/health/ready")

	if status != http.StatusOK {
		t.Fatalf("a replica that can still serve half the API answered %d and was removed from rotation", status)
	}
	if !strings.Contains(body, `"degraded"`) {
		t.Fatalf("the replica claims to be fine while a dependency is down: %s", body)
	}
}

func TestThePublicReadinessPageNamesNoHostAndNoDriverError(t *testing.T) {
	fiberApp := serve(t, healthy(t, database(health.Required)), nil)

	_, body := get(t, fiberApp, prefix+"/health/ready")

	if strings.Contains(body, "postgres.primary") || strings.Contains(body, "10.0.0.7") {
		t.Fatalf("the unauthenticated readiness page maps the deployment for whoever asks: %s", body)
	}
}

func TestLivenessAnswersWhileTheDependenciesAreDown(t *testing.T) {
	fiberApp := serve(t, healthy(t, database(health.Required)), nil)

	status, body := get(t, fiberApp, prefix+"/health/live")

	if status != http.StatusOK {
		t.Fatalf("liveness answered %d while the process itself was fine, so the orchestrator restarts it", status)
	}
	if !strings.Contains(body, `"live"`) {
		t.Fatalf("the liveness body says something else: %s", body)
	}
}

func TestTheOperatorPageRefusesACallerWithNoAccount(t *testing.T) {
	fiberApp := serve(t, healthy(t, database(health.Required)), nil, operatorPermission)

	status, body := get(t, fiberApp, prefix+"/health/detail")

	if status != http.StatusUnauthorized {
		t.Fatalf("an anonymous caller read the operator health page: %d", status)
	}
	if strings.Contains(body, "postgres.primary") {
		t.Fatalf("the refusal leaked the detail it refused: %s", body)
	}
}

func TestTheOperatorPageRefusesAnAccountWithoutThePermission(t *testing.T) {
	fiberApp := serve(t, healthy(t, database(health.Required)), operator{}, operatorPermission)

	status, body := get(t, fiberApp, prefix+"/health/detail")

	if status != http.StatusForbidden {
		t.Fatalf("an account without %q read the operator health page: %d", operatorPermission, status)
	}
	if strings.Contains(body, "postgres.primary") {
		t.Fatalf("the refusal leaked the detail it refused: %s", body)
	}
}

func TestTheOperatorPageNamesTheDependencyAndTheDriverError(t *testing.T) {
	principal := operator{permissions: []auth.Permission{operatorPermission}}
	fiberApp := serve(t, healthy(t, database(health.Required)), principal, operatorPermission)

	status, body := get(t, fiberApp, prefix+"/health/detail")

	if status != http.StatusServiceUnavailable {
		t.Fatalf("the operator page reported %d for a replica that is down", status)
	}
	if !strings.Contains(body, "postgres.primary") || !strings.Contains(body, "connection refused") {
		t.Fatalf("the operator page says no more than the public one, so it is worth nothing: %s", body)
	}
}

func TestAHealthRouteThatMountsAnOperatorPageDeclaresIt(t *testing.T) {
	route, err := appfiber.Health(appfiber.HealthSpec{
		Registry: healthy(t),
		Operator: []auth.Permission{operatorPermission},
	})
	if err != nil {
		t.Fatalf("a well-formed health route was refused: %v", err)
	}

	fiberApp := fiber.New()
	if err := appfiber.Mount(fiberApp, appfiber.Mounted{Routes: []appfiber.Route{route}}, prefix); err != nil {
		t.Fatalf("the health route mounts an endpoint it does not declare: %v", err)
	}
}

func TestAHealthRouteWithoutARegistryIsRefused(t *testing.T) {
	if _, err := appfiber.Health(appfiber.HealthSpec{}); err == nil {
		t.Fatal("a health route with nothing to ask was accepted, and would answer ready to everything")
	}
}
