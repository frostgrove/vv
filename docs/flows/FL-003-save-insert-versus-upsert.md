# FL-003 — Save: insert versus upsert

**Entry point:** `crud/sqlrepo/repository.go:Save` (reached from `DefaultService.Create` and `:Replace`, whichever binding built the command)
**Implements:** [[UC-001]] [[UC-009]] · **Governed by:** [[D-011]] [[D-012]] [[D-019]]

One method, two statements, and a fork decided entirely by whether the model's
primary key holds a value.

## The path

1. **`HandlerFor.Create`** — `crud/http/crudfiber/handler.go:Create` and its two
   twins. The body is decoded into the handler's input type, mapped onto `M`,
   and handed to the service as a `port.CreateCommand` ([[FL-015]]).

   **`DefaultService.Create`** — `port/service.go:Create` — `port.Sanitize`
   (`port/model.go`):
   - when `meta.PK.Auto` and `AllowClientID` was not set, the key is zeroed —
     a client cannot pick its own id;
   - `ClearGenerated` zeroes every `generated` column by offset, so a client
     cannot forge a server-side timestamp.
   Then the `BeforeSave` hook the command carried, then `repo.Save`, then 201.
   The hook runs *after* the clearing and that order is the guarantee, not an
   accident of where the code sits ([[UC-013]] guarantee 7).

   **`DefaultService.Replace` (PUT)** — `port/service.go:Replace` — takes a
   different route to the same call. When the key is database-generated and
   `AllowClientID` is off it first does a `GetByID` probe: PUT then **replaces
   and never creates**. The reason is on the method: PUT would otherwise be the
   way around `AllowClientID`, and on PostgreSQL an explicit insert into a serial
   column does not advance the sequence, so the next POST collides on the
   primary key and keeps colliding until somebody repairs the sequence by hand.
   A client-owned key (a uuid, a slug) is a different matter and PUT still
   creates those.

2. **(optional) `gate.Save`** — `crud/decorators/security/security.go:337`
   The unscoped-existence probe and the immutable-field check — [[FL-008]].

3. **`repository.Save`** — `crud/sqlrepo/repository.go:442`
   The fork, in full:
   ```go
   hasID, err := r.meta.HasID(m)          // crud/access.go:53 — is the PK non-zero
   switch {
   case !hasID && r.meta.PK.Auto:  insert(insertGen, meta.InsertGen, generatedPK=true)
   case !hasID:                    return crud.ErrMissingID
   default:                        insert(insertFull+upsertTail, meta.Insert, false)
   }
   ```
   `ErrMissingID` is the `noauto` case: a model whose key the application owns
   (uuid, slug, natural key) and which arrived at zero. An integer primary key
   gets `Auto` by default (`crud/meta.go:428`); `db:",noauto"` opts out
   (`crud/meta.go:405`), and from then on an unset key is a 400, not an insert
   of `0`.

4. **The statements were assembled once** — `newRepository`, `repository.go:27`
   At `Bind` time, not per call:
   - `insertGen` = `INSERT INTO t (every insertable column except the PK) VALUES …`
   - `insertFull` = `INSERT INTO t (every insertable column, PK included) VALUES …`
   - `upsertTail` = `d.Upsert(PK.Column, meta.Update)` (`repository.go:49`)
   - `returning` = `" RETURNING <all columns>"`, or empty
   The three column lists come from `crud/meta.go:281-297`:
   `Insert` excludes `generated`; `InsertGen` additionally excludes the PK;
   `Update` additionally excludes `immutable` and `version`.

5. **The conflict clause** — `crud/dialect.go:47` (Postgres/SQLite),
   `crud/dialect.go:89` (MySQL)
   `ON CONFLICT (pk) DO UPDATE SET c = EXCLUDED.c, …` / `ON DUPLICATE KEY UPDATE
   c = VALUES(c)` (or `new.c` with `MySQL{RowAlias: true}`).
   **The immutable columns are not in that list.** Neither is the version
   counter, so an upsert built from a stale model cannot wind the lock back.
   That is the intended behaviour and it has a direct consequence: after an
   upsert, the caller's in-memory model may hold values the database just
   refused to write. Hence step 7.
   When `meta.Update` is empty the clause degrades — Postgres to `DO NOTHING`,
   MySQL to a no-op `pk = pk` assignment — so the statement stays valid.

6. **`repository.insert`** — `crud/sqlrepo/repository.go:462`
   `meta.Values` (`crud/access.go:31`) reads the bind arguments by field offset.
   Then the dialect fork:
   - **RETURNING** (`repository.go:469`): `Query(stmt + returning)` and `scanOne`
     straight back into `m`. If no row came back — `ON CONFLICT DO NOTHING`
     matched an existing row — fall through to `refresh`.
   - **No RETURNING** (`repository.go:485`): `Exec`, then, when the key was
     database-generated and the driver reported one, `meta.SetID(m,
     res.LastInsertID)`. `SetID` (`crud/access.go:64`) converts between integer
     widths, so a driver's `int64` lands in an `int32` or `uint` key.

7. **The read-back** — `repository.go:500` → `repository.refresh` (`:506`)
   On a dialect without RETURNING this is unconditional. Skipping it when the
   model declares no `generated` column saved a round trip and cost correctness:
   the conflict clause leaves out every immutable column, so the caller was left
   holding values the database had refused, and a handler serialised a different
   document on MySQL than on PostgreSQL. `refresh` reads by primary key through
   the repository's relation scopes and returns `ErrNotFound` if the row is not
   there.

## Where the decisions bite

- **`HasID` is the whole fork.** Not a flag, not a method on the model — the
  zero-ness of the primary key. Anything that zeroes or fills the key before
  `Save` changes the statement: that is exactly what `port.Sanitize` and
  `DefaultService.Replace` are doing, deliberately.
- **`Save` has no WHERE clause, so no scope can narrow it.** `sqlrepo.Scope`
  cannot apply (`crud/sqlrepo/blueprint.go:71`) and `security.Gate` therefore has
  to probe for the target row and refuse — [[FL-008]]. Do not "fix" this by
  adding options to `Save`; there is nowhere in an upsert for them to go.
- **Immutable and version columns stay out of the conflict clause.** That is
  what `immutable` means. The compensating read-back is what keeps the returned
  model honest, so the two changes travel together.
- **A generated key is never client-chosen unless someone opted in.** Two
  independent guards, `Sanitize` on POST and the existence probe on PUT, because
  PUT bypasses the first. Both are in the service, so a fourth binding gets them
  by calling ([[D-045]]).

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| `nil` model | `Save` (`repository.go:443`) | 400 (`SchemaError`) |
| `noauto` key left at zero | `Save` (`repository.go:454`) | 400 `ErrMissingID` |
| PUT to an unused id with a generated key | `DefaultService.Replace`'s probe (`port/service.go`) | 404 |
| duplicate key / FK / NOT NULL / CHECK | the adapters' `Executor.conflict` → `sqlfault.Wrap` ([[FL-014]]) | 409. The message is the fault's classification text where the source named its engine, and the driver's own sentence where it did not |
| `ON CONFLICT DO NOTHING` matched an existing row | `insert` (`repository.go:482`) | 200/201 with the *stored* row |
| the row disappears between the write and the read-back | `refresh` (`repository.go:525`) | 404 |
| gate refuses an overwrite of a hidden row | `gate.saveTarget` (`security.go:423`) | 403 |

## Files

| File | Role |
|---|---|
| `crud/http/crudfiber/handler.go`, `crud/http/crudgin/handler.go`, `crud/http/crudnet/handler.go` | `Create`, `Replace` |
| `port/model.go` | `Sanitize`, `ClearGenerated` — what a client may not dictate, in one place for every binding and every transport ([[D-045]]). `crudhttp.Sanitize` and `:ClearGenerated` forward to it |
| `port/service.go` | `DefaultService.Create` / `:Replace` — where the clearing runs, and the one place the hook order is decided ([[FL-015]]) |
| `crud/sqlrepo/repository.go` | `Save`, `insert`, `refresh`, statement assembly in `newRepository` |
| `crud/meta.go` | `Insert` / `InsertGen` / `Update` column lists, tag options |
| `crud/access.go` | `HasID`, `ID`, `SetID`, `Values` |
| `crud/dialect.go` | `Upsert`, `SupportsReturning` |
| `crud/errors.go` | `ErrMissingID`, `ErrConflict` |
| `crud/adapter/crudsql/conflict.go`, `crud/adapter/crudpgx/conflict.go` | `Executor.conflict` — integrity errors → `ErrConflict`, and a fault where the engine was declared. The gate and the assembly are `sqlfault`'s ([[FL-014]]) |
| `crud/decorators/security/security.go` | the gated variant |

## Tests that walk this flow

- `TestSaveInsertsWithGeneratedKeyOnPostgres` — `crud/sqlrepo/repository_test.go` — the `insertGen` + RETURNING path.
- `TestSaveUpsertsWhenKeyIsSet` — `crud/sqlrepo/repository_test.go` — the `insertFull` + conflict path.
- `TestSaveOnMySQLUsesLastInsertID` — `crud/sqlrepo/repository_test.go` — the `LastInsertId` readback.
- `TestSaveOnADialectWithoutRETURNINGReadsTheRowBack` — `crud/sqlrepo/repository_test.go` — pins the unconditional refresh.
- `TestSaveRequiresAssignedKeyWhenNotGenerated` — `crud/sqlrepo/repository_test.go` — `ErrMissingID`.
- `TestSaveNeverWindsTheVersionBack` — `crud/sqlrepo/version_test.go` — the version stays out of the conflict clause.
- `TestDialectUpsert` — `crud/dialect_test.go` — the clause each dialect renders.
- `TestUpsertClauseCarriesItsOwnLeadingSpace` — `crud/dialect_test.go` — concatenation contract with `insertFull`.
Each `crud/http/crudfiber/` test below has an identical twin in `crud/http/crudgin/` and
`crud/http/crudnet/`.

- `TestCreateRefusesAClientChosenKeyAndGeneratedColumns` — `crud/http/crudfiber/handler_test.go` — `Sanitize`.
- `TestPutIsNotAWayAroundAllowClientID` — `crud/http/crudfiber/write_edge_test.go` — the PUT probe.
- `TestReplaceTakesTheIDFromThePathNotTheBody` — `crud/http/crudfiber/handler_test.go`.
- `TestUpsertLeavesTheSameRowInEveryDialect` — `test/integration/dialect_edge_test.go`.
- `TestSaveLeavesTheCallerHoldingTheStoredRowOnEveryEngine` — `test/integration/dialect_edge_test.go` — the reason the read-back exists.
- `TestASaveCannotWindTheLockBack` — `test/integration/dialect_edge_test.go`.
- `TestAnIntegrityConflictIsA409WithAMessage` — `crud/http/crudfiber/write_edge_test.go`.

## See also

[[FL-002]] [[FL-004]] [[FL-008]] [[FL-011]] [[FL-013]]
