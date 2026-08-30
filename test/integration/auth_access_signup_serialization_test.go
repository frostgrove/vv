//go:build integration

package integration

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/google/uuid"
)

type authSignUpAccount struct {
	ID         uuid.UUID `db:"id,pk"`
	Identifier string
}

type authSignUpAccountUpdate struct {
	Identifier *string
}

var authSignUpAccounts = sqlrepo.Define[authSignUpAccount, uuid.UUID, authSignUpAccountUpdate](
	"auth_signup_accounts",
)

type authSignUpForm struct {
	Identifier string
	Password   string
}

type authSignUpRegistrar struct {
	accounts *crud.Repo[authSignUpAccount, uuid.UUID, authSignUpAccountUpdate]
	created  uuid.UUID
}

func (this *authSignUpRegistrar) Create(
	ctx context.Context,
	form authSignUpForm,
) (uuid.UUID, string, error) {
	this.created = uuid.New()
	err := this.accounts.SaveOnly(ctx, &authSignUpAccount{
		ID: this.created, Identifier: form.Identifier,
	})
	return this.created, form.Identifier, err
}

func (*authSignUpRegistrar) Password(form authSignUpForm) string { return form.Password }

func TestAccessSignUpOwnsCredentialAndSessionCommit(t *testing.T) {
	targets := []authSerializationTarget{
		{name: "postgres", database: pgDB, source: crudsql.Postgres(pgDB), postgres: true},
		{name: "mysql", database: myDB, source: crudsql.MySQL(myDB)},
		{name: "mariadb", database: mariaDB, source: crudsql.MariaDB(mariaDB)},
	}
	strategies := []authStrategyFixture{
		{name: "opaque", build: func() access.Strategy { return access.OpaqueToken() }},
		{name: "jwt", build: newAuthJWTStrategy},
	}

	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			target.install(t)
			t.Cleanup(func() { target.drop(t) })
			for _, strategy := range strategies {
				t.Run(strategy.name, func(t *testing.T) {
					t.Run("issuer failure rolls account credential and session back", func(t *testing.T) {
						target.signUpIssuerFailureRollsBack(t, strategy)
					})
					t.Run("commit failure returns no token and rolls all rows back", func(t *testing.T) {
						target.signUpCommitFailureRollsBack(t, strategy)
					})
					t.Run("ambient rollback cannot undo returned signup", func(t *testing.T) {
						target.signUpAmbientRollbackCannotUndoCommit(t, strategy)
					})
					for _, invalidation := range []string{"reset", "logout-all"} {
						t.Run("commit-before-"+invalidation+" publishes session atomically", func(t *testing.T) {
							target.signUpCommitBeforeInvalidation(t, strategy, invalidation)
						})
					}
				})
			}
		})
	}
}

func (this authSerializationTarget) signUpIssuerFailureRollsBack(
	t *testing.T,
	strategy authStrategyFixture,
) {
	t.Helper()
	this.clear(t)
	issuerFailure := errors.New("signup grant lookup failed after session insert")
	fail := func(query string) error {
		if authSubjectRoleRead(query) {
			return issuerFailure
		}
		return nil
	}
	source := newAuthHookSource(this.source, nil, fail)
	runtime, _, signUp, registrar := authSignUpRuntime(t, source, strategy.build())

	response, err := signUp.Execute(t.Context(), authSignUpForm{
		Identifier: identifier, Password: oldPassword,
	}, access.Agent{})
	if !errors.Is(err, issuerFailure) {
		t.Fatalf("issuer error=%v, want %v", err, issuerFailure)
	}
	if response.Token != "" || response.Refresh != "" {
		t.Fatalf("failed signup leaked response %+v", response)
	}
	assertAuthSignUpRows(t, runtime, registrar, 0, 0, 0)
}

func (this authSerializationTarget) signUpCommitFailureRollsBack(
	t *testing.T,
	strategy authStrategyFixture,
) {
	t.Helper()
	this.clear(t)
	commitFailure := errors.New("signup database commit failed")
	source := newAuthHookSource(this.source, nil, nil)
	source.commitErr = commitFailure
	runtime, _, signUp, registrar := authSignUpRuntime(t, source, strategy.build())

	response, err := signUp.Execute(t.Context(), authSignUpForm{
		Identifier: identifier, Password: oldPassword,
	}, access.Agent{})
	if !errors.Is(err, commitFailure) {
		t.Fatalf("commit error=%v, want %v", err, commitFailure)
	}
	if response.Token != "" || response.Refresh != "" {
		t.Fatalf("commit-failed signup leaked response %+v", response)
	}
	assertAuthSignUpRows(t, runtime, registrar, 0, 0, 0)
}

func (this authSerializationTarget) signUpAmbientRollbackCannotUndoCommit(
	t *testing.T,
	strategy authStrategyFixture,
) {
	t.Helper()
	this.clear(t)
	source := newAuthHookSource(this.source, nil, nil)
	runtime, mounted, signUp, registrar := authSignUpRuntime(t, source, strategy.build())
	outer := beginAuthOuter(t, source)
	ctx := crud.BindExecutor(t.Context(), source, outer)

	response, err := signUp.Execute(ctx, authSignUpForm{
		Identifier: identifier, Password: oldPassword,
	}, access.Agent{UserAgent: "signup-ambient-owner"})
	if err != nil || response.Token == "" {
		t.Fatalf("signup response=%+v err=%v", response, err)
	}
	if err := outer.Rollback(t.Context()); err != nil {
		t.Fatalf("rolling back ambient signup owner: %v", err)
	}
	assertAuthSignUpRows(t, runtime, registrar, 1, 1, 1)
	if _, err := mounted.Authenticator().Authenticate(t.Context(), auth.Credential{
		Scheme: auth.SchemeBearer, Token: response.Token,
	}); err != nil {
		t.Fatalf("ambient rollback undid returned signup token: %v", err)
	}
}

func (this authSerializationTarget) signUpCommitBeforeInvalidation(
	t *testing.T,
	strategy authStrategyFixture,
	invalidation string,
) {
	t.Helper()
	this.clear(t)
	committed := make(chan struct{})
	releaseResponse := make(chan struct{})
	source := newAuthHookSource(this.source, nil, nil)
	var stopAtFirstCommit bool
	source.afterCommit = func() {
		if !stopAtFirstCommit {
			stopAtFirstCommit = true
			close(committed)
			<-releaseResponse
		}
	}
	runtime, mounted, signUp, registrar := authSignUpRuntime(t, source, strategy.build())

	signedUp := make(chan authLoginResult, 1)
	go func() {
		response, err := signUp.Execute(t.Context(), authSignUpForm{
			Identifier: identifier, Password: oldPassword,
		}, access.Agent{UserAgent: "signup-commit-barrier"})
		signedUp <- authLoginResult{response: response, err: err}
	}()
	awaitAuthSignal(t, committed, "signup database commit before response")
	ref := access.SubjectRef{Type: authSubject, ID: registrar.created}

	var closed int64
	var err error
	switch invalidation {
	case "reset":
		closed, err = runtime.SetPassword().Execute(t.Context(), access.SetPasswordCommand{
			Subject: ref, Password: newPassword,
		})
	case "logout-all":
		ctx := auth.WithPrincipal(t.Context(), &access.Principal{Ref: ref})
		var response access.LogoutResponse
		response, err = mounted.Endpoints().SignOutAll(ctx, true)
		closed = response.Revoked
	default:
		close(releaseResponse)
		t.Fatalf("unknown signup invalidation %q", invalidation)
	}
	if err != nil || closed != 1 {
		close(releaseResponse)
		t.Fatalf("%s after signup commit closed=%d err=%v, want published session", invalidation, closed, err)
	}
	close(releaseResponse)
	result := awaitAuthResult(t, signedUp, "signup response after invalidation")
	if result.err != nil || result.response.Token == "" {
		t.Fatalf("signup response=%+v err=%v", result.response, result.err)
	}
	assertAuthSignUpRows(t, runtime, registrar, 1, 1, 1)
	assertAllAuthSessionsRevoked(t, runtime.Store(), ref, 1)
	if _, err := mounted.Authenticator().Authenticate(t.Context(), auth.Credential{
		Scheme: auth.SchemeBearer, Token: result.response.Token,
	}); err == nil {
		t.Fatalf("%s missed the session published atomically with signup credential", invalidation)
	}
}

func authSignUpRuntime(
	t *testing.T,
	source crud.Source,
	strategy access.Strategy,
) (*access.Runtime, *access.MountedSubject, *access.SignUpUseCase[authSignUpForm], *authSignUpRegistrar) {
	t.Helper()
	runtime, err := access.New(access.RuntimeSpec{
		Source: source,
		Hasher: authPlainHasher{},
		Config: access.Config{Session: access.SessionConfig{
			TTL: time.Hour, IdleTTL: time.Hour, TouchInterval: time.Minute,
		}},
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	registrar := &authSignUpRegistrar{accounts: authSignUpAccounts.Bind(source)}
	directory := (authTestDirectory{profile: access.Profile{Identifier: identifier}}).withID()
	mounted, signUp, err := access.Mount(runtime, access.SubjectSpec[authSignUpForm]{
		Type: authSubject, Directory: directory, Strategy: strategy, Registrar: registrar,
	})
	if err != nil {
		t.Fatal(err)
	}
	if signUp == nil {
		t.Fatal("mounted registrar produced no signup use case")
	}
	return runtime, mounted, signUp, registrar
}

func assertAuthSignUpRows(
	t *testing.T,
	runtime *access.Runtime,
	registrar *authSignUpRegistrar,
	wantAccounts, wantCredentials, wantSessions int,
) {
	t.Helper()
	accounts, err := registrar.accounts.GetAll(t.Context())
	if err != nil {
		t.Fatalf("reading signup accounts: %v", err)
	}
	credentials, err := runtime.Store().Credentials.GetAll(t.Context())
	if err != nil {
		t.Fatalf("reading signup credentials: %v", err)
	}
	sessions, err := runtime.Store().Sessions.GetAll(t.Context())
	if err != nil {
		t.Fatalf("reading signup sessions: %v", err)
	}
	if len(accounts) != wantAccounts || len(credentials) != wantCredentials || len(sessions) != wantSessions {
		t.Fatalf("signup rows accounts/credentials/sessions=%d/%d/%d, want %d/%d/%d",
			len(accounts), len(credentials), len(sessions), wantAccounts, wantCredentials, wantSessions)
	}
}

func authSubjectRoleRead(query string) bool {
	folded := strings.ToLower(strings.TrimSpace(query))
	return strings.HasPrefix(folded, "select") && strings.Contains(folded, "subject_roles")
}

var _ access.Registrar[authSignUpForm] = (*authSignUpRegistrar)(nil)
