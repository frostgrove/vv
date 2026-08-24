package basic_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/crudtest"
	"github.com/shardit-io/vv/repo/basic"
)

// Note carries the optimistic lock. Update is load-then-write, so between the
// two statements somebody else can write the same row; the version column is
// what turns that from a silent lost update into a refusal.
type Note struct {
	ID      int64  `db:"id,pk,auto"`
	Title   string `db:"title"`
	Version int    `db:"version,version"`
}

type NoteUpdate struct {
	Title *string
}

var Notes = basic.Define[Note, int64, NoteUpdate]("notes")

func noteRow(id int64, title string, version int) []any { return []any{id, title, version} }

// The statement carries both halves of the lock, and neither is optional: the
// WHERE pins the row to the version it was read at, and the SET moves it on so
// that everybody else's copy is now old.
func TestUpdateChecksTheVersionItReadAndAdvancesIt(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(noteRow(1, "old", 3)), // the load
		crudtest.Rows(noteRow(1, "new", 4)), // the UPDATE ... RETURNING
	)

	got, err := Notes.Bind(rec).Update(context.Background(), 1, NoteUpdate{Title: ptr("new")})
	if err != nil {
		t.Fatal(err)
	}
	wantSQL(t, mustSQL(t, rec, 1).SQL,
		`UPDATE "notes" SET "title" = $1, "version" = "version" + 1 `+
			`WHERE ("id" = $2 AND "version" = $3) RETURNING "id", "title", "version"`)
	if v := mustSQL(t, rec, 1).Args[2]; v != 3 {
		t.Fatalf("the update was pinned to version %v, want the 3 it read", v)
	}
	if got.Version != 4 {
		t.Fatalf("the caller was handed version %d; the model has to describe the row that is now there", got.Version)
	}
}

// The whole point, stated as an outcome: when the row moved on, the write does
// not happen and the caller is told. ErrStaleVersion wraps ErrConflict, so a
// transport answers 409 without knowing what a version is.
func TestAnUpdateAgainstARowSomebodyElseChangedIsRefused(t *testing.T) {
	ctx := context.Background()

	t.Run("on a dialect with RETURNING", func(t *testing.T) {
		rec := crudtest.Postgres().Push(
			crudtest.Rows(noteRow(1, "old", 3)), // the load
			crudtest.Rows(),                     // the UPDATE matched nothing
			crudtest.Rows([]any{int64(1)}),      // ... but the row is still there
		)
		_, err := Notes.Bind(rec).Update(ctx, 1, NoteUpdate{Title: ptr("mine")})
		if !errors.Is(err, crud.ErrStaleVersion) {
			t.Fatalf("err = %v, want ErrStaleVersion", err)
		}
		if !errors.Is(err, crud.ErrConflict) {
			t.Fatal("a stale write must read as a conflict, or a transport cannot answer 409")
		}
		if errors.Is(err, crud.ErrNotFound) {
			t.Fatal("a row that is still there must not be reported as missing: the caller's answer is to retry, not to give up")
		}
	})

	t.Run("on a dialect without RETURNING", func(t *testing.T) {
		// MySQL cannot tell "no such row" from "nothing to do" by rows-affected
		// alone — except with a version column, where every matching row is
		// changed because the counter is one of the changes.
		rec := crudtest.MySQL().Push(crudtest.Rows(noteRow(1, "old", 3)))
		rec.ExecResult(crud.Result{RowsAffected: 0})
		rec.Push(crudtest.Rows([]any{int64(1)}))

		got, err := Notes.Bind(rec).Update(ctx, 1, NoteUpdate{Title: ptr("mine")})
		if !errors.Is(err, crud.ErrStaleVersion) {
			t.Fatalf("err = %v, want ErrStaleVersion", err)
		}
		if got.Title != "" {
			t.Fatalf("a model came back with the refusal (%q): re-reading here hands the caller somebody else's write as if it were their own", got.Title)
		}
	})
}

// A row that is genuinely gone is a different answer from a row that moved on,
// and the caller does different things with them: give up, versus read and
// reapply.
func TestAVanishedRowIsStillNotFoundRatherThanStale(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(noteRow(1, "old", 3)), // the load
		crudtest.Rows(),                     // the UPDATE matched nothing
		crudtest.Rows(),                     // and the row is not there any more
	)
	_, err := Notes.Bind(rec).Update(context.Background(), 1, NoteUpdate{Title: ptr("new")})
	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if errors.Is(err, crud.ErrStaleVersion) {
		t.Fatal("a deleted row was reported as a version conflict, which would send the caller round a retry loop forever")
	}
}

// An update that changes nothing writes nothing — and therefore does not move
// the version either. Advancing it would invalidate everybody else's copy of a
// row that never changed.
func TestAnUpdateWithNothingToDoLeavesTheVersionAlone(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(noteRow(1, "same", 3)))

	got, err := Notes.Bind(rec).Update(context.Background(), 1, NoteUpdate{Title: ptr("same")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 3 {
		t.Fatalf("version = %d, want 3", got.Version)
	}
	if len(rec.Statements()) != 1 {
		t.Fatalf("a no-op update issued %d statements: %v", len(rec.Statements()), rec.SQL())
	}
}

// A filtered update has no single row to check, but it must still advance every
// row it touches: otherwise a stale Update somebody is holding would sail past
// this change and undo it without noticing.
func TestUpdateAllAdvancesTheVersionOfEveryRowItWrites(t *testing.T) {
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 4})

	if _, err := Notes.Bind(rec).UpdateAll(context.Background(),
		NoteUpdate{Title: ptr("bulk")}, crud.Where(crud.Eq("Title", "old"))); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL,
		`UPDATE "notes" SET "title" = $1, "version" = "version" + 1 WHERE "title" = $2`)
}

// Save is an upsert — one statement, no WHERE clause for a version to live in —
// so it cannot check the lock. What it must not do is lower it: the conflict
// clause leaves the column alone, so a Save built from a model somebody has been
// holding for a while cannot hand everyone else's stale copies a fresh licence.
func TestSaveNeverWindsTheVersionBack(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(noteRow(7, "x", 9)))

	n := Note{ID: 7, Title: "x", Version: 1}
	if err := Notes.Bind(rec).Save(context.Background(), &n); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Last().SQL, `"version" = EXCLUDED."version"`) {
		t.Fatalf("an upsert overwrote the lock with the model's own stale value: %s", rec.Last().SQL)
	}
	wantSQL(t, rec.Last().SQL,
		`INSERT INTO "notes" ("id", "title", "version") VALUES ($1, $2, $3) `+
			`ON CONFLICT ("id") DO UPDATE SET "title" = EXCLUDED."title" `+
			`RETURNING "id", "title", "version"`)
}

// Nothing changes for a model that declares no version: no extra column in the
// SET, no extra clause in the WHERE, and a missed row is still ErrNotFound.
func TestAModelWithoutAVersionIsUntouched(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(userRow(1, "a@b.c", "Ann", 30, 7)),
		crudtest.Rows(),
	)
	_, err := Users.Bind(rec).Update(context.Background(), 1, UserUpdate{Name: ptr("Anna")})
	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if strings.Contains(mustSQL(t, rec, 1).SQL, "version") {
		t.Fatalf("an unversioned model grew a version clause: %s", mustSQL(t, rec, 1).SQL)
	}
	if n := len(rec.Statements()); n != 2 {
		t.Fatalf("%d statements: an unversioned miss must not cost an extra existence check", n)
	}
}

// The other half of the loop the generator closes: with the version column left
// out, the declaration the generator produces is one Define accepts. Without
// both halves each side can look right on its own while the pair panics at
// start-up, which is exactly what shipped.
func TestTheDeclarationAGeneratorProducesForAVersionedModelIsAccepted(t *testing.T) {
	type Doc struct {
		ID      int64  `db:"id,pk,auto"`
		Title   string `db:"title"`
		Version int    `db:"version,version"`
	}
	// Exactly what cmd/vv emits now: every writable column, and not the lock.
	type DocUpdate struct {
		Title *string
	}

	if _, err := basic.TryDefine[Doc, int64, DocUpdate]("docs"); err != nil {
		t.Fatalf("the generated shape was refused: %v", err)
	}

	// And the control: name the lock and it is refused, which is why the
	// generator has to leave it out rather than merely happen to.
	type WithLock struct {
		Title   *string
		Version *int
	}
	if _, err := basic.TryDefine[Doc, int64, WithLock]("docs"); err == nil {
		t.Fatal("a DTO naming the version column was accepted — the generator's omission proves nothing")
	}
}
