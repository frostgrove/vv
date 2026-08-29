package access

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// LoginUseCase exchanges an identifier and a password for a session.
type LoginUseCase struct {
	*Deps

	issuer SessionIssuer
}

func NewLogin(dependencies *Deps) *LoginUseCase { return &LoginUseCase{Deps: dependencies} }

// Issuing binds this use case to what mints the session it answers with.
//
// A verified password and the token that follows it are two decisions: the
// first is this file's, the second is the subject's [Strategy]. Separating them
// is what lets one kind of caller leave with a JWT and another with an opaque
// token, over the same password check.
func (this *LoginUseCase) Issuing(issuer SessionIssuer) *LoginUseCase {
	bound := *this
	bound.issuer = issuer
	return &bound
}

// Execute signs a caller in.
//
// Three things about the order here, all of them about what a failure reveals:
//
//   - The password is verified even when no credential was found, against a
//     hash of a value nothing can present. Without that, a request for an
//     unknown address returns in a millisecond and one for a known address
//     takes the sixty argon2 needs, and the difference answers "does this
//     person have an account here" to anybody with a stopwatch.
//   - The account's active flag is checked *after* the password. Checking it
//     first would tell somebody with the wrong password that the address is
//     real and disabled.
//   - Every refusal is the same refusal. See [badCredentials].
//
// The subject type is part of the lookup rather than a check on what came back.
// An identifier is unique within a type, so a query without it has more than
// one answer, and the endpoint that asked would sign somebody in to a domain
// they never presented a credential for.
func (this *LoginUseCase) Execute(ctx context.Context, cmd LoginCommand) (AuthResponse, error) {
	if cmd.Subject == "" {
		return AuthResponse{}, fmt.Errorf("access: signing in with no subject type")
	}

	credential, err := this.Store.CredentialFor(ctx, cmd.Subject, ProviderPassword, cmd.Identifier)
	found := err == nil
	if err != nil && !IsNotFound(err) {
		return AuthResponse{}, err
	}

	hash := credential.SecretHash
	if !found {
		hash = DummyHash()
	}
	ok, err := this.Hasher.Verify(cmd.Password, hash)
	if err != nil {
		// An unreadable stored hash is a deployment fault, not a wrong
		// password. Answering "bad credentials" here would lock an account out
		// silently and leave nothing in the logs to find it by.
		if errors.Is(err, ErrSecretFormat) {
			this.Log.ErrorContext(ctx, "stored password hash is unreadable",
				slog.String("credential_id", credential.ID.String()))
			return AuthResponse{}, err
		}
		return AuthResponse{}, err
	}
	if !ok || !found {
		return AuthResponse{}, badCredentials("Login")
	}

	ref := SubjectRef{Type: SubjectType(credential.SubjectType), ID: credential.SubjectID}
	directory, exists := this.Grants.Directory(ref.Type)
	if !exists {
		return AuthResponse{}, badCredentials("Login")
	}
	active, err := directory.Active(ctx, ref.ID)
	if err != nil {
		return AuthResponse{}, err
	}
	if !active {
		return AuthResponse{}, badCredentials("Login")
	}

	response, err := this.issuer.Issue(ctx, ref, cmd.Agent)
	if err != nil {
		return AuthResponse{}, err
	}

	// Best effort, and after the session exists: a directory that cannot record
	// a sign-in has not stopped one from happening.
	if err := directory.Touch(ctx, ref.ID); err != nil {
		this.Log.WarnContext(ctx, "could not record a sign-in",
			slog.String("subject", ref.String()), slog.Any("err", err))
	}
	return response, nil
}
