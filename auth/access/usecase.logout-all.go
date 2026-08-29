package access

import (
	"context"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/google/uuid"
)

// LogoutAllUseCase closes every session a subject holds.
type LogoutAllUseCase struct {
	*Deps
}

func NewLogoutAll(dependencies *Deps) *LogoutAllUseCase {
	return &LogoutAllUseCase{Deps: dependencies}
}

// Execute revokes the subject's sessions and reports how many it closed.
//
// The count is the answer, not decoration: somebody clicking "sign out
// everywhere" because they lost a laptop wants to see that four sessions went
// away, and a bare 204 leaves them to guess.
//
// Except keeps one session alive — the one the request arrived on. "Sign out
// everywhere else" is what a person means by the button, and signing the
// current device out too lands the confirmation on a login screen.
func (this *LogoutAllUseCase) Execute(ctx context.Context, cmd LogoutAllCommand) (int64, error) {
	if cmd.Subject.Zero() {
		return 0, nil
	}

	options := []crud.Option{
		OfSubject(cmd.Subject),
		specs.As(Session_.RevokedAt.IsNull()),
	}
	if cmd.Except != uuid.Nil {
		options = append(options, specs.As(Session_.ID.Ne(cmd.Except)))
	}
	return this.revoke(ctx, ReasonSignedOutEverywhere, options...)
}

// RevokeOne closes a single session, narrowed by the subject that owns it.
//
// Exported because it is the whole of "close that one" — the endpoint a client
// hits from a session list — and a caller that had to reach for RevokeAll with
// a hand-built predicate would be one forgotten clause away from closing every
// session the subject holds.
//
// The subject is part of the WHERE and not an ownership check before it. A
// check-then-write is two statements a concurrent transfer can land between,
// and — more to the point here — an id belonging to somebody else then matches
// no row instead of producing a 403 that confirms the id exists.
func (this *LogoutAllUseCase) RevokeOne(ctx context.Context, ref SubjectRef, id uuid.UUID) (int64, error) {
	return this.revoke(ctx, ReasonSignedOut,
		OfSubject(ref),
		specs.As(Session_.ID.Eq(id)),
		specs.As(Session_.RevokedAt.IsNull()),
	)
}

// revoke is the one statement both logout use cases run. It is here rather than
// in each of them because "closed" is a shape — a timestamp and a reason — and
// two spellings of it drift the first time a column is added.
func (this *Deps) revoke(ctx context.Context, reason string, options ...crud.Option) (int64, error) {
	now := this.Now()
	return this.Store.Sessions.UpdateAll(ctx, SessionUpdate{
		RevokedAt:     crud.Set(now),
		RevokedReason: &reason,
	}, options...)
}
