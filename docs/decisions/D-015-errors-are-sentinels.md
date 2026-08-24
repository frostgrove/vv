# D-015 — Errors are sentinels, matched with `errors.Is`

**Status:** accepted
**Invariant:** Every failure a caller is expected to branch on must be reachable with `errors.Is` against an exported sentinel in package `crud`, and a transport must map it without importing the package that raised it.

## The decision

`crud/errors.go` declares the sentinels. Layers wrap rather than replace: the
security decorator's `ErrForbidden` wraps `crud.ErrForbidden`, `ErrStaleVersion`
wraps `crud.ErrConflict`, the adapters wrap a driver's integrity error in
`crud.ErrConflict` and keep the original underneath. Two structured errors —
`*crud.UnknownFieldError` and `*crud.SchemaError` — are matched with `errors.As`
because they carry the field and the reason.

## The list

| sentinel / type | raised by | HTTP |
| --- | --- | --- |
| `crud.ErrNotFound` | `GetByID`, `Update`, `refresh`, an out-of-scope id ([[D-008]]), `DELETE /:id` that removed nothing | 404 |
| `crud.ErrForbidden` | wrapped by `security.ErrForbidden` / `security.Denied`; a service layer may wrap it too | 403 |
| `crud.ErrConflict` | the adapters' SQLSTATE-23 classifier; `specs.ErrNotUnique` (`FindOne` matched several rows) wraps it | 409 |
| `crud.ErrStaleVersion` | the optimistic lock; **wraps `ErrConflict`** | 409 |
| `crud.ErrMissingID` | `Save` with an unset `noauto` key ([[D-011]]) | 400 |
| `*query.Error` | any rejected query document ([[D-013]]) | 400 + path + reason |
| `*crud.UnknownFieldError` | a field path that does not resolve | 400 |
| `*crud.SchemaError` | a declaration or a statement that cannot be built | 400 |
| `crud.ErrNoTxSupport` | `InTx` on an executor that cannot begin | 500 |
| `crud.ErrReadOnly` | nothing in the tree — see below | 500 |
| anything else | a driver error, a bug | 500, empty body |

`crud.ErrReadOnly` is declared and deliberately unused. `crudfiber.ReadOnly`
does not mount the write routes at all, and `security.ReadOnly` denies through
`ErrForbidden` so a transport answers 403. It exists for a third-party decorator
that wants to say "read-only" rather than "forbidden", and the comment on it
says so, so nobody wires it up thinking it closes a gap.

## Why

**Why sentinels rather than error codes or a typed enum.** `errors.Is` is what
Go callers already reach for, and wrapping means a layer can add context without
breaking the caller's branch. A code would have to be carried, which means every
layer has to know about the code type — the opposite of what wrapping buys.

**Why the transport does not import the decorator.** `crudfiber.Status` matches
`crud.ErrForbidden`. A user's own gate, a third-party gate, or a service method
returning `fmt.Errorf("%w: …", crud.ErrForbidden)` all map to 403 with no
registration step.

**Why `ErrStaleVersion` wraps `ErrConflict`.** A stale write *is* a conflict, and
a transport that has never heard of optimistic locking still answers 409. A
caller who wants to retry checks the more specific sentinel. Both work at once,
which is the point of wrapping.

**Why the adapters classify SQLSTATE class 23.** A duplicate key used to reach
the client as a 500 with an empty body — the class of bug where the client
retries forever and the server logs nothing useful. Class 23 is *integrity
constraint violation*: unique key, foreign key, NOT NULL and CHECK belong there.

**That last claim was wrong about MySQL, and it cost a 500.** "Nothing else does,
so the classification needs no per-driver table" held for PostgreSQL and not for
MySQL, which reports a CHECK violation as `3819` and a missing column default as
`1364`, both with SQLSTATE `HY000` — its "no more specific state" code. Neither
was classified, so a client got a bare 500 where the table below promises 409.
Measured on MySQL 8.4.11, not remembered.

The classifier therefore reads class 23 **and** a short list of MySQL numbers,
and it only trusts a number when the state is exactly `HY000`, so a numeric field
on another driver's error cannot be mistaken for a MySQL code. The list is two
entries long and each one was provoked against a live server. The driver
error is kept underneath, so a caller who wants the constraint name still
`errors.As`-es their way to it.

**Why `crudsql` asks by shape.** `adapter/crudsql` may not name a driver's error
type. It looks for a `SQLState() string` method (pgx, lib/pq) and then for an
exported `SQLState` field (go-sql-driver/mysql), handling both the `string` and
the `[5]byte` spelling. `adapter/crudpgx` can name `*pgconn.PgError` and does.

**Why a 500 says nothing.** The underlying message can be a SQL string, a column
list, or a connection string fragment. `DefaultErrorHandler` fills `Message` only
for statuses below 500.

## What it forbids

- Do not replace a sentinel with a new error. Wrap it.
- Do not stop `ErrStaleVersion` wrapping `ErrConflict`.
- Do not add to the MySQL number list from documentation. Each entry is there
  because the error was provoked against a running server and the SQLSTATE was
  observed to be `HY000`. MariaDB's `4025` is deliberately absent for that
  reason.
- Do not broaden the SQLSTATE classifier past class 23. Class 40 (serialisation
  failure) is retryable and is *not* a client error; class 22 (data exception) is
  a coercion bug. Adding either to 409 tells the client to fix something it
  cannot fix.
- Do not put the driver's message into a 500 response body.
- Do not delete `crud.ErrReadOnly` because nothing uses it, and do not wire it
  into `security.ReadOnly` — that would turn a 403 into a 500.
- Do not map `ErrNoTxSupport` to a 4xx. The caller asked for a transaction from
  something that cannot give one; that is a wiring bug, not a request problem.
- Do not re-derive the table in another transport binding. There is one switch
  and every binding calls it ([[D-034]]); two copies drift the day a sentinel is
  added, and nothing fails when they do.

## Where it lives

- `crud/errors.go` — every sentinel, each with the reason it exists.
- `crud/errors.go:UnknownFieldError` / `crud/errors.go:SchemaError` — the two
  structured ones.
- `adapter/crudsql/conflict.go:conflict` / `adapter/crudsql/conflict.go:sqlState`
  / `adapter/crudsql/conflict.go:sqlStateField` — the shape-based classifier.
- `adapter/crudpgx/conflict.go:conflict` — the typed one.
- `repo/decorators/security/security.go:ErrForbidden` and
  `repo/decorators/security/security.go:Denied`.
- `repo/basic/repository.go:repository.missedRow` — the one place `ErrNotFound`
  and `ErrStaleVersion` are told apart.
- `repo/decorators/specs/errors.go` — `ErrNotUnique` (wraps `crud.ErrConflict`),
  `ErrUnboundedDelete`, `ErrUnboundedUpdate`.
- `http/crudhttp/errors.go:Status` — the whole mapping in one switch, shared by
  every transport binding ([[D-034]]).
- `http/crudhttp/errors.go:Body` — the response body, and the 500 silence.
- `http/crudfiber/options.go:Status`, `http/crudgin/options.go:Status`,
  `http/crudnet/options.go:Status` — the exported per-binding entry points, each
  one line over the shared switch.

## Proven by

- `TestStatusMapsWhatItPromisesTo` in `http/crudfiber/edge_test.go` — the table
  above, as a test.
- `TestRepositoryErrorsBecomeStatusCodes` in `http/crudfiber/edge_test.go`.
- `TestEveryRouteMapsARefusalTheSameWay` in `http/crudfiber/edge_test.go` — a
  route that forgot to route through `fail` would slip past a per-route test.
- `TestA500NeverEchoesTheInternalError` in `http/crudfiber/edge_test.go`.
- `TestAClassifiedConflictIsNotAnyOtherSentinel` in
  `adapter/crudsql/conflict_test.go`.
- `TestOnlyIntegrityErrorsBecomeConflicts` in `adapter/crudpgx/conflict_test.go`
  — the control: a non-integrity SQLSTATE stays a plain error and reaches HTTP
  as 500.
- `TestIntegrityErrorsAreClassifiedWhateverShapeTheDriverUses` in
  `adapter/crudsql/conflict_test.go` — method, string field and `[5]byte` field.
- `TestADuplicateKeyIsAConflictWhicheverWayPgxReportsIt` in
  `adapter/crudpgx/conflict_test.go`.
- `TestIntegrityViolationsAreClassifiedByEveryAdapter` in
  `test/integration/dialect_edge_test.go` — against live PostgreSQL and MySQL,
  through every adapter.
- `TestAnIntegrityConflictIsA409WithAMessage` in
  `http/crudfiber/write_edge_test.go` — end to end.
- `TestAScopeThatFailsIsMappedLikeAnyOtherError` in `http/crudfiber/edge_test.go`.
- `TestInTxWithoutABeginnerIsRefused` in `crud/executor_test.go`.

## See also

[[D-008]] [[D-010]] [[D-011]] [[D-013]] [[D-019]] [[D-034]]
