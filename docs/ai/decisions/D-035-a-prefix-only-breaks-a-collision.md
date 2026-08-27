# D-035 — A package is named for what it is; a prefix only breaks a collision

**Status:** accepted
**Invariant:** a package takes a prefix only where the bare name would collide, and the prefix names the subsystem the package belongs to — the project only where the colliding name *is* the subsystem and there is nothing else to prefix with.

## The decision

Two rules, and the second is the one that is easy to get wrong.

**A package is named for what it is.** `crud`, `query`, `errs`, `port`,
`catalog`, `probe`, `remote`. No prefix, because nothing collides.

`remote` is the one that had a tempting alternative. What it holds is a client,
and `client` is what a first draft called it — but a package named `client`
imported beside `ent.Client` and `http.Client` is the reader's collision the
`sqlfault` paragraph below already covers, and `client.New` says nothing at a
call site. `remote.New` says where the repository is, which is the only thing
about it that differs from a local one.

**A prefix appears only to break a collision, and it names the subsystem.**
`crudfiber` is the CRUD binding for Fiber. `crudpgx` is the CRUD adapter for
pgx. `vvflag` is the flag reader, prefixed because the standard library owns
`flag`.

So the grid runs subsystem × library:

| | fiber | gin | net/http | pgx | database/sql | grpc |
|---|---|---|---|---|---|---|
| **crud** | `crudfiber` | `crudgin` | `crudnet` | `crudpgx` | `crudsql` | `crudgrpc` |
| **auth** | `authfiber` | `authgin` | `authnet` | — | — | `authgrpc` |
| **port** | — | — | `porthttp` | — | — | — |
| **remote** | — | — | `remotehttp` | — | — | — |
| **i18n** | `i18nfiber` | `i18ngin` | — | — | — | — |

[[D-058]] is where that grid became the directory tree: the row is the top-level
directory and the column is the second level, so `crudgin` is at
`crud/http/crudgin` and `authgin` at `auth/http/authgin`. The prefix survives the
nesting for the reason it existed in the first place — a consumer mounting both
imports the two in one file.

`port` and `remote` hold a single cell each and hold it for opposite reasons.
`porthttp` is the HTTP projection every subsystem answers through, so there is
one and it is not CRUD's ([[D-059]]). `remotehttp` is the client transport: there
is one HTTP client and not three, because a consumer calling out uses `net/http`
whatever it serves with — so the fiber and gin cells have nothing to hold.

**The gRPC client transport is the exception, and it is an exception.**
`crudgrpc.Transport` stays in `crud/rpc/crudgrpc` rather than moving to a
`remote/remotegrpc` beside `remotehttp`, because `remote` is in the root module
and may not import grpc: the move would cost a whole module for one file.
[[D-053]]'s old rule — *a transport lives with the binding it calls* — held while
the tables a client reads backwards lived beside the binding. They are
`porthttp`'s now, so the HTTP transport moved and the gRPC one could not. The
asymmetry is recorded rather than smoothed over: `remotehttp` is where the HTTP
client lives, `crudgrpc` is where the gRPC one lives, and the reason is the
module boundary, not the protocol.

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

**The project prefix has one legitimate use, and `vvflag` was already it.**
Where the bare name is a word the package has to leave to somebody else — the
standard library's `flag`, or `db`, which is the name of the handle in nearly
every Go program that has one — a prefix is required and there is no subsystem
to prefix with, because the subsystem *is* that word. The project is what is
left. This is the reading `vvflag` has always relied on while the list below
appeared to forbid it; `vvdb` is the second case and the one that made the
contradiction visible ([[D-057]]).

It stays narrow. Two things have to be true at once: the bare name collides, and
there is no subsystem name to use instead. `vvfiber` fails the second test —
the subsystem is CRUD, and `crudfiber` says so.

## What it forbids

- Do not name a package after the project, **except** in the one case above:
  the bare name is taken and no subsystem name can take its place. `vvfiber`,
  `vvgin`, `vvcrud` are all wrong — they say who wrote it, which the import path
  already said, and `crudfiber` was available.
- Do not add a prefix to a package that does not collide. `vvcrud` and
  `vvquery` buy nothing.
- Do not take a subsystem × library cell for a different subsystem. If
  `crudfiber` grows something that is not CRUD, it moves.
- Do not rename a package after the first tag without a deprecation cycle. Until
  then a rename is a `sed`; afterwards it is every consumer's import block.

## Where it lives

- `crud/http/crudfiber/`, `crud/http/crudgin/`, `crud/http/crudnet/` — the three
  HTTP bindings; `auth/http/authfiber/`, `auth/http/authgin/`,
  `auth/http/authnet/` — the same three cells one row down.
- `crud/adapter/crudsql/`, `crud/adapter/crudpgx/` — the two adapters.
- `port/porthttp/` — the `port` × `net/http` cell ([[D-059]]).
- `remote/remotehttp/` — the `remote` × `net/http` cell.
- `utils/vvflag/` — prefixed against the standard library.
- `utils/vvdb/` — prefixed against `db`, the variable name, with `utils/vvdb/dbpgx` under
  it taking no prefix because nothing is called `dbpgx` ([[D-057]]).
- `crud/sqlfault/` — prefixed against `errs.Fault`, which every file in it names.
- `remote/` — unprefixed, and named for where the repository is rather than for
  what a caller does with it.
- `utils/vvcfg/` — `cfg` alone is too vague to be a package name on its own.
- `scripts/checks.sh:TIER0` — the contract manifest the naming rule sorts, with
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
`port` import `crud/sqlrepo`, and again by making `port/porthttp` import it, and
watching each fail. Both arms had to be re-armed at [[D-058]]: the manifest was
matched by prefix, and a prefix match lets `crud/sqlrepo` in under `crud` the
moment `crud/` has a subtree.

## See also

[[D-033]] [[D-034]] [[D-016]] [[D-053]] [[D-057]] [[D-058]] [[D-059]]
