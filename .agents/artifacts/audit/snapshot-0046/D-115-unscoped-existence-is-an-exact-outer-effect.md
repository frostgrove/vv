# D-115 — Unscoped existence is an exact-outer effect, and a gate answers it inside its own scope

**Status:** accepted
**Invariant:** `crud.ExistsUnscopedOf` answers from the exact `Core` it is handed and from nothing underneath it. A decorator that narrows rows and forwards the verb answers within its own narrowing; a decorator that says nothing about the verb makes the call fail closed.

## The decision

[[D-061]] separated discovery from execution: `Nexter` and `SourceUnwrapper`
walks find *where* something is, and an executable effect is taken from the exact
outer value or refused. `InsertBatchOf` and `UnsafeBulkInserterOf` were written
that way. `ExistsUnscopedOf` was not — it walked `Nexter` looking for something
underneath that could answer, which is the one shape that decision forbids.

Now:

```go
func ExistsUnscopedOf[M any, ID comparable](c Core[M, ID], ctx context.Context, options ...Option) (bool, error, bool) {
	x, ok := c.(UnscopedExister[M, ID])
	if !ok {
		return false, nil, false
	}
	found, err := x.ExistsUnscoped(ctx, options...)
	return found, err, true
}
```

Each built-in wrapper decides in writing what it does with the verb:

| Wrapper | Decision |
|---|---|
| `sqlrepo.repository` | implements it — the one layer that can run the statement |
| `security.gate` | implements it: authorises `Read`, applies **its own** scope and relation scopes, forwards to the exact inner core, and answers `crud.ErrNoUnscopedExists` when that core cannot |
| `faults.enricher` | implements it: forwards the exact verb, enriches the error, and answers `crud.ErrNoUnscopedExists` over a core that cannot, exactly as its `InsertBatch` does |
| `crud.Base` | deliberately does not — a consumer decorator built on it is unknown for this verb and fails closed |

`security.saveTarget` treats "not supported" and `ErrNoUnscopedExists` the same
way and answers `crud.ErrNotFound`, which is what it already did when the verb
was missing.

## Why

**Because the walk was an existence oracle across the layer that exists to hide
rows.** The probe's whole job is to tell "not yours" from "not there" for an
assigned key, and the gate reads its one bit ([[FL-008]]). With a second
narrowing decorator below — the shape a tenancy or audit extension is — the walk
answered that question *from the raw table*. So a caller who could see none of
tenant B's rows could still learn that tenant B holds id 7, by watching an
assigned-key create turn into `ErrNotFound` instead of a row. One bit per guess
is still an oracle, and [[D-008]] refuses to leak exactly this bit through the
status code.

**Because "unscoped" was ambiguous and the ambiguity was load-bearing.** It means
*unscoped by the caller*, never *unscoped by the layer being asked*. A gate below
is the honest universe for the question, and it is the only layer that can say so.
Answering inside its own scope is not a weaker answer than the raw table's — it is
the correct one, and it is the first time two stacked gates agree about what
exists.

**Because six roadmaps were blocked on it.** The multitenancy, extension
architecture, audit, PostgreSQL event-sourcing, OpenTelemetry and product
revisions each name this walk as a thing to resolve before a new cross-cutting
decorator may rely on the CRUD chain. It is the last executable effect in the
repository that did not follow [[D-061]], so closing it is what makes the chain
an extension point rather than a shape that happens to work for one decorator.

**Why the gate authorises `Read` first.** Every other verb it owns does
([[D-030]]), and the probe reaches a statement. The caller has already passed the
same check on the read above it, so this costs a repeated policy call and buys
the property that no path into the gate skips authorisation.

## What it forbids

- Do not restore the `Nexter` walk in `ExistsUnscopedOf`, or add one to any other
  executable effect.
- Do not implement `ExistsUnscoped` on a narrowing decorator without applying
  that decorator's own narrowing. Forwarding the verb raw is the leak this
  decision closes.
- Do not answer the probe from `crud.Base`. A decorator that has not decided is
  not a decorator that agrees.
- Do not turn a "cannot answer" into a hard failure at the call site: the gate's
  contract is that an unanswerable probe means `crud.ErrNotFound`, not a 500.

## Where it lives

- `crud/executor.go` — `UnscopedExister`, `ExistsUnscopedOf`.
- `crud/errors.go` — `ErrNoUnscopedExists`.
- `crud/decorators/security/security.go:ExistsUnscoped`, `:saveTarget`
- `crud/decorators/faults/faults.go:ExistsUnscoped`
- `crud/sqlrepo/repository.go:ExistsUnscoped` — the implementor.

## Proven by

- `TestUnscopedExistenceIsAnsweredOnlyByTheExactOuterCore` in
  `crud/unscoped_test.go` — the repository answers, and both an opaque decorator
  and one built on `crud.Base` report "cannot answer" and run no statement. The
  `crud.Base` subtest is the one that fails on the old walk, and the repository
  case is the control that the helper has not simply become "no to everything".
- `TestTheGateAnswersTheUnscopedProbeWithinItsOwnScope` in
  `crud/decorators/security/unscoped_test.go` — the probe's `WHERE` carries the
  gate's own scope *and* the caller's narrowing. Dropping the scope makes it fail
  with the leak named. Its subtest is the control: a scope that cannot be
  resolved runs no statement at all.
- `TestAnAssignedKeySaveRefusesWhenTheCoreBelowCannotAnswerTheProbe`, same file —
  a gate over an opaque core answers `ErrNotFound` after one statement and never
  reaches a write, with a control that the same save over an answerable core does
  reach one.
- `TestTheEnricherForwardsUnscopedExistence` in
  `crud/decorators/faults/unscoped_test.go` — transparent forwarding, and the
  fail-closed branch over an opaque core.

## See also

[[D-008]] [[D-030]] [[D-061]] [[FL-008]] [[UC-004]]
