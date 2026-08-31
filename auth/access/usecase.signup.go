package access

import (
	"context"

	"github.com/frostgrove/vv/auth"
)

type SignUpUseCase[P any] struct {
	*Deps

	subject   Subject
	registrar Registrar[P]
	enroll    *EnrollUseCase
	issuer    SessionIssuer
}

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
