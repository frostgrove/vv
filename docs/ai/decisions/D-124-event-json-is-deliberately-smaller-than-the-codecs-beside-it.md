# D-124 — `event.JSON` is deliberately smaller than the codecs beside it

**Status:** accepted
**Invariant:** `event/encodable.go` and `event/routing.go` answer one question —
*can a value of this type be written by `encoding/json` and read back as itself*
— and they answer it once, when the codec value is built. It holds no build-tag pair, no
mode-dependent behaviour, no depth or size bound of its own on the decode path,
and no knowledge of who wrote the bytes. The trust boundary is the byte cap and
the field checks on `event/store.go` and `event/repo.go`, not the provenance of
the log.

## The decision

There are two JSON analysers in this repository already: `jobs/json.go` at ~1400
lines and `cache/codec.go` at ~1560. `event/encodable.go` and `event/routing.go`
are ~470 lines and 24 unexported functions between them, and they are the third. That is a DRY finding waiting to be
made, so the reasoning is written here once rather than argued a third time.

**The two existing analysers answer a different question.** They decide what an
application's *arbitrary* value does under two different JSON implementations,
because both packages carry the `goexperiment.jsonv2` build-tag pair — `cache`,
`cache/cachetest` and `jobs` carry the non-test pair, and `internal/cachegen` and
`jobs/jobsfx` a test-only one. Behaviour that differs between the two
implementations is the thing they exist to pin.

`event` carries none of that. It asks a narrower question and it asks it at
declaration time rather than per value, so it needs neither the mode split nor
the per-call machinery.

## What it refuses, and why the obvious question was the wrong one

"Can `encoding/json` encode it" is the wrong question and it was the one asked
first. `encoding/json` answers nil for a type it writes as an empty object — and
that fact is then recorded, stored and replayed as a **zero value with no refusal
at any door**. A fact history is the one place that is unrecoverable: the bytes
are the record, and no later load can get back what was never written.

So the walk refuses nine shapes whose bytes no declaration reads back:

- a type that writes itself and declares no matching unmarshaller;
- one whose unmarshaller sits on the value receiver and is therefore called on a
  copy;
- one whose marshaller sits on a pointer receiver where `encoding/json` cannot
  address it;
- a map key whose text methods do not come as a pair, or come with the reader on
  the value receiver;
- a struct with fields and no field `encoding/json` writes;
- two fields rendering one JSON name;
- a struct written by a marshaller promoted from an embedded field with a field
  of its own beside it — on either route, `MarshalJSON` and `MarshalText` alike,
  and promotion is a language rule, so no tag on the embedded field escapes
  this, `json:"-"` included. It is asked **at any depth of the promotion
  chain**: promotion carries through as many embeddings as it takes, so
  `struct{ B }` where `B` is `struct{ Money; SKU string }` writes itself as the
  money and drops `SKU` exactly as `B` does, and a walk that stops at the first
  hop accepts the wrapper while refusing what it wraps. The refusal names the
  chain it came through — *the `Money` embedded in `B`, so `B.SKU` is written
  by nobody*. Where the embedded type declares no reader on the route it writes
  by, or declares one on the value receiver, the refusal says so too, because
  the first remedy it would otherwise offer — name the embedded field — only
  moves the refusal one hop in;
- a struct whose marshalling pair is promoted from an **embedded pointer**.
  Nothing allocates that pointer before `encoding/json` calls through it, so the
  read dereferences nil and panics, and so does the write of a value whose
  pointer was never set. This one is refused with nothing beside it too: the
  panic is the whole failure and no field has to be hidden for it;
- a field reached through an unexported embedded pointer, which `encoding/json`
  writes and can never read back because it cannot allocate a pointer it may not
  set.

### The one deliberate false positive

`reflect` will not say whether a method is declared on a type or promoted into
it. So the two promotion refusals are asked of the embedded fields instead, and
they cannot tell a struct that writes itself *through* the embedded value from
one that declares its own complete pair and happens to embed a marshalling type
as well. The second round-trips perfectly and is refused all the same — a struct
embedding `time.Time` with its own `MarshalJSON`/`UnmarshalJSON` that writes and
reads both fields will not boot.

That is the trade, chosen rather than inherited: the alternative is to accept
the shape and lose a field silently and irreversibly in the one place nothing
can be recovered from. It fails closed, at declaration, with two remedies the
refusal already names — name the embedded field, or declare a codec of your own.
No tag escapes it either, `json:"-"` included, for the same reason the refusal
itself does not: a tag speaks to `encoding/json`'s field walk and method
promotion is a language rule. That is one statement, not two.

Asking the question along the whole promotion chain widens that false positive
by exactly one step and closes an incoherence: a struct that is refused standing
alone was legal the moment it was wrapped in another one, so a shared block
factored out of several payloads — the shape a developer actually writes — was
the one spelling nothing refused. What stays legal is the chain that hides
nothing at any hop: `struct{ struct{ Money } }` round-trips and is accepted.

`Encode` marshals a **pointer** for the same reason: it makes the value
addressable, which is what lets `encoding/json` reach a marshaller declared on a
pointer receiver — the ordinary Go idiom. Without it such a type is written field
by field, as `{}` for most of them, and `Decode`, which has always passed a
pointer, reads a zero value back with no error at any door.

## What the decode path does not bound, stated so phase 2 inherits it knowingly

There is **no** `MaxDecodedBytes` and **no** `MaxPayloadDepth`. What bounds a
decode is the byte cap the store published — `Limits().MaxPayload`, checked
against the recorded payload before the codec is called — and the framework's own
`MaxPayloadBytes` ceiling above it.

That is sufficient here because a payload comes from a log this deployment wrote
and is capped at a megabyte by construction. It is **not** sufficient for a
deeply nested megabyte, and a store whose log can hold bytes this deployment did
not write inherits the gap. `event/eventpg` must decide whether a byte cap is
still the whole answer for it.

## The codec as a party, not as a function

Three obligations an implementation cannot be checked against and is therefore
told, on `event/codec.go`:

1. **All three methods are called from every request goroutine concurrently**
   and each must be safe for that. The framework holds one codec value per
   declared revision for the life of the process.
2. **`Decode` may return a value that aliases the payload it was given** and may
   return freshly allocated memory. It must **not** return one that aliases
   memory the codec itself will write or reuse — the next `Decode` would rewrite
   a value the application already holds. `Fact.RoundTrip` is the runnable proxy
   and refuses a codec that does it, by decoding the same bytes twice from two
   buffers and comparing the two answers for shared memory, and by re-encoding
   either side of a decode of a different payload.
3. **`CanEncode` answers for the reader type alone**, is asked at declaration,
   and must be constant and cheap. The shipped codec charges its answer when the
   codec value is built.

A panic out of any of the three is recovered into the refusal that method's own
error produces — `ErrEncode`, `ErrPayload`, `ErrCodecType`. That is the whole
rule for every extension point in this subsystem: the framework recovers a panic
from an extension that has a stated refusal channel for the same failure and maps
it to the sentinel that channel already produces, and it recovers nothing else.
So an upcaster's panic is recovered into `ErrUpcast`, and a fold's, an identity
mapper's and a store's are not.

**A payload type's own `Equal` is on that table too**, and it took a round of
review to get there: `Fact.RoundTrip`'s fidelity comparison calls it, so it is a
call into application code the framework declares, and it was on the table
neither as a row nor as a rule. It is asked at **one** position — a struct whose
every field is unexported, which is the one shape the exported-field walk has
nothing to compare and the shape `time.Time` is — and its panic is recovered into
`ErrSample`, because "this sample cannot prove what a round trip claims for it"
is the channel that failure already has. Asked at every position instead, it was
two defects: `struct{ Ref *Node }` with `Equal` on `*Node`'s pointer receiver
called it through a nil receiver and unwound a raw runtime panic out of an
exported method, and a `Doc.Equal` comparing titles and not bodies passed a codec
that dropped the body — an application writes `Equal` for the application's
question, and given the verdict it disables the one check that says a codec
recorded what it was handed.

**Narrowing where `Equal` is asked left the check optional by omission**, which
is the same defect through the other door and took a further round to see:
`Equal` became the *only* answer at that position, so a type that does not
declare one was reported as agreement having compared nothing — `netip.Addr`,
`netip.Prefix`, `big.Int` and every value object that keeps its state privately.
Measured: a codec that dropped a `netip.Addr` and kept the note beside it passed
with `nil`. The position now has **three** answers in a fixed order, and the
order carries the argument. Its own `Equal` first, because a type that declares
one says there what its equality is and `time.Time`'s says a monotonic reading
and a location are not the difference — where `==` says they are. Then `==`,
which is what a comparable value object has instead. A type of **size zero** is
excluded ahead of all three, because it holds one value and nothing it carries
can come back different — that is what keeps a marker fact round-trippable
however it is spelled, including one that forbids `==`, and what keeps the field
beside a marker compared by the walk rather than left to the wire.

**And that list held two classes, not one, which took a further round again.**
`netip.Addr` and `netip.Prefix` are *comparable*, so `==` answers for them.
`big.Int`, `big.Rat` and `big.Float` hold a slice, so they are not, and they
declare no `Equal` — and neither repair a refusal there could name exists for
them, because a method cannot be declared on another package's type and the walk
descends into the value however the payload around it is written. Refusing there
made a correct declaration over the kernel's own shipped codec, round-tripping
perfectly, unprovable for ever: §UC-042's obligation unsatisfiable for an
accounting ledger, which is event sourcing's own canonical domain. So where the
walk has no answer the question is asked of the **wire** instead — what came back
is encoded again and the bytes are compared with the ones the sample encoded to.
That needs no method and no name, so it answers for every type, which is the
property the fixed method name never had. It is weaker: a codec that drops the
same thing on the way out and on the way in re-encodes to the bytes it was given
and passes. So it sits **behind** the walk and never beside it — and never
beside a declared `Equal` either, because a codec that hands a `time.Time` back
in UTC is faithful by that type's own equality and writes other bytes, and asking
the wire there would reinstate the false accusation `Equal` is asked to prevent.

**One leaf is left that neither answer reaches: a `func`.** It is the only kind
the last arm of the walk sees that Go declines to compare, and no wire format
records one, so the wire is silent for it too. Asking whether the value was
comparable at all and taking `no` for agreement — which is how that arm was first
written — passed every codec that lost it. It is `ErrSample` naming the type, and
the repair is one the caller can perform and is the modelling the domain wanted
anyway: record what identifies the behaviour, and choose the function from that
when you fold. A `chan` is comparable, takes the same arm and is compared, which
is what shows the `func` answer is a decision rather than an accident of the
guard.

**And a value reached through an interface has no type in common with the one it
is compared against**, which is the third thing that position had to be told.
The walk indexed one value by the other's shape — `NumField` of the decoded
value against the sample, a key of the decoded map against the sample's — and two
raw runtime panics unwound out of `Fact.RoundTrip` and out of `eventtest`'s
conformance binary: `reflect: Field index out of range` for a struct read back as
a wider one, `reflect.Value.MapIndex: value of type int is not assignable to type
string` for a map read back under another key type. Beside them the same arm
called a whole substituted value the same value, because two *valid* values of
different kinds took the branch written for one valid and one invalid. Both walks
now **answer** a type difference rather than walking it: for fidelity it is a
difference like any other (`ErrPayload`), and for aliasing it is not shared
memory. The one exception is named rather than fallen into — two numbers holding
one value, because no wire format this admits has an integer distinct from a
float, so an `int64` behind an `any` read back as a `float64` of the same value
passes and one of a different value does not. **And the exception stops where the
float does.** `asFloat` answers a second value beside the number, whether the
float holds the integer exactly, and a number it does not is not carried as one
value in two representations: an id or an amount above 2^53 — a snowflake, a
satoshi balance — read back as the nearest float is `ErrPayload` like any other
difference. Without that clause the widening is not a representation rule but a
licence to round every large integer a payload records.

## When to extract a shared analyser

Not now, and the trigger is specific rather than "when it feels like too much":

- a **fourth** copy is proposed, **or**
- `event` grows the `goexperiment.jsonv2` build-tag pair, which is what would
  make its question the same question as the other two.

Until one of those, three analysers answering three questions is less coupling
than one analyser with three modes. The extraction, when it happens, is a
package under `internal/` and not a public one. What a codec may encode stays the
codec's business either way: `Codec.CanEncode` is where that rule lives, and
nothing extracted from three private analysers may promote it into the kernel.

## What it forbids

- Do not add a rule about a payload's *shape* to the kernel. The encodability
  question is the codec's, and a kernel rule about a codec's domain is policy in
  the kernel.
- Do not give `event/encodable.go` or `event/routing.go` a build-tag pair
  without extracting the shared analyser at the same time. That is the trigger,
  not a coincidence.
- Do not widen where `Equal` is asked. Every field of the struct being
  unexported is not a heuristic for "the walk cannot see it" — it is the
  condition itself, and one position further out is an application method
  vetoing the framework's own check.
- Do not make the absence of an `Equal` an answer. Narrowing where the method is
  asked removed the *override* and left the *omission*, and the omission is the
  wider case: a lenient `Equal` needs a consumer to have written one, while no
  `Equal` at all is the default for every type that is not `time.Time`.
- Do not answer an exhausted value walk in the caller's favour, and do not answer
  a walk that had nothing to compare either. A walk that stopped early compared
  nothing past where it stopped, and reporting that as a pass is a check that
  cannot fail; the type walk beside it refuses for the same reason and this one
  says the same thing with `ErrSample`. There are three of those routes —
  exhaustion, a panicking `Equal`, and a `func`, which no wire format records —
  and each is carried out rather than resolved. The fourth, a struct nothing
  could compare, is carried out to the **wire** rather than resolved either.
- Do not refuse a payload for the shape of a type the caller does not own. A
  refusal names a repair, and neither repair a fixed method name can ask for
  exists for a `math/big` value. Where the walk has nothing, the wire answers.
- Do not ask the wire beside an answer the walk already has — not beside a
  declared `Equal`, not beside `==`, not beside the field walk. Re-encoding is a
  question about bytes, and a codec that normalises what it records answers it
  differently while losing nothing; it is a second answer only where there is no
  first one.
- Do not walk two values of different types in step. They are reached through an
  interface, one cannot be indexed or keyed by the other's shape without
  panicking out of an exported method, and a value substituted for another is a
  difference and not a representation. The numeric widening is the one exception
  and it is a named arm, never a branch two answers share.
- Do not add a decode-side depth or size bound here on the strength of a
  hypothetical. Add it in the store that can hold bytes this deployment did not
  write, and say so on its page.
- Do not recover a panic from an extension point that has no stated refusal
  channel for the same failure.

## Where it lives

- `event/codec.go` — `Codec`, `JSON`, `encodeWith`, `decodeWith`,
  `canEncodeWith`, and the three obligations.
- `event/encodable.go` — the walk itself: the type graph, the graph budgets it
  runs under, and the JSON names a struct renders.
- `event/routing.go` — `encoding/json`'s own routing rules restated as
  refusals: which method set it reaches where, the promotion chain, and the map
  key. The two files hold the nine refused shapes between them and were one
  until the ninth arm took it past the 400-line budget.
- `event/fact.go` — `Fact.RoundTrip`: the runnable proxy for obligation 2 and
  for fidelity.
- `event/comparison.go` — `valueWalk`, the two walks it makes over an
  application's own decoded values, `valueWalkNodes` (the budget they run under,
  which is **not** the type walk's — that one bounds a type and one type has any
  number of values), `sameOpaque` with its three answers in order,
  `equalByMethod`, `sameNumber` (the one type difference that is not a
  substitution), `disturbedAtItsOwnWidth` (the second disturbance, as wide as
  the sample's own payload, which asks nothing of a codec that cannot read it),
  and `singleValued`, which is what keeps a marker fact round-trippable.
- `event/store.go` and `event/repo.go` — the byte cap that is the actual trust
  boundary.

## Proven by

- `TestADeclaration` — the gate on the nine refused shapes themselves, in two
  subtests: *"the shipped codec answers for the whole type graph"* holds the
  refused and the accepted tables, and *"a refusal names which asymmetry it
  found, because the three share one sentinel"* pins each diagnosis by name, so
  deleting one arm cannot pass through the message of the arm beside it. The
  accepting rows are the controls: `itemised`, `tallied`, `netted`, `agreed` and
  `folded` — the last of them a chain of two embeddings that hides nothing — are
  what a repair that over-refuses breaks. Three of them are the mirror of a
  refusal that had a control in the refusing direction only, and each is what
  turns an over-broad arm into a red test rather than a process that will not
  boot: `muted` (a field beside the embedding that encoding/json is told to skip,
  against `besideIt` counting one), `remitted` (a struct declaring its own pair
  and embedding one that declares none, against `promotedMarshaller` no longer
  comparing the write route) and `struct{ Held [2]posted }` (a pair in an array
  at an addressable position, against an array element losing the position's
  addressability — `map[string][2]posted` is its refused mirror).
- `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` — a codec that
  cannot encode its own reader type, and one that panics when asked, both refused
  at declaration and both naming the revision.
- `TestACodecThatDecodesIntoAReusedBufferIsCaught` — obligation 2, including the
  re-encode arm that reaches a reused buffer the value walk cannot see through
  and the second, wider disturbance behind it — the sample's own payload with one
  byte changed, which is what finds a codec that fills that buffer to the
  payload's width and clears nothing, where the zero value is too narrow to reach
  the bytes the first answer points at,
  and the nine subtests that hold the comparison itself: *a pointer field is not
  asked its own type's `Equal` through a nil* (which panicked out of `RoundTrip`
  before), *an application's own `Equal` does not decide whether the codec kept
  what it was handed* — with `time.Time` and the forgetful codec beside it, so
  neither half of the narrowing can be made by breaking the other — *an `Equal`
  that panics refuses the sample rather than unwinding*, *a sample larger than
  the walk is refused rather than reported as a pass*, *a struct nothing can
  compare is answered at the wire rather than reported as a pass* — the dropped
  `netip.Addr`, with a faithful codec over the same type and the `time.Time` case
  as its two controls, so the answer cannot be made by refusing every
  all-unexported struct, and `hushed` and `heldTight` beside a codec that reads
  the same wire format back whole — *a payload the caller does not own the type
  of is provable all the same*, a `*big.Int` through the shipped codec and
  through one of the caller's own, with a codec that reads the amount back as
  nothing as the control that stops the pass being widened — *a leaf no wire
  format records is refused rather than reported as a pass*, a `func` refused and
  a `chan` compared, with the repair the refusal names round-tripping as the
  third row, and beside it the same payload through a codec that drops the note
  as well, which is the row that says the first condition the walk carried out is
  the one the caller is told about — *a value substituted for another behind an
  interface is answered, not walked*, a thirteen-row table whose five accepting
  rows are the controls, three of them the widening arm at its boundary: an
  `int64` and a `uint64` too large for a float to hold exactly are refused where
  `1 << 52` read back as that float passes, so the exception cannot be bought by
  rounding every large id and cannot be closed by refusing every large one — and *a
  fact whose whole content is that it happened round trips*, whose controls are a
  payload-carrying fact given its own zero value, which is still a caller
  mistake, a marker type that forbids `==`, and a marker beside a field a codec
  truncates on both sides, which is what holds the zero-size arm. *What an
  encoding drops by design is not what it lost* carries the two rows that pin the
  order the answers are asked in: a codec that loses the same digit both ways is
  caught by `==` and not by the wire, and one that hands a `time.Time` back in
  UTC is faithful and must not be caught by the wire at all. Two further subtests
  hold the arms of the two walks that every fixture above reaches through one
  field holding a slice or a map directly, and that a comparison stopping at that
  arm would therefore pass: *memory shared one hop in is found through a slice
  element, a map value and a pointer*, three codecs filling one buffer per decode
  whose payloads carry their bytes in a `[][]byte`, a `map[string][]byte` and
  behind a pointer — each beside the same wire format over a codec that allocates
  — and *a scalar changed, a map entry dropped and a value behind a pointer are
  each a difference*, four codecs that keep the shape and change one ordinary
  thing: an integer, a string, one map entry every other entry of which arrives
  as it was given, and a value reached through a pointer. A third holds what the
  two of them still decided by a length or by a scalar: *a value read back
  somewhere else, or not at all, is a difference too*, three codecs that keep the
  count and lose the place — every entry read back under an upper-cased key,
  every amount multiplied, and a set pointer field read back as nothing beside a
  field that survived — each beside a codec that reads the same payload back
  where it stood. They are what makes the entry comparison look each key up in
  the value beside it rather than in the value it came from, what makes an entry
  that is not there a difference rather than an absence, and what makes a pointer
  one side holds and the other does not a difference. Every one of the ten is
  red when the arm it names is neutralised, and no other case in the file is.
- `TestTheProxiesCompareSomethingThatCanDiffer` — the control: the comparison the
  proxy makes can fail, so a pass means something.
- `TestACodecPanicBecomesThatMethodsOwnRefusal` and
  `TestAPanickingCodecBecomesTheRefusalItsErrorWouldHaveBeen` — the recovery
  rule, at declaration and at load.
- `TestOneDeclarationIsDecidedAndFoldedFromManyGoroutines` — obligation 1, under
  `-race`.
- `FuzzAStoredPayloadIsFoldedOrRefused` — a recorded payload is folded or refused
  and never silently read as something else.

## See also

[[D-021]] [[D-121]] [[D-122]] [[FL-036]] [[UC-032]]
