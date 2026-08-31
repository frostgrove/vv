package query

import "strings"

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
	opIContains
	opIStartsWith
	opIEndsWith
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
	"icontains": opIContains, "containsignorecase": opIContains,
	"istartswith": opIStartsWith, "startswithignorecase": opIStartsWith,
	"iendswith": opIEndsWith, "endswithignorecase": opIEndsWith,

	"in":  opIn,
	"nin": opNotIn, "notin": opNotIn,
	"between": opBetween,

	"isnull": opIsNull, "null": opIsNull,
	"isnotnull": opIsNotNull, "notnull": opIsNotNull,
}

func normalizeOp(s string) (opKind, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "$")
	if k, ok := opNames[s]; ok {
		return k, true
	}
	k, ok := opNames[strings.ToLower(s)]
	return k, ok
}

func (this opKind) textual() bool {
	switch this {
	case opLike, opNotLike, opILike, opContains, opStartsWith, opEndsWith, opIContains, opIStartsWith, opIEndsWith:
		return true
	}
	return false
}

func (this opKind) multi() bool {
	switch this {
	case opIn, opNotIn, opBetween:
		return true
	}
	return false
}

func (this opKind) unary() bool { return this == opIsNull || this == opIsNotNull }

func (this opKind) String() string {
	for name, kind := range opNames {
		if kind == this && len(name) > 2 {
			return name
		}
	}
	return "op"
}
