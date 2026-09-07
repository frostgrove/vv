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
  - [ ] Once GAP-190 is decided, a case drives the accepted marker shape through `RoundTrip` and
        asserts the decided answer (a pass, or a refusal with a message that names the escape).
- **Status:** deferred, carried 2026-09-07 into `EVENTSOURCE_P1_PLAN.md` `## Debt` under *From the S2 test review, round 1*, with the section that owns the assertion named.

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
