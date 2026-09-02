package appfiber_test

import (
	"net/http"
	"strings"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/app/http/appfiber"
	"github.com/frostgrove/vv/auth"
)

type deadJobs struct{ ran bool }

func (this *deadJobs) Operations() *appfiber.RouteSet {
	return appfiber.Routes("/ops/jobs").GET("/dead", appfiber.Requires(permRead), answering(&this.ran))
}

type twiceOverTheSamePath struct{}

func (this twiceOverTheSamePath) Operations() *appfiber.RouteSet {
	var ran bool
	return appfiber.Routes("/ops/jobs").
		GET("/dead", appfiber.Requires(permRead), answering(&ran)).
		GET("/dead", appfiber.Public("a second opinion about the same path"), answering(&ran))
}

func TestAModuleThatOnlyRegistersOperationsIsMountedDeclaredAndChecked(t *testing.T) {
	module := &deadJobs{}

	mounted := servedBy(t,
		provide(appfiber.AsMiddleware(func() appfiber.Middleware { return signedInAs(operator{}) })),
		provide(appfiber.AsOperations(func() *deadJobs { return module })),
		refusingUnchecked(),
	)

	if got := request(t, mounted, "/api/v1/ops/jobs/dead"); got != http.StatusForbidden {
		t.Fatalf("a caller without %s got %d from a module that contributed operations rather than a route", permRead, got)
	}
	if module.ran {
		t.Fatal("the handler ran for a caller that holds none of the permissions the operations declare")
	}
}

func TestAModuleThatOnlyRegistersOperationsAdmitsTheCallerItsPolicyNames(t *testing.T) {
	module := &deadJobs{}

	mounted := servedBy(t,
		provide(appfiber.AsMiddleware(func() appfiber.Middleware {
			return signedInAs(operator{permissions: []auth.Permission{permRead}})
		})),
		provide(appfiber.AsOperations(func() *deadJobs { return module })),
		refusingUnchecked(),
	)

	if got := request(t, mounted, "/api/v1/ops/jobs/dead"); got != http.StatusOK {
		t.Fatalf("a caller holding %s got %d, so the contributed operations refuse what they should admit", permRead, got)
	}
	if !module.ran {
		t.Fatal("the handler behind the contributed operations never ran")
	}
}

func TestOperationsTheRegistrarRefusesNameTheModuleTheyCameFrom(t *testing.T) {
	err := fx.New(
		provide(newFiber),
		provide(appfiber.AsOperations(func() twiceOverTheSamePath { return twiceOverTheSamePath{} })),
		mounting(),
	).Err()

	if err == nil {
		t.Fatal("a module registered the same path twice and the application started")
	}
	if !strings.Contains(err.Error(), "twiceOverTheSamePath") {
		t.Fatalf("the refusal does not say which module the operations came from: %v", err)
	}
}
