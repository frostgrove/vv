package eventtest_test

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/receipt"
)

// The reference ledger this package's own tests are run against, written the way
// the contract describes it: the insert first, blocking on the key the way a
// primary-key index does, and the read after it and in the same unit. The four
// flags are the four defects a decorator cannot reach, each one thing written
// wrong.

var (
	errLedgerOutsideAUnit = errors.New("eventtest_test: this context carries no unit of this ledger's, and a claim runs inside the caller's")
	errLedgerNoRow        = errors.New("eventtest_test: the completion of an operation key wrote no row")
)

type ledgerRows struct {
	mutex    sync.Mutex
	kept     map[string]receipt.Receipt
	staged   map[*unit]map[string]receipt.Receipt
	claiming *unit
	locks    *latches
	ticks    int64
	base     time.Time
}

type memoryLedger struct {
	rows    *ledgerRows
	backing event.Backing

	outside   bool
	completes bool
	dirty     bool
	newest    bool
}

func newMemoryLedger(t interface{ Fatalf(string, ...any) }) *memoryLedger {
	rows := &ledgerRows{
		kept:   map[string]receipt.Receipt{},
		staged: map[*unit]map[string]receipt.Receipt{},
		locks:  newLatches(),
		base:   time.Now().UTC().Truncate(time.Second),
	}
	backing, err := event.NewBacking(rows)
	if err != nil {
		t.Fatalf("the fixture cannot name what it writes to: %v", err)
	}
	return &memoryLedger{rows: rows, backing: backing}
}

func (this *memoryLedger) beside() *memoryLedger {
	held := *this
	return &held
}

func (this *memoryLedger) Transaction(ctx context.Context) (event.Authority, error) {
	held := unitIn(ctx, this.rows)
	if held == nil {
		return event.Authority{}, nil
	}
	return event.NewAuthority(this.backing, held)
}

// The insert and then the read, in that order and in this unit. The key latch is
// what the primary-key index is: a second claim of one key waits for the unit
// ahead of it to end, and the read after it sees whatever that unit left.
func (this *memoryLedger) Claim(ctx context.Context, row receipt.Receipt) (receipt.Receipt, bool, error) {
	held := unitIn(ctx, this.rows)
	if held == nil {
		return receipt.Receipt{}, false, errLedgerOutsideAUnit
	}
	this.remember(held)
	if err := this.rows.locks.take(ctx, held, row.Key.Value(), true); err != nil {
		return receipt.Receipt{}, false, err
	}
	won := this.insert(held, row)
	found, taken := this.read(held, row.Key)
	if !taken {
		return receipt.Receipt{}, won, nil
	}
	return found, won, nil
}

func (this *memoryLedger) Complete(ctx context.Context, row receipt.Receipt) error {
	held := unitIn(ctx, this.rows)
	if held == nil {
		return errLedgerOutsideAUnit
	}
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	found, taken := this.held(held, row.Key)
	if !taken {
		return errLedgerNoRow
	}
	found.First, found.Last, found.Complete = row.First, row.Last, true
	if this.completes {
		this.rows.kept[row.Key.Value()] = found
	}
	this.writing(held)[row.Key.Value()] = found
	return nil
}

// On no unit at all, which is the second connection a resolve runs on — except
// under the dirty flag, where it is the claiming unit's own and an uncommitted
// row reads as a committed one.
func (this *memoryLedger) Find(ctx context.Context, key receipt.Key) (receipt.Receipt, bool, error) {
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	if this.dirty {
		if held := this.rows.claiming; held != nil {
			found, taken := this.held(held, key)
			return found, taken, nil
		}
	}
	found, taken := this.held(nil, key)
	return found, taken, nil
}

func (this *memoryLedger) Horizon(context.Context) (time.Time, error) {
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	var at time.Time
	for _, held := range this.rows.kept {
		switch {
		case at.IsZero():
			at = held.RecordedAt
		case this.newest && held.RecordedAt.After(at):
			at = held.RecordedAt
		case !this.newest && held.RecordedAt.Before(at):
			at = held.RecordedAt
		}
	}
	if at.IsZero() {
		return this.now(), nil
	}
	return at, nil
}

func (this *memoryLedger) remember(by *unit) {
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	this.rows.claiming = by
}

func (this *memoryLedger) insert(by *unit, row receipt.Receipt) bool {
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	if _, taken := this.held(by, row.Key); taken {
		return false
	}
	row.RecordedAt = this.now()
	if this.outside {
		this.rows.kept[row.Key.Value()] = row
		return true
	}
	this.writing(by)[row.Key.Value()] = row
	return true
}

func (this *memoryLedger) read(by *unit, key receipt.Key) (receipt.Receipt, bool) {
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	return this.held(by, key)
}

// The clock the row is dated by is the ledger's own and never the caller's, and
// it ticks so that two rows written one after the other carry two instants: a
// horizon taken from the newest and one taken from the oldest are otherwise the
// same instant and the section that compares them would assert nothing.
func (this *memoryLedger) now() time.Time {
	this.rows.ticks++
	return this.rows.base.Add(time.Duration(this.rows.ticks) * time.Millisecond)
}

func (this *memoryLedger) held(by *unit, key receipt.Key) (receipt.Receipt, bool) {
	if by != nil {
		if found, taken := this.rows.staged[by][key.Value()]; taken {
			return found, true
		}
	}
	found, taken := this.rows.kept[key.Value()]
	return found, taken
}

func (this *memoryLedger) writing(by *unit) map[string]receipt.Receipt {
	held, made := this.rows.staged[by]
	if made {
		return held
	}
	held = map[string]receipt.Receipt{}
	this.rows.staged[by] = held
	rows := this.rows
	by.stage(func() {
		rows.mutex.Lock()
		defer rows.mutex.Unlock()
		for key, row := range rows.staged[by] {
			rows.kept[key] = row
		}
	})
	by.holding(func() {
		rows.mutex.Lock()
		defer rows.mutex.Unlock()
		delete(rows.staged, by)
		if rows.claiming == by {
			rows.claiming = nil
		}
	})
	return held
}
