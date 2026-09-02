package access

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

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

type revoked struct {
	sessions []Session

	count int64
}

func (this revoked) nothing() bool { return len(this.sessions) == 0 }

func (this *Deps) announce(ctx context.Context, closed revoked) {
	if err := this.tell(ctx, closed); err != nil {
		this.Log.ErrorContext(ctx,
			"sessions were closed but the strategy could not be told; their credentials stay usable "+
				"until the revocation is replayed or they expire",
			slog.String("error", err.Error()))
	}
}

// The sessions table is the journal a failed announcement is replayed from, and
// telling a sink about a session it already knows costs a duplicate deny-list
// entry and nothing else, so windows may overlap freely.
func (this *Deps) ReannounceRevocations(ctx context.Context, since time.Time) error {
	if this.revocations.empty() {
		return nil
	}
	sessions, err := this.Store.SessionsRevokedSince(ctx, since, this.Now())
	if err != nil {
		return err
	}
	return this.tell(ctx, revoked{sessions: sessions})
}

func (this *Deps) tell(ctx context.Context, closed revoked) error {
	if closed.nothing() || this.revocations.empty() {
		return nil
	}

	byType := make(map[SubjectType][]uuid.UUID)
	for _, session := range closed.sessions {
		subject := SubjectType(session.SubjectType)
		if _, served := this.revocations.byType[subject]; !served {
			continue
		}
		byType[subject] = append(byType[subject], session.ID)
	}

	var failures []error
	for subject, ids := range byType {
		if err := this.revocations.byType[subject].SessionsRevoked(ctx, ids); err != nil {
			failures = append(failures, fmt.Errorf("%s sessions %v: %w", subject, ids, err))
		}
	}
	return errors.Join(failures...)
}
