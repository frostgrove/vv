# Auditable evidence roadmap — 2026-09-01

**Status:** active implementation roadmap. S0 and the S1 foundation are
implemented; the recorder, concurrent memory store, sealed CRUD integration and
PostgreSQL append/deployment/history path form the current alpha base. Protected
disclosure, broader Frostgrove composition and the advanced lifecycle/control
surface remain in delivery and are not credited as complete below.

**Supersedes:** the one-module, PostgreSQL-first topology and provisional wrapper
examples previously carried by this document. The 2026-08-26 snapshot remains a
historical research source, not current architecture.

**Contract:** [[D-136]], [[UC-034]] and [[FL-040]].

## Baseline checked on 2026-09-09

The repository now contains the dependency-light audit kernel, a concurrent
memory writer, the sealed CRUD adapter and a nested PostgreSQL writer/deployment
module. The prior no-runtime baseline is obsolete. The current vertical slice
can compile catalogues, capture declared events and entity revisions, protect
evidence before the store boundary, append/reconcile standalone records, group
work under a proven source-bound transaction, and co-commit supported CRUD
mutations with evidence. Public one-page history verifies record-era catalogues
and signatures before disclosure. Both stores expose an immutable bounded
catalog-mutation log; PostgreSQL readiness verifies the exact managed schema and
catalog lineage. Application fixtures exercise the real auth, tenancy, security,
faults, CRUD, SQL, event, jobs, storage, observer and i18n seams. Generated
Serving and Deployment definitions prove the runtime/admin split, and a live
PostgreSQL fixture co-commits the real `sqlrepo` business row and audit revision
in one source-bound transaction across create, update, soft delete and restore.

This is an alpha base, not the full product described by this roadmap. The basic
bounded history kernel and both store implementations are usable now;
access-evidence-before-protected-disclosure, durable attempts,
advanced selectors and reconstruction, correction, holds, purge planning and
the final hostile-edge pass remain explicitly unshipped until their gates pass.

| Delivery slice | Current state | Usable boundary |
|---|---|---|
| S0 contract and trace authority | implemented | The architecture, use cases, invariants and executable reservations are current |
| S1 declarations and protection foundation | implemented | Catalogues, codecs, trusted context, privacy admission, commitments, protection, integrity and safe failures are usable |
| S2 recorder and memory | alpha | Manual event/entity append, grouping, idempotency, retry/reconciliation, concurrent memory persistence and signed public one-page history are usable; protected disclosure belongs to S3 |
| S4 sealed CRUD | alpha | Create, assigned/unassigned Save, update, hard/soft delete and restore are transactionally supervised; ambiguous bulk/write-only paths refuse |
| S5 PostgreSQL | partial alpha | Exact schema readiness, catalog activation/readback, append, transaction joining, lookup, basic history and restart identity are implemented; exact inspection/control parity is owed |
| S6 Frostgrove composition | partial alpha | Auth/tenancy/security/faults/CRUD/SQL/event/jobs/storage/observer/i18n wiring, generated serving/deployment profiles and live PostgreSQL CRUD atomicity are executable; broader deployment graphs and adoption artifacts remain |
| S3 advanced investigation/control | not shipped | Attempts, cursors, reconstruction, comparison, neighbours, corrections, holds and purge planning remain planned |
| S7 final hardening | not started | Deferred edge, mutation, concurrency and whole-tree release gates remain planned |

| Current Frostgrove fact | Audit consequence |
|---|---|
| The root module has no third-party requirement | Dependency-light policy, memory and conformance work stays in the root module |
| Event sourcing uses a root kernel/memory/conformance split and a nested PostgreSQL module | Audit follows the same dependency-cost boundary |
| Optional effects do not tunnel through unknown wrappers | Covered CRUD needs one sealed, deliberately ordered authority firewall |
| Source-bound transactions and datasource identity exist | Atomic evidence is possible only through exact root-transaction proof |
| Authentication, tenancy, jobs, event, storage, telemetry and module profiles already own their semantics | Audit accepts explicit bounded projections at application composition; it imports none of those owners pairwise |
| Structured errors and deterministic presentation already exist | Audit preserves the machine failure contract; localized text is never evidence or authority |
| Constructors are not lifecycle owners | Audit starts no migration, worker, exporter, reaper or timer |

Concurrent OTel and i18n changes must be re-read at every integration checkpoint.
They are not evidence that an audit capability has shipped.

## Release boundary

Audit is four products sharing one protected evidence model:

1. explicit business and security events;
2. committed entity revisions;
3. durable operation-attempt lifecycles;
4. audit-control evidence for access, grants, corrections, verification,
   inventory, holds and purge planning.

Reconstruction is a protected reader capability over declared entity evidence,
not a fifth record product. An operation identity may correlate the four products
and one proven transaction may co-commit them. An event does not prove a complete
row revision, a row diff does not prove a business decision, and neither proves
that protected work started before a durable attempt record.

The planned package topology is:

```text
audit/                  root policy, record, recorder, reader and store kernel
  auditmemory/          complete concurrent in-memory store
  audittest/            public conformance and application-policy probes
  auditcrud/            sealed transaction-aware CRUD adapter
  auditpg/              nested PostgreSQL module
```

The first four are root-module packages. PostgreSQL is a nested module because
it carries an independent dependency decision. No application that uses only
the kernel, memory store or conformance suite pays for that driver ecosystem.

There is no production audit combination package for authentication, tenancy,
jobs, event sourcing, storage, telemetry, an ORM, router, broker or dependency
injection container. Application-only fixtures may import all selected modules
to prove their composition.

## Non-negotiable boundary

- Evidence is allowlisted. New fields, context values and callback results are
  absent until declared with stable meaning, classification and retention.
- A target is either a typed present target or explicit target absence. An empty
  or fabricated reference is never used for targetless activity.
- Actor, scope, source, reason, correlation, causation and trace facts carry
  explicit provenance. Raw claims, headers, tokens, request bodies, errors, SQL
  and arbitrary maps have no audit spelling.
- Canonical encoding, redaction, tokenization and protection finish before the
  store or observer boundary.
- Atomic means one exact source-bound root transaction over one backing.
  Correlation, matching configuration, a savepoint or two successful commits is
  not proof.
- Evidence describes committed persisted semantics. Denied, rolled-back, no-op
  and unconfirmed work never becomes a successful revision.
- The framework retries no business mutation and reruns no protected callback.
  Unknown outcomes stay reconcilable unknowns.
- Ordinary resource access grants no history access. Every read has independent
  purpose, role, scope, projection and budget authority, and its use is audited
  before sensitive results are released.
- History and corrections are append-only. Integrity is called tamper-evident
  under named keys and controls, never universally tamper-proof.
- Retention inventory and hold-aware purge planning may ship; physical deletion,
  key destruction and export require later accepted execution contracts.
- Constructors validate and allocate only. Every migration, worker, scan,
  publication and destructive action is application-owned.

## Competitive primary-source matrix

The comparison is pinned to exact commits checked on 2026-09-09. It adopts
useful product capabilities, not upstream internals or their implicit trust
models.

| Source | Capability worth carrying forward | Frostgrove boundary |
|---|---|---|
| [PaperTrail change predicates](https://github.com/paper-trail-gem/paper_trail/blob/098058ae472d13763fe66e6866a6a4dfc64a3eca/lib/paper_trail/version_concern.rb#L99-L168) and [reification/navigation](https://github.com/paper-trail-gem/paper_trail/blob/098058ae472d13763fe66e6866a6a4dfc64a3eca/README.md#L686-L705) | before/after lookup, reconstruction, previous/next navigation | Typed declared equality indexes replace string/hash queries; lifecycle and neighbours report unavailable evidence instead of pretending navigation is total |
| [django-simple-history as-of manager](https://github.com/jazzband/django-simple-history/blob/e71cb9e054da29f2112d0e146e71962160da4324/simple_history/manager.py#L158-L208) and [population command](https://github.com/jazzband/django-simple-history/blob/e71cb9e054da29f2112d0e146e71962160da4324/simple_history/management/commands/populate_history.py#L10-L44) | instance/class as-of views and history for existing rows | First-touch capture records a truthful baseline rather than a synthetic creation; resource-wide point-in-time sets remain follow-up until completeness can be proved |
| [Audit.NET creation policies](https://github.com/thepirat000/Audit.NET/blob/15a5e5b2996f3f4649bfa18e6b9a7c30da45a354/src/Audit.NET/EventCreationPolicy.cs#L6-L15) and [timed/end behavior](https://github.com/thepirat000/Audit.NET/blob/15a5e5b2996f3f4649bfa18e6b9a7c30da45a354/src/Audit.NET/AuditScope.cs#L185-L203) | explicit start/end policy, duration and checkpoints | Attempts are typed append-only transitions; start is never discarded or replaced, raw exception/target objects are never cloned, and uncertainty is a first-class state |
| [spatie activity-log schema](https://github.com/spatie/laravel-activitylog/blob/c37c43b32ea1ff15dc00491c07dafdfb8fd57340/database/migrations/create_activity_log_table.php.stub#L11-L20) | optional subjects, actors, event names and grouped activity | Target absence is a sealed declaration meaning rather than nullable storage convention; generic JSON properties are rejected |
| [JaVers query builder](https://github.com/javers/javers/blob/cc835d974ca9b8a24b2d9b7cd54aad9c4b32d1ef/javers-core/src/main/java/org/javers/repository/jql/QueryBuilder.java#L75-L78) and [changed-property filter](https://github.com/javers/javers/blob/cc835d974ca9b8a24b2d9b7cd54aad9c4b32d1ef/javers-core/src/main/java/org/javers/repository/jql/QueryBuilder.java#L197-L216) | type-wide discovery, initial changes and query work visibility | Discovery is scope/time/budget bounded; fields are origin-sealed typed declarations; first touch captures all reconstructable after-state while diagnostics expose only non-identifying work counters |
| [Envers changed/not-changed criteria](https://github.com/hibernate/hibernate-orm/blob/4a89bfc9c98debcad450716a8871c895245989f2/hibernate-envers/src/main/java/org/hibernate/envers/query/criteria/AuditProperty.java#L52-L68) and [revision queries](https://github.com/hibernate/hibernate-orm/blob/4a89bfc9c98debcad450716a8871c895245989f2/hibernate-envers/src/main/java/org/hibernate/envers/query/AuditQueryCreator.java#L165-L203) | changed-field predicates and entity revision history | The query algebra is finite and typed, old catalogues retain record-era meaning, and schema evolution produces explicit unknown knowledge rather than a guessed value |

These references do not prove Frostgrove behavior. They justify the product
questions; Frostgrove's own mutation-resistant tests and conformance suites must
prove its answers.

## First-release capability set

### Capture and truth

- Typed targetful and targetless events with stable action, outcome and reason
  codes.
- Declared entity revisions for no-ID create/save, assigned save, update, soft
  or hard delete and restore through one sealed security-first CRUD path.
- Full reconstructable after-state on the first changed touch of a pre-existing
  row, without labelling that row newly created.
- Exact idempotent replay, distinct certainly-not-written and unconfirmed
  outcomes, per-subject predecessor continuity and optional signatures.
- Required or best-effort evidence consequence declared explicitly.

### Attempts

- Independently committed required start before protected work.
- Bounded checkpoints, open and uncertain states, one truthful terminal,
  authorized abandonment, exact restart resume and committed-only
  reconciliation.
- Separate per-chain history and global unsettled-state authority.
- Broad type/target investigation returns transitions; exact status requires a
  complete authenticated chain.

### Investigation and reconstruction

- Protected bounded history by exact resource/scope and subject, declared entity
  index, event type/target/index, actor hop, context coordinate, operation or
  attempt coordinate.
- Typed before/after/either equality, bounded alternatives and compatible
  conjunctions; no generic property or backend predicate language.
- Immutable snapshot cursors that freeze request, authority ceilings, catalogue,
  store, progress and expiry and reauthorize every continuation.
- Exact revision/item inspection, changed-field filters and role-specific
  projection.
- Reconstruction and comparison for declared reversible fields with explicit
  absent, redacted, tokenized and unknown knowledge.
- Authenticated present, soft-deleted and hard-deleted lifecycle state and
  independent tri-state predecessor/successor answers.

### Operations

- Append-only catalog lineage with externally authorized deployment changes.
- Concurrent in-memory store and non-vacuous public conformance.
- PostgreSQL store with explicit managed or verified schema and caller-owned
  connection/transaction lifecycle.
- Independently committed access evidence, corrections, integrity verification,
  overlapping identified holds, retention inventory and fenced hold-aware purge
  plans.
- Closed safe errors that preserve branchable causes while exposing no evidence,
  key, selector, SQL, driver message or internal coordinate.

## Frostgrove integration boundary

| Owner | First-release integration |
|---|---|
| CRUD security and faults | One sealed security-first terminal owns the root transaction, revalidates the authorized fence and records exact persisted state; unsupported write-only/bulk effects refuse before I/O |
| Authentication | The application maps the authenticated context returned by its guard into declared actor and provenance facts; denial evidence is separately limited |
| Tenancy | The application maps an authority-minted namespace and local scope; equal local identifiers in different namespaces remain different evidence subjects |
| Event sourcing | A committed event reference may be recorded as a bounded link under separate event and audit authorities; no event payload is copied and neither record implies the other |
| Jobs | Logical effect identity and per-delivery diagnostic identity remain distinct; required attempts prove start/settlement without pretending at-least-once delivery is exactly once |
| Storage | A manual typed event may name a governed logical namespace/key reference; content, credentials and signed URLs are excluded, and cross-backend dual writes are not called atomic |
| Structured errors and i18n | Audit failures keep one machine kind/code/sentinel contract; presentation may localize safe wording but cannot change authority, classification or evidence |
| OpenTelemetry | An application-local observer mirrors bounded outcomes, duration and exact work deltas only; no subject, actor, scope, selector, value, key or cause becomes a signal |
| Application modules and health | Profiles opt into recorder, history and attempt services explicitly; PostgreSQL readiness can contribute a health probe, while constructors contribute no workers |
| Cache | Cached data is never audit authority, history truth, cursor state or a substitute for store verification |

## Delivery order

The labels preserve the plan's stable identities; delivery follows dependency
rank rather than numeric order.

| Rank | Section | Usable outcome |
|---:|---|---|
| 0 | S0 | binding decision, consumer contract, current roadmap and executable trace authority |
| 1 | S1 | names, errors, declarations, catalogues, codecs, context and privacy foundation |
| 2 | S2 | usable manual recorder/basic history alpha, concurrent memory store and conformance core |
| 3 | S4 | sealed CRUD create/save/update/delete/restore alpha plus focused Frostgrove composition fixtures |
| 4 | S3 | attempts, advanced typed history, reconstruction, comparison, neighbours, correction, holds and purge planning |
| 5 | S5 | nested PostgreSQL module, explicit schema lifecycle and live conformance |
| 6 | S6 | exhaustive cross-module fixtures, adoption docs, examples and structural checks |
| 7 | S7 | final hostile-edge, concurrency, mutation and whole-tree hardening pass |

This order deliberately makes the recorder and CRUD path useful before advanced
features. Security invariants that prevent false authorization, atomicity or
commit claims remain immediate gates. Non-blocking combinatorial and hardening
findings accumulate for S7 instead of restarting design review around every
increment. Each implemented section gets one fresh happy-path reviewer and one
fresh adversarial reviewer after its tests; a fix round is rechecked by new
reviewers.

### Alpha thresholds

- After S2, an application can declare and record protected manual evidence and
  read basic bounded history against memory. This is an application-development
  alpha, not durable production audit.
- After S4 and its core composition fixtures, the supported CRUD paths are alpha
  usable with explicit bypass reporting.
- After S3, the advanced reader, reconstruction, lifecycle and attempt surfaces
  are alpha usable.
- After S5, PostgreSQL durability and live transaction behavior exist, but broad
  integration and final hardening still remain.
- Production-release language is forbidden until S6 and S7 close their gates.

## Bypass matrix

| Path | Required verdict |
|---|---|
| Manual declared event | atomic audit append; same-unit atomic only under shared exact authority |
| Supported CRUD through the sealed adapter | entity mutation and evidence commit or roll back together |
| CRUD in a caller transaction without the recorder's active group | refused before mutation I/O |
| Assigned save | created, changed or truthful first-touch baseline from locked persisted state |
| Write-only and bulk methods without exact outcomes | refused before callbacks, reads, transaction or mutation |
| Raw SQL, ordinary ORM lifecycle, builder shortcut, relation/M2M change | outside policy unless a separately declared writer profile proves it |
| Database cascade or trigger | outside policy unless a database evidence profile declares and proves it |
| Targetless event or attempt | valid explicit absence; target search refuses before authority and I/O |
| Broad attempt investigation | transition evidence only, never inferred exact status |
| Telemetry, logs or event history | optional mirrors/references, never the authoritative ledger |
| Audit read or control action | dedicated authority and independently committed use evidence |
| Purge request in release one | bounded hold-aware plan only; no physical deletion |

## Verification and release gates

Every section must satisfy its cumulative trace checkpoint and its focused unit,
race or live-store profile. Tests carry positive controls so removing the
behavior makes the claimed proof fail. Store conformance must run against both a
real implementation and deliberately broken stores; unsupported capability is
reported, not skipped into a pass.

The release gate includes:

- exact public-consumer compilation and no-constructor-I/O checks;
- allowlist, secret, protection, safe-error and telemetry canaries;
- transaction, rollback, uncertainty, idempotency and concurrent-writer tests;
- supported CRUD truth plus fail-closed unsupported-effect tests;
- access substitution, cursor, stable-cohort, reconstruction and lifecycle
  attacks;
- attempt start-before-work, restart, uncertainty, terminal race, retention and
  catalog-activation tests;
- memory and live PostgreSQL conformance, including transaction joining and
  unknown-commit behavior;
- dependency, module, no-combination, no-lifecycle and external-consumer graphs;
- full formatting, tidy, vet, vulnerability, examples and structural checks;
- the integration suite twice in succession under the race detector;
- fresh happy-path and adversarial final review over the complete diff.

## Production follow-up

- Explicit bounded legacy backfill and relationship/M2M policies.
- Resource-wide point-in-time entity sets with a population snapshot and
  completeness watermark.
- Derived reconstruction checkpoints that never replace immutable predecessor
  verification.
- Fleet-wide overdue-attempt discovery, leases, ownership handoff and scheduled
  reconciliation or abandonment.
- Fenced purge execution, physical deletion, authenticated non-reuse tombstones
  and signed export/outbox delivery.
- Database trigger or CDC profiles for writers outside Frostgrove.
- Partition automation, WORM archive anchoring and KMS/HSM adapters.
- Cross-region causal settlement and ergonomic generation from a reviewed
  manifest.

Each follow-up needs its own accepted contract. None may be hidden behind a
constructor or inferred from a release-one capability.

## Explicit non-goals

- Audit every field, table, request, ORM hook or raw statement automatically.
- Generic serialization of models, context, claims, errors or metadata.
- A generic analytics warehouse or unrestricted query language.
- Hidden distributed transactions, automatic mutation retry or exactly-once
  delivery.
- Automatic revert, purge, export, relay, migration or background work.
- Pairwise extension packages or optional-extension imports from the kernel.
- Universal legal compliance, complete observation of external writers or a
  tamper-proof claim.

## Definition of done

The first durable release is done only when all eight sections have their exact
cumulative checkpoint, every production-core requirement has a non-vacuous
passing proof and distinct positive neighbour, both stores pass the public
contract they claim, the supported CRUD and integration paths are runnable by an
external consumer, PostgreSQL behavior is live-tested twice under race, and the
final docs state current capability without crediting a planned package.

Until then, this roadmap's section and alpha labels are delivery status, not
promises that a runtime surface already exists.
