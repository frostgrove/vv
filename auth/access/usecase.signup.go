package access

import (
	"context"
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
// The account and its credential share one transaction, so an address the
// application's own unique index refuses rolls the credential back with it and
// a half-registered account never exists. The session is opened after that
// transaction commits: opening it inside would let a failure with nothing to do
// with signing in roll it back, and an account that exists while the response
// said 500 is the worse of the two outcomes for the person at the other end.
//
// The identifier is folded once, here, by [Subject.Normalize] — the same
// function [LoginUseCase] is given by the same binding, which is what makes the
// two sides of the column meet.
func (this *SignUpUseCase[P]) Execute(ctx context.Context, payload P, agent Agent) (AuthResponse, error) {
	var subject SubjectRef

	err := this.Store.Tx(ctx, func(txCtx context.Context) error {
		id, identifier, err := this.registrar.Create(txCtx, payload)
		if err != nil {
			return err
		}
		subject = this.subject.Ref(id)

		return this.enroll.Execute(txCtx, EnrollCommand{
			Subject:    subject,
			Identifier: this.subject.Identifier(identifier),
			Password:   this.registrar.Password(payload),
			Role:       this.registrar.Role(),
		})
	})
	if err != nil {
		return AuthResponse{}, err
	}

	return this.issuer.Issue(ctx, subject, agent)
}
