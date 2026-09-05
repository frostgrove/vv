# Decisions & invariants

This directory pins the things that could reasonably have gone the other way: a
design choice with a real alternative, an invariant the code depends on but
cannot express in a type, and the owner's own corrections. Every file names the
failure mode that made the answer what it is, and says what a future change must
not do.

What does not belong here: how to use a feature (that is
`docs/usage-guides/`), what straightforward code already says through its names
and structure, or a change with no alternative worth naming.

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
still says what not to do while it is open — see D-024.

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
| [D-006](D-006-preload-is-a-batched-second-query.md) | Batched preload options are fail-closed and fold identically across transports | accepted | querying |
| [D-007](D-007-narrowing-crosses-a-relation-only-when-declared.md) | A scope covers its own `FROM`; the far side of a relation is narrowed only where declared | accepted | security |
| [D-008](D-008-out-of-scope-is-404-not-403.md) | A row hidden by a scope is `ErrNotFound`, never a denial | accepted | security |
| [D-009](D-009-context-executor-capture-is-unconditional.md) | `WithExecutor` reaches every repository; only `WithExecutorFor` restricts it | **superseded by D-082** | transactions & datasources |
| [D-010](D-010-update-is-load-diff-write.md) | `Update` writes only what changed, and locks only inside a transaction | accepted | writes |
| [D-011](D-011-save-is-jpa-shaped.md) | No key inserts, a key upserts the row that key names and no other, an unset `noauto` key is `ErrMissingID`; `Create` and `Replace` sit beside it | accepted | writes |
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
| [D-025](D-025-mapkey-collapses-non-comparable-keys.md) | Both ends of a relation agree on a key, and two different keys never collide | accepted | relations |
| [D-026](D-026-deleteall-fetches-victims-with-caller-options.md) | Every row a gated filtered write touches was shown to `Inspect` first | accepted — settled by D-087 | security |
| [D-027](D-027-intx-cross-database-capture-is-documented-not-enforced.md) | A repository never runs on a database it was not bound to, unless the application said so | **superseded by D-082** | transactions & datasources |
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
| [D-061](D-061-a-wrapper-forwards-what-it-wraps.md) | Discovery follows declared wrapper walks; storage effects require exact explicit forwarding and never tunnel through an unknown layer | accepted | core seam, transactions & datasources |
| [D-062](D-062-the-library-logs-through-the-callers-logger.md) | Library lines use `port.Logger(ctx)`; Source wrappers see direct calls, while complete transaction tracing belongs below the handles | accepted | process & tooling, transports |
| [D-063](D-063-every-body-a-transport-reads-is-bounded.md) | Every request and response body is read under a byte cap, the same one on every binding, and a body past it is 413 / `ResourceExhausted` | accepted | transports, errors |
| [D-064](D-064-migration-generation-never-guesses-a-model.md) | Automatic migration generation uses only one uniquely best model; ambiguity is interactive or empty, never guessed | accepted | process & tooling, migrations |
| [D-065](D-065-structs-have-reference-semantics-unless-they-are-values.md) | A struct crosses an application boundary by pointer unless copying is its documented value semantics | accepted | API design, process & tooling |
| [D-066](D-066-access-owns-no-identity-and-no-route.md) | `access` names no subject type, no route and no identifier rule; the consumer creates the identity and normalises what it stores | accepted | auth, security, API design |
| [D-067](D-067-an-identifier-is-unique-within-a-subject-type.md) | `credentials` is unique on (subject_type, provider, identifier) and a subject holds at most one password; every lookup carries the type, and a sign-in without one is refused | accepted | auth, security |
| [D-068](D-068-a-strategy-declares-issuing-and-verifying-together.md) | One strategy value produces both the issuer and the verifier; a guard is per subject and the verifier set is what was declared | accepted | auth, security, API design |
| [D-069](D-069-a-shared-stdlib-only-tier-sits-under-everything.md) | A first-party stdlib-only `SHARED` tier that `crud` and every contract package may import; supersedes D-016's stdlib half | accepted | core seam, process & tooling |
| [D-070](D-070-the-default-role-is-a-row-not-a-setting.md) | What a sign-up grants is a row in `subject_default_roles`, resolved against `roles` when written; no config key, no `Registrar.Role` | accepted | auth, security, migrations |
| [D-071](D-071-a-derived-path-map-refuses-what-it-cannot-read.md) | `port.Paths[M]` derives the inverse map from the model's wire tags; a column no named tag gives a key for is a start-up refusal, never a guess from the field name | accepted | errors, API design |
| [D-072](D-072-closing-a-session-reaches-the-strategy-that-issued-it.md) | Every closing path goes through `Deps.revoke`, which reads before it writes so the issuing strategy can be named; a strategy that verifies without reading declares a `RevocationSink`, announced after the commit | accepted | auth, security |
| [D-073](D-073-a-mounted-route-declares-its-access-or-start-up-fails.md) | Every mounted route names its permissions or the reason it is open, and the router is compared against those declarations at assembly; both directions of disagreement are a start-up failure, and the comparison is an audit rather than enforcement | accepted | auth, security, transports |
| [D-074](D-074-a-container-binding-is-a-satellite.md) | The library resolves no component by type; a dependency-injection container is bound to from a satellite whose only job is that binding (narrows D-037) | accepted | process & tooling, philosophy |
| [D-075](D-075-where-a-credential-goes-is-the-requests-choice-except-on-rotation.md) | A request names one of three deliveries and gets it or a refusal; silence takes the most closed one, and a rotation answers through the channel the credential arrived on | accepted | auth, transports |
| [D-076](D-076-a-guard-is-idempotent-only-with-itself.md) | One guard instance authenticates once per request; every different guard runs, and invalid credential-source declarations fail at construction | accepted | auth, security, API design |
| [D-077](D-077-rollback-outlives-the-request-with-a-bound.md) | Rollback ignores request cancellation but keeps a finite cleanup deadline | accepted | transactions & datasources |
| [D-078](D-078-jwt-trust-is-exact-bounded-and-distinguishes-outage.md) | HMAC names one strong algorithm, JWKS trust and stale-on-error are bounded, provider failure is not a credential refusal, and Standard principals have subjects | accepted | auth, security, resilience |
| [D-079](D-079-bind-budgets-are-statement-wide.md) | Every statement respects its dialect bind budget, and chunks of one logical write are atomic | accepted | writes, querying, transactions |
| [D-080](D-080-a-relation-table-name-is-immutable-after-resolution.md) | A relation target's structured table reference is immutable; no dotted string is guessed into components | accepted | relations, declarations |
| [D-081](D-081-database-secrets-are-values-and-typed-tls-is-verified.md) | Database secrets render redacted, and an omitted typed-server TLS mode means verified TLS | accepted | configuration, security |
| [D-082](D-082-source-bound-sessions-are-the-safe-default.md) | A safe executor binding names its canonical source; unconditional adoption is explicitly unsafe | accepted | transactions & datasources |
| [D-083](D-083-native-bulk-is-an-explicit-repository-capability.md) | Safe batch insertion is typed and policy-aware; native bulk is the magic default, portable SQL the explicit opt-out, and raw effects are Unsafe | accepted | writes, core seam, transactions & datasources |
| [D-084](D-084-shared-cache-flights-do-not-inherit-a-waiters-context.md) | Shared cache flights have finite value-free contexts; no caller donates request values or lifetime to another | accepted | caching, security, resilience |
| [D-085](D-085-transient-cache-budgets-cover-cache-attributable-work.md) | Typed admission bounds cache-attributable transient work before it begins; finite waiters have permanently reserved backing | accepted | caching, resilience |
| [D-086](D-086-a-configuration-is-a-tree-and-its-provenance-is-part-of-the-result.md) | A configuration validates node by node, strictness and the default path live on a `Source` value, and the report names origins and no values | accepted | configuration, security |
| [D-087](D-087-a-verb-refuses-an-option-it-cannot-honour.md) | Every option a verb is given is honoured by its statement or refused by name before it runs | accepted | querying, security |
| [D-088](D-088-a-credential-never-outlives-what-bounds-it.md) | An access token's `exp` is clamped to the session's absolute end, rotation honours the idle deadline, and an inadmissible lifetime matrix fails at start-up | accepted | auth, security |
| [D-089](D-089-what-a-sign-in-costs-is-bounded-at-a-seam-the-application-fills.md) | Attempt limiting, an Argon2 bulkhead and login field bounds; `access` owns the seams and the composition root fills them | accepted | auth, security, resilience |
| [D-090](D-090-liveness-never-probes-and-a-degraded-replica-stays-in-rotation.md) | Liveness runs no check, and only a required dependency answers 503; a degraded replica stays in rotation | accepted | operations, resilience |
| [D-091](D-091-health-importance-and-public-codes-belong-to-the-composition-root.md) | A checker supplies a probe; importance and the public code are set where the program is assembled, and no subsystem gets a health package | accepted | operations, security, API design |
| [D-092](D-092-a-background-activity-is-a-supervised-runner.md) | Background work is a contributed `Runner` a supervisor starts, or a `runtime.Loop` its component starts — never a bare goroutine; a run that ends early is a reported failure, and drain precedes cancel | accepted | operations, composition, resilience |
| [D-093](D-093-a-built-in-capability-is-proved-by-its-method.md) | A built-in cache capability is proved only by its typed method; every other one comes from the set the driver declared | accepted | caching, extension architecture |
| [D-094](D-094-an-execution-memo-remembers-stored-envelopes-only.md) | An execution memo is an explicit bounded value that holds copied envelopes, never a miss, an error or a read its own coordination discarded, and answers nothing after `Close` | accepted | caching, resilience |
| [D-095](D-095-resolvemany-writes-nothing-until-the-whole-batch-fits.md) | `ResolveMany` calls one loader once and proves the cumulative bound before the first write; a partial batch is a failure, not a result | accepted | caching, resilience |
| [D-096](D-096-a-subsystem-owns-the-seam-the-root-owns-the-choice.md) | A subsystem publishes a bounded observer fan-out and a `Check` probe; importance, transport and globals are the composition root's | accepted | caching, jobs, operations, composition |
| [D-097](D-097-an-access-token-names-the-service-it-was-minted-for.md) | Every minted access token carries an `aud` its verifier requires; silence takes the issuer and only `UnsafeAnyAudience` waives it | accepted | auth, security |
| [D-098](D-098-a-rotation-answers-before-it-spends-the-credential.md) | The replacement exists before the swap that retires the presented credential, and a refresh that loses the swap rotates again rather than refusing | accepted | auth, security, concurrency |
| [D-099](D-099-a-request-presents-its-credential-in-one-place.md) | A cookie-reading guard takes the cookie or the header, never both and never two cookies of a name; the old precedence is `Unsafe`-named | accepted | auth, security |
| [D-100](D-100-declaration-and-enforcement-are-two-projections-of-one-policy.md) | One `appfiber.Policy` produces both the access declaration the boot gate compares and the outer check that runs before the handler; the registrar has no way to mount a route without stating one (narrows D-073) | accepted | auth, security, transports, composition |
| [D-101](D-101-migrating-the-jobs-schema-is-a-deployment-profile-choice.md) | No zero value migrates a jobs schema: silence verifies, the deployment profile derived from the environment decides, and an unrecognised environment is production | accepted | jobs, operations, composition |
| [D-102](D-102-ambient-authority-is-checked-where-it-is-spent.md) | A cookie-delivering access surface refuses an unsafe request that spends its own cookie and cannot say it came from somewhere this deployment serves; a header credential and a safe method are not checked, and the waiver is a sentence | accepted | auth, security, transports |
| [D-103](D-103-a-preflight-is-answered-never-routed.md) | A CORS preflight that skips the guard is answered by the handler the consumer named — or by a bare `204` when nobody was named — and never continues to a route | accepted | auth, security, transports |
| [D-104](D-104-a-cache-never-shares-an-eviction-domain.md) | A physical resource holds caching or durable state and never both; a cache is refused the resource that holds queued work or revoked sessions, and only two durable tenants may share one behind a written `SharedDurableSecurity` reason, and `RequireDeclaredResources` turns an undeclared resource into a refusal | accepted | caching, jobs, security, operations |
| [D-105](D-105-the-persistence-patch-and-the-public-patch-body-are-two-types.md) | What an `UPDATE` may write and what a client may send are two types joined by a total mapper; a public body is derived by narrowing and every field past it is confirmed by name in a checked-in manifest | accepted | process & tooling, transports |
| [D-106](D-106-a-module-is-a-descriptor-and-a-profile-decides-what-of-it-runs.md) | A bounded context contributes one definition whose constructors are filed by role; a deployment profile decides which roles are wired, and the descriptor a doctor prints is answered without building any of it | accepted | composition, operations |
| [D-107](D-107-a-crud-resource-declares-the-permissions-its-gate-enforces.md) | A CRUD route's access declaration is derived from the `security.Policy` its repository is gated with — the requirement is data the gate reads, and an action the policy does not declare is refused where the declaration is built (narrows D-073) | accepted | auth, security, transports |
| [D-108](D-108-a-jobs-deployment-role-is-declared-not-read-off-the-graph.md) | Consuming jobs and owning the schedule clock are deployment roles named on `jobsfx.Spec`; a container that merely holds a consumer is refused rather than promoted to a worker replica, and an enabled role is a runner in the supervisor's group | accepted | composition, operations |
| [D-109](D-109-an-operations-route-is-inferred-from-its-guard-and-confirmed-once.md) | An application operation that guards itself has its route inferred from that guard, recorded in `routes.manifest.yml` and confirmed once by a person; until it is confirmed the generated file does not compile, and once the use case reads the generated operation the confirmation is never asked for again | accepted | auth, security, process & tooling |
| [D-110](D-110-a-modules-contributions-are-inferred-from-what-its-packages-construct.md) | A module's constructor list is derived from what its package tree builds, recorded in `module.manifest.yml` with an inferred kind, and confirmed once per contribution — or excluded; until every included row is confirmed the generated file does not compile, so a constructor nobody placed stops the build instead of quietly not existing | accepted | composition, process & tooling |
| [D-111](D-111-a-resource-declaration-is-contributed-never-inferred.md) | `cachefx` is what makes the eviction-domain rule run in a deployment: it collects `cache.ResourceDeclaration` from an fx group, activates with declared resources required unless the spec writes `cachefx.Accepted`, and infers nothing from a provider — so the durable packages the rule protects never import `cache` to be counted (extends D-104) | accepted | caching, composition, security, operations |
| [D-112](D-112-a-revocation-list-refuses-a-server-that-evicts-it.md) | The Redis revocation list asks its own server for `maxmemory-policy` at start-up and refuses every evicting one; a server that will not answer is a third verdict that is warned about by default and refused on request, an absent server is refused outright, and the check is a lifecycle method with `revokeredisfx` as its fx form | accepted | auth, security, caching, operations |
| [D-113](D-113-a-resource-states-its-mounted-operations-as-one-set.md) | Which of a CRUD resource's ten routes are mounted is one bitmask on `port.Rules`, read by every transport and by the declaration `crudhttp.Table` derives; `ReadOnly` becomes its commonest value, and naming both is a start-up panic | accepted | transports, API design, security |
| [D-114](D-114-one-cross-cutting-opentelemetry-module.md) | Exactly one published module `otel/` (`github.com/frostgrove/vv/otel`, package `vvotel`) adapts base seams (`port`, `storage`, `cache`); non-OTel modules and the root remain OTel-free, and combination packages are forbidden | accepted | process & tooling, composition, operations |

## By area

**Core seam** — D-001 (two-parameter `Core`, three-parameter `Repo`),
D-002 (`Opt[T]`), D-021 (why any of it is reflective), D-022 (the handler's
interface), D-030 (a verb on the seam is every decorator's obligation, and the
test that enforces it), D-061 (what embedding erases, which discovery walks are
safe, and why storage effects require exact forwarding), D-083 (the optional
typed batch effect and its fail-closed decorator boundary).

**Caching** — D-084 (why shared loads inherit no request values, how disabled
storage preserves that boundary, and the synchronous observer obligation),
D-085 (typed byte admission, permanently backed waiter slots, separate flight,
wire and decoded limits, and the exact ownership boundary), D-093 (why a
declared name can never grant a built-in capability), D-094 (what an execution
memo may remember and what `Close` guarantees), D-095 (why a batch resolve
writes nothing until all of it fits), D-096 (the fan-out and probe seams a
subsystem owns and the choices it does not), D-104 (why a cache never shares an
eviction domain with a job queue or a revocation list, and what the one waiver
may excuse), D-111 (what makes that rule run in a deployment: a declaration
contributed as data, required by default in the binding, and never inferred
from a provider), D-112 (the half a declaration cannot reach: what the
revocation list's own server does when it fills up, asked of it rather than
assumed), D-021 (declarative magic with an
explicit construction path), D-020 (adversarial controls are the
specification).

**Configuration** — D-086 (why the whole tree validates rather than the root,
why the older `Validate` owns its subtree, why the default path stopped being a
package variable, and why the provenance report carries no values), D-013 (an
unknown key is a rejection), D-081 (why a credential is a value type).

**Querying** — D-060 (what a request may name, and how much it may ask for),
D-028 (cursor pagination), D-003 (closed AST, `Raw`), D-004 (`Where` ANDs),
D-005 (`EXISTS`, not a join), D-006 (batched preload), D-013 (unknown field is a
rejection), D-014 (deterministic SQL), D-079 (statement-wide bind budgets and
atomic chunks), D-087 (an option a verb cannot honour is refused, not dropped),
D-024 (**open** — `DISTINCT`).

**Security** — D-007 (narrowing across relations), D-008 (404, not 403),
D-004 (why a scope cannot be peeled off), D-003 (why a caller cannot compose out
of one), D-026 (`Inspect` and caller paging, settled by D-087), D-055 (where the
principal a scope reads comes from, and why `security` imports `auth` and not
the reverse), D-076 (why a principal from one guard is not evidence that a
different guard ran), D-112 (why an evicted key and a session nobody revoked are
the same read, and what the deny-list therefore asks its server before it is
trusted).

**Auth** — D-112 (why a revocation list refuses a Redis that evicts, and why
"the server would not say" is a verdict rather than a pass),
D-073 (what a route has to declare, why the check is at assembly and
not per request, and why the declaration lives in `authhttp` rather than
`porthttp`), D-100 (the registrar that makes the declaration and the outer check
two projections of one policy, and the arrow D-073 is actually about), D-107
(the CRUD half of that: the permission a route declares is read off the policy
its gate enforces, and why a routes manifest with `confirmed:` buys nothing
there), D-109 (the other half: an operation that guards itself has its route
inferred from that guard, confirmed once, and then bound), D-055 (the contract, the four satellites, why `auth` is not on the
manifest and why a `Principal` is an interface), D-056 (the 401's shape, why the
reason lives in the wrapped error, and where 401 sits between 403 and 404),
D-048 (the rule D-055 obeys rather than amends), D-035 (the `auth` row of the
naming grid), D-051 (why an `auth*` binding does not require its `crud*`
sibling), D-070 (the default role a sign-up grants is a row, which is the last
hole in D-066), D-072 (closing a session has to reach the strategy that issued
it, because a verifier that reads no row cannot see a `revoked_at`), D-075 (where
a session's two credentials go is the request's to say, what silence takes, and
the one case — rotation — where the caller is deliberately given no say), D-076
(why idempotence is per guard instance, how a bare API-key header is declared,
and which invalid auth declarations fail while the graph is built).
The JWT-provider boundary is D-078: exact HMAC algorithms and key lengths,
finite JWKS freshness, typed outage handling, unambiguous key sets and the
non-empty subject promised by `Standard`. What a session's credentials are allowed
to outlive is D-088, and what a sign-in is allowed to cost — attempts, concurrent
hashes and field lengths — is D-089. D-097 is the audience every minted access
token names and the one way to waive it; D-098 is why a rotation writes only
once its answer exists and why the loser of a concurrent refresh is not signed
out; D-099 is why two credential sources in one request are a refusal rather
than a ranking; D-102 is why a cookie-borne write has to say where it came from,
and why a bearer one does not; D-103 is why the preflight decorator answers a
request instead of passing it on, since the headers it recognises are the
client's to write.

**Writes** — D-010 (load-diff-write, locking, `version`), D-011 (`Save` is
JPA-shaped, when it stops being one statement, and the explicit `Create` /
`Replace` beneath it), D-083 (typed insert-only batch, native magic and portable opt-out),
D-012 (PUT does not create), D-002 (three-state DTO fields).

**Transactions & datasources** — D-082 (source-bound sessions by default,
strict legacy inference and the explicit unsafe escape hatch), D-019 (dialect
differences), D-077 (bounded detached rollback), D-079 (atomic write chunks),
D-083 (native effects resolve the same source-bound executor), D-041 (what else
keys on datasource identity), D-042 (why the ownership flag exists at all).
D-009 and D-027 retain the superseded argument.

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

**Composition** — D-074 (why an fx binding is a satellite and what that does not
license), D-111 (the cache binding, and why the package that lives on a
resource declares it as data rather than by importing the subsystem that
checks it), D-037 (the container this library still does not hold, and the three
steps by which one appears), D-051 (why `appfiber` requiring both fx and Fiber is
one decision), D-021 (the boilerplate a consumer should not be writing).

**Interop with an ORM** — D-017 (Go-side behaviour does not run), D-009 (how the
transaction is shared), D-018 (`-types`, `-into`, `-import`).

**Errors** — D-015 (the sentinel list and the HTTP mapping), D-049 (which of the two decides a status), D-046 (how a driver
error is classified, and why the class alone is not a gate), D-039 (message text
is not an interface), D-040 (retryable is not a client error), D-044 (a body
names nothing internal), D-047 (and neither does a fault's `Error()` text),
D-038 (a fault is additive), D-043 (one hop per layer), D-050 (which of those
hops may decline, and why only a generated one may), D-071 (deriving that hop
from the model's tags, and every gap in the derivation being a refusal),
D-041 (the catalog, and which unique keys it can tell apart per engine),
D-042 (the probe: what it may narrow, what it must never invent, the cap
numbers, and the three answers §16 owed it), D-045 (why the
mapping is in one place rather than one per binding, and where the violations
pipeline lives now), D-052 (the second vocabulary the same kinds are rendered
into, and what it costs).

**Dialects** — D-019 (what is hidden and what is observable, now twelve
differences), D-011 (the upsert forms), D-010 (why MySQL re-reads), D-041 (the
per-engine half of difference 9), D-046 (difference 10 — what a classified
violation can say, and which constructor decided the engine), D-042
(difference 11 — which violations the probe can find on which engine, whether it
runs inside a transaction at all, and how much a folded violation is known to
mean), D-083 (difference 12 — explicit native COPY versus portable INSERT).

**Relations** — D-005 (filters), D-006 (preloads), D-007 (narrowings),
D-080 (immutable structured table resolution, qualified refs and independent blueprints),
D-025 (fail-fast key normalisation).

**Process & tooling** — D-064 (why migration source discovery never executes code or guesses an ambiguous model), D-048 (what joins the contract manifest, why nothing on the roadmap's `?` list does, and why phase 9's catalogues did not make `i18n` one), D-035 (naming), D-036 (first-party requirements), D-051 (why a satellite's unit is a decision rather than a require), D-033 (one module per optional dependency, and how a
release is tagged), D-016 (**superseded** in its module half; its stdlib rule
still binds), D-018 (generated artefacts, and every flag's reason), D-050 (why a
generated one is held to a standard a hand-written one is not, and what the
generator has to declare because reflection cannot see a flag), D-105 (why the
persistence patch and the public PATCH body are two types, why a public body is
narrowed rather than copied, and why widening one is confirmed by name), D-109
(why a route inferred from a guard is confirmed once by a person and then
bound), D-110 (the same shape for a module: a constructor list read off the
package tree, an inferred kind confirmed or excluded once, and why excluding is
an answer rather than a workaround), D-020 (tests are
the specification), D-057 (who opens the connection, and the one case where a
package may carry the project as a prefix), D-081 (redacted database secrets
and verified typed TLS), D-058 (the layout axis, why
`repo/basic` became `crud/sqlrepo`, and the one line that keeps `utils/` from
collecting the repository).

**Transports** — D-034 (**superseded**), D-045 (the shared half is
transport-neutral), D-059 (the HTTP projection of the error contract belongs to
`port`, not to a subsystem, and what that costs a forwarder file), D-052 (a gRPC
resource carries documents), D-049 (the kind decides the status), D-013
(binding-level rejection).

**Operations** — D-090 (why liveness asks nothing and why degraded keeps its
traffic), D-091 (importance as a composition decision, the opt-in public code,
and why there is no package per checked subsystem), D-101 (why nothing migrates
a jobs schema by default, and what a production profile refuses), D-096 (the neutral probe
and observer fan-out that give D-091 something to wrap), D-092 (contributing a runner
is what starts it, a component's own loop is a supervisor of one, an early
return is a failure, drain before cancel, and a ticker that says it is
per-replica), D-106 (a module is a descriptor, a
deployment profile decides what of it is wired, and describing never builds),
D-110 (where that descriptor's constructor list comes from, and why an
unconfirmed one does not compile),
D-108 (why a jobs worker fleet is named rather than inferred from the graph that
happens to hold a consumer), D-112 (a start-up question asked of Redis, and the
three answers a deployment can get back), D-037 (why none of this is a
container).

**Philosophy & docs** — D-021 (magic over orthodoxy, and D-050 as its newest
application), D-023 (guides lead with the result), D-020 (what a test is for).

## Open tensions

- **[D-024](D-024-distinct-and-the-forced-primary-key.md) — `DISTINCT`.**
  A bare `?distinct=1` with no `select` projects every column, primary key
  included, so it deduplicates nothing and says so to nobody. Separately, a paged
  `DISTINCT` cannot have a stable tiebreaker, so page 2 of the same query can
  legitimately differ between calls. Three ways out, all with a cost.

