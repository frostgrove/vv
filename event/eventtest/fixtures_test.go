package eventtest_test

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventtest"
)

// The three fixtures live here, in a third package, and that is the whole of
// §INV-019's compile-time half: a store is built out of the exported vocabulary
// alone — a struct with exported fields, a defined string, an exported constant,
// an error from event.Failure and a backing with an exported minting constructor
// — and if any value the contract makes a store return needed event-internal
// access, this file would not compile.

const (
	fixturePayload = 4 << 10
	fixtureBatch   = 16
	fixtureKey     = 256
	fixturePage    = 8
	fixtureRead    = 5
)

var (
	errFixture          = errors.New("eventtest_test: this fixture was asked to fail")
	errCursorForeign    = errors.New("eventtest_test: this cursor was minted over another log")
	errCursorUnparsable = errors.New("eventtest_test: this cursor is not one this fixture mints")
	unparsableCursor    = event.Cursor("neither this fixture's format nor anybody's")
)

type held struct {
	mutex    sync.Mutex
	streams  map[event.Stream][]event.Envelope
	global   []event.Envelope
	position event.Position
	live     []*stagingTx
	minter   string
}

func (this *held) remember(tx *stagingTx) {
	for _, known := range this.live {
		if known == tx {
			return
		}
	}
	this.live = append(this.live, tx)
}

func (this *held) liveCount() int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return len(this.live)
}

func (this *held) forget(tx *stagingTx) {
	kept := this.live[:0]
	for _, known := range this.live {
		if known != tx {
			kept = append(kept, known)
		}
	}
	this.live = kept
}

func newHeld() *held {
	return &held{streams: map[event.Stream][]event.Envelope{}, minter: strconv.FormatInt(time.Now().UnixNano(), 36)}
}

func (this *held) publish(envelopes []event.Envelope) {
	for _, envelope := range envelopes {
		this.position++
		envelope.Position = this.position
		this.streams[envelope.Stream] = append(this.streams[envelope.Stream], envelope)
		this.global = append(this.global, envelope)
	}
}

func (this *held) page(stream event.Stream, after event.Version, staged []event.Envelope, cap int) []event.Envelope {
	whole := append(append([]event.Envelope{}, this.streams[stream]...), staged...)
	if int(after) >= len(whole) {
		return nil
	}
	whole = whole[after:]
	page := make([]event.Envelope, 0, min(len(whole), cap))
	for _, envelope := range whole[:min(len(whole), cap)] {
		page = append(page, handOut(envelope))
	}
	return page
}

func (this *held) walk(from event.Position, cap int) []event.Envelope {
	start := sort.Search(len(this.global), func(index int) bool { return this.global[index].Position > from })
	end := min(start+cap, len(this.global))
	page := make([]event.Envelope, 0, end-start)
	for _, envelope := range this.global[start:end] {
		page = append(page, handOut(envelope))
	}
	return page
}

func (this *held) end() event.Cursor {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.cursor(this.position)
}

// One more event of somebody else's, at the end of the log, between one read and
// the next.
func (this *held) elsewhere() {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.publish([]event.Envelope{{
		Stream:     event.Stream{Family: "eventtest.elsewhere", Key: event.Key("somebody/" + strconv.FormatUint(uint64(this.position)+1, 10))},
		Version:    1,
		Type:       "eventtest.elsewhere.happened",
		Payload:    []byte("{}"),
		RecordedAt: time.Now(),
	}})
}

func (this *held) cursor(at event.Position) event.Cursor {
	return event.Cursor(this.minter + ":" + strconv.FormatUint(uint64(at), 10))
}

// Two ways a cursor can be wrong and only one of them is a failure to parse: one
// another log minted reads cleanly and names somebody else, while one that is
// not this format at all cannot be read. The lenient instantiation keeps the
// first refusal and drops the second, which is a store whose parser ignores its
// own error and starts the walk again at the beginning.
func (this *held) read(cursor event.Cursor) (event.Position, error) {
	if cursor == "" {
		return 0, nil
	}
	minter, at, cut := strings.Cut(string(cursor), ":")
	if !cut {
		return 0, errCursorUnparsable
	}
	position, err := strconv.ParseUint(at, 10, 64)
	if err != nil {
		return 0, errCursorUnparsable
	}
	if minter != this.minter {
		return 0, errCursorForeign
	}
	return event.Position(position), nil
}

func parsed(held *held, cursor event.Cursor, lenient bool) (event.Position, error) {
	from, err := held.read(cursor)
	if lenient && errors.Is(err, errCursorUnparsable) {
		return 0, nil
	}
	return from, err
}

// A store retains what it hands out, so it clones it: the page belongs to
// whoever received it, including its capacity, from the moment it is returned.
func handOut(envelope event.Envelope) event.Envelope {
	frozen := bytes.Clone(envelope.Payload)
	envelope.Payload = frozen[:len(frozen):len(frozen)]
	return envelope
}

func limits() event.Limits {
	return event.Limits{
		MaxPayload: fixturePayload,
		MaxBatch:   fixtureBatch,
		MaxKey:     fixtureKey,
		StreamPage: fixturePage,
		MaxRead:    fixtureRead,
	}
}

// The narrowest numbers the kernel admits, and every one of them is legal: one
// change per append, one envelope per page, one per global read, and a MaxKey
// with room for the suite's own names and little else.
func narrowLimits() event.Limits {
	return event.Limits{MaxPayload: fixturePayload, MaxBatch: 1, MaxKey: 40, StreamPage: 1, MaxRead: 1}
}

type armed struct {
	mutex   sync.Mutex
	outcome event.Outcome
	set     bool
}

func (this *armed) arm(outcome event.Outcome) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.outcome, this.set = outcome, true
}

func (this *armed) fires() error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if !this.set {
		return nil
	}
	this.set = false
	return event.Failure(this.outcome, errFixture)
}

// The trivial store: an append-only slice with no transactions, nothing broken,
// and one rule its second instantiation drops.
type sliceStore struct {
	held           *held
	backing        event.Backing
	published      event.Limits
	admitsAnything bool
	closed         bool
	armed          armed
}

func newSliceStore(t *testing.T, admitsAnything bool) *sliceStore {
	t.Helper()
	holding := newHeld()
	backing, err := event.NewBacking(holding)
	if err != nil {
		t.Fatalf("the fixture cannot name what it writes to: %v", err)
	}
	return &sliceStore{held: holding, backing: backing, published: limits(), admitsAnything: admitsAnything}
}

func (this *sliceStore) Capabilities() event.Capabilities {
	return event.Capabilities{
		Transactions:       event.Unsupported,
		Persistence:        event.Unsupported,
		MonotoneVisibility: event.Supported,
		SharedBacking:      event.Unsupported,
	}
}

func (this *sliceStore) Limits() event.Limits { return this.published }

func (this *sliceStore) Backing() event.Backing { return this.backing }

func (this *sliceStore) Transaction(context.Context) (event.Authority, error) {
	return event.Authority{}, nil
}

func (this *sliceStore) Close() error {
	this.held.mutex.Lock()
	defer this.held.mutex.Unlock()
	this.closed = true
	return nil
}

func (this *sliceStore) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	if err := this.ready(ctx); err != nil {
		return nil, err
	}
	this.held.mutex.Lock()
	defer this.held.mutex.Unlock()
	return this.held.page(stream, after, nil, this.published.StreamPage), nil
}

func (this *sliceStore) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	if err := this.ready(ctx); err != nil {
		return nil, "", err
	}
	this.held.mutex.Lock()
	defer this.held.mutex.Unlock()
	from, err := this.held.read(after)
	if err != nil {
		return nil, "", event.Failure(event.BadCursor, err)
	}
	page := this.held.walk(from, this.published.MaxRead)
	if len(page) > 0 {
		from = page[len(page)-1].Position
	}
	return page, this.held.cursor(from), nil
}

func (this *sliceStore) Append(ctx context.Context, request event.AppendRequest) error {
	if err := this.ready(ctx); err != nil {
		return err
	}
	this.held.mutex.Lock()
	defer this.held.mutex.Unlock()
	at := event.Version(len(this.held.streams[request.Stream]))
	if request.Expected != at && !this.admitsAnything {
		return event.Failure(event.Conflict, errFixture)
	}
	this.held.publish(recorded(request, at))
	return nil
}

func (this *sliceStore) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	this.held.mutex.Lock()
	closed := this.closed
	this.held.mutex.Unlock()
	if closed {
		return event.Failure(event.Closed, nil)
	}
	return this.armed.fires()
}

func recorded(request event.AppendRequest, at event.Version) []event.Envelope {
	envelopes := make([]event.Envelope, 0, len(request.Records))
	recorded := time.Now()
	for offset, record := range request.Records {
		envelopes = append(envelopes, event.Envelope{
			Stream:     request.Stream,
			Version:    at + event.Version(offset) + 1,
			Type:       record.Type,
			Revision:   record.Revision,
			Payload:    bytes.Clone(record.Payload),
			RecordedAt: recorded,
		})
	}
	return envelopes
}

// The transaction-capable fixture, and the one whose second instantiation leaves
// a rolled-back transaction's events readable. A store with no transactions
// cannot commit that defect, and the transactions section would not run against
// one at all.
type stagingStore struct {
	held      *held
	backing   event.Backing
	published event.Limits
	leaks     bool
	persists  bool
	lenient   bool
	closed    bool
	armed     armed
}

type stagingTx struct {
	store   *stagingStore
	staged  []event.Envelope
	counts  map[event.Stream]int
	claimed map[event.Stream]bool
	done    atomic.Bool
}

type stagingKey struct{ held *held }

func newStagingStore(t *testing.T, leaks bool, published event.Limits) *stagingStore {
	t.Helper()
	holding := newHeld()
	backing, err := event.NewBacking(holding)
	if err != nil {
		t.Fatalf("the fixture cannot name what it writes to: %v", err)
	}
	return &stagingStore{held: holding, backing: backing, published: published, leaks: leaks}
}

func (this *stagingStore) beside() *stagingStore {
	return &stagingStore{held: this.held, backing: this.backing, published: this.published, leaks: this.leaks, persists: this.persists, lenient: this.lenient}
}

func (this *stagingStore) Capabilities() event.Capabilities {
	return event.Capabilities{
		Transactions:       event.Supported,
		Persistence:        support(this.persists),
		MonotoneVisibility: event.Supported,
		SharedBacking:      event.Supported,
	}
}

func (this *stagingStore) Limits() event.Limits { return this.published }

func (this *stagingStore) Backing() event.Backing { return this.backing }

func (this *stagingStore) Close() error {
	this.held.mutex.Lock()
	defer this.held.mutex.Unlock()
	this.closed = true
	return nil
}

func (this *stagingStore) begin() *stagingTx {
	return &stagingTx{store: this, counts: map[event.Stream]int{}, claimed: map[event.Stream]bool{}}
}

func (this *stagingStore) inside(ctx context.Context) *stagingTx {
	tx, _ := ctx.Value(stagingKey{held: this.held}).(*stagingTx)
	return tx
}

func (this *stagingStore) Transaction(ctx context.Context) (event.Authority, error) {
	tx := this.inside(ctx)
	if tx == nil {
		return event.Authority{}, nil
	}
	if tx.done.Load() {
		return event.Authority{}, errors.New("eventtest_test: this transaction has already been committed or rolled back")
	}
	return event.NewAuthority(this.backing, tx)
}

func (this *stagingStore) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	if err := this.ready(ctx); err != nil {
		return nil, err
	}
	this.held.mutex.Lock()
	defer this.held.mutex.Unlock()
	return this.held.page(stream, after, this.inside(ctx).stagedFor(stream), this.published.StreamPage), nil
}

func (this *stagingStore) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	if err := this.ready(ctx); err != nil {
		return nil, "", err
	}
	this.held.mutex.Lock()
	defer this.held.mutex.Unlock()
	from, err := parsed(this.held, after, this.lenient)
	if err != nil {
		return nil, "", event.Failure(event.BadCursor, err)
	}
	page := this.held.walk(from, this.published.MaxRead)
	if len(page) > 0 {
		from = page[len(page)-1].Position
	}
	return page, this.held.cursor(from), nil
}

func (this *stagingStore) Append(ctx context.Context, request event.AppendRequest) error {
	if err := this.ready(ctx); err != nil {
		return err
	}
	this.held.mutex.Lock()
	defer this.held.mutex.Unlock()
	tx := this.inside(ctx)
	at := event.Version(len(this.held.streams[request.Stream])) + tx.stagedCount(request.Stream)
	if request.Expected != at {
		return event.Failure(event.Conflict, errFixture)
	}
	if this.claimedByAnother(request.Stream, tx) {
		return event.Failure(event.Conflict, errFixture)
	}
	envelopes := recorded(request, at)
	if tx == nil {
		this.held.publish(envelopes)
		return nil
	}
	tx.stage(request.Stream, envelopes)
	return nil
}

func (this *stagingStore) claimedByAnother(stream event.Stream, tx *stagingTx) bool {
	for _, live := range this.held.live {
		if live != tx && live.claimed[stream] {
			return true
		}
	}
	return false
}

func (this *stagingStore) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	this.held.mutex.Lock()
	closed := this.closed
	this.held.mutex.Unlock()
	if closed {
		return event.Failure(event.Closed, nil)
	}
	if tx := this.inside(ctx); tx != nil && tx.done.Load() {
		return event.Failure(event.Refused, errors.New("eventtest_test: this transaction has already been committed or rolled back"))
	}
	return this.armed.fires()
}

func (this *stagingTx) Commit(context.Context) error { return this.finish(true) }

func (this *stagingTx) Rollback(context.Context) error { return this.finish(this.store.leaks) }

func (this *stagingTx) finish(publish bool) error {
	this.store.held.mutex.Lock()
	defer this.store.held.mutex.Unlock()
	if !this.done.CompareAndSwap(false, true) {
		return errors.New("eventtest_test: this transaction has already been committed or rolled back")
	}
	if publish {
		this.store.held.publish(this.staged)
	}
	this.staged = nil
	this.store.held.forget(this)
	return nil
}

func (this *stagingTx) stage(stream event.Stream, envelopes []event.Envelope) {
	this.claimed[stream] = true
	this.counts[stream] += len(envelopes)
	this.staged = append(this.staged, envelopes...)
	this.store.held.remember(this)
}

func (this *stagingTx) stagedCount(stream event.Stream) event.Version {
	if this == nil {
		return 0
	}
	return event.Version(this.counts[stream])
}

func (this *stagingTx) stagedFor(stream event.Stream) []event.Envelope {
	if this == nil {
		return nil
	}
	kept := make([]event.Envelope, 0, this.counts[stream])
	for _, envelope := range this.staged {
		if envelope.Stream == stream {
			kept = append(kept, envelope)
		}
	}
	return kept
}

// What a store is handed, watched at the four doors that take a context. A store
// may wait rather than refuse, so the window the section runs under is the only
// thing between a store that waits forever and a binary that go test kills
// naming a goroutine stack instead of a section.
type deadlines struct {
	mutex sync.Mutex
	seen  int
	open  int
}

func (this *deadlines) watch(ctx context.Context) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.seen++
	if _, set := ctx.Deadline(); !set {
		this.open++
	}
}

func (this *deadlines) counts() (seen, open int) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.seen, this.open
}

type timed struct {
	event.Store
	watching *deadlines
}

func (this timed) Transaction(ctx context.Context) (event.Authority, error) {
	this.watching.watch(ctx)
	return this.Store.Transaction(ctx)
}

func (this timed) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	this.watching.watch(ctx)
	return this.Store.ReadStream(ctx, stream, after)
}

func (this timed) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	this.watching.watch(ctx)
	return this.Store.ReadAll(ctx, after)
}

func (this timed) Append(ctx context.Context, request event.AppendRequest) error {
	this.watching.watch(ctx)
	return this.Store.Append(ctx, request)
}

// The store that refuses everything, and the fourth anti-vacuity rule: every
// section carries a control, so every section must fail against it. It is honest
// about its bounds and its capabilities, so nothing here is refused at a door and
// every section runs.
type refusingStore struct{ backing event.Backing }

func newRefusingStore(t *testing.T) *refusingStore {
	t.Helper()
	backing, err := event.NewBacking(new(int))
	if err != nil {
		t.Fatalf("the fixture cannot name what it writes to: %v", err)
	}
	return &refusingStore{backing: backing}
}

func (this *refusingStore) Capabilities() event.Capabilities {
	return event.Capabilities{
		Transactions:       event.Supported,
		Persistence:        event.Supported,
		MonotoneVisibility: event.Supported,
		SharedBacking:      event.Supported,
	}
}

func (this *refusingStore) Limits() event.Limits { return limits() }

func (this *refusingStore) Backing() event.Backing { return this.backing }

func (this *refusingStore) Close() error { return nil }

func (this *refusingStore) Transaction(context.Context) (event.Authority, error) {
	return event.NewAuthority(this.backing, this)
}

func (this *refusingStore) ReadStream(context.Context, event.Stream, event.Version) ([]event.Envelope, error) {
	return nil, event.Failure(event.NotWritten, errFixture)
}

func (this *refusingStore) ReadAll(context.Context, event.Cursor) ([]event.Envelope, event.Cursor, error) {
	return nil, "", event.Failure(event.NotWritten, errFixture)
}

func (this *refusingStore) Append(context.Context, event.AppendRequest) error {
	return event.Failure(event.NotWritten, errFixture)
}

type refusingTx struct{}

func (refusingTx) Commit(context.Context) error   { return nil }
func (refusingTx) Rollback(context.Context) error { return nil }

// A store under a defect's decorator is the value the suite holds and the value
// a hook is handed back, and the contract gives a decorator no way to be walked
// past — every method is required, so the exact outer value answers. So the
// factory remembers what it wrapped rather than unwrapping it.
type fixtures struct {
	mutex sync.Mutex
	inner map[event.Store]event.Store
	over  func(event.Store) event.Store
}

func newFixtures(over func(event.Store) event.Store) *fixtures {
	return &fixtures{inner: map[event.Store]event.Store{}, over: over}
}

func (this *fixtures) wrap(store event.Store) event.Store {
	if this.over == nil {
		return store
	}
	outer := this.over(store)
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.inner[outer] = store
	return outer
}

func (this *fixtures) of(store event.Store) event.Store {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if inner, wrapped := this.inner[store]; wrapped {
		return inner
	}
	return store
}

func sliceFactory(admitsAnything bool, over func(event.Store) event.Store) eventtest.Factory {
	built := newFixtures(over)
	return eventtest.Factory{
		New: func(t *testing.T) event.Store {
			store := newSliceStore(t, admitsAnything)
			t.Cleanup(func() { _ = store.Close() })
			return built.wrap(store)
		},
		Fail: func(t *testing.T, s event.Store, outcome event.Outcome) bool {
			store, is := built.of(s).(*sliceStore)
			if !is {
				t.Fatalf("the suite asked a store this factory did not build to fail: %T", s)
			}
			store.armed.arm(outcome)
			return true
		},
		Unparsable: func(*testing.T, event.Store) event.Cursor { return unparsableCursor },
	}
}

func support(claimed bool) event.Support {
	if claimed {
		return event.Supported
	}
	return event.Unsupported
}

func stagingFactory(leaks bool, over func(event.Store) event.Store) eventtest.Factory {
	return stagingFactoryAt(limits(), leaks, over)
}

func stagingFactoryAt(published event.Limits, leaks bool, over func(event.Store) event.Store) eventtest.Factory {
	built := newFixtures(over)
	return eventtest.Factory{
		New: func(t *testing.T) event.Store {
			store := newStagingStore(t, leaks, published)
			t.Cleanup(func() {
				_ = store.Close()
				if live := store.held.liveCount(); live != 0 {
					t.Errorf("%d transaction(s) this section began were still unresolved when it ended, and one of a store with a connection pool holds a connection and every lock it took into the section after", live)
				}
			})
			return built.wrap(store)
		},
		Begin: func(t *testing.T, ctx context.Context, s event.Store) (context.Context, eventtest.Tx) {
			store, is := built.of(s).(*stagingStore)
			if !is {
				t.Fatalf("the suite asked a store this factory did not build to begin a transaction: %T", s)
			}
			tx := store.begin()
			return context.WithValue(ctx, stagingKey{held: store.held}, tx), tx
		},
		Sibling: func(t *testing.T, s event.Store) event.Store {
			store, is := built.of(s).(*stagingStore)
			if !is {
				t.Fatalf("the suite asked a store this factory did not build for a second value: %T", s)
			}
			return store.beside()
		},
		Fail: func(t *testing.T, s event.Store, outcome event.Outcome) bool {
			store, is := built.of(s).(*stagingStore)
			if !is {
				t.Fatalf("the suite asked a store this factory did not build to fail: %T", s)
			}
			store.armed.arm(outcome)
			return true
		},
		Unparsable: func(*testing.T, event.Store) event.Cursor { return unparsableCursor },
	}
}

// The store nobody's suite starts against: a log that already holds events this
// run did not write, more of them than the walk's own bound used to allow, put
// there by another deployment, another process or this suite's own last run.
func prefilledFactory(events int) eventtest.Factory {
	factory := stagingFactory(false, nil)
	built := factory.New
	factory.New = func(t *testing.T) event.Store {
		store := built(t)
		prefill(store.(*stagingStore), events)
		return store
	}
	return factory
}

func prefill(store *stagingStore, events int) {
	store.held.mutex.Lock()
	defer store.held.mutex.Unlock()
	envelopes := make([]event.Envelope, 0, events)
	for index := range events {
		envelopes = append(envelopes, event.Envelope{
			Stream:     event.Stream{Family: "eventtest.elsewhere", Key: event.Key("somebody/" + strconv.Itoa(index))},
			Version:    1,
			Type:       "eventtest.elsewhere.happened",
			Payload:    []byte("{}"),
			RecordedAt: time.Now(),
		})
	}
	store.held.publish(envelopes)
}

// The store whose cursor parser ignores its own error: a checkpoint it cannot
// read starts the walk at the beginning of the log instead of being refused, so
// a projector whose persisted cursor was truncated silently re-applies every
// event this store ever wrote.
func lenientFactory(lenient bool) eventtest.Factory {
	factory := stagingFactory(false, nil)
	built := factory.New
	factory.New = func(t *testing.T) event.Store {
		store := built(t)
		store.(*stagingStore).lenient = lenient
		return store
	}
	return factory
}

// The same store publishing the narrowest numbers the kernel admits, so a suite
// whose counts are fitted to the numbers above reports a correct store failed.
func narrowFactory() eventtest.Factory {
	return stagingFactoryAt(narrowLimits(), false, nil)
}

func stagingOf(t *testing.T, s event.Store) *stagingStore {
	t.Helper()
	store, is := s.(*stagingStore)
	if !is {
		t.Fatalf("the suite handed back a store this factory did not build: %T", s)
	}
	return store
}

// The one fixture that claims what it holds survives the value that wrote it:
// every store it builds is another value over one log, so the second one a
// section takes is a restart. Its defective instantiation claims the same and
// builds that second value over a log with nothing in it.
func persistentFactory(forgets bool) eventtest.Factory {
	var shared *stagingStore
	return eventtest.Factory{
		New: func(t *testing.T) event.Store {
			if shared == nil {
				shared = newStagingStore(t, false, limits())
				shared.persists = true
			}
			store := shared.beside()
			t.Cleanup(func() { _ = store.Close() })
			return store
		},
		Begin: func(t *testing.T, ctx context.Context, s event.Store) (context.Context, eventtest.Tx) {
			store := stagingOf(t, s)
			tx := store.begin()
			return context.WithValue(ctx, stagingKey{held: store.held}, tx), tx
		},
		Sibling: func(t *testing.T, s event.Store) event.Store {
			if !forgets {
				return stagingOf(t, s).beside()
			}
			empty := newStagingStore(t, false, limits())
			empty.persists, empty.backing = true, stagingOf(t, s).backing
			return empty
		},
		Fail: func(t *testing.T, s event.Store, outcome event.Outcome) bool {
			stagingOf(t, s).armed.arm(outcome)
			return true
		},
		Unparsable: func(*testing.T, event.Store) event.Cursor { return unparsableCursor },
	}
}

// The log another process is appending to, and the factory that answers where
// its end is. A walk that stops on an empty page never stops here, so a section
// that reaches a verdict at all reaches one because what it walks is bounded by
// what it wrote.
func busyFactory() eventtest.Factory {
	factory := stagingFactory(false, func(store event.Store) event.Store {
		return &busy{Store: store, of: store.(*stagingStore)}
	})
	factory.Tail = func(t *testing.T, s event.Store) event.Cursor {
		store, is := s.(*busy)
		if !is {
			t.Fatalf("the suite asked a store this factory did not build for the end of its log: %T", s)
		}
		return store.of.held.end()
	}
	return factory
}

type busy struct {
	event.Store
	of *stagingStore
}

func (this *busy) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	this.of.held.elsewhere()
	return this.Store.ReadAll(ctx, after)
}

func refusingFactory() eventtest.Factory {
	return eventtest.Factory{
		New: func(t *testing.T) event.Store { return newRefusingStore(t) },
		Begin: func(_ *testing.T, ctx context.Context, _ event.Store) (context.Context, eventtest.Tx) {
			return ctx, refusingTx{}
		},
		Sibling: func(t *testing.T, _ event.Store) event.Store { return newRefusingStore(t) },
		Fail:    func(*testing.T, event.Store, event.Outcome) bool { return true },
	}
}
