package healthfx_test

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/health"
	"github.com/frostgrove/vv/health/healthfx"
)

func database() health.Contribution {
	return health.Contribution{
		Name:       "postgres.primary",
		Code:       "database",
		Importance: health.Required,
		Probe:      health.ProbeFunc(func(context.Context) error { return errors.New("connection refused") }),
	}
}

func TestAModulesCheckReachesTheRegistryTheTransportAnswersFrom(t *testing.T) {
	var registry *health.Registry
	err := fx.New(
		fx.NopLogger,
		fx.Provide(healthfx.AsCheck(database)),
		healthfx.Auto(),
		fx.Populate(&registry),
	).Err()
	if err != nil {
		t.Fatalf("a module that contributed one check did not start: %v", err)
	}

	report := registry.Ready(context.Background())

	if report.Status != health.StatusDown {
		t.Fatalf("the contributed check was not asked: the registry reported %q", report.Status)
	}
	if len(report.Codes) != 1 || report.Codes[0] != "database" {
		t.Fatalf("the contributed check's public code did not reach the report: %v", report.Codes)
	}
}

func TestTwoChecksOfOneNameKeepTheApplicationFromStarting(t *testing.T) {
	second := func() health.Contribution {
		check := database()
		check.Code = "database.replica"
		return check
	}
	err := fx.New(
		fx.NopLogger,
		fx.Provide(healthfx.AsCheck(database), healthfx.AsCheck(second)),
		healthfx.Auto(),
		fx.Invoke(func(*health.Registry) {}),
	).Err()

	if !errors.Is(err, health.ErrRegistration) {
		t.Fatalf("an application whose two modules claim one check name started: %v", err)
	}
}

func TestAnApplicationWithNoChecksIsReady(t *testing.T) {
	var registry *health.Registry
	err := fx.New(fx.NopLogger, healthfx.Auto(), fx.Populate(&registry)).Err()
	if err != nil {
		t.Fatalf("an application with no dependencies to check did not start: %v", err)
	}

	if status := registry.Ready(context.Background()).Status; status != health.StatusReady {
		t.Fatalf("a process with nothing to check reported %q", status)
	}
}
