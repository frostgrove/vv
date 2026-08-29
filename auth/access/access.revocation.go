package access

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// Closing a session is two facts, and only one of them is a row.
//
// The row is what a strategy that reads it on every request needs, and for
// those this file does nothing at all. The second fact is for a strategy that
// does not read the row — a signed token verifies against a key, not against a
// table, so a sign-out changes nothing it looks at and the credential keeps
// working until it expires. That strategy declared a [RevocationSink], and this
// is what tells it.
//
// Every path that closes a session runs through [Deps.revoke], so there is one
// place to be told from rather than five to remember. See [[D-072]].

// revocationSinks routes a closed session to the strategy that issued it.
//
// Keyed by subject type, because a session belongs to exactly one. Announcing
// to every registered sink instead would put a key in a deployment's deny-list
// that nothing ever reads, and the day a sink starts costing something per
// entry that becomes somebody's afternoon.
//
// A pointer shared by every Deps the runtime builds, so a subject mounted after
// a use case was assembled is still reachable from it. The alternative — a copy
// taken at construction — is a sink that silently answers for nothing, which is
// exactly the failure this whole file exists to prevent.
type revocationSinks struct {
	byType map[SubjectType]RevocationSink
}

func newRevocationSinks() *revocationSinks {
	return &revocationSinks{byType: map[SubjectType]RevocationSink{}}
}

func (this *revocationSinks) register(subject SubjectType, sink RevocationSink) {
	if this == nil || sink == nil {
		return
	}
	this.byType[subject] = sink
}

func (this *revocationSinks) empty() bool { return this == nil || len(this.byType) == 0 }

// revoked is what one closing call actually did.
//
// The rows are carried as well as the count because the two answer different
// questions: the count is what the caller is told, and the ids are what a sink
// has to be told. An UPDATE ... WHERE gives only the first.
type revoked struct {
	// sessions holds the id and the subject type of every row the call matched,
	// read before the write.
	sessions []Session
	// count is how many rows the write actually changed, which is what a caller
	// who clicked "sign out everywhere" reads.
	count int64
}

func (this revoked) nothing() bool { return len(this.sessions) == 0 }

// announce tells each strategy which of its sessions were just closed.
//
// It is never called inside the transaction that wrote the rows. A rollback
// after a sink was told would leave a deny-list refusing a session that is
// still live, and nothing ever takes an entry back out — see the two password
// use cases, which collect inside the transaction and announce after it.
//
// A sink that fails does not fail the call. The rows are committed and the
// caller is signed out; answering "could not sign you out" to somebody who is
// signed out is worse than the window this leaves, and that window is bounded
// by the credential's own lifetime rather than open-ended. It is logged at
// error level with the ids, so somebody watching the logs can see the window
// rather than only somebody reading this comment.
func (this *Deps) announce(ctx context.Context, closed revoked) {
	if closed.nothing() || this.revocations.empty() {
		return
	}

	byType := make(map[SubjectType][]uuid.UUID)
	for _, session := range closed.sessions {
		subject := SubjectType(session.SubjectType)
		if _, served := this.revocations.byType[subject]; !served {
			continue
		}
		byType[subject] = append(byType[subject], session.ID)
	}

	for subject, ids := range byType {
		if err := this.revocations.byType[subject].SessionsRevoked(ctx, ids); err != nil {
			this.Log.ErrorContext(ctx,
				"sessions were closed but the strategy could not be told; their credentials stay usable until they expire",
				slog.String("subject_type", string(subject)),
				slog.Any("session_ids", ids),
				slog.String("error", err.Error()))
		}
	}
}
