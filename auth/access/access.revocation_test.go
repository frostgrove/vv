package access

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/google/uuid"
)

// What these pin is [[D-072]]: closing a session has to reach the strategy that
// issued it, and every path that closes one goes through the same helper.
//
// The failure they exist for is silent. With a signed token the row is written,
// the count comes back right, the endpoint answers 200 — and the credential
// keeps working for the rest of its lifetime, because nothing the verifier
// reads has changed. There is nothing to see from outside except a session that
// is closed everywhere except where it matters.

// recordingSink is a RevocationSink that remembers what it was told.
type recordingSink struct {
	told [][]uuid.UUID
	err  error
}

func (this *recordingSink) SessionsRevoked(_ context.Context, sessions []uuid.UUID) error {
	this.told = append(this.told, append([]uuid.UUID(nil), sessions...))
	return this.err
}

func (this *recordingSink) flat() []uuid.UUID {
	out := []uuid.UUID{}
	for _, batch := range this.told {
		out = append(out, batch...)
	}
	return out
}

// sessionRow is a row shaped for the two columns revoke selects.
func sessionRow(id uuid.UUID, subject SubjectType) []any {
	return []any{id.String(), string(subject)}
}

func credentialRow(subject SubjectRef, secret string) []any {
	return []any{
		uuid.New().String(), string(subject.Type), subject.ID.String(),
		ProviderPassword, "someone@example.test", secret, time.Now(), time.Now(),
	}
}

// depsWithSinks builds the use-case bundle over a recorder, with sinks
// registered as Mount would have registered them.
func depsWithSinks(recorder *crudtest.Recorder, sinks map[SubjectType]RevocationSink) *Deps {
	registry := newRevocationSinks()
	for subject, sink := range sinks {
		registry.register(subject, sink)
	}
	return newDeps(NewStore(recorder), nil, nil, Config{}, slog.New(slog.DiscardHandler), registry)
}

// The whole point, at its smallest: signing out names the session to the
// strategy, not just to the table.
func TestSigningOutTellsTheStrategyWhichSessionClosed(t *testing.T) {
	session := uuid.New()
	recorder := crudtest.Postgres().
		Push(crudtest.Rows(sessionRow(session, testSubject))).
		ExecResult(crud.Result{RowsAffected: 1})

	sink := &recordingSink{}
	dependencies := depsWithSinks(recorder, map[SubjectType]RevocationSink{testSubject: sink})

	closed, err := NewLogout(dependencies).Execute(context.Background(),
		LogoutCommand{SessionID: session})
	if err != nil {
		t.Fatalf("signing out: %v", err)
	}
	if closed != 1 {
		t.Fatalf("logout closed %d sessions, want 1", closed)
	}

	if got := sink.flat(); len(got) != 1 || got[0] != session {
		t.Fatalf("the strategy was told about %v, want exactly the session that closed (%s)", got, session)
	}
}

// The control for the test above. A strategy that verifies by reading the
// session row declares no sink, and nothing about it may change: it must not
// pay for a lookup, and it must not fail because a sink it never had returned
// an error.
func TestAStrategyThatDeclaredNoSinkIsNeverAsked(t *testing.T) {
	session := uuid.New()
	recorder := crudtest.Postgres().
		Push(crudtest.Rows(sessionRow(session, testSubject))).
		ExecResult(crud.Result{RowsAffected: 1})

	dependencies := depsWithSinks(recorder, nil)

	closed, err := NewLogout(dependencies).Execute(context.Background(),
		LogoutCommand{SessionID: session})
	if err != nil {
		t.Fatalf("signing out with no sink registered: %v", err)
	}
	if closed != 1 {
		t.Fatalf("logout closed %d sessions, want 1", closed)
	}
}

// A session belongs to exactly one kind of caller. Telling every registered
// strategy would put a key in a deny-list that nothing ever reads.
func TestOnlyTheOwningSubjectsStrategyIsTold(t *testing.T) {
	const staff SubjectType = "staff"
	mine, theirs := uuid.New(), uuid.New()

	recorder := crudtest.Postgres().
		Push(crudtest.Rows(sessionRow(mine, testSubject), sessionRow(theirs, staff))).
		ExecResult(crud.Result{RowsAffected: 2})

	users, others := &recordingSink{}, &recordingSink{}
	dependencies := depsWithSinks(recorder, map[SubjectType]RevocationSink{
		testSubject: users,
		staff:       others,
	})

	if _, err := NewLogoutAll(dependencies).Execute(context.Background(),
		LogoutAllCommand{Subject: SubjectRef{Type: testSubject, ID: uuid.New()}}); err != nil {
		t.Fatalf("signing out everywhere: %v", err)
	}

	if got := users.flat(); len(got) != 1 || got[0] != mine {
		t.Fatalf("the user strategy was told about %v, want [%s]", got, mine)
	}
	if got := others.flat(); len(got) != 1 || got[0] != theirs {
		t.Fatalf("the staff strategy was told about %v, want [%s]", got, theirs)
	}
}

// A rollback after a sink was told would leave a deny-list refusing a session
// that is still live, and nothing ever takes an entry back out. So the password
// use cases collect inside the transaction and announce after it commits.
func TestASinkIsNotToldWhenTheTransactionRollsBack(t *testing.T) {
	subject := SubjectRef{Type: testSubject, ID: uuid.New()}
	const current = "the-current-password"
	boom := errors.New("the credential write failed")

	recorder := crudtest.Postgres().Push(
		// The lookup that verifies the current password, and then the row
		// Update locks inside the transaction before it writes.
		crudtest.Rows(credentialRow(subject, "hashed:"+current)),
		crudtest.Rows(credentialRow(subject, "hashed:"+current)),
		// The write itself fails, so the sessions this change would have closed
		// are still live when the transaction unwinds. Queued rather than
		// Recorder.Fail: the UPDATE carries RETURNING and is therefore issued as
		// a query, which Fail does not reach.
		crudtest.Result{Err: boom},
	)

	sink := &recordingSink{}
	dependencies := depsWithSinks(recorder, map[SubjectType]RevocationSink{testSubject: sink})
	dependencies.Hasher = cheapHasher{}

	_, err := NewChangePassword(dependencies).Execute(context.Background(), ChangePasswordCommand{
		Subject:      subject,
		Current:      current,
		New:          "a-long-enough-new-password",
		RevokeOthers: true,
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the write's own failure", err)
	}
	if len(sink.told) != 0 {
		t.Fatalf("the strategy was told about %v from a transaction that rolled back", sink.flat())
	}
}

// The rows are committed and the caller is signed out. Answering "could not
// sign you out" to somebody who is signed out is worse than the window a
// failed announcement leaves, and that window is bounded by the credential's
// own lifetime.
func TestAFailingSinkDoesNotFailTheSignOut(t *testing.T) {
	session := uuid.New()
	recorder := crudtest.Postgres().
		Push(crudtest.Rows(sessionRow(session, testSubject))).
		ExecResult(crud.Result{RowsAffected: 1})

	sink := &recordingSink{err: errors.New("redis is unreachable")}
	dependencies := depsWithSinks(recorder, map[SubjectType]RevocationSink{testSubject: sink})

	closed, err := NewLogout(dependencies).Execute(context.Background(),
		LogoutCommand{SessionID: session})
	if err != nil {
		t.Fatalf("an unreachable sink failed a sign-out that had already happened: %v", err)
	}
	if closed != 1 {
		t.Fatalf("logout closed %d sessions, want 1", closed)
	}
	if len(sink.told) != 1 {
		t.Fatal("the sink was not even attempted")
	}
}

// The read exists so a sink can be told *which* sessions closed. An UPDATE ...
// WHERE answers only how many, and this is the assertion that keeps somebody
// from collapsing the two statements back into one.
func TestClosingASessionReadsTheRowsBeforeItWritesThem(t *testing.T) {
	session := uuid.New()
	recorder := crudtest.Postgres().
		Push(crudtest.Rows(sessionRow(session, testSubject))).
		ExecResult(crud.Result{RowsAffected: 1})

	dependencies := depsWithSinks(recorder, map[SubjectType]RevocationSink{testSubject: &recordingSink{}})
	if _, err := NewLogout(dependencies).Execute(context.Background(),
		LogoutCommand{SessionID: session}); err != nil {
		t.Fatalf("signing out: %v", err)
	}

	statements := recorder.Statements()
	if len(statements) != 2 {
		t.Fatalf("signing out ran %d statements, want a read then a write: %v", len(statements), recorder.SQL())
	}
	if !statements[0].Query || !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statements[0].SQL)), "SELECT") {
		t.Fatalf("the first statement is not the read: %q", statements[0].SQL)
	}
	if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statements[1].SQL)), "UPDATE") {
		t.Fatalf("the second statement is not the write: %q", statements[1].SQL)
	}
	// Narrowed to what the read found, so what was announced and what was
	// closed are the same set.
	if !strings.Contains(strings.ToUpper(statements[1].SQL), "IN (") {
		t.Fatalf("the write does not name the rows the read found: %q", statements[1].SQL)
	}
}

// The seam is only worth anything if Mount connects it. A strategy that
// declares a sink and a runtime that never registers it compiles, passes every
// test above, and leaves the token working after a sign-out.
func TestMountRegistersTheStrategysRevocationSink(t *testing.T) {
	runtime := testRuntime(t)
	sink := &recordingSink{}

	if _, _, err := Mount(runtime, SubjectSpec[struct{}]{
		Type:      testSubject,
		Directory: stubDirectory{active: true},
		Strategy:  revokingStrategy{sink: sink},
	}); err != nil {
		t.Fatalf("mounting a subject whose strategy revokes: %v", err)
	}

	if got := runtime.revocations.byType[testSubject]; got != RevocationSink(sink) {
		t.Fatalf("the runtime registered %v for %q, want the strategy's own sink", got, testSubject)
	}

	// And the same registry reaches the use case an application asks the
	// runtime for, which is the administrator's password reset — the one
	// closing path that is not behind a subject's endpoints.
	if NewSetPassword(newDeps(runtime.store, runtime.grants, runtime.hasher,
		runtime.config, runtime.logger, runtime.revocations)).revocations.byType[testSubject] == nil {
		t.Fatal("the runtime's own use cases cannot reach the sink")
	}
}

// The control for the test above: a strategy that declares none registers none,
// rather than a typed nil that access would then call.
func TestMountRegistersNothingForAStrategyWithoutASink(t *testing.T) {
	runtime := testRuntime(t)
	if _, _, err := Mount(runtime, SubjectSpec[struct{}]{
		Type:      testSubject,
		Directory: stubDirectory{active: true},
	}); err != nil {
		t.Fatalf("mounting a subject: %v", err)
	}
	if !runtime.revocations.empty() {
		t.Fatalf("the default strategy registered a sink: %v", runtime.revocations.byType)
	}
}

// revokingStrategy is the smallest strategy that declares a revocation sink.
type revokingStrategy struct{ sink RevocationSink }

func (this revokingStrategy) Build(dependencies StrategyDeps) (Issued, error) {
	built, err := OpaqueToken().Build(dependencies)
	if err != nil {
		return Issued{}, err
	}
	built.Revocations = this.sink
	return built, nil
}
