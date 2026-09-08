package eventtest

import (
	"context"
	"sync"

	"github.com/frostgrove/vv/event"
)

type reusedPositions struct{ over }

func (this reusedPositions) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	return atOnePosition(page), err
}

func (this reusedPositions) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	return atOnePosition(page), cursor, err
}

func atOnePosition(page []event.Envelope) []event.Envelope {
	for index := range page {
		page[index].Position = 1
	}
	return page
}

// A log written beside the insert rather than by it — a trigger, an outbox, a
// second table a batch is copied into — that half the time does not run. Every
// stream answers every decision it holds, so the only reader that can see the
// loss is one comparing the two reads against each other.
type partialLog struct{ over }

func (this partialLog) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	if err != nil {
		return page, cursor, err
	}
	kept := make([]event.Envelope, 0, len(page))
	for _, envelope := range page {
		if envelope.Version%2 == 1 {
			kept = append(kept, envelope)
		}
	}
	return kept, cursor, nil
}

// A log whose stream column is not the one the event was written to — a
// denormalised copy, a partition key derived rather than carried. Every count
// tallies: the log answers one row for every event this run wrote and every
// stream answers its own, and the two describe two different sets.
type renamedInTheLog struct{ over }

func (this renamedInTheLog) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	if err != nil || len(page) == 0 {
		return page, cursor, err
	}
	last := len(page) - 1
	page[last].Stream = event.Stream{Family: page[last].Stream.Family, Key: page[last].Stream.Key + "-elsewhere"}
	return page, cursor, nil
}

type shortPages struct{ over }

func (this shortPages) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	if err != nil || len(page) < 2 {
		return page, err
	}
	return page[:len(page)-1], nil
}

type misOrderedPages struct{ over }

func (this misOrderedPages) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	if err != nil {
		return page, err
	}
	for left, right := 0, len(page)-1; left < right; left, right = left+1, right-1 {
		page[left], page[right] = page[right], page[left]
	}
	return page, nil
}

type foreignPages struct{ over }

func (this foreignPages) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	if err != nil || len(page) == 0 {
		return page, err
	}
	page[0].Stream = event.Stream{Family: stream.Family, Key: stream.Key + "-elsewhere"}
	return page, nil
}

// A store that holds its own place in the log — a server-side cursor, a value
// that remembers where it last read — and continues from there rather than from
// the cursor it was handed. One walk from the beginning to the end is right, and
// every page it answers is right; a walk resumed at a persisted cursor reads
// wherever the walk before it stopped, which is what a projector restarting is.
type ownPlace struct {
	over
	mutex sync.Mutex
	place event.Cursor
}

func (this *ownPlace) ReadAll(ctx context.Context, _ event.Cursor) ([]event.Envelope, event.Cursor, error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	page, next, err := this.Store.ReadAll(ctx, this.place)
	if err != nil {
		return page, next, err
	}
	this.place = next
	return page, next, nil
}

type unsafeCursors struct{ over }

func (this unsafeCursors) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	if err != nil || len(page) < 2 {
		return page, cursor, err
	}
	return page[:len(page)/2], cursor, nil
}

// The newest event of the log, kept out of the global read while the cursor
// beside it moves past — which is the watermark a store publishes to avoid ever
// showing a gap, and is exactly what a store promising monotone visibility may
// not do: an append that has already returned to its caller is behind a cursor
// taken after it.
type withheldTails struct{ over }

func (this withheldTails) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	if err != nil || len(page) == 0 {
		return page, cursor, err
	}
	return page[:len(page)-1], cursor, nil
}
