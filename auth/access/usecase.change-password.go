package access

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/crud/decorators/specs"
)

// ChangePasswordUseCase replaces a caller's own secret.
//
// It is here and not in a "profile" endpoint because a password is a
// credential, and a credential is this context's. The user module never sees
// one — which is the same reason its table has no password_hash column.
type ChangePasswordUseCase struct {
	*Deps
}

func NewChangePassword(dependencies *Deps) *ChangePasswordUseCase {
	return &ChangePasswordUseCase{Deps: dependencies}
}

// Execute verifies the current password and writes the new one.
//
// The current password is required even though the caller is already
// authenticated. An unattended session must not be enough to take an account
// over: without this check, walking past somebody's laptop is a full account
// takeover, and the session that did it is the one thing they would then use to
// sign out everywhere.
func (this *ChangePasswordUseCase) Execute(ctx context.Context, cmd ChangePasswordCommand) (int64, error) {
	if cmd.Subject.Zero() {
		return 0, badCredentials("ChangePassword")
	}

	credential, err := this.Store.Credentials.First(ctx,
		OfSubject(cmd.Subject),
		specs.As(Credential_.Provider.Eq(ProviderPassword)),
	)
	if IsNotFound(err) {
		return 0, badCredentials("ChangePassword")
	}
	if err != nil {
		return 0, err
	}

	ok, err := this.Hasher.Verify(cmd.Current, credential.SecretHash)
	if err != nil && !errors.Is(err, ErrSecretFormat) {
		return 0, err
	}
	if err != nil || !ok {
		return 0, badCredentials("ChangePassword")
	}
	if err := this.checkPassword(cmd.New); err != nil {
		return 0, err
	}

	hash, err := this.Hasher.Hash(cmd.New)
	if err != nil {
		return 0, err
	}

	var closed revoked
	err = this.Store.Tx(ctx, func(txCtx context.Context) error {
		if _, err := this.Store.Credentials.Update(txCtx, credential.ID, CredentialUpdate{
			SecretHash: &hash,
		}); err != nil {
			return err
		}
		if !cmd.RevokeOthers {
			return nil
		}
		// Keep is the session the request arrived on. A zero uuid matches no
		// row, so an unset Keep closes everything including this one — which is
		// what an administrator resetting somebody else's password wants.
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
	// After the transaction and never inside it: a rollback would leave a
	// deny-list refusing sessions that are still live, and nothing takes an
	// entry back out.
	this.announce(ctx, closed)
	return closed.count, nil
}
