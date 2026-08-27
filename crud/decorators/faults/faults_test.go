package faults_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/faults"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/errs"
)

type Doc struct {
	ID       int64  `db:"id,pk,auto"`
	TenantID int64  `db:"tenant_id"`
	Title    string `db:"title"`
	Body     string `db:"body"`
}

type DocUpdate struct {
	Title *string
	Body  *string
}

var Docs = sqlrepo.Define[Doc, int64, DocUpdate]("docs")

// AuditEntry is the second model, and it exists for one control: two tables in
// one database with a column of the same name. Without it, a decorator that
// translated every column it was handed would look correct.
type AuditEntry struct {
	ID    int64  `db:"id,pk,auto"`
	Title string `db:"title"`
}

type AuditUpdate struct{ Title *string }

var Audits = sqlrepo.Define[AuditEntry, int64, AuditUpdate]("audit_log")

// conflict is what the adapters hand this decorator: a classified fault with a
// Source and no path at all.
func conflict(table string, columns ...string) error {
	return errs.Conflict().Code(errs.CodeUnique).
		General().Code(errs.CodeUnique).Origin(errs.OriginState).
		Source(errs.Source{Table: table, Constraint: table + "_key", Columns: columns}).
		Wrapping(crud.ErrConflict).Fault()
}

func docs(rec *crudtest.Recorder) crud.Repo[Doc, int64, DocUpdate] {
	return Docs.Bind(rec, faults.Enrich[Doc, int64]())
}

// failWith refuses the next statement whichever way the repository issues it.
// A Save is an Exec on MySQL and a Query on PostgreSQL (RETURNING), and a
// recorder that only failed one of the two would leave half the verbs below
// succeeding quietly.
func failWith(rec *crudtest.Recorder, err error) *crudtest.Recorder {
	rec.Fail(err)
	for i := 0; i < 4; i++ {
		rec.Push(crudtest.Result{Err: err})
	}
	return rec
}

// saveFailing runs one Save that the recorder refuses with err, and hands back
// whatever the decorator turned it into.
func saveFailing(t *testing.T, r crud.Repo[Doc, int64, DocUpdate], rec *crudtest.Recorder, err error) *errs.Fault {
	t.Helper()
	failWith(rec, err)
	_, got := r.Save(context.Background(), &Doc{Title: "a"})
	if got == nil {
		t.Fatal("the save succeeded; nothing was enriched")
	}
	f, ok := errs.AsFault(got)
	if !ok {
		t.Fatalf("the error is not a fault: %v", got)
	}
	return f
}

// The hop [[D-043]] gives this layer: a column becomes the model field, through
// crud.Meta and never crud.Schema.
func TestAColumnBecomesAModelField(t *testing.T) {
	rec := crudtest.Postgres()
	f := saveFailing(t, docs(rec), rec, conflict("docs", "title"))

	if len(f.Violations) != 1 {
		t.Fatalf("%d violations, want the one the adapter produced", len(f.Violations))
	}
	if got := f.Violations[0].Path.String(); got != "Title" {
		t.Fatalf("the column `title` became %q, want the model field Title", got)
	}
	if f.Violations[0].Approximate {
		t.Fatal("a column that resolved exactly was marked approximate")
	}
}

// The control the hop needs, and the reason it goes through Meta: two tables in
// one database carry a `title`, and only one of them is this repository's.
func TestAColumnFromAnotherTableIsNotTranslated(t *testing.T) {
	rec := crudtest.Postgres()
	f := saveFailing(t, docs(rec), rec, conflict("audit_log", "title"))

	if got := f.Violations[0].Path; len(got) != 0 {
		t.Fatalf("a violation on audit_log named %s as a field of Doc", got)
	}
	if !f.Violations[0].Approximate {
		t.Fatal("a column this layer did not translate was not marked approximate")
	}

	// The other half, and the one that makes this a control rather than a
	// second positive: the same column name through the repository that owns
	// audit_log does translate. Without it, a decorator that translated nothing
	// would pass both legs.
	rec2 := crudtest.Postgres()
	failWith(rec2, conflict("audit_log", "title"))
	_, err := Audits.Bind(rec2, faults.Enrich[AuditEntry, int64]()).Save(context.Background(), &AuditEntry{Title: "a"})
	other, ok := errs.AsFault(err)
	if !ok {
		t.Fatalf("the audit save's error is not a fault: %v", err)
	}
	if got := other.Violations[0].Path.String(); got != "Title" {
		t.Fatalf("the same column through its own repository became %q", got)
	}
}

// A column name in `field` would be a live [[D-044]] breach: the path is the one
// thing that is rendered.
func TestAnUnresolvedColumnNeverBecomesTheField(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		wantApx bool
	}{
		{"a column the model does not declare", conflict("docs", "shadow_ban"), true},
		{"a column from another table", conflict("audit_log", "title"), true},
		{"one of two columns unresolved", conflict("docs", "title", "shadow_ban"), true},
		// No column at all is not a failure to translate. The driver named
		// nothing, so there was nothing to attempt.
		{"no column at all", conflict("docs"), false},
		{"no table either", conflict("", "title"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres()
			f := saveFailing(t, docs(rec), rec, tc.err)

			v := f.Violations[0]
			if len(v.Path) != 0 {
				t.Fatalf("an unresolved column produced the path %s", v.Path)
			}
			if v.Approximate != tc.wantApx {
				t.Fatalf("approximate = %v, want %v", v.Approximate, tc.wantApx)
			}
			if strings.Contains(v.Path.String(), "_") {
				t.Fatalf("the path looks like a column name: %s", v.Path)
			}
		})
	}
}

// §13's recommendation: one violation at the deepest common ancestor of the
// fields a composite key spans. The per-column form says "slug is not unique"
// and "tenant_id is not unique", and neither is true on its own.
func TestACompositeUniqueYieldsOneViolationAtTheCommonAncestor(t *testing.T) {
	rec := crudtest.Postgres()
	f := saveFailing(t, docs(rec), rec, conflict("docs", "tenant_id", "title"))

	if len(f.Violations) != 1 {
		t.Fatalf("a two-column key produced %d violations, want one", len(f.Violations))
	}
	v := f.Violations[0]
	if len(v.Path) != 0 {
		t.Fatalf("two flat fields have no common ancestor, so the path must be empty; it is %s", v.Path)
	}
	if v.Approximate {
		t.Fatal("the ancestor was chosen deliberately, so the violation is not approximate")
	}
	// And nothing internal came with it. Params is where a column name would
	// arrive wearing presentation ([[D-044]]).
	for k, val := range v.Params {
		t.Fatalf("the composite violation carries a param %q = %v", k, val)
	}

	// The control: the single-column case on the same repository still names
	// its field, so "empty path" is the composite answer rather than the
	// decorator giving up.
	rec2 := crudtest.Postgres()
	one := saveFailing(t, docs(rec2), rec2, conflict("docs", "title"))
	if got := one.Violations[0].Path.String(); got != "Title" {
		t.Fatalf("the single-column case became %q", got)
	}
}

// Op is what unblocks the foreign-key direction [[D-046]] defers: on PostgreSQL
// and SQLite a missing parent and a child still referring to a row are the same
// key with the same fields, and only the verb separates them.
func TestTheDecoratorSetsOpAndEntity(t *testing.T) {
	rec := crudtest.Postgres()
	f := saveFailing(t, docs(rec), rec, conflict("docs", "title"))
	if f.Op != "Save" || f.Entity != "Doc" {
		t.Fatalf("the fault says op=%q entity=%q, want Save/Doc", f.Op, f.Entity)
	}

	// The control: a layer closer to the failure that already said which
	// command this was knows better than the verb does.
	rec2 := crudtest.Postgres()
	already := errs.Conflict().Op("RegisterUser").Entity("Signup").Code(errs.CodeUnique).
		General().Code(errs.CodeUnique).Wrapping(crud.ErrConflict).Fault()
	f = saveFailing(t, docs(rec2), rec2, already)
	if f.Op != "RegisterUser" || f.Entity != "Signup" {
		t.Fatalf("the decorator overwrote op=%q entity=%q", f.Op, f.Entity)
	}
}

// A decorator that manufactured faults would turn every closed pool into a
// structured 500 that looked classified.
func TestAnErrorThatIsNotAFaultIsReturnedUnchanged(t *testing.T) {
	boom := errors.New("dial tcp 10.0.0.5:5432: connection refused")
	rec := crudtest.Postgres()
	failWith(rec, boom)

	_, err := docs(rec).Save(context.Background(), &Doc{Title: "a"})
	if !errors.Is(err, boom) {
		t.Fatalf("the error stopped matching the one the driver raised: %v", err)
	}
	if _, ok := errs.AsFault(err); ok {
		t.Fatalf("the decorator invented a fault for %v", err)
	}
}

// A fault is a value two goroutines may render at once, and [[D-042]] treats it
// as immutable. The adapter that produced it may also have handed the same
// pointer to a caller who already wrapped it.
func TestTheDecoratorDoesNotMutateTheFaultItWasGiven(t *testing.T) {
	given := conflict("docs", "title")
	original, _ := errs.AsFault(given)

	rec := crudtest.Postgres()
	got := saveFailing(t, docs(rec), rec, given)

	if got == original {
		t.Fatal("the decorator returned the fault it was given rather than a copy")
	}
	if len(original.Violations[0].Path) != 0 {
		t.Fatalf("the original's path was rewritten to %s", original.Violations[0].Path)
	}
	if original.Op != "" || original.Entity != "" {
		t.Fatalf("the original's op/entity were written to: %q/%q", original.Op, original.Entity)
	}
	// The copy still wraps what the original wrapped, or errors.Is stops
	// finding crud.ErrConflict one layer above the adapter that attached it.
	if !errors.Is(got, crud.ErrConflict) {
		t.Fatal("the copy stopped matching crud.ErrConflict")
	}
}

// [[D-030]] made mechanical: every method on the seam that can fail is
// decorated, and a verb added later reddens here rather than silently skipping
// enrichment.
func TestEveryVerbIsDecorated(t *testing.T) {
	// Each entry drives one Core method to failure and reports the fault the
	// decorator produced, so "decorated" means observed rather than declared.
	verbs := map[string]func(crud.Repo[Doc, int64, DocUpdate]) error{
		"GetByID": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := r.GetByID(context.Background(), 1)
			return err
		},
		"Get": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := r.Get(context.Background())
			return err
		},
		"GetAll": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := r.GetAll(context.Background())
			return err
		},
		"First": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := r.First(context.Background())
			return err
		},
		"Save": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := r.Save(context.Background(), &Doc{Title: "a"})
			return err
		},
		"SaveOnly": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			return r.SaveOnly(context.Background(), &Doc{Title: "a"})
		},
		"SaveAll": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			return r.SaveAll(context.Background(), []*Doc{{Title: "a"}})
		},
		"Update": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := r.Update(context.Background(), 1, DocUpdate{})
			return err
		},
		"UpdateAll": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			title := "t"
			_, err := r.UpdateAll(context.Background(), DocUpdate{Title: &title}, crud.Where(crud.Eq("ID", int64(1))))
			return err
		},
		"Aggregate": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := r.Aggregate(context.Background(), crud.Aggregate(crud.CountAll("n")))
			return err
		},
		"Delete": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := r.Delete(context.Background(), 1)
			return err
		},
		"DeleteAll": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := r.DeleteAll(context.Background(), crud.Where(crud.Eq("ID", int64(1))))
			return err
		},
		"Count": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := r.Count(context.Background())
			return err
		},
		"Exists": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			_, err := r.Exists(context.Background())
			return err
		},
		"Tx": func(r crud.Repo[Doc, int64, DocUpdate]) error {
			return r.Tx(context.Background(), func(ctx context.Context) error {
				return r.SaveOnly(ctx, &Doc{Title: "a"})
			})
		},
	}

	// The control, and the reason this is reflection rather than a list: the
	// seam's own method set decides what has to be here. A verb added to
	// crud.Core reddens this line.
	seam := reflect.TypeOf((*crud.Core[Doc, int64])(nil)).Elem()
	for i := 0; i < seam.NumMethod(); i++ {
		name := seam.Method(i).Name
		if name == "Meta" {
			continue // it returns no error, so there is nothing to enrich
		}
		if _, ok := verbs[name]; !ok {
			t.Fatalf("crud.Core has a verb %q that this test does not drive, so nothing says whether it is decorated", name)
		}
	}
	if len(verbs) != seam.NumMethod()-1 {
		t.Fatalf("the table drives %d verbs and the seam has %d besides Meta", len(verbs), seam.NumMethod()-1)
	}

	for name, drive := range verbs {
		t.Run(name, func(t *testing.T) {
			rec := crudtest.Postgres()
			failWith(rec, conflict("docs", "title"))
			err := drive(docs(rec))
			if err == nil {
				t.Fatal("the verb succeeded, so nothing reached the decorator")
			}
			f, ok := errs.AsFault(err)
			if !ok {
				t.Fatalf("the error is not a fault: %v", err)
			}
			if f.Op == "" {
				t.Fatal("the fault carries no Op, so this verb is not decorated")
			}
			if got := f.Violations[0].Path.String(); got != "Title" {
				t.Fatalf("the column was not translated on this verb: path = %q", got)
			}
		})
	}
}

// The decorator is the innermost middleware, so the gate's own refusal passes
// through with nothing added: a 403 is not a driver error.
func TestTheGatesRefusalPassesThroughUnenriched(t *testing.T) {
	rec := crudtest.Postgres()
	r := Docs.Bind(rec, security.Gate(security.ReadOnly[Doc, int64]()), faults.Enrich[Doc, int64]())

	err := r.SaveOnly(context.Background(), &Doc{Title: "a"})
	if !errors.Is(err, crud.ErrForbidden) {
		t.Fatalf("the gate's refusal became %v", err)
	}
	if _, ok := errs.AsFault(err); ok {
		t.Fatal("the decorator turned a denial into a fault")
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("the refused write still reached the database: %v", rec.SQL())
	}
}
