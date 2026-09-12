//go:build integration

package eventpg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/projection"
	"github.com/frostgrove/vv/runtime"
)

const waitFamily = "eventpg.wait.orders"

// Long enough that a commit, a round trip to PostgreSQL and a channel send finish
// inside it, and short enough that a case which must NOT reach it fails rather
// than hanging until the suite's own timeout.
const waitElapses = 3 * time.Second

// A commit through the repository door, which is the only one that answers a
// Commit: the store's own Append answers nothing a mark can be minted from, and
// the map from a version to a position is what ES-05 costs one read.
func committed(t *testing.T, ctx context.Context, repo *event.Repo[held, string], fact *event.Fact[held, string, held], id string, payloads ...string) event.Commit {
	t.Helper()
	at := loaded(t, ctx, repo, id)
	changes := make([]event.Change[held], 0, len(payloads))
	for _, payload := range payloads {
		changes = append(changes, fact.New(id, held{Bytes: []byte(payload)}))
	}
	_, commit, err := repo.Append(ctx, at, changes...)
	if err != nil {
		t.Fatalf("appending %v to %q answered %v", payloads, id, err)
	}
	return commit
}

// The wait spec the module page gives: derived from the Spec the runner was
// built from, so the sequencer, the queue and the defaults are the projection's
// own rather than a request path's guess at them.
func waitingOver(t *testing.T, spec projection.Spec, over projection.Cover) projection.WaitSpec {
	t.Helper()
	derived, err := projection.WaitOf(spec, over)
	if err != nil {
		t.Fatalf("the wait spec derived from %q was refused: %v", spec.Name, err)
	}
	return derived
}

func markOf(t *testing.T, ctx context.Context, spec projection.WaitSpec, store event.Store, commit event.Commit) projection.Mark {
	t.Helper()
	mark, err := spec.Committed(ctx, store, commit)
	if err != nil {
		t.Fatalf("minting a mark over the commit of %v answered %v", commit.Stream(), err)
	}
	if mark.Zero() {
		t.Fatalf("the mint over the commit of %v answered the zero Mark beside a nil error", commit.Stream())
	}
	return mark
}

type answered struct {
	vis projection.Visibility
	err error
}

// A wait on a goroutine of the test's, which is where a request handler's is, so
// the case releases its polls one at a time and reads the answer when it comes.
func awaiting(ctx context.Context, spec projection.WaitSpec) <-chan answered {
	held := make(chan answered, 1)
	go func() {
		vis, err := projection.Wait(ctx, spec)
		held <- answered{vis: vis, err: err}
	}()
	return held
}

func answeredWithin(t *testing.T, held <-chan answered) answered {
	t.Helper()
	select {
	case one := <-held:
		return one
	case <-time.After(30 * time.Second):
		t.Fatal("the wait never answered")
		return answered{}
	}
}

// The interval seam a wait is driven through, so a live case decides how many
// polls were made without a sleep and without a clock: a poll happens when this
// beats and at no other moment.
type paced struct{ beats chan time.Time }

func neverBeating() *paced { return &paced{beats: make(chan time.Time)} }

func (this *paced) Ticks(time.Duration) runtime.Ticker { return beating{beat: this.beats} }

func (this *paced) fire(t *testing.T) {
	t.Helper()
	select {
	case this.beats <- time.Now():
	case <-time.After(30 * time.Second):
		t.Fatal("the wait was not blocked on its ticker, so nothing released it into another poll")
	}
}

type beating struct{ beat chan time.Time }

func (this beating) Ticks() <-chan time.Time { return this.beat }

func (this beating) Stop() {}

// The store with one counter around the read a mint makes, so "one ReadStream for
// a commit that fits a page" is a measurement rather than a reading of the
// source. Nothing here reaches an optional interface of the store's, which is the
// one thing a decorator would have lost.
type countedReads struct {
	event.Store
	reads atomic.Int64
}

func (this *countedReads) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	this.reads.Add(1)
	return this.Store.ReadStream(ctx, stream, after)
}

var errBlinkedCheckpointRow = errors.New("eventpg_test: this checkpoint row could not be read")

// The shipped checkpoint store with counters and one arming. The counters are
// what a wait's cost is read off; the arming fails the loads inside a window, and
// it is the only way a live case reaches a poll that cannot be made at all — a
// PostgreSQL a case can make fail on demand is a PostgreSQL a case has broken for
// every other test in the run.
type countedCheckpoints struct {
	event.Checkpoints
	loads, saves, forgets atomic.Int64
	failFrom, failTo      atomic.Int64
	hold                  atomic.Pointer[heldLoad]
}

// A census read the case holds open, which is the only way something else — a
// cutover — commits INSIDE a poll rather than between two of them. Firing the
// ticker cannot reach that window: it releases a wait into its next poll and the
// window is the park round trip and the census round trip of the poll already
// running.
type heldLoad struct {
	at       int64
	arrived  chan struct{}
	released chan struct{}
}

func holdingLoad(at int64) *heldLoad {
	return &heldLoad{at: at, arrived: make(chan struct{}), released: make(chan struct{})}
}

func (this *heldLoad) reached(t *testing.T) {
	t.Helper()
	select {
	case <-this.arrived:
	case <-time.After(30 * time.Second):
		t.Fatal("no poll reached the census this case holds open, so nothing landed inside a poll")
	}
}

func (this *heldLoad) let() { close(this.released) }

func (this *countedCheckpoints) Load(ctx context.Context, name string) (event.Checkpoint, error) {
	at := this.loads.Add(1)
	if held := this.hold.Load(); held != nil && at == held.at {
		close(held.arrived)
		<-held.released
	}
	from, to := this.failFrom.Load(), this.failTo.Load()
	if from > 0 && at >= from && (to == 0 || at <= to) {
		return event.Checkpoint{}, errBlinkedCheckpointRow
	}
	return this.Checkpoints.Load(ctx, name)
}

func (this *countedCheckpoints) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	this.saves.Add(1)
	return this.Checkpoints.Save(ctx, checkpoint)
}

func (this *countedCheckpoints) Forget(ctx context.Context, name string) error {
	this.forgets.Add(1)
	return this.Checkpoints.Forget(ctx, name)
}

func (this *countedCheckpoints) failing(from, to int64) {
	this.failTo.Store(to)
	this.failFrom.Store(from)
}

// Every call a wait made to the queue, with the one thing no other instrument can
// see beside it: whether a transaction of the destination's was bound to the
// context it was made on. make api sees a method set and check-event-kernel sees
// a digest, and neither sees which methods a caller calls or where it calls them.
type parkCall struct {
	method string
	bound  bool
}

type watchedPark struct {
	queue  *livePark
	source crud.Source

	mutex sync.Mutex
	calls []parkCall
}

func watching(queue *livePark, source crud.Source) *watchedPark {
	return &watchedPark{queue: queue, source: source}
}

func (this *watchedPark) record(ctx context.Context, method string) {
	bound := false
	if held, found := crud.ExecutorFor(ctx, this.source); found {
		_, bound = crudsql.Transaction(held)
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.calls = append(this.calls, parkCall{method: method, bound: bound})
}

func (this *watchedPark) Sequences(ctx context.Context, of projection.Identity) (uint64, error) {
	this.record(ctx, "Sequences")
	return this.queue.Sequences(ctx, of)
}

func (this *watchedPark) Holds(ctx context.Context, of projection.Identity, sequence string) (bool, error) {
	this.record(ctx, "Holds")
	return this.queue.Holds(ctx, of, sequence)
}

func (this *watchedPark) Park(ctx context.Context, letter projection.Letter) error {
	this.record(ctx, "Park")
	return this.queue.Park(ctx, letter)
}

func (this *watchedPark) Holes(ctx context.Context, of projection.Identity) (uint64, error) {
	this.record(ctx, "Holes")
	return this.queue.Holes(ctx, of)
}

func (this *watchedPark) made() []parkCall {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return slices.Clone(this.calls)
}

func (this *watchedPark) counted(t *testing.T, method string, want int) {
	t.Helper()
	got := 0
	for _, call := range this.made() {
		if call.method == method {
			got++
		}
	}
	if got != want {
		t.Fatalf("the wait called %s %d times where the budget is %d, and a call count is what says where a contract's method runs", method, got, want)
	}
}

// A handler's own brake: the pass stops at the front of a page until the case
// lets it through, so a projection is HELD behind a mark rather than raced
// against one. It releases on the pass's own context too, because a runner whose
// context is done and whose handler is blocked holds the process open.
type brake struct {
	mutex    sync.Mutex
	open     chan struct{}
	arrivals atomic.Int64
}

func braked() *brake { return &brake{open: make(chan struct{})} }

func flowing() *brake {
	held := braked()
	held.let()
	return held
}

func (this *brake) hold() {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	select {
	case <-this.open:
		this.open = make(chan struct{})
	default:
	}
}

func (this *brake) let() {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	select {
	case <-this.open:
	default:
		close(this.open)
	}
}

func (this *brake) gate() chan struct{} {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.open
}

func (this *brake) arrive(ctx context.Context) error {
	this.arrivals.Add(1)
	select {
	case <-this.gate():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (this *brake) arrived() int64 { return this.arrivals.Load() }

func (this *destination) insertsWhile(stop *brake) projection.Handler {
	return projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
		if err := stop.arrive(ctx); err != nil {
			return err
		}
		for _, envelope := range batch.Envelopes {
			if _, err := this.on(ctx).ExecContext(ctx, "INSERT INTO "+this.table+" (payload) VALUES ($1)", string(envelope.Payload)); err != nil {
				return err
			}
		}
		return nil
	})
}

// The poisoning handler with a brake in front of it, which is what a partition
// that is behind looks like from the outside: it has read its page and applied
// nothing of it.
func (this *destination) applyingWhile(poison *poisonous, stop *brake) projection.Handler {
	applying := this.applying(poison)
	return projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
		if err := stop.arrive(ctx); err != nil {
			return err
		}
		return applying.Apply(ctx, batch)
	})
}

// The queue's own rows without the psql cross-check, because a case that polls
// until a letter lands would otherwise start a psql every five milliseconds and
// read the two at two moments. What a case asserts is still read through
// livePark.letters, which does cross-check.
func parkedIn(t *testing.T, queue *livePark, of projection.Identity) []string {
	t.Helper()
	return queue.query(t, "SELECT sequence FROM "+queue.park+" WHERE identity = '"+of.String()+"'")
}

// A key of the family whose sequence key lands in the partition named. The
// partition is asked rather than the hash recomputed, so a case cannot disagree
// with the arithmetic it is testing against.
func keyInPartition(t *testing.T, family string, part projection.Partition, prefix string) string {
	t.Helper()
	for index := range 10000 {
		key := fmt.Sprintf("%s/%d", prefix, index)
		if part.Matches(sequenceOf(family, key)) {
			return key
		}
	}
	t.Fatalf("no key under %q of %q lands in %s, so this case cannot put an event where it needs one", prefix, family, part)
	return ""
}

func aWholeCover(t *testing.T) projection.Cover {
	t.Helper()
	return coverOf(t, projection.Whole())
}

func quartered(t *testing.T) projection.Cover {
	t.Helper()
	return coverOf(t, aPartition(t, 0, 3), aPartition(t, 1, 3), aPartition(t, 2, 3), aPartition(t, 3, 3))
}

// §6.1, §UC-204, §INV-113. The whole appendix in one test, against the live
// store: a command commits, the caller mints a mark from the commit it holds,
// waits, and reads the read model. There is no sleep, no poll of a business
// table and no assertion about wall-clock duration anywhere in it — what makes
// the read show the new state is the wait.
//
// The second projection beside it is the scope sentence as a measurement: the
// wait names one projection, and a second one over the same log that has applied
// nothing does not hold it back and cannot be waited on through it.
func TestAConfirmedCommandIsVisibleWithoutASleep(t *testing.T) {
	stand := newProjectionCase(t, 16, 0)
	into := stand.destination(t, "visible_read_model")
	beside := stand.destination(t, "visible_second_read_model")
	repo, fact := boundRepo(t, stand.store, waitFamily+".visible")
	ctx := context.WithoutCancel(t.Context())

	spec := inUnit(stand.spec(t, "visible", into.inserts(&deliveries{}, nil)), stand.source, into.source)
	runner := stand.run(t, spec)
	runner.following(t, "the projection this wait is about reached the end of the log")

	stop := braked()
	t.Cleanup(stop.let)
	second := inUnit(stand.spec(t, "visible_second", beside.insertsWhile(stop)), stand.source, beside.source)
	stand.run(t, second)

	commit := committed(t, ctx, repo, fact, "visible/A", "visible-1")

	waiting := waitingOver(t, spec, aWholeCover(t))
	counted := &countedReads{Store: stand.store}
	waiting.Until = markOf(t, ctx, waiting, counted, commit)
	if reads := counted.reads.Load(); reads != 1 {
		t.Fatalf("the mint issued %d reads of the stream for a commit of one fact, where a commit that fits a page costs exactly one", reads)
	}

	bounded, cancel := context.WithTimeout(ctx, waitElapses)
	defer cancel()
	vis, err := projection.Wait(bounded, waiting)
	if err != nil || !vis.Reached {
		t.Fatalf("a wait over a live projection for a commit that landed answered %+v and %v", vis, err)
	}
	if vis.Behind != 0 || vis.At < waiting.Until.At() {
		t.Fatalf("the reached wait reports %+v against a mark this generation has passed", vis)
	}

	if rows := into.rows(t); !slices.Contains(rows, "visible-1") {
		t.Fatalf("the read model holds %v immediately after a wait that reached, and the whole of ES-05 is that this read does not show the old state", rows)
	}

	t.Run("a second projection of the same log is not what this wait answered about", func(t *testing.T) {
		if rows := beside.rows(t); len(rows) != 0 {
			t.Fatalf("the second projection's read model holds %v where its handler has been held at its first page, so the arm above reached over a log both were following", rows)
		}

		// §INV-108, the cross-spec half: every value here is of the right type,
		// both specs were derived by WaitOf, and the position on the mark is
		// global — so the census would reach and the park would be asked under
		// keys no letter of this projection was ever written under.
		elsewhere := waitingOver(t, second, aWholeCover(t))
		crossed := waiting
		crossed.Until = markOf(t, ctx, elsewhere, stand.store, commit)
		if _, err := projection.Wait(bounded, crossed); !errors.Is(err, projection.ErrSpec) {
			t.Fatalf("a mark minted from the second projection's spec and waited on the first answered %v", err)
		}
	})

	t.Run("the control: the same wait against a projection that is not running", func(t *testing.T) {
		stopped := inUnit(stand.spec(t, "visible_stopped", into.inserts(&deliveries{}, nil)), stand.source, into.source)
		absent := waitingOver(t, stopped, aWholeCover(t))
		absent.Until = markOf(t, ctx, absent, stand.store, commit)

		short, stop := context.WithTimeout(ctx, 300*time.Millisecond)
		defer stop()
		vis, err := projection.Wait(short, absent)
		if !errors.Is(err, projection.ErrNotVisible) || vis.Reached || vis.Moved {
			t.Fatalf("a wait over a projection nothing is running answered %+v and %v, so the arm above is proving the wait and not proving that time passes", vis, err)
		}
	})
}

// §6.2, §UC-206, §INV-110, §INV-113. The pair a reasonable implementer writes
// wrong, live. Everything the positive arm rests on is read out of PostgreSQL
// first: the checkpoint row's own watermark is at or above the mark, the read
// model never received the event, and the letter is in the queue. A wait that
// compared only the watermark would answer reached — which is exactly what the
// control makes it do.
func TestTheParkedPairIsTheWholeOfTheAppendix(t *testing.T) {
	stand := newProjectionCase(t, 12, 0)
	into := stand.destination(t, "parked_read_model")
	queue := stand.park(t, "parked_park")
	poison := poisoning("parked-1")
	repo, fact := boundRepo(t, stand.store, waitFamily+".parked")
	ctx := context.WithoutCancel(t.Context())

	spec := stand.parking(t, "parked", into, queue, poison)
	runner := stand.run(t, spec)
	runner.following(t, "the parking projection reached the end of an empty log")

	commit := committed(t, ctx, repo, fact, "parked/A", "parked-1")
	runner.degraded(t, "the projection parked the sequence the caller's own stream belongs to")

	waiting := waitingOver(t, spec, aWholeCover(t))
	waiting.Until = markOf(t, ctx, waiting, stand.store, commit)

	of := identityOf(t, "parked")
	row := storedCheckpoint(t, stand.schema, "parked")
	if event.Position(row.highest) < waiting.Until.At() {
		t.Fatalf("the checkpoint row stands at %d against a mark at %d, so the scan has not passed the parked change and this case is not about a parked one", row.highest, waiting.Until.At())
	}
	if row.quarantined == 0 {
		t.Fatalf("the checkpoint row records no quarantined envelope at advance %d, so nothing was parked", row.advance)
	}
	if rows := into.rows(t); slices.Contains(rows, "parked-1") {
		t.Fatalf("the read model holds %v, so the parked envelope was applied and the pair below would be about nothing", rows)
	}
	if letters := queue.letters(t, of); len(letters) != 1 {
		t.Fatalf("the queue holds %v where the poisoned envelope is one letter", letters)
	}

	// A deadline this arm must never reach: the park is asked before the census
	// and on the first poll, so an implementation that asked it after reaching
	// fails here rather than hanging.
	bounded, cancel := context.WithTimeout(ctx, waitElapses)
	defer cancel()
	vis, err := projection.Wait(bounded, waiting)
	if !errors.Is(err, projection.ErrParked) {
		t.Fatalf("a wait whose own sequence is parked answered %v, where the scan passed the change and the read model never received it", err)
	}
	if vis != (projection.Visibility{Parked: true, Polls: 1}) {
		t.Fatalf("the answer is %+v, where the park is asked before the census and a true answer reads no census at all", vis)
	}

	t.Run("the control: the identical wiring with Park nil reports the parked event reached", func(t *testing.T) {
		unqueued := waiting
		unqueued.Park = nil
		vis, err := projection.Wait(bounded, unqueued)
		if err != nil || !vis.Reached {
			t.Fatalf("the same wait with no queue answered %+v and %v, and this arm is the failure the pair exists to pin", vis, err)
		}
		if rows := into.rows(t); slices.Contains(rows, "parked-1") {
			t.Fatalf("the read model holds %v, so the arm above is not reporting a change that never arrived", rows)
		}
	})
}

// §6.3, §UC-207, §INV-110. A live four-partition cover: the caller's stream
// hashes into the partition that parked it while another partition is a hundred
// positions behind. The answer comes on the FIRST poll, from the park question,
// because an implementation that asked the queue only once the census reached
// would burn the caller's whole deadline on a condition it could have named at
// once.
func TestParkedInOnePartitionWhileAnotherLags(t *testing.T) {
	const family = waitFamily + ".quartered"
	stand := newProjectionCase(t, 24, 0)
	into := stand.destination(t, "quartered_read_model")
	queue := stand.park(t, "quartered_park")
	poison := poisoning("quartered-poison")
	repo, fact := boundRepo(t, stand.store, family)
	ctx := context.WithoutCancel(t.Context())

	cover := quartered(t)
	lagging := keyInPartition(t, family, aPartition(t, 0, 3), "lagging")
	parked := keyInPartition(t, family, aPartition(t, 2, 3), "parked")

	stop := flowing()
	t.Cleanup(stop.let)
	var specs []projection.Spec
	for id := range uint32(4) {
		member := stand.parking(t, "quartered", into, queue, poison)
		member.Partition = aPartition(t, id, 3)
		if id == 0 {
			member.Handler = into.applyingWhile(poison, stop)
		}
		stand.run(t, member)
		specs = append(specs, member)
	}

	// One event a member, so every row of the cover exists: a member with no row
	// is a topology refusal rather than a member at the origin, and this case is
	// about neither.
	for id := range uint32(4) {
		key := keyInPartition(t, family, aPartition(t, id, 3), fmt.Sprintf("warm-%d", id))
		stand.write(t, aStream(family, key), family+".held", fmt.Sprintf("warm-%d", id))
	}
	waitFor(t, "every member of the cover recorded a row", func() bool {
		for id := range uint32(4) {
			if _, found := stand.row(t, memberIdentity(t, "quartered", projection.Ungenerated, id, 3).String()); !found {
				return false
			}
		}
		return true
	})

	// Partition 0 stops here: it has read its page and applied nothing of it, so
	// its watermark stands where it was while every other member walks a hundred
	// positions on.
	arrived := stop.arrived()
	stop.hold()
	for index := range 100 {
		stand.write(t, aStream(family, lagging), family+".held", fmt.Sprintf("lagging-%d", index))
	}
	waitFor(t, "partition 0 reached the page it is held at", func() bool {
		return stop.arrived() > arrived
	})

	commit := committed(t, ctx, repo, fact, parked, "quartered-poison")
	waiting := waitingOver(t, specs[2], cover)
	waiting.Until = markOf(t, ctx, waiting, stand.store, commit)
	waitFor(t, "the partition holding the caller's stream parked it", func() bool {
		return len(parkedIn(t, queue, identityOf(t, "quartered"))) == 1
	})

	behind := storedCheckpoint(t, stand.schema, memberIdentity(t, "quartered", projection.Ungenerated, 0, 3).String())
	if waiting.Until.At() < event.Position(behind.highest)+100 {
		t.Fatalf("the held member stands at %d against a mark at %d, so this cover has no member a hundred positions behind the mark and the census would reach", behind.highest, waiting.Until.At())
	}

	bounded, cancel := context.WithTimeout(ctx, waitElapses)
	defer cancel()
	vis, err := projection.Wait(bounded, waiting)
	if !errors.Is(err, projection.ErrParked) {
		t.Fatalf("a wait whose partition parked the change while another lags answered %v, where the park is asked before the census and the lag is beside the point", err)
	}
	if vis.Polls != 1 {
		t.Fatalf("the parked answer came on poll %d, where asking the park only after reaching would have burned the whole deadline for a condition nameable at once", vis.Polls)
	}

	t.Run("the control: with that partition not parked the same wiring waits for the laggard and reaches", func(t *testing.T) {
		queue.clear(t, identityOf(t, "quartered"), sequenceOf(family, parked))
		stop.let()

		patient, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		vis, err := projection.Wait(patient, waiting)
		if err != nil || !vis.Reached {
			t.Fatalf("the same wait over an emptied queue answered %+v and %v, so the early answer above is discriminating rather than the only answer this wiring gives", vis, err)
		}
	})
}

// §6.4, §UC-209, §UC-210, §INV-109. Against eventpg specifically, because this is
// the store where the naive version WORKS: the row is visible to the transaction
// that wrote it and the position on it is a real number, so a mint taken there
// answers a mark nothing will ever deliver. The two absences beside it are
// asserted indistinguishable, which is the whole of what ErrUncommitted says.
func TestAMarkMintedInsideTheWritingTransactionIsRefused(t *testing.T) {
	const family = waitFamily + ".minted"
	stand := newProjectionCase(t, 12, 0)
	into := stand.destination(t, "minted_read_model")
	repo, fact := boundRepo(t, stand.store, family)
	ctx := context.WithoutCancel(t.Context())

	spec := inUnit(stand.spec(t, "minted", into.inserts(&deliveries{}, nil)), stand.source, into.source)
	waiting := waitingOver(t, spec, aWholeCover(t))

	inside, tx := begin(t, stand.store, nil)
	at := loaded(t, inside, repo, "minted/A")
	_, commit, err := repo.Append(inside, at, fact.New("minted/A", held{Bytes: []byte("minted-1")}))
	if err != nil {
		t.Fatalf("an append inside the caller's own transaction answered %v", err)
	}
	if rows := storedOn(t, transactionOf(t, inside, stand.store), stand.schema, aStream(family, "minted/A")); len(rows) != 1 || rows[0].position == 0 {
		t.Fatalf("the writing transaction sees %d rows at %v, and this case is about a store that shows its own uncommitted append a real position", len(rows), rows)
	}

	counted := &countedReads{Store: stand.store}
	if _, err := waiting.Committed(inside, counted, commit); !errors.Is(err, projection.ErrSpec) {
		t.Fatalf("a mint on the context carrying the transaction that wrote the append answered %v, where the position it would read can still roll back", err)
	}
	if reads := counted.reads.Load(); reads != 0 {
		t.Fatalf("the refusal was made after %d reads, and a transaction of this store's bound to the context is answered before any read is issued", reads)
	}

	open, err := waiting.Committed(ctx, stand.store, commit)
	if !errors.Is(err, projection.ErrUncommitted) || !open.Zero() {
		t.Fatalf("a mint on a fresh connection while the writer is still open answered %+v and %v", open, err)
	}
	if rows := stored(t, stand.schema, aStream(family, "minted/A")); len(rows) != 0 {
		t.Fatalf("the pool sees %d rows before the caller committed, so the arm above read a committed row", len(rows))
	}
	whileOpen := err

	if err := tx.Commit(inside); err != nil {
		t.Fatalf("committing the caller's own transaction answered %v", err)
	}
	if mark := markOf(t, ctx, waiting, stand.store, commit); mark.At() == 0 {
		t.Fatal("the control minted the zero mark, so the refusals above are about this store and not about the bound transaction")
	}

	t.Run("after the writing transaction rolled back, the same call answers the same refusal", func(t *testing.T) {
		rolling, rolled := begin(t, stand.store, nil)
		at := loaded(t, rolling, repo, "minted/B")
		_, second, err := repo.Append(rolling, at, fact.New("minted/B", held{Bytes: []byte("minted-2")}))
		if err != nil {
			t.Fatalf("an append inside the transaction that is about to roll back answered %v", err)
		}
		if err := rolled.Rollback(rolling); err != nil {
			t.Fatalf("rolling back answered %v", err)
		}

		vanished, err := waiting.Committed(ctx, stand.store, second)
		if !errors.Is(err, projection.ErrUncommitted) || !vanished.Zero() {
			t.Fatalf("a mint over a commit whose transaction rolled back answered %+v and %v", vanished, err)
		}
		if err.Error() != whileOpen.Error() {
			t.Fatalf("the rolled-back arm answers %q where the still-open arm answers %q, and ES-07's Unresolved one level down tells the two apart no better, deliberately", err, whileOpen)
		}
		if rows := stored(t, stand.schema, aStream(family, "minted/B")); len(rows) != 0 {
			t.Fatalf("the pool sees %d rows of a rolled-back append", len(rows))
		}
	})
}

// §6.5, §UC-212. Two live waits with a 200 ms deadline, and Moved is the whole of
// the difference between behind and broken: a bare timeout is the same answer for
// a slow projector, a stopped one and a stopped daemon, and the maintainers of
// the mechanism this appendix cites have that open against themselves as a
// defect.
func TestSlowVersusStopped(t *testing.T) {
	const budget = 200 * time.Millisecond

	t.Run("a projector that is advancing but slow", func(t *testing.T) {
		const family = waitFamily + ".slow"
		stand := newProjectionCase(t, 12, 1)
		into := stand.destination(t, "slow_read_model")
		repo, fact := boundRepo(t, stand.store, family)
		ctx := context.WithoutCancel(t.Context())

		for index := range 300 {
			stand.write(t, aStream(family, fmt.Sprintf("slow/%d", index)), family+".held", fmt.Sprintf("slow-%d", index))
		}
		commit := committed(t, ctx, repo, fact, "slow/last", "slow-last")

		// One envelope a page and a handler that takes a millisecond over each,
		// which is a projector that is working and is not going to reach the mark
		// inside the caller's budget.
		spec := inUnit(stand.spec(t, "slow", into.inserts(&deliveries{}, func(context.Context) {
			time.Sleep(time.Millisecond)
		})), stand.source, into.source)
		waiting := waitingOver(t, spec, aWholeCover(t))
		waiting.Until = markOf(t, ctx, waiting, stand.store, commit)
		waiting.Every = 20 * time.Millisecond
		stand.run(t, spec)

		bounded, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		vis, err := projection.Wait(bounded, waiting)
		if !errors.Is(err, projection.ErrNotVisible) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the deadline answered %v, where a caller that branches on either gets what it expects", err)
		}
		if errors.Is(err, projection.ErrTopology) || errors.Is(err, event.ErrBackend) {
			t.Fatalf("a deadline over a healthy store answered %v, where §5.2 reserves ErrTopology for the caller asking wrong and event.ErrBackend for a store in trouble", err)
		}
		if vis.Reached || vis.Polls < 2 {
			t.Fatalf("the deadline answered %+v, where the Visibility is the last poll that could be read and there was more than one", vis)
		}
		if !vis.Moved {
			t.Fatalf("an advancing projector reported %+v, and Moved is the whole of the difference between behind and broken", vis)
		}
		if vis.Behind == 0 || vis.Behind != waiting.Until.At()-vis.At {
			t.Fatalf("the answer reports Behind %d at %d against a mark at %d, and Behind is the distance to the mark as an upper bound", vis.Behind, vis.At, waiting.Until.At())
		}
		if vis.At == 0 {
			t.Fatalf("the answer reports %+v, where an advancing projector's Behind shrank from the whole of the mark between the first poll and the last", vis)
		}
	})

	t.Run("a projector whose runner is stopped", func(t *testing.T) {
		const family = waitFamily + ".stopped"
		stand := newProjectionCase(t, 12, 0)
		into := stand.destination(t, "stopped_read_model")
		repo, fact := boundRepo(t, stand.store, family)
		ctx := context.WithoutCancel(t.Context())

		stand.write(t, aStream(family, "stopped/warm"), family+".held", "stopped-warm")
		spec := inUnit(stand.spec(t, "stopped", into.inserts(&deliveries{}, nil)), stand.source, into.source)
		runner := stand.run(t, spec)
		runner.following(t, "the projection drained the log it starts on")
		runner.stop(t)

		commit := committed(t, ctx, repo, fact, "stopped/A", "stopped-1")
		waiting := waitingOver(t, spec, aWholeCover(t))
		waiting.Until = markOf(t, ctx, waiting, stand.store, commit)
		waiting.Every = 20 * time.Millisecond

		bounded, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		vis, err := projection.Wait(bounded, waiting)
		if !errors.Is(err, projection.ErrNotVisible) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the deadline answered %v", err)
		}
		if errors.Is(err, projection.ErrTopology) || errors.Is(err, event.ErrBackend) {
			t.Fatalf("a deadline over a healthy store answered %v, where §5.2 reserves ErrTopology for the caller asking wrong and event.ErrBackend for a store in trouble", err)
		}
		if vis.Reached || vis.Moved || vis.Polls < 2 {
			t.Fatalf("a stopped projector reported %+v over a budget of %v, where the row never moved", vis, budget)
		}
		row := storedCheckpoint(t, stand.schema, "stopped")
		if vis.At != event.Position(row.highest) || vis.Behind != waiting.Until.At()-vis.At {
			t.Fatalf("the answer reports %+v where the row this process read stands at %d", vis, row.highest)
		}

		// The two arms above land their deadline BETWEEN two polls, which is where
		// a short budget lands least often. A budget that runs out inside a round
		// trip gets the store's own answer to a statement made with a done context
		// — eventpg classifies it and the census names the member it could not
		// read — so the same ordinary timeout arrives as ErrTopology over
		// event.ErrBackend unless the wait tells its own budget from a store that
		// failed. The lock is how that round trip is made to outlast the budget
		// without breaking PostgreSQL for every other test in the run.
		t.Run("a deadline that elapses inside a poll is the budget and not the store failing", func(t *testing.T) {
			held := waiting
			held.Checkpoints = stand.checkpoints(t)

			blocker := checkpointPool(t, 2)
			holding, err := blocker.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("the transaction that holds the checkpoint table answered %v", err)
			}
			defer func() { _ = holding.Rollback() }()
			if _, err := holding.ExecContext(ctx, "LOCK TABLE "+quoteIdentifier(stand.schema.Name)+".checkpoints IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatalf("locking the checkpoint table answered %v", err)
			}

			bounded, cancel := context.WithTimeout(ctx, budget)
			defer cancel()
			vis, err := projection.Wait(bounded, held)
			if !errors.Is(err, projection.ErrNotVisible) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("a budget that ran out inside a census read answered %v, where a handler branching on ErrNotVisible serves stale and this is the most common shape of a short deadline", err)
			}
			if errors.Is(err, projection.ErrTopology) || errors.Is(err, event.ErrBackend) {
				t.Fatalf("a budget that ran out inside a census read answered %v, where the cover is right, the schema is right and the store is fine — and a caller reading §5.2's vocabulary pages an operator over it", err)
			}
			if vis != (projection.Visibility{Polls: 1}) {
				t.Fatalf("the answer is %+v, where one poll was made and nothing of it could be read", vis)
			}
			if err := holding.Rollback(); err != nil {
				t.Fatalf("releasing the checkpoint table answered %v", err)
			}

			// Without this the arm above passes for a wait that never wrapped a
			// poll's refusal into its deadline at all, which is §UC-244's rule
			// going the other way.
			t.Run("the control: a deadline reached over a poll that really failed carries the store's own class", func(t *testing.T) {
				points := &countedCheckpoints{Checkpoints: stand.checkpoints(t)}
				driven := neverBeating()
				control := waiting
				control.Checkpoints = points
				control.Ticks = driven.Ticks

				bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				answers := awaiting(bounded, control)
				waitFor(t, "the first poll read the census", func() bool { return points.loads.Load() >= 1 })
				points.failing(2, 0)
				driven.fire(t)
				got := answeredWithin(t, answers)

				if !errors.Is(got.err, projection.ErrNotVisible) || !errors.Is(got.err, context.DeadlineExceeded) {
					t.Fatalf("a deadline reached over a failing poll answered %v", got.err)
				}
				if !errors.Is(got.err, projection.ErrTopology) || !errors.Is(got.err, event.ErrBackend) || !errors.Is(event.CauseOf(got.err), errBlinkedCheckpointRow) {
					t.Fatalf("a deadline reached over a poll that could not be made answered %v, where the last poll's own refusal is wrapped into it and the arm above is what that is told apart from", got.err)
				}
			})
		})

		t.Run("the control: a cancelled context is not a deadline", func(t *testing.T) {
			cancelled, stop := context.WithCancel(ctx)
			held := awaiting(cancelled, waiting)
			time.AfterFunc(20*time.Millisecond, stop)
			got := answeredWithin(t, held)
			if !errors.Is(got.err, context.Canceled) || errors.Is(got.err, projection.ErrNotVisible) {
				t.Fatalf("a cancelled wait answered %v, where the caller stopped asking and a cancelled request stays one", got.err)
			}
			if got.vis != (projection.Visibility{}) {
				t.Fatalf("a cancelled wait answered %+v, where it concluded nothing", got.vis)
			}
		})
	})
}

// §6.6, §UC-208, §INV-110, §INV-112. What a wait costs against the live store and
// the live queue, and WHERE each call runs. A recording Park asserting Holes was
// never asked is the only thing that can see this: make api sees a method set and
// check-event-kernel sees a digest, and neither sees which methods a caller calls
// or whether a transaction was bound when it did.
//
// Both standings have an EMPTY queue, and the second one's checkpoint row records
// quarantined envelopes anyway — the count is cumulative and never falls, so a
// generation that parked a sequence and had it cleared is the live shape of the
// arm an earlier draft read Holes on.
func TestTheCallCountBudgetAndItsPlacement(t *testing.T) {
	for _, one := range []struct {
		what    string
		poisons bool
	}{
		{"a generation that has parked nothing", false},
		{"a generation whose queue was drained after a park", true},
	} {
		t.Run(one.what, func(t *testing.T) {
			family := waitFamily + ".budget"
			stand := newProjectionCase(t, 16, 0)
			into := stand.destination(t, "budget_read_model")
			queue := stand.park(t, "budget_park")
			poison := poisoning("budget-poison")
			repo, fact := boundRepo(t, stand.store, family)
			ctx := context.WithoutCancel(t.Context())

			stop := flowing()
			t.Cleanup(stop.let)
			spec := stand.parking(t, "budget", into, queue, poison)
			spec.Handler = into.applyingWhile(poison, stop)
			runner := stand.run(t, spec)

			stand.write(t, aStream(family, "budget/warm"), family+".held", "budget-warm")
			waitFor(t, "the projection applied the log it starts on", func() bool {
				return len(into.read(t)) == 1
			})

			of := identityOf(t, "budget")
			if one.poisons {
				stand.write(t, aStream(family, "budget/poison"), family+".held", "budget-poison")
				runner.degraded(t, "the projection parked the poisoned sequence")
				queue.clear(t, of, sequenceOf(family, "budget/poison"))
				waitFor(t, "the projection read a zero count off the emptied queue", func() bool {
					return runner.held.State().Parked == 0
				})
			}

			row := storedCheckpoint(t, stand.schema, "budget")
			if (row.quarantined > 0) != one.poisons {
				t.Fatalf("the checkpoint row records %d quarantined where this standing wants %v", row.quarantined, one.poisons)
			}

			// The mark is held on the far side of a brake, so the three polls
			// below are three polls and not a race against a drain.
			stop.hold()
			commit := committed(t, ctx, repo, fact, "budget/A", "budget-1")
			waiting := waitingOver(t, spec, aWholeCover(t))
			if waiting.Park == nil {
				t.Fatal("the derived spec dropped the queue this projection parks into, so the instruments below would measure a wait that asks nothing")
			}
			waiting.Until = markOf(t, ctx, waiting, stand.store, commit)
			driven := neverBeating()
			waiting.Ticks = driven.Ticks

			// The instruments wrap the queue this projection parks into and the
			// schema its checkpoint rows live in, and are values of the wait's
			// own: what they count is the wait's calls and never the loop's,
			// which matters because the two run at once here — the state a
			// request path's wait is always in.
			watched := watching(queue, stand.source)
			waiting.Park = watched
			points := &countedCheckpoints{Checkpoints: stand.checkpoints(t)}
			waiting.Checkpoints = points

			// The count of census reads is what says a poll is OVER rather than
			// merely released: a poll that had read a row below the mark cannot
			// then reach on the row this case is about to advance, so the brake
			// comes off only once two polls have read and the third is the one
			// that finds the change.
			bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			answers := awaiting(bounded, waiting)
			polled := func(polls int64) func() bool {
				return func() bool { return points.loads.Load() >= polls }
			}
			waitFor(t, "the first poll read the census", polled(1))
			driven.fire(t)
			waitFor(t, "the second poll read the census", polled(2))
			stop.let()
			waitFor(t, "the projection applied the caller's own commit", func() bool {
				held, found := stand.row(t, of.String())
				return found && event.Position(held.highest) >= waiting.Until.At()
			})
			driven.fire(t)
			got := answeredWithin(t, answers)

			if got.err != nil || !got.vis.Reached {
				t.Fatalf("a wait over a live projection that reached the mark answered %+v and %v", got.vis, got.err)
			}
			if got.vis.Polls != 3 {
				t.Fatalf("the wait made %d polls where the case drove three, so the counts below are over the wrong number of polls", got.vis.Polls)
			}
			if got.vis.Quarantined != uint64(row.quarantined) {
				t.Fatalf("the census reported %d quarantined where the row records %d", got.vis.Quarantined, row.quarantined)
			}
			watched.counted(t, "Sequences", 3)
			watched.counted(t, "Holds", 0)
			watched.counted(t, "Holes", 0)
			watched.counted(t, "Park", 0)
			for _, call := range watched.made() {
				if call.bound {
					t.Fatalf("the wait called %s with a transaction of the destination's bound, and a request path's poll opens no unit of work", call.method)
				}
			}
			if loads := points.loads.Load(); loads != 3 {
				t.Fatalf("three polls over a one-member cover cost %d checkpoint reads, where a recorded member costs one a poll", loads)
			}
			if saves, forgets := points.saves.Load(), points.forgets.Load(); saves != 0 || forgets != 0 {
				t.Fatalf("the wait wrote %d saves and %d forgets through the value it polls, and it holds the writer's own store only to read through it", saves, forgets)
			}
		})
	}
}

// §6.7, §INV-112. A wait polling the checkpoint store the projection's own loop is
// saving through, under -race. Six of them, on the caller's own goroutines, while
// the loop advances: the fences are two different objects over one row and the
// advance the row ends at is the projection's alone.
func TestAWaitBesideTheProjectionsOwnLoop(t *testing.T) {
	const family = waitFamily + ".beside"
	stand := newProjectionCase(t, 24, 4)
	into := stand.destination(t, "beside_read_model")
	repo, fact := boundRepo(t, stand.store, family)
	ctx := context.WithoutCancel(t.Context())

	for index := range 60 {
		stand.write(t, aStream(family, fmt.Sprintf("beside/%d", index)), family+".held", fmt.Sprintf("beside-%d", index))
	}

	// A page at a time with a handler that takes a few milliseconds over each, so
	// the loop is saving for as long as the waits are polling rather than being
	// over before the first of them made a round trip.
	points := &countedCheckpoints{Checkpoints: stand.checkpoints(t)}
	spec := inUnit(stand.spec(t, "beside", into.inserts(&deliveries{}, func(context.Context) {
		time.Sleep(3 * time.Millisecond)
	})), stand.source, into.source)
	spec.Checkpoints = points

	marks := make([]projection.Mark, 6)
	waiting := waitingOver(t, spec, aWholeCover(t))
	for index := range marks {
		commit := committed(t, ctx, repo, fact, fmt.Sprintf("beside/wait-%d", index), fmt.Sprintf("beside-wait-%d", index))
		marks[index] = markOf(t, ctx, waiting, stand.store, commit)
	}

	runner := stand.run(t, spec)

	bounded, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var waits sync.WaitGroup
	answers := make([]answered, len(marks))
	for index, mark := range marks {
		waits.Add(1)
		go func() {
			defer waits.Done()
			held := waiting
			held.Until = mark
			held.Every = 5 * time.Millisecond
			vis, err := projection.Wait(bounded, held)
			answers[index] = answered{vis: vis, err: err}
		}()
	}
	waits.Wait()

	polled := 0
	for index, one := range answers {
		if one.err != nil || !one.vis.Reached {
			t.Fatalf("the wait for the mark %d answered %+v and %v beside the loop that was saving through the same checkpoint store", index, one.vis, one.err)
		}
		polled = max(polled, one.vis.Polls)
	}
	// Without this the six could all have reached on their first poll against a
	// row that had stopped moving, and nothing above would have run beside
	// anything.
	if polled < 2 {
		t.Fatalf("the longest of the six waits made %d polls, so none of them was polling while the loop was saving", polled)
	}
	if state := runner.held.State(); state.Phase == projection.PhaseHalted {
		t.Fatalf("the projection halted with %v while six waits polled the store it saves through", state.Err)
	}
	runner.stop(t)

	row := storedCheckpoint(t, stand.schema, "beside")
	saves := points.saves.Load()
	if row.advance != saves {
		t.Fatalf("the checkpoint row stands at advance %d over %d saves, and every one of those saves is the projection's — a wait presents none", row.advance, saves)
	}
	if saves < 5 {
		t.Fatalf("the loop saved %d times over a log of sixty-six read four at a time, so the row was not moving while the waits polled it", saves)
	}
	if forgets := points.forgets.Load(); forgets != 0 {
		t.Fatalf("%d forgets were issued through the store the waits polled", forgets)
	}
	if rows := len(into.rows(t)); rows != 66 {
		t.Fatalf("the read model holds %d rows where sixty of the log and six of the marks are sixty-six", rows)
	}
}

// §6.8, §UC-242, §INV-111. The live half: a parking projection over a four-member
// cover, and three specs written by hand that differ from the derived one in
// exactly one field each. THE THREE ARMS ASSERT THE WRONG ANSWERS. Nothing in
// this framework can check any of them — a wait holds a Checkpoints and a Park
// and never a Spec — so the day one becomes checkable the arm fails and says so.
func TestTheDerivedSpecAgainstThreeHandWrittenOnesLive(t *testing.T) {
	const family = waitFamily + ".derived"
	stand := newProjectionCase(t, 24, 0)
	into := stand.destination(t, "derived_read_model")
	queue := stand.park(t, "derived_park")
	poison := poisoning("derived-poison")
	repo, fact := boundRepo(t, stand.store, family)
	ctx := context.WithoutCancel(t.Context())

	cover := quartered(t)
	parked := keyInPartition(t, family, aPartition(t, 2, 3), "derived-parked")

	var runners []*running
	var specs []projection.Spec
	for id := range uint32(4) {
		member := stand.parking(t, "derived", into, queue, poison)
		member.Partition = aPartition(t, id, 3)
		runners = append(runners, stand.run(t, member))
		specs = append(specs, member)
	}
	for id := range uint32(4) {
		key := keyInPartition(t, family, aPartition(t, id, 3), fmt.Sprintf("derived-warm-%d", id))
		stand.write(t, aStream(family, key), family+".held", fmt.Sprintf("derived-warm-%d", id))
	}

	commit := committed(t, ctx, repo, fact, parked, "derived-poison")
	waitFor(t, "the partition holding the caller's stream parked it", func() bool {
		return len(parkedIn(t, queue, identityOf(t, "derived"))) == 1
	})

	derived := waitingOver(t, specs[2], cover)
	derived.Until = markOf(t, ctx, derived, stand.store, commit)
	waitFor(t, "every member of the cover walked past the mark", func() bool {
		for id := range uint32(4) {
			row, found := stand.row(t, memberIdentity(t, "derived", projection.Ungenerated, id, 3).String())
			if !found || event.Position(row.highest) < derived.Until.At() {
				return false
			}
		}
		return true
	})

	bounded, cancel := context.WithTimeout(ctx, waitElapses)
	defer cancel()
	if _, err := projection.Wait(bounded, derived); !errors.Is(err, projection.ErrParked) {
		t.Fatalf("the derived spec answered %v, where the queue holds the caller's own sequence", err)
	}

	t.Run("a Sequence that is not the projection's", func(t *testing.T) {
		foreign := derived
		foreign.Sequence = projection.Unordered()
		foreign.Until = markOf(t, ctx, foreign, stand.store, commit)
		vis, err := projection.Wait(bounded, foreign)
		if err != nil || !vis.Reached {
			t.Fatalf("a hand-written spec naming another sequencer answered %+v and %v, and the arm asserts the wrong answer because nothing here can check that field", vis, err)
		}
	})

	t.Run("a nil Park", func(t *testing.T) {
		unqueued := derived
		unqueued.Park = nil
		vis, err := projection.Wait(bounded, unqueued)
		if err != nil || !vis.Reached {
			t.Fatalf("a hand-written spec with a nil Park answered %+v and %v, and the arm asserts the wrong answer because the field's zero value is the mistake", vis, err)
		}
	})

	t.Run("an Over that is not the cover the rows are recorded at", func(t *testing.T) {
		for _, runner := range runners {
			runner.stop(t)
		}
		beyond := committed(t, ctx, repo, fact, "derived/beyond", "derived-beyond")
		ahead := waitingOver(t, specs[0], cover)
		ahead.Park = nil
		ahead.Until = markOf(t, ctx, ahead, stand.store, beyond)

		// The row a generation recorded before it was partitioned. A runner is
		// refused one beside partitioned rows, and a split retires it rather than
		// leaving it, so it is written here through the store's own door: what
		// this arm is about is the set a census folds over, not the set a runner
		// may start against.
		leftover(t, stand, identityOf(t, "derived"), memberIdentity(t, "derived", projection.Ungenerated, 0, 3), ahead.Until.At()+100)

		short, stop := context.WithTimeout(ctx, 300*time.Millisecond)
		defer stop()
		if vis, err := projection.Wait(short, ahead); !errors.Is(err, projection.ErrNotVisible) || vis.Reached {
			t.Fatalf("the derived spec answered %+v and %v, where every member of its cover is behind a mark nothing applied", vis, err)
		}

		coarse := ahead
		coarse.Over = aWholeCover(t)
		vis, err := projection.Wait(bounded, coarse)
		if err != nil || !vis.Reached {
			t.Fatalf("a hand-written spec naming a cover the rows are not recorded at answered %+v and %v, and the arm asserts the wrong answer because a plausible wrong cover folds a minimum over the wrong set", vis, err)
		}
	})
}

// A checkpoint row for an identity no runner is recording at, written through the
// tracker door with a cursor this store minted for one that is. It is an
// operator's leftover row, and the only thing a case needs out of it is that the
// census folds it like any other.
func leftover(t *testing.T, stand *projectionCase, at, from projection.Identity, highest event.Position) {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	checkpoints := stand.checkpoints(t)
	source, err := event.Track(checkpoints, from.String())
	if err != nil {
		t.Fatalf("a tracker over %q was refused: %v", from, err)
	}
	minted, err := source.Load(ctx)
	if err != nil {
		t.Fatalf("reading the row of %q answered %v", from, err)
	}
	if minted.Cursor == "" {
		t.Fatalf("%q holds no cursor, so there is none this store minted to write beside", from)
	}
	tracker, err := event.Track(checkpoints, at.String())
	if err != nil {
		t.Fatalf("a tracker over %q was refused: %v", at, err)
	}
	if _, err := tracker.Load(ctx); err != nil {
		t.Fatalf("reading the row of %q answered %v", at, err)
	}
	if _, err := tracker.Save(ctx, minted.Cursor, event.Progress{Highest: highest, At: time.Now()}); err != nil {
		t.Fatalf("writing the leftover row of %q answered %v", at, err)
	}
}

// §6.9, §UC-244, §INV-113. The poll number is the whole of the rule, live and on
// the two states a deployment actually reaches. A poll that never worked is the
// caller asking wrong and is answered on the round trip it was going to pay
// anyway; a poll that stops working is the deployment moving — a split in
// progress — and an operator's split must not abort a request path's wait.
func TestAPollThatFailsFirstAndAPollThatFailsFourth(t *testing.T) {
	const family = waitFamily + ".polled"
	stand := newProjectionCase(t, 16, 0)
	into := stand.destination(t, "polled_read_model")
	repo, fact := boundRepo(t, stand.store, family)
	ctx := context.WithoutCancel(t.Context())

	points := &countedCheckpoints{Checkpoints: stand.checkpoints(t)}
	spec := inUnit(stand.spec(t, "polled", into.inserts(&deliveries{}, nil)), stand.source, into.source)
	spec.Checkpoints = points
	stand.write(t, aStream(family, "polled/warm"), family+".held", "polled-warm")
	runner := stand.run(t, spec)
	runner.following(t, "the projection drained the log it starts on")
	runner.stop(t)

	commit := committed(t, ctx, repo, fact, "polled/A", "polled-1")
	waiting := waitingOver(t, spec, aWholeCover(t))
	waiting.Until = markOf(t, ctx, waiting, stand.store, commit)
	of := identityOf(t, "polled")

	t.Run("a first poll that cannot be made returns at once", func(t *testing.T) {
		held := waiting
		held.Ticks = neverBeating().Ticks
		points.loads.Store(0)
		points.failing(1, 0)
		defer points.failing(0, 0)

		// A deadline this arm must never reach: a first-poll refusal is answered
		// on the round trip the caller was going to pay anyway, so an
		// implementation that polled through instead fails here rather than
		// hanging.
		bounded, cancel := context.WithTimeout(ctx, waitElapses)
		defer cancel()
		vis, err := projection.Wait(bounded, held)
		if !errors.Is(err, projection.ErrTopology) || !errors.Is(err, event.ErrBackend) || !errors.Is(event.CauseOf(err), errBlinkedCheckpointRow) {
			t.Fatalf("a first-poll failure answered %v, where the census names the member it could not read and the kernel's own read door classifies what the store said", err)
		}
		if _, census := projection.Observe(bounded, points, of, aWholeCover(t)); census.Error() != err.Error() {
			t.Fatalf("the wait answered %q where the census it polls answers %q, and a first-poll refusal travels unwrapped and unreclassified", err, census)
		}
		if vis != (projection.Visibility{Polls: 1}) {
			t.Fatalf("a first-poll failure answered %+v, where nothing was read and the wait made one poll", vis)
		}
	})

	t.Run("a poll that stops working is polled through and the deadline decides", func(t *testing.T) {
		held := waiting
		driven := neverBeating()
		held.Ticks = driven.Ticks
		points.loads.Store(0)
		defer points.failing(0, 0)

		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		answers := awaiting(bounded, held)
		driven.fire(t)
		driven.fire(t)
		points.failing(4, 0)
		driven.fire(t)
		got := answeredWithin(t, answers)

		if !errors.Is(got.err, projection.ErrNotVisible) || !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("a deadline reached over failing polls answered %v, where a caller that branches on either gets what it expects", got.err)
		}
		if !errors.Is(got.err, projection.ErrTopology) || !errors.Is(event.CauseOf(got.err), errBlinkedCheckpointRow) {
			t.Fatalf("a deadline reached over failing polls answered %v, where the last poll's own refusal is wrapped into it so a caller that asks why gets the reason rather than a timeout that means four things", got.err)
		}
		if got.vis.Polls <= 3 {
			t.Fatalf("the wait made %d polls where three of them succeeded before the first failure, so polled-through is assumed rather than measured", got.vis.Polls)
		}
		row := storedCheckpoint(t, stand.schema, "polled")
		if got.vis.At != event.Position(row.highest) {
			t.Fatalf("the answer is %+v where the row this process read stands at %d, and the Visibility is the last poll that COULD be read", got.vis, row.highest)
		}
	})

	t.Run("the control: the same wiring against a healthy checkpoint store reaches", func(t *testing.T) {
		held := waiting
		held.Every = 20 * time.Millisecond
		points.failing(0, 0)
		restarted := stand.run(t, spec)
		defer restarted.stop(t)

		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		vis, err := projection.Wait(bounded, held)
		if err != nil || !vis.Reached {
			t.Fatalf("the wiring every arm above differs from in which load fails answered %+v and %v", vis, err)
		}
	})

	t.Run("a cover taken mid-split", func(t *testing.T) {
		const family = waitFamily + ".split"
		stand := newProjectionCase(t, 16, 0)
		into := stand.destination(t, "split_read_model")
		repo, fact := boundRepo(t, stand.store, family)
		ctx := context.WithoutCancel(t.Context())

		cover := coverOf(t, aPartition(t, 0, 1), aPartition(t, 1, 1))
		var specs []projection.Spec
		var runners []*running
		for id := range uint32(2) {
			member := inUnit(stand.spec(t, "split", into.inserts(&deliveries{}, nil)), stand.source, into.source)
			member.Partition = aPartition(t, id, 1)
			runners = append(runners, stand.run(t, member))
			specs = append(specs, member)
		}
		for id := range uint32(2) {
			key := keyInPartition(t, family, aPartition(t, id, 1), fmt.Sprintf("split-warm-%d", id))
			stand.write(t, aStream(family, key), family+".held", fmt.Sprintf("split-warm-%d", id))
		}
		waitFor(t, "both members of the cover recorded a row", func() bool {
			for id := range uint32(2) {
				if _, found := stand.row(t, memberIdentity(t, "split", projection.Ungenerated, id, 1).String()); !found {
					return false
				}
			}
			return true
		})
		// The rows stand still from here: a split is a topology change an
		// operator makes to a stopped share, and neither member may walk past the
		// mark while the arms below are about a cover that cannot be folded.
		for _, one := range runners {
			one.stop(t)
		}

		commit := committed(t, ctx, repo, fact, "split/A", "split-1")
		waiting := waitingOver(t, specs[0], cover)
		waiting.Until = markOf(t, ctx, waiting, stand.store, commit)
		parent := memberIdentity(t, "split", projection.Ungenerated, 0, 1)

		t.Run("a split that starts while the wait is running is polled through", func(t *testing.T) {
			held := waiting
			driven := neverBeating()
			held.Ticks = driven.Ticks
			// Neither member may reach the mark while this arm runs, so the two
			// runners are stopped and the rows stand where they are.
			bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			answers := awaiting(bounded, held)
			driven.fire(t)
			if _, _, err := splitting(t, stand, stand.checkpoints(t), parent); err != nil {
				t.Fatalf("splitting %q answered %v", parent, err)
			}
			driven.fire(t)
			got := answeredWithin(t, answers)

			if !errors.Is(got.err, projection.ErrNotVisible) || !errors.Is(got.err, context.DeadlineExceeded) {
				t.Fatalf("a deadline reached over a cover that went mid-split answered %v", got.err)
			}
			if !errors.Is(got.err, projection.ErrTopology) {
				t.Fatalf("the deadline answered %v, where the split the operator ran travels wrapped into it", got.err)
			}
			if got.vis.Polls < 3 {
				t.Fatalf("the wait made %d polls, where an operator's split must not abort a request path's wait", got.vis.Polls)
			}
		})

		t.Run("the same cover, with the split already made when the wait starts, is terminal on the first poll", func(t *testing.T) {
			held := waiting
			held.Ticks = neverBeating().Ticks
			bounded, cancel := context.WithTimeout(ctx, waitElapses)
			defer cancel()
			vis, err := projection.Wait(bounded, held)
			if !errors.Is(err, projection.ErrTopology) || errors.Is(err, projection.ErrNotVisible) {
				t.Fatalf("a cover half of whose members were split away answered %v on its first poll, where the caller is told on the round trip it was going to pay anyway", err)
			}
			if vis != (projection.Visibility{Polls: 1}) {
				t.Fatalf("the refusal carries %+v, where nothing was folded and the wait made one poll", vis)
			}

			t.Run("the control: the cover the children record at is not refused", func(t *testing.T) {
				lower, higher := childrenOf(t, parent)
				children := coverOf(t, lower.Partition(), higher.Partition(), aPartition(t, 1, 1))
				after := waiting
				after.Over = children
				after.Ticks = neverBeating().Ticks
				short, stop := context.WithTimeout(ctx, 300*time.Millisecond)
				defer stop()
				if _, err := projection.Wait(short, after); errors.Is(err, projection.ErrTopology) {
					t.Fatalf("the cover the split handed the rows down to answered %v, so the refusal above is about the store and not about the cover", err)
				}
			})
		})
	})
}

// §6.10, §UC-245, §INV-113. Both directions of naming the wrong generation are
// silent, and one of them is the exact outcome ES-05 exists to forbid: a true
// statement about a read model the caller is no longer reading. The ownership row
// moves through the same fenced statement a cutover issues, because what the wait
// reads is that row.
func TestACutoverThatCommitsWhileAWaitIsRunning(t *testing.T) {
	const family = waitFamily + ".cutover"
	stand := newProjectionCase(t, 16, 0)
	first := stand.destination(t, "cutover_g2_read_model")
	second := stand.destination(t, "cutover_g3_read_model")
	rows := stand.generations(t, "cutover_generations")
	repo, fact := boundRepo(t, stand.store, family)
	ctx := context.WithoutCancel(t.Context())

	if err := rows.Activate(ctx, "cutover", projection.Ungenerated, 2); err != nil {
		t.Fatalf("activating the generation this deployment reads answered %v", err)
	}

	stop := flowing()
	t.Cleanup(stop.let)
	retiring := stand.generational(t, "cutover", 2, first)
	retiring.Generations = rows
	retiring.Handler = first.insertsWhile(stop)
	runner := stand.run(t, retiring)
	stand.write(t, aStream(family, "cutover/warm"), family+".held", "cutover-warm")
	runner.following(t, "the live generation drained the log it starts on")

	stop.hold()
	commit := committed(t, ctx, repo, fact, "cutover/A", "cutover-1")

	arriving := stand.generational(t, "cutover", 3, second)
	arriving.Generations = rows

	t.Run("the arriving generation before its cutover, refused on the first poll", func(t *testing.T) {
		waiting := waitingOver(t, arriving, aWholeCover(t))
		waiting.Until = markOf(t, ctx, waiting, stand.store, commit)
		waiting.Ticks = neverBeating().Ticks

		bounded, cancel := context.WithTimeout(ctx, waitElapses)
		defer cancel()
		vis, err := projection.Wait(bounded, waiting)
		if !errors.Is(err, projection.ErrGeneration) {
			t.Fatalf("a wait on a generation reads do not resolve to answered %v, where the caller pays one row read instead of a whole deadline", err)
		}
		if vis.Polls != 1 {
			t.Fatalf("the refusal came on poll %d, where the first read is what costs the caller nothing instead of a deadline", vis.Polls)
		}

		t.Run("the control: the same wait with Generations nil burns the deadline", func(t *testing.T) {
			unscoped := waitingOver(t, arriving, aWholeCover(t))
			unscoped.Generations = nil
			unscoped.Until = waiting.Until
			unscoped.Ticks = neverBeating().Ticks
			short, stop := context.WithTimeout(ctx, 300*time.Millisecond)
			defer stop()
			if vis, err := projection.Wait(short, unscoped); !errors.Is(err, projection.ErrNotVisible) || vis.Reached {
				t.Fatalf("the same wait with no ownership row answered %+v and %v, and a nil Generations scopes the promise rather than widening it", vis, err)
			}
		})
	})

	t.Run("the retiring generation, cut over between the first poll and the poll that would reach", func(t *testing.T) {
		waiting := waitingOver(t, retiring, aWholeCover(t))
		waiting.Until = markOf(t, ctx, waiting, stand.store, commit)
		driven := neverBeating()
		waiting.Ticks = driven.Ticks
		// A value of the wait's own, for its census-read count: the first poll
		// must have read a row below the mark before anything advances it, or the
		// cutover below lands beside a poll that had already reached rather than
		// on the poll that would have.
		points := &countedCheckpoints{Checkpoints: stand.checkpoints(t)}
		waiting.Checkpoints = points

		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		answers := awaiting(bounded, waiting)
		waitFor(t, "the first poll read the census", func() bool { return points.loads.Load() >= 1 })

		stop.let()
		waitFor(t, "the live generation applied the caller's own commit", func() bool {
			held, found := stand.row(t, generationalIdentity(t, "cutover", 2).String())
			return found && event.Position(held.highest) >= waiting.Until.At()
		})
		if err := rows.Activate(ctx, "cutover", 2, 3); err != nil {
			t.Fatalf("the cutover answered %v", err)
		}
		driven.fire(t)
		got := answeredWithin(t, answers)

		if !errors.Is(got.err, projection.ErrGeneration) {
			t.Fatalf("a wait on the retiring generation answered %v, where a true statement about a read model the caller is no longer reading is the stale read this exists to forbid", got.err)
		}
		if got.vis.Reached {
			t.Fatalf("the refusal carries %+v, and a refused wait has concluded nothing about the read model", got.vis)
		}
		if generation := rows.recorded(t, "cutover"); generation != 3 {
			t.Fatalf("the ownership row resolves reads to generation %d, so the refusal above is about something else", generation)
		}

		t.Run("the control: the same wait with Generations nil answers Reached", func(t *testing.T) {
			unscoped := waitingOver(t, retiring, aWholeCover(t))
			unscoped.Generations = nil
			unscoped.Until = waiting.Until
			unscoped.Ticks = neverBeating().Ticks
			vis, err := projection.Wait(bounded, unscoped)
			if err != nil || !vis.Reached {
				t.Fatalf("the same wait with no ownership row answered %+v and %v, which is the behaviour the field exists to change", vis, err)
			}
		})
	})

	// The same cutover one poll earlier, and this is the poll a caught-up
	// deployment takes every time: the generation is already past the mark, so
	// the wait reaches on poll 1 and the window the second ownership read closes
	// is the park round trip and the census round trip of that one poll.
	t.Run("the retiring generation, cut over inside the census of the first poll", func(t *testing.T) {
		// A deployment that rolled its cutover back, which is what makes the
		// retiring generation the one reads resolve to again. Nothing else about
		// the stand changes: the row is where the arm above left it, past the mark.
		if err := rows.Activate(ctx, "cutover", 3, 2); err != nil {
			t.Fatalf("pointing the ownership row back at the generation this arm waits on answered %v", err)
		}
		waiting := waitingOver(t, retiring, aWholeCover(t))
		waiting.Until = markOf(t, ctx, waiting, stand.store, commit)
		waiting.Ticks = neverBeating().Ticks
		points := &countedCheckpoints{Checkpoints: stand.checkpoints(t)}
		hold := holdingLoad(1)
		points.hold.Store(hold)
		waiting.Checkpoints = points

		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		answers := awaiting(bounded, waiting)
		hold.reached(t)
		if err := rows.Activate(ctx, "cutover", 2, 3); err != nil {
			t.Fatalf("the cutover answered %v", err)
		}
		hold.let()
		got := answeredWithin(t, answers)

		if !errors.Is(got.err, projection.ErrGeneration) || got.vis.Reached {
			t.Fatalf("a cutover that committed inside the first poll's census answered %+v and %v, where reaching the mark in the retiring generation is a true statement about a read model the caller is no longer reading", got.vis, got.err)
		}
		if got.vis.Polls != 1 {
			t.Fatalf("the refusal came on poll %d, and what this arm is about is the poll a caught-up deployment reaches on", got.vis.Polls)
		}
		if got.vis.At < waiting.Until.At() {
			t.Fatalf("the refusal carries %+v against a mark at %d, so the census had not reached and this arm refused for something other than the cutover", got.vis, waiting.Until.At())
		}
		if generation := rows.recorded(t, "cutover"); generation != 3 {
			t.Fatalf("the ownership row resolves reads to generation %d, so the refusal above is about something else", generation)
		}
	})
}
