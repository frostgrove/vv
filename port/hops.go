package port

import (
	"context"

	"github.com/frostgrove/vv/errs"
)

type hopsKey struct{}

func WithHops(ctx context.Context, hops []errs.Resolver) context.Context {
	if len(hops) == 0 {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, hopsKey{}, append([]errs.Resolver(nil), hops...))
}

func HopsFrom(ctx context.Context) []errs.Resolver {
	if ctx == nil {
		return nil
	}
	hops, _ := ctx.Value(hopsKey{}).([]errs.Resolver)
	return append([]errs.Resolver(nil), hops...)
}
