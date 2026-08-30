package catalog

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
)

// What a Set has to get right is which database a catalog belongs to. Everything
// here is one assertion about that, and every one of them carries the twin that
// fails for the opposite mistake — because a Set that merged everything and a Set
// that shared nothing both pass half of these on their own.

// passes reports how many introspection passes a recorder saw.
func passes(t *testing.T, r *crudtest.Recorder) int {
	t.Helper()
	n := len(r.Statements())
	if n%pgPass != 0 {
		t.Fatalf("the recorder saw %d statements, which is not a whole number of %d-statement passes: %v",
			n, pgPass, r.SQL())
	}
	return n / pgPass
}

func TestTwoSourcesOverDifferentHandlesDoNotShareACatalog(t *testing.T) {
	ctx := context.Background()
	handleA, handleB := new(int), new(int)
	recA, recB := recorder(oneTable(), 1), recorder(oneTable(), 1)
	srcA := identified{Recorder: recA, handle: handleA}
	srcB := identified{Recorder: recB, handle: handleB}

	var set Set
	catA, err := set.Load(ctx, srcA)
	if err != nil {
		t.Fatal(err)
	}
	catB, err := set.Load(ctx, srcB)
	if err != nil {
		t.Fatal(err)
	}

	if catA == catB {
		t.Error("two databases were given one catalog, so a constraint name from one would be read against the other's schema")
	}
	if got := passes(t, recA); got != 1 {
		t.Errorf("the first database was introspected %d times, want once", got)
	}
	if got := passes(t, recB); got != 1 {
		t.Errorf("the second database was never introspected on its own connection (%d passes)", got)
	}
}

// The control for the test above. Without it a Set that simply loads a fresh
// catalog every call — no keying at all — passes, because two fresh catalogs are
// also two different catalogs.
func TestTwoIndependentlyBuiltSourcesOverOneHandleShareOneCatalog(t *testing.T) {
	ctx := context.Background()
	handle := new(int)
	rec := recorder(oneTable(), 2)
	first := identified{Recorder: rec, handle: handle}
	second := identified{Recorder: rec, handle: handle}

	var set Set
	catA, err := set.Load(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	catB, err := set.Load(ctx, second)
	if err != nil {
		t.Fatal(err)
	}

	if catA != catB {
		t.Error("two sources over one handle were given two catalogs, so the same database was read twice and can now be described two ways")
	}
	if got := passes(t, rec); got != 1 {
		t.Errorf("one database was introspected %d times, want once", got)
	}
}

// Two declarers over one handle at once. Both read — the load is outside the
// lock on purpose — and the re-scan before the append is what makes the second
// one back drop its own catalog and take the first one's. Without it the Set
// holds two entries for one handle, the two declarers hold two catalogs with two
// negative caches and two reload floors, and which schema a repository consults
// depends on which goroutine appended first.
func TestTwoGoroutinesDeclaringOverOneHandleEndUpWithOneCatalog(t *testing.T) {
	ctx := context.Background()
	handle := new(int)
	var arrive sync.WaitGroup
	arrive.Add(2)

	srcs := []gated{
		{identified{Recorder: recorder(oneTable(), 1), handle: handle}, &arrive, new(sync.Once)},
		{identified{Recorder: recorder(oneTable(), 1), handle: handle}, &arrive, new(sync.Once)},
	}

	var set Set
	got := make([]Catalog, len(srcs))
	errs := make([]error, len(srcs))
	var wg sync.WaitGroup
	for i, source := range srcs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = set.Load(ctx, source)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("declarer %d: %v", i, err)
		}
	}

	if got[0] != got[1] {
		t.Error("two goroutines declaring over one handle were given two catalogs, so one database is now described twice and a reload through one is invisible to the other")
	}
	found, ok := set.For(srcs[0])
	if !ok || found != got[0] {
		t.Error("the Set answers a catalog neither declarer holds, so which schema a repository consults depends on which goroutine appended first")
	}
	set.mu.Lock()
	n := len(set.entries)
	set.mu.Unlock()
	if n != 1 {
		t.Errorf("the Set holds %d entries for one handle, want 1", n)
	}

	// The control. Both goroutines have to have really been inside the load: a
	// recorder that saw nothing means one of them short-circuited on the first
	// scan and the assertions above proved nothing about the second. One pass
	// each is what the unlocked load makes them run — asserting a single pass
	// between them would fail against correct code.
	for i, source := range srcs {
		if got := passes(t, source.Recorder); got != 1 {
			t.Errorf("declarer %d ran %d introspection passes, so it never reached the load the re-scan exists to reconcile", i, got)
		}
	}
}

func TestAReadWritePairAndItsPrimaryShareOneCatalog(t *testing.T) {
	ctx := context.Background()
	handle := new(int)
	rec := recorder(oneTable(), 2)
	primary := identified{Recorder: rec, handle: handle}
	replica := identified{Recorder: recorder(oneTable(), 1), handle: new(int)}
	pair := crud.ReadWrite(primary, replica)

	var set Set
	catPair, err := set.Load(ctx, pair)
	if err != nil {
		t.Fatal(err)
	}
	catPrimary, err := set.Load(ctx, primary)
	if err != nil {
		t.Fatal(err)
	}

	if catPair != catPrimary {
		t.Error("a read/write pair and its own primary were given two catalogs, though the pair forwards the primary's identity")
	}
	if got := passes(t, rec); got != 1 {
		t.Errorf("the primary was introspected %d times, want once", got)
	}
}

// The control for the test above: keying on the pair value rather than on the
// identity it forwards would still pass that one, because a pair equals itself.
func TestAReadWritePairOverAnotherPrimaryGetsItsOwnCatalog(t *testing.T) {
	ctx := context.Background()
	replica := identified{Recorder: recorder(oneTable(), 1), handle: new(int)}
	pairA := crud.ReadWrite(identified{Recorder: recorder(oneTable(), 1), handle: new(int)}, replica)
	pairB := crud.ReadWrite(identified{Recorder: recorder(oneTable(), 1), handle: new(int)}, replica)

	var set Set
	catA, err := set.Load(ctx, pairA)
	if err != nil {
		t.Fatal(err)
	}
	catB, err := set.Load(ctx, pairB)
	if err != nil {
		t.Fatal(err)
	}
	if catA == catB {
		t.Error("two pairs over two different primaries share one catalog, so a write to one database is checked against the other's schema")
	}
}

// The exact difference between crud.KeyOf and a src.(crud.Identified) test.
//
// A crud.ReadWrite pair *is* Identified and answers nil when its primary is not.
// Under the interface test both pairs below key on nil, crud.SameDataSource(nil,
// nil) is false, and so neither entry is ever found again: the two catalogs come
// out different — the first half passes — while every lookup misses and every
// Load re-introspects. That is what the pass counts here catch.
func TestTwoReadWritePairsOverUnidentifiedPrimariesDoNotCollide(t *testing.T) {
	ctx := context.Background()
	recA, recB := recorder(oneTable(), 2), recorder(oneTable(), 2)
	replica := crudtest.Postgres()
	pairA := crud.ReadWrite(recA, replica)
	pairB := crud.ReadWrite(recB, replica)

	for i, pair := range []crud.Source{pairA, pairB} {
		id, ok := pair.(crud.Identified)
		if !ok {
			t.Fatalf("pair %d is not crud.Identified, so this test no longer pins the difference it was written for", i)
		}
		if id.DataSource() != nil {
			t.Fatalf("pair %d names a datasource, so its primary is identified after all and this test proves nothing", i)
		}
		if crud.KeyOf(pair) == nil {
			t.Fatalf("crud.KeyOf answered nil for pair %d — every such pair would key on nil and collide", i)
		}
	}

	var set Set
	catA, err := set.Load(ctx, pairA)
	if err != nil {
		t.Fatal(err)
	}
	catB, err := set.Load(ctx, pairB)
	if err != nil {
		t.Fatal(err)
	}
	if catA == catB {
		t.Error("two pairs over two different unidentified primaries share one catalog")
	}

	// The half that fails under the interface test: the same pair, asked twice.
	again, err := set.Load(ctx, pairA)
	if err != nil {
		t.Fatal(err)
	}
	if again != catA {
		t.Error("the same pair got a second catalog, so its entry was stored under a key nothing can find again")
	}
	if got := passes(t, recA); got != 1 {
		t.Errorf("one pair was introspected %d times, want once", got)
	}
}

// §16's open question, answered where the catalog can see it: the recorder needs
// no DataSource(). crud.KeyOf takes a source that cannot name its database at
// face value, so a recorder keys as itself and two recorders are two catalogs.
//
// Self-controlling in both directions, and the pass count is what makes that
// true. A Set that refused every unidentified source fails the first half; one
// that merged them fails it too; one that never remembers anything fails the
// second half on the count rather than on identity.
func TestTheRecorderKeysAsItselfSoTheProbeHasAUnitTestSeam(t *testing.T) {
	ctx := context.Background()
	recA, recB := recorder(oneTable(), 2), recorder(oneTable(), 1)

	if _, ok := any(recA).(crud.Identified); ok {
		t.Fatal("the recorder now names a datasource — that changes what crud.InTx does over it, and D-041 says why the answer was no")
	}

	var set Set
	catA, err := set.Load(ctx, recA)
	if err != nil {
		t.Fatal(err)
	}
	catB, err := set.Load(ctx, recB)
	if err != nil {
		t.Fatal(err)
	}
	if catA == catB {
		t.Error("two recorders share one catalog, so a unit test over two fake databases would read one schema for both")
	}

	again, err := set.Load(ctx, recA)
	if err != nil {
		t.Fatal(err)
	}
	if again != catA {
		t.Error("the same recorder got a second catalog")
	}
	if got := passes(t, recA); got != 1 {
		t.Errorf("one recorder was introspected %d times, want once", got)
	}
}

func TestAnUncomparableHandleIsRefusedRatherThanPanicking(t *testing.T) {
	weird := []int{1, 2, 3}
	for _, tc := range []struct {
		name   string
		source func(*crudtest.Recorder) crud.Source
	}{
		// The handle itself is a slice: reflect calls the type uncomparable and
		// crud.SameDataSource answers false without ever comparing.
		{"an uncomparable handle", func(r *crudtest.Recorder) crud.Source {
			return identified{Recorder: r, handle: weird}
		}},
		// The source names no database, so it *is* the key — and its type is a
		// pointer beside an interface, which reflect.Type calls comparable even
		// though the interface contains a slice. SameDataSource inspects the value
		// recursively and refuses it without evaluating a panicking ==.
		{"a source whose own type only looks comparable", func(r *crudtest.Recorder) crud.Source {
			return awkward{Recorder: r, payload: weird}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			rec := recorder(oneTable(), 1)

			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("%s panicked instead of being refused: %v", tc.name, p)
				}
			}()

			var set Set
			cat, err := set.Load(ctx, tc.source(rec))
			if !errors.Is(err, ErrUncomparableHandle) {
				t.Fatalf("loading over %s answered %v, want ErrUncomparableHandle", tc.name, err)
			}
			if cat != nil {
				t.Error("a refused load still handed back a catalog")
			}
			if n := len(rec.Statements()); n != 0 {
				t.Errorf("the refusal came after %d statements had already run — it has to happen before any of them", n)
			}
		})
	}
}

// The control. Without it a Set that refused every handle passes the test above,
// and the second lookup is what says the refusal earns its keep: a stored
// uncomparable key would never be matched again, so the failure it prevents is a
// catalog that re-reads on every call and looks like it is working.
func TestAComparableHandleIsAcceptedAndFoundAgain(t *testing.T) {
	ctx := context.Background()
	rec := recorder(oneTable(), 2)
	source := identified{Recorder: rec, handle: new(int)}

	var set Set
	cat, err := set.Load(ctx, source)
	if err != nil {
		t.Fatalf("a comparable handle was refused: %v", err)
	}
	again, err := set.Load(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if cat != again {
		t.Error("the same handle got two catalogs")
	}
	if got := passes(t, rec); got != 1 {
		t.Errorf("the handle was introspected %d times, want once", got)
	}

	found, ok := set.For(source)
	if !ok || found != cat {
		t.Error("For could not find a catalog Load had just stored")
	}
	if got := passes(t, rec); got != 1 {
		t.Errorf("For issued statements — a lookup must do no I/O (%d passes)", got)
	}
}
