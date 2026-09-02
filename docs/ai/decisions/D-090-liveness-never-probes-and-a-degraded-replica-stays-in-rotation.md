# D-090 — Liveness never probes a dependency, and a degraded replica stays in rotation

**Status:** accepted
**Invariant:** The liveness projection runs no check and reaches no dependency;
it answers from the fact that the process served the request. Readiness returns
503 only when a *required* dependency is failing. A degraded replica answers
200 and says `degraded` in its body.

## The decision

A health page has two readers with opposite needs, and the failure of most
health implementations is answering both with one number.

The orchestrator reading liveness is asking "should I kill this process?". The
only honest input is whether the process can still serve a request, and it just
proved it did. If liveness pings PostgreSQL, then a database that is slow for
forty seconds restarts every replica of every service that depends on it —
simultaneously, while the database is already struggling, and with cold caches
and a reconnect storm as the recovery plan. The one condition where restarting
helps least is the condition that check makes restart-triggering. So
`Registry.Live` runs nothing.

The load balancer reading readiness is asking "should I send the next request
here?". That answer does depend on the dependencies, but only on the ones
without which this replica cannot serve *anything*. Degradation is different: an
export that has lost its SMTP relay still serves reads, and a search index that
is rebuilding still answers everything not searched for. Removing a degraded
replica removes it from every path, including the ones that work — and because
replicas share their dependencies, the degradation is almost never one replica's.
"Take the degraded one out" is the move that turns a partial outage into a total
one, at the moment there is nothing left to fail over to.

`Importance` is therefore the routing decision, not the state. `Required` and
nothing else closes the door.

## What this rules out

- A liveness endpoint with any dependency behind it, including an "it's only a
  cheap ping" one. Cheap is not the property that matters; blast radius is.
- A single endpoint serving both readers, with the orchestrator and the load
  balancer distinguished by a query parameter. They are different questions and
  a shared handler drifts to whichever reader complains first.
- Mapping `degraded` to 503 "to be safe". It is not the safe direction.

## What it costs

A process that is alive but permanently unable to work — a stuck goroutine that
still serves HTTP — is not restarted by liveness. That case is the supervisor's
([[D-092]]): a runner that fails takes the process down deliberately, rather
than being inferred from a probe that cannot tell a wedged process from a slow
one.

## Where it lives

[[FL-027]] — the registry's three projections and their transport bindings.

## Proven by

`health/registry_test.go` — *liveness asks no dependency anything*.
`app/http/appfiber/health_test.go` — *a degraded replica stays in rotation*,
*a replica whose required dependency is down asks to be taken out of rotation*,
*liveness answers while the dependencies are down*.
