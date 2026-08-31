package query

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/frostgrove/vv/crud"
)

func (this *compiler) node(raw json.RawMessage, where string, depth int) (crud.Predicate, error) {
	raw = trim(raw)
	if len(raw) == 0 || isNull(raw) {
		return nil, errf(where, "filter must be a non-empty object")
	}
	if depth > this.config.maxDepth() {
		return nil, errf(where, "filter is nested deeper than the allowed %d levels", this.config.maxDepth())
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, errf(where, "expected an object, got %s", preview(raw))
	}

	if depth == 1 {
		if err := rejectDuplicateJSONKeys(raw); err != nil {
			return nil, err
		}
	}
	if len(obj) == 0 {
		return nil, errf(where, "filter must not be empty")
	}

	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var preds []crud.Predicate
	for _, key := range keys {
		val := obj[key]
		sub := where + "." + key

		logical := !isFilterField(this, key)
		switch strings.ToLower(key) {
		case "$and":
			p, err := this.list(val, sub, depth+1, "AND")
			if err != nil {
				return nil, err
			}
			preds = appendPred(preds, p)
		case "$or":
			p, err := this.list(val, sub, depth+1, "OR")
			if err != nil {
				return nil, err
			}
			preds = appendPred(preds, p)
		case "$not":
			inner, err := this.node(val, sub, depth+1)
			if err != nil {
				return nil, err
			}
			if inner != nil {
				preds = append(preds, crud.Not(inner))
			}
		case "and":
			if !logical {
				p, err := this.condition(key, val, sub)
				if err != nil {
					return nil, err
				}
				preds = appendPred(preds, p)
				continue
			}
			p, err := this.list(val, sub, depth+1, "AND")
			if err != nil {
				return nil, err
			}
			preds = appendPred(preds, p)
		case "or":
			if !logical {
				p, err := this.condition(key, val, sub)
				if err != nil {
					return nil, err
				}
				preds = appendPred(preds, p)
				continue
			}
			p, err := this.list(val, sub, depth+1, "OR")
			if err != nil {
				return nil, err
			}
			preds = appendPred(preds, p)
		case "not":
			if !logical {
				p, err := this.condition(key, val, sub)
				if err != nil {
					return nil, err
				}
				preds = appendPred(preds, p)
				continue
			}
			inner, err := this.node(val, sub, depth+1)
			if err != nil {
				return nil, err
			}
			if inner != nil {
				preds = append(preds, crud.Not(inner))
			}
		default:
			p, err := this.condition(key, val, sub)
			if err != nil {
				return nil, err
			}
			preds = appendPred(preds, p)
		}
	}
	switch len(preds) {
	case 0:
		return nil, nil
	case 1:
		return preds[0], nil
	default:
		return crud.And(preds...), nil
	}
}

func isFilterField(c *compiler, key string) bool {
	if c == nil || c.meta == nil || strings.HasPrefix(key, "$") {
		return false
	}
	_, _, err := c.meta.FieldAt(key)
	return err == nil
}

func appendPred(destination []crud.Predicate, p crud.Predicate) []crud.Predicate {
	if p == nil {
		return destination
	}
	return append(destination, p)
}

func (this *compiler) list(raw json.RawMessage, where string, depth int, op string) (crud.Predicate, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		p, oerr := this.node(raw, where, depth)
		if oerr != nil {
			return nil, errf(where, "expected an array of filter objects, got %s", preview(raw))
		}
		return p, nil
	}
	var preds []crud.Predicate
	for i, item := range items {
		p, err := this.node(item, where+"["+itoa(i)+"]", depth)
		if err != nil {
			return nil, err
		}
		preds = appendPred(preds, p)
	}
	if len(preds) == 0 {
		return nil, errf(where, "%s must contain at least one filter", strings.ToLower(op))
	}
	if op == "OR" {
		return crud.Or(preds...), nil
	}
	return crud.And(preds...), nil
}

func (this *compiler) condition(path string, raw json.RawMessage, where string) (crud.Predicate, error) {
	f, canonical, err := this.path(path, where)
	if err != nil {
		return nil, err
	}
	if !allowed(this.config.filterable(), this.qualify(canonical)) {
		return nil, errf(where, "%s is not filterable", this.qualify(canonical))
	}

	raw = trim(raw)
	if len(raw) == 0 {
		return nil, errf(where, "filter value is missing")
	}

	switch raw[0] {
	case '{':
		return this.operators(canonical, f, raw, where)
	case '[':
		if err := this.count(where); err != nil {
			return nil, err
		}
		vals, err := decodeList(raw, f)
		if err != nil {
			return nil, errf(where, "%s", err)
		}
		if err := this.countValues(len(vals), where); err != nil {
			return nil, err
		}
		return buildMulti(canonical, opIn, vals, where)
	default:
		if err := this.count(where); err != nil {
			return nil, err
		}
		if isNull(raw) {
			return crud.IsNull(canonical), nil
		}
		v, err := decodeValue(raw, f)
		if err != nil {
			return nil, errf(where, "%s", err)
		}
		if err := this.countBinds(1, where); err != nil {
			return nil, err
		}
		return crud.Eq(canonical, v), nil
	}
}

func (this *compiler) operators(field string, f *crud.Field, raw json.RawMessage, where string) (crud.Predicate, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, errf(where, "expected an operator object, got %s", preview(raw))
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil, errf(where, "operator object must not be empty")
	}

	var preds []crud.Predicate
	for _, key := range keys {
		if err := this.count(where); err != nil {
			return nil, err
		}
		p, err := this.operator(field, f, key, obj[key], where+"."+key)
		if err != nil {
			return nil, err
		}
		preds = appendPred(preds, p)
	}
	switch len(preds) {
	case 0:
		return nil, nil
	case 1:
		return preds[0], nil
	default:
		return crud.And(preds...), nil
	}
}

func (this *compiler) operator(field string, f *crud.Field, op string, raw json.RawMessage, where string) (crud.Predicate, error) {
	kind, ok := normalizeOp(op)
	if !ok {
		return nil, errf(where, "unknown operator %q", op)
	}

	if isNull(trim(raw)) && (kind.unary() || kind.textual() || kind.multi()) {
		return nil, errf(where, "%s has no meaning with null", op)
	}

	switch {
	case kind.unary():
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, errf(where, "%s expects true or false", op)
		}
		if (kind == opIsNull) == b {
			return crud.IsNull(field), nil
		}
		return crud.IsNotNull(field), nil

	case kind.textual():
		if crud.ElemType(f.Type).Kind() != reflect.String {
			return nil, errf(where, "%s requires a text field", op)
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, errf(where, "%s expects a string", op)
		}
		if err := this.countBinds(1, where); err != nil {
			return nil, err
		}
		return buildText(field, kind, s), nil

	case kind.multi():
		vals, err := decodeList(raw, f)
		if err != nil {
			return nil, errf(where, "%s", err)
		}
		if err := this.countValues(len(vals), where); err != nil {
			return nil, err
		}
		return buildMulti(field, kind, vals, where)

	default:
		if isNull(trim(raw)) {
			if kind != opEq && kind != opNe {
				return nil, errf(where, "%s has no meaning with null", op)
			}
			return buildScalar(field, kind, nil), nil
		}
		v, err := decodeValue(raw, f)
		if err != nil {
			return nil, errf(where, "%s", err)
		}
		if err := this.countBinds(1, where); err != nil {
			return nil, err
		}
		return buildScalar(field, kind, v), nil
	}
}

func buildScalar(field string, kind opKind, v any) crud.Predicate {
	switch kind {
	case opNe:
		return crud.Ne(field, v)
	case opGt:
		return crud.Gt(field, v)
	case opGte:
		return crud.Gte(field, v)
	case opLt:
		return crud.Lt(field, v)
	case opLte:
		return crud.Lte(field, v)
	default:
		return crud.Eq(field, v)
	}
}

func buildText(field string, kind opKind, s string) crud.Predicate {
	switch kind {
	case opNotLike:
		return crud.NotLike(field, s)
	case opILike:
		return crud.LikeIgnoreCase(field, s)
	case opContains:
		return crud.Contains(field, s)
	case opStartsWith:
		return crud.StartsWith(field, s)
	case opEndsWith:
		return crud.EndsWith(field, s)
	case opIContains:
		return crud.ContainsIgnoreCase(field, s)
	case opIStartsWith:
		return crud.StartsWithIgnoreCase(field, s)
	case opIEndsWith:
		return crud.EndsWithIgnoreCase(field, s)
	default:
		return crud.Like(field, s)
	}
}

func buildMulti(field string, kind opKind, vals []any, where string) (crud.Predicate, error) {
	if len(vals) == 0 {
		return nil, errf(where, "%s needs at least one value", kind.String())
	}
	switch kind {
	case opNotIn:
		return crud.NotIn(field, vals...), nil
	case opBetween:
		if len(vals) != 2 {
			return nil, errf(where, "between expects exactly two values, got %d", len(vals))
		}
		return crud.Between(field, vals[0], vals[1]), nil
	default:
		return crud.In(field, vals...), nil
	}
}

func preview(b []byte) string {
	const max = 40
	s := string(b)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
