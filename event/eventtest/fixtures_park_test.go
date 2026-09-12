package eventtest_test

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/frostgrove/vv/event/projection"
)

// The reference queue this package's own tests are run against. Sequences reads
// the committed state with no unit at all, because that is where a pass asks it;
// Holds reads the caller's unit where there is one and the committed state where
// there is none, because a pass asks it inside the unit it is writing the letter
// in and a wait asks it on a request path's own goroutine. The three flags are
// the three defects a decorator cannot reach.

var errParkOutsideAUnit = errors.New("eventtest_test: this context carries no unit of this queue's, and a letter is written in the caller's")

type parkRows struct {
	mutex  sync.Mutex
	kept   []projection.Letter
	staged map[*unit][]projection.Letter
}

type memoryPark struct {
	rows *parkRows

	needsAUnit    bool
	outside       bool
	committedOnly bool

	maxSequences, maxLetters int
}

func newMemoryPark() *memoryPark {
	return &memoryPark{rows: &parkRows{staged: map[*unit][]projection.Letter{}}}
}

func (this *memoryPark) beside() *memoryPark {
	held := *this
	return &held
}

func (this *memoryPark) bounded(sequences, letters int) *memoryPark {
	held := this.beside()
	held.maxSequences, held.maxLetters = sequences, letters
	return held
}

func (this *memoryPark) Sequences(ctx context.Context, of projection.Identity) (uint64, error) {
	if this.needsAUnit && unitIn(ctx, this.rows) == nil {
		return 0, errParkOutsideAUnit
	}
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	held := map[string]bool{}
	for _, letter := range this.rows.kept {
		if letter.Identity == of {
			held[letter.Sequence] = true
		}
	}
	return uint64(len(held)), nil
}

func (this *memoryPark) Holds(ctx context.Context, of projection.Identity, sequence string) (bool, error) {
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	for _, letter := range this.letters(this.reading(ctx), of) {
		if letter.Sequence == sequence {
			return true, nil
		}
	}
	return false, nil
}

func (this *memoryPark) Park(ctx context.Context, letter projection.Letter) error {
	held := this.writer(ctx)
	if held == nil && !this.outside {
		return errParkOutsideAUnit
	}
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	if err := this.room(held, letter); err != nil {
		return err
	}
	if held == nil {
		this.rows.kept = append(this.rows.kept, letter)
		return nil
	}
	this.rows.staged[held] = append(this.writing(held), letter)
	return nil
}

func (this *memoryPark) Holes(ctx context.Context, of projection.Identity) (uint64, error) {
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	return uint64(len(this.letters(nil, of))), nil
}

func (this *memoryPark) reading(ctx context.Context) *unit {
	if this.committedOnly {
		return nil
	}
	return unitIn(ctx, this.rows)
}

// Nil under the flag, which is what writing a letter on a handle of the queue's
// own comes to: the row is there whether or not the unit beside it commits.
func (this *memoryPark) writer(ctx context.Context) *unit {
	if this.outside {
		return nil
	}
	return unitIn(ctx, this.rows)
}

// The bound is per sequence and never per queue, which is the shape §UC-151
// describes: a new sequence is refused when the queue is at its own limit, and an
// existing one when it holds its own limit of letters.
func (this *memoryPark) room(by *unit, letter projection.Letter) error {
	letters, sequences := 0, map[string]bool{}
	for _, held := range this.letters(by, letter.Identity) {
		sequences[held.Sequence] = true
		if held.Sequence == letter.Sequence {
			letters++
		}
	}
	switch {
	case letters == 0 && this.maxSequences > 0 && len(sequences) >= this.maxSequences:
		return fmt.Errorf("%w: %q holds %d sequences and a letter for the new sequence %q needs a %d+1th",
			projection.ErrParkFull, letter.Identity, len(sequences), letter.Sequence, len(sequences))
	case this.maxLetters > 0 && letters >= this.maxLetters:
		return fmt.Errorf("%w: the sequence %q of %q holds %d letters, which is its own bound",
			projection.ErrParkFull, letter.Sequence, letter.Identity, letters)
	}
	return nil
}

func (this *memoryPark) letters(by *unit, of projection.Identity) []projection.Letter {
	held := []projection.Letter{}
	for _, letter := range this.rows.kept {
		if letter.Identity == of {
			held = append(held, letter)
		}
	}
	if by == nil {
		return held
	}
	for _, letter := range this.rows.staged[by] {
		if letter.Identity == of {
			held = append(held, letter)
		}
	}
	return held
}

func (this *memoryPark) writing(by *unit) []projection.Letter {
	held, made := this.rows.staged[by]
	if made {
		return held
	}
	this.rows.staged[by] = []projection.Letter{}
	rows := this.rows
	by.stage(func() {
		rows.mutex.Lock()
		defer rows.mutex.Unlock()
		rows.kept = append(rows.kept, rows.staged[by]...)
	})
	by.holding(func() {
		rows.mutex.Lock()
		defer rows.mutex.Unlock()
		delete(rows.staged, by)
	})
	return rows.staged[by]
}
