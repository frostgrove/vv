# D-011 — `Save` is JPA-shaped: no key inserts, a key upserts

**Status:** accepted
**Invariant:** `Save` must insert when the primary key is unset and the key is database-generated, upsert when the key is set, and return `crud.ErrMissingID` when the key is unset and is not database-generated. The upsert must reach the row the key names and no other, and no row the repository's declared `Scope` hides.

## The decision

One method, three branches:

| model state | statement |
| --- | --- |
| key unset, `auto` key | `INSERT` without the key column |
| key unset, `noauto` key | no statement; `crud.ErrMissingID` |
| key set | `INSERT … ON CONFLICT (pk) DO UPDATE` |
| key set, and the dialect's upsert is not key-targeted, or a `Scope` is declared | `UPDATE … WHERE pk = ? [AND scope]`, then a probe, then `INSERT` — one transaction |

Columns tagged `immutable` are in the insert list and out of the conflict
clause, so they are written once and survive every later upsert. Columns tagged
`generated` are in neither and are read back. The model is refreshed in place
after every write.

## Why

**Why the fourth row exists.** The first three rows are the contract; the fourth
is what it costs to keep the contract true on an engine that cannot express it in
one statement, or on a repository that narrowed itself.

- MySQL and MariaDB have no key-targeted upsert. `ON DUPLICATE KEY UPDATE` fires
  on *every* unique index, so `Save(&User{ID: 1, Email: "a@b"})` rewrites whichever
  row already holds `a@b` — a different row, in place of the one the caller named,
  with a 200. `crud.UpsertTargetsPrimaryKey` is the switch, and it reads the
  `UpsertScope` capability the probe already relied on ([[D-042]]).
- `sqlrepo.Scope` is a permanent narrowing. A repository declared for one tenant
  had protected reads and an unprotected upsert: a key from another tenant
  overwrote their row, because an upsert has no `WHERE` clause. Rather than grow
  `Save` an option ([[D-060]] is the same argument one layer up), the declaration
  gives up the single statement and the write becomes an `UPDATE` that carries the
  scope.

The insert half of the fourth row is **not** narrowed, and that is deliberate: a
scope decides which existing row a write may reach, not which values a new row may
hold. Refusing an insert whose values fall outside the scope is `security.Gate`'s
`Inspect`, and it needs the request to decide.

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

**Why `Save` has no options.** A caller does not get to narrow a write: the
repository's own declaration does. `security.Gate` still refuses rather than
filters, because its narrowing is per-principal and arrives with the request —
see [[D-008]].

## The explicit verbs under Save

`Save` is the default and stays a facade. Beside it, and only for a caller who
names them, sit two explicit verbs:

| verb | statement | what it refuses |
| --- | --- | --- |
| `Create` | `INSERT` only, never a conflict tail | an existing key is the engine's duplicate-key 409 |
| `Replace` | the fourth row's sequence with `AND version = ?` on the update and `version = version + 1` in the `SET` | a row somebody else advanced is `crud.ErrStaleVersion` |

`Patch` was **not** added: `Update` already is it — load, diff, write only what
changed, version-checked ([[D-010]]). A second spelling of one verb is how a
codebase ends up with two of everything.

Both are optional capabilities (`crud.Creator`, `crud.Replacer`), resolved on the
**exact outer Core** the way `InsertBatch` is ([[D-083]]) rather than by walking
the decorator chain. Walking it would step past `security.Gate` and turn an
explicit verb into a way around authorisation; refusing with
`ErrNoCreateSupport` / `ErrNoReplaceSupport` says so out loud instead. A
decorator that wants to offer them forwards them deliberately.

This is not the split this decision forbids. Nothing routes through `Create` or
`Replace` unless the caller wrote the word: `port`, the HTTP bindings and the gate
all still call `Save`.

## What it forbids

- Do not split `Save` into `Insert` and `Update` "for clarity". The single
  method is the contract, and it is what every transport calls. `Create` and
  `Replace` are additions beside it, reachable only by name.
- Do not add immutable columns to the conflict clause. That is the whole
  protection for `tenant_id`.
- Do not make an unset `noauto` key insert a zero value.
- Do not skip the read-back on a dialect without `RETURNING`.
- Do not add options to `Save` and imply they narrow anything. If a write needs
  a *per-request* row-level rule, it belongs in `security.Gate` or a service method.
- Do not emit a targetless upsert from `Save` on a dialect that has no
  key-targeted one. `crud.UpsertTargetsPrimaryKey` is the question to ask.
- Do not narrow the insert half of a scoped `Save` with the scope. The row does
  not exist yet, so there is nothing to protect and a great deal to break.
- Do not reach `Create` or `Replace` by walking the decorator chain.

## Where it lives

- `crud/sqlrepo/repository.go:repository.Save` — the three branches and the fork
  into the fourth; `:Create` and `:Replace` — the explicit verbs.
- `crud/sqlrepo/upsert.go:repository.upsertByPrimaryKey` — the fourth row's
  sequence; `:needsConditionalUpsert` — when it is taken.
- `crud/sqlrepo/blueprint.go:Blueprint.writeScope` — the scope as declared, before
  soft delete adds its own predicate to the read scope.
- `crud/write.go` — `Creator`, `Replacer`, `CreateOf`, `ReplaceOf` and the two
  `Repo` methods that refuse when a decorator swallowed them.
- `crud/dialect.go:UpsertTargetsPrimaryKey`, `:UpdateCountsChangedRowsOnly` — the
  two dialect questions the fourth row asks.
- `crud/sqlrepo/repository.go:repository.saveReturning` / `:saveWithoutReturning` —
  the `RETURNING` path, the `LastInsertID` path and the unconditional refresh.
- `crud/sqlrepo/repository.go:repository.refresh` — re-reads through the narrowing,
  so a write that was allowed to touch only some rows cannot read back a row it
  was not allowed to touch.
- `crud/sqlrepo/repository.go:newRepository` — `insertGen`, `insertFull` and
  `upsertTail` are assembled once at bind time from `Meta.InsertGen`,
  `Meta.Insert` and `Meta.Update`.
- `crud/meta.go:buildSchema` — which field lands in which list; `generated` is in
  none of them, `immutable` and `version` are out of `Update`.
- `crud/meta.go:TagKey` — `pk`, `auto`, `noauto`, `immutable`, `generated`.
- `crud/dialect.go:Postgres.Upsert` — the conflict spelling `Save` uses, including
  the `DO NOTHING` degenerate form. `crud/dialect.go:MySQL.Upsert` still renders
  `ON DUPLICATE KEY UPDATE` for a dialect that declares the key-targeted
  capability, and `Save` no longer reaches it ([[D-019]], [[D-042]]).
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
- `TestScopeNarrowsTheRowASaveMayReachButNotTheRowItCreates` in
  `crud/sqlrepo/blueprint_edge_test.go` — the half the scope deliberately does not
  reach.
- `TestSaveNeverReachesARowByAKeyTheCallerDidNotName`,
  `TestSaveInsertsWhenNoRowCarriesTheKeyOnADialectThatCannotTargetTheKey`,
  `TestSaveLeavesAChangelessRowAloneRatherThanInsertingASecondOne` and
  `TestSaveOnlyRunsItsWholeSequenceUnderOneTransaction` in
  `crud/sqlrepo/upsert_test.go` — the fourth row on a dialect that cannot target
  the key.
- `TestSaveCannotOverwriteARowOutsideTheDeclaredScope`,
  `TestSaveAllCannotOverwriteARowOutsideTheDeclaredScope` and
  `TestSaveReadsTheRowBackThroughTheDeclaredScope` in
  `crud/sqlrepo/upsert_test.go` — the fourth row on a repository that narrowed
  itself, and the read-back that follows it.
- `TestADialectThatTargetsThePrimaryKeyKeepsTheSingleStatementUpsert` in
  `crud/sqlrepo/upsert_test.go` — the control: nothing was given up where nothing
  was wrong.
- `TestAConditionalSaveIsRefusedOverBudgetBeforeAnyStatement` in
  `crud/sqlrepo/upsert_test.go` — [[D-079]] still bites on the multi-statement branch.
- `TestCreateInsertsAndLetsAnExistingKeyCollide`,
  `TestReplaceRefusesAWriteAgainstARowSomebodyElseAdvanced`,
  `TestReplaceCreatesTheRowWhenNobodyHoldsTheKey`,
  `TestReplaceRequiresTheKeyItIsReplacing` and
  `TestADecoratorThatDoesNotForwardTheExplicitVerbsRefusesThemOutLoud` in
  `crud/sqlrepo/upsert_test.go` — the explicit verbs.
- `TestCreateRefusesAClientChosenKeyAndGeneratedColumns` in
  `crud/http/crudfiber/handler_test.go`.

## See also

[[D-010]] [[D-012]] [[D-008]] [[D-017]] [[D-019]] [[D-042]] [[D-079]] [[D-083]]
