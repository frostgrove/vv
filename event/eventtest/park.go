package eventtest

import (
	"context"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/projection"
)

// The factory constructs and the harness never does. A park is the application's
// own queue and only its author knows how to open one.
//
// THE HARNESS WRITES LETTERS IT DOES NOT REMOVE. Nothing in this framework
// removes one and only a redrive does, so every letter a run parks is parked
// under an identity carrying that run's own name and left there.
type ParkFactory struct {
	// Called one or more times per section, and the harness holds several of its
	// values live at once. A value this builds must not destroy or reset what an
	// earlier one wrote in the same section. Whatever a value holds open is closed
	// by the factory, through t.Cleanup.
	New func(t *testing.T) projection.Park

	// Begins a unit of this queue's own and answers the context that carries it.
	// Required: Holds and Park run inside the caller's unit, and that is what
	// orders an eviction against the loop's own blocking test.
	Begin func(t *testing.T, ctx context.Context, p projection.Park) (context.Context, Tx)

	// The two bounds this implementation refuses at, zero meaning it declares
	// none. Sequences is the whole queue's and Letters is one sequence's, and the
	// section that drives them is reported not certified when neither is
	// declared — a bound nobody stated is not a bound nobody has.
	//
	// The harness writes what it takes to reach them, so a park configured at
	// Axon's 1024 is declared over a value configured smaller rather than driven
	// to a thousand letters in somebody's database.
	Sequences, Letters int

	// The window every section runs under, zero meaning the default.
	Window time.Duration
}

const (
	buildsNoPark    = "eventtest: this factory builds no park, so there is nothing to certify"
	answersNoPark   = "eventtest: this factory answered no park"
	opensNoParkUnit = "eventtest: this factory begins no unit of work, and Holds and Park run inside the caller's — there is no section here that could run without one"
)

// What the harness will write to reach a declared bound. A queue that refuses at
// a larger number than this is certified on the bound it declares over a value
// configured smaller, rather than having this many rows written into it.
const widestBound = 64

// RunPark certifies a projection.Park against the obligations the interface
// cannot enforce: which of the four methods needs the caller's unit, and what
// Holds answers when it is asked outside one — which is where a wait asks it.
func RunPark(t *testing.T, factory ParkFactory) {
	report(t, sweep(t, parkInventory(), parking(t, factory), telling))
}

func parking(t *testing.T, factory ParkFactory) func(*testing.T, string, string) *park {
	admitPark(t, factory)
	run := runIdentity(t, event.MaxNameBytes, len(parkName("", "s99-", "llllllll"))+len("@4294967295"))
	return func(t *testing.T, name, mark string) *park {
		return &park{
			recording: recording{t: t, name: name, mark: mark, window: factory.Window},
			factory:   factory,
			run:       run,
		}
	}
}

type park struct {
	recording
	factory ParkFactory
	run     string
}

func admitPark(t *testing.T, factory ParkFactory) {
	if factory.New == nil {
		t.Fatal(buildsNoPark)
	}
	if factory.Begin == nil {
		t.Fatal(opensNoParkUnit)
	}
	if factory.New(t) == nil {
		t.Fatal(answersNoPark)
	}
}

func parkName(run, mark, label string) string {
	return "eventtest.park." + run + "." + mark + label
}

func (this *park) queue() projection.Park {
	found := this.factory.New(this.t)
	if found == nil {
		this.refuse("the factory answered no park")
	}
	return found
}

// Always Identity.Whole(), because that is the only value the framework ever
// hands a park: the projection and its generation, with the partition dropped.
func (this *park) of(label string, generation projection.Generation) projection.Identity {
	if len(label) > widestLabel {
		this.refuse("this section named a projection %q, where the harness reserves %d bytes of the name for a label", label, widestLabel)
	}
	held, err := projection.NewIdentity(parkName(this.run, this.mark, label), generation, projection.Whole())
	if err != nil {
		this.refuse("the harness cannot name a projection of its own: %v", err)
	}
	return held.Whole()
}

func (this *park) letter(of projection.Identity, sequence string, position event.Position) projection.Letter {
	return projection.Letter{
		Identity:  of,
		Sequencer: "eventtest.sequencer",
		Sequence:  sequence,
		Envelope: event.Envelope{
			Stream:   event.Stream{Family: "eventtest.park", Key: event.Compose(this.run, sequence)},
			Version:  event.Version(position),
			Position: position,
			Type:     "eventtest.parked",
			Revision: 1,
			Payload:  []byte(`{"sequence":"` + sequence + `"}`),
		},
		Attempt: 1,
	}
}

func (this *park) begin(ctx context.Context, held projection.Park) (context.Context, Tx) {
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

func (this *park) commit(ctx context.Context, tx Tx) {
	if err := tx.Commit(ctx); err != nil {
		this.refuse("committing the unit this section was inside answered %v", err)
	}
}

func (this *park) rollback(ctx context.Context, tx Tx) {
	if err := tx.Rollback(ctx); err != nil {
		this.refuse("rolling back the unit this section was inside answered %v", err)
	}
}

func (this *park) sequences(ctx context.Context, held projection.Park, of projection.Identity) uint64 {
	found, err := held.Sequences(ctx, of)
	if err != nil {
		this.refuse("counting the parked sequences of %s answered %v, and a pass reads that refusal as a postpone: a projection whose count can never be read retries for ever without advancing and without halting", of, err)
	}
	return found
}

func (this *park) holds(ctx context.Context, held projection.Park, of projection.Identity, sequence string) bool {
	found, err := held.Holds(ctx, of, sequence)
	if err != nil {
		this.refuse("asking whether %s holds the sequence %q answered %v", of, sequence, err)
	}
	return found
}

func (this *park) holes(ctx context.Context, held projection.Park, of projection.Identity) uint64 {
	found, err := held.Holes(ctx, of)
	if err != nil {
		this.refuse("counting the holes of %s answered %v", of, err)
	}
	return found
}

func (this *park) park(ctx context.Context, held projection.Park, letter projection.Letter) {
	if err := held.Park(ctx, letter); err != nil {
		this.refuse("parking the letter of %q at position %d answered %v", letter.Sequence, letter.Envelope.Position, err)
	}
}

// One letter parked and committed, which is what every section that measures a
// committed queue starts from.
func (this *park) parked(ctx context.Context, held projection.Park, letter projection.Letter) {
	inside, tx := this.begin(ctx, held)
	this.park(inside, held, letter)
	this.commit(inside, tx)
}
