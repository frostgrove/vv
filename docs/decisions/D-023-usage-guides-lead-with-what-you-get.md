# D-023 — The usage guides lead with what you get, not with how to set it up

**Status:** accepted
**Invariant:** Both usage guides must open with Part I — the resulting API — and put installation, wiring and configuration in Part II; the table of contents must say "read this first" on Part I.

## The decision

`docs/usage-guides/ent.md` and `docs/usage-guides/gorm.md` are both split:

- **Part I — what you get.** The two-line declaration, the routes that fall out,
  the query DSL, the handler config that bounds it, the typed Go API, a real
  transaction-shaped usecase, and what the ORM still owns.
- **Part II — how to set it up.** `go get`, the datasource, the repository
  declaration, codegen, mounting, the service layer, tenancy, relations, testing,
  gotchas.

The two files are structurally parallel — sections 1–6 and 10–16 line up — so a
reader who has read one can navigate the other.

This is an owner correction. The guides were originally set-up-first and were
restructured.

## Why

The reader arriving at these guides already has a working ORM. They are not
evaluating whether to use a database; they are deciding whether this layer is
worth adding to a codebase that works. That decision is made on the *result*.

A setup-first guide asks them to spend attention on `entc.go` feature flags, a
`crud.Source` implementation and a `go:generate` line before showing them a
single thing they get for it. The most likely outcome is that they stop reading
at the feature flag, and the thing that would have convinced them is in section
12.

Leading with the result inverts that: section 1 is two lines of Go, section 2 is
the full route table and the filter DSL, and by section 5 they have seen the
shape of a serious endpoint. Only then does the guide ask for anything.

There is a second, quieter reason. Part I is written entirely in terms the reader
already has — their entity struct, their table constant, an HTTP request — so it
doubles as the reference a reader comes back to. Part II is read once.

## What it forbids

- Do not move installation, the feature flag, or the datasource declaration into
  Part I. `go get` lives at the top of Part II, under "Before you start".
- Do not apply the change to one guide and not the other. They are read as a
  pair and the parallel numbering is what makes that work.
- Do not let Part I accumulate configuration. Section 3 is the handler config
  and it is there because *bounding* the DSL is part of what you get; anything
  that is a wiring step belongs in Part II.
- Do not remove the "· read this first" marker on Part I in the table of
  contents. It is what stops a reader scrolling straight to the setup steps out
  of habit.

## Where it lives

- `docs/usage-guides/ent.md` — the two-part table of contents, `# Part I — what
  you get` and `# Part II — how to set it up`.
- `docs/usage-guides/gorm.md` — the same structure, same numbering for the
  sections that correspond.
- `README.md` — links both guides with a one-line description of the promise
  each makes.

## Proven by

A document structure is not something a Go test can assert, and none does. What
*is* tested is that the guides do not drift from the code — which is the part
that would otherwise rot:

- `TestEntGeneratedStructIsAModel`, `TestEntGeneratedMetamodel`,
  `TestEntStructReadsThroughVV` and `TestEntStructWritesThroughVV` in
  `test/integration/ent_model_test.go`, plus
  `TestEntModelThroughVVOnBothEngines` in
  `test/integration/matrix_test.go` — Part I of the ent guide, executed.
- `TestEntUsecaseDSLInsideTransaction` in `test/integration/usecase_test.go` —
  §5 of the ent guide, executed.
- `TestTheGormGuidesHeadlineDocumentRuns` in
  `test/integration/gorm_model_test.go` — the gorm guide's headline query
  document, run against a live database. The name says what it is for.
- `TestGormModelThroughVVOnBothEngines` and
  `TestGormUsecaseDSLInsideTransactionOnBothEngines` in
  `test/integration/matrix_test.go`.
- `TestGormMappingMatchesGorm` in `test/integration/gorm_model_test.go` — the
  per-entity mapping test both guides tell the reader to copy.
- `TestEntsGoSideDefaultsDoNotApplyToVVWrites` and
  `TestGormHooksDoNotRunOnVVWrites` — §16 of each guide ([[D-017]]).

Note a live inconsistency worth fixing, though it is outside this decision's
scope: `README.md` links the guides as `docs/ent.md` and `docs/gorm.md`, and
they now live at `docs/usage-guides/ent.md` and `docs/usage-guides/gorm.md`.

## See also

[[D-017]] [[D-021]] [[D-020]]
