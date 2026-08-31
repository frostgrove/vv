package probe

import (
	"reflect"
	"strconv"
	"strings"
	"time"
)

type finding struct {
	row  int
	cand candidate
}

func (this *full) duplicates(p plan) []finding {
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

func render(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case time.Time:
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
