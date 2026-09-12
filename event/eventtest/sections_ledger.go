package eventtest

import (
	"context"
	"time"

	"github.com/frostgrove/vv/event/receipt"
)

// How long a call issued beside this goroutine is watched before it is called
// blocked. A claim that waits on the index and a cutover that waits behind a
// locking read both wait for as long as the unit ahead of them is open, and no
// implementation answers one of them in a fraction of this.
const waiting = 500 * time.Millisecond

func ledgerInventory() []section[*receipts] {
	return []section[*receipts]{
		{name: "claim", run: ledgerClaimSection},
		{name: "repeat", run: ledgerRepeatSection},
		{name: "claim order", run: ledgerClaimOrderSection},
		{name: "unit of work", run: ledgerUnitSection},
		{name: "completion", run: ledgerCompletionSection},
		{name: "horizon", run: ledgerHorizonSection},
		{name: "transaction", run: ledgerTransactionSection},
	}
}

func ledgerClaimSection(this *receipts) {
	ctx := this.context()
	held := this.held()
	inside, tx := this.begin(ctx, held)
	want := this.receipt("fresh", 1)

	found, won := this.claim(inside, held, want)
	if !won {
		this.refuse("a claim of a key no operation has ever taken answered that it did not take it, and won is the insert's affected-row count and nothing else")
	}
	this.sameRow(found, want, "the claim that took a fresh key")
	if found.RecordedAt.IsZero() {
		this.refuse("the row this claim took carries no instant, and a resolve compares that instant with the horizon this ledger publishes — RecordedAt is the ledger's own database clock and is filled on the row handed back")
	}
	this.commit(inside, tx)
}

// A claim that loses answers the row its TABLE holds, and the fingerprint on it
// is part of that row rather than an echo of the question. The caller compares
// the two to tell a retry of one operation from a second operation under one
// key, so a ledger that hands back what it was asked with makes every collision
// read as a repeat: the work is skipped, the receipt names the first operation's
// range, and a caller is told an append that never happened already had.
//
// Both losers run here because only the second discriminates. A claim made with
// the fingerprint the row already holds compares a value with itself.
func ledgerRepeatSection(this *receipts) {
	ctx := this.context()
	held := this.held()
	first, tx := this.begin(ctx, held)
	want := this.receipt("repeat", 2)

	taken, won := this.claim(first, held, want)
	if !won {
		this.refuse("a claim of a key no operation has ever taken answered that it did not take it")
	}
	taken.First, taken.Last, taken.Complete = 3, 5, true
	this.complete(first, held, taken)
	this.commit(first, tx)

	this.lost(ctx, held, this.receipt("repeat", 2), taken, "a second claim of a key a committed row already holds")
	this.lost(ctx, held, this.receipt("repeat", 9), taken, "a claim of that same key made under another operation's fingerprint")
}

func (this *receipts) lost(ctx context.Context, held receipt.Ledger, want, row receipt.Receipt, doing string) {
	inside, tx := this.begin(ctx, held)
	found, again := this.claim(inside, held, want)
	if again {
		this.refuse("%s answered that it took it, and both callers would append under one operation key", doing)
	}
	this.sameRow(found, row, doing)
	this.rollback(inside, tx)
}

// The one section this harness exists for. The claim is an insert that DOES
// NOTHING on conflict and then a select of the same key, in that order and in
// one unit: the insert is what blocks on the index, and the select is a second
// statement and therefore a second snapshot, which is what lets a loser read the
// row the winner committed while it was blocked. A select placed first sees
// nothing, its caller decides it is the first, and the insert's zero arrives
// after the decision — so two callers both proceed and one operation key holds
// two appends.
//
// Three answers are conformant for the loser and one is not. It may block and
// then report that it did not take the key, which is READ COMMITTED; it may fail,
// which is what a serialisation failure does to the whole unit; and it may not
// report that it took a key another unit is holding open.
func ledgerClaimOrderSection(this *receipts) {
	ctx := this.context()
	held := this.held()
	winner, first := this.begin(ctx, held)
	loser, second := this.begin(ctx, held)
	want := this.receipt("order", 3)

	taken, won := this.claim(winner, held, want)
	if !won {
		this.refuse("a claim of a key no operation has ever taken answered that it did not take it")
	}

	// The second claim is built here and not in the goroutine beside it: a
	// refusal leaves through a panic, and one raised anywhere but the section's
	// own goroutine takes the binary down rather than the section.
	retried := this.receipt("order", 3)
	entered, answered := make(chan struct{}), make(chan claimed, 1)
	go func() {
		close(entered)
		row, also, err := held.Claim(loser, retried)
		answered <- claimed{row: row, won: also, err: err}
	}()
	<-entered

	select {
	case got := <-answered:
		this.raced(got, taken, "while the unit that took it was still open")
	case <-time.After(waiting):
		this.commit(winner, first)
		this.raced(<-answered, taken, "once the unit that took it committed")
	}
	this.rollback(loser, second)

	inside, third := this.begin(ctx, held)
	after, again := this.claim(inside, held, this.receipt("order", 3))
	if again {
		this.refuse("a claim issued after the race answered that it took the key, so the row the winner committed is not in this ledger and nothing above was a race for it")
	}
	this.sameRow(after, taken, "the claim issued after the race")
	this.rollback(inside, third)
}

type claimed struct {
	row receipt.Receipt
	won bool
	err error
}

func (this *receipts) raced(got claimed, taken receipt.Receipt, when string) {
	if got.err != nil {
		return
	}
	if got.won {
		this.refuse("a second claim of the key another unit had already taken reported %s that it took the key too, so both callers append under one operation key and this ledger reads the row before it inserts it", when)
	}
	this.sameRow(got.row, taken, "the claim that lost the race")
}

// WHERE EACH METHOD RUNS IS PART OF THE CONTRACT, and this is the half nothing
// at any door can check: a claim written on a connection of the ledger's own
// outlives the rollback of the events it names, and a lookup issued on the
// claiming connection answers Found for an operation that can still roll back.
func ledgerUnitSection(this *receipts) {
	ctx := this.context()
	held := this.held()
	inside, tx := this.begin(ctx, held)
	want := this.receipt("unit", 4)

	if _, won := this.claim(inside, held, want); !won {
		this.refuse("a claim of a key no operation has ever taken answered that it did not take it")
	}
	if _, taken := this.find(ctx, held, want.Key); taken {
		this.refuse("a lookup outside every unit found the row of a claim nobody has committed: either the claim was written on a connection of this ledger's own or the lookup runs on the claiming one, and both of them report an operation that can still roll back as one that happened")
	}
	this.rollback(inside, tx)
	if _, taken := this.find(ctx, held, want.Key); taken {
		this.refuse("the unit the claim was taken in rolled back and its row is still there, so the claim was written on a connection of this ledger's own rather than in the caller's unit")
	}

	beside, second := this.begin(ctx, held)
	if _, won := this.claim(beside, held, want); !won {
		this.refuse("a claim of the key a rolled-back unit had taken answered that it did not take it, so the key is spent on an operation that never happened")
	}
	this.rollback(beside, second)
}

func ledgerCompletionSection(this *receipts) {
	ctx := this.context()
	held := this.held()
	inside, tx := this.begin(ctx, held)
	want := this.receipt("done", 5)

	taken, won := this.claim(inside, held, want)
	if !won {
		this.refuse("a claim of a key no operation has ever taken answered that it did not take it")
	}
	taken.First, taken.Last, taken.Complete = 4, 7, true
	this.complete(inside, held, taken)
	if _, found := this.find(ctx, held, want.Key); found {
		this.refuse("a lookup outside every unit found the row of a completion nobody has committed")
	}
	this.commit(inside, tx)

	row, found := this.find(ctx, held, want.Key)
	if !found {
		this.refuse("the unit that claimed and completed this key committed and a lookup finds no row at all")
	}
	this.sameRow(row, taken, "the lookup of a completed operation")
	if row.RecordedAt.IsZero() {
		this.refuse("the completed row carries no instant, and a resolve compares that instant with the horizon this ledger publishes")
	}
}

// A horizon is an instant at or before the OLDEST row this ledger still answers
// for. Taken from the newest instead, it reads a row that was swept as one that
// never existed, and every key minted before it resolves Expired — which is a
// caller told an operation did not happen when its events are in the log.
func ledgerHorizonSection(this *receipts) {
	ctx := this.context()
	held := this.held()
	older := this.recorded(ctx, held, "old", 6)
	newer := this.recorded(ctx, held, "new", 7)

	if older.IsZero() || newer.IsZero() {
		this.refuse("this ledger dates no row it answers for, and a horizon is compared against that instant")
	}
	if !newer.After(older) {
		this.unable("this ledger dated two rows written one after the other at one instant, so a horizon taken from its newest row and one taken from its oldest are the same instant here and nothing below would discriminate")
		return
	}
	at := this.horizon(ctx, held)
	if at.IsZero() {
		this.refuse("this ledger published the zero instant as its horizon, which claims it holds every row it has ever written for ever — a table with no sweep says so through the instant its oldest row carries")
	}
	if at.After(older) {
		this.refuse("this ledger published a horizon of %s and still answers for a row recorded at %s, so it takes its horizon from the newest row it holds rather than the oldest and reports a row that was swept as one that never existed", at, older)
	}
}

func (this *receipts) recorded(ctx context.Context, held receipt.Ledger, label string, seed byte) time.Time {
	inside, tx := this.begin(ctx, held)
	want := this.receipt(label, seed)
	taken, won := this.claim(inside, held, want)
	if !won {
		this.refuse("a claim of a key no operation has ever taken answered that it did not take it")
	}
	taken.First, taken.Last, taken.Complete = 1, 1, true
	this.complete(inside, held, taken)
	this.commit(inside, tx)
	return taken.RecordedAt
}

// What Claim and Complete rest on: the ledger and the event store resolve one
// context to one transaction, so the row and the events are one commit. A ledger
// that answers a valid authority for a context carrying no unit makes every
// claim look atomic with an append it was never inside.
func ledgerTransactionSection(this *receipts) {
	ctx := this.context()
	held := this.held()
	inside, tx := this.begin(ctx, held)

	first := this.authority(inside, held, "the unit it began")
	if !first.Valid() {
		this.refuse("this ledger answered no transaction for the context its own factory bound one to, so every claim through it is refused before it reaches the table")
	}
	if again := this.authority(inside, held, "the unit it began"); !again.Same(first) {
		this.refuse("this ledger answered two different transactions for one context, so a completion issued on the claim's own unit reads as one issued on another")
	}

	beside, second := this.begin(ctx, held)
	if other := this.authority(beside, held, "a second unit"); other.Same(first) {
		this.refuse("this ledger answered one transaction for two units of its own, so a completion written in the wrong unit compares equal to the claim's and the row survives the rollback of the events it names")
	}
	this.rollback(beside, second)
	this.rollback(inside, tx)

	if outside := this.authority(ctx, held, "no unit at all"); outside.Valid() {
		this.refuse("this ledger answered a valid transaction for a context carrying none, so a claim on its own autocommit passes the check that a receipt and its append are one commit")
	}
}
