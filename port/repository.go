package port

import (
	"context"

	"github.com/shardit-io/vv/crud"
)

// Repository is everything a Service needs. crud.Repo[M, ID, U] satisfies it,
// and so does specs.Repo and any struct that embeds either — which is how a
// service layer with extra checks takes the repository's place ([[D-022]]).
//
// It is narrow on purpose: it lists what the routes call, not what the
// repository can do. Every method added here is a method every hand-written
// stand-in has to supply.
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
