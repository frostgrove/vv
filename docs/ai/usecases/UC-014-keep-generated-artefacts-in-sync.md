# UC-014 — Keep generated artefacts in sync with the model

**Actor:** the application author, and whoever reviews the pull request
**Covered by:** [[FL-010]] [[FL-004]] [[FL-015]]

## Scenario
The partial-update DTO, the typed metamodel and — when the author asks for them
— the resource's own wire shape, the mapping onto the model and the inverse of
that mapping are mechanical restatements of the model, so they are generated.
Generated code has one failure mode: somebody changes the model and does not
regenerate. The author wants that to be a red build or a refusal to start, not a
surprise in production — and wants the generator's output to be stable enough
that a diff of it is reviewable rather than noise.

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
13. And guarantee 9 in the other direction. A column the model has and a
    generated artefact does not cover is a **start-up refusal naming the
    column**, not a silent omission — and it fires with nothing regenerated,
    because the check is made against the *compiled* model rather than against
    the generator's own reading of the source. Without it, a column added and
    never regenerated for is quietly invisible to updates and to the typed query
    API, and only a regenerate-and-diff test would ever notice.
14. When a wire shape is generated, the mapping from the client's keys to the
    model is total and its inverse is checked to name every column a request can
    carry and nothing else. A column it does not cover, and an entry for
    something no request carries, both refuse to start. That is what makes an
    error body name the key the client sent rather than the model's field name.
15. Columns the author took out of the artefacts through the command line rather
    than through the model are declared in the generated output. Nothing at run
    time can see a command-line flag, so the generated file has to carry the
    list — which has a second benefit: a reader of the output can see what the
    flags did.

## Out of scope

- **Generating the model.** The model is the source of truth; this generates from
  it, never the other way.
- **Migrations.** Nothing here touches the database schema.
- **Detecting drift between the model and the actual table.** Whether the model's
  columns exist is the database's answer, not the generator's.
- **Editing the output.** It is generated; changes belong in the model or in the
  flags. Hand-editing the inverse mapping in particular is what the start-up
  check refuses.
- **Making the generated wire shape match the model's own JSON tags.** A
  generated resource has a wire shape of its own, derived from the Go field
  names, and that is the point of having an adapter layer at all. An author who
  wants the model's shape on the wire does not generate one.
- **Relations across packages.** A relation whose target model lives in another
  package is dropped from the expansion.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-010]] | the derivation rules, the flags, the relation expansion and its bounds |
| [[FL-004]] | the start-up validation that catches a stale artefact naming a field the model lost |
| [[FL-015]] | what the generated mapping and its inverse are for once the process is running |

## Status
**covered.**

Proven: every derivation rule in guarantee 2, pinned byte-for-byte, including
which columns are absent from the DTO but present in the metamodel; the
drop-entirely and keep-queryable flags; embedded-struct flattening and the
well-known ORM base struct; relation expansion, the depth bound and the cycle
cut; determinism over repeated runs and stable model ordering, for both halves of
the output; that the output compiles and its start-up validation passes, checked
by actually building and running it; the refusals in guarantee 11; and the
package-joining behaviour in guarantee 10.

**Gap 1 — closed.** Guarantees 9a and 9b are the direction that was missing, and
the check is deliberately made against the *compiled* model rather than against
the generator's own reading of the source: the two derivations have to be
independent or the check is one derivation agreeing with itself. Proven with the
untampered artefact as its control, and in both directions — a column added to
the model, and an entry deleted from the artefact.

**Gap 2 — closed.** The drift test needed no database and is no longer gated by a
suite that opens two. Both halves — the worked example and the flag-driven
stores — now run in the plain unit suite, each checking the regeneration command
against the directive the package actually carries, and the comparison itself has
a control so a helper that read one file twice could not stay green.

**Gap 3 — history, and it was half stale when it was written.** The generator did
learn about the version column, and both halves are pinned: the lock leaves the
DTO and stays in the metamodel, and the declaration the generated DTO implies is
one the repository accepts, with a DTO naming the lock refused as the control.
What remained true was the second sentence — no model in the test tree carried a
version column, so none of that ran against a real generated artefact. There is
one now, generated with the wire shape as well, so the case is reachable rather
than argued.

One smaller thing, still open: the metamodel attribute type is chosen from the
field type's *spelling*, so a named type over an integer, or a timestamp reached
through an alias, gets the generic attribute and loses its range operators.
Untested.
