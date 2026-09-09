package projection_test

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	versions    map[event.Stream]event.Version
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
