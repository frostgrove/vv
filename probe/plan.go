package probe

import (
	"github.com/shardit-io/vv/catalog"
	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
)

type termKind uint8

const (
	kindUnique termKind = iota + 1
	kindForeignKey
	kindRestrict
)

func (k termKind) code() errs.Code {
	switch k {
	case kindForeignKey:
		return errs.CodeForeignKey
	case kindRestrict:
		return errs.CodeRestrict
	}
	return errs.CodeUnique
}

// candidate is one constraint the probe can reproduce from values, resolved once
// at declaration. The slice of them is in catalog order, and that order is the
// identity a positional read depends on ([[D-014]]).
type candidate struct {
	kind termKind
	name string
	// cols are the columns of the repository's own table this keys on. They are
	// what the payload has to supply and what a violation's path resolves from.
	cols []string
	// table is the table the term's subquery reads and refCols are its columns,
	// parallel to cols. For a unique key both are this table's own.
	table   string
	refCols []string
	// pkOnly marks the candidate an upsert's ON CONFLICT (pk) target swallows.
	pkOnly bool
}

// mode is which of the three statement shapes a request needs.
type mode uint8

const (
	modeInsert mode = iota // one row, nothing stored to read
	modeUpdate             // one row, FROM t AS cur — unchanged columns come from there
	modeBulk               // many rows through a derived table, each carrying its index
)

// term is one candidate bound to one row: the reference for each of its columns,
// in key order, plus the key of the row that must not count as its own
// collision.
//
// row is the position in the payload, or -1 for a write of one row where no
// index belongs in the path.
type term struct {
	row    int
	cand   candidate
	vals   []ref
	own    ref
	hasOwn bool
}

// plan is what one request probes: one term per constraint per row, in
// constraint order and then row order. That order is the identity the result is
// read by ([[D-014]]).
type plan struct {
	mode  mode
	cands []candidate
	terms []term
	rows  []Row
	// capped says a cap cut something out, so the answer is incomplete whatever
	// the statement finds.
	capped bool
}

// candidatesFor reads the catalog once and keeps every constraint the probe
// could reproduce. Everything it drops here it drops for a reason the catalog
// can state; nothing is dropped because it looked awkward.
func candidatesFor(cat catalog.Catalog, table *catalog.Table, pkCol string) []candidate {
	var out []candidate
	for i := range table.Constraints {
		c := &table.Constraints[i]
		switch c.Kind {
		case catalog.KindPrimaryKey, catalog.KindUnique, catalog.KindUniqueIndex:
			if !reproducible(c) {
				continue
			}
			out = append(out, candidate{
				kind:    kindUnique,
				name:    c.Name,
				cols:    c.Columns,
				table:   table.Name,
				refCols: c.Columns,
				pkOnly:  len(c.Columns) == 1 && c.Columns[0] == pkCol,
			})
		case catalog.KindForeignKey:
			if !reproducible(c) || c.RefTable == "" || len(c.RefColumns) != len(c.Columns) {
				continue
			}
			// A shorthand REFERENCES parent records no parent column on SQLite,
			// and a term cannot be built against a column nothing named.
			if anyEmpty(c.RefColumns) {
				continue
			}
			out = append(out, candidate{
				kind:    kindForeignKey,
				name:    c.Name,
				cols:    c.Columns,
				table:   c.RefTable,
				refCols: c.RefColumns,
			})
		}
	}
	// The inbound direction, which no lookup on Catalog can express.
	if r, ok := cat.(catalog.Referrers); ok {
		for _, c := range r.ReferencedBy(table.Name) {
			if !restricting(c) || !reproducible(c) {
				continue
			}
			if len(c.RefColumns) != len(c.Columns) || anyEmpty(c.RefColumns) || anyEmpty(c.Columns) {
				continue
			}
			out = append(out, candidate{
				kind:    kindRestrict,
				name:    c.Name,
				cols:    c.RefColumns, // our columns, the ones a child points at
				table:   c.Table,
				refCols: c.Columns, // the child's own foreign-key columns
			})
		}
	}
	return out
}

// reproducible reports a constraint the probe can replay from a value. The four
// shapes it cannot are the ones [[D-041]] records the catalog carrying flags
// for, and each of them would make the probe claim a check it did not perform:
//
//   - a partial index, whose predicate is not recoverable as a value;
//   - a prefix key, which compares the first n characters and not the value;
//   - an expression key part, whose text nothing here parses;
//   - a deferrable constraint, which the server does not apply until COMMIT.
func reproducible(c *catalog.Constraint) bool {
	if c.Partial || c.Deferrable || len(c.Columns) == 0 {
		return false
	}
	for _, n := range c.Prefixes {
		if n != 0 {
			return false
		}
	}
	return !anyEmpty(c.Columns)
}

// restricting reports a foreign key whose ON UPDATE action refuses rather than
// propagating. Empty is NO ACTION, which is the standard's default and refuses.
func restricting(c *catalog.Constraint) bool {
	switch c.OnUpdate {
	case "", "NO ACTION", "RESTRICT":
		return true
	}
	return false
}

func anyEmpty(ss []string) bool {
	for _, s := range ss {
		if s == "" {
			return true
		}
	}
	return false
}

// planFor turns the candidates into the terms this request actually needs.
//
// Relevance is by written column: a constraint none of whose columns the write
// touched cannot have been broken by it. What is left has to be bindable — every
// key part either written, or readable from the stored row where there is one.
func (f *full) planFor(req Request) plan {
	d := req.Source.Dialect()
	p := plan{mode: modeFor(req), rows: req.Rows}
	if len(p.rows) > f.cfg.maxRows {
		p.rows = p.rows[:f.cfg.maxRows]
		p.capped = true
	}

	for _, c := range f.cands {
		if f.cfg.skip[c.name] {
			continue
		}
		if req.Upsert && c.kind == kindUnique && f.swallowed(d, c) {
			continue
		}
		if c.kind == kindRestrict && p.mode != modeUpdate {
			// Nothing an insert writes can break an inbound foreign key: there
			// is no old value for a child row to be pointing at.
			continue
		}
		if len(p.cands) == f.cfg.maxConstraints {
			p.capped = true
			break
		}
		before := len(p.terms)
		for i, row := range p.rows {
			idx := -1
			if p.mode == modeBulk {
				idx = i
			}
			if t, ok := f.bind(c, row, p.mode, idx); ok {
				p.terms = append(p.terms, t)
			}
		}
		if len(p.terms) > before {
			p.cands = append(p.cands, c)
		}
	}
	return p
}

func modeFor(req Request) mode {
	switch {
	case req.Batch:
		return modeBulk
	case req.Stored:
		return modeUpdate
	default:
		return modeInsert
	}
}

// swallowed reports a unique key this write's own conflict clause absorbed, so
// no violation may claim it. Which keys those are is the dialect's answer and
// never a hard-coded rule ([[D-019]] difference 11).
func (f *full) swallowed(d crud.Dialect, c candidate) bool {
	if us, ok := d.(crud.UpsertScope); ok && us.UpsertSwallowsPrimaryKeyOnly() {
		return c.pkOnly
	}
	// ON DUPLICATE KEY UPDATE, and anything unknown: every unique key.
	return true
}

// bind resolves one candidate's key parts against the payload, reporting false
// when the candidate cannot be probed at all.
func (f *full) bind(c candidate, row Row, m mode, idx int) (term, bool) {
	var zero term
	touched := false
	vals := make([]ref, 0, len(c.cols))
	for _, col := range c.cols {
		v, written := row.Values[col]
		switch {
		case written:
			touched = true
			if c.kind == kindRestrict && v == nil {
				// A NULL new value cannot be told from an unchanged one with a
				// plain <>, and the two dialect spellings of a null-safe compare
				// disagree. Not probing it is the narrowing answer.
				return zero, false
			}
			vals = append(vals, ref{kind: refBind, val: v})
		case m == modeUpdate && c.kind != kindRestrict:
			// [[D-010]] drops a column whose value already matches the stored
			// one, so the unchanged half of a composite key has no value in the
			// change set. It is read from the row the update did not change.
			vals = append(vals, ref{kind: refCur, name: col})
		default:
			return zero, false
		}
	}
	if !touched {
		return zero, false
	}
	// Every referencing column of a foreign key has to be non-null or the
	// constraint does not apply at all — SQL's MATCH SIMPLE. A bare
	// NOT EXISTS(… WHERE id = NULL) is true, and reporting that is the one
	// thing [[D-042]] rules out.
	//
	// A unique term over a NULL key part is dropped for a different reason:
	// under the default NULLS DISTINCT it matches nothing, and the catalog does
	// not record which semantics a key has. Skipping loses a violation only
	// under NULLS NOT DISTINCT; binding IS NULL would invent one under the
	// default.
	if c.kind != kindRestrict {
		for _, r := range vals {
			if r.known() && r.val == nil {
				return zero, false
			}
		}
	}
	t := term{row: idx, cand: c, vals: vals}
	if c.kind == kindUnique && row.HasID {
		// An update always aims at a row, and so does an upsert. Without this a
		// key the write did not change reports the row colliding with itself.
		t.own, t.hasOwn = ref{kind: refBind, val: row.ID}, true
	}
	return t, true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
