# `errs` — not implemented

**Tier:** contract. It is on the manifest (`crud`, `query`, `errs`, `port`), so it
imports the standard library and other contract packages and nothing else.
`make check-tiers` enforces that from the moment the first `.go` file lands.

**Module:** its own, `github.com/shardit-io/vv/errs`, on its own version line —
**but not yet.** The decision stands ([[D-036]]): `errs` is meant to be adopted by
services with no database, and lockstep would give them a version bump for every
CRUD bugfix in a library they do not use.

The timing is forced by the toolchain, and it was measured rather than assumed.
Split before the first tag, the root requires `errs@v0.0.0`, and every module
that requires the root then fails to walk its module graph — `go list -m all`
cannot read `errs/go.mod` at a revision that does not exist. None of the obvious
answers work: an explicit `require` in each satellite does not help, `go mod
tidy` under a transient replace does not help, and `go work edit -replace` is
refused outright with *"workspace module … is replaced"*. Only a fetchable
version fixes it, and there is no remote and no tag.

**So `errs` starts as a package in the root module and becomes its own module in
the same change as the first tag.** Nothing about the contract changes; the
`go.mod` arrives later than the code.

**What it owes:** the error contract — `Code`, `Kind`, `Path`, `Origin`,
`Violation`, `Fault`, the `Classifier`/`Resolver`/`CodeMapper` SPI, and
`MessageSource`.

**`errs/sqlerr/` already exists**, and has no `TODO.md` because it has real code.
Phase 0 put the captured error corpus there — the types, and four checked-in
fixtures under `testdata/corpus`. It landed before `errs` itself for a reason
worth knowing: the corpus is what the four dialect parsers are written against,
the parsers are unit-tested, and a unit test in this module cannot import the
`test` module where the drivers live. So the fixtures had to be here and the
generator had to go the other way. `make corpus` recaptures them.

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
