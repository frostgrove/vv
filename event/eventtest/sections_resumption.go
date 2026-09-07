package eventtest

import (
	"context"
	"errors"
	"fmt"

	"github.com/frostgrove/vv/event"
)

func resumptionSection(this *probe) {
	ctx := this.context()
	store, repo, held := this.open()
	from := this.tail(ctx, store)
	written := this.spread(ctx, repo, held, "a", "b")

	reader, err := event.Read(store, from)
	if err != nil {
		this.refuse("reading through this store answered %v", err)
	}
	if _, err := reader.Next(ctx); err != nil {
		this.refuse("the first page of a walk answered %v", err)
	}
	seen := this.mine(reader.Events(), held)
	persisted := reader.Cursor()
	if persisted == "" {
		this.refuse("a page this store answered came with no cursor, so nothing about this walk can be persisted")
	}

	late, more := this.lateWriter(ctx, store, held)
	written += more
	want := written - len(seen)
	tail := this.from(ctx, store, walking{from: persisted, held: held, want: want}, "a walk resumed from a persisted cursor")
	seen = append(seen, tail...)
	if beside := this.beside(store, held); beside != nil {
		restarted := this.from(ctx, beside, walking{from: persisted, held: held, want: want}, "a walk resumed through a second store value over this backing, which is what a restart is")
		if !sameEvents(restarted, tail) {
			this.refuse("a cursor persisted through one store value returned %d events when it was resumed through another over the same backing, where the value that minted it returned %d",
				len(restarted), len(tail))
		}
	} else {
		this.unable("no second store value over this backing resumed the walk, so nothing here was a restart")
	}
	this.foreignCursor(ctx, store, persisted)
	this.unparsableCursor(ctx, store)
	whole := this.walkLog(ctx, store, walking{from: from, held: held, want: written})
	if !sameEvents(seen, whole) {
		this.refuse("a walk interrupted at a persisted cursor returned %d of this run's events where one uninterrupted walk returned %d, so the cursor this store answered was not a resume point",
			len(seen), len(whole))
	}
	for _, name := range late {
		if !contains(seen, name) {
			this.refuse("%s was committed after the cursor was persisted and the walk that resumed from it never returned that event", name)
		}
	}
}

// A cursor is minted over a backing and means nothing away from it. Presented to
// another, it is a wiring refusal rather than a page: two values the framework
// minted apart, put together by a hand, and the alternative is a projector that
// silently resumes somebody else's log at whatever position the number happens
// to name. The second store is asked of the factory here rather than at the top
// of the section because only a factory that builds two backings has an
// elsewhere at all, and which kind this one is is what Backing().Equal answers.
func (this *probe) foreignCursor(ctx context.Context, store event.Store, minted event.Cursor) {
	elsewhere := this.store()
	if elsewhere.Backing().Equal(store.Backing()) {
		this.unable("this factory builds no store over a second backing, so no cursor was presented to one it was not minted over")
		return
	}
	reader, err := event.Read(elsewhere, minted)
	if err != nil {
		this.refuse("reading through a second store answered %v", err)
	}
	if _, err := reader.Next(ctx); !errors.Is(err, event.ErrCursor) {
		this.refuse("a cursor minted over one backing was presented to another and answered %v", err)
	}
}

// The other half of the cursor clause, and the half no cursor the suite can
// mint reaches: a cursor another backing minted reads cleanly and names
// somebody else, while one this store cannot read at all is refused by its own
// parser or by nothing. Only the store knows what that looks like, so it is
// asked for one — and when it answers none the clause is reported rather than
// passed over, because a store that starts again at the beginning of its log
// for a truncated checkpoint re-applies every event a projector has ever seen.
func (this *probe) unparsableCursor(ctx context.Context, store event.Store) {
	if this.factory.Unparsable == nil {
		this.unable("this factory answers no cursor this store cannot parse, so the store's own reading of one was never asked for")
		return
	}
	reader, err := event.Read(store, this.factory.Unparsable(this.t, store))
	if err != nil {
		this.refuse("reading through this store answered %v", err)
	}
	switch _, err := reader.Next(ctx); {
	case err == nil:
		this.refuse("a cursor this store answered it cannot parse was accepted and answered a page, so a projector whose persisted checkpoint was truncated resumes at whatever position this store guessed and re-applies every event it has already seen")
	case !errors.Is(err, event.ErrCursor):
		this.refuse("a cursor this store answered it cannot parse was read from with %v", err)
	}
}

// The half a store with no transactions cannot be asked for: an event whose
// position is decided after a later one was already published. A store that
// answers its newest position as a cursor while a lower one can still commit
// loses exactly this event, and nothing else in the section can see it.
func (this *probe) lateWriter(ctx context.Context, store event.Store, held declaration) ([]string, int) {
	if this.capabilities.Transactions != event.Supported || this.factory.Begin == nil {
		this.unable("no writer committed out of position order, because this store has no transactions to hold one open")
		return nil, this.spread(ctx, this.bind(store, held), held, "c")
	}
	repo := this.bind(store, held)
	inside, tx := this.begin(ctx, store)
	late, beside := this.account("late"), this.account("beside")
	_, at := this.load(inside, repo, late)
	this.append(inside, repo, at, held.credited.New(late, credited{Amount: 1}))

	_, aside := this.load(ctx, repo, beside)
	this.append(ctx, repo, aside, held.credited.New(beside, credited{Amount: 2}))
	this.commit(ctx, tx)

	return []string{
		fmt.Sprintf("%s/%s@%d", held.family, this.keyFor(held, late), 1),
		fmt.Sprintf("%s/%s@%d", held.family, this.keyFor(held, beside), 1),
	}, 2
}
