package access

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/crud/decorators/specs"
)

type ChangePasswordUseCase struct {
	*Deps
}

func NewChangePassword(dependencies *Deps) *ChangePasswordUseCase {
	return &ChangePasswordUseCase{Deps: dependencies}
}

func (this *ChangePasswordUseCase) Execute(ctx context.Context, cmd ChangePasswordCommand) (int64, error) {
	if cmd.Subject.Zero() {
		return 0, badCredentials("ChangePassword")
	}

	var closed revoked
	err := this.Store.OwnedTx(ctx, func(txCtx context.Context) error {
		credentials, err := this.Store.LockPasswordCredentials(txCtx, cmd.Subject)
		if err != nil {
			return err
		}
		if len(credentials) == 0 {
			return badCredentials("ChangePassword")
		}

		credential := credentials[0]
		ok, err := this.Hasher.Verify(cmd.Current, credential.SecretHash)
		if err != nil && !errors.Is(err, ErrSecretFormat) {
			return err
		}
		if err != nil || !ok {
			return badCredentials("ChangePassword")
		}
		if err := this.checkPassword(cmd.New); err != nil {
			return err
		}

		hash, err := this.Hasher.Hash(cmd.New)
		if err != nil {
			return err
		}
		if _, err := this.Store.Credentials.Update(txCtx, credential.ID, CredentialUpdate{
			SecretHash: &hash,
		}); err != nil {
			return err
		}
		if !cmd.RevokeOthers {
			return nil
		}

		closedSessions, err := this.revoke(txCtx, ReasonPasswordChanged,
			OfSubject(cmd.Subject),
			specs.As(Session_.RevokedAt.IsNull()),
			specs.As(Session_.ID.Ne(cmd.Keep)),
		)
		closed = closedSessions
		return err
	})
	if err != nil {
		return 0, err
	}

	this.announce(ctx, closed)
	return closed.count, nil
}
