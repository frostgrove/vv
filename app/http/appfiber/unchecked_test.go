package appfiber_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"

	"github.com/frostgrove/vv/app/http/appfiber"
	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
)

func refusingUnchecked() fx.Option {
	return provide(func() appfiber.UncheckedRule { return appfiber.RefusingUnchecked })
}

func handWritten(path string, needs ...auth.Permission) func() appfiber.Route {
	return newRoute(route{
		mount:   func(r fiber.Router) { r.Get(path, handler) },
		declare: []authhttp.Endpoint{authhttp.Requires(http.MethodGet, path, needs...)},
	})
}

type smuggled struct{}

func (this smuggled) Mount(r fiber.Router) { r.Get("/smuggled", handler) }

func (this smuggled) Access() []authhttp.Endpoint {
	return []authhttp.Endpoint{authhttp.Requires(http.MethodGet, "/smuggled", permRead)}
}

func TestADeclaredPermissionNoMountedCheckAnswersForStopsTheStart(t *testing.T) {
	err := fx.New(
		provide(newFiber),
		provide(appfiber.AsRoute(handWritten("/things", permRead))),
		refusingUnchecked(),
		mounting(),
	).Err()

	if err == nil {
		t.Fatal("a route that declares a permission and mounts no check started the application")
	}
	if !strings.Contains(err.Error(), "GET /things") || !strings.Contains(err.Error(), string(permRead)) {
		t.Fatalf("the refusal names neither the operation nor the permission it declares: %v", err)
	}
}

func TestAnOperationRegisteredThroughTheRegistrarIsNeverReportedAsUnchecked(t *testing.T) {
	set := appfiber.Routes("").GET("/things", appfiber.Requires(permRead), handler)

	err := fx.New(
		provide(newFiber),
		provide(appfiber.AsRoute(routeFrom(t, set))),
		refusingUnchecked(),
		mounting(),
	).Err()

	if err != nil {
		t.Fatalf("an operation whose check the registrar mounted was reported as unchecked: %v", err)
	}
}

func TestAnUncheckedDeclarationIsNamedEvenWhenNothingRefusesIt(t *testing.T) {
	var written bytes.Buffer

	err := fx.New(
		provide(newFiber),
		provide(func() *slog.Logger { return slog.New(slog.NewTextHandler(&written, nil)) }),
		provide(appfiber.AsRoute(handWritten("/things", permRead))),
		mounting(),
	).Err()
	if err != nil {
		t.Fatalf("the default rule refused a hand-written route: %v", err)
	}

	if !strings.Contains(written.String(), "/things") {
		t.Fatalf("nothing said that %s is declared and checked by nothing the registrar mounted: %s",
			permRead, written.String())
	}
}

func TestAnExcusedContributorPassesAndTheOneNobodyExcusedDoesNot(t *testing.T) {
	err := fx.New(
		provide(newFiber),
		provide(appfiber.AsRoute(handWritten("/things", permRead))),
		provide(appfiber.AsRoute(func() appfiber.Route { return smuggled{} })),
		provide(func() appfiber.UncheckedRule {
			return appfiber.ExcusingUnchecked(
				"its use case checks the permission until it moves to the registrar",
				"appfiber_test.route")
		}),
		mounting(),
	).Err()

	if err == nil {
		t.Fatal("a contributor nobody excused was let through with the excused one")
	}
	if strings.Contains(err.Error(), "GET /things") {
		t.Fatalf("the excused contributor was refused anyway: %v", err)
	}
	if !strings.Contains(err.Error(), "GET /smuggled") {
		t.Fatalf("the refusal does not name the contributor the excuse says nothing about: %v", err)
	}
}

func TestAnExcuseThatSaysNothingExcusesNobody(t *testing.T) {
	err := fx.New(
		provide(newFiber),
		provide(appfiber.AsRoute(handWritten("/things", permRead))),
		provide(func() appfiber.UncheckedRule { return appfiber.ExcusingUnchecked("", "appfiber_test.route") }),
		mounting(),
	).Err()

	if err == nil {
		t.Fatal("an excuse with no reason behind it exempted a route from the check it declares")
	}
}

func TestTheOperatorHealthPageIsCheckedByWhatMountedIt(t *testing.T) {
	route, err := appfiber.Health(appfiber.HealthSpec{
		Registry: healthy(t),
		Operator: []auth.Permission{operatorPermission},
	})
	if err != nil {
		t.Fatalf("a well-formed health route was refused: %v", err)
	}

	started := fx.New(
		provide(newFiber),
		provide(appfiber.AsRoute(func() appfiber.Route { return route })),
		refusingUnchecked(),
		mounting(),
	).Err()

	if started != nil {
		t.Fatalf("the operator health page declares a permission its own mount does not check: %v", started)
	}
}
