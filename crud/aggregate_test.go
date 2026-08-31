package crud_test

import (
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
)

func aggRow(name string, v any) crud.AggregateRow {
	return crud.AggregateRow{Value: map[string]any{name: v}}
}

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

func TestAnAbsentAggregateAndOneThatIsZeroAreDifferentAnswers(t *testing.T) {
	present := crud.AggregateRow{Value: map[string]any{"total": int64(0), "mean": 0.0}}

	if n, ok := present.Int("total"); !ok || n != 0 {
		t.Fatalf("a total of zero read as (%d, %v), want (0, true) — a real zero must still be present", n, ok)
	}
	if f, ok := present.Float("mean"); !ok || f != 0 {
		t.Fatalf("an average of zero read as (%v, %v), want (0, true) — a real zero must still be present", f, ok)
	}

	if n, ok := present.Int("nosuch"); ok || n != 0 {
		t.Fatalf("a name no aggregation answers to read as (%d, %v), want (0, false)", n, ok)
	}
	if f, ok := present.Float("nosuch"); ok || f != 0 {
		t.Fatalf("a name no aggregation answers to read as (%v, %v), want (0, false)", f, ok)
	}

	var empty crud.AggregateRow
	if _, ok := empty.Float("total"); ok {
		t.Fatal("the zero AggregateRow answered for an aggregate it does not carry")
	}
}

func schemaErr(err error) bool   { return errors.As(err, new(*crud.SchemaError)) }
func unknownName(err error) bool { return errors.As(err, new(*crud.UnknownFieldError)) }

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

func TestTheAggregateFunctionIsRenderedInTheCaseTheEnginesSpellIt(t *testing.T) {
	m := articleMeta(t)
	spec := crud.AggregateSpec{Aggregations: []crud.Aggregation{{As: "total", Fn: "sum", Field: "Views"}}}

	b := crud.NewSQL(crud.Postgres{}, m)
	spec.Render(b)

	done(t, b, `SUM("views")`)
}

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
		spec := crud.AggregateSpec{Aggregations: []crud.Aggregation{
			crud.Sum("total", "Comments.ArticleID"),
		}}
		if err := spec.Validate(m); err == nil {
			t.Fatal("an aggregate over a relation path validated; Render turns it into SUM over a subquery")
		}
	})

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

func TestAggregateSortNamesOnlyGroupedModelColumns(t *testing.T) {
	m := articleMeta(t)
	spec := crud.AggregateSpec{
		GroupBy:      []string{"AuthorID"},
		Aggregations: []crud.Aggregation{crud.CountAll("n")},
	}

	if err := spec.ValidateSort(m, []crud.Order{crud.Desc("AuthorID")}); err != nil {
		t.Fatalf("group sort refused: %v", err)
	}
	if err := spec.ValidateSort(m, []crud.Order{crud.Desc("Views")}); !schemaErr(err) {
		t.Fatalf("ungrouped sort err = %T %v, want SchemaError", err, err)
	}
	if err := spec.ValidateSort(m, []crud.Order{crud.Desc("missing")}); !unknownName(err) {
		t.Fatalf("unknown sort err = %T %v, want UnknownFieldError", err, err)
	}
}
