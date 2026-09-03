package crudsqlfx

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/utils/vvdb"
)

func Module(configuration *vvdb.Config) fx.Option {
	return fx.Module("vv.crudsql",
		fx.Provide(
			func(lifecycle fx.Lifecycle) (*sql.DB, error) { return Open(lifecycle, configuration) },
			func(database *sql.DB) (crud.Source, error) {
				ctx, cancel := context.WithTimeout(context.Background(), schemaDeadline(configuration))
				defer cancel()
				return crudsql.Wired(ctx, crudsql.Engine(configuration.Engine), database)
			},
		),

		fx.Invoke(verify),
	)
}

// DefaultSchemaTimeout bounds the one thing this module cannot move out of a
// constructor: reading the schema. `crud.Source` is what every repository in the
// graph depends on, so it has to exist by the time the graph is built, and
// building it means asking the server what its tables are.
//
// Constructors run inside fx.New, which StartTimeout does not reach. Without a
// deadline of its own that read hangs the building of the graph against an
// unreachable server — the process neither starts nor exits — so it gets one
// here. `Pool.ConnectTimeout` is preferred when the configuration states it,
// because a deployment that already said how long it waits for a connection has
// said the useful number.
const DefaultSchemaTimeout = 15 * time.Second

func schemaDeadline(configuration *vvdb.Config) time.Duration {
	if configuration != nil && configuration.Pool.ConnectTimeout > 0 {
		return configuration.Pool.ConnectTimeout
	}
	return DefaultSchemaTimeout
}

// verify is this module's activation, and it is a real one. It has to force the
// source to be built — nothing in a consumer's graph necessarily asks for it,
// and a source nobody constructs is a pool nobody opens — and it registers the
// connection check where a deadline exists. An empty fx.Invoke would do the
// first half and neither state nor hold the second ([[D-092]]).
//
// Only the ping moved to start. The schema read above still happens while the
// graph is being built, because the source it produces is a dependency of it;
// what it gained is a deadline, not a later moment.
func verify(lifecycle fx.Lifecycle, database *sql.DB, _ crud.Source) {
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := database.PingContext(ctx); err != nil {
				return fmt.Errorf("crudsqlfx: connecting to the database: %w", err)
			}
			return nil
		},
	})
}

func Open(lifecycle fx.Lifecycle, configuration *vvdb.Config) (*sql.DB, error) {
	database, err := vvdb.Open(configuration)
	if err != nil {
		return nil, err
	}
	lifecycle.Append(fx.Hook{
		OnStop: func(context.Context) error { return database.Close() },
	})
	return database, nil
}
