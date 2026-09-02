package access

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Endpoints struct {
	subject Subject
	store   *Store
	issuer  SessionIssuer

	now      func() time.Time
	sessions SessionConfig

	refresher SessionRefresher
	login     *LoginUseCase
	logout    *LogoutUseCase
	logoutAll *LogoutAllUseCase
	password  *ChangePasswordUseCase
}

func (this Endpoints) Subject() Subject { return this.subject }

func newEndpoints(dependencies *Deps, subject Subject, issuer SessionIssuer, refresher SessionRefresher) Endpoints {
	return Endpoints{
		subject:   subject,
		store:     dependencies.Store,
		issuer:    issuer,
		now:       dependencies.Now,
		sessions:  dependencies.Config.Sessions(),
		refresher: refresher,
		login:     NewLogin(dependencies).Issuing(issuer),
		logout:    NewLogout(dependencies),
		logoutAll: NewLogoutAll(dependencies),
		password:  NewChangePassword(dependencies),
	}
}

type SignInRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type RefreshRequest struct {
	Refresh string `json:"refresh"`
}

func (this Endpoints) Refresh(ctx context.Context, body RefreshRequest, agent Agent) (AuthResponse, error) {
	if this.refresher == nil {
		return AuthResponse{}, ErrNoRefresh
	}
	return this.refresher.Refresh(ctx, body.Refresh, agent)
}

type ChangeSecretRequest struct {
	Current      string `json:"current"`
	New          string `json:"new"`
	RevokeOthers bool   `json:"revokeOthers"`
}

func (this Endpoints) SignIn(ctx context.Context, body SignInRequest, agent Agent) (AuthResponse, error) {
	return this.login.Execute(ctx, LoginCommand{
		Subject:    this.subject.Type,
		Identifier: this.subject.Identifier(body.Email),
		Password:   body.Password,
		Agent:      agent,
	})
}

func (this Endpoints) SignOut(ctx context.Context) (LogoutResponse, error) {
	principal, err := this.principal(ctx)
	if err != nil {
		return LogoutResponse{}, err
	}
	revoked, err := this.logout.Execute(ctx, LogoutCommand{SessionID: principal.SessionID})
	return LogoutResponse{Revoked: revoked}, err
}

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

func (this Endpoints) WhoAmI(ctx context.Context) (PrincipalDto, error) {
	principal, err := this.principal(ctx)
	if err != nil {
		return PrincipalDto{}, err
	}
	return NewPrincipalDto(principal), nil
}

func (this Endpoints) ListSessions(ctx context.Context) ([]SessionDto, error) {
	principal, err := this.principal(ctx)
	if err != nil {
		return nil, err
	}
	sessions, err := this.store.LiveSessionsOf(ctx, principal.Ref, this.now(), this.sessions.IdleTTL)
	if err != nil {
		return nil, err
	}
	out := make([]SessionDto, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, NewSessionDto(session, principal.SessionID))
	}
	return out, nil
}

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
