package accessjwt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/auth/authjwt"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type RevocationList interface {
	Revoked(ctx context.Context, session uuid.UUID) (bool, error)

	Revoke(ctx context.Context, session uuid.UUID, until time.Time) error
}

type Claims struct {
	Subject     string `json:"sub"`
	SubjectType string `json:"sty"`
	SessionID   string `json:"sid"`
	Issuer      string `json:"iss"`
	IssuedAt    int64  `json:"iat"`
	ExpiresAt   int64  `json:"exp"`
}

type Spec struct {
	Method jwt.SigningMethod
	Key    any

	Verify authjwt.KeySource

	Issuer string

	AccessTTL time.Duration

	RefreshTTL time.Duration

	RefreshGrace time.Duration

	Revocation RevocationList
}

func Strategy(spec Spec) access.Strategy { return &strategy{spec: spec} }

type strategy struct{ spec Spec }

const (
	DefaultAccessTTL    = 5 * time.Minute
	DefaultRefreshGrace = 10 * time.Second
)

func (this *strategy) Build(dependencies access.StrategyDeps) (access.Issued, error) {
	if this.spec.Method == nil || this.spec.Key == nil {
		return access.Issued{}, fmt.Errorf("accessjwt: a signing method and key are required")
	}
	if this.spec.Issuer == "" {
		return access.Issued{}, fmt.Errorf("accessjwt: an issuer is required; a token nobody claims is one nobody can scope")
	}

	settings := this.spec
	if settings.AccessTTL <= 0 {
		settings.AccessTTL = DefaultAccessTTL
	}
	if settings.RefreshTTL <= 0 {
		settings.RefreshTTL = dependencies.Config.Sessions().TTL
	}
	if settings.RefreshGrace <= 0 {
		settings.RefreshGrace = DefaultRefreshGrace
	}

	sessions := sqlrepo.Define[rotatingSession, uuid.UUID, rotatingUpdate]("").Bind(dependencies.Source)
	core := &core{spec: settings, deps: dependencies, sessions: sessions}

	parser := authjwt.New[Claims](settings.Verify,
		authjwt.Issuer(settings.Issuer),

		authjwt.AllowAnyAudience(),
	)

	issued := access.Issued{
		Issuer:        core,
		Refresher:     core,
		Authenticator: &authenticator{core: core, parser: parser},
	}

	if settings.Revocation != nil {
		issued.Revocations = core
	}
	return issued, nil
}

func (this *core) SessionsRevoked(ctx context.Context, sessions []uuid.UUID) error {
	until := this.now().Add(this.spec.AccessTTL)
	for _, session := range sessions {
		if err := this.spec.Revocation.Revoke(ctx, session, until); err != nil {
			return err
		}
	}
	return nil
}

type core struct {
	spec     Spec
	deps     access.StrategyDeps
	sessions *crud.Repo[rotatingSession, uuid.UUID, rotatingUpdate]
}

func (this *core) now() time.Time { return this.deps.Config.Now() }

func (this *core) Issue(ctx context.Context, subject access.SubjectRef, agent access.Agent) (access.AuthResponse, error) {
	refresh, err := access.NewToken()
	if err != nil {
		return access.AuthResponse{}, err
	}
	sessionID, err := uuid.NewRandom()
	if err != nil {
		return access.AuthResponse{}, fmt.Errorf("accessjwt: reading entropy for a session id: %w", err)
	}

	now := this.now()
	agent = agent.Truncated()
	saved, err := this.sessions.Save(ctx, &rotatingSession{
		ID:          sessionID,
		SubjectType: string(subject.Type),
		SubjectID:   subject.ID,
		TokenHash:   access.HashToken(refresh),
		UserAgent:   agent.UserAgent,
		IP:          agent.IP,
		LastUsedAt:  now,
		ExpiresAt:   now.Add(this.spec.RefreshTTL),
	})
	if err != nil {
		return access.AuthResponse{}, err
	}
	return this.answer(ctx, subject, saved.ID, refresh, saved.ExpiresAt, now)
}

func (this *core) answer(
	ctx context.Context,
	subject access.SubjectRef,
	session uuid.UUID,
	refresh string,
	refreshExpiry time.Time,
	now time.Time,
) (access.AuthResponse, error) {
	expiry := now.Add(this.spec.AccessTTL)
	token, err := jwt.NewWithClaims(this.spec.Method, jwt.MapClaims{
		"sub": subject.ID.String(),
		"sty": string(subject.Type),
		"sid": session.String(),
		"iss": this.spec.Issuer,
		"iat": now.Unix(),
		"exp": expiry.Unix(),
	}).SignedString(this.spec.Key)
	if err != nil {
		return access.AuthResponse{}, fmt.Errorf("accessjwt: signing an access token: %w", err)
	}

	principal, err := this.deps.Grants.For(ctx, subject)
	if err != nil {
		return access.AuthResponse{}, err
	}
	principal.SessionID = session

	return access.AuthResponse{
		Token:            token,
		ExpiresAt:        expiry,
		Refresh:          refresh,
		RefreshExpiresAt: refreshExpiry,
		Principal:        access.NewPrincipalDto(principal),
	}, nil
}

func (this *core) Refresh(ctx context.Context, credential string, agent access.Agent) (access.AuthResponse, error) {
	if credential == "" {
		return access.AuthResponse{}, refused()
	}
	digest := access.HashToken(credential)
	now := this.now()

	session, err := this.find(ctx, digest)
	if err != nil {
		return access.AuthResponse{}, err
	}
	if session == nil {
		return access.AuthResponse{}, refused()
	}

	switch Classify(presentedOf(*session, digest), now, this.spec.RefreshGrace) {
	case Rotate, RotateAgain:
		return this.rotate(ctx, *session, now)
	case Replay:
		if err := this.close(ctx, session.ID, now, access.ReasonRefreshReplayed); err != nil {
			return access.AuthResponse{}, err
		}
		this.deps.Logger.WarnContext(ctx, "a spent refresh credential was replayed; the session was closed",
			slog.String("session_id", session.ID.String()),
			slog.String("subject_type", session.SubjectType))
		return access.AuthResponse{}, refused()
	default:
		return access.AuthResponse{}, refused()
	}
}

func (this *core) find(ctx context.Context, digest string) (*rotatingSession, error) {
	session, err := this.sessions.First(ctx, crud.Where(crud.Eq("TokenHash", digest)))
	if err == nil {
		return &session, nil
	}
	if !errors.Is(err, crud.ErrNotFound) {
		return nil, err
	}
	session, err = this.sessions.First(ctx, crud.Where(crud.Eq("PreviousTokenHash", digest)))
	if errors.Is(err, crud.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func presentedOf(session rotatingSession, digest string) Presented {
	return Presented{
		Digest:    digest,
		Current:   session.TokenHash,
		Previous:  session.PreviousTokenHash,
		RotatedAt: session.RotatedAt,
		Revoked:   session.RevokedAt != nil,
		ExpiresAt: session.ExpiresAt,
	}
}

func (this *core) rotate(ctx context.Context, session rotatingSession, now time.Time) (access.AuthResponse, error) {
	subject := access.SubjectRef{Type: access.SubjectType(session.SubjectType), ID: session.SubjectID}

	directory, served := this.deps.Grants.Directory(subject.Type)
	if !served {
		return access.AuthResponse{}, refused()
	}
	active, err := directory.Active(ctx, subject.ID)
	if err != nil {
		return access.AuthResponse{}, err
	}
	if !active {
		return access.AuthResponse{}, refused()
	}

	next, err := access.NewToken()
	if err != nil {
		return access.AuthResponse{}, err
	}
	digest := access.HashToken(next)

	changed, err := this.sessions.UpdateAll(ctx,
		rotatingUpdate{
			TokenHash:         &digest,
			PreviousTokenHash: &session.TokenHash,
			RotatedAt:         crud.Set(now),
			LastUsedAt:        &now,
		},
		crud.Where(crud.And(
			crud.Eq("ID", session.ID),

			crud.Eq("TokenHash", session.TokenHash),
			crud.IsNull("RevokedAt"),
		)),
	)
	if err != nil {
		return access.AuthResponse{}, err
	}
	if changed == 0 {
		return access.AuthResponse{}, refused()
	}
	return this.answer(ctx, subject, session.ID, next, session.ExpiresAt, now)
}

func (this *core) close(ctx context.Context, session uuid.UUID, now time.Time, reason string) error {
	if _, err := this.sessions.UpdateAll(ctx,
		rotatingUpdate{RevokedAt: crud.Set(now), RevokedReason: &reason},
		crud.Where(crud.And(crud.Eq("ID", session), crud.IsNull("RevokedAt"))),
	); err != nil {
		return err
	}
	if this.spec.Revocation == nil {
		return nil
	}
	return this.spec.Revocation.Revoke(ctx, session, now.Add(this.spec.AccessTTL))
}

func refused() error {
	return auth.Unauthenticated("the refresh credential is not usable")
}
