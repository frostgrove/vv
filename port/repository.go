package port

import (
	"context"

	"github.com/frostgrove/vv/crud"
)

type Repository[M any, ID comparable, U any] interface {
	Meta() *crud.Meta
	GetByID(ctx context.Context, id ID, options ...crud.Option) (M, error)
	Get(ctx context.Context, options ...crud.Option) (crud.PaginatedResponse[M], error)
	GetAll(ctx context.Context, options ...crud.Option) ([]M, error)
	Save(ctx context.Context, m *M) (M, error)
	Update(ctx context.Context, id ID, dataTransferObject U, options ...crud.Option) (M, error)
	Delete(ctx context.Context, ids ...ID) (int64, error)
	Count(ctx context.Context, options ...crud.Option) (int64, error)
}
