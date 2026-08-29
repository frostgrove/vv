package crud

import "context"

// Core is the decoratable seam. It carries two type parameters so that a
// middleware can be written once and still infer both from its arguments; the
// update DTO is erased to `any` here and re-typed by Repo above it.
//
// Implement it only if you are writing a decorator — the SQL repository is in
// crud/sqlrepo.
type Core[M any, ID comparable] interface {
	// Meta describes the bound model and table.
	Meta() *Meta

	// GetByID returns one row or ErrNotFound. Options apply to that read, which
	// is what makes crud.Preload and crud.Select work on a single-entity fetch.
	GetByID(ctx context.Context, id ID, options ...Option) (M, error)
	// Get returns a page of rows.
	Get(ctx context.Context, options ...Option) (PaginatedResponse[M], error)
	// GetAll returns every matching row, unpaged unless options say otherwise.
	GetAll(ctx context.Context, options ...Option) ([]M, error)
	// First returns the first matching row or ErrNotFound. It accepts the same
	// narrowing options as Get; paging controls are normalised to one row.
	First(ctx context.Context, options ...Option) (M, error)
	// Save inserts when the primary key is unset and upserts otherwise. It
	// returns the row the database stored — including generated and normalised
	// values — and never changes m.
	Save(ctx context.Context, m *M) (M, error)
	// SaveOnly performs the same insert-or-upsert without reading a row back.
	// It never changes m. Use it for fire-and-forget writes where generated or
	// trigger-owned values are not needed.
	SaveOnly(ctx context.Context, m *M) error
	// Update loads the row, diffs the DTO against it and writes only what
	// actually changed. dto is always the U of the enclosing Repo. Options
	// narrow both halves — the load and the UPDATE's own WHERE — which is how a
	// decorator keeps a row that moved out of scope between them from being
	// written anyway.
	Update(ctx context.Context, id ID, dataTransferObject any, options ...Option) (M, error)
	// UpdateAll writes the DTO's defined columns to every row the options match,
	// in one statement, and reports how many rows the database says it touched.
	// It is Update's filtered partner, the way DeleteAll is Delete's.
	UpdateAll(ctx context.Context, dataTransferObject any, options ...Option) (int64, error)

	// Aggregate runs a grouped summary under the same narrowing as every other
	// read. It is on the seam rather than beside it so a decorator cannot be
	// bypassed by asking for a total instead of for rows.
	Aggregate(ctx context.Context, options ...Option) ([]AggregateRow, error)

	// SaveAll writes many rows in one statement and never changes its models. It
	// is on the seam for the same reason Aggregate is: a decorator that checks
	// writes has to see this one.
	SaveAll(ctx context.Context, models []*M) error
	// Delete removes rows by id and reports how many went away.
	Delete(ctx context.Context, ids ...ID) (int64, error)
	// DeleteAll removes everything matching the options.
	DeleteAll(ctx context.Context, options ...Option) (int64, error)
	// Count counts matching rows.
	Count(ctx context.Context, options ...Option) (int64, error)
	// Exists reports whether any row matches.
	Exists(ctx context.Context, options ...Option) (bool, error)
	// Tx runs fn in a transaction, joining one already present in ctx.
	Tx(ctx context.Context, fn func(context.Context) error) error
}

// Middleware wraps a Core. The type parameters are inferred from whatever the
// decorator is configured with, so call sites stay free of explicit generics.
type Middleware[M any, ID comparable] func(Core[M, ID]) Core[M, ID]

// Repo is the typed façade a consumer holds. It promotes every Core method and
// re-types Update against the update DTO, so the whole surface is compile-time
// checked while decorators only ever deal with two type parameters.
type Repo[M any, ID comparable, U any] struct {
	Core[M, ID]
}

// Wrap lifts a Core into a typed Repo.
//
// Repositories are passed by pointer throughout the public composition API.
// Repo is currently a small façade over Core, but that implementation detail
// should not force consumers to copy a dependency to decorate or inject it.
func Wrap[M any, ID comparable, U any](c Core[M, ID]) *Repo[M, ID, U] {
	return &Repo[M, ID, U]{Core: c}
}

// Update applies a partial update DTO and returns the refreshed model. Options
// narrow the row it may touch, exactly as they narrow a read.
//
// Fields of U are matched to model fields by name. A *T field is applied when
// non-nil, an Opt[T] field when defined (a null Opt writes SQL NULL), and a
// plain T field is always applied. Only fields whose value actually differs
// from the stored row reach the UPDATE statement; when nothing differs the
// loaded row is returned without a write.
func (this *Repo[M, ID, U]) Update(ctx context.Context, id ID, dataTransferObject U, options ...Option) (M, error) {
	return this.Core.Update(ctx, id, dataTransferObject, options...)
}

// UpdateAll applies a partial update DTO to every row the options match and
// reports how many rows were touched. It is one statement, so "deactivate every
// user in this tenant" costs one round trip rather than one per row.
//
// It differs from Update in exactly one way, and the difference is forced: there
// is no single row to diff against, so every field the DTO defines is written to
// every matching row. Undefined fields are still never written and a null Opt
// still writes NULL.
//
// The row count is the database's own, and the two engines count differently:
// PostgreSQL reports the rows it matched, MySQL the rows it actually changed, so
// writing a value a row already holds is counted by one and not by the other.
func (this *Repo[M, ID, U]) UpdateAll(ctx context.Context, dataTransferObject U, options ...Option) (int64, error) {
	return this.Core.UpdateAll(ctx, dataTransferObject, options...)
}

// Unwrap returns the decorated Core, e.g. to add another layer.
func (this *Repo[M, ID, U]) Unwrap() Core[M, ID] { return this.Core }

// Decorate applies middlewares to an existing typed repo. The first middleware
// ends up outermost, so it observes calls first.
func Decorate[M any, ID comparable, U any](r *Repo[M, ID, U], mw ...Middleware[M, ID]) *Repo[M, ID, U] {
	return Wrap[M, ID, U](Chain(r.Core, mw...))
}

// Chain applies middlewares to a Core; mw[0] ends up outermost.
func Chain[M any, ID comparable](c Core[M, ID], mw ...Middleware[M, ID]) Core[M, ID] {
	for i := len(mw) - 1; i >= 0; i-- {
		if mw[i] != nil {
			c = mw[i](c)
		}
	}
	return c
}

// Base is an embeddable pass-through decorator: embed it and override only the
// methods you care about.
type Base[M any, ID comparable] struct {
	Core[M, ID]
}

// Next returns the wrapped core.
func (this Base[M, ID]) Next() Core[M, ID] { return this.Core }
