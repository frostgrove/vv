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

// exec picks the foreign executor bound to ctx over the repository's own source
// — but only one this repository's database would accept. A context carrying
// somebody else's database (crud.WithExecutorFor) is not this repository's
// business and is left alone.
func (r *repository[M, ID, U]) exec(ctx context.Context) crud.Executor {
	if e, ok := crud.ExecutorFor(ctx, r.src); ok {
		return e
	}
	return r.src
}

func (r *repository[M, ID, U]) Meta() *crud.Meta { return r.meta }

// relScopes folds the narrowings this query carries into the repository's own
// permanent ones. The blueprint's are a property of the table; a query's arrive
// from a decorator whose answer depends on who is asking, and the two AND.
func (r *repository[M, ID, U]) relScopes(o *crud.Options) *crud.RelationScopes {
	if o == nil || o.RelScopes.Empty() {
		return r.bp.relScopes
	}
	return crud.MergeRelationScopes(r.bp.relScopes, o.RelScopes)
}

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
		resp := crud.NewPaginatedResponse(items, page, limit, int64(len(items)))
		resp.TotalPages = 0
		resp.HasNext = more
		return resp, nil
	}

	var total int64
	switch {
	case o.NoTotal:
		// No COUNT ran, so the only number that is true is the size of what came
		// back. Deriving it from the offset invented one the client had chosen:
		// page 999 of an empty table reported 19960 results.
		total = int64(len(items))
	case o.Unpaged:
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
	if o.Limit == 0 && o.Page == 0 && o.Offset == 0 && !o.Unpaged {
		// GetAll's contract is every matching row, and MaxLimit is a cap on a
		// *page*. Silently truncating here would be worse than a slow query:
		// the decorators that read a whole set in order to check it would
		// check the first n and let the rest through.
		return r.find(ctx, o, 0, 0)
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

	// Every projection but a DISTINCT one carries the primary key, and the two
	// things that need it need it for the same reason: a preload attaches its
	// rows by key, and the stable-pagination tiebreaker breaks ties by key. A
	// DISTINCT that selects the key back is fine — the caller has then chosen a
	// unique column, and with it a result that cannot collapse.
	identified := hasPK(cols)
	if !identified && len(o.Preloads) > 0 {
		return nil, &crud.SchemaError{Model: r.meta.Name, Field: o.Preloads[0].Path,
			Reason: "a DISTINCT projection carries no primary key, so a preload has no rows to attach to"}
	}
	sort := r.sortOf(o, limit > 0 && identified)

	b := crud.NewSQL(r.d, r.meta).RelationScopes(r.relScopes(o))
	switch {
	case o.Distinct:
		// Distinct arrives on its own as often as not — `?distinct=1` with no
		// select — and the prebuilt SELECT cannot carry the keyword, so the
		// column list has to be spelled out here or the statement reads
		// "SELECT DISTINCT FROM users", which no database accepts.
		if cols == nil {
			cols = r.meta.Fields
		}
		if sort, err = r.distinctSort(cols, sort, len(o.Sort) > 0); err != nil {
			return nil, err
		}
		b.Raw("SELECT DISTINCT ").Columns(cols).Raw(" FROM ").Table()
	case cols == nil:
		b.Raw(r.selectFrom)
	default:
		b.Raw("SELECT ").Columns(cols).Raw(" FROM ").Table()
	}
	b.Where(r.scoped(o)).OrderBy(sort).LimitOffset(limit, offset)
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

// projection resolves Select() into a field list, keeping the primary key: it
// is what preloads join on and what a client needs to address the row. A nil
// result means "every column", which lets the prebuilt statement be used.
//
// Distinct is the one exception. The primary key is unique by definition, so
// carrying it would make SELECT DISTINCT unable to remove a single row —
// `?distinct=1&select=title` returned one row per article rather than one per
// title. A caller who asks for the distinct values of some columns is asking
// for values, not for rows, and gives up addressing them.
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
	if o.Distinct {
		// Nothing is added under DISTINCT — not the key, not a preload's join
		// column — because every extra column is one more thing that can differ
		// between two rows, and a projection nobody chose defeats the keyword
		// silently. The caller gets the distinct values of what they selected.
		for _, name := range o.Fields {
			f := r.meta.Field(name)
			if f == nil {
				return nil, &crud.UnknownFieldError{Model: r.meta.Name, Field: name}
			}
			add(f)
		}
		return out, nil
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

// hasPK reports whether a projection can still identify its rows. A nil
// projection is every column, so it always can.
func hasPK(cols []*crud.Field) bool {
	if cols == nil {
		return true
	}
	for _, f := range cols {
		if f.PK {
			return true
		}
	}
	return false
}

// distinctSort reconciles a sort with a DISTINCT projection. Both engines refuse
// a SELECT DISTINCT that orders by something outside the select list (42P10 /
// ER_FIELD_IN_ORDER_NOT_SELECT), and all three inputs — distinct, select and
// sort — come from the wire, so the combination has to end in a statement or in
// a refusal the client can read, never in a 500.
//
// Widening the projection to cover the sort is what this used to do, and it
// produced a statement that ran and an answer nobody asked for: the extra column
// is exactly what tells the duplicate rows apart, so DISTINCT stopped removing
// them and the response carried a column the client had not selected. The
// caller's own sort is therefore refused, because those two requests genuinely
// cannot both be honoured; the repository's default sort, which the caller never
// asked for, is dropped instead of being turned into an error they cannot avoid.
func (r *repository[M, ID, U]) distinctSort(cols []*crud.Field, sort []crud.Order, asked bool) ([]crud.Order, error) {
	if len(sort) == 0 {
		return sort, nil
	}
	projected := make(map[string]bool, len(cols))
	for _, f := range cols {
		projected[f.Name] = true
	}
	out := make([]crud.Order, 0, len(sort))
	for _, s := range sort {
		if strings.Contains(s.Field, ".") {
			// A sort through a relation renders as a scalar subquery, which can
			// never be in the select list; there is no statement to build.
			return nil, &crud.SchemaError{Model: r.meta.Name, Field: s.Field,
				Reason: "a DISTINCT query cannot be sorted through a relation"}
		}
		f := r.meta.Field(s.Field)
		if f == nil {
			return nil, &crud.UnknownFieldError{Model: r.meta.Name, Field: s.Field}
		}
		if projected[f.Name] {
			out = append(out, s)
			continue
		}
		if asked {
			return nil, &crud.SchemaError{Model: r.meta.Name, Field: s.Field,
				Reason: "a DISTINCT projection cannot be sorted by a column it does not select"}
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
	return crud.RunPreloads(ctx, r.exec(ctx), r.d, r.meta, items, o.Preloads, r.bp.set.preloadDepth, r.relScopes(o))
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
	b := crud.NewSQL(r.d, r.meta).RelationScopes(r.relScopes(o))
	if o.Distinct {
		// count(*) would count the rows the SELECT DISTINCT is about to
		// collapse, and Get would then hand the client a total — and a page
		// count — for pages that do not exist. count(DISTINCT a, b) is MySQL's
		// spelling only, so the portable one is a derived table, which MySQL in
		// turn insists on being able to name.
		cols, err := r.projection(o)
		if err != nil {
			return 0, err
		}
		if cols == nil {
			cols = r.meta.Fields
		}
		b.Raw("SELECT count(*) FROM (SELECT DISTINCT ").Columns(cols).Raw(" FROM ").Table().
			Where(r.scoped(o)).Raw(") AS rxcrud_distinct")
	} else {
		b.Raw(r.countFrom).Where(r.scoped(o))
	}
	q, args, err := b.Done()
	if err != nil {
		return 0, err
	}
	return r.scalar(ctx, q, args)
}

func (r *repository[M, ID, U]) Exists(ctx context.Context, opts ...crud.Option) (bool, error) {
	o := crud.Build(opts...)
	b := crud.NewSQL(r.d, r.meta).RelationScopes(r.relScopes(o)).
		Raw("SELECT 1 FROM ").Table().Where(r.scoped(o)).Raw(" LIMIT 1")
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
		return r.refresh(ctx, m, nil, r.bp.relScopes)
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
	// Without RETURNING the only way to keep Save's promise — the model
	// describes the row — is to go and read it. Skipping this when the model
	// declares no `generated` column saved a round trip and cost correctness:
	// an upsert's conflict clause leaves out every immutable column, so the
	// caller was left holding values the database had just refused, and a
	// handler serialised a different document on MySQL than on PostgreSQL.
	return r.refresh(ctx, m, nil, r.bp.relScopes)
}

// refresh re-reads the row identified by the model's primary key, optionally
// through a narrowing — a write that was allowed to touch only some rows must
// not read back a row it was not allowed to touch.
func (r *repository[M, ID, U]) refresh(ctx context.Context, m *M, within crud.Predicate, rs *crud.RelationScopes) error {
	id, err := r.meta.ID(m)
	if err != nil {
		return err
	}
	b := crud.NewSQL(r.d, r.meta).RelationScopes(rs).Raw(r.selectFrom).
		Where(crud.And(crud.Eq(r.meta.PK.Name, id), within)).Raw(" LIMIT 1")
	q, args, err := b.Done()
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

func (r *repository[M, ID, U]) Update(ctx context.Context, id ID, dto any, opts ...crud.Option) (M, error) {
	var zero M

	// The caller's narrowing goes into both halves. Only checking it on the
	// load would be check-then-act: a row that leaves the narrowing between the
	// two statements would be written anyway, and handed back to a caller who
	// was never allowed to see it.
	byID := crud.Where(crud.Eq(r.meta.PK.Name, id))
	o := crud.Build(opts...)
	within := r.scoped(o)

	// Inside somebody's transaction we can lock the row we are about to diff
	// against; outside of one, locking would be pointless.
	loadOpts := append([]crud.Option{byID}, opts...)
	loadOpts = append(loadOpts, crud.Limit(1), crud.Unsorted())
	if _, inTx := crud.ExecutorFor(ctx, r.src); inTx {
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

	b := crud.NewSQL(r.d, r.meta).RelationScopes(r.relScopes(o)).Raw("UPDATE ").Table().Raw(" SET ")
	for i, ch := range changes {
		if i > 0 {
			b.Raw(", ")
		}
		b.Ident(ch.Field.Column).Raw(" = ").Bind(ch.Value)
	}
	// The optimistic lock, in the two halves it needs: the counter goes up so
	// that anyone else holding this row knows their copy is old, and the value
	// it had when we read it goes into the WHERE, so if somebody got there first
	// this statement matches nothing instead of overwriting them.
	stale, err := r.versionCheck(&cur)
	if err != nil {
		return zero, err
	}
	if stale != nil {
		b.Raw(", ").Ident(r.meta.Version.Column).Raw(" = ").Ident(r.meta.Version.Column).Raw(" + 1")
	}
	b.Where(crud.And(crud.Eq(r.meta.PK.Name, id), within, stale))

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
			return zero, r.missedRow(ctx, id, within, stale != nil)
		}
		return cur, nil
	}

	q, args, err := b.Done()
	if err != nil {
		return zero, err
	}
	// MySQL reports 0 rows affected for a write that changed nothing, so
	// rows-affected cannot tell "no such row" from "nothing to do". Re-reading
	// answers both: it is what RETURNING gives the other dialect for free, and
	// patching the loaded row in memory instead used to report success — with a
	// fabricated model — for a row deleted between the load and the write.
	res, err := r.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return zero, err
	}
	// With a version column the count is trustworthy after all: every matching
	// row is changed, because the counter is always one of the changes. So zero
	// here means the row we read is not the row that is there now — and a
	// re-read would otherwise hand the caller somebody else's write as if it
	// were their own.
	if stale != nil && res.RowsAffected == 0 {
		return zero, r.missedRow(ctx, id, within, true)
	}
	if err := r.refresh(ctx, &cur, within, r.relScopes(o)); err != nil {
		return zero, err
	}
	return cur, nil
}

// versionCheck returns the predicate that pins the row to the version it was
// read at, or nil for a model with no version column.
func (r *repository[M, ID, U]) versionCheck(cur *M) (crud.Predicate, error) {
	f := r.meta.Version
	if f == nil {
		return nil, nil
	}
	vals, err := r.meta.Values(cur, []*crud.Field{f})
	if err != nil {
		return nil, err
	}
	return crud.Eq(f.Name, vals[0]), nil
}

// missedRow explains an UPDATE that matched nothing. Both answers are 4xx and
// they are not interchangeable: a row that is gone is ErrNotFound and the caller
// should stop, while a row that moved on is ErrStaleVersion and the caller
// should read it again and reapply.
func (r *repository[M, ID, U]) missedRow(ctx context.Context, id ID, within crud.Predicate, versioned bool) error {
	if !versioned {
		return crud.ErrNotFound
	}
	still, err := r.Exists(ctx, crud.Where(crud.And(crud.Eq(r.meta.PK.Name, id), within)))
	if err != nil {
		return err
	}
	if still {
		return crud.ErrStaleVersion
	}
	return crud.ErrNotFound
}

// UpdateAll writes the DTO's defined columns to every matching row in one
// statement. There is no row to diff against — there are many — so this is the
// one write that does not skip a column whose value is already there.
func (r *repository[M, ID, U]) UpdateAll(ctx context.Context, dto any, opts ...crud.Option) (int64, error) {
	o := crud.Build(opts...)
	changes, err := r.bp.plan.Writes(dto)
	if err != nil {
		return 0, err
	}
	if len(changes) == 0 {
		// A DTO that defines nothing is a caller asking for nothing, not a
		// caller asking to rewrite the table with its own values.
		return 0, nil
	}

	b := crud.NewSQL(r.d, r.meta).RelationScopes(r.relScopes(o)).Raw("UPDATE ").Table().Raw(" SET ")
	for i, ch := range changes {
		if i > 0 {
			b.Raw(", ")
		}
		b.Ident(ch.Field.Column).Raw(" = ").Bind(ch.Value)
	}
	// There is no version to check against — a filtered write is not a
	// read-modify-write of one row — but every row it touches still has to
	// advance, or a stale Update somebody else is holding would sail past this
	// change and undo it.
	if f := r.meta.Version; f != nil {
		b.Raw(", ").Ident(f.Column).Raw(" = ").Ident(f.Column).Raw(" + 1")
	}
	// The repository's own scope is not optional here either: without it a
	// filtered update would reach exactly the rows the repository exists to hide.
	q, args, err := b.Where(r.scoped(o)).Done()
	if err != nil {
		return 0, err
	}
	res, err := r.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected, nil
}

func (r *repository[M, ID, U]) Delete(ctx context.Context, ids ...ID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	// The scope belongs in here too. Without it a row the repository refuses to
	// show is still deletable by id — GET /:id answers 404 and DELETE /:id
	// answers 200 for the same row.
	var byID crud.Predicate
	if len(ids) == 1 {
		byID = crud.Eq(r.meta.PK.Name, ids[0])
	} else {
		byID = crud.InAny(r.meta.PK.Name, ids)
	}
	b := crud.NewSQL(r.d, r.meta).RelationScopes(r.bp.relScopes).Raw(r.deleteFrom).
		Where(crud.And(r.bp.set.scope, byID))
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
	q, args, err := crud.NewSQL(r.d, r.meta).RelationScopes(r.relScopes(o)).
		Raw(r.deleteFrom).Where(r.scoped(o)).Done()
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
