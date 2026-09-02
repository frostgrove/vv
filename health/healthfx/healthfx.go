package healthfx

import (
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/health"
)

const checkGroup = `group:"vv.health.checks"`

func AsCheck(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(checkGroup))
}

type Registered struct {
	fx.In

	All []health.Contribution `group:"vv.health.checks"`
}

type Spec struct {
	Timeout time.Duration

	Freshness time.Duration
}

func Checking(spec Spec) fx.Option {
	return fx.Module("vv.health",
		fx.Provide(func(registered Registered) (*health.Registry, error) {
			return health.New(health.Spec{
				Contributions: registered.All,
				Timeout:       spec.Timeout,
				Freshness:     spec.Freshness,
			})
		}),
	)
}

func Auto() fx.Option { return Checking(Spec{}) }
