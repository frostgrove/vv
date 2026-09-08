package access

import (
	"bytes"
	"context"
	"github.com/frostgrove/vv/auth"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/errs"
	"github.com/google/uuid"
)

type countingHasher struct {
	hashes   atomic.Int64
	verifies atomic.Int64
}

func (this *countingHasher) Hash(password string) (string, error) {
	this.hashes.Add(1)
	return "hashed:" + password, nil
}

func (this *countingHasher) Verify(password, encoded string) (bool, error) {
	this.verifies.Add(1)
	return encoded == "hashed:"+password, nil
}

type recordedAttempt struct {
	attempt Attempt
	outcome AttemptOutcome
}

type attemptLog struct {
	mu   sync.Mutex
	seen []recordedAttempt
}

func (this *attemptLog) AttemptObserved(_ context.Context, attempt Attempt, outcome AttemptOutcome) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.seen = append(this.seen, recordedAttempt{attempt: attempt, outcome: outcome})
}

func (this *attemptLog) outcomes() []AttemptOutcome {
	this.mu.Lock()
	defer this.mu.Unlock()
	out := make([]AttemptOutcome, 0, len(this.seen))
	for _, entry := range this.seen {
		out = append(out, entry.outcome)
	}
	return out
}

func protectedLogin(t *testing.T, source crud.Source, hasher Hasher, protection Protection) *LoginUseCase {
	t.Helper()
	store := NewStore(source)
	grants := NewGrants(store, MustDirectories(stubDirectory{active: true}))
	issuer := &serialIssuer{store: store, response: AuthResponse{Token: "issued"}}
	dependencies := newDeps(store, grants, hasher, Config{}, slog.New(slog.DiscardHandler), nil, protection)
	return NewLogin(dependencies).Issuing(issuer)
}

func missedLogin(recorder *crudtest.Recorder) {
	recorder.Push(crudtest.Rows(), crudtest.Rows(), crudtest.Rows())
}

func fixedPolicy(moment time.Time, perIdentifier int) AttemptPolicy {
	return AttemptPolicy{
		MaxPerIdentifier: perIdentifier,
		MaxPerIP:         1000,
		Window:           time.Hour,
		LockFor:          time.Hour,
		Now:              func() time.Time { return moment },
	}
}

func TestSignInsStopReachingTheDatabaseOnceTheAttemptCeilingIsReached(t *testing.T) {
	moment := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	recorder := crudtest.Postgres()
	watcher := &attemptLog{}
	login := protectedLogin(t, recorder, &countingHasher{}, Protection{
		Limiter:  NewMemoryLimiter(fixedPolicy(moment, 2)),
		Observer: watcher,
	})
	command := LoginCommand{
		Subject: testSubject, Identifier: "ann@example.test", Password: "a guess",
		Agent: Agent{IP: "203.0.113.7"},
	}

	for guess := range 2 {
		missedLogin(recorder)
		if err := mustRefuse(t, login, command); !isBadCredentials(err) {
			t.Fatalf("guess %d was refused as %v, not as bad credentials", guess+1, err)
		}
	}
	spent := len(recorder.Statements())
	if spent == 0 {
		t.Fatal("the first two guesses never reached the database, so the check below proves nothing")
	}

	err := mustRefuse(t, login, command)
	fault, ok := errs.AsFault(err)
	if !ok || fault.Code != CodeTooManyAttempts {
		t.Fatalf("the third guess was refused as %v, want a %q fault", err, CodeTooManyAttempts)
	}
	if len(recorder.Statements()) != spent {
		t.Fatalf("the refused guess still cost %d statement(s)",
			len(recorder.Statements())-spent)
	}
	if got := watcher.outcomes(); len(got) != 3 || got[2] != AttemptRefused {
		t.Fatalf("the observer saw %v, want two failures and a refusal", got)
	}
}

func TestSigningInSuccessfullyGivesTheIdentifierItsBudgetBack(t *testing.T) {
	moment := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	ref := SubjectRef{Type: testSubject, ID: uuid.New()}
	credential := uuid.New()
	recorder := crudtest.Postgres()
	login := protectedLogin(t, recorder, &countingHasher{}, Protection{
		Limiter: NewMemoryLimiter(fixedPolicy(moment, 2)),
	})
	command := LoginCommand{
		Subject: testSubject, Identifier: "ann@example.test", Password: "the right one",
		Agent: Agent{IP: "203.0.113.7"},
	}

	missedLogin(recorder)
	guess := command
	guess.Password = "a guess"
	if err := mustRefuse(t, login, guess); !isBadCredentials(err) {
		t.Fatalf("a wrong password was refused as %v", err)
	}

	recorder.Push(
		crudtest.Rows(serialCredentialRow(credential, ref, command.Identifier, "hashed:the right one")),
		crudtest.Rows([]any{credential.String()}),
		crudtest.Rows(serialCredentialRow(credential, ref, command.Identifier, "hashed:the right one")),
	)
	if _, err := login.Execute(t.Context(), command); err != nil {
		t.Fatalf("the right password was refused: %v", err)
	}

	for guess := range 2 {
		missedLogin(recorder)
		wrong := command
		wrong.Password = "a guess"
		if err := mustRefuse(t, login, wrong); !isBadCredentials(err) {
			t.Fatalf("guess %d after the successful sign-in was refused as %v; the budget did not reset", guess+1, err)
		}
	}
}

func TestAnOverLongIdentifierOrPasswordCostsNoHashAndNoStatement(t *testing.T) {
	recorder := crudtest.Postgres()
	hasher := &countingHasher{}
	login := protectedLogin(t, recorder, hasher, Protection{})

	missedLogin(recorder)
	ordinary := LoginCommand{Subject: testSubject, Identifier: "ann@example.test", Password: "a guess"}
	if err := mustRefuse(t, login, ordinary); !isBadCredentials(err) {
		t.Fatalf("the control refusal is %v, not bad credentials", err)
	}
	if hasher.verifies.Load() == 0 || len(recorder.Statements()) == 0 {
		t.Fatal("an ordinary miss cost neither a hash nor a statement, so the checks below prove nothing")
	}
	spentHashes, spentStatements := hasher.verifies.Load(), len(recorder.Statements())

	for name, command := range map[string]LoginCommand{
		"an identifier past the ceiling": {
			Subject: testSubject, Identifier: strings.Repeat("a", DefaultMaxIdentifierLength+1), Password: "a guess",
		},
		"a password past the ceiling": {
			Subject: testSubject, Identifier: "ann@example.test",
			Password: strings.Repeat("p", DefaultMaxPasswordLength+1),
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := mustRefuse(t, login, command)
			if !isBadCredentials(err) {
				t.Fatalf("the refusal is %v; an over-long field must answer exactly as a wrong password does", err)
			}
			if hasher.verifies.Load() != spentHashes {
				t.Fatal("the over-long field was hashed")
			}
			if len(recorder.Statements()) != spentStatements {
				t.Fatalf("the over-long field reached the database: %v", recorder.SQL())
			}
		})
	}
}

func mustRefuse(t *testing.T, login *LoginUseCase, command LoginCommand) error {
	t.Helper()
	response, err := login.Execute(t.Context(), command)
	if err == nil {
		t.Fatalf("the sign-in succeeded: %+v", response)
	}
	return err
}

func TestAPasswordPastTheCeilingIsRefusedAsAFieldViolation(t *testing.T) {
	runtime := testRuntime(t)
	enrol := NewEnroll(newDeps(
		runtime.store, nil, runtime.hasher, runtime.config, runtime.logger, runtime.revocations, Protection{}))

	err := enrol.Execute(t.Context(), EnrollCommand{
		Subject:    SubjectRef{Type: testSubject, ID: uuid.New()},
		Identifier: "ann@example.test",
		Password:   strings.Repeat("p", DefaultMaxPasswordLength+1),
	})
	fault, ok := errs.AsFault(err)
	if !ok || len(fault.Violations) != 1 || fault.Violations[0].Code != errs.CodeTooLong {
		t.Fatalf("an unbounded password was refused as %v, want a too_long violation on a field", err)
	}
}

func TestAnIdentifierPastTheCeilingIsRefusedAsAFieldViolation(t *testing.T) {
	runtime := testRuntime(t)
	enrol := NewEnroll(newDeps(
		runtime.store, nil, runtime.hasher, runtime.config, runtime.logger, runtime.revocations, Protection{}))

	err := enrol.Execute(t.Context(), EnrollCommand{
		Subject:    SubjectRef{Type: testSubject, ID: uuid.New()},
		Identifier: strings.Repeat("a", DefaultMaxIdentifierLength+1) + "@example.test",
		Password:   "0123456789",
	})
	fault, ok := errs.AsFault(err)
	if !ok || len(fault.Violations) != 1 || fault.Violations[0].Code != errs.CodeTooLong {
		t.Fatalf("an unbounded identifier was refused as %v, want a too_long violation on a field", err)
	}
}

func TestTheBulkheadRefusesWhatItHasNoRoomToQueue(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	bulkhead := NewBulkhead(blockingHasher{once: &sync.Once{}, entered: entered, release: release}, 1, 0)

	done := make(chan error, 1)
	go func() {
		_, err := bulkhead.Hash("the first password")
		done <- err
	}()
	<-entered

	if _, err := bulkhead.Hash("the second password"); !isOverloaded(err) {
		t.Fatalf("a second hash past a one-permit bulkhead answered %v, want an overload refusal", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("the admitted hash failed: %v", err)
	}

	if _, err := bulkhead.Hash("the third password"); err != nil {
		t.Fatalf("the freed bulkhead refused a hash it had room for: %v", err)
	}
}

func TestTheBulkheadPassesEveryCallThroughWhileItHasRoom(t *testing.T) {
	inner := &countingHasher{}
	bulkhead := Bulkhead(inner)

	encoded, err := bulkhead.Hash("a password")
	if err != nil {
		t.Fatalf("the default bulkhead refused a single hash: %v", err)
	}
	ok, err := bulkhead.Verify("a password", encoded)
	if err != nil || !ok {
		t.Fatalf("verifying through the bulkhead: ok=%v err=%v", ok, err)
	}
	if inner.hashes.Load() != 1 || inner.verifies.Load() != 1 {
		t.Fatalf("the bulkhead swallowed work: %d hashes, %d verifies",
			inner.hashes.Load(), inner.verifies.Load())
	}
	if bulkhead.Unwrap() != Hasher(inner) {
		t.Fatal("the bulkhead does not name what it stands in front of")
	}
}

func isOverloaded(err error) bool {
	fault, ok := errs.AsFault(err)
	return ok && fault.Code == CodeOverloaded
}

type blockingHasher struct {
	once    *sync.Once
	entered chan struct{}
	release chan struct{}
}

func (this blockingHasher) Hash(password string) (string, error) {
	this.once.Do(func() { close(this.entered) })
	<-this.release
	return "hashed:" + password, nil
}

func (this blockingHasher) Verify(password, encoded string) (bool, error) {
	return encoded == "hashed:"+password, nil
}

func TestEnrollingASecondPasswordForOneAccountIsRefusedBeforeItIsWritten(t *testing.T) {
	subject := SubjectRef{Type: testSubject, ID: uuid.New()}
	command := EnrollCommand{Subject: subject, Identifier: "ann@example.test", Password: "0123456789"}

	free := crudtest.Postgres()
	if err := enrolWith(t, free).Execute(t.Context(), command); err != nil {
		t.Fatalf("the control enrolment was refused: %v", err)
	}
	if !wroteInto(free, "credentials") {
		t.Fatalf("the control enrolment wrote no credential: %v", free.SQL())
	}

	taken := crudtest.Postgres()
	existing := uuid.New()
	taken.Push(
		crudtest.Rows([]any{existing.String()}),
		crudtest.Rows(serialCredentialRow(existing, subject, "ann-was-renamed@example.test", "hashed:old")),
	)
	second := command
	second.Identifier = "ann@example.test"
	err := enrolWith(t, taken).Execute(t.Context(), second)
	if err == nil {
		t.Fatal("an account was enrolled with a second password; the first identifier would keep signing in")
	}
	if wroteInto(taken, "credentials") {
		t.Fatalf("the refused enrolment still wrote a credential: %v", taken.SQL())
	}
}

func enrolWith(t *testing.T, source crud.Source) *EnrollUseCase {
	t.Helper()
	store := NewStore(source)
	return NewEnroll(newDeps(
		store, nil, cheapHasher{}, Config{}, slog.New(slog.DiscardHandler), nil, Protection{}))
}

func TestAPasswordChangeRefusesAnAccountThatHoldsMoreThanOne(t *testing.T) {
	subject := SubjectRef{Type: testSubject, ID: uuid.New()}

	single := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	only := uuid.New()
	held := serialCredentialRow(only, subject, "ann@example.test", "hashed:current")
	single.Push(
		crudtest.Rows([]any{only.String()}),
		crudtest.Rows(held),
		crudtest.Rows(held),
		crudtest.Rows(held),
	)
	change := changeWith(t, single)
	if _, err := change.Execute(t.Context(), ChangePasswordCommand{
		Subject: subject, Current: "current", New: "0123456789",
	}); err != nil {
		t.Fatalf("the control change was refused: %v", err)
	}
	if !updated(single, "credentials") {
		t.Fatalf("the control change wrote no password: %v", single.SQL())
	}

	double := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	pair := []uuid.UUID{uuid.New(), uuid.New()}
	sort.Slice(pair, func(i, j int) bool { return bytes.Compare(pair[i][:], pair[j][:]) < 0 })
	first := serialCredentialRow(pair[0], subject, "ann@example.test", "hashed:current")
	double.Push(
		crudtest.Rows([]any{pair[0].String()}, []any{pair[1].String()}),
		crudtest.Rows(first),
		crudtest.Rows(serialCredentialRow(pair[1], subject, "ann-also@example.test", "hashed:current")),
		crudtest.Rows(first),
		crudtest.Rows(first),
		crudtest.Rows(),
	)
	_, err := changeWith(t, double).Execute(t.Context(), ChangePasswordCommand{
		Subject: subject, Current: "current", New: "0123456789",
	})
	if err == nil {
		t.Fatal("a password change wrote to one of two credentials; the other would keep signing in")
	}
	if updated(double, "credentials") {
		t.Fatalf("the refused change still rewrote a password: %v", double.SQL())
	}
}

func changeWith(t *testing.T, source crud.Source) *ChangePasswordUseCase {
	t.Helper()
	store := NewStore(source)
	return NewChangePassword(newDeps(
		store, nil, cheapHasher{}, Config{}, slog.New(slog.DiscardHandler), newRevocationSinks(), Protection{}))
}

func TestAPasswordResetRefusesAnAccountThatHoldsMoreThanOne(t *testing.T) {
	subject := SubjectRef{Type: testSubject, ID: uuid.New()}
	pair := []uuid.UUID{uuid.New(), uuid.New()}
	sort.Slice(pair, func(i, j int) bool { return bytes.Compare(pair[i][:], pair[j][:]) < 0 })

	held := serialCredentialRow(pair[0], subject, "ann@example.test", "hashed:old")
	source := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	source.Push(
		crudtest.Rows([]any{pair[0].String()}, []any{pair[1].String()}),
		crudtest.Rows(held),
		crudtest.Rows(serialCredentialRow(pair[1], subject, "ann-also@example.test", "hashed:old")),
		crudtest.Rows(held),
		crudtest.Rows(held),
		crudtest.Rows(),
	)
	store := NewStore(source)
	grants := NewGrants(store, MustDirectories(stubDirectory{
		active:  true,
		profile: Profile{Identifier: "ann@example.test"},
	}))
	reset := NewSetPassword(newDeps(
		store, grants, cheapHasher{}, Config{}, slog.New(slog.DiscardHandler), newRevocationSinks(), Protection{}))

	if _, err := reset.Execute(t.Context(), SetPasswordCommand{
		Subject: subject, Password: "0123456789",
	}); err == nil {
		t.Fatal("a reset rewrote one of two credentials; the other identifier would still sign in with its old password")
	}
	if updated(source, "credentials") {
		t.Fatalf("the refused reset still rewrote a password: %v", source.SQL())
	}
}

func updated(recorder *crudtest.Recorder, table string) bool {
	for _, statement := range recorder.SQL() {
		if strings.HasPrefix(strings.TrimSpace(statement), "UPDATE") && strings.Contains(statement, table) {
			return true
		}
	}
	return false
}

// Sign-in folds the identifier it looks up; this path wrote the directory's raw
// one. A subject whose rule lowercases therefore got a credential stored as
// "Ann@Example.test" that no sign-in could ever find — a set password that
// silently could not be used.
func TestASetPasswordWritesTheIdentifierSignInWillLookUp(t *testing.T) {
	subject := SubjectRef{Type: "user", ID: uuid.New()}
	source := crudtest.Postgres()
	source.Push(
		crudtest.Rows(),
		crudtest.Rows(),
		crudtest.Rows(),
		crudtest.Rows(),
	)
	store := NewStore(source)
	grants := NewGrants(store, MustDirectories(stubDirectory{
		active:  true,
		profile: Profile{Identifier: "Ann@Example.test"},
	}))
	grants.registerNormalizer(Subject{Type: "user", Normalize: strings.ToLower})

	// Unguarded: this is about the identifier that gets written, not about who
	// may write it — TestSettingAnotherAccountsPasswordNeedsThePermissionItDeclares
	// is the one that pins the check.
	reset := NewSetPassword(newDeps(
		store, grants, cheapHasher{}, Config{}, slog.New(slog.DiscardHandler), newRevocationSinks(), Protection{})).Unguarded()
	if _, err := reset.Execute(t.Context(), SetPasswordCommand{Subject: subject, Password: "0123456789"}); err != nil {
		t.Fatalf("setting a password: %v", err)
	}

	written := strings.Join(source.SQL(), "\n")
	if !strings.Contains(written, "credentials") {
		t.Fatalf("nothing was written to credentials: %s", written)
	}
	folded := false
	for _, statement := range source.Statements() {
		for _, argument := range statement.Args {
			text, ok := argument.(string)
			if !ok {
				continue
			}
			if text == "Ann@Example.test" {
				t.Fatal("the directory's unfolded identifier was written, so no sign-in can find this credential")
			}
			if text == "ann@example.test" {
				folded = true
			}
		}
	}
	if !folded {
		t.Fatal("the folded identifier was never written, so this proves nothing")
	}
}

// The current-password check is a password oracle behind a valid session: an
// attacker holding a stolen access token could guess at full speed, and nothing
// counted it. It goes through the same limiter and the same observer as a
// sign-in, keyed on the subject rather than a caller-supplied identifier.
func TestGuessingTheCurrentPasswordIsCountedAndEventuallyRefused(t *testing.T) {
	moment := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	subject := SubjectRef{Type: testSubject, ID: uuid.New()}
	recorder := crudtest.Postgres()
	watcher := &attemptLog{}
	store := NewStore(recorder)
	grants := NewGrants(store, MustDirectories(stubDirectory{active: true}))
	change := NewChangePassword(newDeps(
		store, grants, cheapHasher{}, Config{Password: PasswordConfig{MinLength: 4}},
		slog.New(slog.DiscardHandler), newRevocationSinks(),
		Protection{Limiter: NewMemoryLimiter(fixedPolicy(moment, 2)), Observer: watcher},
	))
	command := ChangePasswordCommand{
		Subject: subject, Current: "a guess", New: "0123456789",
		Agent: Agent{IP: "203.0.113.7"},
	}

	credential := uuid.New()
	for guess := range 2 {
		recorder.Push(
			crudtest.Rows([]any{credential.String()}),
			crudtest.Rows(serialCredentialRow(credential, subject, "ann@example.test", "hashed:the-real-one")),
		)
		if _, err := change.Execute(t.Context(), command); !isBadCredentials(err) {
			t.Fatalf("guess %d with the wrong current password answered %v", guess+1, err)
		}
	}
	spent := len(recorder.Statements())
	if spent == 0 {
		t.Fatal("the first two guesses never reached the database, so the check below proves nothing")
	}

	// The third is refused before the database is touched, which is what a
	// limiter is for.
	if _, err := change.Execute(t.Context(), command); err == nil {
		t.Fatal("guessing continued past the ceiling")
	}
	if len(recorder.Statements()) != spent {
		t.Fatalf("the refused guess still reached the database: %v", recorder.SQL())
	}
	if len(watcher.outcomes()) < 2 {
		t.Fatalf("the observer saw %v — a wrong current password was never counted as an attempt", watcher.outcomes())
	}
}

// "Change my password because it may be compromised" is the reason people change
// passwords, and this left every other session signed in unless the request body
// asked otherwise — a decision the deployment could not make and the caller had
// no reason to know about.
func TestAPasswordChangeClosesTheOtherSessionsByDefault(t *testing.T) {
	subject := SubjectRef{Type: testSubject, ID: uuid.New()}
	credential := uuid.New()

	run := func(t *testing.T, config Config, body ChangePasswordCommand) []crudtest.Statement {
		t.Helper()
		recorder := crudtest.Postgres()
		recorder.Push(
			crudtest.Rows([]any{credential.String()}),
			crudtest.Rows(credentialRow(credential, subject, "hashed:the-old-one")),
			crudtest.Rows(credentialRow(credential, subject, "hashed:the-old-one")),
			crudtest.Rows(credentialRow(credential, subject, "hashed:0123456789")),
			// One live session other than the caller's, so a revocation has
			// something to close and shows up as an UPDATE.
			crudtest.Rows(sessionRow(uuid.New(), subject.Type)),
			crudtest.Rows(),
			crudtest.Rows(),
		)
		store := NewStore(recorder)
		grants := NewGrants(store, MustDirectories(stubDirectory{active: true}))
		change := NewChangePassword(newDeps(store, grants, cheapHasher{}, config,
			slog.New(slog.DiscardHandler), newRevocationSinks(), Protection{}))
		body.Subject = subject
		body.Current = "the-old-one"
		body.New = "0123456789"
		if _, err := change.Execute(t.Context(), body); err != nil {
			t.Fatalf("changing the password: %v", err)
		}
		return recorder.Statements()
	}

	minimum := Config{Password: PasswordConfig{MinLength: 4}}
	if !revokedSessions(run(t, minimum, ChangePasswordCommand{})) {
		t.Fatal("a password change left every other session signed in")
	}

	// The deployment may choose otherwise, and only the deployment.
	keeping := minimum
	keeping.Password.KeepOtherSessions = true
	if revokedSessions(run(t, keeping, ChangePasswordCommand{})) {
		t.Fatal("a deployment that asked to keep other sessions had them closed anyway")
	}
	// ... and the body can still widen it, never narrow it.
	if !revokedSessions(run(t, keeping, ChangePasswordCommand{RevokeOthers: true})) {
		t.Fatal("a caller who asked to close the other sessions was ignored")
	}
}

func revokedSessions(statements []crudtest.Statement) bool {
	for _, statement := range statements {
		normalized := strings.ToUpper(crudtest.Normalize(statement.SQL))
		if strings.HasPrefix(strings.TrimSpace(normalized), "UPDATE") && strings.Contains(normalized, "SESSIONS") {
			return true
		}
	}
	return false
}

// The module declares and seeds PermCredentialWrite, PermGrantWrite and
// PermRoleWrite, and then enforced none of them. A deployment that mounted these
// use cases behind an authenticated route therefore let every signed-in caller
// set anyone's password and grant themselves any role.
func TestSettingAnotherAccountsPasswordNeedsThePermissionItDeclares(t *testing.T) {
	subject := SubjectRef{Type: testSubject, ID: uuid.New()}
	build := func() *SetPasswordUseCase {
		recorder := crudtest.Postgres()
		store := NewStore(recorder)
		grants := NewGrants(store, MustDirectories(stubDirectory{
			active: true, profile: Profile{Identifier: "ann@example.test"},
		}))
		return NewSetPassword(newDeps(store, grants, cheapHasher{}, Config{},
			slog.New(slog.DiscardHandler), newRevocationSinks(), Protection{}))
	}
	command := SetPasswordCommand{Subject: subject, Password: "0123456789"}

	if _, err := build().Execute(t.Context(), command); err == nil {
		t.Fatal("a caller with no principal at all set another account's password")
	}

	holder := signedInAs(t, "something.else")
	if _, err := build().Execute(holder, command); err == nil {
		t.Fatal("a signed-in caller without PermCredentialWrite set another account's password")
	}

	// The control: the permission the module declares for this is the one that
	// opens it, and the seed path stays reachable with no principal at all.
	allowed := signedInAs(t, PermCredentialWrite)
	if _, err := build().Execute(allowed, command); err != nil {
		t.Fatalf("the permission the module declares did not open it: %v", err)
	}
	if _, err := build().Unguarded().Execute(t.Context(), command); err != nil {
		t.Fatalf("the seed path was refused: %v", err)
	}
}

func TestGrantingARoleNeedsThePermissionItDeclares(t *testing.T) {
	subject := SubjectRef{Type: testSubject, ID: uuid.New()}
	command := GrantRoleCommand{Subject: subject, Role: "admin"}

	// The kind of refusal, not its presence: an empty recorder fails this call
	// anyway, so asserting err != nil would pass with the check deleted.
	guarded := NewGrantService(NewStore(crudtest.Postgres()))
	if err := guarded.GrantRole(t.Context(), command); !refusedForAuthorization(err) {
		t.Fatalf("a caller with no principal was refused as %v, not for want of one", err)
	}
	holder := signedInAs(t, "something.else")
	if err := guarded.GrantRole(holder, command); !refusedForAuthorization(err) {
		t.Fatalf("a signed-in caller without PermGrantWrite was refused as %v", err)
	}

	// The control: the seed path runs with no principal and is not refused, so
	// the refusals above are the permission and not the service being broken.
	seeding := NewUnguardedGrantService(NewStore(crudtest.Postgres().Push(crudtest.Rows())))
	if err := seeding.GrantRole(t.Context(), command); refusedForAuthorization(err) {
		t.Fatalf("the seed path was refused for want of a principal: %v", err)
	}
}

func signedInAs(t *testing.T, permissions ...auth.Permission) context.Context {
	t.Helper()
	return auth.WithPrincipal(t.Context(), &Principal{
		Ref:         SubjectRef{Type: testSubject, ID: uuid.New()},
		Permissions: permissions,
	})
}

// Any other failure is this fixture's empty database talking. What must not
// happen is a refusal *for want of a principal*, which is what the unguarded
// constructor exists to avoid.
func refusedForAuthorization(err error) bool {
	fault, ok := errs.AsFault(err)
	return ok && (fault.Kind == errs.KindUnauthorized || fault.Kind == errs.KindForbidden)
}

// Raising the cost factor used to protect only accounts created after the
// change: every existing password kept verifying against its old, cheaper hash
// forever, and nothing in the seam could even say so.
func TestASignInUpgradesAPasswordHashDerivedAtALowerCost(t *testing.T) {
	weak := &Argon2Hasher{Time: 1, Memory: 8 << 10, Threads: 1, KeyLen: 32}
	strong := &Argon2Hasher{Time: 3, Memory: 64 << 10, Threads: 2, KeyLen: 32}

	stored, err := weak.Hash("the-right-password")
	if err != nil {
		t.Fatal(err)
	}
	if !strong.NeedsRehash(stored) {
		t.Fatal("a hash derived at a lower cost was not recognised as needing one, so this proves nothing")
	}
	// The control: the current hasher's own output needs nothing, or every sign-in
	// would rewrite a credential for no reason.
	current, err := strong.Hash("the-right-password")
	if err != nil {
		t.Fatal(err)
	}
	if strong.NeedsRehash(current) {
		t.Fatal("a hash at the current cost was reported as needing a re-derivation")
	}
	// And a hash it cannot read is left alone rather than destroyed on a guess.
	if strong.NeedsRehash("$2y$10$something-bcrypt-shaped") {
		t.Fatal("a hash this package did not produce was marked for rewriting")
	}

	rehasher, ok := RehasherOf(strong)
	if !ok || rehasher == nil {
		t.Fatal("the capability is not discoverable through RehasherOf")
	}
	if _, found := RehasherOf(cheapHasher{}); found {
		t.Fatal("a hasher that declares no rehashing was reported as offering it")
	}
}
