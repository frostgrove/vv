package access

import (
	"context"
	"fmt"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/errs"
)

// EnrollUseCase gives a subject that already exists a password to sign in with,
// and optionally a role.
//
// The line that is not here is the point: nothing in this package creates an
// account. An application registering somebody writes its own row first — with
// its own columns, its own validation and its own idea of what a sign-up form
// asks for — and hands the finished [SubjectRef] to this. That is why
// there is no ProvisionCommand, no payload generic and no map of extra fields:
// the half that varies between applications never crosses the boundary.
//
// It joins a transaction rather than owning one. crud.InTx makes Store.Tx a
// no-op when the context already carries an executor, so the ordinary shape is
// the caller's:
//
//	err := users.Tx(ctx, func(txCtx context.Context) error {
//	    account, err := users.Save(txCtx, &User{ … })
//	    if err != nil {
//	        return err
//	    }
//	    subject = SubjectRef{Type: SubjectUser, ID: account.ID}
//	    return enroll.Execute(txCtx, EnrollCommand{
//	        Subject:    subject,
//	        Identifier: email,
//	        Password:   password,
//	        Role:       "client",
//	    })
//	})
//
// and a credential that cannot be written rolls the account back with it.
type EnrollUseCase struct {
	*Deps
}

func NewEnroll(dependencies *Deps) *EnrollUseCase { return &EnrollUseCase{Deps: dependencies} }

// Execute writes the credential and grants the role.
//
// Everything that can refuse runs before anything is written, so a rejected
// password is not a half-enrolled subject.
//
// The identifier is stored exactly as it arrives. Whatever rule the application
// applies to it — lowercasing an address, refusing one that does not parse —
// has to be the same rule it applies before [LoginUseCase], because these two
// are the only places the column is written and read.
func (this *EnrollUseCase) Execute(ctx context.Context, cmd EnrollCommand) error {
	if cmd.Subject.Zero() {
		return fmt.Errorf("access: enrolling an empty subject")
	}
	if cmd.Identifier == "" {
		return errs.Validation().
			Field("Identifier").Code(errs.CodeRequired).
			Entity("Credential").Op("Enroll").Fault()
	}
	if err := this.checkPassword(cmd.Password); err != nil {
		return err
	}

	hash, err := this.Hasher.Hash(cmd.Password)
	if err != nil {
		return err
	}

	return this.Store.Tx(ctx, func(txCtx context.Context) error {
		if err := this.Store.Credentials.SaveOnly(txCtx, &Credential{
			SubjectType: string(cmd.Subject.Type),
			SubjectID:   cmd.Subject.ID,
			Provider:    ProviderPassword,
			Identifier:  cmd.Identifier,
			SecretHash:  hash,
		}); err != nil {
			return err
		}
		return this.grantRole(txCtx, cmd.Subject, cmd.Role)
	})
}

// grantRole gives the subject the role the caller named.
//
// An unnamed role grants nothing, which is the safe reading of "not specified":
// somebody enrolled who can do nothing is a support ticket, and somebody who
// inherits a role a typo named is an incident. A role that does not exist is an
// error rather than a silent skip, for the same reason.
func (this *EnrollUseCase) grantRole(ctx context.Context, subject SubjectRef, slug auth.Role) error {
	if slug == "" {
		return nil
	}
	role, err := this.Store.RoleBySlug(ctx, slug)
	if err != nil {
		return fmt.Errorf("access: the role %q does not exist: %w", slug, err)
	}
	return this.Store.SubjectRoles.SaveOnly(ctx, &SubjectRole{
		SubjectType: string(subject.Type),
		SubjectID:   subject.ID,
		RoleID:      role.ID,
	})
}

// checkPassword enforces the one rule this context has: length, and nothing
// else. See [PasswordConfig] for why.
//
// On Deps rather than on this use case, because a password change has to apply
// the same rule and a second copy of it is a second rule the first time either
// is edited.
func (this *Deps) checkPassword(password string) error {
	minimum := this.Config.MinPasswordLength()
	if len([]rune(password)) < minimum {
		return errs.Validation().
			Field("Password").Code(CodeWeakPassword).
			Params(errs.P{"min": minimum}).
			Message(fmt.Sprintf("a password needs at least %d characters", minimum)).
			Entity("Credential").Fault()
	}
	return nil
}
