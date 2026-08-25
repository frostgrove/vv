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
   crud ─────────────► repo/basic ──────────► decorators
   the contract        speaks SQL           specs · security · faults
      │                       │                       │
      │                       ▼                       │
      │                  adapter/*  ◄─────────────────┘
      │              crudsql · crudpgx
      ▼
   errs ──► sqlerr ──► sqlfault ──► catalog ──► probe
   the error contract, then the four layers that fill it

   query ──► port ──► crudhttp ──► crudnet · crudfiber · crudgin
                 └──► crudgrpc
   one request document, classified once, spelled per transport

   remote ◄── crudhttp.Transport · crudgrpc.Transport
   the same thing backwards: another service's resource, as a repository
```

## Core — you always import these

| Module | Import | What it is |
|---|---|---|
| [crud](crud.md) | `vv/crud` | The contract: model metadata, `Opt`, options, predicates, relations, pagination, the two-method executor seam |
| [repo/basic](basic.md) | `vv/repo/basic` | The plain repository. `Define`, `Bind`, and the layer that speaks SQL |

## Decorators — wrap the repository, all optional

| Module | Import | What it is |
|---|---|---|
| [specs](specs.md) | `vv/repo/decorators/specs` | JPA Specifications, the Criteria API and a compile-checked metamodel |
| [security](security.md) | `vv/repo/decorators/security` | Row-level scope, authorization, per-entity inspection |
| [faults](faults.md) | `vv/repo/decorators/faults` | Turns one refused write into every violation the payload caused |

## The request — one document, four transports

| Module | Import | What it is |
|---|---|---|
| [query](query.md) | `vv/query` | The wire DSL: one JSON document → `crud.Options`, bounded for untrusted input |
| [port](port.md) | `vv/port` | The transport-neutral half: eight commands, `Service`, `Mapper`, the path chain |
| [crudhttp](crudhttp.md) | `vv/http/crudhttp` | The HTTP half: the status table, the envelope, the renderer seam |
| [crudnet](crudnet.md) | `vv/http/crudnet` | A full CRUD API on `net/http`. Stdlib, so it ships in the library |
| [crudfiber](crudfiber.md) | `vv/http/crudfiber` | **Module** — the same API on Fiber v3 |
| [crudgin](crudgin.md) | `vv/http/crudgin` | **Module** — the same API on Gin |
| [crudgrpc](crudgrpc.md) | `vv/rpc/crudgrpc` | **Module** — the same API on gRPC, over `google.protobuf.Struct` |
| [remote](remote.md) | `vv/remote` | The consuming half: another service's resource, held as a `port.Repository` |

## The error subsystem — what a failed write tells the client

| Module | Import | What it is |
|---|---|---|
| [errs](errs.md) | `vv/errs` | The contract: `Code`, `Kind`, `Path`, `Violation`, `Fault`, the SPI, message catalogues. Stdlib only |
| [sqlerr](sqlerr.md) | `vv/errs/sqlerr` | A driver error becomes a code. Four dialect tables, keyed three different ways |
| [sqlfault](sqlfault.md) | `vv/sqlfault` | The tree walk, the integrity gate and fault assembly. What `WithFaults` takes |
| [catalog](catalog.md) | `vv/catalog` | Per-handle schema introspection, four dialects. Read once, answered from memory |
| [probe](probe.md) | `vv/probe` | One extra statement finds every *other* violation the same payload caused |

## Adapters — how vv reaches your database

| Module | Import | What it is |
|---|---|---|
| [crudsql](crudsql.md) | `vv/adapter/crudsql` | `database/sql` — and therefore ent, gorm, sqlx, sqlc, bun, squirrel |
| [crudpgx](crudpgx.md) | `vv/adapter/crudpgx` | **Module** — pgx v5, with `COPY` bulk insert |
| [crudtest](crudtest.md) | `vv/crud/crudtest` | An in-memory source: unit-test a repository with no database at all |

## Tooling

| Module | Import | What it is |
|---|---|---|
| [cmd/vv](vv-cli.md) | `vv/cmd/vv` | Generates the update DTO, the metamodel and — with `-adapter` — the whole resource |
| [vvflag](vvflag.md) | `vv/vvflag` | Read one typed flag out of `os.Args` before `flag.Parse` owns it |
| [vvcfg](vvcfg.md) | `vv/tools/vvcfg` | **Module** — load a YAML config into a struct, with validation |

## What "module" means here

The published root module `github.com/shardit-io/vv` has **no third-party
requirement at all**. Anything that would add one is a module of its own in the
same repository, so you download the Fiber binding or the Gin binding or
neither, and pgx only if you use pgx ([[D-033]]).

Versions move in lockstep: the library and every satellite are tagged together,
so `@v0.1.0` means the same thing everywhere. No `replace` is ever needed.

Four packages are the **contract manifest** — `crud`, `query`, `errs`, `port`.
They are the interfaces a third party implements, they import only the standard
library and each other, and the list is closed ([[D-048]]). `make check` enforces
it rather than this sentence doing so.

## See also

- [usage-guides/ent.md](../../usage-guides/ent.md) — adopt an ent model as-is
- [usage-guides/gorm.md](../../usage-guides/gorm.md) — adopt a gorm model as-is
- [`_examples/`](../../../_examples/) — one runnable program per stack
- [roadmaps/Roadmap.md](../../roadmaps/Roadmap.md) — what is left
