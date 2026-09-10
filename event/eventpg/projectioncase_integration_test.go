//go:build integration

package eventpg

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
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

// One scratch schema of a case's own — the log, the checkpoint table and
// whatever read models it names — behind one pool. Nothing here asserts
// anything: every claim below is read back out of the database rather than off a
// projection's own account of itself, which is the whole difference between this
// section and the ones that need no database.
type projectionCase struct {
	schema Schema
	pool   *sql.DB
	source crud.Source
	store  *Store
	log    event.Log
}

func newProjectionCase(t *testing.T, connections, maxRead int) *projectionCase {
	t.Helper()
	schema := Schema{Name: scratchName()}
	pool := checkpointPool(t, connections)
	t.Cleanup(func() { dropSchema(t, schema.Name) })
	source := crudsql.Postgres(pool)
	store, err := New(Spec{DB: pool, Source: source, Schema: schema, SchemaManagement: ManageSchema, MaxRead: maxRead})
	if err != nil {
		t.Fatalf("a store over %+v was refused: %v", schema, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Prepare(context.WithoutCancel(t.Context())); err != nil {
		t.Fatalf("a store over %q did not prepare it: %v", schema.Name, err)
	}
	return &projectionCase{schema: schema, pool: pool, source: source, store: store, log: event.ReadOnly(store)}
}

// A read model this case asserts against: the table, the pool it lives behind
// and the crud source naming that pool. A handler writes through the transaction
// the pass bound for that source when there is one and on the pool when there is
// not, which is the whole of the difference between the two advance modes.
type destination struct {
	table     string
	pool      *sql.DB
	source    crud.Source
	elsewhere bool
}

func (this *projectionCase) destination(t *testing.T, table string) *destination {
	t.Helper()
	held := &destination{
		table:  quoteIdentifier(this.schema.Name) + "." + quoteIdentifier(table),
		pool:   this.pool,
		source: this.source,
	}
	held.create(t)
	return held
}

func (this *destination) create(t *testing.T) {
	t.Helper()
	if _, err := this.pool.ExecContext(context.WithoutCancel(t.Context()),
		"CREATE TABLE "+this.table+" (id bigserial PRIMARY KEY, payload text NOT NULL)"); err != nil {
		t.Fatalf("the read model %s this case asserts against could not be created: %v", this.table, err)
	}
}

func (this *destination) drop(t *testing.T) {
	t.Helper()
	if _, err := this.pool.ExecContext(context.WithoutCancel(t.Context()), "DROP TABLE "+this.table); err != nil {
		t.Fatalf("the read model %s could not be dropped: %v", this.table, err)
	}
}

// In the order the rows were written, which is the order the handler applied
// them: a bigserial the handler never names cannot be reordered by anything this
// framework does. Cross-checked against psql, so what a case asserts about a
// read model is not this process's own account of a query it also issued.
func (this *destination) rows(t *testing.T) []string {
	t.Helper()
	held := this.read(t)
	this.agreesWithPsql(t, held)
	return held
}

func (this *destination) tally(t *testing.T) map[string]int {
	t.Helper()
	held := map[string]int{}
	for _, payload := range this.rows(t) {
		held[payload]++
	}
	return held
}

// The same read with no cross-check, because a case that polls this until it
// changes would otherwise start a psql every five milliseconds.
func (this *destination) count(t *testing.T) int { return len(this.read(t)) }

func (this *destination) read(t *testing.T) []string {
	t.Helper()
	rows, err := this.pool.QueryContext(context.WithoutCancel(t.Context()), "SELECT payload FROM "+this.table+" ORDER BY id")
	if err != nil {
		t.Fatalf("reading %s answered %v", this.table, err)
	}
	defer func() { _ = rows.Close() }()
	var held []string
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("a row of %s could not be read: %v", this.table, err)
		}
		held = append(held, payload)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading %s answered %v", this.table, err)
	}
	return held
}

const testPSQL = "FROSTGROVE_EVENTPG_TEST_PSQL"

// The same rows as psql prints them. The command is the operator's own — the
// section's checkpoint sets it to `docker exec -i vv-postgres-1 psql -U vv -d vv`
// — and when it is set and cannot answer, that is a failure rather than a
// measurement quietly not taken. Unset, the pool's answer stands alone: the DSN
// is what names this database and a psql binary is not always beside it. A read
// model in a second database is never asked, because psql reaches the one the
// DSN names.
func (this *destination) agreesWithPsql(t *testing.T, held []string) {
	t.Helper()
	if this.elsewhere {
		return
	}
	printed, asked := psqlAnswers(t, "SELECT payload FROM "+this.table+" ORDER BY id")
	if !asked {
		return
	}
	if !slices.Equal(printed, held) {
		t.Fatalf("psql prints %v for %s where this process read %v out of the same table, so one of the two is not reading the database", printed, this.table, held)
	}
}

func psqlAnswers(t *testing.T, query string) ([]string, bool) {
	t.Helper()
	command := strings.Fields(os.Getenv(testPSQL))
	if len(command) == 0 {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
	defer cancel()
	printed, err := exec.CommandContext(ctx, command[0], append(append([]string{}, command[1:]...), "-tAX", "-c", query)...).Output()
	if err != nil {
		t.Fatalf("%s names %q and it could not answer %.60q, so the cross-check this case asks for was never made: %v", testPSQL, os.Getenv(testPSQL), query, err)
	}
	held := strings.Split(strings.TrimSpace(string(printed)), "\n")
	if len(held) == 1 && held[0] == "" {
		return nil, true
	}
	return held, true
}

// The checkpoint row as the database holds it, read on a connection no
// projection is using and through no store's own Load: a case that asserts what
// a projection recorded must not be asking the code that recorded it.
func (this *projectionCase) row(t *testing.T, name string) (checkpointRow, bool) {
	t.Helper()
	return maybeStoredCheckpoint(t, this.schema, name)
}

func (this *projectionCase) write(t *testing.T, stream event.Stream, wire string, payloads ...string) {
	t.Helper()
	this.writeAt(t, stream, wire, 1, payloads...)
}

func (this *projectionCase) writeAt(t *testing.T, stream event.Stream, wire string, revision int, payloads ...string) {
	t.Helper()
	at, _ := streamVersion(t, this.schema, stream)
	records := make([]event.Record, 0, len(payloads))
	for _, payload := range payloads {
		records = append(records, event.Record{Type: wire, Revision: revision, Payload: []byte(payload)})
	}
	request := event.AppendRequest{Stream: stream, Expected: event.Version(at), Records: records}
	if err := this.store.Append(context.WithoutCancel(t.Context()), request); err != nil {
		t.Fatalf("appending %v to %v answered %v", payloads, stream, err)
	}
}

// One event a stream, which is what a log of a live application looks like from
// a projection's side: every payload is its own aggregate's first fact.
func (this *projectionCase) writeEach(t *testing.T, family, wire string, payloads ...string) {
	t.Helper()
	for _, payload := range payloads {
		this.write(t, aStream(family, family+"/"+payload), wire, payload)
	}
}

// A second value over the same backing, which is what a second process holds.
// Never the value another projection is running through: a resume that shared
// one would be reading a tracker's own memory rather than the row.
func (this *projectionCase) checkpoints(t *testing.T) *Checkpoints {
	t.Helper()
	return preparedCheckpoints(t, this.pool, this.schema, VerifySchema)
}

// The second process's whole wiring over one schema: its own pool, its own
// Store, and — through spec — its own Checkpoints and its own Projection.
// Nothing of the first wiring's is shared, so what carries a walk across the
// process boundary is the cursor in the row and never something a value
// remembered. It takes its own MaxRead, because two live instances of one name
// are two deployments and a page size is one of the things that differs between
// them.
func (this *projectionCase) restarted(t *testing.T, maxRead int) *projectionCase {
	t.Helper()
	pool := checkpointPool(t, 8)
	source := crudsql.Postgres(pool)
	store, err := New(Spec{DB: pool, Source: source, Schema: this.schema, SchemaManagement: VerifySchema, MaxRead: maxRead})
	if err != nil {
		t.Fatalf("the second wiring's store over %+v was refused: %v", this.schema, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Prepare(context.WithoutCancel(t.Context())); err != nil {
		t.Fatalf("the second wiring's store over %q did not verify it: %v", this.schema.Name, err)
	}
	return &projectionCase{schema: this.schema, pool: pool, source: source, store: store, log: event.ReadOnly(store)}
}

// The same rows reached through another wiring's pool and source, which is what
// a second process holds of one read model.
func (this *destination) through(other *projectionCase) *destination {
	return &destination{table: this.table, pool: other.pool, source: other.source, elsewhere: this.elsewhere}
}

func (this *projectionCase) spec(t *testing.T, name string, handler projection.Handler) projection.Spec {
	t.Helper()
	return projection.Spec{
		Name:        name,
		Log:         this.log,
		Checkpoints: this.checkpoints(t),
		Handler:     handler,
		Idle:        20 * time.Millisecond,
		Backoff:     projection.Backoff{First: 5 * time.Millisecond, Max: 50 * time.Millisecond},
		Ticks:       runtime.SystemTicks,
	}
}

// The advance inside the caller's own unit, spelled the way the module page
// spells it: crud.InNewTx over the source the checkpoint store lives in, and a
// Destination the pass resolves inside that unit before the handler runs.
func inUnit(spec projection.Spec, source crud.Source, destination any) projection.Spec {
	spec.Advance = projection.InUnit
	spec.Destination = destination
	spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
		return crud.InNewTx(ctx, source, work)
	}
	return spec
}

// Writes one row per envelope through whatever the pass bound for this
// destination's own source — the unit's transaction under InUnit, the pool under
// AfterApply — so what a rollback takes back with it is decided by the wiring
// and never by this handler.
func (this *destination) inserts(seen *deliveries, after func(ctx context.Context)) projection.Handler {
	return projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
		for _, envelope := range batch.Envelopes {
			if _, err := this.on(ctx).ExecContext(ctx, "INSERT INTO "+this.table+" (payload) VALUES ($1)", string(envelope.Payload)); err != nil {
				return err
			}
		}
		seen.record(batch)
		if after != nil {
			after(ctx)
		}
		return nil
	})
}

func (this *destination) on(ctx context.Context) executor {
	if held, found := crud.ExecutorFor(ctx, this.source); found {
		if tx, taken := crudsql.Transaction(held); taken {
			return tx
		}
	}
	return this.pool
}

// What reached a handler, in the order it reached one. A case reads the pages
// back rather than counting them, because the identity on each envelope is what
// a redelivery is told apart by.
type deliveries struct {
	mutex   sync.Mutex
	batches []projection.Batch
}

func (this *deliveries) record(batch projection.Batch) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.batches = append(this.batches, batch)
}

func (this *deliveries) pages() []projection.Batch {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return slices.Clone(this.batches)
}

// A projection on a goroutine of its own, stopped with the case. Nothing here
// waits for it: what a case is about it waits for itself, out of the database.
type running struct {
	held     *projection.Projection
	cancel   context.CancelFunc
	returned chan error

	waiting sync.Once
	ended   bool
	err     error
}

func (this *projectionCase) run(t *testing.T, spec projection.Spec) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	return this.runOn(t, ctx, cancel, spec)
}

func (this *projectionCase) runOn(t *testing.T, ctx context.Context, cancel context.CancelFunc, spec projection.Spec) *running {
	t.Helper()
	held, err := projection.New(spec)
	if err != nil {
		t.Fatalf("a well-formed projection was refused: %v", err)
	}
	started := &running{held: held, cancel: cancel, returned: make(chan error, 1)}
	go func() { started.returned <- held.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		started.stopped(t)
	})
	return started
}

func (this *running) stopped(t *testing.T) error {
	t.Helper()
	this.waiting.Do(func() {
		select {
		case this.err = <-this.returned:
			this.ended = true
		case <-time.After(30 * time.Second):
		}
	})
	if !this.ended {
		t.Fatal("Run did not return after its context was done, and a loop that outlives its own context holds the process open")
	}
	return this.err
}

// The orderly half of a shutdown, and the order is the supervisor's: the drain
// is acknowledged between passes, and only then is the context cancelled.
func (this *running) stop(t *testing.T) {
	t.Helper()
	if err := this.held.Drain(context.WithoutCancel(t.Context())); err != nil {
		t.Fatalf("draining the projection answered %v", err)
	}
	this.cancel()
	this.stopped(t)
}

func (this *running) following(t *testing.T, what string) {
	t.Helper()
	waitFor(t, what, func() bool {
		if state := this.held.State(); state.Phase == projection.PhaseHalted {
			t.Fatalf("the projection halted with %v where this case expects it to follow", state.Err)
		}
		return this.held.State().Phase == projection.PhaseFollowing
	})
}

// The whole log read over a queue that is holding something, which is what
// following looks like once a sequence is parked: the projection has not caught
// up, and saying it had would be the lie the phase exists to prevent.
func (this *running) degraded(t *testing.T, what string) {
	t.Helper()
	waitFor(t, what, func() bool {
		if state := this.held.State(); state.Phase == projection.PhaseHalted {
			t.Fatalf("the projection halted with %v where this case expects it to be degraded", state.Err)
		}
		return this.held.State().Phase == projection.PhaseDegraded
	})
}

func (this *running) halted(t *testing.T, what string) error {
	t.Helper()
	waitFor(t, what, func() bool { return this.held.State().Phase == projection.PhaseHalted })
	state := this.held.State()
	if state.Err == nil {
		t.Fatal("the projection reports it halted and carries no reason, so an operator has nothing to read")
	}
	return state.Err
}

// The process dying between the handler's write and the checkpoint, spelled as
// the one thing a test can do that a crash does: the pass's own context is
// cancelled from inside the handler, after its rows are written and before any
// advance can be confirmed. Every door of both stores answers a cancelled
// context by refusing before it issues anything, so nothing this framework would
// have done next reaches the database — which is what a killed process leaves
// behind.
type killPoint struct {
	ctx    context.Context
	cancel context.CancelFunc
	fired  atomic.Bool
}

func newKillPoint(t *testing.T) *killPoint {
	t.Helper()
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	t.Cleanup(cancel)
	return &killPoint{ctx: ctx, cancel: cancel}
}

func (this *killPoint) fire(context.Context) {
	if this.fired.CompareAndSwap(false, true) {
		this.cancel()
	}
}

func (this *killPoint) struck() bool { return this.fired.Load() }
