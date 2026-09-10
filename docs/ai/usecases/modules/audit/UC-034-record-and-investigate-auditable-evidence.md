# UC-034 — Record and investigate auditable evidence

**Actor:** the application author defining accountable operations, the
investigator reviewing them, and the operator preserving the ledger
**Covered by:** [[FL-039]]

## Scenario

An application needs more than diagnostic logs when it answers who approved an
order, which committed values changed, whether protected work actually started,
and what an authorized investigator could see later. Some evidence is about a
business or security decision, some about a persisted entity revision, some
about the lifecycle of an operation attempt, and some about access or lifecycle
control over the ledger itself. Reconstructing declared state is a protected
reader capability over entity evidence. Treating these concerns as one generic
event loses the distinctions an incident review depends on.

The author wants to declare those meanings once, capture only reviewed evidence,
and make a covered mutation and its evidence commit together. The investigator
wants bounded typed questions whose answers remain protected and independently
audited. The operator wants a complete in-memory implementation for tests, a
durable production implementation, conformance proof, legal-hold-aware planning
and an honest account of every path the framework cannot cover.

## What must hold

1. Business or security events, entity revisions, operation attempts and
   audit-control evidence are distinct declared products. They may share a
   correlation or atomic revision without one being presented as proof of the
   others. Reconstruction is an authorized view over entity evidence rather than
   another record product.
2. Every resource, action, operation type, target-presence choice, field,
   equality index, context fact, code, privacy mode, reconstruction promise,
   reader class and retention rule is allowlisted. New model fields and ambient
   context values never enter evidence implicitly.
3. A targetful declaration records one typed target. A targetless declaration
   records authenticated target absence and has no target-history door. Empty or
   fabricated references never blur that distinction.
4. Invalid, duplicate or semantically incompatible declarations are refused
   before traffic starts. A deterministic catalogue lineage binds the policy,
   code and codec meaning used by each record, and retained generations decode
   old evidence without granting present access.
5. Actor chains, scope, source, reason, correlation, causation and trace facts
   carry declared provenance. Raw claims, headers, credentials, request bodies,
   errors, stack traces, SQL and arbitrary metadata maps are not accepted as
   evidence.
6. Canonical encoding, redaction, one-way tokenization and reversible protection
   finish before a store or observer receives a value. A secret cannot be
   retained reversibly merely because a caller supplied a codec.
7. A manual event is appended atomically. An entity mutation is called atomic
   with its evidence only when both use the same exact proven root transaction
   and backing. Correlation, matching configuration and separate successful
   commits never satisfy that claim.
8. A covered create, save, update, delete or restore is authorized before
   capture, revalidates the authorized victim in the transaction, executes its
   callbacks and mutation once, and records the exact persisted result. A
   denial, rollback, no-op or unconfirmed commit creates no false success
   revision.
9. Write-only, bulk, raw SQL, ORM shortcut, relation, cascade, trigger and
   external-writer paths are either covered by a separately proven policy,
   refused before mutation, or reported outside policy. A count or stale caller
   value never becomes per-entity evidence.
10. Several items form one atomic revision only under one transaction authority.
    A swallowed inner failure cannot allow the outer group to seal, and a caller
    savepoint cannot impersonate a root transaction.
11. Certainly-not-written, conflict, unconfirmed, canceled, unreadable and
    backend failures remain distinguishable. The framework reruns neither a
    business mutation nor protected application code. Reconciliation never
    turns uncertainty into a guessed result.
12. Repeating an identical idempotent request returns the original committed
    result; changing its meaning conflicts. Stable logical equality is separate
    from the commitment to exact randomized protected bytes.
13. A required operation attempt durably records its start before protected work
    begins. Checkpoints, uncertainty and settlement are immutable bounded
    transitions under one expected-state comparison, and exactly one truthful
    terminal may win.
14. An uncertain attempt remains uncertain until an authorized reconciliation
    resolves it. Restart resumes only an exact verified open chain. A
    best-effort attempt is visibly non-authoritative and cannot satisfy a policy
    that requires durable start evidence.
15. Audit access is independent of ordinary resource access. Every search,
    exact read, reconstruction, comparison, neighbour lookup, correction,
    verification, inventory, hold and purge plan is authorized for its exact
    requester, purpose, role, scope, query class, projection and bounds.
16. History exposes a finite typed query vocabulary over declared resource,
    subject, entity-index, event, target, actor, context and operation
    coordinates. Equality sets and conjunctions are canonical and bounded. No
    generic property path, raw backend predicate or empty wildcard is accepted.
17. A history origin freezes one bounded immutable cohort. Every continuation
    binds the original request and grant, store, catalogue lineage, prior
    narrowing, cumulative progress and expiry, and is reauthorized before use.
    Concurrent or backdated commits do not enter that walk.
18. Returned records are authenticated and checked against the sealed grant
    after the store responds. Sensitive results become visible only after
    separate access evidence has committed outside any caller transaction;
    denial evidence is rate-limited and non-recursive.
19. Reconstruction returns only declared reversible fields and authenticated
    present, soft-deleted or hard-deleted lifecycle state. Missing keys, unknown
    codecs, redaction, tokenization, uncaptured fields and incomplete history
    stay explicit rather than becoming guessed values.
20. Comparing two ordered boundaries shares one authorization and work budget.
    Equality is indeterminate when either endpoint is unknown. Predecessor and
    successor independently report present, proven chain boundary or unavailable;
    unavailable evidence never becomes false absence.
21. Evidence is append-only. Corrections, disputes and reversals are new linked
    assertions. A hard-delete terminal closes that logical subject chain; reuse
    of the same database key cannot silently create a second history.
22. Integrity authenticates canonical record-era meaning and exact stored
    envelopes under declared keys. It is described as tamper-evident under those
    controls, never universally tamper-proof or proof that every external write
    was observed.
23. Retention inventory and purge planning are bounded, snapshot-fenced and
    aware of overlapping identified holds. Placing or releasing one membership
    cannot erase another. Physical deletion, key destruction and export do not
    occur until their own accepted execution contract exists.
24. The in-memory and durable stores implement the same observable contract and
    preserve caller-owned transaction and resource lifecycles. A public
    conformance suite reports unsupported capabilities rather than counting
    skipped behavior as certified.
25. Authentication, tenancy, jobs, event sourcing, storage, telemetry and
    application composition translate only bounded declared facts at the host
    boundary. No pairwise production integration package is required, and
    telemetry remains a lossy non-authoritative mirror without evidence values
    or authority.
26. Constructors validate and allocate only. They start no migration, watcher,
    worker, exporter, reaper, timer or goroutine. Destructive and background work
    starts only through an explicit application-owned lifecycle.
27. A consumer that does not use audit neither imports its packages nor pays for
    a PostgreSQL driver. The dependency-light vocabulary, memory implementation,
    conformance suite and seam adapter remain usable without a third-party
    dependency.
28. Every guarantee is connected to an implementation section, a coverage
    obligation, a deliberately broken behavior and a distinct legal neighbour.
    Missing, skipped, orphaned or moved proof fails the cumulative checkpoint
    instead of becoming an undocumented gap.

## Out of scope

- Automatically auditing every model, field, request, ORM lifecycle or raw SQL
  statement.
- Generic model or context serialization, unrestricted metadata and a generic
  analytics language over evidence.
- Hidden distributed transactions, automatic retries, automatic revert,
  exactly-once delivery or a workflow engine.
- Treating an event stream, trace, metric, log, database trigger or CDC feed as
  authoritative audit evidence by implication.
- Automatic purge, key destruction, export delivery, archive, relay, migration,
  fleet scan, overdue-attempt reaper or background processing.
- Resource-wide point-in-time entity sets until a bounded population snapshot
  and completeness watermark have their own contract.
- Universal legal compliance, universal completeness or a tamper-proof claim.
