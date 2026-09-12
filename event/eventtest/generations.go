package eventtest

import (
	"context"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/projection"
)

// What closes the window between the read of the ownership row and the commit
// of the unit that read it. [[D-126]] leaves the isolation level to the caller,
// so an implementation states which of the two it has rather than being guessed
// at: at READ COMMITTED a plain read of the row and a concurrent Activate of it
// do not conflict, both commit, and a generation the row no longer names commits
// the effect it staged anyway.
//
// The zero value is neither, and that is the point of it: a field nobody filled
// would turn off the one section this harness exists for.
type Closure uint8

const (
	ClosureUnstated Closure = iota

	// Active is `SELECT active … FOR SHARE` or `FOR KEY SHARE`, so a cutover's
	// UPDATE waits behind every unit that read the row. This is the spelling the
	// harness can measure.
	LockingRead

	// Units run SERIALIZABLE and the abort closes it. The harness reports that
	// section not certified rather than passed: the abort comes from the cutover
	// reading the checkpoint rows the staging unit writes, and a harness holding
	// two methods and no checkpoints writes none of them.
	SerializableUnit
)

func (this Closure) String() string {
	switch this {
	case LockingRead:
		return "a locking read"
	case SerializableUnit:
		return "a serializable unit"
	}
	return "unstated"
}

// The factory constructs and the harness never does. The ownership row is one
// row per projection across all of its generations, in the application's own
// table, and only its author knows how to open one.
type GenerationsFactory struct {
	// Called one or more times per section, and the harness holds several of its
	// values live at once. A value this builds must not destroy or reset what an
	// earlier one wrote in the same section. Whatever a value holds open is closed
	// by the factory, through t.Cleanup.
	New func(t *testing.T) projection.Generations

	// Begins a unit of this implementation's own and answers the context that
	// carries it. Required, and called twice at once: both methods run inside the
	// caller's unit, and what the locking section measures is one unit waiting
	// behind another. A factory whose units come from a pool of one blocks until
	// the section window expires.
	Begin func(t *testing.T, ctx context.Context, g projection.Generations) (context.Context, Tx)

	// Which of the two spellings this implementation has. Stating neither fails
	// the run before any section starts, because a capability nobody stated is not
	// one nobody has.
	Closes Closure

	// The window every section runs under, zero meaning the default.
	Window time.Duration
}

const (
	buildsNoGenerations  = "eventtest: this factory builds no ownership row, so there is nothing to certify"
	answersNoGenerations = "eventtest: this factory answered no ownership row"
	opensNoOwnershipUnit = "eventtest: this factory begins no unit of work, and both methods of this contract run inside the caller's — there is no section here that could run without one"
	statesNoClosure      = "eventtest: this factory states nothing about what closes the window between the read of the ownership row and the commit of the unit that read it, and a field nobody filled would turn off the one section this harness exists for: state eventtest.LockingRead or eventtest.SerializableUnit"
)

// RunGenerations certifies a projection.Generations against the obligation the
// interface cannot enforce: Active is a locking read, so the generation that
// staged an effect is the generation that owned the row for the whole of its
// unit.
func RunGenerations(t *testing.T, factory GenerationsFactory) {
	report(t, sweep(t, generationsInventory(), owning(t, factory), telling))
}

func owning(t *testing.T, factory GenerationsFactory) func(*testing.T, string, string) *generations {
	admitGenerations(t, factory)
	run := runIdentity(t, event.MaxNameBytes, len(generationsName("", "s99-", "llllllll")))
	return func(t *testing.T, name, mark string) *generations {
		return &generations{
			recording: recording{t: t, name: name, mark: mark, window: factory.Window},
			factory:   factory,
			run:       run,
		}
	}
}

type generations struct {
	recording
	factory GenerationsFactory
	run     string
}

func admitGenerations(t *testing.T, factory GenerationsFactory) {
	if factory.New == nil {
		t.Fatal(buildsNoGenerations)
	}
	if factory.Begin == nil {
		t.Fatal(opensNoOwnershipUnit)
	}
	if factory.Closes == ClosureUnstated {
		t.Fatal(statesNoClosure)
	}
	if factory.New(t) == nil {
		t.Fatal(answersNoGenerations)
	}
}

func generationsName(run, mark, label string) string {
	return "eventtest.rows." + run + "." + mark + label
}

func (this *generations) rows() projection.Generations {
	found := this.factory.New(this.t)
	if found == nil {
		this.refuse("the factory answered no ownership row")
	}
	return found
}

func (this *generations) named(label string) string {
	if len(label) > widestLabel {
		this.refuse("this section named a projection %q, where the harness reserves %d bytes of the name for a label", label, widestLabel)
	}
	return generationsName(this.run, this.mark, label)
}

func (this *generations) begin(ctx context.Context, held projection.Generations) (context.Context, Tx) {
	inside, tx := this.factory.Begin(this.t, ctx, held)
	if tx == nil {
		this.refuse("the factory began no unit of work")
	}
	if inside == nil {
		this.refuse("the factory answered no context carrying the unit it began")
	}
	this.t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return inside, tx
}

func (this *generations) commit(ctx context.Context, tx Tx) {
	if err := tx.Commit(ctx); err != nil {
		this.refuse("committing the unit this section was inside answered %v", err)
	}
}

func (this *generations) rollback(ctx context.Context, tx Tx) {
	if err := tx.Rollback(ctx); err != nil {
		this.refuse("rolling back the unit this section was inside answered %v", err)
	}
}

func (this *generations) active(ctx context.Context, held projection.Generations, name string) projection.Generation {
	found, err := held.Active(ctx, name)
	if err != nil {
		this.refuse("reading the ownership row of %q answered %v", name, err)
	}
	return found
}

func (this *generations) activate(ctx context.Context, held projection.Generations, name string, from, to projection.Generation) {
	if err := held.Activate(ctx, name, from, to); err != nil {
		this.refuse("moving the ownership row of %q from %d to %d answered %v", name, from, to, err)
	}
}

// A row stood up and committed, which every section but the first needs before
// it can measure anything about moving one.
func (this *generations) standing(ctx context.Context, held projection.Generations, name string, at projection.Generation) {
	inside, tx := this.begin(ctx, held)
	this.activate(inside, held, name, projection.Ungenerated, at)
	this.commit(inside, tx)
}

func (this *generations) reads(ctx context.Context, held projection.Generations, name string) projection.Generation {
	inside, tx := this.begin(ctx, held)
	found := this.active(inside, held, name)
	this.rollback(inside, tx)
	return found
}
