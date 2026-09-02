package accessjwt

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/google/uuid"
)

type scriptedSessions struct {
	*crudtest.Recorder

	mu    sync.Mutex
	execs []crud.Result
	rows  [][]any
}

func (this *scriptedSessions) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	this.mu.Lock()
	if len(this.execs) > 0 {
		this.Recorder.ExecResult(this.execs[0])
		this.execs = this.execs[1:]
	}
	this.mu.Unlock()
	return this.Recorder.Exec(ctx, query, args...)
}

func (this *scriptedSessions) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	this.mu.Lock()
	if strings.Contains(query, "sessions") && len(this.rows) > 0 {
		this.Recorder.Push(crudtest.Rows(this.rows[0]))
		this.rows = this.rows[1:]
	}
	this.mu.Unlock()
	return this.Recorder.Query(ctx, query, args...)
}

func rowOf(id uuid.UUID, current, previous string, rotatedAt any, moment time.Time) []any {
	return []any{
		id.String(), "user", uuid.New().String(), current, previous,
		"", "", moment, moment, rotatedAt, moment.Add(24 * time.Hour), nil, "",
	}
}

func updates(source *scriptedSessions) []crudtest.Statement {
	var out []crudtest.Statement
	for _, statement := range source.Statements() {
		if strings.HasPrefix(strings.ToUpper(crudtest.Normalize(statement.SQL)), "UPDATE") {
			out = append(out, statement)
		}
	}
	return out
}

func rotating(t *testing.T, spec Spec, source *scriptedSessions, moment time.Time) access.SessionRefresher {
	t.Helper()

	config := access.Config{
		Session: access.SessionConfig{TTL: 24 * time.Hour, IdleTTL: time.Hour},
		Clock:   func() time.Time { return moment },
	}
	deps := testDeps(source, config)
	deps.Subject = access.Subject{Type: "user"}
	deps.Grants = access.NewGrants(deps.Store, access.MustDirectories(rotationDirectory{}))

	issued, err := Strategy(spec).Build(deps)
	if err != nil {
		t.Fatalf("building the strategy: %v", err)
	}
	return issued.Refresher
}

func TestARefreshThatLosesTheRaceRotatesAgainRatherThanSigningTheCallerOut(t *testing.T) {
	moment := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	session := uuid.New()
	presented := access.HashToken("the-refresh-credential")
	winner := access.HashToken("what the concurrent refresh was handed")

	source := &scriptedSessions{Recorder: crudtest.Postgres()}
	source.execs = []crud.Result{{RowsAffected: 0}, {RowsAffected: 1}}
	source.rows = [][]any{
		rowOf(session, presented, "", nil, moment),
		rowOf(session, winner, presented, moment, moment),
	}

	response, err := rotating(t, testSpec(), source, moment).
		Refresh(t.Context(), "the-refresh-credential", access.Agent{})
	if err != nil {
		t.Fatalf("the refresh that lost the compare-and-swap was refused: %v\nstatements: %v", err, source.SQL())
	}
	if response.Refresh == "" || response.Token == "" {
		t.Fatalf("the loser left with refresh=%q token=%q", response.Refresh, response.Token)
	}

	written := updates(source)
	if len(written) != 2 {
		t.Fatalf("the rotation wrote %d times, want the lost swap and the one that took the winner's digest", len(written))
	}
	if !hasArgument(written[1], winner) {
		t.Fatalf("the second swap did not compare against the digest the winner left behind: %v", written[1])
	}
}

func hasArgument(statement crudtest.Statement, want string) bool {
	for _, argument := range statement.Args {
		if text, ok := argument.(string); ok && text == want {
			return true
		}
	}
	return false
}

func TestARotationMovesTheLineageOnlyOnceTheReplacementExists(t *testing.T) {
	moment := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	presented := access.HashToken("the-refresh-credential")

	unsignable := testSpec()
	unsignable.Key = "a key of a type this signing method cannot use"

	source := &scriptedSessions{Recorder: crudtest.Postgres()}
	source.execs = []crud.Result{{RowsAffected: 1}}
	source.rows = [][]any{rowOf(uuid.New(), presented, "", nil, moment)}

	if _, err := rotating(t, unsignable, source, moment).
		Refresh(t.Context(), "the-refresh-credential", access.Agent{}); err == nil {
		t.Fatal("a rotation that could not mint an access token answered as though it had")
	}
	if written := updates(source); len(written) != 0 {
		t.Fatalf("the credential was spent for an answer nobody received: %v", written)
	}

	control := &scriptedSessions{Recorder: crudtest.Postgres()}
	control.execs = []crud.Result{{RowsAffected: 1}}
	control.rows = [][]any{rowOf(uuid.New(), presented, "", nil, moment)}
	if _, err := rotating(t, testSpec(), control, moment).
		Refresh(t.Context(), "the-refresh-credential", access.Agent{}); err != nil {
		t.Fatalf("the control rotation failed too, so the case above proves nothing: %v", err)
	}
	if written := updates(control); len(written) != 1 {
		t.Fatalf("the control rotation wrote %d times, so the absence above proves nothing", len(written))
	}
}
