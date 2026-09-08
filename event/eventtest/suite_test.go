package eventtest_test

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventtest"
)

// The compile-time half of the zero-diff obligation, and it is a demonstration
// rather than a claim: unexport one field of event.Envelope and this package
// stops compiling.
func TestATrivialStoreNeedsNoInternalAccess(t *testing.T) {
	eventtest.Run(t, sliceFactory(false, nil))
}

func TestATransactionCapableStoreSatisfiesTheContract(t *testing.T) {
	eventtest.Run(t, stagingFactory(false, nil))
}

// The precondition of anti-vacuity rule 2 and not the rule itself: three
// sections a store claims none of are reported in the third word and none of
// them counts toward the certifications a run is refused for having none of.
// That a run of nothing but those fails is Run's answer, and
// TestTheRunReportsWhatItFound is where it is watched.
func TestAGatedSectionIsReportedNotCertified(t *testing.T) {
	gated := eventtest.Certify(t, sliceFactory(false, nil), "transactions", "durability", "shared backing")
	if certified := eventtest.Certified(gated); certified != 0 {
		t.Fatalf("a run of three sections this store claims none of certified %d of them, so the case below asserts nothing", certified)
	}
	if len(gated) != 3 {
		t.Fatalf("a run of three sections reported %d verdicts", len(gated))
	}
	for _, given := range gated {
		if given.Word != "not certified" {
			t.Fatalf("the %s section was reported %q for a store that does not claim it", given.Section, given.Word)
		}
	}

	whole := eventtest.Certify(t, sliceFactory(false, nil))
	if eventtest.Certified(whole) == 0 {
		t.Fatal("a whole run over a store that satisfies the contract certified nothing, so the count a run fails on is stuck at zero and would fail every run")
	}
}

// The row a section that never returned leaves behind. A factory hook that
// calls t.Fatal leaves the subtest through runtime.Goexit, so nothing inside the
// section ever answers a verdict and the runner keeps the row it initialised.
// SkipNow leaves it by the same door and is the one of the two this test can
// watch without the failure propagating to the test doing the watching, and the
// word on that row must not be the one that means the store is correct. That Run
// reports the row rather than counting it is watched by
// TestTheRunReportsWhatItFound.
func TestTheVerdictOfASectionThatNeverReturnedIsNotPassed(t *testing.T) {
	factory := stagingFactory(false, nil)
	factory.Begin = func(t *testing.T, _ context.Context, _ event.Store) (context.Context, eventtest.Tx) {
		t.SkipNow()
		return nil, nil
	}
	verdicts := eventtest.Certify(t, factory, "transactions")
	if len(verdicts) != 1 {
		t.Fatalf("running one section reported %d verdicts", len(verdicts))
	}
	if verdicts[0].Word == "passed" {
		t.Fatal("a section whose factory left the run through the goroutine that section was dispatched on was reported passed, so a store nothing was asked of certified a section")
	}
	if eventtest.Certified(verdicts) != 0 {
		t.Fatalf("a section that answered no verdict counted toward the %d certifications a run is refused for having none of", eventtest.Certified(verdicts))
	}
}

// The rule the door applies, asked of the rule rather than of the door: what
// the door then does with it — a fatal before any section runs — is
// TestTheRunReportsWhatItFound's two admission cases, which drive these same
// three shapes through Run itself.
func TestAClaimWithNoHookAndACapabilityNobodyStatedAreBothNamed(t *testing.T) {
	for _, claim := range []struct {
		what    string
		stated  event.Capabilities
		factory eventtest.Factory
	}{
		{"a store that claims transactions and a factory that begins none", claimed(event.Supported, event.Unsupported), eventtest.Factory{}},
		{"a store that claims a shared backing and a factory with no second value", claimed(event.Unsupported, event.Supported), eventtest.Factory{}},
		{"a store that states nothing about its transactions", event.Capabilities{Persistence: event.Unsupported, MonotoneVisibility: event.Unsupported, SharedBacking: event.Unsupported}, eventtest.Factory{}},
	} {
		if eventtest.Missing(claim.stated, claim.factory) == "" {
			t.Fatalf("%s was admitted, and a store that claims a capability and then avoids being tested on it is what the section it would skip exists to catch", claim.what)
		}
	}

	whole := eventtest.Factory{
		Begin:   stagingFactory(false, nil).Begin,
		Sibling: stagingFactory(false, nil).Sibling,
	}
	if broken := eventtest.Missing(claimed(event.Supported, event.Supported), whole); broken != "" {
		t.Fatalf("a store that claims both capabilities and a factory that supplies both hooks was refused: %s", broken)
	}
}

func claimed(transactions, sharedBacking event.Support) event.Capabilities {
	return event.Capabilities{
		Transactions:       transactions,
		Persistence:        event.Unsupported,
		MonotoneVisibility: event.Unsupported,
		SharedBacking:      sharedBacking,
	}
}

// The contract lets a store wait for a competing transaction rather than refuse
// at once, so a section that hands a store a context nothing will ever cancel is
// a section that can hang the binary instead of reporting a verdict. This
// watches the four doors that take one.
func TestEveryStoreCallASectionMakesCarriesADeadline(t *testing.T) {
	watching := &deadlines{}
	eventtest.Certify(t, stagingFactory(false, func(store event.Store) event.Store {
		return timed{Store: store, watching: watching}
	}))
	seen, open := watching.counts()
	if seen == 0 {
		t.Fatal("a whole run made no store call at all through the value it was handed, so nothing below was measured")
	}
	if open != 0 {
		t.Errorf("%d of %d store calls a run made carried no deadline, and a store that waits on one of them is reported by go test's own panic rather than by the section it was in", open, seen)
	}
}

// The first store this suite meets whose log it did not fill is a database, and
// nothing truncates one between runs. This is that store: 6 000 events written
// by nobody here — more than the 1 024 pages at this fixture's MaxRead of five
// that the walk once gave up after — and every section must still reach its own
// verdict rather than report the suite's own budget as the store's defect.
func TestASectionWalksItsOwnTailOfALogSomebodyElseFilled(t *testing.T) {
	verdicts := eventtest.Certify(t, prefilledFactory(6000))
	if len(verdicts) != len(eventtest.SectionNames()) {
		t.Fatalf("a whole run reported %d verdicts where the suite has %d sections", len(verdicts), len(eventtest.SectionNames()))
	}
	for _, given := range verdicts {
		if given.Word == "failed" {
			t.Errorf("the %s section was reported failed over a store whose only difference is a log it did not write: %s", given.Section, given.Reason)
		}
	}
	if eventtest.Certified(verdicts) != eventtest.Certified(eventtest.Certify(t, stagingFactory(false, nil))) {
		t.Error("the same store certified a different number of sections with a log somebody else filled in front of it, so what a section reads depends on what it does not own")
	}
}

// Every count a section writes is derived from what the store publishes, so the
// numbers a store is free to choose cannot decide a verdict. These are the
// narrowest the kernel admits — one change per append, one envelope per page,
// one per global read, and a MaxKey with room for the suite's own names and
// little else — and the same store at the fixture's ordinary numbers is the
// control.
func TestEverySectionIsCertifiedAtTheNarrowestLimitsAStoreMayPublish(t *testing.T) {
	narrow := eventtest.Certify(t, narrowFactory())
	if len(narrow) != len(eventtest.SectionNames()) {
		t.Fatalf("a whole run reported %d verdicts where the suite has %d sections", len(narrow), len(eventtest.SectionNames()))
	}
	for _, given := range narrow {
		if given.Word == "failed" {
			t.Errorf("the %s section was reported failed over a store whose only difference is the legal numbers it publishes: %s", given.Section, given.Reason)
		}
	}
	if eventtest.Certified(narrow) != eventtest.Certified(eventtest.Certify(t, stagingFactory(false, nil))) {
		t.Error("the same store certified a different number of sections at a MaxBatch of one, a page of one and a MaxKey of forty, so what a section certifies is decided by numbers the store is free to choose")
	}
}

// A store whose log grows while a section is walking it, which is a database two
// deployments share. The end an empty page marks never arrives there, so a
// section reaches a verdict only because what it walks is bounded by what it
// wrote and because the factory answers where the log's end was.
func TestASectionReachesAVerdictOverALogSomebodyElseIsStillWritingTo(t *testing.T) {
	verdicts := eventtest.Certify(t, busyFactory())
	if len(verdicts) != len(eventtest.SectionNames()) {
		t.Fatalf("a whole run reported %d verdicts where the suite has %d sections", len(verdicts), len(eventtest.SectionNames()))
	}
	for _, given := range verdicts {
		if given.Word == "failed" {
			t.Errorf("the %s section was reported failed over a store whose only difference is that somebody else is appending to its log: %s", given.Section, given.Reason)
		}
	}
	if eventtest.Certified(verdicts) != eventtest.Certified(eventtest.Certify(t, stagingFactory(false, nil))) {
		t.Error("the same store certified a different number of sections while another writer was appending to its log")
	}
}

// The one section whose fixture nothing else in this tree carries: no other
// store here claims persistence, so without this its assertion path runs only
// against the store that refuses everything and its first real exercise would be
// the first store that claims it.
func TestAStoreThatKeepsWhatItWroteIsCertifiedForDurability(t *testing.T) {
	if word := oneVerdict(t, persistentFactory(false), "durability"); word != "passed" {
		t.Errorf("a store whose second value over one backing reads what the first wrote was reported %q for durability", word)
	}
	if word := oneVerdict(t, persistentFactory(true), "durability"); word != "failed" {
		t.Errorf("a store that claims persistence and whose second value over one backing has none of what the first wrote was reported %q for durability", word)
	}
}

// The instant is one of the four things the division of labour puts in the
// store's own column, and it orders nothing — so a store that loses it is
// invisible to every other section and to every consumer until somebody reads an
// audit trail. The store that answers the zero time is the defect inventory's;
// this is the subtler one, which has no column at all and fills the field when
// the row is read, so one event has as many instants as it has readers.
func TestAStoreThatMintsTheRecordedInstantWhenAnEventIsReadIsNotCertified(t *testing.T) {
	broken := stagingFactory(false, func(s event.Store) event.Store { return &reminting{Store: s} })
	if word := oneVerdict(t, broken, "dense versions"); word != "failed" {
		t.Errorf("a store that mints the recorded instant at read time was reported %q for dense versions, and an audit trail regenerated on every read is worse than a missing one", word)
	}
	if word := oneVerdict(t, stagingFactory(false, nil), "dense versions"); word != "passed" {
		t.Errorf("the same store recording its own instants was reported %q for dense versions, so the failure above is not the store's", word)
	}
}

// Capabilities, Limits and Backing are constant for a store's life, and each is
// read by two parties at two moments: this suite keeps all three from the door,
// the kernel keeps the first two from Bind and re-reads the backing per
// operation. A store that derives one of them from whatever executor is bound
// answers one party one thing and the other another. The limits half is the
// defect inventory's; these are the two beside it, and both passed every section
// of this suite before the clause was asserted.
func TestAStoreWhoseCapabilitiesOrBackingChangeUnderOneValueIsNotCertified(t *testing.T) {
	for _, store := range []struct {
		what string
		over func(event.Store) event.Store
	}{
		{"claims transactions on one call and not on the next", func(s event.Store) event.Store { return &wavering{Store: s} }},
		{"names a fresh backing on every call", func(s event.Store) event.Store { return &repointing{Store: s} }},
	} {
		if word := oneVerdict(t, stagingFactory(false, store.over), "binding"); word != "failed" {
			t.Errorf("a store that %s was reported %q for binding", store.what, word)
		}
	}
	if word := oneVerdict(t, stagingFactory(false, nil), "binding"); word != "passed" {
		t.Errorf("the same store answering one value to every call was reported %q for binding, so the failures above are not the two stores'", word)
	}
}

// The other constancy obligation, and it is the factory's rather than a store's:
// every store New builds publishes the same numbers and the same claims, because
// the suite reads both once at the door and every count it writes and every
// section it gates comes from that reading. A factory whose second store differs
// makes one section's derivation another section's, and no message anywhere names
// the factory unless the second store is compared with the admitted one.
func TestAFactoryWhoseLaterStoresPublishSomethingElseIsRefused(t *testing.T) {
	for _, factory := range []struct {
		what  string
		later func(event.Store) event.Store
	}{
		{"publishes a wider page than the store it was admitted on", func(s event.Store) event.Store { return relimiting{Store: s} }},
		{"claims fewer capabilities than the store it was admitted on", func(s event.Store) event.Store { return unclaiming{Store: s} }},
	} {
		if word := oneVerdict(t, shiftingFactory(factory.later), "binding"); word != "failed" {
			t.Errorf("a factory whose second store %s was reported %q for binding", factory.what, word)
		}
	}
	if word := oneVerdict(t, shiftingFactory(nil), "binding"); word != "passed" {
		t.Errorf("the same factory building one kind of store throughout was reported %q for binding, so the failures above are not the factory's shape", word)
	}
}

// The half of the cursor clause no cursor this suite can mint reaches: a cursor
// another backing minted parses cleanly and names somebody else, and only the
// store knows what it cannot read at all. Without the hook the clause is
// reported in the third word rather than counted as a pass, because the store
// phase two writes builds every value over one database and has no elsewhere for
// the foreign half either.
func TestACursorAStoreCannotParseIsRefusedRatherThanReadFromTheBeginning(t *testing.T) {
	if word := oneVerdict(t, lenientFactory(true), "resumption"); word != "failed" {
		t.Errorf("a store that starts its walk at the beginning of the log for a cursor it could not parse was reported %q for resumption", word)
	}
	if word := oneVerdict(t, lenientFactory(false), "resumption"); word != "passed" {
		t.Errorf("the same store refusing what it cannot parse was reported %q for resumption, so the failure above is not the store's", word)
	}

	silent := stagingFactory(false, nil)
	silent.Unparsable = nil
	if word := oneVerdict(t, silent, "resumption"); word != "not certified" {
		t.Errorf("a factory that answers no cursor its store cannot parse was reported %q for resumption, so a clause nothing asked about was counted as one the store kept", word)
	}
}

// What this proves and what it does not, because the difference cost a review
// round: every section reaches the store and fails when the store refuses, which
// is liveness — a section that stopped asserting anything still fails here,
// because every section opens with a load. What proves a section carries a
// control is the defect inventory, where each of the twenty is named by a store
// that answers every call successfully and wrongly.
func TestEverySectionFailsAgainstAStoreThatRefusesEverything(t *testing.T) {
	verdicts := eventtest.Certify(t, refusingFactory())
	if len(verdicts) != len(eventtest.SectionNames()) {
		t.Fatalf("a whole run reported %d verdicts where the suite has %d sections", len(verdicts), len(eventtest.SectionNames()))
	}
	for _, given := range verdicts {
		if given.Word != "failed" {
			t.Errorf("the %s section was reported %q against a store that refuses every operation, so whatever else it asserts, it never reached the store", given.Section, given.Word)
		}
	}
}
