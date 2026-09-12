package projection

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/runtime"
)

// The zero value resolves to AfterApply, which is the mode that promises less.
type Advance uint8

const (
	UnsetAdvance Advance = iota
	AfterApply
	InUnit
)

func (this Advance) Valid() bool { return this == AfterApply || this == InUnit }

func (this Advance) String() string {
	switch this {
	case AfterApply:
		return "[advance after apply]"
	case InUnit:
		return "[advance in unit]"
	default:
		return "[advance unset]"
	}
}

// Doubling, and without jitter: a projection is a singleton per name, so there
// is no herd to spread.
type Backoff struct {
	First time.Duration
	Max   time.Duration
}

type Spec struct {
	Name        string
	Log         event.Log
	Checkpoints event.Checkpoints
	Handler     Handler

	Advance Advance

	// Required by InUnit and refused beside AfterApply. The framework opens no
	// transaction: crud.InNewTx(ctx, source, work) is the one-line spelling.
	//
	// Under InUnit it must open ONE transaction, on the resource the handler
	// writes to and the checkpoint store lives in, and the handler must write
	// through the context it is given. That is what InUnit's atomicity is a
	// property of; this package checks as much of it as it can see and the rest
	// is an obligation it cannot check.
	//
	// It runs the work exactly once. A unit that retries its transaction answers
	// the failure instead and the projection re-delivers the page after a
	// backoff: one pass presents one advance, so a second run of the work inside
	// one pass is refused before it writes anything.
	Unit func(ctx context.Context, work func(context.Context) error) error

	// The handle the handler writes through — a crud.Source, a *sql.DB, whatever
	// the application's own data source is. Required by InUnit, which resolves it
	// inside the unit, before the handler runs, and refuses the pass when what is
	// bound for it is not a transaction. Unchecked is the one way to say it
	// cannot be resolved that way, and it says so where a reviewer reads the
	// composition rather than by a field left zero.
	Destination any

	// The three that name this projection's own share of the log, and every zero
	// value is what a projection that exists today already is: a nil Sequence is
	// ByStream(), the zero Partition is Whole(), and Generation zero is
	// Ungenerated, which renders nothing — so no name and no checkpoint row moves
	// because these fields arrived.
	//
	// Partition is the fraction of the key space this runner reads: every page is
	// filtered by it before the handler sees one, on the key Sequence answers.
	// Take it from a Cover, which is the value that checks the set, and reach an
	// existing projection's topology only through Split — a partitioned runner
	// started beside a live row for a coarser share of the same space is refused
	// at its first resume rather than replaying the log into a live read model.
	Sequence   Sequencer
	Partition  Partition
	Generation Generation

	Idle time.Duration

	// The read throttle a rebuild is given so that it does not starve the live
	// projection beside it: at most one read of the log per interval while the
	// previous read answered that there is more, and nothing at all once the
	// whole log has been read, where Idle governs and Backoff governs a retry.
	//
	// It is not a concurrency cap and must not be presented as one: it bounds the
	// reads of this loop and of nothing else. Zero reads as fast as the store
	// answers, which is every projection that exists today.
	//
	// Drop it before cutting over to this generation. Cutover names a window in
	// which reads move backwards by whatever the retiring generation advanced
	// while the switch committed, and what closes that window is this generation
	// catching up — which is the one thing Pace slows down.
	Pace time.Duration

	// A hint that the log has grown, and never the delivery mechanism: Idle polls
	// whether or not anything sends here, so a producer that stops sending costs
	// latency and nothing else. Closing it says there will be no more hints — the
	// loop stops waiting on it and follows on Idle alone, because a closed channel
	// is permanently ready and receiving from one is a busy read of the log.
	Wake <-chan struct{}

	Backoff  Backoff
	Attempts int
	Tolerate time.Duration

	// ParkSequence needs a queue, and it needs the tier InUnit with a resolvable
	// Destination is: the loop's blocking test, its park write, its applies and a
	// redrive's eviction order each other because they are rows in one
	// transaction, and outside that they are two opinions about what is parked.
	// New refuses the weaker wirings rather than scoping the guarantee to half
	// the shipped matrix.
	OnPermanentFailure Failure
	Park               Park

	// The ownership row, and the durable two-sender boundary neither reference
	// has. Required beside Effects at a generation other than Ungenerated,
	// because that is the pair that admits two senders; optional otherwise, and
	// supplying it there is the migration rather than a field nothing uses. The
	// suppressor is gated on this being supplied and never on Generation: before
	// a cutover the row answers this projection's own generation and it stages
	// exactly as it did without the field, and after one it answers another and
	// it stops — one code path for both cases, and the only one that closes the
	// first cutover of a deployment that exists today.
	Generations Generations

	// The live effects, and they are not the projection: a Handler is handed a
	// Batch and holds no route to one of these. Refused beside AfterApply, where
	// an effect staged outside a unit has the window a broker outage turns into a
	// permanently lost event. A rebuild is a spec with this nil, which is the
	// whole of "a rebuild does not get the effect-dispatch capability".
	Effects Effects

	// Suppressed for every envelope at or below it, per envelope and not per
	// page. The envelope's side of the comparison is durable — it is where the
	// resumed checkpoint left this generation — so a warm-up interrupted below
	// the barrier resumes suppressed rather than firing from the beginning.
	//
	// THIS SIDE IS A DEPLOYMENT-HELD CONSTANT AND NOT A RECORDED POSITION.
	// Nothing stores the barrier a generation was warmed up under and nothing
	// compares a restart's value against it, so a release that lowers or drops
	// this field stages an effect for every envelope of the warm-up still below
	// it, on the first pass, with no error and no refusal. The number is the
	// prior generation's mark: Observe answers it as Barrier.At, and a rollback
	// of the release that set it is a rollback of the barrier too.
	//
	// Refused with a nil Effects, and refused beside ParkSequence: a warm-up that
	// parked an event produces a generation whose rows are not comparable to the
	// live one, and the comparison is a cutover's only evidence.
	EffectsAfter event.Position

	Classifier Classifier
	Observer   Observer
	Ticks      runtime.Ticks
}

type unchecked struct{}

// The destination that cannot be resolved through crud's binding — a document
// store, a search index, a second database. InUnit with this is InUnit with the
// alignment obligation and no check on it.
var Unchecked = unchecked{}

const (
	defaultIdle     = time.Second
	defaultFirst    = 250 * time.Millisecond
	defaultMax      = 30 * time.Second
	defaultAttempts = 10
	defaultTolerate = time.Minute
)

// Performs no I/O, starts nothing, reads no environment. The first read of the
// checkpoint and the first read of the log both happen on the first pass of Run.
//
// Every refusal wraps ErrSpec and names the field it is about, and they are
// collected rather than reported one at a time: a spec assembled wrong is
// usually assembled wrong in more than one place, and a caller who fixes one
// field per build learns the shape one field at a time.
func New(spec Spec) (*Projection, error) {
	var problems []error

	identity, named := NewIdentity(spec.Name, spec.Generation, spec.Partition)
	if named != nil {
		problems = append(problems, named)
	}
	// The two doors ask overlapping questions about one field, so a name the
	// identity already refused is not reported a second time by the tracker's.
	tracker, err := event.Track(spec.Checkpoints, identity.String())
	if err != nil && (named == nil || !errors.Is(err, event.ErrDeclaration)) {
		problems = append(problems, namedRefusal(err))
	}
	if spec.Sequence != nil {
		if broken := unusable(spec.Sequence); broken != "" {
			problems = append(problems, fmt.Errorf("%w: %s", ErrSpec, broken))
		}
	}
	if absent(spec.Log) {
		problems = append(problems, fmt.Errorf("%w: Log names no log, and a projection reads through one", ErrSpec))
	}
	if absent(spec.Handler) {
		problems = append(problems, fmt.Errorf("%w: Handler names nothing to apply a page to", ErrSpec))
	}
	if appends(spec.Log) {
		problems = append(problems, fmt.Errorf("%w: Log is a value this projection could append through, and a projector that can append is how a replay writes — wrap it in event.ReadOnly", ErrSpec))
	}
	problems = append(problems, refusedAdvance(spec)...)
	if !spec.OnPermanentFailure.Valid() {
		problems = append(problems, fmt.Errorf("%w: OnPermanentFailure is neither Halt nor ParkSequence, and an unknown policy is not Halt", ErrSpec))
	}
	problems = append(problems, refusedPark(spec)...)
	problems = append(problems, refusedEffects(spec)...)
	problems = append(problems, refusedNumbers(spec)...)
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	return newProjection(withDefaults(spec), tracker, identity), nil
}

// event.Track asks two questions in one call and the classes tell them apart: a
// declaration refusal is the name this spec chose, and a wiring one is the store
// it named.
func namedRefusal(err error) error {
	if errors.Is(err, event.ErrDeclaration) {
		return fmt.Errorf("%w: Name is not one a checkpoint row can be keyed by: %w", ErrSpec, err)
	}
	return fmt.Errorf("%w: Checkpoints names no checkpoint store, and a projection records through one: %w", ErrSpec, err)
}

func refusedAdvance(spec Spec) []error {
	var problems []error
	if spec.Advance != UnsetAdvance && !spec.Advance.Valid() {
		problems = append(problems, fmt.Errorf("%w: Advance is neither AfterApply nor InUnit, and an unknown mode is not AfterApply", ErrSpec))
		return problems
	}
	if spec.Advance != InUnit {
		if spec.Unit != nil {
			problems = append(problems, fmt.Errorf("%w: Unit is supplied beside AfterApply, and a unit this projection opens nothing inside is a wiring the caller did not get — a handler whose own writes want one transaction opens it itself", ErrSpec))
		}
		return problems
	}
	if spec.Unit == nil {
		problems = append(problems, fmt.Errorf("%w: Advance is InUnit and Unit names no unit of work for the advance to ride in", ErrSpec))
	}
	if spec.Destination == nil {
		problems = append(problems, fmt.Errorf("%w: Advance is InUnit and Destination is unstated — name the handle the handler writes through, or projection.Unchecked to say out loud that it cannot be resolved", ErrSpec))
	}
	if !absent(spec.Checkpoints) && spec.Checkpoints.Capabilities().Transactions != event.Supported {
		problems = append(problems, fmt.Errorf("%w: Advance is InUnit and this checkpoint store does not support transactions, so the advance could only be a second write beside the handler's", ErrSpec))
	}
	return problems
}

// The three the park costs, and all three are about the tier rather than about
// the queue. The causal order a park promises is not a property of the queue: it
// is a property of the queue and the read model committing together, so a
// redrive that evicts the blocking letter while its write for the next one is
// still in flight lets the loop read a Holds of false and apply the one after
// that. Under AfterApply there is no unit at all, and under Unchecked there is
// one the read model is not in — both lose it, with no error on any path.
func refusedPark(spec Spec) []error {
	if spec.OnPermanentFailure != ParkSequence {
		return nil
	}
	var problems []error
	if absent(spec.Park) {
		problems = append(problems, fmt.Errorf("%w: OnPermanentFailure is ParkSequence and Park names no queue, and a policy with nowhere to record is a skip with extra words", ErrSpec))
	}
	if spec.Advance != InUnit {
		problems = append(problems, fmt.Errorf("%w: OnPermanentFailure is ParkSequence and Advance is not InUnit, and a queue whose letters do not commit with the read model orders nothing — a redrive and this loop would then apply two letters of one sequence out of order, which is what the queue exists to prevent", ErrSpec))
	}
	if _, unresolvable := spec.Destination.(unchecked); unresolvable {
		problems = append(problems, fmt.Errorf("%w: OnPermanentFailure is ParkSequence and Destination is Unchecked, so the read model is outside the unit the park write rides in and the two cannot be ordered against each other — what a destination this framework cannot resolve gets is Halt", ErrSpec))
	}
	return problems
}

// The three the capability costs beyond the one both doors share, and none of
// them is about the sink. An effect staged outside a unit is the reference's own
// dual-write window — the broker outage that advances the checkpoint and loses
// the event permanently — so the capability constructs at one tier and the
// refusal is a contract rather than a caveat. A barrier with no sink is a field
// whose only effect is a suppression of nothing. And a barrier beside a queue is
// a warm-up that can pass its own barrier by PARKING an event: the generation it
// leaves has rows that are not comparable to the live one, and that comparison
// is the only evidence a cutover has. That last one is why the redrive's door
// refuses a barrier outright — see NewRedrive.
func refusedEffects(spec Spec) []error {
	var problems []error
	if !absent(spec.Effects) && spec.Advance != InUnit {
		problems = append(problems, fmt.Errorf("%w: Effects is supplied beside Advance: AfterApply, and an effect staged outside the unit that commits the advance is a second write — the outage that takes the sink away advances the checkpoint over an event nothing will ever send, which is the window the capability exists to close", ErrSpec))
	}
	if spec.EffectsAfter > 0 && absent(spec.Effects) {
		problems = append(problems, fmt.Errorf("%w: EffectsAfter names a barrier and Effects names no sink, and a barrier with nothing to gate is a spec assembled wrong rather than a field that is merely unused", ErrSpec))
	}
	if spec.EffectsAfter > 0 && spec.OnPermanentFailure == ParkSequence {
		problems = append(problems, fmt.Errorf("%w: EffectsAfter names a barrier beside OnPermanentFailure: ParkSequence, and a warm-up that parks an event passes its own barrier over a read model that never received it — the rows the generation then holds are not comparable to the live one's, and that comparison is the only evidence a cutover has. What a barrier is warmed up under is Halt", ErrSpec))
	}
	return append(problems, unstageable(spec.Effects, spec.Generations, spec.Generation)...)
}

func refusedNumbers(spec Spec) []error {
	var problems []error
	for _, bound := range []struct {
		field  string
		chosen time.Duration
	}{
		{"Idle", spec.Idle},
		{"Pace", spec.Pace},
		{"Tolerate", spec.Tolerate},
		{"Backoff.First", spec.Backoff.First},
		{"Backoff.Max", spec.Backoff.Max},
	} {
		if bound.chosen < 0 {
			problems = append(problems, fmt.Errorf("%w: %s is %s", ErrSpec, bound.field, bound.chosen))
		}
	}
	if spec.Backoff.First > 0 && spec.Backoff.Max > 0 && spec.Backoff.First > spec.Backoff.Max {
		problems = append(problems, fmt.Errorf("%w: Backoff.First is %s and Backoff.Max is %s, and a backoff that shrinks is not one", ErrSpec, spec.Backoff.First, spec.Backoff.Max))
	}
	if spec.Attempts < 0 {
		problems = append(problems, fmt.Errorf("%w: Attempts is %d", ErrSpec, spec.Attempts))
	}
	return problems
}

// Each default is the one that promises less. Destination has none under InUnit:
// the answer "I cannot check this" has to be written rather than defaulted into.
//
// A Park is dropped unless the policy names it, and that is the whole of what
// makes one harmless beside Halt rather than merely unused: a composition root
// builds one spec for a live generation and a rebuild, so the field arrives
// beside a policy that never writes to it, and a loop that still read it there
// would ask Holds per envelope and write letters from outside a unit — the
// blocking path, at the tier refusedPark exists to refuse. It is dropped once,
// here, rather than tested at each of the three places that reach for it,
// because three tests of one condition is how an invariant becomes false
// without anybody editing it.
func withDefaults(spec Spec) Spec {
	if spec.OnPermanentFailure != ParkSequence {
		spec.Park = nil
	}
	if spec.Advance == UnsetAdvance {
		spec.Advance = AfterApply
	}
	if spec.Idle == 0 {
		spec.Idle = defaultIdle
	}
	if spec.Backoff.Max == 0 {
		spec.Backoff.Max = defaultMax
	}
	if spec.Backoff.First == 0 {
		spec.Backoff.First = min(defaultFirst, spec.Backoff.Max)
	}
	if spec.Backoff.Max < spec.Backoff.First {
		spec.Backoff.Max = spec.Backoff.First
	}
	if spec.Attempts == 0 {
		spec.Attempts = defaultAttempts
	}
	if spec.Tolerate == 0 {
		spec.Tolerate = defaultTolerate
	}
	if spec.Sequence == nil {
		spec.Sequence = ByStream()
	}
	if spec.Classifier == nil {
		spec.Classifier = Classify
	}
	if spec.Ticks == nil {
		spec.Ticks = runtime.SystemTicks
	}
	return spec
}

// A bare assertion is what this asks for and the one place in this package where
// that is right: event.ReadOnly answers a wrapper with no Next and no Append, so
// what is being asked is whether the value in hand still carries the append
// surface — and a wrapper that handed it back through a decorator hop would be
// the wrapper this refusal exists to require.
func appends(log event.Log) bool {
	_, is := log.(event.Store)
	return is
}

func absent(value any) bool {
	if value == nil {
		return true
	}
	switch reflected := reflect.ValueOf(value); reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
