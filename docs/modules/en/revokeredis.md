# revokeredis — an accessjwt revocation list in Redis

```go
import "github.com/frostgrove/vv/auth/access/accessjwt/revokeredis"
```

```bash
go get github.com/frostgrove/vv/auth/access/accessjwt/revokeredis
```

**Module:** separate · **Depends on:** [accessjwt](accessjwt.md),
`redis/go-redis/v9`, `google/uuid`

Makes a signed token stop working as soon as its session is closed, rather than
when it expires.

---

## When you need it

Only when `accessjwt.Spec.AccessTTL` is too long a window between revoking a
session and the revocation biting. At five minutes it usually is not. Adding
this costs a lookup on **every** authenticated request, so it has to be faster
than the session read a signed token was chosen to avoid.

That window is what a sign-out lands in, not only a replay. `access` announces
every closed session to the strategy ([[D-072]]), so with a list configured
`POST /auth/logout`, `logout-all`, closing one session from the list and either
password path all take effect on the next request; without one they take effect
when the access token expires. Which of those a product can live with is the
whole of the decision.

**A deny-list in the database is not worth having.** It is that same session
read back again with worse semantics: an allow-list that cannot be read refuses
everybody, which is loud and safe, while a deny-list that cannot be read would
admit everybody unless something stops it. That is why `Revoked` returns an
error rather than a `false`.

## Wiring

```go
list, err := revokeredis.New(client)          // the client is yours to open
if err != nil {
	return err
}
if _, err := list.VerifyEvictionPolicy(ctx); err != nil {   // at the start, not here
	return err
}

strategy := accessjwt.Strategy(accessjwt.Spec{
	// … signing, issuer, TTLs …
	Revocation: list,
})
```

The client is handed in rather than dialled here, for the same reason the
database connection is: who opens a connection, with which pool and which
timeouts, is the application's decision.

| | |
|---|---|
| `New(client, options...)` | builds the list, and reaches nothing |
| `Prefix(string)` | namespaces the keys; default `access:revoked:` |
| `Logger(*slog.Logger)` | where the start-up warning goes; default `slog.Default()` |
| `OnUnknownPolicy(Reported\|Refused)` | what to do with a server that will not say what it evicts; default `Reported` |
| `VerifyEvictionPolicy(ctx)` | the start-up check — see below |
| `Ping(ctx)` | reachability on its own, wrapping `ErrUnreachable` |

`New` opens no connection and asks no question, so the check belongs to whoever
owns the lifecycle. Under fx that is [the binding below](#the-fx-form), which
appends it as a start hook: a constructor runs inside `fx.New`, where
`fx.StartTimeout` does not reach and where a refusal unwinds nothing that has
already been built.

## The check

Redis under `maxmemory` with an evicting policy deletes cold keys to make room,
and a revocation between the sign-out and the next request is about as cold as a
key gets. `Revoked` cannot tell that deletion from the expiry the entry was
written with, so it answers "not revoked" and the signed-out token works again.
Nothing in a test suite sees this: development Redis runs `maxmemory 0` with
`noeviction`, where the bug does not exist.

So the list asks. `VerifyEvictionPolicy` sends `CONFIG GET maxmemory-policy` and
returns what it learned along with the refusal, if there is one.

| Verdict | When | What happens |
|---|---|---|
| `Retaining` | the server answered `noeviction` | the check passes |
| `Evicting` | the server named any other policy | the check fails with `ErrEvicting`, naming the policy |
| `Unknown` | the server would not answer | a `WARN`, or `ErrUnknownPolicy` under `OnUnknownPolicy(Refused)` |

**Every `volatile-*` policy is refused too.** `volatile-lru` only evicts keys
that carry a TTL, which sounds like an exemption and is the opposite of one:
every key this list writes carries a TTL, so the whole list is eligible.

**`Unknown` is a verdict, not a pass.** On Elasticache, Upstash and most other
managed Redis, `CONFIG` is disabled or hidden behind an ACL, and the answer comes
back as a permissions error rather than as a policy. Refusing to start there
would make this package unusable on the platforms most deployments run on;
treating it as success would make the most common case the silent one, which is
the whole defect. So it is neither: the verdict says `Unknown`, it carries the
reason the server gave, and by default a `WARN` names the parameter, the
consequence and the two ways out. A deployment that has confirmed `noeviction`
out of band — or that would rather not start until it has — writes
`OnUnknownPolicy(Refused)`, and the same event is then returned rather than
logged.

**A server that is not there is refused whatever that option says.** `CONFIG`
fails identically for an ACL and for nothing listening, and the second is not a
policy nobody would state: it is a list that will fail every read it is asked
for, and a deny-list that cannot be read refuses every authenticated request. The
`Unknown` branch therefore pings before it settles, and an absent server fails
with `ErrUnreachable`. Only a server that answered gets the benefit of
`Reported` — which also means the eviction check is the reachability check, and a
separate `Ping` at boot buys nothing.

See [[D-112]].

## The fx form

```go
import "github.com/frostgrove/vv/auth/access/accessjwt/revokeredis/revokeredisfx"
```

```bash
go get github.com/frostgrove/vv/auth/access/accessjwt/revokeredis/revokeredisfx
```

**Module:** separate · **Depends on:** revokeredis, `go.uber.org/fx`

A module of its own so that building the list by hand — or with a different
container — takes no dependency on fx ([[D-033]], [[D-074]]).

```go
fx.New(
	fx.Provide(func() redis.UniversalClient { … }),   // still yours to open
	revokeredisfx.Auto(),
)
```

| | |
|---|---|
| `Auto()` | the list from the graph's `redis.UniversalClient`, published as `*revokeredis.List` and as `accessjwt.RevocationList`, checked at start |
| `Revoking(options...)` | the same, with `revokeredis.Option`s of your own |
| `Verifying()` | the start hook alone, over a `*revokeredis.List` the root built itself |

A `*slog.Logger` in the graph becomes the list's logger. It is optional, and a
`revokeredis.Logger(...)` written into `Revoking` outranks it — the graph
supplies a default, the caller decides.

Nothing reaches Redis while the graph is being built. The check runs on the start
hook, so `fx.StartTimeout` bounds it and a refusal unwinds the pools and clients
the start had already opened.

## Where it must live

The check asks what the server does when it fills up. Who else is on that server
is the other half of the same question, and no `CONFIG` answers it: a cache
sharing the instance is what makes it fill up in the first place, and `Prefix`
does not help — it separates names, not memory.

So there are three resource identities, one per kind of state — caching, durable
work, and this list — and the composition root is where they are declared. Name
the resource this client points at with `cache.DurableSecurityTenant`
([cache](cache.md)); activation then refuses any cache resolved onto it, and no
waiver excuses that. Sharing one resource with a job queue is the single overlap
a written `cache.SharedDurableSecurity(reason)` excuses. An undeclared resource
is unchecked, not proven separate. See [[D-104]].

## What it stores

One key per revoked **session id**, not per token id: the session id survives
rotation, so revoking has to close the credentials that have not been issued yet
as well as the one in flight.

Each entry expires when the last token naming that session would have expired
anyway, so the list stays the size of the tokens actually in flight rather than
of every session ever closed. An expiry already in the past is written with a
one-second floor rather than skipped — the caller's clock and Redis's may
disagree, and holding a useless key for a second costs nothing beside dropping a
live one.
