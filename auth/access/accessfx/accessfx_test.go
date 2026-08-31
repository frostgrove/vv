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

func host() fx.Option {
	return fx.Provide(
		func() crud.Source { return nil },
		func() *slog.Logger { return slog.New(slog.DiscardHandler) },
	)
}

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
