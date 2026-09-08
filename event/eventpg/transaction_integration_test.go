//go:build integration

package eventpg

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/event"
)

// A transaction of the store's own data source, bound the way a consumer binds
// one. The store's Source is asked for its beginner rather than asserted to one,
// because a value reached through a decorator has lost every method its own
// interface does not name.
func begin(t *testing.T, store *Store, options *sql.TxOptions) (context.Context, crud.Tx) {
	t.Helper()
	beginner, found := crud.BeginnerOf(crudsql.Postgres(store.db).WithTxOptions(options))
	if !found {
		t.Fatal("the store's own data source cannot begin a transaction, so nothing below could bind one")
	}
	tx, err := beginner.Begin(t.Context())
	if err != nil {
		t.Fatalf("beginning a transaction on the store's own data source answered %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) })
	return crud.BindExecutor(t.Context(), store.source, tx), tx
}

func transactionOf(t *testing.T, ctx context.Context, store *Store) *sql.Tx {
	t.Helper()
	tx, bound := crudsql.TransactionFor(ctx, store.source)
	if !bound {
		t.Fatal("no transaction of this store's data source is bound on this context, so the reads below would run on the pool")
	}
	return tx
}

func callerTable(t *testing.T, name string) string {
	t.Helper()
	mustExecute(t, "DROP TABLE IF EXISTS "+quoteIdentifier(name))
	mustExecute(t, "CREATE TABLE "+quoteIdentifier(name)+" (note text NOT NULL)")
	t.Cleanup(func() { _ = execute(t, "DROP TABLE IF EXISTS "+quoteIdentifier(name)) })
	return name
}

func callerNotes(t *testing.T, on executor, table string) []string {
	t.Helper()
	rows, err := on.QueryContext(t.Context(), "SELECT note FROM "+quoteIdentifier(table))
	if err != nil {
		t.Fatalf("the caller's own rows could not be read: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var held []string
	for rows.Next() {
		var note string
		if err := rows.Scan(&note); err != nil {
			t.Fatalf("the caller's own rows could not be read: %v", err)
		}
		held = append(held, note)
	}
	return held
}

func TestAnAppendJoinsTheBoundTransactionAndNothingOutsideItSees(t *testing.T) {
	const family = "eventpg.s3.joined"
	schema := sharedSchema(t)
	store := prepared(t, schema)
	repo, fact := boundRepo(t, store, family)
	table := callerTable(t, "eventpg_s3_joined")
	stream := aStream(family, "A-joined")

	inside, tx := begin(t, store, nil)
	authority, err := store.Transaction(inside)
	if err != nil || !authority.Valid() {
		t.Fatalf("the store answers (%v, %v) for a context carrying a transaction of its own data source", authority, err)
	}
	joined := transactionOf(t, inside, store)
	if _, err := joined.ExecContext(inside, "INSERT INTO "+quoteIdentifier(table)+" (note) VALUES ($1)", "the caller's own row"); err != nil {
		t.Fatalf("the caller's own write inside its transaction answered %v", err)
	}

	at := loaded(t, inside, repo, "A-joined")
	_, receipt, err := repo.Append(inside, at, fact.New("A-joined", held{Bytes: []byte("staged")}))
	if err != nil {
		t.Fatalf("an append inside the caller's transaction answered %v", err)
	}
	if !receipt.Authority().Same(authority) {
		t.Error("the receipt names another transaction than the one the context carries")
	}

	if got := payloads(storedOn(t, joined, schema, stream)); len(got) != 1 || got[0] != "staged" {
		t.Fatalf("a read inside the transaction that wrote them sees %v", got)
	}
	if got := payloads(stored(t, schema, stream)); len(got) != 0 {
		t.Fatalf("the pool sees %v before the caller committed, so the append did not run inside the transaction", got)
	}
	if _, found := streamVersion(t, schema, stream); found {
		t.Error("the pool sees the stream row before the caller committed")
	}

	if err := tx.Commit(inside); err != nil {
		t.Fatalf("committing answered %v", err)
	}
	if got := payloads(stored(t, schema, stream)); len(got) != 1 || got[0] != "staged" {
		t.Fatalf("the pool sees %v after the caller committed", got)
	}
	if got := callerNotes(t, liveDB(t), table); len(got) != 1 {
		t.Fatalf("the caller's own rows are %v after the commit that carried the events", got)
	}

	t.Run("the same operation with nothing bound writes immediately", func(t *testing.T) {
		beside := aStream(family, "A-immediate")
		at := loaded(t, t.Context(), repo, "A-immediate")
		if _, _, err := repo.Append(t.Context(), at, fact.New("A-immediate", held{Bytes: []byte("at once")})); err != nil {
			t.Fatalf("an append with nothing bound answered %v", err)
		}
		if got := payloads(stored(t, schema, beside)); len(got) != 1 {
			t.Fatalf("the pool sees %v right after an append nothing was bound for, so a store that always opened its own transaction would pass the case above for the wrong reason", got)
		}
	})
}

func TestARollbackLeavesNoFragmentAndBurnsThePositions(t *testing.T) {
	const family = "eventpg.s3.rollback"
	schema := sharedSchema(t)
	store := prepared(t, schema)
	repo, fact := boundRepo(t, store, family)
	fresh, existing := aStream(family, "A-fresh"), aStream(family, "A-existing")
	ctx := t.Context()

	before := loaded(t, ctx, repo, "A-existing")
	committed, _, err := repo.Append(ctx, before, fact.New("A-existing", held{Bytes: []byte("committed")}))
	if err != nil {
		t.Fatalf("the append that gives this case a stream to move answered %v", err)
	}

	inside, tx := begin(t, store, nil)
	joined := transactionOf(t, inside, store)
	if _, _, err := repo.Append(inside, loaded(t, inside, repo, "A-fresh"), fact.New("A-fresh", held{Bytes: []byte("discarded")})); err != nil {
		t.Fatalf("an append to a stream created inside the transaction answered %v", err)
	}
	if _, _, err := repo.Append(inside, committed, fact.New("A-existing", held{Bytes: []byte("discarded")})); err != nil {
		t.Fatalf("an append to a stream that existed before the transaction answered %v", err)
	}
	var burnt int64
	for _, row := range append(storedOn(t, joined, schema, fresh), storedOn(t, joined, schema, existing)...) {
		burnt = max(burnt, row.position)
	}
	if burnt == 0 {
		t.Fatal("the transaction drew no position at all, so there is nothing for the rollback to burn")
	}
	if err := tx.Rollback(inside); err != nil {
		t.Fatalf("rolling back answered %v", err)
	}

	if got := stored(t, schema, fresh); len(got) != 0 {
		t.Errorf("the rolled-back transaction left %d events on a stream it created", len(got))
	}
	if _, found := streamVersion(t, schema, fresh); found {
		t.Error("the rolled-back transaction left a streams row with no events under it")
	}
	if got := payloads(stored(t, schema, existing)); len(got) != 1 || got[0] != "committed" {
		t.Errorf("the stream that existed before the transaction holds %v", got)
	}
	if version, _ := streamVersion(t, schema, existing); version != 1 {
		t.Errorf("the stream that existed before the transaction is at version %d, so a rolled-back advance survived with no events under it", version)
	}

	t.Run("the positions the rollback burnt are never reissued", func(t *testing.T) {
		at := loaded(t, ctx, repo, "A-existing")
		if _, _, err := repo.Append(ctx, at, fact.New("A-existing", held{Bytes: []byte("afterwards")})); err != nil {
			t.Fatalf("the append after the rollback answered %v", err)
		}
		rows := stored(t, schema, existing)
		if len(rows) != 2 {
			t.Fatalf("the stream holds %d events after the rollback and one more append", len(rows))
		}
		if rows[1].position <= burnt {
			t.Fatalf("the append after the rollback took position %d where the rolled-back one had reached %d, so a position was reused", rows[1].position, burnt)
		}
	})

	t.Run("the committed variant leaves both halves", func(t *testing.T) {
		kept := aStream(family, "A-kept")
		inside, tx := begin(t, store, nil)
		if _, _, err := repo.Append(inside, loaded(t, inside, repo, "A-kept"), fact.New("A-kept", held{Bytes: []byte("kept")})); err != nil {
			t.Fatalf("the append inside the transaction that commits answered %v", err)
		}
		if err := tx.Commit(inside); err != nil {
			t.Fatalf("committing answered %v", err)
		}
		version, found := streamVersion(t, schema, kept)
		if !found || version != 1 || len(stored(t, schema, kept)) != 1 {
			t.Fatalf("the committed transaction left version %d (present: %v) over %d events, so the assertions above hold for a transaction that writes nothing either way",
				version, found, len(stored(t, schema, kept)))
		}
	})
}

func TestTheIsolationMatrixTellsAConflictFromASerialisationFailure(t *testing.T) {
	const family = "eventpg.s3.isolation"
	schema := sharedSchema(t)
	store := prepared(t, schema)
	repo, fact := boundRepo(t, store, family)
	ctx := t.Context()

	for _, level := range []struct {
		name      string
		isolation sql.IsolationLevel
		conflict  bool
	}{
		{"read committed", sql.LevelReadCommitted, true},
		{"repeatable read", sql.LevelRepeatableRead, false},
		{"serializable", sql.LevelSerializable, false},
	} {
		t.Run(level.name, func(t *testing.T) {
			id := "A-" + level.isolation.String()
			stream := aStream(family, id)
			if _, _, err := repo.Append(ctx, loaded(t, ctx, repo, id), fact.New(id, held{Bytes: []byte("first")})); err != nil {
				t.Fatalf("the append that gives this case a stream answered %v", err)
			}

			inside, tx := begin(t, store, &sql.TxOptions{Isolation: level.isolation})
			at := loaded(t, inside, repo, id)

			if _, _, err := repo.Append(ctx, loaded(t, ctx, repo, id), fact.New(id, held{Bytes: []byte("the winner")})); err != nil {
				t.Fatalf("the append outside the transaction, which is the one that wins, answered %v", err)
			}

			_, _, err := repo.Append(inside, at, fact.New(id, held{Bytes: []byte("the loser")}))
			if level.conflict {
				if !errors.Is(err, event.ErrConflict) {
					t.Fatalf("the loser inside a %s transaction was told %v, and at this level the row it would update is the winner's committed one", level.name, err)
				}
				if errors.Is(err, event.ErrBackend) {
					t.Errorf("the loser was told %v, which sends a caller that only had to decide again into rolling its transaction back", err)
				}
			} else {
				if !errors.Is(err, event.ErrBackend) || !errors.Is(err, crud.ErrUnavailable) {
					t.Fatalf("the loser inside a %s transaction was told %v, and a serialisation failure says the transaction is dead rather than that the stream moved", level.name, err)
				}
				if errors.Is(err, event.ErrConflict) {
					t.Errorf("the loser was told %v, which asks it to reload the aggregate inside a transaction PostgreSQL will not commit", err)
				}
				fault, carried := errs.AsFault(event.CauseOf(err))
				if !carried || fault.Kind != errs.KindRetryable {
					t.Errorf("the cause is %v and the caller reads retryability off an errs.Fault", event.CauseOf(err))
				}
			}
			if err := tx.Rollback(inside); err != nil {
				t.Fatalf("rolling the loser back answered %v", err)
			}
			if got := len(stored(t, schema, stream)); got != 2 {
				t.Fatalf("the stream holds %d events where one writer won and one lost", got)
			}
		})
	}
}

func TestAFailureInsideABoundTransactionIsNeverUnconfirmed(t *testing.T) {
	const family = "eventpg.s3.joinedfailure"
	schema := sharedSchema(t)
	store := countingStore(t, schema, 2)
	repo, fact := boundRepo(t, store, family)
	injected := errors.New("eventpg test: a driver failure carrying no SQLSTATE at all")
	ctx := t.Context()

	inside, tx := begin(t, store, nil)
	at := loaded(t, inside, repo, "A-inside")
	arm(t, 1, true, injected)
	_, _, insideErr := repo.Append(inside, at, fact.New("A-inside", held{Bytes: []byte("inside")}))
	if err := tx.Rollback(inside); err != nil {
		t.Fatalf("rolling back answered %v", err)
	}

	beside := aStream(family, "A-onthepool")
	pooled := loaded(t, ctx, repo, "A-onthepool")
	arm(t, 1, true, injected)
	_, _, poolErr := repo.Append(ctx, pooled, fact.New("A-onthepool", held{Bytes: []byte("on the pool")}))

	if !errors.Is(insideErr, event.ErrBackend) || errors.Is(insideErr, event.ErrUncertain) {
		t.Errorf("the same failure inside the caller's transaction answered %v, and there is no window in which a write inside somebody else's uncommitted transaction became durable", insideErr)
	}
	if !errors.Is(poolErr, event.ErrUncertain) || errors.Is(poolErr, event.ErrBackend) {
		t.Errorf("the same failure on the pool answered %v, and the statement's own commit is exactly what was not acknowledged", poolErr)
	}
	if got := len(stored(t, schema, beside)); got != 1 {
		t.Fatalf("the uncertain append left %d events where the statement was written and only the answer was lost, so a store that reported NotWritten here would have said the write did not land about a write that did", got)
	}

	// The unique index over (family, key, version) is the floor under admission
	// and is unreachable through this store, so it is reached deliberately: a row
	// at the version the token's next append will take, written past this package
	// and leaving streams.version where it was.
	t.Run("a constraint violation inside the caller's transaction is not written and says so", func(t *testing.T) {
		id, ahead := "A-violating", aStream(family, "A-violating")
		at := loaded(t, ctx, repo, id)
		moved, _, err := repo.Append(ctx, at, fact.New(id, held{Bytes: []byte("the history so far")}))
		if err != nil {
			t.Fatalf("the append that gives this case a stream answered %v", err)
		}
		mustExecute(t, "INSERT INTO "+quoteIdentifier(schema.Name)+
			".events (family, key, version, type, revision, payload, recorded_at) VALUES ($1, $2, 2, $3, 1, $4, statement_timestamp())",
			family, id, family+".held", []byte("planted past this package"))

		inside, tx := begin(t, store, nil)
		_, _, err = repo.Append(inside, moved, fact.New(id, held{Bytes: []byte("refused by the unique index")}))
		if !errors.Is(err, event.ErrBackend) || errors.Is(err, event.ErrUncertain) {
			t.Fatalf("an append the unique index refused answered %v", err)
		}
		if err := tx.Rollback(inside); err != nil {
			t.Fatalf("rolling back answered %v", err)
		}
		if got := len(stored(t, schema, ahead)); got != 2 {
			t.Fatalf("the stream holds %d events where the appended one and the planted one are all there should be", got)
		}
	})
}

func TestARepoAuthorityAndAReceiptCompareSameAcrossTwoWritersInOneTransaction(t *testing.T) {
	const family = "eventpg.s3.authority"
	schema := sharedSchema(t)
	store := prepared(t, schema)
	repo, fact := boundRepo(t, store, family)
	table := callerTable(t, "eventpg_s3_authority")
	stream := aStream(family, "A-two-writers")

	inside, tx := begin(t, store, nil)
	before, err := repo.Authority(inside)
	if err != nil || !before.Valid() {
		t.Fatalf("the repository answers (%v, %v) inside a transaction of the store's own data source", before, err)
	}
	_, receipt, err := repo.Append(inside, loaded(t, inside, repo, "A-two-writers"), fact.New("A-two-writers", held{Bytes: []byte("the first writer")}))
	if err != nil {
		t.Fatalf("the append answered %v", err)
	}
	second, found := crud.ExecutorFor(inside, store.source)
	if !found {
		t.Fatal("the second writer finds no executor for the source the first one wrote through")
	}
	if _, err := second.Exec(inside, "INSERT INTO "+quoteIdentifier(table)+" (note) VALUES ($1)", "the second writer"); err != nil {
		t.Fatalf("the second writer's own row answered %v", err)
	}
	after, err := repo.Authority(inside)
	if err != nil {
		t.Fatalf("the repository answers %v after the second writer wrote", err)
	}
	if !before.Same(receipt.Authority()) || !after.Same(receipt.Authority()) {
		t.Error("the authority before the append, the receipt's own and the one after the second writer do not compare Same, so two subsystems cannot prove they wrote in one transaction")
	}
	if err := tx.Commit(inside); err != nil {
		t.Fatalf("committing answered %v", err)
	}
	if len(stored(t, schema, stream)) != 1 || len(callerNotes(t, liveDB(t), table)) != 1 {
		t.Fatal("one of the two writers landed and the other did not")
	}

	t.Run("two operations in two transactions on one pool do not compare Same", func(t *testing.T) {
		first, _ := begin(t, store, nil)
		other, _ := begin(t, store, nil)
		one, err := repo.Authority(first)
		if err != nil {
			t.Fatal(err)
		}
		two, err := repo.Authority(other)
		if err != nil {
			t.Fatal(err)
		}
		if !one.Valid() || !two.Valid() {
			t.Fatal("one of the two transactions is not bound, so the comparison below is between nothing and nothing")
		}
		if one.Same(two) {
			t.Fatal("two live transactions on one pool compare Same, so the comparison above holds for any two operations at all")
		}
	})

	t.Run("neither writer lands when the transaction rolls back", func(t *testing.T) {
		discarded := aStream(family, "A-discarded")
		inside, tx := begin(t, store, nil)
		if _, _, err := repo.Append(inside, loaded(t, inside, repo, "A-discarded"), fact.New("A-discarded", held{Bytes: []byte("discarded")})); err != nil {
			t.Fatal(err)
		}
		executor, _ := crud.ExecutorFor(inside, store.source)
		if _, err := executor.Exec(inside, "INSERT INTO "+quoteIdentifier(table)+" (note) VALUES ($1)", "discarded"); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(inside); err != nil {
			t.Fatal(err)
		}
		if len(stored(t, schema, discarded)) != 0 || len(callerNotes(t, liveDB(t), table)) != 1 {
			t.Fatal("the rolled-back transaction left one of its two writers behind")
		}
	})
}

func TestASavepointWritesUnderItsParentsAuthorityAndARollbackDiscardsThem(t *testing.T) {
	const family = "eventpg.s3.savepoint"
	schema := sharedSchema(t)
	store := prepared(t, schema)
	repo, fact := boundRepo(t, store, family)
	stream := aStream(family, "A-savepoint")

	inside, tx := begin(t, store, nil)
	parent, err := repo.Authority(inside)
	if err != nil || !parent.Valid() {
		t.Fatalf("the repository answers (%v, %v) inside the parent transaction", parent, err)
	}
	at, _, err := repo.Append(inside, loaded(t, inside, repo, "A-savepoint"), fact.New("A-savepoint", held{Bytes: []byte("under the parent")}))
	if err != nil {
		t.Fatalf("the append under the parent answered %v", err)
	}

	beginner, found := crud.BeginnerOf(tx)
	if !found {
		t.Fatal("the bound transaction cannot take a savepoint, so nothing below was measured")
	}
	savepoint, err := beginner.Begin(inside)
	if err != nil {
		t.Fatalf("taking a savepoint answered %v", err)
	}
	within := crud.BindExecutor(inside, store.source, savepoint)
	_, receipt, err := repo.Append(within, at, fact.New("A-savepoint", held{Bytes: []byte("inside the savepoint")}))
	if err != nil {
		t.Fatalf("the append inside the savepoint answered %v", err)
	}
	if !receipt.Authority().Same(parent) {
		t.Error("a receipt taken inside a savepoint names another transaction than the parent, and a savepoint commits with whoever owns the transaction it was taken in")
	}

	if err := savepoint.Rollback(within); err != nil {
		t.Fatalf("rolling back to the savepoint answered %v", err)
	}
	joined := transactionOf(t, inside, store)
	if got := payloads(storedOn(t, joined, schema, stream)); len(got) != 1 || got[0] != "under the parent" {
		t.Fatalf("the parent holds %v after the savepoint rolled back", got)
	}
	still, err := repo.Authority(inside)
	if err != nil || !still.Same(parent) {
		t.Errorf("the parent answers (%v, %v) after a savepoint inside it rolled back", still, err)
	}
	if _, _, err := repo.Append(inside, at, fact.New("A-savepoint", held{Bytes: []byte("after the rollback")})); err != nil {
		t.Fatalf("an append on the parent after the savepoint rolled back answered %v", err)
	}
	if err := tx.Commit(inside); err != nil {
		t.Fatalf("committing the parent answered %v", err)
	}
	if got := payloads(stored(t, schema, stream)); len(got) != 2 || got[1] != "after the rollback" {
		t.Fatalf("the committed transaction left %v", got)
	}

	t.Run("a receipt from another transaction on the same pool does not compare Same", func(t *testing.T) {
		other, _ := begin(t, store, nil)
		elsewhere, err := repo.Authority(other)
		if err != nil {
			t.Fatal(err)
		}
		if !elsewhere.Valid() {
			t.Fatal("the second transaction is not bound, so the comparison below is between nothing and nothing")
		}
		if elsewhere.Same(parent) {
			t.Fatal("two transactions on one pool compare Same, so the savepoint's own comparison above holds for anything")
		}
	})
}

func TestAnAmbientNonTransactionRefusesAtAllFourDoors(t *testing.T) {
	const family = "eventpg.s3.ambient"
	schema := sharedSchema(t)
	store := countingStore(t, schema, 2)
	repo, fact := boundRepo(t, store, family)
	ctx := t.Context()
	at := loaded(t, ctx, repo, "A-ambient")

	for _, bound := range []struct {
		name     string
		executor crud.Executor
	}{
		{"an executor over the pool that is not a transaction", crudsql.From(store.db)},
		{"an executor that says it is a transaction and yields no *sql.Tx", crudsql.From(store.db, crudsql.WithTransaction())},
	} {
		t.Run(bound.name, func(t *testing.T) {
			refusing := crud.BindExecutor(ctx, store.source, bound.executor)
			recount(t)
			var causes []error

			if _, err := repo.Within(refusing); !errors.Is(err, event.ErrAmbientNotTransaction) {
				t.Errorf("Repo.Within answered %v", err)
			} else {
				causes = append(causes, event.CauseOf(err))
			}
			if _, err := repo.Authority(refusing); !errors.Is(err, event.ErrAmbientNotTransaction) {
				t.Errorf("Repo.Authority answered %v", err)
			} else {
				causes = append(causes, event.CauseOf(err))
			}
			if _, _, err := repo.Load(refusing, "A-ambient"); !errors.Is(err, event.ErrAmbientNotTransaction) {
				t.Errorf("Repo.Load answered %v", err)
			} else {
				causes = append(causes, event.CauseOf(err))
			}
			if _, queries := counted(t); queries != 0 {
				t.Error("Repo.Load reached a read before it asked the transaction question")
			}
			if _, _, err := repo.Append(refusing, at, fact.New("A-ambient", held{Bytes: []byte("never written")})); !errors.Is(err, event.ErrAmbientNotTransaction) {
				t.Errorf("Repo.Append answered %v", err)
			} else {
				causes = append(causes, event.CauseOf(err))
			}

			// The fourth door is Reader.Next, which asks ReadAll and never asks the
			// transaction question itself: the kernel mints ErrAmbientNotTransaction
			// in one function and none of the three places it is called from is a
			// read. So the store answers Refused there, which is the one sentinel
			// that says this is policy and will not clear by trying again — the two
			// the kernel would otherwise render, ErrBackend and ErrUncertain, are
			// both retry classes.
			reader, err := event.Read(store, "")
			if err != nil {
				t.Fatalf("a reader over the store could not be opened: %v", err)
			}
			if _, err := reader.Next(refusing); !errors.Is(err, event.ErrRefused) {
				t.Errorf("the read door answered %v", err)
			} else {
				causes = append(causes, event.CauseOf(err))
				for _, retryable := range []error{event.ErrBackend, event.ErrUncertain} {
					if errors.Is(err, retryable) {
						t.Errorf("the read door answered %v, which reads as %v and puts a projector on a retry loop over a wiring error that never clears", err, retryable)
					}
				}
			}

			// The store's own door, because the kernel refuses three of the four
			// before it ever asks the store: with a Repo in the way, an append
			// through a store that never asked the question at all would still read
			// as a refusal here.
			direct := store.Append(refusing, event.AppendRequest{
				Stream:   aStream(family, "A-direct"),
				Expected: 0,
				Records:  []event.Record{{Type: family + ".held", Revision: 1, Payload: []byte("never written")}},
			})
			classifiedAs(t, direct, event.Refused, "an append at the store's own door under a bound executor that is not a transaction")

			if len(causes) != 5 {
				t.Fatalf("%d of the five entries refused as they should, so the causes below are not the whole answer", len(causes))
			}
			for _, cause := range causes {
				if !errors.Is(cause, errAmbientNotTransaction) {
					t.Errorf("one door carries %v as its cause where the store has one value for this and every door carries it", cause)
				}
			}
			if execs, queries := counted(t); execs != 0 || queries != 0 {
				t.Errorf("the four doors issued %d statements and %d queries, so one of them fell back to the pool beside the caller's own executor", execs, queries)
			}
		})
	}

	t.Run("a bound transaction on the same source serves all four", func(t *testing.T) {
		inside, tx := begin(t, store, nil)
		if _, err := repo.Within(inside); err != nil {
			t.Errorf("Repo.Within answered %v inside a transaction", err)
		}
		if _, err := repo.Authority(inside); err != nil {
			t.Errorf("Repo.Authority answered %v inside a transaction", err)
		}
		token := loaded(t, inside, repo, "A-served")
		if _, _, err := repo.Append(inside, token, fact.New("A-served", held{Bytes: []byte("served")})); err != nil {
			t.Errorf("Repo.Append answered %v inside a transaction", err)
		}
		reader, err := event.Read(store, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reader.Next(inside); err != nil {
			t.Errorf("the read door answered %v inside a transaction", err)
		}
		if err := tx.Rollback(inside); err != nil {
			t.Fatal(err)
		}
	})
}
