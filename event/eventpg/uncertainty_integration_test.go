//go:build integration

package eventpg

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/event"
)

var injected = errors.New("eventpg test: a driver failure carrying no SQLSTATE at all")

// The shape a driver's error has to have for the rule to read a code off it:
// sqlfault finds a SQLSTATE by this method or by a field of that name, so a
// case can name a code the live server will not send this suite on demand.
type stateError struct{ state string }

func (this stateError) Error() string { return "eventpg test: a driver failure carrying " + this.state }

func (this stateError) SQLState() string { return this.state }

// A row lock another session holds, so an append blocks where a cancellation, a
// timeout and a terminated backend can each be aimed at a statement that is
// really in flight.
func lockStream(t *testing.T, schema Schema, stream event.Stream) func() {
	t.Helper()
	tx, err := liveDB(t).BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("the session that holds the row lock could not begin: %v", err)
	}
	var release sync.Once
	releasing := func() { release.Do(func() { _ = tx.Rollback() }) }
	t.Cleanup(releasing)
	var version int64
	row := tx.QueryRowContext(context.Background(), "SELECT version FROM "+quoteIdentifier(schema.Name)+
		".streams WHERE family = $1 AND key = $2 FOR UPDATE", stream.Family, string(stream.Key))
	if err := row.Scan(&version); err != nil {
		releasing()
		t.Fatalf("the row of %v could not be locked, so nothing below would have blocked: %v", stream, err)
	}
	return releasing
}

func executeOn(t *testing.T, pool *sql.DB, statement string) {
	t.Helper()
	if _, err := pool.ExecContext(t.Context(), statement); err != nil {
		t.Fatalf("%.60q on this test's own pool answered %v", statement, err)
	}
}

func backendPID(t *testing.T, pool *sql.DB) int64 {
	t.Helper()
	var pid int64
	if err := pool.QueryRowContext(t.Context(), "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatalf("the backend this pool talks to could not be named: %v", err)
	}
	return pid
}

// A stream at version 1 through the store itself, and the token its next append
// is admitted at. The setup goes through the store rather than through an INSERT
// so the cases below fail at the append and never at the arrangement.
func standing(t *testing.T, repo *event.Repo[held, string], fact *event.Fact[held, string, held], id string) event.At[held] {
	t.Helper()
	at, _, err := repo.Append(t.Context(), loaded(t, t.Context(), repo, id), fact.New(id, held{Bytes: []byte("the history so far")}))
	if err != nil {
		t.Fatalf("the append that gives this case a stream answered %v", err)
	}
	return at
}

func sqlStateOf(err error) string {
	if fault := sqlfault.Extract(err); fault != nil {
		return fault.SQLState
	}
	return ""
}

// Retryability has two spellings and a caller may read either, so a code that
// carries it is asserted in both: the sentinel the kernel promotes to a wrap and
// the kind the fault under the cause declares. A store that answered one and not
// the other tells half its callers to give up on a failure that clears.
func retryableCause(t *testing.T, err error, state string) {
	t.Helper()
	if got := sqlStateOf(event.CauseOf(err)); got != state {
		t.Fatalf("the append answered SQLSTATE %q where this case arranged %q: %v", got, state, err)
	}
	if !errors.Is(err, event.ErrBackend) || !errors.Is(err, crud.ErrUnavailable) {
		t.Fatalf("a %s answered %v, where the code says the failure clears on its own and the caller may try the same append again", state, err)
	}
	if errors.Is(err, event.ErrUncertain) || errors.Is(err, event.ErrConflict) {
		t.Fatalf("a %s answered %v, which is either a recovery it does not need or a reload that would not help", state, err)
	}
	fault, carried := errs.AsFault(event.CauseOf(err))
	if !carried || fault.Kind != errs.KindRetryable {
		t.Fatalf("the cause of a %s is %v, and a caller reads retryability off an errs.Fault as readily as off the sentinel", state, event.CauseOf(err))
	}
}

func TestEachCancellationWindowIsAnsweredByTheRuleAndNotByOneOutcome(t *testing.T) {
	schema := sharedSchema(t)
	ctx := t.Context()

	t.Run("a context already cancelled on entry is the bare sentinel and zero events", func(t *testing.T) {
		const family = "eventpg.s3.cancel.entry"
		repo, fact := boundRepo(t, prepared(t, schema), family)
		at := loaded(t, ctx, repo, "A-entry")
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		_, _, err := repo.Append(cancelled, at, fact.New("A-entry", held{Bytes: []byte("never issued")}))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("an append on a context that was already done answered %v", err)
		}
		for _, classified := range []error{event.ErrUncertain, event.ErrBackend, event.ErrConflict} {
			if errors.Is(err, classified) {
				t.Errorf("the cancellation was reported as %v, and nothing was issued for anybody to be uncertain about", classified)
			}
		}
		if got := len(stored(t, schema, aStream(family, "A-entry"))); got != 0 {
			t.Fatalf("the stream holds %d events after an append on a done context", got)
		}
	})

	t.Run("a server-side cancellation is a code the backend sent while it stayed alive", func(t *testing.T) {
		const family = "eventpg.s3.cancel.timeout"
		store := countingStore(t, schema, 1)
		repo, fact := boundRepo(t, store, family)
		at := standing(t, repo, fact, "A-timeout")
		release := lockStream(t, schema, aStream(family, "A-timeout"))
		executeOn(t, store.db, "SET statement_timeout = '400ms'")

		err := errorOfAppend(t, repo, fact, at, "A-timeout")
		release()
		executeOn(t, store.db, "SET statement_timeout = 0")

		if got := sqlStateOf(event.CauseOf(err)); got != "57014" {
			t.Fatalf("the statement that ran out of time answered SQLSTATE %q where a cancelled query is 57014: %v", got, err)
		}
		if !errors.Is(err, event.ErrBackend) || errors.Is(err, event.ErrUncertain) {
			t.Fatalf("a 57014 answered %v, and a cancel is refused inside the commit-critical section so this one did not commit", err)
		}
		if err := store.db.PingContext(ctx); err != nil {
			t.Fatalf("the backend did not survive the cancellation, so the answer above was not one it sent while alive: %v", err)
		}
		if got := len(stored(t, schema, aStream(family, "A-timeout"))); got != 1 {
			t.Fatalf("the stream holds %d events where only the one this case set up should be there", got)
		}
	})

	t.Run("a client-side cancellation is answered by the rule and not by one outcome", func(t *testing.T) {
		const family = "eventpg.s3.cancel.client"
		store := countingStore(t, schema, 1)
		repo, fact := boundRepo(t, store, family)
		at := standing(t, repo, fact, "A-client")
		release := lockStream(t, schema, aStream(family, "A-client"))

		cancelling, cancel := context.WithCancel(ctx)
		go func() {
			time.Sleep(300 * time.Millisecond)
			cancel()
		}()
		_, _, err := repo.Append(cancelling, at, fact.New("A-client", held{Bytes: []byte("cancelled in flight")}))
		release()

		switch state := sqlStateOf(event.CauseOf(err)); state {
		case "":
			if !errors.Is(err, event.ErrUncertain) {
				t.Fatalf("no SQLSTATE came back and the store answered %v, where the client stopped waiting and the server may have committed", err)
			}
		case "57014":
			if !errors.Is(err, event.ErrBackend) || errors.Is(err, event.ErrUncertain) {
				t.Fatalf("the backend answered 57014 and the store answered %v, where a code it sent while alive is proof the statement rolled back", err)
			}
		default:
			t.Fatalf("a cancelled append answered SQLSTATE %q, which is neither of the two observations this window has: %v", state, err)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, event.ErrConflict) {
			t.Fatalf("the append answered %v, and a statement that was issued is never reported as a plain cancellation or as a conflict", err)
		}
	})

	t.Run("the uncancelled append in the same arrangement lands", func(t *testing.T) {
		const family = "eventpg.s3.cancel.control"
		store := countingStore(t, schema, 1)
		repo, fact := boundRepo(t, store, family)
		at := standing(t, repo, fact, "A-uncancelled")
		if _, _, err := repo.Append(ctx, at, fact.New("A-uncancelled", held{Bytes: []byte("admitted")})); err != nil {
			t.Fatalf("the same append with nobody cancelling anything answered %v", err)
		}
		if got := len(stored(t, schema, aStream(family, "A-uncancelled"))); got != 2 {
			t.Fatalf("the stream holds %d events where the setup and this append make two", got)
		}
	})
}

func errorOfAppend(t *testing.T, repo *event.Repo[held, string], fact *event.Fact[held, string, held], at event.At[held], id string) error {
	t.Helper()
	_, _, err := repo.Append(t.Context(), at, fact.New(id, held{Bytes: []byte("in flight")}))
	if err == nil {
		t.Fatal("the append this case is built to interrupt was admitted, so nothing below was measured")
	}
	return err
}

func TestAKilledBackendAnswersUncertainAfterExactlyOneExecContext(t *testing.T) {
	const family = "eventpg.s3.killed"
	schema := sharedSchema(t)
	store := countingStore(t, schema, 1)
	repo, fact := boundRepo(t, store, family)
	stream := aStream(family, "A-killed")
	at := standing(t, repo, fact, "A-killed")

	pid := backendPID(t, store.db)
	release := lockStream(t, schema, stream)
	recount(t)
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = execute(t, "SELECT pg_terminate_backend($1)", pid)
	}()
	err := errorOfAppend(t, repo, fact, at, "A-killed")
	release()

	if !errors.Is(err, event.ErrUncertain) {
		t.Fatalf("an append whose backend was terminated under it answered %v, and the statement's own commit is exactly what was never acknowledged", err)
	}
	if errors.Is(err, event.ErrConflict) || errors.Is(err, context.Canceled) || errors.Is(err, crud.ErrUnavailable) {
		t.Errorf("the uncertain append answered %v, which reads as something a caller may act on", err)
	}
	if execs, _ := counted(t); execs != 1 {
		t.Fatalf("one append reached the driver %d times, and an append issued on the *sql.DB is retried on three connections", execs)
	}
	cause := event.CauseOf(err)
	if cause == nil {
		t.Fatal("the uncertain append carries no cause at all, so an operator has nothing to read")
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Errorf("the rendered refusal is %q and carries the driver's own text, which names the key and the version it was writing", err)
	}
	if got := len(stored(t, schema, stream)); got != 1 {
		t.Fatalf("the stream holds %d events where only the one this case set up should be there, so the store guessed rather than reporting what it could not confirm", got)
	}
}

func TestABadConnBeforeTheSendIsOneCallAndNotWritten(t *testing.T) {
	const family = "eventpg.s3.badconn"
	schema := sharedSchema(t)
	store := countingStore(t, schema, 2)
	repo, fact := boundRepo(t, store, family)
	stream := aStream(family, "A-badconn")
	at := loaded(t, t.Context(), repo, "A-badconn")

	recount(t)
	arm(t, 1, false, driver.ErrBadConn)
	_, _, err := repo.Append(t.Context(), at, fact.New("A-badconn", held{Bytes: []byte("never sent")}))

	if !errors.Is(err, event.ErrBackend) || errors.Is(err, event.ErrUncertain) {
		t.Fatalf("a driver certain the server never saw the query answered %v, and its contract permits ErrBadConn only when it is certain", err)
	}
	if execs, _ := counted(t); execs != 1 {
		t.Fatalf("the append reached the driver %d times", execs)
	}
	if got := len(stored(t, schema, stream)); got != 0 {
		t.Fatalf("the stream holds %d events after a statement that was never sent", got)
	}

	t.Run("the same driver error through the pool is three calls, so the count above is the store's own path", func(t *testing.T) {
		recount(t)
		arm(t, 3, false, driver.ErrBadConn)
		if _, err := store.db.ExecContext(t.Context(), "SELECT 1"); !errors.Is(err, driver.ErrBadConn) {
			t.Fatalf("the pool answered %v for an armed driver", err)
		}
		if execs, _ := counted(t); execs != 3 {
			t.Fatalf("(*sql.DB).ExecContext issued %d statements for one call, and the measurement the store's own path rests on is that this number is not one", execs)
		}
	})
}

func TestTheRetryWithTheSameTokenResolvesTheUncertainty(t *testing.T) {
	const family = "eventpg.s3.retry"
	schema := sharedSchema(t)
	store := countingStore(t, schema, 2)
	repo, fact := boundRepo(t, store, family)
	ctx := t.Context()

	t.Run("the retry conflicts when the first attempt had landed", func(t *testing.T) {
		id, stream := "A-landed", aStream(family, "A-landed")
		at := loaded(t, ctx, repo, id)
		arm(t, 1, true, injected)
		_, _, err := repo.Append(ctx, at, fact.New(id, held{Bytes: []byte("written once")}))
		if !errors.Is(err, event.ErrUncertain) {
			t.Fatalf("a statement that was written and whose answer was lost answered %v", err)
		}
		if _, _, retry := repo.Append(ctx, at, fact.New(id, held{Bytes: []byte("written once")})); !errors.Is(retry, event.ErrConflict) {
			t.Fatalf("the retry at the same token answered %v where the first attempt had landed", retry)
		}
		if got := payloads(stored(t, schema, stream)); len(got) != 1 {
			t.Fatalf("the stream holds %v, so the retry the caller was told to make wrote the decision twice", got)
		}
	})

	t.Run("the retry is admitted when the first attempt had not landed", func(t *testing.T) {
		id, stream := "A-lost", aStream(family, "A-lost")
		at := loaded(t, ctx, repo, id)
		arm(t, 1, false, injected)
		_, _, err := repo.Append(ctx, at, fact.New(id, held{Bytes: []byte("written once")}))
		if !errors.Is(err, event.ErrUncertain) {
			t.Fatalf("a statement whose fate the store cannot pin down answered %v", err)
		}
		if _, _, retry := repo.Append(ctx, at, fact.New(id, held{Bytes: []byte("written once")})); retry != nil {
			t.Fatalf("the retry at the same token answered %v where the first attempt had not landed", retry)
		}
		if got := payloads(stored(t, schema, stream)); len(got) != 1 {
			t.Fatalf("the stream holds %v after one lost attempt and one retry", got)
		}
	})
}

func TestEverySQLStateThisStoreNamesSelectsTheOutcomeTheRuleRequires(t *testing.T) {
	const family = "eventpg.s3.states"
	schema := sharedSchema(t)
	ctx := t.Context()

	// The store's own answer rather than the kernel's rendering of it, because
	// what this table is about is which of the seven outcomes was selected: two
	// of them render as one sentinel and the whole rule lives in telling them
	// apart.
	appending := func(store *Store, stream event.Stream, expected event.Version) error {
		return store.Append(ctx, event.AppendRequest{
			Stream:   stream,
			Expected: expected,
			Records:  []event.Record{{Type: family + ".held", Revision: 1, Payload: []byte("a record")}},
		})
	}
	arranged := func(t *testing.T, store *Store, id string) event.Stream {
		t.Helper()
		stream := aStream(family, id)
		if err := appending(store, stream, 0); err != nil {
			t.Fatalf("the append that gives this case a stream answered %v", err)
		}
		return stream
	}

	t.Run("a unique violation is a code the backend sent while alive", func(t *testing.T) {
		store := prepared(t, schema)
		stream := arranged(t, store, "A-23505")
		mustExecute(t, "INSERT INTO "+quoteIdentifier(schema.Name)+
			".events (family, key, version, type, revision, payload, recorded_at) VALUES ($1, $2, 2, $3, 1, $4, statement_timestamp())",
			family, string(stream.Key), family+".held", []byte("planted past this package"))

		classifiedAs(t, appending(store, stream, 1), event.NotWritten, "an append the unique index refused")
	})

	// A serialisation failure on the path where the store has no transaction of
	// anybody's to shelter behind: the session default is the level an autocommit
	// statement runs at, so this drives the SQLSTATE branch of the rule rather
	// than the joined one, which answers NotWritten before a code is looked at.
	t.Run("a serialisation failure on the autocommit path is not written and is retryable", func(t *testing.T) {
		const racing = family + ".serialisation"
		store := countingStore(t, schema, 1)
		repo, fact := boundRepo(t, store, racing)
		executeOn(t, store.db, "SET default_transaction_isolation = 'repeatable read'")
		at := standing(t, repo, fact, "A-40001")
		stream := aStream(racing, "A-40001")

		holding, err := liveDB(t).BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := holding.ExecContext(context.Background(), "UPDATE "+quoteIdentifier(schema.Name)+
			".streams SET version = version WHERE family = $1 AND key = $2", racing, string(stream.Key)); err != nil {
			t.Fatalf("the writer this case loses to answered %v", err)
		}
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = holding.Commit()
		}()
		_, _, refused := repo.Append(ctx, at, fact.New("A-40001", held{Bytes: []byte("the loser")}))
		executeOn(t, store.db, "SET default_transaction_isolation = 'read committed'")

		if !errors.Is(refused, event.ErrBackend) || !errors.Is(refused, crud.ErrUnavailable) {
			t.Fatalf("an append that lost a race at REPEATABLE READ answered %v, where the code says the transaction is dead and the caller may retry it whole", refused)
		}
		if errors.Is(refused, event.ErrConflict) || errors.Is(refused, event.ErrUncertain) {
			t.Fatalf("the loser answered %v, which is either a reload it cannot do or a recovery it does not need", refused)
		}
		if got := len(stored(t, schema, stream)); got != 1 {
			t.Fatalf("the stream holds %d events where the loser wrote none of them", got)
		}
	})

	// A lock the append gave up waiting for, from the same arrangement the
	// cancellation cases use: 55P03 is a code the server sent while alive, so the
	// outcome is the same NotWritten a unique violation gets, and the cause is
	// the one a caller may act on by issuing the identical append again.
	t.Run("a lock the append waited too long for is not written and is retryable", func(t *testing.T) {
		const blocked = family + ".locktimeout"
		store := countingStore(t, schema, 1)
		repo, fact := boundRepo(t, store, blocked)
		at := standing(t, repo, fact, "A-55P03")
		stream := aStream(blocked, "A-55P03")

		release := lockStream(t, schema, stream)
		executeOn(t, store.db, "SET lock_timeout = '400ms'")
		refused := errorOfAppend(t, repo, fact, at, "A-55P03")
		release()
		executeOn(t, store.db, "SET lock_timeout = 0")

		retryableCause(t, refused, "55P03")
		if got := len(stored(t, schema, stream)); got != 1 {
			t.Fatalf("the stream holds %d events where the append that timed out on the lock wrote none of them", got)
		}
	})

	// A cycle the server has to break: the append takes the stream row and then
	// waits on an event row another session inserted and has not committed, while
	// that session waits for the stream row the append is holding. The append
	// enters the wait first, so its own deadlock_timeout is the one that expires
	// with the cycle complete, and it is the statement the server aborts.
	t.Run("a deadlock the backend broke is not written and is retryable", func(t *testing.T) {
		const racing = family + ".deadlock"
		store := prepared(t, schema)
		repo, fact := boundRepo(t, store, racing)
		at := standing(t, repo, fact, "A-40P01")
		stream := aStream(racing, "A-40P01")

		holding, err := liveDB(t).BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = holding.Rollback() })
		if _, err := holding.ExecContext(context.Background(), "INSERT INTO "+quoteIdentifier(schema.Name)+
			".events (family, key, version, type, revision, payload, recorded_at) VALUES ($1, $2, 2, $3, 1, $4, statement_timestamp())",
			racing, string(stream.Key), racing+".held", []byte("the row the append blocks on")); err != nil {
			t.Fatalf("the session this case deadlocks against answered %v", err)
		}

		// The rollback belongs to the same goroutine, so a run where the server
		// picks the other session as its victim releases the append and fails on
		// what it answered rather than hanging until the suite times out.
		contended := make(chan struct{})
		go func() {
			defer close(contended)
			time.Sleep(300 * time.Millisecond)
			_, _ = holding.ExecContext(context.Background(), "UPDATE "+quoteIdentifier(schema.Name)+
				".streams SET version = version WHERE family = $1 AND key = $2", racing, string(stream.Key))
			_ = holding.Rollback()
		}()
		refused := errorOfAppend(t, repo, fact, at, "A-40P01")
		<-contended

		retryableCause(t, refused, "40P01")
		if got := len(stored(t, schema, stream)); got != 1 {
			t.Fatalf("the stream holds %d events where the deadlocked append wrote none of them", got)
		}
	})

	t.Run("a statement the backend cancelled is not written", func(t *testing.T) {
		store := countingStore(t, schema, 1)
		stream := arranged(t, store, "A-57014")
		release := lockStream(t, schema, stream)
		executeOn(t, store.db, "SET statement_timeout = '400ms'")
		err := appending(store, stream, 1)
		release()
		executeOn(t, store.db, "SET statement_timeout = 0")
		classifiedAs(t, err, event.NotWritten, "an append the backend cancelled")
	})

	t.Run("a checkout that never reached a server is not written", func(t *testing.T) {
		pool, err := sql.Open("pgx", os.Getenv(testDSN))
		if err != nil {
			t.Fatal(err)
		}
		store, err := New(Spec{DB: pool, Source: crudsql.Postgres(pool), Schema: schema})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Prepare(ctx); err != nil {
			t.Fatalf("a store over a pool of its own did not verify the deployed schema: %v", err)
		}
		stream := arranged(t, store, "A-checkout")
		if err := pool.Close(); err != nil {
			t.Fatal(err)
		}
		classifiedAs(t, appending(store, stream, 1), event.NotWritten, "an append whose connection was never checked out")
	})

	t.Run("an error carrying no SQLSTATE at all is unconfirmed", func(t *testing.T) {
		store := countingStore(t, schema, 2)
		stream := arranged(t, store, "A-nostate")
		arm(t, 1, true, injected)
		err := appending(store, stream, 1)
		classifiedAs(t, err, event.Unconfirmed, "an append whose driver answered without a SQLSTATE")
		if got := len(stored(t, schema, stream)); got != 2 {
			t.Fatalf("the stream holds %d events where the statement was written and only the answer was lost, so an implementation with a NotWritten default would be saying the write did not land about a write that did", got)
		}
	})

	// pgx reports a connection that broke under it as a Go error with no
	// SQLSTATE, so class 08 reaches the rule only from a server that named the
	// code and then stopped talking, which no case can arrange against a live
	// one. 08007 is transaction_resolution_unknown, whose whole meaning is that
	// nobody knows whether the statement committed: read as a code the backend
	// sent while it stayed alive, it answers NotWritten to exactly the failure
	// that is not.
	t.Run("a connection-class code is unconfirmed however the backend named it", func(t *testing.T) {
		for _, state := range []string{"08006", "08007"} {
			t.Run(state, func(t *testing.T) {
				store := countingStore(t, schema, 2)
				stream := arranged(t, store, "A-"+state)
				arm(t, 1, true, stateError{state: state})

				classifiedAs(t, appending(store, stream, 1), event.Unconfirmed, "an append answered "+state)
				if got := len(stored(t, schema, stream)); got != 2 {
					t.Fatalf("the stream holds %d events where the statement was written and only its answer was lost, so a store that reads %s as a backend that survived says the write did not land about a write that did", got, state)
				}
			})
		}
	})

	t.Run("a row count the store cannot account for is unclassified", func(t *testing.T) {
		for _, answer := range []struct {
			name   string
			result driver.Result
		}{
			{"a count that is neither the batch nor zero", countingResult{affected: 99}},
			{"a count the driver could not give at all", countingResult{err: errors.New("eventpg test: this driver counts nothing")}},
		} {
			t.Run(answer.name, func(t *testing.T) {
				store := countingStore(t, schema, 2)
				stream := arranged(t, store, "A-rows-"+strings.ReplaceAll(answer.name, " ", "-"))
				armResult(t, answer.result)
				classifiedAs(t, appending(store, stream, 1), event.Unclassified,
					"an append whose row count says nothing about what it wrote")
			})
		}
	})

	t.Run("a lost race with no failure at all is a conflict", func(t *testing.T) {
		store := prepared(t, schema)
		stream := arranged(t, store, "A-conflict")
		classifiedAs(t, appending(store, stream, 0), event.Conflict, "an append at a version the stream has left")
	})
}
