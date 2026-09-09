package eventtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
)

// The factory constructs and the suite never does, exactly as the store suite's
// does, and for the same reason: constructing a checkpoint store can fail and
// only the factory knows how to report it.
//
// A hook that is not supplied is not the same as a capability that is not
// claimed. A store that claims one and supplies no hook for it fails the run
// before any section starts.
type CheckpointFactory struct {
	// Called one or more times per section, and the suite holds several of its
	// values live at once. A value this builds must not destroy or reset what an
	// earlier one wrote in the same section: a section reads back through a
	// second value what a first one wrote. Every value it builds publishes the
	// same Capabilities, because the suite reads them once at the door and gates
	// every section on that reading. Whatever a value holds open is closed by the
	// factory, through t.Cleanup.
	New func(t *testing.T) event.Checkpoints

	// Begins a transaction this checkpoint store joins and returns the context
	// that carries it. Required when the store claims Transactions. The suite
	// rolls back every transaction it began when the section that began it ends.
	Begin func(t *testing.T, ctx context.Context, c event.Checkpoints) (context.Context, Tx)

	// A second value over the same backing, which is what a restart is. Required
	// when the store claims Persistence.
	Sibling func(t *testing.T, c event.Checkpoints) event.Checkpoints

	// A cursor of the log this checkpoint store records against, real rather than
	// a literal the suite invented — a cursor one store cannot read is a legal
	// position to the next, and only the log knows which is which. Required, and
	// called several times per section: two consecutive calls must answer two
	// different cursors, because a section that cannot tell a stale answer from a
	// fresh one certifies a store that never wrote.
	Cursor func(t *testing.T) event.Cursor

	// The grain the instant this store keeps is held at, which only the store
	// knows: microsecond is PostgreSQL's timestamptz and nothing else's, and a
	// suite that mints at one backing's precision reports every other backing
	// broken on nine properties it does not get wrong. Every instant the suite
	// mints goes through it, so what a section then asserts is the round trip and
	// not the grain. Absent means exact: the value handed in is the value
	// answered. It must round rather than invent — applying it twice answers what
	// applying it once did — and it must keep two instants a second apart apart,
	// because a grain coarser than that tells no two passes of a projection from
	// each other and the round trip would assert nothing.
	Instant func(minted time.Time) time.Time

	// The window every section runs under, zero meaning the default.
	Window time.Duration
}

const (
	buildsNoCheckpoints  = "eventtest: this factory builds no checkpoint store, so there is nothing to certify"
	answersNoCheckpoints = "eventtest: this factory answered no checkpoint store"
	mintsNoCursor        = "eventtest: this factory answers no cursor of the log this checkpoint store records against, and a cursor literal this suite invented is nonsense to one store and a legal position to the next"
	repeatsItsCursor     = "eventtest: this factory answered one cursor to two calls, so a section that saves twice cannot tell a store that wrote the second from one that kept the first"
	inventsItsInstant    = "eventtest: this factory's Instant answered one value and then another for the value it had already answered, so it invents an instant rather than declaring the grain this store keeps one at, and no round trip below could be exact"
	collapsesItsInstant  = "eventtest: this factory's Instant answered one instant for two a second apart, and a grain that coarse tells no two passes of a projection from each other, so the round trip below would assert nothing"
)

func RunCheckpoints(t *testing.T, factory CheckpointFactory) {
	report(t, sweep(t, checkpointInventory(), tracking(t, factory), telling))
}

func tracking(t *testing.T, factory CheckpointFactory) func(*testing.T, string, string) *checkpoints {
	claims := admitCheckpoints(t, factory)
	run := runIdentity(t, event.MaxNameBytes, len(checkpointName("", "s99-", strings.Repeat("l", widestLabel))))
	return func(t *testing.T, name, mark string) *checkpoints {
		return &checkpoints{
			recording: recording{t: t, name: name, mark: mark, window: factory.Window},
			factory:   factory,
			claims:    claims,
			run:       run,
		}
	}
}

// What the run learned at the door and every section is handed: what this
// checkpoint store claims, and the name this run's own rows carry. Two runs
// against one database that drew one name would count each other's rows as their
// own and report a correct store as broken.
type checkpoints struct {
	recording
	factory CheckpointFactory
	claims  event.CheckpointCapabilities
	run     string
}

// The first anti-vacuity rule and the third, before a section runs: a store that
// claims a capability and supplies no hook fails, and a store that states
// nothing about one is refused here. Both are fatal rather than a section's
// failure, because neither says anything about one property — it says the run
// cannot be evidence of anything.
func admitCheckpoints(t *testing.T, factory CheckpointFactory) event.CheckpointCapabilities {
	if factory.New == nil {
		t.Fatal(buildsNoCheckpoints)
	}
	held := factory.New(t)
	if held == nil {
		t.Fatal(answersNoCheckpoints)
	}
	if factory.Cursor == nil {
		t.Fatal(mintsNoCursor)
	}
	claims := held.Capabilities()
	if broken := missingCheckpointHook(claims, factory); broken != "" {
		t.Fatal("eventtest: " + broken)
	}
	if factory.Cursor(t) == factory.Cursor(t) {
		t.Fatal(repeatsItsCursor)
	}
	admitInstant(t, factory)
	return claims
}

// The second anti-vacuity rule applied to the other hook a store supplies
// because only it knows the answer: a grain is a rounding of the instant this
// suite mints and not a value of the store's own choosing.
func admitInstant(t *testing.T, factory CheckpointFactory) {
	if factory.Instant == nil {
		return
	}
	now := time.Now().UTC()
	held := factory.Instant(now)
	if !factory.Instant(held).Equal(held) {
		t.Fatal(inventsItsInstant)
	}
	if factory.Instant(now.Add(time.Second)).Equal(held) {
		t.Fatal(collapsesItsInstant)
	}
}

func missingCheckpointHook(claims event.CheckpointCapabilities, factory CheckpointFactory) string {
	for _, claim := range []struct {
		name    string
		stated  event.Support
		hook    string
		absent  bool
		section string
	}{
		{"Transactions", claims.Transactions, "Factory.Begin", factory.Begin == nil, "transactions"},
		{"Persistence", claims.Persistence, "Factory.Sibling", factory.Sibling == nil, "durability"},
	} {
		if claim.stated == event.Unstated {
			return "this checkpoint store states nothing about " + claim.name + ", and a capability nobody stated is not one nobody has: the " + claim.section + " section would be skipped by a store that forgot a field"
		}
		if claim.stated == event.Supported && claim.absent {
			return "this checkpoint store claims " + claim.name + " and this factory supplies no " + claim.hook + ", so the " + claim.section + " section cannot run — a claimed capability is never skipped"
		}
	}
	return ""
}

func checkpointName(run, mark, label string) string {
	return "eventtest.checkpoints." + run + "." + mark + label
}

func (this *checkpoints) named(label string) string {
	if len(label) > widestLabel {
		this.refuse("this section named a projection %q, where the suite reserves %d bytes of the name for a label", label, widestLabel)
	}
	return checkpointName(this.run, this.mark, label)
}

func (this *checkpoints) store() event.Checkpoints {
	held := this.factory.New(this.t)
	if held == nil {
		this.refuse("the factory answered no checkpoint store")
	}
	if held.Capabilities() != this.claims {
		this.refuse("this factory built a checkpoint store claiming %+v where the one it was admitted on claims %+v, and every section this run gated it on was gated on the second", held.Capabilities(), this.claims)
	}
	return held
}

func (this *checkpoints) beside(held event.Checkpoints) event.Checkpoints {
	if this.factory.Sibling == nil {
		return nil
	}
	second := this.factory.Sibling(this.t, held)
	if second == nil {
		this.refuse("the factory answered no second value over this checkpoint store's backing")
	}
	return second
}

func (this *checkpoints) cursor() event.Cursor {
	minted := this.factory.Cursor(this.t)
	if minted == "" {
		this.refuse("the factory answered the empty cursor, which is the origin of a log rather than a point to resume past")
	}
	if len(minted) > event.MaxCursorBytes {
		this.refuse("the factory answered a cursor of %d bytes where the kernel publishes a ceiling of %d", len(minted), event.MaxCursorBytes)
	}
	return minted
}

// The instant is minted at the store's own grain, because a checkpoint row keeps
// it in whatever its backing holds one in and this suite knows nothing about
// that. What the round trip then asserts is the property every store owes — that
// the value it was handed is the value it answers — rather than a precision one
// backing happens to have.
func (this *checkpoints) progress(highest event.Position, applied, quarantined uint64) event.Progress {
	minted := time.Now().UTC()
	if this.factory.Instant != nil {
		minted = this.factory.Instant(minted)
	}
	return event.Progress{Highest: highest, Applied: applied, Quarantined: quarantined, At: minted}
}

func (this *checkpoints) load(ctx context.Context, held event.Checkpoints, projection string) event.Checkpoint {
	found, err := held.Load(ctx, projection)
	if err != nil {
		this.refuse("loading the checkpoint of %q answered %v", projection, err)
	}
	return found
}

func (this *checkpoints) save(ctx context.Context, held event.Checkpoints, checkpoint event.Checkpoint) {
	if err := held.Save(ctx, checkpoint); err != nil {
		this.refuse("saving %q at advance %d answered %v", checkpoint.Projection, checkpoint.Advance, err)
	}
}

func (this *checkpoints) forget(ctx context.Context, held event.Checkpoints, projection string) {
	if err := held.Forget(ctx, projection); err != nil {
		this.refuse("forgetting the checkpoint of %q answered %v", projection, err)
	}
}

// The disposal is registered here rather than left to the section, because a
// section that refuses leaves through a panic: a transaction of a store with a
// connection pool holds a connection and every row lock it took until something
// finishes it, and the next section's value draws from that same pool.
func (this *checkpoints) begin(ctx context.Context, held event.Checkpoints) (context.Context, Tx) {
	inside, tx := this.factory.Begin(this.t, ctx, held)
	if tx == nil {
		this.refuse("the factory began no transaction")
	}
	if inside == nil {
		this.refuse("the factory answered no context carrying the transaction it began")
	}
	this.t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return inside, tx
}

func (this *checkpoints) commit(ctx context.Context, tx Tx) {
	if err := tx.Commit(ctx); err != nil {
		this.refuse("committing the transaction this section was inside answered %v", err)
	}
}

func (this *checkpoints) rollback(ctx context.Context, tx Tx) {
	if err := tx.Rollback(ctx); err != nil {
		this.refuse("rolling back the transaction this section was inside answered %v", err)
	}
}

// A checkpoint store classifies its own failure through the same seven outcomes
// a store does and adds no sentinel, so what a section reads off a refusal is
// the outcome and never the text: a store's own message names the projection and
// sometimes the credentials it connected with.
func (this *checkpoints) classified(err error, outcome event.Outcome, doing string) {
	if err == nil {
		this.refuse("%s was admitted, and this store owes it %v", doing, outcome)
	}
	if err.Error() != event.Failure(outcome, nil).Error() {
		this.refuse("%s answered %q rather than a failure this store classified %v", doing, err, outcome)
	}
}

func (this *checkpoints) unchangedRow(ctx context.Context, held event.Checkpoints, projection string, before event.Checkpoint, doing string) {
	after := this.load(ctx, held, projection)
	if !sameCheckpoint(after, before) {
		this.refuse("%s left the row of %q at %+v where it held %+v", doing, projection, after, before)
	}
}

// Field by field and never with ==, because time.Time's equality carries a
// monotonic reading and a *Location: two reads of one row through one driver
// would otherwise differ for a difference that is not one.
func sameCheckpoint(first, second event.Checkpoint) bool {
	return first.Projection == second.Projection &&
		first.Cursor == second.Cursor &&
		first.Advance == second.Advance &&
		first.Progress.Highest == second.Progress.Highest &&
		first.Progress.Applied == second.Progress.Applied &&
		first.Progress.Quarantined == second.Progress.Quarantined &&
		first.Progress.At.Equal(second.Progress.At)
}
