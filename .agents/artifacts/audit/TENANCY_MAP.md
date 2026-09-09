# TENANCY — audit map

Repository: `/home/user/ws/gd/lease/frostgrove/framework`, module `github.com/frostgrove/vv`.
Everything below is relative to that directory unless stated otherwise.

Audit scope: the `tenancy` subsystem and every seam it touches.
**Priority: the single-database / shared-row topology (`tenancy/tenancyrow` + the
security gate it composes into). Database-per-tenant (`tenancy/tenancydb`) is
secondary but in scope. Documentation quality is in scope.**

## Project rules that override convention defaults

- `CLAUDE.md` (repo root) is binding. Key clauses: docs are read *before* code;
  a doc in `docs/ai/decisions/` is binding law; comments are exceptional
  (40+ line functions only); `crud` (the package, not the subtree) imports stdlib
  + `utils` only; the root module takes no third-party dependency; tests are the
  specification and a test that would still pass with the feature deleted is a
  liability; integration tests never use `t.Parallel()`.
- `/home/user/ws/gd/lease/AGENTS.md`: no explanatory comments by default.
- `.golangci.yml`, `Makefile`, `scripts/checks.sh` carry the structural checks.

## What tenancy is

An **optional extension**: a deployment that does not import it compiles none of
it. A control-plane `Resolver` answers who is calling; the extension turns that
into a **verified `Scope`** (unforgeable, unexported constructor), and the scope
into a narrowed SQL predicate, an object namespace, a cache partition, a durable
job identity, or one selected database.

## Layout — files and what they hold

### Core (`tenancy/`, depends on `crud` + stdlib only)

| File | Holds |
|---|---|
| `authority.go` | `Spec`, `Authority`, `New`/`Must`, `Resolver` iface, `Verify`, `Lookup`, `mint`, `minted`, `accept`, `Fixed` resolver |
| `reference.go` | `Reference` (opaque, `MaxReferenceBytes`=128), `ParseReference`, `Epoch`, `NewEpoch` |
| `scope.go` | `Resolution`, `Scope` (HMAC `binding [32]byte`), `boundTo`, `bind`, `Digest`, `writeField`/`writeBytes` |
| `context.go` | `Bind`, `With` (pinning), `Scope(ctx,class)`, `current` (revalidation), `From` |
| `lifecycle.go` | `Lifecycle` (7 states), `Class` (Read/Write/Durable), `Admission` bitset, `Admit`/`AdmitAll`/`Merge`/`Admits`, `inconsistent` |
| `grant.go` | `Purpose`, `Grant`, `Member`, `Accept`, `Each`, `member`, `holds`, `permits`, `grantBinding`, `permittedByGrant` |
| `seal.go` | `Sealer`, `Seal`, `Unseal`, `mac` — durable records, needs `Spec.DurableKey` >= 32B |
| `errors.go` | 11 sentinels, `refusals` table, `Classify` |
| `outcome.go` | `Outcome` (12 closed constants), `Outcomes`, `OutcomeFor` |

### Seam adapters (one package per seam, one seam each)

| Package | Adapts | Key symbols |
|---|---|---|
| `tenancy/tenancyrow` | `crud/decorators/security` | `Ownership[M]` iface, `Column[M]`, `Through[M]`, `Relation`, `Mode` (`Derive`/`Validate`), `Value`, `Policy`, `Repository`, `relationScopes`, `classOf`, `resolveRelation` |
| `tenancy/tenancydb` | `crud` | `Sources`, `SourcesFunc`, `DirectorySpec`, `Directory`, `Lease`, `Borrow`, `reserve`, `open`, `finish`, `release`, `Evict`, `sweep`, `unlink`, `Close`, `Cached` |
| `tenancy/tenancyjobs` | `jobs` | `ContextProvider`, `IdentityRestorer`, `Capture`, `RestoreIdentity`, `record` |
| `tenancy/tenancystorage` | `storage` | `Namespace`, `Store`, `namespaceOf` |
| `tenancy/tenancycache` | `cache` | `Key[K]`, `Keyed`, `Partition`, `Partitioned` |

### The seam it composes into (read this to judge `tenancyrow`)

`crud/decorators/security/security.go` (1102 lines) — `Policy[M,ID]` and the
`gate[M,ID]`. `tenancyrow.Policy` fills `Scope`, `RelationScopes`, `Inspect`,
`Immutable` and leaves everything else zero. The gate's verbs:
`GetByID Get GetAll First Aggregate Count Exists ExistsUnscoped InsertBatch
SaveAll Save SaveOnly Update UpdateAll Delete Restore DeleteAll` plus
`saveTarget`/`checkImmutableSave`/`deleteVictims`.
Sibling files: `policies.go` (incl. `Combine`), `principal.go`.

Relevant `crud` machinery: `crud.Predicate`, `crud.Eq`, `crud.RelationScopes`
(`AtPath`, `Resolve`, `Empty`), `crud.NarrowRelations`, `crud.IsTautologyFor`,
`crud.Schema`/`Field`/`Relation`, `crud.MustSchemaOf`, `crud.Action`,
`crud.SourceOf`/`BeginnerOf`/`ReadSourceOf`/`KeyOf`, `crud.Middleware`,
`crud.Source`, `crud.UnsafeExecFor`.

## Where state lives

- **Row narrowing**: no state — a predicate composed per call from the context scope.
- **`tenancydb.Directory`**: process-global-ish cache `map[binding]*entry`
  guarded by one `sync.Mutex`; `binding = {reference, epoch}`; entries carry
  `borrowers`, `expires`, `evicted`, `ready chan`, `err`. Bounded by `MaxCached`,
  aged by `TTL`, swept on every `reserve`.
- **`Authority`**: per-process random 32-byte `salt` (scope + grant MAC key) and a
  configured `durableKey` (seal MAC key, >= 32 bytes, must match across processes).
- **Jobs**: the queue table (`jobs`, `jobs/jobspg`) holds a sealed token + partition.
- **Cache / storage**: partition and namespace derived from `Scope.Digest()`
  (SHA-256 over reference + epoch).

## Composition root

The library has no container of its own; `app/` + `app/appfx` is the composition
root seam. Tenancy is wired by the consumer: `_examples/tenancy-sharedrow/main.go`
(165 lines) is the only end-to-end wiring in the repository, and it is in the
unpublished `_examples` module.

## Tests

| Location | Contents |
|---|---|
| `tenancy/*_test.go` | `binding_test`, `contract_test`, `grant_test`, `grant_binding_test`, `scope_test`, `seal_test`, `vocabulary_test`, `support_test` |
| `tenancy/tenancyrow/*_test.go` | `contract_test`, `revalidate_test`, `row_test`, `support_test` (≈705 lines total) |
| `tenancy/tenancydb/*_test.go` | `database_test` (566), `support_test` |
| `tenancy/tenancyjobs/*_test.go` | `durable_test` (453), `support_test` |
| `tenancy/tenancycache/cache_test.go`, `tenancy/tenancystorage/storage_test.go` | |
| `test/integration/tenancy_test.go` | **only two functions**: `TestATenantOwnedRepositoryIsolatesTwoTenantsOnOneTable`, `TestALifecycleThatDoesNotAdmitWorkReachesNoStatement` |
| `scripts/tenancy_test.go` | structural: no base subsystem imports the extension; no tenancy package costs more than the seam it names |

`go test ./tenancy/... ./scripts/...` — **green** at the time of this map.
Integration suite needs Docker (`make up`); the plan records two *pre-existing*
`vvdb` failures unrelated to tenancy.

Run commands: `make unit`, `make integration` (`./test/...`, `-tags=integration`),
`make check` (structural), `make vet`, `make fmt`, `make api`.

## Documentation

| Path | What it is |
|---|---|
| `docs/modules/en/tenancy.md` (359) / `docs/modules/ru/tenancy.md` (364) | the consumer reference, both languages |
| `docs/ai/decisions/D-116-…` | why one extension package, no seam |
| `docs/ai/decisions/D-117-…` | a verified scope is minted, never manufactured |
| `docs/ai/decisions/D-115-…` | unscoped existence is an exact outer effect |
| `docs/ai/decisions/D-008-…` | out of scope is 404, not 403 |
| `docs/ai/flows/FL-033-…` (197) | a request becomes a tenant-bound statement |
| `docs/ai/usecases/modules/security/UC-004-isolate-tenants.md` (138) | the guarantee |
| `docs/roadmaps/2026-09-01-multitenancy-roadmap.md` | what is still open |
| `docs/api/surface.md` | exported-surface baseline (tenancy at line 1757+) |

Also present: `docs/ai/usecases/modules/tenancy/UC-028-serve-many-tenants-from-one-deployment.md`
(122 lines) and `docs/ai/usecases/modules/jobs/`.

**There is no `docs/usage-guides/` page for tenancy.** `CLAUDE.md`'s lookup table
sends "How does a consumer set this up?" to `docs/usage-guides/`, which holds
`ent.md`, `gorm.md`, `migrations.md`, `model-generation.md`, `repository.md`.

## Working-tree caveat — read this before judging anything

**The entire `tenancy/` tree is UNTRACKED**, as are `scripts/tenancy_test.go`,
`test/integration/tenancy_test.go`, `_examples/tenancy-sharedrow/`,
`docs/modules/{en,ru}/tenancy.md`, `docs/ai/decisions/D-116`, `D-117`, `D-118`,
`docs/ai/flows/FL-033`, `FL-035`, `docs/ai/usecases/modules/tenancy/` and
`docs/ai/usecases/modules/jobs/`. A further 35 tracked files are modified
(`crud/decorators/security/security.go`, `crud/errors.go`, `crud/executor.go`,
`crud/decorators/faults/faults.go`, `scripts/checks.sh`, and most of `docs/`).

So `git log` will not show tenancy's history and `git diff` will not show its
contents — this is uncommitted in-progress work. Audit the working tree as it
stands, and do not conclude a file is absent because git does not know it.

## Prior ECONV artifacts — the contract this code was built against

These already exist and are the specification to audit **against**. Do not treat
them as evidence that the code does what they say.

- `.agents/artifacts/usecases/TENANCY_USECASES.md` — **UC-1..UC-93 and INV-1..INV-36**
- `.agents/artifacts/usecases/TENANCY_RECONCILE.md`
- `.agents/artifacts/plans/TENANCY_PLAN.md` — sections S0..S6, coverage matrix,
  mutation-evidence table, and a `## Debt` list of self-declared gaps
- `.agents/artifacts/gaps/TENANCY_S1_GAPS.md`, `TENANCY_S1_TEST_GAPS.md`, `TENANCY_USECASES_GAPS.md`

Self-declared open items in the plan (verify each is *really* only this bad):
S4 is `[~]` (db-per-tenant: no two-real-database test, no outage behaviour);
UC-38/39 RLS deferred; UC-66..77 operator procedures deferred; UC-33/UC-35
enumeration oracles deferred; gate-over-gate scoped writes not forwarded;
a prior test review applied 58 mutations of which **32 survived**.

## Working tree

`git status` shows uncommitted modifications in `crud/`, `docs/`, `_examples/`
and elsewhere. Audit the working tree as it stands.
