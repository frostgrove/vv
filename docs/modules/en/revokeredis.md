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
if err := list.Ping(ctx); err != nil {        // worth doing at boot
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
| `New(client, options...)` | builds the list |
| `Prefix(string)` | namespaces the keys; default `access:revoked:` |
| `Ping(ctx)` | a start-up check — a configured-but-unreachable list refuses every request |

## Where it must live

The list is durable state, and the resource it lives on decides whether it
works. Redis under `maxmemory` evicts the coldest key it can find, and `Revoked`
reads an absent key as "not revoked" — a capacity event on an instance shared
with a cache quietly hands a signed-out session back. `Prefix` does not help:
it separates names, not memory.

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
