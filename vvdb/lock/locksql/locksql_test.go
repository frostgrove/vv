package locksql_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/frostgrove/vv/vvdb/lock"
	"github.com/frostgrove/vv/vvdb/lock/locksql"
)

// A database/sql driver that answers the two advisory-lock statements and records what it was
// asked. Registering one is what lets these tests reach Hold's connection handling — the pin, the
// poison and the unlock check — without a server, which is the whole of what Hold does that the
// integration suites in eventpg and jobspg cannot isolate.
type stubDriver struct{ shared *stubState }

type stubState struct {
	mu           sync.Mutex
	statements   []string
	closed       int
	opened       int
	takeAnswers  []bool
	unlockAnswer bool
	unlockFails  error
}

func (this *stubDriver) Open(string) (driver.Conn, error) {
	this.shared.mu.Lock()
	this.shared.opened++
	this.shared.mu.Unlock()
	return &stubConn{state: this.shared}, nil
}

type stubConn struct{ state *stubState }

func (this *stubConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not used") }
func (this *stubConn) Begin() (driver.Tx, error)           { return stubTx{}, nil }

func (this *stubConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	this.state.mu.Lock()
	defer this.state.mu.Unlock()
	this.state.statements = append(this.state.statements, query)
	return driver.RowsAffected(0), nil
}

type stubTx struct{}

func (stubTx) Commit() error   { return nil }
func (stubTx) Rollback() error { return nil }

func (this *stubConn) Close() error {
	this.state.mu.Lock()
	this.state.closed++
	this.state.mu.Unlock()
	return nil
}

func (this *stubConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	this.state.mu.Lock()
	defer this.state.mu.Unlock()
	this.state.statements = append(this.state.statements, query)
	switch {
	case strings.Contains(query, "pg_try_advisory_xact_lock"), strings.Contains(query, "pg_try_advisory_lock"):
		answer := true
		if len(this.state.takeAnswers) > 0 {
			answer, this.state.takeAnswers = this.state.takeAnswers[0], this.state.takeAnswers[1:]
		}
		return &boolRows{value: answer}, nil
	case strings.Contains(query, "pg_advisory_unlock"):
		if this.state.unlockFails != nil {
			return nil, this.state.unlockFails
		}
		return &boolRows{value: this.state.unlockAnswer}, nil
	}
	return nil, errors.New("unexpected statement: " + query)
}

type boolRows struct {
	value bool
	done  bool
}

func (this *boolRows) Columns() []string { return []string{"answer"} }
func (this *boolRows) Close() error      { return nil }

func (this *boolRows) Next(dest []driver.Value) error {
	if this.done {
		return io.EOF
	}
	this.done = true
	dest[0] = this.value
	return nil
}

var registered sync.Once

func openStub(t *testing.T, state *stubState) *sql.DB {
	t.Helper()
	registered.Do(func() { sql.Register("locksql-stub", &stubDriver{shared: sharedState}) })
	sharedState.replace(state)
	db, err := sql.Open("locksql-stub", "")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxIdleConns(0)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// One driver value is registered for the process, so the state it answers from is swapped per test
// rather than the driver being registered again under a new name.
var sharedState = &stubState{}

func (this *stubState) replace(with *stubState) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.statements = nil
	this.closed = 0
	this.opened = 0
	this.takeAnswers = with.takeAnswers
	this.unlockAnswer = with.unlockAnswer
	this.unlockFails = with.unlockFails
}

func (this *stubState) sql() []string {
	this.mu.Lock()
	defer this.mu.Unlock()
	return append([]string(nil), this.statements...)
}

func TestHoldTakesTheLockRunsTheWorkAndReleasesIt(t *testing.T) {
	db := openStub(t, &stubState{unlockAnswer: true})

	var ran bool
	err := locksql.Hold(t.Context(), db, lock.KeyFrom(7), func(conn *sql.Conn) error {
		ran = true
		if conn == nil {
			t.Fatal("the work was handed no connection, so it cannot be the one holding the lock")
		}
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("the work never ran")
	}
	statements := sharedState.sql()
	if len(statements) != 2 ||
		!strings.Contains(statements[0], "pg_try_advisory_lock") ||
		!strings.Contains(statements[1], "pg_advisory_unlock") {
		t.Fatalf("Hold ran %v, want a try then an unlock", statements)
	}
}

func TestHoldWaitsUntilTheLockIsFree(t *testing.T) {
	db := openStub(t, &stubState{takeAnswers: []bool{false, false, true}, unlockAnswer: true})

	if err := locksql.Hold(t.Context(), db, lock.KeyFrom(7), func(*sql.Conn) error { return nil }); err != nil {
		t.Fatal(err)
	}

	var tries int
	for _, statement := range sharedState.sql() {
		if strings.Contains(statement, "pg_try_advisory_lock") {
			tries++
		}
	}
	if tries != 3 {
		t.Fatalf("Hold tried %d times; a caller that gives up on the first refusal serialises nothing", tries)
	}
}

func TestHoldStopsWaitingWhenTheCallerGivesUp(t *testing.T) {
	db := openStub(t, &stubState{takeAnswers: []bool{false, false, false, false}, unlockAnswer: true})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := locksql.Hold(ctx, db, lock.KeyFrom(7), func(*sql.Conn) error {
		t.Fatal("the work ran without the lock")
		return nil
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled caller was answered %v", err)
	}
}

// An unlock that answers false means this session never held it, so whatever ran under it ran
// unprotected. Reporting that is the only way anyone finds out.
func TestAnUnlockThatAnswersFalseIsAnError(t *testing.T) {
	db := openStub(t, &stubState{unlockAnswer: false})

	err := locksql.Hold(t.Context(), db, lock.KeyFrom(7), func(*sql.Conn) error { return nil })

	if !errors.Is(err, locksql.ErrNotHeld) {
		t.Fatalf("an unlock answering false was reported as %v, want ErrNotHeld", err)
	}

	// The control: the same call with a truthful unlock reports nothing, so the assertion above
	// cannot pass by Hold failing for some other reason.
	db = openStub(t, &stubState{unlockAnswer: true})
	if err := locksql.Hold(t.Context(), db, lock.KeyFrom(7), func(*sql.Conn) error { return nil }); err != nil {
		t.Fatalf("a truthful unlock still reported %v", err)
	}
}

func TestTheWorkFailureSurvivesTheRelease(t *testing.T) {
	db := openStub(t, &stubState{unlockAnswer: true})
	refused := errors.New("the migration refused")

	err := locksql.Hold(t.Context(), db, lock.KeyFrom(7), func(*sql.Conn) error { return refused })

	if !errors.Is(err, refused) {
		t.Fatalf("Hold answered %v; the work's own failure was lost behind the release", err)
	}
}

// Take and TryTake reach lock.Locks through a crudsql wrapper around a *sql.Tx. If that wrapper is
// not seen as a transaction, every caller gets ErrNoTransaction instead of a lock — a failure that
// only shows up against a real server, because nothing else in this package asks the question.
func TestATransactionWrappedForTheLockPackageIsSeenAsOne(t *testing.T) {
	db := openStub(t, &stubState{})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := locksql.Take(t.Context(), tx, lock.Policy{}, lock.Exclusively(lock.KeyFrom(3))); err != nil {
		t.Fatalf("taking a lock in a wrapped transaction answered %v", err)
	}

	statements := sharedState.sql()
	if len(statements) != 1 || !strings.Contains(statements[0], "pg_advisory_xact_lock") {
		t.Fatalf("Take ran %v, want the transactional advisory lock", statements)
	}
}

func TestTryTakeReadsTheAnswerThroughTheWrapper(t *testing.T) {
	db := openStub(t, &stubState{takeAnswers: []bool{false}})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	taken, err := locksql.TryTake(t.Context(), tx, lock.Exclusively(lock.KeyFrom(3)))

	if err != nil {
		t.Fatalf("trying a lock in a wrapped transaction answered %v", err)
	}
	if taken {
		t.Fatal("the engine said the lock was held and TryTake reported it taken")
	}
}
