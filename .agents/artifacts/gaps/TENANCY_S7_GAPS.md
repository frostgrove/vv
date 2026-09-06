# TENANCY - S7 core/seam split - GAPS

## Round 1 — architecture and boundaries — 2026-09-06

**Metrics counted (commands and results in each finding).** Kernel imports of a concrete
extension: **0** (`grep -E "vv/(crud/decorators|jobs|storage|cache)|vv/tenancy/tenancy" tenancy/*.go`
-> no match). Kernel branches naming an adapter: **0**. Adapter -> adapter imports: **0**
(the only `vv/tenancy/tenancyX` lines under an adapter directory are that package's own
`_test` package importing itself). Import cycles: **0**. First-party dependency graph
(`go list -deps`): core **3** (`utils`, `crud`, itself), `tenancyrow` **8**, `tenancydb` **4**,
`tenancyjobs` **5**, `tenancystorage` **5**, `tenancycache` **5**. External (non-first-party)
dependencies of `./tenancy/...`: **0**. Internal-import fan-out per package: core **1**,
`tenancyrow` **3**, all others **2**. Longest function **33** lines
(`tenancydb.Directory.Borrow`, `Authority.Accept`); longest file **284** lines
(`tenancydb/database.go`); max nesting depth **3**. Exported top-level names (tests excluded):
core **28** + **48** methods + 15 consts + 11 error vars; `tenancyrow` 8+10, `tenancydb` 6+7,
`tenancyjobs` 2+2, `tenancystorage` 2+0, `tenancycache` 4+3. `go test -race ./tenancy/...`
green, `go vet` clean, `gofmt -l` silent.

---

### GAP-1 [high][immediate] `Sealer.Seal` takes a bare scope and checks no class, so the durable admission gate left the core when the adapter left the package

- **Where:** `tenancy/seal.go:41-55` (`Seal` -> `this.authority.minted(scope)` at `:45`, never
  `accept`); `tenancy/context.go:77-83` (`From` is public and returns the carried scope with no
  class check); the check that still exists lives in the adapter at
  `tenancy/tenancyjobs/jobs.go:36` (`this.authority.Scope(ctx, tenancy.ClassDurable)`).
  Against `docs/ai/decisions/D-117-a-verified-scope-is-minted-never-manufactured.md:4` and `:97-100`.
- **What:** Before the split, the class check and the MAC were one unexported path inside
  `tenancy/jobs.go`. Now `Seal` is exported and `minted` only proves *this authority minted this
  scope* — it does not ask whether the tenant's lifecycle admits `ClassDurable`. D-117:4 states
  "every factory that performs work — the repository policy, **the durable producer and
  restorer**, the source directory, the grant loop — re-asks that authority whether the scope's
  lifecycle admits the class of work being asked for", and D-117:97-100 states "Do not let a
  factory that performs work take a bare `Scope` ... `From(ctx)` is public, so the value such a
  function needs is always one call away". `Seal` is on the wrong side of both sentences.
  `tenancystorage.Namespace` and `tenancycache.Keyed` — which only *derive a name* and are
  explicitly excused by D-117 — both take `(ctx, authority, class)` and re-check. The one core
  primitive that mints a durable credential does not.
- **Why this severity:** Reproduced. With
  `Admission = Admit(ClassRead, Active, Migrating) ∪ Admit(ClassWrite, Active, Migrating) ∪
  Admit(ClassDurable, Active)` and a tenant in `Migrating`:
  `authority.Verify(ctx, ClassDurable)` refuses (control), but
  `scope, _ := tenancy.From(ctx)` followed by `sealer.Seal(scope, ...)` returns a **44-byte
  durable token**. `tenancyjobs` is safe today only because `Capture` happens to call
  `Scope(ctx, ClassDurable)` first — which is exactly what
  `TestAProducerRefusesToCaptureAScopeTheDurableClassDoesNotAdmit`
  (`tenancy/tenancyjobs/durable_test.go:171`) pins. The next seam that calls `Seal` (D-118's
  outbox is the named one) inherits no such check: it writes a record for a tenant the
  deployment refused durable work for, and that record executes the moment the tenant returns
  to `Active` without an epoch change, because the restorer's `Lookup(..., ClassDurable)` then
  passes.
- **Why this timing:** It is the public contract of a brand-new core primitive that a second
  adapter is planned against. Changing `Seal`'s signature after an outbox seam exists is a
  breaking change to two packages instead of one; and the invariant D-117 states is currently
  held by convention repeated per adapter rather than by the kernel.
- **Close criteria:**
  - [ ] `Seal` resolves the scope itself (`Seal(ctx, class, binding...)`) or takes a class and
        runs the same admission check `Authority.accept` runs; no exported path produces a
        durable token from a scope the class does not admit.
  - [ ] A test in `tenancy/seal_test.go` seals from a lifecycle the durable class does not
        admit and asserts `ErrInactive`, with the admitted lifecycle as the control.
  - [ ] D-117's "Where it lives" names `tenancy/seal.go` and states which check `Seal` performs.
- **Status:** open

---

### GAP-2 [high][immediate] The seal MAC omits `Origin`, so two deployments that share a `DurableKey` accept each other's durable records

- **Where:** `tenancy/seal.go:83-98` (`Sealer.mac` writes count, binding fields, epoch,
  reference — no origin) versus `tenancy/scope.go:65-76` (`bind` writes
  `writeField(mac, origin)` first) and `tenancy/contract_test.go:42-67`
  (`TestTheOriginFencesOneDeploymentFromAnother`, which covers the scope binding only).
- **What:** `Spec.Origin` exists because, in this repository's own words
  (`tenancy/binding_test.go:8-13`), it is "the half that survives a deployment cloned from
  another's configuration, where the salt is regenerated but the wiring is copied verbatim".
  A `DurableKey` **is** copied wiring — it is a configured secret, not a per-process value, and
  D-117:44-49 requires the producer and worker to share it. The seal has no origin, so the
  deployment fence that the scope binding carries does not exist on the durable record.
- **Why this severity:** Reproduced. Two authorities, same `DurableKey`, `Origin`
  `"invoices.staging"` and `"invoices.production"`: production unsealed a record staging sealed
  and returned `ref="acme" epoch=1 err=<nil>`. Concretely: a production database restored into
  staging (or a staging queue table pointed at a shared instance) hands the staging worker
  production tenants' work, and the record verifies before the control plane is asked. The seal
  is meant to answer "did *this deployment* write this record"; it currently answers "did
  anybody holding this key write it". Not a regression against the jobs-specific token it
  replaced (that MAC'd namespace digest ‖ definition ‖ epoch ‖ reference and had no origin
  either), but the generalisation made it a core primitive that every future durable seam
  inherits.
- **Why this timing:** One `writeField(mac, this.authority.origin)` now; after any durable seam
  ships it is a token-format change that invalidates every in-flight record in every queue.
- **Close criteria:**
  - [ ] `Sealer.mac` includes the authority's `origin`, length-prefixed, as the scope binding does.
  - [ ] A test in `tenancy/seal_test.go` mirrors `TestTheOriginFencesOneDeploymentFromAnother`
        for the seal: same key, two origins, refused, with the same-origin case as the control.
  - [ ] `tenancy/seal.go`'s doc comment states that a record is bound to the deployment and not
        only to the key, and FL-033 step 7 says so too.
- **Status:** open

---

### GAP-3 [high][immediate] The object namespace claims a redaction property it does not have, citing the invariant that forbids exactly what it does

- **Where:** `tenancy/tenancystorage/storage.go:36-41` (the comment) and `:42-51`
  (`namespaceOf` -> `prefix + "-" + hex(sha256(len‖reference ‖ epoch)[:16])`);
  `tenancy/scope.go:78-93` (`Scope.Digest`, unkeyed SHA-256);
  `docs/modules/en/tenancy.md:246-250` and `docs/modules/ru/tenancy.md:251-255`;
  `docs/ai/flows/FL-033-a-request-becomes-a-tenant-bound-statement.md:98-105`.
- **What:** The comment says the namespace is a digest "because a bucket path is quoted in
  provider errors, access logs and support tickets, **which is exactly where INV-25's redaction
  has to hold**". INV-25
  (`.agents/artifacts/usecases/TENANCY_USECASES.md:1714-1727`) forbids "a tenant reference, **a
  stable tenant hash**, a database, schema, DSN or user, a pool key, a bucket, prefix or object
  reference" and names the consequence: "a 'stable hash' makes it a cross-signal join key". The
  namespace is precisely a stable, unkeyed, per-`(tenant, generation)` hash used as a bucket
  prefix. The second half of the comment — fixed width, so no tenant's namespace prefixes
  another's — is sound and is what the code actually buys.
- **Why this severity:** The digest is `SHA-256(uint64(len(ref)) ‖ ref ‖ uint64(epoch))` with no
  key and no salt. Tenant references in the shipped example are `"42"` and `"43"`
  (`_examples/tenancy-sharedrow/main.go:115`); real ones are customer slugs. An attacker or an
  analyst holding one bucket listing recovers every tenant name by enumerating a candidate list
  against epochs 1..N — the whole search space is a few thousand hashes. So the value the
  comment says must not be in a support ticket is recoverable from the support ticket, and the
  same value is a join key across every log line, provider error and cache partition
  (`tenancy/tenancycache/cache.go:51` reads the same digest). A reader who trusts the comment
  will put the namespace in a metric label.
- **Why this timing:** Object addresses are durable. Once tenants have objects under
  `prefix-<sha256>`, changing the derivation is a bucket migration per tenant, not a code change.
- **Close criteria:**
  - [ ] Either the derivation becomes a MAC under a configured, deployment-stable key (the same
        class of secret as `Spec.DurableKey`, so the address is stable across processes and
        unguessable without it), or the comment, `docs/modules/{en,ru}/tenancy.md` and FL-033
        state the property that actually holds — fixed width and injective, **not** redaction —
        and stop citing INV-25.
  - [ ] A test asserts whichever property is claimed; if the claim stays "redaction", the test
        must fail for an unkeyed digest.
- **Status:** open

---

### GAP-4 [high][immediate] The layout law is a hand-written table: a sixth package under `tenancy/` and a new base subsystem both pass it

- **Where:** `scripts/tenancy_test.go:34-46` (twelve subsystems enumerated by hand),
  `:56-65` (five adapters enumerated by hand), `:78-83` (the core half, which is derived and
  does work). D-116:107-112 cites both halves as the proof.
- **What:** Both loops iterate a literal list. Nothing derives "every package under `./tenancy/`
  other than the core must appear in the adapter table", and nothing derives "every top-level
  package of the root module other than `tenancy` is a base subsystem".
- **Why this severity:** Both holes reproduced against a copy of the tree.
  (a) Adding `tenancy/tenancyaudit` that imports `cache`, `jobs`, `storage`,
  `crud/decorators/security` **and two sibling adapters** (`tenancycache`, `tenancyjobs`) leaves
  `go test ./scripts/` green — `ok github.com/frostgrove/vv/scripts 2.897s`. D-116:78-83 forbids
  a package with that exact name in as many words, and D-116:84-85 forbids the sibling imports;
  the test D-116:107 cites as the enforcement does not see either.
  (b) Adding `eventsource/` (a plausible next subsystem — the event-sourcing roadmap is live)
  that imports `tenancy`, plus `internal/probe` that imports `tenancyrow`, also leaves
  `TestNoBaseSubsystemDependsOnTheOptionalExtension` green. The one-way arrow is enforced only
  for twelve names somebody remembered to type; `internal/`, `cmd/` and anything added later are
  outside it. This repository's own `CLAUDE.md` names this failure mode: "A hand-written list is
  how a module escapes unit, vet, tidy and release at once, and this repository has already been
  bitten by exactly that."
  The two negative controls D-116:108-110 claims **do** work: adding `import _ "cache"` to the
  core reports `the core reaches github.com/frostgrove/vv/cache`, and adding
  `import _ "crud/decorators/security"` to `tenancydb` reports four violations. The law is
  correctly *stated* for the five packages it names; it is not *enforced* beyond them.
- **Why this timing:** The whole architectural claim of this change is "measured rather than
  asserted". A measurement that only measures the rows already known to pass is an assertion
  with a test around it, and the next package added under `tenancy/` is the one that will not be
  measured.
- **Close criteria:**
  - [ ] The adapter set is derived (`go list ./tenancy/...` minus the core) and a package with no
        row in the seam table fails the test rather than being skipped.
  - [ ] The base-subsystem set is derived (top-level packages of the root module minus `tenancy`,
        `cmd`, `scripts`) rather than enumerated.
  - [ ] Both derivations have a control: the `tenancyaudit` and `eventsource` shapes above are
        added, watched to fail, and removed; the report says so.
- **Status:** open

---

### GAP-5 [high][immediate] D-117 — a binding decision — names a file and three symbols that no longer exist, and four test paths that moved

- **Where:** `docs/ai/decisions/D-117-a-verified-scope-is-minted-never-manufactured.md`
  - `:120` — "`tenancy/errors.go` — the refusal vocabulary and `classify`". The function is
    `Classify`, exported (`tenancy/errors.go:47`); that export is one of the security-relevant
    consequences of this split and the decision that governs it does not mention it.
  - `:121-122` — "`tenancy/jobs.go` — `JobContext`, `JobIdentity`, `tokenMAC`". The file does not
    exist; none of the three symbols exists anywhere in the tree
    (`grep -rn "JobContext\|JobIdentity\|tokenMAC" tenancy/` -> no match). The replacements are
    `tenancy/seal.go:Sealer`, `tenancy/tenancyjobs/jobs.go:ContextProvider` and
    `:IdentityRestorer`.
  - `:143` — `tenancy/row_test.go` (now `tenancy/tenancyrow/row_test.go`).
  - `:145-146` — `tenancy/seams_test.go`: no file of that name exists in the tree at all.
  - `:153` — `tenancy/durable_test.go` (now `tenancy/tenancyjobs/durable_test.go`).
  - `:156` — `tenancy/database_test.go` (now `tenancy/tenancydb/database_test.go`).
  - `:113-123` "Where it lives" lists no `seal.go` and no `Sealer`/`Digest` at all.
  Same class, elsewhere: `docs/ai/flows/FL-033-...md:146` cites
  `jobIdentity.RestoreIdentity` (the type is `identityRestorer`,
  `tenancy/tenancyjobs/jobs.go:66`); `docs/roadmaps/2026-09-01-multitenancy-roadmap.md:395`
  and `:509` cite `tenancy/database_test.go` and `tenancy/row_test.go`.
- **What:** A decision doc is binding in this repository and is the first thing the next agent
  reads. Its map of where the invariant is enforced points at a file that was deleted by this
  change.
- **Why this severity:** An agent asked to change the durable path follows D-117 to
  `tenancy/jobs.go`, finds nothing, and either re-derives the design or concludes the invariant
  moved. CLAUDE.md: "A doc that names a symbol which no longer exists has failed at the one job
  it has", and "Treat a stale doc exactly like a failing test." Six stale references in the one
  document that governs the security property this whole split had to preserve.
- **Why this timing:** The rest of this round's findings (GAP-1, GAP-2) are judged against
  D-117's text. Fixing the code without fixing the map leaves the next reviewer measuring
  against a document that describes the pre-split package.
- **Close criteria:**
  - [ ] D-117 "Where it lives" names `tenancy/seal.go` (`Sealer`, `Seal`, `Unseal`),
        `tenancy/scope.go:Digest`, `tenancy/errors.go:Classify` and
        `tenancy/tenancyjobs/jobs.go`, and drops `tenancy/jobs.go`, `JobContext`, `JobIdentity`,
        `tokenMAC`.
  - [ ] Every test path in D-117 "Proven by" resolves; `tenancy/seams_test.go` is replaced with
        the file that actually holds each cited test.
  - [ ] FL-033:146 names `identityRestorer.RestoreIdentity`; the two multitenancy-roadmap paths
        are corrected.
  - [ ] D-117 states what `Seal` checks and what it does not (see GAP-1).
- **Status:** open

---

### GAP-6 [medium][immediate] The doc-citation checker is structurally blind to a citation whose *file* moved, which is the only kind this change produced

- **Where:** `scripts/docs_test.go:620` (`if match == nil || match[2] == "" { continue }` — a
  citation with no `:Symbol` suffix is dropped) and `:552`
  (`if len(files) == 0 { continue }` — a citation whose file is not in the tree is dropped, and
  is not even counted in `checked`).
- **What:** `TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs` can only report a symbol
  that moved *within a file that still exists*. A bare `` `tenancy/jobs.go` `` and a
  `` `tenancy/seams_test.go` `` are both invisible. That is exactly why every item in GAP-5
  survived a green `go test ./scripts/`.
- **Why this severity:** Not wrong behaviour in the library, but the repository's stated defence
  against doc drift has a hole aligned with the most common kind of refactor — moving a file. A
  restructure is precisely the change this check is supposed to catch and precisely the one it
  cannot.
- **Why this timing:** Without it, GAP-5 is closed by hand and reopens on the next move.
- **Close criteria:**
  - [ ] A citation of the form `` `path/to/file.go` `` (no symbol) that names no file in the tree
        is reported, with an explicit exemption list for paths that belong to a reader's own
        project (the `vv_wire_gen.go` case the fixture already models).
  - [ ] The fixture in `symbolFixture` gains a moved-file case and the test asserts it is
        reported.
- **Status:** open

---

### GAP-7 [medium][immediate] `grantBinding` does not count its variable-length sequences, so two different grants share one binding

- **Where:** `tenancy/grant.go:185-204` (`grantBinding`: classes then cohort, each
  `writeField`ed, neither sequence counted) against its own comment at `:171-173` ("Every member
  and the deadline are inside the MAC") and against `tenancy/seal.go:83-90`, which counts its
  sequence and whose comment (`:34-40`) names this exact hazard: "a token whose fields can be
  slid past one another is a token that authenticates a different record than it travelled with".
- **What:** The class names (`"read"`, `"write"`, `"durable"`) and the cohort references are
  written into one MAC as an uncounted, undelimited run of length-prefixed fields, and a
  `Reference` may legally be the string `"read"`, `"write"` or `"durable"`
  (`tenancy/reference.go:40-53` refuses only control characters, whitespace and >128 bytes).
- **Why this severity:** Reproduced. `grantBinding(salt, "invoices", purpose,
  cohort=["write","acme"], classes={ClassRead}, until)` and `grantBinding(salt, "invoices",
  purpose, cohort=["acme"], classes={ClassRead, ClassWrite}, until)` produce the **same** 32-byte
  binding (`237919c23eff048b...` for both). Not currently reachable from outside the package —
  `Grant`'s fields are all unexported and only `Authority.Accept` constructs one, so an attacker
  cannot present the second grant — which is why this is `medium` and not `high`. But the
  binding does not prove what `holds` is written to assume, `TestEveryPartOfAGrantIsInsideItsBinding`
  (`tenancy/grant_binding_test.go:40`) does not cover framing, and the core now has three MAC
  constructions (`bind`, `grantBinding`, `Sealer.mac`) with three different framing conventions
  sharing only `writeField`.
- **Why this timing:** It is a security primitive in the core, one `binary.BigEndian.PutUint64`
  to fix, and it is the same defect the new seal was explicitly designed to avoid — leaving the
  two inconsistent is how the next MAC gets written the old way.
- **Close criteria:**
  - [ ] `grantBinding` counts the class sequence and the cohort sequence, as `Sealer.mac` counts
        its binding fields.
  - [ ] A test in `tenancy/grant_binding_test.go` asserts the two grants above bind differently
        (in-package, since it needs `grantBinding` directly).
  - [ ] The three MAC framings share one documented convention, or one helper.
- **Status:** open

---

### GAP-8 [medium][immediate] The module docs document none of the six symbols the split had to export

- **Where:** `docs/modules/en/tenancy.md` (359 lines) and `docs/modules/ru/tenancy.md` (364
  lines): `grep -n "Classify\|Digest\|Sealer\|Seal(\|Unseal"` returns **nothing** in either.
  The symbols are in `docs/api/surface.md:1757-1793`.
- **What:** `Scope.Digest`, `Classify`, `Authority.Sealer`, `Sealer`, `Sealer.Seal` and
  `Sealer.Unseal` are new public surface, exported specifically because the adapters now live
  outside the package. The module doc mentions "the durable seal" in prose
  (`docs/modules/en/tenancy.md:30-31`) and never names the type, the accessor or the error a
  missing key produces (`ErrUntrusted` from `tenancy/seal.go:29`, which reads as
  "scope was not produced by this authority" for what is actually "you configured no
  `DurableKey`").
- **Why this severity:** CLAUDE.md: "Added a public API -> the flow that exercises it, the use
  case it serves, `docs/modules/<package>.md`". A consumer writing a second durable seam cannot
  discover `Authority.Sealer` from the consumer reference, and the one error it returns is
  misleading enough to be worth naming there.
- **Why this timing:** The surface is new; documenting it now is the cheapest it will ever be,
  and the entry point for anyone writing the outbox seam.
- **Close criteria:**
  - [ ] Both module docs document `Authority.Sealer`, `Sealer.Seal`, `Sealer.Unseal`, what is
        bound into the MAC and what the caller must pass at both ends.
  - [ ] Both document `Scope.Digest` (what it identifies, that it authorises nothing) and
        `Classify` (what a seam must run application-supplied errors through).
  - [ ] The "no durable key" refusal is named with its sentinel, or the sentinel is changed to
        one that reads as a misconfiguration.
- **Status:** open

---

### GAP-9 [medium][immediate] `MaxPrefixBytes` and `digestBytes` silently encode a bound that lives as an unexported literal in `storage`

- **Where:** `tenancy/tenancystorage/storage.go:12-15`
  (`MaxPrefixBytes = 30`, `digestBytes = 16`) and `:50`
  (`prefix + "-" + hex.EncodeToString(digest[:digestBytes])`), against
  `storage/validate.go:12` (`len(s) == 0 || len(s) > 63`).
- **What:** `30 + 1 + 2*16 = 63` exactly. The two literals are a derivation of `storage`'s
  namespace bound, which `storage` does not export and neither package names. Verified: a prefix
  of exactly `MaxPrefixBytes` produces a 63-character namespace — the derivation is saturated,
  with zero slack.
- **Why this severity:** If `storage` lowers its bound, or `digestBytes` grows to 20 for
  collision headroom, every deployment with a prefix longer than the new slack starts getting
  `storage parse namespace: invalid` at runtime — and the suite stays green, because the only
  test at that edge (`tenancy/tenancystorage/storage_test.go:87`) checks that
  `MaxPrefixBytes+1` is *refused*, which `namespaceOf`'s own guard does regardless of what
  `storage` thinks. A magic threshold justified by nothing visible, on a durable address.
- **Why this timing:** `MaxPrefixBytes` is exported, so it is already a published contract a
  consumer sizes its prefixes against.
- **Close criteria:**
  - [ ] `storage` exports its namespace bound and `tenancystorage` derives `MaxPrefixBytes` from
        it (`storage.MaxNamespaceBytes - 1 - 2*digestBytes`), or the relationship is stated in a
        comment and pinned.
  - [ ] A test builds a namespace at exactly `MaxPrefixBytes` and asserts it parses — the
        control that makes the `+1` case mean something.
- **Status:** open

---

### GAP-10 [medium][immediate] `tenancystorage.Namespace` refuses a non-portable prefix outside the tenancy sentinel vocabulary

- **Where:** `tenancy/tenancystorage/storage.go:46-50` — the length rule refuses with
  `tenancy.ErrMalformed`; the character rule is delegated to `storage.ParseNamespace` at `:50`
  and comes back as a `*storage.Error`. Compare `tenancy/tenancydb/database.go:188,198`, where
  every error crossing the seam goes through `tenancy.Classify`.
- **What:** Reproduced: `namespaceOf("Invoices", scope)` returns
  `storage parse namespace: invalid`, for which `errors.Is(err, tenancy.ErrMalformed)` is
  **false** and `tenancy.OutcomeFor(err)` is `OutcomeError` rather than one of the eleven
  refusals. `docs/modules/en/tenancy.md:317-318` says "Every refusal is a sentinel compared with
  `errors.Is`"; D-117:53-57 says "A refusal names a kind and nothing else. The ten sentinels in
  `tenancy/errors.go` are the vocabulary."
- **Why this severity:** A caller that branches on `errors.Is(err, tenancy.ErrMalformed)` to say
  "your prefix is wrong" gets the right answer for a 31-byte prefix and the wrong answer for
  `"Invoices"` — two rules about one concept, answered in two vocabularies. No tenant leaks
  (`storage.Error` quotes no value — checked), so this is a contract inconsistency rather than a
  disclosure.
- **Why this timing:** It is the seam's public error contract, and `Classify` was exported in
  this very change for exactly this job.
- **Close criteria:**
  - [ ] `namespaceOf` validates the prefix once, in one vocabulary, or runs the `storage` error
        through `tenancy.Classify`/maps it to `ErrMalformed`.
  - [ ] A test asserts a non-portable prefix answers the same sentinel as an over-long one.
- **Status:** open

---

### GAP-11 [medium][immediate] The rule the roadmap was amended to state does not justify `tenancydb`, and two of its own statements are now contradicted

- **Where:** `docs/roadmaps/2026-09-01-extension-architecture-roadmap.md:290-299` (the
  "Narrowed by [[D-116]]" amendment) and `:323` (the verification matrix, unamended);
  `docs/roadmaps/2026-09-01-multitenancy-roadmap.md:338` and `:354`.
- **What:** three separate items.
  1. The amendment says "A subpackage is also added **when the seams an extension adapts do not
     cost the same graph**. That is a measurement and not a preference." Measured:
     `deps(./tenancy)` = {`utils`, `crud`, itself} = 3 and `deps(./tenancy/tenancydb)` = 4, the
     fourth being `tenancydb` itself. `tenancydb` costs **zero** first-party packages beyond the
     core, so by the amended rule it should be files in the core, not a package. D-116:50-53
     gives a different (and better) reason — the topologies are alternatives, and the core holds
     no lifecycle-owning object while `Directory` holds a mutex, a source cache and a `Close` —
     but that reason is nowhere in the rule. The next person applying the written rule gets a
     different answer for four of the five packages than the tree has.
  2. `:323` "Package growth | No pairwise, **nested** or combined extension package/module
     appears in discovered modules" — unamended, and now describes the five new packages.
  3. `multitenancy-roadmap.md:354` (exit evidence) "row and database topologies fit **one
     package** with no mode-dependent global" — contradicted by the tree, not amended.
     `:338` cites `go list -deps ./tenancy/` as the no-external-dependency evidence; after the
     split that command covers the core only. (`./tenancy/...` does have zero external
     dependencies — verified — so the claim holds; the command quoted no longer proves it.)
- **Why this severity:** The written criterion is not the applied criterion, and two roadmap
  statements now assert the opposite of the code. CLAUDE.md requires the roadmap and the
  decision to be updated in the same change as the code.
- **Why this timing:** The next extension (audit, event sourcing, i18n — all live roadmaps) will
  be laid out by reading this rule.
- **Close criteria:**
  - [ ] The amended rule states the second criterion D-116 actually used (an adapter that owns a
        lifecycle or mutable state is its own package even when its graph equals the core's), or
        `tenancydb` folds into the core.
  - [ ] `:323` is amended to distinguish a nested *seam adapter* from a nested *combination*
        package, or drops "nested".
  - [ ] `multitenancy-roadmap.md:354` is rewritten to describe the delivered layout; `:338`
        quotes `./tenancy/...`.
- **Status:** open

---

### GAP-12 [medium][immediate] `Ownership` is a public extension point whose contract is written on a private helper and whose failure mode is fail-open

- **Where:** `tenancy/tenancyrow/row.go:21-27` (the interface, no doc comment) and `:240-258`
  (`relationScopes`, where the contract actually is);
  `docs/api/surface.md:1826-1830` publishes it as an interface a consumer can implement.
- **What:** Four of the five methods carry an invariant the gate cannot check.
  `NarrowsRelations() == false` means "supply no relation-scope function", and
  `security.Gate`'s guard against a policy that thinks it narrowed a preload is then not armed —
  which is correct for `Through`/`Column`-without-relations and silently wrong for a third-party
  strategy that narrows relations in `Relations` but returns `false`. `Frozen()` returning `nil`
  silently disables immutability. Neither is refused; the reasoning that makes this safe lives
  in a comment on the unexported `relationScopes`, not on the exported interface.
  Per `microkernel.md`, an extension point needs a contract, a registration mechanism, a failure
  policy and a compatibility note; this one has a registration mechanism (`Policy`/`Repository`
  take it) and a working contract for the two built-ins, and no written failure policy.
- **Why this severity:** A consumer-written `Ownership` that gets `NarrowsRelations` wrong
  produces a cross-tenant preload read reachable from unprivileged input — the precise failure
  [[D-007]] exists for — and nothing in the type says so. The two shipped implementations are
  correct, so this is a contract gap and not a live bug.
- **Why this timing:** The interface is newly public in a newly public package; adding the
  contract to it now costs a comment, later it costs a breaking change.
- **Close criteria:**
  - [ ] `Ownership` carries the contract each method must satisfy, including that
        `NarrowsRelations` must be true whenever `Relations` returns a non-nil scope set, and
        what happens when it is not.
  - [ ] A contract test (one suite, both built-ins plus a deliberately dishonest fake) pins the
        stated failure policy.
- **Status:** open

---

### GAP-13 [low][immediate] Source comments cite invariant identifiers that exist only under `.agents/`

- **Where:** `tenancy/grant.go:78` ("the whole point of INV-2"),
  `tenancy/tenancystorage/storage.go:38` ("INV-25's redaction"),
  `tenancy/scope_test.go:282` ("INV-3"), `tenancy/tenancyrow/contract_test.go:57`
  ("INV-31 / UC-7").
- **What:** `grep -rn "INV-2[0-9]" docs/` returns nothing. The `INV-*` and `UC-7` vocabulary
  lives in `.agents/artifacts/usecases/TENANCY_USECASES.md`, a working artifact outside the
  documented tree. `docs/ai/usecases/` numbers its use cases `UC-004`, `UC-028`; there is no
  `UC-7`.
- **Why this severity:** Cosmetic on its own, but it is how GAP-3 happened: a comment justified
  itself against an invariant nobody following the repository's own lookup order can read, and
  the invariant turned out to say the opposite.
- **Why this timing:** These comments are the justification for two security-relevant design
  choices; a reference a reader cannot resolve is a justification they cannot check.
- **Close criteria:**
  - [ ] Each `INV-*`/`UC-7` reference in `tenancy/**` either resolves to a `docs/` identifier
        (`[[D-117]]`, `[[UC-028]]`, `[[FL-033]]`) or states the invariant inline.
- **Status:** open

---

### GAP-14 [low][deferred] Counted threshold breaches, and one shipped example that promises more than it wires

- **Where:**
  - `tenancy/tenancystorage/storage.go:28` — `Store(ctx, authority, prefix, class, backend)`:
    **5** parameters against the `architecture.md` threshold of 4.
  - `tenancy/tenancydb/database.go:36-48` — `Directory`: **10** fields against a threshold of 7
    (seven configuration + `mutex`, `entries`, `closed`; the three are the cache it is).
  - `tenancy/tenancyrow/row.go:14-19`, `:50`, `:112` — `Mode` (`Derive`/`Validate`) is a flag
    parameter that changes what `Column`'s `Apply` means, which `architecture.md` lists under
    hard bans. It predates the split; the package boundary around it does not.
  - `tenancy/tenancyrow/row.go:217` panics on a nil authority while `tenancystorage`,
    `tenancycache`, `tenancyjobs` and `tenancydb` all return an error — four adapters refuse,
    one panics.
  - `_examples/tenancy-sharedrow/main.go:1-3` — the header promises "a narrowed statement, a
    namespaced bucket, **a partitioned cache and a durable job**"; the file imports neither
    `tenancycache` nor `tenancyjobs` and wires neither.
- **What / why this severity:** None of these changes behaviour. They are counted because the
  brief asks for numbers, and because the example header is the one line a consumer reads before
  copying the wiring.
- **Why this timing:** Deferred — module-internal shape and one doc line; no external contract
  depends on any of them being fixed before the next section.
- **Close criteria:**
  - [ ] `Store` takes a spec, or the breach is justified in writing.
  - [ ] `Column`'s two modes become two named constructors, or the flag is justified in writing.
  - [ ] The nil-authority policy is the same across all five adapters, or the difference is
        stated where `Policy` panics.
  - [ ] The example header describes what the example wires, or the example wires what it
        promises.
- **Status:** open

---

## What was checked and found clean

- **Microkernel purity, both directions.** `grep -E "vv/(crud/decorators|jobs|storage|cache)|vv/tenancy/tenancy" tenancy/*.go`
  -> 0 matches. `grep -E "tenancyrow|tenancydb|tenancyjobs|tenancystorage|tenancycache" tenancy/*.go`
  -> 0 matches. The core branches on no adapter and names none. Adapter -> adapter imports: 0.
  The core builds and its whole test suite passes with zero adapters compiled
  (`go test ./tenancy/` alone: `ok`). Adding a sixth adapter of the same kind costs **0** lines of
  kernel diff — `Sealer.Seal`'s variadic binding vector, `Scope.Digest` and `Classify` are exactly
  the three generic hooks a second seam needs (which is also why GAP-1 and GAP-2 matter: the
  next seam inherits both).
- **Scope unforgeability survived the split.** `Scope` has only unexported fields, no exported
  constructor, and `MarshalJSON` refuses (`tenancy/scope.go:46-48`). `Digest` requires a `Scope`
  and returns a value that authorises nothing; `Classify` maps errors and constructs nothing;
  `Sealer` refuses a zero value (`tenancy/seal.go:42,62`), refuses an authority with no durable
  key (`:28-30`), and `Seal` refuses a scope another authority minted (`:45`, pinned by
  `TestOnlyAScopeThisAuthorityMintedIsSealed`). None of the six new exports is a way to forge,
  launder or widen a scope. `Unseal` returns a reference and a generation, never a `Scope`
  (`:61`), and the restorer re-asks the control plane (`tenancy/tenancyjobs/jobs.go:88-94`).
- **The generalised seal is stronger than the jobs-specific token it replaced**, on the axis it
  was generalised for: the binding field count is inside the MAC and every field is
  length-prefixed (`tenancy/seal.go:83-94`), so a two-field record cannot be replayed as a
  one-field one and a boundary cannot be moved —
  `TestBindingFieldsCannotBeSlidPastOneAnother` walks five variants including "an empty field
  appended". It is neither weaker nor stronger on the deployment axis (GAP-2), because neither
  version bound the origin.
- **Two adapters cannot disagree about a derived value.** `tenancystorage` and `tenancycache`
  both read `Scope.Digest()` (`storage.go:49`, `cache.go:51`) — the export exists precisely to
  close that, `TestTheDigestSeparatesTenantsAndGenerations` pins it in the core, and both
  seams' restored-generation tests read from it. The two truncation widths (16 bytes fixed vs
  16..32 by budget) are prefixes of one digest, so a restore fences both.
- **Naming.** `tenancyrow`/`tenancydb`/`tenancyjobs`/`tenancystorage`/`tenancycache` do **not**
  contradict [[D-114]]:12, which bans a package whose identity is the intersection of two
  *extensions* (`tenancyotel`, `jobstenancy`). These are extension x base seam, which D-116:78-83
  distinguishes explicitly. The `tenancy` prefix is load-bearing for three of the five —
  `package storage`, `package cache` and `package jobs` would each collide with the base package
  the adapter imports — and consistent for the other two.
- **The two negative controls D-116 claims for the layout law both fire** (verified against a
  copy of the tree, not read): `import _ "cache"` in the core is reported at
  `tenancy_test.go:82`; `import _ "crud/decorators/security"` in `tenancydb` is reported four
  times at `:73`. The law is correctly stated for the five rows it has; GAP-4 is about the rows
  it does not have.
- **Reverse index and surface baseline are current.** `docs/ai/flows/Index.md:237-250` lists all
  nine core files and all five adapter files against FL-033. `docs/api/surface.md:1757-1841`
  carries all six packages. `scripts/checks.sh:18` adds `tenancy` to `SUBSYSTEMS`.
  `./tenancy/...` has **0** external dependencies.
- **Concurrency in the moved code.** `Directory` takes its slot before opening
  (`database.go:159-177`), detaches the opener from one borrower's cancellation
  (`:183-184`), unlinks on eviction but closes only at the last release (`:228-235`, `:256-262`),
  and `Lease.Release` is `sync.Once` (`:73-78`). `go test -race ./tenancy/...` green.
  The split moved this whole mechanism into its own package without changing it.

---

## Round 2 — dispositions — 2026-09-06

Every `[high][immediate]` and every `[medium][immediate]` is closed. Each fix is
named with the mutation that kills it; each mutation was run against the fixed
tree and the tree restored afterwards.

| Finding | Disposition |
|---|---|
| GAP-1 `Seal` checks no class | **closed** — `Seal` now calls `accept(scope, ClassDurable)`. `TestOnlyAScopeTheDurableClassAdmitsIsSealed` (`tenancy/seal_test.go`) seals from a lifecycle the durable class does not admit and expects `ErrInactive`, with the admitted state as the control. Mutation `accept → minted`: killed. |
| GAP-2 the seal MAC omits `Origin` | **closed** — `Sealer.mac` writes the origin first, as `bind` does. `TestARecordSealedInAnotherDeploymentIsNotRead` shares one `DurableKey` across two origins. Mutation, origin removed: killed. |
| GAP-3 the namespace claims a redaction it does not have | **closed** — the comment now says what is true: the digest keeps a customer's *name* out of a path that is quoted in logs, and is fixed width so no namespace is a prefix of another. It also says outright that it is an address and not an anonymisation, and must not travel into telemetry. |
| GAP-4 the layout law is a hand-written table | **closed** — both laws enumerate. `TestNoBaseSubsystemDependsOnTheOptionalExtension` walks every package of the root module (direct imports; every package is enumerated, so a transitive edge is a direct edge somewhere along it) and `TestNoTenancyPackageCostsMoreThanTheSeamItNames` walks `./tenancy/...` and fails a package that names no seam. Both reproductions from Round 1 now fail: a `tenancyaudit` importing two siblings, and a new top-level `eventsource/` importing the core. |
| GAP-5 D-117 cites a deleted file and moved tests | **closed** — D-117's *Where it lives* and *Proven by* rewritten; `FL-033` and two roadmap paths corrected. |
| GAP-6 the doc checker is blind to a moved file | **closed** — `scripts/docs_test.go` now collects a citation with no `:Symbol` too, and a **directory-qualified** path that resolves to no file in this tree is stale. A bare basename still may be the reader's own project, and five paths that name another tree are listed with their reason. Two pre-existing stale citations found and fixed (`cache/scope.go`, in FL-025 and the reverse index). Mutation: D-117 citing `tenancy/jobs.go` — killed, where it survived before. |
| GAP-7 `grantBinding` does not count its runs | **closed** — both runs are counted. `TestAClassCannotBeSpelledAsACohortMember` builds the collision the finding describes ("write" as a tenant). Mutation, counts removed: killed. |
| GAP-8 the module docs name none of the new symbols | **closed** — *What the core lends a seam* in both languages: `Classify`, `Scope.Digest`, `Authority.Sealer`, `Seal`, `Unseal`, with what each refuses. |
| GAP-9 / GAP-10 the namespace bounds and vocabulary | **closed** — the duplicated length check is gone: `storage` owns what a namespace may be, its refusal is answered as `ErrMalformed`, and `MaxPrefixBytes` is now pinned from both sides by `TestAPrefixIsRefusedInThisPackagesVocabularyOrLeavesRoomForTheDigest`. Mutations — 31, 29, a 4-byte digest, the store's own error returned raw: all killed. |
| GAP-11 the amended rule does not justify `tenancydb` | **closed** — the rule now has two criteria and says so in both the extension-architecture roadmap and D-116: the graphs differ, **or** the package names an alternative the deployment chooses or an object that owns a lifecycle the core does not. `:323` distinguishes a nested seam adapter from a combination package; the multitenancy roadmap's exit evidence and its `go list` command are corrected. |
| GAP-12 `Ownership` has no written contract | **closed** — the interface carries the obligation of each method, including that answering `NarrowsRelations() == false` while `Relations` narrows is a cross-tenant read nothing below will catch. A subtest wires a deliberately dishonest strategy and pins the documented consequence. |
| GAP-13 comments cite invariants outside `docs/` | **closed** — the four `INV-*`/`UC-7` references state the invariant inline. |
| GAP-14 threshold breaches, and the example header | **partly closed** — the example header no longer promises a cache and a job it does not wire. The counted breaches (a 5-parameter `Store`, a 10-field `Directory`, `Mode` as a flag) stand and are recorded in the plan's Debt with their justification. |
