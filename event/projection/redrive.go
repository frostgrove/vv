package projection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/event"
)

var errLetterRanTwice = errors.New("projection: this unit of work ran the work it was given more than once, and one redrive of one letter is one apply and one eviction: a unit that retries its transaction answers the failure instead")

// A grant over one sequence of one park, minted by the implementation and opaque
// here: this package compares nothing in a Claim and only hands it back.
//
// It is total — the identity and the sequence are in it — so a call cannot name
// one sequence while holding another's grant. Until is the deadline the
// implementation granted, published so an operator can size a unit against it;
// the framework reads no clock and enforces no expiry. [[D-126]] rules out a
// store's clock and a fence, not this one: the park is the application's table
// and the duration is the application's.
type Claim struct {
	Of       Identity
	Sequence string
	Token    string
	Until    time.Time
}

// The operator's half of the park: what a redrive reads, applies through and
// removes. A claim is what makes it exclusive, and an exclusion that is only a
// read is no exclusion at all — two operators, or one operator and the
// runtime.Runner a host wraps Redrive.Any in, take the same least-recently-tried
// sequence, load the same letters, and apply from the first, so the third letter
// lands before the second one finished. That is the queue's own ordering
// guarantee broken by the queue's own recovery path.
//
// Claim(ctx, of, "") takes the least recently tried unclaimed sequence and
// Claim(ctx, of, "A") takes that one if it is unclaimed. Both answer found=false
// rather than an error when there is nothing to take, because an operator
// draining an empty queue has not failed.
//
// A GRANT EXPIRES, SO IT TRAVELS ON EVERY WRITE IT AUTHORISES. A call whose
// claim no longer owns the sequence is refused with an error wrapping
// ErrClaimLost and is never applied — Evict runs inside the same unit as the
// apply, so a refusal there rolls the apply back with it, which is why the token
// has to be on the write and not only on the read.
//
// The relationship an operator owns: the claim duration must exceed the longest
// unit a redrive may take. A duration shorter than one letter's apply turns
// every redrive into a sequence of lost claims and drains nothing.
type Redriver interface {
	Claim(ctx context.Context, of Identity, sequence string) (Claim, bool, error)
	Sequence(ctx context.Context, claim Claim) ([]Letter, error)
	Evict(ctx context.Context, claim Claim, letter Letter) error
	Touch(ctx context.Context, claim Claim, cause error) error
	Release(ctx context.Context, claim Claim) error
}

// What one call of a redrive did. Cause is the letter's own new failure and
// travels beside a nil error: the redrive did not fail, the letter did. Left is
// what the sequence still holds when the call returns.
type Retried struct {
	Sequence string
	Applied  int
	Left     int
	Cause    error
}

// A redrive is generation-wide and never per partition: a park is keyed by
// Identity.Whole(), and an operator drains "orders@2" rather than having to know
// which partition a poison order's key hashed into.
//
// It touches no checkpoint. The fence admits one writer and the loop owns it, so
// the redrive's evidence is the queue and the checkpoint stays the record of the
// scan. Nothing here starts a goroutine either — the call runs on the operator's
// own, and a host that wants a schedule wraps Any in a runtime.Runner.
//
// There is no Classifier here and the omission is deliberate: a letter that
// fails again is requeued with its new cause whichever class it is in, because
// the alternative — giving up on it — removes a letter without applying it, and
// that is an operator's act through Evict and never a policy's. What decides
// when to stop trying is Letter.Attempt against the application's own table,
// which is where the queue lives.
type RedriveSpec struct {
	Identity  Identity
	Handler   Handler
	Sequencer Sequencer

	// The queue, and the same value the projection's Spec.Park names when one
	// implementation serves both halves.
	Park Redriver

	// One unit per letter, and it is the caller's: crud.InNewTx(ctx, source,
	// work) is the one-line spelling. The handler's write and the eviction commit
	// together or a crash between them leaves the letter parked and nothing
	// applied, which is the outcome the next redrive can start from.
	Unit func(ctx context.Context, work func(context.Context) error) error

	// Required, and Unchecked is refused: a redrive with the read model outside
	// its unit cannot order its own eviction against the loop's blocking test,
	// which is the same reason ParkSequence does not construct there.
	Destination any

	// Two of the loop's three, checked the same way and for the same reason: an
	// effect belongs to an applied envelope rather than to a page, so a letter
	// carries its effect into the park and the redrive that applies it is what
	// stages it. The stage rides the same unit as the apply and the eviction, so
	// a crash anywhere between them leaves the letter parked, nothing applied and
	// nothing staged.
	//
	// An eviction stages nothing, ever. A skip is an operator saying the event
	// will never be applied, and an effect for an event nothing applied is the
	// thing the capability exists to prevent.
	//
	// The third is here to be REFUSED rather than absent, because the field a
	// composition root copies from a Spec into a RedriveSpec is a field it can
	// only get wrong: a barrier over a queue suppresses an effect that is owed
	// and nothing will ever offer it again. NewRedrive says so at the call site;
	// a missing field would say nothing at all.
	Effects      Effects
	EffectsAfter event.Position
	Generations  Generations
}

type Redrive struct{ spec RedriveSpec }

// Performs no I/O and starts nothing. Every refusal wraps ErrSpec or
// ErrTopology, names the field it is about, and they are collected.
func NewRedrive(spec RedriveSpec) (*Redrive, error) {
	var problems []error
	if spec.Identity.Projection() == "" {
		problems = append(problems, fmt.Errorf("%w: Identity is the zero value, which NewIdentity never answers — name the projection and its generation through NewIdentity", ErrSpec))
	}
	if !spec.Identity.Partition().Whole() {
		problems = append(problems, fmt.Errorf("%w: Identity names the partition %s and a park is keyed by the projection and its generation with the partition dropped, so a redrive is generation-wide and there is no per-partition queue to drain",
			ErrTopology, spec.Identity.Partition()))
	}
	if absent(spec.Handler) {
		problems = append(problems, fmt.Errorf("%w: Handler names nothing to apply a letter to", ErrSpec))
	}
	if absent(spec.Park) {
		problems = append(problems, fmt.Errorf("%w: Park names no queue, and a redrive reads its letters from one", ErrSpec))
	}
	if spec.Unit == nil {
		problems = append(problems, fmt.Errorf("%w: Unit names no unit of work, and a letter's apply and its eviction issued outside one are two commits", ErrSpec))
	}
	problems = append(problems, refusedDestination(spec)...)
	problems = append(problems, refusedBarrier(spec)...)
	problems = append(problems, unstageable(spec.Effects, spec.Generations, spec.Identity.Generation())...)
	if spec.Sequencer != nil {
		if broken := unusable(spec.Sequencer); broken != "" {
			problems = append(problems, fmt.Errorf("%w: %s", ErrSpec, broken))
		}
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	if spec.Sequencer == nil {
		spec.Sequencer = ByStream()
	}
	return &Redrive{spec: spec}, nil
}

// The refusal that is true of EVERY redrive rather than of a wiring an operator
// might avoid, which is why it is unconditional. A letter is in the queue
// because a loop parked it; a loop parks only under OnPermanentFailure:
// ParkSequence; and New refuses that beside EffectsAfter. So a generation that
// has letters is a generation that had no barrier, every letter it left is owed
// its effect, and the one thing a barrier can do on this side is suppress that
// effect for ever — the loop advanced over the position and will never read it
// again. The loop's symmetric wiring is refused; this one has to be.
func refusedBarrier(spec RedriveSpec) []error {
	if spec.EffectsAfter == 0 {
		return nil
	}
	return []error{fmt.Errorf("%w: EffectsAfter names a barrier on a redrive, and a redrive drains letters a generation with no barrier parked — every one of them is owed its effect, and suppressing it here loses it for ever because the loop advanced over that position and will never read it again. A spec builder that serves a loop and a redrive leaves this field at zero on the redrive", ErrSpec)}
}

func refusedDestination(spec RedriveSpec) []error {
	if spec.Destination == nil {
		return []error{fmt.Errorf("%w: Destination is unstated — name the handle the handler writes through, and a redrive's letters and its evictions ride in the transaction bound for it", ErrSpec)}
	}
	if _, unresolvable := spec.Destination.(unchecked); unresolvable {
		return []error{fmt.Errorf("%w: Destination is Unchecked, and a redrive whose read model is outside its own unit cannot order its eviction against the loop's blocking test — what a destination this framework cannot resolve gets is Halt", ErrSpec)}
	}
	return nil
}

// The named sequence if the park still has it and nobody else holds it. A
// sequence that is not parked answers Retried{Applied: 0} and no error: an
// operator draining a queue that is already empty has not failed.
func (this *Redrive) Sequence(ctx context.Context, sequence string) (Retried, error) {
	return this.claimed(ctx, sequence)
}

// The least recently tried unclaimed sequence, which is the rotation: a loop
// that always picks the same failing sequence starves every other one, and one
// that picks at random loses the fairness. The order lives in the application's
// table, where its clock does.
func (this *Redrive) Any(ctx context.Context) (Retried, error) {
	return this.claimed(ctx, "")
}

// Claim, drain, release — and the release runs on every exit path including a
// panic, because a grant nobody released stands until the application's clock
// expires it and the sequence is undrainable until it does.
//
// The one exit that releases nothing is a lost claim: it owns neither the
// sequence nor the grant any more, and releasing would free the sequence out
// from under whoever took it. THE SUPPRESSION IS THIS PACKAGE'S AND NOT THE
// IMPLEMENTATION'S — nothing in the Redriver contract requires Release to
// compare the token, and an idempotent "delete the row for this sequence" is a
// reasonable spelling of it, against which the call below is the only thing
// standing between a loser and freeing the winner's sequence mid-drain.
func (this *Redrive) claimed(ctx context.Context, sequence string) (held Retried, err error) {
	claim, found, err := this.spec.Park.Claim(ctx, this.spec.Identity, sequence)
	if err != nil {
		return Retried{Sequence: sequence}, fmt.Errorf("the park refused a claim over %q: %w", this.spec.Identity, err)
	}
	if !found {
		return Retried{Sequence: sequence}, nil
	}
	lost := false
	defer func() {
		if lost {
			return
		}
		if released := this.spec.Park.Release(ctx, claim); released != nil && err == nil {
			err = fmt.Errorf("the sequence %q was drained and its claim could not be released, so it stands until the application's clock expires it: %w", claim.Sequence, released)
		}
	}()
	held, err = this.drain(ctx, claim)
	lost = errors.Is(err, ErrClaimLost)
	return held, err
}

// One unit per letter, in insert order, stopping at the first letter that fails
// again: a sequence is a queue and not a set, so the letters behind the blocker
// stay where they are. On a repeat failure the letter is requeued with its new
// cause in a unit of its own and the call answers a Retried carrying it beside a
// NIL error — the redrive did not fail, the letter did.
//
// Every letter is checked against the spec's sequencer before the first handler
// call rather than as it is reached, because a queue built under another key is
// a refusal about the whole drain and not about the letter it was noticed on.
func (this *Redrive) drain(ctx context.Context, claim Claim) (Retried, error) {
	letters, err := this.spec.Park.Sequence(ctx, claim)
	if err != nil {
		return Retried{Sequence: claim.Sequence}, fmt.Errorf("the park refused the letters of %q: %w", claim.Sequence, err)
	}
	if refusal := this.sequenced(letters); refusal != nil {
		return Retried{Sequence: claim.Sequence, Left: len(letters)}, refusal
	}
	applied := 0
	for index, letter := range letters {
		failed, refused := this.letter(ctx, claim, letter)
		switch {
		case refused != nil:
			return Retried{Sequence: claim.Sequence, Applied: applied, Left: len(letters) - applied}, refused
		case failed != nil:
			return this.requeued(ctx, claim, letter, failed, applied, len(letters)-index)
		}
		applied++
	}
	return Retried{Sequence: claim.Sequence, Applied: applied}, nil
}

func (this *Redrive) sequenced(letters []Letter) error {
	named := this.spec.Sequencer.Name()
	for _, letter := range letters {
		if letter.Sequencer == named {
			continue
		}
		return fmt.Errorf("%w: the letters of %q were parked under the sequencer %q and this redrive names %q, so the order this letter's place encodes is not the order %q would give it — a key changes with a new generation and never under a queue that is already holding letters",
			ErrTopology, letter.Sequence, letter.Sequencer, named, named)
	}
	return nil
}

// One letter, one unit, and inside it the two halves the loop makes too: the
// destination is resolved and required to be a transaction before the handler
// runs, and the eviction rides the same transaction as the write it accounts
// for. A crash anywhere between them leaves the letter parked and nothing
// applied, so the next redrive starts this sequence again from here.
//
// The tier check the pass makes is half of one here, deliberately: a RedriveSpec
// carries no Checkpoints, because no redrive path issues a save, so there is no
// second authority to compare the destination's against. What is left is the
// half a redrive can make, and the other half is falsified by consequence — a
// redrive over a second pool leaves the letter parked and nothing applied.
func (this *Redrive) letter(ctx context.Context, claim Claim, held Letter) (failed, refused error) {
	var refusal, applied, staged, evicted error
	var ran bool
	answered := this.spec.Unit(ctx, func(inner context.Context) error {
		if ran {
			return errLetterRanTwice
		}
		ran = true
		if refusal = this.checkUnit(inner); refusal != nil {
			return refusal
		}
		if applied = this.apply(inner, held); applied != nil {
			return applied
		}
		if staged = this.staging(inner, held); staged != nil {
			return staged
		}
		evicted = this.spec.Park.Evict(inner, claim, held)
		return evicted
	})
	switch {
	case refusal != nil:
		return nil, refusal
	case staged != nil:
		return nil, fmt.Errorf("the letter %q of %q was applied and its effect could not be staged, so its unit rolled back and nothing was applied: %w", held.Sequence, held.Identity, staged)
	case evicted != nil:
		return nil, fmt.Errorf("the letter %q of %q was applied and could not be evicted, so its unit rolled back and nothing was applied: %w", held.Sequence, held.Identity, evicted)
	case applied != nil:
		return applied, nil
	case answered != nil:
		return nil, fmt.Errorf("the unit carrying the letter %q of %q did not commit, so nothing was applied and nothing was evicted: %w", held.Sequence, held.Identity, answered)
	case !ran:
		return nil, fmt.Errorf("%w: the letter %q of %q was not redriven: %w", ErrSpec, held.Sequence, held.Identity, errUnitRanNothing)
	}
	return nil, nil
}

// The same gate the loop has, over the one envelope this letter carries: a
// redrive is where the effect of a parked envelope is staged, and the identity
// it is staged under is the redrive's own whole one — a park is keyed by the
// projection and its generation, so which partition the key once hashed into is
// not a thing a drain knows or needs.
func (this *Redrive) staging(ctx context.Context, held Letter) error {
	return gate{
		effects:     this.spec.Effects,
		after:       this.spec.EffectsAfter,
		generations: this.spec.Generations,
		of:          this.spec.Identity,
	}.stage(ctx, []event.Envelope{held.Envelope}, held.Attempt+1)
}

func (this *Redrive) apply(ctx context.Context, held Letter) error {
	return this.spec.Handler.Apply(ctx, Batch{
		Projection: this.spec.Identity.Projection(),
		Identity:   this.spec.Identity,
		Envelopes:  copyOf([]event.Envelope{held.Envelope}),
		Attempt:    held.Attempt + 1,
	})
}

// The requeue is a unit of its own, because the one the apply ran in rolled back
// and the letter's new cause is a write that has to survive that rollback: it is
// what the rotation orders by, so a failure that left no mark would have this
// sequence picked first again on the very next call.
func (this *Redrive) requeued(ctx context.Context, claim Claim, held Letter, failure error, applied, left int) (Retried, error) {
	answered := Retried{Sequence: claim.Sequence, Applied: applied, Left: left, Cause: failure}
	if err := this.spec.Park.Touch(ctx, claim, failure); err != nil {
		if errors.Is(err, ErrClaimLost) {
			return Retried{Sequence: claim.Sequence, Applied: applied, Left: left}, err
		}
		return answered, fmt.Errorf("the letter %q of %q failed again and could not be requeued with its new cause, so the rotation will pick this sequence first again: %w", held.Sequence, held.Identity, err)
	}
	return answered, nil
}

func (this *Redrive) checkUnit(ctx context.Context) error {
	executor, bound := crud.ExecutorFor(ctx, this.spec.Destination)
	if !bound {
		return fmt.Errorf("%w: %q names a Destination the unit bound no executor for, so the handler's writes are outside the transaction the eviction rides in", ErrSpec, this.spec.Identity)
	}
	if !crud.IsTransaction(executor) {
		return fmt.Errorf("%w: %q names a Destination the unit bound an executor for and it is not a transaction, so the handler's writes are outside the transaction the eviction rides in", ErrSpec, this.spec.Identity)
	}
	return nil
}
