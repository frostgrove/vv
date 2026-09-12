package projection

import (
	"context"
	"errors"
	"fmt"

	"github.com/frostgrove/vv/event"
)

// What an effect handler is handed, and the value that IS the capability: a
// projection Handler has no route to one. The envelopes are the ones this
// delivery APPLIED, past the barrier, and no others — a parked envelope is not
// among them and is not lost either: its letter carries its effect, and the
// redrive that applies it is what stages it. An evicted letter's effect is never
// staged.
//
// Envelopes is never empty. A delivery with nothing to stage does not call Stage
// at all, not even with an empty slice, which is what makes a suppression free
// rather than a round trip that is told to do nothing.
//
// The hand-off rule is the Batch's: the slice, its capacity and every payload in
// it are yours from the moment they are handed over.
type Effect struct {
	Identity  Identity
	Envelopes []event.Envelope
	Attempt   int
}

// Stage, not Send, and not Dispatch: those name where this does not run. It is
// called INSIDE the transaction that commits the advance, so what it does must
// roll back with it — a staged job ([[D-118]]), a row in your own tables.
//
// An externally visible irreversible action here — an HTTP call, a payment, a
// mail, a non-transactional publish — is sent again on every rollback the
// delivery can take, and there are four: a lost fence ([[D-133]]), which is the
// expected outcome of a rolling deploy rather than an exotic one; a full park; a
// later envelope's permanent failure; and a serialisation failure the caller's
// own Unit answers. The dial-out belongs to whatever drains the stage, with its
// own retry and its own idempotency.
//
// This is a contract and not a sandbox. Nothing here can stop a handler dialling
// out; what the shape does is make the honest thing the easy thing and the
// dishonest thing visible in a review.
type Effects interface {
	Stage(ctx context.Context, effect Effect) error
}

type EffectsFunc func(ctx context.Context, effect Effect) error

func (this EffectsFunc) Stage(ctx context.Context, effect Effect) error { return this(ctx, effect) }

// The three suppressors and the stage, in the one order they are checked and in
// the one place both callers reach for: the loop's pass and the operator's
// redrive stage through this, so the order cannot hold in one of them and not in
// the other.
//
// Cheapest first, and a rebuild pays the difference on every page. A nil Effects
// costs nothing at all and is the rebuild's own answer — there is nothing to
// withhold because there is nothing to hold. A delivery with no applied envelope
// past the barrier costs a walk over the page already in hand and still makes no
// round trip. Only what is left reaches the ownership row, which is the one
// round trip of the three.
//
// A suppression is never an error and never a phase. It is the normal state of
// every rebuild and of every retired generation, and a State field reporting "I
// did not stage" would be published continuously by healthy projections.
type gate struct {
	effects     Effects
	after       event.Position
	generations Generations
	of          Identity
}

// The envelopes an effect is owed for: the ones this delivery applied whose
// position is past the barrier. ONE side of that comparison is durable — the
// envelope's position, which is where the resumed checkpoint left this
// generation — and the other is the spec's own constant, held wherever the
// deployment holds its configuration. That is what makes a warm-up interrupted
// at M below the barrier resume SUPPRESSED over what is left of it rather than
// firing from the beginning, and it is also the whole of what a restart under a
// LOWER barrier costs: nothing records the number a generation was warmed up
// under, so a release that drops the field stages the rest of the warm-up and no
// code refuses it. Spec.EffectsAfter is where that is written down.
//
// The split is per envelope and never per page: a page straddling the barrier
// would otherwise double-fire over history the prior generation covered, or
// lose the first effects past it.
func (this gate) past(applied []event.Envelope) []event.Envelope {
	if absent(this.effects) {
		return nil
	}
	owed := make([]event.Envelope, 0, len(applied))
	for _, envelope := range applied {
		if envelope.Position > this.after {
			owed = append(owed, envelope)
		}
	}
	return owed
}

// The third suppressor and the stage, both inside whatever unit of work the
// caller is already in: the ownership row is read through the ambient
// transaction that commits the advance, so the row this delivery staged under is
// the row its unit commits under — WHEN THE IMPLEMENTATION'S READ IS THE LOCKING
// ONE Generations asks for. That obligation is the boundary, not this call:
// [[D-126]] leaves the isolation level to the caller, and over a plain read at
// READ COMMITTED a cutover that commits between this read and this commit
// conflicts with nothing, so two generations do both believe they own the live
// effects and neither unit rolls back.
//
// What the boundary is not, under either read: a promise that an envelope the
// retiring generation staged is one the arriving generation will not stage when
// it reaches it. The retiring generation advances past the barrier the cutover
// was observed at, and that overlap is Marten's accepted window — an operator
// closes it by draining the retiring generation before the switch, and nothing
// here closes it for them.
//
// It is gated on the capability being SUPPLIED and never on this projection's
// own generation. A projection at Ungenerated reads a row that answers
// Ungenerated, which is its own generation, so it stages exactly as it did
// before the field arrived; after a cutover from Ungenerated the row answers
// another number and it stops, inside the transaction that commits its own
// advance. Gated on the generation instead, the suppressor never runs for any
// projection that exists today and the first cutover of every live deployment
// has two senders.
//
// The two failures are told apart by their type rather than by where they were
// raised, because the answer to one of them travels through a settlement that
// carries no tally.
func (this gate) stage(ctx context.Context, applied []event.Envelope, attempt int) error {
	owed := this.past(applied)
	if len(owed) == 0 {
		return nil
	}
	if !absent(this.generations) {
		active, err := this.generations.Active(ctx, this.of.Projection())
		if err != nil {
			return &unowned{err: fmt.Errorf("the ownership row of %q could not be read, and which generation owns the live effects is what decides whether this delivery stages one: %w", this.of.Projection(), err)}
		}
		if active != this.of.Generation() {
			return nil
		}
	}
	if err := this.effects.Stage(ctx, Effect{Identity: this.of, Envelopes: copyOf(owed), Attempt: attempt}); err != nil {
		return &unstaged{err: fmt.Errorf("the Effects of %q refused to stage the effect of %d applied envelopes: %w", this.of, len(owed), err)}
	}
	return nil
}

// A read of the ownership row that failed, which is a read of the application's
// own table: a cutover row that cannot be read is not a reason to stop applying
// events, it is a reason not to stage. The delivery postpones and costs no
// attempt, the way every other store failure does.
type unowned struct{ err error }

func (this *unowned) Error() string { return this.err.Error() }

func (this *unowned) Unwrap() error { return this.err }

func asUnowned(err error) bool {
	var found *unowned
	return errors.As(err, &found)
}

// A stage that failed, and it is deliberately NOT routed to the park: parking an
// envelope because the STAGING failed would record a read-model hole that does
// not exist. Retryable is the ordinary backoff with an attempt consumed;
// permanent halts, naming the sink.
type unstaged struct{ err error }

func (this *unstaged) Error() string { return this.err.Error() }

func (this *unstaged) Unwrap() error { return this.err }

func asUnstaged(err error) bool {
	var found *unstaged
	return errors.As(err, &found)
}

// The refusal both doors make, because an ownership row is about the capability
// rather than about the loop that carries it: a redrive stages through the same
// suppressors a pass does, so it refuses the same wiring. It is collected rather
// than returned on its own, like every other refusal either door makes.
//
// The BARRIER is where the two doors part, and they part because the queue is
// what a letter comes from: the loop refuses a barrier beside ParkSequence, so
// every letter a redrive can ever drain was parked by a generation that had
// none and is owed its effect. Spec.EffectsAfter is therefore a field the loop
// accepts and NewRedrive refuses outright.
func unstageable(effects Effects, generations Generations, generation Generation) []error {
	if !absent(effects) && generation != Ungenerated && absent(generations) {
		return []error{fmt.Errorf("%w: Effects is supplied at generation %d and Generations names no ownership row, and a generation is exactly what admits two senders — the retiring generation would go on staging beside the arriving one for ever, with no error on any path", ErrSpec, generation)}
	}
	return nil
}
