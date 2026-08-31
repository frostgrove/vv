package port

import "context"

type Mapper[In, M any] interface {
	Model(ctx context.Context, in In) (M, error)
}

func Identity[M any]() Mapper[M, M] { return identity[M]{} }

type identity[M any] struct{}

func (identity[M]) Model(_ context.Context, in M) (M, error) { return in, nil }
