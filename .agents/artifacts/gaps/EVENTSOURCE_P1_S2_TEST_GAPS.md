# EVENTSOURCE_P1 — S2 (the declaration seam: codec, chain, aggregate, fact, change, fold) — TEST GAPS

## Round 1 — econv test reviewer (clean context) — 2026-09-07

**What was graded.** `event/{declaration,seal,fold,roundtrip,upcast,crossings}_test.go` and the two
S2 fuzz targets in `event/fuzz_test.go` — twelve tests, 56 subtests, three benchmarks, two fuzz
targets, 2 179 test lines and three `testdata/crossings` fixture packages — against
`event/{codec,encodable,chain,aggregate,fact,change,seal}.go` (1 021 lines), the plan's §S2,
§Contracts, §Coverage matrix and §Carried gaps C1/C9/C10,
`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md`
§UC-001/002/003/004/005/012/014/015/016/021/024/026/041/042/050/051/052/056/063/064/065/067/068 and
§INV-004/005/010/017/020/021/023/033/042/044,
`.agents/artifacts/gaps/EVENTSOURCE_P1_S2_GAPS.md` rounds 1–3, `CLAUDE.md`, and
`~/.claude/skills/econv/references/{gaps,universality,architecture,building-blocks,data-integrity,microkernel,restrictions,readability}.md`.

**Runs, all in this worktree.**

```
go test -race -count=1 ./event/                        ok  1.237s  (cold cache: real 1.46s)
go test -race -count=3 ./event/                        ok  1.659s
go test -race -count=5 ./event/                        ok  2.085s
go test -race -count=1 -shuffle=on ./event/  (x3)      ok  1.249 / 1.243 / 1.242s
go vet ./event/                                        EXIT=0
gofmt -l event                                         silent
go test -list '^(…the twelve S2 names…)$'   | grep -c  12
go test -list '^(…the two S2 fuzz names…)$' | grep -c   2
go test -v -run '^(…the twelve…)$' | grep -c '    --- PASS'   56
```

No flake, no order dependence, no leftover file or temp module: `diff -r` against a
pre-campaign copy of `event/` (sources, tests **and** `testdata/`) reports the tree
byte-identical after the whole campaign. The plan's phase-5 checkpoint numbers — twelve tests,
two fuzz targets, 56 subtests — reproduce exactly, so nothing here is a claim of coverage that
was not run.

**Where the section is genuinely strong, said first because it changes what the findings mean.**
The codec walk is the best-tested thing in this tree: twenty-two deliberate breakages of
`event/encodable.go` — every refusal arm, both bounds, the addressability rule at four
positions, the visited set's key, the tag parser, the embedded-promotion rule — and all twenty-two
died, most of them with a message that names the repair. The freeze, the per-fold clone,
`sameValue`'s four exceptions, the seal (including its AST/comment cross-check), the panic policy
for all three codec methods and the upcaster/fold asymmetry are all pinned by cases that fail on
the obvious mutation. Failure messages are the best in the repository: they state the consequence
("every load of that stream then replays a zero value with no refusal at any door") rather than
`got != want`. Controls sit beside almost every refusal.

The findings below are what the campaign found on the other side of that: **one whole extension
point whose failure path is asserted nowhere, three shapes of the ordinary Go embedding that the
tables never reach, one arm of C1's proxy that can be deleted outright, and two assertions that fit
exactly one sample.**

---

### GAP-T20 [critical][immediate] The shipped codec's decode-failure path is asserted by nothing — `event.JSON` can swallow every `json.Unmarshal` error and the suite plus 3.9 M fuzz executions stay green

- **Where:** `event/codec.go:57-61` (`jsonCodec.Decode`, the code no test constrains);
  `event/fuzz_test.go:109-127` (`FuzzAStoredPayloadIsFoldedOrRefused`'s body — the coverage
  matrix's S2-side proof for §UC-015 and §UC-014); `event/fold_test.go:186-200` (the only
  decode-refusal case in the section, driven through the test's own `refusingDecode` fixture and
  never through `event.JSON`)
- **What:** replace `err := json.Unmarshal(payload, &value); return value, err` with
  `_ = json.Unmarshal(payload, &value); return value, nil` and the entire package is green:

  ```
  $ go test -race -count=1 ./event/                 ok  github.com/frostgrove/vv/event  1.245s
  $ go test -race -run FuzzAStoredPayloadIsFoldedOrRefused -v ./event/   PASS (all 16 seeds)
  $ go test -run XXX -fuzz FuzzAStoredPayloadIsFoldedOrRefused -fuzztime 20s ./event/
    3 898 221 execs, 48 new interesting, PASS
  ```

  The fuzz target is named *…IsFoldedOrRefused* and its body is a disjunction: `if err != nil`
  → assert the class; `else` → assert determinism. A codec that refuses **nothing** takes the
  second branch on every input and satisfies it, because a zero value folds deterministically.
  The target therefore cannot fail on the property its name states. Nothing else in the tree
  drives `event.JSON`'s `Decode` with bytes it cannot read: the sixteen malformed seeds
  (`[1,2,3]`, `{"Minor":"250"}`, `"\xff\xfe"`, the empty payload, 200 open brackets) are all
  routed through a decoder whose error the mutation deleted, and the one test that does assert
  `ErrPayload` from a decode (`fold_test.go:186-200`) uses `refusingDecode`, a fixture that
  returns its own error unconditionally.
- **Why this severity:** this is the framework's default codec and the one every application
  starts with. With the error dropped, a truncated write, a hand-edited row, a payload written by
  an older deploy's shape or any corruption at all decodes to **the zero value of the reader
  type**, is folded, and reaches the caller as an ordinary state with `err == nil`. That is
  precisely §UC-015's refusal (`ErrPayload`, history class), §INV-006's "no partially rehydrated
  state" and §UC-009's "a fresh stream is indistinguishable from a lost one" collapsing into a
  single silent data-corruption path — the failure mode the whole section exists to prevent. The
  suite does not merely fail to catch a mutation; it has no assertion of the shipped codec's only
  failure mode at all.
- **Why this timing:** S4's `Load` and S5's `TestEveryHistoryClassRefusalIsRaisedByTheThingItNames`
  and `store failure classification` are all planned to *surface* an `ErrPayload` that S2 is
  responsible for *producing*. If S2's producer is unasserted, every one of those downstream cases
  will be written against a fixture codec and the shipped one will never be exercised.
- **Close criteria:**
  - [ ] A subtest of `TestFoldRefusesAnotherInstance` (or a new one) folds a change whose recorded
        bytes `event.JSON` cannot read and asserts `errors.Is(err, ErrPayload)` and that the state
        came back as it went in — red when `jsonCodec.Decode` returns `nil` for its error.
  - [ ] A direct assertion that `JSON[creditedV2]().Decode([]byte("[1,2,3]"))` returns a non-nil
        error, with a well-formed payload beside it as the control.
  - [ ] `FuzzAStoredPayloadIsFoldedOrRefused` gains a **positive** arm: a small table of payloads
        the declaration *must* refuse, asserted inside the fuzz body or as a seed-only subtest, so
        "folded or refused" stops being satisfiable by "always folded".
- **Status:** closed 2026-09-07 — `TestFoldRefusesAnotherInstance` gains *recorded bytes the shipped codec cannot read are refused by the fold*: four payloads (`[1,2,3]`, `{"Minor":"250"}`, `\xff\xfe`, none at all) are put on a decided change and folded, each asserted `ErrPayload` with the state coming back at 7, with the same change over its own bytes as the control; and the direct pair, `JSON[creditedV2]().Decode([]byte("[1,2,3]"))` non-nil beside a well-formed payload. `FuzzAStoredPayloadIsFoldedOrRefused` gains the positive arm in its setup — the same four payloads driven through `credited.apply` before `f.Fuzz` — so the target cannot pass on a codec that refuses nothing. Mutation: `_ = json.Unmarshal(payload, &value); return value, nil` KILLS both (`--- FAIL: TestFoldRefusesAnotherInstance` and `--- FAIL: FuzzAStoredPayloadIsFoldedOrRefused`), where before it left the whole package green.

---

### GAP-T21 [critical][immediate] The declaration tables have 49 rows and not one is the ordinary Go embedding — three shapes the shipped codec accepts silently delete data from every fact ever written

- **Where:** `event/declaration_test.go:499-582` (`TestADeclaration`/"the shipped codec answers for
  the whole type graph" — 26 refused rows, 23 accepted rows), `event/declaration_test.go:612-637`
  (the diagnosis table); the code they gate is `event/encodable.go:82` and `188-233`
- **What:** measured from a throwaway out-of-tree module (`/tmp/eprobe`) with a `replace` onto this
  checkout — nothing in the repository was edited:

  ```
  type Line    struct{ Money;      SKU  string }   // Money declares the JSON pair
      CanEncode=<nil>  Encode=500                          Decode={Money:{Cents:500} SKU:}
  type Stamped struct{ time.Time;  Note string }   // the textbook embedding
      CanEncode=<nil>  Encode="2026-01-02T03:04:05Z"       Decode=2026-01-02 03:04:05 +0000 UTC
  type Held    struct{ C Code }                    // Code has UnmarshalText on the VALUE receiver
      CanEncode=<nil>  Encode={"C":"usd"}                  Decode={C:}
  type Outer   struct{ *inner;     Note string }   // embedded pointer to an unexported struct
      CanEncode=<nil>  Encode={"N":7,"Note":"note"}        Decode ERROR, forever
  ```

  Every field declared beside the embedded one is written by nobody and read back by nobody, with
  `nil` at every door; the fourth encodes cleanly and can never be decoded — a poison pill in the
  log. The tables reach a *value-receiver* marshaller (`stamped`, `cents`, `region`), a
  *pointer-receiver* pair (`guarded`, `posted`, `priced`), a promoted **field name** collision
  (`twice`, `placed`, `shadowed`) and an embedded pointer to a **struct with exported fields**
  (`pointed`) — but never a promoted **method set**, never a value-receiver `Unmarshal*`, and never
  an embedded pointer to an **unexported** type. `TestADeclaration` is the only gate S2 puts in
  front of `main`.
- **Why this severity:** `struct{ Money; SKU string }` and `struct{ time.Time; Note string }` are
  not exotic; they are how Go composes value types, and `Code` with `UnmarshalText` on the value
  receiver is the mistake `go vet` does not report. Each writes a fact log that permanently omits
  the fields the application believes it recorded, and §INV-023 ("encodability is the codec's
  question, answered before `main`") and §UC-056's panic are what the tables are supposed to
  enforce. Note that `Fact.RoundTrip` *does* catch `Line` and `Stamped` through its fidelity
  comparison — but only for an application that opts into §UC-042's helper; `CanEncode`, the check
  that runs unconditionally at declaration, does not.
- **Why this timing:** it is edge coverage of a public contract other sections depend on, and it is
  the third route of the same root cause the section already repaired twice (GAP-178, GAP-179): the
  tables grew by eleven rows and never gained the ordinary embedding. The implementation half is
  **GAP-185/GAP-186/GAP-187** in `EVENTSOURCE_P1_S2_GAPS.md` round 3, all `[critical][immediate]`
  and all still `open` with no disposition. This row is the **test half** and does not duplicate
  them: it stays open after the walk is repaired, because a repair with no row is a repair one
  refactor from being deleted.
- **Close criteria:**
  - [ ] Three refused rows — a struct embedding a type with a `MarshalJSON`/`UnmarshalJSON` pair
        beside a field of its own; the same with a text pair (`time.Time` is the fixture); a struct
        holding a type whose `UnmarshalText` is on the value receiver — each red on today's
        `event/encodable.go`.
  - [ ] One refused row for an embedded pointer to an unexported struct type.
  - [ ] An accepting control beside each: the same shape with the embedded type tagged
        (`json:"money"`), a `time.Time` held in a **named** field, a pointer-receiver
        `UnmarshalText`, an embedded pointer to an exported struct (`pointed` already serves).
  - [ ] A diagnosis row per refusal, since all four share `ErrCodecType` with the three arms
        already in the table.
- **Status:** closed 2026-09-07, together with its implementation half. GAP-185, GAP-186 and GAP-187 are closed in `EVENTSOURCE_P1_S2_GAPS.md`; the walk goes from five refusals to eight and the tables from 26/23 rows to **35 refused and 31 accepted**, with the diagnosis table from 11 rows to **17**. New refused rows: the promoted JSON pair (`lined`), the textbook `time.Time` embedding (`noted`), the same shape **tagged**, the value-receiver `UnmarshalJSON` as a reader type and as a field (`sloppy`), the value-receiver `UnmarshalText` map key (`map[casual]int`), and the embedded pointer to an unexported struct (`pointed`, which moves off the accepted table). New accepting controls: `itemised` (embedded, nothing beside it), `invoiced` (named field), `dueAt` (`time.Time` in a named field), `money` and `struct{ Amount money }` (the pointer-receiver spelling), `struct{ *Stream }` (embedded pointer to an **exported** struct), `holdingDated` (a named `*dated`), and `ring` kept as *a type embedding a pointer to itself, which renders no promoted name*. Mutations, each applied alone against `-run TestADeclaration`: the promoted-marshaller arm deleted **KILLED**; `readsAs` restored to `||` **KILLED**; `objectKey`'s bare `PointerTo(key).Implements` restored **KILLED**; the embedded-pointer refusal deleted **KILLED**; and two over-broad-repair controls — the embedded-pointer refusal raised where the pointer is *seen* **KILLED** (the `ring` row), `besideIt` counting the embedded field itself **KILLED** (the `itemised` row).

  **One disagreement, argued.** This finding's third close criterion asks for *"the same shape with the embedded type tagged (`json:"money"`)"* as an **accepting control**. That is wrong: a JSON tag does not undo Go's method promotion. Measured out of tree, `struct{ Money \`json:"money"\`; SKU string }` still writes `500` and still reads `SKU` back as `""`. It is therefore on the **refused** table, and the accepting control is a **named** field — which is also what the refusal's message tells the caller to write.

---

### GAP-T22 [high][immediate] The second arm of C1's non-aliasing proxy can be deleted outright with the suite green — the whole class of codec it exists for is unexercised

- **Where:** `event/fact.go:159-181` (`reusesItsBuffer`), specifically the disturbance arm at
  `168-180`; the two clones that live only inside it, `event/fact.go:137` (`zero = bytes.Clone(zero)`)
  and `event/fact.go:141` (`selfDecode(bytes.Clone(written))`); the test that is supposed to hold
  them, `event/roundtrip_test.go:236-411`
- **What:** three survivors, each applied alone and re-run under `-race`:

  | Mutation | Verdict |
  |---|---|
  | `reusesItsBuffer`'s entire second arm replaced by `return false, nil` | **SURVIVED** |
  | `zero = bytes.Clone(zero)` deleted (`fact.go:137`) | **SURVIVED** |
  | `read.selfDecode(bytes.Clone(written))` → `read.selfDecode(written)` (`fact.go:141`) | **SURVIVED** |

  All nine subtests of `TestACodecThatDecodesIntoAReusedBufferIsCaught` are decided by the *first*
  arm — `sharesMemory` (`fact.go:189-223`), a pointer comparison over slices and maps reached
  through **exported** fields. The plan's own phase-5 note says why: the map fixture that the
  disturbance arm used to catch was rewritten into "the ordinary shape", which the pointer arm
  catches, and no fixture replaced it.
- **Why this severity:** the disturbance arm is the only thing in the proxy that can see a decoded
  value aliasing the codec's reused buffer through something `sharesMemory` does not walk — a
  **string** (a zero-copy decoder built on `unsafe.String` over its scratch buffer, which is what a
  high-performance binary codec is) or an **unexported field** (`sharesMemory` skips them by
  design, `fact.go:217`). For that class, C1/GAP-100's obligation — "a decoded value must not alias
  memory its codec will reuse" — and §UC-042's runnable proxy are unenforced, and the failure it
  prevents is the one §INV-021 says four consecutive rounds produced: the application's state is
  rewritten by the *next* payload the codec reads, with no error anywhere. §INV-021's own text
  calls the aliasing codec "ordinary … and not a broken one", so this is not a hypothetical
  implementer.
- **Why this timing:** C1 is a carried gap whose only proof is this test, and S5's
  `eventtest.RoundTrip` is planned to be a proxy for the same obligation across every store. A
  proxy with a dead arm exported into the conformance suite is a promise the suite cannot keep.
- **Close criteria:**
  - [ ] A fixture codec whose decoded value reaches its reused buffer through a field
        `sharesMemory` does not compare — the cheapest is an **unexported** `[]byte` field over a
        scratch array that is filled and never cleared; no `unsafe` needed — asserted `ErrPayload`.
  - [ ] An allocating codec of the same wire format beside it as the control, asserted to pass.
  - [ ] Deleting `event/fact.go:168-180` turns the new case red, and deleting either clone at
        `fact.go:137` / `fact.go:141` turns it red or makes it accuse the control.
- **Status:** closed 2026-09-07 — `roundtrip_test.go` gains `sealedCodec`: one buffer to encode into, one to decode into, and the decoded value reaches the second through an **unexported** `[]byte` field, which is the class `sharesMemory` skips by design (`fact.go:217`) and what a zero-copy reader over a private slice or a `string` is. Its control is `sealingCodec`, the same wire format and the same reused encode buffer, decoding into memory of its own. It also gains *a codec that consumes the buffer it was handed is handed one of its own* over `unescapingCodec`, a reader that unescapes in place — which is the property the two payload clones exist for and which nothing in the section stated before. Mutations, each alone: `reusesItsBuffer`'s whole second arm → `return false, nil` **KILLED**; `zero = bytes.Clone(zero)` deleted **KILLED**, and in the shape the criterion names — it accuses the **control** (*"the same wire format over a codec that allocates per decode was refused"*); `read.selfDecode(bytes.Clone(written))` → `read.selfDecode(written)` at `fact.go:141` **KILLED**; and the same at `fact.go:160` **KILLED**.

---

### GAP-T23 [high][immediate] `Aggregate.Family()` is pinned by one literal comparison against the fixture's own family — an implementation that ignores its receiver and answers `"accounts.account"` survives the whole suite

- **Where:** `event/declaration_test.go:329-335` (the only two `Family()` assertions in the
  section — the concrete method and the `Declaration` seam, both against the string literal
  `"accounts.account"` that `declareAccounts` declared at `event/declaration_test.go:298`);
  `event/aggregate.go:46-49`
- **What:** two mutations, applied one at a time:

  | `Family()` returns | Verdict |
  |---|---|
  | `"accounts.account"` (the fixture's own family) | **SURVIVED** — full suite green under `-race` |
  | `"zzz"` | KILLED — `TestADeclaration`/"a revision is the chain's position…" |

  Nothing else in the section reads `Family()`. `TestFoldRefusesAnotherInstance`/"two aggregates
  over one state type are told apart by the family" declares `savings.account` beside
  `accounts.account` but asserts only `ErrWrongStream`, which is decided by `locate`'s
  `this.family` and not by the accessor; `TestAChangeRetainsNoApplicationValue` asserts
  `change.Stream()` against `Stream{Family: "notes.note", …}`, again through `locate`.
- **Why this severity:** `universality.md` — the assertion fits exactly one sample, so it cannot
  distinguish "reports the family it was declared with" from "reports the family the example was
  declared with", and per `gaps.md` a fit-to-one-example finding is never below
  `[high][immediate]`. The concrete consequence is not cosmetic: `Declaration.Family()` is, by
  `event/aggregate.go:103-110`'s own comment, what S4's `Binding` keys its bound-family set by and
  what §INV-039 ("one family names one aggregate, forever") is enforced through. A `Family()` that
  answers a constant makes two aggregates one family — "two aggregates over one history, folding
  each other's facts, with no error at any point", which §UC-002 calls the dangerous case — and
  this suite is the only thing standing between that and S4.
- **Why this timing:** §UC-059 and §INV-039 are S4/S5 work that will be written on top of this
  accessor. Fixing the assertion after `Binding` is built means re-deriving what `Binding` was
  supposed to be reading.
- **Close criteria:**
  - [ ] The revision/name/family subtest becomes a two-row table over **two** declarations with
        different families, each asserting `Family()` and `Declaration.Family()` against the string
        that declaration was constructed from — not against a literal repeated in the assertion.
  - [ ] A `Family()` returning any fixed string, including `"accounts.account"`, turns it red.
- **Status:** closed 2026-09-07 — the subtest is now a two-row loop over `[]string{"accounts.account", "savings.account"}`, building a declaration per row and asserting `Family()` and `Declaration.Family()` against the string that row was constructed from. No literal is repeated in an assertion. Mutation: `Family()` returning the constant `"accounts.account"` **KILLED** (`--- FAIL: TestADeclaration`), where it previously survived the whole suite.

---

### GAP-T24 [high][immediate] The crossings test accepts *any* build failure as proof, so a negative fixture that rots into "does not compile for an unrelated reason" keeps §UC-026 and §INV-020's compile half green forever

- **Where:** `event/crossings_test.go:72-77` — `response, err := buildCrossing(...)` followed by
  `if err == nil { t.Fatalf(...) }` is the whole assertion; `response` is used only in the failure
  message. Fixtures `event/testdata/crossings/{change,identity}/`
- **What:** adding one unrelated line — `var unrelated = undefinedSymbolThatDoesNotExist` — to
  `change/change.go` and to `identity/identity.go` leaves `TestTheCrossingsThatMustNotCompile`
  **green** in both cases. The `control` fixture, which is the section's answer to "the refusal is
  not just nothing compiles", proves only that *something* builds; it says nothing about *why* the
  other two do not. Today the two fail for the right reason —

  ```
  change/change.go:19:36:   cannot use alphaTicked.New("A-17", Ticked{…}) (value of struct type
                            event.Change[Alpha]) as event.Change[Beta] value in argument to betas.Fold
  identity/identity.go:19:25: cannot use BetaID{…} (value of struct type BetaID) as AlphaID value
                            in argument to alphaTicked.New
  ```

  — and nothing checks that they still will.
- **Why this severity:** this test is the sole proof of §UC-026 and of §INV-020's compile-time
  half, and one of two legs of §INV-015 in the matrix. The realistic regression is a rename or a
  signature change in `event/` during S4–S6 that breaks the negative fixture for an arithmetic
  reason (a renamed helper, an extra parameter) while leaving the crossing itself expressible: the
  build still fails, the test still passes, and the compile-time guard has silently become
  untested. It is `restrictions.md`'s "a test that asserts an exception was raised without
  asserting which one", in compiler form, on the one property the framework claims Go can enforce
  for it.
- **Why this timing:** S4 adds `Bind`, `At[S]` and `Repo.Append` to the same fixture set (§UC-027,
  §INV-015 name `testdata/crossings` again). Every fixture added on top of an assertion that
  cannot tell a crossing from a typo inherits the same hole.
- **Close criteria:**
  - [ ] Each negative fixture's row carries the substring its compiler error must contain
        (`as event.Change[Beta] value in argument to`, `as AlphaID value in argument to`), asserted
        against `response`.
  - [ ] Injecting an unrelated undefined symbol into either fixture turns the test red.
- **Status:** closed 2026-09-07 — each negative fixture's row carries the substring its compiler error must contain (`as event.Change[Beta] value in argument to betas.Fold`, `as AlphaID value in argument to alphaTicked.New`), asserted against `response`.

  **One disagreement, argued.** The finding's second close criterion — *"injecting an unrelated undefined symbol into either fixture turns the test red"* — is **not** met, and should not be: measured, `go build` reports **both** errors, so the crossing is still refused by the type system and the fixture still proves exactly what the test claims. The regression the finding actually describes is *the crossing stops being expressible while the build still fails*, and that is now caught. Both mutations verified: the change fixture rewritten to fold through its own aggregate **plus** an undefined symbol **KILLED**; the identity fixture rewritten to mint with `AlphaID` **plus** an undefined symbol **KILLED**. Both survived before this change.

---

### GAP-T25 [medium][immediate] `Fact.RoundTrip`'s six refusal messages are matched by sentinel alone, and two mutations that make it name the wrong reason survive

- **Where:** `event/roundtrip_test.go` — every assertion in all nine subtests is
  `errors.Is(err, ErrPayload)` or `errors.Is(err, ErrSample)`; `err.Error()` is read nowhere in the
  file. `event/fact.go:121-157` produces six distinct messages across those two sentinels
- **What:** two survivors:

  | Mutation | Where | What the caller is then told |
  |---|---|---|
  | the `read.accepts(sample)` guard deleted | `event/fact.go:125-127` | a sample of the **wrong reader type** is silently coerced to that revision's zero value by `selfEncode`'s type assertion (`chain.go:69`) and reported as *"encodes its sample exactly as its own zero value"* — the caller is sent to fix a sample that is fine and never told the type is wrong. `roundtrip_test.go:404` still passes, because it asserts `ErrSample` and both paths are `ErrSample` |
  | `carry`'s `typeName: previous.typeName` → `typeNameOf[B]()` | `event/chain.go:77` | every retained revision's refusal names the **current** reader type, so the "revision 1 reads `creditedV1`" diagnosis reads "revision 1 reads `creditedV2`" |

  Both also survive as `linkOf`'s `accepts: func(any) bool { return true }`.
- **Why this severity:** §UC-042's **Observed** is "a failure that names the revision and the field
  rather than printing two structs" — the message *is* the deliverable of this helper, and it is
  the only artefact its user sees. The section learned exactly this lesson in round 1 ("a table
  asserting the sentinel alone would have passed with the wrong repair printed", which is why
  `TestADeclaration:612-637` exists) and applied it only to `TestADeclaration`.
- **Why this timing:** S5 exports this helper as `eventtest.RoundTrip`, at which point its messages
  are a published contract for every store implementer.
- **Close criteria:**
  - [ ] A diagnosis table beside the sentinel assertions, one row per distinct `RoundTrip` message
        (wrong reader type, wrong sample count, zero-value sample, aliasing verdict, fidelity
        verdict), asserting the substring that names the repair.
  - [ ] A row asserting the retained revision's own type name appears in the wrong-type refusal, so
        `chain.go:77` cannot be flattened.
- **Status:** closed 2026-09-07 — `TestACodecThatDecodesIntoAReusedBufferIsCaught` gains *a refusal names the repair, because five of them share two sentinels*: one row per distinct `RoundTrip` message (wrong reader type, wrong sample count, zero-value sample, aliasing verdict, fidelity verdict), each asserting the substring that names the repair. The wrong-reader-type row asserts `revision 1 of "accounts.credited" reads event.creditedV1 and the sample is a event.creditedV2` — the retained revision's **own** type name. Mutations: `carry` taking `typeNameOf[B]()` **KILLED**; `roundTrip` dropping the `read.accepts(sample)` guard **KILLED**; `linkOf`'s `accepts` answering `true` for every sample **KILLED**.

---

### GAP-T26 [medium][immediate] Every multi-revision chain in the suite declares `JSON[E]()` at its *current* revision, so a `Then` that discards the caller's codec and substitutes the shipped one is invisible

- **Where:** `event/chain.go:61`; the fixtures `event/declaration_test.go:303`, `:341`, `:415` and
  `event/roundtrip_test.go:378`, `:394`
- **What:** replacing `Chain[B]{codec: codec, …}` with `Chain[B]{codec: JSON[B](), …}` — a `Then`
  that ignores the codec it was handed — **SURVIVED** the full suite under `-race`. It survives
  because there is no two-revision chain anywhere in the section whose *current* codec is anything
  other than `JSON`: `decimalCodec` and `scratchCodec` are only ever used at revision 1
  (`roundtrip_test.go:394`, `:378`), where `From`'s equivalent mutation is caught. The sharper
  mutation — the current link reading through the *previous* codec — is caught, so the reader chain
  is fine; what is unpinned is the codec `Fact.New` writes with.
- **Why this severity:** §UC-001's stated benefit is "because each revision carries its own codec, a
  revision bump may change the encoding without a data migration", and §INV-023 asks the question
  "once per retained revision". Both are asserted only where the answer happens to be `JSON`. An
  application that bumps to revision 2 with a compact binary codec would silently keep writing JSON,
  and the round trip would pass, because it would read back what it wrote.
- **Why this timing:** it is a hole in a fixture set, not in a contract, but the fixtures are what
  S5's conformance suite will be modelled on.
- **Close criteria:**
  - [ ] One two-revision chain whose **current** revision carries a non-JSON fixture codec
        (`decimalCodec` at revision 2 over `JSON` at revision 1 is the cheapest), asserting the
        bytes `Fact.New` freezes are that codec's format.
  - [ ] `Then` substituting `JSON[B]()` for the codec it was given turns it red.
- **Status:** closed 2026-09-07 — `TestADeclaration` gains *the current revision writes with the codec that revision declared*: `Then(From(JSON[creditedV1]()), decimalCodec{}, …)`, a two-revision chain whose **current** revision carries a non-JSON codec, asserting the frozen payload is `250` and that the fact folds to 250. Mutation: `Then` returning `Chain[B]{codec: JSON[B](), …}` **KILLED** — the freeze becomes `{"Minor":250}`.

---

### GAP-T27 [medium][immediate] §UC-041's per-cause return contract is asserted for causes 2, 3 and 4 and not for cause 1, and the order of causes 2 and 3 is asserted by no list where both apply

- **Where:** `event/aggregate.go:70-93` (`Fold`, whose contract comment enumerates the four causes
  and what each returns); `event/fold_test.go:87-99` and `:132-151`, the two cause-1 subtests, both
  of which discard the returned state with `_, err :=`
- **What:** two survivors:

  | Mutation | Verdict | The clause it breaks |
  |---|---|---|
  | cause 1 returns the **zero** state instead of the state it was given | **SURVIVED** | §UC-041 "Causes 1, 2 and 3 run before any fold does, so they return the state that went in, untouched" |
  | the carried-refusal loop moved **ahead of** the crossing loop | **SURVIVED** | §UC-041 "in the order `Fold` performs them, which is `Append`'s order and which is what decides the sentinel when two apply" |

  Causes 2 and 4 both have `state.Balance != 7` / `state.Balance != 10` assertions
  (`fold_test.go:78-80`, `:197-199`); cause 1 has none. The ordering survives because no test folds
  a list holding **both** a change decided for another stream **and** a change carrying its own
  refusal — the only shape where the two loops disagree.
- **Why this severity:** §UC-041 states the return value per cause precisely because a caller has to
  know whether the value it still holds was touched, and it is the one place where the framework's
  own loop writes the caller's memory (§INV-021's inbound table, third row). The ordering clause
  exists because reporting a request-class fault in the wiring class was GAP-81 and GAP-173, twice.
- **Why this timing:** S4's `Append` is specified to perform the identical comparisons in the
  identical order (§UC-064), and its tests will be written against whatever S2 established.
- **Close criteria:**
  - [ ] Both cause-1 subtests keep the returned state and assert it equals the state they passed in;
        at least one uses a reference-kind state so "untouched" is observable.
  - [ ] One subtest folds `[]Change{anotherInstances, carryingItsOwnRefusal}` and asserts
        `ErrWrongStream`, with the reversed list asserted too so the order is pinned in both
        directions.
- **Status:** closed 2026-09-07 — both cause-1 subtests keep the state they were given and assert it came back untouched; the zero-identity one uses a reference-kind state (`account{Balance: 7, Applied: map[string]bool{"seed": true}, Tags: []string{"acme"}}`) so *untouched* is observable through the map and the slice. `TestFoldRefusesAnotherInstance` gains *a crossing and a carried refusal in one list answer in the order the append performs them*, folding `{theirs, unencodable}` and `{unencodable, theirs}` and asserting `ErrWrongStream` for both. Mutations: `Fold` returning the zero state on cause 1 **KILLED**; the carried-refusal loop moved ahead of the crossing loop **KILLED**.

---

### GAP-T28 [medium][deferred] §UC-052's own named control — a state holding `time.Time` — is absent, and the case covers three of the seven shapes [SPEC] enumerates

- **Where:** `event/declaration_test.go:456-491`
- **What:** the subtest declares a nested struct reaching `[]string`, `[2]creditedV2` and a map; a
  state type that **is** a map; and a struct holding `json.RawMessage`. §UC-052 names seven shapes
  and its **Control** paragraph is explicit: "The declaration of a `time.Time`-holding state in the
  same test is accepted with no extra argument, so the case cannot pass by refusing everything" —
  because the foreign value type is the shape whose deleted copier walk **refused**
  `struct{ PlacedAt time.Time; Total int64 }`, "the most ordinary aggregate state there is"
  (§UC-052, §D.12). That shape is not in the table.
- **Why this severity:** the property holds today by construction (the kernel walks no state type),
  so nothing is broken; what is missing is the regression barrier against the mechanism §UC-052
  deleted being reintroduced — which is the reason the control was written into the use case.
- **Why this timing:** no contract another section depends on; extra coverage.
- **Close criteria:**
  - [ ] A `struct{ PlacedAt time.Time; Total int64 }` state and an `[8]Item`-of-map state added to
        the table, both asserted to declare with no extra argument.
- **Status:** deferred, carried 2026-09-07 into `EVENTSOURCE_P1_PLAN.md` `## Debt` under *From the S2 test review, round 1*, with the section that owns the assertion named.

---

### GAP-T29 [low][immediate] `Aggregate.Key` may hand back the raw, illegal rendering beside its refusal and no test notices

- **Where:** `event/aggregate.go:51-55`; `event/declaration_test.go:431-454`, which is the only test
  that calls `Key` and never calls it with an identity that renders an illegal key
- **What:** making `Key` return `this.key(id)` rather than `stream.Key` on the refusal path —
  handing the caller the over-long or control-character-bearing string the kernel just refused —
  **SURVIVED** the full suite. `Key`'s two-value shape was added in S2 specifically so
  `eventtest.Keys` could obtain one; the first reader of the value-beside-the-error is a helper that
  does not exist yet.
- **Why this severity:** low — a caller writing `key, _ := agg.Key(id)` is already ignoring a
  refusal. But §INV-025's spirit (a refusal carries a classification, not data) and §C10.1's key
  proxy both read this return.
- **Why this timing:** the contract of a public two-value method that S5's conformance helper is
  specified to call; cheap now, a behaviour to preserve later.
- **Close criteria:**
  - [ ] The composite-identity subtest asserts `Key` answers the **empty** key together with
        `ErrKey` for a blank rendering and for one over `MaxKeyBytes`, with a legal identity beside
        it as the control.
- **Status:** closed 2026-09-07 — the composite-identity subtest gains a three-row table over an aggregate whose mapper is a bare conversion: a blank rendering, one carrying a newline and one over `MaxKeyBytes`, each asserted `ErrKey` **and** an empty key, with the legal composite identity at the head of the subtest as the control. Mutation: `Key` returning `this.key(id)` on the refusal path **KILLED** (the newline and over-cap rows; the blank row cannot tell the two apart, since a blank rendering *is* the empty key).

---

### GAP-T30 [low][deferred] `Fold` with an empty change list is asserted by nothing, so a short-circuit that skips the key check survives

- **Where:** `event/aggregate.go:70-75`; `event/seal_test.go:24-26` is the only caller of
  `Fold(id, state)` with no changes and it discards both results
- **What:** inserting `if len(changes) == 0 { return state, nil }` before `locate` **SURVIVED**.
  §UC-041's cause 1 states no exception for an empty list, and the mirror property at the other door
  is a planned S4 test (`TestAnEmptyAppendChecksTheKeyAndNothingElse`, §INV-031), so the two doors
  would disagree.
- **Why this timing:** an edge with no contract impact inside S2.
- **Close criteria:**
  - [ ] `Fold(zeroID, state)` with no changes asserts `ErrKey` and that the state came back
        untouched; `Fold(legalID, state)` with no changes asserts `nil` and the same state.
- **Status:** deferred, carried 2026-09-07 into `EVENTSOURCE_P1_PLAN.md` `## Debt` under *From the S2 test review, round 1*, with the section that owns the assertion named.

---

### GAP-T31 [low][deferred] The round-trip fidelity cases read the real clock

- **Where:** `event/roundtrip_test.go:308`, `:321`, `:322` — three `time.Now()` calls inside
  `TestACodecThatDecodesIntoAReusedBufferIsCaught`
- **What:** the monotonic-reading and local-zone cases are constructed from the wall clock. They are
  not flaky in practice (`time.Time.Equal` ignores both, and RFC3339Nano round-trips losslessly) and
  `-count=5 -shuffle=on` found nothing, but the property under test — "what an encoding drops by
  design is not what it lost" — needs a *fixed* instant with a monotonic reading and a non-UTC zone,
  which `time.Now()` supplies only incidentally and `time.Date(...)` states.
- **Why this timing:** cosmetic; the case passes and the risk is theoretical.
- **Close criteria:**
  - [ ] The two `stamp` samples are built from a fixed instant in a named non-UTC location, with the
        monotonic case constructed explicitly rather than borrowed from the clock.
- **Status:** deferred, carried 2026-09-07 into `EVENTSOURCE_P1_PLAN.md` `## Debt` under *From the S2 test review, round 1*, with the section that owns the assertion named.

---

### GAP-T32 [low][deferred] The concurrency test never crosses an upcaster, which C9's clause names among the callbacks called from every request goroutine

- **Where:** `event/seal_test.go:240-283`
- **What:** the eight workers mint and fold `declared.credited`, a two-revision fact — but a `Change`
  always carries the **current** revision, so every concurrent decode goes through
  `linkOf(JSON[creditedV2])` and no upcaster runs. `RoundTrip(opened{…})` is a one-revision chain.
  C9's contract sentence is "a codec, a fold, an upcaster, an identity mapper and a store's injected
  clock from every request goroutine concurrently", and four of the five are driven here.
- **Why this timing:** the S5 `concurrency` section is where C9 is checkpointed; this is the S2 half
  being one callback short.
- **Close criteria:**
  - [ ] One worker loop drives a retained revision through `Fact.RoundTrip` on a chain of two, or
        through the fact's applier at revision 1, so the upcaster runs on every goroutine.
- **Status:** deferred, carried 2026-09-07 into `EVENTSOURCE_P1_PLAN.md` `## Debt` under *From the S2 test review, round 1*, with the section that owns the assertion named.

---

### GAP-T33 [low][deferred] A marker fact is on the accepted table and can never pass `Fact.RoundTrip`; the suite pins one door of the contradiction and not the other

- **Where:** `event/declaration_test.go:549` (`{"a marker fact that carries no data at all",
  JSON[struct{}]().CanEncode()}`, asserted `nil`); `event/fact.go:138-140`
- **What:** measured out of tree — declaring a `struct{}` payload and calling `RoundTrip(Marker{})`
  answers
  `event: this sample cannot prove what a round trip claims for it: … revision 1 of "probe.marked"
  encodes its sample exactly as its own zero value`. So §UC-042's obligation ("a payload's revisions
  are round-tripped in a test") is unsatisfiable for the shape the declaration table explicitly
  blesses, and no test in the section drives that same shape through the second door. The
  implementation half is **GAP-190 `[medium][deferred]`**, open.
- **Why this timing:** deferred with GAP-190; the test row moves with whatever that decides.
- **Close criteria:**
  - [x] Once GAP-190 is decided, a case drives the accepted marker shape through `RoundTrip` and
        asserts the decided answer (a pass, or a refusal with a message that names the escape).
- **Status:** closed 2026-09-08 with GAP-190, in S2 rather than in S5 — the repair was one predicate in the kernel (`singleValued`, `event/comparison.go`) rather than a policy in the `*testing.T` wrapper. `TestACodecThatDecodesIntoAReusedBufferIsCaught/a fact whose whole content is that it happened round trips` drives the accepted `struct{}` shape through the second door and asserts the pass, with a payload-carrying fact given its own zero value as the control that still refuses. Struck from `EVENTSOURCE_P1_PLAN.md` `## Debt`. Mutation: `singleValued` dropped from the zero-value guard turns that subtest red.

---

## Coverage of S2's assigned matrix rows

Every §S2 `Covers` item, checked against a test that would fail on regression. **26 of 33 hold; the
seven below do not, or hold only in part.**

| Item | Verdict |
|---|---|
| UC-001, UC-003, UC-004, UC-005, UC-012, UC-016, UC-021, UC-024, UC-042, UC-050, UC-051, UC-056, UC-063, UC-064, UC-065, UC-067, UC-068 | held — each has at least one mutation that dies on it |
| INV-004, INV-005, INV-010, INV-017, INV-021 (1, 2), INV-023, INV-033, INV-042, INV-044 | held |
| **UC-015** (S2 half: "the codec refuses a malformed payload") | **not held** — GAP-T20. The matrix's S2 proof, `FuzzAStoredPayloadIsFoldedOrRefused`, passes on a codec that refuses nothing |
| **UC-014** (S2 half: "the chain refuses an unreadable revision") | held for the revision bound (`declaration_test.go:656-667` drives `apply` at 0, −1, 3), **not held** for the decode half, which shares GAP-T20's hole |
| **UC-041** | held for causes 2, 3, 4 and for the seal; **not held** for cause 1's return value and for the cause 2/3 ordering — GAP-T27 |
| **UC-052** | held for three of the seven shapes; the use case's own named control is absent — GAP-T28 |
| **UC-026 / INV-020** (compile half) | proved only by a test that accepts any build failure — GAP-T24 |
| **INV-023** | held at revision 1 and at revision 2 *for `JSON`*; the three shapes of GAP-T21 pass the gate, and GAP-T26 shows the current revision's codec is never a non-JSON one |
| **C1** (GAP-100) | held for the pointer-comparison arm; the second arm is deletable — GAP-T22 |
| **C9** | held for codec, mapper and fold; the upcaster is not driven concurrently — GAP-T32 |
| **C10.1** | held, and well: the AST walk plus the comment parse is the strongest single case in the section |

**No test in the section is without a UC or INV behind it.** Two read package internals —
`credited.apply` (`declaration_test.go:660`, `fuzz_test.go:110`) and `change.payload`
(`fold_test.go:345`, `fuzz_test.go:148`) — and both are justified in the plan: the revision bound is
unreachable from the exported surface until S4's `Load`, and a slice's capacity is not observable
from outside. `declared.aggregate.facts` (`seal_test.go:162`) is the third and is what makes the
accept-or-seal race assertion checkable. `chargeJSON` (`declaration_test.go:370`, `:378`, `:386`,
`:389`) is reached directly to build reflected type graphs no source can express — and the subtest
asserts the three constants against the literals its shapes were built from first
(`declaration_test.go:361-364`), which is S1's GAP-T15 lesson correctly applied.

**Doubles discipline is clean.** There is no mock in the section. Every double is a real
implementation of the project's own `Codec[V]` interface — `aliasCodec`, `scratchCodec`,
`prefixCodec`, `allocatingCodec`, `truncatingCodec`, `forgetfulCodec`, `emptyingCodec`,
`scratchMapCodec`, `floatCodec`, `roundingCodec`, `decimalCodec`, `refusingCodec`,
`panickingCodec`/`Credit`/`Encode`/`Decode`, `refusingDecode` — each written to break exactly one
clause of the codec contract, and eleven of the fourteen have a control of the same wire format
beside them. Nothing third-party is patched; nothing the project owns is mocked.

**Determinism and isolation.** No `t.Parallel`, no sleep, no network, no shared mutable fixture: every
subtest builds its own declaration through `declareAccounts` / `declareNotes` / `mintOpened`. The one
scheduling-dependent case, `TestTheSealRefusesALateFact`/"a fact declared as the first reader runs",
reaches **both** branches — instrumented over three runs it sealed in 184, 194 and 193 of 200 rounds,
so neither the accept nor the refuse arm is dead. `TestTheCrossingsThatMustNotCompile` shells out to
`go build` in a `t.TempDir` with `GOPROXY=off`, leaves nothing behind, and costs 0.12 s.

**Speed and layering.** The whole package is **1.24 s** under `-race` on a cold test cache, of which
the crossings build is 0.12 s and the 200-round seal race 0.03 s. Unit only; no database, no store.

---

## Mutation log — 114 breakages applied, 108 scored, 93 caught, 15 survived

Applied to the working tree one at a time with a scripted harness, `go test -count=1 ./event/`
(survivors re-run under `-race`), reverted after each. Six runs were unscorable — five broke the
build on an unused import or variable and one did not apply — and five of those six were re-expressed
and are scored below. `diff -r` against a pre-campaign copy of `event/` — sources, tests **and**
`testdata/` — reports the tree byte-identical afterwards; `gofmt -l event` silent, `go vet ./event/`
clean, `go test -race -count=3 ./event/` green.

### Survived — 15

| # | Mutation | Where | Finding |
|---|---|---|---|
| M12 | `jsonCodec.Decode` swallows `json.Unmarshal`'s error | `codec.go:57-61` | **GAP-T20** |
| M07 | `carry` takes `typeNameOf[B]()` rather than `previous.typeName` | `chain.go:77` | GAP-T25 |
| M10 | `roundTrip` drops the `read.accepts(sample)` guard | `fact.go:125-127` | GAP-T25 |
| M16 | `linkOf`'s `accepts` answers `true` for every sample | `chain.go:67` | GAP-T25 |
| M15 | `zero = bytes.Clone(zero)` deleted | `fact.go:137` | GAP-T22 |
| M17 | `selfDecode(bytes.Clone(written))` → `selfDecode(written)` | `fact.go:141` | GAP-T22 |
| M18 | `reusesItsBuffer` drops the whole disturbance arm | `fact.go:168-180` | **GAP-T22** |
| M13 | `Family()` returns the constant `"accounts.account"` | `aggregate.go:46-49` | **GAP-T23** |
| M08 | `Fold` returns the zero state on cause 1 | `aggregate.go:73-74` | GAP-T27 |
| M11 | `Fold` checks the carried refusal **before** the crossing | `aggregate.go:76-85` | GAP-T27 |
| M21 | `Fold` short-circuits an empty change list before `locate` | `aggregate.go:70-75` | GAP-T30 |
| M09 | `Aggregate.Key` returns the raw rendering beside its refusal | `aggregate.go:53-54` | GAP-T29 |
| M20 | `Then` substitutes `JSON[B]()` for the codec it was handed | `chain.go:61` | GAP-T26 |
| M22 | an undefined symbol added to the `change` crossing fixture | `testdata/crossings/change/change.go` | **GAP-T24** |
| M23 | an undefined symbol added to the `identity` crossing fixture | `testdata/crossings/identity/identity.go` | **GAP-T24** |

One further survivor is **not** a finding: `sealed atomic.Bool` → a plain `bool` shim survives
`go test` and is **killed under `-race`** by `TestTheSealRefusesALateFact`/"a fact declared as the
first reader runs" (`WARNING: DATA RACE`, then `FAIL`), exactly as the plan's round-2 log claims.
The section's own checkpoint runs `-race`, so the case holds.

### Caught — 93, grouped by the file the mutation broke

| Where | Mutations, all killed | Caught by |
|---|---|---|
| `encodable.go` (22) | the interface refusal; `ownMethods`' writes-without-a-reader arm; `ownMethods`' addressability arm; `objectKey` asking the kinds first; `objectKey`'s reads-without-a-writer arm; the empty-object refusal; the colliding-name refusal; the claim walk not following embedded fields; the claim walk's cycle guard; `json:"-"` ignored in `fields`; `json:"-"` ignored in the claim walk; the visited set keyed by type; `readsAs` asking only the value's method set; the chan/func/complex arm; a tag's name being the whole tag; the three bounds raised to 2²⁰; `writeRoute` asking text before JSON; a slice element made non-addressable; a map value made addressable; an array element always addressable; an embedded field with a name of its own promoted anyway; `structBehind` not following a pointer | `TestADeclaration`/"the shipped codec answers for the whole type graph" (20), /"a refusal names which asymmetry it found" (1), /"a type graph larger than the walk" (1); the cycle-guard mutation kills the binary |
| `fact.go` (18) | `New` not freezing; `New` without the full-slice expression; the `MaxPayloadBytes` check deleted; the same off by one (`>=`); `New` discarding the encode refusal; `New` discarding the key refusal; `New` recording revision 1; `RoundTrip`'s sample-count check; the zero-sample refusal; `written = bytes.Clone(written)` deleted; the aliasing verdict; the fidelity comparison; `reusesItsBuffer`'s shared-backing arm; `sharesMemory`'s empty-slice guard; `sameValue` as `reflect.DeepEqual`; `equalByMethod` not reading the signature; `sameValue` never asking `Equal`; `sameValue` treating NaN as unequal; `sameValue` comparing unexported fields; `applierOf`'s revision bound; `Revisions()` returning 1; `Name()` returning ""; `TryDeclare` ignoring `declare`'s refusal; `applierOf` never calling the fold; `roundTrip` reading every sample with the current codec; `roundTrip` not carrying through the upcasters; `RoundTrip` not sealing; `New` not sealing; the wire-name text rule; the nil fold; the empty chain | `TestACodecThatDecodesIntoAReusedBufferIsCaught` (9), `TestAChangeRetainsNoApplicationValue` (3), `TestFoldRefusesAnotherInstance` (4), `TestADeclaration` (6), `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` (3), `TestTheSealRefusesALateFact` (2), `TestACodecPanicBecomesThatMethodsOwnRefusal` (1) |
| `chain.go` (8) | `Then` skipping `canEncodeWith`; `Then` retaining only the current revision; `upcastTo` not recovering; `upcastTo` returning the raw error; `From` finding nil by `== nil`; the begins-at-`Then` check; `linkOf`'s `selfZero` answering a constant; `carry` dropping the upcaster; the current revision reading through the previous codec | `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` (4), `TestAnUpcasterMayRefuseAndAFoldMayNot` (2), `TestADeclaration` (3) |
| `codec.go` (7) | `Encode` marshalling `value` rather than `&value`; a trailing byte appended to every payload; `encodeWith`/`decodeWith`/`canEncodeWith` each not recovering; `encodeWith`/`decodeWith` not wrapping the codec's error; `JSON` never charging its answer | `TestADeclaration`/"what the shipped codec writes is frozen" (2), /"…accepts, it records with its contents" (1), `TestACodecPanicBecomesThatMethodsOwnRefusal` (2), `TestEveryMalformedDeclaration…` (1), `TestFoldRefusesAnotherInstance` (2) |
| `aggregate.go` (11) | the key text rule deleted; the key checked against `MaxNameBytes`; `TryDefine` accepting a nil mapper; the family text rule; `Fold` not cloning the frozen payload; `Fold` returning the zero state on causes 2 and 4; `Fold` checking only the first change; `Fold` not sealing; `Key` not sealing; `locate` dropping the family from the stream; `Family()` returning a constant that is not the fixture's | `TestFoldRefusesAnotherInstance` (5), `TestAReferenceKindStateFoldsWithoutAliasing` (1), `TestAChangeRetainsNoApplicationValue` (1), `TestTheSealRefusesALateFact` (2), `TestEveryMalformedDeclaration…` (2), `TestADeclaration` (1), `TestACodecPanicBecomes…` (1) |
| `change.go` (4) | `decidedFor` answering nil; `decidedFor` comparing the stream before the carried refusal; `Stream()` returning the zero stream; `Err()` returning nil | `TestFoldRefusesAnotherInstance` (2), `TestAChangeRetainsNoApplicationValue` (1), `TestACodecPanicBecomes…` (1) |
| `seal.go` (4) | `seal()` doing nothing; `declare` not checking the seal; the duplicate-name refusal; the enumeration comment losing a row | `TestTheSealRefusesALateFact` (3), `TestADeclaration`/"a second aggregate shares no name space" (1) |
| `testdata/` (1) | the `change` fixture rewritten so it no longer crosses (and therefore compiles) | `TestTheCrossingsThatMustNotCompile` |

### Unscorable — 6

`From` skipping `canEncodeWith` (unused generic function — the class is covered by the scored
`Then` mutation and by `TestEveryMalformedDeclaration…`/"a codec is asked at every revision, and the
refusal names which one", which names revision 1 explicitly); the visited set keyed by type, a tag's
name being the whole tag, `roundTrip` dropping the aliasing verdict, `roundTrip` dropping the
fidelity comparison, and `Fold`'s empty short-circuit — each re-expressed in a compiling form and
scored above.

---

## Round 1 verdict

**Not green.** Two `[critical][immediate]` and three `[high][immediate]` findings are open:

- **GAP-T20** — `event.JSON`'s decode-failure path is asserted nowhere; the codec can swallow every
  error and the suite, the seed corpus and 3.9 M fuzz executions stay green. §UC-015's S2 half has
  no test.
- **GAP-T21** — three ordinary Go embeddings that the shipped codec accepts and that silently delete
  data from every fact have no row in a 49-row declaration table. (Implementation half: GAP-185/186/187,
  still open.)
- **GAP-T22** — the second arm of C1's non-aliasing proxy, with both of its clones, is deletable with
  the suite green.
- **GAP-T23** — `Family()` is pinned by one literal against the one example family; a constant survives.
- **GAP-T24** — the crossings test accepts any build failure as proof of §UC-026 and §INV-020.

GAP-T25 through GAP-T33 are `[medium]`/`[low]` and do not block; the four `deferred` ones (T28, T30,
T31, T32, T33) belong in the plan's `## Debt`.

Nothing was edited to produce this report: the twelve tests, two fuzz targets and 56 subtests the
plan's checkpoint claims all exist and all pass, and the tree is byte-identical to its pre-review
state.

---

## Round 1 dispositions — 2026-09-07

**Nine closed, five deferred, none dropped.** GAP-T20 to GAP-T27 and GAP-T29 — every
`[immediate]` finding at any severity — are closed in the section with the mutation evidence
recorded on each. GAP-T28, GAP-T30, GAP-T31, GAP-T32 and GAP-T33 are carried into the plan's
`## Debt` under *From the S2 test review, round 1*, each with the section that owns the assertion
named.

GAP-T21 could not be closed as a test-only change: its rows assert refusals the walk did not
make. Its implementation half — **GAP-185, GAP-186 and GAP-187**, all `[critical][immediate]` and
all open in `EVENTSOURCE_P1_S2_GAPS.md` round 3 — is closed with it, and the walk's contract goes
from five refusals to eight in the same change. `#### event/codec.go`'s refusal list, the walk's
own comment in `event/encodable.go` and the plan's §S2 all carry the new shape.

**Two close criteria were not met as written, and both are argued on the finding.** GAP-T21's
suggested accepting control (*the same shape with the embedded type tagged*) is wrong — a JSON tag
does not undo Go's method promotion, measured — so the tagged spelling is refused instead.
GAP-T24's second criterion (*an unrelated undefined symbol turns the test red*) is wrong for the
same kind of reason — `go build` reports the crossing error **and** the typo, so the fixture still
proves what it claims; the mutation that matters is the crossing ceasing to be expressible while
the build still fails, and that one is killed.

**Mutation campaign for this round: 22 applied, 21 killed, 1 not applicable.**

| Mutation | Where | Verdict | Closed |
|---|---|---|---|
| `jsonCodec.Decode` swallows `json.Unmarshal`'s error | `codec.go:59` | KILLED — `TestFoldRefusesAnotherInstance` | GAP-T20 |
| the same, against the fuzz seed corpus | `codec.go:59` | KILLED — `FuzzAStoredPayloadIsFoldedOrRefused` | GAP-T20 |
| the promoted-marshaller arm deleted | `encodable.go` | KILLED — `TestADeclaration` | GAP-T21 / GAP-185 |
| `besideIt` counts the embedded field itself (over-broad repair) | `encodable.go` | KILLED — the `itemised` **control** | GAP-T21 / GAP-185 |
| `readsAs` accepts a value receiver again | `encodable.go` | KILLED — `TestADeclaration` | GAP-T21 / GAP-186 |
| `objectKey`'s read probe accepts a value receiver again | `encodable.go` | KILLED — `TestADeclaration` | GAP-T21 / GAP-186 |
| the embedded-pointer refusal deleted | `encodable.go` | KILLED — `TestADeclaration` | GAP-T21 / GAP-187 |
| the same refusal raised where the pointer is seen (over-broad repair) | `encodable.go` | KILLED — the `ring` **control** | GAP-T21 / GAP-187 |
| `reusesItsBuffer` drops the whole disturbance arm | `fact.go:168-180` | KILLED | GAP-T22 |
| `zero = bytes.Clone(zero)` deleted | `fact.go:137` | KILLED — accuses the control | GAP-T22 |
| `selfDecode(bytes.Clone(written))` → `selfDecode(written)` | `fact.go:141` | KILLED | GAP-T22 |
| the same at the second decode | `fact.go:160` | KILLED | GAP-T22 |
| `Family()` returns the constant `"accounts.account"` | `aggregate.go:46-49` | KILLED | GAP-T23 |
| the `change` fixture stops crossing **and** fails to build unrelatedly | `testdata/crossings/change` | KILLED | GAP-T24 |
| the `identity` fixture stops crossing **and** fails to build unrelatedly | `testdata/crossings/identity` | KILLED | GAP-T24 |
| the `change` fixture rewritten so it compiles | `testdata/crossings/change` | KILLED | GAP-T24 |
| an undefined symbol beside an intact crossing | `testdata/crossings/*` | n/a — the compiler still reports the crossing | GAP-T24, argued |
| `carry` takes `typeNameOf[B]()` | `chain.go:77` | KILLED | GAP-T25 |
| `roundTrip` drops the `read.accepts(sample)` guard | `fact.go:125-127` | KILLED | GAP-T25 |
| `linkOf`'s `accepts` answers `true` for every sample | `chain.go:67` | KILLED | GAP-T25 |
| `Then` substitutes `JSON[B]()` for the codec it was handed | `chain.go:61` | KILLED | GAP-T26 |
| `Fold` returns the zero state on cause 1 | `aggregate.go:73-74` | KILLED | GAP-T27 |
| `Fold` checks the carried refusal before the crossing | `aggregate.go:76-85` | KILLED | GAP-T27 |
| `Aggregate.Key` returns the raw rendering beside its refusal | `aggregate.go:53-54` | KILLED | GAP-T29 |

**What the section is now.** Twelve tests, two fuzz targets, **62** subtests (was 56); the
declaration tables are 35 refused and 31 accepted rows (was 26 and 23) with a 17-row diagnosis
table (was 11). Four codec fixtures are added — `sealedCodec`, `sealingCodec`, `unescapingCodec`
and the `money`/`sloppy`/`casual` marshalling shapes — and each has a control of the same wire
format or the same receiver spelling beside it.

---

## Round 2 — econv test reviewer (clean context, re-audit) — 2026-09-07

**What was graded.** The same twelve tests, two fuzz targets and three benchmarks, now **62**
subtests over **2 546** test lines in `event/{declaration,seal,fold,roundtrip,upcast,crossings}_test.go`
plus the two S2 targets in `event/fuzz_test.go`, against `event/{codec,encodable,chain,aggregate,fact,change,seal}.go`
(1 110 lines) — the plan's §S2, §Contracts, §Coverage matrix and §Carried gaps C1/C9/C10,
`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` §UC-001/002/003/004/005/012/014/015/016/021/
024/026/041/042/050/051/052/056/063/064/065/067/068 and §INV-004/005/010/017/020/021/023/033/042/044,
`EVENTSOURCE_P1_S2_GAPS.md` rounds 1–4 (GAP-188, GAP-189 open; GAP-190 deferred), round 1 of this
file, `CLAUDE.md`, and `~/.claude/skills/econv/references/{gaps,universality,architecture,building-blocks,data-integrity,microkernel,restrictions,readability}.md`.

**Runs, all in this worktree.**

```
go test -race -count=1 ./event/                        ok  1.239s   (cold: real 1.47s)
go test -race -count=3 ./event/                        ok  1.664s
go test -race -count=1 -shuffle=on ./event/  (x3)      ok  1.244 / 1.247 / 1.244s
go vet ./event/                                        EXIT=0
gofmt -l event                                         silent
go test -list '^(…the twelve S2 names…)$'   | grep -c  12
go test -list '^(…the two S2 fuzz names…)$' | grep -c   2
go test -race -v -run '^(…the twelve…)$' | grep -c '    --- PASS'   62
```

The plan's phase-5 numbers reproduce exactly. `diff -r` against a pre-campaign copy of `event/`
(sources, tests **and** `testdata/`) reports the tree byte-identical after the whole campaign; the
only files written were an out-of-tree probe module at `/tmp/eprobe` with a `replace` onto this
checkout.

**Round 1's nine closures were re-verified by mutation, not by reading the dispositions.** All
sixteen re-applied mutations died, each on the case its disposition names:

| Round-1 finding | Mutation re-applied | Verdict |
|---|---|---|
| GAP-T20 | `jsonCodec.Decode` swallows `json.Unmarshal`'s error | KILLED — `TestFoldRefusesAnotherInstance` **and** `FuzzAStoredPayloadIsFoldedOrRefused` |
| GAP-T21 | the promoted-marshaller arm deleted; `readsAs` restored to `\|\|`; the embedded-pointer refusal deleted | KILLED (3/3) — `TestADeclaration`, both the table and the diagnosis subtest |
| GAP-T22 | the whole disturbance arm → `return false, nil`; `zero = bytes.Clone(zero)` deleted; both `selfDecode(bytes.Clone(written))` clones | KILLED (4/4) — one of them by accusing the **control**, as the disposition claims |
| GAP-T23 | `Family()` returns the constant `"accounts.account"` | KILLED — the two-row family table |
| GAP-T24 | the `change` fixture rewritten to fold through its own aggregate **plus** an undefined symbol | KILLED — the substring assertion, naming both compiler errors |
| GAP-T25 | `carry` takes `typeNameOf[B]()`; `roundTrip` drops the `read.accepts` guard | KILLED (2/2) — the five-row diagnosis table |
| GAP-T26 | `Then` substitutes `JSON[B]()` | KILLED — the frozen payload is `{"Minor":250}` |
| GAP-T27 | cause 1 returns the zero state; the carried-refusal loop moved ahead of the crossing loop | KILLED (2/2) |
| GAP-T29 | `Key` returns `this.key(id)` beside its refusal | KILLED — the newline row |

**What is stronger than round 1 found it, said first.** 131 of 153 scored breakages died. Every
sentinel-producing arm of `fact.go`, `chain.go`, `aggregate.go`, `change.go` and `seal.go` is
pinned; `New`'s freeze, its full-slice expression, the payload ceiling and its off-by-one, the
per-fold clone, `applierOf`'s bound at both ends, `decidedFor`'s three cases and its ordering,
`Then`'s revision arithmetic, the seal's enumeration in both directions, and the codec walk's
twenty-two refusal and bound arms all die on the obvious mutation. The concurrency test detects a
**real** race: making `Fact.New` write one field on the shared `*Aggregate` produces
`WARNING: DATA RACE` and `--- FAIL: TestOneDeclarationIsDecidedAndFoldedFromManyGoroutines`.

The findings below are the other side of that: **two whole comparison functions inside
`Fact.RoundTrip` whose recursive arms are dead in the suite, one more spelling of GAP-185's
embedding that ships silently deleting data, and three refusals with a control in one direction
only.**

---

### GAP-T34 [critical][immediate] `sharesMemory`'s pointer, slice-element and map-entry arms can all be deleted with the suite green — and three ordinary aliasing codecs then pass C1's proxy

- **Where:** `event/fact.go:199-201` (the `Pointer, Interface` arm), `:203-208` (the
  `Slice, Array` element walk), `:209-214` (the `Map` entry walk); the test that is supposed to
  hold them, `event/roundtrip_test.go:315-558` — nine subtests, none of which drives a payload
  whose aliased memory is reached through any of the three
- **What:** each arm deleted alone **SURVIVED** the full suite under `-race`; deleted together they
  also survive, and the consequence is measured out of tree (`/tmp/eprobe`, a `replace` onto this
  checkout — nothing in the repository was edited). Three codecs, each handing out a slice of its
  own 64-byte buffer that it fills and never clears, in the prefix format whose zero-value encoding
  is one byte and therefore cannot disturb the sample's bytes:

  ```
                              today            with the three arms deleted
  struct{ Chunks [][]byte }   ErrPayload  →    <nil>
  struct{ Parts map[string][]byte }  ErrPayload  →    <nil>
  struct{ Held *Chunked }     ErrPayload  →    <nil>
  ```

  The nine subtests are decided by the two arms that *are* driven: the top-level
  `Slice, Map` pointer comparison (`fact.go:195-198`, driven by `scratchCodec`, `prefixCodec` and
  `scratchMapCodec`) and the `Struct` field walk (`:215-220`, driven by `counted` and `listed`).
  Every fixture in the file reaches its shared memory through **one exported struct field holding a
  slice or a map directly**. There is no fixture whose payload holds `[][]byte`, `map[string][]byte`,
  `[]struct{…[]byte}` or a pointer.
- **Why this severity:** `restrictions.md`'s "tests that pass on a gutted implementation". C1 /
  GAP-100 is a carried gap whose *only* proof in the whole plan is this helper, and §INV-021's own
  text calls the aliasing codec "ordinary … and not a broken one". A payload that carries its
  chunks in a `[][]byte`, its parts in a `map[string][]byte`, or its body behind a pointer is not
  exotic — it is what a compact binary format decodes into. For that class the obligation is
  enforced by code that no test constrains, so the next refactor of `sharesMemory` (it is 35 lines,
  the second-longest function in the section, and the plan already flags it as the one at maximum
  nesting depth) can delete two thirds of it and ship. The failure it then admits is the one
  §INV-021 says four consecutive rounds produced: the application's state is rewritten by the
  *next* payload the codec reads, with no error anywhere.
- **Why this timing:** S5 exports this exact comparison as `eventtest.RoundTrip`, the conformance
  suite's non-aliasing proxy that every store implementer is told to run. A proxy with three dead
  arms exported into the suite is a promise the suite cannot keep, and S5's own defect fixtures will
  be written against whatever S2 established.
- **Close criteria:**
  - [ ] Three refused fixtures, one per arm: a payload whose aliased slice is an **element of a
        slice** (`struct{ Chunks [][]byte }`), one whose aliased slice is a **map value**
        (`struct{ Parts map[string][]byte }`), and one reached **through a pointer**
        (`struct{ Held *Chunked }`) — each over a codec that fills one buffer and never clears it,
        in a prefix format whose zero encoding is shorter than the sample's, so the disturbance arm
        cannot decide them. Each asserted `ErrPayload`.
  - [ ] An allocating codec of the same wire format beside each one as the control, asserted to
        pass.
  - [ ] Deleting `fact.go:199-201`, `:203-208` or `:209-214` alone turns the matching case red.
- **Status:** closed 2026-09-08 — see `## Round 8 dispositions`.

---

### GAP-T35 [critical][immediate] `sameValue`'s scalar arm — the one that compares every int, string and bool a payload holds — can be replaced by `return true` with the suite green, and so can its pointer arm and its map-length check

- **Where:** `event/fact.go:278-280` (the `default:` arm, which is where every `Int*`, `Uint*`,
  `String` and `Bool` in a payload is decided), `:270-274` (the `Pointer, Interface` arm), `:269`
  (the map-length comparison), `:249-251` (the budget clause); the test that is supposed to hold
  them, `event/roundtrip_test.go:400-460`
- **What:** four survivors, each applied alone and re-run under `-race`:

  | Mutation | Verdict |
  |---|---|
  | `default: return !first.Comparable() \|\| first.Equal(second)` → `return true` | **SURVIVED** |
  | the `Pointer, Interface` arm → `return true` | **SURVIVED** |
  | `first.Len() == second.Len() && sameEntries(…)` → `sameEntries(…)` | **SURVIVED** |
  | `if *budget <= 0 { return true }` → `return false` | **SURVIVED** |

  Measured out of tree, the implementation **does** catch all of these — three codecs that keep the
  shape and change only a scalar, and one that drops a map entry:

  ```
  writes Minor+1, reads it back        → ErrPayload "does not read back the main.Credited it was given"
  lowercases every Reason it records   → ErrPayload, same
  writes "REDACTED" for every Reason   → ErrPayload, same
  drops the map entry "drop"           → ErrPayload, same
  ```

  So the arms are live and load-bearing, and nothing in the suite reaches them. The seven fidelity
  fixtures decide on something else every time: `truncatingCodec` on a **slice length**,
  `roundingCodec` on the **float** arm, `forgetfulCodec` and the two `stamp` samples on
  `time.Time`'s own **`Equal`**, `holding` on the unexported-field skip, `weighed` on the
  signature check, `floatCodec` on NaN. Not one of them changes an integer or a string.
- **Why this severity:** "a codec that drops part of what it was given … found before a stream
  contains one" is `Fact.RoundTrip`'s first stated property (`fact.go:84-88`) and §UC-042's whole
  deliverable, and the most ordinary way a codec breaks it is a scalar: a unit mix-up that writes
  minor units and reads major, a string truncated to a column width, an enum written by ordinal and
  read by name. All four are silently blessed by a suite that a `return true` cannot fail. The
  budget clause is the same shape from the other side — `fact.go:246-247`'s comment states "runs out
  of budget in the caller's favour … what it cannot answer it does not accuse", and inverting it
  makes every payload with more than 1 024 leaves a refused declaration. That half is the test side
  of **GAP-189**, still `open` in `EVENTSOURCE_P1_S2_GAPS.md`.
- **Why this timing:** S5 exports this comparison as `eventtest.RoundTrip`. Its answers become a
  published contract for every store implementer, and S5's `payload ownership` and `stream identity`
  sections are specified to lean on it.
- **Close criteria:**
  - [ ] A refused fixture whose codec changes an **integer** field and nothing else (encode
        `Minor+1`), and one whose codec changes a **string** field and nothing else, each asserted
        `ErrPayload` with the diagnosis substring, and each with the same codec unmangled as its
        control. Replacing the `default:` arm with `return true` turns both red.
  - [ ] A refused fixture whose codec drops one **map entry** while keeping every entry it does
        write, so `first.Len() == second.Len()` is what decides it.
  - [ ] A refused fixture whose changed value is reached **through a pointer**
        (`struct{ Held *Counted }`), so `fact.go:270-274` cannot answer `true`.
  - [ ] One row for the budget clause: a payload with more leaves than `codecGraphNodes` is
        **accepted** (a pass, not an accusation), asserted against the constant rather than derived
        from it — S1's GAP-T15 rule, which `declaration_test.go:427-430` already applies correctly
        to the type-graph bounds.
- **Status:** closed 2026-09-08 — see `## Round 8 dispositions`.

---

### GAP-T36 [critical][immediate] The `json:"-"` spelling of the promoted-marshaller embedding has no row, and it is accepted: `struct{ Money json:"-"; SKU string }` writes `500` and reads `SKU` back as `""`

- **Where:** `event/declaration_test.go:632-638` — the three embedding rows the section added for
  GAP-185 are the untagged spelling (`lined`), the standard-library one (`noted`) and the
  `json:"money"` tagged one; there is no `json:"-"` row. The code they gate is
  `event/encodable.go:242`, `promotedMarshaller`'s `if !field.Anonymous || field.Tag.Get("json") == "-" { continue }`
- **What:** measured out of tree, with nothing in the repository edited:

  ```
  type Skipped struct{ Money `json:"-"`; SKU string }   // Money declares the JSON pair
      CanEncode = <nil>
      Encode({{500} SKU-1}) = 500
      json.Unmarshal(500, &Skipped{}) = {Money:{Cents:500} SKU:}
  ```

  `SKU` is written by nobody and read back by nobody, with `nil` at every door. This is exactly
  GAP-185's failure, in its fourth spelling. The declaration table refuses `lined`, `noted` and the
  `json:"money"` shape and accepts this one, because `promotedMarshaller` treats a `json:"-"` tag on
  the embedded field as evidence that the field is not the writer — which is false: **Go's method
  promotion is a language rule and no struct tag undoes it**, which is the argument round 1 itself
  made when it moved the `json:"money"` spelling from the accepted table to the refused one.
  Removing the `json:"-"` skip at `encodable.go:242` (which makes the walk *correct* for this
  shape) **SURVIVED** the full suite, because no row of either table drives it.
- **Why this severity:** `gaps.md` — silent data loss. Every field declared beside the embedded one
  is permanently absent from every fact ever recorded, and §INV-023 ("encodability is the codec's
  question, answered before `main`") plus §UC-056's panic are precisely what `TestADeclaration` is
  the only gate for. `json:"-"` on an embedded field is what a developer writes *believing* it
  suppresses the promotion — it is the shape someone reaches for after reading the `lined` refusal
  message, and the message's own remedy ("name the embedded field instead of promoting it") is the
  correct one that this escape lets them skip.
- **Why this timing:** it is the fourth route of a root cause the section has now repaired three
  times (GAP-178, GAP-179, GAP-185), and each repair was written against the route the finding
  named rather than the rule. S4 and S5 build on `CanEncode` as the boot-time gate; a fact log
  written through this shape is unrecoverable, and there is no later door.
- **Close criteria:**
  - [ ] A refused row for `struct{ money \`json:"-"\`; SKU string }`, red on today's
        `event/encodable.go`, with a diagnosis row asserting it names the embedded type and the
        hidden field — the same pair the `json:"money"` row already has.
  - [ ] An accepting control beside it: the named-field spelling (`invoiced` already serves) and
        `itemised` (the embedded type with nothing beside it).
  - [ ] Deleting the `json:"-"` clause at `encodable.go:242` no longer survives — i.e. once the
        walk is correct, an over-broad repair that refuses `itemised` is caught by the control.

---

### GAP-T37 [high][immediate] Three of the walk's rules have a control in one direction only, so three over-broad repairs turn an ordinary legal shape into a boot-time panic with the suite green

- **Where:** `event/encodable.go:258` (`besideIt`'s `json:"-"` skip), `:245-247`
  (`promotedMarshaller`'s write-route comparison), `:101` (an array element inherits the position's
  addressability); the accepted table, `event/declaration_test.go:649-691`
- **What:** three survivors, each applied alone. Each turns a shape the codec encodes and decodes
  perfectly into an `ErrCodecType` panic out of `Declare`, and the suite stays green because the
  shape has no accepting row:

  | Mutation | The legal shape it starts refusing | Verified accepted today |
  |---|---|---|
  | `besideIt` counts a `json:"-"` field | `struct{ money; Note string \`json:"-"\` }` | `CanEncode=<nil>` |
  | `promotedMarshaller` stops comparing the write route | `struct{ Inner; Note string }` where the **outer** declares its own JSON pair | `CanEncode=<nil>`, `Encode=[0,""]` |
  | an array element loses the position's addressability | `struct{ Held [2]Posted }` — an array of a pointer-receiver pair, at an addressable position | `CanEncode=<nil>`, `Encode={"Held":[[0,""],[0,""]]}` |

  The refused table has the mirror of each — `map[string][2]posted`, `lined`, `pointed` — and the
  accepted table has no row for any of the three. `encodable.go:229-238`'s own comment states the
  second rule ("A struct that declares its own pair and embeds a marshalling type is refused too"),
  which implies a struct that declares its own pair and embeds a **non**-marshalling one is
  accepted; nothing asserts that implication.
- **Why this severity:** `universality.md` and the section's own discipline — "put a control case
  next to any test that could pass vacuously" (`CLAUDE.md`) — applied in one direction. A refusal
  with a control only on the refusing side cannot tell "refuses the shape that loses data" from
  "refuses everything shaped roughly like it", and the walk gained three arms in round 3 and three
  more in round 1 of this review; the next one is the one that over-reaches. The cost of an
  over-broad refusal is not cosmetic here: `Declare` panics, so it is a process that will not boot,
  for a payload type the shipped codec round-trips.
- **Why this timing:** the walk is a public contract (`event.JSON`'s `CanEncode`) that S3, S4 and
  S5 all declare fixtures against, and `encodable.go` is at 379 of the 400-line threshold with the
  plan stating "the next arm the walk gains is the one that splits it". A split with no controls on
  the accepting side is where an over-broad arm survives the move.
- **Close criteria:**
  - [ ] Three accepted rows: `struct{ money; Note string \`json:"-"\` }`; a struct declaring its own
        `MarshalJSON`/`UnmarshalJSON` pair that embeds a plain struct with a field beside it; and
        `struct{ Held [2]posted }`.
  - [ ] Each of the three mutations above turns the matching row red.
- **Status:** closed 2026-09-08 — see `## Round 8 dispositions`.

---

### GAP-T38 [medium][immediate] The applier `TryDeclare` writes into the fact table is read by no test — `a.declare(name, nil)` survives the whole suite

- **Where:** `event/fact.go:41-43` (`if err := a.declare(name, apply); err != nil`);
  `event/seal_test.go:162`, the only read of `aggregate.facts`, which asks for **membership** and
  never for the value: `_, held := declared.aggregate.facts["accounts.late"]`
- **What:** `a.declare(name, apply)` → `a.declare(name, nil)` **SURVIVED**. Every test reaches a
  fact's applier through the `*Fact` handle (`declared.credited.apply`, `Change.apply`) and never
  through the table the declaration writes it into. The table is therefore a write-only structure
  in S2, and its only asserted property is the set of its keys.
- **Why this severity:** the map's *value* is the one thing a declaration produces that nothing
  reads back, and it is what S4's `Bind` will dispatch every stored event on. A `nil` there, or the
  wrong fact's applier under a name (`this.facts[name] = apply` with the name from one fact and the
  applier from another is one transposed line), turns every `Load` into a nil-pointer panic or a
  silent replay of the wrong fold — and S2's own gate would be green. The plan already names
  `applierOf`'s revision bound as "implemented and not exercised by S2, named rather than left to
  be found" and drives it directly through `credited.apply`; the table's contents are the same class
  of unreachable-until-S4 code and got no such treatment.
- **Why this timing:** S4 writes `Bind` against this table. A test written after `Bind` exists
  cannot distinguish "the table holds the right applier" from "`Bind` looks it up correctly".
- **Close criteria:**
  - [ ] One subtest that reaches `aggregate.facts[name]` for two distinct facts of one aggregate and
        asserts each applier folds the payload **its own** fact encodes — red on `declare(name, nil)`
        and red on the two appliers transposed.

---

### GAP-T39 [medium][immediate] Two of the walk's three bounds have no test of their own: `codecGraphEdges` is never exercised, and neither `codecGraphDepth` nor `codecGraphNodes` can be deleted alone

- **Where:** `event/encodable.go:135-141` (`descend`, the edge counter), `:83` (the depth and node
  test, one `if`); `event/declaration_test.go:426-458`, the only subtest that drives any of them
- **What:** three survivors:

  | Mutation | Verdict |
  |---|---|
  | `descend` never increments `this.edges` | **SURVIVED** |
  | `visit`'s bound drops the `depth > codecGraphDepth` clause | **SURVIVED** |
  | `visit`'s bound drops the `len(this.seen) >= codecGraphNodes` clause | **SURVIVED** |

  The subtest builds two shapes: 1 100 nested single-element arrays and a 1 100-field struct. The
  first is 1 100 deep **and** 1 100 distinct types, so it fires on whichever of the two clauses
  survives; the second is decided by `members.budget`, not by the walk's node count. Nothing is
  wide, so `codecGraphEdges` is never approached. Measured, the edge bound **is** live and is the
  one that fires first for a wide graph: 60 structs of 100 `int` fields each (6 000 edges, 61 nodes,
  depth 2) is refused with *"the type graph … is larger than the codec walks"*, and 20 of the same
  (2 000 edges) is accepted.
- **Why this severity:** §INV-022's rule is that every bound is "declared, reachable and
  unescapable", and the subtest's own opening assertion pins all three constants against the
  literals its shapes were built from — which reads as though all three are exercised. One of the
  three is not exercised at all, and the other two are not separable. A deleted `this.edges++` means
  an application's wide type graph is walked to the end, which is the DoS shape the bound exists
  for and the one the kernel is told not to trust.
- **Why this timing:** cheap now (two generated shapes beside the two already there), and the
  section's checkpoint claims the bounds as covered; S6's structural pass will read that claim.
- **Close criteria:**
  - [ ] A generated **wide** shape that exceeds `codecGraphEdges` while staying under
        `codecGraphNodes` and `codecGraphDepth` — 60 structs of 100 fields is measured to work —
        asserted refused, with a 20-struct shape of the same construction asserted accepted as its
        control. Deleting `this.edges++` turns it red.
  - [ ] A shape that exceeds the **node** bound while staying under the depth bound (a struct of
        1 100 distinct one-field struct types is one), so the two clauses at `encodable.go:83` are
        separable.

---

### GAP-T40 [low][deferred] The seal's locking discipline is pinned by nothing: both `seal()`'s mutex and `declare()`'s lock ordering can be removed with the suite green under `-race` and 200 rounds

- **Where:** `event/seal.go:38-40` (`this.mutex.Lock(); this.sealed.Store(true); this.mutex.Unlock()`),
  `:44-48` (`declare` taking the lock **before** reading the seal);
  `event/seal_test.go:140-175`, the 200-round race subtest
- **What:** two survivors under `-race -count=1`:

  | Mutation | Verdict |
  |---|---|
  | `seal()` stores without taking the mutex | **SURVIVED** |
  | `declare()` reads `sealed` **before** taking the mutex | **SURVIVED** |

  The subtest's assertion is that the error and the table agree about one write — `arrived == nil`
  iff the fact is in the table. Both mutations preserve that; what they break is the ordering
  clause `seal.go:31-33` states, *"a fact arriving with the first observation is either accepted or
  refused and never both"* read as *the reader that sealed does not then see a later write*. That is
  currently unobservable, because nothing in phase 1 reads `facts` except this test, after the
  goroutines have joined.
- **Why this severity:** low — the property is real but has no reader yet, and the third mutation
  the plan's round-2 log claims (`sealed` as a plain `bool`) genuinely does turn the case red under
  `-race`, so the atomic half is held.
- **Why this timing:** deferred. **S4's `Bind`** is the first party that reads the fact table
  concurrently with a possible late `Declare`, and it is the section that can make the ordering
  observable — a `Bind` that walks the table while a goroutine declares must see a consistent map.
- **Close criteria:**
  - [ ] Once `Bind` exists, one case that walks the fact table from a reader goroutine while a
        second declares, asserting the reader's view and the declaration's answer agree — red when
        `seal()` stores outside the mutex or `declare()` reads the seal outside it.

---

## Round 1's deferred five, re-checked

Unchanged and still correctly in `## Debt`; each was re-confirmed by the mutation or the reading it
rests on.

| Item | Re-check |
|---|---|
| GAP-T28 — §UC-052's `time.Time`-holding state control | still absent from `declaration_test.go:559-594`; the three shapes are unchanged |
| GAP-T30 — `Fold` with an empty change list | `if len(changes) == 0 { return state, nil }` before `locate` **SURVIVED** again |
| GAP-T31 — the round-trip fidelity cases read the real clock | `roundtrip_test.go:426-427` still `time.Now()`; no flake in 3× `-count=3` and 3× `-shuffle=on` |
| GAP-T32 — the concurrency test crosses no upcaster | `seal_test.go:274` still round-trips the one-revision `opened`; a `Change` always carries the current revision |
| GAP-T33 — the marker fact accepted at one door and unpassable at the other | `declaration_test.go:664` still accepts `JSON[struct{}]()`; GAP-190 still `[medium][deferred]` and open |

---

## Coverage of S2's assigned matrix rows — round 2

**27 of 33 hold; the six below do not, or hold only in part.** Round 1's seven are down to six:
UC-015, UC-014's decode half, UC-041's cause-1 and ordering clauses, UC-026/INV-020's compile half
and C1's second arm are all now held and were re-verified by mutation.

| Item | Verdict |
|---|---|
| UC-001, UC-002 (compile), UC-003, UC-004, UC-005, UC-012, UC-014, UC-015, UC-016, UC-021, UC-024, UC-026, UC-050, UC-051, UC-056, UC-063, UC-064, UC-065, UC-067, UC-068 | held — each has at least one mutation that dies on it |
| INV-004, INV-005, INV-010, INV-017, INV-020, INV-021 (1, 2, 8), INV-033, INV-042, INV-044 | held |
| **UC-042 / C1** | held for the two arms the fixtures drive; **not held** for the three `sharesMemory` arms (GAP-T34) and for every scalar the fidelity comparison decides (GAP-T35) |
| **INV-023** | held for seven of the walk's eight refusals; **not held** for the `json:"-"` embedding, which is accepted and loses data (GAP-T36) |
| **UC-041** | held for all four causes and both orderings; the empty-list edge is still open as GAP-T30 |
| **UC-052** | held for three of the seven shapes; §UC-052's own named control is still absent (GAP-T28) |
| **INV-022** (the walk's own bounds) | held for one of three; `codecGraphEdges` is unexercised and the other two are inseparable (GAP-T39) |
| **C9** | held for codec, mapper and fold, and the concurrency test detects a real injected race; the upcaster is still not driven concurrently (GAP-T32) |
| **C10.1** | held, and still the strongest single case in the section |

**No test in the section is without a UC or INV behind it**, and the four reads of package internals
are the same four round 1 justified (`credited.apply`, `change.payload`, `declared.aggregate.facts`,
`chargeJSON`). One of them, `facts`, is read for membership only — that is GAP-T38.

**Doubles discipline is clean.** Eighteen doubles, every one a real implementation of the project's
own `Codec[V]`, each breaking exactly one clause of the codec contract, and fourteen with a control
of the same wire format beside them. No mock, nothing third-party patched, nothing the project owns
mocked. Two embed a nil `Codec[V]` (`panickingCodec`, `refusingCredit`) and are safe only because
the kernel calls the one method each overrides; that is a deliberate narrowing, not over-mocking.

**Determinism and isolation.** No `t.Parallel`, no sleep, no network, no shared mutable fixture.
Three `-shuffle=on` runs and `-count=3` are green. `TestTheCrossingsThatMustNotCompile` shells out
to `go build` in a `t.TempDir` with `GOPROXY=off` and leaves nothing behind. The two `time.Now()`
call sites are GAP-T31, deferred. No file, temp module or `testdata/fuzz` corpus was left behind by
this campaign.

**Speed and layering.** The whole package is **1.24 s** under `-race`, **1.47 s** cold, of which the
crossings build is 0.12 s and the 200-round seal race 0.03 s. Unit only; no database, no store.

---

## Mutation log — round 2: 162 breakages applied, 153 scored, 131 caught, 22 survived

Applied to the working tree one at a time with a scripted harness, `go test -count=1 ./event/`
(survivors re-run under `-race`), reverted after each. Nine were unscorable — eight broke the build
on an unused import or variable and one did not change behaviour — and six of those were
re-expressed in a compiling form and are scored. `diff -r` against a pre-campaign copy of `event/`
— sources, tests **and** `testdata/` — reports the tree byte-identical afterwards; `gofmt -l event`
silent, `go vet ./event/` clean, `go test -race -count=3 ./event/` green.

### Survived — 22, of which 4 are equivalent mutations and 1 is a carried deferral

| # | Mutation | Where | Finding |
|---|---|---|---|
| X3 | `sharesMemory` drops the `Pointer, Interface` arm | `fact.go:199-201` | **GAP-T34** |
| X4 | `sharesMemory` drops the slice/array element walk | `fact.go:203-208` | **GAP-T34** |
| X5 | `sharesMemory` drops the map entry walk | `fact.go:209-214` | **GAP-T34** |
| X6 | `sameValue`'s `default:` arm answers `true` | `fact.go:278-280` | **GAP-T35** |
| X19 | `sameValue`'s `Pointer, Interface` arm answers `true` | `fact.go:270-274` | **GAP-T35** |
| F33 | `sameValue` drops the map-length comparison | `fact.go:269` | **GAP-T35** |
| X2 | `sameValue`'s budget exhaustion answers `false` | `fact.go:249-251` | GAP-T35 (test half of the open GAP-189) |
| E5 | `promotedMarshaller` stops skipping a `json:"-"` embedded field | `encodable.go:242` | **GAP-T36** |
| E4 | `besideIt` counts a `json:"-"` field | `encodable.go:258` | **GAP-T37** |
| E6 | `promotedMarshaller` stops comparing the write route | `encodable.go:245-247` | **GAP-T37** |
| E11 | an array element loses the position's addressability | `encodable.go:101` | **GAP-T37** |
| X29 | `TryDeclare` writes `nil` into the fact table | `fact.go:41` | **GAP-T38** |
| E29 | `descend` never counts an edge | `encodable.go:139` | **GAP-T39** |
| X16 | `visit`'s bound drops the depth clause | `encodable.go:83` | GAP-T39 |
| X17 | `visit`'s bound drops the node clause | `encodable.go:83` | GAP-T39 |
| X13 | `seal()` stores without the mutex | `seal.go:38-40` | GAP-T40 |
| X14 | `declare()` reads the seal outside the lock | `seal.go:44-48` | GAP-T40 |
| A11 | `Fold` short-circuits an empty change list | `aggregate.go:70` | GAP-T30, carried in `## Debt` |
| E2 | `writesAs` drops the not-a-pointer guard | `encodable.go:311` | **equivalent** — `**T` implements no marshaller |
| X1 | `sameEntries` iterates the sample rather than the answer | `fact.go:294` | **equivalent** — the lengths are compared first |
| X20 | `sameValue`'s kind-mismatch branch answers `true` | `fact.go:253-255` | **equivalent** in the reachable graph |
| X22 | `sharesMemory`'s guard admits mismatched kinds | `fact.go:190` | **equivalent** — both values have one type |

### Caught — 131, grouped by the file the mutation broke

| Where | Mutations, all killed | Caught by |
|---|---|---|
| `encodable.go` (25) | the interface refusal; all three `ownMethods` arms; all three `objectKey` route arms; `objectKey` asking the kinds first; the empty-object refusal; the colliding-name refusal; `readAsJSON`'s promotion clause; the visited set ignoring addressability; the tag parser; the three bounds raised together; `writeRoute`'s order; `json:"-"` ignored in `fields`; a map value made addressable; `chargeJSON` starting non-addressable; `structBehind` not following a pointer; `members.collect` not carrying `blocked`; `blocked` recorded for an exported embedded pointer; the members budget never decremented; the members budget set to the edge bound; the embedded-pointer refusal; the promoted-marshaller arm; `readsAs` accepting the value receiver | `TestADeclaration`/"the shipped codec answers for the whole type graph" (19), /"a refusal names which asymmetry it found" (4), /"a type graph larger than the walk" (2) |
| `fact.go` (33) | `New` not freezing, without the full-slice expression, keeping the codec's own array, recording revision 1, discarding the encode or the key refusal, not sealing; the payload ceiling deleted and off by one; `RoundTrip` not sealing, dropping the sample count, returning no carried values; `roundTrip` dropping the zero-sample refusal, the aliasing verdict, the fidelity comparison, `bytes.Clone(written)`, both `selfDecode` clones, reading every sample with the current codec; `reusesItsBuffer` dropping the shared-backing arm, decoding the same buffer twice; `sharesMemory` dropping the empty-slice guard and the struct walk; `sameValue` as `DeepEqual`, never asking `Equal`, treating NaN as unequal, comparing unexported fields, not recursing into a struct, dropping the slice-length check, its float arm answering true; `equalByMethod` not reading the signature; `applierOf`'s bound deleted and off by one at each end, never calling the fold; `Revisions`, `Name`, `TryDeclare`'s four checks | `TestACodecThatDecodesIntoAReusedBufferIsCaught` (13), `TestAChangeRetainsNoApplicationValue` (3), `TestFoldRefusesAnotherInstance` (4), `TestADeclaration` (8), `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` (4), `TestTheSealRefusesALateFact` (1) |
| `chain.go` (12) | `From`'s nil check, `== nil`, its `canEncodeWith`; `Then`'s `canEncodeWith`, its begins-at-`From` check, its nil-codec and nil-upcaster checks, retaining only the current revision, its revision arithmetic; `upcastTo` not recovering and returning the raw error; `carry` dropping the upcaster, taking `typeNameOf[B]()`; `linkOf`'s `selfZero` and `accepts` | `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` (6), `TestAnUpcasterMayRefuseAndAFoldMayNot` (2), `TestADeclaration` (3), `TestACodecThatDecodesIntoAReusedBufferIsCaught` (2) |
| `codec.go` (9) | `Encode` marshalling `value`; `JSON` never charging; all three `recover` blocks; `encodeWith`/`decodeWith` not wrapping; `canEncodeWith` naming no revision; `Decode` swallowing `json.Unmarshal`'s error | `TestADeclaration` (3), `TestACodecPanicBecomesThatMethodsOwnRefusal` (2), `TestEveryMalformedDeclaration…` (2), `TestFoldRefusesAnotherInstance` (2, one of them the shipped-codec decode row) |
| `aggregate.go` (16) | `TryDefine`'s two checks; `locate`'s key rule, the wrong cap, the dropped family; `Fold` not cloning, checking only the first change, returning the zero state on cause 1 and on cause 4, reversing the list, applying revision 1, the cause order swapped; `Family`/`Key`/`Fold` not sealing; `Key` returning the raw rendering; `Family` returning a constant | `TestFoldRefusesAnotherInstance` (7), `TestAReferenceKindStateFoldsWithoutAliasing` (1), `TestAChangeRetainsNoApplicationValue` (1), `TestTheSealRefusesALateFact` (3), `TestEveryMalformedDeclaration…` (2), `TestADeclaration` (2) |
| `change.go` (6) | `decidedFor` answering nil, comparing the stream before the carried refusal, naming no fact, dropping the no-fact case, comparing only the key, comparing only the family; `Stream()` and `Err()` | `TestFoldRefusesAnotherInstance` (4), `TestAChangeRetainsNoApplicationValue` (1), `TestACodecPanicBecomes…` (1) |
| `seal.go` (5) | `seal()` doing nothing; `declare` not checking the seal; the duplicate-name refusal; the enumeration losing a row; the enumeration gaining a row that does not exist | `TestTheSealRefusesALateFact` (4), `TestADeclaration`/"a second aggregate shares no name space" (1) |
| the shared `*Aggregate` (1) | `Fact.New` writes one field on the aggregate every request shares | `TestOneDeclarationIsDecidedAndFoldedFromManyGoroutines` — `WARNING: DATA RACE`, then `FAIL` |
| `testdata/` (1) | the `change` fixture rewritten to fold through its own aggregate **and** to fail the build unrelatedly | `TestTheCrossingsThatMustNotCompile` — the substring assertion |

### Unscorable — 9

`readAsJSON` dropping the promotion clause, the visited set keyed by type alone, a tag's name being
the whole tag, `Fold` not cloning, `reusesItsBuffer` dropping the shared-backing arm and `From`
skipping `canEncodeWith` each broke the build on an unused import or variable; the first five were
re-expressed in a compiling form and are scored above. `sealed` as a plain `bool` needs a shim type
to compile and is already covered by the plan's own round-2 log. One mutation (`R1`, a field added
to `*Aggregate` and never written) changed no behaviour and was re-expressed as the scored race
above.

---

## Round 2 verdict

**Not green.** Three `[critical][immediate]` and one `[high][immediate]` finding are open:

- **GAP-T34** — `sharesMemory`'s pointer, slice-element and map-entry arms are all deletable with
  the suite green, and three ordinary aliasing codecs then pass C1's proxy with `<nil>`.
- **GAP-T35** — `sameValue`'s scalar arm, its pointer arm and its map-length check are all
  deletable with the suite green; a codec that writes 250 and reads 251, or lowercases every string
  it records, is caught by the implementation and by no test.
- **GAP-T36** — `struct{ Money json:"-"; SKU string }` is accepted by the shipped codec, writes
  `500` and reads `SKU` back as `""`. GAP-185's fourth spelling, with no row in a 66-row table.
- **GAP-T37** — three of the walk's rules have a control in one direction only, so three over-broad
  repairs turn a legal shape into a boot-time panic with the suite green.

GAP-T38 and GAP-T39 are `[medium][immediate]` and do not block the gate but are close-before-S4
work; GAP-T40 is `[low][deferred]` and belongs in `## Debt` beside round 1's five, which are all
still correctly carried.

Nothing was edited to produce this report: the twelve tests, two fuzz targets and 62 subtests the
plan's checkpoint claims all exist and all pass, and the tree is byte-identical to its pre-review
state.

---

## Round 2 dispositions — 2026-09-08 — GAP-T36

**GAP-T36 — closed by removing the `json:"-"` skip from `promotedMarshaller`, plus one refused row,
one accepting control and one diagnosis row.**

*Reproduced first, out of tree.* `/tmp/vvprobe`, a third module with a `replace` onto this
checkout, nothing in the repository edited:

```
Skipped  CanEncode = <nil>
Skipped  Encode = 500  err=<nil>
Skipped  Decode = {Money:{Cents:500} SKU:}  err=<nil>
Lined    CanEncode = ... so SKU is written by nobody and read back by nobody ...
Named    CanEncode = <nil>
Itemised CanEncode = <nil>
```

The finding's reading of the cause is the correct one and the fix is the one it names. Go's method
promotion is a language rule; `json:"-"` on an embedded field tells `encoding/json` not to write
that field **as a member** and leaves the promoted `MarshalJSON` exactly where it was. So the tag
is no longer asked about at `event/encodable.go`: `promotedMarshaller` now skips only a field that
is not `Anonymous`. `besideIt` still reads the tag, because there the tag means what it says — a
field spelled `json:"-"` is one the author declared unwritten, not one the promotion hid.

After the fix, same probe, same command:

```
Skipped  CanEncode = event: the codec cannot encode its own reader type: event: the declaration is
  malformed: main.Skipped writes itself through the MarshalJSON of the embedded main.Money, so SKU
  is written by nobody and read back by nobody; name the embedded field instead of promoting it, or
  declare a codec of your own
Named    CanEncode = <nil>
Itemised CanEncode = <nil>
Tallied  round-trip: 700 -> {Money:{Cents:700}} err=<nil> CanEncode=<nil>
```

*Close criteria, one by one.*

- **A refused row for the tagged spelling** — `event/declaration_test.go`, named type `skipped`
  (`money` embedded with `json:"-"`, `SKU string` beside it), in the refused table beside the
  `json:"money"` row. Red on the pre-fix walk: re-applying the E5 mutation gives
  `declaration_test.go:656: the shipped codec accepted the same shape spelled json:"-", which does
  not undo it either (<nil>), and what it cannot encode must be refused before main`.
- **A diagnosis row** asserting the message names both the embedded type and the hidden field:
  substring `the embedded event.money, so SKU is written by nobody`. Red on the pre-fix walk:
  `declaration_test.go:771: the embedding spelled json:"-" was accepted, so nothing here says which
  repair it needs`.
- **An accepting control beside it** — `invoiced` (named field) and `itemised` (embedded, nothing
  beside it) already served; added `tallied`, the tagged spelling *with nothing beside it*, which
  is the shape the naive repair ("refuse a `json:"-"` embedded marshaller outright") breaks and
  neither existing row catches. Verified truthful out of tree: `tallied` round-trips `700`.
  Mutation `hidden != "" || field.Tag.Get("json") == "-"` is now caught —
  `declaration_test.go:700: the shipped codec refused the same, spelled json:"-"`.
- **The E5 mutation no longer survives.** It is the exact revert of this fix and is killed by the
  refused row and the diagnosis row together, in two subtests.

*Docs updated in the same change.* `docs/ai/decisions/D-124` — the refusal bullet now says no tag
on the embedded field escapes it, `json:"-"` included. `EVENTSOURCE_P1_PLAN.md` §S2 refusal 6 — the
same, with GAP-T36 named as where it was found. `docs/modules/{en,ru}/event.md` do not enumerate the
eight shapes and needed no edit; `docs/ai/flows/FL-036` and the `event` use-case pages name no
promotion rule.

*Gates.* `gofmt -l .` silent, `go build ./...` ok, `go vet ./event/...` clean,
`go test -race -count=1 ./event/...` green (`event` 4.997s, `eventmemory` 1.042s, `eventtest`
2.507s), `make unit` green with no `FAIL` line, `make check` green through all nine checks.

GAP-T34, GAP-T35, GAP-T37, GAP-T38, GAP-T39 and GAP-T40 are untouched here and stay open as round 2
left them.

---

## Round 3 — clean-context remediation audit (impl reviewer) — 2026-09-08 — the GAP-T36 cluster

Scope: the one cluster handed to this round — `promotedMarshaller`'s `json:"-"` skip
(`event/encodable.go`) and the missing refused row (`event/declaration_test.go`). Everything below
was derived from the code and from mutation, not from the round-2 disposition note. The tree was
restored byte-for-byte after every mutation (`sha256 event/encodable.go` =
`2dd3dc58fc2f6ecfc34f53fe5921b49ae114d77078871121666dd173059e6baa`, unchanged from the state this
round found).

### GAP-T36 [critical][immediate] The `json:"-"` spelling of the promoted-marshaller embedding — verified closed

Not closed by the note; closed by the code and by four mutations.

- **The skip is gone.** `event/encodable.go:244-248` is now `field := value.Field(index); if
  !field.Anonymous { continue }`. `grep -n 'json:"-"' event/encodable.go` returns two hits, both in
  prose (`:152`, `:235`); no branch in `promotedMarshaller` reads a tag.
- **The defect is real and the refusal is right.** Measured out of tree (`/tmp/promoprobe`, a
  standalone module, nothing in this repository imported):
  `json.Marshal(skipped{money{500}, "ABC"})` = `500`; `json.Unmarshal` of those bytes gives
  `{money:{Cents:500} SKU:}`. `SKU` is lost with `nil` at both doors.
- **The revert is killed, in two subtests.** Re-applying
  `if !field.Anonymous || field.Tag.Get("json") == "-" { continue }` →
  `declaration_test.go:656: the shipped codec accepted the same shape spelled json:"-" ... (<nil>)`
  **and** `declaration_test.go:771: the embedding spelled json:"-" was accepted, so nothing here
  says which repair it needs`.
- **The two plausible over-broad repairs are killed by controls.**
  `besideIt` dropping `index == embedded` → `declaration_test.go:700: the shipped codec refused an
  embedded marshalling type with no field beside it` (`itemised`).
  `promotedMarshaller` refusing on the tag itself, before `besideIt` → `declaration_test.go:700:
  the shipped codec refused the same, spelled json:"-"` (`tallied`) plus the diagnosis row. The new
  `tallied` control therefore earns its place: it is the only row that catches the naive repair,
  and `itemised` does not.
- **Adjacent shapes still answer correctly.** Probed in-tree (temporary `_test.go`, deleted):
  `struct{ money; SKU string \`json:"-"\` }` accepted, `struct{ money; sku string }` accepted,
  `struct{ auditMid; SKU string }` (two-level promotion) refused, `struct{ *money; SKU string }`
  refused.
- **No public surface moved.** `git diff event/encodable.go` = 14 insertions / 10 deletions, of
  which 10/9 are the doc comment and 1/1 is the condition. Package `event` exports 138 top-level
  symbols before and after.
- **Docs closed with it.** `docs/ai/decisions/D-124` line 50-51 now states the rule
  (`promotion is a language rule, so no tag on the embedded field escapes this, json:"-"
  included`); `EVENTSOURCE_P1_PLAN.md:1435` carries the same with GAP-T36 named.
- **Status:** fixed

Gates run by this round: `gofmt -l .` silent; `go vet ./event/...` clean;
`go test -race -count=2 ./event/...` green (`event` 5.304s, `eventmemory` 1.082s, `eventtest`
4.011s), no flake; `go test ./scripts/ -run Event` ok; `make check` green through all nine checks.

**Microkernel, derived not inherited.** `grep -rn "eventmemory\|eventpg\|eventtest" event/*.go |
grep -v _test.go` → **0 hits**; the only two kernel files that name an extension at all are
`declaration_test.go` and `sources_test.go`, both tests. Every `event.X` symbol `eventmemory`
touches from outside the package is exported — 27 of them, `AppendRequest Authority Backing
BadCursor Capabilities Closed Conflict Cursor Envelope ErrClosed ErrWrongStore Failure Limits
MaxBatchCount MaxKeyBytes MaxPageCount MaxPayloadBytes NewAuthority NewBacking Position Refused
ResidentPage Store Stream Supported Unsupported Version` — and `Cursor` is a bare `string`
(`event/identity.go:14`), so a store mints and parses one with no help. `event/eventpg` is still
writable with zero diffs under `event/`. This cluster added no unexported-only construction path.

---

### GAP-T41 [high][immediate] The text-route arm of `promotedMarshaller` has no row anywhere, its mutant survives the whole suite, and GAP-185's close criterion (c) was never met

- **Where:** `event/encodable.go:243-257` (`promotedMarshaller`, reached with
  `writes == byTextMethods` from `ownMethods:216-217`); `event/declaration_test.go:611-658`,
  `:659-698`, `:746-777` — the 36 refused, 32 accepted and 18 diagnosis rows, none of which
  embeds a text-pair type
- **What:** `promotedMarshaller` is route-generic — it compares `writeRoute(...) != writes` and
  renders `writes.writer()`, so it refuses `MarshalText` promotion exactly as it refuses
  `MarshalJSON` promotion. Nothing tests that half. The mutation

  ```go
  func promotedMarshaller(value reflect.Type, writes route, where string) error {
      if writes == byTextMethods { return nil }   // <- added
      for index := range value.NumField() {
  ```

  **SURVIVED** `go test ./event/...` — `event` ok 0.830s, `eventmemory` ok, `eventtest` ok. The
  two complete text pairs in the fixture set (`settled` at `declaration_test.go:188-192`,
  `district` at `:162-166`) appear only as **map keys** (`:667`, `:679`); no fixture embeds either.

  The shape the arm protects, measured out of tree:

  ```
  type settled string    // MarshalText on the value receiver, UnmarshalText on the pointer
  type textPromoted struct{ settled; SKU string }
      json.Marshal   = "s"
      json.Unmarshal = {settled:s SKU:}      // SKU lost, nil at both doors
  ```

  and in-tree today: `CanEncode` = *"event.auditTextPromoted writes itself through the MarshalText
  of the embedded event.settled, so SKU is written by nobody and read back by nobody"*.
- **Why this severity:** `gaps.md` — *missing test for a stated invariant*. The invariant is stated
  three times: `D-124` bullet 7 ("a struct written by a marshaller promoted from an embedded field
  with a field of its own beside it"), `encodable.go:58-63`'s eight-shape list, and the plan's
  refusal 6. It is also **GAP-185's own close criterion 2 verbatim** — *"`declaration_test.go`
  carries shapes (a), (b), (c) and (d)"* — where shape (c) is spelled out at
  `EVENTSOURCE_P1_S2_GAPS.md:1200`: `type Tagged struct{ Code; Note string } // Code has the TEXT
  pair`. GAP-185's closing note (`:1251`) accounts for (a), (b), the `json:"money"` spelling and
  (d), and **does not mention (c) at all**. The criterion was dropped, not met, and the finding was
  marked closed anyway — a finding closed by a note and not by the code is still open, and this one
  is. The code is correct today, so there is no live data loss; what is missing is the only thing
  that keeps it correct through the next edit.
- **Why this timing:** the plan states `encodable.go` is at 383 of the 400-line threshold and that
  "the next arm the walk gains is the one that splits it". An unprotected route is exactly what a
  file split loses silently. `CanEncode` is a published contract that S3, S4 and S5 declare
  fixtures against, and a fact log written through this shape is unrecoverable — there is no later
  door. It is also the fifth spelling of a root cause repaired four times (GAP-178, GAP-179,
  GAP-185, GAP-T36), each time against the route the finding named rather than against the rule.
- **Close criteria:**
  - [ ] A refused row for a struct embedding a **text**-pair type with a field of its own beside it
        (`struct{ settled; SKU string }` — `settled` already exists), asserted `ErrCodecType`.
  - [ ] A diagnosis row for it asserting the message names `MarshalText` and the hidden field, so
        it cannot pass through the JSON arm beside it.
  - [ ] An accepting control beside it: the same embedded text pair with **nothing** beside it, and
        the same type in a **named** field — the pair `itemised`/`invoiced` already provides for the
        JSON route.
  - [ ] `if writes == byTextMethods { return nil }` in `promotedMarshaller` turns the refused row
        and the diagnosis row red.
  - [ ] GAP-185's status line in `EVENTSOURCE_P1_S2_GAPS.md` is amended to record that criterion 2
        shape (c) was outstanding until now, rather than left claiming a closure it did not have.
- **Status:** open

---

### GAP-T42 [medium][immediate] An embedded pointer to a marshalling type is refused by the wrong arm, with a diagnosis whose remedy the developer has already applied

- **Where:** `event/encodable.go:220-221` (`ownMethods`' value-receiver arm) reached before
  `promotedMarshaller` for this shape; no row in `event/declaration_test.go`
- **What:** `struct{ *money; SKU string }` — embedding a **pointer** to a type whose pair is
  `MarshalJSON` on the value receiver and `UnmarshalJSON` on the pointer receiver. Both method sets
  promote through a pointer embed, so the outer type implements `json.Unmarshaler` **on its own
  value**, `readsAs` answers `byFields`, and `ownMethods` falls into the value-receiver arm.
  Measured in-tree:

  ```
  CanEncode = event.auditPtrEmbed declares UnmarshalJSON on the value receiver, so encoding/json
  calls it on a copy and discards everything it writes; every fact recorded with it reads back as
  the zero value with no error at any door — declare it on the pointer receiver
  ```

  Two things in that sentence are false for this shape. `encoding/json` does not discard: measured
  out of tree, `json.Marshal` writes `9` and `json.Unmarshal` **panics** with
  `runtime error: invalid memory address or nil pointer dereference`, because the promoted method
  runs against a nil embedded pointer. And the remedy — "declare it on the pointer receiver" — is
  already satisfied: `UnmarshalJSON` *is* on `*money`. The developer is told to do the thing they
  did.
- **Why this severity:** the shape fails closed, so no fact is lost and nothing is corrupted —
  `medium`, not `high`. But message accuracy is a property this suite asserts explicitly (the
  *"a refusal names which asymmetry it found, because the three share one sentinel"* subtest, 18
  rows), and this is a refusal whose stated asymmetry is not the one that is there. The correct
  answer is `promotedMarshaller`'s: the struct writes itself as the embedded value and `SKU` is
  written by nobody.
- **Why this timing:** `CanEncode` is a boot-time gate that panics through `Declare`; a diagnosis
  that names an impossible repair turns a five-minute fix into an afternoon, and S3/S4/S5 all
  inherit these messages as the published contract. Cheap now, and the arm ordering is the thing
  that would have to be revisited later.
- **Close criteria:**
  - [ ] A refused row for `struct{ *money; SKU string }`, and a diagnosis row asserting the message
        names the embedded type and the hidden field rather than a receiver the developer already
        got right.
  - [ ] `ownMethods` reaches `promotedMarshaller` for this shape, or the value-receiver arm's
        message is qualified so it does not claim "discards everything it writes" where the real
        outcome is a nil dereference.
  - [ ] An accepting control beside it: an embedded pointer to a marshalling type with nothing
        beside it, if that shape is in fact legal, or a second refused row if it is not.
- **Status:** open

---

### GAP-T43 [medium][deferred] D-124 lists the eight refusals as data-loss shapes only, and never states the deliberate false positive that this fix just widened

- **Where:** `docs/ai/decisions/D-124-event-json-is-deliberately-smaller-than-the-codecs-beside-it.md:38-54`;
  the rule it omits is stated only in `event/encodable.go:240-242`
- **What:** the decision doc introduces the list with *"the walk refuses eight shapes whose bytes no
  declaration reads back"* — every bullet reads as a shape that loses data. The code refuses a ninth
  thing that does **not**: a struct that declares its own correct `MarshalJSON`/`UnmarshalJSON`
  pair and also embeds a marshalling type. Measured in-tree, a struct embedding `time.Time` with
  its own complete pair that writes and reads both fields is refused with
  *"writes itself through the MarshalJSON of the embedded time.Time, so Note is written by
  nobody"* — which is not true of it. `reflect` cannot distinguish declared from promoted, so the
  over-refusal is the right trade; it is simply not in the binding document. This round's fix
  widened its reach: the same struct written with `json:"-"` on the embedded field was accepted
  before and is refused now.
- **Why this severity:** the decision doc is binding law here and is what the next agent reads
  instead of the code; a consumer whose legal payload type will not boot finds no explanation in
  it. No behaviour is wrong — `medium`.
- **Why this timing:** documentation of an existing, correct trade; it changes no contract and
  blocks no section. Deferred, and it must not be dropped.
- **Close criteria:**
  - [ ] `D-124` gains a paragraph naming the false positive, the reason `reflect` forces it, and
        the two remedies the refusal already offers.
  - [ ] The paragraph says explicitly that the tagged spelling does not escape it either, so the
        rule and the GAP-T36 bullet are one statement rather than two.
- **Status:** open

---

### GAP-T44 [low][deferred] The refusal a developer sees after writing `json:"-"` never mentions the tag they wrote

- **Where:** `event/encodable.go:253` — one message for every spelling of the promotion
- **What:** `struct{ money \`json:"-"\`; SKU string }` is refused with the identical sentence
  `struct{ money; SKU string }` gets: *"writes itself through the MarshalJSON of the embedded
  event.money, so SKU is written by nobody"*. The tag is the developer's stated belief that the
  embedding is suppressed; the message neither confirms nor contradicts it, and the remedy it does
  offer — "name the embedded field instead of promoting it" — reads as advice to start encoding the
  money they had just told the encoder to skip. `encodable.go:235-238`'s own comment says this
  spelling "is the spelling a developer reaches for after reading this refusal", which is precisely
  the reader the message does not address.
- **Why this severity:** wording of a diagnostic. Nothing is wrong and nothing is lost — `low`.
- **Why this timing:** cosmetic, and the fix is one clause; no section depends on it.
- **Close criteria:**
  - [ ] When the embedded field carries a `json` tag, the refusal says so and states that no tag
        undoes method promotion.
  - [ ] The existing diagnosis row for `skipped` is extended, or a second one added, so the tagged
        and untagged messages cannot silently become one again.
- **Status:** open

---

### GAP-T45 [low][deferred] D-124's "Proven by" does not name the test that is the only gate for its eight refusals

- **Where:** `docs/ai/decisions/D-124-...md:140-155`
- **What:** the six entries name `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt`,
  `TestACodecThatDecodesIntoAReusedBufferIsCaught`, `TestTheProxiesCompareSomethingThatCanDiffer`,
  `TestACodecPanicBecomesThatMethodsOwnRefusal`,
  `TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen`,
  `TestOneDeclarationIsDecidedAndFoldedFromManyGoroutines` and
  `FuzzAStoredPayloadIsFoldedOrRefused` — all seven verified to exist. The eight-shape list at
  `:38-54` is gated by none of them: it is gated by `TestADeclaration`'s *"the shipped codec answers
  for the whole type graph"* (36 refused + 32 accepted rows) and *"a refusal names which asymmetry
  it found"* (18 rows), and this round's fix added three rows to exactly those two subtests.
  `CLAUDE.md` makes updating **Proven by** part of the same change as a test that pins an invariant.
- **Why this severity:** documentation index, no behaviour — `low`.
- **Why this timing:** no contract depends on it; it is drift to be swept, not a block.
- **Close criteria:**
  - [ ] `D-124`'s **Proven by** names `TestADeclaration` and the two subtests, with the row counts
        or without them, so a reader looking for the gate on the eight shapes finds it.
- **Status:** open

---

**Verdict for this cluster.** GAP-T36 is genuinely closed — reproduced, reverted, killed, and
controlled. The cluster does **not** go green: GAP-T41 is `[high][immediate]` and is the same arm,
carrying an unmet close criterion inherited from GAP-185 and a surviving mutant proven here.

---

## Round 4 dispositions — 2026-09-08 — GAP-T41 … GAP-T45

All five findings of round 3's GAP-T36 cluster are closed. Every one was reproduced before it was
repaired, and every repair is held by a mutation that turns a named row red.

### GAP-T41 — closed by asking the promotion question of both routes and gating it with three rows

*Reproduced first.* The mutation the finding names was applied to this tree and run:

```
### M1 GAP-T41 revert: the text route stops being asked
  (before the fix) ok  github.com/frostgrove/vv/event  0.849s   <- SURVIVED, as the finding said
```

Measured out of tree (`/tmp/vvprobe3`, standalone module, nothing here imported):

```
TextPromoted Marshal   = "s"
TextPromoted Unmarshal = {Settled:s SKU:}   err=<nil>      <- SKU lost, nil at both doors
```

*What changed.* No production change was needed — `promotedMarshaller` was route-generic already.
What was missing was the gate, and it is now three fixtures and four rows in
`event/declaration_test.go`: `termed` (`struct{ settled; SKU string }`) on the refused table and in
the diagnosis table asserting `the MarshalText of the embedded event.settled, so SKU is written by
nobody`; `netted` (`struct{ settled }`) and `agreed` (`struct{ Terms settled; SKU string }`) as the
accepting controls. Both controls were verified truthful out of tree rather than assumed:
`Netted round-trip: "net30" -> {Settled:net30}` and
`Agreed round-trip: {"Terms":"net30","SKU":"ABC"} -> {Terms:net30 SKU:ABC}`.

*Evidence it is closed.* Same mutation, after:

```
### M1 GAP-T41 revert: the text route stops being asked
--- FAIL: TestADeclaration/the_shipped_codec_answers_for_the_whole_type_graph…
    declaration_test.go:685: the shipped codec accepted the same promotion on the text route
    (<nil>), and what it cannot encode must be refused before main
--- FAIL: TestADeclaration/a_refusal_names_which_asymmetry_it_found…
```

The naive over-broad repair is killed too — `hidden := "SKU"` (refuse any embedded marshalling
type) turns `itemised` red: *"the shipped codec refused an embedded marshalling type with no field
beside it"*. `netted` holds the same line for the text route.

Criterion 5 is met: `EVENTSOURCE_P1_S2_GAPS.md` GAP-185 now carries an **Amended 2026-09-08**
paragraph and a matching note in its summary row, recording that close criterion 2 shape (c) was
outstanding until this round rather than met.

- **Status:** closed

### GAP-T42 — closed by asking the promotion question before the receiver question

*Reproduced first.* In tree, before the fix:

```
ptrEmbed      = … event.probePtrEmbed declares UnmarshalJSON on the value receiver, so
  encoding/json calls it on a copy and discards everything it writes … declare it on the pointer
  receiver
ptrEmbedAlone = … the same message …
```

Out of tree, what actually happens to that shape — the finding's reading confirmed on every
spelling of it, both routes, with and without a field beside it:

```
MoneyAlone zero write    PANIC runtime error: invalid memory address or nil pointer dereference
MoneyAlone read          PANIC …
MoneyBeside read         PANIC …
GuardedAlone read        PANIC …      (the pair on the pointer receiver)
SettledAlone write/read  PANIC …      (the text route)
```

So the message was wrong twice over: nothing is discarded, and the remedy it named was already
applied. The shape is also never legal, which answers criterion 3 — it takes a **second refused
row**, not an accepting control.

*What changed.* `ownMethods` (`event/encodable.go`) now asks `promotedMarshaller` of **every**
struct that writes itself through methods, not only where the two routes agree, and asks it before
the receiver arm. An embedded pointer promotes both of the embedded type's method sets onto the
outer value, which is why the receiver arm answered first. `promotedMarshaller` gained the arm for
it, and refuses with nothing beside it too, because the panic is the whole failure:

```
event.charged writes itself through the MarshalJSON promoted from the embedded pointer
*event.money, which nothing allocates before encoding/json calls the UnmarshalJSON promoted with
it, so every load of a fact recorded with it panics on a nil pointer, and SKU is written by nobody
either; embed the type by value, or name the field
```

`charged` and `owed` are on the refused table; three diagnosis rows name the embedded pointer, the
field it hides, and the same for the no-field-beside-it spelling.

*Evidence it is closed.* Four mutations, each red:

```
M2 promotion asked only where the routes agree  -> charged falls back to "declares UnmarshalJSON
   on the value receiver", which never says "the embedded pointer *event.money"
M3 the embedded-pointer arm removed             -> charged gets the generic promotion message
M9 the pointer arm fires only when a field sits beside it -> owed falls back to the receiver
   message
```

(`owed`'s refused row alone does **not** hold the line — the shape stays refused through the wrong
arm — which is why its diagnosis row exists; the refused row would otherwise pass with the feature
deleted.)

- **Status:** closed

### GAP-T43 — closed by a section in D-124, and the eight-shape list is now nine

`docs/ai/decisions/D-124-…md` gains **The one deliberate false positive**: `reflect` will not say
whether a method is declared or promoted, so a struct with its own complete pair that also embeds a
marshalling type is refused although it round-trips; the trade is stated, the alternative named
(lose a field silently and irreversibly in the one place nothing can be recovered from), and the
two remedies the refusal already offers are listed. The paragraph says the tagged spelling does not
escape it either, so the rule and the GAP-T36 bullet are one statement.

The refused list itself is now **nine** shapes: the embedded-pointer refusal of GAP-T42 is a
different failure from the promotion one — a panic rather than a loss, and one that fires with
nothing beside it — so it is its own bullet. Updated in the same change: `D-124` (the count, the
bullet, the "Where it lives" line), `event/encodable.go`'s `chargeJSON` comment,
`docs/ai/flows/FL-036` (the `D-124` line and the file table) and `EVENTSOURCE_P1_PLAN.md` §S2
(refusal 6 now says "on either route", the new refusal 8, the old 8 renumbered to 9, "all nine
refusals", and the diagnosis row count). `docs/modules/{en,ru}/event.md` enumerate no shapes and
needed no edit.

- **Status:** closed

### GAP-T44 — closed by `noTagUndoesIt`, with a control on the untagged message

The refusal now answers the developer who wrote the tag:

```
event.skipped writes itself through the MarshalJSON of the embedded event.money, so SKU is written
by nobody and read back by nobody — the json:"-" you put on it does not undo that, because a tag
speaks to encoding/json's field walk and method promotion is a language rule; name the embedded
field instead of promoting it, or declare a codec of your own
```

The `json:"money"` spelling gets the same clause with its own tag text. The existing diagnosis row
for `skipped` is unchanged and still passes — the clause was placed after the substring it asserts
— and a second row asserts `the json:"-" you put on it does not undo that`.

The control is the row beside it: `lined`, untagged, asserts `read back by nobody; name the
embedded field`, a substring that exists only when there is **no** clause. Both directions are
mutation-proven — dropping the clause turns the tagged row red, appending it unconditionally turns
the untagged row red (`… — the json:"" you put on it …`).

- **Status:** closed

### GAP-T45 — closed by naming the real gate in D-124's Proven by

`D-124`'s **Proven by** gains `TestADeclaration` and its two subtests — *"the shipped codec answers
for the whole type graph"* and *"a refusal names which asymmetry it found, because the three share
one sentinel"* — and says that the accepting rows (`itemised`, `tallied`, `netted`, `agreed`) are
the controls a repair that over-refuses breaks.

- **Status:** closed

### Gates

```
$ gofmt -l .                              (silent)
$ go build ./...                          ok
$ go vet ./event/...                      ok
$ go test -race -count=1 ./event/...
ok  github.com/frostgrove/vv/event            4.910s
ok  github.com/frostgrove/vv/event/eventmemory 1.048s
ok  github.com/frostgrove/vv/event/eventtest   2.505s
$ go test -race -count=2 ./event/...      ok, no flake (5.384s / 1.096s / 4.067s)
$ make unit                               exit 0, no FAIL line
$ make check                              check-deps, check-tiers, check-utils, check-triplets,
                                          check-todo, check-replaces, check-tidy,
                                          check-otel-schema, check-workspace — all ok
```

### The zero-diff obligation for `event/eventpg`

Untouched. `git diff event/encodable.go | grep '^[+-]func'` shows two additions, both unexported
(`alsoHidden`, `noTagUndoesIt`); no exported declaration was added, removed or changed, and no
value a store constructs moved behind an unexported path. Every change is inside the codec walk,
which a store neither calls nor implements.

---

## Round 5 — clean-context re-audit of the GAP-T36 cluster (impl reviewer) — 2026-09-08

Scope: the cluster handed to this round — `promotedMarshaller`'s `json:"-"` arm
(`event/encodable.go`) and its row in `event/declaration_test.go` — plus everything round 4's
repair of GAP-T41…GAP-T45 touched. Nothing below was taken from a disposition note. Every claim
is the output of code run in this tree or of a mutation applied to it and then reverted; the tree
was restored byte-for-byte after each (`sha256 event/encodable.go` =
`f78fb87256fb5f820fd5befa27867e07cf546741af67290e88d3b4bda3a05b61`, `sha256
event/declaration_test.go` = `312fb58c29f01a90ad32f2a7a6a013edfd35c1703ecdef8970e19649427ffbeb`,
both unchanged from the state this round found).

### What is confirmed closed

**GAP-T36, GAP-T41, GAP-T42, GAP-T44 — closed by the code and by six mutations, each killed.**
Every mutation was applied to this tree, run, and reverted.

```
M1  restore `|| field.Tag.Get("json") == "-"` in promotedMarshaller
    -> declaration_test.go:685  the shipped codec accepted the same shape spelled json:"-"
       declaration_test.go:808  the embedding spelled json:"-" was accepted
M2  `if writes == byTextMethods { return nil }` at the top of promotedMarshaller
    -> declaration_test.go:685  accepted the same promotion on the text route
       declaration_test.go:808  the same promotion on the text route was accepted
M3  delete the embedded-pointer arm (encodable.go:273-275)
    -> declaration_test.go:811  charged refused with "... the embedded *event.money ...", which
       never says "the embedded pointer *event.money"
M4  noTagUndoesIt always returns ""
    -> declaration_test.go:811  skipped never says `the json:"-" you put on it does not undo that`
M5  noTagUndoesIt returns the clause unconditionally
    -> declaration_test.go:811  lined (the untagged control) says `the json:"" you put on it ...`
M6  besideIt stops skipping `index == embedded`
    -> declaration_test.go:731  itemised, the accepting control, is refused
       declaration_test.go:811  charged now names `money` rather than `SKU`
```

Both directions are held: M1–M4 kill the positive rows, M5 and M6 kill the controls. `tallied`,
`itemised`, `netted`, `agreed` and `invoiced` are real controls, not decoration.

**The defect GAP-T36 named is real and the refusal is right.** Measured in this tree with a
temporary probe (`event/zzauditprobe_test.go`, deleted; `ls` confirms it is gone and
`git status event/` shows the same 12 untracked files it showed on arrival):
`json.Marshal(skipped{money{500}, "ABC"})` = `500`, `json.Unmarshal` gives `{money:{Cents:500}
SKU:}` — `SKU` lost, `nil` at both doors. `CanEncode` refuses it, and names the tag.

**GAP-T43 and GAP-T45 — closed in the binding document, verified by reading it.**
`docs/ai/decisions/D-124-…:38` says nine, `:49-53` carries the promotion bullet on either route
with `json:"-"` named and the embedded-pointer bullet, `:62-78` is *The one deliberate false
positive* with the `reflect` reason, the two remedies and the sentence that no tag escapes it,
`:157` and `:166-172` name `TestADeclaration`, its two subtests and the four accepting controls.
`EVENTSOURCE_P1_S2_GAPS.md:1253` and `:1579` carry GAP-185's amendment (criterion 2 shape (c)).
`FL-036:182` and `:237` say nine. The plan's `:1488` says all nine.

**Gates run by this round.** `gofmt -l ./event/` silent · `go vet ./event/...` clean ·
`go test -race -count=2 ./event/...` green, no flake (`event` 5.296s, `eventmemory` 1.085s,
`eventtest` 4.021s; a second full run 5.267s / 1.085s / 4.008s).

**Microkernel — derived, not inherited: PASS.**
`grep -rn "eventmemory\|eventpg\|eventtest" event/*.go | grep -v _test.go` → **0**.
`go list -f '{{join .Imports "\n"}}' ./event` → `bytes context encoding encoding/json errors fmt
crud errs reflect strings sync sync/atomic time unicode unicode/utf8` — stdlib plus two
root-module packages, no third party, no extension. `eventmemory` and `eventtest` each import
`github.com/frostgrove/vv/event` and stdlib only; neither imports the other.
`find event -name go.mod` → 0; `find event -type d -name internal` → 0. No exported interface in
`event` carries an unexported method, so a store outside the package can implement `Store` and
`Log` in full. The exported surface is **byte-identical** before and after this cluster's change:
`go doc -all ./event` diffed against the same command run with `event/encodable.go` and
`event/declaration_test.go` stashed to `HEAD` — no difference. `event/eventpg` is still writable
with zero diffs under `event/`.

---

### GAP-T46 [critical][immediate] The promotion question is asked one hop deep, so a marshaller promoted through two embeddings hides the field at the inner hop with no refusal at any door — the arm GAP-T36 called the last one is not the last one

- **Where:** `event/encodable.go:263-281` (`promotedMarshaller` walks only `value`'s own
  anonymous fields) and `event/encodable.go:298-307` (`besideIt` looks only at `value`'s own
  fields); the rule they are supposed to hold is stated without a depth qualifier at
  `event/encodable.go:58-61` and at
  `docs/ai/decisions/D-124-event-json-is-deliberately-smaller-than-the-codecs-beside-it.md:49-52`
- **What:** `promotedMarshaller` asks *"does an immediate anonymous field of this struct write by
  the route this struct writes by, and does this struct have a field of its own beside it"*. Both
  halves stop at the outermost struct. When the marshaller arrives through **two** embeddings and
  the hidden field sits at the **inner** one, `besideIt` answers `""`, `promotedMarshaller`
  returns `nil`, `ownMethods` then settles the position on `writes == reads` and the walk never
  descends. Measured in this tree (temporary `event/zzauditprobe_test.go`, since deleted):

  ```go
  type probeMid   struct { money; Extra string }   // refused, correctly
  type probeOuter struct { probeMid }              // CanEncode() = <nil>   <- ACCEPTED

  json.Marshal(probeOuter{probeMid{money{500}, "beside"}}) = 500
  json.Unmarshal(that, &probeOuter{})              = {probeMid:{money:{Cents:500} Extra:}}
  ```

  `Extra` is written by nobody and read back by nobody. Every spelling of the shape behaves the
  same way and every one is accepted:

  ```
  probeOuter      struct{ probeMid }                          <nil>   Extra lost
  probeTextOuter  struct{ probeTextMid }  (MarshalText route) <nil>   Extra lost, writes "net30"
  probeTimeOuter  struct{ probeTimeMid }  (probeTimeMid = struct{ time.Time; Zone string })
                                                              <nil>   Zone lost, writes
                                                                      "1970-01-01T00:00:00Z"
  probeThreeC     three levels, hidden field at the innermost <nil>   Deep lost, writes 7
  map[string]probeOuter, []probeOuter, [2]probeOuter, struct{ F probeOuter }
                                                              <nil>   all four
  ```

  `probeTimeOuter` is `noted`, the fixture the refused table calls *"the textbook embedding, whose
  pair arrives from the standard library"* (`declaration_test.go:668`), with exactly one more
  struct wrapped around it. `struct{ probeMid; SKU string }` **is** refused — the hole is
  precisely *"the outer struct has nothing of its own beside the embed"*, which is the shape a
  developer reaches for when factoring a shared `Audit`/`Timestamps`/`Money` block out of several
  payloads.
- **Why this severity:** `gaps.md` — *wrong behaviour, data loss/corruption*, and it is data loss
  in the one place this whole walk exists to protect. A consumer declares
  `type Audit struct { time.Time; Actor string }` as a shared block and
  `Declare(aggregate, "accounts.opened", From(JSON[opened]()), …)` where
  `type opened struct{ Audit }`. `CanEncode` returns nil, the process boots, and every fact ever
  written records the timestamp and drops `Actor`. There is no later door: the bytes are the
  record. `D-124:49-52` states the rule with no depth qualifier and `encodable.go:58-61` repeats
  it, so the binding document promises a refusal the code does not give. This is the sixth
  spelling of one root cause (GAP-178, GAP-179, GAP-185, GAP-T36, GAP-T41 and now this), and the
  fifth time it was repaired against the shape the finding named rather than against the rule —
  the cluster brief calls the `json:"-"` arm *"the last surviving arm of the GAP-178/185 family"*
  and it is not.
- **Why this timing:** `CanEncode` is the boot-time gate S3, S4 and S5 declare fixtures against
  and phase 2's `event/eventpg` inherits unchanged; a fact log written through this shape cannot
  be repaired by anything phase 2 ships. The general mechanism that closes it is not a new row:
  `promotedMarshaller` must follow the **promotion chain** — when an immediate anonymous field
  carries the route the struct writes by and is itself a struct, ask the same question of that
  struct (its own `besideIt`, and its own embedded fields) before answering `nil`, with the same
  visited-set bound the rest of the walk already uses so an embedded pointer to the type being
  walked still terminates. That is a change to the arm, not to the table, and doing it after
  phase 2 has fixtures against these messages is the expensive order.
- **Close criteria:**
  - [ ] `JSON[struct{ probeMid }]().CanEncode()` is `ErrCodecType`, where
        `probeMid = struct{ money; Extra string }`, and the message names `Extra` and the
        embedded type the marshaller came from.
  - [ ] The same for the text route (`struct{ termedMid }` where
        `termedMid = struct{ settled; Extra string }`) and for three levels of embedding.
  - [ ] A refused row and a diagnosis row for each in `event/declaration_test.go`.
  - [ ] The accepting controls survive: `itemised`, `tallied`, `netted`, `agreed`, `invoiced`,
        `ring` (which embeds a pointer to itself), `carried`, `labelled` and
        `struct{ *Stream }` still return `nil`, and a new control — a struct embedding a struct
        that embeds a marshalling type **with nothing hidden at either level** — returns `nil`.
  - [ ] Restricting the new recursion to depth 1 (that is, reverting it) turns the new refused
        rows red, and making it refuse any nested embed turns the new accepting control red.
  - [ ] `D-124`'s promotion bullet and `encodable.go`'s shape list say *at any depth of the
        promotion chain*, so the document and the code state one rule.
- **Status:** open

---

### GAP-T47 [high][immediate] `event/encodable.go` is 422 lines against the 400-line budget the plan tracks it by, and the plan's "No file breaches 400 lines" is now false

- **Where:** `event/encodable.go` (422 lines; `wc -l`); the budget is
  `references/architecture.md:49` — *Lines per file/module <= 400*; the plan asserts compliance at
  `EVENTSOURCE_P1_PLAN.md:5238` (*"**No file breaches 400 lines**"*) and names this exact file as
  the one at the edge at `:3310`
- **What:** counted, not eyeballed: `422` total, `96` comment lines, `30` blank, `296` code. At
  `HEAD` the file was `379`; round 2's `json:"-"` repair took it to `383` and round 4's
  embedded-pointer arm plus `alsoHidden`/`noTagUndoesIt` took it to `422` — **22 over**. The plan
  wrote the trigger itself at `:3310`: *"still under the 400-line threshold but no longer with a
  section's worth of room — the next arm the walk gains is the one that splits it."* The walk
  gained refusal 9 and was not split. Two other measured figures are inside their thresholds and
  are recorded here so the count is not one number: longest function `members.collect` at 40
  lines, then `jsonWalk.visit` 33, `jsonWalk.fields` 21, `ownMethods` 21, `objectKey` 21,
  `promotedMarshaller` 19 — none over the function budget; maximum nesting in the file is 3;
  package `event` imports 13 stdlib packages plus `crud` and `errs`, no cycle.
- **Why this severity:** `gaps.md` — *violation of a measurable threshold in `architecture.md`*.
  Nothing is wrong at runtime today. What it costs is the thing the plan predicted: the file now
  holds two separable subjects — the graph walk with its budgets and visited set (`chargeJSON`,
  `jsonWalk`, `members`) and `encoding/json`'s routing rules restated as refusals (`ownMethods`,
  `promotedMarshaller`, `besideIt`, `writeRoute`, `readRoute`, `readsAs`, `writesAs`,
  `onTheValueReceiver`, `objectKey`, the `route` methods) — and GAP-T46's repair adds to the
  second. A split done after that repair has to move a function that just changed.
- **Why this timing:** it is a structural claim the plan's own metrics section makes and no longer
  holds, and the next change to this file is GAP-T46's, which grows it further. Splitting first is
  strictly cheaper than splitting after.
- **Close criteria:**
  - [ ] `wc -l event/encodable.go` and every file it is split into is `<= 400`.
  - [ ] The exported surface of package `event` is unchanged (`go doc -all ./event` diffed).
  - [ ] `EVENTSOURCE_P1_PLAN.md:5238` and `:3310` record the real numbers rather than the old
        claim, and `docs/ai/flows/FL-036`'s file table names every file the walk now lives in.
  - [ ] `go test -race ./event/...` green with no test edited.
- **Status:** open

---

### GAP-T48 [low][deferred] A struct embedding a write-only marshalling type now names a remedy that leads to a second refusal rather than the missing reader

- **Where:** `event/encodable.go:229-233` — round 4 moved `promotedMarshaller` ahead of the
  "declares no reader" arm at `:240`
- **What:** for `struct{ writeOnly; SKU string }`, where `writeOnly` has `MarshalJSON` and no
  `UnmarshalJSON` at all, the refusal is now

  ```
  event.probeWriteOnlyEmbed writes itself through the MarshalJSON of the embedded
  event.probeWriteOnly, so SKU is written by nobody and read back by nobody; name the embedded
  field instead of promoting it, or declare a codec of your own
  ```

  measured in tree. Before round 4 it was *"writes itself through MarshalJSON and declares no
  UnmarshalJSON"*. Both sentences are true, but the first remedy the new one offers — *name the
  embedded field* — does not fix the type: naming the field makes the outer written field by
  field, the walk descends into `writeOnly`, and the developer gets a second refusal for the
  missing reader. The root cause is one hop further in than the message says.
- **Why this severity:** wording of a diagnostic on a shape that fails closed either way. Nothing
  is lost and nothing is wrong — `low`.
- **Why this timing:** cosmetic, no contract depends on it, and GAP-T46's repair touches the same
  arm; sweeping it then is the cheap moment. Deferred, and not to be dropped.
- **Close criteria:**
  - [ ] Where the embedded type carries no reader on the route it writes by, the refusal says so
        rather than offering only the promotion remedy — or the promotion message names both hops.
  - [ ] A diagnosis row for the shape, so the two messages cannot silently become one.
- **Status:** open

---

### GAP-T49 [low][deferred] A new fixture comment narrates the review round rather than the fixture

- **Where:** `event/declaration_test.go:205-207` (the comment above `termed`)
- **What:** *"…and a walk that asks the question only of the route the last finding named answers
  'legal' here."* — "the last finding" is this artifact's review history, not anything a reader of
  the file can resolve. `CLAUDE.md` says comments are exceptional and tests are not narrated; the
  first two sentences of the same comment do carry an invariant the code cannot make visible (that
  `encoding/json` reaches a text pair on a payload type as readily as a JSON one) and earn their
  place. The comment above `charged` at `:130-133` is the same kind and stays entirely on the
  invariant — it is the model. Removing the trailing clause is expected cleanup, not a regression.
- **Why this severity:** cosmetics — `low`.
- **Why this timing:** no contract, no behaviour; sweep it with the next edit to the file.
- **Close criteria:**
  - [ ] The comment above `termed` states only what `encoding/json` does, with no reference to a
        finding or a review round.
- **Status:** open

---

**Verdict for this cluster.** GAP-T36, GAP-T41, GAP-T42, GAP-T43, GAP-T44 and GAP-T45 are
genuinely closed — reproduced, mutated in six ways and killed in all six, controls proven in both
directions, docs read rather than trusted. The microkernel boolean is derived and holds:
`event/eventpg` is writable with zero diffs under `event/`. The cluster does **not** go green:
GAP-T46 is `[critical][immediate]` and is the same root cause the brief called closed — the walk
asks the promotion question one hop deep, so `struct{ probeMid }` is accepted and loses a field on
every fact ever written — and GAP-T47 is a measured threshold breach the plan's own metrics
section denies.

---

## Round 6 dispositions — 2026-09-08 — GAP-T46 … GAP-T49

All four closed. Every claim below is the output of code run in this tree, or of a mutation applied
to it and then reverted; the tree was restored after each and `sha256 event/routing.go` returned to
`e5dc40bc5fdb99013eeea5882ed3a2d6b44532f0021f755cd2456dc4734f25ba` every time.

### GAP-T46 — closed by asking the promotion question along the whole chain

**Reproduced first.** A scratch `event/zzrepro_test.go` (deleted; `git status event/` shows the same
untracked files it showed on arrival plus `event/routing.go`) printed, before the fix:

```
probeMid   struct{money; Extra}       CanEncode=…event.probeMid writes itself through the
                                      MarshalJSON of the embedded event.money, so Extra is …
probeOuter struct{probeMid}           CanEncode=<nil>
probeTextOuter                        CanEncode=<nil>
probeTimeOuter                        CanEncode=<nil>
probeThreeA (three levels)            CanEncode=<nil>
map[string]probeOuter / []probeOuter / [2]probeOuter / struct{F probeOuter}   all <nil>
json.Marshal(probeOuter{500,"beside"}) = 500
json.Unmarshal -> {probeMid:{money:{Cents:500} Extra:}}
json.Marshal(probeTextOuter) = "net30" · probeTimeOuter = "1970-01-01T00:00:00Z" · probeThreeA = 7
```

Every one of the reviewer's spellings, to the byte.

**The change.** `promotedMarshaller` takes the chain it was reached through and recurses: where an
immediate anonymous field carries the route the struct writes by and **nothing of the struct's own
sits beside it**, the same question is asked of that embedded struct. The recursion enters an
embedded struct held **by value** and nothing else — the embedded-pointer arm returns where it is
found, and a non-struct promotes no fields — and a Go struct cannot contain itself by value, so the
chain is finite without a visited set. That is stated in the function's comment, because it is the
termination argument and the code cannot show it. `besideIt` is unchanged; what changed is who asks
it and with what prefix.

The refusal names the chain rather than the outermost type: *`event.probeOuter` writes itself
through the `MarshalJSON` of **the `event.money` embedded in `probeMid`**, so **`probeMid.Extra`**
is written by nobody and read back by nobody*. At depth 0 the sentence is byte-identical to what it
was, which is why every row round 4 left behind still passes unchanged.

**Rows.** Five refused, one accepted, six diagnoses (the diagnosis table goes from 24 rows to 32 across GAP-T46 and GAP-T48; the refused table to 46 and the accepted to 35). Refused: `stacked` (`struct{ lined }`),
`layered` (`struct{ stacked }` — three levels, hidden at the innermost), `filed`
(`struct{ noted }` — the standard library's pair one hop out), `staged` (`struct{ termed }` — the
text route), `owing` (`struct{ charged }` — the embedded pointer one hop out). Accepted:
**`folded`** (`struct{ itemised }` — two embeddings, nothing hidden at either level), the control
the close criteria asked for. The diagnoses pin the chain in the message, including the
three-level path `stacked.lined.SKU`.

**Mutations, each applied alone and reverted.**

```
M1  the recursion deleted (the fix reverted)   -> declaration_test.go:722  stacked accepted
                                                  declaration_test.go:854  and undiagnosed
M2  any nested embed refused (over-broad)      -> declaration_test.go:769  folded refused
                                                  declaration_test.go:857  layered misdiagnosed
M5  embeddedAs ignores the chain               -> declaration_test.go:857  "the embedded event.money"
M6  the hidden field named without its chain   -> declaration_test.go:857  "so SKU" not "so lined.SKU"
```

Both directions: M1 and M5/M6 kill the positive rows, M2 kills the accepting control.

**Live proof, out of tree.** `/tmp/vvprobe`, a third module with a `replace` onto this checkout,
declares the reviewer's own scenario:

```
Opened{Audit{time.Time; Actor}}   CanEncode=…the MarshalJSON of the time.Time embedded in Audit,
                                  so Audit.Actor is written by nobody…
Wrapped{Line{Money; SKU}}         CanEncode=…the main.Money embedded in Line, so Line.SKU…
Deep (three levels)               CanEncode=…embedded in Wrapped.Line, so Wrapped.Line.SKU…
CleanOuter (control)              CanEncode=<nil>
Opened  writes "1970-01-01T00:00:00Z" and reads back Actor=""
Wrapped writes 500 and reads back SKU=""
CleanOuter writes 500 and reads back {Clean:{Money:{Cents:500}}} — the accepted one round-trips,
through the shipped codec's own Encode/Decode as well
```

**Documents.** `D-124`'s promotion bullet now says *at any depth of the promotion chain* and shows
the shape; its *one deliberate false positive* section says what the chain widens and what stays
legal; `FL-036`'s step 4 and file table name both files; the plan's refusal 6 carries the chain
clause and GAP-T46. `docs/modules/{en,ru}/event.md` gained *What `JSON[V]()` refuses, and when* —
the embedding trap with the accepted spelling beside it, in both languages, because this is the one
refusal a consumer meets by writing ordinary Go.

### GAP-T47 — closed by splitting the file at the boundary the plan itself named

`event/encodable.go` was **422**. It is now **212**, and `event/routing.go` is **256** — the split
is exactly the two subjects the finding and the plan both named: the graph walk (`chargeJSON`,
`jsonWalk`, `position`, `members`, the three budgets) stays in `encodable.go`; `encoding/json`'s
routing rules restated as refusals (`ownMethods`, `promotedMarshaller`, `besideIt`, `writeRoute`,
`readRoute`, `readsAs`, `writesAs`, `onTheValueReceiver`, `objectKey`, the `route` methods) are
`routing.go`. No test was edited to make the split pass and no exported name moved: `make api`
regenerates `docs/api/surface.md` byte-identical across the whole round (`diff` silent against the
copy taken before it), and `go doc -all ./event` names none of the symbols in either file. The plan's `:3334` and `:5262` metrics now record the real numbers, its
`| event/encodable.go |` row is both files, and the largest file shipped in the kernel is
`event/errors.go` at 369.

The seven mutations round 5 verified against the unsplit file were re-run against the split one and
all seven are still killed: the `json:"-"` skip restored, `if writes == byTextMethods { return nil }`,
the embedded-pointer arm deleted, `noTagUndoesIt` silent, `noTagUndoesIt` unconditional, `besideIt`
counting the embedded field, and the promotion question moved back behind the receiver question.

### GAP-T48 — closed by naming the hop the promotion remedy does not reach

`nameItInstead` asks whether the embedded type reads itself back on the route it writes by. Where
it does, the message is what it was — *name the embedded field instead of promoting it, or declare
a codec of your own*. Where it does not, the remedy says which hop is the real one:

```
event.billed  … so SKU is written by nobody and read back by nobody; event.cents declares no
              UnmarshalJSON, so naming the embedded field moves the refusal one hop in rather than
              closing it — declare that reader, or declare a codec of your own
event.mislaid … event.sloppy declares UnmarshalJSON on the value receiver, where encoding/json
              calls it on a copy, so naming the embedded field moves the refusal one hop in rather
              than closing it — declare that reader on the pointer receiver
```

Two refused rows and two diagnosis rows (`billed`, `mislaid`). Mutations: `nameItInstead` always
returning the promotion remedy kills the `billed` diagnosis; always returning the other kills
`lined`'s *"read back by nobody; name the embedded field"* — the control that pins the ordinary
case still saying the ordinary thing.

### GAP-T49 — closed by deleting the clause

The comment above `termed` is now two sentences and both are about what `encoding/json` does. The
clause naming *"the last finding"* is gone.

### Gates

```
$ gofmt -l .                          (silent)
$ go build ./...                      EXIT=0
$ go vet ./event/...                  EXIT=0
$ go test -race -count=1 ./event/...  ok event 4.946s · eventmemory 1.047s · eventtest 2.508s
$ go test -run XXX -fuzz FuzzAStoredPayloadIsFoldedOrRefused -fuzztime 20s ./event/
                                      1 792 845 execs, PASS, nothing written to testdata/fuzz
$ make unit                           zero FAIL lines
$ make check                          nine arms, all ok
```

### The zero-diff obligation for `event/eventpg`

Untouched. The split moved unexported functions between two files of one package and added two
more (`embeddedAs`, `nameItInstead`) plus one parameter on `promotedMarshaller`; `make api`
regenerates `docs/api/surface.md` with no diff. No value a store constructs moved behind an
unexported path, and no exported interface gained a method. `event/eventpg` is still writable with zero diffs
under `event/`.

---

## Round 7 — clean-context re-audit of the GAP-T36 cluster (impl reviewer) — 2026-09-08

Scope: the cluster handed to this round — `promotedMarshaller`'s `json:"-"` arm and its rows in
`event/declaration_test.go` — plus everything round 6's repair of GAP-T46…GAP-T49 touched
(`event/routing.go` is new in that round, 257 lines, untracked). Nothing below is taken from a
disposition note. Every claim is the output of code run in this tree or of a mutation applied to it
and reverted; after each the tree was restored and `diff` confirmed byte-identity, and
`git status --porcelain event/` at the end shows the same 10 modified and 13 untracked files it
showed on arrival.

### What is confirmed closed

**GAP-T36 — closed by the code, and the test that closes it dies without it.**
`event/routing.go:87-89` reads `if !field.Anonymous { continue }`; the `|| field.Tag.Get("json") ==
"-"` clause the finding named is gone. Restoring it (mutation M1, applied and reverted) turns
`declaration_test.go:722` red — *the shipped codec accepted the same shape spelled `json:"-"`* —
and `declaration_test.go:854` — *the embedding spelled `json:"-"` was accepted*. Measured in this
tree with a temporary probe (`event/zzaudit_probe_test.go`, deleted; `git status event/` confirms):
`JSON[struct{ money \`json:"-"\`; SKU string }]().CanEncode()` is `ErrCodecType` and the message
names the tag.

**GAP-T46 — closed along the whole chain, at every position I could reach it from.** A probe over
22 shapes: `struct{ mid }`, the same tagged `json:"mid"`, the same tagged `json:"-"`, three and four
levels of embedding, the `time.Time`-in-a-shared-`Audit`-block shape, an embedded interface, an
unexported intermediate, an intermediate that itself embeds a pointer, and the same outer type
reached through `map[string]T`, `[]T`, `[2]T`, `struct{ F T }` and `struct{ F *T }` — **every one
refused**, each naming the chain (`the event.money embedded in pL3.pL2.pL1, so pL3.pL2.pL1.Deep is
written by nobody`). The accepting controls hold: `struct{ struct{ money } }` round-trips through
`Encode`/`Decode` and `CanEncode` is nil. Four mutations applied and reverted, all killed:
recursion deleted → `declaration_test.go:722`/`:854`; `nameItInstead` always the promotion remedy →
`:857`; `embeddedAs` ignoring the chain → `:857`; the hidden field named without its chain prefix →
`:857`.

**GAP-T47 — closed, counted.** `wc -l`: `event/encodable.go` **213**, `event/routing.go` **257**;
the largest non-test file shipped under `event/` is `event/errors.go` at **369**, and no non-test
file in `event/`, `event/eventmemory/` or `event/eventtest/` exceeds 400 (max of the three trees:
369, 364 `eventtest/sections_write.go`). Longest function in the two files: `members.collect` 40,
`jsonWalk.visit` 33, `promotedMarshaller` 28, `ownMethods` 21, `objectKey` 21 — all ≤ 50. Maximum
nesting depth in `routing.go` is 3. `event` imports 13 stdlib packages plus `crud` and `errs`; no
cycle. The plan records the real numbers at `:3340`, `:5240` and `:5277`; `FL-036:41` and `:241`
name `event/routing.go`.

**GAP-T48, GAP-T49 — closed, read rather than trusted.** `billed` and `mislaid` carry the
second-hop clause and the mutation that removes it kills `declaration_test.go:857`; the comment
above `termed` (`declaration_test.go:219-220`) is two sentences, both about what `encoding/json`
does, with no reference to a finding.

**Gates run by this round.** `gofmt -l .` silent · `go vet ./event/...` exit 0 ·
`go test -race -count=2 ./event/...` green, no flake (`event` 5.304s, `eventmemory` 1.086s,
`eventtest` 4.027s) · `make check` — nine arms, all `ok` · `make api` regenerates
`docs/api/surface.md` **byte-identical** (`diff` silent against a copy taken first).

**Microkernel — derived, not inherited: PASS.**
`grep -rn "eventmemory\|eventpg\|eventtest" event/*.go | grep -v _test.go` → **0**.
`go list -f '{{join .Imports "\n"}}' ./event` → stdlib plus `crud` and `errs`, no third party, no
extension. `find event -name go.mod` → 0; `find event -type d -name internal` → 0. `eventmemory` and
`eventtest` each import `github.com/frostgrove/vv/event` and stdlib; neither imports the other.
`Store` and `Log` (`event/store.go:111`, `:142`) carry **no** unexported method, so a store outside
the package implements them in full; every value a store constructs is exported or has an exported
constructor — `Envelope`/`Record`/`Capabilities`/`Limits` are exported fields, `Cursor` is a
`string`, `Authority` via `NewAuthority`, `Backing` via `NewBacking`, refusals via `Failure`. The
only unexported method on an exported interface is `Declaration.declaration()`, which a store never
implements. `event/eventpg` is still writable with zero diffs under `event/`.

---

### GAP-T50 [critical][immediate] The map-key door never asks the promotion question, so a struct key whose text pair is promoted collapses a map to one entry and loses a field on every fact ever written

- **Where:** `event/routing.go:236-256` (`objectKey` asks only *writes/reads*, and never calls
  `promotedMarshaller`); reached from `event/encodable.go:88-92`, the map arm of `jsonWalk.visit`,
  which passes `at.value.Key()` to `objectKey` and descends only into `Elem()` — the key type is
  never `visit`ed, so `ownMethods` is never asked of it. Test side:
  `event/declaration_test.go:675-689`, `:718`, `:731-749`, `:821-850` — nine map-key rows and not
  one of them keys a map by a struct whose text pair is **promoted**.
- **What:** `ownMethods` (`routing.go:33-53`) asks the promotion question of every *value* position.
  `objectKey` is the parallel gate for a *key* position and asks four questions — pair, reader on
  the value receiver, writer only, reader only — and stops. A struct key that promotes
  `MarshalText`/`UnmarshalText` from an embedded field, with a field of its own beside it, answers
  "writes and reads" and is accepted. Measured in this tree
  (`event/zzaudit_probe_test.go`, since deleted):

  ```go
  type pTerm struct{ Days int }                       // MarshalText / *UnmarshalText
  type pKey  struct { pTerm; Region string }

  JSON[pKey]().CanEncode()          -> ErrCodecType   // refused as a payload
  JSON[map[pKey]int]().CanEncode()  -> <nil>          // ACCEPTED as a key
  JSON[map[pKeyOuter]int]().CanEncode() -> <nil>      // and one embedding further out
  ```

  and it is reachable through the real seam, not only through the codec:

  ```
  type pLedger struct { Balances map[pKey]int; Note string }
  TryDeclare(aggregate, "ledgers.posted", From(JSON[pLedger]()), fold)   err = <nil>
  declared.New("one", …).Err()                                          = <nil>
  recorded bytes: {"Balances":{"30":1,"30":2},"Note":"n"}
  replayed:       Balances:map[{pTerm:{Days:30} Region:""}:2]  Note:"n"
  ```

  Two distinct keys — `{30,"EU"}` and `{30,"US"}` — write the same object name, the fact is
  **recorded with a duplicate JSON name**, and the replay is a one-entry map whose surviving key has
  `Region:""`. Nil at every door: `CanEncode` nil, `TryDeclare` nil, `Change.Err()` nil, `Encode`
  nil, `Decode` nil.
- **Why this severity:** `gaps.md` — *wrong behaviour, data loss/corruption*, in the one place this
  whole walk exists to protect, and it is the same root cause the cluster brief calls closed. A
  consumer declares `type Terms struct{ Duration; Region string }` as a shared block and a payload
  `struct{ Limits map[Terms]int }`; the process boots, every fact is written with a key that
  discards `Region`, and half the entries are gone before anything is read back. The bytes are the
  record and no later door can recover them. `D-124:49-53` states the promotion refusal with no
  position qualifier — *"a struct written by a marshaller promoted from an embedded field with a
  field of its own beside it — on either route, `MarshalJSON` and `MarshalText` alike … asked at any
  depth of the promotion chain"* — so the binding document promises a refusal the code does not
  give, exactly as GAP-T46 found. This is the seventh spelling of one root cause (GAP-178, GAP-179,
  GAP-185, GAP-T36, GAP-T41, GAP-T46, and now the key door), and the sixth time the repair was made
  against the shape the finding named rather than against the rule.
- **Why this timing:** `CanEncode` is the boot-time gate S3, S4 and S5 write fixtures against and
  phase 2's `event/eventpg` inherits unchanged; a fact log written through this shape cannot be
  repaired by anything phase 2 ships. The general mechanism that closes it is not a new row: the key
  position is a position like any other, so `objectKey` must ask the promotion question the value
  door already asks — where the key is a struct and writes by `byTextMethods`, call
  `promotedMarshaller(key, byTextMethods, where, "")` before answering `nil`, with the same chain
  recursion and the same refusal text. Doing it after phase 2 has fixtures against these messages is
  the expensive order.
- **Close criteria:**
  - [ ] `JSON[map[pKey]int]().CanEncode()` is `ErrCodecType` where
        `pKey = struct{ pTerm; Region string }` and `pTerm` carries a text pair, and the message
        names `Region` and the embedded type the `MarshalText` came from.
  - [ ] The same one and two embeddings further out (`map[struct{ pKey }]int`), and for a key whose
        pair arrives from the standard library.
  - [ ] A refused row and a diagnosis row for each in `event/declaration_test.go`, beside the nine
        map-key rows already there.
  - [ ] The accepting controls survive and one is added: `map[district]int`, `map[settled]int`,
        `map[string]int64`, `map[int64]int` still return `nil`, and `map[struct{ pTerm }]int` — a
        struct key that promotes a text pair and hides **nothing** — returns `nil` and round-trips
        through `Encode`/`Decode`.
  - [ ] Reverting the new call turns the new refused rows red, and refusing any struct key with an
        embedded field turns the new accepting control red.
  - [ ] `D-124`'s map-key bullet and its promotion bullet state one rule for both positions, and
        `event/routing.go`'s `objectKey` comment says the key door asks what the value door asks.
- **Status:** open

---

### GAP-T51 [medium][immediate] Removing `besideIt`'s `json:"-"` skip kills no test, so the accept side of the arm this cluster is about is unpinned

- **Where:** `event/routing.go:149-158` (`besideIt`, line 152:
  `if index == embedded || field.Tag.Get("json") == "-" || !readAsJSON(field)`);
  `event/declaration_test.go:731-768` — the accepted table has `itemised` (`struct{ money }`),
  `tallied` (`struct{ money \`json:"-"\` }`) and `folded` (`struct{ itemised }`), and no row where a
  marshalling type is embedded **with a sibling the developer spelled `json:"-"`**.
- **What:** mutation M-E, applied to this tree and reverted — delete `field.Tag.Get("json") == "-"`
  from `besideIt`'s skip list — leaves `go test -run TestADeclaration ./event/` **green**
  (`ok github.com/frostgrove/vv/event 0.009s`). Four other mutations of the same function and its
  callers were killed; this one survives. With the mutation in place,
  `struct{ money; SKU string \`json:"-"\` }` — a legal, correctly round-tripping shape, where the
  developer told `encoding/json` not to write `SKU` — is refused at `Declare`, and no test says so.
  The current behaviour is right; nothing holds it.
- **Why this severity:** no wrong behaviour today. What is missing is the control `CLAUDE.md`
  requires beside anything that could pass vacuously: the whole `json:"-"` arm of this cluster is
  about a tag that *does not* excuse a shape, and the neighbouring rule — a tag on a **sibling**
  that *does* — has no row. A later edit that unifies the two tag reads (an obvious tidy-up, since
  `promotedMarshaller` was just taught to ignore the tag on the embedded field) turns a booting
  consumer application into a panic at `Declare`, and the suite stays green. `medium`, because the
  gap is a missing test rather than a defect.
- **Why this timing:** immediate — it is one row in the accepted table and one in the diagnosis
  table of the file this cluster is already editing, and it is precisely the distinction the
  cluster's own repair created. Adding it after GAP-T50's repair touches the same function is the
  expensive order.
- **Close criteria:**
  - [ ] An accepted row for `struct{ money; SKU string \`json:"-"\` }` (a marshalling embed with a
        deliberately skipped sibling) and one for the same shape one embedding out.
  - [ ] Deleting `field.Tag.Get("json") == "-"` from `besideIt` turns that row red.
  - [ ] `tallied` and `itemised` still pass, so the new row is not the only thing holding the arm.
- **Status:** open

---

### GAP-T52 [low][deferred] A `json:"-"` on the embedded field is named only when the refusal is raised at depth 0, so the developer who wrote the tag one hop out is not told it does not help

- **Where:** `event/routing.go:101` (`noTagUndoesIt(field)` reads the tag of the field the refusal
  is raised at) and `event/routing.go:131-137`
- **What:** measured in this tree. For `struct{ mid \`json:"-"\` }` where
  `mid = struct{ money; Extra string }`, the refusal is raised at the **inner** hop, whose embedded
  field carries no tag, so the message is

  ```
  event.pOuterSkip writes itself through the MarshalJSON of the event.pMoney embedded in pMid,
  so pMid.Extra is written by nobody and read back by nobody; name the embedded field instead of
  promoting it, or declare a codec of your own
  ```

  — accurate, actionable, and silent about the `json:"-"` the developer just wrote and is about to
  write again. At depth 0 the same spelling does say it (`skipped`, `declaration_test.go:835`).
- **Why this severity:** wording of a diagnostic on a shape that fails closed either way. Nothing is
  lost and nothing is wrong — `low`.
- **Why this timing:** cosmetic, no contract depends on it, and GAP-T50's repair touches the same
  message assembly; sweeping it then is the cheap moment. Deferred, and not to be dropped.
- **Close criteria:**
  - [ ] Where any hop of the promotion chain carries a `json` tag on its embedded field, the refusal
        says the tag does not undo promotion.
  - [ ] A diagnosis row for the tagged spelling one embedding out, beside
        `declaration_test.go:835`.
- **Status:** open

---

**Verdict for this cluster.** GAP-T36 is genuinely closed — reproduced, reverted, killed by two
named assertions, and the fix introduced nothing on the shapes it touches. GAP-T46, GAP-T47, GAP-T48
and GAP-T49 are closed too, verified by four more mutations and by counting the files rather than
reading the disposition. The microkernel boolean is derived and holds: `event/eventpg` is writable
with zero diffs under `event/`. The cluster does **not** go green: GAP-T50 is `[critical][immediate]`
and is the same root cause the brief called closed one door over — a struct map key whose text pair
is promoted is accepted, writes a duplicate JSON name, and replays as one entry with a field lost,
through `TryDeclare` and the shipped codec, with nil at every door.

---

## Round 8 dispositions — 2026-09-08 — GAP-T34, GAP-T35, GAP-T37

Tests only. No file under `event/` outside `declaration_test.go` and `roundtrip_test.go` was
edited, so `event/eventpg`'s zero-diff obligation is untouched and no contract in
`EVENTSOURCE_P1_PLAN.md` moved.

**Reproduced first, in this tree.** Twelve mutations, each applied alone to the real
implementation, `go test -count=1 ./event/...` run, the file restored byte-for-byte after each.
The two functions the findings name have moved since they were written — `sharesMemory` is
`valueWalk.shares` and `sameValue` is `valueWalk.same`, both now in `event/comparison.go`, and
`besideIt`/`promotedMarshaller` are in `event/routing.go` — so each mutation is named by what it
does rather than by the line it was found at.

| Mutation | Before | After |
|---|---|---|
| `shares`: the `Pointer, Interface` arm → `return false` | **SURVIVED** | CAUGHT |
| `shares`: the same arm for a pointer only | **SURVIVED** | CAUGHT |
| `shares`: the slice/array element walk deleted | **SURVIVED** | CAUGHT |
| `shares`: the map entry walk deleted | **SURVIVED** | CAUGHT |
| `shares`: the struct field walk deleted (control) | CAUGHT | CAUGHT |
| `same`: the `default:` arm answers `true` for every comparable leaf | CAUGHT | CAUGHT |
| `same`: the same arm for integer kinds only | CAUGHT | CAUGHT |
| `same`: the same arm for `String` only | CAUGHT | CAUGHT |
| `same`: the `Pointer, Interface` recursion → `return true` | CAUGHT | CAUGHT |
| `same`: the same for a pointer only | CAUGHT | CAUGHT |
| `same`: the same for a pointer to a struct with an exported field | **SURVIVED** | CAUGHT |
| `same`: the map-length comparison dropped | **SURVIVED** | CAUGHT |
| `same`: the slice-length comparison dropped (control) | CAUGHT | CAUGHT |
| `same`: budget exhaustion answers `false` | CAUGHT | CAUGHT |
| both walks run on `codecGraphNodes` instead of `valueWalkNodes` | CAUGHT | CAUGHT |
| `besideIt` counts a `json:"-"` field | **SURVIVED** | CAUGHT |
| `promotedMarshaller` stops comparing the write route | **SURVIVED** | CAUGHT |
| an array element loses the position's addressability | **SURVIVED** | CAUGHT |

Four of GAP-T35's five arms were closed by fixtures the section gained after the finding was
written, and the finding is stale about them: the `default:` arm is driven for an integer by
`slippingCodec` and for a string by `unnotedCodec`, and the pointer arm by `ledgered`'s `*big.Int`.
The row above that still survived says what was left: the pointer arm is held **only** by the wire
fallback, so a pointer to a struct the walk could have compared was decided by nothing. That is
what the new pointer row closes.

**GAP-T34 — closed by three refused fixtures, one per arm, each with an allocating control.**

`event/roundtrip_test.go` gains `chunked` (`[][]byte`), `parted` (`map[string][]byte`) and
`pocketed` (`*bodied`) — a payload whose shared memory is an element of a slice, a value of a map
and a field behind a pointer, which is what a compact binary format decodes into and what no
fixture in the file reached before: every one of the nine older cases reaches its bytes through one
exported field holding a slice or a map directly, so the top-level pointer comparison and the
struct field walk decided all of them. Each of the three has two codecs over one wire format — a
count of frames, then a length-prefixed frame each — one filling a `[64]byte` of its own from the
front on every decode and never clearing it, one cloning. The zero value of each encodes as a count
of nought whose decode writes nothing, so the re-encode arm that disturbs a reused buffer cannot
decide any of them and the walk over the two answers is the whole verdict.

The subtest is *memory shared one hop in is found through a slice element, a map value and a
pointer*. It asserts `ErrPayload` **and** the substring `decoded into memory its codec reuses`, so
a fidelity refusal cannot be mistaken for the aliasing one, and asserts the cloning codec of the
same format passes as the control. Each of the three arms deleted alone turns exactly that subtest
red.

**GAP-T35 — closed by four refused rows and one accepted one.**

`event/roundtrip_test.go` gains *a scalar changed, a map entry dropped and a value behind a pointer
are each a difference*: four codecs that keep the shape and change one ordinary thing, each beside
the same payload through a faithful codec of the same wire format.

- `driftingCodec` writes `Minor+1` and reads it back — the unit mix-up that writes minor units and
  reads major. Decided by the `default:` arm on an `int64`.
- `clippingCodec` clips `Reason` to three bytes — a string truncated to a column width. Decided by
  the same arm on a `string`.
- `droppingCodec` writes every entry but `"drop"` and reads back exactly what it wrote, so every
  entry that arrives is the entry it was given and `first.Len() == second.Len()` is the only thing
  that says one is missing.
- `slantingCodec` keeps the pointer and changes what it points at (`totalling{Held *totalled}`), so
  the `Pointer` recursion has to follow it — `totalled` has an exported field, so the wire fallback
  that holds `ledgered` never runs.

The budget clause's row is the accepting control in *a sample larger than the walk is refused rather
than reported as a pass*: `marksOf(codecGraphNodes + 8)` is asserted to **pass**, against the
constant and not derived from it, which is what says a sample's values are counted on a budget of
their own and not on the one that bounds a type graph.

**GAP-T37 — closed by three accepted rows, the mirror of three refusals that had a control in the
refusing direction only.**

`event/declaration_test.go` gains `muted` (`money` embedded, `Note string \`json:"-"\`` beside it),
`remitted` (a struct declaring its own `MarshalJSON`/`UnmarshalJSON` pair and embedding `summed`,
which declares none, with `Note string` beside it), and the row
`JSON[struct{ Held [2]posted }]().CanEncode()` — a pointer-receiver pair held in an array at an
addressable position, whose refused mirror `map[string][2]posted` was already there. All three are
shapes the shipped codec encodes and decodes; each of the three over-broad repairs turns exactly
one of them into an `ErrCodecType` panic out of `Declare`, which is a process that will not boot,
and each is now red on that mutation.

**Docs updated in the same change.** `docs/ai/decisions/D-124` — *Proven by* now names the two new
`RoundTrip` subtests and what each arm they hold is, and the three new accepting rows beside the
five controls already listed, saying which over-broad repair each catches. `docs/ai/flows/FL-036`
names both test functions already and no file or symbol moved, so its file table and the reverse
index in `docs/ai/flows/Index.md` are unchanged. No caller-visible behaviour changed, so
`docs/modules/{en,ru}/event.md`, the use-case pages and `EVENTSOURCE_P1_PLAN.md`'s contracts needed
no edit.

**Gates.** `gofmt -l .` silent, `go build ./...` exit 0, `go vet ./event/...` exit 0,
`go test -race -count=1 ./event/...` green (`event` 5.350s, `eventmemory` 1.248s, `eventtest`
2.547s), `make unit` exit 0, `make check` green through all nine checks.

GAP-T38, GAP-T39, GAP-T40 and GAP-T50 … GAP-T52 are untouched here and stay as their own rounds
left them.

---

## Round 9 — clean-context re-audit of the GAP-T34 / GAP-T35 / GAP-T37 cluster (test reviewer) — 2026-09-08

**What was audited.** The round-8 dispositions for GAP-T34, GAP-T35 and GAP-T37, against
`event/{comparison,fact,routing,encodable}.go` and `event/{roundtrip,declaration}_test.go`, the
plan's §S2 and §Contracts,
`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` §UC-042/§UC-015/§INV-021/§INV-023, and
`~/.claude/skills/econv/references/{gaps,restrictions,universality,architecture,building-blocks,data-integrity,microkernel,readability}.md`.
Every verdict below was measured in this worktree; nothing was taken from a disposition note.

**Gates, run here.** `gofmt -l .` silent. `go vet ./event/...` exit 0. `go test -race -count=2
./event/...` green — `event` 6.000s, `eventmemory` 1.495s, `eventtest` 4.061s, 6.4s wall.
`go test -race -count=1 -shuffle=on ./event/...` green, no order dependence, no flake. After the
whole mutation campaign, `event/{comparison,routing,encodable,fact}.go` are byte-identical to the
pre-campaign copies (`diff -q`, four files, all identical), so the tree is exactly as it was found.

**Microkernel: PASSES, derived rather than inherited.** A store written entirely outside the
module (`/tmp/eprobe2/kernel`, a throwaway module with a `replace` onto this checkout — nothing in
the repository was edited) implements `event.Store` and constructs every value the contract asks of
it: `event.NewBacking`, the zero `event.Authority` as *nothing of mine is bound*, `Capabilities`,
`Limits` sized through the exported `event.ResidentPage`, `Envelope` field by field, `Cursor` as a
plain string, and refusals through `event.Failure(event.Conflict, …)` / `event.Failure(
event.BadCursor, …)`. `event.Open`/`event.Bind`/`Repo.Load`/`Repo.Append`/`event.Read` drive it end
to end and it answers `state={Total:250}` after one append and one read. No unexported value is
needed anywhere, so `event/eventpg` remains writable with zero diffs under `event/`.

**The three findings of this cluster are genuinely closed.** All thirteen mutations the round-8
table names were reproduced against the real implementation, one at a time, each with
`go test -count=1 ./event/...` and a byte-for-byte restore afterwards. Every one now **fails**, and
each fails in the subtest the disposition claims:

| Mutation | Verdict | Killed by |
|---|---|---|
| `shares`: the `Pointer, Interface` arm → `return false` | CAUGHT | *memory shared one hop in …* |
| `shares`: the slice/array element walk deleted | CAUGHT | *memory shared one hop in …* |
| `shares`: the map entry walk deleted | CAUGHT | *memory shared one hop in …* |
| `shares`: the struct field walk deleted (control) | CAUGHT | three subtests |
| `same`: the `default:` arm answers `true` | CAUGHT | *a scalar changed …* + five more |
| `same`: the `Pointer, Interface` recursion → `return true` | CAUGHT | *a scalar changed …* |
| `same`: a **pointer to a struct** answers `true` (the narrow survivor of round 8) | CAUGHT | *a scalar changed …* |
| `same`: the map-length comparison dropped | CAUGHT | *a scalar changed …* |
| `same`: the slice-length comparison dropped (control) | CAUGHT | *a codec that records less …* |
| `same`: budget exhaustion answers `false` | CAUGHT | three subtests |
| both walks on `codecGraphNodes` instead of `valueWalkNodes` | CAUGHT | *a sample larger than the walk …* |
| `besideIt` counts a `json:"-"` field | CAUGHT | `TestADeclaration` (the `muted` row) |
| `promotedMarshaller` stops comparing the write route | CAUGHT | `TestADeclaration` (the `remitted` row) |
| an array element loses the position's addressability | CAUGHT | `TestADeclaration` (the `[2]posted` row) |

The fixtures behind them hold up on inspection: each of the six new refusing cases carries an
allocating or faithful control of the same wire format, the aliasing rows assert the substring
`decoded into memory its codec reuses` rather than the bare sentinel so a fidelity refusal cannot
be mistaken for an aliasing one, no sample is shared mutably across rows, and the budget row pins
`valueWalkNodes` against `codecGraphNodes` in both directions. The three new accepted rows were
checked out of tree to be shapes the shipped codec really does round-trip (`remitted` and
`struct{ Held [2]posted }` both answer `RoundTrip=<nil>`), so none of them is a control that passes
by blessing a shape that loses data.

**Checked and not a finding.** `muted` is on the accepted table and a sample of it carrying a
populated `Note` is refused by `Fact.RoundTrip` — but that asymmetry is pre-existing and not
introduced here: `dropped` (`First`/`Second` both `json:"-"`) has been on that table since round 1
and behaves identically, measured. The CanEncode door asks whether the shape loses data the
developer did not ask to lose; the RoundTrip door compares exported fields without reading tags.

**Sixteen further mutations of my own, in the same files, that no finding named.** Twelve died.
The four survivors are below, and three of them are one hole.

### GAP-T53 [critical][immediate] The value walk's *absence* arms are unpinned: a codec that reads a map entry back under another name, one that changes a map value, and one that drops a pointer field are each refused by the implementation and by no test

- **Where:** `event/comparison.go:248-255` (`sameEntries`, the entry comparison at `:250`),
  `event/comparison.go:167-169` (the invalid-value arm of `same`), `event/comparison.go:180-184`
  (the nil clause of the `Pointer, Interface` arm); the test that is supposed to hold them,
  `event/roundtrip_test.go:1126-1156` — the four-row subtest round 8 added, plus
  `event/roundtrip_test.go:1215-1236`
- **What:** three survivors, each applied alone to the real implementation and re-run with
  `go test -count=1 ./event/...`:

  | Mutation | Verdict |
  |---|---|
  | `sameEntries`: `this.same(first.MapIndex(key), second.MapIndex(key))` → `…, first.MapIndex(key))` | **SURVIVED** |
  | `same`: `if !first.IsValid() \|\| !second.IsValid() { return first.IsValid() == second.IsValid() }` → `return true` | **SURVIVED** |
  | `same`: `if first.IsNil() \|\| second.IsNil() { return first.IsNil() == second.IsNil() }` → `return true` | **SURVIVED** |

  Measured out of tree (`/tmp/eprobe2`, a `replace` onto this checkout — nothing in the repository
  was edited), the implementation **does** catch all three, so the arms are live and load-bearing:

  ```
  reads every value back multiplied by 100   → ErrPayload "does not read back the main.Counted"
  reads every key back as key+"!"            → ErrPayload, same
  keeps the note, reads the pointer as nil   → ErrPayload "does not read back the main.Referring"
  ```

  The round-8 subtest closed the **map-length** check (`droppingCodec` drops an entry, so
  `first.Len() == second.Len()` is what decides it) and the **pointer recursion** (`slantingCodec`
  keeps the pointer and changes what it points at). Neither reaches these three. `droppingCodec`
  short-circuits at `&&` and never enters `sameEntries`' body for a refusal at all — the only
  payload that runs that body to a verdict is the passing control. No fixture anywhere makes a
  codec read back a *different set* of things rather than a different value in the same place, and
  `unnotedCodec` (`roundtrip_test.go:480-491`) drops the `Note` **beside** an already-unset pointer
  rather than dropping a pointer that was set.
- **Why this severity:** `restrictions.md`, "tests that pass on a gutted implementation", on the
  same helper the cluster is about. These are not exotic codecs. A map read back under another key
  is what a codec that lower-cases, trims or prefixes its keys does — the ordinary normalisation
  someone adds for a database column. A map value read back changed is the unit mix-up §UC-042
  exists to catch, one level in from the scalar the round-8 subtest does pin. A pointer field read
  back as `nil` is what every codec that forgets an optional field produces, and it is the single
  most common way a JSON schema drifts. For all three, `Fact.RoundTrip` reports a pass, the fact is
  recorded, and §UC-042's "a codec that drops part of what it was given … found before a stream
  contains one" is a sentence with nothing running it.
- **Why this timing:** S5 exports this comparison as `eventtest.RoundTrip`, the proxy every store
  implementer is told to run, and §UC-042's **Observed** is the message it produces. Three arms of
  it that can answer `true` unconditionally are three promises the conformance suite cannot keep,
  and S5's defect fixtures will be written against whatever S2 established. It is the same class as
  GAP-T35, one door over, in the same function.
- **Close criteria:**
  - [ ] A refused fixture whose codec reads every entry back under a **different key**, the entry
        count unchanged, asserted `ErrPayload` with the `does not read back` substring, with the
        same wire format read back faithfully beside it as the control. The invalid-value arm
        answering `true` turns it red.
  - [ ] A refused fixture whose codec reads a map **value** back changed, every key and the count
        unchanged, so `sameEntries` comparing `first` with itself turns it red.
  - [ ] A refused fixture whose codec reads a **set** pointer field back as `nil` while keeping the
        field beside it, so the nil clause answering `true` turns it red; the existing
        `referring{Ref: nil}` case is already the accepting control for the other direction.
- **Status:** closed — round 10 dispositions

### GAP-T54 [medium][immediate] `sameNumber`'s exactness guard — the clause that makes "a number too large for a float to hold is a difference like any other" true — is asserted by nothing

- **Where:** `event/comparison.go:208-220` (`asFloat`, the integer clause at `:210-212` and the
  unsigned one at `:213-215`); the comment that states the rule,
  `event/comparison.go:197-201`; the table that is supposed to hold it,
  `event/roundtrip_test.go:1401-1431`
- **What:** `return float64(held), int64(float64(held)) == held` → `return float64(held), true`
  **SURVIVED** the full suite. Measured out of tree, the implementation catches it: a payload
  `boxed{Meta: int64(1<<62 + 1)}` read back as `float64(1<<62)` answers `ErrPayload "does not read
  back the main.Boxed it was given"` today and is silently accepted with the guard gone. The
  ten-row substitution table drives the widening rule at `1` versus `float64(1)` and at `1` versus
  `float64(2)` and nowhere near the boundary where a float stops holding an integer exactly.
- **Why this severity:** the widening exception is the one place the comparison deliberately calls
  two values of *different types* the same value, and it is the crack every other row of that table
  is there to keep narrow. Its own comment says "a number too large for that to hold is a
  difference like any other"; nothing makes that sentence true. The concrete loss is an identifier
  or an amount above 2^53 recorded as an `int64` behind an interface and read back rounded — the
  ordinary shape of a Snowflake id or a satoshi balance — passing the helper that exists to say the
  codec kept what it was handed.
- **Why this timing:** it is a boundary value of a public contract S5 exports as
  `eventtest.RoundTrip`, and `universality.md`'s rule about assertions that fit only the sample
  applies: `1` and `2` are the sample, and the rule is about magnitudes.
- **Close criteria:**
  - [ ] Two rows on the substitution table: an `int64` above 2^53 read back as the `float64` nearest
        to it, asserted refused; and an `int64` a float holds exactly (`1 << 52`) read back as that
        float, asserted accepted, so the row above is not bought by refusing every large number.
  - [ ] Dropping either exactness clause in `asFloat` turns the first row red.
- **Status:** closed — round 10 dispositions

### GAP-T55 [low][deferred] `valueWalk.spend`'s carried-out short-circuit can be deleted, so which of two refusals a payload with two problems reports is decided by nothing

- **Where:** `event/comparison.go:76-86`, the guard at `:77-79`
- **What:** deleting `if this.beyond || this.panicked != "" || this.unrecordable != "" ||
  this.opaque != "" { return false }` **SURVIVED**. That clause is what makes the *first*
  carried-out condition the one the caller is told about: today a payload holding a func beside a
  field the codec loses answers `ErrSample … record what identifies the behaviour — a name, a code`
  (measured out of tree); without it the walk keeps going, finds the lost field, and answers
  `ErrPayload … does not read back`, which is true and names no repair the caller can make for the
  func. No fixture in the file carries two carried-out conditions at once.
- **Why this severity:** low — both answers are refusals and neither is silent data loss. What is
  unpinned is which message a caller sees, and §UC-042's **Observed** is the message.
- **Why this timing:** deferred. It is one function's internal ordering with no contract another
  section reads, and the section already has the diagnosis table that would host the row.
- **Close criteria:**
  - [ ] One row on the diagnosis table: a payload holding a func **and** a field the codec drops,
        asserted `ErrSample` and the `record what identifies the behaviour` substring, so the
        short-circuit is what decides it.
  - [ ] Deleting the guard at `comparison.go:77-79` turns that row red.
- **Status:** closed — round 10 dispositions

### Mutation log — round 9, complete

Each applied alone to the real implementation, `go test -count=1 ./event/...`, file restored
byte-for-byte afterwards. Thirteen named by the cluster, sixteen mine.

| # | Mutation | File | Verdict |
|---|---|---|---|
| 1 | `shares`: `Pointer, Interface` arm → `return false` | comparison.go | CAUGHT |
| 2 | `shares`: slice/array element walk deleted | comparison.go | CAUGHT |
| 3 | `shares`: map entry walk deleted | comparison.go | CAUGHT |
| 4 | `shares`: struct field walk deleted (control) | comparison.go | CAUGHT |
| 5 | `same`: `default:` arm → `return true` | comparison.go | CAUGHT |
| 6 | `same`: `Pointer, Interface` recursion → `return true` | comparison.go | CAUGHT |
| 7 | `same`: a pointer to a struct answers `true` | comparison.go | CAUGHT |
| 8 | `same`: map-length comparison dropped | comparison.go | CAUGHT |
| 9 | `same`: slice-length comparison dropped (control) | comparison.go | CAUGHT |
| 10 | `same`: budget exhaustion → `return false` | comparison.go | CAUGHT |
| 11 | both walks on `codecGraphNodes` | fact.go | CAUGHT |
| 12 | `besideIt` counts a `json:"-"` field | routing.go | CAUGHT |
| 13 | `promotedMarshaller` stops comparing the write route | routing.go | CAUGHT |
| 14 | array element loses the position's addressability | encodable.go | CAUGHT |
| 15 | `sameEntries` compares `first` with itself | comparison.go | **SURVIVED** — GAP-T53 |
| 16 | `same`: the invalid-value arm answers `true` | comparison.go | **SURVIVED** — GAP-T53 |
| 17 | `same`: the nil clause answers `true` | comparison.go | **SURVIVED** — GAP-T53 |
| 18 | `asFloat`: the integer exactness guard dropped | comparison.go | **SURVIVED** — GAP-T54 |
| 19 | `spend`: the carried-out guard dropped | comparison.go | **SURVIVED** — GAP-T55 |
| 20 | `shares`: struct arm drops the `IsExported` filter | comparison.go | CAUGHT |
| 21 | `shares`: the empty-slice/map length guard dropped | comparison.go | CAUGHT |
| 22 | `shares`: the same-type guard dropped | comparison.go | CAUGHT |
| 23 | `shares`: map arm compares `first` with itself | comparison.go | CAUGHT |
| 24 | `shares`: slice arm compares `first` with itself | comparison.go | CAUGHT |
| 25 | `sameElements` compares `first` with itself | comparison.go | CAUGHT |
| 26 | `sameFields`: `compared` set unconditionally | comparison.go | CAUGHT |
| 27 | `sameEntries` walks `second`'s keys (behaviour-preserving under the length guard) | comparison.go | SURVIVED, not a finding |
| 28 | `sameNumber` answers `true` | comparison.go | CAUGHT |
| 29 | `readsBackOnTheWire` answers `true` | comparison.go | CAUGHT |
| 30 | `singleValued` answers `false` | comparison.go | CAUGHT |
| 31 | `notAliased` never called | fact.go | CAUGHT |
| 32 | a struct field is always addressable | encodable.go | CAUGHT |
| 33 | `promotedMarshaller` never recurses past the first hop | routing.go | CAUGHT |
| 34 | `besideIt` returns the first field, embedded or not | routing.go | CAUGHT |

Detection rate: 30 of 32 behaviour-changing mutations caught (#27 is semantically equivalent under
the length guard and #4/#9 are the dispositions' own controls). All five survivors are in
`event/comparison.go`, and four of the five are in the value walk's *fidelity* half rather than its
aliasing half — the same side of the helper GAP-T35 was about.

**Verdict for the cluster.** GAP-T34, GAP-T35 and GAP-T37 are **closed**, verified by code rather
than by note. The cluster is not green: the same audit found GAP-T53 `[critical][immediate]`, a
hole of the same class, three arms wide, in the function GAP-T35 named.

---

## Round 10 dispositions — 2026-09-08 — GAP-T53, GAP-T54, GAP-T55

Tests and docs only. No file under `event/` outside `roundtrip_test.go` was edited, so
`event/eventpg`'s zero-diff obligation is untouched and no value a store must construct moved.
All three are **closed**, including GAP-T55, which round 9 graded `[deferred]`: it is one fixture
and one row in a subtest that already asserts the sentinel, and deferring one row costs more to
carry than to write.

**Reproduced first, in this tree.** Six mutations, each applied alone to the real implementation of
`event/comparison.go`, `go test -count=1 ./event/...` run, the file restored byte-for-byte after
each and confirmed identical by `diff -q` at the end.

| Mutation | Before | After | Killed by |
|---|---|---|---|
| `sameEntries`: `this.same(first.MapIndex(key), second.MapIndex(key))` → `…, first.MapIndex(key))` | **SURVIVED** | CAUGHT | *a value read back somewhere else, or not at all, is a difference too* |
| `same`: the invalid-value arm → `return true` | **SURVIVED** | CAUGHT | the same subtest |
| `same`: the nil clause of the `Pointer, Interface` arm → `return true` | **SURVIVED** | CAUGHT | the same subtest |
| `asFloat`: `int64(float64(held)) == held` → `true` | **SURVIVED** | CAUGHT | *a value substituted for another behind an interface is answered, not walked* |
| `asFloat`: `uint64(float64(held)) == held` → `true` | **SURVIVED** | CAUGHT | the same subtest |
| `spend`: the carried-out guard deleted | **SURVIVED** | CAUGHT | *a leaf no wire format records is refused rather than reported as a pass* |

**GAP-T53 — closed by three refused fixtures with three faithful controls, in a subtest of their
own.**

`event/roundtrip_test.go` gains *a value read back somewhere else, or not at all, is a difference
too*, three codecs that keep the count and lose the place. Each is beside a codec that reads the
same wire format back where it stood, and each row asserts `ErrPayload` **and** the substring
`does not read back`, so an aliasing refusal cannot be mistaken for the fidelity one.

- `rekeyingCodec` reads every entry back under an upper-cased key — the normalisation somebody adds
  for a database column. The entry count is unchanged, so the length check decides nothing;
  `second.MapIndex(key)` is invalid for a key the sample never had, and it is the invalid-value arm
  that turns that into a difference. This row is red on **both** the entry-comparison mutation and
  the invalid-arm mutation.
- `inflatingCodec` reads every amount back multiplied by a hundred, every key and the count
  unchanged — §UC-042's unit mix-up one level in from the scalar the round-8 subtest pinned.
  Verified to be the killing row on its own: with the `rekeying` row removed from the table, the
  entry-comparison mutation is still red, and the message names the multiplied amount.
- `unreferringCodec` keeps the note and reads the set `*target` back as `nil` — the codec that
  forgets an optional field. The `referring{Ref: nil}` case at *a pointer field is not asked its own
  type's `Equal` through a nil* is the accepting control for the other direction and was already
  there; the faithful `JSON[referring]()` row beside this one is the control for this direction.

Round 9's close criteria are met as written. The `droppingCodec` gap the finding named — a refusal
that short-circuits at `&&` and never enters `sameEntries`' body — is what these three enter.

**GAP-T54 — closed by three rows on the substitution table, two refused and one accepted.**

The ten-row table in *a value substituted for another behind an interface is answered, not walked*
is now thirteen. `int64(1)<<62 + 1` read back as `float64(int64(1)<<62 + 1)` is asserted **refused**,
`uint64(1)<<63 + 1` the same way, and `int64(1) << 52` read back as that float is asserted
**accepted**. The refused pair is what makes the comment's *"a number too large for that to hold is
a difference like any other"* true — one row per exactness clause, because an `int64` row cannot be
turned red by dropping the unsigned clause. The accepted row is the control in the other direction,
and it is not decorative: an over-strict guard (`… && held < 1<<20`) turns exactly that row red and
leaves the two refused rows green.

**GAP-T55 — closed by one row beside the func case, not deferred.**

`unroutedCodec` is `routingCodec` losing the note as well, so `routed` carries two of the walk's
carried-out conditions at once — a `func` it cannot record and a field the codec dropped. The row
sits in *a leaf no wire format records is refused rather than reported as a pass*, which already
asserts the sentinel, and demands `ErrSample` **and** `record what identifies the behaviour`.
Deleting the guard at `comparison.go:77-79` makes the walk keep going, find the lost note and answer
`ErrPayload … does not read back`, and the row is red on exactly that. Round 9's criterion named the
diagnosis table as the host; that table asserts a substring and never a sentinel, so the row was put
where both halves of the criterion can be asserted. The existing `routes` row (a func alone →
`ErrSample`) and the `truncatingCodec` row (a dropped field alone → `ErrPayload`) are the two
one-condition controls that say the new row is about the order and not about either condition.

**Three over-refusal controls, run to prove the new rows are not vacuous in the accepting
direction.** `asFloat` made to refuse any integer above 2^20 → the `1 << 52` row red. `same` made to
answer `false` for every map → four subtests red including the new one's faithful column. `same`
made to answer `false` for every pointer → six subtests red including the new one's. And the
`pipes` row was checked to be still reached past the new GAP-T55 row: the mutation it exists for
(the default arm refusing every leaf) still fails at *a channel read back as another channel*, so
nothing was masked by inserting a row above it.

**Docs updated in the same change.** The boundary of the widening exception is behaviour a caller
sees — an id above 2^53 is now demonstrably `ErrPayload` — so `docs/modules/en/event.md` and
`docs/modules/ru/event.md` state it on the interface bullet, `docs/ai/decisions/D-124` states it in
the numeric-exception paragraph and names the three new accepting rows in *Proven by*, and
`EVENTSOURCE_P1_PLAN.md`'s clause 6e records it. D-124's *Proven by* also names the new subtest and
the new GAP-T55 row, and its stale "the seven subtests that hold the comparison itself" is corrected
to the nine the list actually holds. `FL-036` names test functions rather than subtests and no file
or symbol moved, so its file table and the reverse index in `docs/ai/flows/Index.md` are unchanged.
No contract in the plan changed — no implementation line was edited.

**Gates.** `gofmt -l .` silent, `go build ./...` exit 0, `go vet ./event/...` exit 0,
`go test -race -count=1 ./event/...` green, `go test -race -count=1 -shuffle=on ./event/...` green,
`make unit` exit 0, `make check` green through all nine checks.

GAP-T38, GAP-T39, GAP-T40 and GAP-T50 … GAP-T52 are untouched here and stay as their own rounds
left them.

---

## Round 11 — clean-context re-audit of the GAP-T34 / GAP-T35 / GAP-T37 cluster (test reviewer) — 2026-09-08

**What was audited.** The round-8 dispositions for GAP-T34, GAP-T35 and GAP-T37 and the round-10
dispositions for GAP-T53, GAP-T54 and GAP-T55, against `event/{comparison,fact,routing,encodable}.go`
and `event/{roundtrip,declaration}_test.go`, `.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md`
§UC-042/§INV-021/§INV-023, `EVENTSOURCE_P1_PLAN.md` §S2 and its architecture metrics,
`docs/ai/decisions/D-124`, and
`~/.claude/skills/econv/references/{gaps,restrictions,universality,architecture,building-blocks,data-integrity,microkernel,readability}.md`.
Every verdict below was measured in this worktree. Nothing was taken from a disposition note.

**Gates, run here.** `gofmt -l .` silent. `go vet ./event/...` exit 0. `go test -race -count=2
./event/...` green — `event` 5.993s, `eventmemory` 1.492s, `eventtest` 4.026s, **6.4s wall**.
`go test -race -count=1 -shuffle=on ./event/...` green: no order dependence, no flake, no
`t.Parallel` anywhere under `event/`. After the campaign, `event/{comparison,fact,routing,
encodable}.go` and `event/roundtrip_test.go` are byte-identical to the pre-campaign copies
(`diff -q`), so the tree is exactly as it was found.

**Microkernel: PASSES, derived and not inherited.** A store written entirely outside the module
(`/tmp/eaudit/probe/kernel`, a throwaway module with a `replace` onto this checkout — nothing in the
repository was edited) satisfies `event.Store` using only exported values: `event.NewBacking` over
its own handle, the zero `event.Authority` as *nothing of mine is bound*, `Capabilities` with the
four `Support` constants, `Limits` sized through the exported `event.ResidentPage`, `Envelope` field
by field, `Cursor` as a plain string, and refusals through `event.Failure(event.Conflict, …)` and
`event.Failure(event.BadCursor, …)`. `event.Open` / `event.Bind` / `Repo.Load` / `Repo.Append` /
`event.Read` drive it end to end and it answers `state={Total:250}` after one append and one read,
`read 1 envelope(s) cursor="1"`. No unexported value is needed anywhere, so `event/eventpg` remains
writable with **zero diffs** under `event/`.

**The six findings under audit are genuinely closed, verified against the code.** All nineteen
mutations the round-8 and round-10 tables name were reproduced against the real implementation, one
at a time, `go test -count=1 ./event/...` each, with a byte-for-byte restore afterwards. Every one
now fails, in the subtest the disposition claims:

| Mutation | Verdict | Killed by |
|---|---|---|
| `shares`: the `Pointer, Interface` arm → `return false` | CAUGHT | *memory shared one hop in …* |
| `shares`: the slice/array element walk deleted | CAUGHT | *memory shared one hop in …* |
| `shares`: the map entry walk deleted | CAUGHT | *memory shared one hop in …* |
| `shares`: the struct field walk deleted (control) | CAUGHT | three subtests |
| `same`: the `default:` arm → `return true` | CAUGHT | *a scalar changed …* + four more |
| `same`: the `Pointer, Interface` recursion → `return true` | CAUGHT | *a scalar changed …* |
| `same`: a pointer to a struct answers `true` | CAUGHT | *a scalar changed …* |
| `same`: the map-length comparison dropped | CAUGHT | *a scalar changed …* |
| `same`: the slice-length comparison dropped (control) | CAUGHT | *a codec that records less …* |
| `same`: budget exhaustion → `return false` | CAUGHT | three subtests |
| both walks on `codecGraphNodes` | CAUGHT | *a sample larger than the walk …* |
| `besideIt` counts a `json:"-"` field | CAUGHT | `TestADeclaration` (the `muted` row) |
| `promotedMarshaller` stops comparing the write route | CAUGHT | `TestADeclaration` (`remitted`) |
| an array element loses the position's addressability | CAUGHT | `TestADeclaration` (`[2]posted`) |
| `sameEntries` compares `first` with itself | CAUGHT | *a value read back somewhere else …* |
| `same`: the invalid-value arm → `return true` | CAUGHT | the same subtest |
| `same`: the nil clause → `return true` | CAUGHT | the same subtest |
| `asFloat`: the signed exactness guard → `true` | CAUGHT | *a value substituted for another …* |
| `asFloat`: the unsigned exactness guard → `true` | CAUGHT | the same subtest |
| `spend`: the carried-out guard deleted | CAUGHT | *a leaf no wire format records …* |

Round 10's strongest claim was re-derived rather than believed: with the `rekeying` row **deleted
from the table** and the entry-comparison mutation applied, the subtest is still red and the message
names *every amount multiplied, every key and the count unchanged* — so `inflatingCodec` is
load-bearing on its own and is not carried by the row above it. The test file was restored and
re-run green afterwards. The three accepting rows GAP-T37 added were checked out of tree to be
shapes the shipped codec really does round-trip, and the substitution table's boundary rows are
written as expressions (`int64(1)<<62 + 1`, `int64(1) << 52`, `marksOf(codecGraphNodes + 8)`) rather
than as literals fitted to one sample — `universality.md` holds here.

**Fifty further mutations of my own, in the same four files, that no finding named.** Thirty-nine
died. The survivors are below. Five are findings, four are equivalent or unreachable, and one probe
that started as a mutation found a live defect the suite cannot see at all.

### GAP-T56 [critical][immediate] A codec that reuses a decode buffer it never clears, reached through an unexported field, passes `RoundTrip` — obligation 2's own shape, answered `<nil>`

- **Where:** `event/comparison.go:17-38` (`reusesItsBuffer`, the two arms); the fixture set that is
  supposed to cross them, `event/roundtrip_test.go:23-42` (`scratchCodec`), `:49-63`
  (`prefixCodec`), `:70-100` (`sealedNote`, `sealedCodec`); the subtest,
  `event/roundtrip_test.go:1108-1122`; the claim, `docs/ai/decisions/D-124` §*The codec as a party*
  obligation 2 and §*Proven by*
- **What:** the aliasing half has two arms and four codec shapes to cover, and the fixture set
  covers three cells of the four:

  | | zeroes its buffer on every decode | fills it and never clears it |
  |---|---|---|
  | the bytes are reachable through an **exported** field | `scratchCodec` — caught by the walk | `prefixCodec` — caught by the walk |
  | the bytes are reachable only through an **unexported** field | `sealedCodec` — caught by the re-encode probe | **no fixture — and not caught** |

  Measured out of tree (`/tmp/eaudit/probe/crossed`, a `replace` onto this checkout — nothing in the
  repository was edited), a codec in the fourth cell — `sealedPrefixCodec`, which is `prefixCodec`'s
  body returning `sealedNote{body: this.buffer[:width]}` and an allocating `Encode`:

  ```
  RoundTrip answers: <nil>
  the application holds "twelve"
  after the next payload it holds "zzzzzz"
  ```

  Both arms miss it and neither can see it: `shares` walks the exported half only and there is none,
  and the re-encode probe decodes the reader type's **zero value**, whose encoding is one byte of
  width nought, so it copies nothing into the buffer and the first answer is undisturbed. The
  fidelity walk then compares an intact value with the sample and agrees. `sealedCodec` is caught
  only because it writes `this.scratch = [32]byte{}` on every decode, which the ordinary
  implementation does not — the test file says so itself, at `roundtrip_test.go:44-48`, about
  `prefixCodec`, and never crosses that observation with the unexported case one fixture below it.
- **Why this severity:** `D-124` states obligation 2 as *"It must **not** return one that aliases
  memory the codec itself will write or reuse … `Fact.RoundTrip` is the runnable proxy and refuses a
  codec that does it"*, without qualification, and §UC-042's helper is what a store implementer and
  an application are told to run. Here the proxy reports a pass for a codec that hands the
  application memory it rewrites on the next decode: every fact folded from that stream holds bytes
  belonging to whichever payload was read last. A zero-copy reader over a private scratch buffer is
  the ordinary shape of exactly the compact binary codec `event/eventpg` exists to make attractive,
  and it is the one shape the whole obligation was written for.
- **Why this timing:** S5 exports this as `eventtest.RoundTrip` (`event/eventtest/proxies.go:16`),
  the proxy phase 2's store implementer runs, and D-124 is binding law that currently claims more
  than the code does. Either the proxy covers the fourth cell or the decision must say it does not;
  both are cheaper before `event/eventpg` is written against the claim.
- **Close criteria:**
  - [ ] A fixture in the fourth cell — no exported field, a decode buffer filled to the payload's
        width and never cleared — asserted `ErrPayload` **and** the substring `decoded into memory
        its codec reuses`, with the same wire format over an allocating codec beside it as the
        control.
  - [ ] `Fact.RoundTrip` refuses it. If the kernel cannot, `D-124` obligation 2 and its *Proven by*
        say which shapes the proxy does not reach, `docs/modules/{en,ru}/event.md` say it where a
        consumer reads it, and the fixture above is asserted to pass with a comment naming the
        limitation — a stated limit is acceptable, an unstated one is not.
  - [ ] The three cells that are covered stay covered: deleting either arm of `reusesItsBuffer`
        turns a test red.
- **Status:** open

### GAP-T57 [high][immediate] `besideIt`'s second clause is unpinned: with `readAsJSON` replaced by `IsExported`, a struct embedding a marshaller beside an embedded unexported struct type is accepted, and every field it promotes is lost

- **Where:** `event/routing.go:149-158` (`besideIt`, the condition at `:152`);
  `event/encodable.go:202-205` (`readAsJSON`, what the clause defers to); the accepted table,
  `event/declaration_test.go:759-801`
- **What:** `if index == embedded || field.Tag.Get("json") == "-" || !readAsJSON(field)` →
  `… || !field.IsExported()` **SURVIVED** the whole suite. Measured out of tree, the clause is live
  and decides a real shape:

  ```
  today:            struct{ money; secret } → ErrCodecType "… so secret is written by nobody and
                    read back by nobody; name the embedded field instead of promoting it"
  with the mutant:  struct{ money; secret } → <nil>
  encoding/json:    wrapped{money{500}, secret{Code:"abc"}} encodes as 500
  ```

  `secret` is an unexported struct type embedded by value; encoding/json promotes and writes its
  exported fields, and `readAsJSON` is what says so. GAP-T37 closed the `json:"-"` clause of this
  same expression with the `muted` row. The clause beside it, in the same `if`, has no row at all.
- **Why this severity:** this is the ninth refused shape's own arm, and the failure it prevents is
  silent data loss at the one door that exists to prevent it: the fact is recorded as `500`, `Code`
  is written by nobody and read back by nobody, and no error appears at any door — the exact
  sentence D-124's refusal list is built out of. A mutation that reopens a data-loss shape and keeps
  the suite green is `restrictions.md`'s "tests that pass on a gutted implementation".
- **Why this timing:** it is one row on a table that already exists, and the refusal it protects is
  in D-124's enumerated list, which phase 2 and the module docs both cite.
- **Close criteria:**
  - [ ] A refused row for a struct embedding a marshalling type beside an **embedded unexported
        struct type**, asserting `ErrCodecType` and the substring naming the hidden field, so
        replacing `readAsJSON` with `IsExported` turns it red.
  - [ ] Its accepting mirror stays green — `itemised` (an embedded marshaller with nothing beside
        it) already is; the new row must not be closable by refusing every embedded struct.
- **Status:** open

### GAP-T58 [high][immediate] The fourth rule of the promotion loop has a control in the refusing direction only: dropping `!field.Anonymous` turns an ordinary struct that declares its own pair into a boot-time panic

- **Where:** `event/routing.go:84-111` (`promotedMarshaller`, the anonymity skip at `:87-89`); the
  accepted table, `event/declaration_test.go:759-801`, whose three GAP-T37 rows are at `:793-795`
- **What:** deleting `if !field.Anonymous { continue }` **SURVIVED** the whole suite. Measured out
  of tree:

  ```
  today:            struct{ Paid money; Note string } with its own MarshalJSON/UnmarshalJSON → <nil>
                    (and encoding/json round-trips it: [500,"n"] → {Paid:{Cents:500} Note:n})
  with the mutant:  ErrCodecType "main.receipt writes itself through the MarshalJSON of the embedded
                    main.money, so Note is written by nobody" — for a field that is not embedded
  ```

  GAP-T37 named three rules of this walk and round 8 gave each an accepting mirror: `muted` for the
  tag clause, `remitted` for the write-route comparison, `struct{ Held [2]posted }` for
  addressability. The loop has a fourth rule and it has no mirror. `remitted` does not reach it —
  the embedded `summed` declares no pair, so the route comparison rejects it one line earlier, and
  the anonymity skip is never what decides that row.
- **Why this severity:** the shape the over-broad repair refuses is not exotic. Any aggregate
  payload that declares its own JSON pair and holds a named field of a marshalling type — a named
  `time.Time`, a `money`, a `netip.Addr` — panics out of `Declare`, which is a process that will not
  boot, with a message that names an embedding the developer did not write. That is the failure mode
  GAP-T37 exists to make impossible, one rule over, in the same `for` body.
- **Why this timing:** same table, same subtest, and the same class this cluster was opened for. A
  repair to this walk is exactly what phase 2's first unfamiliar payload type will provoke.
- **Close criteria:**
  - [ ] An accepted row for a struct that declares its own `MarshalJSON`/`UnmarshalJSON` pair and
        holds a **named** field whose type declares one too, so deleting the anonymity skip turns it
        red.
  - [ ] `lined` stays refused, so the new row is not bought by accepting the promotion it mirrors.
- **Status:** open

### GAP-T59 [medium][immediate] The aliasing walk's bound is decided by nothing: both its budget guard and its carried-out report are deletable with the suite green

- **Where:** `event/comparison.go:109-142` (`shares`, the `spend()` guard at `:110`);
  `event/fact.go:178-188` (`notAliased`, the report at `:187`); the subtest that reads as if it
  covered them, `event/roundtrip_test.go:1381-1405`
- **What:** two survivors, each applied alone:

  | Mutation | Verdict |
  |---|---|
  | `shares`: `if !this.spend() \|\| !first.IsValid() …` → `if !first.IsValid() …` | **SURVIVED** |
  | `notAliased`: `return walk.unanswered(…)` → `return nil` | **SURVIVED** |

  Both walks are made on separate `valueWalk` values, and `readBack`'s is what answers *a sample
  larger than the walk is refused rather than reported as a pass* in every direction — so the
  aliasing walk's half of "both walks are bounded" (`event/fact.go:118-119`, and D-124's *Where it
  lives*) is asserted by nothing. Measured out of tree, the guard is live and load-bearing: a codec
  whose decoded value points at itself answers today
  `ErrSample: … was given a sample holding more than the 65536 values a round trip walks`, and with
  the guard gone the walk recurses until the process dies.
- **Why this severity:** a caller's codec producing a cyclic value is a mistake, not an attack, and
  the difference between a diagnosis naming the repair and a stack overflow in the test binary is
  the difference between a bug found and a morning lost. No data is lost either way, which is why
  this is medium and not high.
- **Why this timing:** `eventtest.RoundTrip` is what a store implementer runs, and S5's defect
  fixtures will be written against whatever S2 established about which walk answers what.
- **Close criteria:**
  - [ ] A refused fixture whose decoded value is cyclic, or larger than `valueWalkNodes` and
        reachable only through the aliasing walk, asserting `ErrSample` and the `holding more than`
        substring, so deleting `shares`'s `spend()` guard turns it red.
  - [ ] The same row, or one beside it, turns red when `notAliased` stops asking `unanswered`.
- **Status:** open

### GAP-T60 [medium][immediate] The sealed subtest asserts the bare sentinel, so which arm refused is decided by nothing — and `before = bytes.Clone(before)` is deletable with the suite green

- **Where:** `event/comparison.go:25-37` (`reusesItsBuffer`, the clone at `:29`);
  `event/roundtrip_test.go:1108-1122`, whose assertion is `!errors.Is(err, ErrPayload)` and nothing
  else
- **What:** two survivors in the same arm:

  | Mutation | Verdict |
  |---|---|
  | `before = bytes.Clone(before)` deleted | **SURVIVED** |
  | the fallback's verdict discarded (`return false, nil` with the probe's side effects left in place) | **SURVIVED** |

  Measured out of tree on `sealedCodec` restated verbatim, the refusal **changes arm** and the test
  cannot tell:

  ```
  today:        ErrPayload "… decoded into memory its codec reuses"
  clone gone:   ErrPayload "… does not read back the main.sealedNote it was given"
  ```

  The clone is what keeps the two encodings comparable when the codec reuses its **encode** buffer,
  which `sealedCodec` does. Without it, `before` and `after` are one slice, the aliasing arm always
  answers false, and the row stays green only because the probe's zero decode happens to zero the
  scratch array before the fidelity walk runs — an accident of that fixture, and the reason
  GAP-T56's fourth cell is invisible.
- **Why this severity:** the round-8 and round-10 rows learned this lesson and assert
  `decoded into memory its codec reuses` beside the sentinel; the row the arm was written for still
  asserts `ErrPayload` alone, which both refusals share. `restrictions.md`: an assertion two
  different mechanisms satisfy pins neither.
- **Why this timing:** it is one substring on an existing assertion, and it is the assertion that
  makes GAP-T56 measurable.
- **Close criteria:**
  - [ ] `event/roundtrip_test.go:1108-1122` asserts the `decoded into memory its codec reuses`
        substring as well as the sentinel, so deleting `before = bytes.Clone(before)` turns it red.
  - [ ] Discarding the fallback's verdict turns the same row red.
- **Status:** open

### GAP-T61 [low][deferred] `equalByMethod`'s argument-type clause is unpinned, so an over-broad repair turns a legal payload into an `ErrSample` blaming an `Equal` that never ran

- **Where:** `event/comparison.go:298-313` (the signature check at `:304`); the fixture that pins
  the arity but not the type, `event/roundtrip_test.go:435-455` (`weighed`)
- **What:** `if asked.NumIn() != 1 || asked.In(0) != first.Type() || …` → `if asked.NumIn() != 1 ||
  …` **SURVIVED**. `weighed` declares `Equal(first, second int64)`, so `NumIn() != 1` decides it and
  the argument-type clause is never what answers. Measured out of tree, an all-unexported struct
  declaring `Equal(other int64) bool` round-trips today (`<nil>`); with the clause dropped the call
  panics, is recovered, and the caller is told *carries a main.tally whose own Equal panicked when
  it was asked whether the sample came back* — about a method that was never asked.
- **Why this severity:** low. It is a false refusal in a narrow shape (no exported field, a
  one-argument `Equal` of another type), and it fails closed rather than losing data.
- **Why this timing:** deferred. One row on a table that already exists, with no contract another
  section reads.
- **Close criteria:**
  - [ ] A row whose payload type has no exported field and declares `Equal` with **one** argument of
        another type, asserted to round-trip, so dropping the `In(0)` clause turns it red.
- **Status:** open

### GAP-T62 [low][deferred] D-124's *Proven by* overstates what the ten rows pin, and the `sealedCodec` comment claims an arm it does not reach

- **Where:** `docs/ai/decisions/D-124…md:379-380` — *"Every one of the ten is red when the arm it
  names is neutralised, and no other case in the file is"*; `:326-328` — *"including the re-encode
  arm that reaches a reused buffer the value walk cannot see through"*;
  `event/roundtrip_test.go:64-68`
- **What:** measured, the first sentence is false in its second half: neutralising the `default:`
  arm — the arm the `driftingCodec` row names — turns **five** subtests red, not that row alone. The
  second overstates by GAP-T56: the re-encode arm reaches a reused buffer only where the reader
  type's zero value disturbs it, which is the fixture's spelling and not the ordinary one.
- **Why this severity:** low — a doc that claims more than the tests do, in a decision that is
  otherwise the most carefully written page in the section. `CLAUDE.md` treats a stale doc as a
  failing test, which is why it is written down rather than waved past.
- **Why this timing:** deferred; it moves with whatever closes GAP-T56 and GAP-T60.
- **Close criteria:**
  - [ ] The *Proven by* sentence states what is measurable — each row is red when its arm is
        neutralised — without the "and no other case" clause, or the clause is made true.
  - [ ] The `sealedCodec` comment says which spelling of a reused buffer the re-encode arm reaches.
- **Status:** open

### GAP-T63 [low][deferred] `TestACodecThatDecodesIntoAReusedBufferIsCaught` is 640 lines and 24 subtests, and its name states one of them

- **Where:** `event/roundtrip_test.go:1061-1700`
- **What:** the function now holds the aliasing half, the fidelity half, the substitution table, the
  walk budget, marker facts, the zero-value refusal, the retained-revision cases and the diagnosis
  table. Nineteen of the twenty-four subtests are about something other than a reused buffer.
  `readability.md`: a name that states the behaviour is the cheapest documentation there is, and this
  one now misdirects — a reader looking for where a substituted value behind an interface is decided
  has no reason to open a function named for buffer reuse.
- **Why this severity:** low. Nothing is untested and nothing is wrong; the cost is navigation, paid
  by every later round.
- **Why this timing:** deferred — a split is mechanical but touches every line of the file, and the
  cluster's open findings should land first.
- **Close criteria:**
  - [ ] The aliasing subtests and the fidelity subtests live in two functions whose names say which
        obligation each holds, with `D-124`'s *Proven by* and `FL-036` updated in the same change.
- **Status:** open

### Mutation log — round 11, complete

Each applied alone to the real implementation, `go test -count=1 ./event/...`, restored
byte-for-byte afterwards and confirmed identical by `diff -q` at the end. Nineteen reproduced from
the round-8 and round-10 tables (all CAUGHT, table above), fifty of my own below.

| # | Mutation | File | Verdict |
|---|---|---|---|
| 1 | `asFloat`: over-strict guard `&& held < 1<<20` (over-refusal control) | comparison.go | CAUGHT (the `1 << 52` row) |
| 2 | `same`: every map is a difference (over-refusal control) | comparison.go | CAUGHT (5 subtests) |
| 3 | `same`: every non-nil pointer is a difference (over-refusal control) | comparison.go | CAUGHT (6 subtests) |
| 4 | `shares`: the `second.Len() > 0` half of the empty guard dropped | comparison.go | SURVIVED — near-equivalent, see note |
| 5 | `singleValued`: a nil type answers `true` | comparison.go | SURVIVED — unreachable, see note |
| 6 | `sameFields`: unexported fields compared too | comparison.go | CAUGHT |
| 7 | `equalByMethod`: the whole signature check dropped | comparison.go | CAUGHT |
| 8 | `same`: two different types answered `true` rather than as numbers | comparison.go | CAUGHT |
| 9 | `asFloat`: the float arm answers "not a number" | comparison.go | CAUGHT |
| 10 | `sameOpaque`: the `==` arm answers `true` | comparison.go | CAUGHT |
| 11 | `sameOpaque`: the `singleValued` short-circuit dropped | comparison.go | CAUGHT |
| 12 | `reusesItsBuffer`: the decoder is handed the shared buffer | comparison.go | CAUGHT |
| 13 | `reusesItsBuffer`: the whole re-encode fallback removed | comparison.go | CAUGHT |
| 14 | `reusesItsBuffer`: the fallback's verdict discarded, side effects kept | comparison.go | **SURVIVED — GAP-T60** |
| 15 | `reusesItsBuffer`: `before = bytes.Clone(before)` dropped | comparison.go | **SURVIVED — GAP-T60** |
| 16 | `reusesItsBuffer`: the zero decode between the two encodings dropped | comparison.go | CAUGHT |
| 17 | `spend`: the budget is never spent | comparison.go | CAUGHT |
| 18 | `unanswered`: `beyond` reported ahead of the unrecordable leaf | comparison.go | CAUGHT |
| 19 | `same`: the NaN clause dropped | comparison.go | CAUGHT |
| 20 | `asFloat`: `Uintptr` dropped from the unsigned arm | comparison.go | SURVIVED — no codec this admits produces one |
| 21 | `equalByMethod`: only the `In(0)` clause dropped | comparison.go | **SURVIVED — GAP-T61** |
| 22 | `readsBackOnTheWire`: compared with the zero encoding | comparison.go | CAUGHT |
| 23 | `same`: the func leaf waved through as agreement | comparison.go | CAUGHT |
| 24 | `sameOpaque`: the opaque leaf waved through as agreement | comparison.go | CAUGHT |
| 25 | `shares`: the `spend()` guard dropped | comparison.go | **SURVIVED — GAP-T59** |
| 26 | `unanswered`: the panicked arm never reported | comparison.go | CAUGHT |
| 27 | `roundTrip`: the first decode is handed the shared buffer | fact.go | CAUGHT |
| 28 | `roundTrip`: the fidelity walk never run | fact.go | CAUGHT |
| 29 | `roundTrip`: `notAliased` run for a sample that disturbs nothing too | fact.go | SURVIVED — equivalent for a zero-sized type |
| 30 | `roundTrip`: the sample-type check dropped | fact.go | CAUGHT |
| 31 | `roundTrip`: a sample encoding as its own zero value accepted | fact.go | CAUGHT |
| 32 | `notAliased`: the reuse verdict never acted on | fact.go | CAUGHT |
| 33 | `notAliased`: the carried-out report dropped | fact.go | **SURVIVED — GAP-T59** |
| 34 | `readBack`: `unanswered` never asked after the walk | fact.go | CAUGHT |
| 35 | `readBack`: the opaque wire fallback always answers same | fact.go | CAUGHT |
| 36 | `besideIt`: `readAsJSON` replaced by `IsExported` | routing.go | **SURVIVED — GAP-T57** |
| 37 | `promotedMarshaller`: the `!field.Anonymous` skip dropped | routing.go | **SURVIVED — GAP-T58** |
| 38 | `promotedMarshaller`: an embedded non-struct entered too | routing.go | CAUGHT |
| 39 | `promotedMarshaller`: the embedded-pointer arm falls through | routing.go | CAUGHT |
| 40 | `besideIt`: the embedded field itself counts | routing.go | CAUGHT |
| 41 | `ownMethods`: the unaddressable marshaller accepted | routing.go | CAUGHT |
| 42 | `readAsJSON`: an embedded unexported struct is not written | encodable.go | CAUGHT |
| 43 | `structBehind`: a pointer is not followed | encodable.go | CAUGHT |
| 44 | `fields`: a struct with one unwritten field accepted | encodable.go | CAUGHT |
| 45 | `collect`: the `json:"-"` skip dropped | encodable.go | CAUGHT |
| 46 | `visit`: a slice element loses its addressability | encodable.go | CAUGHT |
| 47 | `visit`: a map value becomes addressable | encodable.go | CAUGHT |
| 48 | *(attribution reruns)* `sameEntries`, the invalid arm and the nil clause, each run alone to read which row reports | comparison.go | CAUGHT — rekeying, rekeying, unreferring |
| 49 | the `rekeying` row deleted from the table + the entry-comparison mutation | roundtrip_test.go | CAUGHT by `inflating` alone — round 10's claim verified |
| 50 | *(probe, not a mutation)* the fourth cell of the aliasing matrix, run against the unmutated tree | — | **PASSES — GAP-T56** |

**Detection: 39 of 50 caught; of the 11 survivors, 4 are equivalent or unreachable and 7 are the
findings above.** With the nineteen reproduced mutations added, 58 of 69 were caught.

Two survivors are deliberately **not** findings, and why: **#4** — dropping the `second.Len() > 0`
half of the empty-slice guard changes an answer only when two decodes of one payload return slices
of different lengths, which is a nondeterministic codec and a different defect; **#5** —
`singleValued`'s `held != nil` guard cannot be reached, because `link.accepts` (`event/chain.go:67`)
rejects a nil interface before `reflect.TypeOf(sample)` is taken and `sameOpaque` never passes a nil
type. It is dead defensive code; under this repository's house style it is removable cleanup rather
than a defect. **#20** and **#29** are equivalent for every shape either door admits.

**Verdict for the cluster.** GAP-T34, GAP-T35 and GAP-T37 are **closed**, and so are GAP-T53,
GAP-T54 and GAP-T55 — all verified by killed mutations against the real implementation rather than
by a note, and the round-10 independence claim was re-derived by deleting the row it rests on. The
cluster is **not green**: the same audit found GAP-T56 `[critical]`, a codec doing precisely what
D-124 obligation 2 forbids and answering `<nil>` from the proxy that claims to refuse it, and two
`[high]` holes of GAP-T37's own class — one rule of `besideIt` and one of `promotedMarshaller` with
a control in one direction only.
