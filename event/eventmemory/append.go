package eventmemory

import (
	"bytes"
	"context"
	"errors"

	"github.com/frostgrove/vv/event"
)

var (
	errStreamMoved   = errors.New("eventmemory: the stream is not at the version this append was decided at")
	errStreamClaimed = errors.New("eventmemory: another transaction holds this stream")
)

// Admission is against the committed version plus what this transaction has
// already staged for the stream, so a second append inside one transaction is
// admitted at the version the first produced. A stream another live
// transaction has claimed is refused at once and never waited for.
func (this *Store) Append(ctx context.Context, req event.AppendRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.closed.Load() {
		return event.Failure(event.Closed, nil)
	}
	tx, err := this.ambient(ctx)
	if err != nil {
		return event.Failure(event.Refused, err)
	}
	recorded := this.clock()

	this.log.mutex.Lock()
	defer this.log.mutex.Unlock()

	if err := tx.live(); err != nil {
		return event.Failure(event.Refused, err)
	}
	if len(req.Records) == 0 {
		return nil
	}
	admitted := this.log.version(req.Stream) + tx.stagedCount(req.Stream)
	if req.Expected != admitted {
		return event.Failure(event.Conflict, errStreamMoved)
	}
	if holder := this.log.releaseDeadClaim(req.Stream); holder != nil && holder != tx {
		return event.Failure(event.Conflict, errStreamClaimed)
	}

	envelopes := make([]event.Envelope, 0, len(req.Records))
	for offset, record := range req.Records {
		envelopes = append(envelopes, event.Envelope{
			Stream:     req.Stream,
			Version:    admitted + event.Version(offset) + 1,
			Type:       record.Type,
			Revision:   record.Revision,
			Payload:    bytes.Clone(record.Payload),
			RecordedAt: recorded,
		})
	}
	if tx == nil {
		this.log.publish(envelopes)
		return nil
	}
	tx.stage(req.Stream, envelopes)
	return nil
}
