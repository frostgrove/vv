# D-058 — The top-level axis is the subsystem; the transport is the second level

**Status:** accepted
**Invariant:** a package's directory is decided by which subsystem it belongs to first and by which library it binds second. A new transport for an existing subsystem adds a directory inside that subsystem; it never adds a directory at the root.

## The decision

The tree was laid out by transport: `http/` held `crudhttp`, `crudnet`,
`crudfiber`, `crudgin`, `authhttp`, `authnet`, `authgin`, `authfiber`; `rpc/`
held `crudgrpc` and `authgrpc`; `adapter/` held both adapters; `repo/` held the
repository and its decorators. Fourteen directories of code at the root, and no
directory that answered "where does subsystem X live".

It is laid out by subsystem now. [[D-035]]'s grid is unchanged — the same
subsystem × library cells, with the same names in them. Only the nesting order
is reversed:

```
was:  http/crudgin  http/authgin       one row of the grid, spread across columns
is:   crud/http/crudgin  auth/http/authgin   the column gathered under its row
```

Six directories of code at the root — `crud`, `auth`, `port`, `remote`, `errs`,
`utils` — plus `cmd`, `internal`, `docs`, `test` and `_examples`.

Four consequences, and each is checkable:

**A package keeps its prefix even where the path repeats it.**
`crud/http/crudfiber` holds `package crudfiber`, not `package fiber`: the bare
name does not compile beside `github.com/gofiber/fiber/v3`, and a consumer
mounting CRUD routes and auth middleware on Gin imports `crudgin` and `authgin`
in one file. The standard library reads the same way — `net/http/httptest`.

**A directory can be a package and a subtree at once.** `crud/` is still
`package crud` and is now also the root of a subtree, exactly as `net` is a
package with `net/http` beneath it, and as `errs` + `errs/sqlerr` and `auth` +
`auth/apikey` already were. The import path `github.com/frostgrove/vv/crud` did
not change, which is 190 import sites left alone.

**The shared half stays outside every subsystem.** `port/` is the neutral half of
a binding for *any* subsystem, so it is not under `crud/`. That is not a
technicality: the auth middleware imported a package called `crudhttp` before
this, and [[D-059]] is the dependency that was hiding behind the old layout.

**Depth is not the goal.** A wrapper directory appears where three or more
packages of one kind sit under it — `crud/http/`, `crud/decorators/` — and not
for symmetry.

## Why

**Because a subsystem was spread across five directories and nothing said so.**
Auth was `auth/`, `auth/apikey/`, `auth/authjwt/`, `http/auth{http,net,gin,fiber}/`
and `rpc/authgrpc/`. Reading it whole meant knowing it had been filed by
transport. CRUD was worse: ten entry points — `crud/`, `repo/`, `query/`,
`port/`, `adapter/`, `catalog/`, `probe/`, `sqlfault/`, `http/`, `rpc/`.

**Because the transport axis made every new subsystem pay every transport.**
[[D-035]]'s grid already carries an `i18n` row with `i18nfiber` and `i18ngin` in
it. On the old layout that row would have added two directories to `http/`, zero
directories that say where i18n lives, and no place a reader could see that the
two were one subsystem.

**Because the move was free exactly once.** There is no tag. Every import is
first-party, so the whole thing is `git mv` plus a sweep. After `v0.1.0` the same
change costs a deprecation cycle — [[D-035]] says so in as many words, and the
roadmap repeats it. This decision is as much about *when* as about *what*.

**What it costs.** Longer import paths, and a `crud/http/crudfiber` that reads as
a stutter until you remember why the prefix is there. That is the price of one
directory per subsystem, and the alternative — dropping the prefix — is a build
failure in the binding's own files.

## `basic` became `sqlrepo`

The repository package was `repo/basic`. "Basic" names a rank, and the rank is
borrowed: basic relative to what? `auth` is basic, `errs` is basic, `crud` more
so. The name distinguished nothing.

There are three implementations of the `crud.Core` seam and two of them were
already named for where the data is:

| implementation | where the rows are | package |
|---|---|---|
| `crudtest` | in this process, in memory | `crudtest` |
| `repo/basic` → | in the database, behind SQL | **`sqlrepo`** |
| `remote.Resource` | in another service, behind the network | `remote` |

`repository.go` opens by calling itself "the SQL implementation of `crud.Core`".
The package name now repeats what the file already said.

Rejected: `sql` (does not import beside `database/sql`, which is the collision
[[D-035]] exists for), `table` (reads best at the call site but names the input
rather than the thing — the package is a repository, not a table), `store` and
`sqlstore` (the same fault as `basic`: `crudtest` and `remote` are stores too),
`crudsql` (taken by the `database/sql` adapter).

No exported symbol changed. `Define`, `TryDefine`, `New`, `Blueprint`,
`Setting`, `Scope`, `RelationScope`, `SoftDelete`, `DefaultLimit`, `MaxLimit`,
`DefaultSort`, `PreloadDepth`, `UnstablePagination` and `DefaultPageSize` are
what they were; only the qualifier in front of them moved.

Nothing else was renamed. `crud`, `query`, `errs`, `port`, `catalog`, `probe`,
`remote`, `crudtest`, `sqlfault`, `crudsql`, `crudpgx`, `crudfiber`, `crudgin`,
`crudnet`, `crudgrpc`, `authjwt` and `apikey` are each already named for what
they are, and [[D-035]] argues the hard ones by name. `decorators/` was left
alone with a reservation on the record: the word names a pattern, and the type in
the code is `crud.Middleware` assembled by `crud.Chain`, so the repository holds
two words for one thing. `crud/middleware/` would be truer to the code;
`decorators/` is truer to [[D-030]], which has the word in its title.

## `utils/` is the one name chosen for what it is not

`utils/vvflag` reads a flag, `utils/vvcfg` loads a config file, `utils/vvdb`
turns that config into a DSN or a `*sql.DB`, and `utils/vvgoose` runs the
application's migration command. All four are the consumer's
application plumbing. None is a subsystem of this library, so none has a row in
the grid.

A directory called `utils` collects the whole repository unless it has a
boundary, so it has one, and it is a single line: **nothing under `utils/`
imports `crud/`, `auth/`, `port/` or `remote/`.** A package that needs to is not a
utility; it belongs to the subsystem it reached for and moves there.

`make check-utils` is that line rather than this paragraph being it. It lists
both halves separately because they need different commands — `vvflag` and
`vvdb` are packages of the root module; the other packages with their own
`go.mod` are invisible to a root-module `go list`.

`vvdb` is the case that shows the line is a test rather than a size limit. It has
its own name argument, its own flow and a satellite module of its own
(`utils/vvdb/dbpgx`), which is more apparatus than either of its neighbours — and
it still belongs here, because [[D-057]]'s forbid list already says it may not
import `crud` or `errs`, may not be called from anywhere inside the repository
path, and may not return a `crud.Source`. A package forbidden all of that is not
a subsystem of the library; it is what the library is handed. How many packages
sit under `utils/` is not what the boundary measures.

## What it forbids

- Do not add a transport directory at the root. A second Fiber integration goes
  to `<subsystem>/http/`, never back to a top-level `http/`.
- Do not drop a package's prefix because the path already carries the subsystem.
  `crud/http/crudfiber` is `package crudfiber`. The collision the prefix breaks
  is in the binding's own import block and the path does not reach it.
- Do not move `port/` or `remote/` under `crud/`. Both serve every subsystem;
  `port` under `crud` is how the auth middleware came to depend on the
  repository in the first place ([[D-059]]).
- Do not put a package under `utils/` that imports a subsystem.
- Do not match the contract manifest by prefix. `Makefile:TIER0` lists exact
  package paths and lists them non-recursively, because `crud` as a prefix now
  matches `crud/sqlrepo` and `./crud/...` now lists thirty packages. Either
  mistake leaves the target green and meaningless.
- Do not read [[D-016]] as covering the subtree. It is about the package `crud`.
- Do not reverse this without a new decision. Going back to the transport axis
  after the first tag costs every consumer's import block.

## Where it lives

The whole tree, and `CLAUDE.md`'s *Layout* section is the reader's copy of it:

- `crud/` — `crudtest/`, `query/`, `sqlrepo/`, `decorators/{specs,security,faults}/`,
  `adapter/{crudsql,crudpgx}/`, `catalog/`, `probe/`, `sqlfault/`,
  `http/{crudhttp,crudnet,crudfiber,crudgin}/`, `rpc/crudgrpc/`.
- `auth/` — `apikey/`, `authjwt/`, `http/{authhttp,authnet,authgin,authfiber}/`,
  `rpc/authgrpc/`.
- `port/` — `porthttp/` ([[D-059]]).
- `remote/` — `remotehttp/`.
- `errs/` — `sqlerr/`. Not moved: `Makefile:TIER0_SEALED` holds it and the
  roadmap plans it as its own module, and a move under `crud/` or `port/` would
  cost both.
- `utils/` — `vvflag/`, `vvcfg/`, `vvgoose/`, and `vvdb/` with `vvdb/dbpgx/` under it
  ([[D-057]]).
- `cmd/vv/`, `internal/codegen/` — Go convention, not this decision's to move.
- `Makefile:TIER0`, `:TIER0_STDLIB` — the two arms re-armed for a tree with
  subtrees under manifest names.
- `Makefile:SUBSYSTEMS`, `:check-utils` — the `utils/` boundary, enforced rather
  than stated.
- `cmd/vv/main.go:73` (`-specs`) and `internal/codegen/codegen.go:513-515`
  (`DefaultPortPkg`, `DefaultErrsPkg`, `DefaultNetPkg`) — the generator's
  defaults are string literals and moved with the packages they name.

## Proven by

- `make check-utils` — no package under `utils/` reaches `crud`, `auth`, `port`
  or `remote`, which is what lets `vvdb` sit there. Verified in both halves:
  importing `crud` from `utils/vvdb` (root module) and `port` from
  `utils/vvdb/dbpgx` (its own module) each fail with the offender named. The
  second is the one worth checking — an arm that listed only `./utils/...` would
  have passed it.
- `make check-tiers`, and the thing that matters is that it can still fail.
  Verified three ways: importing `crud/sqlrepo` from `port` (the manifest arm
  names it), importing it from `port/porthttp` (the same arm, the cell added at
  [[D-059]]), and importing `errs` from `crud` (the stdlib arm). Under the old
  prefix match the first two passed, which is why the arms changed with the tree
  rather than after it.
- `make generate` — byte-identical output after the move, run twice. The
  generator writes import paths it holds as string literals, so a missed default
  would have shown up as a stale file rather than as a compile error.
- `test/codegen/codegen_test.go` — `assertDirective` compares the `//go:generate`
  lines byte for byte against copies held in the test. The copies and the
  directives moved together; had either been missed the integration run would
  have said so, which is what that assertion is for.
- `make examples` — the seven example stacks build, vet and test. They are the
  closest thing to a consumer in the tree, and they compile with nothing changed
  but their import blocks.
- `make integration`, twice in a row, on PostgreSQL, MySQL and MariaDB. Nothing
  here changes behaviour, so a diff in the suite would have meant the move was
  not the move.


## The one asymmetry the axis does not explain

The HTTP client transport is `remote/remotehttp`. The gRPC client transport is
`crud/rpc/crudgrpc`, beside the server it calls — which the axis above would put
under `remote/` too.

The reason is the module graph and not the axis. `remote` is in the root module,
the root module takes no third-party requirement ([[D-036]]), and a gRPC client
requires grpc. So `remote/remotegrpc` would be a module of its own containing one
file, and a consumer calling a gRPC resource would download it in addition to
`crudgrpc`, which they already have and which already requires grpc. The
asymmetry costs a reader one surprise; the symmetry would cost every consumer a
module.

[[D-045]] points here for this record, and until now it pointed at nothing.

## See also

[[D-035]] [[D-059]] [[D-016]] [[D-033]] [[D-045]] [[D-048]] [[D-051]] [[D-053]] [[D-057]]
