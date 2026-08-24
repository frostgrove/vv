# `errs` — not implemented

**Tier:** contract. It is on the manifest (`crud`, `query`, `errs`, `port`), so it
imports the standard library and other contract packages and nothing else.
`make check-tiers` enforces that from the moment the first `.go` file lands.

**Module:** its own, `github.com/shardit-io/vv/errs`, on its own version line.
That is decided and it is the one thing here that is not a detail: `errs` is
meant to be adopted by services with no database, and lockstep would give them a
version bump for every CRUD bugfix in a library they do not use. It needs D-033
amended from *no external requirement* to *no third-party requirement* — see
D-036.

**What it owes:** the error contract — `Code`, `Kind`, `Path`, `Origin`,
`Violation`, `Fault`, the `Classifier`/`Resolver`/`CodeMapper` SPI, and
`MessageSource`. `errs/sqlerr/` holds the four dialect parsers and is part of the
same module.

**Open before any code:**

- The renderer seam does **not** live here. `Renderer` returning
  `(status int, header http.Header, body any)` fails the transport-neutrality
  test the errors roadmap sets for itself, because gRPC cannot implement it. It
  belongs in `http/crudhttp`.
- No contract package constructs a `Fault`. `crud` may not import `errs`
  (D-016's surviving half), but `query` may — so without the rule there would be
  two classification paths for library-origin errors.
- `Violation` carries no `Kind`; the sort derives it from `Code` through the
  wired `Codes` value, which is why that value is wired rather than global.

**Governed by:** [ROADMAP-errors.md](../ROADMAP-errors.md) §5, and its phase 1.
