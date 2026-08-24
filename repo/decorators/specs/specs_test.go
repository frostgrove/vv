package specs_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/crudtest"
	"github.com/shardit-io/vv/repo/basic"
	"github.com/shardit-io/vv/repo/decorators/specs"
)

type User struct {
	ID        int64         `db:"id,pk,auto"`
	Email     string        `db:"email"`
	Name      string        `db:"name"`
	Age       crud.Opt[int] `db:"age"`
	Active    bool          `db:"active"`
	CreatedAt time.Time     `db:"created_at"`
}

type UserUpdate struct {
	Name *string
}

// The metamodel: JPA's generated User_ class, written by hand once and checked
// against the model at package initialisation.
type userAttrs struct {
	ID      specs.Ord[User, int64]
	Email   specs.Str[User]
	Name    specs.Str[User]
	Age     specs.Ord[User, int]
	Active  specs.Attr[User, bool]
	Created specs.Cmp[User, time.Time] `attr:"CreatedAt"`
}

var (
	User_ = specs.Metamodel[User, userAttrs]()
	Users = basic.Define[User, int64, UserUpdate]("users")
)

var now = time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

func row(id int64, email, name string) []any {
	return []any{id, email, name, 30, true, now}
}

func where(t *testing.T, s specs.Specification[User]) (string, []any) {
	t.Helper()
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := specs.Executor(Users.Bind(rec)).FindAll(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	last := rec.Last()
	_, clause, _ := strings.Cut(last.SQL, " WHERE ")
	return clause, last.Args
}

// The literal JPA style: a specification is a function of (root, builder).
func isActive() specs.Specification[User] {
	return specs.Of[User](func(root specs.Root[User], cb specs.Builder) crud.Predicate {
		return cb.Equal(root.Get("Active"), true)
	})
}

func emailEndsWith(suffix string) specs.Specification[User] {
	return specs.Of[User](func(root specs.Root[User], cb specs.Builder) crud.Predicate {
		return cb.Like(root.Get("Email"), "%"+suffix)
	})
}

func TestCriteriaBuilderStyle(t *testing.T) {
	clause, args := where(t, specs.Where(isActive()).And(emailEndsWith("@corp.com")))
	if clause != `("active" = $1 AND "email" LIKE $2)` {
		t.Fatalf("clause = %s", clause)
	}
	if args[0] != true || args[1] != "%@corp.com" {
		t.Fatalf("args = %v", args)
	}
}

func TestMetamodelStyle(t *testing.T) {
	clause, args := where(t, specs.Where(User_.Age.Gte(18)).
		And(User_.Name.StartsWith("An")).
		And(User_.Email.In("a@x", "b@x")).
		Or(User_.ID.Eq(1)))

	if clause != `(("age" >= $1 AND "name" LIKE $2 AND "email" IN ($3, $4)) OR "id" = $5)` {
		t.Fatalf("clause = %s", clause)
	}
	if args[0] != 18 || args[1] != "An%" {
		t.Fatalf("args = %v", args)
	}
}

func TestComposition(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec specs.Specification[User]
		want string
	}{
		{"and", specs.Where(User_.Active.Eq(true)).And(User_.Age.Lt(30)),
			`("active" = $1 AND "age" < $2)`},
		{"or", specs.Where(User_.Active.Eq(true)).Or(User_.Age.Lt(30)),
			`("active" = $1 OR "age" < $2)`},
		{"not", specs.Not(User_.Active.Eq(true)),
			`NOT ("active" = $1)`},
		{"allOf", specs.AllOf(User_.Active.Eq(true), User_.Age.Gt(1), User_.Name.Ne("x")),
			`("active" = $1 AND "age" > $2 AND "name" <> $3)`},
		{"anyOf", specs.AnyOf(User_.Active.Eq(true), User_.Age.Gt(1)),
			`("active" = $1 OR "age" > $2)`},
		{"nil operand is ignored", specs.Where[User](nil).And(User_.Age.Gt(1)),
			`"age" > $1`},
		{"isNull", specs.Where(User_.Age.IsNull()), `"age" IS NULL`},
		{"between", specs.Where(User_.Created.Between(now, now)),
			`"created_at" BETWEEN $1 AND $2`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if clause, _ := where(t, tc.spec); clause != tc.want {
				t.Fatalf("clause = %s, want %s", clause, tc.want)
			}
		})
	}
}

// Specification.where(null) means "no restriction", so the statement has no
// WHERE clause at all.
func TestEmptySpecificationDoesNotRestrict(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := specs.Executor(Users.Bind(rec)).FindAll(context.Background(), specs.Where[User](nil)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Last().SQL, "WHERE") {
		t.Fatalf("expected no WHERE: %s", rec.Last().SQL)
	}
}

func TestFindOne(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(row(1, "a@x", "Ann")))
	repo := specs.Executor(Users.Bind(rec))

	u, err := repo.FindOne(context.Background(), User_.Email.Eq("a@x"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Ann" {
		t.Fatalf("user = %+v", u)
	}
	// findOne fetches two rows so it can tell "one" from "more than one".
	if !strings.Contains(rec.Last().SQL, "LIMIT 2") {
		t.Fatalf("sql = %s", rec.Last().SQL)
	}

	rec.Reset()
	rec.Push(crudtest.Rows(row(1, "a@x", "Ann"), row(2, "a@x", "Bob")))
	if _, err := repo.FindOne(context.Background(), User_.Email.Eq("a@x")); !errors.Is(err, specs.ErrNotUnique) {
		t.Fatalf("err = %v, want ErrNotUnique", err)
	}

	rec.Reset()
	rec.Push(crudtest.Rows())
	if _, err := repo.FindOne(context.Background(), User_.Email.Eq("nobody")); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestFindPageCountsAndDeletes(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(row(1, "a@x", "Ann"), row(2, "b@x", "Bob")),
		crudtest.Rows([]any{int64(9)}),
	)
	repo := specs.Executor(Users.Bind(rec))

	page, err := repo.FindPage(context.Background(), User_.Active.Eq(true), crud.Page(1), crud.Limit(2))
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 9 || page.TotalPages != 5 || !page.HasNext {
		t.Fatalf("page = %+v", page)
	}

	rec.Reset()
	rec.Push(crudtest.Rows([]any{int64(3)}))
	if n, err := repo.CountBy(context.Background(), User_.Active.Eq(false)); err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}

	rec.Reset()
	rec.ExecResult(crud.Result{RowsAffected: 3})
	if n, err := repo.DeleteBy(context.Background(), User_.Active.Eq(false)); err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if got := crudtest.Normalize(rec.Last().SQL); got != `DELETE FROM "users" WHERE "active" = $1` {
		t.Fatalf("sql = %s", got)
	}
}

// DeleteBy with an empty specification would truncate the table; that has to be
// deliberate, so it is refused here.
func TestDeleteByRefusesEmptySpecification(t *testing.T) {
	rec := crudtest.Postgres()
	repo := specs.Executor(Users.Bind(rec))
	if _, err := repo.DeleteBy(context.Background(), specs.Where[User](nil)); !errors.Is(err, specs.ErrUnboundedDelete) {
		t.Fatalf("err = %v, want ErrUnboundedDelete", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatal("nothing should have reached the database")
	}
}

// The decorator keeps the whole basic surface.
func TestExecutorStillHasTheBasicMethods(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(row(1, "a@x", "Ann")))
	repo := specs.Executor(Users.Bind(rec))
	if _, err := repo.GetByID(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	name := "Bob"
	rec.Reset()
	rec.Push(crudtest.Rows(row(1, "a@x", "Ann")), crudtest.Rows(row(1, "a@x", "Bob")))
	u, err := repo.Update(context.Background(), 1, UserUpdate{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Bob" {
		t.Fatalf("user = %+v", u)
	}
}

func TestMetamodelValidatesAtDeclarationTime(t *testing.T) {
	type wrongType struct {
		Age specs.Str[User] // model has Opt[int]
	}
	type missing struct {
		Nope specs.Str[User]
	}
	assertPanics(t, "wrong type", func() { specs.Metamodel[User, wrongType]() })
	assertPanics(t, "unknown field", func() { specs.Metamodel[User, missing]() })
	assertPanics(t, "unknown attribute", func() { specs.Text[User]("Nope") })
}

func assertPanics(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected a panic at declaration time", what)
		}
	}()
	f()
}
