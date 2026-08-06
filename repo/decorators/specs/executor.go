package specs

import (
	"context"

	"rx-crud/crud"
)

// Repo is a repository with the JpaSpecificationExecutor surface bolted on. It
// embeds the plain repository, so GetByID/Save/Update/... all still work.
//
//	users := specs.Executor(Users.Bind(db))
//	adults, err := users.FindAll(ctx, specs.Where(isActive).And(User_.Age.Gte(18)))
//
// Three JPA names collide with methods the basic repository already has, so
// they carry a By suffix: count -> CountBy, exists -> ExistsBy, delete ->
// DeleteBy.
type Repo[M any, ID comparable, U any] struct {
	crud.Repo[M, ID, U]
}

// Executor decorates a repository with specification queries. All three type
// parameters are inferred from the argument.
func Executor[M any, ID comparable, U any](r crud.Repo[M, ID, U]) Repo[M, ID, U] {
	return Repo[M, ID, U]{Repo: r}
}

// FindOne returns the single row matching the specification. It reports
// crud.ErrNotFound when there is none and ErrNotUnique when there is more than
// one — the same contract as JPA's findOne.
func (r Repo[M, ID, U]) FindOne(ctx context.Context, s Specification[M], opts ...crud.Option) (M, error) {
	var zero M
	items, err := r.GetAll(ctx, append([]crud.Option{As(s), crud.Limit(2)}, opts...)...)
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

// FindFirst returns the first match, or crud.ErrNotFound. Pair it with
// crud.OrderBy to make "first" mean something.
func (r Repo[M, ID, U]) FindFirst(ctx context.Context, s Specification[M], opts ...crud.Option) (M, error) {
	var zero M
	items, err := r.GetAll(ctx, append([]crud.Option{As(s), crud.Limit(1)}, opts...)...)
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, crud.ErrNotFound
	}
	return items[0], nil
}

// FindAll returns every row matching the specification.
func (r Repo[M, ID, U]) FindAll(ctx context.Context, s Specification[M], opts ...crud.Option) ([]M, error) {
	return r.GetAll(ctx, append([]crud.Option{As(s)}, opts...)...)
}

// FindPage returns a page of rows matching the specification — findAll(spec,
// pageable).
func (r Repo[M, ID, U]) FindPage(ctx context.Context, s Specification[M], opts ...crud.Option) (crud.PaginatedResponse[M], error) {
	return r.Get(ctx, append([]crud.Option{As(s)}, opts...)...)
}

// CountBy counts matching rows.
func (r Repo[M, ID, U]) CountBy(ctx context.Context, s Specification[M]) (int64, error) {
	return r.Count(ctx, As(s))
}

// ExistsBy reports whether anything matches.
func (r Repo[M, ID, U]) ExistsBy(ctx context.Context, s Specification[M]) (bool, error) {
	return r.Exists(ctx, As(s))
}

// DeleteBy removes every matching row and reports how many went away.
func (r Repo[M, ID, U]) DeleteBy(ctx context.Context, s Specification[M]) (int64, error) {
	p := Predicate(s)
	if p == nil {
		return 0, ErrUnboundedDelete
	}
	return r.DeleteAll(ctx, crud.Where(p))
}
