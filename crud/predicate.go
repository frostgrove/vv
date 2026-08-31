package crud

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"strings"
	"time"

	"github.com/frostgrove/vv/utils"
)

type Predicate interface {
	render(w *writer)

	document(w *docWriter)
}

type scope struct {
	meta  *Meta
	alias string
}

func (this scope) qualify(d Dialect, col string) string {
	if this.alias == "" {
		return d.Quote(col)
	}
	return this.alias + "." + d.Quote(col)
}

func (this scope) correlate(d Dialect, col string) string {
	if this.alias == "" {
		return quoteTable(d, this.meta.TableReference()) + "." + d.Quote(col)
	}
	return this.alias + "." + d.Quote(col)
}

type writer struct {
	sb    strings.Builder
	args  []any
	d     Dialect
	m     *Meta
	cur   scope
	alias int
	err   error

	rel  *RelationScopes
	path string
}

func (this *writer) fail(err error) {
	if this.err == nil {
		this.err = err
	}
}

func (this *writer) nextAlias() string {
	this.alias++
	return "rx" + itoa(this.alias)
}

func (this *writer) current() scope {
	if this.cur.meta == nil {
		return scope{meta: this.m}
	}
	return this.cur
}

func (this *writer) leaf(path string, emit func(col string)) {
	cur := this.current()
	hops, f, _, err := cur.meta.WalkPath(path)
	if err != nil {
		this.fail(err)
		this.str("1 = 0")
		return
	}
	if f == nil {
		this.fail(&SchemaError{Model: cur.meta.Name, Field: path, Reason: "path names a relation, not a column"})
		this.str("1 = 0")
		return
	}
	if len(hops) == 0 {
		emit(cur.qualify(this.d, f.Column))
		return
	}

	saved, savedPath := this.cur, this.path
	defer func() { this.cur, this.path = saved, savedPath }()

	for _, hop := range hops {
		alias := this.nextAlias()
		this.str("EXISTS (SELECT 1 FROM ")
		this.str(quoteTable(this.d, hop.Target.TableReference()))
		this.str(" AS " + alias)
		if hop.Rel.Kind == ManyToMany {
			j := this.nextAlias()
			this.str(" JOIN " + quoteTable(this.d, hop.Rel.JoinTableReference()) + " AS " + j +
				" ON " + j + "." + this.d.Quote(hop.Rel.JoinRef) + " = " + alias + "." + this.d.Quote(hop.Remote.Column))
			this.str(" WHERE " + j + "." + this.d.Quote(hop.Rel.JoinLocal) + " = " + cur.correlate(this.d, hop.Local.Column))
		} else {
			this.str(" WHERE " + alias + "." + this.d.Quote(hop.Remote.Column) + " = " + cur.correlate(this.d, hop.Local.Column))
		}
		this.str(" AND ")
		cur = scope{meta: hop.Target, alias: alias}
		this.cur, this.path = cur, joinPath(this.path, hop.Rel.Name)
		if this.hopScope() {
			this.str(" AND ")
		}
	}
	emit(cur.qualify(this.d, f.Column))
	this.str(strings.Repeat(")", len(hops)))
}

func (this *writer) hopScope() bool {
	p := this.rel.At(this.path, this.cur.meta)
	if p == nil {
		return false
	}

	saved := this.rel
	this.rel = nil
	p.render(this)
	this.rel = saved
	return true
}

func (this *writer) column(ref string) {
	this.leaf(ref, func(col string) { this.sb.WriteString(col) })
}

func (this *writer) bind(v any) {
	this.args = append(this.args, v)
	this.sb.WriteString(this.d.Placeholder(len(this.args)))
}

func (this *writer) str(s string) { this.sb.WriteString(s) }

func (this *writer) likeEscape() {
	if d, ok := this.d.(LikeEscaper); ok {
		this.str(d.LikeEscapeClause())
		return
	}
	this.str(` ESCAPE '\'`)
}

type cmpNode struct {
	field     string
	op        string
	value     any
	undefined bool
}

func (this cmpNode) render(w *writer) {
	if this.undefined {
		w.fail(&SchemaError{Model: w.current().meta.Name, Field: this.field, Reason: "an undefined Opt is not a comparison value"})
		w.str("1 = 0")
		return
	}
	w.leaf(this.field, func(col string) {
		w.str(col)
		w.str(" " + this.op + " ")
		w.bind(this.value)
	})
}

type nullNode struct {
	field string
	not   bool
}

func (this nullNode) render(w *writer) {
	w.leaf(this.field, func(col string) {
		w.str(col)
		if this.not {
			w.str(" IS NOT NULL")
		} else {
			w.str(" IS NULL")
		}
	})
}

type inNode struct {
	field  string
	values []any
	not    bool
}

func (this inNode) render(w *writer) {
	if len(this.values) == 0 {
		if this.not {
			w.str("1 = 1")
		} else {
			w.str("1 = 0")
		}
		return
	}
	w.leaf(this.field, func(col string) {
		w.str(col)
		if this.not {
			w.str(" NOT IN (")
		} else {
			w.str(" IN (")
		}
		for i, v := range this.values {
			if i > 0 {
				w.str(", ")
			}
			w.bind(v)
		}
		w.str(")")
	})
}

type betweenNode struct {
	field   string
	low, hi any
	not     bool
}

func (this betweenNode) render(w *writer) {
	w.leaf(this.field, func(col string) {
		w.str(col)
		if this.not {
			w.str(" NOT BETWEEN ")
		} else {
			w.str(" BETWEEN ")
		}
		w.bind(this.low)
		w.str(" AND ")
		w.bind(this.hi)
	})
}

type likeNode struct {
	field      string
	pattern    string
	not        bool
	ignoreCase bool
	mode       likeMode
}

type likeMode uint8

const (
	likePattern likeMode = iota
	likeContains
	likeStartsWith
	likeEndsWith
)

func (this likeNode) render(w *writer) {
	pattern := this.pattern
	switch this.mode {
	case likeContains:
		pattern = "%" + escapeLike(pattern) + "%"
	case likeStartsWith:
		pattern = escapeLike(pattern) + "%"
	case likeEndsWith:
		pattern = "%" + escapeLike(pattern)
	}
	w.leaf(this.field, func(col string) {
		if this.ignoreCase {
			w.str("LOWER(" + col + ")")
		} else {
			w.str(col)
		}
		if this.not {
			w.str(" NOT LIKE ")
		} else {
			w.str(" LIKE ")
		}
		if this.ignoreCase {
			w.str("LOWER(")
			w.bind(pattern)
			w.str(")")
		} else {
			w.bind(pattern)
		}
		if this.mode != likePattern {
			w.likeEscape()
		}
	})
}

type fieldCmpNode struct {
	left, right string
	op          string
}

func (this fieldCmpNode) render(w *writer) {
	w.leaf(this.left, func(left string) {
		w.leaf(this.right, func(right string) {
			w.str(left + " " + this.op + " " + right)
		})
	})
}

type logicNode struct {
	op   string
	kids []Predicate
}

func flatten(op string, kids, out []Predicate) []Predicate {
	for _, k := range kids {
		if k == nil {
			continue
		}
		if ln, ok := k.(logicNode); ok && ln.op == op {
			out = flatten(op, ln.kids, out)
			continue
		}
		out = append(out, k)
	}
	return out
}

func (this logicNode) render(w *writer) {
	live := flatten(this.op, this.kids, nil)
	switch len(live) {
	case 0:
		if this.op == "AND" {
			w.str("1 = 1")
		} else {
			w.str("1 = 0")
		}
		return
	case 1:
		live[0].render(w)
		return
	}
	w.str("(")
	for i, k := range live {
		if i > 0 {
			w.str(" " + this.op + " ")
		}
		k.render(w)
	}
	w.str(")")
}

type notNode struct{ inner Predicate }

func (this notNode) render(w *writer) {
	if this.inner == nil {
		w.str("1 = 0")
		return
	}
	w.str("NOT (")
	this.inner.render(w)
	w.str(")")
}

type constNode bool

func (this constNode) render(w *writer) {
	if this {
		w.str("1 = 1")
	} else {
		w.str("1 = 0")
	}
}

type rawNode struct {
	sql  string
	args []any
}

func (this rawNode) render(w *writer) {
	i := 0
	for {
		j := strings.IndexByte(this.sql[i:], '?')
		if j < 0 {
			w.str(this.sql[i:])
			if len(this.args) > 0 {
				w.fail(&SchemaError{Model: w.m.Name, Reason: "crud.Raw: fewer ? markers than arguments"})
			}
			return
		}
		j += i
		w.str(this.sql[i:j])
		if j+1 < len(this.sql) && this.sql[j+1] == '?' {
			w.str("?")
			i = j + 2
			continue
		}
		if len(this.args) == 0 {
			w.fail(&SchemaError{Model: w.m.Name, Reason: "crud.Raw: more ? markers than arguments"})
			return
		}
		w.bind(this.args[0])
		this.args = this.args[1:]
		i = j + 1
	}
}

func Eq(field string, value any) Predicate {
	if stored, defined, null, ok := utils.Inspect(value); ok {
		if !defined {
			return cmpNode{field: field, op: "=", undefined: true}
		}
		if null {
			return nullNode{field: field}
		}
		value = stored
	}
	if isNil(value) {
		return nullNode{field: field}
	}
	return cmpNode{field: field, op: "=", value: value}
}

func Ne(field string, value any) Predicate {
	if stored, defined, null, ok := utils.Inspect(value); ok {
		if !defined {
			return cmpNode{field: field, op: "<>", undefined: true}
		}
		if null {
			return nullNode{field: field, not: true}
		}
		value = stored
	}
	if isNil(value) {
		return nullNode{field: field, not: true}
	}
	return cmpNode{field: field, op: "<>", value: value}
}

func Gt(field string, value any) Predicate  { return cmpNode{field: field, op: ">", value: value} }
func Gte(field string, value any) Predicate { return cmpNode{field: field, op: ">=", value: value} }
func Lt(field string, value any) Predicate  { return cmpNode{field: field, op: "<", value: value} }
func Lte(field string, value any) Predicate { return cmpNode{field: field, op: "<=", value: value} }

func IsNull(field string) Predicate    { return nullNode{field: field} }
func IsNotNull(field string) Predicate { return nullNode{field: field, not: true} }

func Like(field, pattern string) Predicate { return likeNode{field: field, pattern: pattern} }
func NotLike(field, pattern string) Predicate {
	return likeNode{field: field, pattern: pattern, not: true}
}

func LikeIgnoreCase(field, pattern string) Predicate {
	return likeNode{field: field, pattern: pattern, ignoreCase: true}
}

func Contains(field, s string) Predicate {
	return likeNode{field: field, pattern: s, mode: likeContains}
}

func ContainsIgnoreCase(field, s string) Predicate {
	return likeNode{field: field, pattern: s, ignoreCase: true, mode: likeContains}
}
func StartsWith(field, s string) Predicate {
	return likeNode{field: field, pattern: s, mode: likeStartsWith}
}
func EndsWith(field, s string) Predicate {
	return likeNode{field: field, pattern: s, mode: likeEndsWith}
}

func StartsWithIgnoreCase(field, s string) Predicate {
	return likeNode{field: field, pattern: s, ignoreCase: true, mode: likeStartsWith}
}

func EndsWithIgnoreCase(field, s string) Predicate {
	return likeNode{field: field, pattern: s, ignoreCase: true, mode: likeEndsWith}
}

func Between(field string, low, high any) Predicate {
	return betweenNode{field: field, low: low, hi: high}
}

func In(field string, values ...any) Predicate {
	return inNode{field: field, values: append([]any(nil), values...)}
}

func NotIn(field string, values ...any) Predicate {
	return inNode{field: field, values: append([]any(nil), values...), not: true}
}

func InAny[T any](field string, values []T) Predicate {
	return inNode{field: field, values: anySlice(values)}
}

func NotInAny[T any](field string, values []T) Predicate {
	return inNode{field: field, values: anySlice(values), not: true}
}

func EqField(left, right string) Predicate { return fieldCmpNode{left, right, "="} }

func And(preds ...Predicate) Predicate { return logicNode{"AND", append([]Predicate(nil), preds...)} }

func Or(preds ...Predicate) Predicate { return logicNode{"OR", append([]Predicate(nil), preds...)} }

func Not(p Predicate) Predicate { return notNode{p} }

func True() Predicate  { return constNode(true) }
func False() Predicate { return constNode(false) }

func IsTautology(p Predicate) bool {
	if !tautologyWithinBudget(p) {
		return false
	}
	v, known := constantValueFor(nil, p)
	return known && v
}

func IsTautologyFor(m *Meta, p Predicate) bool {
	if !tautologyWithinBudget(p) {
		return false
	}
	v, known := constantValueFor(m, p)
	return known && v
}

func MayBeTautologyFor(m *Meta, p Predicate) bool {
	if !tautologyWithinBudget(p) {
		return true
	}
	if containsRawForTautology(p) || containsOpaqueBindForTautology(p) {
		return true
	}
	if IsTautologyFor(m, p) {
		return true
	}

	if containsLogicForTautology(p) {
		p = simplifyForTautology(m, p)
		_, exhausted := bddTautologyFor(m, p)
		return exhausted
	}
	return false
}

func containsRawForTautology(p Predicate) bool {
	switch n := p.(type) {
	case rawNode:
		return true
	case notNode:
		return containsRawForTautology(n.inner)
	case logicNode:
		for _, kid := range n.kids {
			if containsRawForTautology(kid) {
				return true
			}
		}
	}
	return false
}

func containsLogicForTautology(p Predicate) bool {
	switch n := p.(type) {
	case notNode:
		return containsLogicForTautology(n.inner)
	case logicNode:
		return true
	}
	return false
}

func containsOpaqueBindForTautology(p Predicate) bool {
	switch n := p.(type) {
	case cmpNode:
		return opaqueBindForTautology(n.value)
	case inNode:
		for _, value := range n.values {
			if opaqueBindForTautology(value) {
				return true
			}
		}
	case betweenNode:
		return opaqueBindForTautology(n.low) || opaqueBindForTautology(n.hi)
	case notNode:
		return containsOpaqueBindForTautology(n.inner)
	case logicNode:
		for _, kid := range n.kids {
			if containsOpaqueBindForTautology(kid) {
				return true
			}
		}
	}
	return false
}

func opaqueBindForTautology(value any) bool {
	if _, ok := canonicalBindForTautology(value); ok {
		return false
	}
	return !knownNullBindForTautology(value)
}

func knownNullBindForTautology(value any) bool {
	seenPointers := map[reflect.Value]struct{}{}
	topLevel := true
	for {
		if value == nil {
			return true
		}
		if named, ok := value.(sql.NamedArg); ok {
			if !topLevel {
				return false
			}
			topLevel = false
			value = named.Value
			continue
		}
		if _, out := value.(sql.Out); out {
			return false
		}
		if _, valuer := value.(driver.Valuer); valuer {
			return false
		}
		rv := reflect.ValueOf(value)
		if rv.Kind() != reflect.Pointer && rv.Kind() != reflect.Interface {
			return false
		}
		if rv.IsNil() {
			return true
		}
		if rv.Kind() == reflect.Pointer {
			if _, seen := seenPointers[rv]; seen {
				return false
			}
			seenPointers[rv] = struct{}{}
		}
		topLevel = false
		value = rv.Elem().Interface()
	}
}

const (
	maxTautologyASTNodes = 512
	maxTautologyASTDepth = 64
)

type tautologyWalkNode struct {
	p          Predicate
	depth      int
	underLogic bool
}

func tautologyWithinBudget(p Predicate) bool {
	stack := []tautologyWalkNode{{p: p}}
	seen := 0
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		seen++
		if seen > maxTautologyASTNodes || current.depth > maxTautologyASTDepth {
			return false
		}
		switch n := current.p.(type) {
		case notNode:
			stack = append(stack, tautologyWalkNode{p: n.inner, depth: current.depth + 1, underLogic: current.underLogic})
		case logicNode:
			if len(n.kids) > maxTautologyASTNodes-seen-len(stack) {
				return false
			}
			for _, kid := range n.kids {
				stack = append(stack, tautologyWalkNode{p: kid, depth: current.depth + 1, underLogic: true})
			}
		case inNode:
			if current.underLogic {
				seen += len(n.values)
				if seen > maxTautologyASTNodes {
					return false
				}
			}
		}
	}
	return true
}

func constantValueFor(m *Meta, p Predicate) (bool, bool) {
	p = simplifyForTautology(m, p)
	return constantValueForSimplified(m, p)
}

func constantValueForSimplified(m *Meta, p Predicate) (bool, bool) {
	switch n := p.(type) {
	case nil:
		return true, true
	case constNode:
		return bool(n), true
	case inNode:
		if len(n.values) == 0 {
			return n.not, true
		}
	case nullNode:
		if primaryKeyField(m, n.field) {
			return n.not, true
		}
	case fieldCmpNode:
		if n.op == "=" && samePrimaryKey(m, n.left, n.right) {
			return true, true
		}
	case logicNode:
		live := flatten(n.op, n.kids, nil)
		if len(live) == 0 {
			return n.op == "AND", true
		}
		if booleanTautologyFor(m, logicNode{op: n.op, kids: live}) {
			return true, true
		}
		if n.op == "AND" {
			allTrue := true
			for _, kid := range live {
				v, known := constantValueForSimplified(m, kid)
				if known && !v {
					return false, true
				}
				allTrue = allTrue && known && v
			}
			return allTrue, allTrue
		}
		allFalse := true
		for _, kid := range live {
			v, known := constantValueForSimplified(m, kid)
			if known && v {
				return true, true
			}
			allFalse = allFalse && known && !v
		}
		return false, allFalse
	case notNode:
		if v, known := constantValueForSimplified(m, n.inner); known {
			return !v, true
		}
	}
	return false, false
}

func simplifyForTautology(m *Meta, p Predicate) Predicate {
	switch n := p.(type) {
	case notNode:
		inner := simplifyForTautology(m, n.inner)
		if v, known := constantValueForSimplified(m, inner); known {
			return constNode(!v)
		}
		if nested, ok := inner.(notNode); ok {
			return nested.inner
		}
		return notNode{inner: inner}
	case logicNode:
		live := flatten(n.op, n.kids, nil)
		kept := make([]Predicate, 0, len(live))
		for _, kid := range live {
			kid = simplifyForTautology(m, kid)
			if v, known := constantValueForSimplified(m, kid); known {
				if n.op == "AND" {
					if !v {
						return constNode(false)
					}
					continue
				}
				if v {
					return constNode(true)
				}
				continue
			}
			kept = append(kept, kid)
		}
		if len(kept) == 0 {
			return constNode(n.op == "AND")
		}
		if len(kept) == 1 {
			return kept[0]
		}
		return logicNode{op: n.op, kids: kept}
	default:
		return p
	}
}

func primaryKeyField(m *Meta, name string) bool {
	if m == nil || m.PK == nil {
		return false
	}
	f := m.Field(name)
	return f != nil && f == m.PK
}

func samePrimaryKey(m *Meta, a, b string) bool {
	if !primaryKeyField(m, a) {
		return false
	}
	return primaryKeyField(m, b)
}

func sameValueSet(left, right []any) bool {
	for _, l := range left {
		if !containsValue(right, l) {
			return false
		}
	}
	for _, r := range right {
		if !containsValue(left, r) {
			return false
		}
	}
	return true
}

func containsValue(values []any, want any) bool {
	for _, value := range values {
		if sameBindValue(value, want) {
			return true
		}
	}
	return false
}

func allNonNil(values []any) bool {
	for _, value := range values {
		if !definitelyNonNullBind(value) {
			return false
		}
	}
	return true
}

func definitelyNonNullBind(value any) bool {
	_, ok := canonicalBindForTautology(value)
	return ok
}

func sameBindValue(left, right any) bool {
	left, leftOK := canonicalBindForTautology(left)
	right, rightOK := canonicalBindForTautology(right)
	return leftOK && rightOK && reflect.DeepEqual(left, right)
}

func canonicalBindForTautology(value any) (any, bool) {
	seenPointers := map[reflect.Value]struct{}{}
	namedUnwrapped := false
	topLevel := true
	for {
		if isNil(value) {
			return nil, false
		}
		if named, ok := value.(sql.NamedArg); ok {
			if namedUnwrapped || !topLevel {
				return nil, false
			}
			namedUnwrapped = true
			topLevel = false
			value = named.Value
			continue
		}
		if _, out := value.(sql.Out); out {
			return nil, false
		}
		if _, mayBecomeNull := value.(driver.Valuer); mayBecomeNull {
			return nil, false
		}
		rv := reflect.ValueOf(value)
		if rv.Kind() != reflect.Pointer && rv.Kind() != reflect.Interface {
			return primitiveDriverValue(value, rv)
		}
		if rv.IsNil() {
			return nil, false
		}
		if rv.Kind() == reflect.Pointer {
			if _, seen := seenPointers[rv]; seen {
				return nil, false
			}
			seenPointers[rv] = struct{}{}
		}
		topLevel = false
		value = rv.Elem().Interface()
	}
}

func primitiveDriverValue(value any, rv reflect.Value) (any, bool) {
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u := rv.Uint()
		if u > uint64(^uint64(0)>>1) {
			return nil, false
		}
		return int64(u), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	case reflect.Bool:
		return rv.Bool(), true
	case reflect.String:
		return rv.String(), true
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return rv.Bytes(), true
		}
	case reflect.Struct:
		if stamped, ok := value.(time.Time); ok {
			return stamped, true
		}
	}
	return nil, false
}

func booleanTautologyFor(m *Meta, p Predicate) bool {
	tautology, _ := bddTautologyFor(m, p)
	return tautology
}

func bddTautologyFor(m *Meta, p Predicate) (tautology, exhausted bool) {
	if !twoValuedFor(m, p) {
		return false, false
	}
	b := tautologyBDD{nodes: []bddNode{{}, {}}}
	root := b.build(m, p)
	return !b.exhausted && root == bddTrue, b.exhausted
}

const (
	bddFalse = 0
	bddTrue  = 1
)

type bddNode struct {
	variable  int
	whenFalse int
	whenTrue  int
}

type bddKey struct {
	variable  int
	whenFalse int
	whenTrue  int
}

type bddApplyKey struct {
	op    string
	left  int
	right int
}

type tautologyBDD struct {
	leaves []Predicate
	nodes  []bddNode
	unique map[bddKey]int
	apply  map[bddApplyKey]int
	not    map[int]int

	exhausted bool
}

const maxTautologyBDDNodes = 512

func (this *tautologyBDD) build(m *Meta, p Predicate) int {
	if this.exhausted {
		return bddFalse
	}
	switch n := p.(type) {
	case constNode:
		if n {
			return bddTrue
		}
		return bddFalse
	case notNode:
		return this.negate(this.build(m, n.inner))
	case logicNode:
		result := bddTrue
		if n.op == "OR" {
			result = bddFalse
		}
		for _, kid := range flatten(n.op, n.kids, nil) {
			result = this.combine(n.op, result, this.build(m, kid))
		}
		return result
	case cmpNode:
		return this.comparison(m, n)
	case nullNode:
		base := this.variable(m, nullNode{field: n.field})
		if n.not {
			return this.negate(base)
		}
		return base
	case inNode:
		result := bddFalse
		for _, value := range n.values {
			result = this.combine("OR", result, this.comparison(m, cmpNode{field: n.field, op: "=", value: value}))
		}
		if n.not {
			return this.negate(result)
		}
		return result
	case betweenNode:
		result := this.combine("AND",
			this.comparison(m, cmpNode{field: n.field, op: ">=", value: n.low}),
			this.comparison(m, cmpNode{field: n.field, op: "<=", value: n.hi}),
		)
		if n.not {
			return this.negate(result)
		}
		return result
	case likeNode:
		base := this.variable(m, likeNode{field: n.field, pattern: n.pattern, ignoreCase: n.ignoreCase, mode: n.mode})
		if n.not {
			return this.negate(base)
		}
		return base
	default:
		return this.variable(m, p)
	}
}

func (this *tautologyBDD) comparison(m *Meta, n cmpNode) int {
	base := n
	switch n.op {
	case "<>":
		base.op = "="
		return this.negate(this.variable(m, base))
	case ">":
		base.op = "<="
		return this.negate(this.variable(m, base))
	case ">=":
		base.op = "<"
		return this.negate(this.variable(m, base))
	default:
		return this.variable(m, base)
	}
}

func (this *tautologyBDD) variable(m *Meta, p Predicate) int {
	for i, leaf := range this.leaves {
		if samePredicate(m, leaf, p) {
			return this.make(i, bddFalse, bddTrue)
		}
	}
	this.leaves = append(this.leaves, p)
	return this.make(len(this.leaves)-1, bddFalse, bddTrue)
}

func (this *tautologyBDD) make(variable, whenFalse, whenTrue int) int {
	if this.exhausted {
		return bddFalse
	}
	if whenFalse == whenTrue {
		return whenFalse
	}
	if this.unique == nil {
		this.unique = make(map[bddKey]int)
	}
	key := bddKey{variable, whenFalse, whenTrue}
	if prior, ok := this.unique[key]; ok {
		return prior
	}
	if len(this.nodes) >= maxTautologyBDDNodes {
		this.exhausted = true
		return bddFalse
	}
	this.nodes = append(this.nodes, bddNode{variable, whenFalse, whenTrue})
	id := len(this.nodes) - 1
	this.unique[key] = id
	return id
}

func (this *tautologyBDD) negate(node int) int {
	if this.exhausted {
		return bddFalse
	}
	if node == bddFalse {
		return bddTrue
	}
	if node == bddTrue {
		return bddFalse
	}
	if this.not == nil {
		this.not = make(map[int]int)
	}
	if prior, ok := this.not[node]; ok {
		return prior
	}
	n := this.nodes[node]
	result := this.make(n.variable, this.negate(n.whenFalse), this.negate(n.whenTrue))
	this.not[node] = result
	return result
}

func (this *tautologyBDD) combine(op string, left, right int) int {
	if this.exhausted {
		return bddFalse
	}
	if this.apply == nil {
		this.apply = make(map[bddApplyKey]int)
	}
	key := bddApplyKey{op, left, right}
	if prior, ok := this.apply[key]; ok {
		return prior
	}
	if left < 2 && right < 2 {
		value := left == bddTrue && right == bddTrue
		if op == "OR" {
			value = left == bddTrue || right == bddTrue
		}
		if value {
			return bddTrue
		}
		return bddFalse
	}
	variable := this.topVariable(left, right)
	lf, lt := this.branch(left, variable)
	rf, rt := this.branch(right, variable)
	result := this.make(variable, this.combine(op, lf, rf), this.combine(op, lt, rt))
	this.apply[key] = result
	return result
}

func (this *tautologyBDD) topVariable(left, right int) int {
	leftVariable, rightVariable := this.variableOf(left), this.variableOf(right)
	if leftVariable < rightVariable {
		return leftVariable
	}
	return rightVariable
}

func (this *tautologyBDD) variableOf(node int) int {
	if node < 2 {
		return len(this.leaves)
	}
	return this.nodes[node].variable
}

func (this *tautologyBDD) branch(node, variable int) (whenFalse, whenTrue int) {
	if node < 2 || this.nodes[node].variable != variable {
		return node, node
	}
	n := this.nodes[node]
	return n.whenFalse, n.whenTrue
}

func twoValuedFor(m *Meta, p Predicate) bool {
	switch n := p.(type) {
	case constNode, nullNode:
		return true
	case cmpNode:
		return primaryKeyField(m, n.field) && definitelyNonNullBind(n.value)
	case inNode:
		return primaryKeyField(m, n.field) && len(n.values) > 0 && allNonNil(n.values)
	case betweenNode:
		return primaryKeyField(m, n.field) && definitelyNonNullBind(n.low) && definitelyNonNullBind(n.hi)
	case likeNode:
		return primaryKeyField(m, n.field)
	case fieldCmpNode:
		return primaryKeyField(m, n.left) && primaryKeyField(m, n.right)
	case notNode:
		return n.inner != nil && twoValuedFor(m, n.inner)
	case logicNode:
		live := flatten(n.op, n.kids, nil)
		for _, kid := range live {
			if !twoValuedFor(m, kid) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func samePredicate(m *Meta, left, right Predicate) bool {
	switch l := left.(type) {
	case constNode:
		r, ok := right.(constNode)
		return ok && l == r
	case cmpNode:
		r, ok := right.(cmpNode)
		return ok && l.op == r.op && sameField(m, l.field, r.field) && sameBindValue(l.value, r.value)
	case nullNode:
		r, ok := right.(nullNode)
		return ok && l.not == r.not && sameField(m, l.field, r.field)
	case inNode:
		r, ok := right.(inNode)
		return ok && l.not == r.not && sameField(m, l.field, r.field) && sameValueSet(l.values, r.values)
	case betweenNode:
		r, ok := right.(betweenNode)
		return ok && l.not == r.not && sameField(m, l.field, r.field) &&
			sameBindValue(l.low, r.low) && sameBindValue(l.hi, r.hi)
	case likeNode:
		r, ok := right.(likeNode)
		return ok && l.not == r.not && l.ignoreCase == r.ignoreCase && l.mode == r.mode && l.pattern == r.pattern && sameField(m, l.field, r.field)
	case fieldCmpNode:
		r, ok := right.(fieldCmpNode)
		return ok && l.op == r.op && sameField(m, l.left, r.left) && sameField(m, l.right, r.right)
	case notNode:
		r, ok := right.(notNode)
		return ok && samePredicate(m, l.inner, r.inner)
	case logicNode:
		r, ok := right.(logicNode)
		if !ok || l.op != r.op {
			return false
		}
		leftKids, rightKids := flatten(l.op, l.kids, nil), flatten(r.op, r.kids, nil)
		if len(leftKids) != len(rightKids) {
			return false
		}
		for i := range leftKids {
			if !samePredicate(m, leftKids[i], rightKids[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func sameField(m *Meta, a, b string) bool {
	if m == nil {
		return false
	}
	fa, fb := m.Field(a), m.Field(b)
	return fa != nil && fa == fb
}

func Raw(sql string, args ...any) Predicate { return rawNode{sql, args} }

func anySlice[T any](vs []T) []any {
	out := make([]any, len(vs))
	for i, v := range vs {
		out[i] = v
	}
	return out
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		return rv.IsNil()
	}
	return false
}

type Order struct {
	Field     string
	Desc      bool
	NullsLast bool
	NullsSet  bool
}

func Asc(field string) Order { return Order{Field: field} }

func Desc(field string) Order { return Order{Field: field, Desc: true} }

func (this Order) WithNullsLast() Order { this.NullsLast, this.NullsSet = true, true; return this }

func (this Order) WithNullsFirst() Order { this.NullsLast, this.NullsSet = false, true; return this }

func (this *writer) sortExpr(segs []string, cur scope) {
	if len(segs) == 1 {
		f := cur.meta.Field(segs[0])
		if f == nil {
			this.fail(&UnknownFieldError{Model: cur.meta.Name, Field: segs[0]})
			this.str("NULL")
			return
		}
		this.str(cur.qualify(this.d, f.Column))
		return
	}
	rel := cur.meta.Relation(segs[0])
	if rel == nil {
		this.fail(&UnknownFieldError{Model: cur.meta.Name, Field: segs[0]})
		this.str("NULL")
		return
	}
	if rel.Kind.ToMany() {
		this.fail(&SchemaError{Model: cur.meta.Name, Field: rel.Name,
			Reason: "cannot sort through a " + rel.Kind.String() + " relation"})
		this.str("NULL")
		return
	}
	target, local, remote, err := rel.Resolve()
	if err != nil {
		this.fail(err)
		this.str("NULL")
		return
	}
	alias := this.nextAlias()
	saved, savedPath := this.cur, this.path
	defer func() { this.cur, this.path = saved, savedPath }()

	this.cur, this.path = scope{meta: target, alias: alias}, joinPath(savedPath, rel.Name)

	this.str("(SELECT ")
	this.sortExpr(segs[1:], this.cur)

	this.str(" FROM " + quoteTable(this.d, target.TableReference()) + " AS " + alias +
		" WHERE " + alias + "." + this.d.Quote(remote.Column) + " = " + cur.correlate(this.d, local.Column))
	if this.rel.At(this.path, target) != nil {
		this.str(" AND ")
		this.hopScope()
	}
	this.str(" LIMIT 1)")
}

func (this Order) render(w *writer) {
	if this.NullsSet && (w.d.Name() == "mysql" || w.d.Name() == "sqlite") {
		w.sortExpr(strings.Split(this.Field, "."), w.current())
		w.str(" IS NULL")
		if this.NullsLast {
			w.str(" ASC, ")
		} else {
			w.str(" DESC, ")
		}
	}
	w.sortExpr(strings.Split(this.Field, "."), w.current())
	if this.Desc {
		w.str(" DESC")
	} else {
		w.str(" ASC")
	}
	if this.NullsSet && w.d.Name() == "postgres" {
		if this.NullsLast {
			w.str(" NULLS LAST")
		} else {
			w.str(" NULLS FIRST")
		}
	}
}
