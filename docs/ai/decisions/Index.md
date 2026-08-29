# Decisions & invariants

This directory pins the things that could reasonably have gone the other way: a
design choice with a real alternative, an invariant the code depends on but
cannot express in a type, and the owner's own corrections. Every file names the
failure mode that made the answer what it is, and says what a future change must
not do.

What does not belong here: how to use a feature (that is
`docs/usage-guides/`), what a function does (that is the doc comment), or a
change with no alternative worth naming.

## How to use this directory

**Read a decision before changing code when** you are about to touch anything
listed in its *Where it lives* section, or when a piece of code looks wrong in a
way that is easy to "fix". The security, relation-narrowing and DISTINCT rules in
particular all look like bugs from a distance and are not. If a change would
violate an *Invariant* line, the change is wrong or the decision needs
superseding — not both quietly at once.

**Add a decision when** you make a choice whose opposite is defensible, or when
you fix a bug whose fix could plausibly be reverted by someone who does not know
what it prevented. Give it the next free number, never reuse one, and mark a
replaced decision `superseded by D-0NN` rather than deleting it.

**Mark it `open`** when the tension is real and unresolved. An open decision
still says what not to do while it is open — see D-024 through D-027.

**Mark it `in force from phase N`** when the rule is settled but the code it
governs is not written yet. A bare `accepted` reads as *the tree obeys this*, and
a binding rule the code visibly breaks teaches the next reader that decisions are
aspirational. The rule still binds: it forbids working around it in the meantime,
and it is what the implementing phase has to satisfy. Such a decision heads its
evidence section `Proven by (owed)` and names the tests that phase must write —
so an agent checking that every symbol a doc names still exists knows those are
deliberate rather than rot. **The set is now empty.** D-038 left it when phase 1
landed `errs`, D-045 when phase 5 landed `port`, D-041 when phase 6 landed
`catalog`, D-042 when phase 7 landed `probe`, and D-043 when phase 8 landed the
generated mappers and the start-up refusal it owed; D-045's own named follow-up
— move the violations pipeline when a second transport wants it — was
discharged by phase 9 and is recorded there rather than left as a paragraph to
rediscover. D-040 and D-044 were the last two carrying a partial status and both
are now bare `accepted`: D-040's owed 503 and `Retry-After` are pinned by
`TestARetryableFailureIsA503WithRetryAfter` in all three HTTP bindings, and
D-044's owed rendered body by `TestARenderedBodyNamesNothingInternal` in
`port/porthttp/render_test.go`. An *accepted* decision may still owe evidence: D-039
did and phase 2 paid it, D-038 did — the tree walk through a multi-error — and
phase 3 paid that. Nothing in `docs/` heads a section `Proven by (owed)` today,
and the next decision written before its code does should say so here.

## Index

| ID | Invariant (one line) | Status | Area |
|----|----------------------|--------|------|
| [D-001](D-001-two-parameter-seam-three-parameter-facade.md) | A middleware is declarable with `[M, ID]` only, so call sites carry no explicit generics | accepted | core seam |
| [D-002](D-002-opt-models-three-states.md) | Absent, explicit null and value are three distinct outcomes for a nullable column | accepted | core seam |
| [D-003](D-003-closed-predicate-ast.md) | The predicate AST is closed; only `crud.Raw` emits caller text, and its markers must match its arguments | accepted | querying |
| [D-004](D-004-where-ands-never-replaces.md) | `crud.Where` appends; no option may remove or weaken a predicate another option added | accepted | querying |
| [D-005](D-005-relation-filter-is-a-correlated-exists.md) | A relation filter must not change the outer statement's cardinality | accepted | querying |
| [D-006](D-006-preload-is-a-batched-second-query.md) | One statement per relation per level; a preload refuses pagination | accepted | querying |
| [D-007](D-007-narrowing-crosses-a-relation-only-when-declared.md) | A scope covers its own `FROM`; the far side of a relation is narrowed only where declared | accepted | security |
| [D-008](D-008-out-of-scope-is-404-not-403.md) | A row hidden by a scope is `ErrNotFound`, never a denial | accepted | security |
| [D-009](D-009-context-executor-capture-is-unconditional.md) | `WithExecutor` reaches every repository; only `WithExecutorFor` restricts it | accepted | transactions & datasources |
| [D-010](D-010-update-is-load-diff-write.md) | `Update` writes only what changed, and locks only inside a transaction | accepted | writes |
| [D-011](D-011-save-is-jpa-shaped.md) | No key inserts, a key upserts, an unset `noauto` key is `ErrMissingID` | accepted | writes |
| [D-012](D-012-put-replaces-never-creates.md) | `PUT /:id` on a database-generated key 404s for an id that names no row | accepted | HTTP |
| [D-013](D-013-unknown-field-is-a-rejection.md) | Every field path resolves or the whole request fails before any SQL | accepted | querying |
| [D-014](D-014-sql-is-deterministic.md) | The same request compiles to byte-identical SQL and the same argument order | accepted | querying |
| [D-015](D-015-errors-are-sentinels.md) | Every branchable failure is reachable with `errors.Is` against a `crud` sentinel | accepted | errors |
| [D-016](D-016-one-module-crud-stays-stdlib-only.md) | One `go get`, no `replace`; `crud/` imports only the standard library | **superseded by D-033** (module half; the stdlib rule stands) | process & tooling |
| [D-017](D-017-orm-go-side-behaviour-does-not-run.md) | An vv write is exactly the statement vv built; no ORM builder, hook or privacy rule runs | accepted | interop |
| [D-018](D-018-dtos-and-metamodels-are-generated.md) | `vv_gen.go` is generated output, reproducible from the model source | accepted | process & tooling |
| [D-019](D-019-dialect-differences-are-not-observable.md) | The same call answers the same on every engine, except for eleven named differences | accepted | dialects |
| [D-020](D-020-tests-are-the-specification.md) | A test that could pass vacuously carries a control case that fails without the behaviour | accepted | process & tooling |
| [D-021](D-021-magic-is-preferred-to-go-orthodoxy.md) | Boilerplate is removed from the consumer; the magic must fail at build or start-up, never at request time | accepted | philosophy |
| [D-022](D-022-the-handler-takes-an-interface.md) | The handler holds `Repository[M, ID, U]` and never reaches past it | accepted | HTTP |
| [D-023](D-023-usage-guides-lead-with-what-you-get.md) | Both guides open with the resulting API; setup is Part II | accepted | docs |
| [D-024](D-024-distinct-and-the-forced-primary-key.md) | A `DISTINCT` query produces a valid statement or a readable refusal — never a 500, never a silent no-op | **open** | querying |
| [D-025](D-025-mapkey-collapses-non-comparable-keys.md) | Both ends of a relation agree on a key, and two different keys never collide | **open** | relations |
| [D-026](D-026-deleteall-fetches-victims-with-caller-options.md) | Every row a gated filtered write touches was shown to `Inspect` first | **open** | security |
| [D-027](D-027-intx-cross-database-capture-is-documented-not-enforced.md) | A repository never runs on a database it was not bound to, unless the application said so | **open** | transactions & datasources |
| [D-028](D-028-a-cursor-is-the-sort-tuple.md) | A cursor may only be used with the sort it was made for, and only when that sort ends in the primary key | accepted | querying |
| [D-029](D-029-aggregates-live-on-the-seam.md) | A summary read carries every narrowing a row read carries, and no client can compose one | accepted | querying, security |
| [D-030](D-030-a-new-verb-on-the-seam-is-a-decorator-obligation.md) | Every method added to `crud.Core` is overridden by the gate or has a written reason not to be | accepted | core seam, security |
| [D-031](D-031-soft-delete-is-a-statement-not-a-decorator.md) | Declaring a soft delete declares both the stamp and the read filter | accepted | querying |
| [D-032](D-032-a-replica-never-decides-a-write.md) | A read inside a transaction, or one that decides a write, always goes to the primary | accepted | transactions & datasources |
| [D-033](D-033-optional-dependencies-are-their-own-modules.md) | The root module has no third-party requirement; a package that needs one is its own module | **amended by D-036 and D-051** | process & tooling |
| [D-034](D-034-a-transport-binding-is-a-shell-over-crudhttp.md) | A transport binding owns routing, body binding and the response — everything else comes from `crudhttp` | **superseded by D-045** | HTTP |
| [D-035](D-035-a-prefix-only-breaks-a-collision.md) | A package is named for what it is; a prefix only breaks a collision, and it names the subsystem | accepted | process & tooling |
| [D-036](D-036-the-root-module-takes-no-third-party-requirement.md) | The root module may require a first-party module; third-party requirements still become their own module | accepted | process & tooling |
| [D-037](D-037-app-never-resolves-a-component-by-type.md) | No component is ever resolved by type; `app` holds no `map[reflect.Type]any` | accepted | philosophy |
| [D-038](D-038-a-fault-is-additive.md) | A fault wraps and never replaces; the `crud` sentinel underneath stays reachable with `errors.Is` | accepted | errors |
| [D-039](D-039-message-text-is-not-an-interface.md) | No classification and no field path comes from a driver's message text | accepted | errors |
| [D-040](D-040-a-retryable-class-is-not-a-client-error.md) | A lock timeout, deadlock or serialisation failure is never a 4xx, and the framework does not retry | accepted | errors |
| [D-041](D-041-the-catalog-is-per-physical-handle.md) | The catalog is keyed on the database handle, never global, and its absence fails at start-up | accepted | errors |
| [D-042](D-042-the-probe-is-advisory.md) | The probe may only narrow the truth; it never suppresses the driver's own violation | accepted | errors |
| [D-043](D-043-a-path-is-translated-one-hop-per-layer.md) | Each layer translates only the hop it owns; an unresolvable path is marked approximate, never guessed | accepted | errors |
| [D-044](D-044-the-public-payload-names-nothing-internal.md) | No response body names a constraint, table, column, SQLSTATE or engine number, at any status | accepted | errors |
| [D-045](D-045-the-shared-half-is-transport-neutral.md) | The shared half is transport-neutral; a binding is a shell over `port` (supersedes D-034) | accepted | HTTP |
| [D-046](D-046-the-classifier-is-keyed-on-dialect-sqlstate-native.md) | The classifier is keyed on `(dialect, sqlstate, native)`; SQLSTATE class alone is not a gate | accepted | errors |
| [D-047](D-047-a-faults-error-text-is-classification-only.md) | A fault's `Error()` names the kind, code, op, entity and count, and nothing a driver said | accepted | errors |
| [D-048](D-048-the-contract-manifest-is-closed.md) | A package joins the contract manifest only when a second implementation asks, and never when the standard library already contracts it | accepted | process & tooling |
| [D-049](D-049-the-kind-decides-the-status.md) | The kind decides the status; the sentinel decides only when there is no fault | accepted | errors |
| [D-050](D-050-the-generated-adapter-is-total.md) | A generated artefact covers every column its side of the wire carries, and a gap is a start-up refusal; hand-written stays partial | accepted | process & tooling, errors |
| [D-051](D-051-a-satellite-carries-one-dependency-decision.md) | A satellite isolates one dependency *decision*; several requires are one decision when no consumer can take one without the others | accepted | process & tooling |
| [D-052](D-052-a-grpc-resource-carries-documents-not-a-schema.md) | A gRPC resource carries `google.protobuf.Struct` documents, no generated message and no reflection; a code is spelled the same on every transport | accepted | RPC, errors |
| [D-053](D-053-a-client-refuses-what-changes-the-answer.md) | A client refuses an option that changes which rows come back and documents one that changes only their order or freshness | accepted | client, transports |
| [D-054](D-054-the-closed-ast-gets-one-marshaller.md) | The closed predicate AST gets one marshaller inside `crud`; `Raw`, `EqField` and `False` are refused by name | accepted | client, query |
| [D-055](D-055-a-principal-is-a-value-in-the-context.md) | A principal is a value in the context and the library never puts it there; `auth` is a package, not a manifest entry | accepted | auth, security |
| [D-056](D-056-an-authentication-failure-is-a-fault-that-wraps-a-sentinel.md) | A 401 is a fault wrapping `auth.ErrUnauthenticated`, and its reason never leaves the process | accepted | auth, errors |
| [D-057](D-057-the-application-opens-the-connection.md) | The application opens the connection and hands it over; `vvdb` imports nothing of vv and nothing in the seam reaches it | accepted | process & tooling |
| [D-058](D-058-the-layout-axis-is-the-subsystem.md) | The top-level directory is the subsystem and the transport is the second level; `repo/basic` becomes `crud/sqlrepo`, and `utils/` may not import a subsystem | accepted | process & tooling |
| [D-059](D-059-the-http-projection-of-the-error-contract-belongs-to-port.md) | The status table, the envelope, the `Renderer` seam and the body decode are `port/porthttp`'s, so an auth middleware does not import the repository | accepted | transports, errors |
| [D-060](D-060-a-request-may-not-choose-how-much-comes-back.md) | `query.Config` is open by default about what a request may *name* and closed about how much comes back; `unpaged` is declared per endpoint | accepted | querying, transports |
| [D-061](D-061-a-wrapper-forwards-what-it-wraps.md) | No optional interface is found by a bare assertion on the layer below; a decorator forwards `Next()`, a Source wrapper forwards `UnwrapSource()`, and the library walks | accepted | core seam, transactions & datasources |
| [D-062](D-062-the-library-logs-through-the-callers-logger.md) | The library never writes to a process-wide logger; every line goes through `port.Logger(ctx)`, and statements are instrumented by wrapping the `Source` | accepted | process & tooling, transports |
| [D-063](D-063-every-body-a-transport-reads-is-bounded.md) | Every request and response body is read under a byte cap, the same one on every binding, and a body past it is 413 / `ResourceExhausted` | accepted | transports, errors |
| [D-064](D-064-migration-generation-never-guesses-a-model.md) | Automatic migration generation uses only one uniquely best model; ambiguity is interactive or empty, never guessed | accepted | process & tooling, migrations |
| [D-065](D-065-structs-have-reference-semantics-unless-they-are-values.md) | A struct crosses an application boundary by pointer unless copying is its documented value semantics | accepted | API design, process & tooling |
| [D-066](D-066-access-owns-no-identity-and-no-route.md) | `access` names no subject type, no route and no identifier rule; the consumer creates the identity and normalises what it stores | accepted | auth, security, API design |
| [D-067](D-067-an-identifier-is-unique-within-a-subject-type.md) | `credentials` is unique on (subject_type, provider, identifier); every lookup carries the type, and a sign-in without one is refused | accepted | auth, security |
| [D-068](D-068-a-strategy-declares-issuing-and-verifying-together.md) | One strategy value produces both the issuer and the verifier; a guard is per subject and the verifier set is what was declared | accepted | auth, security, API design |

## By area

**Core seam** — D-001 (two-parameter `Core`, three-parameter `Repo`),
D-002 (`Opt[T]`), D-021 (why any of it is reflective), D-022 (the handler's
interface), D-030 (a verb on the seam is every decorator's obligation, and the
test that enforces it), D-061 (what embedding erases, and the two one-method
interfaces that survive it).

**Querying** — D-060 (what a request may name, and how much it may ask for),
D-028 (cursor pagination), D-003 (closed AST, `Raw`), D-004 (`Where` ANDs),
D-005 (`EXISTS`, not a join), D-006 (batched preload), D-013 (unknown field is a
rejection), D-014 (deterministic SQL), D-024 (**open** — `DISTINCT`).

**Security** — D-007 (narrowing across relations), D-008 (404, not 403),
D-004 (why a scope cannot be peeled off), D-003 (why a caller cannot compose out
of one), D-026 (**open** — `Inspect` and caller paging), D-055 (where the
principal a scope reads comes from, and why `security` imports `auth` and not
the reverse).

**Auth** — D-055 (the contract, the four satellites, why `auth` is not on the
manifest and why a `Principal` is an interface), D-056 (the 401's shape, why the
reason lives in the wrapped error, and where 401 sits between 403 and 404),
D-048 (the rule D-055 obeys rather than amends), D-035 (the `auth` row of the
naming grid), D-051 (why an `auth*` binding does not require its `crud*`
sibling).

**Writes** — D-010 (load-diff-write, locking, `version`), D-011 (`Save` is
JPA-shaped), D-012 (PUT does not create), D-002 (three-state DTO fields).

**Transactions & datasources** — D-009 (unconditional capture, opt-in scoping,
the two identity rules `crud.KeyOf` and `ownScope`, and — since phase 7 — which
transactions vv opened and the savepoint budget on them), D-019 (dialect
differences), D-027 (**open** — cross-database capture), D-041 (what else keys
on that identity), D-042 (why the ownership flag exists at all).

**HTTP** — D-063 (the body cap, and why all three bindings share one number),
D-062 (where the library's own log lines go), D-012 (PUT), D-022 (interface, not
struct), D-015 (error → status),
D-013 (400 for an unknown field), D-045 (what a binding owns and what `port`
owns — there are three HTTP bindings, Fiber, Gin and net/http, and one service
seam between them and the gRPC one; D-034 is its superseded first draft, kept
for the argument).

**RPC** — D-052 (the document wire shape, the absent schema and reflection, the
code spelling and the empty-field violation), D-045 (what `crud/rpc/crudgrpc` is a
shell over, and the phase-9 measurement that adding it changed nothing shared),
D-049 (why one `codes.Code` per kind and never per code), D-051 (why three
requires are one decision).

**Interop with an ORM** — D-017 (Go-side behaviour does not run), D-009 (how the
transaction is shared), D-018 (`-types`, `-into`, `-import`).

**Errors** — D-015 (the sentinel list and the HTTP mapping), D-049 (which of the two decides a status), D-046 (how a driver
error is classified, and why the class alone is not a gate), D-039 (message text
is not an interface), D-040 (retryable is not a client error), D-044 (a body
names nothing internal), D-047 (and neither does a fault's `Error()` text),
D-038 (a fault is additive), D-043 (one hop per layer), D-050 (which of those
hops may decline, and why only a generated one may),
D-041 (the catalog, and which unique keys it can tell apart per engine),
D-042 (the probe: what it may narrow, what it must never invent, the cap
numbers, and the three answers §16 owed it), D-045 (why the
mapping is in one place rather than one per binding, and where the violations
pipeline lives now), D-052 (the second vocabulary the same kinds are rendered
into, and what it costs).

**Dialects** — D-019 (what is hidden and what is observable, now eleven
differences), D-011 (the upsert forms), D-010 (why MySQL re-reads), D-041 (the
per-engine half of difference 9), D-046 (difference 10 — what a classified
violation can say, and which constructor decided the engine), D-042
(difference 11 — which violations the probe can find on which engine, whether it
runs inside a transaction at all, and how much a folded violation is known to
mean).

**Relations** — D-005 (filters), D-006 (preloads), D-007 (narrowings),
D-025 (**open** — key normalisation).

**Process & tooling** — D-064 (why migration source discovery never executes code or guesses an ambiguous model), D-048 (what joins the contract manifest, why nothing on the roadmap's `?` list does, and why phase 9's catalogues did not make `i18n` one), D-035 (naming), D-036 (first-party requirements), D-051 (why a satellite's unit is a decision rather than a require), D-033 (one module per optional dependency, and how a
release is tagged), D-016 (**superseded** in its module half; its stdlib rule
still binds), D-018 (generated artefacts, and every flag's reason), D-050 (why a
generated one is held to a standard a hand-written one is not, and what the
generator has to declare because reflection cannot see a flag), D-020 (tests are
the specification), D-057 (who opens the connection, and the one case where a
package may carry the project as a prefix), D-058 (the layout axis, why
`repo/basic` became `crud/sqlrepo`, and the one line that keeps `utils/` from
collecting the repository).

**Transports** — D-034 (**superseded**), D-045 (the shared half is
transport-neutral), D-059 (the HTTP projection of the error contract belongs to
`port`, not to a subsystem, and what that costs a forwarder file), D-052 (a gRPC
resource carries documents), D-049 (the kind decides the status), D-013
(binding-level rejection).

**Philosophy & docs** — D-021 (magic over orthodoxy, and D-050 as its newest
application), D-023 (guides lead with the result), D-020 (what a test is for).

## Open tensions

- **[D-024](D-024-distinct-and-the-forced-primary-key.md) — `DISTINCT`.**
  A bare `?distinct=1` with no `select` projects every column, primary key
  included, so it deduplicates nothing and says so to nobody. Separately, a paged
  `DISTINCT` cannot have a stable tiebreaker, so page 2 of the same query can
  legitimately differ between calls. Three ways out, all with a cost.
- **[D-025](D-025-mapkey-collapses-non-comparable-keys.md) — `mapKey`.**
  A non-comparable relation key falls through to `reflect.Value.String()`, which
  returns a per-type constant, so every such key collides and every parent gets
  every child. Unreachable from any model in the tree today. Fixing it means
  choosing a serialisation inside a package that may not take a dependency
  (D-016), or refusing the declaration.
- **[D-026](D-026-deleteall-fetches-victims-with-caller-options.md) — gated
  filtered writes.** `gate.DeleteAll` and `gate.UpdateAll` fetch the rows they
  will show `Inspect` using the caller's own options, including `Limit`; the
  `DELETE` and `UPDATE` are unlimited. A caller-supplied `Limit(10)` means
  `Inspect` sees ten rows and the statement touches all of them. No test covers
  it.
- **[D-027](D-027-intx-cross-database-capture-is-documented-not-enforced.md) —
  cross-database capture.** An unscoped `crud.WithExecutor` is adopted by every
  repository, including ones bound to another database. `WithExecutorFor` is the
  documented answer and is not enforced. Enforcing it needs a `Source` identity
  that would refuse the foreign executors the whole interop seam depends on.
