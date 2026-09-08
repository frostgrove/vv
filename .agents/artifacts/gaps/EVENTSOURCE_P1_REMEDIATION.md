# EVENTSOURCE_P1 — consolidated remediation list

Triage of every finding left open when the P1 chain's review gates hit their round caps. Every
item below was **re-verified against the code as it stands**, not against the GAPS note: by running
the mutation, by building the input, or by pointing at the lines that do the wrong thing. Findings
whose note said "open" but which a later round closed are in `## Already closed` at the end, with
the evidence that closed them.

**Method.** Two throwaway trees, both outside the repository, both deleted after use:

- `/tmp/vvprobe` — a module with `replace github.com/frostgrove/vv => <this checkout>`, used to
  drive the exported API from a third package. Nothing was written into the tree.
- `/tmp/vvmut` — a byte copy of the checkout with `go.work` intact, used for mutation runs.
  Baseline before every campaign: `go test -count=1 ./event/... ./scripts/` → all `ok`.

GAP ids collide between sections (S3, S4, S5 and S6 each restart at `GAP-1`; `GAP-T19` exists in
both S1's and S3's test files), so every id below is written `S<n>/GAP-…` for an implementation
review and `S<n>T/GAP-…` for a test review.

**Constraint that outranks every item here:** phase 2 writes `event/eventpg` with **zero diffs
under `event/`**. Every repair below is additive inside `event/`, `event/eventmemory`,
`event/eventtest` or `scripts/`; none of them requires a new exported symbol on the store seam.

---

## 1. `encodable-tagged-promotion` — critical — implementation

**Findings:** `S2T/GAP-T36` — `event/encodable.go:242`, `event/declaration_test.go`

**What is wrong.** `promotedMarshaller` skips an embedded field spelled `json:"-"`:

```go
if !field.Anonymous || field.Tag.Get("json") == "-" {
    continue
}
```

Go's method promotion does not read JSON tags. The tagged spelling still promotes `MarshalJSON`
onto the outer struct, so the struct writes itself as the embedded value and every field beside it
is written by nobody and read back by nobody — with `nil` at every door. This is the last
surviving arm of the GAP-178 / GAP-185 family, and it is an **implementation** hole, not a missing
row: the walk is asked and answers "legal".

**Proof (run in `/tmp/vvprobe`, a third package):**

```
type Money struct{ Cents int }
func (m Money) MarshalJSON()  ([]byte, error) { return json.Marshal(m.Cents) }
func (m *Money) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &m.Cents) }

type lineTagged   struct{ Money `json:"-"`; SKU string }   // the shape under test
type lineUntagged struct{ Money;            SKU string }   // the row that exists
type lineNamed    struct{ Amount Money;     SKU string }   // the accepting control

untagged  CanEncode = event: … lineUntagged writes itself through the MarshalJSON of the
                      embedded Money, so SKU is written by nobody …      (refused, correct)
named     CanEncode = <nil>                                             (accepted, correct)
json:"-"  CanEncode = <nil>                                             ← WRONG
json:"-"  encodes to 500 and reads back {Money:{Cents:500}, SKU:""}
```

**Close criteria.**
- `promotedMarshaller` asks whether the method is promoted, not whether the field is JSON-visible;
  the `json:"-"` skip at `:242` is removed or narrowed to something method promotion actually obeys.
- `event/declaration_test.go`'s refused table gains the `json:"-"` spelling beside the untagged
  one, with `lineNamed` as the accepting control.
- The walk's own contract comment at `encodable.go:47-62` and `#### event/codec.go`'s refusal list
  say the tag does not undo promotion.

---

## 2. `roundtrip-fidelity-broken` — critical — implementation

**Findings:** `S2/GAP-188` — `event/fact.go:256` and `:302-312`; `S2/GAP-189` — `event/fact.go:152`,
`:164`, `:190`, `:249-251`; `S2/GAP-190` — `event/fact.go:138-140`

`Fact.RoundTrip` is the framework's only runnable proof that a declared codec does not lose data.
Three defects, all reproduced, all in one file.

**(a) It panics out of exported kernel API.** `sameValue` calls `equalByMethod` at `:256`, *before*
the `Pointer`/`Interface` nil guard at `:270-273`. A payload field of pointer type whose type
declares `Equal` on the pointer receiver is invoked with a nil receiver:

```
type Node struct{ ID int }
func (n *Node) Equal(o *Node) bool { return n.ID == o.ID }
type ref struct{ Ref *Node; Note string }

event.JSON[ref]().CanEncode()            → <nil>
fact.RoundTrip(ref{Ref: nil, ...})       → panic: runtime error: invalid memory address
                                             …/event/fact.go:311 equalByMethod
                                             …/event/fact.go:256 sameValue
                                             …/event/fact.go:153 (*Fact).roundTrip
control: fact.RoundTrip(ref{Ref: &Node{ID:3}, ...}) → returns
```

`event/codec.go:26-35` claims to enumerate every extension point and its panic policy. `Equal` has
no row, and `roundTrip` has no `recover`. In S5 this panics the conformance binary rather than
reporting a refusal.

**(b) An application's `Equal` silently disables the check.** Measured with a codec that drops one
field:

```
type Doc struct{ Title, Body string }
func (d Doc) Equal(o Doc) bool { return d.Title == o.Title }   // Body not compared
codec: decodes then clears Body

with Equal:     RoundTrip → err=<nil>   out=[{Title:"t" Body:""}]        ← the drop passes
control, the same codec over a type with no Equal method:
                RoundTrip → event: the recorded payload cannot be read by this
                            declaration: … decodes to a different value
```

That is exactly the defect GAP-178's third close criterion added the comparison to catch, made
invisible by a method the application wrote for its own reasons.

**(c) Both value walks stop at 1024 and report a pass.** `sameValue` returns `true` on exhaustion
(`:249-251`), `sharesMemory` returns `false` (`:190`), and both take `codecGraphNodes` — the
**type**-graph bound reused for a quantity it does not measure. Measured with a codec that corrupts
one element of a `[]int`:

```
n=10     corrupts element 5      RoundTrip refuses = true    ← control
n=2000   corrupts element 5      RoundTrip refuses = true
n=2000   corrupts element 1500   RoundTrip refuses = false   ← proves nothing
n=5000   corrupts element 4999   RoundTrip refuses = false
```

The type walk at the same bound does the opposite and *refuses*, so one file answers a budget
exhaustion two ways.

**(d) A marker fact can never satisfy §UC-042.** An empty payload struct is legal at declaration and
`RoundTrip` refuses it forever, because its encoding equals its zero encoding:

```
TryDeclare(a, "opened", From(JSON[struct{}]()), fold)  → err=<nil>
fact.RoundTrip(opened{})  → event: this sample cannot prove what a round trip claims for it:
                            revision 1 of "opened" encodes its sample exactly as its own zero value
```

**Close criteria.**
- The nil guard runs before `equalByMethod`; `equalByMethod` refuses a nil pointer or nil interface
  receiver; a panic out of `Equal` is either recovered into a stated sentinel or the method is not
  called, and `event/codec.go`'s panic-policy table gains the row.
- The `Equal` arm no longer decides the verdict alone — narrowed to the shapes the field walk
  cannot answer (a struct with no exported field, which `time.Time` is), or a disagreement between
  `Equal` and the field walk is reported rather than resolved in `Equal`'s favour.
- The two value walks take their own named budget, and exhaustion is **reported** (`ErrSample`
  naming the sample as larger than the comparison walks) rather than resolved in the caller's
  favour.
- `Fact.RoundTrip` has a stated answer for a marker fact — an accepted no-op, a distinct sentinel,
  or §UC-042 scoped away from payload-free facts. Today the two doors contradict each other.
- `roundtrip_test.go` carries `ref{Ref:nil}`, the lenient-`Equal` drop, the `n=2000 @ 1500` case and
  the marker fact, each with the control that answers the other way today.

**Status: closed 2026-09-08.** All four reproduced first, byte for byte as written above, then
fixed. Dispositions and the full mutation campaign are in `EVENTSOURCE_P1_S2_GAPS.md` **round 5**.

- **(a)** `equalByMethod` is now reached only from `sameFields`, and only where the struct has **no
  exported field** — the one shape the field walk cannot answer and the shape `time.Time` is. The
  nil guard therefore runs before it by construction. Its panic is recovered into `ErrSample`, and
  `event/codec.go`'s policy paragraph and `[[D-124]]` both carry the row.
- **(b)** closed by the same narrowing: an application `Equal` can no longer veto a walk that had
  an answer of its own.
- **(c)** both walks take `valueWalkNodes = 1 << 16`, carry a `beyond` flag, and `RoundTrip` answers
  `ErrSample` naming the bound and the repair — the answer the type walk already gives.
- **(d)** a reader type of size zero holds exactly one value: the zero-value refusal does not fire
  for it, the non-aliasing half is skipped, fidelity still runs. `ErrSample` stays for a wrong
  sample. Test half **GAP-T33** closes with it and leaves the plan's `## Debt`.

**Note for cluster 3.** `event/fact.go` reached 403 lines against the 400-line budget, so the two
walks moved to a new **`event/comparison.go`** (`fact.go` 193, `comparison.go` 216), the same way
`event/routing.go` moved out of `event/encodable.go`. `sameValue`/`sharesMemory` are now
`valueWalk.same`/`valueWalk.shares` and take no `budget *int`; `sameValue`'s struct arm is
`valueWalk.sameFields`. No exported symbol changed — `make api` regenerates `docs/api/surface.md`
byte-identical — so `event/eventpg`'s zero-diff obligation is untouched.

---

## 3. `roundtrip-walks-unpinned` — critical — test

**Findings:** `S2T/GAP-T34` — `event/fact.go:199-214`; `S2T/GAP-T35` — `event/fact.go:269`, `:270-274`,
`:279`; `S2T/GAP-T37` — `event/encodable.go:101`, `:245-247`, `:258`

The two walks cluster 2 repairs are almost entirely inert, and the type walk has controls in one
direction only. Every row measured one mutation at a time, `go test -count=1 ./event/...` after each,
file restored between:

| Mutation | Suite |
|---|---|
| `sameValue`'s default (scalar) arm → `return true` | **SURVIVED** |
| `sameValue`'s pointer/interface arm → `return true` | **SURVIVED** |
| `sameValue`'s map length check dropped | **SURVIVED** |
| `sameValue`'s slice length check dropped | caught (`TestACodecThatDecodesIntoAReusedBufferIsCaught`) |
| `sharesMemory`'s pointer/interface arm → `return false` | **SURVIVED** |
| `sharesMemory`'s slice/array element loop deleted | **SURVIVED** |
| `sharesMemory`'s map entry loop deleted | **SURVIVED** |
| `sharesMemory`'s struct field loop deleted | caught |
| `besideIt` counts a `json:"-"` field | **SURVIVED** |
| `promotedMarshaller` stops comparing the write route | **SURVIVED** |
| an array element loses the position's addressability | **SURVIVED** |

The scalar arm is the one that compares every int, string and bool a payload holds; `return true`
there means the fidelity half of `RoundTrip` proves nothing about any scalar. The three
`encodable.go` mutations are *over-broad repairs*: each turns a shape the shipped codec encodes and
decodes perfectly into an `ErrCodecType` panic out of `Declare` — a process that will not boot —
with the suite green, because the accepted table has no row for any of the three.

**Close criteria.**
- `roundtrip_test.go` gains a case per surviving arm: an aliasing codec behind a pointer, one that
  aliases a slice element, one that aliases a map entry; a codec that corrupts a scalar, one that
  corrupts through a pointer, one that returns a map with a different length.
- `declaration_test.go` gains three accepted rows: `struct{ money; Note string `json:"-"` }`; a
  struct declaring its own `MarshalJSON`/`UnmarshalJSON` pair that embeds a plain struct with a
  field beside it; `struct{ Held [2]posted }`.
- Each mutation in the table turns exactly one named row red.

**Status: closed 2026-09-08.** Dispositioned in `EVENTSOURCE_P1_S2_TEST_GAPS.md`
`## Round 8 dispositions`. Tests only — nothing under `event/` outside `declaration_test.go` and
`roundtrip_test.go` changed. Every surviving mutation in the table above, re-run against the real
implementation one at a time, is now caught, plus four narrower ones and the constant swap
`valueWalkNodes` → `codecGraphNodes`. Four of GAP-T35's five arms turned out to be caught already
by fixtures added after the finding was written (`slippingCodec` for an integer, `unnotedCodec` for
a string, `ledgered` for the pointer arm through the wire fallback); what still survived was the
map-length check and the pointer arm wherever the walk itself could have answered, and both are now
red on their mutation. New: `roundtrip_test.go` *memory shared one hop in is found through a slice
element, a map value and a pointer* (three reusing codecs, three allocating controls, one wire
format whose zero encoding disturbs nothing) and *a scalar changed, a map entry dropped and a value
behind a pointer are each a difference* (four codecs, four faithful controls), plus the accepting
budget row `marksOf(codecGraphNodes + 8)`; `declaration_test.go` `muted`, `remitted` and
`JSON[struct{ Held [2]posted }]()` on the accepted table.

**Round 9 re-audited the cluster and confirmed all three closed, and found three more of the same
class in the same function** — `GAP-T53` `[critical]` (the value walk's *absence* arms: an entry
read back under another key, a map value changed one level in, and a set pointer read back as nil
were each refused by the implementation and by no test), `GAP-T54` `[medium]` (`asFloat`'s exactness
guard, the clause that stops the numeric widening at 2^53) and `GAP-T55` `[low]` (`spend`'s
carried-out short-circuit, which decides which of two refusals a payload with two problems reports).
**All three are closed 2026-09-08**, dispositioned in `EVENTSOURCE_P1_S2_TEST_GAPS.md`
`## Round 10 dispositions`, tests and docs only. New: `roundtrip_test.go` *a value read back
somewhere else, or not at all, is a difference too* (three codecs, three faithful controls), three
rows on the substitution table at the widening boundary (two refused, `1 << 52` accepted as the
control), and one row beside the func case for a payload carrying two carried-out conditions. Each
of the six surviving mutations round 9 logged is now red, and three over-refusal controls prove the
new rows are not vacuous in the accepting direction.

---

## 4. `eventtest-clauses-inert` — critical — test

**Findings:** `S5T/GAP-15`, `S5T/GAP-16`, `S5T/GAP-17`, `S5T/GAP-18` — `event/eventtest/sections_*.go`,
`event/eventtest/probe.go`, `event/eventtest/defects.go`

**This is the artefact phase 2's PostgreSQL store runs unchanged, and it is 90 % inert.**

Full clause-neutralisation census, run this pass. Every `this.refuse(...)` call in the suite's six
section files and in `probe.go` was replaced by `_ = 0`, one at a time, with
`go vet ./event/... && go test -count=1 ./event/...` after each and the file restored before the
next:

```
180 scorable clauses (2 more do not compile when removed)
SURVIVED  162      CAUGHT  18      detection rate  10 %
```

The 18 that are caught are `sections_lifecycle.go:74,256`, `sections_write.go:245,254,336`,
`sections_read.go:64,176,258`, `sections_ownership.go:108,166`, `sections_resumption.go:98` and
seven in `probe.go`. Everything else can be deleted with the whole tree green.

The reviewer's own numbers (27/165 = 16 %) were measured over a narrower clause set; this census is
wider and worse. The named consequences all reproduce:

- **`bounds` — 0 of 6 clauses load-bearing.** `sections_read.go:213, 221, 225, 230, 238, 241` all
  survive. `:221` is the assertion that a batch of `MaxBatch + 1` answers `ErrTooLarge`, and it is
  the S5 proof the matrix cites for **UC-025** and C4/GAP-103. A store whose batch bound is never
  enforced is certified by a section whose only live check is that a `Load` succeeds.
- **`binding` — 0 of 11.** `sections_write.go:12-61`, including `:44`, the `ErrFamily` arm, which is
  **UC-059**'s and **INV-039**'s only S5 clause.
- **`global order` — 0 of 4.** `sections_read.go:22, 37` and `subsequence`'s `:51, :57`, which are
  **UC-038**'s only proof anywhere in the tree.
- **UC-066** — `sections_transactions.go:190`, the single clause, survives.
- **UC-061** — `sections_ownership.go:184` and `:191` (`aliased`), the §INV-021 shape the section
  exists for, both survive.
- **The two shared cross-section clauses** — `probe.go:199` (a page longer than the store's own
  published `StreamPage`, at the raw-read door) and `probe.go:273` (`advanced`: a page answered
  beside the cursor it was read from, i.e. the infinite-resume outage) — both survive. One deletion
  there weakens twelve sections at once.

`defects_test.go` asks one question per defect row — *does the section named for this row report
`failed`* — so a row certifies the one clause that happens to fire first for that row's store, and
`bounds`, `binding` and `global order` are named by rows that break a **call** the section makes
(`probe.load`, `probe.bind`, the shared `ascending` helper), not a **clause** it asserts, which is
exactly what round 1's own close criterion forbade.

**Close criteria.**
- A clause-level mutation harness exists, is **run and recorded**, and the survivor list with its
  count is in the plan the way the defect inventory's size is.
- Every matrix row whose *Proved by* names an S5 section and nothing else has at least one clause
  demonstrated load-bearing: neutralise it, name the test that turns red, paste the message.
  UC-025, UC-059, INV-039, UC-038, UC-066 and UC-061 are the rows this is false for today.
- `bounds`, `binding` and `global order` each gain a defect row whose store answers every call
  successfully and **wrongly** in a way one of that section's own clauses catches — a store that
  admits a batch over its published `MaxBatch`; a binding that admits a second aggregate over one
  family; a log whose per-stream order is not a subsequence of that stream's own read.
- A defect row whose store answers a page longer than its published `StreamPage` at the `drain`
  door, and one whose `ReadAll` answers a non-empty page with the cursor it was given.

---

## 5. `eventmemory-conformance-holes` — critical — test

**Findings:** `S3T/GAP-T19` — `event/eventmemory/transaction.go:29-36`, `:64`;
`S3T/GAP-T20` — `:120-126`, `:128-132`; `S3T/GAP-T21` — `event/eventmemory/log.go:76`, `:87`,
`event/eventmemory/read.go:29`; `S3T/GAP-T22` — `event/eventmemory/transaction.go:48-54`

The store's own behaviour is right today. Nothing in the tree — including `eventtest`, which
certifies phase 2's store — would notice if it stopped being. Four mutations, whole
`./event/...` after each:

| Mutation | Suite |
|---|---|
| the transaction binding keyed by the `*Store` instead of the `*Log` | **SURVIVED** |
| `stage()` claims only the first stream a transaction touches | **SURVIVED** |
| `release()` releases only one claimed stream | **SURVIVED** |
| the log indexes streams by a **case-folded** key while the envelope keeps the caller's | **SURVIVED** |
| `Store.Transaction` swallows its error and reports "nothing bound" | **SURVIVED** |
| (control) `stagedFor` returns every staged record rather than this stream's | caught |

Each is the shape phase 2 has:

- **Store-keyed binding.** `eventpg`'s ordinary deployment is a store value per request over one
  backing. No transaction in the suite is ever used through a **second** store value over the same
  log, so a store-keyed binding lets every per-request store silently escape its caller's unit of
  work and autocommit.
- **Claim set.** Every transaction in the suite claims exactly one stream, so the set is untested in
  both directions: a store that claims only the first stream lets a second writer into the rest of
  a multi-stream unit of work; one that releases only one leaks claims permanently.
- **Byte-for-byte key identity (§INV-033).** Every key in the suite differs from every other in more
  than one byte. A PostgreSQL store on a case-insensitive collation would merge two aggregates'
  histories into one stream and this suite would certify it.
- **`Store.Transaction`'s third answer.** The contract at `event/store.go:146-159` names three
  answers; the third — an invalid authority **and** an error — is asserted nowhere, so the door that
  reports "something bound is not a transaction" may report "nothing bound" and fall through to
  autocommit.

**Close criteria.**
- A conformance case opens a unit of work on one store value and drives it through a **second**
  store value over the same backing, asserting the second one stages rather than autocommits.
  `eventtest.Factory` already has `Sibling`.
- A transaction that stages to two streams, asserting both are claimed and both are released, with
  a competing writer as the control on each.
- Two keys differing in exactly one byte — case and one-byte substitution — asserted to be two
  streams, with a control that the same key is one stream.
- A case that drives `WithTransaction(ctx, nil)` through `Store.Transaction` and asserts the
  authority is invalid **and** the error is non-nil.

---

## 6. `ctx-value-under-the-log-mutex` — high — implementation

**Findings:** `S3/GAP-13` — `event/eventmemory/append.go:29-32`, `event/eventmemory/read.go:21-27`,
`event/eventmemory/transaction.go:63-75`; `S3/GAP-14` — `event/store.go:111-122`, `:87-102`;
`S3/GAP-7` — `event/eventmemory/transaction.go:120-126`

**(a) A caller's `ctx.Value` runs inside the log-wide mutex.** `Append` and `ReadStream` take
`this.log.mutex` and *then* call `this.ambient(ctx)`, which begins with `ctx.Value(...)` and may make
a second one. `context.Context` is an interface the **caller** implements, so this is foreign code
under the one lock every reader and writer of the log contends for. `ReadAll` resolves it outside
the lock, so one concept has two resolution points. Reproduced in `/tmp/vvprobe`:

```
an unrelated ReadStream on a DIFFERENT stream waited 600.9ms
while the appending caller's ctx.Value ran
```

The head-of-line block is the ordinary harm. The hard one is AB-BA: a caller whose context decorator
takes a lock in `Value`, and any goroutine holding that lock across a `store.Append`, hangs the
whole log permanently. Only the `finished` check and the staging need to be inside the section, and
the plan's decision table is telling `eventpg` to copy "resolve it inside the section that acts".

**(b) `Log.ReadAll` inside a transaction, and `Envelope.Position` on a staged envelope, are
unspecified.** `event/store.go`'s `Log.ReadAll` says nothing about a bound transaction while
`ReadStream` states read-your-own-writes explicitly. `eventmemory` returns no staged envelope to
anyone and answers `Position` 0 for staged records; a SQL store issuing `SELECT … ORDER BY position`
**on the transaction it joined** returns its own uncommitted rows by construction, with real
sequence positions. Both facts are consumer-visible through `Reader.Events()`. A drain that a caller
happens to run inside its own write transaction reads **nothing** on `eventmemory` and **its own
uncommitted, possibly rolled-back events** on a SQL store — with positions it may checkpoint past.

**(c) An abandoned `*Tx` bricks its streams for the life of the process.** `release()` runs only
from `Commit` and `Rollback`, so a transaction that is dropped leaves its entry in `log.claims`
forever. Reproduced, with a control:

```
autocommit append to the same stream after the transaction was abandoned:
                                              event: the store reported [outcome conflict]
a second transaction's append to the same stream:
                                              event: the store reported [outcome conflict]
control, an unclaimed stream:                 <nil>
```

**Close criteria.**
- The `ctx.Value` lookup happens **before** `this.log.mutex.Lock()` at every door; only the
  `finished` check and the staging stay inside. `ReadAll` resolves it the same way as the other two.
  A test with a context whose `Value` blocks asserts an unrelated operation on the same log
  completes while it is blocked, and
  `TestOneTransactionUsedFromManyGoroutinesRefusesRatherThanCrashes` still goes red when the
  `finished` check is hoisted back out.
- `event/store.go` states whether `Log.ReadAll` inside a bound transaction may return that
  transaction's uncommitted events, or states that it is unspecified and that no kernel path runs a
  global read inside a write transaction; and states what `Envelope.Position` means on an
  uncommitted envelope. The plan's decision-table rows are marked as *this store's* answer.
- `eventmemory`'s doc states what an abandoned `*Tx` costs, or the claim is released on a finaliser
  / a `Log`-level sweep, or the suite carries the case. Silence is the one option that leaves
  `eventpg` free to copy the behaviour into a connection pool.

**Closed 2026-09-08** — see `EVENTSOURCE_P1_S3_GAPS.md` § *Round 3 — remediation*. `ambient` is
the `ctx.Value` lookup and runs before `log.mutex` at all three doors; the new `Tx.live` is the
liveness half and is the only thing inside the section. `event/store.go` states that `Log.ReadAll`
inside a bound transaction and `Envelope.Position` before a commit are both **unspecified**, which
keeps `eventpg` free to answer either way. `log.claims` holds a `weak.Pointer[Tx]`, so a
transaction nobody can commit stops claiming its streams once the runtime collects it. Seven
mutations applied, seven killed, every file restored byte-identically.

---

## 7. `kernel-enum-and-seam-contract-unpinned` — high — test

**Findings:** `S1T/GAP-T15` — `event/outcome.go:5-13`, `event/store.go:13-17`;
`S1T/GAP-T16` — `event/store_test.go:241-257`; `S1T/GAP-T17` — `event/outcome.go:34-43`

Three mutations, whole `./event/...` (and `./scripts/` for the second), each survived:

| Mutation | Suite |
|---|---|
| `Outcome` renumbered so `Conflict` is 0 and `Unclassified` 1 | **SURVIVED** |
| `Support` renumbered so `Supported` is 0 and `Unstated` 2 | **SURVIVED** |
| an optional `Snapshotter` interface added to `event/store.go` | **SURVIVED** (`./event/...` *and* `./scripts/`) |
| `func (this *failure) Unwrap() error { return this.cause }` added | **SURVIVED** |

Every assertion in the suite is written in terms of the identifiers, so it moves with them. Under
the first, a `failure` whose `outcome` field was never set reads as **`Conflict`** rather than the
door's fail-safe default — at an append door that turns "we do not know whether the write landed"
into "it certainly did not, re-decide and append again", the silent double write §INV-045 exists to
close. Under the second, `Capabilities{}` reads as **every capability supported**, so a store that
never said it has transactions is admitted into the transactional path — §INV-043 fails open.

The third is the microkernel law: §INV-030's *Falsified by* is literally "a method inventory showing
**no optional interface** in `event`", the inventory exists at `store_test.go:242` as `declared`,
and it is used only for the forbidden-verb check. An optional interface in `event/` readmits the
capability-discovery tunnel D-115 was corrected to close: the moment the kernel writes
`if t, ok := store.(Transactional)`, a policing decorator that does not forward it is walked past.

**Close criteria.**
- A test asserts `var zero Outcome; zero == Unclassified`, and through the door that
  `refuseAppend(&failure{cause: err})` is `ErrUncertain` while `refuseRead` of the same is
  `ErrBackend`. A test asserts `var zero Support; zero == Unstated` and that `Capabilities{}`
  answers `Unstated` on all four fields. Both messages name the fail-open consequence.
- The exported-interface subtest asserts the set is exactly `{Store, Log}`, with a message naming
  the tunnel it prevents; the forbidden-verb check stays as the weaker second net.
- A subtest asserts `Failure(<each outcome>, cause)` answers `errors.Is(·, cause)` false and
  `errs.AsFault(·)` false, with the refusal built from it as the control that the cause is still
  reachable through `CauseOf`.

---

## 8. `reader-cursor-advances-past-a-refusal` — high — test

**Findings:** `S4T/GAP-9` — `event/reader.go:52-56`, `event/reader_test.go:68-96`

Hoisting the cursor assignment above the page check leaves `./event/...` **green**:

```go
this.cursor = cursor                       // mutation
if err := this.checkPage(page); err != nil {
    return false, err
}
this.events = page
```

Both page-defect rows of `TestAConsumerReadsThroughPagesTheStorePublished` assert only that `Next`
answers `ErrBackend`; neither reads `reader.Cursor()` afterwards and neither calls `Next` again once
the defect is cleared. With the four-event stream the test already builds and the defect cleared
after the refusal, the shipped code delivers 4 of 4 events and the mutant delivers 1 of 4 — three
committed events silently skipped, with no error after the one already logged. That is the sentence
`Reader.checkPage`'s own comment claims to prevent, §UC-039's *no event may be skipped at a page
boundary*, and §INV-036's tiling claim. No later section can close it: `eventtest.Factory` has an
`Outcome` hook and no page-defect hook, so a conforming store never reaches this branch.

**Close criteria.**
- The test captures `reader.Cursor()` before each defect row and asserts it is unchanged after the
  refusal.
- One row clears the defect after a refused `Next` and asserts the reader goes on to deliver every
  event of the stream exactly once.
- The hoist turns that case, and only that case, red.

---

## 9. `scripts-checks-report-through-unfalsified-bodies` — high — test

**Findings:** `S6T/GAP-T9` — `scripts/extensions_test.go:147-179`, `:93-109`;
`S6T/GAP-T10` — `scripts/extensionlisting_test.go:138-141`, `:57-64`;
`scripts/extensionwalk_test.go` is where the falsification belongs

`scripts/extensionwalk_test.go` drives the *arms* (`startsBeforeMain`, `startsAGoroutine`,
`crossingsInto`, `uncoveredDirectories`) and never drives the bodies that turn their answers into a
failure. Four mutations, whole `go test -count=1 ./scripts/` after each:

| Mutation | Suite |
|---|---|
| `startsNothing`'s `startsAGoroutine` report loop → `_ = startsAGoroutine` | **SURVIVED** |
| `startsNothing`'s `startsBeforeMain` report loop → `_ = startsBeforeMain` | **SURVIVED** |
| `noBaseSubsystemDependsOn`'s `crossingsInto` report loop → `_ = crossingsInto` | **SURVIVED** |
| `packagesUnder`'s `uncoveredDirectories` report loop → `_ = uncoveredDirectories` | **SURVIVED** |

D-116's optionality and §INV-013's "importing a package must not do anything" are held by these
functions and by nothing else — `make check-deps` cannot see a first-party edge and `make check` has
no arm for either. With the loops disconnected, a real `func init()` and a real `go func(){}()` in
`event/eventmemory/log.go`, and a real `_ "github.com/frostgrove/vv/event"` in `cache/address.go`,
all ship green. `uncoveredDirectories` is the floor that makes every `scripts/` check see a nested
module, and `event/eventpg` **is** a nested module: with the floor disconnected the whole store — its
cost, its `init`, its environment reads and its goroutines — is invisible to every check `scripts/`
holds. `startsNothing`'s only guard on the linked set is `if linked == 0`, which one package
satisfies, so any incidental narrowing of `underTest` drops `event/eventmemory` and `event/eventtest`
out of the goroutine arm silently.

**Close criteria.**
- `startsNothing`, `noBaseSubsystemDependsOn` and `packagesUnder` split reporting from walking the
  way `costOverruns` already did: each returns its complaints and takes the tree it walks as a
  parameter; the extension files call `t.Error` over the result.
- `scripts/extensionwalk_test.go` drives each over a written module tree — one production package
  with an `init`, one with a goroutine, one importing `testing` that must stay silent, and a nested
  module holding source — asserting the complaint set and the walked package set **by name**.
- `if linked == 0` is replaced by a by-name comparison, the way
  `TestTheChecksWalkEveryPackageTheExtensionHoldsAndLinkAllButTheSuite` does on the `event/` side.

---

## 10. `event-source-analyser-arms-undefended` — high — test

**Findings:** `S6T/GAP-T8` — `event/renderedtypes_test.go:26-38`, `:76-90`, `:93-116`, `:121-136`;
`event/mutablestate_test.go:157-190`; the fixtures at `event/fixtures_test.go`

Nine arm deletions, one at a time, `go test -count=1 ./event/` after each — **all nine survived**:

```
identityInside's *types.Map          SURVIVED     writtenThrough's *types.Slice   SURVIVED
identityInside's *types.Chan         SURVIVED     writtenThrough's *types.Chan    SURVIVED
identityInside's *types.Array        SURVIVED     writtenThrough's *types.Array   SURVIVED
identityNamed's pointer unwrap       SURVIVED
carriedIdentity's pointer loop       SURVIVED
identityOfAPart's pointer unwrap     SURVIVED
```

Both analysers are tables over the Go type kinds and both fixtures cover only the kinds the kernel
happens to use today. Each file's own prose is ahead of its fixture:
`renderedtypes_test.go:10-13` says "a map keyed by one … a pointer to any of those";
`mutablestate_test.go:11-18` says "a package-level map, slice, channel or pointer". The deletions
are not decorative — the reviewer demonstrated each letting a real violation through in the shipped
tree, and phase 2's `eventpg` is where all three §INV-011 shapes arrive at once (a refusal built
over `*event.AppendRequest`, a `map[Key]…` of staged rows) alongside the §INV-013 one (a
package-level slice of prepared statements written through a helper's parameter).

**Close criteria.**
- `renderingFixture` gains a case per undefended arm, each asserted **reported**: a refusal
  rendering a `*Envelope`, a `map[Key]int`, a `map[string]Cursor`, a `[4]Version`, a
  `chan Position`.
- `mutableFixture` gains a package-level slice, channel and array of a mutable element, each written
  **only through a helper's parameter** so `mutationsOf` cannot answer for the kind arm, plus a
  silent control.
- Deleting any one arm turns the matching fixture subtest red and the message names the case.

---

## 11. `aggregate-crossing-and-log-injection` — medium — implementation

**Findings:** `S2/GAP-182` — `event/aggregate.go:70-93`, `event/change.go`;
`S1/GAP-166` — `event/identity.go:25`

**(a) A change folds through an aggregate that never declared it.** `Fold` compares the change's
*stream*, and a stream is `{Family, Key}` — so two `*Aggregate` values of one family and one state
type accept each other's changes and run a fold the receiving aggregate never declared:

```
first  := Define[ledger,string]("probe.cross", …); Declare(first,  "credited", …, +len(Items))
second := Define[ledger,string]("probe.cross", …); Declare(second, "other",    …, -1000)
second.Fold("k", ledger{}, first.credited.New("k", Batch{Items: {1,2,3}}))
    → state={3}  err=<nil>          ← first's fold ran on second's aggregate
```

§INV-039 admits two aggregates over one family through two `*Binding` values deliberately; nothing
says the *changes* cross too.

**(b) A store-supplied family forges a second bracketed field in a log line.** GAP-150 closed the
newline route and not the bracket one:

```
Stream{Family: "orders\nFAKE"}                          → "[stream unnameable]"      (closed)
Stream{Family: "orders] admin logged in [stream x"}     → "[stream orders] admin logged in [stream x]"
Stream{Family: "orders"}                                → "[stream orders]"          (control)
```

`Stream.String()` is the one rendering §INV-025 names, and an `Envelope`'s family is a store's data.
Phase 5 deliberately wrote no test for this, because asserting today's answer would encode a known
defect as expected.

**Close criteria.**
- `Change` carries the aggregate it was minted on (a pointer identity, not a name) and `Fold`
  refuses one minted elsewhere, or the contract states that a family is the identity and two
  aggregates over one family are one aggregate.
- `Stream.String()` chooses `%q`, or a wider rule that no store-supplied text can close the bracket
  it opened; `TestEveryRenderingNamesAClassAndNeverAValue` gains the bracket row beside the newline,
  over-cap, invalid-UTF-8, NUL and empty ones it already carries.

**Status: closed, 2026-09-08.** (a) `Change` carries the `Declaration` it was decided on — an
unexported field set by `Fact.New` — and `decidedFor(stream, on)` compares it beside the stream at
both doors, `Aggregate.Fold` and `Repo.Append`, with `ErrFamily`, which is `Bind`'s own answer to
one family and two aggregates. (b) `event/text.go:checkFamily` is the kernel text rule plus `[` and
`]`, and it runs at `TryDefine` and at `Stream.String`; `%q` was refused because it annotates the
closing bracket rather than removing it. Dispositions, the before/after measurements and the
mutation evidence are in `EVENTSOURCE_P1_S2_GAPS.md` and `EVENTSOURCE_P1_S1_GAPS.md`.

**Round 5's re-audit reopened (b) on the other input, and it is closed too, 2026-09-08.**
`S1/GAP-201`: the rule had been applied to the family and not to the *wire type name*, the other
string `MaxNameBytes` governs and the other one the kernel renders into a line holding a bracketed
field — `Envelope{Type: "accounts.credited] admin logged in [stream x"}` rendered
`… no declared fact: "accounts.credited] admin logged in [stream x" on [stream accounts.account]`,
two open brackets and two close. `checkFamily` is now `checkName` and governs both names at all
four doors they cross — `TryDefine` and `Stream.String` for the family, `TryDeclare` and
`Repo.apply` for the type name — and the frame it refuses is `event/text.go`'s own `fieldOpen` /
`fieldClose`, which is what `Stream.String` writes. `S2/GAP-202`, the comment rewrap round 11
raised against the (a) repair, is closed in the same pass.

---

## 12. `deferred-residue` — medium — test and prose

These reproduce and are each one assertion or one sentence. Grouped because they are one sitting and
none of them blocks phase 2 on its own.

| Finding | Where | Verified |
|---|---|---|
| `S1T/GAP-T18` | `event/errors.go:167-169`, `event/refusal_test.go:389` | swapping the count and the bound in `tooLarge`'s message **SURVIVED** — the assertion is two order-blind `Contains` calls, so a 413 can tell a caller its 2 MiB payload "is 1048576 bytes against a bound of 2097152" |
| `S2T/GAP-T40` | `event/seal.go:33-39`, `:41-52` | removing `seal()`'s mutex **SURVIVED** under `-race`; removing `declare()`'s lock **SURVIVED** under `-race` |
| `S1T/GAP-T19` | `event/backing.go:44-50`, `event/backing_test.go:103-134` | the six nil routes assert only that *something* refused; three of them are proved by the comparability rule, since a nil slice, map and func are uncomparable and refused either way |
| `S6T/GAP-T11` | `event/refusalmessages_test.go:239-273` | the `derived` skip can be deleted with `./event/` green; no fixture case is silent *because* it is derived |
| `S1/GAP-169` | `event/store.go:83-102` | the `Envelope` trust-boundary comment claims a store is "trusted … not at all" while `RecordedAt` is checked by nothing and is read by the caller |
| `S3/GAP-15` | `event/eventmemory/transaction.go:12` | `errNotTransaction`'s text says "carries no transaction of this store's for this store" on the branch that means *something is bound and it is not a transaction* — it describes the answer it is not giving, and says "for this store" twice |
| `S3/GAP-16` | `event/eventmemory/transaction.go:81-93`, `:109-118` | `Commit` has two errors; `errStaleClaim` leaves the transaction live and still claiming |
| `S3/GAP-19` | `event/eventmemory/append.go:26` | `recorded := this.clock()` runs before the empty-records short circuit, so the injected clock is read on appends that record nothing |
| `S3/GAP-8`, `S3/GAP-9` | `event/eventmemory/append.go:34-36`, `event/eventmemory/read.go:31-33` | an empty `Append` returns `nil` without checking `Expected`; `ReadStream` turns an out-of-range `after` into "the end of the stream". Both are the store's answer to a question the kernel does not settle, and `eventpg` will answer them differently |
| `S5T/GAP-19`, `S6/GAP-19…23`, `S1/GAP-164`, `S1/GAP-168` | the plan, `[[FL-036]]`, `event/` comment ratio | prose, metrics and citation drift already carried in the plan's `## Debt` |

---

## Already closed — verified against the code, not the note

| Finding | Evidence it is closed |
|---|---|
| `S2/GAP-185` promoted marshaller | `promotedMarshaller`, now at `event/routing.go`. `struct{ Money; SKU string }` → `CanEncode` refuses, naming `SKU`. The `json:"-"` spelling (GAP-T36), the text route (GAP-T41), the embedded pointer (GAP-T42) and the promotion **chain** (GAP-T46) are all closed — cluster 1, four rounds. |
| `S2/GAP-186` unmarshaller on the value receiver | `readsAs` (`encodable.go:293-298`) excludes a value-receiver implementation; `onTheValueReceiver` gives the refusal; `objectKey` does the same for map keys |
| `S2/GAP-187` embedded pointer to an unexported struct | `members.collect`'s `blocked`/`unwritable` rule at `encodable.go:183-202`, raised where a rendered name is reached rather than where the pointer is seen |
| `S1/GAP-162` `findAs` answers `(nil, true)` | `if nilByAnyRoute(candidate) { return false }` at `event/errors.go:352-354`; `TestANilOrLyingCauseNeverPanicsTheKernel` in `event/walk_test.go` |
| `S1/GAP-163` partition gate reads a hand-maintained copy | `TestTheRefusalVocabularyIsAPartition` in `event/refusal_test.go` |
| `S1T/GAP-T13` class wrap asserted only on the bare sentinel | M-A1 (`matches` → `sameError` on `refusal.Is`'s sentinel arm) is now **caught** by `TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot` (`event/status_test.go`) |
| `S1T/GAP-T14` per-method seam signature unpinned | closed by compilation rather than by assertion: `Close() error → Close()` no longer builds — `event/eventtest/defects.go:79`, `defects_lifecycle.go:54`, `sections_lifecycle.go:70,73`. Every one of the reviewer's five rewrites now breaks an in-tree package. The reflect allow-list is still the only *assertion*, which is worth a note in the seam test but is no longer a hole |
| `S2T/GAP-T38` `a.declare(name, nil)` | now caught — `TestAStreamWithHistoryFoldsToItsCurrentState`, `TestTheMemoryStoreSatisfiesTheContract`, `TestTheSuiteStillDetectsEveryDefectItWasBuiltToDetect` |
| `S2T/GAP-T39` the three codec-graph bounds | raising any one of `codecGraphEdges`, `codecGraphDepth` or `codecGraphNodes` out of reach is caught by `TestADeclaration` |
| `S3T/GAP-T3` `stagedFor` crosses streams | caught by `TestATransactionThatStagesToTwoStreamsNeverCrossesThem` |
| `S3/GAP-1` a second log's transaction shadows this store's | the binding is keyed by `*Log` (`transaction.go:29-36`). **The test half is still open — cluster 5.** |
| `S4/GAP-1`, `S4/GAP-2` | closed in phase 5; `TestRepoAuthorityIsTheComparisonTwoSubsystemsUse` exists in `event/transaction_test.go` and drives §UC-049's crossed context through both doors |
| `S5/GAP-24, 25, 26, 28, 29` | closed in phase 5; `TestAStoreThatMintsTheRecordedInstantWhenAnEventIsReadIsNotCertified` and `TestAFactoryWhoseLaterStoresPublishSomethingElseIsRefused` exist in `event/eventtest/suite_test.go` |
| `S6/GAP-13, 14, 15, 16` | closed in phase 5 per the round-2 dispositions; `scripts/` is green and the shared walks are driven by `scripts/extensionwalk_test.go`. **The report bodies they call are still unfalsified — cluster 9.** |
| Carry-into-the-plan 5 (GAP-104) | a load of an oversized **stored** payload answers `ErrPayload` (history class), not `ErrTooLarge` (request class) — `event/repo.go:265-267` |
| Carry-into-the-plan 1, 4 (GAP-100, GAP-103) | the mechanisms exist (`Fact.RoundTrip`, `Repo.checkPage`, `Reader.checkPage`, the `bounds` section); their **tests** are inert — clusters 3 and 4 |

---

## Dispositions

### 1. `encodable-tagged-promotion` — **closed 2026-09-08**

`promotedMarshaller` no longer reads the embedded field's JSON tag: the skip is `if !field.Anonymous`
alone. `besideIt` still reads it, because there a `json:"-"` field is one the author declared
unwritten rather than one the promotion hid. Reproduced first in `/tmp/vvprobe` (`CanEncode=<nil>`,
`Encode=500`, `Decode={Money:{Cents:500} SKU:}`), refused after (`… so SKU is written by nobody and
read back by nobody …`), with `Named` and `Itemised` still `<nil>`.

All three close criteria met: the skip is removed; `event/declaration_test.go` gains the refused row
`skipped`, the diagnosis row that pins both the embedded type and the hidden field, and the
accepting control `tallied` (the tagged spelling with nothing beside it, which the naive repair
breaks and `itemised`/`invoiced` do not catch); the walk's contract comment at `encodable.go:47-62`,
`promotedMarshaller`'s own comment, `#### event/codec.go`'s refusal 6 in the plan and
`docs/ai/decisions/D-124` all now say no tag undoes promotion. Reverting the fix turns two subtests
red. Full detail and the mutation transcripts are in `EVENTSOURCE_P1_S2_TEST_GAPS.md`,
*Round 2 dispositions — GAP-T36*.

### 1. `encodable-tagged-promotion` — follow-up round, **closed 2026-09-08**

Round 3's clean-context audit verified GAP-T36 itself closed and opened five findings on the same
arm. All five are now closed; the detail and the mutation transcripts are in
`EVENTSOURCE_P1_S2_TEST_GAPS.md`, *Round 4 dispositions — GAP-T41 … GAP-T45*.

| Finding | Disposition |
|---|---|
| `S2T/GAP-T41` `[high]` — the text-route arm of `promotedMarshaller` had no row and its mutant survived the whole suite | **closed by three fixtures and four rows.** No production change: the arm was route-generic already and refused the shape correctly; what was missing was the gate. `termed` (`struct{ settled; SKU string }`) refused with a diagnosis row naming `MarshalText` and `SKU`, `netted` and `agreed` as accepting controls, both verified to round-trip out of tree. `if writes == byTextMethods { return nil }` now turns two subtests red; it survived before. GAP-185's close criterion 2 shape (c) is recorded as outstanding-until-now in `EVENTSOURCE_P1_S2_GAPS.md`. |
| `S2T/GAP-T42` `[medium]` — an embedded pointer to a marshalling type was refused by the receiver arm, naming a repair the developer had already applied | **closed by asking the promotion question first.** `ownMethods` now asks `promotedMarshaller` of every struct that writes itself through methods, before the receiver arm; `promotedMarshaller` gained an arm for the embedded pointer and refuses it with nothing beside it too, because the read dereferences nil and panics (measured on both routes and both receiver spellings). `charged` and `owed` refused, three diagnosis rows, four mutations red. |
| `S2T/GAP-T43` `[medium][deferred]` — `D-124` never stated the deliberate false positive | **closed, not deferred.** `D-124` gains **The one deliberate false positive**, and the refused list is now **nine** shapes: the embedded-pointer refusal is a different failure from the promotion one and is its own bullet. Count and prose updated in `D-124`, `event/encodable.go`, `FL-036` and the plan in the same change. |
| `S2T/GAP-T44` `[low][deferred]` — the refusal never mentioned the tag the developer wrote | **closed, not deferred.** `noTagUndoesIt` appends *"the json:%q you put on it does not undo that, because a tag speaks to encoding/json's field walk and method promotion is a language rule"*. The control is the untagged row beside it, which asserts a substring that exists only when there is no clause — so both dropping the clause and appending it unconditionally turn a row red. |
| `S2T/GAP-T45` `[low][deferred]` — `D-124`'s **Proven by** did not name the gate on its refused shapes | **closed, not deferred.** It now names `TestADeclaration` and its two subtests, and says which accepting rows are the controls. |

`event/eventpg`'s zero-diff obligation is intact: the two new functions are unexported, no exported
declaration changed, and nothing a store constructs moved.

### 1. `encodable-tagged-promotion` — second follow-up round, **closed 2026-09-08**

Round 5's clean-context re-audit verified GAP-T36, GAP-T41 … GAP-T45 closed and opened four more on
the same arm — including the one this cluster's brief got wrong twice: the `json:"-"` skip was
**not** the last surviving arm of the family. All four are closed; the detail, the reproduction and
the mutation transcripts are in `EVENTSOURCE_P1_S2_TEST_GAPS.md`, *Round 6 dispositions — GAP-T46 …
GAP-T49*.

| Finding | Disposition |
|---|---|
| `S2T/GAP-T46` `[critical]` — the promotion question was asked **one hop deep**, so `struct{ B }` where `B` is `struct{ money; Extra string }` was accepted, wrote itself as `500` and read `Extra` back as `""` while `B` alone was refused | **closed by asking it along the whole promotion chain.** `promotedMarshaller` carries the path it was reached through and recurses into an embedded struct held by value where nothing of the outer struct's own sits beside it; the embedded-pointer arm returns where it is found and a non-struct promotes no fields, so the recursion is finite without a visited set — a Go struct cannot contain itself by value. The refusal names the hop: *the `event.money` embedded in `lined`, so `lined.SKU` is written by nobody*. Five refused rows (`stacked`, `layered`, `filed`, `staged`, `owing`), the accepting control **`folded`** (two embeddings, nothing hidden at either level), six diagnosis rows. Four mutations red in both directions — reverting the recursion, over-refusing any nested embed, dropping the chain from the type name, dropping it from the field path. Reproduced first as a scratch test printing `<nil>` for all eight of the reviewer's spellings, and proved out of tree on `type Audit struct{ time.Time; Actor string }` / `type Opened struct{ Audit }`. |
| `S2T/GAP-T47` `[high]` — `event/encodable.go` was 422 lines against the 400-line budget and the plan claimed compliance | **closed by the split the plan itself predicted.** `event/encodable.go` is **212** — the graph walk, its budgets and the rendered-name collection; `event/routing.go` is **256** — `encoding/json`'s routing rules restated as refusals. No test edited, no exported name moved (`make api` regenerates `docs/api/surface.md` with no diff), and the seven mutations round 5 verified against the unsplit file are still killed against the split one. The plan's `:3334` and `:5262` metrics now carry the real numbers, `FL-036` and `docs/ai/flows/Index.md` name both files, and `D-124` says where each half lives. |
| `S2T/GAP-T48` `[low][deferred]` — the promotion refusal offered a remedy that leads to a second refusal | **closed, not deferred.** Where the embedded type declares no reader on the route it writes by — or declares one on the value receiver, where `encoding/json` calls it on a copy — the refusal says so and names that hop as the repair. Two refused rows and two diagnosis rows (`billed` over `cents`, `mislaid` over `sloppy`), with `lined`'s existing row as the control that the ordinary case still says the ordinary thing. |
| `S2T/GAP-T49` `[low][deferred]` — a fixture comment narrated the review round | **closed, not deferred.** The comment above `termed` is two sentences about what `encoding/json` does; the clause naming *"the last finding"* is gone. |

`event/eventpg`'s zero-diff obligation is intact: two new unexported functions, one new parameter on
an unexported one, a file split inside one package, and `docs/api/surface.md` regenerates unchanged.

### 2. `roundtrip-fidelity-broken` — third round, **closed 2026-09-08**

Round 6's clean-context re-audit found GAP-188(1), GAP-188(2), GAP-188(4), GAP-189 and GAP-190 fully
closed and opened four more findings on the same position — the omission the narrowing left behind,
and two raw runtime panics on a route the closure never covered. All four are closed; the
reproductions, the after-transcripts and the nine-mutation campaign are in
`EVENTSOURCE_P1_S2_GAPS.md`, *Round 7 — remediation of the round-6 cluster*.

| Finding | Disposition |
|---|---|
| `S2/GAP-191` `[high][immediate]` — a struct with no exported field and no `Equal(T) bool` was compared by nothing and reported as a pass, so the narrowing that closed GAP-188 left the check optional by omission | **closed by giving that position three answers in a fixed order** rather than one. `sameOpaque`: a type of size zero holds one value and is excluded; then the type's own `Equal`; then `==`, which is what `netip.Addr`, `netip.Prefix` and every comparable value object has instead; then **none**, recorded on the walk as `incomparable` and carried out as `ErrSample` naming the type and both repairs. The finding's headline case — a codec that drops a `netip.Addr` — is now `ErrPayload`, a real difference, because the `==` arm can compare it; `hushed` and `heldTight`, which nothing can compare, are `ErrSample`. Five docs say so: `event/fact.go`, `docs/modules/{en,ru}/event.md`, `docs/modules/{en,ru}/eventtest.md`, `[[D-124]]` and plan clause 6d. |
| `S2/GAP-192` `[high][immediate]` — a payload reached through an interface panicked `reflect` out of `Fact.RoundTrip` | **closed by comparing `reflect.Type` rather than `reflect.Kind` in both walks.** Two valid values whose types differ are answered — a difference for fidelity, not-shared for aliasing — and never indexed or keyed by each other's shape. The two `shares` routes the finding named but did not measure panic too, and are pinned by rows of their own. |
| `S2/GAP-193` `[medium][immediate]` — two valid values of different kinds were reported as the same value | **closed with GAP-192, one repair, as its own timing argument asked.** The numeric widening that arm used to swallow is `sameNumber`, a named arm that compares the numbers: `1` read back as `float64(1)` passes, as `float64(2)` does not. |
| `S2/GAP-194` `[medium][deferred]` — two helpers took five parameters against `architecture.md`'s ceiling of four | **closed, not deferred.** `roundTripping{read, written, zero, revision}` carries the revision under test and the two encodings its round trip produced; `notAliased` and `reusesItsBuffer` are at three parameters each. |

`event/eventpg`'s zero-diff obligation is intact: every symbol added is unexported — `sameOpaque`,
`sameNumber`, `asFloat`, `roundTripping`, `valueWalk.incomparable` — no exported signature moved,
and `docs/api/surface.md` regenerates unchanged.

### 3. `roundtrip-fidelity-broken` — fourth round, **closed 2026-09-08**

Round 8's clean-context re-audit found GAP-188, GAP-189, GAP-190, GAP-191, GAP-192, GAP-193 and
GAP-194 fully closed against nine mutations, and opened three findings on the consequence round 7's
own repair had: the position that now refuses where nothing can compare refuses types nobody can
repair. All three are closed — two by code, one rejected with a measurement. The reproductions, the
after-transcripts and the eleven-mutation campaign are in `EVENTSOURCE_P1_S2_GAPS.md`,
*Round 9 — remediation of the round-8 cluster*.

| Finding | Disposition |
|---|---|
| `S2/GAP-195` `[high][immediate]` — the GAP-191 repair made a payload carrying a `math/big` value permanently unprovable: a correct declaration over the kernel's own codec, round-tripping perfectly, refused for ever with a repair the consumer cannot perform | **closed by asking the wire where the value walk has nothing to compare**, which is the mechanism the finding proposed. `valueWalk.opaque` replaces `incomparable` and is carried out to `Fact.readBack`, which calls `readsBackOnTheWire`: what the codec returned is encoded again with the revision's own codec and the bytes are compared with the ones the sample encoded to. That needs no method and no name, so it answers for `big.Int`, `big.Rat` and `big.Float` alike. It is weaker than the walk — a codec that drops the same thing on both sides re-encodes to the bytes it was given — so it runs **behind** it and only where the walk had nothing, which is where `incomparable` already sat. Measured: the faithful `*big.Int` payload passes through the shipped codec and through one of the caller's own, and a codec that reads the amount back as nothing is `ErrPayload` naming `big.Int`. `hushed` and `heldTight` moved from `ErrSample` to `ErrPayload` with it. Six docs say so: `event/fact.go`, `event/comparison.go`, `docs/modules/{en,ru}/event.md`, `docs/modules/{en,ru}/eventtest.md`, `[[UC-032]]`, `[[D-124]]` and plan clause **6f**. |
| `S2/GAP-196` `[medium][immediate]` — `same`'s default arm answered agreement for the one kind it cannot compare, which is the fourth unanswered route and the one `[[D-124]]`'s forbid list already named | **closed by recording the kind rather than agreeing with it.** `!first.Comparable() \|\| first.Equal(second)` became an explicit two-arm answer: comparable is compared, and a `func` — the only kind that arm reaches and Go declines to compare, and one no wire format records — is recorded as `unrecordable` and carried out as `ErrSample`. The repair it names exists and is the modelling the domain wanted: record what identifies the behaviour, choose the function from it when you fold. A `chan` is comparable, takes the same arm and stays `ErrPayload`, which is the control that the answer is a decision rather than a blanket refusal. |
| `S2/GAP-197` `[low][deferred]` — at the one position `Equal` is still asked, a lenient application `Equal` still decides the verdict | **rejected — `wontfix`, on a measurement rather than a citation.** The only repair on offer is a second answer beside a declared `Equal`, and it costs a false accusation: a codec that records an instant and hands it back in UTC is faithful by `time.Time.Equal` and re-encodes to different bytes, so the repair calls a correct codec broken. That is the failure `Equal` is asked at that position to prevent. Written into `[[D-124]]`'s forbid list (*"Do not ask the wire beside an answer the walk already has"*) and into plan clause 6a, which gained the module page's hedge. Held by a test rather than by the sentence: the mutation that applies the rejected repair turns *what an encoding drops by design is not what it lost* red. |

`event/eventpg`'s zero-diff obligation is intact: every symbol added is unexported —
`readsBackOnTheWire`, `valueWalk.opaque`, `valueWalk.unrecordable` — no exported signature moved,
and `make api` regenerates `docs/api/surface.md` byte-identical.

### 6. `ctx-value-under-the-log-mutex` — second round, **closed 2026-09-08**

Round 4's clean-context re-audit found GAP-13, GAP-14 and GAP-7 closed against six mutations and
opened five findings on what round 3's own repairs left: a hole in the reclamation mechanism, and
four consequences of the way it and the GAP-13 split were written. All five are closed. The
reproduction, the before-and-after transcripts and the four-mutation campaign are in
`EVENTSOURCE_P1_S3_GAPS.md`, *Round 5 — remediation of round 4*.

| Finding | Disposition |
|---|---|
| `S3/GAP-20` `[medium][immediate]` — the weak claim is defeated by the receipt the kernel itself hands back: an `event.Authority` names the store's `*Tx`, travels in every non-empty `Commit`, and a caller is told to keep it, so a retained receipt pins an abandoned transaction's claims for the life of the process | **closed by taking the `*Tx` out of the authority.** `Tx` carries a `txIdentity{log, nth}` minted under the log's mutex at `Begin`, and `Store.Transaction` hands that to `event.NewAuthority`: comparable, stable, monotone per log, never reused, and a route to nothing. Reproduced out of tree through `Repo.Append`'s own receipt — *conflict* with the receipt held, `<nil>` with it dropped, *conflict* with the `*Tx` held — and answered `<nil>`, `<nil>`, *conflict* after. What reachability still costs is then stated with its condition: `doc.go`, `docs/modules/{en,ru}/eventmemory.md` and the plan's **Degradation** paragraph name the `*Tx` and a context outliving the request as the two things that still claim, and correct round 3's staged-payload claim |
| `S3/GAP-21` `[medium][deferred]` — the `ReadAll`-inside-a-transaction rule was written on the interface a store implements, not on the value a consumer holds, and the kernel's sentence about itself read as a guarantee it does not give | **closed on `Reader.Cursor`**, the value the consumer calls, plus both `docs/modules/*/event.md`. `event/store.go` now separates *no path the kernel initiates opens a transaction and then reads globally* from *`Reader.Next` runs this read on whatever context it is given and refuses none*. Refusing at the call was rejected with the reason in the plan: a `Reader` holds a `Log`, which has no `Transaction` method, and `ReadOnly` has no `Next` to walk past |
| `S3/GAP-22` `[low][deferred]` — the GAP-13 fix moved cursor parsing *into* the section it was opened to shrink | **closed by moving it back out**, with the *report* left below the liveness check because `Refused` outranks `BadCursor` — and that precedence is now pinned, which it was not: the test round 4 cited passed a cursor that parses. It now passes a foreign one at every refusing door, with the live-transaction control that the same cursor is `BadCursor` there |
| `S3/GAP-23` `[low][deferred]` — `ambient`'s comment explained a rule `Tx.live` implements | **closed by moving the sentence to `Tx.live`.** `ambient`'s comment says only what `ambient` does |
| `S3/GAP-24` `[low][deferred]` — `claimedByAnother` is spelled as a question and mutates the map | **closed by naming the command:** `releaseDeadClaim(stream) *Tx` performs the drop the name promises and answers the claimant that survived it. The drop stayed in `Log` because moving it to `Append` would take that function past the 50-line ceiling it sits two lines under |

`event/eventpg`'s zero-diff obligation is intact: `txIdentity`, `nameTransaction` and
`releaseDeadClaim` are unexported and `Tx.identity` is an unexported field, the two kernel edits
are comments, and `make api` regenerates `eventmemory` at the same eight package-level symbols.
