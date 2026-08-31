package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud/crudtest"
)

func TestAnUnknownDialectIsRefusedBeforeAnyStatement(t *testing.T) {
	ctx := context.Background()
	rec := crudtest.New(namedDialect{name: "cockroach"})

	cat, err := Load(ctx, rec)
	if !errors.Is(err, ErrUnknownDialect) {
		t.Fatalf("loading on a dialect nothing introspects answered %v, want ErrUnknownDialect", err)
	}
	if cat != nil {
		t.Error("a refused load still handed back a catalog — an empty one reads as a database with no constraint problems")
	}
	if n := len(rec.Statements()); n != 0 {
		t.Errorf("the refusal came after %d statements had run, and it has to come before any of them", n)
	}
}

func TestAKnownDialectLoadsAndIssuesItsStatements(t *testing.T) {
	ctx := context.Background()
	rec := recorder(oneTable(), 1)

	cat, err := Load(ctx, rec)
	if err != nil {
		t.Fatalf("a dialect with a back-end was refused: %v", err)
	}
	if cat == nil {
		t.Fatal("no catalog and no error")
	}
	if got := len(rec.Statements()); got != pgPass {
		t.Errorf("the load sent %d statements, want %d: %v", got, pgPass, rec.SQL())
	}
	if _, ok := cat.Table("rows"); !ok {
		t.Error("the catalog does not know the one table the schema had")
	}
}

func TestABlockedIntrospectionFailsLoadRatherThanReturningAHalfCatalog(t *testing.T) {
	blocked := errors.New("permission denied for table pg_constraint")

	for _, how := range []struct {
		name     string
		response func(crudtest.Result) crudtest.Result
	}{
		{"refused by Query", func(crudtest.Result) crudtest.Result {
			return crudtest.Result{Err: blocked}
		}},
		{"refused through Rows.Err, which is what pgx does", func(r crudtest.Result) crudtest.Result {
			return crudtest.RowsFailing(blocked, r.Rows...)
		}},
	} {
		t.Run(how.name, func(t *testing.T) {
			for k := range pgPass {
				ctx := context.Background()
				rec := crudtest.Postgres()
				s := oneTable()
				response := s.results()
				response[k] = how.response(response[k])
				rec.Push(response...)

				cat, err := Load(ctx, rec)
				if !errors.Is(err, ErrIntrospection) {
					t.Errorf("statement %d refused and Load answered %v, want an error wrapping ErrIntrospection", k, err)
				}
				if !errors.Is(err, blocked) {
					t.Errorf("statement %d refused and the server's own error was dropped: %v", k, err)
				}
				if cat != nil {
					t.Errorf("statement %d refused and Load still handed back a catalog", k)
				}
			}
		})
	}
}

func TestARowThatCannotBeScannedFailsLoadRatherThanDroppingIt(t *testing.T) {
	s := oneTable()
	short := pgColumnRow("rows", "id", 1)
	s.columns = [][]any{short[:len(short)-1]}

	cat, err := Load(context.Background(), recorder(s, 1))
	if !errors.Is(err, ErrIntrospection) {
		t.Fatalf("a row the driver could not scan answered %v, want ErrIntrospection", err)
	}
	if cat != nil {
		t.Error("a schema whose columns did not all scan was still handed back as a catalog")
	}
}

func TestAnUnblockedIntrospectionBuildsThePopulatedCatalog(t *testing.T) {
	ctx := context.Background()
	s := pgSchema{
		columns: [][]any{
			pgColumnRow("rows", "id", 1),
			pgColumnRow("rows", "slug", 2),
		},
		constraints: [][]any{
			pgConstraintRow("rows", "rows_pkey", "p", 1, "id"),
			pgConstraintRow("rows", "rows_slug_key", "u", 1, "slug"),
		},
		indexes: [][]any{
			pgIndexRow("rows", "rows_live_idx", "slug", true, "(deleted_at IS NULL)"),
		},
	}
	rec := crudtest.Postgres()
	queued := len(s.results())
	rec.Push(s.results()...)

	cat, err := Load(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(rec.Statements()); got != queued {
		t.Fatalf("the loader sent %d statements against %d queued result sets, so some of it read an empty queue: %v",
			got, queued, rec.SQL())
	}

	tbl, ok := cat.Table("rows")
	if !ok {
		t.Fatal("the catalog does not know the table")
	}
	if len(tbl.Columns) != 2 || tbl.Columns[1].Name != "slug" {
		t.Errorf("columns = %+v, want both of them in order", tbl.Columns)
	}
	if len(tbl.PrimaryKey) != 1 || tbl.PrimaryKey[0] != "id" {
		t.Errorf("primary key = %v, want [id]", tbl.PrimaryKey)
	}
	con, ok := cat.Constraint("rows", "rows_live_idx")
	if !ok {
		t.Fatal("the catalog lost the unique index")
	}
	if con.Kind != KindUniqueIndex || !con.Partial || con.Predicate != "(deleted_at IS NULL)" {
		t.Errorf("the partial index came back as %+v", con)
	}
}
