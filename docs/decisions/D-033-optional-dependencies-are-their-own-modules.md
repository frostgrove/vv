# D-033 — Every optional dependency is its own module

**Status:** accepted
**Invariant:** the published root module must have no external requirements at all, and a package that imports one must live in its own module under the same repository, versioned in lockstep with the root and reachable without a `replace`.

## The decision

Six modules in one repository, four of them published:

```
github.com/shardit-io/ordo                    crud, query, repo, cmd,
                                                    adapter/crudsql, http/crudhttp
github.com/shardit-io/ordo/http/crudfiber     + fiber/v3
github.com/shardit-io/ordo/http/crudgin       + gin
github.com/shardit-io/ordo/adapter/crudpgx    + pgx/v5
github.com/shardit-io/ordo/test               unpublished, replace ../
github.com/shardit-io/ordo/_examples          unpublished, replace ../
```

The two unpublished modules exist for the same reason and use the same mechanism:
the integration suite needs ent, gorm, sqlx, sqlc and two drivers, and the
examples need every stack they demonstrate. Neither may become a dependency of
anything a consumer downloads. The leading underscore additionally keeps
`_examples` out of `go build ./...` at the root, so it is built by `make
examples` rather than by `make unit`.

A consumer installs the base and then only the plugins it uses:

```bash
go get github.com/shardit-io/ordo
go get github.com/shardit-io/ordo/http/crudgin
```

`adapter/crudsql` stays in the root because it is `database/sql`, which is the
standard library — and it is therefore how ent, gorm, sqlx, sqlc and bun are
reached with no plugin at all. `http/crudnet`, the `net/http` binding, stays for
the same reason: the rule is about dependencies, not about layers, so a binding
with no dependency is not a module.

This supersedes [[D-016]], which chose one module and accepted the opposite
trade. D-016's own escape clause is the route taken here: *"If a dependency is
heavy enough to want its own module, that is a new decision with its own file,
and it has to answer the `replace` problem."*

## Why

**Why the trade was re-taken.** D-016 accepted two unconditional requirements —
fiber and pgx — as the price of one `go get`. The price is not constant: it is
one MVS floor per optional dependency, paid by every consumer including the ones
who import none of them. Adding a second HTTP binding made that concrete. Gin
pulls sonic, validator/v10, quic-go, protobuf and mongo-driver; a consumer on
Fiber has no business having any of them take part in its version selection, and
a consumer on `database/sql` has no business resolving pgx.

**Why it is cheap now and would not have been later.** There were no tags. A
module split before the first release costs a `go.work` file; after it, it
breaks every existing import path or strands them on a dead version line.

**How the `replace` problem is answered.** It was the whole of D-016's case, and
it has three parts:

- *No `replace` in anything published.* Each submodule's `go.mod` carries a
  plain `require github.com/shardit-io/ordo vX.Y.Z`. The only `replace` in
  the tree is in `test/go.mod`, which is not published.
- *Tags carry the directory prefix.* The root is `vX.Y.Z`; a submodule is
  `http/crudgin/vX.Y.Z`. This is how Go maps a subdirectory module to a commit;
  without the prefix `go get .../http/crudgin@latest` finds nothing and falls
  back to a pseudo-version of the default branch.
- *Local development uses `go.work`.* It joins all five modules, so a change to
  `crud/` is seen by every binding without a version existing anywhere.

**Why lockstep versions.** Go is happy to let a consumer select the base at
v0.3.0 and a binding at v0.1.0, and the binding will still build, because MVS
raises the base to the higher version. That is the incoherent combination D-016
worried about and it is real. The answer is process, not mechanism: one release
tags everything at one version, and `make release` refuses to run if a
submodule's `go.mod` does not name the version being cut.

## What it forbids

- Do not add an external requirement to the root `go.mod`. If a package needs
  one, the package moves into a module of its own.
- Do not give a package its own module because of what layer it is in. A binding
  or an adapter that imports only the standard library belongs in the library:
  a second `go get` bought for no dependency is a cost with nothing on the other
  side of it. `adapter/crudsql` and `http/crudnet` are both in the root for that
  reason.
- Do not put a `replace` in a published `go.mod`. It is invisible to consumers
  and it hides the fact that the required version does not exist yet.
- Do not tag a submodule before the root tag it requires is pushed. A binding
  that names a version nobody can fetch is worse than an unreleased binding.
- Do not release one module at a version the others are not at. `make version`
  then `make release`, in that order.
- Do not delete `test/go.mod` or `_examples/go.mod`, and do not move a driver, an
  ORM or a test helper into a published module to make an integration test or an
  example compile.
- Do not import anything outside the standard library from a non-test file in
  `crud/`. That half of [[D-016]] survives unchanged and is now checkable across
  the whole root module rather than just one package.

## Where it lives

- `go.mod` — module `github.com/shardit-io/ordo`, `go 1.26`, no `require`
  block at all.
- `http/crudfiber/go.mod`, `http/crudgin/go.mod`, `adapter/crudpgx/go.mod` —
  one external requirement each, plus the library.
- `http/crudnet/` and `adapter/crudsql/` — no `go.mod`, because there is no
  dependency to isolate.
- `test/go.mod`, `_examples/go.mod` — the unpublished modules, each with a
  `replace` per published module.
- `go.work` — joins the four published modules and `test`.
- `Makefile:MODULES` — the list every other target loops over.
- `Makefile:version` — rewrites each submodule's library requirement.
- `Makefile:release` — the ordering and the lockstep check.
- `Makefile:tidy` — and the message it prints when there is no tag yet.
- `README.md` — "Install", which states what to `go get`.

## The one rough edge

Until the root has a pushed tag, `go mod tidy` cannot run in a submodule.
`go mod tidy` ignores `go.work` by design — a module has to resolve its
dependencies the way a consumer would — so a `require` naming a version that
does not exist fails there and only there. Building, testing and vetting all
work from the workspace with no tag at all.

The fix is to cut the first tag rather than to work around it, and `make tidy`
says so when it hits this. Working around it with a `replace` would put a
directive in a published `go.mod` that consumers ignore, which hides the problem
instead of solving it.

## Proven by

Nothing in the test suite asserts the module layout — `go build` is the check,
and it is not a test that can be pointed at. `make unit` and `make vet` loop over
every module, so a package that drifts into the wrong one stops compiling.

Two checks are worth running by hand, and both are cheap:

```
go list -deps ./... | grep -v '^github.com/shardit-io/ordo' | grep '\.'
```

run from the repository root, should print nothing but standard-library paths —
the root module has no external dependency. And from a binding's directory, the
same command must not name the other binding's framework.

The dependency isolation itself is visible in the `go.mod` files: gin's
transitive set (sonic, validator/v10, quic-go, protobuf, mongo-driver) appears
in `http/crudgin/go.mod` and nowhere else.

## See also

[[D-016]] [[D-034]] [[D-015]] [[D-018]] [[D-020]]
