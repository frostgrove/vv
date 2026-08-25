package probe

import (
	"github.com/shardit-io/vv/crud"
)

// The aliases the probe's own statement uses. They are prefixed because a scope
// predicate rendered inside a term generates its own — rx1, rx2 — and a real
// table may be called anything at all.
const (
	aliasThis   = "vvt"   // this repository's table, inside a unique term
	aliasParent = "vvp"   // the table a foreign key points at
	aliasChild  = "vvc"   // the table whose foreign key points back
	aliasCur    = "vvcur" // the stored row an update is changing
)

type refKind uint8

const (
	// refBind is a value known in Go and bound as a parameter.
	refBind refKind = iota + 1
	// refCur is a column of the stored row, read in SQL rather than carried, so
	// nothing about its value is known here.
	refCur
)

// ref is one value a term compares against.
type ref struct {
	kind refKind
	val  any
	name string
}

// known reports a value this package can reason about. A stored column is not
// one, so any rule about its nullness has to be written into the statement
// instead of decided here.
func (r ref) known() bool { return r.kind == refBind }

func (r ref) render(b *crud.SQL) {
	if r.kind == refCur {
		b.Raw(aliasCur + ".").Ident(r.name)
		return
	}
	b.Bind(r.val)
}

// statement renders the whole probe: one boolean column per term, and the FROM
// an update needs to reach the row it is changing.
//
// One flat statement of N×M columns rather than a derived table of the batch's
// rows, and that is a measurement rather than a preference. A UNION ALL of
// one-row SELECTs runs on all four engines, but PostgreSQL resolves an untyped
// parameter in it to text and the comparison against a bigint column is then
// `operator does not exist: bigint = text` — SQLSTATE 42883. Casting the first
// arm fixes PostgreSQL and breaks MySQL, whose CAST target vocabulary is not its
// column types: CAST(? AS varchar(64)) is a syntax error there. Binding every
// row's values directly needs no type at all, and the caps bound what it costs.
func (f *full) statement(p plan, req Request, scope crud.Predicate) (string, []any, error) {
	d := req.Source.Dialect()
	b := crud.NewSQL(d, req.Meta)
	b.Raw("SELECT ")
	for i := range p.terms {
		if i > 0 {
			b.Raw(", ")
		}
		f.renderTerm(b, p.terms[i], scope)
		// A debuggable alias and nothing more. The result is read by position,
		// because PostgreSQL truncates an identifier at 63 bytes with a NOTICE
		// no driver surfaces, and `AS "x"` is a string literal on MySQL without
		// ANSI_QUOTES.
		b.Raw(" AS ").Ident("c" + itoa(i))
	}
	if p.mode == modeUpdate {
		b.Raw(" FROM ").Table().Raw(" AS " + aliasCur + " WHERE " + aliasCur + ".").
			Ident(f.pkCol).Raw(" = ").Bind(p.rows[0].ID)
	}
	return b.Done()
}

func (f *full) renderTerm(b *crud.SQL, t term, scope crud.Predicate) {
	switch t.cand.kind {
	case kindForeignKey:
		f.renderForeignKey(b, t)
	case kindRestrict:
		f.renderRestrict(b, t)
	default:
		f.renderUnique(b, t, scope)
	}
}

// renderUnique asks whether another row already holds this key.
func (f *full) renderUnique(b *crud.SQL, t term, scope crud.Predicate) {
	b.Raw("EXISTS(SELECT 1 FROM ").Table().Raw(" AS " + aliasThis + " WHERE ")
	for i, col := range t.cand.refCols {
		if i > 0 {
			b.Raw(" AND ")
		}
		b.Raw(aliasThis + ".").Ident(col).Raw(" = ")
		t.vals[i].render(b)
	}
	if t.hasOwn {
		// Its own row is not a collision. Without this an update that changes
		// nothing about the key reports the row colliding with itself.
		b.Raw(" AND " + aliasThis + ".").Ident(f.pkCol).Raw(" <> ")
		t.own.render(b)
	}
	if scope != nil {
		// The same narrowing a read carries, so the probe does not confirm a row
		// the caller could not have seen ([[D-008]]).
		b.Raw(" AND (")
		b.Alias(aliasThis).Predicate(scope)
		b.Raw(")")
	}
	b.Raw(")")
}

// renderForeignKey asks whether the parent row is missing, and refuses to ask at
// all unless every referencing column is non-null.
//
// The guard is "every column", not "this one". Under MATCH SIMPLE any NULL
// column disables the whole constraint, so a composite key with one NULL half
// is satisfied by definition and a probe without the guard reports a violation
// on a row the server accepts.
func (f *full) renderForeignKey(b *crud.SQL, t term) {
	b.Raw("(")
	for _, r := range t.vals {
		if r.known() {
			continue // a nil value skipped the whole term before it got here
		}
		r.render(b)
		b.Raw(" IS NOT NULL AND ")
	}
	b.Raw("NOT EXISTS(SELECT 1 FROM ").Ident(t.cand.table).Raw(" AS " + aliasParent + " WHERE ")
	for i, col := range t.cand.refCols {
		if i > 0 {
			b.Raw(" AND ")
		}
		b.Raw(aliasParent + ".").Ident(col).Raw(" = ")
		t.vals[i].render(b)
	}
	b.Raw("))")
}

// renderRestrict asks whether a child row still points at the value this update
// is changing. It fires only when the value actually changes: a column written
// with the value it already had breaks nothing, and reporting it would be a
// violation the server never raised.
func (f *full) renderRestrict(b *crud.SQL, t term) {
	b.Raw("((")
	for i, col := range t.cand.cols {
		if i > 0 {
			b.Raw(" OR ")
		}
		b.Raw(aliasCur + ".").Ident(col).Raw(" <> ")
		t.vals[i].render(b)
	}
	b.Raw(") AND EXISTS(SELECT 1 FROM ").Ident(t.cand.table).Raw(" AS " + aliasChild + " WHERE ")
	for i, col := range t.cand.refCols {
		if i > 0 {
			b.Raw(" AND ")
		}
		b.Raw(aliasChild + ".").Ident(col).Raw(" = " + aliasCur + ".").Ident(t.cand.cols[i])
	}
	b.Raw("))")
}
