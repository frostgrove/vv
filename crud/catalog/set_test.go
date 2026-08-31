package catalog

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
)

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

func TestTwoReadWritePairsOverUnidentifiedPrimariesDoNotCollide(t *testing.T) {
	ctx := context.Background()
	recA, recB := recorder(oneTable(), 2), recorder(oneTable(), 2)
	replica := crudtest.Postgres()
	pairA := crud.ReadWrite(anonymous{recorder: recA}, replica)
	pairB := crud.ReadWrite(anonymous{recorder: recB}, replica)

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

func TestTheRecorderKeysAsItselfSoTheProbeHasAUnitTestSeam(t *testing.T) {
	ctx := context.Background()
	recA, recB := recorder(oneTable(), 2), recorder(oneTable(), 1)

	identified, ok := any(recA).(crud.Identified)
	if !ok || identified.DataSource() != recA {
		t.Fatal("the recorder does not identify itself, so catalog and transaction scoping can drift")
	}
	if crud.KeyOf(recA) != recA {
		t.Fatalf("crud.KeyOf answered %v, want the recorder itself", crud.KeyOf(recA))
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
		{"an uncomparable handle", func(r *crudtest.Recorder) crud.Source {
			return identified{Recorder: r, handle: weird}
		}},

		{"a source whose own type only looks comparable", func(r *crudtest.Recorder) crud.Source {
			return awkward{anonymous: anonymous{recorder: r}, payload: weird}
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
