// Four runners over one log, and the topology change that takes one of them to
// two without a window in which ordering breaks.
//
// A partition is a MASK and not a modulus. The set is declared once, as a
// `projection.Cover`, and every runner is built from `cover.Partitions()` —
// which is the one spelling that makes the two silent failures unconstructible:
// a forgotten member, whose share of the log is never delivered to anything
// while every runner reports healthy, and two members that overlap, which are
// two checkpoint rows with no fence between them applying every shared key twice
// and out of order.
//
// The sequence key is the application's, through `SequenceBy`: two envelopes
// with one key are applied in the log's order and two with different keys are
// not ordered with respect to each other. It must be total, pure and stable for
// the life of the log — the key an envelope produced in one release is the key
// it must produce in every later one — and none of the three is checked at run
// time, because a key that moved is applied twice or not at all.
//
// Then the handoff: one partition becomes two, both starting at the parent's
// exact cursor, and the parent's row is retired. It is one transaction of the
// caller's and there is no inverse — a merge would have to order two cursors,
// which is not a question a cursor answers.
//
// It needs no database: the log and the checkpoint rows are `eventmemory`, which
// supports transactions and declines persistence.
//
//	cd _examples && GOWORK=off go run ./event-partitions
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
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

	if err := fill(ctx, store, "first", 12); err != nil {
		return err
	}

	// THE SET IS DECLARED ONCE. NewCover is the only thing that checks it: two
	// members overlap when (idA ^ idB) & min(maskA, maskB) is zero, and a set with
	// no overlap covers the space when the members' shares sum to MaxPartitions.
	cover, err := newCover(0, 1, 2, 3)
	if err != nil {
		return err
	}
	fmt.Printf("declared a cover of %d partitions: %v\n", cover.Count(), cover.Partitions())

	model := &readModel{}
	running, err := start(store, checkpoints, model, cover.Partitions()...)
	if err != nil {
		return err
	}
	if err := drain(ctx, running); err != nil {
		return err
	}

	for _, part := range cover.Partitions() {
		of, err := projection.NewIdentity("orders", projection.Ungenerated, part)
		if err != nil {
			return err
		}
		held, err := checkpoints.Load(ctx, of.String())
		if err != nil {
			return err
		}
		fmt.Printf("%-12s applied=%-3d highest=%-3d keys=%v\n", of, held.Progress.Applied, held.Progress.Highest, model.keysOf(of))
	}
	if out := model.outOfOrder(); len(out) != 0 {
		return fmt.Errorf("these keys were applied out of the log's order: %v", out)
	}
	fmt.Println("every key's events were applied in the log's order, and no key reached two partitions")

	// THE HANDOFF. The parent is drained and stopped first: a split committed
	// under a running parent takes its row away, and the parent's next save finds
	// no row at its advance and HALTS rather than creating a fresh one beside its
	// children.
	stop(ctx, running)
	parent, err := projection.NewIdentity("orders", projection.Ungenerated, cover.Partitions()[3])
	if err != nil {
		return err
	}
	before, err := checkpoints.Load(ctx, parent.String())
	if err != nil {
		return err
	}
	lower, higher, err := projection.Split(ctx, projection.SplitSpec{
		Checkpoints: checkpoints,
		Identity:    parent,
		Unit:        unitOver(checkpoints),
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s became %s and %s, both at the parent's exact cursor\n", parent, lower, higher)

	for _, child := range []projection.Identity{lower, higher} {
		held, err := checkpoints.Load(ctx, child.String())
		if err != nil {
			return err
		}
		if string(held.Cursor) != string(before.Cursor) {
			return fmt.Errorf("the child %s carries a cursor the parent never held", child)
		}
		fmt.Printf("%-12s advance=%-3d applied=%-3d cursor is the parent's, byte for byte\n", child, held.Advance, held.Progress.Applied)
	}

	// The retired share cannot be started again: redeploying the release that ran
	// before the split would otherwise walk the whole log into the read model the
	// children are filling, with every row reporting a healthy watermark.
	if _, _, err := projection.Split(ctx, projection.SplitSpec{
		Checkpoints: checkpoints, Identity: parent, Unit: unitOver(checkpoints),
	}); !errors.Is(err, projection.ErrTopology) {
		return fmt.Errorf("a second split of a retired parent answered %v", err)
	}
	fmt.Println("a second split of the retired parent is refused: there is nothing left to hand down")

	// The two children take over the parent's whole key space and nothing else
	// moves, which is what a mask buys and a modulus does not.
	if err := fill(ctx, store, "second", 12); err != nil {
		return err
	}
	next := append(cover.Partitions()[:3:3], lower.Partition(), higher.Partition())
	after, err := projection.NewCover(next...)
	if err != nil {
		return err
	}
	children, err := start(store, checkpoints, model, after.Partitions()...)
	if err != nil {
		return err
	}
	if err := drain(ctx, children); err != nil {
		return err
	}
	stop(ctx, children)

	if out := model.outOfOrder(); len(out) != 0 {
		return fmt.Errorf("these keys were applied out of the log's order after the split: %v", out)
	}
	fmt.Printf("the %d-partition cover drained the rest of the log and every key is still in order\n", after.Count())
	return nil
}

func newCover(ids ...uint32) (projection.Cover, error) {
	parts := make([]projection.Partition, 0, len(ids))
	for _, id := range ids {
		part, err := projection.NewPartition(id, uint32(len(ids))-1)
		if err != nil {
			return projection.Cover{}, err
		}
		parts = append(parts, part)
	}
	return projection.NewCover(parts...)
}

// One runner per member, built from the set and from nothing else.
func start(store *eventmemory.Store, checkpoints *eventmemory.Checkpoints,
	model *readModel, parts ...projection.Partition,
) ([]*projection.Projection, error) {
	router := projection.NewRouter(projection.SkipForeign)
	projection.On(router, Place, model.placed)
	projection.On(router, Pay, model.paid)

	held := make([]*projection.Projection, 0, len(parts))
	for _, part := range parts {
		running, err := projection.New(projection.Spec{
			Name:        "orders",
			Log:         event.ReadOnly(store),
			Checkpoints: checkpoints,
			Handler:     router,

			// The application's own key, by the read model it conflicts on.
			Sequence:  projection.SequenceBy("by-order", func(envelope event.Envelope) string { return string(envelope.Stream.Key) }),
			Partition: part,

			Idle: 5 * time.Millisecond,
		})
		if err != nil {
			return nil, err
		}
		held = append(held, running)
	}
	return held, nil
}

// Run blocks on the caller's own goroutine and this package starts none, so a
// host is the thing that decides how many goroutines four partitions cost.
func drain(ctx context.Context, running []*projection.Projection) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for _, held := range running {
		go func() { _ = held.Run(ctx) }()
	}
	for {
		following := 0
		for _, held := range running {
			switch held.State().Phase {
			case projection.PhaseFollowing:
				following++
			case projection.PhaseHalted:
				return fmt.Errorf("%s halted: %w", held.Name(), held.State().Err)
			}
		}
		if following == len(running) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func stop(ctx context.Context, running []*projection.Projection) {
	for _, held := range running {
		_ = held.Drain(ctx)
	}
}

// A unit of work over the checkpoint store's own transaction, which is what
// crud.InNewTx is in a wiring with a database. The framework opens none: the
// four writes a split makes are one handoff or a topology nobody can read.
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

// The read model four runners share, and the one thing it records is what makes
// the run checkable: which partition applied each key, and in what order.
type readModel struct {
	mutex   sync.Mutex
	applied map[string][]string
	owners  map[string]string
}

func (this *readModel) placed(_ context.Context, _ Placed, envelope event.Envelope) error {
	return this.record(envelope, "placed")
}

func (this *readModel) paid(_ context.Context, _ Paid, envelope event.Envelope) error {
	return this.record(envelope, "paid")
}

// The partition that applied it is read off Batch.Identity in a real handler; an
// applier is handed the envelope, so this one keys on the stream and checks the
// ownership at the end instead.
func (this *readModel) record(envelope event.Envelope, what string) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.applied == nil {
		this.applied, this.owners = map[string][]string{}, map[string]string{}
	}
	key := string(envelope.Stream.Key)
	this.applied[key] = append(this.applied[key], what)
	return nil
}

func (this *readModel) keysOf(of projection.Identity) int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held := 0
	for key := range this.applied {
		if of.Partition().Matches(key) {
			held++
		}
	}
	return held
}

// A key whose `paid` landed before its `placed` is the corruption a partition
// set exists to prevent, and it is what a `hash % N -> hash % (N+1)`
// re-partitioning produces on roughly N/(N+1) of all keys.
func (this *readModel) outOfOrder() []string {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	var held []string
	for key, applied := range this.applied {
		if len(applied) >= 2 && applied[0] == "paid" {
			held = append(held, key)
		}
	}
	sort.Strings(held)
	return held
}
