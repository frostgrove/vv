//go:build integration

package eventpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/receipt"
)

// The reference implementation's statements, and they are the contract rather
// than an illustration of it. The insert is what blocks on the primary-key
// index; the read is a statement of its own, after it, so that a loser at READ
// COMMITTED takes a second snapshot and sees the row the winner committed while
// it was blocked. %s is the table, which is the one part of these an
// application names for itself.
//
// _examples/event-receipts publishes the same two, and
// TestTheExampleLedgerIsTheOneTheLiveSuiteProved compares them byte for byte and
// in order — so "the reference implementation" is a fact this suite proved
// rather than a label a page applies.
const (
	claimInsertStatement = `INSERT INTO %s (key, fingerprint, family, stream_key, recorded_at)
VALUES ($1, $2, $3, $4, statement_timestamp())
ON CONFLICT (key) DO NOTHING`

	claimSelectStatement = `SELECT key, fingerprint, family, stream_key, first_version, last_version, complete, recorded_at
  FROM %s WHERE key = $1`

	completeStatement = `UPDATE %s SET first_version = $2, last_version = $3, complete = true WHERE key = $1`

	horizonOldestStatement = `SELECT coalesce(min(recorded_at), now()) FROM %s`

	horizonRetentionStatement = `SELECT now() - $1::interval`

	// The process clock written where the database's belongs, which is the
	// second ledger defect and nothing else about it differs.
	claimProcessClockStatement = `INSERT INTO %s (key, fingerprint, family, stream_key, recorded_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (key) DO NOTHING`

	// The horizon read the other way round, which is the third defect: an
	// instant AFTER the oldest row this ledger still answers for reads a row
	// that was swept as one that never existed.
	horizonNewestStatement = `SELECT coalesce(max(recorded_at), now()) FROM %s`
)

// The application's own table, beside the events and in their database. Nothing
// of this framework deploys it: eventpg stays at SchemaVersion 2 and a
// deployment that wants no receipts deploys nothing.
func receiptTable(t *testing.T, name string) string {
	t.Helper()
	mustExecute(t, "DROP TABLE IF EXISTS "+quoteIdentifier(name))
	mustExecute(t, "CREATE TABLE "+quoteIdentifier(name)+` (
		key text PRIMARY KEY,
		fingerprint text NOT NULL,
		family text NOT NULL,
		stream_key text NOT NULL,
		first_version bigint NOT NULL DEFAULT 0,
		last_version bigint NOT NULL DEFAULT 0,
		complete boolean NOT NULL DEFAULT false,
		recorded_at timestamptz NOT NULL
	)`)
	t.Cleanup(func() { _ = execute(t, "DROP TABLE IF EXISTS "+quoteIdentifier(name)) })
	return quoteIdentifier(name)
}

// A receipt.Ledger over SQL, written the way an application writes one: the
// claim and the completion run on the transaction the caller bound to the
// context, and the lookup and the horizon run on the pool, because the whole
// point of resolving is that the first connection is gone.
//
// The flags are the ledger defects, each one thing written wrong. With all of
// them off this is the reference implementation, and
// TestFourLedgerDefectsEachBreakTheCaseThatNamesThem is what keeps the positive
// cases from passing whether or not the statements are the ones the contract
// describes. The fifth, echoesPrint, is driven only through the harness — a
// ledger it certified is the one defect eventtest was measured to miss.
type liveLedger struct {
	source    crud.Source
	backing   event.Backing
	db        *sql.DB
	table     string
	retention time.Duration

	selectFirst  bool
	processClock bool
	optimistic   bool
	dirty        bool
	echoesPrint  bool
	skew         time.Duration

	claims    atomic.Int64
	completes atomic.Int64

	mutex   sync.Mutex
	bound   []any
	holding *sql.Tx
}

func newLiveLedger(store *Store, table string) *liveLedger {
	return &liveLedger{source: store.source, backing: store.Backing(), db: store.db, table: table}
}

// The ledger's own answer to "which transaction is this", minted over the
// backing the store it sits beside named. A ledger on a second handle resolves
// this context to a different *sql.Tx and Authority.Same is what says so.
func (this *liveLedger) Transaction(ctx context.Context) (event.Authority, error) {
	tx, err := boundTx(ctx, this.source)
	if err != nil || tx == nil {
		return event.Authority{}, err
	}
	return event.NewAuthority(this.backing, tx)
}

func (this *liveLedger) Claim(ctx context.Context, held receipt.Receipt) (receipt.Receipt, bool, error) {
	this.claims.Add(1)
	tx, err := this.unit(ctx)
	if err != nil {
		return receipt.Receipt{}, false, err
	}
	// The first defect: the read placed before the insert. What an
	// implementation that puts it there must then do is decide from it — the
	// insert's zero arrives after the decision, which is the whole failure.
	if this.selectFirst {
		found, taken, err := this.read(ctx, tx, held.Key)
		if err != nil || taken {
			return found, false, err
		}
		if _, err := this.insert(ctx, tx, held); err != nil {
			return receipt.Receipt{}, false, err
		}
		return held, true, nil
	}
	won, err := this.insert(ctx, tx, held)
	if err != nil {
		return receipt.Receipt{}, false, err
	}
	found, taken, err := this.read(ctx, tx, held.Key)
	if err != nil || !taken {
		return receipt.Receipt{}, won, err
	}
	// The fifth defect: the RETURNING list that binds the parameter where it means
	// the column. Every loser then carries the fingerprint it asked with, and a
	// collision is answered as a repeat.
	if this.echoesPrint && !won {
		found.Fingerprint = held.Fingerprint
	}
	return found, won, nil
}

func (this *liveLedger) Complete(ctx context.Context, held receipt.Receipt) error {
	this.completes.Add(1)
	tx, err := this.unit(ctx)
	if err != nil {
		return err
	}
	written, err := tx.ExecContext(ctx, fmt.Sprintf(completeStatement, this.table),
		held.Key.Value(), int64(held.First), int64(held.Last))
	if err != nil {
		return causeOf(err)
	}
	affected, err := written.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("the completion of an operation key wrote %d rows where the claim took one", affected)
	}
	return nil
}

// On the pool, which is a connection nobody's transaction is on — except under
// the fourth defect, where it is the claiming transaction's own and an
// uncommitted row reads as a committed one.
func (this *liveLedger) Find(ctx context.Context, key receipt.Key) (receipt.Receipt, bool, error) {
	if this.dirty {
		if tx := this.claiming(); tx != nil {
			return this.read(ctx, tx, key)
		}
	}
	return this.read(ctx, this.db, key)
}

func (this *liveLedger) Horizon(ctx context.Context) (time.Time, error) {
	var at time.Time
	switch {
	case this.optimistic:
		return at, this.db.QueryRowContext(ctx, fmt.Sprintf(horizonNewestStatement, this.table)).Scan(&at)
	case this.retention > 0:
		return at, this.db.QueryRowContext(ctx, horizonRetentionStatement, strconv.FormatInt(int64(this.retention/time.Second), 10)+" seconds").Scan(&at)
	}
	return at, this.db.QueryRowContext(ctx, fmt.Sprintf(horizonOldestStatement, this.table)).Scan(&at)
}

func (this *liveLedger) unit(ctx context.Context) (*sql.Tx, error) {
	tx, err := boundTx(ctx, this.source)
	if err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, errors.New("this context carries no transaction of the ledger's data source, and a claim runs inside the caller's")
	}
	this.remember(tx)
	return tx, nil
}

func (this *liveLedger) insert(ctx context.Context, on executor, held receipt.Receipt) (bool, error) {
	statement := fmt.Sprintf(claimInsertStatement, this.table)
	arguments := []any{held.Key.Value(), held.Fingerprint.String(), held.Stream.Family, string(held.Stream.Key)}
	if this.processClock {
		statement = fmt.Sprintf(claimProcessClockStatement, this.table)
		arguments = append(arguments, time.Now().Add(this.skew))
	}
	this.record(arguments)
	written, err := on.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, causeOf(err)
	}
	affected, err := written.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func (this *liveLedger) read(ctx context.Context, on executor, key receipt.Key) (receipt.Receipt, bool, error) {
	rows, err := on.QueryContext(ctx, fmt.Sprintf(claimSelectStatement, this.table), key.Value())
	if err != nil {
		return receipt.Receipt{}, false, causeOf(err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return receipt.Receipt{}, false, rows.Err()
	}
	var text, print, family, streamKey string
	var first, last int64
	var complete bool
	var recordedAt time.Time
	if err := rows.Scan(&text, &print, &family, &streamKey, &first, &last, &complete, &recordedAt); err != nil {
		return receipt.Receipt{}, false, err
	}
	held, err := rowReceipt(text, print, family, streamKey, first, last, complete, recordedAt)
	return held, err == nil, err
}

// The row as the table holds it, read back through the two doors the package
// publishes for exactly this: a ledger binds Key.Value and Fingerprint.String
// and scans NewKey and ParseFingerprint.
func rowReceipt(key, print, family, streamKey string, first, last int64, complete bool, recordedAt time.Time) (receipt.Receipt, error) {
	held, err := receipt.NewKey(key)
	if err != nil {
		return receipt.Receipt{}, err
	}
	fingerprint, err := receipt.ParseFingerprint(print)
	if err != nil {
		return receipt.Receipt{}, err
	}
	return receipt.Receipt{
		Key:         held,
		Fingerprint: fingerprint,
		Stream:      event.Stream{Family: family, Key: event.Key(streamKey)},
		First:       event.Version(first),
		Last:        event.Version(last),
		Complete:    complete,
		RecordedAt:  recordedAt,
	}, nil
}

func (this *liveLedger) record(arguments []any) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.bound = arguments
}

func (this *liveLedger) claimed() []any {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.bound
}

func (this *liveLedger) remember(tx *sql.Tx) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.holding = tx
}

func (this *liveLedger) claiming() *sql.Tx {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.holding
}

// One deployed schema, one store, one repository and one ledger over the
// store's own handle: everything a receipt case needs, with the events and the
// rows in one database and one transaction.
type receiptStand struct {
	t      *testing.T
	schema Schema
	store  *Store
	repo   *event.Repo[held, string]
	fact   *event.Fact[held, string, held]
	ledger *liveLedger
	table  string
	name   string
	family string
}

func newReceiptStand(t *testing.T, name string) *receiptStand {
	t.Helper()
	schema := deployed(t, "eventpg_s5_"+name)
	rows := "eventpg_s5_" + name + "_receipts"
	receiptTable(t, rows)
	return attachedStand(t, schema.Name, rows, "eventpg.s5."+name)
}

// The same stand over a schema and a table that are already there, which is
// what a second process joining an operation's database has: it deploys
// nothing, drops nothing and verifies what it finds.
func attachedStand(t *testing.T, schema, rows, family string) *receiptStand {
	t.Helper()
	held := Schema{Name: schema}
	store := prepared(t, held)
	repo, fact := boundRepo(t, store, family)
	table := quoteIdentifier(rows)
	return &receiptStand{t: t, schema: held, store: store, repo: repo, fact: fact,
		ledger: newLiveLedger(store, table), table: table, name: rows, family: family}
}

func (this *receiptStand) unit(ctx context.Context, work func(context.Context) error) error {
	return crud.InNewTx(ctx, this.store.source, work)
}

// The caller's own unit at a level the CALLER chose. Nothing in this framework
// sets one ([[D-126]]); what the level decides is how a loser fails.
func (this *receiptStand) unitAt(ctx context.Context, level sql.IsolationLevel, work func(context.Context) error) error {
	return crud.InNewTx(ctx, crudsql.Postgres(this.store.db).WithTxOptions(&sql.TxOptions{Isolation: level}), work)
}

// The load, the decision, the digest and the fingerprint: every step a caller
// takes before it claims anything.
func (this *receiptStand) decide(t *testing.T, ctx context.Context, id string, payloads ...string) (event.At[held], []event.Change[held], receipt.Fingerprint) {
	t.Helper()
	at := loaded(t, ctx, this.repo, id)
	return at, this.changes(id, payloads...), this.digest(t, at, this.changes(id, payloads...)...)
}

func (this *receiptStand) changes(id string, payloads ...string) []event.Change[held] {
	changes := make([]event.Change[held], 0, len(payloads))
	for _, payload := range payloads {
		changes = append(changes, this.fact.New(id, held{Bytes: []byte(payload)}))
	}
	return changes
}

func (this *receiptStand) digest(t *testing.T, at event.At[held], changes ...event.Change[held]) receipt.Fingerprint {
	t.Helper()
	digest, err := this.repo.Digest(at, changes...)
	if err != nil {
		t.Fatalf("digesting the decision answered %v", err)
	}
	print, err := receipt.NewFingerprint(digest)
	if err != nil {
		t.Fatalf("the fingerprint of the decision was refused: %v", err)
	}
	return print
}

func (this *receiptStand) claimSpec(key receipt.Key, at event.At[held], print receipt.Fingerprint) receipt.ClaimSpec {
	return receipt.ClaimSpec{Ledger: this.ledger, Store: this.store, Key: key, Fingerprint: print, Stream: at.Stream()}
}

func (this *receiptStand) resolveSpec(key receipt.Key, issued time.Time) receipt.ResolveSpec {
	return receipt.ResolveSpec{Ledger: this.ledger, Store: this.store, Key: key, Issued: issued}
}

// Claim, decide, append, complete, in one unit of the caller's — the whole
// canonical operation, open-coded, because the cases below have to interleave
// something with it.
func (this *receiptStand) operate(t *testing.T, ctx context.Context, key receipt.Key, id string, payloads ...string) (receipt.Verdict, error) {
	t.Helper()
	var verdict receipt.Verdict
	err := this.unit(ctx, func(inner context.Context) error {
		at, changes, print := this.decide(t, inner, id, payloads...)
		taken, err := receipt.Claim(inner, this.claimSpec(key, at, print))
		verdict = taken.Verdict()
		if err != nil || taken.Verdict() != receipt.Recorded {
			return err
		}
		_, commit, err := this.repo.Append(inner, at, changes...)
		if err != nil {
			return err
		}
		return taken.Complete(inner, commit)
	})
	return verdict, err
}

func (this *receiptStand) stream(id string) event.Stream { return aStream(this.family, id) }

// What the database holds for a stream, counted in psql and cross-checked
// against this process's own read: what UC-227, UC-250 and UC-251 are about is
// how many copies of the events exist, and Go is the party under test.
func (this *receiptStand) copies(t *testing.T, id string) int {
	t.Helper()
	return this.copiesIn(t, this.family, id)
}

func (this *receiptStand) copiesIn(t *testing.T, family, id string) int {
	t.Helper()
	held := len(stored(t, this.schema, aStream(family, id)))
	printed, asked := psqlAnswers(t, "SELECT count(*) FROM "+quoteIdentifier(this.schema.Name)+
		".events WHERE family = '"+family+"' AND key = '"+id+"'")
	if asked && (len(printed) != 1 || printed[0] != strconv.Itoa(held)) {
		t.Fatalf("psql prints %v copies of the events of %q where this process read %d, so one of the two is not reading the database", printed, id, held)
	}
	return held
}

// The receipt row as the table holds it, read on the pool and cross-checked
// against psql.
func (this *receiptStand) row(t *testing.T, key receipt.Key) (receipt.Receipt, bool) {
	t.Helper()
	held, found, err := this.ledger.read(context.WithoutCancel(t.Context()), this.ledger.db, key)
	if err != nil {
		t.Fatalf("the receipt row of an operation key could not be read: %v", err)
	}
	printed, asked := psqlAnswers(t, "SELECT first_version || ':' || last_version || ':' || complete FROM "+
		this.table+" WHERE key = '"+key.Value()+"'")
	if asked {
		var want []string
		if found {
			want = []string{strconv.FormatInt(int64(held.First), 10) + ":" + strconv.FormatInt(int64(held.Last), 10) + ":" + strconv.FormatBool(held.Complete)}
		}
		if len(printed) != len(want) || (len(want) == 1 && printed[0] != want[0]) {
			t.Fatalf("psql prints %v for the receipt row where this process read %v, so one of the two is not reading the database", printed, want)
		}
	}
	return held, found
}

func (this *receiptStand) rowCount(t *testing.T) int {
	t.Helper()
	var counted int
	if err := this.ledger.db.QueryRowContext(context.WithoutCancel(t.Context()), "SELECT count(*) FROM "+this.table).Scan(&counted); err != nil {
		t.Fatalf("the receipts this case wrote could not be counted: %v", err)
	}
	printed, asked := psqlAnswers(t, "SELECT count(*) FROM "+this.table)
	if asked && (len(printed) != 1 || printed[0] != strconv.Itoa(counted)) {
		t.Fatalf("psql prints %v rows in the ledger where this process read %d", printed, counted)
	}
	return counted
}

func operationKey(t *testing.T, raw string) receipt.Key {
	t.Helper()
	key, err := receipt.NewKey(raw)
	if err != nil {
		t.Fatalf("the operation key was refused: %v", err)
	}
	return key
}

// A domain refusal raised after everything else in the unit has been done,
// which is how a case rolls one back without the rollback being about the
// receipt.
var errDomainRefused = errors.New("the domain refused this command")

func TestClaimAppendCompleteAndTheRollbackControl(t *testing.T) {
	stand := newReceiptStand(t, "claim")
	ctx := t.Context()
	key := operationKey(t, "req-claim-alpha")

	var written event.Commit
	var print receipt.Fingerprint
	if err := stand.unit(ctx, func(inner context.Context) error {
		at, changes, digested := stand.decide(t, inner, "A-17", "the first fact", "the second")
		print = digested
		taken, err := receipt.Claim(inner, stand.claimSpec(key, at, digested))
		if err != nil {
			return err
		}
		if taken.Verdict() != receipt.Recorded {
			t.Errorf("a fresh key answered %v where no row existed for it", taken.Verdict())
		}
		_, commit, err := stand.repo.Append(inner, at, changes...)
		if err != nil {
			return err
		}
		written = commit
		return taken.Complete(inner, commit)
	}); err != nil {
		t.Fatalf("the unit that claims, appends and completes answered %v", err)
	}

	held, found := stand.row(t, key)
	if !found {
		t.Fatal("the unit committed and the ledger holds no row for the key it claimed")
	}
	if held.First != written.First() || held.Last != written.Last() || !held.Complete {
		t.Fatalf("the row carries %d..%d complete=%v where the append wrote %d..%d", held.First, held.Last, held.Complete, written.First(), written.Last())
	}
	if !held.Fingerprint.Equal(print) || held.Stream != stand.stream("A-17") {
		t.Fatalf("the row carries a fingerprint of %v over %v, which is not the operation that was claimed", held.Fingerprint, held.Stream)
	}
	if stand.rowCount(t) != 1 {
		t.Fatalf("the ledger holds %d rows where a claim and a completion write one", stand.rowCount(t))
	}
	if stand.copies(t, "A-17") != 2 {
		t.Fatalf("the stream holds %d events where the operation decided two", stand.copies(t, "A-17"))
	}
	if held.RecordedAt.IsZero() {
		t.Fatal("the row carries no instant, and recorded_at is the database's own statement_timestamp()")
	}

	t.Run("the control: the same unit rolled back leaves no row at all", func(t *testing.T) {
		rolled := operationKey(t, "req-claim-bravo")
		err := stand.unit(ctx, func(inner context.Context) error {
			at, changes, digested := stand.decide(t, inner, "B-42", "a fact nobody keeps")
			taken, err := receipt.Claim(inner, stand.claimSpec(rolled, at, digested))
			if err != nil {
				return err
			}
			_, commit, err := stand.repo.Append(inner, at, changes...)
			if err != nil {
				return err
			}
			if err := taken.Complete(inner, commit); err != nil {
				return err
			}
			return errDomainRefused
		})
		if !errors.Is(err, errDomainRefused) {
			t.Fatalf("the unit answered %v where the domain refused it", err)
		}
		if _, found := stand.row(t, rolled); found {
			t.Fatal("the unit rolled back and its row is in the ledger, so the row's existence is evidence about the claim rather than about the transaction")
		}
		if stand.copies(t, "B-42") != 0 {
			t.Fatalf("the rolled-back stream holds %d events, so the row and the events did not roll back together and this control compares two different things", stand.copies(t, "B-42"))
		}
		if stand.rowCount(t) != 1 {
			t.Fatalf("the ledger holds %d rows after a rolled-back claim, where the one committed operation wrote one", stand.rowCount(t))
		}
	})
}

// What one racer did, collected on its own goroutine and asserted on the test's:
// a value crosses the channel and nothing calls t.Fatal off the test goroutine.
type racer struct {
	verdict  receipt.Verdict
	err      error
	held     receipt.Receipt
	appended bool
	blocked  time.Duration
}

// Two callers presenting one key, started so that the claims overlap. The
// winner takes the key and holds its unit open until the loser is seen waiting
// on the primary-key index in pg_stat_activity — which is the evidence that the
// loser blocks on the index rather than on a timeout — and only then commits or
// rolls back.
//
// Each racer claims first and loads inside its own unit afterwards, which is
// the order [SPEC] §1.2 gives and the one Once makes unwritable the other way
// round. The fingerprint is digested before the unit opens, from a decision
// taken at whatever version the store held: it covers the stream and the
// records and not the version, which is what lets two attempts of one operation
// compare equal at all.
func (this *receiptStand) race(t *testing.T, level sql.IsolationLevel, key receipt.Key, id, payload string, winnerCommits bool) (racer, racer) {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	at, changes, print := this.decide(t, ctx, id, payload)
	spec := this.claimSpec(key, at, print)

	run := func(inside func(), commits bool) racer {
		var held racer
		held.err = this.unitAt(ctx, level, func(inner context.Context) error {
			started := time.Now()
			taken, err := receipt.Claim(inner, spec)
			held.blocked = time.Since(started)
			held.verdict, held.held = taken.Verdict(), taken.Receipt()
			if inside != nil {
				inside()
			}
			if err != nil {
				return err
			}
			if taken.Verdict() != receipt.Recorded {
				return nil
			}
			_, token, err := this.repo.Load(inner, id)
			if err != nil {
				return err
			}
			_, commit, err := this.repo.Append(inner, token, changes...)
			if err != nil {
				return err
			}
			held.appended = true
			if err := taken.Complete(inner, commit); err != nil {
				return err
			}
			held.held = taken.Receipt()
			if !commits {
				return errDomainRefused
			}
			return nil
		})
		return held
	}

	claimed, release := make(chan struct{}), make(chan struct{})
	winner, loser := make(chan racer, 1), make(chan racer, 1)
	go func() {
		winner <- run(func() { close(claimed); <-release }, winnerCommits)
	}()
	<-claimed
	go func() { loser <- run(nil, true) }()
	if !this.waitsOnTheIndex(t) {
		close(release)
		<-winner
		<-loser
		t.Fatalf("no session ever waited on a lock over %s, so the loser below did not block on the index and the case is measuring two calls that never overlapped", this.name)
	}
	close(release)
	return <-winner, <-loser
}

// The loser's side of the claim, as PostgreSQL reports it: a backend waiting on
// a Lock inside a statement that names this ledger's table.
func (this *receiptStand) waitsOnTheIndex(t *testing.T) bool {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := this.ledger.db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND state = 'active' AND query LIKE '%' || $1 || '%'`, this.name).Scan(&waiting); err != nil {
			t.Errorf("pg_stat_activity could not be read, so whether the loser blocked was never measured: %v", err)
			return false
		}
		if waiting > 0 {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestTwoCallersRaceOneKey(t *testing.T) {
	ctx := t.Context()

	for _, level := range []struct {
		what      string
		isolation sql.IsolationLevel
		repeats   bool
	}{
		{"read committed", sql.LevelReadCommitted, true},
		{"repeatable read", sql.LevelRepeatableRead, false},
		{"serializable", sql.LevelSerializable, false},
	} {
		t.Run(level.what, func(t *testing.T) {
			stand := newReceiptStand(t, "race"+strconv.Itoa(int(level.isolation)))
			key := operationKey(t, "req-race-"+level.what)
			winner, loser := stand.race(t, level.isolation, key, "A-race", "the operation", true)

			if winner.err != nil || winner.verdict != receipt.Recorded || !winner.appended {
				t.Fatalf("the winner answered %v with %v (appended: %v), and the caller that takes a fresh key records it", winner.err, winner.verdict, winner.appended)
			}
			if loser.appended {
				t.Fatal("both callers appended under one operation key, which is the whole of what a claim prevents")
			}
			if level.repeats {
				if loser.err != nil || loser.verdict != receipt.Repeated {
					t.Fatalf("the loser at %s answered %v with %v, where its insert reports zero rows and its following select reads the row the winner committed", level.what, loser.err, loser.verdict)
				}
				if loser.held.First != winner.held.First || loser.held.Last != winner.held.Last {
					t.Fatalf("the loser was answered the range %d..%d where the winner wrote %d..%d", loser.held.First, loser.held.Last, winner.held.First, winner.held.Last)
				}
			} else {
				fault, carried := errs.AsFault(loser.err)
				if !carried || fault.Code != errs.CodeSerializationFailure || fault.Kind != errs.KindRetryable {
					t.Fatalf("the loser at %s answered %v, where the index made it wait and the level then killed its transaction with a serialisation failure", level.what, loser.err)
				}
				if loser.verdict != 0 {
					t.Fatalf("the loser at %s answered the verdict %v beside its refusal, and a claim that raised 40001 concluded nothing", level.what, loser.verdict)
				}

				retried, err := stand.operate(t, ctx, key, "A-race", "the operation")
				if err != nil || retried != receipt.Repeated {
					t.Fatalf("the retry after a 40001 answered %v with %v, where the winner's row is committed and visible to it", err, retried)
				}
			}
			if got := stand.copies(t, "A-race"); got != 1 {
				t.Fatalf("the stream holds %d copies of the operation's events where exactly one append lands at every level", got)
			}
			if got := stand.rowCount(t); got != 1 {
				t.Fatalf("the ledger holds %d rows for one operation key", got)
			}

			t.Run("the control: the winner rolls back and the loser is free to append", func(t *testing.T) {
				rolled := newReceiptStand(t, "rolled"+strconv.Itoa(int(level.isolation)))
				key := operationKey(t, "req-rolled-"+level.what)
				winner, loser := rolled.race(t, level.isolation, key, "A-rolled", "the operation", false)

				if !errors.Is(winner.err, errDomainRefused) {
					t.Fatalf("the winner answered %v where this arm rolls its unit back", winner.err)
				}
				if loser.err != nil || loser.verdict != receipt.Recorded || !loser.appended {
					t.Fatalf("the loser answered %v with %v (appended: %v): the winner rolled back, so the row it was blocked on never existed and this caller takes the key",
						loser.err, loser.verdict, loser.appended)
				}
				if got := rolled.copies(t, "A-rolled"); got != 1 {
					t.Fatalf("the stream holds %d copies where the winner rolled back and the loser wrote one, so the block resolved on a timeout rather than on the writer's outcome", got)
				}
				if got := rolled.rowCount(t); got != 1 {
					t.Fatalf("the ledger holds %d rows where one caller committed a claim", got)
				}
			})
		})
	}
}

// A second handle on what a deployment would call one database: another pool,
// another transaction, and no sentence about atomicity that means anything. It
// is minted over the store's own backing on purpose, so what the refusal is
// about is the transaction rather than the database.
func (this *receiptStand) elsewhere(t *testing.T) *liveLedger {
	t.Helper()
	pool, err := sql.Open("pgx", os.Getenv(testDSN))
	if err != nil {
		t.Fatalf("a second pool on the same database could not be opened: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return &liveLedger{source: crudsql.Postgres(pool), backing: this.store.Backing(), db: pool, table: this.table}
}

func inAnotherTransaction(t *testing.T, ctx context.Context, ledger *liveLedger) context.Context {
	t.Helper()
	beginner, found := crud.BeginnerOf(ledger.source)
	if !found {
		t.Fatal("the second handle cannot begin a transaction, so nothing below could bind one")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning a transaction on the second handle answered %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	return crud.BindExecutor(ctx, ledger.source, tx)
}

func TestTheTwoPoolRefusalBesideTheOnePoolAcceptance(t *testing.T) {
	stand := newReceiptStand(t, "twopool")
	ctx := t.Context()
	crossed := stand.elsewhere(t)

	refused := stand.unit(ctx, func(inner context.Context) error {
		at, _, print := stand.decide(t, inner, "A-crossed", "a fact nobody keeps")
		spec := stand.claimSpec(operationKey(t, "req-two-pools"), at, print)
		spec.Ledger = crossed
		_, err := receipt.Claim(inAnotherTransaction(t, inner, crossed), spec)
		return err
	})
	if !errors.Is(refused, receipt.ErrSpec) {
		t.Fatalf("a ledger on a second pool answered %v, where a receipt that is not atomic with its append is a row that outlives a rollback", refused)
	}
	if crossed.claims.Load() != 0 {
		t.Fatalf("the ledger on the second pool was reached %d times, and the refusal is at the door before anything is written", crossed.claims.Load())
	}
	if _, found := stand.row(t, operationKey(t, "req-two-pools")); found {
		t.Fatal("the refused claim left a row in the ledger")
	}

	t.Run("the control: one handle for both is accepted", func(t *testing.T) {
		verdict, err := stand.operate(t, ctx, operationKey(t, "req-one-pool"), "A-crossed", "the operation")
		if err != nil || verdict != receipt.Recorded {
			t.Fatalf("the identical composition with one handle for both answered %v with %v, so the refusal above is not discriminating", err, verdict)
		}
		if got := stand.copies(t, "A-crossed"); got != 1 {
			t.Fatalf("the accepted operation left %d events", got)
		}
	})

	// §UC-248: one claim and five completions of it, each a way to undo what the
	// claim proved. The row is read in psql after every refusal, because what a
	// refusal must not do is write.
	t.Run("five completions that are not the claim's", func(t *testing.T) {
		key := operationKey(t, "req-completions")
		var written event.Commit
		if err := stand.unit(ctx, func(inner context.Context) error {
			at, changes, print := stand.decide(t, inner, "A-complete", "the operation")
			taken, err := receipt.Claim(inner, stand.claimSpec(key, at, print))
			if err != nil {
				return err
			}
			_, commit, err := stand.repo.Append(inner, at, changes...)
			if err != nil {
				return err
			}
			written = commit

			elsewhere, _, _ := stand.decide(t, inner, "B-complete", "another aggregate's append")
			_, other, err := stand.repo.Append(inner, elsewhere, stand.changes("B-complete", "another aggregate's append")...)
			if err != nil {
				return err
			}

			second, _ := begin(t, stand.store, nil)
			for _, one := range []struct {
				what string
				run  func() error
			}{
				{"a completion on a context carrying no transaction", func() error { return taken.Complete(ctx, commit) }},
				{"a completion on a second transaction of the same store", func() error { return taken.Complete(second, commit) }},
				{"a completion with another aggregate's commit", func() error { return taken.Complete(inner, other) }},
			} {
				if err := one.run(); !errors.Is(err, receipt.ErrSpec) {
					t.Errorf("%s answered %v", one.what, err)
				}
				if _, found := stand.row(t, key); found {
					t.Errorf("%s left a committed row for a claim whose unit is still open", one.what)
				}
			}

			if err := taken.Complete(inner, commit); err != nil {
				t.Errorf("the claim's own transaction, its own commit, once, on a Recorded verdict answered %v, so the four refusals above refuse everything", err)
			}
			if err := taken.Complete(inner, commit); !errors.Is(err, receipt.ErrSpec) {
				t.Errorf("a second completion of one claim answered %v, where an operation that appended twice would otherwise record one range", err)
			}
			return nil
		}); err != nil {
			t.Fatalf("the unit holding the five completions answered %v", err)
		}

		held, found := stand.row(t, key)
		if !found || held.First != written.First() || held.Last != written.Last() || !held.Complete {
			t.Fatalf("the row carries %d..%d complete=%v (present: %v) where the admitted completion wrote %d..%d",
				held.First, held.Last, held.Complete, found, written.First(), written.Last())
		}

		if err := stand.unit(ctx, func(inner context.Context) error {
			at, _, print := stand.decide(t, inner, "A-complete", "the operation")
			taken, err := receipt.Claim(inner, stand.claimSpec(key, at, print))
			if err != nil {
				return err
			}
			if taken.Verdict() != receipt.Repeated {
				t.Errorf("the second claim of a completed key answered %v", taken.Verdict())
			}
			if err := taken.Complete(inner, event.Commit{}); !errors.Is(err, receipt.ErrSpec) {
				t.Errorf("completing a repeat answered %v, where it would overwrite the first attempt's range with this one's", err)
			}
			return nil
		}); err != nil {
			t.Fatalf("the unit holding the fifth completion answered %v", err)
		}

		after, _ := stand.row(t, key)
		if after.First != written.First() || after.Last != written.Last() {
			t.Fatalf("the row carries %d..%d after the refused completions where the first attempt wrote %d..%d", after.First, after.Last, written.First(), written.Last())
		}
	})
}

// The database's own clock, which is the one every instant in this ledger comes
// from except a caller's Issued. Reading it here rather than time.Now() keeps
// the arms that are not about skew from measuring the offset between this host
// and the server; the two that ARE about skew offset it deliberately.
func (this *receiptStand) databaseNow(t *testing.T) time.Time {
	t.Helper()
	var now time.Time
	if err := this.ledger.db.QueryRowContext(context.WithoutCancel(t.Context()), "SELECT now()").Scan(&now); err != nil {
		t.Fatalf("the database's own clock could not be read: %v", err)
	}
	return now
}

func (this *receiptStand) resolve(t *testing.T, ctx context.Context, ledger receipt.Ledger, key receipt.Key, issued time.Time) (receipt.Resolution, error) {
	t.Helper()
	spec := this.resolveSpec(key, issued)
	spec.Ledger = ledger
	return receipt.Resolve(ctx, spec)
}

func TestUnresolvedWhileOpenAndAfterARollbackAreIndistinguishable(t *testing.T) {
	stand := newReceiptStand(t, "unresolved")
	ctx := t.Context()

	// One committed row, so the ledger's horizon is an instant it really holds
	// rows from rather than "nothing, ever".
	if _, err := stand.operate(t, ctx, operationKey(t, "req-earlier"), "A-earlier", "an earlier operation"); err != nil {
		t.Fatalf("the operation that gives this ledger a horizon answered %v", err)
	}

	open := operationKey(t, "req-open")
	inside, tx := begin(t, stand.store, nil)
	at, changes, print := stand.decide(t, inside, "A-open", "the operation nobody has resolved")
	taken, err := receipt.Claim(inside, stand.claimSpec(open, at, print))
	if err != nil {
		t.Fatalf("the claim inside the transaction this case holds open answered %v", err)
	}
	_, commit, err := stand.repo.Append(inside, at, changes...)
	if err != nil {
		t.Fatalf("the append inside the transaction this case holds open answered %v", err)
	}
	if err := taken.Complete(inside, commit); err != nil {
		t.Fatalf("the completion inside the transaction this case holds open answered %v", err)
	}

	issued := stand.databaseNow(t)
	started := time.Now()
	whileOpen, err := stand.resolve(t, ctx, stand.ledger, open, issued)
	took := time.Since(started)
	if err != nil {
		t.Fatalf("resolving an operation whose writer is still open answered %v", err)
	}
	if whileOpen.Standing != receipt.Unresolved {
		t.Fatalf("an operation whose transaction is still open resolved to %v, and there is no row to lock so nothing tells an open writer from one that rolled back", whileOpen.Standing)
	}
	if took > 5*time.Second {
		t.Fatalf("the resolve took %s, and a resolver that blocks on the writer has turned a non-conclusion into a stall", took)
	}
	if whileOpen.Horizon.IsZero() {
		t.Fatal("the resolve carries no horizon, which is the only thing an Unresolved caller has to make a comparison with")
	}

	// §UC-249: the mirror of the claim's placement rule. A resolve issued inside
	// the unit that took the claim reads its own uncommitted row.
	if _, err := stand.resolve(t, inside, stand.ledger, open, issued); !errors.Is(err, receipt.ErrSpec) {
		t.Fatalf("a resolve on the claiming transaction's own context answered %v, where it reads its own uncommitted row and answers Found for an operation that can still roll back", err)
	}
	crossed := stand.elsewhere(t)
	if _, err := stand.resolve(t, inAnotherTransaction(t, ctx, crossed), crossed, open, issued); !errors.Is(err, receipt.ErrSpec) {
		t.Fatalf("a resolve inside a transaction of the ledger's and not the store's answered %v, and it reads its own uncommitted row whether or not the event store shares that unit", err)
	}
	if _, err := stand.resolve(t, inside, crossed, open, issued); !errors.Is(err, receipt.ErrSpec) {
		t.Fatalf("a resolve inside a transaction of the store's and not the ledger's answered %v, and each of the two doors refuses on its own", err)
	}

	if err := tx.Commit(inside); err != nil {
		t.Fatalf("committing the transaction this case held open answered %v", err)
	}
	afterCommit, err := stand.resolve(t, ctx, stand.ledger, open, issued)
	if err != nil {
		t.Fatalf("resolving after the writer committed answered %v", err)
	}
	if afterCommit.Standing != receipt.Found || afterCommit.Receipt.First != commit.First() || afterCommit.Receipt.Last != commit.Last() {
		t.Fatalf("the identical call after the commit answered %v with %d..%d where the append wrote %d..%d",
			afterCommit.Standing, afterCommit.Receipt.First, afterCommit.Receipt.Last, commit.First(), commit.Last())
	}

	t.Run("the control: after a rollback it is Unresolved again, and the two absences are one answer", func(t *testing.T) {
		rolled := operationKey(t, "req-rolled-back")
		inside, tx := begin(t, stand.store, nil)
		at, changes, print := stand.decide(t, inside, "A-rolled-back", "an operation that never happened")
		taken, err := receipt.Claim(inside, stand.claimSpec(rolled, at, print))
		if err != nil {
			t.Fatalf("the claim inside the transaction this control rolls back answered %v", err)
		}
		_, commit, err := stand.repo.Append(inside, at, changes...)
		if err != nil {
			t.Fatalf("the append inside the transaction this control rolls back answered %v", err)
		}
		if err := taken.Complete(inside, commit); err != nil {
			t.Fatalf("the completion inside the transaction this control rolls back answered %v", err)
		}
		if err := tx.Rollback(inside); err != nil {
			t.Fatalf("rolling the writer back answered %v", err)
		}

		afterRollback, err := stand.resolve(t, ctx, stand.ledger, rolled, issued)
		if err != nil {
			t.Fatalf("resolving after the writer rolled back answered %v", err)
		}
		if afterRollback.Standing != whileOpen.Standing || afterRollback.Receipt != whileOpen.Receipt {
			t.Fatalf("an operation that rolled back resolved to %v/%+v and one still in flight to %v/%+v, and telling the two apart is the thing this framework must not claim to do",
				afterRollback.Standing, afterRollback.Receipt, whileOpen.Standing, whileOpen.Receipt)
		}
		if stand.copies(t, "A-rolled-back") != 0 {
			t.Fatalf("the rolled-back stream holds %d events", stand.copies(t, "A-rolled-back"))
		}
	})
}

func TestExpiredUnresolvedAZeroIssuedAndTheTwoSkewArms(t *testing.T) {
	stand := newReceiptStand(t, "horizon")
	ctx := t.Context()
	present := operationKey(t, "req-present")
	if _, err := stand.operate(t, ctx, present, "A-present", "the operation this ledger still holds"); err != nil {
		t.Fatalf("the operation this ledger holds a row for answered %v", err)
	}
	mustExecute(t, "UPDATE "+stand.table+" SET recorded_at = now() - interval '1 hour'")
	swept := operationKey(t, "req-swept")

	// The same four standings against the two horizon spellings the contract
	// names: the table's own MIN(recorded_at), and now() - retention computed in
	// SQL for a table that may be empty. Both are the database's clock and
	// neither is a time.Now() in the ledger's process.
	retention := &liveLedger{source: stand.ledger.source, backing: stand.ledger.backing, db: stand.ledger.db, table: stand.table, retention: 2 * time.Hour}
	for _, ledger := range []struct {
		what string
		held *liveLedger
	}{
		{"the oldest row this ledger holds", stand.ledger},
		{"now() - retention, computed in SQL", retention},
	} {
		t.Run(ledger.what, func(t *testing.T) {
			horizon, err := ledger.held.Horizon(ctx)
			if err != nil {
				t.Fatalf("the ledger published no horizon: %v", err)
			}
			for _, one := range []struct {
				what   string
				key    receipt.Key
				issued time.Time
				want   receipt.Standing
			}{
				{"a key minted before the horizon and swept", swept, horizon.Add(-30 * time.Minute), receipt.Expired},
				{"a key minted after the horizon", swept, horizon.Add(30 * time.Minute), receipt.Unresolved},
				{"a caller that cannot date its own key", swept, time.Time{}, receipt.Unresolved},
				{"the row this ledger still holds", present, horizon.Add(30 * time.Minute), receipt.Found},
			} {
				resolution, err := stand.resolve(t, ctx, ledger.held, one.key, one.issued)
				if err != nil {
					t.Fatalf("%s answered %v", one.what, err)
				}
				if resolution.Standing != one.want {
					t.Errorf("%s resolved to %v where the horizon says %v", one.what, resolution.Standing, one.want)
				}
				if resolution.Horizon.Sub(horizon).Abs() > 10*time.Second {
					t.Errorf("%s carries the horizon %s where the ledger published %s", one.what, resolution.Horizon, horizon)
				}
			}
		})
	}

	// The two skew arms. What the offset between a caller's clock and the
	// database's can do is move a caller between two NON-conclusions, and what
	// it cannot do is produce a false Found or a false "it did not happen".
	t.Run("the caller's clock, offset in each direction", func(t *testing.T) {
		horizon, err := stand.ledger.Horizon(ctx)
		if err != nil {
			t.Fatalf("the ledger published no horizon: %v", err)
		}
		for _, one := range []struct {
			what  string
			skew  time.Duration
			truth time.Time
			was   receipt.Standing
			want  receipt.Standing
		}{
			{"a caller whose clock is two hours behind the database's", -2 * time.Hour, horizon.Add(30 * time.Minute), receipt.Unresolved, receipt.Expired},
			{"a caller whose clock is three hours ahead of the database's", 3 * time.Hour, horizon.Add(-30 * time.Minute), receipt.Expired, receipt.Unresolved},
		} {
			resolution, err := stand.resolve(t, ctx, stand.ledger, swept, one.truth.Add(one.skew))
			if err != nil {
				t.Fatalf("%s answered %v", one.what, err)
			}
			if resolution.Standing != one.want {
				t.Errorf("%s resolved to %v where the arithmetic says %v — its own instant is %s and the horizon is %s",
					one.what, resolution.Standing, one.want, one.truth.Add(one.skew), horizon)
			}
			if one.was == receipt.Found || resolution.Standing == receipt.Found {
				t.Errorf("%s moved a caller onto or off a conclusion: it read %v where an unskewed clock reads %v, and both of those must be non-conclusions",
					one.what, resolution.Standing, one.was)
			}
		}
	})
}

func TestThePublishedClaimBindsNoInstantAsAParameter(t *testing.T) {
	stand := newReceiptStand(t, "instant")
	ctx := t.Context()

	if !strings.Contains(claimInsertStatement, "statement_timestamp()") {
		t.Fatalf("the claim statement is %q and the instant a receipt is dated by is the database's own", claimInsertStatement)
	}
	if strings.Contains(claimInsertStatement, "$5") {
		t.Fatalf("the claim statement binds a fifth parameter, and the only instant it may carry is the one the server reads: %q", claimInsertStatement)
	}

	before := stand.databaseNow(t)
	key := operationKey(t, "req-instant")
	if _, err := stand.operate(t, ctx, key, "A-instant", "the operation"); err != nil {
		t.Fatalf("the operation this case reads the row of answered %v", err)
	}
	after := stand.databaseNow(t)

	bound := stand.ledger.claimed()
	if len(bound) != 4 {
		t.Fatalf("the claim bound %d parameters where the published statement takes four", len(bound))
	}
	for at, argument := range bound {
		if instant, is := argument.(time.Time); is {
			t.Fatalf("the claim bound the instant %s as parameter %d, and a receipt dated by the writer's own process clock is one no other reader's arithmetic agrees with", instant, at+1)
		}
	}

	held, found := stand.row(t, key)
	if !found {
		t.Fatal("the committed operation left no row to read an instant off")
	}
	if held.RecordedAt.Before(before) || held.RecordedAt.After(after) {
		t.Fatalf("the row is dated %s, outside the %s..%s this process read off the database around the claim, so the column is not the database's clock",
			held.RecordedAt, before, after)
	}
}

// What the second process is told, and it is the whole of what a retry in a new
// process has: the key, and the names it needs to reach the same database.
const (
	retryRole     = "FROSTGROVE_EVENTPG_S5_RETRY_ROLE"
	retrySchema   = "FROSTGROVE_EVENTPG_S5_RETRY_SCHEMA"
	retryTable    = "FROSTGROVE_EVENTPG_S5_RETRY_TABLE"
	retryFamily   = "FROSTGROVE_EVENTPG_S5_RETRY_FAMILY"
	retryStream   = "FROSTGROVE_EVENTPG_S5_RETRY_STREAM"
	retryKey      = "FROSTGROVE_EVENTPG_S5_RETRY_KEY"
	retryExitCode = 97
)

// The first attempt, in a process of its own, which commits and is then gone
// before it can say so. os.Exit after the commit returns is the connection
// dying between the COMMIT and the response, without a timing window and
// without a second party killing a backend: the caller that issued the command
// never learns its outcome, and nothing it held — no token, no version, no
// state — survives to be handed to the retry.
func theFirstAttempt(t *testing.T, role string) {
	t.Helper()
	stand := attachedStand(t, os.Getenv(retrySchema), os.Getenv(retryTable), os.Getenv(retryFamily))
	ctx := context.Background()
	id := os.Getenv(retryStream)

	if err := stand.unit(ctx, func(inner context.Context) error {
		at, changes, print := stand.decide(t, inner, id, "the operation")
		fmt.Printf("\nfingerprint=%s\n", print)
		if role == "naive" {
			_, _, err := stand.repo.Append(inner, at, changes...)
			return err
		}
		taken, err := receipt.Claim(inner, stand.claimSpec(operationKey(t, os.Getenv(retryKey)), at, print))
		if err != nil {
			return err
		}
		_, commit, err := stand.repo.Append(inner, at, changes...)
		if err != nil {
			return err
		}
		return taken.Complete(inner, commit)
	}); err != nil {
		t.Fatalf("the first attempt answered %v, so the retry below would be about a command that never ran", err)
	}
	os.Exit(retryExitCode)
}

func theFirstAttemptRan(t *testing.T, stand *receiptStand, role, id string, key receipt.Key) string {
	t.Helper()
	output, code := runs(t, "^TestTheLostConnectionRetryEndToEnd$", append(os.Environ(),
		retryRole+"="+role,
		retrySchema+"="+stand.schema.Name,
		retryTable+"="+stand.name,
		retryFamily+"="+stand.family,
		retryStream+"="+id,
		retryKey+"="+key.Value()))
	if code != retryExitCode {
		t.Fatalf("the process holding the first attempt exited %d rather than dying after its commit, so nothing below is a retry:\n%s", code, output)
	}
	for _, line := range strings.Split(output, "\n") {
		if printed, is := strings.CutPrefix(strings.TrimSpace(line), "fingerprint="); is {
			return printed
		}
	}
	t.Fatalf("the process holding the first attempt printed no fingerprint, so the two attempts cannot be compared:\n%s", output)
	return ""
}

func TestTheLostConnectionRetryEndToEnd(t *testing.T) {
	if role := os.Getenv(retryRole); role != "" {
		theFirstAttempt(t, role)
		return
	}

	stand := newReceiptStand(t, "retry")
	ctx := t.Context()
	key := operationKey(t, "req-lost-connection")
	first := theFirstAttemptRan(t, stand, "operation", "A-retry", key)

	if got := stand.copies(t, "A-retry"); got != 1 {
		t.Fatalf("the stream holds %d events after the first attempt, so the process that died left nothing or left twice", got)
	}

	at, _, print := stand.decide(t, ctx, "A-retry", "the operation")
	if at.Version() != 1 {
		t.Fatalf("the retry loaded at version %d, and the only thing a new process can do is load what the store now holds", at.Version())
	}
	if print.String() != first {
		t.Fatalf("the retry digested %s where the first attempt digested %s, and two attempts of one operation are the same operation only if they encode the same bytes",
			print, first)
	}

	var taken receipt.Held
	if err := stand.unit(ctx, func(inner context.Context) error {
		at, _, print := stand.decide(t, inner, "A-retry", "the operation")
		held, err := receipt.Claim(inner, stand.claimSpec(key, at, print))
		taken = held
		return err
	}); err != nil {
		t.Fatalf("the retry's claim answered %v, where the row the first attempt committed carries this operation's own fingerprint", err)
	}
	if taken.Verdict() != receipt.Repeated {
		t.Fatalf("the retry answered %v, where a caller told anything else either appends a second copy or is refused about its own operation", taken.Verdict())
	}
	if taken.Receipt().First != 1 || taken.Receipt().Last != 1 {
		t.Fatalf("the retry was answered the range %d..%d where the first attempt wrote 1..1, and that range is what the caller answers its own client from",
			taken.Receipt().First, taken.Receipt().Last)
	}
	if got := stand.copies(t, "A-retry"); got != 1 {
		t.Fatalf("the stream holds %d copies of the operation's events after the retry", got)
	}
	if got := stand.rowCount(t); got != 1 {
		t.Fatalf("the ledger holds %d rows for one operation key", got)
	}

	t.Run("the control: the same retry without a key writes the events twice", func(t *testing.T) {
		if fingerprint := theFirstAttemptRan(t, stand, "naive", "A-naive", key); fingerprint == "" {
			t.Fatal("the naive first attempt printed no fingerprint")
		}
		if got := stand.copies(t, "A-naive"); got != 1 {
			t.Fatalf("the naive first attempt left %d events", got)
		}
		if err := stand.unit(ctx, func(inner context.Context) error {
			at, changes, _ := stand.decide(t, inner, "A-naive", "the operation")
			_, _, err := stand.repo.Append(inner, at, changes...)
			return err
		}); err != nil {
			t.Fatalf("the naive retry answered %v", err)
		}
		if got := stand.copies(t, "A-naive"); got != 2 {
			t.Fatalf("the naive retry left %d copies of one operation's events, and this control is what measures the mechanism's value rather than asserting it", got)
		}
	})
}

func TestAnUnresolvedClaimRefusesItsRetry(t *testing.T) {
	stand := newReceiptStand(t, "incomplete")
	ctx := t.Context()
	abandoned := operationKey(t, "req-abandoned")

	// A caller that claims and then returns early on a domain refusal without
	// rolling its unit back: the claim commits with complete = false and nobody
	// ever resolves it.
	if err := stand.unit(ctx, func(inner context.Context) error {
		at, _, print := stand.decide(t, inner, "A-abandoned", "a decision that was never appended")
		_, err := receipt.Claim(inner, stand.claimSpec(abandoned, at, print))
		return err
	}); err != nil {
		t.Fatalf("the unit that claims and completes nothing answered %v", err)
	}

	resolution, err := receipt.Resolve(ctx, stand.resolveSpec(abandoned, stand.databaseNow(t)))
	if err != nil {
		t.Fatalf("resolving a claim nobody completed answered %v", err)
	}
	if resolution.Standing != receipt.Incomplete {
		t.Fatalf("a row whose claim committed without its completion resolved to %v: it has no range, so it is not Found, and there is a row, so it is not Unresolved", resolution.Standing)
	}
	if resolution.Receipt.First != 0 || resolution.Receipt.Last != 0 || resolution.Receipt.Complete {
		t.Fatalf("the incomplete row carries %d..%d complete=%v", resolution.Receipt.First, resolution.Receipt.Last, resolution.Receipt.Complete)
	}

	var retried receipt.Held
	refused := stand.unit(ctx, func(inner context.Context) error {
		at, _, print := stand.decide(t, inner, "A-abandoned", "a decision that was never appended")
		held, err := receipt.Claim(inner, stand.claimSpec(abandoned, at, print))
		retried = held
		return err
	})
	if !errors.Is(refused, receipt.ErrIncomplete) {
		t.Fatalf("the retry of an abandoned claim answered %v, where whether that operation's events reached the log is not a question its row answers", refused)
	}
	if retried.Verdict() != 0 {
		t.Fatalf("the retry of an abandoned claim answered the verdict %v beside its refusal, and a state nothing may be concluded from is a refusal rather than an answer", retried.Verdict())
	}

	t.Run("the control: the same caller rolling back leaves no row and resolves to Unresolved", func(t *testing.T) {
		rolled := operationKey(t, "req-abandoned-rolled")
		err := stand.unit(ctx, func(inner context.Context) error {
			at, _, print := stand.decide(t, inner, "A-abandoned-rolled", "a decision that was never appended")
			if _, err := receipt.Claim(inner, stand.claimSpec(rolled, at, print)); err != nil {
				return err
			}
			return errDomainRefused
		})
		if !errors.Is(err, errDomainRefused) {
			t.Fatalf("the unit answered %v where the domain refused it", err)
		}
		resolution, err := receipt.Resolve(ctx, stand.resolveSpec(rolled, stand.databaseNow(t)))
		if err != nil || resolution.Standing != receipt.Unresolved {
			t.Fatalf("a claim that rolled back resolved to %v (%v), so Incomplete does not distinguish a committed claim from an absent one", resolution.Standing, err)
		}
	})

	// §UC-246: a decision that yields no changes is an ordinary outcome, and
	// completing it is what tells a finished no-op from an abandoned claim.
	t.Run("the control: a completed empty range answers Repeated", func(t *testing.T) {
		nothing := operationKey(t, "req-nothing-to-do")
		if err := stand.unit(ctx, func(inner context.Context) error {
			at, _, print := stand.decide(t, inner, "A-nothing")
			taken, err := receipt.Claim(inner, stand.claimSpec(nothing, at, print))
			if err != nil {
				return err
			}
			_, commit, err := stand.repo.Append(inner, at)
			if err != nil {
				return err
			}
			if !commit.Empty() {
				t.Errorf("an append of no changes answered a commit of %d..%d, and a decision that yielded nothing writes nothing", commit.First(), commit.Last())
			}
			return taken.Complete(inner, commit)
		}); err != nil {
			t.Fatalf("the unit that decides nothing and completes answered %v, where a no-op is an ordinary outcome and refusing it leaves the row incomplete", err)
		}

		resolution, err := receipt.Resolve(ctx, stand.resolveSpec(nothing, stand.databaseNow(t)))
		if err != nil || resolution.Standing != receipt.Found || resolution.Receipt.First != 0 || resolution.Receipt.Last != 0 {
			t.Fatalf("a completed no-op resolved to %v with %d..%d (%v)", resolution.Standing, resolution.Receipt.First, resolution.Receipt.Last, err)
		}

		var retried receipt.Held
		if err := stand.unit(ctx, func(inner context.Context) error {
			at, _, print := stand.decide(t, inner, "A-nothing")
			held, err := receipt.Claim(inner, stand.claimSpec(nothing, at, print))
			retried = held
			return err
		}); err != nil {
			t.Fatalf("the retry of a completed no-op answered %v", err)
		}
		if retried.Verdict() != receipt.Repeated {
			t.Fatalf("the retry of a completed no-op answered %v, so ErrIncomplete above is about the missing events rather than about the missing resolution", retried.Verdict())
		}

		t.Run("and Once completes the empty commit without the caller doing anything", func(t *testing.T) {
			through := operationKey(t, "req-nothing-through-once")
			if err := stand.unit(ctx, func(inner context.Context) error {
				at, _, print := stand.decide(t, inner, "A-nothing-once")
				taken, err := receipt.Once(inner, stand.claimSpec(through, at, print), func(inner context.Context) (event.Commit, error) {
					_, commit, err := stand.repo.Append(inner, at)
					return commit, err
				})
				if err != nil {
					return err
				}
				if !taken.Receipt().Complete {
					t.Errorf("Once answered a receipt that is not complete, and a claim that reaches its commit without one is the row Resolve reports as a defect")
				}
				return nil
			}); err != nil {
				t.Fatalf("Once over a decision that yields no changes answered %v", err)
			}
			resolution, err := receipt.Resolve(ctx, stand.resolveSpec(through, stand.databaseNow(t)))
			if err != nil || resolution.Standing != receipt.Found {
				t.Fatalf("the no-op Once completed resolved to %v (%v)", resolution.Standing, err)
			}
		})
	})
}

// One fact whose payload is whatever its codec chose to write, which is the one
// place a decision's bytes can drift between two attempts of one operation.
type stamped struct{ At time.Time }

type clockCodec struct{}

func (clockCodec) Encode(stamped) ([]byte, error) {
	return []byte(time.Now().UTC().Format(time.RFC3339Nano)), nil
}

func (clockCodec) Decode(payload []byte) (stamped, error) {
	at, err := time.Parse(time.RFC3339Nano, string(payload))
	return stamped{At: at}, err
}

func (clockCodec) CanEncode() error { return nil }

type commandCodec struct{}

func (commandCodec) Encode(value stamped) ([]byte, error) {
	return []byte(value.At.UTC().Format(time.RFC3339Nano)), nil
}

func (commandCodec) Decode(payload []byte) (stamped, error) {
	at, err := time.Parse(time.RFC3339Nano, string(payload))
	return stamped{At: at}, err
}

func (commandCodec) CanEncode() error { return nil }

func TestACodecThatDoesNotEncodeTheSameBytesTwice(t *testing.T) {
	stand := newReceiptStand(t, "codec")
	ctx := t.Context()
	command := stamped{At: time.Date(2026, time.September, 12, 9, 0, 0, 0, time.UTC)}

	for _, one := range []struct {
		what    string
		family  string
		codec   event.Codec[stamped]
		want    receipt.Verdict
		refusal error
		copies  int
	}{
		{"a fact whose codec records the clock", "eventpg.s5.codec.clock", clockCodec{}, receipt.Collided, receipt.ErrCollision, 1},
		{"the control: the same aggregate taking its instant from the command", "eventpg.s5.codec.command", commandCodec{}, receipt.Repeated, nil, 1},
	} {
		t.Run(one.what, func(t *testing.T) {
			aggregate, err := event.TryDefine[stamped](one.family, func(id string) event.Key { return event.Key(id) })
			if err != nil {
				t.Fatalf("the aggregate %q was refused: %v", one.family, err)
			}
			fact, err := event.TryDeclare(aggregate, one.family+".stamped", event.From(one.codec),
				func(state stamped, carried stamped) stamped { return carried })
			if err != nil {
				t.Fatalf("the fact of %q was refused: %v", one.family, err)
			}
			repo, err := event.Bind(event.Open(stand.store), aggregate)
			if err != nil {
				t.Fatalf("the declaration this operation is decided on was not bound: %v", err)
			}
			key := operationKey(t, "req-codec-"+one.family)

			attempt := func() (receipt.Verdict, error) {
				var verdict receipt.Verdict
				err := stand.unit(ctx, func(inner context.Context) error {
					_, at, err := repo.Load(inner, "A-codec")
					if err != nil {
						return err
					}
					changes := []event.Change[stamped]{fact.New("A-codec", command)}
					digest, err := repo.Digest(at, changes...)
					if err != nil {
						return err
					}
					print, err := receipt.NewFingerprint(digest)
					if err != nil {
						return err
					}
					taken, err := receipt.Once(inner, receipt.ClaimSpec{
						Ledger: stand.ledger, Store: stand.store, Key: key, Fingerprint: print, Stream: at.Stream(),
					}, func(inner context.Context) (event.Commit, error) {
						_, commit, err := repo.Append(inner, at, changes...)
						return commit, err
					})
					verdict = taken.Verdict()
					return err
				})
				return verdict, err
			}

			if verdict, err := attempt(); err != nil || verdict != receipt.Recorded {
				t.Fatalf("the first attempt answered %v with %v", err, verdict)
			}
			verdict, err := attempt()
			if one.refusal != nil && !errors.Is(err, one.refusal) {
				t.Fatalf("the retry answered %v where a decision that encodes differently every time is a refusal on every retry rather than a duplicate append", err)
			}
			if one.refusal == nil && err != nil {
				t.Fatalf("the retry answered %v", err)
			}
			if verdict != one.want {
				t.Fatalf("the retry answered %v where the two attempts' bytes say %v", verdict, one.want)
			}
			if got := stand.copiesIn(t, one.family, "A-codec"); got != one.copies {
				t.Fatalf("the stream holds %d copies of the operation's events where the retry may add none", got)
			}
		})
	}
}

func TestOneKeyTwoStreamsAndTheKeyPerAppendControl(t *testing.T) {
	stand := newReceiptStand(t, "twostreams")
	ctx := t.Context()
	key := operationKey(t, "req-two-streams")

	// One key over two aggregates: claim for A, append to A, append to B,
	// complete with A's commit.
	operation := func(t *testing.T, key receipt.Key, second string) (receipt.Verdict, error) {
		t.Helper()
		var verdict receipt.Verdict
		err := stand.unit(ctx, func(inner context.Context) error {
			at, changes, print := stand.decide(t, inner, "A-pair", "the A fact")
			taken, err := receipt.Claim(inner, stand.claimSpec(key, at, print))
			verdict = taken.Verdict()
			if err != nil || taken.Verdict() != receipt.Recorded {
				return err
			}
			_, commit, err := stand.repo.Append(inner, at, changes...)
			if err != nil {
				return err
			}
			beside := loaded(t, inner, stand.repo, "B-pair")
			if _, _, err := stand.repo.Append(inner, beside, stand.changes("B-pair", second)...); err != nil {
				return err
			}
			return taken.Complete(inner, commit)
		})
		return verdict, err
	}

	if verdict, err := operation(t, key, "the B fact"); err != nil || verdict != receipt.Recorded {
		t.Fatalf("the operation that appends to two aggregates under one key answered %v with %v", err, verdict)
	}
	verdict, err := operation(t, key, "a B fact that differs")
	if err != nil {
		t.Fatalf("the second operation under the same key answered %v", err)
	}
	if verdict != receipt.Repeated {
		t.Fatalf("the second operation answered %v: one key covers one append, so the second operation's A batch decided it and its B batch was never looked at", verdict)
	}
	if got := stand.copies(t, "B-pair"); got != 1 {
		t.Fatalf("the second aggregate holds %d events: the second operation was reported as already done and its differing B batch never landed, which is the wrong answer this case asserts", got)
	}
	if got := stand.copies(t, "A-pair"); got != 1 {
		t.Fatalf("the first aggregate holds %d events", got)
	}

	t.Run("the control: a key per append answers Repeated for A and Collided for B", func(t *testing.T) {
		perAppend := func(t *testing.T, first, second receipt.Key, fact string) (receipt.Verdict, receipt.Verdict, error) {
			t.Helper()
			var left, right receipt.Verdict
			err := stand.unit(ctx, func(inner context.Context) error {
				atA, changesA, printA := stand.decide(t, inner, "C-pair", "the A fact")
				takenA, err := receipt.Claim(inner, stand.claimSpec(first, atA, printA))
				left = takenA.Verdict()
				if err != nil {
					return err
				}
				atB, changesB, printB := stand.decide(t, inner, "D-pair", fact)
				takenB, err := receipt.Claim(inner, stand.claimSpec(second, atB, printB))
				right = takenB.Verdict()
				if err != nil {
					return err
				}
				if takenA.Verdict() == receipt.Recorded {
					_, commit, err := stand.repo.Append(inner, atA, changesA...)
					if err != nil {
						return err
					}
					if err := takenA.Complete(inner, commit); err != nil {
						return err
					}
				}
				if takenB.Verdict() == receipt.Recorded {
					_, commit, err := stand.repo.Append(inner, atB, changesB...)
					if err != nil {
						return err
					}
					if err := takenB.Complete(inner, commit); err != nil {
						return err
					}
				}
				return nil
			})
			return left, right, err
		}

		left, right := operationKey(t, "req-pair-a"), operationKey(t, "req-pair-b")
		if a, b, err := perAppend(t, left, right, "the B fact"); err != nil || a != receipt.Recorded || b != receipt.Recorded {
			t.Fatalf("the first key-per-append operation answered %v with %v and %v", err, a, b)
		}
		a, b, err := perAppend(t, left, right, "a B fact that differs")
		if !errors.Is(err, receipt.ErrCollision) {
			t.Fatalf("the retry whose B batch differs answered %v, where the B key was spent on another operation", err)
		}
		if a != receipt.Repeated || b != receipt.Collided {
			t.Fatalf("the key-per-append retry answered %v for A and %v for B, which is the answer the hole above does not give", a, b)
		}
		if got, beside := stand.copies(t, "C-pair"), stand.copies(t, "D-pair"); got != 1 || beside != 1 {
			t.Fatalf("the two aggregates hold %d and %d events after a retry that was refused and rolled back", got, beside)
		}
	})

	t.Run("the control: completing A's claim with B's commit is refused", func(t *testing.T) {
		crossed := operationKey(t, "req-crossed-commit")
		err := stand.unit(ctx, func(inner context.Context) error {
			at, changes, print := stand.decide(t, inner, "E-pair", "the A fact")
			taken, err := receipt.Claim(inner, stand.claimSpec(crossed, at, print))
			if err != nil {
				return err
			}
			if _, _, err := stand.repo.Append(inner, at, changes...); err != nil {
				return err
			}
			beside := loaded(t, inner, stand.repo, "F-pair")
			_, other, err := stand.repo.Append(inner, beside, stand.changes("F-pair", "the B fact")...)
			if err != nil {
				return err
			}
			return taken.Complete(inner, other)
		})
		if !errors.Is(err, receipt.ErrSpec) {
			t.Fatalf("completing one aggregate's claim with another's commit answered %v, so the detectable half of this hazard is not refused either", err)
		}
	})
}

func TestTwoMisorderedCallersAndTheirOnceControl(t *testing.T) {
	stand := newReceiptStand(t, "misordered")
	ctx := t.Context()
	ignored := operationKey(t, "req-ignored-verdict")
	if verdict, err := stand.operate(t, ctx, ignored, "A-ignored", "the operation"); err != nil || verdict != receipt.Recorded {
		t.Fatalf("the operation this case repeats against answered %v with %v", err, verdict)
	}

	// The first caller: told Repeated, appends anyway, and calls Complete. The
	// transaction is the enforcement, which is the only enforcement a package
	// that opens no transaction can have.
	refused := stand.unit(ctx, func(inner context.Context) error {
		at, changes, print := stand.decide(t, inner, "A-ignored", "the operation")
		taken, err := receipt.Claim(inner, stand.claimSpec(ignored, at, print))
		if err != nil {
			return err
		}
		if taken.Verdict() != receipt.Repeated {
			t.Errorf("the second claim of a completed key answered %v", taken.Verdict())
		}
		_, commit, err := stand.repo.Append(inner, at, changes...)
		if err != nil {
			return err
		}
		return taken.Complete(inner, commit)
	})
	if !errors.Is(refused, receipt.ErrSpec) {
		t.Fatalf("a completion on a verdict that is not Recorded answered %v", refused)
	}
	if got := stand.copies(t, "A-ignored"); got != 1 {
		t.Fatalf("the stream holds %d copies after a caller that ignored its verdict, where its own rollback is what left one", got)
	}

	// The second caller: appends first and claims afterwards, twice — two
	// retries of one operation at two expected versions. Nothing in this package
	// can see an append that already happened on this context, and the case
	// asserts the damage rather than pretending it is caught.
	after := operationKey(t, "req-after-append")
	appendThenClaim := func() error {
		return stand.unit(ctx, func(inner context.Context) error {
			at, changes, print := stand.decide(t, inner, "B-after", "the operation")
			_, commit, err := stand.repo.Append(inner, at, changes...)
			if err != nil {
				return err
			}
			taken, err := receipt.Claim(inner, stand.claimSpec(after, at, print))
			if err != nil {
				return err
			}
			if taken.Verdict() != receipt.Recorded {
				return nil
			}
			return taken.Complete(inner, commit)
		})
	}
	for attempt := range 2 {
		if err := appendThenClaim(); err != nil {
			t.Fatalf("attempt %d of the caller that claims after appending answered %v", attempt+1, err)
		}
	}
	if got := stand.copies(t, "B-after"); got != 2 {
		t.Fatalf("the stream holds %d copies where a caller that claims after appending is refused by nothing and both of its attempts wrote", got)
	}

	t.Run("the control: the same two operations through Once leave one copy each", func(t *testing.T) {
		through := operationKey(t, "req-through-once")
		once := func() error {
			return stand.unit(ctx, func(inner context.Context) error {
				at, changes, print := stand.decide(t, inner, "C-once", "the operation")
				_, err := receipt.Once(inner, stand.claimSpec(through, at, print), func(inner context.Context) (event.Commit, error) {
					_, commit, err := stand.repo.Append(inner, at, changes...)
					return commit, err
				})
				return err
			})
		}
		for attempt := range 2 {
			if err := once(); err != nil {
				t.Fatalf("attempt %d through Once answered %v", attempt+1, err)
			}
		}
		if got := stand.copies(t, "C-once"); got != 1 {
			t.Fatalf("the stream holds %d copies through Once, where the work cannot run before the claim because Once is what calls it", got)
		}
	})

	t.Run("the control: a caller that appends after Repeated and never completes leaves two", func(t *testing.T) {
		if err := stand.unit(ctx, func(inner context.Context) error {
			at, changes, print := stand.decide(t, inner, "A-ignored", "the operation")
			if _, err := receipt.Claim(inner, stand.claimSpec(ignored, at, print)); err != nil {
				return err
			}
			_, _, err := stand.repo.Append(inner, at, changes...)
			return err
		}); err != nil {
			t.Fatalf("the caller that never completes answered %v", err)
		}
		if got := stand.copies(t, "A-ignored"); got != 2 {
			t.Fatalf("the stream holds %d copies where the refusal is at Complete and this caller skipped it", got)
		}
	})
}

func TestTheCollisionTableLiveBesideTheVersionVariant(t *testing.T) {
	stand := newReceiptStand(t, "collision")
	ctx := t.Context()
	key := operationKey(t, "req-collision")
	if verdict, err := stand.operate(t, ctx, key, "A-collision", "the first fact", "the second"); err != nil || verdict != receipt.Recorded {
		t.Fatalf("the operation this key was spent on answered %v with %v", err, verdict)
	}

	for _, one := range []struct {
		what     string
		id       string
		payloads []string
	}{
		{"one byte of one payload", "A-collision", []string{"the first fact", "the seconc"}},
		{"a different stream, with identical records", "B-collision", []string{"the first fact", "the second"}},
		{"the same records in a different order", "A-collision", []string{"the second", "the first fact"}},
	} {
		t.Run(one.what, func(t *testing.T) {
			var verdict receipt.Verdict
			err := stand.unit(ctx, func(inner context.Context) error {
				at, _, print := stand.decide(t, inner, one.id, one.payloads...)
				taken, err := receipt.Claim(inner, stand.claimSpec(key, at, print))
				verdict = taken.Verdict()
				return err
			})
			if !errors.Is(err, receipt.ErrCollision) {
				t.Fatalf("%s answered %v, and a verdict is a value it is legal to discard while a refusal is not", one.what, err)
			}
			if verdict != receipt.Collided {
				t.Fatalf("%s answered the verdict %v", one.what, verdict)
			}
			if got := stand.copies(t, one.id); got > 2 {
				t.Fatalf("%s left %d events, and a refused claim appends nothing", one.what, got)
			}
			if got := stand.rowCount(t); got != 1 {
				t.Fatalf("%s left %d rows in the ledger for one operation key", one.what, got)
			}
		})
	}

	// The arm that must NOT differ, and it is what makes the three above mean
	// "the content decided it" rather than "something decided it": the same
	// records re-presented after the stream moved on, at a different expected
	// version, are the same operation.
	t.Run("the version variant answers Repeated with the first attempt's range", func(t *testing.T) {
		if err := stand.unit(ctx, func(inner context.Context) error {
			at := loaded(t, inner, stand.repo, "A-collision")
			_, _, err := stand.repo.Append(inner, at, stand.changes("A-collision", "an unrelated fact")...)
			return err
		}); err != nil {
			t.Fatalf("the append that moves the stream on answered %v", err)
		}

		var taken receipt.Held
		var at event.At[held]
		if err := stand.unit(ctx, func(inner context.Context) error {
			token, _, print := stand.decide(t, inner, "A-collision", "the first fact", "the second")
			at = token
			held, err := receipt.Claim(inner, stand.claimSpec(key, token, print))
			taken = held
			return err
		}); err != nil {
			t.Fatalf("the identical records re-presented at a different expected version answered %v", err)
		}
		if at.Version() != 3 {
			t.Fatalf("the re-presentation was decided at version %d, and this arm is about a stream that moved on", at.Version())
		}
		if taken.Verdict() != receipt.Repeated {
			t.Fatalf("the identical records at a different expected version answered %v: the version a token was loaded at is deliberately not in the digest, and a retry in a new process can reproduce nothing else",
				taken.Verdict())
		}
		if taken.Receipt().First != 1 || taken.Receipt().Last != 2 {
			t.Fatalf("the repeat was answered the range %d..%d where the first attempt wrote 1..2", taken.Receipt().First, taken.Receipt().Last)
		}
		if got := stand.copies(t, "A-collision"); got != 3 {
			t.Fatalf("the stream holds %d events where one operation wrote two and one unrelated append wrote one", got)
		}
		if got := stand.copies(t, "B-collision"); got != 0 {
			t.Fatalf("the stream the collided arm named holds %d events", got)
		}
	})
}

// The falsifying half of a conformance suite, without the package: four
// decorators of the reference ledger, each one thing written wrong, each
// asserted to make the case that names it answer the wrong thing. Without these
// the four positive cases pass whether or not the ledger's statements are the
// ones the contract describes — which is the shape
// test/integration/gate_relscope_test.go's "not declared" arm has.
func TestFourLedgerDefectsEachBreakTheCaseThatNamesThem(t *testing.T) {
	ctx := t.Context()

	t.Run("a claim whose select runs before its insert breaks TestTwoCallersRaceOneKey", func(t *testing.T) {
		stand := newReceiptStand(t, "defectorder")
		stand.ledger.selectFirst = true
		key := operationKey(t, "req-select-first")
		winner, loser := stand.race(t, sql.LevelReadCommitted, key, "A-order", "the operation", true)

		if winner.err != nil || winner.verdict != receipt.Recorded {
			t.Fatalf("the winner answered %v with %v", winner.err, winner.verdict)
		}
		if loser.verdict != receipt.Recorded || !loser.appended {
			t.Fatalf("the loser answered %v (appended: %v), where its select ran before the block, saw nothing, and the insert's zero arrived after the decision",
				loser.verdict, loser.appended)
		}
		if got := stand.copies(t, "A-order"); got != 2 {
			t.Fatalf("the stream holds %d copies of one operation's events where the ordering defect lets both callers append — TestTwoCallersRaceOneKey is what asserts one, and it is measuring the order rather than the statement count",
				got)
		}
	})

	t.Run("a claim that binds the writer's own clock breaks TestThePublishedClaimBindsNoInstantAsAParameter", func(t *testing.T) {
		stand := newReceiptStand(t, "defectclock")
		stand.ledger.processClock = true
		stand.ledger.skew = 2 * time.Hour
		if strings.Contains(claimProcessClockStatement, "statement_timestamp()") {
			t.Fatal("the defective claim reads the server's clock after all, so this arm is decorating nothing")
		}

		key := operationKey(t, "req-process-clock")
		if _, err := stand.operate(t, ctx, key, "A-clock", "the operation"); err != nil {
			t.Fatalf("the operation this arm dates a row with answered %v", err)
		}
		bound := stand.ledger.claimed()
		if len(bound) != 5 {
			t.Fatalf("the defective claim bound %d parameters where it binds the four of the published statement and an instant", len(bound))
		}
		if _, is := bound[4].(time.Time); !is {
			t.Fatalf("the defective claim bound %T as its fifth parameter, and the defect is a process clock where the database's belongs", bound[4])
		}

		held, found := stand.row(t, key)
		if !found {
			t.Fatal("the operation left no row to read an instant off")
		}
		if !held.RecordedAt.After(stand.databaseNow(t)) {
			t.Fatalf("the row is dated %s and the database reads %s: this arm skews the writer's clock two hours forward and the column is supposed to carry it",
				held.RecordedAt, stand.databaseNow(t))
		}

		absent := operationKey(t, "req-never-written")
		resolution, err := receipt.Resolve(ctx, stand.resolveSpec(absent, stand.databaseNow(t).Add(time.Minute)))
		if err != nil {
			t.Fatalf("resolving an absent key against a ledger on a process clock answered %v", err)
		}
		if resolution.Standing != receipt.Expired {
			t.Fatalf("an absent key minted a minute ago resolved to %v against a horizon two hours in the future, where the skew arms of TestExpiredUnresolvedAZeroIssuedAndTheTwoSkewArms read Unresolved",
				resolution.Standing)
		}
	})

	t.Run("a horizon from the newest row breaks TestExpiredUnresolvedAZeroIssuedAndTheTwoSkewArms", func(t *testing.T) {
		stand := newReceiptStand(t, "defecthorizon")
		if _, err := stand.operate(t, ctx, operationKey(t, "req-swept-row"), "A-horizon", "an operation the sweep will reach"); err != nil {
			t.Fatalf("the older of this arm's two rows answered %v", err)
		}
		mustExecute(t, "UPDATE "+stand.table+" SET recorded_at = now() - interval '1 hour'")
		if _, err := stand.operate(t, ctx, operationKey(t, "req-recent-row"), "B-horizon", "a recent operation"); err != nil {
			t.Fatalf("the newer of this arm's two rows answered %v", err)
		}

		absent := operationKey(t, "req-never-written")
		issued := stand.databaseNow(t).Add(-30 * time.Minute)
		honest, err := receipt.Resolve(ctx, stand.resolveSpec(absent, issued))
		if err != nil || honest.Standing != receipt.Unresolved {
			t.Fatalf("the reference ledger resolved a key minted after its oldest row to %v (%v), so this arm's comparison has no control", honest.Standing, err)
		}

		stand.ledger.optimistic = true
		optimistic, err := receipt.Resolve(ctx, stand.resolveSpec(absent, issued))
		if err != nil {
			t.Fatalf("resolving against an optimistic horizon answered %v", err)
		}
		if optimistic.Standing != receipt.Expired {
			t.Fatalf("a horizon taken from the newest row resolved the same key to %v where the oldest row reads %v: an instant after the oldest row a ledger holds reads a row that was swept as one that never existed",
				optimistic.Standing, honest.Standing)
		}
		if _, err := receipt.Resolve(ctx, stand.resolveSpec(operationKey(t, "req-swept-row"), issued)); !errors.Is(err, receipt.ErrLedger) {
			t.Fatalf("resolving a row older than the horizon its own ledger published answered %v, and an answer no conformant ledger gives is refused before it is compared", err)
		}
	})

	t.Run("a find on the claiming connection breaks TestUnresolvedWhileOpenAndAfterARollbackAreIndistinguishable", func(t *testing.T) {
		stand := newReceiptStand(t, "defectfind")
		if _, err := stand.operate(t, ctx, operationKey(t, "req-earlier"), "A-earlier", "an earlier operation"); err != nil {
			t.Fatalf("the operation that gives this ledger a horizon answered %v", err)
		}
		stand.ledger.dirty = true
		open := operationKey(t, "req-dirty-find")

		inside, _ := begin(t, stand.store, nil)
		at, changes, print := stand.decide(t, inside, "A-dirty", "the operation nobody has committed")
		taken, err := receipt.Claim(inside, stand.claimSpec(open, at, print))
		if err != nil {
			t.Fatalf("the claim inside the transaction this arm holds open answered %v", err)
		}
		_, commit, err := stand.repo.Append(inside, at, changes...)
		if err != nil {
			t.Fatalf("the append inside the transaction this arm holds open answered %v", err)
		}
		if err := taken.Complete(inside, commit); err != nil {
			t.Fatalf("the completion inside the transaction this arm holds open answered %v", err)
		}

		resolution, err := receipt.Resolve(ctx, stand.resolveSpec(open, stand.databaseNow(t)))
		if err != nil {
			t.Fatalf("resolving against a ledger that looks on the claiming connection answered %v", err)
		}
		if resolution.Standing != receipt.Found {
			t.Fatalf("an operation whose transaction can still roll back resolved to %v on the writer's own connection, where a lookup on a second connection reads Unresolved", resolution.Standing)
		}
		if resolution.Receipt.Key != open || resolution.Receipt.First != commit.First() {
			t.Fatal("the resolve answered a row for another key, so this arm is not reading the uncommitted one")
		}
	})
}
