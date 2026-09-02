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

	Audience string

	UnsafeAnyAudience bool

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
	if this.spec.Audience != "" && this.spec.UnsafeAnyAudience {
		return access.Issued{}, fmt.Errorf(
			"accessjwt: Audience %q and UnsafeAnyAudience name opposite policies; a token cannot both be scoped and be replayable anywhere",
			this.spec.Audience)
	}

	lifetimes := dependencies.Config.Sessions()
	settings := this.spec
	if err := refuseLifetimesBelowZero(settings, lifetimes); err != nil {
		return access.Issued{}, err
	}
	if settings.AccessTTL == 0 {
		settings.AccessTTL = DefaultAccessTTL
	}
	if settings.RefreshTTL == 0 {
		settings.RefreshTTL = lifetimes.TTL
	}
	if settings.RefreshGrace == 0 {
		settings.RefreshGrace = DefaultRefreshGrace
	}
	if settings.Audience == "" && !settings.UnsafeAnyAudience {
		settings.Audience = settings.Issuer
	}
	window := Window{Grace: settings.RefreshGrace, Idle: lifetimes.IdleTTL}
	if err := checkLifetimes(settings, lifetimes, window); err != nil {
		return access.Issued{}, err
	}

	sessions := sqlrepo.Define[rotatingSession, uuid.UUID, rotatingUpdate]("").Bind(dependencies.Source)
	core := &core{spec: settings, window: window, deps: dependencies, sessions: sessions}

	audience := authjwt.Audience(settings.Audience)
	if settings.UnsafeAnyAudience {
		audience = authjwt.AllowAnyAudience()
	}
	parser := authjwt.New[Claims](settings.Verify, authjwt.Issuer(settings.Issuer), audience)

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
	window   Window
	deps     access.StrategyDeps
	sessions *crud.Repo[rotatingSession, uuid.UUID, rotatingUpdate]
}

// refuseLifetimesBelowZero separates a lifetime left out from a lifetime written wrongly.
//
// Zero is a caller taking the default, which is what leaving a field of Spec unset says. A
// negative duration is a caller who meant something and got it wrong — a subtraction that came
// out backwards, a flag parsed from a string — and replacing it with the default is the clamp
// [[D-088]] forbids: the deployment would report the lifetime it configured and mint tokens on
// another one, and no check below would ever mention the value that was written.
func refuseLifetimesBelowZero(settings Spec, lifetimes access.SessionConfig) error {
	var problems []error
	if settings.AccessTTL < 0 {
		problems = append(problems, fmt.Errorf(
			"accessjwt: AccessTTL is %s; leave it at zero to take the default of %s",
			settings.AccessTTL, DefaultAccessTTL))
	}
	if settings.RefreshTTL < 0 {
		problems = append(problems, fmt.Errorf(
			"accessjwt: RefreshTTL is %s; leave it at zero to take the session TTL of %s",
			settings.RefreshTTL, lifetimes.TTL))
	}
	if settings.RefreshGrace < 0 {
		problems = append(problems, fmt.Errorf(
			"accessjwt: RefreshGrace is %s; leave it at zero to take the default of %s",
			settings.RefreshGrace, DefaultRefreshGrace))
	}
	return errors.Join(problems...)
}

func checkLifetimes(settings Spec, lifetimes access.SessionConfig, window Window) error {
	switch {
	case settings.RefreshTTL > lifetimes.TTL:
		return fmt.Errorf(
			"accessjwt: RefreshTTL %s outlives the session TTL %s; the lineage would keep rotating past the session row it belongs to",
			settings.RefreshTTL, lifetimes.TTL)
	case settings.AccessTTL > settings.RefreshTTL:
		return fmt.Errorf(
			"accessjwt: AccessTTL %s outlives RefreshTTL %s; the access token would still verify when nothing can renew it",
			settings.AccessTTL, settings.RefreshTTL)
	case window.Idle > 0 && settings.AccessTTL > window.Idle:
		return fmt.Errorf(
			"accessjwt: AccessTTL %s outlives the session idle TTL %s; an abandoned session would keep answering past the idle deadline",
			settings.AccessTTL, window.Idle)
	case settings.RefreshGrace >= settings.RefreshTTL:
		return fmt.Errorf(
			"accessjwt: RefreshGrace %s is not shorter than RefreshTTL %s; a spent credential would never become a replay",
			settings.RefreshGrace, settings.RefreshTTL)
	}
	return nil
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
	sessionExpiry time.Time,
	now time.Time,
) (access.AuthResponse, error) {
	expiry := now.Add(this.spec.AccessTTL)
	if expiry.After(sessionExpiry) {
		expiry = sessionExpiry
	}
	claims := jwt.MapClaims{
		"sub": subject.ID.String(),
		"sty": string(subject.Type),
		"sid": session.String(),
		"iss": this.spec.Issuer,
		"iat": now.Unix(),
		"exp": expiry.Unix(),
	}
	if this.spec.Audience != "" {
		claims["aud"] = this.spec.Audience
	}
	token, err := jwt.NewWithClaims(this.spec.Method, claims).SignedString(this.spec.Key)
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
		RefreshExpiresAt: sessionExpiry,
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

	switch Classify(presentedOf(*session, digest), now, this.window) {
	case Rotate, RotateAgain:
		return this.rotate(ctx, *session, digest, now)
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
		Digest:     digest,
		Current:    session.TokenHash,
		Previous:   session.PreviousTokenHash,
		RotatedAt:  session.RotatedAt,
		LastUsedAt: session.LastUsedAt,
		Revoked:    session.RevokedAt != nil,
		ExpiresAt:  session.ExpiresAt,
	}
}

const rotationAttempts = 3

func (this *core) rotate(
	ctx context.Context,
	session rotatingSession,
	presented string,
	now time.Time,
) (access.AuthResponse, error) {
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

	response, err := this.answer(ctx, subject, session.ID, next, session.ExpiresAt, now)
	if err != nil {
		return access.AuthResponse{}, err
	}

	current := session
	for attempt := 0; ; attempt++ {
		changed, err := this.swap(ctx, current, digest, now)
		if err != nil {
			return access.AuthResponse{}, err
		}
		if changed > 0 {
			return response, nil
		}
		if attempt+1 == rotationAttempts {
			return access.AuthResponse{}, refused()
		}
		fresh, err := this.reread(ctx, session.ID, presented, now)
		if err != nil {
			return access.AuthResponse{}, err
		}
		current = *fresh
	}
}

func (this *core) swap(ctx context.Context, session rotatingSession, digest string, now time.Time) (int64, error) {
	return this.sessions.UpdateAll(ctx,
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
}

func (this *core) reread(
	ctx context.Context,
	id uuid.UUID,
	presented string,
	now time.Time,
) (*rotatingSession, error) {
	session, err := this.sessions.GetByID(ctx, id)
	if errors.Is(err, crud.ErrNotFound) {
		return nil, refused()
	}
	if err != nil {
		return nil, err
	}
	switch Classify(presentedOf(session, presented), now, this.window) {
	case Rotate, RotateAgain:
		return &session, nil
	default:
		return nil, refused()
	}
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
