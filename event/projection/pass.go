package projection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/event"
)

// What the loop does next, and the four are exhaustive: read again at once,
// wait out the poll, wait out a backoff, or stop advancing for good.
type step uint8

const (
	readAgain step = iota
	waitIdle
	waitBackoff
	stopNow
)

var (
	errUnitRolledBack  = errors.New("projection: the unit of work carrying this page did not commit")
	errUnitRanNothing  = errors.New("projection: this unit of work answered without running the work it was given")
	errUnitAnsweredNil = errors.New("projection: this unit of work answered nil for a body that did not")
	errUnitKeptAPage   = errors.New("projection: this unit of work committed an advance whose handler had refused the page it accounts for, so it does not roll back on the error it answered")
	errUnitRanTwice    = errors.New("projection: this unit of work ran the work it was given more than once, and one pass presents one advance: a unit that retries its transaction answers the failure instead, and the page is re-delivered")
	errFenceRefused    = errors.New("projection: this checkpoint store refused a save over the very row its own fence admits")
	errRowRetired      = errors.New("projection: the checkpoint row this projection was recording through was retired or restored behind it")
)

// One pass: resume if this is the first, settle a save whose outcome could not
// be read off its answer, read a page if none is held, and deliver the page it
// holds. A retry re-enters here with the page still held and issues no read at
// all.
func (this *Projection) once(ctx context.Context) (step, error) {
	if this.reader == nil {
		if taken, err := this.resume(ctx); err != nil || taken != readAgain {
			return taken, err
		}
	}
	if this.unsettled != nil {
		return this.settle(ctx)
	}
	if this.page == nil {
		if taken, err := this.read(ctx); err != nil || taken != readAgain {
			return taken, err
		}
	}
	return this.deliver(ctx, this.wholePage())
}

func (this *Projection) resume(ctx context.Context) (step, error) {
	held, err := this.tracker.Load(ctx)
	if err != nil {
		return this.refused(err, "the checkpoint store")
	}
	reader, err := event.Read(this.spec.Log, held.Cursor)
	if err != nil {
		return this.refused(err, "the log")
	}
	this.reader = reader
	this.applied, this.quarantined = held.Progress.Applied, held.Progress.Quarantined
	this.seed(held.Progress)
	return readAgain, nil
}

// A read that answered ends whatever streak the loop was in, for the reason a
// save that landed does: what Ready measures is a run of consecutive failures,
// and a store that answered is not failing.
func (this *Projection) read(ctx context.Context) (step, error) {
	more, err := this.reader.Next(ctx)
	if err != nil {
		return this.refused(err, "the log")
	}
	this.settled()
	if !more {
		this.transition(PhaseFollowing, 0, nil)
		return waitIdle, nil
	}
	this.page, this.cursor, this.attempt = this.reader.Events(), this.reader.Cursor(), 1
	this.transition(PhaseDraining, this.attempt, nil)
	return readAgain, nil
}

// What one delivery reached, which is how an error out of the caller's own unit
// of work is read: the unit may answer anything at all, so what the pass knows
// is what its own body got to before that answer.
type tally struct {
	refusal error
	applied error
	sink    error
	save    error

	saved     bool
	isolated  bool
	retryable bool

	presented uint64
	progress  event.Progress

	envelopes   uint64
	quarantined uint64
}

// What delivers a page, and whether what it will have applied is known before it
// runs. The whole page is: it applies every envelope or it fails. The isolation
// pass is not: what it quarantined is what its own run discovered.
type applier struct {
	run      func(ctx context.Context, held *tally) error
	foreseen func(held *tally)
}

func (this *Projection) deliver(ctx context.Context, apply applier) (step, error) {
	if this.spec.Advance == InUnit {
		return this.insideAUnit(ctx, apply)
	}
	return this.outsideAUnit(ctx, apply)
}

// Handle first, advance last, and outside a unit that order is the delivery
// guarantee itself rather than a preference: an advance written before the
// handler ran is a page a crash loses.
func (this *Projection) outsideAUnit(ctx context.Context, apply applier) (step, error) {
	var held tally
	if err := apply.run(ctx, &held); err != nil {
		return this.applyFailed(ctx, held, err)
	}
	if apply.foreseen != nil {
		apply.foreseen(&held)
	}
	if err := this.presentSave(ctx, &held); err != nil {
		return this.saveFailed(ctx, held, err)
	}
	this.landed(held.progress)
	return readAgain, nil
}

// The two per-pass checks InUnit owes are made here, inside the unit and before
// the handler runs, and they halt directly rather than reaching the classifier:
// they are the framework's own wiring refusals, and a classifier an application
// supplied must not be able to call one retryable and spin on it for ever.
//
// What the unit answered is read against what the body reached, never against
// its text: the caller owns Unit and may return anything. A body that saved and
// a unit that then failed is the one window where the row may or may not have
// moved, and the settlement below is what bounds it.
//
// A second run of the work inside one pass is refused before it writes anything,
// and what the tally holds is the FIRST run's, deliberately: the fence moved on
// that run, and a second save would present one above a number the row may never
// have taken. The refusal is not a halt — a caller's own retry around the
// transaction is the shape [[D-126]] tells them to own — so the settlement below
// reads the row and finds either the first run's advance, which lands, or the one
// below it, which re-delivers the page.
func (this *Projection) insideAUnit(ctx context.Context, apply applier) (step, error) {
	var held tally
	var entered bool
	unit := this.spec.Unit(ctx, func(inner context.Context) error {
		if entered {
			return errUnitRanTwice
		}
		entered = true
		if held.refusal = this.checkUnit(inner); held.refusal != nil {
			return held.refusal
		}
		return this.claimed(inner, apply, &held)
	})
	switch {
	case held.refusal != nil:
		return this.stop(this.haltedBy(held.refusal))
	case held.saved && held.applied != nil:
		return this.awaiting(ctx, pending{presented: held.presented, cause: answered(unit), failure: held.applied})
	case held.applied != nil:
		return this.applyFailed(ctx, held, held.applied)
	case held.save != nil:
		return this.saveFailed(ctx, held, held.save)
	case held.saved && unit == nil:
		this.landed(held.progress)
		return readAgain, nil
	case held.saved:
		return this.awaiting(ctx, pending{presented: held.presented, cause: unit})
	case unit != nil:
		return this.applyFailed(ctx, held, unit)
	default:
		return this.stop(this.haltedBy(errUnitRanNothing))
	}
}

// Inside the unit the advance and the handler's writes commit together, so the
// order they are written in is invisible to everyone but the lock manager — and
// that is what it is used for here. A fenced save waits on the tuple the winner
// is holding, so presenting the advance BEFORE the handler runs is this
// framework's spelling of the reference implementation's claim-before-read: a
// second live instance of one projection name blocks there, is refused when the
// winner commits, and never applies the page at all. Outside a unit the same
// order would be an advance a crash could leave over an unapplied page, which is
// why it lives here and not in the pass above.
//
// The isolation pass is the one applier whose tally only its own run knows —
// what the sink took is what it discovered — so it saves afterwards, and pays
// the double-apply this order exists to close.
func (this *Projection) claimed(ctx context.Context, apply applier, held *tally) error {
	if apply.foreseen == nil {
		if held.applied = apply.run(ctx, held); held.applied != nil {
			return held.applied
		}
		if held.save = this.presentSave(ctx, held); held.save != nil {
			return held.save
		}
		held.saved = true
		return nil
	}
	apply.foreseen(held)
	if held.save = this.presentSave(ctx, held); held.save != nil {
		return held.save
	}
	held.saved = true
	if held.applied = apply.run(ctx, held); held.applied != nil {
		return held.applied
	}
	return nil
}

func (this *Projection) wholePage() applier {
	return applier{
		run: func(ctx context.Context, _ *tally) error {
			return this.applyPage(ctx, this.page, this.attempt)
		},
		foreseen: func(held *tally) { held.envelopes = uint64(len(this.page)) },
	}
}

// The isolation pass, which is how quarantine buys envelope granularity with
// re-delivery rather than with a second handler signature: the page is delivered
// again one envelope to a Batch, in position order, an envelope that applies is
// applied, and one whose failure is permanent goes to the sink with its cause
// and is passed. A retryable failure ends the pass and returns the whole page to
// retrying under the same attempt budget, because a database that went away is
// not a corrupt payload — and once that budget is spent the same rule that made
// the page's failure permanent makes this one permanent too, which is what keeps
// an exhausted retry from re-entering here for ever.
func (this *Projection) oneAtATime() applier {
	return applier{run: func(ctx context.Context, held *tally) error {
		held.isolated = true
		for _, envelope := range this.page {
			err := this.applyPage(ctx, []event.Envelope{envelope}, this.attempt)
			switch {
			case err == nil:
				held.envelopes++
				continue
			case stopping(err):
				return err
			case !this.permanent(err):
				held.retryable = true
				return err
			}
			if held.sink = this.quarantine(ctx, envelope, err); held.sink != nil {
				return held.sink
			}
			held.quarantined++
		}
		return nil
	}}
}

func (this *Projection) quarantine(ctx context.Context, envelope event.Envelope, cause error) error {
	return this.spec.Quarantine.Quarantine(ctx, Quarantined{
		Projection: this.spec.Name,
		Envelope:   envelope,
		Cause:      cause,
	})
}

func (this *Projection) applyPage(ctx context.Context, page []event.Envelope, attempt int) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &panicked{value: recovered}
		}
	}()
	return this.spec.Handler.Apply(ctx, Batch{Projection: this.spec.Name, Envelopes: copyOf(page), Attempt: attempt})
}

// Built at the save and nowhere else, so it describes a page that was applied or
// quarantined rather than one that was merely delivered. Both counts are
// cumulative across restarts, seeded from the checkpoint the resume answered,
// because the columns are persisted and a dashboard that resets on every deploy
// is one nobody can read. Applied counts envelopes.
//
// The advance is read back from the tracker that presented it rather than
// computed here, because the settlement below compares it against the row and a
// second copy of the fence is a number the store never saw.
func (this *Projection) presentSave(ctx context.Context, held *tally) error {
	held.progress = event.Progress{
		Highest:     this.page[len(this.page)-1].Position,
		Applied:     this.applied + held.envelopes,
		Quarantined: this.quarantined + held.quarantined,
		At:          time.Now(),
	}
	presented, err := this.tracker.Save(ctx, this.cursor, held.progress)
	held.presented = presented
	return err
}

func (this *Projection) applyFailed(ctx context.Context, held tally, err error) (step, error) {
	switch {
	case stopping(err):
		return stopNow, err
	case held.sink != nil:
		return this.stop(this.haltedBy(fmt.Errorf("the quarantine sink refused an envelope this projection could not apply: %w", err)))
	case held.retryable:
		return this.redeliver(err)
	case !this.permanent(err):
		return this.redeliver(err)
	case this.spec.OnPermanentFailure == Quarantine && !held.isolated:
		this.attempt++
		return this.deliver(ctx, this.oneAtATime())
	default:
		return this.stop(this.haltedBy(err))
	}
}

func (this *Projection) permanent(err error) bool {
	if _, panicking := asPanic(err); panicking {
		return true
	}
	if this.spec.Classifier(err) == Permanent {
		return true
	}
	return this.attempt >= this.spec.Attempts
}

func (this *Projection) saveFailed(ctx context.Context, held tally, err error) (step, error) {
	switch {
	case stopping(err):
		return stopNow, err
	case errors.Is(err, event.ErrUncertain), errors.Is(err, event.ErrConflict):
		return this.awaiting(ctx, pending{presented: held.presented, cause: err})
	default:
		return this.refused(err, "the checkpoint store")
	}
}

// A save whose outcome could not be read off the answer it got, and the one load
// that reads it off the row instead. It is held rather than resolved on the
// spot, so a backend that goes away during the resolution is retried AS the
// resolution: re-delivering the page would present a second checkpoint over a
// save the tracker cannot account for, which the door refuses rather than
// guessing at.
type pending struct {
	presented uint64

	// What left the outcome unread: ErrUncertain from a save nobody confirmed,
	// ErrConflict from one the fence refused, or the caller's own unit answering
	// after the save inside it had already returned nil.
	cause error

	// The handler's failure this pass still owes an answer for. Set only under
	// InUnit, where the advance is claimed before the handler runs, so a page the
	// handler refused leaves a save that landed inside a unit that then rolled
	// back — and which of those two the row holds decides whether the failure is
	// answered at all.
	failure error
}

func (this *Projection) awaiting(ctx context.Context, waiting pending) (step, error) {
	this.unsettled = &waiting
	return this.settle(ctx)
}

// The bounded resolution, for every save whose fate its own answer did not
// carry: one load, and the advance and the cursor the row carries decide between
// four outcomes. A second save could only guess.
//
// The row is re-read through a tracker of its own, and that is deliberate. A
// tracker's window refuses a row that moved under a live one — and here the loop
// already knows it moved, because the store either refused the save over the
// fence or never confirmed it. What decides which moves are legal is the table
// below, which tells four apart where the window has one refusal for all of
// them; the door's other re-checks — a half-absent row, an empty cursor at a
// live advance, another name's row, a cursor over the ceiling — run exactly as
// they do on the first resume, because those are about the answer's shape rather
// than about a tracker's life.
func (this *Projection) settle(ctx context.Context) (step, error) {
	waiting := *this.unsettled
	tracker, err := event.Track(this.spec.Checkpoints, this.spec.Name)
	if err != nil {
		return this.refused(err, "the checkpoint store")
	}
	found, err := tracker.Load(ctx)
	if err != nil {
		return this.refused(err, "the checkpoint store")
	}
	this.unsettled = nil
	switch {
	case found.Advance > waiting.presented, found.Advance == waiting.presented && this.anothers(found, waiting.cause):
		return this.overtaken(found, tracker)
	case found.Advance == waiting.presented:
		return this.confirmed(waiting, found, tracker)
	case found.Advance+1 == waiting.presented && fenced(waiting.cause):
		return this.stop(this.haltedBy(fmt.Errorf("%w: %w", errFenceRefused, waiting.cause)))
	case found.Advance+1 == waiting.presented && this.spec.Advance == InUnit:
		return this.rolledBack(ctx, waiting, found, tracker)
	case found.Advance+1 == waiting.presented:
		return this.stop(this.haltedBy(fmt.Errorf("a checkpoint save was issued and never confirmed, the row did not move, and the page it accounts for is already applied outside any unit: %w", waiting.cause)))
	default:
		return this.stop(this.haltedBy(fmt.Errorf("%w: %w", errRowRetired, waiting.cause)))
	}
}

func fenced(cause error) bool { return errors.Is(cause, event.ErrConflict) }

// Whether the row standing at the advance this pass presented is another
// writer's. A refused save says so on its own answer. An unconfirmed one is
// decided by the cursor, because the advance alone cannot decide it: the fence
// admits one writer at each advance, this pass presented its own cursor beside
// it, and a row at that advance carrying any other cursor was written by
// somebody else — a second live instance reaching the same fence the moment
// this pass's unit rolled back and released the row. A row carrying this pass's
// own cursor may still be that instance's, and it does not matter: two passes
// that read one page resume at one point.
func (this *Projection) anothers(found event.Checkpoint, cause error) bool {
	return fenced(cause) || found.Cursor != this.cursor
}

// A unit of work that answers nil for a body that did not is making a claim
// rather than making none, and the settlement names it as one.
func answered(unit error) error {
	if unit == nil {
		return errUnitAnsweredNil
	}
	return unit
}

// The row is at the advance this pass presented, carries the cursor it
// presented, and nothing refused this pass's own save, so this pass wrote it —
// or a second instance wrote one indistinguishable from it, which resumes at the
// same point. The exception is the one write this framework cannot make safe: an
// advance claimed before a handler that then refused the page, committed anyway
// by a unit of work that answered an error. That is a page skipped over a
// handler that never applied it, and it is a halt.
func (this *Projection) confirmed(waiting pending, found event.Checkpoint, tracker *event.Tracker) (step, error) {
	if waiting.failure != nil {
		return this.stop(this.haltedBy(fmt.Errorf("%w: %w", errUnitKeptAPage, waiting.failure)))
	}
	this.tracker = tracker
	this.landed(found.Progress)
	return readAgain, nil
}

// The row is one below what this pass presented and this pass's own save was
// admitted, so the whole unit rolled back and the page is where the projection
// left it. Under InUnit that is proof about the handler's writes too: they went
// back with the advance. What the unit answered travels with the redelivery,
// because a projection that backs off for ever over a unit assembled wrong is
// one nobody can diagnose from "it did not commit".
func (this *Projection) rolledBack(ctx context.Context, waiting pending, found event.Checkpoint, tracker *event.Tracker) (step, error) {
	this.tracker = tracker
	if waiting.failure != nil {
		return this.applyFailed(ctx, tally{}, waiting.failure)
	}
	return this.redeliver(fmt.Errorf("%w: %w", errUnitRolledBack, waiting.cause))
}

// A second live writer at this name took the advance this pass presented. The
// deployment declared this runner a singleton and is running two, which is what
// every rolling restart does for a few seconds on purpose — so this is not a
// halt: the row the other instance left is adopted, the reader is rebuilt from
// ITS cursor rather than from this pass's, the page in hand is dropped, and the
// loop backs off. Backing off is what bounds the cost, because the two
// instances applied the overlapping page twice and only InUnit rolled the
// loser's half back; and the streak it opens is cleared by a save that lands and
// by nothing else, so a projection that keeps losing reports through Ready
// rather than duplicating quietly for ever.
func (this *Projection) overtaken(found event.Checkpoint, tracker *event.Tracker) (step, error) {
	reader, err := event.Read(this.spec.Log, found.Cursor)
	if err != nil {
		return this.refused(err, "the log")
	}
	this.tracker, this.reader = tracker, reader
	this.applied, this.quarantined = found.Progress.Applied, found.Progress.Quarantined
	this.page, this.cursor, this.attempt = nil, "", 0
	this.contested = true
	this.seed(found.Progress)
	this.streak++
	this.transition(PhaseRetrying, this.attempt, fmt.Errorf("%w: %q took the row it left", ErrOvertaken, this.spec.Name))
	return waitBackoff, nil
}

// The whole failure table for a store's refusal, and it is total by its last
// arm rather than by hope: a pass that cannot classify its own failure must not
// fall through to the retryable one, which is how a structurally impossible
// write becomes a loop nothing clears.
func (this *Projection) refused(err error, door string) (step, error) {
	switch {
	case stopping(err):
		return stopNow, err
	case errors.Is(err, event.ErrBackend):
		return this.postpone(err)
	default:
		return this.stop(fmt.Errorf("%w: %q was refused by %s: %w", ErrHalted, this.spec.Name, door, err))
	}
}

func (this *Projection) haltedBy(cause error) error {
	return fmt.Errorf("%w: %q: %w", ErrHalted, this.spec.Name, cause)
}

func (this *Projection) redeliver(cause error) (step, error) {
	this.attempt++
	this.streak++
	this.transition(PhaseRetrying, this.attempt, cause)
	return waitBackoff, nil
}

// A store's failure costs no attempt: the budget bounds handler failures, and a
// store retries without limit because the database coming back is the normal
// case and nothing is lost while the checkpoint stands still.
func (this *Projection) postpone(cause error) (step, error) {
	this.streak++
	this.transition(PhaseRetrying, this.attempt, cause)
	return waitBackoff, nil
}

func (this *Projection) landed(progress event.Progress) {
	this.applied, this.quarantined = progress.Applied, progress.Quarantined
	this.page, this.cursor, this.attempt = nil, "", 0
	this.contested = false
	this.settled()
	this.progressed(progress)
}

// A store that answered ends a streak of store failures. It does not end one a
// second writer opened: a losing instance reads and applies perfectly well every
// pass and lands nothing, so a streak cleared by the read would leave Ready
// reporting health while the read model took every page twice.
func (this *Projection) settled() {
	if this.contested {
		return
	}
	this.streak = 0
	this.waited.Store(0)
}

// InUnit is checked as far as it can be seen, per pass, inside the unit and
// before the handler runs. What no check can see is a Destination naming one
// resource while the handler writes to another, and Unchecked is how a
// composition says out loud that it cannot be resolved at all.
func (this *Projection) checkUnit(ctx context.Context) error {
	authority, err := this.tracker.Transaction(ctx)
	if err != nil {
		return fmt.Errorf("%w: %q asked for the advance inside the caller's unit and what that unit bound for the checkpoint store is not a transaction: %w", ErrSpec, this.spec.Name, err)
	}
	if !authority.Valid() {
		return fmt.Errorf("%w: %q asked for the advance inside the caller's unit and that unit bound no transaction of the checkpoint store's backing, so the advance would be a second write beside the handler's", ErrSpec, this.spec.Name)
	}
	if _, unresolvable := this.spec.Destination.(unchecked); unresolvable {
		return nil
	}
	executor, bound := crud.ExecutorFor(ctx, this.spec.Destination)
	if !bound {
		return fmt.Errorf("%w: %q names a Destination the unit bound no executor for, so the handler's writes are outside the transaction the advance rides in", ErrSpec, this.spec.Name)
	}
	if !crud.IsTransaction(executor) {
		return fmt.Errorf("%w: %q names a Destination the unit bound an executor for and it is not a transaction, so the handler's writes are outside the transaction the advance rides in", ErrSpec, this.spec.Name)
	}
	return nil
}

// A handler has an error channel, so its panic is recovered into a failure of
// that page rather than taken to be one of the framework's. It is permanent by
// the same argument a corrupt payload is: nothing about the next attempt is
// different. The value travels on the error and reaches an operator through the
// observer, and no line of this framework's renders it.
type panicked struct{ value any }

func (this *panicked) Error() string { return fmt.Sprintf("the handler panicked: %v", this.value) }

func asPanic(err error) (*panicked, bool) {
	var found *panicked
	return found, errors.As(err, &found)
}

func stopping(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
