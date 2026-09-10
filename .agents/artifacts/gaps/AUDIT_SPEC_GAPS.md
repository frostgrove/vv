# AUDIT SPECIFICATION GAPS — ROUND 1

**Review verdict:** FAIL before code
**Reviewers:** fresh happy-path/DX and fresh adversarial/security/concurrency agents
**Rule:** code starts only after every critical/high item is closed and a new
pair reviews the revised documents from clean context.

## Critical

### G-001 — accepted create/replace path did not match the real service

`port.DefaultService.Create` and `Replace` call repository `Save`; they do not
call optional `crud.Creator`/`crud.Replacer`. Those optional effects are also
erased by the required outer security gate. The draft planned to refuse `Save`
while promising create/replace.

**Disposition:** redesign the adapter around the actual paths:

- a security-admitted no-ID `Save` is a create and audit owns its atomic transaction;
- the one-shot admitted Save from the outer security gate carries `Previous`, so
  assigned-ID create versus update is exact and race-fenced;
- every direct Save without that admission is refused before I/O;
- the technical item says `entity.created` or `entity.changed`; the service verb
  `replace` is a separate business action when the application needs it;
- direct optional `Creator`/`Replacer` are not advertised in release 1;
- `SaveOnly` remains refused because persisted after-state is incomplete.

The revised plan must prove this against `DefaultService`, security, faults, and
opaque wrappers rather than add unsafe forwarding methods.

### G-002 — recorder/history did not own the manifest

`Compile` produced a manifest, but `New` accepted none. History and
reconstruction therefore had no explicit source of policy generations, codecs,
setters, or projections.

**Disposition:** `audit.New` takes one immutable `CatalogSet` whose active and
retained chained manifests come from explicit compile/retain calls. The spec
supplies stable catalog ID, owner, generation, parent reference, and digest.
Recorder writes only the active catalog; History and Control authenticate each
historical revision against its retained manifest while current policy authorizes
the operation. There is no registration on import or first use.

### G-003 — lifecycle/export promises exceeded the frozen seam

The use case promised purge execution and at-least-once export, while the release
and Store API contained only inventory/dry-run support.

**Disposition:** release 1 contains correction/dispute append, integrity verify,
retention inventory, legal-hold placement/release, and a fenced hold-aware purge
plan. Purge execution and external delivery move to explicit follow-up until a
store CAS/fence and export outbox contract land. No release coverage row claims
them.

### G-004 — shared transaction commit uncertainty was absent

`crud.InAtomic` returns a commit error directly. An owned transaction may have
committed both business and audit data even when its response was lost.

**Disposition:** auditcrud tracks whether its callback ran and whether it returned
nil. An error after a successful callback on a wrapper-owned transaction becomes
an opaque `ErrCommitUnconfirmed`; its cause is reachable only by `CauseOf`. A
joined transaction produces no final receipt; final commit classification belongs
to its owner. Tests kill the connection around commit and cover deadlock,
serialization, rollback failure, and retry/reconciliation.

## High

### G-005 — identity and idempotency model was contradictory

The draft conflated a semantic operation name with an operation identity and
alternated between revision-ID and idempotency-key uniqueness.

**Disposition:** four types and two digests:

- `OperationName` is a declared semantic code;
- `OperationID` correlates work and may come from trusted context;
- `RevisionID` identifies the persisted revision and is generated unless fixed;
- `IdempotencyKey` is optional caller-owned retry identity within a declared
  namespace;
- keyed `SemanticDigest` commits to pre-protection logical values, excludes
  generated revision ID, idempotency key, and observed/recorded time, and decides
  idempotent equality without publishing a plaintext equality oracle;
- `EnvelopeDigest` commits to every exact stored protected envelope and its
  immutable coordinates;
- `IntegrityDigest` binds revision ID, idempotency domain, observed time, catalog
  lineage, semantic digest, and envelope digest and is signed; store time/position
  stay outside the claim.

Same key+semantic digest returns the original persisted receipt. Same key with
different semantics conflicts. Same revision ID is never allowed to name a
different integrity digest.

### G-006 — nested grouping could not return a final receipt

**Disposition:** `Record` is standalone shorthand and refuses inside a group;
inside callers use `Stage`. Outermost `Within` returns the only append result.
An exact same-recorder nested group joins and returns `Joined` without a receipt;
a differing identity refuses before its callback. Nested error/panic removes only
that frame's items. Late staging refuses. Concurrent Stage is mutex-safe, and
canonical item sorting rather than scheduling order defines dense ordinal.

### G-007 — reconstruction had no concrete typed boundary

**Disposition:** add a typed reversible-field declaration with getter, setter,
and codec chain; a compiling example; `AtRevisionAfter` and signed `AtObservedTime`
boundaries; and per-field
`Known/Absent/Redacted/Destroyed/MissingKey/UnknownCodec/Gap` results. Missing or
unauthenticated catalog history is operation-level and returns no projection.
The caller retains typed `ReconstructField[M,V]` handles and reads through a free
generic `Projected` function. There is no whole-model accessor; `Get` succeeds
only for Known, so untracked or unreadable fields never masquerade as zero values.

### G-008 — manual events could not map outcome/reason/time

**Disposition:** `EventPolicy` gains typed target, outcome, reason, and occurred-at
extractors. Defaults are explicit policy values, not zero-value inference.
Observed time always comes from the recorder clock.

### G-009 — mixed item retention and signature semantics were unresolved

**Disposition:** one revision may group only items with the same retention class
and evidence consequence. A mismatch refuses before append. Holds address whole
revisions in release 1. Items have independent leaf digests inside the signed
revision digest, preserving verification and future selective lifecycle work.

### G-010 — page unit conflicted with item order

**Disposition:** public `Limit` counts complete revisions. A revision is never
split. The outer cursor follows `(recorded_at, revision_id)`; `item_ordinal` only
orders items inside it. Backend continuation is not a public cursor.

### G-011 — concurrent staging made canonical order nondeterministic

**Disposition:** release 1 treats items in one revision as a canonical multiset,
not an occurrence sequence. Stage accepts bounded concurrent calls, then sorts by
canonical item bytes and assigns dense ordinals. Thus scheduling cannot change
the digest. Causality uses explicit operation/causation identities rather than
slice position.

### G-012 — delete/restore were promised conditionally

**Disposition:** freeze exact algorithms after the dedicated CRUD design audit.
If they cannot be proven without a base-seam change, remove them from release 1
before code. The `CRUD` preset is replaced by an explicit `Actions(...)` list so
a declaration cannot accidentally claim an unsupported verb.

### G-013 — D-122 error properties were not total

**Disposition:** the decision and plan must freeze a closed class partition,
bounded panic-safe foreign error traversal, hidden causes under standard
`errors.Is/As`, cancellation precedence, unknown enum defaults, nil behavior,
and conformance defects for cyclic/joined/panicking chains.

### G-014 — semantic identifiers allowed confusable Unicode

**Disposition:** declared semantic names use a lowercase ASCII grammar with
dot-separated segments. Protected references remain bounded UTF-8 bytes and are
never interpreted or normalized by the kernel.

### G-015 — live PostgreSQL commands did not actually select live tests

**Disposition:** the auditpg checkpoint runs both unit and
`go test -race -tags=integration` from the nested module twice. Root integration
scripts are extended or an audit-specific target runs the nested live suite.

### G-016 — privacy waiver was only prose

**Disposition:** `Config` takes a typed `PrivacyAdmission`. Its zero value permits
plaintext only for public/internal classes. Every broader waiver has a stable
reason code and enters the manifest deployment fingerprint; key bytes are copied
and never rendered.

## Medium

### G-017 — examples used wrong result arity

All accepted snippets must compile. `audit.New` returns `(*Recorder, error)` and
`Record` returns `(Receipt, error)`.

### G-018 — typed subject query repeated formatting boilerplate

`ResourcePolicy.Subject(id)` returns a validated reference and
`History.For(policy).Subject(id)` creates the typed query shell.

### G-019 — reader/store cursor ownership was ambiguous

The raw store log is a trusted SPI. It consumes an unexported/sealed store grant
and a backend position. Only `History` accepts/returns the public MAC cursor.

### G-020 — store interface was too broad and transaction claims unclear

Split `Writer`, `Log`, and lifecycle capabilities; a shipped `Store` may compose
them. `auditmemory` supports atomic audit append and its own transactions but
declares cross-subsystem transaction sharing unsupported. Initial `auditpg`
publishes database/sql sharing supported and native pgx sharing unsupported.

### G-021 — module manifest generation was implicit

S6 explicitly runs and confirms the D-110 module manifest for any audit
lifecycle constructor contributed to an application fixture.

# ROUND 2 — TRANSACTION, RECOVERY, HISTORY, AND LIFECYCLE REVIEW

**Review verdict:** FAIL before code, revised for a new clean-context round
**Reviewers:** fresh happy-path/DX and adversarial/security/concurrency agents

## Critical

### G-022 — unsafe executor fallback was indistinguishable from a source binding

**Disposition:** add the neutral `crud.ResolveExecutor` result kind while keeping
existing CRUD behavior. Audit accepts only an explicit source-bound association,
never `WithUnsafeExecutor`, and tests DB-A versus unsafe DB-B with a defect fake.
The claim is intentionally caller-attested for arbitrary foreign transactions;
database/sql cannot reveal their parent pool.

### G-023 — grouped CRUD could commit outside the group append

**Disposition:** `Within` freezes the Writer authority. Auditcrud performs a pure
preflight before `InAtomic`, reserves known constraints, and executes model I/O
inside a one-use recorder-owned `MutationGuard.Run` callback. Any post-start
failure poisons the outer group even when swallowed. A group without the same
authority refuses audited CRUD before mutation I/O. Release 1 accepts only a
provenance-preserving framework root transaction; direct savepoints and raw
wrapper laundering refuse, while invisible caller-issued SQL savepoints are
explicitly outside the framework's detectable guarantee.

## High

### G-024 — Capture lost retry state

**Disposition:** Capture returns `CaptureResult`; NotWritten/Unconfirmed errors
also retain the identical opaque token. `RetryTokenOf` is the bounded safe
recovery door when an adapter's ordinary method shape cannot return the result.

### G-025 — no-key and caller-transaction retry were contradictory

**Disposition:** exact same RevisionID plus identical integrity content is a
second replay door independent of optional idempotency keys. Tokens freeze the
original mode. Transaction-bound failures are reconciliation-only and Retry
refuses them before Writer I/O; commit uncertainty exposes a safe ReconcileKey.

### G-026 — arbitrary entity getters defeated metadata-secret guarantees

**Disposition:** entity policies bind exact repository metadata and fields,
extract through `crud.Schema.Values`, fingerprint relevant metadata, refuse
reversible capture of metadata-secret fields, and test table/column/type/secret
drift. Manual-event extractors are honestly documented as trusted policy and
checked through application canaries.

### G-027 — group operation policy was undeclared

**Disposition:** Compile includes sealed OperationPolicy declarations. GroupSpec
and standalone overrides carry the handle, so retention/consequence and catalog
membership are checked before the callback.

### G-028 — History lacked evidence/protection/integrity dependencies

**Disposition:** History receives the Recorder plus explicit Log, Access,
CursorKeys, Revealer, and Verifier. It validates exact Backing, durable LogID, and
CatalogSet equality and uses a private committed-evidence barrier; wrong stores,
missing keys, unsigned evidence, malicious settlement, and recursion have defect
tests.

### G-029 — lifecycle mixed stores and trusted requested targets

**Disposition:** Control validates one Recorder/Log/Lifecycle backing and catalog,
uses a preliminary bounded decision, narrows the lookup, then post-validates the
actual stored target before every action.

### G-030 — inventory evidence could invalidate its own purge fence

**Disposition:** an authenticated encrypted fence contains one bounded exact
candidate cohort, immutable record-era eligibility digests, and per-candidate
hold epoch plus active-HoldID-set digest captured in one store snapshot. It also
binds durable LogID, Backing, CatalogSet, query, as-of, expiry, and fence-key
generation. Planning revalidates exactly that cohort. It relies on no allocation
high-water or commit-order guess, so its own later evidence and late unrelated
commits do not invalidate it.

### G-031 — reconstruction could require unbounded replay

**Disposition:** State requires nonzero revision/page/byte budgets and exposes
BudgetExceeded distinctly. Inclusive revision/time-tie semantics, missing/foreign
boundaries, MissingKey, Gap, and Unprojected are frozen and tested.

### G-032 — PostgreSQL live coverage could be vacuous

**Disposition:** the tagged suite has a fail-not-skip TestMain, an untagged
subprocess proves the unset-DSN failure, and the final whole-tree gate runs the
nested live suite twice under race in addition to every repository check.

## Medium

### G-033 — soft and hard delete shared one claimed action

**Disposition:** tombstone metadata selects an explicitly declared soft-delete
action and permits restore; absence selects hard-delete and forbids restore.

### G-034 — read boundaries and unknown reasons were underspecified

**Disposition:** AtRevisionAfter is inclusive. AtObservedTime resolves one exact
boundary from integrity-bound observations on the complete verified predecessor
chain; changes are never applied in random RevisionID or unsigned store-time
order. A missing/forked chain is TemporalAmbiguity and budget exhaustion stays
bounded unknown. Foreign/missing targets do not leak, and every unavailable
field reason has its own knowledge state. Missing catalog lineage is an
operation-level non-disclosing failure, not a partial field result.

### G-035 — least-privilege read interfaces were only prose

**Disposition:** Protector signs nothing and reveals nothing; Signer verifies
nothing. Revealer and Verifier are separate read capabilities, and Control takes
its Verifier explicitly.

# ROUND 3 — CRYPTOGRAPHY, CATALOG HISTORY, DISCLOSURE, AND LIFECYCLE REVIEW

**Review verdict:** FAIL before code, revised for a new clean-context round
**Review provenance:** the happy-path reviewer audited the frozen usecase and
plan hashes. The edge reviewer reported an older snapshot, so its verdict is
recorded only as corroborating discovery, not as the required paired gate.
Independent clean-context crypto/context and history/lifecycle design audits then
derived the dispositions below. A new pair must review the next frozen hashes.

## Critical

### G-036 — randomized protection conflicted with stable replay and exact integrity

One digest could either compare pre-protection plaintext or authenticate
randomized ciphertext, but could not safely do both.

**Disposition:** freeze two representations. A deployment-keyed logical
`SemanticDigest` drives idempotency; an `EnvelopeDigest` covers every exact stored
algorithm/key/nonce/ciphertext byte and its kernel-created AAD coordinates.
`IntegrityDigest` binds both and the signature binds only that integrity digest.
Same-key replay compares logical semantics and returns the original envelope;
Retry reuses the frozen envelope without calling collaborators. Deterministic
encryption and an unkeyed plaintext digest are forbidden.

### G-037 — current-only catalogs could not authenticate historical evidence

**Disposition:** `CatalogSpec.Previous`, exact `CatalogRef`, and immutable
`CatalogSet` model a single retained lineage. Lineage compilation rejects gaps,
forks, reused generations/meanings, incompatible codec reuse, and undeclared
privacy weakening. Writes use the active manifest; reads verify the record-era
manifest but authorize through current policy. Stores install immutable manifests
and activate with expected-parent CAS; stale writers refuse after rotation.

### G-038 — context fact admission and provenance were not representable

**Disposition:** every supported context fact has a sealed declaration containing
requiredness, classification, storage mode, and an exact allowed `ProvenanceSet`.
Provenance labels are incomparable; enum ordering never authorizes. Undeclared
facts are discarded before semantic/protection/output doors, missing required or
disallowed facts refuse before Writer I/O, and actor hops retain independent
provenance. Context-policy identity enters catalog and logical semantics.

### G-039 — protected results could escape while access evidence remained rollbackable

**Disposition:** History and Control refuse ambient audit transactions/groups and
use a private standalone non-recursive evidence append. They return no copied
payload until the receipt is `Committed`. `NotWritten`, `Unconfirmed`, and a
malicious `InCallerTransaction` settlement discard the prepared result. Retry may
settle evidence but never releases the old payload; a later request reauthorizes
and rereads.

## High

### G-040 — savepoint refusal was bypassable through raw transaction laundering

`crudsql.Transaction` deliberately normalizes wrappers and framework savepoints
to `*sql.Tx`, erasing the provenance audit needs.

**Disposition:** add a neutral transaction binding/scope result and a stricter
database/sql `TopLevelTransaction` proof. Only a directly provenance-preserving
framework root is accepted. Direct framework savepoints, raw `*sql.Tx` wrappers,
and the savepoint-to-raw-to-`From` laundering sequence are Unstated/refused.
Caller-issued raw SQL savepoints cannot be detected and are explicitly outside
the guarantee rather than falsely claimed safe.

### G-041 — a boolean hold could not represent overlapping legal matters

**Disposition:** immutable membership is `(HoldID, RevisionRef)` with protected
matter identity. Place/release transition replay is idempotent, release addresses
one membership, a released membership cannot resurrect, and eligibility requires
`active_count == 0`. Every effective transition advances `hold_epoch`; the
projection also stores the active-set digest, so H1/H2 overlap and ABA are safe.

### G-042 — opaque store values were not implementable outside package audit

**Disposition:** every external Writer/Log/LifecycleLog/CatalogAdmin request,
result, row, manifest, protected envelope, cursor, and fence gets a validating
exported constructor and copied accessor set. Constructors establish origin,
one-of, ordering, and bounds invariants; external `_test` compile fixtures prove a
real third-party implementation needs no private field or type assertion.

### G-043 — inventory fences lacked privacy, durable identity, authority, and bounds

**Disposition:** `ControlConfig` takes a dedicated rotating `FenceKeys` sealer.
The authenticated encrypted claim binds format/key generation, durable LogID,
Backing, active catalog and CatalogSet digest, normalized query, store snapshot,
expiry, and a sorted bounded cohort with integrity/retention/hold digests. Token
bytes are bounded before open and decoded counts before allocation/store I/O.
Retained keys make fences restart-safe within lifetime; key retirement, log fork,
or catalog activation fails closed.

### G-044 — nested-module tidy commands contradicted unreleased module reality

**Disposition:** repository `make tidy` and `make check-tidy` own the pre-release
workspace. `GOWORK=off` fixtures use a temporary local replace. Raw nested
`go mod tidy` and no-replace consumer checks are not release gates until the root
and nested versions are tagged together.

### G-045 — section ownership and structural/API checkpoints were incomplete

**Disposition:** S0 names the currently reserved decision/usecase/flow IDs and
their indexes; S4 owns the exact neutral CRUD files and D-082 amendment; S5/S6 own
workspace consumer manifests; S6 and the final gate run `make api` and require
human review of its diff. Docs tests resolve every new reciprocal link.

### G-046 — time reconstruction and retention could reinterpret history

**Disposition:** reconstruction follows verified predecessor links and resolves a
time request to an exact returned boundary. Per-subject recorded time is
nondecreasing or explicitly ambiguous; equal timestamps never use random revision
ordering to apply changes. Expiration uses the immutable record-era retention
digest. Later retention changes are authorized lifecycle assertions, not edits or
reinterpretations of old records.

### G-047 — post-mutation validation failures could be swallowed

**Disposition:** `PreflightMutation` reserves known subject/capacity constraints
and returns a one-use `MutationGuard.Run` callback boundary. From callback start,
repository error/panic, generated collision, head mismatch, encoding, protection,
signing, or staging failure latches the first safe error on the outer group.
Swallowing it cannot permit sealing; outstanding reservations also refuse append.
Only a successful no-op releases its reservation without poisoning.

# ROUND 4 — EXTERNAL SPI, ORIGIN BINDING, AND LINEARIZABILITY REVIEW

**Review verdict:** FAIL before code; all findings below are incorporated in the
working documents and await a fresh exact-hash pair.
**Review provenance:** two clean-context residual reviewers independently audited
the evolving Round-3 documents. Their hashes are discovery snapshots, not a pass
gate because the files changed while the sweep was running.

## Critical

### G-048 — control denial did not share the frozen authority pipeline

**Disposition:** ControlConfig now requires the application DenialLimiter and
Control has the same resolve-once, requester-alias, origin-bound decision, and
deadline/cancellation-only downstream context contract as History. ControlDenied
is separately declared; nil, false, failure, or panic cannot append or grant.

### G-049 — lifecycle reports, cursors, and rotating fences were not implementable

**Disposition:** every control command/request/result is declared. Inventory and
verification expose copied candidates/statuses, completeness, continuation,
as-of, fence, and committed evidence; purge exposes its exact eligible set.
InventoryFence and ControlCursor have bounded Parse/Bytes contracts. FenceKeys
advertises exactly one primary plus unique retained algorithm/profile/key IDs.

### G-050 — a restarted store could not return the original replay header

**Disposition:** StoredHeaderData contains the complete persisted immutable
RevisionHeaderView. NewStoredHeader reconstructs it without an old request;
NewAppendResult and NewLookupResult origin-validate it against the current
request. A keyed randomized retry returns the stored original IDs, envelope,
digests, seal, times, and outcome rather than the fresh candidate.

### G-051 — control promised exact targets over a broad-only Log seam

**Disposition:** an explicit ExactLog/ExactQuery/ExactResult seam carries only a
bounded canonical RevisionRef/ItemRef set plus sealed catalog/resource/class/
protected-coordinate constraints. One result entry per target is mandatory and
hostile omission, substitution, order, size, or scope fails before disclosure.

### G-052 — hold outcomes were guessed outside atomic append

**Disposition:** Control verifies the bounded authenticated prior transition chain
and conditional Append carries one resulting signed candidate plus a keyed
AppendIntentDigest over every expected/result state component. The store full-state
CASes while holding membership/projection locks and returns the origin-bound
projection. Replay returns the original candidate. The full place/release state
table defines no-op, conflict, resurrection, epoch, set-digest, and concurrency
rules.

### G-053 — mutation admission did not bind Save and Update payloads

**Disposition:** a direct security/audit binding yields receiver-bound one-shot
admissions. The complete Save candidate or Update payload plus options, ID,
scope, relations, snapshots, and verdict lives inside the sealed value; the
effect method has no second substitutable payload. Audit revalidates the frozen
schema snapshot before any I/O. Reverse, opaque, zero, foreign, replayed, and
payload-substitution controls are mandatory.

## High

### G-054 — projection and verification collapsed materially different states

**Disposition:** FieldKnowledge distinguishes Tokenized from Redacted and every
other unknown state. Verification distinguishes invalid, missing key, missing
seal, unsupported profile, malformed, incomplete, and unknown catalog; backend,
cancellation, authorization, and committed-evidence failures remain operation
errors with no report.

### G-055 — committed-only reconciliation lookup was not representable

**Disposition:** LookupResultData includes settlement visibility and authority.
Found and AbsentNow are valid only as Committed with zero authority and correct
header presence. Recorder strips all context values and group/transaction state;
conformance requires a committed snapshot unable to see staged rows.

### G-056 — the new invariants had no stable tests or mutation map

**Disposition:** happy, edge, and test obligations now use AH/AE/AT identifiers.
AI-041 through AI-046 have individual section rows and explicit test/mutant
families for decision replay, monotone continuation, retained tokens, aliases,
admission substitution, and every limiter outcome.

### G-057 — section ownership and gates still overlapped

**Disposition:** S1 names its root files and excludes store/reader/bridge work;
S2/S3 split memory and conformance filenames. S2 alone owns the neutral
ResolveExecutor result because Writer authority depends on it; S4 owns the
internal bridge, mutation-admission and transaction-scope CRUD changes, security,
faults, crudsql, and decision amendments. S5 owns only
auditpg-local files; S6 owns workspace/consumer manifests. S3 vets, S4 races all
changed CRUD packages, and S6 runs and checks the exact D-110 generator after
human manifest confirmation.

### G-058 — concurrency and context isolation covered only some collaborators

**Disposition:** every injected store, authority, limiter, catalog, codec,
cryptographic, clock, ID, resolver, and observer seam is concurrently safe or
explicitly wrapped. After one resolver call every downstream context is value-free
except deadline/cancellation/cause. Race doubles, buffer mutation, and context
canaries cover each door.

## Medium

### G-059 — RetryToken persistence was ambiguous

**Disposition:** RetryToken is explicitly process-local, copied, bounded by
MaxRetryTokenBytes, and has no serialization/parsing/text/JSON door. ReconcileKey
is the durable restart-safe coordinate; restart retries rebuild the declared
idempotent logical operation rather than deserialize an execution capability.

### G-060 — lifecycle “read-only” wording hid its evidence write

**Disposition:** Inventory and PlanPurge are read-only only with respect to
retained evidence, holds, and eligibility. Each performs exactly its required
standalone audit-evidence append and withholds the report until it commits.

### G-061 — public declarations and snippets contained missing or invalid symbols

**Disposition:** Catalog, ResourcePolicy, ResourceHistory, EventType,
OperationType, Declaration, Recorder/results, History/Page views, Control and all
reports are declared. Declaration is sealed. Package-qualified function
declarations were changed to valid crudsql-local syntax.

### G-062 — collaborator-returned bytes had a TOCTOU ownership hole

**Disposition:** codec/crypto/reveal/cursor/fence/identity outputs are bounded and
copied immediately. Providers retain or mutate neither request nor returned
buffers; mutation and race proxies prove the ownership contract.

# ROUND 5 — EXTERNAL EXECUTION, SATELLITE DX, AND COMPLETE TRACE REVIEW

**Review verdict:** FAIL before code; the design artifacts are being corrected and
must receive a new exact-hash happy/edge pair before S0 starts.
**Review provenance:** the Round-4 frozen snapshot was independently reviewed at
usecases `c1eb52486f8f7d6796f9e7bc5101ec2de8ebe672b966dc791fdb312c61c3fe29`,
plan `bb3a7dece6afc71660361286dcc3b4b0e2ecb705a891d3794b6ec3463fa787cb`,
reconcile `43759b84dfaf8ab580d94a596bf20f60db1af6dd7c9457e800976510e217ca5b`,
and gaps `3aa2fe47d026eb8a027f4c1acda5de9a5bb7ecc421c3bc467af06a10040b8d04`.
Two additional clean-context design sweeps checked the in-flight correction.

## Critical

### G-063 — transaction Authority could not recover an executor without a registry

**Disposition:** Writer now has a pure BindTransaction door that receives the
already resolved source-bound executor and returns a store-owned Execution
interface exposing only Authority, EntityHead, and Append. Its private concrete
captures the executor; only the active Recorder frame retains the interface.
Requests expose no executor/Execution and no public high-level method accepts one.
Abort/panic/pre-append failure drops the sole reference without registry, cleanup,
context lookup, or global state. Typed nil, authority change/substitution,
cross-frame use, and simultaneous transaction swapping are kill-tested.

### G-064 — denial-limiter panic had two incompatible meanings

**Disposition:** the subsystem-wide panic rule wins. A limiter panic performs
cleanup and zero append/grant work, then re-panics identically; it returns no error
and therefore no DenialEvidenceState. Only an ordinary returned limiter error maps
to LimiterFailed. Returned denials map exactly across NotConfigured, Suppressed,
LimiterFailed, NotWritten, Unconfirmed, and Committed and are cause-elided even
through CauseOf.

### G-080 — whole-revision target authorization had no public grant dimension

**Disposition:** control requests and grants now carry immutable logical
EvidenceSelectors for both subjects and searchable event targets. A selector is
either exact or explicitly all values for one declaration-bound kind, resource,
action set, storage mode, and classification; empty covers nothing. Control
privately derives retained tokens/commitments, store queries carry the explicit
match mode, stored summaries remain exact, and cross-target/wildcard-substitution
mutants fail all-of authorization.

## High

### G-065 — built-in codecs and HMAC helpers were advertised but not frozen

**Disposition:** all nine compileable non-generic codec constructors now have
stable v1 descriptions and exact canonical bytes/refusals. HMAC helpers synthesize
fixed algorithm and domain-separated profiles rather than accept caller-authored
descriptions. Key IDs, minimum copied keys, active-key cardinality, exact 32-byte
outputs, length framing, retained-token ordering, constant-time verification, and
cross-protocol substitution tests are explicit.

### G-066 — memory, conformance, and PostgreSQL adoption surfaces were prose-only

**Disposition:** audit.Store, auditmemory LogSpec/Log/Spec/Store/Tx,
audittest Factory/Verdict/SectionResult/Report/application proxies, and auditpg
Schema/SchemaManagement/Spec/Store/migration/readiness methods are frozen. The
same non-vacuous conformance factory runs against memory and PostgreSQL; external
compile, constructor-no-I/O, pool ownership, missing-hook, nil-store, and zero-
certification controls are mandatory.

### G-067 — typed event targets were not actually discoverable without operation ID

**Disposition:** EventTargetRef now seals declaration identity plus one copied
logical target only. History derives all retained store coordinates with its own
CatalogSet/Tokenizer. Authority receives logical Resource/Action/target/class/mode,
never store tokens. Plaintext/token/indexed-protected targets are searchable;
protected-only/redacted targets remain recordable but return an explicit
unsearchable refusal and never broad-scan. Cross-declaration substitutions are
kill-tested.

### G-068 — audit ordering contradicted D-061 and outer faults could wrap capture

**Disposition:** S0 owns a narrow D-061 amendment: direct `security -> audit` is a
semantic admission boundary, not optional-effect discovery; all ordinary exact-
outer/no-tunneling rules remain. Faults belongs inside audit and forwards neither
binder nor admitted effects. A successfully bound security wrapper exposes a
neutral exact marker, so direct outer faults refuses at bind time before creating
a savepoint. Opaque application bypasses remain explicitly outside policy.

### G-069 — the faults savepoint exception was not reconciled with root-only scope

**Disposition:** caller-presented savepoints remain refused. After root Execution
and one-use guard admission, inner faults may wrap only the business statement in
one framework savepoint and must finalize before head/capture/stage/append. Guard
rechecks root scope before each audit step. Begin/release/rollback/probe failure
poisons the root, and no Execution/request/receipt/settlement escapes.

### G-070 — ScopedReference lost its namespace in grants and canonical bytes

**Disposition:** selectors and both grant types carry ScopedReference. Each
component is independently nonempty/bounded and the pair is one domain-tagged,
length-framed canonical identity through commitments, tokens, cursors, fences,
evidence, and revocation. Typed read access returns the pair rather than exposing
an undocumented byte encoding.

### G-071 — mixed-resource lifecycle authorization still had representative labels

**Disposition:** every revision carries one authenticated canonical authorization
summary: all item resources, all classifications across actor/context/subject/
target/value/change arms, admitted scope, and every subject/searchable-target
coordinate. Inventory/Purge candidates and private fences carry that summary.
Control batch-inspects and authenticates every candidate through ExactLog and
requires all summary dimensions to lie within the sealed grant. Per-action grant
rules replace the impossible blanket “grant subset of exact RevisionRef request.”

### G-072 — correction authorization did not cryptographically bind the proposal

**Disposition:** CorrectionProposalDigest is a domain-separated keyed semantic
commitment to declaration fingerprint and complete logical Draft. Authority sees
safe logical metadata, the grant must echo the exact digest only for correction,
and authenticated post-lookup validation binds original ItemRef/resource/target/
retention/consequence. Substitution before append is an origin failure.

### G-073 — process-local Backing was used as a restart token identity

**Disposition:** Backing remains process-local facet equality; nonzero persisted
BackingID plus LogID is serialized in reconciliation, cursors, control cursors,
and fences and stored once in auditpg settings. Restart with a new pool succeeds
under the same durable IDs; deliberate fork activation rotates BackingID and token
keys. Bit-identical simultaneously served clones remain an honest external split-
brain limitation.

### G-076 — the public Draft type allowed false standalone entity evidence

**Disposition:** manual events use `Draft`; resource capture produces the
distinct opaque `EntityDraft`. Recorder Record/Stage/Capture accept only Draft,
while EntityDraft has no public append door and can cross only the in-tree
auditcrud internal bridge into a one-use supervised guard. EntityHead was removed
from standalone Writer and exists only on its transaction-bound Execution. A
compile-negative fixture and a runtime hostile bridge fixture prove that an
application cannot turn caller-authored before/after state into committed entity
evidence.

### G-077 — control continuations had no key-service operation

**Disposition:** FenceKeys now has distinct typed seal/open operations and
envelopes for InventoryFence and ControlCursor. Each format has its own request,
outer framing, AAD domain, parser bound, and cross-protocol substitution refusal;
one advertised rotating key inventory may implement both without conflating their
claims. The existing cursor mutant varies both history and control continuations.

### G-078 — protected correction targets could not be compared after lookup

**Disposition:** ControlConfig now carries the least-privilege Revealer and
requires its complete retained description inventory whenever correction is
declared. ExactLog authentication precedes a record-era decode/reveal or retained
token-alias comparison; temporary plaintext is discarded immediately and every
missing-key/malformed/wrong-description failure releases no mutation or existence
signal. Recorder supplies the already validated tokenization/write services
without exposing Writer.

### G-079 — a complete deployment still required handwritten AEAD

**Disposition:** the root now freezes no-I/O AES-256-GCM constructors over copied
caller-owned rotating key material for protection/reveal, history cursors, and the
typed inventory-fence/control-cursor protocols. HKDF-SHA-256 derives disjoint
profile keys even when a caller reuses input material; message domains remain
separate. One key is active, nonces come from crypto/rand, retained keys remain
readable, and every metadata/AAD/tag/cross-protocol mutation has a negative
control. KMS/HSM implementations remain replaceable least-privilege adapters.

### G-081 — SubjectRef claimed aliases it had no keyring to derive

**Disposition:** SubjectRef now mirrors EventTargetRef: it seals only declaration
identity plus one copied logical subject and exposes no operational alias. History
and Control derive active and retained identity/token coordinates from Recorder's
validated keyrings after origin and authorization checks. Authority views remain
logical and store views remain protected.

### G-082 — redacted correction targets had no provable equality

**Disposition:** a current redacted correction proposal refuses before authority
or store I/O. An event original must share the stable resource/action declaration;
after authenticated exact lookup its record-era target compares through
plaintext, retained token, or reveal. A record-era redacted target returns the
same non-disclosing ineligible/mismatch result with zero append. Entity originals
compare against their necessarily searchable subject. Redacted coordinates are
never guessed, inherited, or used to trigger a scan.

### G-083 — redacted scope contradicted exact lifecycle authorization

**Disposition:** any data-bearing history/control policy requires a required,
equality-searchable requester ScopeFact. Compile, Lineage, NewHistory, and
NewControl reject protected-only or redacted ScopeFact declarations in every
active or retained evidence policy they may authorize. Optional absent evidence
scope remains absent and never acts as a wildcard; once present, its exact
ScopedReference survives as plaintext, token, or indexed-protected coordinates
through summaries, grants, cursors, fences, and revocation.

### G-084 — stable outcome and reason codes were undeclared runtime strings

**Disposition:** resource/event manifests now carry immutable action-scoped
OutcomeCodes and ReasonCodes plus reason optionality; ControlPolicy carries
per-action external reason sets and a derived closed kernel-outcome table.
Extracted, denied, correction, and dispute codes are checked before protection or
Writer I/O, and historical rows use the record-era inventory. Verification and
privacy-waiver reasons use distinct closed namespaces, while narrative remains a
separately classified protected field.

### G-085 — manifest identity omitted executable and wire semantics

**Disposition:** each policy declares a nonzero semantic version and named
expected logical-output fingerprints; Compile derives a manifest
PolicyFingerprint from that contract and the complete declarative description.
Custom codec engines cross only DefineCodec, whose copied accepted/rejected wire
fixtures are executed and hashed into CodecSemanticFingerprint. audittest checks
typed subject/event samples without retaining them in production declarations.
No function address, reflection, path, closure, or binary hash is mistaken for
semantic identity; unsampled behavior changes explicitly require an author-owned
version bump and killing fixture.

## Medium

### G-074 — denial state and optional limiter requirements contradicted G-048

**Disposition:** DenialLimiter is optional. Nil or an undeclared denial action is
NotConfigured; this is distinct from false/Suppressed and returned-error/
LimiterFailed. Denial states never expose causes or recovery capabilities, and a
panic has no returned state.

### G-075 — AH/AE/AI traceability was incomplete and range-overlapping

**Disposition:** grouped ranges are replaced by one row per AH, AE, and AI. Every
row names explicit AT, implementation sections, and stable AM mutant IDs. A
reciprocal mutant registry names defect, passing neighbor, killing AT, owner, and
reserved test symbol. A stdlib AuditTrace check rejects missing, duplicate,
orphan, ranged, nonreciprocal, or checkpoint-unreachable entries and later checks
the concrete test registrations.

## Round 6 frozen-contract review

The clean happy/DX and hostile/security reviewers independently verified the
same pre-review snapshot: use cases `e99cfa08`, reconciliation `0b66eb49`, plan
`2e67ce3c`, gaps `b57a40cb`, and repository rules `2bfa3fc6`. Both returned FAIL
with no Critical finding. The dispositions below define the next snapshot and
must receive a new clean-context pair before code starts.

### G-086 — time reconstruction trusted unsigned store metadata

**Disposition:** remove AtRecordedTime and expose AtObservedTime over the complete
verified entity predecessor chain. Only integrity-bound ObservedAt selects the
boundary; RecordedAt and backend position remain unsigned operational pagination
facts. A monotone hostile rewrite and regressing signed-clock cases receive
separate controls.

### G-087 — legal-hold matter had no manifest privacy policy

**Disposition:** ControlPolicy now seals a HoldMatterPolicy containing the exact
Reference codec, classification, and protected/indexed-protected mode whenever a
hold action exists. It enters policy/catalog fingerprints, protection AAD,
lifecycle values, authorization summaries, lineage checks, and conformance; raw
matter remains absent from manifests and results.

### G-088 — advertised no-ID Save omitted mandatory security admission

**Disposition:** every public capability/release row now says
security-admitted no-ID Save. No-ID, assigned-ID, update, delete, and restore all
require the directly adjacent one-shot security effect; every ordinary direct
path returns ErrAdmission before transaction or model I/O. A negative adopter
fixture pairs that refusal with the legal DefaultService create path.

### G-089 — the trace gate passed when its own test disappeared

**Disposition:** every checkpoint invokes executable `scripts/audit-trace.sh`.
It parses `go test -json` and requires the exact structural test to emit one pass;
zero matches, deletion, or rename fails. A subprocess removal/rename mutant proves
the gate is non-vacuous, and the registry now includes reciprocal actor-goal edges.

### G-090 — a hostile hold store could select or project false successful state

**Disposition:** conditional bundles are replaced by one signed CAS candidate.
Control verifies a bounded genesis-to-head transition chain, recomputes exact
membership/count/epoch/set/head state, and signs expected plus result state. Writer
CASes every component atomically; lifecycle projections and fences are accepted
only when authenticated transition references recompute them. Transition links
are full RevisionRefs.

### G-091 — value-free contexts leaked caller cancellation causes

**Disposition:** downstream contexts delegate Deadline, Done, and Err, always
return nil Value, and expose only normalized cancellation through context.Cause.
Every collaborator canary includes a distinct secret-bearing cancel cause and
checks errors, observations, and evidence for leakage.

### G-092 — entity continuity omitted the exact evidence scope

**Disposition:** CommitEvidenceScope commits explicit absence or both canonical
ScopedReference components before every entity-subject alias. Its typed commitment
enters reservations, alias bindings, head requests, genesis, locking, rotation,
grouping, and reconstruction. Same resource/ID under tenant and region scopes has
distinct chains and foreign-scope predecessors refuse.

### G-093 — outcome and reason visibility was undeclared

**Disposition:** codes are manifest-public non-sensitive protocol labels, may not
contain user identifiers/secrets/narrative, and appear only after the containing
item/action is authorized. Sensitive explanation remains a separately declared
classified field; code membership never grants value visibility.

### G-094 — catalog administration was neither authorized nor audited

**Disposition:** bootstrap is an explicit external trust boundary. Install and
activation require and persist a bounded independently authorized
CatalogChangeRef; CatalogAdmin is deployment-only and absent from the serving
graph. Audit does not pretend that it can self-authenticate creation of its first
manifest.

### G-095 — actor goals were outside executable traceability

**Disposition:** GoalTrace maps every AU identifier to exact AH/AE/AI requirements
and AT obligations. The same reciprocal executable parser rejects an unmapped
goal, unknown edge, range, or one-way relationship.

### G-096 — final release omitted the workspace vulnerability scan

**Disposition:** S7 runs `make vuln` after the complete workspace graph is tidy
and before live integration repetition. A tooling/network failure remains a
reported release prerequisite, never a silently skipped green gate.

### G-097 — shortest composition hid the catalog deployment workflow

**Disposition:** the declarative DX includes a separate compiling workflow for
schema preparation, external change reference, ordered install, expected-parent
activation, verification, and only then runtime construction. AT-016 proves the
runtime graph cannot reach CatalogAdmin.

### G-098 — finite codec fixtures overclaimed arbitrary future stability

**Disposition:** custom CodecEngine implementations are explicitly trusted,
deterministic, concurrency-safe application collaborators. DefineCodec enforces
bounds, ownership, and fixture-observable behavior only; a post-construction
state-switch fixture demonstrates the honest limit. Built-ins remain fully
specified, and application CI goldens plus version bumps own wider confidence.

### G-099 — denial and access digests lacked a keyed construction

**Disposition:** AccessRequestDigest, AccessResultDigest, and
DenialRequestDigest are typed Config.Semantics outputs under three fixed disjoint
domains over complete normalized coordinates. Low-entropy dictionaries,
coordinate omission, cross-domain substitution, and profile rotation receive
mutants and legal neighbors.

## Current-contract supersession ledger

Earlier rounds remain historical evidence; this table is authoritative for
spellings that intentionally changed. Every row is “implemented in draft,
awaiting fresh exact-hash PASS,” not a claim that production code exists.

| Earlier gap | Current disposition |
|---|---|
| G-001 | legacy SaveScoped wording is superseded by receiver-bound admitted Save; ordinary assigned Save and all write-only forms refuse |
| G-005 | every revision replay remains byte-exact; hold writes now present one authenticated-prior/signed-result CAS candidate and no longer need a same-ID content exception |
| G-007 | reconstruction has typed handles and repeated decode, deliberately no setter or whole-model reconstruction |
| G-017 | Record returns `(RecordResult, error)` and receipt/retry state is observable through accessors |
| G-018 | typed querying is `policy.History(history).Subject`/`State`; SubjectRef is the sealed untyped coordinate |
| G-020 | store seams are Writer, Log, ExactLog, LifecycleLog, and CatalogAdmin with shared StoreInfo identity |
| G-028 | HistoryConfig includes Access, Denials, Cursors, Revealer, and Verifier plus Recorder/Log |
| G-029 | Control uses ExactLog for exact targets, Log for scans, LifecycleLog for inventory/holds/purge, and post-validates all results |
| G-041 | hold membership uses a complete serialized state machine and conditional signed append result |
| G-042 | external compile fixtures now cover ExactLog and every new conditional/control result constructor |
| G-043 | rotating cursor/fence descriptions, serialized tokens, outer/decoded bounds, and restart semantics are explicit |
| G-048 | DenialLimiter is optional; nil is the observable NotConfigured state, false is Suppressed, returned error is LimiterFailed, and panic re-raises without a returned state |
| G-056 | the former partial range map is superseded by the Round-5 exact requirement and reciprocal mutant registries plus AuditTrace enforcement |

## Round 7 — temporal truth, deployment authority, and executable-gate review

Two fresh clean-context reviewers independently audited the same frozen snapshot:
repository rules `2bfa3fc6`, use cases `6b4fd94d`, reconciliation `8783542c`,
plan `7679bc25`, and gaps `71e9fe12`. The happy/DX review found 0 Critical,
8 High, and 2 Medium findings; the hostile/security review found 0 Critical,
4 High, and 4 Medium findings. Their two overlapping High findings are recorded
once below, yielding 10 unique High and 6 unique Medium gaps.

All G-100 through G-115 entries are open. The design draft is being amended to
the intended dispositions below; none is a claim that production code or a
passing implementation checkpoint exists. The file's header verdict remains
unchanged until another exact-hash clean-context gate.

### G-100 [high][immediate] — observed-time reconstruction was not causally closed

**Evidence:** selecting the last predecessor-ordered revision whose signed
ObservedAt is at or before the requested time can include a descendant of an
excluded later-observed predecessor when signed clocks regress. The query also
lacked an authenticated current chain head from which completeness can be
proved, while external rollback after the framework boundary is not observable.

**Disposition:** resolve an origin-bound authenticated current entity head, verify
one complete genesis-to-head predecessor chain, and return only the greatest
causally closed prefix whose every member satisfies the observed-time boundary.
Missing, substituted, truncated, forked, or cyclic heads refuse. State explicitly
that rollback or mutation performed outside the accepted framework transaction
boundary cannot be detected and receives no audit-truth guarantee.

### G-101 [high][immediate] — catalog mutation authority was referenced but not queryable

**Evidence:** CatalogAdmin accepted a CatalogChangeRef for install and activation,
but no persisted typed mutation record or bounded lookup/list seam let deployment
operators prove after restart which authorization caused each catalog state.

**Disposition:** add an append-only, queryable catalog-mutation ledger. Each typed
record binds the external change reference, operation, target and predecessor
CatalogRefs, expected and resulting active state, authority digest, and durable
store identity. Install/activation replay, restart, stale CAS, and verification
must use those records; bounded deployment-only queries expose copied views.

### G-102 [high][immediate] — deployment and serving authority were only statically narrowed

**Evidence:** the composed Store and shared concrete handles could still implement
CatalogAdmin together with Writer/Log capabilities even when a constructor accepted
only a narrower interface. Interface typing alone does not remove the live admin
capability or its credentials from the serving object graph.

**Disposition:** expose dynamically distinct deployment and runtime handles with
separate constructors, concrete wrappers, credentials, and lifetimes. A runtime
handle must not implement or retain a path to CatalogAdmin; an admin handle must
not implement serving Writer/Log doors. Close deployment authority before runtime
construction and kill type-assertion, retained-reference, and shared-credential
mutants.

### G-103 [high][immediate] — documentation activation was not staged with implementation

**Evidence:** S0 owned decision and flow amendments that describe transaction-aware
CRUD behavior before S4 implements or verifies it, while later sections also own
adoption documentation. That permits published docs to claim behavior whose
owning checkpoint has not run.

**Disposition:** give every implementation section its corresponding planned-to-
implemented documentation transition and gate it with that section. S4 owns the
D-061/D-082 amendments and CRUD transaction/admission flow; S6 owns adopter and
deployment documentation after the APIs exist; S7 records final status. S0 may
reserve identifiers and write explicitly planned contracts, not mark later
behavior implemented.

### G-104 [high][immediate] — traceability was static rather than section-aware

**Evidence:** the Markdown registry reserved future symbols and promised later
registration, but it had no tracked state tying a behavioral test to the section
where it becomes executable. AM-TOPO-001 was owned by S0 even though its positive
and negative composition evidence is built in S6.

**Disposition:** maintain one tracked section-aware behavioral trace registry with
planned, implemented, and passing states, exact owning package/checkpoint, and
reciprocal goal/requirement/test/mutant edges. Each completed section must have
observable registered tests; future sections remain explicitly planned. Move
AM-TOPO-001 and its killing symbol to S6, leaving only parser anti-vacuity work in
S0.

### G-105 [high][immediate] — access commitments omitted authorization and continuation origins

**Evidence:** the access digest prose did not exhaustively bind Role and all
classification/retention ceilings, and AccessResultDigest bound only a
continuation-present bit rather than the authenticated origin of the emitted
cursor or fence. Distinct grants or continuations could therefore share evidence.

**Disposition:** freeze per-action canonical schemas. Request and denial digests
bind requester, purpose, exact Role, scope, resource/action/coordinate selectors,
classifications, retentions, catalog/backing, boundaries, byte/count ceilings,
and any decoded cursor/fence origin claim. Result digest binds the request digest,
ordered references and statuses, counts/truncation, plus the exact typed
continuation claim digest, protocol, key generation, and expiry without exposing
raw protected bytes. Add one omission mutant per coordinate.

### G-106 [high][immediate] — semantic privacy of public codes was overclaimed

**Evidence:** grammar, length, and manifest membership can reject malformed codes
but cannot prove that an otherwise valid outcome or reason string contains no
customer identifier, secret, or sensitive narrative.

**Disposition:** define code meaning as a trusted, reviewed policy-author
obligation and say so at every privacy claim. Compile enforces only mechanical
syntax and closed-set membership; explicit author admission, CI review/goldens,
and canaries own semantic non-sensitivity. A violating code is manifest-public
author error, never something the kernel claims to sanitize.

### G-107 [high][immediate] — hold authorization did not bind the exact transition

**Evidence:** origin-bound Control grants constrained broad action and target
dimensions, but no dedicated commitment required the grant to echo the exact
hold command, authenticated prior state, and proposed result state used by the
conditional append.

**Disposition:** add a keyed HoldAuthorizationDigest over action, requester,
purpose, Role, scope, catalog/backing, RevisionRef, HoldID and matter commitment,
expected membership/count/epoch/set/head, candidate disposition and complete
result state, proof head, and idempotency domain. Authority grants echo that exact
digest; Control rechecks it immediately before the signed CAS append. Substitute
each component independently in negative controls.

### G-108 [medium][immediate] — hold transition state lacked a persisted typed wire arm

**Evidence:** conditional append request/result views described hold state, but
the frozen persisted Revision item vocabulary did not expose a complete typed
hold-transition arm from which an external store or restarted verifier can rebuild
the state machine without transient request memory.

**Disposition:** define a bounded HoldTransitionItem wire arm with validating
constructor and copied accessors for command kind, target, protected matter/alias
coordinates, authorization digest, expected and resulting projection, disposition,
and predecessor/proof references. Include it in leaf, envelope, integrity,
manifest, Log/ExactLog, and conformance round trips.

### G-109 [medium][immediate] — resolver cancellation could re-export the caller's cause

**Evidence:** downstream contexts normalized context.Cause, but ContextResolver
still received the original context and could return or wrap its secret-bearing
cancellation cause before the value-free wrapper existed.

**Disposition:** at the resolver boundary, preserve only the public canceled or
deadline sentinel and discard caller cause identity, text, wrapping, and CauseOf
reachability. Resolver diagnostics remain safe classified failures only when not
derived from caller cancellation. Add wrapping, joining, and identical-text
secret-cause controls.

### G-110 [high][immediate] — lifecycle AsOf was not an exact kernel-clock value

**Evidence:** eligibility depended on a store-supplied inventory snapshot time,
allowing a hostile backend to choose or rewrite AsOf while still echoing a
plausible report and fence.

**Disposition:** sample the injected kernel Clock exactly once for each inventory
operation, bind that exact instant into authorization, query, access digest,
store snapshot request, report, fence, and evidence, and require a byte-exact
store echo. Purge planning retains the authenticated fence AsOf. Zero, future,
rounded, independently sampled, or substituted values refuse.

### G-111 [medium][immediate] — actor-goal trace edges were not fully reciprocal

**Evidence:** GoalTrace named requirement edges, but RequirementTrace had no exact
reverse Goal set, so the parser could not compare both representations for set
equality or detect every one-sided reassignment.

**Disposition:** add exact Goals to each RequirementTrace row, require bidirectional
set equality with GoalTrace, and verify that each goal-to-test edge resolves
through the same requirement, section, mutant, and executable symbol. Mutants
delete or move either side independently.

### G-112 [medium][immediate] — custom codec termination and complexity were outside the stated TCB

**Evidence:** finite DefineCodec fixtures can check returned bytes, bounds, and
observable determinism, but a custom in-process CodecEngine can loop forever,
allocate without bound, or become expensive after construction. Output limits do
not bound internal work, and a timeout goroutine would merely leak it.

**Disposition:** add termination, memory, and complexity to the explicit trusted
custom-codec author obligation; do not claim kernel containment. Built-ins retain
auditable hard bounds. Recommend process isolation for untrusted engines and add
subprocess watchdog evidence for the honest limitation without leaking goroutines
in the production API.

### G-113 [high][immediate] — arbitrary SemanticDigester implementations could defeat privacy and replay

**Evidence:** the open SemanticDigester interface could return raw SHA-256,
nondeterministic, stateful, or deployment-unstable values even though low-entropy
privacy and retry equality require a stable keyed PRF under a declared profile.

**Disposition:** make the audit-owned HMAC construction the enforceable default
and define any custom digester as an explicit cryptographic TCB admission. Its
contract requires keyed-PRF security, domain separation, deterministic stability
across concurrency/restart for one key ID, copied ownership, and declared rotation
semantics; description and key generation enter deployment/catalog identity.
Conformance vectors and defect fakes test observable violations without claiming
finite tests can certify cryptography.

### G-114 [medium][immediate] — S4 did not execute its internal bridge checkpoint directly

**Evidence:** S4 owns `audit/internal/auditcrudbridge/**`, but its checkpoint named
only `./audit/auditcrud` and `./crud/...`; the internal package's direct tests,
race run, and vet could disappear while the section remained green.

**Disposition:** add explicit test, race, and vet commands for
`./audit/internal/auditcrudbridge` to S4, together with the external compile-
negative EntityDraft door and hostile one-use bridge fixture. Register their
symbols under S4 in the section-aware trace registry.

### G-115 [medium][immediate] — deployment activation lacked a resumable multi-step contract

**Evidence:** the DX showed an ordered happy sequence, but did not freeze partial
install, restart, concurrent activator, verification failure, close, or handoff
semantics between schema preparation and runtime construction.

**Disposition:** specify deployment as an explicit resumable state machine:
prepare schema, open deployment-only authority, obtain/change authorization,
inspect current ledger state, idempotently install each manifest, CAS-activate the
expected parent, verify active lineage and mutation records, close deployment
authority, then open a dynamically separate runtime handle. Test restart between
every step, stale concurrent activation, already-installed replay, failed
verification, and proof that runtime never opens early.

# ROUND 13 — ALPHA ORDER AND REAL SERVICE PATH

**Review verdict:** FAIL, then fixed
**Reviewed hashes:** USECASES `c69d8fbd26aadb76ba3635ca6e3cf98987b9a3fe6c5612a660fa16b32ab0b2cf`;
RECONCILE `2bcaf447b4434d93dc6f1be6d460304ef97cefe4d3ddbd387a425e87daa57294`;
PLAN `2fb5a637a383eb083ec876e6d78be35305041443859bba79e48c598bf7bd7f18`.

### G-116 [critical][immediate] — numeric trace order made early CRUD impossible

**Evidence:** the delivery plan required S4 before S3, but active reservations
were selected by the numeric section suffix. The S4 checkpoint therefore required
S3 attempt/advanced-history tests before those packages existed.

**Disposition:** section labels are identities. Section rows now freeze delivery
ranks `S0, S1, S2, S4, S3, S5, S6, S7`, and the runner compares only those ranks.
S4 owns bounded alpha integration smoke tests; exhaustive AT-014 remains S6.

### G-117 [high][immediate] — DefaultService bypassed empty Restore authorization

**Evidence:** `defaultRestorableService.RestoreMany` returned `0, nil` for an
empty ID slice without invoking the repository, so fixing only Gate could not
enforce AE-021 through the real service path.

**Disposition:** S4 also owns `port/service.go`, its focused tests, and FL-015.
RestoreMany forwards the empty set to the sealed repository boundary; Gate
authorizes before its no-work result.

# ROUND 14 — TARGETED ALPHA RECHECK

**Review verdict:** PASS
**Reviewed hashes:** USECASES `1ba2985f4952cf519b0ce306ebba036a72e6848beea4988a688d21cad2e1ee6b`;
RECONCILE `b86fdf61dd23f449ed8f2647123128d866d32f7c11e0e24f0c33d685db5c5f30`;
PLAN `92b2647857c5d6c2d60f5b14a9e505bb188cafaf9dcc26470cb6b81d33866890`.

Fresh happy and edge reviewers independently confirmed that S4 excludes S3/S5,
the empty RestoreMany path reaches Gate, S4 owns its code/flow/tests, and the
alpha smoke checkpoint does not claim exhaustive AT-014 completion.
