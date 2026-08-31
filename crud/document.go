package crud

import (
	"encoding/json"
	"strings"
)

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

type PredicateError struct {
	Node   string
	Reason string
}

func (this *PredicateError) Error() string {
	return "crud: " + this.Node + " has no filter document: " + this.Reason
}

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

func (this *docWriter) text(s string) {
	b, _ := json.Marshal(s)
	this.sb.Write(b)
}

func (this *docWriter) value(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		this.fail("crud", "a filter value cannot be encoded as JSON: "+err.Error())
		return
	}
	this.sb.Write(b)
}

func (this *docWriter) leaf(field, op string, emit func()) {
	this.str("{")
	this.text(field)
	this.str(":{")
	this.text(op)
	this.str(":")
	emit()
	this.str("}}")
}

func sub(p Predicate) (string, error) {
	var w docWriter
	p.document(&w)
	if w.err != nil {
		return "", w.err
	}
	return w.sb.String(), nil
}

const everyRow = "{}"

func (this cmpNode) document(w *docWriter) {
	if this.undefined {
		w.failWith(&SchemaError{Model: "predicate", Field: this.field, Reason: "an undefined Opt is not a comparison value"})
		return
	}
	op, ok := docOps[this.op]
	if !ok {
		w.fail("crud", "no wire operator for "+this.op)
		return
	}
	w.leaf(this.field, op, func() { w.value(this.value) })
}

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
	if len(this.values) == 0 {
		if this.not {
			w.str(everyRow)
			return
		}
		w.fail("crud.In", "an empty In matches no rows, which no filter document says")
		return
	}
	op := "in"
	if this.not {
		op = "nin"
	}
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
		w.str(`{"not":`)
		emit()
		w.str("}")
		return
	}
	emit()
}

func (this likeNode) document(w *docWriter) {
	if this.mode != likePattern && this.not {
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
	unconditional := false
	for _, k := range live {
		doc, err := sub(k)
		if err != nil {
			w.failWith(err)
			return
		}
		if doc == everyRow {
			if this.op == "OR" {
				unconditional = true
			}
			continue
		}
		docs = append(docs, doc)
	}
	if unconditional {
		w.str(everyRow)
		return
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
