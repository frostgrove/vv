// The Go of docs/usage-guides/event-sourcing.md, compiled. Every fenced Go block
// on that page is a contiguous run of lines of this package, and
// TestEveryGoFenceInTheEventSourcingGuideIsCompiled (./scripts) compares the two.
// It is not a runnable program and opens no connection: what it holds is that the
// adoption route a consumer follows type-checks against the library it describes,
// which the doc checks that read backticked names and file.go:Symbol citations are
// structurally unable to see.
package eventguide

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventpg"
	"github.com/frostgrove/vv/event/projection"
	"github.com/frostgrove/vv/event/receipt"
	"github.com/frostgrove/vv/runtime"
)

// Part II — declare the aggregate

type Order struct {
	Status Status
	Total  int64
}

type OrderID struct{ Tenant, Number string }

type OrderPlaced struct{ Total int64 }
type OrderShipped struct{ Carrier string }

var Orders = event.Define[Order, OrderID]("order", func(id OrderID) event.Key {
	return event.Compose(id.Tenant, id.Number)
})

var (
	Placed  = event.Declare(Orders, "order.placed", event.From(event.JSON[OrderPlaced]()), place)
	Shipped = event.Declare(Orders, "order.shipped", event.From(event.JSON[OrderShipped]()), ship)
)

func place(state Order, fact OrderPlaced) Order {
	state.Status, state.Total = Placed_, fact.Total
	return state
}

func ship(state Order, fact OrderShipped) Order {
	state.Status = Shipped_
	return state
}

// What the page names without declaring, because a guide's reader already has it.

type Status int

const (
	Draft Status = iota
	Placed_
	Shipped_
)

type Command struct{ Total int64 }

var ErrNotDraft = errors.New("the order is not a draft")

func decide(state Order, command Command) []event.Change[Order] { return nil }

type readModelOfOrders struct{}

func (this *readModelOfOrders) placed(ctx context.Context, fact OrderPlaced, envelope event.Envelope) error {
	return nil
}

func (this *readModelOfOrders) shipped(ctx context.Context, fact OrderShipped, envelope event.Envelope) error {
	return nil
}

func serveFresh() error                      { return nil }
func serveParked() error                     { return nil }
func serveStale(behind event.Position) error { return nil }
func answered(held receipt.Receipt)          {}

// Part I — what you get

func decideAndAppend(ctx context.Context, orders *event.Repo[Order, OrderID], id OrderID, total int64) error {
	state, at, err := orders.Load(ctx, id) // folded from the whole history
	if err != nil {
		return err
	}
	if state.Status != Draft {
		return ErrNotDraft
	}
	_, commit, err := orders.Append(ctx, at, Placed.New(id, OrderPlaced{Total: total}))
	_ = commit
	return err
}

func superviseTheReadModel(ctx context.Context, following *projection.Projection) error {
	supervisor, err := runtime.Auto(following) // projection.New(...) built below
	if err != nil {
		return err
	}
	return supervisor.Start(ctx)
}

func readItBack(ctx context.Context, store event.Store, commit event.Commit, waiting projection.WaitSpec) error {
	mark, err := waiting.Committed(ctx, store, commit) // after the transaction committed
	waiting.Until = mark
	_, err = projection.Wait(ctx, waiting)
	return err
}

func askWhatHappened(ctx context.Context, ledger receipt.Ledger, store event.Store, key receipt.Key) error {
	resolution, err := receipt.Resolve(ctx, receipt.ResolveSpec{
		Ledger: ledger, Store: store, Key: key,
	})
	switch resolution.Standing {
	case receipt.Found: // here is the range it wrote
	case receipt.Unresolved: // still in flight — do NOT re-issue it
	}
	return err
}

// Part III — bind a store

func bind(dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	source := crudsql.Postgres(db)

	store, err := eventpg.New(eventpg.Spec{DB: db, Source: source, Schema: eventpg.Schema{Name: "events"}})
	if err != nil {
		return err
	}
	orders, err := event.Bind(event.Open(store), Orders)
	_ = orders
	return err
}

func write(ctx context.Context, source crud.Source, orders *event.Repo[Order, OrderID], id OrderID, command Command) error {
	var commit event.Commit
	var err error

	err = crud.InNewTx(ctx, source, func(ctx context.Context) error {
		state, at, err := orders.Load(ctx, id)
		if err != nil {
			return err
		}
		_, commit, err = orders.Append(ctx, at, decide(state, command)...)
		return err
	})
	_ = commit
	return err
}

// Part IV — follow the log into a read model

func follow(ctx context.Context, store event.Store, checkpoints event.Checkpoints, source crud.Source) error {
	readModel := &readModelOfOrders{}

	router := projection.NewRouter(projection.SkipForeign)
	projection.On(router, Placed, readModel.placed)
	projection.On(router, Shipped, readModel.shipped)

	spec := projection.Spec{
		Name:        "orders",
		Log:         event.ReadOnly(store),
		Checkpoints: checkpoints,
		Handler:     router,
		Advance:     projection.InUnit,
		Unit: func(ctx context.Context, work func(context.Context) error) error {
			return crud.InNewTx(ctx, source, work)
		},
		Destination: source,
	}

	following, err := projection.New(spec)
	if err != nil {
		return err
	}

	supervisor, err := runtime.Auto(following) // or runtime.NewSupervisor(runtime.Spec{Runners: ...})
	if err != nil {
		return err
	}
	return supervisor.Start(ctx)
}

func park(spec *projection.Spec, parkTable projection.Park) {
	spec.Park = parkTable // your own table, behind the interface
	spec.OnPermanentFailure = projection.ParkSequence
}

// Part V — read your own change back, without sleeping

func waitOf(spec projection.Spec) error {
	whole, err := projection.NewCover(projection.Whole())
	if err != nil {
		return err
	}
	waiting, err := projection.WaitOf(spec, whole)
	_ = waiting
	return err
}

func serve(ctx context.Context, store event.Store, commit event.Commit, waiting projection.WaitSpec) error {
	mark, err := waiting.Committed(ctx, store, commit)
	if err != nil {
		return err
	}
	waiting.Until = mark

	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	vis, err := projection.Wait(ctx, waiting)
	switch {
	case err == nil:
		return serveFresh()
	case errors.Is(err, projection.ErrParked):
		return serveParked() // a redrive is the fix; waiting is not
	case errors.Is(err, projection.ErrNotVisible):
		return serveStale(vis.Behind) // your branch, not a flag
	default:
		return err
	}
}

// Part VI — answer "did my command happen?"

type request struct {
	Header interface{ Get(string) string }
}

func claim(ctx context.Context, source crud.Source, ledger receipt.Ledger, store event.Store, orders *event.Repo[Order, OrderID], id OrderID, command Command, request request) error {
	var answer receipt.Receipt

	key, err := receipt.NewKey(request.Header.Get("Idempotency-Key"))
	if err != nil {
		return err
	}

	err = crud.InNewTx(ctx, source, func(ctx context.Context) error {
		state, at, err := orders.Load(ctx, id)
		if err != nil {
			return err
		}
		changes := decide(state, command)

		print, err := orders.Digest(at, changes...)
		if err != nil {
			return err
		}
		fingerprint, err := receipt.NewFingerprint(print)
		if err != nil {
			return err
		}

		held, err := receipt.Once(ctx, receipt.ClaimSpec{
			Ledger: ledger, Store: store, Key: key,
			Fingerprint: fingerprint, Stream: at.Stream(),
		}, func(ctx context.Context) (event.Commit, error) {
			_, commit, err := orders.Append(ctx, at, changes...)
			return commit, err
		})
		if err != nil {
			return err // a collision, or a row nobody resolved
		}
		answer = held.Receipt() // the range this attempt wrote, or the first one's
		return nil
	})
	answered(answer)
	return err
}

// Part VII — read history at a version

func history(ctx context.Context, orders *event.Repo[Order, OrderID], id OrderID, disputed event.Version) (Order, error) {
	before, err := orders.StateAt(ctx, id, disputed-1)
	return before, err
}
