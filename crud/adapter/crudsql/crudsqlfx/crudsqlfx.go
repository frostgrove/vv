// Package crudsqlfx opens the application's database and puts a source in an fx
// graph.
//
//	fx.Options(
//		crudsqlfx.Module(&configuration.Db),
//		fx.Provide(users.NewRepository),
//	)
//
// It provides a `*sql.DB` and a [crud.Source] over it, with the error subsystem
// wired — see [crudsql.Wired] for the three pieces that have to be in place and
// what each of them being missing looks like from outside.
//
// # What it does not do
//
// It registers no driver. Which driver answers `sql.Open("pgx", ...)` is the
// application's decision and always was: the library never opens a connection it
// was not handed the means to open ([[D-057]]). An application imports the
// driver for its side effect, next to this:
//
//	import _ "github.com/jackc/pgx/v5/stdlib"
//
// # Why this is a module and not a package of the library
//
// The framework holds no container ([[D-037]]). What it holds here is an adapter
// to one the consumer chose ([[D-074]]).
package crudsqlfx

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/utils/vvdb"
)

// Module opens the pool and provides the source.
//
// The configuration is passed rather than resolved, because a deployment keeps
// it inside a configuration struct of its own shape and this package must not
// have an opinion about that shape.
func Module(configuration *vvdb.Config) fx.Option {
	return fx.Module("vv.crudsql",
		fx.Provide(
			func(lifecycle fx.Lifecycle) (*sql.DB, error) { return Open(lifecycle, configuration) },
			func(database *sql.DB) (crud.Source, error) {
				return crudsql.Wired(context.Background(), crudsql.Engine(configuration.Engine), database)
			},
		),
		// The transport may not depend on a repository yet, and invalid database
		// configuration should still fail application start rather than the
		// first request that happens to need one.
		fx.Invoke(func(crud.Source) {}),
	)
}

// Open opens the pool, proves it answers, and closes it on shutdown.
//
// The ping is not ceremony. sql.Open validates a DSN and connects to nothing, so
// without it a wrong host, a wrong password and a database that is not running
// all look like a healthy start-up, and the failure arrives on the first request
// that needed a row — as a 500, minutes later, in a log nobody is watching.
func Open(lifecycle fx.Lifecycle, configuration *vvdb.Config) (*sql.DB, error) {
	database, err := vvdb.Open(configuration)
	if err != nil {
		return nil, err
	}
	if err := database.PingContext(context.Background()); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("crudsqlfx: connecting to the database: %w", err)
	}
	lifecycle.Append(fx.Hook{
		OnStop: func(context.Context) error { return database.Close() },
	})
	return database, nil
}
