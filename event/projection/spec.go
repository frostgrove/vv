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

	Idle time.Duration

	// A hint that the log has grown, and never the delivery mechanism: Idle polls
	// whether or not anything sends here, so a producer that stops sending costs
	// latency and nothing else. Closing it says there will be no more hints — the
	// loop stops waiting on it and follows on Idle alone, because a closed channel
	// is permanently ready and receiving from one is a busy read of the log.
	Wake <-chan struct{}

	Backoff  Backoff
	Attempts int
	Tolerate time.Duration

	OnPermanentFailure Failure
	Quarantine         Quarantines

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

	tracker, err := event.Track(spec.Checkpoints, spec.Name)
	if err != nil {
		problems = append(problems, namedRefusal(err))
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
		problems = append(problems, fmt.Errorf("%w: OnPermanentFailure is neither Halt nor Quarantine, and an unknown policy is not Halt", ErrSpec))
	}
	if spec.OnPermanentFailure == Quarantine && absent(spec.Quarantine) {
		problems = append(problems, fmt.Errorf("%w: OnPermanentFailure is Quarantine and Quarantine names no sink, and a policy with nowhere to record is a skip with extra words", ErrSpec))
	}
	problems = append(problems, refusedNumbers(spec)...)
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	return newProjection(withDefaults(spec), tracker), nil
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

func refusedNumbers(spec Spec) []error {
	var problems []error
	for _, bound := range []struct {
		field  string
		chosen time.Duration
	}{
		{"Idle", spec.Idle},
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
func withDefaults(spec Spec) Spec {
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
