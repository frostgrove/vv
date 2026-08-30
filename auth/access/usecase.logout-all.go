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
	var closed revoked
	err := this.Store.OwnedTx(ctx, func(txCtx context.Context) error {
		// Password login holds this same lock until its session insert commits.
		// Taking it before reading sessions makes the two possible orders honest:
		// either this invalidation commits first and login re-checks afterwards,
		// or login commits first and its new session is in the set revoked below.
		if _, err := this.Store.LockPasswordCredentials(txCtx, cmd.Subject); err != nil {
			return err
		}
		var err error
		closed, err = this.revoke(txCtx, ReasonSignedOutEverywhere, options...)
		return err
	})
	if err != nil {
		return 0, err
	}
	this.announce(ctx, closed)
	return closed.count, nil
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
	var closed revoked
	err := this.Store.OwnedTx(ctx, func(txCtx context.Context) error {
		var err error
		closed, err = this.revoke(txCtx, ReasonSignedOut,
			OfSubject(ref),
			specs.As(Session_.ID.Eq(id)),
			specs.As(Session_.RevokedAt.IsNull()),
		)
		return err
	})
	if err != nil {
		return 0, err
	}
	this.announce(ctx, closed)
	return closed.count, nil
}

// revoke is the one operation every closing path runs. It is here rather than
// in each of them because "closed" is a shape — a timestamp and a reason — and
// two spellings of it drift the first time a column is added. It is also the
// one place a [RevocationSink] can be told from, which is why it reads before
// it writes.
//
// Two statements where one would do, on a path somebody takes once a day: an
// UPDATE ... WHERE answers how many rows changed and never which, and a sink
// that has to be told "these three sessions" cannot be built from a number. The
// alternative was five call sites each assembling their own list from their own
// predicate, and the one that got it wrong would leave a signed token working
// after a sign-out with nothing to see.
//
// Every caller owns [Store.OwnedTx]. The read locks matching rows in
// ascending session-id order, so it is both a MySQL current read and a stable
// multi-row lock order. Subject-wide invalidators acquire password credentials
// first; single-session logout acquires no credential afterwards, so there is
// no sessions -> credentials reverse edge.
//
// The write is narrowed to the ids the locking read found rather than repeating
// the caller's predicate, so what was announced and what was closed are the
// same set. RevokedAt IS NULL remains on the UPDATE as the idempotency guard for
// an already-closed row and as defence if a custom repository weakens locking.
func (this *Deps) revoke(ctx context.Context, reason string, options ...crud.Option) (revoked, error) {
	found, err := this.Store.Sessions.GetAll(ctx, append(
		append(make([]crud.Option, 0, len(options)+4), options...),
		crud.Select(Session_.ID.Name(), Session_.SubjectType.Name()),
		// In a MySQL REPEATABLE READ transaction the caller may already have
		// established a consistent-read snapshot before it waited on the
		// credential lock. Revocation must be a current locking read or it can
		// miss the login that committed immediately before the lock was acquired.
		crud.OrderBy(Session_.ID.Asc()),
		crud.ForUpdate(),
		crud.PrimaryOnly(),
	)...)
	if err != nil {
		return revoked{}, err
	}
	if len(found) == 0 {
		return revoked{}, nil
	}

	ids := make([]uuid.UUID, 0, len(found))
	for _, session := range found {
		ids = append(ids, session.ID)
	}

	now := this.Now()
	count, err := this.Store.Sessions.UpdateAll(ctx, SessionUpdate{
		RevokedAt:     crud.Set(now),
		RevokedReason: &reason,
	},
		crud.Where(crud.InAny("ID", ids)),
		specs.As(Session_.RevokedAt.IsNull()),
	)
	if err != nil {
		return revoked{}, err
	}
	return revoked{sessions: found, count: count}, nil
}
