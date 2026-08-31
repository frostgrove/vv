package specs

import (
	"context"

	"github.com/frostgrove/vv/crud"
)

type Repo[M any, ID comparable, U any] struct {
	*crud.Repo[M, ID, U]
}

func Executor[M any, ID comparable, U any](r *crud.Repo[M, ID, U]) *Repo[M, ID, U] {
	return &Repo[M, ID, U]{Repo: r}
}

func (this *Repo[M, ID, U]) FindOne(ctx context.Context, s Specification[M], options ...crud.Option) (M, error) {
	var zero M
	items, err := this.GetAll(ctx, take(2, s, options))
	if err != nil {
		return zero, err
	}
	switch len(items) {
	case 0:
		return zero, crud.ErrNotFound
	case 1:
		return items[0], nil
	default:
		return zero, ErrNotUnique
	}
}

func (this *Repo[M, ID, U]) FindFirst(ctx context.Context, s Specification[M], options ...crud.Option) (M, error) {
	return this.First(ctx, append([]crud.Option{As(s)}, options...)...)
}

func (this *Repo[M, ID, U]) FindAll(ctx context.Context, s Specification[M], options ...crud.Option) ([]M, error) {
	return this.GetAll(ctx, append([]crud.Option{As(s)}, options...)...)
}

func (this *Repo[M, ID, U]) FindPage(ctx context.Context, s Specification[M], options ...crud.Option) (crud.PaginatedResponse[M], error) {
	return this.Get(ctx, append([]crud.Option{As(s)}, options...)...)
}

func (this *Repo[M, ID, U]) CountBy(ctx context.Context, s Specification[M]) (int64, error) {
	return this.Count(ctx, As(s))
}

func (this *Repo[M, ID, U]) ExistsBy(ctx context.Context, s Specification[M]) (bool, error) {
	return this.Exists(ctx, As(s))
}

func (this *Repo[M, ID, U]) DeleteBy(ctx context.Context, s Specification[M]) (int64, error) {
	p := Predicate(s)
	if p == nil || crud.MayBeTautologyFor(this.Meta(), p) {
		return 0, ErrUnboundedDelete
	}
	return this.DeleteAll(ctx, crud.Where(p))
}

func (this *Repo[M, ID, U]) UpdateBy(ctx context.Context, s Specification[M], dataTransferObject U) (int64, error) {
	p := Predicate(s)
	if p == nil || crud.MayBeTautologyFor(this.Meta(), p) {
		return 0, ErrUnboundedUpdate
	}
	return this.UpdateAll(ctx, dataTransferObject, crud.Where(p))
}

func take[M any](n int, s Specification[M], options []crud.Option) crud.Option {
	o := crud.Build(append([]crud.Option{As(s)}, options...)...)
	o.Page, o.Offset, o.Limit, o.Unpaged = 0, 0, n, false
	return crud.With(o)
}
