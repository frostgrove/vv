# security — the gate

```go
import "github.com/frostgrove/vv/crud/decorators/security"
```

**Module:** root · **Depends on:** `crud`, and the standard library

Row-level security as a decorator. Multi-tenancy in one line, per-principal
scoping in SQL, and a set of refusals that are hard to get wrong because the
predicate AST is closed — a caller cannot peel a scope back off.

**Reach for it when** rows belong to somebody: a tenant, an owner, an
organisation. It sits between the transport and the repository, so no handler
ever writes `WHERE tenant_id = ?` and no handler can forget to.

---

## Multi-tenancy in one line

```go
policy := security.ScopeField[Doc, int64]("TenantID", func(ctx context.Context) (any, error) {
    t, ok := ctx.Value(tenantKey{}).(int64)
    if !ok {
        return nil, security.Denied(security.Read, "no tenant in context")
    }
    return t, nil
})

docs := Docs.Bind(db, security.Gate(policy))
```

From there, without another line anywhere:

- reads are filtered **in SQL**, so no over-fetch and no post-filter;
- a foreign id returns `crud.ErrNotFound`, never `ErrForbidden` — a 403 would
  confirm the row exists ([[D-008]]);
- `Save` into another tenant is refused, and so is overwriting a row that
  belongs to one — after inspection, an assigned-key create is INSERT-only and
  an update is pinned to the inspected row and scope **in the database**, so a
  row inserted or replaced after the probe cannot be silently overwritten;
- `TenantID` is frozen, so an update DTO naming it is rejected before any SQL
  runs;
- an unscoped `DeleteAll` is refused.

---

## The three checks

Each hook is optional, but a policy must contain at least one real rule:
`security.Gate(security.Policy{})` is a start-up error. Bind the repository
directly when it is intentionally unrestricted.

| | What it is | Cost |
|---|---|---|
| **Scope** | a predicate ANDed into every read and every scoped write | free — it is part of the statement |
| **Authorize** | coarse: may this principal do this kind of thing at all | before SQL; an assigned-key `Save` preflights both possible branches (`Create` and `Update`) |
| **Inspect** | fine: per entity, seeing the actual row | one call per row it is asked about |

```go
policy := security.Policy[Doc, int64]{
    Scope: func(ctx context.Context) (crud.Predicate, error) {
        t, err := tenantOf(ctx)
        if err != nil { return nil, err }
        return crud.Eq("TenantID", t), nil
    },
    Authorize: func(ctx context.Context, a security.Action) error {
        if a != security.Read && !isEditor(ctx) {
            return security.Denied(a, "read-only principal")
        }
        return nil
    },
    Inspect: func(ctx context.Context, a security.Action, d *Doc) error {
        if a == security.Delete && d.Locked {
            return security.Denied(a, "the document is locked")
        }
        return nil
    },
    Immutable:    []string{"TenantID", "OwnerID"},
    InspectReads: false,
}
```

Actions are `security.Read`, `Create`, `Update`, `Delete`.

**A `Scope` returning nil or an unconditional predicate is refused by default.**
This keeps a failed tenant lookup — or an accidental `crud.True()` — from
becoming a full-table read. An administrator that is intentionally unrestricted
declares that decision beside the scope:

```go
AllowUnscopedScope: true,
```

Returning an error still refuses the operation.

**`InspectReads` is off by default.** `Scope` is the cheap way to filter a list;
inspecting every returned row is a Go call per row.

**An `Inspect` decision is part of the final write.** For `Update`, `Delete`
and their bulk variants the gate pins the statement to the complete row (or
rows) it inspected. A concurrent change to an action-relevant field therefore
matches nothing instead of turning, for example, an approved unlocked delete
into a delete of a row that was locked a moment later.

**A custom `Scope` or `RelationScopes` without `Inspect` may read, but cannot write a body**:
`Save`, `SaveAll`, `InsertBatch`, `Update`, and `UpdateAll` accept values that a
predicate alone cannot validate. An existing-row write could move the row out
of its old `WHERE` scope; `InsertBatch` could create one outside it. The gate
refuses that policy shape before it queries or writes. Use `ScopeField` for the
normal tenancy case (it supplies the matching inspection), or add `Inspect` to
the custom policy. With that check, an assigned-key save is create-only on a
missing row and a snapshot-pinned, scoped update on an existing one.
`InsertBatch` is always `Create`, including for assigned keys: the gate
authorises it once, inspects every incoming row, and never turns a conflict into
an authorised overwrite. This is a deliberate fail-closed migration from older
zero/Scope-only gate behaviour.

The scope and relation-scope resolvers run before **every** save, including a
generated-key insert where no SQL `WHERE` can carry their predicates. A failed
principal lookup therefore refuses the insert; it never degrades into an
unscoped write.

---

## Crossing a relation

A scope is a `WHERE` clause, and a `WHERE` clause only constrains its own `FROM`.
A preload is a second statement against a second table, so it inherits
nothing — which is how `?preload=comments` hands back exactly the rows the scope
existed to hide ([[D-007]], [[UC-004]]).

**Narrow the far side too:**

```go
policy := security.Combine(
    security.ScopeField[Article, int64]("TenantID", tenantOf),
    security.ScopeRelationField[Article, int64]("Comments", "TenantID", tenantOf),
    security.ScopeRelationField[Article, int64]("Comments.Author", "TenantID", tenantOf),
)
```

The path is resolved at **declaration time**, so a typo fails at start-up rather
than leaking rows later, and it may be several hops.

For the general form — a narrowing that follows the rows wherever they are
reached, which is what a self-relation needs — set `RelationScopes` directly:

```go
RelationScopes: func(ctx context.Context) (*crud.RelationScopes, error) {
    t, err := tenantOf(ctx)
    if err != nil { return nil, err }
    return (*crud.RelationScopes)(nil).
        AtPath("Comments", crud.Eq("TenantID", t)).
        AtPath("Comments.Author", crud.Eq("TenantID", t)), nil
},
```

The per-table equivalent is `sqlrepo.RelationScope` on the blueprint. Where both
are declared, **both apply**.

**A `RelationScopes` callback returning nil or no rules is refused by default.**
That is just as important as a root scope: a missing relation claim must not
turn `?preload=comments` into an unrestricted relation read. An intentional
administrator bypass sets `AllowUnscopedRelationScopes: true` beside that
callback. `Combine` fails closed for each constituent relation scope as well.

---

## Ready-made policies

| Constructor | Does |
|---|---|
| `ScopeField[M, ID](field, valueFn)` | the multi-tenancy one: scope on a column, freeze it, narrow saves |
| `ScopeRelationField[M, ID](path, field, valueFn)` | the same narrowing on the far side of a relation |
| `ReadOnly[M, ID]()` | every write refused |
| `Freeze[M, ID](fields...)` | those fields may never be updated |
| `Combine[M, ID](policies...)` | ANDs scopes, merges relation narrowings, chains checks |

`Combine` is how a real policy is built: a tenant scope, a relation scope, a
freeze and a role check are four values that compose rather than one function
with four branches. Prefer it to stacking several `Gate` middlewares: one gate
can make the combined checked state atomic, while an opaque middleware stack is
refused rather than bypassed on an assigned-key save.

Every `field` and `path` above is a model field name or a relation path, and a
policy that narrows nothing because somebody renamed a column is the worst
failure this package has — it reads as protection and is not. The generated
metamodel answers both as identifiers:

```go
security.Combine(
    security.ScopeField[Article, int64](Article_.TenantID.Name(), tenantOf),
    security.ScopeRelationField[Article, int64](
        Article_.Comments.Path(), Comment_.TenantID.Name(), tenantOf),
    security.Freeze[Article, int64](Article_.TenantID.Name()),
)
```

As with a blueprint's relation scope, the *path* comes from the root's metamodel
and the *field* from the target's own — see [specs](specs.md).

## Driven by the authenticated caller

Every constructor above takes a `valueFn` — a `func(context.Context) (any,
error)` you write, over a context key you invented. That still works and is
still the escape hatch. But the value almost always *is* the caller, and
[auth](auth.md) is the vocabulary for saying so:

| Constructor | Does |
|---|---|
| `RequirePermission[M, ID](perms...)` | refuse unless the caller holds **all** of them |
| `RequireAnyPermission[M, ID](perms...)` | refuse unless the caller holds **at least one** |
| `RequireRole[M, ID](roles...)` | refuse unless the caller is in one of the roles |
| `PerAction[M, ID](map[Action]auth.Permission)` | one permission per verb; a verb the map does not name is refused |
| `ScopeAttr[M, ID](field, claim)` | `ScopeField` narrowed by a claim off the principal |
| `ScopeRelationAttr[M, ID](path, field, claim)` | the same across a relation |
| `ScopeSubject[M, ID](field)` | narrow to rows the caller owns |
| `ScopeRelationSubject[M, ID](path, field)` | the same across a relation |
| `InspectOwner[M, ID](allow)` | a row-level check that sees the principal and the row |

```go
policy := security.Combine(
    security.PerAction[Article, int64](map[security.Action]auth.Permission{
        security.Read:   "article:read",
        security.Create: "article:write",
        security.Update: "article:write",
        security.Delete: "article:delete",
    }),
    security.ScopeAttr[Article, int64]("TenantID", "tenant"),
)
articles := Articles.Bind(db, security.Gate(policy))
```

`ScopeAttr` **wraps `ScopeField`** rather than reimplementing it, so it inherits
the row check and the frozen column. That matters more than it sounds: a
principal-driven scope written by hand is read-only unless it also supplies
`Inspect`; the gate rejects body writes before SQL instead of treating a read
narrowing as a create policy. `ScopeAttr` supplies that inspection and is the
ordinary tenant-safe declaration.

Four things that are easy to get wrong and are decided here:

- **A claim the principal does not carry is a denial, not a zero value.** A
  missing tenant must never compile to `WHERE tenant_id = 0`, which matches
  nothing on most schemas and everything on one where 0 is a real tenant.
- **An absent principal is a 401 from every one of them, with no statement
  executed.** Nothing has been decided yet, so it is not a 403 — and it is
  [[UC-004]]'s guarantee 16.
- **`PerAction` refuses a verb it does not name**, even for a caller holding
  every permission in the map. A verb added to the seam later is refused rather
  than inherited ([[D-030]]).
- **The two quantifiers disagree about the empty case.** Naming no permission in
  `RequirePermission` refuses nothing, so a list built from configuration that
  happens to be empty adds no rule; naming none in `RequireAnyPermission`
  refuses everything, because "any of nothing" is not satisfiable.

Getting a principal *into* the context is a transport's job — see
[authnet](authnet.md), [authgin](authgin.md), [authfiber](authfiber.md) or
[authgrpc](authgrpc.md). The import runs one way: this package knows about
`auth`, and `auth` knows nothing about a repository ([[D-055]]).

## Two permissions that are separate on purpose

```go
AllowUnscopedDeleteAll: false   // default
AllowUnscopedUpdateAll: false   // default
```

`DeleteAll` and `UpdateAll` are refused when neither the policy nor the caller
narrowed the statement. They are two flags rather than one because rewriting
every row in a table is not something a policy should inherit from having
allowed the table to be emptied.

## Errors

`security.ErrForbidden` wraps `crud.ErrForbidden`, so the transport maps it to
403 without importing this package. `security.Denied(action, reason)` builds one
with a reason attached.

**Out of scope is 404, not 403** ([[D-008]]). A 403 on a row the caller may not
see confirms that the row exists, which is the leak the scope was for. A refusal
the caller *could* have avoided — writing into another tenant, updating a frozen
field — is a 403, because they already know the row.

## See also

- [sqlrepo](sqlrepo.md) — `Scope` and `RelationScope` for the per-table form
- [probe](probe.md) — the probe is scope-aware and reads `security.Policy`
- [[UC-004]] isolate tenants · [[FL-007]] a read through the gate · [[FL-008]] a write through it
- [[D-007]] narrowing crosses a relation only when declared · [[D-008]] out of scope is 404
