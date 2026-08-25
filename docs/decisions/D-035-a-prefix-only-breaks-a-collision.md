# D-035 — A package is named for what it is; a prefix only breaks a collision

**Status:** accepted
**Invariant:** a package takes a prefix only where the bare name would collide, and the prefix names the subsystem the package belongs to, never the project.

## The decision

Two rules, and the second is the one that is easy to get wrong.

**A package is named for what it is.** `crud`, `query`, `errs`, `port`,
`catalog`, `probe`. No prefix, because nothing collides.

**A prefix appears only to break a collision, and it names the subsystem.**
`crudfiber` is the CRUD binding for Fiber. `crudpgx` is the CRUD adapter for
pgx. `vvflag` is the flag reader, prefixed because the standard library owns
`flag`.

So the grid runs subsystem × library:

| | fiber | gin | net/http | pgx | database/sql | grpc |
|---|---|---|---|---|---|---|
| **crud** | `crudfiber` | `crudgin` | `crudnet` | `crudpgx` | `crudsql` | `crudgrpc` |
| **i18n** | `i18nfiber` | `i18ngin` | — | — | — | — |

## Why

**Because the ecosystem convention is the subsystem, not the project — and it is
easy to misread as the project.** OpenTelemetry's contrib packages are
`otelgin`, `otelhttp`, `otelfiber`, and `otel` there looks like the project
prefix. It is not: OpenTelemetry is single-purpose, so its project name and its
subsystem name are the same word. A framework with several subsystems has to
generalise the subsystem.

An earlier draft of `ROADMAP-framework.md` proposed unifying every satellite on
the project prefix — `vvfiber`, `vvgin`, `vvpgx` — on exactly that misreading.
The owner caught it, and the argument is short: the first time a second
subsystem needs a Fiber integration there is nowhere to put it, because
`vvfiber` has already claimed the whole project × Fiber cell for whichever
subsystem happened to get there first.

**The collision is mechanical, not stylistic.** A package named `fiber` cannot
be imported alongside `github.com/gofiber/fiber/v3`, and the binding's own files
import both. Same for `flag` and the standard library. That is the whole reason
a prefix exists; where there is no collision there is no prefix.

**Why not prefix everything for consistency.** Because a prefix that carries no
information is noise in every import block and every call site, and because
`crud`, `query` and `errs` are the names a reader already expects for those
things.

**A collision can be a reader's rather than the compiler's, and `sqlfault` is
the case.** Phase 3 added `sqlfault`, the layer that turns a driver error into
an `errs.Fault`. `fault` alone collides with nothing at compile time, so the
strict reading of the rule above says drop the prefix. It is kept because every
file in that package imports `errs` and returns a `*errs.Fault`, so a bare
`fault.New` beside `errs.Fault` makes a reader hold two meanings of one word.
The prefix names what it is about — SQL — and it is the same construction as
`errs/sqlerr` one layer down. This is the one place the rule is read as *a
prefix only breaks a collision, and a collision of meaning counts*; anything
further from that reading needs its own entry here.

## What it forbids

- Do not name a package after the project. `vvfiber`, `vvgin`, `vvcrud` are all
  wrong: they say who wrote it, which the import path already said.
- Do not add a prefix to a package that does not collide. `vvcrud` and
  `vvquery` buy nothing.
- Do not take a subsystem × library cell for a different subsystem. If
  `crudfiber` grows something that is not CRUD, it moves.
- Do not rename a package after the first tag without a deprecation cycle. Until
  then a rename is a `sed`; afterwards it is every consumer's import block.

## Where it lives

- `http/crudfiber/`, `http/crudgin/`, `http/crudnet/` — the three HTTP bindings.
- `adapter/crudsql/`, `adapter/crudpgx/` — the two adapters.
- `vvflag/` — prefixed against the standard library.
- `sqlfault/` — prefixed against `errs.Fault`, which every file in it names.
- `tools/vvcfg/` — `cfg` alone is too vague to be a package name on its own.
- `Makefile:TIER0` — the contract manifest the naming rule sorts, with
  `TIER0_SEALED` and `TIER0_STDLIB` beside it: the two arms that seal `errs`
  and `crud` against each other, which the manifest arm cannot see because it
  filters every contract package out of its own result ([[D-036]], [[D-016]]).

## Proven by

Nothing in the test suite asserts a package name; the compiler is the check, and
a collision is a build failure rather than a test failure. What is checkable is
the tier the name implies:

```
make check-tiers
```

fails when a contract package imports outside the manifest — verified by making
`query` import `repo/basic` and watching it fail.

## See also

[[D-033]] [[D-034]] [[D-016]]
