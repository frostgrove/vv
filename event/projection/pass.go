package projection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/event"
)

// What the loop does next, and the five are exhaustive: read again at once,
// wait out the poll, wait out the read throttle, wait out a backoff, or stop
// advancing for good.
type step uint8

const (
	readAgain step = iota
	waitIdle
	waitPace
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
// be read off its answer, count what the park holds if this loop has reason to,
// read a page if none is held, and deliver the page it holds. A retry re-enters
// here with the page still held and issues no read at all.
func (this *Projection) once(ctx context.Context) (step, error) {
	if this.reader == nil {
		if taken, err := this.resume(ctx); err != nil || taken != readAgain {
			return taken, err
		}
	}
	if this.unsettled != nil {
		return this.settle(ctx)
	}
	if taken, err := this.counted(ctx); err != nil || taken != readAgain {
		return taken, err
	}
	if this.page == nil {
		if taken := this.throttled(); taken != readAgain {
			return taken, nil
		}
		if taken, err := this.read(ctx); err != nil || taken != readAgain {
			return taken, err
		}
	}
	this.opened = this.attempt
	return this.deliver(ctx, this.delivering())
}

// The park's one clearing rule, in one place because three of them is how an
// invariant becomes false without anybody editing it. The count is read once per
// resume — a process start, or an overtaken that adopted another instance's row
// — and again at the start of every pass while the count this loop believes is
// non-zero.
//
// A healthy projection therefore pays one round trip per resume and nothing per
// pass, and Holds is never called at all. A degraded one pays one per page and
// clears at the first pass after a drain empties the queue, which is what makes
// State.Parked a live number rather than "parked since this instance resumed".
//
// What makes this loop believe is the park's own answer and its own writes:
// only the projection parks, so a letter this pass wrote is a sequence the next
// pass has to ask Holds about, and a letter an operator inserted by hand leaves
// the count low until the next pass that reads it.
//
// A read that fails postpones rather than halting. It is a read of the
// application's own table, and stopping a live projection because that table
// blinked is a projector stopped by another route.
func (this *Projection) counted(ctx context.Context) (step, error) {
	if this.spec.Park == nil || (!this.resumed && this.parked == 0) {
		return readAgain, nil
	}
	count, err := this.spec.Park.Sequences(ctx, this.identity.Whole())
	if err != nil {
		return this.postpone(fmt.Errorf("the park of %q could not be counted: %w", this.identity, err))
	}
	this.resumed = false
	this.counting(count)
	return readAgain, nil
}

func (this *Projection) resume(ctx context.Context) (step, error) {
	if taken, err := this.unclaimed(ctx); err != nil || taken != readAgain {
		return taken, err
	}
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
	this.resumed = true
	this.seed(held.Progress)
	return readAgain, nil
}

// Only the first start of a projection chooses its topology, so a runner asks
// the store, once, whether its own share is already being recorded by another
// shape. Two questions, and both are rows rather than a stored count: a live row
// at "orders" beside a runner at "orders#0.1" is two writers over every key of
// that half, each with its own fence, each reporting a healthy watermark, and
// the second one walks the whole log again into the read model the first one
// filled.
//
// The check is here rather than in New because New performs no I/O, and it costs
// one Load for the retirement plus one per coarser ancestor — one in total for a
// projection that named no partition, which is every projection that exists
// today.
func (this *Projection) unclaimed(ctx context.Context) (step, error) {
	if taken, err := this.unretired(ctx); err != nil || taken != readAgain {
		return taken, err
	}
	for _, coarser := range this.identity.coarser() {
		tracker, err := event.Track(this.spec.Checkpoints, coarser.String())
		if err != nil {
			return this.refused(err, "the checkpoint store")
		}
		found, err := tracker.Load(ctx)
		if err != nil {
			return this.refused(err, "the checkpoint store")
		}
		if found.Fresh() {
			continue
		}
		return this.stop(this.haltedBy(fmt.Errorf("%w: %q records through a checkpoint row standing at advance %d for a coarser share of this key space, and a partitioned runner started beside one delivers every key of that share twice — the route from a projection that has run to a partitioned one is Split, which hands the parent's cursor to its children and retires the parent, and a topology that never ran has no row to retire",
			ErrTopology, coarser, found.Advance)))
	}
	return readAgain, nil
}

// The mirror of the question above, asked of this runner's own share. A split
// retires the parent's row, so the release that ran before it finds no row of
// its own and none for any ancestor either: it resumes from the origin and walks
// the whole log into the read model its children are filling, with every row
// reporting a healthy watermark while it happens. Redeploying the previous
// release is the ordinary rollback of a bad deploy, so this is not an exotic
// path — it is the one an operator reaches for first.
//
// The record a split leaves is what makes it visible, and removing that record
// is the deliberate act that re-admits the coarser topology. The refusal names
// the row so an operator can do it.
func (this *Projection) unretired(ctx context.Context) (step, error) {
	tracker, found, err := retired(ctx, this.spec.Checkpoints, this.identity)
	if err != nil {
		return this.refused(err, "the checkpoint store")
	}
	if tracker == nil || found.Fresh() {
		return readAgain, nil
	}
	return this.stop(this.haltedBy(fmt.Errorf("%w: %q was retired by a split, which handed its cursor to two children and removed its row, and a runner started at a retired share walks the whole log again into the read model those children are filling. The row %q records the retirement at the cursor it was handed down from; the route back to one runner over this share is a new generation, and forgetting that row is the deliberate act that re-admits this topology",
		ErrTopology, this.identity, tracker.Projection())))
}

// Pace gates the read and nothing else, and only while the previous read
// answered that there is more: a rebuild draining beside a live projection is
// what it is for, and a projection that has caught up waits out Idle instead. It
// is consumed once per read, so a retry — which re-applies the page it holds and
// issues no read — waits for nothing here, and a Pace of zero consumes it and
// reads at once.
func (this *Projection) throttled() step {
	if !this.pacing {
		return readAgain
	}
	this.pacing = false
	if this.spec.Pace == 0 {
		return readAgain
	}
	return waitPace
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
		this.transition(this.followed(), 0, nil)
		return waitIdle, nil
	}
	page := this.reader.Events()
	matched, keys, err := this.matching(page)
	if err != nil {
		return this.stop(this.haltedBy(err))
	}
	this.pacing = true
	this.page, this.matched, this.keys, this.cursor, this.attempt = page, matched, keys, this.reader.Cursor(), 1
	this.transition(PhaseDraining, this.attempt, nil)
	return readAgain, nil
}

// Caught up is never published while a sequence is parked: the whole log has
// been read and part of it was not applied, which is a different thing to say
// and the one an operator watching for the end of their drain needs said.
func (this *Projection) followed() Phase {
	if this.parked > 0 {
		return PhaseDegraded
	}
	return PhaseFollowing
}

// This runner's share of the page it just read and the key each envelope of it
// belongs to, decided once per read and held beside the page so a retry
// re-applies what the first attempt did rather than asking the sequencer again.
// A projection over the whole key space with no queue asks nothing at all, which
// is every projection that exists today.
//
// The keys are computed here rather than where they are used, and that is what
// keeps a sequencer's panic out of the caller's unit of work: it halts. It is
// not recovered into a failure of the page the way a handler's is — an envelope
// that could not be assigned to a sequence belongs to no partition and to no
// queue, so there is nothing for a retry or a park to be about.
func (this *Projection) matching(page []event.Envelope) (held []event.Envelope, keys []string, err error) {
	if this.spec.Partition.Whole() && this.spec.Park == nil {
		return page, nil, nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			held, keys, err = nil, nil, fmt.Errorf("the sequencer %q panicked, and an envelope with no sequence belongs to no partition and to no queue: %v", this.spec.Sequence.Name(), recovered)
		}
	}()
	held, keys = make([]event.Envelope, 0, len(page)), make([]string, 0, len(page))
	for _, envelope := range page {
		key := this.spec.Sequence.SequenceOf(envelope)
		if !this.spec.Partition.Matches(key) {
			continue
		}
		held, keys = append(held, envelope), append(keys, key)
	}
	return held, keys, nil
}

// What one delivery reached, which is how an error out of the caller's own unit
// of work is read: the unit may answer anything at all, so what the pass knows
// is what its own body got to before that answer.
type tally struct {
	refusal error
	applied error
	park    error
	full    error
	read    error
	save    error

	saved     bool
	isolated  bool
	retryable bool

	presented uint64
	progress  event.Progress

	envelopes   uint64
	quarantined uint64

	// What the handler APPLIED in this delivery, which is what an effect is owed
	// for. It is not the page and it is not the matched subset: a parked envelope
	// never reached the handler and is not in here, and its effect is not lost
	// either — the letter carries it.
	owed []event.Envelope
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
//
// The stage runs directly after the handler in both orders, which is where an
// effect belongs: it is owed for what the handler applied, and nothing between
// the two may decide otherwise. Where it sits relative to the save is invisible
// to everything but the lock manager, because all three commit together — and
// running before the save in the order that has one left costs the pass no
// settlement round trip when the sink refuses.
func (this *Projection) claimed(ctx context.Context, apply applier, held *tally) error {
	if apply.foreseen == nil {
		if held.applied = apply.run(ctx, held); held.applied != nil {
			return held.applied
		}
		if held.applied = this.staging(ctx, held); held.applied != nil {
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
	held.applied = this.staging(ctx, held)
	return held.applied
}

// The whole of the effect gate the loop has, and it is the gate's: this pass
// supplies the four values and the order lives in one place, beside the redrive
// that stages through the same one.
//
// It is reached from inside the unit and from nowhere else. Under AfterApply
// there is no call at all, because Effects constructs at no other tier than the
// one whose transaction it rides in.
func (this *Projection) staging(ctx context.Context, held *tally) error {
	return gate{
		effects:     this.spec.Effects,
		after:       this.spec.EffectsAfter,
		generations: this.spec.Generations,
		of:          this.identity,
	}.stage(ctx, held.owed, this.attempt)
}

// Which of the two whole-page appliers this pass uses, and the question is the
// count rather than the wiring: while the queue holds nothing there is nothing
// to ask it about, and the page goes to the handler exactly as it did before
// there was a queue at all.
func (this *Projection) delivering() applier {
	if this.spec.Park == nil || this.parked == 0 {
		return this.matchedPage()
	}
	return this.unblockedPage()
}

// The page this runner's partition matched, which is the whole page for a
// projection over the whole key space. A page that matched nothing still
// advances — the checkpoint is what says this runner has read past that position
// — and the handler is not called with an empty batch, because a batch of no
// envelopes is a delivery that never happened.
//
// It is the one applier that knows what it will apply before it runs, which is
// what lets the advance be claimed before the handler is called.
func (this *Projection) matchedPage() applier {
	return applier{
		run: func(ctx context.Context, held *tally) error {
			if len(this.matched) == 0 {
				return nil
			}
			if err := this.applyPage(ctx, this.matched, this.attempt); err != nil {
				return err
			}
			held.owed = this.matched
			return nil
		},
		foreseen: func(held *tally) { held.envelopes = uint64(len(this.matched)) },
	}
}

// The same page while the queue holds something: every matched envelope is asked
// whether its sequence is parked, one that is goes to the queue behind the
// letters already there WITHOUT REACHING THE HANDLER, and what is left is
// delivered whole. That is the whole of the ordering guarantee on the hot path —
// one existence check per envelope, exactly as the mechanism this is taken from
// spells it — and it is why the check is here rather than in the isolation pass:
// the handler must not be given the fourth event of an order whose second one
// failed.
//
// What it will apply is not known until it has asked, so it saves afterwards and
// pays the double-apply the other order closes. A degraded projection paying that
// is the trade; a healthy one never reaches here.
func (this *Projection) unblockedPage() applier {
	return applier{run: func(ctx context.Context, held *tally) error {
		free := make([]event.Envelope, 0, len(this.matched))
		for index, envelope := range this.matched {
			holds, err := this.spec.Park.Holds(ctx, this.identity.Whole(), this.keys[index])
			if err != nil {
				held.read = fmt.Errorf("the park of %q could not be asked about the sequence %q: %w", this.identity, this.keys[index], err)
				return held.read
			}
			if !holds {
				free = append(free, envelope)
				continue
			}
			if err := this.parking(ctx, held, this.letter(envelope, this.keys[index], nil)); err != nil {
				return err
			}
		}
		if len(free) == 0 {
			return nil
		}
		if err := this.applyPage(ctx, free, this.attempt); err != nil {
			return err
		}
		held.envelopes += uint64(len(free))
		held.owed = free
		return nil
	}}
}

// The isolation pass, which is how a park buys envelope granularity with
// re-delivery rather than with a second handler signature: the page is delivered
// again one envelope to a Batch, in position order, an envelope that applies is
// applied, and one whose failure is permanent is parked with its cause — AND SO
// IS EVERY LATER ENVELOPE OF THE SAME SEQUENCE, which is the difference between
// a dead-letter queue and a skip list. The events behind the blocker never reach
// the handler, so nothing is applied over a read model that never received what
// came before it.
//
// A retryable failure ends the pass and returns the whole page to retrying under
// the same attempt budget, because a database that went away is not a corrupt
// payload — and once that budget is spent the same rule that made the page's
// failure permanent makes this one permanent too, which is what keeps an
// exhausted retry from re-entering here for ever.
//
// Whether it asks the queue is decided once, at the top: a pass that arrived
// here with an empty queue makes no round trip at all, and the sequences it
// blocks are the ones its own run discovered.
func (this *Projection) sequenceBySequence() applier {
	return applier{run: func(ctx context.Context, held *tally) error {
		held.isolated = true
		asking := this.parked > 0
		blocked := map[string]bool{}
		for index, envelope := range this.matched {
			sequence := this.keys[index]
			if !blocked[sequence] && asking {
				holds, err := this.spec.Park.Holds(ctx, this.identity.Whole(), sequence)
				if err != nil {
					held.read = fmt.Errorf("the park of %q could not be asked about the sequence %q: %w", this.identity, sequence, err)
					return held.read
				}
				blocked[sequence] = holds
			}
			if blocked[sequence] {
				if err := this.parking(ctx, held, this.letter(envelope, sequence, nil)); err != nil {
					return err
				}
				continue
			}
			err := this.applyPage(ctx, []event.Envelope{envelope}, this.attempt)
			switch {
			case err == nil:
				held.envelopes++
				held.owed = append(held.owed, envelope)
				continue
			case stopping(err):
				return err
			case !this.permanent(err):
				held.retryable = true
				return err
			}
			if err := this.parking(ctx, held, this.letter(envelope, sequence, err)); err != nil {
				return err
			}
			blocked[sequence] = true
		}
		return nil
	}}
}

// One letter, and the three answers a park has. A letter that was written raises
// the durable count of what the destination did not take and makes this loop
// believe the queue is non-empty, so the next pass reads how far. A park with no
// room ends the pass without skipping anything. Anything else halts, because a
// policy with nowhere to record is a skip with extra words.
func (this *Projection) parking(ctx context.Context, held *tally, letter Letter) error {
	err := this.spec.Park.Park(ctx, letter)
	switch {
	case err == nil:
		held.quarantined++
		if this.parked == 0 {
			this.counting(1)
		}
		return nil
	case errors.Is(err, ErrParkFull):
		held.full = fmt.Errorf("the park of %q has no room for the sequence %q: %w", this.identity, letter.Sequence, err)
		return held.full
	default:
		held.park = fmt.Errorf("the park of %q refused a letter for the sequence %q: %w", this.identity, letter.Sequence, err)
		return held.park
	}
}

// The letter the loop writes, and the identity on it is always Identity.Whole():
// a queue keyed by the partition orphans every letter it holds the moment that
// partition is split.
func (this *Projection) letter(envelope event.Envelope, sequence string, cause error) Letter {
	return Letter{
		Identity:  this.identity.Whole(),
		Sequencer: this.spec.Sequence.Name(),
		Sequence:  sequence,
		Envelope:  envelope,
		Cause:     cause,
		Attempt:   this.attempt,
	}
}

func (this *Projection) applyPage(ctx context.Context, page []event.Envelope, attempt int) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &panicked{value: recovered}
		}
	}()
	return this.spec.Handler.Apply(ctx, Batch{Projection: this.spec.Name, Identity: this.identity, Envelopes: copyOf(page), Attempt: attempt})
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

// Total by construction, and the two effect arms sit ABOVE the park's for a
// reason the order alone does not show: a delivery whose STAGING failed applied
// its page perfectly well, so routing it to the isolation pass would park an
// envelope over a read-model hole that does not exist. They are told by the
// error's type rather than by the tally, because a stage that failed after the
// advance was claimed is answered through a settlement that carries none.
func (this *Projection) applyFailed(ctx context.Context, held tally, err error) (step, error) {
	switch {
	case stopping(err):
		return stopNow, err
	case held.full != nil:
		return this.stalled(held.full)
	case held.read != nil:
		return this.postpone(held.read)
	case held.park != nil:
		return this.stop(this.haltedBy(err))
	case asUnowned(err):
		return this.postpone(err)
	case asUnstaged(err) && !this.permanent(err):
		return this.redeliver(err)
	case asUnstaged(err):
		return this.stop(this.haltedBy(err))
	case held.retryable:
		return this.redeliver(err)
	case !this.permanent(err):
		return this.redeliver(err)
	case this.spec.OnPermanentFailure == ParkSequence && !held.isolated:
		this.attempt++
		return this.deliver(ctx, this.sequenceBySequence())
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
//
// It is keyed by the identity and never by Spec.Name, for the reason every other
// checkpoint call in this loop is: the two render alike only at Ungenerated over
// the whole key space, and a settlement that read the coarse row would decide a
// partition's fate on a row belonging to a different topology — and then adopt
// it, because three of the four arms below assign the tracker they were given.
func (this *Projection) settle(ctx context.Context) (step, error) {
	waiting := *this.unsettled
	tracker, err := event.Track(this.spec.Checkpoints, this.identity.String())
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

// The row another instance left is adopted, and so is what it parked: an
// overtaken is a resume, and the one path where a park this loop did not write
// is real. A survivor that carried its own zero over would ask Holds about
// nothing and hand the next event of the winner's parked sequence to a handler.
//
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
	this.page, this.matched, this.keys, this.cursor, this.attempt = nil, nil, nil, "", 0
	this.contested, this.resumed = true, true
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

// The third verdict, and the one an operator clears rather than a redeploy. The
// unit has already rolled back — there is always one, because ParkSequence
// constructs at no other tier — so nothing was parked, the advance did not move
// and no envelope was skipped: the page in hand is delivered again when the
// queue has room.
//
// It costs no attempt, and the attempt the pass opened with is put back rather
// than left where the isolation pass raised it: the budget bounds handler
// failures, and a queue an operator has not drained yet is not one. Left
// consumed, a projection blocked for Attempts passes would start calling a
// database that went away permanent and parking over it.
func (this *Projection) stalled(cause error) (step, error) {
	this.attempt = this.opened
	this.streak++
	this.transition(PhaseBlocked, this.attempt, cause)
	return waitBackoff, nil
}

func (this *Projection) landed(progress event.Progress) {
	this.applied, this.quarantined = progress.Applied, progress.Quarantined
	this.page, this.matched, this.keys, this.cursor, this.attempt = nil, nil, nil, "", 0
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
//
// Three tiers, and the middle one is what this last comparison closes. Aligned:
// the checkpoint store's transaction and the destination's are one value, which
// is what InUnit promises and is now measured rather than asserted. Divergent:
// both are transactions and they are two, which passes every other check in this
// package and quietly buys AfterApply atomicity under an InUnit spec — refused
// here, per pass, before the handler, and never downgraded. Unresolvable:
// Unchecked, where no comparison is attempted at all.
//
// A destination whose executor names no comparable identity is refused for the
// same reason the divergent one is: an alignment that cannot be proven is not one
// this framework asserts.
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
	mine, err := event.NewAuthority(this.tracker.Backing(), crud.KeyOf(executor))
	if err != nil {
		return fmt.Errorf("%w: %q names a Destination whose bound transaction carries no identity two transactions can be compared by, so the one authority InUnit promises cannot be shown: %w", ErrSpec, this.spec.Name, err)
	}
	if !authority.Same(mine) {
		return fmt.Errorf("%w: %q asked for the advance inside the caller's unit and that unit bound one transaction for the checkpoint store and another for Destination, so the handler's writes and the advance are two commits and a crash between them leaves one of the two", ErrSpec, this.spec.Name)
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
