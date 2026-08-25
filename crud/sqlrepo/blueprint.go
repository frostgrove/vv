// Package sqlrepo is the implementation of crud.Core whose rows are in a
// database, behind SQL. crudtest holds them in memory and remote holds them in
// another service; everything in crud/decorators/ wraps one of the three.
//
// It is named for where the data is, not for a rank — "basic", which it was
// called until [[D-058]], distinguished it from nothing.
//
// Declaring one is a two-liner next to the model:
//
//	var Users = sqlrepo.Define[User, int64, UserUpdate]("users")
//
//	users := Users.Bind(db)                       // plain
//	users := Users.Bind(db, security.Gate(policy)) // decorated
//
// Define validates the model tags, the ID type and the update DTO eagerly, so a
// broken mapping panics at package initialisation rather than on first request.
package sqlrepo

import (
	"reflect"

	"github.com/shardit-io/vv/crud"
)

// DefaultPageSize is the page size used when a query does not ask for one.
const DefaultPageSize = 20

// Setting tunes a repository at declaration time.
type Setting func(*settings)

type settings struct {
	softDelete   string
	replica      crud.Source
	defaultLimit int
	maxLimit     int
	defaultSort  []crud.Order
	stableSort   bool
	preloadDepth int
	scope        crud.Predicate
	relScopes    []relScope
}

// relScope is a RelationScope declaration, kept as written until Define can
// validate the path against the model.
type relScope struct {
	path string
	pred crud.Predicate
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
//	sqlrepo.Define[User, uint, UserUpdate]("users",
//	    sqlrepo.Scope(crud.IsNull("DeletedAt")))
//
// It applies to Get, GetAll, GetByID, Count, Exists, Delete and DeleteAll, and
// to the load half of Update. It cannot apply to Save, which is an upsert and
// has no WHERE clause — guard that in a service method or a security policy.
//
// It also follows a relation back into this same model, at any depth: a
// preload of User.Manager, and a filter or sort that hops through it, carry the
// scope too. Reaching a *different* model is a different question, because
// another model's rows are another repository's business — say what should
// happen there with RelationScope.
func Scope(p crud.Predicate) Setting { return func(s *settings) { s.scope = p } }

// SoftDelete turns deletion into a column write: Delete and DeleteAll stamp the
// named timestamp instead of removing rows, and every read filters the stamped
// ones out.
//
//	var Docs = sqlrepo.Define[Doc, int64, DocUpdate]("docs",
//	    sqlrepo.SoftDelete("DeletedAt"))
//
// The two halves are one declaration on purpose. Written by hand this is
// sqlrepo.Scope for the reads plus a service layer that overrides two methods for
// the writes, and the failure when somebody adds the first and forgets the
// second is silent: the reads hide rows that the deletes are still destroying.
//
// It lives here rather than in a decorator because a decorator sits above
// crud.Core, and Core has no verb for "write this column" — only Update, whose
// DTO is the one declared at Define time. The stamp is a statement, so it
// belongs where the statements are built.
//
// The field must be nullable, because "not deleted" has to have a value. A
// crud.Opt[time.Time] or a *time.Time; anything else is refused here rather than
// at the first delete.
//
// What it does not do: a unique index still sees the tombstones. Re-creating a
// row whose soft-deleted twin holds the same unique key is a conflict, and the
// answer is a partial index the library cannot write for you.
func SoftDelete(field string) Setting {
	return func(s *settings) { s.softDelete = field }
}

// RelationScope narrows the far side of a relation, wherever this repository
// reaches it: a preload of that path, a filter that hops through it, a sort
// that walks it. The predicate is written against the *target* model.
//
//	sqlrepo.Define[Article, int64, ArticleUpdate]("articles",
//	    sqlrepo.Scope(crud.IsNull("DeletedAt")),                  // articles
//	    sqlrepo.RelationScope("Comments", crud.IsNull("DeletedAt"))) // comments
//
// Scope alone would not have covered the second line. A preload is a second
// statement against a second table and a nested filter opens a correlated
// subquery with its own FROM, so neither inherits the parent statement's WHERE:
// without this, ?preload=comments hands back exactly the rows the article's own
// scope exists to hide.
//
// The path is canonical and may be nested ("Comments.Author"). It is validated
// against the model, so a typo fails at declaration time rather than silently
// narrowing nothing.
func RelationScope(path string, p crud.Predicate) Setting {
	return func(s *settings) { s.relScopes = append(s.relScopes, relScope{path, p}) }
}

// Blueprint is a validated, datasource-independent repository declaration.
// Bind it to as many sources as you like — the reflection work happens once.
type Blueprint[M any, ID comparable, U any] struct {
	meta       *crud.Meta
	plan       *crud.UpdatePlan
	set        settings
	relScopes  *crud.RelationScopes
	softDelete *crud.Field
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
	if err := bp.resolveSoftDelete(); err != nil {
		return nil, err
	}
	if err := bp.resolveRelationScopes(); err != nil {
		return nil, err
	}
	return bp, nil
}

// resolveSoftDelete validates the tombstone column and folds its "not deleted"
// test into the permanent scope — so declaring the delete behaviour is what
// declares the read behaviour, and the two cannot be added separately or fall
// out of step.
func (bp *Blueprint[M, ID, U]) resolveSoftDelete() error {
	if bp.set.softDelete == "" {
		return nil
	}
	f := bp.meta.Field(bp.set.softDelete)
	if f == nil {
		return &crud.SchemaError{Model: bp.meta.Name, Field: bp.set.softDelete,
			Reason: "no such field to soft-delete into"}
	}
	if !f.Optional && f.Type.Kind() != reflect.Pointer {
		return &crud.SchemaError{Model: bp.meta.Name, Field: f.Name,
			Reason: "a soft-delete column has to be nullable, or there is no value that means `not deleted`"}
	}
	if f.PK || f.Immutable || f.Generated || f.Version {
		return &crud.SchemaError{Model: bp.meta.Name, Field: f.Name,
			Reason: "a soft-delete column has to be writable"}
	}
	bp.softDelete = f
	bp.set.scope = crud.And(bp.set.scope, crud.IsNull(f.Name))
	return nil
}

// resolveRelationScopes turns the declarations into the form the SQL writer and
// the preloader consult, and settles the one case that needs no declaring: a
// relation that points back at this repository's own model is narrowed by
// Scope, because there is only one possible answer to what those rows are.
func (bp *Blueprint[M, ID, U]) resolveRelationScopes() error {
	for _, rs := range bp.set.relScopes {
		_, canonical, err := bp.meta.RelationAt(rs.path)
		if err != nil {
			return err
		}
		bp.relScopes = bp.relScopes.AtPath(canonical, rs.pred)
	}
	bp.relScopes = bp.relScopes.ForModel(bp.meta.Type, bp.set.scope)
	return nil
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
