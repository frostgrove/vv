package access

import (
	"context"

	"github.com/google/uuid"
)

// Endpoints is one subject's sign-in operations, transport-neutral.
//
// It is what [Mount] builds and a binding is handed. Every method takes decoded
// values and answers a value or an error, so a binding decodes, calls and
// writes — and the three of them cannot drift on which subject type a sign-in
// is for or which rule folds an identifier.
//
// Registering is deliberately not here. It is the one operation whose shape is
// the application's, so it is [SignUpUseCase], generic over that shape, handed
// back by [Mount] separately. Threading that type parameter through here would
// put it on all seven of these — and on every binding's handler — for the sake
// of one, and force the whole set through an `any` to get out of a
// [MountedSubject] that cannot itself be generic.
type Endpoints struct {
	subject Subject
	store   *Store
	issuer  SessionIssuer

	refresher SessionRefresher
	login     *LoginUseCase
	logout    *LogoutUseCase
	logoutAll *LogoutAllUseCase
	password  *ChangePasswordUseCase
}

// Subject answers the kind of caller these endpoints are for.
func (this Endpoints) Subject() Subject { return this.subject }

// newEndpoints assembles one subject's operations over the shared use cases.
func newEndpoints(dependencies *Deps, subject Subject, issuer SessionIssuer, refresher SessionRefresher) Endpoints {
	return Endpoints{
		subject:   subject,
		store:     dependencies.Store,
		issuer:    issuer,
		refresher: refresher,
		login:     NewLogin(dependencies).Issuing(issuer),
		logout:    NewLogout(dependencies),
		logoutAll: NewLogoutAll(dependencies),
		password:  NewChangePassword(dependencies),
	}
}

// A SignInRequest is what the login endpoint reads. The identifier is spelled
// `email` because that is what it is in every application that has used this so
// far; a deployment whose identifier is not an address decodes its own body and
// calls [Endpoints.SignIn] with it.
type SignInRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// A RefreshRequest is what the refresh endpoint reads.
type RefreshRequest struct {
	Refresh string `json:"refresh"`
}

// Refresh rotates a caller's credential.
//
// Anonymous on purpose: the whole reason to refresh is that the access token is
// gone or expired, so requiring one would make the endpoint useless exactly
// when it is needed. What authenticates the call is the rotating credential
// itself.
func (this Endpoints) Refresh(ctx context.Context, body RefreshRequest, agent Agent) (AuthResponse, error) {
	if this.refresher == nil {
		return AuthResponse{}, ErrNoRefresh
	}
	return this.refresher.Refresh(ctx, body.Refresh, agent)
}

// A ChangeSecretRequest is what the password endpoint reads.
type ChangeSecretRequest struct {
	Current      string `json:"current"`
	New          string `json:"new"`
	RevokeOthers bool   `json:"revokeOthers"`
}

// SignIn exchanges an identifier and a password for a session.
//
// The subject type and the identifier rule are the surface's, not the caller's.
// That is the whole reason this step is here rather than in each binding: a
// login handler that passed the raw body through would sign a caller in to
// whatever subject type happened to hold that identifier — and with an
// identifier unique per type rather than globally, that is a different person.
func (this Endpoints) SignIn(ctx context.Context, body SignInRequest, agent Agent) (AuthResponse, error) {
	return this.login.Execute(ctx, LoginCommand{
		Subject:    this.subject.Type,
		Identifier: this.subject.Identifier(body.Email),
		Password:   body.Password,
		Agent:      agent,
	})
}

// SignOut closes the session the request arrived on.
func (this Endpoints) SignOut(ctx context.Context) (LogoutResponse, error) {
	principal, err := this.principal(ctx)
	if err != nil {
		return LogoutResponse{}, err
	}
	revoked, err := this.logout.Execute(ctx, LogoutCommand{SessionID: principal.SessionID})
	return LogoutResponse{Revoked: revoked}, err
}

// SignOutAll closes the caller's other sessions, and this one too when
// everywhere is true.
//
// Keeping the current session is the default because "sign out everywhere else"
// is what a person means by the button; closing it as well lands the
// confirmation on a login screen.
func (this Endpoints) SignOutAll(ctx context.Context, everywhere bool) (LogoutResponse, error) {
	principal, err := this.principal(ctx)
	if err != nil {
		return LogoutResponse{}, err
	}
	except := principal.SessionID
	if everywhere {
		except = uuid.Nil
	}
	revoked, err := this.logoutAll.Execute(ctx, LogoutAllCommand{
		Subject: principal.Ref,
		Except:  except,
	})
	return LogoutResponse{Revoked: revoked}, err
}

// ChangeSecret replaces the caller's own password.
func (this Endpoints) ChangeSecret(ctx context.Context, body ChangeSecretRequest) (LogoutResponse, error) {
	principal, err := this.principal(ctx)
	if err != nil {
		return LogoutResponse{}, err
	}
	revoked, err := this.password.Execute(ctx, ChangePasswordCommand{
		Subject:      principal.Ref,
		Current:      body.Current,
		New:          body.New,
		RevokeOthers: body.RevokeOthers,
		Keep:         principal.SessionID,
	})
	return LogoutResponse{Revoked: revoked}, err
}

// WhoAmI answers what the caller is, resolved now rather than when the token
// was minted. A client that polls this sees a demotion within one poll.
func (this Endpoints) WhoAmI(ctx context.Context) (PrincipalDto, error) {
	principal, err := this.principal(ctx)
	if err != nil {
		return PrincipalDto{}, err
	}
	return NewPrincipalDto(principal), nil
}

// ListSessions answers the caller's live sign-ins, marking the current one.
func (this Endpoints) ListSessions(ctx context.Context) ([]SessionDto, error) {
	principal, err := this.principal(ctx)
	if err != nil {
		return nil, err
	}
	sessions, err := this.store.LiveSessionsOf(ctx, principal.Ref)
	if err != nil {
		return nil, err
	}
	out := make([]SessionDto, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, NewSessionDto(session, principal.SessionID))
	}
	return out, nil
}

// KillSession closes one of the caller's own sessions.
//
// Only their own: the subject narrows the statement, so an id belonging to
// somebody else matches no row and answers the same as one already closed. A
// 404 here would confirm which ids exist.
func (this Endpoints) KillSession(ctx context.Context, rawID string) error {
	principal, err := this.principal(ctx)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		return BadSessionID(rawID)
	}
	_, err = this.logoutAll.RevokeOne(ctx, principal.Ref, id)
	return err
}

func (this Endpoints) principal(ctx context.Context) (*Principal, error) {
	return RequirePrincipal(ctx)
}
