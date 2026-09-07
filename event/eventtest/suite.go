package eventtest

import (
	"context"
	"crypto/rand"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
)

// What the suite requires of the transaction a factory begins, because a store
// author implements this and the sections assert it. Commit answers an error
// when this transaction has already been committed or rolled back — one case
// commits twice on purpose and reports a store whose second commit answers
// nothing. Rollback answers nil wherever the suite calls it, since the case that
// calls it is asserting the rollback rather than an error. The suite disposes of
// what it began itself: it rolls back every transaction it opened when the
// section that opened it ends, committed ones included, and ignores that answer.
type Tx interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// The factory constructs and the suite never does: constructing a store can
// fail and only the factory knows how to report it, which is why New takes a
// *testing.T rather than returning an error.
//
// A hook that is not supplied is not the same as a capability that is not
// claimed. A store that claims a capability and supplies no hook for it fails
// the run before any section starts, because a store must not be able to claim
// a capability and then avoid being tested on it.
type Factory struct {
	// Called one or more times per section, and the suite holds several of its
	// stores live at once. A store this builds must not destroy or reset what an
	// earlier one wrote in the same section: a section reads back through a
	// second value what a first one wrote, so a New that truncates its backing
	// reports a correct store as broken. Whether two of its stores share one
	// backing is the factory's own choice and is asked rather than assumed —
	// Backing().Equal is how the suite finds out, and a section that needs two
	// backings, or a second value over one, is reported not certified when this
	// factory has only the other kind. Every store it builds publishes the same
	// Limits and the same Capabilities, because the suite reads both once at the
	// door and derives every count it writes from them. Whatever a store holds
	// open is closed by the factory, through t.Cleanup.
	New func(t *testing.T) event.Store

	// Begins a transaction of this store's own and returns the context that
	// carries it. Required when the store claims Transactions. The suite rolls
	// back every transaction it began when the section that began it ends —
	// including the one it leaves deliberately unresolved across a Close — so
	// nothing this returns holds a connection or a lock into the next section.
	Begin func(t *testing.T, ctx context.Context, s event.Store) (context.Context, Tx)

	// A second store value over the same backing, which is what a restart is.
	// Required when the store claims SharedBacking.
	Sibling func(t *testing.T, s event.Store) event.Store

	// Makes the store's next Append, ReadStream or ReadAll answer a failure it
	// classifies as the named outcome, instead of performing the operation, and
	// answers false for an outcome this store cannot honestly produce — so
	// cannot and did not are told apart rather than guessed.
	Fail func(t *testing.T, s event.Store, outcome event.Outcome) bool

	// A cursor at the current end of this store's log. Optional, and it gates
	// nothing: without it every section still runs and finds the end by reading
	// to it. Supply it for a store whose log holds what this run did not write —
	// a database nothing truncates between runs, where reading to the end costs
	// the whole log once per walking section — and for one another process is
	// appending to, where a read that ends on an empty page has no end to reach.
	Tail func(t *testing.T, s event.Store) event.Cursor

	// A cursor this store cannot parse: one a projector truncated, one another
	// deployment's format, one that is not a cursor at all. Only the store knows
	// what it cannot read — a literal this suite invented would be nonsense to
	// one store and a legal position to the next — so without this hook the
	// clause is reported not certified rather than assumed. A store that reads
	// from the beginning of its log for a checkpoint it could not parse
	// re-applies every event it has ever written.
	Unparsable func(t *testing.T, s event.Store) event.Cursor

	// The window every section runs under, zero meaning the default below. The
	// contract lets a store wait for a competing transaction rather than refuse
	// at once, so a store whose operations are a network away and whose waits are
	// long sets its own here rather than being reported failed for the wait.
	Window time.Duration
}

func Run(t *testing.T, factory Factory) {
	verdicts := sweep(t, factory, inventory(), func(t *testing.T, given verdict) {
		t.Log(given.line())
		if given.word == failed {
			t.Error(given.reason)
		}
	})
	for _, given := range verdicts {
		if given.word == unreported {
			t.Errorf("eventtest: the %s section reported no verdict at all, so the run left the section before it could reach one and nothing it would have asked was asked", given.section)
		}
	}
	if certified(verdicts) == 0 {
		t.Error("eventtest: this run certified nothing — every section it ran was reported not certified, and a green run over a store that was tested on nothing is evidence of nothing")
	}
}

func sweep(t *testing.T, factory Factory, sections []section, tell func(*testing.T, verdict)) []verdict {
	opened := admit(t, factory, sections)
	verdicts := make([]verdict, 0, len(sections))
	for index, held := range sections {
		given := verdict{section: held.name}
		t.Run(held.name, func(t *testing.T) {
			given = certify(&probe{
				t:       t,
				factory: factory,
				opening: opened,
				mark:    markOf(index),
				name:    held.name,
			}, held)
			tell(t, given)
		})
		verdicts = append(verdicts, given)
	}
	return verdicts
}

func certify(subject *probe, held section) verdict {
	if held.needs != nil {
		if reason := held.needs(subject.capabilities, subject.factory); reason != "" {
			return verdict{section: held.name, word: notCertified, reason: reason}
		}
	}
	subject.walk(held.run)
	return subject.verdict()
}

// What the run learned at the door and every section is handed: what this store
// publishes, and the name this run's own streams carry. The limits are read once
// here rather than per section because every count the suite writes is derived
// from them, and a store publishing two sets of them would make one section's
// derivation another section's.
type opening struct {
	capabilities event.Capabilities
	limits       event.Limits
	run          string
}

// The first anti-vacuity rule and the third, before a section runs: a store that
// claims a capability and supplies no hook fails, and a store that states
// nothing about one is refused here as it is at Bind and at Read. Both are
// fatal rather than a section's failure, because neither says anything about
// one property — it says the run cannot be evidence of anything. So is a MaxKey
// with no room for the suite's own identities, and for the same reason: it is
// this suite's requirement rather than the store's defect.
func admit(t *testing.T, factory Factory, sections []section) opening {
	if factory.New == nil {
		t.Fatal("eventtest: this factory builds no store, so there is nothing to certify")
	}
	store := factory.New(t)
	if store == nil {
		t.Fatal("eventtest: this factory answered no store")
	}
	capabilities := store.Capabilities()
	if broken := missing(capabilities, factory); broken != "" {
		t.Fatal("eventtest: " + broken)
	}
	limits := store.Limits()
	return opening{capabilities: capabilities, limits: limits, run: runIdentity(t, limits.MaxKey, reserve(sections))}
}

func missing(capabilities event.Capabilities, factory Factory) string {
	for _, claim := range []struct {
		name    string
		stated  event.Support
		hook    string
		absent  bool
		section string
	}{
		{"Transactions", capabilities.Transactions, "Factory.Begin", factory.Begin == nil, "transactions"},
		{"Persistence", capabilities.Persistence, "", false, "durability"},
		{"MonotoneVisibility", capabilities.MonotoneVisibility, "", false, "monotone visibility"},
		{"SharedBacking", capabilities.SharedBacking, "Factory.Sibling", factory.Sibling == nil, "shared backing"},
	} {
		if claim.stated == event.Unstated {
			return "this store states nothing about " + claim.name + ", and a capability nobody stated is not one nobody has: the " + claim.section + " section would be skipped by a store that forgot a field"
		}
		if claim.stated == event.Supported && claim.absent {
			return "this store claims " + claim.name + " and this factory supplies no " + claim.hook + ", so the " + claim.section + " section cannot run — a claimed capability is never skipped"
		}
	}
	return ""
}

func markOf(index int) string { return "s" + strconv.Itoa(index) + "-" }

// What the suite spends of a store's MaxKey beyond the run identity: the
// separator Compose writes between an identity's two parts, the widest section
// mark, and the widest label a section names — which account refuses to exceed,
// so a section that wants a longer one is told so here rather than through a
// store's own refusal of a key it never chose.
const widestLabel = 8

func reserve(sections []section) int {
	return 1 + len(markOf(max(len(sections)-1, 0))) + widestLabel
}

// Sixteen bytes where the store has room for them, because a collision here is
// not a crash: nothing truncates a store between runs, so two runs that drew one
// identity over one long-lived backing count each other's events as their own
// and refuse a correct store for them. Four bytes collide at around 77 000 runs
// against one backing, which a database reaches; sixteen is the width at which
// the question stops being one, and a store whose MaxKey has room for fewer
// narrows it to what fits rather than being reported failed for a key of the
// suite's own choosing.
const (
	identityBytes    = 16
	narrowestRunName = 4
)

func runIdentity(t *testing.T, maxKey, reserved int) string {
	t.Helper()
	width := min(identityBytes, (maxKey-reserved)/2)
	if width < narrowestRunName {
		t.Fatalf("eventtest: this store publishes a MaxKey of %d and this suite needs %d of it — %d for the narrowest run identity it will tell its own streams from another run's by, and %d for its own section marks and labels",
			maxKey, 2*narrowestRunName+reserved, 2*narrowestRunName, reserved)
	}
	identity := make([]byte, width)
	if _, err := rand.Read(identity); err != nil {
		t.Fatalf("eventtest: this run cannot name its own streams: %v", err)
	}
	return fmt.Sprintf("%x", identity)
}
