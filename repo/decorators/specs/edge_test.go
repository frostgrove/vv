package specs_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"rx-crud/crud"
	"rx-crud/crud/crudtest"
	"rx-crud/repo/basic"
	"rx-crud/repo/decorators/specs"
)

type namedSpec struct {
	name string
	spec specs.Specification[User]
}

// emptySpecs is every way to say "no restriction". Specification.where(null) is
// the archetype; the rest are what a caller ends up holding when the conditions
// it meant to add all turned out to be optional.
func emptySpecs() []namedSpec {
	nothing := specs.Of[User](func(specs.Root[User], specs.Builder) crud.Predicate { return nil })
	return []namedSpec{
		{"where(nil)", specs.Where[User](nil)},
		{"where(nil).and(nil)", specs.Where[User](nil).And(nil)},
		{"where(nil).or(nil)", specs.Where[User](nil).Or(nil)},
		{"not(nil)", specs.Not[User](nil)},
		{"not of an empty composition", specs.Where[User](nil).And(nil).Not()},
		{"allOf()", specs.AllOf[User]()},
		{"anyOf()", specs.AnyOf[User]()},
		{"allOf(nil, nil)", specs.AllOf[User](nil, nil)},
		{"anyOf(nil, nil)", specs.AnyOf[User](nil, nil)},
		{"lift(nil)", specs.Lift[User](nil)},
		{"a nil SpecFunc", specs.SpecFunc[User](nil)},
		{"a specification that returns no predicate", nothing},
		{"a composition of two of those", specs.Where(nothing).And(nothing)},
	}
}

// An empty specification restricts nothing, so the statement has no WHERE clause
// at all — not `WHERE 1 = 1`, which would be the same answer through a plan the
// database has to think about.
func TestNoShapeOfEmptySpecificationRestrictsTheQuery(t *testing.T) {
	for _, tc := range emptySpecs() {
		t.Run(tc.name, func(t *testing.T) {
			clause, args := where(t, tc.spec)
			if clause != "" {
				t.Fatalf("%s compiled to WHERE %s", tc.name, clause)
			}
			if len(args) != 0 {
				t.Fatalf("%s bound %#v", tc.name, args)
			}
		})
	}
}

// The same holds through the bridge into a plain repository: specs.As of an
// empty specification is an option that adds nothing.
func TestAsOfAnEmptySpecificationAddsNoFilter(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())

	if _, err := Users.Bind(rec).GetAll(context.Background(),
		specs.As(specs.Where[User](nil)), crud.Limit(3)); err != nil {
		t.Fatal(err)
	}
	want := `SELECT "id", "email", "name", "age", "active", "created_at" FROM "users" ORDER BY "id" ASC LIMIT 3`
	if got := crudtest.Normalize(rec.Last().SQL); got != want {
		t.Fatalf("sql  = %s\nwant = %s", got, want)
	}
}

// DeleteBy exists to delete what a specification selects. A specification that
// selects nothing in particular selects the whole table, so every shape of empty
// has to be refused, not just the obvious where(null).
func TestDeleteByRefusesEveryShapeOfEmptySpecification(t *testing.T) {
	for _, tc := range emptySpecs() {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 99})

			n, err := specs.Executor(Users.Bind(rec)).DeleteBy(context.Background(), tc.spec)

			if !errors.Is(err, specs.ErrUnboundedDelete) {
				t.Fatalf("err = %v, want ErrUnboundedDelete", err)
			}
			if n != 0 {
				t.Fatalf("the call reported %d rows deleted", n)
			}
			if len(rec.Statements()) != 0 {
				t.Fatalf("%s reached the database: %v", tc.name, rec.SQL())
			}
		})
	}
}

// A missing operand is not a wildcard. Reading the nil half of an OR as "true"
// would turn an optional condition into "match everything", which is how a
// filter quietly becomes a data leak; both operators keep the side they have.
func TestANilOperandNarrowsToTheOtherSide(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec specs.Specification[User]
		want string
	}{
		{"and(nil)", specs.Where(User_.Active.Eq(true)).And(nil), `"active" = $1`},
		{"or(nil)", specs.Where(User_.Active.Eq(true)).Or(nil), `"active" = $1`},
		{"nil.or(something)", specs.Where[User](nil).Or(User_.Active.Eq(true)), `"active" = $1`},
		{"allOf with a nil member", specs.AllOf[User](nil, User_.ID.Eq(1)), `"id" = $1`},
		{"anyOf with a nil member", specs.AnyOf[User](nil, User_.ID.Eq(1)), `"id" = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if clause, _ := where(t, tc.spec); clause != tc.want {
				t.Fatalf("clause = %s, want %s", clause, tc.want)
			}
		})
	}
}

// findOne is a uniqueness assertion, not "take the first": a second match is a
// conflict and the row is withheld rather than picked. findFirst is the call
// that means "any of them will do", and the two differ in the SQL they send.
func TestFindOneAssertsUniquenessWhereFindFirstDoesNot(t *testing.T) {
	twoMatches := func() crudtest.Result {
		return crudtest.Rows(row(1, "a@x", "Ann"), row(2, "a@x", "Bob"))
	}

	t.Run("two matches are a conflict", func(t *testing.T) {
		rec := crudtest.Postgres().Push(twoMatches())

		u, err := specs.Executor(Users.Bind(rec)).FindOne(context.Background(), User_.Email.Eq("a@x"))

		if !errors.Is(err, specs.ErrNotUnique) {
			t.Fatalf("err = %v, want ErrNotUnique", err)
		}
		if u.ID != 0 || u.Email != "" {
			t.Fatalf("an ambiguous findOne still handed back %+v", u)
		}
		if !strings.Contains(rec.Last().SQL, "LIMIT 2") {
			t.Fatalf("findOne fetched %s; it has to look for a second row to know there is one", rec.Last().SQL)
		}
	})

	// The probe is what makes findOne's contract real, so a caller's own paging
	// must not be able to take it away. Behind the caller's options a
	// crud.Limit(1) won: the database could then only ever return one row, and
	// a second match was reported as a unique hit with err == nil.
	t.Run("a caller's own paging cannot disarm the probe", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			opts []crud.Option
		}{
			{"a tighter limit", []crud.Option{crud.Limit(1)}},
			{"no limit at all", []crud.Option{crud.Unpaged()}},
			{"a page", []crud.Option{crud.Page(3), crud.Limit(1)}},
			{"an offset", []crud.Option{crud.Offset(10)}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				rec := crudtest.Postgres().Push(twoMatches())

				_, err := specs.Executor(Users.Bind(rec)).
					FindOne(context.Background(), User_.Email.Eq("a@x"), tc.opts...)

				if !errors.Is(err, specs.ErrNotUnique) {
					t.Fatalf("err = %v, want ErrNotUnique; the caller's paging disarmed "+
						"the uniqueness check and %s was sent", err, rec.Last().SQL)
				}
				if !strings.HasSuffix(rec.Last().SQL, "LIMIT 2") {
					t.Fatalf("findOne sent %s, want it to end LIMIT 2 whatever the caller asked for",
						rec.Last().SQL)
				}
			})
		}
	})

	t.Run("the conflict is the one a transport answers 409 to", func(t *testing.T) {
		if !errors.Is(specs.ErrNotUnique, crud.ErrConflict) {
			t.Fatal("specs.ErrNotUnique no longer wraps crud.ErrConflict, so a transport cannot map it")
		}
	})

	t.Run("findFirst takes the first of them", func(t *testing.T) {
		rec := crudtest.Postgres().Push(twoMatches())

		u, err := specs.Executor(Users.Bind(rec)).FindFirst(context.Background(), User_.Email.Eq("a@x"))

		if err != nil {
			t.Fatal(err)
		}
		if u.ID != 1 {
			t.Fatalf("findFirst returned %+v, want the first row", u)
		}
		if !strings.Contains(rec.Last().SQL, "LIMIT 1") {
			t.Fatalf("findFirst fetched %s, want a single row", rec.Last().SQL)
		}
	})

	t.Run("no match at all", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows(), crudtest.Rows())
		repo := specs.Executor(Users.Bind(rec))

		if u, err := repo.FindOne(context.Background(), User_.Email.Eq("nobody")); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("findOne: err = %v (%+v), want ErrNotFound", err, u)
		}
		if u, err := repo.FindFirst(context.Background(), User_.Email.Eq("nobody")); !errors.Is(err, crud.ErrNotFound) {
			t.Fatalf("findFirst: err = %v (%+v), want ErrNotFound", err, u)
		}
	})
}

// A metamodel is the analogue of a generated User_ class: it is declared once,
// at package initialisation, and every way it can fail to describe the model has
// to fail there — a query is far too late to discover that a column was renamed.
func TestAMetamodelThatCannotBindIsRefusedAtDeclarationTime(t *testing.T) {
	type notAnAttribute struct {
		Name string
	}
	type notARelation struct {
		Author struct {
			Name specs.Str[User]
		}
	}

	assertPanics(t, "a metamodel that is not a struct", func() { specs.Metamodel[User, int]() })
	assertPanics(t, "a field that is neither an attribute nor a nested struct",
		func() { specs.Metamodel[User, notAnAttribute]() })
	assertPanics(t, "a nested struct that does not name a relation",
		func() { specs.Metamodel[User, notARelation]() })
	assertPanics(t, "an attribute declared at the wrong width", func() { specs.Ordered[User, int64]("Age") })
	assertPanics(t, "an attribute declared with the wrong type", func() { specs.Attribute[User, bool]("Email") })
}

// The `attr` tag is the metamodel's only escape hatch: it points an attribute at
// a differently named column, and it takes a field out of the mapping entirely.
func TestTheAttrTagRenamesAndSkips(t *testing.T) {
	type tagged struct {
		Address specs.Str[User] `attr:"email"` // the same column, spelled the wire's way
		Ignored specs.Str[User] `attr:"-"`     // not part of the model at all
	}

	m := specs.Metamodel[User, tagged]()

	if got := m.Address.Name(); got != "Email" {
		t.Fatalf(`attr:"email" bound to %q, want the canonical Email`, got)
	}
	if got := m.Ignored.Name(); got != "" {
		t.Fatalf(`attr:"-" bound to %q instead of being skipped`, got)
	}
	if clause, args := where(t, m.Address.Eq("a@x")); clause != `"email" = $1` || args[0] != "a@x" {
		t.Fatalf("the renamed attribute filtered %s %v", clause, args)
	}
}

// The executor is a decorator, so a repository that was already narrowed stays
// narrowed: a specification is ANDed onto the repository scope, never swapped
// for it.
func TestASpecificationCannotEscapeARepositoryScope(t *testing.T) {
	scoped := basic.Define[User, int64, UserUpdate]("users", basic.Scope(crud.Eq("Active", true)))
	rec := crudtest.Postgres().Push(crudtest.Rows())

	if _, err := specs.Executor(scoped.Bind(rec)).FindAll(context.Background(),
		specs.Where(User_.Active.Eq(false))); err != nil {
		t.Fatal(err)
	}

	want := `SELECT "id", "email", "name", "age", "active", "created_at" FROM "users" ` +
		`WHERE ("active" = $1 AND "active" = $2)`
	if got := crudtest.Normalize(rec.Last().SQL); got != want {
		t.Fatalf("sql  = %s\nwant = %s", got, want)
	}
	if args := rec.Last().Args; len(args) != 2 || args[0] != true || args[1] != false {
		t.Fatalf("bound %#v, want the scope's value first and the specification's second", args)
	}
}
