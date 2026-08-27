# FL-022 — A migration command becomes SQL and schema

**Entry point:** `utils/vvgoose/vvgoose.go:Migrate`
**Implements:** [[UC-022]]

One entrypoint has two deliberately separate paths. File generation parses
source and writes SQL without opening a database. Runtime commands build an
isolated Goose Provider over the configured primary. The split is what lets a
developer create the migration that will bring an unavailable or empty database
forward.

## The generation path

1. **CLI dispatch** — `utils/vvgoose/vvgoose.go:newRootCommand`
   Cobra accepts flags after positional arguments, keeps `--config-path` as a
   hidden inherited flag, and decides whether stdin is a terminal.
2. **Name and config normalization** —
   `utils/vvgoose/migration.go:createMigration` and
   `utils/vvgoose/vvgoose.go:normalizeConfig`
   Empty programmatic migration fields become the same path, model root and
   history table that vvcfg's tags provide.
3. **Source discovery** — `utils/vvgoose/internal/modelscan/`
   `go/parser` walks configured roots, skips generated/test/vendor/migration
   trees, reads structs and constant `TableName` returns, and produces stable
   candidates. It never imports application code ([[D-064]]).
4. **Selection** — `utils/vvgoose/migration.go:chooseModel`
   One best candidate is automatic. Equal candidates go to Huh's searchable
   selector only for terminal input; otherwise the selected model is nil.
5. **Dialect SQL** — `utils/vvgoose/sql.go:renderMigration`
   Fields become quoted columns in PostgreSQL, MySQL/MariaDB or SQLite syntax.
   A nil or zero-field model becomes a valid statement-free Goose skeleton.
6. **Exclusive write** — `utils/vvgoose/migration.go:writeExclusive`
   The UTC timestamp and normalized name form the filename. `O_EXCL` prevents
   replacement; a collision advances the version by one second.

## The runtime path

1. **Provider construction** — `utils/vvgoose/provider.go:newProvider`
   The complete config is validated, including replica topology. A copy then
   drops the replica, `vvdb.Open` opens only primary, and `goose.NewProvider`
   receives `os.DirFS`, the engine dialect and the configured history table.
   Global Goose registration is disabled.
2. **Commands** — `utils/vvgoose/provider.go`
   `runMigrate` calls `Up`; `runStatus` calls `Status`; `runRollback` repeats
   `Down` up to the requested positive count and stops at version zero;
   `runFresh` calls `DownTo(0)` followed by `Up`.
3. **Ownership** — every path closes the `*sql.DB` it opened and joins a close
   failure with the operation failure rather than erasing either one.

## Configuration path

`utils/vvdb/config.go:Migration` stores path, model roots and history table next
to the database facts but outside the DSN. Its validation never stats a source
directory and `ReadReplica` clears it. `utils/vvcfg/vvcfg_test.go` proves YAML,
defaults, environment prefixes and raw-DSN overlay preserve that metadata.

## Tests that walk this flow

| Test | What it pins |
|---|---|
| `utils/vvgoose/migration_test.go:TestMigrationCommandAcceptsFlagsAfterTheNameAndDoesNotOpenTheDatabase` | exact CLI spelling, offline empty generation |
| `utils/vvgoose/migration_test.go:TestCreateMigrationInfersColumnsFromTheOnlyMatchingModel` | unique source model to SQL |
| `utils/vvgoose/migration_test.go:TestGeneratedSQLiteMigrationRunsThroughGoose` | generated SQL creates the inferred columns through Goose |
| `utils/vvgoose/migration_test.go:TestAmbiguousModelIsEmptyOutsideInteractiveMode` | deterministic non-interactive ambiguity |
| `utils/vvgoose/internal/modelscan/modelscan_test.go` | source evidence, fields, exclusions and matching |
| `utils/vvgoose/sql_test.go:TestRenderMigrationUsesEachEngineDialect` | four engine renderings |
| `utils/vvgoose/provider_test.go:TestSQLiteProviderLifecycle` | pending, up, applied, down, pending through Goose |
| `utils/vvgoose/provider_test.go:TestFreshReappliesMigrations` | tracked reset and re-apply |

## Traps

**`fresh` is not a blind schema drop.** It can only reverse migrations with
working Down sections and deliberately leaves untracked tables alone.

**Source matching is evidence, not type checking.** An application-defined
scalar whose underlying type cannot be known from the field expression maps to
editable `TEXT`; the generator does not compile the application to guess.
