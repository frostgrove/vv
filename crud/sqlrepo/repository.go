package sqlrepo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"

	"github.com/frostgrove/vv/crud"
)

type repository[M any, ID comparable, U any] struct {
	source crud.Source

	replica crud.Source
	bp      *Blueprint[M, ID, U]
	meta    *crud.Meta
	d       crud.Dialect

	selectFrom string
	countFrom  string
	deleteFrom string
	returning  string
	insertGen  string
	insertFull string
	upsertTail string
}

type preparedWrite struct {
	query string
	args  []any
}

func (this *repository[M, ID, U]) SupportsRestore() bool {
	return this != nil && this.bp != nil && this.bp.softDelete != nil
}

func newRepository[M any, ID comparable, U any](source crud.Source, bp *Blueprint[M, ID, U]) *repository[M, ID, U] {
	r := &repository[M, ID, U]{source: source, bp: bp, meta: bp.meta, d: source.Dialect()}

	if replica, ok := crud.ReadSourceOf(source); ok {
		r.replica = replica
	}
	m, d := r.meta, r.d

	cols := joinColumns(d, m.Fields)
	table := m.QuotedTable(d)

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

func (this *repository[M, ID, U]) exec(ctx context.Context) crud.Executor {
	if e, ok := crud.ExecutorFor(ctx, this.source); ok {
		return e
	}
	return this.source
}

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

func (this *repository[M, ID, U]) Source() crud.Source { return this.source }

func (this *repository[M, ID, U]) relScopes(o *crud.Options) *crud.RelationScopes {
	if o == nil || o.RelScopes.Empty() {
		return this.bp.relScopes
	}
	return crud.MergeRelationScopes(this.bp.relScopes, o.RelScopes)
}

func (this *repository[M, ID, U]) Tx(ctx context.Context, fn func(context.Context) error) error {
	return crud.InTx(ctx, this.source, fn)
}

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

// Get uses one extra row for an honest NoTotal result and replaces offset with
// a cursor boundary. Combining the two would skip rows past that boundary.
func (this *repository[M, ID, U]) Get(ctx context.Context, options ...crud.Option) (crud.PaginatedResponse[M], error) {
	o := crud.Build(options...)
	limit, offset, page := o.Resolved(this.bp.set.defaultLimit, this.bp.set.maxLimit)

	_, back, cursoring := o.Cursor()
	if cursoring {
		offset, page = 0, 0
	}

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
		total = int64(len(items))
	case o.Unpaged:
		total = int64(offset + len(items))
	case offset == 0 && len(items) < limit:
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
			return
		}
		if !crud.CursorFieldSupported(f) {
			return
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
		items, _, err := this.find(ctx, o, 0, 0)
		return items, err
	}
	limit, offset, _ := o.Resolved(this.bp.set.defaultLimit, this.bp.set.maxLimit)
	items, _, err := this.find(ctx, o, limit, offset)
	return items, err
}

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

func (this *repository[M, ID, U]) scoped(o *crud.Options) crud.Predicate {
	if this.bp.set.scope == nil {
		return o.Predicate()
	}
	return crud.And(this.bp.set.scope, o.Predicate())
}

func (this *repository[M, ID, U]) find(ctx context.Context, o *crud.Options, limit, offset int) ([]M, []crud.Order, error) {
	return this.findWithin(ctx, o, limit, offset, this.scoped(o))
}

func (this *repository[M, ID, U]) findWithin(ctx context.Context, o *crud.Options, limit, offset int, within crud.Predicate) ([]M, []crud.Order, error) {
	cols, err := this.projection(o)
	if err != nil {
		return nil, nil, err
	}

	identified := hasPK(cols)
	if !identified && len(o.Preloads) > 0 {
		return nil, nil, &crud.SchemaError{Model: this.meta.Name, Field: o.Preloads[0].Path,
			Reason: "a DISTINCT projection carries no primary key, so a preload has no rows to attach to"}
	}
	sort := this.sortOf(o, limit > 0 && identified)

	b := crud.NewSQL(this.d, this.meta).RelationScopes(this.relScopes(o))
	switch {
	case o.Distinct:
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

	where := within
	order := sort
	if token, back, ok := o.Cursor(); ok {
		step, err := this.cursorWhere(sort, token, back)
		if err != nil {
			return nil, nil, err
		}
		where = crud.And(where, step)
		if back {
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

func (this *repository[M, ID, U]) preload(ctx context.Context, items []M, o *crud.Options) error {
	if len(o.Preloads) == 0 {
		return nil
	}
	specs := o.Preloads
	if o.PreloadRows != 0 {
		if o.PreloadRows < 0 {
			return &crud.SchemaError{Model: this.meta.Name, Reason: "a preload row cap cannot be negative"}
		}
		specs = slices.Clone(specs)
		for i := range specs {
			if specs[i].MaxRows == 0 || o.PreloadRows < specs[i].MaxRows {
				specs[i].MaxRows = o.PreloadRows
			}
		}
	}
	return crud.RunPreloads(ctx, this.read(ctx, o), this.d, this.meta, items, specs, this.bp.set.preloadDepth, this.relScopes(o))
}

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

func (this *repository[M, ID, U]) SaveOnly(ctx context.Context, m *M) error {
	stmt, args, _, err := this.saveStatement(m)
	if err != nil {
		return err
	}
	_, err = this.exec(ctx).Exec(ctx, stmt, args...)
	return err
}

func (this *repository[M, ID, U]) SaveScoped(ctx context.Context, m *M, save *crud.ScopedSave[M]) error {
	if !this.d.SupportsReturning() {
		if _, inTx := crud.ExecutorFor(ctx, this.source); !inTx {
			return crud.InNewTx(ctx, this.source, func(tx context.Context) error {
				return this.saveScoped(tx, m, save)
			})
		}
	}
	return this.saveScoped(ctx, m, save)
}

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

	if changed && response.RowsAffected == 0 {
		return crud.ErrNotFound
	}

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

func (this *repository[M, ID, U]) refresh(ctx context.Context, m *M, within crud.Predicate, rs *crud.RelationScopes) error {
	id, err := this.meta.ID(m)
	if err != nil {
		return err
	}
	return this.refreshByID(ctx, m, id, within, rs)
}

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

	response, err := this.exec(ctx).Exec(ctx, q, args...)
	if err != nil {
		return zero, err
	}

	if stale != nil && response.RowsAffected == 0 {
		return zero, this.missedRow(ctx, id, within, true)
	}
	if err := this.refresh(ctx, &cur, within, this.relScopes(o)); err != nil {
		return zero, err
	}
	return cur, nil
}

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

func (this *repository[M, ID, U]) UpdateAll(ctx context.Context, dataTransferObject any, options ...crud.Option) (int64, error) {
	o := crud.Build(options...)
	changes, err := this.bp.plan.Writes(dataTransferObject)
	if err != nil {
		return 0, err
	}
	if len(changes) == 0 {
		return 0, nil
	}

	b := crud.NewSQL(this.d, this.meta).RelationScopes(this.relScopes(o)).Raw("UPDATE ").Table().Raw(" SET ")
	for i, ch := range changes {
		if i > 0 {
			b.Raw(", ")
		}
		b.Ident(ch.Field.Column).Raw(" = ").Bind(ch.Value)
	}

	if f := this.meta.Version; f != nil {
		b.Raw(", ").Ident(f.Column).Raw(" = ").Ident(f.Column).Raw(" + 1")
	}

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

func (this *repository[M, ID, U]) Restore(ctx context.Context, ids ...ID) (int64, error) {
	if this.bp.softDelete == nil {
		return 0, crud.ErrNoTombstone
	}
	if len(ids) == 0 {
		return 0, nil
	}
	plan, err := this.restorePlan(ids, nil, nil, nil)
	if err != nil {
		return 0, err
	}
	return this.executePrepared(ctx, plan)
}

func (this *repository[M, ID, U]) RestoreScoped(ctx context.Context, restore *crud.ScopedRestore[ID]) (int64, error) {
	if this.bp.softDelete == nil {
		return 0, crud.ErrNoTombstone
	}
	if restore == nil {
		return 0, crud.ErrBadRequest
	}
	if len(restore.IDs) == 0 {
		return 0, nil
	}
	plan, err := this.restorePlan(restore.IDs, restore.Scope, restore.RelationScopes, restore.Snapshots)
	if err != nil {
		return 0, err
	}
	return this.executePrepared(ctx, plan)
}

func (this *repository[M, ID, U]) LoadTombstones(ctx context.Context, ids []ID, scope crud.Predicate, relations *crud.RelationScopes) ([]M, error) {
	if this.bp.softDelete == nil {
		return nil, crud.ErrNoTombstone
	}
	if len(ids) == 0 {
		return nil, nil
	}
	ids = uniqueIDs(ids)
	effectiveRelations := crud.MergeRelationScopes(this.bp.relScopes, relations)
	fixed := crud.And(this.bp.restoreScope, crud.IsNotNull(this.bp.softDelete.Name), scope)
	_, args, err := crud.NewSQL(this.d, this.meta).RelationScopes(effectiveRelations).Predicate(fixed).Done()
	if err != nil {
		return nil, err
	}
	available := crud.BindLimit(this.d) - len(args)
	if available < 1 {
		return nil, &crud.SchemaError{Model: this.meta.Name, Reason: fmt.Sprintf(
			"Restore inspection needs one id bind in addition to %d scope binds, but dialect %q permits at most %d; reduce the repository scope",
			len(args), this.d.Name(), crud.BindLimit(this.d))}
	}

	var out []M
	for start := 0; start < len(ids); start += available {
		part := ids[start:min(start+available, len(ids))]
		byID := crud.InAny(this.meta.PK.Name, part)
		if len(part) == 1 {
			byID = crud.Eq(this.meta.PK.Name, part[0])
		}
		o := crud.Build(crud.PrimaryOnly(), crud.Unsorted(), crud.NarrowRelations(relations))
		rows, _, err := this.findWithin(ctx, o, 0, 0, crud.And(fixed, byID))
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func (this *repository[M, ID, U]) restorePlan(ids []ID, scope crud.Predicate, relationScopes *crud.RelationScopes, snapshots map[ID]crud.Predicate) ([]preparedWrite, error) {
	ids = uniqueIDs(ids)
	relationScopes = crud.MergeRelationScopes(this.bp.relScopes, relationScopes)
	fixedPredicate := crud.And(this.bp.restoreScope, crud.IsNotNull(this.bp.softDelete.Name), scope)
	_, fixed, err := crud.NewSQL(this.d, this.meta).RelationScopes(relationScopes).Predicate(fixedPredicate).Done()
	if err != nil {
		return nil, err
	}
	overhead := len(fixed)
	limit := crud.BindLimit(this.d)
	if overhead >= limit {
		return nil, &crud.SchemaError{Model: this.meta.Name, Reason: fmt.Sprintf(
			"Restore needs one id bind in addition to %d scope binds, but dialect %q permits at most %d; reduce the repository scope or use a temporary table",
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
			rv := reflect.ValueOf(id)
			if rv.IsValid() && !rv.Comparable() {
				continue
			}
			var ok bool
			entry.snapshot, ok = snapshots[id]
			if !ok || entry.snapshot == nil {
				continue
			}
			_, args, err := crud.NewSQL(this.d, this.meta).Predicate(entry.snapshot).Done()
			if err != nil {
				return nil, err
			}
			entry.binds += len(args)
		}
		if entry.binds > available {
			return nil, &crud.SchemaError{Model: this.meta.Name, Reason: fmt.Sprintf(
				"one inspected Restore row needs %d id/snapshot binds in addition to %d scope binds, but dialect %q permits at most %d; narrow the persisted model or inspection snapshot",
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
		f := this.bp.softDelete
		b := crud.NewSQL(this.d, this.meta).RelationScopes(relationScopes).
			Raw("UPDATE ").Table().Raw(" SET ").Ident(f.Column).Raw(" = NULL")

		if version := this.meta.Version; version != nil {
			b.Raw(", ").Ident(version.Column).Raw(" = ").Ident(version.Column).Raw(" + 1")
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

func uniqueIDs[ID comparable](ids []ID) []ID {
	if len(ids) < 2 {
		return ids
	}
	seen := make(map[ID]struct{}, len(ids))
	out := make([]ID, 0, len(ids))
	for _, id := range ids {
		rv := reflect.ValueOf(id)
		if rv.IsValid() && !rv.Comparable() {
			out = append(out, id)
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

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
			rv := reflect.ValueOf(id)
			if rv.IsValid() && !rv.Comparable() {
				continue
			}
			var ok bool
			entry.snapshot, ok = snapshots[id]
			if !ok || entry.snapshot == nil {
				continue
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
			if version := this.meta.Version; version != nil {
				b.Raw(", ").Ident(version.Column).Raw(" = ").Ident(version.Column).Raw(" + 1")
			}
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

func (this *repository[M, ID, U]) stamp(ctx context.Context, where crud.Predicate, rs *crud.RelationScopes) (int64, error) {
	f := this.bp.softDelete
	b := crud.NewSQL(this.d, this.meta).RelationScopes(rs).
		Raw("UPDATE ").Table().Raw(" SET ").Ident(f.Column).Raw(" = ").Bind(crud.NowFunc())
	if version := this.meta.Version; version != nil {
		b.Raw(", ").Ident(version.Column).Raw(" = ").Ident(version.Column).Raw(" + 1")
	}
	b.Where(where)
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

	if len(o.Sort) > 0 && !o.NoSort {
		b.OrderBy(o.Sort)
	}

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

func (this *repository[M, ID, U]) SaveAll(ctx context.Context, models []*M) error {
	if len(models) == 0 {
		return nil
	}
	fields, generated, err := this.batchInsertFields(models, "SaveAll")
	if err != nil {
		return err
	}
	tail := this.upsertTail
	if generated {
		tail = ""
	}

	plan, err := this.batchInsertPlan(models, fields, tail, "SaveAll")
	if err != nil {
		return err
	}
	_, err = this.executePrepared(ctx, plan)
	return err
}

func (this *repository[M, ID, U]) InsertBatch(ctx context.Context, models []*M, options ...crud.BatchOption) error {
	if len(models) == 0 {
		return nil
	}
	fields, _, err := this.batchInsertFields(models, "InsertBatch")
	if err != nil {
		return err
	}
	portable := crud.UsesPortableBatch(options...) || this.bp.set.portableBatch
	if used, err := this.nativeInsertBatch(ctx, models, fields, portable); used || err != nil {
		return err
	}
	plan, err := this.batchInsertPlan(models, fields, "", "InsertBatch")
	if err != nil {
		return err
	}
	_, err = this.executePrepared(ctx, plan)
	return err
}

func (this *repository[M, ID, U]) batchInsertFields(models []*M, operation string) ([]*crud.Field, bool, error) {
	generated := false
	for i, model := range models {
		if model == nil {
			return nil, false, &crud.SchemaError{Model: this.meta.Name, Reason: operation + " called with a nil model"}
		}
		hasID, err := this.meta.HasID(model)
		if err != nil {
			return nil, false, err
		}
		gen := !hasID && this.meta.PK.Auto
		if !hasID && !this.meta.PK.Auto {
			return nil, false, crud.ErrMissingID
		}
		if i == 0 {
			generated = gen
			continue
		}
		if gen != generated {
			return nil, false, &crud.SchemaError{Model: this.meta.Name, Reason: operation +
				" cannot mix rows with and without a key: they use different column lists, and splitting the batch would hide the cost"}
		}
	}
	if generated {
		return this.meta.InsertGen, true, nil
	}
	return this.meta.Insert, false, nil
}

func (this *repository[M, ID, U]) nativeInsertBatch(ctx context.Context, models []*M, fields []*crud.Field, portable bool) (bool, error) {
	if portable || len(fields) == 0 {
		return false, nil
	}
	_, ok := crud.UnsafeBulkInserterOf(this.source)
	if !ok {
		return false, nil
	}
	columns := make([]string, len(fields))
	for i, field := range fields {
		columns[i] = field.Column
	}
	rows := make([][]any, len(models))
	for i, model := range models {
		values, err := this.meta.Values(model, fields)
		if err != nil {
			return true, err
		}
		rows[i] = values
	}
	_, err := crud.UnsafeBulkInsertFor(ctx, this.source, this.meta.TableReference(), columns, rows)
	if errors.Is(err, crud.ErrNoBulkInsertSupport) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	return true, nil
}

func (this *repository[M, ID, U]) batchInsertPlan(models []*M, fields []*crud.Field, tail, operation string) ([]preparedWrite, error) {
	limit := crud.BindLimit(this.d)
	width := len(fields)
	if width == 0 {
		plan := make([]preparedWrite, len(models))
		for i := range models {
			q, args, err := crud.NewSQL(this.d, this.meta).
				Raw("INSERT INTO ").Table().Raw(crud.DefaultValuesClause(this.d)).Done()
			if err != nil {
				return nil, err
			}
			plan[i] = preparedWrite{query: q, args: args}
		}
		return plan, nil
	}
	perStatement := limit / width
	if perStatement == 0 {
		advice := "reduce the insertable model width"
		if operation == "SaveAll" {
			advice += " or use InsertBatch with a native-capable source for insert-only data"
		} else {
			advice += " or allow a native bulk capability"
		}
		return nil, &crud.SchemaError{Model: this.meta.Name, Reason: fmt.Sprintf(
			"one %s row needs %d bound values, but dialect %q permits at most %d; %s",
			operation, width, this.d.Name(), limit, advice)}
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
