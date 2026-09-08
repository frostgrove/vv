package access

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/crud/decorators/specs"
)

// Distinguishes a wrong current password from every other failure inside the
// transaction, so only the first is counted as an attempt. It never leaves this
// file: the caller is answered with badCredentials, which says nothing.
var errWrongCurrentPassword = errors.New("access: the current password does not match")

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

	// The same admission and the same record as a sign-in, keyed on the subject
	// rather than an identifier the caller supplies: this check is a password
	// oracle behind a valid session, and it used to be unlimited and unobserved.
	attempt := Attempt{Subject: cmd.Subject.Type, Identifier: cmd.Subject.ID.String(), IP: cmd.Agent.IP}
	if err := this.admit(ctx, attempt); err != nil {
		return 0, err
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

		if len(credentials) > 1 {
			return ambiguousPassword(cmd.Subject, len(credentials))
		}

		credential := credentials[0]
		ok, err := this.Hasher.Verify(cmd.Current, credential.SecretHash)
		if err != nil && !errors.Is(err, ErrSecretFormat) {
			return err
		}
		if err != nil || !ok {
			return errWrongCurrentPassword
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
		// The deployment decides, and the body may only widen it. A caller who
		// just changed a password because it may be compromised must not be able
		// to leave the sessions that compromise reached.
		if this.Config.Password.KeepOtherSessions && !cmd.RevokeOthers {
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
	if errors.Is(err, errWrongCurrentPassword) {
		this.recordAttempt(ctx, attempt, AttemptFailed)
		return 0, badCredentials("ChangePassword")
	}
	if err != nil {
		return 0, err
	}
	this.recordAttempt(ctx, attempt, AttemptSucceeded)

	this.announce(ctx, closed)
	return closed.count, nil
}
