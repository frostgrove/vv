package probe

import (
	"context"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/catalog"
	"github.com/frostgrove/vv/errs"
)

func Full(cat catalog.Catalog, o ...Option) Handler {
	f := &full{cat: cat, config: defaults()}
	for _, opt := range o {
		opt(&f.config)
	}
	return f
}

type full struct {
	cat    catalog.Catalog
	config config

	meta  *crud.Meta
	tbl   *catalog.Table
	pkCol string
	cands []candidate
}

func (this *full) Savepoints() (bool, int) { return this.config.savepoints, this.config.maxSavepoints }

func (this *full) Enrich(ctx context.Context, request *Request) (*errs.Fault, error) {
	if request == nil || request.Fault == nil {
		return nil, nil
	}
	if this.meta == nil {
		return request.Fault, ErrNotDeclared
	}
	if request.Meta == nil || request.Source == nil || len(request.Rows) == 0 {
		return request.Fault, nil
	}
	if !this.runs(ctx, request) {
		return request.Fault, nil
	}

	p := this.planFor(request)
	found := this.duplicates(p)

	partial, probeErr := p.capped, error(nil)
	if len(p.terms) > 0 {
		hits, err := this.run(ctx, request, p)
		if err != nil {
			probeErr, partial = err, true
		} else {
			found = append(found, hits...)
		}
	}
	return this.merge(request, found, partial), probeErr
}

func (this *full) runs(ctx context.Context, request *Request) bool {
	_, inTx, owned := crud.OwnedExecutorFor(ctx, request.Source)
	if !inTx {
		return true
	}
	if sr, ok := request.Source.Dialect().(crud.StatementRollback); ok && sr.RollsBackStatementOnly() {
		return true
	}

	return owned && request.Recovered
}

func (this *full) run(ctx context.Context, request *Request, p plan) ([]finding, error) {
	var scope crud.Predicate
	if this.config.scope != nil {
		s, err := this.config.scope(ctx)
		if err != nil {
			return nil, err
		}
		scope = s
	}
	q, args, err := this.statement(p, request, scope)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, this.config.timeout)
	defer cancel()

	ex, ok := crud.ExecutorFor(ctx, request.Source)
	if !ok {
		ex = request.Source
	}
	rows, err := ex.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cells := make([]any, len(p.terms))
	dest := make([]any, len(cells))
	for i := range cells {
		dest[i] = &cells[i]
	}

	var out []finding
	if !rows.Next() {
		return nil, rows.Err()
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	for i := range p.terms {
		if truthy(cells[i]) {
			out = append(out, finding{row: p.terms[i].row, cand: p.terms[i].cand})
		}
	}
	return out, rows.Err()
}

func (this *full) merge(request *Request, found []finding, partial bool) *errs.Fault {
	g := *request.Fault
	g.Violations = make([]errs.Violation, len(request.Fault.Violations))
	copy(g.Violations, request.Fault.Violations)

	mine := make([]errs.Violation, 0, len(found))
	for _, fd := range found {
		mine = append(mine, this.violation(request, fd))
	}

	keep := make([]bool, len(mine))
	for i := range keep {
		keep[i] = true
	}
	for i := range g.Violations {
		if j, ok := this.same(&g.Violations[i], mine, keep); ok {
			fold(&g.Violations[i], mine[j], this.config.codeOnly)
			keep[j] = false
		}
	}
	for i, v := range mine {
		if keep[i] {
			g.Violations = append(g.Violations, v)
		}
	}
	if partial {
		g.Partial = true
	}
	errs.SortViolations(g.Violations)
	return &g
}

func (this *full) same(dv *errs.Violation, mine []errs.Violation, keep []bool) (int, bool) {
	if dv.Origin != errs.OriginState || dv.Code == "" {
		return 0, false
	}
	if dv.Source.Constraint != "" {
		for i, v := range mine {
			if keep[i] && v.Source.Constraint == dv.Source.Constraint && this.sameSource(dv.Source, v.Source, true) {
				return i, true
			}
		}
		return 0, false
	}
	if len(dv.Path) > 0 {
		return 0, false
	}
	found, n := 0, 0
	for i, v := range mine {
		if keep[i] && v.Code == dv.Code && this.sameSource(dv.Source, v.Source, false) {
			found, n = i, n+1
		}
	}
	return found, n == 1
}

func (this *full) sameSource(driver, probed errs.Source, named bool) bool {
	if driver.Schema != "" && driver.Schema != probed.Schema {
		return false
	}
	if driver.Table != "" && driver.Table != probed.Table {
		return false
	}

	if named && this.meta != nil && this.meta.TableReference().Schema != "" {
		return driver.Schema != "" && driver.Table != "" &&
			driver.Schema == probed.Schema && driver.Table == probed.Table
	}
	return true
}

func fold(dv *errs.Violation, pv errs.Violation, codeOnly bool) {
	if len(dv.Path) == 0 && !codeOnly {
		dv.Path, dv.Approximate = pv.Path, pv.Approximate
	}
	if dv.Source.Schema == "" {
		dv.Source.Schema = pv.Source.Schema
	}
	if dv.Source.Table == "" {
		dv.Source.Table = pv.Source.Table
	}
	if len(dv.Source.Columns) == 0 {
		dv.Source.Columns = pv.Source.Columns
	}
	if dv.Source.Constraint == "" {
		dv.Source.Constraint = pv.Source.Constraint
	}
	if dv.Params == nil {
		dv.Params = pv.Params
	}
}

func (this *full) violation(request *Request, fd finding) errs.Violation {
	v := errs.Violation{
		Code:   fd.cand.kind.code(),
		Origin: errs.OriginState,
		Source: errs.Source{
			Table:      this.tbl.Name,
			Schema:     this.tbl.Schema,
			Columns:    fd.cand.cols,
			Constraint: fd.cand.name,
		},
	}
	if !this.config.codeOnly {
		v.Path, v.Approximate = this.path(request, fd)
	}
	if this.config.values {
		if val, ok := valueOf(request, fd); ok {
			v.Params = errs.P{"value": val}
		}
	}
	return v
}

func (this *full) path(request *Request, fd finding) (errs.Path, bool) {
	var head errs.Path
	if fd.row >= 0 {
		head = errs.Path{errs.Indexed(fd.row)}
	}
	if request.Resolve == nil {
		return head, true
	}
	tail, ok := request.Resolve(this.tbl.Name, fd.cand.cols)
	if !ok {
		return head, true
	}
	return append(head, tail...), false
}

func valueOf(request *Request, fd finding) (any, bool) {
	i := fd.row
	if i < 0 {
		i = 0
	}
	if i >= len(request.Rows) || len(fd.cand.cols) != 1 {
		return nil, false
	}
	v, ok := request.Rows[i].Values[fd.cand.cols[0]]
	return v, ok && v != nil
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case int64:
		return x != 0
	case int32:
		return x != 0
	case int:
		return x != 0
	case float64:
		return x != 0
	case []byte:
		return len(x) == 1 && x[0] == '1'
	case string:
		return x == "1" || x == "true" || x == "t"
	}
	return false
}

var (
	_ Handler     = (*full)(nil)
	_ Declarer    = (*full)(nil)
	_ Savepointer = (*full)(nil)
)
