package access

import (
	"context"

	"github.com/frostgrove/vv/auth"
)

// SignUpUseCase is the whole of a self-service registration: the application's
// row, this module's credential, the role, and the session.
//
// It is generic over the application's payload and holds an
// [Registrar] for it, so the only thing a transport binding does is
// decode a body and call this. Before it existed, every consumer wrote the same
// four steps by hand and got the transaction boundary or the normalisation
// wrong — which is a defect nothing reports until somebody cannot sign in.
type SignUpUseCase[P any] struct {
	*Deps

	subject   Subject
	registrar Registrar[P]
	enroll    *EnrollUseCase
	issuer    SessionIssuer
}

// NewSignUp builds it for one subject.
//
// One instance per [Subject], because the registrar, the payload and the
// identifier rule are all that subject's. Two kinds of caller that both sign up
// are two of these.
func NewSignUp[P any](
	dependencies *Deps,
	subject Subject,
	issuer SessionIssuer,
	registrar Registrar[P],
) *SignUpUseCase[P] {
	return &SignUpUseCase[P]{
		Deps:      dependencies,
		subject:   subject,
		registrar: registrar,
		enroll:    NewEnroll(dependencies),
		issuer:    issuer,
	}
}

// Execute registers and signs in.
//
// The account, credential, role and session share one transaction owned by this
// use case. An address the application's own unique index refuses rolls every
// access row back with it; an issuer or commit failure returns no token and
// leaves no half-registered account. Owning the boundary also serialises signup
// with reset/logout-all: either they commit first and signup follows, or signup
// commits its credential and session together and the invalidator sees both.
// An ambient executor is intentionally not this operation's commit boundary.
//
// The identifier is folded once, here, by [Subject.Normalize] — the same
// function [LoginUseCase] is given by the same binding, which is what makes the
// two sides of the column meet.
//
// The role comes from subject_default_roles and not from the registrar. That is
// [[D-070]]: which role a stranger gets is a decision an operator makes about a
// deployment, and holding it in code or in a configuration key put it where
// nothing could check it existed.
//
// It is read *inside* the transaction, so a seed command changing the default
// while a registration is in flight cannot produce an account granted a role
// that no longer exists by the time the credential commits.
func (this *SignUpUseCase[P]) Execute(ctx context.Context, payload P, agent Agent) (AuthResponse, error) {
	var response AuthResponse

	err := this.Store.OwnedTx(ctx, func(txCtx context.Context) error {
		role, err := this.DefaultRole(txCtx, this.subject.Type)
		if err != nil {
			return err
		}

		id, identifier, err := this.registrar.Create(txCtx, payload)
		if err != nil {
			return err
		}
		subject := this.subject.Ref(id)

		// The role goes through resolved as well as named. It is the row this
		// use case just read, so the enrolment grants it without looking the
		// slug up a second time; a subject type with no default passes nil and
		// an empty name, which grants nothing.
		var slug auth.Role
		if role != nil {
			slug = auth.Role(role.Slug)
		}
		if err := this.enroll.execute(txCtx, EnrollCommand{
			Subject:    subject,
			Identifier: this.subject.Identifier(identifier),
			Password:   this.registrar.Password(payload),
			Role:       slug,
		}, role); err != nil {
			return err
		}
		response, err = this.issuer.Issue(txCtx, subject, agent)
		return err
	})
	if err != nil {
		return AuthResponse{}, err
	}
	return response, nil
}
