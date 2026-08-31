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

func Module(configuration *vvdb.Config) fx.Option {
	return fx.Module("vv.crudsql",
		fx.Provide(
			func(lifecycle fx.Lifecycle) (*sql.DB, error) { return Open(lifecycle, configuration) },
			func(database *sql.DB) (crud.Source, error) {
				return crudsql.Wired(context.Background(), crudsql.Engine(configuration.Engine), database)
			},
		),

		fx.Invoke(func(crud.Source) {}),
	)
}

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
