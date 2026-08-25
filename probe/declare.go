package probe

import (
	"errors"
	"fmt"

	"github.com/shardit-io/vv/catalog"
	"github.com/shardit-io/vv/crud"
)

// The declaration-time refusals. Every one of them is something the process
// cannot recover from at request time, so it is a start-up failure ([[D-021]]).
var (
	// ErrUnknownTable reports a catalog that does not know the table the model
	// is bound to. It is also the one thing that tells a blocked introspection
	// from a database with no tables: a MySQL user with no information_schema
	// grants reads zero rows rather than being refused, so Load succeeds and the
	// catalog is empty — and the first declaration that names a table catches it.
	ErrUnknownTable = errors.New("probe: the catalog does not know this table")
	// ErrKeyDoesNotIdentify reports a model whose primary-key column is not a row
	// identity on its own. The probe's exclude-my-own-row clause assumes one, and
	// so does the repository's own WHERE pk = ?.
	ErrKeyDoesNotIdentify = errors.New("probe: the model's primary key does not identify a row on its own")
	// ErrUnknownConstraint reports a Skip naming a constraint the table does not
	// have. A silent no-op would turn the control off on the deploy that renamed
	// the constraint.
	ErrUnknownConstraint = errors.New("probe: the table has no such constraint")
	// ErrNotDeclared reports a handler asked to enrich before it was bound to a
	// model. Reachable only by calling Enrich by hand.
	ErrNotDeclared = errors.New("probe: the handler was never declared against a model")
)

// Declare binds the handler to one model and refuses everything it cannot do.
//
// It returns a new handler rather than mutating the receiver: one Full value
// used to declare two models would otherwise end up describing whichever was
// declared last, and the first repository would probe the wrong table.
func (f *full) Declare(meta *crud.Meta) (Handler, error) {
	if meta == nil || meta.PK == nil {
		return nil, fmt.Errorf("%w: the model declares no primary key", ErrKeyDoesNotIdentify)
	}
	tbl, ok := f.cat.Table(meta.Table)
	if !ok {
		return nil, fmt.Errorf("%w: %s (dialect %s). An empty catalog reads exactly like this: check that the "+
			"connection may read the schema", ErrUnknownTable, meta.Table, f.cat.Dialect())
	}
	if !identifies(tbl, meta.PK.Column) {
		return nil, fmt.Errorf("%w: %s.%s is neither the whole primary key nor a unique key of its own",
			ErrKeyDoesNotIdentify, tbl.Name, meta.PK.Column)
	}
	for name := range f.cfg.skip {
		if _, ok := tbl.Constraint(name); !ok {
			return nil, fmt.Errorf("%w: %s on %s", ErrUnknownConstraint, name, tbl.Name)
		}
	}
	g := *f
	g.meta, g.tbl, g.pkCol = meta, tbl, meta.PK.Column
	g.cands = candidatesFor(f.cat, tbl, meta.PK.Column)
	return &g, nil
}

// identifies reports a column that names one row on its own — the whole primary
// key, or a unique key over exactly that column.
//
// This is where composite primary keys stop. crud.Schema has one PK field, so a
// composite key is not declarable, and a model that maps a single field onto a
// table whose real key is composite is already wrong before the probe sees it:
// the repository's own UPDATE … WHERE pk = ? would touch every row sharing that
// half of the key. Refusing here turns a silent write into a start-up error and
// leaves general composite-key support to the seam that would have to carry it.
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
