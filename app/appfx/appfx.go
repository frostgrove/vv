package appfx

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/app"
)

const seederGroup = `group:"vv.app.seeders"`

func AsSeeder(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(seederGroup))
}

type Seeders struct {
	fx.In

	All []app.Seeder `group:"vv.app.seeders"`

	Logger *slog.Logger `optional:"true"`
}

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
