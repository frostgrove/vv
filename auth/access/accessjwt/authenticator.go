package accessjwt

import (
	"context"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/auth/authjwt"
	"github.com/google/uuid"
)

// authenticator turns a signed access token into a principal.
//
// It reads no session row. That is the one thing a signed token buys, and it is
// also why a revoked session keeps working until the token expires unless a
// [RevocationList] is configured — stated here rather than discovered.
//
// What it still reads, on every request: whether the subject is active, and
// what it is granted. Both come from rows on purpose, so deactivating an
// account and taking a role away both bite on the next call.
type authenticator struct {
	core   *core
	parser *authjwt.Parser[Claims]
}

var _ auth.Authenticator = (*authenticator)(nil)

// Authenticate implements auth.Authenticator.
//
// Every refusal is auth.Unauthenticated with a reason that stays in the wrapped
// error and never reaches a body. A failure that is *not* a refusal — the
// revocation list is unreachable — is returned as itself, so it renders as a
// 500 and shows up where somebody watches the 5xx rate rather than as a
// mysterious wave of 401s.
func (this *authenticator) Authenticate(ctx context.Context, credential auth.Credential) (auth.Principal, error) {
	if !credential.Is(auth.SchemeBearer) {
		return nil, auth.Unauthenticated("unsupported authentication scheme")
	}

	claims, err := this.parser.Parse(ctx, credential.Token)
	if err != nil {
		return nil, auth.Unauthenticated("the presented token is not a valid access token")
	}

	// The subject type is checked and not taken. A token minted for one kind of
	// caller presented to another's guard verifies its signature perfectly —
	// the session is genuine, nothing looks wrong, and the caller is simply
	// somebody other than the route assumed.
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
