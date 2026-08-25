package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shardit-io/vv/crud/crudtest"
)

// A rolling migration adds a constraint while the process runs and the
// classifier meets a name the catalog has never heard of. Reloading once is
// right; reloading on every failed write is a full introspection pass per
// refused INSERT, which is the loop this phase exists to close.
//
// Every test here drives a fake clock, because the alternative is sleeping and a
// test that sleeps is a test that is flaky on a loaded machine.

// frozen returns a loaded catalog whose clock the test moves by hand, together
// with the recorder underneath it and a function that advances the clock.
func frozen(t *testing.T, rec *crudtest.Recorder) (*loaded, func(time.Duration)) {
	t.Helper()
	cat, err := Load(context.Background(), rec)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := cat.(*loaded)
	if !ok {
		t.Fatalf("Load returned %T, and these tests drive the clock of the catalog it returns", cat)
	}
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	return c, func(d time.Duration) { now = now.Add(d) }
}

func TestALookupIssuesNoStatement(t *testing.T) {
	rec := recorder(oneTable(), 2)
	cat, _ := frozen(t, rec)
	before := len(rec.Statements())

	for range 1000 {
		cat.Table("rows")
		cat.Table("nothing")
		cat.Constraint("rows", "rows_pkey")
		cat.Constraint("rows", "nothing")
	}
	if got := len(rec.Statements()); got != before {
		t.Errorf("four thousand lookups sent %d statements — a loaded catalog does no I/O", got-before)
	}

	// The control. Without it a catalog wired to a recorder that is never asked
	// anything at all passes the assertion above, and the test would be
	// measuring a dead loader rather than a context-free lookup.
	if err := cat.Reload(context.Background(), "rows", "nothing"); err != nil {
		t.Fatal(err)
	}
	if got := len(rec.Statements()); got != before+pgPass {
		t.Errorf("a Reload sent %d statements, want one %d-statement pass — the recorder was never going to answer anyway",
			got-before, pgPass)
	}
}

// The phase's own control case: an unknown constraint name does not
// re-introspect in a loop.
func TestTheSameUnknownNameDoesNotReintrospectInALoop(t *testing.T) {
	ctx := context.Background()
	rec := recorder(oneTable(), 60)
	cat, _ := frozen(t, rec)
	before := passes(t, rec)

	// The first control: assert one, not "at least none". A Reload that never
	// reads anything passes the headline trivially.
	if err := cat.Reload(ctx, "rows", "nope"); err != nil {
		t.Fatal(err)
	}
	if got := passes(t, rec) - before; got != 1 {
		t.Fatalf("the first Reload ran %d passes, want exactly one", got)
	}

	for range 50 {
		if err := cat.Reload(ctx, "rows", "nope"); err != nil {
			t.Fatal(err)
		}
	}
	if got := passes(t, rec) - before; got != 1 {
		t.Errorf("fifty-one Reloads of one unknown name ran %d passes — a deploy that renames a constraint would turn every failed write into a full introspection", got)
	}
}

// The second control for the test above. A permanent negative cache is
// indistinguishable from a correct one until the window is allowed to close, and
// a permanent one breaks the rolling migration the cache exists for while the
// headline stays green.
func TestOnceTheWindowPassesTheCatalogReadsAgain(t *testing.T) {
	ctx := context.Background()
	rec := recorder(oneTable(), 60)
	cat, advance := frozen(t, rec)
	before := passes(t, rec)

	if err := cat.Reload(ctx, "rows", "nope"); err != nil {
		t.Fatal(err)
	}
	advance(minBackoff + reloadFloor)
	if err := cat.Reload(ctx, "rows", "nope"); err != nil {
		t.Fatal(err)
	}
	if got := passes(t, rec) - before; got != 2 {
		t.Errorf("after the window closed the catalog ran %d passes in total, want two — the second one is the reload a rolling migration needs", got)
	}

	// And the interval grew: the floor is open again but the name's own window
	// is not, so the two guards are genuinely two.
	advance(reloadFloor + time.Millisecond)
	if err := cat.Reload(ctx, "rows", "nope"); err != nil {
		t.Fatal(err)
	}
	if got := passes(t, rec) - before; got != 2 {
		t.Errorf("the name was asked about again after %v, so its backoff did not grow (%d passes)", reloadFloor, got)
	}
}

// The loop the per-name entry does not close. Fifty *different* unknown names
// arrive from one bulk write against a stale catalog, and a per-name cache lets
// every one of them start a fresh pass a millisecond after the last.
func TestManyDistinctUnknownNamesDoNotReintrospectOnceEach(t *testing.T) {
	ctx := context.Background()
	rec := recorder(oneTable(), 200)
	cat, _ := frozen(t, rec)
	before := passes(t, rec)

	for i := range 50 {
		if err := cat.Reload(ctx, "rows", name(i)); err != nil {
			t.Fatal(err)
		}
	}
	if got := passes(t, rec) - before; got != 1 {
		t.Errorf("fifty distinct unknown names ran %d passes within one floor window, want one", got)
	}
}

// The control for the test above. Without it a floor of forever — a catalog that
// reloads once and never again — passes it, and that catalog can never see a
// migration at all.
func TestOnceTheFloorLiftsADifferentNameReadsAgain(t *testing.T) {
	ctx := context.Background()
	rec := recorder(oneTable(), 200)
	cat, advance := frozen(t, rec)
	before := passes(t, rec)

	for i := range 5 {
		if err := cat.Reload(ctx, "rows", name(i)); err != nil {
			t.Fatal(err)
		}
		advance(reloadFloor + time.Millisecond)
	}
	if got := passes(t, rec) - before; got != 5 {
		t.Errorf("five distinct names, each after the floor lifted, ran %d passes, want five", got)
	}
}

// A name that turns up resets its backoff, so a constraint that was added and
// then renamed again is not stuck behind an interval that grew while it was
// missing.
func TestAReloadThatFindsTheNameResetsTheBackoff(t *testing.T) {
	ctx := context.Background()
	before, after := oneTable(), oneTable()
	after.constraints = append(after.constraints, pgConstraintRow("rows", "arrived", "u", 1, "id"))

	for _, tc := range []struct {
		name      string
		third     pgSchema // what the third pass finds
		wantExtra int      // passes after the window that only a reset opens
	}{
		{"the name turns up", after, 1},
		{"the name stays missing", before, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres()
			before.push(rec, 2)
			tc.third.push(rec, 1)
			before.push(rec, 10)

			cat, advance := frozen(t, rec)
			base := passes(t, rec)

			// Two misses, so the name's own window has grown past the floor.
			if err := cat.Reload(ctx, "rows", "arrived"); err != nil {
				t.Fatal(err)
			}
			advance(minBackoff + time.Millisecond)
			if err := cat.Reload(ctx, "rows", "arrived"); err != nil {
				t.Fatal(err)
			}
			advance(minBackoff*backoffScale + time.Millisecond)

			// The third pass is the one that differs between the two cases.
			if err := cat.Reload(ctx, "rows", "arrived"); err != nil {
				t.Fatal(err)
			}
			if got := passes(t, rec) - base; got != 3 {
				t.Fatalf("the setup ran %d passes, want three", got)
			}

			// Past the floor, short of the interval a third miss would have
			// armed. Only a catalog that forgot the name reads here.
			advance(reloadFloor + time.Millisecond)
			if err := cat.Reload(ctx, "rows", "arrived"); err != nil {
				t.Fatal(err)
			}
			if got := passes(t, rec) - base - 3; got != tc.wantExtra {
				t.Errorf("after the third pass %s, the next Reload ran %d further passes, want %d",
					tc.name, got, tc.wantExtra)
			}
		})
	}
}

// A failed pass keeps the schema it already had. The alternative is a database
// that went down leaving the process with no schema at all, which is worse than
// leaving it with yesterday's.
func TestAFailedReloadKeepsTheSchemaAndSaysSo(t *testing.T) {
	ctx := context.Background()
	down := errors.New("server closed the connection unexpectedly")
	rec := crudtest.Postgres()
	oneTable().push(rec, 1)
	rec.Push(crudtest.Result{Err: down})

	cat, advance := frozen(t, rec)
	err := cat.Reload(ctx, "rows", "nope")
	if !errors.Is(err, ErrIntrospection) || !errors.Is(err, down) {
		t.Fatalf("a failed reload answered %v, want an error wrapping both ErrIntrospection and the driver's own", err)
	}
	if _, ok := cat.Table("rows"); !ok {
		t.Error("a failed reload threw away the schema the catalog already had")
	}

	// And the floor was armed anyway: a database that is down must not turn
	// every failed write into a failed introspection pass.
	before := len(rec.Statements())
	if err := cat.Reload(ctx, "rows", "other"); err != nil {
		t.Fatal(err)
	}
	if got := len(rec.Statements()); got != before {
		t.Errorf("a second reload ran %d statements straight after a failed one", got-before)
	}
	advance(reloadFloor + time.Millisecond)
	if err := cat.Reload(ctx, "rows", "other"); err != nil {
		t.Fatal(err)
	}
	if len(rec.Statements()) == before {
		t.Error("the floor never lifted, so a database that came back up would never be read again")
	}
}

func name(i int) string { return "missing_" + string(rune('a'+i%26)) + string(rune('a'+i/26)) }

// The far end of the doubling. That the interval grows is pinned above; that it
// stops growing is the other half, and nothing else asks for it. A backoff with
// no ceiling reaches days after a couple of dozen misses, so a name that has
// been missing since this morning is next asked about next week — and the
// rolling migration the negative cache exists to serve is the one case it then
// never sees.
func TestTheBackoffStopsDoublingAtTheCeiling(t *testing.T) {
	ctx := context.Background()
	rec := recorder(oneTable(), 40)
	cat, advance := frozen(t, rec)
	base := passes(t, rec)

	// Enough misses that an unclamped doubling has overshot: the tenth arms
	// 512s, and the ceiling is what turns that back into 5 min.
	wait := minBackoff
	for i := range 10 {
		if i > 0 {
			advance(wait + time.Millisecond)
			wait = min(wait*backoffScale, maxBackoff)
		}
		if err := cat.Reload(ctx, "rows", "nope"); err != nil {
			t.Fatal(err)
		}
	}
	if got := passes(t, rec) - base; got != 10 {
		t.Fatalf("the setup ran %d passes, want ten — each miss must arm the interval it then waits out", got)
	}

	// The control. Short of the ceiling nothing reads, so the window this test
	// closes is a real one and not an entry that stopped arming — that catalog
	// would read at the ceiling too and pass the assertion below for the wrong
	// reason.
	advance(maxBackoff - reloadFloor)
	if err := cat.Reload(ctx, "rows", "nope"); err != nil {
		t.Fatal(err)
	}
	if got := passes(t, rec) - base; got != 10 {
		t.Errorf("the name was asked about %v after the tenth miss (%d passes) — its window is not the ceiling", maxBackoff-reloadFloor, got)
	}

	advance(reloadFloor + time.Millisecond)
	if got := passes(t, rec) - base; got != 10 {
		t.Fatalf("advancing the clock ran %d passes on its own", got)
	}
	if err := cat.Reload(ctx, "rows", "nope"); err != nil {
		t.Fatal(err)
	}
	if got := passes(t, rec) - base; got != 11 {
		t.Errorf("after %v the tenth miss's window had still not closed (%d passes) — the interval doubled past the ceiling", maxBackoff, got)
	}
}
