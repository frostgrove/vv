# D-136 — Audit evidence is explicit, protected, and transaction-honest

**Status:** in force from audit S1; the S0 contract and trace authority are
current, while runtime proof is owed by the sections that implement it
**Invariant:** Audit records only declared evidence, protects it before any
store or observer sees it, and calls a business mutation atomic with its evidence
only when both share one exact proven transaction authority. Correlation,
telemetry, event history, ORM hooks and matching datasource configuration are not
substitutes for that proof.

## The decision

### Four products, one explicit policy boundary

Audit has four related products, and none may stand in for another:

1. A business or security event records a declared decision or occurrence.
2. An entity revision records the declared committed before/after evidence of
   one logical resource.
3. An attempt lifecycle proves a durable start, bounded transitions and one
   truthful terminal or unresolved outcome for protected work.
4. Audit-control evidence records access, grants, correction, verification,
   inventory, hold and purge-planning activity without recursive capture.

They may share an operation identity and one atomic revision. That makes them
correlated or co-committed; it does not make their meanings interchangeable.
Reconstruction is a protected reader capability over declared entity evidence,
not a fifth record product. A target is likewise a sealed declaration choice:
either a typed target is present or target absence is explicit. An empty
reference never impersonates either case.

The policy is an allowlist. Resources, actions, operation types, fields, equality
indexes, context facts, codes, privacy modes, reconstruction promises, retention
classes and read classes enter evidence only through a reviewed declaration.
Adding a model field, context value or callback result changes nothing by itself.
Importing a package registers nothing.

### The catalogue is record-era meaning

Every active declaration belongs to one deterministic, named and versioned
catalogue generation. A generation binds its direct predecessor and the
fingerprints of executable policy, codecs and public code inventories. Stored
evidence binds the generation that gave it meaning. Retained generations may
authenticate and decode old evidence, but only the current generation may grant
present access.

Installation and activation are deployment operations. They require an
independently authorized external change reference and queryable mutation
history because an audit ledger cannot authenticate its own first authority.
Serving construction verifies the selected lineage and never installs or
migrates it implicitly.

### Protection happens before the trust boundary

Each retained value has an explicit classification and storage mode. Plaintext,
redacted, one-way tokenized, reversibly protected and indexed-protected evidence
remain different states. Exact metadata secrets and declared secret event values
cannot be retained reversibly. No generic model serialization, request dump,
header map, bearer credential, raw exception, SQL text or arbitrary metadata map
has an audit spelling.

Redaction, canonical encoding, tokenization and protection finish before the
store or observer is called. Protection binds the catalogue, revision, item,
field or context coordinate, codec, classification and mode. Caller-owned input
is copied before it can be retained; returned data is owned by its recipient.

Actor, scope, source, reason, correlation, causation and trace facts carry
explicit provenance. Provenance labels are incomparable. Policy names the
accepted set for each fact rather than assuming that one source is stronger than
another. The trusted resolver is the sole reader of caller context; later
collaborators receive copied facts, cancellation and deadlines, not the caller's
ambient value graph or cancellation cause.

### Atomic means one exact authority

A revision is atomic with business work only when all writes use one exact
source-bound root transaction over one backing. An unsafe source-less executor,
the same connection string, a tracing relationship, a matching operation
identity or two successful commits is not proof. A caller savepoint is not a
root transaction. A faults-owned inner statement savepoint may exist only after
root admission and must finish before audit capture.

A standalone append is one store-owned atomic operation. A caller-owned group
may co-commit several declared items. A covered CRUD mutation uses one sealed
security-first authority firewall: authorization runs before audit capture, the
authorized fence is transactionally revalidated, the mutation runs once, and
the exact persisted result determines evidence. Unsupported write-only, bulk,
raw SQL, ORM shortcut, relation and trigger paths refuse or remain explicitly
outside policy; a count or caller intent never becomes a fabricated victim set
or committed after-state.

A denial, rollback, no-op or certainly failed write does not claim a committed
change. An unconfirmed append or commit remains unconfirmed. The framework does
not rerun application code or silently retry a mutation. Idempotency compares a
stable keyed logical commitment, while integrity separately commits the exact
stored envelope.

### Attempts prove start and settlement separately

A required attempt commits its declared start before protected work begins. Its
checkpoints and settlements are immutable transitions under one authenticated
expected-state comparison. Open and uncertain are distinct. Success, failure,
cancellation and authorized abandonment are distinct terminals, and no second
terminal is admitted. A lost commit is reconciled; it is never relabelled as
failure or retried under another authority.

A best-effort attempt is visibly non-authoritative and cannot gate work that
requires durable evidence. Broad operation-type or target investigation returns
transition evidence only. Exact status requires a complete, bounded,
authenticated chain through the asserted head.

### Reading is a new act of authority

Ordinary access to a resource grants no audit access. Every history,
reconstruction, comparison, neighbour, correction, verification, inventory,
hold and purge-planning request carries an independently sealed grant for its
requester, purpose, role, scope, query class, projection and limits. Store
coordinates are narrowed by that grant, then every returned row is authenticated
and checked again before it can be released.

Paged reads freeze an immutable bounded cohort. A continuation is bound to the
original request, requester, catalogue lineage, store, prior effective ceilings,
cumulative progress and expiry; current authority may narrow or revoke it but
cannot restore anything removed on an earlier page. Sensitive results are not
released until separate access evidence has committed outside any caller
transaction.

Reconstruction reports known, absent, redacted, tokenized and unknown states
without converting any of them into a zero value. Present, soft-deleted and
hard-deleted lifecycle states are authenticated evidence. Predecessor and
successor are independent present, proven-boundary or unavailable answers;
retention, authorization, integrity or budget loss never becomes false absence.

### Integrity and lifecycle claims stay narrow

Evidence is append-only. Corrections, disputes and business reversals are new
authorized assertions. A signature or digest makes exact bytes tamper-evident
under its key and storage controls; it does not make the system universally
tamper-proof. Store-recorded time and physical positions are pagination and
operator facts, not authenticated causal truth.

Retention inventory and hold-aware purge planning are bounded, fenced and
audited. Overlapping identified holds are membership state, not a boolean.
Execution of purge, key destruction or export requires a separately accepted
compare-and-swap or outbox contract. No constructor starts a writer, reaper,
exporter, migration, timer or goroutine.

### The microkernel owns seams, not combinations

The dependency-light kernel, complete memory implementation, public conformance
suite and CRUD adapter belong to the root module. PostgreSQL persistence is a
separate nested module because the module boundary represents dependency cost.
This follows [[D-033]] and the event precedent in [[D-121]].

Authentication, tenancy, jobs, event sourcing, storage, telemetry and
application modules translate their own trusted values at the composition root.
No pairwise audit extension imports another optional subsystem. Telemetry may
mirror bounded non-identifying work and outcomes, but it never carries evidence
values or authority and never changes an audit result.

### The design graph is executable authority

The tracked normalized adjacency registry, exact semantic manifest and
independent completeness anchor defined by [[FL-040]] become the executable
design authority at S0. Their separation is deliberate: adjacency proves
connectivity, semantic records freeze observable meaning, and the independent
anchor catches deletion of a closed subgraph or highest identifier that an
internally consistent graph could otherwise accept.

The drafting artifacts remain human-reviewed implementation context, not a
clean-checkout runtime or CI dependency. A later graph or semantic change
reopens S0: all three tracked authorities are updated together and fresh happy
and adversarial reviewers approve the change before dependent implementation
continues. Structural traceability cannot decide whether a legal edge expresses
the right product meaning; that remains a review obligation.

## Why the alternatives fail

- Automatic reflection and request/context dumps silently widen evidence when a
  model or middleware changes and move protection behind capture.
- Post-commit dual writes can lose evidence; a second hidden transaction can
  preserve evidence for a business change that rolled back.
- Logs, spans, metrics, CDC and event streams have different retention,
  authority and failure semantics. Treating one as the ledger turns sampling or
  exporter loss into silent evidence loss.
- A freely ordered audit decorator can run before authorization or append after
  mutation. One sealed security-first CRUD composition is the smallest surface
  on which the transaction claim is honest.
- Generic property and backend predicates move authorization into adapter
  syntax. Typed declared equality coordinates keep query power reviewable and
  bounded.
- A mutable start record replaced at completion destroys the evidence needed to
  distinguish never-started, open, uncertain and terminal work.
- One PostgreSQL-bearing audit module charges every policy-only or memory-store
  consumer for a backend choice and contradicts the repository's dependency-cost
  rule.
- One checksum over one generated graph cannot reveal that the generator and
  graph both forgot the same closed component. Independent inventories and
  semantic records are required.

## What it forbids

- No implicit field, target, actor, scope, code, context or model capture.
- No plaintext crossing a boundary declared redacted, tokenized or protected.
- No atomicity claim from correlation, configuration equality or an unsafe
  executor.
- No successful revision for denied, rolled-back, no-op or unconfirmed work.
- No generic history query language, unbounded scan or authority inferred from a
  stored row.
- No access result released before its own committed access evidence.
- No mutation, history rewrite, hidden retry, automatic revert or constructor
  lifecycle.
- No audit-by-telemetry, audit-by-event-stream or audit-by-ORM-hook claim.
- No audit combinations with authentication, tenancy, jobs, event sourcing,
  storage, telemetry, an ORM, router, broker or backend package.
- No universal compliance, completeness or tamper-proof claim.

## Proven by (owed)

S0 currently proves that the complete reviewed goal, happy-case, hostile-case,
invariant, test-obligation, mutant, positive-control, package and section graph
is materialized, independently anchored and executable as described in
[[FL-040]].

Runtime evidence is owed by audit S1 through S7. Each section must activate its
declared coverage test, every mutant-kill test and a distinct positive neighbour;
the cumulative checkpoint must observe one non-skipped pass for each exact
reservation. In addition, the implementation owes:

- external-consumer compile tests for every public construction path;
- allowlist, protection, canonical-wire and cancellation-cause canaries;
- exact transaction, rollback, uncertainty, idempotency and single-evaluation
  tests;
- sealed CRUD ordering, source identity, persisted-state and unsupported-effect
  tests;
- authorization-substitution, cursor, reconstruction, lifecycle, neighbour,
  hold and access-evidence tests;
- complete attempt state-machine, restart, retention and settlement races;
- non-vacuous memory and PostgreSQL conformance with deliberately broken stores;
- module-graph, no-lifecycle, observer-privacy and application-composition tests;
- race, live PostgreSQL and final whole-tree checkpoints run twice where required.

None of those runtime guarantees is current merely because it is named here.

## See also

[[D-020]] [[D-021]] [[D-033]] [[D-061]] [[D-082]] [[D-115]] [[D-118]]
[[D-121]] [[D-134]] [[D-135]] [[UC-034]] [[FL-040]]
