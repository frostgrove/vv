package query

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/frostgrove/vv/crud"
)

// node compiles one filter object. Keys are either logical combinators
// (and/or/not) or field paths; the rest of the object is ANDed together.
//
// Keys are visited in sorted order so the generated SQL is deterministic —
// Go map iteration is not, and untestable SQL is not worth having.
func (c *compiler) node(raw json.RawMessage, where string, depth int) (crud.Predicate, error) {
	raw = trim(raw)
	if len(raw) == 0 || isNull(raw) {
		return nil, errf(where, "filter must be a non-empty object")
	}
	if depth > c.cfg.maxDepth() {
		return nil, errf(where, "filter is nested deeper than the allowed %d levels", c.cfg.maxDepth())
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, errf(where, "expected an object, got %s", preview(raw))
	}
	// Request.UnmarshalJSON scans the whole document. RawFilter is also public,
	// though, so compile its programmatic route with the same no-duplicates
	// rule rather than granting it a last-wins escape hatch. Do it after the
	// shape check so malformed query-string JSON keeps the useful "expected an
	// object" diagnostic rather than exposing a decoder implementation detail.
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

		// A schema may legitimately expose a field called Or, And or Not. The
		// dollar spelling is always a combinator; the bare spelling is a
		// combinator only when it does not resolve to a model field. That keeps
		// old documents readable on ordinary models and gives the ambiguous
		// models both operations (`$or`) and the field itself (`or`).
		logical := !isFilterField(c, key)
		switch strings.ToLower(key) {
		case "$and":
			p, err := c.list(val, sub, depth+1, "AND")
			if err != nil {
				return nil, err
			}
			preds = appendPred(preds, p)
		case "$or":
			p, err := c.list(val, sub, depth+1, "OR")
			if err != nil {
				return nil, err
			}
			preds = appendPred(preds, p)
		case "$not":
			inner, err := c.node(val, sub, depth+1)
			if err != nil {
				return nil, err
			}
			if inner != nil {
				preds = append(preds, crud.Not(inner))
			}
		case "and":
			if !logical {
				p, err := c.condition(key, val, sub)
				if err != nil {
					return nil, err
				}
				preds = appendPred(preds, p)
				continue
			}
			p, err := c.list(val, sub, depth+1, "AND")
			if err != nil {
				return nil, err
			}
			preds = appendPred(preds, p)
		case "or":
			if !logical {
				p, err := c.condition(key, val, sub)
				if err != nil {
					return nil, err
				}
				preds = appendPred(preds, p)
				continue
			}
			p, err := c.list(val, sub, depth+1, "OR")
			if err != nil {
				return nil, err
			}
			preds = appendPred(preds, p)
		case "not":
			if !logical {
				p, err := c.condition(key, val, sub)
				if err != nil {
					return nil, err
				}
				preds = appendPred(preds, p)
				continue
			}
			inner, err := c.node(val, sub, depth+1)
			if err != nil {
				return nil, err
			}
			if inner != nil {
				preds = append(preds, crud.Not(inner))
			}
		default:
			p, err := c.condition(key, val, sub)
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

func appendPred(dst []crud.Predicate, p crud.Predicate) []crud.Predicate {
	if p == nil {
		return dst
	}
	return append(dst, p)
}

// list compiles the array form of and/or.
func (c *compiler) list(raw json.RawMessage, where string, depth int, op string) (crud.Predicate, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		// A bare object is accepted too: {"or": {...}} is just that object.
		p, oerr := c.node(raw, where, depth)
		if oerr != nil {
			return nil, errf(where, "expected an array of filter objects, got %s", preview(raw))
		}
		return p, nil
	}
	var preds []crud.Predicate
	for i, item := range items {
		p, err := c.node(item, where+"["+itoa(i)+"]", depth)
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

// condition compiles one field entry: either a shorthand value or an operator
// object.
func (c *compiler) condition(path string, raw json.RawMessage, where string) (crud.Predicate, error) {
	f, canonical, err := c.path(path, where)
	if err != nil {
		return nil, err
	}
	if !allowed(c.cfg.filterable(), c.qualify(canonical)) {
		return nil, errf(where, "%s is not filterable", c.qualify(canonical))
	}

	raw = trim(raw)
	if len(raw) == 0 {
		return nil, errf(where, "filter value is missing")
	}

	switch raw[0] {
	case '{':
		return c.operators(canonical, f, raw, where)
	case '[':
		// {"status": ["draft","live"]} means IN.
		if err := c.count(where); err != nil {
			return nil, err
		}
		vals, err := decodeList(raw, f)
		if err != nil {
			return nil, errf(where, "%s", err)
		}
		if err := c.countValues(len(vals), where); err != nil {
			return nil, err
		}
		return buildMulti(canonical, opIn, vals, where)
	default:
		if err := c.count(where); err != nil {
			return nil, err
		}
		if isNull(raw) {
			return crud.IsNull(canonical), nil
		}
		v, err := decodeValue(raw, f)
		if err != nil {
			return nil, errf(where, "%s", err)
		}
		if err := c.countBinds(1, where); err != nil {
			return nil, err
		}
		return crud.Eq(canonical, v), nil
	}
}

// operators compiles {"gte": 1, "lt": 10} into one ANDed condition.
func (c *compiler) operators(field string, f *crud.Field, raw json.RawMessage, where string) (crud.Predicate, error) {
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
		if err := c.count(where); err != nil {
			return nil, err
		}
		p, err := c.operator(field, f, key, obj[key], where+"."+key)
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

func (c *compiler) operator(field string, f *crud.Field, op string, raw json.RawMessage, where string) (crud.Predicate, error) {
	kind, ok := normalizeOp(op)
	if !ok {
		return nil, errf(where, "unknown operator %q", op)
	}
	// json.Unmarshal reads null as "leave the destination alone", so a null
	// operand used to arrive as the zero value of whatever the operator wanted:
	// {"contains": null} became LIKE '%%' and {"notIn": null} became NOT IN () —
	// a narrowing the client asked for turning into no narrowing at all. Only a
	// scalar comparison has an answer for null, and crud.Eq folds that to IS NULL.
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
		if err := c.countBinds(1, where); err != nil {
			return nil, err
		}
		return buildText(field, kind, s), nil

	case kind.multi():
		vals, err := decodeList(raw, f)
		if err != nil {
			return nil, errf(where, "%s", err)
		}
		if err := c.countValues(len(vals), where); err != nil {
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
		if err := c.countBinds(1, where); err != nil {
			return nil, err
		}
		return buildScalar(field, kind, v), nil
	}
}

// buildScalar, buildText and buildMulti are the single place an operator turns
// into a predicate; both the JSON and the query-string front doors go through
// them.
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
