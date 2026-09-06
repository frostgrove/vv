# tenancy/ + _examples/tenancy-sharedrow — Universality (dimension 4) — AUDIT (2026-09-06)

Scope: `tenancy/` (all six packages) and `_examples/tenancy-sharedrow/main.go`.
Law: `~/.claude/skills/econv/references/universality.md` — no hardcode, no fitting
to one example. The question answered here is: **would this behave correctly on an
input nobody has ever shown the code?**

Every claim below was produced by a Go program under
`/tmp/claude-1000/-home-user-ws-gd-lease/82a92a15-ac6c-4104-9609-680bbbcbfeb1/scratchpad/probe/`
(`ref/ own/ sqlp/ down/ life/ plane/ part/ hostile/`), run against a
**verified-pristine frozen copy** of the tree — see *Measurement hazard* below.

---

## Measurement hazard (read first)

During this audit another agent was **mutation-testing the same working tree**.
Files under `tenancy/` changed under me three times mid-run: `tenancyrow/row.go`
(`Narrow` returning a constant `crud.Eq(name, "globex-1a2b")`), `tenancy/grant.go`
(`_ = cohort` in `grantBinding`), `tenancy/reference.go` (`if false && (...)` in
`valid()`), `tenancydb/database.go` (`release` losing its close), and
`tenancystorage/storage.go` (prefix bound dropped + zero digest).

`tenancy/` is **untracked** in git (`git status --porcelain -- tenancy/` → `?? tenancy/`),
so there is no committed baseline to diff against. I therefore built one:
20 snapshots of the tree taken 2s apart, per-file modal content, then
`go test -count=1 -overlay=… ./tenancy/...` → all six packages green.
Files that varied during sampling: `lifecycle.go` (18/20 modal),
`tenancydb/database.go` (19/20), `tenancyrow/row.go` (18/20).
**Every probe result in this document was re-run against that pristine tree and
reproduces identically.** Line numbers cite the pristine content, which matches
the live files at the time of writing.

---

## Map (universality-relevant only)

| Where a value from outside enters | What the code does with it | Structural risk |
|---|---|---|
| `Reference` — `reference.go:15-53` | opaque string, 1..128 bytes, byte-for-byte equality, no folding | validator has an opinion but only an ASCII-shaped one |
| `Epoch` — `reference.go:55-66` | `uint64`, non-zero, compared with `!=` only | 64-bit wire format in `Digest`/`seal` |
| `Lifecycle` — `lifecycle.go:5-36` | closed set of 7, `Valid()` = `Provisioning..Deleted` | no parse, no extension point |
| `Class` — `lifecycle.go:38-61` | closed set of 3 | `classOf` (`tenancyrow/row.go:260-265`) collapses 4 crud actions onto 1 |
| `Admission` — `lifecycle.go:63-98` | `[3]uint8` bitset, `1 << state` | 8 bits for 7 states; unknown input dropped silently |
| `Value func(Reference) (any, error)` — `tenancyrow/row.go:29-36` | 5 call sites (`row.go:73,84,100,152,160`), 4 of them feed `crud.Eq` unchecked (`row.go:77,90,156,164`), 1 feeds `assign` | the single deployment-supplied seam, and it is unvalidated in both directions |
| storage prefix — `tenancystorage/storage.go:42-51` | length-checked here, character-checked by `storage.ParseNamespace` | two owners for one rule |
| cache partition — `tenancycache/cache.go:43-54` | `digest[:min(budget, 32)]` | length depends on the key beside it |
| jobs partition — `tenancyjobs/jobs.go:40` | raw reference → `jobs.ParsePartition` → digested by `jobs` | safe |

Composition roots that exercise any of it: `_examples/tenancy-sharedrow/main.go`
(165 lines, build-only, no database) and `test/integration/tenancy_test.go`
(2 test functions).

---

## Scorecard

| Law aspect | Verdict | Evidence |
|---|---|---|
| Domain literals in logic | **pass** | `grep -rnoE '"[^"]{4,}"' tenancy/ \| grep -v _test` → only import paths, `String()` vocabulary, redaction strings, error prose. Zero sample-derived strings. |
| Regexes tuned to one layout | **pass** | no `regexp` import anywhere in `tenancy/` |
| Fixture/sample paths in source | **pass** | `grep -rn "_examples\|testdata\|fixture\|/test/" tenancy/ --include='*.go' \| grep -v _test.go` → empty |
| Branching on a sampled value | **pass** | `grep -rnE '== *"' tenancy/ \| grep -v _test` → only `== ""` emptiness guards |
| Injection surface (SQL/path/namespace) | **pass** | every reference binds as a parameter; every namespace/partition is a digest — proven, see *Clean results* |
| **Shape assumptions from the sample** | **fail** | GAP-1, GAP-2, GAP-15 — the ownership `Value` seam only works for the two shapes the example and the integration test happen to use |
| **Closed vocabularies fitted to one deployment** | **fail** | GAP-4, GAP-5 — 7 lifecycle states, 3 classes, no extension point, no parse |
| **Type over-fitted to one control plane** | **fail** | GAP-6 — `Epoch uint64` |
| **Magic thresholds, no derivation/config/calibration** | **fail** | GAP-10 — 6 of 9 constants are picked numbers |
| **Behaviour undefined for a neighbouring input** | **fail** | GAP-3, GAP-7, GAP-8, GAP-12 — misconfiguration, a canonicalising control plane, a Unicode reference, a long cache key |
| Delete-the-example test | **pass for `src`, fail for the key** | nothing in `tenancy/` names the example; but GAP-9 — the example's placeholder secret is accepted by `tenancy.New` |

---

## Metrics

| Metric | Threshold | Actual | Worst offenders |
|---|---|---|---|
| Domain/sample string literals in logic | 0 | **0** | — |
| Regexes | 0 | **0** | — |
| Fixture paths referenced from source | 0 | **0** | — |
| Magic numbers with no derivation, no config entry, no calibration note | 0 | **6** | `MaxCohortSize=10000` (grant.go:18), `MaxPurposeBytes=64` (grant.go:17), `MaxReferenceBytes=128` (reference.go:11), `digestBytes=16` (tenancystorage/storage.go:14), `MinPartitionBytes=16` (tenancycache/cache.go:11), `DefaultOpenTimeout=30s` (tenancydb/database.go:14) |
| Derived constants whose derivation is written down | all | **0 of 1** | `MaxPrefixBytes=30` (= 63 − 1 − 2·`digestBytes`); nothing says so |
| Legitimately protocol-fixed | — | **3** | `MinDurableKeyBytes=32`, `MaxPartitionBytes=32`, `[32]byte` bindings — SHA-256 widths |
| Closed enums with no parse and no extension point | 0 | **2** | `Lifecycle` (7 values), `Class` (3 values) |
| Bits available in the lifecycle bitset vs states used | headroom | **8 vs 7** | `Admission.states [3]uint8`, `1 << state`, lifecycle.go:63,72,95 |
| Deployment-supplied `Value` results validated before use | 5 of 5 | **0 of 5** | row.go:73, 84, 100, 152, 160 |
| Type-safe paths through `assign` | all | **converts, not checks** | row.go:123 uses `ConvertibleTo` |
| Distinct ownership shapes covered by any test | ≥3 | **2** | `string→string` (row_test.go:18), `"1"/"2" →int64` (test/integration/tenancy_test.go:36) |
| `Through` exercised with a non-default `Value` | ≥1 | **0** | only row_test.go:278, with `nil` |
| Files > 400 lines (non-test) | 0 | **0** | largest: tenancydb/database.go (284) |
| `go vet ./tenancy/...` | clean | **clean** | — |
| `go test ./tenancy/...` on the pristine tree | green | **green** | 6/6 packages |

---

## Findings

### GAP-1 [critical][immediate] `assign` converts instead of checking, so an ownership value of the wrong type is silently reinterpreted and the row is written to a tenant that does not exist

- **Where:** `tenancy/tenancyrow/row.go:118-131` (`assign`), reached from `row.go:99-116`
  (`column.Apply`) through `row.go:229-235` (`Policy.Inspect`).
- **Scale:** local (one function) — but it is the only write path of every `Column`
  ownership, i.e. every tenant-owned create in the shared-row topology.
- **Confidence:** CONFIRMED.
  `go run -overlay=<pristine> ./own` →
  ```
  string column, Value returns int64(42)      Apply -> WROTE  "*"
  uint8  column, Value returns int64(300)     Apply -> WROTE  0x2c
  float64 column, Value returns int64(2^53+1) Apply -> WROTE  "9007199254740992"
  ```
  `go run -overlay=<pristine> ./sqlp` — the same gate, end to end through `crudtest.Postgres()`:
  ```
  INSERT  INSERT INTO "invoices" ("tenant_id", "number") VALUES ($1, $2)
          ARGS []interface {}{"*", "INV-9"}
  SELECT  SELECT "id", "tenant_id", "number" FROM "invoices" WHERE "tenant_id" = $1
          ARGS []interface {}{42}
  ```
- **What / Why this severity:** `assign` asks `incoming.Type().ConvertibleTo(target.Type())`
  and then calls `Convert`. Go's conversion rules make **integer → string** legal (it
  yields the rune), make **any integer → any narrower integer** legal (it truncates),
  and make **int64 → float64** legal (it rounds). None of those is a tenant mapping.
  Concretely, with the tenant column declared `TenantID string` and a deployment
  `Value` that returns `int64` — the exact shape `docs/modules/en/tenancy.md:164-171`
  tells a consumer to write, only with the column and the mapping crossed — the gate
  **writes `tenant_id = '*'`** and then **reads `WHERE tenant_id = 42`**. The row is
  invisible to the tenant that created it, forever, with no error anywhere. Worse, the
  conversion is a code-point map: a numeric tenant id of `65` writes `tenant_id = 'A'`,
  so any tenant whose reference literally is `"A"` owns and reads that row — a
  cross-tenant leak reachable from ordinary business input.
  The `uint8` case is a bare truncation: tenant `300` is stored as `44`, and tenant
  `44` reads it. The `float64` case collides `2^53+1` with `2^53`.
- **Why this timing:** it is the contract of the one seam a deployment must implement.
  Every consumer that is not string-into-string is exposed, and any later work on
  `Ownership` will have to be redone around whatever check is added. Under
  `universality.md` a hardcode/fit finding is never deferred.
- **Close criteria:**
  - [ ] `assign` refuses unless the incoming type is identical to, or assignable to,
        the column type; a named type over the same underlying kind is the only
        conversion permitted, and no string↔numeric or narrowing-integer conversion is.
  - [ ] the refusal is a typed configuration error naming both types, raised the first
        time the strategy runs, not a silent write.
  - [ ] a test per column kind (`string`, `int64`, `uint8`, `float64`, `[16]byte`,
        `Opt[T]`) asserting that a mismatched `Value` refuses rather than converts.
  - [ ] a test asserting the value the gate writes and the value the gate narrows on
        are the same value.

### GAP-2 [critical][immediate] A nil ownership value becomes `tenant_id IS NULL` on the read path and is accepted as ownership on the write path

- **Where:** `tenancy/tenancyrow/row.go:72-78` (`column.Narrow`), `:80-93`
  (`column.Relations`), `:151-158` and `:159-165` (`through`), `:100-116`
  (`column.Apply`'s equality shortcut) — 4 unchecked `crud.Eq` call sites at
  `row.go:77, 90, 156, 164`. The conversion happens in `crud/predicate.go:396-410`.
  The gate's guard that should have caught it is `crud/decorators/security/security.go:141`.
- **Scale:** systemic — 4 of the 4 predicate-building sites, both strategies.
- **Confidence:** CONFIRMED.
  ```
  Value -> untyped nil          SQL  SELECT ... WHERE "tenant_id" IS NULL   ARGS []interface {}(nil)
  Value -> typed nil (*string)  SQL  SELECT ... WHERE "tenant_id" IS NULL   ARGS []interface {}(nil)
  *int64 column, Value returns typed nil   Apply -> WROTE  (*int64)(nil)
  string column, Value returns (nil, nil)  Apply -> REFUSED  security: forbidden: create: the ownership value is empty
  ```
- **What / Why this severity:** `crud.Eq(field, nil)` is not an error — it is
  `nullNode`, i.e. `IS NULL`. A `Value` that cannot map a reference and returns
  `(nil, nil)` — the natural shape when a directory lookup misses — therefore does not
  refuse the request; it **narrows the whole repository to the rows with no owner**.
  `IS NULL` is not a tautology, so `security.go:141`'s `IsTautologyFor` guard passes it
  through. Every un-owned row in a shared table (legacy rows, rows written before the
  column was backfilled, rows written by GAP-1) becomes readable, updatable and
  deletable by that request.
  The two halves also disagree: `assign` explicitly refuses an untyped nil
  ("the ownership value is empty", `row.go:121`) but `Apply`'s equality shortcut at
  `row.go:109-111` accepts a **typed** nil as "already correctly owned" and lets the
  create through with a NULL owner. So the write path refuses one spelling of nothing
  and accepts the other, while the read path accepts both and turns them into a
  predicate that matches rows.
- **Why this timing:** it is a fail-open in the narrowing predicate, which is the
  single guarantee this subsystem exists to provide (`UC-004`, `INV-2`).
- **Close criteria:**
  - [ ] a `Value` returning nil (typed or untyped) is a refusal at every one of the 5
        call sites, not a predicate and not an accepted ownership.
  - [ ] `Narrow`/`Relations` reject any value that `crud.Eq` would turn into
        `nullNode` or an undefined `cmpNode`, before handing it to `crud`.
  - [ ] a control test asserting the leak *is* there without the check, so the positive
        test cannot pass vacuously (house rule, `CLAUDE.md`).

### GAP-3 [critical][immediate] An admission that names nothing the library recognises is silently replaced by the permissive default

- **Where:** `tenancy/lifecycle.go:65-76` (`Admit` drops an out-of-range class or state
  without a word) and `tenancy/authority.go:41-44` (`New` treats the resulting zero
  `Admission` as "the deployment configured nothing" and substitutes `AdmitAll(Active)`).
- **Scale:** local, one code path — every `tenancy.New` in existence goes through it.
- **Confidence:** CONFIRMED. `go run -overlay=<pristine> ./life`:
  ```
  Admit(ClassRead, Lifecycle(7)) -> IsZero=true Admits=false
  Admit(Class(9), Active).IsZero() = true
  New(Spec{Admission: Admit(ClassRead, Archived)}) err=<nil>
    authority.Admits(ClassRead, Active)   = true  <- never asked for
    authority.Admits(ClassWrite, Active)  = true  <- never asked for
    authority.Admits(ClassDurable, Active)= true  <- never asked for
  ```
- **What / Why this severity:** a deployment that maps its own status vocabulary onto
  `Lifecycle` and gets one constant wrong — an off-by-one, a state this version does
  not have, a `Class` from a newer version — receives **no error at construction**.
  `Admit` returns an empty `Admission`; `New` cannot distinguish "empty because the
  operator wrote nothing" from "empty because everything the operator wrote was
  dropped", so it installs a policy the operator never asked for: reads, writes **and**
  durable work admitted for `Active`. An intended read-only-while-archived policy
  becomes read-write-active. Configuration failing open is the shape `restrictions.md`
  §2 calls `[critical][immediate]`.
- **Why this timing:** it is a public constructor contract; anything built on top has
  to keep bending around the ambiguity between "unset" and "rejected".
- **Close criteria:**
  - [ ] `Admit` returns an error (or panics at wiring, consistent with `Column`'s
        panics) for an unknown class or state instead of dropping it.
  - [ ] `Spec.Admission` distinguishes "not set" from "set and empty"; the second is
        refused by `New`, naming what was dropped.
  - [ ] a test that a mis-specified `Admission` refuses construction instead of
        yielding the default.

### GAP-4 [high][immediate] `Lifecycle` is a closed seven-value set fitted to one deployment's vocabulary, and its bitset has exactly one spare bit

- **Where:** `tenancy/lifecycle.go:5-13` (the constants), `:15-17` (`Valid`),
  `:19-36` (`String`, no inverse), `:63` (`[3]uint8`), `:72` and `:95` (`1 << state`),
  `:109-116` (`inconsistent`, iterating `Provisioning..Deleted`).
- **Scale:** systemic — the set is read at 6 sites and there is no seam anywhere.
- **Confidence:** CONFIRMED.
  ```
  Lifecycle(7).Valid()=false String()="unknown"   ... Lifecycle(200).Valid()=false String()="unknown"
  Admit(ClassRead, Lifecycle(7)) -> IsZero=true
  uint8(1) << 7 = 128 ;  uint8(1) << 8 = 0 ;  uint8(1) << 9 = 0
  Resolve -> Lifecycle(7):   tenancy: scope was not produced by this authority: forbidden  (Outcome untrusted)
  Resolve -> Lifecycle(200): tenancy: scope was not produced by this authority: forbidden  (Outcome untrusted)
  ```
- **What / Why this severity:** three things, one cause.
  (a) A deployment with `Archived`, `Trialing`, `PendingDeletion`, `Dunning` or
  `Frozen` cannot express it. It must fold its state onto one of the six, and the fold
  is lossy in exactly the direction that matters: `Trialing` folded onto `Active` gets
  a trial tenant full write access; folded onto `Suspended` it gets refused reads. The
  library offers no `ParseLifecycle`, no registry, no "unknown but admitted" state —
  adding an eighth state requires editing this file, which under `microkernel.md` means
  the extension point is missing.
  (b) `Admission.states` is `[3]uint8` and `Admit` does `1 << state`. Seven states use
  bits 0..6. An eighth (value 7) still fits; a **ninth silently evaluates to 0**, so
  the state is set nowhere and admitted never, with no compile error and no test that
  would notice. That is a trap laid for the next person who grows the enum.
  (c) A control plane that reports a state this version does not have is refused with
  `ErrUntrusted` — *"scope was not produced by this authority"*, `Outcome untrusted`.
  That is the outcome an operator alerts on for **scope forgery**. Rolling out a new
  tenant status in the control plane therefore pages the security on-call with a
  message that names the wrong cause; and `LifecycleUnknown` (0) produces the same
  answer as `Lifecycle(200)`, so "the control plane returned nothing" and "the control
  plane returned something new" are indistinguishable.
- **Why this timing:** the vocabulary is a public type in a published surface
  (`docs/api/surface.md:1757+`); widening it later is a breaking change, and every
  deployment that folded its states in the meantime has to be re-audited.
- **Close criteria:**
  - [ ] either a justified statement in a decision doc that the six states are the
        complete domain vocabulary and why a seventh cannot exist, or an extension
        mechanism (registered states, or an opaque state + a deployment-supplied
        admission predicate).
  - [ ] a state the library does not recognise produces a distinct refusal and a
        distinct `Outcome`, never `untrusted`.
  - [ ] `Admission` stops being a `uint8` bitset indexed by the enum, or carries a
        compile-time assertion that `Deleted < 8`.
  - [ ] a test that an out-of-range lifecycle from the resolver refuses with that
        distinct outcome.

### GAP-5 [high][immediate] Three classes, and `classOf` collapses four crud actions onto `ClassWrite`

- **Where:** `tenancy/lifecycle.go:38-61` (`ClassRead`/`ClassWrite`/`ClassDurable`),
  `tenancy/tenancyrow/row.go:260-265` (`classOf`: everything that is not
  `crud.ActionRead` is `ClassWrite` — that is `ActionCreate`, `ActionUpdate`,
  `ActionDelete` and `ActionRestore`, per `crud/action.go:6-10`).
- **Scale:** local, but it is the only mapping from an operation to a policy class.
- **Confidence:** CONFIRMED. `go run -overlay=<pristine> ./part`, tenant lifecycle
  `deleting`, `Admit(ClassRead, Active, Deleting).Merge(Admit(ClassWrite, Active, Deleting))`:
  ```
  DELETE while the tenant is 'deleting': affected=1 err=<nil>
  INSERT while the tenant is 'deleting': err=<nil>
    statement: INSERT INTO "invoices" ("tenant_id", "number") VALUES ($1, $2) args=[acme INV-NEW]
  ```
- **What / Why this severity:** the obvious operator policy for a tenant being
  deleted — *the purge job may remove rows, nothing may add them* — **cannot be
  expressed**. Admitting `ClassWrite` for `Deleting` admits `INSERT` with it, so a
  request that arrives during the purge window creates rows in a tenant that is being
  torn down, and the purge either misses them or the delete races the insert.
  Symmetrically for `Provisioning`: "creates only, no deletes" is inexpressible.
  Beyond the row seam, the three classes have no room for the operations a real
  deployment separates: an analytics/replica read (which should be admitted in states
  a primary read is not), a schema migration (which must run precisely while
  `Migrating` refuses everything else), an export or a legal hold (which must survive
  `Deleted`). Each of those has to be smuggled in as one of the three, which means
  admitting the other operations that share the class.
- **Why this timing:** `Class` is in the public surface and is a parameter of
  `Bind`, `Scope`, `Namespace`, `Keyed`, `Borrow` and `Capture`. Adding a fourth value
  later changes every one of those call sites' meaning.
- **Close criteria:**
  - [ ] a written justification that read/write/durable is the complete axis of
        variation, **or** a mechanism for a deployment-named class.
  - [ ] `classOf` either maps each `crud.Action` to a distinct class or the decision to
        merge them is stated in a decision doc with the purge case answered.
  - [ ] a test pinning what a tenant in `Deleting` may and may not do, per action.

### GAP-6 [high][immediate] `Epoch uint64` forces a lossy fold for any control plane whose generation is not a 64-bit counter, and the fold defeats the restore fence

- **Where:** `tenancy/reference.go:55-66` (`Epoch`, `NewEpoch`),
  `tenancy/scope.go:84-92` (`Digest` writes 8 bytes),
  `tenancy/seal.go:10` (`sealGenerationBytes = 8`) and ``:41-59` / `:61-81`
  (the token's generation field is `uint64` big-endian),
  `tenancy/context.go:68-70` and `tenancy/tenancyjobs/jobs.go:92` (staleness is `!=`).
- **Scale:** systemic — the 64-bit width is fixed in three independent wire formats.
- **Confidence:** CONFIRMED. `go run -overlay=<pristine> ./plane`:
  ```
  generation A = 1111111111111111deadbeef00000001 -> Epoch 16045690981097406465
  generation B = 2222222222222222deadbeef00000001 -> Epoch 16045690981097406465
  two distinct generations, one Epoch: true
  a job sealed under generation A, replayed after a restore to generation B:
    unseal err=<nil>  token generation=16045690981097406465
    control plane now says epoch=16045690981097406465 err=<nil>
    stale check (scope.Epoch() != generation) fires: false
  ```
- **What / Why this severity:** nothing in the code or in `docs/modules/en/tenancy.md`
  says what an `Epoch` *is* — a counter, a timestamp, a version. What the code
  requires is only "non-zero and different after a restore", and it compares with `!=`,
  never `>`, so no ordering is assumed (that part is fine and should stay).
  What is not fine is the width. A control plane whose generation is a UUID, a ULID, a
  git-style content hash or a `restored_at` string — all ordinary shapes — has no
  representation and must fold to 64 bits. The probe above folds two UUIDs that share
  their low half and the fence disappears: a durable job token sealed under generation
  A unseals cleanly after the tenant is restored to generation B, `Lookup` returns B,
  and `scope.Epoch() != generation` is false, so `tenancyjobs.RestoreIdentity`
  (`jobs.go:92-94`) admits the work. `Scope.Digest()` (`scope.go:84-92`) then hands
  the restored tenant **the previous generation's object namespace and cache
  partition** — which is the single failure the comment at `scope.go:78-83` says the
  function exists to make impossible.
  The birthday bound makes an accidental collision negligible; the problem is that the
  library dictates the fold and the deployment chooses it in silence, with no way to
  declare "my generation does not fit and I need you to tell me".
- **Why this timing:** the width is in the seal token's on-disk format. Changing it
  later invalidates every enqueued job row.
- **Close criteria:**
  - [ ] `docs/modules/en/tenancy.md` states what a deployment must put in `Epoch`,
        what property it must have, and what happens if two generations collide.
  - [ ] either `Epoch` widens to an opaque byte string with the length-prefixed
        framing the rest of the MACs already use, or `NewEpoch` documents the 64-bit
        contract and the seal refuses a generation the deployment cannot guarantee unique.
  - [ ] a test that two distinct generations folding to one value is either impossible
        or refused, not silently accepted.

### GAP-7 [high][immediate] `Authority.Lookup` demands a byte-identical answer, so a canonicalising control plane fails every grant with `untrusted`

- **Where:** `tenancy/authority.go:90-102` — `if resolution.Reference != reference { return Scope{}, ErrUntrusted }`.
  Reached by `tenancy/grant.go:148-161` (`member`), `tenancy/tenancyjobs/jobs.go:88`
  (`RestoreIdentity`), and every `Each` run.
- **Scale:** systemic — 3 entry points, all of the cross-tenant and durable paths.
- **Confidence:** CONFIRMED. `go run -overlay=<pristine> ./plane` against a resolver
  that lowercases and strips zero-width characters (i.e. any directory backed by a
  `citext` column, an LDAP directory, or a trimming API):
  ```
  Lookup("acme"    ) -> <nil> (Outcome ok)
  Lookup("ACME"    ) -> tenancy: scope was not produced by this authority: forbidden (Outcome untrusted)
  Lookup("ac​me") -> tenancy: scope was not produced by this authority: forbidden (Outcome untrusted)
  Each over a one-member cohort: err=<nil> members=[{[tenant reference] untrusted ...}]
  ```
- **What / Why this severity:** the comment at `reference.go:37-39` says the library
  will not fold because folding is the control plane's answer. Correct — but the
  consequence is an **unstated requirement on the control plane**: `Lookup` must be
  byte-idempotent, i.e. it must echo back exactly the bytes it was given. A control
  plane that canonicalises (any case-insensitive store, any store that normalises
  Unicode, any store that trims) satisfies its own contract and is silently
  incompatible with this one.
  The failure shape is the bad part. `Each` returns `err = nil` — the run **succeeds** —
  while every `Member` carries `Outcome untrusted`. A monthly-billing grant over 8,000
  tenants completes, reports no error, bills nobody, and every member is labelled with
  the outcome an operator has wired to "someone is forging scopes". The refusal is a
  correct decision reported as the wrong diagnosis, which is the definition of a
  behaviour that is undefined for a neighbouring input.
- **Why this timing:** it is an unwritten precondition on the one interface a
  deployment must implement. Every consumer that discovers it discovers it in
  production, on the batch path.
- **Close criteria:**
  - [ ] the `Resolver` contract states, in the interface's documentation and in
        `docs/modules/en/tenancy.md`, that `Lookup(r)` must return a `Resolution` whose
        `Reference` is byte-identical to `r`.
  - [ ] the mismatch produces a distinct sentinel and `Outcome` ("the control plane
        answered about a different tenant"), not `ErrUntrusted`/`untrusted`.
  - [ ] `Each` reports a run in which no member succeeded as something other than
        `err == nil`, or the contract for reading `[]Member` is stated where a caller
        will see it.

### GAP-8 [high][immediate] The reference validator rejects the ASCII-visible hazards and accepts the Unicode ones

- **Where:** `tenancy/reference.go:40-53` (`Reference.valid`) — same shape at
  `tenancy/grant.go:35-45` (`Purpose.valid`).
- **Scale:** systemic — 2 validators, identical rule.
- **Confidence:** CONFIRMED. `go run -overlay=<pristine> ./ref`:
  ```
  NUL U+0000                    rejected      ZERO WIDTH SPACE U+200B        ACCEPTED
  NEL U+0085                    rejected      ZERO WIDTH NON-JOINER U+200C   ACCEPTED
  NBSP U+00A0                   rejected      LEFT-TO-RIGHT MARK U+200E      ACCEPTED
  LINE SEPARATOR U+2028         rejected      RIGHT-TO-LEFT OVERRIDE U+202E  ACCEPTED
  leading space                 rejected      BOM / ZWNBSP U+FEFF            ACCEPTED
  lone surrogate                rejected      SOFT HYPHEN U+00AD             ACCEPTED
                                              MONGOLIAN VOWEL SEP U+180E     ACCEPTED
                                              WORD JOINER U+2060             ACCEPTED
  NFC reference == NFD reference: false
  zero-width-space reference == plain reference: false
  zwsp.String()="[tenant reference]" plain.String()="[tenant reference]"
  ```
  Also accepted: Cyrillic `а` (U+0430) and fullwidth `ａ` (U+FF41) confusables.
- **What / Why this severity:** the validator is not neutral — it has an opinion, and
  the opinion is ASCII-shaped. `unicode.IsControl` is category **Cc** only
  (`≤ U+001F`, `U+007F..U+009F`), and `unicode.IsSpace` is the White_Space property.
  Neither covers category **Cf** (format), which is where every invisible character
  that matters lives. So the rule "a reference contains nothing invisible" is enforced
  for the ASCII spelling of invisible and not for the Unicode spelling.
  The consequence: `acme` and `ac<U+200B>me` are two distinct tenants that render
  identically in a terminal, a browser, a support ticket and a spreadsheet — and
  `Reference.String()`/`LogValue()` redact to `[tenant reference]` (reference.go:25,31),
  so **no log in the system can tell them apart either**. An operator provisioning a
  tenant from a pasted value picks up a BOM and creates a second tenant that looks
  like the first. A `U+202E` override reverses how the reference renders in an
  operator console. NFC/NFD and Latin/Cyrillic confusables give the same pair.
  This is not a SQL-injection issue — see *Clean results*, everything binds — it is an
  identity issue, and identity is what this type is for.
- **Why this timing:** it is a validation rule on a public parse function; loosening
  it later is harmless, tightening it later rejects references already in production
  databases.
- **Close criteria:**
  - [ ] the rule is stated as a rule ("a reference contains no invisible or
        non-rendering character") and enforced for the whole of it, including
        category Cf, or the decision to allow Cf is written down with the log-redaction
        consequence answered.
  - [ ] `docs/modules/en/tenancy.md` states whether normalisation is the control
        plane's job, and what happens when it disagrees (links GAP-7).
  - [ ] a table-driven test over the character classes above, so the boundary is
        pinned rather than incidental.

### GAP-9 [high][immediate] The example's placeholder durable key is long enough to be accepted, so copying the wiring produces a working, publicly-known seal key

- **Where:** `_examples/tenancy-sharedrow/main.go:76` —
  `DurableKey: []byte("replace-me-with-32-bytes-from-your-secret-store")`.
  Accepted by `tenancy/authority.go:56-58` (`len(spec.DurableKey) < MinDurableKeyBytes`).
- **Scale:** local, 1 occurrence.
- **Confidence:** CONFIRMED — `python3 -c "print(len('replace-me-with-32-bytes-from-your-secret-store'))"` → `47`, and
  `MinDurableKeyBytes = 32` (`authority.go:11`). The example is the only end-to-end
  wiring in the repository and `docs/modules/en/tenancy.md` points consumers at it.
- **What / Why this severity:** the example is a template, and this template compiles,
  runs and seals. Nothing forces the string to be replaced: a consumer who copies the
  composition root — which is exactly what the file's own header comment says it is
  for ("the wiring is the part a consumer copies") — gets a deployment whose durable
  key is a literal published in a public repository. That key is the HMAC key for
  every job identity token (`seal.go:83-97`), so anyone with it can mint a token
  that makes a worker restore any tenant's identity.
  `restrictions.md` §4: no secrets in code, and a placeholder that works is worse than
  one that does not.
- **Why this timing:** hardcode findings are never deferred, and the blast radius is a
  consumer's production deployment.
- **Close criteria:**
  - [ ] the example's key is shorter than `MinDurableKeyBytes`, or a recognised
        sentinel that `tenancy.New` refuses by name, so copying the file fails loudly.
  - [ ] the example reads the key from an environment variable / config with a
        fail-fast, matching how the rest of the repository handles secrets.

### GAP-10 [high][immediate] Six magic thresholds with no derivation, no config entry and no calibration note; two of them a real deployment hits

- **Where and what each is:**

  | Constant | Where | Kind |
  |---|---|---|
  | `MaxCohortSize = 10000` | `tenancy/grant.go:18` | **picked** — no derivation, not in `docs/`, not even in `docs/api/surface.md` |
  | `MaxPurposeBytes = 64` | `tenancy/grant.go:17` | **picked** |
  | `MaxReferenceBytes = 128` | `tenancy/reference.go:11` | **picked** |
  | `digestBytes = 16` | `tenancy/tenancystorage/storage.go:14` | **picked** (128-bit truncation of SHA-256) |
  | `MinPartitionBytes = 16` | `tenancy/tenancycache/cache.go:11` | **picked**, but with a prose justification at `cache.go:37-42` |
  | `DefaultOpenTimeout = 30s` | `tenancy/tenancydb/database.go:14` | **picked**, but overridable via `DirectorySpec.OpenTimeout` |
  | `MaxPrefixBytes = 30` | `tenancy/tenancystorage/storage.go:13` | **derived** — `63 − 1 − 2·digestBytes`, and the derivation is written nowhere |
  | `MaxPartitionBytes = 32`, `MinDurableKeyBytes = 32`, `[32]byte`, `[8]byte` generation | `tenancycache/cache.go:12`, `authority.go:11`, `scope.go:31` etc. | **legitimate** — SHA-256/HMAC widths |

- **Scale:** systemic — 6 unjustified constants across 5 packages.
- **Confidence:** CONFIRMED for the boundaries and the derivation:
  ```
  Accept(cohort of  10000) -> <nil>
  Accept(cohort of  10001) -> tenancy: a grant names 1..10000 tenants: ...
  prefix (30 bytes) -> namespace "pppppppppppppppppppppppppppppp-92337d0d18df1f4f7a2d7303648dd3eb" (len 63)
  prefix (31 bytes) -> refused: tenancy: an object namespace prefix is 1..30 bytes: ...
  ```
  (`storage.validateNamespace`, `storage/validate.go:11-23`, caps a namespace at 63 bytes.)
- **What / Why this severity:** two of these are hit by ordinary deployments, not by
  hostile ones.
  **`MaxCohortSize = 10000`** — a SaaS with more than ten thousand tenants cannot
  express "run the monthly close for everyone" as one grant. It must chunk, and each
  chunk is a separately-bound `*Grant` with its own deadline and its own MAC, so the
  partial-failure story the `Each` design was built for (grant.go:113-116) is now the
  caller's problem, per chunk. Nothing says why 10,000 and not 1,000 or 10^6, and
  there is no way to raise it.
  **`MaxPrefixBytes = 30`** — a bucket prefix of 31 characters is refused, and so is
  any prefix that is not lowercase-alphanumeric-with-inner-hyphen (GAP-13). Deployments
  name buckets after services; 31 characters is unremarkable. The number is genuinely
  derived, but because the derivation is unwritten, the next person who changes
  `digestBytes` to 20 will not know to change 30 to 26, and the failure will be an
  entire object store refusing every namespace.
  `MaxReferenceBytes = 128` is safe downstream — see *Clean results* — but it is still
  a number somebody picked, and a control plane whose references are 36-character
  UUIDs with a 100-character environment prefix is not far from it.
- **Why this timing:** `universality.md` — "magic thresholds with no derivation, no
  config entry and no calibration note" is a listed violation, minimum
  `[high][immediate]`, never deferred.
- **Close criteria:**
  - [ ] each of the six carries a one-line note saying how it was chosen and what a
        deployment does when it is wrong, or becomes a field on the relevant `Spec`.
  - [ ] `MaxPrefixBytes` states its derivation from `storage`'s 63-byte namespace bound
        and `digestBytes`, ideally as a computed expression rather than a literal.
  - [ ] `MaxCohortSize` is either justified against a measured cost (MAC size, memory,
        the `Each` loop's holding time) or made configurable per `Spec`.

### GAP-11 [medium][immediate] `ErrMalformed`'s text names "a tenant reference" and is reused for the epoch, the purpose, the cohort size and the storage prefix

- **Where:** `tenancy/errors.go:11` defines the text; reused at
  `tenancy/reference.go:59` (an **epoch**), `tenancy/grant.go:26,84,87` (a **purpose**,
  a **class list**, a **cohort size**), `tenancy/tenancystorage/storage.go:47`
  (a **bucket prefix**), `tenancy/tenancyjobs/jobs.go:21,42,50`.
- **Scale:** systemic — 8 reuse sites.
- **Confidence:** CONFIRMED:
  ```
  NewEpoch(0) -> tenancy: value is not a well-formed tenant reference: crud: bad request
  Accept(cohort of 10001) -> tenancy: a grant names 1..10000 tenants: tenancy: value is not a well-formed tenant reference: crud: bad request
  prefix "" -> tenancy: an object namespace prefix is 1..30 bytes: tenancy: value is not a well-formed tenant reference: crud: bad request
  ```
- **What / Why this severity:** the sentinel was named after the first thing it
  validated and then reused for four unrelated concepts. An operator who sees
  *"value is not a well-formed tenant reference"* in a log while starting a bucket
  will go looking at tenant references. The wrapping also composes badly: the outer
  message says "prefix is 1..30 bytes" and the inner says "tenant reference", so the
  two halves of one error contradict each other. This is the error-message analogue of
  fitting to the first example.
- **Why this timing:** it is the public error contract; `errors.Is` callers already
  depend on the sentinel identity, so splitting it later is a breaking change.
- **Close criteria:**
  - [ ] the sentinel's text is neutral ("value is not well-formed for this field"), or
        distinct sentinels exist per concept and `Classify`/`OutcomeFor` map them.
  - [ ] no error message names a concept the caller did not supply.

### GAP-12 [high][immediate] One tenant occupies several cache partitions, and which one is decided by the length of the key next to it

- **Where:** `tenancy/tenancycache/cache.go:43-54` (`Partition` returns
  `digest[:min(limit.MaxBytes, MaxPartitionBytes)]`), consuming a budget computed at
  `cache/address.go:110-118` (`partitionLimit = maxKeyBytes - len(raw)`) and hashed at
  `cache/key.go:122` (`sha256.Sum256(raw)`).
- **Scale:** local to one function, but it decides the partition of every tenant-scoped
  cache entry.
- **Confidence:** CONFIRMED. `go run -overlay=<pristine> ./part`, one tenant, one key:
  ```
  budget    16 -> partition bytes=16  digest=4bebd3e2ce62...
  budget    20 -> partition bytes=20  digest=a90ee26032e5...
  budget    24 -> partition bytes=24  digest=c59072ba4788...
  budget    31 -> partition bytes=31  digest=033999e41835...
  budget    32 -> partition bytes=32  digest=a936251ec9e7...
  budget    64 -> partition bytes=32  digest=a936251ec9e7...
  one tenant occupies 5 distinct cache partitions across those budgets
  budget    15 -> tenancy: tenant binding is not compatible with this capability: forbidden
  ```
- **What / Why this severity:** the partition is supposed to *be* the tenant's identity
  in the cache. Here it is a variable-length prefix of the tenant digest, re-hashed,
  and the length is `MaxKeyBytes − len(encoded key)`. Two entries for the same tenant
  whose keys differ in length by enough land in different partitions. Isolation still
  holds (no cross-tenant collision), which is why this is not critical — but the
  partition stops being an identity, so anything that ever addresses a partition
  (per-tenant invalidation, per-tenant quota, an operator reading the address, a
  provider that shards on the partition digest) sees one tenant as N.
  It is also fitted to the *default* budget: with `MaxKeyBytes = 16<<10` the budget is
  always > 32 and the behaviour never varies, which is precisely why nobody has seen
  it. A deployment that sets `MaxKeyBytes: 64` — a Redis-key-length-conscious one —
  gets the variable behaviour, and if its keys come within 16 bytes of the limit it
  gets `ErrIncompatible`: *"tenant binding is not compatible with this capability"*,
  a refusal that names the tenant binding for what is a key-budget problem.
- **Why this timing:** the partition is part of the on-wire cache address; changing it
  invalidates the cache, which is cheap now and expensive once a deployment depends on
  warm data.
- **Close criteria:**
  - [ ] the partition a tenant gets is the same value regardless of the key beside it —
        e.g. always `digest[:MinPartitionBytes]`, or the full digest with the budget
        checked rather than consumed.
  - [ ] a budget too small to carry a partition refuses with an error that names the
        key budget, not the tenant binding.
  - [ ] a test asserting two keys of different lengths for one tenant produce the same
        partition digest.

### GAP-13 [medium][immediate] The namespace prefix has two owners, so an operator's typo escapes the tenancy error contract

- **Where:** `tenancy/tenancystorage/storage.go:42-51` — length checked here, character
  class checked by `storage.ParseNamespace` → `storage/validate.go:11-23`.
- **Scale:** local, 1 site.
- **Confidence:** CONFIRMED:
  ```
  prefix "Invoices"   -> refused: storage parse namespace: invalid | tenancy.OutcomeFor = error
  prefix "my_bucket"  -> refused: storage parse namespace: invalid | tenancy.OutcomeFor = error
  prefix "-invoices"  -> refused: storage parse namespace: invalid | tenancy.OutcomeFor = error
  prefix "faktury-ä"  -> refused: storage parse namespace: invalid | tenancy.OutcomeFor = error
  prefix "invoices-"  -> namespace "invoices--92337d0d18df1f4f7a2d7303648dd3eb"
  ```
- **What / Why this severity:** `namespaceOf` validates one half of the rule (length)
  and delegates the other half (`[a-z0-9]` with inner hyphens) to `storage`, so a
  prefix with a capital letter, an underscore, a non-ASCII letter or a leading hyphen —
  all normal in a service name — produces a raw `storage` error whose
  `tenancy.OutcomeFor` is `OutcomeError`, the bucket labelled "not one of ours". The
  tenancy seam's closed twelve-outcome set (`outcome.go:7-20`), which
  `outcome.go:44-46` says exists so a signal cannot carry a driver's text, has a hole
  for its own configuration mistakes. A trailing hyphen is meanwhile accepted and
  yields a double hyphen.
- **Why this timing:** it is the error contract of a public function, consumed by
  metrics.
- **Close criteria:**
  - [ ] `namespaceOf` validates the prefix against the same rule `storage` will apply,
        and refuses with a tenancy sentinel naming the character class.
  - [ ] a test that every rejected prefix maps to a tenancy `Outcome`, never `error`.

### GAP-14 [medium][immediate] The documented admission-consistency rule is not the implemented one

- **Where:** `docs/modules/en/tenancy.md:122-124` — *"a state admitted for writing **or
  for durable work** but not for reading is refused, naming the state"* — versus
  `tenancy/lifecycle.go:109-116`, which checks `ClassWrite` only, with a comment at
  `:106-108` saying durable is deliberately exempt.
- **Scale:** local, 1 contradiction (the Russian page at `docs/modules/ru/tenancy.md`
  was not checked line-for-line).
- **Confidence:** CONFIRMED by reading both, cited above.
- **What / Why this severity:** a deployment reading the doc believes
  `Admit(ClassDurable, Migrating)` without a matching read admission will be refused at
  construction. It is accepted. Since the code's comment argues the exemption is
  deliberate and correct, the doc is the wrong half — but a consumer designing an
  admission policy from the doc designs the wrong one, and `CLAUDE.md` treats a stale
  doc as a failing test.
- **Close criteria:**
  - [ ] `docs/modules/en/tenancy.md` and its Russian twin say "for writing", and state
        that durable work is deliberately exempt and why.

### GAP-15 [high][immediate] Only two ownership shapes are exercised anywhere, and both are shapes where the reference happens to be a decimal number or a plain string

- **Where:**
  - `tenancy/tenancyrow/row_test.go:18` — `TenantID string`, `Value = nil` (8 of the 9
    `Column` call sites in the unit suite pass `nil`; `grep -n "tenancyrow.Column\[" tenancy/`).
  - `test/integration/tenancy_test.go:36-47` — references `"1"` and `"2"`,
    `numericTenant = strconv.ParseInt`, column `int64`.
  - `_examples/tenancy-sharedrow/main.go:115,127,157` — references `"42"` and `"43"`,
    same `strconv.ParseInt`.
  - `Through` appears exactly once in the whole repository —
    `tenancy/tenancyrow/row_test.go:278`, with `nil` — and never with a `Value`.
- **Scale:** systemic — the entire `Value` seam has 2 covered shapes out of the open set.
- **Confidence:** CONFIRMED — the greps above, plus the fact that GAP-1 and GAP-2 both
  survive a green suite (`go test -count=1 -overlay=<pristine> ./tenancy/...` → 6/6 ok).
- **What / Why this severity:** this is the fit-to-example finding that explains the
  others. The only two configurations ever run are (a) reference string → string
  column and (b) a reference that **is a decimal integer** → BIGINT column. Both the
  example and the integration test chose references (`"42"`, `"1"`) that make
  `strconv.ParseInt` succeed, so the numeric path has never been exercised with a
  reference that is not a number, and no test anywhere passes a `Value` whose result
  type differs from the column's. That is why `assign`'s `ConvertibleTo` looked
  adequate: in the two shapes on record it is.
  Apply the second-instance test from `universality.md`: a deployment with
  `tenant_id uuid` and a `Value` returning `uuid.UUID` works (proven in `./own`) — but
  the same deployment with the default `Value` gets a `crud.SchemaError` at request
  time rather than at wiring time, and one with `tenant_id text` and a numeric `Value`
  gets GAP-1's silent corruption. Neither is covered.
- **Why this timing:** it is the coverage gap that lets GAP-1 and GAP-2 ship green, and
  any fix for those needs these tests to exist first.
- **Close criteria:**
  - [ ] a table-driven test over column kinds × `Value` result types asserting, for
        each cell, either the exact stored value or a refusal — never a conversion.
  - [ ] at least one test where the reference is **not** a decimal number and the
        column is numeric, asserting the refusal path.
  - [ ] `Through` covered with a non-`nil` `Value` and a non-string related column.
  - [ ] a test asserting the value written by `Apply` and the value bound by `Narrow`
        are equal, for every covered cell.

---

## Clean results (commands run, nothing found)

These are stated because silence is not evidence.

1. **No domain literals, no sample-derived branching, no regexes, no fixture paths.**
   - `grep -rnoE '"[^"]{4,}"' --include='*.go' tenancy/ | grep -v _test.go` → import
     paths, the `Lifecycle`/`Class` `String()` vocabulary, the four redaction strings
     (`"[tenant reference]"`, `"[tenant resolution]"`, `"[tenancy scope]"`,
     `"[tenancy grant]"`, `"[tenant cache key]"`), error prose, and the domain
     separator `"grant"` at `grant.go:188`. Nothing sample-derived.
   - `grep -rnE '== *"' --include='*.go' tenancy/ | grep -v _test` → 10 hits, all
     `== ""` emptiness guards.
   - `grep -rn "regexp" tenancy/` → nothing.
   - `grep -rn "_examples\|testdata\|fixture\|/test/" --include='*.go' tenancy/ | grep -v _test.go` → nothing.
     The delete-the-example test passes for `tenancy/` itself.

2. **A hostile-but-accepted reference is structurally inert downstream.** This was the
   sharpest question in the brief and the answer is clean.
   `go run -overlay=<pristine> ./hostile` — full gate, root + declared relation preload:
   ```
   "100%_of'them\""   [0] SELECT ... FROM "invoices" WHERE "tenant_id" = $1   args=["100%_of'them\""]
                      [1] SELECT ... FROM "lines" WHERE "invoice_id" IN ($1) AND "tenant_id" = $2
   "../../etc/passwd" [0] SELECT ... WHERE "tenant_id" = $1                   args=["../../etc/passwd"]
   "tenant​zero" [0] SELECT ... WHERE "tenant_id" = $1
   "café" (NFC) and "café" (NFD)  — both bind, both distinct
   ```
   `%` and `_` never reach a `LIKE`; `'`, `"`, `\`, `/`, `..` never reach statement
   text. `crud.Eq` binds, always.
   `go run -overlay=<pristine> ./down`, reference `../..%_'"tenant`:
   `namespace "invoices-1d926a15f7bd35d75b9d7f30dd578fb4"` — the storage namespace,
   the cache partition and the jobs partition are all digests
   (`tenancystorage/storage.go:49-50`, `tenancycache/cache.go:51`,
   `jobs/scope.go:229-247` — `jobs` digests the partition into a `PartitionKey` before
   it reaches `jobspg`), so no downstream component treats a reference character
   structurally.

3. **The length budgets are coherent end to end.** A 128-byte reference —
   `MaxReferenceBytes`, the largest the type admits — passes every downstream limit:
   ```
   ParsePartition(len=128) -> err=<nil>            (jobs.MaxPartitionBytes = 512)
   token bytes = 168                               (jobs.MaxIdentityTokenBytes = 2048)
   unseal err=<nil>  reference matches=true
   namespace "invoices-92337d0d18df1f4f7a2d7303648dd3eb" (len 41)   — fixed width, independent of the reference
   ```
   There is **no length that passes `ParseReference` and then fails downstream**. The
   digest design is what buys this, and it is the right design.

4. **Epoch staleness assumes no ordering.** `tenancy/context.go:68-70` and
   `tenancy/tenancyjobs/jobs.go:92` compare with `!=`, never `>` or `<`. A control
   plane whose generation decreases (a rollback) is treated exactly like one that
   increases. That is correct and should stay; GAP-6 is about the *width*, not the
   comparison. Wraparound is not reachable: `NewEpoch(0)` refuses, so a counter that
   wraps to 0 is rejected rather than silently reused.

5. **`Admission.inconsistent()` genuinely refuses the fail-open configuration it
   claims to.** `authority.go:45-47` refuses a state admitted for writing but not
   reading, naming the state. Only the *doc's* description of it is wrong (GAP-14).

6. `go vet ./tenancy/...` → clean. No file in scope exceeds 400 lines
   (largest: `tenancydb/database.go`, 284).

---

## Remediation order

Boundaries and contracts before internals; the type-safety of the one deployment seam
before anything that depends on it.

1. **GAP-15 — the tests that make the rest verifiable.** (S, blast radius: test files
   only.) A table of column kinds × `Value` result types. Nothing below can be shown to
   work without it, and it must land first so the fixes for GAP-1/GAP-2 fail red before
   they go green.
2. **GAP-2 — refuse a nil ownership value at all 5 call sites.** (S, blast radius:
   `tenancyrow/row.go` + one gate test.) Independent of GAP-1 and strictly smaller;
   do it first so the fail-open is closed while GAP-1's type rule is being argued.
3. **GAP-1 — `assign` checks instead of converting.** (M, blast radius:
   `tenancyrow/row.go`, `docs/modules/{en,ru}/tenancy.md`, possibly a new sentinel in
   `tenancy/errors.go`.) Depends on 1. This is the change that makes the `Value` seam
   safe for any deployment, and it is a behaviour change for any consumer currently
   relying on an implicit conversion — which, by GAP-15, is nobody who is tested.
4. **GAP-3 — an admission that names nothing refuses construction.** (S, blast radius:
   `lifecycle.go`, `authority.go`, one test.) Independent; do it early because it
   changes a constructor's contract that GAP-4's work will build on.
5. **GAP-9 — the example's key.** (S, blast radius: one line in `_examples`.)
   Independent, trivial, and the only finding with an immediate external-security cost.
6. **GAP-7 — write down the `Resolver` byte-identity precondition and give the
   mismatch its own sentinel and `Outcome`.** (S–M, blast radius: `authority.go`,
   `errors.go`, `outcome.go`, `docs/`.) Must precede GAP-4, because both add outcomes
   and the closed twelve-constant `Outcome` set should grow once, not twice.
7. **GAP-4 — the lifecycle vocabulary: decide and document, or open it.** (L, blast
   radius: `lifecycle.go`, `authority.go`, `scope.go` binding format if the enum
   widens, `docs/api/surface.md`, a decision doc.) Depends on 4 and 6. This is the
   expensive one and it is a public-surface decision; it should be a written decision
   before it is code.
8. **GAP-5 — the class axis: justify three or open it.** (L, blast radius: every
   `Class` parameter in the public surface — `Bind`, `Scope`, `Namespace`, `Keyed`,
   `Borrow`, `Capture`.) Depends on 7; the same decision doc should answer both, since
   lifecycle × class is one policy matrix.
9. **GAP-6 — state the `Epoch` contract; widen the type if the answer is "opaque".**
   (M if documentation-only, L if the seal token's format changes.) Depends on nothing,
   but the format change must not land after deployments have enqueued jobs.
10. **GAP-10 — derive, configure or annotate the six constants.** (S each, blast radius
    local; `MaxCohortSize` is the one that may become a `Spec` field, which is M.)
11. **GAP-12 — make the cache partition a stable identity.** (S, blast radius:
    `tenancycache/cache.go` + a cold cache.) Do it before any deployment warms one.
12. **GAP-8 — extend the reference validator to category Cf, or write the decision.**
    (S in code, but it tightens a public parse function; sequence it before any
    deployment stores references, i.e. now.)
13. **GAP-13, GAP-11, GAP-14 — the error contract and the doc.** (S each, no
    dependencies.) GAP-11 changes a sentinel's text, not its identity, so it is safe
    at any point.

---

## What I did not check

- **`tenancydb` beyond the constants.** `Directory`'s cache, eviction, lease and
  concurrency were read but not probed. The `binding{reference, epoch}` map key
  inherits the byte-identity property of GAP-7/GAP-8 (an NFC and an NFD reference are
  two entries and therefore two connection pools), but I did not run it. `Fence`,
  `Sources` failure modes, and behaviour under a real second database are untested here
  and are already `[~]` in `TENANCY_PLAN.md` §S4.
- **A real driver.** Every SQL claim comes from `crudtest.Postgres()`, which records
  statements and arguments. I did not verify what PostgreSQL or MySQL *does* with
  `tenant_id = 42` bound as `int64` against a `text` column (pgx errors, MySQL coerces),
  because GAP-1's defect is already proven on the write side, in process. The
  integration suite needs Docker and was not run.
- **`driver.Valuer` and enum ownership columns end to end.** `./own` shows
  `sql.NullString` refused with the default `Value` and a matching `Value` accepted at
  the model layer, but I did not check how `crud`'s statement builder binds a
  `driver.Valuer` struct, nor whether a custom `Valuer` whose `Value()` returns a
  different tenant than the struct holds would slip past `Apply`'s `EqualValues`
  comparison (`row.go:109`). That is a plausible second instance of GAP-1 and is
  untested.
- **`Purpose` and `Grant` beyond `MaxCohortSize` and the canonicalisation case.**
  `grantBinding` at `grant.go:185-204` writes `uint64(until.UnixNano())`, which is
  undefined for times outside 1678–2262; a "no expiry" sentinel like
  `time.Unix(1<<62, 0)` folds. `Grant`'s fields are unexported and there is no
  deserialiser, so I could not construct the collision from outside the package and did
  not chase it. **PLAUSIBLE, not confirmed.**
- **`docs/modules/ru/tenancy.md`** was not compared line-for-line against the English
  page; GAP-14 may or may not be duplicated there.
- **The other five dimensions.** Microkernel, building blocks, architecture metrics,
  data integrity/concurrency, DRY, readability, restrictions and test quality are out
  of scope here. In particular: `tenancydb.Directory`'s in-process mutex and cache as a
  correctness mechanism under N replicas, the `Each` loop's transaction story, and
  `crud/decorators/security/security.go` (1102 lines) were all read but not audited.
- **Mutation testing of the tenancy suite.** Deliberately not run: another agent was
  already mutating the same working tree during this audit (see *Measurement hazard*),
  and adding a second mutator would have made both sets of results unreadable. GAP-15
  is the coverage claim I can make without it; a proper mutation campaign against
  `tenancyrow/row.go` and `lifecycle.go` should follow, on a quiescent tree.
- **`.agents/artifacts/usecases/TENANCY_USECASES.md` (UC-1..93, INV-1..36) was not
  cross-referenced.** The map names it as the specification; I audited the code against
  the law, not against that document, so a finding here may already be a known,
  accepted UC — and conversely an INV with no test would not have shown up in my sweep.
