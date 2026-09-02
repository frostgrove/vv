package security

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
)

var ErrForbidden = fmt.Errorf("security: %w", crud.ErrForbidden)

type Action = crud.Action

const (
	Read    = crud.ActionRead
	Create  = crud.ActionCreate
	Update  = crud.ActionUpdate
	Delete  = crud.ActionDelete
	Restore = crud.ActionRestore
)

type Policy[M any, ID comparable] struct {
	Scope func(ctx context.Context) (crud.Predicate, error)

	AllowUnscopedScope bool

	AllowUnscopedRelationScopes bool

	RelationScopes func(ctx context.Context) (*crud.RelationScopes, error)

	Requires map[Action][]auth.Permission

	Authorize func(ctx context.Context, action Action) error

	Inspect func(ctx context.Context, action Action, m *M) error

	InspectReads bool

	Immutable []string

	AllowUnscopedDeleteAll bool

	AllowUnscopedUpdateAll bool
}

func (this Policy[M, ID]) RequiredFor(action Action) ([]auth.Permission, bool) {
	permissions, declared := this.Requires[action]
	if !declared {
		return nil, false
	}
	return slices.Clone(permissions), true
}

func Gate[M any, ID comparable](p Policy[M, ID]) crud.Middleware[M, ID] {
	validate(p)
	return func(next crud.Core[M, ID]) crud.Core[M, ID] {
		return &gate[M, ID]{Core: next, p: p, immutable: index[M](p.Immutable)}
	}
}

func validate[M any, ID comparable](p Policy[M, ID]) {
	if p.AllowUnscopedScope && p.Scope == nil {
		panic("security: Policy.AllowUnscopedScope requires Policy.Scope")
	}
	if p.AllowUnscopedRelationScopes && p.RelationScopes == nil {
		panic("security: Policy.AllowUnscopedRelationScopes requires Policy.RelationScopes")
	}
	if p.InspectReads && p.Inspect == nil {
		panic("security: Policy.InspectReads requires Policy.Inspect")
	}
	if p.Scope == nil && p.RelationScopes == nil && p.Requires == nil && p.Authorize == nil && p.Inspect == nil && len(p.Immutable) == 0 {
		panic("security: Gate requires a scope, relation scope, authorizer, inspector, or immutable field; bind the repository directly when it is intentionally unrestricted")
	}
}

func index[M any](names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	schema := crud.MustSchemaOf[M]()
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		f := schema.Field(n)
		if f == nil {
			panic("security: Policy.Immutable names " + n + ", which is not a field or column of " +
				schema.Name + " — a frozen name that resolves to nothing freezes nothing")
		}
		m[f.Name] = struct{}{}
	}
	return m
}

type gate[M any, ID comparable] struct {
	crud.Core[M, ID]
	p         Policy[M, ID]
	immutable map[string]struct{}
}

func (this *gate[M, ID]) SupportsRestore() bool {
	return this != nil && crud.SupportsRestore(this.Core)
}

func (this *gate[M, ID]) Next() crud.Core[M, ID] { return this.Core }

func Denied(action Action, reason string) error {
	return fmt.Errorf("%w: %s: %s", ErrForbidden, action, reason)
}

func (this *gate[M, ID]) authorize(ctx context.Context, action Action) error {
	if this.p.Requires != nil {
		if err := requireDeclared(ctx, this.p.Requires, action); err != nil {
			return err
		}
	}
	if this.p.Authorize == nil {
		return nil
	}
	return this.p.Authorize(ctx, action)
}

func (this *gate[M, ID]) inspect(ctx context.Context, a Action, m *M) error {
	if this.p.Inspect == nil {
		return nil
	}
	return this.p.Inspect(ctx, a, m)
}

func (this *gate[M, ID]) scope(ctx context.Context) (crud.Predicate, error) {
	if this.p.Scope == nil {
		return nil, nil
	}
	p, err := this.p.Scope(ctx)
	if err != nil {
		return nil, err
	}
	if crud.IsTautologyFor(this.Meta(), p) && !this.p.AllowUnscopedScope {
		return nil, Denied(Read, "scope returned no narrowing; set AllowUnscopedScope only for an intentional unrestricted principal")
	}
	return p, nil
}

func (this *gate[M, ID]) relationScopes(ctx context.Context) (*crud.RelationScopes, error) {
	if this.p.RelationScopes == nil {
		return nil, nil
	}
	rs, err := this.p.RelationScopes(ctx)
	if err != nil {
		return nil, err
	}
	if rs.Empty() && !this.p.AllowUnscopedRelationScopes {
		return nil, Denied(Read, "relation scopes returned no narrowing; set AllowUnscopedRelationScopes only for an intentional unrestricted principal")
	}
	if rs.Empty() {
		return nil, nil
	}
	return rs.Resolve(this.Meta())
}

func (this *gate[M, ID]) narrow(ctx context.Context) (crud.Option, error) {
	rs, err := this.relationScopes(ctx)
	if err != nil {
		return nil, err
	}
	if rs == nil {
		return nil, nil
	}
	return crud.NarrowRelations(rs), nil
}

func (this *gate[M, ID]) scoped(ctx context.Context, options []crud.Option) ([]crud.Option, crud.Predicate, error) {
	p, err := this.scope(ctx)
	if err != nil {
		return nil, nil, err
	}
	rel, err := this.narrow(ctx)
	if err != nil {
		return nil, nil, err
	}
	if p == nil && rel == nil {
		return options, nil, nil
	}
	return append([]crud.Option{crud.Where(p), rel}, options...), p, nil
}

func (this *gate[M, ID]) writeScopes(ctx context.Context) (crud.Predicate, *crud.RelationScopes, error) {
	scope, err := this.scope(ctx)
	if err != nil {
		return nil, nil, err
	}
	rel, err := this.relationScopes(ctx)
	if err != nil {
		return nil, nil, err
	}
	return scope, rel, nil
}

func relationNarrowing(rs *crud.RelationScopes) crud.Option {
	if rs == nil {
		return nil
	}
	return crud.NarrowRelations(rs)
}

func (this *gate[M, ID]) whole(willInspect bool, options []crud.Option) []crud.Option {
	if !willInspect || this.p.Inspect == nil {
		return options
	}
	return append(append([]crud.Option{}, options...), crud.SelectAll())
}

func inspectionRead() crud.Option {
	return func(o *crud.Options) {
		o.Sort = nil
		o.Preloads = nil
		o.Fields = nil
		o.PreloadRows = 0
		o.Page, o.Limit, o.Offset = 0, 0, 0
		o.After, o.Before = "", ""
		o.Unpaged = false
		o.NoSort, o.NoTotal = true, true
		o.ForUpdate = false
		o.Distinct = false
		o.Agg = crud.AggregateSpec{}
	}
}

func (this *gate[M, ID]) GetByID(ctx context.Context, id ID, options ...crud.Option) (M, error) {
	var zero M
	if err := this.authorize(ctx, Read); err != nil {
		return zero, err
	}
	m, err := this.loadScoped(ctx, id, this.whole(true, options)...)
	if err != nil {
		return zero, err
	}
	if err := this.inspect(ctx, Read, &m); err != nil {
		return zero, err
	}
	return m, nil
}

func (this *gate[M, ID]) loadScoped(ctx context.Context, id ID, options ...crud.Option) (M, error) {
	var zero M
	scope, rel, err := this.writeScopes(ctx)
	if err != nil {
		return zero, err
	}
	return this.loadScopedWith(ctx, id, scope, rel, options...)
}

func (this *gate[M, ID]) loadScopedWith(ctx context.Context, id ID, scope crud.Predicate, rel *crud.RelationScopes, options ...crud.Option) (M, error) {
	var zero M
	if scope == nil {
		return this.Core.GetByID(ctx, id, append([]crud.Option{relationNarrowing(rel)}, options...)...)
	}
	items, err := this.Core.GetAll(ctx, append([]crud.Option{
		crud.Where(scope), relationNarrowing(rel), crud.Where(crud.Eq(this.Meta().PK.Name, id)), crud.Limit(1), crud.Unsorted(),
	}, options...)...)
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, crud.ErrNotFound
	}
	return items[0], nil
}

func (this *gate[M, ID]) Get(ctx context.Context, options ...crud.Option) (crud.PaginatedResponse[M], error) {
	var zero crud.PaginatedResponse[M]
	if err := this.authorize(ctx, Read); err != nil {
		return zero, err
	}
	scoped, _, err := this.scoped(ctx, this.whole(this.p.InspectReads, options))
	if err != nil {
		return zero, err
	}
	page, err := this.Core.Get(ctx, scoped...)
	if err != nil {
		return zero, err
	}
	if err := this.inspectAll(ctx, page.Items); err != nil {
		return zero, err
	}
	return page, nil
}

func (this *gate[M, ID]) GetAll(ctx context.Context, options ...crud.Option) ([]M, error) {
	if err := this.authorize(ctx, Read); err != nil {
		return nil, err
	}
	scoped, _, err := this.scoped(ctx, this.whole(this.p.InspectReads, options))
	if err != nil {
		return nil, err
	}
	items, err := this.Core.GetAll(ctx, scoped...)
	if err != nil {
		return nil, err
	}
	if err := this.inspectAll(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (this *gate[M, ID]) First(ctx context.Context, options ...crud.Option) (M, error) {
	var zero M
	if err := this.authorize(ctx, Read); err != nil {
		return zero, err
	}
	scoped, _, err := this.scoped(ctx, this.whole(this.p.InspectReads, options))
	if err != nil {
		return zero, err
	}
	m, err := this.Core.First(ctx, scoped...)
	if err != nil {
		return zero, err
	}
	if err := this.inspect(ctx, Read, &m); err != nil {
		return zero, err
	}
	return m, nil
}

func (this *gate[M, ID]) inspectAll(ctx context.Context, items []M) error {
	if !this.p.InspectReads || this.p.Inspect == nil {
		return nil
	}
	for i := range items {
		if err := this.p.Inspect(ctx, Read, &items[i]); err != nil {
			return err
		}
	}
	return nil
}

func (this *gate[M, ID]) Aggregate(ctx context.Context, options ...crud.Option) ([]crud.AggregateRow, error) {
	if err := this.authorize(ctx, Read); err != nil {
		return nil, err
	}
	if this.p.Inspect != nil && this.p.InspectReads {
		return nil, Denied(Read, "this policy inspects every row it returns, which an aggregate has none of")
	}
	scoped, _, err := this.scoped(ctx, options)
	if err != nil {
		return nil, err
	}
	return this.Core.Aggregate(ctx, scoped...)
}

func (this *gate[M, ID]) Count(ctx context.Context, options ...crud.Option) (int64, error) {
	if err := this.authorize(ctx, Read); err != nil {
		return 0, err
	}
	scoped, _, err := this.scoped(ctx, options)
	if err != nil {
		return 0, err
	}
	return this.Core.Count(ctx, scoped...)
}

func (this *gate[M, ID]) Exists(ctx context.Context, options ...crud.Option) (bool, error) {
	if err := this.authorize(ctx, Read); err != nil {
		return false, err
	}
	scoped, _, err := this.scoped(ctx, options)
	if err != nil {
		return false, err
	}
	return this.Core.Exists(ctx, scoped...)
}

func (this *gate[M, ID]) InsertBatch(ctx context.Context, models []*M, options ...crud.BatchOption) error {
	if len(models) == 0 {
		return nil
	}
	work := make([]*M, len(models))
	for i, model := range models {
		if model == nil {
			return Denied(Create, "nil model")
		}
		copy := *model
		work[i] = &copy
	}
	if _, _, err := this.writeScopes(ctx); err != nil {
		return err
	}
	if (this.p.Scope != nil || this.p.RelationScopes != nil) && this.p.Inspect == nil {
		return Denied(Create, "a scope-only policy cannot safely authorise InsertBatch; add Inspect to validate each incoming row")
	}
	if err := this.authorize(ctx, Create); err != nil {
		return err
	}
	for _, model := range work {
		if err := this.inspect(ctx, Create, model); err != nil {
			return err
		}
	}
	err, ok := crud.InsertBatchOf(this.Core, ctx, work, options...)
	if !ok {
		return crud.ErrNoBatchInsertSupport
	}
	return err
}

func (this *gate[M, ID]) SaveAll(ctx context.Context, models []*M) error {
	if len(models) == 0 {
		return nil
	}

	work := make([]*M, len(models))
	for i, m := range models {
		if m == nil {
			return Denied(Create, "nil model")
		}
		copy := *m
		work[i] = &copy
	}
	models = work
	scope, rel, err := this.writeScopes(ctx)
	if err != nil {
		return err
	}
	if (this.p.Scope != nil || this.p.RelationScopes != nil) && this.p.Inspect == nil {
		return Denied(Create, "a scope-only policy cannot safely authorise SaveAll; add Inspect to validate each incoming row")
	}
	meta := this.Meta()
	hasAssignedID := false
	assigned := make([]bool, len(models))
	previous := make([]*M, len(models))
	for i, m := range models {
		hasID, err := meta.HasID(m)
		if err != nil {
			return err
		}
		action := Create
		if hasID {
			hasAssignedID = true
			assigned[i] = true

			if err := this.authorize(ctx, Create); err != nil {
				return err
			}
			if err := this.authorize(ctx, Update); err != nil {
				return err
			}
			id, err := meta.ID(m)
			if err != nil {
				return err
			}
			existing, err := this.saveTarget(ctx, meta, id, scope, rel)
			if err != nil {
				return err
			}
			if existing != nil {
				snapshot := *existing
				previous[i] = &snapshot
				action = Update
				if err := this.inspect(ctx, Update, existing); err != nil {
					return err
				}
				if err := this.checkImmutableSave(meta, existing, m); err != nil {
					return err
				}
			}
		}
		if action == Create && !hasID {
			if err := this.authorize(ctx, Create); err != nil {
				return err
			}
		}
		if err := this.inspect(ctx, action, m); err != nil {
			return err
		}
	}

	if hasAssignedID {
		return this.saveTransaction(ctx, func(tx context.Context) error {
			for i, m := range models {
				if assigned[i] {
					if err := this.saveScopedOnly(tx, m, previous[i], scope, rel); err != nil {
						return err
					}
					continue
				}
				if err := this.Core.SaveOnly(tx, m); err != nil {
					return err
				}
			}
			return nil
		})
	}
	return this.Core.SaveAll(ctx, models)
}

func (this *gate[M, ID]) Save(ctx context.Context, m *M) (M, error) {
	var zero M
	if m == nil {
		return zero, Denied(Create, "nil model")
	}
	copy := *m
	return this.save(ctx, &copy, true)
}

func (this *gate[M, ID]) SaveOnly(ctx context.Context, m *M) error {
	if m == nil {
		return Denied(Create, "nil model")
	}
	copy := *m
	_, err := this.save(ctx, &copy, false)
	return err
}

func (this *gate[M, ID]) save(ctx context.Context, m *M, wantStored bool) (M, error) {
	var zero M
	scope, rel, err := this.writeScopes(ctx)
	if err != nil {
		return zero, err
	}
	if (this.p.Scope != nil || this.p.RelationScopes != nil) && this.p.Inspect == nil {
		return zero, Denied(Create, "a scope-only policy cannot safely authorise Save; add Inspect to validate the incoming row")
	}
	meta := this.Meta()
	hasID, err := meta.HasID(m)
	if err != nil {
		return zero, err
	}

	action := Create
	var previous *M
	if hasID {
		if err := this.authorize(ctx, Create); err != nil {
			return zero, err
		}
		if err := this.authorize(ctx, Update); err != nil {
			return zero, err
		}
		id, err := meta.ID(m)
		if err != nil {
			return zero, err
		}
		existing, err := this.saveTarget(ctx, meta, id, scope, rel)
		if err != nil {
			return zero, err
		}
		if existing != nil {
			snapshot := *existing
			previous = &snapshot
			action = Update
			if err := this.inspect(ctx, Update, existing); err != nil {
				return zero, err
			}
			if err := this.checkImmutableSave(meta, existing, m); err != nil {
				return zero, err
			}
		}
	}
	if action == Create && !hasID {
		if err := this.authorize(ctx, Create); err != nil {
			return zero, err
		}
	}

	if err := this.inspect(ctx, action, m); err != nil {
		return zero, err
	}
	if hasID {
		if wantStored {
			if err := this.saveScoped(ctx, m, previous, scope, rel); err != nil {
				return zero, err
			}
			return *m, nil
		}
		return zero, this.saveScopedOnly(ctx, m, previous, scope, rel)
	}
	if wantStored {
		return this.Core.Save(ctx, m)
	}
	return zero, this.Core.SaveOnly(ctx, m)
}

func (this *gate[M, ID]) saveTransaction(ctx context.Context, fn func(context.Context) error) error {
	source, ok := crud.SourceOf(this.Core)
	if !ok {
		return this.Core.Tx(ctx, fn)
	}
	if _, found := crud.ExecutorFor(ctx, source); found {
		return fn(ctx)
	}
	return crud.InNewTx(ctx, source, fn)
}

func (this *gate[M, ID]) saveScoped(ctx context.Context, m, previous *M, scope crud.Predicate, rel *crud.RelationScopes) error {
	err, supported := crud.SaveScopedOf(this.Core, ctx, m, &crud.ScopedSave[M]{
		Previous:       previous,
		Scope:          scope,
		RelationScopes: rel,
	})
	if !supported {
		return Denied(Update, "the storage core cannot perform a scoped upsert atomically")
	}
	if previous == nil && errors.Is(err, crud.ErrCreateRaced) {
		return Denied(Create, "assigned key was concurrently created")
	}
	if errors.Is(err, crud.ErrNotFound) || errors.Is(err, crud.ErrStaleVersion) {
		if scope != nil || rel != nil {
			return crud.ErrNotFound
		}
		return Denied(Update, "row is outside the scope")
	}
	return err
}

func (this *gate[M, ID]) saveScopedOnly(ctx context.Context, m, previous *M, scope crud.Predicate, rel *crud.RelationScopes) error {
	err, supported := crud.SaveScopedOnlyOf(this.Core, ctx, m, &crud.ScopedSave[M]{
		Previous:       previous,
		Scope:          scope,
		RelationScopes: rel,
	})
	if !supported {
		return Denied(Update, "the storage core cannot perform a scoped write-only upsert atomically")
	}
	if previous == nil && errors.Is(err, crud.ErrCreateRaced) {
		return Denied(Create, "assigned key was concurrently created")
	}
	if errors.Is(err, crud.ErrNotFound) || errors.Is(err, crud.ErrStaleVersion) {
		if scope != nil || rel != nil {
			return crud.ErrNotFound
		}
		return Denied(Update, "row is outside the scope")
	}
	return err
}

func (this *gate[M, ID]) saveTarget(ctx context.Context, meta *crud.Meta, id any, scope crud.Predicate, rel *crud.RelationScopes) (*M, error) {
	byID := crud.Where(crud.Eq(meta.PK.Name, id))
	options := []crud.Option{byID, relationNarrowing(rel), crud.Limit(1), crud.Unsorted(), crud.PrimaryOnly()}
	if scope != nil {
		options = append([]crud.Option{crud.Where(scope)}, options...)
	}
	found, err := this.Core.GetAll(ctx, this.whole(true, options)...)
	if err != nil {
		return nil, err
	}
	if len(found) == 1 {
		return &found[0], nil
	}
	if scope == nil {
		return nil, nil
	}

	hidden, err, supported := crud.ExistsUnscopedOf(this.Core, ctx, byID, crud.PrimaryOnly())
	if err != nil {
		return nil, err
	}
	if !supported {
		return nil, crud.ErrNotFound
	}
	if hidden {
		return nil, crud.ErrNotFound
	}
	return nil, nil
}

func (this *gate[M, ID]) checkImmutableSave(meta *crud.Meta, old, next *M) error {
	if len(this.immutable) == 0 {
		return nil
	}
	for name := range this.immutable {
		f := meta.Field(name)
		if f == nil {
			return Denied(Update, "immutable field "+name+" is not part of "+meta.Name)
		}
		a, err := meta.Values(old, []*crud.Field{f})
		if err != nil {
			return err
		}
		b, err := meta.Values(next, []*crud.Field{f})
		if err != nil {
			return err
		}
		if !crud.EqualValues(a[0], b[0]) {
			return Denied(Update, "field "+f.Name+" is immutable")
		}
	}
	return nil
}

func snapshotPredicate[M any](meta *crud.Meta, m *M) (crud.Predicate, error) {
	values, err := meta.Values(m, meta.Fields)
	if err != nil {
		return nil, err
	}
	preds := make([]crud.Predicate, 0, len(meta.Fields))
	for i, f := range meta.Fields {
		value, null, err := snapshotValue(values[i])
		if err != nil {
			return nil, fmt.Errorf("security: snapshot %s.%s: %w", meta.Name, f.Name, err)
		}
		if null {
			preds = append(preds, crud.IsNull(f.Name))
			continue
		}
		preds = append(preds, crud.Eq(f.Name, value))
	}
	return crud.And(preds...), nil
}

func snapshotValue(value any) (resolved any, null bool, err error) {
	valuer, ok := value.(driver.Valuer)
	if !ok && value != nil {
		rv := reflect.ValueOf(value)
		if rv.Kind() != reflect.Pointer {
			copy := reflect.New(rv.Type())
			copy.Elem().Set(rv)
			valuer, ok = copy.Interface().(driver.Valuer)
		}
	}
	if !ok {
		if value == nil {
			return nil, true, nil
		}
		rv := reflect.ValueOf(value)
		if nilable(rv.Kind()) && rv.IsNil() {
			return nil, true, nil
		}
		return value, false, nil
	}
	rv := reflect.ValueOf(value)
	if rv.IsValid() && rv.Kind() == reflect.Pointer && rv.IsNil() {
		return nil, true, nil
	}
	resolved, err = valuer.Value()
	if err != nil {
		return nil, false, err
	}
	if resolved == nil {
		return nil, true, nil
	}
	rv = reflect.ValueOf(resolved)
	if nilable(rv.Kind()) && rv.IsNil() {
		return nil, true, nil
	}
	if !driver.IsValue(resolved) {
		return nil, false, fmt.Errorf("driver.Valuer returned unsupported type %T", resolved)
	}
	return resolved, false, nil
}

func nilable(kind reflect.Kind) bool {
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	default:
		return false
	}
}

func inspectedID[M any, ID comparable](meta *crud.Meta, model *M) (ID, error) {
	var zero ID
	raw, err := meta.ID(model)
	if err != nil {
		return zero, err
	}
	value := crud.ElemValue(raw)
	id, ok := value.(ID)
	if !ok {
		return zero, &crud.SchemaError{Model: meta.Name, Field: meta.PK.Name,
			Reason: fmt.Sprintf("inspected id has type %T after wrapper normalisation, expected the repository id type", value)}
	}

	rv := reflect.ValueOf(id)
	if rv.IsValid() && !rv.Comparable() {
		return zero, &crud.SchemaError{Model: meta.Name, Field: meta.PK.Name,
			Reason: fmt.Sprintf("inspected id value of type %T is not comparable and cannot key an atomic snapshot", id)}
	}
	return id, nil
}

func snapshotPredicates[M any](meta *crud.Meta, models []M) (crud.Predicate, error) {
	preds := make([]crud.Predicate, 0, len(models))
	for i := range models {
		p, err := snapshotPredicate(meta, &models[i])
		if err != nil {
			return nil, err
		}
		preds = append(preds, p)
	}
	return crud.Or(preds...), nil
}

func (this *gate[M, ID]) Update(ctx context.Context, id ID, dataTransferObject any, options ...crud.Option) (M, error) {
	var zero M
	if (this.p.Scope != nil || this.p.RelationScopes != nil) && this.p.Inspect == nil {
		if _, _, err := this.writeScopes(ctx); err != nil {
			return zero, err
		}
		return zero, Denied(Update, "a scope-only policy cannot safely authorise Update; add Inspect to validate the incoming row")
	}
	if err := this.authorize(ctx, Update); err != nil {
		return zero, err
	}

	scope, rel, err := this.writeScopes(ctx)
	if err != nil {
		return zero, err
	}
	if len(this.immutable) > 0 {
		defined, err := crud.DefinedFields(this.Meta().Schema, dataTransferObject)
		if err != nil {
			return zero, err
		}
		for _, name := range defined {
			if _, frozen := this.immutable[name]; frozen {
				return zero, Denied(Update, "field "+name+" is immutable")
			}
		}
	}

	var inspected crud.Predicate
	if this.p.Inspect != nil {
		cur, err := this.loadScopedWith(ctx, id, scope, rel, crud.PrimaryOnly())
		if err != nil {
			return zero, err
		}
		inspected, err = snapshotPredicate(this.Meta(), &cur)
		if err != nil {
			return zero, err
		}
		if err := this.inspect(ctx, Update, &cur); err != nil {
			return zero, err
		}
	}

	return this.Core.Update(ctx, id, dataTransferObject, append([]crud.Option{crud.Where(scope), relationNarrowing(rel), crud.Where(inspected)}, options...)...)
}

func (this *gate[M, ID]) UpdateAll(ctx context.Context, dataTransferObject any, options ...crud.Option) (int64, error) {
	if (this.p.Scope != nil || this.p.RelationScopes != nil) && this.p.Inspect == nil {
		if _, _, err := this.writeScopes(ctx); err != nil {
			return 0, err
		}
		return 0, Denied(Update, "a scope-only policy cannot safely authorise UpdateAll; add Inspect to validate every incoming row")
	}
	if err := this.authorize(ctx, Update); err != nil {
		return 0, err
	}
	if len(this.immutable) > 0 {
		defined, err := crud.DefinedFields(this.Meta().Schema, dataTransferObject)
		if err != nil {
			return 0, err
		}
		for _, name := range defined {
			if _, frozen := this.immutable[name]; frozen {
				return 0, Denied(Update, "field "+name+" is immutable")
			}
		}
	}
	scoped, scope, err := this.scoped(ctx, options)
	if err != nil {
		return 0, err
	}
	if crud.IsTautologyFor(this.Meta(), scope) && crud.IsTautologyFor(this.Meta(), crud.Build(options...).Predicate()) && !this.p.AllowUnscopedUpdateAll {
		return 0, Denied(Update, "refusing an unscoped UpdateAll; set AllowUnscopedUpdateAll to permit it")
	}
	if this.p.Inspect != nil {
		targets, err := this.Core.GetAll(ctx, this.whole(true, append(scoped, crud.PrimaryOnly(), inspectionRead()))...)
		if err != nil {
			return 0, err
		}
		if len(targets) == 0 {
			return 0, nil
		}
		inspected, err := snapshotPredicates(this.Meta(), targets)
		if err != nil {
			return 0, err
		}
		for i := range targets {
			if err := this.p.Inspect(ctx, Update, &targets[i]); err != nil {
				return 0, err
			}
		}
		scoped = append(scoped, crud.Where(inspected))
	}
	return this.Core.UpdateAll(ctx, dataTransferObject, scoped...)
}

func (this *gate[M, ID]) Delete(ctx context.Context, ids ...ID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if err := this.authorize(ctx, Delete); err != nil {
		return 0, err
	}
	scope, rel, err := this.writeScopes(ctx)
	if err != nil {
		return 0, err
	}
	if scope == nil && rel == nil && this.p.Inspect == nil {
		return this.Core.Delete(ctx, ids...)
	}
	var snapshots map[ID]crud.Predicate
	var snapshotList []crud.Predicate
	if this.p.Inspect != nil {
		victims, err := this.deleteVictims(ctx, ids, scope, rel)
		if err != nil {
			return 0, err
		}
		if len(victims) == 0 {
			return 0, nil
		}
		snapshots = make(map[ID]crud.Predicate, len(victims))
		snapshotList = make([]crud.Predicate, 0, len(victims))
		for i := range victims {
			snapshot, err := snapshotPredicate(this.Meta(), &victims[i])
			if err != nil {
				return 0, err
			}
			if err := this.p.Inspect(ctx, Delete, &victims[i]); err != nil {
				return 0, err
			}
			id, err := inspectedID[M, ID](this.Meta(), &victims[i])
			if err != nil {
				return 0, err
			}
			snapshots[id] = snapshot
			snapshotList = append(snapshotList, snapshot)
		}
	}

	if n, err, ok := crud.DeleteScopedOf(this.Core, ctx, &crud.ScopedDelete[ID]{
		IDs: ids, Scope: scope, RelationScopes: rel, Snapshots: snapshots,
	}); ok {
		return n, err
	}

	pk := this.Meta().PK.Name
	within := crud.And(scope, crud.InAny(pk, ids))
	if snapshots != nil {
		within = crud.And(within, crud.Or(snapshotList...))
	}
	return this.Core.DeleteAll(ctx, crud.Where(within), relationNarrowing(rel))
}

func (this *gate[M, ID]) Restore(ctx context.Context, ids ...ID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if err := this.authorize(ctx, Restore); err != nil {
		return 0, err
	}
	scope, rel, err := this.writeScopes(ctx)
	if err != nil {
		return 0, err
	}
	if scope == nil && rel == nil && this.p.Inspect == nil {
		n, err, ok := crud.RestoreOf(this.Core, ctx, ids...)
		if !ok {
			return 0, &crud.SchemaError{Model: this.Meta().Name, Reason: "inner core does not preserve tombstone Restore"}
		}
		return n, err
	}

	var snapshots map[ID]crud.Predicate
	if this.p.Inspect != nil {
		rows, err, ok := crud.LoadTombstonesOf(this.Core, ctx, ids, scope, rel)
		if !ok {
			return 0, &crud.SchemaError{Model: this.Meta().Name, Reason: "inner core cannot load tombstones for Restore inspection"}
		}
		if err != nil {
			return 0, err
		}
		if len(rows) == 0 {
			return 0, nil
		}
		snapshots = make(map[ID]crud.Predicate, len(rows))
		for i := range rows {
			if err := this.p.Inspect(ctx, Restore, &rows[i]); err != nil {
				return 0, err
			}
			snapshot, err := snapshotPredicate(this.Meta(), &rows[i])
			if err != nil {
				return 0, err
			}
			id, err := inspectedID[M, ID](this.Meta(), &rows[i])
			if err != nil {
				return 0, err
			}
			snapshots[id] = snapshot
		}
	}

	n, err, ok := crud.RestoreScopedOf(this.Core, ctx, &crud.ScopedRestore[ID]{
		IDs: ids, Scope: scope, RelationScopes: rel, Snapshots: snapshots,
	})
	if !ok {
		return 0, &crud.SchemaError{Model: this.Meta().Name, Reason: "inner core cannot perform a scoped Restore atomically"}
	}
	return n, err
}

func (this *gate[M, ID]) deleteVictims(ctx context.Context, ids []ID, scope crud.Predicate, rel *crud.RelationScopes) ([]M, error) {
	chunk := len(ids)
	if source, ok := crud.SourceOf(this.Core); ok {
		chunk = min(chunk, crud.BindLimit(source.Dialect()))
	}
	chunk = min(chunk, 4096)
	if chunk < 1 {
		chunk = 1
	}
	var out []M
	var read func([]ID) error
	read = func(part []ID) error {
		byID := crud.InAny(this.Meta().PK.Name, part)
		if len(part) == 1 {
			byID = crud.Eq(this.Meta().PK.Name, part[0])
		}
		rows, err := this.Core.GetAll(ctx, this.whole(true, []crud.Option{
			crud.Where(crud.And(scope, byID)), relationNarrowing(rel), crud.PrimaryOnly(),
		})...)
		if err == nil {
			out = append(out, rows...)
			return nil
		}
		var schemaErr *crud.SchemaError
		if len(part) > 1 && errors.As(err, &schemaErr) {
			middle := len(part) / 2
			if err := read(part[:middle]); err != nil {
				return err
			}
			return read(part[middle:])
		}
		return err
	}
	for start := 0; start < len(ids); start += chunk {
		if err := read(ids[start:min(start+chunk, len(ids))]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (this *gate[M, ID]) DeleteAll(ctx context.Context, options ...crud.Option) (int64, error) {
	if err := this.authorize(ctx, Delete); err != nil {
		return 0, err
	}
	scoped, scope, err := this.scoped(ctx, options)
	if err != nil {
		return 0, err
	}
	if crud.IsTautologyFor(this.Meta(), scope) && crud.IsTautologyFor(this.Meta(), crud.Build(options...).Predicate()) && !this.p.AllowUnscopedDeleteAll {
		return 0, Denied(Delete, "refusing an unscoped DeleteAll; set AllowUnscopedDeleteAll to permit it")
	}
	if this.p.Inspect != nil {
		victims, err := this.Core.GetAll(ctx, this.whole(true, append(scoped, crud.PrimaryOnly(), inspectionRead()))...)
		if err != nil {
			return 0, err
		}
		if len(victims) == 0 {
			return 0, nil
		}
		inspected, err := snapshotPredicates(this.Meta(), victims)
		if err != nil {
			return 0, err
		}
		for i := range victims {
			if err := this.p.Inspect(ctx, Delete, &victims[i]); err != nil {
				return 0, err
			}
		}
		scoped = append(scoped, crud.Where(inspected))
	}
	return this.Core.DeleteAll(ctx, scoped...)
}
