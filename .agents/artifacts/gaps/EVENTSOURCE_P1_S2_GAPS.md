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
- **Status:** open

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
- **Status:** open

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
- **Status:** open

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
- **Status:** open

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
- **Status:** open

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
- **Status:** open

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
- **Status:** open

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
- **Status:** open
