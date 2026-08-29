package access

import (
	"context"
	"log/slog"
	"time"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud/decorators/specs"
)

// SessionAuthenticator turns a presented bearer token into this application's
// [Principal]. It is the auth.Authenticator the guard is built over, and the
// only place a token becomes a caller.
//
// The token is opaque — 256 random bits, stored as a digest — rather than a
// JWT, and that is the decision this file rests on. Every property the product
// needs from a session is a property of a row: signing out closes it, signing
// out everywhere closes the rest, deactivating an account locks it, and a
// demoted role takes effect on the next request. A self-contained token gives
// none of those back without a revocation list, which is the same table with an
// inverted meaning and a worse failure mode.
//
// What it costs is a read per request. That is what the touch threshold below
// is about: the read is cheap, and it is only the *write* that had to be
// rationed.
type SessionAuthenticator struct {
	store  *Store
	grants *GrantsService
	config SessionConfig
	logger *slog.Logger
	now    func() time.Time

	// only, when set, is the one subject type this authenticator answers for.
	//
	// It is what makes a guard mounted on one subject's routes refuse a session
	// belonging to another. Without it a token minted for a customer is
	// accepted by the staff surface — the session is genuine, so nothing looks
	// wrong, and the caller is simply somebody else than the route assumed.
	only SubjectType
}

// For narrows this authenticator to one subject type.
//
// A copy rather than a mutation: the runtime builds one authenticator per
// subject off the same stores, and a setter would have the last subject
// registered decide for all of them.
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

// Authenticate implements auth.Authenticator.
//
// Every refusal is auth.Unauthenticated with a reason that stays in the wrapped
// error and never reaches a body: an unknown token, an expired one and one
// belonging to a deactivated account are one answer to the client. A failure
// that is *not* a refusal — the database is down — is returned as itself, so it
// renders as a 500 and shows up where somebody is watching the 5xx rate rather
// than as a mysterious wave of 401s.
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
		// A session for a subject type nothing serves any more. Refusing is the
		// only safe reading: the store that could have said "no" is gone.
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

// touch keeps last_used_at fresh enough for the idle timeout to mean something,
// without making every authenticated read a write.
//
// The failure is logged and swallowed on purpose: a request that authenticated
// correctly must not fail because a bookkeeping UPDATE lost a race.
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

// NewGuard is the guard the transport mounts.
//
// It is Optional, and that is not a hole. Optional means an anonymous request
// reaches the handler without a principal — which is what /auth/login needs —
// and everything after it still fails closed: every gated repository refuses an
// absent principal, and every handler that needs a caller asks
// [RequirePrincipal]. A *bad* token is still a 401 at the door, whatever this
// setting says.
func NewGuard(authenticator *SessionAuthenticator) *auth.Guard {
	return auth.NewGuard(authenticator, auth.Optional())
}
