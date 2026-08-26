# D-011 — `Save` is JPA-shaped: no key inserts, a key upserts

**Status:** accepted
**Invariant:** `Save` must insert when the primary key is unset and the key is database-generated, upsert when the key is set, and return `crud.ErrMissingID` when the key is unset and is not database-generated.

## The decision

One method, three branches:

| model state | statement |
| --- | --- |
| key unset, `auto` key | `INSERT` without the key column |
| key unset, `noauto` key | no statement; `crud.ErrMissingID` |
| key set | `INSERT … ON CONFLICT (pk) DO UPDATE` |

Columns tagged `immutable` are in the insert list and out of the conflict
clause, so they are written once and survive every later upsert. Columns tagged
`generated` are in neither and are read back. The model is refreshed in place
after every write.

## Why

**Why one method.** This is JPA's `save`, and it is what the owner asked for.
The point is that a caller holding a model does not have to know whether the row
exists: the same call works for both, and the round trip that would tell them
apart is avoided.

**Why `immutable` survives the conflict.** `tenant_id` and `created_at` are the
canonical cases. An upsert built from a client-supplied model would otherwise
re-tenant a row or forge its creation time on the second write — and it would do
it with a 200. Keeping them out of the `DO UPDATE SET` list means the conflict
path physically cannot touch them. The same list is what keeps `version` out
([[D-010]]).

**Why `ErrMissingID` rather than writing the zero key.** A `noauto` key means the
application owns it — a UUID from `uuid.New()`, a slug, a natural key. If the
caller forgot to set it, the alternatives are:

- write the zero value: the *first* such row succeeds and every subsequent one
  collides on the primary key, so the bug shows up later, on a different row, as
  a duplicate-key conflict. Or worse, on a table without a unique constraint,
  every such row silently overwrites the previous one.
- refuse: the caller finds out at the call site.

The second one is the only one that names the actual mistake. It is also the
safety net the ent and gorm guides lean on: vv does not run an ORM's Go-side
`Default(uuid.New)` ([[D-017]]), so this refusal is what stops a zero UUID
landing. Note the limit of the net — a zero `time.Time` is a legal value, so a
Go-side time default has no equivalent guard.

**Why the model is refreshed even without `RETURNING`.** `Save`'s promise is that
the model describes the row. Skipping the read-back when the model declares no
`generated` column saved a round trip and cost correctness: the conflict clause
leaves out every immutable column, so the caller was left holding values the
database had just refused, and a handler serialised a different document on MySQL
than on PostgreSQL.

**Why `ON CONFLICT DO NOTHING` still reads back.** When every updatable column is
immutable, the conflict clause degenerates to `DO NOTHING`, `RETURNING` yields no
row, and the model would be stale. The insert path detects the empty result and
re-reads.

**Why `Save` has no options.** It is an upsert; there is no `WHERE` clause for a
predicate to narrow. That is documented on `sqlrepo.Scope`, and it is why
`security.Gate` has to refuse rather than filter — see [[D-008]].

## What it forbids

- Do not split `Save` into `Insert` and `Update` "for clarity". The single
  method is the contract.
- Do not add immutable columns to the conflict clause. That is the whole
  protection for `tenant_id`.
- Do not make an unset `noauto` key insert a zero value.
- Do not skip the read-back on a dialect without `RETURNING`.
- Do not add options to `Save` and imply they narrow anything. If a write needs
  a row-level rule, it belongs in `security.Gate` or a service method.

## Where it lives

- `crud/sqlrepo/repository.go:repository.Save` — the three branches.
- `crud/sqlrepo/repository.go:repository.insert` — `RETURNING` path, `LastInsertID`
  path, the `DO NOTHING` re-read, and the unconditional refresh.
- `crud/sqlrepo/repository.go:repository.refresh` — re-reads through the narrowing,
  so a write that was allowed to touch only some rows cannot read back a row it
  was not allowed to touch.
- `crud/sqlrepo/repository.go:newRepository` — `insertGen`, `insertFull` and
  `upsertTail` are assembled once at bind time from `Meta.InsertGen`,
  `Meta.Insert` and `Meta.Update`.
- `crud/meta.go:buildSchema` — which field lands in which list; `generated` is in
  none of them, `immutable` and `version` are out of `Update`.
- `crud/meta.go:TagKey` — `pk`, `auto`, `noauto`, `immutable`, `generated`.
- `crud/dialect.go:Postgres.Upsert` / `crud/dialect.go:MySQL.Upsert` — the two
  conflict spellings, including the `DO NOTHING` and the MySQL no-op assignment
  for an empty update list ([[D-019]]).
- `crud/errors.go:ErrMissingID` — maps to 400 over HTTP.
- `port/service.go:DefaultService.Create` — on `POST`, `port.Sanitize` clears a
  generated key and every `generated` column before anything reaches `Save`. It
  moved out of the three bindings at phase 5 ([[D-045]], [[FL-015]]).

## Proven by

- `TestSaveUpsertsWhenKeyIsSet` in `crud/sqlrepo/repository_test.go`.
- `TestSaveInsertsWithGeneratedKeyOnPostgres` and
  `TestSaveOnMySQLUsesLastInsertID` in `crud/sqlrepo/repository_test.go`.
- `TestSaveRequiresAssignedKeyWhenNotGenerated` in
  `crud/sqlrepo/repository_test.go` — the `ErrMissingID` branch.
- `TestAUUIDPrimaryKeyWorksEverywhere` and `TestAGoSideDefaultIsNotAppliedByVV`
  in `test/integration/uuid_test.go` — the refusal doing its job on the shape it
  exists for.
- `TestSaveOnADialectWithoutRETURNINGReadsTheRowBack` in
  `crud/sqlrepo/repository_test.go`.
- `TestSaveLeavesTheCallerHoldingTheStoredRowOnEveryEngine` in
  `test/integration/dialect_edge_test.go` — the correctness the round trip buys.
- `TestUpsertLeavesTheSameRowInEveryDialect` in
  `test/integration/dialect_edge_test.go`.
- `TestSaveNeverWindsTheVersionBack` in `crud/sqlrepo/version_test.go` and
  `TestASaveCannotWindTheLockBack` in `test/integration/dialect_edge_test.go`.
- `TestSaveJudgesAFrozenFieldByItsValue` in
  `crud/decorators/security/edge_test.go`.
- `TestScopeCannotReachSave` in `crud/sqlrepo/blueprint_edge_test.go` — pins the
  documented gap rather than leaving it to be discovered.
- `TestCreateRefusesAClientChosenKeyAndGeneratedColumns` in
  `crud/http/crudfiber/handler_test.go`.

## See also

[[D-010]] [[D-012]] [[D-008]] [[D-017]] [[D-019]]
