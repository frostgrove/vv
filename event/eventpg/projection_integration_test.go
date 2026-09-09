//go:build integration

package eventpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/projection"
	"github.com/frostgrove/vv/runtime"
)

// Two live instances of one projection name, which is what every rolling deploy
// runs for a few seconds on purpose and what no single-instance case ever
// exercises: the contention branch is not reached at N=1, so an implementation
// that is wrong at N=2 passes everything else in this suite. The reference this
// was adjudicated against runs its whole acceptance suite at
// --scale event-sourcing-app=2 for that reason.
//
// What only a database can answer is [[D-133]] §3: whether the loser is refused
// BEFORE its handler runs. Under InUnit the advance is presented first, and at
// advance 1 there is no row to lock — the store's claim is an INSERT … ON
// CONFLICT DO NOTHING, so what holds the loser is PostgreSQL's speculative
// insertion rather than a row lock, and whether that is enough is a question
// about the server and not about this package. Under AfterApply there is no
// transaction to hold across a handler call at all, so both instances apply the
// overlapping page and at-least-once is the whole of what is promised.
//
// The interleaving is DRIVEN and not hoped for: the gate holds each instance's
// first save until both have issued one, so both are contending for advance 1
// over the page they both read. Without it the two instances interleave by luck
// and a run where one drained the log before the other woke reads as a pass.
// Every assertion is made against the read model's rows in the database.
func TestTwoLiveInstancesOfOneNameOverOneSchema(t *testing.T) {
	payloads := make([]string, 0, 24)
	for index := range 24 {
		payloads = append(payloads, fmt.Sprintf("event-%02d", index))
	}
	contended := payloads[0]

	for _, mode := range []struct {
		what    string
		advance projection.Advance
		applied int
	}{
		{"under InUnit the loser is refused before its handler runs", projection.InUnit, 1},
		{"under AfterApply the loser has already applied", projection.AfterApply, 2},
	} {
		t.Run(mode.what, func(t *testing.T) {
			stand := newProjectionStand(t, payloads, 2)
			handled := &counting{}
			for range 2 {
				stand.start(t, mode.advance, handled)
			}
			stand.drained(t, len(payloads))

			if !stand.gate.opened() {
				t.Fatal("the two instances never issued a save at the same advance, so nothing below was contended and this case proves nothing")
			}
			if !stand.overtaken() {
				t.Fatal("neither instance ever published ErrOvertaken, so the fence was never lost and this case proves nothing")
			}

			rows := stand.readModel(t)
			for _, payload := range payloads {
				if rows[payload] == 0 {
					t.Fatalf("%q is in no row of the read model, and a checkpoint that advanced past an event nobody applied is a lost event", payload)
				}
			}
			if rows[contended] != mode.applied {
				t.Fatalf("the page both instances claimed left %q in %d rows of the read model where this mode leaves it in %d: the claim was taken %s the handler",
					contended, rows[contended], mode.applied, map[bool]string{true: "before", false: "after"}[mode.applied == 1])
			}
			if mode.applied == 1 && handled.total() != uint64(len(payloads)) {
				t.Fatalf("the handlers were called for %d envelopes over a log of %d, where a loser refused before its handler runs calls none", handled.total(), len(payloads))
			}
			stand.healthy(t)
		})
	}

	// The control, and it is what makes both arms above mean anything: a
	// projection that halted on its first page, or a fence that refused
	// everything, leaves a read model this case could not tell from a contended
	// one.
	t.Run("the control: one instance alone drains the same log and never halts", func(t *testing.T) {
		stand := newProjectionStand(t, payloads, 1)
		handled := &counting{}
		held := stand.start(t, projection.InUnit, handled)
		stand.drained(t, len(payloads))

		rows := stand.readModel(t)
		for _, payload := range payloads {
			if rows[payload] != 1 {
				t.Fatalf("one instance alone left %q in %d rows", payload, rows[payload])
			}
		}
		if handled.total() != uint64(len(payloads)) {
			t.Fatalf("one instance alone called its handler for %d envelopes over a log of %d", handled.total(), len(payloads))
		}
		if stand.overtaken() {
			t.Fatal("one instance alone published ErrOvertaken, so the contention above is not what the two arms measured")
		}
		waitFor(t, "one instance alone reached PhaseFollowing", func() bool {
			return held.State().Phase == projection.PhaseFollowing
		})
		stand.healthy(t)
	})
}

// Holds each instance's first save until every instance has issued one, so the
// contention this case is about is a fact of the run rather than of the
// scheduler. It opens once and lets everything through afterwards.
type gate struct {
	mutex   sync.Mutex
	arrived int
	need    int
	open    chan struct{}
	tripped atomic.Bool
}

func newGate(need int) *gate { return &gate{need: need, open: make(chan struct{})} }

func (this *gate) arrive(ctx context.Context) {
	this.mutex.Lock()
	this.arrived++
	if this.arrived >= this.need {
		this.tripped.Store(true)
		select {
		case <-this.open:
		default:
			close(this.open)
		}
	}
	this.mutex.Unlock()
	select {
	case <-this.open:
	case <-ctx.Done():
	case <-time.After(20 * time.Second):
	}
}

func (this *gate) opened() bool { return this.tripped.Load() }

// One instance's view of the gate: only its first save waits, because every save
// after it is one the other instance is not racing for.
type gated struct {
	event.Checkpoints
	gate   *gate
	waited atomic.Bool
}

func (this *gated) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	if this.waited.CompareAndSwap(false, true) {
		this.gate.arrive(ctx)
	}
	return this.Checkpoints.Save(ctx, checkpoint)
}

type counting struct {
	mutex  sync.Mutex
	counts map[string]uint64
}

func (this *counting) count(payloads ...string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.counts == nil {
		this.counts = map[string]uint64{}
	}
	for _, payload := range payloads {
		this.counts[payload]++
	}
}

func (this *counting) total() uint64 {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	var held uint64
	for _, count := range this.counts {
		held += count
	}
	return held
}

// One schema, one pool, one log and one read-model table, with as many
// Projection values over them as a case starts. The log answers one envelope a
// page, so the two instances contend over every page rather than over one; and
// the pool is wide enough for both to hold a transaction and still read, because
// a pool at its limit starves one of them and looks exactly like the mutual
// exclusion this case is here to measure.
type projectionStand struct {
	schema    Schema
	pool      *sql.DB
	source    crud.Source
	log       event.Log
	gate      *gate
	held      []*projection.Projection
	contested atomic.Bool
}

func newProjectionStand(t *testing.T, payloads []string, instances int) *projectionStand {
	t.Helper()
	schema := Schema{Name: scratchName()}
	pool := checkpointPool(t, 12)
	t.Cleanup(func() { dropSchema(t, schema.Name) })

	store, err := New(Spec{DB: pool, Source: crudsql.Postgres(pool), Schema: schema, SchemaManagement: ManageSchema, MaxRead: 1})
	if err != nil {
		t.Fatalf("a store over %+v was refused: %v", schema, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.WithoutCancel(t.Context())
	if err := store.Prepare(ctx); err != nil {
		t.Fatalf("a store over %q did not prepare it: %v", schema.Name, err)
	}
	preparedCheckpoints(t, pool, schema, ManageSchema)

	if _, err := pool.ExecContext(ctx, "CREATE TABLE "+quoteIdentifier(schema.Name)+".read_model (id bigserial PRIMARY KEY, payload text NOT NULL)"); err != nil {
		t.Fatalf("the read model this case asserts against could not be created: %v", err)
	}
	for _, payload := range payloads {
		stream := aStream("eventpg.projection", "orders/"+payload)
		request := event.AppendRequest{Stream: stream, Records: []event.Record{{Type: "eventpg.projection.placed", Revision: 1, Payload: []byte(payload)}}}
		if err := store.Append(ctx, request); err != nil {
			t.Fatalf("appending %q answered %v", payload, err)
		}
	}
	return &projectionStand{schema: schema, pool: pool, source: crudsql.Postgres(pool), log: event.ReadOnly(store), gate: newGate(instances)}
}

func (this *projectionStand) start(t *testing.T, advance projection.Advance, handled *counting) *projection.Projection {
	t.Helper()
	spec := projection.Spec{
		Name:        "orders",
		Log:         this.log,
		Checkpoints: &gated{Checkpoints: preparedCheckpoints(t, this.pool, this.schema, VerifySchema), gate: this.gate},
		Handler:     this.writes(handled),
		Advance:     advance,
		Idle:        20 * time.Millisecond,
		Backoff:     projection.Backoff{First: 5 * time.Millisecond, Max: 25 * time.Millisecond},
		Observer:    projection.ObserverFunc(this.observed),
		Ticks:       runtime.SystemTicks,
	}
	if advance == projection.InUnit {
		spec.Destination = this.source
		spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
			return crud.InNewTx(ctx, this.source, work)
		}
	}
	held, err := projection.New(spec)
	if err != nil {
		t.Fatalf("a well-formed projection was refused: %v", err)
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	returned := make(chan error, 1)
	go func() { returned <- held.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-returned:
		case <-time.After(30 * time.Second):
			t.Error("Run did not return after its context was cancelled")
		}
	})
	this.held = append(this.held, held)
	return held
}

func (this *projectionStand) observed(state projection.State) {
	if errors.Is(state.Err, projection.ErrOvertaken) {
		this.contested.Store(true)
	}
}

func (this *projectionStand) overtaken() bool { return this.contested.Load() }

// The handler writes through whatever the pass bound: the unit's transaction
// under InUnit, so its rows go back with a refused claim, and the pool under
// AfterApply, so they do not.
func (this *projectionStand) writes(handled *counting) projection.Handler {
	return projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
		on := executor(this.pool)
		if bound, found := crud.ExecutorFor(ctx, this.source); found {
			tx, taken := crudsql.Transaction(bound)
			if !taken {
				return errors.New("the executor this pass bound is not a transaction")
			}
			on = tx
		}
		for _, envelope := range batch.Envelopes {
			if _, err := on.ExecContext(ctx, "INSERT INTO "+quoteIdentifier(this.schema.Name)+".read_model (payload) VALUES ($1)", string(envelope.Payload)); err != nil {
				return err
			}
		}
		handled.count(payloadsOfEnvelopes(batch.Envelopes)...)
		return nil
	})
}

func (this *projectionStand) drained(t *testing.T, events int) {
	t.Helper()
	waitFor(t, fmt.Sprintf("the checkpoint row reached position %d", events), func() bool {
		for _, held := range this.held {
			if state := held.State(); state.Phase == projection.PhaseHalted {
				t.Fatalf("an instance halted with %v, and a second live writer at one name is not a reason to stop applying", state.Err)
			}
		}
		return this.stored(t).Progress.Highest == event.Position(events)
	})
}

func (this *projectionStand) stored(t *testing.T) event.Checkpoint {
	t.Helper()
	held, err := preparedCheckpoints(t, this.pool, this.schema, VerifySchema).Load(context.WithoutCancel(t.Context()), "orders")
	if err != nil {
		t.Fatalf("reading the checkpoint row out of the database answered %v", err)
	}
	return held
}

func (this *projectionStand) readModel(t *testing.T) map[string]int {
	t.Helper()
	rows, err := this.pool.QueryContext(context.WithoutCancel(t.Context()), "SELECT payload, count(*) FROM "+quoteIdentifier(this.schema.Name)+".read_model GROUP BY payload")
	if err != nil {
		t.Fatalf("reading the read model answered %v", err)
	}
	defer func() { _ = rows.Close() }()
	held := map[string]int{}
	for rows.Next() {
		var payload string
		var count int
		if err := rows.Scan(&payload, &count); err != nil {
			t.Fatalf("a read model row could not be read: %v", err)
		}
		held[payload] = count
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the read model answered %v", err)
	}
	return held
}

func (this *projectionStand) healthy(t *testing.T) {
	t.Helper()
	for index, held := range this.held {
		if state := held.State(); state.Phase == projection.PhaseHalted {
			t.Fatalf("instance %d halted with %v", index+1, state.Err)
		}
	}
}

func payloadsOfEnvelopes(envelopes []event.Envelope) []string {
	held := make([]string, 0, len(envelopes))
	for _, envelope := range envelopes {
		held = append(held, string(envelope.Payload))
	}
	return held
}

// The two advance modes at one kill point, and what tells them apart is a
// measurement rather than a sentence: the process dies between the handler's
// write and the checkpoint, and what the database holds afterwards is read back
// out of it.
//
// AfterApply commits the handler's rows and saves after them, so the kill leaves
// rows no checkpoint accounts for and the restart delivers the same page again —
// the at-least-once window §UC-106 names, bounded by MaxRead and by nothing
// else, and closed by the handler's own idempotency rather than by this
// framework. InUnit claims the advance inside the caller's unit before the
// handler runs, so the kill takes the rows back with it and the restart applies
// the page once. Neither leaves a checkpoint standing over a page nobody
// applied, which is the one outcome no mode may produce.
func TestTheTwoModesLeaveDifferentStateAtOneKillPoint(t *testing.T) {
	const (
		family = "eventpg.killpoint"
		wire   = "eventpg.killpoint.placed"
	)
	payloads := []string{"k-1", "k-2", "k-3", "k-4"}

	for _, mode := range []struct {
		what      string
		advance   projection.Advance
		survived  int
		afterward int
	}{
		{"AfterApply leaves the handler's rows with no checkpoint accounting for them", projection.AfterApply, len(payloads), 2},
		{"InUnit takes the handler's rows back with the advance", projection.InUnit, 0, 1},
	} {
		t.Run(mode.what, func(t *testing.T) {
			held := newProjectionCase(t, 8, 0)
			rows := held.destination(t, "read_model")
			held.writeEach(t, family, wire, payloads...)

			kill := newKillPoint(t)
			first := &deliveries{}
			spec := held.spec(t, "orders", rows.inserts(first, kill.fire))
			if mode.advance == projection.InUnit {
				spec = inUnit(spec, held.source, held.source)
			}
			killed := held.runOn(t, kill.ctx, kill.cancel, spec)

			if err := killed.stopped(t); !errors.Is(err, context.Canceled) {
				t.Fatalf("the killed projection's Run answered %v where the process it stands for died", err)
			}
			if !kill.struck() {
				t.Fatal("no handler ever ran, so the kill this case is about never happened and nothing below was measured")
			}
			if got := len(first.pages()); got != 1 {
				t.Fatalf("the handler was called %d times before the kill where the whole log is one page and the kill lands in it", got)
			}
			if got := rows.count(t); got != mode.survived {
				t.Fatalf("the read model holds %d rows after the kill where this mode leaves %d", got, mode.survived)
			}
			if row, found := held.row(t, "orders"); found {
				t.Fatalf("a checkpoint row at advance %d outlived a kill before any save was confirmed, and a checkpoint over a page nobody accounted for skips it for good", row.advance)
			}

			second := &deliveries{}
			restarted := held.run(t, held.spec(t, "orders", rows.inserts(second, nil)))
			restarted.following(t, "the restarted projection drained the log")

			tally := rows.tally(t)
			for _, payload := range payloads {
				if tally[payload] != mode.afterward {
					t.Fatalf("%q is in %d rows of the read model once the restart drained where this mode leaves it in %d", payload, tally[payload], mode.afterward)
				}
			}
			for _, page := range second.pages() {
				if page.Attempt != 1 {
					t.Fatalf("the page the restart delivered arrived at attempt %d, and a redelivery in a new process is a fresh delivery rather than a retry", page.Attempt)
				}
			}
			row, found := held.row(t, "orders")
			if !found || row.advance != 1 || row.highest == 0 {
				t.Fatalf("the restart left %+v (found %v) where one applied page is one save at advance 1 over a non-zero highest", row, found)
			}
		})
	}

	// The control, and it is what makes both arms above mean anything: with
	// nobody killing anything the same projection in the same wiring applies each
	// event once and saves once, so a mode that lost or duplicated a page for a
	// reason other than the kill fails here first.
	t.Run("the control: nothing kills it and each mode applies every event once", func(t *testing.T) {
		for _, advance := range []projection.Advance{projection.AfterApply, projection.InUnit} {
			t.Run(advance.String(), func(t *testing.T) {
				held := newProjectionCase(t, 8, 0)
				rows := held.destination(t, "read_model")
				held.writeEach(t, family, wire, payloads...)

				spec := held.spec(t, "orders", rows.inserts(&deliveries{}, nil))
				if advance == projection.InUnit {
					spec = inUnit(spec, held.source, held.source)
				}
				held.run(t, spec).following(t, "the projection drained the log")

				tally := rows.tally(t)
				for _, payload := range payloads {
					if tally[payload] != 1 {
						t.Fatalf("%q is in %d rows of the read model where a projection nobody interrupted applies it once", payload, tally[payload])
					}
				}
				if row, found := held.row(t, "orders"); !found || row.advance != 1 {
					t.Fatalf("the uninterrupted run left %+v (found %v) where one page is one save", row, found)
				}
			})
		}
	})
}

// UC-100: the resume is the row's doing and not the read model's. The second
// wiring shares nothing with the first — its own *sql.DB, its own Store, its own
// Checkpoints, its own Projection — so what carries the walk across the process
// boundary is the cursor in the checkpoint row and nothing a value remembered.
func TestAProjectionResumesThroughASecondValueOverOneBacking(t *testing.T) {
	const (
		family = "eventpg.resume"
		wire   = "eventpg.resume.placed"
	)
	before := []string{"r-1", "r-2", "r-3"}
	after := []string{"r-4", "r-5", "r-6"}
	whole := append(slices.Clone(before), after...)

	held := newProjectionCase(t, 8, 0)
	rows := held.destination(t, "read_model")
	held.writeEach(t, family, wire, before...)

	first := held.run(t, held.spec(t, "orders", rows.inserts(&deliveries{}, nil)))
	first.following(t, "the first wiring drained what the log held")
	first.stop(t)

	stopped, found := held.row(t, "orders")
	if !found {
		t.Fatal("the first wiring saved no checkpoint at all, so there is nothing for a second one to resume from")
	}
	held.writeEach(t, family, wire, after...)

	second := held.restarted(t, 0)
	resumed := second.run(t, second.spec(t, "orders", rows.through(second).inserts(&deliveries{}, nil)))
	resumed.following(t, "the second wiring drained what the first had not")

	if got := rows.rows(t); !slices.Equal(got, whole) {
		t.Fatalf("the read model holds %v where the two wirings between them apply each event once, in the log's own order", got)
	}
	moved, _ := held.row(t, "orders")
	if moved.advance <= stopped.advance {
		t.Fatalf("the checkpoint row is at advance %d where the first wiring left it at %d, so the second wiring saved nothing and the rows above are the first one's alone", moved.advance, stopped.advance)
	}
	if moved.highest <= stopped.highest {
		t.Fatalf("the checkpoint row reports highest %d where the first wiring left it at %d over a log that grew", moved.highest, stopped.highest)
	}

	// The control, and it is what makes the resume above the cursor's doing
	// rather than the read model's: the same second wiring, pointed at a name no
	// row was ever saved for, starts at the origin and applies the whole log.
	t.Run("the control: a name that was never saved starts at the origin", func(t *testing.T) {
		fresh := held.destination(t, "read_model_from_zero")
		afresh := second.run(t, second.spec(t, "orders-never-saved", fresh.through(second).inserts(&deliveries{}, nil)))
		afresh.following(t, "the never-saved name drained the whole log")
		if got := fresh.rows(t); !slices.Equal(got, whole) {
			t.Fatalf("a projection name with no row applied %v where absence is the origin and the origin is the whole log", got)
		}
	})
}

// UC-113 end to end: the walk's watermark is what a projection inherits, and
// what it buys is that a checkpoint never passes a position a writer could still
// commit. A projection over a naive `position > N` checkpoint passes every other
// case in this file and loses that event in production, silently and for good.
func TestAProjectionDoesNotPassAPositionAWriterCouldStillCommit(t *testing.T) {
	const family = "eventpg.projection.inflight"
	held := newProjectionCase(t, 12, 0)
	rows := held.destination(t, "read_model")
	holding, holder, drawn, committed := inFlightGap(t, held.store, held.schema, family)

	started := held.run(t, held.spec(t, "orders", rows.inserts(&deliveries{}, nil)))
	started.following(t, "the projection read the log while a writer held a position")

	if got := rows.count(t); got != 0 {
		t.Fatalf("the projection applied %d rows while position %d was still uncommitted, and a consumer that checkpointed past it never sees it", got, drawn)
	}
	if row, found := held.row(t, "orders"); found {
		t.Fatalf("the projection saved a checkpoint at highest %d while position %d could still commit below it", row.highest, drawn)
	}

	if err := holder.Commit(holding); err != nil {
		t.Fatalf("the writer that held the low position could not commit: %v", err)
	}
	waitFor(t, "the projection applied both positions once the writer committed", func() bool { return rows.count(t) == 2 })
	if got := rows.rows(t); !slices.Equal(got, []string{"the position a writer still holds", "the position that committed first"}) {
		t.Fatalf("the projection applied %v where the log holds position %d and then %d", got, drawn, committed)
	}
	if row, found := held.row(t, "orders"); !found || row.highest < committed {
		t.Fatalf("the checkpoint row is %+v (found %v) where the log's highest committed position is %d", row, found, committed)
	}

	// The control: a projection that stalls for ever passes the first half of
	// this case exactly as a correct one does.
	t.Run("the control: with no writer in flight the same projection delivers at once", func(t *testing.T) {
		quiet := newProjectionCase(t, 8, 0)
		into := quiet.destination(t, "read_model")
		quiet.writeEach(t, family, family+".held", "one", "two")
		quiet.run(t, quiet.spec(t, "orders", into.inserts(&deliveries{}, nil))).following(t, "the projection over a quiescent log drained it")
		if got := into.rows(t); !slices.Equal(got, []string{"one", "two"}) {
			t.Fatalf("a projection over a log nobody is writing to applied %v, so a projection that stalls for good would pass the case above", got)
		}
	})
}

// INV-071's live half. The read is outside every unit of work, and a burnt gap
// is where that stops being a style preference: a walk mints no settlement bound
// while a transaction of its own backing is bound, so a read issued inside the
// projection's own write transaction would stop at the first rolled-back append
// in the log and stay there for the life of that transaction. What that looks
// like from outside is a projection that applies nothing and reports nothing,
// which is why this case is written under a deadline.
func TestAProjectionInAUnitPassesABurntGap(t *testing.T) {
	const (
		family = "eventpg.projection.burnt"
		wire   = family + ".held"
	)
	held := newProjectionCase(t, 12, 0)
	rows := held.destination(t, "read_model")

	held.write(t, aStream(family, "A-1"), wire, "before the gap")
	burn(t, held.store, family, "A-burnt")
	held.write(t, aStream(family, "A-2"), wire, "after the gap")

	positions := storedPositions(t, held.schema)
	if len(positions) != 2 || positions[1] != positions[0]+2 {
		t.Fatalf("the log holds the positions %v where this case burns exactly one between two committed ones, so there is no gap below to pass", positions)
	}

	spec := inUnit(held.spec(t, "orders", rows.inserts(&deliveries{}, nil)), held.source, held.source)
	held.run(t, spec)
	waitFor(t, "the InUnit projection passed the burnt gap and applied both committed events — a read issued inside its own unit never settles one, and this deadline is what tells that apart from a slow database", func() bool {
		return rows.count(t) == 2
	})

	if got := rows.rows(t); !slices.Equal(got, []string{"before the gap", "after the gap"}) {
		t.Fatalf("the projection applied %v where the log holds the two committed events in position order", got)
	}
	if row, found := held.row(t, "orders"); !found || row.highest != positions[1] {
		t.Fatalf("the checkpoint row is %+v (found %v) where the highest committed position is %d", row, found, positions[1])
	}
}

// The sink writes its record through whatever the pass bound, exactly as the
// handler does. Under InUnit that is the unit's own transaction — [[D-133]]'s
// neighbour decision D5 — so the record and the advance commit together, and a
// record that outlived a rolled-back advance would be a row in this table with
// no checkpoint behind it.
type sink struct {
	into   *destination
	inside atomic.Bool
	refuse error
}

func (this *sink) Quarantine(ctx context.Context, quarantined projection.Quarantined) error {
	if this.refuse != nil {
		return this.refuse
	}
	if held, found := crud.ExecutorFor(ctx, this.into.source); found && crud.IsTransaction(held) {
		this.inside.Store(true)
	}
	_, err := this.into.on(ctx).ExecContext(ctx, "INSERT INTO "+this.into.table+" (payload) VALUES ($1)",
		string(quarantined.Envelope.Payload))
	return err
}

// §10(13): quarantine is envelope-granular, and the number that says so is read
// out of the checkpoint row rather than off Progress in memory. A page-granular
// quarantine loses up to MaxRead good events for one corrupt payload and reports
// the same phase while doing it.
//
// The mode is InUnit deliberately: the whole-page attempt that discovers the
// permanent failure has already applied the envelopes before it, and under
// AfterApply those rows are committed and the isolation pass writes them a
// second time — legal, at least once, and no longer an exact count of anything.
// Inside a unit they go back with the advance, so what the read model holds
// afterwards is exactly the isolation pass's own work.
func TestAQuarantineIsEnvelopeGranular(t *testing.T) {
	const (
		family  = "eventpg.projection.quarantine"
		wire    = family + ".held"
		corrupt = "q-corrupt"
	)
	payloads := []string{"q-1", "q-2", corrupt, "q-4", "q-5"}

	refusing := func(rows *destination) projection.Handler {
		return projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
			for _, envelope := range batch.Envelopes {
				if string(envelope.Payload) == corrupt {
					return fmt.Errorf("%w: %s at version %d is of a revision this build cannot read", event.ErrPayload, envelope.Stream, envelope.Version)
				}
				if _, err := rows.on(ctx).ExecContext(ctx, "INSERT INTO "+rows.table+" (payload) VALUES ($1)", string(envelope.Payload)); err != nil {
					return err
				}
			}
			return nil
		})
	}

	held := newProjectionCase(t, 8, 0)
	rows := held.destination(t, "read_model")
	quarantine := &sink{into: held.destination(t, "quarantined")}
	held.writeEach(t, family, wire, payloads...)

	spec := inUnit(held.spec(t, "orders", refusing(rows)), held.source, held.source)
	spec.OnPermanentFailure = projection.Quarantine
	spec.Quarantine = quarantine
	held.run(t, spec).following(t, "the projection passed the page the corrupt payload is in")

	if got := quarantine.into.rows(t); !slices.Equal(got, []string{corrupt}) {
		t.Fatalf("the sink holds %v where one envelope of the page was permanently unapplicable", got)
	}
	if !quarantine.inside.Load() {
		t.Fatal("the sink was called on a context carrying no transaction of the checkpoint store's source, so its record does not commit with the advance it accounts for")
	}
	if got := rows.rows(t); !slices.Equal(got, []string{"q-1", "q-2", "q-4", "q-5"}) {
		t.Fatalf("the read model holds %v where every envelope but the corrupt one applies", got)
	}
	row, found := held.row(t, "orders")
	if !found {
		t.Fatal("the projection quarantined an envelope and saved no checkpoint, so the page is delivered again for ever")
	}
	if row.quarantined != 1 {
		t.Fatalf("the checkpoint row records %d quarantined where one envelope of a page of %d was passed: a page-granular quarantine records %d here", row.quarantined, len(payloads), len(payloads))
	}
	if row.applied != int64(len(payloads)-1) {
		t.Fatalf("the checkpoint row records %d applied where %d of the page's %d envelopes reached the read model", row.applied, len(payloads)-1, len(payloads))
	}

	// The control: the same page under the default policy halts, so what passed
	// the envelope above is the policy the spec asked for and not this framework
	// skipping what it could not apply.
	t.Run("the control: the same page under Halt advances nothing and applies nothing", func(t *testing.T) {
		halting := newProjectionCase(t, 8, 0)
		into := halting.destination(t, "read_model")
		halting.writeEach(t, family, wire, payloads...)

		stopped := halting.run(t, inUnit(halting.spec(t, "orders", refusing(into)), halting.source, halting.source))
		cause := stopped.halted(t, "the projection under Halt stopped advancing on the corrupt payload")

		if !errors.Is(cause, event.ErrPayload) {
			t.Fatalf("the halted projection reports %v, which does not carry the handler's own refusal", cause)
		}
		if !strings.Contains(cause.Error(), family) {
			t.Fatalf("the halted projection reports %q and names not even the family an operator has to go and look at", cause)
		}
		if strings.Contains(cause.Error(), corrupt) {
			t.Fatalf("the halted projection reports %q and carries the payload it could not read, which every rendering rule in this framework exists to keep out", cause)
		}
		if got := into.count(t); got != 0 {
			t.Fatalf("the halted projection left %d rows where the unit carrying them rolled back with the advance", got)
		}
		if row, found := halting.row(t, "orders"); found {
			t.Fatalf("the halted projection left a checkpoint at advance %d over a page it never applied", row.advance)
		}
	})
}

// What the projection asked the checkpoint store, counted, with a barrier on the
// one Load that settles an unconfirmed save: the row this case is about must
// reach its final state before that Load reads it, and microseconds after a
// terminated backend is not a window a test can aim at.
type asked struct {
	event.Checkpoints
	loads   atomic.Int64
	saves   atomic.Int64
	entered chan struct{}
	release chan struct{}

	mutex     sync.Mutex
	presented event.Cursor
}

func (this *asked) Load(ctx context.Context, projection string) (event.Checkpoint, error) {
	if this.loads.Add(1) == 2 && this.release != nil {
		close(this.entered)
		<-this.release
	}
	return this.Checkpoints.Load(ctx, projection)
}

func (this *asked) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	this.saves.Add(1)
	this.mutex.Lock()
	this.presented = checkpoint.Cursor
	this.mutex.Unlock()
	return this.Checkpoints.Save(ctx, checkpoint)
}

// The cursor the last save presented beside its advance, which is what a row
// has to carry for the settlement to read it as this pass's own.
func (this *asked) cursor(t *testing.T) event.Cursor {
	t.Helper()
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.presented == "" {
		t.Fatal("no save ever presented a cursor, so the row this case commits could not be given the shape of one this pass wrote")
	}
	return this.presented
}

func (this *asked) settling(t *testing.T) {
	t.Helper()
	select {
	case <-this.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("no second load was ever issued, so the save this case interrupted was never settled and nothing below was measured")
	}
}

// The row the blocking session holds, given the shape of the save that was
// issued over it: the cursor that save presented and the progress it carried.
// The advance alone would not do — the settlement reads the cursor to tell this
// pass's own landed save from a second instance's row at the same advance, and a
// cursor no log ever minted is neither.
func landedSave(t *testing.T, held *sql.Tx, schema Schema, name string, cursor event.Cursor, highest int64, applied int64) {
	t.Helper()
	if _, err := held.ExecContext(context.WithoutCancel(t.Context()),
		"UPDATE "+quoteIdentifier(schema.Name)+"."+checkpointsTable+
			" SET cursor = $1, highest = $2, applied = $3 WHERE projection = $4",
		[]byte(cursor), highest, applied, name); err != nil {
		t.Fatalf("the row that stands for the save this case interrupted could not be given that save's own cursor: %v", err)
	}
}

// A row for this name, inserted and not committed, so a save at advance 1 blocks
// on the speculative insertion PostgreSQL makes for the unique index. It is the
// one way to hold a save open long enough to terminate the backend under it.
func blockingCheckpoint(t *testing.T, schema Schema, name string) *sql.Tx {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	tx, err := liveDB(t).BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("the session that holds the checkpoint row could not begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+quoteIdentifier(schema.Name)+"."+checkpointsTable+
		" (projection, cursor, advance, highest, applied, quarantined, updated_at) VALUES ($1, $2, 1, 0, 0, 0, now())",
		name, []byte("held-by-another-session")); err != nil {
		t.Fatalf("the row that blocks this case's save could not be inserted: %v", err)
	}
	return tx
}

// Terminates a backend once one of its statements is really blocked on a lock,
// so what this case interrupts is a statement in flight rather than one that had
// already answered. It reports through a channel rather than through t, because
// nothing but the test's own goroutine may fail a test.
func terminateWhenBlocked(t *testing.T, pid int64) <-chan error {
	t.Helper()
	db := liveDB(t)
	ctx := context.WithoutCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			var blocked bool
			if err := db.QueryRowContext(ctx,
				"SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1 AND state = 'active' AND wait_event_type = 'Lock')",
				pid).Scan(&blocked); err != nil {
				done <- err
				return
			}
			if blocked {
				_, err := db.ExecContext(ctx, "SELECT pg_terminate_backend($1)", pid)
				done <- err
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		done <- errors.New("no statement of that backend ever blocked on a lock, so nothing was interrupted")
	}()
	return done
}

func terminated(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the backend this case interrupts was not terminated, so nothing below was measured: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the watcher that terminates this case's backend never answered")
	}
}

// UC-104. A save whose backend was terminated under it is a write whose fate the
// answer does not carry, and the whole of what this framework does about it is
// ONE load: the row decides, and a second save could only guess — the guess
// being an ErrConflict a loop would then read as a second writer. Both branches
// are driven here against a real terminated backend, and the difference between
// them is the row and nothing else.
//
// The row that stands for the landed save carries the cursor that save
// presented, because that is what the settlement reads: the advance alone cannot
// say who wrote the row, the fence admits one writer at each advance, and a row
// at that advance carrying any other cursor is a second instance's and is taken
// as one. What no row can prove is the other direction — a second instance that
// read the same page presents the same cursor, and the framework does not try to
// tell that apart, because both resume at the same point.
func TestAnUnconfirmedSaveIsResolvedByOneLoadAndNeverBySaving(t *testing.T) {
	const (
		family = "eventpg.projection.unconfirmed"
		wire   = family + ".held"
	)
	payloads := []string{"u-1", "u-2"}

	for _, arm := range []struct {
		what    string
		commits bool
	}{
		{"a row at the advance this pass presented, carrying its cursor, is the save having landed and the loop carries on", true},
		{"no row at all is a save that did not land over a page already applied, and the loop halts", false},
	} {
		t.Run(arm.what, func(t *testing.T) {
			held := newProjectionCase(t, 8, 0)
			rows := held.destination(t, "read_model")
			held.writeEach(t, family, wire, payloads...)
			positions := storedPositions(t, held.schema)

			killable := checkpointPool(t, 1)
			recorder := &asked{
				Checkpoints: preparedCheckpoints(t, killable, held.schema, VerifySchema),
				entered:     make(chan struct{}),
				release:     make(chan struct{}),
			}
			blocker := blockingCheckpoint(t, held.schema, "orders")
			pid := backendPID(t, killable)

			spec := held.spec(t, "orders", rows.inserts(&deliveries{}, nil))
			spec.Checkpoints = recorder
			watcher := terminateWhenBlocked(t, pid)
			started := held.run(t, spec)

			terminated(t, watcher)
			recorder.settling(t)
			if arm.commits {
				landedSave(t, blocker, held.schema, "orders", recorder.cursor(t), positions[len(positions)-1], int64(len(payloads)))
				if err := blocker.Commit(); err != nil {
					t.Fatalf("the session holding the row could not commit it: %v", err)
				}
			} else if err := blocker.Rollback(); err != nil {
				t.Fatalf("the session holding the row could not roll it back: %v", err)
			}
			close(recorder.release)

			if arm.commits {
				started.following(t, "the projection carried on from the row the settlement read")
			} else {
				cause := started.halted(t, "the projection stopped over a save nothing accounted for")
				if !errors.Is(cause, event.ErrUncertain) {
					t.Fatalf("the halt reports %v and does not carry the uncertainty that caused it", cause)
				}
				if row, found := held.row(t, "orders"); found {
					t.Fatalf("a checkpoint row at advance %d exists where the save was never confirmed and the projection wrote nothing over it", row.advance)
				}
			}
			if got := recorder.saves.Load(); got != 1 {
				t.Fatalf("the projection issued %d saves where an unconfirmed one is settled by a load and never by a second save", got)
			}
			if got := recorder.loads.Load(); got != 2 {
				t.Fatalf("the projection issued %d loads where a resume and one bounded resolution are two", got)
			}
			got := rows.tally(t)
			for _, payload := range payloads {
				if got[payload] != 1 {
					t.Fatalf("the read model holds %v where the page was applied once and never re-applied on the assumption that the save failed", got)
				}
			}
		})
	}

	// The control, and it is what tells the two windows apart by evidence rather
	// than by hope: the same terminated backend inside a bound transaction is a
	// statement that left a transaction PostgreSQL will now refuse to commit, so
	// the store answers NotWritten, the pass retries, and no settlement runs at
	// all.
	t.Run("the control: the same failure inside a bound transaction is NotWritten and the pass retries", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		rows := held.destination(t, "read_model")
		held.writeEach(t, family, wire, payloads...)

		recorder := &asked{Checkpoints: held.checkpoints(t)}
		blocker := blockingCheckpoint(t, held.schema, "orders")

		pids := make(chan int64, 16)
		spec := inUnit(held.spec(t, "orders", rows.inserts(&deliveries{}, nil)), held.source, held.source)
		spec.Checkpoints = recorder
		spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
			return crud.InNewTx(ctx, held.source, func(inner context.Context) error {
				if tx, bound := crudsql.TransactionFor(inner, held.source); bound {
					var pid int64
					if err := tx.QueryRowContext(inner, "SELECT pg_backend_pid()").Scan(&pid); err == nil {
						select {
						case pids <- pid:
						default:
						}
					}
				}
				return work(inner)
			})
		}
		started := held.run(t, spec)

		var pid int64
		select {
		case pid = <-pids:
		case <-time.After(20 * time.Second):
			t.Fatal("no unit ever bound a transaction, so there was no backend to interrupt")
		}
		terminated(t, terminateWhenBlocked(t, pid))
		if err := blocker.Rollback(); err != nil {
			t.Fatalf("the session holding the row could not roll it back: %v", err)
		}
		started.following(t, "the projection retried the pass its transaction lost and drained the log")

		if got := recorder.loads.Load(); got != 1 {
			t.Fatalf("the projection issued %d loads where a failure the store called NotWritten is retried rather than settled", got)
		}
		if got := recorder.saves.Load(); got < 2 {
			t.Fatalf("the projection issued %d saves where the first one's transaction was killed and the pass presented the same advance again", got)
		}
		for _, payload := range payloads {
			if got := rows.tally(t)[payload]; got != 1 {
				t.Fatalf("%q is in %d rows where the killed unit took its own writes back with it and the retry applied the page once", payload, got)
			}
		}
		if row, found := held.row(t, "orders"); !found || row.advance != 1 {
			t.Fatalf("the checkpoint row is %+v (found %v) where the retry's own save is the first one to land", row, found)
		}
	})
}

var errUnitLostOnPurpose = errors.New("the unit of work carrying this page was rolled back by this case")

// Reads once from the origin and never again, which is what an instance that
// delivers one page and then goes away does to the log: its second read waits
// out its own context rather than moving its cursor.
type firstPageOnly struct{ event.Log }

func (this *firstPageOnly) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	if after != "" {
		<-ctx.Done()
		return nil, "", ctx.Err()
	}
	return this.Log.ReadAll(ctx, after)
}

// UC-104's other half, and the interleaving the two-instance case above cannot
// reach: the winner's page is SHORTER than the loser's, and the winner then goes
// away. [[D-133]] says a rolling deploy runs two instances of one name on
// purpose, so the two differ in the way two deployments differ — the old replica
// reads one event a page and the new one takes the whole log.
//
// The advance a settling load finds cannot say who wrote the row. The fence
// admits one writer at each advance, and the moment this pass's unit rolls back
// and releases the row a second instance reaches that same advance with a cursor
// of its own. Resuming from this pass's own instead leaves every position
// between the two cursors applied by nobody, and the next save puts the
// checkpoint over them for good — no error on any path, Quarantined zero, and
// Progress.Highest reading as a completeness watermark it is not.
//
// The order is driven and not hoped for: the new instance claims advance 1 and
// its unit takes the claim back; the old instance then takes that advance with
// its own cursor and stops; and the new instance's settling load is held at a
// gate until the row it must read is committed.
func TestASettlementTakesTheRowsCursorWhenAShorterWinnerLeftIt(t *testing.T) {
	const (
		family = "eventpg.projection.settlement"
		wire   = family + ".held"
	)
	payloads := []string{"s-1", "s-2", "s-3"}

	newer := newProjectionCase(t, 12, 0)
	rows := newer.destination(t, "read_model")
	newer.writeEach(t, family, wire, payloads...)
	older := newer.restarted(t, 1)

	settling := &asked{
		Checkpoints: newer.checkpoints(t),
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	claimed := &deliveries{}
	var lost atomic.Bool
	fresh := newer.spec(t, "orders", rows.inserts(claimed, nil))
	fresh.Checkpoints = settling
	fresh.Advance = projection.InUnit
	fresh.Destination = newer.source
	fresh.Unit = func(ctx context.Context, work func(context.Context) error) error {
		return crud.InNewTx(ctx, newer.source, func(inner context.Context) error {
			if err := work(inner); err != nil {
				return err
			}
			if lost.CompareAndSwap(false, true) {
				return errUnitLostOnPurpose
			}
			return nil
		})
	}
	replacement := newer.run(t, fresh)
	settling.settling(t)

	stalled := older.spec(t, "orders", rows.through(older).inserts(&deliveries{}, nil))
	stalled.Log = &firstPageOnly{Log: older.log}
	replaced := older.run(t, inUnit(stalled, older.source, older.source))

	waitFor(t, "the old replica took the advance the new one had presented", func() bool {
		row, found := newer.row(t, "orders")
		return found && row.advance == 1
	})
	winner, _ := newer.row(t, "orders")
	replaced.cancel()
	replaced.stopped(t)

	if pages := claimed.pages(); len(pages) != 1 || len(pages[0].Envelopes) != len(payloads) {
		t.Fatalf("the new replica delivered %d pages where this case needs it to have claimed the whole log of %d in one", len(pages), len(payloads))
	}
	if event.Cursor(winner.cursor) == settling.cursor(t) {
		t.Fatal("the old replica wrote the very cursor the new one presented, so the two read the same page and this case measures nothing")
	}
	if got := settling.saves.Load(); got != 1 {
		t.Fatalf("the new replica issued %d saves before its settlement, where the whole case is what ONE unconfirmed save settles to", got)
	}
	close(settling.release)
	replacement.following(t, "the new replica settled and drained what was left")

	newer.writeEach(t, family, wire, "s-4")
	positions := storedPositions(t, newer.schema)
	waitFor(t, "the checkpoint reached the position appended after the settlement", func() bool {
		row, found := newer.row(t, "orders")
		return found && row.highest == positions[len(positions)-1]
	})

	row, _ := newer.row(t, "orders")
	tally := rows.tally(t)
	for _, payload := range append(slices.Clone(payloads), "s-4") {
		if tally[payload] != 1 {
			t.Fatalf("the read model holds %v while the checkpoint stands at advance %d, highest %d and quarantined %d: %q is behind the cursor and is never delivered again",
				tally, row.advance, row.highest, row.quarantined, payload)
		}
	}
	if state := replacement.held.State(); state.Phase == projection.PhaseHalted {
		t.Fatalf("the new replica halted with %v, and a second live writer at one name is not a reason to stop applying", state.Err)
	}

	// The control, and it is what makes the assertion above mean anything: the
	// same wiring with nothing to contend against applies each event once, so a
	// case that passed because the read model is written twice, or because
	// neither instance ever applied anything, fails here.
	t.Run("the control: one instance alone over the same log applies every event once", func(t *testing.T) {
		alone := newProjectionCase(t, 8, 0)
		into := alone.destination(t, "read_model")
		alone.writeEach(t, family, wire, payloads...)
		alone.run(t, inUnit(alone.spec(t, "orders", into.inserts(&deliveries{}, nil)), alone.source, alone.source)).
			following(t, "one instance alone drained the log")
		if got := into.tally(t); len(got) != len(payloads) {
			t.Fatalf("one instance alone left %v over a log of %d", got, len(payloads))
		}
		for _, payload := range payloads {
			if got := into.tally(t)[payload]; got != 1 {
				t.Fatalf("one instance alone left %q in %d rows", payload, got)
			}
		}
	})
}

// A second live database on the same server: its own pool, its own crud source,
// its own transaction manager. The checkpoint table stays in the first one, so a
// unit opened there cannot cover a write made here — which is the whole of what
// UC-128 is about, and what no check inside this framework can see.
func secondDatabase(t *testing.T) (*sql.DB, crud.Source) {
	t.Helper()
	name := scratchName()
	dsn, err := url.Parse(os.Getenv(testDSN))
	if err != nil || dsn.Scheme == "" {
		t.Fatalf("%s does not name a database this case can build a second one beside: %v", testDSN, err)
	}
	ctx := context.WithoutCancel(t.Context())
	if _, err := liveDB(t).ExecContext(ctx, "CREATE DATABASE "+quoteIdentifier(name)); err != nil {
		t.Fatalf("the second database this case writes its read model into could not be created: %v", err)
	}
	t.Cleanup(func() {
		if _, err := liveDB(t).ExecContext(context.WithoutCancel(t.Context()), "DROP DATABASE IF EXISTS "+quoteIdentifier(name)+" WITH (FORCE)"); err != nil {
			t.Errorf("the second database %q could not be dropped, so a later run inherits it: %v", name, err)
		}
	})
	dsn.Path = "/" + name
	pool, err := sql.Open("pgx", dsn.String())
	if err != nil {
		t.Fatalf("a pool onto the second database could not be opened: %v", err)
	}
	pool.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = pool.Close() })
	return pool, crudsql.Postgres(pool)
}

// A read model in a database this framework's checkpoint store knows nothing
// about, reached through its own pool.
func (this *projectionCase) elsewhere(t *testing.T, table string) *destination {
	t.Helper()
	pool, source := secondDatabase(t)
	held := &destination{table: quoteIdentifier(table), pool: pool, source: source, elsewhere: true}
	held.create(t)
	return held
}

// UC-128, and the case asserts all three wirings because the point is that they
// are told apart. Two of them are the same crash at the same point with a
// different Destination, and what separates them is a precondition rather than a
// mode: InUnit promises the handler's writes and the advance commit together
// only as far as the handler writes through the context the unit gave it, and
// Unchecked is how a composition says out loud that nothing can check whether it
// did.
func TestThreeWiringsToASecondDatabaseAreToldApart(t *testing.T) {
	const (
		family = "eventpg.projection.elsewhere"
		wire   = family + ".held"
	)
	payloads := []string{"e-1", "e-2", "e-3"}

	t.Run("AfterApply to a second database works, and its window is the one UC-106 names", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		rows := held.elsewhere(t, "read_model")
		held.writeEach(t, family, wire, payloads...)

		kill := newKillPoint(t)
		killed := held.runOn(t, kill.ctx, kill.cancel, held.spec(t, "orders", rows.inserts(&deliveries{}, kill.fire)))
		if err := killed.stopped(t); !errors.Is(err, context.Canceled) {
			t.Fatalf("the killed projection's Run answered %v", err)
		}
		if got := rows.rows(t); !slices.Equal(got, payloads) {
			t.Fatalf("the second database holds %v where the handler committed the whole page into it before the kill", got)
		}
		if row, found := held.row(t, "orders"); found {
			t.Fatalf("the checkpoint moved to advance %d over rows in a database no unit of this one covered", row.advance)
		}
	})

	t.Run("InUnit naming the second source is refused on the first pass, before the handler runs", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		rows := held.elsewhere(t, "read_model")
		held.writeEach(t, family, wire, payloads...)

		seen := &deliveries{}
		stopped := held.run(t, inUnit(held.spec(t, "orders", rows.inserts(seen, nil)), held.source, rows.source))
		cause := stopped.halted(t, "the projection refused a unit that bound nothing for its Destination")

		if !errors.Is(cause, projection.ErrSpec) {
			t.Fatalf("the projection halted with %v where a Destination the unit bound no executor for is a wiring refusal", cause)
		}
		if got := len(seen.pages()); got != 0 {
			t.Fatalf("the handler was called %d times where the refusal is made inside the unit and before it runs", got)
		}
		if got := rows.count(t); got != 0 {
			t.Fatalf("the second database holds %d rows where the handler never ran", got)
		}
		if row, found := held.row(t, "orders"); found {
			t.Fatalf("the refused projection left a checkpoint at advance %d", row.advance)
		}
	})

	t.Run("InUnit with Unchecked is accepted and leaves the second database's rows where the advance rolled back", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		rows := held.elsewhere(t, "read_model")
		held.writeEach(t, family, wire, payloads...)

		kill := newKillPoint(t)
		spec := inUnit(held.spec(t, "orders", rows.inserts(&deliveries{}, kill.fire)), held.source, projection.Unchecked)
		killed := held.runOn(t, kill.ctx, kill.cancel, spec)
		if err := killed.stopped(t); !errors.Is(err, context.Canceled) {
			t.Fatalf("the killed projection's Run answered %v", err)
		}
		if got := rows.rows(t); !slices.Equal(got, payloads) {
			t.Fatalf("the second database holds %v where a write this framework never saw survives the rollback of the advance", got)
		}
		if row, found := held.row(t, "orders"); found {
			t.Fatalf("the checkpoint moved to advance %d where the unit carrying it rolled back", row.advance)
		}
	})

	// The control, and it is the other half of the measurement: the same handler,
	// the same unit and the same kill against a read model in the checkpoint
	// store's own database, with Destination naming it. One wiring, two outcomes,
	// and what differs is the precondition rather than the mode.
	t.Run("the control: the same crash against a read model the unit does cover leaves no rows at all", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		rows := held.destination(t, "read_model")
		held.writeEach(t, family, wire, payloads...)

		kill := newKillPoint(t)
		spec := inUnit(held.spec(t, "orders", rows.inserts(&deliveries{}, kill.fire)), held.source, held.source)
		killed := held.runOn(t, kill.ctx, kill.cancel, spec)
		if err := killed.stopped(t); !errors.Is(err, context.Canceled) {
			t.Fatalf("the killed projection's Run answered %v", err)
		}
		if !kill.struck() {
			t.Fatal("no handler ever ran, so the crash the two arms above are compared against never happened")
		}
		if got := rows.count(t); got != 0 {
			t.Fatalf("the read model holds %d rows where the unit that carried them rolled back with the advance", got)
		}
		if row, found := held.row(t, "orders"); found {
			t.Fatalf("the checkpoint moved to advance %d where the unit carrying it rolled back", row.advance)
		}
	})
}
