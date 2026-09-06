# tenancy core - D2: hard restrictions (errors, determinism, config, data safety, observability) + state machines - AUDIT (2026-09-06)

Scope audited: `tenancy/authority.go`, `scope.go`, `reference.go`, `context.go`,
`lifecycle.go`, `grant.go`, `seal.go`, `errors.go`, `outcome.go` and
`tenancy/{scope,grant,grant_binding,seal,binding,contract,vocabulary,support}_test.go`.
Seam packages (`tenancyrow`, `tenancydb`, `tenancyjobs`, `tenancystorage`,
`tenancycache`) were read only where they answer a question about the core contract.

**Snapshot discipline.** Another agent was editing the working tree during this audit
(`tenancy/tenancystorage/storage.go` and `tenancy/tenancyrow/row.go` were observed
mid-mutation, and `zz_audit_test.go` files appeared in a shared scratch copy). All
findings below were produced against a private snapshot whose nine scope files were
verified byte-identical to the working tree by `sha256sum` before the run and again
after it:

```
$ sha256sum -c scope_checksums.txt
tenancy/authority.go: OK   tenancy/scope.go: OK      tenancy/reference.go: OK
tenancy/context.go: OK     tenancy/lifecycle.go: OK  tenancy/grant.go: OK
tenancy/seal.go: OK        tenancy/errors.go: OK     tenancy/outcome.go: OK
```

Probe programs: `/tmp/claude-1000/-home-user-ws-gd-lease/82a92a15-ac6c-4104-9609-680bbbcbfeb1/scratchpad/probes/`
and `.../scratchpad/d2only/fw/tenancy/zz*_test.go`. Nothing in the repository was modified.

---

## Map

**Entry points.** `Authority.Verify(ctx,class)` (resolver answers who is calling) and
`Authority.Lookup(ctx,ref,class)` (name a tenant) are the two mints; both funnel into
the unexported `mint` (authority.go:104). `Bind` = `Verify` + `With`. Everything
downstream reads through `Authority.Scope(ctx,class)` (context.go:39) or, in one
place, the exported unchecked `From(ctx)` (context.go:77).

**Composition.** No container. `Spec` -> `New`/`Must` is the whole configuration
surface; the consumer wires it (`_examples/tenancy-sharedrow/main.go:136`). `Now` is
injected; the 32-byte HMAC `salt` is drawn ad hoc from `crypto/rand` inside `New`
(authority.go:48-51) and is per-`Authority`, not per-process.

**Trust primitives.** Three independent MACs, all HMAC-SHA256, all length-framed by
`writeField`/`writeBytes` (scope.go:97-104):
- `bind(salt, origin, resolution)` -> `Scope.binding` (scope.go:65) - keyed by the
  random per-authority salt. Verified by `boundTo` with `hmac.Equal`.
- `grantBinding(salt, origin, purpose, cohort, classes, until)` (grant.go:185) - same key.
- `Sealer.mac(fields, epoch, reference)` (seal.go:83) - keyed by the configured
  `Spec.DurableKey`, because the verifying process is not the sealing process.
Plus one unkeyed identity digest: `Scope.Digest()` = SHA-256(len||reference||epoch)
(scope.go:84), used by `tenancystorage` and `tenancycache` for namespaces.

**State.** The core holds none per request. Package-level state is
`errors.go:10` (sentinels), `errors.go:34` (`refusals` array) and `outcome.go:30`
(`outcomeOf`, a package-level mutable map, read-only in practice). No DB, no
migrations, no queue, no jobs, no models, no I/O, no logging, no env reads:

```
$ rg -n 'os.Getenv|os.Open|net/http|log\.|fmt.Print|panic\(' <the nine scope files>
tenancy/authority.go:73:		panic(err)          # Must(), the composition-root form
tenancy/{grant,reference,scope}.go: LogValue() redaction guards only
$ rg -n 'TODO|FIXME|HACK|nolint' tenancy/        -> no matches
```

**"State machine".** `Lifecycle` (7 states, lifecycle.go:5-13) is *not* a state machine
owned here: the control plane owns the transitions and the core only *admits*. The
admission whitelist is a `[3]uint8` bitset (`Admission`, lifecycle.go:63) with one
construction-time consistency rule (`inconsistent`, lifecycle.go:109). There is no
transition table, and correctly so - but the ordinals are load-bearing (`Valid()`
uses `>=`/`<=`, `Admit` uses `1 << state`), and nothing pins that.

**Tests.** 38 test functions across 7 files in the core, ~1180 lines. `go test
./tenancy/...` green. No integration dependency, no sleeps, no network.

---

## Scorecard

| Law (restrictions.md / data-integrity.md) | Verdict | Evidence |
|---|---|---|
| 2. Failure and error policy | **at risk** | `Classify` redaction works (probe: DSN dropped) but is **untested** (mutation R4 survived); `Classify(ErrMalformed)` loses the only `crud` kind the error had -> HTTP 500 for a client error; partial cohort runs report only what was attempted |
| 3. Determinism and reproducibility | **pass** | `Now` injected; MACs deterministic (`bind`/`grantBinding`/`mac` verified reproducible); cohort order is insertion order after dedupe; no map-order dependence in logic; salt non-determinism is the security property and is documented in D-117 |
| 4. Configuration and dependencies | **fail** | `fmt %+v` / `slog` of `*Authority` and `Spec` prints the **durable key and the salt in full**; a 0-byte `DurableKey` fails late as `ErrUntrusted` instead of at startup; a mistyped `Admit(...)` silently widens to all three classes |
| 5. Tests | **fail** | 20 mutations, **9 survived**, including the resolver-redaction rule, the whole write-without-read construction check, the durable-key minimum and the resolver-substitution guard; `TestADeploymentWithNoDurableKeySealsNothing` (seal_test.go:48) passes under either behaviour |
| 7. Explainability / observability | **at risk** | refusals are a closed vocabulary (good), but `ErrCapacity`/`ErrUnavailable` carry no `crud` kind -> `errs.KindInternal` -> 500 with no retry signal; `Each` cannot tell a caller which cohort members were never attempted |
| 8. Data safety | **at risk** | `Spec.DurableKey` is defensively copied (authority.go:64) and no input is mutated; but `Sealer.Unseal` copies an unbounded token before rejecting it, and `Each` has no idempotency or leader-election story |
| DI: state machines / ambiguous state | **at risk** | `Lifecycle` correctly has no local transition table; but the unit-of-work **pin does not include the lifecycle**, so the same ctx moves between admission policies mid-flight, and under `Revalidate` the returned scope reports a lifecycle the control plane no longer holds |
| DI: concurrency / N replicas | **at risk** | `Grant` is immutable and race-free (verified under `-race`); `Authority.Each` is not guarded at all - four concurrent runs entered every tenant four times |
| Forgery of `Scope` / `Grant` / seal framing | **pass** | `bind`, `Digest` and `mac` are all injectively framed; verified by exhaustive small sweeps and by mutation (R20 killed, R9 killed) |

---

## Direct answer to the question

**Can a scope, a grant or a seal be forged?** No. Framing, keying and comparison are
correct. Every framing mutation was killed or proved collision-free.

**Replayed?** Yes, three ways, all confirmed:
1. a `Scope` **value** copied out of a context has no expiry and no unit-of-work
   binding - it re-binds into an unrelated request years later (GAP-3);
2. a sealed token is a pure function of (key, binding fields, epoch, reference) with
   no nonce and no expiry, so it replays within its binding-field granularity (GAP-6);
3. a ctx captured inside `Each` keeps working after the run - this one is *deliberate*
   and tested (grant_test.go:185), bounded by `grant.until`.

**Widened?** Yes, two ways, both confirmed:
1. `From(ctx)` + `Sealer.Seal` turns any minted scope into a durable identity with no
   class check and no grant check - a read-only cohort grant produces write-class
   durable work (GAP-1);
2. a mistyped `Admit(...)` collapses to the zero `Admission`, which `New` silently
   replaces with `AdmitAll(Active)` - read, write **and** durable (GAP-2).

**Survive past the moment it should stop being valid?** Yes: (a) by default
(`Revalidate:false`) forever, for the life of the `Authority` (GAP-3); (b) the
lifecycle half of the pin is not enforced, so a suspended tenant's bound work becomes
writable the instant anything re-verifies (GAP-4).

---

## Metrics

| Metric | Threshold | Actual | Worst offenders |
|---|---|---|---|
| files > 400 lines (scope) | 0 | 0 | `grant.go` 228, `authority.go` 152, `lifecycle.go` 116 |
| functions > 50 lines (scope) | 0 | 0 | `Accept` grant.go:79-111 (33), `Unseal` seal.go:61-81 (21) |
| nesting depth > 3 | 0 | 0 | max 3 (`Accept`'s cohort loop) |
| env reads / secrets in code | 0 | 0 | none |
| I/O, logging, vendor SDKs in core | 0 | 0 | stdlib + `crud` only |
| `TODO`/`nolint`/skipped tests | 0 | 0 | none |
| **mutations that survived** | 0 | **9 / 20** | R4 Classify, R7 `inconsistent()`, R5+R6 config guards, R18 resolver substitution, R16 cohort ceiling, R10 seal field count, R1+R2 constant-time, R3 length guard |
| **types that render a secret under `%+v`** | 0 | **2** | `Authority` (salt + durableKey), `Spec` (DurableKey) |
| exported funcs taking a bare `Scope` and doing security work | 0 (per D-117) | **1** | `Sealer.Seal` seal.go:41 |
| refusal sentinels with no `crud` kind | 0 | **2** | `ErrCapacity`, `ErrUnavailable` (errors.go:29,31) -> HTTP 500 |
| stated invariants with no test | 0 | **4** | write-without-read refusal, durable-key minimum, resolver-substitution guard, sentinel-wrapping redaction |
| doc claims contradicted by code | 0 | **3** | module doc:121-123 (durable/read), module doc:224-230 (`no scope-taking variant`), module doc:132-134 + D-117 (`lifecycle ... hold`) |

---

## Findings

### GAP-1 [critical][immediate] `Sealer.Seal` takes a bare `Scope` and applies neither class admission nor the grant, so `From(ctx)` + `Seal` mints durable identity a read-only policy forbids

- **Where:** `tenancy/seal.go:41-55` (`Seal` calls `this.authority.minted`, authority.go:122, which checks only the MAC and the zero value - never `admission.Admits`, never `permittedByGrant`). Reachable via the exported `tenancy/context.go:77` `From`.
- **Scale:** local (one function), systemic blast radius (every durable queue row).
- **Confidence:** **CONFIRMED** - two probes.

  ```
  # read-only admission; the class gate refuses, Seal does not
  Authority.Scope(ctx, ClassDurable) = ErrInactive, as configured
  BYPASS: From(ctx)+Seal produced a 44-byte durable identity for a read-only
          admission: 00000000000000017db5c0c2
  and it unseals: reference="acme" epoch=1 err=<nil>
  ```
  ```
  # inside a READ-ONLY cohort grant
  (b) the read-only grant still refuses a write on that ctx:
      tenancy: work across tenants needs an explicit grant: forbidden
  (c) BYPASS: a READ-only grant produced a durable identity for "acme": 44 bytes, err=<nil>
      the worker will accept it: reference="acme" epoch=1 err=<nil>
  ```
- **What / Why this severity:** `docs/ai/decisions/D-117` is binding and says, verbatim
  under **What it forbids**: *"Do not let a factory that performs work take a bare
  `Scope`, and do not export one that maps a scope to an address without checking it -
  `From(ctx)` is public, so the value such a function needs is always one call away."*
  `docs/modules/en/tenancy.md:229-230` states the same as fact: *"There is no
  scope-taking variant of either: the unchecked mapping is unexported."* `Sealer.Seal`
  is exactly that variant, exported, in the one seam D-117 calls out by name ("the
  durable producer") and the one seal.go:39 itself calls "what turns a scope into a
  record another process will trust". `Namespace`/`Keyed` are explicitly exempted by
  D-117 because they *derive a name*; `Seal` mints authority.
  Concrete failure: a deployment configures `Admission: Admit(ClassRead, Active).Merge(Admit(ClassWrite, Active))`
  - deliberately no durable class - or runs a monthly cohort report under
  `Accept(purpose, cohort, until, ClassRead)`. A reporting handler writes the natural
  three lines `scope, _ := tenancy.From(ctx); token, _ := sealer.Seal(scope, queue, def);
  enqueue(token, payload)`. Every one of the 10 000 cohort tenants now has a durable
  job row that the worker will restore and execute as that tenant, with write access,
  long after the grant's `until` has passed - the grant deadline is enforced only in
  `permittedByGrant` (grant.go:216-227), which `Seal` never reaches.
  The in-repo consumer `tenancy/tenancyjobs/jobs.go:36` *does* call
  `Scope(ctx, ClassDurable)` first, so the repository is not currently exploiting its
  own hole; the hole is in the exported contract, and D-117 forbids leaving it open.
- **Why this timing:** it is a public contract in a security kernel. Every consumer
  written against the current `Seal(scope, ...)` signature has to change when this is
  closed, and durable rows already written under the wrong class cannot be
  distinguished afterwards.
- **Close criteria:**
  - [ ] `Sealer.Seal` takes `(ctx, class)` (or an `Authority`-checked value) and goes through `Authority.Scope`, so admission and grant are both re-asked
  - [ ] a test seals from a read-only admission and from a read-only grant and asserts `ErrInactive` / `ErrGrantRequired`
  - [ ] D-117 "Where it lives" lists `tenancy/seal.go` (it currently names a non-existent `tenancy/jobs.go`)

### GAP-2 [critical][immediate] A mistyped `Admit(...)` yields the zero `Admission`, which `New` silently replaces with `AdmitAll(Active)` - a fail-open widening from three classes to none configured

- **Where:** `tenancy/lifecycle.go:65-76` (`Admit` returns the zero value for an invalid class, for an invalid state, and for no states at all) + `tenancy/authority.go:41-44` (`if admission.IsZero() { admission = AdmitAll(Active) }`).
- **Scale:** local (two call sites), systemic effect (the whole admission policy).
- **Confidence:** **CONFIRMED**

  ```
  Admit with an out-of-range class     -> IsZero=true states=[0 0 0]
  Admit with an out-of-range class     -> authority admits READ/Active=true WRITE/Active=true DURABLE/Active=true
  Admit with no states at all          -> IsZero=true states=[0 0 0]
  Admit with no states at all          -> authority admits READ/Active=true WRITE/Active=true DURABLE/Active=true
  Admit with an out-of-range state     -> IsZero=true states=[0 0 0]
  Admit(ClassRead, LifecycleUnknown)   -> IsZero=true states=[0 0 0]
  ```
- **What / Why this severity:** `Admission`'s zero value carries two meanings at once -
  "nothing configured, use the default" and "configured, and it admits nothing" - which
  is the ambiguous-state defect in `data-integrity.md` ("Absence must not silently mean
  something"). D-117 says *"A state nobody enumerated ... is refused rather than
  admitted"* and *"Do not widen `Admission` ... The states are enumerated where the
  deployment can see them."* Here a **class** nobody enumerated is admitted.
  Concrete failure: an operator intends a read-only reporting deployment and writes
  `Admission: tenancy.Admit(tenancy.ClassRead)` - forgetting the state list, or copying
  a line where the state came from a variable that was `LifecycleUnknown`. `New`
  returns no error. The deployment now admits `Active` for read, **write and durable**.
  Every write verb and every durable capture that the operator believed impossible
  succeeds. There is no log line, no error, and nothing on the `Authority` a consumer
  can print to check (`Admits` must be probed class by class).
  The same mechanism converts a future 8th `Lifecycle` state into a widening: `1 << 8`
  is `0` in `uint8` (measured: `1<<6=64, 1<<7=128, 1<<8=0`), so `Admit(ClassRead, <state 8>)`
  would return the zero `Admission` and `New` would widen it to `AdmitAll(Active)`.
  The bitset holds exactly 8 states and nothing guards the boundary.
- **Why this timing:** it is the configuration contract of a security kernel, it is
  silent, and it fails open. Any later work that adds a `Lifecycle` state or a `Class`
  inherits the trap.
- **Close criteria:**
  - [ ] `Admit` returns `(Admission, error)` or panics on an invalid class/state instead of returning the zero value
  - [ ] "nothing configured" and "configured to admit nothing" are distinguishable (an explicit `Spec.Admission == nil` / a `configured bool` / a required field)
  - [ ] a compile-time or `New`-time assertion that `Deleted < 8`, with a test
  - [ ] tests: `New` with an out-of-range class, with an empty state list, and with `LifecycleUnknown`, each asserting a refusal rather than the default

### GAP-3 [high][immediate] A `Scope` value has no expiry and no unit-of-work binding: one copied out of a context re-binds into an unrelated request after the tenant was suspended and its generation moved

- **Where:** `tenancy/scope.go:27-32` (a plain comparable value), `tenancy/context.go:23-32` (`With` re-admits any minted scope into any context), `tenancy/context.go:54-56` (`if !this.revalidate { return accepted, nil }`), `Spec.Revalidate` defaults to `false` (authority.go:23).
- **Scale:** local, but it is the default configuration.
- **Confidence:** **CONFIRMED**

  ```
  a scope minted 10000 requests ago, after the tenant was suspended and its generation moved:
    With()          -> err=<nil>
    Scope(ClassRead)-> reference="acme" lifecycle=active epoch=1 err=<nil>
    Seal()          -> 44 bytes err=<nil>
  ```
- **What / Why this severity:** D-117 frames the default window as *"one `Bind`"* and
  the module doc as *"until the next explicit boundary"*. Both descriptions assume the
  scope stays inside the unit of work it was minted for. Nothing enforces that: `Scope`
  is a copyable value, `From` hands it out, and `With` accepts it into any context with
  no notion of when it was minted or which request it belonged to.
  Concrete failure: a consumer memoises "the current tenant scope" in a struct field or
  a package-level variable (a `sync.Map` cache keyed by API key is the obvious shape),
  or a goroutine spawned from request 1 finishes during request 500. The tenant is
  suspended for non-payment and its epoch is bumped by a restore. With the default
  `Revalidate:false`, the stale scope keeps minting reads *and* seals until the process
  restarts - it reports `lifecycle=active epoch=1` while the control plane says
  `suspended/99`. `Revalidate:true` closes it, but it is opt-in, so the zero `Spec` is
  the unsafe one.
- **Why this timing:** it is a property of the `Scope` type, not of a call site.
  Adding a mint timestamp / nonce / context-identity later changes the binding input
  and invalidates every scope in flight, so it cannot be a patch.
- **Close criteria:**
  - [ ] either the binding covers a mint instant (or a per-`Bind` nonce that `With` checks), or the doc stops describing the window as a unit of work and says "the process lifetime, unless `Revalidate`"
  - [ ] a test that stashes a scope, moves the control plane, and asserts the intended behaviour on a fresh context under both `Revalidate` settings

### GAP-4 [high][immediate] The unit-of-work pin covers reference and epoch but not lifecycle, so a bound request moves between admission policies mid-flight

- **Where:** `tenancy/context.go:28` - `current.reference != accepted.reference || current.epoch != accepted.epoch`. The lifecycle is not compared, although it is inside the binding (`bind`, scope.go:70) and is a field of `Scope`.
- **Scale:** local (one condition).
- **Confidence:** **CONFIRMED**

  ```
  bound suspended; write refused with ErrInactive; carried lifecycle=suspended
  With() ACCEPTED a scope with a different lifecycle on the same reference+epoch
  the same unit of work now writes: err=<nil> carried lifecycle=active
  and back: the same unit of work now refuses the write it was allowed a line ago:
      tenancy: tenant lifecycle does not admit this work: forbidden
  ```
- **What / Why this severity:** the code contradicts its own binding documentation in
  three places. `context.go:34-35`: *"the **lifecycle** and generation the scope was
  minted with hold until the next explicit boundary"*. D-117: *"Between boundaries the
  carried lifecycle and generation hold"*. `docs/modules/en/tenancy.md:132-134`: same.
  The test that claims to pin this - `TestTheUnitOfWorkPinCoversTheGenerationAsWellAsTheTenant`
  (contract_test.go:13, commented *"the pin is on the whole binding, not on the
  tenant's name"*) - only exercises the epoch.
  Concrete failure: a request opens a transaction and issues three writes under
  `Active`. Between statements the tenant is suspended; a middleware, a retry wrapper
  or a nested handler calls `Bind`/`With` again. `With` accepts the `Suspended` scope
  (same reference, same epoch), the transaction's remaining writes refuse with
  `ErrInactive`, and the transaction commits half an aggregate - the exact atomicity
  loss that context.go:19-22 says the pin exists to prevent, spelled with the lifecycle
  instead of the tenant. The reverse direction is worse: work bound while `Suspended`
  becomes writable the instant anything re-verifies during an unsuspension.
- **Why this timing:** it is a one-line condition in a public contract, and the fix
  changes an error path (`ErrPinned` where callers currently see success), so consumers
  must be told before they depend on the current behaviour.
- **Close criteria:**
  - [ ] `With` compares the whole binding (or reference+epoch+lifecycle) and returns `ErrPinned` on a lifecycle change
  - [ ] `TestTheUnitOfWorkPinCoversTheGenerationAsWellAsTheTenant` gains a lifecycle subtest with a passing control
  - [ ] under `Revalidate`, `current` (context.go:60-75) returns a scope re-minted at the fresh lifecycle, or the doc says the carried lifecycle is advisory

### GAP-5 [high][immediate] `%+v` and `slog` of `*Authority` and `Spec` print the durable key and the HMAC salt in full

- **Where:** `tenancy/authority.go:18-35`. `Scope` (scope.go:38-48), `Reference`
  (reference.go:25-35), `Resolution` (scope.go:21-25) and `Grant` (grant.go:57-67) each
  carry `String`/`Format`/`LogValue`/`MarshalJSON` guards. `Authority`, `Spec` and
  `Sealer` carry none.
- **Scale:** local (2 types), systemic exposure (any composition-root log line).
- **Confidence:** **CONFIRMED**

  ```
  fmt %+v of *Authority -> &{resolver:{Reference:{value:acme} Lifecycle:2 Epoch:1}
    admission:{states:[4 4 4]} origin:
    salt:[202 55 27 169 213 115 49 174 33 234 211 213 18 32 28 67 70 122 135 93 133 21 80 68 27 190 97 38 89 152 248 43]
    durableKey:[83 85 80 69 82 45 83 69 67 82 69 84 45 68 85 82 65 66 76 69 45 75 69 89 45 51 50 45 66 89 84 69]
    revalidate:false now:0x514ac0}

  slog line -> time=... level=INFO msg=wiring authority="&{... salt:[202 55 ...]
    durableKey:[83 85 80 ...] ...}" spec="{... DurableKey:[83 85 80 ...] ...}"

  control: Scope %+v=[tenancy scope]
           json=/json: error calling MarshalJSON for type tenancy.Scope: ...
           Reference %+v=[tenant reference]
  ```
  (`83 85 80 69 82...` is `SUPER-SECRET-DURABLE-KEY-32-BYTE`; `%+v` of `Spec` also
  prints it, and `Resolution` inside `Fixed` renders `{value:acme}` raw because the
  guard is on `Resolution`, not on the `Fixed` alias.)
- **What / Why this severity:** `restrictions.md` §4 "No secrets in code, logs,
  artifacts, prompts or tests" and §7 "never containing secrets". The durable key is
  the *only* thing standing between queue write access and tenant impersonation
  (seal.go:15-21). One `slog.Info("tenancy wired", "spec", spec)` at a composition root
  - the single most natural line an operator writes when a graph will not build - puts
  the key into every log sink, and rotating it invalidates every unprocessed durable
  row. The salt leaking additionally makes every `Scope` forgeable by anyone who can
  reproduce `bind`. Note the `Fixed` alias also defeats `Resolution`'s own guard.
- **Why this timing:** it is a two-method fix on a public type, and every day it stays
  open is a day a log line can be written that cannot be un-written.
- **Close criteria:**
  - [ ] `Authority`, `Spec`, `Sealer` and `Fixed` implement `String`/`Format`/`LogValue`/`MarshalJSON` guards like `Scope` does
  - [ ] a test in the shape of `TestNothingCarryingATenantRendersIt` (scope_test.go:197) asserting the key bytes appear in neither `%v`, `%+v`, `%#v`, `slog` nor `json.Marshal`, with a control that shows the assertion can fail

### GAP-6 [high][immediate] Nine of twenty mutations survived the core suite, including the resolver-redaction rule and the whole construction-time admission check

- **Where:** `tenancy/scope_test.go:144` (`TestAResolverFailureNeverTravelsBackAsText`),
  `tenancy/seal_test.go:48` (`TestADeploymentWithNoDurableKeySealsNothing`), and the
  absence of any test for `authority.go:45-47`, `authority.go:56-58`,
  `authority.go:98-100`, `lifecycle.go:67-69`, `grant.go:86-88`.
- **Scale:** systemic - 9 of 20 mutations, 45%.
- **Confidence:** **CONFIRMED** - each mutation applied to an isolated snapshot whose
  baseline was re-verified green immediately before, then reverted.

  ```
  M[R1  Unseal: hmac.Equal -> plain string compare]              --> *** SURVIVED ***
  M[R2  boundTo: hmac.Equal -> plain string compare]             --> *** SURVIVED ***
  M[R3  Unseal: length guard <= becomes <]                       --> *** SURVIVED ***
  M[R4  Classify: return the resolver error verbatim]            --> *** SURVIVED ***
  M[R5  New: drop the minimum durable-key length]                --> *** SURVIVED ***
  M[R6  Admit: drop the class validity guard]                    --> *** SURVIVED ***
  M[R7  inconsistent(): never fires]                             --> *** SURVIVED ***
  M[R10 mac(): the binding-field count is not sealed]            --> *** SURVIVED ***
  M[R16 Accept: no MaxCohortSize ceiling]                        --> *** SURVIVED ***
  M[R18 Lookup: do not check the resolver echoed the reference]  --> *** SURVIVED ***
  ```
  (killed, for contrast: R8 default admission, R9 `Digest` epoch, R11 grant deadline,
  R12 reference charset, R13 epoch pin, R14 `permittedByGrant`, R15 grant cohort MAC,
  R17 per-member deadline, R19 `Sealer()` key guard, R20 length prefix.)
- **What / Why this severity:** three of the survivors are stated invariants:
  - **R4.** D-117: *"a resolver's own error text ... is collapsed to `ErrUnavailable`
    unless the resolver deliberately returned one of the sentinels."* The test's
    sentinel sub-case (scope_test.go:165-175) asserts only
    `errors.Is(refusal, ErrNoScope)`, which holds whether `Classify` returns the bare
    sentinel or the wrapped original. The behaviour is correct today - I verified
    `Classify(fmt.Errorf("postgres://tenant_acme:hunter2@10.0.0.7:5432/acme...: %w", ErrUnmapped))`
    returns exactly `"tenancy: the tenant has no mapping for this capability: forbidden"`
    - but a one-word regression (`return err` instead of `return refusal`, errors.go:53)
    ships a DSN with a password to every caller and no test notices.
  - **R7.** `New`'s write-without-read refusal (authority.go:45-47), which D-117 and
    both module docs present as a guarantee, has **no test at all**: `rg 'admits writes
    but not reads|inconsistent' tenancy/*_test.go` returns nothing.
  - **R18.** `Lookup`'s check that the resolver echoed the reference it was asked about
    (authority.go:98-100) has no test. It is the guard that stops a buggy or hostile
    control plane from substituting one tenant for another; without it, a cohort billing
    run named for `acme` would silently execute as `globex`. I confirmed the guard works
    (`Lookup(acme)` against an always-answers-`globex` resolver returns `ErrUntrusted`)
    - it is simply unpinned.
  `TestADeploymentWithNoDurableKeySealsNothing` (seal_test.go:66-73) is written so that
  it passes whether `New` refuses a 31-byte key or accepts it and `Sealer()` refuses
  later: `if err != nil { if len(key)==0 { t.Fatalf } ; return }`. That is
  `restrictions.md` §5's "a test that cannot fail" for the fail-fast property.
  R1/R2 (constant-time comparison) are not reachable by a Go unit test and are listed
  for completeness, not as defects.
- **Why this timing:** `CLAUDE.md` makes tests the specification and calls a test that
  would pass with the feature deleted a liability. Five of these guards are the ones a
  future refactor is most likely to "simplify".
- **Close criteria:**
  - [ ] the redaction test asserts on the *text* in the sentinel branch, with a resolver error carrying a DSN and a password, plus a control that fails without `Classify`
  - [ ] a test constructs `Admit(ClassWrite, Suspended)` and asserts `New` refuses, naming the state
  - [ ] a test drives a resolver that answers `Lookup` with a different reference and asserts `ErrUntrusted`, through `Lookup` and through `Each`
  - [ ] `TestADeploymentWithNoDurableKeySealsNothing` asserts *where* each key length is refused, not merely that it is
  - [ ] `MaxCohortSize` and the seal field-count have a test each
  - [ ] the mutation set is re-run and the survivor count is 2 (R1, R2) with a written note that they are untestable

### GAP-7 [high][immediate] `Classify` strips the only `crud` kind a malformed-input refusal had, turning a client error into HTTP 500

- **Where:** `tenancy/errors.go:34-37` - `refusals` holds ten sentinels and `ErrMalformed` is not one of them; `errors.go:56` falls through to `ErrUnavailable`, which is a bare `errors.New` (errors.go:31). `tenancy/outcome.go:56-58` handles `ErrMalformed` explicitly, so the two functions disagree.
- **Scale:** local (one omission), systemic effect (every resolver refusal path).
- **Confidence:** **CONFIRMED**

  ```
  Classify(wrapped ErrMalformed) = "tenancy: the tenant capability is unavailable"
    (is it ErrMalformed? false) (is it ErrUnavailable? true)
    ErrMalformed wraps crud.ErrBadRequest: true
    the classified result wraps crud.ErrBadRequest: false
    the classified result wraps any crud sentinel:
      badrequest=false forbidden=false conflict=false notfound=false

  ErrMalformed                     -> errs.Kind=bad_request internal=false
  Classify(resolver's malformed)   -> errs.Kind=internal     internal=true
  ErrCapacity                      -> errs.Kind=internal     internal=true
  ErrUnavailable                   -> errs.Kind=internal     internal=true
  ```
  (`port/kind.go:70-88`: an error matching no `crud` sentinel falls to `errs.KindInternal`.)
- **What / Why this severity:** D-117 promises *"a sentinel the resolver chose
  deliberately"* is kept. `ErrMalformed` is one of the package's eleven exported
  sentinels and is what `ParseReference`, `ParsePurpose` and `NewEpoch` return, so a
  resolver that validates an `X-Tenant` header and returns
  `fmt.Errorf("header %q is not a reference: %w", raw, tenancy.ErrMalformed)` is
  following the documented pattern. Concrete failure: the client sends a malformed
  tenant header, the resolver refuses correctly, and the caller receives **500 Internal
  Server Error** instead of 400. The request is retried by the client and by every
  gateway in front of it, the on-call is paged for a server fault that is a client
  typo, and `OutcomeFor` simultaneously reports `untrusted` for the same error - so the
  metric and the HTTP status disagree about what happened. D-117 says "the ten
  sentinels" while `errors.go` declares eleven; the mismatch is the bug's origin.
  Secondarily, `ErrCapacity` and `ErrUnavailable` are the only two sentinels that wrap
  no `crud` kind at all, so a control-plane outage is reported as `internal` with no
  retryable signal even though `errs.KindRetryable` exists (`port/kind.go:55-56`).
- **Why this timing:** it changes an outward-facing status code, and every consumer
  writing a resolver today is being taught the wrong pattern.
- **Close criteria:**
  - [ ] `ErrMalformed` is in `refusals`, or `Classify` handles it the way `OutcomeFor` does, and D-117's "ten sentinels" is corrected
  - [ ] `ErrUnavailable`/`ErrCapacity` wrap a `crud`/`errs` kind that maps to 503/429 rather than 500, or the choice of 500 is written down
  - [ ] `TestEveryRefusalIsForbiddenToATransportThatKnowsNothingAboutTenants` (scope_test.go:177) is extended to assert the `errs.Kind` of every sentinel *and* of `Classify` applied to it

### GAP-8 [medium][immediate] A missing `Spec.DurableKey` is a configuration error reported at first use as `ErrUntrusted` ("scope was not produced by this authority")

- **Where:** `tenancy/authority.go:56-58` accepts `len(DurableKey) == 0`; `tenancy/seal.go:28-30` refuses with `ErrUntrusted`.
- **Scale:** local.
- **Confidence:** **CONFIRMED** - `TestADeploymentWithNoDurableKeySealsNothing` (seal_test.go:73) asserts exactly `ErrUntrusted` for the no-key case, and mutation R5 shows the 1..31-byte band is untested.
- **What / Why this severity:** `restrictions.md` §4: configuration is "validated at
  startup, failing fast and specifically". `New` succeeding with no durable key is
  deliberate and right - a deployment that does no durable work should not need one -
  but the eventual refusal is the *wrong sentinel*. Concrete failure: a deployment adds
  a queue, forgets `DurableKey`, and `tenancyjobs.ContextProvider` fails at wiring with
  "tenancy: scope was not produced by this authority: forbidden". That message sends an
  operator to the resolver, the salt and the scope-minting path - none of which are
  involved. `ErrIncompatible` ("tenant binding is not compatible with this capability")
  already exists and says the right thing, or a distinct `ErrNoDurableKey` would.
- **Why this timing:** it is a public error contract that consumers will start matching
  with `errors.Is`.
- **Close criteria:**
  - [ ] `Sealer()` on a keyless authority returns a sentinel that names the missing configuration
  - [ ] a test asserts `New` refuses a 1..31-byte key at construction (mutation R5 must be killed)

### GAP-9 [medium][immediate] `Authority.Each` has no concurrency, idempotency or resumability story, and cannot report which cohort members were never attempted

- **Where:** `tenancy/grant.go:117-146` (`Each`), `grant.go:47-53` (`Grant` exposes `Size()` but not the cohort), `tenancy/outcome.go:22-28` (twelve outcomes, none meaning "not attempted").
- **Scale:** local (one function), systemic effect under N replicas.
- **Confidence:** **CONFIRMED**

  ```
  four concurrent Each runs over one grant entered: map[acme:4 globex:4]
    (no lock, no idempotency key, no leader election)     # -race clean

  cohort of 4, Grant.Size()=4, run stopped with err=... after 2 members reported
    reported: "acme" -> ok
    reported: "globex" -> ok
  the grant exposes Size() but no accessor for the cohort, and no Outcome means
  "never attempted": [ok absent untrusted inactive stale incompatible unmapped
                      grant pinned capacity unavailable error]
  ```
- **What / Why this severity:** `data-integrity.md`: "Background jobs and schedulers run
  on every replica. Either the job is idempotent and concurrency-safe, or it takes a
  DB-backed lock / leader election." `Each` is the library's cross-tenant batch driver
  and the doc comment (grant.go:113-116) argues *for* resumability: *"a run that cannot
  be resumed is a run somebody will resume by hand across every tenant it already
  finished."* Concrete failure: a monthly billing `Each` over 10 000 tenants is
  scheduled on three replicas. Every tenant is billed three times; nothing in the
  library notices, because `Grant` is immutable and race-free (correctly - `-race` is
  clean) and the duplication is semantic. Second failure: the run stops at member 4 000
  on the deadline. The returned slice has 4 000 entries; the remaining 6 000 are simply
  absent and indistinguishable from "not in the cohort". `Grant.Size()` gives a count
  but no references, so the operator cannot compute the remainder from the grant - only
  from their own copy of the input slice, which `Accept` may have deduplicated to a
  different length. `restrictions.md` §2: "Partial success is modelled explicitly (what
  succeeded, what failed, why)". Here "never attempted" is modelled by omission.
- **Why this timing:** `Outcome` is a closed, exported vocabulary and `Grant` is an
  exported type; adding a member or an accessor later is a surface change.
- **Close criteria:**
  - [ ] `Each` declares in writing what happens when two replicas run it, or takes a caller-supplied coordination seam
  - [ ] a caller can enumerate the unreached members (a `Grant.Cohort()` accessor, or a `Member` per cohort entry with an `OutcomeSkipped`)
  - [ ] a test asserts the length and contents of the result for a run that stops mid-cohort

### GAP-10 [medium][immediate] `Sealer.Unseal` copies an unbounded token before checking it against a 168-byte ceiling it already knows

- **Where:** `tenancy/seal.go:65-72` - the only length guard is `len(token) <= sealHeaderBytes` (40); `ParseReference(string(token[40:]))` allocates the whole tail before `Reference.valid()` rejects anything over `MaxReferenceBytes`.
- **Scale:** local.
- **Confidence:** **CONFIRMED**

  ```
  Unseal of an 8388648-byte token: err=tenancy: scope was not produced by this
    authority: forbidden after 1.059672ms (a valid token is at most 168 bytes)
  ```
- **What / Why this severity:** the maximum valid token is
  `sealHeaderBytes + MaxReferenceBytes` = 168 bytes and both constants are in scope at
  the call site. `Unseal` reads from the queue table, which seal.go:17-18 itself says
  is *"writable by whatever else reaches that database"*. Concrete failure: a row with
  a 64 MB token costs a 64 MB allocation and a copy per delivery attempt; with
  at-least-once delivery and a handful of workers, that is an unauthenticated,
  DB-triggered memory amplification. The refusal is correct - only the cost is wrong.
  `jobs.NewProtectedIdentityToken` bounds the token on the *producing* side
  (`jobs/durable_context.go:108`), which does not constrain what a worker reads back.
- **Why this timing:** a two-line guard, but it is in the one function that parses
  untrusted bytes, and it should land before anything else in `seal.go` moves.
- **Close criteria:**
  - [ ] `Unseal` refuses `len(token) > sealHeaderBytes+MaxReferenceBytes` before allocating
  - [ ] a test covers the over-long token alongside the existing `TestARecordTooShortToCarryAClaimIsRefused` (seal_test.go:158)

### GAP-11 [medium][immediate] Both module docs state a guarantee about durable admission that the code deliberately does not provide

- **Where:** `docs/modules/en/tenancy.md:121-123` and `docs/modules/ru/tenancy.md:123-127` say a state admitted for writing **or for durable work** but not for reading is refused. `tenancy/lifecycle.go:109-116` (`inconsistent`) checks only `ClassWrite`, and `lifecycle.go:106-108` records that durable is *deliberately* exempt - as does D-117.
- **Scale:** local (two doc paragraphs), but they are the consumer-facing reference.
- **Confidence:** **CONFIRMED**

  ```
  Admit(ClassWrite, Migrating) alone  -> New err = tenancy: migrating admits writes
      but not reads, and every write resolves a read scope first: admit it for reads
      too, or for neither
  Admit(ClassDurable, Migrating) alone -> New err = <nil>
     and it mints a durable scope for a migrating tenant that admits no read:
     reference="acme" err=<nil>
  ```
- **What / Why this severity:** `CLAUDE.md` treats a stale doc as a failing test, and
  the module docs are the consumer's reference. Concrete failure: an operator reads the
  module page, writes `Admission: Admit(ClassDurable, Migrating)` believing an
  inconsistent policy will be refused at startup, gets no error, and ships a deployment
  where migrating tenants can enqueue and execute durable work while every read and
  write refuses - which may be what they wanted, but they were told the configuration
  was impossible. The exemption is the *right* behaviour (D-117 argues it well); the
  documentation is what is wrong, in both languages.
- **Why this timing:** it is a documented public guarantee; consumers are configuring
  against it now.
- **Close criteria:**
  - [ ] both module docs say "admitted for writing but not for reading", matching `inconsistent` and D-117
  - [ ] the durable exemption is stated in the module docs, not only in the source comment and the decision

### GAP-12 [medium][deferred] `Fixed` is an exported "answer every context with this tenant" resolver sitting next to a constructor that refuses exactly that

- **Where:** `tenancy/authority.go:143-152`; the neighbouring refusal is `authority.go:39`: *"an authority needs a resolver; there is no default tenant to fall back to"*. It is in the exported surface (`docs/api/surface.md`).
- **Scale:** local.
- **Confidence:** **CONFIRMED**

  ```
  New(Spec{}) = tenancy: an authority needs a resolver; there is no default tenant to fall back to
  Fixed answers every context with the same tenant: reference="acme" err=<nil>
  ```
- **What / Why this severity:** D-117 justifies `Fixed` as how a *test* gets a real
  scope by the production path, and that reasoning is sound. But nothing in the name,
  the type or the API surface marks it as a test double, and `restrictions.md` §5 keeps
  sample scaffolding out of `src/`. Concrete failure: a team bringing tenancy up
  incrementally wires `tenancy.Fixed{Reference: theirOnlyTenant, ...}` "until the
  control plane is ready" and ships it. Every request in the deployment - including
  requests carrying another tenant's credentials - resolves to that one tenant, and the
  library's central promise ("there is no default tenant") is defeated by a type the
  library exports. `Fixed` also defeats `Resolution`'s own rendering guard: `%+v` of a
  `Fixed` prints `{Reference:{value:acme} ...}` because the methods are on `Resolution`.
- **Why this timing:** it is a naming and documentation change on an exported symbol
  with no behavioural consequence, so it can follow the immediate items.
- **Close criteria:**
  - [ ] `Fixed` is named for what it is (`FixedTenant`, or moved behind a `tenancytest` package), or the module doc says in one sentence that it is a test double and must not be wired in production
  - [ ] `Fixed` carries the same rendering guards as `Resolution`

### GAP-13 [low][deferred] `Bind` and `With` return a nil `context.Context` on refusal

- **Where:** `tenancy/context.go:10` and `context.go:26,30` - `return nil, err`.
- **Scale:** local (2 functions).
- **Confidence:** **CONFIRMED** - `Bind on a refusing resolver returned ctx==nil: true (err=tenancy: the tenant has no mapping for this capability: forbidden)`.
- **What / Why this severity:** the idiomatic Go shape is `ctx, err := f(ctx, ...)`,
  which overwrites the caller's variable. A caller that logs, traces or cancels through
  `ctx` in a `defer` after an early `return err` panics with a nil-pointer dereference
  on a *refusal* path - the path least covered by tests. Returning the input context
  unchanged costs nothing and makes the refusal survivable.
- **Close criteria:**
  - [ ] `Bind`/`With` return the input context alongside the error, or the doc states the nil contract

### GAP-14 [low][deferred] `Sealer.Unseal` returns an epoch that nothing in the core requires the caller to compare

- **Where:** `tenancy/seal.go:61-81` returns `(Reference, Epoch, error)`; `Authority.Lookup` (authority.go:90-102) does not take an epoch and does not compare one. The only in-repo consumer does the comparison by hand at `tenancy/tenancyjobs/jobs.go:92-94`.
- **Scale:** local.
- **Confidence:** **CONFIRMED** (by reading; the comparison is present in `tenancyjobs`, absent from the core).
- **What / Why this severity:** the sealed token has no nonce and no expiry - two seals
  of the same scope are byte-identical (`two seals of the same scope identical: true`;
  `len=44 = 8 epoch + 32 mac + 4 reference`) - so the *only* thing that stops a token
  written before a tenant restore from being honoured afterwards is a caller-written
  `if scope.Epoch() != generation`. `Unseal`'s doc comment says "the control plane is
  what turns it back into work" without saying the generation must be compared, and the
  core offers no API that does it. A second durable seam that forgets the line replays
  pre-restore work into the new generation and the seal verifies. Related: the replay
  boundary of a token is exactly the granularity of the binding fields the caller
  chooses; `tenancyjobs.record` (jobs.go:104-107) chooses (queue digest, definition
  name), so within one definition every row's token is interchangeable.
- **Close criteria:**
  - [ ] the core offers `Authority.LookupAt(ctx, reference, epoch, class)` (or `Unseal` returns something a `Lookup` must consume), so the generation check cannot be forgotten
  - [ ] `Unseal`'s contract states, in `docs/modules/*/tenancy.md`, that the returned generation must be compared and what the binding fields must include to bound replay

---

## What came out clean (with the commands, so the silence is evidence)

- **Framing / unforgeability of `bind`.** `origin` **and** `reference` are both
  length-prefixed by `writeField` (scope.go:97-104); lifecycle+epoch follow as a fixed
  9-byte scratch, which is unambiguous after two framed fields. Exhaustive sweep over
  42 `(origin, reference)` pairs including `("a","bc")` vs `("ab","c")`: no collision.
  Mutation R20 (drop the length prefix) is killed by
  `TestFieldsCannotBeSlidPastTheLengthPrefix` (binding_test.go:53).
- **`Scope.Digest()`.** Also length-framed on the reference, fixed-width on the epoch.
  35-pair sweep over adversarial names and epochs (`"a"/0x61`, `"ab"/0x6161`, ...): no
  collision. Mutation R9 (drop the epoch) killed by three tests in two packages.
- **`boundTo` / cross-authority.** `hmac.Equal`, zero-value and validity guards first.
  Authority Y refuses authority X's scope with `ErrUntrusted`.
- **Seal binding-field framing.** An absent field and one empty field produce different
  MACs (`463434165e4bdb6f` vs `00ea480db146f091`); `("ab","c")` and `("a","bc")` differ;
  a 0-field token does not verify against a 1-empty-field binding.
- **`Unseal` canonicalisation.** The MAC is recomputed from the **parsed** epoch and the
  **parsed** `Reference`, not over the token bytes, and `ParseReference` runs first - so
  there is no non-canonical encoding that verifies. `hmac.Equal` is used for the compare.
- **`Admission` shift bound, today.** `AdmitAll(all six states)` yields
  `[3]uint8{0x7e, 0x7e, 0x7e}` - bits 1..6, every state present, no overflow. (The
  *future* hazard is folded into GAP-2.)
- **`Classify` redaction.** `Classify(fmt.Errorf("postgres://tenant_acme:hunter2@10.0.0.7:5432/acme?sslmode=disable: %w", ErrUnmapped))`
  returns the bare sentinel; doubly-wrapped sentinels and unmapped driver errors both
  collapse correctly. (It works; GAP-6 is that nothing pins it.)
- **`OutcomeFor` totality.** All eleven sentinels, bare and wrapped, map into the closed
  twelve-constant set; `nil` -> `ok`; unknown -> `error`. Verified by enumeration.
- **`Member.Err` redaction.** A resolver failure carrying
  `postgres://globex_rw:hunter2@10.0.0.9/globex` reaches the caller as
  `"tenancy: the tenant capability is unavailable"`, because `Lookup` applies `Classify`
  before `member` records it (grant.go:149-152). The caller's own `work` error travels
  verbatim, which is correct - the caller owns both ends.
- **`Each` does not leak into the caller's context**, and refuses to run from an
  already-bound one (`ErrPinned`); the per-member deadline re-check is real and killed
  by mutation R17.
- **`Grant` concurrency.** Immutable after `Accept`; four concurrent `Each` runs under
  `-race` produced no race. (The *semantic* duplication is GAP-9.)
- **Input mutation / data safety.** `Spec.DurableKey` is copied (`append([]byte(nil), ...)`,
  authority.go:64); `ParseReference`/`ParsePurpose` use `strings.Clone`; `Accept` builds
  a fresh `members` slice rather than sorting the caller's. No argument is mutated.
- **Cohort dedupe vs MAC.** `Accept` MACs the deduplicated `members` and `holds`
  recomputes over `grant.cohort`, which is the same slice - no mismatch. Mutation R15
  (drop the cohort from the MAC) killed by `TestEveryPartOfAGrantIsInsideItsBinding`.
- **No hidden non-determinism, no env reads, no I/O, no logging, no vendor SDK, no
  `TODO`/`nolint`, no skipped tests** in the nine scope files (commands in the Map).
- **Byte-exact, never-folded reference comparison** (`acme` / `ACME` / Cyrillic `аcme` /
  `acme​` are four tenants) is a documented decision at reference.go:37-39 and is
  reported here as behaviour, not as a defect.

---

## Remediation order

1. **GAP-5 (secrets in `%+v`/`slog`)** - S, blast radius: 3 types, no callers change.
   First because it is the only finding that gets *worse* with time: a leaked durable
   key cannot be un-logged and rotating it strands every unprocessed queue row.
2. **GAP-2 (`Admit` silent-empty -> `AdmitAll`)** - S/M, blast radius: `Admit`'s
   signature and every `Spec` literal. Before GAP-1, because GAP-1's fix routes `Seal`
   through the admission that GAP-2 currently allows to be silently wrong.
3. **GAP-1 (`Seal` takes a bare `Scope`)** - M, blast radius: the `Sealer` public API,
   `tenancyjobs`, D-117 and both module docs. Structural: every consumer written
   against `Seal(scope, ...)` changes.
4. **GAP-4 (lifecycle absent from the pin)** - S, blast radius: one condition, but it
   turns current successes into `ErrPinned`, so it must precede any consumer work that
   re-binds. Do it together with GAP-3's decision, because both are about what a
   carried scope means.
5. **GAP-3 (a `Scope` value never expires)** - M/L, blast radius: the binding input, and
   therefore every scope in flight during a deploy. Needs a written decision first
   (bound the value, or narrow the documented claim); the fix cannot be incremental.
6. **GAP-7 (`ErrMalformed` -> 500)** - S, blast radius: one table entry plus the
   `ErrUnavailable`/`ErrCapacity` kind choice, outward-facing status codes.
7. **GAP-6 (nine surviving mutations)** - M, blast radius: tests only. Must come after
   1-6 so the new tests pin the corrected behaviour rather than the current one; the
   R4/R7/R18 tests can be written immediately and independently.
8. **GAP-8 (`ErrUntrusted` for a missing durable key)** - S, blast radius: one sentinel,
   fold into GAP-1's pass over `seal.go`.
9. **GAP-10 (`Unseal` length ceiling)** - S, blast radius: one guard, fold into the same
   `seal.go` pass.
10. **GAP-11 (module docs vs `inconsistent`)** - S, docs only, both languages; fold into
    GAP-2's pass since both concern `Admission`.
11. **GAP-9 (`Each` concurrency and resumability)** - M/L, blast radius: `Grant` and
    `Outcome`, both exported. Independent of the rest; needs a decision doc.
12. **GAP-12, GAP-13, GAP-14** - S each, deferred, no ordering constraint.

---

## What I did not check

- **The seam packages** `tenancyrow`, `tenancydb`, `tenancystorage`, `tenancycache` and
  `tenancyjobs` beyond the four lines that answer a core-contract question
  (`tenancyjobs/jobs.go:36,44,84,92-94,104-107`). In particular I did not audit
  `tenancydb.Directory`'s mutex-guarded cache, its `ready` channel or its eviction -
  the largest concurrency surface in the subsystem, and dimension 5 material.
- **`crud/decorators/security`** (1102 lines), which is where the narrowing predicate
  actually reaches SQL. GAP-1 and GAP-4 are about what the core hands that gate; what
  the gate does with it is unaudited here.
- **The integration suite** (`test/integration/tenancy_test.go`, `make integration`) -
  it needs Docker and was not run. Only `go test ./tenancy/...` was executed.
  `make check` (the structural checks) was not run either.
- **`-race` on the full core suite.** I ran `-race` only on my own concurrency probe.
  The mutation campaign ran without it, for speed.
- **`scripts/tenancy_test.go`** (the structural import checks) and `docs/api/surface.md`
  drift beyond confirming `From` and `Seal` appear there.
- **A real timing analysis** of `hmac.Equal` vs the parse-before-compare ordering in
  `Unseal`. I established the ordering by reading; I did not measure whether the parse
  leaks anything useful. Mutations R1/R2 are listed as untestable rather than as gaps.
- **Whether `jobs.Partition`, `jobs.Namespace` or `storage`/`cache` key derivation can
  collapse two distinct `Reference` values into one address.** I confirmed
  `jobs.ParsePartition` (`jobs/scope.go:180-189`) refuses rather than truncates, but did
  not sweep the whole path, and `Scope.Digest()` is unkeyed, so a namespace is
  computable by anyone who knows a reference.
- **`Origin`'s value in production.** `binding_test.go:14` isolates it correctly with a
  shared salt, but because `New` draws a fresh salt per `Authority` there is no reachable
  configuration in which two authorities share a salt, so `Origin`'s contribution is
  untestable from outside the package and unmeasurable in a deployment. I did not decide
  whether that makes it dead weight or a correct defence in depth.
- **The `_examples/tenancy-sharedrow` wiring** beyond `main.go:136,143`, and the
  `docs/ai/flows/FL-033` and `UC-004` texts.
- **Whether the working-tree edits another agent was making during this audit affect the
  seam packages' behaviour.** I pinned only the nine core files by checksum.
