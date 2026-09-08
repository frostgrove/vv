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
		if row := this.rows[0]; row == nil {
			this.Recorder.Push(crudtest.Result{})
		} else {
			this.Recorder.Push(crudtest.Rows(row))
		}
		this.rows = this.rows[1:]
	}
	this.mu.Unlock()
	return this.Recorder.Query(ctx, query, args...)
}

func rowOf(id uuid.UUID, current, previous string, rotatedAt any, moment time.Time) []any {
	return rowAtGeneration(id, current, previous, rotatedAt, moment, 1)
}

func rowAtGeneration(id uuid.UUID, current, previous string, rotatedAt any, moment time.Time, generation int64) []any {
	return []any{
		id.String(), "user", uuid.New().String(), current, previous, generation,
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

type recordingRevocations struct {
	mu      sync.Mutex
	revoked map[uuid.UUID]time.Time
}

func (this *recordingRevocations) Revoked(context.Context, uuid.UUID) (bool, error) {
	return false, nil
}

func (this *recordingRevocations) Revoke(_ context.Context, session uuid.UUID, until time.Time) error {
	this.mu.Lock()
	defer this.mu.Unlock()
	if this.revoked == nil {
		this.revoked = make(map[uuid.UUID]time.Time)
	}
	this.revoked[session] = until
	return nil
}

// The theft response: a refresh credential that was already rotated away, past
// the grace, means somebody else holds a copy — so the session is closed and put
// on the deny-list rather than merely refused. Nothing tested this arm at all,
// and deleting the whole of it left the suite green.
func TestAReplayedRefreshCredentialClosesTheSessionAndDeniesItsAccessToken(t *testing.T) {
	moment := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	session := uuid.New()
	stolen := access.HashToken("the-credential-that-was-already-spent")

	source := &scriptedSessions{Recorder: crudtest.Postgres()}
	source.execs = []crud.Result{{RowsAffected: 1}}
	// Rotated well past the grace, with the presented digest sitting in the
	// previous column: this is a spent credential, not a retry of the last one.
	source.rows = [][]any{
		rowOf(session, access.HashToken("what the legitimate holder has now"), stolen, moment.Add(-time.Hour), moment),
	}

	config := access.Config{
		Session: access.SessionConfig{TTL: 24 * time.Hour, IdleTTL: time.Hour},
		Clock:   func() time.Time { return moment },
	}
	deps := testDeps(source, config)
	deps.Subject = access.Subject{Type: "user"}
	deps.Grants = access.NewGrants(deps.Store, access.MustDirectories(rotationDirectory{}))

	denied := &recordingRevocations{}
	spec := testSpec()
	spec.Revocation = denied
	issued, err := Strategy(spec).Build(deps)
	if err != nil {
		t.Fatalf("building the strategy: %v", err)
	}

	if _, err := issued.Refresher.Refresh(t.Context(), "the-credential-that-was-already-spent", access.Agent{}); err == nil {
		t.Fatal("a spent refresh credential was accepted")
	}

	written := updates(source)
	if len(written) != 1 {
		t.Fatalf("the replay wrote %d times, want the one that closes the session: %v", len(written), source.SQL())
	}
	if !hasArgument(written[0], access.ReasonRefreshReplayed) {
		t.Fatalf("the session was not closed for the replay: %v", written[0])
	}

	denied.mu.Lock()
	until, listed := denied.revoked[session]
	denied.mu.Unlock()
	if !listed {
		t.Fatal("the session was closed but its outstanding access token was never denied, so it keeps working for its full lifetime")
	}
	if until.Before(moment.Add(spec.AccessTTL)) {
		t.Fatalf("the deny-list entry expires at %v, before the access token does at %v", until, moment.Add(spec.AccessTTL))
	}
}

// Reuse used to be detectable exactly one rotation back: the current digest and
// the previous one are two columns, so a credential from three rotations ago
// matched neither, was never found, and came back as an ordinary 401 that closed
// nothing. A thief who sat on a stolen credential was safer than one who used it
// at once.
func TestACredentialOlderThanThePreviousRotationIsStillAReplay(t *testing.T) {
	moment := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	session := uuid.New()
	stolen := "3." + session.String() + ".the-random-part-of-a-stolen-credential"

	source := &scriptedSessions{Recorder: crudtest.Postgres()}
	source.execs = []crud.Result{{RowsAffected: 1}}
	source.rows = [][]any{
		// Neither digest matches: the session has rotated twice since.
		nil,
		nil,
		rowAtGeneration(session, access.HashToken("the-current-one"), access.HashToken("the-previous-one"), moment, moment, 6),
	}

	config := access.Config{
		Session: access.SessionConfig{TTL: 24 * time.Hour, IdleTTL: time.Hour},
		Clock:   func() time.Time { return moment },
	}
	deps := testDeps(source, config)
	deps.Subject = access.Subject{Type: "user"}
	deps.Grants = access.NewGrants(deps.Store, access.MustDirectories(rotationDirectory{}))

	denied := &recordingRevocations{}
	spec := testSpec()
	spec.Revocation = denied
	issued, err := Strategy(spec).Build(deps)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := issued.Refresher.Refresh(t.Context(), stolen, access.Agent{}); err == nil {
		t.Fatal("a credential from three rotations ago was accepted")
	}

	written := updates(source)
	if len(written) != 1 || !hasArgument(written[0], access.ReasonRefreshReplayed) {
		t.Fatalf("the session was not closed for the replay: %v, %v", written, source.SQL())
	}
	denied.mu.Lock()
	_, listed := denied.revoked[session]
	denied.mu.Unlock()
	if !listed {
		t.Fatal("the session was closed but its access token was never denied")
	}
}

// The control that keeps the lookup honest: a credential naming a session it
// never came from must not be able to close that session. It matches no digest,
// so all it can name is a session id — which is a v4 UUID nobody was shown.
func TestACredentialNamingASessionItNeverCameFromClosesNothing(t *testing.T) {
	moment := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	session := uuid.New()
	forged := "1." + session.String() + ".invented"

	source := &scriptedSessions{Recorder: crudtest.Postgres()}
	source.rows = [][]any{
		nil,
		nil,
		// Only one rotation ahead of what the forgery claims, so it is not
		// superseded and there is nothing to conclude from it.
		rowAtGeneration(session, access.HashToken("the-current-one"), access.HashToken("the-previous-one"), moment, moment, 2),
	}

	config := access.Config{
		Session: access.SessionConfig{TTL: 24 * time.Hour, IdleTTL: time.Hour},
		Clock:   func() time.Time { return moment },
	}
	deps := testDeps(source, config)
	deps.Subject = access.Subject{Type: "user"}
	deps.Grants = access.NewGrants(deps.Store, access.MustDirectories(rotationDirectory{}))
	issued, err := Strategy(testSpec()).Build(deps)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := issued.Refresher.Refresh(t.Context(), forged, access.Agent{}); err == nil {
		t.Fatal("a forged credential was accepted")
	}
	if written := updates(source); len(written) != 0 {
		t.Fatalf("a forged credential closed a session: %v", written)
	}
}
