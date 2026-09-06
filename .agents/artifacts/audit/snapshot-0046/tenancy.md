# tenancy — one tenant, verified, all the way down

```go
import (
    "github.com/frostgrove/vv/tenancy"            // the core: a verified scope
    "github.com/frostgrove/vv/tenancy/tenancyrow" // and one package per seam you adapt
)
```

**Module:** root · **The core depends on:** `crud` and the standard library

An optional extension for a deployment that serves many tenants from one process.
The host verifies who is calling; this turns that into a **verified scope**, and
the scope into a narrowed statement, an object namespace, a cache partition, a
durable job identity or one selected database — without a tenant argument
appearing anywhere in business code.

**Reach for it when** rows, objects, cached values and background work all belong
to a customer organisation, and the failure you cannot afford is the request that
forgot which one.

**It is optional in the way that matters.** Nothing in `crud`, `port`, `storage`,
`cache`, `jobs` or `auth` imports it. A deployment that does not import it
compiles none of it ([[D-116]]).

---

## One core, one package per seam

The core is what a deployment takes to have tenants at all: an authority, scopes,
admission, grants and the seal that carries a tenant into a durable record. Every
seam it can adapt is a package beside it, so importing one costs that seam and
nothing else — `tenancyrow` is the only one that reaches `security`, and through
it `auth` and `errs`.

| Package | What it gives you | What it costs you |
|---|---|---|
| `tenancy` | the authority, the scope, admission, grants, the durable seal | `crud` |
| `tenancy/tenancyrow` | shared-row narrowing: a `security.Policy`, and the middleware over it | `crud/decorators/security` |
| `tenancy/tenancydb` | one database per tenant: the directory and the lease | `crud` |
| `tenancy/tenancyjobs` | durable work: the capture at enqueue and the identity at execution | `jobs` |
| `tenancy/tenancystorage` | an object namespace, and a store bound to it | `storage` |
| `tenancy/tenancycache` | a cache key that carries its scope, and the partition it becomes | `cache` |

The direction is one-way and the weight is measured rather than asserted:
`TestNoTenancyPackageCostsMoreThanTheSeamItNames` fails if the core reaches a
seam, or if an adapter reaches a second one ([[D-116]]).

---

## The shape

```go
authority := tenancy.Must(tenancy.Spec{Resolver: controlPlane})

invoices := Invoices.Bind(database, tenancyrow.Repository[Invoice, int64](authority,
    tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil,
        tenancyrow.Relation{Path: "Lines", Field: "TenantID"})))
```

On the request path, once the host has authenticated:

```go
ctx, err := authority.Bind(ctx, tenancy.ClassWrite)
```

And then nothing else changes. `invoices.GetAll(ctx)` returns this tenant's rows,
`invoices.Save(ctx, &Invoice{...})` stamps the owner, and a row belonging to
somebody else is `crud.ErrNotFound` rather than a 403 that would confirm it
exists ([[D-008]]).

---

## The authority is what the deployment injects

```go
type Resolver interface {
    Resolve(ctx context.Context) (Resolution, error)
    Lookup(ctx context.Context, reference Reference) (Resolution, error)
}

type Resolution struct {
    Reference Reference
    Lifecycle Lifecycle
    Epoch     Epoch
}
```

`Resolve` answers for the caller of the current request; `Lookup` answers for a
named tenant, which is what durable work and cohort jobs use. It returns **plain
data** — never a `Scope`. That is deliberate: if application code could return the
type the data plane accepts, the type would be constructible by application code
([[D-117]]).

A `Scope` therefore has no exported constructor. A test builds one the way
production does:

```go
authority := tenancy.Must(tenancy.Spec{
    Resolver: tenancy.Fixed{Reference: ref, Lifecycle: tenancy.Active, Epoch: epoch},
})
```

`tenancy.Fixed` is an ordinary resolver, not a forge. There is no test-only
constructor to leak into production.

### Lifecycle admission is a whitelist, per operation class

```go
tenancy.Spec{
    Resolver:  controlPlane,
    Admission: tenancy.Admit(tenancy.ClassRead, tenancy.Active, tenancy.Suspended).
        Merge(tenancy.Admit(tenancy.ClassWrite, tenancy.Active)).
        Merge(tenancy.Admit(tenancy.ClassDurable, tenancy.Active)),
}
```

The default is `Active` for all three classes. Everything else — including a
lifecycle value no version of this package declares — refuses. A deployment that
wants a suspended tenant to remain readable says so, and its writes still refuse.

Admission is also checked for consistency at construction: a state admitted for
writing or for durable work but not for reading is refused, naming the state.
Every mutating verb resolves its narrowing predicate for the read class first, so
such a policy would mean something other than what it says.

### `Origin` fences a deployment

`Spec.Origin` goes into every scope's binding, so a scope that travelled from
another environment does not verify here.

### How fresh a carried scope is

By default a scope is verified once, at `Bind`, and its lifecycle and generation
hold until the next explicit boundary — the next `Bind`, a durable handler entry,
a cohort member. Asking the control plane once per statement would make it a hot
dependency of every request.

```go
tenancy.Spec{Resolver: controlPlane, Revalidate: true}
```

`Revalidate` buys the other trade: every verb re-asks the resolver for the
tenant, refuses `ErrStale` if the generation moved and `ErrInactive` if the
lifecycle no longer admits the class. A deployment that must stop a deleted
tenant mid-request sets it; one that cannot afford the round trip declares "one
`Bind`" as its window and says so in its own runbook.

---

## Ownership is a strategy, not a column

```go
tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil)     // the row carries its owner
tenancyrow.Through[Line]("Owner", "TenantID", nil)              // the row is owned through a relation
```

| | What it narrows with | Create | Frozen |
|---|---|---|---|
| `Column` | `TenantID = ?` on the row, plus a declared narrowing for each relation named in the call | `Derive` stamps the owner when the field is empty; `Validate` requires the caller to have supplied the right one. Either way a foreign owner is refused | the ownership column |
| `Through` | a dotted path into the owner, which compiles to the correlated `EXISTS` of [[FL-005]] | refused — the owner it would attach to has not been read | the key that points at the owner, because repointing it moves the row without any of its own columns changing |

The third argument maps a `Reference` to the column's value; `nil` means the
reference's own string. A deployment whose ownership column is a `BIGINT` or a
UUID supplies the conversion:

```go
tenancyrow.Column[User]("TenantID", tenancyrow.Derive, func(r tenancy.Reference) (any, error) {
    return strconv.ParseInt(r.Value(), 10, 64)
})
```

`Ownership` is an interface — `Narrow`, `Relations`, `NarrowsRelations`, `Apply`
and `Frozen`. A resource whose ownership is neither of these two shapes
implements it rather than bending one of them. `NarrowsRelations` is declared
rather than inferred: a strategy is asked whether it narrows relations, never
probed with a made-up tenant, because a probe a strategy answered with an error
would switch the narrowing off in exactly the silence this is here to prevent.

**Declare every tenant-owned relation.** A preload is a second statement against
a second table and the root's `WHERE` does not reach it ([[D-007]]); the foreign
key is ordinary business data the gate does not freeze. So a tenanted parent with
an undeclared relation hands back the far tenant's rows to anyone who can set the
key — and that is the failure this framework's decision docs were written to
prevent. `tenancyrow.Relation{Path, Field}` in the `Column` call is the declaration,
resolved at construction so a typo panics at wiring. A relation you do not
declare is read whole, deliberately and visibly.

### Composing with a policy you already have

`tenancyrow.Policy` returns a `security.Policy`, so an application that already has a
gate combines rather than stacks:

```go
gate := security.Gate(security.Combine(
    tenancyrow.Policy[Invoice, int64](authority, ownership),
    security.RequirePermission[Invoice, int64]("invoices:write"),
))
```

`security.Combine` ANDs the scopes, chains the inspections, unions the frozen
columns and keeps both unscoped-bulk refusals. **Prefer it to two gates in one
chain**: a scoped upsert is an exact capability of the core directly below, and a
gate does not forward it, so two stacked gates cannot perform an assigned-key
`Save`.

---

## The other seams

```go
// durable work — capture at enqueue, re-derive at execution
provider, err := tenancyjobs.ContextProvider(authority, provenance, epoch)
restorer, err := tenancyjobs.IdentityRestorer(authority)

// objects — a fixed-width namespace, generation included
store, err := tenancystorage.Store(ctx, authority, "invoices", tenancy.ClassWrite, backend)

// cache — the key carries the scope, because a cache partitions on its key
values := tenancycache.Partitioned[string](namespace)
key, err := tenancycache.Keyed(ctx, authority, tenancy.ClassRead, "invoice:1")
```

Both go through the authority rather than taking a scope value, because holding a
scope says an authority minted it once — not that this authority admits this
class of work now. A suspended tenant does not get to name its bucket to write
into, a scope another authority minted is refused here exactly as the row seam
refuses it, and a read-only cohort grant cannot write objects. There is no
scope-taking variant of either: the unchecked mapping is unexported, because the
value it would need is one public `From(ctx)` away.

**The durable seam needs a key.** `Spec.DurableKey` (32 bytes or more, the same
value in the producer and the worker) authenticates the reference the producer
writes into the record. Without it `ContextProvider` and `IdentityRestorer` refuse to be
constructed, and that is the point: the queue table is durable, shared and
writable by whatever else reaches that database, so an unauthenticated reference
in a record makes queue write access into tenant impersonation.

**The durable record is a reference, never an authority.** The producer writes the
tenant and the generation under a MAC; the worker verifies the MAC, and then
disbelieves the record anyway — it asks the control plane what is true *now*. A
tenant suspended, deleted or restored since the work was enqueued does not reach
its handler, and a record written by anything that does not hold the durable key
reaches nothing at all.

**Namespaces and partitions are digests, not references.** A bucket path is quoted
in provider errors and access logs, which is exactly where a tenant name must not
be; and a fixed-width namespace cannot be a prefix of another tenant's however the
references were named. The generation is inside the digest, so a restored tenant
reads none of the previous generation's objects or cached values.

---

## One database per tenant

```go
directory, err := tenancydb.NewDirectory(tenancydb.DirectorySpec{
    Authority: authority,           // mandatory — the directory re-checks admission itself
    Sources:   sources,             // your factory: a verified scope -> one crud.Source
    MaxCached: 64,                  // mandatory
    TTL:       10 * time.Minute,    // mandatory
    Fence:     itIsReallyThisTenants, // optional: prove the source is the right one
    OpenTimeout: 30 * time.Second,   // optional; this is the default
})

lease, err := directory.Borrow(ctx, tenancy.ClassWrite)
defer lease.Release()
repository := Invoices.Bind(lease.Source(), ...)
```

The directory owns the contract and the bounds; the driver and the secret store
are the application's decision. Selection happens before any statement and is
keyed on the tenant **and** the generation, so a binding cached before a rotation
is never handed to the generation that replaced it. There is no default and no
last-used fallback. Eviction unlinks a binding immediately and closes its source
only when the last borrower gives the lease back.

`MaxCached` and `TTL` are required rather than defaulted, because an unbounded
per-tenant pool is how one tenant takes a deployment down. `Authority` is
required for a sharper reason: `Borrow` re-asks whether the carried scope's
lifecycle admits *this class* of work. Under database-per-tenant there is no row
policy underneath to catch a suspended tenant that bound for a read and then
opened its database to write in.

**What it does not do:** multiplex one repository across databases per request. A
repository is bound to one source ([[UC-012]]); the directory tells you which one,
and binding is yours.

---

## Work across tenants

```go
grant, err := authority.Accept(purpose, cohort, deadline, tenancy.ClassRead)
outcomes, err := authority.Each(ctx, grant, tenancy.ClassWrite, func(ctx context.Context) error {
    return billing.Roll(ctx)
})
```

Issuing a grant is control-plane policy; an authority **accepts** one, bounds it
and refuses to widen it. A grant names the *classes* of work it permits, and the
classes travel with the work: the read-only grant above cannot write, whatever
the purpose is called, because a purpose is a label and not a permission. There
is no wildcard cohort and no empty one. Each member
is entered on its own, with its lifecycle re-checked; one member that became
non-active is recorded in `outcomes` and skipped rather than failing the run, so a
partially applied run is resumable instead of repeated.

---

## Errors

Every refusal is a sentinel compared with `errors.Is`, and none of them carries a
tenant, a database, a key or a resolver's own text.

```go
tenancy.ErrNoScope    tenancy.ErrUntrusted     tenancy.ErrInactive
tenancy.ErrStale      tenancy.ErrIncompatible  tenancy.ErrUnmapped
tenancy.ErrGrantRequired                       tenancy.ErrPinned
tenancy.ErrCapacity   tenancy.ErrUnavailable   tenancy.ErrMalformed
```

The first seven wrap `crud.ErrForbidden`, so a transport answers 403 without
importing this package. `ErrPinned` wraps `crud.ErrConflict`. `ErrCapacity` and
`ErrUnavailable` deliberately wrap neither: a full pool is not an authorisation
failure and should not page the security team.

A resolver that fails for its own reasons produces `ErrUnavailable` and nothing
else — its message names the tenant, the database and often the credential, and
that text does not travel. Log it in the resolver, which is your code and owns
that channel.

`tenancy.OutcomeFor(err)` maps any of them to one of twelve closed constants, which
is what a dashboard or a metric label may carry.

---

## What it does not cover

- **`Tx`.** The gate inherits it rather than overriding it, for the reason
  [[D-030]] writes down: it touches no row itself, and everything the closure does
  reaches the database through the same gated repository. So `orders.Tx(ctx, fn)`
  with no scope opens a transaction and the first verb inside it refuses. If your
  code opens the unit of work before it resolves the tenant, resolve first.
- **A `Save` with an assigned key must carry the owner.** The whole row is the
  write, and the ownership column is frozen, so a zero owner reads as a change to
  it. `Derive` stamps a create, not an overwrite.
- **A raw statement, `crud.UnsafeExecFor`, or a repository bound without the
  middleware.** The narrowing is a predicate this composes in, not a property of
  the connection.
- **A pagination cursor minted under another tenant's scope**, which is the same
  class of oracle as a unique-constraint create.

## See also

- [security](security.md) — the gate this returns, and every verb it already covers
- [crud](crud.md) — the middleware chain, and the exact-outer capability rules
- [jobs](../../ai/flows/FL-033-a-request-becomes-a-tenant-bound-statement.md) — the durable half of the flow
- [[D-116]] why this is a package and not a module · [[D-117]] why a scope cannot be manufactured
