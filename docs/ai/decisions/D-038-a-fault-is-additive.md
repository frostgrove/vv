# D-038 — A fault is additive

**Status:** accepted
**Invariant:** A `Fault` wraps; it never replaces. Every `crud` sentinel that was reachable with `errors.Is` before a fault was attached is still reachable after.

## The decision

The error subsystem adds structure to an error. It does not become the error.

A caller who wrote `errors.Is(err, crud.ErrConflict)` before any of this existed
keeps that branch, and a caller who wants the violation list reaches it with
`errors.As`. Both work at once, on the same value.

## Why

**Because that is what [[D-015]] already decided, and the fault is the first
thing large enough to be tempted to break it.** D-015's whole argument is that
`errors.Is` is the branch Go callers already reach for, and that a layer adds
context by wrapping rather than by substituting. A `Fault` carrying its own code
enum is exactly the "carried code" D-015 rejected — unless it wraps, in which
case it is both.

**Because the transports do not know about it.** `crud/http/crudhttp:Status` maps
sentinels. If a fault replaced `crud.ErrConflict`, every binding would need a
registration step and the ones that had not been updated would answer 500 for a
duplicate key — the exact failure the sentinel table exists to prevent.

**Because the wrapping has to survive a multi-error.** `fmt.Errorf("%w: %w", …)`
is what the adapters already build, and `errors.Unwrap` returns nil for it. A
`Fault` with `Unwrap() []error` is the same shape, so anything walking the tree
has to walk it as a tree. `crud/adapter/crudsql` walked with a plain `errors.Unwrap`
loop in three separate readers and went blind the moment a fault was in the
chain; phase 3 replaced all three with `crud/sqlfault/extract.go:walk`, which follows
both `Unwrap() error` and `Unwrap() []error`. All three, and not only the
SQLSTATE: the forbid is general, and the MySQL number and the SQLite result code
were the two arms phase 0 had just added.

**Why the negative matters more than the positive.** "A fault wrapping a
sentinel matches it" passes for `errors.Join` and for a dozen wrong
implementations. What pins the decision is that a fault wrapping *nothing*
matches nothing — a fault built for a validation failure must not answer yes to
`errors.Is(err, crud.ErrConflict)` merely because it is a fault.

## What it forbids

- Do not construct a `Fault` that discards the driver or sentinel error it
  describes.
- Do not stop `ErrStaleVersion` wrapping `ErrConflict` ([[D-015]]).
- Do not make a contract package construct a fault. `crud` may not import `errs`
  at all ([[D-016]]), and `query` may — so without the rule there would be two
  classification paths for a library-origin error.
- Do not walk the chain with a bare `errors.Unwrap` loop once faults exist.

## Where it lives

- `errs/fault.go:Fault.Unwrap` — the mechanism: `[]error`, so a walk of the tree
  finds everything the fault was built over.
- `errs/build.go:Builder.Wrapping` — the only exported way anything gets into
  it. A third-party `Classifier` returns a `*Fault` and the field is unexported,
  so a sentinel match cannot be forged.
- `errs/doc.go` — the rule that no contract package constructs a fault, which
  moved here from the package's placeholder when phase 1 deleted it.
- `crud/errors.go` — the sentinels that must stay reachable.
- `port/porthttp/errors.go:Status` — the mapping that keeps working untouched.
  It was not edited when `errs` landed, which is this decision's second claim.
- `crud/sqlfault/extract.go:walk` — the tree walk, following both `Unwrap` shapes.
  One walk now serves the SQLSTATE, the engine number and the SQLite result
  code; the three separate `errors.Unwrap` loops it replaced are gone.
- `crud/sqlfault/extract.go:Extract` — the three readers over that walk.
- `crud/sqlfault/classify.go:Wrap` — the seam, and its own claim: the sentinel is
  attached here whatever a classifier returned, so a third-party
  `errs.Classifier` can neither forge a `crud.ErrConflict` — `wrapped` is
  unexported — nor accidentally drop one.

## Proven by

- `TestAFaultWrappingASentinelMatchesIt` and — the load-bearing half —
  `TestAFaultWrappingNothingMatchesNothing` in `errs/fault_test.go`. The first
  passes for `errors.Join` and for a dozen wrong implementations; only the
  second pins the decision. Its second subtest builds the fault as a struct
  literal, which is everything an implementer of `errs.Classifier` can
  construct, and that one matches nothing either.
- `TestAFaultSurvivesBeingWrappedAgain` in the same file — `errors.Is`,
  `errs.AsFault` and `errors.As` against the driver error all still hold through
  a further `fmt.Errorf("saving user: %w", f)`, with the wraps-nothing twin as
  its control.
- `TestASQLSTATEIsStillFoundThroughAMultiErrorAndThroughAFault` in
  `crud/adapter/crudsql/conflict_test.go` — the regression this decision asked phase 3
  for, at the gate, on all three readers. Its control is the negative twin over
  an error that is not a violation: the positive fixtures carry
  `crud.ErrConflict` by construction, so `errors.Is` says nothing there and only
  the absence of a learned code does.
- `TestADriverErrorIsFoundThroughEveryWrappingShape`,
  `TestTheWrappingsThatDefeatAPlainUnwrapLoop` and
  `TestTheMethodPathIsReachedOnAnErrorThatIsNotAStruct` in
  `crud/sqlfault/extract_test.go` — five wrapping shapes including a fault's own
  `Unwrap() []error` and a multi-error with the sentinel *first*, and the third
  is the regression a struct-only callback would cause.
- `TestAnAlreadyClassifiedErrorIsNotClassifiedTwice` and
  `TestASentinelIsAttachedWhateverTheClassifierReturned` in
  `crud/sqlfault/classify_test.go` — a fault is not built over a fault, and a
  third-party classifier's fault still comes back matching the sentinel. The
  second's control is a `42P01` through the same classifier, which must match
  nothing.
- `TestAClassifiedConflictsBodyCarriesNothingInternal` in
  `crud/http/crudnet/write_edge_test.go` and its two twins — the seam control against
  a real produced fault: `crudhttp.Status` was not edited and a classified
  conflict is still a 409.
- `TestAFaultKeepsItsSentinelReachableThroughStatus` and
  `TestAFaultsKindDecidesAndTheSentinelIsTheFallback` in
  `port/porthttp/errors_test.go` — the same claim against a real `crud`
  sentinel, through the unedited `Status`. This is the only place in the tree
  where that runs, and it is here rather than in `errs`' own test package
  because `errs` is a package of the root module until the first tag ([[D-036]]).

## See also

[[D-015]] [[D-016]] [[D-046]] [[D-044]]
