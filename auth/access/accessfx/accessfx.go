package accessfx

import (
	"context"
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/port"
	"github.com/google/uuid"
)

const (
	subjectGroup = `group:"vv.access.subjects"`
	grantsGroup  = `group:"vv.access.grants"`
)

func AsSubject(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(subjectGroup))
}

func AsGrants(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(grantsGroup))
}

type Registered struct {
	fx.In

	Subjects []*access.MountedSubject `group:"vv.access.subjects"`
	Declared []access.ModuleGrants    `group:"vv.access.grants"`
}

func Module(configuration access.Config, options ...auth.Option) fx.Option {
	return fx.Module("vv.access",
		fx.Provide(
			func(source crud.Source, logger *slog.Logger) (*access.Runtime, error) {
				return access.New(access.RuntimeSpec{
					Source: source,
					Config: configuration,
					Logger: logger,
				})
			},
			newGrants,
			func(runtime *access.Runtime, grants *access.GrantsService) *auth.Guard {
				return newAdminGuard(runtime, grants, options)
			},
			newSetPassword,
			newRoleService,
			newPermissionService,
			newGrantService,
		),
		fx.Invoke(syncOnStart),
	)
}

func newGrants(runtime *access.Runtime, registered Registered) (*access.GrantsService, error) {
	if len(registered.Subjects) == 0 {
		return nil, fmt.Errorf("accessfx: no subject is mounted; nothing can sign in")
	}
	return runtime.Grants(), nil
}

func newAdminGuard(runtime *access.Runtime, _ *access.GrantsService, options []auth.Option) *auth.Guard {
	return runtime.AdminGuard(options...)
}

func newSetPassword(runtime *access.Runtime, _ *access.GrantsService) *access.SetPasswordUseCase {
	return runtime.SetPassword()
}

func newRoleService(runtime *access.Runtime) *access.RoleService {
	return access.NewRoleService(runtime.Store())
}

func newPermissionService(runtime *access.Runtime) *port.DefaultService[access.Permission, uuid.UUID, access.PermissionUpdate] {
	return access.NewPermissionService(runtime.Store())
}

func newGrantService(runtime *access.Runtime) *access.GrantService {
	return access.NewGrantService(runtime.Store())
}

func syncOnStart(lifecycle fx.Lifecycle, runtime *access.Runtime, registered Registered, _ *access.GrantsService) {
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			runtime.Declare(registered.Declared...)
			return runtime.Sync(ctx)
		},
	})
}
