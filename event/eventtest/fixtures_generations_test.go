package eventtest_test

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/projection"
)

// The reference ownership row, written the way the contract describes it: Active
// is the locking read, so the cutover's write waits behind every unit that read
// the row, and Activate is one fenced statement rather than a read followed by a
// write. The two flags are the two defects a decorator cannot reach.

var errOwnershipOutsideAUnit = errors.New("eventtest_test: this context carries no unit of this ownership row's, and both methods run inside the caller's")

type ownershipRows struct {
	mutex  sync.Mutex
	kept   map[string]projection.Generation
	staged map[*unit]map[string]projection.Generation
	locks  *latches
}

type memoryGenerations struct {
	rows *ownershipRows

	locking   bool
	elsewhere bool
	reading   bool
}

func newMemoryGenerations() *memoryGenerations {
	return &memoryGenerations{
		rows: &ownershipRows{
			kept:   map[string]projection.Generation{},
			staged: map[*unit]map[string]projection.Generation{},
			locks:  newLatches(),
		},
		locking: true,
	}
}

func (this *memoryGenerations) beside() *memoryGenerations {
	held := *this
	return &held
}

func (this *memoryGenerations) unlocked() *memoryGenerations {
	held := this.beside()
	held.locking = false
	return held
}

func (this *memoryGenerations) onASecondHandle() *memoryGenerations {
	held := this.beside()
	held.elsewhere = true
	return held
}

// The read alone on a handle of its own — a replica, a second pool, a connection
// the value opened — while the write still rides in the caller's unit.
func (this *memoryGenerations) readingElsewhere() *memoryGenerations {
	held := this.beside()
	held.reading = true
	return held
}

// The row a read path resolves its tables through. Under the locking read the
// share is held until this unit ends, which is what makes a cutover wait; under
// the plain one nothing is taken and the cutover commits between this read and
// this unit's commit.
func (this *memoryGenerations) Active(ctx context.Context, name string) (projection.Generation, error) {
	held := this.reader(ctx)
	if held != nil && this.locking {
		if err := this.rows.locks.take(ctx, held, name, false); err != nil {
			return projection.Ungenerated, err
		}
	}
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	return this.held(held, name), nil
}

// One fenced statement: the row lock is taken first, the way an UPDATE takes
// one, and what the row holds is compared inside it.
func (this *memoryGenerations) Activate(ctx context.Context, name string, from, to projection.Generation) error {
	held := this.unit(ctx)
	if held == nil && !this.elsewhere {
		return errOwnershipOutsideAUnit
	}
	if held != nil {
		if err := this.rows.locks.take(ctx, held, name, true); err != nil {
			return err
		}
	}
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	if this.held(held, name) != from {
		return fmt.Errorf("%w: the ownership row of %q does not hold %d, so this cutover has nothing to move", event.ErrConflict, name, from)
	}
	if held == nil {
		this.rows.kept[name] = to
		return nil
	}
	this.writing(held)[name] = to
	return nil
}

func (this *memoryGenerations) reader(ctx context.Context) *unit {
	if this.reading {
		return nil
	}
	return this.unit(ctx)
}

func (this *memoryGenerations) unit(ctx context.Context) *unit {
	if this.elsewhere {
		return nil
	}
	return unitIn(ctx, this.rows)
}

func (this *memoryGenerations) held(by *unit, name string) projection.Generation {
	if by != nil {
		if found, staged := this.rows.staged[by][name]; staged {
			return found
		}
	}
	return this.rows.kept[name]
}

func (this *memoryGenerations) writing(by *unit) map[string]projection.Generation {
	held, made := this.rows.staged[by]
	if made {
		return held
	}
	held = map[string]projection.Generation{}
	this.rows.staged[by] = held
	rows := this.rows
	by.stage(func() {
		rows.mutex.Lock()
		defer rows.mutex.Unlock()
		for name, generation := range rows.staged[by] {
			rows.kept[name] = generation
		}
	})
	by.holding(func() {
		rows.mutex.Lock()
		defer rows.mutex.Unlock()
		delete(rows.staged, by)
	})
	return held
}
