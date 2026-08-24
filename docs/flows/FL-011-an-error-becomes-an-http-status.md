# FL-011 — An error becomes an HTTP status

**Entry point:** `http/crudhttp/errors.go:Status` (via each binding's `Handler.fail`)
**Implements:** [[UC-015]] · **Governed by:** [[D-015]] [[D-008]] [[D-019]] [[D-034]]

Every handler ends in `h.fail(c, err)`. One function decides the status, one
decides the body, and everything the library knows how to classify is a client
mistake or an access decision. Everything else is a 500 that says nothing.

## Where each sentinel is produced

| Sentinel / type | Declared | Produced by |
|---|---|---|
| `crud.ErrNotFound` | `crud/errors.go:10` | `repository.GetByID` (`repo/basic/repository.go:121`), `repository.refresh` (`:526`), the `Update` load (`:554`), `missedRow` (`:661`), `gate.loadScoped` (`repo/decorators/security/security.go:258`), `Handler.Delete` when 0 rows went away (`http/crudfiber/handler.go:279`, `http/crudgin/handler.go:318`), `specs.FindOne`/`FindFirst` |
| `crud.ErrConflict` | `crud/errors.go:31` | the adapters only — `adapter/crudsql/conflict.go:20` and `adapter/crudpgx/conflict.go:20`, on SQLSTATE class 23 |
| `crud.ErrStaleVersion` (wraps `ErrConflict`) | `crud/errors.go:36` | `repository.missedRow` (`repo/basic/repository.go:659`) |
| `specs.ErrNotUnique` (wraps `ErrConflict`) | `repo/decorators/specs/errors.go:13` | `specs.FindOne` when two rows match |
| `crud.ErrForbidden` | `crud/errors.go:25` | wrapped by `security.ErrForbidden` (`security.go:27`) and produced by `security.Denied` (`security.go:141`); a service layer is expected to wrap it too |
| `crud.ErrMissingID` | `crud/errors.go:16` | `repository.Save` on an unset `noauto` key (`repository.go:454`) |
| `*query.Error` | `query/compile.go:13` | every rejection in the wire DSL, carrying `Path` and `Reason` |
| `*crud.UnknownFieldError` | `crud/errors.go:41` | `Schema.Field` misses in the repository and the SQL writer |
| `*crud.SchemaError` | `crud/errors.go:52` | declaration-time and render-time refusals |
| `crudhttp.ErrBadRequest` | `http/crudhttp/errors.go:16` | `BadRequest` / `BadRequestf` (`errors.go:19`) — malformed body, unparseable id, `MaxBulk` exceeded. One sentinel for every binding, so a 400 raised by one is recognised by the shared switch ([[D-034]]) |
| `crud.ErrNoTxSupport` | `crud/errors.go:13` | `crud.InTx` on a handle that cannot begin — **not classified**, so 500 |
| `crud.ErrReadOnly` | `crud/errors.go:22` | nothing in the tree; it exists for a decorator that wants to say "read-only" rather than "forbidden" — **not classified**, so 500 |

## The path

1. **The adapters classify integrity errors first.**
   `adapter/crudsql/conflict.go:20` wraps anything whose SQLSTATE starts with
   `23` as `ErrConflict`, keeping the driver error underneath so a caller can
   still `errors.As` their way to the constraint name. `sqlState`
   (`adapter/crudsql/conflict.go:31`) asks by *shape* — a `SQLState() string` method (pgx,
   lib/pq) or an exported `SQLState` field (go-sql-driver/mysql) — because the
   package may not name a driver's error type.
   `adapter/crudpgx/conflict.go:20` does the same for `*pgconn.PgError`, and
   `crudpgx.rows.Err` (`crudpgx.go:81`) is where it actually happens on
   PostgreSQL: pgx does not report a failed statement from `Query`, it hands
   back a live `Rows` whose first `Next` is false and whose `Err` carries the
   error — and on PostgreSQL every insert and update runs as
   `… RETURNING`, i.e. as a query.

2. **`Handler.fail`** — `http/crudfiber/handler.go:369`,
   `http/crudgin/handler.go:404` → `h.opt.errorHandler`, which is that binding's
   `DefaultErrorHandler` unless `WithErrorHandler` replaced it. Every route in
   both bindings ends here; no route has its own mapping.

3. **`Status`** — `http/crudhttp/errors.go:39`
   A single ordered switch, using `errors.Is`/`errors.As` so wrapped errors
   classify:
   ```
   nil                                      -> 200
   ErrNotFound                              -> 404
   ErrForbidden                             -> 403
   ErrConflict                              -> 409     (ErrStaleVersion, ErrNotUnique)
   ErrBadRequest | *query.Error |
   *UnknownFieldError | *SchemaError |
   ErrMissingID                             -> 400
   anything else                            -> 500
   ```
   **The order is load-bearing.** An error that wraps two sentinels gets the
   first matching arm; 404 before 403 is the same instinct as
   `gate.loadScoped`'s ([[FL-007]]) — do not confirm that a row exists.

4. **`DefaultErrorHandler`** — `options.go:122`
   ```go
   status := Status(err)
   body := ErrorBody{Error: statusText(status)}
   switch {
   case errors.As(err, &qe):                    body.Path, body.Message = qe.Path, qe.Reason
   case status != fiber.StatusInternalServerError: body.Message = err.Error()
   }
   ```
   A `query.Error` reports its own `Path` and `Reason` — the clause that was
   wrong and why, and nothing internal. Everything else non-500 gets
   `err.Error()`. **A 500 gets no message at all**: the underlying text could be
   a SQL statement, a constraint name or a connection string.
   `statusText` (`options.go:136`) produces the stable machine-readable code:
   `not_found`, `forbidden`, `conflict`, `bad_request`, `internal_error`.

5. **The response.** `{"error":"…","path":"…","message":"…"}` — `ErrorBody`,
   `options.go:91`, with `omitempty` on the last two.

## Where the decisions bite

- **A 500 leaks nothing.** One `case` in `DefaultErrorHandler`. Adding
  `err.Error()` to the default 500 branch is the change that turns an internal
  error into an information disclosure.
- **`ErrConflict` is produced by adapters, never by the repository.** That is
  what keeps the classification in one place per driver and out of every write
  path. A new adapter that skips it turns duplicate keys into 500s.
- **`crudpgx` classifies in `rows.Err`, not only in `Query`.** Without it a
  duplicate key reached the client as a bare 500 through pgx while the same
  statement through `database/sql` answered 409.
- **Sentinels are the transport contract.** A service layer wraps
  `crud.ErrForbidden`/`ErrNotFound`/`ErrConflict` and gets the right status
  without importing anything from `http/`. `security` wraps `crud.ErrForbidden`
  for exactly this reason.
- **`ErrReadOnly` and `ErrNoTxSupport` are unclassified on purpose.** Neither is
  reachable from a well-formed request through the mounted routes; both would be
  a programming error, and 500 is the honest answer.

## Traps

- **DELETE is asymmetric.** `DELETE /:id` on nothing is 404
  (`handler.go:293`); `POST /bulk-delete` with ids that match nothing is
  `200 {"deleted":0}` (`handler.go:320`), and with an empty list it never
  reaches the repository at all (`handler.go:310`).
- **`Status` is exported.** A custom `WithErrorHandler` should reuse it rather
  than reimplement the mapping, or the two will drift.
- **A `query.Error` is 400 by type, not by wrapping.** It is matched with
  `errors.As`, so wrapping one in `fmt.Errorf("%w", …)` preserves the status but
  a `query.Error` value copied into a plain `errors.New` does not.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| unknown field, bad value, bad operator | `query` compile → `*query.Error` | 400 with `path` and `message` |
| unparseable `:id`, malformed body, over `MaxBulk` | `badRequestf` | 400 `bad_request` |
| unset `noauto` primary key on save | `ErrMissingID` | 400 |
| row absent or out of scope | `ErrNotFound` | 404 `{"error":"not_found"}` |
| policy denial | `security.Denied` → `ErrForbidden` | 403 |
| duplicate key / FK / NOT NULL / CHECK | adapter `conflict()` | 409 with the driver's message |
| stale optimistic-lock version | `ErrStaleVersion` | 409 |
| `FindOne` matched several rows | `specs.ErrNotUnique` | 409 |
| driver failure, closed pool, context cancelled | nothing classifies it | 500 `{"error":"internal_error"}` and no detail |

## Files

| File | Role |
|---|---|
| `http/crudhttp/errors.go` | `Status`, `Body`, `StatusText`, `ErrorBody`, `ErrBadRequest`, `BadRequest` — the whole mapping, shared by every binding |
| `http/crudfiber/options.go`, `http/crudgin/options.go` | the exported `Status` and `DefaultErrorHandler` per binding: one line and one response write each |
| `http/crudfiber/handler.go`, `http/crudgin/handler.go` | `fail`, and the DELETE asymmetry |
| `crud/errors.go` | every sentinel, `UnknownFieldError`, `SchemaError` |
| `query/compile.go` | `query.Error` — path and reason, safe to hand back |
| `adapter/crudsql/conflict.go` | SQLSTATE 23 → `ErrConflict`, by error shape |
| `adapter/crudpgx/conflict.go`, `adapter/crudpgx/crudpgx.go` | the same for pgx, including `rows.Err` |
| `repo/decorators/security/security.go` | `ErrForbidden`, `Denied` |
| `repo/decorators/specs/errors.go` | `ErrNotUnique`, `ErrUnboundedDelete`, `ErrUnboundedUpdate` |
| `repo/decorators/specs/executor.go` | `FindOne` / `FindFirst` — where `ErrNotFound` and `ErrNotUnique` are raised |

## Tests that walk this flow

Every test below that names `http/crudfiber/` has an identical twin in
`http/crudgin/`, same name, same file name. The two suites are ported one to one
and the mapping they exercise is one switch ([[D-034]]) — removing an arm from
`crudhttp.Status` fails both, which is the check that it is shared rather than
copied.

- `TestStatusMapsWhatItPromisesTo` — `http/crudfiber/edge_test.go` — the switch, arm by arm.
- `TestRepositoryErrorsBecomeStatusCodes` — `http/crudfiber/edge_test.go`.
- `TestEveryRouteMapsARefusalTheSameWay` — `http/crudfiber/edge_test.go` — no route has its own mapping.
- `TestA500NeverEchoesTheInternalError` — `http/crudfiber/edge_test.go` — the disclosure guard.
- `TestDeletingNothingIs404ForOneRowAndZeroForASet` — `http/crudfiber/edge_test.go`.
- `TestAQueryThatNamesSomethingTheModelLacksIsABadRequest` — `http/crudfiber/edge_test.go`.
- `TestEveryRejectionNamesThePathThatWasWrong` — `query/edge_test.go` — the `Path` field.
- `TestAnIntegrityConflictIsA409WithAMessage` — `http/crudfiber/write_edge_test.go`.
- `TestIntegrityErrorsAreClassifiedWhateverShapeTheDriverUses` — `adapter/crudsql/conflict_test.go`.
- `TestAClassifiedConflictIsNotAnyOtherSentinel` — `adapter/crudsql/conflict_test.go`.
- `TestADuplicateKeyIsAConflictWhicheverWayPgxReportsIt` — `adapter/crudpgx/conflict_test.go` — including `rows.Err`.
- `TestOnlyIntegrityErrorsBecomeConflicts` — `adapter/crudpgx/conflict_test.go`.
- `TestIntegrityViolationsAreClassifiedByEveryAdapter` — `test/integration/dialect_edge_test.go`.
- `TestWithErrorHandlerReplacesTheMapping` — `http/crudfiber/options_test.go`.
- `TestAScopeThatFailsIsMappedLikeAnyOtherError` — `http/crudfiber/edge_test.go`.
- `TestHTTPRejections` — `test/integration/http_test.go` — end to end against a database.
- `TestGinHTTPRejections` — `test/integration/http_gin_test.go` — the same, through the Gin binding.

## See also

[[FL-001]] [[FL-002]] [[FL-003]] [[FL-007]] [[FL-008]] [[FL-012]] [[FL-013]]
