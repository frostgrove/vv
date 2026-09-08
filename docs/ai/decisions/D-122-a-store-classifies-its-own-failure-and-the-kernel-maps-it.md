# D-122 — A store classifies its own failure in a closed vocabulary, and the kernel maps it through `errors.As`

**Status:** accepted
**Invariant:** A store's only error channel to the kernel is
`event.Failure(outcome, cause)`. It adds no sentinel of its own, and the kernel
reads its classification with a bounded `errors.As` — never with a type
assertion, never from the message, and never by re-reading a stream to work out
what happened. Each of the twenty-four sentinels belongs to exactly one of six
classes, `errors.Is` never crosses a class, and no wrap answers for a target
inside the vocabulary. A store's *cause* is reachable through `event.CauseOf`
and by nothing else.

## The decision

Seven outcomes, and the store picks one:

```go
Unclassified · Conflict · NotWritten · Unconfirmed · Closed · BadCursor · Refused
```

`event/errors.go:refuse` maps the selection and reads nothing else. The door the
failure arrived at decides exactly one thing — what an *unclassified* error
means — because the safe guess differs between a write, where guessing "not
written" is a silent double write, and a read, which wrote nothing there is
anything to be uncertain about.

## Why `errors.As` and not a type assertion

Because the assertion version is in this repository and it has the bug.
`jobs/queue.go:808:normalizeSenderError` reads its own classification with
`err.(rejectedPlacement)`. It is correct for an error the queue built itself and
wrong for one that came back through anything: a decorator that wraps a
placement refusal with `%w` to add its own context turns a **rejection** into an
**ambiguity**, and the caller is told the enqueue may or may not have happened.

`event` is a seam a decorator is expected to sit on ([[D-061]]), so the same
shape here would be worse, not better. The classification is found through the
wrapping, which is what lets a decorator forward a store's failure and add its
own context without changing what it means.

## Why an enum and not the sentinel set

A store could have been asked to return `event.ErrConflict` directly. It is not,
for three reasons and the third is the load-bearing one.

1. **A store selects; it never renders.** The message a caller sees, the class
   it belongs to and the wrap a transport reads are the kernel's to choose, in
   one place, for every store.
2. **The map's totality is a property of a constructor rather than a hope about
   callers.** `Failure` normalises an outcome outside the seven to
   `Unclassified` before the value can travel, so a store that grew a value the
   kernel does not know about takes the door's fail-safe default rather than
   reaching a `default:` branch nobody wrote.
3. **A sentinel a store returns is a sentinel a store can return by accident.**
   With the vocabulary as the channel, a store that happens to wrap
   `event.ErrConflict` for its own reasons cannot make the kernel report a
   conflict.

## Why `Support` is a tri-state

`cache.Capabilities` and `storage.Capabilities` are structs of `bool`s, and both
are right for their question: a backend either does batch reads or it does not,
and neither answer needs a third.

An event store's capabilities are read at a **door** that refuses. A `bool` has
no way to say *"nobody told us"*, so a zero value would read as "does not
support" — and a store that forgot to fill the struct would be silently demoted
rather than refused. `Unstated` is the zero value and it is refused at both
doors: `Bind` and `Read`. A capability nobody stated is not one nobody has.

`Unsupported` is then a real answer with a real consequence: the conformance
suite reports the sections that need it as **not certified** rather than as a
pass, which is the same distinction one level up.

## The refusal wrapper, and its two traversals

`event/errors.go:refusal` holds three fields — the sentinel, a declared class
wrap, and the store's cause — and the three are reachable by three different
means on purpose.

| Traversal | Reaches |
|---|---|
| `errors.Is` | the sentinel, and the declared class wrap |
| `errors.As` | the declared class wrap only |
| `event.CauseOf` | the cause, and nothing else does |

**Why the cause is not reachable by either standard traversal.**
`port.KindOf` falls through `errs.AsFault` into six `errors.Is` branches over
`crud` sentinels. So a cause that happens to carry `crud.ErrUnavailable` — a
pool timeout, a driver's own retryable class — would make an **uncertain commit**
render as 503 *retry me*, and retrying an unconfirmed append writes the same
decision twice. That is the failure this shape exists to prevent, and it is not
hypothetical: a store's cause is a driver's error and driver errors carry
classes.

**Why it is a field rather than an absence.** An earlier shape dropped the cause
entirely. That took the *application's* own error down with it — a policy
decorator's refusal and an upcaster's error are the two cases where the caller
must be able to match what the author of the failure wrote. So the cause is kept
and the traversal is gated instead.

**The measurement that forced the shape.** `cache/errors.go`'s `opaqueError` is
the nearest precedent and it does not fit: it declares no `Unwrap` and no `As`,
so `errs.AsFault` cannot see a class wrap through it, and a refusal that needs to
carry *"this is a client error"* through to a transport cannot use it.

**The two exceptions, and they are exceptions to the field and not to the rule.**
`ErrRefused` and `ErrUpcast` take their cause **as** their wrap, because for
those two the cause *is* the answer: a policy refusal's transport status is the
policy's to state, and an upcaster's own error is what the caller reads. Both
still go through `causeAsWrap`, which promotes only a chain the kernel could read
to the end — past the budget the two traversals would disagree, and a
`crud.ErrForbidden` a decorator meant as a 403 would be invisible while an
`*errs.Fault` at the same depth still set the status.

## No wrap answers for a target inside this vocabulary

`refusal.Is` gates the **target** of the traversal, not the promotion of the
wrap: a wrap may carry anything, and it answers for anything except one of the
twenty-four. Without that, a quota decorator wrapping `ErrConflict` to mean 409
would put a caller's retry loop on a policy refusal that will never clear.

Gating the target rather than refusing or dropping the wrap is what keeps the
decorator's and the upcaster's own error matchable when it happens to co-carry
one of the twenty-four. The prohibition on a store returning one of this
vocabulary's sentinels as its cause is therefore **not checked** — it is made
harmless.

## The kernel's own bounded `errors.As`

`event/errors.go:findAs` and `event/errors.go:walk` are the kernel's traversal,
with a hop budget and a `recover`. Every question the kernel asks of an error it
did not build goes through them.

A store's chain is a **foreign** chain. The standard library's walk over one has
no budget and no `recover`, so a cyclic `Unwrap` in a store's error wedged the
request goroutine on the first statement of every store failure, and a matcher
that panics took the request with it. The budget counts errors *visited* rather
than depth reached, because `errors.Join` branches: a budget spent per path lets
a store that joins per-shard failures of per-row failures cost two to the depth.

`findAs` also refuses a `T` that is nil, because every caller dereferences what
it is handed and the producing input is an unpopulated `*errs.Fault` a store
wrapped — a nil-interface mistake rather than a contract violation.

## Cancellation outranks everything

Both traversals answer **false** for `context.Canceled` and
`context.DeadlineExceeded`, always. A classified failure whose cause happens to
be a cancellation is the outcome's refusal and not a cancellation: a caller who
branches on cancellation first would otherwise abandon a write nobody confirmed.
A *bare* cancellation from a store travels as itself, because a store's own text
around one names the key and the version it was reading.

## What it forbids

- Do not read a store's classification with a type assertion, here or in a new
  subsystem written on this model.
- Do not let a store return one of this vocabulary's sentinels as its error. It
  will not be honoured, and it is not checked because it is harmless.
- Do not add a sentinel to a class without asking whether the class is right;
  adding one is additive, moving one between classes is breaking.
- Do not make the cause reachable by `errors.Is` or `errors.As`. `CauseOf` is
  the reader, and the reason is a rendered status a caller acts on.
- Do not use the standard library's `errors.Is`/`errors.As` on a value the
  kernel did not build. Use `matches` and `findAs`.
- Do not recover a store's panic. It has an error channel and an outcome
  vocabulary; recovering one would be the kernel classifying a failure the store
  did not classify.

## Where it lives

- `event/outcome.go` — `Outcome`, `Failure`, `failure`.
- `event/errors.go` — the twenty-four sentinels, `vocabulary`, `refusal`,
  `refuse`, `CauseOf`, `causeAsWrap`, `walk`, `findAs`, `matches`.
- `event/store.go` — `Support`, `Capabilities`, and the clause that says a store
  which can classify must.
- `event/binding.go:admitCapabilities` — where `Unstated` is refused.

## Proven by

- `TestTheRefusalVocabularyIsAPartition` — every ordered pair of the twenty-four
  answers exactly the closed intra-class list, over every door, every outcome
  and every cause; and the gate's list is read from the source as well as
  called, so a sentinel declared and not gated is reported.
- `TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot` — each half named
  separately, including a cause that answers only through its own `Is` method
  and one that answers only through its own `As`.
- `TestAContextCauseNeverTravelsThroughARefusal` — a store's text around a
  cancellation does not travel, and a classified cancellation is not one.
- `TestAnOutcomeOutsideTheVocabularyNormalises` and
  `TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault` — the map's
  totality, at the constructor and at the door.
- `TestAJoinedCauseCannotOutlastTheWalksBudget` and
  `TestANilOrLyingCauseNeverPanicsTheKernel` — the budget and the recover.
- `TestEveryRenderingNamesAClassAndNeverAValue` and
  `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor` — what a refusal says,
  in its rendering and in its source. The source half follows a string built
  into a local one statement earlier, so `detail := string(key)` is read where
  it is wrapped; what it still does not follow is written down in [[FL-036]]
  rather than left in a code comment.
- `TestAFormatThatShiftsItsOwnArgumentsIsRefusedRatherThanReadWrongly` and
  `FuzzAFormatIsMappedVerbByVerbOrRefusedWhole` — which verb renders which
  argument, checked differentially against `fmt` itself, because a mapping that
  is off by one excuses a rendered key and reports a wire type name that was
  never one.

## See also

[[D-015]] [[D-040]] [[D-049]] [[D-061]] [[D-121]] [[FL-036]] [[UC-032]]
