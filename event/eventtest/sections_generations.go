package eventtest

import (
	"context"
	"errors"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/projection"
)

func generationsInventory() []section[*generations] {
	return []section[*generations]{
		{name: "ungenerated", run: ungeneratedSection},
		{name: "activation", run: activationSection},
		{name: "fenced activation", run: fencedActivationSection},
		{name: "unit of work", run: generationsUnitSection},
		{name: "locking read", needs: needsALockingRead, run: lockingReadSection},
	}
}

func needsALockingRead(this *generations) string {
	if this.factory.Closes != LockingRead {
		return "this factory states that what closes the window is " + this.factory.Closes.String() +
			", and the abort that closes it there comes from the cutover reading the checkpoint rows the staging unit writes — a harness holding two methods and no checkpoints writes none of them, so what this section can measure is the locking read alone"
	}
	return ""
}

// A projection with no row is the state of every deployment that has never cut
// over, and an error there stops a live projection over a row it never needed.
func ungeneratedSection(this *generations) {
	ctx := this.context()
	held := this.rows()
	name := this.named("none")

	inside, tx := this.begin(ctx, held)
	if found := this.active(inside, held, name); found != projection.Ungenerated {
		this.refuse("a projection that has never cut over answered generation %d, where a row nobody wrote is Ungenerated and a nil error", found)
	}
	this.rollback(inside, tx)
}

func activationSection(this *generations) {
	ctx := this.context()
	held := this.rows()
	name := this.named("move")

	this.standing(ctx, held, name, 1)
	if found := this.reads(ctx, held, name); found != 1 {
		this.refuse("the ownership row of a projection stood up at generation 1 reads %d, so the switch of the read target is not the one write this cutover was", found)
	}

	inside, tx := this.begin(ctx, held)
	this.activate(inside, held, name, 1, 2)
	this.commit(inside, tx)
	if found := this.reads(ctx, held, name); found != 2 {
		this.refuse("the ownership row was moved from 1 to 2 and reads %d, so a read path resolving through it reads the retiring generation's tables", found)
	}
}

// Activate is fenced and is one statement. A read followed by a write is the one
// spelling it may not have: two operators would both read `from`, both find it,
// and both write — and the refusal one of them is owed never arrives.
func fencedActivationSection(this *generations) {
	ctx := this.context()
	held := this.rows()
	name := this.named("fence")
	this.standing(ctx, held, name, 1)

	inside, tx := this.begin(ctx, held)
	err := held.Activate(inside, name, 3, 4)
	this.rollback(inside, tx)
	if err == nil {
		this.refuse("a cutover from generation 3 was admitted over a row that holds 1, so this implementation reads the row and then writes it rather than moving it with one fenced statement")
	}
	if !errors.Is(err, event.ErrConflict) {
		this.refuse("a cutover from a generation the row does not hold answered %v, and what a second operator reads must be an error wrapping event.ErrConflict rather than one it cannot tell from a broken connection", err)
	}
	if found := this.reads(ctx, held, name); found != 1 {
		this.refuse("a refused cutover left the ownership row at %d where it held 1", found)
	}

	this.contended(ctx, held)
}

// Two operators cutting over at once, sequenced so that the second is issued
// while the first is still open: one winner and one refusal, whichever way the
// implementation makes the second wait.
func (this *generations) contended(ctx context.Context, held projection.Generations) {
	name := this.named("both")
	this.standing(ctx, held, name, 1)

	first, winner := this.begin(ctx, held)
	second, loser := this.begin(ctx, held)
	this.activate(first, held, name, 1, 2)

	entered, answered := make(chan struct{}), make(chan error, 1)
	go func() {
		close(entered)
		answered <- held.Activate(second, name, 1, 3)
	}()
	<-entered

	var err error
	select {
	case err = <-answered:
	case <-time.After(waiting):
		this.commit(first, winner)
		err = <-answered
	}
	if err == nil {
		this.refuse("two units both moved the ownership row of %q from generation 1 and both were told they had, so two operators cutting over at once leave two winners and the read target is whichever committed last", name)
	}
	this.rollback(second, loser)
}

// Both methods run inside the caller's unit, and what that buys is exact: the
// arriving generation's rows, the evidence read off them and this row move
// together or none of them does. An implementation answering from a handle of
// its own is invisible to the framework — it holds a method set and no resource,
// so it cannot ask this value which pool it opened.
func generationsUnitSection(this *generations) {
	ctx := this.context()
	held := this.rows()
	name := this.named("unit")
	this.standing(ctx, held, name, 1)

	inside, tx := this.begin(ctx, held)
	this.activate(inside, held, name, 1, 2)
	if found := this.active(inside, held, name); found != 2 {
		this.refuse("a read inside the unit that moved the ownership row answered %d rather than the 2 that unit had just written, so at least one of the two runs on a connection that is not the caller's and a cutover is not atomic with the evidence it was derived from", found)
	}
	this.rollback(inside, tx)

	if found := this.reads(ctx, held, name); found != 1 {
		this.refuse("the unit that moved the ownership row rolled back and the row reads %d, so the write was issued outside the caller's unit and the read target moved for a cutover that never committed", found)
	}
}

// THE OBLIGATION THIS HARNESS EXISTS FOR. Active is the effect gate's read, and
// under a plain read at READ COMMITTED a cutover that commits between that read
// and the unit's commit conflicts with nothing: both commit, and a generation
// the row no longer names stages its effect anyway. Under a locking read the
// UPDATE waits behind every unit that read the row, so the generation that
// staged is the generation that owned the row for the whole of its unit.
//
// What is measured is the waiting, because that is what the obligation is. The
// framework cannot ask a method set what it locked, and neither can this.
func lockingReadSection(this *generations) {
	ctx := this.context()
	held := this.rows()
	name := this.named("lock")
	this.standing(ctx, held, name, 1)

	reader, staging := this.begin(ctx, held)
	if found := this.active(reader, held, name); found != 1 {
		this.refuse("the unit that is about to stage read generation %d off a row standing at 1", found)
	}

	writer, cutting := this.begin(ctx, held)
	entered, answered := make(chan struct{}), make(chan error, 1)
	go func() {
		close(entered)
		answered <- held.Activate(writer, name, 1, 2)
	}()
	<-entered

	select {
	case err := <-answered:
		this.rollback(reader, staging)
		if err != nil {
			this.refuse("a cutover issued while a unit held the ownership row answered %v, and what it is owed is the wait", err)
		}
		this.refuse("a cutover moved the ownership row of %q while the unit that had read it was still open, so this read takes no lock: the retiring generation commits the effect it staged under a row that no longer names it, and no isolation level this contract leaves to a caller closes that",
			name)
	case <-time.After(waiting):
	}

	this.rollback(reader, staging)
	if err := <-answered; err != nil {
		this.refuse("the cutover answered %v once the unit that had read the row ended, where what was waiting for that unit was a row lock", err)
	}
	this.commit(writer, cutting)
	if found := this.reads(ctx, held, name); found != 2 {
		this.refuse("the cutover committed and the ownership row reads %d", found)
	}
}
