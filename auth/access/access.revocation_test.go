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

func sessionRow(id uuid.UUID, subject SubjectType) []any {
	return []any{id.String(), string(subject)}
}

func credentialRow(id uuid.UUID, subject SubjectRef, secret string) []any {
	return []any{
		id.String(), string(subject.Type), subject.ID.String(),
		ProviderPassword, "someone@example.test", secret, time.Now(), time.Now(),
	}
}

func depsWithSinks(recorder *crudtest.Recorder, sinks map[SubjectType]RevocationSink) *Deps {
	registry := newRevocationSinks()
	for subject, sink := range sinks {
		registry.register(subject, sink)
	}
	return newDeps(NewStore(recorder), nil, nil, Config{}, slog.New(slog.DiscardHandler), registry)
}

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

func TestOnlyTheOwningSubjectsStrategyIsTold(t *testing.T) {
	const staff SubjectType = "staff"
	mine, theirs := uuid.New(), uuid.New()

	recorder := crudtest.Postgres().
		Push(

			crudtest.Rows(),
			crudtest.Rows(sessionRow(mine, testSubject), sessionRow(theirs, staff)),
		).
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

func TestASinkIsNotToldWhenTheTransactionRollsBack(t *testing.T) {
	subject := SubjectRef{Type: testSubject, ID: uuid.New()}
	const current = "the-current-password"
	boom := errors.New("the credential write failed")
	credentialID := uuid.New()

	recorder := crudtest.Postgres().Push(

		crudtest.Rows([]any{credentialID.String()}),
		crudtest.Rows(credentialRow(credentialID, subject, "hashed:"+current)),
		crudtest.Rows(credentialRow(credentialID, subject, "hashed:"+current)),

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

func TestSessionInvalidationOwnsCommitBeforeAnnouncementDespiteOuterRollback(t *testing.T) {
	session := uuid.New()
	recorder := crudtest.Postgres().
		Push(crudtest.Rows(sessionRow(session, testSubject))).
		ExecResult(crud.Result{RowsAffected: 1})
	source := &serialSource{Recorder: recorder}
	sink := &recordingSink{}
	registry := newRevocationSinks()
	registry.register(testSubject, sink)
	dependencies := newDeps(NewStore(source), nil, nil, Config{}, slog.New(slog.DiscardHandler), registry)

	outerExec, err := source.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	outer := outerExec.(*serialTx)
	ctx := crud.BindExecutor(t.Context(), source, outer)
	closed, err := NewLogout(dependencies).Execute(ctx, LogoutCommand{SessionID: session})
	if err != nil || closed != 1 {
		t.Fatalf("closed=%d err=%v", closed, err)
	}
	inner := source.last
	if inner == nil || inner == outer || !inner.committed {
		t.Fatalf("owned invalidation transaction=%p committed=%v outer=%p", inner, inner != nil && inner.committed, outer)
	}
	if got := sink.flat(); len(got) != 1 || got[0] != session {
		t.Fatalf("post-commit sink announcement=%v, want [%s]", got, session)
	}
	if err := outer.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if !inner.committed || len(sink.flat()) != 1 {
		t.Fatal("ambient rollback changed committed invalidation/sink outcome")
	}
}

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
	if depth := recorder.TxDepth(); depth != 1 {
		t.Fatalf("signing out opened %d transactions, want one around its locking read and write", depth)
	}
	if !statements[0].Query || !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statements[0].SQL)), "SELECT") {
		t.Fatalf("the first statement is not the read: %q", statements[0].SQL)
	}
	if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statements[1].SQL)), "UPDATE") {
		t.Fatalf("the second statement is not the write: %q", statements[1].SQL)
	}

	if !strings.Contains(strings.ToUpper(statements[1].SQL), "IN (") {
		t.Fatalf("the write does not name the rows the read found: %q", statements[1].SQL)
	}
}

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

	if NewSetPassword(newDeps(runtime.store, runtime.grants, runtime.hasher,
		runtime.config, runtime.logger, runtime.revocations)).revocations.byType[testSubject] == nil {
		t.Fatal("the runtime's own use cases cannot reach the sink")
	}
}

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

type revokingStrategy struct{ sink RevocationSink }

func (this revokingStrategy) Build(dependencies StrategyDeps) (Issued, error) {
	built, err := OpaqueToken().Build(dependencies)
	if err != nil {
		return Issued{}, err
	}
	built.Revocations = this.sink
	return built, nil
}
