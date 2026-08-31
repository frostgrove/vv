//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/auth/access/accessjwt"
	"github.com/frostgrove/vv/auth/authjwt"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/errs"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestAccessPasswordSessionSerialization(t *testing.T) {
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
					t.Run("login linearizes before reset", func(t *testing.T) {
						target.loginBeforeInvalidation(t, strategy, "reset")
					})
					t.Run("login linearizes before change-password", func(t *testing.T) {
						target.loginBeforeInvalidation(t, strategy, "change-password")
					})
					t.Run("login linearizes before logout-all", func(t *testing.T) {
						target.loginBeforeInvalidation(t, strategy, "logout-all")
					})
					t.Run("reset linearizes before login", func(t *testing.T) {
						target.resetBeforeLogin(t, strategy)
					})
					t.Run("issuer failure rolls its session back", func(t *testing.T) {
						target.issuerFailureRollsBack(t, strategy)
					})
					t.Run("ambient rollback cannot undo returned login or logout", func(t *testing.T) {
						target.ambientRollbackCannotUndoSecurityBoundary(t, strategy)
					})
					t.Run("high-count primary-key lock order", func(t *testing.T) {
						target.highCountLoginBeforeLogoutAll(t, strategy, 64)
					})
					if target.postgres {
						for _, isolation := range []struct {
							name  string
							level sql.IsolationLevel
						}{
							{name: "repeatable-read snapshot invalidation fails closed", level: sql.LevelRepeatableRead},
							{name: "serializable snapshot invalidation fails closed", level: sql.LevelSerializable},
						} {
							t.Run(isolation.name, func(t *testing.T) {
								target.postgresSnapshotInvalidationFailsClosed(t, strategy, isolation.level)
							})
						}
					}
				})
			}
			t.Run("credential creation after discovery linearizes after invalidation", func(t *testing.T) {
				target.credentialCreationAfterDiscovery(t)
			})
		})
	}
}

type authSerializationTarget struct {
	name     string
	database *sql.DB
	source   crud.Source
	postgres bool
}

type authStrategyFixture struct {
	name  string
	build func() access.Strategy
}

const (
	authSubject access.SubjectType = "auth_serial_user"
	oldPassword                    = "password-before-reset"
	newPassword                    = "password-after-reset"
	identifier                     = "serial@example.test"
)

func (this authSerializationTarget) loginBeforeInvalidation(
	t *testing.T,
	strategy authStrategyFixture,
	invalidation string,
) {
	t.Helper()
	this.clear(t)

	locks := make(chan string, 16)
	verifyEntered := make(chan struct{})
	releaseLogin := make(chan struct{})
	source := newAuthHookSource(this.source, locks, nil)
	hasher := &authBarrierHasher{entered: verifyEntered, release: releaseLogin}
	runtime, mounted, ref := authRuntime(t, source, hasher, strategy.build())
	seedAuthCredential(t, runtime.Store(), ref, oldPassword)

	loginDone := make(chan authLoginResult, 1)
	go func() {
		response, err := mounted.Endpoints().SignIn(t.Context(), access.SignInRequest{
			Email: identifier, Password: oldPassword,
		}, access.Agent{UserAgent: "barrier-login"})
		loginDone <- authLoginResult{response: response, err: err}
	}()

	awaitAuthSignal(t, locks, "login credential lock")
	awaitAuthSignal(t, verifyEntered, "login verification after lock")

	invalidated := make(chan authCountResult, 1)
	switch invalidation {
	case "reset":
		go func() {
			count, err := runtime.SetPassword().Execute(t.Context(), access.SetPasswordCommand{
				Subject: ref, Password: newPassword,
			})
			invalidated <- authCountResult{count: count, err: err}
		}()
	case "change-password":
		go func() {
			ctx := auth.WithPrincipal(t.Context(), &access.Principal{Ref: ref})
			response, err := mounted.Endpoints().ChangeSecret(ctx, access.ChangeSecretRequest{
				Current: oldPassword, New: newPassword, RevokeOthers: true,
			})
			invalidated <- authCountResult{count: response.Revoked, err: err}
		}()
	case "logout-all":
		go func() {
			ctx := auth.WithPrincipal(t.Context(), &access.Principal{Ref: ref})
			response, err := mounted.Endpoints().SignOutAll(ctx, true)
			invalidated <- authCountResult{count: response.Revoked, err: err}
		}()
	default:
		t.Fatalf("unknown invalidation %q", invalidation)
	}

	awaitAuthSignal(t, locks, invalidation+" credential lock attempt")
	select {
	case result := <-invalidated:
		close(releaseLogin)
		t.Fatalf("%s completed before login released its credential lock: %+v", invalidation, result)
	default:
	}
	close(releaseLogin)

	login := awaitAuthResult(t, loginDone, "login completion")
	if login.err != nil || login.response.Token == "" {
		t.Fatalf("login response=%+v err=%v", login.response, login.err)
	}
	closed := awaitAuthResult(t, invalidated, invalidation+" completion")
	if closed.err != nil || closed.count != 1 {
		t.Fatalf("%s closed=%d err=%v, want the just-issued session", invalidation, closed.count, closed.err)
	}

	assertAllAuthSessionsRevoked(t, runtime.Store(), ref, 1)
	if _, err := mounted.Authenticator().Authenticate(t.Context(), auth.Credential{
		Scheme: auth.SchemeBearer, Token: login.response.Token,
	}); err == nil {
		t.Fatalf("%s left the credential issued from the old password usable", invalidation)
	}
}

func (this authSerializationTarget) postgresSnapshotInvalidationFailsClosed(
	t *testing.T,
	strategy authStrategyFixture,
	isolation sql.IsolationLevel,
) {
	t.Helper()
	this.clear(t)

	locks := make(chan string, 16)
	verifyEntered := make(chan struct{})
	releaseLogin := make(chan struct{})
	isolated := crudsql.Postgres(this.database).WithTxOptions(&sql.TxOptions{Isolation: isolation})
	source := newAuthHookSource(isolated, locks, nil)
	hasher := &authBarrierHasher{entered: verifyEntered, release: releaseLogin}
	runtime, mounted, ref := authRuntime(t, source, hasher, strategy.build())
	seedAuthCredential(t, runtime.Store(), ref, oldPassword)

	loginDone := make(chan authLoginResult, 1)
	go func() {
		response, err := mounted.Endpoints().SignIn(t.Context(), access.SignInRequest{
			Email: identifier, Password: oldPassword,
		}, access.Agent{UserAgent: "snapshot-fence-login"})
		loginDone <- authLoginResult{response: response, err: err}
	}()
	awaitAuthSignal(t, locks, "snapshot-fence login credential lock")
	awaitAuthSignal(t, verifyEntered, "snapshot-fence login verification")

	invalidated := make(chan authCountResult, 1)
	go func() {
		count, err := runtime.SetPassword().Execute(t.Context(), access.SetPasswordCommand{
			Subject: ref, Password: newPassword,
		})
		invalidated <- authCountResult{count: count, err: err}
	}()
	awaitAuthSignal(t, locks, "old-snapshot invalidation credential lock attempt")
	select {
	case result := <-invalidated:
		close(releaseLogin)
		t.Fatalf("snapshot invalidation completed before login released its credential: %+v", result)
	default:
	}
	close(releaseLogin)

	login := awaitAuthResult(t, loginDone, "snapshot-fence login commit")
	if login.err != nil || login.response.Token == "" {
		t.Fatalf("login response=%+v err=%v", login.response, login.err)
	}
	result := awaitAuthResult(t, invalidated, "old-snapshot invalidation refusal")
	fault, ok := errs.AsFault(result.err)
	if !ok || fault.Kind != errs.KindRetryable || fault.Code != errs.CodeSerializationFailure {
		t.Fatalf("old-snapshot invalidation count=%d error=%v, want retryable %q",
			result.count, result.err, errs.CodeSerializationFailure)
	}

	sessions, err := runtime.Store().Sessions.GetAll(t.Context(), access.OfSubject(ref))
	if err != nil || len(sessions) != 1 || sessions[0].RevokedAt != nil {
		t.Fatalf("post-40001 sessions=%+v err=%v, want one live committed login", sessions, err)
	}
	if _, err := mounted.Authenticator().Authenticate(t.Context(), auth.Credential{
		Scheme: auth.SchemeBearer, Token: login.response.Token,
	}); err != nil {
		t.Fatalf("rolled-back invalidation poisoned the committed session: %v", err)
	}
	revoked, err := runtime.SetPassword().Execute(t.Context(), access.SetPasswordCommand{
		Subject: ref, Password: newPassword,
	})
	if err != nil || revoked != 1 {
		t.Fatalf("fresh invalidation retry revoked=%d err=%v, want 1", revoked, err)
	}
	assertAllAuthSessionsRevoked(t, runtime.Store(), ref, 1)
}

func (this authSerializationTarget) resetBeforeLogin(t *testing.T, strategy authStrategyFixture) {
	t.Helper()
	this.clear(t)

	locks := make(chan string, 16)
	releaseReset := make(chan struct{})
	resetLocked := make(chan struct{})
	source := newAuthHookSource(this.source, locks, nil)
	var holdReset sync.Once
	source.afterQuery = func(query string) {
		if authCredentialLockQuery(query) {
			holdReset.Do(func() {
				close(resetLocked)
				<-releaseReset
			})
		}
	}
	runtime, mounted, ref := authRuntime(t, source, authPlainHasher{}, strategy.build())
	seedAuthCredential(t, runtime.Store(), ref, oldPassword)

	resetDone := make(chan error, 1)
	go func() {
		_, err := runtime.SetPassword().Execute(t.Context(), access.SetPasswordCommand{
			Subject: ref, Password: newPassword,
		})
		resetDone <- err
	}()

	awaitAuthSignal(t, locks, "reset canonical credential lock")
	awaitAuthSignal(t, resetLocked, "reset holding credential lock")

	loginDone := make(chan authLoginResult, 1)
	go func() {
		response, err := mounted.Endpoints().SignIn(t.Context(), access.SignInRequest{
			Email: identifier, Password: oldPassword,
		}, access.Agent{})
		loginDone <- authLoginResult{response: response, err: err}
	}()

	awaitAuthSignal(t, locks, "login current-read lock attempt")
	select {
	case result := <-loginDone:
		close(releaseReset)
		t.Fatalf("login completed before reset released its credential lock: %+v", result)
	default:
	}
	close(releaseReset)

	if err := awaitAuthResult(t, resetDone, "reset commit"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	login := awaitAuthResult(t, loginDone, "stale login refusal")
	fault, ok := errs.AsFault(login.err)
	if !ok || fault.Code != access.CodeBadCredentials {
		t.Fatalf("stale login error=%v, want typed %q fault", login.err, access.CodeBadCredentials)
	}
	if login.response.Token != "" {
		t.Fatalf("reset-first login returned a credential: %+v", login.response)
	}
	assertAllAuthSessionsRevoked(t, runtime.Store(), ref, 0)
}

func (this authSerializationTarget) issuerFailureRollsBack(t *testing.T, strategy authStrategyFixture) {
	t.Helper()
	this.clear(t)

	issuerFailure := errors.New("integration grant lookup failed after session insert")
	fail := func(query string) error {
		folded := strings.ToLower(query)
		if strings.Contains(folded, "subject_roles") && strings.HasPrefix(strings.TrimSpace(folded), "select") {
			return issuerFailure
		}
		return nil
	}
	source := newAuthHookSource(this.source, nil, fail)
	runtime, mounted, ref := authRuntime(t, source, authPlainHasher{}, strategy.build())
	seedAuthCredential(t, runtime.Store(), ref, oldPassword)

	response, err := mounted.Endpoints().SignIn(t.Context(), access.SignInRequest{
		Email: identifier, Password: oldPassword,
	}, access.Agent{})
	if !errors.Is(err, issuerFailure) {
		t.Fatalf("issuer error=%v, want original typed failure", err)
	}
	if response.Token != "" || response.Refresh != "" {
		t.Fatalf("failed issuer leaked credential response %+v", response)
	}
	assertAllAuthSessionsRevoked(t, runtime.Store(), ref, 0)
}

func (this authSerializationTarget) ambientRollbackCannotUndoSecurityBoundary(
	t *testing.T,
	strategy authStrategyFixture,
) {
	t.Helper()
	this.clear(t)

	source := newAuthHookSource(this.source, nil, nil)
	runtime, mounted, ref := authRuntime(t, source, authPlainHasher{}, strategy.build())
	seedAuthCredential(t, runtime.Store(), ref, oldPassword)

	loginOuter := beginAuthOuter(t, source)
	loginCtx := crud.BindExecutor(t.Context(), source, loginOuter)
	response, err := mounted.Endpoints().SignIn(loginCtx, access.SignInRequest{
		Email: identifier, Password: oldPassword,
	}, access.Agent{UserAgent: "ambient-owner"})
	if err != nil || response.Token == "" {
		t.Fatalf("login response=%+v err=%v", response, err)
	}
	if err := loginOuter.Rollback(t.Context()); err != nil {
		t.Fatalf("rolling back ambient login owner: %v", err)
	}
	if _, err := mounted.Authenticator().Authenticate(t.Context(), auth.Credential{
		Scheme: auth.SchemeBearer, Token: response.Token,
	}); err != nil {
		t.Fatalf("ambient rollback undid a token returned after access commit: %v", err)
	}

	logoutOuter := beginAuthOuter(t, source)
	logoutCtx := crud.BindExecutor(t.Context(), source, logoutOuter)
	logoutCtx = auth.WithPrincipal(logoutCtx, &access.Principal{Ref: ref})
	closed, err := mounted.Endpoints().SignOutAll(logoutCtx, true)
	if err != nil || closed.Revoked != 1 {
		t.Fatalf("logout-all revoked=%d err=%v", closed.Revoked, err)
	}
	if err := logoutOuter.Rollback(t.Context()); err != nil {
		t.Fatalf("rolling back ambient logout owner: %v", err)
	}
	assertAllAuthSessionsRevoked(t, runtime.Store(), ref, 1)
	if _, err := mounted.Authenticator().Authenticate(t.Context(), auth.Credential{
		Scheme: auth.SchemeBearer, Token: response.Token,
	}); err == nil {
		t.Fatal("ambient rollback undid committed revocation or its JWT sink announcement")
	}
}

func (this authSerializationTarget) highCountLoginBeforeLogoutAll(
	t *testing.T,
	strategy authStrategyFixture,
	total int,
) {
	t.Helper()
	this.clear(t)

	locks := make(chan string, 2*total+8)
	verifyEntered := make(chan struct{})
	releaseLogin := make(chan struct{})
	source := newAuthHookSource(this.source, locks, nil)
	hasher := &authBarrierHasher{entered: verifyEntered, release: releaseLogin}
	runtime, mounted, ref := authRuntime(t, source, hasher, strategy.build())
	seedManyAuthCredentials(t, runtime.Store(), ref, oldPassword, total)

	loginDone := make(chan authLoginResult, 1)
	go func() {
		response, err := mounted.Endpoints().SignIn(t.Context(), access.SignInRequest{
			Email: identifier, Password: oldPassword,
		}, access.Agent{UserAgent: "high-count-login"})
		loginDone <- authLoginResult{response: response, err: err}
	}()
	awaitAuthSignal(t, verifyEntered, "high-count login verification after every PK lock")
	for i := 0; i < total; i++ {
		query := awaitAuthSignal(t, locks, "high-count login PK lock")
		if !authCredentialLockQuery(query) {
			t.Fatalf("login lock %d is not an exact credential lock: %s", i, query)
		}
	}

	invalidated := make(chan authCountResult, 1)
	go func() {
		ctx := auth.WithPrincipal(t.Context(), &access.Principal{Ref: ref})
		response, err := mounted.Endpoints().SignOutAll(ctx, true)
		invalidated <- authCountResult{count: response.Revoked, err: err}
	}()
	query := awaitAuthSignal(t, locks, "high-count logout first PK lock attempt")
	if !authCredentialLockQuery(query) {
		t.Fatalf("logout lock is not exact credential PK: %s", query)
	}
	select {
	case result := <-invalidated:
		close(releaseLogin)
		t.Fatalf("high-count logout completed before login released locks: %+v", result)
	default:
	}
	close(releaseLogin)

	login := awaitAuthResult(t, loginDone, "high-count login completion")
	if login.err != nil || login.response.Token == "" {
		t.Fatalf("high-count login response=%+v err=%v", login.response, login.err)
	}
	closed := awaitAuthResult(t, invalidated, "high-count logout completion")
	if closed.err != nil || closed.count != 1 {
		t.Fatalf("high-count logout closed=%d err=%v, want 1 and no deadlock", closed.count, closed.err)
	}
	assertAllAuthSessionsRevoked(t, runtime.Store(), ref, 1)
}

func (this authSerializationTarget) credentialCreationAfterDiscovery(t *testing.T) {
	t.Helper()
	this.clear(t)

	discovered := make(chan struct{})
	releaseInvalidation := make(chan struct{})
	source := newAuthHookSource(this.source, nil, nil)
	var pause sync.Once
	source.beforeQuery = func(query string) {
		if authCredentialLockQuery(query) {
			pause.Do(func() {
				close(discovered)
				<-releaseInvalidation
			})
		}
	}
	runtime, mounted, ref := authRuntime(t, source, authPlainHasher{}, access.OpaqueToken())
	seedAuthCredential(t, runtime.Store(), ref, oldPassword)

	invalidated := make(chan authCountResult, 1)
	go func() {
		ctx := auth.WithPrincipal(t.Context(), &access.Principal{Ref: ref})
		response, err := mounted.Endpoints().SignOutAll(ctx, true)
		invalidated <- authCountResult{count: response.Revoked, err: err}
	}()
	awaitAuthSignal(t, discovered, "credential ID discovery before exact lock")

	createdID := uuid.New()
	createdIdentifier := "created+" + createdID.String() + "@example.test"
	if err := runtime.Store().Credentials.SaveOnly(t.Context(), &access.Credential{
		ID:          createdID,
		SubjectType: string(ref.Type),
		SubjectID:   ref.ID,
		Provider:    access.ProviderPassword,
		Identifier:  createdIdentifier,
		SecretHash:  "hashed:" + oldPassword,
	}); err != nil {
		close(releaseInvalidation)
		t.Fatalf("creating a credential after lock-set discovery: %v", err)
	}
	close(releaseInvalidation)
	result := awaitAuthResult(t, invalidated, "pre-creation-linearized invalidation")
	if result.err != nil || result.count != 0 {
		t.Fatalf("invalidation count=%d err=%v", result.count, result.err)
	}

	response, err := mounted.Endpoints().SignIn(t.Context(), access.SignInRequest{
		Email: createdIdentifier, Password: oldPassword,
	}, access.Agent{})
	if err != nil || response.Token == "" {
		t.Fatalf("post-discovery credential login response=%+v err=%v", response, err)
	}
}

func beginAuthOuter(t *testing.T, source crud.Source) crud.Tx {
	t.Helper()
	beginner, ok := crud.BeginnerOf(source)
	if !ok {
		t.Fatal("auth integration source cannot begin an ambient transaction")
	}
	tx, err := beginner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func authRuntime(
	t *testing.T,
	source crud.Source,
	hasher access.Hasher,
	strategy access.Strategy,
) (*access.Runtime, *access.MountedSubject, access.SubjectRef) {
	t.Helper()
	runtime, err := access.New(access.RuntimeSpec{
		Source: source,
		Hasher: hasher,
		Config: access.Config{Session: access.SessionConfig{
			TTL: time.Hour, IdleTTL: time.Hour, TouchInterval: time.Minute,
		}},
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := (authTestDirectory{profile: access.Profile{Identifier: identifier}}).withID()
	mounted, _, err := access.Mount(runtime, access.SubjectSpec[struct{}]{
		Type: authSubject, Directory: directory, Strategy: strategy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime, mounted, access.SubjectRef{Type: authSubject, ID: directory.id}
}

func seedAuthCredential(t *testing.T, store *access.Store, ref access.SubjectRef, password string) {
	t.Helper()
	if err := store.Credentials.SaveOnly(t.Context(), &access.Credential{
		ID:          uuid.New(),
		SubjectType: string(ref.Type),
		SubjectID:   ref.ID,
		Provider:    access.ProviderPassword,
		Identifier:  identifier,
		SecretHash:  "hashed:" + password,
	}); err != nil {
		t.Fatalf("seeding credential: %v", err)
	}
}

func seedManyAuthCredentials(
	t *testing.T,
	store *access.Store,
	ref access.SubjectRef,
	password string,
	total int,
) {
	t.Helper()
	if total < 1 {
		t.Fatal("high-count credential fixture needs at least one row")
	}
	seedAuthCredential(t, store, ref, password)
	for i := 1; i < total; i++ {
		id := uuid.New()
		if err := store.Credentials.SaveOnly(t.Context(), &access.Credential{
			ID:          id,
			SubjectType: string(ref.Type),
			SubjectID:   ref.ID,
			Provider:    access.ProviderPassword,
			Identifier:  "serial+" + id.String() + "@example.test",
			SecretHash:  "hashed:" + password,
		}); err != nil {
			t.Fatalf("seeding high-count credential %d/%d: %v", i+1, total, err)
		}
	}
}

func assertAllAuthSessionsRevoked(t *testing.T, store *access.Store, ref access.SubjectRef, want int) {
	t.Helper()
	sessions, err := store.Sessions.GetAll(t.Context(), access.OfSubject(ref))
	if err != nil {
		t.Fatalf("reading sessions: %v", err)
	}
	if len(sessions) != want {
		t.Fatalf("session rows=%d, want %d: %+v", len(sessions), want, sessions)
	}
	for _, session := range sessions {
		if session.RevokedAt == nil {
			t.Fatalf("session %s survived invalidation: %+v", session.ID, session)
		}
	}
}

type authTestDirectory struct {
	id      uuid.UUID
	profile access.Profile
}

func (this authTestDirectory) SubjectType() access.SubjectType { return authSubject }
func (this authTestDirectory) Active(context.Context, uuid.UUID) (bool, error) {
	return true, nil
}
func (this authTestDirectory) Describe(context.Context, uuid.UUID) (access.Profile, error) {
	return this.profile, nil
}
func (authTestDirectory) Touch(context.Context, uuid.UUID) error { return nil }

func (this authTestDirectory) withID() authTestDirectory {
	if this.id == uuid.Nil {
		this.id = uuid.New()
	}
	return this
}

type authPlainHasher struct{}

func (authPlainHasher) Hash(password string) (string, error) { return "hashed:" + password, nil }
func (authPlainHasher) Verify(password, encoded string) (bool, error) {
	return encoded == "hashed:"+password, nil
}

type authBarrierHasher struct {
	once    sync.Once
	entered chan struct{}
	release <-chan struct{}
}

func (*authBarrierHasher) Hash(password string) (string, error) {
	return "hashed:" + password, nil
}
func (this *authBarrierHasher) Verify(password, encoded string) (bool, error) {
	this.once.Do(func() {
		close(this.entered)
		<-this.release
	})
	return encoded == "hashed:"+password, nil
}

type authMemoryRevocations struct {
	mu      sync.Mutex
	entries map[uuid.UUID]time.Time
}

func (this *authMemoryRevocations) Revoked(_ context.Context, session uuid.UUID) (bool, error) {
	this.mu.Lock()
	defer this.mu.Unlock()
	_, found := this.entries[session]
	return found, nil
}

func (this *authMemoryRevocations) Revoke(_ context.Context, session uuid.UUID, until time.Time) error {
	this.mu.Lock()
	defer this.mu.Unlock()
	if this.entries == nil {
		this.entries = make(map[uuid.UUID]time.Time)
	}
	this.entries[session] = until
	return nil
}

func newAuthJWTStrategy() access.Strategy {
	secret := []byte("0123456789abcdef0123456789abcdef")
	return accessjwt.Strategy(accessjwt.Spec{
		Method:     jwt.SigningMethodHS256,
		Key:        secret,
		Verify:     authjwt.HMAC256(secret),
		Issuer:     "auth-serialization-integration",
		AccessTTL:  5 * time.Minute,
		RefreshTTL: time.Hour,
		Revocation: &authMemoryRevocations{},
	})
}

type authLoginResult struct {
	response access.AuthResponse
	err      error
}

type authCountResult struct {
	count int64
	err   error
}

func awaitAuthSignal[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(10 * time.Second):
		var zero T
		t.Fatalf("timed out waiting for %s", what)
		return zero
	}
}

func awaitAuthResult[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	return awaitAuthSignal(t, ch, what)
}

type authHookSource struct {
	inner       crud.Source
	locks       chan<- string
	fail        func(string) error
	beforeQuery func(string)
	afterQuery  func(string)
	afterCommit func()
	commitErr   error
}

func newAuthHookSource(inner crud.Source, locks chan<- string, fail func(string) error) *authHookSource {
	return &authHookSource{inner: inner, locks: locks, fail: fail}
}

func (this *authHookSource) Dialect() crud.Dialect { return this.inner.Dialect() }
func (this *authHookSource) DataSource() any       { return crud.KeyOf(this.inner) }

func (this *authHookSource) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	if err := this.before(query); err != nil {
		return crud.Result{}, err
	}
	return this.inner.Exec(ctx, query, args...)
}

func (this *authHookSource) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	if err := this.before(query); err != nil {
		return nil, err
	}
	rows, err := this.inner.Query(ctx, query, args...)
	if err == nil && this.afterQuery != nil {
		this.afterQuery(query)
	}
	return rows, err
}

func (this *authHookSource) Begin(ctx context.Context) (crud.Tx, error) {
	beginner, ok := crud.BeginnerOf(this.inner)
	if !ok {
		return nil, crud.ErrNoTxSupport
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &authHookTx{Tx: tx, source: this}, nil
}

func (this *authHookSource) before(query string) error {
	if this.fail != nil {
		if err := this.fail(query); err != nil {
			return err
		}
	}
	if this.locks != nil && authCredentialLockQuery(query) {
		this.locks <- query
	}
	if this.beforeQuery != nil {
		this.beforeQuery(query)
	}
	return nil
}

type authHookTx struct {
	crud.Tx
	source *authHookSource
}

func (this *authHookTx) DataSource() any { return this.source.DataSource() }
func (this *authHookTx) Commit(ctx context.Context) error {
	if this.source.commitErr != nil {
		if err := this.Tx.Rollback(ctx); err != nil {
			return errors.Join(this.source.commitErr, err)
		}
		return this.source.commitErr
	}
	if err := this.Tx.Commit(ctx); err != nil {
		return err
	}
	if this.source.afterCommit != nil {
		this.source.afterCommit()
	}
	return nil
}
func (this *authHookTx) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	if err := this.source.before(query); err != nil {
		return crud.Result{}, err
	}
	return this.Tx.Exec(ctx, query, args...)
}
func (this *authHookTx) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	if err := this.source.before(query); err != nil {
		return nil, err
	}
	rows, err := this.Tx.Query(ctx, query, args...)
	if err == nil && this.source.afterQuery != nil {
		this.source.afterQuery(query)
	}
	return rows, err
}

func authCredentialLockQuery(query string) bool {
	folded := strings.ToLower(query)
	if !strings.Contains(folded, "credentials") || !strings.Contains(folded, "for update") {
		return false
	}
	where := strings.Index(folded, " where ")
	if where < 0 {
		return false
	}
	predicate := folded[where:]
	return (strings.Contains(predicate, `"id" =`) || strings.Contains(predicate, "`id` =")) &&
		!strings.Contains(predicate, "subject_id") && !strings.Contains(predicate, "provider")
}

func (this authSerializationTarget) install(t *testing.T) {
	t.Helper()
	this.drop(t)
	statements := authSchemaMySQL
	if this.postgres {
		statements = authSchemaPostgres
	}
	for _, statement := range statements {
		if _, err := this.database.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("%s auth schema statement %q: %v", this.name, statement, err)
		}
	}
}

func (this authSerializationTarget) clear(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"subject_permissions", "subject_roles", "sessions", "credentials",
		"subject_default_roles", "auth_signup_accounts",
	} {
		if _, err := this.database.ExecContext(t.Context(), "DELETE FROM "+table); err != nil {
			t.Fatalf("%s clearing %s: %v", this.name, table, err)
		}
	}
}

func (this authSerializationTarget) drop(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, table := range []string{
		"subject_permissions", "subject_roles", "sessions", "credentials",
		"subject_default_roles", "auth_signup_accounts",
	} {
		if _, err := this.database.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("%s dropping %s: %v", this.name, table, err)
		}
	}
}

var authSchemaPostgres = []string{
	`CREATE TABLE auth_signup_accounts (
        id UUID PRIMARY KEY,
        identifier TEXT NOT NULL UNIQUE
    )`,
	`CREATE TABLE subject_default_roles (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
        subject_type TEXT NOT NULL UNIQUE,
        role_id UUID NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
    )`,
	`CREATE TABLE credentials (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
        subject_type TEXT NOT NULL,
        subject_id UUID NOT NULL,
        provider TEXT NOT NULL,
        identifier TEXT NOT NULL,
        secret_hash TEXT NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
    )`,
	`CREATE UNIQUE INDEX uq_auth_serial_credentials_identifier
        ON credentials (subject_type, provider, identifier)`,
	`CREATE INDEX ix_auth_serial_credentials_subject
        ON credentials (subject_type, subject_id)`,
	`CREATE TABLE sessions (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
        subject_type TEXT NOT NULL,
        subject_id UUID NOT NULL,
        token_hash TEXT NOT NULL UNIQUE,
        previous_token_hash TEXT NOT NULL DEFAULT '',
        user_agent TEXT NOT NULL DEFAULT '',
        ip TEXT NOT NULL DEFAULT '',
        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        last_used_at TIMESTAMPTZ NOT NULL,
        rotated_at TIMESTAMPTZ,
        expires_at TIMESTAMPTZ NOT NULL,
        revoked_at TIMESTAMPTZ,
        revoked_reason TEXT NOT NULL DEFAULT ''
    )`,
	`CREATE TABLE subject_roles (
        id UUID PRIMARY KEY,
        subject_type TEXT NOT NULL,
        subject_id UUID NOT NULL,
        role_id UUID NOT NULL,
        granted_at TIMESTAMPTZ NOT NULL DEFAULT now()
    )`,
	`CREATE TABLE subject_permissions (
        id UUID PRIMARY KEY,
        subject_type TEXT NOT NULL,
        subject_id UUID NOT NULL,
        permission_id UUID NOT NULL,
        granted_at TIMESTAMPTZ NOT NULL DEFAULT now()
    )`,
}

var authSchemaMySQL = []string{
	`CREATE TABLE auth_signup_accounts (
        id CHAR(36) PRIMARY KEY,
        identifier VARCHAR(255) NOT NULL UNIQUE
    ) ENGINE=InnoDB`,
	`CREATE TABLE subject_default_roles (
        id CHAR(36) PRIMARY KEY DEFAULT (UUID()),
        subject_type VARCHAR(64) NOT NULL UNIQUE,
        role_id CHAR(36) NOT NULL,
        created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
        updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
    ) ENGINE=InnoDB`,
	`CREATE TABLE credentials (
        id CHAR(36) PRIMARY KEY DEFAULT (UUID()),
        subject_type VARCHAR(64) NOT NULL,
        subject_id CHAR(36) NOT NULL,
        provider VARCHAR(64) NOT NULL,
        identifier VARCHAR(255) NOT NULL,
        secret_hash TEXT NOT NULL,
        created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
        updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
        UNIQUE KEY uq_auth_serial_credentials_identifier (subject_type, provider, identifier),
        KEY ix_auth_serial_credentials_subject (subject_type, subject_id)
    ) ENGINE=InnoDB`,
	`CREATE TABLE sessions (
        id CHAR(36) PRIMARY KEY DEFAULT (UUID()),
        subject_type VARCHAR(64) NOT NULL,
        subject_id CHAR(36) NOT NULL,
        token_hash VARCHAR(128) NOT NULL UNIQUE,
        previous_token_hash VARCHAR(128) NOT NULL DEFAULT '',
        user_agent VARCHAR(256) NOT NULL DEFAULT '',
        ip VARCHAR(64) NOT NULL DEFAULT '',
        created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
        last_used_at DATETIME(6) NOT NULL,
        rotated_at DATETIME(6) NULL,
        expires_at DATETIME(6) NOT NULL,
        revoked_at DATETIME(6) NULL,
        revoked_reason TEXT NOT NULL,
        KEY ix_auth_serial_sessions_subject (subject_type, subject_id, revoked_at)
    ) ENGINE=InnoDB`,
	`CREATE TABLE subject_roles (
        id CHAR(36) PRIMARY KEY,
        subject_type VARCHAR(64) NOT NULL,
        subject_id CHAR(36) NOT NULL,
        role_id CHAR(36) NOT NULL,
        granted_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
    ) ENGINE=InnoDB`,
	`CREATE TABLE subject_permissions (
        id CHAR(36) PRIMARY KEY,
        subject_type VARCHAR(64) NOT NULL,
        subject_id CHAR(36) NOT NULL,
        permission_id CHAR(36) NOT NULL,
        granted_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
    ) ENGINE=InnoDB`,
}

var (
	_ access.Directory         = authTestDirectory{}
	_ access.Hasher            = authPlainHasher{}
	_ access.Hasher            = (*authBarrierHasher)(nil)
	_ accessjwt.RevocationList = (*authMemoryRevocations)(nil)
	_ crud.Source              = (*authHookSource)(nil)
	_ crud.Beginner            = (*authHookSource)(nil)
	_ crud.Tx                  = (*authHookTx)(nil)
)
