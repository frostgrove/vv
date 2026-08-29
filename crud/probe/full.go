package probe

import (
	"context"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/catalog"
	"github.com/frostgrove/vv/errs"
)

// Full issues one extra statement — one boolean column per constraint the write
// could have broken — and reports every violation it finds beside the one the
// driver already reported.
//
// It reads the catalog and never the database's schema at request time, so a
// table it cannot find is a start-up failure and not a surprise on the first
// collision ([[D-041]], [[D-021]]).
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

	// Bound by Declare, and nil until then.
	meta  *crud.Meta
	tbl   *catalog.Table
	pkCol string
	cands []candidate
}

// Savepoints answers the decorator: whether the write has to be wrapped in one,
// and how many one transaction may hold.
func (this *full) Savepoints() (bool, int) { return this.config.savepoints, this.config.maxSavepoints }

// Enrich runs the probe and returns the driver's fault with whatever it found
// added to it.
//
// The returned fault is a copy. A *Fault is a value two goroutines may render at
// once, the adapter that produced it may have handed the same pointer to a
// caller who already wrapped it, and [[D-042]] treats it as immutable.
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
		// Inside a transaction this engine poisons, with nothing restoring it.
		// Simple is the honest answer, and it is what §8's table calls the
		// default here.
		return request.Fault, nil
	}

	p := this.planFor(request)
	found := this.duplicates(p) // a map, not a statement

	partial, probeErr := p.capped, error(nil)
	if len(p.terms) > 0 {
		hits, err := this.run(ctx, request, p)
		if err != nil {
			// The probe is the most failure-prone part of this design: it
			// re-binds values from a statement that already failed. Downgrading
			// a correct 409 into an opaque 500 here would be the exact inversion
			// of the point ([[D-042]]).
			probeErr, partial = err, true
		} else {
			found = append(found, hits...)
		}
	}
	return this.merge(request, found, partial), probeErr
}

// runs answers the transaction matrix. Which side of it a dialect is on comes
// from crud.StatementRollback and never from its name ([[D-019]]).
func (this *full) runs(ctx context.Context, request *Request) bool {
	_, inTx, owned := crud.OwnedExecutorFor(ctx, request.Source)
	if !inTx {
		return true
	}
	if sr, ok := request.Source.Dialect().(crud.StatementRollback); ok && sr.RollsBackStatementOnly() {
		return true
	}
	// A foreign transaction is never restored, whatever WithSavepoints says, so
	// owned is part of the test rather than implied by Recovered.
	return owned && request.Recovered
}

// run renders the statement, sends it, and reads the answer by position.
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

	// The write has already failed and the client is waiting. A probe that hangs
	// costs the request twice over, and a timeout takes the probe-error path
	// rather than the response's.
	ctx, cancel := context.WithTimeout(ctx, this.config.timeout)
	defer cancel()

	ex, ok := crud.ExecutorFor(ctx, request.Source)
	if !ok {
		// No transaction here, so the source itself. A crud.ReadWrite pair
		// forwards this to its primary, which is what [[D-032]] asks for: the
		// probe decides the content of a write's response.
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
		// An update whose row is gone reads no rows at all. Nothing found is a
		// narrowing, and the driver's own violation is untouched.
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

// merge builds the answer: the driver's violations, then the probe's, with the
// duplicate between them folded rather than listed twice.
func (this *full) merge(request *Request, found []finding, partial bool) *errs.Fault {
	g := *request.Fault
	g.Violations = make([]errs.Violation, len(request.Fault.Violations))
	copy(g.Violations, request.Fault.Violations)

	mine := make([]errs.Violation, 0, len(found))
	for _, fd := range found {
		mine = append(mine, this.violation(request, fd))
	}
	// Deduplicating one way round only. The driver's violation is what actually
	// happened and stays in the answer whatever the probe found ([[D-042]]); what
	// it gains is the path it never had, which is the first time a MySQL
	// duplicate key can name a field at all.
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

// same finds the probe violation that describes what the driver reported.
//
// By constraint name where the driver named one, which is PostgreSQL. Where it
// named none — MySQL, MariaDB and SQLite carry no constraint in their structured
// error at all — by code, and only when exactly one probe violation carries that
// code. With two, there is no way to tell which of them the engine stopped at,
// and folding into the wrong one would move a path onto a field that is correct.
func (this *full) same(dv *errs.Violation, mine []errs.Violation, keep []bool) (int, bool) {
	if dv.Origin != errs.OriginState || dv.Code == "" {
		return 0, false
	}
	if dv.Source.Constraint != "" {
		for i, v := range mine {
			if keep[i] && v.Source.Constraint == dv.Source.Constraint {
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
		if keep[i] && v.Code == dv.Code {
			found, n = i, n+1
		}
	}
	return found, n == 1
}

func fold(dv *errs.Violation, pv errs.Violation, codeOnly bool) {
	if len(dv.Path) == 0 && !codeOnly {
		dv.Path, dv.Approximate = pv.Path, pv.Approximate
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

// violation turns one finding into the public shape.
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
		// Only ever from the payload, and only when the caller asked. The
		// default keeps a unique-violation response from confirming which value
		// exists.
		if val, ok := valueOf(request, fd); ok {
			v.Params = errs.P{"value": val}
		}
	}
	return v
}

// path prefixes the row index onto whatever hop the caller resolved. The index
// is this package's own — nobody else knows which row of the batch a term ran
// for — and the column-to-field hop belongs to the layer that performed the
// mapping ([[D-043]]).
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
		// A composite key has no single offending value, and inventing one by
		// joining them would put two internal column orders into a message.
		return nil, false
	}
	v, ok := request.Rows[i].Values[fd.cand.cols[0]]
	return v, ok && v != nil
}

// truthy reads one boolean cell. The four engines answer a SELECT EXISTS(...)
// four ways — a bool on PostgreSQL, an int64 on MySQL and MariaDB, an int on
// SQLite, and a driver is free to hand any of them back as bytes.
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
