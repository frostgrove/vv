package eventtest

import (
	"bytes"
	"context"
	"sync"

	"github.com/frostgrove/vv/event"
)

type writesTheInput struct{ over }

func (this writesTheInput) Append(ctx context.Context, request event.AppendRequest) error {
	err := this.Store.Append(ctx, request)
	for _, record := range request.Records {
		for index := range record.Payload {
			record.Payload[index] = 'z'
		}
	}
	return err
}

// One buffer for every payload of a page and one slice for every page, refilled
// per read. What it returns is right for the page it returns and wrong for the
// page before it, which is the shape a driver accessor valid only until the next
// fetch has.
type pooledPages struct {
	over
	page    []event.Envelope
	payload []byte
}

func (this *pooledPages) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	if err != nil {
		return nil, err
	}
	return this.pool(page), nil
}

func (this *pooledPages) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	if err != nil {
		return nil, "", err
	}
	return this.pool(page), cursor, nil
}

func (this *pooledPages) pool(page []event.Envelope) []event.Envelope {
	total := 0
	for _, envelope := range page {
		total += len(envelope.Payload)
	}
	if cap(this.payload) < total {
		this.payload = make([]byte, total)
	}
	if cap(this.page) < len(page) {
		this.page = make([]event.Envelope, len(page))
	}
	this.payload, this.page = this.payload[:total], this.page[:len(page)]
	at := 0
	for index, envelope := range page {
		copy(this.payload[at:], envelope.Payload)
		this.page[index] = withPayload(envelope, this.payload[at:at+len(envelope.Payload)])
		at += len(envelope.Payload)
	}
	return this.page
}

// A buffer per read, freshly allocated and shared by nothing, sub-sliced without
// a third index — which is the ordinary shape for a driver that scans a page
// into one row buffer. Every clause about pooling holds and every byte is right
// until a consumer appends to a payload, and the byte the append spills lands on
// the next event's first one.
type packedPages struct{ over }

func (this packedPages) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	page, err := this.Store.ReadStream(ctx, stream, after)
	if err != nil {
		return nil, err
	}
	return packed(page), nil
}

func (this packedPages) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	if err != nil {
		return nil, "", err
	}
	return packed(page), cursor, nil
}

func packed(page []event.Envelope) []event.Envelope {
	total := 0
	for _, envelope := range page {
		total += len(envelope.Payload)
	}
	buffer := make([]byte, 0, total)
	held := make([]event.Envelope, 0, len(page))
	for _, envelope := range page {
		at := len(buffer)
		buffer = append(buffer, envelope.Payload...)
		held = append(held, withPayload(envelope, buffer[at:]))
	}
	return held
}

// A stream read once and kept, with every later page cut out of the array it was
// kept in and no third index on the cut — which is what a driver-side cache of a
// whole stream looks like. The payloads are cloned into that array on every read,
// so every clause about bytes holds; what a consumer's append to a page lands on
// is the first row of the page after it, and this store serves that row again.
type retainedPages struct {
	over
	mutex sync.Mutex
	whole map[event.Stream][]event.Envelope
}

func (this *retainedPages) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	fresh, err := this.Store.ReadStream(ctx, stream, after)
	if err != nil {
		return nil, err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held := this.whole[stream]
	if int(after) > len(held) {
		return fresh, nil
	}
	for int(after)+len(fresh) > len(held) {
		held = append(held, fresh[len(held)-int(after)])
	}
	this.whole[stream] = held
	page := held[after : int(after)+len(fresh)]
	for index := range page {
		page[index].Payload = bytes.Clone(fresh[index].Payload)
	}
	return page, nil
}
