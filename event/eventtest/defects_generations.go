package eventtest

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/event/projection"
)

// One inventory of the defects the ownership harness is built to detect, in
// code, because a count kept in prose is what drifts. Every section is named by
// at least one row and a test computes that rather than remembering it.
//
// Three of the six are ownership rows rather than decorators, and they are the
// three the whole harness is for: which handle a read runs on, and whether it
// takes a lock. Neither is anything a decorator that forwards can reach — the
// framework holds a method set and no resource, and so does this.
type generationsDefect struct {
	name    string
	section string
	over    func(projection.Generations) projection.Generations
}

func generationsDefects() []generationsDefect {
	return []generationsDefect{
		{"answers an error for a projection that has never cut over", "ungenerated",
			func(held projection.Generations) projection.Generations {
				return refusesAnAbsentRow{overGenerations{held}}
			}},
		{"moves nothing and answers nil", "activation",
			func(held projection.Generations) projection.Generations { return movesNothing{overGenerations{held}} }},
		{"reads the row and then writes it rather than moving it with one fenced statement", "fenced activation",
			func(held projection.Generations) projection.Generations { return readThenWrite{overGenerations{held}} }},
		{"answers from a handle of its own rather than the caller's unit", "unit of work", nil},
		{"reads from a handle of its own while its write rides in the caller's unit", "unit of work", nil},
		{"reads the ownership row without taking a lock on it", "locking read", nil},
	}
}

type overGenerations struct{ projection.Generations }

var errNoOwnershipRow = errors.New("eventtest: this projection holds no ownership row")

type refusesAnAbsentRow struct{ overGenerations }

func (this refusesAnAbsentRow) Active(ctx context.Context, name string) (projection.Generation, error) {
	found, err := this.Generations.Active(ctx, name)
	if err == nil && found == projection.Ungenerated {
		return projection.Ungenerated, errNoOwnershipRow
	}
	return found, err
}

type movesNothing struct{ overGenerations }

func (movesNothing) Activate(context.Context, string, projection.Generation, projection.Generation) error {
	return nil
}

// The one spelling Activate may not have. It moves the row to `to` whatever the
// row holds, so the refusal the second of two operators is owed never arrives
// and the read target is whichever of them committed last.
type readThenWrite struct{ overGenerations }

func (this readThenWrite) Activate(ctx context.Context, name string, _, to projection.Generation) error {
	found, err := this.Generations.Active(ctx, name)
	if err != nil {
		return err
	}
	return this.Generations.Activate(ctx, name, found, to)
}
