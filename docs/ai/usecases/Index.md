# Use cases

A use case here is an abstract scenario a consumer of vv needs covered,
written in the consumer's language. It says what somebody is trying to achieve
and what must be observably true for them to have achieved it. It contains no
implementation: no file paths, no function names, no package names. The only
identifiers that appear are the ones a consumer literally types — `repo.Update`,
`crud.Where`, `?preload=` — because those are part of the vocabulary, not of the
mechanism.

**A use case links only to flows.** If a statement needs a file or a function to
be true, it belongs in a flow ([[FL-001]]…[[FL-013]]) and the use case links to
it instead. That is the whole point: a flow goes stale when a file moves, a use
case goes stale only when the product changes. A use case that names a symbol
has become a flow and should be split.

The valuable part of each file is **What must hold** — a numbered list of
guarantees, each checkable from outside the library. Read it as the contract. A
change that breaks one of those lines breaks the use case, whether or not a test
happens to catch it.

## How to use this directory

**Adding a feature.** Look here first. If a use case already covers what you are
about to build, you are implementing an existing guarantee, and its **Status**
tells you what was already missing — start there rather than rediscovering it. If
nothing covers it, the feature needs a use case before it needs a design, and the
gap list below is where the owner decides whether it is worth building.

**Changing behaviour.** Find every use case whose guarantees touch what you are
changing. A guarantee is a promise somebody may already be relying on: if your
change contradicts one, that is a product decision and needs saying out loud, not
a refactor. If it makes one *more* true, move the Status.

**Reviewing.** The **Out of scope** section of each file exists so nobody reads
more into a use case than is there. When a bug report says "I expected X", check
whether X is in a guarantee, in Out of scope, or in neither — the answer is
usually already written down.

**Statuses are honest on purpose.** "Partially covered" with a precise account of
what is missing is more useful than "covered", and the gap list at the bottom of
this page is the roadmap.

## Layout

Two directories, and the split is about blast radius rather than about size.

```
general/            what holds across the whole framework, no single owner
modules/<module>/   what holds for one module, plus that module's own
                    happy- and edge-case catalogue in <Module>.md
```

A use case lives in `general/` only when no one module could deliver it alone —
UC-001 is every transport and the repository behind them, UC-015 is the error
contract from a driver's `SQLSTATE` to a status code four layers away. Everything
else names an owner, and the owner is the directory. Where a use case genuinely
constrains two modules the second one is listed in the **Also constrains** column
below, so a change to that module still finds it.

`<Module>.md` inside each module directory is a different kind of file from a
`UC-NNN`. A use case is a settled contract with a status; `<Module>.md` is the
release-readiness sweep — the happy paths and the edge cases a consumer will
actually hit, the top-level DX they should get, and a verdict on whether today's
code delivers it. It is allowed to be wrong about the code in a way a use case is
not, and it says where it is unsure. `general/General.md` is the same sweep for
the framework as a whole.

## Readiness indexes

For newcomer-oriented navigation, see the [module index](modules/Index.md) and
[general index](general/Index.md). The cross-module tag assessment is
[Release readiness](Release-readiness.md).

## Index
| ID | Use case | Actor | Lives in | Also constrains | Status |
|----|----------|-------|---------|-----------------|--------|
| [UC-001](general/UC-001-expose-a-crud-api-without-handlers.md) | Expose a full CRUD API for a resource without writing handlers | application author | `general` | — | covered |
| [UC-002](modules/query/UC-002-let-an-untrusted-client-query.md) | Let an untrusted client filter, sort, page and search | untrusted client, HTTP or gRPC | `query` | crudhttp · crudgrpc | covered |
| [UC-003](modules/sqlrepo/UC-003-partial-update-absent-vs-null.md) | Apply a partial update that tells absent from null | client sending a partial update | `sqlrepo` | codegen | covered |
| [UC-004](modules/security/UC-004-isolate-tenants.md) | Isolate tenants so a caller cannot see or touch another's rows | application author | `security` | sqlrepo | covered |
| [UC-005](modules/sqlrepo/UC-005-run-repository-work-in-an-orm-transaction.md) | Run repository work inside a transaction the ORM owns | application author | `sqlrepo` | adapters | covered |
| [UC-006](modules/sqlrepo/UC-006-query-and-sort-across-relations.md) | Query and sort across relations, from the wire and from Go | client and application author | `sqlrepo` | crud | covered |
| [UC-007](modules/specs/UC-007-write-typed-compile-checked-queries.md) | Write typed, compile-checked queries in Go | application author | `specs` | codegen | covered |
| [UC-008](modules/sqlrepo/UC-008-write-many-rows-in-one-statement.md) | Write many rows in one statement | application author | `sqlrepo` | security | covered |
| [UC-009](modules/sqlrepo/UC-009-survive-concurrent-writers.md) | Survive concurrent writers | application author | `sqlrepo` | adapters | covered |
| [UC-010](modules/crud/UC-010-adopt-an-existing-orm-model.md) | Adopt an existing ORM's model without changing it | application author on ent or gorm | `crud` | adapters | covered |
| [UC-011](modules/crudtest/UC-011-test-repository-behaviour-without-a-database.md) | Test repository behaviour without a database | application author writing tests | `crudtest` | — | partially covered |
| [UC-012](modules/sqlrepo/UC-012-talk-to-more-than-one-database.md) | Talk to more than one database in one process | application author | `sqlrepo` | adapters | covered |
| [UC-013](modules/port/UC-013-business-rules-between-handler-and-repository.md) | Insert business rules between the handler and the repository | application author | `port` | crudhttp | covered |
| [UC-014](modules/codegen/UC-014-keep-generated-artefacts-in-sync.md) | Keep generated artefacts in sync with the model | application author and reviewer | `codegen` | specs | covered |
| [UC-015](general/UC-015-map-a-failure-to-the-transport.md) | Map a failure to the transport correctly | client reading a status, HTTP or gRPC, and application author | `general` | — | covered |
| [UC-016](modules/sqlrepo/UC-016-hide-rows-permanently-at-the-repository-level.md) | Hide rows permanently at the repository level | application author | `sqlrepo` | security | covered |
| [UC-017](modules/faults/UC-017-get-every-error-for-one-payload-at-once.md) | Get every error for one payload in one response | client rendering a form, and application author | `faults` | errs | covered |
| [UC-018](modules/remote/UC-018-consume-another-services-crud-api.md) | Consume another service's CRUD API | the application author on the calling side | `remote` | port | covered |
| [UC-019](modules/auth/UC-019-authenticate-a-request-and-let-the-repository-see-who-it-is.md) | Authenticate a request and let the repository see who it is | the application author, on behalf of every caller | `auth` | authhttp | covered |
| [UC-020](modules/security/UC-020-authorize-without-a-policy-per-endpoint.md) | Authorize by role and permission without a policy per endpoint | the application author protecting every resource | `security` | auth | covered |
| [UC-021](modules/vvdb/UC-021-configure-a-database-once-in-one-file.md) | Configure a database once, in one file, for any engine | the application author | `vvdb` | utils | covered |
| [UC-022](modules/vvgoose/UC-022-run-and-generate-database-migrations.md) | Generate and run database migrations from the application config | the application author | `vvgoose` | vvdb · vvcfg | covered |
| [UC-023](modules/auth/UC-023-sign-people-in-without-writing-an-auth-system.md) | Sign people in without writing an auth system | the application author | `access` | accessjwt · revokeredis · accessnet/gin/fiber | covered |
| [UC-024](modules/cache/UC-024-cache-recreatable-values-without-unbounded-work.md) | Cache recreatable values without unbounded work | the application author | `cache` | cachememory | covered |
| [UC-025](modules/health/UC-025-say-whether-this-replica-should-take-traffic.md) | Say whether this replica should take traffic | the application author, for an orchestrator, a load balancer and an operator | `health` | appfiber | covered |
| [UC-026](modules/runtime/UC-026-run-background-work-without-losing-a-worker-silently.md) | Run background work without losing a worker silently | the application author | `runtime` | appfx · jobs | covered |
| [UC-027](modules/app/UC-027-run-one-codebase-as-an-api-a-worker-and-a-seed-command.md) | Run one codebase as an API, a worker and a seed command | the application author | `app` | appfx · appfiber · runtime | covered |

## Coverage map
| Use case | Flows |
|---|---|
| [UC-001](general/UC-001-expose-a-crud-api-without-handlers.md) | `general` | — | [[FL-001]] [[FL-002]] [[FL-003]] [[FL-004]] [[FL-011]] [[FL-012]] [[FL-013]] [[FL-015]] |
| [UC-002](modules/query/UC-002-let-an-untrusted-client-query.md) | `query` | crudhttp · crudgrpc | [[FL-001]] [[FL-012]] [[FL-011]] [[FL-013]] |
| [UC-003](modules/sqlrepo/UC-003-partial-update-absent-vs-null.md) | `sqlrepo` | codegen | [[FL-002]] [[FL-004]] [[FL-010]] [[FL-011]] |
| [UC-004](modules/security/UC-004-isolate-tenants.md) | `security` | sqlrepo | [[FL-007]] [[FL-008]] [[FL-005]] [[FL-006]] [[FL-011]] |
| [UC-005](modules/sqlrepo/UC-005-run-repository-work-in-an-orm-transaction.md) | `sqlrepo` | adapters | [[FL-009]] [[FL-002]] |
| [UC-006](modules/sqlrepo/UC-006-query-and-sort-across-relations.md) | `sqlrepo` | crud | [[FL-005]] [[FL-006]] [[FL-001]] [[FL-012]] |
| [UC-007](modules/specs/UC-007-write-typed-compile-checked-queries.md) | `specs` | codegen | [[FL-010]] [[FL-004]] [[FL-005]] |
| [UC-008](modules/sqlrepo/UC-008-write-many-rows-in-one-statement.md) | `sqlrepo` | security | [[FL-002]] [[FL-008]] |
| [UC-009](modules/sqlrepo/UC-009-survive-concurrent-writers.md) | `sqlrepo` | adapters | [[FL-002]] [[FL-003]] [[FL-009]] [[FL-011]] |
| [UC-010](modules/crud/UC-010-adopt-an-existing-orm-model.md) | `crud` | adapters | [[FL-004]] [[FL-003]] [[FL-009]] [[FL-010]] |
| [UC-011](modules/crudtest/UC-011-test-repository-behaviour-without-a-database.md) | `crudtest` | — | [[FL-001]] [[FL-002]] [[FL-004]] |
| [UC-012](modules/sqlrepo/UC-012-talk-to-more-than-one-database.md) | `sqlrepo` | adapters | [[FL-009]] [[FL-016]] |
| [UC-013](modules/port/UC-013-business-rules-between-handler-and-repository.md) | `port` | crudhttp | [[FL-001]] [[FL-002]] [[FL-003]] [[FL-011]] [[FL-013]] [[FL-015]] |
| [UC-014](modules/codegen/UC-014-keep-generated-artefacts-in-sync.md) | `codegen` | specs | [[FL-010]] [[FL-004]] [[FL-015]] [[FL-029]] [[FL-002]] |
| [UC-015](general/UC-015-map-a-failure-to-the-transport.md) | `general` | — | [[FL-011]] [[FL-013]] [[FL-014]] [[FL-015]] |
| [UC-016](modules/sqlrepo/UC-016-hide-rows-permanently-at-the-repository-level.md) | `sqlrepo` | security | [[FL-004]] [[FL-007]] [[FL-005]] [[FL-006]] |
| [UC-017](modules/faults/UC-017-get-every-error-for-one-payload-at-once.md) | `faults` | errs | [[FL-011]] [[FL-014]] [[FL-017]] |
| [UC-018](modules/remote/UC-018-consume-another-services-crud-api.md) | `remote` | port | [[FL-018]] [[FL-013]] [[FL-015]] |
| [UC-019](modules/auth/UC-019-authenticate-a-request-and-let-the-repository-see-who-it-is.md) | `auth` | authhttp | [[FL-019]] [[FL-007]] [[FL-008]] [[FL-011]] [[FL-013]] |
| [UC-020](modules/security/UC-020-authorize-without-a-policy-per-endpoint.md) | `security` | auth | [[FL-020]] [[FL-007]] [[FL-008]] [[FL-011]] |
| [UC-021](modules/vvdb/UC-021-configure-a-database-once-in-one-file.md) | `vvdb` | utils | [[FL-021]] |
| [UC-022](modules/vvgoose/UC-022-run-and-generate-database-migrations.md) | `vvgoose` | vvdb · vvcfg | [[FL-022]] |
| [UC-023](modules/auth/UC-023-sign-people-in-without-writing-an-auth-system.md) | `access` | accessjwt · revokeredis · accessnet/gin/fiber | [[FL-023]] |
| [UC-024](modules/cache/UC-024-cache-recreatable-values-without-unbounded-work.md) | `cache` | cachememory | [[FL-025]] |
| [UC-025](modules/health/UC-025-say-whether-this-replica-should-take-traffic.md) | `health` | appfiber | [[FL-027]] [[FL-024]] |
| [UC-026](modules/runtime/UC-026-run-background-work-without-losing-a-worker-silently.md) | `runtime` | appfx · jobs | [[FL-028]] |
| [UC-027](modules/app/UC-027-run-one-codebase-as-an-api-a-worker-and-a-seed-command.md) | `app` | appfx · appfiber · runtime | [[FL-030]] |

## Gaps

The roadmap, worst first. The first group is behaviour that does not match a
stated guarantee — three of it live, with entries 1 and 5 kept as closed ones
because the numbers are cited elsewhere; the rest are missing proof or documented
sharp edges that need a decision.

### Guarantees that do not hold

1. ~~**[UC-002] An unknown top-level key in a query document is silently
   ignored.**~~ **Closed, both doors.** The document decodes with unknown fields
   refused, and the refusal names the offending key and offers the accepted set
   back — a test walks the struct tags to prove that list cannot drift from the
   struct. The query string is closed as far as it can be: a parameter one edit
   away from one of ours is refused, including a transposition, because
   "prelaod" is the typo people actually make. It stops there on purpose — a
   handler reads its own parameters off the same URL, so an unrelated name has
   to pass, and the control test pins that it does. The entry keeps its number
   because the numbers are cited elsewhere.

2. **[UC-004] A create is not narrowed, and a hand-written policy does not guard
   it either.** An upsert has no `WHERE` for the tenant predicate to live in, so
   only a row-level check can refuse it. The provided helper installs one; a
   policy that declares only the narrowing — which is what "row-level security in
   one line" looks like — leaves a create into another tenant unconstrained. No
   test covers that shape.

   **Narrower since the `auth` subsystem landed.** `security.ScopeAttr` and
   `ScopeSubject` are the principal-driven form, and they are built on
   `ScopeField` rather than beside it, so they inherit the row check and the
   frozen column — `TestScopeAttrNarrowsInSQLAndFreezesTheColumn` in
   `crud/decorators/security/principal_test.go` pins the refused create and
   carries the control that a create into the caller's own tenant still
   succeeds. What remains open is the shape this entry names: a `Policy` written
   by hand with `Scope` set and `Inspect` nil. Nothing refuses that, and nothing
   tests it ([[UC-020]]).

3. **[UC-004] A save is an existence oracle, and so is a unique-constraint
   collision.** To refuse overwriting an invisible row, the gate probes for it
   *without* the narrowing: 403 for another tenant's id, success for an unused
   one. Deliberate and tested. The unguarded twin is not: a create colliding on a
   unique index over another column returns a 409 carrying the driver's
   constraint name, for a row the caller cannot see.

   Both halves now have a decision, and they are different decisions. The
   constraint name leaving the process was [[D-044]]'s, and phase 4 closed it:
   no body names a constraint any more. The oracle itself is [[D-042]]'s, and D-042 does **not**
   close it — a unique constraint a public endpoint can trigger is an oracle by
   construction. **Phase 7 landed the adjustability it promised** and the second
   half narrows accordingly: the disclosure is now per-constraint opt-out
   (`probe.Skip`), scope-aware probing from the `security.Policy`
   (`probe.WithScope`), code-only mode (`probe.CodeOnly`), and a default that
   never echoes the offending value (`probe.WithValues` is opt-in, pinned live by
   `TestTheOffendingValueReachesTheBodyOnlyWhenAsked`). The scope narrows the
   probe's *unique* terms only: a foreign-key term reads the parent table and a
   restrict term the child, and the model's own predicate names neither, so
   `Skip` is the control there. The gate's own unnarrowed existence probe — the
   first half — does not move.

4. **[UC-008] A caller-supplied limit desynchronises a policy's row-level check
   from the statement.** The inspected rows honour the limit; the `UPDATE` and
   `DELETE` do not carry one. So a filtered write with a limit inspects one row
   and writes every matching row.

5. ~~**[UC-014] The generator does not know about the version column.**~~
   **Closed, and it was half stale when it was written.** The generator did learn
   the `version`/`lock` option, and both halves have been pinned for some time:
   the lock leaves the DTO and stays in the metamodel, and the declaration the
   generated DTO implies is one the repository accepts, with a DTO naming the
   lock refused as the control. What stayed true was the second sentence — no
   model in the test tree carried a version column, so none of that ran against a
   real generated artefact. Phase 8 added one, generated with the resource half
   as well, so the case is reachable rather than argued. The entry keeps its
   number because the numbers are cited elsewhere.

### Guarantees that hold but are not proven

6. ~~**[UC-014] The drift test only catches a *removed* field, and its harder
   half is behind Docker.**~~ **Closed by phase 8, both halves.** A generated
   artefact now asserts at package initialisation that it covers every writable
   column, and that check reads the *compiled* model rather than the generator's
   own view of the source — two independent derivations, which is what
   regenerate-and-diff could never be, since it only ever measures the generator
   against itself. And the flag-driven drift test moved out of the integration
   suite: it needs no database, it runs under `make unit`, and its own control
   tampers with the regenerated copy so a helper that read one file twice cannot
   stay green. [[D-050]] is the decision. The entry keeps its number because the
   numbers are cited elsewhere.

7. **[UC-005] The reverse rollback is unproven.** No test writes through an ORM
   *inside* a repository-owned transaction and shows the rollback taking the
   ORM's write. Nor does any test show that a joined inner block cannot roll back
   independently.

8. **[UC-005] Preloads inside a transaction are unproven**, as is a partial
   update taking the row lock by itself when a transaction is present. Both are
   structural in the code; neither has a test.

9. **[UC-011] The handler test seam is not shipped.** The handler takes an
   interface and this repository's own tests exercise it thoroughly, but the
   stand-in repository and every helper around it are internal. An application
   rewrites them or falls back to binding a real repository to the recorder,
   which nothing demonstrates.

10. **[UC-002] The preload allow-list is not checked hop by hop.** An entry naming
    a deep path implicitly authorises loading every relation on the way to it.

11. **[UC-004] A per-principal nested *sort* narrowing has no test.** The
    table-level equivalent does, and they share the code path.

12. **[UC-004] The page total can be computed without the per-request relation
    narrowing.** A list filtered through a relation can return correctly narrowed
    items alongside a total counted over a wider set.

13. **[UC-010] The ORM-callback claim is proven on one side only.** That an ORM
    hook does not fire is executed for gorm and reasoned for ent, because no ent
    schema in the test tree declares one.

14. **[UC-007] The existence and update-by verbs are covered only by the
    database-backed suite**, and the update-by refusal of an empty filter is
    tested for one spelling where its delete counterpart is tested for thirteen.

15. **[UC-012] Multi-database is proven for one adapter on one engine.** Scoped
    bindings with pgx, and any combination across two engines, are untested.
    Guarantee 10, the per-handle schema catalog, is the exception: it runs live
    through both adapters and on all four engines.

### Sharp edges that need a decision, not a test

16. ~~**[UC-015] Every status but 500 echoes the error's own text.**~~
    **Closed by phase 4.** `err.Error()` reaches no body any more, classified or
    not: the render layer builds one envelope from the fault's public
    projection, and a refusal carrying no fault is turned into a synthesised one
    first, so there is nowhere for a driver's sentence to arrive from. [[D-044]]
    is the decision; the proof is a render over **every** captured corpus entry
    rather than one hand-written case. It is UC-015's guarantee 11 and it holds.
    The entry keeps its number because the numbers are cited elsewhere.

17. **[UC-016] A create can resurrect a hidden row.** The rule cannot reach an
    upsert, so a save carrying a tombstone's key overwrites it — and under a gate
    the "does this row exist" probe goes *through* the rule, so the tombstone
    reads as absent and the write is treated as a fresh create. Silently, through
    the ordinary create endpoint.

18. ~~**[UC-012] Two ways of writing a scoped binding do nothing, silently.**~~
    **Closed by [[D-082]].** `BindExecutor` associates the canonical source and
    executor; transaction-as-source and strict inferred mismatches return typed
    `ErrExecutorScope` before a datasource call. Unconditional capture remains
    only as the explicitly named `WithUnsafeExecutor` waiver.

19. **[UC-002] Query-string and JSON doors disagree in four places** — an
    unparsable `isNull` value flipping to `IS NOT NULL`, a scalar term dropping
    extra comma-separated values, byte-slice columns taking base64 from one door
    and raw text from the other, and a range operator against `null` binding a
    nil instead of being refused.

20. **[UC-002] The budgets still have holes.** Search predicates and select
    entries are not charged against the condition budget; the search-field list
    has no length cap; the depth budget does not bound a preload path; and inside
    a preload's own filter both counters restart. Sort terms have left this list:
    they have a cap of their own, added with the list-length cap after an audit
    found the condition budget was measuring names and nothing was measuring
    volume ([[D-060]]). Separately, the page cap is a repository setting rather
    than part of the query configuration, so an endpoint reviewed through its
    query configuration alone is reviewed through the wrong file — narrower than
    it was, since an endpoint that declares nothing now refuses `unpaged`
    outright.

21. **[UC-004] A frozen field name is never validated against the model**, so a
    typo silently protects nothing. Contrast the scope and relation-path
    declarations, which fail at start-up.

22. ~~**[UC-001] There are three HTTP bindings — Fiber, Gin and net/http — and
    no other transport.**~~ **Closed by phase 9**, which added the fourth and
    the first one that is not HTTP. There are now four bindings — Fiber, Gin,
    net/http and gRPC — and what is left unwritten is GraphQL and a queue
    consumer. The `net/http` one is free: it needs
    no dependency, so it ships in the library rather than as a module of its
    own. A project on Echo still writes its own routes, and one on chi or
    gorilla/mux can register the `net/http` handler methods individually
    instead. What none of them writes is the status table, the key coercion, the
    violations pipeline or the create-time field clearing — and the fourth
    binding is the evidence rather than the claim, because writing a whole
    transport on another protocol needed nothing added to the shared half.
    Where the four differ is a table in [[FL-013]]; a fifth would have to add
    its column. What remains genuinely unwritten is a queue consumer, and it is
    a different shape: no request, no key in a path and no status to map, so it
    calls a service directly and never builds a command.
