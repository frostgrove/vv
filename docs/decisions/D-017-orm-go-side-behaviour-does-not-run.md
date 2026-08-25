# D-017 — An ORM's Go-side defaults, hooks and privacy rules do not run on vv writes

**Status:** accepted
**Invariant:** A write issued through vv must be exactly the statement vv built; it must not enter an ORM's builder, callback chain or privacy layer.

## The decision

vv shares the ORM's connection and transaction, and nothing else. It builds
a statement and hands it to `Exec` or `Query`. Anything the ORM implements in Go
around its own builders — `field.Bool("active").Default(true)`,
`field.Time(…).Default(time.Now)`, a gorm `BeforeCreate` hook, an ent hook,
interceptor or privacy rule — is on a code path vv never enters.

This is a consequence of [[D-009]], not an independent choice: the whole reason
the seam works with *any* framework is that it asks for `Exec` and `Query` and
nothing more. There is no interface through which an ORM's Go-side behaviour
could be invoked.

## Why

The alternative is an adapter per ORM that goes through the ORM's builders. That
buys back the hooks and costs everything else:

- the query DSL would have to be translated into each ORM's builder API, which
  is where the nested-filter and preload semantics ([[D-005]], [[D-006]]) would
  quietly become each ORM's semantics instead;
- every ORM's builder has different holes (no `RETURNING`, no conflict clause,
  no correlated subquery), so the API would differ by backend, which is exactly
  what [[D-019]] forbids;
- an adapter would exist per ORM instead of per *driver*, and the current tree
  supports ent, gorm, sqlx, sqlc, pgx, bun, squirrel and plain `database/sql`
  with two adapters.

So the boundary is stated rather than papered over, and the guides carry the
consequence at the point where a reader would otherwise be bitten.

**The safe shapes**, best first:

1. **Put the invariant in the database.** `DEFAULT now()`, `NOT NULL`, a `CHECK`,
   a trigger. Mark the column `generated` on the model and vv never writes
   it and always reads it back. This is the only shape that holds no matter which
   code path writes the row.
2. **Put it in a `security.Policy`.** `Scope`, `Inspect` and `Immutable` are
   enforced by vv itself, so they apply to every vv write.
3. **Put it in `BeforeSave` / `BeforeUpdate`** on the handler, or in a service
   layer that embeds the repository ([[D-022]]). This covers the vv path
   only, so it is a defaulting mechanism rather than an invariant.
4. **Keep that operation on the ORM's builder** and let vv own the rest.
   The two share a transaction, so the split can run through a single request
   handler.

One safety net exists and its limit is worth knowing: an unset non-generated
primary key is `crud.ErrMissingID` ([[D-011]]), so an ORM's Go-side
`Default(uuid.New)` failing to run produces an error rather than a zero UUID. A
zero `time.Time` is a legal value, so a Go-side time default has no equivalent
guard — it lands.

## What it forbids

- Do not add an ORM-aware write path. If an adapter starts importing `ent` or
  `gorm` to invoke a builder, this decision has been reversed and the reversal
  needs its own file.
- Do not describe an ORM's hook as "supported" in any document. Both guides say
  the opposite, in a section reachable from the table of contents.
- Do not remove the `ErrMissingID` refusal as "unnecessary strictness". It is
  the only thing standing between a missing Go-side key default and a table of
  zero UUIDs.
- Do not delete the two tests below to "avoid testing a negative". A promise
  about what does *not* happen is exactly the kind that rots unnoticed.

## Where it lives

- `crud/executor.go` — the package doc: only `Exec` and `Query` cross the
  boundary.
- `crud/executor.go:Executor` — the whole surface an adapter has to supply.
- `crud/adapter/crudsql/crudsql.go` and `crud/adapter/crudpgx/crudpgx.go` — neither imports
  an ORM.
- `crud/sqlrepo/repository.go:repository.Save` — one statement, straight to the
  executor.
- `docs/usage-guides/ent.md` §16 and §8 — the gotcha, and the note that an
  ent-backed `Source` buys the *transaction*, not the callbacks.
- `docs/usage-guides/gorm.md` §16 — the same for gorm.

## Proven by

Which half is executed differs by ORM, and this is worth being precise about:

**gorm — both halves are executed.**
- `TestGormHooksDoNotRunOnVVWrites` in
  `test/integration/gorm_model_test.go` — `test/gormstore/model.go:Label` has a
  real `BeforeCreate` hook that increments a counter and defaults a column. The
  test drives gorm's own `Create` first and asserts the hook fired, so a passing
  run cannot be vacuous; then it drives vv's `Save` and asserts the counter
  did not move and the stored row carries the zero value.

**ent — the defaults half is executed, the hook half is reasoned.**
- `TestEntsGoSideDefaultsDoNotApplyToVVWrites` in
  `test/integration/ent_model_test.go` — `field.Bool("active").Default(true)`.
  ent's builder fills it in, vv's `Save` writes the model's `false`, and the
  row is re-read *through ent* so the difference is the stored value and not
  something vv failed to read back.
- No schema in `test/ent/schema/` declares `Hooks()` or `Policy()`, so the hook
  and privacy half of the claim is **reasoned, not executed**. The reasoning is
  that it is the same code path as the defaults — ent's builder — and the gorm
  test above executes precisely that shape on the other ORM. The README and
  `docs/usage-guides/ent.md` §16 both say so in as many words.

**The UUID shape.**
- `TestAGoSideDefaultIsNotAppliedByVV` in `test/integration/uuid_test.go` —
  the `ErrMissingID` net catching a missing Go-side key default.

**What still works across the boundary.**
- `TestGormSoftDeletesStayInvisibleOnBothEngines` in
  `test/integration/matrix_test.go` — gorm's soft deletes are a *column*
  convention, so `sqlrepo.Scope(crud.IsNull("DeletedAt"))` reproduces them in SQL.
  That is the safe shape, demonstrated.
- `TestEntStructInsideEntTransaction` and `TestGormModelInsideGormTransaction` —
  the transaction is shared even though the callbacks are not.

## See also

[[D-009]] [[D-011]] [[D-019]] [[D-022]] [[D-020]]
