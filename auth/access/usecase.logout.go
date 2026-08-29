package access

import (
	"context"

	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/google/uuid"
)

// LogoutUseCase closes the session the request arrived on.
type LogoutUseCase struct {
	*Deps
}

func NewLogout(dependencies *Deps) *LogoutUseCase { return &LogoutUseCase{Deps: dependencies} }

// Execute revokes one session.
//
// It is an UPDATE and not a DELETE. "Signed out at 14:02 from this address" is
// a question somebody asks after an incident, and a deleted row cannot answer
// it. The token is already unusable the moment revoked_at is set — the
// authenticator checks it before anything else.
//
// Revoking an already-revoked session succeeds and touches nothing: the caller
// asked for an end state, and a second click is not an error.
func (this *LogoutUseCase) Execute(ctx context.Context, cmd LogoutCommand) (int64, error) {
	if cmd.SessionID == uuid.Nil {
		return 0, nil
	}
	return this.revoke(ctx, ReasonSignedOut,
		specs.As(Session_.ID.Eq(cmd.SessionID)),
		specs.As(Session_.RevokedAt.IsNull()),
	)
}
