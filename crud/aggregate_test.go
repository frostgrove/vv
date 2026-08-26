package crud_test

import (
	"errors"
	"testing"
	"time"

	"github.com/shardit-io/vv/crud"
)

// ---------------------------------------------------------------------------
// reading a summary back
//
// The driver decides the shape. PostgreSQL hands an AVG back as a float64 and a
// NUMERIC as a string; MySQL hands a SUM over a DECIMAL back as []byte; a SUM
// over an integer column arrives as an int64 on both. A caller writing
// row.Float("total") knows none of that and should not have to, so every shape
// a driver produces is part of the contract rather than a fallback.

func aggRow(name string, v any) crud.AggregateRow {
	return crud.AggregateRow{Value: map[string]any{name: v}}
}

// Every shape a driver puts a numeric aggregate in reads back as the number it
// stands for.
func TestAFloatAggregateIsReadWhateverShapeTheDriverReturned(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  any
		want float64
	}{
		{"a float64, which is what an AVG usually is", 12.5, 12.5},
		{"a float32", float32(0.5), 0.5},
		{"an int64, which is what a SUM over an integer column is", int64(30), 30},
		{"an int", 30, 30},
		{"[]byte, which is MySQL's SUM over a DECIMAL", []byte("12.5"), 12.5},
		{"a string, which is lib/pq's NUMERIC", "12.5", 12.5},
		{"text in exponent form", []byte("1.25e2"), 125},
		{"a negative total in text", "-0.25", -0.25},
		{"an integral value in text", "30", 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := aggRow("total", tc.raw).Float("total")
			if !ok {
				t.Fatalf("a %T came back unreadable, so a SUM through this driver reports nothing at all", tc.raw)
			}
			if got != tc.want {
				t.Fatalf("a %T read as %v, want %v — the coercion changed the number", tc.raw, got, tc.want)
			}
		})
	}
}

// The same value read as an integer and as a float is the same number. The two
// accessors are separate coercion paths over the same driver shapes, and a
// shape handled by one and not the other is a total that comes back zero on
// exactly one engine.
func TestASumReadsAsTheSameNumberThroughIntAndFloat(t *testing.T) {
	for _, raw := range []any{int64(30), 30, 30.0, float32(30), "30", []byte("30")} {
		n, okInt := aggRow("total", raw).Int("total")
		f, okFloat := aggRow("total", raw).Float("total")
		if !okInt || !okFloat {
			t.Fatalf("a %T is readable as an integer=%v and as a float=%v — one of the two coercions is missing a shape the drivers produce",
				raw, okInt, okFloat)
		}
		if float64(n) != f || n != 30 {
			t.Fatalf("a %T reads as %d through Int and %v through Float, want 30 both ways", raw, n, f)
		}
	}
}

// A value that is not a number is refused rather than read as zero. This is the
// half that makes the test above mean something: a coercion that answered "ok"
// for everything would pass it and would turn a wrong column into a silent
// zero.
func TestAnAggregateThatIsNotANumberIsRefusedRatherThanReadAsZero(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  any
	}{
		{"NULL, which is what a SUM over no rows is", nil},
		{"empty text", ""},
		{"text that is not a number", "abc"},
		{"a decimal comma", []byte("12,5")},
		{"a boolean", true},
		{"a time", time.Unix(0, 0)},
		{"a string with a trailing unit", "12.5kg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := aggRow("total", tc.raw)
			if n, ok := r.Int("total"); ok || n != 0 {
				t.Fatalf("Int read %s as %d and reported success — a caller cannot tell that apart from a real total", tc.name, n)
			}
			if f, ok := r.Float("total"); ok || f != 0 {
				t.Fatalf("Float read %s as %v and reported success — a caller cannot tell that apart from a real total", tc.name, f)
			}
		})
	}
}

// "There is no such aggregate" and "the aggregate is zero" are different
// answers, and the second return value is the only place the difference is
// visible. A caller that only reads the number cannot tell a misspelt name from
// a group that really summed to nothing.
func TestAnAbsentAggregateAndOneThatIsZeroAreDifferentAnswers(t *testing.T) {
	present := crud.AggregateRow{Value: map[string]any{"total": int64(0), "mean": 0.0}}

	if n, ok := present.Int("total"); !ok || n != 0 {
		t.Fatalf("a total of zero read as (%d, %v), want (0, true) — a real zero must still be present", n, ok)
	}
	if f, ok := present.Float("mean"); !ok || f != 0 {
		t.Fatalf("an average of zero read as (%v, %v), want (0, true) — a real zero must still be present", f, ok)
	}

	// The control: the same zero from a name that was never asked for.
	if n, ok := present.Int("nosuch"); ok || n != 0 {
		t.Fatalf("a name no aggregation answers to read as (%d, %v), want (0, false)", n, ok)
	}
	if f, ok := present.Float("nosuch"); ok || f != 0 {
		t.Fatalf("a name no aggregation answers to read as (%v, %v), want (0, false)", f, ok)
	}

	// And a row with no aggregates at all, which is what a caller gets from the
	// zero AggregateRow — the map is nil, not empty.
	var empty crud.AggregateRow
	if _, ok := empty.Float("total"); ok {
		t.Fatal("the zero AggregateRow answered for an aggregate it does not carry")
	}
}

// ---------------------------------------------------------------------------
// the refusal path

func schemaErr(err error) bool   { return errors.As(err, new(*crud.SchemaError)) }
func unknownName(err error) bool { return errors.As(err, new(*crud.UnknownFieldError)) }

// Validate is where an aggregate read is refused, and it refuses eagerly: a
// name that does not resolve is an error, never a clause that quietly
// disappears from the projection.
func TestValidateRefusesAnAggregateNoEngineCouldAnswer(t *testing.T) {
	m := articleMeta(t)

	for _, tc := range []struct {
		name   string
		spec   crud.AggregateSpec
		accept bool
		is     func(error) bool
	}{
		{name: "COUNT(*) needs no field",
			spec: crud.AggregateSpec{Aggregations: []crud.Aggregation{crud.CountAll("n")}}, accept: true},
		{name: "a sum, an average and a distinct count over real fields",
			spec: crud.AggregateSpec{
				GroupBy: []string{"AuthorID"},
				Aggregations: []crud.Aggregation{
					crud.Sum("total", "Views"), crud.Avg("mean", "Views"), crud.CountDistinct("authors", "AuthorID"),
				}}, accept: true},
		{name: "the function may be spelled in lower case",
			spec: crud.AggregateSpec{Aggregations: []crud.Aggregation{{As: "total", Fn: "sum", Field: "Views"}}}, accept: true},
		{name: "the field may be spelled as the column",
			spec: crud.AggregateSpec{Aggregations: []crud.Aggregation{crud.Sum("total", "views")}}, accept: true},

		{name: "grouping alone is not an aggregate read",
			spec: crud.AggregateSpec{GroupBy: []string{"AuthorID"}}, is: schemaErr},
		{name: "an aggregation with no name has nothing to come back under",
			spec: crud.AggregateSpec{Aggregations: []crud.Aggregation{{Fn: "COUNT"}}}, is: schemaErr},
		{name: "two aggregations under one name would overwrite each other",
			spec: crud.AggregateSpec{Aggregations: []crud.Aggregation{
				crud.CountAll("n"), crud.Sum("n", "Views")}}, is: schemaErr},
		{name: "a function outside the closed set",
			spec: crud.AggregateSpec{Aggregations: []crud.Aggregation{{As: "p50", Fn: "MEDIAN", Field: "Views"}}}, is: schemaErr},
		{name: "a function name carrying a second expression",
			spec: crud.AggregateSpec{Aggregations: []crud.Aggregation{
				{As: "n", Fn: "COUNT(*), (SELECT password FROM users)", Field: "Views"}}}, is: schemaErr},
		{name: "SUM has nothing to sum without a field",
			spec: crud.AggregateSpec{Aggregations: []crud.Aggregation{{As: "total", Fn: "SUM"}}}, is: schemaErr},
		{name: "AVG has nothing to average without a field",
			spec: crud.AggregateSpec{Aggregations: []crud.Aggregation{{As: "mean", Fn: "AVG"}}}, is: schemaErr},
		{name: "a field the model does not declare",
			spec: crud.AggregateSpec{Aggregations: []crud.Aggregation{crud.Sum("total", "Nope")}}, is: unknownName},
		{name: "a grouping column the model does not declare",
			spec: crud.AggregateSpec{
				GroupBy:      []string{"Nope"},
				Aggregations: []crud.Aggregation{crud.CountAll("n")}}, is: unknownName},
		{name: "a grouping column that names a relation rather than a column",
			spec: crud.AggregateSpec{
				GroupBy:      []string{"Comments"},
				Aggregations: []crud.Aggregation{crud.CountAll("n")}}, is: schemaErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate(m)
			if tc.accept {
				if err != nil {
					t.Fatalf("a legitimate aggregate read was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("the aggregate was accepted, so it reaches the statement and the engine refuses it instead")
			}
			if !tc.is(err) {
				t.Fatalf("refused with %T (%v), want the declaration error the rest of the library raises", err, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// the projection

// The grouping columns come first and each aggregate renders under the function
// it names. CountOf sits next to CountDistinct on purpose: it is the control
// for the DISTINCT keyword, so a renderer that emitted DISTINCT for every count
// fails here rather than shipping a wrong number.
func TestTheProjectionRendersEachAggregateUnderTheFunctionItNames(t *testing.T) {
	m := articleMeta(t)
	spec := crud.AggregateSpec{
		GroupBy: []string{"AuthorID"},
		Aggregations: []crud.Aggregation{
			crud.CountAll("rows"),
			crud.CountOf("scored", "Views"),
			crud.CountDistinct("authors", "AuthorID"),
			crud.Avg("mean", "Views"),
			crud.Sum("total", "Views"),
			crud.Min("lowest", "Views"),
			crud.Max("highest", "Views"),
		},
	}

	b := crud.NewSQL(crud.Postgres{}, m)
	b.Raw("SELECT ")
	spec.Render(b)
	b.Raw(" FROM ").Table()

	done(t, b, `SELECT "author_id", COUNT(*), COUNT("views"), COUNT(DISTINCT "author_id"), `+
		`AVG("views"), SUM("views"), MIN("views"), MAX("views") FROM "articles"`)
}

// Without a grouping column the projection starts with the first aggregate and
// no leading comma — the separator rule has two halves and only this case
// exercises the second.
func TestAnAggregateProjectionWithoutGroupingStartsAtTheFirstAggregate(t *testing.T) {
	m := articleMeta(t)
	spec := crud.AggregateSpec{Aggregations: []crud.Aggregation{
		crud.CountAll("rows"), crud.Sum("total", "Views"),
	}}

	b := crud.NewSQL(crud.Postgres{}, m)
	b.Raw("SELECT ")
	spec.Render(b)
	b.Raw(" FROM ").Table()

	done(t, b, `SELECT COUNT(*), SUM("views") FROM "articles"`)
}

// The function reaches the statement upper-cased whatever case it was declared
// in, so the closed set in Validate and the text in the statement are the same
// set of names.
func TestTheAggregateFunctionIsRenderedInTheCaseTheEnginesSpellIt(t *testing.T) {
	m := articleMeta(t)
	spec := crud.AggregateSpec{Aggregations: []crud.Aggregation{{As: "total", Fn: "sum", Field: "Views"}}}

	b := crud.NewSQL(crud.Postgres{}, m)
	spec.Render(b)

	done(t, b, `SUM("views")`)
}

// The two options accumulate rather than replace, so a caller can add a summary
// column in one place and the grouping in another.
func TestASecondAggregateAddsToTheFirstRatherThanReplacingIt(t *testing.T) {
	var o crud.Options
	crud.Aggregate(crud.CountAll("rows"))(&o)
	crud.Aggregate(crud.Sum("total", "Views"), crud.Avg("mean", "Views"))(&o)
	crud.GroupBy("AuthorID")(&o)
	crud.GroupBy("Title")(&o)

	if n := len(o.Agg.Aggregations); n != 3 {
		t.Fatalf("%d aggregations survived, want 3 — a second Aggregate replaced the first", n)
	}
	if got := []string{o.Agg.Aggregations[0].As, o.Agg.Aggregations[2].As}; got[0] != "rows" || got[1] != "mean" {
		t.Fatalf("the aggregations came out in the order %v, want the order they were declared in", got)
	}
	if n := len(o.Agg.GroupBy); n != 2 {
		t.Fatalf("%d grouping columns survived, want 2 — a second GroupBy replaced the first", n)
	}
}

// The three shapes Validate used to let through, each of which reached an
// engine as a statement this package built and no engine parses.
//
// All three came out of writing the tests above: a refusal path with no test is
// a refusal path nobody has read, and these are what was behind it.
func TestValidateRefusesTheThreeShapesThatReachedTheDriver(t *testing.T) {
	m := articleMeta(t)

	t.Run("COUNT(DISTINCT) with no field", func(t *testing.T) {
		spec := crud.AggregateSpec{Aggregations: []crud.Aggregation{
			{As: "n", Fn: "COUNT", Distinct: true},
		}}
		if err := spec.Validate(m); err == nil {
			t.Fatal("COUNT(DISTINCT *) validated; no engine parses it, so the refusal came back from the driver as a 500")
		}
	})

	t.Run("an aggregate over a relation path", func(t *testing.T) {
		// FieldAt resolves this happily — it is a real path — and Render then
		// writes it with w.Column, which expands a path into a correlated
		// EXISTS. The result was SUM(EXISTS (SELECT 1 FROM ...)).
		spec := crud.AggregateSpec{Aggregations: []crud.Aggregation{
			crud.Sum("total", "Comments.ArticleID"),
		}}
		if err := spec.Validate(m); err == nil {
			t.Fatal("an aggregate over a relation path validated; Render turns it into SUM over a subquery")
		}
	})

	// The control, and it is the point: the two refusals above must not have
	// been bought by refusing aggregates generally. Both of these are ordinary
	// and must still pass.
	t.Run("control: the ordinary shapes still validate", func(t *testing.T) {
		for _, spec := range []crud.AggregateSpec{
			{Aggregations: []crud.Aggregation{crud.CountAll("n")}},
			{Aggregations: []crud.Aggregation{crud.CountDistinct("authors", "AuthorID")}},
			{Aggregations: []crud.Aggregation{crud.Sum("total", "Views")}},
		} {
			if err := spec.Validate(m); err != nil {
				t.Fatalf("an ordinary aggregate was refused: %v", err)
			}
		}
	})
}
