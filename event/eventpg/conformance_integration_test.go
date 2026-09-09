//go:build integration

package eventpg

import (
	"context"
	"crypto/rand"
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
	"github.com/frostgrove/vv/event/eventtest"
)

func TestTheStoreSatisfiesTheContract(t *testing.T) {
	eventtest.Run(t, conformance(t, Schema{}, Spec{}))
}

// The same store publishing numbers a deployment is free to choose. MaxKey is a
// CHECK operand and a fingerprint input, so a store at forty cannot verify a
// schema deployed at five hundred and twelve — which is why every store value
// this factory builds migrates a schema of its own and the narrow run needs
// nothing of its own at all.
func TestTheStoreSatisfiesTheContractAtNarrowerLimits(t *testing.T) {
	eventtest.Run(t, conformance(t, Schema{MaxKey: 40}, Spec{StreamPage: 4, MaxBatch: 2, MaxRead: 3}))
}

// Every store value gets its own pool, its own freshly migrated schema and its
// own crud.Source, so two values New built are two data sources and a
// transaction bound for one is not found for the other — which is what the
// chained pair of the transactions section asks for. Sibling is the opposite: a
// second value over the first one's pool and schema, which is the equal backing
// durability and shared backing are read through.
func conformance(t *testing.T, schema Schema, spec Spec) eventtest.Factory {
	return downgraded(t, eventtest.Factory{
		New: func(t *testing.T) event.Store {
			return conformanceStore(t, schema, spec, scratchName())
		},
		Begin: func(t *testing.T, ctx context.Context, s event.Store) (context.Context, eventtest.Tx) {
			return beginOn(t, ctx, supplied(t, s))
		},
		Sibling: func(t *testing.T, s event.Store) event.Store {
			held := supplied(t, s)
			return failable(t, storeOver(t, held.pool, held.schema, held.spec, VerifySchema), held.pool, held.schema, held.spec)
		},
		Fail: func(t *testing.T, s event.Store, outcome event.Outcome) bool {
			return supplied(t, s).arm(t, outcome)
		},
		Unparsable: func(*testing.T, event.Store) event.Cursor {
			return "neither this store's format nor anybody's"
		},
		// The operations are a network away and every store value this factory
		// builds migrates a schema before it serves anything.
		Window: 30 * time.Second,
	})
}

func conformanceStore(t *testing.T, schema Schema, spec Spec, name string) *failing {
	t.Helper()
	schema.Name = name
	pool := conformancePool(t, "pgx")
	t.Cleanup(func() { auditThenDrop(t, schema.Name) })
	store := storeOver(t, pool, schema, spec, ManageSchema)
	return failable(t, store, pool, schema, spec)
}

func storeOver(t *testing.T, pool *sql.DB, schema Schema, spec Spec, management SchemaManagement) *Store {
	t.Helper()
	spec.DB, spec.Source = pool, crudsql.Postgres(pool)
	spec.Schema, spec.SchemaManagement = schema, management
	store, err := New(spec)
	if err != nil {
		t.Fatalf("a store over %+v was refused: %v", schema, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Prepare(context.WithoutCancel(t.Context())); err != nil {
		t.Fatalf("a store over the scratch schema %q did not prepare it: %v", schema.Name, err)
	}
	return store
}

// Six, because three transactions and a read on the pool are live at once in the
// crossed case of the transactions section, and a pool that cannot serve the
// fourth checkout blocks until the section window expires. One idle, because a
// conformance run holds twenty pools open at the end of the store failure
// section and this cluster admits a hundred connections in all.
func conformancePool(t *testing.T, driver string) *sql.DB {
	t.Helper()
	pool, err := sql.Open(driver, os.Getenv(testDSN))
	if err != nil {
		t.Fatalf("a pool of this store value's own could not be opened: %v", err)
	}
	pool.SetMaxOpenConns(6)
	pool.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

func beginOn(t *testing.T, ctx context.Context, store *failing) (context.Context, eventtest.Tx) {
	t.Helper()
	beginner, found := crud.BeginnerOf(crudsql.Postgres(store.db))
	if !found {
		t.Fatal("the store's own data source cannot begin a transaction, so no section below could bind one")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning a transaction on the store's own data source answered %v", err)
	}
	return crud.BindExecutor(ctx, store.source, tx), tx
}

var scratchNames struct {
	prefix sync.Once
	name   string
	next   atomic.Int64
}

// A name per store value, unique to this process: the mutation harness runs
// several test binaries against one cluster and two of them sharing a schema
// would read each other's rows as their own.
func scratchName() string {
	scratchNames.prefix.Do(func() {
		held := make([]byte, 4)
		if _, err := rand.Read(held); err != nil {
			panic("eventpg_test: this process cannot name its own scratch schemas: " + err.Error())
		}
		scratchNames.name = "eventpg_c" + hex.EncodeToString(held)
	})
	return scratchNames.name + "_" + strconv.FormatInt(scratchNames.next.Add(1), 10)
}

func supplied(t *testing.T, s event.Store) *failing {
	t.Helper()
	store, is := s.(*failing)
	if !is {
		t.Fatalf("the suite handed back a store this factory did not build: %T", s)
	}
	return store
}

var errInjected = errors.New("eventpg_test: this decorator classified nothing and forwarded nothing")

// The one thing a store value cannot be asked for through the contract: a
// failure on purpose. Nothing here fabricates an event.Failure, because the
// section that reads one is the only place this store's own classification of a
// real driver error is under test. Six of the seven outcomes are produced by a
// real *eventpg.Store in a state that really produces them — closed, never
// prepared, over a pool that was closed under it, at a version the stream has
// left, over another schema's log, and over a driver that writes the statement
// and then loses the answer. The seventh is a decorator that classifies nothing,
// which is what an unclassified failure is.
type failing struct {
	*Store

	pool   *sql.DB
	schema Schema
	spec   Spec
	held   event.Store

	mutex sync.Mutex
	armed event.Store
	built map[event.Outcome]event.Store
}

func failable(t *testing.T, store *Store, pool *sql.DB, schema Schema, spec Spec) *failing {
	t.Helper()
	return &failing{Store: store, pool: pool, schema: schema, spec: spec, held: mutated(t, store)}
}

func (this *failing) fires() event.Store {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held := this.armed
	this.armed = nil
	return held
}

func (this *failing) Append(ctx context.Context, request event.AppendRequest) error {
	if held := this.fires(); held != nil {
		return held.Append(ctx, request)
	}
	return this.held.Append(ctx, request)
}

func (this *failing) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	if held := this.fires(); held != nil {
		return held.ReadStream(ctx, stream, after)
	}
	return this.held.ReadStream(ctx, stream, after)
}

func (this *failing) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	if held := this.fires(); held != nil {
		return held.ReadAll(ctx, after)
	}
	return this.held.ReadAll(ctx, after)
}

func (this *failing) arm(t *testing.T, outcome event.Outcome) bool {
	t.Helper()
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.built == nil {
		this.built = map[event.Outcome]event.Store{}
	}
	held, made := this.built[outcome]
	if !made {
		held = this.wayTo(t, outcome)
		this.built[outcome] = held
	}
	if held == nil {
		return false
	}
	if outcome == event.Unconfirmed {
		arm(t, 1, true, errors.New("eventpg_test: the connection carried the statement and answered nothing this store can read"))
	}
	this.armed = held
	return true
}

// Each of these is a store value or a decorator that a real failure travels out
// of, and each is built once per store value because the section asks one store
// for one outcome at a time.
func (this *failing) wayTo(t *testing.T, outcome event.Outcome) event.Store {
	t.Helper()
	switch outcome {
	case event.Closed:
		beside := storeOver(t, this.pool, this.schema, this.spec, VerifySchema)
		if err := beside.Close(); err != nil {
			t.Fatalf("closing a second store value over one schema answered %v", err)
		}
		return beside
	case event.Refused:
		spec := this.spec
		spec.DB, spec.Source = this.pool, crudsql.Postgres(this.pool)
		spec.Schema, spec.SchemaManagement = this.schema, VerifySchema
		beside, err := New(spec)
		if err != nil {
			t.Fatalf("a store this case never prepares was refused at construction: %v", err)
		}
		return beside
	case event.NotWritten:
		return this.overAClosedPool(t)
	case event.Conflict:
		return conflicting{this.Store}
	case event.BadCursor:
		return badCursor{Store: this.Store, foreign: cursorOfAnotherSchema(t, this.schema, this.spec)}
	case event.Unconfirmed:
		return storeOver(t, conformancePool(t, countingDriverName), this.schema, this.spec, VerifySchema)
	case event.Unclassified:
		return unclassified{this.Store}
	}
	return nil
}

// Prepared and then broken, in that order: the readiness gate is two steps
// before the checkout, so a store over a DSN that resolves nowhere would answer
// Refused rather than reach the classifier at all. With the pool closed under a
// prepared store the checkout fails, nothing is issued, and the rule reads that
// as proof the write did not land.
func (this *failing) overAClosedPool(t *testing.T) event.Store {
	t.Helper()
	pool := conformancePool(t, "pgx")
	beside := storeOver(t, pool, this.schema, this.spec, VerifySchema)
	if err := pool.Close(); err != nil {
		t.Fatalf("closing the pool under a prepared store answered %v", err)
	}
	return beside
}

// A cursor of a log this store does not carry, taken from a store over a second
// schema of the same database rather than invented, so what refuses it is the
// real parser reading a real cursor.
func cursorOfAnotherSchema(t *testing.T, schema Schema, spec Spec) event.Cursor {
	t.Helper()
	beside := conformanceStore(t, schema, spec, scratchName())
	_, cursor, err := beside.ReadAll(context.WithoutCancel(t.Context()), "")
	if err != nil {
		t.Fatalf("a walk of a second schema's empty log answered %v", err)
	}
	if cursor == "" {
		t.Fatal("a walk of a second schema's empty log answered no cursor, so nothing below names another log")
	}
	return cursor
}

type conflicting struct{ event.Store }

// The real admission predicate, asked at a version the stream is not at: one
// version above the one the caller decided at is a version no stream this append
// could win is standing at, so PostgreSQL answers zero rows for real.
func (this conflicting) Append(ctx context.Context, request event.AppendRequest) error {
	request.Expected++
	return this.Store.Append(ctx, request)
}

type badCursor struct {
	event.Store
	foreign event.Cursor
}

func (this badCursor) ReadAll(ctx context.Context, _ event.Cursor) ([]event.Envelope, event.Cursor, error) {
	return this.Store.ReadAll(ctx, this.foreign)
}

type unclassified struct{ event.Store }

func (this unclassified) Append(context.Context, event.AppendRequest) error { return errInjected }

func (this unclassified) ReadStream(context.Context, event.Stream, event.Version) ([]event.Envelope, error) {
	return nil, errInjected
}

func (this unclassified) ReadAll(context.Context, event.Cursor) ([]event.Envelope, event.Cursor, error) {
	return nil, "", errInjected
}

// The drop is deferred rather than sequenced, because an audit that cannot ask
// its question leaves through t.Fatal and a run whose schemas survive it reads
// its own leftovers as a foreign writer on the next pass.
func auditThenDrop(t *testing.T, schema string) {
	defer dropSchema(t, schema)
	for _, finding := range auditOf(t, schema).findings {
		t.Errorf("the schema a conformance store wrote to is inconsistent: %s", finding)
	}
}

// §INV-080, and the case that makes every other claim about checkpoints mean
// something. A store whose cursor is the highest position its own read returned,
// rather than the highest one every position below which has settled, is
// identical to a correct store in a quiescent log — which is why it passed all
// twenty sections until the resumption section held a writer's transaction open
// across the walk. The control is the unmodified store, which must pass the same
// section.
func TestTheNewestPositionCursorNowFailsResumption(t *testing.T) {
	output, code := runsTheSuite(t, append(os.Environ(), mutationVariable+"=in-flight-newest-position"))
	if code == 0 {
		t.Fatalf("a store answering the newest position its own read returned passed the conformance suite, so a cursor that skips a position a writer still holds is invisible to it:\n%s", output)
	}
	if !strings.Contains(output, reportedAs+"resumption: failed") {
		t.Fatalf("the run failed and the resumption section did not report it — the sections that did are %v:\n%s", sectionsThatFailed(output), output)
	}

	control, code := runsTheSuite(t, os.Environ())
	if code != 0 {
		t.Fatalf("the unmodified store fails the same run, so the failure above is not the cursor's:\n%s", control)
	}
	if !strings.Contains(control, reportedAs+"resumption: passed") {
		t.Fatalf("the unmodified store did not pass the resumption section, so what fails above is the section rather than the store:\n%s", control)
	}
}
