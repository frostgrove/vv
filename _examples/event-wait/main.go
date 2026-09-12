// Reading your own change back, without a `sleep` — and the branch for the one
// case waiting cannot fix.
//
// After a confirmed command the next read must not show the old state. The
// framework's answer is not "wait until the projector has no lag": there is no
// head, so there is no zero for a lag to reach. It is **wait for this commit**,
// in **this** generation, over **this** cover — and, where the projection has a
// queue for permanently failing work, wait for it to have been APPLIED rather
// than merely delivered.
//
// The three things this file exists to show, in order:
//
//  1. `WaitOf(spec, cover)` derives the wait from the projection's own `Spec`.
//     Three of the five facts a wait needs are silently wrong-able by a request
//     handler — a `Sequence` that is not the projection's reports reached for a
//     parked event, a nil `Park` reports delivered where the caller asked
//     applied, and a cover that is not the one the rows are recorded at folds a
//     minimum over the wrong set. Deriving them is what stops that.
//
//  2. `spec.Committed(ctx, store, commit)` mints the mark AFTER the caller's
//     transaction has committed. A position read inside the transaction that
//     wrote it belongs to an append that can still roll back, and a rolled-back
//     append burns its position — no projection ever delivers it, so a wait on
//     such a mark would never reach. That call is refused, and this file drives
//     the refusal rather than describing it.
//
//  3. THE PARKED BRANCH. A permanent failure parks the failing envelope and the
//     rest of its sequence, in the same commit as the read model and the
//     advance, and the scan carries on. The checkpoint therefore moves PAST an
//     event the read model never received: a wait that only compared the
//     watermark would answer "reached" for a change sitting in the queue. The
//     park is asked FIRST, on EVERY poll, under the keys the mark carries, and
//     the answer is `ErrParked` — terminal, because waiting longer cannot help
//     and a redrive can.
//
// It needs a database: `ParkSequence` requires the advance, the read model's
// writes and the park write to be one transaction, so the read model has to be a
// handle this framework can resolve rather than a map behind a mutex.
//
//	make up                                # from the repository root
//	cd _examples && GOWORK=off go run ./event-wait
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
	"github.com/frostgrove/vv/event/eventpg"
	"github.com/frostgrove/vv/event/projection"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultDSN = "postgres://vv:vv@localhost:55432/vv?sslmode=disable"

type Order struct{ Placed, Shipped bool }

type Placed struct{ Customer string }

type Shipped struct{ Carrier string }

var errNoSuchCarrier = errors.New("this read model has no such carrier and never will")

var Orders = event.Define[Order, string]("waitorder", func(id string) event.Key { return event.Key(id) })

var (
	Place = event.Declare(Orders, "waitorder.placed", event.From(event.JSON[Placed]()), func(state Order, _ Placed) Order {
		state.Placed = true
		return state
	})
	Ship = event.Declare(Orders, "waitorder.shipped", event.From(event.JSON[Shipped]()), func(state Order, _ Shipped) Order {
		state.Shipped = true
		return state
	})
)

func main() {
	dsn := flag.String("dsn", defaultDSN, "the database the log, the checkpoints, the read model and the park all live in")
	flag.Parse()
	if err := run(context.Background(), *dsn); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	source := crudsql.Postgres(db)
	schema := eventpg.Schema{Name: "example_wait"}

	store, err := eventpg.New(eventpg.Spec{DB: db, Source: source, Schema: schema, SchemaManagement: eventpg.ManageSchema})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	if err := store.Prepare(ctx); err != nil {
		return err
	}
	checkpoints, err := eventpg.NewCheckpoints(eventpg.CheckpointSpec{DB: db, Source: source, Schema: schema, SchemaManagement: eventpg.ManageSchema})
	if err != nil {
		return err
	}
	defer func() { _ = checkpoints.Close() }()
	if err := checkpoints.Prepare(ctx); err != nil {
		return err
	}
	if err := bootstrap(ctx, db); err != nil {
		return err
	}

	orders, err := event.Bind(event.Open(store), Orders)
	if err != nil {
		return err
	}

	model := &readModel{db: db}
	queue := &parkTable{db: db}
	spec := followingSpec(store, checkpoints, source, model, queue)

	following, err := projection.New(spec)
	if err != nil {
		return err
	}

	// ONE WAIT SPEC, BUILT AT THE COMPOSITION ROOT, from the same Spec the runner
	// was built from. A request path fills in Until and nothing else.
	whole, err := projection.NewCover(projection.Whole())
	if err != nil {
		return err
	}
	waiting, err := projection.WaitOf(spec, whole)
	if err != nil {
		return err
	}

	loop, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- following.Run(loop) }()
	defer func() { stop(); <-done }()

	if err := happyPath(ctx, orders, store, source, waiting); err != nil {
		return err
	}
	if err := markedInsideTheTransaction(ctx, orders, store, source, waiting); err != nil {
		return err
	}
	return parkedPath(ctx, orders, store, source, waiting, queue)
}

// Append, mint, wait, read. The mark is minted after crud.InNewTx has returned,
// which is after the COMMIT — the whole of what makes it a position a projection
// will deliver.
func happyPath(ctx context.Context, orders *event.Repo[Order, string], store event.Store, source crud.Source, waiting projection.WaitSpec) error {
	id := fmt.Sprintf("ok-%d", time.Now().UnixNano())

	var commit event.Commit
	if err := crud.InNewTx(ctx, source, func(ctx context.Context) error {
		_, at, err := orders.Load(ctx, id)
		if err != nil {
			return err
		}
		_, commit, err = orders.Append(ctx, at, Place.New(id, Placed{Customer: "acme"}))
		return err
	}); err != nil {
		return err
	}

	mark, err := waiting.Committed(ctx, store, commit)
	if err != nil {
		return err
	}
	waiting.Until = mark

	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	seen, err := projection.Wait(deadline, waiting)
	if err != nil {
		return fmt.Errorf("the confirmed command was not visible: %w", err)
	}
	fmt.Printf("reached after %d poll(s): at=%d behind=%d moved=%v parked=%v\n",
		seen.Polls, seen.At, seen.Behind, seen.Moved, seen.Parked)
	return nil
}

// The refusal that is the reason `Committed` is a separate call rather than
// something `Append` could have answered: inside the transaction that wrote it,
// an envelope's position belongs to an append that can still roll back.
func markedInsideTheTransaction(ctx context.Context, orders *event.Repo[Order, string], store event.Store, source crud.Source, waiting projection.WaitSpec) error {
	id := fmt.Sprintf("inside-%d", time.Now().UnixNano())

	refusal := crud.InNewTx(ctx, source, func(ctx context.Context) error {
		_, at, err := orders.Load(ctx, id)
		if err != nil {
			return err
		}
		_, commit, err := orders.Append(ctx, at, Place.New(id, Placed{Customer: "acme"}))
		if err != nil {
			return err
		}
		_, err = waiting.Committed(ctx, store, commit)
		return err
	})
	if !errors.Is(refusal, projection.ErrSpec) {
		return fmt.Errorf("a mark minted inside the writing transaction answered %v", refusal)
	}
	fmt.Println("a mark minted inside the writing transaction is refused, before the commit it would have described")
	return nil
}

// The branch waiting cannot fix. `Ship` is the fact this read model refuses
// permanently, so the sequence is parked, the scan carries on and the watermark
// passes the change. A wait that compared only the watermark would report
// success for a row the read model never received.
func parkedPath(ctx context.Context, orders *event.Repo[Order, string], store event.Store, source crud.Source, waiting projection.WaitSpec, queue *parkTable) error {
	id := fmt.Sprintf("parked-%d", time.Now().UnixNano())

	var commit event.Commit
	if err := crud.InNewTx(ctx, source, func(ctx context.Context) error {
		_, at, err := orders.Load(ctx, id)
		if err != nil {
			return err
		}
		_, commit, err = orders.Append(ctx, at,
			Place.New(id, Placed{Customer: "acme"}),
			Ship.New(id, Shipped{Carrier: "poison"}),
		)
		return err
	}); err != nil {
		return err
	}

	mark, err := waiting.Committed(ctx, store, commit)
	if err != nil {
		return err
	}
	waiting.Until = mark

	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	seen, err := projection.Wait(deadline, waiting)

	switch {
	case err == nil:
		return fmt.Errorf("the parked change was reported visible: %+v", seen)
	case errors.Is(err, projection.ErrParked):
		// The branch a request handler writes. Waiting longer cannot help; an
		// operator's redrive can, and until then this order's page is stale on
		// purpose rather than by accident.
		held, count := queue.holding(ctx)
		fmt.Printf("parked after %d poll(s): quarantined=%d, %d letter(s) held, sequences %v\n",
			seen.Polls, seen.Quarantined, count, held)
		return nil
	case errors.Is(err, projection.ErrNotVisible):
		return fmt.Errorf("the deadline elapsed with the change neither applied nor named: %+v", seen)
	default:
		return err
	}
}

// The projection, and the three fields the park costs. ParkSequence needs the
// queue AND the InUnit tier AND a Destination this framework can resolve: the
// park write, the read model's writes and the advance are one transaction or the
// queue orders nothing.
func followingSpec(store *eventpg.Store, checkpoints *eventpg.Checkpoints, source crud.Source, model *readModel, queue *parkTable) projection.Spec {
	router := projection.NewRouter(projection.SkipForeign)
	projection.On(router, Place, model.placed)
	projection.On(router, Ship, model.shipped)

	return projection.Spec{
		Name:        "waitorders",
		Log:         event.ReadOnly(store),
		Checkpoints: checkpoints,
		Handler:     router,
		Idle:        20 * time.Millisecond,

		Advance: projection.InUnit,
		Unit: func(ctx context.Context, work func(context.Context) error) error {
			return crud.InNewTx(ctx, source, work)
		},
		Destination: source,

		OnPermanentFailure: projection.ParkSequence,
		Park:               queue,

		// The read model's own refusal is not one of the four history classes, so
		// the default classifier would call it retryable and this projection would
		// retry a poison carrier for ever. A Classifier is how an application says
		// which of its own failures never get better.
		Classifier: func(err error) projection.Verdict {
			if errors.Is(err, errNoSuchCarrier) {
				return projection.Permanent
			}
			return projection.Classify(err)
		},
	}
}

// The read model, and the poison it refuses for ever. A permanent verdict is
// what parks a sequence; a retryable one is retried without limit.
type readModel struct{ db *sql.DB }

func (this *readModel) placed(ctx context.Context, fact Placed, envelope event.Envelope) error {
	return this.write(ctx, envelope, "placed by "+fact.Customer)
}

func (this *readModel) shipped(ctx context.Context, fact Shipped, envelope event.Envelope) error {
	if fact.Carrier == "poison" {
		return fmt.Errorf("%w: %q", errNoSuchCarrier, fact.Carrier)
	}
	return this.write(ctx, envelope, "shipped by "+fact.Carrier)
}

// The handler writes through the transaction the unit bound, which is the one
// the advance rides in. crud.ExecutorFor is how a handler reaches it.
func (this *readModel) write(ctx context.Context, envelope event.Envelope, what string) error {
	executor, bound := crud.ExecutorFor(ctx, this.db)
	if !bound {
		return fmt.Errorf("the handler was called outside the unit that carries the advance")
	}
	_, err := executor.Exec(ctx,
		`INSERT INTO example_wait_orders (stream_key, version, note) VALUES ($1, $2, $3)
		 ON CONFLICT (stream_key, version) DO UPDATE SET note = EXCLUDED.note`,
		string(envelope.Stream.Key), int64(envelope.Version), what)
	return err
}

// The park, and it is the application's table. The framework writes to it and
// counts it; it removes nothing.
//
// WHERE EACH METHOD RUNS IS PART OF THE CONTRACT. Park and Holds are called
// inside the caller's unit of work by the loop — and Holds is ALSO called
// OUTSIDE one, by a wait, which must answer the committed state there.
// Sequences runs outside a unit, before the pass opens one, so a healthy
// projection does not open a transaction per pass to be told the queue is empty.
// Holes is a cutover's question and neither the loop nor a wait asks it.
type parkTable struct{ db *sql.DB }

func (this *parkTable) Sequences(ctx context.Context, of projection.Identity) (uint64, error) {
	var counted uint64
	err := this.db.QueryRowContext(ctx,
		`SELECT count(DISTINCT sequence) FROM example_wait_park WHERE identity = $1 AND applied = false`,
		of.String()).Scan(&counted)
	return counted, err
}

// On the pool rather than on the ambient transaction, because a wait has no
// unit and the committed answer is the one both callers need.
func (this *parkTable) Holds(ctx context.Context, of projection.Identity, sequence string) (bool, error) {
	var held bool
	err := this.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM example_wait_park WHERE identity = $1 AND sequence = $2 AND applied = false)`,
		of.String(), sequence).Scan(&held)
	return held, err
}

// Inside the caller's unit: this row, the read model's rows and the advance are
// one commit or none of them happened.
func (this *parkTable) Park(ctx context.Context, letter projection.Letter) error {
	executor, bound := crud.ExecutorFor(ctx, this.db)
	if !bound {
		return fmt.Errorf("a letter was parked outside the unit that carries the advance")
	}
	var cause string
	if letter.Cause != nil {
		cause = letter.Cause.Error()
	}
	_, err := executor.Exec(ctx,
		`INSERT INTO example_wait_park (identity, sequencer, sequence, stream_key, version, cause, attempt)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		letter.Identity.String(), letter.Sequencer, letter.Sequence,
		string(letter.Envelope.Stream.Key), int64(letter.Envelope.Version), cause, letter.Attempt)
	return err
}

// Two counts over the rows rather than a column, so it cannot drift from what it
// summarises: the letters queued now, plus the letters an operator evicted
// without applying.
func (this *parkTable) Holes(ctx context.Context, of projection.Identity) (uint64, error) {
	var counted uint64
	err := this.db.QueryRowContext(ctx,
		`SELECT count(*) FROM example_wait_park WHERE identity = $1 AND (applied = false OR evicted = true)`,
		of.String()).Scan(&counted)
	return counted, err
}

func (this *parkTable) holding(ctx context.Context) ([]string, int) {
	rows, err := this.db.QueryContext(ctx,
		`SELECT sequence FROM example_wait_park WHERE applied = false ORDER BY id`)
	if err != nil {
		return nil, 0
	}
	defer func() { _ = rows.Close() }()
	var held []string
	for rows.Next() {
		var sequence string
		if err := rows.Scan(&sequence); err != nil {
			return held, len(held)
		}
		held = append(held, sequence)
	}
	return held, len(held)
}

// The example creates its own tables so it runs against an empty database. A
// real deployment owns these two and this framework ships neither.
func bootstrap(ctx context.Context, db *sql.DB) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS example_wait_orders (
			stream_key text NOT NULL,
			version    bigint NOT NULL,
			note       text NOT NULL,
			PRIMARY KEY (stream_key, version)
		)`,
		`CREATE TABLE IF NOT EXISTS example_wait_park (
			id        bigserial PRIMARY KEY,
			identity  text NOT NULL,
			sequencer text NOT NULL,
			sequence  text NOT NULL,
			stream_key text NOT NULL,
			version   bigint NOT NULL,
			cause     text NOT NULL DEFAULT '',
			attempt   int NOT NULL DEFAULT 0,
			applied   boolean NOT NULL DEFAULT false,
			evicted   boolean NOT NULL DEFAULT false
		)`,
		`CREATE INDEX IF NOT EXISTS example_wait_park_open ON example_wait_park (identity, sequence) WHERE applied = false`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
