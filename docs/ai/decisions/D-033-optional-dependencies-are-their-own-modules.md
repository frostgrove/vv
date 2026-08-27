# D-033 — Every optional dependency is its own module

**Status:** accepted — the *external requirement* half amended by [[D-036]], and the *one requirement per module* half by [[D-051]]
**Invariant:** the published root module must have no **third-party** requirement ([[D-036]] narrowed this from *no external requirement at all*), and a package that imports one must live in its own module under the same repository, versioned in lockstep with the root and reachable without a `replace`.

## The decision

Fourteen modules in one repository, twelve of them published. The list is
**discovered** by `Makefile:MODULES` (`find . -name go.mod`) and never read from
here; this is the shape, not the manifest.

```
github.com/frostgrove/vv                     the root: crud, query, errs, port,
                                             remote, auth, cmd, and every
                                             stdlib-only binding under them
                                             (crudnet, authhttp, authnet,
                                             apikey, crudsql, crudhttp,
                                             porthttp, remotehttp, vvdb,
                                             vvflag)

crud/http/crudfiber                          + fiber/v3
crud/http/crudgin                            + gin
crud/rpc/crudgrpc                            + grpc, protobuf, genproto  (D-051)
crud/adapter/crudpgx                         + pgx/v5

auth/authjwt                                 + golang-jwt
auth/http/authfiber                          + fiber/v3
auth/http/authgin                            + gin
auth/rpc/authgrpc                            + grpc

utils/vvcfg                                  + a config loader
utils/vvdb/dbpgx                             + pgx/v5
utils/vvgoose                                + Goose, CLI UI, registered SQL drivers (D-064)

test                                         unpublished, replace ../
_examples                                    unpublished, replace ../
```

Two pairs look like one dependency decision split in two and are not.
`crudpgx` adapts a pool the application already opened; `dbpgx` builds one from a
config — [[D-057]] makes either usable without the other, so neither implies the
other. `crudgin` and `authgin` are the same case across subsystems: a consumer
mounting auth on Gin must not be made to take the CRUD binding ([[D-051]]).

`errs` is the module this list does not yet contain and will at the first tag —
it is a first-party requirement, which [[D-036]] permits and which cannot be
split before a tag exists.

The two unpublished modules exist for the same reason and use the same mechanism:
the integration suite needs ent, gorm, sqlx, sqlc and two drivers, and the
examples need every stack they demonstrate. Neither may become a dependency of
anything a consumer downloads. The leading underscore additionally keeps
`_examples` out of `go build ./...` at the root, so it is built by `make
examples` rather than by `make unit`.

A consumer installs the base and then only the plugins it uses:

```bash
go get github.com/frostgrove/vv
go get github.com/frostgrove/vv/crud/http/crudgin
```

`crud/adapter/crudsql` stays in the root because it is `database/sql`, which is the
standard library — and it is therefore how ent, gorm, sqlx, sqlc and bun are
reached with no plugin at all. `crud/http/crudnet`, the `net/http` binding, stays for
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
  plain `require github.com/frostgrove/vv vX.Y.Z`. The only `replace`
  directives in the tree are in `test/go.mod` and `_examples/go.mod`, neither of
  which is published.
- *Tags carry the directory prefix.* The root is `vX.Y.Z`; a submodule is
  `crud/http/crudgin/vX.Y.Z`. This is how Go maps a subdirectory module to a commit;
  without the prefix `go get .../crud/http/crudgin@latest` finds nothing and falls
  back to a pseudo-version of the default branch.
- *Local development uses `go.work`.* It joins the eleven published modules and
  `test`, so a change to `crud/` is seen by every binding without a version
  existing anywhere.

**Why lockstep versions.** Go is happy to let a consumer select the base at
v0.3.0 and a binding at v0.1.0, and the binding will still build, because MVS
raises the base to the higher version. That is the incoherent combination D-016
worried about and it is real. The answer is process, not mechanism — and mechanism is not merely unchosen, it
is unavailable. MVS has no upper bound, so nothing can stop a consumer selecting
`vv@v0.2.0` with `vv/crud/http/crudgin@v0.1.0`; the binding's own requirement raises
the library, and only the *other* direction — old binding, new library — is
dangerous, which is exactly what a bare `go get -u` produces. What the repository
can do is refuse to *publish* an incoherent set: `make release` will not run if a
satellite's `go.mod` does not name the version being cut, it creates tags
idempotently, and it pushes them in one `git push --atomic` so a release cannot
half-land. `retract` in the root `go.mod` is the only remedy after the fact.

## What it forbids

- Do not add an external requirement to the root `go.mod`. If a package needs
  one, the package moves into a module of its own.
- Do not give a package its own module because of what layer it is in. A binding
  or an adapter that imports only the standard library belongs in the library:
  a second `go get` bought for no dependency is a cost with nothing on the other
  side of it. `crud/adapter/crudsql` and `crud/http/crudnet` are both in the root for that
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

- `go.mod` — module `github.com/frostgrove/vv`, `go 1.26`, no `require`
  block at all.
- `crud/http/crudfiber/go.mod`, `crud/http/crudgin/go.mod`, `crud/adapter/crudpgx/go.mod` —
  one external requirement each, plus the library.
- `crud/rpc/crudgrpc/go.mod` — three, and still one *decision*: grpc, protobuf and
  genproto arrive together or not at all. [[D-051]] is the rule that says so,
  written when this line's literal reading first came apart.
- `crud/http/crudnet/` and `crud/adapter/crudsql/` — no `go.mod`, because there is no
  dependency to isolate.
- `test/go.mod`, `_examples/go.mod` — the unpublished modules, each with a
  `replace` per published module it imports. A satellite absent from either is
  absent because that module does not import it, not because the published list
  is hand-maintained.
- `scripts/common.sh` — discovers modules with `find`, rather than listing
  them. A hand-written list is how a module escapes unit, vet, tidy and release
  at once, and this repository has already been bitten by exactly that.
- `scripts/modules.sh` — rewrites each submodule's library requirement and
  tidies modules without leaving published `replace` directives behind.
- `scripts/release.sh` — the release ordering and lockstep check.
- `README.md` — "Install", which states what to `go get`.

## The one rough edge

Until the root has a pushed tag, `go mod tidy` cannot run in a submodule.
`go mod tidy` ignores `go.work` by design — a module has to resolve its
dependencies the way a consumer would — so a `require` naming a version that
does not exist fails there and only there. Building, testing and vetting all
work from the workspace with no tag at all.

This does not disappear after the first tag, as this section originally claimed.
Every release that adds a package to the root reopens the window, because a
satellite resolves the library from the *previous* tag.

`make tidy` handles it without putting a `replace` in anything published: it adds
one, tidies, and drops it again, so the directive never survives into a released
`go.mod`. That fixes `go.mod`. It cannot mint a `go.sum` hash, which only a
published version can — so `GOWORK=off go build` in a satellite stays broken
until the first tag exists, and that half is waited for rather than worked
around.

## Proven by

Nothing in the test suite asserts the module layout — `go build` is the check,
and it is not a test that can be pointed at. `make unit` and `make vet` loop over
every module, so a package that drifts into the wrong one stops compiling.

Two checks are worth running by hand, and both are cheap:

```
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... \
  | grep -v '^github.com/frostgrove/vv'
```

run from the repository root, must print nothing. `make check-deps` is that
command, and it runs the same question against each satellite so a binding
cannot pick up the other binding's framework.

**The command this file used to give was broken and never had pass/fail
meaning.** It ended in `grep '\.'`, which matches standard-library paths — a
clean tree printed seventeen lines of `crypto/internal/entropy/v1.0.0` and
`vendor/golang.org/x/crypto/...`. `.Standard` is the question that was meant.
[[D-016]] carried the same defect and has the same fix.

**And `go build` is not the check this section says it is.** Under `go.work` the
build list is the union of every member, so a root-module package importing a
satellite's dependency compiles, vets and tests green while `go.mod` stays
empty — a consumer then gets `no required module provides package`. `make unit`
and `make vet` cannot see it. That is why the check is a Makefile target rather
than a sentence.

The dependency isolation itself is visible in the `go.mod` files: gin's
transitive set (sonic, validator/v10, quic-go, protobuf, mongo-driver) appears
in `crud/http/crudgin/go.mod` and nowhere else.

## See also

[[D-016]] [[D-034]] [[D-015]] [[D-018]] [[D-020]]
