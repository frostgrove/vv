package eventtest

import (
	"context"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/receipt"
)

// The factory constructs and the harness never does, exactly as the store
// suite's does. A ledger is the application's own table and only its author
// knows how to open one, which is why New takes a *testing.T rather than
// returning an error.
//
// THE HARNESS WRITES ROWS IT DOES NOT REMOVE. Every key it claims carries a run
// identity of its own, so two runs against one table never read each other's
// rows; the sweep is the application's, here as everywhere else.
type LedgerFactory struct {
	// Called one or more times per section, and the harness holds several of its
	// values live at once. A value this builds must not destroy or reset what an
	// earlier one wrote in the same section: a section reads back through a
	// second value what a first one claimed. Whatever a value holds open is
	// closed by the factory, through t.Cleanup.
	New func(t *testing.T) receipt.Ledger

	// Begins a unit of this ledger's own and answers the context that carries
	// it. Required, and called twice at once: the section that measures the
	// claim's two statements holds one unit open while a second claims the same
	// key, so a factory whose units come from a pool of one blocks until the
	// section window expires. The harness rolls back every unit it began when the
	// section that began it ends.
	Begin func(t *testing.T, ctx context.Context, l receipt.Ledger) (context.Context, Tx)

	// The window every section runs under, zero meaning the default. A claim that
	// blocks on the index blocks for as long as the unit ahead of it is open, and
	// this harness never holds one open longer than one section.
	Window time.Duration
}

const (
	buildsNoLedger  = "eventtest: this factory builds no ledger, so there is nothing to certify"
	answersNoLedger = "eventtest: this factory answered no ledger"
	opensNoUnit     = "eventtest: this factory begins no unit of work, and a claim outside one is a row that outlives the rollback of the events it names — there is no section here that could run without one"
)

// RunLedger certifies a receipt.Ledger against the obligations receipt.Claim
// cannot check: the two statements of a claim in their one order, where each
// method runs, and a horizon at or before the oldest row the ledger still
// answers for.
func RunLedger(t *testing.T, factory LedgerFactory) {
	report(t, sweep(t, ledgerInventory(), claiming(t, factory), telling))
}

func claiming(t *testing.T, factory LedgerFactory) func(*testing.T, string, string) *receipts {
	admitLedger(t, factory)
	run := runIdentity(t, event.MaxNameBytes, len(ledgerKey("", "s99-", "llllllll")))
	return func(t *testing.T, name, mark string) *receipts {
		return &receipts{
			recording: recording{t: t, name: name, mark: mark, window: factory.Window},
			factory:   factory,
			run:       run,
		}
	}
}

type receipts struct {
	recording
	factory LedgerFactory
	run     string
}

// The first anti-vacuity rule, before a section runs: a harness that cannot
// build a ledger or open a unit certifies nothing, and that is fatal rather than
// one section's failure — it says the run cannot be evidence of anything.
func admitLedger(t *testing.T, factory LedgerFactory) {
	if factory.New == nil {
		t.Fatal(buildsNoLedger)
	}
	if factory.Begin == nil {
		t.Fatal(opensNoUnit)
	}
	if factory.New(t) == nil {
		t.Fatal(answersNoLedger)
	}
}

func ledgerKey(run, mark, label string) string {
	return "eventtest.ledger." + run + "." + mark + label
}

func (this *receipts) held() receipt.Ledger {
	found := this.factory.New(this.t)
	if found == nil {
		this.refuse("the factory answered no ledger")
	}
	return found
}

func (this *receipts) key(label string) receipt.Key {
	if len(label) > widestLabel {
		this.refuse("this section named an operation %q, where the harness reserves %d bytes of the key for a label", label, widestLabel)
	}
	held, err := receipt.NewKey(ledgerKey(this.run, this.mark, label))
	if err != nil {
		this.refuse("the harness cannot mint an operation key of its own: %v", err)
	}
	return held
}

// Two fingerprints that differ, so a section can tell a repeat from a collision
// without digesting anything: what a ledger owes is the bytes back, and the
// digest they came from is the caller's business.
func (this *receipts) print(seed byte) receipt.Fingerprint {
	digest := [32]byte{}
	for index := range digest {
		digest[index] = seed
	}
	held, err := receipt.NewFingerprint(digest)
	if err != nil {
		this.refuse("the harness cannot mint a fingerprint of its own: %v", err)
	}
	return held
}

func (this *receipts) stream(label string) event.Stream {
	return event.Stream{Family: "eventtest.ledger", Key: event.Compose(this.run, this.mark+label)}
}

func (this *receipts) receipt(label string, seed byte) receipt.Receipt {
	return receipt.Receipt{Key: this.key(label), Fingerprint: this.print(seed), Stream: this.stream(label)}
}

// The disposal is registered here rather than left to the section, because a
// section that refuses leaves through a panic: a unit of a ledger with a
// connection pool holds a connection and every row lock it took until something
// finishes it, and the next section's value draws from that same pool.
func (this *receipts) begin(ctx context.Context, held receipt.Ledger) (context.Context, Tx) {
	inside, tx := this.factory.Begin(this.t, ctx, held)
	if tx == nil {
		this.refuse("the factory began no unit of work")
	}
	if inside == nil {
		this.refuse("the factory answered no context carrying the unit it began")
	}
	this.t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return inside, tx
}

func (this *receipts) commit(ctx context.Context, tx Tx) {
	if err := tx.Commit(ctx); err != nil {
		this.refuse("committing the unit this section was inside answered %v", err)
	}
}

func (this *receipts) rollback(ctx context.Context, tx Tx) {
	if err := tx.Rollback(ctx); err != nil {
		this.refuse("rolling back the unit this section was inside answered %v", err)
	}
}

func (this *receipts) claim(ctx context.Context, held receipt.Ledger, row receipt.Receipt) (receipt.Receipt, bool) {
	found, won, err := held.Claim(ctx, row)
	if err != nil {
		this.refuse("claiming %v answered %v", row.Key, err)
	}
	return found, won
}

func (this *receipts) complete(ctx context.Context, held receipt.Ledger, row receipt.Receipt) {
	if err := held.Complete(ctx, row); err != nil {
		this.refuse("completing %v over the range %d..%d answered %v", row.Key, row.First, row.Last, err)
	}
}

func (this *receipts) find(ctx context.Context, held receipt.Ledger, key receipt.Key) (receipt.Receipt, bool) {
	found, taken, err := held.Find(ctx, key)
	if err != nil {
		this.refuse("looking %v up answered %v", key, err)
	}
	return found, taken
}

func (this *receipts) horizon(ctx context.Context, held receipt.Ledger) time.Time {
	at, err := held.Horizon(ctx)
	if err != nil {
		this.refuse("reading this ledger's horizon answered %v", err)
	}
	return at
}

func (this *receipts) authority(ctx context.Context, held receipt.Ledger, where string) event.Authority {
	found, err := held.Transaction(ctx)
	if err != nil {
		this.refuse("asking this ledger which transaction %s carries answered %v", where, err)
	}
	return found
}

// The row a claim answered, compared field by field: time.Time carries a
// monotonic reading and a *Location, so two reads of one row through one driver
// would differ under == for a difference that is not one.
func (this *receipts) sameRow(found, want receipt.Receipt, doing string) {
	switch {
	case found.Key != want.Key:
		this.refuse("%s answered a row for another key than the one it was asked about", doing)
	case !found.Fingerprint.Equal(want.Fingerprint):
		this.refuse("%s answered the fingerprint %v where the row holds %v", doing, found.Fingerprint, want.Fingerprint)
	case found.Stream != want.Stream:
		this.refuse("%s answered the stream %s where the row holds %s", doing, found.Stream, want.Stream)
	case found.First != want.First || found.Last != want.Last:
		this.refuse("%s answered the range %d..%d where the row holds %d..%d", doing, found.First, found.Last, want.First, want.Last)
	case found.Complete != want.Complete:
		this.refuse("%s answered a row whose completion is %v where the row holds %v", doing, found.Complete, want.Complete)
	}
}
