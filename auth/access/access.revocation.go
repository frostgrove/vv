package access

import (
	"context"
	"log/slog"

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
