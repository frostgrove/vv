# D-091 — Health importance and public codes belong to the composition root

**Status:** accepted
**Invariant:** A checker supplies a probe and nothing else. Whether its failure
takes the replica out of rotation, and whether an unauthenticated caller learns
that it exists, are set where the contribution is registered. `health` imports
no subsystem, and no subsystem imports `health`.

## The decision

The same Redis ping is a required dependency in the session service and a
degrading one in the report exporter. The same PostgreSQL handle is required for
the API and irrelevant to the worker that only drains a queue. Importance is not
a property of the thing being checked; it is a property of the program that
depends on it. So `Contribution.Importance` is set by the composition root, and
`Disabled` is one of its values — a deployment that does not run the converter
says so in one place instead of building a registry conditionally.

The direction of the imports follows from the same reasoning, and it is the part
that is easy to get wrong. The tempting shape is a `jobspghealth` or
`storageminiohealth` package that "just wires it up". That is a package whose
identity is the intersection of two independently selectable choices, and the
extension-architecture roadmap forbids exactly that family. What `health` owns is
a dependency-neutral seam — `Probe` is one method — and the two-line adapter from
`Driver.CheckSchema` to a `Contribution` is written by the application that chose
both halves:

```go
healthfx.AsCheck(func(driver *jobspg.Driver) health.Contribution {
    return health.Contribution{
        Name:       "jobs.schema",
        Code:       "jobs",
        Importance: health.Required,
        Probe:      health.ProbeFunc(driver.CheckSchema),
    }
})
```

Ten checked dependencies are ten of those, not ten packages.

## Two projections, and why the public one is deliberately poor

`Report` carries a status and the stable codes of the checks that produced it.
`Detail` carries names, messages, importances and durations, and it is behind a
permission. The split exists because a readiness endpoint is reachable by
anything that can open a socket to the pod, and a health page is the cheapest
reconnaissance there is: it names your database host, your object store, your
converter vendor and which of them is down right now.

`Code` is therefore opt-in. A contribution with no `Code` still moves the status
— the replica goes out of rotation — and names nothing. A contribution that
publishes `"database"` has had a person decide that the word `database` in a
public body is worth the operator being able to grep for it.

## What this rules out

- A checker that decides its own importance, or a `Critical bool` on the probe.
- A package per checked subsystem.
- A public projection that carries the probe's error text, the check's name, or
  anything else derived from the deployment rather than declared for it.

## Where it lives

[[FL-027]].

## Proven by

`health/registry_test.go` — *the status moves without naming a check that
publishes no code*, *a required dependency that fails takes the replica out of
rotation*, *a degrading dependency keeps the replica serving and says so*, *a
disabled contribution is never asked*, *an operator message is bounded*.
`app/http/appfiber/health_test.go` — *the public readiness page names no host and
no driver error*, *the operator page refuses a caller with no account*, *the
operator page refuses an account without the permission*, *the operator page
names the dependency and the driver error*.
