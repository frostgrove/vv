// Package appfx hands the composition root to uber/fx.
//
// It is the seam between a bounded context and a command: a module contributes
// to a value group typed here, and the command walks the group. Without it the
// command would name every module — and could not be extended without editing it
// — or every module would import the command, which is the coupling a bounded
// context exists to avoid.
//
// # Why this is a module and not a package of the library
//
// The framework holds no container ([[D-037]]). What it holds here is an adapter
// to one the consumer chose: fx keeps the graph, resolves by type and reports
// what it could not build, and nothing in `github.com/frostgrove/vv` learns how
// to find a component. A consumer who wires by hand never imports this and never
// resolves fx in `go.sum` ([[D-074]]).
package appfx

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/app"
)

const seederGroup = `group:"vv.app.seeders"`

// AsSeeder annotates a constructor so its result joins the seeder group.
//
//	fx.Provide(appfx.AsSeeder(newRoleSeeder))
func AsSeeder(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(seederGroup))
}

// Seeders is every seeder fx collected.
//
// Depending on it is also how "after every contributor has run" is expressed
// rather than remembered: nothing that takes this can be built until the group
// is complete.
type Seeders struct {
	fx.In

	All []app.Seeder `group:"vv.app.seeders"`
	// Logger is where the run is reported, and it is optional: an application
	// that has one in its graph gets the seed log through it without saying so,
	// and one that does not still runs. A required dependency here would make
	// wiring a logger the price of seeding at all.
	Logger *slog.Logger `optional:"true"`
}

// Seeding provides the [app.Runner] over whatever joined the group.
//
// The environment and the set of environments this deployment has are the
// consumer's — the framework has no opinion about how many there are or what
// they are called — so they are passed here rather than read from a
// configuration type this package would have to define.
//
//	fx.Options(
//		accounts.Module(),
//		roles.Module(),
//		appfx.Seeding(app.Seeding{Env: cfg.Env, Known: cfg.Envs}),
//	)
//
// It is deliberately not in a server's graph. Seeding is something somebody
// runs, not something a deploy does on the way up.
func Seeding(spec app.Seeding) fx.Option {
	return fx.Module("vv.app.seeding",
		fx.Provide(func(registered Seeders) (*app.Runner, error) {
			if spec.Logger == nil {
				spec.Logger = registered.Logger
			}
			return app.NewRunner(registered.All, spec)
		}),
	)
}
