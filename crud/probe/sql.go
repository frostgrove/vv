package probe

import (
	"github.com/frostgrove/vv/crud"
)

const (
	aliasThis   = "vvt"
	aliasParent = "vvp"
	aliasChild  = "vvc"
	aliasCur    = "vvcur"
)

type refKind uint8

const (
	refBind refKind = iota + 1

	refCur
)

type ref struct {
	kind refKind
	val  any
	name string
}

func (this ref) known() bool { return this.kind == refBind }

func (this ref) render(b *crud.SQL) {
	if this.kind == refCur {
		b.Raw(aliasCur + ".").Ident(this.name)
		return
	}
	b.Bind(this.val)
}

func (this *full) statement(p plan, request *Request, scope crud.Predicate) (string, []any, error) {
	d := request.Source.Dialect()
	b := crud.NewSQL(d, request.Meta)
	b.Raw("SELECT ")
	for i := range p.terms {
		if i > 0 {
			b.Raw(", ")
		}
		this.renderTerm(b, p.terms[i], scope)

		b.Raw(" AS ").Ident("c" + itoa(i))
	}
	if p.mode == modeUpdate {
		b.Raw(" FROM ").Table().Raw(" AS " + aliasCur + " WHERE " + aliasCur + ".").
			Ident(this.pkCol).Raw(" = ").Bind(p.rows[0].ID)
	}
	return b.Done()
}

func (this *full) renderTerm(b *crud.SQL, t term, scope crud.Predicate) {
	switch t.cand.kind {
	case kindForeignKey:
		this.renderForeignKey(b, t)
	case kindRestrict:
		this.renderRestrict(b, t)
	default:
		this.renderUnique(b, t, scope)
	}
}

func (this *full) renderUnique(b *crud.SQL, t term, scope crud.Predicate) {
	b.Raw("EXISTS(SELECT 1 FROM ").Table().Raw(" AS " + aliasThis + " WHERE ")
	for i, col := range t.cand.refCols {
		if i > 0 {
			b.Raw(" AND ")
		}
		b.Raw(aliasThis + ".").Ident(col).Raw(" = ")
		t.vals[i].render(b)
	}
	if t.hasOwn {
		b.Raw(" AND " + aliasThis + ".").Ident(this.pkCol).Raw(" <> ")
		t.own.render(b)
	}
	if scope != nil {
		b.Raw(" AND (")
		b.Alias(aliasThis).Predicate(scope)
		b.Raw(")")
	}
	b.Raw(")")
}

func (this *full) renderForeignKey(b *crud.SQL, t term) {
	b.Raw("(")
	for _, r := range t.vals {
		if r.known() {
			continue
		}
		r.render(b)
		b.Raw(" IS NOT NULL AND ")
	}
	b.Raw("NOT EXISTS(SELECT 1 FROM ").TableRef(t.cand.table).Raw(" AS " + aliasParent + " WHERE ")
	for i, col := range t.cand.refCols {
		if i > 0 {
			b.Raw(" AND ")
		}
		b.Raw(aliasParent + ".").Ident(col).Raw(" = ")
		t.vals[i].render(b)
	}
	b.Raw("))")
}

func (this *full) renderRestrict(b *crud.SQL, t term) {
	b.Raw("((")
	for i, col := range t.cand.cols {
		if i > 0 {
			b.Raw(" OR ")
		}
		b.Raw(aliasCur + ".").Ident(col).Raw(" <> ")
		t.vals[i].render(b)
	}
	b.Raw(") AND EXISTS(SELECT 1 FROM ").TableRef(t.cand.table).Raw(" AS " + aliasChild + " WHERE ")
	for i, col := range t.cand.refCols {
		if i > 0 {
			b.Raw(" AND ")
		}
		b.Raw(aliasChild + ".").Ident(col).Raw(" = " + aliasCur + ".").Ident(t.cand.cols[i])
	}
	b.Raw("))")
}
