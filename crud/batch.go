package crud

import "context"

type BatchOption struct {
	portable bool
}

func PortableBatch() BatchOption { return BatchOption{portable: true} }

func UsesPortableBatch(options ...BatchOption) bool {
	for _, option := range options {
		if option.portable {
			return true
		}
	}
	return false
}

type BatchInserter[M any] interface {
	InsertBatch(ctx context.Context, models []*M, options ...BatchOption) error
}

func InsertBatchOf[M any, ID comparable](core Core[M, ID], ctx context.Context, models []*M, options ...BatchOption) (error, bool) {
	if len(models) == 0 {
		return nil, true
	}
	if inserter, ok := core.(BatchInserter[M]); ok {
		return inserter.InsertBatch(ctx, models, options...), true
	}
	return nil, false
}

func (this *Repo[M, ID, U]) InsertBatch(ctx context.Context, models []*M, options ...BatchOption) error {
	if len(models) == 0 {
		return nil
	}
	err, ok := InsertBatchOf(this.Core, ctx, models, options...)
	if !ok {
		return ErrNoBatchInsertSupport
	}
	return err
}
