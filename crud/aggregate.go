package crud

import (
	"fmt"
	"strconv"
	"strings"
)

// Aggregation is one summary column: a function over a field, under a name the
// caller reads the result back by.
//
// The reason these live in the repository rather than in the caller's own SQL is
// not convenience. A `GROUP BY` written by hand runs outside every narrowing the
// repository applies — the permanent scope, the relation scopes, the security
// gate's row filter — so the moment a total is needed, the layer that enforces
// tenancy is the layer that gets bypassed. Counting unread messages should not
// be the query that leaks another tenant's.
type Aggregation struct {
	// As is the key the value comes back under.
	As string
	// Fn is the SQL aggregate: COUNT, SUM, AVG, MIN, MAX.
	Fn string
	// Field is the model field to aggregate, empty for COUNT(*).
	Field string
	// Distinct renders COUNT(DISTINCT x) and friends.
	Distinct bool
}

// The five aggregates every engine spells the same way. A sixth would be a
// dialect question, and this package does not have one to ask.
func CountAll(as string) Aggregation { return Aggregation{As: as, Fn: "COUNT"} }
func CountOf(as, field string) Aggregation {
	return Aggregation{As: as, Fn: "COUNT", Field: field}
}
func CountDistinct(as, field string) Aggregation {
	return Aggregation{As: as, Fn: "COUNT", Field: field, Distinct: true}
}
func Sum(as, field string) Aggregation { return Aggregation{As: as, Fn: "SUM", Field: field} }
func Avg(as, field string) Aggregation { return Aggregation{As: as, Fn: "AVG", Field: field} }
func Min(as, field string) Aggregation { return Aggregation{As: as, Fn: "MIN", Field: field} }
func Max(as, field string) Aggregation { return Aggregation{As: as, Fn: "MAX", Field: field} }

// AggregateRow is one row of a summary: the grouping columns under their model
// field names, and the aggregates under the names they were given.
//
// Values arrive as the driver produced them, which is why the accessors below
// exist — an engine may hand back a SUM as an int64, a string or a []byte
// depending on the column type and the driver, and a caller should not have to
// know which.
type AggregateRow struct {
	Group map[string]any
	Value map[string]any
}

// Int reads an aggregate as an integer, whatever shape the driver chose.
func (this AggregateRow) Int(name string) (int64, bool) {
	n, err := toInt64(this.Value[name])
	return n, err == nil
}

// Float reads an aggregate as a float, for AVG and for a SUM over a decimal.
func (this AggregateRow) Float(name string) (float64, bool) {
	f, err := toFloat64(this.Value[name])
	return f, err == nil
}

// aggregateSpec is what an option accumulates: the aggregates and the grouping.
type AggregateSpec struct {
	Aggregations []Aggregation
	GroupBy      []string
}

// Aggregate adds summary columns to a read.
func Aggregate(aggs ...Aggregation) Option {
	return func(o *Options) { o.Agg.Aggregations = append(o.Agg.Aggregations, aggs...) }
}

// GroupBy adds grouping columns. Every grouping column comes back on the row, so
// a caller never has to repeat itself.
func GroupBy(fields ...string) Option {
	return func(o *Options) { o.Agg.GroupBy = append(o.Agg.GroupBy, fields...) }
}

// validate resolves every name against the model and refuses an aggregate the
// engines do not agree on. It is the same rule as everywhere else: a name that
// does not exist is a refusal, never a clause that quietly disappears.
func (this AggregateSpec) Validate(m *Meta) error {
	if len(this.Aggregations) == 0 {
		return &SchemaError{Model: m.Name, Field: "aggregate",
			Reason: "an aggregate read needs at least one aggregation"}
	}
	seen := make(map[string]bool, len(this.Aggregations))
	for _, ag := range this.Aggregations {
		if ag.As == "" {
			return &SchemaError{Model: m.Name, Field: "aggregate",
				Reason: "every aggregation needs a name to come back under"}
		}
		if seen[ag.As] {
			return &SchemaError{Model: m.Name, Field: ag.As,
				Reason: "two aggregations answer to the same name"}
		}
		seen[ag.As] = true
		if !aggFuncs[strings.ToUpper(ag.Fn)] {
			return &SchemaError{Model: m.Name, Field: ag.As,
				Reason: "unknown aggregate " + ag.Fn}
		}
		if ag.Field == "" && strings.ToUpper(ag.Fn) != "COUNT" {
			return &SchemaError{Model: m.Name, Field: ag.As,
				Reason: ag.Fn + " needs a field"}
		}
		// COUNT(*) counts rows and COUNT(DISTINCT x) counts values; there is no
		// COUNT(DISTINCT *), and no engine parses one. Without this the spec
		// validated and Render wrote it, so the refusal came back from the
		// driver as a 500 for a statement this package built.
		if ag.Field == "" && ag.Distinct {
			return &SchemaError{Model: m.Name, Field: ag.As,
				Reason: "COUNT(DISTINCT) needs a field; use CountAll for every row"}
		}
		if ag.Field != "" {
			f, _, err := m.FieldAt(ag.Field)
			if err != nil {
				return err
			}
			// FieldAt resolves a path across a relation, and Render writes the
			// field with w.Column, which expands such a path into a correlated
			// EXISTS — so `Sum("total", "Comments.ArticleID")` validated and
			// then rendered `SUM(EXISTS (SELECT 1 FROM ...))`. An aggregate is
			// over a column of the row being grouped; reaching another table is
			// a join this API does not express ([[D-029]]).
			if strings.Contains(ag.Field, ".") || f == nil {
				return &SchemaError{Model: m.Name, Field: ag.As,
					Reason: "an aggregate takes a column of this model, not a path across a relation: " + ag.Field}
			}
		}
	}
	for _, g := range this.GroupBy {
		f, _, err := m.FieldAt(g)
		if err != nil {
			return err
		}
		// The same refusal the aggregation loop makes, for the same reason: Render
		// writes a grouping column with w.Column, which expands a relation path
		// into a correlated EXISTS. `GROUP BY EXISTS (SELECT ...)` is a statement
		// this package builds and no engine accepts, so the refusal used to come
		// back from the driver as a 500.
		if strings.Contains(g, ".") || f == nil {
			return &SchemaError{Model: m.Name, Field: "groupBy",
				Reason: "a grouping column belongs to this model, not to a path across a relation: " + g}
		}
	}
	return nil
}

// aggFuncs is closed on purpose. The name is rendered into the statement, so an
// open set would be a second Raw with none of Raw's visibility.
var aggFuncs = map[string]bool{"COUNT": true, "SUM": true, "AVG": true, "MIN": true, "MAX": true}

// render writes the projection: the grouping columns first, then the aggregates.
func (this AggregateSpec) Render(w *SQL) {
	for i, g := range this.GroupBy {
		if i > 0 {
			w.Raw(", ")
		}
		w.Column(g)
	}
	for i, ag := range this.Aggregations {
		if i > 0 || len(this.GroupBy) > 0 {
			w.Raw(", ")
		}
		w.Raw(strings.ToUpper(ag.Fn) + "(")
		if ag.Distinct {
			w.Raw("DISTINCT ")
		}
		if ag.Field == "" {
			w.Raw("*")
		} else {
			w.Column(ag.Field)
		}
		w.Raw(")")
	}
}

func toInt64(v any) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	case int32:
		return int64(n), nil
	case uint64:
		return int64(n), nil
	case float64:
		return int64(n), nil
	case float32:
		return int64(n), nil
	case []byte:
		return parseInt(string(n))
	case string:
		return parseInt(n)
	case nil:
		return 0, fmt.Errorf("crud: aggregate is NULL")
	}
	return 0, fmt.Errorf("crud: cannot read %T as an integer", v)
}

// The integer arms are not padding. A driver decides the Go type from the
// *column*, not from the aggregate: pgx v5 scans an INT4 into an int32 when the
// destination is `any`, so AVG or SUM over an integer column arrives here as an
// int32 and has to read as a float. toInt64 has carried the int32 arm since it
// was written; this one did not, so the same total read correctly through Int
// and came back as "absent" through Float — on one driver, for one column type,
// with nothing to see.
func toFloat64(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int32:
		return float64(n), nil
	case uint64:
		return float64(n), nil
	case []byte:
		return parseFloat(string(n))
	case string:
		return parseFloat(n)
	case nil:
		return 0, fmt.Errorf("crud: aggregate is NULL")
	}
	return 0, fmt.Errorf("crud: cannot read %T as a float", v)
}

// A driver may hand a numeric aggregate back as text — MySQL does it for SUM
// over a DECIMAL, and lib/pq does it for NUMERIC — so the text forms are part of
// the contract rather than a fallback.
func parseInt(s string) (int64, error) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("crud: %q is not a number", s)
	}
	return int64(f), nil
}

func parseFloat(s string) (float64, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("crud: %q is not a number", s)
	}
	return f, nil
}
