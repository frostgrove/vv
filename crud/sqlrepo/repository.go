package sqlrepo

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/frostgrove/vv/crud"
)

// repository is the SQL implementation of crud.Core.
type repository[M any, ID comparable, U any] struct {
	source crud.Source
	// replica serves the reads that may be served stale; nil when the source
	// offered none.
	replica crud.Source
	bp      *Blueprint[M, ID, U]
	meta    *crud.Meta
	d       crud.Dialect

	// Static statement fragments, assembled once at Bind time.
	selectFrom string // SELECT c1, c2 FROM "t"
	countFrom  string // SELECT count(*) FROM "t"
	deleteFrom string // DELETE FROM "t"
	returning  string // " RETURNING c1, c2" or ""
	insertGen  string // INSERT with a database-generated primary key
	insertFull string // INSERT with every insertable column
	upsertTail string // conflict clause for insertFull
}

type preparedWrite struct {
	query string
	args  []any
}

func newRepository[M any, ID comparable, U any](source crud.Source, bp *Blueprint[M, ID, U]) *repository[M, ID, U] {
	r := &repository[M, ID, U]{source: source, bp: bp, meta: bp.meta, d: source.Dialect()}
	// crud.ReadSourceOf and not a type assertion: a source wrapped for
	// instrumentation is still a ReadWrite underneath, and losing the replica
	// here would send every read to the primary with nothing saying why.
	if replica, ok := crud.ReadSourceOf(source); ok {
		r.replica = replica
	}
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
func (this *repository[M, ID, U]) exec(ctx context.Context) crud.Executor {
	if e, ok := crud.ExecutorFor(ctx, this.source); ok {
		return e
	}
	return this.source
}

// read picks the datasource for a statement that only reads.
//
// Three rules, in order. An executor on the context wins outright — joining a
// transaction and then reading around it would defeat the transaction, and it is
// also how read-your-own-writes keeps working inside one. A read marked
// PrimaryOnly stays on the writable source, because its answer decides a write.
// Everything else may go to the replica, if one was declared.
func (this *repository[M, ID, U]) read(ctx context.Context, o *crud.Options) crud.Executor {
	if e, ok := crud.ExecutorFor(ctx, this.source); ok {
		return e
	}
	if this.replica != nil && (o == nil || !o.Primary) {
		return this.replica
	}
	return this.source
}

func (this *repository[M, ID, U]) Meta() *crud.Meta { return this.meta }

// Source hands back the datasource this repository was bound to, satisfying
// crud.Sourced. The probe needs it to resolve its executor through
// crud.ExecutorFor, which is what makes "never probe on another connection"
// enforceable rather than aspirational ([[D-009]]).
func (this *repository[M, ID, U]) Source() crud.Source { return this.source }

// relScopes folds the narrowings this query carries into the repository's own
// permanent ones. The blueprint's are a property of the table; a query's arrive
// from a decorator whose answer depends on who is asking, and the two AND.
func (this *repository[M, ID, U]) relScopes(o *crud.Options) *crud.RelationScopes {
	if o == nil || o.RelScopes.Empty() {
		return this.bp.relScopes
	}
	return crud.MergeRelationScopes(this.bp.relScopes, o.RelScopes)
}

func (this *repository[M, ID, U]) Tx(ctx context.Context, fn func(context.Context) error) error {
	return crud.InTx(ctx, this.source, fn)
}

// executePrepared runs a preflighted write plan. One statement is atomic on
// its own. More than one joins the caller's transaction when present and opens
// one otherwise, so a later refusal cannot leave earlier chunks committed.
func (this *repository[M, ID, U]) executePrepared(ctx context.Context, plan []preparedWrite) (int64, error) {
	if len(plan) == 0 {
		return 0, nil
	}
	var affected int64
	run := func(tx context.Context) error {
		for _, statement := range plan {
			response, err := this.exec(tx).Exec(tx, statement.query, statement.args...)
			if err != nil {
				return err
			}
			if response.RowsAffected > 0 && affected > math.MaxInt64-response.RowsAffected {
				return &crud.SchemaError{Model: this.meta.Name, Reason: "rows-affected total exceeds int64"}
			}
			affected += response.RowsAffected
		}
		return nil
	}
	if len(plan) == 1 {
		if err := run(ctx); err != nil {
			return 0, err
		}
		return affected, nil
	}
	if err := crud.InAtomic(ctx, this.source, run); err != nil {
		return 0, err
	}
	return affected, nil
}

// ---------------------------------------------------------------------------
// reads

func (this *repository[M, ID, U]) GetByID(ctx context.Context, id ID, options ...crud.Option) (M, error) {
	var zero M
	o := crud.Build(append([]crud.Option{
		crud.Where(crud.Eq(this.meta.PK.Name, id)), crud.Limit(1), crud.Unsorted(),
	}, options...)...)
	items, _, err := this.find(ctx, o, 1, 0)
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, crud.ErrNotFound
	}
	return items[0], nil
}

func (this *repository[M, ID, U]) Get(ctx context.Context, options ...crud.Option) (crud.PaginatedResponse[M], error) {
	o := crud.Build(options...)
	limit, offset, page := o.Resolved(this.bp.set.defaultLimit, this.bp.set.maxLimit)

	// A cursor replaces the offset rather than adding to it. Honouring both
	// would skip a page's worth of rows past the cursor, which is nobody's
	// intent, and the page number a cursor walk carries is meaningless anyway.
	_, back, cursoring := o.Cursor()
	if cursoring {
		offset, page = 0, 0
	}

	// Without a COUNT we still want an honest HasNext, so we fetch one row past
	// the page and throw it away.
	probe := limit
	if o.NoTotal && limit > 0 && limit < math.MaxInt {
		probe = limit + 1
	}
	items, sort, err := this.find(ctx, o, probe, offset)
	if err != nil {
		return crud.PaginatedResponse[M]{}, err
	}

	if o.NoTotal && limit > 0 {
		more := len(items) > limit
		if more {
			// Reading backwards, the extra row is the one furthest from the
			// cursor, and find has already turned the page over — so it is at
			// the front, not the back.
			if back {
				items = items[len(items)-limit:]
			} else {
				items = items[:limit]
			}
		}
		response := crud.NewPaginatedResponse(items, page, limit, int64(len(items)))
		response.TotalPages = 0
		response.HasNext = more
		if cursoring {
			// One end is known by construction: a cursor was handed in, so
			// there is something on that side of it.
			if back {
				response.HasPrev, response.HasNext = more, true
			} else {
				response.HasPrev = true
			}
		}
		this.setCursors(&response, sort)
		return response, nil
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
		if total, err = this.Count(ctx, crud.With(o)); err != nil {
			return crud.PaginatedResponse[M]{}, err
		}
	}
	response := crud.NewPaginatedResponse(items, page, limit, total)
	this.setCursors(&response, sort)
	return response, nil
}

// setCursors stamps the edges that lead to real neighbouring pages, so a client
// can leave offsets behind whenever it wants to rather than having to choose up
// front. An edge without a neighbour would only cause an empty request, so it
// must not be handed out.
//
// They are emitted only when the sort is unique — the same condition paging by
// cursor needs — because a cursor over a sort that ties names more than one
// place in the result, and handing one out would be handing out a bug.
func (this *repository[M, ID, U]) setCursors(response *crud.PaginatedResponse[M], sort []crud.Order) {
	if len(response.Items) == 0 || len(sort) == 0 {
		return
	}
	fields := make([]*crud.Field, len(sort))
	names := make([]string, len(sort))
	unique := false
	for i, s := range sort {
		f := this.meta.Field(s.Field)
		if f == nil {
			return // a sort through a relation has no value on the row itself
		}
		if !crud.CursorFieldSupported(f) {
			return // do not issue a token CursorPredicate must refuse
		}
		fields[i], names[i] = f, f.Name
		unique = unique || f.PK
	}
	if !unique {
		return
	}
	edge := func(m *M) string {
		vals, err := this.meta.Values(m, fields)
		if err != nil {
			return ""
		}
		c, err := crud.EncodeCursor(names, vals)
		if err != nil {
			return ""
		}
		return c
	}
	if response.HasPrev {
		response.PrevCursor = edge(&response.Items[0])
	}
	if response.HasNext {
		response.NextCursor = edge(&response.Items[len(response.Items)-1])
	}
}

func (this *repository[M, ID, U]) GetAll(ctx context.Context, options ...crud.Option) ([]M, error) {
	o := crud.Build(options...)
	if o.Limit == 0 && o.Page == 0 && o.Offset == 0 && !o.Unpaged {
		// GetAll's contract is every matching row, and MaxLimit is a cap on a
		// *page*. Silently truncating here would be worse than a slow query:
		// the decorators that read a whole set in order to check it would
		// check the first n and let the rest through.
		items, _, err := this.find(ctx, o, 0, 0)
		return items, err
	}
	limit, offset, _ := o.Resolved(this.bp.set.defaultLimit, this.bp.set.maxLimit)
	items, _, err := this.find(ctx, o, limit, offset)
	return items, err
}

// First is Get narrowed to one row. Filters, sort, projection, preloads and
// locking still apply; only pagination is owned by this operation.
func (this *repository[M, ID, U]) First(ctx context.Context, options ...crud.Option) (M, error) {
	var zero M
	o := crud.Build(options...)
	o.Page, o.Limit, o.Offset = 0, 1, 0
	o.After, o.Before = "", ""
	o.Unpaged, o.NoTotal = false, true
	items, _, err := this.find(ctx, o, 1, 0)
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, crud.ErrNotFound
	}
	return items[0], nil
}

// scoped folds the repository's permanent scope into a caller's predicate.
func (this *repository[M, ID, U]) scoped(o *crud.Options) crud.Predicate {
	if this.bp.set.scope == nil {
		return o.Predicate()
	}
	return crud.And(this.bp.set.scope, o.Predicate())
}

func (this *repository[M, ID, U]) find(ctx context.Context, o *crud.Options, limit, offset int) ([]M, []crud.Order, error) {
	cols, err := this.projection(o)
	if err != nil {
		return nil, nil, err
	}

	// Every projection but a DISTINCT one carries the primary key, and the two
	// things that need it need it for the same reason: a preload attaches its
	// rows by key, and the stable-pagination tiebreaker breaks ties by key. A
	// DISTINCT that selects the key back is fine — the caller has then chosen a
	// unique column, and with it a result that cannot collapse.
	identified := hasPK(cols)
	if !identified && len(o.Preloads) > 0 {
		return nil, nil, &crud.SchemaError{Model: this.meta.Name, Field: o.Preloads[0].Path,
			Reason: "a DISTINCT projection carries no primary key, so a preload has no rows to attach to"}
	}
	sort := this.sortOf(o, limit > 0 && identified)

	// The cursor's comparison is built from the sort that actually ran, which is
	// only settled here — the tiebreaker the sort picks up is part of what the
	// cursor compares.

	b := crud.NewSQL(this.d, this.meta).RelationScopes(this.relScopes(o))
	switch {
	case o.Distinct:
		// Distinct arrives on its own as often as not — `?distinct=1` with no
		// select — and the prebuilt SELECT cannot carry the keyword, so the
		// column list has to be spelled out here or the statement reads
		// "SELECT DISTINCT FROM users", which no database accepts.
		if cols == nil {
			cols = this.meta.Fields
		}
		if sort, err = this.distinctSort(cols, sort, len(o.Sort) > 0); err != nil {
			return nil, nil, err
		}
		b.Raw("SELECT DISTINCT ").Columns(cols).Raw(" FROM ").Table()
	case cols == nil:
		b.Raw(this.selectFrom)
	default:
		b.Raw("SELECT ").Columns(cols).Raw(" FROM ").Table()
	}
	// The cursor is built here rather than above because the sort is only final
	// now: a DISTINCT read rewrites it, and the comparison has to match the
	// ORDER BY that actually runs or it names a different place in the result.
	where := this.scoped(o)
	order := sort
	if token, back, ok := o.Cursor(); ok {
		step, err := this.cursorWhere(sort, token, back)
		if err != nil {
			return nil, nil, err
		}
		where = crud.And(where, step)
		if back {
			// Paging backwards asks for the n rows *immediately* before the
			// cursor. Read in the sort's own direction those are the first n of
			// everything before it — the far end of the list. So the statement
			// runs inverted and find turns the page back over.
			order = invertSort(sort)
		}
	}

	b.Where(where).OrderBy(order).LimitOffset(limit, offset)
	if o.ForUpdate {
		b.Raw(this.d.LockClause())
	}
	q, args, err := b.Done()
	if err != nil {
		return nil, nil, err
	}
	items, err := this.queryCols(ctx, this.read(ctx, o), q, args, limit, cols)
	if err != nil {
		return nil, nil, err
	}
	if _, back, ok := o.Cursor(); ok && back {
		slices.Reverse(items)
	}
	if err := this.preload(ctx, items, o); err != nil {
		return nil, nil, err
	}
	return items, sort, nil
}

// invertSort flips every term, which turns "the rows before this one, read
// forwards" into "the rows after it, read backwards" — the same set, in the
// order that lets LIMIT take the ones nearest the cursor.
func invertSort(sort []crud.Order) []crud.Order {
	out := make([]crud.Order, len(sort))
	for i, o := range sort {
		o.Desc = !o.Desc
		if o.NullsSet {
			o.NullsLast = !o.NullsLast
		}
		out[i] = o
	}
	return out
}

// cursorWhere turns the caller's cursor into the row comparison that selects
// what comes after (or before) it.
//
// The sort has to be unique or "after this row" names more than one place in the
// result: rows sharing the boundary value are skipped or repeated depending on
// which side of the tie the engine put them. A paged read appends the primary
// key for exactly that reason, so the check is whether the key survived —
// sqlrepo.UnstablePagination removes it, and with it the ability to page by
// cursor.
func (this *repository[M, ID, U]) cursorWhere(sort []crud.Order, token string, back bool) (crud.Predicate, error) {
	unique := false
	for _, s := range sort {
		if f := this.meta.Field(s.Field); f != nil && f.PK {
			unique = true
			break
		}
	}
	if !unique {
		return nil, &crud.SchemaError{Model: this.meta.Name, Field: "cursor",
			Reason: "paging by cursor needs a sort that ends in the primary key"}
	}
	return crud.CursorPredicate(this.meta, sort, token, back)
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
func (this *repository[M, ID, U]) projection(o *crud.Options) ([]*crud.Field, error) {
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
			f := this.meta.Field(name)
			if f == nil {
				return nil, &crud.UnknownFieldError{Model: this.meta.Name, Field: name}
			}
			add(f)
		}
		return out, nil
	}
	add(this.meta.PK)
	for _, name := range o.Fields {
		f := this.meta.Field(name)
		if f == nil {
			return nil, &crud.UnknownFieldError{Model: this.meta.Name, Field: name}
		}
		add(f)
	}
	// Anything a preload joins on has to come back too.
	for _, spec := range o.Preloads {
		seg, _, _ := strings.Cut(spec.Path, ".")
		if rel := this.meta.Relation(seg); rel != nil {
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
func (this *repository[M, ID, U]) distinctSort(cols []*crud.Field, sort []crud.Order, asked bool) ([]crud.Order, error) {
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
			return nil, &crud.SchemaError{Model: this.meta.Name, Field: s.Field,
				Reason: "a DISTINCT query cannot be sorted through a relation"}
		}
		f := this.meta.Field(s.Field)
		if f == nil {
			return nil, &crud.UnknownFieldError{Model: this.meta.Name, Field: s.Field}
		}
		if projected[f.Name] {
			out = append(out, s)
			continue
		}
		if asked {
			return nil, &crud.SchemaError{Model: this.meta.Name, Field: s.Field,
				Reason: "a DISTINCT projection cannot be sorted by a column it does not select"}
		}
	}
	return out, nil
}

// preload runs the requested relation loads against the same executor, so a
// preload inside a transaction sees the transaction.
func (this *repository[M, ID, U]) preload(ctx context.Context, items []M, o *crud.Options) error {
	if len(o.Preloads) == 0 || len(items) == 0 {
		return nil
	}
	return crud.RunPreloads(ctx, this.read(ctx, o), this.d, this.meta, items, o.Preloads, this.bp.set.preloadDepth, this.relScopes(o))
}

// sortOf resolves the sort terms, appending a primary-key tiebreaker so that
// paginated results are stable across pages.
func (this *repository[M, ID, U]) sortOf(o *crud.Options, paged bool) []crud.Order {
	if o.NoSort {
		return nil
	}
	sort := o.Sort
	if len(sort) == 0 {
		sort = this.bp.set.defaultSort
	}
	if !paged || !this.bp.set.stableSort {
		return sort
	}
	for _, s := range sort {
		if f := this.meta.Field(s.Field); f != nil && f.PK {
			return sort
		}
	}
	return append(append([]crud.Order{}, sort...), crud.Asc(this.meta.PK.Name))
}

func (this *repository[M, ID, U]) Count(ctx context.Context, options ...crud.Option) (int64, error) {
	o := crud.Build(options...)
	b := crud.NewSQL(this.d, this.meta).RelationScopes(this.relScopes(o))
	if o.Distinct {
		// count(*) would count the rows the SELECT DISTINCT is about to
		// collapse, and Get would then hand the client a total — and a page
		// count — for pages that do not exist. count(DISTINCT a, b) is MySQL's
		// spelling only, so the portable one is a derived table, which MySQL in
		// turn insists on being able to name.
		cols, err := this.projection(o)
		if err != nil {
			return 0, err
		}
		if cols == nil {
			cols = this.meta.Fields
		}
		b.Raw("SELECT count(*) FROM (SELECT DISTINCT ").Columns(cols).Raw(" FROM ").Table().
			Where(this.scoped(o)).Raw(") AS vv_distinct")
	} else {
		b.Raw(this.countFrom).Where(this.scoped(o))
	}
	q, args, err := b.Done()
	if err != nil {
		return 0, err
	}
	return this.scalar(ctx, this.read(ctx, o), q, args)
}

func (this *repository[M, ID, U]) Exists(ctx context.Context, options ...crud.Option) (bool, error) {
	o := crud.Build(options...)
	b := crud.NewSQL(this.d, this.meta).RelationScopes(this.relScopes(o)).
		Raw("SELECT 1 FROM ").Table().Where(this.scoped(o)).Raw(" LIMIT 1")
	q, args, err := b.Done()
	if err != nil {
		return false, err
	}
	rows, err := this.read(ctx, o).Query(ctx, q, args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	if rows.Next() {
		return true, rows.Err()
	}
	return false, rows.Err()
}

// ExistsUnscoped is the storage capability security uses to distinguish an
// unused client-owned key from a row hidden by this blueprint's permanent
// scope. It intentionally bypasses only the blueprint scope; it is not part of
// Core and ordinary application code cannot reach it through a Repo.
func (this *repository[M, ID, U]) ExistsUnscoped(ctx context.Context, options ...crud.Option) (bool, error) {
	o := crud.Build(options...)
	b := crud.NewSQL(this.d, this.meta).RelationScopes(this.relScopes(o)).
		Raw("SELECT 1 FROM ").Table().Where(o.Predicate()).Raw(" LIMIT 1")
	q, args, err := b.Done()
	if err != nil {
		return false, err
	}
	rows, err := this.read(ctx, o).Query(ctx, q, args...)
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

// Save writes m and returns a separately allocated representation of the row
// the database stored. It never mutates m. Dialects with RETURNING do that in
// one statement; the remaining dialects write and refresh inside one
// transaction so a replacement cannot slip into the result between them.
func (this *repository[M, ID, U]) Save(ctx context.Context, m *M) (M, error) {
	var zero M
	if this.d.SupportsReturning() {
		return this.saveReturning(ctx, m)
	}
	if _, ok := crud.ExecutorFor(ctx, this.source); ok {
		return this.saveWithoutReturning(ctx, m)
	}
	var saved M
	err := crud.InNewTx(ctx, this.source, func(tx context.Context) error {
		var err error
		saved, err = this.saveWithoutReturning(tx, m)
		return err
	})
	if err != nil {
		return zero, err
	}
	return saved, nil
}

// SaveOnly writes m without fetching the stored row afterwards. In particular,
// it never adds RETURNING and never mutates m.
func (this *repository[M, ID, U]) SaveOnly(ctx context.Context, m *M) error {
	stmt, args, _, err := this.saveStatement(m)
	if err != nil {
		return err
	}
	_, err = this.exec(ctx).Exec(ctx, stmt, args...)
	return err
}

// SaveScoped is the narrow storage primitive used by a security gate after it
// has classified an assigned-key Save. It deliberately does not use an upsert
// update branch. That branch cannot tell whether the conflicting row is the
// row that was inspected: a concurrent insert would turn an authorised Create
// into an unauthorised Update. Instead a create is INSERT-only and an update is
// a conditional UPDATE pinned to the complete inspected snapshot.
//
// It is an optional capability rather than part of crud.Core because it is not
// a general persistence verb. Security discovers it through crud.SaveScopedOf;
// callers that cannot obtain it must refuse the scoped Save rather than fall
// back to an ordinary upsert.
func (this *repository[M, ID, U]) SaveScoped(ctx context.Context, m *M, save *crud.ScopedSave[M]) error {
	// MySQL has no RETURNING. Keep its write and refresh in one transaction so a
	// replacement cannot slip between them and become the model returned for an
	// action the gate approved on a different row. PostgreSQL and SQLite receive
	// the refreshed row from the conditional statement itself.
	if !this.d.SupportsReturning() {
		// A context executor is the foreign-transaction seam. It may not expose
		// a concrete transaction type, but replacing it with a source-owned
		// transaction would split a caller's unit of work and make an outer
		// rollback leave this guarded write behind.
		if _, inTx := crud.ExecutorFor(ctx, this.source); !inTx {
			return crud.InNewTx(ctx, this.source, func(tx context.Context) error {
				return this.saveScoped(tx, m, save)
			})
		}
	}
	return this.saveScoped(ctx, m, save)
}

// SaveScopedOnly preserves the gate's atomic create-or-update decision without
// scanning a stored row. It is an internal capability, not a general CRUD
// verb; public callers reach it through security.Gate.SaveOnly.
func (this *repository[M, ID, U]) SaveScopedOnly(ctx context.Context, m *M, save *crud.ScopedSave[M]) error {
	if !this.d.SupportsReturning() {
		if _, inTx := crud.ExecutorFor(ctx, this.source); !inTx {
			return crud.InNewTx(ctx, this.source, func(tx context.Context) error {
				return this.saveScopedOnly(tx, m, save)
			})
		}
	}
	return this.saveScopedOnly(ctx, m, save)
}

func (this *repository[M, ID, U]) saveScoped(ctx context.Context, m *M, save *crud.ScopedSave[M]) error {
	if m == nil {
		return &crud.SchemaError{Model: this.meta.Name, Reason: "Save called with a nil model"}
	}
	hasID, err := this.meta.HasID(m)
	if err != nil {
		return err
	}
	if !hasID {
		saved, err := this.Save(ctx, m)
		if err != nil {
			return err
		}
		*m = saved
		return nil
	}
	rs := crud.MergeRelationScopes(this.bp.relScopes, save.RelationScopes)
	if save.Previous == nil {
		return this.saveScopedCreate(ctx, m, save.Scope, rs)
	}
	return this.saveScopedUpdate(ctx, m, save.Previous, save.Scope, rs)
}

func (this *repository[M, ID, U]) saveScopedOnly(ctx context.Context, m *M, save *crud.ScopedSave[M]) error {
	if m == nil {
		return &crud.SchemaError{Model: this.meta.Name, Reason: "SaveOnly called with a nil model"}
	}
	hasID, err := this.meta.HasID(m)
	if err != nil {
		return err
	}
	if !hasID {
		return this.SaveOnly(ctx, m)
	}
	rs := crud.MergeRelationScopes(this.bp.relScopes, save.RelationScopes)
	if save.Previous == nil {
		return this.saveScopedCreateOnly(ctx, m)
	}
	return this.saveScopedUpdateOnly(ctx, m, save.Previous, save.Scope, rs)
}

// saveScopedCreate inserts exactly once. PostgreSQL and SQLite can make a
// duplicate a no-row result with DO NOTHING. MySQL executes a normal INSERT:
// its duplicate-key error is classified as crud.ErrConflict by the adapter.
// INSERT IGNORE is not safe here because it also turns invalid data into a
// warning and can persist a coerced row that normal Save would reject.
func (this *repository[M, ID, U]) saveScopedCreate(ctx context.Context, m *M, scope crud.Predicate, rs *crud.RelationScopes) error {
	values, err := this.meta.Values(m, this.meta.Insert)
	if err != nil {
		return err
	}
	b := crud.NewSQL(this.d, this.meta).Raw("INSERT INTO ").Table().Raw(" (").Columns(this.meta.Insert).Raw(") VALUES (").Binds(values).Raw(")")

	if this.d.SupportsReturning() {
		q, args, err := b.Raw(this.d.Upsert(this.meta.PK.Column, nil)).Raw(this.returning).Done()
		if err != nil {
			return err
		}
		rows, err := this.exec(ctx).Query(ctx, q, args...)
		if err != nil {
			return err
		}
		ok, err := this.scanOne(rows, m)
		if err != nil {
			return err
		}
		if !ok {
			return crud.ErrCreateRaced
		}
		return nil
	}
	if this.d.Name() != "mysql" {
		return &crud.SchemaError{Model: this.meta.Name, Reason: "dialect cannot perform a create-only scoped Save"}
	}
	q, args, err := b.Done()
	if err != nil {
		return err
	}
	response, err := this.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if response.RowsAffected == 0 {
		return crud.ErrConflict
	}
	// The transaction SaveScoped opened for no-RETURNING dialects keeps this
	// freshly inserted row locked through the refresh. Its root scope is enough
	// to preserve the policy decision, while deliberately not pinning values a
	// trigger may normalise — Save promises to hand those generated values back.
	return this.refresh(ctx, m, crud.And(this.bp.set.scope, scope), rs)
}

func (this *repository[M, ID, U]) saveScopedCreateOnly(ctx context.Context, m *M) error {
	values, err := this.meta.Values(m, this.meta.Insert)
	if err != nil {
		return err
	}
	b := crud.NewSQL(this.d, this.meta).Raw("INSERT INTO ").Table().Raw(" (").Columns(this.meta.Insert).Raw(") VALUES (").Binds(values).Raw(")")
	if this.d.SupportsReturning() {
		b.Raw(this.d.Upsert(this.meta.PK.Column, nil))
	} else if this.d.Name() != "mysql" {
		return &crud.SchemaError{Model: this.meta.Name, Reason: "dialect cannot perform a create-only scoped SaveOnly"}
	}
	q, args, err := b.Done()
	if err != nil {
		return err
	}
	response, err := this.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if response.RowsAffected == 0 {
		return crud.ErrCreateRaced
	}
	return nil
}

func (this *repository[M, ID, U]) saveScopedUpdate(ctx context.Context, m, previous *M, scope crud.Predicate, rs *crud.RelationScopes) error {
	guard, err := this.scopedSaveGuard(previous, scope)
	if err != nil {
		return err
	}
	values, err := this.meta.Values(m, this.meta.Update)
	if err != nil {
		return err
	}
	changed, err := this.scopedSaveChanged(previous, m)
	if err != nil {
		return err
	}
	if len(this.meta.Update) == 0 {
		return this.refresh(ctx, m, guard, rs)
	}

	b := crud.NewSQL(this.d, this.meta).RelationScopes(rs).Raw("UPDATE ").Table().Raw(" SET ")
	for i, f := range this.meta.Update {
		if i > 0 {
			b.Raw(", ")
		}
		b.Ident(f.Column).Raw(" = ").Bind(values[i])
	}
	b.Where(guard)
	if this.d.SupportsReturning() {
		q, args, err := b.Raw(this.returning).Done()
		if err != nil {
			return err
		}
		rows, err := this.exec(ctx).Query(ctx, q, args...)
		if err != nil {
			return err
		}
		ok, err := this.scanOne(rows, m)
		if err != nil {
			return err
		}
		if !ok {
			return crud.ErrNotFound
		}
		return nil
	}

	q, args, err := b.Done()
	if err != nil {
		return err
	}
	response, err := this.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	// A no-op UPDATE reports zero on MySQL, so only a requested value change can
	// use the count as a miss signal. The no-change case performs a guarded read
	// below; it writes no confidential state either way.
	if changed && response.RowsAffected == 0 {
		return crud.ErrNotFound
	}
	// MySQL reports zero affected rows for a no-op UPDATE. It may mean the row
	// still exactly matches the inspected snapshot, or that a concurrent writer
	// changed it first. A broad scope refresh cannot distinguish the two and
	// would hand back a row Inspect never approved, so the no-change arm keeps
	// the complete snapshot guard through the refresh as well.
	if !changed {
		return this.refresh(ctx, m, guard, rs)
	}
	return this.refresh(ctx, m, crud.And(this.bp.set.scope, scope), rs)
}

func (this *repository[M, ID, U]) saveScopedUpdateOnly(ctx context.Context, m, previous *M, scope crud.Predicate, rs *crud.RelationScopes) error {
	guard, err := this.scopedSaveGuard(previous, scope)
	if err != nil {
		return err
	}
	values, err := this.meta.Values(m, this.meta.Update)
	if err != nil {
		return err
	}
	changed, err := this.scopedSaveChanged(previous, m)
	if err != nil {
		return err
	}
	if len(this.meta.Update) == 0 {
		return this.existsScoped(ctx, guard, rs)
	}

	b := crud.NewSQL(this.d, this.meta).RelationScopes(rs).Raw("UPDATE ").Table().Raw(" SET ")
	for i, f := range this.meta.Update {
		if i > 0 {
			b.Raw(", ")
		}
		b.Ident(f.Column).Raw(" = ").Bind(values[i])
	}
	b.Where(guard)
	q, args, err := b.Done()
	if err != nil {
		return err
	}
	response, err := this.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if response.RowsAffected != 0 {
		return nil
	}
	if changed || this.d.Name() != "mysql" {
		return crud.ErrNotFound
	}
	// MySQL reports zero for a matched no-op update. Verify only existence under
	// the exact inspected guard; no model is loaded and no unapproved row can
	// become a successful write between the check and the operation.
	return this.existsScoped(ctx, guard, rs)
}

func (this *repository[M, ID, U]) existsScoped(ctx context.Context, within crud.Predicate, rs *crud.RelationScopes) error {
	b := crud.NewSQL(this.d, this.meta).RelationScopes(rs).Raw("SELECT 1 FROM ").Table().Where(within).Raw(" LIMIT 1")
	q, args, err := b.Done()
	if err != nil {
		return err
	}
	rows, err := this.exec(ctx).Query(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return crud.ErrNotFound
	}
	return rows.Err()
}

// scopedSaveGuard pins an update to every scalar field the gate inspected.
// That is stricter than a version check when a model has no version column and
// prevents a delete/reinsert or concurrent mutation from being updated under a
// decision made for an older row.
func (this *repository[M, ID, U]) scopedSaveGuard(previous *M, scope crud.Predicate) (crud.Predicate, error) {
	return this.scopedSaveFieldsGuard(previous, scope, this.meta.Fields)
}

func (this *repository[M, ID, U]) scopedSaveFieldsGuard(m *M, scope crud.Predicate, fields []*crud.Field) (crud.Predicate, error) {
	values, err := this.meta.Values(m, fields)
	if err != nil {
		return nil, err
	}
	preds := make([]crud.Predicate, 0, len(fields)+2)
	preds = append(preds, this.bp.set.scope, scope)
	for i, f := range fields {
		preds = append(preds, crud.Eq(f.Name, values[i]))
	}
	return crud.And(preds...), nil
}

func (this *repository[M, ID, U]) scopedSaveChanged(previous, next *M) (bool, error) {
	before, err := this.meta.Values(previous, this.meta.Update)
	if err != nil {
		return false, err
	}
	after, err := this.meta.Values(next, this.meta.Update)
	if err != nil {
		return false, err
	}
	for i := range before {
		if !crud.EqualValues(before[i], after[i]) {
			return true, nil
		}
	}
	return false, nil
}

// saveStatement chooses the two physical forms of Save and collects the
// caller's values once. It never writes to m, which keeps SaveOnly useful for
// callers who deliberately retain their command object after persistence.
func (this *repository[M, ID, U]) saveStatement(m *M) (string, []any, bool, error) {
	if m == nil {
		return "", nil, false, &crud.SchemaError{Model: this.meta.Name, Reason: "Save called with a nil model"}
	}
	hasID, err := this.meta.HasID(m)
	if err != nil {
		return "", nil, false, err
	}
	var stmt string
	var fields []*crud.Field
	generatedPK := !hasID && this.meta.PK.Auto
	switch {
	case generatedPK:
		stmt, fields = this.insertGen, this.meta.InsertGen
	case !hasID:
		return "", nil, false, crud.ErrMissingID
	default:
		stmt, fields = this.insertFull+this.upsertTail, this.meta.Insert
	}
	args, err := this.meta.Values(m, fields)
	if err != nil {
		return "", nil, false, err
	}
	if limit := crud.BindLimit(this.d); len(args) > limit {
		return "", nil, false, &crud.SchemaError{Model: this.meta.Name, Reason: fmt.Sprintf(
			"Save needs %d bound values, but dialect %q permits at most %d; use a narrower persistence model or a driver bulk capability",
			len(args), this.d.Name(), limit)}
	}
	return stmt, args, generatedPK, nil
}

func (this *repository[M, ID, U]) saveReturning(ctx context.Context, m *M) (M, error) {
	var zero M
	stmt, args, _, err := this.saveStatement(m)
	if err != nil {
		return zero, err
	}
	rows, err := this.exec(ctx).Query(ctx, stmt+this.returning, args...)
	if err != nil {
		return zero, err
	}
	var saved M
	ok, err := this.scanOne(rows, &saved)
	if err != nil {
		return zero, err
	}
	if !ok {
		return zero, crud.ErrNotFound
	}
	return saved, nil
}

func (this *repository[M, ID, U]) saveWithoutReturning(ctx context.Context, m *M) (M, error) {
	var zero M
	stmt, args, generatedPK, err := this.saveStatement(m)
	if err != nil {
		return zero, err
	}
	response, err := this.exec(ctx).Exec(ctx, stmt, args...)
	if err != nil {
		return zero, err
	}

	// Start from a zero model rather than a copy of m: custom scanner fields or
	// pointer fields in a shallow copy could otherwise still mutate the command
	// object while the refresh scans into the result. The refresh key stays
	// separate from that model. MySQL exposes every generated key as int64 even
	// when the declared column is uint; asking Schema.SetID to bridge those
	// numeric families would weaken its checked-assignment contract merely to
	// build the SELECT that immediately scans the real stored type.
	var saved M
	var refreshID any
	if generatedPK {
		if !response.HasLastInsertID {
			return zero, &crud.SchemaError{Model: this.meta.Name, Field: this.meta.PK.Name,
				Reason: "dialect did not return the generated primary key"}
		}
		refreshID = response.LastInsertID
	} else {
		id, err := this.meta.ID(m)
		if err != nil {
			return zero, err
		}
		refreshID = id
	}
	if err := this.refreshByID(ctx, &saved, refreshID, nil, this.bp.relScopes); err != nil {
		return zero, err
	}
	return saved, nil
}

// refresh re-reads the row identified by the model's primary key, optionally
// through a narrowing — a write that was allowed to touch only some rows must
// not read back a row it was not allowed to touch.
func (this *repository[M, ID, U]) refresh(ctx context.Context, m *M, within crud.Predicate, rs *crud.RelationScopes) error {
	id, err := this.meta.ID(m)
	if err != nil {
		return err
	}
	return this.refreshByID(ctx, m, id, within, rs)
}

// refreshByID keeps a driver-returned identity as a query value until the row
// scanner assigns the database's representation to the model. It is the narrow
// boundary needed by generated uint keys on LastInsertId dialects; ordinary
// refresh callers continue to derive the typed identity through refresh.
func (this *repository[M, ID, U]) refreshByID(ctx context.Context, m *M, id any, within crud.Predicate, rs *crud.RelationScopes) error {
	b := crud.NewSQL(this.d, this.meta).RelationScopes(rs).Raw(this.selectFrom).
		Where(crud.And(crud.Eq(this.meta.PK.Name, id), within)).Raw(" LIMIT 1")
	q, args, err := b.Done()
	if err != nil {
		return err
	}
	rows, err := this.exec(ctx).Query(ctx, q, args...)
	if err != nil {
		return err
	}
	ok, err := this.scanOne(rows, m)
	if err != nil {
		return err
	}
	if !ok {
		return crud.ErrNotFound
	}
	return nil
}

func (this *repository[M, ID, U]) Update(ctx context.Context, id ID, dataTransferObject any, options ...crud.Option) (M, error) {
	var zero M

	// The caller's narrowing goes into both halves. Only checking it on the
	// load would be check-then-act: a row that leaves the narrowing between the
	// two statements would be written anyway, and handed back to a caller who
	// was never allowed to see it.
	byID := crud.Where(crud.Eq(this.meta.PK.Name, id))
	o := crud.Build(options...)
	within := this.scoped(o)

	cur, err := this.mutationRead(ctx, byID, o)
	if err != nil {
		return zero, err
	}

	changes, err := this.bp.plan.Changes(dataTransferObject, &cur)
	if err != nil {
		return zero, err
	}
	if len(changes) == 0 {
		return cur, nil
	}

	b := crud.NewSQL(this.d, this.meta).RelationScopes(this.relScopes(o)).Raw("UPDATE ").Table().Raw(" SET ")
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
	stale, err := this.versionCheck(&cur)
	if err != nil {
		return zero, err
	}
	if stale != nil {
		b.Raw(", ").Ident(this.meta.Version.Column).Raw(" = ").Ident(this.meta.Version.Column).Raw(" + 1")
	}
	b.Where(crud.And(crud.Eq(this.meta.PK.Name, id), within, stale))

	if this.d.SupportsReturning() {
		q, args, err := b.Raw(this.returning).Done()
		if err != nil {
			return zero, err
		}
		rows, err := this.exec(ctx).Query(ctx, q, args...)
		if err != nil {
			return zero, err
		}
		ok, err := this.scanOne(rows, &cur)
		if err != nil {
			return zero, err
		}
		if !ok {
			return zero, this.missedRow(ctx, id, within, stale != nil)
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
	response, err := this.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return zero, err
	}
	// With a version column the count is trustworthy after all: every matching
	// row is changed, because the counter is always one of the changes. So zero
	// here means the row we read is not the row that is there now — and a
	// re-read would otherwise hand the caller somebody else's write as if it
	// were their own.
	if stale != nil && response.RowsAffected == 0 {
		return zero, this.missedRow(ctx, id, within, true)
	}
	if err := this.refresh(ctx, &cur, within, this.relScopes(o)); err != nil {
		return zero, err
	}
	return cur, nil
}

// mutationRead builds the deliberately small read shape that may decide a
// write. A caller's predicate and relation narrowings are security boundaries,
// so they survive. Projection, ordering, cursors, pagination, aggregation and
// preloads describe a response and must not influence the row used for a diff.
// In particular, diffing a projected zero value can turn a no-op into a write
// (or the reverse), and omitting a version column manufactures a stale miss.
func (this *repository[M, ID, U]) mutationRead(ctx context.Context, byID crud.Option, o *crud.Options) (M, error) {
	var zero M
	read := &crud.Options{
		Filter:      append([]crud.Predicate(nil), o.Filter...),
		RelScopes:   o.RelScopes,
		Primary:     true,
		ForUpdate:   o.ForUpdate,
		NoSort:      true,
		NoTotal:     true,
		PreloadRows: 0,
	}
	read.Apply(byID, crud.SelectAll())
	if _, inTx := crud.ExecutorFor(ctx, this.source); inTx {
		read.ForUpdate = true
	}

	found, _, err := this.find(ctx, read, 1, 0)
	if err != nil {
		return zero, err
	}
	if len(found) == 0 {
		return zero, crud.ErrNotFound
	}
	return found[0], nil
}

// versionCheck returns the predicate that pins the row to the version it was
// read at, or nil for a model with no version column.
func (this *repository[M, ID, U]) versionCheck(cur *M) (crud.Predicate, error) {
	f := this.meta.Version
	if f == nil {
		return nil, nil
	}
	vals, err := this.meta.Values(cur, []*crud.Field{f})
	if err != nil {
		return nil, err
	}
	return crud.Eq(f.Name, vals[0]), nil
}

// missedRow explains an UPDATE that matched nothing. Both answers are 4xx and
// they are not interchangeable: a row that is gone is ErrNotFound and the caller
// should stop, while a row that moved on is ErrStaleVersion and the caller
// should read it again and reapply.
func (this *repository[M, ID, U]) missedRow(ctx context.Context, id ID, within crud.Predicate, versioned bool) error {
	if !versioned {
		return crud.ErrNotFound
	}
	still, err := this.Exists(ctx,
		crud.Where(crud.And(crud.Eq(this.meta.PK.Name, id), within)),
		crud.PrimaryOnly(),
	)
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
func (this *repository[M, ID, U]) UpdateAll(ctx context.Context, dataTransferObject any, options ...crud.Option) (int64, error) {
	o := crud.Build(options...)
	changes, err := this.bp.plan.Writes(dataTransferObject)
	if err != nil {
		return 0, err
	}
	if len(changes) == 0 {
		// A DTO that defines nothing is a caller asking for nothing, not a
		// caller asking to rewrite the table with its own values.
		return 0, nil
	}

	b := crud.NewSQL(this.d, this.meta).RelationScopes(this.relScopes(o)).Raw("UPDATE ").Table().Raw(" SET ")
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
	if f := this.meta.Version; f != nil {
		b.Raw(", ").Ident(f.Column).Raw(" = ").Ident(f.Column).Raw(" + 1")
	}
	// The repository's own scope is not optional here either: without it a
	// filtered update would reach exactly the rows the repository exists to hide.
	q, args, err := b.Where(this.scoped(o)).Done()
	if err != nil {
		return 0, err
	}
	response, err := this.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return response.RowsAffected, nil
}

func (this *repository[M, ID, U]) Delete(ctx context.Context, ids ...ID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	plan, err := this.deletePlan(ids, nil, nil, nil)
	if err != nil {
		return 0, err
	}
	return this.executePrepared(ctx, plan)
}

// DeleteScoped is the storage half of security.Gate.Delete. Policy has already
// resolved and inspected; this method keeps that narrowing beside the SQL bind
// budget, permanent repository scope, soft-delete clock and transaction, so no
// layer has to guess how many ids fit a physical statement.
func (this *repository[M, ID, U]) DeleteScoped(ctx context.Context, deletion *crud.ScopedDelete[ID]) (int64, error) {
	if deletion == nil {
		return 0, crud.ErrBadRequest
	}
	if len(deletion.IDs) == 0 {
		return 0, nil
	}
	plan, err := this.deletePlan(deletion.IDs, deletion.Scope, deletion.RelationScopes, deletion.Snapshots)
	if err != nil {
		return 0, err
	}
	return this.executePrepared(ctx, plan)
}

// deletePlan keeps the permanent scope in every chunk and charges its values
// before deciding how many ids fit. A relation scope may itself add binds while
// the root predicate crosses a relation, so counting only the ids would still
// let the final statement cross the server limit.
func (this *repository[M, ID, U]) deletePlan(ids []ID, scope crud.Predicate, relationScopes *crud.RelationScopes, snapshots map[ID]crud.Predicate) ([]preparedWrite, error) {
	relationScopes = crud.MergeRelationScopes(this.bp.relScopes, relationScopes)
	fixedPredicate := crud.And(this.bp.set.scope, scope)
	_, fixed, err := crud.NewSQL(this.d, this.meta).RelationScopes(relationScopes).
		Predicate(fixedPredicate).Done()
	if err != nil {
		return nil, err
	}
	overhead := len(fixed)
	var deletedAt any
	if this.bp.softDelete != nil {
		deletedAt = crud.NowFunc()
		overhead++
	}
	limit := crud.BindLimit(this.d)
	if overhead >= limit {
		return nil, &crud.SchemaError{Model: this.meta.Name, Reason: fmt.Sprintf(
			"Delete needs one id bind in addition to %d scope/write binds, but dialect %q permits at most %d; reduce the repository scope or use a temporary table",
			overhead, this.d.Name(), limit)}
	}
	available := limit - overhead
	type item struct {
		id       ID
		snapshot crud.Predicate
		binds    int
	}
	items := make([]item, 0, len(ids))
	for _, id := range ids {
		entry := item{id: id, binds: 1}
		if snapshots != nil {
			var ok bool
			entry.snapshot, ok = snapshots[id]
			if !ok || entry.snapshot == nil {
				continue // no inspected row means there is nothing authorised to delete.
			}
			_, args, err := crud.NewSQL(this.d, this.meta).Predicate(entry.snapshot).Done()
			if err != nil {
				return nil, err
			}
			entry.binds += len(args)
		}
		if entry.binds > available {
			return nil, &crud.SchemaError{Model: this.meta.Name, Reason: fmt.Sprintf(
				"one inspected Delete row needs %d id/snapshot binds in addition to %d scope/write binds, but dialect %q permits at most %d; narrow the persisted model or inspection snapshot",
				entry.binds, overhead, this.d.Name(), limit)}
		}
		items = append(items, entry)
	}
	if len(items) == 0 {
		return nil, nil
	}

	plan := make([]preparedWrite, 0, (len(items)-1)/max(1, available)+1)
	for start := 0; start < len(items); {
		end, used := start, 0
		for end < len(items) && used+items[end].binds <= available {
			used += items[end].binds
			end++
		}
		chunkIDs := make([]ID, 0, end-start)
		chunkSnapshots := make([]crud.Predicate, 0, end-start)
		for _, entry := range items[start:end] {
			chunkIDs = append(chunkIDs, entry.id)
			if snapshots != nil {
				chunkSnapshots = append(chunkSnapshots, entry.snapshot)
			}
		}
		byID := crud.InAny(this.meta.PK.Name, chunkIDs)
		if len(chunkIDs) == 1 {
			byID = crud.Eq(this.meta.PK.Name, chunkIDs[0])
		}
		where := crud.And(fixedPredicate, byID)
		if snapshots != nil {
			where = crud.And(where, crud.Or(chunkSnapshots...))
		}
		b := crud.NewSQL(this.d, this.meta).RelationScopes(relationScopes)
		if this.bp.softDelete != nil {
			f := this.bp.softDelete
			b.Raw("UPDATE ").Table().Raw(" SET ").Ident(f.Column).Raw(" = ").Bind(deletedAt)
		} else {
			b.Raw(this.deleteFrom)
		}
		q, args, err := b.Where(where).Done()
		if err != nil {
			return nil, err
		}
		plan = append(plan, preparedWrite{query: q, args: args})
		start = end
	}
	return plan, nil
}

func (this *repository[M, ID, U]) DeleteAll(ctx context.Context, options ...crud.Option) (int64, error) {
	o := crud.Build(options...)
	if this.bp.softDelete != nil {
		return this.stamp(ctx, this.scoped(o), this.relScopes(o))
	}
	q, args, err := crud.NewSQL(this.d, this.meta).RelationScopes(this.relScopes(o)).
		Raw(this.deleteFrom).Where(this.scoped(o)).Done()
	if err != nil {
		return 0, err
	}
	response, err := this.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return response.RowsAffected, nil
}

// stamp is what a delete becomes when the repository soft-deletes: one UPDATE
// setting the tombstone, under exactly the narrowing the DELETE would have had.
//
// The relation narrowings are a parameter and not read from the blueprint, which
// is what "exactly" costs. Reading `r.bp.relScopes` here dropped the per-request
// ones — and those are where a security policy's `RelationScopes` arrive
// ([[D-007]]) — so two repositories differing only in `SoftDelete` produced a
// narrowed DELETE and an unnarrowed UPDATE for the same call. The unnarrowed one
// is a write, over rows the policy hides.
//
// The permanent scope already carries "not deleted" (the setting folds it in),
// so a row deleted twice is not counted twice and the number returned is what
// this call actually removed from view.
func (this *repository[M, ID, U]) stamp(ctx context.Context, where crud.Predicate, rs *crud.RelationScopes) (int64, error) {
	f := this.bp.softDelete
	b := crud.NewSQL(this.d, this.meta).RelationScopes(rs).
		Raw("UPDATE ").Table().Raw(" SET ").Ident(f.Column).Raw(" = ").Bind(crud.NowFunc()).
		Where(where)
	q, args, err := b.Done()
	if err != nil {
		return 0, err
	}
	response, err := this.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return response.RowsAffected, nil
}

// ---------------------------------------------------------------------------
// scanning

func (this *repository[M, ID, U]) query(ctx context.Context, ex crud.Executor, q string, args []any, sizeHint int) ([]M, error) {
	return this.queryCols(ctx, ex, q, args, sizeHint, nil)
}

func (this *repository[M, ID, U]) queryCols(ctx context.Context, ex crud.Executor, q string, args []any, sizeHint int, cols []*crud.Field) ([]M, error) {
	if cols == nil {
		cols = this.meta.Fields
	}
	rows, err := ex.Query(ctx, q, args...)
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
	dest, err := this.meta.Pointers(&scratch, cols)
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

func (this *repository[M, ID, U]) scanOne(rows crud.Rows, m *M) (bool, error) {
	defer rows.Close()
	if !rows.Next() {
		return false, rows.Err()
	}
	dest, err := this.meta.Pointers(m, this.meta.Fields)
	if err != nil {
		return false, err
	}
	if err := rows.Scan(dest...); err != nil {
		return false, err
	}
	return true, rows.Err()
}

func (this *repository[M, ID, U]) scalar(ctx context.Context, ex crud.Executor, q string, args []any) (int64, error) {
	rows, err := ex.Query(ctx, q, args...)
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

// Aggregate runs a summary read: the grouping columns and the aggregates, under
// exactly the narrowing every other read gets.
//
// That is the point of it existing at all. A GROUP BY written by hand runs
// outside the permanent scope, outside the relation scopes and outside whatever
// the security gate installed — so the query that counts things becomes the
// query that counts another tenant's things.
func (this *repository[M, ID, U]) Aggregate(ctx context.Context, options ...crud.Option) ([]crud.AggregateRow, error) {
	o := crud.Build(options...)
	if err := o.Agg.Validate(this.meta); err != nil {
		return nil, err
	}
	if !o.NoSort {
		if err := o.Agg.ValidateSort(this.meta, o.Sort); err != nil {
			return nil, err
		}
	}

	b := crud.NewSQL(this.d, this.meta).RelationScopes(this.relScopes(o)).Raw("SELECT ")
	o.Agg.Render(b)
	b.Raw(" FROM ").Table().Where(this.scoped(o))
	if len(o.Agg.GroupBy) > 0 {
		b.Raw(" GROUP BY ")
		for i, g := range o.Agg.GroupBy {
			if i > 0 {
				b.Raw(", ")
			}
			b.Column(g)
		}
	}
	// A sort is honoured only over the grouping columns; anything else is not a
	// column this statement has.
	if len(o.Sort) > 0 && !o.NoSort {
		b.OrderBy(o.Sort)
	}
	// A summary is not implicitly a page. With no explicit paging control every
	// group is returned; otherwise the ordinary repository cap still applies.
	// In particular Unpaged may not erase MaxLimit on this verb.
	limit, offset := 0, 0
	if o.Page != 0 || o.Limit != 0 || o.Offset != 0 || o.Unpaged {
		limit, offset, _ = o.Resolved(this.bp.set.defaultLimit, this.bp.set.maxLimit)
	}
	b.LimitOffset(limit, offset)

	q, args, err := b.Done()
	if err != nil {
		return nil, err
	}
	rows, err := this.read(ctx, o).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// The group columns resolve once, above the loop. FieldAt is a path walk —
	// a Split, an allocation and a Join per call — and it used to run per group
	// column *per row*, so a thousand-row aggregate over three groupings did
	// three thousand walks to answer a question the statement had already
	// settled. It also belongs here for a second reason: a path that does not
	// resolve is a request-level failure, and finding that out on row 900 rather
	// than before the scan is the wrong shape of error.
	groups := make([]*crud.Field, len(o.Agg.GroupBy))
	for i, g := range o.Agg.GroupBy {
		f, _, err := this.meta.FieldAt(g)
		if err != nil {
			return nil, err
		}
		groups[i] = f
	}

	width := len(o.Agg.GroupBy) + len(o.Agg.Aggregations)
	cells := make([]any, width)
	dest := make([]any, width)
	for i := range cells {
		dest[i] = &cells[i]
	}

	var out []crud.AggregateRow
	for rows.Next() {
		// cells is reused, so every value has to be copied out below before the
		// next Scan overwrites it — which the two maps already do.
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := crud.AggregateRow{
			Group: make(map[string]any, len(o.Agg.GroupBy)),
			Value: make(map[string]any, len(o.Agg.Aggregations)),
		}
		for i, f := range groups {
			row.Group[f.Name] = cells[i]
		}
		for i, ag := range o.Agg.Aggregations {
			row.Value[ag.As] = cells[len(o.Agg.GroupBy)+i]
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// SaveAll writes many rows in the fewest statements the dialect permits.
//
// It is Save's batched partner and it keeps Save's fork: every row must agree
// about whether the database generates the key, because the two forms are two
// different statements. A mixed batch is refused rather than split, so the call
// cannot silently change an assigned-key upsert into a generated-key insert.
//
// It is deliberately write-only, like SaveOnly: callers that need individual
// stored rows can call Save for each command and keep the results explicitly.
func (this *repository[M, ID, U]) SaveAll(ctx context.Context, models []*M) error {
	if len(models) == 0 {
		return nil
	}
	generated := false
	for i, m := range models {
		if m == nil {
			return &crud.SchemaError{Model: this.meta.Name, Reason: "SaveAll called with a nil model"}
		}
		hasID, err := this.meta.HasID(m)
		if err != nil {
			return err
		}
		gen := !hasID && this.meta.PK.Auto
		if !hasID && !this.meta.PK.Auto {
			return crud.ErrMissingID
		}
		if i == 0 {
			generated = gen
			continue
		}
		if gen != generated {
			return &crud.SchemaError{Model: this.meta.Name, Reason: "SaveAll cannot mix rows with and without a key: " +
				"they are two different statements, and splitting the batch would hide the cost"}
		}
	}

	fields := this.meta.Insert
	tail := this.upsertTail
	if generated {
		fields, tail = this.meta.InsertGen, ""
	}

	plan, err := this.saveAllPlan(models, fields, tail)
	if err != nil {
		return err
	}
	_, err = this.executePrepared(ctx, plan)
	return err
}

// saveAllPlan performs every model read and renders every statement before the
// first one runs. A bad value in a later chunk is therefore a preflight error,
// not a partially persisted batch.
func (this *repository[M, ID, U]) saveAllPlan(models []*M, fields []*crud.Field, tail string) ([]preparedWrite, error) {
	limit := crud.BindLimit(this.d)
	width := len(fields)
	perStatement := len(models)
	if width > 0 {
		perStatement = limit / width
		if perStatement == 0 {
			return nil, &crud.SchemaError{Model: this.meta.Name, Reason: fmt.Sprintf(
				"one SaveAll row needs %d bound values, but dialect %q permits at most %d; use SaveOnly with a narrower model or a driver bulk API",
				width, this.d.Name(), limit)}
		}
	}
	chunks := (len(models)-1)/perStatement + 1
	plan := make([]preparedWrite, 0, chunks)
	for start := 0; start < len(models); start += perStatement {
		end := min(start+perStatement, len(models))
		b := crud.NewSQL(this.d, this.meta).Raw("INSERT INTO ").Table().Raw(" (")
		for i, f := range fields {
			if i > 0 {
				b.Raw(", ")
			}
			b.Ident(f.Column)
		}
		b.Raw(") VALUES ")
		for i, m := range models[start:end] {
			if i > 0 {
				b.Raw(", ")
			}
			values, err := this.meta.Values(m, fields)
			if err != nil {
				return nil, err
			}
			b.Raw("(").Binds(values).Raw(")")
		}
		q, args, err := b.Raw(tail).Done()
		if err != nil {
			return nil, err
		}
		plan = append(plan, preparedWrite{query: q, args: args})
	}
	return plan, nil
}
