# D-093 — A built-in capability is proved by its method, anything else is the driver's to declare

**Status:** accepted
**Invariant:** `cache.Supports` answers a built-in capability only by finding
the typed interface through the wrapper chain, and every other capability only
from the set the backend itself declared. A name a driver writes down can never
grant a built-in capability, and the core never needs editing for a capability
it does not know.

## The decision

`Supports` used to be a closed switch over one constant. The consequence was
not that the other capabilities were unimplemented — it was that they were
unimplementable: a driver with a compare-and-swap, a maintenance sweep or a tag
index had no way to say so, and `Definition.Requires` could demand exactly one
thing. A base package deciding, by switch, what its drivers are allowed to be
good at is the shape the extension architecture forbids.

The core therefore owns two things and no third. First, six built-in capability
interfaces — `BatchReader`, `CompareAndSwapper`, `Maintainer`, `HealthChecker`,
`TagInvalidator`, `Transactional` — each with a constant and a typed lookup that
walks decorators through `Next()`. Second, `CapabilityDeclarer`, the seam a
driver implements to name capabilities the core has never heard of. `Supports`
routes a built-in name to the typed lookup and everything else to the declared
set.

The asymmetry is deliberate. A built-in name is a promise about a method the
core will call, so only the method may prove it: a driver that declares
`batch_read` without `GetMany` would otherwise make `LookupMany` reach for a
reader that is not there. A name the core will never call is a coordination
token between a driver and the application that selected it, and there is
nothing for the core to verify beyond its shape.

The declared set is filtered, not trusted: built-in names are dropped, malformed
names are dropped, a declarer that returns more than `MaxDeclaredCapabilities`
entries contributes none, and a declarer that panics contributes none. Traversal
is bounded and cycle-safe, exactly like `BackendDescriptionOf`.

## What it forbids

- Do not add a capability by extending a switch in `cache` when the capability
  is the driver's own; declare it.
- Do not let a declared name satisfy a built-in capability.
- Do not find an optional interface with a bare type assertion — a decorator
  loses every method its own interface does not name. Use `CapabilityOf`
  ([[D-061]] is the failure this comes from).
- Do not widen `Definition.Requires` validation into a list of known names: a
  requirement the activated provider cannot meet is an activation error, and
  that is where the answer belongs.

## Where it lives

- `cache/capability.go` — the constants, the six interfaces, `Supports`,
  `CapabilityOf`, `DeclaredCapabilitiesOf`.
- `cache/declaration.go` — `normalizeCapabilities`, which now accepts any
  well-formed name and refuses duplicates and over-long lists.
- `cache/activation.go` — the requirement check against the resolved provider.
- `cache/cachememory/backend.go` — `CheckBackend`, the first built-in capability
  beyond batch reads that ships with the repository.

## Proven by

- `cache/capability_test.go` — a declared name reaches `Supports` through a
  decorator; a declared built-in name does not; every built-in capability
  survives two decorators; a panicking, unbounded or malformed declarer
  contributes nothing; a requirement beyond `batch_read` refuses activation
  when the provider cannot meet it and activates when it can.

## See also

[[D-061]] [[D-091]] [[FL-025]]
