package security_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shardit-io/ordo/crud"
	"github.com/shardit-io/ordo/crud/crudtest"
	"github.com/shardit-io/ordo/repo/basic"
	"github.com/shardit-io/ordo/repo/decorators/security"
)

func ptrTo[T any](v T) *T { return &v }

// sqlKinds lists the first word of every statement that reached the database,
// which is what a "nothing was written" assertion is really about.
func sqlKinds(rec *crudtest.Recorder) []string {
	out := make([]string, 0, len(rec.SQL()))
	for _, s := range rec.SQL() {
		verb, _, _ := strings.Cut(strings.TrimSpace(s), " ")
		out = append(out, verb)
	}
	return out
}

func wrote(rec *crudtest.Recorder, verb string) bool {
	for _, v := range sqlKinds(rec) {
		if v == verb {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// failing closed

var errNoPrincipal = errors.New("the principal could not be resolved")

// A scope that cannot be computed is the request of a caller nobody could
// identify. Every entry point has to refuse it — an operation that treats "no
// scope yet" as "no scope needed" runs unfiltered, which is the worst possible
// reading of a failure.
func TestAScopeThatFailsClosesEveryDoor(t *testing.T) {
	ctx := context.Background()
	broken := security.Policy[Doc, int64]{
		Scope: func(context.Context) (crud.Predicate, error) { return nil, errNoPrincipal },
	}

	for _, tc := range []struct {
		name string
		call func(*testing.T, crud.Repo[Doc, int64, DocUpdate]) error
	}{
		{"GetByID", func(t *testing.T, r crud.Repo[Doc, int64, DocUpdate]) error {
			d, err := r.GetByID(ctx, 1)
			if d != (Doc{}) {
				t.Errorf("a row came back from a call that failed: %+v", d)
			}
			return err
		}},
		{"Get", func(t *testing.T, r crud.Repo[Doc, int64, DocUpdate]) error {
			page, err := r.Get(ctx)
			if len(page.Items) != 0 {
				t.Errorf("%d rows came back from a call that failed", len(page.Items))
			}
			return err
		}},
		{"GetAll", func(t *testing.T, r crud.Repo[Doc, int64, DocUpdate]) error {
			items, err := r.GetAll(ctx)
			if len(items) != 0 {
				t.Errorf("%d rows came back from a call that failed", len(items))
			}
			return err
		}},
		{"Count", func(t *testing.T, r crud.Repo[Doc, int64, DocUpdate]) error {
			n, err := r.Count(ctx)
			if n != 0 {
				t.Errorf("count = %d from a call that failed", n)
			}
			return err
		}},
		{"Exists", func(t *testing.T, r crud.Repo[Doc, int64, DocUpdate]) error {
			ok, err := r.Exists(ctx)
			if ok {
				t.Error("exists answered true from a call that failed")
			}
			return err
		}},
		{"Update", func(t *testing.T, r crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := r.Update(ctx, 1, DocUpdate{Title: ptrTo("new")})
			return err
		}},
		{"Delete", func(t *testing.T, r crud.Repo[Doc, int64, DocUpdate]) error {
			n, err := r.Delete(ctx, 1)
			if n != 0 {
				t.Errorf("%d rows deleted by a call that failed", n)
			}
			return err
		}},
		{"DeleteAll", func(t *testing.T, r crud.Repo[Doc, int64, DocUpdate]) error {
			n, err := r.DeleteAll(ctx, crud.Where(crud.Eq("Title", "junk")))
			if n != 0 {
				t.Errorf("%d rows deleted by a call that failed", n)
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows(docRow(1, 7, "a")))
			err := tc.call(t, Docs.Bind(rec, security.Gate(broken)))
			if !errors.Is(err, errNoPrincipal) {
				t.Fatalf("err = %v, want the scope's own error", err)
			}
			if n := len(rec.Statements()); n != 0 {
				t.Fatalf("%d statements ran without a scope to narrow them: %v", n, rec.SQL())
			}
		})
	}
}

// Save is the one call a scope cannot reach — an upsert has no WHERE clause —
// so the policy's own check is what has to hold the line. Without a principal
// nothing is written, whether or not the payload carries a key.
func TestSaveWithoutAPrincipalWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  Doc
	}{
		{"a create", Doc{TenantID: 8, Title: "sneaky"}},
		{"an overwrite", Doc{ID: 1, TenantID: 8, Title: "sneaky"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows(docRow(1, 8, "theirs")))
			d := tc.doc

			err := gated(rec).Save(context.Background(), &d)

			if !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if wrote(rec, "INSERT") {
				t.Fatalf("a row was written for a caller with no principal: %v", rec.SQL())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// an out-of-scope row is absent, not forbidden

// Answering 403 for somebody else's id confirms that the id exists, which is
// exactly the fact the scope is there to hide. Every lookup has to behave as if
// the row simply is not there.
func TestAnIDInAnotherTenantIsInvisibleRatherThanForbidden(t *testing.T) {
	ctx := withTenant(context.Background(), 7)

	t.Run("GetByID", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows())
		_, err := gated(rec).GetByID(ctx, 42)
		mustBeMissingNotForbidden(t, err)
	})

	t.Run("Update", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows())
		_, err := gated(rec).Update(ctx, 42, DocUpdate{Title: ptrTo("new")})
		mustBeMissingNotForbidden(t, err)
		if wrote(rec, "UPDATE") {
			t.Fatalf("a row outside the scope was updated: %v", rec.SQL())
		}
	})

	t.Run("Delete", func(t *testing.T) {
		// Deleting is not a lookup, so there is nothing to leak and nothing to
		// refuse: the statement runs, narrowed, and removes nothing.
		rec := crudtest.Postgres().Push(crudtest.Rows())
		n, err := gated(rec).Delete(ctx, 42)
		if err != nil || n != 0 {
			t.Fatalf("n = %d, err = %v; want a delete that quietly matched nothing", n, err)
		}
		if got := crudtest.Normalize(rec.Last().SQL); got != `DELETE FROM "docs" WHERE ("tenant_id" = $1 AND "id" IN ($2))` {
			t.Fatalf("sql = %s, want the tenant in the statement", got)
		}
	})
}

func mustBeMissingNotForbidden(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if errors.Is(err, crud.ErrForbidden) {
		t.Fatalf("err = %v: a denial tells the caller the row exists", err)
	}
}

// ---------------------------------------------------------------------------
// immutable fields

// An update DTO that defines a frozen field is refused for defining it, not for
// changing it: "I am sending the tenant I already have" is how a caller probes
// whether the field is writable, and it must not get an answer.
func TestAFrozenFieldIsRefusedOnUpdateEvenWhenTheValueIsUnchanged(t *testing.T) {
	ctx := withTenant(context.Background(), 7)
	rec := crudtest.Postgres().Push(crudtest.Rows(docRow(1, 7, "old")))

	_, err := gated(rec).Update(ctx, 1, DocUpdate{TenantID: ptrTo(int64(7))}) // the row's own tenant

	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if !strings.Contains(err.Error(), "TenantID") {
		t.Fatalf("err = %v; a denial has to name the field it refused", err)
	}
	if n := len(rec.Statements()); n != 0 {
		t.Fatalf("%d statements ran before the frozen field was noticed: %v", n, rec.SQL())
	}
}

// A Save carries the whole row, so every frozen column is always "defined" and
// the same rule would reject every update. There the test is the value: an
// unchanged tenant goes through, a changed one does not.
func TestSaveJudgesAFrozenFieldByItsValue(t *testing.T) {
	ctx := withTenant(context.Background(), 7)

	t.Run("unchanged goes through", func(t *testing.T) {
		rec := crudtest.Postgres().Push(
			crudtest.Rows(docRow(1, 7, "old")), // the gate reads the row it is replacing
			crudtest.Rows(docRow(1, 7, "new")), // RETURNING
		)
		d := Doc{ID: 1, TenantID: 7, Title: "new"}
		if err := gated(rec).Save(ctx, &d); err != nil {
			t.Fatal(err)
		}
		if !wrote(rec, "INSERT") {
			t.Fatalf("the upsert never ran: %v", rec.SQL())
		}
	})

	t.Run("changed is refused", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows(docRow(1, 7, "old")))
		d := Doc{ID: 1, TenantID: 8, Title: "moved"}

		err := gated(rec).Save(ctx, &d)

		if !errors.Is(err, security.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if wrote(rec, "INSERT") {
			t.Fatalf("a row was moved into another tenant: %v", rec.SQL())
		}
	})
}

// ---------------------------------------------------------------------------
// Inspect

// rejecting builds a policy whose per-row check refuses one particular title,
// counting the rows it was asked about.
func rejecting(title string, seen *int) security.Policy[Doc, int64] {
	return security.Policy[Doc, int64]{
		Inspect: func(_ context.Context, a security.Action, d *Doc) error {
			*seen++
			if d.Title == title {
				return security.Denied(a, "that document is sealed")
			}
			return nil
		},
	}
}

// Inspect is a veto over the whole operation, not a filter applied to the rows
// it likes: one refused row means the batch does not happen at all.
func TestInspectAbortsTheWholeCall(t *testing.T) {
	ctx := context.Background()

	t.Run("Update never reaches the write", func(t *testing.T) {
		seen := 0
		rec := crudtest.Postgres().Push(crudtest.Rows(docRow(1, 7, "sealed")))
		repo := Docs.Bind(rec, security.Gate(rejecting("sealed", &seen)))

		_, err := repo.Update(ctx, 1, DocUpdate{Title: ptrTo("new")})

		if !errors.Is(err, security.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if wrote(rec, "UPDATE") {
			t.Fatalf("the row was written after the inspection refused it: %v", rec.SQL())
		}
	})

	t.Run("Delete spares the rows it was allowed to take", func(t *testing.T) {
		seen := 0
		rec := crudtest.Postgres().
			Push(crudtest.Rows(docRow(1, 7, "fine"), docRow(2, 7, "sealed"))).
			ExecResult(crud.Result{RowsAffected: 2})
		repo := Docs.Bind(rec, security.Gate(rejecting("sealed", &seen)))

		n, err := repo.Delete(ctx, 1, 2)

		if !errors.Is(err, security.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if n != 0 {
			t.Fatalf("the call reported %d rows deleted", n)
		}
		if wrote(rec, "DELETE") {
			t.Fatalf("one sealed row in the batch still let the other be deleted: %v", rec.SQL())
		}
	})

	t.Run("DeleteAll spares the rows it was allowed to take", func(t *testing.T) {
		seen := 0
		rec := crudtest.Postgres().
			Push(crudtest.Rows(docRow(1, 7, "fine"), docRow(2, 7, "sealed"))).
			ExecResult(crud.Result{RowsAffected: 2})
		repo := Docs.Bind(rec, security.Gate(rejecting("sealed", &seen)))

		n, err := repo.DeleteAll(ctx, crud.Where(crud.Eq("TenantID", int64(7))))

		if !errors.Is(err, security.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if n != 0 {
			t.Fatalf("the call reported %d rows deleted", n)
		}
		if wrote(rec, "DELETE") {
			t.Fatalf("one sealed row still let the rest of the table go: %v", rec.SQL())
		}
	})
}

// A page is all or nothing. Handing back the rows that passed would tell the
// caller precisely how many rows it is not allowed to see, and a caller that
// ignores the error would render a page that is quietly missing rows.
func TestInspectReadsFailsThePageInsteadOfTrimmingIt(t *testing.T) {
	ctx := context.Background()
	rows := func() crudtest.Result { return crudtest.Rows(docRow(1, 7, "fine"), docRow(2, 7, "sealed")) }

	t.Run("on", func(t *testing.T) {
		seen := 0
		policy := rejecting("sealed", &seen)
		policy.InspectReads = true
		rec := crudtest.Postgres().Push(rows())

		page, err := Docs.Bind(rec, security.Gate(policy)).Get(ctx)

		if !errors.Is(err, security.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if len(page.Items) != 0 {
			t.Fatalf("the caller was handed %d of the rows anyway: %+v", len(page.Items), page.Items)
		}
	})

	t.Run("off by default", func(t *testing.T) {
		seen := 0
		rec := crudtest.Postgres().Push(rows())

		page, err := Docs.Bind(rec, security.Gate(rejecting("sealed", &seen))).Get(ctx)

		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 2 {
			t.Fatalf("the page carries %d rows, want both", len(page.Items))
		}
		if seen != 0 {
			t.Fatalf("the row check ran %d times on a list; it is opt-in because Scope is the cheap way to filter", seen)
		}
	})

	t.Run("a single entity is always inspected", func(t *testing.T) {
		seen := 0
		rec := crudtest.Postgres().Push(crudtest.Rows(docRow(2, 7, "sealed")))

		_, err := Docs.Bind(rec, security.Gate(rejecting("sealed", &seen))).GetByID(ctx, 2)

		if !errors.Is(err, security.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden even with InspectReads off", err)
		}
		if seen != 1 {
			t.Fatalf("the row check ran %d times, want once", seen)
		}
	})
}

// ---------------------------------------------------------------------------
// a gate over a repository that already has a scope

// Note is soft-deleted, so the repository narrows every read to the live rows,
// while the gate narrows them to one tenant. Neither knows about the other.
type Note struct {
	ID        int64               `db:"id,pk,auto"`
	TenantID  int64               `db:"tenant_id"`
	Title     string              `db:"title"`
	DeletedAt crud.Opt[time.Time] `db:"deleted_at"`
}

type NoteUpdate struct {
	Title *string
}

var (
	liveNotes  = basic.Define[Note, int64, NoteUpdate]("notes", basic.Scope(crud.IsNull("DeletedAt")))
	notePolicy = security.ScopeField[Note, int64]("TenantID", tenantOf)
)

func noteRow(id, tenant int64, title string) []any { return []any{id, tenant, title, nil} }

// Two independent narrowings, both permanent, neither able to displace the
// other: the repository scope, the gate scope and the caller's own filter all
// end up in the same AND.
func TestTheGateScopeAndTheRepositoryScopeBothApply(t *testing.T) {
	ctx := withTenant(context.Background(), 7)

	t.Run("on a read", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows())
		repo := liveNotes.Bind(rec, security.Gate(notePolicy))

		if _, err := repo.GetAll(ctx, crud.Where(crud.Eq("Title", "x"))); err != nil {
			t.Fatal(err)
		}
		want := `SELECT "id", "tenant_id", "title", "deleted_at" FROM "notes" ` +
			`WHERE ("deleted_at" IS NULL AND "tenant_id" = $1 AND "title" = $2)`
		if got := crudtest.Normalize(rec.Last().SQL); got != want {
			t.Fatalf("sql  = %s\nwant = %s", got, want)
		}
	})

	t.Run("on a delete", func(t *testing.T) {
		rec := crudtest.Postgres().
			Push(crudtest.Rows(noteRow(1, 7, "mine"))). // the policy inspects the victims
			ExecResult(crud.Result{RowsAffected: 1})
		repo := liveNotes.Bind(rec, security.Gate(notePolicy))

		if _, err := repo.DeleteAll(ctx); err != nil {
			t.Fatal(err)
		}
		want := `DELETE FROM "notes" WHERE ("deleted_at" IS NULL AND "tenant_id" = $1)`
		if got := crudtest.Normalize(rec.Last().SQL); got != want {
			t.Fatalf("sql  = %s\nwant = %s", got, want)
		}
	})

	// The gate cannot see the repository's scope, so it counts as unscoped and
	// the refusal stands. Erring towards the refusal is the right way round.
	t.Run("a repository scope does not satisfy the gate's own delete guard", func(t *testing.T) {
		rec := crudtest.Postgres()
		repo := liveNotes.Bind(rec, security.Gate(security.Policy[Note, int64]{}))

		if _, err := repo.DeleteAll(context.Background()); !errors.Is(err, security.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if len(rec.Statements()) != 0 {
			t.Fatalf("a truncate reached the database: %v", rec.SQL())
		}
	})
}
