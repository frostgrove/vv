package eventmemory_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/eventtest"
)

func TestTheMemoryStoreSatisfiesTheContract(t *testing.T) {
	eventtest.Run(t, conformance(eventmemory.LogSpec{}, eventmemory.Spec{}))
}

// The same store publishing numbers a deployment is free to choose and a
// hundredth the size of the defaults: a page of four, two changes an append and
// a key of forty bytes are all legal here, and a suite fitted to this store's
// own defaults would report them as its defect.
func TestTheMemoryStoreSatisfiesTheContractAtNarrowerLimits(t *testing.T) {
	eventtest.Run(t, conformance(eventmemory.LogSpec{MaxKey: 40}, eventmemory.Spec{StreamPage: 4, MaxBatch: 2, MaxRead: 3}))
}

func conformance(held eventmemory.LogSpec, spec eventmemory.Spec) eventtest.Factory {
	return eventtest.Factory{
		New: func(t *testing.T) event.Store {
			log := newLog(t, held)
			spec.Log = log
			return failable(t, spec)
		},
		Begin: func(t *testing.T, ctx context.Context, s event.Store) (context.Context, eventtest.Tx) {
			store := supplied(t, s)
			tx, err := store.Store.Begin(ctx)
			if err != nil {
				t.Fatalf("beginning a transaction answered %v", err)
			}
			return eventmemory.WithTransaction(ctx, tx), tx
		},
		Sibling: func(t *testing.T, s event.Store) event.Store {
			spec.Log = supplied(t, s).log
			return failable(t, spec)
		},
		Fail: func(t *testing.T, s event.Store, outcome event.Outcome) bool {
			if outcome == event.Unconfirmed {
				return false
			}
			supplied(t, s).arm(outcome)
			return true
		},
		Unparsable: func(*testing.T, event.Store) event.Cursor { return "neither this store's format nor anybody's" },
	}
}

func failable(t *testing.T, spec eventmemory.Spec) *failing {
	t.Helper()
	store := newStore(t, spec)
	t.Cleanup(func() { _ = store.Close() })
	return &failing{Store: store, log: spec.Log}
}

func supplied(t *testing.T, s event.Store) *failing {
	t.Helper()
	store, is := s.(*failing)
	if !is {
		t.Fatalf("the suite handed back a store this factory did not build: %T", s)
	}
	return store
}

var errInjected = errors.New("eventmemory_test: this store was asked to fail its next operation")

// The one thing a store value cannot be asked for through the contract: a
// failure on purpose. It refuses before forwarding, so an injected NotWritten is
// a write that certainly did not land, and it answers nothing for Unconfirmed —
// this store has no commit window to lose, which is what the suite reports as
// not certified rather than as a pass.
type failing struct {
	*eventmemory.Store
	log *eventmemory.Log

	mutex   sync.Mutex
	outcome event.Outcome
	armed   bool
}

func (this *failing) arm(outcome event.Outcome) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.outcome, this.armed = outcome, true
}

func (this *failing) fires() error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if !this.armed {
		return nil
	}
	this.armed = false
	if this.outcome == event.Unclassified {
		return errInjected
	}
	return event.Failure(this.outcome, errInjected)
}

func (this *failing) Append(ctx context.Context, request event.AppendRequest) error {
	if err := this.fires(); err != nil {
		return err
	}
	return this.Store.Append(ctx, request)
}

func (this *failing) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	if err := this.fires(); err != nil {
		return nil, err
	}
	return this.Store.ReadStream(ctx, stream, after)
}

func (this *failing) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	if err := this.fires(); err != nil {
		return nil, "", err
	}
	return this.Store.ReadAll(ctx, after)
}
