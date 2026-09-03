# D-112 — A revocation list refuses a server that evicts it

**Status:** accepted
**Invariant:** A `revokeredis.List` is checked against its own server's
`maxmemory-policy` before it is used. `noeviction` is the only answer that
passes; every evicting policy fails the check with `revokeredis.ErrEvicting`,
and a server that would not answer produces a third verdict — `Unknown` — which
is warned about by default and refused when the deployment writes
`OnUnknownPolicy(Refused)`. The check is a method the lifecycle owner calls, not
work `New` does, and `revokeredisfx` appends it as a start hook.

## The decision

`Revoked` reads the absence of a key as "this session was not revoked", because
the entry is written with the expiry of the last token that could name the
session and disappearing on time is the whole design. Redis under `maxmemory`
with an evicting policy also makes keys disappear — early, silently, and by
preference for the cold ones, which is exactly what a revocation is between the
sign-out and the next presentation of the token. The two deletions are
indistinguishable at the read, so a capacity event on such a server hands a
signed-out token back to its holder.

[[D-104]] already forbids a cache to share the resource that holds this list,
and [[D-111]] made that rule run in a deployment. Neither reaches the case where
the list has the server to itself and that server is configured to evict — a
declaration says who lives where, and says nothing about what the server does
when it fills up. That is the server's own configuration, so it is asked for.

**`noeviction` is the only acceptable answer, including the `volatile-*`
family.** `volatile-lru` sounds safe because it only evicts keys that carry a
TTL, and every key this list writes carries one. That makes the whole list
eligible rather than exempt.

**The check is a method, and the fx form is a hook over it.** `revokeredis` is
not an fx package and does not become one; the question is asked by
`(*List).VerifyEvictionPolicy`, which whoever owns the lifecycle calls. A
constructor could not be the place: constructors run inside `fx.New`, where
`fx.StartTimeout` does not reach and where a refusal rolls back nothing, because
the stop hooks of everything already built belong to a start that never
happened. `revokeredisfx.Verifying` appends the one lifecycle hook, and
`revokeredisfx.Revoking` is the magic form that also builds the list from the
graph's Redis client — magic that removes wiring and nothing else, since
everything it does is reachable by hand ([[D-021]]).

**An unknown policy is a third verdict, not a quiet pass.** Elasticache, Upstash
and most other managed Redis offerings forbid `CONFIG` outright or hide it behind
an ACL, so the answer to the question is an error about permissions rather than a
policy. Reading that as a refusal would make the framework unusable on the
platforms most of its consumers run on; reading it as success would make the
common case the silent one, which is the defect this whole decision is about. So
it is neither: `EvictionPolicy.Verdict` is `Unknown`, it carries the reason the
server gave, the default emits a `WARN` naming the parameter, the consequence and
the remedy, and a deployment that has confirmed its policy out of band — or wants
to be stopped until it has — writes `OnUnknownPolicy(Refused)`.

The default is `Reported` rather than `Refused` for the same reason [[D-111]]
inverts its own strictness at the binding: a default that refused would be
adopted by deleting the check. A warning that names the parameter is what an
operator can act on; a start failure on every managed Redis is what an operator
routes around.

**"Unknown" means the server would not say, never that there is no server.**
`CONFIG` fails identically when an ACL forbids it and when nothing is listening,
and the second of those is not a policy nobody would state — it is a list that
will fail every read it is later asked for, which the guard turns into a refusal
of every authenticated request. So the `Unknown` branch pings before it settles,
and a list with no server behind it fails the start with
`revokeredis.ErrUnreachable` whatever `OnUnknownPolicy` says. A reachable server
that answered nothing is the only thing `Reported` waves through, and the
eviction check is therefore also the reachability check `Ping` was written for.

## What it forbids

- Do not treat a `CONFIG` error as a passing check. `Unknown` is a verdict of its
  own and must stay distinguishable from `Retaining` at the type.
- Do not make `Refused` the default. An unknown policy on managed Redis is the
  common case, and a framework that refuses to start there is removed rather than
  configured.
- Do not accept `volatile-lru`, `volatile-lfu`, `volatile-random` or
  `volatile-ttl` on the grounds that they only touch keys with a TTL. Every key
  this list writes has one.
- Do not move the check into `New` or into any other constructor. It reaches the
  network, so it belongs to the start, where a timeout bounds it and a refusal
  unwinds what the start had already brought up.
- Do not add `go.uber.org/fx` to `revokeredis`. The binding is
  `revokeredisfx`, a module of its own ([[D-033]], [[D-074]]).
- Do not compare these failures by message. `ErrEvicting`, `ErrUnknownPolicy`
  and `ErrUnreachable` are the sentinels.

## Where it lives

- `auth/access/accessjwt/revokeredis/eviction.go` — `EvictionPolicy` the type and
  the method, `Verdict`, `VerifyEvictionPolicy`, `UnknownPolicy` with
  `OnUnknownPolicy`, and the warning `report` writes.
- `auth/access/accessjwt/revokeredis/revokeredis.go` — `Ping` and
  `ErrUnreachable`, which the unknown branch leans on.
- `auth/access/accessjwt/revokeredis/revokeredisfx/revokeredisfx.go` — `Auto`,
  `Revoking` and the low-level `Verifying`, and `verifyOnStart`, the one
  lifecycle hook.

## Proven by

- `auth/access/accessjwt/revokeredis/eviction_test.go` —
  `TestAnEvictedRevocationReadsAsASessionNobodyRevoked` is the control the rest
  of the file rests on: it deletes the key behind the list's back and asserts the
  list then answers "not revoked", so if that ever stops being true the check has
  no reason to exist and this test says so first.
  `TestOnlyNoevictionIsAcceptedAndEveryEvictingPolicyIsRefused` walks the seven real
  policies plus one Redis has not shipped, and its `noeviction` subtest is the
  positive control.
  `TestAServerThatWillNotAnswerIsUnknownAndSaidOutLoud` drives both shapes of
  refusal a managed Redis produces — a `NOPERM` and a disabled command — and
  asserts a `WARN` was written rather than nothing.
  `TestADeploymentCanTurnAnUnknownEvictionPolicyIntoARefusal` pins the opt-in and
  that the same event is not both logged and returned.
  `TestAServerThatIsNotThereIsRefusedRatherThanCalledUnknown` is the reachability
  half: an absent server fails with `ErrUnreachable` and is not warned about as if
  it had merely declined to answer.
  `TestNothingIsAskedOfTheServerUntilTheCheckIsRun` is what pins the check to the
  start rather than to construction — it counts `CONFIG` calls and requires zero
  before `VerifyEvictionPolicy` is called.
- `auth/access/accessjwt/revokeredis/revokeredisfx/revokeredisfx_test.go` —
  `TestAnEvictingServerFailsTheStartAndNotTheGraph` builds the graph, asserts the
  server was asked nothing while it was being built, and only then starts and
  gets `ErrEvicting`; moving the check into the constructor fails it at the
  counter rather than at the error.
  `TestARetainingServerStartsAndTheListItLeavesBehindWorks` is its control — the
  same wiring against `noeviction` starts and publishes a list that revokes and
  reads back.
  `TestAServerThatWillNotSayIsWarnedAboutThroughTheGraphsLogger` and
  `TestADeploymentCanTurnAnUnknownPolicyIntoARefusedStart` are the managed-Redis
  pair at the binding: the same `NOPERM` starts with a `WARN` carrying what the
  server said, and refuses the start once the deployment asks it to.
  `TestTheOptionsAConsumerWritesOutrankTheOnesTheGraphSupplies` pins the
  precedence — the graph's `*slog.Logger` is a default, and the one written into
  `Revoking` wins.
  `TestTheLowLevelFormChecksAListTheRootBuiltItself` runs `Verifying` over a list
  the root constructed with its own prefix, which is the low half of [[D-021]].

## See also

[[D-021]] [[D-033]] [[D-062]] [[D-072]] [[D-074]] [[D-104]] [[D-111]] [[FL-023]]
[[UC-023]]
