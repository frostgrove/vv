package eventtest_test

import (
	"context"
	"errors"
	"sync"
)

// The unit of work the three application fixtures share, and the row locks two
// of them need. They are written here rather than borrowed from a driver
// because what the ledger and the ownership harnesses measure is WAITING — a
// claim that blocks on the index, a cutover that waits behind a locking read —
// and a fixture that cannot block could not tell the harness's own detection
// from a harness that detects nothing.
//
// One thing it deliberately does not model: a commit applies its staged writes
// one at a time rather than as one atomic step. No section here writes two rows
// in one unit and reads them from another.
var errUnitFinished = errors.New("eventtest_test: this unit of work has already been committed or rolled back")

type unit struct {
	mutex   sync.Mutex
	staged  []func()
	release []func()
	done    bool
}

type unitKey struct{ rows any }

func beginUnit(ctx context.Context, rows any) (context.Context, *unit) {
	held := &unit{}
	return context.WithValue(ctx, unitKey{rows: rows}, held), held
}

func unitIn(ctx context.Context, rows any) *unit {
	held, _ := ctx.Value(unitKey{rows: rows}).(*unit)
	return held
}

func (this *unit) stage(write func()) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.staged = append(this.staged, write)
}

func (this *unit) holding(free func()) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.release = append(this.release, free)
}

func (this *unit) Commit(context.Context) error { return this.finish(true) }

// Answers nil for a unit already finished, because the harness rolls back every
// unit it began when the section ends, committed ones included.
func (this *unit) Rollback(context.Context) error {
	_ = this.finish(false)
	return nil
}

// The callbacks are taken out from under this unit's own lock before any of them
// runs: they take the lock of the rows they belong to, and a fixture staging a
// write takes those two in the other order.
func (this *unit) finish(apply bool) error {
	this.mutex.Lock()
	if this.done {
		this.mutex.Unlock()
		return errUnitFinished
	}
	this.done = true
	staged, release := this.staged, this.release
	this.staged, this.release = nil, nil
	this.mutex.Unlock()

	if apply {
		for _, write := range staged {
			write()
		}
	}
	for _, free := range release {
		free()
	}
	return nil
}

// A row lock a unit holds until it ends. A waiter blocks until the holder's unit
// commits or rolls back, or until its own context is done — which is what keeps
// a harness that is measuring a wait from hanging a test binary when the
// implementation under it never waits at all.
type latches struct {
	mutex sync.Mutex
	held  map[string]*latch
}

type latch struct {
	writer  *unit
	readers map[*unit]bool
	changed chan struct{}
}

func newLatches() *latches { return &latches{held: map[string]*latch{}} }

func (this *latches) take(ctx context.Context, by *unit, key string, exclusive bool) error {
	for {
		this.mutex.Lock()
		held, made := this.held[key]
		if !made {
			held = &latch{readers: map[*unit]bool{}, changed: make(chan struct{})}
			this.held[key] = held
		}
		if held.free(by, exclusive) {
			held.hold(by, exclusive)
			this.mutex.Unlock()
			by.holding(func() { this.drop(key, by) })
			return nil
		}
		waiting := held.changed
		this.mutex.Unlock()
		select {
		case <-waiting:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (this *latches) drop(key string, by *unit) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held, made := this.held[key]
	if !made {
		return
	}
	if held.writer == by {
		held.writer = nil
	}
	delete(held.readers, by)
	close(held.changed)
	held.changed = make(chan struct{})
}

func (this *latch) free(by *unit, exclusive bool) bool {
	if this.writer != nil && this.writer != by {
		return false
	}
	if !exclusive {
		return true
	}
	for reader := range this.readers {
		if reader != by {
			return false
		}
	}
	return true
}

func (this *latch) hold(by *unit, exclusive bool) {
	if exclusive {
		this.writer = by
		return
	}
	this.readers[by] = true
}
