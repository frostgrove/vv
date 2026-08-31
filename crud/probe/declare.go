package probe

import (
	"errors"
	"fmt"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/catalog"
)

var (
	ErrUnknownTable = errors.New("probe: the catalog does not know this table")

	ErrQualifiedCatalog = errors.New("probe: the catalog cannot look up qualified tables")

	ErrKeyDoesNotIdentify = errors.New("probe: the model's primary key does not identify a row on its own")

	ErrUnknownConstraint = errors.New("probe: the table has no such constraint")

	ErrNotDeclared = errors.New("probe: the handler was never declared against a model")
)

func (this *full) Declare(meta *crud.Meta) (Handler, error) {
	if meta == nil || meta.PK == nil {
		return nil, fmt.Errorf("%w: the model declares no primary key", ErrKeyDoesNotIdentify)
	}
	ref := meta.TableReference()
	var tbl *catalog.Table
	var ok bool
	if ref.Schema == "" {
		tbl, ok = this.cat.Table(ref.Name)
	} else {
		qualified, supported := this.cat.(catalog.QualifiedCatalog)
		if !supported {
			return nil, fmt.Errorf("%w: %s (dialect %s)", ErrQualifiedCatalog, ref.String(), this.cat.Dialect())
		}
		tbl, ok = qualified.TableByRef(ref)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s (dialect %s). An empty catalog reads exactly like this: check that the "+
			"connection may read the schema", ErrUnknownTable, meta.Table, this.cat.Dialect())
	}
	if !identifies(tbl, meta.PK.Column) {
		return nil, fmt.Errorf("%w: %s.%s is neither the whole primary key nor a unique key of its own",
			ErrKeyDoesNotIdentify, tbl.Name, meta.PK.Column)
	}
	for name := range this.config.skip {
		if _, ok := tbl.Constraint(name); !ok {
			return nil, fmt.Errorf("%w: %s on %s", ErrUnknownConstraint, name, tbl.Name)
		}
	}
	g := *this
	g.meta, g.tbl, g.pkCol = meta, tbl, meta.PK.Column
	g.cands = candidatesFor(this.cat, tbl, meta.PK.Column, ref.Schema != "")
	return &g, nil
}

func identifies(t *catalog.Table, col string) bool {
	if len(t.PrimaryKey) == 1 && t.PrimaryKey[0] == col {
		return true
	}
	for i := range t.Constraints {
		c := &t.Constraints[i]
		switch c.Kind {
		case catalog.KindPrimaryKey, catalog.KindUnique, catalog.KindUniqueIndex:
		default:
			continue
		}
		if len(c.Columns) == 1 && c.Columns[0] == col && !c.Partial && !c.Deferrable {
			return true
		}
	}
	return false
}
