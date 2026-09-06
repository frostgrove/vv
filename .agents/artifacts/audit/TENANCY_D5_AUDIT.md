# tenancy/ + its kernel seams — microkernel, building blocks, architecture — AUDIT (2026-09-06)

Scope: `tenancy/` (all 6 packages), `scripts/tenancy_test.go`, `scripts/checks.sh`,
`_examples/tenancy-sharedrow/`, and the extension points in `crud/`,
`crud/decorators/security/`, `jobs/`, `cache/`, `storage/`.
Dimension: laws 1 (microkernel), 2 (building blocks / contexts), 3 (architecture metrics).

## Audit baseline — the tree moved under me

**The working tree was modified by another process during this audit.** This is
recorded first because three of my measurements were taken against states that no
longer exist, and every number below is pinned to one frozen snapshot.

| Time | Observation | Command / evidence |
|---|---|---|
| ~00:12 | `go test ./tenancy/...` reported `ok (cached)` for all 6 packages | cached results |
| ~00:15–00:19 | `go test -count=1 ./tenancy/...` **failed 4 times in a row**, 4 distinct tests, all "a scope from another authority was accepted": `TestOnlyAScopeThisAuthorityMintedIsSealed` (seal_test.go:92), `TestTheOriginFencesOneDeploymentFromAnother` (contract_test.go:62), `TestOnlyTheAuthorityThatMintedAScopeAcceptsIt` (scope_test.go:77), `TestTheCacheSeamRefusesAScopeAnotherAuthorityMinted` (cache_test.go:131), `TestTheObjectSeamRefusesAScopeAnotherAuthorityMinted` (storage_test.go:129) | pasted below |
| ~00:22 | `tenancy/scope.go` rewritten on disk (mtime `2026-09-06 00:22:46`); suite went green | `ls -la --time-style=full-iso` |
| 00:30:51 | `tenancy/scope.go:44` changed to `LogValue() { return slog.StringValue(this.reference.Value()) }` and back | `diff -r` against my snapshot |
| ~00:31 | a blank import `_ "github.com/frostgrove/vv/crud/decorators/security"` appeared at `tenancy/tenancydb/database.go:11` and was removed | `diff -r` against my snapshot |
| ~00:31 | `TestTheRepositorySeamAsksForTheClassOfTheVerb` failed once, then passed | `go test -count=1` |
| 00:45 | **`scripts/tenancy_test.go` rewritten** (107 → 131 lines): the 12-subsystem list replaced by an enumeration of `./...`, the 5-row adapter table replaced by a derivation from `./tenancy/...`. This closes most of GAP-5 — amended in place below | `md5sum scripts/tenancy_test.go` → `79ba45a594e6f21b11dd5fb486f918cf` |
| 00:45 | `tenancy/grant.go`, `scope.go`, `seal.go` + 2 test files advanced (`writeCount`, origin inside the seal MAC, `Sealer.Seal` now `accept(scope, ClassDurable)` instead of `minted`, 3 new tests). Every structural number I report was re-measured after this and is unchanged | `diff -r` + re-run of every metric |

The last two are almost certainly another agent verifying D-116's own "Proven by"
claims (D-116 says in as many words: *"an import of `cache` added to the core is
reported, and so is `security` added to `tenancydb`"*). I am not reporting them as
defects. I **am** reporting that a green/red verdict on this tree is not
reproducible while that is happening.

**Frozen snapshot** (all numbers below): copy of the tree taken 00:24, verified
green, per-file md5:

```
0531d25a523188817e4af6e6c5aa8382  tenancy/authority.go
9d70c1353e35d66a08c6c22c77b9d676  tenancy/context.go
1b6eebd427a2fc66c707507867c2919b  tenancy/doc.go
b445fbda9c5c19d146016c33c704f0ba  tenancy/errors.go
8ffd8551cfa59b1d0f04beeae3c97f67  tenancy/grant.go
308e16ca49585cbd6029eb1c1cf5edd0  tenancy/lifecycle.go
e794785fbdbe16324cb93c3425b35f86  tenancy/outcome.go
ac294e2aebc17a302f04c4bf670314d4  tenancy/reference.go
58de2b46c22e92dad58510c121d62fcf  tenancy/scope.go
f757f777ca800ff901aaaee7289be0ad  tenancy/seal.go
629b633b22836c1a0c202c5228b43961  tenancy/tenancycache/cache.go
d355bf8999f8b496fc73e346dbdbdd80  tenancy/tenancydb/database.go
da0a7ab8ea72da0827e92a95f817fac0  tenancy/tenancyjobs/jobs.go
70bf969a5b72ec8f8bdd732e08ec5437  tenancy/tenancyrow/row.go
390eac657e3955f6de466ff702a40f6f  tenancy/tenancystorage/storage.go
```

Baseline on the snapshot:

```
$ go test -count=1 ./tenancy/...
ok  	github.com/frostgrove/vv/tenancy	0.004s
ok  	github.com/frostgrove/vv/tenancy/tenancycache	0.002s
ok  	github.com/frostgrove/vv/tenancy/tenancydb	0.146s
ok  	github.com/frostgrove/vv/tenancy/tenancyjobs	0.002s
ok  	github.com/frostgrove/vv/tenancy/tenancyrow	0.002s
ok  	github.com/frostgrove/vv/tenancy/tenancystorage	0.004s
```

The transient red, verbatim, for the record:

```
$ go test -count=1 ./tenancy/...
--- FAIL: TestOnlyAScopeThisAuthorityMintedIsSealed (0.00s)
    seal_test.go:92: err = <nil>, want ErrUntrusted — a scope from another authority was sealed under this key
--- FAIL: TestTheOriginFencesOneDeploymentFromAnother (0.00s)
    contract_test.go:62: err = <nil>, want ErrUntrusted — a scope that travelled from staging was honoured in production
--- FAIL: TestOnlyTheAuthorityThatMintedAScopeAcceptsIt/one_minted_by_another_deployment (0.00s)
        scope_test.go:77: a scope this authority never produced was accepted — every other control rests on this one
--- FAIL: TestTheCacheSeamRefusesAScopeAnotherAuthorityMinted (0.00s)
    cache_test.go:131: err = <nil>, want ErrUntrusted
--- FAIL: TestTheObjectSeamRefusesAScopeAnotherAuthorityMinted (0.00s)
    storage_test.go:129: err = <nil>, want ErrUntrusted
```

## Map

- **Kernels, outermost in:** the root module `github.com/frostgrove/vv` is a set of
  independently selectable subsystems (`crud`, `auth`, `jobs`, `cache`, `storage`,
  `port`, `app`, …); each is a mini-kernel with its own named extension points.
  `tenancy` is a *plugin of five of them at once*: one core + one adapter package
  per seam. There is no container in the library (`app/appfx` is the seam);
  composition is application code.
- **Extension points tenancy plugs into:** `security.Policy[M,ID]` (11 optional
  hook fields) → `tenancyrow`; `crud.Source` + `crud.Middleware` → `tenancydb`;
  `jobs.TrustedContextProvider` / `jobs.TrustedIdentityRestorer` → `tenancyjobs`;
  `cache.Partitioner[K]` → `tenancycache`; `storage.Namespace` → `tenancystorage`.
- **Extension points tenancy itself exposes:** `tenancy.Resolver` (the control
  plane), `tenancyrow.Ownership[M]` + `tenancyrow.Value`, `tenancydb.Sources` +
  `DirectorySpec.Fence`, `Spec.Now`.
- **State:** none in the row seam (a predicate per call); one `sync.Mutex`-guarded
  `map[binding]*entry` in `tenancydb.Directory`; a per-process `crypto/rand` salt
  and a configured `durableKey` in `Authority`; two package-level maps/arrays in
  `tenancy/errors.go` and `tenancy/outcome.go`.
- **No LLM/model/embedding anywhere in scope.** No SQL is written in tenancy: every
  statement is composed as a `crud.Predicate` and executed by `crud/sqlrepo`.
- **Tests:** 18 `_test.go` files, ~3.4 k test lines vs 1770 production lines.
  4 of 18 are internal (`package tenancy`, `package tenancyjobs`,
  `package tenancycache`, `package tenancystorage`); the rest are external.
  `test/integration/tenancy_test.go` is 165 lines / 2 functions and covers
  `tenancyrow` only. `scripts/tenancy_test.go` (107 lines) carries the structural
  optionality proof.
- **Composition root:** none in the library. The only end-to-end wiring is
  `_examples/tenancy-sharedrow/main.go` (165 lines, in the unpublished
  `_examples` module), and it wires 2 of the 5 seams.

## Scorecard

| Law | Verdict | Evidence |
|---|---|---|
| 1. Microkernel — kernel imports of the extension | **pass** | 0 across 44 root packages and 33 satellite modules (measured, not asserted) |
| 1. Microkernel — kernel diff to add tenancy | **pass** | 4 kernel files, +37/−14 Go lines vs 1734 extension lines = **2.1 %** kernel share; every one of the 4 is a general mechanism, none tenancy-shaped |
| 1. Microkernel — a *second* extension of the same kind | **at risk / fail for `jobs`** | `jobs` names the tenancy concept in **6 non-test files, 22 sites**, incl. a closed 2-value enum `PartitionMode{PartitionGlobal, PartitionTenantRequired}` |
| 1. Extension points — 4 requirements | **partial** | `Ownership[M]`: contract ✓, registration ✓, failure policy ✓ *for errors* / **✗ for panics**, version note **✗**. `jobs.TrustedContextProvider`: all 4 ✓ but its input type has no exported constructor |
| 1. Extension → extension imports | **pass** | 0; each adapter's first-party graph = core + its own seam (`TestNoTenancyPackageCostsMoreThanTheSeamItNames`, 5 subtests, PASS) |
| 1. Optionality is proven, not asserted | **pass** (51/51 root packages) / **gap** (0/33 satellite modules) | `scripts/tenancy_test.go` — rewritten during this audit, see GAP-5 |
| 2. Building blocks | **n-a as written / pass on substance** | the VO/Command/UseCase/App-UseCase/Repository vocabulary does not apply to a Go framework library; the substantive checks (immutability, validation at construction, no I/O in values, provider-declared contracts) hold — see the classification table |
| 2. Bounded contexts | **n-a** | one library, no contexts; the analogue (adapter isolation) is enforced by test |
| 3. Architecture metrics | **at risk** | 0 cycles, 0 functions > 33 lines, 0 functions with complexity > 10, avg complexity **2.72**; breaches: `Authority` 9 public methods / 218 LOC / 3 responsibilities, `Directory` 10 fields, `tenancy` 113 exported symbols |
| 3. Isolation (≤ 2 fakes) | **fail for 2 of 9 types** | `tenancydb.Directory` needs 4; `tenancyjobs.contextProvider` needs a booted queue (7 collaborators) because `jobs.ContextCaptureRequest` has no exported constructor |
| 3. Injection | **pass, with one documented exception** | everything is injected via `Spec`/`DirectorySpec`; `crypto/rand` salt is deliberately not (D-117), cost measured: 3 of 8 core test files must be internal |
| DRY | **at risk in tests only** | one concept ("an Authority over a canned resolution") has **2 names and 6 copies** across 6 packages |

## Metrics

### Size and complexity (production files only, frozen snapshot)

| Metric | Threshold | Actual | Worst offenders |
|---|---|---|---|
| Lines per file | ≤ 400 | **0 breaches** | max `tenancy/tenancydb/database.go` (284), `tenancy/tenancyrow/row.go` (269), `tenancy/grant.go` (228) |
| Lines per "class" (type + its methods) | ≤ 200 | **1 breach** | `Authority` = **218** lines across 4 files (authority.go 59, context.go 53, seal.go 9, grant.go 97) |
| Lines per function | ≤ 50 | **0 breaches** | max 33: `Directory.Borrow` (database.go:119), `Authority.Accept` (grant.go:79); then 32: `NewDirectory` (database.go:80), `tenancy.New` (authority.go:37) |
| Cyclomatic complexity | ≤ 10 | **0 breaches** (`gocyclo -over 10` empty) | max 10 `Authority.Accept`; 9 `Authority.Each`; 8 `Directory.Borrow`, `resolveRelation`, `tenancy.New`, `Reference.valid`. **Average 2.72** over 92 functions |
| Nesting depth | ≤ 3 | **0 breaches** | max 3 inside `tenancy/tenancyrow/row.go` |
| Function parameters | ≤ 4 | **2 breaches** | `grantBinding` 6 (unexported, `grant.go:185`); **`tenancystorage.Store` 5** (exported, `storage.go:28`) |
| Public methods per type | ≤ 7 | **2 breaches** | `Authority` **9** (`Accept Admits Bind Each Lookup Scope Sealer Verify With`); `Scope` **9** (6 of them the `String/Format/LogValue/MarshalJSON` redaction protocol — one concern in Go) |
| Fields per type | ≤ 7 | **1 breach** | `tenancydb.Directory` **10** (`database.go:37-49`); `Authority` exactly 7 |
| Exported symbols per package | ≤ 7 | **3 breaches** | `tenancy` **113** (16 types + 12 funcs + 48 methods + 26 consts + 11 vars); `tenancyrow` 20; `tenancydb` 14; `tenancycache` 9; `tenancyjobs` **4**; `tenancystorage` **3** |
| Import cycles | 0 | **0** | `go list ./tenancy/...` resolves |
| Internal imports per file | ≤ 5 | **0 breaches** | max 3 (`tenancyrow/row.go`: crud, security, tenancy) |
| Global mutable state | 0 | **2** | `tenancy/errors.go:34 refusals` (array), `tenancy/outcome.go:30 outcomeOf` (map). Both unexported, write-once at init; a Go `map` cannot be made immutable |

### Coupling

| Package | direct first-party imports | transitive first-party deps | afferent (non-self, non-test consumers) |
|---|---|---|---|
| `tenancy` | 1 (`crud`) | **3** (utils, crud, self) | 5 adapters + example |
| `tenancy/tenancyrow` | 3 | **8** (+ errs, internal/nilvalue, auth, security) | example, integration test |
| `tenancy/tenancydb` | 2 | 4 | **0** |
| `tenancy/tenancyjobs` | 2 | 5 | **0** |
| `tenancy/tenancystorage` | 2 | 5 | example (`Namespace` only; `Store` has **0**) |
| `tenancy/tenancycache` | 2 | 5 | **0** |

`tenancyrow` is the only adapter that pays for `security` (which alone drags in
`auth`, `errs`, `internal/nilvalue`) — exactly what D-116 claims, and it is true:

```
$ go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./tenancy
github.com/frostgrove/vv/utils
github.com/frostgrove/vv/crud
github.com/frostgrove/vv/tenancy
```

### Fan-in of the concrete `*tenancy.Authority`

13 non-test references, in **every one of the 5 adapters** plus the example
(`tenancyjobs/jobs.go:15,27,58,67`, `tenancyrow/row.go:216,247,267`,
`tenancystorage/storage.go:20,28`, `tenancycache/cache.go:25`,
`tenancydb/database.go:27,37`, `_examples/tenancy-sharedrow/main.go:135`).
This is a concrete type across every seam boundary, which the architecture law
normally forbids. **D-117 makes it mandatory** — an interface would be
implementable by application code, which is the exact forgery D-117 exists to
prevent. Recorded as a *documented* exception, not a defect.

### Kernel share of the tenancy feature

```
$ git diff --numstat -- '*.go'      # kernel edits made for tenancy
crud/decorators/faults/faults.go     8 +   0 -
crud/decorators/security/security.go 23 +  3 -
crud/errors.go                       1 +   0 -
crud/executor.go                     5 +  11 -
                                   ---------------
                                    37 +  14 -
```
1770 production lines of extension (`find tenancy -name '*.go' ! -name '*_test.go' | xargs wc -l`), 37 added kernel lines. **Kernel share 2.0 %.**

### Isolation test — fakes needed to unit-test each type

| Type | fakes | verdict |
|---|---|---|
| `Reference` `Epoch` `Purpose` `Lifecycle` `Class` `Admission` `Outcome` | 0 | pass |
| `Authority` | 1 (`Resolver`; `tenancy.Fixed` ships as a canned one) | pass |
| `Scope` `Sealer` | 1 (via `Authority`) | pass |
| `Grant` | 2 (`Resolver` + `Spec.Now`) | pass, at threshold |
| `tenancyrow` `column`/`through`/`Policy` | 2 (`Resolver` + `crudtest.Recorder`) | pass, at threshold |
| `tenancycache.Key`/`Partition` | 1 | pass |
| `tenancystorage.Namespace` | 1 | pass; `Store` = 2 |
| **`tenancydb.Directory`** | **4** (`Resolver`, `Sources`, `Fence`, `Now`) | **fail** |
| **`tenancyjobs.contextProvider`** | 1 hand-written fake, but **7 real collaborators must be assembled** (`jobs.MustWire`, `NewCatalog`, `NamespaceOf`, `ParseIdentityProvenance`, `NewIdentityEpoch`, `jobsmemory.NewDefault`, `NewQueue`+`Activate`) because `jobs.ContextCaptureRequest` has no exported constructor | **fail** |

### Mutation evidence (5 mutations on the frozen snapshot)

| # | Mutation | Result |
|---|---|---|
| M1 | delete `tenancy/tenancydb/database.go:222-224` (the zero-reference/zero-epoch guard) | **SURVIVED** — all 6 packages `ok` |
| M2 | `tenancyrow.classOf` always returns `ClassRead` | KILLED — `contract_test.go:48` |
| M3 | `tenancyrow.relationScopes` ignores `NarrowsRelations()` | KILLED — `row_test.go:314` and `:357` |
| M4 | drop `tenancy.Classify(err)` on the `Sources` factory error (`database.go:188`) | **SURVIVED** — all 6 packages `ok` |
| M5 | (external, not a mutation) an `Ownership` whose `Value` returns `strconv`'s error | **LEAK** — see GAP-3 |

## Findings

### GAP-1 [high][immediate] `jobs` names one extension in its own vocabulary; a second partition axis costs 6 kernel files

- **Where:** `jobs/scope.go:118-146` (`PartitionMode`, `PartitionTenantRequired`,
  `TenantPartitioner`, `TenantPartitionerFunc`), `jobs/catalog.go:118-122`
  (`RequiresTenantPartition`), `jobs/durable_context.go:18,21,26,323,399,405,412,439,473,501-505,549-558,587,602-626,639,712,836`,
  `jobs/queue.go:89`, `jobs/activation.go:35`, `jobs/delivery_record.go:325`.
- **Scale:** systemic — **75 non-test lines** in `jobs/` mention tenant/tenancy
  (`grep -rniE 'tenant|tenanc' --include='*.go' jobs/ | grep -v _test | wc -l` → 75),
  across **6 non-test files**, **22** of them branch or enum sites.
- **Confidence:** CONFIRMED.
  ```
  $ grep -rln 'PartitionTenantRequired\|ContextTenant\|RequiresTenantPartition' --include='*.go' jobs/ | grep -v _test
  jobs/catalog.go
  jobs/activation.go
  jobs/queue.go
  jobs/scope.go
  jobs/delivery_record.go
  jobs/durable_context.go
  $ go doc github.com/frostgrove/vv/jobs PartitionMode   # 2 values, closed
  ```
- **What / Why this severity:** `PartitionMode` is a closed two-value enum whose
  second value is the name of one optional extension. An `audit` extension that
  wants durable records partitioned by *region*, or an `i18n` extension
  partitioned by *locale*, cannot express it: it must add a constant to
  `jobs/scope.go:120`, a branch to `jobs/catalog.go:120`, six branches in
  `jobs/durable_context.go`, and touch `jobs/queue.go:89`,
  `jobs/activation.go:35`, `jobs/delivery_record.go:325`. That is the microkernel
  defining test failing with a number: **adding the second extension of this kind
  costs 6 kernel files.** `jobs.TrustTenantPartitioner`
  (`jobs/durable_context.go:405`) is worse: the kernel ships its *own*
  implementation of the extension point, in the extension's vocabulary, competing
  with `tenancyjobs.ContextProvider`.
- **Project rule check:** `docs/ai/flows/FL-035-…:132` states this deliberately —
  *"A partition is a tenant, not a sequence. `PartitionGlobal` and
  `PartitionTenantRequired` are the whole of `PartitionMode`."* It is a **flow**
  doc (a map), not a **decision** doc (binding law), and it states the closure
  without justifying it against the governing
  `docs/roadmaps/2026-09-01-extension-architecture-roadmap.md`, whose clause 2
  requires base packages to own *dependency-neutral* extension points and whose
  clause 11 requires N extensions to cost N decisions. So: **documented as
  intentional, not decided.** Reported as a finding for the owner, not as an
  undocumented defect.
- **Why this timing:** the audit and i18n roadmaps are both queued against this
  same seam. Renaming the axis after `jobs` is tagged is a breaking change to
  `PartitionMode`, `TenantIdentity`, `DurableContext.Tenant()`,
  `IdentityRestoreRequest.Tenant()` and the on-disk record format
  (`jobs/placement.go:400` writes the literal `"tenant"` into the placement digest).
- **Close criteria:**
  - [ ] a decision doc that either (a) generalises the axis to a named
        `PartitionKind`/`PartitionAxis` owned by the application, or (b) records
        that the axis is permanently tenant-only and states what the audit and
        i18n roadmaps must do instead;
  - [ ] if (a): `grep -c Tenant jobs/*.go` on non-test files drops to 0;
  - [ ] a test that a second, non-tenant partition axis reaches a durable record.

### GAP-2 [high][immediate] `tenancyrow` is the one application-supplied seam that does not `Classify`, and it leaks the tenant reference into a 500

- **Where:** `tenancy/tenancyrow/row.go:29` (`type Value func(tenancy.Reference) (any, error)`),
  returned unwrapped at `row.go:75`, `row.go:86`, `row.go:102`, `row.go:154`, `row.go:162`.
- **Scale:** systemic (5 return sites, 1 exported callback type).
- **Confidence:** CONFIRMED. External module, `Value` written exactly the way
  `docs/modules/en/tenancy.md:170-174` shows it (`strconv.ParseInt`), reference
  `acme-7f3c`:
  ```
  === RUN   TestAValueFunctionsErrorTextTravelsBackToTheCaller
      caller sees: strconv.ParseInt: parsing "acme-7f3c": invalid syntax
      LEAK: the tenant reference reached the caller's error
      tenancy.Classify would have said: tenancy: the tenant capability is unavailable
      tenancy.OutcomeFor says: error
  ```
- **What / Why this severity:** two invariants break at once.
  (1) **Redaction.** `Reference.String()` is `"[tenant reference]"`,
  `MarshalJSON` refuses, `LogValue` redacts — and then the reference walks out
  through the error channel of an application callback the module doc tells the
  consumer to write. D-116 states the rule the code does not follow:
  *"`Classify` is how every seam answers application-supplied code — a resolver, a
  source factory, a fence — without letting its text travel."* Five `Classify`
  call sites exist (`authority.go:85,96`, `context.go:63`, `database.go:188,198`);
  `Value` has none.
  (2) **Status.** The raw error wraps none of `crud.ErrForbidden/ErrBadRequest/…`,
  so `porthttp.StatusFor` (`port/porthttp/errors.go:45-47`) falls to
  `http.StatusInternalServerError`. A tenant whose reference does not parse gets a
  **500 with the tenant name in the body**, where D-008 requires a 404 and
  `TestEveryRefusalIsForbiddenToATransportThatKnowsNothingAboutTenants`
  (`tenancy/scope_test.go:177`) asserts every *declared* refusal is a 403/409.
- **Why this timing:** `Value` is a public contract already documented with a
  worked example; wrapping it later changes what consumers' error handling sees.
- **Close criteria:**
  - [ ] `column.Narrow/Relations/Apply` and `through.Narrow/Relations` route the
        `Value` error through `tenancy.Classify`;
  - [ ] a test in the shape of `TestAResolverFailureNeverTravelsBackAsText`
        (`scope_test.go:144`) for `Value`, asserting the reference does not appear
        in the returned error text and that the result wraps `crud.ErrForbidden`.

### GAP-3 [medium][immediate] The `Ownership`/`security.Policy` seam has no panic policy, and `jobs` proves the project knows it should

- **Where:** `crud/decorators/security/security.go` (0 `recover()`),
  `tenancy/tenancyrow/row.go` (0 `recover()`), against
  `jobs/queue.go:724-737` `invokeContextProvider` and **34** `recover()` sites in `jobs/*.go`.
- **Scale:** local to the seam, systemic in consequence (every gate verb).
- **Confidence:** CONFIRMED.
  ```
  $ grep -c 'recover()' crud/decorators/security/security.go
  0
  $ grep -rn 'recover()' --include='*.go' tenancy/ | grep -v _test | wc -l
  0
  $ grep -rn 'recover()' --include='*.go' jobs/*.go | grep -v _test | wc -l
  34
  ```
  External test, an `Ownership` whose `Narrow` panics:
  ```
  === RUN   TestAPanickingOwnershipStrategy
      NO FAILURE POLICY: the panic escaped the gate to the caller:
      the strategy has a bug and the tenant is acme-7f3c
  ```
- **What / Why this severity:** microkernel requirement 3 (a written failure
  policy) is met for *errors* — the gate fails closed, proven below — and absent
  for *panics*. `jobs` collapses a panicking provider to `ErrDriver`
  (`queue.go:726-731`) precisely so an extension's text cannot travel; the same
  repository's CRUD seam lets a panic message reach whatever recovers it, with
  the tenant reference in it. The inconsistency is the defect, not the choice:
  two extension points of the same system answer the same question two ways and
  neither answer is written down.
- **Why this timing:** it is a contract property of a point the docs explicitly
  invite third parties to implement (`docs/modules/en/tenancy.md:173-176`).
- **Close criteria:**
  - [ ] a decision doc stating whether the CRUD security seam contains extension
        panics, and why it differs from `jobs` if it does not;
  - [ ] if it contains them: a test in the shape of `jobs/queue_test.go:372`.

### GAP-4 [medium][immediate] Two of the three `Classify` guarantees D-116 claims are untested, and one guard is dead code

- **Where:** `tenancy/tenancydb/database.go:188` (Sources), `:198` (Fence),
  `:222-224` (the zero-reference/zero-epoch guard).
- **Scale:** local (3 sites in one file).
- **Confidence:** CONFIRMED by mutation on the frozen snapshot.
  - M4 — delete `tenancy.Classify` from the `Sources` error path:
    ```
    M4 applied: tenancydb.open no longer classifies the Sources factory error
    ok  github.com/frostgrove/vv/tenancy            ok  .../tenancycache
    ok  .../tenancydb   ok  .../tenancyjobs   ok  .../tenancyrow   ok  .../tenancystorage
    ```
    **SURVIVED.** `tenancy/tenancydb/database_test.go` has 16 test functions and
    none of them asserts that a factory error carrying a DSN is collapsed —
    `grep -n 'password\|dsn\|Classify' tenancy/tenancydb/database_test.go` → no hit.
  - M1 — delete the guard at `:222-224`:
    ```
    M1 applied: removed the zero-reference/zero-epoch guard in tenancydb.Directory.scope
    ok  (all six packages)
    ```
    **SURVIVED**, and by reading it is unreachable: `Authority.Scope` → `accept` →
    `minted` → `Scope.boundTo` (`tenancy/scope.go:58`) already returns false when
    `!this.reference.valid() || !this.epoch.valid()`, and `Reference.valid()` is
    false exactly when the value is empty, `Epoch.valid()` exactly when it is zero.
- **What / Why this severity:** D-116 names the `Classify` guarantee for "a
  resolver, a source factory, a fence" as one of the four reasons the split is
  correct. One third of it is proven (`TestAResolverFailureNeverTravelsBackAsText`,
  `scope_test.go:144`); the other two thirds would survive deletion. Concretely: a
  `Sources` implementation that returns `pq: password authentication failed for
  user "tenant_acme"` would hand that string to the caller, and nothing in the
  suite would notice.
- **Why this timing:** it is a stated invariant with no test —
  `restrictions.md §5` makes that `[high]`; the deletable guard is cheap and
  misleads the next reader into thinking `Authority.Scope` can return a zero scope.
- **Close criteria:**
  - [ ] a test that a `Sources` error and a `Fence` error carrying a DSN/credential
        come back as `tenancy.ErrUnavailable` with no original text;
  - [ ] `:222-224` either deleted, or given a test that fails without it.

### GAP-5 [low][deferred] The optionality proof now covers every root package and still no satellite module

**Amended 00:45 — the finding was `[medium][immediate]` against the 107-line
version of `scripts/tenancy_test.go`; that file was rewritten during this audit
and the rewrite closed most of it.** Both states are recorded because the fix is
concurrent work, not something I can treat as settled.

- **Where:** `scripts/tenancy_test.go:49-65` (`TestNoBaseSubsystemDependsOnTheOptionalExtension`),
  `:120-131` (`TestTheExtensionDoesNothingWhenItIsMerelyImported`).
- **Scale:** local (2 test functions); 33 unguarded modules.
- **Confidence:** CONFIRMED, both before and after.

**Before (md5 of the version I first read):** the base-subsystem test enumerated
12 `./<subsystem>/...` patterns, which resolved to **39 of the 44** non-tenancy,
non-scripts root packages:

```
covered: 39
all non-tenancy non-scripts root packages: 44
--- NOT covered by the test ---
github.com/frostgrove/vv/cmd/vv
github.com/frostgrove/vv/cmd/vv-otel-gen
github.com/frostgrove/vv/internal/cachegen
github.com/frostgrove/vv/internal/codegen
github.com/frostgrove/vv/internal/nilvalue
```

`internal/nilvalue` sits inside `crud/decorators/security`'s own dependency graph,
so an import added there would have reached every gate consumer, and the test
would have stayed green.

**After (md5 `79ba45a594e6f21b11dd5fb486f918cf`):** the list is gone. The test now
does `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...`, guards with
`len(edges) < 50`, and asks every package. Verified:

```
$ go list -f '{{.ImportPath}} {{join .Imports " "}}' ./... | wc -l
51
$ go list -f '{{.ImportPath}}' ./... | grep -E '^github.com/frostgrove/vv/(internal|cmd)'
github.com/frostgrove/vv/cmd/vv
github.com/frostgrove/vv/cmd/vv-otel-gen
github.com/frostgrove/vv/internal/cachegen
github.com/frostgrove/vv/internal/codegen
github.com/frostgrove/vv/internal/nilvalue
$ go test -count=1 -v -run 'TestNoBaseSubsystemDependsOnTheOptionalExtension|TestNoTenancyPackageCostsMoreThanTheSeamItNames|TestTheExtensionDoesNothingWhenItIsMerelyImported' ./scripts/
--- PASS: TestNoBaseSubsystemDependsOnTheOptionalExtension (0.08s)
--- PASS: TestNoTenancyPackageCostsMoreThanTheSeamItNames (0.35s)   [5 subtests, all PASS]
--- PASS: TestTheExtensionDoesNothingWhenItIsMerelyImported (0.03s)
ok  	github.com/frostgrove/vv/scripts	0.463s
```

Coverage went from 39/44 to **51/51** root packages. The direct-imports-only
check is sound now and was not before: its justification — *"a transitive edge is
a direct edge somewhere along it"* — holds exactly because every package is
enumerated. The seam test likewise derives its package list from `./tenancy/...`
and now fails a sixth package that names no seam, so the hardcoded 5-row table
that a new adapter had to be added to is gone.

**What remains open:**

1. **0 of 33 satellite modules are checked.** `go list ./...` in the root module
   does not descend into nested modules:
   ```
   $ go list ./crud/... | wc -l
   14
   $ go list ./crud/... | grep -c crudpgx
   0
   ```
   So `crud/adapter/crudpgx`, `jobs/jobspg`, `jobs/jobspg/jobspgfx`,
   `cache/cachefx`, `app/appfx`, `otel` and 27 more can import tenancy and every
   test in this repository stays green. I scanned all 33 by hand
   (`for m in $(find . -name go.mod …); do (cd $m && go list -deps …) | grep -c tenancy; done`):
   **0 depend on tenancy today.** This is a hole in the guard, not a present
   violation — hence `[low][deferred]`.
2. **The "does nothing" grep still cannot see a registry.** It matches
   `^func init\(`, `^var .*= *(os\.Getenv|regexp\.MustCompile)`, `go func\(`
   and `go [a-zA-Z]`. A package-level `var registry = map[string]X{}` passes.
   There are two package-level containers in the extension today —
   `tenancy/errors.go:34 var refusals = [...]error{…}` and
   `tenancy/outcome.go:30 var outcomeOf = map[error]Outcome{…}` — both write-once
   and benign, both invisible to the check.
3. **`crud.MustSchemaOf` is not covered and does not need to be.** `Column`
   (`tenancy/tenancyrow/row.go:51`) and `resolveRelation` (`:191`) call it at
   **construction**, not at import, so importing `tenancyrow` writes nothing into
   `crud`'s process-global `var schemaCache sync.Map` (`crud/meta.go:173`).
   *Wiring* a `tenancyrow.Column[M]` does write there, and also panics on a bad
   field name — which is composition-root behaviour and matches
   `security.Gate`'s own `validate` (`crud/decorators/security/security.go:64-76`).
   The test's claim is about import, and it is true.
- **Why this timing:** deferred — the expensive half is fixed and nothing today
  violates the remaining half.
- **Close criteria:**
  - [ ] the module list is derived (`find . -name go.mod`) so all 34 modules are
        asked, not just the root;
  - [ ] a negative control proving the check fails when the import is added
        (D-116's "Proven by" claims one exists; I saw the mutation being run by
        hand during this audit, not a committed test);
  - [ ] the "does nothing" grep rejects package-level container initialisers, or
        the claim is narrowed in words to "no init, no goroutine, no env read".

### GAP-6 [medium][immediate] D-117 — the binding decision doc — cites 5 files and 4 symbols that do not exist, and the repo's own doc check cannot see it

- **Where:** `docs/ai/decisions/D-117-a-verified-scope-is-minted-never-manufactured.md`,
  "Where it lives" and "Proven by".
- **Scale:** 9 stale citations in one doc.
- **Confidence:** CONFIRMED.
  ```
  MISSING tenancy/jobs.go        MISSING tenancy/row_test.go
  MISSING tenancy/seams_test.go  MISSING tenancy/durable_test.go
  MISSING tenancy/database_test.go
  $ for s in JobContext JobIdentity tokenMAC classify; do grep -rn "\b$s\b" --include='*.go' tenancy/ jobs/; done
  (no output — none of the four exists anywhere)
  ```
  All 15 **test names** D-117 cites do exist; only the paths are wrong (the tests
  live in `tenancy/tenancyrow/row_test.go`, `tenancy/tenancyjobs/durable_test.go`,
  `tenancy/tenancydb/database_test.go`). `Classify` is exported, D-117 writes
  `classify`.
- **Why it is invisible:** `scripts/docs_test.go` has exactly the right check —
  `TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs` (`:515`) — but
  `staleSymbolCitations` (`:548-551`) does
  `files := filesNamed(citation.path, declared); if len(files) == 0 { continue }`.
  **A citation to a file that no longer exists is skipped, not reported.** It
  catches "the symbol moved out of a file that still exists" and misses "the file
  is gone", which is the more common case after a package split — and this package
  was split (D-116 moved the seams out of `tenancy/` into five adapters).
  Both doc tests pass:
  `--- PASS: TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs (0.24s)`.
- **What / Why this severity:** under CLAUDE.md a decision doc is binding law and
  is what the next agent reads instead of re-deriving. D-117 tells that agent the
  durable seam lives in `tenancy/jobs.go` with `JobContext`, `JobIdentity` and
  `tokenMAC`. It lives in `tenancy/tenancyjobs/jobs.go` with `contextProvider`,
  `identityRestorer` and `Sealer.mac`. Microkernel requirement 4 — the version /
  compatibility note for the extension point — is this document.
- **Why this timing:** it is the doc D-116 and FL-033 both defer to; every later
  reader inherits the error.
- **Close criteria:**
  - [ ] D-117's "Where it lives" and "Proven by" name paths that exist
        (`git ls-files`-checkable);
  - [ ] `staleSymbolCitations` reports, rather than skips, a citation whose `.go`
        path matches no file in the tree, with a fixture proving it.

### GAP-7 [medium][deferred] `Authority` carries three responsibilities; `seal.go` already shows the fix and `grant.go` did not take it

- **Where:** `tenancy/authority.go:27-35` + 16 methods across
  `authority.go` (6), `context.go` (4), `grant.go` (5), `seal.go` (1).
- **Scale:** local (1 type).
- **Confidence:** CONFIRMED.
  ```
  $ grep -h 'func (this \*Authority)' tenancy/*.go | wc -l      → 16
  $ grep -h 'func (this \*Authority) [A-Z]' tenancy/*.go | wc -l → 9
  Authority method LOC: context.go 53 + authority.go 59 + seal.go 9 + grant.go 97 = 218
  fields: 7 (resolver admission origin salt durableKey revalidate now)
  ```
- **What / Why this severity:** the one-sentence rule needs two "and"s: *"mints and
  accepts verified scopes, **and** issues and enforces cross-tenant grants,
  **and** hands out a durable sealer."* Thresholds: 9 public methods (> 7), 218
  lines (> 200). It is **not** a distributed god object — every file has a domain
  name, there are no cycles, no shared mutable blob, and `Authority` is immutable
  after `New` — so this is a size/cohesion finding, not a structural one.
  The interesting part is internal inconsistency: `seal.go` extracted the durable
  capability into a **9-line** `Sealer` handle (`Authority.Sealer()` →
  `Sealer{authority}`), which is exactly right. `grant.go` did not, and it is the
  **97-line** half — `Accept`, `Each`, `member`, `holds`, `permittedByGrant` all
  hang off `Authority`, and `permittedByGrant` is reached from
  `context.go:51` on the hot path of every verb. The same `Grants{authority}`
  handle would drop `Authority` to 6 public methods and ~121 lines, both under
  threshold, with no change to the mint.
- **Why this timing:** deferred — it is an internal shape with a public-surface
  cost (`Authority.Accept`/`Each` would move to `Authority.Grants().Accept/Each`),
  so it is cheap now and a breaking change after the first tag. Flag it in the
  plan rather than doing it mid-audit.
- **Close criteria:**
  - [ ] `Authority` ≤ 7 public methods and ≤ 200 lines, or a written justification
        in the plan for the breach;
  - [ ] `docs/api/surface.md` regenerated.

### GAP-8 [medium][deferred] `tenancydb.Directory` is four objects: 10 fields, 11 methods, 4 fakes to test

- **Where:** `tenancy/tenancydb/database.go:36-49` (fields), `:64-78` (`Lease`),
  `:80-284` (methods).
- **Scale:** local (1 type).
- **Confidence:** CONFIRMED — 10 struct fields (threshold 7), 11 methods
  (`Borrow Evict Close Cached` + `reserve open finish scope release sweep unlink`),
  and the isolation test needs `Resolver` + `Sources` + `Fence` + `Now` = **4** fakes
  (threshold 2).
- **What / Why this severity:** the one-sentence rule needs three "and"s:
  *"selects a source per tenant generation, **and** caches it under a bound,
  **and** ages it out, **and** hands out reference-counted leases."* The
  concurrency is genuinely good — the reserve-before-open at `:159-177` and the
  detached open at `:183-201` are the right shapes and are tested
  (`TestNoMoreSourcesAreOpenAtOnceThanTheBudgetAllows`,
  `TestConcurrentBorrowersForOneTenantShareOneOpen`,
  `TestABorrowerThatGivesUpDoesNotFailTheOnesBesideIt`). The cost of the merge is
  not correctness, it is that the cache/TTL/eviction machinery cannot be tested
  without a `Resolver` and a `Sources`, so 16 tests all boot the whole directory.
  A `bindingCache` holding `{max, ttl, now, mutex, entries, closed}` with
  `reserve/finish/release/sweep/unlink/Evict/Close/Cached` would be testable with
  **1** fake (a clock), leaving `Directory` with `{authority, sources, fence,
  openTimeout, cache}` = 5 fields and `Borrow/open/scope`.
- **Why this timing:** deferred — entirely internal; `Directory`'s public surface
  (`NewDirectory`, `Borrow`, `Evict`, `Close`, `Cached`, `Lease`) does not change.
- **Close criteria:**
  - [ ] ≤ 7 fields, ≤ 2 fakes for each resulting type, or a written justification;
  - [ ] the existing 16 tests still pass unchanged.

### GAP-9 [medium][deferred] One test concept, two names, six copies

- **Where:** `tenancy/scope_test.go:18-46`, `tenancy/tenancyrow/support_test.go:11-39`,
  `tenancy/tenancydb/support_test.go:13-41` (variant A: `reference` +
  `authorityFor` + `activeAuthority`); `tenancy/tenancyjobs/support_test.go:12-40`,
  `tenancy/tenancystorage/storage_test.go:13`, `tenancy/tenancycache/cache_test.go:12`
  (variant B: `fixedAuthority` / `authorityOnly`). Plus the same canned resolver
  under three names: `knownTenants` (`tenancy/support_test.go:5`,
  `tenancy/tenancyjobs/support_test.go:42`) and `directory`
  (`tenancy/tenancydb/support_test.go:65`).
- **Scale:** systemic (6 copies of the helper, 3 of the fake).
- **Confidence:** CONFIRMED — `tenancyrow/support_test.go` lines 1-41 and
  `tenancydb/support_test.go` lines 1-41 differ **only** in the package clause and
  two extra imports:
  ```
  $ diff <(sed -n '1,41p' tenancy/tenancyrow/support_test.go) <(sed -n '1,41p' tenancy/tenancydb/support_test.go)
  1c1
  < package tenancyrow_test
  ---
  > package tenancydb_test
  3a4,5
  >       "context"
  >       "fmt"
  ```
- **What / Why this severity:** the counter-rule in `architecture.md` (do not merge
  what merely looks alike) does **not** apply: a future change to how an
  `Authority` is built for a test would have to change all six together, which is
  the definition of shared meaning. Go's package-scoped test helpers are the
  reason it happened; `crud/crudtest` is this repository's own precedent for the
  cure. Consequence today is small (drift between variant A and B: A takes an
  `Admission`, B takes a generation and returns a `Scope`).
- **Why this timing:** deferred — test-only, no external contract. But a shared
  kit would be a **new published package**, which D-116 constrains, so it is a
  plan decision and not a drive-by.
- **Close criteria:**
  - [ ] one helper for "an `Authority` over a canned resolution", used by all six
        packages, or a written note that the duplication is preferred to a
        published `tenancytest` package.

### GAP-10 [low][deferred] Three of five adapters have no consumer anywhere, and the example's own doc comment overstates it

- **Where:** `_examples/tenancy-sharedrow/main.go:1-3` and its imports at `:16-19`.
- **Scale:** local.
- **Confidence:** CONFIRMED.
  ```
  tenancyrow:     _examples/tenancy-sharedrow/main.go, test/integration/tenancy_test.go  (+ own tests)
  tenancydb:      own tests only
  tenancyjobs:    own (internal) tests only
  tenancystorage: _examples (Namespace only; Store has no caller anywhere)
  tenancycache:   own (internal) tests only
  ```
  The file's package comment says it turns one verified tenant into *"a narrowed
  statement, a namespaced bucket, a partitioned cache and a durable job"*. It
  imports `tenancyrow` and `tenancystorage` and neither `tenancycache` nor
  `tenancyjobs`. `make examples` builds it clean.
- **What / Why this severity:** `architecture.md`'s Protected Variations
  counter-balance says an abstraction over a variation that never gets a second
  instance is added complexity. Three adapters (14+4+9 = 27 exported symbols) have
  no proven composition. `tenancystorage.Store` (`storage.go:28`, 5 parameters —
  over the 4-parameter threshold) has zero callers including tests, so its
  signature has never been exercised by anyone but its author. This is **low**
  because the packages are each 51-107 lines and unit-tested, and because the
  roadmap already lists S4 (database-per-tenant) as `[~]`.
- **Close criteria:**
  - [ ] the example wires the four seams its comment claims, or the comment names
        the two it wires;
  - [ ] `tenancystorage.Store` gets a caller or is dropped.

### GAP-11 [low][deferred] `tenancyrow.Policy` returns a value a consumer can widen; `Combine` cannot, `Repository` cannot

- **Where:** `tenancy/tenancyrow/row.go:216-238` returning
  `security.Policy[M,ID]` (11 public fields, `security/security.go:27-49`).
- **Scale:** local.
- **Confidence:** CONFIRMED, external module:
  ```
  policy := tenancyrow.Policy[Invoice,int64](a, tenancyrow.Column[Invoice]("TenantID", …))
  policy.AllowUnscopedScope = true
  policy.Scope = func(context.Context) (crud.Predicate, error) { return nil, nil }
  → unbound read: err=<nil> rows=1
    sql="SELECT \"id\", … FROM \"invoices\""     (no WHERE at all)
  ```
- **What / Why this severity:** D-117's headline invariant is *"there is no path
  that succeeds without a scope."* It holds for `tenancyrow.Repository` and it
  holds through `security.Combine` — I read `policies.go:193-238`: `Combine` ANDs
  the scopes and sets `AllowUnscopedScope` to the **conjunction** of every
  contributor's flag, so combining tenancy (false) with a permissive policy still
  refuses. It does **not** hold for a consumer who assigns the field directly.
  That is a two-line, deliberately-named ("AllowUnscoped…") act, which is why this
  is `low` and not `high` — but it is neither documented nor tested, and
  `docs/modules/en/tenancy.md:196-201` teaches `Policy` as the composable entry
  point without saying that the value it hands back is open.
- **Close criteria:**
  - [ ] one sentence in `docs/modules/en/tenancy.md` and `ru/tenancy.md` saying
        `Repository` is the closed form and `Policy` is a value the caller owns;
  - [ ] a test pinning that `Combine` does not widen `AllowUnscopedScope`.

## Answers to the questions asked

**Kernel purity — can a second extension be added with zero kernel diffs?**

For the **CRUD**, **cache** and **storage** seams: **yes.** For **`jobs`**: **no** —
see GAP-1. Classification of every kernel hook tenancy touches:

| Hook | Where | Made *for* tenancy? | Classification |
|---|---|---|---|
| `crud.UnscopedExister`, `crud.ExistsUnscopedOf` | `crud/executor.go:135-148` | pre-existed; **rewritten** for tenancy (D-115) | **general** — restores the exact-outer rule of D-061 to the one helper that broke it; a `security` gate, a `faults` enricher and any future narrowing decorator get the same answer. No tenancy name, no tenancy branch |
| `crud.ErrNoUnscopedExists` | `crud/errors.go:14` (+1 line) | yes | **general** — joins the existing `ErrNoBatchInsertSupport` / `ErrNoCreateSupport` / `ErrNoReplaceSupport` capability family |
| `security.gate.ExistsUnscoped` | `crud/decorators/security/security.go:377-394` (+20) | yes | **general** — "answer inside my own scope"; any second narrowing decorator inherits it |
| `faults.enricher.ExistsUnscoped` | `crud/decorators/faults/faults.go:244-251` (+8) | yes | **general** — D-030's decorator obligation, identical in shape to its `InsertBatch` |
| `security.Policy[M,ID]` | `security/security.go:27-49` | **no** — long pre-dates tenancy | **general** — 11 optional hooks; tenancy fills 4 |
| `crud.RelationScopes` | `crud/relation*.go` | **no** — first touched 2 years of commits before tenancy (`git log -S RelationScopes` → 14 commits, earliest `29e324e feat: tests added`) | **general** |
| `cache.Partitioner[K]`, `cache.Partitioned` | `cache/` (commit `9f9e715 feat(cache): add typed cache foundation`) | **no** | **general** — no tenancy vocabulary; `cache`'s own `ResourceTenant` is an unrelated concept (a co-tenant of a shared Redis, `cache/resource.go:9-14`) |
| `storage.Namespace`, `ParseNamespace` | `storage/` | **no** | **general** |
| `jobs.TrustedContextProvider`, `TrustedIdentityRestorer` | `jobs/durable_context.go` (commit `ba6f93f feat(jobs): add typed durable foundation`) | **no** — pre-existed | **general interface, tenancy-shaped surroundings** — the interfaces are neutral; their inputs and outputs are not |
| `jobs.PartitionMode{PartitionGlobal, PartitionTenantRequired}`, `TenantPartitioner`, `TrustTenantPartitioner`, `TenantIdentity`, `ContextTenant`, `RequiresTenantPartition`, `ContextCaptureSpec.Tenant` | 6 files, 22 sites | **no** — pre-existed | **tenancy-shaped hook. GAP-1** |

Kernel diff to add tenancy: **4 files, +37/−14 lines, 2.0 % of the feature.**
Kernel diff to add a *second* CRUD-side extension (audit row filtering): **0**
production files — it writes its own `security.Policy` and composes with
`security.Combine`. Kernel diff to add a second *durable-identity* extension:
**6 files in `jobs/`.**

One non-production cost per new extension: `scripts/tenancy_test.go` is 107 lines
named after one extension, with three hand-maintained lists (12 subsystems at
`:35-39`, 5 adapter/seam pairs at `:56-65`, 3 regexes at `:102`). A second
extension clones it, exactly as `scripts/otel_release_test.go` (56 lines) already
did for OTel. `scripts/checks.sh:18 SUBSYSTEMS` grew by one word — that one is a
general mechanism.

**Optionality — does `scripts/tenancy_test.go` prove what it claims?**

Re-ran: all three PASS (output in GAP-5). The `no base subsystem depends` claim
covers **39 of 44** root packages and **0 of 33** satellite modules; I checked the
missing 38 by hand and none depends on tenancy today. The `importing does nothing`
claim greps for `init()`, `os.Getenv`/`regexp.MustCompile` package vars, and
`go func(` — it does **not** cover a package-level registry (two package-level
containers exist: `tenancy/errors.go:34`, `tenancy/outcome.go:30`; both are
write-once and benign). On `crud.MustSchemaOf`: the claim is safe and the test is
right not to mention it — `Column` (`row.go:51`) and `resolveRelation`
(`row.go:191`) call it at **construction**, so importing `tenancyrow` writes
nothing into `crud/meta.go:173 var schemaCache sync.Map`; *wiring* a
`tenancyrow.Column[M]` does, and it also **panics** on a bad field name, which is
composition-root behaviour and consistent with `security.Gate`'s `validate`
(`security.go:64-76`). Detail in GAP-5.

**Building blocks — every exported type classified.**

The five-block vocabulary (VO / Command / UseCase / App-UseCase / Repository) is
written for an application with bounded contexts. This is a Go framework library
with no contexts, no usecases and no database of its own; forcing the labels would
be dishonest. What transfers is the *substance*: immutability, validation at
construction, no I/O inside values, and who owns a contract. Judged on that:

| Symbol | Kind | Verdict |
|---|---|---|
| `Reference` (`reference.go:13`) | **VO** | ✓ immutable, `ParseReference` validates, no I/O, owns its own rendering (`String`/`Format`/`LogValue`/`MarshalJSON` all redact) |
| `Epoch` (`reference.go:55`) | **VO** | ✓ `NewEpoch` refuses 0 |
| `Purpose` (`grant.go:21`) | **VO** | ✓ |
| `Lifecycle`, `Class` (`lifecycle.go:3,38`) | **VO / enum** | ✓ closed vocabulary + `Valid()`; an undeclared value is refused rather than admitted (`mint` at `authority.go:105-113`) |
| `Admission` (`lifecycle.go:63`) | **VO** | ✓ immutable bitset; `Merge` returns a new value |
| `Scope` (`scope.go:27`) | **VO with an authenticity tag** | ✓ immutable, unexported fields, constructible only by `Authority.mint`; 9 public methods (over threshold, 6 of them the redaction protocol) |
| `Grant` (`grant.go:47`) | **VO / capability token** | ✓ MAC-bound over every member and the deadline; `holds` re-verifies |
| `Resolution` (`scope.go:11`) | **DTO, deliberately not a VO** | acceptable — it is the `Resolver`'s return value and D-117 requires it be plain data. Its three fields are each self-validating VOs; the only invalid combination is a zero field, caught at the single ingress (`resolution.valid()` in `mint`) |
| `Member` (`grant.go:69`) | **output DTO** | ✓ |
| `Outcome` (`outcome.go:5`) | **closed enum** | ✓ 12 constants; `OutcomeFor` never reads an error's text |
| `Spec` (`authority.go:18`), `DirectorySpec` (`database.go:26`) | **parameter objects** | ✓ validated by their single consumer (`New`, `NewDirectory`) — a Go struct literal cannot validate itself |
| `Authority` (`authority.go:27`) | **domain service / aggregate root** | none of the five. 3 responsibilities → **GAP-7** |
| `Sealer` (`seal.go:22`) | **capability handle** | ✓ the right shape; 9 lines, 2 methods, cannot be forged (`Seal` refuses a scope this authority did not mint) |
| `Resolver` (`authority.go:13`) | **plugin contract** | ✓ declared by the kernel that calls it — correct for microkernel, and the reason `Resolver` returns a `Resolution` and never a `Scope` |
| `Fixed` (`authority.go:143`) | **canned `Resolver`** | ✓ the one thing that lets every seam be tested with 1 fake |
| `Ownership[M]` (`tenancyrow/row.go:21`) | **strategy contract** | ✓ — externally implementable, proven below |
| `Value` (`row.go:29`), `Relation` (`:38`), `Mode` (`:14`) | callback / VO / enum | `Value`'s errors are unclassified → **GAP-2** |
| `Policy`, `Repository` (`row.go:216,267`) | **factories** | ✓; `Policy` returns an open value → GAP-11 |
| `Sources`, `SourcesFunc` (`database.go:16,20`) | **plugin contract** | ✓ |
| `Directory` (`database.go:36`) | **resource pool** ≈ Repository | 4 responsibilities → **GAP-8** |
| `Lease` (`database.go:64`) | **handle** | ✓ `sync.Once`-guarded `Release`, nil-safe |
| `tenancycache.Key[K]` (`cache.go:17`) | **VO** | ✓ carries its own checked scope; `Keyed` is the only constructor |
| `tenancyjobs.ContextProvider` / `IdentityRestorer` (`jobs.go:15,58`) | **adapter factories** | ✓; 0 exported types, 4 exported symbols total — the tightest package in the extension |
| `tenancystorage.Namespace` / `Store` (`storage.go:20,28`) | **derivation functions** | `Store` has 5 params and no caller → GAP-10 |

Contract-ownership direction is right everywhere: `tenancy` declares `Resolver`,
`tenancyrow` declares `Ownership`, `tenancydb` declares `Sources` — each is the
*extension point of the package that calls it*, which is what microkernel law
requires and what the literal "provider declares the interface" rule would
misread. No ORM or driver type crosses any boundary: `crud.Source` is an
interface, `crud.Predicate` is opaque (`Has unexported methods`).

**Is `Authority` a god object?** No — measured. 16 methods (9 public), 7 fields,
218 lines, fan-in 5/5 adapters, fan-out 0 first-party (it imports only stdlib +
`crud` for the error taxonomy). It is immutable after `New`. It fails the
one-sentence rule with three responsibilities, and `grant.go` (97 of the 218
lines) is the one that should have become a handle the way `Sealer` did. That is
GAP-7, `[medium][deferred]` — a size finding, not a structural one.

**Cycles and injection.** 0 cycles. Everything that defines behaviour is injected:
`Resolver`, `Admission`, `Origin`, `DurableKey`, `Revalidate`, `Now` via
`tenancy.Spec`; `Authority`, `Sources`, `MaxCached`, `TTL`, `Fence`,
`OpenTimeout`, `Now` via `DirectorySpec`. Nothing reads an env var
(`grep -rn 'os.Getenv' tenancy/` → nothing). The one non-injected dependency is
the `crypto/rand` salt at `authority.go:48-51`.

**Is the salt a testability problem?** Measurably small, and paid for. It cannot
be injected without reintroducing exactly what D-117 forbids — a way for
application code to choose the salt is a way to mint a scope. Its cost is that
`bind(salt, origin, resolution)` can only be exercised from inside the package, so
**3 of 8 core test files are `package tenancy`**: `binding_test.go` (needs `bind`
and a fixed salt to isolate `Origin` from the salt — the test says so at
`binding_test.go:8-13`), `grant_binding_test.go` (needs `Grant.binding` and
`holds`), `seal_test.go` (needs `mint`). The other 5 are external. That is the
right trade and it is written down. `Spec.Now` being injectable is what makes
`grant_test.go` able to move a deadline without sleeping.

**The `Ownership[M]` extension point — is it real?** **Yes. Verified by building
two third strategies outside the package, in a separate module, using only
exported API.**

`Ownership[M]` has five exported methods and no unexported ones, and every type in
its signature is exported (`tenancy.Reference`, `crud.Predicate`,
`*crud.RelationScopes`, `crud.Action`). `Column` and `Through` return the
*interface*, not a concrete type — the unexported `column[M]`/`through[M]` are
behind it — so `Policy[M,ID](authority, ownership)` accepts anything satisfying
it. Both strategies compiled and ran first try:

```
=== RUN   TestAnExternalCompositeOwnershipNarrows
    composite SQL: SELECT "id","region","tenant_id","attrs","number" FROM "invoices"
                   WHERE ("region" = $1 AND "tenant_id" = $2)  args=[eu acme-7f3c]
--- PASS
=== RUN   TestAnExternalJSONBOwnershipNarrows
    jsonb SQL: SELECT … FROM "invoices" WHERE attrs ->> 'tenant' = $1  args=[acme-7f3c]
--- PASS
```

Exported API needed and present: `crud.MustSchemaOf`, `Schema.Field`,
`Schema.Pointers`, `crud.Field.Name`, `crud.Eq`/`And`/`Raw`, `crud.EqualValues`,
`crud.ElemValue`, `RelationScopes.AtPath`, `security.Denied`, `crud.SchemaError`.

**What is missing:**
1. `resolveRelation` (`row.go:191-214`) is unexported. An external strategy that
   wants `Through`-style relation-path validation must reimplement the walk over
   `Schema.Relation` / `Relation.LocalField` / `Relation.Elem` /
   `crud.SchemaOfType` — all of which *are* exported, so it is possible but
   duplicated (24 lines).
2. `assign` (`row.go:118-131`) and `orDefault` (`row.go:31-36`) are unexported;
   both are ~10 lines to reproduce.
3. **No version/compatibility note.** `Ownership` is a Go interface with no
   embedded-struct escape hatch: adding a sixth method breaks every external
   implementation at compile time, and nothing says whether that may happen.
4. **No panic policy** — GAP-3.

**What is right:** the point **fails closed** against three hostile external
implementations, with zero statements reaching the database:

```
tautology (Narrow → nil,nil)      → security: forbidden: read: scope returned no narrowing;
                                     set AllowUnscopedScope only for an intentional unrestricted principal
                                     statements=0
liar (NarrowsRelations→true,       → security: forbidden: read: relation scopes returned no narrowing
      Relations→nil,nil)              statements=0
broken (Narrow → error)            → the strategy's error, statements=0
```

The second one matters: it is the exact silence the comment at `row.go:240-246`
says the design exists to prevent, and the base gate catches it. (The third line
is GAP-2: the error itself is not classified.)

**`tenancydb` vs `tenancyrow` symmetry — is there a missing shared abstraction?**

**No. The duplication is correct and I would refuse a merge.** Judged, not assumed:

| | `tenancyrow` | `tenancydb` |
|---|---|---|
| shape | stateless factory → `security.Policy` → `crud.Middleware` | stateful pool with a mutex, TTL, borrower counts, `Close` |
| lifecycle | none | `NewDirectory` … `Close`, leases |
| production LOC | 269 | 284 |
| seam | `crud/decorators/security` (8 transitive deps) | `crud` (4) |
| what it produces | a `WHERE` clause per call | one `crud.Source` per {tenant, epoch} |
| unit of variation | `Ownership[M]` — 2 built-in, third-party implementable | `Sources` — the deployment's directory |

The only shared code is **six lines**: "resolve the scope for the class, then read
`Reference()`/`Epoch()`." That already lives in the core as
`Authority.Scope(ctx, class)` (`context.go:39`), which both call. Extracting
anything more would mean inventing a common supertype over "a predicate" and "a
database handle", which share no meaning and no future requirement. The
`architecture.md` counter-rule applies exactly: shared coincidence is not shared
meaning.

Two asymmetries are *not* justified, and both are already findings:
`tenancydb` classifies its application-supplied callbacks and `tenancyrow` does
not (**GAP-2**), and `tenancydb.Directory.scope` (`:214-226`) re-checks what
`Authority.Scope` already guarantees, unreachable and unkilled by mutation
(**GAP-4/M1**).

Third observation on D-116's *"an adapter costs one seam"* claim: it is true and
measured, and the split is genuinely load-bearing —
`go list -deps ./tenancy` returns 3 first-party packages while
`go list -deps ./tenancy/tenancyrow` returns 8. A deployment that partitions a
cache and nothing else really does avoid `auth`, `errs` and `internal/nilvalue`.

## Remediation order

Boundaries and contracts before internals; a leak before a shape.

1. **GAP-1** — decide the `jobs` partition axis. **L**, blast radius: `jobs` public
   API + the durable record digest + the audit and i18n roadmaps. Everything else
   about `jobs` is downstream of this answer, and it gets more expensive with every
   week `jobs` is closer to a tag. Nothing below depends on it.
2. **GAP-2** — classify `tenancyrow.Value` errors. **S** (5 call sites + 1 test),
   blast radius: the error a consumer sees from a misconfigured `Value`. Do this
   before GAP-3, because it fixes the common case and narrows what a panic policy
   would have to cover.
3. **GAP-4** — test the two unproven `Classify` guarantees; delete or test the dead
   guard. **S**, blast radius: none. Same shape as GAP-2's test, so write them
   together.
4. **GAP-3** — decide and document the panic policy for the CRUD security seam.
   **M**, blast radius: every gate verb. Depends on 2 and 3 being settled, because
   the answer is "the same thing `Classify` does" or "nothing, deliberately".
5. **GAP-6** — fix D-117's 9 stale citations **and** close the
   `staleSymbolCitations` hole. **S**, blast radius: every future reader. Do the
   tooling half in the same change or the next split repeats it.
6. **GAP-11**, **GAP-10** — two doc sentences and one example comment. **S**.
7. **GAP-5 (remainder)** — ask all 34 modules, not just the root; widen the
   "does nothing" grep. **S**, blast radius: `scripts/`. Independent. The
   expensive half landed at 00:45 while this audit was running.
8. **GAP-7**, **GAP-8** — extract `Grants` from `Authority` and `bindingCache` from
   `Directory`. **M** each, blast radius: GAP-7 changes the public surface
   (`Authority.Accept`/`Each`), so it must land before the first tag or never;
   GAP-8 is internal and can wait. Both depend on 1-6 being settled, because a
   refactor over an unfixed leak just moves the leak.
9. **GAP-9** — one shared test kit, or a written note preferring the copies. **S**,
   blast radius: tests only. Last, and only if 8 is done, since the refactors
   change what the helpers build.

## What I did not check

- **Laws 4-9.** Universality (hardcode), data integrity/concurrency, DRY in
  production code beyond what bore on structure, readability, restrictions, and
  the test dimension proper. I ran 5 mutations as *architecture* evidence, not as
  a test-suite audit; the plan records a prior review where 32 of 58 mutations
  survived, and I did not re-run it.
- **`test/integration/`.** Never executed — it needs Docker (`make up`), and the
  memory note records that this suite reports success without reaching PostgreSQL
  when port 55432 is taken. I read `test/integration/tenancy_test.go` (165 lines,
  2 functions, `tenancyrow` only) and counted it; I did not run it.
- **`make check` / `make vet` / `make api` / `make unit`** across all 34 modules.
  I ran `make examples` (green) and targeted `go test` runs only.
- **The concurrency in `tenancydb`** beyond counting fields, methods and fakes. The
  reserve-before-open and detached-open shapes look right and have named tests, but
  correctness under `-race` at N replicas is a data-integrity question (law 5) and
  I did not run `-race`.
- **`tenancy/seal.go` and `tenancy/grant.go` cryptographic construction.** I read
  the length-prefixing and confirmed every field is inside each MAC; I did not
  audit it as cryptography.
- **The Russian documentation** (`docs/modules/ru/tenancy.md`, 364 lines) — I read
  only the English reference and checked that both exist.
- **`docs/api/surface.md`** — confirmed the tenancy sections exist at lines 1757,
  1796, 1805, 1816, 1822, 1835; did not diff them against the current surface
  (`make api` not run).
- **Whether the 00:45 rewrite of `scripts/tenancy_test.go` is final.** I measured
  it, ran it and amended GAP-5 against it; the file may move again. Its md5 at the
  time of measurement is `79ba45a594e6f21b11dd5fb486f918cf`.
- **Whether the transient red at 00:15-00:19 corresponds to a defect anyone else
  can reproduce.** I could not: the source hash I hold now is byte-identical to
  what I read before the failures, the failure did not reproduce in a standalone
  program against the same package, and the tree was being written to throughout.
  I have recorded it exactly and drawn no conclusion from it.
