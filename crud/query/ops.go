package query

import "strings"

// opKind is the canonical operator. Every spelling a client may send — JSON
// key, `$`-prefixed Mongo style, or a query-string triple — folds into one of
// these, so the two front doors can never drift apart.
type opKind int

const (
	opEq opKind = iota
	opNe
	opGt
	opGte
	opLt
	opLte
	opLike
	opNotLike
	opILike
	opContains
	opStartsWith
	opEndsWith
	opIn
	opNotIn
	opBetween
	opIsNull
	opIsNotNull
)

var opNames = map[string]opKind{
	"eq": opEq, "=": opEq, "equals": opEq, "is": opEq,
	"ne": opNe, "neq": opNe, "not": opNe, "!=": opNe, "<>": opNe,
	"gt": opGt, ">": opGt,
	"gte": opGte, ">=": opGte, "ge": opGte,
	"lt": opLt, "<": opLt,
	"lte": opLte, "<=": opLte, "le": opLte,

	"like":    opLike,
	"notlike": opNotLike, "notLike": opNotLike,
	"ilike": opILike, "likeignorecase": opILike,
	"contains": opContains, "search": opContains,
	"startswith": opStartsWith, "prefix": opStartsWith,
	"endswith": opEndsWith, "suffix": opEndsWith,

	"in":  opIn,
	"nin": opNotIn, "notin": opNotIn,
	"between": opBetween,

	"isnull": opIsNull, "null": opIsNull,
	"isnotnull": opIsNotNull, "notnull": opIsNotNull,
}

// normalizeOp folds a spelling into its canonical operator.
func normalizeOp(s string) (opKind, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "$")
	if k, ok := opNames[s]; ok {
		return k, true
	}
	k, ok := opNames[strings.ToLower(s)]
	return k, ok
}

// textual reports whether the operator compares against a LIKE pattern rather
// than a typed value, so the raw string is used instead of a coerced one.
func (k opKind) textual() bool {
	switch k {
	case opLike, opNotLike, opILike, opContains, opStartsWith, opEndsWith:
		return true
	}
	return false
}

// multi reports whether the operator takes a value list.
func (k opKind) multi() bool {
	switch k {
	case opIn, opNotIn, opBetween:
		return true
	}
	return false
}

// unary reports whether the operator takes no value at all.
func (k opKind) unary() bool { return k == opIsNull || k == opIsNotNull }

func (k opKind) String() string {
	for name, kind := range opNames {
		if kind == k && len(name) > 2 {
			return name
		}
	}
	return "op"
}
