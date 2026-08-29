package crud

import (
	"encoding/json"
	"strings"
)

// This file is the second half of predicate.go's nodes: every one of them
// renders to SQL there and to a filter document here. The two live apart
// because predicate.go is already long, and they cannot drift because
// [Predicate] declares both methods — a node that forgets one does not compile.

// MarshalPredicate renders a predicate as a query-DSL filter document, the
// shape query.Request's Filter carries. It is what lets a filter built in Go be
// sent to a service that speaks this library's wire protocol.
//
// The result is json.RawMessage and not a query.Filter because query imports
// crud, so the dependency cannot run both ways. The caller wraps it:
//
//	doc, err := crud.MarshalPredicate(crud.Build(opts...).Predicate())
//	req.Filter = query.RawFilter(string(doc))
//
// Some predicates have no spelling in the DSL, and those are refused by name
// rather than dropped. A clause that goes missing between a caller and a server
// is a query that answers with more rows than were asked for, and it is the one
// failure a caller cannot see: the response is well-formed, the status is 200,
// and the extra rows look like data.
//
// A nil predicate is an empty document — no narrowing, which is what no filter
// means.
func MarshalPredicate(p Predicate) (json.RawMessage, error) {
	if p == nil {
		return json.RawMessage("{}"), nil
	}
	var w docWriter
	p.document(&w)
	if w.err != nil {
		return nil, w.err
	}
	return json.RawMessage(w.sb.String()), nil
}

// A PredicateError reports a predicate that has no filter document.
//
// Node names the constructor that produced it rather than the internal type,
// because the fix is at the call site that wrote crud.Raw, and the caller has
// never heard of a rawNode.
type PredicateError struct {
	Node   string
	Reason string
}

func (this *PredicateError) Error() string {
	return "crud: " + this.Node + " has no filter document: " + this.Reason
}

// docWriter accumulates a document and remembers the first refusal, the way
// writer does for SQL. Every document method can then be written straight
// through with no error plumbing.
type docWriter struct {
	sb  strings.Builder
	err error
}

func (this *docWriter) failWith(err error) {
	if this.err == nil {
		this.err = err
	}
}

func (this *docWriter) fail(node, reason string) {
	this.failWith(&PredicateError{Node: node, Reason: reason})
}

func (this *docWriter) str(s string) { this.sb.WriteString(s) }

// text writes a JSON string. Field names go through it as well as values: a
// field named `a"b` would otherwise close the key and the remainder of the
// document would be whatever that produced.
func (this *docWriter) text(s string) {
	b, _ := json.Marshal(s) // a string always encodes
	this.sb.Write(b)
}

// value writes a bind value. A value encoding/json refuses — a channel, a NaN —
// fails here rather than producing a document the server would answer 400 to.
func (this *docWriter) value(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		this.fail("crud", "a filter value cannot be encoded as JSON: "+err.Error())
		return
	}
	this.sb.Write(b)
}

// leaf writes {"field":{"op":…}}, the shape every comparison has, with emit
// writing the operand.
//
// One key per object, always. Two conditions on one field merged into a single
// object would need the field name twice, and a JSON object with a repeated key
// decodes to whichever copy came last — half the caller's filter silently gone.
// logicNode writes an array for exactly this reason.
func (this *docWriter) leaf(field, op string, emit func()) {
	this.str("{")
	this.text(field)
	this.str(":{")
	this.text(op)
	this.str(":")
	emit()
	this.str("}}")
}

// sub renders one node on its own, so a parent can look at what it got before
// deciding where to put it. logicNode and notNode both have to: an inner
// document of {} means "every row", and where that lands changes the answer.
func sub(p Predicate) (string, error) {
	var w docWriter
	p.document(&w)
	if w.err != nil {
		return "", w.err
	}
	return w.sb.String(), nil
}

// everyRow is the document that narrows nothing. An empty filter object is how
// the DSL spells it, and there is no document for its opposite — which is why
// the nodes that mean "no rows" are refused instead.
const everyRow = "{}"

// ---------------------------------------------------------------------------
// the nodes, in predicate.go's order

func (this cmpNode) document(w *docWriter) {
	op, ok := docOps[this.op]
	if !ok {
		w.fail("crud", "no wire operator for "+this.op)
		return
	}
	w.leaf(this.field, op, func() { w.value(this.value) })
}

// docOps is the SQL operator each comparison renders with, back to the word the
// DSL reads. Keyed on the rendered operator rather than on a constructor, so
// there is one entry per thing cmpNode can hold.
var docOps = map[string]string{
	"=": "eq", "<>": "ne", ">": "gt", ">=": "gte", "<": "lt", "<=": "lte",
}

func (this nullNode) document(w *docWriter) {
	op := "isnull"
	if this.not {
		op = "isnotnull"
	}
	w.leaf(this.field, op, func() { w.str("true") })
}

func (this inNode) document(w *docWriter) {
	op := "in"
	if this.not {
		op = "nin"
	}
	// An empty list is kept and not refused: the DSL compiles {"in":[]} back to
	// the same always-false node, and {"nin":[]} to the same always-true one.
	w.leaf(this.field, op, func() {
		w.str("[")
		for i, v := range this.values {
			if i > 0 {
				w.str(",")
			}
			w.value(v)
		}
		w.str("]")
	})
}

func (this betweenNode) document(w *docWriter) {
	emit := func() {
		w.leaf(this.field, "between", func() {
			w.str("[")
			w.value(this.low)
			w.str(",")
			w.value(this.hi)
			w.str("]")
		})
	}
	if this.not {
		// No notBetween in the DSL, and no NotBetween constructor here either,
		// so this arm is unreachable today. Negation rather than refusal
		// because NOT (a BETWEEN b AND c) is the same set of rows, spelled
		// differently.
		w.str(`{"not":`)
		emit()
		w.str("}")
		return
	}
	emit()
}

func (this likeNode) document(w *docWriter) {
	if this.mode != likePattern && this.not {
		// The DSL's convenience operators have no negative spellings. Preserve
		// the meaning as a Boolean negation rather than degrading it to raw LIKE.
		w.str(`{"not":`)
		likeNode{field: this.field, pattern: this.pattern, ignoreCase: this.ignoreCase, mode: this.mode}.document(w)
		w.str("}")
		return
	}
	if this.mode != likePattern {
		op := "contains"
		switch this.mode {
		case likeStartsWith:
			op = "startsWith"
		case likeEndsWith:
			op = "endsWith"
		}
		if this.ignoreCase {
			op = "i" + op[:1] + op[1:]
		}
		w.leaf(this.field, op, func() { w.value(this.pattern) })
		return
	}
	switch {
	case this.ignoreCase && this.not:
		// The DSL has ilike and notlike, not both at once. Unreachable from the
		// constructors, and negated rather than refused for betweenNode's
		// reason.
		w.str(`{"not":`)
		w.leaf(this.field, "ilike", func() { w.value(this.pattern) })
		w.str("}")
	case this.ignoreCase:
		w.leaf(this.field, "ilike", func() { w.value(this.pattern) })
	case this.not:
		w.leaf(this.field, "notlike", func() { w.value(this.pattern) })
	default:
		w.leaf(this.field, "like", func() { w.value(this.pattern) })
	}
}

func (this fieldCmpNode) document(w *docWriter) {
	w.fail("crud.EqField", "the wire DSL compares a field to a value, never to another field")
}

func (this logicNode) document(w *docWriter) {
	live := flatten(this.op, this.kids, nil)
	docs := make([]string, 0, len(live))
	for _, k := range live {
		doc, err := sub(k)
		if err != nil {
			w.failWith(err)
			return
		}
		if doc == everyRow {
			// AND with an unconditional term is the rest of the terms; OR with
			// one is unconditional. Getting either backwards changes which rows
			// come back, so neither is left to the server to work out from a
			// document with an empty object in it.
			if this.op == "OR" {
				w.str(everyRow)
				return
			}
			continue
		}
		docs = append(docs, doc)
	}

	switch len(docs) {
	case 0:
		if this.op == "AND" {
			w.str(everyRow)
			return
		}
		w.fail("crud.Or", "an empty Or matches no rows, which no filter document says; sending it as an empty filter would return every row")
	case 1:
		w.str(docs[0])
	default:
		key := "and"
		if this.op == "OR" {
			key = "or"
		}
		w.str(`{"`)
		w.str(key)
		w.str(`":[`)
		w.str(strings.Join(docs, ","))
		w.str("]}")
	}
}

func (this notNode) document(w *docWriter) {
	if this.inner == nil {
		w.fail("crud.Not", "Not(nil) matches no rows, which no filter document says")
		return
	}
	doc, err := sub(this.inner)
	if err != nil {
		w.failWith(err)
		return
	}
	if doc == everyRow {
		// {"not":{}} is the trap this guard exists for: the DSL reads an empty
		// inner object as no condition, drops the not with it, and the caller's
		// "no rows" arrives as "every row" — the exact inversion, with a 200 on
		// it.
		w.fail("crud.Not", "Not of an unconditional predicate matches no rows, which no filter document says")
		return
	}
	w.str(`{"not":`)
	w.str(doc)
	w.str("}")
}

func (this constNode) document(w *docWriter) {
	if this {
		w.str(everyRow)
		return
	}
	w.fail("crud.False", "False matches no rows, which no filter document says; sending it as an empty filter would return every row")
}

func (this rawNode) document(w *docWriter) {
	w.fail("crud.Raw", "it is SQL, and a filter document carries field paths and values")
}
