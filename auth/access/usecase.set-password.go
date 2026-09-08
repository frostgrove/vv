package access

import (
	"context"
	"fmt"

	"github.com/frostgrove/vv/crud/decorators/specs"
)

type SetPasswordUseCase struct {
	*Deps
	unguarded bool
}

func NewSetPassword(dependencies *Deps) *SetPasswordUseCase {
	return &SetPasswordUseCase{Deps: dependencies}
}

// Setting another account's password is the permission the module declared as
// PermCredentialWrite and then enforced nowhere. Unguarded is the seed and CLI
// path, where there is no principal to check and a check could only ever fail.
func (this *SetPasswordUseCase) Unguarded() *SetPasswordUseCase {
	bound := *this
	bound.unguarded = true
	return &bound
}

func (this *SetPasswordUseCase) Execute(ctx context.Context, cmd SetPasswordCommand) (int64, error) {
	if cmd.Subject.Zero() {
		return 0, fmt.Errorf("access: setting a password for an empty subject")
	}
	if !this.unguarded {
		if _, err := Require(ctx, PermCredentialWrite); err != nil {
			return 0, err
		}
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
	// Folded the same way sign-in folds what it looks up. Writing the directory's
	// raw identifier here made the credential unfindable by every path that
	// normalizes — a set password that silently could not be used to sign in.
	identifier := this.Grants.normalize(cmd.Subject.Type, profile.Identifier)
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
		case len(credentials) > 1:
			return ambiguousPassword(cmd.Subject, len(credentials))
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

	this.announce(ctx, closed)
	return closed.count, nil
}
