# D-064 — Migration generation never guesses between models

**Status:** accepted
**Invariant:** Automatic table generation uses a source model only when one
candidate is strictly best. Equal candidates are selected by the author in an
interactive terminal and produce an empty editable table migration outside it.
Application source is parsed, never executed.

## The decision

`table` input is evidence, not permission to pick the first similarly named
struct found on disk. A unique table-name or type-name match may fill the
columns automatically. Equal matches are an unresolved choice: an interactive
terminal gets a searchable selector; a non-interactive process gets a valid
empty Goose file. `--model` makes the choice explicit and `--empty` prevents
discovery entirely. Generic `migration <name>` never infers a table from its
name; `--tables` makes that request explicit.

Discovery uses Go's parser over source files. It does not load packages,
compile an unfinished application, run initializers, or call a model's
`TableName` method. Only a method whose return is statically a string constant
is read.

## Why

**Because the dangerous failure is valid SQL for the wrong struct.** Picking
the first `User` when both identity and billing declare one produces a migration
that applies successfully and creates the wrong columns. A blank file is loud
at review; a plausible table is not.

**Because generation must work before the application builds.** A migration is
often the edit that makes a newly written model usable. Requiring the whole
module to type-check creates a cycle in that workflow and makes an unrelated
broken package block database work.

**Because an empty SQL table is not portable.** PostgreSQL accepts forms that
MySQL and SQLite reject. The empty outcome is a Goose skeleton containing no
statement, not `CREATE TABLE name ()` waiting to fail during deployment.

**Because the requested entrypoint is one migration command, not a driver
assembly exercise.** The `vvgoose` satellite carries Goose, the CLI and
searchable UI, and registers the three `database/sql` driver families needed by
the four engines. That is one operational choice under [[D-051]]: a consumer
who chooses the two-line cross-engine command does not separately wire a
driver. The root module remains unchanged under [[D-033]].

## What it forbids

- Do not select the first candidate returned by a filesystem walk or map.
- Do not prompt when input is not a terminal or `--no-interactive` is present.
- Do not import or execute application packages to discover a model.
- Do not emit invalid zero-column SQL as the empty fallback.
- Do not move Goose, the terminal UI or SQL drivers into the root module.

## Proven by

- `TestAmbiguousModelIsEmptyOutsideInteractiveMode` pins the non-interactive
  outcome with two equally named models.
- `TestTableMigrationInfersColumnsFromTheOnlyMatchingModel` pins the unique
  automatic path and the relation exclusion.
- `TestDiscoverSkipsTestsGeneratedFilesAndExcludedTrees` and
  `TestDiscoverUsesConstantTableNameFromAnOrdinaryFile` pin source-only
  discovery.
- `TestMigrationCommandCreatesAnEditableFileWithoutOpeningTheDatabase` pins
  both the empty file and the absence of a database connection.

## See also

[[D-021]] [[D-033]] [[D-051]] [[FL-022]] [[UC-022]]
