// Package security is the access-control decorator.
//
// It sits between the caller and the repository it wraps and enforces three
// independent things:
//
//   - Scope   — a predicate ANDed into every read and every scoped write. This
//     is row-level security: a tenant simply cannot see or touch what the scope
//     excludes, and because the predicate AST is closed, the caller has no way
//     to peel it back off.
//   - Authorize — a coarse "may this principal do this kind of thing at all".
//   - Inspect  — a fine, per-entity check that sees the actual row.
//
// A policy may leave individual checks nil, but it must enforce at least one
// thing. A Gate with no effective rule is a declaration error: attaching it to
// a repository must never look like tenant protection while passing every row.
package security

import (
	"context"
	"errors"
	"fmt"

	"github.com/frostgrove/vv/crud"
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

func (this Action) String() string {
	switch this {
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
	// Scope returns the row filter for the current principal. Returning nil or an
	// unconditional predicate is refused unless AllowUnscopedScope is set. A
	// missed tenant lookup must not be indistinguishable from an intentional
	// administrator bypass.
	//
	// It narrows the statement's own FROM and nothing else. Rows of another
	// table reached through a preload, a nested filter or a nested sort are not
	// covered by it; RelationScopes is what covers those.
	Scope func(ctx context.Context) (crud.Predicate, error)

	// AllowUnscopedScope permits Scope to return nil or True intentionally. It is
	// an explicit declaration for a policy that sometimes represents an
	// unrestricted administrator; without it an unconditional predicate fails
	// closed.
	// It has no effect when Scope itself is nil.
	AllowUnscopedScope bool

	// AllowUnscopedRelationScopes permits RelationScopes to return nil or an
	// empty declaration intentionally. It is the relation counterpart of
	// AllowUnscopedScope: a failed principal lookup must not turn a preload or a
	// nested filter into an unrestricted read.
	AllowUnscopedRelationScopes bool

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

	// Authorize is the coarse check, called before any SQL. An assigned-key Save is
	// an upsert, so the gate checks both Create and Update before it looks up which
	// branch applies; grant both for a principal allowed to use that convenience.
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
//
// Hooks that are absent place no restriction of their own, but a policy must
// contain at least one real rule; Gate(Policy{}) is rejected at start-up, so an
// intentionally unrestricted repository is bound directly. A scope-only
// policy may serve reads, but cannot write a body: Save, Update and UpdateAll
// can change a field that moves a row out of the SQL scope. The gate needs
// Inspect to prove the incoming state belongs to an arbitrary predicate. With
// Inspect, an assigned-key Save becomes a create-only insert or a
// snapshot-pinned scoped update, never an unguarded upsert. Use ScopeField for
// the normal tenancy case (it includes Inspect), or supply Inspect beside a
// custom Scope. [[D-060]] says why this fails closed.
func Gate[M any, ID comparable](p Policy[M, ID]) crud.Middleware[M, ID] {
	validate(p)
	return func(next crud.Core[M, ID]) crud.Core[M, ID] {
		return &gate[M, ID]{Core: next, p: p, immutable: index[M](p.Immutable)}
	}
}

// validate distinguishes an intentionally narrow gate (a freeze-only or
// read-only policy is useful) from the accidental zero Policy that otherwise
// makes a visibly gated resource completely public.
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
	if p.Scope == nil && p.RelationScopes == nil && p.Authorize == nil && p.Inspect == nil && len(p.Immutable) == 0 {
		panic("security: Gate requires a scope, relation scope, authorizer, inspector, or immutable field; bind the repository directly when it is intentionally unrestricted")
	}
}

// index resolves the frozen names once, to the model's canonical spelling, and
// refuses one that names nothing.
//
// It used to keep the strings as written, and the two enforcement points then
// spoke different vocabularies: Update compares against crud.DefinedFields,
// which answers *canonical* field names, while Save resolved each name through
// the forgiving meta.Field, which also accepts the column spelling. So
// Freeze("tenant_id") froze the column on PUT and **not** on PATCH — the field
// was silently writable through the verb a client is most likely to use.
//
// The panic is the part that has to land before a tag. It is a declaration
// mistake, so [[D-021]] puts it at start-up; after a release it would stop an
// already-deployed application from booting, which is a different and much worse
// conversation than a build that never started.
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

// Next hands back the Core this gate wraps, so a chain built with the gate in
// the middle stays walkable ([[crud.Nexter]]).
//
// Without it a probe wired above a gate could not find the repository
// underneath, and the order the two decorators happened to be listed in decided
// whether the probe ran at all. Forwarding what it wraps is a decorator's job
// even when the decorator itself has nothing to say about the question.
func (this *gate[M, ID]) Next() crud.Core[M, ID] { return this.Core }

// Denied wraps ErrForbidden with what was refused.
func Denied(action Action, reason string) error {
	return fmt.Errorf("%w: %s: %s", ErrForbidden, action, reason)
}

func (this *gate[M, ID]) authorize(ctx context.Context, a Action) error {
	if this.p.Authorize == nil {
		return nil
	}
	return this.p.Authorize(ctx, a)
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

// relationScopes resolves the request-specific relation narrowing once. Keep
// this separate from narrow: writes that render a relation-hopping Scope need
// the actual declaration on the SQL builder, not merely an Option for a Core
// call.
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

// narrow builds the option that carries the policy across relation boundaries.
// A nil Option is a no-op to Apply, so a policy that declares nothing costs one
// nil check.
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

// scoped prepends the policy scope to a caller's options. Both halves of the
// policy go in front: the row filter for this table, and the narrowing that
// follows every relation this query walks. In front, because Where ANDs — a
// caller cannot subtract either of them by appending anything.
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

// writeScopes resolves both policy narrowings once for a write. A generated-key
// Save has nowhere to put either predicate, but it must still evaluate their
// resolvers: an unavailable principal is never permission to insert an
// unscoped row. Assigned-key saves reuse the same resolved values for their
// preflight and conditional statement, so a time-varying resolver cannot make
// those two phases disagree.
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

// whole cancels a caller's projection, for the reads whose rows are about to be
// handed to Inspect. Inspect is an opaque closure, so the gate cannot know which
// columns it reads; given a projected row it compares against zero values and
// believes them. That cuts both ways, and ?select= is untrusted input: under the
// tenancy policy in the package documentation every projected read became a
// denial, and a rule that hides rows by a column value was bypassed by simply
// not selecting that column.
func (this *gate[M, ID]) whole(willInspect bool, options []crud.Option) []crud.Option {
	if !willInspect || this.p.Inspect == nil {
		return options
	}
	return append(append([]crud.Option{}, options...), crud.SelectAll())
}

// inspectionRead removes every caller-controlled read shape that could make an
// Inspect loop see fewer rows than the write that follows. A bulk UPDATE or
// DELETE has no pagination, cursor, projection, preload, sort or DISTINCT
// clause; carrying any of those only into its preliminary GetAll would turn
// "inspect every victim" into "inspect this page of victims". Filters and
// relation scopes deliberately remain: they are the set the final statement
// will address.
//
// Keep this here rather than making it a public Crud option. It is not a
// request feature; it is an invariant of the gate's two-statement protocol.
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

// ---------------------------------------------------------------------------
// reads

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

// loadScoped fetches one row through the policy scope. Out-of-scope rows come
// back as ErrNotFound, never as a denial — a 403 would leak that the id exists.
// It deliberately does not run Authorize, so the caller decides which action is
// being authorised.
// loadScoped is a read that decides a write on every path but GetByID, so it
// stays on the primary: authorising against a replica that has not caught up
// checks a row as it was, not as it is.
func (this *gate[M, ID]) loadScoped(ctx context.Context, id ID, options ...crud.Option) (M, error) {
	var zero M
	scope, rel, err := this.writeScopes(ctx)
	if err != nil {
		return zero, err
	}
	return this.loadScopedWith(ctx, id, scope, rel, options...)
}

// loadScopedWith is loadScoped after a write has already resolved its policy
// narrowings. Reusing those exact values matters for the preflight/write pair:
// a resolver may depend on a principal snapshot or current time, neither of
// which is allowed to change the set between inspection and mutation.
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

// Aggregate is scoped like any other read, and it has to be spelled out rather
// than inherited: the gate embeds crud.Core, so an aggregate that fell through
// to the plain repository would answer over every row in the table. "How many"
// is as much of a disclosure as "which ones" — a count that changes when another
// tenant writes is an oracle for their data.
//
// Inspect has nothing to look at here. A summary row is not an entity, so a
// policy that authorises per row cannot express itself over one; such a policy
// refuses the whole call rather than pretending to have checked it.
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
func (this *gate[M, ID]) SaveAll(ctx context.Context, models []*M) error {
	if len(models) == 0 {
		return nil
	}
	// Inspect hooks are allowed to normalise the candidate they inspect. Batch
	// persistence, like Save and SaveOnly, must not leak that mutation to the
	// caller, so the entire guarded operation works on private copies.
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
			// The key decides whether Save is an INSERT or UPDATE only after a
			// lookup. Run both coarse permissions first so a denied caller never
			// gets that lookup as an existence oracle.
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
	// Any assigned key can conflict, even in an otherwise generated-key batch.
	// Run those rows through the conditional capability in one transaction; an
	// ordinary SaveAll is safe only when every key is database generated.
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

// Save returns a separate stored model. Work always happens on a copy, so a
// policy's Inspect hook and the storage scanner cannot change the caller's
// command object.
func (this *gate[M, ID]) Save(ctx context.Context, m *M) (M, error) {
	var zero M
	if m == nil {
		return zero, Denied(Create, "nil model")
	}
	copy := *m
	return this.save(ctx, &copy, true)
}

// SaveOnly performs the guarded write without obtaining the stored row. It
// uses a copy for the same no-mutation guarantee as Save.
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
		// See SaveAll: the lookup must not be the first observable action of a
		// caller denied either branch of an assigned-key upsert.
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
	// Inspect the incoming state too: this is what catches a row being written
	// into somebody else's scope.
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

// saveTransaction joins an executor the caller placed in the context. Executor
// is deliberately the complete foreign-transaction seam: ORMs expose wildly
// different concrete transaction types, but every statement still has to stay
// on the one object they gave us. A pool must not be passed through
// WithExecutor; doing so explicitly selects it as the operation's executor.
// Without a contextual executor vv opens its own transaction instead.
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

// saveScoped delegates an assigned-key Save to a storage core that can make the
// inspected action atomic. A nil previous row means create-only: a concurrent
// insert is a refusal, never an implicitly authorised update. A non-nil row is
// used as an optimistic snapshot so the UPDATE cannot affect a replacement the
// gate never inspected.
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

// saveTarget returns the row a Save with an id would overwrite, or nil when the
// statement is a plain insert.
//
// The lookup goes through the scope. Save is an upsert, so an invisible row
// must be distinguished internally from an unused key before any INSERT is
// attempted; otherwise a client could overwrite and re-tenant it. The caller
// sees ErrNotFound in either case. That keeps the physical-existence probe an
// integrity guard rather than an existence oracle.
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
	// Nothing visible under that id. It is still an overwrite if the row is
	// merely hidden, and only an insert if it is genuinely not there.
	hidden, err, supported := crud.ExistsUnscopedOf(this.Core, ctx, byID, crud.PrimaryOnly())
	if err != nil {
		return nil, err
	}
	if !supported {
		// An unknown physical state cannot safely become an INSERT. Refuse it as
		// absent instead of advertising that the storage core lacks this probe.
		return nil, crud.ErrNotFound
	}
	if hidden {
		return nil, crud.ErrNotFound
	}
	return nil, nil
}

// checkImmutableSave rejects a full Save that would change a field the policy
// froze.
func (this *gate[M, ID]) checkImmutableSave(meta *crud.Meta, old, next *M) error {
	if len(this.immutable) == 0 {
		return nil
	}
	for name := range this.immutable {
		// index resolved these at declaration and panicked on a name that
		// resolves to nothing, so this is the canonical spelling and meta.Field
		// cannot answer nil.
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

// snapshotPredicate pins a later statement to the complete row Inspect saw.
// A scope only says where a row may be; Inspect can decide from any field (for
// example, a Locked flag), so repeating only the scope would still let a
// concurrent state change turn an approved action into a forbidden one.
func snapshotPredicate[M any](meta *crud.Meta, m *M) (crud.Predicate, error) {
	values, err := meta.Values(m, meta.Fields)
	if err != nil {
		return nil, err
	}
	preds := make([]crud.Predicate, 0, len(meta.Fields))
	for i, f := range meta.Fields {
		preds = append(preds, crud.Eq(f.Name, values[i]))
	}
	return crud.And(preds...), nil
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
	// Resolve policy narrowings once before the check/write protocol begins. The
	// same values protect both statements, so a time-varying resolver cannot
	// inspect under one principal and mutate under another.
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
	// The scope applies here too, so an out-of-scope id is ErrNotFound rather
	// than a denial.
	//
	// PrimaryOnly, like every other check this gate makes. A read that decides a
	// write never goes to a replica ([[D-032]]) — and this one decides one: the
	// row it loads is what Inspect is shown. Taken from a lagging replica, a row
	// that has just moved out of scope still authorises the update, and the
	// UPDATE that follows lands on the primary. This was the one check that did
	// not pass it.
	//
	// Only when there is a rule to run. With no Inspect the row was loaded and
	// then not looked at — a whole round trip, on the most common policy shape
	// there is (a scope and no per-row rule), before every single update. The
	// out-of-scope-is-404 half below does not need it: the UPDATE carries the
	// same scope, so a row outside it matches nothing and the repository answers
	// ErrNotFound on its own ([[D-008]]).
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
	// And it goes into the write itself, not just the check in front of it.
	// Checking here and writing unscoped was check-then-act: a row that left
	// the scope in between was updated anyway, and the fresh copy of somebody
	// else's record was handed back to this caller with err == nil.
	return this.Core.Update(ctx, id, dataTransferObject, append([]crud.Option{crud.Where(scope), relationNarrowing(rel), crud.Where(inspected)}, options...)...)
}

// UpdateAll is the filtered write, and it is the one that most needs a gate: it
// touches rows nobody named. Everything Update checks is checked here — the
// coarse authorisation, the frozen fields, the scope in the statement's own
// WHERE — and Inspect additionally sees each row that is about to change, since
// with no id in the call there is nothing else that could stand for consent.
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
			rawID, err := this.Meta().ID(&victims[i])
			if err != nil {
				return 0, err
			}
			id, ok := rawID.(ID)
			if !ok {
				return 0, &crud.SchemaError{Model: this.Meta().Name, Field: this.Meta().PK.Name,
					Reason: fmt.Sprintf("inspected id has type %T, expected the repository id type", rawID)}
			}
			snapshots[id] = snapshot
			snapshotList = append(snapshotList, snapshot)
		}
	}

	// SQL storage exposes this narrow capability so the layer that knows the
	// dialect can split the id/snapshot pairs, preserve one soft-delete stamp and
	// put every chunk in one transaction. A transparent faults decorator forwards
	// it; an enforcing decorator must opt in rather than being bypassed.
	if n, err, ok := crud.DeleteScopedOf(this.Core, ctx, &crud.ScopedDelete[ID]{
		IDs: ids, Scope: scope, RelationScopes: rel, Snapshots: snapshots,
	}); ok {
		return n, err
	}

	// Low-level/custom cores keep the old one-statement contract. It is safe and
	// fail-closed when their own statement limit is exceeded; only a core that
	// explicitly owns atomic chunking receives the capability above.
	pk := this.Meta().PK.Name
	within := crud.And(scope, crud.InAny(pk, ids))
	if snapshots != nil {
		within = crud.And(within, crud.Or(snapshotList...))
	}
	return this.Core.DeleteAll(ctx, crud.Where(within), relationNarrowing(rel))
}

// deleteVictims reads an inspected id set in bounded pieces. These are only
// decision reads, so a statement-budget refusal may be retried as two smaller
// reads without any partial write. The final conditional deletes still carry
// the snapshots and run atomically in storage.
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
