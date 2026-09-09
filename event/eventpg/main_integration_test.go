//go:build integration

package eventpg

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"

	"github.com/jackc/pgx/v5/stdlib"
)

const testDSN = "FROSTGROVE_EVENTPG_TEST_DSN"

// The variable's presence and nothing else: no sql.Open, no ping, no schema, so
// a value that is present and wrong fails the test that needed a database and
// not this. A live suite that skips when it cannot reach one prints ok for a run
// that proved nothing, which is why an unset variable fails here.
func TestMain(m *testing.M) {
	if os.Getenv(testDSN) == "" {
		fmt.Fprintf(os.Stderr, "%s is not set, and this suite proves nothing without a database, so it fails rather than skipping. Run:\n\n  %s='postgres://vv:vv@localhost:55432/vv?sslmode=disable' go test -race -count=1 -tags=integration ./event/eventpg/...\n\n", testDSN, testDSN)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

var live struct {
	once sync.Once
	db   *sql.DB
	err  error
}

func liveDB(t *testing.T) *sql.DB {
	t.Helper()
	live.once.Do(func() {
		live.db, live.err = sql.Open("pgx", os.Getenv(testDSN))
		if live.err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		live.err = live.db.PingContext(ctx)
	})
	if live.err != nil {
		t.Fatalf("%s names a database this test could not reach, so nothing below was proved: %v", testDSN, live.err)
	}
	return live.db
}

func scratch(t *testing.T, name string) string {
	t.Helper()
	dropSchema(t, name)
	t.Cleanup(func() { dropSchema(t, name) })
	return name
}

func dropSchema(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := liveDB(t).ExecContext(ctx, "DROP SCHEMA IF EXISTS "+quoteIdentifier(name)+" CASCADE"); err != nil {
		t.Errorf("the scratch schema %q could not be dropped, so a later run would read this one's rows: %v", name, err)
	}
}

func migrate(t *testing.T, schema Schema) error {
	t.Helper()
	if _, err := MigrationStatements(schema); err != nil {
		t.Fatalf("%+v answered no migration at all: %v", schema, err)
	}
	return deploy(liveDB(t), schema)
}

// The operator's own path: the exported list, run the way MIGRATIONS.md says to
// run it. Nothing here goes through Store.Migrate, so a test that deploys a
// schema and one that verifies it share no code.
func deploy(db *sql.DB, schema Schema) error {
	statements, err := MigrationStatements(schema)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("the migration could not open a transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for index, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("statement %d: %w", index+1, err)
		}
	}
	return tx.Commit()
}

// A schema of this test's own, dropped before and after it, migrated by the
// operator's list. Every case that mutates a schema takes one of these, because
// the mutation is the case and a schema two cases share is a case that passes
// for what the one before it did.
func deployed(t *testing.T, name string) Schema {
	t.Helper()
	schema := Schema{Name: scratch(t, name)}
	if err := migrate(t, schema); err != nil {
		t.Fatalf("the scratch schema %q did not deploy, so nothing below was measured against anything: %v", name, err)
	}
	return schema
}

var common struct {
	once   sync.Once
	schema Schema
	err    error
}

// One deployed schema for the cases that need a schema and not a schema of their
// own. It is dropped and rebuilt at its first use in a run, so a run never reads
// what the run before it wrote.
func sharedSchema(t *testing.T) Schema {
	t.Helper()
	db := liveDB(t)
	common.once.Do(func() {
		common.schema = Schema{Name: "eventpg_shared"}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+quoteIdentifier(common.schema.Name)+" CASCADE"); err != nil {
			common.err = err
			return
		}
		common.err = deploy(db, common.schema)
	})
	if common.err != nil {
		t.Fatalf("the shared schema could not be deployed, so nothing below was proved: %v", common.err)
	}
	return common.schema
}

func openStore(t *testing.T, schema Schema, management SchemaManagement) *Store {
	t.Helper()
	db := liveDB(t)
	store, err := New(Spec{DB: db, Source: crudsql.Postgres(db), Schema: schema, SchemaManagement: management})
	if err != nil {
		t.Fatalf("a store over %+v under %s was refused: %v", schema, management, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func prepared(t *testing.T, schema Schema) *Store {
	t.Helper()
	store := openStore(t, schema, VerifySchema)
	if err := store.Prepare(t.Context()); err != nil {
		t.Fatalf("a store over the deployed schema %q did not verify it: %v", schema.Name, err)
	}
	return store
}

func execute(t *testing.T, statement string, arguments ...any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := liveDB(t).ExecContext(ctx, statement, arguments...)
	return err
}

func mustExecute(t *testing.T, statement string, arguments ...any) {
	t.Helper()
	if err := execute(t, statement, arguments...); err != nil {
		t.Fatalf("%.80q was refused, so the state this test needs was never built: %v", statement, err)
	}
}

func schemaExists(t *testing.T, name string) bool {
	t.Helper()
	var exists bool
	row := liveDB(t).QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, name)
	if err := row.Scan(&exists); err != nil {
		t.Fatalf("whether the schema %q exists could not be read: %v", name, err)
	}
	return exists
}

// The aggregate the live tests declare for themselves. Its one fact encodes to
// the bytes it was handed, which is the only shape through which a payload of an
// exact length reaches a store.
type held struct {
	Bytes []byte
}

type heldCodec struct{}

func (heldCodec) Encode(value held) ([]byte, error) { return value.Bytes, nil }

func (heldCodec) Decode(payload []byte) (held, error) { return held{Bytes: payload}, nil }

func (heldCodec) CanEncode() error { return nil }

func declareHolding(t testing.TB, family string) (*event.Aggregate[held, string], *event.Fact[held, string, held]) {
	t.Helper()
	aggregate, err := event.TryDefine[held](family, func(id string) event.Key { return event.Key(id) })
	if err != nil {
		t.Fatalf("the aggregate %q was refused: %v", family, err)
	}
	fact, err := event.TryDeclare(aggregate, family+".held", event.From(event.Codec[held](heldCodec{})),
		func(state held, carried held) held { return carried })
	if err != nil {
		t.Fatalf("the fact of %q was refused: %v", family, err)
	}
	return aggregate, fact
}

func metaRow(t *testing.T, schema string) (string, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var fingerprint string
	var log []byte
	row := liveDB(t).QueryRowContext(ctx, "SELECT fingerprint, log FROM "+quoteIdentifier(schema)+".schema_meta WHERE singleton")
	if err := row.Scan(&fingerprint, &log); err != nil {
		t.Fatalf("%s.schema_meta holds no row this test could read: %v", schema, err)
	}
	return fingerprint, log
}

func constraintDefinition(t *testing.T, schema, table, constraint string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var definition string
	row := liveDB(t).QueryRowContext(ctx, `
		SELECT pg_get_constraintdef(c.oid)
		  FROM pg_constraint c
		  JOIN pg_class t ON t.oid = c.conrelid
		  JOIN pg_namespace n ON n.oid = t.relnamespace
		 WHERE n.nspname = $1 AND t.relname = $2 AND c.conname = $3`, schema, table, constraint)
	if err := row.Scan(&definition); err != nil {
		t.Fatalf("%s.%s carries no constraint %s: %v", schema, table, constraint, err)
	}
	return definition
}

// A database/sql driver that wraps the one the fixtures register, counts what
// reaches it and can be armed to fail one statement. It is what turns two claims
// this store rests on into measurements rather than readings of its own source:
// an append issued on the *sql.DB rather than on a connection the store checked
// out reaches a driver three times for one call, and an error carrying no
// SQLSTATE is the branch a NotWritten default would answer wrongly.
//
// It implements no driver.NamedValueChecker, so every parameter goes through
// database/sql's own converter — which is the six types §1 promises and nothing
// else.
const countingDriverName = "eventpg_counting"

func init() { sql.Register(countingDriverName, countingDriver{next: stdlib.GetDefaultDriver()}) }

var instrument struct {
	execs   atomic.Int64
	queries atomic.Int64
	armed   atomic.Pointer[injection]
	between atomic.Pointer[interception]
}

// What turns the window between two statements of one call into a case rather
// than a race: the work runs on the connection that issued the statement, before
// it reaches the server, so the call cannot proceed past it until it returns.
type interception struct {
	statement string
	run       func()
	once      atomic.Bool
}

// send says whether the statement is written before the failure is returned,
// which is the whole difference between a write that certainly did not land and
// one whose fate nobody knows. remaining is how many calls it answers, because
// the pool's own retry loop is three and telling three from one is the point.
type injection struct {
	send      bool
	err       error
	answer    driver.Result
	remaining atomic.Int64
}

type countingDriver struct{ next driver.Driver }

func (this countingDriver) Open(name string) (driver.Conn, error) {
	held, err := this.next.Open(name)
	if err != nil {
		return nil, err
	}
	execer, isExecer := held.(driver.ExecerContext)
	queryer, isQueryer := held.(driver.QueryerContext)
	preparer, isPreparer := held.(driver.ConnPrepareContext)
	beginner, isBeginner := held.(driver.ConnBeginTx)
	if !isExecer || !isQueryer || !isPreparer || !isBeginner {
		return nil, fmt.Errorf("the driver under test does not carry the context methods, so counting them would count a fallback path instead")
	}
	return &countingConn{next: held, execer: execer, queryer: queryer, preparer: preparer, beginner: beginner}, nil
}

type countingConn struct {
	next     driver.Conn
	execer   driver.ExecerContext
	queryer  driver.QueryerContext
	preparer driver.ConnPrepareContext
	beginner driver.ConnBeginTx
}

func (this *countingConn) Prepare(query string) (driver.Stmt, error) { return this.next.Prepare(query) }

func (this *countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return this.preparer.PrepareContext(ctx, query)
}

func (this *countingConn) Close() error { return this.next.Close() }

func (this *countingConn) Begin() (driver.Tx, error) { return this.next.Begin() }

func (this *countingConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	return this.beginner.BeginTx(ctx, options)
}

func (this *countingConn) Ping(ctx context.Context) error {
	if pinger, is := this.next.(driver.Pinger); is {
		return pinger.Ping(ctx)
	}
	return nil
}

func (this *countingConn) ResetSession(ctx context.Context) error {
	if resetter, is := this.next.(driver.SessionResetter); is {
		return resetter.ResetSession(ctx)
	}
	return nil
}

func (this *countingConn) ExecContext(ctx context.Context, query string, arguments []driver.NamedValue) (driver.Result, error) {
	instrument.execs.Add(1)
	held := instrument.armed.Load()
	if held == nil || held.remaining.Add(-1) < 0 {
		return this.execer.ExecContext(ctx, query, arguments)
	}
	if !held.send {
		return nil, held.err
	}
	written, err := this.execer.ExecContext(ctx, query, arguments)
	if err != nil {
		return nil, err
	}
	if held.answer != nil {
		return held.answer, nil
	}
	return written, held.err
}

func (this *countingConn) QueryContext(ctx context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	instrument.queries.Add(1)
	if held := instrument.between.Load(); held != nil && query == held.statement && held.once.CompareAndSwap(false, true) {
		held.run()
	}
	return this.queryer.QueryContext(ctx, query, arguments)
}

func arm(t *testing.T, times int, send bool, err error) {
	t.Helper()
	held := &injection{send: send, err: err}
	held.remaining.Store(int64(times))
	instrument.armed.Store(held)
	t.Cleanup(func() { instrument.armed.Store(nil) })
}

// The statement is written and the driver reports something else about what it
// wrote. A row count is the whole of what an append learns from PostgreSQL, so a
// count the store cannot account for is the one thing it has no way to check.
func armResult(t *testing.T, answer driver.Result) {
	t.Helper()
	held := &injection{send: true, answer: answer}
	held.remaining.Store(1)
	instrument.armed.Store(held)
	t.Cleanup(func() { instrument.armed.Store(nil) })
}

type countingResult struct {
	affected int64
	err      error
}

func (this countingResult) LastInsertId() (int64, error) {
	return 0, errors.New("this driver has no last insert id")
}

func (this countingResult) RowsAffected() (int64, error) { return this.affected, this.err }

func interceptOnce(t *testing.T, statement string, run func()) {
	t.Helper()
	instrument.between.Store(&interception{statement: statement, run: run})
	t.Cleanup(func() { instrument.between.Store(nil) })
}

func counted(t *testing.T) (int64, int64) {
	t.Helper()
	return instrument.execs.Load(), instrument.queries.Load()
}

func recount(t *testing.T) {
	t.Helper()
	instrument.execs.Store(0)
	instrument.queries.Store(0)
}

// A pool of its own on the counting driver, so what the counters hold belongs to
// the store the case built and to nothing else in the run.
func countingPool(t *testing.T, connections int) *sql.DB {
	t.Helper()
	pool, err := sql.Open(countingDriverName, os.Getenv(testDSN))
	if err != nil {
		t.Fatalf("a pool on the counting driver could not be opened: %v", err)
	}
	pool.SetMaxOpenConns(connections)
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

func countingStore(t *testing.T, schema Schema, connections int) *Store {
	t.Helper()
	pool := countingPool(t, connections)
	store, err := New(Spec{DB: pool, Source: crudsql.Postgres(pool), Schema: schema})
	if err != nil {
		t.Fatalf("a store over the counting pool was refused: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Prepare(t.Context()); err != nil {
		t.Fatalf("a store over the counting pool did not verify the deployed schema: %v", err)
	}
	recount(t)
	return store
}

func boundRepo(t *testing.T, store *Store, family string) (*event.Repo[held, string], *event.Fact[held, string, held]) {
	t.Helper()
	aggregate, fact := declareHolding(t, family)
	repo, err := event.Bind(event.Open(store), aggregate)
	if err != nil {
		t.Fatalf("a repository over the store could not be bound: %v", err)
	}
	return repo, fact
}

// The store's own classification, read the way the kernel reads it: a failure
// renders its outcome and nothing of its cause, so the comparison is against the
// value event.Failure builds for that outcome and not against a string this file
// wrote down.
func classifiedAs(t *testing.T, err error, outcome event.Outcome, doing string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s was admitted, and the store was supposed to report %v", doing, outcome)
	}
	if err.Error() != event.Failure(outcome, nil).Error() {
		t.Fatalf("%s reported %q rather than a failure the store classified %v", doing, err, outcome)
	}
}

type storedRow struct {
	position   int64
	version    int64
	name       string
	revision   int
	payload    []byte
	recordedAt time.Time
}

func storedOn(t *testing.T, on executor, schema Schema, stream event.Stream) []storedRow {
	t.Helper()
	rows, err := on.QueryContext(t.Context(),
		"SELECT position, version, type, revision, payload, recorded_at FROM "+quoteIdentifier(schema.Name)+
			".events WHERE family = $1 AND key = $2 ORDER BY position", stream.Family, string(stream.Key))
	if err != nil {
		t.Fatalf("the events of %v could not be read: %v", stream, err)
	}
	defer func() { _ = rows.Close() }()
	var held []storedRow
	for rows.Next() {
		var row storedRow
		if err := rows.Scan(&row.position, &row.version, &row.name, &row.revision, &row.payload, &row.recordedAt); err != nil {
			t.Fatalf("an event row of %v could not be read: %v", stream, err)
		}
		held = append(held, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("the events of %v could not be read to their end: %v", stream, err)
	}
	return held
}

func stored(t *testing.T, schema Schema, stream event.Stream) []storedRow {
	t.Helper()
	return storedOn(t, liveDB(t), schema, stream)
}

func streamVersion(t *testing.T, schema Schema, stream event.Stream) (int64, bool) {
	t.Helper()
	rows, err := liveDB(t).QueryContext(t.Context(), "SELECT version FROM "+quoteIdentifier(schema.Name)+
		".streams WHERE family = $1 AND key = $2", stream.Family, string(stream.Key))
	if err != nil {
		t.Fatalf("the stream row of %v could not be read: %v", stream, err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return 0, false
	}
	var version int64
	if err := rows.Scan(&version); err != nil {
		t.Fatalf("the stream row of %v could not be read: %v", stream, err)
	}
	return version, true
}

func aStream(family, key string) event.Stream {
	return event.Stream{Family: family, Key: event.Key(key)}
}

func loaded(t *testing.T, ctx context.Context, repo *event.Repo[held, string], id string) event.At[held] {
	t.Helper()
	_, at, err := repo.Load(ctx, id)
	if err != nil {
		t.Fatalf("loading %q answered %v", id, err)
	}
	return at
}

func payloads(rows []storedRow) []string {
	var held []string
	for _, row := range rows {
		held = append(held, string(row.payload))
	}
	return held
}
