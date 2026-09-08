# EVENTSOURCE_P1 — implementation S2 — GAPS

## Round 1 — econv-implementation-reviewer (clean context) — 2026-09-07

Reviewed against the **code**. Read in full: `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md`
(§ Carried gaps C1/C9/C10, § Microkernel classification, § Contracts for `event/codec.go`,
`chain.go`, `aggregate.go`, `fact.go`, `change.go`, § S2, § Architecture metrics),
`.agents/artifacts/gaps/EVENTSOURCE_P1_S1_GAPS.md`, `CLAUDE.md`, and
`~/.claude/skills/econv/references/{microkernel,architecture,building-blocks,data-integrity,universality,readability,restrictions,gaps}.md`.
Every number and every transcript below was produced by running in this worktree. The
adversarial probes ran from three throwaway out-of-tree modules under `/tmp` with a `replace`
onto this checkout — **nothing in the repository was edited**.

### Checkpoint verification — the pasted transcripts are real

| Clause | Result here |
|---|---|
| `go build ./... && go vet ./event/ && test -z "$(gofmt -l event)" && go test -race -count=1 ./event/` | `ok github.com/frostgrove/vv/event 1.176s`, `EXIT=0` |
| `go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./event/ \| sort \| paste -sd, -` | `…/crud,…/errs,…/event,…/utils` — byte-identical to the plan's line, unchanged from S1 |
| `go test -list '^(…nine names…)$' ./event/ \| grep -c '^Test'` | **9** |
| `go test -race -count=1 ./event/` twice | green both times |
| `make check` | nine arms, all `ok` |
| `make unit` | `EXIT=0`, zero `FAIL` lines |

### Architecture metrics — counted, not eyeballed

*Size.* Six files, **647** lines: `codec.go` 205, `fact.go` 164, `aggregate.go` 109,
`chain.go` 103, `seal.go` 41, `change.go` 25. Threshold 400 — no breach. The plan's "647 lines,
longest 205, comment density 118/647 = 18 %" is **exact** (`grep -cE '^\s*//'` → 118).

*Functions.* 38 across the six files (14/5/7/8/2/2). Longest **41** lines (`Fact.roundTrip`),
then 39 (`jsonWalk.visit`), 24 (`Aggregate.Fold`), 21 (`Then`). Maximum nesting depth **4**
(`visit`'s `switch → case Struct → for → if`); 3 or less everywhere else. No flag parameter, no
mode switch, no boolean argument on any exported signature.

*Public surface.* S2 adds exactly **22** exported symbols — 6 types (`Aggregate`, `Chain`,
`Change`, `Codec`, `Declaration`, `Fact`), 7 functions (`Declare`, `Define`, `From`, `JSON`,
`Then`, `TryDeclare`, `TryDefine`), 9 methods (`Aggregate.Family/Fold/Key`, `Change.Err/Stream`,
`Fact.Name/New/Revisions/RoundTrip`). Compared symbol by symbol with the plan's
`#### event/codec.go`, `chain.go`, `aggregate.go`, `fact.go`, `change.go` blocks and the S2
**Realises** line: **no drift in either direction, no silent extra surface, nothing marked done
that is missing.** Every signature matches character for character, including the S2 addition
`Aggregate.Key(id ID) (Key, error)` and the S2-corrected `RoundTrip(byRevision ...any) ([]E, error)`.

*Coupling.* Internal (`github.com/frostgrove/vv/…`) imports in the six S2 files: **0**. Stdlib
imports: `encoding`, `encoding/json`, `fmt`, `reflect` (codec), `fmt` (chain), `bytes`, `fmt`,
`sync` (aggregate), `bytes`, `fmt` (fact), none (change), `fmt` (seal). Threshold 5 — no breach.
Zero import cycles; no package in the tree imports `event`.

*Isolation.* Fakes needed to unit-test each new object: `Codec`/`JSON` **0** (pure functions over
`reflect.Type`), `Chain` **1** (a `Codec[V]`), `Aggregate` **0** (a mapper is a `func`), `Fact`
**0**, `Change` **0**. Deleting `RoundTrip` touches `fact.go` and three `link` fields in
`chain.go` and nothing else. No shared mutable context, no god object.

*Global state.* `event/codec.go:65-68` holds two package `var`s, both immutable
`reflect.TypeFor[…]()` values. No other package-level state is added. The only mutex is
`Aggregate.mutex`, per declaration, and it is measurably not a bottleneck
(`BenchmarkNewParallel-8` 141.9 ns/op vs `BenchmarkNewSerial` 256.4 ns/op — parallel scales).

*Purity of rehydration and upcasting — proved, not asserted.*
`grep -nE '"(time|math/rand|crypto/rand|os|net|log|context)"' event/{codec,chain,aggregate,fact,change,seal}.go`
→ **no hits, exit 1**. The whole replay path — `Aggregate.Fold` → `Change.apply` → `applierOf`
→ `link.decode` → `decodeWith` → `Codec.Decode` → `upcastTo` → the caller's upcaster →
the caller's fold — reaches no clock, no randomness, no I/O and no telemetry inside the kernel.
`Compose`, `checkText` and `chargeJSON` are likewise pure.

*Microkernel purity — the command and the result.*
`grep -rniE 'eventmemory|eventpg|if name ==|registry' event/*.go` excluding tests → **three hits,
all in comments** (`aggregate.go:25,103`, `seal.go:14-15`, naming `eventtest`), **zero in code**.
No `init()`, no import side effect, no registry, no type switch over a concrete extension — the
only type assertions in the section are `sample.(V)` in `chain.go:64,66`, the generic-erasure
closures, which branch on a *type parameter* and not on an extension. S2 adds **no** store-facing
symbol, so a second store still costs **zero** `event/` diffs; the extension point S2 does add
(`Codec[V]`) declares its contract, its concurrency obligation, its aliasing obligation and its
failure policy on the interface itself (`codec.go:10-36`), is registered by being passed to
`From`/`Then` (no registry), and the kernel works with zero codecs registered because a
`Chain{}` is refused at `TryDeclare` rather than dereferenced.

*Discipline.* `grep -rn "t.Skip\|nolint\|TODO\|FIXME"` over `event/` → **none**. No string
comparison of errors (`grep -nE '\.Error\(\) *==|strings\.Contains\(err'` → none). Every refusal
is `%w` over an exported sentinel. No mutated input argument (`Fold`'s consumption of `state` is
declared on the signature's own comment and is the documented contract). No third-party
dependency. No prompt, no model call, no secret.

**What is genuinely clean and worth saying so:** contract conformance is exact; the freeze
(`fact.go:74-75`, `bytes.Clone` plus `frozen[:n:n]`), the per-fold clone (`aggregate.go:85`), the
`RoundTrip` clone-what-you-keep repair (`fact.go:122,127,139`), the panic policy for all three
codec methods plus the upcaster, and the fold's *un*recovered panic are all implemented as
written. `Declare`/`TryDefine` share one code path. The seal is correct under `-race` and its
exhaustiveness is policed by an AST walk of the package, which is a real mechanism rather than a
sentence.

The findings below are all in one place — `event/codec.go`'s shipped codec — plus one in
`Fact.RoundTrip` and three smaller ones.

---

### GAP-170 [critical][immediate] `event.JSON` admits payload types that encode to `{}`, and every fact written with one is permanently, silently empty

- **Where:** `event/codec.go:43-57` (`JSON`, `jsonCodec.Encode`), `event/codec.go:112-122`
  (`jsonWalk.visit`'s struct arm), `event/codec.go:134-138` (`marshalsItself`)
- **What:** `CanEncode` answers *"does this type graph contain a kind `encoding/json` refuses"*.
  It does not answer *"does a value of this type survive `Encode` → `Decode` through this
  codec"*, which is the question `Declare` asks it and the only one worth asking. Three reproduced
  shapes are accepted at declaration, encode to `{}` with a `nil` error, mint a `Change` whose
  `Err()` is `nil`, and fold back to the zero value with **no error at any door**:

  1. **A pointer-receiver `MarshalJSON` — the ordinary Go idiom.** `marshalsItself` consults
     `reflect.PointerTo(value)`, but `jsonCodec.Encode` passes `value` to `json.Marshal` as an
     `any`, so the top-level `reflect.Value` is **not addressable** and `encoding/json`'s
     `condAddrEncoder` takes the else-branch: the pointer-receiver marshaller is never called and
     the struct is encoded field by field. Measured here: `json.Marshal(v)` → `"{}"`,
     `json.Marshal(&v)` → `"{\"a\":\"1\"}"`. `Decode` does not have the bug — it passes `&value` —
     so the codec's two halves ask two different questions.
  2. **A struct with fields and no encodable field** (every field unexported).
     `readAsJSON` returns `false` for each, the loop `continue`s, the struct arm falls off the
     end and returns `nil`.
  3. **Two fields rendering one JSON name** (`A int \`json:"x"\``, `B int \`json:"x"\``).
     `encoding/json` drops both at the same depth; the walk does not look at rendered names.

  Transcript, from an out-of-tree module against this checkout:

  ```
  1. every field unexported             CanEncode=ok  Encode="{}"(<nil>)  Decode={owner:}(<nil>)
  3. two fields render one JSON name    CanEncode=ok  Encode="{}"(<nil>)  Decode={A:0 B:0}(<nil>)
  5. pointer-receiver marshaller        CanEncode=ok  Encode="{}"(<nil>)  Decode={fields:map[]}(<nil>)
     for comparison, json.Marshal(&value) on 5 -> "{\"a\":\"1\"}"
  ```

  End to end through the declaration seam, with a `Doc` whose `MarshalJSON`/`UnmarshalJSON` are on
  the pointer receiver:

  ```
  A. CanEncode(Doc): <nil>
  A. change err: <nil>
  A. folded={Seen:0} err=<nil> seenFields=0   <-- 2 expected
  ```

- **Why this severity:** this framework's product is an immutable fact history. In S4 the change
  above reaches `Append` with `Err() == nil` and `{}` is written to the log **durably and
  irreversibly** — the payload cannot be re-derived from anything, because the only copy of it was
  the caller's argument, which is gone. Every subsequent `Load` replays an empty document and
  produces a state that is wrong, with the right version, the right count and no refusal anywhere.
  That is the exact failure mode `universality.md` calls critical: the walk is right on the shapes
  in `declaration_test.go` (a value-receiver `stamped`, exported-field structs, `json.RawMessage`)
  and wrong on the first payload type outside them. `math/big.Int` by value, a domain
  `Money`/`Doc`/`Manifest` with a pointer-receiver codec, and a payload someone forgot to export
  are all ordinary, and all three are silently destroyed. `Fact.RoundTrip` does not rescue it: it
  reports `ErrSample` ("encodes its sample exactly as its own zero value"), which names the sample
  and not the declaration, and it is opt-in.
- **Why this timing:** it changes the contract of the only shipped extension of the second
  extension point, and S3/S4/S5 all build fixtures and conformance cases on `event.JSON`. Every
  fact written before this is repaired is unrecoverable, so it cannot wait for a phase in which
  data exists.
- **Close criteria:**
  - [ ] `jsonCodec.Encode` marshals an addressable value (`json.Marshal(&value)`), so the codec's
        encode path asks the same addressability question its walk asks. Verified byte-identical
        for `int`, `[]int(nil)`, `map[string]int`, `json.RawMessage`, a value-receiver marshaller
        and a slice element — measured here, only the broken case differs
  - [ ] `jsonWalk` carries an `addressable bool` through `descend`, cleared when it descends into
        a **map value** or a **map key** (measured: a map value is still non-addressable under
        `json.Marshal(&v)` — `{"k":{}}`), and `marshalsItself` consults `reflect.PointerTo` only
        where it is set
  - [ ] the struct arm refuses a struct with `NumField() > 0` and zero encodable fields with
        `ErrCodecType`, and **accepts** `struct{}` — a marker fact carries no data by design and
        must stay legal
  - [ ] the struct arm refuses two fields at one depth whose rendered JSON names collide
  - [ ] a table test in `event/declaration_test.go` carries all three shapes as refusals with the
        matching accepting control beside each (a value-receiver marshaller, `struct{}`, distinct
        names), and each refusal case fails if its arm is deleted
- **Status:** closed 2026-09-07 — `event/codec.go` (`Encode` marshals `&value`; the walk carries addressability and refuses the empty-object and colliding-name shapes), `event/declaration_test.go` (three new subtests, eleven new table rows). Verified by mutation: reverting `Encode`, deleting the empty-object arm, deleting the collision arm and ignoring addressability each turn a named case red

---

### GAP-171 [high][immediate] The codec walk asks only whether a type writes itself, never whether it reads back, so an accepted declaration can write facts no declaration will ever read

- **Where:** `event/codec.go:97` and `event/codec.go:134-138` (`marshalsItself` — only
  `json.Marshaler` and `encoding.TextMarshaler`); `event/codec.go:140-149` (`objectKey` —
  only `encoding.TextMarshaler`)
- **What:** `marshalsItself` returns `true` for a type that marshals itself and **stops
  descending**, without asking whether it also *un*marshals itself. `objectKey` does the same for
  a map key. `jobs`'s own predicate, which the plan names as the model
  (`jobs/json.go:1355-1359`), asks all four — `jsonDecoder`, `textDecoder`, `jsonEncoder`,
  `textEncoder`. Measured:

  ```
  2. marshals itself, does not unmarshal itself   CanEncode=ok  Encode="1250"(<nil>)
       Decode={cents:0}(json: cannot unmarshal number into Go value of type main.Money)
  4. map key writes itself, does not read itself  CanEncode=ok  Encode="{\"k\":7}"(<nil>)
       Decode=map[](json: cannot unmarshal string into Go struct field .k of type main.Code)
  ```

  The walk's own refusal text already reasons about reading back — *"an interface, so what it
  holds is not known at declaration and **does not read back**"* (`codec.go:95`) — so the rule is
  applied to one shape and skipped for the two next to it.
- **Why this severity:** the fact is written successfully and is then **unreadable forever**. In
  S4, every `Load` of that stream returns `ErrPayload` and the aggregate can never be rehydrated
  again — a poison pill in an append-only log, which is worse than a rejected write because
  nothing can be appended to repair it (§INV-006's dense fold cannot skip a row). Reachable with
  the first `Money`, `Currency`, `Code` or `Duration` value type whose author wrote
  `MarshalJSON`/`MarshalText` and not the inverse — a very ordinary omission, and one the caller
  is told at declaration is fine.
- **Why this timing:** same contract, same file and the same repair as GAP-170; splitting them
  means walking the same predicate twice. S5's conformance fixtures will be written against
  whatever `CanEncode` means.
- **Close criteria:**
  - [ ] `marshalsItself` accepts a type only when a marshaller **and** its matching unmarshaller
        are both present (`json.Marshaler`+`json.Unmarshaler`, or
        `encoding.TextMarshaler`+`encoding.TextUnmarshaler`), respecting the addressability rule
        of GAP-170 for the pointer half
  - [ ] `objectKey`'s `default` arm asks `TextMarshaler` **and** `TextUnmarshaler`
  - [ ] `event/declaration_test.go` carries "a type that writes itself and does not read itself"
        and "a map key that writes itself and does not read itself" as `ErrCodecType` refusals,
        with the existing `stamped`/`map[string]int64` cases as their accepting controls
- **Status:** closed 2026-09-07 — `event/codec.go` (`writeRoute`/`readRoute`/`ownMethods`, `objectKey`). A type is accepted only where its write route and read route agree; a map key must declare `UnmarshalText`. Verified by mutation

---

### GAP-172 [high][immediate] `Fact.RoundTrip`'s non-aliasing proxy is vacuous for every revision that is not the current one

- **Where:** `event/fact.go:131-149` (`roundTrip` — `read.decode`, then `encodeWith(this.chain.codec, …)`
  either side of `read.selfDecode`), `event/chain.go:72-88` (`carry`)
- **What:** the aliasing verdict is computed by re-encoding, **with the current revision's codec**,
  the value that `read.decode` returned. For a retained revision that is not the current one,
  `read.decode` is *that revision's codec followed by every declared upcaster*, and an upcaster
  converts between two types — so it copies. Whatever the old codec aliased is gone by the time
  the comparison runs, and `RoundTrip` answers `nil`. Measured, with the suite's own
  `scratchCodec` as revision 1 behind a copying upcaster:

  ```
  RoundTrip over an aliasing revision-1 codec behind a copying upcaster: <nil>
  RoundTrip over the same codec as the only revision:
      event: the recorded payload cannot be read by this declaration: revision 1 of
      "probe.written" decoded into memory its codec reuses, …
  ```

  Since only the last link is `linkOf(codec)` with nothing carried over it, the proxy in practice
  tests **exactly one revision — the current one** — of an *n*-revision chain, and reports a clean
  pass for the other *n-1*. `event/roundtrip_test.go:57-71` only ever declares the scratch codec
  as a single-revision chain, so nothing sees this.
- **Why this severity:** C1 is closed in the plan on the strength of this being a *runnable* proxy
  rather than a sentence — "this is what makes it not a sentence". A green `RoundTrip` over a
  three-revision chain currently certifies non-aliasing for one revision and says nothing about
  the two the application no longer looks at, which are precisely the codecs nobody re-reads. The
  consequence when it is missed: a `Load` page of *n* events of an old revision decodes into one
  reused buffer, and any fold that retains the payload (`state.Tags = append(state.Tags,
  event.Body)`) ends with *n* copies of the last event — silent state corruption with no error,
  which is the failure §INV-021 hand-off 8 exists to prevent. This is `architecture.md`'s
  "a check that cannot fail" and `gaps.md`'s "missing test for a stated invariant".
- **Why this timing:** the repair changes `link`'s shape in `chain.go`, which S4's `Load` and S5's
  conformance fixtures read. Doing it after those are written means rewriting them.
- **Close criteria:**
  - [ ] the aliasing comparison is made **in the revision's own type**: `link.selfDecode` returns
        the decoded value (`func([]byte) (any, error)`) and the two encodings compared are
        `selfEncode` of that value, before and after the zero-value decode — so no upcaster stands
        between the aliased buffer and the comparison
  - [ ] fidelity keeps its own arm through `read.decode`, so the two properties are still both
        proved and the upcaster is still exercised
  - [ ] `TestACodecThatDecodesIntoAReusedBufferIsCaught` gains a case where the scratch codec is
        **revision 1 of a two-revision chain whose upcaster copies**, asserting `ErrPayload`, with
        the current single-revision case kept as the control; the case must go red on today's code
- **Status:** closed 2026-09-07 — `event/chain.go` (`link.selfDecode` returns the decoded value), `event/fact.go` (`reusesItsBuffer`, the comparison in the revision's own type, fidelity through `read.decode` afterwards), `event/roundtrip_test.go`. The new case answers `<nil>` on the old spelling

---

### GAP-173 [medium][immediate] A change whose mapper failed carries a zero stream, so `Fold` reports a request-class `ErrKey` as a wiring-class `ErrWrongStream`

- **Where:** `event/fact.go:59-63` (`New` returns before `change.stream` is set),
  `event/aggregate.go:74-83` (the stream loop runs before the carried-error loop),
  `event/aggregate.go:76` (`return state, ErrWrongStream` — the bare sentinel)
- **What:** when the declared mapper renders an illegal key, `Fact.New` sets `change.err = ErrKey`
  and returns **before** `change.stream = stream`, so the change carries `Stream{}`. `Fold` then
  compares streams before it looks at carried errors, and `Stream{}` never equals a legal one.
  Measured:

  ```
  C. carried err on the change: event: the identity does not render a legal stream key: … is empty
  C. Fold reports:              event: this change was decided for another stream
  D. a zero Change:             event: this change was decided for another stream
  ```

  `ErrKey` wraps `crud.ErrBadRequest` and renders a client status; `ErrWrongStream` is a wiring
  sentinel with no declared wrap and falls through to `errs.KindInternal`. A caller that mints a
  change from a request-supplied identity and folds it against the loaded aggregate's identity —
  `change := fact.New(cmd.ID, p); state, err := agg.Fold(loaded.ID, state, change)` — is told the
  server broke over data only the caller can correct. `ErrWrongStream` is also the one refusal in
  the whole section returned as a **bare sentinel**, with no `%w` and no context, so it cannot say
  which change or distinguish "decided for another stream" from "decided for no stream at all".
  `fold_test.go:81-93` covers the mirror direction (`Fold(blankID, …)` → `ErrKey`, correct) and
  not this one.
- **Why this severity:** a status-class inversion visible to a transport, on a path a request
  handler will write; it is not data loss, and `Fold(sameBadID, …)` answers correctly, which
  narrows the window.
- **Why this timing:** S4's `Append` inherits the same ordering over the same `Change`, and the
  spec's cause ordering (§UC-041) is what both doors implement — fixing one door and not the other
  produces two answers to one question.
- **Close criteria:**
  - [ ] a change that carries a refusal is answered with that refusal rather than compared for
        stream equality — either by checking `change.err` first for changes with a zero stream, or
        by `Fact.New` recording the family it was decided for even when the key failed
  - [ ] `ErrWrongStream` is raised through `fmt.Errorf("%w: …")` naming the rule (never the key,
        §INV-025), as every other refusal in the section is
  - [ ] `TestFoldRefusesAnotherInstance` gains "a change minted for an identity that renders no key
        is folded under a legal one", asserting `errors.Is(err, ErrKey)` and
        `!errors.Is(err, ErrWrongStream)`, with the existing blank-identity case as its control
- **Status:** closed 2026-09-07 — `event/change.go` (`decidedFor`), `event/aggregate.go` (`Fold`'s stream loop), `event/fold_test.go`. Verified by mutation: the old comparison turns both the new case and the crossing's message assertion red

---

### GAP-174 [low][immediate] `event/seal.go`'s enumeration comment claims six exhaustive readers and names one that does not exist, and nothing polices it

- **Where:** `event/seal.go:12-23` (the enumeration, row `Bind`), and the forward references at
  `event/codec.go:78` (`Bind never asks it`), `event/aggregate.go:25` (`eventtest.Keys`),
  `event/aggregate.go:103` (`eventtest.Families`), `event/seal.go:14-15`
- **What:** the comment says *"The readers are exhaustive, and this is the enumeration"* and lists
  six, of which `Bind` does not exist — `grep -rn "func Bind" event/ --include=*.go` (non-test)
  returns nothing; S4 adds it. The exhaustiveness test
  (`event/seal_test.go:115-125`) compares the AST call sites against `sealingReaders()`, a
  **test-side map of five**, and never reads this comment. So the one artefact a reader of the
  library sees is the one nothing checks, and it is currently false. Four comments in the section
  name symbols (`Bind`, `eventtest.Keys`, `eventtest.Families`) that no package in the tree
  provides. Separately, `codec.go:77-80`'s third sentence (*"A type that marshals itself is
  accepted without descending"*) restates `codec.go:97` verbatim, and `seal.go:21-23` narrates
  what the test does, both of which `CLAUDE.md`'s comment rule calls expected cleanup.
- **Why this severity:** cosmetic today. It matters because this comment *is* C10.1's deliverable
  — "the sealing readers are exhaustive and enumerated in code" — and an enumeration that names a
  symbol that does not exist has already failed at the one job it has.
- **Why this timing:** S4 adds `Bind` and the sixth row at the same time; if the comment is not
  brought under the test now, the next drift is invisible for the same reason this one is.
- **Close criteria:**
  - [ ] the enumeration in `event/seal.go` names exactly the readers that exist in the tree today
        (five), or the row is marked as arriving with `Bind` in S4
  - [ ] `TestTheSealRefusesALateFact`'s exhaustiveness subtest also parses the enumeration out of
        `seal.go`'s comment and compares it to the AST call sites, so a comment row with no reader
        and a reader with no comment row both fail
  - [ ] `codec.go:78`'s `Bind` reference and the sentence restating `codec.go:97` are removed;
        `seal.go:21-23`'s narration of the test is removed
- **Status:** closed 2026-09-07 — `event/seal.go` (five rows; `Bind` named in prose as arriving with S4), `event/seal_test.go` (`sealEnumeration` parses the comment and the exhaustiveness subtest compares three sets), `event/codec.go` (the `Bind` reference and the restating sentence are gone). Verified by mutation: a sixth row with no reader turns the subtest red

---

### GAP-175 [medium][deferred] `Fold` returns a half-applied state through the same signature as an untouched one, and for a reference-kind state the caller's own argument has already been written through

- **Where:** `event/aggregate.go:68-91`, `event/fact.go:153-164` (`applierOf`)
- **What:** causes 1–3 return `state` untouched; cause 4 (a decode or upcast failure mid-list)
  returns the state as of the last change applied. The caller cannot tell the two apart from the
  return values, and for a map/slice/pointer state the argument it passed in has already been
  advanced by the folds that ran — so the ordinary Go reflex on an error (`if err != nil { … }`
  and keep using the variable you already had) silently keeps a half-rehydrated aggregate. The
  contract is stated in the comment (`aggregate.go:66-67`) and nowhere in the type.
- **Why this severity:** it is a documented, tested (`fold_test.go:128-142`) and spec-level
  decision (§UC-041, GAP-96) forced by reference-kind consumption; the trigger in S2 is narrow,
  because a `Change` from `Fact.New` always carries the current revision, whose `decode` runs no
  upcaster, so cause 4 needs a codec that cannot decode what it just encoded — which is what
  GAP-170/171/172 are about.
- **Why this timing:** deferred — it changes no contract another section depends on, and the
  all-or-nothing repair (decode the whole list before folding any of it) needs `applierOf` split
  into a decode half and an apply half, which is a design change rather than a fix. It must not be
  dropped: it is the one way a partially rehydrated aggregate escapes.
- **Close criteria:**
  - [ ] either `Fold` decodes the whole list before applying any of it, making cause 4
        all-or-nothing like causes 1–3, or the asymmetry is recorded in the plan's `## Debt` with
        the argument for keeping it
  - [ ] whichever is chosen, `docs/modules/en/event.md` (S6) states which of the two a caller gets
        and what to do with the argument after an error
- **Status:** deferred, carried to the plan's `## Debt` — kept deliberately: an all-or-nothing `Fold` holds *n* decoded application values resident, which §INV-042 exists to prevent, and is unachievable for a reference-kind state. What is owed is the statement in `docs/modules/en/event.md`, which S6 owns

---

### GAP-176 [low][deferred] A type graph over the walk's node or edge bound panics `main` as "the codec cannot encode its own reader type", and the depth bound is unreachable

- **Where:** `event/codec.go:59-63`, `event/codec.go:90-92`, `event/codec.go:126-132`
- **What:** two things. (1) `depth > codecGraphDepth` cannot fire: `visit` returns early for a
  seen type, so reaching depth 1025 needs 1025 *distinct* types, and `len(this.seen) >= 1024`
  fires first — a dead branch. (2) exceeding the node or edge bound is answered with
  `ErrCodecType`, which wraps `ErrDeclaration` and therefore **panics at package
  initialisation**, with the message "the codec cannot encode its own reader type". `jobs`, the
  stated model, answers `ErrTooLarge` for the same condition (`jobs/json.go:1067-1070`). A
  generated domain model with more than 1024 distinct types or 4096 field edges reachable from one
  payload — a large OpenAPI or protobuf tree, which is not exotic — takes the process down with a
  diagnosis that names the wrong thing and offers no remedy other than editing the framework.
- **Why this severity:** the bounds themselves are `jobs`'s own numbers and are justified; only
  the classification and the dead branch are wrong, and the shape needed to reach them is large.
- **Why this timing:** deferred — no contract depends on it and no data is at risk.
- **Close criteria:**
  - [ ] the node/edge exhaustion is raised as a bound refusal that names the bound and the
        remedy (a codec of the application's own), not as "cannot encode its own reader type"
  - [ ] the unreachable `depth` branch is removed, or a case that reaches it is added
- **Status:** deferred, carried to the plan's `## Debt` — owner S6

---

### GAP-177 [low][deferred] A recovered codec or upcaster panic discards the panic value entirely, so the only diagnosis is a bare sentinel

- **Where:** `event/codec.go:168-172` (`ErrEncode`, cause `nil`), `event/codec.go:181-186`
  (`ErrPayload`, cause `nil`), `event/chain.go:91-96` (`ErrUpcast`, cause `nil`)
- **What:** each recover discards `recover()`'s value and builds the refusal with a `nil` cause,
  so `CauseOf` answers `nil` and no stack, message or type of the panicking value survives. A
  store operator holding an `ErrPayload` from a hand-rolled binary codec that panicked on a
  truncated buffer has the sentinel and nothing else, and `port.Logger(ctx)` is not reachable from
  these call sites. §INV-025 forbids the *text* travelling in the rendered refusal; it does not
  require the cause to be destroyed, and `refusal.cause` exists precisely so a cause can be
  carried without being rendered.
- **Why this severity:** debuggability only; the classification and the caller's obligation are
  both correct.
- **Why this timing:** deferred; `ErrUpcast`'s "wrapping nothing" is an explicit plan decision
  (`jobs/upcast.go:71`'s shape), so this is a question for the owner rather than a defect to fix
  unasked.
- **Close criteria:**
  - [ ] either the recovered value is carried as the refusal's `cause` (reachable by `CauseOf`,
        still invisible to `Error()`, `Is` and `As`), or the decision to discard it is recorded in
        `## Debt` with the reason
- **Status:** deferred, carried to the plan's `## Debt` — owner S6; `ErrUpcast` wrapping nothing is an explicit plan decision and `TestAnUpcasterMayRefuseAndAFoldMayNot` asserts it, so this is a contract question for the owner rather than a defect to fix unasked

---

## Round 1 — dispositions, 2026-09-07

All eight findings are answered; none is closed by silence and none is rejected.
Five are repaired in the code, three are carried to
`.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` `## Debt` with the argument for
each. The contract sections `#### event/codec.go`, `#### event/chain.go`,
`#### event/aggregate.go` and `#### event/fact.go` were changed in the same edit,
as were the S2 section's carried-gaps line and both checkpoint transcripts.

| Finding | Grade | Disposition |
|---|---|---|
| GAP-170 | `[critical][immediate]` | **fixed** — the walk asks whether a value survives `Encode` → `Decode`, not whether `encoding/json` refuses a kind |
| GAP-171 | `[high][immediate]` | **fixed** — write route and read route must agree, on the type and on a map key |
| GAP-172 | `[high][immediate]` | **fixed** — the aliasing verdict is taken in the revision's own type, before any upcaster |
| GAP-173 | `[medium][immediate]` | **fixed** — a carried refusal outranks the stream comparison, and `ErrWrongStream` names the wire type |
| GAP-174 | `[low][immediate]` | **fixed** — five rows, and the enumeration is now parsed out of the source by the seal test |
| GAP-175 | `[medium][deferred]` | **carried to `## Debt`**, with the argument for keeping the asymmetry and the S6 statement it owes |
| GAP-176 | `[low][deferred]` | **carried to `## Debt`**, owner S6 |
| GAP-177 | `[low][deferred]` | **carried to `## Debt`**, owner S6 |

### Two things the fixes turned up that the review did not name

**GAP-171's repair had to be a route agreement, not a "does it also unmarshal"
predicate.** The close criterion as written — accept only when a marshaller and
its matching unmarshaller are both present — leaves the mirror hole open: a type
with **exported** fields and a `MarshalJSON`/`UnmarshalJSON` pair on the pointer
receiver, held as a map value, is written field by field and read back through
`UnmarshalJSON`. Falling through to the field walk accepts it, because every
field is encodable. So the rule implemented is *the write route and the read
route must agree at this position*, with one exception in the caller's favour: a
type that declares an unmarshaller and **no** marshaller at all is accepted and
descended, because a lenient `UnmarshalJSON` over the default encoding is an
ordinary and correct Go idiom and refusing it would be a false positive on
ordinary code. The distinguishing question — *does this type have a marshaller
this position cannot reach* — is the third arm of `ownMethods`, and
`declaration_test.go` carries a fixture (`posted`, exported fields plus a working
pointer-receiver pair) that is accepted at the reader type and refused as a map
value.

**The three `ownMethods` arms cannot be told apart by sentinel.** Each is
subsumed by the arm beside it as a *verdict* — a type that writes itself and does
not read itself also has a marshaller, so the third arm would refuse it too — so
a table asserting `errors.Is(err, ErrCodecType)` kills none of the first two
mutations. The section therefore carries *a refusal names which asymmetry it
found*, five rows asserting the diagnosis by name, and that subtest is what makes
the arms load-bearing. It is stated here rather than left for the next reviewer
to rediscover: the arms exist for the **repair they name**, and merging them
would be a real regression that no sentinel assertion can see.

---

## Round 2 — econv-implementation-reviewer (clean context, re-audit after round 1's fixes) — 2026-09-07

Read in full: `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` (§ Carried gaps C1/C9/C10,
§ Microkernel classification, `#### event/codec.go`, `#### event/chain.go`,
`#### event/aggregate.go`, `#### event/fact.go`, `#### event/change.go`,
`#### event/binding.go`, § S2, § Architecture metrics), round 1 of this file, `CLAUDE.md`,
and `~/.claude/skills/econv/references/{microkernel,architecture,building-blocks,data-integrity,universality,readability,restrictions,gaps}.md`.
Every number below was produced in this worktree. The adversarial probes ran from a throwaway
out-of-tree module under `/tmp` with a `replace` onto this checkout — **nothing in the
repository was edited**.

### Checkpoint verification — the pasted transcripts are real

| Clause | Result here |
|---|---|
| `go build ./... && go vet ./event/ && test -z "$(gofmt -l event)" && go test -race -count=1 ./event/` | `ok github.com/frostgrove/vv/event 1.182s`, `EXIT=0` — byte-identical to the pasted `1.182s` |
| `go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./event/ \| sort \| paste -sd, -` | `…/crud,…/errs,…/event,…/utils` — identical to the plan's line, unchanged from S1 |
| `go test -list '^(…the nine names…)$' ./event/ \| grep -c '^Test'` | **9** |
| `go test -race -count=1 ./event/` twice, then `-count=3` | green each time |
| `make check` | **nine arms**, all `ok` (`check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`, `check-replaces`, `check-tidy`, `check-otel-schema`, `check-workspace`) |
| `make unit` | `EXIT=0`, `grep -c '^FAIL'` → **0** |

### Architecture metrics — counted, not eyeballed

*Size.* Six files, **807** lines: `codec.go` 332, `fact.go` 176, `aggregate.go` 109,
`chain.go` 106, `change.go` 44, `seal.go` 40. Threshold 400 — no breach. Comment lines
(`grep -cE '^\s*//'`) **155**, density 19 % — the plan's "807 lines, longest 332, 155/807 =
19 %" is exact.

*Functions.* 42 across the six files. Four longest: `jsonWalk.visit` **33**,
`Fact.roundTrip` **29**, `jsonWalk.fields` **24**, `Aggregate.Fold` **24** — the plan's four
numbers are exact. Maximum nesting depth **4**, on `fields`' `for → if named → if already
rendered`. No flag parameter, no mode switch, no boolean argument on any exported signature.

*Public surface.* `go doc -all ./event` confirms S2's **22** exported symbols — 6 types, 7
functions, 9 methods — and every signature matches the plan's contract blocks character for
character, including `Aggregate.Key(id ID) (Key, error)` and
`RoundTrip(byRevision ...any) ([]E, error)`. **No drift in either direction; no silent extra
surface; nothing marked done that is missing.** Round 1 added no symbol.

*Coupling.* Internal (`github.com/frostgrove/vv/…`) imports in the six S2 files: **0**.
Stdlib per file: `encoding`, `encoding/json`, `fmt`, `reflect`, `strings` (codec); `fmt`
(chain); `bytes`, `fmt`, `sync` (aggregate); `bytes`, `fmt` (fact); `fmt` (change, seal).
Threshold 5 — no breach. Zero import cycles; no package in the tree imports `event`.

*Isolation.* Fakes needed to unit-test each object: `Codec`/`JSON` **0**, `Chain` **1**,
`Aggregate` **0**, `Fact` **0**, `Change` **0**. No shared mutable context, no god object.

*Microkernel purity — the command and the result.*
`grep -rniE 'eventmemory|eventpg|if name ==|registry|init\(\)' event/{codec,chain,aggregate,fact,change,seal}.go`
→ **exit 1, zero hits**, in code and in comments alike. S2 adds no store-facing symbol, so
phase 2's `eventpg` still costs **zero** `event/` diffs. The one extension point S2 adds
(`Codec[V]`) declares its contract, its concurrency obligation, its aliasing obligation and
its panic policy on the interface itself (`codec.go:11-37`), registers by being passed to
`From`/`Then` with no registry, and the kernel works with zero codecs registered because a
`Chain{}` is refused at `TryDeclare` rather than dereferenced.

*Purity.*
`grep -nE '"(time|math/rand|crypto/rand|os|net|log|context|sync/atomic)"' event/{codec,chain,aggregate,fact,change,seal}.go`
→ **exit 1, no hits**. The whole replay path reaches no clock, no randomness, no I/O and no
telemetry.

*Discipline.* No `t.Skip`, `nolint`, `TODO`, `FIXME`; no error compared by string; every
refusal `%w` over an exported sentinel; no third-party dependency.

### Round 1's five repairs, verified against the code and by probe

| Finding | Verdict |
|---|---|
| GAP-170 | **partially closed** — `Encode` marshals `&value` (`codec.go:57`), the walk carries `position.addressable`, and the two *directly declared* shapes are refused. The third close criterion ("two fields at **one depth** whose rendered JSON names collide") is met only for fields declared on the struct itself; **promoted fields are not checked at all**, and the same silent-`{}` failure is fully reproducible through embedding. See GAP-178 |
| GAP-171 | **partially closed** — `writeRoute`/`readRoute`/`ownMethods` are real and load-bearing; a map value with a pointer-receiver pair is correctly refused (verified: `json` writes it field-by-field and reads it through `UnmarshalJSON`, so the refusal is not a false positive). `objectKey`'s **`default` arm** asks both methods, exactly as the criterion was worded — and the `String`/integer fast path above it still returns `nil` without asking, which is the kind every ordinary domain key type has. See GAP-179 |
| GAP-172 | **closed as written, still vacuous for the codec shape it names** — `link.selfDecode` returns the decoded value, `reusesItsBuffer` compares in the revision's own type, and `roundtrip_test.go:90-106` drives revision 1 behind a copying upcaster with a control. But the second decode is the **zero value's** encoding, which for a length-prefixed codec is *shorter* than the sample and never overwrites the region the first answer aliases. See GAP-180 |
| GAP-173 | **closed.** `Change.decidedFor` (`change.go:34-44`) answers a carried refusal before the crossing, `ErrWrongStream` is raised through `fmt.Errorf` naming the wire type, and `errors.go:30` keeps it a sentinel to `errors.Is` against |
| GAP-174 | **closed.** `seal.go:12-22` names five readers, all five exist and all five call `seal()` (`aggregate.go:45,50,69`, `fact.go:57,100`), `Bind` is named in prose as arriving with S4, and `seal_test.go` parses the enumeration out of the source |

GAP-175, GAP-176 and GAP-177 are carried in the plan's `## Debt` and remain `deferred`.

### What is genuinely clean and worth saying so

Contract conformance is exact. The freeze (`fact.go:74-75`), the per-fold clone
(`aggregate.go:85`), `RoundTrip`'s clone-what-you-keep discipline
(`fact.go:126,131,154`), the panic policy for all three codec methods plus the upcaster,
and the fold's *un*recovered panic are implemented as written. `Declare`/`Define` share one
path with their `Try` twins. The seal is correct under `-race` and its exhaustiveness is
policed by an AST walk plus a parse of the comment — a mechanism, not a sentence. Round 1
introduced no new sentinel, no new dependency and no new exported symbol.

The findings below are three in `event/codec.go`'s shipped codec and `Fact.RoundTrip` — all
three the same root cause, a hand-derived model of `encoding/json`'s routing rules standing
in for the question `CanEncode` says it answers — plus one measured hot-path defect and two
smaller ones.

---

### GAP-178 [critical][immediate] The codec walk cannot see promoted fields, so an embedded struct silently deletes data from every fact ever written

- **Where:** `event/codec.go:118-120` (the comment that exempts promotion), `event/codec.go:160-183`
  (`jsonWalk.fields` — the `rendered` set is per struct, not per depth),
  `event/codec.go:270-279` (`renderedName` returns `""` for an anonymous struct field, so it is
  never entered in the collision set), `event/codec.go:281-283` (`readAsJSON` counts an
  anonymous struct as one written field regardless of what it promotes)
- **What:** `fields` builds its `rendered` collision set from the fields *declared on this
  struct*. `encoding/json` builds its from the fields *promoted to each depth*. Three shapes
  are accepted by `CanEncode` with `nil`, encode with a `nil` error, mint a `Change` whose
  `Err()` is `nil`, and fold back with data missing and **no error at any door**. Measured from
  an out-of-tree module against this checkout:

  ```
  (a) type Payload struct{ A; B }                  // A and B each carry ID int `json:"id"`
      CanEncode=<nil>  Encode="{}"  Decode={A:{ID:0} B:{ID:0}}

  (b) type Root struct{ Mid; Deep1 }               // Mid embeds Deep1; N int `json:"n"`
      CanEncode=<nil>  in={Mid:{Deep1:{N:1}} Deep1:{N:2}}
      Encode="{\"n\":2}"  Decode={Mid:{Deep1:{N:0}} Deep1:{N:2}}   roundtrips=false

  (c) type Placed struct{ A; B; Amount int `json:"amount"` }
      CanEncode=<nil>  Encode="{\"amount\":99}"  Decode={A:{ID:0} B:{ID:0} Amount:99}
  ```

  End to end through the whole declaration seam, with (c):

  ```
  change err: <nil>
  folded={Total:99 SeenA:0 SeenB:0} err=<nil>   (expected {Total:99 SeenA:7 SeenB:11})
  RoundTrip err=<nil> carried=[{A:{ID:0} B:{ID:0} Amount:99}]
  ```

  (c) is the worst of the three: the payload is **partially** written, so there is no
  zero-value smell — `Amount` survives, the two identifiers vanish, and the fact reads as a
  perfectly ordinary event forever. `Fact.RoundTrip` does not rescue any of them: it never
  compares the value it decoded against the sample it was given, so it hands the mangled value
  back with a `nil` error.
- **Why this severity:** identical to GAP-170, which was graded `[critical]` and closed on a
  repair that does not cover this route. The product is an immutable fact history; in S4 the
  change above reaches `Append` with `Err() == nil` and the truncated bytes are written
  **durably and irreversibly**, because the only copy of the dropped fields was the caller's
  argument. Embedding a shared `Base`, `Timestamps`, `Audit` or `Meta` into two levels of a
  payload struct is ordinary Go, not an exotic shape, and `go vet`'s `structtag` does not look
  at promotion either. GAP-170's own close criterion — *"the struct arm refuses two fields at
  one depth whose rendered JSON names collide"* — is not met: promoted fields **are** at one
  depth, and shape (a) is exactly two of them colliding.
- **Why this timing:** it is a `universality.md` finding — the walk is right on the shapes in
  `declaration_test.go` (flat structs, tagged collisions, a value-receiver marshaller) and
  wrong on the first payload type that embeds anything, so it can never be `deferred`. It also
  changes the meaning of `CanEncode`, which S3's fixtures, S4's `Load` and S5's conformance
  suite are all about to be written against, and every fact written before the repair is
  unrecoverable.
- **Close criteria:**
  - [ ] the effective JSON member set is no longer re-derived by hand. Either
        `jsonWalk.fields` computes promoted names per depth the way `encoding/json`'s own
        `typeFields` does (breadth-first by depth, shallower wins, a tie at one depth drops
        both), **or** — the general mechanism that subsumes this finding, GAP-179 and all five
        of the walk's hand-written rules — `chargeJSON` answers its stated question by *running*
        it: build a probe value of the reader type with every reachable leaf set to a
        distinguishable non-zero value, `Encode` it, `Decode` it, and refuse unless
        `reflect.DeepEqual` holds, keeping the static walk only as the fallback for the shapes
        no probe can be built for (recursive, interface, unexported-only)
  - [ ] `event/declaration_test.go` carries shapes (a), (b) and (c) above as `ErrCodecType`
        refusals, each with an accepting control beside it — a single embedded struct whose
        promoted names are distinct, and a promoted field shadowed by nothing — and each refusal
        goes red if its arm is deleted
  - [ ] `Fact.RoundTrip` compares the value it re-read in the revision's own type against the
        sample it was given, so a payload type that loses data is caught by the proxy the plan
        calls "runnable" rather than handed back with a `nil` error
  - [ ] `event/codec.go:118-120`'s exemption sentence is removed or rewritten to say what is
        now actually true
- **Status:** closed 2026-09-07 — `event/encodable.go` (`members.collect` computes the names a
  struct renders the way `encoding/json` does, following embedded fields one type per path, and
  refuses a name claimed twice by any route), `event/fact.go` (`roundTrip` compares what came
  back with the sample in the revision's own type — `sameValue`, not `reflect.DeepEqual`, which
  measured false for a `time.Now()` payload and for a populated unexported field and would have
  been a false accusation on the two most ordinary payload fields there are),
  `event/declaration_test.go` (three refused rows, three accepting controls, two diagnosis rows),
  `event/roundtrip_test.go` (*a codec that records less than it was handed is caught*, with the
  shipped codec and a codec that forgets a `time.Time` beside it, and *what an encoding drops by
  design is not what it lost* as the comparison's own control). The
  exemption sentence is gone and the plan's refusal 5 says what is now true. **The second close
  criterion — the dynamic probe — is rejected with the argument below.** Verified by mutation:
  restricting the claim walk to the fields declared on the struct turns the named case and its
  diagnosis row red; deleting the fidelity comparison turns the truncating-codec case red;
  spelling it `reflect.DeepEqual`, dropping the `Equal`-method arm, or comparing unexported
  fields each turns one of the three fidelity cases red

---

### GAP-179 [critical][immediate] A map key of string or integer kind that declares its own text method is accepted without a route check, and the fact is written with corrupted keys or is unreadable forever

- **Where:** `event/codec.go:254-260` (`objectKey`'s fast path: `String`, `Int…`, `Uint…`
  return `nil` before any question is asked), `event/codec.go:261-267` (the `default` arm that
  GAP-171's repair landed in), `event/codec.go:149-153` (`visit`'s `Map` arm, which asks
  `objectKey` and never `ownMethods` for the key)
- **What:** `encoding/json` routes a map key through `MarshalText` when the key type has one,
  **whatever its kind**, and through `UnmarshalText` when `*K` has one, checked *before* the
  string/integer kinds. `objectKey` returns `nil` for those kinds first, so a key type that
  declares one half of the text pair never reaches the route-agreement check GAP-171 installed
  one arm below. Two reproduced shapes, both accepted with `CanEncode() == nil`:

  ```
  type Currency string;  func (c Currency) MarshalText() ([]byte, error)   // no UnmarshalText
      in=map[usd:100 eur:50]  enc={"cur:eur":50,"cur:usd":100}  dec=map[cur:eur:50 cur:usd:100]

  type Kind int;         func (k Kind) MarshalText() ([]byte, error)       // no UnmarshalText
      in=map[3:1]  enc={"k3":1}
      dec=map[]  err=json: cannot unmarshal number k3 into Go struct field ByKind.m.k3 of type main.Kind

  type ReadOnly string;  func (c *ReadOnly) UnmarshalText([]byte) error    // no MarshalText
      in=map[abc:1]  enc={"abc":1}  dec=map[ABC:1]
  ```

  End to end through the declaration seam, with `map[Currency]int64`:

  ```
  change err: <nil>
  folded={Totals:map[cur:eur:50 cur:usd:100]} err=<nil>     (expected map[eur:50 usd:100])
  RoundTrip err=<nil> carried=[{ByCurrency:map[cur:eur:50 cur:usd:100]}]
  ```

  The two existing map-key fixtures, `region` and `district` (`declaration_test.go:81-89`), are
  both **struct**-kind, so every map-key case in the suite enters through the `default` arm and
  the fast path above it has never been exercised by anything.
- **Why this severity:** the `Kind` case is GAP-171's own stated failure — *"the fact is written
  successfully and is then unreadable forever … a poison pill in an append-only log, which is
  worse than a rejected write because nothing can be appended to repair it"* — reproduced
  verbatim through the branch the repair did not touch. The `Currency` case is worse than that:
  it does not fail at all. `cur:usd` is written into the log, replayed into every rehydration
  and returned to the application as a legal currency code, with the right version, the right
  count and no refusal anywhere. `type Currency string` / `type Code string` / `type Kind int`
  with a `MarshalText` written for display and no reader is the single most ordinary shape a
  domain value type takes in Go.
- **Why this timing:** `universality.md` — the rule was fitted to the two fixtures that
  happened to be struct-kind, so it is `[critical][immediate]` and never `deferred`. It is the
  same file, the same predicate and the same repair as GAP-178, and S5's conformance fixtures
  will be written against whatever `CanEncode` means.
- **Close criteria:**
  - [ ] `objectKey` asks the write-route/read-route agreement question **before** the
        string/integer fast path, in the same order `encoding/json` asks it: a key type that
        declares `MarshalText` must declare `UnmarshalText` on `*K`, and a key type that
        declares `UnmarshalText` on `*K` and no `MarshalText` is refused too, whatever the key's
        kind — or the dynamic probe of GAP-178's second close criterion replaces `objectKey`
        outright
  - [ ] `event/declaration_test.go` carries `map[Currency]int` (string kind, write-only),
        `map[Kind]int` (integer kind, write-only) and `map[ReadOnly]int` (string kind,
        read-only) as `ErrCodecType` refusals, each with an accepting control beside it — a
        plain `map[string]int`, a plain `map[int]int`, and a string-kind key declaring the
        matching pair
  - [ ] the diagnosis subtest ("a refusal names which asymmetry it found") gains a row for the
        fast-path kinds, so merging the arm back into the fast path is killed by name and not
        only by sentinel
- **Status:** closed 2026-09-07 — `event/encodable.go` (`objectKey` asks the write route and the
  read route first, in both directions, and reaches the string/integer kinds only when the key
  declares neither text method), `event/declaration_test.go` (`map[currency]int`,
  `map[grade]int`, `map[kept]int` refused; `map[int64]int`, `map[settled]int` and the existing
  `map[string]int64` accepting beside them; two diagnosis rows). Verified by mutation: putting
  the kinds back in front turns the `currency` case red, and deleting the reader-without-a-writer
  arm turns the `kept` case red

---

### GAP-180 [high][immediate] `Fact.RoundTrip`'s non-aliasing proxy passes an ordinary reusable-buffer codec, and the only fixture it catches is one that zeroes its own array

- **Where:** `event/fact.go:127-141` (`roundTrip` — the disturbing payload is `read.selfZero()`),
  `event/fact.go:145-163` (`reusesItsBuffer`), `event/roundtrip_test.go:30-34`
  (`scratchCodec.Decode`, whose first statement is `this.scratch = [32]byte{}`)
- **What:** the proxy decodes the sample, re-encodes it, decodes **the zero value's encoding**,
  re-encodes the first answer again, and reports aliasing if the two differ. It therefore
  detects reuse only where decoding the zero value *overwrites the bytes the first answer
  points at*. A codec that slices one buffer to the payload length and copies into it — the
  shape `roundtrip_test.go:10-14` itself names as the motivation, "a hand-rolled binary codec"
  — never touches that region, because the zero value's encoding is *shorter*. Measured against
  this checkout with a length-prefixed codec holding one `buf []byte`:

  ```
  first answer after a second decode: "twe"   (second decode was "twelve")
  Fact.RoundTrip verdict on that codec: <nil>
  ```

  The aliasing is real and destructive — the value the codec already returned reads `"twe"`
  after the next decode — and the proxy reports a clean pass. `scratchCodec` is caught only
  because it explicitly writes `[32]byte{}` over its whole buffer on every decode, which is a
  property of the fixture and not of the contract.
- **Why this severity:** this is GAP-172's grade and GAP-172's failure, reached through a
  different codec shape. C1 is closed in the plan on the strength of this being a *runnable*
  proxy — "this is what makes it not a sentence" — and it is a check that, for the ordinary
  implementation of the contract it polices, cannot fail. The consequence when it is missed is
  the one §INV-021 hand-off 8 exists to prevent: a `Load` page of *n* events decodes into one
  reused buffer, a fold that retains the payload (`state.Tags = append(state.Tags, e.Body)`)
  ends with *n* copies of the last event, and nothing anywhere returns an error. `architecture.md`'s
  "a check that cannot fail" and `gaps.md`'s "missing test for a stated invariant" both apply.
- **Why this timing:** `eventtest.RoundTrip` (S5) is the `*testing.T` wrapper over this
  algorithm and the conformance suite's whole non-aliasing story rests on it; a store author
  reading a green S5 run would be told an obligation was proved that was not. The repair
  changes what `roundTrip` does with the revision's readers, which S5's fixtures are written
  against.
- **Close criteria:**
  - [ ] the verdict no longer depends on the disturbing payload being longer than the sample.
        The general mechanism: decode the **same** `written` bytes twice through
        `read.selfDecode` and refuse when the two independently returned values share a backing
        array — reachable with `reflect` alone (`reflect.Value.Pointer()`/`UnsafePointer()` over
        every `[]byte` and `string` leaf of the returned value), needs no `unsafe`, and is
        exact rather than a proxy. Keep the zero-value decode as a second arm if it is wanted;
        it is not sufficient on its own
  - [ ] `TestACodecThatDecodesIntoAReusedBufferIsCaught` gains "a codec that reuses one buffer
        and does not clear it", a length-prefixed codec of the shape above, asserting
        `ErrPayload`; the case must answer `<nil>` on today's code, and a control codec that
        allocates per decode must still pass
  - [ ] `roundtrip_test.go`'s `scratchCodec` no longer zeroes its array, or a second fixture
        that does not is added beside it, so the suite stops depending on a property the
        contract does not require
  - [ ] the module page (S6) states which of the two properties a green `RoundTrip` proved and
        which codec shapes it cannot see
- **Status:** closed 2026-09-07 — `event/fact.go` (`reusesItsBuffer` decodes the same bytes twice
  from two separate input buffers and `sharesMemory` walks the two answers for a slice or map
  they hold in common; the zero-value disturbance stays behind it as the second arm, which is
  what reaches a `string` leaf), `event/roundtrip_test.go` (`prefixCodec`, a length-prefixed
  codec that reuses one buffer and never clears it, with `allocatingCodec` of the same wire
  format as its control). `scratchCodec` keeps its zeroing array — the third criterion's *or* —
  because the new fixture is what the criterion asks for and the old one still pins the
  disturbance arm. The module-page sentence is S6's and is written into the plan's
  `#### event/fact.go` for it to carry. Verified by mutation: removing the shared-backing arm
  answers `<nil>` for the new case

---

### GAP-181 [medium][immediate] Every decision takes the aggregate's mutex to write a boolean that is already true, and one aggregate serialises the whole process — measured 5.2x

- **Where:** `event/seal.go:23-27` (`seal()` — an unconditional `Lock`/`Store`/`Unlock`), and
  its five callers on the hot path: `event/aggregate.go:45` (`Family`), `:50` (`Key`), `:69`
  (`Fold`), `event/fact.go:57` (`New`), `:100` (`RoundTrip`)
- **What:** `sealed` is monotone — once `true` it is never written back — but `seal()` takes the
  per-aggregate `sync.Mutex` on **every** call, so every `Fact.New` and every `Aggregate.Fold`
  in the process contends on one mutex per declaration. A real deployment has one `*Aggregate`
  per aggregate type (`Order`, `Account`), shared by every request. Measured here on a 20-thread
  machine, 200 000 iterations each:

  ```
  BenchmarkNewSerial-20                          223.3 ns/op
  BenchmarkNewParallel-20                        148.9 ns/op   one aggregate, 20 goroutines
  BenchmarkNewParallelDistinctAggregates-20       28.4 ns/op   64 aggregates, 20 goroutines
  ```

  28.4 vs 148.9 ns/op is a **5.2x** penalty attributable to the seal mutex alone; a 20-core
  machine gets a 1.5x speedup from 20 goroutines instead of ~8x. Round 1's own metrics section
  concluded "measurably not a bottleneck … parallel scales" from the 141.9-vs-256.4 pair, which
  is parallel-with-contention against serial and has no distinct-aggregate control — the
  comparison that would have shown the contention is the one that was not run.
- **Why this severity:** no contract changes and nothing is incorrect; it is throughput on the
  framework's central operation. It is not `[low]` because the number is 5.2x, it is on the
  path every write takes, and S4 adds four more sealing readers (`Bind`, `Load`, `Append`,
  `Within`) to the same mutex.
- **Why this timing:** `immediate` because S4 multiplies it and because the fix is one line
  today and a re-measurement of five call sites later. It does not block the gate at this
  severity.
- **Close criteria:**
  - [ ] `sealed` becomes an `atomic.Bool` (or equivalent) with a lock-free fast path in
        `seal()` — `if this.sealed.Load() { return }` before the lock — while `declare` keeps
        taking the mutex and reading `sealed` under it, so the "no fact after the first
        observation" invariant is unchanged. Correct because `sealed` only ever goes false→true
  - [ ] `go test -race` over `event/` and `event/concurrency_test.go`'s existing declare/observe
        race stay green, and a test asserts a fact declared concurrently with a first
        observation is either accepted or `ErrSealed` and never both
  - [ ] the distinct-aggregate control is added beside whatever benchmark is quoted, so a future
        claim that the mutex is not a bottleneck has to survive it
- **Status:** closed 2026-09-07 — `event/aggregate.go` (`sealed atomic.Bool`), `event/seal.go`
  (the fast path, and `declare` still reading it under the mutex), `event/seal_test.go` (*a fact
  declared as the first reader runs is accepted or sealed and never both*, 200 rounds of two
  observers against one late declaration, asserting the table membership agrees with the answer;
  plus the three benchmarks, the distinct-aggregate one among them). Measured here on 20 threads
  at 200 000 iterations: one aggregate 165.6 → 42.4 ns/op, 64 aggregates 67.1 → 43.7 ns/op.
  Verified by mutation: `sealed` as a plain `bool` is a `WARNING: DATA RACE` in the new subtest

---

### GAP-182 [medium][deferred] A change minted on one aggregate folds through a different aggregate of the same family and state type, running a fold that aggregate never declared

- **Where:** `event/change.go:34-44` (`decidedFor` compares only `Stream{Family, Key}`),
  `event/aggregate.go:68-91` (`Fold` never asks whether the change's fact belongs to this
  declaration), `event/change.go:21` (`apply` is captured from the *fact* at `Fact.New`, not
  looked up in the folding aggregate's `facts` table)
- **What:** measured here:

  ```
  a := Define[State, string]("order", key)
  b := Define[State, string]("order", key)          // a second declaration, same family
  paidOnA := Declare(a, "order.paid", …)
  b.Fold("o-1", State{}, paidOnA.New("o-1", Paid{5}))   →  {N:5}  err=<nil>
  ```

  `b` never declared `order.paid`, and `b.facts` — which exists and is written by `declare` — is
  not consulted by `Fold` at all in S2. The crossings fixtures prove the compiler catches a
  state-type crossing and an identity-type crossing; this is the third crossing and nothing
  catches it. In S4 the write path would accept the append and `b`'s own `Load` would refuse
  `order.paid` as an unknown wire type, so the stream becomes unloadable through the declaration
  that wrote it.
- **Why this severity:** it needs two aggregate declarations over one family and one state type
  in one process, which is a wiring mistake rather than an ordinary call, and S4's `Bind` check
  4 ("the family is not already bound through this `Binding` to a *different* declaration →
  `ErrFamily`") closes the bound path. The store-free `Fold` door (§UC-041) has no `Binding` and
  therefore no such check.
- **Why this timing:** `deferred` — no contract another section depends on changes, S2 has no
  store so nothing is written, and the repair belongs beside S4's `ErrFamily` check so the two
  doors give one answer. It must not be dropped: it is the one crossing the type system does not
  close.
- **Close criteria:**
  - [ ] either `Change` carries the `Declaration` it was decided on and `Fold` refuses a change
        whose declaration is not this aggregate, or `Fold` looks its `apply` up in
        `this.facts[change.name]` and refuses an unknown wire type with the sentinel S4's `Load`
        uses — the two doors must not answer differently
  - [ ] a test folds a change minted on one aggregate through a second aggregate of the same
        family and state type and asserts the refusal, with the same-aggregate fold as its
        control
- **Status:** deferred, carried to the plan's `## Debt` — owner **S4**, beside `Bind`'s
  `ErrFamily` check, so the bound door and the store-free door answer one question once

---

### GAP-183 [low][deferred] `reusesItsBuffer` accuses a non-deterministic codec of aliasing, which is the wrong repair for the extension point `Codec[V]` exists to admit

- **Where:** `event/fact.go:145-163` (`reusesItsBuffer` — the verdict is
  `!bytes.Equal(before, after)` over two encodings of one value), `event/fact.go:139-141` (the
  refusal text: "decoded into memory its codec reuses")
- **What:** the comparison assumes `Encode` is a function of its argument. `encoding/json` sorts
  map keys and is; protobuf's Go implementation explicitly is **not** deterministic across calls
  for a message with a map field, and a hand-rolled codec over a `map` is not either. Such a
  codec answers `before != after` for a value it never aliased and is reported as `ErrPayload`
  "decoded into memory its codec reuses", which sends the reader to a repair that is not the
  problem. The plan names protobuf, msgpack and gob as exactly the codecs `Codec[V]` exists for.
- **Why this severity:** `RoundTrip` is a declaration-time diagnostic, not a runtime path; it
  fails closed and costs a wrong diagnosis rather than data.
- **Why this timing:** `deferred` — GAP-180's repair replaces the mechanism, and whichever
  mechanism lands should carry the determinism question with it rather than being changed twice.
- **Close criteria:**
  - [ ] either `roundTrip` establishes that the codec's encode is deterministic first — two
        `selfEncode` calls on one value, with no decode in between — and reports a distinct
        refusal naming non-determinism when it is not, or the aliasing verdict stops depending
        on re-encoding at all (GAP-180's backing-array comparison does not)
  - [ ] the module page (S6) states that a codec whose `Encode` is not a function of its
        argument cannot be round-tripped by this proxy
- **Status:** deferred, carried to the plan's `## Debt` — owner **S6**. GAP-180's repair made the
  *first* arm exact and independent of re-encoding, and the entry records the symmetric false
  positive it introduces: a codec that hands out a slice of a table its package keeps for ever is
  two equal addresses and is not the reuse the contract forbids. Both are the one question the
  entry names

---

### GAP-184 [low][deferred] Two comments name an `eventtest` package no tree in the repository provides, and one refuses a working codec pair

- **Where:** `event/aggregate.go:25` (`eventtest.Keys is the runnable proxy`),
  `event/aggregate.go:103` (`eventtest.Families`), `event/codec.go:193-204` (`ownMethods`)
- **What:** two things. (1) GAP-174's repair marked the seal enumeration's `Bind` row as
  arriving with S4 and brought the enumeration under the seal test, but the two `eventtest.*`
  forward references were not touched and carry no such marker;
  `grep -rn "package eventtest" .` returns nothing. A reader of the library is sent to a
  package that does not exist, and nothing polices these two the way `seal_test.go` now polices
  the enumeration. (2) `ownMethods`' first arm refuses a type whose write route and read route
  differ in either direction, so `MarshalText` on the value receiver paired with `UnmarshalJSON`
  on the pointer receiver is refused with *"writes itself through MarshalText and declares no
  UnmarshalText"* — measured here, that pair encodes `{"m":"7"}` and decodes back to `{N:7}`
  correctly. It fails closed, at declaration, so nothing is at risk; it costs a consumer a
  workaround for a pair that works.
- **Why this severity:** cosmetic and a false positive that fails safely.
- **Why this timing:** `deferred` — S5 creates `eventtest` and can carry both `Keys` and
  `Families`, and (2) should not be loosened before GAP-178/GAP-179 settle what the walk asks.
- **Close criteria:**
  - [ ] the two `eventtest.*` references are marked as arriving with S5, as `seal.go:20-22`
        marks `Bind`, or the seal test's parse is extended to cover every forward reference in
        the section
  - [ ] `ownMethods` accepts a write route of `byTextMethods` against a read route of
        `byJSONMethods` (the text output is a JSON string and `UnmarshalJSON` receives it), or a
        table row records the refusal as deliberate with the reason
- **Status:** half closed, half deferred. The two `eventtest.*` references are closed here —
  `event/aggregate.go:25,103` now name the conformance suite and say it has not arrived, which is
  the spelling GAP-174 gave `Bind`, and `event/seal.go`'s two enumeration rows with them —
  `grep -rn "eventtest" event/*.go | grep -v _test.go` is empty. The
  `ownMethods` false positive is carried to the plan's `## Debt`, owner **S6**: loosening the arm
  is a change to what the walk asks, which GAP-178 and GAP-179 have just settled

---

### Round 2 — what a re-audit found that the fixes introduced

Nothing in round 1's five repairs is wrong in itself, and none of them broke anything: the
surface is unchanged, the import graph is unchanged, `make check` and `make unit` are green,
and `-race` is green three runs in a row. GAP-172's and GAP-173's repairs are complete.

The pattern in GAP-178, GAP-179 and GAP-180 is one pattern, stated once so it is not fixed
three times and found a fourth: **each repair was written to the close criterion's wording and
to the fixture beside it, rather than to the rule the criterion was a instance of.** GAP-170
said "two fields at one depth" and got the two fields declared on the struct; GAP-171 said
"`objectKey`'s `default` arm" and got the `default` arm; C1 said "a codec that reuses its decode
buffer" and got the one fixture that zeroes its array. In all three the general mechanism —
*ask the codec whether a value survives the round trip, instead of re-deriving the rules by
which it might* — was available and was not taken, and in all three the shape that breaks it is
the ordinary one rather than the exotic one.

---

## Round 2 — dispositions, 2026-09-07

All seven findings are answered; none is closed by silence. Four are repaired in
the code, two are carried to `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md`
`## Debt` with the argument for each, and one is half of each. **One close
criterion is rejected with the argument**, in GAP-178, and the finding is closed
through the alternative the criterion itself offers first. The contract sections
`#### event/codec.go`, `#### event/fact.go` and `#### event/aggregate.go`, C1's
runnable proxy, the S2 section's file list, its round-2 paragraph and both
checkpoint transcripts were changed in the same edit.

| Finding | Grade | Disposition |
|---|---|---|
| GAP-178 | `[critical][immediate]` | **fixed** — the walk computes the names a struct renders the way `encoding/json` does, promoted fields included, and `RoundTrip` compares what came back with the sample. The dynamic probe is **rejected**, below |
| GAP-179 | `[critical][immediate]` | **fixed** — a map key's two text methods are asked about first and in both directions, before the kinds |
| GAP-180 | `[high][immediate]` | **fixed** — the aliasing verdict is two decodes compared for a shared slice or map; the disturbance arm stays behind it |
| GAP-181 | `[medium][immediate]` | **fixed** — `sealed` is an `atomic.Bool` with a lock-free fast path, and the distinct-aggregate benchmark is beside the one it controls |
| GAP-182 | `[medium][deferred]` | **carried to `## Debt`**, owner S4, beside `Bind`'s `ErrFamily` |
| GAP-183 | `[low][deferred]` | **carried to `## Debt`**, owner S6, with the symmetric false positive GAP-180's repair introduces |
| GAP-184 | `[low][deferred]` | **half closed** (the `eventtest` references), **half carried** (the `ownMethods` false positive), owner S6 |

### The rejected criterion: `chargeJSON` must not answer by running a probe

GAP-178's second close criterion offers two mechanisms and round 2's closing
paragraph recommends the second: build a probe value of the reader type with
every reachable leaf set to a distinguishable non-zero value, `Encode` it,
`Decode` it, and refuse unless `reflect.DeepEqual` holds. **The first mechanism is
implemented and the second is refused**, and the reason is that a synthetic value
is not a value of the application's type — it is a value of its *shape*, and the
two are the same thing only for types that carry no rule. Measured against this
checkout:

- **`json.RawMessage`** is a `[]byte` whose contents must be valid JSON. Any
  distinguishable non-zero leaf makes `json.Marshal` fail, so the probe refuses a
  type the suite accepts today and the golden table pins the wire format of.
- **A validating `UnmarshalText` or `UnmarshalJSON`** — `type Currency string`
  that accepts only known codes, a `Rating` bounded to 1..5, a `Duration` that
  parses `"5m"` — refuses the probe's manufactured leaf and the declaration
  panics at package initialisation for a type that works perfectly. Every one of
  those is exactly what `Codec[V]`'s consumers write, and there is no
  distinguishable value the framework can pick that a domain type is obliged to
  accept.
- **A type whose leaves cannot be set** — `time.Time`, `big.Int`, anything with
  unexported fields — has no probe at all, so the static walk has to stay for it,
  and the two mechanisms then disagree about which shapes were checked.
- **`omitempty`** makes the probe's own comparison ambiguous for the one thing it
  was for: a zero leaf is omitted by design, so a dropped field and an omitted
  one are the same absence.
- It also runs an application's marshaller at `JSON[V]()`, which is a package
  `var` initialiser — a panic there is not the declaration refusal the section
  panics with, and `chargeJSON` is charged once per codec, not per call, so the
  cost is not the objection; the refusals are.

The rule the criterion was an instance of is nonetheless the right one, and it is
taken where a **real** value exists: `Fact.RoundTrip` now compares what came back
with the sample the caller supplied, in the revision's own type. That is the
dynamic check with the caller's own value instead of a manufactured one, and it
is what closes GAP-178's third criterion. The static walk keeps the declaration
door, where no value exists yet.

### Three corrections to the findings' own reasoning, all measured

**`go vet` does see the same-depth promoted collision.** GAP-178 says
"`go vet`'s `structtag` does not look at promotion either". It does: with the
finding's own shape (a) written with tags, `go vet` reports *"struct field ID
repeats json tag \"id\" also at …"* at the embedded type's declaration — the
first version of this repair's fixtures failed `make vet` for that reason. What
vet does **not** see is (b), a name promoted from two different depths, and it
says nothing at all about an **untagged** pair, which is how embedding is
ordinarily written. The fixtures were rewritten untagged, which is both the more
ordinary shape and the one nothing but this walk sees; the finding's severity is
unchanged and its rationale is narrower than it was stated.

**The obvious spelling of GAP-178's third criterion is itself a false
accusation.** *"`Fact.RoundTrip` compares the value it re-read against the sample
it was given"* reads as `reflect.DeepEqual`, and `reflect.DeepEqual` answers
**false** for two payloads nothing is wrong with: measured,
`stamp{At: time.Now()}` (a monotonic reading and a `Local` location that no
encoding preserves, and that `time.Time.Equal` says are not the difference) and
`holding{Amount: 5, formatted: "$0.05"}` (a populated unexported field
`encoding/json` never wrote and no load was ever going to read). A framework whose
own runnable proxy refuses a payload with a timestamp in it is the failure this
section exists to prevent, one door further in. The comparison implemented walks
the exported half, asks a type's own `Equal(T) bool` where it declares one, treats
two NaNs as one value, and runs out of budget in the caller's favour — and the
three cases that pin those choices go red under `DeepEqual`, under a field walk
with no `Equal` arm, and under one that compares unexported fields.

**The map-key routing this repair is written against is the toolchain's, not the
one `encoding/json`'s v1 source describes.** `resolveKeyName` in
`$GOROOT/src/encoding/json/encode.go` checks `Kind() == String` *before*
`TextMarshaler`, which would make GAP-179's `Currency` case harmless; the Go
1.27.0 toolchain here answers `{"cur:usd":100}`, so the live path asks
`TextMarshaler` first. The repair refuses route disagreement **whatever the
kind**, which is correct under the routing this toolchain has and conservative
under the one the v1 source describes — a framework may not have a rule that is
right on one toolchain and silently wrong on another.

---

## Round 3 — econv-implementation-reviewer (clean context, re-audit after round 2's fixes) — 2026-09-07

Read in full: `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` (§ Carried gaps, § Microkernel
classification, `#### event/codec.go`, `#### event/chain.go`, `#### event/aggregate.go`,
`#### event/fact.go`, `#### event/change.go`, § S2, § Architecture metrics, § Debt),
`.agents/artifacts/usecases/EVENTSOURCE_P1_USECASES.md` §UC-042/§UC-041/§UC-065, rounds 1 and 2
of this file, `CLAUDE.md`, and
`~/.claude/skills/econv/references/{microkernel,architecture,building-blocks,data-integrity,universality,readability,restrictions,gaps}.md`.
Every number below was produced in this worktree. The adversarial probes ran from a throwaway
out-of-tree module under `/tmp/eprobe` with a `replace` onto this checkout — **nothing in the
repository was edited**.

### Checkpoint verification — the pasted transcripts are real

| Clause | Result here |
|---|---|
| `go build ./... && go vet ./event/ && test -z "$(gofmt -l event)" && go test -race -count=1 ./event/` | `ok github.com/frostgrove/vv/event 1.199s`, `EXIT=0` (plan pastes 1.204s — same run, timing noise) |
| `go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./event/ \| sort \| paste -sd, -` | `…/crud,…/errs,…/event,…/utils` — identical to the plan's line, unchanged from S1 |
| `go test -list '^(…the nine names…)$' ./event/ \| grep -c '^Test'` | **9** |
| `go test -race -count=1 ./event/` twice, then `-count=3` | green each time (`1.198s`, `1.200s`, `1.576s`) |
| `go vet ./event/...` | `EXIT=0` |
| `gofmt -l .` | silent |
| `make check` | **nine arms**, all `ok` |
| `make unit` | `EXIT=0`, `grep -c '^FAIL'` → **0** |

### Architecture metrics — counted, not eyeballed

*Size.* Seven files, **1021** lines: `fact.go` 312, `encodable.go` 290, `aggregate.go` 112,
`chain.go` 106, `codec.go` 103, `seal.go` 54, `change.go` 44. Threshold 400 — no breach. Comment
lines (`grep -cE '^\s*//'`) **207**, density 20.3 %. The plan's "1021 lines, longest
`encodable.go` at 290, 207/1021 = 20 %" is exact.

*Functions.* **55** across the seven files (the plan's "five longest" list is exact:
`Fact.roundTrip` 37, `sharesMemory` 35, `sameValue` 35, `members.collect` 33, `jsonWalk.visit`
33, then `Aggregate.Fold` 24). Maximum control-structure nesting **3** by my counter (4 counting
`case` as a level, which is the plan's convention) — either way under threshold. Maximum
parameter count **4** (`TryDeclare`). No flag parameter, no mode switch, no boolean argument on
any exported signature.

*Public surface.* An AST count over the seven files gives 25 top-level exported declarations, of
which 3 are `jsonCodec`'s methods on an unexported type — **22** exported symbols, matching the
plan's claim, and every signature matches the plan's contract blocks character for character
(`Aggregate.Key(id ID) (Key, error)`, `RoundTrip(byRevision ...any) ([]E, error)`,
`Fold(id ID, state S, changes ...Change[S]) (S, error)`, `Then[A, B any](prev Chain[A],
codec Codec[B], up func(A) (B, error)) Chain[B]`). **No contract drift in either direction; no
silent extra surface; nothing marked done that is missing.** Round 2 added no symbol. Public
methods per type: `Aggregate` 3, `Fact` 4, `Change` 2, `Codec` 3, `Chain` 0 — threshold 7, no
breach.

*Coupling.* Internal (`github.com/frostgrove/vv/…`) imports in the seven S2 files: **0**
(`grep -n 'frostgrove/vv' event/{codec,encodable,chain,aggregate,fact,change,seal}.go` → exit 1).
Threshold 5 — no breach. Zero import cycles; no package in the tree imports `event`.

*Isolation.* Fakes needed to unit-test each object: `Codec`/`JSON` **0**, `Chain` **1** (a
`Codec`), `Aggregate` **0**, `Fact` **0**, `Change` **0**. No shared mutable context, no god
object, no fixture path referenced from non-test source.

*Microkernel purity — the command and the result.*
`grep -rniE 'eventmemory|eventpg|if name ==|registry|init\(\)' event/{codec,encodable,chain,aggregate,fact,change,seal}.go`
→ **exit 1, zero hits**, in code and in comments alike. S2 adds no store-facing symbol, so phase
2's `eventpg` still costs **zero** `event/` diffs. The one extension point S2 adds (`Codec[V]`)
declares its contract, its concurrency obligation, its aliasing obligation and its panic policy
on the interface itself (`codec.go:9-40`), registers by being passed to `From`/`Then` with no
registry, and the kernel works with zero codecs registered because a `Chain{}` is refused at
`TryDeclare` rather than dereferenced. **One new undeclared extension point was introduced by
round 2 and is GAP-188: `sameValue` calls an application's `Equal` method.**

*Purity.*
`grep -nE '"(time|math/rand|crypto/rand|os|net|log|context)"' event/{codec,encodable,chain,aggregate,fact,change,seal}.go`
→ **exit 1, no hits**. Rehydration and upcasting reach no clock, no randomness, no I/O and no
telemetry. `sync/atomic` appears once, on the seal.

*Discipline.* No `t.Skip`, `nolint`, `TODO`, `FIXME`, `XXX`; no error compared by string; every
refusal `%w` over an exported sentinel; no third-party dependency; no global mutable state (the
four `reflect.Type` package vars in `encodable.go:17-22` are written once at init and never
reassigned).

*Concurrency, read rather than only run.* `chargeJSON` builds a fresh `jsonWalk` and a fresh
`members` per call, so no walk state is shared. `seal()`'s fast path is an `atomic.Bool` load and
`declare` reads the same flag under the mutex, so a fact arriving with a first observation is
accepted or `ErrSealed` and never both — mutation-checked in round 2 and still green under
`-race -count=3`. `Aggregate.facts` is written only under the mutex and read by nothing in S2.
`Chain.links` is never written after construction; `Then` allocates a new slice rather than
appending into `prev`'s. `Change.payload` is `bytes.Clone`d with a full-slice expression at
`Fact.New` and cloned again per fold, so no slice the caller can mutate escapes. No map is
iterated while being appended to.

### Round 2's four repairs, verified against the code and by probe

| Finding | Verdict |
|---|---|
| GAP-178 | **closed for the route it names, and the class is still open.** `members.collect` (`encodable.go:154-186`) does compute promoted names and refuses a name claimed twice by any route; shapes (a), (b) and (c) are refused with `ErrCodecType`, verified. `Fact.roundTrip` compares in the revision's own type. But the *class* — "the hand-derived model of `encoding/json` diverges from `encoding/json`" — is reproducible through three routes the repair does not reach: GAP-185, GAP-186, GAP-187 |
| GAP-179 | **closed.** `objectKey` (`encodable.go:255-273`) asks both text methods first and in both directions; `map[Currency]int` with a write-only `MarshalText` is refused, `map[string]int` and `map[int64]int` still accepted. One residual hole in the *read* direction is GAP-186 |
| GAP-180 | **closed.** `reusesItsBuffer` (`fact.go:159-181`) decodes the same bytes twice from two separate buffers and `sharesMemory` compares the answers; the zero-value disturbance is the second arm. A length-prefixed reusing codec is caught, an allocating codec of the same wire format passes |
| GAP-181 | **closed.** `sealed` is an `atomic.Bool` (`aggregate.go:15`), `seal()` returns on a `Load` (`seal.go:35-37`), `declare` reads it under the mutex (`seal.go:46`). Green under `-race -count=3` |

GAP-175, GAP-176, GAP-177, GAP-182, GAP-183 and GAP-184's second half are carried in the plan's
`## Debt` and remain `deferred`; each is present there with its argument, verified by grep.

### What is genuinely clean and worth saying so

Contract conformance is exact — the plan and the code say the same thing about every one of the
22 exported symbols, and the six S2 clauses on `#### event/fact.go` are implemented as written.
The seal's exhaustiveness is policed by an AST walk plus a parse of the comment: five rows, five
readers, `grep -rn 'seal()' event/*.go` agrees. `Change.decidedFor`'s carried-refusal-before-
crossing order is right and is the fix GAP-173 asked for. The freeze, the per-fold clone and
`RoundTrip`'s clone-what-you-keep discipline are all present. The kernel holds no store-facing
symbol at all, so the zero-diff obligation for `eventpg` is met by construction. The import graph
is unchanged from S1.

The six findings below are five in `event/encodable.go`'s walk and `event/fact.go`'s two value
walks — four of them the same root cause round 2's own closing paragraph named and then repaired
one route at a time — and one contradiction between two doors of the same section.

---

### GAP-185 [critical][immediate] A marshaller pair promoted from an embedded type is trusted as the struct's own, so every field declared beside the embedded one is silently deleted from every fact

- **Where:** `event/encodable.go:82` (`visit` asks `ownMethods` before it ever looks at the
  struct's members), `event/encodable.go:188-199` (`ownMethods` — `writes == reads` settles the
  position with `nil` and the walk returns), `event/encodable.go:201-233` (`writeRoute`/`readRoute`
  ask the *method set*, which in Go includes every method promoted from an embedded field)
- **What:** when a struct embeds a type that declares a matching `MarshalJSON`/`UnmarshalJSON`
  or `MarshalText`/`UnmarshalText` pair, Go promotes that pair onto the outer struct. The outer
  struct then writes itself as the embedded value and **nothing else** — every field declared
  beside the embedded one is never written and never read back. `ownMethods` sees
  `writes == reads` and returns `true, nil`, so `members.collect`, the `written == 0` arm and the
  descent never run. Four shapes, all measured from an out-of-tree module against this checkout,
  all `CanEncode() == nil`:

  ```
  (a) type Line struct{ Money; SKU string }          // Money has the JSON pair
      CanEncode=<nil>  Encode=500          Decode={Money:{Cents:500} SKU:}
  (b) type Stamped struct{ time.Time; Note string }  // the textbook embedding
      CanEncode=<nil>  Encode="2026-01-02T03:04:05Z" Decode=2026-01-02 03:04:05 +0000 UTC
  (c) type Tagged struct{ Code; Note string }        // Code has the TEXT pair
      CanEncode=<nil>  Encode="abc"        Decode={Code:abc Note:}
  (d) type Order struct{ Lines []Line; Ref string }  // one level down, inside a slice
      CanEncode=<nil>  Encode={"Lines":[7],"Ref":"r"} Decode={Lines:[{Money:{Cents:7} SKU:}] Ref:r}
  ```

  End to end through the whole declaration seam, with (a) extended by a `Count` field:

  ```
  change err: <nil>
  folded={SKU: Cents:500 Count:0} err=<nil>   (expected {SKU:sku-1 Cents:500 Count:7})
  ```

  There is `nil` at every door: `CanEncode`, `Encode`, `Fact.New().Err()`, `Aggregate.Fold`. Only
  `Fact.RoundTrip` catches it, and only when the caller both calls it and supplies a sample whose
  lost fields are non-zero — it is opt-in, and §UC-042's wrapper does not exist yet.
- **Why this severity:** identical in kind and consequence to GAP-178, which was graded
  `[critical][immediate]` and closed on a repair that does not cover this route. `struct{ time.Time; … }`
  is the single most textbook embedding in Go; embedding a `Money`, `Decimal`, `NullString` or a
  domain `Code` with a text pair is the ordinary way a shared value type is reused. The product is
  an immutable fact history: in S4 the change above reaches `Append` with `Err() == nil` and the
  truncated bytes are written **durably and irreversibly**, because the only copy of the dropped
  fields was the caller's argument. `go vet` says nothing about any of the four.
- **Why this timing:** a `universality.md` finding — the walk is right on the shapes in
  `declaration_test.go` (no fixture there embeds a type with a marshaller pair;
  `grep -n 'Money\|MarshalJSON' event/declaration_test.go` shows every marshaller fixture declared
  on the fixture type itself) and wrong on the first payload that embeds one, so it can never be
  `deferred`. It changes the meaning of `CanEncode`, which S3's fixtures, S4's `Load` and S5's
  conformance suite are all about to be written against, and every fact written before the repair
  is unrecoverable.
- **Close criteria:**
  - [ ] `ownMethods` no longer settles a **struct** position on a marshaller it did not find
        declared on that struct. The general mechanism: at a struct position whose write route is
        not `byFields`, ask whether any anonymous field's type (or its pointer) carries the same
        route, and — when it does and `members.collect` over the struct yields any member the
        routing type does not itself carry — refuse with `ErrCodecType`, naming the embedded type
        and the fields it hides. A struct that declares its own marshaller *and* embeds a
        marshalling type is refused too; that is a conservative false positive which fails closed
        at declaration with one remedy (name the field instead of embedding it, or declare a
        `Codec[V]` of your own), and it is the same trade the walk already makes for
        `MarshalText`/`UnmarshalJSON` (GAP-184)
  - [ ] `event/declaration_test.go` carries shapes (a), (b), (c) and (d) as `ErrCodecType`
        refusals, each with an accepting control beside it — a **named** (not embedded) field of
        the same marshalling type, an embedded marshalling type with no sibling field at all, and
        the existing value-receiver marshaller declared on its own type — and each refusal goes
        red if its arm is deleted
  - [ ] the diagnosis subtest gains a row so the message names *which* embedded type swallows the
        struct, not only the sentinel
  - [ ] `#### event/codec.go`'s five-refusal list gains this as refusal 6, and the walk's own
        comment (`encodable.go:47-59`) stops saying it "stops at a type that both writes and reads
        itself" without the exception
- **Status:** closed 2026-09-07 — `ownMethods` no longer settles a struct position on a marshaller it did not find declared there: `promotedMarshaller` (`event/encodable.go`) asks whether an anonymous field carries the route the struct writes by, and refuses when any field of the struct's own stands beside it, naming the embedded type and the field it hides. The tagged spelling is refused too, and measurement says that is right — a `json:"money"` tag does not undo method promotion, so this finding's own suggested control was wrong and the accepting control is a **named** field. `declaration_test.go` carries shapes (a), (b) and the tagged one as `ErrCodecType` refusals with four accepting controls (a named field of the same type, the embedded type with no field beside it, a `time.Time` in a named field, the same pair on the pointer receiver) and two diagnosis rows. Shape (d) is shape (a) one level down and is refused by the same arm through the descent. Verified by mutation: deleting the arm turns the three rows red; making `besideIt` count the embedded field itself turns the *control* red.

  **Amended 2026-09-08 (S2 test round 3, GAP-T41).** Close criterion 2 was **not** met in full when this was marked closed: shape (c) — an embedded type carrying the **text** pair, `struct{ Code; Note string }` where `Code` has `MarshalText`/`UnmarshalText` — was never given a row, and the closing note above accounts for (a), (b), the `json:"money"` spelling and (d) without mentioning it. `promotedMarshaller` was route-generic from the start and refused the shape correctly, so nothing was ever lost through it; what was missing was the gate, and `if writes == byTextMethods { return nil }` survived the whole suite until GAP-T41 closed it. The criterion is met now: `termed`, `netted` and `agreed` in `event/declaration_test.go`.

---

### GAP-186 [critical][immediate] A read route through a method on the value receiver counts as a read route, so a payload field and a map key both decode back to their zero value with no error at any door

- **Where:** `event/encodable.go:223-225` (`readsAs` — `value.Implements(unmarshaler)` accepts a
  method whose receiver is a value and therefore cannot write anything back),
  `event/encodable.go:211-219` (`readRoute` is built on it), `event/encodable.go:257`
  (`objectKey`'s read side: `reflect.PointerTo(key).Implements(textUnmarshaler)` is true for a
  value-receiver `UnmarshalText` too)
- **What:** `encoding/json` calls `UnmarshalJSON`/`UnmarshalText` through the addressable
  pointer, so a method declared on the **value** receiver is invoked on a copy and every mutation
  it makes is discarded. The decoded value is the zero value, and `json.Unmarshal` returns `nil`.
  Both `readsAs` and `objectKey`'s read probe answer "this type reads itself" for such a method,
  so the route-agreement check that GAP-171 and GAP-179 installed passes. Two shapes measured
  against this checkout, both `CanEncode() == nil`:

  ```
  type Sloppy struct{ V int }
  func (s Sloppy) MarshalJSON()  ([]byte, error)   // value receiver, correct
  func (s Sloppy) UnmarshalJSON(b []byte) error    // value receiver, inert
      Encode={"S":9,"Note":"note"}   Decode={S:{V:0} Note:note}   err=<nil>
      change err=<nil>   folded={N:0} err=<nil>          (expected {N:9})

  type Cur string
  func (c Cur) MarshalText() ([]byte, error)       // writes "x-"+c
  func (c Cur) UnmarshalText(b []byte) error       // value receiver, inert
      map[Cur]int{"usd":1}
      Encode={"M":{"x-usd":1}}   Decode={M:map[:1]}   err=<nil>
      change err=<nil>   folded={N:1} err=<nil>
  ```

  In the second, every key of the recorded map comes back as the **empty string** — one key
  survives per map because they all collide on `""` — and the fold sees a map of the right length
  with the wrong contents and no refusal anywhere.
- **Why this severity:** this is refusal 1's own stated failure (*"a type that writes itself and
  declares no matching unmarshaller … the bytes are written and are then unreadable forever"*)
  reached through a type that declares one which cannot work. A value-receiver `UnmarshalJSON` is
  the classic Go mistake the walk exists to catch — `go vet` has no check for it, the compiler is
  silent, and `encoding/json` returns `nil`. The consequence is worse than a rejected write: the
  fact is durable and every replay of it, for ever, produces a zero value with the right version
  and the right count. §INV-025's refusal never fires because there is no refusal.
- **Why this timing:** `universality.md` — the predicate was written against the fixtures in
  `declaration_test.go`, whose unmarshallers are all on the pointer receiver
  (`grep -n 'func (this \*' event/declaration_test.go`), so the value-receiver branch has never
  been exercised by anything. It is the same file, the same two predicates and the same repair as
  GAP-185, and S5's conformance fixtures will be written against whatever `CanEncode` means.
- **Close criteria:**
  - [ ] `readsAs` accepts a read route only when the method is reachable **through the pointer and
        not through the value** — `reflect.PointerTo(v).Implements(u) && !v.Implements(u)` for a
        non-pointer `v`, with the pointer-kind reader type (`JSON[*Doc]`) handled explicitly — and
        `objectKey`'s read probe asks the same question, so a value-receiver `UnmarshalText` on a
        map key is refused rather than counted
  - [ ] a refusal message that says the reader is on the value receiver and cannot write back,
        distinct from "declares no `UnmarshalJSON`", with a diagnosis row pinning it by name
  - [ ] `event/declaration_test.go` carries `Sloppy` (value-receiver `UnmarshalJSON` on a payload
        field) and `map[Cur]int` (value-receiver `UnmarshalText` on a map key) as `ErrCodecType`
        refusals, each with the pointer-receiver spelling of the same type as its accepting
        control, and each goes red if the arm is deleted
  - [ ] `#### event/codec.go`'s refusal 1 and refusal 3 are reworded to say the reader must be on
        the pointer receiver
- **Status:** closed 2026-09-07 — `readsAs` answers true only for a method reachable through the pointer and **not** through the value, `objectKey`'s read probe asks the same question, and the case carries a message of its own (*"declares UnmarshalJSON on the value receiver, so encoding/json calls it on a copy"*) distinct from *"declares no UnmarshalJSON"*. `declaration_test.go` carries the value-receiver reader as a reader type and as a payload field, and a value-receiver `UnmarshalText` map key, each refused, with the pointer-receiver spelling of the same shape as its control. Verified by mutation: restoring the `||` in `readsAs`, and restoring `objectKey`'s bare `PointerTo(key).Implements`, each turn the rows red.

---

### GAP-187 [critical][immediate] An embedded pointer to an unexported struct type is accepted, encodes cleanly and can never be decoded — a poison pill in the log

- **Where:** `event/encodable.go:280-283` (`readAsJSON` — `field.Anonymous && promotes` accepts an
  embedded `*unexported`), `event/encodable.go:285-290` (`structBehind` follows the pointer and
  answers `promotes = true`), `event/encodable.go:166` (`collect` recurses through it and claims
  its members), `event/encodable.go:111-120` (`fields` counts it as a written field and descends)
- **What:** `encoding/json` **writes** the promoted fields of an embedded pointer to an
  unexported struct type and then **refuses to read them back**, because `reflect` cannot set the
  embedded pointer. The walk models only the write half. Measured against this checkout:

  ```
  type hidden struct{ N int }
  type P struct{ *hidden }

  event.JSON[P]().CanEncode()          → <nil>
  json.Marshal(&P{&hidden{7}})         → {"N":7}   err=<nil>
  json.Unmarshal({"N":7}, &P{})        → json: cannot unmarshal number into Go struct field
                                          P.N of type main.P: cannot set embedded pointer to
                                          unexported struct type
  ```

  End to end through the declaration seam:

  ```
  Fact.New err: <nil>                       ← the fact is minted and would be appended
  Fold folded={N:0} err=event: the recorded payload cannot be read by this declaration
  ```

  The **embedded value** spelling (`type Q struct{ hidden }`) round-trips correctly and must stay
  accepted; only the pointer spelling is broken, and the walk cannot tell them apart because
  `structBehind` deliberately dereferences.
- **Why this severity:** exactly GAP-171's and GAP-179's stated worst case — *"the fact is written
  successfully and is then unreadable forever … a poison pill in an append-only log, which is
  worse than a rejected write because nothing can be appended to repair it"* — reached through a
  third route. In S4 `Append` sees `Err() == nil` and the bytes land durably; every `Load` of that
  stream fails with `ErrPayload` from then on and no repair is possible from inside the
  application, because the aggregate can no longer be rehydrated to append a correcting fact.
  Embedding a package-private struct to share fields is ordinary Go; making it a pointer is a
  choice people make for optionality.
- **Why this timing:** `universality.md` — `readAsJSON`/`structBehind` were written to make
  GAP-178's *value*-embedding fixtures pass and the pointer spelling was never asked about
  (`grep -n '\*' event/declaration_test.go` shows no embedded-pointer fixture). It cannot be
  `deferred`: S3's fixtures, S4's `Load` and S5's conformance suite are all about to be written
  against what `CanEncode` means, and a fact written before the repair is unrecoverable.
- **Close criteria:**
  - [ ] the walk refuses an **anonymous** field whose type is a pointer to an **unexported**
        struct type, with `ErrCodecType` and a message that says it encodes and can never be
        decoded and names the remedy (embed by value, or export the type)
  - [ ] `event/declaration_test.go` carries `struct{ *hidden }` as an `ErrCodecType` refusal with
        three accepting controls beside it — `struct{ hidden }` (embedded value, unexported type),
        `struct{ *Exported }` (embedded pointer, exported type) and a **named** field of type
        `*hidden` — and the refusal goes red if the arm is deleted
  - [ ] a diagnosis row pins the message, so merging the arm into the existing
        "writes none of them" refusal is killed by name
  - [ ] `#### event/codec.go`'s refusal list gains the shape, and `readAsJSON`'s and
        `structBehind`'s contract in `encodable.go` says which half of `encoding/json`'s promotion
        rule each models
- **Status:** closed 2026-09-07 — `members.collect` carries the blocking embedded pointer down the promotion and refuses where a **name is rendered** through it, which is `encoding/json`'s own asymmetry rather than a blunt ban: the embedded **value** spelling and a type embedding a pointer to itself both render nothing through an unallocatable pointer and both round-trip, so both stay legal. Refusing on sight was measured and rejected for exactly that reason — it turns `type ring struct{ *ring; N int }`, which decodes correctly, into a refusal. `pointed` moves from the accepted table to the refused one; `struct{ *Stream }` (an embedded pointer to an **exported** struct), `carried` (an embedded value of an unexported struct) and `holdingDated` (a **named** field of `*dated`) are its three accepting controls, and a diagnosis row pins the message. Verified by mutation: deleting the refusal turns the row red; raising it where the pointer is *seen* rather than where a name is rendered turns the `ring` control red.

---

### GAP-188 [high][immediate] `sameValue` calls an application's own `Equal` method — an undeclared extension point with no panic policy, which crashes `Fact.RoundTrip` on a nil pointer and silently disables the fidelity check when the method is lenient

- **Where:** `event/fact.go:302-312` (`equalByMethod` — `first.MethodByName("Equal")` then
  `equal.Call(...)`), `event/fact.go:256-258` (it is asked **before** the `Pointer`/`Interface`
  nil guard at `fact.go:270-274`), `event/codec.go:26-35` (the panic policy that enumerates every
  extension point and does not mention this one)
- **What:** two consequences of one decision — round 2 made the framework call a method the
  contract never declared, on a value the framework chose, with no policy for what it may do.

  **(1) It crashes.** `equalByMethod` is asked before the nil check, so for a payload field of
  pointer type whose type declares `Equal` on the **pointer** receiver, the method is invoked with
  a nil receiver. Measured:

  ```
  type Node struct{ ID int }
  func (n *Node) Equal(o *Node) bool { return n.ID == o.ID }
  type payload struct{ Ref *Node; Note string }

  event.JSON[payload]().CanEncode()                 → <nil>
  fact.RoundTrip(payload{Ref: nil, Note: "hello"})  → PANIC: runtime error: invalid memory
                                                       address or nil pointer dereference
  ```

  A raw runtime panic unwinds out of an exported kernel method. The section's own rule —
  *"the kernel recovers a panic from an extension that has a stated refusal channel for the same
  failure … and it recovers nothing else"* — has no row for `Equal`, and `roundTrip` has no
  `recover`. In S5 this panics the conformance binary instead of reporting a refusal.

  **(2) It makes the check it was added for optional.** A payload type that declares a *semantic*
  `Equal` overrides the fidelity comparison entirely. Measured:

  ```
  type Doc struct{ Title, Body string }
  func (d Doc) Equal(o Doc) bool { return d.Title == o.Title }   // Body is not compared
  a codec that drops Body:  fact.RoundTrip(Doc{"t","the body that the codec drops"})
                            → err=<nil>  got=[{Title:t Body:}]
  ```

  This is the exact defect GAP-178's third close criterion added the comparison to catch, made
  invisible by a method the application wrote for its own reasons. `architecture.md`'s "a check
  that cannot fail" applies, and the application never asked for its `Equal` to be given this job.
- **Why this severity:** `high`, not `critical`, because (1) fails loudly and (2) needs an
  application `Equal` — neither writes a wrong fact by itself. It is not `medium` because
  `Fact.RoundTrip` is exported kernel API, `struct{ Ref *Node }` with a pointer-receiver `Equal`
  is an ordinary Go shape, and the crash reaches a consumer as a framework bug with no diagnosis.
  It is a `universality.md` finding: the `Equal` arm was installed to make one sample shape
  (`time.Time`, whose fields are all unexported) work, and it was generalised to *any* method
  named `Equal` with no thought for the receiver or for what the method means.
- **Why this timing:** `immediate`. It is a `universality.md` finding, which is never `deferred`;
  it was introduced by round 2's own repair, which is the case the re-audit exists for; and S5's
  `eventtest.RoundTrip` is written directly over this algorithm, so a store author would get a
  panic instead of a report.
- **Close criteria:**
  - [ ] the nil guard runs **before** `equalByMethod`, and `equalByMethod` refuses to call a
        method whose receiver is a nil pointer or a nil interface. A test drives
        `payload{Ref: nil}` through `Fact.RoundTrip` and asserts it returns rather than panics,
        with a non-nil `Ref` as its control
  - [ ] a panic out of the application's `Equal` is either recovered into a stated sentinel or the
        method is not called at all — and whichever is chosen, `event/codec.go`'s panic-policy
        table gains a row for it, because that table claims to enumerate every extension point
  - [ ] the `Equal` arm no longer decides the verdict on its own. Either it is narrowed to the
        shapes a field walk cannot answer — a struct with no exported field, which is the case
        `time.Time` is — or a disagreement between `Equal` and the exported-field walk is
        reported rather than resolved in `Equal`'s favour. `roundtrip_test.go` carries the
        lenient-`Equal` case above as an `ErrPayload` refusal, with `time.Time` and the existing
        `stamp{At: time.Now()}` case as its controls, so neither repair can be made by breaking
        the other
  - [ ] `#### event/fact.go`'s clause 6 states what `Equal` is asked, when, and what happens when
        it disagrees with the field walk
- **Status:** closed 2026-09-08, round 5 — `event/comparison.go` (`equalByMethod` reached only from `sameFields`, and only where the struct has no exported field; its panic recovered into `ErrSample`), `event/codec.go` (the panic-policy row), `event/roundtrip_test.go` (three new subtests, two new diagnosis rows, the `weighed` fixture moved to an unexported field so the signature check is still reached). Criterion 3 taken by the narrowing branch

---

### GAP-189 [medium][immediate] Both value walks stop after 1024 nodes and report a pass, so `RoundTrip` silently proves nothing about a payload with more leaves than that — and the constant is the type-graph bound reused for a quantity it does not measure

- **Where:** `event/fact.go:152` and `event/fact.go:164` (`budget := codecGraphNodes` — the same
  constant for two value walks), `event/fact.go:249-251` (`sameValue` returns **true** on
  exhaustion), `event/fact.go:190` (`sharesMemory` returns **false** on exhaustion),
  `event/encodable.go:11-15` (`codecGraphNodes` is declared as a bound on the **type** graph and
  is `jobs`'s number for that)
- **What:** the fidelity comparison and the aliasing walk are both budgeted at 1024 visited
  values, and both exhaust in the caller's favour with no signal. Measured against this checkout
  with a codec that corrupts one element of a `[]int`:

  ```
  n=10     corrupts element 5      RoundTrip refuses  = true
  n=2000   corrupts element 5      RoundTrip refuses  = true
  n=2000   corrupts element 1500   RoundTrip refuses  = false
  n=5000   corrupts element 4999   RoundTrip refuses  = false
  ```

  The same budget governs `sharesMemory`, so the aliasing half is equally vacuous past leaf 1024.
  Nothing tells the caller which of the two happened: a green `RoundTrip` means either "the
  payload survives" or "the payload is bigger than the comparison walks", and those are different
  answers. The type walk at the same bound does the opposite and **refuses**
  (`encodable.go:76`, `:129`, `:174`: *"the type graph reached from %s is larger than the codec
  walks"*), so one section answers a budget exhaustion two ways.
- **Why this severity:** `medium` — it needs a defective codec before anything is lost, and the
  shipped `JSON` codec is not one; and a payload with more than 1024 leaves is realistic (a batch
  import, a line-item list, a map of SKUs) but not the default. It is not `low` because the
  plan's §D.13 budget table and `#### event/fact.go`'s "which of the two properties it proved"
  sentence are what a store author reads, and neither mentions a size at which the proof stops.
  It is also `architecture.md`'s "a check that cannot fail", one door in from GAP-180.
- **Why this timing:** `immediate` at this severity, which does not block the gate: the constant
  and the exhaustion policy are what S5's `eventtest.RoundTrip` and S6's module page are about to
  be written against, and `codecGraphNodes` sitting in `event/fact.go` is a magic threshold with
  no calibration for the quantity it is bounding there.
- **Close criteria:**
  - [ ] the two value walks take their own named constant — a value-walk budget, not
        `codecGraphNodes` — with the number justified in a sentence beside it
  - [ ] exhaustion is reported rather than resolved: `RoundTrip` answers `ErrSample` naming the
        sample as larger than the comparison walks (the same answer the type walk gives for the
        same reason), or it returns a verdict that says which of fidelity and non-aliasing it
        actually established
  - [ ] `roundtrip_test.go` carries the `n=2000, corrupts element 1500` case above, asserting the
        chosen answer, with the `n=10` case as its control; the case must answer `<nil>` on
        today's code
  - [ ] `#### event/fact.go`'s "which of the two properties it proved" sentence and the S6 module
        page name the size limit
- **Status:** closed 2026-09-08, round 5 — `event/comparison.go` (`valueWalkNodes = 1 << 16` with its justification, `valueWalk.beyond`, `valueWalk.unanswered`), `event/roundtrip_test.go` (*a sample larger than the walk is refused rather than reported as a pass*, carrying `n=2000 @ 1500` with `n=10 @ 5` as its control, plus the exhaustion case)

---

### GAP-190 [medium][deferred] A marker fact is legal at declaration and can never pass `Fact.RoundTrip`, so §UC-042's obligation is unsatisfiable for the most ordinary event shape there is

- **Where:** `event/fact.go:138-140` (the `bytes.Equal(written, zero)` refusal),
  `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md:1362-1363` (*"`struct{}` stays legal: a marker
  fact carries no data by design"*)
- **What:** the two doors of one section disagree. `event.JSON[struct{}]().CanEncode()` is `nil`
  and the walk explicitly keeps `struct{}` legal; `Fact.RoundTrip` then refuses **every** sample
  of such a fact, because a marker's only value encodes exactly as its zero value. Measured:

  ```
  type marker struct{}
  event.JSON[marker]().CanEncode()   → <nil>
  fact.New("i", marker{}).Err()      → <nil>
  fact.RoundTrip(marker{})           → event: this sample cannot prove what a round trip claims
                                        for it: … encodes its sample exactly as its own zero
                                        value, so nothing a second read could disturb is under test
  ```

  There is no other sample a caller could pass. `order.cancelled`, `invoice.voided`,
  `account.closed` — a fact whose whole content is that it happened — is one of the two shapes
  event sourcing has, and the framework's own runnable proxy for it always fails.
- **Why this severity:** `medium` — it fails closed, at test time, with a message that explains
  itself, and nothing is written or lost. It is not `low` because §UC-042 is a stated obligation
  and S5's `eventtest.RoundTrip` will turn this into a red conformance run for a correct
  declaration, which teaches a store author to skip the helper.
- **Why this timing:** `deferred` — no contract another section depends on changes, and the repair
  belongs beside the `*testing.T` wrapper that decides what a green run means. It must not be
  dropped: it is the difference between a suite that can be run over every declared fact and one
  that has to be run over a hand-picked subset.
- **Close criteria:**
  - [ ] a revision whose sample encodes as its zero value reports **fidelity proved,
        non-aliasing not under test** rather than `ErrSample` — either from `Fact.RoundTrip` or
        from `eventtest.RoundTrip` over it — so a marker fact can be round-tripped
  - [ ] `ErrSample` is kept for a sample that is *wrong* (wrong count, wrong dynamic type) and a
        test drives a marker fact through the helper and asserts it passes, with a payload-carrying
        fact whose sample happens to be the zero value as its control (that one is a caller mistake
        and must still be refused)
  - [ ] the module page (S6) says which facts the non-aliasing half cannot be run for
- **Status:** closed 2026-09-08, round 5, in S2 rather than deferred to S5 — `event/comparison.go` (`singleValued`), `event/fact.go` (the zero-value refusal conditioned on it, the non-aliasing half skipped for a single-valued reader type), `event/roundtrip_test.go` (*a fact whose whole content is that it happened round trips*, with `note{}` as its control). Its test half GAP-T33 closes with it

---

### Round 3 — what a re-audit found that round 2's fixes introduced, and what they did not close

Round 2's four repairs are correct in themselves and broke nothing: the surface is unchanged
at 22, the import graph is unchanged, `make check` and `make unit` are green, `-race` is green
three runs in a row, and the seal's fast path is measurably what it claims. **GAP-188 is the one
finding the fixes themselves introduced** — `sameValue`'s `Equal` arm, which was added to stop
`reflect.DeepEqual` from accusing a `time.Time`, is a call into application code with no panic
policy, a nil-receiver crash, and a veto over the very check it was added to make possible.

The other four are the same pattern round 2's own closing paragraph named, one round later:
**each repair was written to the route the finding named rather than to the rule the route was an
instance of.** GAP-178 said "promoted fields" and got promoted *field names* (GAP-185: promoted
*methods*); GAP-171/GAP-179 said "a type that declares no matching unmarshaller" and got the
missing-method case (GAP-186: a method that is present and inert); GAP-178's repair taught the
walk to follow embedded fields and did not ask which of them `encoding/json` can *write into*
(GAP-187). The rule none of the three repairs was written against is the one
`#### event/codec.go` already states and the walk does not implement: **"does a value of this type
survive `Encode` then `Decode` through this codec"** — five hand-derived arms now stand in for it,
and this round found three more shapes where the derivation and `encoding/json` disagree. The
round-2 disposition rejected a synthetic probe with five measured arguments, all of them sound;
the argument they do not answer is that the hand-derived model has now been wrong on six ordinary
shapes across three rounds, and the shipped codec is the only implementation whose routing the
kernel is obliged to model. Whatever mechanism closes GAP-185, GAP-186 and GAP-187 should be
chosen so that the *next* divergence is found by the walk rather than by a reviewer.

---

## Round 3 dispositions — 2026-09-07

Closed while the S2 test review's `[critical]` findings were being closed, because GAP-T21 is
their test half and cannot be green without them.

| Finding | Disposition |
|---|---|
| **GAP-185** | **closed.** `promotedMarshaller`, now in `event/routing.go`; refusal 6 of the walk. The finding's suggested accepting control — *the same shape with the embedded type tagged* — was **wrong** and is not used: a `json:"money"` tag does not undo Go's method promotion, and the tagged spelling still writes `500` and drops `SKU`. It is on the **refused** table with the untagged one, and the control is a named field. **Amended 2026-09-08:** criterion 2's shape (c), the text-pair route, was outstanding until S2 test round 3 (GAP-T41) — refused correctly all along, gated by nothing. |
| **GAP-186** | **closed.** `readsAs` and `objectKey`; refusal 7. |
| **GAP-187** | **closed**, and narrower than the close criterion's wording. The criterion asked the walk to refuse *"an anonymous field whose type is a pointer to an unexported struct type"*. Measured, that over-refuses: `type ring struct{ *ring; N int }` encodes `{"N":7}` and decodes back correctly, because the promoted `N` is shadowed by the outer one and `encoding/json` never has to allocate the pointer. The rule implemented is `encoding/json`'s own — refuse where a **rendered name** is reached through such a pointer — which refuses every shape the finding measured (`struct{ *hidden }`, `struct{ *inner; Note string }`, `pointed`) and keeps the two that work. Refusal 8. |
| **GAP-188** | still **open**. Not touched here: it is `[high][immediate]` in the *implementation* review and no test-review finding depends on it. `sameValue`'s `Equal` arm still runs before the nil guard and still vetoes the field walk. |
| **GAP-189** | still **open**, `[medium][immediate]`. |
| **GAP-190** | still **open**, `[medium][deferred]`; its test half is GAP-T33, now carried in the plan's `## Debt`. |

**What the three repairs cost the surface: nothing.** No exported symbol was added, removed or
changed; `event/encodable.go` grows from 290 to 379 lines and remains the section's longest file.
The walk's own contract comment and `#### event/codec.go`'s refusal list both go from five shapes
to eight, in the same change.

---

## Round 4 amendment — 2026-09-08 — GAP-185's depth, and where the walk lives

Appended while closing the S2 test review's round 5 cluster (GAP-T46, GAP-T47). No finding of this
file was reopened; two of its closing notes stopped being true and are corrected here rather than
edited into agreement.

| Note | Correction |
|---|---|
| **GAP-185** — *"`promotedMarshaller` in `event/encodable.go`; refusal 6 of the walk"* | The refusal was asked **one hop deep**: `struct{ B }` where `B` is `struct{ money; Extra string }` was accepted while `B` alone was refused, so every shape refusal 6 exists for was legal again the moment it was wrapped in one more struct — the `Audit`/`Timestamps` block a consumer factors out of several payloads. Closed in S2 test round 5 as **GAP-T46**: the question is asked along the whole promotion chain and the refusal names the hop it came through. |
| **the file it lives in** | `promotedMarshaller`, `ownMethods`, `besideIt`, `objectKey`, `writeRoute`, `readRoute`, `readsAs`, `writesAs`, `onTheValueReceiver` and the `route` methods are `event/routing.go` as of S2 test round 6 (GAP-T47); `event/encodable.go` keeps the walk — `chargeJSON`, `jsonWalk`, `position`, `members` and the three graph budgets. The single file had reached **422** lines against the 400-line budget, which this file's own round 3 note predicted at 379. The two are **212** and **256**, and no exported name moved. |

---

## Round 5 — remediation of the round-3 cluster — 2026-09-08

The three findings round 3 left open — GAP-188, GAP-189, GAP-190 — closed together, because all
three are `Fact.RoundTrip`'s comparison and two of them are the same sentence read twice: *what the
round trip could not establish it must not report as a pass.* Every transcript below was produced
in this worktree.

### Reproduction, before the fix

An in-package probe (`event/zzprobe_test.go`, deleted after) drove the reviewer's own inputs:

```
=== RUN   TestProbe188Nil
panic: runtime error: invalid memory address or nil pointer dereference
	event.(*probeNode).Equal(0x0?, ...)
	event.equalByMethod(...)                 event/fact.go:311
	event.sameValue(...)                     event/fact.go:256
	event.(*Fact[...]).roundTrip(...)        event/fact.go:153
	event.(*Fact[...]).RoundTrip(...)        event/fact.go:112

=== RUN   TestProbe188Lenient
    lenient Equal: out=[{Title:t Body:}] err=<nil>

=== RUN   TestProbe189Budget
    n=10   corrupts element 5:    refuses=true   (the control)
    n=2000 corrupts element 5:    refuses=true
    n=2000 corrupts element 1500: refuses=false  err=<nil>
    n=5000 corrupts element 4999: refuses=false  err=<nil>

=== RUN   TestProbe190Marker
    CanEncode(struct{}) = <nil>      TryDeclare = <nil>
    RoundTrip(marker{}) err=event: this sample cannot prove what a round trip claims for it:
      … encodes its sample exactly as its own zero value …
```

Byte for byte the four transcripts the findings carry.

### Dispositions

| Finding | Disposition |
|---|---|
| **GAP-188** | **closed** by narrowing where `Equal` is asked and recovering its panic. `valueWalk.equalByMethod` is now reached **only** from `sameFields`, and only where the struct has no exported field — the one shape the field walk has nothing to compare, and the shape `time.Time` is. The pointer arm's nil guard therefore runs before it by construction, so the nil-receiver call cannot be made at all; and a lenient application `Equal` can no longer veto a walk that had an answer. The call is recovered into `ErrSample` naming the type, and `event/codec.go`'s panic-policy paragraph carries the row (as does `[[D-124]]`, which is where the rule lives). Criterion 3 taken by the **narrowing** branch, not the disagreement branch. |
| **GAP-189** | **closed.** Both value walks take `valueWalkNodes = 1 << 16`, declared in `event/comparison.go` with the sentence that justifies it — a sample is a specimen, not a production payload, and `codecGraphNodes` bounds a *type* graph while one type has any number of values. `valueWalk` carries a `beyond` flag, and `RoundTrip` answers `ErrSample` naming the bound and the repair, which is the answer the type walk already gives for the same reason. The finding's `n=2000 @ 1500` case now refuses with `ErrPayload` — the genuine difference, found because the walk reaches it — and a sample of `valueWalkNodes + 8` values refuses with `ErrSample`. |
| **GAP-190** | **closed**, and not deferred to S5: the repair turned out to be one predicate in the kernel rather than a policy in the `*testing.T` wrapper. A reader type of **size zero** holds exactly one value; for one, the zero-value-sample refusal does not fire and the non-aliasing half is skipped — there is no second payload to disturb anything with — while fidelity still runs. `ErrSample` is kept for a sample that is *wrong*: the wrong count, the wrong dynamic type, and the zero value of a type that carries data. Its test half **GAP-T33** closes with it and is struck from the plan's `## Debt`. |

### What changed

- `event/fact.go` — `roundTrip` splits its two halves into `notAliased` and `readBack`; the
  zero-value refusal is conditioned on `singleValued`.
- `event/comparison.go` — **new file.** `event/fact.go` reached **403** lines against the 400-line
  budget, so the two walks moved out exactly as `event/routing.go` moved out of
  `event/encodable.go` in round 4. `fact.go` **193**, `comparison.go` **216**. Every symbol in it
  is unexported and `make api` regenerates `docs/api/surface.md` **byte-identical**, so
  `event/eventpg`'s zero-diff obligation is untouched: no exported symbol was added, removed or
  changed by any of the three repairs.
- `event/codec.go` — the panic-policy paragraph gains the `Equal` row it claimed to enumerate.
- `event/roundtrip_test.go` — five new subtests; the `weighed` fixture moved to an unexported
  field with a codec of its own, because under the narrowing its old exported-field spelling never
  reached the signature check it exists to pin; the diagnosis table goes from five rows to seven.
- Docs in the same change: `[[D-124]]` (the `Equal` row, two new *What it forbids* clauses, the
  file list, *Proven by*), `[[FL-036]]` and `docs/ai/flows/Index.md` (the new file),
  `[[UC-032]]` clause 4, `docs/modules/{en,ru}/event.md` and `docs/modules/{en,ru}/eventtest.md`
  (what a green round trip means, the size limit, and which facts the non-aliasing half cannot be
  run for), and `#### event/fact.go` in the plan, which gains clauses 6a, 6b and 6c.

### Mutation campaign — every repair is held by a named test

Each mutation applied to the shipped code, the suite run, the mutation reverted:

| Mutation | Killed by |
|---|---|
| `Equal` asked at every node again (the pre-fix call site) | `…/a pointer field is not asked its own type's Equal through a nil` — **panics**, exactly as before the fix |
| the same mutation, lenient-`Equal` subtest | `…/an application's own Equal does not decide whether the codec kept what it was handed` — `answered <nil>` |
| `recover` removed from `equalByMethod` | `…/an Equal that panics refuses the sample rather than unwinding` — **panics** |
| the signature check removed | `…/what an encoding drops by design is not what it lost` — the `weighed` row is refused |
| `valueWalkNodes` set back to `codecGraphNodes` | `…/a sample larger than the walk is refused rather than reported as a pass` — element 1500 of 2000 goes unreported |
| `unanswered` returning `nil` always | the same subtest, the `Equal`-panics subtest, **and** the diagnosis table |
| `singleValued` dropped from the zero-value guard | `…/a fact whose whole content is that it happened round trips` |
| `sameFields` never falling through to `equalByMethod` | `…/a codec that records less than it was handed is caught` (the `time.Time` case) and two more |

### Gate

`gofmt -l .` silent · `go build ./...` · `go vet ./event/...` · `go test -race -count=1 ./event/...`
green twice · `make unit` exit 0 · `make check` nine arms `ok` · `make api` byte-identical.

---

## Round 6 — econv-implementation-reviewer (clean context, re-audit of the round-5 cluster) — 2026-09-08

Scope: GAP-188, GAP-189, GAP-190 and the round-5 change that closed them
(`event/comparison.go` new, `event/fact.go`, `event/codec.go`,
`event/roundtrip_test.go`). Every transcript below was produced in this worktree
with an in-package probe (`event/zzaudit_test.go`, deleted after; the tree is
byte-identical to how it was found — `event/comparison.go` and `event/fact.go`
both md5-match the copies taken before the mutation campaign, `git status`
back to 251 entries).

### Gate, run here

`gofmt -l .` silent · `go vet ./event/...` exit 0 · `go build ./...` exit 0 ·
`go test -race -count=2 ./event/...` green (`event` 5.9 s, `eventmemory` 1.1 s,
`eventtest` 4.0 s). No flake in two runs.

### Metrics — counted

| Metric | Value |
|---|---|
| `event/comparison.go` | 216 lines, 10 functions, longest `shares` at 34 lines |
| `event/fact.go` | 193 lines, 10 functions, longest `roundTrip` at 35 lines |
| longest non-test file under `event/` | `errors.go` 369, then `repo.go` 269, `routing.go` 256 — all under the 400-line budget |
| exported declarations in `event/comparison.go` | **0** (`grep -cE "^(func\|type\|const\|var) [A-Z]"`) |
| exported declarations changed in `fact.go` + `codec.go` | **0** (`git diff HEAD -- event/fact.go event/codec.go \| grep -E "^[+-](func\|type\|const\|var) [A-Z]" \| wc -l`) |
| kernel imports of / branches on a concrete store | **0** (`grep -rn "eventmemory\|eventpg" event/*.go \| grep -v _test.go \| wc -l`) |
| functions over the 4-parameter ceiling | **2** — see GAP-194 |

### Microkernel — derived, not inherited

`event/eventpg`'s zero-diff obligation is **intact**. Every value a store must
construct is still constructible from outside package `event`: `Envelope`,
`Stream`, `Record`, `AppendRequest`, `Limits` and `Capabilities` have no
unexported field; `Backing` and `Authority` have `NewBacking` and `NewAuthority`;
failures are `Failure(outcome, cause)`. The empirical proof is that
`event/eventmemory` — a package outside `event` — already constructs all of them
(`event/eventmemory/append.go:49` `event.Envelope{…}`, `:25` `event.Failure(…)`).
The round-5 change added one file whose every symbol is unexported and changed no
exported signature, so `docs/api/surface.md` cannot have moved.

### The three findings, verified against the code

| Finding | Verdict |
|---|---|
| **GAP-188** criterion 1 (nil guard before `Equal`) | **closed.** `equalByMethod` is reached from `sameFields` alone (`comparison.go:170`), and `same`'s `Pointer`/`Interface` arm (`:143-147`) nil-guards before it can descend. Probe: `referring{Ref *target, Note string}` with `Equal` on `*target` — `err=<nil>`, no panic. |
| **GAP-188** criterion 2 (a panic policy, and the table's row) | **closed.** `equalByMethod:210-214` recovers into `panicked`; `unanswered:73` renders it as `ErrSample` naming the type; `event/codec.go:31-34` carries the row. |
| **GAP-188** criterion 3 (the arm must not decide the verdict on its own) | **closed for the override, open for the omission.** The narrowing branch was taken and it works: `document.Equal` comparing titles no longer vetoes the field walk. But for the shape the narrowing hands to `Equal`, the *absence* of `Equal` is now reported as agreement — GAP-191 below. |
| **GAP-188** criterion 4 (clause 6 of `#### event/fact.go`) | **closed.** Plan clause 6a states where `Equal` is asked and why there is no disagreement case. |
| **GAP-189** all four criteria | **closed.** `valueWalkNodes = 1 << 16` with its justification at `comparison.go:41-45`; `beyond`/`unanswered` at `:58-78`; the `n=2000 @ 1500` case with `n=10 @ 5` as its control at `roundtrip_test.go:649-663`; the size limit is on `docs/modules/en/event.md:263`. |
| **GAP-190** all three criteria | **closed.** `singleValued` at `comparison.go:13-16`, the guard at `fact.go:145`, the marker test with `note{}` as its control at `roundtrip_test.go:676-691`, and `docs/modules/en/event.md:256-261` names which facts the non-aliasing half cannot be run for. Criterion 2's "through the helper" is satisfied by `Fact.RoundTrip`, which criterion 1 explicitly allows; `eventtest.RoundTrip` (`event/eventtest/proxies.go:16-31`) is a pass-through with no policy of its own, so a copy of the case there would re-test the kernel and nothing else. |

### Mutation campaign — re-run here, not inherited

Each mutation applied to the shipped code, the suite run, the mutation reverted
and the file md5-checked back:

| Mutation | Result |
|---|---|
| `Equal` asked at every node again (before the `switch` in `same`) | **killed** by *…a pointer field is not asked its own type's Equal through a nil* and *…an application's own Equal does not decide whether the codec kept what it was handed* |
| `recover` removed from `equalByMethod` | **killed** by *…an Equal that panics refuses the sample rather than unwinding* — panics, `spanned.Equal` index out of range |
| `valueWalkNodes = codecGraphNodes` | **killed** by *…a sample larger than the walk is refused rather than reported as a pass* — element 1500 of 2000 goes unreported |
| `unanswered` returning `nil` always | **killed** by three subtests, including the diagnosis table |
| `singleValued` dropped from the zero-value guard | **killed** by *…a fact whose whole content is that it happened round trips* |
| `sameFields` never falling through to `equalByMethod` | **killed** by *…a codec that records less than it was handed is caught* and two more |

The round-5 mutation table is honest. Every repair is held by a named test.

---

### GAP-191 [high][immediate] A struct with no exported field and no `Equal(T) bool` is compared by nothing and reported as a pass, so the narrowing that closed GAP-188 left the check optional by omission

- **Where:** `event/comparison.go:167-170` (`sameFields` falls through to
  `equalByMethod` when nothing was compared), `event/comparison.go:202-209`
  (`equalByMethod` returns **true** when the name is absent or the signature does
  not match), `event/fact.go:84-88` (the `RoundTrip` contract comment that claims
  the opposite)
- **What:** the narrowing made `Equal` the **only** answer for a struct with no
  exported field. When the type does not declare one, `equalByMethod` returns
  `true` and `RoundTrip` reports a pass having compared nothing. Measured:

  ```
  located{IP netip.Addr; Note string}
    JSON[located]().CanEncode()                     → <nil>
    codec sets read.IP = netip.Addr{} (drops it)
    RoundTrip(located{IP: 10.0.0.1, Note: "n"})     → err=<nil>  out=[{IP:invalid IP Note:n}]

  hushed struct{ marks []int64 }   (no exported field, no Equal)
    sample {1,2,3}, codec decodes {99,99}
    RoundTrip                                        → err=<nil>  out=[{marks:[99 99]}]

  heldTight struct{ marks []int64 }, func (*heldTight) Equal(*heldTight) bool
    a pointer-receiver Equal is not in the value's method set, so it is never found
    codec drops everything
    RoundTrip(heldTight{marks: []int64{7}})          → err=<nil>  out=[{marks:[]}]
  ```

  `netip.Addr`, `netip.Prefix`, `big.Int`, `big.Rat` and every application value
  object that keeps its state privately and does not happen to spell
  `Equal(T) bool` are that shape. Swap `time.Time` for `netip.Addr` in the
  suite's own `stamp`/`forgetfulCodec` case — the one that pins *"a codec that
  wrote the note and forgot the time"* — and it goes green.

  The file's own stated policy is at `comparison.go:48-51`: *"a walk that stopped
  early has compared nothing past where it stopped, and answering the caller's
  way there is a check that cannot fail. What went unanswered is carried out
  rather than resolved."* `valueWalk` already carries the machinery (`beyond`,
  `panicked`, `unanswered`) and applies it to budget exhaustion and to a
  panicking `Equal`. This is the third unanswered route and it is resolved in the
  caller's favour instead.
- **Why this severity:** `high`. This is the exact defect GAP-178's third close
  criterion added the comparison to catch and GAP-188's own text named — *"it
  makes the check it was added for optional"*. The narrowing removed the
  **override** and left the **omission**, and the omission is the wider case: a
  lenient `Equal` needs a consumer to have written one, while no `Equal` at all
  is the default. It is a `universality.md` finding: whether the only check that
  says a codec recorded what it was handed runs at all is decided by whether the
  payload type happens to declare a method with a fixed name and signature — the
  mechanism is "the sample looked like `time.Time`". It is not `critical` because
  it needs a defective codec of the consumer's own before anything is lost; the
  shipped `JSON` codec does not drop such a field.
- **Why this timing:** `immediate`. A `universality.md` finding is never
  deferred. `eventtest.RoundTrip` is the report a store author reads and it
  reports a pass. And the repair changes what a green round trip means, which
  `docs/modules/{en,ru}/event.md` and `#### event/fact.go` clause 6 both state —
  so it is cheaper before S5/S6 depend on the current sentence than after.
- **Close criteria:**
  - [ ] `sameFields` records "nothing compared" on the walk — a third arm beside
        `beyond` and `panicked` — when the struct has no exported field and
        `equalByMethod` did not run, and `RoundTrip` answers `ErrSample` naming
        the type and the repair
  - [ ] a **zero-size** reader type is excluded from that arm, so GAP-190 stays
        closed: *…a fact whose whole content is that it happened round trips*
        must stay green, and a mutation that drops the exclusion must turn it red
  - [ ] `roundtrip_test.go` carries the `netip.Addr` case above — a codec that
        drops a field whose type keeps its state privately and declares no
        `Equal` — asserting the chosen answer, with the existing
        `stamp`/`time.Time` case as its control, so the repair cannot be made by
        refusing every all-unexported struct
  - [ ] a control pins that a **pointer-receiver** `Equal` is not silently taken
        as an answer (`heldTight` above)
  - [ ] `event/fact.go:84-88` stops claiming that *"a codec that drops part of
        what it was given … [is] found before a stream contains one"* unqualified,
        and `docs/modules/{en,ru}/event.md`'s *"and by nothing if it does not"* is
        replaced by whatever the code now does
- **Status:** open

---

### GAP-192 [high][immediate] A payload reached through an interface panics `reflect` out of `Fact.RoundTrip`, which is the failure GAP-188 was closed for, on a route the closure never covered

- **Where:** `event/comparison.go:158-165` (`sameFields` indexes `second` by
  `first`'s field count), `event/comparison.go:182-189` (`sameEntries` indexes
  `second` with a key of `first`'s key type), `event/fact.go:111` (`RoundTrip`
  has no `recover`), `event/eventtest/proxies.go:16-31` (the conformance wrapper
  has none either)
- **What:** two raw runtime panics unwind out of exported kernel API. Measured:

  ```
  boxed struct{ Meta any }
    sample  boxed{Meta: narrow{A: 1}}          (1 field)
    codec   decodes boxed{Meta: wide{A,B,C}}   (3 fields)
    RoundTrip → panic: reflect: Field index out of range

    sample  boxed{Meta: map[string]int{"a": 1}}
    codec   decodes boxed{Meta: map[int]string{1: "a"}}
    RoundTrip → panic: reflect.Value.MapIndex: value of type int is not assignable to type string
  ```

  `sameFields` walks `first.NumField()` — the **decoded** value's shape — and
  indexes `second`, the sample, with it. `sameEntries` does the same with map
  keys. Two values only reach those arms with different `reflect.Type` when they
  are reached through an interface, and the shipped codec refuses an interface
  field at declaration with *"declare the concrete type **or a codec of your
  own**"* — so this is the path the kernel itself points a consumer down, and
  `Codec[V]` is the extension point that exists to admit it.

  GAP-188's disposition argued only the nil route: *"The pointer arm's nil guard
  therefore runs before it by construction, so the nil-receiver call cannot be
  made at all."* The panic route through mismatched dynamic types was never
  closed, and in `eventtest` it aborts the conformance binary rather than
  reporting a refusal — the S5 consequence GAP-188 itself named.
- **Why this severity:** `high`, on exactly the reasoning GAP-188(1) was rated
  `high`: it fails loudly and writes no wrong fact by itself, but a raw runtime
  panic unwinds out of an exported kernel method with no diagnosis, in the one
  helper whose whole job is to diagnose. It is not `critical` because it needs a
  consumer-written codec over an interface-typed payload.
- **Why this timing:** `immediate`. Same grounds as GAP-188: exported kernel API,
  and S5's `eventtest.RoundTrip` is written directly over this algorithm.
- **Close criteria:**
  - [ ] `same` (and `shares`) never index or key one value with the other's
        shape: two valid values whose `reflect.Type` differs are answered, not
        walked
  - [ ] both measured cases are in `roundtrip_test.go` asserting a refusal rather
        than a panic, with a same-dynamic-type interface payload as the control,
        so the repair cannot be made by refusing every interface field
  - [ ] a mutation that restores the current indexing turns those cases red
  - [ ] whichever answer is chosen is stated in `event/codec.go`'s policy
        paragraph or in `#### event/fact.go` clause 6, because that paragraph
        claims to enumerate what the kernel does with a call into code it was
        promised nothing about
- **Status:** open

---

### GAP-193 [medium][immediate] Two valid values of different kinds are reported as the same value, so a codec that substitutes one thing for another passes

- **Where:** `event/comparison.go:133-135`
- **What:**

  ```go
  if !first.IsValid() || !second.IsValid() || first.Kind() != second.Kind() {
      return first.IsValid() == second.IsValid()
  }
  ```

  When both are valid and the kinds differ this returns **true** — "the same
  value". Measured:

  ```
  bagged struct{ Meta any; Note string }
    sample  Meta = map[string]any{"a": 1}
    codec   decodes Meta = "dropped"
    RoundTrip → err=<nil>  out=[{Meta:dropped Note:n}]

  boxed struct{ Meta any }
    sample  Meta = [3]int{1,2,3}
    codec   decodes Meta = [3]string{"x","y","z"}
    RoundTrip → err=<nil>  out=[{Meta:[x y z]}]
  ```

  The arm presumably exists for the widening a JSON codec legitimately produces —
  an `int` in an `any` read back as a `float64`, which also answers `<nil>` here.
  The two are opposite answers and the code cannot tell them apart, because they
  take the same branch.
- **Why this severity:** `medium` — reachable only through an interface-typed
  field or element, which the shipped codec refuses at declaration, so it needs a
  codec of the consumer's own. It is not `low` because a whole substituted value
  is reported as fidelity proved, which is the same class of wrong answer as
  GAP-191 through a different arm, and because `[3]int` → `[3]string` is not a
  representation difference by any reading.
- **Why this timing:** `immediate` — it is the same arm and the same repair as
  GAP-192, and doing them separately means touching `same` twice.
- **Close criteria:**
  - [ ] a kind mismatch between two valid values is either a difference
        (`ErrPayload`) or unanswered (`ErrSample`), never agreement
  - [ ] the numeric widening a codec legitimately produces is named and handled
        explicitly rather than by falling into the same branch
  - [ ] `roundtrip_test.go` carries the mangled-`any` case with the widened
        numeric case as its control
- **Status:** open

---

### GAP-194 [medium][deferred] Two helpers take five parameters against `architecture.md`'s ceiling of four

- **Where:** `event/comparison.go:18`
  (`reusesItsBuffer(read, own, written, zero, walk)` — 5),
  `event/fact.go:163`
  (`notAliased(read, own, written, zero, revision)` — 5)
- **What:** counted, both are 5 against `architecture.md:54` — *"Function
  parameters | <= 4"*. Four of the five in each are one concept: the revision
  under test and the three byte strings its round trip produced (`written`,
  `zero`, and what `own` was decoded from). `roundTrip` (`fact.go:127-161`)
  assembles all of them and hands them on twice.
- **Why this severity:** `architecture.md`'s table puts a measured-threshold
  breach at `high`; I am calling it `medium` and saying why rather than
  inflating it — both functions are unexported, both are short (22 and 11
  lines), no contract crosses them, and the repair is a body refactor with no
  behavioural surface. Nothing fails on any input because of it.
- **Why this timing:** `deferred` — module-internal, no external contract, and
  nothing in S5 or S6 bends around it.
- **Close criteria:**
  - [ ] both are at 4 parameters or fewer, e.g. by a small unexported value
        carrying the revision's `read`, `written` and `zero`
  - [ ] `go test -race ./event/...` unchanged and no exported symbol moves
- **Status:** open

---

### What is genuinely clean and worth saying so

- **GAP-189 and GAP-190 are fully closed**, by the code and by tests that go red
  when the code is reverted. I ran both reverts here.
- **GAP-188 is three-quarters closed.** The nil-receiver crash it named is
  structurally impossible now, not merely guarded: `equalByMethod` has exactly
  one caller and the pointer arm cannot reach it through a nil. The panic policy
  is real and `event/codec.go` carries the row it claimed to enumerate.
- **The suite does not pass vacuously.** Every one of the five new subtests
  carries its own control, and the controls are the right ones: the `time.Time`
  case is beside the lenient-`Equal` case so neither half of the narrowing can be
  made by breaking the other, and `marksOf(8)` is beside `marksOf(valueWalkNodes+8)`.
- **No exported symbol moved**, so `event/eventpg`'s zero-diff obligation is
  untouched. Derived above, not inherited.
- **House style** holds in `event/comparison.go`: six comment blocks, each on
  either a non-trivial walk or an invariant the code cannot show (why
  `reflect.DeepEqual` is wrong, why 65 536, why size zero means one value). None
  restates code and none exists because a symbol is exported — every symbol in
  the file is unexported.

---

## Round 7 — remediation of the round-6 cluster — 2026-09-08

The four findings round 6 left open — GAP-191, GAP-192, GAP-193, GAP-194 — closed
together. The first three are one position in `event/comparison.go` reached three
ways, and doing them separately means touching `same` three times, which is what
GAP-193's own timing argument said.

### Reproduced first, in this worktree, before anything was changed

An in-package probe (`event/zzprobe_test.go`, deleted after) drove each finding's
own input through `Fact.RoundTrip`. What it printed:

```
191 netip.Addr dropped                   err=<nil> out=[{IP:invalid IP Note:n}]
191 hushed, no exported field, no Equal  err=<nil> out=[{marks:[99 99]}]
191 pointer-receiver Equal               err=<nil> out=[{marks:[]}]
192 struct widened behind an interface   PANIC reflect: Field index out of range
192 map rekeyed behind an interface      PANIC reflect.Value.MapIndex: value of type int is not assignable to type string
193 map replaced by a string             err=<nil> out=[{Meta:dropped Note:n}]
193 [3]int read back as [3]string        err=<nil> out=[{Meta:[x y z]}]
193 control: int widened to float64      err=<nil> out=[{Meta:1}]
```

and the two routes GAP-192 named in `shares`, which round 6 measured only through
`same`, panic as well — a codec whose two answers for one payload have different
dynamic types drives the **aliasing** walk into the same indexing:

```
192 shares walks a struct by the other's shape  PANIC reflect: Field index out of range
192 shares keys a map by the other's key        PANIC reflect.Value.MapIndex: value of type string is not assignable to type int
```

After the change, the same eight inputs:

```
191 netip.Addr dropped                   ErrPayload  does not read back the event.located it was given
191 hushed, no exported field, no Equal  ErrSample   carries a event.hushed: … declares no Equal(event.hushed) bool …
191 pointer-receiver Equal               ErrSample   carries a event.heldTight: … declares no Equal(event.heldTight) bool …
192 struct widened behind an interface   ErrPayload  does not read back the event.boxed it was given
192 map rekeyed behind an interface      ErrPayload  does not read back the event.boxed it was given
193 map replaced by a string             ErrPayload  does not read back the event.bagged it was given
193 [3]int read back as [3]string        ErrPayload  does not read back the event.boxed it was given
193 control: int widened to float64      err=<nil>   out=[{Meta:1}]
192 shares (both routes)                 err=<nil>   no panic
```

### What changed

`event/comparison.go`. The position `Equal` was asked at now has **three answers
in a fixed order** — `sameOpaque`: a type of size zero (one value, nothing to
lose), then the type's own `Equal(T) bool`, then `==` where the type is
comparable, then **none**, recorded on the walk as `incomparable` and carried out
by `unanswered` as `ErrSample` naming the type and the repair. `equalByMethod`
returns `(answered, same)` instead of resolving its own absence as agreement.
`same` and `shares` compare `reflect.Type` rather than `reflect.Kind` and answer a
difference instead of walking one value by the other's shape; the numeric
widening that arm used to swallow is `sameNumber`, a named arm of its own.
`event/fact.go` grew `roundTripping`, which takes `notAliased` and
`reusesItsBuffer` from five parameters to three.

### Gate, run here

`gofmt -l .` silent · `go build ./...` exit 0 · `go vet ./event/...` exit 0 ·
`go test -race -count=1 ./event/...` green · `make unit` green · `make check`
green.

### Mutation campaign — nine mutations, each applied to the shipped code, the
suite run, the mutation reverted and `event/comparison.go` md5-checked back to
`8cf623edad2ca590e0af2e23be394aee`

| Mutation | Result |
|---|---|
| `same` guards on `Kind` again (the round-6 spelling) | **killed** — *a value substituted for another behind an interface is answered, not walked* panics `reflect: Field index out of range` |
| `shares` guards on `Kind` again | **killed** — the same subtest, through the two `inTurn` rows |
| `sameOpaque` reports agreement where nothing compared | **killed** by *a struct nothing can compare…* and by the diagnosis row *a payload nothing can compare* |
| `sameOpaque` drops the `==` arm | **killed** by *a struct nothing can compare…* (the `netip.Addr` row) and by *what an encoding drops by design is not what it lost* (`weighed`) |
| `sameOpaque` drops the zero-size exclusion | **killed** by *a fact whose whole content is that it happened round trips* — the `witnessed` row, whose type forbids `==` |
| `sameNumber` always agrees | **killed** by the substitution table and by the diagnosis row |
| `sameNumber` never agrees | **killed** by the widened-number control |
| `equalByMethod` stops checking the signature | **killed** by *what an encoding drops by design…* — `weighed.Equal(int64, int64)` panics and becomes `ErrSample` |
| `equalByMethod` takes a missing `Equal` for agreement | **killed** by *a struct nothing can compare…* and the diagnosis row |

### Dispositions

| Finding | Disposition |
|---|---|
| **GAP-191** | **closed** by `sameOpaque`'s three answers. Criterion 1: `sameFields` falls through to `sameOpaque`, which records `incomparable` on the walk beside `beyond` and `panicked`, and `unanswered` renders it as `ErrSample` naming the type and both repairs. Criterion 2: `singleValued(first.Type())` is the first arm, and it is load-bearing rather than decorative — `struct{ _ [0]func() }` is size zero and **not** comparable, so the new `witnessed` row in *a fact whose whole content is that it happened round trips* goes red when the arm is dropped. Criterion 3: the `netip.Addr` case is `roundtrip_test.go`'s *a struct nothing can compare is refused rather than reported as a pass*, and the chosen answer is **`ErrPayload`, a difference** rather than `ErrSample` — because the `==` arm can compare a `netip.Addr` and does, which is a stronger close than refusing it; its two controls are a faithful codec over the same type and the `time.Time` case, so the repair cannot be made by refusing every all-unexported struct. Criterion 4: `heldTight`, whose `Equal` sits on the pointer receiver and is therefore not in the value's method set, is refused with `ErrSample` naming it. Criterion 5: `event/fact.go:88-94`, `docs/modules/{en,ru}/event.md` and `docs/modules/{en,ru}/eventtest.md` all say what the position does now; the `and by nothing if it does not` sentence is gone. `[[D-124]]` carries the rule and the forbid, and plan clause **6d** carries the contract. |
| **GAP-192** | **closed** by comparing `reflect.Type` in both walks. Criterion 1: `same:147` answers a type difference through `sameNumber` and never enters a `switch` arm with it; `shares:87` returns false. Criterion 2: both measured cases are rows of *a value substituted for another behind an interface is answered, not walked*, and the same-dynamic-type rows (`narrow` for `narrow`, `map[string]int` for `map[string]int`) are the controls, so the repair cannot be made by refusing every interface field. The two `shares` routes round 6 did not measure are rows too, driven by `inTurn`. Criterion 3: the mutation table above, rows 1 and 2. Criterion 4: plan clause **6e** and `[[D-124]]` state the answer; `event/codec.go`'s paragraph is about the panic policy and keeps its `Equal` row unchanged, because nothing here is a call into application code. |
| **GAP-193** | **closed** with GAP-192, one repair. Criterion 1: two valid values of different types are a difference. Criterion 2: `sameNumber` is the named arm, and it compares the numbers rather than waving them through — `1` read back as `float64(2)` is refused. Criterion 3: the mangled-`any` and `[3]int`→`[3]string` rows sit in the same table as the widened-number control. |
| **GAP-194** | **closed**, not deferred: `roundTripping{read, written, zero, revision}` is one concept and `notAliased` and `reusesItsBuffer` are at **three** parameters each. No exported symbol moved and `go test -race ./event/...` is unchanged. |

### What this round changed in the tests, and why

`sealedNote` gained `Equal(sealedNote) bool` on the value receiver. It is the
fixture whose contents are unreachable to the **aliasing** walk, which is what
*a codec that reaches its buffer through a field no comparison walks is caught
too* is about; under GAP-191's repair its allocating control was refused as
uncomparable, which would have replaced a `nil`-vs-`ErrPayload` discriminator
with an `ErrSample`-vs-`ErrPayload` one. Declaring the `Equal` keeps the
aliasing half blind to `body` — `shares` walks exported fields alone — and lets
fidelity answer, so the subtest still pins what it was written to pin.

### Zero-diff obligation

Intact. Every symbol added is unexported (`sameOpaque`, `sameNumber`, `asFloat`,
`roundTripping`, `valueWalk.incomparable`), no exported signature moved, and
`docs/api/surface.md` regenerates byte-identical.

---

## Round 8 — econv-implementation-reviewer (clean context, re-audit of the round-7 cluster) — 2026-09-08

Scope: the GAP-188 / GAP-189 / GAP-190 cluster and everything round 5 and round 7
changed to close it and its follow-ons (`event/comparison.go`, `event/fact.go`,
`event/codec.go`, `event/roundtrip_test.go`). Every transcript below was produced
in this worktree by an in-package probe (`event/zzaudit8_test.go`,
`zzaudit8b_test.go`, `zzaudit8c_test.go`) and an out-of-module probe
(`/tmp/zzmk`), all deleted afterwards. The tree is byte-identical to how it was
found: `git status --porcelain | wc -l` = 251 before and after,
`event/comparison.go` md5 `8cf623edad2ca590e0af2e23be394aee`, `event/fact.go`
`078d1fa59687c92a78da8e688fedf45f`, `event/codec.go`
`52e480c9cbd64bfa526601f3772ce9b6`, `event/roundtrip_test.go`
`ab738f167671f0dba0f9b41983c73759`.

### Gate, run here

`gofmt -l .` silent · `go vet ./event/...` exit 0 · `go build ./...` exit 0 ·
`go test -race -count=2 ./event/...` green twice in a row (`event` 5.95 s / 5.94 s,
`eventmemory` 1.09 s, `eventtest` 4.01 s / 4.04 s — no flake) · `make check`
green (`check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`,
`check-replaces`, `check-tidy`, `check-otel-schema`, `check-workspace` all `ok`) ·
`make api` regenerates `docs/api/surface.md` **byte-identical** (`diff` empty).

### Metrics — counted, not eyeballed

| Metric | Command | Value |
|---|---|---|
| `event/comparison.go` | `wc -l` | 280 |
| `event/fact.go` | `wc -l` | 205 |
| `event/codec.go` | `wc -l` | 106 |
| longest non-test file under `event/` | `wc -l event/*.go` | `errors.go` 369, then `comparison.go` 280, `repo.go` 269 — all under 400 |
| longest function in `comparison.go` | awk over `^func`…`^}` | `shares` 34 lines |
| longest function in `fact.go` | same | `roundTrip` 34 lines |
| exported declarations in `comparison.go` | `grep -cE "^(func\|type\|const\|var) [A-Z]"` | **0** |
| non-test functions in `event/` over 4 parameters | regex scan of every `^func` | **0** — GAP-194 closed |
| kernel imports of / branches on a concrete store | `grep -rn "eventmemory\|eventpg\|eventtest" event/*.go \| grep -v _test.go \| wc -l` | **0** |
| extensions importing each other | `grep -rn "eventtest" event/eventmemory/*.go`, `grep -rn "eventmemory" event/eventtest/*.go`, non-test | **0** and **0** |

### Microkernel — derived here, not inherited

**`event/eventpg`'s zero-diff obligation is intact.** Derived by writing a
throwaway module `zzmk` (its own `go.mod`, a `replace` onto this checkout, so
nothing in `event/` could be relied on) that implements the full `event.Store`
interface and returns every value the contract makes a store construct:
`Capabilities{Transactions: event.Supported, …}`, `Limits{…}`,
`NewBacking(identity)`, `NewAuthority(backing, tx)`, `Envelope{Stream, Version,
Position, Type, Revision, Payload, RecordedAt}`, `Record{Type, Revision,
Payload}`, `AppendRequest{Stream, Expected, Records}`, `Cursor`, and
`Failure(NotWritten, cause)` / `Failure(Conflict, cause)`. It compiles and its
`var _ event.Store = store{}` assertion holds. Nothing a store must build is
gated behind an unexported field or a package-internal constructor.

### The three cluster findings, verified against the code

Each verified by constructing the finding's own input and by reverting the repair
in place, running the suite, and restoring the file to its md5.

| Finding | Verdict |
|---|---|
| **GAP-188 (1) — nil-receiver `Equal` panic** | **closed.** `equalByMethod` (`comparison.go:265`) has exactly one caller, `sameOpaque:255`, itself reached only from `sameFields:209` where no exported field was compared; `same`'s `Pointer`/`Interface` arm (`:157-160`) nil-guards before descending. Probe: `referring{Ref: nil, Note: "hello"}` → `err=<nil>`, no panic. **Mutation M1** (ask `Equal` at every node before the `switch`, the round-2 spelling) → *a pointer field is not asked its own type's Equal through a nil* red. |
| **GAP-188 (2) — no panic policy, no row in the table** | **closed.** `equalByMethod:274-278` recovers into `walk.panicked`; `unanswered:73` renders `ErrSample` naming the type; `codec.go:31-34` carries the row and it matches the code ("asks of a struct whose every field is unexported, and there alone"). **Mutation M2** (drop the `recover`) → *an Equal that panics refuses the sample rather than unwinding* panics with `index out of range [0] with length 0`. |
| **GAP-188 (3) — an application `Equal` disables the fidelity check** | **closed for the shape the finding measured.** `document{Title, Body}` with a title-only `Equal` and a body-dropping codec → `ErrPayload`. Mutation M1 also turns *an application's own Equal does not decide whether the codec kept what it was handed* red. Residual at the narrowed position measured and recorded as GAP-197 below. |
| **GAP-189 — both walks on the type-graph budget, exhausting in the caller's favour** | **closed.** `valueWalkNodes = 1 << 16` at `comparison.go:44` with its justification at `:40-43`; `beyond` / `unanswered` at `:58-80`; `notAliased` and `readBack` (`fact.go:181`, `:192`) both build `valueWalk{budget: valueWalkNodes}` and both carry the answer out. **Mutation M4** (`valueWalkNodes = codecGraphNodes`) → *a sample larger than the walk is refused rather than reported as a pass* red on the `n=2000 @ 1500` row. **Mutation M5** (`unanswered` returns `nil` always) → four subtests red. |
| **GAP-190 — a marker fact can never satisfy UC-042** | **closed.** `singleValued` at `comparison.go:13-15`; the zero-value refusal conditioned on it at `fact.go:146`; the non-aliasing half skipped at `:152`. Probe: `RoundTrip(voided{})` → `err=<nil>`, one value carried. **Mutation M6** (drop `singleValued` from the `fact.go` guard) and **M7** (drop the zero-size arm from `sameOpaque`) each turn *a fact whose whole content is that it happened round trips* red — M7 through the `witnessed` row, whose type forbids `==`, so the arm is load-bearing rather than decorative. |
| **GAP-191 — the absence of `Equal` reported as agreement** | **closed** at the position the finding named. `sameOpaque:251-263` answers in three steps and records `incomparable` where there is none. **Mutation M9** (`sameOpaque` reports agreement when nothing compared) and **M10** (drop the `==` arm) both turn *a struct nothing can compare…* red. New consequence recorded as GAP-195 below. |
| **GAP-192 / GAP-193 — one value walked by the other's shape / two valid values of different kinds called equal** | **closed.** `same:147` and `shares:92` compare `reflect.Type`. **Mutation M8** (guard on `Kind` again) → *a value substituted for another behind an interface is answered, not walked* panics `reflect: Field index out of range`. **Mutation M11** (`sameNumber` always agrees) → the same subtest plus the diagnosis row red. |
| **GAP-194 — five-parameter helpers** | **closed.** `notAliased(carried, own)` = 2, `reusesItsBuffer(carried, own, walk)` = 3. A regex scan of every `^func` in `event/*.go` (non-test) finds **zero** functions over four parameters. |

Nine mutations applied to the shipped code and reverted, `event/comparison.go`
and `event/fact.go` md5-checked back after each. **Every one was killed by a
named test.** The round-5 and round-7 mutation tables are honest.

---

### GAP-195 [high][immediate] The GAP-191 repair made a payload carrying a `math/big` value permanently unprovable: a declaration the kernel accepts, over the kernel's own shipped codec, which round-trips perfectly, is refused forever with a repair the consumer cannot perform

- **Where:** `event/comparison.go:251-263` (`sameOpaque` — `incomparable` is the
  answer when a struct has no exported field, declares no `Equal(T) bool` on the
  value receiver, and is not comparable), `event/comparison.go:75`
  (`unanswered`'s message and the two repairs it names),
  `docs/modules/en/event.md:250-258` (which names `big.Int` and offers the same
  two repairs), `docs/ai/decisions/D-124…md:157-173`
- **What:** round 7 closed GAP-191 by turning "nothing compared → pass" into
  "nothing compared → `ErrSample`". D-124 and the module page both justify that
  with a list — *"`netip.Addr`, `netip.Prefix`, `big.Int` and every value object
  that keeps its state privately"* — but that list holds two different classes
  and only one of them the `==` arm can answer. `netip.Addr` and `netip.Prefix`
  are **comparable**, so `==` compares them and the fidelity check works.
  `big.Int`, `big.Rat` and `big.Float` hold a slice, so they are **not**
  comparable and declare no `Equal`, and `sameOpaque` falls to `incomparable`
  for them **whatever the codec did**. Measured in this worktree:

  ```
  reflect: big.Int   NumField=2  field 0 exported=false  Comparable=false  hasEqual=false
           big.Rat   Comparable=false  hasEqual=false
           big.Float Comparable=false  hasEqual=false

  JSON[credited]().CanEncode()                      -> <nil>          (declaration accepts it)
  Declare(a, "credited", From(JSON[credited]()), …) -> ok             (no refusal at any door)
  fact.RoundTrip(credited{Amount: big.NewInt(1234), Note: "n"})
     through the SHIPPED JSON codec, which encodes and decodes it perfectly
     -> event: this sample cannot prove what a round trip claims for it: …
        carries a big.Int: every field of it is unexported, it declares no
        Equal(big.Int) bool on the value receiver and == cannot compare it …
        declare that Equal, or record what the fact carries in an exported field
  ```

  **Neither repair the message names exists for this type.** You cannot declare a
  method on `math/big.Int` from your own package. And wrapping does not help:
  the walk descends through the exported field into `big.Int` regardless, so
  declaring `Equal` on the payload struct itself is not reached — `sameOpaque`
  runs only where a struct has *no* exported field. Measured:

  ```
  type owned struct{ Amount *big.Int; Note string }
  func (this owned) Equal(other owned) bool { … Amount.Cmp … }   // the obvious workaround
  fact.RoundTrip(owned{Amount: big.NewInt(5), Note: "n"})
     -> the same ErrSample naming big.Int
  ```

  The only escape is to stop carrying a `math/big` value in the payload at all.
- **Why this severity:** `high`. This is GAP-190's shape exactly — *"a legal
  declaration whose UC-042 obligation is unsatisfiable"* — moved from `struct{}`
  to `math/big`, and it is worse than GAP-190 in one respect: GAP-190's refusal
  was reached only by a caller who had no other sample to give, while this one is
  reached by a **correct codec doing everything right**. `eventtest.RoundTrip`
  (`event/eventtest/proxies.go:16-21`) is `t.Fatal` on any error, so a store
  author whose domain records an exact amount as a `*big.Int` — an accounting
  ledger, which is event sourcing's own canonical domain — gets a red conformance
  run they cannot make green. It is a `universality.md` finding: whether an
  entirely correct declaration can be proved at all is decided by whether its type
  happens to spell one method with one fixed name and one fixed signature, and
  that mechanism is wrong on the standard library's own arbitrary-precision types.
  It is not `critical` because it fails closed at test time, writes no wrong fact
  and loses nothing.
- **Why this timing:** `immediate`. A `universality.md` finding is never deferred.
  It was introduced by round 7's own repair, which is the case a re-audit exists
  for. `docs/modules/{en,ru}/event.md`, `docs/modules/{en,ru}/eventtest.md`,
  `[[D-124]]` and plan clause 6d all state the current sentence, and S5's
  conformance report and phase 2's `eventpg` round-trip cases are written over
  it — so it is cheaper to settle before those depend on it than after.
- **The general mechanism that should replace the fixed method name:** compare at
  the **wire**, which needs no method and no name. `reusesItsBuffer`
  (`comparison.go:25`) already computes `before := read.selfEncode(own)`; where
  the value walk has nothing to compare, `bytes.Equal(before, carried.written)`
  is a real answer for **every** type — a codec that dropped a `big.Int` re-encodes
  to different bytes and is caught, and one that kept it re-encodes to the same
  bytes and passes. (It is weaker than the value walk for a codec that drops the
  same thing on both sides, so it belongs *behind* the walk, not instead of it —
  which is exactly the position `incomparable` occupies today.) A second option,
  if the wire comparison is rejected: let the caller supply the comparison for
  the one revision it cannot answer for, which stays constructible from outside
  `event` and keeps the kernel free of a rule about a payload's shape — the thing
  `[[D-124]]`'s own "What it forbids" list forbids.
- **Close criteria:**
  - [ ] a **faithful** codec over a payload carrying a `*big.Int` round-trips
        without error, through the shipped `JSON` codec and through a codec of the
        caller's own
  - [ ] a codec that **drops** that `*big.Int` and keeps the field beside it is
        still refused, and is refused as a difference (`ErrPayload`) rather than
        as an unusable sample — this is the control that stops the repair being
        made by widening the pass
  - [ ] `roundtrip_test.go` carries both rows above beside the existing
        `hushed` / `heldTight` rows, so whatever stays `ErrSample` is a shape a
        consumer can actually repair
  - [ ] `unanswered`'s message names only repairs that exist for the type it
        names, or the `incomparable` arm no longer fires for a type the consumer
        does not own
  - [ ] `docs/modules/{en,ru}/event.md` stops presenting "declare `Equal` on the
        value receiver, or record what the fact carries in an exported field" as
        the remedy for `big.Int`, and `[[D-124]]` stops listing `netip.Addr` and
        `big.Int` as one class — the `==` arm answers the first and nothing
        answers the second
  - [ ] a mutation that reverts whichever mechanism is chosen turns the
        `*big.Int` row red
- **Status:** closed — round 9, by asking the wire where the value walk has nothing to compare

---

### GAP-196 [medium][immediate] `same`'s default arm answers agreement for a kind it cannot compare, which is the fourth unanswered route and the one `[[D-124]]`'s forbid list already names

- **Where:** `event/comparison.go:165-167`
  (`default: return !first.Comparable() || first.Equal(second)`)
- **What:** the arm reads "if the value is not comparable, say the two are the
  same". Every kind reaching `default` is comparable except one — `Func` — so the
  `!first.Comparable()` short-circuit is not a guard against a `reflect` panic,
  it is an answer in the caller's favour for a position that compared nothing.
  Measured:

  ```
  type fn struct{ Do func() int; Note string }
  codec decodes Do as a different closure, Note unchanged
  RoundTrip(fn{Do: func() int { return 1 }, Note: "n"})  ->  err=<nil>
  ```

  A `chan` field takes the same arm and is refused correctly (`ErrPayload`),
  because `Chan` *is* comparable — which is what shows the `Func` answer is an
  accident of the guard rather than a decision. The same value is reached through
  `sameElements` and `sameEntries` as well, so `[]func()`, `[2]func()` and
  `map[string]func()` all agree with anything.

  `[[D-124]]`'s "What it forbids" says in terms: *"Do not answer an exhausted
  value walk in the caller's favour, and do not answer a walk that had nothing to
  compare either… There are three of those routes now."* This is a fourth,
  answered the other way, and `comparison.go:47-50` states the same policy in the
  file itself.
- **Why this severity:** `medium`, and I am saying why rather than inflating it.
  It needs a codec of the consumer's own — `chargeJSON` refuses `Func` at
  declaration (`encodable.go:80-81`), so the shipped codec never reaches it — and
  a function is not data anyone records, so nothing is lost in practice. It is
  not `low` because it is the same class of wrong answer as GAP-191 through a
  fourth arm, in the one helper whose entire job is to answer honestly, and
  because a binding decision doc forbids exactly this.
- **Why this timing:** `immediate` on GAP-193's own accepted timing argument —
  it is the same function and the same repair as GAP-195, and doing them
  separately means touching `same` twice.
- **Close criteria:**
  - [ ] a kind the walk cannot compare is recorded on the walk (`incomparable`,
        or whatever GAP-195 settles on) rather than answered as agreement
  - [ ] `roundtrip_test.go` carries the `func`-field row above asserting the
        chosen answer, with the `chan` row as its control so the repair cannot be
        made by refusing every non-struct leaf
  - [ ] a mutation restoring `!first.Comparable() || …` turns that row red
- **Status:** closed — round 9, by recording a `func` rather than agreeing with it

---

### GAP-197 [low][deferred] At the one position `Equal` is still asked, a lenient application `Equal` still decides the verdict — GAP-188(2)'s defect, narrowed rather than removed

- **Where:** `event/comparison.go:255-257` (`sameOpaque` asks `equalByMethod`
  first), `.agents/artifacts/plans/EVENTSOURCE_P1_PLAN.md` clause 6a (*"removes
  the case where an application's own question overrides the framework's"*)
- **What:** measured, for a struct with no exported field the application's own
  `Equal` is still the whole verdict:

  ```
  type quiet struct{ title, body string }
  func (this quiet) Equal(other quiet) bool { return this.title == other.title }
  codec drops body
  RoundTrip(quiet{title: "t", body: "the body the codec drops"})
     -> err=<nil>   carried=[{title:t body:}]

  and the same through an interface-typed field:
  RoundTrip(boxed{Meta: quiet{title: "t", body: "dropped"}})  -> err=<nil>
  ```

  This is GAP-188(2)'s exact transcript with an all-unexported struct in place of
  `Doc`. `[[D-124]]` decided it deliberately — *"a type that declares one says
  there what its equality is"* — and `docs/modules/en/event.md:257-260` states it
  accurately and with the right hedge (*"cannot switch this check off **for a
  struct the field walk could answer**"*). So the code and the docs agree and this
  is a recorded trade, not drift. It is recorded here because a reader of plan
  clause 6a's unhedged sentence would not expect it, and because the wire-level
  comparison proposed in GAP-195 would close it in the same change: `Equal` would
  become one of two answers rather than the only one.
- **Why this severity:** `low`. It needs a consumer to have written a lenient
  `Equal` on an all-unexported struct, and `[[D-124]]` accepted it with an
  argument. Nothing is wrong on any input the decision did not foresee.
- **Why this timing:** `deferred` — no external contract moves, nothing in S5 or
  S6 bends around it, and the honest close may well be `wontfix` citing
  `[[D-124]]`. It must not be dropped: if GAP-195 is closed by the wire
  comparison, this closes with it for free, and if it is closed some other way
  this stays open.
- **Close criteria:**
  - [ ] either a second answer runs beside a declared `Equal` at that position so
        a lenient one cannot pass a codec that dropped a field, with `time.Time`
        as the control that must stay green
  - [ ] or `wontfix` with the argument written into `[[D-124]]`, and plan clause
        6a's *"removes the case where an application's own question overrides the
        framework's"* gains the module page's hedge
- **Status:** rejected — round 9, `wontfix`: the repair accuses a codec that normalises a time.Time to UTC

---

### What is genuinely clean, and worth saying so

- **The cluster this round was sent to audit — GAP-188, GAP-189, GAP-190 — is
  fully closed by the code**, not by a disposition note. I constructed each
  finding's own input and ran it, and I reverted each repair in place and watched
  a named test go red: nine mutations, nine kills, every file md5-checked back.
- **GAP-191, GAP-192, GAP-193 and GAP-194 are closed too**, on the same evidence.
  GAP-195 is not a re-opening of GAP-191 — the repair works for the case
  GAP-191 measured (`netip.Addr`, `hushed`, `heldTight`) and the new finding is
  the class the `==` arm cannot reach, which GAP-191's own text did not separate
  out.
- **No exported symbol moved.** `docs/api/surface.md` regenerates byte-identical,
  and every symbol round 5 and round 7 added is unexported. Derived here.
- **`event/eventpg`'s zero-diff obligation is intact**, derived from outside the
  repository's own packages by a throwaway module that implements `event.Store`
  in full.
- **The suite does not pass vacuously.** Every subtest in
  `TestACodecThatDecodesIntoAReusedBufferIsCaught` carries its own control, and
  the controls are the right ones: `time.Time` sits beside the lenient-`Equal`
  case so neither half of the narrowing can be made by breaking the other,
  `marksOf(8)` beside `marksOf(valueWalkNodes+8)`, the faithful `located` codec
  beside the dropping one, and the same-dynamic-type interface rows beside the
  substituted ones.
- **House style holds.** `event/comparison.go` carries seven comment blocks, each
  on either a walk of 30-plus lines or an invariant the code cannot make visible
  (why `reflect.DeepEqual` is the wrong comparison, why 65 536 and not 1 024, why
  size zero means one value, why the three answers are in that order). None
  restates code, none narrates a test, and none exists because a symbol is
  exported — the file exports nothing. Receiver name is `this` throughout. Every
  refusal in the cluster is matched with `errors.Is` against an exported sentinel
  and every message states what broke in words.

---

## Round 9 — remediation of the round-8 cluster — 2026-09-08

GAP-195, GAP-196 and GAP-197 closed together. They are one position reached three
ways — what the value walk does when it has no answer — and round 6's own timing
argument applies again: doing them separately means touching `sameOpaque` three
times.

### Reproduced first, in this worktree, before anything was changed

An in-package probe (`event/zzprobe_test.go`, deleted afterwards) drove each
finding's own input through `Fact.RoundTrip`. What it printed against the code as
found:

```
reflect: big.Int Comparable= false hasEqual= false
195 JSON[pCredited]().CanEncode() = <nil>
195 faithful shipped codec over *big.Int  -> event: this sample cannot prove what a round trip
    claims for it: ... carries a big.Int: every field of it is unexported, it declares no
    Equal(big.Int) bool on the value receiver and == cannot compare it ...
195 the obvious workaround, Equal on the payload -> the same ErrSample naming big.Int
195 control, a codec that drops the *big.Int -> the same ErrSample naming big.Int
196 a func field read back as another closure -> err=<nil> carried=1
196 control, a chan field read back as another chan -> ErrPayload does not read back the
    event.pPiped it was given
197 a codec that normalises to UTC: At.Equal=true  wire same=false
    ("2026-09-08T04:18:10.036921382+05:00" vs "2026-09-07T23:18:10.036921382Z")
```

Two things that transcript settles beyond the findings' own text. The `big.Int`
refusal is **indistinguishable from a real defect** — the faithful codec and the
one that drops the amount answer with the same sentence, so the message told a
consumer to perform a repair that does not exist *and* hid the case where one was
needed. And the 197 row is the measurement that decides 197's disposition: the
hypothetical repair — a second answer beside a declared `Equal` — accuses a codec
that normalises an instant to UTC of losing what it kept.

After the change, the same inputs:

```
195 faithful shipped codec over *big.Int  -> <nil>
195 the obvious workaround, Equal on the payload -> <nil>
195 control, a codec that drops the *big.Int -> ErrPayload  does not read back the
    event.pCredited it was given: nothing can compare the big.Int it carries, so what came
    back was encoded again and the bytes are not the ones the sample encoded to
196 a func field read back as another closure -> ErrSample  carries a func() int, which no
    wire format records and no two values of which can be compared ... record what
    identifies the behaviour — a name, a code — and choose the function from that when you fold
196 control, a chan field read back as another chan -> ErrPayload (unchanged)
```

### What changed

`event/comparison.go`. `valueWalk.incomparable` is gone and two fields stand in
its place, because the two things it held are not one thing. **`opaque`** — a
struct with no exported field, no `Equal(T) bool` and no `==` — is carried out of
the walk to `Fact.readBack`, which answers it at the **wire**: `readsBackOnTheWire`
encodes what the codec returned with the revision's own codec and compares those
bytes with the ones the sample encoded to. That question needs no method and no
name, so it answers for `big.Int`, `big.Rat`, `big.Float` and every other type a
consumer cannot declare a method on. It is the weaker of the two answers — a codec
that drops the same thing on the way out and on the way in re-encodes to the bytes
it was given — so it runs **behind** the walk and only where the walk had nothing,
which is exactly the position `incomparable` occupied. **`unrecordable`** — a
`func`, the only kind `same`'s last arm reaches that Go declines to compare — is
`ErrSample` through `unanswered`, naming a repair that does exist: record what
identifies the behaviour and choose the function from it when you fold. `same`'s
`default` arm no longer reads `!first.Comparable() || first.Equal(second)`, which
answered agreement for the one kind it could not compare.

`event/fact.go`. `readBack` gained the three statements that ask the wire, and
`RoundTrip`'s comment says what the position does now. The helper is **inlined
rather than extracted**: a call that returns an `error` and takes a `string` is
what `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor` reads as a message
being worded, and passing `roundTripping` — which carries the payload bytes — to
one is what it forbids. That check found it; the first spelling here was a helper.

### Gate, run here

`gofmt -l .` silent · `go build ./...` exit 0 · `go vet ./event/...` exit 0 ·
`go test -race -count=2 ./event/...` green twice (`event` 6.17 s, `eventmemory`
1.09 s, `eventtest` 4.08 s) · `make unit` exit 0 · `make check` green
(`check-deps`, `check-tiers`, `check-utils`, `check-triplets`, `check-todo`,
`check-replaces`, `check-tidy`, `check-otel-schema`, `check-workspace` all `ok`) ·
`make api` regenerates `docs/api/surface.md` **byte-identical**.

### Mutation campaign — eleven mutations, each applied to the shipped code, the
suite run, the mutation reverted and both files md5-checked back
(`comparison.go` `de3200a11d9601804a83cfd9b2e86f1b`, `fact.go`
`c9294425c45a0ffbbf9c14cdddb6c27e`)

| Mutation | Result |
|---|---|
| M1 `same`'s default arm asks `!first.Comparable() \|\| ...` again (the round-7 spelling) | **killed** — *a leaf no wire format records is refused rather than reported as a pass*, and the diagnosis row |
| M2 `readBack` never asks the wire and passes what the walk could not compare | **killed** — five subtests, including *a payload the caller does not own the type of is provable all the same* |
| M3 `readBack` refuses `opaque` instead of asking the wire (round 7's spelling) | **killed** — the `*big.Int` rows and the `hushed`/`heldTight` rows |
| M4 the wire always agrees | **killed** — the same three subtests |
| M5 `unanswered` drops the arm for what no wire format records | **killed** — the `func` subtest and the diagnosis row |
| M6 `sameOpaque` drops the `==` arm | **killed** — *what an encoding drops by design is not what it lost*, through the codec that loses the same digit both ways, which the wire cannot see |
| M7 `sameOpaque` drops the `Equal` arm | **killed** — four subtests |
| M8 `sameOpaque` drops the zero-size arm | **killed** — *a fact whose whole content is that it happened round trips*, through the marker-beside-data row |
| M9 the wire compares against the zero value's encoding instead of the sample's | **killed** — the `*big.Int` rows and the `hushed`/`heldTight` rows |
| M10 `same`'s default arm sends a `func` to the wire instead of refusing it | **killed** — the `func` subtest and the diagnosis row |
| M11 the wire is asked beside a declared `Equal` as well as behind it (the rejected GAP-197 repair) | **killed** — *what an encoding drops by design is not what it lost*, through the UTC-normalising codec |

M8 and M11 are new tension this round put there rather than found. M8 **survived**
on the first campaign: with the wire answering for a type nothing can compare, the
zero-size arm became indistinguishable for a marker fact standing alone. It is not
indistinguishable for a **marker beside data** — recording `opaque` stops the walk,
so the field next to the marker would be left to a wire its own codec agrees with —
and `witnessedNote` + `trimmingCodec` (three bytes of six, read back exactly as
written) is the row that says so, with a body it records whole as its control. M11
is the mutation that would be applied by anyone closing GAP-197 the other way, and
the UTC codec is what stops them.

### Dispositions

| Finding | Disposition |
|---|---|
| **GAP-195** | **closed by asking the wire where the walk has nothing**, which is the general mechanism the finding itself proposed. Criterion 1: a `*big.Int` payload round-trips through the shipped `JSON` codec **and** through `tallyingCodec`, a codec of the caller's own — both rows of *a payload the caller does not own the type of is provable all the same*, and both assert the carried value as well as the `nil`. Criterion 2: `untalliedCodec` reads the amount back as nothing and keeps the note, and is refused as **`ErrPayload`** naming `big.Int` — a difference, not an unusable sample. Criterion 3: both rows sit in `roundtrip_test.go`, and `hushed`/`heldTight` moved to `ErrPayload` in the subtest beside it, which now carries `murmuringCodec` as the control that the answer is not "refuse every all-unexported struct". Criterion 4: the `incomparable` arm no longer fires at all — `unanswered` has no branch for it — so no message names a repair that does not exist. Criterion 5: `docs/modules/{en,ru}/event.md` and `{en,ru}/eventtest.md` say the wire answers there; `[[D-124]]` separates the two classes in terms (`netip.Addr` comparable, `big.Int` not) and its forbid list gained two lines; plan clause **6f** carries the contract and 6d hands off to it. Criterion 6: mutations M2, M3, M4 and M9. |
| **GAP-196** | **closed by recording the kind rather than agreeing with it.** Criterion 1: `same`'s default arm records `unrecordable` and `unanswered` renders it as `ErrSample` naming the type and a repair that exists. Criterion 2: *a leaf no wire format records is refused rather than reported as a pass* carries the `func` row, the `chan` row as the control that keeps `==` answering for a comparable leaf, and a third row where the repair the message names — record what identifies the behaviour — round-trips. Criterion 3: mutations M1, M5 and M10. The finding's own fixture (a codec that writes only the note) is refused now for the reason the walk gives, before the wire is consulted at all, so the answer does not depend on what the codec chose to write. |
| **GAP-197** | **rejected — `wontfix`, with the argument measured rather than cited**, which is the second of the two close criteria the finding offered. The proposed repair is a second answer beside a declared `Equal`, and the transcript above is what it costs: `utcCodec` records an instant and hands it back in UTC, `time.Time.Equal` says nothing was lost and the re-encoding says otherwise, so the repair accuses a faithful codec. That is the false accusation `Equal` is asked at that position to prevent, and it is the reason `[[D-124]]` gives for asking the method first. The wire therefore sits behind the walk and never beside it, which is also what keeps a lenient `Equal` deciding at that one position — a consumer who writes one on an all-unexported struct has said there what its equality is. Written into `[[D-124]]` (*"Do not ask the wire beside an answer the walk already has"*, in the forbid list) and into plan clause 6a, which gained the module page's hedge — *"for a struct the field walk could answer"* — and a pointer to 6f. The rejection is held by a test rather than a sentence: mutation M11 is exactly this repair and *what an encoding drops by design is not what it lost* goes red for it. |

### What this round changed in the tests, and why

- `hushed` and `heldTight` moved from `ErrSample` to `ErrPayload`, and the subtest
  they live in is now *a struct nothing can compare is answered at the wire rather
  than reported as a pass*. Both are codecs that genuinely lose what they were
  handed, and naming that as a difference is a stronger close than refusing the
  sample. `murmuringCodec` — the same wire format read back whole — joins them as
  the control.
- The diagnosis table grew a row and is now *ten of them share two sentinels*. The
  `hushed` row's expected sentence changed with the message; the `func` row is new.
- Three fixtures added for the `*big.Int` case (`ledgered`, `tallyingCodec`,
  `untalliedCodec`), four for the `func` case (`routed`, `routingCodec`, `named`,
  `piped`/`pipingCodec`), and three for the mutation tension described above
  (`coarseCodec`, `utcCodec`, `witnessedNote`/`trimmingCodec`).

### Zero-diff obligation

Intact. Every symbol this round added is unexported (`readsBackOnTheWire`,
`valueWalk.opaque`, `valueWalk.unrecordable`), no exported signature moved, and
`make api` regenerates `docs/api/surface.md` byte-identical — diffed here.

---

## Round 10 — econv-implementation-reviewer (clean context, re-audit of the round-9 cluster) — 2026-09-08

Scope: GAP-188, GAP-189, GAP-190 and every repair layered on them through rounds 5,
7 and 9 (`event/comparison.go`, `event/fact.go`, `event/codec.go`,
`event/roundtrip_test.go`). Nothing was inherited: each closed finding was
re-derived by constructing its own input, and each repair was reverted in place,
the suite run, and the file restored to its md5.

Every transcript below was produced by an **out-of-module** probe
(`/tmp/zzaudit10`, its own `go.mod` with a `replace` onto this checkout) so that
nothing unexported in `event/` could be relied on, and by an out-of-module store
(`/tmp/zzmk10`). The tree is byte-identical to how it was found:
`git status --porcelain | wc -l` = 251 before and after; `event/comparison.go`
md5 `de3200a11d9601804a83cfd9b2e86f1b`, `event/fact.go`
`c9294425c45a0ffbbf9c14cdddb6c27e`, `event/codec.go`
`52e480c9cbd64bfa526601f3772ce9b6`, `event/roundtrip_test.go`
`5698171f08468bb1f6543fad7c2eb85f`.

### Gate, run here

`gofmt -l .` silent (exit 0) · `go vet ./event/...` exit 0 ·
`go test -race -count=2 ./event/...` green, no flake (`event` 5.953 s,
`eventmemory` 1.085 s, `eventtest` 4.032 s).

### Metrics — counted

| Metric | Command | Value |
|---|---|---|
| `event/comparison.go` | `wc -l` | 313 |
| `event/fact.go` | `wc -l` | 221 |
| `event/codec.go` | `wc -l` | 106 |
| longest function in the two | `awk` over `^func`…`^}` | `shares` 34, `roundTrip` 34, `same` 33 |
| exported declarations in `comparison.go` | `grep -cE "^(func\|type\|const\|var) [A-Z]"` | **0** |
| non-test functions in `event/` over 4 parameters | paren-depth scan of every `^func` | **0** |
| kernel imports of / branches on a concrete store | `grep -rn "eventmemory\|eventpg\|eventtest" event/*.go \| grep -v _test.go \| wc -l` | **0** |

### Microkernel — derived here, not inherited

**`event/eventpg`'s zero-diff obligation is intact.** Derived by building
`/tmp/zzmk10`, a module outside this repository that implements the whole
`event.Store` interface and constructs every value the contract makes a store
produce: `Capabilities{Transactions: event.Supported, …}`, `Limits{…}`,
`NewBacking(identity)`, `NewAuthority(backing, tx)`,
`Envelope{Stream, Version, Position, Type, Revision, Payload, RecordedAt}`,
`Record{…}`, `AppendRequest{…}`, `Cursor`, `Failure(NotWritten, cause)` and
`Failure(Conflict, cause)`. It compiles and `var _ event.Store = store{}` holds.
Nothing a store must build is behind an unexported field or a package-internal
constructor. The two findings below are both in unexported code
(`valueWalk.spend`, `Fact.readBack`) and neither repair moves an exported symbol.

### The cluster, verified against the code

Each verified by constructing the finding's own input **and** by reverting the
repair in place, running the suite, and restoring the file to its md5.

| Finding | Verdict |
|---|---|
| **GAP-188 (1)** nil-receiver `Equal` panic | **closed.** `equalByMethod` (`comparison.go:298`) has one caller, `sameOpaque:288`, reached only from `sameFields:236` where no exported field was compared; `same`'s `Pointer`/`Interface` arm (`:180-184`) nil-guards first. **Mutation:** ask `Equal` before the `switch` in `same` → *a pointer field is not asked its own type's Equal through a nil* red, with the panic text in the message. |
| **GAP-188 (2)** no panic policy / no row in the table | **closed.** `equalByMethod:307-311` recovers into `walk.panicked`; `unanswered:91` renders `ErrSample`; `codec.go:31-34` carries the row and matches the code. **Mutation:** drop the `recover` → *an Equal that panics refuses the sample rather than unwinding* **panics** (`index out of range [0] with length 0`). |
| **GAP-188 (3)** an application `Equal` disables the fidelity check | **closed for the shape the finding measured.** Same mutation turns *an application's own Equal does not decide whether the codec kept what it was handed* red. |
| **GAP-189** both walks on the type-graph budget | **closed.** `valueWalkNodes = 1 << 16` at `comparison.go:60` with its justification at `:56-59`. **Mutation:** `valueWalkNodes = codecGraphNodes` → *a sample larger than the walk is refused rather than reported as a pass* red on the `n=2000 @ 1500` row. |
| **GAP-190** a marker fact can never satisfy §UC-042 | **closed.** `singleValued` at `comparison.go:13`, the guard at `fact.go:160`, the non-aliasing half skipped at `:167`. **Mutation:** drop `singleValued` from the guard → *a fact whose whole content is that it happened round trips* red. |
| **GAP-195** a `*big.Int` payload permanently unprovable | **closed at the position the finding named.** **Mutation:** `readBack` never asks the wire → three subtests red, including *a payload the caller does not own the type of is provable all the same*. New consequences recorded as GAP-198 and GAP-199 below. |
| **GAP-196** `default:` answering agreement for a `func` | **closed.** **Mutation:** restore `!first.Comparable() \|\| first.Equal(second)` → *a leaf no wire format records is refused rather than reported as a pass* red, plus the diagnosis row. |
| **GAP-197** a lenient `Equal` at the narrowed position | **`wontfix` stands, and the argument is now measured rather than cited.** I re-ran the rejection's own transcript: a decode-side UTC-normalising codec over `stamp` alone answers `<nil>` (`3b` below), which is what the rejection protects. |

Six mutations applied and reverted, all four files md5-checked back. **Every
repair in the cluster is held by a named test.** No finding closed above was
closed by a disposition note.

### What the round-9 repair introduced

The wire answer closed GAP-195 at the node it was measured on and left two
routes open **around** that node, because `valueWalk.opaque` is a *per-node* fact
used as a *whole-walk* verdict. Both are measured below; both are direct
violations of `[[D-124]]`'s own forbid list, and one of them makes
`Fact.RoundTrip` report a pass for a codec that permanently drops a field.

---

### GAP-198 [high][immediate] Recording `opaque` stops the whole value walk, so every field after a `big.Int` is left to a wire its own codec agrees with — a codec that drops a field passes or is refused depending on the order the fields are declared in

- **Where:** `event/comparison.go:77` (`spend` returns false once `this.opaque != ""`),
  `event/comparison.go:294` (`sameOpaque` sets it and returns `true`),
  `event/comparison.go:164-166` (`same` answers **true** for every node reached
  after that), `event/fact.go:198-206` (`readBack` then asks the wire for the
  **whole** value), `event/fact.go:84-97` (the `RoundTrip` contract comment:
  *"What the value comparison cannot answer it does not pass over"*)
- **What:** `sameOpaque` records the type name of a struct nothing can compare
  and returns agreement; `spend` treats that record as a stop condition, so the
  walk compares nothing after it — including fields it has a perfectly good
  answer for. `readBack` then falls to `readsBackOnTheWire`, which is the
  **weaker** answer by the file's own comment (`comparison.go:45-47`) and by
  `[[D-124]]`: *"a codec that drops the same thing on the way out and on the way
  in re-encodes to the bytes it was given"*. So the strong answer is discarded
  for every field past the opaque one and the weak one stands in for it.

  Measured, out of module, on the repository's own `ledgered` shape
  (`{Amount *big.Int; Note string}`) with a codec that writes the amount and
  never writes the note — the mirror image of the shipped `untalliedCodec`
  fixture, which is tested and caught:

  ```
  1a  type amountFirst struct{ Amount *big.Int; Note string }
      codec writes only Amount, reads back only Amount
      RoundTrip(amountFirst{big.NewInt(1234), "the note the codec drops"})
        -> err=<nil>   carried=[{1234 }]          <-- the note is gone, and it passed

  1b  type noteFirst struct{ Note string; Amount *big.Int }    (the SAME codec semantics,
      RoundTrip(noteFirst{"the note the codec drops", big.NewInt(1234)})   fields swapped)
        -> ErrPayload  "does not read back the main.noteFirst it was given"
  ```

  The two payloads carry the same data through the same codec and disagree only
  in the order the fields are declared in. Nothing about a fact depends on that
  order, so this is a proof that fires or does not fire for a reason outside the
  domain.

  It is not confined to a struct's own fields — it stops the walk wherever the
  opaque node is reached, so a batch loses every row:

  ```
  4  type row struct{ Amount *big.Int; Note string }; type batched struct{ Rows []row }
     codec writes every row's amount and no row's note
     RoundTrip(batched{Rows: []row{{1,"first note"},{2,"second note"}}})
       -> err=<nil>   carried=[{[{1 } {2 }]}]      <-- every note in the batch dropped
  ```

  **Causation confirmed in place:** deleting `|| this.opaque != ""` from
  `spend` (`comparison.go:77`) and re-running the same probe turns `1a` into the
  same `ErrPayload` as `1b`. The file was restored to md5
  `de3200a11d9601804a83cfd9b2e86f1b`.

  `[[D-124]]`'s forbid list already rules this out in terms: *"Do not answer an
  exhausted value walk in the caller's favour, and do not answer a walk that had
  nothing to compare either. **A walk that stopped early compared nothing past
  where it stopped, and reporting that as a pass is a check that cannot fail**"*.
  The walk stops early here, and what is past the stop is reported as a pass.
  Plan clause 6f says the wire *"runs behind the walk and only where the walk had
  nothing"*; the walk had an answer for `Note` and was prevented from giving it.
  `docs/modules/en/event.md:259-262` tells a consumer the same untrue thing.

  The neighbouring stop conditions do not have this consequence and that is what
  isolates it: `beyond`, `panicked` and `unrecordable` also stop the walk, and
  all three end in `unanswered` — a **refusal**. `opaque` is the only one that
  stops the walk and then passes. The zero-size arm (`sameOpaque:285`)
  deliberately does **not** record anything, precisely so the field beside a
  marker stays compared (`[[D-124]]` line 171, `witnessedNote`/`trimmingCodec`
  in `roundtrip_test.go:376-396`) — the same reasoning was not applied one arm
  down.
- **Why this severity:** `high`. `Fact.RoundTrip` is the only mechanism the
  framework has for §UC-042, and here it answers `nil` for a codec that
  permanently deletes a field from every fact ever written through it — the exact
  defect the comparison exists to catch (GAP-178 criterion 3, GAP-191, GAP-195
  criterion 2), reached by declaring an ordinary accounting payload with the
  amount first. `[[D-124]]` names an accounting ledger as *"event sourcing's own
  canonical domain"* and a `*big.Int` amount is what put the opaque arm there in
  the first place, so the shape is not exotic — it is the shape round 9 was
  written for. It is not `critical` because it takes a codec of the consumer's
  own: the shipped `JSON` codec writes every exported field, and a field it
  skips by design (`json:"-"`, unexported) is one no load was going to read. It
  is not `medium` because the answer is a **pass**, `architecture.md`'s "a check
  that cannot fail", and because `eventtest.RoundTrip` is the conformance
  suite's own proxy, so a green conformance run means nothing here.
- **Why this timing:** `immediate`. It is a `universality.md` finding — whether
  the only check that says a codec kept what it was handed runs at all is decided
  by field declaration order, which is behaviour undefined for a neighbouring
  input — and those are never deferred. It was introduced by round 9's own
  repair, which is what a re-audit exists for. `[[D-124]]`, plan clause 6f and
  `docs/modules/{en,ru}/event.md` all state the opposite of what the code does,
  and S5's conformance report and phase 2's `eventpg` round-trip cases are
  written over those sentences.
- **The general mechanism that should replace the stop:** a node the walk could
  not answer is a fact **about that node**, not about the walk. Record it and
  keep walking — every other node still gets the strong answer, and the weak
  wire answer is consulted only if the strong one found nothing wrong anywhere.
  `spend`'s three refusal flags may keep stopping the walk, because each ends in
  a refusal; `opaque` must not, because it ends in a pass.
- **Close criteria:**
  - [ ] a node recorded as `opaque` no longer stops the walk: every other node the
        walk can reach is still compared, and a difference found after it is
        `ErrPayload`
  - [ ] `roundtrip_test.go` carries the `1a` row above — the mirror of
        `untalliedCodec`: a codec that keeps the `*big.Int` and drops the `Note`
        beside it — asserting `ErrPayload`, with the field-swapped spelling
        (`1b`) beside it as the control that says the answer does not depend on
        declaration order, and the faithful `tallyingCodec` row as the control
        that the repair was not made by refusing every payload holding a
        `big.Int`
  - [ ] the batched row (`4` above) is carried too, so the fix is to the walk
        rather than to one struct shape
  - [ ] a mutation restoring `|| this.opaque != ""` in `spend` turns those rows
        red
  - [ ] `event/fact.go`'s `RoundTrip` comment, plan clause 6f, `[[D-124]]` and
        `docs/modules/{en,ru}/event.md` stop claiming the wire runs *"only where
        the walk had nothing"* unless that is what the code does
- **Status:** open

---

### GAP-199 [high][immediate] The wire is a whole-value question asked whenever any node was opaque, so it is asked beside every answer the walk already gave — and a faithful codec that hands a `time.Time` back in UTC is accused of losing data as soon as the payload also carries a `big.Int`

- **Where:** `event/fact.go:198-208` (`readBack` asks
  `readsBackOnTheWire(carried, own)` for the **whole** payload when
  `walk.opaque != ""`), `event/comparison.go:48-54` (`readsBackOnTheWire`
  re-encodes the whole value), `event/comparison.go:274-283` (`sameOpaque`'s
  comment: *"It is not asked a second question either … asking the wire beside
  the method would call it a difference"*)
- **What:** the walk's record is per node; the wire question is per payload. So
  the moment one node is opaque, the re-encoding is compared for **every** node,
  including the ones the walk answered — which is the one thing `[[D-124]]`'s
  forbid list rules out by name: *"Do not ask the wire beside an answer the walk
  already has — not beside a declared `Equal`, not beside `==`, not beside the
  field walk. Re-encoding is a question about bytes, and a codec that normalises
  what it records answers it differently while losing nothing; it is a second
  answer only where there is no first one."*

  Measured, out of module, with the shape the suite's own `utcCodec` fixture
  pins (`roundtrip_test.go:659-678`, `:859-864`) plus a `*big.Int` field beside
  it — a codec that records the instant as given and hands it back in UTC:

  ```
  3a  type held struct{ At time.Time; Amount *big.Int }
      codec: Encode = the instant as given; Decode = at.UTC()
      RoundTrip(held{At: 2026-09-08T04:18:10.036921382+05:00, Amount: big.NewInt(5)})
        -> ErrPayload  "does not read back the main.held it was given: nothing can
                        compare the big.Int it carries, so what came back was encoded
                        again and the bytes are not the ones the sample encoded to"

  3b  type heldAlone struct{ At time.Time }        (the control — the same codec,
      RoundTrip(heldAlone{At: <the same instant>})  without the big.Int)
        -> err=<nil>
  ```

  `time.Time.Equal` answers that nothing was lost; the walk asked it and got that
  answer; the wire then overrode it. The refusal even names `big.Int` — the one
  field the codec handled perfectly — so the diagnosis points at the wrong value
  and names a repair (`docs/modules/en/event.md:250-266`) that has nothing to do
  with the difference.

  This is the exact false accusation the round-9 disposition rejected GAP-197 to
  avoid, and mutation **M11** of that round (*"the wire is asked beside a declared
  `Equal` as well as behind it"*) is the mutation the shipped code already
  contains for any payload with an opaque node in it. `roundtrip_test.go:862-864`
  asserts in its own failure message that *"nothing may be asked beside it"*; the
  assertion holds only because the fixture has no second field nothing can
  compare.
- **Why this severity:** `high`, and it is GAP-195's own severity argument
  reappearing one field over. `eventtest.RoundTrip` (`event/eventtest/proxies.go:16-21`)
  is `t.Fatal` on any error, so a store author whose ledger event is
  `{At time.Time; Amount *big.Int; …}` — the payload `[[D-124]]` calls the
  canonical domain — and whose codec normalises instants on read (which is what
  every store that stores UTC does) gets a red conformance run for a correct
  declaration, with a message naming a field that is not the problem and a repair
  that does not apply. It is not `critical` because it fails closed at test time,
  writes no fact and loses nothing. It is not `medium` because a binding decision
  doc forbids exactly this, a shipped test's own failure text asserts the
  opposite, and the consumer cannot make it green without changing the payload.
- **Why this timing:** `immediate`. It is the same function, the same field and
  the same repair as GAP-198, so doing them separately means touching `readBack`
  twice — round 6's and round 8's accepted timing argument. It is a
  `universality.md` finding: a correct declaration is provable or not depending
  on whether some *other* field of the payload happens to be a type nothing can
  compare. And `[[D-124]]`, plan clause 6a/6f and both module pages state the
  rule the code breaks, so leaving it open leaves S5's conformance report and
  S6's module page written over a false sentence.
- **The general mechanism:** the wire cannot be made a per-node question — a
  codec encodes `V` and nothing smaller — so the whole-value answer must not be
  allowed to contradict a per-node answer the walk already gave. Either the
  walk records that some node was answered by a method or by `==` and a wire
  disagreement is then reported as *inconclusive* (`ErrSample`, naming both the
  opaque type and the answered one) rather than as a difference, or the wire is
  consulted only where it is the sole answer for the whole payload. Whichever is
  chosen, `readBack` must not turn "one node was opaque" into "compare all the
  bytes".
- **Close criteria:**
  - [ ] `roundtrip_test.go` carries the `3a` row above — the existing `utcCodec`
        shape with a `*big.Int` field beside the instant — asserting that a
        faithful codec is **not** accused, with `3b` (the same codec without the
        `big.Int`) as the control that the repair was not made by dropping the
        wire, and the `untalliedCodec` row as the control that a codec which
        genuinely drops the amount is still `ErrPayload`
  - [ ] a mutation restoring the unconditional whole-value wire comparison turns
        the `3a` row red
  - [ ] if the answer chosen is *inconclusive*, the refusal names the node that
        could not be compared **and** the node whose own answer it could not be
        reconciled with, so the message does not point at a field the codec
        handled correctly
  - [ ] `[[D-124]]`'s *"Do not ask the wire beside an answer the walk already
        has"*, plan clause 6a/6f and `docs/modules/{en,ru}/event.md`'s *"nothing
        is asked beside it"* either become true of the code or are corrected in
        the same change
- **Status:** open

---

### GAP-200 [low][deferred] Two top-level declarations in `event/fact.go` run together with no blank line between them

- **Where:** `event/fact.go:209-210` (`readBack`'s closing brace and
  `func applierOf` on the next line)
- **What:** every other pair of top-level declarations in the file is separated
  by a blank line; these two are not. `gofmt -l .` is silent, so nothing catches
  it.
- **Why this severity:** `low` — cosmetic, no behaviour.
- **Why this timing:** `deferred` — no contract moves and nothing bends around it.
- **Close criteria:**
  - [ ] a blank line separates the two declarations
- **Status:** open

---

### What is genuinely clean, and worth saying so

- **GAP-188, GAP-189, GAP-190, GAP-195, GAP-196 are closed by the code**, not by
  a disposition note. I constructed each finding's own input out of module and
  ran it, and reverted each repair in place and watched a named test go red: six
  mutations, six kills, all four files md5-checked back.
- **GAP-197's `wontfix` stands and its argument is reproducible**: a decode-side
  UTC-normalising codec over `stamp` alone answers `<nil>` here.
- **No exported symbol moved.** `event/comparison.go` exports nothing
  (`grep -cE "^(func|type|const|var) [A-Z]"` = 0) and neither of the two open
  findings needs an exported symbol to close.
- **`event/eventpg`'s zero-diff obligation is intact**, derived by building a
  module outside this repository that implements `event.Store` in full.
- **Kernel purity holds.** Zero imports of or branches on `eventmemory`,
  `eventtest` or `eventpg` in non-test kernel files; adding a second store costs
  zero diffs under `event/*.go`, which is what `/tmp/zzmk10` demonstrates.
- **The suite does not pass vacuously** where the cluster is concerned: every
  subtest I mutated has a control beside it, and the controls are the right ones
  (`marksOf(8)` beside `marksOf(valueWalkNodes+8)`, `murmuringCodec` beside
  `hushingCodec`, the faithful `tallyingCodec` rows beside `untalliedCodec`, the
  `chan` row beside the `func` row). What is missing is a control for the case
  **beside** an opaque node, which is what GAP-198 and GAP-199 are.
- **House style holds in the two changed files.** Receiver name `this`
  throughout; every refusal matched with `errors.Is` against an exported
  sentinel; every message states what broke in words. The comment blocks in
  `comparison.go` sit on walks of 30-plus lines or state an invariant the code
  cannot make visible; none restates code and none exists because a symbol is
  exported — the file exports nothing. Two of them are now **wrong** rather than
  superfluous (`comparison.go:45-47` and `:274-283`), and that is carried by
  GAP-198 and GAP-199 rather than as a separate finding.

---

## Remediation-pass dispositions — 2026-09-08

Cluster `aggregate-crossing-and-log-injection` of `EVENTSOURCE_P1_REMEDIATION.md` § 11. One finding
from this file.

| Gap | Severity | Disposition |
|---|---|---|
| GAP-182 | `[medium][deferred]` | **closed by the first of its two close criteria** — a `Change` carries the `Declaration` it was decided on and `decidedFor` compares it beside the stream, at both doors, with `ErrFamily` |

**Reproduced first, from `/tmp/vvprobe` through the exported API** — two `Define[ledger, string]`
values over one family, the first declaring `credited` (appends `len(Items)`), the second `other`
(appends `-1000`):

```
second.Fold("k", ledger{}, firstCredited.New("k", Batch{Items: []int{1,2,3}}))
before:  state={Applied:[3]}  err=<nil>        ← the first aggregate's fold ran on the second
after:   state={Applied:[]}   err=event: this family is already bound to another aggregate:
                                   "credited" was decided on another aggregate of this family
```

**What changed.** `Change[S]` gains an unexported `origin Declaration`, set by `Fact.New` from the
fact's own aggregate. `decidedFor(stream Stream, on Declaration)` admits only `stream == this.stream
&& origin == on`, and both callers pass their aggregate: `Aggregate.Fold` passes `this`, and
`Repo.Append` passes `this.aggregate` — the two doors that pair a change list with an instance. The
existing order is untouched, so a carried refusal still outranks the comparison and a zero change
still reads as *a change no fact ever decided*; the crossing arm splits into the stream half
(`ErrWrongStream`, unchanged) and the declaration half (`ErrFamily`, new).

**Why `ErrFamily` and not a new sentinel.** The finding's own timing note asks for it: `Bind` check
4 refuses two declarations of one family through one `*Binding` with `ErrFamily`, §INV-039 admits
the two-`Binding` escape deliberately, and these two doors close that escape where it can actually
be seen — at the value that knows both parties. One question, one answer, three doors. Adding a
sentinel would have been additive but would have made a caller branch twice on one wiring mistake.

**Why not the second criterion (`Fold` looks `apply` up in `this.facts[change.name]`).** It answers
the crossing by *running the receiving aggregate's fold over the minting aggregate's bytes*, which
is a second, silent answer to a question `Bind` already answers with a refusal — and where the two
declarations spell one wire name differently it degrades to `ErrUnknownType`, history class, for
what is a wiring mistake. Recorded so it is not re-proposed.

**The `eventpg` zero-diff obligation is untouched.** `origin` is unexported and set only by
`Fact.New`; a store constructs `Envelope`, `Record`, `Failure`, `Backing`, `Authority` and `Cursor`,
never a `Change`. Nothing on the store seam changed shape.

**Left behind, and both were watched fail with `case this.stream == stream && this.origin == on`
reverted to `case this.stream == stream`:**

| Test | Answer with the fix reverted |
|---|---|
| `TestFoldRefusesAnotherInstance/two aggregates over one family are told apart by the declaration` | `a change minted on another aggregate of this family folded with <nil>; one family names one aggregate, and the two are told apart at Bind by the same sentinel` |
| `TestAnAppendRefusesAChangeDecidedOnAnotherAggregateOfThisFamily` | `a change decided on another aggregate of this family appended with <nil>, and a fact recorded through it is unloadable by the declaration that wrote it` |

Both carry the control the case could otherwise pass vacuously against: each asserts **first** that
the two declarations render one and the same `Stream` — so if a later change ever made the families
or the keys differ, the control fails and says the stream comparison is what refuses, and the case
has stopped proving what it claims. The append case's second control is stronger still: the *same
token* and the *same change*, appended through the repository whose aggregate minted it, must
succeed.

**Docs updated in the same change:** `PLAN.md`'s `Fold` contract block (cause 2 now asks two
questions) and its `## Debt` entry; `[[FL-036]]`'s append-order step 2, its test table and the new
test name; `docs/modules/en/event.md` and `docs/modules/ru/event.md` gain *One family names one
aggregate* / *Одно семейство именует один агрегат*.

---

## Round 11 — econv-implementation-reviewer (clean context, re-audit of the remediation cluster) — 2026-09-08

Scope: **GAP-182** (a change folding through an aggregate that never declared it) and every file
the remediation touched for it — `event/change.go`, `event/aggregate.go`, `event/fact.go`,
`event/fold_test.go`, `event/repo_test.go`. Nothing was inherited from the disposition note: the
defect was re-derived by reverting the repair in place, running the suite, and restoring the file.

The tree was left byte-identical to how it was found. `md5sum` before the mutation and after the
restore: `event/change.go` `16fe2a4be211940732c77d53ec850198`, `event/aggregate.go`
`c9610ad1a3033239c267a221dc8daf27`, `event/identity.go` `f95198de6e880da5eeadbc158e7cacb6`.
The two throwaway probes were deleted; `git status --porcelain | grep -c "zzaudit\|eventpgprobe"`
= **0**.

### Gate, run here

`gofmt -l .` silent (exit 0, whole repository) · `go vet ./event/...` exit 0 ·
`go test -race -count=2 ./event/...` green, no flake — `event` 6.081 s, `eventmemory` 1.489 s,
`eventtest` 4.004 s.

### Metrics — counted, not eyeballed

| Metric | Command | Value |
|---|---|---|
| `event/change.go` | `wc -l` | 54 |
| `event/aggregate.go` | `wc -l` | 113 |
| `Change[S].decidedFor` | lines 42–54 | 13 |
| `Aggregate.Fold` | lines 71–94 | 24 |
| fields on `Change[S]` | count | **7** — at the threshold, not over it |
| fields on `Aggregate[S, ID]` | count | 5 |
| exported methods on `*Aggregate[S, ID]` | `Family`, `Key`, `Fold` | 3 |
| exported methods on `Change[S]` | `Stream`, `Err` | 2 |
| parameters, `decidedFor` / `Fold` | count | 2 / 3 |
| nesting depth, both | count | 2 |
| new exported surface from this repair | `git diff HEAD -- event/change.go event/aggregate.go \| grep "^+" \| grep -E "^\+(func\|type\|var\|const) [A-Z]"` | **0** — `origin` is an unexported field, `Declaration` already existed |
| callers of `decidedFor` | `rg -n "decidedFor" event/*.go \| grep -v _test` | **2** — `aggregate.go:78`, `repo.go:76` |

### Microkernel — derived here, not inherited

**`event/eventpg` can still be written with zero diffs under `event/`.** Derived by building
`eventpgprobe/`, a package outside `event` that implements the whole `event.Store` and `event.Log`
interface (`var _ event.Store = (*store)(nil)` compiled) and constructs every value the contract
makes a store produce from exported API alone: `Capabilities{Transactions: event.Supported, …}`,
`Limits{…}`, `event.NewBacking(identity)`, `event.NewAuthority(backing, tx)`,
`Envelope{Stream, Version, Position, Type, Revision, Payload, RecordedAt}`,
`event.Stream{Family, Key}`, `event.Compose(...)`, `event.Cursor("42")`, `event.Record{…}`,
reading `AppendRequest{Stream, Expected, Records}`, and `event.Failure(outcome, err)` over all
seven exported outcomes. `go build ./eventpgprobe/` and `go vet ./eventpgprobe/` both exit 0; the
package was then deleted. **A store never constructs a `Change`**, so the new `origin Declaration`
field changes nothing on the store seam. Verdict: **microkernel passes**.

### GAP-182 — closed, verified by mutation and not by the note

Both doors were checked against the code. `event/change.go:44` is
`case this.stream == stream && this.origin == on:`; `origin` is set once, at
`event/fact.go:59`, from `this.aggregate`; `event/aggregate.go:78` passes `this` and
`event/repo.go:76` passes `this.aggregate`. `Declaration` carries an unexported `declaration()`
method, so its dynamic type is always `*Aggregate[S, ID]` — a pointer, always comparable, so
`this.origin == on` cannot panic, and `TryDeclare` refuses a nil aggregate so a nil pointer is
never boxed into it.

Reverting `case this.stream == stream && this.origin == on:` to `case this.stream == stream:`
turned **both** doors red in one run:

| Test | Answer with the repair reverted |
|---|---|
| `TestFoldRefusesAnotherInstance/two aggregates over one family are told apart by the declaration` (`fold_test.go:291`) | *a change minted on another aggregate of this family folded with `<nil>`* |
| `TestAnAppendRefusesAChangeDecidedOnAnotherAggregateOfThisFamily` (`repo_test.go:157`) | *a change decided on another aggregate of this family appended with `<nil>`* |

The controls are real and each one is the right control. `fold_test.go:279` asserts the two
declarations render **one and the same** `Stream` before the crossing is attempted, so the case
cannot silently degrade into the stream comparison it is meant to sit beside; `fold_test.go:286`
folds the same change through the aggregate that minted it and requires success, so the refusal is
not a `Fold` that refuses everything; and `second`'s fold **subtracts** where `first`'s adds, so
`state.Balance != 7` distinguishes *no fold ran* from *the wrong fold ran*. `repo_test.go:147`
does the same stream-equality control, and `repo_test.go:167` appends the *same token and the same
change* through the twin's own repository and requires it to reach the store exactly once.
`errors.Is(err, ErrFamily) && !errors.Is(err, ErrWrongStream)` is asserted at both doors, so the
two cannot answer one question with two sentinels.

The branch order was re-derived rather than read. A change whose `locate` failed carries `err` and
a **zero** stream, so the first case misses and the carried `ErrKey` still outranks the comparison;
a change whose encode failed carries `err` and a **valid** stream and origin, so the first case
matches and the refusal is caught by `Fold`'s second loop — which is what
`TestFoldRefusesAnotherInstance/a payload over the kernel ceiling …` pins. The zero `Change` still
reads as *a change no fact ever decided*. Nothing was widened: the new `this.stream != stream` arm
carries the message the old fall-through carried, and the fall-through is now reachable only when
the streams match, in which case the families necessarily match and `ErrFamily`'s wording is true.

Docs verified present, not assumed: `docs/modules/en/event.md:249` / `docs/modules/ru/event.md:254`
state that a `Change` names the aggregate it was decided on and that the crossing is `ErrFamily`;
`docs/ai/flows/FL-036…:355` names `TestAnAppendRefusesAChangeDecidedOnAnotherAggregateOfThisFamily`
in its test table.

### GAP-202 [low][deferred] `Fold`'s doc comment carries the ragged rewrap the repair left, and one clause in it is now imprecise

- **Where:** `event/aggregate.go:63-67`.
- **What:** the sentence the repair extended was not rewrapped: line 67 reads
  `// refusal from Fact.New. The fourth is per change and` — 52 columns against the ~75 every
  other line in the block holds — which is the visible trace of an in-place edit rather than a
  rewrite. In the same clause, *"the first three are checks over the whole list before any fold
  runs"* is true of causes two and three and not of cause one, which is a single `locate(id)`
  before the list is looked at.
- **Why this severity:** low. It is cosmetics and one imprecise clause in a comment; no caller and
  no test reads it, and the comment's substantive content — the fixed order, the consumed state,
  and what the returned state means on each failure — is accurate and is an invariant the code
  genuinely cannot make visible, so the comment itself belongs there under this tree's house rule.
- **Why this timing:** deferred. Module-internal prose with no effect on any contract, and it is
  one rewrap in a file the next section will open anyway.
- **Close criteria:**
  - [ ] `event/aggregate.go:63-67` is rewrapped so no line in the block is short of the block's own
        width without a sentence ending there.
  - [ ] The clause distinguishes the one check that runs before the list from the two that run over
        it, or drops the count.
- **Status:** **closed** by round 12's remediation pass — see the dispositions below.


---

## Round 12 dispositions — remediation pass, 2026-09-08

Cluster `aggregate-crossing-and-log-injection` of `EVENTSOURCE_P1_REMEDIATION.md` § 11, re-opened by
round 11 for the prose the (a) repair left. One finding from this file.

| Gap | Severity | Disposition |
|---|---|---|
| GAP-202 | `[low][deferred]` | **closed** — `event/aggregate.go`'s `Fold` block is rewrapped and the count clause is replaced by the distinction it was eliding |

**Reproduced first:** measured over the nine lines of the block as round 9 left them, the rune
widths were `76 80 79 74 54 75 79 78 20` — the `54` is the finding's `// refusal from Fact.New. The
fourth is per change and`, mid-sentence and 20-odd short of its neighbours, which is the visible
trace of the in-place edit. Re-measured after the rewrite: `75 76 79 78 77 80 76 73 62`, and the
only line under the block's width, the `62`, ends the block's last sentence.

**What changed.** The clause *"the first three are checks over the whole list before any fold runs"*
is now *"The first is one check before the list is looked at: the identity renders an illegal key.
The next two are passes over the whole list before any fold runs: …"*, which is what the code does —
`locate(id)` runs once before either loop. The block is rewrapped at the width the rest of it holds;
re-measured, no line is short of it without a sentence ending there. Nothing else in the comment
changed: the fixed order, the consumed state and what the returned state means on each failure are
the invariant the code cannot make visible and stay.

**No test is left behind and that is deliberate.** The finding is prose with no caller and no test
reading it; a test over a comment's line widths would pin formatting rather than behaviour, which is
not what this tree's suites are for. The substantive half of the clause — that a `Fold` refusal from
the first three causes returns the state untouched — is already pinned by
`TestFoldRefusesAnotherInstance`, whose subtests assert the returned state on each of them.

### Gate, run here

`gofmt -l .` silent · `go vet ./event/...` ok · `go test -race -count=2 ./event/...` green ·
`make unit` exit 0 · `make check` exit 0. GAP-182 itself was not touched: `decidedFor`'s
`this.origin == on` arm and both its doors are unchanged, and
`TestFoldRefusesAnotherInstance/two aggregates over one family are told apart by the declaration`
and `TestAnAppendRefusesAChangeDecidedOnAnotherAggregateOfThisFamily` are both still green.

---

## Round 13 — econv-implementation-reviewer (clean context, re-audit of the round-12 remediation cluster) — 2026-09-08

Scope: the aggregate-crossing half of cluster `aggregate-crossing-and-log-injection` — **GAP-182**
and the **GAP-202** it left behind — and every file the two remediations touched:
`event/change.go`, `event/aggregate.go`, `event/fact.go`, `event/repo.go`, `event/fold_test.go`,
`event/repo_test.go`. Nothing was inherited from the disposition notes: the crossing was
re-constructed from outside `package event` through the exported API, and the repair was reverted in
place, the suite run, and the file restored from a byte-level backup.

The tree was left byte-identical to how it was found. `md5sum` before the mutation and after the
restore: `event/change.go` `16fe2a4be211940732c77d53ec850198`, `event/aggregate.go`
`dc59c1e5efb6c86e886c91964f4e2097`, `event/fact.go` `d400976d4395769889aa2085227a4876`,
`event/repo.go` `f360915020180a2279242f952b83e05a`. Three throwaway probe packages were deleted;
`git status --porcelain | grep -c zzaudit` = **0**.

### Gate, run here

`gofmt -l .` silent (exit 0, whole repository) · `go vet ./event/...` exit 0 ·
`go test -race -count=2 ./event/...` green, no flake, twice — `event` 6.030 s / 6.064 s,
`eventmemory` 1.488 s / 1.499 s, `eventtest` 4.009 s / 4.031 s.

### Metrics — counted, not eyeballed

| Metric | Command | Value |
|---|---|---|
| `event/change.go` | `wc -l` | 54 |
| `event/aggregate.go` | `wc -l` | 113 |
| `Change[S].decidedFor` | `awk '/decidedFor/,/^}/' \| wc -l` | 13 |
| `Aggregate.Fold` | `awk '/\) Fold/,/^}/' \| wc -l` | 24 |
| fields on `Change[S]` | count | **7** — at `architecture.md`'s threshold, not over it |
| exported methods on `Change[S]` | `Stream`, `Err` | 2 |
| exported methods on `*Aggregate[S, ID]` | `Family`, `Key`, `Fold` | 3 |
| parameters, `decidedFor` / `Fold` | count | 2 / 3 (limit 4) |
| nesting depth, `decidedFor` / `Fold` | count | 1 / 2 (limit 3) |
| exported surface added by the repair | `git diff HEAD -- event/change.go event/aggregate.go \| grep -E "^\+(func\|type\|var\|const) [A-Z]"` | **0** — `origin` is unexported, `Declaration` pre-existed |
| callers of `decidedFor` | `rg -n decidedFor event/*.go \| grep -v _test` | **2** — `aggregate.go:78`, `repo.go:76` |
| readers of the `Declaration` interface (non-test) | `grep -rn Declaration event/*.go event/eventtest/*.go \| grep -v _test` | **3** — `binding.go:17`, `change.go:18`, `eventtest/proxies.go:69` |
| kernel imports of a concrete store | `grep -rn "eventmemory\|eventpg\|eventtest" event/*.go \| grep -v _test.go \| wc -l` | **0** |

### Microkernel — derived here, not inherited

An external package implementing the whole eight-method `event.Store` (`var _ event.Store =
(*store)(nil)` compiled) was written and run: it constructs `Capabilities`, `Limits`,
`NewBacking`, `NewAuthority`, `Stream`, `Compose`, `Envelope` with an arbitrary `Type`, `Record`,
`Cursor`, reads `AppendRequest`, and builds `Failure` over all seven exported outcomes — then drove
`Repo.Load` and `Repo.Append` end to end. **A store never constructs a `Change`**, so the `origin
Declaration` field the GAP-182 repair added changes nothing a store must build.
**`event/eventpg` is still writable with zero diffs under `event/`. Microkernel passes.**

### GAP-182 — closed, verified by construction and by mutation

Reproduced from outside the package: two `*Aggregate[account, accountID]` values over the family
`ledger.account`, the second declaring `ledger.credited` with a **subtracting** fold, a change minted
on the first, folded through the second:

```
streams equal : true ("acme/A-17" vs "acme/A-17")     ← the stream comparison cannot be what refuses
own fold      : balance=250 err=<nil>                 ← control: the minting aggregate still folds it
crossed fold  : balance=7
                err=event: this family is already bound to another aggregate:
                    "ledger.credited" was decided on another aggregate of this family
                isFamily=true  isWrongStream=false
```

`balance=7` is the argument unchanged, so the refusal is *no fold ran* and not *the wrong fold ran*.
Reverting `event/change.go:44` from `case this.stream == stream && this.origin == on:` to
`case this.stream == stream:` turned **both** doors red in one run:

| Test | Answer with the repair reverted |
|---|---|
| `TestFoldRefusesAnotherInstance/two aggregates over one family are told apart by the declaration` (`fold_test.go:291`) | *a change minted on another aggregate of this family folded with `<nil>`* |
| `TestAnAppendRefusesAChangeDecidedOnAnotherAggregateOfThisFamily` (`repo_test.go:157`) | *a change decided on another aggregate of this family appended with `<nil>`* |

Nothing was widened, re-derived branch by branch rather than read: the first case is now a
**conjunction**, so it accepts strictly less than before; a change carrying `err` with a zero stream
still misses it and answers the carried `ErrKey`; a change carrying `err` with a matching stream and
origin still enters `Fold`'s second loop; the new `this.stream != stream` arm carries the message
the old fall-through carried, and the fall-through is reachable only when the streams match, where
`ErrFamily`'s wording is true. `this.origin == on` cannot panic: `Declaration`'s unexported
`declaration()` keeps the dynamic type to `*Aggregate[S, ID]`, a pointer, and `decidedFor` is
unexported so no foreign non-comparable implementor can reach it. The controls hold —
`fold_test.go:279` and `repo_test.go:147` assert stream equality *before* the crossing, so the case
cannot degrade into the comparison it sits beside, and `repo_test.go:167` appends the same token and
the same change through the twin's own repository and requires it to reach the store exactly once.
`errors.Is(err, ErrFamily) && !errors.Is(err, ErrWrongStream)` is asserted at both doors.

The spec is not contradicted. §INV-039/UC-002's stated control is *"the crossing is `ErrWrongStream`
with zero events"* for two aggregates over **one state type and two families**, and
`fold_test.go/two aggregates over one state type are told apart by the family` still answers
`ErrWrongStream` there. The same-family case the spec left to the implementation is the one that
now answers `ErrFamily`.

### GAP-202 — closed

Re-measured over `event/aggregate.go:57-70`: rune widths `75 78 77 66 2 75 76 79 78 77 80 76 73 62`.
The only two lines short of the block's width, the `66` and the `62`, each end a sentence; the
finding's mid-sentence `54` is gone. The clause is now *"The first is one check before the list is
looked at … The next two are passes over the whole list before any fold runs"*, which is what the
code does — `locate(id)` runs once at `aggregate.go:73`, before either loop at `:77` and `:82`.

### GAP-203 [low][immediate] The `Declaration` interface's comment says it has exactly two readers and that neither has arrived; it has three, and this repair added the third

- **Where:** `event/aggregate.go:104-108`.
- **What:** the comment reads *"it has exactly two readers in phase 1 — the Binding's bound-family
  set and the conformance suite's family check, **neither of which has arrived yet**"*. Counted:
  `grep -rn "Declaration" event/*.go event/eventtest/*.go | grep -v _test.go | grep -v ErrDeclaration
  | grep -v "declaration()"` finds **three** readers and both of the two named have arrived —
  `event/binding.go:17` (`families map[string]Declaration`, plus `:21` and `:48`),
  `event/eventtest/proxies.go:69` (`func Families(t *testing.T, declarations ...event.Declaration)`),
  and `event/change.go:18` + `:42` (`origin Declaration`, `decidedFor(stream Stream, on Declaration)`),
  which is the GAP-182 repair itself. So the comment is wrong on the count and wrong on the tense,
  and the repair under audit is what made the count wrong.
- **Why this severity:** low. It misleads a reader and nothing else: no caller and no test reads it,
  and `Declaration`'s contract — one non-generic view, kept closed by an unexported method — is still
  accurately described by the first sentence. It is not `medium` because no public name or signature
  is wrong.
- **Why this timing:** immediate. `Declaration` is exported and is the seam a phase-2 author meets
  when asking what may hold one; the enumeration is the only place that answers, `seal.go:12-24`
  establishes the tree's convention that such an enumeration is exhaustive and load-bearing (a row
  without a reader fails a test there), and this comment now says the opposite of the code in a file
  phase 2 will open. It is one sentence, and it is cheap exactly now.
- **Close criteria:**
  - [ ] `event/aggregate.go:104-108` names the readers that exist — the `Binding`'s bound-family set,
        a `Change`'s origin, and the conformance suite's family check — or drops the count rather
        than carrying a wrong one.
  - [ ] `grep -rn "Declaration" event/*.go event/eventtest/*.go | grep -v _test.go` and the comment
        agree, and nothing in the comment claims a reader has not arrived when it has.
- **Status:** open

### GAP-204 [low][deferred] `ErrFamily`'s own text asserts a binding, and the store-free `Fold` door that now raises it binds nothing

- **Where:** `event/errors.go:28` (`ErrFamily = errors.New("event: this family is already bound to
  another aggregate")`) reached from `event/change.go:53` through `event/aggregate.go:78`.
- **What:** measured here, on the pure `Aggregate.Fold` path with no `Binding`, no `Bind` and no store
  anywhere in the program:

  ```
  event: this family is already bound to another aggregate: "ledger.credited" was decided on
  another aggregate of this family
  ```

  The sentinel half states a fact that is false for this caller — nothing was bound — and the wrapped
  half states the true one. The class is right and the reuse is the plan's own decision (the two doors
  answer one question, `PLAN.md:6265`); the *text* was written for `Binding.hold` at
  `event/binding.go:53` and was not revisited when a second, bindingless door started raising it.
- **Why this severity:** low. `errors.Is` is unaffected, the refusal is correct, and the wrapped
  clause carries the accurate sentence; only the leading phrase reads wrong in a log line. §INV-025 is
  not breached — no data is rendered, and `"ledger.credited"` is a declared identifier that passed
  `checkName`.
- **Why this timing:** deferred. The spec's own compatibility rule (§INV-024's growth clause) makes
  adding or moving a sentinel breaking and says nothing about its message, so the text can change
  after the tag; `TestEveryRenderingNamesAClassAndNeverAValue/a rendering says which kind of thing it
  is and which one of them` compares sentinel messages for uniqueness and would keep holding. Nothing
  in phase 2 depends on it.
- **Close criteria:**
  - [ ] `ErrFamily`'s message is true at both doors that raise it — `Bind` and the `Fold`/`Append`
        crossing — or the doc comment on `decidedFor` states why `Bind`'s wording is kept at a door
        that binds nothing.
  - [ ] The sentinel-uniqueness subtest still passes and `docs/modules/{en,ru}/event.md`'s *One family
        names one aggregate* section quotes whatever text is chosen.
- **Status:** open

### Round 13 verdict

**GAP-182 and GAP-202 are genuinely closed**, verified by re-constructing the crossing from outside
the package and by reverting the comparison and watching both doors go red in one run. The repair
narrowed rather than widened, added no exported surface, introduced no nil or panic path, and left
`event/eventpg`'s zero-diff obligation untouched because no store ever constructs a `Change`. The
three controls that keep the two new tests from passing vacuously were each checked against the code
rather than read off the note. Zero open `[critical][immediate]` or `[high][immediate]` from this
file. GAP-203 is `[low][immediate]` — one sentence the repair itself falsified — and GAP-204 is
`[low][deferred]`.
