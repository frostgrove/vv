package eventmemory

import (
	"bytes"
	"context"
	"sort"

	"github.com/frostgrove/vv/event"
)

// A staged envelope carries no position, because a position is assigned at
// commit and one assigned earlier would have to be reassigned or reissued —
// this store's answer to what event.Envelope.Position means before a commit.
func (this *Store) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if this.closed.Load() {
		return nil, event.Failure(event.Closed, nil)
	}
	tx, err := this.ambient(ctx)
	if err != nil {
		return nil, event.Failure(event.Refused, err)
	}

	this.log.mutex.Lock()
	defer this.log.mutex.Unlock()

	if err := tx.live(); err != nil {
		return nil, event.Failure(event.Refused, err)
	}
	committed := this.log.streams[stream]
	staged := tx.stagedFor(stream)
	length := len(committed) + len(staged)
	if after >= event.Version(length) {
		return nil, nil
	}
	first := int(after)
	count := min(length-first, this.limits.StreamPage)
	page := make([]event.Envelope, 0, count)
	for offset := range count {
		index, held := first+offset, committed
		if index >= len(committed) {
			index, held = index-len(committed), staged
		}
		page = append(page, handOut(held[index]))
	}
	return page, nil
}

// Position-ordered, so it returns no staged envelope to anyone, its own
// transaction included — this store's answer to a question event.Log.ReadAll
// leaves to the store. The bound transaction is still consulted, and by the same
// two halves as the other two doors: a caller who drives the store past the
// kernel's own check is answered the same way at every door, rather than refused
// at one and handed a page at the other.
//
// The cursor is parsed above the lock and its refusal reported below it. The
// parse reads nothing of the log but the fingerprint it was born with, and a
// caller replaying a corrupt checkpoint in a loop should not contend for the log
// to be told so; the report stays below because a transaction this store cannot
// use is Refused before anything the cursor says is looked at.
func (this *Store) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if this.closed.Load() {
		return nil, "", event.Failure(event.Closed, nil)
	}
	tx, err := this.ambient(ctx)
	if err != nil {
		return nil, "", event.Failure(event.Refused, err)
	}
	from, unreadable := this.log.readCursor(after)

	this.log.mutex.Lock()
	defer this.log.mutex.Unlock()

	if err := tx.live(); err != nil {
		return nil, "", event.Failure(event.Refused, err)
	}
	if unreadable != nil {
		return nil, "", event.Failure(event.BadCursor, unreadable)
	}
	start := sort.Search(len(this.log.global), func(index int) bool {
		return this.log.global[index].Position > from
	})
	count := min(len(this.log.global)-start, this.limits.MaxRead)
	page := make([]event.Envelope, 0, count)
	for _, envelope := range this.log.global[start : start+count] {
		page = append(page, handOut(envelope))
	}
	if len(page) > 0 {
		from = page[len(page)-1].Position
	}
	return page, this.log.mintCursor(from), nil
}

// The log keeps every envelope it published, so the copy is the store's to make.
func handOut(envelope event.Envelope) event.Envelope {
	envelope.Payload = bytes.Clone(envelope.Payload)
	return envelope
}
