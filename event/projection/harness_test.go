package projection_test

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
	"github.com/frostgrove/vv/runtime"
)

const settle = 5 * time.Second

// Every wait in the loop is driven rather than slept through: the projection
// asks for an interval and blocks, this records the interval and hands the test
// the moment the loop is inside its select. One beat channel serves every ticker
// because the loop waits on one at a time, so an unbuffered send is also the
// synchronisation point a test needs.
type ticks struct {
	asked chan time.Duration
	beat  chan time.Time
}

func newTicks() *ticks {
	return &ticks{asked: make(chan time.Duration, 1024), beat: make(chan time.Time)}
}

func (this *ticks) Ticks(interval time.Duration) runtime.Ticker {
	select {
	case this.asked <- interval:
	default:
	}
	return beating{beat: this.beat}
}

type beating struct{ beat chan time.Time }

func (this beating) Ticks() <-chan time.Time { return this.beat }

func (this beating) Stop() {}

func (this *ticks) waitedFor(t *testing.T) time.Duration {
	t.Helper()
	select {
	case interval := <-this.asked:
		return interval
	case <-time.After(settle):
		t.Fatal("the projection never asked for an interval to wait")
		return 0
	}
}

func (this *ticks) expect(t *testing.T, want time.Duration, what string) {
	t.Helper()
	if asked := this.waitedFor(t); asked != want {
		t.Fatalf("%s waited %s where the spec says %s", what, asked, want)
	}
}

func (this *ticks) fire(t *testing.T) {
	t.Helper()
	select {
	case this.beat <- time.Now():
	case <-time.After(settle):
		t.Fatal("the projection was not waiting on a ticker, so nothing released it")
	}
}

func (this *ticks) quiet(t *testing.T, window time.Duration) {
	t.Helper()
	select {
	case interval := <-this.asked:
		t.Fatalf("the projection asked to wait %s, and over this window it was to ask for nothing at all", interval)
	case <-time.After(window):
	}
}

// A log the projection reads through, counting what arrives and letting a case
// answer in its place. It promotes event.Log alone, so it is not a value the
// projection could append through.
type watchedLog struct {
	event.Log

	reads   atomic.Int64
	arrived func(ctx context.Context)
	answer  func(ctx context.Context, after event.Cursor, reads int64) ([]event.Envelope, event.Cursor, error)
}

func watching(log event.Log) *watchedLog { return &watchedLog{Log: log} }

func (this *watchedLog) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	reads := this.reads.Add(1)
	if this.arrived != nil {
		this.arrived(ctx)
	}
	if this.answer != nil {
		return this.answer(ctx, after, reads)
	}
	return this.Log.ReadAll(ctx, after)
}

type watchedCheckpoints struct {
	event.Checkpoints

	loads   atomic.Int64
	saves   atomic.Int64
	forgets atomic.Int64

	transactions event.Support
	onLoad       func(held event.Checkpoint, loads int64) (event.Checkpoint, error)
	onSave       func(checkpoint event.Checkpoint, saves int64) (bool, error)
}

func recording(checkpoints event.Checkpoints) *watchedCheckpoints {
	return &watchedCheckpoints{Checkpoints: checkpoints}
}

func (this *watchedCheckpoints) Capabilities() event.CheckpointCapabilities {
	held := this.Checkpoints.Capabilities()
	if this.transactions != event.Unstated {
		held.Transactions = this.transactions
	}
	return held
}

func (this *watchedCheckpoints) Load(ctx context.Context, projection string) (event.Checkpoint, error) {
	loads := this.loads.Add(1)
	held, err := this.Checkpoints.Load(ctx, projection)
	if err != nil || this.onLoad == nil {
		return held, err
	}
	return this.onLoad(held, loads)
}

// A hook that answers false has taken the save itself, so a case can refuse one
// without the row moving underneath it.
func (this *watchedCheckpoints) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	saves := this.saves.Add(1)
	if this.onSave != nil {
		issue, err := this.onSave(checkpoint, saves)
		if !issue {
			return err
		}
	}
	return this.Checkpoints.Save(ctx, checkpoint)
}

func (this *watchedCheckpoints) Forget(ctx context.Context, projection string) error {
	this.forgets.Add(1)
	return this.Checkpoints.Forget(ctx, projection)
}

// A transaction value of the read model's, standing where a *sql.Tx stands in
// every wiring that has a database: comparable, non-nil, and one per resource.
type transaction struct{ named string }

// The read model's data source, and what Spec.Destination names. It is not a
// transaction, which is what crud requires of the value a session is opened
// against.
type readModel struct{ pool *stand }

func (this readModel) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, nil
}

func (this readModel) Query(context.Context, string, ...any) (crud.Rows, error) { return nil, nil }

func (this readModel) Dialect() crud.Dialect { return crud.Postgres{} }

func (this readModel) DataSource() any { return this.pool }

// What a unit binds for the read model, and what crud.KeyOf answers a *sql.Tx
// for in every wiring that has one. InTransaction is the one call the pass's
// alignment makes of it and nothing else in crud makes of an executor, so
// recording it is how a case says where in the pass the comparison happened.
type readModelTx struct {
	identity any
	asked    func()
}

func (this readModelTx) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, nil
}

func (this readModelTx) Query(context.Context, string, ...any) (crud.Rows, error) {
	return nil, nil
}

func (this readModelTx) DataSource() any { return this.identity }

func (this readModelTx) InTransaction() bool {
	if this.asked != nil {
		this.asked()
	}
	return true
}

// The one seam every InUnit case with a resolvable destination is built on.
// eventmemory cannot give a test tier A on its own: its checkpoint authority is
// minted over an unexported identity and no destination's key can ever equal one
// (crud.SameDataSource refuses two values of different types outright). So the
// authority is re-minted over a value the test chose and the unit binds an
// executor naming a value the test chose, and what the pass compares is two
// halves a case can hand one value to or two.
type alignedCheckpoints struct {
	event.Checkpoints

	identity *transaction
}

func (this alignedCheckpoints) Transaction(ctx context.Context) (event.Authority, error) {
	held, err := this.Checkpoints.Transaction(ctx)
	if err != nil || !held.Valid() {
		return held, err
	}
	return event.NewAuthority(this.Checkpoints.Backing(), this.identity)
}

// One transaction for both resources when store and destination are one value,
// and two when they are not, which is the whole of the difference between tier A
// and tier B. Everything else about the two wirings is identical.
//
// The park's writes are staged in the same unit as the advance and land only
// when it commits, which is what a queue in the application's own database is. A
// stand whose rollback left the letter behind could not tell a park that commits
// with the read model from one that does not.
func (this *stand) inUnit(spec projection.Spec, store, destination *transaction, asked func()) projection.Spec {
	pool := readModel{pool: this}
	spec.Advance = projection.InUnit
	spec.Checkpoints = alignedCheckpoints{Checkpoints: this.points, identity: store}
	spec.Destination = pool
	spec.Unit = this.opening(pool, destination, asked)
	return spec
}

// The unit of work both halves run inside — the loop's pass and the operator's
// redrive — because both owe the same three resources one transaction.
func (this *stand) opening(pool readModel, destination *transaction, asked func()) func(context.Context, func(context.Context) error) error {
	return func(ctx context.Context, work func(context.Context) error) error {
		tx, err := this.checkpoints.Begin(ctx)
		if err != nil {
			return err
		}
		opened := this.park.opened()
		inner := crud.BindExecutor(withUnitOfWork(eventmemory.WithTransaction(ctx, tx), opened), pool, readModelTx{identity: destination, asked: asked})
		if err := work(inner); err != nil {
			_ = tx.Rollback(ctx)
			this.park.discard(opened)
			return err
		}
		if this.interrupted != nil {
			_ = tx.Rollback(ctx)
			this.park.discard(opened)
			return this.interrupted
		}
		if err := tx.Commit(ctx); err != nil {
			this.park.discard(opened)
			return err
		}
		this.park.commit(opened)
		opened.land()
		return nil
	}
}

// What an operator's redrive runs inside, which is the same unit one pass runs
// inside and is opened by the operator's own goroutine rather than by the loop's.
func (this *stand) operating(destination *transaction) func(context.Context, func(context.Context) error) error {
	return this.opening(readModel{pool: this}, destination, nil)
}

// A unit of work over the checkpoint store's own transaction, which is what
// crud.InNewTx is in a wiring with a database and what a split is performed
// inside. It runs the work once.
func (this *stand) unit(ctx context.Context, work func(context.Context) error) error {
	tx, err := this.checkpoints.Begin(ctx)
	if err != nil {
		return err
	}
	if err := work(eventmemory.WithTransaction(ctx, tx)); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

type observed struct {
	mutex  sync.Mutex
	states []projection.State
	seen   chan projection.State
}

func newObserved() *observed { return &observed{seen: make(chan projection.State, 1024)} }

func (this *observed) Observed(state projection.State) {
	this.mutex.Lock()
	this.states = append(this.states, state)
	this.mutex.Unlock()
	select {
	case this.seen <- state:
	default:
	}
}

// The whole history is re-read on every wakeup rather than the channel being
// the record, so two cases waiting for one state both see it and a case that
// asks after the fact is not left waiting for a state that has already been
// published.
func (this *observed) await(t *testing.T, what string, holds func(projection.State) bool) projection.State {
	t.Helper()
	deadline := time.After(settle)
	for {
		if state, found := this.found(holds); found {
			return state
		}
		select {
		case <-this.seen:
		case <-deadline:
			t.Fatalf("the projection never published a state where %s: it published %v", what, this.phases())
			return projection.State{}
		}
	}
}

func (this *observed) found(holds func(projection.State) bool) (projection.State, bool) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	for index := len(this.states) - 1; index >= 0; index-- {
		if holds(this.states[index]) {
			return this.states[index], true
		}
	}
	return projection.State{}, false
}

func (this *observed) phases() []projection.Phase {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held := make([]projection.Phase, 0, len(this.states))
	for _, state := range this.states {
		held = append(held, state.Phase)
	}
	return held
}

func (this *observed) counted(phase projection.Phase) int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	count := 0
	for _, state := range this.states {
		if state.Phase == phase {
			count++
		}
	}
	return count
}

func (this *observed) published() int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return len(this.states)
}

// The read model every case writes to, and the value the InUnit cases stage
// their writes into so a rollback can discard them.
type model struct {
	mutex   sync.Mutex
	applied []string
}

func (this *model) write(rows ...string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.applied = append(this.applied, rows...)
}

func (this *model) rows() []string {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return append([]string(nil), this.applied...)
}

func newLog(t testing.TB) *eventmemory.Log {
	t.Helper()
	log, err := eventmemory.NewLog(eventmemory.LogSpec{})
	if err != nil {
		t.Fatalf("a log was refused: %v", err)
	}
	return log
}

func newStore(t testing.TB, log *eventmemory.Log, spec eventmemory.Spec) *eventmemory.Store {
	t.Helper()
	spec.Log = log
	store, err := eventmemory.New(spec)
	if err != nil {
		t.Fatalf("a store over %+v was refused: %v", spec, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newCheckpoints(t testing.TB, log *eventmemory.Log) *eventmemory.Checkpoints {
	t.Helper()
	held, err := eventmemory.NewCheckpoints(eventmemory.CheckpointSpec{Log: log})
	if err != nil {
		t.Fatalf("a checkpoint store was refused: %v", err)
	}
	t.Cleanup(func() { _ = held.Close() })
	return held
}

// One log, one store over it, one checkpoint store over it, and the decorators
// every case reaches for.
type stand struct {
	log         *eventmemory.Log
	store       *eventmemory.Store
	read        *watchedLog
	checkpoints *eventmemory.Checkpoints
	points      *watchedCheckpoints
	ticks       *ticks
	observer    *observed
	model       *model
	park        *park
	versions    map[event.Stream]event.Version

	// What a unit of work answers instead of committing, for the cases that have
	// to see a body that ran and a transaction that did not: the letter, the read
	// model and the advance go back together, or the queue is not what it says.
	interrupted error
}

func newStand(t *testing.T, spec eventmemory.Spec) *stand {
	t.Helper()
	log := newLog(t)
	store := newStore(t, log, spec)
	checkpoints := newCheckpoints(t, log)
	return &stand{
		log:         log,
		store:       store,
		read:        watching(event.ReadOnly(store)),
		checkpoints: checkpoints,
		points:      recording(checkpoints),
		ticks:       newTicks(),
		observer:    newObserved(),
		model:       &model{},
		versions:    map[event.Stream]event.Version{},
	}
}

// The spec every case starts from, with the two seams a test drives already
// wired: nothing here is a default the library supplies.
func (this *stand) spec(name string, handler projection.Handler) projection.Spec {
	return projection.Spec{
		Name:        name,
		Log:         this.read,
		Checkpoints: this.points,
		Handler:     handler,
		Ticks:       this.ticks.Ticks,
		Observer:    this.observer,
	}
}

func (this *stand) append(t *testing.T, key string, payloads ...string) event.Stream {
	t.Helper()
	stream := event.Stream{Family: "orders", Key: event.Key(key)}
	records := make([]event.Record, 0, len(payloads))
	for _, payload := range payloads {
		records = append(records, event.Record{Type: "orders.placed", Revision: 1, Payload: []byte(payload)})
	}
	request := event.AppendRequest{Stream: stream, Expected: this.versions[stream], Records: records}
	if err := this.store.Append(context.Background(), request); err != nil {
		t.Fatalf("appending %v to %s was refused: %v", payloads, stream, err)
	}
	this.versions[stream] += event.Version(len(records))
	return stream
}

func (this *stand) row(t *testing.T, name string) event.Checkpoint {
	t.Helper()
	held, err := this.checkpoints.Load(context.Background(), name)
	if err != nil {
		t.Fatalf("reading the checkpoint row of %q answered %v", name, err)
	}
	return held
}

func newProjection(t *testing.T, spec projection.Spec) *projection.Projection {
	t.Helper()
	built, err := projection.New(spec)
	if err != nil {
		t.Fatalf("a well-formed projection was refused: %v", err)
	}
	return built
}

// Run on a goroutine of the test's, which is where a supervisor would put it,
// and every case waits for it to return rather than leaving it behind.
func running(t *testing.T, held *projection.Projection) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		returned <- held.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(settle):
			t.Error("Run did not return after its context was cancelled")
		}
	})
	return cancel, returned
}

func returns(t *testing.T, returned <-chan error) error {
	t.Helper()
	select {
	case err := <-returned:
		return err
	case <-time.After(settle):
		t.Fatal("Run did not return")
		return nil
	}
}

// The store as a wait's mint reads through it: every ReadStream counted, and a
// case may answer in its place. It promotes event.Store, so a repository can be
// bound to the store underneath while the mint reads through this one and the
// count is the mint's alone.
type watchedStore struct {
	event.Store

	reads  atomic.Int64
	answer func(page []event.Envelope, reads int64) ([]event.Envelope, error)

	// StreamPage as this store publishes it, so a case can make one commit span
	// two pages without writing a store of its own.
	page int
}

func watchingStore(store event.Store) *watchedStore { return &watchedStore{Store: store} }

func (this *watchedStore) Limits() event.Limits {
	held := this.Store.Limits()
	if this.page > 0 {
		held.StreamPage = this.page
	}
	return held
}

func (this *watchedStore) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	reads := this.reads.Add(1)
	page, err := this.Store.ReadStream(ctx, stream, after)
	if err != nil {
		return page, err
	}
	if this.page > 0 && len(page) > this.page {
		page = page[:this.page]
	}
	if this.answer == nil {
		return page, nil
	}
	return this.answer(page, reads)
}

// The aggregate a wait's own appends are made through: a Commit is a value only
// Repo.Append mints and the first door of a wait takes one. Its family is its
// own, so the raw appends the loop cases make through stand.append never share a
// stream with it.
type waited struct{ Tags []string }

type waitedID string

type tag struct{ Tag string }

var waitedOrders = event.Define[waited]("waits.order", func(id waitedID) event.Key {
	return event.Compose(string(id))
})

var tagged = event.Declare(waitedOrders, "waits.tagged", event.From(event.JSON[tag]()),
	func(state waited, fact tag) waited {
		state.Tags = append(state.Tags, fact.Tag)
		return state
	})

func waitedRepo(t *testing.T, store event.Store) *event.Repo[waited, waitedID] {
	t.Helper()
	repo, err := event.Bind(event.Open(store), waitedOrders)
	if err != nil {
		t.Fatalf("binding the aggregate a wait appends through was refused: %v", err)
	}
	return repo
}

// One append of one or more tagged facts and the Commit it answers, which is
// what a caller holds when it asks for its own change to become visible.
func committing(t *testing.T, repo *event.Repo[waited, waitedID], id waitedID, tags ...string) event.Commit {
	t.Helper()
	ctx := context.Background()
	_, at, err := repo.Load(ctx, id)
	if err != nil {
		t.Fatalf("loading %q was refused: %v", id, err)
	}
	changes := make([]event.Change[waited], 0, len(tags))
	for _, named := range tags {
		changes = append(changes, tagged.New(id, tag{Tag: named}))
	}
	_, commit, err := repo.Append(ctx, at, changes...)
	if err != nil {
		t.Fatalf("appending %v to %q was refused: %v", tags, id, err)
	}
	return commit
}

// The spec a case waits with, derived the way a host derives one: from the Spec
// the runner was built from and the cover it was built out of.
func waitingOver(t *testing.T, spec projection.Spec, over projection.Cover) projection.WaitSpec {
	t.Helper()
	held, err := projection.WaitOf(spec, over)
	if err != nil {
		t.Fatalf("a well-formed wait spec was refused: %v", err)
	}
	return held
}

// The sequencer a commit spans two of: a key off a field of the payload rather
// than off the stream, which is what makes a commit of three facts belong to two
// sequences and what a mark carrying only the last one's key would ask the wrong
// question about.
func byTag() projection.Sequencer {
	return projection.SequenceBy("by-tag", func(envelope event.Envelope) string {
		var held tag
		if err := json.Unmarshal(envelope.Payload, &held); err != nil {
			return "unreadable"
		}
		return held.Tag
	})
}

// The interval seam a wait is driven through, in two shapes. Asked records every
// interval the caller wanted, which is how a case says a wait that reached on its
// first poll never asked for one at all. Ready decides the rest: an always-ready
// ticker lets the poll count be decided by what the checkpoint store answers,
// and one that is never ready leaves the caller's deadline as the only thing that
// releases the wait.
type paced struct {
	mutex sync.Mutex
	asked []time.Duration
	beats chan time.Time
}

func freeRunning() *paced {
	beats := make(chan time.Time)
	close(beats)
	return &paced{beats: beats}
}

func neverBeating() *paced { return &paced{beats: make(chan time.Time)} }

func (this *paced) Ticks(interval time.Duration) runtime.Ticker {
	this.mutex.Lock()
	this.asked = append(this.asked, interval)
	this.mutex.Unlock()
	return beating{beat: this.beats}
}

func (this *paced) intervals() []time.Duration {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return append([]time.Duration(nil), this.asked...)
}

func (this *paced) fire(t *testing.T) {
	t.Helper()
	select {
	case this.beats <- time.Now():
	case <-time.After(settle):
		t.Fatal("the wait was not blocked on its ticker, so nothing released it into another poll")
	}
}

func payloadsOf(envelopes []event.Envelope) []string {
	held := make([]string, 0, len(envelopes))
	for _, envelope := range envelopes {
		held = append(held, string(envelope.Payload))
	}
	return held
}

func identitiesOf(envelopes []event.Envelope) []string {
	held := make([]string, 0, len(envelopes))
	for _, envelope := range envelopes {
		held = append(held, envelope.Stream.Family+"/"+string(envelope.Stream.Key)+"@"+strconv.FormatUint(uint64(envelope.Version), 10))
	}
	return held
}

func same(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
