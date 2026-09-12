// A read model rebuilt beside the one that is serving, and the switch that
// points reads at it without a moment in which either is half-live.
//
// A generation is a number in the recorded name and nothing else: `orders` and
// `orders@2` are two checkpoint rows, two destinations and two runners over one
// log. There is no API by which the arriving one could read, pause or reset the
// live one's checkpoint, which is the whole of "a rebuild is two projections
// with nothing between them".
//
// The evidence is derived rather than supplied. `CutoverSpec` HAS NO BARRIER
// FIELD — a barrier a caller can invent is not evidence, and the zero value of
// one would admit a generation that has delivered nothing — so `Cutover`
// observes its own from the retiring generation's rows, inside the caller's
// unit, in the same breath as the write. A rollback is the same call with `From`
// and `To` exchanged.
//
// And the effect gate. The live generation stages side effects; the rebuild is a
// spec with `Effects` nil, so there is nothing to withhold from it because there
// is nothing to hold. After the cutover the retiring generation reads the
// ownership row inside the transaction that commits its own advance, finds the
// arriving generation's number, and stages nothing — while its handler keeps
// applying and its checkpoint keeps advancing.
//
// THE OWNERSHIP READ CARRIES AN OBLIGATION THIS FRAMEWORK CANNOT ENFORCE: it
// must be a locking read — `SELECT active … FOR SHARE` against a database — or
// run in a SERIALIZABLE unit, because at READ COMMITTED a plain read of that row
// and a concurrent `Activate` of it do not conflict and both commit. The
// implementation below takes the lock, and says where.
//
// It needs no database: the log and the checkpoint rows are `eventmemory`.
//
//	cd _examples && GOWORK=off go run ./event-generations
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

type Order struct{ Paid bool }

type Placed struct{ Customer string }

type Paid struct{ Amount int64 }

var Orders = event.Define[Order, string]("orders", func(id string) event.Key { return event.Key(id) })

var (
	Place = event.Declare(Orders, "orders.placed", event.From(event.JSON[Placed]()), func(state Order, _ Placed) Order { return state })
	Pay   = event.Declare(Orders, "orders.paid", event.From(event.JSON[Paid]()), func(state Order, _ Paid) Order {
		state.Paid = true
		return state
	})
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	log, err := eventmemory.NewLog(eventmemory.LogSpec{})
	if err != nil {
		return err
	}
	store, err := eventmemory.New(eventmemory.Spec{Log: log})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	checkpoints, err := eventmemory.NewCheckpoints(eventmemory.CheckpointSpec{Log: log})
	if err != nil {
		return err
	}
	defer func() { _ = checkpoints.Close() }()

	if err := fill(ctx, store, "first", 8); err != nil {
		return err
	}

	// The ownership row a read path resolves its tables through. ONE ROW PER
	// PROJECTION across all of its generations: it names the winner, so a row per
	// generation could not.
	rows := &ownership{active: map[string]projection.Generation{"orders": 1}}
	mail := &outbox{}

	// The live generation. Effects is set and Generations is beside it, which is
	// required at any generation other than Ungenerated: that pair is what admits
	// two senders.
	live := &readModel{}
	serving, err := projection.New(generational(store, checkpoints, 1, live, mail, rows, 0))
	if err != nil {
		return err
	}
	if err := drain(ctx, serving); err != nil {
		return err
	}

	// The barrier the arriving generation must clear, and the number an operator
	// writes into its Spec.EffectsAfter. It is a Position and never a Cursor:
	// nothing resumes from it.
	whole, err := projection.NewCover(projection.Whole())
	if err != nil {
		return err
	}
	one, err := projection.NewIdentity("orders", 1, projection.Whole())
	if err != nil {
		return err
	}
	barrier, err := projection.Observe(ctx, checkpoints, one, whole)
	if err != nil {
		return err
	}
	fmt.Printf("generation 1 is serving: %d rows, %d effects staged, barrier at %d\n", live.count(), mail.count(), barrier.At)

	// The arriving generation. Effects is NIL — that is the whole of "a rebuild
	// does not get the effect-dispatch capability" — and it replays the log from
	// the origin into its own destination.
	arriving := &readModel{}
	rebuilding, err := projection.New(generational(store, checkpoints, 2, arriving, nil, nil, 0))
	if err != nil {
		return err
	}
	if err := drain(ctx, rebuilding); err != nil {
		return err
	}

	two, err := projection.NewIdentity("orders", 2, projection.Whole())
	if err != nil {
		return err
	}
	readiness, err := projection.Reached(ctx, checkpoints, nil, barrier, two, whole)
	if err != nil {
		return err
	}
	fmt.Printf("generation 2 rebuilt beside it: %d rows, reached=%v behind=%d holes=%d, and it staged nothing\n",
		arriving.count(), readiness.Reached, readiness.Behind, readiness.Holes)
	if mail.count() != live.count() {
		return fmt.Errorf("the rebuild staged %d effects", mail.count()-live.count())
	}
	if arriving.count() != live.count() {
		return fmt.Errorf("the two generations hold %d and %d rows", arriving.count(), live.count())
	}

	// DRAIN OR STOP THE RETIRING GENERATION BEFORE, OR AS, THE SWITCH COMMITS.
	// Cutover names a window it does not close: a retiring generation that is
	// still advancing goes past the barrier while the unit is open, and reads then
	// move backwards by exactly that much. Observing it twice is the whole of the
	// check.
	_ = serving.Drain(ctx)
	again, err := projection.Observe(ctx, checkpoints, one, whole)
	if err != nil {
		return err
	}
	if again.At != barrier.At {
		return fmt.Errorf("the retiring generation advanced from %d to %d between the two observations", barrier.At, again.At)
	}

	if err := projection.Cutover(ctx, projection.CutoverSpec{
		Checkpoints: checkpoints,
		Generations: rows,
		Projection:  "orders",
		From:        1,
		To:          2,
		Retiring:    whole,
		Arriving:    whole,
		Unit:        unitOver(checkpoints),
	}); err != nil {
		return err
	}
	fmt.Printf("the read target moved: Generations.Active answers %d, in one fenced write\n", rows.at("orders"))

	// Two operators cutting over at once leave one winner and one refusal: the
	// write is fenced and is one statement, so a read followed by a write is the
	// one spelling it may not have.
	if err := projection.Cutover(ctx, projection.CutoverSpec{
		Checkpoints: checkpoints, Generations: rows, Projection: "orders",
		From: 1, To: 2, Retiring: whole, Arriving: whole, Unit: unitOver(checkpoints),
	}); !errors.Is(err, event.ErrConflict) {
		return fmt.Errorf("a second cutover from a generation the row no longer holds answered %v", err)
	}
	fmt.Println("a second cutover from 1 is refused on the fence: the row holds 2 exactly once")

	// The effect gate, measured. Generation 1 is still running against a live
	// read model; its next pass reads the ownership row inside the transaction
	// that commits its advance, finds 2, and stages NOTHING — while its handler
	// keeps applying.
	staged, applied := mail.count(), live.count()
	if err := fill(ctx, store, "second", 4); err != nil {
		return err
	}
	retiring, err := projection.New(generational(store, checkpoints, 1, live, mail, rows, 0))
	if err != nil {
		return err
	}
	if err := drain(ctx, retiring); err != nil {
		return err
	}
	_ = retiring.Drain(ctx)
	fmt.Printf("the retired generation applied %d more rows and staged %d more effects\n", live.count()-applied, mail.count()-staged)
	if mail.count() != staged {
		return fmt.Errorf("the retired generation staged %d effects after the cutover", mail.count()-staged)
	}

	// The rollback is the same call exchanged, and it re-checks the readiness in
	// the other direction rather than trusting that generation 1 is still there.
	if err := projection.Cutover(ctx, projection.CutoverSpec{
		Checkpoints: checkpoints, Generations: rows, Projection: "orders",
		From: 2, To: 1, Retiring: whole, Arriving: whole, Unit: unitOver(checkpoints),
	}); err != nil {
		return err
	}
	fmt.Printf("the rollback is the same call with From and To exchanged: the row holds %d again\n", rows.at("orders"))
	return nil
}

// One generation's spec. A rebuild differs from the live one in exactly two
// fields — Generation, and an Effects that is nil — and in the destination its
// handler was given.
func generational(store *eventmemory.Store, checkpoints *eventmemory.Checkpoints, generation projection.Generation,
	into *readModel, mail projection.Effects, rows projection.Generations, after event.Position,
) projection.Spec {
	router := projection.NewRouter(projection.SkipForeign)
	projection.On(router, Place, into.placed)
	projection.On(router, Pay, into.paid)
	spec := projection.Spec{
		Name:        "orders",
		Log:         event.ReadOnly(store),
		Checkpoints: checkpoints,
		Handler:     router,
		Generation:  generation,
		Idle:        5 * time.Millisecond,

		// The tier the capability constructs at, and no other: Stage is called
		// inside the transaction that commits the advance.
		Advance: projection.InUnit,
		Unit:    unitOver(checkpoints),

		// This read model is a map behind a mutex rather than a table, so nothing
		// can resolve it through crud — which is what Unchecked says out loud.
		// Against a database it would name the source the checkpoint rows live in.
		Destination: projection.Unchecked,
	}
	if mail != nil {
		spec.Effects, spec.Generations, spec.EffectsAfter = mail, rows, after
	}
	return spec
}

// The ownership row, and the two obligations the interface cannot enforce. In a
// database this is one row of one table; here it is a map behind a mutex, and
// the mutex is standing in for the `FOR SHARE` a SQL implementation must take —
// without it a cutover committing between a pass's read and its commit leaves
// two senders, at every isolation level.
type ownership struct {
	mutex  sync.Mutex
	active map[string]projection.Generation
}

// A projection with no row answers Ungenerated and a NIL ERROR, because that is
// the state of every deployment that has never cut over.
func (this *ownership) Active(_ context.Context, name string) (projection.Generation, error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.active[name], nil
}

// Fenced, and one statement: two operators cutting over at once would both read
// `from` and both write if this were a read followed by a write.
func (this *ownership) Activate(_ context.Context, name string, from, to projection.Generation) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.active[name] != from {
		return fmt.Errorf("%w: the row of %q holds %d and not %d", event.ErrConflict, name, this.active[name], from)
	}
	this.active[name] = to
	return nil
}

func (this *ownership) at(name string) projection.Generation {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.active[name]
}

// The sink, and what it must be: a DURABLE WRITE and nothing a rollback cannot
// take back. An HTTP call, a payment or a mail here is sent again on every
// rollback the delivery can take — a lost fence, a full park, a later envelope's
// permanent failure, a serialisation failure the caller's own Unit answers — so
// the dial-out belongs to whatever drains this.
type outbox struct {
	mutex  sync.Mutex
	staged []string
}

func (this *outbox) Stage(_ context.Context, effect projection.Effect) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	for _, envelope := range effect.Envelopes {
		this.staged = append(this.staged, fmt.Sprintf("%s/%s#%d", effect.Identity, envelope.Stream.Key, envelope.Version))
	}
	return nil
}

func (this *outbox) count() int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return len(this.staged)
}

type readModel struct {
	mutex sync.Mutex
	rows  []string
}

func (this *readModel) placed(_ context.Context, fact Placed, envelope event.Envelope) error {
	return this.write(fmt.Sprintf("%s placed by %s", envelope.Stream.Key, fact.Customer))
}

func (this *readModel) paid(_ context.Context, fact Paid, envelope event.Envelope) error {
	return this.write(fmt.Sprintf("%s paid %d", envelope.Stream.Key, fact.Amount))
}

func (this *readModel) write(row string) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.rows = append(this.rows, row)
	return nil
}

func (this *readModel) count() int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return len(this.rows)
}

// The unit of work the advance, the handler's writes and the stage all ride in.
// crud.InNewTx(ctx, source, work) is the one-line spelling against a database;
// the framework opens none of its own.
func unitOver(checkpoints *eventmemory.Checkpoints) func(context.Context, func(context.Context) error) error {
	return func(ctx context.Context, work func(context.Context) error) error {
		tx, err := checkpoints.Begin(ctx)
		if err != nil {
			return err
		}
		if err := work(eventmemory.WithTransaction(ctx, tx)); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		return tx.Commit(ctx)
	}
}

func drain(ctx context.Context, running *projection.Projection) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	go func() { _ = running.Run(ctx) }()
	for {
		switch state := running.State(); state.Phase {
		case projection.PhaseFollowing:
			return nil
		case projection.PhaseHalted:
			return fmt.Errorf("%s halted: %w", running.Name(), state.Err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func fill(ctx context.Context, store *eventmemory.Store, tag string, keys int) error {
	orders, err := event.Bind(event.Open(store), Orders)
	if err != nil {
		return err
	}
	for index := range keys {
		id := fmt.Sprintf("%s-%d", tag, index)
		_, at, err := orders.Load(ctx, id)
		if err != nil {
			return err
		}
		if _, _, err := orders.Append(ctx, at,
			Place.New(id, Placed{Customer: id}),
			Pay.New(id, Paid{Amount: 100}),
		); err != nil {
			return err
		}
	}
	return nil
}
