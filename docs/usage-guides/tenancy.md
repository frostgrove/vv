# Serve many tenants from one deployment

The [module reference](../modules/en/tenancy.md) describes the API; this
describes the adoption — what you write, in what order, and what you must decide
along the way.

Everything below is the **shared-row** topology: one database, one set of tables,
an ownership column. That is the profile this repository publishes and tests
against a live database. [One database per tenant](#one-database-per-tenant) is a
separate section at the end.

---

## Part I — what you get (read this first)

Your business code does not change. It never names a tenant:

```go
invoices, err := repository.GetAll(ctx)          // this tenant's rows, only
stored, err := repository.Save(ctx, &Invoice{})  // owner stamped from the scope
one, err := repository.GetByID(ctx, id)          // somebody else's id: ErrNotFound
```

That last line is the one worth pausing on. A row belonging to another tenant is
`crud.ErrNotFound`, not a 403 — a refusal that confirms the row exists is an
enumeration oracle ([[D-008]]).

The narrowing reaches every verb, not just the obvious ones: `Count` and `Exists`
do not see foreign rows, `Aggregate` does not sum across them, `UpdateAll` and
`DeleteAll` refuse to run unscoped, a create into another tenant is refused, and
moving a row between tenants is refused because the ownership column is frozen.

What you write to get it is four things, and only the fourth is per-model:

```go
// 1. your control plane, as an ordinary interface implementation
// 2. one authority, at start-up
authority, err := tenancy.New(tenancy.Spec{Resolver: controlPlane{...}})
// 3. one middleware, after authentication
ctx, err := authority.Bind(ctx, tenancy.ClassWrite)
// 4. one line per tenant-owned model, at the composition root
invoices := Invoices.Bind(database, tenancyrow.Repository[Invoice, int64](authority,
    tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil)))
```

The rest of this guide is those four steps in detail, then the seams around them
— background jobs, object storage, caching, cross-tenant admin work — and then
[what this does not protect](#what-this-does-not-protect), which is worth reading
before you rely on any of it.

---

## Part II — how to set it up

### What you write, in order

1. A **`Resolver`** — your control plane. Answers "who is this request's tenant"
   and "tell me about this named tenant".
2. An **`Authority`** at start-up, built from that resolver.
3. One **middleware** that turns the authenticated caller into a bound context.
4. One **`tenancyrow.Repository`** per tenant-owned model, at the composition root.

Nothing below that mentions a tenant. That is the whole point: business code
keeps calling `invoices.GetAll(ctx)`.

---

### 1. The resolver, and the values it must return

`Resolve` answers for the caller of the current request. `Lookup` answers for a
tenant named by something durable — a queue row, a cohort job.

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

It returns **plain data, never a `Scope`** — if application code could return
the type the data plane accepts, the type would be constructible by application
code ([[D-117]]).

#### Building the three values

None of them has a bare constructor; each is parsed, and each refuses input it
cannot represent.

```go
reference, err := tenancy.ParseReference("acme")     // 1..128 bytes, no spaces,
                                                      // no control characters
epoch, err := tenancy.NewEpoch(7)                     // uint64, non-zero
purpose, err := tenancy.ParsePurpose("monthly-billing") // 1..64 bytes, for grants
```

**The reference** is compared byte for byte and never folded: whether `Acme` and
`acme` are one tenant is your control plane's answer, not this library's. Store
whatever your directory uses as the primary key — a slug, a ULID, a numeric id
rendered as a string.

**The epoch is a generation counter, not a timestamp.** Bump it when a tenant is
restored from backup or moved. Only equality is ever compared, never ordering.
It is what fences a restored tenant from the previous generation's cached
values, object namespaces and queued jobs. If your control plane's generation is
a UUID, you must fold it to a `uint64` yourself — and be aware that a fold can
collide, which weakens exactly that fence.

**The lifecycle** is one of `Provisioning`, `Active`, `Suspended`, `Migrating`,
`Deleting`, `Deleted`. It is a closed set; a state your product has and this list
does not must be mapped onto one of these, and a value outside the set is
refused as untrusted.

#### A worked resolver

The natural source of the tenant on an authenticated request is a claim on the
principal your auth middleware already verified.

```go
type controlPlane struct{ tenants *tenantDirectory }

func (this controlPlane) Resolve(ctx context.Context) (tenancy.Resolution, error) {
    principal, ok := auth.PrincipalFrom(ctx)
    if !ok {
        return tenancy.Resolution{}, tenancy.ErrNoScope
    }
    claim, ok := principal.Attr("tenant")
    if !ok {
        return tenancy.Resolution{}, tenancy.ErrNoScope
    }
    named, ok := claim.(string)
    if !ok {
        return tenancy.Resolution{}, tenancy.ErrMalformed
    }
    reference, err := tenancy.ParseReference(named)
    if err != nil {
        return tenancy.Resolution{}, err
    }
    return this.Lookup(ctx, reference)
}

func (this controlPlane) Lookup(ctx context.Context, reference tenancy.Reference) (tenancy.Resolution, error) {
    record, err := this.tenants.byReference(ctx, reference.Value())
    if err != nil {
        return tenancy.Resolution{}, err
    }
    return tenancy.Resolution{
        Reference: reference,
        Lifecycle: record.lifecycle,
        Epoch:     record.epoch,
    }, nil
}
```

Four things this shape gets right, and you should keep:

- **It never trusts a value the caller controls.** The tenant comes from a
  verified claim, not from a `X-Tenant` header the client sets. If you must
  accept a client-supplied hint — a subdomain, a path segment — resolve it and
  then check it against the principal, and refuse when they disagree.
- **`Lookup` must echo the reference it was given.** The authority compares
  `resolution.Reference` with the reference it asked about and refuses
  `ErrUntrusted` on a mismatch, which stops a control plane from substituting one
  tenant for another. A directory that canonicalises (lower-cases, trims) will
  fail every lookup — canonicalise *before* `ParseReference`, not after.
- **Both methods are called concurrently**, from every in-flight request. Make
  them safe for that, and cache if your directory is remote.
- **Its error text never travels.** Anything it returns is collapsed to
  `ErrUnavailable` unless it is a deliberate sentinel, because a control-plane
  error names the tenant, the database and often the credential. Log it here —
  this is your code and it owns that channel.

#### Public and unauthenticated routes

A route with no tenant returns `ErrNoScope` from `Resolve`, and you simply do not
bind on that route. Nothing forces a tenant onto a request; the refusal happens
at the first tenant-owned verb, not at the middleware.

---

### 2. The authority, once, at start-up

```go
authority, err := tenancy.New(tenancy.Spec{
    Resolver:   controlPlane{tenants: directory},
    Origin:     "invoices.eu-west-1",
    DurableKey: secrets.MustGet("TENANCY_DURABLE_KEY"), // >= 32 bytes
    Admission: tenancy.Admit(tenancy.ClassRead, tenancy.Active, tenancy.Suspended).
        Merge(tenancy.Admit(tenancy.ClassWrite, tenancy.Active)).
        Merge(tenancy.Admit(tenancy.ClassDurable, tenancy.Active)),
})
```

| Field | What it buys you |
|---|---|
| `Origin` | A per-deployment fence. It goes into every scope binding and every durable seal, so a scope or a queue record from staging does not verify in production. Set it, and make it different per environment. |
| `DurableKey` | Authenticates the tenant reference written into durable records. Required before you can enqueue tenant-owned jobs at all. **The same value in the producer and the worker**, from your secret store. |
| `Admission` | A whitelist, per operation class. See below. |
| `Revalidate` | Re-asks the control plane on every verb. See [How fresh](#how-fresh-is-a-bound-tenant). |

#### Admission: say every class you mean

Admission answers "which lifecycle states may do which class of work". The three
classes are `ClassRead`, `ClassWrite` and `ClassDurable` (background work).

**Write out all three classes explicitly.** `Admit` builds one class at a time
and `Merge` combines them; a `Spec.Admission` that ends up empty is treated as
"not configured" and replaced by the permissive default of `Active` for all
three. That means `tenancy.Admit(tenancy.ClassRead)` — a class with no state
list — does *not* give you a read-only deployment; it gives you the default.
State the states, and assert what you got in a start-up test:

```go
if authority.Admits(tenancy.ClassWrite, tenancy.Suspended) {
    log.Fatal("a suspended tenant must not write")
}
```

The default, if you set no `Admission` at all, is `Active` for all three classes.

#### How fresh is a bound tenant?

By default a scope is verified **once**, at `Bind`, and holds until the next
explicit boundary. Asking the control plane on every statement would make it a
hot dependency of every request.

Set `Revalidate: true` and every verb re-asks: a tenant suspended or deleted
mid-request stops at the very next verb. The cost is real and larger than "one
call per statement" — verbs that inspect rows one at a time ask once per row, so
a hundred-row bulk delete is a hundred-and-one lookups, and they happen inside
the transaction. Cache in your resolver if you turn this on.

---

### 3. Where the tenant enters the request

Bind **after** authentication and **before** any handler that touches
tenant-owned data. The scope lives in the context; nothing else propagates it.

#### net/http

```go
func tenantBound(authority *tenancy.Authority, options ...porthttp.RenderOption) func(http.Handler) http.Handler {
    renderer := authhttp.RendererFor(options)
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ctx, err := authority.Bind(r.Context(), tenancy.ClassWrite)
            if err != nil {
                authhttp.Refuse(w, r, renderer, err)
                return
            }
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

handler := authnet.Middleware(guard)(tenantBound(authority)(routes))
```

#### Fiber

```go
app.Use(authfiber.Middleware(guard))
app.Use(func(c fiber.Ctx) error {
    ctx, err := authority.Bind(c.Context(), tenancy.ClassWrite)
    if err != nil {
        return err
    }
    c.SetContext(ctx)
    return c.Next()
})
```

Three constraints worth stating plainly:

- **Order matters.** `Bind` runs after the auth middleware, because the resolver
  reads the principal that middleware established. Reversed, every request is
  `ErrNoScope`.
- **Bind for the widest class the route needs.** A read-only route may bind
  `ClassRead`; a route that writes must bind `ClassWrite`, because admission is
  checked per class at the verb.
- **A unit of work is pinned.** Once a context is bound, binding it again to a
  *different* tenant is `ErrPinned`, not a switch. This is what makes a
  transaction's atomicity mean something. If you open a transaction before you
  resolve the tenant, the first verb inside it refuses — resolve first.

---

### 4. The repositories

One line per tenant-owned model, at the composition root.

```go
invoices := Invoices.Bind(database, tenancyrow.Repository[Invoice, int64](authority,
    tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil,
        tenancyrow.Relation{Path: "Lines", Field: "TenantID"})))
```

#### Mapping a reference to your column's type

The third argument converts a `Reference` into the column's value. `nil` means
"the reference's own string", which is right only when your ownership column is
a string.

```go
// tenant_id BIGINT
tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, func(r tenancy.Reference) (any, error) {
    id, err := strconv.ParseInt(r.Value(), 10, 64)
    if err != nil {
        return nil, tenancy.ErrMalformed
    }
    return id, nil
})
```

**Return the column's own type, and never nothing.** The value is checked against
the column before it reaches a statement, so a mismatch is a refusal you get
immediately rather than a conversion: Go would turn an `int` into a `string` by
reading it as a rune, and `65` against a text column would narrow on `"A"`.
Absence is refused for the same reason — `crud.Eq` turns it into
`tenant_id IS NULL`, which matches every unowned row instead of none.

A `Derive` create needs a plain column. A pointer or `Opt` ownership column cannot
be stamped, so declare `Validate` and supply the owner yourself.

#### `Derive` or `Validate`

`Derive` stamps the owner on create when the field is empty. `Validate` requires
the caller to have supplied the right one. Either way a foreign owner is refused.
Use `Derive` unless your API genuinely accepts the owner from the client and you
want it checked.

#### Declare every tenant-owned relation

A preload is a second statement against a second table, and the root's `WHERE`
does not reach it. **An undeclared relation is read whole, deliberately** — so a
tenanted parent with an undeclared `has_many` hands back the other tenant's rows
to anyone who can request that preload.

```go
tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil,
    tenancyrow.Relation{Path: "Lines", Field: "TenantID"},
    tenancyrow.Relation{Path: "Payments", Field: "TenantID"})
```

Paths are resolved at construction, so a typo panics at wiring rather than
leaking at runtime. Nothing checks that you declared *all* of them — when you add
a relation to a tenanted model, add its declaration in the same change.

#### Ownership through a relation

For a row that carries no owner of its own:

```go
tenancyrow.Through[Line]("Invoice", "TenantID", nil)
```

This compiles to a correlated `EXISTS` over the path. Two limits:

- **Creates are refused.** The owner the row would attach to has not been read,
  so it cannot be judged from the row. Give such a model its own ownership
  column, or create it through the parent.
- **Point it at a to-one relation.** A to-many path is refused at wiring: it would
  mean "at least one child is mine", which is not ownership — a row whose children
  belong to two tenants would answer to both, and the key it would freeze is the
  row's own identity rather than a link to an owner.

#### Composing with a gate you already have

```go
gate := security.Gate(security.Combine(
    tenancyrow.Policy[Invoice, int64](authority, ownership),
    security.RequirePermission[Invoice, int64]("invoices:write"),
))
```

Prefer `security.Combine` to stacking two gates in one chain: a scoped upsert is
an exact capability of the core below, a gate does not forward it, and two
stacked gates cannot perform an assigned-key `Save`.

---

### 5. Migrations and the schema

Shared-row tenancy needs **no separate migration story**: one schema, one set of
tables, migrated exactly as
[the migration guide](migrations.md) describes. What it needs is the ownership
column on every tenant-owned table, and the right indexes.

#### Indexes

Every statement the extension produces is prefixed with `tenant_id = ?` — reads,
page-total `COUNT`s, and the second statement of each declared preload. So:

- **Make `tenant_id` the leading column of every index you already have** on a
  tenant-owned table. An index on `(created_at)` is close to useless once every
  query also filters the tenant; `(tenant_id, created_at)` is the one you want.
- **Give every tenant-owned table an index leading with `tenant_id`.** Without it
  each list becomes a full scan filtered by tenant.
- **Extend unique constraints with the tenant.** A globally unique
  `invoices.number` means one tenant's insert can collide with another's — which
  both breaks a legitimate write and discloses that the other row exists. Make it
  `UNIQUE (tenant_id, number)`.
- **Foreign keys do not carry the tenant.** A child row's `invoice_id` can point
  at another tenant's parent as far as the database is concerned; the declared
  relation narrowing is what stops it being *read*. Composite foreign keys
  including `tenant_id` are the defence-in-depth option if you want it.

---

### 6. Testing a tenant-owned repository

There is no scope constructor, so a test builds one the way production does —
through an authority over a canned resolver.

```go
func boundTo(t *testing.T, named string) (*tenancy.Authority, context.Context) {
    t.Helper()
    reference, err := tenancy.ParseReference(named)
    if err != nil {
        t.Fatal(err)
    }
    epoch, err := tenancy.NewEpoch(1)
    if err != nil {
        t.Fatal(err)
    }
    authority := tenancy.Must(tenancy.Spec{
        Resolver: tenancy.Fixed{Reference: reference, Lifecycle: tenancy.Active, Epoch: epoch},
    })
    ctx, err := authority.Bind(context.Background(), tenancy.ClassWrite)
    if err != nil {
        t.Fatal(err)
    }
    return authority, ctx
}
```

`tenancy.Fixed` is an ordinary resolver, not a forge — there is no test-only
constructor that could leak into production.

**Write the negative test, and give it a control.** The test that matters is not
"my tenant sees its rows" but "the other tenant's row is `crud.ErrNotFound`",
with a control asserting the other tenant *does* see it. Assert on the bound
argument, not on the SQL text: a test that greps the statement for `tenant_id`
passes even when the predicate names the wrong tenant.

---

### 7. Work that is not one request

#### A job for one tenant

```go
provider, err := tenancyjobs.ContextProvider(authority, provenance, epoch)
restorer, err := tenancyjobs.IdentityRestorer(authority)
```

The producer writes the tenant and generation into the record under a MAC; the
worker verifies it and then **disbelieves it anyway**, asking the control plane
what is true now. A tenant suspended, deleted or restored since the work was
enqueued does not reach its handler. Both refuse to be constructed without
`Spec.DurableKey`, because an unauthenticated reference in a queue table makes
queue write access into tenant impersonation.

#### Work across every tenant

```go
grant, err := authority.Accept(purpose, cohort, deadline, tenancy.ClassRead)
outcomes, err := authority.Each(ctx, grant, tenancy.ClassRead, func(ctx context.Context) error {
    return billing.Roll(ctx)
})
```

Issuing a grant is your control plane's policy; the authority accepts one, bounds
it and refuses to widen it. The classes travel with the work, so a read-only
grant cannot write whatever the purpose is called. There is no wildcard cohort.

Each member is entered on its own with its lifecycle re-checked, and a member that
became non-active is recorded in `outcomes` and skipped rather than failing the
run — so a partially applied run is resumable. **Check `outcomes`**: `Each`
returns a nil error when every member failed individually. Count the
`OutcomeOk`s.

#### Objects and cached values

```go
store, err := tenancystorage.Store(ctx, authority, "invoices", tenancy.ClassWrite, backend)

values := tenancycache.Partitioned[string](namespace)
key, err := tenancycache.Keyed(ctx, authority, tenancy.ClassRead, "invoice:1")
```

Both go through the authority rather than taking a scope, because holding a scope
says an authority minted it once, not that it admits this class of work now.
Namespaces and partitions are digests that include the generation, so a restored
tenant reads none of the previous generation's objects or cached values.

Two traps: build the handle **inside** the unit of work that uses it, since the
admission check happens when the handle is built and not when you write through
it; and use `tenancycache.Partitioned`, never `cache.Global`, over a tenant key —
the latter compiles and silently shares one address space across tenants.

---

### 8. Observability

Every refusal is a sentinel you compare with `errors.Is`, and none carries a
tenant, a database or a resolver's text.

```go
tenancy.ErrNoScope   tenancy.ErrUntrusted    tenancy.ErrInactive
tenancy.ErrStale     tenancy.ErrIncompatible tenancy.ErrUnmapped
tenancy.ErrGrantRequired                     tenancy.ErrPinned
tenancy.ErrCapacity  tenancy.ErrUnavailable  tenancy.ErrMalformed
```

The first seven wrap `crud.ErrForbidden`, so a transport answers 403 without
importing this package. `ErrPinned` wraps `crud.ErrConflict`. `ErrCapacity` and
`ErrUnavailable` wrap neither: a full pool is not an authorisation failure and
should not page the security team.

`tenancy.OutcomeFor(err)` maps any of them to one of twelve closed constants.
**That is what a metric label may carry.** A tenant reference must never become
one — cardinality grows with your customer count, and the reference is exactly
what the redaction rules keep out of logs. Count refusals by outcome; if you need
per-tenant diagnosis, govern it separately.

---

### 9. Suspending, restoring and deleting a tenant

The lifecycle transition itself is your control plane's job — this extension does
not orchestrate it. What it does is **fence** it, and what follows is what each
transition means to a running deployment.

| You change | What happens next, in-process |
|---|---|
| `Active` → `Suspended` | Every class your `Admission` does not admit for `Suspended` refuses with `ErrInactive`. A request already bound keeps its old lifecycle until the next `Bind` — unless `Revalidate` is on, which stops it at the very next verb. |
| `Suspended` → `Active` | Nothing to do. The next resolve admits again. |
| Restored from backup, or moved | **Bump the epoch.** The old generation's object namespaces, cache partitions and queued job records stop resolving, because the generation is inside each digest. Durable records written under the old epoch refuse with `ErrStale` at the worker. |
| `Active` → `Deleting` / `Deleted` | Work refuses per your admission table. Queued jobs for that tenant reach their handler no more; they drain to a terminal state rather than retrying forever. |

Three things worth deciding before you need them:

- **Whether an in-flight request may finish.** Without `Revalidate` it finishes
  under the lifecycle it bound with; that is the documented window, and it should
  be in your own runbook. With `Revalidate` it stops mid-request, which can leave
  a half-applied unit of work if the transition lands between two verbs.
- **Bump the epoch on every restore, not only on a move.** A restore that reuses
  the generation serves the previous generation's cached values and objects.
- **Deletion of the data itself is yours.** The extension verifies; it does not
  erase. Rows, objects and cached values outlive the lifecycle flag until you
  remove them.

Operator procedures around these — the actual runbooks for suspension, migration
with a fenced cutover, restore and legal hold — are control-plane work and are
deliberately outside this library.

---

## One database per tenant

```go
directory, err := tenancydb.NewDirectory(tenancydb.DirectorySpec{
    Authority:   authority,
    Sources:     sources,             // your factory: a verified scope -> one crud.Source
    Close:       closeThePool,        // mandatory: how to give one back
    MaxCached:   64,
    TTL:         10 * time.Minute,
    Fence:       itIsReallyThisTenants,
    OpenTimeout: 30 * time.Second,
})

lease, err := directory.Borrow(ctx, tenancy.ClassWrite)
defer lease.Release()
invoices := Invoices.Bind(lease.Source(), ...)
```

Selection happens before any statement and is keyed on the tenant **and** the
generation, so a binding cached before a rotation is never handed to the
generation that replaced it.

`MaxCached`, `TTL` and `Close` are required rather than defaulted. The first two
because an unbounded per-tenant pool is how one tenant takes a deployment down.
`Close` because `crud.Source` is an interface and the pools behind it do not
implement `io.Closer`: the directory opened the source, so it has to be told how
to give it back, or it would evict and rotate for the lifetime of the process
without ever closing a connection.

**It does not multiplex one repository across databases.** A repository is bound
to one source; the directory tells you which one, and binding is yours.

#### Choosing between the two

Shared row is the profile this repository publishes and tests against a live
database. Reach for database-per-tenant when a regulator requires physical
separation, when one tenant's volume needs its own hardware, or when per-tenant
restore must not touch anyone else. Pay for it in operations: N schemas to
migrate, N pools to size, and a backup story per tenant.

**Migrating between them is a data move, not a configuration change** — the
extension does not convert one into the other.

---

## What this does not protect

- **A raw statement**, `crud.UnsafeExecFor`, or a repository bound without the
  middleware. The narrowing is a predicate composed in, not a property of the
  connection.
- **`Tx` opened before the tenant is resolved.** The transaction opens; the first
  verb inside it refuses.
- **A `Save` with an assigned key that omits the owner.** The whole row is the
  write and the ownership column is frozen, so a zero owner reads as a change to
  it. `Derive` stamps a create, not an overwrite.
- **A globally unique constraint**, which stays an existence oracle across
  tenants until you extend it with the tenant column.
- **A pagination cursor minted under another tenant's scope.**
- **`Next()` / `Unwrap()`.** The gate forwards what it wraps, so the ungated
  repository beneath it is reachable from code that asks for it.

---

## See also

- [tenancy module reference](../modules/en/tenancy.md) — the full API
- [security](../modules/en/security.md) — the gate this composes into
- [migrations](migrations.md) — the migration command this guide assumes
- [[D-116]] why this is a package and not a module · [[D-117]] why a scope cannot
  be manufactured · [[D-008]] why a foreign row is 404 and not 403
