package projection

import (
	"bytes"
	"context"

	"github.com/frostgrove/vv/event"
)

// Yours from the moment it is handed over, including the slice's capacity and
// every payload in it: keep it, read it from any goroutine, write into it. Each
// attempt is handed its own, so a page you rewrote in place is not what the
// retry applies.
type Batch struct {
	Projection string
	Identity   Identity
	Envelopes  []event.Envelope
	Attempt    int
}

type Handler interface {
	Apply(ctx context.Context, batch Batch) error
}

type HandlerFunc func(ctx context.Context, batch Batch) error

func (this HandlerFunc) Apply(ctx context.Context, batch Batch) error { return this(ctx, batch) }

// Each attempt is handed its own page. The hand-off rule grants the handler the
// slice, its capacity and every payload in it — keep it, read it from any
// goroutine, write into it — and this projection is the first sender in that
// enumeration which re-reads what it handed over, because a retry re-applies the
// page it holds. So the sender pays: a fresh slice and a fresh copy of every
// payload per attempt, the first included, with the log's own page kept
// untouched beside them. A handler that sorted, filtered or redacted attempt 1's
// page in place is re-applied over what the log answered rather than over what
// it left behind.
func copyOf(page []event.Envelope) []event.Envelope {
	held := make([]event.Envelope, len(page))
	for index, envelope := range page {
		envelope.Payload = bytes.Clone(envelope.Payload)
		held[index] = envelope
	}
	return held
}
