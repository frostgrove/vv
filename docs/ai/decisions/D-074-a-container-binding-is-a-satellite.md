# D-074 — A container binding is a satellite, and the library still holds no container

**Status:** accepted — narrows [[D-037]]
**Invariant:** the library resolves no component by type. A dependency-injection
container may be *bound to* from a satellite module whose only job is that
binding; the container is the consumer's, the graph is the consumer's, and no
package under `github.com/frostgrove/vv` that a consumer gets without asking
learns how to find a component.

## The decision

[[D-037]] refused a container with a mechanism rather than an intention: no
`map[reflect.Type]any`, no `Get[T]()`, no `...any` that is type-switched, and no
`DependsOn` on a component list. That rule stands and is not weakened.

What it did not answer is whether the library may ship an *adapter* to a
container somebody else wrote. It may, as a satellite:

```
app/appfx                              uber/fx: the seeder group, the runner
app/http/appfiber                      uber/fx + Fiber: routes, middleware, the boot gate, listen
auth/access/accessfx                   uber/fx: the access context's graph
crud/adapter/crudsql/crudsqlfx         uber/fx: the pool and the source
storage/storageminio/storageminiofx    uber/fx: the object store
```

Each requires exactly one container and whatever it wires. A consumer who
assembles their program by hand imports none of them and resolves `go.uber.org/fx`
in no `go.sum`.

## Why

**Because D-037's mechanism is untouched by this.** The prohibition is on *this
library* holding the lookup. In `appfx`, fx holds it: fx keeps the graph, fx
resolves by type, fx reports what it could not build. `app.Runner` takes a
`[]Seeder` and `appfiber.Mount` takes a slice of routes — both are ordinary
values passed at a call site, and both work identically for a consumer who built
the slice by hand.

**Because the wiring is the part every application was getting wrong.** The
measurement that produced these modules was one application's `src/core/`: a
route group, an ordered middleware list, a seed runner, a pool with a ping, a
catalog read, a bucket check. Roughly four hundred lines of code with no
application logic in it, every line of which the next consumer would write again
— and some of which they would write without the part that fails at start-up.
That is the shape [[D-021]] exists to remove from a consumer.

**Because the alternative is not "no container" but "a worse one, in every
application".** D-037's slide has three steps and each looks reasonable; an
application that walks it ends up with a hand-rolled registry keyed by type. A
satellite pointing at a mature container is the honest end of that road, and it
is opt-in.

**Because [[D-033]] already draws this boundary and [[D-051]] already sizes it.**
A package that needs a third-party dependency becomes a module. `appfiber`
requires fx *and* Fiber, and that is one decision and not two: a consumer of that
package is assembling Fiber routes out of an fx graph and cannot want one half
without the other.

**Because fx is one container and not the only one.** The naming leaves room:
`appfx` is `app` bound to fx, exactly as `appfiber` is `app` bound to Fiber. A
second container is a second satellite beside it, not a rewrite of the first.

## What it forbids

- Do not move any of this into the root module, or into any package a consumer
  gets without importing a satellite. `go get github.com/frostgrove/vv` must not
  resolve a container.
- Do not add to `app`, `port`, `crud` or `auth` a type that exists for a
  container's benefit — an interface fx happens to want, a struct tag it reads.
  The satellite adapts; the library does not bend.
- Do not let a satellite hold state a consumer could resolve out of it. A value
  group is fx's; `Registered` and `Mounted` are parameter objects, not registries.
- Do not read this as permission for [[D-037]]'s three steps. An `app.New().Add(…)`
  that grew `DependsOn` would still be the thing D-037 refuses, satellite or not.
- Do not require one fx satellite from another to save a consumer an import.
  `crudsqlfx` and `storageminiofx` name no transport, and `accessfx` names no
  route; a bundle module would put all of them in one consumer's graph because
  they wanted one.

## Where it lives

| File | What it holds |
|---|---|
| `app/doc.go` | what the composition root may hold, and what it may not become |
| `app/appfx/appfx.go` | the seeder group, and `Seeding` |
| `app/http/appfiber/appfiber.go` | the route and middleware groups, `Mount`, `Serving`, `Listen` |
| `auth/access/accessfx/accessfx.go` | the subject and grants groups, and the context's graph |
| `crud/adapter/crudsql/crudsqlfx/crudsqlfx.go` | the pool, its ping and its close |
| `storage/storageminio/storageminiofx/storageminiofx.go` | the client, the backend and the bucket check |

## Proven by

`make check-deps` — the root module still resolves no third-party package, and
each satellite reports its own count.

`TestSeedersFromEveryContributorReachTheRunner` and
`TestAMisregisteredSeederFailsTheCommand` in `app/appfx`: the group reaches the
runner, and the runner's own refusals survive the trip through fx.

`app/http/appfiber/appfiber_test.go`: the same behaviour the hand-wired path has —
ordering, mounting, and both halves of the boot gate — asserted through fx.

`TestEveryComponentTheContextOffersIsResolvable` in `accessfx`, and its control,
plus the equivalent pair in `crudsqlfx` and `storageminiofx`: the graph is
complete, and a graph that resolved nothing would fail the control.

## See also

[[D-037]] [[D-033]] [[D-051]] [[D-021]] [[D-058]] [[D-073]]
