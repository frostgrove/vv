# FL-020 — A principal becomes a policy decision

**Entry point:** `crud/decorators/security/principal.go` — `RequirePermission`, `PerAction`, `ScopeAttr`, `ScopeSubject` and their siblings
**Implements:** [[UC-020]] [[UC-004]] · **Governed by:** [[D-055]] [[D-056]] [[D-008]] [[D-004]] [[D-030]] [[D-021]]

## The path

A rule declared here is an ordinary `security.Policy`, so everything
[[FL-007]] and [[FL-008]] describe happens unchanged. What this flow adds is
where the *value* in that policy comes from.

1. **declaration** — `crud/decorators/security/principal.go` — a package
   variable, built before any request. `ScopeAttr` resolves the model field at
   this moment through `ScopeField`, so a typo panics at start-up rather than
   narrowing nothing later (`crud/decorators/security/policies.go:30`).
2. **`Gate`** — `crud/decorators/security/security.go:117` — the policy becomes
   a `crud.Middleware`, installed by `Bind`.
3. **`auth.Require`** — `auth/context.go:45` — every helper's first act. No
   principal is `ErrUnauthenticated`, which is a 401 and not a 403: nothing has
   been decided yet.
4. **the coarse check** — `gate.authorize`,
   `crud/decorators/security/security.go:145` — calls `Policy.Authorize`, which
   is what `RequirePermission`, `RequireAnyPermission`, `RequireRole` and
   `PerAction` fill in. It runs once per operation, before any SQL.
5. **`PerAction`'s undeclared verb** —
   `crud/decorators/security/principal.go:116` — an action the map does not name
   is refused even for a caller holding every permission. A verb added to the
   seam later is refused rather than inherited ([[D-030]]).
6. **the narrowing** — `gate.scoped`,
   `crud/decorators/security/security.go:187` — `Policy.Scope` is called and the
   predicate is *prepended*, so a caller cannot subtract it ([[D-004]]).
7. **`attrOf`** — `crud/decorators/security/principal.go:175` — reads one claim
   off the principal. A claim that is absent, or present and nil, is a denial
   and never a zero value: read as zero, a missing tenant compiles to `WHERE
   tenant_id = 0`, which matches nothing on most schemas and everything on one
   where 0 is a real tenant.
8. **`ScopeField`'s inherited half** —
   `crud/decorators/security/policies.go:44` — the row check and the frozen
   column. `ScopeAttr` is a wrapper rather than a fresh policy precisely to get
   them: a principal-driven scope written from scratch is [[UC-004]]'s Gap 1, a
   rule that narrows reads and leaves creates open.
9. **the answer** — `Denied`, `crud/decorators/security/security.go:141`, which
   wraps `crud.ErrForbidden` → 403. A row the narrowing hid is `crud.ErrNotFound`
   → 404, and 404 outranks both ([[D-008]], `port/kind.go:rank`).

## Where the decisions bite

- [[D-055]] — the import runs `security` → `auth` and never back. This file is
  the only place in the tree where the two meet.
- [[D-008]] — a denial is 403 and a hidden row is 404, and the second wins when
  both could apply. Nothing here changes `rank`.
- [[D-056]] — an absent principal is 401; `security.Denied`'s reason does not
  leak because `port.FaultOf` synthesises the 403 with an empty message.
- [[D-004]] — the narrowing is prepended and `Where` ANDs, so no caller option
  can widen it.
- [[D-030]] — `PerAction` refuses a verb nobody declared, which is how a new
  verb on the seam stays refused until somebody grants it.
- [[D-021]] — `ScopeAttr` and `ScopeSubject` resolve their field when declared.

## Failure modes

- **No principal.** `auth.ErrUnauthenticated` from every helper, zero statements
  executed. Pinned for all six by one table-driven test.
- **A claim the principal does not carry.** `Denied` → 403, zero statements.
- **A permission the principal lacks.** `Denied` → 403, before any SQL.
- **A verb `PerAction` does not name.** `Denied` → 403, even for a caller
  holding every permission in the map.
- **An update naming the frozen column.** Refused before any SQL, inherited from
  `ScopeField`.
- **A create outside the narrowing.** Refused by the inherited row check, zero
  statements.
- **A relation nobody narrowed.** Read whole. That is [[UC-004]]'s guarantee 11
  and it is unchanged here: `ScopeRelationAttr` is the declaration that closes
  it, and it has to be written.

## Files

| File | Role |
|---|---|
| `crud/decorators/security/principal.go` | every principal-driven policy, and `attrOf` |
| `crud/decorators/security/policies.go` | `ScopeField` and `ScopeRelationField`, which the two scope helpers wrap |
| `crud/decorators/security/security.go` | the gate itself — unchanged by this flow |
| `auth/context.go` | `Require`, the fail-closed door |
| `auth/principal.go` | `HasAll`, `HasAny`, `InAny`, and the two quantifiers' different empty case |
| `port/kind.go` | `rank`, where 404 outranks 403 |

## Tests that walk this flow

- `TestEveryPrincipalPolicyFailsClosedWithoutOne` —
  `crud/decorators/security/principal_test.go`. Six helpers, one subtest each,
  asserting `auth.ErrUnauthenticated` **and** zero statements.
- `TestRequirePermissionRefusesTheCallerThatLacksOne` — same file, with the
  control subtest that lets a qualifying caller through and asserts the
  statement reached the recorder. Without it the refusal would pass for a gate
  that refuses everything.
- `TestRequireAnyPermissionTakesTheOtherQuantifier` — same file, including both
  empty cases: all-of nothing refuses nothing, any-of nothing refuses
  everything.
- `TestPerActionRefusesAVerbNobodyDeclared` — same file. Verified by making the
  undeclared arm return nil and watching it fail.
- `TestScopeAttrNarrowsInSQLAndFreezesTheColumn` — same file: the `WHERE`
  clause, the refused create with its control, and the refused update naming the
  frozen column.
- `TestAMissingClaimIsADenialAndNotAZeroValue` — same file. Verified by making
  `attrOf` answer `int64(0)` for an absent claim and watching it fail.
- `TestScopeSubjectNarrowsToTheCallersOwnRows` — same file.
- The whole of `crud/decorators/security/security_test.go` and
  `relscope_test.go` still passes unchanged, which is what says this flow added
  a source of values rather than a second mechanism.

## See also

[[FL-019]] [[FL-007]] [[FL-008]] [[FL-011]] [[UC-004]]
