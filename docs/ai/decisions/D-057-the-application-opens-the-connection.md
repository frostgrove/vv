# D-057 — The application opens the connection; nothing in the seam does

**Status:** accepted
**Invariant:** `vvdb` imports no other package of this library, is reachable from nothing the repository calls, and hands back a driver handle the caller owns — the hop into vv stays a line the caller writes.

## The decision

`vvdb` turns one configuration struct into a connection string, and into a
`*sql.DB` or a `*pgxpool.Pool`. It is the application's plumbing, not the
library's, and three rules keep it that way.

**It imports nothing of vv.** Not `crud`, not `errs`. A service with no vv in it
can take `vvdb` on its own, and `make check-tiers` is not asked to make an
exception for it.

**Nothing in the repository path reaches it.** No `Define`, no `Bind`, no
decorator, no adapter knows it exists. Delete the package and everything else in
this repository still compiles.

**It returns a handle and stops.** The caller writes the next line:

```go
pool := dbpgx.MustConnect(ctx, &cfg.DB)
repo := Products.Bind(crudpgx.Open(pool))
```

The second line is not missing an abstraction. `vvdb` deliberately exports no
`Source`, no `Bind` and nothing else that would let the two collapse into one
call.

## Why

**Because "vv does not own your connection or transaction" is the first claim
this library makes, and a `MustConnect` sitting next to `Define` would look like
it had been withdrawn.** The claim is what makes the seam work with ent, gorm,
sqlx and a framework-owned transaction. It survives here because the connection
`vvdb` opens belongs to the application from the moment it exists: it is the
application that closes it, that decides its lifetime, and that chooses whether
vv ever sees it. Writing that as one visible line costs a line and states the
whole arrangement.

**Because the alternative was tried in the design and reads worse.** A
`dbpgx.MustSource(ctx, cfg)` returning a `crud.Source` is one line shorter and
makes `vvdb` part of vv: it would import `crud`, it would need the dialect and
the classifier, and the package that a service without vv could have adopted
would stop being adoptable.

**Because the boilerplate it removes is not boilerplate.** Every example in this
repository held a DSN as a string constant, and the corpus assembled a third set
by hand. The difference between the four engines is small enough to look
copy-pasteable and sharp enough to connect to the wrong place: a password that
one parser reads as a delimiter, a parameter with a slash in it that moves where
the database name ends, `connect_timeout=0` meaning "forever". Those belong in
one place with tests, not in seven `main` functions.

## The configuration, and the two rules it is built on

**One struct describes four engines.** The keys an operator types are the same
for PostgreSQL, MySQL, MariaDB and SQLite; what differs is the string built from
them. `sslmode` is spelled in PostgreSQL's vocabulary everywhere and translated,
because one document has to spell it one way.

**The escape hatch is whole or absent.** `dsn:` may be set, and then every field
it would override must be empty. Accepting both and preferring one is the shape
of failure this library refuses everywhere else: a configuration with two
sources of truth, one of them silently losing ([[D-013]], [[D-053]]).

**A read/write pair makes option ownership explicit.** A bare pgx `Option` is
accepted by the single-pool helper. `ConnectReadWrite` instead accepts
`Common`, `Primary`, and `Replica` declarations. A tracer may deliberately be
common; a credential, IAM token provider, or role-changing hook belongs to one
side. Reusing one undifferentiated option list could otherwise put a replica
identity on the writable pool. The declarations snapshot their caller-owned
slices and common options run before side-specific ones.

## The name

`vvdb` carries the project as a prefix, which [[D-035]]'s "What it forbids"
list rules out in as many words. That list is amended in the same change rather
than worked around, and the amendment is narrow: a project prefix is allowed
where the bare name is a word the package must leave to somebody else and there
is no subsystem to prefix with instead. `vvflag` was already in exactly that
position — the standard library owns `flag` — and was already listed as
legitimate. `db` is the same case with a variable instead of a package: it is
the name of the handle in nearly every Go program that has one, and
`db := db.MustOpen(cfg)` shadows the package for the rest of the function.

The child package needs no prefix at all: nothing is called `dbpgx`, so it is
`dbpgx` and not `vvdbpgx`. Not `vvpgx` either — that would spend the project
prefix where no collision exists.

**Where the pair sits was settled by [[D-058]] and does not change any of the
above.** It is `utils/vvdb` and `utils/vvdb/dbpgx`. The name is unaffected: the
prefix answers "what may this package be called", the directory answers "what is
this a part of", and `utils/` is the answer to the second because `vvdb` is the
consumer's plumbing rather than a subsystem of the library. Its own forbid list
below is what makes it fit there — a package that may not import `crud` or `errs`
is exactly what `utils/` is allowed to hold.

## What it forbids

- Do not import `crud` or `errs` from `vvdb`. The day it needs one of them, it
  has stopped being the application's plumbing.
- Do not add a function that returns a `crud.Source`, a `Repo`, or anything else
  that removes the caller's line. That line is the decision.
- Do not call `vvdb` from anywhere inside the repository path, a decorator, an
  adapter or a binding. Nothing in vv opens a connection.
- Do not let an engine, a driver name or an `sslmode` be guessed. The set is
  closed and an unknown value is refused ([[D-013]]).
- Do not put the DSN in an error message. It carries the password.
- Do not pass credentials, IAM providers, or role-changing hooks through
  `dbpgx.Common`; declare the pool identity with `Primary` or `Replica`.
- Do not grow `vvdb` a dependency. `database/sql` is the standard library and
  the driver is the consumer's blank import; anything else is a module, and
  `utils/vvdb/dbpgx` is the first of them ([[D-033]], [[D-051]]).
- Do not move it back out of `utils/`. The forbid list above *is* the `utils/`
  boundary [[D-058]] states, arrived at from the other direction: a package that
  may not import `crud`, may not be called from the repository path and may not
  grow a dependency is not a subsystem of this library, whatever its own module
  count says. `make check-utils` is where the first of those forbids stopped
  being a sentence and became an arm.

## Where it lives

- `utils/vvdb/doc.go` — the boundary, stated where a reader of the package meets it.
- `utils/vvdb/config.go`, `utils/vvdb/dsn.go`, `utils/vvdb/open.go` — the three levels.
- `utils/vvdb/dbpgx/` — the one engine that is not `database/sql`.
- `docs/ai/flows/FL-021` — the path, and where the escaping lives.
- `docs/ai/usecases/UC-021` — what the author is trying to do.
- `_examples/pgx-fiber`, `_examples/sql-nethttp`, `_examples/gorm-mysql-gin` —
  the three levels, one per example, each showing the handover line.

## Proven by

The import rule is mechanical and checked:

```
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./utils/vvdb
```

prints `github.com/frostgrove/vv/utils/vvdb` and nothing else — the package itself,
because `-deps` includes it. Anything on a second line is a violation.
`make check-deps` covers the third-party half for the whole root module; this is
the first-party half, and it grows a line the moment `vvdb` imports `crud`.

That the rest of the library does not depend on it is the same command backwards
and prints nothing at all:

```
go list -deps -f '{{.ImportPath}} {{join .Deps " "}}' ./... \
  | grep -v '^github.com/frostgrove/vv/utils/vvdb' | grep vvdb
```

The filter is on the package's own path rather than on a directory list, so it
keeps working when the tree is rearranged.

The behaviour is pinned by `utils/vvdb/*_test.go`, and the escaping — the part a
string comparison cannot check — by `test/dsn/dsn_test.go`, which parses what
`vvdb` writes with pgx and go-sql-driver. `test/integration/vvdb_test.go` opens
three live servers from one shape of config, with a wrong-password control
beside it.

## See also

[[D-033]] [[D-035]] [[D-021]] [[D-013]] [[D-032]] [[D-051]] [[D-058]]
