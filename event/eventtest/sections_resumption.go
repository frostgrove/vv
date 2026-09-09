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

	flight := this.heldWriter(ctx, store, held)
	written += flight.written
	tail := this.acrossTheFlight(ctx, store, held, flight, walking{from: persisted, held: held, want: written - len(seen)})
	seen = append(seen, tail...)

	if beside := this.beside(store, held); beside != nil {
		restarted := this.from(ctx, beside, walking{from: persisted, held: held, want: len(tail)}, "a walk resumed through a second store value over this backing, which is what a restart is")
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
	for _, name := range flight.names {
		if !contains(seen, name) {
			this.refuse("%s was committed after the cursor was persisted and the walk that resumed from it never returned that event", name)
		}
	}
}

// What the walk reads while a lower position is still uncommitted, and what it
// reads from there once that position commits. The whole clause is the second
// half: a store whose cursor is its newest position, rather than the highest one
// every position below which has settled, delivers everything a walk can see
// here and hands back a checkpoint past the position it could not — so the
// resumed walk after the commit never returns it, and the event is lost with no
// error on any path. Nothing else in this suite can see that: in a quiescent log
// the two cursors are the same number.
func (this *probe) acrossTheFlight(ctx context.Context, store event.Store, held declaration, flight inFlight, over walking) []event.Envelope {
	reachable := over
	reachable.want -= len(flight.uncommitted)
	during, reached := this.reading(ctx, store, reachable, "a walk resumed while a writer held a lower position uncommitted")
	if flight.tx == nil {
		return during
	}
	for _, name := range flight.uncommitted {
		if contains(during, name) {
			this.refuse("%s is in a transaction nothing has committed and a walk of the log returned it", name)
		}
	}
	this.commit(ctx, flight.tx)
	after := this.from(ctx, store, walking{from: reached, held: held, want: over.want - len(during)},
		"a walk resumed from the cursor persisted while a lower position was uncommitted")
	for _, name := range flight.uncommitted {
		if !contains(after, name) {
			this.refuse("%s drew its position before this walk persisted a cursor and committed after it, and the walk resumed from that cursor never returned it — so this store answered a cursor past a position that had not settled and a consumer resuming from it loses that event for good",
				name)
		}
	}
	return append(during, after...)
}

// The half a store with no transactions cannot be asked for: an event whose
// position is decided before a later one is published and committed after it.
// The transaction is held OPEN across the walk that follows, because a writer
// that has already committed is one no cursor can be wrong about.
type inFlight struct {
	tx          Tx
	names       []string
	uncommitted []string
	written     int
}

func (this *probe) heldWriter(ctx context.Context, store event.Store, held declaration) inFlight {
	if this.capabilities.Transactions != event.Supported || this.factory.Begin == nil {
		this.unable("no writer held a position uncommitted across the resumed walk, because this store has no transactions to hold one open")
		return inFlight{written: this.spread(ctx, this.bind(store, held), held, "c")}
	}
	repo := this.bind(store, held)
	inside, tx := this.begin(ctx, store)
	late, beside := this.account("late"), this.account("beside")
	_, at := this.load(inside, repo, late)
	this.append(inside, repo, at, held.credited.New(late, credited{Amount: 1}))

	_, aside := this.load(ctx, repo, beside)
	this.append(ctx, repo, aside, held.credited.New(beside, credited{Amount: 2}))

	lateName := fmt.Sprintf("%s/%s@%d", held.family, this.keyFor(held, late), 1)
	return inFlight{
		tx:          tx,
		names:       []string{lateName, fmt.Sprintf("%s/%s@%d", held.family, this.keyFor(held, beside), 1)},
		uncommitted: []string{lateName},
		written:     2,
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
