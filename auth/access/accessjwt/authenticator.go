package accessjwt

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/auth/authjwt"
	"github.com/google/uuid"
)

type authenticator struct {
	core   *core
	parser *authjwt.Parser[Claims]
}

var _ auth.Authenticator = (*authenticator)(nil)

func (this *authenticator) Authenticate(ctx context.Context, credential auth.Credential) (auth.Principal, error) {
	if !credential.Is(auth.SchemeBearer) {
		return nil, auth.Unauthenticated("unsupported authentication scheme")
	}

	claims, err := this.parser.Parse(ctx, credential.Token)
	if err != nil {
		if !errors.Is(err, auth.ErrUnauthenticated) {
			return nil, err
		}
		return nil, auth.Unauthenticated("the presented token is not a valid access token")
	}

	if claims.SubjectType != string(this.core.deps.Subject.Type) {
		return nil, auth.Unauthenticated("the token was issued for another subject type")
	}

	subjectID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, auth.Unauthenticated("the token names no subject")
	}
	session, err := uuid.Parse(claims.SessionID)
	if err != nil {
		return nil, auth.Unauthenticated("the token names no session")
	}

	if list := this.core.spec.Revocation; list != nil {
		revoked, err := list.Revoked(ctx, session)
		if err != nil {
			return nil, err
		}
		if revoked {
			return nil, auth.Unauthenticated("the session has been closed")
		}
	}

	reference := access.SubjectRef{Type: access.SubjectType(claims.SubjectType), ID: subjectID}
	directory, served := this.core.deps.Grants.Directory(reference.Type)
	if !served {
		return nil, auth.Unauthenticated("no directory for the token's subject type")
	}
	active, err := directory.Active(ctx, reference.ID)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, auth.Unauthenticated("the subject is not active")
	}

	principal, err := this.core.deps.Grants.For(ctx, reference)
	if err != nil {
		return nil, err
	}
	principal.SessionID = session
	return principal, nil
}
