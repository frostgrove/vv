// Package crudhttp is the half of the HTTP layer that has no framework in it.
//
// A transport binding — crudfiber, crudgin, or one you write — owns routing,
// body binding and how a response is written. Everything else is here: the
// repository interface it holds, the error-to-status table, what a create
// request is not allowed to dictate, and the narrowing a count or a single
// entity applies to the request document.
//
// The split exists because the alternative was two copies of the status switch
// drifting apart. A binding that re-derives any of this is a bug.
package crudhttp

import (
	"context"

	"github.com/shardit-io/rx/crud"
)

// Repository is everything a transport binding needs. crud.Repo[M, ID, U]
// satisfies it, and so does specs.Repo and any struct that embeds either —
// which is how a service layer with extra checks takes the repository's place.
type Repository[M any, ID comparable, U any] interface {
	Meta() *crud.Meta
	GetByID(ctx context.Context, id ID, opts ...crud.Option) (M, error)
	Get(ctx context.Context, opts ...crud.Option) (crud.PaginatedResponse[M], error)
	GetAll(ctx context.Context, opts ...crud.Option) ([]M, error)
	Save(ctx context.Context, m *M) error
	Update(ctx context.Context, id ID, dto U, opts ...crud.Option) (M, error)
	Delete(ctx context.Context, ids ...ID) (int64, error)
	Count(ctx context.Context, opts ...crud.Option) (int64, error)
}
