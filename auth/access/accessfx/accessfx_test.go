package accessfx_test

import (
	"log/slog"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/auth/access/accessfx"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/port"
	"github.com/google/uuid"
)

// What this package is for is that an application does not assemble the context
// by hand, which means a piece it forgot to provide has to be a wiring failure
// and not a nil dereference on the first sign-in. fx.ValidateApp resolves the
// graph without running a constructor, so these check the shape rather than the
// behaviour — which is the half that can be checked without a database.

// everything the context asks of its host.
func host() fx.Option {
	return fx.Provide(
		func() crud.Source { return nil },
		func() *slog.Logger { return slog.New(slog.DiscardHandler) },
	)
}

// uses names every component this module promises, so that one dropped from
// Module is a failure here rather than an import error in an application.
func uses() fx.Option {
	return fx.Invoke(func(
		*access.Runtime,
		*access.GrantsService,
		*auth.Guard,
		*access.SetPasswordUseCase,
		*access.RoleService,
		*port.DefaultService[access.Permission, uuid.UUID, access.PermissionUpdate],
		*access.GrantService,
	) {
	})
}

func TestEveryComponentTheContextOffersIsResolvable(t *testing.T) {
	if err := fx.ValidateApp(host(), accessfx.Module(access.Config{}), uses()); err != nil {
		t.Fatalf("the access graph is incomplete: %v", err)
	}
}

// The control on the test above: it is only meaningful if a missing dependency
// really does fail validation. Without it, a graph that resolved nothing at all
// would pass.
func TestAHostThatProvidesNoSourceIsRefused(t *testing.T) {
	err := fx.ValidateApp(
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		accessfx.Module(access.Config{}),
		uses(),
	)
	if err == nil {
		t.Fatal("an application with no crud.Source validated, so the check above proves nothing")
	}
}
