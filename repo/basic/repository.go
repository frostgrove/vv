package basic

import (
	"context"
	"strings"

	"rx-crud/crud"
)

// repository is the SQL implementation of crud.Core.
type repository[M any, ID comparable, U any] struct {
	src  crud.Source
	bp   *Blueprint[M, ID, U]
	meta *crud.Meta
	d    crud.Dialect

	// Static statement fragments, assembled once at Bind time.
	selectFrom string // SELECT c1, c2 FROM "t"
	countFrom  string // SELECT count(*) FROM "t"
	deleteFrom string // DELETE FROM "t"
	returning  string // " RETURNING c1, c2" or ""
	insertGen  string // INSERT with a database-generated primary key
	insertFull string // INSERT with every insertable column
	upsertTail string // conflict clause for insertFull
}

func newRepository[M any, ID comparable, U any](src crud.Source, bp *Blueprint[M, ID, U]) *repository[M, ID, U] {
	r := &repository[M, ID, U]{src: src, bp: bp, meta: bp.meta, d: src.Dialect()}
	m, d := r.meta, r.d
	q := d.Quote

	cols := joinColumns(d, m.Fields)
	table := q(m.Table)

	r.selectFrom = "SELECT " + cols + " FROM " + table
	r.countFrom = "SELECT count(*) FROM " + table
	r.deleteFrom = "DELETE FROM " + table
	if d.SupportsReturning() {
		r.returning = " RETURNING " + cols
	}

	r.insertGen = insertStmt(d, table, m.InsertGen)
	r.insertFull = insertStmt(d, table, m.Insert)

	upd := make([]string, 0, len(m.Update))
	for _, f := range m.Update {
		upd = append(upd, f.Column)
	}
	r.upsertTail = d.Upsert(m.PK.Column, upd)
	return r
}

func joinColumns(d crud.Dialect, fields []*crud.Field) string {
	var b strings.Builder
	for i, f := range fields {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.Quote(f.Column))
	}
	return b.String()
}

func insertStmt(d crud.Dialect, table string, fields []*crud.Field) string {
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(table)
	b.WriteString(" (")
	b.WriteString(joinColumns(d, fields))
	b.WriteString(") VALUES (")
	for i := range fields {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.Placeholder(i + 1))
	}
	b.WriteString(")")
	return b.String()
}

// exec picks the foreign executor bound to ctx over the repository's own source.
func (r *repository[M, ID, U]) exec(ctx context.Context) crud.Executor {
	if e, ok := crud.ExecutorFrom(ctx); ok {
		return e
	}
	return r.src
}

func (r *repository[M, ID, U]) Meta() *crud.Meta { return r.meta }

func (r *repository[M, ID, U]) Tx(ctx context.Context, fn func(context.Context) error) error {
	return crud.InTx(ctx, r.src, fn)
}

// ---------------------------------------------------------------------------
// reads

func (r *repository[M, ID, U]) GetByID(ctx context.Context, id ID, opts ...crud.Option) (M, error) {
	var zero M
	o := crud.Build(append([]crud.Option{
		crud.Where(crud.Eq(r.meta.PK.Name, id)), crud.Limit(1), crud.Unsorted(),
	}, opts...)...)
	items, err := r.find(ctx, o, 1, 0)
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, crud.ErrNotFound
	}
	return items[0], nil
}

func (r *repository[M, ID, U]) Get(ctx context.Context, opts ...crud.Option) (crud.PaginatedResponse[M], error) {
	o := crud.Build(opts...)
	limit, offset, page := o.Resolved(r.bp.set.defaultLimit, r.bp.set.maxLimit)

	// Without a COUNT we still want an honest HasNext, so we fetch one row past
	// the page and throw it away.
	probe := limit
	if o.NoTotal && limit > 0 {
		probe = limit + 1
	}
	items, err := r.find(ctx, o, probe, offset)
	if err != nil {
		return crud.PaginatedResponse[M]{}, err
	}

	if o.NoTotal && limit > 0 {
		more := len(items) > limit
		if more {
			items = items[:limit]
		}
		resp := crud.NewPaginatedResponse(items, page, limit, int64(offset+len(items)))
		resp.TotalPages = 0
		resp.HasNext = more
		return resp, nil
	}

	var total int64
	switch {
	case o.Unpaged, o.NoTotal:
		total = int64(offset + len(items))
	case offset == 0 && len(items) < limit:
		// A short first page is already the whole answer.
		total = int64(len(items))
	default:
		if total, err = r.Count(ctx, crud.With(o)); err != nil {
			return crud.PaginatedResponse[M]{}, err
		}
	}
	return crud.NewPaginatedResponse(items, page, limit, total), nil
}

func (r *repository[M, ID, U]) GetAll(ctx context.Context, opts ...crud.Option) ([]M, error) {
	o := crud.Build(opts...)
	if o.Limit == 0 && o.Page == 0 && o.Offset == 0 {
		o.Unpaged = true
	}
	limit, offset, _ := o.Resolved(r.bp.set.defaultLimit, r.bp.set.maxLimit)
	return r.find(ctx, o, limit, offset)
}

// scoped folds the repository's permanent scope into a caller's predicate.
func (r *repository[M, ID, U]) scoped(o *crud.Options) crud.Predicate {
	if r.bp.set.scope == nil {
		return o.Predicate()
	}
	return crud.And(r.bp.set.scope, o.Predicate())
}

func (r *repository[M, ID, U]) find(ctx context.Context, o *crud.Options, limit, offset int) ([]M, error) {
	cols, err := r.projection(o)
	if err != nil {
		return nil, err
	}

	b := crud.NewSQL(r.d, r.meta)
	switch {
	case o.Distinct:
		b.Raw("SELECT DISTINCT ").Columns(cols).Raw(" FROM ").Table()
	case cols == nil:
		b.Raw(r.selectFrom)
	default:
		b.Raw("SELECT ").Columns(cols).Raw(" FROM ").Table()
	}
	b.Where(r.scoped(o)).OrderBy(r.sortOf(o, limit > 0)).LimitOffset(limit, offset)
	if o.ForUpdate {
		b.Raw(r.d.LockClause())
	}
	q, args, err := b.Done()
	if err != nil {
		return nil, err
	}
	items, err := r.queryCols(ctx, q, args, limit, cols)
	if err != nil {
		return nil, err
	}
	if err := r.preload(ctx, items, o); err != nil {
		return nil, err
	}
	return items, nil
}

// projection resolves Select() into a field list, always keeping the primary
// key: it is what preloads join on and what a client needs to address the row.
// A nil result means "every column", which lets the prebuilt statement be used.
func (r *repository[M, ID, U]) projection(o *crud.Options) ([]*crud.Field, error) {
	if len(o.Fields) == 0 {
		return nil, nil
	}
	out := make([]*crud.Field, 0, len(o.Fields)+1)
	seen := map[string]bool{}
	add := func(f *crud.Field) {
		if !seen[f.Name] {
			seen[f.Name] = true
			out = append(out, f)
		}
	}
	add(r.meta.PK)
	for _, name := range o.Fields {
		f := r.meta.Field(name)
		if f == nil {
			return nil, &crud.UnknownFieldError{Model: r.meta.Name, Field: name}
		}
		add(f)
	}
	// Anything a preload joins on has to come back too.
	for _, spec := range o.Preloads {
		seg, _, _ := strings.Cut(spec.Path, ".")
		if rel := r.meta.Relation(seg); rel != nil {
			if local, err := rel.Local(); err == nil {
				add(local)
			}
		}
	}
	return out, nil
}

// preload runs the requested relation loads against the same executor, so a
// preload inside a transaction sees the transaction.
func (r *repository[M, ID, U]) preload(ctx context.Context, items []M, o *crud.Options) error {
	if len(o.Preloads) == 0 || len(items) == 0 {
		return nil
	}
	return crud.RunPreloads(ctx, r.exec(ctx), r.d, r.meta, items, o.Preloads, r.bp.set.preloadDepth)
}

// sortOf resolves the sort terms, appending a primary-key tiebreaker so that
// paginated results are stable across pages.
func (r *repository[M, ID, U]) sortOf(o *crud.Options, paged bool) []crud.Order {
	if o.NoSort {
		return nil
	}
	sort := o.Sort
	if len(sort) == 0 {
		sort = r.bp.set.defaultSort
	}
	if !paged || !r.bp.set.stableSort {
		return sort
	}
	for _, s := range sort {
		if f := r.meta.Field(s.Field); f != nil && f.PK {
			return sort
		}
	}
	return append(append([]crud.Order{}, sort...), crud.Asc(r.meta.PK.Name))
}

func (r *repository[M, ID, U]) Count(ctx context.Context, opts ...crud.Option) (int64, error) {
	o := crud.Build(opts...)
	q, args, err := crud.NewSQL(r.d, r.meta).Raw(r.countFrom).Where(r.scoped(o)).Done()
	if err != nil {
		return 0, err
	}
	return r.scalar(ctx, q, args)
}

func (r *repository[M, ID, U]) Exists(ctx context.Context, opts ...crud.Option) (bool, error) {
	o := crud.Build(opts...)
	b := crud.NewSQL(r.d, r.meta).Raw("SELECT 1 FROM ").Table().Where(r.scoped(o)).Raw(" LIMIT 1")
	q, args, err := b.Done()
	if err != nil {
		return false, err
	}
	rows, err := r.exec(ctx).Query(ctx, q, args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	if rows.Next() {
		return true, rows.Err()
	}
	return false, rows.Err()
}

// ---------------------------------------------------------------------------
// writes

func (r *repository[M, ID, U]) Save(ctx context.Context, m *M) error {
	if m == nil {
		return &crud.SchemaError{Model: r.meta.Name, Reason: "Save called with a nil model"}
	}
	hasID, err := r.meta.HasID(m)
	if err != nil {
		return err
	}
	switch {
	case !hasID && r.meta.PK.Auto:
		return r.insert(ctx, m, r.insertGen, r.meta.InsertGen, true)
	case !hasID:
		return crud.ErrMissingID
	default:
		return r.insert(ctx, m, r.insertFull+r.upsertTail, r.meta.Insert, false)
	}
}

// insert runs an INSERT (optionally with a conflict clause) and refreshes the
// model with whatever the database produced.
func (r *repository[M, ID, U]) insert(ctx context.Context, m *M, stmt string, fields []*crud.Field, generatedPK bool) error {
	args, err := r.meta.Values(m, fields)
	if err != nil {
		return err
	}
	ex := r.exec(ctx)

	if r.d.SupportsReturning() {
		rows, err := ex.Query(ctx, stmt+r.returning, args...)
		if err != nil {
			return err
		}
		ok, err := r.scanOne(rows, m)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		// ON CONFLICT DO NOTHING matched an existing row: read it back.
		return r.refresh(ctx, m)
	}

	res, err := ex.Exec(ctx, stmt, args...)
	if err != nil {
		return err
	}
	if generatedPK && res.HasLastInsertID && res.LastInsertID != 0 {
		if err := r.meta.SetID(m, res.LastInsertID); err != nil {
			return err
		}
	}
	if r.meta.HasGen {
		return r.refresh(ctx, m)
	}
	return nil
}

// refresh re-reads the row identified by the model's primary key.
func (r *repository[M, ID, U]) refresh(ctx context.Context, m *M) error {
	id, err := r.meta.ID(m)
	if err != nil {
		return err
	}
	q, args, err := crud.NewSQL(r.d, r.meta).Raw(r.selectFrom).Raw(" WHERE ").
		Ident(r.meta.PK.Column).Raw(" = ").Bind(id).Raw(" LIMIT 1").Done()
	if err != nil {
		return err
	}
	rows, err := r.exec(ctx).Query(ctx, q, args...)
	if err != nil {
		return err
	}
	ok, err := r.scanOne(rows, m)
	if err != nil {
		return err
	}
	if !ok {
		return crud.ErrNotFound
	}
	return nil
}

func (r *repository[M, ID, U]) Update(ctx context.Context, id ID, dto any) (M, error) {
	var zero M

	// Inside somebody's transaction we can lock the row we are about to diff
	// against; outside of one, locking would be pointless.
	loadOpts := []crud.Option{crud.Where(crud.Eq(r.meta.PK.Name, id)), crud.Limit(1), crud.Unsorted()}
	if _, inTx := crud.ExecutorFrom(ctx); inTx {
		loadOpts = append(loadOpts, crud.ForUpdate())
	}
	found, err := r.GetAll(ctx, loadOpts...)
	if err != nil {
		return zero, err
	}
	if len(found) == 0 {
		return zero, crud.ErrNotFound
	}
	cur := found[0]

	changes, err := r.bp.plan.Changes(dto, &cur)
	if err != nil {
		return zero, err
	}
	if len(changes) == 0 {
		return cur, nil
	}

	b := crud.NewSQL(r.d, r.meta).Raw("UPDATE ").Table().Raw(" SET ")
	for i, ch := range changes {
		if i > 0 {
			b.Raw(", ")
		}
		b.Ident(ch.Field.Column).Raw(" = ").Bind(ch.Value)
	}
	b.Raw(" WHERE ").Ident(r.meta.PK.Column).Raw(" = ").Bind(id)

	if r.d.SupportsReturning() {
		q, args, err := b.Raw(r.returning).Done()
		if err != nil {
			return zero, err
		}
		rows, err := r.exec(ctx).Query(ctx, q, args...)
		if err != nil {
			return zero, err
		}
		ok, err := r.scanOne(rows, &cur)
		if err != nil {
			return zero, err
		}
		if !ok {
			return zero, crud.ErrNotFound
		}
		return cur, nil
	}

	q, args, err := b.Done()
	if err != nil {
		return zero, err
	}
	// MySQL reports 0 rows affected for a write that changed nothing, so
	// rows-affected is not evidence of absence here — and we already know the
	// row exists, we just read it.
	if _, err := r.exec(ctx).Exec(ctx, q, args...); err != nil {
		return zero, err
	}
	if r.meta.HasGen {
		if err := r.refresh(ctx, &cur); err != nil {
			return zero, err
		}
		return cur, nil
	}
	r.bp.plan.Apply(changes, &cur)
	return cur, nil
}

func (r *repository[M, ID, U]) Delete(ctx context.Context, ids ...ID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	b := crud.NewSQL(r.d, r.meta).Raw(r.deleteFrom).Raw(" WHERE ").Ident(r.meta.PK.Column)
	if len(ids) == 1 {
		b.Raw(" = ").Bind(ids[0])
	} else {
		b.Raw(" IN (")
		for i, id := range ids {
			if i > 0 {
				b.Raw(", ")
			}
			b.Bind(id)
		}
		b.Raw(")")
	}
	q, args, err := b.Done()
	if err != nil {
		return 0, err
	}
	res, err := r.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected, nil
}

func (r *repository[M, ID, U]) DeleteAll(ctx context.Context, opts ...crud.Option) (int64, error) {
	o := crud.Build(opts...)
	q, args, err := crud.NewSQL(r.d, r.meta).Raw(r.deleteFrom).Where(r.scoped(o)).Done()
	if err != nil {
		return 0, err
	}
	res, err := r.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected, nil
}

// ---------------------------------------------------------------------------
// scanning

func (r *repository[M, ID, U]) query(ctx context.Context, q string, args []any, sizeHint int) ([]M, error) {
	return r.queryCols(ctx, q, args, sizeHint, nil)
}

func (r *repository[M, ID, U]) queryCols(ctx context.Context, q string, args []any, sizeHint int, cols []*crud.Field) ([]M, error) {
	if cols == nil {
		cols = r.meta.Fields
	}
	rows, err := r.exec(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if sizeHint <= 0 || sizeHint > 512 {
		sizeHint = 16
	}
	out := make([]M, 0, sizeHint)

	// One destination slice pointing into a single scratch model; each row is
	// copied out on append, so the slice is built once per query, not per row.
	var scratch, blank M
	dest, err := r.meta.Pointers(&scratch, cols)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		scratch = blank
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, scratch)
	}
	return out, rows.Err()
}

func (r *repository[M, ID, U]) scanOne(rows crud.Rows, m *M) (bool, error) {
	defer rows.Close()
	if !rows.Next() {
		return false, rows.Err()
	}
	dest, err := r.meta.Pointers(m, r.meta.Fields)
	if err != nil {
		return false, err
	}
	if err := rows.Scan(dest...); err != nil {
		return false, err
	}
	return true, rows.Err()
}

func (r *repository[M, ID, U]) scalar(ctx context.Context, q string, args []any) (int64, error) {
	rows, err := r.exec(ctx).Query(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, rows.Err()
	}
	var n int64
	if err := rows.Scan(&n); err != nil {
		return 0, err
	}
	return n, rows.Err()
}

var _ crud.Core[struct{}, int] = (*repository[struct{}, int, struct{}])(nil)
