package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud/crudtest"
)

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

	if err := cat.Reload(context.Background(), "rows", "nothing"); err != nil {
		t.Fatal(err)
	}
	if got := len(rec.Statements()); got != before+pgPass {
		t.Errorf("a Reload sent %d statements, want one %d-statement pass — the recorder was never going to answer anyway",
			got-before, pgPass)
	}
}

func TestTheSameUnknownNameDoesNotReintrospectInALoop(t *testing.T) {
	ctx := context.Background()
	rec := recorder(oneTable(), 60)
	cat, _ := frozen(t, rec)
	before := passes(t, rec)

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

	advance(reloadFloor + time.Millisecond)
	if err := cat.Reload(ctx, "rows", "nope"); err != nil {
		t.Fatal(err)
	}
	if got := passes(t, rec) - before; got != 2 {
		t.Errorf("the name was asked about again after %v, so its backoff did not grow (%d passes)", reloadFloor, got)
	}
}

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

func TestAReloadThatFindsTheNameResetsTheBackoff(t *testing.T) {
	ctx := context.Background()
	before, after := oneTable(), oneTable()
	after.constraints = append(after.constraints, pgConstraintRow("rows", "arrived", "u", 1, "id"))

	for _, tc := range []struct {
		name      string
		third     pgSchema
		wantExtra int
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

			if err := cat.Reload(ctx, "rows", "arrived"); err != nil {
				t.Fatal(err)
			}
			advance(minBackoff + time.Millisecond)
			if err := cat.Reload(ctx, "rows", "arrived"); err != nil {
				t.Fatal(err)
			}
			advance(minBackoff*backoffScale + time.Millisecond)

			if err := cat.Reload(ctx, "rows", "arrived"); err != nil {
				t.Fatal(err)
			}
			if got := passes(t, rec) - base; got != 3 {
				t.Fatalf("the setup ran %d passes, want three", got)
			}

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

func TestTheBackoffStopsDoublingAtTheCeiling(t *testing.T) {
	ctx := context.Background()
	rec := recorder(oneTable(), 40)
	cat, advance := frozen(t, rec)
	base := passes(t, rec)

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
