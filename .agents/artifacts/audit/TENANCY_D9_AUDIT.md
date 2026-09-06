# tenancy + _examples/tenancy-sharedrow — DRY / one-format-place / readability — AUDIT (2026-09-06)

Scope: `tenancy/` (all six packages, source and tests) and `_examples/tenancy-sharedrow/`.
Dimension: architecture.md §DRY + §Single format place, and readability.md in full.

House rules applied in preference to generic Go advice, per the brief:
framework `CLAUDE.md` §House style, repo-root `/home/user/ws/gd/lease/AGENTS.md`,
`.golangci.yml` (which **disables `revive:receiver-naming`** — so the `this`
receiver is a project decision and is not reported as a defect below).

## Tree stability — read this before trusting any line number

While this audit ran, another process was **applying and reverting mutations to
the working tree**. Observed directly:

```
00:24:15  tenancy/scope.go  md5 85d2f6d9…   line 70: scratch[0] = 0
00:25:16  tenancy/scope.go  md5 58de2b46…   line 70: scratch[0] = byte(resolution.Lifecycle)
00:26:04  tenancy/grant.go  md5 39c50ddb…   (mutated)
00:26:13  tenancy/grant.go  md5 8ffd8551…   (restored)
```

Three consecutive `go test ./tenancy/...` runs failed in three different tests
(`TestTheOriginIsPartOfTheBindingAndNotJustTheSalt`,
`TestAGrantThatExpiresMidRunStopsAndReportsWhatFinished`, `cache_test.go:69`).
Every finding below was therefore taken against, and re-verified against, this
exact set of hashes:

```
0531d25a523188817e4af6e6c5aa8382  tenancy/authority.go
9d70c1353e35d66a08c6c22c77b9d676  tenancy/context.go
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
7c12b2ebf0f1c3fa4dff664eabbd9f52  _examples/tenancy-sharedrow/main.go
```

On that pristine tree the suite is green, bracketed by a tree hash to prove no
mutation was in flight during the run:

```
$ B=$(md5sum tenancy/*.go tenancy/*/*.go | md5sum); go clean -testcache; go test ./tenancy/...
ok  github.com/frostgrove/vv/tenancy               0.004s
ok  github.com/frostgrove/vv/tenancy/tenancycache  0.002s
ok  github.com/frostgrove/vv/tenancy/tenancydb     0.144s
ok  github.com/frostgrove/vv/tenancy/tenancyjobs   0.002s
ok  github.com/frostgrove/vv/tenancy/tenancyrow    0.002s
ok  github.com/frostgrove/vv/tenancy/tenancystorage 0.003s
```

## Map (as it bears on this dimension)

Six packages. `tenancy/` is the core value-object layer: `Reference`, `Epoch`,
`Purpose`, `Scope`, `Resolution`, `Lifecycle`, `Class`, `Admission`, `Grant`,
`Sealer`, `Outcome`, eleven error sentinels. Five adapter packages
(`tenancyrow`, `tenancydb`, `tenancyjobs`, `tenancystorage`, `tenancycache`)
each hold one seam and each begin the same way: resolve a scope for a class from
the context, then turn it into that seam's artefact.

Four hand-rolled MAC/digest constructions live in the core: `bind`
(`scope.go:65`), `Scope.Digest` (`scope.go:84`), `grantBinding` (`grant.go:185`),
`Sealer.mac` (`seal.go:83`). Two shared framing primitives exist —
`writeField`/`writeBytes` (`scope.go:97-104`) — and all four call them, but each
of the four then makes its own decision about what *else* goes in.

Five types carry a redaction protocol (`String`/`Format`/`LogValue`/
`MarshalJSON`); no interface declares it and no test exercises it. The repository
already owns a correct, tested implementation of exactly this protocol —
`utils/vvdb.Secret` (`utils/vvdb/secret.go:16-42`, six methods including
`GoString`, 303 lines of tests in `utils/vvdb/secret_test.go`).

Tests: 3376 lines across the six packages. Roughly 125 of them are byte-identical
copies of each other, because Go test packages cannot share helpers and no
`tenancytest` support package was created (the repository has the precedent —
`crud/crudtest`).

`tenancy/doc.go` is one line: `package tenancy`. No package in `tenancy/` has a
package doc.

## Scorecard

| Law | Verdict | Evidence |
|---|---|---|
| DRY — decisions in one place | **fail** | 4 MAC framings with 3 different rules, one demonstrably colliding (GAP-1); the sentinel set written in 4 places (GAP-2); nil-authority answered 4 ways (GAP-4); the Class set written in 5 places (GAP-6) |
| Single format place | **fail** | redaction is a 4-method protocol filled 13 of 20 slots across 5 types; `tenancycache.Key` leaks the raw reference under `%#v` (GAP-3); 5 hand-written placeholder literals, 2 different prefixes, 0 tests |
| DRY — near-identical bodies | **fail** | 3 byte-identical copies of a 28-line test-helper block, 2 of a 36-line block, 2 of an 8-line block; 6 spellings of "build an authority for one tenant" (GAP-5) |
| readability §1 nesting / guard clauses | **pass** | max nesting depth in the whole non-test tree = 2 (threshold 3, target 2); 0 functions over 40 lines out of 129 |
| readability §3 comments | **fail** | 30 of 30 non-test comment blocks document a declaration shorter than the 40-line gate; 20 comment blocks narrate tests, which the rule forbids outright; 0 package docs (GAP-7, GAP-8) |
| readability §4 naming | **at risk** | `refusalFor` returns success text; `orDefault`; `resolveRelation` returns a value 1 of 2 callers discards; one concept named `knownTenants`/`directory`/`controlPlane`/`switchable` (GAP-9) |
| readability §5 shape | **at risk** | `refusalFor(authority, ctx)` puts `context.Context` second (GAP-10); `column.Apply` folds three unrelated refusal reasons into one negated condition |
| tooling | **at risk** | `gofmt -l` and `go vet` clean; `.golangci.yml` exists but no `Makefile` or `scripts/checks.sh` target runs any linter (GAP-11) |

## Metrics

| Metric | Threshold | Actual | Worst offenders |
|---|---|---|---|
| non-test source lines in `tenancy/` | — | 1734 | `tenancydb/database.go` 284, `tenancyrow/row.go` 269 |
| files > 400 lines (non-test) | 0 | **0** | — |
| functions >= 40 lines (non-test) | — | **0** of 129 | longest: `Borrow` 33, `Accept` 33, `NewDirectory` 32, `New` 32 |
| max nesting depth (non-test) | <= 3 | **2** | `OutcomeFor`, `Accept`, `Borrow`, `sweep`, `Each`, `inconsistent`, `resolveRelation`, `Capture`, `Evict`, `permits`, `grantBinding` |
| comment blocks in non-test source | — | 30 (152 lines, **8.8%** of source) | `seal.go:15` = 7 comment lines on a 1-line type |
| …of those, on a 40+ line function | 30 | **0** | max documented declaration = 33 lines |
| comment blocks narrating tests | 0 | **20** (66 lines) | `binding_test.go:8` (6 lines), `tenancyrow/row_test.go:371` (5, inline) |
| package doc comments | 6 | **0** | `tenancy/doc.go` is 1 line and says nothing |
| MAC/digest framings | 1 | **4** | `bind`, `Digest`, `grantBinding`, `mac` |
| …that seal a field count | 4 | **1** | only `Sealer.mac` (`seal.go:85-87`) |
| places encoding "the set of `Class`" | 1 | **5** | `lifecycle.go:41-43`, `:46`, `:50-54`, `:61`, `:63` (`states [3]uint8`) |
| places encoding "the set of `Lifecycle`" | 1 | **4** | `lifecycle.go:6-12`, `:16`, `:21-31`, `:110` |
| places encoding "the refusal sentinels" | 1 | **4** | `errors.go:10-31`, `errors.go:34-37`, `outcome.go:30-42`, `vocabulary_test.go:63-67` |
| nil-`*Authority` policies | 1 | **4** | `ErrNoScope` / constructed `error` / explicit `panic` / nil deref |
| byte-identical duplicated test-helper lines | 0 | **~125** | 28-line block ×3, 36-line block ×2 |
| redaction protocol slots filled | 20 (5 types × 4) | **13** | `tenancycache.Key` fills 1 |
| redaction tests | >= 1 | **0** | `grep` for `[tenant reference]` in `*_test.go` returns nothing |
| `gofmt -l ./tenancy` | empty | empty | — |
| `go vet ./tenancy/...` | clean | clean | — |
| `staticcheck ./tenancy/...` non-ST1006 | 0 | **0** | (ST1006 hits are the disabled `receiver-naming` rule) |
| `this` receiver consistency | — | **88/88** named receivers use `this`; 3 deliberately anonymous (`MarshalJSON`) | consistent — not a finding |

## Findings

### GAP-1 [high][immediate] Four hand-rolled MAC framings, three different rules, and one of them collides

- **Where:** `tenancy/scope.go:65-76` (`bind`), `tenancy/scope.go:84-93`
  (`Scope.Digest`), `tenancy/grant.go:185-204` (`grantBinding`),
  `tenancy/seal.go:83-98` (`Sealer.mac`). Shared primitives at
  `tenancy/scope.go:97-104`.
- **Scale:** systemic (4 constructions of one decision).
- **Confidence:** CONFIRMED. Every field in all four is length-prefixed through
  `writeField`/`writeBytes`, so that half is DRY. What is *not* shared is the rule
  for variable-length **sequences**. `Sealer.mac` seals the element count
  (`seal.go:85-87`); `grantBinding` seals neither the class-list count nor the
  cohort count and emits both as flat runs of framed fields
  (`grant.go:193-200`). A faithful transcription of `grantBinding` run against
  two different grants:

  ```
  $ go run .   # scratchpad transcription of grant.go:185 and seal.go:83
  grantBinding {read,write} over [a]        = 24ba1ac7a61ac1b17886f24582847149eee6471cd517fdde4a568b9e2ec214e1
  grantBinding {read}       over [write, a] = 24ba1ac7a61ac1b17886f24582847149eee6471cd517fdde4a568b9e2ec214e1
  RESULT: COLLISION — the class list and the cohort run together; neither count is sealed

  sealMac 2 fields [q][job] = 30085c4a5e6c2f1b797f73a57494790659008ff2951b544b19c8cfebd17d9200
  sealMac 1 field  [qjob]   = c9682482a6b52cb129e641349e0d8ff45724b90a70afc46070c9c59a4e5ecb5d
  RESULT: distinct — the count is inside the MAC
  ```

  `write` is an acceptable `Reference` (`reference.go:40-53` rejects only empty,
  over-128-byte, non-UTF-8, space-bearing and control-bearing values), so both
  grants are constructible through `Accept`.
- **What / Why this severity:** `Authority.holds` (`grant.go:163-169`) exists to
  answer "is this the exact grant I issued". It cannot distinguish a grant that
  permits **read+write** over tenant `a` from a grant that permits **read** over
  tenants `write` and `a`. The two share a binding. `seal.go:36-38` states the
  correct rule in prose — *"Each is length-prefixed and the count is sealed with
  them, because a token whose fields can be slid past one another is a token that
  authenticates a different record than it travelled with"* — and the sibling MAC
  200 lines away does not follow it. This is the textbook consequence of writing
  a decision in four places: the four have already diverged and only one comment
  records which is right.
- **Honest bound on exploitability:** I did **not** demonstrate privilege
  escalation. `Grant`'s fields are unexported and only `Accept` constructs one,
  so an attacker outside the package cannot present the second grant carrying the
  first's binding without `reflect`+`unsafe`. The defect is that the
  authenticator is not injective, not that a live path abuses it today.
  `TestEveryPartOfAGrantIsInsideItsBinding` (`grant_binding_test.go:40-83`) walks
  six single-field edits and none of them is a cross-boundary slide, so nothing
  in the suite would notice if this got worse.
- **Why this timing:** the framing rule is the contract that every future MAC in
  this package will copy. A fifth construction added before this is settled
  copies whichever neighbour the author read.
- **Close criteria:**
  - [ ] one framing helper owns "a sequence goes into a MAC" and all four call sites use it
  - [ ] `grantBinding` seals both counts
  - [ ] a test asserts `grantBinding({read,write}, [a]) != grantBinding({read}, [write, a])`
  - [ ] the prose invariant lives in one place, not in `seal.go`'s comment

### GAP-2 [high][immediate] The refusal sentinel set is written in four places; a fifth sentinel silently produces an undeclared empty `Outcome`

- **Where:** `tenancy/errors.go:10-31` (the declarations), `tenancy/errors.go:34-37`
  (`refusals`), `tenancy/outcome.go:30-42` (`outcomeOf`),
  `tenancy/vocabulary_test.go:63-67` (the test's own hand-written list).
- **Scale:** systemic (4 places that must agree by hand; 0 mechanisms that check).
- **Confidence:** CONFIRMED.

  ```
  $ grep -rn 'outcomeOf\|refusals' tenancy/
  tenancy/errors.go:34:var refusals = [...]error{
  tenancy/errors.go:51:	for _, refusal := range refusals {
  tenancy/outcome.go:30:var outcomeOf = map[error]Outcome{
  tenancy/outcome.go:51:	for _, refusal := range refusals {
  tenancy/outcome.go:53:			return outcomeOf[refusal]
  ```

  `outcomeOf` is read at exactly one site, keyed by a member of `refusals`. A
  transcription of `errors.go:34-56` + `outcome.go:30-59` with one sentinel added
  to `refusals` only:

  ```
  $ go run .
  OutcomeFor(ErrQuarantined) = ""
  is it one of the declared twelve? false
  does it equal OutcomeError?       false
  ```
- **What / Why this severity:** `OutcomeFor`'s own comment (`outcome.go:44-46`)
  says *"the answer comes from a closed set of twelve constants"*. A contributor
  adding a twelfth refusal edits `errors.go` — the obvious file — and the map miss
  returns Go's zero `Outcome`, the empty string. Every dashboard, metric label and
  queue-outcome column keyed on this receives `""`: neither a named refusal nor
  `OutcomeError`, so it is invisible to both the success panel and the error
  panel. `TestTheOutcomeVocabularyIsClosed` (`vocabulary_test.go:60-90`) cannot
  catch it, because its list of sentinels is the fourth hand-written copy.
- **Bonus defect, same cause:** `outcomeOf[ErrMalformed]` (`outcome.go:41`) is
  **dead** — `ErrMalformed` is deliberately absent from `refusals`, which is the
  only key set that reaches the map — and the same decision is restated as a
  literal thirteen lines later at `outcome.go:56-58`. Two copies of one mapping,
  one of them unreachable.
- **Why this timing:** the sentinel list is a public contract other subsystems
  classify against. Any new refusal added before this is one mechanism is added
  in four places or in one.
- **Close criteria:**
  - [ ] one declaration is the source of the sentinel set; `refusals` and `outcomeOf` derive from it, or `outcomeOf` becomes the single table and `refusals` its key view
  - [ ] `OutcomeFor` cannot return a value outside `Outcomes()` — a total function, not a map lookup with a zero value
  - [ ] the test enumerates the set rather than restating it (the repository already does this style of structural check in `scripts/`)
  - [ ] `outcomeOf[ErrMalformed]` and the literal branch are one thing

### GAP-3 [high][immediate] Redaction is a four-method protocol nobody declared, filled 13 of 20 times; `tenancycache.Key` leaks the raw tenant reference

- **Where:** `tenancy/reference.go:25-35`, `tenancy/scope.go:21-25` and `:38-48`,
  `tenancy/grant.go:57-67`, `tenancy/tenancycache/cache.go:35`.
- **Scale:** systemic (5 types × 4 methods; 7 slots empty; 0 tests).
- **Confidence:** CONFIRMED by a Go program run against the library.

  | type | `String` | `Format` | `LogValue` | `MarshalJSON` | `GoString` |
  |---|---|---|---|---|---|
  | `Reference` | yes | yes | yes | yes | no |
  | `Scope` | yes | yes | yes | yes | no |
  | `Resolution` | yes | yes | **no** | **no** | no |
  | `*Grant` | yes (ptr) | yes (ptr) | yes (ptr) | yes (ptr) | no |
  | `tenancycache.Key` | yes | **no** | **no** | **no** | no |

  Probe output (tenant reference is the literal `SECRETTENANT`):

  ```
  === 3. tenancycache.Key ===
  REDACTED     Key %v      [tenant cache key]
  REDACTED     Key %+v     [tenant cache key]
  *** LEAK *** Key %#v     tenancycache.Key[string]{scope:tenancy.Scope{reference:tenancy.Reference{value:"SECRETTENANT"}, lifecycle:0x2, epoch:0x7, binding:[32]uint8{0x21, …}}, key:"cart:42"}
  *** LEAK *** []Key %#v   []tenancycache.Key[string]{tenancycache.Key[string]{scope:tenancy.Scope{reference:tenancy.Reference{value:"SECRETTENANT"}, …

  === 4. tenancy.Grant / Purpose / Member ===
  REDACTED     Grant %v            [tenancy grant]
  *** LEAK *** *Grant deref %+v    {purpose:{value:monthly-billing} cohort:[{value:SECRETTENANT}] …
  *** LEAK *** *Grant deref %#v    tenancy.Grant{purpose:…, cohort:[]tenancy.Reference{tenancy.Reference{value:"SECRETTENANT"}}, …

  === 1. direct fmt verbs ===
  REDACTED     Reference %#v       [tenant reference]     <- Format wins over GoString for every verb
  ```
- **What / Why this severity:** `Reference` and `Scope` are safe under `%#v`
  because `fmt.handleMethods` consults `Formatter` before `GoStringer`, for every
  verb. `tenancycache.Key` implements only `String`, so it has neither, and any
  `%#v` — a debug print, a `t.Logf("%#v")`, a panic formatter, an error wrapper —
  renders the tenant in the clear. `*Grant` is protected only through a pointer:
  `fmt.Printf("%+v", *grant)` leaks the entire cohort. The library spends
  thirteen methods and five comment blocks on this property and has **zero tests
  for it**:

  ```
  $ grep -rn 'tenant reference\]\|tenancy scope\]\|LogValue\|MarshalJSON\|%#v' tenancy --include='*_test.go'
  (no output)
  ```
- **The repository already owns the right answer.** `utils/vvdb.Secret`
  (`utils/vvdb/secret.go:16-42`) implements six methods — `String`, **`GoString`**,
  `Format`, `MarshalJSON`, `MarshalText`, `LogValue` — through one private
  `display()` (`secret.go:18-23`), and pins them in
  `TestSecretsStayOutOfOrdinaryRendering` (`utils/vvdb/secret_test.go:16`).
  `tenancy` re-derived a weaker version of that protocol five times.
  `utils` is the SHARED tier, so `tenancy` importing it is structurally legal.
- **Residual hole that is not tenancy's fault, stated so it is not mistaken for one:**
  a `Reference` or `Scope` held in an **unexported** struct field leaks under
  `%v`, `%+v`, `%#v` and `slog`'s text handler, because `printValue` skips
  `handleMethods` when `reflect.Value.CanInterface()` is false. Proven:

  ```
  *** LEAK *** struct UNexported-field %v    {{SECRETTENANT} {{SECRETTENANT} 2 7 [33 15 …]}}
  *** LEAK *** slog text: unexported struct  … holder="{ref:{value:SECRETTENANT} …
  ```

  No method can close that; `vvdb.Secret` has the identical residual. It belongs
  in a decision doc as a stated limit, not in a fix.
- **Also proven safe, so the audit is not one-sided:** `json.Marshal` of a struct
  with an exported `Reference` field errors and emits nothing; `slog` JSON of the
  same emits `"!ERROR:json: error calling MarshalJSON…"` and no tenant;
  `text/template` `{{.Ref}}` renders `[tenant reference]`; `[]Reference`,
  `map[string]Reference` and `map[Reference]int` all redact; `Lookup` of an
  unknown tenant returns `tenancy: the tenant has no mapping for this capability:
  forbidden` with no tenant in it.
- **Why this timing:** this is the invariant the whole `Reference` opaque-type
  design exists to buy, it is unowned and untested, and every new tenant-derived
  type (a partition, a lease key, a job identity) will copy whichever of the five
  existing shapes its author read.
- **Close criteria:**
  - [ ] one declared protocol — an interface or an embedded redacting type — instead of five hand-copied method sets
  - [ ] `tenancycache.Key` renders `[tenant cache key]` under `%#v` and `%+v`
  - [ ] `Grant`'s methods survive dereference, or `Grant` is documented as pointer-only
  - [ ] one test walks every redacted type × every verb × `slog` × `json`, in the shape of `utils/vvdb/secret_test.go:16`
  - [ ] the unexported-field limit is written down in a decision doc

### GAP-4 [medium][immediate] A nil `*Authority` is answered four different ways; six of nine public methods panic

- **Where:** `tenancy/context.go:40-42` and `tenancy/seal.go:25-27` return
  `ErrNoScope`; `tenancy/tenancydb/database.go:81-83` returns a constructed
  `error`; `tenancy/tenancyrow/row.go:217-219` calls `panic`;
  `tenancy/authority.go:78`, `:82`, `:90`, `:104`, `grant.go:79`, `grant.go:117`
  guard nothing.
- **Scale:** systemic (4 policies for one question, 6 unguarded entry points).
- **Confidence:** CONFIRMED.

  ```
  REFUSES  Authority.Scope     tenancy: no verified tenant scope: forbidden
  REFUSES  Authority.Sealer    tenancy: no verified tenant scope: forbidden
  PANIC    Authority.Verify    runtime error: invalid memory address or nil pointer dereference
  PANIC    Authority.Bind      runtime error: invalid memory address or nil pointer dereference
  PANIC    Authority.Lookup    runtime error: invalid memory address or nil pointer dereference
  PANIC    Authority.Admits    runtime error: invalid memory address or nil pointer dereference
  PANIC    Authority.Accept    runtime error: invalid memory address or nil pointer dereference

  PANIC    nil.With(a real, non-zero scope)   runtime error: invalid memory address or nil pointer dereference
  PANIC    nil.Each(ctx, a real grant, ...)   runtime error: invalid memory address or nil pointer dereference
  ```

  `With` and `Each` appear to refuse only when handed a degenerate argument: `With`
  returns `ErrNoScope` from `minted`'s `scope.IsZero()` check (`authority.go:123`)
  *before* it reads `this.salt`, and `Each` returns `ErrGrantRequired` for a nil
  grant (`grant.go:118`). With real arguments both panic.
- **What / Why this severity:** the property has a name and two tests —
  `TestASeamWiredWithNoAuthorityRefusesRatherThanPanics`
  (`tenancy/tenancycache/cache_test.go:147`,
  `tenancy/tenancystorage/storage_test.go:145`), whose shared comment says the
  answer must be *"a refusal rather than a panic in a handler that had a perfectly
  good tenant"*. Both tests reach `Authority.Scope`, which is one of the two
  guarded methods. A consumer whose fx graph delivers a nil `*Authority` to an
  HTTP middleware calling `Bind` gets a 500 and a stack trace instead of that
  refusal, and no test covers it.
- **Why this timing:** it is one decision spread over four bodies. Fixing it later
  means finding all four again, and every new adapter picks one of the four.
- **Close criteria:**
  - [ ] one rule, written once: either every `*Authority` method nil-guards, or none does and the constructors are the only gate
  - [ ] the two seam tests are extended to the core methods, or deleted as proving less than their name claims

### GAP-5 [medium][deferred] Six spellings of "build an authority for one tenant", and ~125 byte-identical duplicated test-helper lines

- **Where:**
  - `reference` + `authorityFor` + `activeAuthority`, a **28-line block, identical in three packages**: `tenancy/scope_test.go:18-45`, `tenancy/tenancyrow/support_test.go:11-38`, `tenancy/tenancydb/support_test.go:13-41`.
  - `directory` + `manyTenantAuthority`, a **36-line block, identical in two**: `tenancy/vocabulary_test.go:127-162`, `tenancy/tenancydb/support_test.go:65-100` (differ by one character: an unused parameter is named `ctx` in one and dropped in the other).
  - `switchable`, identical in `tenancy/scope_test.go:226-236` and `tenancy/tenancydb/support_test.go:43-54`.
  - `bound`, **8 lines identical** in `tenancy/tenancydb/support_test.go:56-63` and `tenancy/tenancyrow/row_test.go:40-47`.
  - `secretReference = "acme-7f3c"` in `tenancyrow/support_test.go:9` and `tenancydb/support_test.go:11`.
  - `fixedAuthority` in three packages with **three different signatures**: `tenancyjobs/support_test.go:12` → `(*Authority, Scope)` with a durable key, `tenancycache/cache_test.go:12` → `*Authority`, `tenancystorage/storage_test.go:13` → `(*Authority, Scope)` without one.
  - Names for one concept: `fixedAuthority`, `authorityFor`, `activeAuthority`, `authorityOnly`, `manyTenantAuthority`.
- **Scale:** systemic (~125 duplicated lines of 3376 test lines).
- **Confidence:** CONFIRMED.

  ```
  $ diff <(sed -n '18,45p' tenancy/scope_test.go) <(sed -n '11,38p' tenancy/tenancyrow/support_test.go)
  IDENTICAL 28 lines
  $ diff <(sed -n '56,63p' tenancy/tenancydb/support_test.go) <(sed -n '40,47p' tenancy/tenancyrow/row_test.go)
  IDENTICAL 8 lines
  $ diff <(sed -n '127,162p' tenancy/vocabulary_test.go) <(sed -n '65,100p' tenancy/tenancydb/support_test.go)
  5c5
  < func (this directory) Resolve(ctx context.Context) (tenancy.Resolution, error) {
  ---
  > func (this directory) Resolve(context.Context) (tenancy.Resolution, error) {
  ```
- **What / Why this severity:** this is `get_user` / `get_user_by_id` /
  `fetch_user` from architecture.md's DRY section, in test scaffolding. The cost
  is real: a change to `tenancy.Spec` — a new required field, a renamed one — has
  to be applied to six bodies, and the compiler will point at all six only if all
  six are still compiled. Divergence has already begun: `tenancystorage`'s
  `fixedAuthority` omits the `DurableKey` that `tenancyjobs`' sets, so the two
  packages test against differently-configured authorities under one name.
- **Counter-argument, weighed:** Go genuinely forbids sharing helpers across test
  packages. That forces *some* duplication — but not this much and not silently.
  The repository already solved it once with `crud/crudtest`, an in-memory source
  published for exactly this. A `tenancy/tenancytest` would carry `reference`,
  `authorityFor`, `activeAuthority`, `bound`, `directory` and `switchable` once.
- **Why this timing:** deferred. It is test-internal, no external contract moves,
  and nothing is currently wrong — only expensive.
- **Close criteria:**
  - [ ] one support package owns the fake resolver and the authority builders
  - [ ] one name per concept; the five `*Authority` builders become one with named thin wrappers
  - [ ] no two packages hold a byte-identical helper

### GAP-6 [medium][immediate] The `Class` set is encoded in five places, one of them a bare array width that panics on a fourth class

- **Where:** `tenancy/lifecycle.go:41-43` (constants), `:46` (`Valid`), `:50-54`
  (`String`), `:61` (`Classes()`), `:63` (`type Admission struct{ states [3]uint8 }`).
  Same shape for `Lifecycle` in four places: `:6-12`, `:16`, `:21-31`, `:110`.
- **Scale:** systemic (5 + 4 encodings of two closed sets).
- **Confidence:** CONFIRMED by reading; the code is five lines and unambiguous.
  `Admit` writes `admission.states[class] |= 1 << state` (`lifecycle.go:72`); the
  only bound check is `class.Valid()` (`:67`), which reads `this <= ClassDurable`
  (`:46`); the backing array is `[3]uint8` (`:63`).
- **What / Why this severity:** adding a fourth class — the roadmap's own
  `ClassAnalytics`-shaped case — means editing the const block, `Valid`, `String`,
  `Classes()` and `[3]uint8`. Four of those five the compiler cannot check. Update
  the first four and forget the array width and `Admit(ClassAnalytics, Active)`
  panics with an index-out-of-range at **construction time in the composition
  root**, from a line that mentions no array. Update only `Classes()` and the new
  class is silently absent from `grantBinding`'s class loop (`grant.go:193`),
  which means a grant naming it authenticates identically to a grant that does not
  — compounding GAP-1. `Lifecycle` is the same shape with a softer failure: forget
  `String()` and a new state renders as `"unknown"`, which
  `TestALifecycleNobodyDeclaredStillNamesItselfSafely`
  (`vocabulary_test.go:92-99`) asserts is the *correct* answer for an undeclared
  state — so the test would pass on a state that is declared and merely
  unformatted. Note also that `Class` and `Outcome` each have an enumerator
  (`Classes()`, `Outcomes()`) and `Lifecycle` has none, although
  `inconsistent` (`:110`) needs one and open-codes the range.
- **Why this timing:** it is the vocabulary the whole subsystem branches on, and
  the array width is a latent panic in a constructor.
- **Close criteria:**
  - [ ] `Admission`'s width derives from `len(Classes())` or the array is indexed through a checked accessor
  - [ ] `Lifecycles()` exists and `Valid`, `String` and `inconsistent` all read it
  - [ ] a test asserts every declared `Class` and `Lifecycle` has a `String()` other than `"unknown"`

### GAP-7 [medium][deferred] Thirty comment blocks, none on a function the rule permits; the prose is design rationale that the repository already keeps in `docs/ai/decisions/`

- **Where:** all 30 blocks, tabulated below.
- **Scale:** systemic (30 of 30 non-test blocks fail the gate; 152 lines, 8.8% of source).
- **Confidence:** CONFIRMED — comment lengths and declaration lengths measured
  with `go/ast`, not eyeballed.

  ```
  $ go run <ast walker> ./tenancy
  TOTAL doc_comment_lines=202 other_comment_lines=16     (whole tree, tests included)
  blocks=30 lines=152                                    (non-test source only)
  funcs >= 40 lines: 0   (of 129)
  longest documented declaration: 33 lines (Borrow, Accept)
  ```

  | location | comment lines | documents | its lines | verdict |
  |---|---|---|---|---|
  | `context.go:15` | 8 | `(*Authority).With` | 10 | rationale — belongs in a decision doc |
  | `context.go:34` | 5 | `(*Authority).Scope` | 20 | consumer reference for `Spec.Revalidate` — belongs in `docs/modules/*/tenancy.md` |
  | `errors.go:39` | 8 | `Classify` | 11 | rationale + a closing sentence that exists only to justify the export |
  | `grant.go:75` | 4 | `(*Authority).Accept` | 33 | rationale; cites `INV-2`, an artifact ID |
  | `grant.go:113` | 4 | `(*Authority).Each` | 30 | rationale |
  | `grant.go:171` | 3 | `permits` | 10 | **misattached** — the text describes `grantBinding` (`:185`), which has no comment; `permits` touches no MAC |
  | `grant.go:212` | 4 | `(*Authority).permittedByGrant` | 13 | rationale |
  | `lifecycle.go:100` | **9** | `(Admission).inconsistent` | **8** | rationale longer than the function |
  | `outcome.go:44` | 3 | `OutcomeFor` | 14 | asserts an invariant the code does not hold — see GAP-2 |
  | `reference.go:37` | 3 | `(Reference).valid` | 14 | describes `==` semantics of the *type*; `valid()` compares nothing |
  | `scope.go:50` | **7** | `(Scope).boundTo` | **7** | one justified clause (constant-time compare) inside six lines of rationale |
  | `scope.go:78` | 6 | `(Scope).Digest` | 10 | rationale |
  | `seal.go:15` | **7** | `type Sealer struct{…}` | **1** | worst ratio in the package |
  | `seal.go:34` | 7 | `(Sealer).Seal` | 15 | rationale; states the framing invariant `grantBinding` breaks (GAP-1) |
  | `seal.go:57` | 4 | `(Sealer).Unseal` | 21 | rationale |
  | `tenancycache/cache.go:15` | 2 | `type Key` | 4 | restates the struct's shape |
  | `tenancycache/cache.go:22` | 3 | `Keyed` | 7 | **near-verbatim duplicate** of `tenancystorage/storage.go:17` |
  | `tenancycache/cache.go:37` | 6 | `Partition` | 12 | closest to justified (threshold note) but never says how `16` was derived |
  | `tenancydb/database.go:113` | 6 | `(*Directory).Borrow` | 33 | rationale |
  | `tenancydb/database.go:153` | 6 | `(*Directory).reserve` | 19 | **arguably justified** — a concurrency invariant the code cannot show |
  | `tenancydb/database.go:179` | 4 | `(*Directory).open` | 19 | **arguably justified** — explains `context.WithoutCancel` |
  | `tenancyjobs/jobs.go:10` | 5 | `ContextProvider` | 10 | rationale |
  | `tenancyjobs/jobs.go:71` | 5 | `(identityRestorer).RestoreIdentity` | 25 | rationale |
  | `tenancyjobs/jobs.go:102` | 2 | `record` | 4 | **near-verbatim duplicate** of `seal.go:34-36` |
  | `tenancyrow/row.go:43` | 7 | `Column` | 13 | rationale |
  | `tenancyrow/row.go:169` | 2 | `(through).Frozen` | 6 | restates `return []string{this.local}` |
  | `tenancyrow/row.go:178` | **6** | `(through).Apply` | **6** | rationale as long as the function |
  | `tenancyrow/row.go:240` | 7 | `relationScopes` | 12 | rationale |
  | `tenancystorage/storage.go:17` | 3 | `Namespace` | 7 | **near-verbatim duplicate** of `tenancycache/cache.go:22` |
  | `tenancystorage/storage.go:36` | 6 | `namespaceOf` | 10 | rationale; cites `INV-25`, an artifact ID |

- **What / Why this severity:** three of the thirty carry a *why* names cannot
  (`scope.go:50` constant-time comparison, `database.go:153` reserve-before-open,
  `database.go:179` detached cancellation); the rest are design rationale. This is
  not a style quibble here, because `CLAUDE.md` states that `docs/` "is where the
  reasoning lives" and makes `docs/ai/decisions/` binding. Rationale in a comment
  is therefore the same decision in two places, with the usual consequence: two
  of the pairs above are already near-verbatim copies of each other
  (`cache.go:22` ≈ `storage.go:17`; `jobs.go:102` ≈ `seal.go:34`), one is attached
  to the wrong function (`grant.go:171`), one describes a property its function
  does not have (`reference.go:37`), and one asserts an invariant GAP-2 disproves
  (`outcome.go:44`). Removing a comment that fails the rule is explicitly
  "expected cleanup, not a regression" in both `CLAUDE.md` and `AGENTS.md`.
- **Why this timing:** deferred. No contract moves; the code is correct with or
  without the prose. It is 152 lines that will drift.
- **Close criteria:**
  - [ ] every surviving comment sits on a 40+ line declaration, or is the calibration note / external-constraint citation the rule allows
  - [ ] `grant.go:171` is attached to `grantBinding` or deleted
  - [ ] the two duplicated pairs become one statement, in the flow or decision that owns it
  - [ ] `outcome.go:44`'s claim is either made true (GAP-2) or removed

### GAP-8 [low][deferred] Twenty comment blocks narrate tests; six packages have no package doc; `doc.go` is one line

- **Where:** `tenancy/binding_test.go:8` (6 lines), `:51`; `tenancy/contract_test.go:12`,
  `:38`, `:69`; `tenancy/scope_test.go:282`; `tenancy/seal_test.go:45`, `:84`,
  `:116`, `:178`; `tenancy/tenancycache/cache_test.go:79`, `:143`;
  `tenancy/tenancystorage/storage_test.go:141`;
  `tenancy/tenancyjobs/durable_test.go:351`, and inline at `:198`, `:268`;
  `tenancy/tenancyrow/contract_test.go:17`, `:57`; inline at
  `tenancy/tenancydb/database_test.go:381` and `tenancy/tenancyrow/row_test.go:371`.
  Package docs: `tenancy/doc.go` (1 line, no doc), and none in `tenancyrow`,
  `tenancydb`, `tenancyjobs`, `tenancystorage`, `tenancycache`.
- **Scale:** systemic (20 blocks, 66 lines).
- **Confidence:** CONFIRMED — `go/ast` inventory above; and

  ```
  $ head -3 tenancy/doc.go
  package tenancy
  $ grep -rn -B1 '^package ' tenancy --include='*.go' | grep '^tenancy.*-//'
  (no output)
  ```
- **What / Why this severity:** `CLAUDE.md` says "do not narrate tests" without a
  length exemption; twenty blocks do. The inverse is also true and is the sharper
  half: readability.md §3 requires the module docstring to carry the
  one-sentence responsibility, and `tenancy/doc.go` — a file whose only possible
  job is that sentence — carries nothing, while 152 lines of prose sit inside the
  package explaining individual functions. The prose is in the place the rule
  forbids and absent from the place the rule requires. A `doc.go` holding only
  `package tenancy` is also a file that exists for no responsibility, which
  architecture.md bans by name.
- **Why this timing:** deferred; cosmetic against the running system.
- **Close criteria:**
  - [ ] every `tenancy/*` package states its responsibility in one sentence with no "and"
  - [ ] `doc.go` holds that sentence for `tenancy` or is deleted
  - [ ] test comments are gone or have become test names

### GAP-9 [medium][immediate] `_examples/tenancy-sharedrow/main.go` prints the raw tenant reference, in the function whose comment says it does not

- **Where:** `_examples/tenancy-sharedrow/main.go:135-145`, specifically `:143`.
- **Scale:** local (1 occurrence — but it is the repository's only end-to-end wiring).
- **Confidence:** CONFIRMED by reading:

  ```go
  func refusalFor(authority *tenancy.Authority, ctx context.Context) string {
      scope, err := authority.Scope(ctx, tenancy.ClassWrite)
      switch {
      case errors.Is(err, tenancy.ErrNoScope):
          return "refused before any statement, and the message names no tenant"
      case err != nil:
          return err.Error()
      default:
          return "bound to " + scope.Lifecycle().String() + " tenant " + scope.Reference().Value()
      }
  }
  ```

  and by `main.go:94`, which prints its result to stdout.
- **What / Why this severity:** `Reference.Value()` is the one deliberate escape
  hatch from the redaction that `String`, `Format`, `LogValue` and `MarshalJSON`
  exist to enforce. The example reaches for it on the success path, three lines
  below a branch that advertises *"the message names no tenant"*. This is the
  file `CLAUDE.md` describes as "the wiring a consumer copies", and what it
  teaches is `scope.Reference().Value()` in a display string. Combined with GAP-3
  — where the same value already leaks through `%#v` — the single-format-place
  law has no enforcement anywhere in this subsystem: not a test, not a lint, and
  the example works against it.
- **Why this timing:** it is the copied artefact. Every consumer who starts here
  starts with a tenant identifier in their logs.
- **Close criteria:**
  - [ ] the example never calls `Reference().Value()` for display
  - [ ] the demonstration of a bound tenant uses `scope.Reference()` under `%v`, or a digest

### GAP-10 [low][deferred] Naming and shape defects that make the code need the comment

- **Where and what:**
  1. `_examples/tenancy-sharedrow/main.go:135` — `refusalFor(authority *tenancy.Authority, ctx context.Context)` puts `context.Context` **second**. Every one of the 30+ context-taking functions in `tenancy/` puts it first. `go vet` does not catch parameter order.
  2. `_examples/tenancy-sharedrow/main.go:135` — `refusalFor` returns `"bound to active tenant 42"` on the **success** path. The name says refusal; the default branch says success.
  3. `_examples/tenancy-sharedrow/main.go:115` and `:157` — the tenant list `[]string{"42", "43"}` is written twice, in `directory()` and in `cohort()`. Adding a tenant to one and not the other yields a grant whose member `Each` will report as `unmapped`.
  4. `tenancy/tenancyrow/row.go:191-214` — `resolveRelation` does two things: it validates a relation path (panicking on failure) and it computes the first hop's local key. `Through` (`:136`) uses the return value; `Column` (`:58`) **discards it** and calls the function purely for the panic. The name announces neither job, and nothing says the returned key is the *first* hop's — `local` is assigned inside the loop under `if local == ""` (`:201-203`), so a two-segment path freezes only the first link.
  5. `tenancy/tenancyrow/row.go:112` — `if action != crud.ActionCreate || this.mode == Validate || !held.IsZero()` folds three unrelated reasons (wrong verb / configured mode / field already populated) into one negated disjunction returning one message, `"row is owned by a different tenant"`, which is accurate for none of the three.
  6. `tenancy/tenancyrow/row.go:31` — `orDefault` is not domain vocabulary; readability.md §4 asks the name to state *what*, and what it states is `ownershipValueOrTheReferenceItself`.
  7. `tenancy/tenancyrow/row.go:222`, `:230`, `:252` — "which class does this verb need" is decided in three places in one function: a `ClassRead` literal, `classOf(action)`, and another `ClassRead` literal.
  8. One concept — a resolver that answers from a map — carries four names: `knownTenants` (`tenancy/support_test.go:5`, `tenancyjobs/support_test.go:42`), `directory` (`vocabulary_test.go:127`, `tenancydb/support_test.go:65`), `switchable` (`scope_test.go:230`, `tenancydb/support_test.go:43`), `controlPlane` (`_examples/…/main.go:48`).
  9. Five redaction placeholders, two prefixes: `"[tenant reference]"`, `"[tenant resolution]"`, `"[tenant cache key]"` against `"[tenancy scope]"`, `"[tenancy grant]"`.
- **Scale:** systemic (9 distinct items, all naming/shape).
- **Confidence:** CONFIRMED by reading, each with a location.
- **Why this timing:** deferred, except item 3 which is inside GAP-9's file and
  rides along with it.
- **Close criteria:**
  - [ ] `ctx` first everywhere
  - [ ] `refusalFor` renamed or split
  - [ ] `resolveRelation` split into a validator and a `firstHopLocalKey`
  - [ ] `column.Apply`'s three refusal reasons produce three messages
  - [ ] one name per concept for the fake resolver and the placeholder prefix

### GAP-11 [low][deferred] `.golangci.yml` configures a linter no build target runs

- **Where:** `/home/user/ws/gd/lease/frostgrove/vv/framework/.golangci.yml` (5 lines).
- **Scale:** local.
- **Confidence:** CONFIRMED.

  ```
  $ grep -n 'golangci\|staticcheck\|lint' Makefile scripts/checks.sh
  (no output)
  $ which golangci-lint
  golangci-lint not found
  ```
- **What:** the file's only content is `revive:receiver-naming: disabled` — the
  rule that permits this repository's `this` convention. Nothing runs it, so the
  convention is held by memory, and the exemption is invisible to anyone who runs
  `staticcheck` instead (which reports 88 ST1006 hits, all of them the sanctioned
  convention). This audit had to read `.golangci.yml` to know not to report them.
- **Close criteria:**
  - [ ] a `make lint` target, or the file is deleted and the convention written into `CLAUDE.md`'s house-style section

## Checked and clean — the commands, so silence is not the evidence

- **Nesting and function size.** `go/ast` walk of all 129 non-test functions:
  max nesting depth **2** (threshold 3, target 2), **0** functions at or above 40
  lines, longest 33. Guard clauses are used consistently; there is no `else` after
  a returning guard anywhere in `tenancy/`. This is the strongest part of the
  subsystem and it is not close.
- **`this` receiver consistency.** 88 of 88 named receivers use `this`; the three
  unnamed ones are all `MarshalJSON`, which does not read the receiver
  (`reference.go:33`, `scope.go:46`, `grant.go:65`). No inconsistency.
- **Error comparison.** No error is compared by string anywhere in `tenancy/`;
  `Classify` (`errors.go:47`) and `OutcomeFor` (`outcome.go:47`) both use
  `errors.Is` against exported sentinels. `grep -rn 'err.Error() ==' tenancy` is
  empty.
- **Failure messages.** Test failures state what broke in plain words —
  `"one logical key resolved to one cache entry for two tenants"`
  (`tenancycache/cache_test.go:59`), `"this field is outside the binding, so a
  scope carrying it can be edited without the authority noticing"`
  (`binding_test.go:45`). No `got != want` in the subsystem.
- **`orDefault` itself.** Two call sites (`row.go:60`, `row.go:141`), one body.
  DRY is satisfied; only the name is at issue (GAP-10.6).
- **`Scope.Digest` framing.** Framed — `writeField(digest, reference.Value())`
  then a fixed 8-byte epoch (`scope.go:86-89`). No ambiguity, because everything
  after the framed field is fixed width. Same for `bind` (`scope.go:65-76`).
- **"Validate then classify" across the adapters.** Seven `authority.Scope(ctx,
  class)` call sites in five packages (`database.go:218`, `cache.go:26`,
  `storage.go:21`, `row.go:222`/`:230`/`:252`, `jobs.go:36`). This is **not**
  reported as duplication: each does something structurally different with the
  scope afterwards, and architecture.md's counter-rule is explicit that shared
  coincidence is not shared meaning. A future requirement would not change all
  seven together.
- **`Narrow`/`Relations` in `column` and `through`.** Four bodies
  (`row.go:72-78`, `:80-93`, `:151-157`, `:159-165`). They share a three-line
  shape — resolve the value, build an `Eq` — and differ in what the predicate
  addresses: `column` names its own column, `through` names a dotted relation
  path. Merging them would need a parameter that selects the addressing mode,
  which is the flag parameter architecture.md bans. **Not a finding.** The one
  thing worth watching is the path format `this.path + "." + this.field`
  (`row.go:156`) against the relative field inside `AtPath` (`row.go:90`, `:164`)
  — two addressing conventions for one relation — but that shape is
  `crud.RelationScopes.AtPath`'s contract (`crud/scope.go:14`), not tenancy's
  decision, so it is out of this scope.
- **`gofmt -l ./tenancy`** — empty. **`go vet ./tenancy/...`** — clean.
  **`staticcheck ./tenancy/...`** — 0 findings outside the deliberately disabled
  ST1006. **`go vet`** and **`go build`** on `_examples/tenancy-sharedrow` — clean;
  `gofmt -l` on it — empty.

## Remediation order

Boundaries and contracts before internals; the things other fixes rest on first.

1. **GAP-2 — one sentinel table.** (S, blast radius: `errors.go`, `outcome.go`,
   `vocabulary_test.go`.) First because GAP-1's test and GAP-4's refusal policy
   both name sentinels, and because it is the cheapest structural fix here.
2. **GAP-1 — one MAC framing helper.** (M, blast radius: `scope.go`, `grant.go`,
   `seal.go`, plus `binding_test.go` and `grant_binding_test.go`.) Before GAP-6,
   because a fourth `Class` changes `grantBinding`'s class loop and you want the
   framing settled before the vocabulary widens. Changes no exported signature.
3. **GAP-3 — one redaction protocol, modelled on `utils/vvdb.Secret`.** (M,
   blast radius: five types in `tenancy/` and `tenancycache/`, one new test file,
   `docs/api/surface.md` if `GoString` is added.) Independent of 1 and 2, and the
   only finding here with a demonstrated leak of a live value.
4. **GAP-9 — the example stops printing the reference.** (S, blast radius: one
   file.) Immediately after GAP-3, so the example demonstrates the fixed
   protocol rather than the escape hatch. Carries GAP-10.1/.2/.3 with it.
5. **GAP-4 — one nil-authority rule.** (S, blast radius: `authority.go`,
   `grant.go`, two seam tests.) After 1, because the chosen refusal is a sentinel.
6. **GAP-6 — `Classes()`/`Lifecycles()` as the single source, and a derived
   `Admission` width.** (S–M, blast radius: `lifecycle.go`, `grant.go:193`.)
   After 2.
7. **GAP-5 — a `tenancy/tenancytest` support package.** (M, blast radius: every
   `*_test.go` in six packages; nothing shipped.) Deliberately after the source
   fixes, so the helpers are extracted once against the final API rather than
   twice.
8. **GAP-7, GAP-8, GAP-10 — comment cleanup, package docs, naming.** (M for the
   comments by volume, S each otherwise; blast radius: no compiled behaviour.)
   Last: pure text, and several of the comments being deleted describe code that
   items 1–6 will have changed.
9. **GAP-11 — wire a linter or delete the config.** (S.) Any time; independent.

## What I did not check

- **Every other dimension.** Microkernel, building blocks, architecture metrics
  beyond size/nesting, universality/hardcode, data integrity and concurrency,
  restrictions, and test strength were out of scope. In particular I did **not**
  assess whether `tenancydb.Directory`'s locking is correct, whether the security
  gate narrows every verb, or whether the mutation survivors recorded in
  `TENANCY_PLAN.md` are still survivors.
- **`crud/decorators/security`.** `tenancyrow.Policy` composes into
  `security.Policy[M,ID]` (`crud/decorators/security/security.go`, 1102 lines). I
  read none of it. Whether `Immutable`, `Scope`, `RelationScopes` and `Inspect`
  duplicate decisions *across* that boundary is unexamined, and it is the most
  likely place for a DRY finding I have missed.
- **`docs/`.** I confirmed that `docs/ai/decisions/` is the repository's home for
  rationale and that `utils/vvdb` has a redaction precedent, but I did **not**
  read `docs/modules/*/tenancy.md` (359 + 364 lines) or `FL-033`, so I cannot say
  whether the 152 lines of code comments are *literal* duplicates of doc text or
  merely the same decisions restated. GAP-7 claims the latter, which is what I
  verified.
- **The integration suite.** `test/integration/tenancy_test.go` needs Docker and
  I did not run it. The two functions it holds were not read.
- **Whether the mutation campaign running against this tree changes any finding.**
  I anchored every citation to a file hash and re-ran the suite on a verified
  pristine tree, but if that campaign is mid-edit rather than mid-run, some of the
  code I read may be about to change.
- **`%#v` behaviour under Go versions other than the one installed here**
  (`go 1.26.6`). `fmt.handleMethods`'s ordering of `Formatter` before `GoStringer`
  is long-standing but is not a documented guarantee, and GAP-3's finding that
  `Reference` survives `%#v` rests on it.
- **`Purpose`.** It deliberately returns its raw value from `String()`
  (`grant.go:32`), unlike every other value object here. I treated that as
  intended — a purpose is a label, not a tenant identifier — but did not confirm
  it against a decision doc.
