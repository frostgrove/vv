package eventtest

import (
	"context"
	"sync"
	"time"

	"github.com/frostgrove/vv/event"
)

// A published number that changes under one store value, which is what a store
// deriving its limits from a live connection or a reloaded configuration does.
// The first call answers what the store publishes, so the comparison across two
// store values holds and only the comparison within one can see this.
type drifting struct {
	over
	mutex sync.Mutex
	taken int
}

func (this *drifting) Limits() event.Limits {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	limits := this.Store.Limits()
	limits.StreamPage += this.taken
	this.taken++
	return limits
}

// A key column narrower than the keys it is given, and a server that truncates
// rather than refusing — a varchar(n) under a non-strict mode, or a driver that
// hashes a key it finds too long. Every read answers the key it was asked for,
// every version is dense, every page is honest, and two identities that render
// two keys with one prefix are one history.
const truncatedKeyBytes = 8

type truncatedKeys struct{ over }

func (this truncatedKeys) Append(ctx context.Context, request event.AppendRequest) error {
	request.Stream = cutTo(request.Stream)
	return this.Store.Append(ctx, request)
}

func (this truncatedKeys) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, cutTo(stream), after)
	for index := range page {
		page[index].Stream = stream
	}
	return page, err
}

func cutTo(stream event.Stream) event.Stream {
	if len(stream.Key) <= truncatedKeyBytes {
		return stream
	}
	return event.Stream{Family: stream.Family, Key: stream.Key[:truncatedKeyBytes]}
}

// The column a migration added after the insert path was written, or a select
// list it was left out of: every other clause holds, the instant orders nothing,
// and every consumer that displays, exports or audits one reads the zero time.
type unrecorded struct{ over }

func (this unrecorded) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	return blanked(page), err
}

func (this unrecorded) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	return blanked(page), cursor, err
}

func blanked(page []event.Envelope) []event.Envelope {
	for index := range page {
		page[index].RecordedAt = time.Time{}
	}
	return page
}

// A store that decides for itself what version an append lands at, by reading
// the stream and rewriting the expectation the caller decided from — which is
// what an upsert keyed on the stream is. One writer never sees it: every append
// it issues is at the version the read answers anyway. Eight that loaded one
// version are all admitted, and each of them decided from a state with none of
// the others in it.
type lastWriteWins struct {
	over
	mutex sync.Mutex
}

func (this *lastWriteWins) Append(ctx context.Context, request event.AppendRequest) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	at, err := this.height(ctx, request.Stream)
	if err != nil {
		return err
	}
	request.Expected = at
	return this.Store.Append(ctx, request)
}

func (this *lastWriteWins) height(ctx context.Context, stream event.Stream) (event.Version, error) {
	page := this.Store.Limits().StreamPage
	var at event.Version
	for {
		read, err := this.Store.ReadStream(ctx, stream, at)
		if err != nil {
			return 0, err
		}
		at += event.Version(len(read))
		if len(read) < page {
			return at, nil
		}
	}
}

// A read cache one store value fills and nothing invalidates, which is what a
// value holding its own snapshot or its own statement cache has. Everything this
// value writes it reads back, so nothing but a second value over the same
// backing writing past it can be seen — and that is what a shared backing is.
type reading struct {
	stream event.Stream
	after  event.Version
}

type cachedStreams struct {
	over
	mutex sync.Mutex
	pages map[reading][]event.Envelope
}

func (this *cachedStreams) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	at := reading{stream: stream, after: after}
	if held, cached := this.pages[at]; cached {
		return clonePage(held), nil
	}
	page, err := this.Store.ReadStream(ctx, stream, after)
	if err != nil {
		return nil, err
	}
	this.pages[at] = clonePage(page)
	return page, nil
}
