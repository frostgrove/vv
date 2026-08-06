package crud_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"rx-crud/crud"
)

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
	dto := Patch{Token: ptr("t")}

	t.Run("a nil DTO", func(t *testing.T) {
		_, err := p.Changes(nil, &Session{})
		wantSchemaError(t, err, "", "update called with nil")
	})
	t.Run("a DTO of another type", func(t *testing.T) {
		_, err := p.Changes(struct{ Token *string }{}, &Session{})
		wantSchemaError(t, err, "", "update called with")
	})
	t.Run("a nil model", func(t *testing.T) {
		_, err := p.Changes(dto, nil)
		wantSchemaError(t, err, "", "pointer to the model")
	})
	t.Run("a model by value rather than by pointer", func(t *testing.T) {
		_, err := p.Changes(dto, Session{})
		wantSchemaError(t, err, "", "pointer to the model")
	})
	t.Run("a pointer to the wrong model", func(t *testing.T) {
		_, err := p.Changes(dto, &Article{})
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
		name   string
		opts   []crud.Option
		limit  int
		offset int
		page   int
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
			limit, offset, page := crud.Build(tc.opts...).Resolved(20, 100)
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
		name string
		opts []crud.Option
	}{
		{"paged", []crud.Option{crud.Offset(-5)}},
		{"unpaged", []crud.Option{crud.Unpaged(), crud.Offset(-5)}},
		{"with a page that would have set one", []crud.Option{crud.Page(1), crud.Offset(-5)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, offset, _ := crud.Build(tc.opts...).Resolved(20, 100); offset != 0 {
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
