# D-030 — A new verb on the seam is an obligation on every decorator

**Status:** accepted
**Invariant:** Every method added to `crud.Core` is either overridden by `security.gate` or has a written reason why inheriting it is safe — and a test refuses to compile the package until one of the two is true.

## The decision

`crud.Core` grew three verbs — `Aggregate`, `UpdateAll` and `SaveAll` — and each
one had to be spelled out in `security.gate` as well. The gate embeds
`crud.Core`, so a method it does not override is inherited, silently, and runs
against the plain repository with no policy at all.

Adding to the seam is therefore not a one-file change. The checklist is: the
repository implements it, the gate overrides it or documents why not, and a test
proves the override is what stops the leak.

The obligation is mechanical now. `coreVerbs` in
`crud/decorators/security/obligation_test.go` is the seam decided one method at a
time, and the test compares it against `crud.Core`'s method set by reflection: a
verb added to the seam and not to that table fails immediately, with a message
that says to decide in writing what the gate does with it. Every row marked
gated is then *driven* — a policy that refuses everything must refuse it, and
must refuse before a statement runs — so a row cannot be satisfied by an override
that checks nothing.

## Why

The embedding that makes decorators cheap to write is the same embedding that
makes them silently incomplete. `gate` overrides twelve methods and inherits
two; nothing in the type system distinguishes "inherited because it is safe"
from "inherited because nobody thought about it".

Until that test existed, this decision was enforced by nothing at all: it was a
paragraph in a directory, and the next verb added to the seam would have been
inherited silently, run against the plain repository with no policy, and broken
no test.

The failure mode is not theoretical. Each of the three was written, and each was
a leak until it was overridden:

- `Aggregate` — counted every tenant's rows. Removing the override makes
  `TestAnAggregateHonoursTheSecurityGate` report 5 where the principal owns 3.
- `SaveAll` — would have been the call that writes the most rows and checks none
  of them: no authorisation, no per-row `Inspect`, no immutable-field check.
- `UpdateAll` — the same, for a filtered write.

**Why not a compile-time obligation.** Removing the embedding and listing every
method explicitly would make the compiler enforce this. It would also make every
decorator in every consumer's codebase a wall of pass-through methods, and the
library is not willing to spend that. The trade is recorded here — and, since the
obligation test, paid at `go test` time rather than left to a reader.

The same embedding is what [[D-061]] is about one level down. There the erasure
costs a probe its datasource; here it costs a verb its policy. Neither is
expressible in the type system, and both are cheap to check.

**The two inherited verbs, in writing.** `gate` overrides twelve methods and
inherits two, and D-030 asks for the reason rather than the count:

- **`Meta`** describes the bound model and the table it maps to. It reads no row,
  takes no context and cannot be narrowed by a policy — there is nothing about it
  a principal could be allowed or refused.
- **`Tx`** runs the caller's closure inside a transaction and touches no row
  itself. What the closure does reaches the database through the same gated
  repository the caller already holds, so every statement inside it is checked by
  the twelve overrides. A gate of its own here would refuse the transaction
  rather than the work — a denial for an action nobody took.

Both are carried in `coreVerbs` with those reasons as their `reason` field, so
the words live beside the check rather than only here, and
`TestTheInheritedVerbsAreNotGated` is the control that "inherited" means
something: without it, every assertion in the table would hold just as well for a
gate that refused everything.

**Why the checks come before the statement.** `gate.SaveAll` validates the whole
batch and only then hands it down. A partially-written batch that failed halfway
would be worse than a refusal: the caller cannot tell which rows landed.

## Optional side-effect verbs obey the same rule

`InsertBatch` is intentionally not a `Core` method: adding it there would break
direct external Core implementations, while a decorator embedding Core would
compile and silently inherit the new verb — this decision's exact bypass
hazard. The optional exact capability preserves source compatibility and turns
that silent inheritance into a refusal. `crud.BatchInserter` is a typed
repository effect, and `Repo.InsertBatch` checks only the exact outer Core. It
never walks `Nexter` looking for an implementation underneath an unknown
decorator. Such a layer receives `ErrNoBatchInsertSupport` before I/O.

Every built-in decorator that may transparently preserve the verb implements it
explicitly. `security.gate.InsertBatch` authorises `Create`, works on private
copies, inspects every row and refuses a scope-only policy that cannot validate
incoming rows. `faults.enricher.InsertBatch` enriches/probes the exact verb and
then forwards it. Both preserve the opaque `BatchOption` list without resolving
or dropping it; sqlrepo resolves the storage choice exactly once. Thus both
built-in decorator orders work, while consumer middleware must make the same
written decision before opting in.

## What it forbids

- Do not add a method to `crud.Core` without deciding, in writing, what
  `security.gate` does with it.
- Do not implement a gate override that checks nothing "for now".
- Do not let a batched write check fewer rows than the unbatched one would.
- Do not walk through an unknown decorator to find an optional side-effect
  verb. Explicitly preserve its policy/observability semantics or fail closed.

## Where it lives

- `crud/repo.go:Core` — the seam.
- `crud/decorators/security/security.go:Aggregate`
- `crud/decorators/security/security.go:SaveAll`
- `crud/decorators/security/security.go:UpdateAll`
- `crud/batch.go:BatchInserter`, `InsertBatchOf`
- `crud/decorators/security/security.go:InsertBatch`
- `crud/decorators/faults/faults.go:InsertBatch`
- `crud/decorators/security/obligation_test.go:coreVerbs` — the seam decided one
  method at a time, and the reason for each of the two that are not.

## Proven by

- `TestAnAggregateHonoursTheSecurityGate` in `test/integration/aggregate_test.go`.
- `TestSaveAllIsCheckedByTheGate` in `test/integration/saveall_test.go` — a
  single foreign row refuses the batch, and nothing is written.
- `TestEveryVerbOnTheSeamIsGatedOrHasAWrittenReason` in
  `crud/decorators/security/obligation_test.go` — the totality check against
  `crud.Core`, and every gated verb driven against a policy that refuses
  everything. Removing any one override makes its subtest fail with the verb
  named.
- `TestTheInheritedVerbsAreNotGated`, same file — the control on the two rows
  that claim to inherit.
- `TestUnknownRepositoryDecoratorFailsBatchInsertionClosed` in
  `crud/batch_test.go`, and
  `TestPortableBatchSurvivesEveryBuiltInDecoratorOrder` in
  `crud/sqlrepo/insert_batch_test.go` — the optional-verb counterpart.

## See also

[[D-001]] [[D-008]] [[D-029]] [[D-061]]
