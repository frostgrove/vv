package probe

import (
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/catalog"
	"github.com/frostgrove/vv/errs"
)

type termKind uint8

const (
	kindUnique termKind = iota + 1
	kindForeignKey
	kindRestrict
)

func (this termKind) code() errs.Code {
	switch this {
	case kindForeignKey:
		return errs.CodeForeignKey
	case kindRestrict:
		return errs.CodeRestrict
	}
	return errs.CodeUnique
}

type candidate struct {
	kind termKind
	name string

	cols []string

	table   crud.TableRef
	refCols []string

	pkOnly bool
}

type mode uint8

const (
	modeInsert mode = iota
	modeUpdate
	modeBulk
)

type term struct {
	row    int
	cand   candidate
	vals   []ref
	own    ref
	hasOwn bool
}

type plan struct {
	mode  mode
	cands []candidate
	terms []term
	rows  []Row

	capped bool
}

func candidatesFor(cat catalog.Catalog, table *catalog.Table, pkCol string, qualified bool) []candidate {
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
				table:   crud.TableRef{Schema: table.Schema, Name: table.Name},
				refCols: c.Columns,
				pkOnly:  len(c.Columns) == 1 && c.Columns[0] == pkCol,
			})
		case catalog.KindForeignKey:
			if !reproducible(c) || c.RefTable == "" || len(c.RefColumns) != len(c.Columns) {
				continue
			}

			if qualified && c.RefSchema == "" {
				continue
			}

			if anyEmpty(c.RefColumns) {
				continue
			}
			out = append(out, candidate{
				kind:    kindForeignKey,
				name:    c.Name,
				cols:    c.Columns,
				table:   crud.TableRef{Schema: c.RefSchema, Name: c.RefTable},
				refCols: c.RefColumns,
			})
		}
	}

	var inbound []*catalog.Constraint
	if qualified {
		if r, ok := cat.(catalog.QualifiedReferrers); ok {
			inbound = r.ReferencedByRef(crud.TableRef{Schema: table.Schema, Name: table.Name})
		}
	} else if r, ok := cat.(catalog.Referrers); ok {
		inbound = r.ReferencedBy(table.Name)
	}
	for _, c := range inbound {
		if qualified && c.Schema == "" {
			continue
		}
		if !restricting(c) || !reproducible(c) {
			continue
		}
		if len(c.RefColumns) != len(c.Columns) || anyEmpty(c.RefColumns) || anyEmpty(c.Columns) {
			continue
		}
		out = append(out, candidate{
			kind:    kindRestrict,
			name:    c.Name,
			cols:    c.RefColumns,
			table:   crud.TableRef{Schema: c.Schema, Name: c.Table},
			refCols: c.Columns,
		})
	}
	return out
}

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

func (this *full) planFor(request *Request) plan {
	d := request.Source.Dialect()
	p := plan{mode: modeFor(request), rows: request.Rows}
	if len(p.rows) > this.config.maxRows {
		p.rows = p.rows[:this.config.maxRows]
		p.capped = true
	}

	for _, c := range this.cands {
		if this.config.skip[c.name] {
			continue
		}
		if request.Upsert && c.kind == kindUnique && this.swallowed(d, c) {
			continue
		}
		if c.kind == kindRestrict && p.mode != modeUpdate {
			continue
		}
		if len(p.cands) == this.config.maxConstraints {
			p.capped = true
			break
		}
		before := len(p.terms)
		for i, row := range p.rows {
			idx := -1
			if p.mode == modeBulk {
				idx = i
			}
			if t, ok := this.bind(c, row, p.mode, idx); ok {
				p.terms = append(p.terms, t)
			}
		}
		if len(p.terms) > before {
			p.cands = append(p.cands, c)
		}
	}
	return p
}

func modeFor(request *Request) mode {
	switch {
	case request.Batch:
		return modeBulk
	case request.Stored:
		return modeUpdate
	default:
		return modeInsert
	}
}

func (this *full) swallowed(d crud.Dialect, c candidate) bool {
	if us, ok := d.(crud.UpsertScope); ok && us.UpsertSwallowsPrimaryKeyOnly() {
		return c.pkOnly
	}

	return true
}

func (this *full) bind(c candidate, row Row, m mode, idx int) (term, bool) {
	var zero term
	touched := false
	vals := make([]ref, 0, len(c.cols))
	for _, col := range c.cols {
		v, written := row.Values[col]
		switch {
		case written:
			touched = true
			if c.kind == kindRestrict && v == nil {
				return zero, false
			}
			vals = append(vals, ref{kind: refBind, val: v})
		case m == modeUpdate && c.kind != kindRestrict:
			vals = append(vals, ref{kind: refCur, name: col})
		default:
			return zero, false
		}
	}
	if !touched {
		return zero, false
	}

	if c.kind != kindRestrict {
		for _, r := range vals {
			if r.known() && r.val == nil {
				return zero, false
			}
		}
	}
	t := term{row: idx, cand: c, vals: vals}
	if c.kind == kindUnique && row.HasID {
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
