# Event sourcing — deferred findings backlog

**Policy, set 2026-09-08.** Only `[critical]` and `[high]` findings block a gate. Everything else is
recorded here and left alone. Core mechanics of all five roadmap phases come first; this backlog is
worked afterwards. A finding here is not forgiven — it is scheduled.

Nothing in this file is a defect in the *design*. Every one is a place where a test does not pin
what it claims, or a residual the reviewer named and the fix round did not reach.

## P1 — closed before the policy changed

These were fixed and re-audited green, and are listed so nobody re-opens them:

| Cluster | Severity | What it was |
|---|---|---|
| `encodable-tagged-promotion` | critical | A `MarshalJSON`/`UnmarshalJSON` pair promoted from an embedded type passed `CanEncode` with nil and silently deleted every field declared beside the embedded one. Three successive rounds: the `json:"-"` spelling, then promotion asked only one hop deep, then `objectKey` never asking the promotion question at all. |
| `roundtrip-fidelity-broken` | critical | The fidelity walk could not prove a `math/big` payload, and a struct with no exported field and no `Equal` was compared by nothing while `RoundTrip` reported a pass. |
| `ctx-value-under-the-log-mutex` | high | The caller's `ctx.Value` ran inside the log-wide mutex, so a context whose `Value` blocks stalled reads of unrelated streams. Also: the kernel published a strong reference to the `*Tx` inside every non-empty commit receipt. |
| `aggregate-crossing-and-log-injection` | medium | A change could fold through an aggregate that never declared it; and a store-supplied `Envelope.Type` rendered with `%q`, which does not escape `[` or `]`, allowed log injection. |

## P1 — deferred, in the order they should be taken

### 1. The conformance suite does not detect what it claims  `[critical]`

`event/eventtest` — 138 of 165 section-level assertions can be neutralised one at a time with the
whole tree green. Measured detection: **27/165 = 16%**. Nine of twenty sections are named by no
defect row (stream identity, conservation, global paging, cancellation, lifecycle, concurrency,
shared backing, monotone visibility and one more). `Run`, the only exported function the package
ships and the only thing `eventpg` calls, has no test at all.

**Why it matters and where it is handled instead.** Phase 2's whole evidence is "`eventpg` passes
`eventtest` unchanged". A suite at 16% detection makes that evidence weak. Rather than harden the
suite in the abstract now, P2's own gate carries a mutation check: the suite must be shown to catch
defects *while exercising the real PostgreSQL store*. That is cheaper and tests the thing that
actually ships. This entry stays open until that check exists.

### 2. `eventmemory` conformance holes  `[critical]`

No transaction is ever driven through a second store value over the same log, so keying the
transaction by the `*Store` instead of the `*Log` survives — which is exactly the escape INV-041
exists to close. Every transaction in the suite claims exactly one stream, so the claim set is
untested in both directions: a two-stream commit can leave a stream permanently unwritable.

### 3. The value walk's absence arms are unpinned  `[critical]`

`event/comparison.go` — the walk's *absence* arms survive deletion: `sameEntries` comparing a map
key with itself, `same`'s invalid-value arm, and a codec that fills a decode buffer to the payload's
width and exposes it only through an unexported field passes `RoundTrip` with nil.

### 4. Kernel enum and seam contract unpinned  `[high]`

Neither enum's zero value is pinned: `Conflict Outcome = iota` survives, so an unset classification
reads as a conflict instead of the door's fail-safe default; and `Supported Support = iota`
survives, so `Capabilities{}` reads as fully supported and INV-043 fails open. The store seam's
per-method signature is pinned by nothing — six rewrites survive. Adding an optional interface to
`event` is green, which readmits the [[D-115]] capability-discovery tunnel.

### 5. Reader cursor advances past a refusal  `[high]`

### 6. `scripts/` checks report through unfalsified bodies  `[high]`

`costsNoMoreThanItNames`, `startsNothing` and `noBaseSubsystemDependsOn` report through bodies
nothing falsifies; gutting them leaves `./scripts/` green. Nothing pins which directories the
INV-011 and INV-013 source checks walk — `extensionDirectories` returning `[".", ".", "."]`
satisfies every floor.

### 7. Event-source analyser arms undefended  `[high]`

Nine type-shape arms in the identity and mutable-state analysers survive deletion.

### 8. Documentation coverage  `[high]`

Thirteen of fourteen non-test source files under `event/` have no flow row (~2700 lines), including
all six section files. `TestNoDocPromisesExactlyOnceDelivery` is English-only, so all of
`docs/modules/ru/` is unreadable to it.

### 9. Residue  `[medium]`, `[low]`

Everything in the per-section GAPS files marked `[medium]` or `[low]` and still open, plus the ten
constraints in `EVENTSOURCE_P1_USECASES_GAPS.md`'s `## Carry into the plan` list that the plan
covered by a rule rather than by a test.

## The lesson this backlog exists to record

The P1 chain ran review gates capped at three rounds and **proceeded with the last verdict red**.
`go build`, `go vet`, `gofmt` and `go test -race` were all green the whole time, which is precisely
why nobody noticed: every finding above is a thing a green suite does not see. Two rules follow, and
they are in force for P2 onward:

1. A gate that hits its round cap **stops the chain** and asks. It never proceeds red.
2. A section is reported by the reviewer's verdict, never by the test run. The test run is
   necessary and not sufficient — `sameValue`, the function comparing every int, string and bool a
   payload holds, could be replaced by `return true` with the whole suite green.
