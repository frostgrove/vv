package access

import (
	"context"

	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/google/uuid"
)

type LogoutUseCase struct {
	*Deps
}

func NewLogout(dependencies *Deps) *LogoutUseCase { return &LogoutUseCase{Deps: dependencies} }

func (this *LogoutUseCase) Execute(ctx context.Context, cmd LogoutCommand) (int64, error) {
	if cmd.SessionID == uuid.Nil {
		return 0, nil
	}
	var closed revoked
	err := this.Store.OwnedTx(ctx, func(txCtx context.Context) error {
		var err error
		closed, err = this.revoke(txCtx, ReasonSignedOut,
			specs.As(Session_.ID.Eq(cmd.SessionID)),
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
