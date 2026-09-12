// A durable operation receipt beside the append, and what a second process can
// honestly ask about it afterwards.
//
// THIS FILE IS THE REFERENCE `receipt.Ledger`. Its two claim statements are the
// ones the live suite ran against PostgreSQL 17.9, and
// `TestTheExampleLedgerIsTheOneTheLiveSuiteProved` compares them with that
// suite's fixture byte for byte AND IN ORDER — so "the reference
// implementation" is a fact somebody measured rather than a label this comment
// applies. A ledger that differs from these statements is the one that owes the
// argument.
//
// The claim is `INSERT … ON CONFLICT (key) DO NOTHING` and THEN a `SELECT` of
// the same key, in that order, in the caller's one transaction. Three things
// make that spelling the one, and all three were executed rather than reasoned
// about:
//
//   - The INSERT is what blocks. PostgreSQL's speculative insertion makes
//     `DO NOTHING` wait on a conflicting uncommitted tuple and then insert or not
//     on that transaction's outcome — which is why a claim never has to answer
//     "unresolved" and a `Resolve` does.
//   - THE READ HAS TO BE A STATEMENT OF ITS OWN. At READ COMMITTED a second
//     statement is a second snapshot, and that is what lets a loser read the row
//     the winner committed while it was blocked. A single statement that tries
//     to do both shares one snapshot, taken before the winner committed, and
//     answers the loser zero rows — neither a win nor a repeat, and refused by
//     `receipt`'s own door with `ErrLedger`.
//   - A `SELECT` placed FIRST is a measured defect: it sees nothing, its caller
//     decides it is first and appends, and the insert's zero arrives after the
//     decision. Both callers then write the same events.
//
// `recorded_at` is `statement_timestamp()` and never a bound parameter, so every
// instant this mechanism compares comes from one clock — the database's. The
// horizon is computed in SQL for the same reason.
//
// What the file shows, in order: the `Once` shape a command uses; the open-coded
// `Claim` / `Append` / `Complete` form beside it, for the caller that must answer
// a repeat differently; a collision; a claim nobody completed; an operation
// resolved from another connection while its writing transaction is still open;
// and the sweep, which is the application's and is a supervised periodic runner
// rather than anything this framework does for you.
//
//	make up                                # from the repository root
//	cd _examples && GOWORK=off go run ./event-receipts
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventpg"
	"github.com/frostgrove/vv/event/receipt"
	"github.com/frostgrove/vv/runtime"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultDSN = "postgres://vv:vv@localhost:55432/vv?sslmode=disable"

// The four statements of a conformant ledger. The first two are the claim, and
// their ORDER is the mechanism rather than a style; the third is the completion
// and the fourth is the horizon, computed in SQL over a configured retention so
// no process clock is read anywhere.
const (
	claimInsertStatement = `INSERT INTO %s (key, fingerprint, family, stream_key, recorded_at)
VALUES ($1, $2, $3, $4, statement_timestamp())
ON CONFLICT (key) DO NOTHING`

	claimSelectStatement = `SELECT key, fingerprint, family, stream_key, first_version, last_version, complete, recorded_at
  FROM %s WHERE key = $1`

	completeStatement = `UPDATE %s SET first_version = $2, last_version = $3, complete = true WHERE key = $1`

	horizonRetentionStatement = `SELECT now() - $1::interval`

	sweepStatement = `DELETE FROM %s WHERE recorded_at < now() - $1::interval`
)

type Order struct{ Total int64 }

type Placed struct{ Total int64 }

var Orders = event.Define[Order, string]("receiptorder", func(id string) event.Key { return event.Key(id) })

var Place = event.Declare(Orders, "receiptorder.placed", event.From(event.JSON[Placed]()), func(state Order, fact Placed) Order {
	state.Total += fact.Total
	return state
})

func main() {
	dsn := flag.String("dsn", defaultDSN, "the database the events and the receipts both live in")
	retention := flag.Duration("retention", 24*time.Hour, "how long a receipt answers for; see the module page for how to choose it")
	flag.Parse()
	if err := run(context.Background(), *dsn, *retention); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dsn string, retention time.Duration) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	source := crudsql.Postgres(db)
	store, err := eventpg.New(eventpg.Spec{
		DB: db, Source: source,
		Schema:           eventpg.Schema{Name: "example_receipts"},
		SchemaManagement: eventpg.ManageSchema,
	})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	if err := store.Prepare(ctx); err != nil {
		return err
	}
	if err := bootstrap(ctx, db); err != nil {
		return err
	}

	orders, err := event.Bind(event.Open(store), Orders)
	if err != nil {
		return err
	}
	book := &ledger{db: db, source: source, backing: store.Backing(), table: "example_receipts_ledger", retention: retention}

	for _, step := range []func(context.Context, *event.Repo[Order, string], event.Store, crud.Source, *ledger) error{
		theOnceShape,
		theRepeatThatCostsNoAppend,
		theOpenCodedForm,
		theCollision,
		theClaimNobodyCompleted,
		theOperationStillInFlight,
	} {
		if err := step(ctx, orders, store, source, book); err != nil {
			return err
		}
	}
	return theSweep(ctx, book)
}

// The shape a command takes. `Once` owns the order — claim, decide, append,
// complete — and the claim is FIRST: a receipt written only after the append is
// safe against two writers at one expected version and not against two retries
// at two different ones, which is what a caller that does not know the outcome
// does. Both of those append, and the collision is found with both sets of
// events already in the log.
func theOnceShape(ctx context.Context, orders *event.Repo[Order, string], store event.Store, source crud.Source, held *ledger) error {
	key := freshKey("once")
	verdict, receiptRow, err := place(ctx, orders, store, source, held, key, "once-order", 1200)
	if err != nil {
		return err
	}
	fmt.Printf("Once: verdict=%s range=%d..%d complete=%v\n", verdict, receiptRow.First, receiptRow.Last, receiptRow.Complete)
	return nil
}

// The same key, the same content — the retry the mechanism exists for. It costs
// one statement and no append, and the caller answers its client from the range
// the FIRST attempt wrote.
func theRepeatThatCostsNoAppend(ctx context.Context, orders *event.Repo[Order, string], store event.Store, source crud.Source, held *ledger) error {
	key := freshKey("repeat")
	if _, _, err := place(ctx, orders, store, source, held, key, "repeat-order", 900); err != nil {
		return err
	}
	appends := held.appends
	verdict, receiptRow, err := place(ctx, orders, store, source, held, key, "repeat-order", 900)
	if err != nil {
		return err
	}
	if held.appends != appends {
		return fmt.Errorf("the repeat appended %d more time(s)", held.appends-appends)
	}
	fmt.Printf("repeat: verdict=%s range=%d..%d, and no second append\n", verdict, receiptRow.First, receiptRow.Last)
	return nil
}

// The open-coded form, and what it is for: a caller that must answer a repeat
// DIFFERENTLY from a first write — a different status code, a different
// response body, a metric. Everything `Once` refuses, this refuses too.
func theOpenCodedForm(ctx context.Context, orders *event.Repo[Order, string], store event.Store, source crud.Source, held *ledger) error {
	key := freshKey("open-coded")
	id := "open-coded-order"

	var answer string
	if err := crud.InNewTx(ctx, source, func(ctx context.Context) error {
		_, at, err := orders.Load(ctx, id)
		if err != nil {
			return err
		}
		changes := []event.Change[Order]{Place.New(id, Placed{Total: 300})}

		fingerprint, err := fingerprintOf(orders, at, changes)
		if err != nil {
			return err
		}
		claim, err := receipt.Claim(ctx, receipt.ClaimSpec{
			Ledger: held, Store: store, Key: key, Fingerprint: fingerprint, Stream: at.Stream(),
		})
		if err != nil {
			return err
		}
		if claim.Verdict() == receipt.Repeated {
			answer = "200 from the first attempt's range"
			return nil
		}
		_, commit, err := orders.Append(ctx, at, changes...)
		if err != nil {
			return err
		}
		answer = "201, and this attempt wrote it"
		return claim.Complete(ctx, commit)
	}); err != nil {
		return err
	}
	fmt.Printf("open-coded: %s\n", answer)
	return nil
}

// The same key with different content. It is a REFUSAL and not only a verdict,
// because a verdict is a value it is legal to discard: the worst code that
// compiles must refuse rather than spend somebody else's key.
func theCollision(ctx context.Context, orders *event.Repo[Order, string], store event.Store, source crud.Source, held *ledger) error {
	key := freshKey("collision")
	if _, _, err := place(ctx, orders, store, source, held, key, "collision-order", 100); err != nil {
		return err
	}
	_, _, err := place(ctx, orders, store, source, held, key, "collision-order", 250)
	if !errors.Is(err, receipt.ErrCollision) {
		return fmt.Errorf("a different operation under one key answered %v", err)
	}
	fmt.Println("collision: the same key with other content is refused, and nothing was appended")
	return nil
}

// A claim that reached its commit with nobody completing it. It is
// `ErrIncomplete` at both doors and it carries NO verdict, because a state
// nothing may be concluded from is a refusal rather than an answer: the events
// of that operation may be in the log or may not, and the row reads the same
// either way. A retry told "already done" out of it would report a success that
// may never have happened.
func theClaimNobodyCompleted(ctx context.Context, orders *event.Repo[Order, string], store event.Store, source crud.Source, held *ledger) error {
	key := freshKey("incomplete")
	id := "incomplete-order"

	if err := crud.InNewTx(ctx, source, func(ctx context.Context) error {
		_, at, err := orders.Load(ctx, id)
		if err != nil {
			return err
		}
		fingerprint, err := fingerprintOf(orders, at, []event.Change[Order]{Place.New(id, Placed{Total: 10})})
		if err != nil {
			return err
		}
		_, err = receipt.Claim(ctx, receipt.ClaimSpec{
			Ledger: held, Store: store, Key: key, Fingerprint: fingerprint, Stream: at.Stream(),
		})
		return err // deliberately: no Append, no Complete, and the claim commits
	}); err != nil {
		return err
	}

	_, _, err := place(ctx, orders, store, source, held, key, id, 10)
	if !errors.Is(err, receipt.ErrIncomplete) {
		return fmt.Errorf("a retry through an unresolved claim answered %v", err)
	}

	resolution, err := receipt.Resolve(ctx, receipt.ResolveSpec{Ledger: held, Store: store, Key: key})
	if err != nil {
		return err
	}
	fmt.Printf("incomplete: the retry is refused, and Resolve reports %s — a defect report, not a state to retry through\n", resolution.Standing)
	return nil
}

// AN ABSENT RECEIPT IS NOT A ROLLBACK. The writing transaction below is held
// open on its own connection; a resolver on a second connection sees no row and
// cannot tell that from a transaction that rolled back, because there is no row
// to lock and a share lock blocks on nothing.
//
// Note the ResolveSpec with NO `Issued`: an HTTP handler holding a retried
// idempotency key has the key and not the instant, and requiring one would send
// it to time.Now(), which is after every horizon and turns `Expired` off for
// ever. Absent, an absent row is `Unresolved` and `Resolution.Horizon` carries
// the ledger's own instant so the caller can make the comparison with a date it
// does have.
func theOperationStillInFlight(ctx context.Context, orders *event.Repo[Order, string], store event.Store, source crud.Source, held *ledger) error {
	key := freshKey("in-flight")
	id := "in-flight-order"

	opened := make(chan error, 1)
	release := make(chan struct{})
	go func() {
		opened <- crud.InNewTx(ctx, source, func(ctx context.Context) error {
			_, at, err := orders.Load(ctx, id)
			if err != nil {
				return err
			}
			changes := []event.Change[Order]{Place.New(id, Placed{Total: 55})}
			fingerprint, err := fingerprintOf(orders, at, changes)
			if err != nil {
				return err
			}
			_, err = receipt.Once(ctx, receipt.ClaimSpec{
				Ledger: held, Store: store, Key: key, Fingerprint: fingerprint, Stream: at.Stream(),
			}, func(ctx context.Context) (event.Commit, error) {
				_, commit, err := orders.Append(ctx, at, changes...)
				return commit, err
			})
			if err != nil {
				return err
			}
			<-release // the transaction stays open, which is the whole point
			return nil
		})
	}()

	time.Sleep(150 * time.Millisecond)
	resolution, err := receipt.Resolve(ctx, receipt.ResolveSpec{Ledger: held, Store: store, Key: key})
	if err != nil {
		return err
	}
	if resolution.Standing != receipt.Unresolved {
		return fmt.Errorf("an operation whose transaction is still open resolved as %s", resolution.Standing)
	}
	fmt.Printf("in flight: %s, horizon %s — report it as in flight, do NOT re-issue the command\n",
		resolution.Standing, resolution.Horizon.Format(time.RFC3339))

	close(release)
	if err := <-opened; err != nil {
		return err
	}
	settled, err := receipt.Resolve(ctx, receipt.ResolveSpec{Ledger: held, Store: store, Key: key})
	if err != nil {
		return err
	}
	fmt.Printf("in flight: after the commit it resolves %s at %d..%d\n", settled.Standing, settled.Receipt.First, settled.Receipt.Last)
	return nil
}

// THE SWEEP IS THE APPLICATION'S. Nothing in `receipt` prunes, and nothing in it
// reads a clock; what bounds the window is this runner plus the horizon the
// ledger publishes from the same retention.
//
// Retention must exceed the longest window in which a client may present the
// same key again — conventionally 24 hours for an HTTP idempotency key, and a
// deployment whose retries are a workflow's uses that workflow's timeout
// instead.
func theSweep(ctx context.Context, held *ledger) error {
	sweeping, err := runtime.Every("receipt-sweep", time.Hour, held.sweep)
	if err != nil {
		return err
	}
	// A composition root hands this to the supervisor beside every other runner;
	// here one pass is run directly so the example shows what it does.
	removed, err := held.removed(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("sweep: %q would run hourly; one pass removed %d row(s) older than %s\n",
		sweeping.Name(), removed, held.retention)
	return nil
}

// One command, through `Once`. The digest is taken BEFORE the claim, so a
// malformed append is learnt before a key is spent.
func place(ctx context.Context, orders *event.Repo[Order, string], store event.Store, source crud.Source,
	held *ledger, key receipt.Key, id string, total int64,
) (receipt.Verdict, receipt.Receipt, error) {
	var verdict receipt.Verdict
	var row receipt.Receipt
	err := crud.InNewTx(ctx, source, func(ctx context.Context) error {
		_, at, err := orders.Load(ctx, id)
		if err != nil {
			return err
		}
		changes := []event.Change[Order]{Place.New(id, Placed{Total: total})}
		fingerprint, err := fingerprintOf(orders, at, changes)
		if err != nil {
			return err
		}
		claim, err := receipt.Once(ctx, receipt.ClaimSpec{
			Ledger: held, Store: store, Key: key, Fingerprint: fingerprint, Stream: at.Stream(),
		}, func(ctx context.Context) (event.Commit, error) {
			held.appends++
			_, commit, err := orders.Append(ctx, at, changes...)
			return commit, err
		})
		verdict, row = claim.Verdict(), claim.Receipt()
		return err
	})
	return verdict, row, err
}

// The fingerprint is over the bytes this append WOULD write, and deliberately
// not over the version the token was loaded at: a retry in a new process loads
// what the store now holds — the version the first attempt moved it to — so a
// digest over the version could not be reproduced by the one caller the whole
// mechanism exists for.
func fingerprintOf(orders *event.Repo[Order, string], at event.At[Order], changes []event.Change[Order]) (receipt.Fingerprint, error) {
	digest, err := orders.Digest(at, changes...)
	if err != nil {
		return receipt.Fingerprint{}, err
	}
	return receipt.NewFingerprint(digest)
}

func freshKey(what string) receipt.Key {
	key, err := receipt.NewKey(what + "-" + strconv.FormatInt(time.Now().UnixNano(), 36))
	if err != nil {
		panic(err)
	}
	return key
}

// The application's own table, in the database the events are in, behind the
// interface this framework declares and does not implement.
//
// WHERE EACH METHOD RUNS IS PART OF THE CONTRACT. `Claim` and `Complete` run
// INSIDE the caller's transaction, through the context they are given, and that
// is what makes a receipt and its events one commit. `Find` and `Horizon` run
// OUTSIDE one, on the pool, because the whole point of resolving is that the
// first connection is gone.
type ledger struct {
	db        *sql.DB
	source    crud.Source
	backing   event.Backing
	table     string
	retention time.Duration

	appends int
}

// The ledger's own answer to "which transaction is this", minted over the same
// backing the store beside it named. A ledger on a second pool resolves this
// context to a different transaction and `Authority.Same` is what says so — at
// which point `Claim` refuses, because "atomic" across two handles is a sentence
// with no meaning.
func (this *ledger) Transaction(ctx context.Context) (event.Authority, error) {
	tx, err := this.bound(ctx)
	if err != nil || tx == nil {
		return event.Authority{}, err
	}
	return event.NewAuthority(this.backing, tx)
}

func (this *ledger) Claim(ctx context.Context, held receipt.Receipt) (receipt.Receipt, bool, error) {
	tx, err := this.unit(ctx)
	if err != nil {
		return receipt.Receipt{}, false, err
	}
	written, err := tx.ExecContext(ctx, fmt.Sprintf(claimInsertStatement, this.table),
		held.Key.Value(), held.Fingerprint.String(), held.Stream.Family, string(held.Stream.Key))
	if err != nil {
		return receipt.Receipt{}, false, err
	}
	affected, err := written.RowsAffected()
	if err != nil {
		return receipt.Receipt{}, false, err
	}
	found, taken, err := this.read(ctx, tx, held.Key)
	if err != nil || !taken {
		return receipt.Receipt{}, affected == 1, err
	}
	return found, affected == 1, nil
}

func (this *ledger) Complete(ctx context.Context, held receipt.Receipt) error {
	tx, err := this.unit(ctx)
	if err != nil {
		return err
	}
	written, err := tx.ExecContext(ctx, fmt.Sprintf(completeStatement, this.table),
		held.Key.Value(), int64(held.First), int64(held.Last))
	if err != nil {
		return err
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

// On the pool, which is a connection nobody's transaction is on. Reading it on
// the claiming transaction instead would answer `Found` for an operation that
// can still roll back.
func (this *ledger) Find(ctx context.Context, key receipt.Key) (receipt.Receipt, bool, error) {
	return this.read(ctx, this.db, key)
}

// Computed in SQL over the configured retention, so it is the database's clock
// and it is correct for an empty table. `MAX(recorded_at)` here would be the
// inverse of the contract: an instant AFTER the oldest row this ledger still
// answers for reads a swept row as one that never existed.
func (this *ledger) Horizon(ctx context.Context) (time.Time, error) {
	var at time.Time
	err := this.db.QueryRowContext(ctx, horizonRetentionStatement, this.interval()).Scan(&at)
	return at, err
}

func (this *ledger) sweep(ctx context.Context) error {
	_, err := this.removed(ctx)
	return err
}

func (this *ledger) removed(ctx context.Context) (int64, error) {
	written, err := this.db.ExecContext(ctx, fmt.Sprintf(sweepStatement, this.table), this.interval())
	if err != nil {
		return 0, err
	}
	return written.RowsAffected()
}

func (this *ledger) interval() string {
	return strconv.FormatInt(int64(this.retention/time.Second), 10) + " seconds"
}

type executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (this *ledger) read(ctx context.Context, on executor, key receipt.Key) (receipt.Receipt, bool, error) {
	var text, print, family, streamKey string
	var first, last int64
	var complete bool
	var recordedAt time.Time
	err := on.QueryRowContext(ctx, fmt.Sprintf(claimSelectStatement, this.table), key.Value()).
		Scan(&text, &print, &family, &streamKey, &first, &last, &complete, &recordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return receipt.Receipt{}, false, nil
	}
	if err != nil {
		return receipt.Receipt{}, false, err
	}
	// The two doors the package publishes for exactly this: a ledger binds
	// Key.Value and Fingerprint.String, and scans NewKey and ParseFingerprint.
	held, err := receipt.NewKey(text)
	if err != nil {
		return receipt.Receipt{}, false, err
	}
	fingerprint, err := receipt.ParseFingerprint(print)
	if err != nil {
		return receipt.Receipt{}, false, err
	}
	return receipt.Receipt{
		Key:         held,
		Fingerprint: fingerprint,
		Stream:      event.Stream{Family: family, Key: event.Key(streamKey)},
		First:       event.Version(first),
		Last:        event.Version(last),
		Complete:    complete,
		RecordedAt:  recordedAt,
	}, true, nil
}

func (this *ledger) unit(ctx context.Context) (*sql.Tx, error) {
	tx, err := this.bound(ctx)
	if err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, errors.New("this context carries no transaction of the ledger's data source, and a claim runs inside the caller's")
	}
	return tx, nil
}

func (this *ledger) bound(ctx context.Context) (*sql.Tx, error) {
	held, found := crud.ExecutorFor(ctx, this.source)
	if !found {
		return nil, nil
	}
	tx, taken := crudsql.Transaction(held)
	if !taken {
		return nil, errors.New("what this context carries for the ledger is not a transaction")
	}
	return tx, nil
}

// The example creates its own table so it runs against an empty database. A real
// deployment owns this one; `eventpg` stays at its own schema version and knows
// nothing about it.
func bootstrap(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS example_receipts_ledger (
		key           text PRIMARY KEY,
		fingerprint   text NOT NULL,
		family        text NOT NULL,
		stream_key    text NOT NULL,
		first_version bigint NOT NULL DEFAULT 0,
		last_version  bigint NOT NULL DEFAULT 0,
		complete      boolean NOT NULL DEFAULT false,
		recorded_at   timestamptz NOT NULL
	)`)
	return err
}
