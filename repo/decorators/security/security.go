// Package security is the access-control decorator.
//
// It sits between the caller and the basic repository and enforces three
// independent things:
//
//   - Scope   — a predicate ANDed into every read and every scoped write. This
//     is row-level security: a tenant simply cannot see or touch what the scope
//     excludes, and because the predicate AST is closed, the caller has no way
//     to peel it back off.
//   - Authorize — a coarse "may this principal do this kind of thing at all".
//   - Inspect  — a fine, per-entity check that sees the actual row.
//
// Anything the policy leaves nil is skipped, so a gate that only scopes rows
// costs one extra AND per query and nothing else.
package security

import (
	"context"
	"fmt"

	"github.com/shardit-io/qq/crud"
)

// ErrForbidden is what the gate returns when a policy denies an operation. It
// wraps crud.ErrForbidden, so a transport can map every denial to 403 without
// knowing this package exists.
var ErrForbidden = fmt.Errorf("security: %w", crud.ErrForbidden)

// Action is the kind of operation being attempted.
type Action uint8

const (
	Read Action = iota
	Create
	Update
	Delete
)

func (a Action) String() string {
	switch a {
	case Read:
		return "read"
	case Create:
		return "create"
	case Update:
		return "update"
	case Delete:
		return "delete"
	default:
		return "unknown"
	}
}

// Policy describes what the gate enforces. Every hook is optional.
type Policy[M any, ID comparable] struct {
	// Scope returns the row filter for the current principal. Returning nil
	// means unrestricted — which is what an admin principal should return.
	//
	// It narrows the statement's own FROM and nothing else. Rows of another
	// table reached through a preload, a nested filter or a nested sort are not
	// covered by it; RelationScopes is what covers those.
	Scope func(ctx context.Context) (crud.Predicate, error)

	// RelationScopes returns the narrowing that follows this principal across
	// relation boundaries. Scope hides rows of the repository's own table, and a
	// preload of a second table is a second statement over a table Scope never
	// mentioned — so without this, `?preload=comments` reads every tenant's
	// comments and hands them back attached to rows the caller was allowed to
	// see.
	//
	// Declare it by path when the answer is "whenever *this* repository reaches
	// them", or by model when the answer is "wherever these rows are reached at
	// all", which is what a self-relation needs:
	//
	//	RelationScopes: func(ctx context.Context) (*crud.RelationScopes, error) {
	//	    t, err := tenantOf(ctx)
	//	    if err != nil { return nil, err }
	//	    return (*crud.RelationScopes)(nil).
	//	        AtPath("Comments", crud.Eq("TenantID", t)).
	//	        AtPath("Comments.Author", crud.Eq("TenantID", t)), nil
	//	}
	//
	// ScopeRelationField writes that for one path; Combine merges several.
	RelationScopes func(ctx context.Context) (*crud.RelationScopes, error)

	// Authorize is the coarse check, called once per operation before any SQL.
	Authorize func(ctx context.Context, action Action) error

	// Inspect is the fine check and sees the row itself. On writes it is called
	// with the row as it exists (for updates and deletes) and with the incoming
	// row (for creates and saves).
	Inspect func(ctx context.Context, action Action, m *M) error

	// InspectReads extends Inspect to every row returned by a list query. Off by
	// default: Scope is the cheap way to filter lists.
	InspectReads bool

	// Immutable names model fields that this principal may never update. An
	// update DTO that defines one of them is rejected outright.
	Immutable []string

	// AllowUnscopedDeleteAll permits DeleteAll when neither the policy nor the
	// caller narrowed the statement. Off by default, because an unscoped
	// DeleteAll behind an access-control layer is almost always a bug.
	AllowUnscopedDeleteAll bool

	// AllowUnscopedUpdateAll is the same permission for UpdateAll, and it is a
	// separate one on purpose: rewriting every row in a table is not something a
	// policy should inherit from having allowed the table to be emptied.
	AllowUnscopedUpdateAll bool
}

// Gate builds the middleware. Both type parameters are inferred from the
// policy, so the call site stays clean:
//
//	users := Users.Bind(db, security.Gate(tenantPolicy))
func Gate[M any, ID comparable](p Policy[M, ID]) crud.Middleware[M, ID] {
	return func(next crud.Core[M, ID]) crud.Core[M, ID] {
		return &gate[M, ID]{Core: next, p: p, immutable: index(p.Immutable)}
	}
}

func index(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

type gate[M any, ID comparable] struct {
	crud.Core[M, ID]
	p         Policy[M, ID]
	immutable map[string]struct{}
}

// Denied wraps ErrForbidden with what was refused.
func Denied(action Action, reason string) error {
	return fmt.Errorf("%w: %s: %s", ErrForbidden, action, reason)
}

func (g *gate[M, ID]) authorize(ctx context.Context, a Action) error {
	if g.p.Authorize == nil {
		return nil
	}
	return g.p.Authorize(ctx, a)
}

func (g *gate[M, ID]) inspect(ctx context.Context, a Action, m *M) error {
	if g.p.Inspect == nil {
		return nil
	}
	return g.p.Inspect(ctx, a, m)
}

func (g *gate[M, ID]) scope(ctx context.Context) (crud.Predicate, error) {
	if g.p.Scope == nil {
		return nil, nil
	}
	return g.p.Scope(ctx)
}

// narrow builds the option that carries the policy across relation boundaries.
// A nil Option is a no-op to Apply, so a policy that declares nothing costs one
// nil check.
func (g *gate[M, ID]) narrow(ctx context.Context) (crud.Option, error) {
	if g.p.RelationScopes == nil {
		return nil, nil
	}
	rs, err := g.p.RelationScopes(ctx)
	if err != nil {
		return nil, err
	}
	if rs.Empty() {
		return nil, nil
	}
	return crud.NarrowRelations(rs), nil
}

// scoped prepends the policy scope to a caller's options. Both halves of the
// policy go in front: the row filter for this table, and the narrowing that
// follows every relation this query walks. In front, because Where ANDs — a
// caller cannot subtract either of them by appending anything.
func (g *gate[M, ID]) scoped(ctx context.Context, opts []crud.Option) ([]crud.Option, crud.Predicate, error) {
	p, err := g.scope(ctx)
	if err != nil {
		return nil, nil, err
	}
	rel, err := g.narrow(ctx)
	if err != nil {
		return nil, nil, err
	}
	if p == nil && rel == nil {
		return opts, nil, nil
	}
	return append([]crud.Option{crud.Where(p), rel}, opts...), p, nil
}

// whole cancels a caller's projection, for the reads whose rows are about to be
// handed to Inspect. Inspect is an opaque closure, so the gate cannot know which
// columns it reads; given a projected row it compares against zero values and
// believes them. That cuts both ways, and ?select= is untrusted input: under the
// tenancy policy in the package documentation every projected read became a
// denial, and a rule that hides rows by a column value was bypassed by simply
// not selecting that column.
func (g *gate[M, ID]) whole(willInspect bool, opts []crud.Option) []crud.Option {
	if !willInspect || g.p.Inspect == nil {
		return opts
	}
	return append(append([]crud.Option{}, opts...), crud.SelectAll())
}

// ---------------------------------------------------------------------------
// reads

func (g *gate[M, ID]) GetByID(ctx context.Context, id ID, opts ...crud.Option) (M, error) {
	var zero M
	if err := g.authorize(ctx, Read); err != nil {
		return zero, err
	}
	m, err := g.loadScoped(ctx, id, g.whole(true, opts)...)
	if err != nil {
		return zero, err
	}
	if err := g.inspect(ctx, Read, &m); err != nil {
		return zero, err
	}
	return m, nil
}

// loadScoped fetches one row through the policy scope. Out-of-scope rows come
// back as ErrNotFound, never as a denial — a 403 would leak that the id exists.
// It deliberately does not run Authorize, so the caller decides which action is
// being authorised.
// loadScoped is a read that decides a write on every path but GetByID, so it
// stays on the primary: authorising against a replica that has not caught up
// checks a row as it was, not as it is.
func (g *gate[M, ID]) loadScoped(ctx context.Context, id ID, opts ...crud.Option) (M, error) {
	var zero M
	scope, err := g.scope(ctx)
	if err != nil {
		return zero, err
	}
	rel, err := g.narrow(ctx)
	if err != nil {
		return zero, err
	}
	if scope == nil {
		return g.Core.GetByID(ctx, id, append([]crud.Option{rel}, opts...)...)
	}
	items, err := g.Core.GetAll(ctx, append([]crud.Option{
		crud.Where(scope), rel, crud.Where(crud.Eq(g.Meta().PK.Name, id)), crud.Limit(1), crud.Unsorted(),
	}, opts...)...)
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, crud.ErrNotFound
	}
	return items[0], nil
}

func (g *gate[M, ID]) Get(ctx context.Context, opts ...crud.Option) (crud.PaginatedResponse[M], error) {
	var zero crud.PaginatedResponse[M]
	if err := g.authorize(ctx, Read); err != nil {
		return zero, err
	}
	scoped, _, err := g.scoped(ctx, g.whole(g.p.InspectReads, opts))
	if err != nil {
		return zero, err
	}
	page, err := g.Core.Get(ctx, scoped...)
	if err != nil {
		return zero, err
	}
	if err := g.inspectAll(ctx, page.Items); err != nil {
		return zero, err
	}
	return page, nil
}

func (g *gate[M, ID]) GetAll(ctx context.Context, opts ...crud.Option) ([]M, error) {
	if err := g.authorize(ctx, Read); err != nil {
		return nil, err
	}
	scoped, _, err := g.scoped(ctx, g.whole(g.p.InspectReads, opts))
	if err != nil {
		return nil, err
	}
	items, err := g.Core.GetAll(ctx, scoped...)
	if err != nil {
		return nil, err
	}
	if err := g.inspectAll(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (g *gate[M, ID]) inspectAll(ctx context.Context, items []M) error {
	if !g.p.InspectReads || g.p.Inspect == nil {
		return nil
	}
	for i := range items {
		if err := g.p.Inspect(ctx, Read, &items[i]); err != nil {
			return err
		}
	}
	return nil
}

// Aggregate is scoped like any other read, and it has to be spelled out rather
// than inherited: the gate embeds crud.Core, so an aggregate that fell through
// to the plain repository would answer over every row in the table. "How many"
// is as much of a disclosure as "which ones" — a count that changes when another
// tenant writes is an oracle for their data.
//
// Inspect has nothing to look at here. A summary row is not an entity, so a
// policy that authorises per row cannot express itself over one; such a policy
// refuses the whole call rather than pretending to have checked it.
func (g *gate[M, ID]) Aggregate(ctx context.Context, opts ...crud.Option) ([]crud.AggregateRow, error) {
	if err := g.authorize(ctx, Read); err != nil {
		return nil, err
	}
	if g.p.Inspect != nil && g.p.InspectReads {
		return nil, Denied(Read, "this policy inspects every row it returns, which an aggregate has none of")
	}
	scoped, _, err := g.scoped(ctx, opts)
	if err != nil {
		return nil, err
	}
	return g.Core.Aggregate(ctx, scoped...)
}

func (g *gate[M, ID]) Count(ctx context.Context, opts ...crud.Option) (int64, error) {
	if err := g.authorize(ctx, Read); err != nil {
		return 0, err
	}
	scoped, _, err := g.scoped(ctx, opts)
	if err != nil {
		return 0, err
	}
	return g.Core.Count(ctx, scoped...)
}

func (g *gate[M, ID]) Exists(ctx context.Context, opts ...crud.Option) (bool, error) {
	if err := g.authorize(ctx, Read); err != nil {
		return false, err
	}
	scoped, _, err := g.scoped(ctx, opts)
	if err != nil {
		return false, err
	}
	return g.Core.Exists(ctx, scoped...)
}

// ---------------------------------------------------------------------------
// writes

// SaveAll runs every check Save runs, once per row, and only then hands the
// batch down as one statement.
//
// Spelled out rather than inherited, for the same reason Aggregate is: the gate
// embeds crud.Core, so a SaveAll that fell through would write the whole batch
// with no authorisation, no per-row inspection and no immutable-field check —
// the one call that writes the most rows would be the one that checks none of
// them.
//
// The checks cost one lookup per row with an assigned key, which is what Save
// costs too. That is the price of the batch being safe; it is still one INSERT.
func (g *gate[M, ID]) SaveAll(ctx context.Context, models []*M) error {
	if len(models) == 0 {
		return nil
	}
	meta := g.Meta()
	for _, m := range models {
		if m == nil {
			return Denied(Create, "nil model")
		}
		hasID, err := meta.HasID(m)
		if err != nil {
			return err
		}
		action := Create
		if hasID {
			id, err := meta.ID(m)
			if err != nil {
				return err
			}
			existing, err := g.saveTarget(ctx, meta, id)
			if err != nil {
				return err
			}
			if existing != nil {
				action = Update
				if err := g.authorize(ctx, Update); err != nil {
					return err
				}
				if err := g.inspect(ctx, Update, existing); err != nil {
					return err
				}
				if err := g.checkImmutableSave(meta, existing, m); err != nil {
					return err
				}
			}
		}
		if action == Create {
			if err := g.authorize(ctx, Create); err != nil {
				return err
			}
		}
		if err := g.inspect(ctx, action, m); err != nil {
			return err
		}
	}
	return g.Core.SaveAll(ctx, models)
}

func (g *gate[M, ID]) Save(ctx context.Context, m *M) error {
	if m == nil {
		return Denied(Create, "nil model")
	}
	meta := g.Meta()
	hasID, err := meta.HasID(m)
	if err != nil {
		return err
	}

	action := Create
	if hasID {
		id, err := meta.ID(m)
		if err != nil {
			return err
		}
		existing, err := g.saveTarget(ctx, meta, id)
		if err != nil {
			return err
		}
		if existing != nil {
			action = Update
			if err := g.authorize(ctx, Update); err != nil {
				return err
			}
			if err := g.inspect(ctx, Update, existing); err != nil {
				return err
			}
			if err := g.checkImmutableSave(meta, existing, m); err != nil {
				return err
			}
		}
	}
	if action == Create {
		if err := g.authorize(ctx, Create); err != nil {
			return err
		}
	}
	// Inspect the incoming state too: this is what catches a row being written
	// into somebody else's scope.
	if err := g.inspect(ctx, action, m); err != nil {
		return err
	}
	return g.Core.Save(ctx, m)
}

// saveTarget returns the row a Save with an id would overwrite, or nil when the
// statement is a plain insert.
//
// The lookup goes through the scope, and a row the scope hides is refused
// outright rather than reported as absent. Save is an upsert: there is no WHERE
// clause for the scope to narrow, so refusing is the only move it has. Left
// alone, a policy that scoped rows and nothing else gave Save no protection at
// all — the insert turned into an update and re-tenanted somebody else's row
// with err == nil.
func (g *gate[M, ID]) saveTarget(ctx context.Context, meta *crud.Meta, id any) (*M, error) {
	byID := crud.Where(crud.Eq(meta.PK.Name, id))
	scope, err := g.scope(ctx)
	if err != nil {
		return nil, err
	}
	rel, err := g.narrow(ctx)
	if err != nil {
		return nil, err
	}
	opts := []crud.Option{byID, rel, crud.Limit(1), crud.Unsorted(), crud.PrimaryOnly()}
	if scope != nil {
		opts = append([]crud.Option{crud.Where(scope)}, opts...)
	}
	found, err := g.Core.GetAll(ctx, g.whole(true, opts)...)
	if err != nil {
		return nil, err
	}
	if len(found) == 1 {
		return &found[0], nil
	}
	if scope == nil {
		return nil, nil
	}
	// Nothing visible under that id. It is still an overwrite if the row is
	// merely hidden, and only an insert if it is genuinely not there.
	hidden, err := g.Core.Exists(ctx, byID, crud.PrimaryOnly())
	if err != nil {
		return nil, err
	}
	if hidden {
		return nil, Denied(Update, "row is outside the scope")
	}
	return nil, nil
}

// checkImmutableSave rejects a full Save that would change a field the policy
// froze.
func (g *gate[M, ID]) checkImmutableSave(meta *crud.Meta, old, next *M) error {
	if len(g.immutable) == 0 {
		return nil
	}
	for name := range g.immutable {
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

func (g *gate[M, ID]) Update(ctx context.Context, id ID, dto any, opts ...crud.Option) (M, error) {
	var zero M
	if err := g.authorize(ctx, Update); err != nil {
		return zero, err
	}
	if len(g.immutable) > 0 {
		defined, err := crud.DefinedFields(g.Meta().Schema, dto)
		if err != nil {
			return zero, err
		}
		for _, name := range defined {
			if _, frozen := g.immutable[name]; frozen {
				return zero, Denied(Update, "field "+name+" is immutable")
			}
		}
	}
	// The scope applies here too, so an out-of-scope id is ErrNotFound rather
	// than a denial.
	cur, err := g.loadScoped(ctx, id)
	if err != nil {
		return zero, err
	}
	if err := g.inspect(ctx, Update, &cur); err != nil {
		return zero, err
	}
	// And it goes into the write itself, not just the check in front of it.
	// Checking here and writing unscoped was check-then-act: a row that left
	// the scope in between was updated anyway, and the fresh copy of somebody
	// else's record was handed back to this caller with err == nil.
	scope, err := g.scope(ctx)
	if err != nil {
		return zero, err
	}
	rel, err := g.narrow(ctx)
	if err != nil {
		return zero, err
	}
	return g.Core.Update(ctx, id, dto, append([]crud.Option{crud.Where(scope), rel}, opts...)...)
}

// UpdateAll is the filtered write, and it is the one that most needs a gate: it
// touches rows nobody named. Everything Update checks is checked here — the
// coarse authorisation, the frozen fields, the scope in the statement's own
// WHERE — and Inspect additionally sees each row that is about to change, since
// with no id in the call there is nothing else that could stand for consent.
func (g *gate[M, ID]) UpdateAll(ctx context.Context, dto any, opts ...crud.Option) (int64, error) {
	if err := g.authorize(ctx, Update); err != nil {
		return 0, err
	}
	if len(g.immutable) > 0 {
		defined, err := crud.DefinedFields(g.Meta().Schema, dto)
		if err != nil {
			return 0, err
		}
		for _, name := range defined {
			if _, frozen := g.immutable[name]; frozen {
				return 0, Denied(Update, "field "+name+" is immutable")
			}
		}
	}
	scoped, scope, err := g.scoped(ctx, opts)
	if err != nil {
		return 0, err
	}
	if scope == nil && crud.Build(opts...).Predicate() == nil && !g.p.AllowUnscopedUpdateAll {
		return 0, Denied(Update, "refusing an unscoped UpdateAll; set AllowUnscopedUpdateAll to permit it")
	}
	if g.p.Inspect != nil {
		targets, err := g.Core.GetAll(ctx, g.whole(true, append(scoped, crud.PrimaryOnly()))...)
		if err != nil {
			return 0, err
		}
		for i := range targets {
			if err := g.p.Inspect(ctx, Update, &targets[i]); err != nil {
				return 0, err
			}
		}
	}
	return g.Core.UpdateAll(ctx, dto, scoped...)
}

func (g *gate[M, ID]) Delete(ctx context.Context, ids ...ID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if err := g.authorize(ctx, Delete); err != nil {
		return 0, err
	}
	scope, err := g.scope(ctx)
	if err != nil {
		return 0, err
	}
	if scope == nil && g.p.Inspect == nil {
		return g.Core.Delete(ctx, ids...)
	}
	pk := g.Meta().PK.Name
	within := crud.And(scope, crud.InAny(pk, ids))
	if g.p.Inspect != nil {
		rel, err := g.narrow(ctx)
		if err != nil {
			return 0, err
		}
		victims, err := g.Core.GetAll(ctx, g.whole(true, []crud.Option{crud.Where(within), rel, crud.PrimaryOnly()})...)
		if err != nil {
			return 0, err
		}
		for i := range victims {
			if err := g.p.Inspect(ctx, Delete, &victims[i]); err != nil {
				return 0, err
			}
		}
	}
	return g.Core.DeleteAll(ctx, crud.Where(within))
}

func (g *gate[M, ID]) DeleteAll(ctx context.Context, opts ...crud.Option) (int64, error) {
	if err := g.authorize(ctx, Delete); err != nil {
		return 0, err
	}
	scoped, scope, err := g.scoped(ctx, opts)
	if err != nil {
		return 0, err
	}
	if scope == nil && crud.Build(opts...).Predicate() == nil && !g.p.AllowUnscopedDeleteAll {
		return 0, Denied(Delete, "refusing an unscoped DeleteAll; set AllowUnscopedDeleteAll to permit it")
	}
	if g.p.Inspect != nil {
		victims, err := g.Core.GetAll(ctx, g.whole(true, append(scoped, crud.PrimaryOnly()))...)
		if err != nil {
			return 0, err
		}
		for i := range victims {
			if err := g.p.Inspect(ctx, Delete, &victims[i]); err != nil {
				return 0, err
			}
		}
	}
	return g.Core.DeleteAll(ctx, scoped...)
}
