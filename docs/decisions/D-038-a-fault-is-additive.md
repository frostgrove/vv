# D-038 — A fault is additive

**Status:** accepted — in force from phase 1 (`ROADMAP-errors.md` §14)
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

**Because the transports do not know about it.** `http/crudhttp:Status` maps
sentinels. If a fault replaced `crud.ErrConflict`, every binding would need a
registration step and the ones that had not been updated would answer 500 for a
duplicate key — the exact failure the sentinel table exists to prevent.

**Because the wrapping has to survive a multi-error.** `fmt.Errorf("%w: %w", …)`
is what the adapters already build, and `errors.Unwrap` returns nil for it. A
`Fault` with `Unwrap() []error` is the same shape, so anything walking the tree
has to walk it as a tree. `adapter/crudsql:sqlState` walks with a plain
`errors.Unwrap` loop today and goes blind the moment a fault is in the chain;
phase 3 owes that fix in the same change that introduces the fault.

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

Nothing yet. Phase 1 creates `errs/`, and this decision is what its `Fault` has
to satisfy on the day it is written.

- `crud/errors.go` — the sentinels that must stay reachable.
- `http/crudhttp/errors.go:Status` — the mapping that must keep working
  untouched.
- `adapter/crudsql/conflict.go:sqlState` — the walk phase 3 owes.

## Proven by (owed)

- Phase 1 owes both halves and the negative is the load-bearing one: a `Fault`
  wrapping `crud.ErrConflict` matches it, **and** a `Fault` wrapping no sentinel
  matches none. Without the second the test passes for `errors.Join` and proves
  nothing.
- Phase 3 owes a regression test that `sqlState` still finds a SQLSTATE through
  a multi-error.

## See also

[[D-015]] [[D-016]] [[D-046]] [[D-044]]
