package sqlrepo

import (
	"reflect"

	"github.com/frostgrove/vv/crud"
)

const DefaultPageSize = 20

type Setting func(*settings)

type settings struct {
	softDelete    string
	replica       crud.Source
	defaultLimit  int
	maxLimit      int
	defaultSort   []crud.Order
	stableSort    bool
	preloadDepth  int
	scope         crud.Predicate
	relScopes     []relScope
	portableBatch bool

	independentTable bool
}

type relScope struct {
	path string
	pred crud.Predicate
}

func DefaultLimit(n int) Setting { return func(s *settings) { s.defaultLimit = n } }

func MaxLimit(n int) Setting { return func(s *settings) { s.maxLimit = n } }

func DefaultSort(orders ...crud.Order) Setting {
	return func(s *settings) { s.defaultSort = orders }
}

func UnstablePagination() Setting { return func(s *settings) { s.stableSort = false } }

func PreloadDepth(n int) Setting { return func(s *settings) { s.preloadDepth = n } }

func PortableBatch() Setting { return func(s *settings) { s.portableBatch = true } }

func Scope(p crud.Predicate) Setting {
	return func(s *settings) { s.scope = crud.And(s.scope, p) }
}

func SoftDelete(field string) Setting {
	return func(s *settings) { s.softDelete = field }
}

func RelationScope(path string, p crud.Predicate) Setting {
	return func(s *settings) { s.relScopes = append(s.relScopes, relScope{path, p}) }
}

func IndependentTable() Setting {
	return func(s *settings) { s.independentTable = true }
}

type Blueprint[M any, ID comparable, U any] struct {
	meta       *crud.Meta
	plan       *crud.UpdatePlan
	set        settings
	relScopes  *crud.RelationScopes
	softDelete *crud.Field

	restoreScope crud.Predicate
}

func Define[M any, ID comparable, U any](table string, options ...Setting) *Blueprint[M, ID, U] {
	bp, err := TryDefine[M, ID, U](table, options...)
	if err != nil {
		panic(err)
	}
	return bp
}

func TryDefine[M any, ID comparable, U any](table string, options ...Setting) (*Blueprint[M, ID, U], error) {
	meta, err := crud.NewMeta[M](table)
	if err != nil {
		return nil, err
	}
	return tryDefine[M, ID, U](meta, options...)
}

func DefineInSchema[M any, ID comparable, U any](schema, table string, options ...Setting) *Blueprint[M, ID, U] {
	bp, err := TryDefineInSchema[M, ID, U](schema, table, options...)
	if err != nil {
		panic(err)
	}
	return bp
}

func TryDefineInSchema[M any, ID comparable, U any](schema, table string, options ...Setting) (*Blueprint[M, ID, U], error) {
	meta, err := crud.NewMetaInSchema[M](schema, table)
	if err != nil {
		return nil, err
	}
	return tryDefine[M, ID, U](meta, options...)
}

func tryDefine[M any, ID comparable, U any](meta *crud.Meta, options ...Setting) (*Blueprint[M, ID, U], error) {
	var id ID
	if err := meta.CheckID(reflect.TypeOf(&id).Elem()); err != nil {
		return nil, err
	}
	plan, err := crud.PlanFor[U](meta.Schema)
	if err != nil {
		return nil, err
	}
	bp := &Blueprint[M, ID, U]{
		meta: meta,
		plan: plan,
		set: settings{
			defaultLimit: DefaultPageSize,
			stableSort:   true,
			preloadDepth: crud.DefaultPreloadDepth,
		},
	}
	for _, o := range options {
		o(&bp.set)
	}
	if bp.set.defaultLimit <= 0 {
		bp.set.defaultLimit = DefaultPageSize
	}
	if err := bp.resolveSoftDelete(); err != nil {
		return nil, err
	}
	if err := bp.resolveRelationScopes(); err != nil {
		return nil, err
	}

	if !bp.set.independentTable {
		if err := crud.TryRegisterTableRef[M](meta.TableReference()); err != nil {
			return nil, err
		}
	}
	return bp, nil
}

func (this *Blueprint[M, ID, U]) resolveSoftDelete() error {
	this.restoreScope = this.set.scope
	if this.set.softDelete == "" && this.meta.Tombstone != nil {
		this.set.softDelete = this.meta.Tombstone.Name
	}
	if this.set.softDelete == "" {
		return nil
	}
	f := this.meta.Field(this.set.softDelete)
	if f == nil {
		return &crud.SchemaError{Model: this.meta.Name, Field: this.set.softDelete,
			Reason: "no such field to soft-delete into"}
	}
	if !f.Nullable() {
		return &crud.SchemaError{Model: this.meta.Name, Field: f.Name,
			Reason: "a soft-delete column has to be nullable, or there is no value that means `not deleted`"}
	}
	if !f.AcceptsTombstoneTimestamp() {
		return &crud.SchemaError{Model: this.meta.Name, Field: f.Name,
			Reason: "a soft-delete column must carry time.Time: use *time.Time, Opt[time.Time], or a Scanner/Valuer timestamp wrapper"}
	}
	if tagged := this.meta.Tombstone; tagged != nil && tagged != f {
		return &crud.SchemaError{Model: this.meta.Name, Field: f.Name,
			Reason: "SoftDelete conflicts with model tombstone " + tagged.Name}
	}
	if f.PK || f.Immutable || f.Generated || f.Version || (f.ServerOwned && !f.Tombstone) {
		return &crud.SchemaError{Model: this.meta.Name, Field: f.Name,
			Reason: "a soft-delete column has to be writable"}
	}
	if this.plan.IncludesField(f) {
		return &crud.SchemaError{Model: this.meta.Name, Field: f.Name,
			Reason: "a soft-delete column cannot be present in the update DTO; tag it `tombstone` so codegen excludes it or remove it from the hand-written patch"}
	}
	this.softDelete = f

	meta := *this.meta
	schema := *this.meta.Schema
	schema.Insert = withoutField(schema.Insert, f)
	schema.InsertGen = withoutField(schema.InsertGen, f)
	schema.Update = withoutField(schema.Update, f)

	schema.Tombstone = f
	meta.Schema = &schema
	this.meta = &meta
	this.set.scope = crud.And(this.set.scope, crud.IsNull(f.Name))
	return nil
}

func withoutField(fields []*crud.Field, excluded *crud.Field) []*crud.Field {
	out := make([]*crud.Field, 0, len(fields))
	for _, field := range fields {
		if field != excluded {
			out = append(out, field)
		}
	}
	return out
}

func (this *Blueprint[M, ID, U]) resolveRelationScopes() error {
	for _, rs := range this.set.relScopes {
		canonical, err := this.meta.ValidateRelationPath(rs.path)
		if err != nil {
			return err
		}
		this.relScopes = this.relScopes.AtPath(canonical, rs.pred)
	}
	this.relScopes = this.relScopes.ForModel(this.meta.Type, this.set.scope)
	return nil
}

func (this *Blueprint[M, ID, U]) Meta() *crud.Meta { return this.meta }

func (this *Blueprint[M, ID, U]) Bind(source crud.Source, mw ...crud.Middleware[M, ID]) *crud.Repo[M, ID, U] {
	core := crud.Core[M, ID](newRepository[M, ID, U](source, this))
	return crud.Wrap[M, ID, U](crud.Chain(core, mw...))
}

func New[M any, ID comparable, U any](source crud.Source, table string, options ...Setting) *crud.Repo[M, ID, U] {
	return Define[M, ID, U](table, options...).Bind(source)
}
