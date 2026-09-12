# EVENTSOURCE P5 — S1 (the bounded read and the digest) — GAPS

## Round 1 — econv-code-reviewer — 2026-09-12

**Two blocking findings, and both are mutation survivors in `Repo.Digest`.** Neither is a
defect in shipped behaviour — the guards are in the tree and they work — but each is a
guarantee this section declares, publishes and will make durable in S3 that **no test in the
repository can falsify**. GAP-1: deleting `Digest`'s third step leaves `./event/...` green and
makes two different operations share one fingerprint (driven, hex below). GAP-2: reordering the
digest preimage and flipping its endianness leaves `./event/...` green, while the same package
freezes `Compose`'s rendering with a golden-vector test and a consequence sentence.

**Everything else this section claims about itself reproduces, most of it exactly.** The pasted
checkpoint is real — I re-ran every arm including `make api`, the three-added/zero-removed
surface diff and all three manifest commands. Eleven of the twelve mutations the section's own
table claims were re-applied and each was caught by the test named beside it, including the
correction the section wrote about UC-233's arm: `[v3,v1,v2]` at bound 2 is caught either way,
and only the `[v1,v3,v2]`-at-bound-1 arm carries the truncate-first claim. I drove seven hard
states of my own rather than reading the tests that drive them. **No ES-08 nuance and no ES-07
digest nuance from the study is dropped** — the enumeration is below.

**The one foreign red is the one named at the head of the plan.** `go test -count=1 ./scripts/`
reports exactly one `--- FAIL:` line, `TestNoI18nPackageCostsMoreThanItsErrorSeam`, on
`github.com/go-json-experiment/json/jsontext`. It is the owner's i18n work, it is not this
section's, and I did not touch it or the allowlist. **`make unit` is therefore not green** and
this report does not claim it is.

---

## The pasted checkpoint output is real. Every line re-run at HEAD

```
gofmt -l . | wc -l                                                             0
go build ./...                                                                 silent
go vet ./event/...                                                             silent

go test -list '^(the ten)$' ./event/ | grep -c '^Test'                         10
go test -race -count=1 -run '^(the ten)$' ./event/         ok  1.014s   (plan pasted 1.016s)
go test -race -count=1 ./event/...      ok  event 6.759s / eventmemory 1.511s
                                        / eventtest 4.327s / projection 1.917s
                                        (plan pasted 6.689 / 1.504 / 4.320 / 1.919)

./scripts/vv api                        regenerated; byte-identical to the tree's file
event-section diff vs HEAD              ADDED=3  REMOVED=0   (ErrVersion, Digest, StateAt)
whole-file git diff of surface.md       the three lines the plan pasted, verbatim

./scripts/checks.sh event-kernel-baseline    154 files recorded (HEAD holds 152)
   re-recording is idempotent: diff of the manifest before/after is empty
./scripts/checks.sh event-kernel             check-event-kernel: ok
./scripts/checks.sh event-kernel-moved …     the same ten paths the plan pasted, in order

./scripts/vv check                      all arms ok — check-deps, check-tiers, check-utils,
                                        check-triplets, check-todo, check-replaces, check-tidy,
                                        check-otel-schema, check-otel-module, check-workspace,
                                        check-event-kernel
go test -count=1 ./...  (root module)   green everywhere but scripts/TestNoI18n…  (1 FAIL line)
go test -count=1 -run '^(the three event arms)$' ./scripts/    all PASS

FROSTGROVE_EVENTPG_TEST_DSN=… go test -race -count=1 -tags=integration ./event/eventpg/...
                                        ok  116.995s   (plan pasted 111.462s, did not skip)
```

The manifest fence is honest in the strong direction too: `git status --porcelain event/`
lists exactly the ten paths `event-kernel-moved` printed, all inside the section's anchored
allowed set, and nothing under `event/eventtest/` or `event/eventpg/` moved. `replay_test.go`
is in the allowed set and did **not** move, which the section records rather than absorbs;
`event/refusal_test.go` was added to the allowed set with its reason written above the
checkpoint, and the reason is real — `declaredVocabulary()` lives there and
`TestTheRefusalVocabularyIsAPartition` pairs it against every `Err*` in a non-test file.

## Metrics counted, not eyeballed

| Metric | Value |
|---|---|
| `event/repo.go` | 404 lines (HEAD: 268) |
| longest function in it | `Append` 36 lines; `replay` 28; `StateAt` 22; `Digest` 21; `digestOf` 10; `prefixed` 4; `eightBytes` 5 |
| max nesting depth added | 3 (`for` → `if` → `return`), in `replay` |
| imports of `event/repo.go` | 5, **all stdlib** (`context`, `crypto/sha256`, `encoding/binary`, `fmt`, `hash`); three added, no internal import, no third-party — the root module still resolves nothing |
| exported symbols in package `event` | 137 surface lines, +3 (`ErrVersion`, `Repo.Digest`, `Repo.StateAt`), 0 removed, 0 changed |
| new types / new interfaces / new globals | 0 / 0 / 0 |
| store-contract methods moved | 0 — `Store`, `Log`, `Checkpoints`, `Limits`, `Envelope`, `Record`, `AppendRequest` untouched (INV-120) |
| `event/eventtest` files moved | 0 |
| mutations applied to `event/repo.go` | 15; 13 caught, **2 survived** (GAP-1, GAP-2) |
| hard states driven | 7 |

## The hard states this section makes possible, driven

Each was constructed and run against the real code; the answer is quoted, not inferred.
(ES-05's park/cutover waits, ES-07's `Resolve` standings and ES-09's snapshot fold are S2/S3/S4
and are not reachable from this section — there is no `Wait`, no `receipt` and no snapshot in
the tree.)

| State | What happened |
|---|---|
| an unreadable event **above** the bound (undeclared type at v5, `StateAt(…,3)` and `(…,4)`) | folds and returns cleanly, `{Balance:6}` and `{Balance:10}`; `StateAt(…,5)` and `Load` both answer `ErrUnknownType`. Correct: v5 is not in the prefix, and the bound is not a way to hide it from a read that does cover it |
| an unsupported revision inside the prefix | `ErrRevision` + the zero state, and `Load` over the identical stream gives the byte-identical sentence |
| `StateAt(…, math.MaxUint64)` over a 5-event stream | `ErrVersion`, zero state, 3 reads — no overflow, no hang, no confirming read |
| a store answering a 2-envelope page where it publishes 4 (a silently truncated history), bound at 8 | `ErrVersion` — a refusal rather than a wrong state, but diagnosed as the caller's bound. See GAP-4 |
| `Digest` over a batch carrying its own refusal (`reading{Rate: +Inf}`) | `ErrEncode`, and `Append` over the same batch answers the **identical sentence**. The behaviour is right; nothing pins it — GAP-1 |
| `Digest` over a token minted against another backing | returns a fingerprint and **no error**; `Append` answers `ErrWrongStore`. The fingerprint equals the one taken over this store's own token. See GAP-5 |
| `StateAt` on a closed store, and at version 0 on a closed store | `ErrClosed` for a real bound; `ErrVersion` **before any store call** for version 0 — consistent with `Load`'s own documented "reading the retained limits issues nothing" |

## Nuance conformance — every ES-08 and ES-07-digest nuance the study records

| Study nuance | Where it is in the code |
|---|---|
| ES-08 §2.1 — the source returns `null` for four situations a caller cannot tell apart; the appendix refuses all four | `repo.go:72-74` (version 0), `:83-85` (past the head / empty stream, one refusal), `:79-82` (an unreadable event is its own sentinel). Driven above; pinned by `TestAVersionPastTheEndAndAnEmptyStreamAreOneRefusal` asserting the two messages are **equal** |
| ES-08 §2.2 — Marten pays a full prefix read to notice | vv notices at the same point for the same money: `reached < version` after the loop, and `TestAVersionPastTheEndAndAnEmptyStreamAreOneRefusal` asserts the refusal costs the **same 3 reads** a load costs, not 4 |
| ES-08 §2.3 — the half-open/half-closed asymmetry of `fromVersion`/`toVersion` | only the `<= :to` half ships; P-13's home for the other half is `replay`'s existing `at` seed. Not in `replay`'s comment — backlog item 22 already has this open and S1 shipped exactly the shape it warned about |
| ES-08 §2.4 — a timestamp is neither business time nor commit order | no time parameter anywhere; `TestABoundedReadYieldsNothingThatCanAppend` asserts `NumIn()==3` and `In(2)==Version` so an overload cannot be added quietly. The page sentence is S6's by the matrix |
| ES-08 §2.6 — the result must not be appendable | `StateAt` returns `(S, error)`; the reflective arm asserts two outs, neither an `At[S]`, beside `Load`'s unchanged three |
| ES-08 §3.5 — a ceiling on `Store.ReadStream` would be a contract change | not taken: truncation is in Go, over-read bounded by one page, `event/eventtest` byte-identical, `make api`'s store section unmoved |
| ES-07 §2.1 — idempotence anchored to the caller's key, not to `(stream, expectedVersion, ids)` | the expected version is **not** in the preimage (`repo.go:196-205`), pinned by `TestTwoAttemptsAtDifferentVersionsDigestEqual` **and** by the must-not-differ fourth row of the collision table |
| ES-07 §2.2 — a partial overlap is its own answer, which a whole-range fingerprint reproduces for free | `digestOf` covers the whole batch in order; the reordered-records row of the collision table is the half that pins it |
| ES-07 §2.3 — an idempotence claim is only as strong as the concurrency check it is anchored to | `Append`'s `Expected` is untouched (`repo.go:141`), and the `Digest` comment says the anchor stays there. Whole `event` suite green on an unchanged `Append` |
| ES-07 §2.4 — nothing here is a retry | `Digest` issues no store call: `TestADigestIsTheBytesThisAppendWouldWrite`'s recording-store arm asserts `map[string]int{}` and `made() == 0` |
| INV-125 — a codec that mints a value disables the mechanism, loudly | the stamping-codec arm is present **and** its failure message labels two fingerprints as *"the arithmetic working and the idempotence mechanism DISABLED"*, which is exactly what the plan asked for and is the one place a reader would otherwise copy the wrong lesson |

**Nothing in this list is missing.** The two blocking findings below are about guarantees that
are in the code and not in any test, not about nuances that were dropped.

## Binding decisions

| Decision | Verdict |
|---|---|
| [[D-128]] the log delivers in position order | untouched — `StateAt` reads one stream through `ReadStream`, never `ReadAll`, and orders nothing by `recorded_at`. No filter or predicate reached `ReadStream` |
| [[D-129]] a checkpoint is a store-minted cursor | no checkpoint code in this section; `Checkpoints` unmoved in the surface |
| [[D-130]] a projection is a supervised runner | `event/projection` untouched in this section |
| [[D-133]] a fence loser takes turns | untouched |
| [[D-118]] a durable write inside the caller's transaction IS the outbox | no table, no durable-intent row, no schema change: `event/eventpg` has no modified file and `SchemaVersion` is unmoved |
| [[D-092]] anything continuous is a `runtime.Runner`, constructors start nothing | `StateAt` and `Digest` start nothing, hold nothing, and `Repo` keeps no state — `TestAPrefixFoldsToTheStateItsVersionHolds`'s fourth arm measures two identical reads costing identical `ReadStream` counts |
| [[D-126]] the store chooses no isolation level | `StateAt` asks `this.transaction(ctx, backing)` and opens nothing; the `ErrTransactionMismatch` arm added to `repo_test.go` proves a bounded read cannot escape the caller's unit |
| [[D-101]] migrating is a deployment profile choice | no migration, no schema version, no profile |
| [[D-132]] no snapshot authority is declared or promised | `TestNoSnapshotAuthorityIsDeclaredOrPromised` green; no identifier matching `(?i)snapshot` in any file this section moved |

## Contract conformance, both ways

Signatures match the plan's *Contracts before code* exactly:
`func (this *Repo[S, ID]) Digest(at At[S], changes ...Change[S]) ([32]byte, error)` and
`func (this *Repo[S, ID]) StateAt(ctx context.Context, id ID, version Version) (S, error)`,
`replay` gaining one `upTo Version`, `ErrVersion` in the request class beside `ErrKey` and in
`vocabulary()`. Receiver `this` throughout. No extra public surface: the `make api` diff is
three added lines and nothing else in the whole file. Nothing in the section's *Realises* list
is missing, and nothing is marked done that is not in the tree.

Two declared file-list departures, both written into the section rather than absorbed:
`event/refusal_test.go` added to the allowed set (justified — I confirmed
`declaredVocabulary()` makes the section unwritable without it), and `event/replay_test.go`
not extended (justified — `replay`'s new parameter is `0` on every `Load` path and
`TestAnUnreadableEventInThePrefixReturnsTheZeroState`'s through-a-`Load` control is what
falsifies INV-118's "not a second implementation").

---

### GAP-1 [high][immediate] `Digest`'s third step is a mutation survivor, and without it two different operations share one fingerprint

- **Where:** `event/repo.go:184-188` (the guard) —

  ```go
  for _, change := range changes {
      if change.err != nil {
          return none, change.err
      }
  }
  ```

  and the table that is supposed to pin it, `event/digest_test.go:158-182`
  (*"a malformed batch is refused with the sentinel an append gives it"*), whose five rows
  cover step 1 (`ErrKey`), step 2 twice (`ErrWrongStream`) and step 4 twice (`ErrTooLarge`)
  and **no** row for step 3. The claim it fails to hold is `event/repo.go:154-158`
  (*"It runs the first four of Append's six steps — … each change's own carried refusal …
  and answers the same refusals Append would"*), §UC-224's **Control**
  (`EVENTSOURCE_P5_USECASES.md:2203-2206`) and §INV-116.
- **What:** deleting the loop leaves the whole event tree green:

  ```
  $ go test -count=1 ./event/...
  ok  github.com/frostgrove/vv/event             1.556s
  ok  github.com/frostgrove/vv/event/eventmemory 0.465s
  ok  github.com/frostgrove/vv/event/eventtest   1.884s
  ok  github.com/frostgrove/vv/event/projection  0.809s
  ```

  Under the mutation the consequence is not a missing refusal, it is a **collision**, because
  a change whose codec refused carries a nil payload and `records` happily builds a record
  from it. Driven, with the guard removed and two genuinely different decisions of one fact:

  ```
  +Inf  -> 12f8779f58536ba76cc1bfa9ca7ab646d69a6a11e414d47faf53e9824671e9e6 err=<nil>
  NaN   -> 12f8779f58536ba76cc1bfa9ca7ab646d69a6a11e414d47faf53e9824671e9e6 err=<nil>
  two DIFFERENT operations share one fingerprint: true
  ```

  Restored; `diff` against the pristine file is empty and `check-event-kernel` is ok.
- **Why this severity:** the fingerprint is the whole of ES-07's *«другое содержимое с тем же
  ключом — конфликт»* and §INV-116's *"covers one append's whole range"*. Under S3 this value
  goes into a durable ledger row and decides `Repeated` against `Collided`. The concrete
  failure the suite would not see: a caller presents key `K` for a decision that fails to
  encode, the receipt records the shape-constant fingerprint; a **different** decision of the
  same fact, under a different key, also fails to encode and is answered from the wrong row —
  a false *already done* for an operation nobody performed. It stays at `[high]` and off
  `[critical]` because the guard is in the tree today and the behaviour is correct: I drove it
  and `Digest` and `Append` answered the identical `ErrEncode` sentence. What is missing is the
  arm that keeps it that way, in a section whose own contract enumerates the four steps.
- **Why this timing:** S3 writes `receipt.Fingerprint` around this value in the next-but-one
  section and the ledger row becomes durable. An unpinned step in the kernel half is exactly
  the thing that cannot be found later — it produces no error and no wrong type, only two equal
  hashes. It is one row in an existing table.
- **Close criteria:**
  - [x] `event/digest_test.go`'s malformed-batch table gains a sixth row driving a change that
        carries its own refusal — the `reading{Rate: math.Inf(1)}` shape
        `event/fold_test.go:220-235` already uses, declared on an `account` aggregate by
        `bindRates` so it sits in the table beside the other five — asserting
        `errors.Is(err, ErrEncode)`, the zero `[32]byte`, and `err.Error()` equal to `Append`'s
        over the same batch, exactly as the five existing rows do.
  - [x] The subtest now **opens** with the pair: `+Inf` and `NaN`, two decisions of one fact
        `encoding/json` refuses, digested and asserted refused before the table runs. The order
        is what makes the row fail on the collision rather than on the sentinel — a `t.Fatalf`
        ends the subtest, so the assertion that names the consequence has to run first.
  - [x] Deleting the `change.err` loop from `Digest` makes it red on the collision:
        *"two different decisions of one fact, neither of which its codec could encode, were
        answered rather than refused and are one fingerprint 551f9e1a…"*. Recorded in the plan's
        S1 transcript.

---

### GAP-2 [high][immediate] The digest preimage is a durable cross-build format with no frozen vector, in a package that freezes exactly this

- **Where:** `event/repo.go:196-216` (`digestOf`, `prefixed`, `eightBytes`) against
  `event/digest_test.go:294-325` (*"the preimage is length-prefixed and covers the type, the
  revision and the payload"*), whose every assertion is **relative** — `A != B` and `A == A`
  within one process. The precedent it does not follow is twenty-five lines away in the same
  package: `event/identity_test.go:5-29`, `TestComposeRendersTheFrozenKey`, twelve golden
  vectors with the consequence written into the failure message, backing
  `event/identity.go:39-47` (*"the rendering is frozen: it is part of every stream ever written
  under it"*).
- **What:** a wholesale change of the preimage layout survives the entire tree. Applied to
  `digestOf` — the revision hashed **before** the type, and `binary.BigEndian` swapped for
  `binary.LittleEndian` — injectivity is preserved, so every existing arm still passes:

  ```
  $ go test -count=1 ./event/...
  ok  event   ok  eventmemory   ok  eventtest   ok  projection      (no FAIL line)
  ```

  Nothing anywhere in the repository names a fixed digest value:
  `rg -n 'digestOf' --glob '*.go'` returns `event/repo.go` and `event/digest_test.go` and
  nothing else, and no test in either compares against a hex constant.
- **Why this severity:** this value is durable **by design** — S3 persists it in the ledger row
  and P-11 sets retention at 24 hours, which means the window in which build *N* writes a
  fingerprint and build *N+1* reads it is a routine deployment. Concrete failure: an operator
  rolls out a build in which somebody tidied `digestOf` (varint instead of eight fixed bytes,
  a domain-separation tag, fields reordered — all of which pass `make unit`, `make check` and
  `make api`, since `digestOf` is unexported); every idempotency key presented again inside the
  retention window recomputes to a different fingerprint, `Claim` answers `Collided`, and each
  legitimate retry is refused with *"this key was spent on another operation"* — which is
  false. §INV-125 deliberately chooses a loud failure over a silent one for a *drifting codec*;
  this is the same loud failure arriving for a reason the caller did nothing to cause, and
  §INV-116 states the preimage in prose while nothing in the tree holds the bytes. `[high]` and
  not `[critical]` for the same reason as GAP-1: today's bytes are correct and the whole
  collision table is green.
- **Why this timing:** the frozen-format sentence belongs on the function, and a golden vector
  is cheapest before a second reader has copied the layout into S3's `receipt.Fingerprint` and
  a live ledger table. After S5 there is a row in PostgreSQL that means whatever this function
  said at the time it was written.
- **Close criteria:**
  - [x] `event/digest_test.go` gains `TestTheDigestPreimageIsFrozen`, in the shape of
        `TestComposeRendersTheFrozenKey`: **six** `digestOf` inputs against their literal 32-byte
        hex — the empty batch, one record, two records, an empty payload at revision 0, a key
        whose own rendering is escaped again, and a revision over one byte — with a failure
        message that names what a change to the layout does to every receipt already written.
        The six were computed twice, once by `digestOf` and once by an independent
        implementation of the documented format; the two agree byte for byte, so the vectors
        freeze the format rather than whatever the function currently does.
  - [x] `digestOf` carries the sentence `Compose` carries, naming the four tidies that look
        harmless and saying the bytes are held as vectors rather than described. The same
        clause is added to §INV-116's **Statement** and to S6's D-142 specification, so the
        decision records it too.
  - [x] Four layout mutations, each caught, each restored — the reorder plus the endianness
        together, the reorder alone, the endianness alone, and `Compose` replaced by a bare
        `Family + "/" + Key` join. The last is why there are six vectors and not three: the
        empty batch catches an endianness flip and the composed-stream change, and only a
        record-bearing vector catches a field reorder. Recorded in the plan's S1 transcript.

---

## Recorded to the backlog under `## P5` — not blocking, not to be fixed here

### GAP-3 `[medium]` — `Digest` and `Append` carry the same four pre-store steps as two copies, and nothing pins them equal

`event/repo.go:113-132` and `event/repo.go:176-192` are byte-identical but for the return
values: `checkKey`, the `decidedFor` loop, the `change.err` loop, `records`. The contract that
binds them is a sentence in a comment (`:154-158`). A fifth pre-store check added to `Append`
— the shape of every past phase — leaves `Digest` answering a fingerprint for a batch `Append`
will refuse, and GAP-1 shows the suite does not hold the correspondence step by step. The
extraction is available: `checkKey`, then `Append`'s empty-batch short circuit, then one
helper carrying steps 2–4 that both call. A cheaper half is a test that drives every refusal
`Append` has before the store and asserts `Digest` answers the same sentence, which is the
shape `TestEveryRefusalClaimHasIsReachableThroughOnce` (P-16) already uses one section later.

### GAP-4 `[low]` — a store that truncates a history with a short page is reported as the caller's bad request

`replay` documents its own blind spot (`event/repo.go:307-310`, *"a store that returns a short
page that is not the end of the stream truncates a history silently"*) and the conformance
suite has a defect for it. What S1 adds is the rendering: driven, a store answering a
2-envelope page where it publishes 4, read at version 8 over an 11-event stream, answers
`ErrVersion` — *"this stream is shorter than the prefix this read was bounded at"* — which
wraps `crud.ErrBadRequest` (`event/errors.go:43`) and renders 400. So a store defect reaches
the caller as their own off-by-one. §INV-118 names three causes for `ErrVersion` and this is a
fourth, which is the shape §ES-08 §2.1 criticises Marten's `null` for. It is `[low]` because
the framework structurally cannot tell the two apart without the confirming read the contract
refuses, and because `Load`'s answer to the same store is worse (a silently truncated state).
What is owed is one sentence on the module page saying that `ErrVersion` also covers a store
that broke its short-page clause, and that the conformance suite is where that is caught.

### GAP-5 `[low]` — `Digest` does not answer `ErrWrongStore`, so one malformed append is not learned before the claim

Driven: `Digest` over a token minted against another backing returns a fingerprint and a nil
error, while `Append` over the same token answers `ErrWrongStore`. The fingerprint is
**identical** to the one taken over this store's own token, which is correct and deliberate —
§INV-116 excludes the backing from the preimage — so the finding is only about the refusal.
The comment's *"a caller that digests first learns a malformed append before it claims
anything"* (`event/repo.go:156-157`) is true of four of Append's six steps and not of the
fifth. Harm is bounded: S3's claim runs inside the caller's transaction, so the refused append
rolls the claim back with it. Owed is either the backing check in `Digest` (it reads
`this.store.Backing()`, which would cost P-12's zero-store-call property one call) or one
clause narrowing the sentence to the four steps it enumerates.

---

## Status

- GAP-1 — **Status: closed** `2026-09-12` — reproduced, then pinned. See *Round 2* below
- GAP-2 — **Status: closed** `2026-09-12` — reproduced, then pinned. See *Round 2* below
- GAP-3 — **Status: open** `[medium]` — backlog `## P5`, left alone
- GAP-4 — **Status: open** `[low]` — backlog `## P5`, left alone
- GAP-5 — **Status: open** `[low]` — backlog `## P5`, left alone

**Round 1 verdict: RED.** Two `[high]` findings block. Both are unpinned guarantees in
`Repo.Digest`, both were driven rather than reasoned about, and both are closed by test arms in a
file this section already owns.

---

## Round 2 — the two `[high]` findings closed — 2026-09-12

Both were reproduced from the pristine tree before anything was written. Round 1's evidence was
not taken on trust: the mutations were re-applied, the tree was re-run, and the collision hex was
re-driven.

Every line number above is as of round 1, and the ones in `event/digest_test.go` have since moved.
Today: `Digest`'s `change.err` loop is still `event/repo.go:184-188` — nothing was added above it —
`digestOf` is `event/repo.go:203-212` behind the frozen-format comment at `:196-202`, the
malformed-batch subtest is `event/digest_test.go:161-217` and `TestTheDigestPreimageIsFrozen` is
`:362-393`.

**GAP-1, reproduced.** The `change.err` loop deleted from `Digest`, `go test -count=1 ./event/...`
→ `ok event / eventmemory / eventtest / projection`, no `FAIL` line. Two decisions of one fact
that `encoding/json` refuses, digested through the mutated `Digest`:

```
+Inf  -> 12f8779f58536ba76cc1bfa9ca7ab646d69a6a11e414d47faf53e9824671e9e6 err=<nil>
NaN   -> 12f8779f58536ba76cc1bfa9ca7ab646d69a6a11e414d47faf53e9824671e9e6 err=<nil>
two DIFFERENT operations share one fingerprint: true
Append over the same batch: event: the payload cannot be encoded by its declared codec: crud: bad request
```

Round 1's hex to the byte. `Append` refuses the same batch, which is the asymmetry the finding is
about.

**GAP-1, closed.** `event/digest_test.go`'s malformed-batch subtest gains `bindRates` — an
`account` aggregate whose one fact carries a `float64`, so `+Inf` and `NaN` are two genuinely
different decisions neither of which encodes — a sixth table row at step 3's place in `Append`'s
order (`ErrEncode`, the zero `[32]byte`, `err.Error()` equal to `Append`'s), and, **before** the
table, the pair that names the consequence. With the loop deleted:

```
--- FAIL: TestADigestIsTheBytesThisAppendWouldWrite/a_malformed_batch_is_refused_…
    digest_test.go:179: two different decisions of one fact, neither of which its codec could
    encode, were answered rather than refused and are one fingerprint 551f9e1a…: a change
    carrying its own refusal carries no payload at all, so every unencodable decision of this
    fact digests to the record's shape alone and an operation key spent on one of them answers
    the other already done
```

The ordering is load-bearing and is why the pair is not at the end: `t.Fatalf` ends the subtest,
so the assertion that names the collision has to run before the one that names the sentinel.
Placed after the table, the reported failure was *"answered `<nil>` where a digest answers the
refusal an append answers"* — true, and the wrong lesson.

**GAP-2, reproduced.** The revision hashed before the type and `binary.BigEndian` swapped for
`binary.LittleEndian`: `go test -count=1 ./event/...` green in all four packages, no `FAIL` line.
Injectivity is preserved, so every relative arm passes.

**GAP-2, closed.** `TestTheDigestPreimageIsFrozen` holds six golden 32-byte vectors, and
`digestOf` carries the frozen-format sentence. Four mutations, each caught and each restored:

| Mutation | The vector that caught it |
|---|---|
| reorder + little-endian | the empty batch — `66d33f5b…` against `d332cf61…` |
| the reorder alone | one record — `1a2dff3d…` against `204bf6a4…`; the empty batch cannot see this one |
| the endianness alone | the empty batch; the length prefix of the composed stream is enough |
| `Compose(Family, Key)` → `Family + "/" + Key` | the empty batch, with the escaped-key vector as the reason it is a layout question: that key renders `acme%2Fevil%252FA-17` composed and `acme/evil%2FA-17` joined |

The vectors were computed twice — once by `digestOf`, once by an independent implementation of the
documented format — and agree byte for byte. A vector taken only from the code under test freezes
whatever that code does, defects included.

**Contracts changed with the code.** §UC-224's **Control** now names the carried refusal and says
why its absence is a collision rather than a missing refusal. §INV-116's **Statement** gains the
frozen-encoding clause and its **Falsified by** gains the golden vectors. The plan's S1 *Tests*
list gains `TestTheDigestPreimageIsFrozen`, its checkpoint counts **eleven** names instead of ten,
the coverage matrix's UC-224 and INV-116 rows are updated, and S6's D-142 specification gains the
frozen-format clause. The matrix ↔ counted-pattern extraction was re-run and reports nothing in
the matrix outside a counted pattern.

**Checkpoint re-run, whole chain, `EXIT=0`.**

```
gofmt -l .                                0
go build ./...                            silent
go vet ./event/...                        silent
go test -list (the eleven)                11
ok  github.com/frostgrove/vv/event            1.014s   # the eleven, -race
ok  github.com/frostgrove/vv/event            6.809s   # ./event/... -race
ok  github.com/frostgrove/vv/event/eventmemory 1.506s
ok  github.com/frostgrove/vv/event/eventtest   4.330s
ok  github.com/frostgrove/vv/event/projection  1.927s
make api                                  regenerated; surface diff ADDED=3 REMOVED=0
event-kernel-baseline                     154 files recorded
check-event-kernel: ok
event-kernel-moved: ok                    the same ten paths, all inside the allowed set
./scripts/vv check                        all eleven arms ok
go test -count=1 ./... (root module)      one --- FAIL: line, TestNoI18nPackageCostsMoreThanIts-
                                          ErrorSeam, the foreign i18n red. Not touched.
```

`make unit` is **not** green, and this round does not claim it is: the one red arm is the owner's
i18n work on `github.com/go-json-experiment/json/jsontext`, named at the head of the plan. The
three `event` arms in `./scripts` pass on their own counted run.

**The live suite was run three times and was red once, and that is reported rather than buried.**
S1 needs no database, but `event/eventpg` was run anyway. The first run failed on
`TestABurntGapIsPassedInsideOneReadAllAndALiveOneIsNot` — *"the walk beyond the gap answered []"* —
and the two runs after it were green (`ok 118.035s`, `ok 114.169s`); alone the test passes in
1.219s. It is the already-recorded cluster-wide-`pg_snapshot_xmin` drawback appearing as a flake:
any other session that has written and not yet ended holds the settlement floor down, and in a full
suite run the other tests are exactly such sessions. It is not this round's: the only non-test
change was a comment, and `event/digest_test.go` is a `_test.go` of package `event`, which is not
compiled into `eventpg`'s binary. Recorded as backlog item **27** under `## P5`.

**Verdict: GREEN for the two blocking findings.** GAP-3, GAP-4 and GAP-5 are untouched and stay in
`EVENTSOURCE_BACKLOG.md` under `## P5`.
