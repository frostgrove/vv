package eventtest

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/frostgrove/vv/event"
)

// One inventory of the defects this suite is built to detect, in code, because a
// count kept in prose is what drifts: a test iterates this slice, runs the
// section each row names against the store that row describes, and fails when a
// section passes.
//
// Fourteen of the eighteen are decorators over a store that is otherwise
// correct. Four are stores rather than decorators, and that is not a
// preference: ignoring the expected version by forwarding would mean reading
// the stream to rewrite the version an append was decided at, keeping a
// rolled-back transaction's events readable by forwarding would mean serving an
// AppendRequest's records from a decorator's own memory, a second store value
// that has none of what the first wrote is a property of what a factory builds
// rather than of a call, and reading from the beginning for a cursor that did
// not parse is a decision inside the store's own parser, which a decorator that
// forwards cannot reach without knowing the store's cursor format. All four are
// things a decorator that claims to forward is forbidden, and a fixture that
// claims to be a broken store is not.
type defect struct {
	name    string
	section string
	over    func(event.Store) event.Store
}

func defects() []defect {
	return []defect{
		{"reuses a position it has already issued", "global order",
			func(store event.Store) event.Store { return reusedPositions{over{store}} }},
		{"returns a short stream page while more events exist", "stream paging",
			func(store event.Store) event.Store { return shortPages{over{store}} }},
		{"returns a page whose versions are out of order", "stream paging",
			func(store event.Store) event.Store { return misOrderedPages{over{store}} }},
		{"answers a stream read with another stream's event", "stream paging",
			func(store event.Store) event.Store { return foreignPages{over{store}} }},
		{"returns its newest position as a cursor while a lower one can still commit", "resumption",
			func(store event.Store) event.Store { return unsafeCursors{over{store}} }},
		{"publishes a stream page shorter than the page it returns", "bounds",
			func(store event.Store) event.Store { return publishesAShorterPage(store) }},
		{"publishes limits whose factors are legal and whose read product is not", "binding",
			func(store event.Store) event.Store { return illegalProduct(store) }},
		{"writes into an AppendRequest's Record.Payload after the forwarded call returns", "payload ownership",
			func(store event.Store) event.Store { return writesTheInput{over{store}} }},
		{"hands out one pooled payload buffer and one pooled envelope slice per page", "payload ownership",
			func(store event.Store) event.Store { return &pooledPages{over: over{store}} }},
		{"hands out sub-slices of one fresh page buffer without cutting their capacity", "payload ownership",
			func(store event.Store) event.Store { return packedPages{over{store}} }},
		{"answers every page of a stream out of one array it keeps", "payload ownership",
			func(store event.Store) event.Store {
				return &retainedPages{over: over{store}, whole: map[event.Stream][]event.Envelope{}}
			}},
		{"forwards the append and then reports that it refused rather than tried", "refusal classes",
			func(store event.Store) event.Store { return refusesWhatItWrote{over{store}} }},
		{"answers every envelope with no recorded instant at all", "dense versions",
			func(store event.Store) event.Store { return unrecorded{over{store}} }},
		{"publishes one number on one call to a store value and another on the next", "binding",
			func(store event.Store) event.Store { return &drifting{over: over{store}} }},
		{"ignores AppendRequest.Expected and admits every append", "expected version", nil},
		{"leaves a rolled-back transaction's events readable", "transactions", nil},
		{"claims persistence and builds a second value over its backing that has none of what the first wrote", "durability", nil},
		{"reads from the beginning of its log for a cursor it could not parse", "resumption", nil},
	}
}

// A store that publishes a page it does not keep to, which is the number the
// kernel verifies on the store's behalf because only the store issues the read.
func publishesAShorterPage(store event.Store) event.Store {
	limits := store.Limits()
	limits.StreamPage = 1
	return relimited{over{store}, limits}
}

// Each factor is inside its own ceiling and the product is not: a page of this
// many envelopes at this payload bound is more than one read may hold resident.
func illegalProduct(store event.Store) event.Store {
	limits := store.Limits()
	limits.MaxPayload = event.MaxPayloadBytes
	limits.StreamPage = event.ResidentPage(event.MaxPayloadBytes) + 1
	return relimited{over{store}, limits}
}

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

type unsafeCursors struct{ over }

func (this unsafeCursors) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	page, cursor, err := this.Store.ReadAll(ctx, after)
	if err != nil || len(page) < 2 {
		return page, cursor, err
	}
	return page[:len(page)/2], cursor, nil
}

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

var errWroteAndRefused = errors.New("eventtest: a decorator that forwarded an append and then reported that it refused rather than tried")

type refusesWhatItWrote struct{ over }

func (this refusesWhatItWrote) Append(ctx context.Context, request event.AppendRequest) error {
	if err := this.Store.Append(ctx, request); err != nil {
		return err
	}
	return event.Failure(event.Refused, errWroteAndRefused)
}
