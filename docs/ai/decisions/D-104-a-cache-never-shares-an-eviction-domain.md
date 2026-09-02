# D-104 — A cache never shares an eviction domain with durable state

**Status:** accepted
**Invariant:** A physical resource identity carries caching or durable state,
never both. A resource the composition root declares as holding durable work or
durable security refuses every cache activated on it, and no waiver excuses
that. Two durable tenants may share one resource only behind
`SharedDurableSecurity(reason)`, and the reason is a sentence, not a flag. A
root that cannot afford an undeclared resource says so once, and activation then
refuses a cache whose resource nobody described.

## The decision

Three subsystems reach for the same Redis: the cache, the job queue, and the
session revocation list. They are three unrelated failure models sharing one
`maxmemory` and one eviction policy, and the shared eviction policy is what
turns a capacity event into a security failure.

`allkeys-lru` evicts whatever is coldest, and it does not know which key
somebody thought of as disposable. Evicting a cache entry is the cache working
as designed — the value is recreatable and the loader runs again. Evicting a
revocation entry is not: `Revoked` reads the absence of a key as "not revoked",
so a signed-out session silently becomes valid again, at the exact moment the
system is under load. Evicting a delivery record loses work that a caller was
told had been accepted. The cache is the tenant that asked for eviction; the
other two are the tenants that pay for it.

So the rule is about identity, not technology: three separate resource
identities, one per kind of state. Sharing one Redis *process* between them is
the same defect as sharing one keyspace — a namespace prefix separates names,
not memory.

The framework cannot discover this on its own. A `cache.Backend` stores opaque
envelopes and describes topology, item size and expiry support; it does not
describe an endpoint, and it must not start describing one, because that would
make the core know about drivers it deliberately does not import. The
composition root is the only place that knows which client points where, so the
identity is declared there: `Provider.Resource` already names it for physical
namespace collisions, and `ActivationSpec.Resources` says what else lives on
that name. `Activate` refuses the graph, which is the earliest moment the
answer exists and is the same moment every other cache misconfiguration is
refused.

An undeclared resource is the one hole this leaves, and it is deliberate: the
first consumer to adopt the declaration has one resource described and the rest
not, and refusing that would make the rule impossible to adopt incrementally. So
the strictness is the root's to ask for. `RequireDeclaredResources` turns
silence into a start-up refusal that names the cache and the resource, and it is
off by default because a default that refused at boot would be adopted by
deleting the check rather than by writing the declarations.

The waiver is deliberately narrow. Durable work and durable security sharing one
resource is an operational risk and a visible one: a full Redis refuses writes
and both subsystems report an error nobody can mistake for success. A cache
sharing with either is invisible by construction, so `SharedDurableSecurity`
cannot excuse it and is refused when it excuses nothing.

## What it forbids

- Do not activate a cache on a resource declared with `DurableWorkTenant` or
  `DurableSecurityTenant`, with or without a waiver.
- Do not read an undeclared resource as proven separate. An undeclared resource
  is unchecked; the declaration is what the check is made of, and
  `RequireDeclaredResources` is how a root refuses to run without one.
- Do not widen `SharedDurableSecurity` to cover a cache, and do not accept it
  with a blank reason or on a resource that shares nothing.
- Do not give `cache` an import of `jobs` or of a revocation list so it can
  discover them. The root declares; the subsystem checks what it was told.
- Do not separate the three with a key prefix and call it done. A prefix
  separates names; eviction is about memory.

## Where it lives

- `cache/resource.go` — the tenants, the waiver, the declaration, the
  eviction-domain check and the undeclared-resource refusal.
- `cache/activation.go` — where the declared domains meet the resource identity
  of every provider a cache actually resolved to.
- `cache/provider.go` — `Provider.Resource` and `resourceIdentity`, the name
  both this check and the physical-namespace check are written against.

## Proven by

- `cache/resource_test.go` — a cache is refused the resource that holds revoked
  sessions or queued work, and a waiver does not change that; the same cache
  activates on a resource that holds only caching; durable work and durable
  security share one resource only with a written reason, and a waiver that
  excuses nothing is itself a refusal; a declaration with no identity, no
  tenant, an unknown tenant or a duplicate resource is refused before any cache
  is published; a cache on a resource nobody declared activates while nothing
  asked for declarations and is refused once `RequireDeclaredResources` is set,
  and the case that is not required is the control saying where the refusal
  comes from.

## See also

[[D-093]] [[D-096]] [[FL-025]] [[UC-024]]
