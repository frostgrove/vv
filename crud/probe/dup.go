package probe

import (
	"reflect"
	"strconv"
	"strings"
	"time"
)

// finding is one violation the probe located, before it becomes public.
type finding struct {
	// row is the position in the payload, or -1 for a write of one row where no
	// index belongs in the path.
	row  int
	cand candidate
}

// duplicates finds rows of one batch that carry the same unique key as another
// row of the same batch. The database reports one of them; both are wrong.
//
// This is the one check that belongs in Go. It is a fact about the payload
// rather than about the database, so there is no second implementation of a
// rule to disagree with the server's — the objection that keeps NOT NULL,
// length and CHECK out of this package entirely.
//
// It is also only ever narrowing, which is the thing that makes byte equality
// safe here. A collation equates *more* values than byte equality does — case
// insensitively, ignoring trailing spaces — and never fewer. Two rows this
// finds equal are equal to the server too; the pairs it misses the statement
// is left to find.
//
// Only the constraints the plan already probes are keyed, so a partial index is
// not replayed here either.
func (f *full) duplicates(p plan) []finding {
	if p.mode != modeBulk || len(p.rows) < 2 {
		return nil
	}
	var out []finding
	for _, c := range p.cands {
		if c.kind != kindUnique {
			continue
		}
		seen := map[string][]int{}
		order := make([]string, 0, len(p.rows))
		for i, row := range p.rows {
			k, ok := keyOf(row, c.cols)
			if !ok {
				continue
			}
			if _, dup := seen[k]; !dup {
				order = append(order, k)
			}
			seen[k] = append(seen[k], i)
		}
		// Walked in first-appearance order rather than map order, so the same
		// payload twice produces the same list ([[D-014]] one layer up).
		for _, k := range order {
			rows := seen[k]
			if len(rows) < 2 {
				continue
			}
			for _, i := range rows {
				out = append(out, finding{row: i, cand: c})
			}
		}
	}
	return out
}

// keyOf builds a comparable key out of one row's values for one constraint, or
// reports that the row cannot be keyed.
//
// A NULL part makes it unkeyable, because under the default NULLS DISTINCT two
// rows that are NULL there do not collide. A non-comparable part makes it
// unkeyable too, and that is [[D-025]]'s bug written down rather than repeated:
// falling through to a per-type constant would give every such row the same key
// and report the whole batch as duplicates of each other.
//
// The encoding is type-qualified and length-prefixed so two different tuples
// cannot render the same string.
func keyOf(row Row, cols []string) (string, bool) {
	var b strings.Builder
	for _, col := range cols {
		v, ok := row.Values[col]
		if !ok || v == nil {
			return "", false
		}
		t := reflect.TypeOf(v)
		if !t.Comparable() {
			return "", false
		}
		s, ok := render(v)
		if !ok {
			return "", false
		}
		b.WriteString(t.String())
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
	}
	return b.String(), true
}

// render turns one key part into text, reporting false for anything it cannot
// render faithfully. False makes the row unkeyable, which is the narrowing
// answer — the alternative is [[D-025]]'s bug, where every value of a type it
// could not render collapsed onto one key and every row became a duplicate of
// every other.
func render(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case time.Time:
		// Compared as an instant and never with ==, which sees the monotonic
		// reading a wall clock does not.
		return x.UTC().Format(time.RFC3339Nano), true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'g', -1, 64), true
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), true
	case reflect.String:
		return rv.String(), true
	}
	return "", false
}
