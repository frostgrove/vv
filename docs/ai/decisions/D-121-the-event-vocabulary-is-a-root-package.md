# D-121 — The event vocabulary is a root package, and its second implementation is what earns it

**Status:** accepted
**Invariant:** `event`, `event/eventmemory` and `event/eventtest` are packages of
`github.com/frostgrove/vv`, not a module. The vocabulary imports the standard
library, `crud` and `errs` and nothing else; no base subsystem imports any of
them; and a store package costs the vocabulary and whatever its own row in
`scripts/event_test.go` says. `event` does **not** join the contract manifest
([[D-048]]) — it is an ordinary root package with ordinary first-party
dependencies, exactly like `jobs`.

## The decision

The [PostgreSQL event-sourcing roadmap](../../roadmaps/2026-09-01-postgres-event-sourcing-roadmap.md)
recorded two E0 decisions this supersedes, both in place and both with the
reason written beside them.

**E0 decision 3 — "no root `event` vocabulary package; a second store would
reopen it".** That is its own condition being met. `event/eventmemory` is not a
double: it admits at the expected version, keeps versions dense and positions
strictly increasing, makes one append atomic, and stages writes inside the
caller's transaction. `event/eventtest` is the contract as a suite a third store
runs against itself, and it exists before that third store does. The clause
asked for the second implementation to justify the seam; the second
implementation shipped in the same phase as the seam.

**E0 decision 1 — "until a named aggregate exists, E1 does not start".** It still
holds for `eventpg` and it does not hold here. The blocker is aimed at what a
wrong aggregate costs once a live schema is written and a migration is owed;
phase 1 writes no schema, no SQL and no table. What it writes is a library
contract, and the thing that keeps a library contract honest is a second
implementation and a conformance suite, both of which it has.

## Why it is not a module

**Because in this repository a module boundary is a third-party dependency
boundary, not an optionality boundary.** [[D-116]] says it in as many words and
[[D-033]] and [[D-036]] are what it rests on: a package that would add a
third-party requirement becomes a module so a consumer downloads only what it
imports. `jobs`, `cache`, `storage`, `port` and `tenancy` are all optional and
all live in the root, because none of them costs a consumer a dependency.
`event` costs nothing either — `go list -deps ./event/...` names the standard
library, `crud`, `errs` and `utils` — so a module for it would be symmetry, and
the [extension architecture revision](../../roadmaps/2026-09-01-extension-architecture-roadmap.md)
already refuses that: *"Stdlib-only implementations do not receive a module
merely for symmetry."*

**What would change the answer.** `event/eventpg` takes a driver for its live
fixtures, exactly as `jobs/jobspg` did. It is a module at that point, under
`event/`, and the move is mechanical: one `go.mod`, one `go.work` line and the
`replace` lines `test/` and `_examples/` already carry.

**Why `eventtest` is not a module either.** It imports `testing`, which is
standard library. `cache/cachetest` and `crud/crudtest` are the precedent.

## Why the zero-diff claim's proof is a fixture and not a check

§INV-019 of the phase-1 specification says a second store costs **zero** diffs to
`event/`. The proof is compile-time and it runs in `make unit`:
`event/eventtest/suite_test.go:TestATrivialStoreNeedsNoInternalAccess` builds a
store in a **third** package — `eventtest`'s own test package — and runs the
suite against it. If any value the contract makes a store return needed
`event`-internal access, that fixture would not compile. Unexport one field of
`Envelope` and it stops compiling, which is how it was checked rather than
claimed.

An earlier draft of the phase-1 plan made a diff of the `event/…` sections of
`docs/api/surface.md` an arm of `make check`. **It is withdrawn.** `CLAUDE.md`
says in terms that `make api` regenerates that file, that *"nothing checks it and
nothing should: a diff there is a question for a person"*, and quoting the second
half of that sentence as support for turning the diff into a build failure
inverts it. The detector was also unsound in both directions: a store can be
admitted by widening an unexported `Outcome` switch with the surface
byte-identical, and any unrelated additive export under `event/` would turn
`make check` red until somebody regenerated the baseline — a structural check
loosened under pressure on its first run.

So the surface baseline is what `CLAUDE.md` asks of it: `make api` produces it,
a person reads the diff. The same argument decides the git arm a later phase may
want — `git diff --stat <phase-1-tag> -- event/ ':!event/eventpg'`, once a tag
exists. Same predicate, wider net, and a **report** rather than a gate, for the
same reason.

## Why `ReadOnly` has no `Next`

[[D-061]] says a wrapper forwards what it wraps and gives every decorator a
`Next()`, so an optional interface reached through one is not lost silently.
`event.ReadOnly` is the one wrapper in this repository that deliberately has
none, and that is the rule working rather than an exception to it.

`ReadOnly(store)` exists to hand a consumer the `Log` half of the seam and take
the append surface away — a projector that can append is how a replay writes.
A `Next()` on it would be the method that hands the taken surface back. The
value it returns embeds `Log`, so everything the read half declares still
forwards; what does not forward is the thing the call was made to remove. A
[[D-061]] review that reaches this closes here rather than reopening it.

## What it forbids

- Do not make `event` a module for tidiness. Make `event/eventpg` one, because
  its fixtures require a driver.
- Do not add `event` to the contract manifest. [[D-048]] closed that list, and
  `event` imports first-party packages that are not on it.
- Do not import `event` from any base package. `scripts/event_test.go` walks the
  import graph of every published module and fails on the first one that does.
- Do not create a package whose identity is the intersection of `event` with a
  third thing — `eventtenancy`, `eventotel`, `eventaudit` — or its mirror.
  [[D-114]] and [[D-116]] refuse the same shape twice already.
- Do not turn the surface baseline into a gate, here or anywhere.
- Do not give `ReadOnly` a `Next()`.

## Where it lives

- `event/` — the vocabulary, the declaration seam and the store contract.
- `event/eventmemory/` — the second implementation.
- `event/eventtest/` — the conformance suite and the three declaration proxies.
- `scripts/event_test.go` — the three graph tests, and what each package of the
  extension is charged.
- `scripts/extensionwalk_test.go` — the four shared walks driven over a written
  tree, which is what stops a deleted arm going unnoticed.
- `scripts/extensionlisting_test.go` — what those three ask the toolchain, and
  therefore **which package classes each check reaches**: a `go list` pattern
  stops at a nested `go.mod`, so every module under `event/` is listed in its own
  directory, and `uncoveredDirectories` fails when a directory holding source was
  listed by nobody.
- `scripts/extensions_test.go` — the prohibitions held over those answers, shared
  with the tenancy extension because `scripts` is one Go package and because two
  copies of one prohibition drift. The cost walk is shared too, so both
  extensions reach a nested module; only the table of what a package may cost and
  the sentences reporting a breach of it stay per extension. That is what makes the three
  checks still true of `event/eventpg` on the day it becomes a module — before
  that day, they would have gone on measuring the tree without it.
- `scripts/checks.sh:SUBSYSTEMS` — with `event` in it, a package under `utils/`
  that imports any of this is a build failure rather than a code review.
- `docs/api/surface.md` — the phase-1 baseline, regenerated by `make api`.

## Proven by

- `TestATrivialStoreNeedsNoInternalAccess` — a store built in a third package,
  which is the whole of the zero-diff claim.
- `TestNoBaseSubsystemDependsOnTheEventExtension` — every package of every
  published module listed, none of them reaching `event` or anything under it,
  with floors on both the module count and the package count so it cannot prove
  nothing. The two unpublished modules are excluded: `test` and `_examples` are
  what an import from outside the extension is *for*.
- `TestNoEventPackageCostsMoreThanTheSeamItNames` — the vocabulary reaches
  `crud` and `errs` and nothing else, and each package beside it reaches nothing
  but the vocabulary. Both halves fail when violated, and a package of the
  extension that is not charged fails rather than being skipped — including one
  that is a module of its own.
- `TestMerelyImportingTheEventExtensionStartsNothing` — no `init` anywhere under
  `event/`, no goroutine started by a package a program links, and no
  package-level value produced by calling anything but `errors.New`,
  `fmt.Errorf` or `reflect.TypeFor`. That arm is fail-closed: an environment
  read, a compiled pattern or a pool opened at import time is refused without
  being named, which the deny list it replaced could not do.
- `TestNoPackageLevelStateIsEverMutated` — the kind is taken from the value's
  **type** rather than from how it is spelled, so `make(...)`, `new(...)` and a
  composite literal are one finding, and a name written through a function's
  parameter is reported where it is declared, a value of a named type declaring
  a pointer-receiver method is reported where it is declared, and an interface
  holder is judged by what it can hold. The twenty-four sentinels and the
  reflected type tokens pass without being exempted by name.
- `TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor` and
  `TestTheKernelNeverIssuesTransactionControlAndNeverRetries` — both resolved
  with `go/types` rather than matched by name, so `stream.Key.String()`,
  `string(stream.Key)`, `detail := string(stream.Key)`, a `%v` on an envelope,
  `tx.commit()`, `commit(tx)` and `finish := tx.Commit` are each the finding
  they are.
- `TestTheStructuralChecksReadOneTypedPackageFromManyGoroutines` — the three
  read one memoised typed package, so a check that wrote into what all three
  read would be a race between two of them rather than a wrong answer.
- `TestTheChecksWalkEveryPackageTheExtensionHoldsAndLinkAllButTheSuite` — what
  those three read is the tree, compared against a second and recursive reading
  of it rather than counted, and the packages a program links are the walked set
  less the ones whose **import list** names `testing`. A walk narrowed to the
  kernel leaves every other assertion in the package green while two of the three
  stop being proved for the store beside it.
- `TestEveryIdentityTheChecksNameIsOneTheVocabularyDeclaresAndEveryOneItDeclaresIsNamed`
  — the four names §INV-011 is checked against are the four types `event`
  declares, and an identity-shaped type it declares is either one of them or is
  written down as something else with its reason. Renaming one, adding a fifth,
  or leaving an exemption behind the type it exempted each fails naming it.
- `TestALifecycleAPackageStartsForItselfIsReportedAndTheIdiomIsNot`,
  `TestAnImportOfTheExtensionFromOutsideItIsReportedAndOneInsideItIsNot`,
  `TestADirectoryHoldingSourceThatNoPackageListedIsReported` and
  `TestAPackageCostingMoreThanItsRowSaysIsReportedAndOneCostingExactlyItIsNot` —
  the four shared walks driven over a written tree that holds what each exists to
  find, because a green answer against this repository is what is wanted and
  therefore proves nothing about the walk that produced it. The cost walk is
  asked twice over one tree: against a table that understates what the extension
  costs, where each of its four sentences is reported, and against one that
  states it exactly, where it says nothing.
- `make check-deps` — `event` adds no external package to the root module's
  graph, which is the whole argument for it not being a module.
- `make check-utils` — with `event` in `SUBSYSTEMS`.

## See also

[[D-033]] [[D-036]] [[D-048]] [[D-058]] [[D-061]] [[D-114]] [[D-116]] [[D-122]]
[[FL-036]] [[UC-032]]
