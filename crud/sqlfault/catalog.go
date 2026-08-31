package sqlfault

import (
	"slices"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/catalog"
	"github.com/frostgrove/vv/errs"
)

type Columns interface {
	ConstraintColumns(table, constraint string) ([]string, bool)
}

type QualifiedColumns interface {
	ConstraintColumnsIn(schema, table, constraint string) ([]string, bool)
}

func FromCatalog(cat catalog.Catalog) Columns { return catalogColumns{cat} }

type catalogColumns struct{ cat catalog.Catalog }

func (this catalogColumns) ConstraintColumns(table, constraint string) ([]string, bool) {
	if this.cat == nil {
		return nil, false
	}
	con, ok := this.cat.Constraint(table, constraint)
	return constraintColumns(con, ok)
}

func (this catalogColumns) ConstraintColumnsIn(schema, table, constraint string) ([]string, bool) {
	if this.cat == nil {
		return nil, false
	}
	qualified, ok := this.cat.(catalog.QualifiedCatalog)
	if !ok {
		return nil, false
	}
	con, ok := qualified.ConstraintByRef(crud.TableRef{Schema: schema, Name: table}, constraint)
	return constraintColumns(con, ok)
}

func constraintColumns(con *catalog.Constraint, ok bool) ([]string, bool) {
	if !ok || len(con.Columns) == 0 {
		return nil, false
	}

	out := make([]string, len(con.Columns))
	copy(out, con.Columns)
	return out, true
}

func (this *Classifier) fill(s errs.Source) errs.Source {
	if this.cols == nil || len(s.Columns) > 0 || s.Table == "" || s.Constraint == "" {
		return s
	}
	var cols []string
	var ok bool
	if s.Schema != "" {
		qualified, supported := this.cols.(QualifiedColumns)
		if !supported {
			return s
		}
		cols, ok = qualified.ConstraintColumnsIn(s.Schema, s.Table, s.Constraint)
	} else {
		cols, ok = this.cols.ConstraintColumns(s.Table, s.Constraint)
	}
	if ok && len(cols) > 0 && !slices.Contains(cols, "") {
		s.Columns = cols
	}
	return s
}
