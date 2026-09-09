//go:build integration

package eventpg

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/eventtest"
)

func TestTheCheckpointStoreSatisfiesTheContract(t *testing.T) {
	eventtest.RunCheckpoints(t, checkpointConformance(t))
}

// One schema and one pool for the whole run, because the cursors this factory
// answers are that schema's log's own and a second schema's would be foreign to
// every row already written. Every value New builds is a second value over it,
// which is what a restart is — and the ten connections are what the eight
// savers of the concurrency section need at once.
func checkpointConformance(t *testing.T) eventtest.CheckpointFactory {
	t.Helper()
	schema := Schema{Name: scratchName()}
	pool := checkpointPool(t, 10)
	t.Cleanup(func() { dropSchema(t, schema.Name) })
	minting := &pgMinter{store: preparedStoreOver(t, pool, schema, ManageSchema)}
	return downgradedCheckpoints(t, eventtest.CheckpointFactory{
		New: func(t *testing.T) event.Checkpoints {
			return preparedCheckpoints(t, pool, schema, VerifySchema)
		},
		Begin: func(t *testing.T, ctx context.Context, c event.Checkpoints) (context.Context, eventtest.Tx) {
			return beginOnPool(t, ctx, pool)
		},
		Sibling: func(t *testing.T, _ event.Checkpoints) event.Checkpoints {
			return preparedCheckpoints(t, pool, schema, VerifySchema)
		},
		Cursor: minting.next,
		// timestamptz is a microsecond, so that is what a row of this schema
		// keeps and what the suite mints for it.
		Instant: func(minted time.Time) time.Time { return minted.Truncate(time.Microsecond) },
		// The operations are a network away and the first value this factory
		// builds migrates the schema before it serves anything.
		Window: 30 * time.Second,
	})
}

func checkpointPool(t *testing.T, connections int) *sql.DB {
	t.Helper()
	pool, err := sql.Open("pgx", os.Getenv(testDSN))
	if err != nil {
		t.Fatalf("a pool of this case's own could not be opened: %v", err)
	}
	pool.SetMaxOpenConns(connections)
	pool.SetMaxIdleConns(2)
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

func preparedCheckpoints(t *testing.T, pool *sql.DB, schema Schema, management SchemaManagement) *Checkpoints {
	t.Helper()
	held, err := NewCheckpoints(CheckpointSpec{DB: pool, Source: crudsql.Postgres(pool), Schema: schema, SchemaManagement: management})
	if err != nil {
		t.Fatalf("a checkpoint store over %+v was refused: %v", schema, err)
	}
	t.Cleanup(func() { _ = held.Close() })
	if err := held.Prepare(context.WithoutCancel(t.Context())); err != nil {
		t.Fatalf("a checkpoint store over %q did not prepare it: %v", schema.Name, err)
	}
	return held
}

func preparedStoreOver(t *testing.T, pool *sql.DB, schema Schema, management SchemaManagement) *Store {
	t.Helper()
	store, err := New(Spec{DB: pool, Source: crudsql.Postgres(pool), Schema: schema, SchemaManagement: management})
	if err != nil {
		t.Fatalf("a store over %+v was refused: %v", schema, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Prepare(context.WithoutCancel(t.Context())); err != nil {
		t.Fatalf("a store over %q did not prepare it: %v", schema.Name, err)
	}
	return store
}

func beginOnPool(t *testing.T, ctx context.Context, pool *sql.DB) (context.Context, eventtest.Tx) {
	t.Helper()
	source := crudsql.Postgres(pool)
	beginner, found := crud.BeginnerOf(source)
	if !found {
		t.Fatal("the pool's own data source cannot begin a transaction, so no section below could bind one")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning a transaction on the pool answered %v", err)
	}
	return crud.BindExecutor(ctx, source, tx), tx
}

// A cursor of this schema's log, minted by writing to it and reading the page
// back, so what the sections save is what a walk of this store would have
// persisted rather than a literal invented here.
type pgMinter struct {
	store *Store
	at    atomic.Uint64
	mutex sync.Mutex
	last  event.Cursor
}

func (this *pgMinter) next(t *testing.T) event.Cursor {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	this.mutex.Lock()
	defer this.mutex.Unlock()
	stream := aStream("eventpg.checkpoints", "cursor/"+strconv.FormatUint(this.at.Add(1), 10))
	request := event.AppendRequest{Stream: stream, Records: []event.Record{{Type: "eventpg.checkpoints.minted", Revision: 1, Payload: []byte("{}")}}}
	if err := this.store.Append(ctx, request); err != nil {
		t.Fatalf("the cursor this suite saves could not be minted: %v", err)
	}
	for {
		page, minted, err := this.store.ReadAll(ctx, this.last)
		if err != nil {
			t.Fatalf("reading the log for a cursor to save answered %v", err)
		}
		if minted == "" {
			t.Fatal("a walk of this store's log answered no cursor, so nothing below saves one")
		}
		this.last = minted
		if len(page) == 0 {
			return minted
		}
	}
}

// §UC-103's live half: two replicas of one projection both read advance n and
// both apply the page, and exactly one of them may record it. The control is the
// single saver afterwards, so a fence that refused everything fails here.
func TestEightSaversAtOneAdvanceLeaveOneWinnerAndSevenConflicts(t *testing.T) {
	ctx := context.WithoutCancel(t.Context())
	schema := Schema{Name: scratch(t, "eventpg_s2_cp_race")}
	pool := checkpointPool(t, 10)
	held := preparedCheckpoints(t, pool, schema, ManageSchema)
	minting := &pgMinter{store: preparedStoreOver(t, pool, schema, VerifySchema)}
	name := "orders.v1"

	saveOne(t, ctx, held, event.Checkpoint{Projection: name, Cursor: minting.next(t), Advance: 1})

	const savers = 8
	answers := make(chan error, savers)
	cursors := make([]event.Cursor, savers)
	for index := range savers {
		cursors[index] = minting.next(t)
	}
	start := make(chan struct{})
	for index := range savers {
		presented := event.Checkpoint{Projection: name, Cursor: cursors[index], Advance: 2,
			Progress: event.Progress{Highest: event.Position(index + 1), Applied: 1, At: instant()}}
		go func() {
			<-start
			answers <- held.Save(ctx, presented)
		}()
	}
	close(start)

	landed, conflicted := 0, 0
	for range savers {
		switch err := <-answers; {
		case err == nil:
			landed++
		case err.Error() == event.Failure(event.Conflict, nil).Error():
			conflicted++
		default:
			t.Errorf("one of %d savers at one advance answered %v, where a loser is refused a conflict", savers, err)
		}
	}
	if landed != 1 || conflicted != savers-1 {
		t.Fatalf("%d of %d savers at one advance landed and %d were refused a conflict, so two replicas advanced one checkpoint and each is applying pages the other's cursor has passed",
			landed, savers, conflicted)
	}

	row := loadOne(t, ctx, held, name)
	if row.Advance != 2 {
		t.Fatalf("the row is at advance %d after one winner of %d saves at advance 2", row.Advance, savers)
	}
	winner := false
	for _, cursor := range cursors {
		winner = winner || row.Cursor == cursor
	}
	if !winner {
		t.Fatal("the row holds a cursor none of the eight savers presented")
	}
	inDatabase := storedCheckpoint(t, schema, name)
	if inDatabase.advance != 2 || event.Cursor(inDatabase.cursor) != row.Cursor {
		t.Fatalf("the database holds advance %d and %d cursor bytes where this store answered advance %d, so the store's account of itself is not the row",
			inDatabase.advance, len(inDatabase.cursor), row.Advance)
	}

	t.Run("one saver alone advances repeatedly", func(t *testing.T) {
		for advance := uint64(3); advance <= 6; advance++ {
			saveOne(t, ctx, held, event.Checkpoint{Projection: name, Cursor: minting.next(t), Advance: advance})
		}
		if row := loadOne(t, ctx, held, name); row.Advance != 6 {
			t.Fatalf("four saves after the contended round left the row at advance %d, so this fence refuses everything rather than all but one", row.Advance)
		}
	})
}

// Retiring a live projection is the one moment a save and a removal meet, and
// the row lock is what makes the meeting exact: the DELETE is held inside a
// transaction, the save at the next advance blocks on it, and the removal
// commits while the save is in flight. Whichever landed first, the row is gone
// afterwards — a save above advance 1 moves a row and never creates one, so a
// projection an operator retired cannot go on writing into a read model it was
// cut over from. The control is the same save with nothing racing it.
func TestASaveAboveAdvanceOneCannotResurrectARowAForgetRemoved(t *testing.T) {
	ctx := context.WithoutCancel(t.Context())
	schema := Schema{Name: scratch(t, "eventpg_s2_cp_retire")}
	pool := checkpointPool(t, 6)
	held := preparedCheckpoints(t, pool, schema, ManageSchema)
	minting := &pgMinter{store: preparedStoreOver(t, pool, schema, VerifySchema)}
	name := "orders.v1"

	for advance := uint64(1); advance <= 5; advance++ {
		saveOne(t, ctx, held, event.Checkpoint{Projection: name, Cursor: minting.next(t), Advance: advance,
			Progress: event.Progress{Highest: event.Position(advance), Applied: advance, At: instant()}})
	}

	retiring, tx := beginOnPool(t, ctx, pool)
	if err := held.Forget(retiring, name); err != nil {
		t.Fatalf("forgetting %q inside a unit of work answered %v", name, err)
	}
	answers := make(chan error, 1)
	presented := event.Checkpoint{Projection: name, Cursor: minting.next(t), Advance: 6,
		Progress: event.Progress{Highest: 6, Applied: 6, At: instant()}}
	go func() { answers <- held.Save(ctx, presented) }()
	time.Sleep(500 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("committing the unit that retired %q answered %v", name, err)
	}
	classifiedAs(t, <-answers, event.Conflict, "a save at advance 6 against a row a committed forget removed")

	var left int
	row := liveDB(t).QueryRowContext(ctx, "SELECT count(*) FROM "+quoteIdentifier(schema.Name)+
		".checkpoints WHERE projection = $1", name)
	if err := row.Scan(&left); err != nil {
		t.Fatalf("counting the rows of %q answered %v", name, err)
	}
	if left != 0 {
		stored := storedCheckpoint(t, schema, name)
		t.Fatalf("a retired projection holds %d row(s), at advance %d, which no save at advance 1 ever created — the retirement an operator watched succeed was silently undone",
			left, stored.advance)
	}
	if after := loadOne(t, ctx, held, name); !after.Fresh() {
		t.Fatalf("a retired projection answers advance %d", after.Advance)
	}

	t.Run("the same save with nothing racing it lands", func(t *testing.T) {
		control := "orders.v2"
		for advance := uint64(1); advance <= 5; advance++ {
			saveOne(t, ctx, held, event.Checkpoint{Projection: control, Cursor: minting.next(t), Advance: advance,
				Progress: event.Progress{Highest: event.Position(advance), Applied: advance, At: instant()}})
		}
		sixth := event.Checkpoint{Projection: control, Cursor: minting.next(t), Advance: 6,
			Progress: event.Progress{Highest: 6, Applied: 6, At: instant()}}
		saveOne(t, ctx, held, sixth)
		if stored := storedCheckpoint(t, schema, control); stored.advance != 6 || event.Cursor(stored.cursor) != sixth.Cursor {
			t.Fatalf("the uncontended save left the table at advance %d, so the refusal above is the race's and not this statement's", stored.advance)
		}
	})
}

// The kernel publishes a ceiling on what a store may mint and this schema keeps
// a column of exactly that width, so a cursor at the ceiling is one this store
// owes room for. One byte over is refused before a statement is issued, because
// a check-constraint violation reaches the classifier with the backend alive and
// becomes a backend failure a consumer retries without limit.
func TestASaveOfACursorAtExactlyTheCeilingLands(t *testing.T) {
	ctx := context.WithoutCancel(t.Context())
	schema := Schema{Name: scratch(t, "eventpg_s2_cp_ceiling")}
	pool := checkpointPool(t, 4)
	held := preparedCheckpoints(t, pool, schema, ManageSchema)
	name := "orders.v1"

	widest := event.Cursor(strings.Repeat("c", event.MaxCursorBytes))
	written := event.Checkpoint{Projection: name, Cursor: widest, Advance: 1, Progress: event.Progress{Highest: 3, Applied: 3, At: instant()}}
	saveOne(t, ctx, held, written)

	row := loadOne(t, ctx, held, name)
	if len(row.Cursor) != event.MaxCursorBytes || row.Cursor != widest {
		t.Fatalf("a cursor of exactly %d bytes came back as %d", event.MaxCursorBytes, len(row.Cursor))
	}
	if stored := storedCheckpoint(t, schema, name); len(stored.cursor) != event.MaxCursorBytes {
		t.Fatalf("the column holds %d bytes where the save presented %d", len(stored.cursor), event.MaxCursorBytes)
	}

	over := event.Checkpoint{Projection: name, Cursor: widest + "c", Advance: 2}
	classifiedAs(t, held.Save(ctx, over), event.Refused, "a save of a cursor one byte over the ceiling")
	if row := loadOne(t, ctx, held, name); row.Cursor != widest || row.Advance != 1 {
		t.Fatalf("the refused save moved the row to advance %d with %d cursor bytes", row.Advance, len(row.Cursor))
	}
}

// D11's case: a cursor is unconstrained bytes the log mints and this store reads
// nothing in, so the column that holds one is bytea. The control is the same two
// bytes through a text column, which are two server errors — so this case is
// about the column's type and not about the store's own copying.
func TestACursorOfArbitraryBytesRoundTripsThroughTheColumn(t *testing.T) {
	ctx := context.WithoutCancel(t.Context())
	schema := Schema{Name: scratch(t, "eventpg_s2_cp_bytes")}
	pool := checkpointPool(t, 4)
	held := preparedCheckpoints(t, pool, schema, ManageSchema)

	for _, carried := range []struct {
		what  string
		bytes []byte
	}{
		{"a NUL", []byte("vve1\x00\xde\xad\xbe\xef")},
		{"an invalid UTF-8 byte", []byte("\x00\xff\x00\xff")},
	} {
		name := "orders." + carried.what
		cursor := event.Cursor(carried.bytes)
		saveOne(t, ctx, held, event.Checkpoint{Projection: name, Cursor: cursor, Advance: 1, Progress: event.Progress{At: instant()}})
		if row := loadOne(t, ctx, held, name); row.Cursor != cursor {
			t.Errorf("a cursor carrying %s was saved as %x and answered as %x", carried.what, carried.bytes, []byte(row.Cursor))
		}
		if held := hexOfStoredCursor(t, schema, name); held != hex.EncodeToString(carried.bytes) {
			t.Errorf("the database holds \\x%s for a cursor carrying %s where the save presented \\x%s, read out of the column rather than out of this store's account of itself",
				held, carried.what, hex.EncodeToString(carried.bytes))
		}
	}

	t.Run("the same bytes through a text column are two server errors", func(t *testing.T) {
		mustExecute(t, "CREATE TABLE "+quoteIdentifier(schema.Name)+".astext (held text NOT NULL)")
		for _, refused := range []struct {
			what      string
			statement string
		}{
			{"a NUL", "INSERT INTO " + quoteIdentifier(schema.Name) + ".astext VALUES (chr(0))"},
			{"an invalid UTF-8 byte", "INSERT INTO " + quoteIdentifier(schema.Name) + ".astext VALUES (convert_from('\\xc3'::bytea, 'UTF8'))"},
		} {
			if err := execute(t, refused.statement); err == nil {
				t.Errorf("a text column admitted %s, so the column type this case is about is not the reason the cursor round-trips", refused.what)
			}
		}
	})
}

// D12's live half, and the two lines are two different parties: the store owes
// an answer to a caller presenting the origin as a resume point, and the column
// owes one to anything that reaches the table another way.
func TestAnEmptyCursorIsRefusedByTheDoorAndByTheColumn(t *testing.T) {
	ctx := context.WithoutCancel(t.Context())
	schema := Schema{Name: scratch(t, "eventpg_s2_cp_empty")}
	pool := checkpointPool(t, 4)
	held := preparedCheckpoints(t, pool, schema, ManageSchema)
	minting := &pgMinter{store: preparedStoreOver(t, pool, schema, VerifySchema)}
	name := "orders.v1"

	recount(t)
	classifiedAs(t, held.Save(ctx, event.Checkpoint{Projection: name, Cursor: "", Advance: 1}),
		event.Refused, "a save of the empty cursor against a name that holds no row")
	if row := loadOne(t, ctx, held, name); !row.Fresh() {
		t.Fatalf("a save of the empty cursor created the row anyway, at advance %d", row.Advance)
	}

	written := event.Checkpoint{Projection: name, Cursor: minting.next(t), Advance: 1, Progress: event.Progress{At: instant()}}
	saveOne(t, ctx, held, written)
	classifiedAs(t, held.Save(ctx, event.Checkpoint{Projection: name, Cursor: "", Advance: 2}),
		event.Refused, "a save of the empty cursor over a row that exists")
	if row := loadOne(t, ctx, held, name); !sameCheckpoint(row, written) {
		t.Fatalf("the refused save left the row at %+v where it held %+v", row, written)
	}

	t.Run("the column refuses one that reaches the table another way", func(t *testing.T) {
		err := execute(t, "INSERT INTO "+quoteIdentifier(schema.Name)+
			".checkpoints (projection, cursor, advance, highest, applied, quarantined, updated_at) "+
			"VALUES ('by hand', ''::bytea, 1, 0, 0, 0, now())")
		if err == nil {
			t.Fatal("the column admitted an empty cursor, so the >= 1 bound is not deployed and a row an operator restores by hand resumes a projection at the origin of the log")
		}
		if !strings.Contains(err.Error(), "checkpoints_cursor_check") {
			t.Errorf("the insert was refused by %v rather than by the cursor's own check constraint", err)
		}
	})
}

// §INV-076: phase 3 adds no sentinel and no class, so every failure either
// checkpoint store answers is nil, a bare context error, or an event.Failure
// carrying one of the seven outcomes. Both stores are driven here because the
// invariant is the vocabulary's rather than one implementation's.
func TestEveryFailureTheCheckpointStoresAnswerCarriesOneOfTheSeven(t *testing.T) {
	ctx := context.WithoutCancel(t.Context())
	schema := Schema{Name: scratch(t, "eventpg_s2_cp_vocabulary")}
	pool := checkpointPool(t, 4)
	live := preparedCheckpoints(t, pool, schema, ManageSchema)
	minting := &pgMinter{store: preparedStoreOver(t, pool, schema, VerifySchema)}

	log, err := eventmemory.NewLog(eventmemory.LogSpec{})
	if err != nil {
		t.Fatal(err)
	}
	memory, err := eventmemory.NewCheckpoints(eventmemory.CheckpointSpec{Log: log})
	if err != nil {
		t.Fatal(err)
	}

	closed := preparedCheckpoints(t, pool, schema, VerifySchema)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	unprepared, err := NewCheckpoints(CheckpointSpec{DB: pool, Source: crudsql.Postgres(pool), Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()

	cursor := minting.next(t)
	saveOne(t, ctx, live, event.Checkpoint{Projection: "orders.v1", Cursor: cursor, Advance: 1, Progress: event.Progress{At: instant()}})
	saveOne(t, ctx, memory, event.Checkpoint{Projection: "orders.v1", Cursor: "eventmemory:1", Advance: 1})

	answers := []struct {
		what string
		err  error
	}{
		{"a losing save on the live store", live.Save(ctx, event.Checkpoint{Projection: "orders.v1", Cursor: cursor, Advance: 1})},
		{"a save of the empty cursor on the live store", live.Save(ctx, event.Checkpoint{Projection: "orders.v1", Cursor: "", Advance: 2})},
		{"a save of an unnamed projection on the live store", live.Save(ctx, event.Checkpoint{Cursor: cursor, Advance: 1})},
		{"a save through a closed live store", closed.Save(ctx, event.Checkpoint{Projection: "orders.v1", Cursor: cursor, Advance: 2})},
		{"a forget through a closed live store", closed.Forget(ctx, "orders.v1")},
		{"a save through a live store that never verified its schema", unprepared.Save(ctx, event.Checkpoint{Projection: "orders.v1", Cursor: cursor, Advance: 2})},
		{"a save on the live store under a cancelled context", live.Save(cancelled, event.Checkpoint{Projection: "orders.v1", Cursor: cursor, Advance: 2})},
		{"a losing save on the memory store", memory.Save(ctx, event.Checkpoint{Projection: "orders.v1", Cursor: "eventmemory:2", Advance: 1})},
		{"a save of the empty cursor on the memory store", memory.Save(ctx, event.Checkpoint{Projection: "orders.v1", Cursor: "", Advance: 2})},
		{"a save on the memory store under a cancelled context", memory.Save(cancelled, event.Checkpoint{Projection: "orders.v1", Cursor: "eventmemory:2", Advance: 2})},
	}
	_, loading := live.Load(cancelled, "orders.v1")
	answers = append(answers, struct {
		what string
		err  error
	}{"a load on the live store under a cancelled context", loading})
	for _, answered := range answers {
		if answered.err == nil {
			t.Errorf("%s was admitted, so this case asserts nothing about how it is classified", answered.what)
			continue
		}
		if errors.Is(answered.err, context.Canceled) || errors.Is(answered.err, context.DeadlineExceeded) {
			continue
		}
		if !oneOfTheSeven(answered.err) {
			t.Errorf("%s answered %q, which is neither a context error nor a failure carrying one of the seven outcomes — so phase 3 added a class the kernel's own map does not read", answered.what, answered.err)
		}
	}
}

func oneOfTheSeven(err error) bool {
	for _, outcome := range []event.Outcome{
		event.NotWritten, event.Unconfirmed, event.Conflict, event.Closed,
		event.BadCursor, event.Unclassified, event.Refused,
	} {
		if err.Error() == event.Failure(outcome, nil).Error() {
			return true
		}
	}
	return false
}

// Field by field and never with ==, because time.Time's equality carries a
// monotonic reading and a *Location: a row read back through a driver that
// answers a session zone would otherwise differ for a difference that is not one.
func sameCheckpoint(first, second event.Checkpoint) bool {
	return first.Projection == second.Projection &&
		first.Cursor == second.Cursor &&
		first.Advance == second.Advance &&
		first.Progress.Highest == second.Progress.Highest &&
		first.Progress.Applied == second.Progress.Applied &&
		first.Progress.Quarantined == second.Progress.Quarantined &&
		first.Progress.At.Equal(second.Progress.At)
}

// The instant a row keeps is a column's width and not a Go nanosecond, so what
// a case saves is already at the precision the column holds — and what it then
// asserts is that the value handed in is the value answered back, which a second
// clock in the row would break.
func instant() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func saveOne(t *testing.T, ctx context.Context, held event.Checkpoints, checkpoint event.Checkpoint) {
	t.Helper()
	if err := held.Save(ctx, checkpoint); err != nil {
		t.Fatalf("saving %q at advance %d answered %v", checkpoint.Projection, checkpoint.Advance, err)
	}
}

func loadOne(t *testing.T, ctx context.Context, held event.Checkpoints, projection string) event.Checkpoint {
	t.Helper()
	found, err := held.Load(ctx, projection)
	if err != nil {
		t.Fatalf("loading the checkpoint of %q answered %v", projection, err)
	}
	return found
}

type checkpointRow struct {
	cursor      []byte
	advance     int64
	highest     int64
	applied     int64
	quarantined int64
	updatedAt   time.Time
}

// Read out of the table rather than out of the store's account of itself.
func storedCheckpoint(t *testing.T, schema Schema, projection string) checkpointRow {
	t.Helper()
	row, found := maybeStoredCheckpoint(t, schema, projection)
	if !found {
		t.Fatalf("%s.checkpoints holds no row for %q this case could read", schema.Name, projection)
	}
	return row
}

// The same read where absence is an answer rather than a failure: a projection
// that never saved and one that saved and was forgotten both leave no row, and a
// case about either has to be able to say so.
func maybeStoredCheckpoint(t *testing.T, schema Schema, projection string) (checkpointRow, bool) {
	t.Helper()
	var row checkpointRow
	held := liveDB(t).QueryRowContext(context.WithoutCancel(t.Context()),
		"SELECT cursor, advance, highest, applied, quarantined, updated_at FROM "+
			quoteIdentifier(schema.Name)+"."+checkpointsTable+" WHERE projection = $1", projection)
	switch err := held.Scan(&row.cursor, &row.advance, &row.highest, &row.applied, &row.quarantined, &row.updatedAt); {
	case errors.Is(err, sql.ErrNoRows):
		return checkpointRow{}, false
	case err != nil:
		t.Fatalf("%s.checkpoints holds a row for %q this case could not read: %v", schema.Name, projection, err)
	}
	return row, true
}

func hexOfStoredCursor(t *testing.T, schema Schema, projection string) string {
	t.Helper()
	var held string
	row := liveDB(t).QueryRowContext(t.Context(), "SELECT encode(cursor, 'hex') FROM "+
		quoteIdentifier(schema.Name)+".checkpoints WHERE projection = $1", projection)
	if err := row.Scan(&held); err != nil {
		t.Fatalf("%s.checkpoints holds no cursor for %q: %v", schema.Name, projection, err)
	}
	return held
}
