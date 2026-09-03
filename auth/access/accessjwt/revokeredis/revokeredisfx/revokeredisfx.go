package revokeredisfx

import (
	"context"
	"log/slog"

	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"

	"github.com/frostgrove/vv/auth/access/accessjwt"
	"github.com/frostgrove/vv/auth/access/accessjwt/revokeredis"
)

type Dependencies struct {
	fx.In

	Client redis.UniversalClient

	Logger *slog.Logger `optional:"true"`
}

func Auto() fx.Option { return Revoking() }

func Revoking(options ...revokeredis.Option) fx.Option {
	return fx.Module("vv.revokeredis",
		fx.Provide(
			func(dependencies Dependencies) (*revokeredis.List, error) {
				return revokeredis.New(dependencies.Client, optionsFor(dependencies, options)...)
			},
			func(list *revokeredis.List) accessjwt.RevocationList { return list },
		),
		Verifying(),
	)
}

func Verifying() fx.Option { return fx.Invoke(verifyOnStart) }

func optionsFor(dependencies Dependencies, options []revokeredis.Option) []revokeredis.Option {
	if dependencies.Logger == nil {
		return options
	}
	return append([]revokeredis.Option{revokeredis.Logger(dependencies.Logger)}, options...)
}

func verifyOnStart(lifecycle fx.Lifecycle, list *revokeredis.List) {
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			_, err := list.VerifyEvictionPolicy(ctx)
			return err
		},
	})
}
