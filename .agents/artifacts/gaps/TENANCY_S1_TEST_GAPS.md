# TENANCY — S1 (the `tenancy` extension and the `crud` kernel change) — TEST GAPS

## Round 1 — test suite — 2026-09-05

**What was graded.** `tenancy/{scope,vocabulary,row,seams,durable,database,grant,grant_binding,revalidate}_test.go`,
`crud/unscoped_test.go`, `crud/decorators/{security,faults}/unscoped_test.go`,
`test/integration/tenancy_test.go`, against `tenancy/*.go` and the S0 change in
`crud/{executor,errors}.go`, `crud/decorators/security/security.go`,
`crud/decorators/faults/faults.go`.

**Revision.** The working tree was being edited while this round ran. Everything
below was re-verified against the snapshot taken at **21:48**, which contains
`tenancy/revalidate_test.go`, `TestOneLeaseReleasedFromManyGoroutinesCountsOnce`,
`TestABorrowerThatGivesUpDoesNotFailTheOnesBesideIt`,
`TestAWaiterHonoursItsOwnDeadlineRatherThanTheOpeners`,
`TestACohortRunFromInsideABoundRequestIsRefusedRatherThanEmpty` and
`TestTheObjectSeamRefusesAScopeAnotherAuthorityMinted`. Findings that were closed
by edits landing during the round are recorded as closed rather than dropped.

**Runs.**

```
go test -race -count=1 ./tenancy/...                     ok   1.18s wall (0.15s test)
go test -race -count=2 ./tenancy/...                     ok   1.36s
go test -race -count=1 -shuffle=on ./tenancy/...  (x2)    ok   both
go test -race ./crud/ ./crud/decorators/{security,faults} ok   ~1.0s each
go vet ./tenancy/...                                      clean
go test -tags=integration -run 'TestATenantOwned…|TestALifecycle…' ./integration/...  ok  1.1s
```

The integration run needed `VV_PG_DSN=…:55433`: **port 55432, which
`docker-compose.yml` and `corpus.PostgresDSN()` both name, is currently bound by
another project's PostgreSQL** (`analyzer4-postgres-1`). `docker compose up -d
--wait` reports `vv-postgres-1 Healthy` and the suite then fails SASL auth
against the *other* server. This is the same trap the plan's "What green means
here" section describes; it is still live, and it means the integration evidence
cannot be reproduced by following the documented command.

**Mutation campaign.** 58 deliberate breakages, each applied to a copy of the
tree, run, and reverted. 26 caught, 32 survived. The full log is at the end; it
includes the eight rows of the plan's own `## Mutation evidence` table that I
re-ran independently — **all eight held**, so that table is honest as far as I
checked it.

**What is genuinely good, in one line each.** `TestNoVerbRunsWithoutAVerifiedScope`
(15 verbs × sentinel × zero statements) is the strongest test in the package;
`TestAPreloadOfADeclaredRelationCarriesTheTenant` carries a real control that
asserts the leak exists without the declaration; `TestEveryPartOfAGrantIsInsideItsBinding`
is a six-way tamper matrix with a "the authority holds the honest grant" control;
`TestOnlyAnAdmittedLifecycleReachesWork` enumerates all six declared states plus
`LifecycleUnknown` plus an invented `Lifecycle(200)` across all three classes;
the new `TestRevalidationStopsTheNextVerbAfterTheControlPlaneMoves` closes what
was the largest hole in the package; failure messages are in house style
throughout and errors are compared with `errors.Is` against sentinels, never by
string; no `t.Parallel()` anywhere; `-count=2` and `-shuffle=on` are clean.

Gap IDs are prefixed `GAP-T` to keep them distinct from `TENANCY_S1_GAPS.md`,
whose GAP-1..GAP-38 are all still `open` and several of which this round
re-confirms with mutation evidence.

---

### GAP-T1 [critical][immediate] Nothing in the unit suite asserts *which tenant* the narrowing predicate names

- **Where:** `tenancy/row_test.go:60-94` (`TestEveryReadCarriesTheTenantIntoTheStatement`),
  `:258-278` (`TestATenantSeesEveryRowItOwns`), `:280-320`; the code is
  `tenancy/row.go:71-77` (`column.Narrow`) and `:219-226` (`Policy.Scope`).
- **What:** every assertion about narrowing is on the *SQL text*, never on the
  bound argument. `recorder.Last().Args` is never inspected by any test in
  `tenancy/`. Two mutations survive the entire unit suite:
  - `Policy.Scope` resolves the scope, throws it away and narrows on
    `ParseReference("any-tenant")` — **SURVIVED**;
  - `column.Narrow` returns `crud.Eq(this.owner.Name, "globex-1a2b")` — a
    hardcoded foreign tenant — **SURVIVED**.
  Only `test/integration/tenancy_test.go` catches the first, and only because it
  runs against a live PostgreSQL — which, per the preamble, currently cannot be
  reached by the documented command.
- **Why this severity:** the shipped defect is "every tenant reads and writes one
  particular tenant's rows", the single worst outcome this extension exists to
  prevent, and the unit suite is green for it. The package's own doc comment
  (`row.go:42-48`) calls the undeclared-relation case "a cross-tenant read
  reachable from unprivileged input"; this is the same failure one layer up and
  nothing pins it.
- **Why this timing:** every later section (`Through`, the directory, the grant
  loop) reuses `Policy` and inherits the blind spot; S2's evidence is cited by
  the published profile.
- **Close criteria:**
  - [ ] `TestATenantSeesEveryRowItOwns` asserts `recorder.Last().Args` equals
        `[]any{secretReference}`, not only the clause text.
  - [ ] A test binds two authorities for two different tenants over one recorder
        and asserts each one's statement carries its own reference as the bound
        argument.
  - [ ] Mutating `column.Narrow` to a constant foreign value fails at least one
        test in `go test ./tenancy/...`.
- **Status:** open

### GAP-T2 [critical][immediate] `TestEveryReadCarriesTheTenantIntoTheStatement` is vacuous for six of its eight verbs

- **Where:** `tenancy/row_test.go:87-91` — `if !strings.Contains(statement, "tenant_id")`.
- **What:** the assertion scans the *whole statement*, and every row-returning
  verb projects the owner column:
  `SELECT "id", "tenant_id", "number", "total" FROM "invoices" WHERE …`.
  With the narrowing replaced by `crud.Eq("total", 424242)` — no tenant predicate
  at all — the subtests behave like this:

  | subtest | result with narrowing removed |
  |---|---|
  | GetAll, First, Get, GetByID, Aggregate, DeleteAll | **PASS** |
  | Count, Exists | FAIL |

  `Aggregate` passes because the test itself supplies `crud.GroupBy("TenantID")`,
  which puts `tenant_id` in the statement regardless. `DeleteAll` passes because
  the recorder is pushed empty rows, so the `DELETE` is never issued at all — the
  only statement the subtest sees is the preceding `SELECT`.
  The sibling `TestAnUnscopedBulkMutationIsRefusedRatherThanWidened`
  (`row_test.go:201-216`) has the same defect twice over: with empty rows pushed
  no `UPDATE` is ever emitted, so the test named for bulk scoping inspects one
  narrowed `SELECT` whose projection contains `tenant_id` no matter what.
- **Why this severity:** this is the package's headline "every verb is narrowed"
  test. Six of eight subtests cannot fail, and the two verbs whose bulk statement
  the test is named after never run. A regression that dropped narrowing from
  `GetAll` ships green.
- **Why this timing:** the test is cited as S2 evidence for UC-19..21, UC-32 and
  INV-12; leaving it means later sections build on a coverage claim that is false.
- **Close criteria:**
  - [ ] The assertion uses the `WHERE` clause (`where(recorder)`, already in the
        file at `row_test.go:52-58`), not the whole statement.
  - [ ] `Aggregate` is asserted without `GroupBy("TenantID")` supplying the token
        the assertion looks for.
  - [ ] `DeleteAll` and `UpdateAll` push rows so the mutating statement is
        actually issued, and the assertion is made against that statement.
  - [ ] With `column.Narrow` returning a non-tenant predicate, every subtest fails.
- **Status:** open

### GAP-T3 [critical][immediate] The database-per-tenant tests never assert that the tenant reaches the source factory or the fence

- **Where:** `tenancy/database_test.go:41-70` (the `openings` double), `:164-192`
  (`TestADatabaseTheFenceDoesNotRecogniseIsRefused`); code at
  `tenancy/database.go:182-200`.
- **What:** two mutations survive the whole suite:
  - `this.sources.Source(opening, scope)` → `this.sources.Source(opening, Scope{})`
    — the factory is asked for **the zero scope** for every tenant — **SURVIVED**;
  - `this.fence(opening, source, scope)` → `this.fence(opening, nil, Scope{})` —
    the fence is handed a nil source and a zero scope — **SURVIVED**.
  The `openings` double records `scope.Reference().Value()` but no test ever
  compares the recorded keys against the cohort; it only counts them
  (`sources.count()`, `peakOpen()`). The fence in the one fence test
  (`:169-171`) ignores both of its arguments and returns a constant error, so the
  test proves "an error returned by a callback is propagated" — a tautology about
  a caller-supplied function — and nothing about selection fencing.
- **Why this severity:** INV-22 ("a resolved datasource is used only after being
  confirmed to be the one the scope names") and INV-6 ("exactly one capability")
  are the whole point of S4, and both are unproven: a directory that routes every
  tenant to one database passes. UC-43 ("the datasource mapping is wrong") has no
  test that models a wrong mapping at all.
- **Why this timing:** the plan's S4 `MISSING` names only "two real databases and
  outage behaviour"; the profile therefore reads as if selection correctness is
  proved against recording sources. It is not, and that sentence should not be
  written until it is.
- **Close criteria:**
  - [ ] `openings` records `(reference, epoch)` per open and a test asserts the
        recorded set equals the cohort that borrowed.
  - [ ] A `Sources` double returns tenant A's recorder for tenant B and a fence
        that compares the source identity to the scope refuses it, with a control
        proving the correct pairing succeeds.
  - [ ] The fence test asserts the fence received the same `crud.Source` the
        factory returned and a scope whose reference matches the borrower.
- **Status:** open

### GAP-T4 [high][immediate] INV-5 is proved for one invalid-scope class out of six

- **Where:** `tenancy/row_test.go:96-132` is the only verb × zero-effect matrix in
  the package, and it covers only the *absent* class.
- **What:** INV-5 asks for "each of the six invalid-scope classes × every
  supported verb, recorded effect count exactly zero". What exists:
  absent = 15 verbs (`ErrNoScope`); forged/untrusted = one `With` call
  (`scope_test.go:71-80`) and, since this round's edits, two object/cache calls
  (`seams_test.go:219+`) — no repository verb; stale = the revalidation test's 8
  verbs (`revalidate_test.go:101-110`) and nothing else; lifecycle-inadmissible =
  the revalidation test's 8 verbs plus `Verify`; unmapped and incompatible = no
  verb matrix at any seam. At the storage, cache and durable seams only the
  absent class has a matrix at all, and it is one call each.
- **Why this severity:** INV-5 is the invariant that makes every refusal more
  than cosmetic. A forged scope that refuses at `With` but is accepted by
  `Policy.Inspect` would ship undetected.
- **Why this timing:** the matrix is cheap to build once (a table of
  {class → authority-or-context} × the existing verb map) and expensive to
  retrofit after more seams land.
- **Close criteria:**
  - [ ] One table-driven test: invalid-scope class × seam × verb, asserting the
        sentinel and `len(recorder.Statements()) == 0` / zero backend calls.
  - [ ] A control row per class with a valid scope showing non-zero effects.
- **Status:** open

### GAP-T5 [high][immediate] Per-class admission is never proved at the repository seam

- **Where:** `tenancy/row.go:259-264` (`classOf`), `:228-234` (`Policy.Inspect`).
- **What:** replacing `classOf` with `func(crud.Action) Class { return ClassRead }`
  **survives the unit suite and the integration suite**. No test builds a
  tenant-owned repository over an admission that admits reads but not writes.
  `TestAdmissionIsDecidedPerOperationClass` (`scope_test.go:117-132`) proves it
  for `Verify` only; `TestACacheKeyIsRefusedWhenTheClassIsNot` and
  `TestADatabaseIsNotHandedToAScopeTheClassNoLongerAdmits` prove it for the cache
  and the directory. The row seam — the one the first release ships — has no such
  test, and `classOf` is exactly the code that a prior review round
  (`TENANCY_S1_GAPS.md` GAP-5) was opened to fix.
- **Why this severity:** a deployment that keeps a suspended tenant readable gets
  writes admitted too. INV-15 says "every enumerated state plus one invented
  state, against every verb"; the repository verbs are the ones missing.
- **Why this timing:** it is the untested half of an already-landed fix, and the
  fix is what the profile advertises.
- **Close criteria:**
  - [ ] A repository bound to an authority with
        `Admit(ClassRead, Active, Suspended).Merge(Admit(ClassWrite, Active))`
        over a `Suspended` tenant: reads succeed, every mutating verb answers
        `ErrInactive` and runs zero statements.
  - [ ] Reverting `classOf` to a constant fails that test.
- **Status:** open

### GAP-T6 [high][immediate] `Spec.Origin` — the deployment fence, UC-14 — has no test, and the test credited with it passes on the salt alone

- **Where:** `tenancy/scope.go:65-76` (`bind`), `tenancy/authority.go:18-25`;
  `tenancy/scope_test.go:71-80` (`"one minted by another deployment"`).
- **What:** `Origin` is set nowhere in `tenancy/`, `test/` or `crud/` — only in
  `_examples/tenancy-sharedrow/main.go:73`. Deleting `writeField(mac, origin)`
  from `bind()` **survives the unit suite and the integration suite.** The
  subtest named "one minted by another deployment" builds a second `Authority`
  in the same process, which gets a fresh random salt, so it would pass with the
  origin removed, with the origin ignored, or with the origin field deleted from
  the struct.
- **Why this severity:** UC-14 and INV-2's "a serialised scope from another
  deployment refuses" rest entirely on `Origin`, and the plan's S1 section asserts
  it in prose ("a scope value that travelled from another deployment does not
  verify"). Nothing in the suite would notice if it stopped being mixed in.
- **Why this timing:** it is a public field of `Spec` that consumers are being
  told to set; a field with no test is a field that silently becomes decorative.
- **Close criteria:**
  - [ ] A test builds two authorities that share a salt (or asserts on `bind`'s
        output directly, in-package) and differ only in `Origin`, and shows the
        scope from one is `ErrUntrusted` at the other.
  - [ ] Removing `writeField(mac, origin)` fails it.
- **Status:** open

### GAP-T7 [high][immediate] The grant's class bound is enforced in two places and tested in neither

- **Where:** `tenancy/grant.go:380-406` (`Each`, the `grant.classes.Admits`
  guard), `:476-488` (`permittedByGrant`).
- **What:** both guards can be deleted and the suite stays green:
  - removing `if !grant.classes.Admits(class, Active) { return nil, ErrGrantRequired }`
    from `Each` — **SURVIVED**;
  - removing the class half of `permittedByGrant` — **SURVIVED**.
  Every grant test accepts a grant with `ClassWrite` and runs it with
  `ClassWrite`. `TestEveryPartOfAGrantIsInsideItsBinding`'s
  `"a class the grant never named"` row proves only that the *MAC* covers the
  class field; it is satisfied by `holds()` failing, never by the class check.
- **Why this severity:** the classes field is the answer to
  `TENANCY_S1_GAPS.md` GAP-9 ("a grant's purpose bounds nothing"). Untested, a
  read-only monthly-billing grant can write, which is the defect the field was
  added to close.
- **Why this timing:** the fix is landed and advertised; the test is the only
  thing that keeps it landed.
- **Close criteria:**
  - [ ] `Accept(..., ClassRead)` then `Each(..., ClassWrite, …)` answers
        `ErrGrantRequired`, enters zero members, with a `ClassRead` control that
        enters all of them.
  - [ ] Inside a member's `work`, a `ClassWrite` verb under a read-only grant is
        refused (this exercises `permittedByGrant`, which `Each`'s guard hides).
- **Status:** open

### GAP-T8 [high][immediate] The unit-of-work pin is tested on the reference and not on the generation

- **Where:** `tenancy/context.go:28`; `tenancy/scope_test.go:237-261`.
- **What:** dropping `|| current.epoch != accepted.epoch` from `With`
  **SURVIVES**. `TestBindingASecondTenantInsideBoundWorkRefuses` only ever
  changes `current.Reference`. The epoch half is the fix recorded as
  `TENANCY_S1_GAPS.md` GAP-14 ("`Authority.With` pins on the reference only, so a
  generation change inside bound work is silent").
- **Why this severity:** a restore that advances the generation mid-transaction
  re-binds silently and the unit of work spans two generations — INV-13 and
  INV-17 both fall, and no test notices.
- **Why this timing:** same as GAP-T7: a landed fix with no test is a fix with a
  half-life.
- **Close criteria:**
  - [ ] The existing test grows a case that advances only `current.Epoch` and
        expects `ErrPinned`.
  - [ ] Removing the epoch comparison fails it.
- **Status:** open

### GAP-T9 [high][immediate] The durable token's binding to the queue and the job name has no test

- **Where:** `tenancy/jobs.go:139-151` (`tokenMAC`); `tenancy/durable_test.go:223-271`.
- **What:** removing the namespace digest and the definition from the MAC
  (`_, _ = namespace, definition`) **SURVIVES**. `TestAForgedDurableRecordEntersNoHandler`
  forges the key and the payload but always replays inside the same
  `(namespace, definition)`. Nothing replays a token minted for queue A / job A
  into queue B / job B.
- **Why this severity:** this is the fix for `TENANCY_S1_GAPS.md` GAP-26 ("the
  durable token is not bound to the record it travels in, and the doc claims it
  is"). Without the test, cross-queue and cross-definition replay — a real
  privilege move for anyone who can write one queue table — reopens silently.
- **Why this timing:** the doc already claims the property.
- **Close criteria:**
  - [ ] A token minted through queue/definition A is replayed through B and
        refuses, with a control proving the same token restores through A.
- **Status:** open

### GAP-T10 [high][immediate] Three of the suite's assertions cannot fail

- **Where:**
  1. `tenancy/scope_test.go:282-302` — `TestAScopeReadsTheSameFromEveryGoroutine`.
  2. `tenancy/row_test.go:201-216` — `TestAnUnscopedBulkMutationIsRefusedRatherThanWidened` (see GAP-T2).
  3. `crud/decorators/security/unscoped_test.go:33` — `if !strings.Contains(where, "id")`.
- **What:**
  1. It copies an immutable value type into 64 goroutines and asserts the copies
     read the same. There is no attempted mutation, no shared writer, and no
     mutation of the implementation can make it fail short of breaking
     `Reference().Value()`. It is credited with INV-3 ("a `-race` test with
     concurrent readers **and an attempted mutation**"); the attempted mutation is
     absent, and cannot be written because `Scope` has no exported field — which
     is the argument for asserting the *type property* (no exported setter, no
     pointer reachable) rather than running 64 goroutines that prove nothing.
  2. See GAP-T2: no bulk statement is ever issued, and the one statement seen
     contains `tenant_id` in its projection regardless.
  3. The gate's scope predicate is on `"tenant_id"`, which contains the substring
     `"id"`. Dropping the caller's own options from the probe entirely
     (`crud.ExistsUnscopedOf(this.Core, ctx, crud.Where(scope), relationNarrowing(rel))`)
     **SURVIVES** — the assertion that says "the caller's own narrowing was
     dropped" cannot detect that the caller's own narrowing was dropped.
- **Why this severity:** `CLAUDE.md` names this category explicitly ("a test that
  would still pass if the feature were deleted is a liability"), and one of the
  three is in the kernel change that the multitenancy, audit, event-sourcing and
  OTel roadmaps all list as their blocker.
- **Why this timing:** each is a one-line fix now and a false coverage claim
  forever otherwise.
- **Close criteria:**
  - [ ] (3) asserts on the parameter count or on `"id" = $` / the exact clause,
        and fails when the caller's options are dropped.
  - [ ] (1) is replaced by a test that asserts what INV-3 can actually assert, or
        deleted with a note in the decision doc saying why the type system covers it.
  - [ ] (2) is fixed under GAP-T2.
- **Status:** open

### GAP-T11 [high][immediate] The gate's unscoped probe is tested against a policy with no relation scopes and no authorizer

- **Where:** `crud/decorators/security/unscoped_test.go:16-48`; the fixture is
  `tenantPolicy = security.ScopeField[Doc, int64]("TenantID", tenantOf)`
  (`security_test.go:45`), which sets neither `RelationScopes` nor `Authorize`.
- **What:** two mutations to `gate.ExistsUnscoped` (`security.go:381-395`) survive:
  - dropping `relationNarrowing(rel)` from the probe — **SURVIVED**;
  - dropping `if err := this.authorize(ctx, Read); err != nil { … }` — **SURVIVED**.
  So the S0 kernel change is proved for the root scope only. The probe is an
  existence oracle by construction; an unauthorised caller getting an answer, or
  an answer that reaches an unnarrowed relation, is exactly the leak
  `ErrNoUnscopedExists` was introduced to close.
- **Why this severity:** INV-32 and INV-10 both run through this method, and the
  plan lists it as the kernel defect that blocked six roadmaps.
- **Why this timing:** S0 is kernel work other extensions are about to build on.
- **Close criteria:**
  - [ ] A probe test over a policy with `Authorize` refusing: `ExistsUnscopedOf`
        returns the refusal and runs zero statements.
  - [ ] A probe test over a policy with `RelationScopes`: the probe's statement
        carries the relation narrowing, with a control showing the leak without it.
- **Status:** open

### GAP-T12 [high][immediate] INV-9 holds for `crud.Core` and for nothing else

- **Where:** `crud/decorators/security/obligation_test.go:95-137` is the only
  method inventory in the repository.
- **What:** it reflects over `crud.Core` and fails when the interface grows —
  genuinely good, and tenancy inherits it because `tenancy.Repository` is
  `security.Gate(...)`. But it covers **only the 17 methods of `crud.Core`**. The
  optional executable capabilities are outside it: `ExistsUnscoped` (added by
  this very change), `InsertBatch`, `Restore`, `RestoreScoped`, `SaveScoped`,
  `DeleteScoped`. A new optional executable capability can therefore ship with no
  decorator decision and no failing test — which is the precise failure INV-9 and
  [[D-030]] exist to prevent, and the one S0 was opened for. The storage, cache
  and durable seams have no inventory at all; the plan's `## Debt` concedes this
  for those three but not for the optional half of the repository seam.
- **Why this severity:** "the next base-seam addition ships unprotected and
  nobody notices until it is used" — INV-9's own violation consequence, now
  reachable through the optional interfaces.
- **Why this timing:** `ExistsUnscoped` was just added through exactly this
  unguarded path.
- **Close criteria:**
  - [ ] The obligation table grows a second section enumerating the optional
        capability interfaces in `crud`, with a reflective check that fails when a
        new one appears.
  - [ ] A method inventory for `storage.Store`, `cache.Scope` and the two `jobs`
        provider interfaces, as the plan's Debt already promises.
- **Status:** open

### GAP-T13 [high][immediate] INV-31 / UC-7: no test composes tenancy with a second decorator, in either order

- **Where:** nothing in `tenancy/` or `test/integration/` builds a chain of two
  middlewares; the plan's matrix nonetheless claims `S2 + S6 → UC-7 (both
  orders), INV-31`.
- **What:** `security.Combine` appears in `tenancy/row.go:182` as a doc comment
  and in no tenancy test. UC-7 asks for {valid, absent, forged, stale, inactive}
  × both composition orders with zero effects in every invalid case.
- **Why this severity:** INV-31's violation consequence is "a composition-root
  reordering — a refactor nobody flags as security-relevant — silently disables
  narrowing", and the plan's own Debt entry for M4 cites "composition with an
  unrelated decorator in both orders (S2)" as something that *can* be proven now.
  It is not proven.
- **Why this timing:** the composition order is a public contract other
  extensions are about to depend on.
- **Close criteria:**
  - [ ] A test binds `tenancy.Repository` with a second, unrelated middleware in
        both orders and runs the five scope classes through a read and a write,
        asserting identical refusals and zero statements.
  - [ ] A test of `security.Combine(tenancy.Policy(...), appPolicy)` showing both
        scopes are ANDed and both immutable lists are unioned.
- **Status:** open

### GAP-T14 [high][immediate] INV-33 / INV-34: nothing checks that a base subsystem cannot import tenancy, and nothing probes import-time activation

- **Where:** `scripts/checks.sh:18` (`SUBSYSTEMS`), used only by `check_utils`;
  `check_deps` (`:40-61`) checks third-party imports of the root module only.
- **What:** the only structural fact that keeps `tenancy` out of the base today is
  `TIER0_STDLIB=(crud …)`, which constrains `crud` alone. Nothing would fail if
  `jobs`, `storage`, `cache`, `auth` or `port` imported `tenancy` tomorrow, and
  nothing checks the "no extension imports another extension" half of INV-33.
  INV-34 ("a probe enumerates process-global registration points before import,
  after import and after construction, and finds no new entry; no goroutine is
  started by construction") has no test at all — the plan's S1 checkpoint offers
  `go list -deps` on `./tenancy/` instead, which is the opposite direction.
- **Why this severity:** optionality is the property the extension-architecture
  roadmap is built on, and it is currently held by nobody remembering to break it.
- **Why this timing:** the second extension (`audit`, `eventpg`) is what this
  check exists to constrain, and it is next.
- **Close criteria:**
  - [ ] `check-deps` (or a new `check-extensions`) fails when any package outside
        `tenancy/` and `_examples/` imports `github.com/frostgrove/vv/tenancy`.
  - [ ] A Go test asserts `runtime.NumGoroutine()` is unchanged across
        `tenancy.New(...)` and that construction registers nothing global.
- **Status:** open

### GAP-T15 [high][immediate] INV-11 relation closure: depth 1 only, one relation kind, no live-database evidence

- **Where:** `tenancy/row_test.go:280-320`; the only relation fixture is
  `Invoice → Lines` (`has_many`, one hop). Re-states
  `TENANCY_S1_GAPS.md` GAP-29, still `open`.
- **What:** INV-11 asks for "a fixture seeds a cross-owned reference at depth ≥ 2;
  traversal returns nothing foreign, with a control subtest proving the leak
  exists when narrowing is removed". What exists is depth 1, `has_many` only, no
  many-to-many, no self-referencing relation, and no integration test that
  preloads anything. The preload assertion is also subject to GAP-T1: it checks
  the *column name* in the child statement, not the tenant value.
- **Why this severity:** INV-11's own text calls this "historically the exact
  failure this framework's decisions were written to prevent". `Column` takes a
  variadic `Relation{Path, Field}` where `Path` is dotted
  (`row.go:190-213` splits on `.`), so depth ≥ 2 is a supported input with no test.
- **Why this timing:** the relation declaration is the design change round 2
  forced; shipping it with depth-1 evidence repeats the round-2 finding.
- **Close criteria:**
  - [ ] A model with a two-hop path (`A → B → C`), a declared
        `Relation{Path: "B.C", Field: "TenantID"}`, and an assertion that the
        depth-2 statement is narrowed, with the undeclared control.
  - [ ] A many-to-many and a self-referencing case.
  - [ ] One integration test that preloads a cross-owned child on live PostgreSQL.
- **Status:** open

### GAP-T16 [medium][immediate] The new concurrency tests are timing-based and can pass vacuously

- **Where:** `tenancy/database_test.go:449-487`
  (`TestABorrowerThatGivesUpDoesNotFailTheOnesBesideIt`), `:489-514`
  (`TestAWaiterHonoursItsOwnDeadlineRatherThanTheOpeners`).
- **What:** both orchestrate a race with `time.Sleep`: a 60 ms (resp. 2 s) open,
  `time.Sleep(10 * time.Millisecond)` twice to place the participants, and
  `waited > time.Second` as the bound. On a loaded machine or under `-race` the
  60 ms open can complete before the three borrowers arrive, at which point all
  three simply open normally, `failures` is all-nil, and the test passes without
  ever exercising the abandoned-opener path. Nothing in the test can tell the two
  situations apart.
- **Why this severity:** flaky-by-construction in one direction and
  silently-vacuous in the other; the second test also spends real wall-clock time
  in a unit suite that otherwise runs in 0.15 s.
- **Why this timing:** they are new and cheap to make deterministic while the
  author's context is fresh.
- **Close criteria:**
  - [ ] The `openings` double signals "I am inside `Source`" on a channel and the
        test waits on it instead of sleeping.
  - [ ] Each test asserts it actually reached the state it is named for (the
        waiters really waited on somebody else's open), so a race that did not
        happen is a failure and not a pass.
- **Status:** open

### GAP-T17 [medium][immediate] `t.Fatalf` is reached from non-test goroutines in three tests

- **Where:** `tenancy/database_test.go:396-422`, `:424-447`, `:449-487` — the
  goroutines call `bound(t, …)` / `boundTo(t, …)`, and those helpers
  (`row_test.go:38-45`, `database_test.go:293-304`) call `t.Fatalf`.
- **What:** `t.Fatalf` from a goroutine that is not the test's own is documented
  as not stopping the test correctly; `go vet`'s `testinggoroutine` check misses
  it because the call is one level down inside a helper.
- **Why this severity:** it only matters when the helper fails — which is exactly
  when a diagnosis is needed, and exactly when the output will be wrong.
- **Close criteria:**
  - [ ] The contexts are built on the test goroutine before the fan-out, or the
        helpers return an error the goroutine reports with `t.Errorf`.
- **Status:** open

### GAP-T18 [medium][deferred] The cohort loop's bounds and its retry story are untested

- **Where:** `tenancy/grant.go:342-374` (`Accept`), `:380-406` (`Each`);
  `tenancy/grant_test.go`.
- **What:** four survivors and two absent use cases.
  - `MaxCohortSize` removed — **SURVIVED** (no test issues an over-large cohort);
  - duplicate-member de-duplication removed — **SURVIVED** (no test passes a
    duplicate, so "a retry applies each tenant exactly once" is unpinned at the
    cohort level);
  - the `ctx.Err()` check between members removed — **SURVIVED**;
  - `Purpose.valid()`'s length and control-character rules removed — **SURVIVED**
    (only the empty purpose is tested).
  UC-81 ("out-of-cohort access refuses rather than returning empty") and UC-82 /
  INV-21 ("per-tenant effect counters equal 1 after failure and retry") have no
  test: `TestOneCohortMemberFailingLeavesTheRestResumable` covers a member
  *skipped* by lifecycle, never a member whose `work` fails, and never a re-run.
- **Why this severity:** INV-21 is a data-integrity invariant (double-applied
  billing on retry) and it is asserted nowhere.
- **Close criteria:**
  - [ ] A cohort with a duplicate reference enters that tenant once.
  - [ ] A member whose `work` returns an error is recorded, the run continues, and
        a resume driven by the returned `[]Member` applies each tenant exactly once
        (per-tenant counters == 1).
  - [ ] A tenant outside the cohort is refused inside `work`, not served empty.
  - [ ] A cancelled context stops the run at the member boundary.
- **Status:** open

### GAP-T19 [medium][deferred] The object and cache seams are tested at the digest, not at the seam

- **Where:** `tenancy/seams_test.go`; `tenancy/storage.go:30` (`Authority.Store`)
  and `tenancy/cache.go:32-34,55` (`Key.Unwrap/Scope/String`, `CacheScope`) are
  at **0.0% coverage**.
- **What:** every object/cache assertion is against `namespaceOf` or the raw
  `Partition` function. No test ever builds a `storage.Store` or a `cache.Scope`
  and shows that a listing stays inside the namespace (UC-62), that an
  invalidation does not cross partitions (UC-64), or that a slow loader is
  coalesced per partition rather than globally (UC-63 / INV-19 — "loader
  invocation count equals the number of distinct partitions, not 1", the one
  concurrency property those seams have). Two bound checks survive mutation:
  `MaxNamespacePrefixBytes` and `MinPartitionBytes` can both be deleted.
- **Why this severity:** INV-19 is a concurrency-only cross-tenant read, the class
  the spec says "no serial test can find" — and there is no such test.
- **Close criteria:**
  - [ ] A `cache.Scope` built through `CacheScope` with a deliberately slow loader
        and two tenants: loader invocations == 2, values do not cross.
  - [ ] A `storage.Store` built through `Authority.Store` where tenant A lists and
        sees none of tenant B's objects.
  - [ ] Boundary tests for the prefix length and the minimum partition width.
- **Status:** open

### GAP-T20 [medium][deferred] The durable seam: refusals are asserted as "something failed", and two branches have no test

- **Where:** `tenancy/durable_test.go:141-169`, `:223-271`; `tenancy/jobs.go:33-41`,
  `:85-109`.
- **What:** every durable refusal is asserted as `err == nil → t.Fatal`.
  Confirmed by probe: `jobs.RestoreTrustedIdentity` collapses all three cases —
  suspended, deleted, generation-moved — to `jobs: driver operation failed`, so
  `errors.Is(err, ErrInactive)` and `errors.Is(err, ErrStale)` are both false.
  That collapse is `TENANCY_S1_GAPS.md` GAP-33 (open, implementation), **but the
  tests are in `package tenancy`** and can call `jobIdentity.RestoreIdentity`
  directly to assert the kind; they do not. As written, a token that failed to
  parse and a tenant that was deleted are the same green.
  Two survivors: `durableReady`'s `MinDurableKeyBytes` floor removed —
  **SURVIVED**; the non-tenant branch of `RestoreIdentity` turned into a refusal —
  **SURVIVED** (no test enqueues work that does not require a partition, so
  UC-58 is untouched).
- **Why this severity:** INV-30 requires refusals to carry a comparable kind and
  the house rule requires `errors.Is` against a sentinel. Three subtests here
  assert only that something went wrong.
- **Close criteria:**
  - [ ] The three lifecycle subtests assert the sentinel through the restorer
        directly (`ErrInactive`, `ErrInactive`, `ErrStale`).
  - [ ] A job declared without `PartitionTenantRequired` restores with no tenant
        bound and runs.
  - [ ] `JobContext` / `JobIdentity` refuse an authority whose `DurableKey` is
        short or absent.
- **Status:** open

### GAP-T21 [medium][deferred] Construction-time and resolver-contract guards are untested

- **Where:** `tenancy/row.go:52-55` and `:190-213` (the panics), `:216-218`
  (`Policy`'s nil check), `tenancy/authority.go:98-100` (`Lookup`'s reference
  match), `:108-110` (`mint`'s `resolution.valid()`), `tenancy/lifecycle.go:109-116`
  (`Admission.inconsistent`), `tenancy/context.go:74-80` (`From`'s zero check).
- **What:** all six survive mutation. In particular:
  - a resolver that answers `Lookup(acme)` with a resolution naming `globex` is
    accepted — the one place the framework checks its resolver, and no test
    covers it;
  - `mint` accepting an invalid resolution means a resolver returning `Epoch(0)`
    produces a `Scope` from `Verify` with no error (it fails later, at `With`);
  - the admission-consistency refusal — the subject of `TENANCY_S1_GAPS.md`
    GAP-24, reworded this round — has no test in either direction;
  - `Column("Nonexistent", …)` and `Relation{Path: "NoSuchRelation"}` panic
    deliberately, and nothing asserts they do, so a typo in a composition root
    has no pinned behaviour.
- **Why this severity:** these are the guards that turn a misconfiguration into a
  start-up failure instead of a silent narrowing hole.
- **Close criteria:**
  - [ ] A resolver double that answers with the wrong reference is refused
        `ErrUntrusted`, with a control.
  - [ ] `New` refuses `Admit(ClassWrite, Suspended)` without the matching read,
        and accepts the durable-only case the comment says it allows.
  - [ ] `Column`/`Through` panic assertions for an unknown field and an unknown
        relation path.
- **Status:** open

### GAP-T22 [medium][deferred] The redaction surface is asserted for two of its seven carriers

- **Where:** `tenancy/scope_test.go:197-222` covers `Scope` and `Reference` under
  `Sprint/%v/%s/%+v` and `json.Marshal`. At **0.0% coverage**: `Grant.String`,
  `Grant.Format`, `Grant.LogValue`, `Grant.MarshalJSON`, `Reference.LogValue`,
  `Scope.LogValue`, `Resolution.String`, `Resolution.Format`.
  `Lifecycle.String` is at 37.5%.
- **What:** a `Grant` holds a cohort of up to 10 000 tenant references and its
  redaction is untested; `LogValue` is the `slog` path, which is where INV-25's
  "log field" clause lands, and it is untested on every type that has one.
  INV-26 asks that the emitted vocabulary be asserted a subset of the declared
  closed set; `TestTheOutcomeVocabularyIsClosed` does that for `Outcome` (good)
  and nothing does it for `Lifecycle.String()`, where only the `unknown` branch
  is asserted.
- **Close criteria:**
  - [ ] The `TestNothingCarryingATenantRendersIt` table grows rows for `Grant`,
        `Resolution` and the `slog` path (`slog.New(handler).Info("x", "scope", scope)`
        over a buffer, scanned for the sentinel).
  - [ ] `Lifecycle.String()` is asserted to be a subset of a declared closed set
        across all seven values.
- **Status:** open

### GAP-T23 [medium][deferred] The plan's coverage matrix claims twenty-odd items that no test supports

- **Where:** `TENANCY_PLAN.md` `## UC / INV coverage matrix`.
- **What:** the matrix's closing line — "Every UC-* and INV-* in the spec appears
  exactly once above" — is true of the *matrix* and is being read as a coverage
  claim. Items that appear in a section row and have no test at all:
  **UC-12** (a caller-supplied tenant hint is never accepted), **UC-14** (GAP-T6),
  **UC-17** (a scope verified for one topology meets the other seam),
  **UC-18** (partly closed this round by `revalidate_test.go`),
  **UC-26** (two tenants holding equal resource identifiers — every test id is
  auto-assigned, so no two tenants ever hold the same one),
  **UC-34** (a soft-deleted foreign row is restored — no tenancy fixture has a
  soft-delete column, and `Restore` is absent from every verb list),
  **UC-36** (an escape-hatch query path — also `TENANCY_S1_GAPS.md` GAP-15/32),
  **UC-37** (an integrity fault naming a foreign row — no `faults`/`probe`
  interaction with tenancy exists), **UC-57** (retry after partial application),
  **UC-58**, **UC-63**, **UC-64**, **UC-81**, **UC-82**, **UC-85**, **UC-87**,
  **UC-89**; **INV-17** at S2, **INV-19**, **INV-21**, **INV-24** (session
  hygiene — not in S4's `MISSING` either), **INV-27**, **INV-28**, **INV-29**.
- **Why this severity:** the matrix is what the profile and the roadmap read from.
  A row with no test is a claim the next agent will not re-derive.
- **Close criteria:**
  - [ ] The matrix gains a third column naming the test per row, or splits into
        "covered by test X" and "argued, not tested".
  - [ ] Every row with no test is moved to `## Debt` with the reason, as UC-33 and
        UC-35 already are.
- **Status:** open

### GAP-T24 [low][deferred] Four resolver fakes for one interface, an exported test double in production code, and small readability nits

- **Where:** `tenancy/authority.go:143-152` (`Fixed`), `tenancy/scope_test.go:224-235`
  (`switchable`), `vocabulary_test.go:127-141` (`directory`),
  `seams_test.go:196-208` (`knownTenants`), `revalidate_test.go:15-28` (`counting`).
- **What:** five implementations of a two-method interface, three of which
  (`switchable`, `directory`, `knownTenants`) differ only in whether the map is a
  map. `Fixed` is a test double that ships in the production package, exported and
  undocumented as such — which sits awkwardly next to UC-92 ("test-only
  construction is reachable from production"), even though it forges no `Scope`
  and so does not break INV-2. Smaller: `directory` (a resolver) and
  `tenancy.Directory` (the source cache) are one word apart in the same test
  binary; `seams_test.go:294-304` hand-rolls `itoa` where `strconv.Itoa` exists;
  `clauseOf` and `where` (`row_test.go:47-58`) duplicate `lastWhere`
  (`security_test.go:55-61`).
- **Close criteria:**
  - [ ] One table-driven resolver fake in a shared test helper, parameterised by
        the map and by a mutable "current" pointer.
  - [ ] `Fixed` is either documented as the supported single-tenant resolver or
        moved behind a `tenancytest` package.
- **Status:** open

### GAP-T25 [low][deferred] Two statements in the plan no longer match the code or the environment

- **Where:** `TENANCY_PLAN.md` `## Mutation evidence`, closing paragraph; and
  `## What "green" means here, exactly`.
- **What:** (a) "Removing only *one* of the directory's two capacity checks left
  the test passing… the second check is not redundant" — the shipped
  `Directory.reserve` has exactly **one** capacity check
  (`database.go`, `if len(this.entries) >= this.max`), so the observation cannot
  be reproduced against this code and reads as evidence for a design that changed.
  (b) The integration evidence cannot currently be reproduced by the documented
  command, for the reason the same section warns about: 55432 is held by another
  project's PostgreSQL, `docker compose up -d --wait` reports the vv container
  healthy anyway, and the suite then authenticates against the wrong server.
- **Close criteria:**
  - [ ] The mutation-evidence paragraph is updated to the single check, or the
        second check is restored and named.
  - [ ] `make integration` fails loudly when the server on the configured port is
        not the one compose started (a `SELECT current_setting('…')` marker, or a
        port-ownership check in `scripts/database.sh`).
- **Status:** open

---

## Mutation log

Every row was applied to a copy of the tree, run with
`go test -count=1 ./tenancy/... ./crud/ ./crud/decorators/security/ ./crud/decorators/faults/`,
and reverted. `CAUGHT` names one test that failed. Mutations that failed to
compile were reworked until they compiled before being scored.

### Survived — the suite stayed green on a broken implementation

| # | Mutation | Where | Consequence if shipped |
|---|---|---|---|
| M1 | `bind()` stops mixing `origin` into the scope binding | `scope.go:65` | a scope from another deployment verifies (UC-14) |
| M3 | `Lookup` accepts a resolution naming a different tenant than asked for | `authority.go:98` | a resolver bug becomes cross-tenant authority |
| M4 | the unit-of-work pin compares the reference and not the epoch | `context.go:28` | a generation change mid-transaction re-binds silently |
| M5 | `Admission.inconsistent()` never reports a write-without-read policy | `lifecycle.go:109` | the construction-time refusal disappears |
| M6 | `classOf()` returns `ClassRead` for every action | `row.go:259` | writes admitted wherever reads are (also survives integration) |
| M7 | `Through.Frozen()` freezes nothing | `row.go:170` | repointing the FK moves a row between tenants |
| M10 | `Each` does not check the classes the grant permits | `grant.go:390` | a read grant runs write work |
| M11 | `permittedByGrant` drops the class check | `grant.go:481` | same, per verb |
| M13 | `Accept` keeps duplicate cohort members | `grant.go:361` | a retry double-applies a tenant |
| M15 | the object namespace prefix is unbounded | `storage.go` | an over-long prefix reaches the bucket |
| M16 | a cache partition is emitted below `MinPartitionBytes` | `cache.go` | partition collisions become thinkable |
| M18 | `JobContext`/`JobIdentity` accept an authority with no durable key | `jobs.go:37` | unauthenticated queue references |
| M19 | `RestoreIdentity` refuses instead of passing through non-tenant work | `jobs.go:86` | untenanted jobs stop running (nothing tests them) |
| M21b | `Purpose.valid()` drops the length and control-character rules | `grant.go:298` | a purpose carrying a newline reaches a label |
| M23 | `From()` hands back a zero scope as if it were carried | `context.go:76` | a zero scope escapes the accessor |
| M24 | `mint()` no longer refuses an invalid resolution | `authority.go:108` | `Verify` returns a scope with `Epoch(0)` and no error |
| M33 | `Policy.Scope` narrows on a fabricated reference | `row.go:220` | **every tenant reads one tenant's rows** (caught only by integration) |
| M36 | the gate's unscoped probe drops the relation narrowing | `security.go:389` | the probe reaches an unnarrowed relation |
| M37 | the gate's unscoped probe drops the caller's own options | `security.go:390` | the probe answers a wider question than asked |
| M38 | the gate does not authorize `Read` before answering the probe | `security.go:382` | an unauthorised caller gets an existence oracle |
| M42 | `Column()` no longer panics on an unknown model field | `row.go:52` | a typo in the composition root |
| M43 | `resolveRelation` no longer validates the declared path | `row.go:196` | a typo'd relation declares nothing |
| M44 | `Policy()` accepts a nil authority | `row.go:216` | wiring failure deferred to first request |
| M46 | the narrowing names the right column with a hardcoded foreign value | `row.go:76` | **every tenant reads one tenant's rows** |
| M47b | the fence is called with a nil source and a zero scope | `database.go:195` | selection fencing is decorative |
| M48b | the source factory is called with the zero scope | `database.go:185` | **every tenant routed to one database** |
| M49 | the durable token is not bound to the queue namespace or the job name | `jobs.go:141` | cross-queue token replay |
| M8 | `Lease.Release` loses its `sync.Once` (snapshot before 21:46) | `database.go` | **closed by `TestOneLeaseReleasedFromManyGoroutinesCountsOnce`, added during this round** |
| M9 | `Directory.Close` does not mark the directory closed | `database.go:243` | `Borrow` after `Close` (also `TENANCY_S1_GAPS.md` GAP-34) |
| M12 | `Accept` no longer bounds the cohort size | `grant.go:349` | a 10 M-member grant |
| M14 | `Each` ignores context cancellation between members | `grant.go:400` | a cancelled cohort run continues |
| M2 | the revalidation branch re-checks nothing (snapshot before 21:45) | `context.go:57` | **closed by `revalidate_test.go`, added during this round** |

### Caught — the suite went red, and on the test that names the behaviour

| # | Mutation | Test that caught it |
|---|---|---|
| M2b | revalidation re-checks the epoch but not the lifecycle | `TestRevalidationStopsTheNextVerbAfterTheControlPlaneMoves` |
| M2c | revalidation re-checks the lifecycle but not the epoch | same |
| M17b | the durable restorer skips the generation check | `TestWorkEnqueuedBeforeASuspensionDoesNotRunAfterIt` |
| M20b | `Reference.valid()` allows interior spaces and control characters | `TestAReferenceIsRefusedWhenItCouldNotSurviveAKeyOrANamespace` |
| M22 | the generation digest drops the epoch | `TestARestoredGenerationReadsNoneOfThePreviousOnes` |
| M25 | the directory does not apply the fence | `TestADatabaseTheFenceDoesNotRecogniseIsRefused` |
| M26 | the binding cache key drops the generation | `TestARotatedGenerationDoesNotReachTheBindingItReplaced` |
| M27 | `Evict` is a no-op | `TestEvictionWaitsForTheLastBorrower` |
| M28 | an evicted source is closed under a live borrower | same |
| M29 | `column.Frozen()` freezes nothing | `TestAMutationCannotMoveARowBetweenTenants` |
| M30 | `Column` ignores its declared relations | `TestAPreloadOfADeclaredRelationCarriesTheTenant` |
| M31 | `Validate` behaves like `Derive` | `TestValidateRefusesACreateThatOmitsOwnership` |
| M32 | a create over a foreign owner is stamped rather than refused | `TestACreateForAnotherTenantIsRefusedWhicheverModeIsDeclared` |
| M34 | `Through` narrows on the local column instead of the path | `TestARowOwnedThroughARelationIsNarrowedByThatRelation` |
| M35 | relation scopes are supplied when nothing is declared | five row tests |
| M39 | `ExistsUnscopedOf` walks `Nexter` again | `TestUnscopedExistenceIsAnsweredOnlyByTheExactOuterCore` |
| M40 | the enricher answers "unsupported" instead of `ErrNoUnscopedExists` | `TestTheEnricherForwardsUnscopedExistence` |
| M41 | `Policy.Inspect` allows everything | four row tests |
| M45 | the narrowing names a column that is not the owner column | `TestEveryReadCarriesTheTenantIntoTheStatement` (2 of 8 subtests), `TestATenantSeesEveryRowItOwns` |
| M50 | `column.Apply` stops comparing the held value to the wanted one | three row tests |
| M51 | `Authority.Scope` ignores the grant bound | `TestAContextCarriedOutOfAnExpiredGrantStopsWorking` |

### The plan's own mutation table, re-run independently

| Plan row | Result |
|---|---|
| `Scope.boundTo` stops comparing the binding | **CAUGHT** — `TestOnlyTheAuthorityThatMintedAScopeAcceptsIt` |
| `Admission.Admits` admits every state | **CAUGHT** — 8 tests, incl. `TestOnlyAnAdmittedLifecycleReachesWork` |
| the directory's slot is not reserved before the source is opened | **CAUGHT** — 6 directory tests |
| both of the directory's capacity checks are removed | **CAUGHT** — 3 directory tests (note: the code has one such check, GAP-T25) |
| a grant's deadline is not re-checked at the point of use | **CAUGHT** — `TestAContextCarriedOutOfAnExpiredGrantStopsWorking` |
| the producer does not capture the tenant | **CAUGHT** — 3 durable tests |
| the durable restorer believes its record | **CAUGHT** — `TestWorkEnqueuedByATenantIsExecutedAsThatTenant` + one more |
| the gate drops its own scope from the unscoped probe | **CAUGHT** — `TestTheGateAnswersTheUnscopedProbeWithinItsOwnScope` |
| `ExistsUnscopedOf` goes back to walking `Nexter` | **CAUGHT** — as claimed |
| the declared relation is dropped from the preload | **CAUGHT** — as claimed |
| ownership is neither stamped nor checked on create | **CAUGHT** — as claimed |
| relation narrowing decided by probing rather than asking | **CAUGHT** — as claimed |

Not re-run (accepted as claimed): `classify` returns the resolver's own error;
the generation leaves the namespace digest; every part of a grant's binding
(four mutations); the producer captures without asking for the durable class.

### The three gaps that would do the most damage

1. **GAP-T1 + GAP-T2 together.** The unit suite cannot distinguish "narrowed to
   the right tenant" from "narrowed to a constant tenant" from, for six of eight
   read verbs, "not narrowed at all". The one test that would catch it needs a
   live PostgreSQL that the documented command currently cannot reach.
2. **GAP-T3.** The database-per-tenant strategy is proved to cache, bound, evict
   and expire correctly, and is proved nowhere to route the right tenant to the
   right database. A directory that sends everyone to one server is green.
3. **GAP-T5 + GAP-T7 + GAP-T8 + GAP-T9.** Four fixes landed by the phase-4 review
   — per-class admission on the row seam, the grant's class bound, the epoch half
   of the unit-of-work pin, and the durable token's binding to its queue — each
   ship with no test. All four survive deletion.

---

## Round 1 — dispositions (orchestrator)

58 mutations applied, 32 survived. That is the finding, and it is worse than the
green output suggested: the suite was passing on a narrowing that named the wrong
tenant entirely. Every `[critical]` and `[high][immediate]` gap below is closed
with a test that fails on the behaviour it names.

| Finding | Fix | Mutation proof |
|---|---|---|
| **GAP-T1 critical** — nothing asserted *which tenant* the narrowing names | `TestEveryVerbNarrowsToTheScopesOwnTenantAndNoOther` asserts, for nine verbs, that the `WHERE` clause contains `"tenant_id" = $` **and** that the bound arguments carry the scope's own reference | narrowing on a constant tenant, and narrowing on the wrong column, each turn **10 of 10** subtests red — where the old test lost only 2 of 8 |
| **GAP-T2 critical** — the read test matched `tenant_id` anywhere in the statement, including the projection, and the bulk verbs issued no statement | the assertion is on the clause after `WHERE`, and `TestABulkMutationReachesTheDatabaseNarrowed` pushes rows so the `DELETE` actually runs, then asserts on that statement | as above |
| **GAP-T3 critical** — the database tests never asserted the tenant reached the factory or the fence | `TestTheFactoryAndTheFenceAreToldWhichTenantIsAsking` records what each was handed and compares it to the borrower, generation included | an empty scope to the factory, an empty scope to the fence, and a nil source to the fence — all three caught |
| **GAP-T5 high** — per-class admission was never proved at the repository seam | `TestTheRepositorySeamAsksForTheClassOfTheVerb`: a suspended tenant a deployment keeps readable reads, and its write refuses with no `INSERT` | `classOf` returning a constant, and `Inspect` asking for the read class, both caught |
| **GAP-T6 high** — `Spec.Origin` had no test, and the salt would mask one | `TestTheOriginIsPartOfTheBindingAndNotJustTheSalt` is internal, so it can hold the salt fixed and vary only the origin. `TestEveryFieldOfAResolutionIsPartOfItsBinding` and `TestFieldsCannotBeSlidPastTheLengthPrefix` cover the rest | origin, lifecycle and the length prefix each removed, each caught |
| **GAP-T7 high** — the grant's class bound was enforced twice and tested nowhere | `TestAReadGrantCannotWriteAtEitherGuard` drives both the entry guard and the per-verb guard | both guards removed independently, both caught |
| **GAP-T8 high** — the unit-of-work pin was tested on the reference only | `TestTheUnitOfWorkPinCoversTheGenerationAsWellAsTheTenant` | dropping the epoch comparison is caught |
| **GAP-T9 high** — the token's binding to the queue and the job name had no test | `TestADurableRecordCannotBeReplayedIntoAnotherJobOrQueue` replays one token into another namespace, another definition, and both | namespace and definition each removed from the MAC, each caught |
| **GAP-T10 high** — three assertions could not fail | (1) `TestAScopeHasNothingAnybodyCanWriteThrough` asserts the *type* property INV-3 is actually about — no exported field, no reference-typed field, no setter — instead of 64 goroutines reading copies; (2) fixed under GAP-T2; (3) the kernel test now asserts `"id" = $` and the argument count | an exported field on `Scope` is caught; **dropping the caller's own options from the gate's probe is now caught**, where before it survived |
| **GAP-T13 high** — nothing composed tenancy with a second decorator in either order | `TestNarrowingHoldsWhicheverOrderTheExtensionsAreListedIn` uses `faults.Enrich`, a real decorator that forwards every optional effect, and asserts both the narrowing and the unscoped refusal in both orders | — |
| **GAP-T14 high** — nothing stopped a base subsystem importing the extension, and nothing probed import-time activation | `scripts/tenancy_test.go`: `TestNoBaseSubsystemDependsOnTheOptionalExtension` walks `go list -deps` over twelve subsystems, and `TestTheExtensionDoesNothingWhenItIsMerelyImported` refuses an `init()`, a package-level `Getenv`/`MustCompile` and a goroutine | making `app` import `tenancy` is caught with the right message; adding an `init()` to the extension is caught |

Still open, and recorded rather than closed: GAP-T4 (INV-5 is proved for one
invalid-scope class of six), GAP-T11, GAP-T12, GAP-T15 (relation closure at depth
≥ 2 and against a live database), GAP-T16..T25. They are in the plan's `## Debt`.
