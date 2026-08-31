package crud

import (
	"fmt"
	"strconv"
	"strings"
)

type Aggregation struct {
	As string

	Fn string

	Field string

	Distinct bool
}

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

type AggregateRow struct {
	Group map[string]any
	Value map[string]any
}

func (this AggregateRow) Int(name string) (int64, bool) {
	n, err := toInt64(this.Value[name])
	return n, err == nil
}

func (this AggregateRow) Float(name string) (float64, bool) {
	f, err := toFloat64(this.Value[name])
	return f, err == nil
}

type AggregateSpec struct {
	Aggregations []Aggregation
	GroupBy      []string
}

func Aggregate(aggs ...Aggregation) Option {
	return func(o *Options) { o.Agg.Aggregations = append(o.Agg.Aggregations, aggs...) }
}

func GroupBy(fields ...string) Option {
	return func(o *Options) { o.Agg.GroupBy = append(o.Agg.GroupBy, fields...) }
}

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

		if ag.Field == "" && ag.Distinct {
			return &SchemaError{Model: m.Name, Field: ag.As,
				Reason: "COUNT(DISTINCT) needs a field; use CountAll for every row"}
		}
		if ag.Field != "" {
			f, _, err := m.FieldAt(ag.Field)
			if err != nil {
				return err
			}

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

		if strings.Contains(g, ".") || f == nil {
			return &SchemaError{Model: m.Name, Field: "groupBy",
				Reason: "a grouping column belongs to this model, not to a path across a relation: " + g}
		}
	}
	return nil
}

func (this AggregateSpec) ValidateSort(m *Meta, orders []Order) error {
	grouped := make(map[*Field]struct{}, len(this.GroupBy))
	for _, name := range this.GroupBy {
		f, _, err := m.FieldAt(name)
		if err != nil {
			return err
		}
		grouped[f] = struct{}{}
	}
	for _, order := range orders {
		f, _, err := m.FieldAt(order.Field)
		if err != nil {
			return err
		}
		if _, ok := grouped[f]; !ok {
			return &SchemaError{Model: m.Name, Field: order.Field,
				Reason: "an aggregate can order by a model column only when that column is grouped"}
		}
	}
	return nil
}

var aggFuncs = map[string]bool{"COUNT": true, "SUM": true, "AVG": true, "MIN": true, "MAX": true}

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
