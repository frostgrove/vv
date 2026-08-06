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

	"rx-crud/crud"
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
	Scope func(ctx context.Context) (crud.Predicate, error)

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

// scoped prepends the policy scope to a caller's options.
func (g *gate[M, ID]) scoped(ctx context.Context, opts []crud.Option) ([]crud.Option, crud.Predicate, error) {
	p, err := g.scope(ctx)
	if err != nil {
		return nil, nil, err
	}
	if p == nil {
		return opts, nil, nil
	}
	return append([]crud.Option{crud.Where(p)}, opts...), p, nil
}

// ---------------------------------------------------------------------------
// reads

func (g *gate[M, ID]) GetByID(ctx context.Context, id ID, opts ...crud.Option) (M, error) {
	var zero M
	if err := g.authorize(ctx, Read); err != nil {
		return zero, err
	}
	m, err := g.loadScoped(ctx, id, opts...)
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
func (g *gate[M, ID]) loadScoped(ctx context.Context, id ID, opts ...crud.Option) (M, error) {
	var zero M
	scope, err := g.scope(ctx)
	if err != nil {
		return zero, err
	}
	if scope == nil {
		return g.Core.GetByID(ctx, id, opts...)
	}
	items, err := g.Core.GetAll(ctx, append([]crud.Option{
		crud.Where(scope), crud.Where(crud.Eq(g.Meta().PK.Name, id)), crud.Limit(1), crud.Unsorted(),
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
	scoped, _, err := g.scoped(ctx, opts)
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
	scoped, _, err := g.scoped(ctx, opts)
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
		// A Save with an id may land on an existing row. Look it up without the
		// scope so that a cross-tenant overwrite is a denial rather than a
		// silent insert-turned-update.
		id, err := meta.ID(m)
		if err != nil {
			return err
		}
		existing, err := g.Core.GetAll(ctx, crud.Where(crud.Eq(meta.PK.Name, id)), crud.Limit(1), crud.Unsorted())
		if err != nil {
			return err
		}
		if len(existing) == 1 {
			action = Update
			if err := g.authorize(ctx, Update); err != nil {
				return err
			}
			if err := g.inspect(ctx, Update, &existing[0]); err != nil {
				return err
			}
			if err := g.checkImmutableSave(meta, &existing[0], m); err != nil {
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

func (g *gate[M, ID]) Update(ctx context.Context, id ID, dto any) (M, error) {
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
	return g.Core.Update(ctx, id, dto)
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
		victims, err := g.Core.GetAll(ctx, crud.Where(within))
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
		victims, err := g.Core.GetAll(ctx, scoped...)
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
