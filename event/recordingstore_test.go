package event

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	errNotATransaction  = errors.New("recording store: what this context carries for this store is not a transaction")
	errUnreadableCursor = errors.New("recording store: this cursor was not minted here")
)

// The store the caller-seam cases drive: real expected-version admission, real
// paging, and a count per contract method, so a case that says no statement was
// issued asserts it instead of assuming it. Its page defect and its injected
// failure are what let one fixture stand in for a store that lies about a page
// and for one whose backend fails.
type recordingStore struct {
	backing      Backing
	capabilities Capabilities
	limits       Limits

	page      func([]Envelope) []Envelope
	whole     func([]Envelope) []Envelope
	fail      error
	failRead  error
	failWhole error

	closed atomic.Bool

	mutex    sync.Mutex
	streams  map[Stream][]Envelope
	global   []Envelope
	position Position
	calls    map[string]int
}

func newRecordingStore(t *testing.T) *recordingStore {
	t.Helper()
	store := &recordingStore{
		capabilities: Capabilities{
			Transactions:       Supported,
			Persistence:        Unsupported,
			MonotoneVisibility: Supported,
			SharedBacking:      Supported,
		},
		limits:  Limits{MaxPayload: 4 << 10, MaxBatch: 4, MaxKey: 128, StreamPage: 2, MaxRead: 3},
		streams: map[Stream][]Envelope{},
		calls:   map[string]int{},
	}
	backing, err := NewBacking(store)
	if err != nil {
		t.Fatalf("the fixture store cannot name what it writes to, so no case below is about the kernel: %v", err)
	}
	store.backing = backing
	return store
}

func (this *recordingStore) Capabilities() Capabilities {
	this.record("Capabilities")
	return this.capabilities
}

func (this *recordingStore) Limits() Limits {
	this.record("Limits")
	return this.limits
}

func (this *recordingStore) Backing() Backing {
	this.record("Backing")
	return this.backing
}

// Closed, the three operating methods answer the outcome a real store answers
// and Transaction answers what an open one would, so a case about what the
// kernel asks a closed store is about the kernel.
func (this *recordingStore) Close() error {
	this.record("Close")
	this.closed.Store(true)
	return nil
}

func (this *recordingStore) Transaction(ctx context.Context) (Authority, error) {
	this.record("Transaction")
	switch held := ctx.Value(recordingKey{store: this}).(type) {
	case nil:
		return Authority{}, nil
	case *recordingTx:
		return NewAuthority(this.backing, held)
	default:
		return Authority{}, errNotATransaction
	}
}

func (this *recordingStore) ReadStream(ctx context.Context, stream Stream, after Version) ([]Envelope, error) {
	this.record("ReadStream")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if this.closed.Load() {
		return nil, Failure(Closed, nil)
	}
	if this.failRead != nil {
		return nil, this.failRead
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held := this.streams[stream]
	if int(after) >= len(held) {
		return nil, nil
	}
	page := append([]Envelope(nil), held[after:min(len(held), int(after)+this.limits.StreamPage)]...)
	if this.page != nil {
		page = this.page(page)
	}
	return page, nil
}

func (this *recordingStore) ReadAll(ctx context.Context, after Cursor) ([]Envelope, Cursor, error) {
	this.record("ReadAll")
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if this.closed.Load() {
		return nil, "", Failure(Closed, nil)
	}
	if this.failWhole != nil {
		return nil, "", this.failWhole
	}
	from, err := positionOf(after)
	if err != nil {
		return nil, "", Failure(BadCursor, err)
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	page := []Envelope{}
	for _, envelope := range this.global {
		if envelope.Position > from && len(page) < this.limits.MaxRead {
			page = append(page, envelope)
		}
	}
	if len(page) > 0 {
		from = page[len(page)-1].Position
	}
	if this.whole != nil {
		page = this.whole(page)
	}
	return page, Cursor(strconv.FormatUint(uint64(from), 10)), nil
}

func (this *recordingStore) Append(ctx context.Context, req AppendRequest) error {
	this.record("Append")
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.closed.Load() {
		return Failure(Closed, nil)
	}
	if this.fail != nil {
		return this.fail
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if Version(len(this.streams[req.Stream])) != req.Expected {
		return Failure(Conflict, nil)
	}
	recorded := time.Now()
	for offset, record := range req.Records {
		this.position++
		envelope := Envelope{
			Stream:     req.Stream,
			Version:    req.Expected + Version(offset) + 1,
			Position:   this.position,
			Type:       record.Type,
			Revision:   record.Revision,
			Payload:    bytes.Clone(record.Payload),
			RecordedAt: recorded,
		}
		this.streams[req.Stream] = append(this.streams[req.Stream], envelope)
		this.global = append(this.global, envelope)
	}
	return nil
}

func (this *recordingStore) record(method string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.calls[method]++
}

func (this *recordingStore) forget() {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	clear(this.calls)
}

func (this *recordingStore) count(method string) int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.calls[method]
}

func (this *recordingStore) made() int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	total := 0
	for _, count := range this.calls {
		total += count
	}
	return total
}

// The eight the contract names, each asserted against the number the contract
// names for it — a call nobody expected is as much a finding as a missing one,
// and the two that are read once at a door must stay at zero for every operation
// afterwards.
func (this *recordingStore) exactly(t *testing.T, what string, want map[string]int) {
	t.Helper()
	for _, method := range []string{"Capabilities", "Limits", "Backing", "ReadAll", "Transaction", "ReadStream", "Append", "Close"} {
		if got := this.count(method); got != want[method] {
			t.Fatalf("%s called %s %d times where the contract names %d", what, method, got, want[method])
		}
	}
}

// A history no repository wrote: an older deploy's rows, a restored dump, a
// foreign writer. Versions and positions are filled in so the only thing a case
// varies is what it means to vary.
func (this *recordingStore) history(stream Stream, records ...Record) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	envelopes := make([]Envelope, 0, len(records))
	for offset, record := range records {
		this.position++
		envelopes = append(envelopes, Envelope{
			Stream:     stream,
			Version:    Version(offset) + 1,
			Position:   this.position,
			Type:       record.Type,
			Revision:   record.Revision,
			Payload:    record.Payload,
			RecordedAt: time.Now(),
		})
	}
	this.streams[stream] = envelopes
	this.global = append(this.global, envelopes...)
}

type recordingTx struct{ store *recordingStore }

type recordingKey struct{ store *recordingStore }

func withRecordingTransaction(ctx context.Context, tx *recordingTx) context.Context {
	return context.WithValue(ctx, recordingKey{store: tx.store}, tx)
}

func withRecordingExecutor(ctx context.Context, store *recordingStore) context.Context {
	return context.WithValue(ctx, recordingKey{store: store}, "a connection, and not a transaction")
}

func positionOf(cursor Cursor) (Position, error) {
	if cursor == "" {
		return 0, nil
	}
	at, err := strconv.ParseUint(string(cursor), 10, 64)
	if err != nil {
		return 0, errUnreadableCursor
	}
	return Position(at), nil
}

func bindAccounts(t *testing.T, store Store) (*Repo[account, accountID], accountDeclaration) {
	t.Helper()
	declared := declareAccounts(t)
	repo, err := Bind(Open(store), declared.aggregate)
	if err != nil {
		t.Fatalf("the fixture store was refused at Bind, so no case below is about what it was written for: %v", err)
	}
	return repo, declared
}
