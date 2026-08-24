# UC-014 — Keep generated artefacts in sync with the model

**Actor:** the application author, and whoever reviews the pull request
**Covered by:** [[FL-010]] [[FL-004]]

## Scenario
The partial-update DTO and the typed metamodel are mechanical restatements of the
model, so they are generated. Generated code has one failure mode: somebody
changes the model and does not regenerate. The author wants that to be a red
build, not a surprise in production — and wants the generator's output to be
stable enough that a diff of it is reviewable rather than noise.

## What must hold

1. Regenerating is a single directive next to the model and a single command over
   the package.
2. The DTO is derived from the model by stated rules: the key, columns the
   database owns, and columns declared read-only are not writable and do not
   appear; a nullable column becomes a three-state field and everything else a
   two-state one; the wire name follows the Go name.
3. The metamodel covers every column, including the ones the DTO leaves out, so a
   column can be filterable and sortable without being patchable.
4. Fields can be dropped entirely, or kept queryable but not writable, from the
   command line — for a model the author cannot annotate.
5. Relations expand into nested attribute groups, bounded by a depth setting, and
   the expansion never walks back into a model already on the path.
6. Output is deterministic. The same input produces the same bytes, run after
   run, with models in a stable order — so a diff is a real change.
7. The output compiles, and the start-up validation it contains agrees with the
   model it was generated from.
8. A stale artefact is a **test failure**: regenerating into a temporary
   directory and comparing against what is checked in is a test, and it fails
   with a message naming the command to run.
9. A stale artefact that names something the model no longer has is caught again
   at process start-up, before any request: the metamodel's validation and the
   repository declaration's validation both fail loudly.
10. Generating into a package the author owns is supported, so the artefacts can
    live next to another generator's output without colliding with it and without
    being overwritten by it. The generator joins the package already declared
    there rather than inventing a second one.
11. The generator refuses a configuration that cannot work — writing into another
    package without being told how to import the models, or finding nothing to
    generate — rather than emitting a file that does not compile.
12. The generator ignores test files and its own previous output, so it is
    idempotent and does not feed on itself.

## Out of scope

- **Generating the model.** The model is the source of truth; this generates from
  it, never the other way.
- **Migrations.** Nothing here touches the database schema.
- **Detecting drift between the model and the actual table.** Whether the model's
  columns exist is the database's answer, not the generator's.
- **Editing the output.** It is generated; changes belong in the model or in the
  flags.
- **Relations across packages.** A relation whose target model lives in another
  package is dropped from the expansion.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-010]] | the derivation rules, the flags, the relation expansion and its bounds |
| [[FL-004]] | the start-up validation that catches a stale artefact naming a field the model lost |

## Status
**partially covered — guarantee 8 has a hole in exactly the direction that
matters, and guarantee 2 has a bug.**

Proven: every derivation rule in guarantee 2, pinned byte-for-byte, including
which columns are absent from the DTO but present in the metamodel; the
drop-entirely and keep-queryable flags; embedded-struct flattening and the
well-known ORM base struct; relation expansion, the depth bound and the cycle
cut; determinism over repeated runs and stable model ordering; that the output
compiles and its start-up validation passes, checked by actually building it; the
refusals in guarantee 11; and the package-joining behaviour in guarantee 10.

**Gap 1 — the drift test only catches a *removed* field.** Both validations in
guarantee 9 check that everything the artefact names still exists on the model.
Neither checks the other direction. Add a column and forget to regenerate, and
the application starts cleanly with a column that is simply invisible to updates
and to the typed query API — no error, no warning, no failing build. Only the
regenerate-and-diff test in guarantee 8 catches it.

**Gap 2 — and that test is split, with the harder half behind Docker.** The
in-repository worked example is diffed in the plain unit suite, so a default-flag
regression is caught by an ordinary test run. The two artefacts generated with
*real* flag combinations — into another package, with read-only fields, over an
ORM's generated types — are diffed only in the integration suite, whose harness
opens PostgreSQL and MySQL and aborts the whole binary if either is unreachable.
A contributor with no containers runs the unit suite, sees green, and does not
learn that the flag-driven artefacts are stale. That test needs no database at
all; it is gated by the suite it happens to live in.

**Gap 3 — the generator does not know about the version column.** A model
declaring one generates a DTO containing it, and the repository declaration then
refuses that DTO at start-up. So the generated output for such a model does not
work, and nothing in the test tree has a version column to notice. This is a bug
in guarantee 2, not a documented limitation.

One smaller thing: the metamodel attribute type is chosen from the field type's
*spelling*, so a named type over an integer, or a timestamp reached through an
alias, gets the generic attribute and loses its range operators. Untested.
