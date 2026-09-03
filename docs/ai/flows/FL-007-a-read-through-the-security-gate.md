# FL-007 — A read through the security gate

**Entry point:** `crud/decorators/security/security.go:gate.GetByID` / `:gate.Get`
**Implements:** [[UC-004]] [[UC-016]] · **Governed by:** [[D-008]] [[D-004]] [[D-003]] [[D-007]]

The gate is a `crud.Middleware` wrapped around the SQL repository at `Bind`
time. On a read it does four things in a fixed order: authorise, resolve the
scopes, cancel the projection when it will have to look at rows, and inspect
what came back.

## The path

1. **`Blueprint.Bind`** — `crud/sqlrepo/blueprint.go:Bind`
   `crud.Chain(core, security.Gate(policy))` — the first middleware ends up
   outermost, so the gate sees the call before the repository does and there is
   no way around it from above.

2. **`gate.GetByID`** — `crud/decorators/security/security.go:GetByID`
   `authorize(ctx, Read)` first (`crud/decorators/security/security.go:authorize`) — the coarse check, once per
   operation, before any SQL. A nil `Authorize` hook is skipped.

3. **Scope resolution** — `gate.scope` (`crud/decorators/security/security.go:scope`), `gate.narrow`
   (`crud/decorators/security/security.go:narrow`)
   `Policy.Scope` returns a `crud.Predicate` for *this* table.
   `Policy.RelationScopes` returns a `*crud.RelationScopes` that follows every
   hop, wrapped as `crud.NarrowRelations` (`crud/options.go:NarrowRelations`). Both are
   per-request closures, which is why they cannot live on the blueprint.
   An error from either aborts before any statement is built — the gate fails
   closed, on every door.

4. **The projection cancel** — `gate.whole` (`crud/decorators/security/security.go:whole`)
   ```go
   if !willInspect || g.p.Inspect == nil { return opts }
   return append(append([]crud.Option{}, opts...), crud.SelectAll())
   ```
   `crud.SelectAll` (`crud/options.go:SelectAll`) drops any projection applied before
   it, and it is appended **last** so it wins over the client's `?select=`.
   `Inspect` is an opaque closure: the gate cannot know which columns it reads,
   and given a projected row it compares against zero values and believes them.
   That cuts both ways — under a tenancy policy every projected read became a
   denial, and a rule that hides rows by a column value was bypassed by simply
   not selecting that column.
   The guard is deliberately two-part: a policy with no `Inspect` keeps the
   client's projection, and `Get`/`GetAll` only cancel when `InspectReads` is on.
   `Count` and `Exists` never cancel — there are no rows.

5. **`gate.loadScoped`** — `crud/decorators/security/security.go:loadScoped`
   With no scope it delegates to `Core.GetByID`. With one it cannot, because
   `GetByID` reports `ErrNotFound` for "no row" and the gate needs the same
   answer for "no row you may see". So it issues
   `GetAll(Where(scope), <relation narrowing>, Where(PK = id), Limit(1), Unsorted())`
   and turns an empty result into `crud.ErrNotFound`.
   **This is why an out-of-scope id is 404 and not 403.** A 403 confirms the row
   exists, which is exactly the fact the scope was declared to hide.
   `loadScoped` deliberately does not call `Authorize` — the caller decides which
   action is being authorised, which is what lets `gate.Update` reuse it.

6. **`gate.Get` / `gate.GetAll`** — `crud/decorators/security/security.go:Get`, `crud/decorators/security/security.go:GetAll`
   `gate.scoped` (`crud/decorators/security/security.go:scoped`) **prepends** `crud.Where(scope)` and the
   narrowing to the caller's options. In front, because `crud.Where` accumulates
   and ANDs (`crud/options.go:Where`) — a caller cannot subtract either of them by
   appending anything, and the predicate AST is closed
   (`crud/predicate.go:Predicate`), so there is no `Raw` route out either.

7. **Into the repository** — [[FL-001]]
   `repository.relScopes` (`crud/sqlrepo/repository.go:relScopes`) merges the blueprint's
   permanent narrowings with the ones this query carries;
   `MergeRelationScopes` (`crud/scope.go:MergeRelationScopes`) ANDs where both declare
   the same path or model. `repository.scoped` (`crud/sqlrepo/repository.go:scoped`) ANDs the blueprint's
   own `Scope` in front of the caller's predicate. Both the repository's rule and
   the policy's rule end up in the statement.

8. **`Inspect` / `InspectReads`** — `crud/decorators/security/security.go:Policy`
   `GetByID` always inspects the single row it returns. `Get` and `GetAll` only
   inspect when `Policy.InspectReads` is set — off by default, because `Scope` is
   the cheap way to filter a list. When it is on and a row is refused, **the
   whole call fails**; the page is not trimmed. A trimmed page would report a
   `Total` that does not match its `Items` and a pager that lies.

## Where the decisions bite

- **Out of scope is missing, not forbidden.** `loadScoped` is the single place
  that decides this. Do not "improve" it into a 403.
- **The scope goes in front and the AST is closed.** Together those two are the
  whole guarantee. A predicate the caller can append cannot remove one that is
  already there, and there is no way to inject raw SQL through the wire DSL.
- **`SelectAll` last, and only when `Inspect` exists.** Both halves matter: last
  so it beats the client's projection, conditional so a scope-only policy does
  not silently undo `?select=` and pay for every column.
- **`Scope` stops at the statement's own `FROM`.** Rows of a second table reached
  by a preload ([[FL-006]]) or a nested filter ([[FL-005]]) are not covered by
  it. `RelationScopes` is the companion, and it has to be declared —
  `security.ScopeRelationField` (`crud/decorators/security/policies.go:ScopeRelationField`)
  writes one path's worth, `Combine` merges several.
- **Combine of nothing is the zero policy.** `Combine`
  (`crud/decorators/security/policies.go:Combine`) seeds `AllowUnscopedDeleteAll`/`UpdateAll` from
  `len(ps) > 0`, because "every policy allows it" is vacuously true of no
  policies — and a role with no policies must not be a licence to truncate the
  table.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| no principal in the context | `Policy.Scope` returns an error | 403 (whatever the policy returned; `security.Denied` wraps `crud.ErrForbidden`) |
| `Authorize` denies the action | `gate.authorize` | 403 |
| id exists but is outside the scope | `gate.loadScopedWith` | **404**, never 403 |
| `Inspect` refuses a single row | `gate.GetByID` | 403 |
| `Inspect` refuses one row of a page, with `InspectReads` | `gate.inspectAll` | 403 for the whole request |
| `RelationScopes` hook errors | `gate.narrow` | the error, before any SQL |
| a relation path in the policy that does not exist | `relationField` (`crud/decorators/security/policies.go:relationField`) | panic at declaration time |

## Files

| File | Role |
|---|---|
| `crud/decorators/security/security.go` | `Gate`, `gate`, `scope`, `narrow`, `scoped`, `whole`, `loadScoped`, the read methods |
| `crud/decorators/security/policies.go` | `ScopeField`, `ScopeRelationField`, `ReadOnly`, `Freeze`, `Combine` |
| `crud/options.go` | `Where`, `SelectAll`, `NarrowRelations` |
| `crud/scope.go` | `RelationScopes`, `MergeRelationScopes`, `At`, `under` |
| `crud/predicate.go` | the closed AST that makes the scope unremovable |
| `crud/sqlrepo/repository.go` | `scoped`, `relScopes` — where the policy meets the blueprint |
| `crud/repo.go` | `Middleware`, `Chain` — the decoration order |

## Tests that walk this flow

- `TestScopeIsAppendedToEveryRead` — `crud/decorators/security/security_test.go`.
- `TestOutOfScopeIDLooksMissing` — `crud/decorators/security/security_test.go` — 404 not 403.
- `TestAnIDInAnotherTenantIsInvisibleRatherThanForbidden` — `crud/decorators/security/edge_test.go`.
- `TestAScopeThatFailsClosesEveryDoor` — `crud/decorators/security/edge_test.go` — fail-closed on every method.
- `TestReadWithoutAPrincipalFails` — `crud/decorators/security/security_test.go`.
- `TestAProjectionDoesNotTurnEveryScopedReadIntoADenial` — `crud/decorators/security/gate_edge_test.go` — the reason `whole` exists.
- `TestAProjectionCannotBypassAnInspectRule` — `crud/decorators/security/gate_edge_test.go` — the other direction.
- `TestAProjectionSurvivesAPolicyThatDoesNotInspect` — `crud/decorators/security/gate_edge_test.go` — the conditional half.
- `TestInspectReadsFailsThePageInsteadOfTrimmingIt` — `crud/decorators/security/edge_test.go`.
- `TestTheGateScopeAndTheRepositoryScopeBothApply` — `crud/decorators/security/edge_test.go`.
- `TestAPreloadIsNarrowedByThePolicy` / `TestAPreloadIsNotNarrowedWithoutTheDeclaration` — `crud/decorators/security/relscope_test.go`.
- `TestTheNarrowingReachesEveryReadPath` — `crud/decorators/security/relscope_test.go`.
- `TestCountAndExistsNarrowTheirSubqueries` — `crud/decorators/security/relscope_test.go`.
- `TestACallerCannotWidenARelationNarrowing` — `crud/decorators/security/relscope_test.go`.
- `TestCombineOfNothingIsNoMorePermissiveThanTheZeroPolicy` — `crud/decorators/security/gate_edge_test.go`.
- `TestGateComposesWithOtherMiddleware` — `crud/decorators/security/security_test.go`.
- `TestTheGatesScopeFollowsAPreload` / `TestTheGatesScopeFollowsANestedFilter` — `test/integration/gate_relscope_test.go`.

## See also

[[FL-008]] [[FL-001]] [[FL-005]] [[FL-006]] [[FL-011]]
