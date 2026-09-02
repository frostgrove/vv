package access

import (
	"context"
	"fmt"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/errs"
)

type EnrollUseCase struct {
	*Deps
}

func NewEnroll(dependencies *Deps) *EnrollUseCase { return &EnrollUseCase{Deps: dependencies} }

func (this *EnrollUseCase) Execute(ctx context.Context, cmd EnrollCommand) error {
	return this.execute(ctx, cmd, nil)
}

func (this *EnrollUseCase) execute(ctx context.Context, cmd EnrollCommand, resolved *Role) error {
	if cmd.Subject.Zero() {
		return fmt.Errorf("access: enrolling an empty subject")
	}
	if err := this.checkIdentifier(cmd.Identifier); err != nil {
		return err
	}
	if err := this.checkPassword(cmd.Password); err != nil {
		return err
	}

	hash, err := this.Hasher.Hash(cmd.Password)
	if err != nil {
		return err
	}

	return this.Store.Tx(ctx, func(txCtx context.Context) error {
		enrolled, err := this.Store.LockPasswordCredentials(txCtx, cmd.Subject)
		if err != nil {
			return err
		}
		if len(enrolled) > 0 {
			return alreadyEnrolled()
		}
		if err := this.Store.Credentials.SaveOnly(txCtx, &Credential{
			SubjectType: string(cmd.Subject.Type),
			SubjectID:   cmd.Subject.ID,
			Provider:    ProviderPassword,
			Identifier:  cmd.Identifier,
			SecretHash:  hash,
		}); err != nil {
			return err
		}
		return this.grantRole(txCtx, cmd.Subject, cmd.Role, resolved)
	})
}

func alreadyEnrolled() error {
	return errs.Conflict().
		Field("Identifier").Code(errs.CodeUnique).
		Message("this account already signs in with a password; change or reset it instead").
		Entity("Credential").Op("Enroll").Fault()
}

func (this *EnrollUseCase) grantRole(ctx context.Context, subject SubjectRef, slug auth.Role, resolved *Role) error {
	if slug == "" {
		return nil
	}
	role := resolved
	if role == nil || role.Slug != string(slug) {
		found, err := this.Store.RoleBySlug(ctx, slug)
		if err != nil {
			return fmt.Errorf("access: the role %q does not exist: %w", slug, err)
		}
		role = &found
	}
	return this.Store.SubjectRoles.SaveOnly(ctx, &SubjectRole{
		SubjectType: string(subject.Type),
		SubjectID:   subject.ID,
		RoleID:      role.ID,
	})
}

func (this *Deps) checkPassword(password string) error {
	minimum := this.Config.MinPasswordLength()
	if len([]rune(password)) < minimum {
		return errs.Validation().
			Field("Password").Code(CodeWeakPassword).
			Params(errs.P{"min": minimum}).
			Message(fmt.Sprintf("a password needs at least %d characters", minimum)).
			Entity("Credential").Fault()
	}
	maximum := this.Config.MaxPasswordLength()
	if len(password) > maximum {
		return errs.Validation().
			Field("Password").Code(errs.CodeTooLong).
			Params(errs.P{"max": maximum}).
			Message(fmt.Sprintf("a password may be at most %d bytes long", maximum)).
			Entity("Credential").Fault()
	}
	return nil
}

func (this *Deps) checkIdentifier(identifier string) error {
	if identifier == "" {
		return errs.Validation().
			Field("Identifier").Code(errs.CodeRequired).
			Entity("Credential").Op("Enroll").Fault()
	}
	maximum := this.Config.MaxIdentifierLength()
	if len(identifier) > maximum {
		return errs.Validation().
			Field("Identifier").Code(errs.CodeTooLong).
			Params(errs.P{"max": maximum}).
			Message(fmt.Sprintf("an identifier may be at most %d bytes long", maximum)).
			Entity("Credential").Op("Enroll").Fault()
	}
	return nil
}
