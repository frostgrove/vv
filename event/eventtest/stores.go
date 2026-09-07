package eventtest

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/event"
)

// The store the suite builds around the store under test. Every method of the
// contract is required, so an embedded event.Store forwards all eight and a
// decorator that overrides one is still the exact value the kernel asks
// everything of.
type over struct{ event.Store }

// A page the store under test filled, cut to a page the suite chose. It is what
// lets the paging sections drive a stream through several pages without writing
// a page's worth of events, and it is honest where a store that published a
// smaller page and returned a longer one would not be: the number it publishes
// is the number it returns.
type narrowed struct {
	over
	page int
}

func (this narrowed) Limits() event.Limits {
	limits := this.Store.Limits()
	limits.StreamPage = this.page
	return limits
}

func (this narrowed) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	if err != nil {
		return nil, err
	}
	return page[:min(len(page), this.page)], nil
}

type relimited struct {
	over
	limits event.Limits
}

func (this relimited) Limits() event.Limits { return this.limits }

type restated struct {
	over
	capabilities event.Capabilities
}

func (this restated) Capabilities() event.Capabilities { return this.capabilities }

type rebacked struct {
	over
	backing event.Backing
}

func (this rebacked) Backing() event.Backing { return this.backing }

// A policing decorator, and the one whose refusal the kernel has to carry
// unchanged: it refuses before forwarding, so nothing reached the backing, and
// what it returns is the whole of what it wants the caller to be told.
type policing struct {
	over
	refusal error
}

func (this policing) Append(context.Context, event.AppendRequest) error { return this.refusal }

// The commit window a store with none of its own cannot produce: the append is
// issued and the answer is that nobody confirmed it. It is not a defect — the
// kernel's obligation is to report the uncertainty and to let no cancellation
// out through it.
type unconfirming struct{ over }

func (this unconfirming) Append(ctx context.Context, request event.AppendRequest) error {
	if err := this.Store.Append(ctx, request); err != nil {
		return err
	}
	return event.Failure(event.Unconfirmed, context.Canceled)
}

var errDecoratorContext = errors.New("eventtest: a decorator's own note about the operation it forwarded")

// The decorator a store's failure has to survive: the kernel finds a
// classification through the wrapping and never by a type assertion, so a
// sentinel that changes here is a kernel that walked past the store that
// classified.
type wrapping struct{ over }

func (this wrapping) Append(ctx context.Context, request event.AppendRequest) error {
	return wrapped(this.Store.Append(ctx, request))
}

func (this wrapping) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	return page, wrapped(err)
}

func (this wrapping) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	return page, cursor, wrapped(err)
}

func wrapped(err error) error {
	if err == nil {
		return nil
	}
	return errors.Join(errDecoratorContext, err)
}

func withPayload(envelope event.Envelope, payload []byte) event.Envelope {
	envelope.Payload = payload
	return envelope
}
