# D-036 — The root module takes no *third-party* requirement

**Status:** accepted — amends [[D-033]]
**Invariant:** the published root module may require another module of this repository and nothing else; a package that imports anything third-party still lives in its own module.

## The decision

[[D-033]] says the root module "must have no external requirements at all". That
is amended to **no third-party requirement**. A first-party module — one in this
repository, with no dependencies of its own — may appear in the root `go.mod`.

Everything else in D-033 stands: a package that imports a driver or a framework
is still its own module, tags still carry the directory prefix, releases are
still lockstep, and a published `go.mod` still carries no `replace`.

## Why

**Because D-033's letter forbade this and its reasoning never covered it.** The
case D-033 argues is MVS floors:

> gin pulls sonic, validator/v10, quic-go, protobuf and mongo-driver; a consumer
> on Fiber has no business having any of them take part in its version
> selection.

A first-party module with an empty `require` block contributes one line and no
transitive graph at all. There is no floor to inherit. The rule was written
against dependency weight and then applied to a case that has none.

**What actually decided it is release cadence, not tidiness.** `errs` is the one
subsystem meant to be adopted by services that have no database — the error
contract is supposed to outlive this repository. D-033's lockstep rule puts
every module on one version, so a team standardising `errs` across forty
services would take a version bump from every CRUD bugfix in a library
thirty-eight of them never import. **Lockstep couples release cadence across
subsystems that have nothing to do with each other, and it lands hardest on the
one with the widest intended audience.**

**Why now and not later.** Module boundaries are far harder to move after the
first tag than names are. Splitting `errs` out afterwards either strands every
import or requires a forwarding module that has to keep publishing forever. The
window is open precisely because nothing has been tagged.

**What this does not license.** It is not an opening for `crud` to import a
sibling. [[D-016]]'s surviving half is unchanged and unrelated: no file in
`crud/` outside `_test.go` may import anything but the standard library, which
is why `crud` cannot import `errs` and why the sentinel a `Fault` wraps is
attached by the caller.

## What it forbids

- Do not add a third-party requirement to the root `go.mod`. That half of
  [[D-033]] is untouched.
- Do not treat this as permission to split a package into a module for taste.
  A first-party module needs a reason of its own — an audience that wants it
  without the rest, or a cadence that should not be coupled.
- Do not let a first-party module acquire a third-party dependency later without
  re-reading this. The moment `errs` requires something, it stops being free and
  the root cannot require it.
- Do not tag a first-party module after the root that requires it. It is a leaf,
  so it is tagged first — `errs`, then the root, then the satellites.

## Where it lives

- `go.mod` — the root; its only permitted requirement is a first-party one.
- `errs/TODO.md` — records that the module is decided and why, until the code
  lands.
- `Makefile:check-deps` — the mechanical check, which filters on the module path
  prefix and so passes a first-party requirement and fails a third-party one.

## Proven by

```
make check-deps
```

prints nothing for the root module and fails when a third-party import appears
in it — verified by adding `github.com/gofiber/fiber/v3` to a root package and
watching it fail, while `go build ./...` and `go vet ./...` stayed green because
`go.work` resolves it from the Fiber binding.

That last part is the reason this check exists at all: the workspace hides
exactly the mistake the invariant forbids.

## See also

[[D-033]] [[D-016]] [[D-015]]
