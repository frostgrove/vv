# Flows

A flow is what actually happens in this codebase, end to end, with the files it
happens in: where a PATCH body turns into an `UPDATE` statement, and what is in
the way. Decisions ([[D-001]] and friends) say *why*. Use cases (`UC-NNN`) say
what a consumer wants. Flows say *where and how*.

**This is the only section that names files and symbols.** If you are writing
down where something lives, you are writing a flow. If you find a file path in a
decision or a use case, it belongs here instead.

Line numbers in these documents were correct when they were written. Treat them
as a starting point for a search, not as an address — the symbol name is the
thing that has to still exist.

## How to use this directory

Before you change something, read the flow that covers it. The **By file** table
below is the fastest way in: find the file you are about to edit and read every
flow it appears in, because a path you are not thinking about probably runs
through it.

| Before you touch… | Read |
|---|---|
| the query DSL, allow-lists, paging, the COUNT | [[FL-001]] |
| the update planner, `Opt`, the version column, the MySQL read-back | [[FL-002]] |
| `Save`, upserts, generated keys, `immutable` columns | [[FL-003]] |
| tags, schema reflection, relations, what fails at start-up | [[FL-004]] |
| the predicate writer, aliases, nested filters and sorts | [[FL-005]] |
| preloads, batching, how children are written into parents | [[FL-006]] |
| the security gate on reads | [[FL-007]] |
| the security gate on writes | [[FL-008]] |
| `context` executors, `InTx`, adapters, savepoints | [[FL-009]] |
| `cmd/vv`, the generated adapter, or anything else generated | [[FL-010]] |
| sentinels, HTTP statuses, what a 500 may say | [[FL-011]] |
| operators, coercion, timestamps, the two front doors | [[FL-012]] |
| the Gin or net/http binding, mounting, or anything the four bindings do differently | [[FL-013]] |
| the gRPC binding, a Struct payload, a status code or an error detail | [[FL-013]] |
| `port`, a command, the service seam, a mapper, or the path chain's middle hops | [[FL-015]] |
| a driver error, the two gates, `sqlfault`, either adapter's `conflict` | [[FL-014]] |
| schema introspection, the per-handle catalog key, the negative cache | [[FL-016]] |
| the probe, the caps, the savepoint mode, or anything that turns one violation into several | [[FL-017]] |
| `remote`, a client transport, `MarshalPredicate`, or calling another service's API | [[FL-018]] |
| a token, an API key, a middleware, a JWKS key set, or how a caller is identified | [[FL-019]] |
| a permission, a role, a claim-driven scope, or what an identified caller may do | [[FL-020]] |
| a DSN, a config file, a pool size, a replica, or how the application reaches its database | [[FL-021]] |

**A code change that alters a path must update its flow document in the same
change.** Not afterwards, not in a follow-up. A flow that describes a path the
code no longer takes is worse than no flow: it is read as current and it is
wrong. If the change makes a step disappear, delete the step; if it adds a trap,
add it to *Where the decisions bite* or *Traps*.

When adding a flow: `FL-NNN-kebab-slug.md`, next number free, and add it to both
tables here — the index and the reverse index. A flow number is identity rather
than order, and this directory has the precedent: phase 6 took **FL-016** before
phase 3 landed FL-014 and before phase 5 landed FL-015.

## Index

| ID | Flow | Entry point | Implements |
|----|------|-------------|-----------|
| [FL-001](FL-001-list-request-to-rows.md) | A list request from wire to rows | `crud/http/crudfiber/handler.go:List` / `:Query` | [[UC-001]] [[UC-002]] |
| [FL-002](FL-002-patch-becomes-an-update.md) | A PATCH becomes an UPDATE | `crud/http/crudfiber/handler.go:Update` | [[UC-003]] [[UC-009]] |
| [FL-003](FL-003-save-insert-versus-upsert.md) | Save: insert versus upsert | `crud/sqlrepo/repository.go:Save` | [[UC-001]] [[UC-009]] |
| [FL-004](FL-004-declaration-what-define-validates.md) | Declaration: what `sqlrepo.Define` validates and when | `crud/sqlrepo/blueprint.go:Define` | [[UC-010]] [[UC-014]] [[UC-016]] |
| [FL-005](FL-005-relation-filter-becomes-a-correlated-exists.md) | A relation filter becomes a correlated EXISTS | `crud/predicate.go:writer.leaf` | [[UC-006]] [[UC-004]] |
| [FL-006](FL-006-preload-becomes-batched-second-queries.md) | A preload becomes batched second queries | `crud/preload.go:RunPreloads` | [[UC-006]] [[UC-004]] |
| [FL-007](FL-007-a-read-through-the-security-gate.md) | A read through the security gate | `crud/decorators/security/security.go:gate.GetByID` | [[UC-004]] [[UC-016]] |
| [FL-008](FL-008-a-write-through-the-security-gate.md) | A write through the security gate | `crud/decorators/security/security.go:gate.Save` | [[UC-004]] [[UC-008]] |
| [FL-009](FL-009-transactions-joining-opening-which-database.md) | Transactions: joining, opening, and which database | `crud/executor.go:InTx` | [[UC-005]] [[UC-012]] |
| [FL-010](FL-010-codegen-model-to-dto-and-metamodel.md) | Codegen: a model becomes a DTO and a metamodel | `internal/codegen.Run` | [[UC-014]] [[UC-010]] [[UC-007]] |
| [FL-011](FL-011-an-error-becomes-an-http-status.md) | An error becomes an HTTP status | `port/porthttp/errors.go:Status` | [[UC-015]] |
| [FL-012](FL-012-a-wire-value-becomes-a-go-value.md) | A wire value becomes a Go value | `crud/query/coerce.go:decodeValue` / `:coerceString` | [[UC-002]] [[UC-006]] |
| [FL-013](FL-013-a-request-through-another-binding.md) | A request through another binding | `crud/http/crudgin/handler.go:List` / `crud/http/crudnet/handler.go:List` / `crud/rpc/crudgrpc/handler.go:List` | [[UC-001]] [[UC-002]] [[UC-013]] [[UC-015]] |
| [FL-014](FL-014-a-driver-error-becomes-a-public-violation.md) | A driver error becomes a public violation | `crud/sqlfault/classify.go:Wrap` | [[UC-015]] [[UC-017]] |
| [FL-015](FL-015-a-request-through-the-port-layer.md) | A request through the port layer | `crud/http/crudnet/handler.go:Create` | [[UC-001]] [[UC-013]] [[UC-015]] |
| [FL-016](FL-016-a-schema-becomes-a-catalog.md) | A schema becomes a catalog | `crud/catalog/load.go:Load` | [[UC-012]] |
| [FL-017](FL-017-a-failed-write-becomes-every-violation.md) | A failed write becomes every violation it caused | `crud/decorators/faults/probe.go:enricher.probed` | [[UC-017]] [[UC-004]] |
| [FL-018](FL-018-a-call-through-the-client.md) | A call through the client | `remote/resource.go:Resource.Get` | [[UC-018]] [[UC-015]] |
| [FL-019](FL-019-a-token-becomes-a-principal.md) | A token becomes a principal | `auth/guard.go:Guard.Authenticate` | [[UC-019]] |
| [FL-020](FL-020-a-principal-becomes-a-policy-decision.md) | A principal becomes a policy decision | `crud/decorators/security/principal.go:ScopeAttr` | [[UC-020]] [[UC-004]] |
| [FL-021](FL-021-a-configuration-becomes-a-connection.md) | A configuration becomes a connection | `utils/vvdb/dsn.go:DSN` / `utils/vvdb/open.go:Open` | [[UC-021]] |
| [FL-022](FL-022-a-migration-command-becomes-sql-and-schema.md) | A migration command becomes SQL and schema | `utils/vvgoose/vvgoose.go:Execute` | [[UC-022]] |
| [FL-023](FL-023-a-sign-in-becomes-a-session.md) | A sign-in becomes a session | `auth/access/access.runtime.go` | [[UC-023]] |

## By file — which flows touch this file

| File | Flows |
|---|---|
| `crud/adapter/crudpgx/conflict.go` | FL-003, FL-011, FL-014 |
| `crud/adapter/crudpgx/crudpgx.go` | FL-009, FL-011, FL-014 |
| `crud/adapter/crudsql/conflict.go` | FL-003, FL-011, FL-014 |
| `crud/adapter/crudsql/crudsql.go` | FL-009, FL-011, FL-014 |
| `auth/doc.go` | FL-019 |
| `auth/principal.go` | FL-019, FL-020 |
| `auth/context.go` | FL-019, FL-020 |
| `auth/credential.go` | FL-019 |
| `auth/guard.go` | FL-019 |
| `auth/errors.go` | FL-019, FL-011 |
| `auth/apikey/apikey.go` | FL-019 |
| `auth/authjwt/parser.go` | FL-019 |
| `auth/authjwt/key.go` | FL-019 |
| `auth/authjwt/jwks.go` | FL-019 |
| `auth/authjwt/claims.go` | FL-019 |
| `auth/authjwt/authenticator.go` | FL-019 |
| `auth/http/authhttp/authhttp.go` | FL-019, FL-011 |
| `auth/http/authnet/authnet.go` | FL-019, FL-013 |
| `auth/http/authgin/authgin.go` | FL-019, FL-013 |
| `auth/http/authfiber/authfiber.go` | FL-019, FL-013 |
| `auth/http/authfiber/locale.go` | FL-019 |
| `auth/rpc/authgrpc/interceptor.go` | FL-019, FL-013 |
| `crud/decorators/security/principal.go` | FL-020, FL-007, FL-008 |
| `cmd/vv/main.go` | FL-010 |
| `internal/codegen/codegen.go` | FL-010 |
| `internal/codegen/render.go` | FL-010 |
| `internal/codegen/adapter.go` | FL-010 |
| `crud/catalog/doc.go` | FL-016 |
| `crud/catalog/catalog.go` | FL-014, FL-016, FL-017 |
| `crud/catalog/errors.go` | FL-016 |
| `crud/catalog/load.go` | FL-016, FL-017 |
| `crud/catalog/set.go` | FL-016 |
| `crud/catalog/reload.go` | FL-016 |
| `crud/catalog/postgres.go` | FL-016 |
| `crud/catalog/mysql.go` | FL-016 |
| `crud/catalog/mariadb.go` | FL-016 |
| `crud/catalog/sqlite.go` | FL-016 |
| `crud/access.go` | FL-001, FL-003, FL-004, FL-006, FL-008 |
| `crud/crudtest/recorder.go` | FL-016 |
| `crud/dialect.go` | FL-002, FL-003, FL-009, FL-017 |
| `crud/errors.go` | FL-002, FL-003, FL-009, FL-011 |
| `crud/executor.go` | FL-002, FL-009, FL-016, FL-017 |
| `errs/doc.go` | FL-011 |
| `errs/code.go` | FL-011 |
| `errs/codes.go` | FL-011, FL-014 |
| `errs/path.go` | FL-011, FL-018 |
| `errs/violation.go` | FL-011, FL-014, FL-017 |
| `errs/fault.go` | FL-011, FL-014, FL-017 |
| `errs/build.go` | FL-011, FL-014 |
| `errs/spi.go` | FL-011, FL-014 |
| `errs/message.go` | FL-011 |
| `errs/catalogue.go` | FL-011 |
| `errs/bridge.go` | FL-011 |
| `errs/sqlerr/doc.go` | FL-011, FL-014 |
| `errs/sqlerr/classify.go` | FL-011, FL-014 |
| `errs/sqlerr/postgres.go` | FL-011, FL-014 |
| `errs/sqlerr/mysql.go` | FL-011, FL-014 |
| `errs/sqlerr/mariadb.go` | FL-011, FL-014 |
| `errs/sqlerr/sqlite.go` | FL-011, FL-014 |
| `errs/sqlerr/corpus.go` | FL-011, FL-014 |
| `errs/sqlerr/testdata/corpus/` | FL-011, FL-014 |
| `crud/meta.go` | FL-002, FL-003, FL-004, FL-010, FL-012 |
| `crud/options.go` | FL-001, FL-007, FL-008, FL-018 |
| `crud/opt.go` | FL-002 |
| `crud/page.go` | FL-001 |
| `crud/document.go` | FL-018 |
| `crud/predicate.go` | FL-005, FL-007, FL-012, FL-018 |
| `crud/preload.go` | FL-006 |
| `crud/relation.go` | FL-001, FL-004, FL-005, FL-006 |
| `crud/render.go` | FL-001, FL-004, FL-005, FL-017 |
| `crud/repo.go` | FL-002, FL-007 |
| `crud/scope.go` | FL-004, FL-005, FL-006, FL-007 |
| `crud/update.go` | FL-002, FL-004, FL-008, FL-010, FL-017 |
| `*/vv_gen.go` — ten checked-in files under `test/` and `_examples/` | FL-010 |
| `crud/http/crudfiber/handler.go` | FL-001, FL-002, FL-003, FL-011, FL-012, FL-013, FL-015 |
| `crud/http/crudfiber/routing_test.go` | FL-013 |
| `crud/http/crudgin/routing_test.go` | FL-013 |
| `crud/http/crudnet/routing_test.go` | FL-013 |
| `crud/http/crudfiber/options.go` | FL-002, FL-011, FL-013, FL-015 |
| `crud/http/crudfiber/middleware.go` | FL-013 |
| `crud/http/crudgin/middleware.go` | FL-013 |
| `crud/http/crudnet/middleware.go` | FL-013 |
| `crud/http/crudgin/handler.go` | FL-001, FL-002, FL-003, FL-011, FL-012, FL-013, FL-015 |
| `crud/http/crudgin/options.go` | FL-002, FL-011, FL-013, FL-015 |
| `crud/http/crudnet/handler.go` | FL-001, FL-002, FL-003, FL-011, FL-012, FL-013, FL-015 |
| `crud/http/crudnet/options.go` | FL-002, FL-011, FL-013, FL-015 |
| `crud/http/crudhttp/doc.go` | FL-013, FL-015 |
| `auth/http/authnet/binding_test.go` | FL-019 |
| `auth/http/authgin/binding_test.go` | FL-019 |
| `crud/http/crudhttp/model.go` | FL-003, FL-013 |
| `crud/http/crudhttp/repository.go` | FL-013 |
| `crud/http/crudhttp/request.go` | FL-001, FL-002, FL-012, FL-013, FL-015 |
| `crud/http/crudhttp/porthttp.go` | FL-011, FL-013, FL-015 |
| `port/porthttp/errors.go` | FL-011, FL-013, FL-014, FL-015, FL-018 |
| `port/porthttp/render.go` | FL-011, FL-015 |
| `port/porthttp/envelope.go` | FL-011 |
| `port/porthttp/decode.go` | FL-013, FL-018 |
| `port/porthttp/bodyindex.go` | FL-011, FL-015 |
| `port/porthttp/body.go` | FL-001, FL-002, FL-011, FL-012, FL-013, FL-015 |
| `port/porthttp/locale_test.go` | FL-013, FL-015 |
| `port/doc.go` | FL-015 |
| `port/rules.go` | FL-013, FL-015 |
| `port/log.go` | FL-013, FL-019 |
| `port/service.go` | FL-001, FL-002, FL-003, FL-011, FL-015 |
| `port/command.go` | FL-002, FL-015 |
| `port/mapper.go` | FL-015 |
| `port/path.go` | FL-011, FL-015 |
| `port/pathmap.go` | FL-010, FL-011, FL-015 |
| `port/repository.go` | FL-013, FL-015 |
| `port/model.go` | FL-003, FL-015 |
| `port/request.go` | FL-001, FL-002, FL-012, FL-013, FL-015, FL-018 |
| `port/sentinel.go` | FL-011, FL-015 |
| `port/kind.go` | FL-011, FL-014, FL-015, FL-018 |
| `port/violations.go` | FL-011, FL-013, FL-015 |
| `port/locale.go` | FL-011, FL-013, FL-015 |
| `remote/dto.go` | FL-018 |
| `remote/options.go` | FL-018 |
| `remote/resource.go` | FL-018 |
| `remote/transport.go` | FL-018 |
| `remote/remotehttp/doc.go` | FL-018 |
| `remote/remotehttp/transport.go` | FL-018 |
| `crud/rpc/crudgrpc/doc.go` | FL-013 |
| `crud/rpc/crudgrpc/handler.go` | FL-013, FL-015 |
| `crud/rpc/crudgrpc/service.go` | FL-013 |
| `crud/rpc/crudgrpc/message.go` | FL-013 |
| `crud/rpc/crudgrpc/status.go` | FL-011, FL-013, FL-015, FL-018 |
| `crud/rpc/crudgrpc/options.go` | FL-011, FL-013, FL-015 |
| `crud/rpc/crudgrpc/interceptor.go` | FL-013 |
| `crud/rpc/crudgrpc/locale.go` | FL-011, FL-013 |
| `crud/rpc/crudgrpc/transport.go` | FL-018 |
| `crud/probe/doc.go` | FL-017 |
| `crud/probe/probe.go` | FL-017 |
| `crud/probe/full.go` | FL-017 |
| `crud/probe/plan.go` | FL-017 |
| `crud/probe/sql.go` | FL-017 |
| `crud/probe/dup.go` | FL-017 |
| `crud/probe/options.go` | FL-017 |
| `crud/probe/declare.go` | FL-017 |
| `crud/decorators/faults/faults.go` | FL-011, FL-014, FL-015, FL-017 |
| `crud/decorators/faults/probe.go` | FL-009, FL-017 |
| `crud/query/coerce.go` | FL-012 |
| `crud/query/compile.go` | FL-001, FL-006, FL-011, FL-013 |
| `crud/query/filter.go` | FL-001, FL-005, FL-012 |
| `crud/query/ops.go` | FL-012 |
| `crud/query/querystring.go` | FL-001, FL-012 |
| `crud/query/request.go` | FL-001 |
| `crud/sqlrepo/blueprint.go` | FL-003, FL-004, FL-006, FL-007 |
| `crud/sqlrepo/repository.go` | FL-001, FL-002, FL-003, FL-004, FL-005, FL-006, FL-007, FL-008, FL-009, FL-011, FL-017 |
| `crud/decorators/security/policies.go` | FL-004, FL-007, FL-008 |
| `crud/decorators/security/security.go` | FL-002, FL-003, FL-007, FL-008, FL-011 |
| `crud/decorators/specs/errors.go` | FL-011 |
| `crud/decorators/specs/executor.go` | FL-011 |
| `crud/decorators/specs/metamodel.go` | FL-010 |
| `crud/sqlfault/doc.go` | FL-011, FL-014 |
| `crud/sqlfault/extract.go` | FL-011, FL-014 |
| `crud/sqlfault/gate.go` | FL-011, FL-014 |
| `crud/sqlfault/classify.go` | FL-011, FL-014 |
| `crud/sqlfault/catalog.go` | FL-011, FL-014 |
| `test/cmd/corpus/main.go` | FL-011 |
| `test/corpus/cases.go` | FL-011, FL-014 |
| `test/corpus/capture.go` | FL-011, FL-014 |
| `test/corpus/corpus.go` | FL-011 |
| `test/codegen/codegen_test.go` | FL-010 |
| `test/versionstore/` | FL-010 |
| `test/integration/corpus_test.go` | FL-011, FL-014 |
| `test/integration/dialect_edge_test.go` | FL-014 |
| `test/integration/catalog_test.go` | FL-016 |
| `test/integration/catalog_schema_test.go` | FL-016 |
| `test/integration/probe_test.go` | FL-017 |
| `test/integration/probe_schema_test.go` | FL-017 |
| `test/integration/http_port_test.go` | FL-013, FL-015 |
| `test/integration/rpc_grpc_test.go` | FL-011, FL-013, FL-015 |
| `test/portmount/mount_test.go` | FL-013, FL-015 |
| `test/portmount/grpcmount_test.go` | FL-011, FL-013, FL-015 |
| `test/dsn/dsn_test.go` | FL-021 |
| `test/integration/vvdb_test.go` | FL-021 |
| `utils/vvdb/config.go` | FL-021, FL-022 |
| `utils/vvdb/dsn.go` | FL-021 |
| `utils/vvdb/open.go` | FL-021 |
| `utils/vvdb/doc.go` | FL-021 |
| `utils/vvdb/dbpgx/dbpgx.go` | FL-021 |
| `utils/vvgoose/vvgoose.go` | FL-022 |
| `utils/vvgoose/migration.go` | FL-022 |
| `utils/vvgoose/sql.go` | FL-022 |
| `auth/access/access.runtime.go` | FL-023 |
| `auth/access/access.strategy.go` | FL-023 |
| `auth/access/access.subject.go` | FL-023 |
| `auth/access/access.endpoints.go` | FL-023 |
| `auth/access/access.authenticator.go` | FL-023, FL-019 |
| `auth/access/usecase.login.go` | FL-023 |
| `auth/access/usecase.signup.go` | FL-023 |
| `auth/access/usecase.enroll.go` | FL-023 |
| `auth/access/http/accesshttp/accesshttp.go` | FL-023 |
| `auth/access/accessjwt/rotation.go` | FL-023 |
| `auth/access/accessjwt/accessjwt.go` | FL-023 |
| `utils/vvgoose/provider.go` | FL-022 |
| `utils/vvgoose/internal/modelscan/` | FL-022 |

`crud/sqlrepo/repository.go` is in eleven of them. It is the layer everything else
decorates, and almost no change to it is local.

## Not yet written

Two paths exist, are used by a documented use case, and have no flow of their
own. Both are currently covered only in passing by the flows above. Write one
before changing them substantially rather than after.

- **A specification becomes options** — `crud/decorators/specs/`: `Specification`
  composition, `Metamodel` binding at package initialisation, and the
  `FindOne` / `DeleteBy` / `UpdateBy` guards. [[UC-007]] leans on [[FL-010]] and
  [[FL-005]] for it; [[FL-011]] names its sentinels; nothing traces the path.
- **A test double answers a statement** — `crud/crudtest/`: the recorder is how
  almost every unit test in the tree asserts on SQL, so its statement queue,
  scan conversion rules and `Begin` behaviour are worth writing down once.
  [[UC-011]] is about it and points at flows that only use it.
