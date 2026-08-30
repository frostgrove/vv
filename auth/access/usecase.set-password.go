package access

import (
	"context"
	"fmt"

	"github.com/frostgrove/vv/crud/decorators/specs"
)

// SetPasswordUseCase gives a subject a password it did not choose.
//
// It is what makes an account created through POST /users usable: that endpoint
// writes a profile and nothing else — the user module has never held a secret —
// so without this there is a row nobody can sign in as. It is also the reset an
// administrator performs when somebody is locked out.
//
// It is not [ChangePasswordUseCase] with the check taken out. The two differ in
// who is asking and in what happens afterwards: a self-service change keeps the
// caller signed in, and an administrative set closes every session the subject
// had, because the reason to perform one is usually that somebody else may be
// holding one.
type SetPasswordUseCase struct {
	*Deps
}

func NewSetPassword(dependencies *Deps) *SetPasswordUseCase {
	return &SetPasswordUseCase{Deps: dependencies}
}

// Execute writes the credential and closes the subject's sessions.
//
// The identifier is the directory's, never the caller's: an administrator who
// could choose it could point a credential at an address they control and sign
// in as somebody else, and the account would look untouched.
func (this *SetPasswordUseCase) Execute(ctx context.Context, cmd SetPasswordCommand) (int64, error) {
	if cmd.Subject.Zero() {
		return 0, fmt.Errorf("access: setting a password for an empty subject")
	}
	if err := this.checkPassword(cmd.Password); err != nil {
		return 0, err
	}

	directory, ok := this.Grants.Directory(cmd.Subject.Type)
	if !ok {
		return 0, fmt.Errorf("access: no directory registered for subject type %q", cmd.Subject.Type)
	}
	profile, err := directory.Describe(ctx, cmd.Subject.ID)
	if err != nil {
		return 0, err
	}
	identifier := profile.Identifier
	if identifier == "" {
		return 0, fmt.Errorf("access: %s has no identifier to sign in with", cmd.Subject)
	}

	hash, err := this.Hasher.Hash(cmd.Password)
	if err != nil {
		return 0, err
	}

	var closed revoked
	err = this.Store.OwnedTx(ctx, func(txCtx context.Context) error {
		credentials, err := this.Store.LockPasswordCredentials(txCtx, cmd.Subject)
		if err != nil {
			return err
		}
		switch {
		case len(credentials) == 0:
			err = this.Store.Credentials.SaveOnly(txCtx, &Credential{
				SubjectType: string(cmd.Subject.Type),
				SubjectID:   cmd.Subject.ID,
				Provider:    ProviderPassword,
				Identifier:  identifier,
				SecretHash:  hash,
			})
		default:
			existing := credentials[0]
			_, err = this.Store.Credentials.Update(txCtx, existing.ID, CredentialUpdate{
				Identifier: &identifier,
				SecretHash: &hash,
			})
		}
		if err != nil {
			return err
		}

		closedSessions, err := this.revoke(txCtx, ReasonPasswordChanged,
			OfSubject(cmd.Subject),
			specs.As(Session_.RevokedAt.IsNull()),
		)
		closed = closedSessions
		return err
	})
	if err != nil {
		return 0, err
	}
	// After the transaction and never inside it — see the same call in
	// ChangePasswordUseCase.
	this.announce(ctx, closed)
	return closed.count, nil
}
