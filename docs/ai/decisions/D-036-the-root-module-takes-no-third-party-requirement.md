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

**Why the rule now, and the split at the first tag.** The rule belongs here now
because module boundaries are far harder to argue after a release than before
one. The *split itself* has to wait, and that is a toolchain constraint rather
than a preference — measured, not assumed.

Before the first tag the root would require `errs@v0.0.0`. Every module that
requires the root then fails to walk its module graph: `go list -m all` tries to
read `errs/go.mod` at a revision that does not exist, and the workspace does not
substitute for it. Resolving `errs` on its own succeeds; the full graph walk is
what fails. An explicit `require` in each satellite does not help, `go mod tidy`
under a transient replace does not help, and `go work edit -replace` is refused —
*"workspace module … is replaced"*. Only a fetchable version fixes it.

So `errs` lives as a package in the root module until the first tag, and the
`go.mod` that makes it a module lands in the same change as `v0.1.0`. The
contract does not change; only its packaging does, and this decision is what
makes that packaging legal when it arrives.

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
- Do not read a build tag as an exemption. A third-party import inside a
  `_test.go` is a requirement of whichever module holds the file, because
  `go mod tidy` reads every build configuration.

## Where it lives

- `go.mod` — the root; its only permitted requirement is a first-party one.
- `errs/doc.go` — records what the first tag freezes, and the packaging note
  the package's placeholder carried until phase 1 replaced it with real code.
- `scripts/checks.sh:check_deps` — the mechanical check, which filters on the module path
  prefix and so passes a first-party requirement and fails a third-party one. It
  lists the root module with `-test -tags=integration`, because a driver imported
  by a `_test.go` behind a build tag is still a `require` in the published
  `go.mod`: `go mod tidy` reads every build configuration and the tag exempts
  nothing. A listing that fails is a refusal rather than an empty answer — a test
  importing a package the module does not require fails exactly that way, and the
  arm's old `2>/dev/null || true` read that as a clean module.
- `scripts/checks.sh:root_third_party` — the listing itself, split out so the failure
  to list and the third-party answer are two different refusals.
- `jobs/jobspg/go.mod` — the module that exists because of this decision. Its
  production code takes nothing but `database/sql`; its tagged integration
  fixtures open a database through pgx, they are internal tests and cannot move
  to `test/` without being rewritten, so the requirement they create lives with
  them instead of in the root.
- `scripts/checks.sh:TIER0_SEALED` — the other half, added with phase 1. This decision's
  case rests on `errs` having an **empty** require block and therefore being
  taggable first, and nothing enforced that: `check-tiers` filters out every
  contract package, so `errs` importing `crud` passed build, vet, tidy and every
  check, and would have become a require cycle at the tag. The sealed arm lets
  `errs/...` import the standard library and itself and nothing else. Scoped to
  the prefix because phase 2's parsers in `errs/sqlerr` will import `errs`, and
  both end up in the same module. It is the one arm that passes `-test`: `go mod
  tidy` counts test imports, so `crud` imported from `errs`' *test* package is
  the same require cycle, and it is the case no other check can see — the import
  is intra-module, so `check-tidy` stays green and `check-deps` finds nothing
  third-party.
- `scripts/checks.sh:TIER0_STDLIB` — the same seal in the other direction, and owed to
  [[D-016]] rather than to this decision: `crud` may import only the standard
  library, which is what makes it unable to import `errs`. Both directions are
  recorded here because this is where the "crud cannot import errs" sentence
  lives. It runs without `-test`: `crud`'s own tests import `crud/sqlrepo`, legal
  inside one module.
- `test/bridge/fieldviolation_test.go` — where the validator assertion lives,
  and not `errs`' own test package where `ROADMAP-errors.md` §5 put it. Until
  the first tag `errs` is a package **of the root module**; `go mod tidy` counts
  test imports and `make check-tidy` runs `go mod tidy -diff` on `.`, so that
  line would either fail the check or put the root's first third-party
  requirement into `go.mod`. `make check-deps` did not see it then, because that
  arm listed the root module without `-test`; it does now, and `jobs/jobspg` is
  the case that made the difference visible.

## Proven by

```
make check-deps
```

prints nothing for the root module and fails when a third-party import appears
in it — verified by adding `github.com/gofiber/fiber/v3` to a root package and
watching it fail, while `go build ./...` and `go vet ./...` stayed green because
`go.work` resolves it from the Fiber binding.

That last part is the reason this check exists at all: the workspace hides
exactly the mistake the invariant forbids. It hid the test half of the same
mistake for as long as the arm ran without `-test`: seven tagged fixtures in
`jobs/jobspg` imported pgx, `check-deps` said `ok`, and the only symptom was
`check-tidy` demanding a pgx `require` in the root `go.mod`.

`scripts/checks_test.go` pins both halves:

- `TestCheckDepsRefusesAThirdPartyPackageOnlyAnIntegrationTestImports` builds a
  fixture repository whose only third-party import is in a `//go:build
  integration` test and requires `check-deps` to name it. Against the arm as it
  was, the fixture printed `check-deps: ok`.
- `TestCheckDepsPassesWhenATestOfTheRootModuleStaysInTheStandardLibrary` is its
  control: the same fixture with a test that imports nothing but `testing` has to
  pass, so the first test cannot be satisfied by a check that refuses everything.
- `TestNoTestInTheRootModuleOfThisRepositoryImportsAThirdPartyPackage` runs the
  check against this repository. It is the one that fails if `jobs/jobspg` loses
  its `go.mod` again.

```
make check-tiers
```

prints nothing for `errs` and fails when it reaches outside `errs/...` —
verified both ways, with `import _ "github.com/frostgrove/vv/crud"` in a file of
`package errs` and again in one of `package errs_test`. The second is the one
worth having: it is invisible to `go build`, `go vet`, `make check-deps` and
`make check-tidy`, and would first appear as a require cycle at the tag.

## See also

[[D-033]] [[D-016]] [[D-015]]
