package crud

import (
	"reflect"
	"strings"
)

// Predicate is a node of the WHERE tree. The AST is closed on purpose — the
// only way to produce arbitrary SQL is Raw, which is easy to grep for and easy
// to forbid. A security decorator can therefore trust that whatever it ANDs in
// cannot be peeled off by the caller.
type Predicate interface {
	render(w *writer)
}

// scope is the table a path segment is being resolved against. The root scope
// has no alias, so top-level columns stay unqualified; every relation hop opens
// a subquery with a generated one.
type scope struct {
	meta  *Meta
	alias string
}

// qualify renders a column for use inside its own scope.
func (s scope) qualify(d Dialect, col string) string {
	if s.alias == "" {
		return d.Quote(col)
	}
	return s.alias + "." + d.Quote(col)
}

// correlate renders a column of this scope for use from a deeper one, where
// bare names would be ambiguous.
func (s scope) correlate(d Dialect, col string) string {
	if s.alias == "" {
		return d.Quote(s.meta.Table) + "." + d.Quote(col)
	}
	return s.alias + "." + d.Quote(col)
}

// writer renders an AST against a dialect, resolving field references through
// the model schema and collecting bind arguments.
type writer struct {
	sb    strings.Builder
	args  []any
	d     Dialect
	m     *Meta
	cur   scope
	alias int
	err   error
}

func (w *writer) fail(err error) {
	if w.err == nil {
		w.err = err
	}
}

func (w *writer) nextAlias() string {
	w.alias++
	return "rx" + itoa(w.alias)
}

// current returns the scope in effect, defaulting to the root model.
func (w *writer) current() scope {
	if w.cur.meta == nil {
		return scope{meta: w.m}
	}
	return w.cur
}

// leaf resolves a possibly-nested field path and hands the rendered column
// expression to emit. Every relation hop on the way wraps the condition in a
// correlated EXISTS, which is what keeps `Comments.Body eq x` from multiplying
// rows the way a join would — COUNT and LIMIT stay honest.
func (w *writer) leaf(path string, emit func(col string)) {
	cur := w.current()
	hops, f, _, err := cur.meta.WalkPath(path)
	if err != nil {
		w.fail(err)
		w.str("1 = 0")
		return
	}
	if f == nil {
		w.fail(&SchemaError{Model: cur.meta.Name, Field: path, Reason: "path names a relation, not a column"})
		w.str("1 = 0")
		return
	}
	if len(hops) == 0 {
		emit(cur.qualify(w.d, f.Column))
		return
	}

	saved := w.cur
	defer func() { w.cur = saved }()

	for _, hop := range hops {
		alias := w.nextAlias()
		w.str("EXISTS (SELECT 1 FROM ")
		w.str(w.d.Quote(hop.Target.Table))
		w.str(" AS " + alias)
		if hop.Rel.Kind == ManyToMany {
			j := w.nextAlias()
			w.str(" JOIN " + w.d.Quote(hop.Rel.JoinTable) + " AS " + j +
				" ON " + j + "." + w.d.Quote(hop.Rel.JoinRef) + " = " + alias + "." + w.d.Quote(hop.Remote.Column))
			w.str(" WHERE " + j + "." + w.d.Quote(hop.Rel.JoinLocal) + " = " + cur.correlate(w.d, hop.Local.Column))
		} else {
			w.str(" WHERE " + alias + "." + w.d.Quote(hop.Remote.Column) + " = " + cur.correlate(w.d, hop.Local.Column))
		}
		w.str(" AND ")
		cur = scope{meta: hop.Target, alias: alias}
		w.cur = cur
	}
	emit(cur.qualify(w.d, f.Column))
	w.str(strings.Repeat(")", len(hops)))
}

func (w *writer) column(ref string) {
	w.leaf(ref, func(col string) { w.sb.WriteString(col) })
}

func (w *writer) bind(v any) {
	w.args = append(w.args, v)
	w.sb.WriteString(w.d.Placeholder(len(w.args)))
}

func (w *writer) str(s string) { w.sb.WriteString(s) }

// ---------------------------------------------------------------------------
// nodes

type cmpNode struct {
	field string
	op    string
	value any
}

func (n cmpNode) render(w *writer) {
	w.leaf(n.field, func(col string) {
		w.str(col)
		w.str(" " + n.op + " ")
		w.bind(n.value)
	})
}

type nullNode struct {
	field string
	not   bool
}

func (n nullNode) render(w *writer) {
	w.leaf(n.field, func(col string) {
		w.str(col)
		if n.not {
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

func (n inNode) render(w *writer) {
	if len(n.values) == 0 {
		// IN () is a syntax error everywhere; degrade to a constant.
		if n.not {
			w.str("1 = 1")
		} else {
			w.str("1 = 0")
		}
		return
	}
	w.leaf(n.field, func(col string) {
		w.str(col)
		if n.not {
			w.str(" NOT IN (")
		} else {
			w.str(" IN (")
		}
		for i, v := range n.values {
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

func (n betweenNode) render(w *writer) {
	w.leaf(n.field, func(col string) {
		w.str(col)
		if n.not {
			w.str(" NOT BETWEEN ")
		} else {
			w.str(" BETWEEN ")
		}
		w.bind(n.low)
		w.str(" AND ")
		w.bind(n.hi)
	})
}

type likeNode struct {
	field      string
	pattern    string
	not        bool
	ignoreCase bool
}

func (n likeNode) render(w *writer) {
	w.leaf(n.field, func(col string) {
		if n.ignoreCase {
			w.str("LOWER(" + col + ")")
		} else {
			w.str(col)
		}
		if n.not {
			w.str(" NOT LIKE ")
		} else {
			w.str(" LIKE ")
		}
		if n.ignoreCase {
			w.str("LOWER(")
			w.bind(n.pattern)
			w.str(")")
			return
		}
		w.bind(n.pattern)
	})
}

type fieldCmpNode struct {
	left, right string
	op          string
}

func (n fieldCmpNode) render(w *writer) {
	w.leaf(n.left, func(left string) {
		w.leaf(n.right, func(right string) {
			w.str(left + " " + n.op + " " + right)
		})
	})
}

type logicNode struct {
	op   string // AND / OR
	kids []Predicate
}

// flatten inlines nested nodes with the same operator, so a chain of .And()
// calls renders as one flat clause instead of a staircase of parentheses.
// AND and OR are associative, so this only affects readability.
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

func (n logicNode) render(w *writer) {
	live := flatten(n.op, n.kids, nil)
	switch len(live) {
	case 0:
		// AND of nothing is true, OR of nothing is false.
		if n.op == "AND" {
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
			w.str(" " + n.op + " ")
		}
		k.render(w)
	}
	w.str(")")
}

type notNode struct{ inner Predicate }

func (n notNode) render(w *writer) {
	if n.inner == nil {
		w.str("1 = 0")
		return
	}
	w.str("NOT (")
	n.inner.render(w)
	w.str(")")
}

type constNode bool

func (n constNode) render(w *writer) {
	if n {
		w.str("1 = 1")
	} else {
		w.str("1 = 0")
	}
}

type rawNode struct {
	sql  string
	args []any
}

// render rewrites ? markers into the dialect's placeholders so raw fragments
// stay portable between MySQL and PostgreSQL.
func (n rawNode) render(w *writer) {
	i := 0
	for {
		j := strings.IndexByte(n.sql[i:], '?')
		if j < 0 {
			w.str(n.sql[i:])
			return
		}
		j += i
		w.str(n.sql[i:j])
		if j+1 < len(n.sql) && n.sql[j+1] == '?' { // ?? escapes a literal ?
			w.str("?")
			i = j + 2
			continue
		}
		if len(n.args) == 0 {
			w.fail(&SchemaError{Model: w.m.Name, Reason: "crud.Raw: more ? markers than arguments"})
			return
		}
		w.bind(n.args[0])
		n.args = n.args[1:]
		i = j + 1
	}
}

// ---------------------------------------------------------------------------
// constructors
//
// Every field reference is a Go field name (preferred) or a column name.

// Eq renders `field = ?`, or `field IS NULL` when value is nil.
func Eq(field string, value any) Predicate {
	if isNil(value) {
		return nullNode{field: field}
	}
	return cmpNode{field, "=", value}
}

// Ne renders `field <> ?`, or `field IS NOT NULL` when value is nil.
func Ne(field string, value any) Predicate {
	if isNil(value) {
		return nullNode{field: field, not: true}
	}
	return cmpNode{field, "<>", value}
}

func Gt(field string, value any) Predicate  { return cmpNode{field, ">", value} }
func Gte(field string, value any) Predicate { return cmpNode{field, ">=", value} }
func Lt(field string, value any) Predicate  { return cmpNode{field, "<", value} }
func Lte(field string, value any) Predicate { return cmpNode{field, "<=", value} }

func IsNull(field string) Predicate    { return nullNode{field: field} }
func IsNotNull(field string) Predicate { return nullNode{field: field, not: true} }

func Like(field, pattern string) Predicate { return likeNode{field: field, pattern: pattern} }
func NotLike(field, pattern string) Predicate {
	return likeNode{field: field, pattern: pattern, not: true}
}

// LikeIgnoreCase compares with LOWER() on both sides, which works on MySQL and
// PostgreSQL alike (unlike ILIKE).
func LikeIgnoreCase(field, pattern string) Predicate {
	return likeNode{field: field, pattern: pattern, ignoreCase: true}
}

func Contains(field, s string) Predicate   { return Like(field, "%"+escapeLike(s)+"%") }
func StartsWith(field, s string) Predicate { return Like(field, escapeLike(s)+"%") }
func EndsWith(field, s string) Predicate   { return Like(field, "%"+escapeLike(s)) }

func Between(field string, low, high any) Predicate {
	return betweenNode{field: field, low: low, hi: high}
}

// In renders `field IN (...)`. An empty list is always false.
func In(field string, values ...any) Predicate { return inNode{field: field, values: values} }

// NotIn renders `field NOT IN (...)`. An empty list is always true.
func NotIn(field string, values ...any) Predicate {
	return inNode{field: field, values: values, not: true}
}

// InAny is In for a typed slice.
func InAny[T any](field string, values []T) Predicate {
	return inNode{field: field, values: anySlice(values)}
}

// NotInAny is NotIn for a typed slice.
func NotInAny[T any](field string, values []T) Predicate {
	return inNode{field: field, values: anySlice(values), not: true}
}

// EqField compares two columns of the same model.
func EqField(left, right string) Predicate { return fieldCmpNode{left, right, "="} }

// And is true when every non-nil argument is true (and when there are none).
func And(preds ...Predicate) Predicate { return logicNode{"AND", preds} }

// Or is true when any non-nil argument is true; empty Or is false.
func Or(preds ...Predicate) Predicate { return logicNode{"OR", preds} }

func Not(p Predicate) Predicate { return notNode{p} }

// True and False are the identity elements, useful as a scope default.
func True() Predicate  { return constNode(true) }
func False() Predicate { return constNode(false) }

// Raw is the escape hatch. Use ? for bind markers regardless of dialect; they
// are rewritten. Write ?? for a literal question mark. Column names are NOT
// resolved or quoted here — that is the caller's job.
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

// ---------------------------------------------------------------------------
// sorting

// Order is one ORDER BY term.
type Order struct {
	Field     string
	Desc      bool
	NullsLast bool
	NullsSet  bool
}

// Asc sorts ascending by a field.
func Asc(field string) Order { return Order{Field: field} }

// Desc sorts descending by a field.
func Desc(field string) Order { return Order{Field: field, Desc: true} }

// WithNullsLast places NULLs at the end (PostgreSQL only; MySQL ignores it).
func (o Order) WithNullsLast() Order  { o.NullsLast, o.NullsSet = true, true; return o }
func (o Order) WithNullsFirst() Order { o.NullsLast, o.NullsSet = false, true; return o }

// sortExpr renders a sortable expression for a path. A relation hop becomes a
// scalar subquery, which is the only shape that can appear in ORDER BY; sorting
// through a to-many relation has no single value, so it is refused rather than
// quietly picking one.
func (w *writer) sortExpr(segs []string, cur scope) {
	if len(segs) == 1 {
		f := cur.meta.Field(segs[0])
		if f == nil {
			w.fail(&UnknownFieldError{Model: cur.meta.Name, Field: segs[0]})
			w.str("NULL")
			return
		}
		w.str(cur.qualify(w.d, f.Column))
		return
	}
	rel := cur.meta.Relation(segs[0])
	if rel == nil {
		w.fail(&UnknownFieldError{Model: cur.meta.Name, Field: segs[0]})
		w.str("NULL")
		return
	}
	if rel.Kind.ToMany() {
		w.fail(&SchemaError{Model: cur.meta.Name, Field: rel.Name,
			Reason: "cannot sort through a " + rel.Kind.String() + " relation"})
		w.str("NULL")
		return
	}
	target, local, remote, err := rel.Resolve()
	if err != nil {
		w.fail(err)
		w.str("NULL")
		return
	}
	alias := w.nextAlias()
	w.str("(SELECT ")
	w.sortExpr(segs[1:], scope{meta: target, alias: alias})
	w.str(" FROM " + w.d.Quote(target.Table) + " AS " + alias +
		" WHERE " + alias + "." + w.d.Quote(remote.Column) + " = " + cur.correlate(w.d, local.Column) +
		" LIMIT 1)")
}

func (o Order) render(w *writer) {
	w.sortExpr(strings.Split(o.Field, "."), w.current())
	if o.Desc {
		w.str(" DESC")
	} else {
		w.str(" ASC")
	}
	if o.NullsSet && w.d.Name() == "postgres" {
		if o.NullsLast {
			w.str(" NULLS LAST")
		} else {
			w.str(" NULLS FIRST")
		}
	}
}
