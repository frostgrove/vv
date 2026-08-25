# D-030 — A new verb on the seam is an obligation on every decorator

**Status:** accepted
**Invariant:** Every method added to `crud.Core` is either overridden by `security.gate` or has a written reason why inheriting it is safe.

## The decision

`crud.Core` grew three verbs — `Aggregate`, `UpdateAll` and `SaveAll` — and each
one had to be spelled out in `security.gate` as well. The gate embeds
`crud.Core`, so a method it does not override is inherited, silently, and runs
against the plain repository with no policy at all.

Adding to the seam is therefore not a one-file change. The checklist is: the
repository implements it, the gate overrides it or documents why not, and a test
proves the override is what stops the leak.

## Why

The embedding that makes decorators cheap to write is the same embedding that
makes them silently incomplete. `gate` overrides eleven methods and inherits the
rest; nothing in the type system distinguishes "inherited because it is safe"
from "inherited because nobody thought about it".

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
library is not willing to spend that. The trade is recorded here instead, which
is the point of this directory.

**Why the checks come before the statement.** `gate.SaveAll` validates the whole
batch and only then hands it down. A partially-written batch that failed halfway
would be worse than a refusal: the caller cannot tell which rows landed.

## What it forbids

- Do not add a method to `crud.Core` without deciding, in writing, what
  `security.gate` does with it.
- Do not implement a gate override that checks nothing "for now".
- Do not let a batched write check fewer rows than the unbatched one would.

## Where it lives

- `crud/repo.go:Core` — the seam.
- `crud/decorators/security/security.go:Aggregate`
- `crud/decorators/security/security.go:SaveAll`
- `crud/decorators/security/security.go:UpdateAll`

## Proven by

- `TestAnAggregateHonoursTheSecurityGate` in `test/integration/aggregate_test.go`.
- `TestSaveAllIsCheckedByTheGate` in `test/integration/saveall_test.go` — a
  single foreign row refuses the batch, and nothing is written.

## See also

[[D-001]] [[D-029]] [[D-008]]
