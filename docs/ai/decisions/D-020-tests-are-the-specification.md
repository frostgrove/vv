# D-020 — Tests are the specification

**Status:** accepted
**Invariant:** A test that could pass without the behaviour it names being present must sit next to a control case that fails when the behaviour disappears.

## The decision

The test suite is where every claim in this documentation is settled. Three
rules follow from that, and all three are visible in the tree:

1. **A test name is a sentence stating the behaviour.**
   `TestAnIDInAnotherTenantIsInvisibleRatherThanForbidden`, not `TestGetByID_2`.
   The name is the specification; the body is the proof.
2. **A test that could pass vacuously carries a control case.** A negative
   assertion — "the hook did not fire", "the other tenant's rows did not come
   back" — passes when the feature is missing *and* when the fixture is wrong.
   The control drives the same path with the guard removed and asserts the
   leak is there, so a passing positive means something.
3. **No `t.Parallel()`.** There are zero calls to it in the tree.

## Why

**Why the control case.** This is the rule that earns its keep. Consider
`TestTheGatesScopeFollowsAPreload`: it asserts that with the policy declared,
only tenant 1's children come back. That assertion passes if the narrowing works
— and equally if the fixture accidentally seeded only tenant 1's children, or if
the preload silently returned nothing, or if the relation was never wired. The
subtest `not declared` runs the identical query under a policy that scopes the
table but *not* the relation, and fails with

> the leak closed itself, so the case above proves nothing

That is the only construction that distinguishes "the guard works" from "there
was nothing to guard against". The same shape appears wherever the tree makes a
negative claim:

- `test/integration/gate_relscope_test.go:103` and `:151` — the preload and the
  nested-filter leaks.
- `crud/decorators/security/relscope_test.go:101` — "the preload narrowed
  itself — the positive test above proves nothing".
- `test/integration/ent_model_test.go:244` — "ent did not apply its own default,
  so this test cannot tell the two paths apart".
- `test/integration/gorm_model_test.go:367` — "gorm's own Create did not fire
  the hook, so this test cannot tell anything apart".
- `crud/decorators/security/gate_edge_test.go:180` — `Combine` folds
  `AllowUnscopedDeleteAll` with `&&`, and "every policy allows it" is vacuously
  true of *no* policies; the test builds a real policy list so the vacuous case
  is not what is being measured.

**Why no `t.Parallel()`.** The integration suite shares tables. `egWipe` and
`truncate` empty them between cases, so two parallel tests would see each
other's rows and fail in a way that depends on scheduling — the worst failure
mode a test suite can have, because the response is to re-run it. Isolating them
would mean a schema per test, which costs more setup time than the parallelism
saves. The unit tests are fast enough that the rule can be uniform rather than
per-package, and a uniform rule is one nobody has to check.

**Why the integration suite runs against real engines.** `crud/crudtest` records
statements without a database, which is right for asserting *what* SQL was built.
It cannot tell you whether the SQL is accepted, whether `RETURNING` came back,
whether the collation matched, or whether two transactions actually took turns.
`docker-compose.yml` brings up PostgreSQL and MySQL, and `make integration`
starts them before running the suite with `-tags=integration`. The build tag is
what keeps those files out of `go test ./...`, so `make unit` runs the whole
library's tests on a machine with no Docker and no database.

## What it forbids

- Do not delete a control case as redundant. It is the half that makes the other
  half mean something.
- Do not add `t.Parallel()` to anything in `test/integration`. The tables are
  shared.
- Do not rename a test to something shorter. The name is the claim.
- Do not assert only the happy path for a negative claim. If the test says
  something does *not* happen, prove first that it can.
- Do not move an integration test into the unit suite because it is slow. The
  recorder cannot answer what a real engine answers ([[D-019]]).

## Where it lives

- `scripts/modules.sh:unit` — `go test ./...`, no database.
- `scripts/vv:integration` — `up` then `go test -tags=integration -count=1
  ./test/...`.
- `docker-compose.yml` — PostgreSQL and MySQL, with `--wait`.
- `test/integration/main_test.go:truncate` and `test/integration/edge_test.go:egWipe`
  — the shared-table reset that `t.Parallel()` would break.
- `test/integration/suite.go` — the conformance suite each driver runs.
- `test/integration/matrix_test.go` — the same query across every provider.
- `crud/crudtest/recorder.go` — the in-memory source for the unit half.
- `*_edge_test.go` throughout the tree — by convention, the file where the
  hostile and degenerate cases live, separate from the happy-path file.
- `crud/query/hostile_test.go` — the adversarial input suite.

## Proven by

This decision is about the tests, so its evidence is the tests themselves:

- `TestTheGatesScopeFollowsAPreload` and `TestTheGatesScopeFollowsANestedFilter`
  in `test/integration/gate_relscope_test.go` — the clearest control-case
  example in the tree. Grep the file for `proves nothing`.
- `TestAPreloadIsNotNarrowedWithoutTheDeclaration` in
  `crud/decorators/security/relscope_test.go`.
- `TestEntsGoSideDefaultsDoNotApplyToVVWrites` in
  `test/integration/ent_model_test.go` and
  `TestGormHooksDoNotRunOnVVWrites` in
  `test/integration/gorm_model_test.go` — both prove the ORM's own path works
  before proving vv's does not go through it.
- `TestCombineOfNothingIsNoMorePermissiveThanTheZeroPolicy` in
  `crud/decorators/security/gate_edge_test.go`.
- `TestAScopedSaveOfAnUnusedIDIsStillAnInsert` in
  `crud/decorators/security/gate_edge_test.go` — the control for
  `TestAScopeWithoutInspectStillRefusesAnOverwriteOfAHiddenRow`.
- `TestSearchWithNothingToSearchProducesNoPredicate` in
  `crud/query/compile_test.go` — the control for the search tests.

## An honest note on "mutation-checked"

The owner's stated process is that the suite is mutation-checked: a change is
made to the implementation and the suite must fail. Nothing in the repository
reproduces that — there is no mutation-testing tool configured, no `go-mutesting`
or `gremlins` config, and no CI file that runs one. The claim is therefore about
how the tests were *written*, not something a future agent can re-run.

What *is* reproducible is the control-case discipline above, and it is the same
idea made permanent: a control case is a mutation that has been checked in.

## See also

[[D-007]] [[D-017]] [[D-019]] [[D-014]] [[D-021]]
