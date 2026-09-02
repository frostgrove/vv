package access

import (
	"bytes"
	"context"
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
