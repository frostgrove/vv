# TENANCY — IMPLEMENTATION PLAN

Phase 3. Grounded in the real tree at `frostgrove/framework`.
Inputs: [TENANCY_USECASES.md](../usecases/TENANCY_USECASES.md) (93 UC, 36 INV),
[TENANCY_RECONCILE.md](../usecases/TENANCY_RECONCILE.md),
`docs/roadmaps/2026-09-01-multitenancy-roadmap.md`,
`docs/roadmaps/2026-09-01-extension-architecture-roadmap.md`.

## What this plan delivers, and what it does not

The blind spec covers the roadmap's M0–M5. This plan delivers **M0, M1 and M3**
— the roadmap's own definition of *the first tenancy release* ("The first release
targets one tenant-owned resource on shared PostgreSQL"; "the published profile
names shared-row only unless the full two-database, pool, epoch, migration and
recovery evidence has also passed") — plus **M2's source-selection strategy** in
the same package, so the package shape the roadmap fixes is proven rather than
asserted.

M4 (a multi-extension consumer fixture) and M5 (operator rehearsals: migration
cohort failure, credential rotation, restore/move fencing, legal hold) are
control-plane and release-process work. They stay in `## Debt`, named, not lost.

## Capability classification

Required by `~/.claude/skills/econv/references/microkernel.md` before planning.

| Capability | Kernel mechanism / which kernel / new extension | Why |
|---|---|---|
| Verified tenant scope, lifecycle, epoch, resolver contracts | **new extension** (`tenancy`) | Nothing in the tree produces or validates one; `jobs` consumes a *digest* of one but never makes it |
| Shared-row narrowing | **existing kernel** — `crud.Middleware` chain, via `security.Policy`/`Gate` | The chain is the kernel; tenancy is a plugin that contributes a policy |
| Database-per-tenant source selection | **new extension**, over the existing `crud.Session`/`BindExecutor` kernel | UC-012 (the repository's own use case) states routing by tenant is the application's; tenancy supplies the choice, not a new binding rule |
| Durable job identity | **existing kernel** — `jobs.TrustedContextProvider`/`TrustedIdentityRestorer` | The seam exists and has no implementor |
| Object namespace / cache partition | **existing kernels** — `storage.Namespace`, `cache.Partitioner` | Tenancy maps a verified scope onto inputs those kernels already take |
| Unscoped-existence discovery | **kernel mechanism change** in `crud` | The kernel let an unknown plugin be skipped; that is a kernel defect, not an extension concern |

The kernel test — *can a second extension be added with zero diffs to kernel
files?* — is what S0 restores. After S0 the answer is yes: `audit`, `eventpg` and
`vvotel` need no further `crud` change to decorate a repository.

## Module or package

`tenancy` is a **package of the root module**, not a new `go.mod`. The reasoning
and the conflict it resolves are in
[TENANCY_RECONCILE.md](../usecases/TENANCY_RECONCILE.md#deltas-against-the-multitenancy-roadmap);
it is recorded as a decision doc in S6 and the multitenancy roadmap is corrected
in the same change. A provider adapter that takes a real third-party dependency
becomes its own module at that point, exactly as `jobspg` did for pgx.

## The open questions, answered

The spec raised twelve. Nine are settled by reuse or by keeping the framework
small; three are owner decisions taken here with a stated default and flagged in
the report.

| Q | Answer | Why |
|---|---|---|
| Q1 foreign row: refusal or not-found | **Not-found, no distinct kind at the seam** | Already decided: [[D-008]], and `security.Gate` implements it |
| Q2 suspension lands mid-unit-of-work | **Complete-then-refuse**, bound = one unit of work | INV-13 pins the scope at the start of a unit of work; observing a control-plane transition mid-transaction would need the framework to subscribe to the control plane, which is out of scope item 2. Stated in the profile |
| Q3 is identity the reference or (reference, epoch) | **The pair, for every durable artefact**; the reference alone for the row predicate | Rows die with the tenant, so a recycled reference cannot resurrect them. Durable records, cache partitions and object namespaces carry the epoch, so a restore fences them — the roadmap's "one active generation and a fenced cutover" |
| Q4 schema-per-tenant | **Not a third strategy.** The source contract stays "one caller-owned root datasource capability" | The spec's own recommendation; a schema-bound source satisfies it later without a new topology |
| Q5 durable freshness bound | **Re-derived once at handler entry**; the bound is one handler execution | `jobs.RestoreTrustedIdentity` is called exactly there; anything finer would need the extension to intercept every side effect |
| Q6 one admission list or one per operation class | **Per operation class** (`Read`, `Write`, `Durable`), all defaulting to `Active` only | The spec names the failure of the single list — deployments keep tenants active when they should be suspended — and the cost of three lists is one field |
| Q7 who cleans up on deletion | **The extension verifies, it does not orchestrate** | The smaller commitment, and the only testable one. Deferred to `## Debt` with the verification helper named |
| Q8 grant issuance | **The extension consumes a grant; it does not issue one.** It owns the bounded grant value and the per-member binding loop | Issuance is deployment policy (out of scope item 2). The value type is what makes "never a nil scope" enforceable |
| Q9 opaque bulk criteria | **Refuse unless the strategy itself adds the constraint** | Which is what `security.Gate` already does: it prepends its own predicate and refuses an unscoped bulk. Criteria are never opaque to it |
| Q10 scope-less bootstrap | **No.** Provisioning uses an admission policy that admits `Provisioning` for the write class, or a grant | A second narrowing bypass is the thing INV-2 exists to prevent |
| Q11 suspension propagation bound | **One `Bind` by default; zero with `Spec.Revalidate`.** The framework caches no resolution either way — pinned means it does not re-ask, not that it remembers an answer. A deployment that wants something between the two puts it in its own resolver and states the bound | Re-asking per statement makes the control plane a hot dependency of every request; never re-asking lets a deleted tenant finish a long request. Both are defensible, so both are offered and the window is a number the profile states. *(Corrected after the phase-4 security review reproduced the second case.)* |
| Q12 pre-tenancy rows with no owner | **Refuse at runtime**; backfill is an operator procedure | Admitting them is a permanent hole. Stated in the profile |

## A note on the contract blocks below

The S0–S6 sections record the contracts **as they were accepted at planning
time**. Two review rounds changed several of them — `Column` gained a relation
declaration, `DirectorySpec` gained an `Authority` and traded `Schema` for
`Fence`, `Spec` gained `DurableKey` and `Revalidate`, `Accept` gained the classes
a grant permits, and the object and cache seams moved behind the authority. The
authority on the current shape is `docs/api/surface.md` and
`docs/modules/{en,ru}/tenancy.md`; what changed and why is in
`## Round 2 — the phase-4 review` and in the dispositions tables of
[TENANCY_S1_GAPS.md](../gaps/TENANCY_S1_GAPS.md). The blocks are left as written
rather than back-dated, because a plan edited into agreement with its outcome
stops recording that the outcome was a correction.

## Sections

Statuses: `[ ]` not started · `[~]` partial (must carry MISSING) · `[x]` done,
checkpoint executed · `[!]` blocked (must carry BLOCKED BY).

---

### S0 — `crud`: unscoped existence becomes an exact-outer executable effect  `[x]`

The blocker named by the multitenancy, extension-architecture, audit,
event-sourcing, OTel and product roadmaps. Kernel work, and it came first,
because a tenancy decorator an outer gate can probe *past* is a cross-tenant
existence oracle.

**Contract**

```go
// crud/errors.go
var ErrNoUnscopedExists = errors.New("crud: repository has no unscoped existence capability")

// crud/executor.go
func ExistsUnscopedOf[M any, ID comparable](c Core[M, ID], ctx context.Context, options ...Option) (bool, error, bool) {
	x, ok := c.(UnscopedExister[M, ID])
	if !ok {
		return false, nil, false
	}
	found, err := x.ExistsUnscoped(ctx, options...)
	return found, err, true
}
```

| Wrapper | Decision |
|---|---|
| `sqlrepo.repository` | unchanged — the implementor |
| `security.gate` | implements it: authorises `Read`, applies **its own** scope and relation scopes, forwards to the exact inner core, answers `ErrNoUnscopedExists` when that core cannot |
| `faults.enricher` | implements it: forwards the exact verb, enriches the error, answers `ErrNoUnscopedExists` over a core that cannot — as its `InsertBatch` does |
| `crud.Base` | deliberately does not — an unknown decorator built on it fails closed |

`security.saveTarget` treats `!supported` and `ErrNoUnscopedExists` identically
and answers `crud.ErrNotFound`, as it already did for `!supported`.

**Covers** INV-32, INV-10 (the assigned-key branch), UC-5.

**Checkpoint S0** — executed:

```
go test -race ./crud/...                    ok (all packages)
go test -race -tags=integration ./test/...  green except two pre-existing failures
make check-deps check-tiers check-utils check-triplets check-todo check-replaces check-workspace  ok
go test ./scripts/...                       ok  (doc + roadmap consistency)
```

Mutation-checked: reverting `ExistsUnscopedOf` to the `Nexter` walk makes
`TestUnscopedExistenceIsAnsweredOnlyByTheExactOuterCore/a_decorator_built_on_crud.Base`
fail; dropping the gate's own scope from its probe makes
`TestTheGateAnswersTheUnscopedProbeWithinItsOwnScope` fail with the leak named.

Docs updated in the same change: `D-115` (new), `D-061`, `D-030`, `FL-008`,
`docs/ai/flows/Index.md`, `UC-004` guarantee 20, `docs/modules/{en,ru}/crud.md`,
`.../security.md`, `.../faults.md`, `docs/api/surface.md`, and the six roadmaps
that named the blocker.

---

### S1 — `tenancy`: the verified scope  `[x]`

**Files** `tenancy/doc.go`, `scope.go`, `lifecycle.go`, `authority.go`,
`context.go`, `errors.go`, `outcome.go`.

**Contract**

```go
package tenancy // github.com/frostgrove/vv/tenancy

type Reference struct{ value string }        // redacted String/Format/LogValue/MarshalJSON
func ParseReference(raw string) (Reference, error)
func (Reference) Value() string
func (Reference) IsZero() bool

type Lifecycle uint8
const (
	LifecycleUnknown Lifecycle = iota
	Provisioning
	Active
	Suspended
	Migrating
	Deleting
	Deleted
)
func (Lifecycle) Valid() bool
func (Lifecycle) String() string              // closed vocabulary, safe in a signal

type Class uint8                              // the operation class an admission list is keyed on
const (
	ClassRead Class = iota
	ClassWrite
	ClassDurable
)

type Epoch uint64                             // zero is invalid
func NewEpoch(uint64) (Epoch, error)

type Resolution struct {
	Reference Reference
	Lifecycle Lifecycle
	Epoch     Epoch
}

type Resolver interface {
	Resolve(ctx context.Context) (Resolution, error)
	Current(ctx context.Context, ref Reference) (Epoch, Lifecycle, error)
}

type Admission struct{ read, write, durable []Lifecycle }
func Admit(class Class, states ...Lifecycle) Admission
func (Admission) Merge(Admission) Admission

type Scope struct{ ... }                      // opaque, immutable, no exported constructor
func (Scope) Reference() Reference
func (Scope) Lifecycle() Lifecycle
func (Scope) Epoch() Epoch
func (Scope) IsZero() bool
func (Scope) String() string                  // "[tenancy scope]"

type Spec struct {
	Resolver  Resolver
	Admission Admission                        // default: Active for every class
	Origin    string                           // deployment/environment fence (UC-14)
}
type Authority struct{ ... }
func New(spec Spec) (*Authority, error)
func Must(spec Spec) *Authority

func (*Authority) Verify(ctx context.Context, class Class) (Scope, error)
func (*Authority) Bind(ctx context.Context, class Class) (context.Context, error)
func (*Authority) Scope(ctx context.Context, class Class) (Scope, error)  // carried, revalidated
func From(ctx context.Context) (Scope, bool)                              // never resolves

var (
	ErrNoScope      = ...  // wraps crud.ErrForbidden
	ErrUntrusted    = ...
	ErrInactive     = ...
	ErrStale        = ...
	ErrIncompatible = ...
	ErrCapacity     = ...  // distinct from every authorisation kind (spec failure surface)
	ErrUnavailable  = ...
)
```

`Scope` has no exported constructor and no exported field, so INV-2 holds by the
type system: a literal, a zero value and a conversion from an identically-shaped
type are all impossible outside the package. A test constructs one the only way
production does — by wiring a fixed `Resolver` into an `Authority` — so there is
no test-only constructor to leak (UC-92).

`Origin` is mixed into the scope's internal binding, so a scope value that
travelled from another deployment does not verify (UC-14).

**Covers** UC-8..18, 90, 92; INV-1..6, 15, 16, 30, 33, 34, 35.

**Checkpoint S1** — executed:
```
go test -race ./tenancy/...                                            ok
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./tenancy/  → no external package
make check-deps check-tiers check-utils                                ok  (tenancy added to SUBSYSTEMS)
```

---

### S2 — `tenancy`: the shared-row strategy  `[x]`

**Files** `tenancy/row.go`.

**Contract**

```go
type Ownership uint8
const (
	Derive   Ownership = iota  // stamp the owner on create when the field is zero
	Validate                   // require the caller to have supplied the right owner
)

type RowSpec struct {
	Field     string
	Value     func(Reference) (any, error)   // default: Reference.Value()
	Ownership Ownership
	Relations []RowRelation
}
type RowRelation struct{ Path, Field string }

func Policy[M any, ID comparable](a *Authority, spec RowSpec) security.Policy[M, ID]
func Repository[M any, ID comparable](a *Authority, spec RowSpec) crud.Middleware[M, ID]
```

`Repository` is `security.Gate(Policy(...))`. `Policy` composes with an
application's own policy through `security.Combine`, which ANDs scopes, chains
`Inspect`, unions `Immutable` and keeps both unscoped-bulk refusals — that is the
documented way to add tenancy to a repository that already has a gate, because
two stacked gates cannot perform an assigned-key `Save` (the scoped upsert is an
exact capability of the core directly below).

The whole `crud.Core` matrix is therefore covered by machinery that already has
[[D-030]]'s obligation test behind it. Tenancy adds:
`Scope` → `crud.Eq(field, value)` from the verified scope, failing closed;
`Inspect` → derive-or-validate ownership per `Ownership`;
`Immutable` → `[]string{field}`; and it never sets `AllowUnscoped*`.

**Covers** UC-19..37; INV-7..13, 17.
UC-38/39 (PostgreSQL RLS) are `## Debt`: the roadmap makes RLS optional defence
in depth and gates it on separate role and pooled-session evidence.

**Checkpoint S2** — executed:
```
go test -race ./tenancy/...                                    ok
go test -race -tags=integration ./test/...                     against live PostgreSQL
  TestATenantOwnedRepositoryIsolatesTwoTenantsOnOneTable       foreign id / count / exists / update / delete / create
  TestALifecycleThatDoesNotAdmitWorkReachesNoStatement         with its control
```

---

### S3 — `tenancy`: root-seam adapters  `[x]`

**Files** `tenancy/jobs.go`, `tenancy/storage.go`, `tenancy/cache.go`.

```go
func JobContext(a *Authority, provenance jobs.IdentityProvenance) (jobs.TrustedContextProvider, error)
func JobIdentity(a *Authority) jobs.TrustedIdentityRestorer

func Namespace(prefix string, scope Scope) (storage.Namespace, error)
func Store(scope Scope, prefix string, backend storage.Backend) (storage.Store, error)

func Partition(scope Scope, limit cache.KeyLimit) ([]byte, error)
func Partitioner[K any](a *Authority) cache.Partitioner[K]
```

The namespace and partition both carry the epoch (Q3), so a restored generation
cannot read the previous one's objects or cached values (UC-65, INV-18).
`JobIdentity` re-resolves lifecycle and epoch through the authority before the
handler is entered, so the durable record is a reference and never an authority
(INV-14, UC-52..56).

**Covers** UC-51..65; INV-14, 18, 19.

**Checkpoint S3** — executed:
```
go test -race ./tenancy/... ./jobs/... ./storage/... ./cache/...   ok
```
The base subsystems are unchanged: the adapters implement their existing
interfaces and no file under `jobs/`, `storage/` or `cache/` was touched.

---

### S4 — `tenancy`: the database-per-tenant strategy  `[~]`

MISSING: two real databases holding equal ids, and datasource-outage behaviour.
Everything else in this section is implemented and proved against recording
sources; the published profile names shared row only because of this line.

**Files** `tenancy/database.go`.

```go
type Sources interface {
	Source(ctx context.Context, scope Scope) (crud.Source, error)
}

type DirectorySpec struct {
	Sources   Sources
	MaxCached int            // mandatory bound (INV-23); zero is a construction error
	TTL       time.Duration  // borrower lifetime; zero is a construction error
	Schema    func(context.Context, crud.Source) (Epoch, error)  // optional generation fence
}

func NewDirectory(spec DirectorySpec) (*Directory, error)
func (*Directory) For(ctx context.Context) (crud.Source, error)
func (*Directory) Bind(ctx context.Context) (context.Context, error)  // one crud.Session, once
func (*Directory) Evict(ref Reference)
```

Selection happens before a transaction or repository is exposed; `Bind` pushes
exactly one `crud.Session`, and a second `Bind` inside an open unit of work
refuses rather than switching (INV-13, UC-45). There is no default and no
last-used fallback (UC-42, UC-43). Eviction does not close a source that still
has a live borrower (UC-48).

**Covers** UC-40..50; INV-6, 22, 23, 24.

**Checkpoint S4** — executed against recording sources:
```
go test -race ./tenancy/...   ok
```
MISSING: two *real* databases and outage behaviour. The strategy is implemented
and every refusal path is proved — absent mapping, wrong mapping, capacity,
rotation to a new generation, eviction with a live borrower, borrower lifetime,
schema-generation fence — but against `crudtest` sources rather than two servers.
The published profile therefore names shared row only.

---

### S5 — `tenancy`: bounded cross-tenant grants  `[x]`

**Files** `tenancy/grant.go`.

```go
type Purpose struct{ value string }
func ParsePurpose(raw string) (Purpose, error)

type Grant struct{ ... }        // opaque; consumed, never issued by this package
func (a *Authority) Accept(ctx context.Context, purpose Purpose, cohort []Reference, until time.Time) (*Grant, error)
func (g *Grant) Each(ctx context.Context, fn func(context.Context) error) error
```

`Each` binds exactly one cohort member at a time, re-checks that member's
lifecycle and epoch before entering `fn`, stops when the grant expires mid-run
(UC-80), and reports which members completed so a partially applied run is
resumable rather than atomic (INV-21). There is no nil-scope path (UC-79).

**Covers** UC-78..83; INV-20, 21.

**Checkpoint S5** — executed: `go test -race ./tenancy/...` ok

---

### S6 — telemetry vocabulary, documentation, profile and the roadmap  `[x]`

`tenancy/outcome.go` exposes a closed `Outcome` vocabulary (`ok`, `absent`,
`untrusted`, `stale`, `inadmissible`, `unmapped`, `incompatible`, `capacity`,
`unavailable`) plus `Lifecycle.String()` and the topology mode — and nothing
else. A sentinel scan test plants a tenant reference, a DSN and a bucket name in
the resolver's environment and asserts none of them is reachable from any
refusal's message, any `Outcome`, or the scope value (INV-25..29, UC-86..89).

Docs, in the same change:

- `docs/ai/decisions/D-116` — one tenancy extension, and why it is a package of
  the root module rather than a `go.mod`.
- `docs/ai/decisions/D-117` — a verified scope is produced by an injected
  authority and cannot be manufactured; lifecycle admission is a whitelist per
  operation class; the durable reference is not an authority.
- `docs/ai/flows/FL-0xx` — a request becomes a verified scope becomes a narrowed
  statement; and the durable half.
- `docs/ai/usecases/modules/tenancy/` — UC pages, with `Index.md` rows.
- `docs/modules/en/tenancy.md` + `docs/modules/ru/tenancy.md`, with `Index.md`
  rows in both.
- `docs/roadmaps/2026-09-01-multitenancy-roadmap.md` — corrected: the package
  decision, M0/M1/M3 marked delivered with their evidence, the published profile
  (shared-row, the answered open questions, the stated exclusions), and M2/M4/M5
  left open with what they still owe.
- `docs/roadmaps/Roadmap.md` item 13 — narrowed to what remains.
- `docs/api/surface.md` — regenerated.
- `_examples/tenancy-sharedrow/` — the hand-wired composition root the extension
  architecture revision asks for, built, vetted and run under `GOWORK=off`, with
  its row in `_examples/README.md`.

**Covers** UC-84..89, 91, 93; INV-25..29.

**Checkpoint S6** — executed:
```
go test ./scripts/...                                              ok
make check-deps check-tiers check-utils check-todo check-replaces check-workspace   ok
make api                                                           60 new surface lines, diff read
make unit                                                          ok
gofmt -l . / make vet                                              clean
```
`make check-tidy` fails on four modules this change does not touch, and
`make check-otel-module` needs the network. Both were confirmed failing on the
clean tree before any change here.

---

### S7 — the core is separated from the seams it adapts  `[x]`

Raised by the owner after S6, against the delivered tree: `tenancy` imported
`crud`, `crud/decorators/security`, `jobs`, `storage` and `cache`, so a
deployment that wanted a tenant-partitioned cache compiled the authorization
subsystem to get it. The plan's own reading of "one extension, one public
package" was the cause, and it was wrong in the direction that matters here —
optionality is a property of the import graph.

**Files** — the core keeps `doc.go`, `reference.go`, `scope.go`, `lifecycle.go`,
`authority.go`, `context.go`, `errors.go`, `outcome.go`, `grant.go` and gains
`seal.go`. Each seam moves to a package beside it: `tenancyrow/row.go`,
`tenancydb/database.go`, `tenancyjobs/jobs.go`, `tenancystorage/storage.go`,
`tenancycache/cache.go`, each with its own tests.

What the split required the core to export, and why each is safe:

- `Scope.Digest` — the derivation the object namespace and the cache partition
  share, so a restore cannot fence the bucket and serve the old cache. It
  identifies a tenant and a generation and authorises nothing.
- `Classify` — every seam calls application-supplied code (a resolver, a source
  factory, a fence) and none of them may let its text travel.
- `Authority.Sealer`, `Sealer.Seal`, `Sealer.Unseal` — the durable token was
  built in `jobs.go` from the authority's unexported key. It is now a core
  capability over opaque binding fields, so the key never leaves the authority
  and the `jobs` adapter contributes only the queue digest and the definition.
  `Seal` refuses a scope this authority did not mint, which the package-private
  version never had to check.

`Authority.Scope` also gained a nil-receiver guard: a seam wired without an
authority is now a plausible mistake at five call sites rather than none, and it
refuses instead of panicking in a handler that had a perfectly good tenant.

**Covers** the same UCs as S1..S5; adds the layout invariant of [[D-116]].

**Checkpoint S7** — executed:

```
go test -race ./tenancy/... ./crud/... ./scripts/                      ok
go vet ./...                                                          ok
gofmt -l .                                                            silent
make check-deps / check-tiers / check-utils / check-triplets
        / check-todo / check-replaces                                 ok
GOWORK=off go vet ./tenancy-sharedrow/ && go run ./tenancy-sharedrow  ok
VV_PG_DSN=…:55433 go test -race -tags=integration ./integration/
        -run 'Tenancy|TenantOwned'                                    ok
full integration suite, twice   FAIL on the two pre-existing vvdb tests only
```

first-party graphs, which is the whole point of the section:

```
./tenancy                 utils crud tenancy
./tenancy/tenancyrow      utils crud errs internal/nilvalue auth
                          crud/decorators/security tenancy tenancyrow
./tenancy/tenancydb       utils crud tenancy tenancydb
./tenancy/tenancyjobs     jobs utils crud tenancy tenancyjobs
./tenancy/tenancystorage  storage utils crud tenancy tenancystorage
./tenancy/tenancycache    cache utils crud tenancy tenancycache
```

Mutations run against the new tests, each restored afterwards: a `Seal` that
skips `minted` (killed), a MAC over unprefixed binding fields (killed), a digest
without the generation (killed), the nil guard removed (killed — after the first
version of those two tests turned out to pass without it, because an unbound
context refuses earlier; they now bind a real scope first), an import of `cache`
added to the core (killed by the layout test) and `security` added to
`tenancydb` (killed).

---

### S7 review — two gates, and what they changed

Two reviewers with clean context, one on architecture and boundaries and one on
the split test suites. Between them: six `[high][immediate]`, two `[critical]`,
and a set of mediums. Everything immediate is closed, each with a mutation that
kills it; `TENANCY_S7_GAPS.md` and `TENANCY_S7_TEST_GAPS.md` carry the
dispositions finding by finding.

Four of the closures were defects the split itself introduced or exposed:

1. **`Sealer.Seal` asked no class.** Before the split the class check and the MAC
   were one unexported path in `jobs.go`; exporting the primitive left the check
   behind in the adapter. `From(ctx)` is public, so any carried scope could be
   sealed into a durable record for a tenant the deployment admits no durable
   work for — one that executes the moment the tenant is active again, with no
   generation change to stop it. `Seal` now accepts for `ClassDurable`.
2. **The seal carried no `Origin`.** A `DurableKey` is configured wiring, and
   wiring is what a cloned environment keeps; the scope binding has fenced
   deployments since S1 and the durable record did not.
3. **Nothing said what the seal's MAC covered.** Every forgery in the suite was a
   token whose MAC did not match. Dropping the reference from the MAC — a record
   sealed for one tenant running as another — left the whole suite green. The
   test that closes it edits the claim under a MAC this deployment really
   produced, which is the only shape that shows what is covered.
4. **The layout law was a hand-written table.** A sixth package under `tenancy/`
   importing two siblings, and a new top-level subsystem importing the core, both
   passed. Both laws now enumerate what they check.

Two were older and are recorded because they were believed closed: the bulk
narrowing tests passed with the narrowing replaced by `Total = 424242`, because a
bulk `DELETE` spells out every column of the rows it read first, the ownership
column among them; and the relation narrowing accepted a constant foreign tenant,
because the assertion named a column and never an argument. Both were recorded in
`TENANCY_S1_TEST_GAPS.md` as closing a `[critical]`. They are closed now, on the
leading conjunct and on the bound argument.

One repository-wide check was strengthened rather than worked around: the
doc-citation test could only see a symbol that moved *within a surviving file*,
which is the one kind of drift a restructure does not produce. It now fails on a
directory-qualified path this tree does not have — and found two pre-existing
stale citations while being written.

---

## Round 2 — the phase-4 review, and what it changed

Two `econv-impl-reviewer` subagents audited the implementation with clean
context. They returned 42 findings, three of them critical, and each critical was
reproduced against the code with real SQL rather than argued. The dispositions are
in [TENANCY_S1_GAPS.md](../gaps/TENANCY_S1_GAPS.md); the design changes they
forced are:

- **`Column` declares its tenant-owned relations.** A preload is a second
  statement and the root's `WHERE` does not reach it; the first draft handed the
  gate a relation-scope function that returned nothing and then set
  `AllowUnscopedRelationScopes: true` to stop the gate complaining — switching off
  the exact guard that exists for this. A strategy that declares no relation now
  supplies no function at all.
- **Freshness is a stated window with two settings**, not an assertion that the
  framework re-asks (Q11 above is corrected).
- **The durable reference is MACed with a configured key**, because the
  per-process salt cannot reach the worker and an unauthenticated reference in a
  queue row is impersonation.
- **The directory reserves its slot before it opens**, and re-checks admission
  through the authority it now requires.
- **A grant names the classes it permits**, because a purpose is a label.
- **The object and cache seams go through the authority**, because holding a scope
  is not the same as being admitted now.

Three of those were found and fixed before the reviewers reported, from re-reading
the code; the reviewers found them independently, which is the check working in
both directions.

## What "green" means here, exactly

The integration suite was run twice in a row, as `CLAUDE.md` requires. Both runs
end `FAIL`, and both fail on exactly the same two tests:

```
--- FAIL: TestOneConfigShapeOpensEveryEngine/postgres
        vvdb_test.go:67: the pool section did not reach the handle: MaxOpenConnections is 0
--- FAIL: TestDbpgxOpensAPoolFromTheSameConfig
        vvdb_test.go:102: the pool section did not reach pgx: MaxConns is 16
```

Both fail identically with this entire change set removed — `crud` stashed,
`tenancy/` and `test/integration/tenancy_test.go` moved aside — so they are
`vvdb` pool-configuration failures that predate this work. They are recorded in
`## Debt` and are not this change's to fix. Every other test in the suite passes,
including all six subtests of the tenancy isolation test, in both runs.

One more thing worth writing down, because it wasted a review cycle: two earlier
runs in this session were reported as green when they were not. The first pair
never reached PostgreSQL at all — another process held the port the compose file
uses, so `vv`'s own container had exited, and the exit status was masked by a pipe
to `tail`. A later run genuinely ran but its failures were cut off by a `head -20`
after ten lines of Gin startup noise. **Read the suite's own last line, not a
filtered view of it.**

## Round 3 — the phase-5 test review, and what it changed

An `econv-test-reviewer` graded the suite by applying **58 mutations**. Twenty-six
were caught; **thirty-two survived**. The suite was green throughout, which is the
point of the exercise: green was not evidence.

The worst of it, and it is worth stating plainly because the plan claimed the
opposite: **no test in the unit suite asserted which tenant the narrowing named.**
Replacing the predicate with `crud.Eq(owner, "globex-1a2b")` — a different tenant
— left the suite green, and replacing it with `crud.Eq("total", 424242)` left six
of the eight subtests of the test named for that behaviour still passing, because
it matched `tenant_id` anywhere in the statement and every row-returning verb
projects that column. Only the live-PostgreSQL test caught it.

Also surviving: the tenant never had to reach the source factory or the fence;
four of the fixes the phase-4 review forced had no test at all; and the S0 kernel
test's "the caller's own narrowing was dropped" assertion could not detect the
caller's narrowing being dropped, because `"tenant_id"` contains `"id"`.

All of the `[critical]` and `[high][immediate]` rows are closed — see the
dispositions table in
[TENANCY_S1_TEST_GAPS.md](../gaps/TENANCY_S1_TEST_GAPS.md). Two structural checks
were added to `scripts/` in the process, because neither belonged in a package
test: no base subsystem may depend on the extension, and importing it may not do
anything.

## Mutation evidence

Every test below was proved to fail on a broken implementation and to pass again
once it was restored — the check `CLAUDE.md` asks for, run for each control this
package rests on.

| What was broken | Which test caught it |
|---|---|
| `Scope.boundTo` stops comparing the binding | `TestOnlyTheAuthorityThatMintedAScopeAcceptsIt` |
| `Admission.Admits` admits every state | `TestOnlyAnAdmittedLifecycleReachesWork` |
| `classify` returns the resolver's own error | `TestAResolverFailureNeverTravelsBackAsText` |
| the generation leaves the namespace digest | `TestARestoredGenerationReadsNoneOfThePreviousOnes` |
| the durable restorer believes its record instead of asking the control plane | `TestWorkEnqueuedBeforeASuspensionDoesNotRunAfterIt` |
| ownership is neither stamped nor checked on create | `TestACreateForAnotherTenantIsRefusedWhicheverModeIsDeclared` |
| both of the directory's capacity checks are removed | `TestTheCacheIsBoundedRatherThanGrowingWithTenants`, `TestABindingIsGivenBackAfterItsBorrowerLifetime` |
| a grant is honoured by an authority that never accepted it | `TestAGrantFromAnotherAuthorityIsNotHeldHere` |
| the purpose, the cohort, the classes or the deadline leave the grant binding | `TestEveryPartOfAGrantIsInsideItsBinding` (four separate mutations) |
| the directory's slot is not reserved before the source is opened | `TestNoMoreSourcesAreOpenAtOnceThanTheBudgetAllows` (64 open against a budget of 4), `TestConcurrentBorrowersForOneTenantShareOneOpen` |
| the producer does not capture the tenant | `TestWorkEnqueuedByATenantIsExecutedAsThatTenant` |
| the producer captures without asking for the durable class | `TestAProducerRefusesToCaptureAScopeTheDurableClassDoesNotAdmit` |
| the declared relation is dropped from the preload | `TestAPreloadOfADeclaredRelationCarriesTheTenant` (its control asserts the leak is real without the declaration) |
| revalidation is skipped, the generation is not compared, a lookup failure returns the carried scope, or the default revalidates too | `TestRevalidationStopsTheNextVerbAfterTheControlPlaneMoves`, `TestWithoutRevalidationTheCarriedScopeHoldsUntilTheNextBoundary` |
| `Lease.Release` loses its idempotence guard | `TestOneLeaseReleasedFromManyGoroutinesCountsOnce` |
| the open shares the caller's cancellation, or a waiter ignores its own deadline | `TestABorrowerThatGivesUpDoesNotFailTheOnesBesideIt`, `TestAWaiterHonoursItsOwnDeadlineRatherThanTheOpeners` |
| `Each` does not notice it is already bound | `TestACohortRunFromInsideABoundRequestIsRefusedRatherThanEmpty` |
| the object seam skips the authority | `TestTheObjectSeamRefusesAScopeAnotherAuthorityMinted` |
| relation narrowing is decided by probing the strategy rather than asking it | `TestAStrategyWhoseRelationsFailAreARefusalRatherThanNoNarrowing` |
| a grant's deadline is not re-checked at the point of use | `TestAContextCarriedOutOfAnExpiredGrantStopsWorking` |
| `ExistsUnscopedOf` goes back to walking `Nexter` | `TestUnscopedExistenceIsAnsweredOnlyByTheExactOuterCore/a_decorator_built_on_crud.Base` |
| the gate drops its own scope from the unscoped probe | `TestTheGateAnswersTheUnscopedProbeWithinItsOwnScope` |

Removing only *one* of the directory's two capacity checks left the test passing,
which is worth recording: the second check is not redundant, it is the one that
holds when two goroutines open a binding for different tenants at once.

## UC / INV coverage matrix

| Section | UC | INV |
|---|---|---|
| S0 | 5 | 32, and the assigned-key branch of 10 |
| S1 | 1, 4, 6, 8–18, 90, 92 | 1, 2, 3, 4, 5, 6, 15, 16, 30, 33, 34, 35 |
| S2 | 2, 19–37 | 7, 8, 9, 10, 11, 12, 13, 17 |
| S3 | 51–65 | 14, 18, 19 |
| S4 | 3, 40–50 | 6, 22, 23, 24 |
| S5 | 78–83 | 20, 21 |
| S6 | 84–89, 91, 93 | 25, 26, 27, 28, 29 |
| S2 + S6 | 7 (both orders), 31 | 31 (order-independent refusal) |
| **Debt** | 38, 39 (RLS), 66–77 (operator procedures) | 36 (deletion finality — verified, not orchestrated) |

Every UC-* and INV-* in the spec appears exactly once above.

## Debt

Deferred, with the reason. None of these is dropped; each is a named milestone in
the corrected roadmap.

- **UC-38, UC-39 — PostgreSQL RLS hardening.** The roadmap makes it optional
  defence in depth and gates it on a separate application role, owner/BYPASSRLS
  checks and pooled-session reset evidence. It is a live-database profile, not a
  package change. `[medium][deferred]`
- **UC-66..77, INV-36 — operator procedures** (suspend, migrate, restore, delete,
  legal hold, cohort failure, generation disagreement). These are control-plane
  procedures; the extension's part is the fence (epoch, admission, namespace
  generation), which S1/S3 deliver. The rehearsals are M5. `[high][deferred]`
- **M4 — the multi-extension consumer fixture.** Tenancy composed with audit,
  event sourcing, i18n and `vvotel` in an unpublished module. Two of those
  extensions do not exist yet, so the fixture cannot be written. What *can* be
  proven now is: composition with an unrelated decorator in both orders (S2), the
  `security.Combine` path, and `_examples/tenancy-sharedrow`, which wires the
  extension from outside the module under `GOWORK=off`. Still owed: a
  purpose-built pair of fake extensions, and a method inventory for the storage,
  cache and jobs adapters — the repository adapter inherits [[D-030]]'s.
  `[medium][deferred]`
- **Gate-over-gate scoped writes.** `security.gate` does not forward
  `ScopedSaver`/`ScopedDeleter`/`ScopedRestorer`, so two gates in one chain cannot
  perform an assigned-key `Save`. Pre-existing, and `security.Combine` is the
  supported composition. Forwarding them correctly means running the inner gate's
  `Inspect` and immutable checks inside the forward, which is security-critical
  work of its own. `[medium][deferred]`
- **`cache.BatchReaderOf`** still walks through backend wrappers — the last
  executable-effect walk in the repository now that [[D-115]] closed
  `crud.ExistsUnscopedOf`. Out of this task's scope; named in the corrected
  roadmaps as the one remaining blocker of its kind. `[medium][deferred]`
- **Two pre-existing integration failures**, `TestOneConfigShapeOpensEveryEngine`
  and `TestDbpgxOpensAPoolFromTheSameConfig`, about `vvdb` pool settings not
  reaching the handle. Verified failing with this entire change set stashed.
  `[medium][deferred]`
- **UC-6 — a way for a composition root to notice it forgot the middleware on one
  repository.** Refused rather than deferred: a registry of repositories is on the
  roadmap's forbidden list, and a service locator to catch a missing middleware
  trades the invariant for the convenience. Named in the published profile so the
  gap is visible rather than assumed closed. `[low][deferred]`
- **UC-33, UC-35 — two enumeration oracles**: a create against a globally unique
  key, and a pagination cursor minted under another tenant's scope. Both are the
  class of leak [[UC-004]] already records for unique constraints, both are
  inherent to the public verb rather than to the narrowing, and both are named in
  the profile. `[medium][deferred]`
- **GAP-T4, T11, T12, T15..T25 — the test gaps that survived triage.** INV-5 is
  proved for one invalid-scope class of six; INV-9's inventory covers `crud.Core`
  and not the optional executable capabilities; relation closure is proved at
  depth 1 against a recorder rather than at depth ≥ 2 against a live database; the
  new concurrency tests are timing-based; three tests call `t.Fatalf` from a
  non-test goroutine. Each is real and none is a leak that the closed rows leave
  open. `[medium][deferred]`
- **Pre-existing `make check-tidy` failure** in `app/http/appfiber`,
  `auth/access/accessjwt`, `.../revokeredis` and `.../revokeredisfx`. Present on
  the clean tree before any change here. `[low][deferred]`
- **The two seam packages test their derivation and not their seam.**
  `tenancystorage.Store` and `tenancycache.Partitioned` have no test that builds a
  real `storage.Store` and shows a listing stays inside the namespace, or builds a
  `cache.Scope` with a slow loader and shows two tenants get two flights under
  `-race`. The second is INV-19, the one cross-tenant read no serial test can
  find. The split made both cheaper — each seam now has its own package and its
  own dependency — and neither is written. `[medium][deferred]`
- **The activation check greps rather than parses.** `go func(`, `go name(`,
  `func init(` and two `var` shapes are what it knows; a package that started a
  lifecycle some other way would pass. `[low][deferred]`
- **Counted threshold breaches, kept deliberately.** `tenancystorage.Store` takes
  five parameters (context, authority, prefix, class, backend) because the class
  is what it re-asks and the backend is what it wraps; `tenancydb.Directory` has
  ten fields, three of which are the cache it is; `tenancyrow.Mode` is a flag
  parameter that changes what `Column`'s `Apply` means. The first two are the
  shape of the thing; the third predates the split and would be two constructors
  in a redesign. `[low][deferred]`
