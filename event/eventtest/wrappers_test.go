package eventtest_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventtest"
)

// The store shapes the constancy clauses are falsified with, and the reason
// they are here rather than in the defect inventory: each is internally
// consistent and breaks nothing a section reads, so every one of them passed
// every section of this suite before the clause it breaks was asserted.

// The store with no column for the instant, which fills the field as it scans
// the row: every envelope carries one, none of them is the zero time, and the
// same event answers a different one to every reader. The counter is what makes
// it that rather than two calls of a clock that might land on one nanosecond.
type reminting struct {
	event.Store
	mutex sync.Mutex
	at    int64
}

func (this *reminting) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	return this.minted(page), err
}

func (this *reminting) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	return this.minted(page), cursor, err
}

func (this *reminting) minted(page []event.Envelope) []event.Envelope {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	for index := range page {
		this.at++
		page[index].RecordedAt = time.Unix(0, this.at)
	}
	return page
}

// The capability derived from the executor rather than from the store: a value
// that answers Supported while something is bound and Unsupported otherwise
// answers the door one thing and the kernel another, and every section this run
// gated is gated on the first.
type wavering struct {
	event.Store
	mutex sync.Mutex
	asked int
}

func (this *wavering) Capabilities() event.Capabilities {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	capabilities := this.Store.Capabilities()
	if this.asked%2 == 1 {
		capabilities.Transactions = event.Unsupported
	}
	this.asked++
	return capabilities
}

// The store that re-points at another database and says so to nobody: every
// call names a fresh backing, so a token minted through one operation is refused
// by the next and two subsystems that wrote atomically cannot prove they did.
type repointing struct{ event.Store }

func (this *repointing) Backing() event.Backing {
	backing, err := event.NewBacking(new(int))
	if err != nil {
		return event.Backing{}
	}
	return backing
}

// One store at the door and another kind afterwards, which is the factory
// obligation nothing else here breaks: both stores are internally consistent, so
// only a comparison between two values of one factory can see it.
type relimiting struct{ event.Store }

func (this relimiting) Limits() event.Limits {
	limits := this.Store.Limits()
	limits.StreamPage++
	return limits
}

type unclaiming struct{ event.Store }

func (this unclaiming) Capabilities() event.Capabilities {
	capabilities := this.Store.Capabilities()
	capabilities.Transactions = event.Unsupported
	return capabilities
}

func shiftingFactory(later func(event.Store) event.Store) eventtest.Factory {
	factory := stagingFactory(false, nil)
	built, made := factory.New, 0
	factory.New = func(t *testing.T) event.Store {
		store := built(t)
		made++
		if made == 1 || later == nil {
			return store
		}
		return later(store)
	}
	return factory
}
