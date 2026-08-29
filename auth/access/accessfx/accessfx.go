// Package accessfx wires the access context into an fx graph.
//
// The context itself is `github.com/frostgrove/vv/auth/access`, and it is built
// through one factory: an application never assembles a Store, a resolver, a
// hasher or an authenticator by hand. What is here is the graph that calls that
// factory, and the two value groups a bounded context contributes to.
//
//	fx.Options(
//		accessfx.Module(configuration.Access),
//		fx.Provide(
//			accessfx.AsSubject(mountUsers),
//			accessfx.AsGrants(usersMayDo),
//		),
//	)
//
// # The ordering this exists to express
//
// Everything that needs to know about every subject — the resolver, the admin
// guard, the administrative password reset — depends on [Registered]. fx cannot
// build any of them until each contributor has run, so none of them can be
// handed out knowing half the directories. A guard assembled before the last
// Mount would verify some credential formats and silently refuse the rest, and a
// use case built early would panic on its first call at a point where the wiring
// looks finished.
//
// # What this module does not do
//
// It mounts no routes and names no subject type. Which callers this deployment
// has, what identifies them, and where their sign-in surface lives are the
// application's ([[D-066]]) — this only holds the graph they are collected in.
//
// # Why this is a module and not a package of the library
//
// The framework holds no container ([[D-037]]). What it holds here is an adapter
// to one the consumer chose: fx keeps the graph, and nothing in the access
// context learns how to find a component ([[D-074]]).
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

// AsSubject annotates a constructor so its mounted subject joins the group.
func AsSubject(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(subjectGroup))
}

// AsGrants annotates a constructor returning an [access.ModuleGrants] — what a
// module declares its permissions and roles to be.
func AsGrants(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(grantsGroup))
}

// Registered is every mounted subject and every declaration fx collected.
type Registered struct {
	fx.In

	Subjects []*access.MountedSubject `group:"vv.access.subjects"`
	Declared []access.ModuleGrants    `group:"vv.access.grants"`
}

// Module wires the context.
//
// The configuration is passed rather than resolved, because a deployment keeps
// it inside a configuration struct of its own shape and this package must not
// have an opinion about that shape.
//
// The options are the admin guard's, and the one there is a reason to pass is
// where the credential is read from: a deployment whose browser holds the access
// token in an HttpOnly cookie hands over `authhttp.Cookie(name)`, or every
// request it makes arrives with no Authorization header and no principal.
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

// newGrants answers the resolver, which exists once every subject is mounted.
//
// No subject at all is a misconfiguration and not an empty deployment: every
// request would resolve to a principal with no profile, and every sign-in would
// refuse for a reason nobody can find.
func newGrants(runtime *access.Runtime, registered Registered) (*access.GrantsService, error) {
	if len(registered.Subjects) == 0 {
		return nil, fmt.Errorf("accessfx: no subject is mounted; nothing can sign in")
	}
	return runtime.Grants(), nil
}

// newAdminGuard is the verifier for routes not under any subject's prefix.
//
// It takes the resolver rather than the runtime so it inherits the same
// ordering: a chain assembled before the last Mount would verify some formats
// and silently refuse the rest.
func newAdminGuard(runtime *access.Runtime, _ *access.GrantsService, options []auth.Option) *auth.Guard {
	return runtime.AdminGuard(options...)
}

// newSetPassword is the administrative password reset, and what makes an account
// somebody provisioned able to sign in at all.
//
// It takes the resolver for the reason newAdminGuard does: this use case asks a
// directory for the subject's identifier, and the directories are not all
// registered until the last Mount.
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

// syncOnStart folds every module's declaration into the tables.
//
// On the lifecycle hook rather than in the invoke body, so that a database that
// is not reachable yet fails start-up with the rest of the start-up rather than
// during dependency construction — where the error reads as a wiring problem.
func syncOnStart(lifecycle fx.Lifecycle, runtime *access.Runtime, registered Registered, _ *access.GrantsService) {
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			runtime.Declare(registered.Declared...)
			return runtime.Sync(ctx)
		},
	})
}
