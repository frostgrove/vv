//go:build integration

package eventpg

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/event"
)

// The in-flight arrangement, and the order of its three writers is the case
// rather than an accident. The transaction that ends up holding the LOWER
// position takes its transaction id AFTER the one that commits the higher one,
// so the observing snapshot's xmax — which is the last completed id plus one and
// not the first unassigned one — sits at or below the holder's own id. That is
// the arrangement in which the rejected `xmin = xmax` rule reports the cluster
// quiet while a writer can still commit, and it is not contrived: any
// transaction that wrote anything before the one holding the low position
// produces it.
func inFlightGap(t *testing.T, store *Store, schema Schema, family string) (context.Context, crud.Tx, int64, int64) {
	t.Helper()
	earlier, committing := begin(t, store, nil)
	if _, err := transactionOf(t, earlier, store).ExecContext(earlier, "SELECT pg_current_xact_id()"); err != nil {
		t.Fatalf("the writer that takes its transaction id first answered %v", err)
	}

	holding, holder := begin(t, store, nil)
	low := aStream(family, "A-held")
	appendOn(t, holding, store, low, 0, "the position a writer still holds")
	drawn := onlyPosition(t, storedOn(t, transactionOf(t, holding, store), schema, low))

	high := aStream(family, "A-committed")
	appendOn(t, earlier, store, high, 0, "the position that committed first")
	if err := committing.Commit(earlier); err != nil {
		t.Fatalf("the writer that took its id first could not commit: %v", err)
	}
	committed := onlyPosition(t, stored(t, schema, high))

	if committed <= drawn {
		t.Fatalf("the committed position is %d and the held one %d, so this arrangement has no gap a walk could pass", committed, drawn)
	}
	return holding, holder, drawn, committed
}

func onlyPosition(t *testing.T, rows []storedRow) int64 {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("this stream holds %d rows where the case that built it wrote one", len(rows))
	}
	return rows[0].position
}

func snapshotBounds(t *testing.T) (string, string) {
	t.Helper()
	var floor, ceiling string
	row := liveDB(t).QueryRowContext(t.Context(),
		"SELECT pg_snapshot_xmin(s)::text, pg_snapshot_xmax(s)::text FROM pg_current_snapshot() s")
	if err := row.Scan(&floor, &ceiling); err != nil {
		t.Fatalf("the observing snapshot could not be read: %v", err)
	}
	return floor, ceiling
}

func cursorOf(t *testing.T, store *Store, cursor event.Cursor) walk {
	t.Helper()
	at, err := readCursor(cursor, logOf(t, store))
	if err != nil {
		t.Fatalf("the store minted a cursor it cannot read back: %v", err)
	}
	return at
}

func floorOn(t *testing.T, on executor) uint64 {
	t.Helper()
	floor, err := number(t.Context(), on, "SELECT pg_snapshot_xmin(pg_current_snapshot())::text")
	if err != nil {
		t.Fatalf("the cluster's floor could not be read: %v", err)
	}
	return floor
}

func assignedOn(t *testing.T, on executor) (uint64, bool) {
	t.Helper()
	rows, err := on.QueryContext(t.Context(), "SELECT pg_current_xact_id_if_assigned()::text")
	if err != nil {
		t.Fatalf("what this transaction had been assigned could not be read: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var assigned sql.Null[string]
	if !rows.Next() || rows.Scan(&assigned) != nil || rows.Err() != nil {
		t.Fatalf("what this transaction had been assigned could not be read: %v", rows.Err())
	}
	if !assigned.Valid {
		return 0, false
	}
	held, err := strconv.ParseUint(assigned.V, 10, 64)
	if err != nil {
		t.Fatalf("the transaction id %q is not a number: %v", assigned.V, err)
	}
	return held, true
}

// The bound the rejected alternative would mint: the store's own statement, on a
// connection of the store's own pool, while the caller's transaction is open.
func mintedBeside(t *testing.T, store *Store) uint64 {
	t.Helper()
	conn, err := store.db.Conn(t.Context())
	if err != nil {
		t.Fatalf("a connection beside the caller's transaction could not be checked out: %v", err)
	}
	defer func() { _ = conn.Close() }()
	bound, err := number(t.Context(), conn, mintStatement)
	if err != nil {
		t.Fatalf("the bound beside the caller's transaction could not be minted: %v", err)
	}
	return bound
}

func burn(t *testing.T, store *Store, family, key string) {
	t.Helper()
	inside, tx := begin(t, store, nil)
	appendOn(t, inside, store, aStream(family, key), 0, "rolled back")
	if err := tx.Rollback(inside); err != nil {
		t.Fatalf("the transaction whose positions this case burns could not be rolled back: %v", err)
	}
}

func TestAWalkDoesNotPassAPositionAWriterCouldStillCommit(t *testing.T) {
	const family = "eventpg.s4.inflight"
	schema := deployed(t, "eventpg_s4_inflight")
	ctx := t.Context()
	store := prepared(t, schema)
	holding, holder, drawn, committed := inFlightGap(t, store, schema, family)

	page, cursor := readAll(t, ctx, store, "")
	if len(page) != 0 {
		t.Fatalf("the walk delivered %v while a writer still held position %d, and a consumer that checkpointed there would never see it",
			positionsOf(page), drawn)
	}
	if at := cursorOf(t, store, cursor); at.from >= uint64(drawn) {
		t.Fatalf("the cursor has reached position %d where a writer could still commit at %d", at.from, drawn)
	}

	t.Run("a second pass over the same gap mints nothing and stays stopped", func(t *testing.T) {
		before := cursorOf(t, store, cursor)
		again, next := readAll(t, ctx, store, cursor)
		if len(again) != 0 {
			t.Fatalf("the second pass delivered %v while the same writer still held position %d", positionsOf(again), drawn)
		}
		after := cursorOf(t, store, next)
		if after != before {
			t.Fatalf("the cursor moved from %+v to %+v over a gap nothing settled, so a polled walk mints a fresh bound on every pass", before, after)
		}
	})

	t.Run("after the writer commits the next walk delivers both, in position order", func(t *testing.T) {
		if err := holder.Commit(holding); err != nil {
			t.Fatalf("the writer that held the low position could not commit: %v", err)
		}
		whole, _ := drainFrom(t, ctx, store, cursor)
		if got := positionsOf(whole); !slices.Equal(got, []event.Position{event.Position(drawn), event.Position(committed)}) {
			t.Fatalf("the walk after the commit delivered %v where the log holds %d and %d", got, drawn, committed)
		}
	})

	// The window between the two statements a settle pairs, driven rather than
	// argued. The writer holding the low position is invisible to the walk's first
	// look at the log and commits before its second one, which is the arrangement in
	// which a settle applied to the rows of the earlier snapshot declares the gap
	// burnt and hands over the row above a position that is committed and present.
	t.Run("a writer that commits between the mint and the second look is not skipped", func(t *testing.T) {
		beside := deployed(t, "eventpg_s4_inflight_window")
		store := countingStore(t, beside, 6)
		holding, holder := begin(t, store, nil)
		appendOn(t, holding, store, aStream(family, "A-held"), 0, "the position the walk must not pass")
		appendOn(t, ctx, store, aStream(family, "A-committed"), 0, "the position above it")

		var inside bool
		interceptOnce(t, mintStatement, func() {
			if err := holder.Commit(holding); err != nil {
				t.Errorf("the writer holding the low position could not commit inside the window: %v", err)
				return
			}
			inside = true
		})

		page, cursor := readAll(t, ctx, store, "")
		rest, _ := drainFrom(t, ctx, store, cursor)
		if !inside {
			t.Fatal("the walk minted no bound, so nothing committed inside the window this case is about and it measures nothing")
		}
		held := storedPositions(t, beside)
		want := make([]event.Position, 0, len(held))
		for _, position := range held {
			want = append(want, event.Position(position))
		}
		if len(want) != 2 {
			t.Fatalf("the table holds the positions %v where this case wrote two, so the gap it is about was never built", held)
		}
		if got := positionsOf(append(page, rest...)); !slices.Equal(got, want) {
			t.Fatalf("the walk delivered %v where the table holds %v, and a committed position a walk passed is never read by that consumer again", got, want)
		}
	})

	t.Run("with no writer in flight the same walk delivers at once", func(t *testing.T) {
		beside := deployed(t, "eventpg_s4_inflight_control")
		quiet := prepared(t, beside)
		appendOn(t, ctx, quiet, aStream(family, "A-1"), 0, "one")
		appendOn(t, ctx, quiet, aStream(family, "A-2"), 0, "two")
		page, _ := readAll(t, ctx, quiet, "")
		if got := payloadsOf(page); !slices.Equal(got, []string{"one", "two"}) {
			t.Fatalf("a walk over a log nobody is writing to answered %v in its first call, so a store that stalls for good would pass the case above", got)
		}
	})
}

// The rule a reviewer proposes, driven rather than argued. It is the store's own
// statement with `pg_snapshot_xmin = pg_snapshot_xmax` where the settled
// watermark should be, and in the arrangement above it reports the cluster quiet
// and hands over the position beyond a gap a writer is still holding.
func TestTheXminEqualsXmaxRuleFailsTheInFlightCase(t *testing.T) {
	const family = "eventpg.s4.rejected"
	schema := deployed(t, "eventpg_s4_rejected")
	ctx := t.Context()
	store := prepared(t, schema)
	_, _, drawn, committed := inFlightGap(t, store, schema, family)

	floor, ceiling := snapshotBounds(t)
	if floor != ceiling {
		t.Fatalf("the observing snapshot reports xmin %s and xmax %s, so the arrangement this negative control needs was not built and it measures nothing",
			floor, ceiling)
	}

	settled := xminEqualsXmaxWalk(t, ctx, schema)
	if !slices.Contains(settled, committed) {
		t.Fatalf("the rejected rule delivered %v, so it did not settle the gap here and this case is no longer evidence that it is unsound", settled)
	}
	if slices.Contains(settled, drawn) {
		t.Fatalf("the rejected rule delivered position %d, which is uncommitted and cannot have been visible to it", drawn)
	}

	page, cursor := readAll(t, ctx, store, "")
	if len(page) != 0 {
		t.Fatalf("this store delivered %v in the arrangement that falsifies the rejected rule, so its settlement is the rejected one",
			positionsOf(page))
	}
	if at := cursorOf(t, store, cursor); at.from >= uint64(drawn) {
		t.Fatalf("this store's cursor reached %d where the rejected rule would have passed %d", at.from, drawn)
	}
}

func xminEqualsXmaxWalk(t *testing.T, ctx context.Context, schema Schema) []int64 {
	t.Helper()
	rows, err := liveDB(t).QueryContext(ctx,
		"SELECT h.quiet, e.position\n"+
			"  FROM (SELECT pg_snapshot_xmin(s) = pg_snapshot_xmax(s) AS quiet FROM pg_current_snapshot() s) h\n"+
			"  LEFT JOIN LATERAL (SELECT position FROM "+quoteIdentifier(schema.Name)+".events WHERE position > $1 ORDER BY position) e ON true",
		int64(0))
	if err != nil {
		t.Fatalf("the rejected rule's own statement answered %v", err)
	}
	defer func() { _ = rows.Close() }()
	var delivered []int64
	for rows.Next() {
		var quiet bool
		var position sql.Null[int64]
		if err := rows.Scan(&quiet, &position); err != nil {
			t.Fatalf("the rejected rule's own statement could not be read: %v", err)
		}
		if quiet && position.Valid {
			delivered = append(delivered, position.V)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("the rejected rule's own statement could not be read to its end: %v", err)
	}
	return delivered
}

func TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot(t *testing.T) {
	const family = "eventpg.s4.burnt"
	ctx := t.Context()

	t.Run("a gap between two committed positions is passed and no page comes back empty", func(t *testing.T) {
		schema := deployed(t, "eventpg_s4_burnt_between")
		store := countingStore(t, schema, 4)
		appendOn(t, ctx, store, aStream(family, "A-before"), 0, "before the gap")
		burn(t, store, family, "A-burnt")
		appendOn(t, ctx, store, aStream(family, "A-after"), 0, "after the gap")

		recount(t)
		head, cursor := readAll(t, ctx, store, "")
		if got := payloadsOf(head); !slices.Equal(got, []string{"before the gap"}) {
			t.Fatalf("the first walk answered %v where it stops at the gap the rollback burnt", got)
		}
		if _, queries := counted(t); queries != 2 {
			t.Errorf("the walk that met the gap issued %d queries where it reads the page and mints one bound", queries)
		}

		recount(t)
		tail, _ := readAll(t, ctx, store, cursor)
		if got := payloadsOf(tail); !slices.Equal(got, []string{"after the gap"}) {
			t.Fatalf("the walk beyond the gap answered %v, and a walk that stops for good at a gap that can never be filled never finishes", got)
		}
		if _, queries := counted(t); queries != 1 {
			t.Errorf("the walk that passed the settled gap issued %d queries where the bound it carried was already settled", queries)
		}
	})

	t.Run("a gap at the head of the page settles inside the same call", func(t *testing.T) {
		schema := deployed(t, "eventpg_s4_burnt_head")
		store := countingStore(t, schema, 4)
		burn(t, store, family, "A-burnt")
		appendOn(t, ctx, store, aStream(family, "A-after"), 0, "after the burnt head")

		recount(t)
		page, cursor := readAll(t, ctx, store, "")
		if got := payloadsOf(page); !slices.Equal(got, []string{"after the burnt head"}) {
			t.Fatalf("the walk answered %v where the only thing between it and the events is a position a rollback burnt", got)
		}
		if _, queries := counted(t); queries != 3 {
			t.Errorf("the walk issued %d queries where it reads the page, mints a bound and takes one more snapshot", queries)
		}
		if at := cursorOf(t, store, cursor); at.bound != 0 {
			t.Errorf("the cursor still carries a bound of %d after the gap it was minted for settled", at.bound)
		}
	})

	// The second look is a fresh snapshot, so it can carry rows the first one did
	// not, and the walk can deliver past the position the bound was minted for. The
	// bound is spent when that happens: carried into the cursor it would name a
	// reach below what the walk has delivered, which is a walk no store could have
	// produced and every later call refuses.
	t.Run("rows that arrive between the two looks are delivered and the cursor stays readable", func(t *testing.T) {
		schema := deployed(t, "eventpg_s4_burnt_grown")
		store := countingStore(t, schema, 6)
		burn(t, store, family, "A-burnt")
		appendOn(t, ctx, store, aStream(family, "A-after"), 0, "after the burnt head")

		interceptOnce(t, mintStatement, func() {
			appendOn(t, ctx, store, aStream(family, "A-arrived"), 0, "arrived between the two looks")
			burn(t, store, family, "A-burnt-above")
			appendOn(t, ctx, store, aStream(family, "A-beyond"), 0, "beyond the second gap")
		})

		page, cursor := readAll(t, ctx, store, "")
		if got := payloadsOf(page); !slices.Equal(got, []string{"after the burnt head", "arrived between the two looks"}) {
			t.Fatalf("the walk answered %v where its second look at the log holds both of those and stops at the gap above them", got)
		}
		rest, _ := drainFrom(t, ctx, store, cursor)
		if got := payloadsOf(rest); !slices.Equal(got, []string{"beyond the second gap"}) {
			t.Fatalf("the walk resumed from the cursor of the call that grew answered %v", got)
		}
	})

	t.Run("a gap a writer still holds is not passed and costs no second bound", func(t *testing.T) {
		schema := deployed(t, "eventpg_s4_burnt_live")
		store := countingStore(t, schema, 6)
		_, _, drawn, _ := inFlightGap(t, store, schema, family)

		recount(t)
		page, cursor := readAll(t, ctx, store, "")
		if len(page) != 0 {
			t.Fatalf("the walk delivered %v across a gap position %d is still being written into", positionsOf(page), drawn)
		}
		if _, queries := counted(t); queries != 3 {
			t.Errorf("the walk issued %d queries where it reads the page, mints a bound and takes one more snapshot", queries)
		}

		recount(t)
		again, _ := readAll(t, ctx, store, cursor)
		if len(again) != 0 {
			t.Fatalf("the second walk delivered %v across the same gap", positionsOf(again))
		}
		if _, queries := counted(t); queries != 1 {
			t.Errorf("the second walk issued %d queries where the bound it carries is still doing the work the first one minted it for", queries)
		}
	})
}

func TestAWalkInsideATransactionMintsNoBoundAndSettlesNoGap(t *testing.T) {
	const family = "eventpg.s4.bound"
	ctx := t.Context()
	schema := deployed(t, "eventpg_s4_bound")
	store := countingStore(t, schema, 4)
	burn(t, store, family, "A-burnt")
	appendOn(t, ctx, store, aStream(family, "A-after"), 0, "after the burnt head")

	inside, tx := begin(t, store, nil)
	page, cursor, err := store.ReadAll(inside, "")
	if err != nil {
		t.Fatalf("a walk on a context carrying a transaction of this backing answered %v", err)
	}
	if len(page) != 0 {
		t.Fatalf("the walk inside the transaction delivered %v, so it settled a gap with a bound it must not mint there", payloadsOf(page))
	}
	if at := cursorOf(t, store, cursor); at.bound != 0 || at.reach != 0 {
		t.Fatalf("the cursor minted inside a transaction carries the bound %d and the reach %d", at.bound, at.reach)
	}

	var assigned sql.Null[string]
	if err := transactionOf(t, inside, store).QueryRowContext(inside,
		"SELECT pg_current_xact_id_if_assigned()::text").Scan(&assigned); err != nil {
		t.Fatalf("what the caller's transaction had been assigned could not be read: %v", err)
	}
	if assigned.Valid {
		t.Fatalf("the caller's transaction was assigned the id %s by a read, which turns it into a writer that holds vacuum back", assigned.V)
	}
	if err := tx.Rollback(inside); err != nil {
		t.Fatal(err)
	}

	t.Run("the same walk outside the transaction settles and advances", func(t *testing.T) {
		page, _ := readAll(t, ctx, store, "")
		if got := payloadsOf(page); !slices.Equal(got, []string{"after the burnt head"}) {
			t.Fatalf("the same walk with nothing bound answered %v, so the walk above proves nothing about the transaction", got)
		}
	})

	// The consequence of minting nothing there, pinned rather than left as a side
	// effect: a consumer that always walks on a context carrying a transaction of
	// this backing never acquires a bound, so the first burnt position stops it and
	// nothing later moves it. A store that minted a bound outside the caller's
	// transaction would pass this gap, so this case is where that choice is made.
	t.Run("three walks, each in its own transaction, all stop at the same burnt gap", func(t *testing.T) {
		beside := deployed(t, "eventpg_s4_bound_stall")
		stalling := prepared(t, beside)
		burn(t, stalling, family, "A-burnt")
		appendOn(t, ctx, stalling, aStream(family, "A-after"), 0, "after the burnt head")

		cursor := event.Cursor("")
		for pass, options := range []*sql.TxOptions{nil, nil, {Isolation: sql.LevelRepeatableRead}} {
			inside, tx := begin(t, stalling, options)
			page, next, err := stalling.ReadAll(inside, cursor)
			if err != nil {
				t.Fatalf("walk %d inside a transaction answered %v", pass+1, err)
			}
			if len(page) != 0 {
				t.Fatalf("walk %d inside a transaction delivered %v, and a store that mints a bound outside the caller's transaction is what passes this gap",
					pass+1, payloadsOf(page))
			}
			if at := cursorOf(t, stalling, next); at != (walk{}) {
				t.Fatalf("walk %d answered the cursor %+v where it delivered nothing and minted nothing", pass+1, at)
			}
			cursor = next
			if err := tx.Rollback(inside); err != nil {
				t.Fatal(err)
			}
		}

		page, _ := readAll(t, ctx, stalling, cursor)
		if got := payloadsOf(page); !slices.Equal(got, []string{"after the burnt head"}) {
			t.Fatalf("the cursor the three stalled walks answered delivers %v outside a transaction, so they measured a walk that never advances rather than one that cannot settle inside a transaction", got)
		}
	})

	// Where a bound for such a walk could have come from, and what each one is
	// worth. On the caller's own transaction it is worth nothing the moment that
	// transaction has written: the floor a snapshot of it reports never passes an id
	// it holds itself, so the settlement test can never come out true. Beside it, on
	// a connection of the store's own pool, it would settle — and it costs a second
	// checkout while the caller holds one, which the case after this one measures.
	t.Run("a transaction that has written holds the floor at or below its own id", func(t *testing.T) {
		beside := deployed(t, "eventpg_s4_bound_floor")
		own := prepared(t, beside)

		writing, tx := begin(t, own, nil)
		appendOn(t, writing, own, aStream(family, "A-writing"), 0, "written inside the caller's transaction")
		held := transactionOf(t, writing, own)
		assigned, carried := assignedOn(t, held)
		if !carried {
			t.Fatal("the caller's transaction wrote a row and was assigned no id, so the floor it holds is not what this case is about")
		}
		bound := mintedBeside(t, own)
		if floor := floorOn(t, held); floor > bound {
			t.Fatalf("a snapshot of the writing transaction reports the floor %d, above the bound %d minted beside it, so a bound off the pool would settle a gap inside a bound transaction and the walk above gives up something it could have had",
				floor, bound)
		}
		if floor := floorOn(t, held); floor > assigned {
			t.Fatalf("the floor is %d while the caller's transaction is open and holds the id %d, so a transaction does not hold the floor down and the reason this store mints nothing there is not the one it states",
				floor, assigned)
		}
		if err := tx.Rollback(writing); err != nil {
			t.Fatal(err)
		}

		reading, quiet := begin(t, own, nil)
		if id, carried := assignedOn(t, transactionOf(t, reading, own)); carried {
			t.Fatalf("a transaction that wrote nothing was assigned the id %d, so the two halves this case separates are one and the case above measures nothing", id)
		}
		if err := quiet.Rollback(reading); err != nil {
			t.Fatal(err)
		}
	})

	// The cost of the alternative, measured rather than asserted. A pool at its
	// limit answers the second checkout with the caller's own deadline, so a store
	// that took one connection per gap while the caller held another would turn a
	// walk that stops into a pool that stops.
	t.Run("a second connection while a transaction holds the first is what a pool at its limit refuses", func(t *testing.T) {
		const waiting = 250 * time.Millisecond
		at := countingPool(t, 1)
		holding, err := at.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("the transaction that holds the pool's one connection could not be opened: %v", err)
		}
		defer func() { _ = holding.Rollback() }()

		limited, cancel := context.WithTimeout(ctx, waiting)
		defer cancel()
		if conn, err := at.Conn(limited); err == nil {
			_ = conn.Close()
			t.Fatal("a pool at its one-connection limit handed out a second connection while a transaction held the only one, so the checkout a bound would need costs nothing and the walk above gives up something free")
		} else if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the second checkout answered %v where it waits for the connection the caller's transaction holds", err)
		}

		wider := countingPool(t, 2)
		spare, err := wider.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("the transaction of the control could not be opened: %v", err)
		}
		defer func() { _ = spare.Rollback() }()
		control, stop := context.WithTimeout(ctx, waiting)
		defer stop()
		conn, err := wider.Conn(control)
		if err != nil {
			t.Fatalf("a pool with a connection to spare refused the second checkout with %v, so the case above measures %s of waiting and not the limit", err, waiting)
		}
		_ = conn.Close()
	})

	t.Run("a walk inside a transaction runs on it and the pool does not see what it wrote", func(t *testing.T) {
		beside := deployed(t, "eventpg_s4_bound_own")
		own := prepared(t, beside)
		staged, tx := begin(t, own, nil)
		appendOn(t, staged, own, aStream(family, "A-staged"), 0, "staged and not committed")

		page, _, err := own.ReadAll(staged, "")
		if err != nil {
			t.Fatalf("a walk inside the transaction that wrote them answered %v", err)
		}
		if got := payloadsOf(page); !slices.Equal(got, []string{"staged and not committed"}) {
			t.Fatalf("a walk inside the transaction that wrote them answered %v", got)
		}
		outside, _ := readAll(t, ctx, own, "")
		if len(outside) != 0 {
			t.Fatalf("a walk with nothing bound answered %v, so the walk above ran on the pool beside the caller's own transaction", payloadsOf(outside))
		}
		if err := tx.Rollback(staged); err != nil {
			t.Fatal(err)
		}
	})
}

const probeFunction = "probe_writer"

// A test-only BEFORE INSERT row trigger that records what the inserting
// transaction had been assigned at the moment the position was drawn. It writes
// into a session setting rather than into a table, because a table would need a
// transaction id of its own and would assign the very thing this measures.
func plantWriterProbe(t *testing.T, schema Schema) {
	t.Helper()
	quoted := quoteIdentifier(schema.Name)
	mustExecute(t, "CREATE OR REPLACE FUNCTION "+quoted+"."+probeFunction+"() RETURNS trigger LANGUAGE plpgsql AS $probe$\n"+
		"BEGIN\n"+
		"  PERFORM set_config('eventpg.probe_xid', coalesce(pg_current_xact_id_if_assigned()::text, ''), false);\n"+
		"  PERFORM set_config('eventpg.probe_position', coalesce(NEW.position::text, ''), false);\n"+
		"  RETURN NEW;\n"+
		"END\n$probe$")
	mustExecute(t, "CREATE TRIGGER "+probeFunction+" BEFORE INSERT ON "+quoted+".events\n"+
		"FOR EACH ROW EXECUTE FUNCTION "+quoted+"."+probeFunction+"()")
}

// One INSERT from a transaction that has written nothing else, issued the way
// any other client would issue it, and what the probe saw while it ran.
func foreignInsert(t *testing.T, schema Schema, stream event.Stream, version int64, commit bool) (string, string) {
	t.Helper()
	ctx := t.Context()
	tx, err := liveDB(t).BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("the foreign writer could not open its transaction: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+quoteIdentifier(schema.Name)+
		".events (family, key, version, type, revision, payload, recorded_at) VALUES ($1, $2, $3, $4, $5, $6, statement_timestamp())",
		stream.Family, string(stream.Key), version, stream.Family+".held", int32(1), []byte("written by somebody else")); err != nil {
		_ = tx.Rollback()
		t.Fatalf("the foreign writer's insert answered %v", err)
	}
	var assigned, position string
	if err := tx.QueryRowContext(ctx,
		"SELECT current_setting('eventpg.probe_xid', true), current_setting('eventpg.probe_position', true)").Scan(&assigned, &position); err != nil {
		_ = tx.Rollback()
		t.Fatalf("what the probe recorded could not be read: %v", err)
	}
	if commit {
		if err := tx.Commit(); err != nil {
			t.Fatalf("the foreign writer could not commit: %v", err)
		}
	} else if err := tx.Rollback(); err != nil {
		t.Fatalf("the foreign writer could not roll back: %v", err)
	}
	return assigned, position
}

func TestAForeignWriterIsNotSkippedAndTheOverridingWriterIs(t *testing.T) {
	const family = "eventpg.s4.foreign"
	ctx := t.Context()

	t.Run("a writer that lets the column draw the position has its id before it is drawn", func(t *testing.T) {
		schema := deployed(t, "eventpg_s4_foreign_premise")
		store := prepared(t, schema)
		stream := aStream(family, "A-foreign")
		appendOn(t, ctx, store, stream, 0, "the row that creates the stream")
		plantWriterProbe(t, schema)

		assigned, position := foreignInsert(t, schema, stream, 2, false)
		if assigned == "" {
			t.Fatal("a foreign writer that had written nothing else drew a position with no transaction id assigned, and the settlement rule would declare its gap burnt while it was still running")
		}
		if position == "" {
			t.Fatal("the position was not drawn before the row trigger ran, so this measurement is of something other than the order it is about")
		}

		mustExecute(t, "DROP TRIGGER events_position_needs_xid ON "+quoteIdentifier(schema.Name)+".events")
		without, drawn := foreignInsert(t, schema, stream, 3, false)
		if without != "" {
			t.Fatalf("with the store's own trigger dropped the writer was still assigned %s, so the trigger is not what assigns it and the premise rests on the shape of one statement", without)
		}
		if drawn == "" {
			t.Fatal("with the trigger dropped the position was not drawn either, so the control says nothing about the order of the two")
		}
	})

	t.Run("a walk does not pass an uncommitted foreign insert", func(t *testing.T) {
		schema := deployed(t, "eventpg_s4_foreign_walk")
		store := prepared(t, schema)
		stream := aStream(family, "A-foreign")
		appendOn(t, ctx, store, stream, 0, "the row that creates the stream")
		whole, cursor := readAll(t, ctx, store, "")
		if len(whole) != 1 {
			t.Fatalf("the walk that gives this case a checkpoint answered %d envelopes", len(whole))
		}

		writing, err := liveDB(t).BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("the foreign writer could not open its transaction: %v", err)
		}
		if _, err := writing.ExecContext(ctx, "INSERT INTO "+quoteIdentifier(schema.Name)+
			".events (family, key, version, type, revision, payload, recorded_at) VALUES ($1, $2, $3, $4, $5, $6, statement_timestamp())",
			stream.Family, string(stream.Key), int64(2), family+".held", int32(1), []byte("written by somebody else")); err != nil {
			_ = writing.Rollback()
			t.Fatalf("the foreign writer's insert answered %v", err)
		}
		appendOn(t, ctx, store, aStream(family, "A-beside"), 0, "committed above the foreign row")

		page, held := readAll(t, ctx, store, cursor)
		if len(page) != 0 {
			t.Fatalf("the walk delivered %v across a position a foreign writer is still holding", payloadsOf(page))
		}
		if err := writing.Commit(); err != nil {
			t.Fatalf("the foreign writer could not commit: %v", err)
		}
		rest, _ := drainFrom(t, ctx, store, held)
		if got := payloadsOf(rest); !slices.Equal(got, []string{"written by somebody else", "committed above the foreign row"}) {
			t.Fatalf("the walk after the foreign writer committed delivered %v, so a row this store did not write was skipped", got)
		}
	})

	// nextval() assigns a transaction id — measured on PostgreSQL 17.9 — so a
	// writer that draws a position and inserts it later in the SAME transaction is
	// covered by the watermark like any other. That is one half of the boundary and
	// this is the other: a position drawn in a transaction that has already ended
	// carries no running writer for the settlement rule to see.
	t.Run("a writer that drew the position inside its transaction is covered", func(t *testing.T) {
		schema := deployed(t, "eventpg_s4_foreign_inside")
		store := prepared(t, schema)
		stream := aStream(family, "A-overriding")

		overriding := writerHolding(t, schema)
		drawn := drawPosition(t, ctx, overriding, schema)
		appendOn(t, ctx, store, aStream(family, "A-beside"), 0, "committed above the drawn position")

		page, cursor := readAll(t, ctx, store, "")
		if len(page) != 0 {
			t.Fatalf("the walk delivered %v across position %d, which the transaction that drew it can still insert", payloadsOf(page), drawn)
		}
		overrideInto(t, ctx, overriding, schema, stream, drawn, "drawn and inserted in one transaction")
		if err := overriding.Commit(); err != nil {
			t.Fatalf("the overriding writer could not commit: %v", err)
		}

		rest, _ := drainFrom(t, ctx, store, cursor)
		if got := payloadsOf(rest); !slices.Equal(got, []string{"drawn and inserted in one transaction", "committed above the drawn position"}) {
			t.Fatalf("the walk after the overriding writer committed delivered %v, so a row it could have waited for was skipped", got)
		}
	})

	t.Run("a writer that drew the position in an earlier transaction is skipped", func(t *testing.T) {
		schema := deployed(t, "eventpg_s4_foreign_overriding")
		store := prepared(t, schema)
		stream := aStream(family, "A-overriding")

		var drawn int64
		if err := liveDB(t).QueryRowContext(ctx, "SELECT nextval(pg_get_serial_sequence($1, 'position'))",
			schema.Name+".events").Scan(&drawn); err != nil {
			t.Fatalf("drawing a position on its own answered %v", err)
		}
		appendOn(t, ctx, store, aStream(family, "A-beside"), 0, "committed above the drawn position")

		page, cursor := readAll(t, ctx, store, "")
		if got := payloadsOf(page); !slices.Equal(got, []string{"committed above the drawn position"}) {
			t.Fatalf("the walk answered %v where the transaction that drew the position below it has already ended", got)
		}

		overriding := writerHolding(t, schema)
		overrideInto(t, ctx, overriding, schema, stream, drawn, "drawn in an earlier transaction")
		if err := overriding.Commit(); err != nil {
			t.Fatalf("the overriding writer could not commit: %v", err)
		}

		rest, _ := drainFrom(t, ctx, store, cursor)
		if len(rest) != 0 {
			t.Fatalf("the walk delivered %v after the overriding writer committed, so the boundary this case measures has moved and the module page overstates the limit",
				payloadsOf(rest))
		}
		if !slices.Contains(storedPositions(t, schema), drawn) {
			t.Fatalf("position %d is not in the table, so nothing was skipped and this case measures nothing", drawn)
		}
	})
}

func writerHolding(t *testing.T, schema Schema) *sql.Tx {
	t.Helper()
	tx, err := liveDB(t).BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("the writer this case needs could not open its transaction: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func drawPosition(t *testing.T, ctx context.Context, on *sql.Tx, schema Schema) int64 {
	t.Helper()
	var drawn int64
	if err := on.QueryRowContext(ctx, "SELECT nextval(pg_get_serial_sequence($1, 'position'))",
		schema.Name+".events").Scan(&drawn); err != nil {
		t.Fatalf("drawing a position outside an insert answered %v", err)
	}
	return drawn
}

// The one writer shape GENERATED ALWAYS AS IDENTITY forces a client to spell out
// loud: a position this transaction did not let the column draw.
func overrideInto(t *testing.T, ctx context.Context, on *sql.Tx, schema Schema, stream event.Stream, position int64, payload string) {
	t.Helper()
	if _, err := on.ExecContext(ctx, "INSERT INTO "+quoteIdentifier(schema.Name)+
		".streams (family, key, version) VALUES ($1, $2, $3)", stream.Family, string(stream.Key), int64(1)); err != nil {
		t.Fatalf("the overriding writer's stream row answered %v", err)
	}
	if _, err := on.ExecContext(ctx, "INSERT INTO "+quoteIdentifier(schema.Name)+
		".events (position, family, key, version, type, revision, payload, recorded_at)\n"+
		"OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4, $5, $6, $7, statement_timestamp())",
		position, stream.Family, string(stream.Key), int64(1), stream.Family+".held", int32(1), []byte(payload)); err != nil {
		t.Fatalf("the overriding writer's insert answered %v", err)
	}
}
