package access

import (
	"context"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/google/uuid"
)

type LogoutAllUseCase struct {
	*Deps
}

func NewLogoutAll(dependencies *Deps) *LogoutAllUseCase {
	return &LogoutAllUseCase{Deps: dependencies}
}

func (this *LogoutAllUseCase) Execute(ctx context.Context, cmd LogoutAllCommand) (int64, error) {
	if cmd.Subject.Zero() {
		return 0, nil
	}

	options := []crud.Option{
		OfSubject(cmd.Subject),
		specs.As(Session_.RevokedAt.IsNull()),
	}
	if cmd.Except != uuid.Nil {
		options = append(options, specs.As(Session_.ID.Ne(cmd.Except)))
	}
	var closed revoked
	err := this.Store.OwnedTx(ctx, func(txCtx context.Context) error {
		if _, err := this.Store.LockPasswordCredentials(txCtx, cmd.Subject); err != nil {
			return err
		}
		var err error
		closed, err = this.revoke(txCtx, ReasonSignedOutEverywhere, options...)
		return err
	})
	if err != nil {
		return 0, err
	}
	this.announce(ctx, closed)
	return closed.count, nil
}

func (this *LogoutAllUseCase) RevokeOne(ctx context.Context, ref SubjectRef, id uuid.UUID) (int64, error) {
	var closed revoked
	err := this.Store.OwnedTx(ctx, func(txCtx context.Context) error {
		var err error
		closed, err = this.revoke(txCtx, ReasonSignedOut,
			OfSubject(ref),
			specs.As(Session_.ID.Eq(id)),
			specs.As(Session_.RevokedAt.IsNull()),
		)
		return err
	})
	if err != nil {
		return 0, err
	}
	this.announce(ctx, closed)
	return closed.count, nil
}

func (this *Deps) revoke(ctx context.Context, reason string, options ...crud.Option) (revoked, error) {
	found, err := this.Store.Sessions.GetAll(ctx, append(
		append(make([]crud.Option, 0, len(options)+4), options...),
		crud.Select(Session_.ID.Name(), Session_.SubjectType.Name()),

		crud.OrderBy(Session_.ID.Asc()),
		crud.ForUpdate(),
		crud.PrimaryOnly(),
	)...)
	if err != nil {
		return revoked{}, err
	}
	if len(found) == 0 {
		return revoked{}, nil
	}

	ids := make([]uuid.UUID, 0, len(found))
	for _, session := range found {
		ids = append(ids, session.ID)
	}

	// Chunked against the dialect's bind budget. One IN list naming every live
	// session meant a subject with enough of them could not be signed out at all:
	// the statement was refused, and the paths that need it most — a password
	// change, a compromise — are exactly the ones a long-lived account reaches.
	// Two binds are already spent on the update's own SET values.
	now := this.Now()
	total := int64(0)
	for _, batch := range chunked(ids, max(1, crud.BindLimit(this.Store.source.Dialect())-revokeFixedBinds)) {
		count, err := this.Store.Sessions.UpdateAll(ctx, SessionUpdate{
			RevokedAt:     crud.Set(now),
			RevokedReason: &reason,
		},
			crud.Where(crud.InAny("ID", batch)),
			specs.As(Session_.RevokedAt.IsNull()),
		)
		if err != nil {
			return revoked{}, err
		}
		total += count
	}
	return revoked{sessions: found, count: total}, nil
}

// RevokedAt and RevokedReason, which every chunk carries.
const revokeFixedBinds = 2

func chunked[T any](values []T, size int) [][]T {
	if size <= 0 || len(values) <= size {
		return [][]T{values}
	}
	out := make([][]T, 0, (len(values)+size-1)/size)
	for start := 0; start < len(values); start += size {
		out = append(out, values[start:min(start+size, len(values))])
	}
	return out
}
