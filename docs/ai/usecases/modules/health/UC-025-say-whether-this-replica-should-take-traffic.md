# UC-025 — Say whether this replica should take traffic

**Actor:** the application author, on behalf of an orchestrator, a load balancer
and an on-call engineer
**Covered by:** [[FL-027]]

## Scenario

The author's service depends on a database, an object store, a queue and a
converter. The platform will restart the process if it says it is not alive, and
will stop routing to it if it says it is not ready. Somebody paged at 3am needs
to know which dependency is the reason, and that person is not the only one who
can reach the endpoint.

Each deployment weighs the dependencies differently: the API cannot serve without
the database, the export worker can serve without the mail relay, and one
deployment does not run the converter at all. The author does not want a health
package per dependency, a boolean on every client, or a readiness endpoint that
becomes load on the very dependency it is asking about.

## What must hold

1. A dependency is registered once, as a probe plus the decisions the program
   makes about it: how important it is, how long it may take, and whether a
   public caller may learn its code. The probe is one method, so anything that
   already answers `func(ctx) error` can be adapted where it is registered.
2. Whether a failure removes the replica from rotation is the composition root's
   decision, not the checker's, and one of its values disables the check
   entirely. A disabled check is never executed.
3. Liveness answers without reaching any dependency. A slow dependency never
   causes a restart.
4. Readiness refuses only for a dependency declared required. A replica that has
   lost a degrading dependency keeps taking traffic and reports that it is
   degraded.
5. The unauthenticated answer carries a status and only those stable codes a
   contribution opted into publishing. It carries no host, no check name it was
   not given a code for, and no error text.
6. A second, authenticated answer carries every check's name, importance, state,
   bounded message and duration. Reaching it without an account, or with an
   account that lacks the declared permission, refuses without disclosing any of
   it.
7. Concurrent scrapes share one evaluation, and an evaluation younger than the
   declared freshness window is reused. One scraper abandoning its request does
   not fail the evaluation the others are waiting for.
8. Every check is bounded: a probe that never answers fails on its own budget
   without holding the endpoint, a probe that panics fails only its own check,
   and an operator message is truncated rather than unbounded.
9. Registration problems are reported together and refuse the program, rather
   than producing a page that quietly checks less than it claims: an unnamed
   check, two checks of one name, two checks publishing one code, an unknown
   importance, a check with no probe.
10. Nothing in the framework has to import the health package for a dependency to
    be checkable, and the health package imports no subsystem.

## Out of scope

- Deciding *why* a dependency is down, or repairing it. A probe reports; it does
  not reconnect, retry or fail over.
- Historical health. There is one answer, about now, from this replica.
- A cluster view. Each replica answers for itself; aggregating is the platform's.
- Restarting a process that is alive but wedged. That is the supervisor's
  ([[UC-026]]), not something a probe can distinguish from a slow dependency.
- A transport for every framework binding. The registry's projections are plain
  values; a Fiber route is provided, and any other transport is a handler over
  the same two calls.
