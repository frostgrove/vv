# security — the gate

```go
import "github.com/shardit-io/vv/repo/decorators/security"
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
  belongs to one — the existence probe deliberately runs **without** the
  narrowing, so an invisible row cannot be silently overwritten;
- `TenantID` is frozen, so an update DTO naming it is rejected before any SQL
  runs;
- an unscoped `DeleteAll` is refused.

---

## The three checks

All optional, and independent.

| | What it is | Cost |
|---|---|---|
| **Scope** | a predicate ANDed into every read and every scoped write | free — it is part of the statement |
| **Authorize** | coarse: may this principal do this kind of thing at all | one call per operation, before any SQL |
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

**`Scope` returning nil means unrestricted** — which is what an admin principal
should return. Returning an error refuses the operation.

**`InspectReads` is off by default.** `Scope` is the cheap way to filter a list;
inspecting every returned row is a Go call per row.

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

The per-table equivalent is `basic.RelationScope` on the blueprint. Where both
are declared, **both apply**.

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
with four branches.

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

- [basic](basic.md) — `Scope` and `RelationScope` for the per-table form
- [probe](probe.md) — the probe is scope-aware and reads `security.Policy`
- [[UC-004]] isolate tenants · [[FL-007]] a read through the gate · [[FL-008]] a write through it
- [[D-007]] narrowing crosses a relation only when declared · [[D-008]] out of scope is 404
