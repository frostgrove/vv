package access

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/google/uuid"
)

func liveSessionRow(id uuid.UUID, ref SubjectRef, lastUsed, expires time.Time) []any {
	return []any{
		id.String(), string(ref.Type), ref.ID.String(), HashToken("presented"),
		"a browser", "203.0.113.7", lastUsed, lastUsed, expires, nil, "",
	}
}

func authenticatorOver(recorder *crudtest.Recorder, configuration Config) *SessionAuthenticator {
	directories, err := NewDirectories(stubDirectory{active: true})
	if err != nil {
		panic(err)
	}
	store := NewStore(recorder)
	return NewAuthenticator(store, NewGrants(store, directories), configuration,
		slog.New(slog.DiscardHandler))
}

func TestASessionIsWeighedAgainstTheClockTheRestOfTheModuleReads(t *testing.T) {
	ref := SubjectRef{Type: testSubject, ID: uuid.New()}
	expiry := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	afterExpiry := expiry.Add(time.Hour)

	recorder := crudtest.Postgres().
		Push(crudtest.Rows(liveSessionRow(uuid.New(), ref, expiry.Add(-time.Minute), expiry)))

	configuration := Config{Clock: func() time.Time { return afterExpiry }}
	_, err := authenticatorOver(recorder, configuration).
		Authenticate(context.Background(), auth.Credential{Scheme: auth.SchemeBearer, Token: "presented"})

	if err == nil {
		t.Fatal("a session the configured clock puts past its expiry still authenticated")
	}
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("the session was refused by something other than the guard's own contract: %v", err)
	}
	if got := len(recorder.Statements()); got != 1 {
		t.Fatalf("the refusal came after %d statements, so liveness is not what decided: %v",
			got, recorder.SQL())
	}
}

func TestTheConfiguredClockIsWhatCloseAndListingAgreeOn(t *testing.T) {
	frozen := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	dependencies := newDeps(nil, nil, nil, Config{Clock: func() time.Time { return frozen }},
		slog.New(slog.DiscardHandler), newRevocationSinks(), Protection{})

	if got := dependencies.Now(); !got.Equal(frozen) {
		t.Fatalf("the use cases stamp %v on a revocation while the issuer stamps %v", got, frozen)
	}
}

func TestDepsHasNoClockOfItsOwnToDriftFromTheConfiguredOne(t *testing.T) {
	frozen := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	dependencies := newDeps(nil, nil, nil, Config{}, slog.New(slog.DiscardHandler),
		newRevocationSinks(), Protection{})

	dependencies.Config = Config{Clock: func() time.Time { return frozen }}

	if got := dependencies.Now(); !got.Equal(frozen) {
		t.Fatalf("the use cases read %v while the issuer, which asks Config, reads %v", got, frozen)
	}
}

func TestListingSessionsLeavesOutTheOnesNobodyCouldStillUse(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	recorder := crudtest.Postgres().Push(crudtest.Rows())

	if _, err := NewStore(recorder).LiveSessionsOf(context.Background(),
		SubjectRef{Type: testSubject, ID: uuid.New()}, now, 48*time.Hour); err != nil {
		t.Fatalf("listing sessions: %v", err)
	}

	statement := recorder.Last()
	where, _, found := strings.Cut(statement.SQL, " ORDER BY ")
	if !found {
		t.Fatalf("the listing no longer orders its rows, so this test reads the wrong half: %s", statement.SQL)
	}
	for _, want := range []string{"revoked_at", "expires_at", "last_used_at"} {
		if !strings.Contains(where[strings.Index(where, " WHERE "):], want) {
			t.Fatalf("the query does not narrow on %s, so a session that died of it is listed as active: %s",
				want, statement.SQL)
		}
	}

	var expiry, idleDeadline bool
	for _, arg := range statement.Args {
		moment, isTime := arg.(time.Time)
		if !isTime {
			continue
		}
		expiry = expiry || moment.Equal(now)
		idleDeadline = idleDeadline || moment.Equal(now.Add(-48*time.Hour))
	}
	if !expiry || !idleDeadline {
		t.Fatalf("the statement was given neither the moment nor the idle deadline to compare against: %v",
			statement.Args)
	}
}

func TestARevocationTheSinkMissedIsReplayedFromTheSessionsTable(t *testing.T) {
	session := uuid.New()
	now := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)

	recorder := crudtest.Postgres().
		Push(crudtest.Rows(sessionRow(session, testSubject))).
		ExecResult(crud.Result{RowsAffected: 1})

	sink := &recordingSink{err: errUnreachableSink}
	dependencies := depsWithSinks(recorder, map[SubjectType]RevocationSink{testSubject: sink})
	dependencies.Config = Config{Clock: func() time.Time { return now }}

	if _, err := NewLogout(dependencies).Execute(context.Background(),
		LogoutCommand{SessionID: session}); err != nil {
		t.Fatalf("signing out: %v", err)
	}
	if len(sink.told) != 1 {
		t.Fatal("the sink was not even attempted")
	}

	sink.err = nil
	recorder.Push(crudtest.Rows(sessionRow(session, testSubject)))
	if err := dependencies.ReannounceRevocations(context.Background(), now.Add(-time.Hour)); err != nil {
		t.Fatalf("replaying what the sink missed: %v", err)
	}

	if got := sink.flat(); len(got) != 2 || got[1] != session {
		t.Fatalf("the strategy was told about %v; the session it never heard about stays usable", got)
	}
}

func TestReplayingRevocationsHandsBackTheFailureRatherThanSwallowingIt(t *testing.T) {
	recorder := crudtest.Postgres().Push(crudtest.Rows(sessionRow(uuid.New(), testSubject)))
	sink := &recordingSink{err: errUnreachableSink}
	dependencies := depsWithSinks(recorder, map[SubjectType]RevocationSink{testSubject: sink})

	err := dependencies.ReannounceRevocations(context.Background(), time.Now().Add(-time.Hour))
	if err == nil {
		t.Fatal("a replay that reached nobody reported success, so a worker would drop the batch")
	}
	if !strings.Contains(err.Error(), errUnreachableSink.Error()) {
		t.Fatalf("the failure does not carry what went wrong: %v", err)
	}
}
