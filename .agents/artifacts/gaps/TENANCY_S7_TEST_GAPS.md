# TENANCY — S7 (the core / seam split) — TEST GAPS

## Round 1 — the split test suites — 2026-09-06

**What was graded.** Every `*_test.go` under `tenancy/` after the split —
`tenancy/{scope,vocabulary,grant,contract}_test.go` (external) and
`tenancy/{seal,binding,grant_binding,support}_test.go` (in-package), plus
`tenancyrow/{row,contract,revalidate,support}_test.go`,
`tenancydb/{database,support}_test.go`, `tenancyjobs/{durable,support}_test.go`,
`tenancystorage/storage_test.go`, `tenancycache/cache_test.go`,
`scripts/tenancy_test.go` and `test/integration/tenancy_test.go` — against the
implementation in `tenancy/*.go` and `tenancy/tenancy*/*.go`, the spec in
`TENANCY_USECASES.md`, and the pre-split suite recorded in `TENANCY_PLAN.md`
§S7 and `TENANCY_S1_TEST_GAPS.md`.

**Runs.**

```
go test -count=1 ./tenancy/...                       ok  0.16s wall (six packages, none over 0.15s)
go test -race -count=1 ./tenancy/... ./scripts/      ok  8.6s wall (8.4s of it is ./scripts, which shells out to `go list`)
go test -count=1 ./tenancy/... ./scripts/  (final)   ./tenancy/... ok; ./scripts/ FAIL — see GAP-S7-T0
                                                     (a concurrent edit to D-117 landed at 00:35, mid-round)
go test -race -count=2 ./tenancy/...                 ok  1.5s
go test -count=1 -shuffle=on ./tenancy/...  (x3)     ok  all three
go test -count=1 -cover ./tenancy/...                79.9 / 79.8 / 87.6 / 80.4 / 71.4 / 73.3 %
gofmt -l tenancy                                     silent
```

No `t.Parallel()`, no network, no clock reads outside the injected `Now`, no
leftover files. The unit suite is genuinely fast and deterministic — three
shuffled runs and a `-count=2 -race` run are clean, and the only wall-clock in it
is the three timing-based directory tests carried over from S1 (`GAP-T16`, still
open). The integration suite was **not** run: `TENANCY_S1_TEST_GAPS.md` and the
user memory both record that port 55432 is held by another project's PostgreSQL,
and a run that authenticates against the wrong server is worse evidence than no
run. Nothing below depends on it.

**Mutation campaign.** **81 deliberate breakages**, each applied to the working
tree, run, and restored from a checksummed snapshot; **57 caught, 24 survived**.
Plus 6 single-test probes that ask whether a *named* test can fail on the
behaviour it names. Full log at the end. The tree was verified byte-identical to
its starting state afterwards (`md5sum` over all 33 files) and `gofmt -l` is
silent.

**What the split did not lose, stated plainly, because it is the question that
was asked.** The two deliberate renames are honest: `TestARestoredGenerationReads‐
NoneOfThePreviousOnes{Objects,Values}` each still fail when the generation leaves
the digest (MS5, caught by both plus the new core test), and the two halves of
`TestANamespaceOrPartitionWithNoScopeIsRefused` each still fail when their own
zero-scope refusal is deleted (MS30, MS34). **The cross-seam property survived
the split**: `Scope.Digest` is exercised from three directions — the new core
test `TestTheDigestSeparatesTenantsAndGenerations` (seal_test.go:181) and both
seam tests — and MS5 kills all three, which proves each seam really derives from
`Scope.Digest` rather than rolling its own. Of the 24 survivors, **14 are
verbatim carry-overs of survivors already recorded in `TENANCY_S1_TEST_GAPS.md`**
(M3, M5, M7, M9, M12, M13, M14, M15, M18, M19, M21b, M23, M24, M42/M44) — the
split neither fixed nor worsened them. **Six survivors are new**, and they are
all in code the split created: `seal.go`'s MAC, the `tenancyjobs` restorer, and
`tenancystorage`'s bounds.

---

### GAP-S7-T0 [critical][immediate] `D-117` claims two tests that do not exist, for two behaviours the code does not have, and `go test ./scripts/` is red on it

*(Numbered 0 because it arrived during this round's final verification run, after
the findings below were written: `docs/ai/decisions/D-117…md` was edited at 00:35
by concurrent work. It is recorded here because it is a coverage claim, which is
this reviewer's subject, and because the round's own evidence contradicts it.)*

- **Where:** `docs/ai/decisions/D-117-a-verified-scope-is-minted-never-manufactured.md:148`
  and `:151`; the checker is `scripts/docs_test.go:59`
  (`TestEveryTestNameTheDocsCiteExists`); the code is `tenancy/seal.go:41-55`
  (`Seal`), `:83-98` (`mac`) and `tenancy/authority.go:122-130` (`minted`).
- **What:** the decision's "Proven by" list cites
  `TestOnlyAScopeTheDurableClassAdmitsIsSealed` and
  `TestARecordSealedInAnotherDeploymentIsNotRead`. Neither name exists in any
  `_test.go` in the repository, so the suite fails:

  ```
  --- FAIL: TestEveryTestNameTheDocsCiteExists (0.09s)
      docs_test.go:59: TestARecordSealedInAnotherDeploymentIsNotRead is cited by …D-117…:151 and no _test.go declares it
      docs_test.go:59: TestOnlyAScopeTheDurableClassAdmitsIsSealed is cited by …D-117…:148 and no _test.go declares it
  ```

  `./scripts/` was green earlier in this session (`go test -race ./tenancy/...
  ./scripts/` → `ok … 8.357s`) and is red now. Worse than the missing names: both
  cited *behaviours* are absent from the code, so writing the tests under those
  names would fail.
  - `:148` says the core primitive "refuses a lifecycle the durable class does not
    admit, rather than relying on each adapter to ask first". `Seal` calls
    `Authority.minted`, which checks `IsZero` and `boundTo` and nothing else.
    Reproduced: an authority admitting only `ClassRead` and `ClassWrite`, whose
    `Verify(ctx, ClassDurable)` refuses, still seals a durable record from a read
    scope — `a deployment that admits no durable work sealed a 44-byte durable
    record from a read scope`. (This is the implementation half of
    `TENANCY_S7_GAPS.md` GAP-1.)
  - `:151` says a copied `DurableKey` is not enough "because `Origin` is inside
    the seal too". `grep -n origin tenancy/seal.go` returns nothing: `Sealer.mac`
    writes the field count, the binding fields, the generation and the reference.
    The nearest real test, `TestARecordAnotherDeploymentSealedIsNotRead`
    (`seal_test.go:142-156`), varies only the key, so it would pass with `Origin`
    absent — which it is. (Implementation half: `TENANCY_S7_GAPS.md` GAP-2.)
- **Why this severity:** `CLAUDE.md` makes a decision doc binding and a stale doc
  a defect equal to a failing test; the rubric makes an unbacked coverage claim
  `[critical]`. Here the claim is unbacked twice over — the test does not exist
  and the property is not implemented — and it is the exact property
  (`Origin` in the seal) that the next reader will assume when they wire a second
  deployment against one `DurableKey`.
- **Why this timing:** the checker that exists for precisely this is failing right
  now, so `make unit`'s `./scripts/` leg is red for everyone until it is resolved.
- **Close criteria:**
  - [ ] `go test ./scripts/` is green.
  - [ ] Either the two behaviours are implemented and the two tests written under
        the cited names, or the citations are corrected to
        `TestARecordAnotherDeploymentSealedIsNotRead` and the `Origin`/durable-class
        sentences are removed from `D-117`.
  - [ ] If `Origin` enters the seal MAC, a test builds two authorities sharing one
        `DurableKey` and differing only in `Origin` and asserts each refuses the
        other's record, with the same-origin control.
- **Status:** open

### GAP-S7-T1 [critical][immediate] The durable token's own claim — which tenant, which generation — is outside every assertion in the suite

- **Where:** `tenancy/seal.go:83-98` (`Sealer.mac`) is the code;
  `tenancy/seal_test.go:99-114` (`TestASealedRecordIsReadBackAsWhatWasSealed`),
  `:119-140` (`TestBindingFieldsCannotBeSlidPastOneAnother`),
  `:142-156`, `:158-176`, and
  `tenancy/tenancyjobs/durable_test.go:223-284`
  (`TestAForgedDurableRecordEntersNoHandler`) are the tests that should have
  caught it.
- **What:** two one-line mutations survive the whole suite:
  - **MS18** — delete `writeField(mac, reference.Value())` from `Sealer.mac`
    (`seal.go:94`). The MAC then covers the binding fields and the generation but
    not the tenant. **SURVIVED** `go test ./tenancy/...` and `./scripts/`.
  - **MS17** — delete the three lines that write the generation into the MAC
    (`seal.go:91-93`). **SURVIVED** the same.

  Both are exploitable, and I reproduced both rather than arguing them. Under
  MS18, splicing an honest token's 40-byte header onto a different tenant's name
  verifies:

  ```
  REPRODUCED: a record sealed for acme verified as "globex" at generation 1
  REPRODUCED: a record this deployment sealed for acme ran as "globex"
  ```

  the second line being the end-to-end path — `jobs.RestoreTrustedIdentity` →
  `identityRestorer.RestoreIdentity` → a handler context carrying globex. Under
  MS17, overwriting the plaintext generation prefix verifies:

  ```
  REPRODUCED: a record sealed at generation 1 verified as "acme" at generation 2
  ```

  which defeats the restore fence at `tenancyjobs/jobs.go:92` — the record whose
  generation the fence exists to reject simply names the current one.

  The reason the existing tests miss it is structural. Every forgery in the suite
  is a token whose **MAC does not match**: `stripped` zeroes it, `scrambled`
  overwrites it, `foreignRecord` computes it under another key,
  `TestARecordAnotherDeploymentSealedIsNotRead` uses another key,
  `TestBindingFieldsCannotBeSlidPastOneAnother` varies the binding fields. Not one
  test presents a token whose **MAC is one this deployment produced and whose
  claim has been edited** — which is the only shape that can tell you what the MAC
  actually covers.
- **Why this severity:** this is INV-14 verbatim ("Nothing recorded in a durable
  artefact — partition, token, provenance, payload — is sufficient to act for a
  tenant"; violation consequence: "Queue write access becomes tenant
  impersonation") and UC-56's acceptance criterion ("A record whose protected
  reference is tampered with is refused, not resolved to whatever the payload
  said"). The seal is the whole reason `Sealer` was lifted into the core in S7,
  and the two fields it exists to authenticate are the two nothing asserts. A
  refactor of `mac` — reordering it, extracting a helper, switching to a
  length-prefixed struct — that drops either field ships green.
- **Why this timing:** `Sealer` is now a *core* capability with two exported
  methods and is the seam every future durable adapter (outbox, schedules,
  `D-118`'s transactional enqueue) will build on. Every one of them inherits this
  blind spot the day it is written.
- **Close criteria:**
  - [ ] A test seals a record for tenant A and splices tenant B's name over the
        tail, asserting `Unseal` answers `ErrUntrusted`, with the honest token as
        the control.
  - [ ] A test seals at generation 1, rewrites the plaintext generation prefix to
        2, and asserts `Unseal` answers `ErrUntrusted`.
  - [ ] The same two, driven through `jobs.RestoreTrustedIdentity` in
        `tenancyjobs`, so the seam is proved and not only the primitive.
  - [ ] Deleting `writeField(mac, reference.Value())` and deleting the generation
        write each fail at least one test in `go test ./tenancy/...`.
- **Status:** open

### GAP-S7-T2 [high][immediate] Both bulk-mutation tests still cannot fail — GAP-T2 was recorded closed and is not

- **Where:** `tenancy/tenancyrow/row_test.go:115-138`
  (`TestABulkMutationReachesTheDatabaseNarrowed`, assertion at `:135`) and
  `:245-260` (`TestAnUnscopedBulkMutationIsRefusedRatherThanWidened`, assertion at
  `:256`). `TENANCY_S1_TEST_GAPS.md` §Round 1 dispositions claims the first of
  these closes GAP-T2.
- **What:** replacing the tenancy narrowing with a predicate that names no tenant
  at all — `column.Narrow` returning `crud.Eq("Total", int64(424242))` —
  leaves **both tests passing**. I printed the statements to find out why:

  ```
  baseline:  DELETE FROM "invoices" WHERE ("tenant_id" = $1 AND ("id" = $2 AND "tenant_id" = $3 AND "number" = $4 AND "total" = $5))
  mutated:   DELETE FROM "invoices" WHERE ("total"     = $1 AND ("id" = $2 AND "tenant_id" = $3 AND "number" = $4 AND "total" = $5))
  ```

  The `DELETE` is compiled as *scope* `AND` *identity of each row that was read
  first*, and the identity clause spells out every column of the fixture row the
  test pushed into the recorder — including `"tenant_id" = $3`. So
  `strings.Contains(clauseOf(mutating), "\"tenant_id\" = $")` is satisfied by the
  test's own fixture data, not by the narrowing. GAP-T2's fix moved the assertion
  from the whole statement to the clause, which removed the *projection* as the
  false source and left the *row identity* as a new one.

  `TestAnUnscopedBulkMutationIsRefusedRatherThanWidened` is worse and unchanged
  since S1: it pushes `crudtest.Rows()` twice, so no `UPDATE` is ever issued, and
  its `strings.Contains(statement, "tenant_id")` runs against the pre-read
  `SELECT` whose projection lists the column.
- **Why this severity:** the narrowing is caught elsewhere —
  `TestEveryVerbNarrowsToTheScopesOwnTenantAndNoOther` kills this mutation (its
  first statement is the un-narrowed `SELECT`) — so this is not a live hole in the
  shipped product. It is a false coverage claim: two tests named for bulk
  narrowing prove nothing about bulk narrowing, and the gaps file records one of
  them as the closure of a `[critical]` finding. The next person to change the
  bulk path will read those names and believe them.
- **Why this timing:** the dispositions table is what the plan, the profile and
  the roadmap read from; a row that says "closed" and is not is worse than an open
  row.
- **Close criteria:**
  - [ ] `TestABulkMutationReachesTheDatabaseNarrowed` pushes a fixture row whose
        `TenantID` is **not** the scope's tenant, or asserts on the scope conjunct
        specifically (`strings.HasPrefix(clause, "(\"tenant_id\" = $1 AND")`), so
        the identity clause cannot supply the token.
  - [ ] `TestAnUnscopedBulkMutationIsRefusedRatherThanWidened` pushes rows so the
        `UPDATE` is actually issued, and asserts against that statement.
  - [ ] `column.Narrow` returning `crud.Eq("Total", 424242)` fails both.
- **Status:** open

### GAP-S7-T3 [high][immediate] The relation half of the narrowing still names no tenant: `Through` and the declared preload both accept a constant foreign value

- **Where:** `tenancy/tenancyrow/row_test.go:274-300`
  (`TestARowOwnedThroughARelationIsNarrowedByThatRelation`, assertion at `:286`)
  and `:324-364` (`TestAPreloadOfADeclaredRelationCarriesTheTenant`, assertion at
  `:343`). Code: `tenancyrow/row.go:151-157` (`through.Narrow`) and `:80-93`
  (`column.Relations`).
- **What:** two mutations, each of which is GAP-T1 applied to the relation path:
  - `through.Narrow` returns `crud.Eq(path+"."+field, "globex-1a2b")` — a
    hardcoded foreign tenant — **SURVIVED** (`TestARowOwnedThroughARelation…`
    asserts only that the SQL text contains `"tenant_id"` and `"invoices"`).
  - `column.Relations` narrows the declared relation to `"globex-1a2b"` —
    **SURVIVED the whole `tenancyrow` package**, because the preload assertion is
    `strings.Contains(clauseOf(statements[1]), "tenant_id")`, a column name, never
    an argument.

  The control that *is* good — narrowing the relation on the wrong *column*
  (`crud.Eq("Text", value)`) — is caught. So the tests distinguish "narrowed on
  the ownership column" from "narrowed on some other column", and cannot
  distinguish "narrowed to my tenant" from "narrowed to somebody else's".
- **Why this severity:** GAP-T1 was `[critical]` for exactly this shape and was
  closed for the root predicate only. The relation declaration is the design
  change the phase-4 review forced, and `row.go:42-49` calls the undeclared case
  "a cross-tenant read reachable from unprivileged input". A `Relations` that
  narrows every tenant's children to one tenant is the same read with the
  declaration present, and the suite is green for it. INV-11 ("Relation closure")
  is the invariant.
- **Why this timing:** it is the same one-line assertion GAP-T1's fix already
  wrote for the root (`slices.Contains(statement.Args, any(secretReference))`),
  applied two lines lower. Deferring it leaves the closed `[critical]` half-closed.
- **Close criteria:**
  - [ ] Both tests assert the bound argument of the child/related statement is the
        scope's own reference, not only that the clause names the column.
  - [ ] `through.Narrow` and `column.Relations` each returning a constant foreign
        reference fails at least one test.
- **Status:** open

### GAP-S7-T4 [high][immediate] The durable class is proved at capture and not at restore, so the worker half of the admission whitelist is untested

- **Where:** `tenancy/tenancyjobs/jobs.go:88` (`Lookup(ctx, reference,
  tenancy.ClassDurable)`); the tests are
  `tenancyjobs/durable_test.go:141-169`
  (`TestWorkEnqueuedBeforeASuspensionDoesNotRunAfterIt`) and `:171-212`
  (`TestAProducerRefusesToCaptureAScopeTheDurableClassDoesNotAdmit`).
- **What:** changing the restorer to ask for `tenancy.ClassRead` instead of
  `tenancy.ClassDurable` — **SURVIVED**. Every authority in `durable_test.go`
  except the producer-side one is built by `support_test.go:12-34`, which passes
  no `Admission` and therefore gets `AdmitAll(Active)`: read, write and durable
  are the same list, so the three classes are indistinguishable in every restore
  test. The producer side has the asymmetric-admission test
  (`TestAProducerRefusesToCaptureAScope…`, which does build
  `Admit(ClassRead…).Merge(Admit(ClassWrite…))`); the restore side has none.
- **Why this severity:** INV-15 says the whitelist is "re-evaluated at durable
  execution time", and UC-53/UC-54 are about the worker, not the producer. The
  shipped defect is a deployment that suspends a tenant, keeps it readable, and
  finds its queue backlog still executing — which is the exact scenario
  `TestWorkEnqueuedBeforeASuspensionDoesNotRunAfterIt` is named for and cannot
  currently distinguish from a read check.
- **Why this timing:** one authority literal in the existing table closes it;
  every later durable adapter copies this test file's shape.
- **Close criteria:**
  - [ ] A restore test over an authority with
        `Admit(ClassRead, Active, Suspended).Merge(Admit(ClassWrite, Active))`
        and no durable admission for `Suspended`: the record refuses, with an
        `Active` control that restores.
  - [ ] Changing `ClassDurable` to `ClassRead` at `jobs.go:88` fails it.
- **Status:** open

### GAP-S7-T5 [medium][immediate] The object namespace's two bounds are answered by `storage`, not by the seam, so both assertions in the test that names them are vacuous

- **Where:** `tenancy/tenancystorage/storage_test.go:79-90`
  (`TestANamespaceWithNoScopeIsRefused`, the prefix assertions at `:84-89`);
  code at `tenancystorage/storage.go:13-15` (`MaxPrefixBytes = 30`,
  `digestBytes = 16`) and `:46-50`.
- **What:** three survivors:
  - deleting the whole prefix check from `namespaceOf` — **SURVIVED**;
  - deleting only `len(prefix) > MaxPrefixBytes` — **SURVIVED**;
  - `digestBytes` 16 → **4** — **SURVIVED**.

  The first two survive because `storage.ParseNamespace` refuses first:
  `validateNamespace` (`storage/validate.go:11-23`) rejects a leading `-`, which
  is what an empty prefix produces, and caps the namespace at 63 bytes, which
  `MaxPrefixBytes+1 + 1 + 32` exceeds. The seam's own bound and its own error
  message (`"an object namespace prefix is 1..%d bytes"`, which is the only
  `tenancy.ErrMalformed` a consumer can key on here) are never what refuses. Raise
  `MaxPrefixBytes` to 40 and the test still passes while a consumer following the
  documented bound gets a `storage` error at runtime.

  The third survives because 500 distinct tenants over a 32-bit digest collide
  with probability ~3e-5, far below what `TestAnObjectNamespaceIsInjectiveAndNo‐
  TenantsIsAPrefixOfAnothers` can see. The cache seam pins its equivalent
  (`MinPartitionBytes`, killed by `TestAPartitionTooNarrowToIdentifyATenantIs‐
  Refused`); the object seam does not pin its digest width at all. The
  implementation side of the same constants is `TENANCY_S7_GAPS.md` GAP-9.
- **Why this severity:** INV-18 is "namespace injectivity and non-containment",
  and the width of the digest is the only thing that makes it hold for a real
  tenant population. A future edit that trims the namespace to fit some provider's
  limit has no test standing in front of it.
- **Why this timing:** `MaxPrefixBytes` is an exported constant of a new public
  package; a consumer is already being told to read it.
- **Close criteria:**
  - [ ] The prefix assertions assert `errors.Is(err, tenancy.ErrMalformed)` and
        use a prefix that `storage.ParseNamespace` would otherwise accept, so the
        seam's bound is what refuses.
  - [ ] A test pins the emitted namespace's digest width (e.g.
        `len(namespace.Value()) == len(prefix)+1+2*digestBytes` with an explicit
        minimum), so shrinking `digestBytes` fails.
  - [ ] Both prefix mutations and the `digestBytes` 16→4 mutation each fail a test.
- **Status:** open

### GAP-S7-T6 [medium][immediate] The rewritten forgery matrix is three names for one case, and one of the names describes something it no longer does

- **Where:** `tenancy/tenancyjobs/durable_test.go:272-276` (the table),
  `:295-298` (`generationBytes`/`macBytes`), `:323-335` (`stripped`,
  `scrambled`), `:261-263` (the layout assertion).
- **What:** all three rows now derive from one honestly sealed token and differ
  only in how the MAC region is made wrong — zeroed, overwritten, or computed
  under another key. Under the one mutation they exist to catch (MS1, `Unseal`
  never compares the MAC) all three subtests fail together, which confirms none is
  vacuous but also confirms they are one case, not three. Specifically:
  - `"a hand-built plaintext claim"` no longer builds one. A hand-built claim is a
    short token — `[8-byte generation][reference]`, no MAC region — which
    `jobs.NewProtectedIdentityToken` accepts (its only bounds are non-empty and
    `MaxIdentityTokenBytes`) and which `Unseal`'s length guard at `seal.go:65` is
    what refuses. `stripped` is a full-length token with a zero MAC, i.e. the same
    case as `scrambled`. The length guard is exercised only in the core
    (`TestARecordTooShortToCarryAClaimIsRefused`), never through the seam.
  - the layout assertion `len(honest) != generationBytes+macBytes+len(...)` is a
    length-only change detector: it holds for any layout with a 40-byte header, so
    it does not establish that `stripped`/`scrambled` are clobbering the MAC
    rather than the generation. It also re-declares the core's unexported
    `sealGenerationBytes`/`sealMACBytes` in another package, which is a test
    depending on a private layout across a boundary — the thing the split was
    supposed to stop.
- **Why this severity:** the forgery matrix is the headline test for INV-14 at the
  seam. Three rows that collapse to one case read as three times the coverage they
  are, and the two cases that are actually missing (an edited claim under a valid
  MAC — GAP-S7-T1 — and a short plaintext token at the seam) are exactly the two
  the row names promise.
- **Why this timing:** the names are what the next reader trusts; fixing the
  matrix and fixing GAP-S7-T1 are the same edit.
- **Close criteria:**
  - [ ] `"a hand-built plaintext claim"` builds `[generation][reference]` with no
        MAC region and asserts the seam refuses it.
  - [ ] The rows for an edited claim (GAP-S7-T1) join the table.
  - [ ] The layout assertion either derives the offsets from an exported core
        constant or is replaced by an assertion that survives a layout change
        (e.g. the honest token round-trips and the forgeries do not).
- **Status:** open

### GAP-S7-T7 [medium][deferred] `Sealer.mac`'s field-count prefix is credited by the doc comment and pinned by nothing

- **Where:** `tenancy/seal.go:34-40` (the comment: "Each is length-prefixed **and
  the count is sealed with them**, because a token whose fields can be slid past
  one another is a token that authenticates a different record") and `:85-87`;
  the test is `tenancy/seal_test.go:119-140`, named
  `TestBindingFieldsCannotBeSlidPastOneAnother`.
- **What:** deleting the count write — **SURVIVED**. All five rows of the sliding
  table ("the boundary moved", "the fields concatenated", "an empty field
  appended", "the fields swapped", "nothing bound at all") differ in the
  length-prefixed body as well, so the length prefixes alone refuse them. The
  count is defence in depth against a field whose encoding can be read as the
  start of the generation field, which is constructible but not with these inputs.
  The analogous omission in `grantBinding` is a live implementation defect —
  `TENANCY_S7_GAPS.md` GAP-7.
- **Why this severity:** a comment that names a mechanism no test exercises is how
  the mechanism gets removed in a cleanup. Not shipped-wrong today.
- **Close criteria:**
  - [ ] A row whose binding list has a different arity but an identical
        length-prefixed body (constructed, not asserted by hand-waving), or the
        comment is narrowed to what the length prefixes actually buy.
- **Status:** open

### GAP-S7-T8 [medium][deferred] Neither `tenancyjobs` constructor is tested for the authority it is supposed to refuse, and the untenanted path has no test at all

- **Where:** `tenancy/tenancyjobs/jobs.go:15-24` (`ContextProvider`), `:58-64`
  (`IdentityRestorer`), `:77-79` (the non-tenant branch). Coverage: 66.7 % and
  75 % respectively — the refusal branches are the uncovered ones.
- **What:** three survivors:
  - deleting `if provenance.IsZero() || epoch.IsZero() { return nil,
    tenancy.ErrMalformed }` — **SURVIVED** (no test constructs a provider without
    them);
  - `IdentityRestorer` ignoring `authority.Sealer()`'s error — **SURVIVED** (no
    test constructs a restorer over a keyless authority; the core's
    `TestADeploymentWithNoDurableKeySealsNothing` proves `Sealer()` refuses but
    nothing proves the seam propagates it);
  - turning the `request.Scope() != jobs.ContextTenant` pass-through into a
    refusal — **SURVIVED** (no test enqueues work that needs no partition; UC-58
    is untouched). This last is `TENANCY_S1_TEST_GAPS.md` M19, carried over.
- **Why this severity:** the first two are start-up refusals whose whole job is to
  turn a wiring mistake into a boot failure; the third means a deployment mixing
  tenanted and untenanted jobs has no evidence the untenanted ones still run.
- **Close criteria:**
  - [ ] `ContextProvider`/`IdentityRestorer` over an authority with no
        `DurableKey` answers `ErrUntrusted`, and over a nil authority `ErrNoScope`.
  - [ ] `ContextProvider` with a zero provenance or a zero epoch answers
        `ErrMalformed`.
  - [ ] A job declared without `PartitionTenantRequired` restores with no tenant
        bound and runs.
- **Status:** open

### GAP-S7-T9 [medium][deferred] The import-time activation test greps the source for one spelling of a goroutine

- **Where:** `scripts/tenancy_test.go:89-107`
  (`TestTheExtensionDoesNothingWhenItIsMerelyImported`), the pattern at `:102`.
- **What:** the check is `grep -E '^func init\(|^var .*= *(os\.Getenv|regexp\.MustCompile)|go func\('` over the extension's `.GoFiles`. Adding
  `go built.watch()` to `tenancy.New` — a goroutine started by construction,
  spelled as a method value — **SURVIVED**. The `init()`, the package-level
  `os.Getenv` and the `go func(` spellings are all caught, so the test is not
  worthless; it is a text matcher standing in for a behavioural property.
  INV-34's own "How to check" asks for a probe ("no goroutine is started by
  construction"), and `TENANCY_S1_TEST_GAPS.md` GAP-T14's close criterion asked
  for `runtime.NumGoroutine()` specifically. The grep was accepted instead.
- **Why this severity:** the property INV-34 protects is the one the whole
  extension architecture rests on, and the test that guards it is a lexical
  approximation that a perfectly ordinary spelling walks past. It is also a test
  of source text rather than behaviour, so it will need rewriting the first time
  the extension legitimately contains the string `go func(` in a test-free file.
- **Close criteria:**
  - [ ] A Go test asserts `runtime.NumGoroutine()` is unchanged across
        `tenancy.New(...)` and across each seam's constructor (with a settle
        loop, not a sleep).
  - [ ] `go built.watch()` in `New` fails it.
- **Status:** open

### GAP-S7-T10 [medium][deferred] The split multiplied the test helpers rather than sharing them, and two copies differ in a way worth knowing about

- **Where:** the resolver fake exists four times under two names —
  `tenancy/support_test.go:5-17` (`knownTenants`, in-package),
  `tenancy/vocabulary_test.go:127-141` (`directory`),
  `tenancy/tenancydb/support_test.go:65-79` (`directory`),
  `tenancy/tenancyjobs/support_test.go:42-56` (`knownTenants`) — all four
  identical in behaviour. `reference`/`mustReference`, `authorityFor`,
  `activeAuthority`, `switchable`, `bound`, `boundTo`, `manyTenantAuthority`,
  `epochOf` and the literal `durableTestKey` are each duplicated two or three
  ways. `fixedAuthority` exists three times and **differs**:
  `tenancystorage/storage_test.go:13-34` verifies `ClassRead` and passes no
  `DurableKey`, `tenancyjobs/support_test.go:12-34` verifies `ClassDurable` with a
  key, and `tenancycache/cache_test.go:12-29` returns no scope at all.
- **What:** the two `fixedAuthority` copies that return a scope swallow the
  `Verify` error unless the state is `Active` (`if err != nil && state ==
  tenancy.Active`), so they hand back a **zero `tenancy.Scope`** for any other
  state. In `tenancyjobs` that is deliberate — `authorityOnly` discards the scope.
  In `tenancystorage` the branch is dead (every call passes `Active`), so a future
  test that passes `Suspended` there would silently receive a zero scope and
  assert against `ErrNoScope` while believing it was asserting something about
  lifecycle. That is a trap, not a defect yet.
  `bound`/`boundTo` were copied into `tenancydb/support_test.go:56-63` and
  `:302-313` complete with their `t.Fatalf`, and are still called from inside
  goroutines at `database_test.go:416`, `:443` and `:480` —
  `TENANCY_S1_TEST_GAPS.md` GAP-T17, carried over verbatim into the new package.
- **Why this severity:** DRY inside one module is `architecture.md`'s bar and this
  is across six; the concrete risk is the divergence above and the fact that a fix
  to `boundTo`'s goroutine problem now has to be made twice.
- **Close criteria:**
  - [ ] One `tenancytest` (or `internal/tenancytest`) package holds the resolver
        fake, `reference`, `authorityFor`, `bound` and `epochOf`, and every seam
        test uses it.
  - [ ] `bound`/`boundTo` return an error instead of calling `t.Fatalf`, so the
        goroutine call sites report with `t.Errorf`.
  - [ ] `fixedAuthority` either fatals on any `Verify` error or documents why not,
        in one place.
- **Status:** open

### GAP-S7-T11 [medium][deferred] Both new seam packages test their derivation and neither tests its seam

- **Where:** `tenancy/tenancystorage/storage.go:28-34` (`Store`) is at **0.0 %**
  coverage; `tenancy/tenancycache/cache.go:33-35, 56-58` (`Key.Unwrap`,
  `Key.Scope`, `Key.String`, `Partitioned`) are at **0.0 %**.
- **What:** every assertion in `storage_test.go` is against `namespaceOf`, and
  every assertion in `cache_test.go` is against `Partition[string]()` called
  directly. No test builds a `storage.Store` and shows a listing stays inside the
  namespace (UC-62), builds a `cache.Scope` through `Partitioned` and shows an
  invalidation does not cross partitions (UC-64), or runs the slow-loader race
  that INV-19 and UC-63 ask for by name ("loader invocation count equals the
  number of distinct partitions, not 1"). This is `TENANCY_S1_TEST_GAPS.md`
  GAP-T19 carried through the split unchanged; the split is the moment it got
  cheaper to close, because each seam now has its own package and its own
  dependency.
- **Why this severity:** INV-19 is the one concurrency-only cross-tenant read in
  the extension, and the spec says outright that no serial test can find it. There
  is no such test.
- **Close criteria:**
  - [ ] A `cache.Scope` built through `Partitioned` with a deliberately slow
        loader and two tenants under `-race`: loader invocations == 2, values do
        not cross, hundreds of goroutines, zero cross-assignments.
  - [ ] A `storage.Store` built through `Store` where tenant A lists and sees none
        of tenant B's objects, with prefixes `""`, `"/"` and `"../"`.
- **Status:** open

### GAP-S7-T12 [low][deferred] Fourteen S1 survivors crossed the split unchanged and are still green on a broken implementation

- **Where:** re-confirmed by mutation against the post-split tree; each is already
  open in `TENANCY_S1_TEST_GAPS.md` and in `TENANCY_PLAN.md` `## Debt`.
- **What:** `Lookup` accepting a resolution that names another tenant
  (`authority.go:98`, M3); `mint` accepting an invalid resolution
  (`authority.go:108`, M24); `Admission.inconsistent` never reporting
  write-without-read (`lifecycle.go:109`, M5); `through.Frozen` freezing nothing
  (`tenancyrow/row.go:171`, M7); `Policy` accepting a nil authority
  (`row.go:216`, M44); `Column` no longer panicking on an unknown model field
  (`row.go:52`, M42); `Directory.Close` not marking the directory closed
  (`tenancydb/database.go:264`, M9); `Accept` keeping duplicates (M13), dropping
  the cohort bound (M12) and ignoring cancellation between members (M14);
  `Purpose.valid` dropping its length and control-character rules (M21b);
  `From()` handing back a zero scope (`context.go:79`, M23); the object-namespace
  prefix bound (M15, now GAP-S7-T5); `IdentityRestorer` accepting a keyless
  authority (M18, now GAP-S7-T8).
- **Why this severity:** none is new and none is a leak the closed rows leave
  open; recorded here so the S7 round's own mutation evidence is complete and so
  nobody re-derives them a third time.
- **Close criteria:**
  - [ ] Each row is closed under its existing `TENANCY_S1_TEST_GAPS.md` gap, or
        moved to `## Debt` with a reason.
- **Status:** open

---

## Mutation log

81 mutations, each applied to the working tree, run with
`go test -count=1 ./tenancy/...` (plus `./scripts/` where the mutation is
structural), and restored from a checksummed snapshot verified before and after
every step. Six further probes ran a single named test under one mutation to ask
whether that test can fail on the behaviour it names. Mutations that did not
compile were reworked until they did before being scored.

### Survived — the suite stayed green on a broken implementation

| # | Mutation | Where | Consequence if shipped |
|---|---|---|---|
| MS18 | the durable MAC no longer covers the tenant reference | `seal.go:94` | **a record sealed for A executes as B** — reproduced end-to-end |
| MS17 | the durable MAC no longer covers the generation | `seal.go:91-93` | **the restore fence is defeated by editing 8 plaintext bytes** — reproduced |
| MS23 | the restorer asks for `ClassRead`, not `ClassDurable` | `tenancyjobs/jobs.go:88` | a suspended-but-readable tenant's backlog keeps running |
| MS3 | `Sealer.mac` drops the field-count prefix | `seal.go:85-87` | the sliding defence the comment names is gone |
| MS24 | `ContextProvider` accepts a zero provenance or epoch | `tenancyjobs/jobs.go:20` | a queue record with no provenance |
| MS27 | `IdentityRestorer` accepts an authority that cannot seal | `tenancyjobs/jobs.go:59` | a worker wired with no durable key fails at the first job instead of at boot |
| MS26 | the restorer refuses untenanted work instead of passing it through | `tenancyjobs/jobs.go:77` | untenanted jobs stop running (M19) |
| MS29 | the object-namespace prefix bound is deleted | `tenancystorage/storage.go:46` | `MaxPrefixBytes` is decorative (M15) |
| MS29b | only the upper prefix bound is deleted | same | same |
| MS32 | the object namespace takes 4 digest bytes instead of 16 | `tenancystorage/storage.go:14` | a 32-bit namespace space |
| MS38 | prefix and digest are joined without a separator | `tenancystorage/storage.go:50` | prefix containment across differing prefixes |
| MS75 | construction starts a goroutine spelled `go built.watch()` | `authority.go:59` | INV-34 breached past the grep |
| MS11 | `Lookup` accepts a resolution naming another tenant | `authority.go:98` | a resolver bug becomes cross-tenant authority (M3) |
| MS12 | `mint` accepts an invalid resolution | `authority.go:108` | `Verify` returns a scope at `Epoch(0)` (M24) |
| MS16 | `Admission.inconsistent` never reports write-without-read | `lifecycle.go:109` | the construction-time refusal disappears (M5) |
| MS41 | `Policy` accepts a nil authority | `tenancyrow/row.go:217` | wiring failure deferred to first request (M44) |
| MS42 | `Column` no longer panics on an unknown model field | `tenancyrow/row.go:53` | a typo in the composition root (M42) |
| MS45 | `through.Frozen` freezes nothing | `tenancyrow/row.go:171` | repointing the FK moves a row between tenants (M7) |
| MS51 | `Directory.Close` does not mark the directory closed | `tenancydb/database.go:267` | `Borrow` after `Close` (M9) |
| MS53 | `Accept` keeps duplicate cohort members | `grant.go:98` | a retry double-applies a tenant (M13) |
| MS54 | `Accept` no longer bounds the cohort size | `grant.go:86` | a 10 M-member grant (M12) |
| MS55 | `Each` ignores cancellation between members | `grant.go:140` | a cancelled cohort run continues (M14) |
| MS62 | `From()` hands back a zero scope as if carried | `context.go:79` | a zero scope escapes the accessor (M23) |
| MS63 | `Purpose.valid` drops the length and control-character rules | `grant.go:36-44` | a purpose carrying a newline reaches a label (M21b) |

### Named tests that cannot fail on the behaviour they name

| Test | Mutation it survives |
|---|---|
| `TestABulkMutationReachesTheDatabaseNarrowed` (`row_test.go:115`) | `column.Narrow` → `crud.Eq("Total", 424242)` — no tenant predicate at all |
| `TestAnUnscopedBulkMutationIsRefusedRatherThanWidened` (`row_test.go:245`) | the same; and no bulk statement is ever issued |
| `TestARowOwnedThroughARelationIsNarrowedByThatRelation` (`row_test.go:274`) | `through.Narrow` → a constant foreign tenant |
| `TestAPreloadOfADeclaredRelationCarriesTheTenant` (`row_test.go:324`) | `column.Relations` → a constant foreign tenant |
| `TestANamespaceWithNoScopeIsRefused` (`storage_test.go:79`) | the seam's whole prefix bound deleted — `storage.ParseNamespace` refuses first |
| `TestTheExtensionDoesNothingWhenItIsMerelyImported` (`scripts/tenancy_test.go:89`) | `go built.watch()` in `New` |

(`TestEveryVerbNarrowsToTheScopesOwnTenantAndNoOther` catches the first two
mutations on its own statement, so the product is not broken; the tests named for
those behaviours are.)

### Caught — the suite went red, on a test that names the behaviour

| # | Mutation | Test that caught it |
|---|---|---|
| MS1 | `Unseal` never compares the MAC | `TestAForgedDurableRecordEntersNoHandler` (all 3 subtests), `TestBindingFieldsCannotBeSlidPastOneAnother`, `TestARecordAnotherDeploymentSealedIsNotRead`, `TestADurableRecordCannotBeReplayedIntoAnotherJobOrQueue` |
| MS2 | `Seal` skips `minted`, so any scope is sealable | `TestOnlyAScopeThisAuthorityMintedIsSealed` |
| MS4 | the seal MAC ignores the binding fields | `TestBindingFieldsCannotBeSlidPastOneAnother`, `TestADurableRecordCannotBeReplayedIntoAnotherJobOrQueue` |
| MS5 | `Scope.Digest` drops the generation | `TestTheDigestSeparatesTenantsAndGenerations`, `…ReadsNoneOfThePreviousOnesObjects`, `…Values` |
| MS6 | `Sealer()` accepts an authority with no durable key | `TestADeploymentWithNoDurableKeySealsNothing` |
| MS7 | `Unseal` drops the minimum-length guard | `TestARecordTooShortToCarryAClaimIsRefused` |
| MS8 | `bind` stops mixing `Origin` in | `TestTheOriginIsPartOfTheBindingAndNotJustTheSalt` |
| MS9 | `boundTo` stops comparing the binding | 5 tests across core, cache and storage |
| MS10 | `Authority.Scope` loses its nil-receiver guard | `TestASeamWiredWithNoAuthorityRefusesRatherThanPanics` (both seams) |
| MS13 | the unit-of-work pin ignores the generation | `TestTheUnitOfWorkPinCoversTheGenerationAsWellAsTheTenant` |
| MS14 | `Classify` hands the resolver's own error back | `TestAResolverFailureNeverTravelsBackAsText` |
| MS15 | `Admission.Admits` admits every state | 8 tests across all five packages |
| MS19/20/21 | `record()` binds nothing / drops the queue / drops the job name | `TestADurableRecordCannotBeReplayedIntoAnotherJobOrQueue` |
| MS22 | the restorer stops comparing the generation | `TestWorkEnqueuedBeforeASuspensionDoesNotRunAfterIt` |
| MS25 | `Capture` asks for the read class | `TestAProducerRefusesToCaptureAScopeTheDurableClassDoesNotAdmit` |
| MS28 | the object namespace drops the digest | `TestAnObjectNamespaceIsInjective…`, `…ReadsNoneOfThePreviousOnesObjects` |
| MS30/MS34 | either seam drops its zero-scope refusal | `TestANamespaceWithNoScopeIsRefused`, `TestAPartitionWithNoScopeIsRefused` |
| MS31/MS35 | either seam ignores the class it was asked for | `TestANamespaceIsRefusedWhenTheClassIsNot`, `TestACacheKeyIsRefusedWhenTheClassIsNot` |
| MS33/MS36 | the partition drops its floor / ignores the budget | `TestAPartitionTooNarrowToIdentifyATenantIsRefused` |
| MS37 | `Keyed` keeps the key and drops the scope | 3 cache tests |
| MS39 | the row narrowing names a constant foreign tenant | `TestEveryVerbNarrowsToTheScopesOwnTenantAndNoOther`, `TestNarrowingHoldsWhicheverOrderTheExtensionsAreListedIn` |
| MS40 | `classOf` answers `ClassRead` for every action | `TestTheRepositorySeamAsksForTheClassOfTheVerb` |
| MS43 | `relationScopes` narrows nothing when relations are declared | `TestAPreloadOfADeclaredRelationCarriesTheTenant` + control |
| MS44 | the ownership column is no longer frozen | `TestAMutationCannotMoveARowBetweenTenants` |
| MS46 | `Apply` never refuses | `TestACreateForAnotherTenantIsRefused…`, `TestValidateRefusesACreateThatOmitsOwnership` |
| MS47/MS52 | the directory's capacity bound / the expiry sweep is removed | 3 directory tests |
| MS48/MS49 | the factory / the fence is handed the zero scope | `TestTheFactoryAndTheFenceAreToldWhichTenantIsAsking` |
| MS50 | the binding cache key drops the generation | `TestARotatedGenerationDoesNotReachTheBindingItReplaced` |
| MS56/MS57 | either of the grant's two class guards is removed | `TestAReadGrantCannotWriteAtEitherGuard` |
| MS58 | `permittedByGrant` drops the deadline | `TestAContextCarriedOutOfAnExpiredGrantStopsWorking` |
| MS59 | `Each` no longer refuses to run inside bound work | `TestACohortRunFromInsideABoundRequestIsRefusedRatherThanEmpty` |
| MS60/MS61 | the grant binding drops the cohort / the deadline | `TestEveryPartOfAGrantIsInsideItsBinding` |
| MS64/MS65 | `Reference.valid` drops its character / length rules | `TestAReferenceIsRefusedWhenItCouldNotSurviveAKeyOrANamespace` |
| MS66/MS67 | `Class.Valid` / `Lifecycle.Valid` accept undeclared values | `TestNothingCarryingATenantRendersIt`, `TestALifecycleNobodyDeclaredStillNamesItselfSafely` |
| MS68 | `OutcomeFor` answers `ok` for every refusal | `TestTheOutcomeVocabularyIsClosed`, `TestOneCohortMemberFailingLeavesTheRestResumable` |
| MS69/MS70 | `Scope.MarshalJSON` / `Reference.String` render the tenant | `TestNothingCarryingATenantRendersIt` |
| MS71/MS72 | the core imports `cache` / `tenancydb` imports `security` | `TestNoTenancyPackageCostsMoreThanTheSeamItNames` |
| MS73/MS76 | an `init()` / a package-level `os.Getenv` appears | `TestTheExtensionDoesNothingWhenItIsMerelyImported` |
| MS74 | construction starts a `go func(){}()` | same |
| MS77/MS78 | `Scope` grows an exported field / a pointer field | `TestAScopeHasNothingAnybodyCanWriteThrough` |
| MS79/MS80 | `Policy.Scope` / `Policy.Inspect` uses a fabricated reference | `TestEveryVerbNarrowsToTheScopesOwnTenantAndNoOther` + 3 more |

### What this round would fix first

0. **GAP-S7-T0.** `./scripts/` is red: `D-117` cites two tests that do not exist
   for two properties the seal does not have. Green first, then the properties.
1. **GAP-S7-T1.** The durable seal authenticates a record and no test says which
   record. Two reproductions, one of them a handler running as the wrong tenant.
2. **GAP-S7-T2 + GAP-S7-T3.** GAP-T1/T2 were closed for the root read verbs and
   nowhere else: the bulk statement and both relation paths still cannot tell
   "narrowed to my tenant" from "narrowed to somebody else's".
3. **GAP-S7-T4.** The durable admission class is checked twice in the code and
   proved once in the tests, on the half that does not survive a restart.

### Verification of this round

The tree was snapshotted with `md5sum` over all 33 files under `tenancy/` before
the campaign and compared file-by-file after it: identical. `gofmt -l tenancy` is
silent, `go test -count=1 ./tenancy/...` is green, and no probe file remains
(`find … -name 'zz_probe_test.go'` is empty). No implementation, test or plan
file was left edited.

---

## Round 2 — dispositions — 2026-09-06

| Finding | Disposition |
|---|---|
| T0 D-117 claims two tests that do not exist | **closed** — both now exist and both behaviours are implemented; `go test ./scripts/` green. The concurrent edit the round caught was mid-fix, and the checker that reported it is the one strengthened under GAP-6. |
| T1 nothing says what the seal's MAC covers | **closed** — `TestTheClaimARecordCarriesIsWhatItsMACCovers` presents a MAC this deployment really produced over an edited claim: the tenant rewritten in the tail, and the generation rewritten in the header. Both reported survivors — the reference dropped from the MAC, the generation dropped — are now killed. |
| T2 both bulk tests cannot fail | **closed** — the assertion is on the *leading* conjunct and the argument bound to it, not on the clause containing a column name the fixture row supplies. `TestABulkMutationReachesTheDatabaseNarrowed` covers `DeleteAll` and `UpdateAll`; `TestAnUnscopedBulkMutationIsRefusedRatherThanWidened` now asserts the unscoped call refuses with zero statements and keeps a scoped control. Mutation `Narrow → Eq("total", 424242)`: killed by three tests. |
| T3 the relation half accepts a foreign constant | **closed** — the preload asserts the child statement binds the scope's own tenant; the `Through` test asserts *every* argument is this tenant, because that strategy contributes both the scope predicate and the relation scope and one of the two naming somebody else is exactly the leak. Both mutations killed. |
| T4 the durable class is proved at capture only | **closed** — `TestTheDurableClassIsAskedAgainWhenTheRecordIsRestored` restores over an authority that keeps a suspended tenant readable and admits it no durable work. Mutation `ClassDurable → ClassRead` at the restorer: killed. |
| T5 the namespace bounds are answered by `storage` | **closed** — see GAP-9/GAP-10 in the architecture round. |
| T6 the forgery matrix is three names for one case | **closed** — "a hand-built plaintext claim" is now a claim with no MAC at all, shorter than a record, which is what exercises the length guard through the seam; the wiped-MAC case is named for what it is. |
| T7 the field-count prefix is credited and pinned by nothing | **closed by removal** — with every field length-prefixed the count added nothing for a fixed field list, and it was untestable. It is gone from `Sealer.mac`, the comment no longer credits it, and it says instead what a second seam sharing the key must do: bind something that names it, as the job seam binds its queue. The count stays in `grantBinding`, where a tenant may legitimately be called "write" and the collision is real and now tested. |
| T8 the `tenancyjobs` constructors and the untenanted path | **closed** — `TestASeamWithNothingToAuthenticateWithRefusesToExist` (five wiring mistakes, two controls) and `TestWorkThatNeedsNoTenantRestoresWithNoneBound`. All three reported survivors killed. |
| T9 the activation grep knows one spelling of a goroutine | **partly closed** — the pattern also matches `go name(...)`, and the test now walks every package under `tenancy/`. A real check would parse rather than grep; recorded in Debt. |
| T10 the split multiplied the helpers | **partly closed** — the two copies that swallowed a `Verify` error for a non-active state are gone: `fixedAuthority` fails the test, and the tolerant path is `authorityOnly`, which verifies nothing. The `t.Fatalf`-from-a-goroutine copies are pre-existing S1 debt and stay recorded there. |
| T11 neither seam package tests its seam | **deferred** — a `storage.Store` listing and a `cache.Scope` loader race under `-race` are the two tests that would close INV-19 and UC-62/63/64. Real, and larger than this change; recorded in the plan's Debt with the shape each needs. |
| T12 fourteen S1 survivors crossed the split | **deferred** — carried into the plan's Debt unchanged. They are S1's, not the split's, and the split neither fixed nor worsened them. |
