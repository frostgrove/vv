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
	if err := this.checkPage(page); err != nil {
		return false, err
	}
	this.events, this.cursor = page, cursor
	return len(page) > 0, nil
}

func (this *Reader) Events() []Envelope { return this.events }

func (this *Reader) Cursor() Cursor { return this.cursor }

// Gaps in the positions are normal and going backwards is not, so a page that
// does not ascend is refused before a consumer checkpoints past an event it
// never saw.
func (this *Reader) checkPage(page []Envelope) error {
	if len(page) > this.limits.MaxRead {
		return fmt.Errorf("%w: a read answered with %d envelopes where this store publishes %d", ErrBackend, len(page), this.limits.MaxRead)
	}
	for offset := 1; offset < len(page); offset++ {
		if page[offset].Position <= page[offset-1].Position {
			return fmt.Errorf("%w: %s answered a page whose positions do not ascend, so a consumer resuming from its cursor cannot tile the log", ErrBackend, page[offset].Stream)
		}
	}
	return nil
}
