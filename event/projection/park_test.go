package projection_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

// The application's own queue and its operator half, in memory, with everything
// the contract names and nothing it does not: the letters of a sequence in
// insert order, the two bounds and the byte one, the claims a redrive is
// exclusive by, and the evicted-unapplied letters Holes counts.
//
// Its writes are staged in the unit of work the stand's Unit opened and land
// only when that unit commits — which is the whole point of a queue that lives
// in the application's database. Reads consult the committed rows plus this
// unit's own staged ones, so a page that parks its way into the bound is refused
// on what it has written rather than on what it wrote last time.
type park struct {
	mutex   sync.Mutex
	held    map[projection.Identity]map[string]*sequence
	skipped map[projection.Identity]uint64
	granted map[projection.Identity]map[string]string
	minted  int
	clock   int64

	maxSequences int
	maxLetters   int
	maxBytes     int

	// The mistake the two-dimensional bound exists to prevent, so a case can
	// measure what it costs rather than assert that it is not made: one isFull()
	// over the whole queue, which refuses a letter behind a blocker in a sequence
	// the queue is already holding.
	flat bool

	sequences atomic.Int64
	holds     atomic.Int64
	written   atomic.Int64
	holes     atomic.Int64
	outside   atomic.Int64
	released  atomic.Int64

	refuses     error
	uncountable error
	unaskable   error

	// Called before a claim is taken, so a case can cause the contention two
	// operators race for rather than hope for it.
	gate func()
}

// The letters of one sequence and when it was last tried, which is what the
// rotation orders by: a redrive that always picked the same failing sequence
// would starve every other one.
type sequence struct {
	letters []projection.Letter
	tried   int64
}

// One staged write. The journal is data rather than a closure because the bound
// is asked of what this unit has already written, and a closure cannot be read.
type staged struct {
	of       projection.Identity
	sequence string
	letter   projection.Letter
	parked   bool
	applied  bool
}

// The unit of work as the application's own resources see it: a journal the park
// reads back, and the writes every other resource lands when it commits.
type unitOfWork struct {
	journal []staged
	landing []func()
}

func (this *unitOfWork) land() {
	if this == nil {
		return
	}
	for _, write := range this.landing {
		write()
	}
	this.landing = nil
}

// A read model whose rows are staged in the unit of work the handler was given,
// which is what a row in the caller's own database is. The stand's own model is
// written straight through, so a case that asserts a rollback needs this one:
// over a Go slice, "the unit did not commit" and "the unit committed" hold the
// same rows.
type ledger struct {
	mutex sync.Mutex
	held  []string
}

func (this *ledger) write(ctx context.Context, rows ...string) {
	work := unitOfWorkIn(ctx)
	if work == nil {
		this.landed(rows...)
		return
	}
	work.landing = append(work.landing, func() { this.landed(rows...) })
}

func (this *ledger) landed(rows ...string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.held = append(this.held, rows...)
}

func (this *ledger) rows() []string {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return append([]string(nil), this.held...)
}

// The handler every case that is about the queue rather than about the handler
// uses: it applies what it is given into a read model that rolls back.
func appliesInto(held *ledger) projection.HandlerFunc {
	return func(ctx context.Context, batch projection.Batch) error {
		held.write(ctx, payloadsOf(batch.Envelopes)...)
		return nil
	}
}

type unitKey struct{}

func withUnitOfWork(ctx context.Context, work *unitOfWork) context.Context {
	if work == nil {
		return ctx
	}
	return context.WithValue(ctx, unitKey{}, work)
}

func unitOfWorkIn(ctx context.Context) *unitOfWork {
	held, _ := ctx.Value(unitKey{}).(*unitOfWork)
	return held
}

func newPark() *park {
	return &park{
		held:    map[projection.Identity]map[string]*sequence{},
		skipped: map[projection.Identity]uint64{},
		granted: map[projection.Identity]map[string]string{},
	}
}

func (this *park) opened() *unitOfWork {
	if this == nil {
		return nil
	}
	return &unitOfWork{}
}

func (this *park) commit(work *unitOfWork) {
	if this == nil || work == nil {
		return
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	for _, write := range work.journal {
		this.apply(write)
	}
	work.journal = nil
}

func (this *park) discard(work *unitOfWork) {
	if this == nil || work == nil {
		return
	}
	work.journal = nil
}

func (this *park) apply(write staged) {
	if write.parked {
		held, found := this.held[write.of]
		if !found {
			held = map[string]*sequence{}
			this.held[write.of] = held
		}
		queue, found := held[write.sequence]
		if !found {
			this.clock++
			queue = &sequence{tried: this.clock}
			held[write.sequence] = queue
		}
		queue.letters = append(queue.letters, write.letter)
		return
	}
	queue, found := this.held[write.of][write.sequence]
	if !found {
		return
	}
	for index, letter := range queue.letters {
		if letter.Envelope.Position != write.letter.Envelope.Position {
			continue
		}
		queue.letters = append(queue.letters[:index], queue.letters[index+1:]...)
		break
	}
	if !write.applied {
		this.skipped[write.of]++
	}
	if len(queue.letters) == 0 {
		delete(this.held[write.of], write.sequence)
	}
}

// The committed letters of one identity with this unit's staged ones applied on
// top, which is what a transaction reading its own writes sees.
func (this *park) queues(work *unitOfWork, of projection.Identity) map[string][]projection.Letter {
	seen := map[string][]projection.Letter{}
	for named, queue := range this.held[of] {
		seen[named] = append([]projection.Letter(nil), queue.letters...)
	}
	if work == nil {
		return seen
	}
	for _, write := range work.journal {
		if write.of != of {
			continue
		}
		if write.parked {
			seen[write.sequence] = append(seen[write.sequence], write.letter)
			continue
		}
		for index, letter := range seen[write.sequence] {
			if letter.Envelope.Position != write.letter.Envelope.Position {
				continue
			}
			seen[write.sequence] = append(seen[write.sequence][:index], seen[write.sequence][index+1:]...)
			break
		}
		if len(seen[write.sequence]) == 0 {
			delete(seen, write.sequence)
		}
	}
	return seen
}

func (this *park) Sequences(ctx context.Context, of projection.Identity) (uint64, error) {
	this.sequences.Add(1)
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.uncountable != nil {
		return 0, this.uncountable
	}
	return uint64(len(this.queues(unitOfWorkIn(ctx), of))), nil
}

func (this *park) Holds(ctx context.Context, of projection.Identity, named string) (bool, error) {
	this.holds.Add(1)
	work := unitOfWorkIn(ctx)
	if work == nil {
		this.outside.Add(1)
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.unaskable != nil {
		return false, this.unaskable
	}
	return len(this.queues(work, of)[named]) > 0, nil
}

// The bound is asked per sequence and never per queue, which is the difference
// between a queue holding 1023 sequences of one letter that still takes a 1024th
// letter into an existing sequence and one that does not.
func (this *park) Park(ctx context.Context, letter projection.Letter) error {
	this.written.Add(1)
	work := unitOfWorkIn(ctx)
	if work == nil {
		this.outside.Add(1)
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.refuses != nil {
		return this.refuses
	}
	if err := this.room(work, letter); err != nil {
		return err
	}
	write := staged{of: letter.Identity, sequence: letter.Sequence, letter: letter, parked: true}
	if work == nil {
		this.apply(write)
		return nil
	}
	work.journal = append(work.journal, write)
	return nil
}

func (this *park) room(work *unitOfWork, letter projection.Letter) error {
	seen := this.queues(work, letter.Identity)
	if this.flat && this.maxSequences > 0 && len(seen) >= this.maxSequences {
		return fmt.Errorf("%w: the queue holds %d sequences", projection.ErrParkFull, len(seen))
	}
	if held, found := seen[letter.Sequence]; found {
		if this.maxLetters > 0 && len(held) >= this.maxLetters {
			return fmt.Errorf("%w: the sequence %q holds %d letters", projection.ErrParkFull, letter.Sequence, len(held))
		}
	} else if this.maxSequences > 0 && len(seen) >= this.maxSequences {
		return fmt.Errorf("%w: the queue holds %d sequences", projection.ErrParkFull, len(seen))
	}
	if this.maxBytes == 0 {
		return nil
	}
	written := len(letter.Envelope.Payload)
	for _, held := range seen {
		for _, queued := range held {
			written += len(queued.Envelope.Payload)
		}
	}
	if written > this.maxBytes {
		return fmt.Errorf("%w: the queue would hold %d bytes of payload", projection.ErrParkFull, written)
	}
	return nil
}

// Two counts and not a column, so it cannot drift from the rows it summarises:
// the letters queued now, plus the letters an operator evicted without applying.
func (this *park) Holes(ctx context.Context, of projection.Identity) (uint64, error) {
	this.holes.Add(1)
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held := uint64(0)
	for _, letters := range this.queues(unitOfWorkIn(ctx), of) {
		held += uint64(len(letters))
	}
	return held + this.skipped[of], nil
}

func (this *park) Claim(_ context.Context, of projection.Identity, named string) (projection.Claim, bool, error) {
	if this.gate != nil {
		this.gate()
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	taken := this.granted[of]
	if taken == nil {
		taken = map[string]string{}
		this.granted[of] = taken
	}
	if named == "" {
		named = this.oldest(of, taken)
	}
	if named == "" || len(queued(this.held[of][named])) == 0 || taken[named] != "" {
		return projection.Claim{}, false, nil
	}
	this.minted++
	token := "grant-" + strconv.Itoa(this.minted)
	taken[named] = token
	return projection.Claim{Of: of, Sequence: named, Token: token, Until: time.Now().Add(time.Minute)}, true, nil
}

// Least recently tried first, and unclaimed: a rotation that picked at random
// would lose the fairness and one that always picked the same sequence would
// starve every other one.
func (this *park) oldest(of projection.Identity, taken map[string]string) string {
	held, at := "", int64(0)
	for named, queue := range this.held[of] {
		if len(queue.letters) == 0 || taken[named] != "" {
			continue
		}
		if held == "" || queue.tried < at {
			held, at = named, queue.tried
		}
	}
	return held
}

func (this *park) Sequence(_ context.Context, claim projection.Claim) ([]projection.Letter, error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if err := this.owns(claim); err != nil {
		return nil, err
	}
	return append([]projection.Letter(nil), queued(this.held[claim.Of][claim.Sequence])...), nil
}

func (this *park) Evict(ctx context.Context, claim projection.Claim, letter projection.Letter) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if err := this.owns(claim); err != nil {
		return err
	}
	write := staged{of: claim.Of, sequence: claim.Sequence, letter: letter, applied: true}
	work := unitOfWorkIn(ctx)
	if work == nil {
		this.apply(write)
		return nil
	}
	work.journal = append(work.journal, write)
	return nil
}

func (this *park) Touch(_ context.Context, claim projection.Claim, cause error) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if err := this.owns(claim); err != nil {
		return err
	}
	queue := this.held[claim.Of][claim.Sequence]
	if queue == nil || len(queue.letters) == 0 {
		return nil
	}
	this.clock++
	queue.tried = this.clock
	queue.letters[0].Cause = cause
	queue.letters[0].Attempt++
	return nil
}

// The call is counted before the grant is compared, because what a case about a
// lost claim has to see is whether the framework made the call at all: an
// implementation that answers ErrClaimLost produces the same outstanding grants
// as one that was never asked.
func (this *park) Release(_ context.Context, claim projection.Claim) error {
	this.released.Add(1)
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if err := this.owns(claim); err != nil {
		return err
	}
	delete(this.granted[claim.Of], claim.Sequence)
	return nil
}

// The grant travels on every write it authorises, so a caller whose claim
// expired mid-drain is refused rather than applying a letter a second caller is
// already applying.
func (this *park) owns(claim projection.Claim) error {
	if this.granted[claim.Of][claim.Sequence] != claim.Token {
		return fmt.Errorf("%w: %q is held by %q and this call carries %q", projection.ErrClaimLost, claim.Sequence, this.granted[claim.Of][claim.Sequence], claim.Token)
	}
	return nil
}

// The grants outstanding, which is what says a claim was released on the way out
// — a sequence nobody released is undrainable until the application's clock
// expires it.
func (this *park) claims(of projection.Identity) []string {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held := make([]string, 0, len(this.granted[of]))
	for _, token := range this.granted[of] {
		held = append(held, token)
	}
	return held
}

// What a case changes about a queue a loop is already reading, which is every
// one of them: the fields are read under the lock the rows are.
func (this *park) bounded(sequences, letters, bytes int) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.maxSequences, this.maxLetters, this.maxBytes = sequences, letters, bytes
}

func (this *park) oneDimensional() {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.flat = true
}

func (this *park) failing(refuses, uncountable, unaskable error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.refuses, this.uncountable, this.unaskable = refuses, uncountable, unaskable
}

// What the application's own clock does to a grant nobody released, and the one
// event two simultaneous Claims cannot see.
func (this *park) expire(of projection.Identity, named string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	delete(this.granted[of], named)
}

// What an operator does when a letter will never apply: it leaves the queue
// without being applied, which is a hole, and nothing is decremented.
func (this *park) skip(of projection.Identity, named string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	queue := this.held[of][named]
	if queue == nil || len(queue.letters) == 0 {
		return
	}
	this.apply(staged{of: of, sequence: named, letter: queue.letters[0]})
}

// A letter written the way the loop writes one, for a case that starts from a
// queue that is already holding something.
func (this *park) seed(of projection.Identity, sequencer, named string, envelopes ...event.Envelope) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	for _, envelope := range envelopes {
		this.apply(staged{of: of, sequence: named, parked: true, letter: projection.Letter{
			Identity:  of,
			Sequencer: sequencer,
			Sequence:  named,
			Envelope:  envelope,
			Attempt:   1,
		}})
	}
}

func (this *park) letters(of projection.Identity, named string) []projection.Letter {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return append([]projection.Letter(nil), queued(this.held[of][named])...)
}

func (this *park) names(of projection.Identity) []string {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held := make([]string, 0, len(this.held[of]))
	for named := range this.held[of] {
		held = append(held, named)
	}
	return held
}

// The one wiring ParkSequence constructs at, and the whole of what a case has to
// say to get it: one transaction for the checkpoint store and the read model,
// the queue staged inside it, and the policy that writes to it.
func (this *stand) parking(spec projection.Spec, held *park) projection.Spec {
	this.park = held
	one := &transaction{named: "one resource"}
	spec = this.inUnit(spec, one, one, nil)
	spec.OnPermanentFailure = projection.ParkSequence
	spec.Park = held
	return spec
}

// The key ByStream() answers for a stream of this stand's, asked of the
// published sequencer rather than spelled out: a case that hard-coded the
// rendering would pin the spelling instead of the rule.
func sequenceOf(key string) string {
	return projection.ByStream().SequenceOf(event.Envelope{Stream: event.Stream{Family: "orders", Key: event.Key(key)}})
}

func queued(held *sequence) []projection.Letter {
	if held == nil {
		return nil
	}
	return held.letters
}

// The whole of ES-03 in one page, and the assertion that separates a
// dead-letter queue from a skip list is the third envelope: A3 is parked WITHOUT
// EVER REACHING THE HANDLER, because the order it belongs to is blocked by A2.
// A projector that passed A2 to a sink and carried on would apply OrderPaid over
// an order whose OrderCreated was never applied, with no error on any path.
func TestAPermanentFailureParksItsSequenceAndTheEventsBehindIt(t *testing.T) {
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)

	t.Run("the failing envelope and every later one of its sequence", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")
		stand.append(t, "a", "A2")
		stand.append(t, "b", "B1")
		stand.append(t, "a", "A3")
		stand.append(t, "b", "B2")

		held := newPark()
		var seen delivered
		spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			seen.took(batch)
			for _, envelope := range batch.Envelopes {
				if string(envelope.Payload) == "A2" {
					return permanent
				}
			}
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		}))
		running(t, newProjection(t, stand.parking(spec, held)))

		state := stand.observer.await(t, "the whole log was read over a queue that is holding something", func(state projection.State) bool {
			return state.Phase == projection.PhaseDegraded
		})
		if state.Progress.Quarantined != 2 {
			t.Fatalf("the projection reports %d quarantined envelopes, where the one that failed and the one behind it are two", state.Progress.Quarantined)
		}
		if rows := stand.model.rows(); !same(rows, []string{"A1", "B1", "B2"}) {
			t.Fatalf("the read model holds %v, where A1 applies, A2 fails, A3 is behind it and both of B's events are of another sequence", rows)
		}
		if seen.redelivered("A3") {
			t.Fatal("A3 was handed to the handler again after the page it was in had failed as a whole and rolled back, and an envelope behind a parked one is never delivered once the failure is known — this is a skip list with a queue's name")
		}
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		letters := held.letters(of, sequenceOf("a"))
		if len(letters) != 2 {
			t.Fatalf("the park holds %d letters for the sequence a, where the failing envelope and the one behind it are two", len(letters))
		}
		if string(letters[0].Envelope.Payload) != "A2" || !errors.Is(letters[0].Cause, event.ErrPayload) {
			t.Fatalf("the first letter is %+v, where the envelope that failed carries its own cause", letters[0])
		}
		if string(letters[1].Envelope.Payload) != "A3" || letters[1].Cause != nil {
			t.Fatalf("the second letter is %+v, where an envelope parked behind another never reached a handler and has no cause of its own", letters[1])
		}
		if names := held.names(of); len(names) != 1 {
			t.Fatalf("the park holds the sequences %v, where one order failed", names)
		}
		row := stand.row(t, "orders")
		if row.Advance != 1 || row.Progress.Applied != 3 || row.Progress.Quarantined != 2 {
			t.Fatalf("the row stands at advance %d with %d applied and %d quarantined, where one page advanced once, applied three and parked two", row.Advance, row.Progress.Applied, row.Progress.Quarantined)
		}
		if state.Parked != 1 {
			t.Fatalf("the projection reports %d parked sequences where one order is blocked", state.Parked)
		}
		if asked := held.holds.Load(); asked != 0 {
			t.Fatalf("the pass asked Holds %d times over a queue that was empty when it began, and the fast path is what makes a healthy projection free", asked)
		}
	})

	t.Run("the control: the same page with nothing failing applies all five", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")
		stand.append(t, "a", "A2")
		stand.append(t, "b", "B1")
		stand.append(t, "a", "A3")
		stand.append(t, "b", "B2")

		held := newPark()
		spec := stand.spec("orders", applies(stand.model))
		running(t, newProjection(t, stand.parking(spec, held)))

		stand.observer.await(t, "the page applied", func(state projection.State) bool {
			return state.Progress.Applied == 5
		})
		if rows := stand.model.rows(); !same(rows, []string{"A1", "A2", "B1", "A3", "B2"}) {
			t.Fatalf("the read model holds %v where nothing failed, so the blocking above is not attributable to the failure", rows)
		}
		if written := held.written.Load(); written != 0 {
			t.Fatalf("%d letters were parked by a page nothing refused", written)
		}
	})
}

// The existence check on the hot path, which is the whole causal-order guarantee
// in one question per envelope: a sequence the queue already holds takes the
// next event of that sequence too, and it is asked inside the unit the letter is
// written in.
func TestALaterPageParksWhatTheQueueAlreadyHolds(t *testing.T) {
	t.Run("a page over a queue that is holding one sequence", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		held.seed(of, "by-stream", sequenceOf("a"), event.Envelope{
			Stream:   event.Stream{Family: "orders", Key: "a"},
			Position: 1,
			Payload:  []byte("A1"),
		})
		stand.append(t, "a", "A4")
		stand.append(t, "c", "C1")

		var seen delivered
		spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			seen.took(batch)
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		}))
		running(t, newProjection(t, stand.parking(spec, held)))

		state := stand.observer.await(t, "the whole log was read over a queue that is holding something", func(state projection.State) bool {
			return state.Phase == projection.PhaseDegraded
		})
		if state.Progress.Quarantined != 1 {
			t.Fatalf("the projection reports %d quarantined envelopes, where the one whose sequence the queue already held is one", state.Progress.Quarantined)
		}
		if rows := stand.model.rows(); !same(rows, []string{"C1"}) {
			t.Fatalf("the read model holds %v, where A4 is behind a parked sequence and C1 is of another", rows)
		}
		if seen.reached("A4") {
			t.Fatal("A4 reached the handler over a sequence the queue was already holding")
		}
		if letters := held.letters(of, sequenceOf("a")); len(letters) != 2 || string(letters[1].Envelope.Payload) != "A4" {
			t.Fatalf("the sequence a holds %d letters and the second is not A4, so the later event went somewhere else", len(letters))
		}
		if asked := held.holds.Load(); asked != 2 {
			t.Fatalf("the pass asked Holds %d times over a page of two envelopes, and the check is one question per envelope", asked)
		}
		if outside := held.outside.Load(); outside != 0 {
			t.Fatalf("%d of the park's calls ran outside the caller's unit of work, and a blocking test made outside the transaction the letter is written in orders nothing", outside)
		}
	})

	t.Run("the control: with the queue empty the same page asks nothing", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		stand.append(t, "a", "A4")
		stand.append(t, "c", "C1")
		running(t, newProjection(t, stand.parking(stand.spec("orders", applies(stand.model)), held)))

		stand.observer.await(t, "the page applied", func(state projection.State) bool {
			return state.Progress.Applied == 2
		})
		if asked := held.holds.Load(); asked != 0 {
			t.Fatalf("the pass asked Holds %d times over an empty queue, so the two calls above are not the queue's contents", asked)
		}
	})
}

// The one clearing rule, measured at both ends. A healthy projection pays one
// call per resume and nothing per pass; a degraded one pays one per pass and one
// question per envelope; and the pass after a redrive empties the queue reads
// zero and both counts return to the healthy ones. The rejected rule — read once
// and never again — leaves State.Parked meaning "parked since this instance
// resumed" and an operator watching for the end of their drain never sees it
// clear.
func TestTheFastPathCallsHoldsNeverAndSequencesOncePerResume(t *testing.T) {
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)

	t.Run("a queue nobody has parked in", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")
		stand.append(t, "b", "B1")

		held := newPark()
		running(t, newProjection(t, stand.parking(stand.spec("orders", applies(stand.model)), held)))

		stand.observer.await(t, "the whole log was read", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing
		})
		stand.ticks.expect(t, time.Second, "the poll the loop follows on")
		for range 4 {
			stand.ticks.fire(t)
		}
		if asked := held.sequences.Load(); asked != 1 {
			t.Fatalf("the queue was counted %d times over a resume and several passes, and a healthy projection counts it once per resume and never per pass", asked)
		}
		if asked := held.holds.Load(); asked != 0 {
			t.Fatalf("the queue was asked about %d sequences while it held none, and what makes the check free is that it is not made", asked)
		}
	})

	t.Run("after one park, and again after a redrive empties the queue", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")
		stand.append(t, "b", "B1")

		held := newPark()
		rows := &ledger{}
		one := &transaction{named: "one resource"}
		spec := stand.spec("orders", projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
			for _, envelope := range batch.Envelopes {
				if envelope.Stream.Key == "a" {
					return permanent
				}
			}
			rows.write(ctx, payloadsOf(batch.Envelopes)...)
			return nil
		}))
		running(t, newProjection(t, stand.parking(spec, held)))

		stand.observer.await(t, "the first page parked its sequence", func(state projection.State) bool {
			return state.Phase == projection.PhaseDegraded && state.Parked == 1
		})
		stand.ticks.expect(t, time.Second, "the poll the loop follows on")
		counted, asked := held.sequences.Load(), held.holds.Load()
		if asked != 0 {
			t.Fatalf("the pass that parked asked Holds %d times over a queue that was empty when it began", asked)
		}
		if counted < 2 {
			t.Fatalf("the queue was counted %d times over the resume and the pass that followed the park, and a loop that parked believes the queue is not empty", counted)
		}

		stand.append(t, "a", "A2")
		stand.append(t, "b", "B2")
		stand.ticks.fire(t)
		stand.observer.await(t, "the second page was accounted for", func(state projection.State) bool {
			return state.Progress.Quarantined == 2
		})
		if grown := held.holds.Load(); grown != 2 {
			t.Fatalf("the queue was asked about %d sequences over a page of two envelopes, and a degraded projection asks one question per envelope", grown)
		}
		if grown := held.sequences.Load() - counted; grown < 1 {
			t.Fatalf("the queue was counted %d more times over the pass that delivered the second page, and a degraded projection counts it at the start of every pass", grown)
		}

		drained, err := redriving(t, stand, held, one, appliesInto(rows)).Sequence(context.Background(), sequenceOf("a"))
		if err != nil || drained.Applied != 2 {
			t.Fatalf("the redrive answered %+v and %v, where the sequence held two letters and the handler applies everything", drained, err)
		}
		stand.ticks.fire(t)
		stand.observer.await(t, "the drain cleared the queue", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing && state.Parked == 0
		})

		counted, asked = held.sequences.Load(), held.holds.Load()
		for range 3 {
			stand.ticks.fire(t)
		}
		if grown := held.sequences.Load() - counted; grown != 0 {
			t.Fatalf("the queue was counted %d more times after it emptied, and a projection back to health pays nothing per pass — the rule that reads it for the life of the process is the one this measures the other end of", grown)
		}
		if grown := held.holds.Load() - asked; grown != 0 {
			t.Fatalf("the queue was asked about %d sequences after it emptied, and the count is what stops the questions", grown)
		}
		if got := rows.rows(); !same(got, []string{"B1", "B2", "A1", "A2"}) {
			t.Fatalf("the read model holds %v, where the two events of b applied as they arrived and the two of a were drained afterwards", got)
		}
	})
}

// The park write is a row in the caller's own transaction, so a unit that does
// not commit leaves neither a letter nor an advance. And the one path where a
// park this loop did not write is real is an overtaken: the survivor adopts the
// winner's row and re-reads what the winner parked.
func TestAParkWriteRollsBackWithItsUnitAndAnOvertakenRereadsTheCount(t *testing.T) {
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)

	t.Run("a unit interrupted after the park leaves the queue empty and the row unmoved", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")
		stand.append(t, "b", "B1")
		stand.interrupted = errors.New("the unit of work was interrupted before it committed")

		held := newPark()
		spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			for _, envelope := range batch.Envelopes {
				if envelope.Stream.Key == "a" {
					return permanent
				}
			}
			return nil
		}))
		running(t, newProjection(t, stand.parking(spec, held)))

		stand.observer.await(t, "the pass returned the page to retrying", func(state projection.State) bool {
			return state.Phase == projection.PhaseRetrying
		})
		if written := held.written.Load(); written == 0 {
			t.Fatal("the pass never wrote a letter at all, so a queue that is empty afterwards says nothing about the rollback")
		}
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		if names := held.names(of); len(names) != 0 {
			t.Fatalf("the queue holds %v after the unit carrying those letters rolled back, so a letter can outlive the advance it was written beside", names)
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the row stands at advance %d after a unit that did not commit", row.Advance)
		}
	})

	t.Run("an overtaken re-reads the count and a single instance does not", func(t *testing.T) {
		ctx := context.Background()
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")

		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		var once sync.Once
		stand.points.onSave = func(checkpoint event.Checkpoint, _ int64) (bool, error) {
			taken := false
			once.Do(func() {
				taken = true
				held.seed(of, "by-stream", sequenceOf("z"), event.Envelope{
					Stream:   event.Stream{Family: "orders", Key: "z"},
					Position: 9,
					Payload:  []byte("Z1"),
				})
			})
			if !taken {
				return true, nil
			}
			winner := event.Checkpoint{Projection: checkpoint.Projection, Cursor: checkpoint.Cursor, Advance: checkpoint.Advance}
			if err := stand.checkpoints.Save(ctx, winner); err != nil {
				return false, err
			}
			return false, event.Failure(event.Conflict, nil)
		}
		running(t, newProjection(t, stand.parking(stand.spec("orders", applies(stand.model)), held)))

		stand.observer.await(t, "it is retrying over a second writer", func(state projection.State) bool {
			return state.Phase == projection.PhaseRetrying && errors.Is(state.Err, projection.ErrOvertaken)
		})
		stand.ticks.expect(t, time.Second, "the poll the loop opens with")
		stand.ticks.expect(t, 250*time.Millisecond, "the backoff a lost fence opens")
		stand.ticks.fire(t)
		stand.observer.await(t, "the survivor picked up what the winner parked", func(state projection.State) bool {
			return state.Parked == 1
		})
		if counted := held.sequences.Load(); counted < 2 {
			t.Fatalf("the queue was counted %d times across a resume and an overtaken, and an overtaken is a resume: the count the loser carried over is the one path where another writer's park is real", counted)
		}
	})

	t.Run("the control: one instance alone counts the queue once", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")

		held := newPark()
		running(t, newProjection(t, stand.parking(stand.spec("orders", applies(stand.model)), held)))

		stand.observer.await(t, "the whole log was read", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing
		})
		stand.ticks.expect(t, time.Second, "the poll the loop follows on")
		if counted := held.sequences.Load(); counted != 1 {
			t.Fatalf("one instance alone counted the queue %d times, so the arm above measures nothing", counted)
		}
	})
}

// The third verdict, and the one an operator clears with one DELETE rather than
// a redeploy. All three bounds end the pass the same way, and what makes that
// statement true is the unit: there is always one, because ParkSequence
// constructs at no other tier, so the letters the earlier envelopes of the page
// parked go back with the advance and the retry re-parks them rather than adding
// a second copy.
func TestAFullParkBlocksTheAdvanceAndSkipsNothing(t *testing.T) {
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)
	of := func(t *testing.T) projection.Identity {
		return identityOf(t, "orders", projection.Ungenerated, projection.Whole())
	}
	blocked := func(t *testing.T, bound func(*park), keys ...string) (*stand, *park, *ledger) {
		t.Helper()
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		bound(held)
		for _, key := range keys {
			stand.append(t, key, key+"1")
		}
		rows := &ledger{}
		spec := stand.spec("orders", projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
			for _, envelope := range batch.Envelopes {
				if envelope.Stream.Key != "z" {
					return permanent
				}
			}
			rows.write(ctx, payloadsOf(batch.Envelopes)...)
			return nil
		}))
		running(t, newProjection(t, stand.parking(spec, held)))
		return stand, held, rows
	}

	for _, bound := range []struct {
		what  string
		apply func(*park)
	}{
		{"the sequence holds its own limit of letters", func(held *park) {
			held.bounded(0, 1, 0)
			held.seed(identityOf(t, "orders", projection.Ungenerated, projection.Whole()), "by-stream", sequenceOf("b"), event.Envelope{
				Stream:   event.Stream{Family: "orders", Key: "b"},
				Position: 99,
				Payload:  []byte("B0"),
			})
		}},
		{"the queue holds its own limit of sequences", func(held *park) { held.bounded(1, 0, 0) }},
		{"the payloads parked reach the byte bound", func(held *park) { held.bounded(0, 0, 2) }},
	} {
		t.Run(bound.what, func(t *testing.T) {
			stand, held, rows := blocked(t, bound.apply, "a", "b")

			state := stand.observer.await(t, "the pass ended on a queue with no room", func(state projection.State) bool {
				return state.Phase == projection.PhaseBlocked
			})
			if !errors.Is(state.Err, projection.ErrParkFull) {
				t.Fatalf("a pass that met a queue with no room published %v", state.Err)
			}
			if state.Attempt != 1 {
				t.Fatalf("the blocked pass stands at attempt %d, and an operator who has not drained the queue yet has not spent a handler's budget", state.Attempt)
			}
			if row := stand.row(t, "orders"); !row.Fresh() {
				t.Fatalf("the row stands at advance %d over a page that could not be accounted for", row.Advance)
			}
			if got := rows.rows(); len(got) != 0 {
				t.Fatalf("the read model holds %v over a pass whose unit rolled back", got)
			}
			for _, named := range held.names(of(t)) {
				if letters := held.letters(of(t), named); len(letters) > 1 {
					t.Fatalf("the sequence %q holds %d letters after a pass that could not finish, and a letter written beside one that hit the bound goes back with it", named, len(letters))
				}
			}
			if count := stand.observer.counted(projection.PhaseHalted); count != 0 {
				t.Fatal("a queue with no room halted the projection, and a halt needs a redeploy to clear a condition one DELETE clears")
			}
		})
	}

	t.Run("Ready fails once the block outlasts Tolerate", func(t *testing.T) {
		ctx := context.Background()
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		held.bounded(1, 0, 0)
		held.seed(identityOf(t, "orders", projection.Ungenerated, projection.Whole()), "by-stream", sequenceOf("z"), event.Envelope{
			Stream:   event.Stream{Family: "orders", Key: "z"},
			Position: 99,
			Payload:  []byte("Z0"),
		})
		stand.append(t, "a", "A1")

		spec := stand.spec("orders", projection.HandlerFunc(func(context.Context, projection.Batch) error { return permanent }))
		spec.Tolerate = 300 * time.Millisecond
		spec.Backoff = projection.Backoff{First: 250 * time.Millisecond, Max: 250 * time.Millisecond}
		built := newProjection(t, stand.parking(spec, held))
		running(t, built)

		stand.observer.await(t, "the pass ended on a queue with no room", func(state projection.State) bool {
			return state.Phase == projection.PhaseBlocked
		})
		stand.ticks.expect(t, time.Second, "the poll the loop opens with")
		for range 2 {
			stand.ticks.expect(t, 250*time.Millisecond, "the backoff a blocked pass opens")
			stand.ticks.fire(t)
		}
		// The backoff a pass opens is asked for after the previous one was waited
		// out and added, so a third registration is what makes the accumulated
		// wait a number this case can read rather than one it races.
		stand.ticks.expect(t, 250*time.Millisecond, "the backoff the third blocked pass opens")
		err := built.Ready(ctx)
		if err == nil {
			t.Fatal("a projection blocked past its tolerance reported healthy, and a queue nobody drains is a projection that has stopped applying events")
		}
		if !errors.Is(err, projection.ErrParkFull) {
			t.Fatalf("the readiness answer is %v and does not carry what the projection is blocked on", err)
		}
	})

	t.Run("the control: room again lets the very next pass advance", func(t *testing.T) {
		stand, held, rows := blocked(t, func(held *park) { held.bounded(1, 0, 0) }, "a", "b", "z")

		stand.observer.await(t, "the pass ended on a queue with no room", func(state projection.State) bool {
			return state.Phase == projection.PhaseBlocked
		})
		stand.ticks.expect(t, time.Second, "the poll the loop opens with")
		stand.ticks.expect(t, 250*time.Millisecond, "the backoff a blocked pass opens")
		held.bounded(2, 0, 0)
		stand.ticks.fire(t)

		stand.observer.await(t, "the page was accounted for", func(state projection.State) bool {
			return state.Progress.Quarantined == 2
		})
		if got := rows.rows(); !same(got, []string{"z1"}) {
			t.Fatalf("the read model holds %v where the envelope of another sequence applies once the queue has room", got)
		}
		if row := stand.row(t, "orders"); row.Advance != 1 {
			t.Fatalf("the row stands at advance %d after the queue had room again, and a pass that had to be restarted to clear a block is a halt with another name", row.Advance)
		}
	})
}

// Two dimensions, exactly the mechanism's, and the check is per sequence and
// never per queue: a queue holding 1023 sequences of one letter each still takes
// a 1024th letter into an existing sequence.
func TestTheParksBoundIsPerSequenceAndNotPerQueue(t *testing.T) {
	ctx := context.Background()
	of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
	letter := func(named, payload string) projection.Letter {
		return projection.Letter{
			Identity:  of,
			Sequencer: "by-stream",
			Sequence:  named,
			Envelope:  event.Envelope{Stream: event.Stream{Family: "orders", Key: event.Key(named)}, Position: 1, Payload: []byte(payload)},
		}
	}

	t.Run("1023 sequences of one letter each", func(t *testing.T) {
		held := newPark()
		held.bounded(1024, 1024, 0)
		for index := range 1023 {
			parked(t, held, of, "k"+strconv.Itoa(index), "one")
		}

		if err := held.Park(ctx, letter(sequenceOf("k0"), "two")); err != nil {
			t.Fatalf("a letter for a sequence the queue already holds was refused with %v, and the bound is asked of the sequence rather than of the queue", err)
		}
		if err := held.Park(ctx, letter("the-1024th", "one")); err != nil {
			t.Fatalf("the 1024th sequence was refused with %v, and 1023 is below the limit", err)
		}
		if err := held.Park(ctx, letter("the-1025th", "one")); !errors.Is(err, projection.ErrParkFull) {
			t.Fatalf("the 1025th sequence answered %v where the queue is at its limit", err)
		}
	})

	t.Run("the control: the same queue at the letter bound", func(t *testing.T) {
		held := newPark()
		held.bounded(1024, 2, 0)
		parked(t, held, of, "a", "one", "two")

		if err := held.Park(ctx, letter(sequenceOf("a"), "three")); !errors.Is(err, projection.ErrParkFull) {
			t.Fatalf("a letter for a sequence at its own limit answered %v", err)
		}
		if err := held.Park(ctx, letter(sequenceOf("b"), "one")); err != nil {
			t.Fatalf("a letter for another sequence was refused with %v while one sequence was full, so the two dimensions are one", err)
		}
	})

	// What the two arms above assert of the queue, measured through the loop that
	// writes to it: the bound is the implementation's and this package can neither
	// call it nor certify it, so what a test here can show is what an
	// implementation that got it wrong costs the projection that uses it. A queue
	// at its sequence limit still takes the events behind a blocker it is already
	// holding, and the projection stays degraded and keeps advancing; one that
	// asked isFull() without the sequence stops the partition instead, with the
	// blocked order's own room unused.
	t.Run("a queue at its sequence limit still takes what is behind a blocker", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A4")
		stand.append(t, "c", "C1")
		held := newPark()
		held.bounded(1, 1024, 0)
		parked(t, held, of, "a", "A3")
		running(t, newProjection(t, stand.parking(stand.spec("orders", applies(stand.model)), held)))

		state := stand.observer.await(t, "the whole log was read over a queue at its sequence limit", func(state projection.State) bool {
			return state.Phase == projection.PhaseDegraded
		})
		if rows := stand.model.rows(); !same(rows, []string{"C1"}) {
			t.Fatalf("the read model holds %v, where the event behind the blocker goes to the queue and the one of another order applies", rows)
		}
		if letters := held.letters(of, sequenceOf("a")); len(letters) != 2 {
			t.Fatalf("the blocked order holds %d letters, where a queue at its sequence limit still has room in the sequence it already holds", len(letters))
		}
		if state.Progress.Quarantined != 1 {
			t.Fatalf("the projection reports %d quarantined where one envelope went behind the blocker", state.Progress.Quarantined)
		}
		if row := stand.row(t, "orders"); row.Advance != 1 {
			t.Fatalf("the row stands at advance %d, where a page that parked one envelope and applied another advances once", row.Advance)
		}
	})

	t.Run("the control: a one-dimensional bound stops the partition instead", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A4")
		stand.append(t, "c", "C1")
		held := newPark()
		held.bounded(1, 1024, 0)
		held.oneDimensional()
		parked(t, held, of, "a", "A3")
		running(t, newProjection(t, stand.parking(stand.spec("orders", applies(stand.model)), held)))

		stand.observer.await(t, "the queue refused the letter and the partition stopped", func(state projection.State) bool {
			return state.Phase == projection.PhaseBlocked
		})
		if rows := stand.model.rows(); len(rows) != 0 {
			t.Fatalf("the read model holds %v, where a blocked pass applies nothing at all", rows)
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the row stands at advance %d, where a bound asked without the sequence blocks a projection whose blocked order had room", row.Advance)
		}
	})
}

// One broken order does not stop the projector, so a park does not fail
// readiness — but it does stop the projection from saying it caught up, because
// the whole log having been read and part of it not having been applied are two
// different things and only one of them is what an operator watching a drain
// wants told.
func TestAParkedProjectionIsDegradedAndStillReady(t *testing.T) {
	ctx := context.Background()
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)

	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "A1")
	stand.append(t, "b", "B1")
	stand.append(t, "c", "C1")

	held := newPark()
	rows := &ledger{}
	spec := stand.spec("orders", projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
		for _, envelope := range batch.Envelopes {
			if envelope.Stream.Key != "c" {
				return permanent
			}
		}
		rows.write(ctx, payloadsOf(batch.Envelopes)...)
		return nil
	}))
	built := newProjection(t, stand.parking(spec, held))
	running(t, built)

	degraded := stand.observer.await(t, "the whole log was read over a queue holding two sequences", func(state projection.State) bool {
		return state.Phase == projection.PhaseDegraded
	})
	if degraded.Parked != 2 {
		t.Fatalf("the projection reports %d parked sequences where two orders are blocked", degraded.Parked)
	}
	if degraded.Progress.Quarantined != 2 {
		t.Fatalf("the row records %d quarantined where two envelopes were parked", degraded.Progress.Quarantined)
	}
	if count := stand.observer.counted(projection.PhaseFollowing); count != 0 {
		t.Fatal("the projection reported that it had caught up while two of its sequences were blocked")
	}
	if err := built.Ready(ctx); err != nil {
		t.Fatalf("a projection with a parked order reported unhealthy with %v, and a replica that stops serving for one poison order is the projector stopping by another route", err)
	}

	stand.ticks.expect(t, time.Second, "the poll the loop follows on")
	one := &transaction{named: "one resource"}
	redrive := redriving(t, stand, held, one, appliesInto(rows))
	for _, key := range []string{"a", "b"} {
		if drained, err := redrive.Sequence(ctx, sequenceOf(key)); err != nil || drained.Applied != 1 {
			t.Fatalf("draining %q answered %+v and %v", key, drained, err)
		}
	}
	stand.ticks.fire(t)

	following := stand.observer.await(t, "the drain cleared the queue", func(state projection.State) bool {
		return state.Phase == projection.PhaseFollowing
	})
	if following.Parked != 0 {
		t.Fatalf("the projection reports %d parked sequences after both were drained", following.Parked)
	}
	if following.Progress.Quarantined != 2 {
		t.Fatalf("the row records %d quarantined after the drain, and the durable mark of what once failed is never decremented — the phase clears and it does not", following.Progress.Quarantined)
	}
}

// An eviction without an apply is a hole, and it is the question a cutover asks:
// Progress.Quarantined is cumulative and cannot answer it, so a generation that
// parked one sequence and redrove it completely would be refused for ever by a
// check written against the counter.
func TestAnEvictionLeavesAHoleAndDecrementsNothing(t *testing.T) {
	ctx := context.Background()
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)
	of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())

	drained := func(t *testing.T, skipping bool) (*stand, *park, event.Checkpoint) {
		t.Helper()
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")
		stand.append(t, "b", "B1")

		held := newPark()
		rows := &ledger{}
		spec := stand.spec("orders", projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
			for _, envelope := range batch.Envelopes {
				if envelope.Stream.Key == "a" {
					return permanent
				}
			}
			rows.write(ctx, payloadsOf(batch.Envelopes)...)
			return nil
		}))
		cancel, returned := running(t, newProjection(t, stand.parking(spec, held)))
		stand.observer.await(t, "the sequence was parked", func(state projection.State) bool {
			return state.Phase == projection.PhaseDegraded && state.Parked == 1
		})
		cancel()
		_ = returns(t, returned)

		if skipping {
			held.skip(of, sequenceOf("a"))
		} else if answered, err := redriving(t, stand, held, &transaction{named: "one resource"}, appliesInto(rows)).Sequence(ctx, sequenceOf("a")); err != nil || answered.Applied != 1 {
			t.Fatalf("draining the parked sequence answered %+v and %v", answered, err)
		}
		return stand, held, stand.row(t, "orders")
	}

	t.Run("a letter an operator skipped", func(t *testing.T) {
		_, held, row := drained(t, true)

		holes, err := held.Holes(ctx, of)
		if err != nil {
			t.Fatalf("counting the holes answered %v", err)
		}
		if holes != 1 {
			t.Fatalf("the park reports %d holes where one letter left the queue without being applied", holes)
		}
		if row.Progress.Quarantined != 1 {
			t.Fatalf("the row records %d quarantined after an eviction, and no counter is decremented by one", row.Progress.Quarantined)
		}
		if names := held.names(of); len(names) != 0 {
			t.Fatalf("the queue still holds %v after its last letter left, so the sequence never unblocks", names)
		}
	})

	t.Run("the control: a sequence redriven to completion leaves no hole", func(t *testing.T) {
		_, held, row := drained(t, false)

		holes, err := held.Holes(ctx, of)
		if err != nil {
			t.Fatalf("counting the holes answered %v", err)
		}
		if holes != 0 {
			t.Fatalf("the park reports %d holes after every letter was applied, and a cutover gated on that number refuses the ordinary recovery path for ever", holes)
		}
		if row.Progress.Quarantined != 1 {
			t.Fatalf("the row records %d quarantined after a completed drain, and the permanent statement that something once failed does not fall", row.Progress.Quarantined)
		}
	})
}

// A park is keyed by the projection AND its generation, so a rebuild does not
// inherit the live generation's blocked sequences — which would end with a
// non-zero Holes it could never clear and a cutover it could never make.
func TestTwoGenerationsShareAParkAndSeeNoneOfEachOthersLetters(t *testing.T) {
	ctx := context.Background()
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)

	t.Run("the second generation sees none of the first's letters", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")
		stand.append(t, "b", "B1")

		held := newPark()
		refusing := projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			for _, envelope := range batch.Envelopes {
				if envelope.Stream.Key == "a" {
					return permanent
				}
			}
			return nil
		})
		live := stand.spec("orders", refusing)
		live.Generation = 1
		cancel, returned := running(t, newProjection(t, stand.parking(live, held)))
		stand.observer.await(t, "generation 1 parked its sequence", func(state projection.State) bool {
			return state.Identity.Generation() == 1 && state.Parked == 1
		})
		cancel()
		_ = returns(t, returned)

		first := identityOf(t, "orders", 1, projection.Whole())
		second := identityOf(t, "orders", 2, projection.Whole())
		if letters := held.letters(first, sequenceOf("a")); len(letters) != 1 {
			t.Fatalf("generation 1 parked %d letters, so the arm below measures nothing", len(letters))
		}
		counted, err := held.Sequences(ctx, second)
		if err != nil {
			t.Fatalf("counting generation 2's queue answered %v", err)
		}
		if counted != 0 {
			t.Fatalf("generation 2 reads %d parked sequences, and a park keyed by the bare projection name lets the live generation's letters block a rebuild that never failed", counted)
		}
		holes, err := held.Holes(ctx, second)
		if err != nil || holes != 0 {
			t.Fatalf("generation 2 reports %d holes and %v, and a rebuild that inherited them could never be cut over to", holes, err)
		}
	})

	t.Run("the control: a partition of one generation writes into the generation's queue", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		for index := range 8 {
			stand.append(t, "k"+strconv.Itoa(index), "one")
		}

		held := newPark()
		refusing := projection.HandlerFunc(func(context.Context, projection.Batch) error { return permanent })
		live := stand.spec("orders", refusing)
		live.Generation, live.Partition = 1, partitionOf(t, 0, 1)
		cancel, returned := running(t, newProjection(t, stand.parking(live, held)))
		stand.observer.await(t, "the partition parked what it matched", func(state projection.State) bool {
			return state.Progress.Quarantined > 0
		})
		cancel()
		_ = returns(t, returned)

		generation := identityOf(t, "orders", 1, projection.Whole())
		partitioned := identityOf(t, "orders", 1, partitionOf(t, 0, 1))
		counted, err := held.Sequences(ctx, generation)
		if err != nil || counted == 0 {
			t.Fatalf("the generation's queue holds %d sequences and answered %v, and a runner that names a partition still writes into the queue of its generation — one keyed by the partition orphans every letter the moment that partition is split", counted, err)
		}
		if stray := held.names(partitioned); len(stray) != 0 {
			t.Fatalf("the letters %v were written under the runner's own partitioned name, so a split moves the queue out from under them and the next event of a parked order goes straight to a handler", stray)
		}
		for _, named := range held.names(generation) {
			for _, letter := range held.letters(generation, named) {
				if letter.Identity != generation {
					t.Fatalf("a letter carries the identity %q where the queue is keyed by the projection and its generation", letter.Identity)
				}
			}
		}
	})
}

// The causal order a park promises is not a property of the queue: it is a
// property of the queue and the read model committing together. New refuses the
// wirings that cannot order them rather than scoping the invariant to half the
// shipped matrix.
func TestParkSequenceIsRefusedOutsideTierA(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	held := newPark()

	for _, refused := range []struct {
		what  string
		names string
		build func(projection.Spec) projection.Spec
	}{
		{"the advance is written after the handler applies", "InUnit", func(spec projection.Spec) projection.Spec {
			return spec
		}},
		{"the destination cannot be resolved at all", "Unchecked", func(spec projection.Spec) projection.Spec {
			spec.Advance = projection.InUnit
			spec.Unit = runsTheWork
			spec.Destination = projection.Unchecked
			return spec
		}},
	} {
		t.Run(refused.what, func(t *testing.T) {
			spec := refused.build(stand.spec("orders", applies(stand.model)))
			spec.OnPermanentFailure, spec.Park = projection.ParkSequence, held
			built, err := projection.New(spec)
			if err == nil {
				t.Fatalf("a park %s was accepted, and the order it promises is not one that wiring can keep", refused.what)
			}
			if built != nil {
				t.Fatal("a refused spec answered a projection as well as an error")
			}
			if !errors.Is(err, projection.ErrSpec) {
				t.Fatalf("the refusal is %v, which is not the class a spec that cannot be assembled carries", err)
			}
			if !strings.Contains(err.Error(), refused.names) {
				t.Fatalf("the refusal reads %q and does not name %s", err, refused.names)
			}
		})

		t.Run("the control: the same wiring with Halt", func(t *testing.T) {
			spec := refused.build(stand.spec("orders", applies(stand.model)))
			spec.Park = held
			if _, err := projection.New(spec); err != nil {
				t.Fatalf("the same wiring under Halt was refused with %v, so the refusal above is the mode's and not the park's", err)
			}
		})
	}

	t.Run("the control: InUnit over a resolvable destination is accepted", func(t *testing.T) {
		one := &transaction{named: "one resource"}
		spec := stand.inUnit(stand.spec("orders", applies(stand.model)), one, one, nil)
		spec.OnPermanentFailure, spec.Park = projection.ParkSequence, held
		if _, err := projection.New(spec); err != nil {
			t.Fatalf("a park at the one tier it constructs at was refused with %v", err)
		}
	})
}

// The failure table for the queue, each arm asserted: a park with no room blocks,
// a park that refused for any other reason halts, and a read of the application's
// own table that blinked postpones — stopping a live projection because that
// table blinked is a projector stopped by another route.
func TestAParkFailureThatIsNotFullHaltsAndAReadFailurePostpones(t *testing.T) {
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)
	refusing := projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
		for _, envelope := range batch.Envelopes {
			if envelope.Stream.Key == "a" {
				return permanent
			}
		}
		return nil
	})

	t.Run("a park that refuses a letter for any other reason halts", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")

		held := newPark()
		unreachable := errors.New("the park table is unreachable")
		held.failing(unreachable, nil, nil)
		running(t, newProjection(t, stand.parking(stand.spec("orders", refusing), held)))

		halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted
		})
		if !errors.Is(halted.Err, projection.ErrHalted) {
			t.Fatalf("a park that refused a letter answered %v", halted.Err)
		}
		if reported := halted.Err.Error(); !strings.Contains(reported, "park") {
			t.Fatalf("the halt reads %q and does not name the park, and a policy with nowhere to record is a skip with extra words", reported)
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the row stands at advance %d over an envelope nothing recorded", row.Advance)
		}
	})

	t.Run("a count that cannot be read postpones", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")

		held := newPark()
		unreachable := errors.New("the park table is unreachable")
		held.failing(nil, unreachable, nil)
		running(t, newProjection(t, stand.parking(stand.spec("orders", applies(stand.model)), held)))

		retrying := stand.observer.await(t, "the pass postponed", func(state projection.State) bool {
			return state.Phase == projection.PhaseRetrying
		})
		if !errors.Is(retrying.Err, unreachable) {
			t.Fatalf("a queue that could not be counted published %v", retrying.Err)
		}
		if retrying.Attempt != 0 {
			t.Fatalf("the postponed pass stands at attempt %d, and a read of the application's own table costs no handler's budget", retrying.Attempt)
		}
		if count := stand.observer.counted(projection.PhaseHalted); count != 0 {
			t.Fatal("a queue that could not be counted halted the projection")
		}
	})

	t.Run("a blocking test that cannot be read postpones", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")

		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		parked(t, held, of, "b", "B0")
		unreachable := errors.New("the park table is unreachable")
		held.failing(nil, nil, unreachable)
		var seen delivered
		spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			seen.took(batch)
			return nil
		}))
		running(t, newProjection(t, stand.parking(spec, held)))

		retrying := stand.observer.await(t, "the pass postponed", func(state projection.State) bool {
			return state.Phase == projection.PhaseRetrying && errors.Is(state.Err, unreachable)
		})
		if seen.count() != 0 {
			t.Fatalf("the handler was called %d times over a page whose blocking test never answered, so an envelope of a parked sequence could have reached it", seen.count())
		}
		if retrying.Attempt != 1 {
			t.Fatalf("the postponed pass stands at attempt %d, and a read that blinked costs no handler's budget", retrying.Attempt)
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the row stands at advance %d over a page whose blocking test never answered", row.Advance)
		}
		if count := stand.observer.counted(projection.PhaseHalted); count != 0 {
			t.Fatal("a blocking test that could not be read halted the projection")
		}
	})
}

// A composition root builds one spec for a live generation and a rebuild, so a
// Park arrives beside a policy that never writes to it — and it is accepted
// there rather than refused. What makes that harmless is that the whole
// blocking path is unreachable from it: gated on the field alone, a queue that
// is already holding a sequence puts a pass at AfterApply into Holds per
// envelope and a letter written from OUTSIDE any unit, where nothing rolls it
// back and a postponed pass writes it a second time.
func TestAParkIsInertBesideAPolicyThatDoesNotNameIt(t *testing.T) {
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)

	holding := func(t *testing.T) (*stand, *park, projection.Identity) {
		t.Helper()
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")
		stand.append(t, "a", "A2")
		stand.append(t, "c", "C1")
		held := newPark()
		stand.park = held
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		parked(t, held, of, "a", "A0")
		return stand, held, of
	}

	untouched := func(t *testing.T, held *park, of projection.Identity) {
		t.Helper()
		if asked := held.sequences.Load(); asked != 0 {
			t.Fatalf("the loop counted the queue %d times over a policy that never writes to it, and the count is the read a healthy projection is not to pay for", asked)
		}
		if asked := held.holds.Load(); asked != 0 {
			t.Fatalf("the loop asked Holds %d times over a policy that never writes to it, which is the blocking path at a tier ParkSequence is refused at", asked)
		}
		if written := held.written.Load(); written != 0 {
			t.Fatalf("the loop wrote %d letters under a policy that parks nothing", written)
		}
		if letters := held.letters(of, sequenceOf("a")); len(letters) != 1 {
			t.Fatalf("the queue holds %d letters where it was seeded with one and nothing was to be added to it", len(letters))
		}
	}

	t.Run("the advance is written after the handler applies", func(t *testing.T) {
		stand, held, of := holding(t)
		spec := stand.spec("orders", applies(stand.model))
		spec.Park = held
		running(t, newProjection(t, spec))

		state := stand.observer.await(t, "the whole log was read", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing
		})
		if rows := stand.model.rows(); !same(rows, []string{"A1", "A2", "C1"}) {
			t.Fatalf("the read model holds %v, where a policy that parks nothing applies the whole page", rows)
		}
		if state.Parked != 0 || state.Progress.Quarantined != 0 {
			t.Fatalf("the projection reports %d parked and %d quarantined under a policy that parks nothing", state.Parked, state.Progress.Quarantined)
		}
		untouched(t, held, of)
	})

	t.Run("the one tier a park would construct at, under Halt", func(t *testing.T) {
		stand, held, of := holding(t)
		one := &transaction{named: "one resource"}
		spec := stand.inUnit(stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			for _, envelope := range batch.Envelopes {
				if string(envelope.Payload) == "A2" {
					return permanent
				}
			}
			return nil
		})), one, one, nil)
		spec.Park = held
		running(t, newProjection(t, spec))

		halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted
		})
		if !errors.Is(halted.Err, projection.ErrHalted) {
			t.Fatalf("a permanent failure under Halt answered %v, where the policy the spec names is the one that stops", halted.Err)
		}
		untouched(t, held, of)
	})

	t.Run("the control: the same queue and the same page under ParkSequence", func(t *testing.T) {
		stand, held, of := holding(t)
		spec := stand.parking(stand.spec("orders", applies(stand.model)), held)
		running(t, newProjection(t, spec))

		state := stand.observer.await(t, "the whole log was read over a queue that is holding something", func(state projection.State) bool {
			return state.Phase == projection.PhaseDegraded
		})
		if rows := stand.model.rows(); !same(rows, []string{"C1"}) {
			t.Fatalf("the read model holds %v, where the two events of the parked order go to the queue and the third is of another sequence", rows)
		}
		if asked := held.holds.Load(); asked != 3 {
			t.Fatalf("the pass asked Holds %d times over a page of three envelopes, so the policy is not what reaches the blocking path", asked)
		}
		if letters := held.letters(of, sequenceOf("a")); len(letters) != 3 {
			t.Fatalf("the queue holds %d letters for the parked order, where the seeded one and the two behind it are three", len(letters))
		}
		if state.Progress.Quarantined != 2 {
			t.Fatalf("the projection reports %d quarantined where two envelopes went to the queue", state.Progress.Quarantined)
		}
	})
}

// A Park whose calls a case counts by the unit bound on the context they were
// handed, which is the one thing the interface's contract states that the
// signatures cannot.
type watchedPark struct {
	held projection.Park

	inside  map[string]*atomic.Int64
	outside map[string]*atomic.Int64

	// What an implementation written against "every method runs inside the
	// caller's unit" does, so the cost of getting that sentence wrong is measured
	// rather than argued.
	demanding bool
}

func watchingPark(held projection.Park) *watchedPark {
	this := &watchedPark{held: held, inside: map[string]*atomic.Int64{}, outside: map[string]*atomic.Int64{}}
	for _, named := range []string{"Sequences", "Holds", "Park", "Holes"} {
		this.inside[named], this.outside[named] = &atomic.Int64{}, &atomic.Int64{}
	}
	return this
}

func (this *watchedPark) saw(ctx context.Context, named string) bool {
	if unitOfWorkIn(ctx) != nil {
		this.inside[named].Add(1)
		return true
	}
	this.outside[named].Add(1)
	return false
}

func (this *watchedPark) Sequences(ctx context.Context, of projection.Identity) (uint64, error) {
	if bound := this.saw(ctx, "Sequences"); !bound && this.demanding {
		return 0, errors.New("this queue is read through the transaction the caller bound, and this call bound none")
	}
	return this.held.Sequences(ctx, of)
}

func (this *watchedPark) Holds(ctx context.Context, of projection.Identity, sequence string) (bool, error) {
	this.saw(ctx, "Holds")
	return this.held.Holds(ctx, of, sequence)
}

func (this *watchedPark) Park(ctx context.Context, letter projection.Letter) error {
	this.saw(ctx, "Park")
	return this.held.Park(ctx, letter)
}

func (this *watchedPark) Holes(ctx context.Context, of projection.Identity) (uint64, error) {
	this.saw(ctx, "Holes")
	return this.held.Holes(ctx, of)
}

func (this *watchedPark) counted(t *testing.T, named string, inside, outside int64) {
	t.Helper()
	if got := this.inside[named].Load(); got != inside {
		t.Fatalf("%s ran inside the caller's unit %d times where the contract says %d", named, got, inside)
	}
	if got := this.outside[named].Load(); got != outside {
		t.Fatalf("%s ran outside the caller's unit %d times where the contract says %d", named, got, outside)
	}
}

// The four methods do not all run in the same place, and an application writes
// this interface against the sentence that says so: Holds and Park are inside
// the unit, because that is what orders a redrive's eviction against the loop's
// own blocking test, and Sequences is outside it, because a healthy projection
// would otherwise open a transaction every pass to be told the queue is empty.
func TestEachParkMethodIsCalledWhereItsContractSaysItIs(t *testing.T) {
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)

	t.Run("counted per method over a park, a later page and a drain", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")
		held := newPark()
		watched := watchingPark(held)
		spec := stand.parking(stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			return permanent
		})), held)
		spec.Park = watched
		running(t, newProjection(t, spec))

		stand.observer.await(t, "the failing order was parked", func(state projection.State) bool {
			return state.Phase == projection.PhaseDegraded
		})
		stand.append(t, "a", "A2")
		stand.ticks.fire(t)
		stand.observer.await(t, "the later event of the parked order was parked behind it", func(state projection.State) bool {
			return state.Progress.Quarantined == 2
		})

		watched.counted(t, "Holds", 1, 0)
		watched.counted(t, "Park", 2, 0)
		watched.counted(t, "Holes", 0, 0)
		if inside := watched.inside["Sequences"].Load(); inside != 0 {
			t.Fatalf("Sequences ran inside the caller's unit %d times, and a count read there costs a healthy projection a transaction per pass", inside)
		}
		if outside := watched.outside["Sequences"].Load(); outside < 2 {
			t.Fatalf("Sequences ran outside the caller's unit %d times, where a resume and every pass over a non-empty queue are at least two", outside)
		}
	})

	t.Run("the control: a Sequences that requires the ambient transaction never advances", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")
		held := newPark()
		watched := watchingPark(held)
		watched.demanding = true
		var calls atomic.Int64
		spec := stand.parking(stand.spec("orders", projection.HandlerFunc(func(context.Context, projection.Batch) error {
			calls.Add(1)
			return nil
		})), held)
		spec.Park = watched
		running(t, newProjection(t, spec))

		stand.observer.await(t, "the count could not be read", func(state projection.State) bool {
			return state.Phase == projection.PhaseRetrying
		})
		if called := calls.Load(); called != 0 {
			t.Fatalf("the handler was called %d times over a projection that never got past its own park count", called)
		}
		if row := stand.row(t, "orders"); row.Advance != 0 {
			t.Fatalf("the row stands at advance %d, and a count that can never be read postpones for ever rather than advancing", row.Advance)
		}
	})
}

// The attempt budget bounds handler failures and does not distinguish their
// class once it is spent, so a database that went away for that many passes
// parks every sequence of the page it was on rather than halting. That is the
// trade ParkSequence buys over Halt and it is stated rather than hidden: the
// projection keeps up with every other sequence, the letters carry the transient
// cause, and the recovery is a redrive — which is the mechanism's own
// recommended driver, scheduled rather than automatic.
func TestASpentAttemptBudgetParksATransientFailureAndARedriveClearsIt(t *testing.T) {
	ctx := context.Background()
	outage := errors.New("the read model's pool is exhausted")
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)

	page := func(t *testing.T) (*stand, *park, projection.Identity) {
		t.Helper()
		stand := newStand(t, eventmemory.Spec{})
		for _, key := range []string{"a", "b", "c", "d"} {
			stand.append(t, key, key+"1")
		}
		return stand, newPark(), identityOf(t, "orders", projection.Ungenerated, projection.Whole())
	}

	t.Run("a database that went away for the whole budget parks the page", func(t *testing.T) {
		stand, held, of := page(t)
		var calls atomic.Int64
		spec := stand.parking(stand.spec("orders", projection.HandlerFunc(func(context.Context, projection.Batch) error {
			calls.Add(1)
			return outage
		})), held)
		spec.Attempts = 2
		running(t, newProjection(t, spec))

		stand.observer.await(t, "the first attempt failed", func(state projection.State) bool {
			return state.Phase == projection.PhaseRetrying
		})
		stand.ticks.fire(t)
		state := stand.observer.await(t, "the budget was spent and the page was parked", func(state projection.State) bool {
			return state.Phase == projection.PhaseDegraded
		})
		if state.Parked != 4 || state.Progress.Quarantined != 4 {
			t.Fatalf("the projection reports %d parked and %d quarantined, where a page of four orders under a spent budget parks four", state.Parked, state.Progress.Quarantined)
		}
		if called := calls.Load(); called != 6 {
			t.Fatalf("the handler was called %d times, where two whole-page attempts and one envelope each are six", called)
		}
		for _, key := range []string{"a", "b", "c", "d"} {
			letters := held.letters(of, sequenceOf(key))
			if len(letters) != 1 {
				t.Fatalf("the order %s holds %d letters where one envelope of it was read", key, len(letters))
			}
			if !errors.Is(letters[0].Cause, outage) {
				t.Fatalf("the letter of %s carries %v, where a letter parked by a spent budget carries the failure that spent it", key, letters[0].Cause)
			}
			if letters[0].Attempt != 3 {
				t.Fatalf("the letter of %s was written at attempt %d, where a budget of two is spent on two whole-page attempts and the isolation pass is the third", key, letters[0].Attempt)
			}
		}
		if row := stand.row(t, "orders"); row.Advance != 1 {
			t.Fatalf("the row stands at advance %d, where the scan passes a parked page once", row.Advance)
		}

		drained := 0
		operator := redriving(t, stand, held, &transaction{named: "one resource"}, applies(stand.model))
		for range 4 {
			retried, err := operator.Any(ctx)
			if err != nil {
				t.Fatalf("the redrive of %q answered %v once the outage was over", retried.Sequence, err)
			}
			drained += retried.Applied
		}
		if drained != 4 {
			t.Fatalf("the redrive applied %d letters where four were parked, so an outage that parks a page is not one an operator clears", drained)
		}
		stand.ticks.fire(t)
		cleared := stand.observer.await(t, "the queue emptied and the projection caught up", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing
		})
		if cleared.Parked != 0 || cleared.Progress.Quarantined != 4 {
			t.Fatalf("after the drain the projection reports %d parked and %d quarantined, where the live count clears and the durable mark does not", cleared.Parked, cleared.Progress.Quarantined)
		}
	})

	t.Run("the control: a permanent failure parks without spending the budget", func(t *testing.T) {
		stand, held, of := page(t)
		var calls atomic.Int64
		spec := stand.parking(stand.spec("orders", projection.HandlerFunc(func(context.Context, projection.Batch) error {
			calls.Add(1)
			return permanent
		})), held)
		spec.Attempts = 2
		running(t, newProjection(t, spec))

		stand.observer.await(t, "the page was parked", func(state projection.State) bool {
			return state.Phase == projection.PhaseDegraded
		})
		if called := calls.Load(); called != 5 {
			t.Fatalf("the handler was called %d times, where one whole-page attempt and one envelope each are five — a permanent failure spends no attempt", called)
		}
		if letters := held.letters(of, sequenceOf("a")); len(letters) != 1 || letters[0].Attempt != 2 {
			t.Fatalf("the letters of the first order are %+v, where a permanent failure reaches the isolation pass on the second attempt rather than the third", letters)
		}
		if stand.observer.counted(projection.PhaseRetrying) != 0 {
			t.Fatalf("the projection retried %d times over a failure nothing about the next attempt would change", stand.observer.counted(projection.PhaseRetrying))
		}
	})
}
