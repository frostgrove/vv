# TENANCY - RECONCILE

Phase 2. Written against the real tree at `frostgrove/framework`, after the
blind spec in [TENANCY_USECASES.md](TENANCY_USECASES.md).

Governing documents: `docs/roadmaps/2026-09-01-multitenancy-roadmap.md` (domain),
`docs/roadmaps/2026-09-01-extension-architecture-roadmap.md` (package/module model),
`docs/ai/decisions/` (binding).

## Mechanism inventory

| Concern | Existing mechanism | Path:line |
|---|---|---|
| repository seam | `crud.Core[M,ID]` — 15 verbs, the whole supported matrix | `crud/repo.go:5` |
| repository decoration | `crud.Middleware[M,ID]`, `crud.Chain`, `crud.Decorate`, `crud.Base` (declared `Next()`) | `crud/repo.go:39`, `crud/repo.go:63`, `crud/repo.go:59`, `crud/repo.go:76` |
| row narrowing, the complete matrix | `security.Policy` + `security.Gate` — scope, relation scopes, permissions, authorize, inspect, immutable, unscoped-bulk refusals | `crud/decorators/security/security.go:29`, `:59` |
| tenant-shaped policy helper | `security.ScopeField` — AND-ed `Eq` predicate, `Inspect` that *rejects* a mismatched owner, frozen column | `crud/decorators/security/policies.go:13` |
| scoped atomic writes | `crud.ScopedSaver`, `crud.ScopedDeleter`, `crud.ScopedRestorer`, `crud.TombstoneLoader` | `crud/executor.go:160-206`, `crud/lifecycle.go:12-24` |
| exact optional repository effects | `crud.BatchInserter` / `InsertBatchOf` (exact outer, [[D-030]] [[D-061]]) | `crud/batch.go:20`, `:24` |
| **executable effect that still walks** | `crud.UnscopedExister` / `ExistsUnscopedOf` — follows `Nexter` through unknown wrappers | `crud/executor.go:138`, `:141` |
| datasource identity / transaction binding | `crud.Source`, `crud.Session`, `crud.BindExecutor`, `crud.InTx`, `crud.SameDataSource`, `crud.KeyOf` | `crud/executor.go:57`, `:246`, `:290`, `:520` |
| declared navigation walk | `crud.Nexter` + `crud.SourceOf` (bounded at 64) | `crud/executor.go:113`, `:118` |
| predicate AST (closed) | `crud.Predicate`, `Eq`, `And`, `Or`, `InAny`, `IsTautologyFor` | `crud/predicate.go` |
| service seam and its chain | `port.Service`, `port.ServiceMiddleware`, `port.ChainService`, `port.RestorableOf` | `port/service.go:12`, `:52`, `:54`, `:41` |
| durable job identity | `jobs.TrustedContextProvider`, `jobs.TrustedIdentityRestorer`, `ContextCapture`, `RestoredIdentity`, `IdentityEpoch`, `IdentityProvenance`, `ContextScope`, `PartitionMode`, `TenantPartitioner`, `TenantIdentity` (32-byte digest) | `jobs/durable_context.go:389`, `:461`, `:336`, `:471`, `:33`, `:46`, `:14`, `jobs/scope.go:118`, `:139` |
| jobs queue wiring point | `jobs.QueueSpec.Context`, `jobs.WorkersConfig.Identity` | `jobs/queue.go:45`, `jobs/workers_config.go:42` |
| object namespace | `storage.Store` bound to one `storage.Namespace` at construction; `storage.Middleware`/`storage.Chain`; `storage.Backend` takes the namespace per call | `storage/store.go:10`, `:25`, `:27`, `:43`, `:69` |
| cache partition | `cache.Partitioner[K]`, `cache.Partitioned`, `cache.Global`, `cache.ScopePlan` | `cache/key.go:81`, `:93`, `:89`, `cache/declaration.go:21` |
| identity in the request | `auth.Principal`, `auth.Claims`, `auth.Permission`, principal-in-context ([[D-055]]) | `auth/principal.go:13`, `:23`, `:11` |
| error model | `crud` sentinels (`ErrNotFound`, `ErrForbidden`, `ErrConflict`…), `errs` kinds/resolvers | `crud/errors.go:9-35`, `errs/` |
| configuration | none consumed by a decorator; the composition root constructs values | — |
| dependency wiring | fx modules per subsystem (`*fx` satellites), `module.Definition`/`Catalog` ([[D-106]] [[D-111]]) | `app/`, `runtime/` |
| test infrastructure | `crud/crudtest.Recorder` (statement-recording fake `Source`), `test/` integration module (live PostgreSQL/MySQL, never `t.Parallel`) | `crud/crudtest/recorder.go:38`, `test/integration/` |
| module/tier rules | `make check`: `check-deps`, `check-tiers`, `check-triplets`, `check-replaces`, `check-tidy`; modules discovered by `find . -name go.mod` | `scripts/checks.sh:38`, `:66`, `Makefile` |
| doc consistency gates | `scripts/docs_test.go` — every test name, symbol and status a doc cites must exist where the doc says | `scripts/docs_test.go:38`, `:241`, `:515` |
| roadmap gate | `scripts/roadmap_test.go` — the roadmap may not credit a package with an activation the scanner cannot find | `scripts/roadmap_test.go:26` |
| public contracts I must not break | `crud.Core`, `crud.Middleware`, `security.Policy`/`Gate`, `port.Service`, `jobs.TrustedContextProvider`/`Restorer`, `storage.Store`, `cache.Scope` | above |
| project rules | `CLAUDE.md` (docs are binding, update in the same change), `docs/ai/decisions/` — especially [[D-030]] [[D-033]] [[D-036]] [[D-051]] [[D-055]] [[D-058]] [[D-061]] [[D-074]] | `CLAUDE.md`, `docs/ai/decisions/` |
| existing kernels and extension points | `crud.Middleware` chain, `port.ServiceMiddleware` chain, `storage.Middleware` chain, jobs trusted-context boundary, cache partitioner | above |

## Reuse decisions

- **Reuse `security.Gate` for the shared-row matrix** (`crud/decorators/security/security.go:59`).
  It already covers all fifteen `Core` verbs with an AND-ed predicate, refuses
  unscoped `DeleteAll`/`UpdateAll`, freezes columns, pins a filtered write to the
  inspected snapshot, and answers `ErrNotFound` rather than `ErrForbidden` for a
  foreign row ([[D-008]]). Re-implementing that matrix inside `tenancy` would be a
  second copy of the most security-critical code in the repository, and [[D-030]]
  would then need a second obligation table. Tenancy contributes a `Policy` and
  returns `crud.Middleware`; it owns no repository facade.
- **Reuse `crud.Predicate`, `crud.Meta`/`Schema`** for column resolution and the
  narrowing term; the AST is closed, so a caller cannot peel the scope back off.
- **Reuse the jobs trusted-context boundary as-is** (`jobs/durable_context.go:389`,
  `:461`). It is already tenant-shaped and privacy-shaped: the durable record
  carries a 32-byte `TenantIdentity` digest, a provenance string and an epoch, and
  `RestoreTrustedIdentity` is the re-resolution point. Tenancy implements the two
  interfaces; `jobs` keeps compiling with neither.
- **Reuse `crud.Session`/`BindExecutor`/`SameDataSource`** for database-per-tenant
  source selection instead of inventing a routing mechanism; UC-012 states
  explicitly that *which* datasource a repository uses is the application's
  choice, so tenancy supplies the choice, not a new binding rule.
- **Extend `crud`** with exactly one base change: make `ExistsUnscopedOf` an
  exact-outer executable effect. See "Deltas" below — it is the blocker M0 item 4
  names, and it is what [[D-061]] already decided for every other effect.
- **Build new** — `tenancy` package: verified scope value, lifecycle/epoch,
  resolver contracts, context propagation, row/database strategies, jobs/storage/
  cache factories, error identity, telemetry vocabulary. Searched for an existing
  equivalent in: `crud/`, `crud/decorators/`, `auth/`, `auth/access/`, `port/`,
  `jobs/`, `cache/`, `storage/`, `app/`, `runtime/`, `remote/`, `utils/`, plus a
  repository-wide grep for `Tenant` — the only production hits are the jobs
  durable-context vocabulary above; every other hit is a test fixture column.
- **Do not touch** — `jobs`, `cache`, `storage`, `port`, `auth` production code.
  Their seams are sufficient; the extension adapts them from outside.

## Deltas against the blind spec

The blind spec is 93 usecases and 36 invariants. Most of it survives unchanged —
the shape it asked for is the shape the project's seams already take, which is the
useful outcome of writing it blind. What follows is every place it had to bend,
and one place it should not have.

| Item | Blind spec said | Project reality | Decision | Justification |
|---|---|---|---|---|
| DX: the trust authority returns "scope, lifecycle state, epoch" | the extension point table gives the injected contract a `Scope` on its output | if application code returns the accepted type, the type is constructible by application code — INV-2 would be unachievable | **the spec is wrong and the code does not follow it**: `Resolver` returns a `Resolution` (plain data) and only `Authority` mints a `Scope` | The blind validators found this independently and rated it critical. It is the one place the spec contradicts its own invariant |
| UC-90/UC-92: "test-only construction" that production must not reach | the spec assumes an exported constructor that must then be fenced off | Go has no build-tag-free way to do that across a module boundary | **no test-only constructor exists**: a test wires `tenancy.Fixed` into an ordinary `Authority` | Stricter than asked. The forge that would have leaked was never built |
| INV-16: "an epoch that differs from the authority's current epoch is not used" | stated absolutely, while UC-66 admits a resolution cache and Q5/Q11 concede a window | asking the control plane per statement makes it a hot dependency of every request; never asking lets a deleted tenant finish a long request | **two settings, one stated window**: pinned to one `Bind` by default, zero with `Spec.Revalidate` | Neither absolute is defensible for every deployment; a window nobody can state is worse than either |
| INV-9 "total verb coverage" over four seams | the spec wants one method inventory per constrained seam | the repository seam already has one — [[D-030]]'s reflection test against `crud.Core` — and the other three do not | **reuse it for the repository; owe the other three** | A second copy of the most security-critical decorator in the repository would be the DRY violation, not the coverage |
| Ownership as "which attribute carries ownership" | one attribute, everywhere in the spec | a child table owned through its parent has no attribute of its own | **`Ownership` is an interface**; `Column` and `Through` are two implementations | Both blind validators flagged this as spec-level hardcode |
| UC-45 "a late datasource switch inside an open unit of work" | implies the framework multiplexes one repository across sources | a repository is bound to one source ([[UC-012]]), and `Identified.DataSource()` takes no context, so per-context routing would break transaction scoping | **the directory resolves; the application binds** | Fighting the kernel here would break [[D-041]] and [[D-061]] to serve one usecase the application can serve itself |
| UC-35 a replayed foreign pagination cursor | asks for it to be indistinguishable from a nonexistent one | the cursor is the sort tuple ([[D-028]]); a boundary value is an oracle in the same class as a unique-constraint create | **documented as a known exclusion**, alongside the one [[UC-004]] already records | The blind coverage validator called its acceptance self-contradictory, and it is |
| UC-6 "a declared tenant-owned resource" the root can check it did not forget | wants a way to enumerate what should be tenanted | a registry of repositories is on the roadmap's forbidden list | **refused**, and named in the profile | A service locator to catch a missing middleware trades the invariant for the convenience |
| UC-78 grant issuance | assumes the extension issues grants | issuance is deployment policy, and the spec's own scope section calls the control plane out of scope | **the extension accepts and bounds a grant; it does not issue one** | Keeps the extension smaller and puts a security-critical decision where the policy lives |
| INV-25..29 privacy | phrased against "generic signals" without naming the emitter | the extension emits no signals at all — it has no telemetry seam | **the closed vocabulary is what a signal may carry**, and the refusals are proved to carry nothing else | The invariant is testable against the values, which is the half the extension owns |

Per axis of variation: topology has a real extension point (row/database
strategies as files); ownership shape has one (`Ownership`); identifier type has
one (`Value func(Reference) (any, error)`); lifecycle vocabulary does **not** vary
— it is a closed set, and a state the package does not declare refuses rather
than being admitted. Provider/driver choice varies and is deliberately outside
the package ([[D-116]]).

## Deltas against the multitenancy roadmap

These are deviations from `docs/roadmaps/2026-09-01-multitenancy-roadmap.md`
itself, and each one needs the roadmap updated in the same change.

| Roadmap statement | Project reality | Decision | Justification |
|---|---|---|---|
| `tenancy/` is a module: "exactly one published tenancy `go.mod`" | The extension architecture revision says "Stdlib-only implementations do not receive a module merely for symmetry. A package already in the root may stay there when it adds no third-party module graph and obeys the contract tiers." The shared-row, database and root-seam-adapter slices need only stdlib plus root packages. [[D-036]] also records that before the first tag every module requiring the root fails to walk its own module graph. | **`tenancy` is a package of the root module**, not a new `go.mod`. A provider adapter that takes a real third-party dependency (an RLS driver helper, a secret manager, a control-plane SDK) becomes its own module at that point, exactly as `jobspg` did for pgx. | The roadmap's stated invariant is *one extension, one public package*; the module boundary in this repository is a **third-party dependency** boundary ([[D-033]] [[D-036]] [[D-051]]), not an optionality boundary. `storage`, `cache`, `jobs` and `security` are all optional and all live in the root. Recorded as a decision doc. |
| `port.ChainService` is "illustrative until its milestone is accepted" | It exists: `port/service.go:54`, with `RestorableOf` discovery at `:41`. | Use it as a real seam for the service-level factory. | It shipped after the roadmap revision was written. |
| "the current executable `ExistsUnscopedOf` `Next` walk is a blocker that must become exact-outer/explicitly forwarded or fail closed" | Still true: `crud/executor.go:141` walks `Nexter`. `security.gate.saveTarget` (`security.go:656`) is its only caller, and `sqlrepo` its only implementor. | Fix it in `crud` before the tenancy decorator exists, and give `security.gate` an explicit, scope-applying `ExistsUnscoped` so stacked gates keep working. | [[D-030]] and [[D-061]] already decided this rule for `InsertBatch` and `UnsafeBulkInsert`; `ExistsUnscopedOf` is the one executable effect that never got it. Without the fix a tenancy gate is an opaque wrapper an outer gate can probe *past*, which is a cross-tenant existence oracle. |
| "no tenancy profile may cite `jobspg`" | Still true; `jobspg`/`jobsredis` are `BUILDING`. | The jobs adapter is tested against the in-memory driver and the `jobs` contracts only, and the published profile cites no PostgreSQL job evidence. | The roadmap's own gate 10. |
| "`tenants.UnitOfWork(...).Orders()` … are not framework package APIs" | Nothing like it exists. | Keep it that way; the extension returns middleware and providers only. | — |

## Stop signals raised

1. **The module-versus-package question above is an owner decision.** It is
   settled here in favour of a root package because the extension-architecture
   revision states the sizing rule explicitly and every existing optional
   subsystem follows it, but the multitenancy revision says the opposite in four
   places. Both documents are updated in this change; if the owner wants a
   separate `go.mod` anyway, the move is mechanical (one `go.mod`, one `go.work`
   line, `replace` lines in `test/` and `_examples/`) and no source changes.
2. **`crud.ExistsUnscopedOf` is a public contract with one caller and one
   implementor in this repository, and unknown consumers outside it.** Making it
   exact-outer is a behaviour change for a consumer whose own decorator sits
   between `security.Gate` and `sqlrepo` and relied on the walk. That is exactly
   the tunnelling [[D-061]] forbids, so the change is a fix rather than a
   regression, but it is called out here rather than slipped in.
