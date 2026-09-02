package access

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/frostgrove/vv/errs"
)

type LoginUseCase struct {
	*Deps

	issuer SessionIssuer
}

func NewLogin(dependencies *Deps) *LoginUseCase { return &LoginUseCase{Deps: dependencies} }

func (this *LoginUseCase) Issuing(issuer SessionIssuer) *LoginUseCase {
	bound := *this
	bound.issuer = issuer
	return &bound
}

func (this *LoginUseCase) Execute(ctx context.Context, cmd LoginCommand) (AuthResponse, error) {
	if cmd.Subject == "" {
		return AuthResponse{}, fmt.Errorf("access: signing in with no subject type")
	}

	attempt := Attempt{Subject: cmd.Subject, Identifier: cmd.Identifier, IP: cmd.Agent.IP}
	if err := this.admit(ctx, attempt); err != nil {
		return AuthResponse{}, err
	}
	if !this.withinBounds(cmd) {
		this.recordAttempt(ctx, attempt, AttemptFailed)
		return AuthResponse{}, badCredentials("Login")
	}

	var (
		response  AuthResponse
		directory Directory
		ref       SubjectRef
	)
	err := this.Store.OwnedTx(ctx, func(txCtx context.Context) error {
		credential, err := this.Store.LockCredentialFor(
			txCtx, cmd.Subject, ProviderPassword, cmd.Identifier)
		found := err == nil
		if err != nil && !IsNotFound(err) {
			return err
		}

		hash := credential.SecretHash
		if !found {
			hash = DummyHash()
		}
		ok, err := this.Hasher.Verify(cmd.Password, hash)
		if err != nil {
			if errors.Is(err, ErrSecretFormat) {
				this.Log.ErrorContext(txCtx, "stored password hash is unreadable",
					slog.String("credential_id", credential.ID.String()))
			}
			return err
		}
		if !ok || !found {
			return badCredentials("Login")
		}

		ref = SubjectRef{Type: SubjectType(credential.SubjectType), ID: credential.SubjectID}
		var exists bool
		directory, exists = this.Grants.Directory(ref.Type)
		if !exists {
			return badCredentials("Login")
		}
		active, err := directory.Active(txCtx, ref.ID)
		if err != nil {
			return err
		}
		if !active {
			return badCredentials("Login")
		}

		if err := this.Store.FenceSessionIssue(txCtx, credential); err != nil {
			return err
		}
		response, err = this.issuer.Issue(txCtx, ref, cmd.Agent)
		return err
	})
	if err != nil {
		if isBadCredentials(err) {
			this.recordAttempt(ctx, attempt, AttemptFailed)
		}
		return AuthResponse{}, err
	}
	this.recordAttempt(ctx, attempt, AttemptSucceeded)

	if err := directory.Touch(ctx, ref.ID); err != nil {
		this.Log.WarnContext(ctx, "could not record a sign-in",
			slog.String("subject", ref.String()), slog.Any("err", err))
	}
	return response, nil
}

func (this *LoginUseCase) withinBounds(cmd LoginCommand) bool {
	return len(cmd.Identifier) <= this.Config.MaxIdentifierLength() &&
		len(cmd.Password) <= this.Config.MaxPasswordLength()
}

func isBadCredentials(err error) bool {
	fault, ok := errs.AsFault(err)
	return ok && fault.Code == CodeBadCredentials
}
