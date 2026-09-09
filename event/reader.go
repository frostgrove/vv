package event

import (
	"context"
	"fmt"
)

// Store embeds Log, so a store already passes where a log is wanted. This is for
// the consumer that must not be able to assert its way back to the append
// surface, because a projector that can append is how a replay writes.
//
// It is deliberately the one wrapper in this repository with no Next, and that
// is the point of the rule rather than a breach of it: a Next here would be the
// method that hands back the surface this call exists to take away.
func ReadOnly(store Store) Log {
	if nilByAnyRoute(store) {
		return nil
	}
	return readOnly{Log: store}
}

type readOnly struct{ Log }

// The second and last door a store enters the kernel by, so it runs the same
// store-honesty checks Bind does. There is no page-size parameter here or
// anywhere: the kernel is the only party that fills a read, which is what gives
// MaxRead exactly one meaning.
func Read(log Log, after Cursor) (*Reader, error) {
	_, limits, err := admit(log)
	if err != nil {
		return nil, err
	}
	return &Reader{log: log, limits: limits, cursor: after}, nil
}

// The one stateful value in the caller-facing surface, and it is for one
// goroutine at a time: Next advances it and Events serves what the last Next
// fetched. The page is a different question — it belongs to whoever received it
// from the moment it is returned, survives the next Next, and may be fanned out
// to workers.
type Reader struct {
	log    Log
	limits Limits
	cursor Cursor
	events []Envelope
}

func (this *Reader) Next(ctx context.Context) (bool, error) {
	page, cursor, err := this.log.ReadAll(ctx, this.cursor)
	if err != nil {
		return false, refuseRead(err)
	}
	if err := this.checkPage(page, cursor); err != nil {
		return false, err
	}
	this.events, this.cursor = page, cursor
	return len(page) > 0, nil
}

func (this *Reader) Events() []Envelope { return this.events }

// Safe to persist and to resume from in another process, with one exception:
// not if the walk that produced it ran on a context carrying a transaction of
// this backing. What such a walk returned is the store's business — a store
// reading through that transaction answered its uncommitted events and this
// cursor is already past them, so a rollback afterwards discards events nothing
// will read again. Drain outside the write, or discard the cursor with the
// transaction.
func (this *Reader) Cursor() Cursor { return this.cursor }

// The page and the cursor it was answered with are one answer, so both are
// checked here and the reader's own cursor is not advanced until they pass.
//
// Gaps in the positions are normal and going backwards is not, so a page that
// does not ascend is refused before a consumer checkpoints past an event it
// never saw. That arm is one half of a law and not a store's option: the whole
// of it is that the cursor tiles the log in position order, and the other half —
// a cursor that advanced past a position which had not settled — is invisible
// from one page and is certified live instead, by the resumption section walking
// across a writer holding a lower position uncommitted. What forbids the
// alternative order outright is one stream's events reaching a projector in the
// order that stream holds them, which is [[D-128]]. A cursor over the published
// ceiling is refused because a checkpoint
// store has a column of exactly that width. An empty cursor beside a NON-EMPTY
// page is refused because the empty cursor is the origin: a log that minted one
// there would make this reader read the head of the log for ever while a
// consumer's checkpoint advanced once a pass — no error, no halt, and a
// destination written twice. An empty page answered with an empty cursor is a
// fresh log read from the origin and stays legal, because nothing was delivered
// to resume past.
func (this *Reader) checkPage(page []Envelope, cursor Cursor) error {
	if len(page) > this.limits.MaxRead {
		return fmt.Errorf("%w: a read answered with %d envelopes where this store publishes %d", ErrBackend, len(page), this.limits.MaxRead)
	}
	if len(cursor) > MaxCursorBytes {
		return fmt.Errorf("%w: a read answered a cursor of %d bytes where the kernel publishes a ceiling of %d", ErrBackend, len(cursor), MaxCursorBytes)
	}
	if cursor == "" && len(page) > 0 {
		return fmt.Errorf("%w: a read answered %d envelopes beside the empty cursor, which is the origin of the log, so resuming from it reads the same page again", ErrBackend, len(page))
	}
	for offset := 1; offset < len(page); offset++ {
		if page[offset].Position <= page[offset-1].Position {
			return fmt.Errorf("%w: %s answered a page whose positions do not ascend, so a consumer resuming from its cursor cannot tile the log", ErrBackend, page[offset].Stream)
		}
	}
	return nil
}
