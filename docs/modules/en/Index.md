# Modules

One page per importable package: what it does, what you get, and how to wire it.
Declarative and task-oriented — the *why* lives in [decisions/](../../decisions/Index.md)
and the *where in the source* lives in [flows/](../../flows/Index.md).

Read the [README](../../../README.md) first if you have not. These pages are the
long form of it.

## The map

```
                      your model struct
                              │
      ┌───────────────────────┼───────────────────────┐
      ▼                       ▼                       ▼
   crud ─────────────► crud/sqlrepo ──────────► decorators
   the contract        speaks SQL           specs · security · faults
      │                       │                       │
      │                       ▼                       │
      │                  adapter/*  ◄─────────────────┘
      │              crudsql · crudpgx
      ▼
   errs ──► sqlerr ──► sqlfault ──► catalog ──► probe
   the error contract, then the four layers that fill it

   query ──► port ──► porthttp ──► crudhttp ──► crudnet · crudfiber · crudgin
                 │          └──────────► authhttp ──► authnet · authgin · authfiber
                 └──► crudgrpc
   one request document, classified once, spelled per transport —
   and one status table, which is why porthttp is not under crud/

   remote ◄── remotehttp.Transport · crudgrpc.Transport
   the same thing backwards: another service's resource, as a repository

   vvdb ──► dbpgx · database/sql ──► crudsql · crudpgx
   one config file, then the handle the application hands over

   auth ──► authjwt · apikey ──► authnet · authgin · authfiber · authgrpc
                                            │
                                            ▼
                                    security.Gate
   who is calling, established once at the door and read by every policy
```

## Core — you always import these

| Module | Import | What it is |
|---|---|---|
| [crud](crud.md) | `vv/crud` | The contract: model metadata, `Opt`, options, predicates, relations, pagination, the two-method executor seam |
| [crud/sqlrepo](sqlrepo.md) | `vv/crud/sqlrepo` | The plain repository. `Define`, `Bind`, and the layer that speaks SQL |

## Decorators — wrap the repository, all optional

| Module | Import | What it is |
|---|---|---|
| [specs](specs.md) | `vv/crud/decorators/specs` | JPA Specifications, the Criteria API and a compile-checked metamodel |
| [security](security.md) | `vv/crud/decorators/security` | Row-level scope, authorization, per-entity inspection |
| [faults](faults.md) | `vv/crud/decorators/faults` | Turns one refused write into every violation the payload caused |

## Auth — who the caller is, and what they may do

| Module | Import | What it is |
|---|---|---|
| [auth](auth.md) | `vv/auth` | The contract: `Principal`, `Role`, `Permission`, `Credential`, `Authenticator`, `Guard`, the context key, the 401 |
| [authjwt](authjwt.md) | `vv/auth/authjwt` | **Module** — JWT verification, generic over *your* claims struct; HMAC, RSA, ECDSA, EdDSA, JWKS |
| [apikey](apikey.md) | `vv/auth/apikey` | An `Authenticator` over a shared secret, compared in constant time |
| [authhttp](authhttp.md) | `vv/auth/http/authhttp` | The HTTP half of the middleware: the renderer and the refusal |
| [authnet](authnet.md) | `vv/auth/http/authnet` | The `net/http` middleware. Stdlib, so it ships in the library |
| [authgin](authgin.md) | `vv/auth/http/authgin` | **Module** — the Gin middleware |
| [authfiber](authfiber.md) | `vv/auth/http/authfiber` | **Module** — the Fiber v3 middleware |
| [authgrpc](authgrpc.md) | `vv/auth/rpc/authgrpc` | **Module** — the gRPC unary and stream interceptors |

`auth` is deliberately **not** on the contract manifest. It is a package with
two implementations of its own interface, which is the normal case rather than
the exception ([[D-048]], [[D-055]]).

## The request — one document, four transports

| Module | Import | What it is |
|---|---|---|
| [query](query.md) | `vv/crud/query` | The wire DSL: one JSON document → `crud.Options`, bounded for untrusted input |
| [port](port.md) | `vv/port` | The transport-neutral half: eight commands, `Service`, `Mapper`, the path chain |
| [porthttp](porthttp.md) | `vv/port/porthttp` | The HTTP projection of the error contract: the status table, the envelope, the `Renderer` seam, the body decode. Every subsystem's, not CRUD's |
| [crudhttp](crudhttp.md) | `vv/crud/http/crudhttp` | What is HTTP *and* CRUD: the request shapes, the model hop, and the forwarders over `porthttp` |
| [crudnet](crudnet.md) | `vv/crud/http/crudnet` | A full CRUD API on `net/http`. Stdlib, so it ships in the library |
| [crudfiber](crudfiber.md) | `vv/crud/http/crudfiber` | **Module** — the same API on Fiber v3 |
| [crudgin](crudgin.md) | `vv/crud/http/crudgin` | **Module** — the same API on Gin |
| [crudgrpc](crudgrpc.md) | `vv/crud/rpc/crudgrpc` | **Module** — the same API on gRPC, over `google.protobuf.Struct` |
| [remote](remote.md) | `vv/remote` | The consuming half: another service's resource, held as a `port.Repository` |
| [remotehttp](remotehttp.md) | `vv/remote/remotehttp` | The HTTP client transport: a `remote.Call` becomes a request |

## The error subsystem — what a failed write tells the client

| Module | Import | What it is |
|---|---|---|
| [errs](errs.md) | `vv/errs` | The contract: `Code`, `Kind`, `Path`, `Violation`, `Fault`, the SPI, message catalogues. Stdlib only |
| [sqlerr](sqlerr.md) | `vv/errs/sqlerr` | A driver error becomes a code. Four dialect tables, keyed three different ways |
| [sqlfault](sqlfault.md) | `vv/crud/sqlfault` | The tree walk, the integrity gate and fault assembly. What `WithFaults` takes |
| [catalog](catalog.md) | `vv/crud/catalog` | Per-handle schema introspection, four dialects. Read once, answered from memory |
| [probe](probe.md) | `vv/crud/probe` | One extra statement finds every *other* violation the same payload caused |

## Connecting — the application's side of the handle

| Module | Import | What it is |
|---|---|---|
| [vvdb](vvdb.md) | `vv/vvdb` | One configuration → a DSN, or a `*sql.DB` with the pool sized. Four engines, stdlib only |
| [dbpgx](dbpgx.md) | `vv/vvdb/dbpgx` | **Module** — the same configuration, a `*pgxpool.Pool` |

Nothing here is reachable from the repository seam: the application opens the
connection and hands it to an adapter below ([[D-057]]).

## Adapters — how vv reaches your database

| Module | Import | What it is |
|---|---|---|
| [crudsql](crudsql.md) | `vv/crud/adapter/crudsql` | `database/sql` — and therefore ent, gorm, sqlx, sqlc, bun, squirrel |
| [crudpgx](crudpgx.md) | `vv/crud/adapter/crudpgx` | **Module** — pgx v5, with `COPY` bulk insert |
| [crudtest](crudtest.md) | `vv/crud/crudtest` | An in-memory source: unit-test a repository with no database at all |

## Tooling

| Module | Import | What it is |
|---|---|---|
| [cmd/vv](vv-cli.md) | `vv/cmd/vv` | Generates the update DTO, the metamodel and — with `-adapter` — the whole resource |
| [vvflag](vvflag.md) | `vv/utils/vvflag` | Read one typed flag out of `os.Args` before `flag.Parse` owns it |
| [vvcfg](vvcfg.md) | `vv/utils/vvcfg` | **Module** — load a YAML config into a struct, with validation |

## What "module" means here

The published root module `github.com/shardit-io/vv` has **no third-party
requirement at all**. Anything that would add one is a module of its own in the
same repository, so you download the Fiber binding or the Gin binding or
neither, and pgx only if you use pgx ([[D-033]]).

Versions move in lockstep: the library and every satellite are tagged together,
so `@v0.1.0` means the same thing everywhere. No `replace` is ever needed.

The **contract manifest** is `crud`, `crud/crudtest`, `crud/query`, `errs`,
`errs/sqlerr`, `port` and `port/porthttp`. They are the interfaces a third party
implements, they import only the standard library and each other, and the list is
closed ([[D-048]]). `make check` enforces it rather than this sentence doing so —
by exact package path and non-recursively, because `crud/` has a subtree now and
a prefix match would let all of it in ([[D-058]]).

## See also

- [usage-guides/ent.md](../../usage-guides/ent.md) — adopt an ent model as-is
- [usage-guides/gorm.md](../../usage-guides/gorm.md) — adopt a gorm model as-is
- [`_examples/`](../../../_examples/) — one runnable program per stack
- [roadmaps/Roadmap.md](../../roadmaps/Roadmap.md) — what is left
