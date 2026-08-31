package access

import (
	"context"
	"log/slog"
	"time"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud/decorators/specs"
)

type SessionAuthenticator struct {
	store  *Store
	grants *GrantsService
	config SessionConfig
	logger *slog.Logger
	now    func() time.Time

	only SubjectType
}

func (this *SessionAuthenticator) For(subjectType SubjectType) *SessionAuthenticator {
	narrowed := *this
	narrowed.only = subjectType
	return &narrowed
}

func NewAuthenticator(store *Store, grants *GrantsService, configuration Config, logger *slog.Logger) *SessionAuthenticator {
	return &SessionAuthenticator{
		store:  store,
		grants: grants,
		config: configuration.Sessions(),
		logger: logger,
		now:    time.Now,
	}
}

var _ auth.Authenticator = (*SessionAuthenticator)(nil)

func (this *SessionAuthenticator) Authenticate(ctx context.Context, cred auth.Credential) (auth.Principal, error) {
	if !cred.Is(auth.SchemeBearer) {
		return nil, auth.Unauthenticated("unsupported authentication scheme")
	}

	session, err := this.store.SessionByToken(ctx, HashToken(cred.Token))
	if notFound(err) {
		return nil, auth.Unauthenticated("no session for the presented token")
	}
	if err != nil {
		return nil, err
	}

	now := this.now()
	if !session.Live(now, this.config.IdleTTL) {
		return nil, auth.Unauthenticated("the session is closed")
	}

	ref := SubjectRef{Type: SubjectType(session.SubjectType), ID: session.SubjectID}
	if this.only != "" && ref.Type != this.only {
		return nil, auth.Unauthenticated("the session belongs to another subject type")
	}
	directory, ok := this.grants.Directory(ref.Type)
	if !ok {
		return nil, auth.Unauthenticated("no directory for the session's subject type")
	}
	active, err := directory.Active(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, auth.Unauthenticated("the subject is not active")
	}

	principal, err := this.grants.For(ctx, ref)
	if err != nil {
		return nil, err
	}
	principal.SessionID = session.ID

	this.touch(ctx, session, now)
	return principal, nil
}

func (this *SessionAuthenticator) touch(ctx context.Context, session Session, now time.Time) {
	if this.config.TouchInterval > 0 && now.Sub(session.LastUsedAt) < this.config.TouchInterval {
		return
	}
	_, err := this.store.Sessions.UpdateAll(ctx,
		SessionUpdate{LastUsedAt: &now},
		specs.As(Session_.ID.Eq(session.ID)),
	)
	if err != nil {
		this.logger.WarnContext(ctx, "could not record session activity",
			slog.String("session_id", session.ID.String()), slog.Any("err", err))
	}
}

func NewGuard(authenticator *SessionAuthenticator) *auth.Guard {
	return auth.NewGuard(authenticator, auth.Optional())
}
