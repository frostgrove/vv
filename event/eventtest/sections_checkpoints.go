package eventtest

import (
	"context"
	"time"

	"github.com/frostgrove/vv/event"
)

func checkpointInventory() []section[*checkpoints] {
	return []section[*checkpoints]{
		{name: "binding", run: checkpointBindingSection},
		{name: "absence", run: absenceSection},
		{name: "round trip", run: roundTripSection},
		{name: "fence", run: fenceSection},
		{name: "forget", run: forgetSection},
		{name: "names", run: namesSection},
		{name: "bounds", run: cursorBoundsSection},
		{name: "refusal classes", run: checkpointRefusalClassesSection},
		{name: "lifecycle", run: checkpointLifecycleSection},
		{name: "concurrency", run: checkpointConcurrencySection},
		{name: "transactions", needs: needsCheckpointTransactions, run: checkpointTransactionsSection},
		{name: "durability", needs: needsCheckpointPersistence, run: checkpointDurabilitySection},
	}
}

func needsCheckpointTransactions(this *checkpoints) string {
	if this.claims.Transactions != event.Supported {
		return "this checkpoint store does not claim transactions, so no save of it can ride in a caller's unit of work"
	}
	return ""
}

// The gate is on the claim and not on the hook, so a store that keeps nothing
// past its own process is reported here rather than certified for a property it
// does not have. Its second value is still exercised — by every section that
// takes one — and what is not asked here is only whether a row outlives the
// process.
func needsCheckpointPersistence(this *checkpoints) string {
	if this.claims.Persistence != event.Supported {
		return "this checkpoint store does not claim persistence, so nothing of it survives a restart; what a second value over its backing reads is asked by the sections that take one"
	}
	return ""
}

// Capabilities and Backing are constant for a value's life and every section is
// gated on the first reading of them, so a store that derives either from
// whatever is bound answers one thing here and another there.
func checkpointBindingSection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	backing := held.Backing()
	if backing.Equal(event.Backing{}) {
		this.refuse("this checkpoint store names no backing at all, so nothing it minted could ever be told from another deployment's")
	}
	if !held.Backing().Equal(backing) {
		this.refuse("this checkpoint store named one backing and then another, so what a row was written through and what it is read through are two resources")
	}
	if held.Capabilities() != this.claims {
		this.refuse("this checkpoint store claimed %+v and then %+v", this.claims, held.Capabilities())
	}

	authority, err := held.Transaction(ctx)
	if err != nil {
		this.refuse("asking a checkpoint store outside every unit of work which one it is inside answered %v, where the answer is no authority and no error", err)
	}
	if authority.Valid() {
		this.refuse("a checkpoint store outside every unit of work answered a valid authority, so a consumer asking for one before it writes is told it has a transaction nobody opened")
	}
	if fresh := this.load(ctx, held, this.named("a")); !fresh.Fresh() {
		this.refuse("a name nothing ever saved answered advance %d", fresh.Advance)
	}
}

// Absence is total: a row that is half-absent is not a fresh start with some
// extra fields, it is a store that did not answer the question. And presence is
// its opposite — a name that was saved is not fresh, and a store reporting one
// resumes a consumer at the origin of the log against a live read model.
func absenceSection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	name := this.named("a")

	absent := this.load(ctx, held, name)
	switch {
	case !absent.Fresh():
		this.refuse("a name nothing ever saved answered advance %d", absent.Advance)
	case absent.Cursor != "":
		this.refuse("a name nothing ever saved answered a cursor of %d bytes beside no advance", len(absent.Cursor))
	case absent.Projection != "":
		this.refuse("a name nothing ever saved answered the projection %q beside no advance", absent.Projection)
	case absent.Progress != (event.Progress{}):
		this.refuse("a name nothing ever saved answered the progress %+v beside no advance", absent.Progress)
	}

	saved := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 1, Progress: this.progress(7, 7, 0)}
	this.save(ctx, held, saved)
	written := this.load(ctx, held, name)
	if written.Fresh() {
		this.refuse("a name this section had just saved at advance 1 answered the zero checkpoint, so a consumer restarting reads the whole log again against a live read model")
	}
	if written.Projection != name {
		this.refuse("a row saved for %q answered the projection %q", name, written.Projection)
	}
}

// What a checkpoint is for: the cursor and the progress that went in are the
// ones that come back, through a second value and after a second save. A store
// answering the cursor before the last one resumes a consumer over a page it has
// already applied, every time, with no error anywhere.
func roundTripSection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	name := this.named("a")

	first := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 1, Progress: this.progress(11, 11, 1)}
	this.save(ctx, held, first)
	if found := this.load(ctx, held, name); !sameCheckpoint(found, first) {
		this.refuse("a checkpoint saved as %+v was answered as %+v", first, found)
	}

	second := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 2, Progress: this.progress(23, 23, 2)}
	if second.Cursor == first.Cursor {
		this.refuse("the factory answered one cursor to two calls, so nothing below tells a store that wrote the second from one that kept the first")
	}
	this.save(ctx, held, second)
	found := this.load(ctx, held, name)
	if !sameCheckpoint(found, second) {
		this.refuse("a second save left %+v where it wrote %+v", found, second)
	}
	if found.Cursor == first.Cursor {
		this.refuse("a second save left the cursor the first one wrote, so a consumer resumes over the page it applied last")
	}
	if beside := this.beside(held); beside != nil {
		if through := this.load(ctx, beside, name); !sameCheckpoint(through, second) {
			this.refuse("a second value over this backing answered %+v where the first wrote %+v", through, second)
		}
	} else {
		this.unable("this factory builds no second value over this backing, so nothing here was read back through one")
	}
}

// The whole of what makes two replicas of one projection safe: a save lands if
// and only if the stored advance is one below the one presented, or there is no
// row and the presented advance is one. A store that admits any other save lets
// two writers take turns over one name, each overwriting the other's cursor.
func fenceSection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	name := this.named("a")

	ahead := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 2, Progress: this.progress(3, 3, 0)}
	this.classified(held.Save(ctx, ahead), event.Conflict, "a save at advance 2 against a name that holds no row")
	if fresh := this.load(ctx, held, name); !fresh.Fresh() {
		this.refuse("a save the fence refused created the row anyway, at advance %d", fresh.Advance)
	}

	first := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 1, Progress: this.progress(5, 5, 0)}
	this.save(ctx, held, first)

	for _, refused := range []struct {
		what    string
		advance uint64
	}{
		{"a save at the advance the row already holds", 1},
		{"a save two above the advance the row holds", 3},
	} {
		losing := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: refused.advance, Progress: this.progress(9, 9, 0)}
		this.classified(held.Save(ctx, losing), event.Conflict, refused.what)
		this.unchangedRow(ctx, held, name, first, refused.what)
	}

	next := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 2, Progress: this.progress(13, 13, 0)}
	this.save(ctx, held, next)
	if found := this.load(ctx, held, name); !sameCheckpoint(found, next) {
		this.refuse("the save one above the row's own advance left %+v where it wrote %+v, so this fence refuses everything and no consumer of it advances", found, next)
	}
}

// Retiring a projection, and the two things it must not be: a Forget that
// reaches another name's row, and a Forget that is refused for a name nobody
// ever wrote.
func forgetSection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	mine, beside := this.named("a"), this.named("b")

	kept := event.Checkpoint{Projection: beside, Cursor: this.cursor(), Advance: 1, Progress: this.progress(4, 4, 0)}
	this.save(ctx, held, event.Checkpoint{Projection: mine, Cursor: this.cursor(), Advance: 1, Progress: this.progress(2, 2, 0)})
	this.save(ctx, held, kept)

	this.forget(ctx, held, mine)
	if after := this.load(ctx, held, mine); !after.Fresh() {
		this.refuse("a name this section forgot still answers advance %d", after.Advance)
	}
	this.unchangedRow(ctx, held, beside, kept, "forgetting one name")
	this.forget(ctx, held, mine)
	if after := this.load(ctx, held, mine); !after.Fresh() {
		this.refuse("forgetting a name twice left it at advance %d", after.Advance)
	}

	fresh := event.Checkpoint{Projection: mine, Cursor: this.cursor(), Advance: 1, Progress: this.progress(6, 6, 0)}
	this.save(ctx, held, fresh)
	if found := this.load(ctx, held, mine); !sameCheckpoint(found, fresh) {
		this.refuse("a name that was forgotten and saved again answered %+v where the save wrote %+v", found, fresh)
	}
	this.forgetRacingASave(ctx, held, mine, fresh)
}

// Retiring a live projection is the one moment a removal and a save meet, and a
// unit of work is what makes the meeting exact: the removal is held open, a save
// at the next advance is issued against the row it is about to take away, and the
// removal commits while that save is in flight. Whichever of the two landed
// first, the row is gone afterwards — a save above advance 1 moves a row and
// never creates one. A store that asks whether the row is there at one moment and
// writes at another puts back the row the operator retired, at an advance no
// first save ever created, and the projection they cut over from goes on writing
// into the read model.
func (this *checkpoints) forgetRacingASave(ctx context.Context, held event.Checkpoints, name string, row event.Checkpoint) {
	if this.claims.Transactions != event.Supported {
		this.unable("this checkpoint store does not claim transactions, so no removal of it could be held in flight while a save raced it")
		return
	}
	inside, tx := this.begin(ctx, held)
	this.forget(inside, held, name)

	presented := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: row.Advance + 1, Progress: this.progress(79, 79, 0)}
	answers := make(chan error, 1)
	go func() { answers <- held.Save(ctx, presented) }()
	time.Sleep(settling)
	this.commit(ctx, tx)

	if err := <-answers; err != nil && err.Error() != event.Failure(event.Conflict, nil).Error() {
		this.refuse("a save at advance %d racing the removal of %q answered %v, where it either lands and is taken away or is refused a conflict", presented.Advance, name, err)
	}
	if after := this.load(ctx, held, name); !after.Fresh() {
		this.refuse("a removal of %q that committed while a save at advance %d was in flight left the row at advance %d, and a save above advance 1 moves a row rather than creating one — so a projection an operator retired goes on writing into the read model it was cut over from",
			name, presented.Advance, after.Advance)
	}
}

// Long enough for a save issued beside this goroutine to reach the row the
// removal above is holding, and short enough that twelve sections still report
// inside the window every one of them runs under. A store that has not started
// the save by then is asked the same question and owes the same answer; only the
// interleaving is lost.
const settling = 250 * time.Millisecond

// The defect the kernel's own door refuses in front of every consumer, asked of
// the store itself: a Load that ignores its argument, or whose statement is
// mis-parameterised, answers another projection's well-formed cursor — of the
// right log, in the right format, refused by nothing — and the walk resumes
// wherever that one had got to, skipping everything in between, forever.
func namesSection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	first := event.Checkpoint{Projection: this.named("a"), Cursor: this.cursor(), Advance: 1, Progress: this.progress(17, 17, 0)}
	second := event.Checkpoint{Projection: this.named("b"), Cursor: this.cursor(), Advance: 1, Progress: this.progress(29, 29, 0)}
	this.save(ctx, held, first)
	this.save(ctx, held, second)

	for _, written := range []event.Checkpoint{first, second} {
		found := this.load(ctx, held, written.Projection)
		if !sameCheckpoint(found, written) {
			this.refuse("%q was asked for and this checkpoint store answered %+v, where that name's own row is %+v", written.Projection, found, written)
		}
	}
}

// A cursor is opaque bytes the log mints and this store reads nothing in, so the
// ceiling the kernel publishes is what a store owes room for — and the two bytes
// below are why the column that holds one is not text. Below the ceiling the
// bound is just as load-bearing: the empty cursor IS the origin of a log, so a
// row carrying one at a non-zero advance is a readable checkpoint that restarts
// a consumer at the beginning.
func cursorBoundsSection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	name := this.named("a")

	widest := event.Checkpoint{Projection: name, Cursor: cursorOfWidth(this.cursor(), event.MaxCursorBytes), Advance: 1, Progress: this.progress(31, 31, 0)}
	this.save(ctx, held, widest)
	found := this.load(ctx, held, name)
	if !sameCheckpoint(found, widest) {
		this.refuse("a cursor of exactly the %d bytes the kernel publishes as the ceiling was saved and answered back as %d bytes, byte %d first differing",
			event.MaxCursorBytes, len(found.Cursor), firstDifference(found.Cursor, widest.Cursor))
	}

	for _, refused := range []struct {
		what   string
		cursor event.Cursor
	}{
		{"a save of the empty cursor, which is the origin of a log rather than a point to resume past", ""},
		{"a save of a cursor one byte over the ceiling the kernel publishes", cursorOfWidth(this.cursor(), event.MaxCursorBytes+1)},
	} {
		losing := event.Checkpoint{Projection: name, Cursor: refused.cursor, Advance: 2, Progress: this.progress(37, 37, 0)}
		if err := held.Save(ctx, losing); err == nil {
			this.refuse("%s was admitted", refused.what)
		}
		this.unchangedRow(ctx, held, name, widest, refused.what)
	}
}

// A NUL and a 0xff, because a cursor is unconstrained bytes and the kernel's own
// text rule is applied to a key, a family and a type name and to no cursor
// anywhere. Both are a server error in a text column and neither is in a byte
// one, which is the whole of what this width proves beyond the width.
func cursorOfWidth(minted event.Cursor, width int) event.Cursor {
	held := make([]byte, 0, width)
	held = append(held, minted...)
	held = append(held, 0x00, 0xff)
	for len(held) < width {
		held = append(held, byte(len(held)))
	}
	return event.Cursor(held[:width])
}

func firstDifference(first, second event.Cursor) int {
	for index := 0; index < len(first) && index < len(second); index++ {
		if first[index] != second[index] {
			return index
		}
	}
	return min(len(first), len(second))
}

// Every refusal a checkpoint store answers is one of the seven outcomes, and a
// consumer branches on that and never on the text. The two that decide a
// consumer's next move are told apart here: a conflict is another writer or a
// forgotten row and is terminal, and a refusal is this store declining to write
// what it was handed.
func checkpointRefusalClassesSection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	name := this.named("a")
	written := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 1, Progress: this.progress(41, 41, 0)}
	this.save(ctx, held, written)

	this.classified(held.Save(ctx, event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 1}),
		event.Conflict, "a save at the advance the row already holds")
	this.classified(held.Save(ctx, event.Checkpoint{Projection: name, Advance: 2}),
		event.Refused, "a save of the empty cursor")

	cancelled, stop := context.WithCancel(ctx)
	stop()
	for _, door := range []struct {
		what string
		ask  func() error
	}{
		{"a load", func() error { _, err := held.Load(cancelled, name); return err }},
		{"a save", func() error {
			return held.Save(cancelled, event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 2})
		}},
		{"a forget", func() error { return held.Forget(cancelled, name) }},
	} {
		if err := door.ask(); err == nil || err.Error() != context.Canceled.Error() {
			this.refuse("%s under a context the caller cancelled answered %v, which a caller matching a cancellation cannot recognise as one", door.what, err)
		}
	}
	this.unchangedRow(ctx, held, name, written, "three refused calls")
}

func checkpointLifecycleSection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	name := this.named("a")
	written := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 1, Progress: this.progress(43, 43, 0)}
	this.save(ctx, held, written)
	beside := this.beside(held)

	if err := held.Close(); err != nil {
		this.refuse("closing this checkpoint store answered %v", err)
	}
	if err := held.Close(); err != nil {
		this.refuse("closing this checkpoint store a second time answered %v, and a composition root that closes twice is an ordinary shutdown", err)
	}

	_, loading := held.Load(ctx, name)
	this.classified(loading, event.Closed, "a load through a closed checkpoint store")
	this.classified(held.Save(ctx, event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 2}),
		event.Closed, "a save through a closed checkpoint store")
	this.classified(held.Forget(ctx, name), event.Closed, "a forget through a closed checkpoint store")

	if beside == nil {
		this.unable("no second value over this backing read what a close left behind")
		return
	}
	this.unchangedRow(ctx, beside, name, written, "closing the value that wrote the row")
}

// The fence under real contention rather than in sequence: every writer presents
// the same advance and exactly one may land. A store that reads the row and then
// writes it admits as many as the window is wide, and nothing else in this suite
// can see that.
func checkpointConcurrencySection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	name := this.named("a")
	this.save(ctx, held, event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 1, Progress: this.progress(47, 47, 0)})

	const savers = 8
	answers := make(chan error, savers)
	start := make(chan struct{})
	for range savers {
		presented := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 2, Progress: this.progress(53, 53, 0)}
		go func() {
			<-start
			answers <- held.Save(ctx, presented)
		}()
	}
	close(start)
	landed, conflicted := 0, 0
	for range savers {
		switch err := <-answers; {
		case err == nil:
			landed++
		case err.Error() == event.Failure(event.Conflict, nil).Error():
			conflicted++
		default:
			this.refuse("one of %d savers at one advance answered %v, where a loser is refused a conflict", savers, err)
		}
	}
	if landed != 1 || conflicted != savers-1 {
		this.refuse("%d of %d savers at one advance landed and %d were refused a conflict, so two writers advanced one checkpoint and each is applying pages the other's cursor has passed",
			landed, savers, conflicted)
	}
	if found := this.load(ctx, held, name); found.Advance != 2 {
		this.refuse("the row is at advance %d after one winner of %d saves at advance 2", found.Advance, savers)
	}

	alone := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 3, Progress: this.progress(59, 59, 0)}
	this.save(ctx, held, alone)
	if found := this.load(ctx, held, name); !sameCheckpoint(found, alone) {
		this.refuse("one saver alone was refused after the contended round, so this fence refuses everything rather than all but one")
	}
}

// What makes a projection restartable: the save rides in the caller's own unit
// of work, so the rows the handler wrote and the advance that accounts for them
// commit together or neither does. A store that writes beside the unit leaves an
// advance for work that rolled back, and the page it accounts for is never
// applied by anybody.
func checkpointTransactionsSection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	name := this.named("a")
	written := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 1, Progress: this.progress(61, 61, 0)}
	this.save(ctx, held, written)

	inside, tx := this.begin(ctx, held)
	authority, err := held.Transaction(inside)
	if err != nil || !authority.Valid() {
		this.refuse("a checkpoint store inside a unit of work this factory began answered %v and a %v authority, so nothing could ever check that a save of it landed inside one", err, authority.Valid())
	}
	discarded := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 2, Progress: this.progress(67, 67, 0)}
	this.save(inside, held, discarded)
	this.unchangedRow(ctx, held, name, written, "a save staged inside a unit of work nobody has committed")
	this.rollback(ctx, tx)
	this.unchangedRow(ctx, held, name, written, "a save inside a unit of work that rolled back")

	again, second := this.begin(ctx, held)
	kept := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 2, Progress: this.progress(71, 71, 0)}
	this.save(again, held, kept)
	this.commit(ctx, second)
	if found := this.load(ctx, held, name); !sameCheckpoint(found, kept) {
		this.refuse("a save inside a unit of work that committed left %+v where it wrote %+v", found, kept)
	}
	this.forgetsInAUnit(ctx, held, name, kept)
}

// Retiring a projection is a write like any other, so it rides in the caller's
// unit of work like any other: a Forget the caller rolls back leaves the row,
// and one it commits removes it. A store that reached the row either way
// retires a projection nobody retired, or keeps one an operator did. And what
// the unit itself reads back is the removal it staged: absence is total inside a
// unit exactly as it is outside one, so a store that answers the name beside no
// advance has answered half a row, which the kernel's own door refuses.
func (this *checkpoints) forgetsInAUnit(ctx context.Context, held event.Checkpoints, name string, kept event.Checkpoint) {
	discarded, tx := this.begin(ctx, held)
	this.forget(discarded, held, name)
	switch staged := this.load(discarded, held, name); {
	case !staged.Fresh():
		this.refuse("a load inside the unit that staged the removal of %q answered advance %d, so the unit does not read its own work", name, staged.Advance)
	case staged.Projection != "" || staged.Cursor != "" || staged.Progress != (event.Progress{}):
		this.refuse("a load inside the unit that staged the removal of %q answered %+v, and a row that is half-absent is not a fresh start but a store that did not answer the question", name, staged)
	}
	this.rollback(ctx, tx)
	this.unchangedRow(ctx, held, name, kept, "a forget inside a unit of work that rolled back")

	again, second := this.begin(ctx, held)
	this.forget(again, held, name)
	this.commit(ctx, second)
	if after := this.load(ctx, held, name); !after.Fresh() {
		this.refuse("a forget inside a unit of work that committed left the row at advance %d", after.Advance)
	}
}

// What survives the value that wrote it, which is what a restart is.
func checkpointDurabilitySection(this *checkpoints) {
	ctx := this.context()
	held := this.store()
	name := this.named("a")
	written := event.Checkpoint{Projection: name, Cursor: this.cursor(), Advance: 1, Progress: this.progress(73, 73, 3)}
	this.save(ctx, held, written)

	restarted := this.beside(held)
	if restarted == nil {
		this.unable("this factory builds no second value over the backing the first one wrote to, so nothing here outlived the value that wrote it")
		return
	}
	if !restarted.Backing().Equal(held.Backing()) {
		this.unable("the second value this factory built names another backing, so nothing here was a restart")
		return
	}
	this.unchangedRow(ctx, restarted, name, written, "reading through a value built after the one that wrote")
}
