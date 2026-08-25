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

## Index
| ID | Use case | Actor | Status |
|----|----------|-------|--------|
| [UC-001](UC-001-expose-a-crud-api-without-handlers.md) | Expose a full CRUD API for a resource without writing handlers | application author | covered |
| [UC-002](UC-002-let-an-untrusted-client-query.md) | Let an untrusted client filter, sort, page and search | untrusted client, HTTP or gRPC | partially covered |
| [UC-003](UC-003-partial-update-absent-vs-null.md) | Apply a partial update that tells absent from null | client sending a partial update | covered |
| [UC-004](UC-004-isolate-tenants.md) | Isolate tenants so a caller cannot see or touch another's rows | application author | partially covered |
| [UC-005](UC-005-run-repository-work-in-an-orm-transaction.md) | Run repository work inside a transaction the ORM owns | application author | covered |
| [UC-006](UC-006-query-and-sort-across-relations.md) | Query and sort across relations, from the wire and from Go | client and application author | covered |
| [UC-007](UC-007-write-typed-compile-checked-queries.md) | Write typed, compile-checked queries in Go | application author | covered |
| [UC-008](UC-008-write-many-rows-in-one-statement.md) | Write many rows in one statement | application author | partially covered |
| [UC-009](UC-009-survive-concurrent-writers.md) | Survive concurrent writers | application author | covered |
| [UC-010](UC-010-adopt-an-existing-orm-model.md) | Adopt an existing ORM's model without changing it | application author on ent or gorm | covered |
| [UC-011](UC-011-test-repository-behaviour-without-a-database.md) | Test repository behaviour without a database | application author writing tests | partially covered |
| [UC-012](UC-012-talk-to-more-than-one-database.md) | Talk to more than one database in one process | application author | covered |
| [UC-013](UC-013-business-rules-between-handler-and-repository.md) | Insert business rules between the handler and the repository | application author | covered |
| [UC-014](UC-014-keep-generated-artefacts-in-sync.md) | Keep generated artefacts in sync with the model | application author and reviewer | covered |
| [UC-015](UC-015-map-a-failure-to-the-transport.md) | Map a failure to the transport correctly | client reading a status, HTTP or gRPC, and application author | covered |
| [UC-016](UC-016-hide-rows-permanently-at-the-repository-level.md) | Hide rows permanently at the repository level | application author | covered |
| [UC-017](UC-017-get-every-error-for-one-payload-at-once.md) | Get every error for one payload in one response | client rendering a form, and application author | covered |
| [UC-018](UC-018-consume-another-services-crud-api.md) | Consume another service's CRUD API | the application author on the calling side | covered |
| [UC-019](UC-019-authenticate-a-request-and-let-the-repository-see-who-it-is.md) | Authenticate a request and let the repository see who it is | the application author, on behalf of every caller | covered |
| [UC-020](UC-020-authorize-without-a-policy-per-endpoint.md) | Authorize by role and permission without a policy per endpoint | the application author protecting every resource | covered |

## Coverage map
| Use case | Flows |
|---|---|
| [UC-001](UC-001-expose-a-crud-api-without-handlers.md) | [[FL-001]] [[FL-002]] [[FL-003]] [[FL-004]] [[FL-011]] [[FL-012]] [[FL-013]] [[FL-015]] |
| [UC-002](UC-002-let-an-untrusted-client-query.md) | [[FL-001]] [[FL-012]] [[FL-011]] [[FL-013]] |
| [UC-003](UC-003-partial-update-absent-vs-null.md) | [[FL-002]] [[FL-004]] [[FL-010]] [[FL-011]] |
| [UC-004](UC-004-isolate-tenants.md) | [[FL-007]] [[FL-008]] [[FL-005]] [[FL-006]] [[FL-011]] |
| [UC-005](UC-005-run-repository-work-in-an-orm-transaction.md) | [[FL-009]] [[FL-002]] |
| [UC-006](UC-006-query-and-sort-across-relations.md) | [[FL-005]] [[FL-006]] [[FL-001]] [[FL-012]] |
| [UC-007](UC-007-write-typed-compile-checked-queries.md) | [[FL-010]] [[FL-004]] [[FL-005]] |
| [UC-008](UC-008-write-many-rows-in-one-statement.md) | [[FL-002]] [[FL-008]] |
| [UC-009](UC-009-survive-concurrent-writers.md) | [[FL-002]] [[FL-003]] [[FL-009]] [[FL-011]] |
| [UC-010](UC-010-adopt-an-existing-orm-model.md) | [[FL-004]] [[FL-003]] [[FL-009]] [[FL-010]] |
| [UC-011](UC-011-test-repository-behaviour-without-a-database.md) | [[FL-001]] [[FL-002]] [[FL-004]] |
| [UC-012](UC-012-talk-to-more-than-one-database.md) | [[FL-009]] [[FL-016]] |
| [UC-013](UC-013-business-rules-between-handler-and-repository.md) | [[FL-001]] [[FL-002]] [[FL-003]] [[FL-011]] [[FL-013]] [[FL-015]] |
| [UC-014](UC-014-keep-generated-artefacts-in-sync.md) | [[FL-010]] [[FL-004]] [[FL-015]] |
| [UC-015](UC-015-map-a-failure-to-the-transport.md) | [[FL-011]] [[FL-013]] [[FL-014]] [[FL-015]] |
| [UC-016](UC-016-hide-rows-permanently-at-the-repository-level.md) | [[FL-004]] [[FL-007]] [[FL-005]] [[FL-006]] |
| [UC-017](UC-017-get-every-error-for-one-payload-at-once.md) | [[FL-011]] [[FL-014]] [[FL-017]] |
| [UC-018](UC-018-consume-another-services-crud-api.md) | [[FL-018]] [[FL-013]] [[FL-015]] |
| [UC-019](UC-019-authenticate-a-request-and-let-the-repository-see-who-it-is.md) | [[FL-019]] [[FL-007]] [[FL-008]] [[FL-011]] [[FL-013]] |
| [UC-020](UC-020-authorize-without-a-policy-per-endpoint.md) | [[FL-020]] [[FL-007]] [[FL-008]] [[FL-011]] |

## Gaps

The roadmap, worst first. The first group is behaviour that does not match a
stated guarantee — four of it live, with entry 5 kept as a closed one because
the numbers are cited elsewhere; the rest are missing proof or documented sharp
edges that need a decision.

### Guarantees that do not hold

1. **[UC-002] An unknown top-level key in a query document is silently ignored.**
   The request body is decoded without rejecting unknown fields, so a misspelled
   `filter` key parses cleanly and the endpoint returns the whole table. This
   directly contradicts the use case's headline — "an unknown field is rejected,
   never ignored" — which holds inside the filter but not at the document's own
   level, where a client's typo actually lands. Same for unknown query-string
   parameters. Untested and unguarded.

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
   `repo/decorators/security/principal_test.go` pins the refused create and
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

18. **[UC-012] Two ways of writing a scoped binding do nothing, silently.**
    Naming a transaction rather than the database keys the binding on the
    transaction handle, which no repository matches, so the write goes to the
    pool outside the transaction and reports success. Naming nothing at all
    degrades to capturing everything. Both look correct at the call site.

19. **[UC-002] Query-string and JSON doors disagree in four places** — an
    unparsable `isNull` value flipping to `IS NOT NULL`, a scalar term dropping
    extra comma-separated values, byte-slice columns taking base64 from one door
    and raw text from the other, and a range operator against `null` binding a
    nil instead of being refused.

20. **[UC-002] The budgets have holes.** Search predicates, sort terms and select
    entries are not charged against the condition budget; the search-field list
    has no length cap; the depth budget does not bound a preload path; and inside
    a preload's own filter both counters restart. Separately, the page cap is a
    repository setting rather than part of the query configuration, so an
    endpoint reviewed through its query configuration alone looks bounded and is
    not.

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
