package crud_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
)

type stableDriverValue struct{ value int64 }

func (this stableDriverValue) Value() (driver.Value, error) { return this.value, nil }

type otherDriverValue struct{}

func (otherDriverValue) Value() (driver.Value, error) { return int64(7), nil }

// database/sql accepts decimal decomposition without requiring driver.Valuer.
// A driver may bind this as a non-NULL number, but the predicate guard must not
// depend on an implementation-specific conversion it cannot inspect.
type decimalDriverValue struct{}

func (decimalDriverValue) Decompose([]byte) (byte, bool, []byte, int32) {
	return 0, false, []byte{7}, 0
}

// ---------------------------------------------------------------------------
// the predicate AST
//
// The tree is rendered exactly as it was built. Nothing is simplified away:
// a reader debugging a WHERE clause has to be able to find the predicate they
// wrote in the SQL they got.

func TestDegenerateTreesStillRenderValidSQL(t *testing.T) {
	m := articleMeta(t)
	a := crud.Eq("Title", "Go")

	for _, tc := range []struct {
		name string
		p    crud.Predicate
		sql  string
	}{
		{"a double negative is not folded", crud.Not(crud.Not(a)), `NOT (NOT ("title" = $1))`},
		{"NOT of a constant", crud.Not(crud.True()), `NOT (1 = 1)`},
		{"NOT of an empty AND, which is true", crud.Not(crud.And()), `NOT (1 = 1)`},
		{"a contradiction is rendered, not resolved", crud.And(crud.True(), crud.False()), `(1 = 1 AND 1 = 0)`},
		{"OR of one operand needs no parentheses", crud.Or(a), `"title" = $1`},
		{"OR of nothing but nils is false", crud.Or(nil, nil), `1 = 0`},
		// The empty OR keeps its meaning inside the AND rather than being
		// dropped as an empty node: "and nothing at all" is false, and losing
		// it would silently widen the query to everything the AND allows.
		{"an empty OR inside an AND still makes it false", crud.And(a, crud.Or()), `("title" = $1 AND 1 = 0)`},
		{"an empty AND inside an OR is true and swallows it", crud.Or(a, crud.And()), `("title" = $1 OR 1 = 1)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, _ := mustRender(t, crud.Postgres{}, m, tc.p)
			if sql != tc.sql {
				t.Fatalf("sql  = %s\nwant = %s", sql, tc.sql)
			}
		})
	}
}

// An IN over no values cannot name the column at all — `IN ()` is a syntax
// error everywhere — so it degrades to the constant that answers the question
// honestly, whichever way the list arrived.
func TestInOverAnEmptyListIsAConstant(t *testing.T) {
	m := articleMeta(t)

	for _, tc := range []struct {
		name  string
		p     crud.Predicate
		sql   string
		binds int
	}{
		{"a nil slice", crud.InAny[int]("Views", nil), `1 = 0`, 0},
		{"an empty slice", crud.InAny("Views", []int{}), `1 = 0`, 0},
		{"no variadic arguments", crud.In("Views"), `1 = 0`, 0},
		{"NOT IN of nothing excludes nothing", crud.NotInAny[int]("Views", nil), `1 = 1`, 0},
		{"a list of one is still a list", crud.InAny("Views", []int{4}), `"views" IN ($1)`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args := mustRender(t, crud.Postgres{}, m, tc.p)
			if sql != tc.sql {
				t.Fatalf("sql = %s, want %s", sql, tc.sql)
			}
			if len(args) != tc.binds {
				t.Fatalf("args = %v, want %d of them", args, tc.binds)
			}
		})
	}
}

func TestIsTautologyRecognisesClosedUnconditionalPredicates(t *testing.T) {
	for _, p := range []crud.Predicate{
		nil,
		crud.True(),
		crud.NotInAny("ID", []int64{}),
		crud.And(crud.True(), crud.NotInAny("ID", []int64{})),
		crud.Or(crud.False(), crud.True()),
		crud.Not(crud.False()),
	} {
		if !crud.IsTautology(p) {
			t.Fatalf("%T was not recognised as unconditional", p)
		}
	}
	for _, p := range []crud.Predicate{
		crud.False(),
		crud.Eq("Views", 1),
		crud.And(crud.True(), crud.Eq("Views", 1)),
	} {
		if crud.IsTautology(p) {
			t.Fatalf("%T was mistaken for an unconditional predicate", p)
		}
	}
}

// Bulk-write guards use IsTautology, so it must share the renderer's treatment
// of nil branches. In particular Or(nil) is false and its negation is true.
func TestIsTautologyUsesTheRenderersNilBranchSemantics(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    crud.Predicate
		want bool
	}{
		{"empty and", crud.And(nil), true},
		{"empty or", crud.Or(nil), false},
		{"negated empty or", crud.Not(crud.Or(nil)), true},
		{"nil alongside false or", crud.Or(nil, crud.False()), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := crud.IsTautology(tc.p); got != tc.want {
				t.Fatalf("IsTautology(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestBooleanConstructorsDoNotRetainACallersSlice(t *testing.T) {
	kids := []crud.Predicate{nil}
	p := crud.And(kids...)
	// If And retained kids, this turns p into a self-reference. Apart from
	// making the public AST mutable, that would recurse forever in rendering and
	// in the bulk-write tautology guard.
	kids[0] = p
	if !crud.IsTautology(p) {
		t.Fatal("the original And(nil) should stay an unconditional predicate")
	}

	orKids := []crud.Predicate{nil}
	q := crud.Or(orKids...)
	orKids[0] = q
	if crud.IsTautology(q) {
		t.Fatal("the original Or(nil) should stay a contradiction")
	}
}

func TestIsTautologyForUsesPrimaryKeyNullability(t *testing.T) {
	m := metaOf[Item](t, "items")
	for _, tc := range []struct {
		name string
		p    crud.Predicate
		want bool
	}{
		{"primary key is not null", crud.IsNotNull("ID"), true},
		{"primary key is not null through Ne", crud.Ne("id", nil), true},
		{"primary key is not null through Not(IsNull)", crud.Not(crud.IsNull("id")), true},
		{"primary key equals itself", crud.EqField("ID", "id"), true},
		{"nullable null complement", crud.Or(crud.IsNull("Note"), crud.IsNotNull("note")), true},
		{"nullable null complement through Not", crud.Or(crud.IsNull("Note"), crud.Not(crud.IsNull("note"))), true},
		{"identity-wrapped null complement", crud.Or(crud.And(crud.True(), crud.IsNull("Note")), crud.Not(crud.IsNull("note"))), true},
		{"primary key equality complement", crud.Or(crud.Eq("ID", int64(1)), crud.Ne("id", int64(1))), true},
		{"primary key range complement", crud.Or(crud.Lt("ID", int64(1)), crud.Gte("id", int64(1))), true},
		{"primary key membership complement", crud.Or(crud.In("ID", int64(1), int64(2)), crud.NotIn("id", int64(1), int64(2))), true},
		{"primary key membership complement ignores list order", crud.Or(crud.In("ID", int64(1), int64(2)), crud.NotIn("id", int64(2), int64(1), int64(1))), true},
		{"primary key negated membership complement ignores list order", crud.Or(
			crud.In("ID", int64(1), int64(2)), crud.Not(crud.In("id", int64(2), int64(1), int64(1)))), true},
		{"primary key propositional tautology", crud.Or(
			crud.Eq("ID", int64(1)),
			crud.And(crud.Not(crud.Eq("id", int64(1))), crud.Eq("ID", int64(2))),
			crud.Not(crud.Eq("id", int64(2))),
		), true},
		{"primary key between complement", crud.Or(
			crud.Between("ID", int64(1), int64(2)),
			crud.Not(crud.And(crud.Gte("id", int64(1)), crud.Lte("id", int64(2)))),
		), true},
		{"primary key membership expansion complement", crud.Or(
			crud.In("ID", int64(1), int64(2)),
			crud.Not(crud.Or(crud.Eq("id", int64(1)), crud.Eq("id", int64(2)))),
		), true},
		{"nullable field is not null", crud.IsNotNull("Note"), false},
		{"nullable field equals itself", crud.EqField("Note", "note"), false},
		{"nullable comparison and its negation", crud.Or(crud.Eq("Note", "x"), crud.Not(crud.Eq("note", "x"))), false},
		{"nullable driver value and its negation", crud.Or(
			crud.Eq("ID", sql.NullInt64{}), crud.Not(crud.Eq("id", sql.NullInt64{}))), false},
		{"named NULL bind and its negation", crud.Or(
			crud.Eq("ID", sql.Named("id", nil)), crud.Not(crud.Eq("id", sql.Named("id", nil)))), false},
		{"unknown field equals itself", crud.EqField("Missing", "Missing"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := crud.IsTautologyFor(m, tc.p); got != tc.want {
				t.Fatalf("IsTautologyFor(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestIsTautologyForRecognisesLikeComplementsOnAStringPrimaryKey(t *testing.T) {
	type NaturalKey struct {
		ID string `db:"id,pk"`
	}
	m := metaOf[NaturalKey](t, "natural_keys")
	p := crud.Or(crud.Like("ID", "%a%"), crud.NotLike("id", "%a%"))
	if !crud.IsTautologyFor(m, p) {
		t.Fatal("LIKE/NOT LIKE complement on a non-NULL primary key was not recognised")
	}
}

func TestIsTautologyForKeepsNullablePointerChainsAsNarrowing(t *testing.T) {
	m := metaOf[Item](t, "items")
	var inner *int64
	outer := &inner
	for _, value := range []any{outer, sql.Named("id", outer)} {
		p := crud.Or(crud.Eq("ID", value), crud.Not(crud.Eq("id", value)))
		if crud.IsTautologyFor(m, p) {
			t.Fatalf("nullable pointer chain %#v was classified as an unconditional predicate", value)
		}
	}
}

func TestIsTautologyForNormalisesANonNilPointerBind(t *testing.T) {
	m := metaOf[Item](t, "items")
	id := int64(7)
	p := crud.Or(crud.Eq("ID", &id), crud.Not(crud.Eq("id", id)))
	if !crud.IsTautologyFor(m, p) {
		t.Fatal("database/sql-equivalent pointer and value binds were not recognised")
	}
}

func TestIsTautologyForNormalisesDatabaseSQLPrimitiveAliases(t *testing.T) {
	m := metaOf[Item](t, "items")
	type localID int64
	p := crud.Or(crud.Eq("ID", localID(7)), crud.Not(crud.Eq("id", int64(7))))
	if !crud.IsTautologyFor(m, p) {
		t.Fatal("database/sql-equivalent numeric aliases were not recognised")
	}
}

func TestIsTautologyForNormalisesDatabaseSQLNamedAndByteSliceBinds(t *testing.T) {
	m := metaOf[Item](t, "items")
	type localBytes []byte
	for _, value := range []any{
		sql.Named("id", int64(7)),
		sql.RawBytes{7},
		localBytes{7},
	} {
		t.Run(fmt.Sprintf("%T", value), func(t *testing.T) {
			p := crud.Or(crud.Eq("ID", value), crud.Not(crud.Eq("id", value)))
			if !crud.IsTautologyFor(m, p) {
				t.Fatalf("database/sql-equivalent bind %T was not recognised", value)
			}
		})
	}

	nested := sql.Named("outer", sql.Named("inner", int64(7)))
	p := crud.Or(crud.Eq("ID", nested), crud.Not(crud.Eq("id", nested)))
	if crud.IsTautologyFor(m, p) {
		t.Fatal("nested sql.Named arguments are rejected by database/sql and must not be classified as unconditional")
	}

	named := sql.Named("id", int64(7))
	p = crud.Or(crud.Eq("ID", &named), crud.Not(crud.Eq("id", &named)))
	if crud.IsTautologyFor(m, p) {
		t.Fatal("a pointer to sql.Named is rejected by database/sql and must not be classified as unconditional")
	}
}

func TestMayBeTautologyForFailsClosedOnAnOpaqueDriverValuer(t *testing.T) {
	m := metaOf[Item](t, "items")
	for _, tc := range []struct {
		name  string
		left  any
		right any
	}{
		{"same value", stableDriverValue{value: 7}, stableDriverValue{value: 7}},
		{"distinct values that may convert alike", stableDriverValue{value: 7}, otherDriverValue{}},
		{"an opaque value and known value may convert alike", otherDriverValue{}, int64(7)},
		{"a database sql decimal is opaque to the guard", decimalDriverValue{}, decimalDriverValue{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := crud.Or(crud.Eq("ID", tc.left), crud.Ne("id", tc.right))
			if crud.IsTautologyFor(m, p) {
				t.Fatal("an opaque driver.Valuer is not a semantic proof")
			}
			if !crud.MayBeTautologyFor(m, p) {
				t.Fatal("a possible driver.Valuer tautology was not marked for a bulk-write guard")
			}
		})
	}
}

func TestMayBeTautologyForFailsClosedOnRawSQL(t *testing.T) {
	m := metaOf[Item](t, "items")
	if !crud.MayBeTautologyFor(m, crud.Raw("1 = 1")) {
		t.Fatal("raw SQL should be refused by a specification bulk-write guard")
	}
}

func TestMayBeTautologyForBoundsLargeBooleanSpecifications(t *testing.T) {
	m := metaOf[Item](t, "items")
	terms := make([]crud.Predicate, 513)
	values := make([]any, 513)
	for i := range terms {
		terms[i] = crud.Eq("ID", int64(i))
		values[i] = int64(i)
	}
	if !crud.MayBeTautologyFor(m, crud.Or(terms...)) {
		t.Fatal("a boolean formula beyond the BDD budget must be refused by a bulk-write guard")
	}
	if crud.MayBeTautologyFor(m, crud.In("ID", values...)) {
		t.Fatal("a large but non-boolean In predicate should remain an allowed bulk narrowing")
	}

	p := crud.Eq("ID", int64(1))
	for i := 0; i <= 64; i++ {
		p = crud.And(p, crud.Eq("ID", int64(i+2)))
	}
	if !crud.MayBeTautologyFor(m, p) {
		t.Fatal("a boolean formula beyond the nesting budget must be refused by a bulk-write guard")
	}
}

// nil is NULL only where SQL has a spelling for it. Eq and Ne rewrite; every
// other operator binds the nil, because `x > NULL` is a legal comparison that
// simply never matches, and inventing an IS NULL there would change the answer.
func TestNilValuesBecomeISNULLOnlyForEquality(t *testing.T) {
	m := metaOf[Item](t, "items")

	for _, tc := range []struct {
		name string
		p    crud.Predicate
		sql  string
		args []any
	}{
		{"an untyped nil", crud.Eq("Name", nil), `"name" IS NULL`, nil},
		{"a typed nil pointer", crud.Eq("Ratio", (*float64)(nil)), `"ratio" IS NULL`, nil},
		{"a nil slice", crud.Eq("Tags", []byte(nil)), `"tags" IS NULL`, nil},
		{"a nil map", crud.Eq("Name", map[string]string(nil)), `"name" IS NULL`, nil},
		// An empty slice is a value: it is the difference between "no bytes"
		// and "no value at all".
		{"an empty slice is a value", crud.Eq("Tags", []byte{}), `"tags" = $1`, []any{[]byte{}}},
		{"Ne of nil is IS NOT NULL", crud.Ne("Ratio", (*float64)(nil)), `"ratio" IS NOT NULL`, nil},
		{"an ordering comparison binds the nil", crud.Gt("Qty", nil), `"qty" > $1`, []any{nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkRender(t, crud.Postgres{}, m, tc.p, tc.sql, tc.args)
		})
	}
}

// BETWEEN is `low <= x AND x <= high`, so bounds handed over the wrong way
// round match nothing. Swapping them for the caller would turn a query that
// returns nothing into one that returns everything in range — a wrong answer is
// worse than an empty one.
func TestBetweenKeepsTheBoundsInTheOrderItWasGiven(t *testing.T) {
	checkRender(t, crud.Postgres{}, articleMeta(t), crud.Between("Views", 10, 1),
		`"views" BETWEEN $1 AND $2`, []any{10, 1})
}

// Raw is the one place a caller writes SQL by hand, so the count of markers and
// the count of arguments have to agree. Neither mismatch can be let through:
// too few arguments leaves a marker unbound, and too many silently drops a
// value the caller believed was in the statement.
func TestRawArgumentsHaveToMatchItsMarkers(t *testing.T) {
	m := articleMeta(t)

	for _, tc := range []struct {
		name string
		p    crud.Predicate
		ok   bool
		sql  string
		args []any
	}{
		{"as many arguments as markers", crud.Raw(`"views" > ? AND "title" <> ?`, 1, "x"), true,
			`"views" > $1 AND "title" <> $2`, []any{1, "x"}},
		{"?? is an escape and consumes nothing", crud.Raw(`"title" LIKE '%??%' AND "views" > ?`, 1), true,
			`"title" LIKE '%?%' AND "views" > $1`, []any{1}},
		{"no markers and no arguments", crud.Raw(`"views" IS NOT NULL`), true, `"views" IS NOT NULL`, nil},
		{"more markers than arguments", crud.Raw(`"views" BETWEEN ? AND ?`, 1), false, "", nil},
		{"more arguments than markers", crud.Raw(`"views" > ?`, 1, 2), false, "", nil},
		{"a native placeholder is not a marker, so its argument is orphaned",
			crud.Raw(`"views" > $1`, 1), false, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := crud.NewSQL(crud.Postgres{}, m).Predicate(tc.p).Done()
			if !tc.ok {
				var se *crud.SchemaError
				if !errors.As(err, &se) {
					t.Fatalf("err = %v, want a SchemaError about the marker count", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if sql != tc.sql || !reflect.DeepEqual(args, tc.args) {
				t.Fatalf("(%s, %#v)\nwant (%s, %#v)", sql, args, tc.sql, tc.args)
			}
		})
	}
}

// A filter naming a field the model does not have is reported, and — this is
// the part that matters — its value never reaches the argument list. A filter
// that cannot be resolved must not be able to smuggle anything into the
// statement it was going to appear in.
func TestAPredicateOnAnUnknownFieldBindsNothing(t *testing.T) {
	m := articleMeta(t)

	for _, tc := range []struct {
		name string
		p    crud.Predicate
	}{
		{"an equality", crud.Eq("Nope", "secret")},
		{"an ordering comparison", crud.Gt("Nope", "secret")},
		{"a LIKE", crud.Contains("Nope", "secret")},
		{"an IN list", crud.In("Nope", "secret", "also secret")},
		{"a BETWEEN", crud.Between("Nope", "secret", "secret")},
		{"an unknown field behind a relation that exists", crud.Eq("Author.Nope", "secret")},
		{"one operand of a column comparison", crud.EqField("Title", "Nope")},
		{"inside an AND with a valid sibling", crud.And(crud.Eq("Title", "Go"), crud.Eq("Nope", "secret"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := crud.NewSQL(crud.Postgres{}, m).Predicate(tc.p).Done()
			var unknown *crud.UnknownFieldError
			if !errors.As(err, &unknown) {
				t.Fatalf("err = %v, want an UnknownFieldError", err)
			}
			for _, a := range args {
				if strings.Contains(fmt.Sprint(a), "secret") {
					t.Fatalf("the value of an unresolvable filter reached the statement: %s %#v", sql, args)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// update plans

// Session carries one of every field kind an update DTO can collide with.
type Session struct {
	ID       int64            `db:"id,pk,auto"`
	TenantID int64            `db:"tenant_id,immutable"`
	Token    string           `db:"token"`
	Score    int              `db:"score"`
	Note     crud.Opt[string] `db:"note"`
	SeenAt   time.Time        `db:"seen_at"`
	Digest   string           `db:"digest,generated"`
}

func sessionSchema(t *testing.T) *crud.Schema {
	t.Helper()
	s, err := crud.SchemaOf[Session]()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func planErrOf[U any](s *crud.Schema) func() error {
	return func() error {
		_, err := crud.PlanFor[U](s)
		return err
	}
}

// A DTO that cannot be applied is refused where it is declared — at Define
// time — rather than on the first request that happens to use the field.
func TestPlanRefusesDTOsThatCannotBeApplied(t *testing.T) {
	s := sessionSchema(t)

	type TouchesKey struct{ ID int64 }
	type TouchesImmutable struct{ TenantID int64 }
	type TouchesGenerated struct{ Digest string }
	type Invented struct{ Nope string }
	type WrongType struct{ Score string }
	type WrongTypeBehindAPointer struct{ Score *string }
	type WrongTypeBehindAnOpt struct{ Score crud.Opt[string] }
	type RenamedOntoAGeneratedColumn struct {
		X string `db:"Digest"`
	}

	for _, tc := range []struct {
		name   string
		build  func() error
		field  string
		reason string
	}{
		{"the primary key", planErrOf[TouchesKey](s), "ID", "primary key cannot be updated"},
		{"an immutable column", planErrOf[TouchesImmutable](s), "TenantID", "immutable"},
		{"a generated column", planErrOf[TouchesGenerated](s), "Digest", "never written"},
		{"a column the model does not have", planErrOf[Invented](s), "Nope", "no field Nope on model Session"},
		{"a type the column cannot take", planErrOf[WrongType](s), "Score", "type mismatch"},
		{"...through a pointer", planErrOf[WrongTypeBehindAPointer](s), "Score", "type mismatch"},
		{"...through an Opt", planErrOf[WrongTypeBehindAnOpt](s), "Score", "type mismatch"},
		{"a renamed field still lands on the real column",
			planErrOf[RenamedOntoAGeneratedColumn](s), "X", "never written"},
		{"a DTO that is not a struct at all", planErrOf[int](s), "", "must be a struct"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantSchemaError(t, tc.build(), tc.field, tc.reason)
		})
	}
}

// An empty DTO is legal and asks for nothing: no columns, no UPDATE. The
// repository needs that answer rather than an error, because "PATCH with an
// empty body" is a request a client is allowed to make.
func TestADTOWithNothingInItAsksForNoColumns(t *testing.T) {
	s := sessionSchema(t)

	type Nothing struct{}
	type EverythingOptional struct {
		Token *string
		Note  crud.Opt[string]
		Score *int
	}

	empty, err := crud.PlanFor[Nothing](s)
	if err != nil {
		t.Fatal(err)
	}
	cur := Session{ID: 1, Token: "t", Score: 3}
	changes, err := empty.Changes(Nothing{}, &cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("changes = %v, want none from a DTO with no fields", changes)
	}

	p, err := crud.PlanFor[EverythingOptional](s)
	if err != nil {
		t.Fatal(err)
	}
	changes, err = p.Changes(EverythingOptional{}, &cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("changes = %v, want none: every field was left undefined", changes)
	}
	defined, err := p.Defined(EverythingOptional{})
	if err != nil {
		t.Fatal(err)
	}
	if len(defined) != 0 {
		t.Fatalf("defined = %v, want nothing reported as provided", defined)
	}
}

// time.Now() carries a monotonic reading that a value read back from a database
// never has. Comparing those two with == would report a change on every single
// request and rewrite the row forever.
func TestATimeThatOnlyDiffersInItsClockReadingIsNotAChange(t *testing.T) {
	s := sessionSchema(t)
	type Touch struct{ SeenAt time.Time }
	p, err := crud.PlanFor[Touch](s)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	stored := now.Round(0) // what comes back from the database: no monotonic clock
	if now == stored {
		t.Fatal("the fixture is not testing anything: the two times are identical")
	}

	cur := Session{SeenAt: stored}
	if changes, err := p.Changes(Touch{SeenAt: now}, &cur); err != nil || len(changes) != 0 {
		t.Fatalf("changes = %v (%v), want the same instant to count as unchanged", changes, err)
	}

	// The same instant in another zone is the same instant.
	elsewhere := stored.In(time.FixedZone("UTC+2", 2*60*60))
	if changes, err := p.Changes(Touch{SeenAt: elsewhere}, &cur); err != nil || len(changes) != 0 {
		t.Fatalf("changes = %v (%v), want a zone change alone not to rewrite the row", changes, err)
	}

	// ...and a different instant still is one, or the comparison would be
	// letting every update through unwritten.
	later := stored.Add(time.Nanosecond)
	changes, err := p.Changes(Touch{SeenAt: later}, &cur)
	if err != nil || len(changes) != 1 {
		t.Fatalf("changes = %v (%v), want one column written", changes, err)
	}
	if got := changes[0].Value.(time.Time); !got.Equal(later) {
		t.Fatalf("wrote %v, want %v", got, later)
	}

	if !crud.EqualValues(now, stored) || crud.EqualValues(now, later) {
		t.Fatal("EqualValues does not agree with the planner about what a changed time is")
	}
}

// Nullability is a property of the column, not of the Go type: a model field
// spelled `int` says how the value is scanned, not whether the database accepts
// NULL. The planner passes the intent through and lets the database be the one
// that refuses — so an Opt over a plain field is accepted here, and a null
// really does write NULL.
func TestAnOptDTOFieldOverAPlainModelFieldWritesNull(t *testing.T) {
	s := sessionSchema(t)
	type Nullable struct{ Score crud.Opt[int] }

	p, err := crud.PlanFor[Nullable](s)
	if err != nil {
		t.Fatalf("PlanFor = %v; an Opt over a plain column is accepted by design", err)
	}
	cur := Session{Score: 3}
	changes, err := p.Changes(Nullable{Score: crud.Null[int]()}, &cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Value != nil {
		t.Fatalf("changes = %#v, want one column written as NULL", changes)
	}
	p.Apply(changes, &cur)
	if cur.Score != 0 {
		t.Fatalf("Score = %d after applying a NULL, want the zero value in memory", cur.Score)
	}
}

// Every entry point that takes a caller's value reports what it was given
// instead of panicking on it: these are the calls a transport makes with
// whatever arrived on the wire.
func TestPlanReportsBadArgumentsInsteadOfPanicking(t *testing.T) {
	s := sessionSchema(t)
	type Patch struct{ Token *string }
	p, err := crud.PlanFor[Patch](s)
	if err != nil {
		t.Fatal(err)
	}
	dataTransferObject := Patch{Token: ptr("t")}

	t.Run("a nil DTO", func(t *testing.T) {
		_, err := p.Changes(nil, &Session{})
		wantSchemaError(t, err, "", "update called with nil")
	})
	t.Run("a DTO of another type", func(t *testing.T) {
		_, err := p.Changes(struct{ Token *string }{}, &Session{})
		wantSchemaError(t, err, "", "update called with")
	})
	t.Run("a nil model", func(t *testing.T) {
		_, err := p.Changes(dataTransferObject, nil)
		wantSchemaError(t, err, "", "pointer to the model")
	})
	t.Run("a model by value rather than by pointer", func(t *testing.T) {
		_, err := p.Changes(dataTransferObject, Session{})
		wantSchemaError(t, err, "", "pointer to the model")
	})
	t.Run("a pointer to the wrong model", func(t *testing.T) {
		_, err := p.Changes(dataTransferObject, &Article{})
		wantSchemaError(t, err, "", "pointer to the model")
	})
	t.Run("Defined of a foreign DTO", func(t *testing.T) {
		_, err := p.Defined(42)
		wantSchemaError(t, err, "", "update called with int")
	})
	t.Run("DefinedFields of a nil DTO", func(t *testing.T) {
		_, err := crud.DefinedFields(s, nil)
		wantSchemaError(t, err, "", "nil update DTO")
	})
}

// ---------------------------------------------------------------------------
// options

// Options are applied left to right and each one owns exactly one field, so the
// last mention of a field wins. That is what lets a caller override a stored
// query shape by appending to it rather than by rebuilding it.
func TestTheLastOfTwoContradictoryOptionsWins(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options []crud.Option
		limit   int
		offset  int
		page    int
	}{
		{"a second limit replaces the first", []crud.Option{crud.Limit(50), crud.Limit(5)}, 5, 0, 1},
		{"a limit of zero hands the decision back to the repository",
			[]crud.Option{crud.Limit(50), crud.Limit(0)}, 20, 0, 1},
		{"a second page replaces the first", []crud.Option{crud.Page(5), crud.Page(2)}, 20, 20, 2},
		{"an offset of zero stops overriding the page",
			[]crud.Option{crud.Page(3), crud.Offset(99), crud.Offset(0)}, 20, 40, 3},
		// Unpaged is a flag, not a value: there is no option that turns it off,
		// so the order it arrives in does not matter. What it resolves to here
		// is the repository's maximum, which no flag may exceed.
		{"a limit before Unpaged is still ignored", []crud.Option{crud.Limit(10), crud.Unpaged()}, 100, 0, 1},
		{"and after it too", []crud.Option{crud.Unpaged(), crud.Limit(10)}, 100, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limit, offset, page := crud.Build(tc.options...).Resolved(20, 100)
			if limit != tc.limit || offset != tc.offset || page != tc.page {
				t.Fatalf("Resolved = (limit %d, offset %d, page %d), want (%d, %d, %d)",
					limit, offset, page, tc.limit, tc.offset, tc.page)
			}
		})
	}
}

// A negative offset is a syntax error in every dialect, so it must never reach
// the statement — whatever a client managed to send.
func TestANegativeOffsetNeverReachesTheStatement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options []crud.Option
	}{
		{"paged", []crud.Option{crud.Offset(-5)}},
		{"unpaged", []crud.Option{crud.Unpaged(), crud.Offset(-5)}},
		{"with a page that would have set one", []crud.Option{crud.Page(1), crud.Offset(-5)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, offset, _ := crud.Build(tc.options...).Resolved(20, 100); offset != 0 {
				t.Fatalf("offset = %d, want 0", offset)
			}
		})
	}
}

// The maximum is the repository's own ceiling: it clamps the caller's limit and
// its own default alike, so a misconfigured default cannot serve a bigger page
// than the maximum promises.
func TestTheMaximumClampsTheDefaultToo(t *testing.T) {
	if limit, _, _ := crud.Build().Resolved(200, 100); limit != 100 {
		t.Fatalf("limit = %d, want the default clamped to the maximum", limit)
	}
	// A repository with no page size at all cannot paginate: every page is the
	// whole table, which is why Define insists on a default.
	limit, offset, page := crud.Build(crud.Page(3)).Resolved(0, 0)
	if limit != 0 || offset != 0 || page != 3 {
		t.Fatalf("Resolved = (%d, %d, %d), want an unlimited repository to ignore paging", limit, offset, page)
	}
}
