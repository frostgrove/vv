package eventtest

import (
	"context"
	"errors"
	"sync"

	"github.com/frostgrove/vv/event/projection"
)

// One inventory of the defects the park harness is built to detect, in code,
// because a count kept in prose is what drifts. Every section is named by at
// least one row and a test computes that rather than remembering it.
//
// Three of the eight are queues rather than decorators. Requiring the ambient
// transaction where the contract says there is none, writing a letter outside
// the caller's unit and answering the blocking test from the committed state
// alone are decisions about which handle a statement runs on, and a decorator
// that forwards cannot reach the handle the queue opened.
type parkDefect struct {
	name    string
	section string
	over    func(projection.Park) projection.Park
}

func parkDefects() []parkDefect {
	return []parkDefect{
		{"answers the blocking test from the snapshot it took the first time it was asked", "committed state",
			func(held projection.Park) projection.Park {
				return &staleHolds{overPark: overPark{held}, answered: map[string]bool{}}
			}},
		{"counts its parked sequences where it is asked for its holes", "counts",
			func(held projection.Park) projection.Park { return holesAreSequences{overPark{held}} }},
		{"keys its rows on the projection name and drops the generation", "identity",
			func(held projection.Park) projection.Park { return dropsTheGeneration{overPark{held}} }},
		{"refuses a full queue with an error of its own rather than one wrapping ErrParkFull", "bounds",
			func(held projection.Park) projection.Park { return bareRefusal{overPark{held}} }},
		{"counts the letters it was handed rather than the rows its table holds", "inside the unit",
			func(held projection.Park) projection.Park {
				return &cachedCount{overPark: overPark{held}, counted: map[string]uint64{}}
			}},
		{"requires the caller's unit of work to count its parked sequences", "outside a unit", nil},
		{"writes its letters on a connection of its own rather than in the caller's unit", "inside the unit", nil},
		{"answers the blocking test from the committed state even inside the unit that parked", "inside the unit", nil},
	}
}

// The park the harness builds around the park under test. Every method of the
// contract is required, so an embedded park forwards all four — under the alias,
// because an embedded projection.Park names its field Park and a field shadows
// the method beside it: the value would forward three of the four and implement
// the contract not at all.
type queue = projection.Park

type overPark struct{ queue }

// The answer a wait needs and the one a cache gives it: the sequence was not
// parked when this value was first asked, and it goes on saying so after the
// unit that parked it committed.
type staleHolds struct {
	overPark

	mutex    sync.Mutex
	answered map[string]bool
}

func (this *staleHolds) Holds(ctx context.Context, of projection.Identity, sequence string) (bool, error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	at := of.String() + "/" + sequence
	if held, asked := this.answered[at]; asked {
		return held, nil
	}
	held, err := this.queue.Holds(ctx, of, sequence)
	if err != nil {
		return false, err
	}
	this.answered[at] = held
	return held, nil
}

type holesAreSequences struct{ overPark }

func (this holesAreSequences) Holes(ctx context.Context, of projection.Identity) (uint64, error) {
	return this.queue.Sequences(ctx, of)
}

type dropsTheGeneration struct{ overPark }

func (this dropsTheGeneration) Sequences(ctx context.Context, of projection.Identity) (uint64, error) {
	return this.queue.Sequences(ctx, ungenerated(of))
}

func (this dropsTheGeneration) Holds(ctx context.Context, of projection.Identity, sequence string) (bool, error) {
	return this.queue.Holds(ctx, ungenerated(of), sequence)
}

func (this dropsTheGeneration) Park(ctx context.Context, letter projection.Letter) error {
	letter.Identity = ungenerated(letter.Identity)
	return this.queue.Park(ctx, letter)
}

func (this dropsTheGeneration) Holes(ctx context.Context, of projection.Identity) (uint64, error) {
	return this.queue.Holes(ctx, ungenerated(of))
}

func ungenerated(of projection.Identity) projection.Identity {
	held, err := projection.NewIdentity(of.Projection(), projection.Ungenerated, projection.Whole())
	if err != nil {
		return of
	}
	return held
}

type bareRefusal struct{ overPark }

var errQueueFull = errors.New("eventtest: a queue's own refusal, wrapping nothing this package publishes")

func (this bareRefusal) Park(ctx context.Context, letter projection.Letter) error {
	if err := this.queue.Park(ctx, letter); err != nil {
		if errors.Is(err, projection.ErrParkFull) {
			return errQueueFull
		}
		return err
	}
	return nil
}

// The count park.go says must be exact: a queue that answers it out of what it
// has been handed rather than out of its table reports a sequence a rolled-back
// pass never parked, and the loop opens a unit per pass for ever to be told
// about a letter that is not there.
type cachedCount struct {
	overPark

	mutex   sync.Mutex
	counted map[string]uint64
}

func (this *cachedCount) Sequences(ctx context.Context, of projection.Identity) (uint64, error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if held, counted := this.counted[of.String()]; counted {
		return held, nil
	}
	return this.queue.Sequences(ctx, of)
}

func (this *cachedCount) Park(ctx context.Context, letter projection.Letter) error {
	if err := this.queue.Park(ctx, letter); err != nil {
		return err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.counted[letter.Identity.String()]++
	return nil
}
