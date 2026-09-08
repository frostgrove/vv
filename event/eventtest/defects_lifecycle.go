package eventtest

import (
	"context"
	"errors"
	"sync"

	"github.com/frostgrove/vv/event"
)

var (
	errWroteAndRefused = errors.New("eventtest: a decorator that forwarded an append and then reported that it refused rather than tried")
	errClosedAlready   = errors.New("eventtest: a store that answers the second close of one value with an error of its own")
)

type refusesWhatItWrote struct{ over }

func (this refusesWhatItWrote) Append(ctx context.Context, request event.AppendRequest) error {
	if err := this.Store.Append(ctx, request); err != nil {
		return err
	}
	return event.Failure(event.Refused, errWroteAndRefused)
}

// A store that cannot tell a statement it never issued from one nobody
// confirmed, and reports both as the second — which is what a driver answering
// "context canceled" out of its own pool looks like to a store that guesses.
// The caller that reads an uncertainty re-reads the stream and decides again
// over a write that was never made.
type uncertainWhenCancelled struct{ over }

func (this uncertainWhenCancelled) Append(ctx context.Context, request event.AppendRequest) error {
	if err := ctx.Err(); err != nil {
		return event.Failure(event.Unconfirmed, err)
	}
	return this.Store.Append(ctx, request)
}

// The second close, which a composition root shutting down twice makes and a
// pool that has already released its connections answers for.
type closesOnce struct {
	over
	mutex  sync.Mutex
	closed bool
}

func (this *closesOnce) Close() error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.closed {
		return errClosedAlready
	}
	this.closed = true
	return this.Store.Close()
}

// A store that hands its driver's error back without saying what it means. It is
// the most ordinary store defect there is, and the one no reader of an error
// message can recover from: the classification is the store's own half of the
// division of labour, and a kernel that guessed at it would be inferring a
// conflict from a driver code it does not import.
type unclassifying struct{ over }

func (this unclassifying) Append(ctx context.Context, request event.AppendRequest) error {
	return stripped(this.Store.Append(ctx, request))
}

func (this unclassifying) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	return page, stripped(err)
}

func (this unclassifying) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	return page, cursor, stripped(err)
}

func stripped(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(err.Error())
}
