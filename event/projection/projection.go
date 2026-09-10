package projection

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/runtime"
)

// The loop's own fields — the reader, the page it holds, the wake channel and
// the streak it is inside — are read and written by Run's goroutine alone. What
// the other three methods reach is the state under the mutex, the accumulated
// backoff, and four channels.
//
// The advance is not among them. It is the tracker's, which presents it and
// answers what it presented, so there is one copy of the fence in this loop and
// it is not this value's.
type Projection struct {
	spec     Spec
	identity Identity
	tracker  *event.Tracker

	mutex sync.Mutex
	state State

	reader      *event.Reader
	page        []event.Envelope
	matched     []event.Envelope
	keys        []string
	cursor      event.Cursor
	unsettled   *pending
	wake        <-chan struct{}
	attempt     int
	opened      int
	applied     uint64
	quarantined uint64
	parked      uint64
	resumed     bool
	streak      int
	contested   bool

	waited  atomic.Int64
	halted  atomic.Bool
	running atomic.Bool

	drain   chan struct{}
	drained chan struct{}
	halting chan struct{}
	done    chan struct{}

	asking   sync.Once
	settling sync.Once
	ceasing  sync.Once
}

var _ runtime.Runner = (*Projection)(nil)

func newProjection(spec Spec, tracker *event.Tracker, identity Identity) *Projection {
	return &Projection{
		spec:     spec,
		identity: identity,
		tracker:  tracker,
		state:    State{Projection: spec.Name, Identity: identity, Phase: PhaseStarting},
		resumed:  true,
		wake:     spec.Wake,
		drain:    make(chan struct{}),
		drained:  make(chan struct{}),
		halting:  make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// The supervisor's duplicate-name refusal covers two runners of one identity in
// one process, and an operator sees the name they chose. It is the identity
// rather than Spec.Name because two partitions of one projection are two runners
// and two checkpoint rows, and at Ungenerated over the whole key space the two
// render alike.
func (this *Projection) Name() string { return "vv.event.projection." + this.identity.String() }

// A promise about how a deployment should run it and not an enforcement: the
// checkpoint's fence is what actually holds when a deployment ignores it.
func (this *Projection) Declaration() runtime.Declaration {
	return runtime.Declaration{Placement: runtime.Singleton, Durability: runtime.Durable}
}

// Blocks until the context is done and returns ctx.Err(), never ErrHalted: a
// returning runner is a failure the supervisor reports and by default takes the
// process down for, and one projection that cannot apply one page is not a
// reason to stop serving.
//
// Six properties of this loop are load-bearing and none of them is visible from
// its shape.
//
// The read is outside every unit of work, which is not a style preference: a
// store's walk mints no settlement bound while a transaction of its backing is
// bound, so a walk inside the projection's own write transaction stops at the
// first burnt gap and stays there for the life of that transaction. A projection
// that opened the unit first would deadlock its own progress on the first
// rolled-back append in the log, in production, with every test over a gapless
// log green.
//
// The framework opens no transaction. Spec.Unit is the application's.
//
// The checkpoint advances only for a page this projection finished with. An
// empty page's cursor is kept in the reader and never saved, so an idle
// projection issues no writes at all and a restart costs one round trip to
// re-settle.
//
// A retry re-applies the page it holds and does not re-read: the page and the
// cursor are captured together at the read, and each attempt is handed its own
// copy of the page.
//
// A halted projection keeps running and does exactly nothing — no read, no save,
// no handler call, no ticker — and a halt is terminal for this value's life.
//
// And the drain is acknowledged between passes, so a shutdown never lands
// between a handler's write and the advance that accounts for it.
func (this *Projection) Run(ctx context.Context) error {
	if !this.running.CompareAndSwap(false, true) {
		return fmt.Errorf("%w: %q was run twice, and one projection value is one loop", ErrSpec, this.spec.Name)
	}
	defer close(this.done)

	idle := this.spec.Ticks(this.spec.Idle)
	defer idle.Stop()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if this.halted.Load() || this.acknowledge() {
			return this.until(ctx)
		}
		taken, err := this.once(ctx)
		switch {
		case err != nil:
			return err
		case taken == stopNow:
			return this.until(ctx)
		case taken == waitIdle:
			if err := this.follow(ctx, idle); err != nil {
				return err
			}
		case taken == waitBackoff:
			if err := this.backoff(ctx); err != nil {
				return err
			}
		}
	}
}

// Stops the loop after the pass it is in, and returns at once when there is no
// pass to finish: a supervisor drains before it cancels precisely so a runner
// can commit its last unit of work, and a halted projection or one whose Run has
// not begun would otherwise hold the whole supervisor to its grace for nothing.
// It starts no goroutine — one channel closed by the loop and one closed here
// are the whole mechanism.
func (this *Projection) Drain(ctx context.Context) error {
	this.asking.Do(func() { close(this.drain) })
	if !this.running.Load() {
		return nil
	}
	select {
	case <-this.drained:
	case <-this.halting:
	case <-this.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// The readiness half, and it names no importance and no code. A single failure
// followed by a success never reports unhealthy: what is measured is the backoff
// this projection has already waited in the streak it is inside, which a pass
// that read or that saved resets.
func (this *Projection) Ready(context.Context) error {
	state := this.State()
	if state.Phase == PhaseHalted {
		return state.Err
	}
	waited := time.Duration(this.waited.Load())
	if waited <= this.spec.Tolerate {
		return nil
	}
	if state.Err != nil {
		return fmt.Errorf("projection %q has been retrying for %s of backoff, past the %s it tolerates: %w", this.spec.Name, waited, this.spec.Tolerate, state.Err)
	}
	return fmt.Errorf("projection %q has been retrying for %s of backoff, past the %s it tolerates", this.spec.Name, waited, this.spec.Tolerate)
}

func (this *Projection) until(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// A wake is a hint layered on the poll and never the delivery mechanism, and a
// closed channel is the one shape that would make it one: it is permanently
// ready, so receiving from it without asking whether it is still open reads the
// log as fast as the store can answer, for ever. Closing a channel is how Go
// broadcasts to an unknown number of waiters, so a composition root that closes
// Wake at its shutdown is the ordinary case. The loop stops waiting on it — a nil
// channel is never ready in a select — and follows on the poll alone.
func (this *Projection) follow(ctx context.Context, idle runtime.Ticker) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-this.drain:
			return nil
		case _, open := <-this.wake:
			if open {
				return nil
			}
			this.wake = nil
		case <-idle.Ticks():
			return nil
		}
	}
}

// The wait is measured through the same injectable ticker the poll uses, so a
// test drives the schedule rather than sleeping through it, and what Ready reads
// is the backoff that was actually waited rather than a clock nobody injected.
func (this *Projection) backoff(ctx context.Context) error {
	delay := this.delay()
	waiting := this.spec.Ticks(delay)
	defer waiting.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-this.drain:
		return nil
	case <-waiting.Ticks():
	}
	this.waited.Add(int64(delay))
	return nil
}

func (this *Projection) delay() time.Duration {
	delay := this.spec.Backoff.First
	for range max(this.streak-1, 0) {
		if delay >= this.spec.Backoff.Max/2 {
			return this.spec.Backoff.Max
		}
		delay *= 2
	}
	return min(delay, this.spec.Backoff.Max)
}

func (this *Projection) acknowledge() bool {
	select {
	case <-this.drain:
		this.settling.Do(func() { close(this.drained) })
		return true
	default:
		return false
	}
}

// Terminal for this value's life: there is no Resume, no Retry and no Clear, and
// the exit is a new value in a new process after the operator has fixed what
// halted it. A halt that could clear itself would be a retry loop with a longer
// period, and the classifier already said the failure was permanent.
func (this *Projection) stop(cause error) (step, error) {
	this.halted.Store(true)
	this.ceasing.Do(func() { close(this.halting) })
	this.transition(PhaseHalted, this.attempt, cause)
	return stopNow, nil
}
