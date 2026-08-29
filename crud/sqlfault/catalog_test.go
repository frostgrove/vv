package sqlfault

import (
	"slices"
	"testing"

	"github.com/frostgrove/vv/crud/catalog"
	"github.com/frostgrove/vv/errs"
)

// fakeColumns is a schema lookup that records whether it was asked. The
// recording is what the negative half of the test below needs: "the driver's
// column survived" passes for a classifier that consulted the catalog and got
// the same answer back.
type fakeColumns struct {
	cols   []string
	asked  int
	lastTb string
	lastCn string
}

func (this *fakeColumns) ConstraintColumns(table, constraint string) ([]string, bool) {
	this.asked++
	this.lastTb, this.lastCn = table, constraint
	if this.cols == nil {
		return nil, false
	}
	return this.cols, true
}

// A PostgreSQL unique violation names the constraint and the table and no
// column — every entry in the corpus records exactly that — so the columns a
// client would want are the ones only the schema can supply.
func TestTheColumnsSPIFillsWhatTheDriverDidNotName(t *testing.T) {
	err := duplicateKey() // 23505, ConstraintName and TableName, no ColumnName

	cat := &fakeColumns{cols: []string{"tenant_id", "slug"}}
	f, ok := New("postgres", WithColumns(cat)).Classify(err)
	if !ok {
		t.Fatal("no fault")
	}
	want := []string{"tenant_id", "slug"}
	if !slices.Equal(f.Violations[0].Source.Columns, want) {
		t.Fatalf("Source.Columns = %v, want %v in key order", f.Violations[0].Source.Columns, want)
	}
	if !slices.Equal(f.Detail.Columns, want) {
		t.Fatalf("Detail.Columns = %v, want %v", f.Detail.Columns, want)
	}
	if cat.lastTb != "users" || cat.lastCn != "users_email_key" {
		t.Fatalf("the lookup was keyed on %q/%q, want the table and constraint the driver named", cat.lastTb, cat.lastCn)
	}

	// The first control: the identical error with nothing wired leaves the list
	// nil — nil rather than empty, because "not known" must not read as "no
	// columns" — while the lookup key was there all along.
	f, ok = New("postgres").Classify(err)
	if !ok {
		t.Fatal("no fault")
	}
	if f.Violations[0].Source.Columns != nil {
		t.Fatalf("Source.Columns = %#v with no catalog wired: the fill did not come from the catalog", f.Violations[0].Source.Columns)
	}
	if f.Violations[0].Source.Constraint == "" || f.Violations[0].Source.Table == "" {
		t.Fatal("the constraint and table are missing, so the positive above proved only that a lookup key existed")
	}
}

// The second control: what the driver did name is never replaced. A NOT NULL
// violation on PostgreSQL carries the column, and the engine saw the statement.
func TestACatalogNeverOverwritesTheColumnTheDriverNamed(t *testing.T) {
	err := &pgconnish{Code: "23502", Message: "null value in column", ColumnName: "email", TableName: "users", ConstraintName: "users_email_not_null"}

	cat := &fakeColumns{cols: []string{"something", "else"}}
	f, ok := New("postgres", WithColumns(cat)).Classify(err)
	if !ok {
		t.Fatal("no fault")
	}
	if got := f.Violations[0].Source.Columns; !slices.Equal(got, []string{"email"}) {
		t.Fatalf("Source.Columns = %v, want the driver's own [email]", got)
	}
	if cat.asked != 0 {
		t.Fatalf("the catalog was consulted %d times for a violation the driver had already answered", cat.asked)
	}
}

// A lookup that misses leaves nil rather than an empty slice, for the reason
// errs/build.go:cloneStrings already gives.
func TestALookupThatMissesLeavesTheColumnsUnknown(t *testing.T) {
	cat := &fakeColumns{} // answers false for everything
	f, ok := New("postgres", WithColumns(cat)).Classify(duplicateKey())
	if !ok {
		t.Fatal("no fault")
	}
	if cat.asked != 1 {
		t.Fatalf("the catalog was asked %d times, want once", cat.asked)
	}
	if f.Violations[0].Source.Columns != nil {
		t.Fatalf("Source.Columns = %#v, want nil", f.Violations[0].Source.Columns)
	}
	if f.Detail.Columns != nil {
		t.Fatalf("Detail.Columns = %#v, want nil", f.Detail.Columns)
	}
}

// A violation with no table and no constraint has no lookup key at all, and the
// catalog cannot invent one. This is what the roadmap's "phase 6 unblocks
// Source.Columns for a SQLite foreign key and a PostgreSQL 22001" turned out to
// be wrong about: those two errors carry nothing to look up by.
func TestAViolationWithNoLookupKeyIsNotLookedUp(t *testing.T) {
	cat := &fakeColumns{cols: []string{"whatever"}}
	c := New("postgres", WithColumns(cat))

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"a value too long, which PostgreSQL reports with no table and no constraint", pgish("22001")},
		{"a SQLite foreign key, which carries nothing at all", &sqliteish{code: 787}},
		// The one that is not an edge case: mysql.MySQLError has Number, SQLState
		// and Message and nothing else, so every MySQL and MariaDB violation
		// arrives with no lookup key. WithColumns is inert on those two engines,
		// which is worth a row here so it does not read as a wiring mistake.
		{"a MySQL duplicate key, which names neither table nor constraint", myish(1062, "23000", "Duplicate entry 'a@b.c' for key 'users.email'")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := c
			switch tc.err.(type) {
			case *sqliteish:
				engine = New("sqlite", WithColumns(cat))
			case *mysqlish:
				engine = New("mysql", WithColumns(cat))
			}
			f, ok := engine.Classify(tc.err)
			if !ok {
				t.Fatal("no fault")
			}
			if f.Violations[0].Source.Columns != nil {
				t.Fatalf("Source.Columns = %v, invented from nothing", f.Violations[0].Source.Columns)
			}
		})
	}
	if cat.asked != 0 {
		t.Fatalf("the catalog was asked %d times with no table or constraint to ask about", cat.asked)
	}
}

var _ Columns = (*fakeColumns)(nil)
var _ errs.Classifier = New("postgres")

// fakeCatalog is the smallest thing that satisfies catalog.Catalog, so the
// adapter FromCatalog builds can be driven without a database.
type fakeCatalog struct {
	con  *catalog.Constraint
	kept int
}

func (this *fakeCatalog) Table(string) (*catalog.Table, bool) { return nil, false }
func (this *fakeCatalog) Dialect() string                     { return "postgres" }

func (this *fakeCatalog) Constraint(string, string) (*catalog.Constraint, bool) {
	this.kept++
	if this.con == nil {
		return nil, false
	}
	return this.con, true
}

// A unique index on an expression — CREATE UNIQUE INDEX ... ON users
// (lower(email)) — has no column name for that key part, and the catalog records
// the part as "" with the text in Expressions. "" is not a column name, and the
// SPI hands back names.
func TestAKeyWithAnExpressionPartIsNotAColumnList(t *testing.T) {
	for _, tc := range []struct {
		name string
		cols []string
		want []string
	}{
		// The control, and it has to come from the same adapter: a key every part
		// of which is a column still fills, or "an expression key answers nothing"
		// is indistinguishable from "the adapter answers nothing".
		{"a key of plain columns", []string{"tenant_id", "slug"}, []string{"tenant_id", "slug"}},
		{"a key that is only an expression", []string{""}, nil},
		{"a key with one expression part", []string{"tenant_id", ""}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cat := &fakeCatalog{con: &catalog.Constraint{
				Name:    "users_lower_email_idx",
				Table:   "users",
				Columns: tc.cols,
			}}
			f, ok := New("postgres", WithColumns(FromCatalog(cat))).Classify(duplicateKey())
			if !ok {
				t.Fatal("no fault")
			}
			if cat.kept == 0 {
				t.Fatal("the catalog was never consulted, so what came back proves nothing")
			}
			if !slices.Equal(f.Violations[0].Source.Columns, tc.want) {
				t.Fatalf("Source.Columns = %#v, want %#v", f.Violations[0].Source.Columns, tc.want)
			}
			if !slices.Equal(f.Detail.Columns, tc.want) {
				t.Fatalf("Detail.Columns = %#v, want %#v", f.Detail.Columns, tc.want)
			}
		})
	}
}

// The same rule at the seam rather than in the adapter, because Columns is a
// third-party interface and an implementation of somebody else's can hand back
// whatever it likes.
func TestAColumnListWithANamelessEntryIsTreatedAsAMiss(t *testing.T) {
	cat := &fakeColumns{cols: []string{"tenant_id", ""}}
	f, ok := New("postgres", WithColumns(cat)).Classify(duplicateKey())
	if !ok {
		t.Fatal("no fault")
	}
	if cat.asked != 1 {
		t.Fatalf("the catalog was asked %d times, want once", cat.asked)
	}
	if f.Violations[0].Source.Columns != nil {
		t.Fatalf("Source.Columns = %#v: a key part no column can name was reported as a column", f.Violations[0].Source.Columns)
	}
	if f.Detail.Columns != nil {
		t.Fatalf("Detail.Columns = %#v, want nil", f.Detail.Columns)
	}
}

var _ catalog.Catalog = (*fakeCatalog)(nil)
