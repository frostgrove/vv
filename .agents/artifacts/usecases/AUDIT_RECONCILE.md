# AUDIT — RECONCILIATION WITH THE CURRENT TREE

## Baseline checked on 2026-09-09

The repository has no `audit` package. The current audit roadmap still describes
the pre-event/pre-tenancy/pre-OTel tree and chooses a single PostgreSQL-bearing
module. That topology conflicts with decisions that landed later.

## Reproducible upstream reference corpus

The 2026-09-09 competitive pass uses read-only clones under
`tmp/audit-frameworks`; moving default branches are not evidence. Every comparison
below is pinned to the exact source commit:

| Framework | Source | Commit | Frostgrove use |
|---|---|---|---|
| PaperTrail | `https://github.com/paper-trail-gem/paper_trail` | `098058ae472d13763fe66e6866a6a4dfc64a3eca` | pre-change history, before/after predicates, previous/next navigation, reification semantics |
| django-simple-history | `https://github.com/jazzband/django-simple-history` | `e71cb9e054da29f2112d0e146e71962160da4324` | population of existing rows, class-wide history, and class-wide as-of sets retained as explicit follow-up |
| Audit.NET | `https://github.com/thepirat000/Audit.NET` | `15a5e5b2996f3f4649bfa18e6b9a7c30da45a354` | operation lifecycle, start/end/duration/checkpoint behavior |
| spatie/laravel-activitylog | `https://github.com/spatie/laravel-activitylog` | `c37c43b32ea1ff15dc00491c07dafdfb8fd57340` | explicit activity/event metadata, nullable subjects, and batch grouping; Frostgrove makes absence a sealed declaration choice |
| JaVers | `https://github.com/javers/javers` | `cc835d974ca9b8a24b2d9b7cd54aad9c4b32d1ef` | initial full snapshots, by-class/property discovery, query work statistics |
| Hibernate ORM/Envers | `https://github.com/hibernate/hibernate-orm` | `4a89bfc9c98debcad450716a8871c895245989f2` | property changed/not-changed predicates, revision queries, schema-evolution caveats |

The checked Frostgrove tree snapshot was
`9835570421a71f29ed541f4c2dc97e045ca4eace`; concurrent OTel/i18n work after that
point is deliberately preserved and must be re-read at every implementation
checkpoint. The resulting decisions are not ports of upstream internals:
Frostgrove uses a truthful full `entity.changed` first-touch anchor rather than a
synthetic creation, explicit `FieldUnobserved` across schema introduction, typed
resource/operation discovery, declaration-level target absence without fabricated
references, signed-name positive/negative change predicates, typed entity equality
indexes with side semantics, authenticated lifecycle state, epistemically honest
neighbors, broad attempt investigation distinct from exact status, immutable
append-only attempt transitions instead of mutable replace-on-end, and the existing
capability/authorization/microkernel boundaries.

## Binding facts

| Current fact | Consequence for audit |
|---|---|
| The root module has no third-party requirements | Dependency-light audit vocabulary and adapters can live in the root without costing unused consumers anything |
| `event`, `eventmemory`, and `eventtest` are root packages; `eventpg` is a nested module | Audit should use the same root-kernel/root-memory/root-conformance/nested-backend split |
| A module boundary means dependency cost, not optionality | One PostgreSQL-bearing `audit` module is obsolete; PostgreSQL belongs in `audit/auditpg` |
| `crud.Chain` and `crud.Base.Next` exist | `auditcrud.Secured` is an ordinary `crud.Middleware` factory whose returned authority-firewall terminal deliberately omits `Next`; the narrow D-061 exception prevents navigation into its Gate/private receiver rather than creating an audit-owned chain |
| `port.ChainService` and `storage.Chain` now exist | Generic adapters are possible, but should ship only where their evidence and atomicity claims are honest |
| Exact optional effects no longer tunnel | `auditcrud.Secured` owns one closed `security Gate -> private audit receiver -> faults/store` graph; Gate-only scoped effects remain private, faults is the receiver's direct inner, and there is no public admission protocol or unsecured audit adapter to tunnel |
| Audit history/lifecycle needs exact multitenant coordinates | Data-bearing control policy requires a searchable requester scope; every present retained evidence scope is searchable, while subject/event-target selectors distinguish exact from explicit declaration-bound all and never treat empty as wildcard |
| Corrections can address protected but not redacted targets | Exact lookup authenticates the record-era target; protected values are revealed only for equality, while current or record-era redacted targets refuse without guessing or a distinct existence signal |
| Runtime strings are not stable audit codes | Event outcomes/reasons and caller-authored control reasons are immutable action-scoped manifest-public sets; verification and internal outcomes remain closed package types, protected narrative is a separate field, and semantic privacy of syntactically valid public labels is a declaration-author/reviewer obligation rather than an undecidable kernel check |
| Go functions have no trustworthy runtime identity | Declarations carry explicit semantic versions and named expected logical fingerprints; codec engines are wrapped by audit-owned accepted/rejected wire fixtures, while application tests own typed samples and unsampled behavior remains an explicit trusted-author versioning duty rather than a false finite-test guarantee; arbitrary callbacks are also trusted for termination and complexity and their results are bounded before later work |
| Legal-hold matter and projection are privacy/integrity data | ControlPolicy declares a protected matter codec/classification/mode; authorization binds exact command/HoldID/target/matter intent, retained aliases resolve one private stable HoldIdentity, every transition signs it with prior/result membership/count/epoch/canonical-identity-set/head, and Writer applies one exact full-state CAS |
| Store-recorded timestamps are mutable metadata | Historical time reconstruction verifies an asserted authenticated current subject head through genesis and uses only a causally closed signed ObservedAt prefix; RecordedAt remains an unsigned pagination/operator fact, while rollback of both history and a previously valid store-present head requires an independent external anchor to detect |
| Secondary request digests can expose low-entropy coordinates | Access, result, grant, hold, and denial digests use distinct keyed semantic domains over exhaustive typed normalized inputs, including role/query class and continuation-origin coordinates; a custom SemanticDigester is trusted to remain a stable keyed PRF |
| Mature audit systems expose changed-property, actor/context, exact-revision, and typed-property lookup | Frostgrove admits a finite typed query algebra over declared fields/indexes and protected equality coordinates, with current authorization and retained-manifest verification; arbitrary metadata paths and adapter query objects remain forbidden |
| Activity systems permit an absent subject (`laravel-activitylog/database/migrations/create_activity_log_table.php.stub:15`) but commonly encode that as nullable storage rather than declaration meaning | Event and attempt declarations choose a typed target or explicit target absence; presence is authenticated meaning, and a targetless declaration has no target query door |
| JaVers accepts string property names (`javers/javers-core/src/main/java/org/javers/repository/jql/QueryBuilder.java:202-213`) and PaperTrail exposes before/to hash predicates (`paper_trail/lib/paper_trail/version_concern.rb:145-168`), but generic paths widen the kernel | A sealed typed EntityIndex permits only Before/After/Either equality over plaintext, tokenized, or indexed-protected representations, bounded AnyOf values, and bounded compatible origin-sealed AllOf conjunctions whose item predicates are satisfied by one item |
| Reification and version navigation are often presented as total (`paper_trail/README.md:686-705`) even after deletion, pruning, or incomplete reads | Projection/comparison carries authenticated EntityPresent/EntitySoftDeletedState/EntityHardDeletedState lifecycle state, while predecessor and successor are independent NeighborPresent/NeighborChainBoundary/NeighborUnavailable claims under one exact budget |
| A type-wide operation search is useful for investigation but cannot prove one attempt is complete | AttemptType-wide and searchable-target history return transition pages only; exact OperationID status separately verifies the complete bounded chain through its authenticated head |
| Offset/keyset pagination alone admits concurrent backdated rows | History origin materializes a bounded expiring store-side cohort and every cursor freezes its identity, stable sort, cumulative progress, and original grant ceilings |
| Snapshot/diff APIs often guess when protected history is unavailable | Reconstruction comparison uses two ordered authenticated boundaries under one combined budget and reports Equal, Changed, or Indeterminate from endpoint knowledge |
| Some history systems permit identifier reuse after delete | A signed hard-delete revision terminally closes the stable scoped-subject chain; reuse needs an explicitly different application identity rather than a silent second genesis |
| A revision group or `OperationID` proves correlation, not that protected work began after durable evidence or reached one truthful settlement | Release P1 needs a distinct catalog-declared attempt lifecycle with an independently committed start, immutable checkpoints/uncertainty/resolution, and one terminal under exact expected-state CAS |
| Audit.NET exposes useful start/end policies and timed checkpoints, but its pinned scope can replace the start record on end, discard it, accept arbitrary objects/custom-field maps, and derive exception/target data at end (`src/Audit.NET/EventCreationPolicy.cs:6-15`; `src/Audit.NET/AuditScope.cs:185-202,245-269,501-524`) | Frostgrove adopts explicit lifecycle intent only: append-only typed transitions, fail-closed Required start, no discard/replace, no ambient/runtime-reflection capture, and no raw exception/target cloning; bounded compile-time reflection may validate explicit typed schema opt-ins |
| A store must not select lifecycle time | Control calls its trusted clock exactly once and binds the normalized AsOf into request, authorization, inventory, selection, and fence; the store may choose only an opaque snapshot identity and must echo AsOf exactly |
| Audit cannot self-authenticate its first catalog | Catalog install/activation are deployment-only, require a persisted independently authorized change-ledger reference, and expose a bounded immutable mutation log for reopen verification; distinct concrete deployment/runtime handles ensure the runtime dynamic type has no CatalogAdmin |
| `crud.InAtomic`, source-bound executors, and exact datasource identity exist | The CRUD adapter can open/join one transaction once resolution proves an exact binding rather than the legacy unsafe fallback |
| The current transaction vocabulary can recognize a raw transaction but does not retain root-versus-savepoint provenance | Audit requires a small neutral scope query and accepts only a provenance-preserving framework root presented by callers; faults may own one narrower inner savepoint solely around the business statement after root admission and must finalize it before audit capture |
| `WithUnsafeExecutor` deliberately remains a source-less fallback | Audit cannot treat `ExecutorFor` alone as proof; CRUD needs a neutral resolution-kind API and audit accepts only exact source-bound resolution |
| `sqlrepo.Update` reads and locks current state only inside a transaction | The private receiver preserves the Gate's copied fence, appends its narrowing after caller options, and locks/revalidates exact before-state in the same proven root transaction before deriving the persisted diff |
| `sqlrepo.Save` is an upsert and security has race-specific handling | The sealed terminal supports no-ID and assigned-ID Save only through its hidden Gate: no-ID is create, while fenced assigned-ID `Previous` proves create versus truthful change or first-touch baseline; write-only and bulk variants fail closed |
| Security performs some inspection reads before calling its inner core | Gate authorization and inspection remain before root-transaction creation; the private receiver freezes and transactionally revalidates their fence, no option/inspector/payload getter runs twice, and alpha makes no transactional claim for policy-callback I/O |
| `tenancy.Scope` is minted by an authority | Application code maps a verified scope to audit context; audit never accepts raw tenant headers or imports tenancy |
| `event` and `jobs` expose transaction authority and durable references | Application composition can link bounded references while each subsystem retains its own record and semantics |
| `otel` decorates base seams and emits bounded diagnostics | Audit exposes a typed observer only; application OTel wiring mirrors lifecycle without values or authority |
| Module descriptors are data and profiles decide activation | Audit schema management, verifier, and purge planner are contributed explicitly, never started by `New`; later executors/relays need their own accepted contracts |
| Nested modules consume unreleased sibling modules through the workspace | Pre-release isolation fixtures need a temporary local replace, repository `make tidy`/`make check-tidy` owns tidiness, and a no-replace consumer claim waits for matching tags |

## Package topology to decide

```text
audit/                     root audit kernel and store seam
  auditmemory/             complete stdlib in-memory store
  audittest/               exported store/policy conformance
  auditcrud/               adapter for crud.Middleware
  auditpg/                 nested module; PostgreSQL implementation and integration tests
```

No `auditotel`, `audittenancy`, `auditevent`, `auditjobs`, `auditauth`,
`auditgorm`, `auditent`, or audit×storage-backend package is admitted. A future
`auditstorage` or `auditservice` is a seam adapter, not a combination package,
but it requires a concrete policy whose post-success dual-write semantics are
accepted first. The manual typed-event API covers those boundaries without a
misleading generic wrapper.

Attempt declarations, `Attempts` orchestration, typed run capabilities, transition
wire views, and least-privilege exact inspection remain in root `audit` rather than
an `auditattempt` combination package. `auditmemory` and `auditpg` implement the
same attempt append/head/Unsettled-counter/activation-anchor CAS behavior through
separate per-chain `AttemptLog` and global-counter `AttemptTypeState` facets and
retention guards, while `audittest` certifies their state-machine, restart,
identity-alias, integrity, boundedness, retention, and concurrency behavior.
Authentication, HTTP, jobs, storage, and application code translate only declared
typed facts and durable references at the composition root; audit imports none of
those owners and provides no framework-specific interceptor.

Release P1 is entirely call-driven. `NewAttempts` validates and allocates only;
deadline passage, uncertain settlement, and process restart start no goroutine,
timer, callback, scan, or reaper. Fleet enumeration, scheduled reconciliation or
abandonment, leases, and framework-specific automation remain explicit follow-up
adapters over the P1 exact-inspection and command contracts.

The root audit vocabulary may import root `crud` only for immutable Meta/Schema
identity and transaction-source vocabulary, as root `event` already does. The
executable repository wrapper remains in `audit/auditcrud`; CRUD never imports
audit and the dependency-free module boundary is unchanged.

## Current documentation defects

1. The roadmap baseline says service/storage chains, tenancy, OTel, and event do
   not exist; all now exist.
2. It links to a deleted historical snapshot.
3. It claims PostgreSQL policy and backend must be one module, contrary to the
   current dependency-cost rule and the event precedent.
4. README and ORM guides show ad-hoc audit inserts or a nonexistent `audit.Log`
   decorator without shared-transaction proof.
5. There is no audit use case, flow, decision, module page, API baseline, graph
   test, conformance suite, or runnable example.

## Current seam risks

### Update

`auditcrud.Secured` must not issue an unscoped pre-read. Its hidden Gate evaluates
caller options once and appends scope, relation, and snapshot constraints last,
so a hostile assigning option cannot erase policy narrowing. Gate authorization
and inspection complete before the mutation transaction. The private receiver
then carries the copied fence through exact source resolution (unbound for an
owned root or source-bound for a join) into a proven framework root transaction,
locks and revalidates the before-state, invokes the inner Update once, and derives
the persisted diff. Response-only options are refused exactly
as the repository refuses them; release alpha does not claim that arbitrary
policy-callback I/O is transactional.

### Delete and restore

Delete needs exact pre-state before the statement. Restore needs exact tombstone
state and exact returned post-state. The hidden Gate authorizes Restore before
the empty-ID shortcut, and `port.DefaultService.RestoreMany` forwards an empty set
instead of returning before that boundary. Only the private receiver may use its
exact scoped and tombstone effects. It locks/revalidates victims in the proven root transaction,
requires exact counts/results, and refuses an unsupported inner capability at
bind time rather than duplicating or bypassing security logic.

### Save/upsert and bulk

No-ID Save is the real create path used by `port.DefaultService`; it and
assigned-ID Save enter only through the sealed terminal's hidden Gate and private
receiver. No-ID is create, while fenced assigned-ID `Previous` proves create
versus truthful change or first-touch baseline under races. `SaveOnly`, `SaveAll`,
`UpdateAll`, `DeleteAll`, and unsupported upsert/scoped variants refuse before a
Gate callback, pre-read, root transaction, mutation, or append. Counts do not
identify bulk victims. A repository deliberately bound without `Secured` is
`outside_policy`; a declared covered action cannot silently fall through.

### Project checkout

`project/backend/go.mod` currently replaces Frostgrove packages with the nearby
detached `frostgrove/framework` worktree rather than this main framework
checkout. Integration fixtures should run in the framework repository first;
the project replace should only move in a separately coordinated change.

### Durable operation attempts

The current tree has no audit implementation, and operation grouping alone cannot
prove that a job, authentication decision, HTTP action, or application command was
attempted. Release P1 therefore adds a separate typed product shaped by
`DeclareAttempt(AttemptPolicy[Start, Checkpoint, Finish])`, `Attempts`, lower-level
`Begin`, and the advertised `Run` gate. Each policy seals exactly one
`OperationType`, its start/checkpoint/finish schemas and code inventories, maximum
open lifetime, consequence, context policy, classifications, semantic goldens, and
one nonzero target arm: an equality-searchable typed target or explicit absence. A
targetless policy stores no empty/sentinel/fabricated Reference and has no
target-history door. An ordinary business/security event may correlate through the
same `OperationID`, but neither product substitutes for the other.

The binding state machine is `Missing -> Open` through `Started`; declared
checkpoints leave `Open`; a known direct outcome moves `Open` to exactly one of
`Succeeded`, `Failed`, or `Cancelled`; and known settlement uncertainty moves
`Open -> Uncertain` through `OutcomeUnknown`. `OutcomeUnknown` is nonterminal:
checkpoints and ordinary finish refuse after it, while the distinct authorized
`ResolveUnknown` command alone may move `Uncertain` to one truthful known outcome.
Once the signed deadline has passed, an authorized `Abandon` may instead move
`Open` or `Uncertain` to `Abandoned`, meaning only that no authoritative completion
was observed by the deadline, never that business work failed. Expiry itself
changes no state. Every incompatible concurrent close conflicts; byte-identical
idempotent replay returns the committed result without advancing the head.

Every transition is a new immutable signed record. Its canonical wire binds the
attempt and operation types, globally stable `OperationID`, target presence and
the complete typed target only when present, scope/owner,
catalog/policy and derived replay fingerprints, start anchor/deadline, declared
phase/code/reason, sequence, prior leaf, expected/result state, checkpoint count,
and protected typed fields. The per-chain projection repeats target presence and,
when present, its authenticated record-era lookup coordinate and retained aliases
so a state row cannot be moved between target semantics. AttemptChainID derives only from LogID, CatalogID, and
OperationID, so operation rename, policy/key rotation, or scope change cannot hide
an existing chain; those compare-only facts conflict when incompatible. Mutable
attempt-head and authenticated Unsettled-counter rows are coordination indexes for
one full-state CAS, never evidence truth. Their public read authority is deliberately
split: `AttemptLog` returns only one bounded per-chain projection and transition
references, while `AttemptTypeState` returns only one global signed Unsettled-counter
certificate. Neither result smuggles the other facet. Resume, exact status,
abandonment, and retention fully exact-inspect the bounded per-attempt chain.
Catalog activation instead reads `AttemptTypeState`, exactly verifies the single
current signed counter-head certificate, and relies on
inductive append CAS; it never claims a bounded scan of global lifetime history.
A signed wall-clock regression does not reorder the chain; duration is derived
only when start/terminal times make it knowable, and process-monotonic duration is
Observer data only.

Required and BestEffort have deliberately different gates. Required `Begin`
performs one standalone append and returns a bound run capability only after
`Committed` plus `Inserted`; a replay returns its original receipt but no handle.
Required `Run` calls protected code only after that result and calls it zero times
on replay.
`NotWritten`, `Unconfirmed`, cancellation, denial, invalid input, and even
`Lookup.AbsentNow` after an uncertain begin yield no handle and zero protected
calls. A BestEffort begin failure remains visible and may be followed only by an
explicit caller decision to continue outside authoritative lifecycle coverage; it
also yields no handle and can never be reported as a complete attempt. Reusing an
OperationID or catalog-wide attempt-start idempotency key with any changed
operation, policy/replay fingerprint, target presence/value, stable scope/owner,
or start value conflicts before work. Ephemeral trace/correlation/client/source changes do not defeat exact
replay; the authenticated original context is returned. A required non-generated
OperationFact makes the ID restart-stable, while CorrelationFact groups multiple
distinct attempts.

AttemptResult never turns an append intention into state truth. Its
`ResultingState` accessor is explicitly optional: only an authenticated Committed
settlement with Inserted or Replayed disposition returns `(state, true)`. NotWritten, Unconfirmed,
cancellation, invalid input, and pre-append refusal return the zero state with
`false`, even when the locally prepared candidate had a deterministic next state.

A standalone terminal is appended only for a known outcome. Where the business
mutation and terminal share one exact transaction authority, an attempt-bound
group may stage them together and they commit or roll back as one revision. A
potentially committed but unconfirmed transactional terminal instead returns its
`ReconcileKey`; committed-only lookup settles that exact immutable intent, and the
caller must not race it with `Failed`, `OutcomeUnknown`, or another standalone
terminal. `OutcomeUnknown` is reserved for separately known external settlement
uncertainty. A returned error, context cancellation, deadline, or panic alone says
nothing about settlement; `Run` propagates the error, re-panics identically, and
leaves the committed attempt open unless explicit evidence truthfully closes it.

Checkpoint count, transition bytes, chain-inspection work, typed values,
references, lifetime, and code inventories have policy, request, hard, and store
ceilings, with capacity reserved for the largest direct terminal and for
OutcomeUnknown plus its largest resolution. Open and Uncertain chains are never
purge candidates; after closure the complete chain and co-revisions form one
retention cohort whose cutoff is the maximum record-era cutoff of every member,
and a hold on any member blocks all. Paging and the release-P1 PurgePlan never split
that unit. The plan excludes the current type-counter head until atomic supersession
or an authenticated activation-reset anchor and declares the future executor's
operation/start-key non-reuse coordination prerequisites. P1 neither deletes
evidence nor claims a tombstone already survived deletion. Catalog activation refuses removal or
incompatible change while exact authenticated Unsettled (Open plus Uncertain) is
nonzero, using a sealed bounded proof persisted in the catalog mutation log. Only declared typed
fields cross the boundary: request/response dumps, headers, credentials, raw
errors/exceptions, stack traces, and ambient context maps have no runtime capture
spelling. Compile-time reflection may only validate explicitly opted-in typed
schema declarations; it cannot discover values or weaken allowlists. An
application-owned admission/rate limiter must precede Begin for attacker-driven
authentication flows; that is a composition prerequisite, not a kernel ability to
recognize abuse.

Attempt investigation has two intentionally unequal products. An AttemptType-wide
page, and a target page only for a present equality-searchable declared target,
returns verified transition items across matching chains in the ordinary immutable
snapshot protocol. It never returns final state, duration, or a complete-chain
claim. `AttemptHistory.Operation` is an all-or-nothing exact door: History obtains
the per-chain projection through its explicit same-origin `HistoryConfig.Attempts`
dependency and tiles exact reads until the complete bounded start-to-head chain is
verified or returns no status. A target door on explicit absence or a non-searchable
mode refuses before ContextResolver, authority, keyring, store, observer, and denial
evidence work.

Resume, ResolveUnknown, and Abandon use a strict attempt-only access branch with
nonzero Purpose and Role as well as intent, catalog, type, operation, target
presence, scope, owner, fingerprints, and count/byte bounds. Origin-bound grants,
keyed result commitments, `AttemptContinuationAuthorized`, and
`AttemptAccessDenied` evidence preserve those exact coordinates; ordinary history
requests have a zero attempt branch.

This is the implementation reconciliation for `AU-011`, `AH-082..AH-099`,
`AE-095..AE-117`, and `AI-065..AI-083`. The implementation plan must materialize
it across catalog contracts, root transition/service/store contracts, history and
control authorization, memory/PostgreSQL conformance, application composition,
and mutation-resistant tests. It must not collapse the lifecycle into the manual
event API or leave it as an unowned adapter concern.

### Field identity and protected services

Entity getters cannot prove which model field they read. Entity policy therefore
binds each evidence field to an exact `crud.Schema` field and extracts through
that descriptor; secret metadata mechanically forbids reversible capture even
under an alias. Manual event extractors remain trusted application policy and are
checked with application canaries because a framework cannot infer credential
semantics from an arbitrary string.

Randomized protection makes one digest insufficient. A stable keyed logical
commitment drives idempotency without publishing an equality oracle, while a
second digest authenticates the exact stored protection envelope and all of its
coordinates. The kernel supplies AAD; Retry reuses the original frozen envelope
and calls no resolver, extractor, digester, protector, or signer again.

A transaction-bound external Writer cannot recover an executor from an opaque
Authority after the fact and must not maintain an Authority-to-executor registry.
Its pure `BindTransaction` call receives the exact source-bound executor and
returns a private concrete Execution that exposes only Authority, EntityHead, and
Append. The root Writer retains neither object nor executor; only the active
Recorder frame does. Aborting before append drops the sole capability reference,
and no public request or application method exposes the executor.
Manual events and captured entity state use distinct Draft and EntityDraft types.
Only the former is accepted by Recorder Record/Stage/Capture; the latter has no
standalone Writer or Recorder door and crosses the in-tree internal auditcrud
bridge only from `Secured`'s private receiver under a one-use transaction-bound
mutation guard. Application code receives no public carrier, admission token, or
binder, and Writer therefore has no standalone EntityHead method.

Required CRUD capture cannot leave mutation and audit staging as two separable
application steps. `PreflightMutation` reserves every known constraint before
business I/O and returns a one-use recorder-owned guard whose callback performs
the mutation exactly once. Any failure after mutation starts permanently poisons
the outer group, even if application code swallows the returned error; the group
then rolls back and makes no Append call.

History and control cannot be assembled from unrelated store facets. They take
the same Recorder used for evidence plus explicit least-privilege read facets,
validate one durable LogID, durable BackingID, process-local Backing, and CatalogSet, and post-validate every
row/target. Writes use only the active catalog; historical reads authenticate the
exact retained chained manifest while current policy remains the sole source of
authorization. A missing historical generation is an explicit unknown state,
never an invitation to reinterpret old evidence through current declarations.
When the admitted catalog set contains attempt declarations, History additionally
requires its explicit `AttemptLog` per-chain state dependency from that same origin;
attempt orchestration and Control receive `AttemptTypeState` separately wherever a
global counter certificate is actually required.

Broad history/search and exact control lookup are separate facets. `Log` accepts
only normalized bounded scans; `ExactLog` accepts a canonical explicit revision/
item set plus the sealed grant's catalog/resource/action/classification and protected
scope/subject coordinates. This lets an external store apply authorization before
its byte/count limit and prevents a hold or correction lookup from degenerating
into an application-side full scan.

No protected page, inventory, verification result, or purge plan may escape on an
append merely staged in the caller's transaction. History and Control refuse an
ambient audit authority/group, append bounded non-recursive evidence through a
private standalone door, and return copied payload only after a `Committed`
settlement. `NotWritten`, `Unconfirmed`, and a malicious
`InCallerTransaction` result discard the prepared payload; retry can settle the
evidence but cannot release the old data.

Inventory freezes one bounded candidate cohort at one kernel-selected instant.
Control reads its trusted clock exactly once, normalizes that value, and binds it
through the control request, grant, InventoryQuery, selection digest, and encrypted
fence. The store owns only an opaque snapshot identity and must echo the exact AsOf
before any candidate becomes eligible. Each candidate binds its exact revision,
complete authorization summary, integrity and record-era retention digests, hold
epoch, and active-hold set digest. That summary contains every item
resource, every item action, every classification across actors/context/subjects/targets/values/
changes, the admitted scope, and every subject/searchable-target coordinate.
Lifecycle authorization is all-of over that summary, never representative-label
or any-item matching. The action-bound InventoryQuery carries the effective
record-action ceiling before the store applies its limit. Holds are identified memberships `(HoldID, RevisionRef)`, so two
matters can overlap and releasing one cannot clear another. An authenticated,
encrypted fence binds the cohort to durable LogID, durable BackingID, active
catalog/catalog-set, frozen requester and aliases, normalized query, original and
prior-effective grant ceilings, as-of, expiry, and a rotating retained key; it is bounded before decode and remains
restart-safe without exposing candidate identities.

Control selection and grants carry declaration-bound logical EvidenceSelectors
for both entity subjects and searchable event targets. Each selector is exact or
explicitly all-values for one coordinate kind/resource/action/mode/classification;
empty never means wildcard. History/Control, not declaration handles, derive the
active and retained operational tokens/commitments. Store queries carry that
match mode, while stored authorization summaries remain exact only.

ScopedReference is one identity, not two adjacent strings. Both components are
independently bounded and its canonical form is a domain tag plus two
length-delimited values. The exact pair survives context admission, authorization,
tokenization, grants, continuations, evidence, and revocation. Equal local values
under different namespaces never intersect.

Entity continuity commits explicit scope absence or that complete pair before it
derives subject aliases. The typed scope commitment is repeated in mutation
reservations, alias bindings, head requests, genesis, and predecessor validation,
so equal resource/ID values under tenant and region scopes cannot share a chain.

Every event declaration seals a nonzero union between a typed target and explicit
target absence. The choice is manifest, semantic, integrity, authorization, result,
and replay meaning; absence has no encoded Reference or aliases. Typed target
history seals only declaration identity plus a copied logical present target.
History derives retained token coordinates with its configured keyring; the access
authority receives logical Resource/Action/target/classification/mode, while store
tokens remain internal. A target door on an absent declaration refuses before
authority or I/O. Protected-only and redacted present targets remain recordable but
also have no target-history door or broad-scan fallback.

The history surface also seals resource-wide, entity-index, event-type/index,
actor-hop, declared context, operation-type/instance, attempt-type/target/exact-
operation, entity-neighbor, and exact revision/item classes.
Resource and operation-type discovery remain scope/time/action/limit bounded and
cannot spell an all-resource scan. Field/context projection, observed/
occurred/recorded time semantics, direction, changed fields, and event code
predicates normalize before authority and remain identical or narrower through
grant, StoreQuery, cursor, post-validation, and access evidence. Event indexes are
manifest-declared typed equality coordinates; no JSON/property expression or raw
backend predicate crosses the root kernel.

Entity indexes follow the same closed rule but bind one exact declared schema field
and authenticate optional Before and After canonical values on each entity item.
Their only comparisons are typed BeforeEquals, AfterEquals, and EitherEquals;
absence never equals a value. Plaintext, tokenized, and indexed-protected equality
are the only storage modes, and History alone expands active/retained lookup aliases
under explicit hard count and byte bounds. `AnyOf` is one canonical nonempty bounded typed value set for
one origin-sealed selector, capped by MaxSelectorAlternatives. `AllOf` is one
canonical nonempty conjunction of at most MaxQuerySelectors origin-sealed actor,
context, event-index, entity-index, or attempt-target selectors compatible with the
bound typed door; item predicates are satisfied by one item rather than distributed
across sibling items in a revision. The logical selector
algebra and work bounds bind authorization. Only after allow does History derive
bounded internal aliases; StoreQuery, snapshot/cursor, result evidence, and
authenticated post-validation bind their exact expansion. No string field path or
generic property language is introduced.

The one-coordinate AnyOf is retained because a finite status/reference set can be
answered in one frozen cohort; it is not a general OR node and cannot combine
different coordinates or bypass the primary typed history door.

Entity reconstruction exposes an authenticated lifecycle state derived from the
complete legal action prefix: EntityPresent, EntitySoftDeletedState, or terminal
EntityHardDeletedState.
Comparison includes its endpoint states and reports an indeterminate lifecycle
difference whenever either boundary is epistemically incomplete. Exact neighbor
navigation is a separate bounded query. Its predecessor and successor independently
return NeighborPresent, NeighborChainBoundary, or NeighborUnavailable;
NeighborChainBoundary requires verified genesis or verified current head,
predecessor presence authenticates the signed direct link, and successor presence
closes the exact head-to-target path. Missing retention, authorization, integrity,
or budget proof remains NeighborUnavailable and discloses no inaccessible reference.

Every paged origin freezes a finite immutable cohort under explicit count, byte,
identity-byte, expiry, and cumulative grant ceilings. A continuation advances
only within that snapshot, so concurrent or backdated appends cannot enter the
walk. Reconstruction comparison shares one verified subject chain and combined
budget; hidden or incomplete endpoints remain indeterminate. Hard-delete heads
remain terminal across active and retained identity aliases, preventing database
key reuse from becoming an unaudited new incarnation.

Release P1 deliberately does not turn declaration-wide entity history into a
resource-wide point-in-time set. Such a future API needs its own bounded population
snapshot, completeness watermark, authorization summary, and access evidence; it
cannot be inferred by paginating per-subject reconstruction.

Correction authorization commits the complete proposed logical draft with a
domain-separated keyed semantic digest. The grant echoes that exact digest;
authenticated post-lookup checks then bind the original item, resource, target,
retention, and consequence before append. No store-facing token is exposed as the
authority's logical target.

Returned denials distinguish NotConfigured, Suppressed, LimiterFailed,
NotWritten, Unconfirmed, and Committed without exposing causes or recovery data.
A limiter panic follows the global cleanup-and-identical-repanic rule and returns
no denial state.

Access/control request, grant, result, and continuation fingerprints plus hold- and
denial-request fingerprints are not unkeyed summaries. Recorder's configured
semantic service derives them through disjoint domains over exhaustive typed
normalized coordinates, including operation role, query class, target presence,
the complete logical equality-selector conjunction, reconstruction
boundary/budgets, control reason/limit/hold intent, exact cursor protocol/key/expiry,
result head leaf/status/hold projection, and continuation-origin request/grant
commitments, so low-entropy subjects cannot be enumerated offline or substituted
across roles. A custom semantic service is an explicit trusted stable keyed-PRF,
termination, complexity, determinism, and concurrency boundary; finite construction
fixtures do not prove its future behavior.
Store-query and result domains additionally bind the exact bounded internal
storage-alias expansion derived only after authorization; authority never receives
the token/protected lookup bytes.
The same honest boundary applies to custom identity, tokenization, protection,
reveal, signing, verification, cursor, and fence providers: only audit-owned
HMAC/AES helpers make PRF/AEAD/unforgeability/nonce/key-continuity properties a
framework guarantee, while external providers are explicit deployment TCBs whose
finite vectors prove only executed calls.

AtObservedTime obtains the store-present asserted current subject head, exact-reads
and authenticates that signed revision, and walks its predecessor chain through
genesis. It computes only the causally closed signed ObservedAt prefix. If an
excluded later-than-request predecessor has a descendant at or before the requested
instant, the result is temporal ambiguity rather than a projection through the
ineligible predecessor. Search must reach the asserted head, so a dropped suffix is
detectable relative to it. RecordedAt and backend positions remain useful for stable
store pagination but cannot change the projection. A hostile store that rolls back
both history and an older still-valid asserted head is outside this store-present
completeness claim unless the application supplies an independently retained anchor.

CatalogAdmin is deliberately a deployment SPI, not a runtime control service.
Install and every direct-child activation persist a bounded external change-ledger
reference because audit cannot bootstrap its own first trustworthy audit row. A
bounded immutable catalog-mutation query makes the complete ordered record
independently comparable after reopen; install/activate success without that exact
readback is non-conforming, and a mutation that would exceed the complete-log count
or byte ceiling refuses before persistence. Deployment and runtime constructors return distinct
concrete handle types, and the runtime dynamic type has no CatalogAdmin methods, so
neither interface embedding nor type assertion puts administration into the serving
graph.

Hold placement/release first authorizes a keyed request digest over command, HoldID
commitment, target, matter presence and commitment, requester, purpose, role, and
catalog without store I/O. Only the sealed grant permits a bounded exact transition-
chain read from which Control derives the expected and result states. One
AppendRequest carries the byte-identical authorization commitment, expected state,
and signed result candidate. A dedicated mutually exclusive HoldTransitionWireView persists command,
HoldID, stable private HoldIdentity, target, matter representation/presence,
expected/result projections, and disposition under envelope and integrity
signatures. The set digest uses the fixed domain over the canonical unique active
HoldIdentity set for the exact log/revision and is unchanged by commitment
rotation or transition order. Placement always has matter;
release has none and zero matter commitment, including no-op shapes. The store locks
aliases, membership, and projection, CASes every expected component, and returns the
signed result atomically; stale/conflict/not-found append nothing. Stable
SemanticDigest is the keyed command identity, while AppendIntentDigest binds the
exact state-derived signed candidate for same-revision retry. After reauthorization,
a committed-only keyed lookup may return the authenticated original result before
current-state reconstruction, and Append repeats that match atomically to close the
absence race. Matter codec/classification/protected mode are part of the
control manifest and AAD rather than hardcoded by a backend.

The resolver is the sole consumer of caller context values. Immediately after it
returns, observable cancellation wins: its value, error, and diagnostic cause are
discarded and the public path exposes only normalized context.Canceled or
context.DeadlineExceeded with no raw CauseOf payload. Recorder, History, and Control
pass every later collaborator a Deadline/Done/Err-only context whose Cause is the
normalized cancellation sentinel, never the caller's cause object, and explicit
copied logical facts. Reconciliation Lookup additionally requires
committed visibility and zero live authority. RetryToken is intentionally
process-local; ReconcileKey plus a rebuilt idempotent operation is the durable
restart path.

The root store SPI must be genuinely implementable from another Go package.
Requests, rows, protected envelopes, manifests, cursors, fences, and results use
opaque values only where exported validating constructors and copied accessors
preserve every invariant. External-package compile fixtures are part of the
contract, not a documentation approximation.

Adoption is itself a frozen contract: auditmemory exposes LogSpec/NewLog,
Spec/New, Begin/WithTransaction/Commit/Rollback; audittest exposes a non-vacuous
Factory, returned Report with per-section Verdicts, and declaration/subject/codec
application proxies; auditpg exposes Spec/New, deterministic Schema/fingerprint/
migration statements, explicit Verify/Manage lifecycle, readiness, Check, and
caller-owned pool semantics. The two stores additionally expose equivalent
attempt transition/head operations through AttemptLog and global Unsettled-counter/
activation-anchor operations through the separate AttemptTypeState facet,
retention-cohort guards, catalog-mutation readback, and exact inspection, while audittest
exercises them through public factories rather than backend knowledge. External
consumer fixtures compile every surface.

## Competitive requirements reconciled with Frostgrove

| Common framework feature | Frostgrove decision |
|---|---|
| automatic CRUD history | ship for explicitly covered exact mutation paths |
| actor/request resolvers | sealed per-fact allowlists, incomparable provenance sets, requiredness, classification, and storage mode; no ambient raw maps |
| include/exclude/mask | allowlist only; explicit redacted/protected/tokenized modes |
| snapshots/diffs | distinct entity revisions; reconstructable declared projections and epistemically honest ordered comparison |
| revision grouping | explicit operation/revision identity; atomic only under shared authority |
| as-of/reify/revert | reconstruct declared projection at revision or signed observed time; revert is a new authorized mutation |
| class/resource-wide as-of entity set | explicit follow-up requiring a bounded population snapshot and completeness watermark; P1 resource-wide revision history is not this claim |
| relationship/M2M auditing | explicit later policy; never implied by row update |
| retention/pruning | identified overlapping holds, bounded encrypted inventory fence, and dry run before automation |
| legal-hold concurrency | authenticated transition chain plus signed full-state CAS and explicit place/release state machine |
| request/action audit | separate typed business/security event, linked by operation ID |
| targetless activity/operation | explicit declaration-level absent-target arm authenticated end to end; never an empty or synthetic reference |
| operation scope/attempt lifecycle | take Audit.NET's explicit start/checkpoint/end intent, but use a catalog-declared typed `Started -> Open/Uncertain -> one terminal` append-only chain, globally stable operation identity, Required-start fail-closed gating, explicit `ResolveUnknown`, signed CAS, bounded activation proof, and indivisible retention; never mutable replace/discard or ambient object/exception capture |
| changed-property lookup | sealed EntityIndex with Before/After/Either equality, bounded AnyOf and compatible origin-sealed AllOf, internal retained privacy aliases, and no string-path/property DSL |
| version reify/previous/next | authenticated EntityPresent/EntitySoftDeletedState/EntityHardDeletedState projection state and exact NeighborPresent/NeighborChainBoundary/NeighborUnavailable predecessor/successor claims rather than guessed total navigation |
| viewer/search/export | capability-based typed resource/subject/entity-index/event/index/actor/context/operation-type/attempt-type/attempt-target/exact reader with immutable cursors; broad attempt pages never claim exact status, and export remains separate |
| exact target inspection | separate grant-narrowed ExactLog seam; no broad scan or private store assertion |
| auditing audit access | dedicated non-recursive access evidence |
| multi-database support | one backing per atomic revision; correlation across backings |
| hooks/interceptors | one adapter per owner seam; explicit bypass matrix |
| integrity | keyed logical commitment plus exact protected-envelope integrity, optional signature, append-only controls, and an honest tamper-evident claim |
| telemetry/SIEM | lossy mirror/outbox, never system of record |

## Required repository changes

Delivery checkpoints are ordered foundation/core, then CRUD plus Frostgrove
composition integrations, then advanced history/reconstruction/attempt/control,
then the final hostile-edge, concurrency, mutation, and full-suite hardening pass.
This changes implementation order only; it removes no release-P1 requirement.
The CRUD-alpha checkpoint covers its four supported happy mutation paths and
focused boundary failures; the broad combinatorial substitution/property matrix
remains mandatory in the final hardening pass.

- add the audit decision, use case, flows, module pages, usage guide, example, and
  replace the stale roadmap;
- add root `audit`, `auditmemory`, `audittest`, and `auditcrud` packages;
- add nested `audit/auditpg` module, workspace entry, module checks, and live tests;
- add release-P1 typed `AttemptPolicy`/`DeclareAttempt`, `Attempts`, `Begin`/`Run`,
  bound run and completion capabilities, immutable transition wire/state
  constructors, `ResolveUnknown`/`Abandon`, committed-only reconciliation, and
  least-privilege attempt-type/target investigation and exact attempt inspection
  to root `audit`; target presence is an explicit declaration arm;
- implement identical attempt-head/Unsettled-counter CAS, idempotent replay, attempt-bound
  atomic terminal grouping, global OperationID/start-key uniqueness, retained
  identity aliases, full-chain verification, retention guards, PurgePlan executor-
  prerequisite declarations, and
  restart behavior through separate `AttemptLog` and `AttemptTypeState` facets in
  `auditmemory` and `auditpg`, with public `audittest` conformance; History receives
  its same-origin AttemptLog explicitly, access binds Purpose/Role, and
  ResultingState is optional evidence truth;
- add a narrow root/savepoint transaction-scope query to `crud`/`crudsql` and amend
  D-082 without changing existing resolver behavior;
- narrowly amend D-061 for the sealed `auditcrud.Secured` authority firewall,
  whose fixed `security -> private audit receiver -> faults/store` graph omits
  `Next`, and make `faults -> Secured` fail at bind time before savepoint creation;
- add the private one-use auditcrud bridge reachable only by the composite's
  receiver, without a public mutation-admission vocabulary, token, binder, or
  unsecured auditcrud entry and without importing audit into either owner package;
- extend dependency/module/docs checks without weakening existing manifests;
- add one tracked normalized, reciprocal AU/requirement/test/section/package/mutant
  registry and a section-aware audit-trace executable that requires every active
  reserved behavioral test to emit a non-skipped pass event from its declared
  package;
- add a deployment-only catalog workflow with externally authorized change-ledger
  references, immutable queryable mutation history, a direct-child activation loop
  using sealed bounded attempt-counter proofs and replayed original gates, and
  distinct concrete deployment/runtime handles;
- add a trusted Control clock with exact inventory AsOf echo, an asserted
  store-present authenticated subject head, causally closed observed-time
  reconstruction, typed signed hold transitions, and exhaustive origin-bound keyed
  request commitments;
- add a closed typed history algebra, declared secondary event and entity indexes,
  Before/After/Either equality, bounded canonical AnyOf/AllOf selectors, exact
  inspection and tri-state entity neighbors, immutable bounded search cohorts,
  authenticated projection lifecycle, shared-budget comparison, and terminal
  hard-delete entity heads;
- replace nonexistent or non-atomic audit snippets in README and ORM guides;
- add application-only integration fixtures covering security ordering, verified
  tenancy projection, event/job/storage references, admission-limited auth and
  explicit HTTP/job attempt translation, OTel observer isolation, and module-profile
  activation;
- add one test and named surviving-mutant obligation per attempt use case/invariant,
  including start-before-callback, no-handle Required/BestEffort failures,
  `OutcomeUnknown -> ResolveUnknown`, terminal/abandon races, unconfirmed
  transaction reconciliation, forged projection/resume, retention cohort, catalog
  authenticated Unsettled guard, bounds with direct and uncertainty-resolution reserves, panic/cancellation honesty, and
  application limiter ordering;
- add named positive controls and mutants for absent-target capture/query refusal,
  EntityIndex side and privacy-alias closure, single-item AllOf semantics,
  lifecycle-state authentication, neighbor Boundary-versus-Unknown honesty, broad
  attempt-page versus exact-status separation, AttemptLog/AttemptTypeState facet
  isolation, optional ResultingState, and Purpose/Role substitution;
- keep fleet scans, schedulers/reapers, leased abandonment/reconciliation, and
  framework-specific automatic attempt adapters, plus resource-wide point-in-time
  entity sets, as follow-up rather than hidden runtime or an implied completeness
  claim started by a constructor;
- keep fenced purge execution, physical deletion, authenticated non-reuse
  tombstone materialization/retention, and post-deletion reuse behavior as one
  separately accepted follow-up contract rather than a release-P1 planning claim;
- run unit, vet, formatting, tidy, examples, integration twice, module graph,
  docs, race, and package-specific mutation-resistant tests.
