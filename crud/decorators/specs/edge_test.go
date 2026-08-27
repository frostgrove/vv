package specs_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

type stableDriverValue struct{ value int64 }

func (v stableDriverValue) Value() (driver.Value, error) { return v.value, nil }

type otherDriverValue struct{}

func (otherDriverValue) Value() (driver.Value, error) { return int64(7), nil }

type decimalDriverValue struct{}

func (decimalDriverValue) Decompose([]byte) (byte, bool, []byte, int32) {
	return 0, false, []byte{7}, 0
}

type namedSpec struct {
	name string
	spec specs.Specification[User]
}

// emptySpecs is every way to say "no restriction". Specification.where(null) is
// the archetype; the rest are what a caller ends up holding when the conditions
// it meant to add all turned out to be optional.
func emptySpecs() []namedSpec {
	nothing := specs.Of[User](func(specs.Root[User], specs.Builder) crud.Predicate { return nil })
	conjunction := specs.Of[User](func(_ specs.Root[User], cb specs.Builder) crud.Predicate {
		return cb.Conjunction()
	})
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
		{"criteria conjunction", conjunction},
		{"lifted true", specs.Lift[User](crud.True())},
		{"empty and", specs.Lift[User](crud.And())},
		{"empty not-in", User_.ID.NotIn()},
		{"primary key is not null", User_.ID.NotNull()},
		{"not primary key is null", specs.Not(User_.ID.IsNull())},
		{"nullable null complement", specs.Where(User_.Age.IsNull()).Or(User_.Age.NotNull())},
		{"nullable null complement through not", specs.Where(User_.Age.IsNull()).Or(specs.Not(User_.Age.IsNull()))},
		{"identity-wrapped null complement", specs.Where[User](nil).And(User_.Age.IsNull()).Or(specs.Not(User_.Age.IsNull()))},
		{"primary key equality complement", specs.Where(User_.ID.Eq(1)).Or(User_.ID.Ne(1))},
		{"primary key range complement", specs.Where(User_.ID.Lt(1)).Or(User_.ID.Gte(1))},
		{"primary key membership complement", specs.Where(User_.ID.In(1, 2)).Or(User_.ID.NotIn(1, 2))},
		{"primary key reordered membership complement", specs.Where(User_.ID.In(1, 2)).Or(User_.ID.NotIn(2, 1, 1))},
		{"primary key reordered negated membership complement", specs.Where(User_.ID.In(1, 2)).Or(specs.Not(User_.ID.In(2, 1, 1)))},
		{"primary key nested boolean tautology", specs.Where(User_.ID.Eq(1)).
			Or(specs.Not(User_.ID.Eq(1)).And(User_.ID.Eq(2))).
			Or(specs.Not(User_.ID.Eq(2)))},
		{"primary key between complement", specs.Where(User_.ID.Between(1, 2)).
			Or(specs.Not(specs.AllOf(User_.ID.Gte(1), User_.ID.Lte(2))))},
		{"primary key membership expansion complement", specs.Where(User_.ID.In(1, 2)).
			Or(specs.Not(specs.AnyOf(User_.ID.Eq(1), User_.ID.Eq(2))))},
		{"a field compared with itself", specs.Of[User](func(r specs.Root[User], cb specs.Builder) crud.Predicate {
			return cb.EqualTo(r.Get("ID"), r.Get("id"))
		})},
	}
}

// An unrestricted specification may render as no WHERE clause or as `1 = 1`.
// The latter is still a whole-table question, so bulk operations must recognise
// the semantic identity rather than relying on a nil AST node.
func TestEveryUnrestrictedDeclarativeSpecificationIsRecognised(t *testing.T) {
	for _, tc := range emptySpecs() {
		t.Run(tc.name, func(t *testing.T) {
			if p := specs.Predicate(tc.spec); !crud.IsTautologyFor(Users.Meta(), p) {
				t.Fatalf("%s is not recognised as unrestricted: %T", tc.name, p)
			}
		})
	}
}

func TestBulkSpecificationsKeepRealNullableNarrowing(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec specs.Specification[User]
	}{
		{"nullable is not null", User_.Age.NotNull()},
		{"nullable field equals itself", specs.Of[User](func(r specs.Root[User], cb specs.Builder) crud.Predicate {
			return cb.EqualTo(r.Get("Age"), r.Get("age"))
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
			if _, err := specs.Executor(Users.Bind(rec)).DeleteBy(context.Background(), tc.spec); err != nil {
				t.Fatalf("DeleteBy() = %v, want nullable narrowing to reach the repository", err)
			}
		})
	}
}

func TestBulkSpecificationsKeepNullablePointerChainsAsNarrowing(t *testing.T) {
	var inner *int64
	outer := &inner
	p := specs.Lift[User](crud.Or(crud.Eq("ID", outer), crud.Not(crud.Eq("id", outer))))
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	if _, err := specs.Executor(Users.Bind(rec)).DeleteBy(context.Background(), p); err != nil {
		t.Fatalf("DeleteBy() = %v, want nullable pointer chain to reach the repository", err)
	}
}

func TestBulkSpecificationsRefuseAnEquivalentNonNilPointerBind(t *testing.T) {
	id := int64(7)
	p := specs.Lift[User](crud.Or(crud.Eq("ID", &id), crud.Not(crud.Eq("id", id))))
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	if _, err := specs.Executor(Users.Bind(rec)).DeleteBy(context.Background(), p); !errors.Is(err, specs.ErrUnboundedDelete) {
		t.Fatalf("DeleteBy() = %v, want ErrUnboundedDelete", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("tautological pointer bind reached the database: %v", rec.SQL())
	}
}

func TestBulkSpecificationsFailClosedOnAnOpaqueDriverValuer(t *testing.T) {
	p := specs.Lift[User](crud.Or(
		crud.Eq("ID", stableDriverValue{value: 7}),
		crud.Ne("id", otherDriverValue{}),
	))
	for _, tc := range []struct {
		name string
		call func(specs.Repo[User, int64, UserUpdate]) error
		want error
	}{
		{
			name: "delete",
			call: func(r specs.Repo[User, int64, UserUpdate]) error {
				_, err := r.DeleteBy(context.Background(), p)
				return err
			},
			want: specs.ErrUnboundedDelete,
		},
		{
			name: "update",
			call: func(r specs.Repo[User, int64, UserUpdate]) error {
				_, err := r.UpdateBy(context.Background(), p, UserUpdate{})
				return err
			},
			want: specs.ErrUnboundedUpdate,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
			if err := tc.call(specs.Executor(Users.Bind(rec))); !errors.Is(err, tc.want) {
				t.Fatalf("bulk operation = %v, want %v", err, tc.want)
			}
			if len(rec.Statements()) != 0 {
				t.Fatalf("possible driver.Valuer tautology reached the database: %v", rec.SQL())
			}
		})
	}
}

func TestBulkSpecificationsRefuseRawSQL(t *testing.T) {
	p := specs.Lift[User](crud.Raw("1 = 1"))
	for _, tc := range []struct {
		name string
		call func(specs.Repo[User, int64, UserUpdate]) error
		want error
	}{
		{
			name: "delete",
			call: func(r specs.Repo[User, int64, UserUpdate]) error {
				_, err := r.DeleteBy(context.Background(), p)
				return err
			},
			want: specs.ErrUnboundedDelete,
		},
		{
			name: "update",
			call: func(r specs.Repo[User, int64, UserUpdate]) error {
				_, err := r.UpdateBy(context.Background(), p, UserUpdate{})
				return err
			},
			want: specs.ErrUnboundedUpdate,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
			if err := tc.call(specs.Executor(Users.Bind(rec))); !errors.Is(err, tc.want) {
				t.Fatalf("bulk operation = %v, want %v", err, tc.want)
			}
			if len(rec.Statements()) != 0 {
				t.Fatalf("raw SQL reached the database: %v", rec.SQL())
			}
		})
	}
}

func TestBulkSpecificationsRefuseOpaqueDatabaseSQLBinds(t *testing.T) {
	p := specs.Lift[User](crud.Or(
		crud.Eq("ID", decimalDriverValue{}),
		crud.Ne("id", decimalDriverValue{}),
	))
	for _, tc := range []struct {
		name string
		call func(specs.Repo[User, int64, UserUpdate]) error
		want error
	}{
		{
			name: "delete",
			call: func(r specs.Repo[User, int64, UserUpdate]) error {
				_, err := r.DeleteBy(context.Background(), p)
				return err
			},
			want: specs.ErrUnboundedDelete,
		},
		{
			name: "update",
			call: func(r specs.Repo[User, int64, UserUpdate]) error {
				_, err := r.UpdateBy(context.Background(), p, UserUpdate{})
				return err
			},
			want: specs.ErrUnboundedUpdate,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
			if err := tc.call(specs.Executor(Users.Bind(rec))); !errors.Is(err, tc.want) {
				t.Fatalf("bulk operation = %v, want %v", err, tc.want)
			}
			if len(rec.Statements()) != 0 {
				t.Fatalf("opaque database/sql bind reached the database: %v", rec.SQL())
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
			if !errors.Is(err, crud.ErrBadRequest) {
				t.Fatalf("err = %v, want the transport-safe bad-request sentinel", err)
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

func TestUpdateByRefusesEveryShapeOfEmptySpecification(t *testing.T) {
	for _, tc := range emptySpecs() {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 99})

			n, err := specs.Executor(Users.Bind(rec)).UpdateBy(context.Background(), tc.spec, UserUpdate{})

			if !errors.Is(err, specs.ErrUnboundedUpdate) {
				t.Fatalf("err = %v, want ErrUnboundedUpdate", err)
			}
			if !errors.Is(err, crud.ErrBadRequest) {
				t.Fatalf("err = %v, want the transport-safe bad-request sentinel", err)
			}
			if n != 0 {
				t.Fatalf("the call reported %d rows updated", n)
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
	scoped := sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.Scope(crud.Eq("Active", true)))
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

// ---------------------------------------------------------------------------
// relation handles

// A second model set: User has no relations, and a relation handle is the one
// thing a metamodel cannot describe without one.
type Post struct {
	ID       int64    `db:"id,pk,auto"`
	AuthorID int64    `db:"author_id"`
	Title    string   `db:"title"`
	Author   *Writer  `rel:"belongs_to"`
	Comments []Remark `rel:"has_many"`
}

type Writer struct {
	ID   int64  `db:"id,pk,auto"`
	Name string `db:"name"`
}

type Remark struct {
	ID       int64   `db:"id,pk,auto"`
	PostID   int64   `db:"post_id"`
	AuthorID int64   `db:"author_id"`
	Body     string  `db:"body"`
	Author   *Writer `rel:"belongs_to"`
}

type writerAttrs struct {
	specs.Rel[Post, Writer]
	Name specs.Str[Post]
}

type remarkAttrs struct {
	specs.Rel[Post, Remark]
	Body   specs.Str[Post]
	Author writerAttrs
}

// The group sits under a Go field name that is *not* the relation's, so the
// path the handle answers can only have come from binding.
type postAttrs struct {
	Title specs.Str[Post]
	Comm  remarkAttrs `attr:"Comments"`
}

// A relation handle is the typed spelling of a path that is otherwise a string
// literal: sqlrepo.RelationScope, crud.Preload and a relation policy all take one.
func TestARelationHandleAnswersItsCanonicalPath(t *testing.T) {
	m := specs.Metamodel[Post, postAttrs]()

	for _, spelling := range []struct {
		how  string
		path string
	}{
		{"Path", m.Comm.Path()},
		{"RelPath", m.Comm.RelPath()},
		{"String", m.Comm.String()},
	} {
		if spelling.path != "Comments" {
			t.Errorf("%s() answered %q, want the canonical Comments — the handle took its "+
				"path from the Go field name instead of the relation", spelling.how, spelling.path)
		}
	}

	// A handle one hop further carries the whole path, which is what a nested
	// preload and a nested relation scope are addressed by.
	if got := m.Comm.Author.Path(); got != "Comments.Author" {
		t.Errorf("the nested handle answered %q, want Comments.Author", got)
	}

	// The attributes beside it still describe the *root*, so the group is not
	// two things at once.
	if got := m.Comm.Body.Name(); got != "Comments.Body" {
		t.Errorf("the attribute beside the handle answered %q, want Comments.Body", got)
	}
}

// A handle declaring the wrong target would narrow a table nobody meant to
// narrow, and the path itself would still look right — so it has to be refused
// where every other metamodel mistake is, at declaration time.
func TestARelationHandleDeclaringTheWrongTargetIsRefused(t *testing.T) {
	type wrongTarget struct {
		Comments struct {
			specs.Rel[Post, Writer] // Comments lands on Remark
		}
	}
	type rightTarget struct {
		Comments struct {
			specs.Rel[Post, Remark]
		}
	}

	assertPanics(t, "a handle pointing at the wrong model",
		func() { specs.Metamodel[Post, wrongTarget]() })

	// The control: without it, the refusal above could be about anything.
	m := specs.Metamodel[Post, rightTarget]()
	if got := m.Comments.Path(); got != "Comments" {
		t.Fatalf("the right target bound to %q, so the refusal above proves nothing", got)
	}
}

// A handle stands for the group it sits in. At the root there is no group and
// no relation, so there is no path it could honestly answer.
func TestARelationHandleAtTheRootIsRefused(t *testing.T) {
	type atRoot struct {
		specs.Rel[Post, Remark]
		Title specs.Str[Post]
	}
	type withoutIt struct {
		Title specs.Str[Post]
	}

	assertPanics(t, "a handle at the root", func() { specs.Metamodel[Post, atRoot]() })

	// The control: the same struct minus the handle binds, so the refusal is
	// about the handle and not about Post.
	if got := specs.Metamodel[Post, withoutIt]().Title.Name(); got != "Title" {
		t.Fatalf("the same metamodel without the handle bound Title to %q", got)
	}
}
