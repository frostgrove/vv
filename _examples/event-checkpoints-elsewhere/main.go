// A checkpoint store of your own, over a database this framework knows nothing
// about — and the one wiring where `InUnit` is accepted and cannot be checked.
//
// Three databases are in play and only two of them are handles here: the event
// log (an `eventmemory` store, so this example needs no event schema), the
// checkpoint database, and the read model's own. The projection's unit of work
// opens a transaction in the **checkpoint** database, so the advance rides in
// it; the handler writes to the **read model's**, which is a second `*sql.DB`
// and therefore a second data source. `Spec.Destination` is
// `projection.Unchecked`, which is the only way to say out loud that the
// framework cannot resolve where the handler writes.
//
// What that costs, measured rather than described: a crash between the
// handler's commit and the unit's leaves the read model's rows in place while
// the advance rolls back. The page is redelivered and a non-idempotent handler
// applies it twice — `AfterApply` semantics under an `InUnit` spec. The handler
// below upserts on `(stream, version)`, which is the identity every envelope
// already carries and the whole of what closes that window.
//
// Naming the read model's own `crud.Source` as `Destination` instead is not the
// fix: the unit binds no transaction of that source, so the projection refuses
// the pass before the handler runs. The two honest wirings are this one, with
// an idempotent handler, and `AfterApply` — which promises the same thing and
// says so in its name.
//
// Run it against the repository's dev database:
//
//	make up                                        # from the repository root
//	cd _examples && GOWORK=off go run ./event-checkpoints-elsewhere
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultDSN = "postgres://vv:vv@localhost:55432/vv?sslmode=disable"

type Ledger struct{ Balance int64 }

type AccountID struct{ Tenant, Number string }

type Opened struct{ Owner string }

type Credited struct{ Amount int64 }

var Account = event.Define[Ledger, AccountID]("account", func(id AccountID) event.Key {
	return event.Compose(id.Tenant, id.Number)
})

var (
	Open   = event.Declare(Account, "account.opened", event.From(event.JSON[Opened]()), openLedger)
	Credit = event.Declare(Account, "account.credited", event.From(event.JSON[Credited]()), creditLedger)
)

func openLedger(state Ledger, _ Opened) Ledger { return state }

func creditLedger(state Ledger, fact Credited) Ledger {
	state.Balance += fact.Amount
	return state
}

func main() {
	checkpointDSN := flag.String("checkpoints", defaultDSN, "the database the checkpoint row lives in")
	readModelDSN := flag.String("read-model", defaultDSN, "the database the read model lives in")
	flag.Parse()

	if err := run(context.Background(), *checkpointDSN, *readModelDSN); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, checkpointDSN, readModelDSN string) error {
	checkpointDB, err := sql.Open("pgx", checkpointDSN)
	if err != nil {
		return err
	}
	defer func() { _ = checkpointDB.Close() }()

	readModelDB, err := sql.Open("pgx", readModelDSN)
	if err != nil {
		return err
	}
	defer func() { _ = readModelDB.Close() }()

	if err := bootstrap(ctx, checkpointDB, readModelDB); err != nil {
		return err
	}

	log, err := filledLog(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	checkpoints := newCheckpoints(checkpointDB)
	defer func() { _ = checkpoints.Close() }()

	balances := &balances{database: readModelDB}
	router := projection.NewRouter(projection.SkipForeign)
	projection.On(router, Open, balances.opened)
	projection.On(router, Credit, balances.credited)

	// The unit opens its transaction in the CHECKPOINT database, because that is
	// where the advance is written. crud.InNewTx binds it on the context, which
	// is what the checkpoint store finds and what makes the advance part of it.
	source := crudsql.Postgres(checkpointDB)
	following, err := projection.New(projection.Spec{
		Name:        "balances",
		Log:         event.ReadOnly(log),
		Checkpoints: checkpoints,
		Handler:     router,
		Advance:     projection.InUnit,
		Unit: func(ctx context.Context, work func(context.Context) error) error {
			return crud.InNewTx(ctx, source, work)
		},
		Destination: projection.Unchecked,
		Idle:        50 * time.Millisecond,
		Observer: projection.ObserverFunc(func(state projection.State) {
			fmt.Printf("%-10s %-10s applied=%d quarantined=%d\n",
				state.Projection, state.Phase, state.Progress.Applied, state.Progress.Quarantined)
		}),
	})
	if err != nil {
		return err
	}
	return drain(ctx, following, readModelDB)
}

// The loop is the caller's goroutine and this package starts none: Run blocks,
// Drain stops it after the pass it is in, and the supervisor a real deployment
// composes does exactly this with a grace period.
func drain(ctx context.Context, following *projection.Projection, readModelDB *sql.DB) error {
	running, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()

	done := make(chan error, 1)
	go func() { done <- following.Run(running) }()

	for following.State().Phase != projection.PhaseFollowing {
		select {
		case err := <-done:
			return err
		case <-running.Done():
			return running.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := following.Drain(running); err != nil {
		return err
	}
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return report(ctx, readModelDB)
}

func report(ctx context.Context, readModelDB *sql.DB) error {
	rows, err := readModelDB.QueryContext(ctx, "SELECT account, balance FROM example_balances ORDER BY account")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var account string
		var balance int64
		if err := rows.Scan(&account, &balance); err != nil {
			return err
		}
		fmt.Printf("%-20s %d\n", account, balance)
	}
	return rows.Err()
}

func filledLog(ctx context.Context) (*eventmemory.Store, error) {
	held, err := eventmemory.NewLog(eventmemory.LogSpec{})
	if err != nil {
		return nil, err
	}
	store, err := eventmemory.New(eventmemory.Spec{Log: held})
	if err != nil {
		return nil, err
	}
	accounts, err := event.Bind(event.Open(store), Account)
	if err != nil {
		return nil, err
	}
	for _, id := range []AccountID{{"acme", "1"}, {"acme", "2"}} {
		_, at, err := accounts.Load(ctx, id)
		if err != nil {
			return nil, err
		}
		if _, _, err := accounts.Append(ctx, at,
			Open.New(id, Opened{Owner: id.Tenant}),
			Credit.New(id, Credited{Amount: 120}),
			Credit.New(id, Credited{Amount: -20}),
		); err != nil {
			return nil, err
		}
	}
	return store, nil
}

// The read model, in its own database. Every write is an upsert keyed on the
// identity the envelope already carries — (stream, version) is unique and stable
// for every event ever written — so a redelivered page changes nothing. That is
// what makes this wiring safe, and it is the handler's own doing rather than the
// framework's.
type balances struct{ database *sql.DB }

func (this *balances) opened(ctx context.Context, _ Opened, envelope event.Envelope) error {
	return this.applied(ctx, envelope, 0)
}

func (this *balances) credited(ctx context.Context, fact Credited, envelope event.Envelope) error {
	return this.applied(ctx, envelope, fact.Amount)
}

func (this *balances) applied(ctx context.Context, envelope event.Envelope, amount int64) error {
	_, err := this.database.ExecContext(ctx,
		`INSERT INTO example_balances (account, balance, version) VALUES ($1, $2, $3)
		 ON CONFLICT (account) DO UPDATE
		    SET balance = example_balances.balance + EXCLUDED.balance,
		        version = EXCLUDED.version
		  WHERE example_balances.version < EXCLUDED.version`,
		string(envelope.Stream.Key), amount, int64(envelope.Version))
	return err
}

func bootstrap(ctx context.Context, checkpointDB, readModelDB *sql.DB) error {
	if _, err := checkpointDB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS example_checkpoints (
			projection  text        NOT NULL PRIMARY KEY,
			cursor      bytea       NOT NULL CHECK (octet_length(cursor) >= 1 AND octet_length(cursor) <= 4096),
			advance     bigint      NOT NULL CHECK (advance > 0),
			highest     bigint      NOT NULL CHECK (highest >= 0),
			applied     bigint      NOT NULL CHECK (applied >= 0),
			quarantined bigint      NOT NULL CHECK (quarantined >= 0),
			updated_at  timestamptz NOT NULL
		)`); err != nil {
		return err
	}
	if _, err := checkpointDB.ExecContext(ctx, `DELETE FROM example_checkpoints WHERE projection = 'balances'`); err != nil {
		return err
	}
	if _, err := readModelDB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS example_balances (
			account text   NOT NULL PRIMARY KEY,
			balance bigint NOT NULL,
			version bigint NOT NULL
		)`); err != nil {
		return err
	}
	_, err := readModelDB.ExecContext(ctx, `TRUNCATE example_balances`)
	return err
}

// The seven methods, over this program's own database. Nothing here opens,
// commits or rolls back anything: the unit of work is the caller's, and this
// store joins whatever transaction it finds bound for its own source.
type checkpoints struct {
	database *sql.DB
	source   crud.Source
	backing  event.Backing
}

var _ event.Checkpoints = (*checkpoints)(nil)

func newCheckpoints(database *sql.DB) *checkpoints {
	backing, err := event.NewBacking(database)
	if err != nil {
		panic(err)
	}
	return &checkpoints{database: database, source: crudsql.Postgres(database), backing: backing}
}

func (this *checkpoints) Capabilities() event.CheckpointCapabilities {
	return event.CheckpointCapabilities{Transactions: event.Supported, Persistence: event.Supported}
}

func (this *checkpoints) Backing() event.Backing { return this.backing }

// What the caller's unit bound for this store's own data source, and nothing
// else. An executor of this source that is not a transaction is refused before
// any statement, because a save through one would be written beside the caller's
// work rather than inside it.
func (this *checkpoints) Transaction(ctx context.Context) (event.Authority, error) {
	tx, err := this.bound(ctx)
	if err != nil || tx == nil {
		return event.Authority{}, err
	}
	return event.NewAuthority(this.backing, tx)
}

var errNotATransaction = errors.New("example: this context carries an executor of this checkpoint store's data source that is not a transaction")

func (this *checkpoints) bound(ctx context.Context) (*sql.Tx, error) {
	held, found := crud.ExecutorFor(ctx, this.source)
	if !found {
		return nil, nil
	}
	tx, taken := crudsql.Transaction(held)
	if !taken {
		return nil, errNotATransaction
	}
	return tx, nil
}

func (this *checkpoints) on(ctx context.Context) (queryer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx, err := this.bound(ctx)
	if err != nil {
		return nil, event.Failure(event.Refused, err)
	}
	if tx != nil {
		return tx, nil
	}
	return this.database, nil
}

type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Absence is the zero checkpoint and never an error: a projection that has never
// run has no row, and Checkpoint.Fresh() is what says so.
func (this *checkpoints) Load(ctx context.Context, name string) (event.Checkpoint, error) {
	on, err := this.on(ctx)
	if err != nil {
		return event.Checkpoint{}, err
	}
	var cursor []byte
	var advance, highest, applied, quarantined int64
	var updatedAt time.Time
	row := on.QueryRowContext(ctx,
		`SELECT cursor, advance, highest, applied, quarantined, updated_at
		   FROM example_checkpoints WHERE projection = $1`, name)
	switch err := row.Scan(&cursor, &advance, &highest, &applied, &quarantined, &updatedAt); {
	case errors.Is(err, sql.ErrNoRows):
		return event.Checkpoint{}, nil
	case err != nil:
		return event.Checkpoint{}, event.Failure(event.Unclassified, err)
	}
	return event.Checkpoint{
		Projection: name,
		Cursor:     event.Cursor(cursor),
		Advance:    uint64(advance),
		Progress: event.Progress{
			Highest:     event.Position(highest),
			Applied:     uint64(applied),
			Quarantined: uint64(quarantined),
			At:          updatedAt,
		},
	}, nil
}

var errCheckpointMoved = errors.New("example: the checkpoint row is not at the advance this save follows")

// The fence, and it is two statements of which exactly one is issued. A save
// above advance 1 MOVES a row and matches nothing where the row has gone; a save
// at advance 1 CREATES one and never overwrites one. A single INSERT … ON
// CONFLICT would ask whether the row is there against its own snapshot and which
// row it collides with against the live index, so a removal committing between
// those two moments would bring a retired row back at an advance no first save
// ever created.
//
// A lost race is a row count of zero and never an error, which is what makes
// Conflict a classification rather than a caught driver message.
func (this *checkpoints) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	on, err := this.on(ctx)
	if err != nil {
		return err
	}
	statement := `UPDATE example_checkpoints
		    SET cursor = $2, advance = $3, highest = $4, applied = $5, quarantined = $6, updated_at = $7
		  WHERE projection = $1 AND advance = $3 - 1`
	if checkpoint.Advance == 1 {
		statement = `INSERT INTO example_checkpoints
			(projection, cursor, advance, highest, applied, quarantined, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (projection) DO NOTHING`
	}
	result, err := on.ExecContext(ctx, statement,
		checkpoint.Projection, []byte(checkpoint.Cursor), int64(checkpoint.Advance),
		int64(checkpoint.Progress.Highest), int64(checkpoint.Progress.Applied),
		int64(checkpoint.Progress.Quarantined), checkpoint.Progress.At)
	if err != nil {
		return event.Failure(event.Unconfirmed, err)
	}
	moved, err := result.RowsAffected()
	if err != nil {
		return event.Failure(event.Unconfirmed, err)
	}
	if moved == 0 {
		return event.Failure(event.Conflict, errCheckpointMoved)
	}
	return nil
}

// Not fenced, and an absent name is not a refusal: forgetting a name that is
// already gone is the state the caller asked for.
func (this *checkpoints) Forget(ctx context.Context, name string) error {
	on, err := this.on(ctx)
	if err != nil {
		return err
	}
	if _, err := on.ExecContext(ctx, `DELETE FROM example_checkpoints WHERE projection = $1`, name); err != nil {
		return event.Failure(event.Unconfirmed, err)
	}
	return nil
}

// It closes nothing it did not open. The pool was opened by the composition root
// and is shared with everything else over it.
func (this *checkpoints) Close() error { return nil }
