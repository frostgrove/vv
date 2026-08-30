package access

import (
	"context"
	"log/slog"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
	"github.com/google/uuid"
)

// A SessionIssuer turns "this caller is who they say they are" into whatever a
// client presents on the next request.
//
// It is the half of a [Strategy] that runs at sign-in. Everything that decided
// the caller is genuine — a verified password, an OAuth assertion, an
// administrator acting deliberately — ends here, and this is the only place a
// token of any kind comes into existence.
type SessionIssuer interface {
	Issue(ctx context.Context, subject SubjectRef, agent Agent) (AuthResponse, error)
}

// A Strategy is how one kind of caller holds a session: what is minted at
// sign-in and what is accepted on the next request.
//
// One value declares both ends, and that is the whole point. Issuing and
// verifying have to agree about the format, the key and the subject type; wired
// separately they agree until somebody changes one of them, and the failure is
// either every caller logged out at once or — worse — a token minted for one
// subject type accepted as another.
type Strategy interface {
	// Build wires the strategy for one subject against the runtime's stores.
	//
	// It is called once per subject at composition, not per request. An error
	// is a misconfiguration and stops the process starting. Mount validates the
	// returned Issued before publishing any part of the subject registration.
	Build(dependencies StrategyDeps) (Issued, error)
}

// StrategyDeps is what a strategy is given to build itself from.
type StrategyDeps struct {
	Subject Subject
	Store   *Store
	// Source is the datasource the store was built over, for a strategy that
	// needs a repository of its own — a wider model over the sessions table,
	// say, when the strategy adds columns the core does not know about.
	Source crud.Source
	Grants *GrantsService
	Config Config
	Logger *slog.Logger
}

// A SessionRefresher exchanges a rotating credential for the next one.
//
// A strategy that does not rotate returns nil for it, and the refresh route is
// not mounted at all: a path nothing serves answers 404, which is the honest
// shape of "this deployment has nothing to refresh".
type SessionRefresher interface {
	// Refresh rotates. The credential it is given is spent by the call, and a
	// second use of it is a replay rather than a mistake — see the strategy
	// that implements this for what that costs the caller.
	Refresh(ctx context.Context, credential string, agent Agent) (AuthResponse, error)
}

// A RevocationSink is told which sessions a closing call has just closed.
//
// A strategy that verifies a credential by reading its session row needs none:
// the row it reads on the next request is the row the sign-out wrote, so
// "closed" is already true. A strategy that verifies without reading — a signed
// token — does, because nothing it checks per request has changed and the
// credential stays valid until it expires.
//
// Without it a deny-list is written only by the strategy's own replay path, and
// signing out closes the session everywhere except in the one place the next
// request will look. See [[D-072]].
//
// nil is the ordinary case and costs an opaque deployment nothing.
type RevocationSink interface {
	// SessionsRevoked reports sessions that have just been closed.
	//
	// How long the fact has to be remembered is the implementation's own
	// question — it is the one that knows how long a credential naming these
	// sessions could still be accepted — so no deadline is passed in.
	SessionsRevoked(ctx context.Context, sessions []uuid.UUID) error
}

// Issued is what a built strategy answers with.
type Issued struct {
	// Issuer and Authenticator are required. Mount rejects literal and typed nil
	// implementations at composition rather than leaving a request-time panic.
	Issuer        SessionIssuer
	Authenticator auth.Authenticator
	// Refresher is optional. Literal nil means this strategy does not rotate; a
	// typed nil is an invalid declaration because it would mount a broken route.
	Refresher SessionRefresher
	// Revocations is optional. Literal nil means closing a session is fully
	// expressed by the row, which is true of everything that verifies by reading
	// it. A typed nil is rejected before it can be registered as a callable sink.
	Revocations RevocationSink
}

// OpaqueToken is the strategy this module ships: 256 random bits, stored as a
// digest in the sessions table, verified by reading it back.
//
// It is the default because every property a product asks of a session is a
// property of a row here. Signing out closes it, signing out everywhere closes
// the rest, deactivating an account locks it, and a demoted role takes effect on
// the next request. A self-contained token gives none of those back without a
// revocation list — which is the same table with an inverted meaning and a
// worse failure mode, because an unreachable deny-list either admits everybody
// or nobody while an unreachable allow-list simply refuses.
//
// What it costs is one read per request. That is the trade, stated plainly.
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

// opaqueIssuer mints a session row and hands the token back once.
type opaqueIssuer struct {
	deps StrategyDeps
}

func (this *opaqueIssuer) Issue(ctx context.Context, subject SubjectRef, agent Agent) (AuthResponse, error) {
	token, err := NewToken()
	if err != nil {
		return AuthResponse{}, err
	}

	now := this.deps.Config.Now()
	agent = agent.Truncated()
	saved, err := this.deps.Store.Sessions.Save(ctx, &Session{
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
