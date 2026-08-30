// Package accessjwt is the JWT strategy for access: a short-lived signed access
// token over a rotating refresh credential held as a session row.
//
// # What it buys, and what it costs
//
// It does not save database work in the common case. Authenticating still reads
// the directory to see whether the subject is active and reads the grants to
// resolve roles and permissions — both from rows, both on every request, both
// deliberate ([[D-066]]). What a signed token removes is the *session* read,
// which is one of three.
//
// The reason to choose it is a verifier that cannot reach the database: another
// service, another perimeter, an edge that only holds a public key. Inside one
// process with one database, [access.OpaqueToken] is the simpler thing and this
// is complexity bought for nothing.
//
// # Rotation
//
// A refresh credential is spent by the call that uses it. The session row holds
// the digest of the current one and of the one before it, and a rotation is a
// compare-and-swap on the current digest. Losing that swap is not one situation
// but two, and telling them apart is the whole of the design:
//
//   - The presented digest is the *previous* one and the rotation happened
//     within [Spec.RefreshGrace]. Two tabs refreshed at once. Rotate again and
//     answer normally — refusing here signs people out for using two windows.
//   - The presented digest is the previous one and the grace has passed, or it
//     matches nothing at all. Somebody is replaying a credential that was
//     already spent, which is what a stolen refresh token looks like. The
//     session is closed, and every credential in its lineage with it.
//
// That shape is ported from the Python context this module replaces, where it
// was `classify_lost_cas`, and it earns its keep the first time somebody has
// two tabs open.
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

// A RevocationList makes a signed token stop working before it expires.
//
// Optional, and a deployment should think before adding one. Without it the
// window between "revoked" and "actually refused" is [Spec.AccessTTL], which at
// a few minutes is what most products can live with. With it, every request
// pays a lookup — so the only implementation worth having is one that is faster
// than the database read a signed token was chosen to avoid.
//
// It is keyed by session id and not by token id, because the id survives
// rotation: revoking a session has to close the credentials it has not issued
// yet as well as the one in flight.
type RevocationList interface {
	// Revoked reports whether this session has been closed. An error is
	// returned rather than swallowed: a deny-list that cannot be reached must
	// not silently admit everybody.
	Revoked(ctx context.Context, session uuid.UUID) (bool, error)
	// Revoke closes one, until at least the moment the last token naming it
	// would have expired anyway. Holding it longer is waste; shorter is a hole.
	Revoke(ctx context.Context, session uuid.UUID, until time.Time) error
}

// Claims is what this module signs and reads back.
//
// Roles and permissions are deliberately absent. They come from the rows on
// every request, so a demotion takes effect on the next call rather than when a
// token happens to expire — see [[D-066]]. What is in here is only what the
// database cannot answer without: which session, and which kind of caller.
type Claims struct {
	Subject     string `json:"sub"`
	SubjectType string `json:"sty"`
	SessionID   string `json:"sid"`
	Issuer      string `json:"iss"`
	IssuedAt    int64  `json:"iat"`
	ExpiresAt   int64  `json:"exp"`
}

// A Spec configures the strategy.
type Spec struct {
	// Method and Key sign an access token. HMAC with a []byte secret, or an
	// asymmetric method with its private key.
	Method jwt.SigningMethod
	Key    any
	// Verify is how a presented token is checked. For HMAC it is
	// authjwt.HMAC(secret); for an asymmetric method it is the public half,
	// which is what lets another service verify without being able to mint.
	Verify authjwt.KeySource

	// Issuer goes in `iss` and is required on the way back in.
	Issuer string

	// AccessTTL is how long a signed token lives. It is also the worst-case
	// delay between revoking a session and the revocation biting, when there is
	// no RevocationList. Default five minutes.
	AccessTTL time.Duration
	// RefreshTTL bounds the whole lineage. Default is the session TTL from
	// access.Config, so a rotating session does not outlive an opaque one.
	RefreshTTL time.Duration
	// RefreshGrace is how long after a rotation the previous credential is
	// still treated as a concurrent refresh rather than a replay. Default ten
	// seconds — long enough for two tabs, short enough that a stolen credential
	// is not useful.
	RefreshGrace time.Duration

	// Revocation is optional. nil means a revoked session is refused when its
	// access token expires and not before.
	Revocation RevocationList
}

// Strategy builds the JWT strategy from a spec.
func Strategy(spec Spec) access.Strategy { return &strategy{spec: spec} }

type strategy struct{ spec Spec }

// Defaults applied to a zero value.
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
		// Said out loud rather than left to a default, which is what authjwt
		// insists on. These tokens carry no aud because there is nothing to
		// name: one issuer, one verifier, and the `sty` claim already refuses a
		// token minted for another kind of caller. What makes it safe is that
		// no *second* service trusts this issuer — a fact about the deployment
		// and not about the token, and the day one appears this line is wrong
		// and Claims needs an aud to narrow.
		authjwt.AllowAnyAudience(),
	)

	issued := access.Issued{
		Issuer:        core,
		Refresher:     core,
		Authenticator: &authenticator{core: core, parser: parser},
	}
	// Only when there is a list to write to, and as an explicit branch rather
	// than assigning core either way: a typed nil in that interface is not nil,
	// and Mount correctly refuses it as a broken advertised capability.
	if settings.Revocation != nil {
		issued.Revocations = core
	}
	return issued, nil
}

// SessionsRevoked implements access.RevocationSink.
//
// This is the other half of the deny-list, and without it the list only ever
// held what the replay path put there: signing out closed the row, and the
// signed token — which reads no row — kept working until it expired. See
// [[D-072]].
//
// The deadline is computed here because this is where AccessTTL is known. No
// token minted before now can be accepted after it, so holding the entry longer
// is waste and holding it shorter is a hole.
func (this *core) SessionsRevoked(ctx context.Context, sessions []uuid.UUID) error {
	until := this.now().Add(this.spec.AccessTTL)
	for _, session := range sessions {
		if err := this.spec.Revocation.Revoke(ctx, session, until); err != nil {
			return err
		}
	}
	return nil
}

// core is what the issuer, the refresher and the verifier share.
type core struct {
	spec     Spec
	deps     access.StrategyDeps
	sessions *crud.Repo[rotatingSession, uuid.UUID, rotatingUpdate]
}

func (this *core) now() time.Time { return this.deps.Config.Now() }

// Issue opens a lineage: one session row, one refresh credential, one signed
// access token over it.
func (this *core) Issue(ctx context.Context, subject access.SubjectRef, agent access.Agent) (access.AuthResponse, error) {
	refresh, err := access.NewToken()
	if err != nil {
		return access.AuthResponse{}, err
	}

	now := this.now()
	agent = agent.Truncated()
	saved, err := this.sessions.Save(ctx, &rotatingSession{
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

// answer mints the signed token and renders the principal.
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

// Refresh rotates, and closes the session when it cannot.
//
// The decision itself is [Classify], which is pure and takes no database. What
// is left here is the two lookups that find the row and the write that follows
// the verdict.
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
		// Close the lineage. The holder of the newer credential loses it too,
		// which is the point: one of the two parties is not the account's owner
		// and there is no way to tell which from here.
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

// find looks the digest up as the current credential, then as the previous one.
// Two statements rather than one OR, because the partial index on the previous
// digest only serves the second.
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

// rotate swaps the credential as a compare-and-swap on the current digest.
//
// The swap is what makes two simultaneous refreshes safe: exactly one of them
// matches, the other lands in the previous-digest branch above and is told
// apart there. A read-then-write would let both succeed and issue two lineages
// from one session.
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
			// The swap: the row must still hold the digest this rotation read.
			crud.Eq("TokenHash", session.TokenHash),
			crud.IsNull("RevokedAt"),
		)),
	)
	if err != nil {
		return access.AuthResponse{}, err
	}
	if changed == 0 {
		// Somebody else rotated between the read and the write. Their
		// credential is the live one; this call has nothing to hand back.
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

// refused is the one answer every failed rotation gets.
//
// An unknown credential, an expired session, a deactivated subject and a replay
// are one answer to the client. Telling them apart turns this endpoint into a
// way to probe which credentials were once real.
func refused() error {
	return auth.Unauthenticated("the refresh credential is not usable")
}
