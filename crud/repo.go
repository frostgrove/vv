package crud

import "context"

type Core[M any, ID comparable] interface {
	Meta() *Meta

	GetByID(ctx context.Context, id ID, options ...Option) (M, error)

	Get(ctx context.Context, options ...Option) (PaginatedResponse[M], error)

	GetAll(ctx context.Context, options ...Option) ([]M, error)

	First(ctx context.Context, options ...Option) (M, error)

	Save(ctx context.Context, m *M) (M, error)

	SaveOnly(ctx context.Context, m *M) error

	Update(ctx context.Context, id ID, dataTransferObject any, options ...Option) (M, error)

	UpdateAll(ctx context.Context, dataTransferObject any, options ...Option) (int64, error)

	Aggregate(ctx context.Context, options ...Option) ([]AggregateRow, error)

	SaveAll(ctx context.Context, models []*M) error

	Delete(ctx context.Context, ids ...ID) (int64, error)

	DeleteAll(ctx context.Context, options ...Option) (int64, error)

	Count(ctx context.Context, options ...Option) (int64, error)

	Exists(ctx context.Context, options ...Option) (bool, error)

	Tx(ctx context.Context, fn func(context.Context) error) error
}

type Middleware[M any, ID comparable] func(Core[M, ID]) Core[M, ID]

type Repo[M any, ID comparable, U any] struct {
	Core[M, ID]
}

func Wrap[M any, ID comparable, U any](c Core[M, ID]) *Repo[M, ID, U] {
	return &Repo[M, ID, U]{Core: c}
}

func (this *Repo[M, ID, U]) Update(ctx context.Context, id ID, dataTransferObject U, options ...Option) (M, error) {
	return this.Core.Update(ctx, id, dataTransferObject, options...)
}

func (this *Repo[M, ID, U]) UpdateAll(ctx context.Context, dataTransferObject U, options ...Option) (int64, error) {
	return this.Core.UpdateAll(ctx, dataTransferObject, options...)
}

func (this *Repo[M, ID, U]) Unwrap() Core[M, ID] { return this.Core }

func Decorate[M any, ID comparable, U any](r *Repo[M, ID, U], mw ...Middleware[M, ID]) *Repo[M, ID, U] {
	return Wrap[M, ID, U](Chain(r.Core, mw...))
}

func Chain[M any, ID comparable](c Core[M, ID], mw ...Middleware[M, ID]) Core[M, ID] {
	for i := len(mw) - 1; i >= 0; i-- {
		if mw[i] != nil {
			c = mw[i](c)
		}
	}
	return c
}

type Base[M any, ID comparable] struct {
	Core[M, ID]
}

func (this Base[M, ID]) Next() Core[M, ID] { return this.Core }
