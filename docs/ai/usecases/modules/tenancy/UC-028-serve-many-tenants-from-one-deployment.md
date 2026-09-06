# UC-028 — Serve many tenants from one deployment without a tenant argument

**Actor:** the application author, on behalf of every tenant of the service
**Covered by:** [[FL-033]] [[FL-007]] [[FL-008]] [[FL-005]] [[FL-006]]

## Scenario
One deployment serves many customer organisations. Rows, stored objects, cached
values and background work all belong to one of them, and the failure that cannot
be afforded is the request that forgot which. [[UC-004]] already covers the row
half when the application supplies a tenant value from its own context; what this
adds is the part nobody can write correctly by hand twice: the value the
application supplies is *verified*, it carries a lifecycle state and a generation,
and it reaches the object store, the cache and the job queue with the same force
it reaches the `WHERE` clause.

The author wants business code that never mentions a tenant, and a composition
root where enabling this is one decision rather than one per subsystem.

## What must hold

1. There is no ambient tenant. No default, no last-used value, no environment
   variable, no process-global. Work that arrives with no verified tenant refuses
   and executes nothing — including immediately after a successful operation for
   another tenant in the same process.
2. The value the data plane accepts cannot be written by the code it constrains.
   The application supplies a trust contract that returns plain data — a
   reference, a lifecycle state, a generation — and the framework is what turns
   that into the accepted value. A zero value, a value copied out of another
   deployment and a value from a second authority are all refused.
3. Which lifecycle states may work is a whitelist the deployment writes, and it
   is decided per class of operation. A state nobody enumerated refuses. A
   deployment that wants a suspended tenant to stay readable says so, and its
   writes still refuse.
4. Ownership on create is derived or validated, never taken from unvalidated
   caller input, and no ordinary mutation moves a row between tenants. This holds
   for a resource that carries its owner and for one owned across a declared
   relation — where what is frozen is the key that points at the owner, because
   repointing it moves the row without any of its own columns changing.
5. A resource whose ownership is neither of those two shapes is expressible. The
   strategy is an interface, not a column name.
6. A generation is part of durable identity. After a tenant is restored to a new
   generation, its object namespace and its cache partition are different, so the
   new generation reads none of the previous one's data.
7. Nothing recorded durably is sufficient to act for a tenant. A worker reads the
   reference the producer wrote and then asks the control plane what is true now:
   a tenant suspended, deleted or moved to a new generation after the work was
   enqueued does not reach its handler, and a record whose reference disagrees
   with the partition it travelled with reaches nothing at all.
8. An object namespace and a cache partition are injective and non-containing.
   Two tenants with equal logical keys stay apart, and no tenant's namespace is a
   prefix of another's however the references were named.
9. Under one database per tenant, a verified tenant selects exactly one
   datasource before any statement, and there is no default and no last-used
   fallback. A missing mapping, a full budget and a database whose schema
   generation disagrees are three distinguishable refusals, and none of them is a
   different tenant's database.
10. The per-tenant resource budget is bounded and the bound is required, not
    defaulted. Reclaiming a binding never closes it under a caller still holding
    it.
11. A unit of work's tenant is fixed when it starts. Binding a different tenant
    inside work that is already bound refuses rather than switching.
12. Work across tenants needs an explicit bounded grant — a purpose, a named
    cohort and an expiry — never an absent tenant and never a wildcard. Each
    member is entered on its own with its lifecycle re-checked, and a member that
    became non-active is reported and skipped rather than failing the run, so a
    partially applied run is resumable rather than repeated.
13. A refusal names a kind and nothing else. No tenant reference, database name,
    key or wrapped provider text reaches it, and the kinds a signal may carry are
    a closed set whose size does not grow with the number of tenants.
14. Refusals are distinguishable by what the caller should do: an authorisation
    refusal, a capacity refusal and an availability refusal are three different
    answers, and a full pool does not read as a security event.
15. A tenant still sees everything it owns. Isolation that narrowed to nothing
    would satisfy every guarantee above.
16. Removing the extension leaves the rest of the application compiling. Nothing
    in the base subsystems knows it exists.

## Out of scope

- **Authentication and membership.** The host proves who is calling before any of
  this runs. This never sees a token, a header or a router.
- **The control plane.** Onboarding, billing, plan management and the tenant
  registry's own storage are what the injected trust contract stands in front of.
- **Provider SDKs.** Drivers, secret managers and migration tools are the
  application's dependency decision.
- **Operator procedures.** Suspend, migrate, restore, delete and legal hold are
  procedures; what is in scope is the fence they rely on — the generation, the
  admission whitelist and the namespace change.
- **Cross-tenant atomicity.** Two tenants in two databases cannot be one
  transaction. Per-tenant atomicity plus a resumable run replaces it, and
  anything stronger is refused rather than approximated.
- **Database-enforced row-level security.** Optional defence in depth, not a
  replacement for the narrowing, and not covered here.
- **Cleaning up on deletion.** The fence is verified; the cleanup is a per-
  subsystem operator procedure.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-033]] | a request becoming a verified scope, and a durable record becoming one again |
| [[FL-007]] | the narrowing entering a read |
| [[FL-008]] | the narrowing entering a write, and the hidden-key question |
| [[FL-005]] | the narrowing travelling into an owner reached across a relation |
| [[FL-006]] | the narrowing travelling into a preload's second statement |

## Status
**covered for shared row; the database-per-tenant profile is not advertised.**

Guarantees 1–8 and 11–16 have tests, including a live-PostgreSQL isolation test
and a durable round trip that proves the record is not an authority. Guarantee 15
has its own control, because a predicate that narrowed to nothing would pass every
other assertion.

Guarantees 9 and 10 are implemented and unit-proved against recording sources.
They are **not advertised**: two real databases, outage behaviour and the
suspend/migration/restore rehearsal have not been run. Until they are, the
supported topology is shared row.

Two known oracles are documented rather than closed, both of the same class as the
unique-constraint result [[UC-004]] already records: a create against a globally
unique key, and a pagination cursor minted under another tenant's scope. An
application requiring non-enumerability avoids exposing those shapes.
