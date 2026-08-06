// Package basic holds the plain CRUD repository: the layer that actually talks
// SQL. Everything else in repo/ decorates a basic repository.
//
// Declaring one is a two-liner next to the model:
//
//	var Users = basic.Define[User, int64, UserUpdate]("users")
//
//	users := Users.Bind(db)                       // plain
//	users := Users.Bind(db, security.Gate(policy)) // decorated
//
// Define validates the model tags, the ID type and the update DTO eagerly, so a
// broken mapping panics at package initialisation rather than on first request.
package basic

import (
	"reflect"

	"rx-crud/crud"
)

// DefaultPageSize is the page size used when a query does not ask for one.
const DefaultPageSize = 20

// Setting tunes a repository at declaration time.
type Setting func(*settings)

type settings struct {
	defaultLimit int
	maxLimit     int
	defaultSort  []crud.Order
	stableSort   bool
	preloadDepth int
	scope        crud.Predicate
}

// DefaultLimit sets the page size used when a caller does not pass one.
func DefaultLimit(n int) Setting { return func(s *settings) { s.defaultLimit = n } }

// MaxLimit caps the page size a caller may request. Zero disables the cap.
func MaxLimit(n int) Setting { return func(s *settings) { s.maxLimit = n } }

// DefaultSort is applied to paginated queries that do not sort explicitly.
func DefaultSort(orders ...crud.Order) Setting {
	return func(s *settings) { s.defaultSort = orders }
}

// UnstablePagination turns off the implicit primary-key tiebreaker that is
// otherwise appended to paginated queries.
func UnstablePagination() Setting { return func(s *settings) { s.stableSort = false } }

// PreloadDepth caps how many relation hops a single preload path may take.
func PreloadDepth(n int) Setting { return func(s *settings) { s.preloadDepth = n } }

// Scope narrows every read this repository performs, permanently. It is ANDed
// in before anything a caller passes, so no query option can widen it again.
//
// This is how a table with soft deletes stops handing back tombstones:
//
//	basic.Define[User, uint, UserUpdate]("users",
//	    basic.Scope(crud.IsNull("DeletedAt")))
//
// It applies to Get, GetAll, GetByID, Count, Exists and DeleteAll, and to the
// load half of Update. It cannot apply to Save, which is an upsert and has no
// WHERE clause — guard that in a service method or a security policy.
func Scope(p crud.Predicate) Setting { return func(s *settings) { s.scope = p } }

// Blueprint is a validated, datasource-independent repository declaration.
// Bind it to as many sources as you like — the reflection work happens once.
type Blueprint[M any, ID comparable, U any] struct {
	meta *crud.Meta
	plan *crud.UpdatePlan
	set  settings
}

// Define declares a repository for model M with primary key ID and update DTO
// U, and panics if the declaration does not hold together. An empty table name
// falls back to the snake_case plural of the model type name.
func Define[M any, ID comparable, U any](table string, opts ...Setting) *Blueprint[M, ID, U] {
	bp, err := TryDefine[M, ID, U](table, opts...)
	if err != nil {
		panic(err)
	}
	return bp
}

// TryDefine is Define without the panic.
func TryDefine[M any, ID comparable, U any](table string, opts ...Setting) (*Blueprint[M, ID, U], error) {
	meta, err := crud.NewMeta[M](table)
	if err != nil {
		return nil, err
	}
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
	// Declaring the repository is also what teaches relations on other models
	// which table this one lives in.
	crud.RegisterTable[M](meta.Table)
	for _, o := range opts {
		o(&bp.set)
	}
	if bp.set.defaultLimit <= 0 {
		bp.set.defaultLimit = DefaultPageSize
	}
	return bp, nil
}

// Meta exposes the bound model description — useful for building specifications
// or introspecting columns.
func (bp *Blueprint[M, ID, U]) Meta() *crud.Meta { return bp.meta }

// Bind attaches the blueprint to a datasource, wrapping it in the given
// decorators. The first decorator ends up outermost.
func (bp *Blueprint[M, ID, U]) Bind(src crud.Source, mw ...crud.Middleware[M, ID]) crud.Repo[M, ID, U] {
	core := crud.Core[M, ID](newRepository[M, ID, U](src, bp))
	return crud.Wrap[M, ID, U](crud.Chain(core, mw...))
}

// New declares and binds in one call.
func New[M any, ID comparable, U any](src crud.Source, table string, opts ...Setting) crud.Repo[M, ID, U] {
	return Define[M, ID, U](table, opts...).Bind(src)
}
