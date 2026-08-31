package access

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
	"github.com/google/uuid"
)

type SessionIssuer interface {
	Issue(ctx context.Context, subject SubjectRef, agent Agent) (AuthResponse, error)
}

type Strategy interface {
	Build(dependencies StrategyDeps) (Issued, error)
}

type StrategyDeps struct {
	Subject Subject
	Store   *Store

	Source crud.Source
	Grants *GrantsService
	Config Config
	Logger *slog.Logger
}

type SessionRefresher interface {
	Refresh(ctx context.Context, credential string, agent Agent) (AuthResponse, error)
}

type RevocationSink interface {
	SessionsRevoked(ctx context.Context, sessions []uuid.UUID) error
}

type Issued struct {
	Issuer        SessionIssuer
	Authenticator auth.Authenticator

	Refresher SessionRefresher

	Revocations RevocationSink
}

func OpaqueToken() Strategy { return opaqueStrategy{} }

type opaqueStrategy struct{}

func (opaqueStrategy) Build(dependencies StrategyDeps) (Issued, error) {
	authenticator := NewAuthenticator(
		dependencies.Store,
		dependencies.Grants,
		dependencies.Config,
		dependencies.Logger,
	).For(dependencies.Subject.Type)

	return Issued{
		Issuer:        &opaqueIssuer{deps: dependencies},
		Authenticator: authenticator,
	}, nil
}

type opaqueIssuer struct {
	deps StrategyDeps
}

func (this *opaqueIssuer) Issue(ctx context.Context, subject SubjectRef, agent Agent) (AuthResponse, error) {
	token, err := NewToken()
	if err != nil {
		return AuthResponse{}, err
	}
	sessionID, err := uuid.NewRandom()
	if err != nil {
		return AuthResponse{}, fmt.Errorf("access: reading entropy for a session id: %w", err)
	}

	now := this.deps.Config.Now()
	agent = agent.Truncated()
	saved, err := this.deps.Store.Sessions.Save(ctx, &Session{
		ID:          sessionID,
		SubjectType: string(subject.Type),
		SubjectID:   subject.ID,
		TokenHash:   HashToken(token),
		UserAgent:   agent.UserAgent,
		IP:          agent.IP,
		LastUsedAt:  now,
		ExpiresAt:   now.Add(this.deps.Config.Sessions().TTL),
	})
	if err != nil {
		return AuthResponse{}, err
	}

	principal, err := this.deps.Grants.For(ctx, subject)
	if err != nil {
		return AuthResponse{}, err
	}
	principal.SessionID = saved.ID

	return AuthResponse{
		Token:     token,
		ExpiresAt: saved.ExpiresAt,
		Principal: NewPrincipalDto(principal),
	}, nil
}
