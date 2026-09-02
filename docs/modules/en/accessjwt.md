# accessjwt — signed access tokens over a rotating refresh credential

```go
import "github.com/frostgrove/vv/auth/access/accessjwt"
```

```bash
go get github.com/frostgrove/vv/auth/access/accessjwt
```

**Module:** separate · **Depends on:** [access](access.md), [authjwt](authjwt.md),
`golang-jwt/jwt/v5`, `google/uuid`

An [`access.Strategy`](access.md#strategies): a short-lived signed token that a
verifier can check without the database, over a refresh credential that rotates
on every use.

---

## Read this before choosing it

**It does not save database work in the common case.** Authenticating still
reads the directory to see whether the subject is active, and reads the grants
to resolve roles and permissions. Both come from rows on every request, and both
are deliberate ([[D-066]]) — a demotion has to bite on the next call, not when a
token happens to expire. What a signed token removes is the *session* read,
which is one of three.

**The reason to choose it is a verifier that cannot reach the database.**
Another service, another perimeter, an edge holding only a public key. Inside
one process with one database, `access.OpaqueToken()` is simpler and this is
complexity bought for nothing.

**Revocation is delayed by `AccessTTL` unless you add a list.** A signed token
keeps working until it expires. At five minutes that is what most products can
live with; if it is not, see [revokeredis](revokeredis.md).

That applies to a sign-out as much as to a replay, and it is the one thing to be
sure of before choosing this strategy. `access` announces every session it
closes through `Issued.Revocations` ([[D-072]]), which this module fills in only
when `Spec.Revocation` is set — so with no list, `POST /auth/logout` writes the
row, answers 200, and the token in the caller's hands stays good for the rest of
its `AccessTTL`.

## Wiring

```go
strategy := accessjwt.Strategy(accessjwt.Spec{
	Method: jwt.SigningMethodHS256,
	Key:    secret,
	Verify: authjwt.HMAC(secret),
	Issuer: "example.com",

	Audience: "api.example.com",

	AccessTTL:    5 * time.Minute,
	RefreshGrace: 10 * time.Second,
})

users, signUp, err := access.Mount(runtime, access.SubjectSpec[SignUpForm]{
	Type:      "user",
	Directory: userDirectory,
	Strategy:  strategy,
})
```

| Field | Default | What it is |
|---|---|---|
| `Method`, `Key` | required | how an access token is signed |
| `Verify` | required | how one is checked; the public half for an asymmetric method |
| `Issuer` | required | goes in `iss`, and is required on the way back in |
| `Audience` | the issuer | goes in `aud`, and is required on the way back in ([[D-097]]) |
| `UnsafeAnyAudience` | `false` | mint no `aud` and accept any; the only way to a token no service claims, and refused together with `Audience` |
| `AccessTTL` | `5m` | token lifetime, and the revocation delay without a list |
| `RefreshTTL` | the session TTL | bounds the whole lineage |
| `RefreshGrace` | `10s` | how long a just-replaced credential is still a concurrent refresh |
| `Revocation` | none | see [revokeredis](revokeredis.md) |

**The matrix is checked at start-up, not at request time** ([[D-088]]). `Build`
refuses `AccessTTL > RefreshTTL`, `RefreshTTL > session.ttl`,
`AccessTTL > session.idle_ttl` and `RefreshGrace >= RefreshTTL`. Each of those is
a configuration under which every token minted is already wrong, so it is a
process that does not start rather than a request that is refused.

**A lifetime left at zero takes the default in the table above; a lifetime below
zero is refused by name and by value.** Both are checked at `Build`, and the
refusal comes before any default is put in place — a `-5m` replaced by five
minutes would pass every check below it and be reported nowhere ([[D-088]]).

The idle deadline is not a field here. It is `session.idle_ttl` from
`access.Config` — the same value the opaque strategy applies — precisely so the
two cannot come to disagree about when a session is over.

The response carries both halves:

```json
{ "token": "…", "expiresAt": "…", "refresh": "…", "refreshExpiresAt": "…", "principal": {…} }
```

and the binding mounts `POST /auth/refresh` only for a subject whose strategy
rotates.

## What is in the token

```go
type Claims struct {
	Subject     string `json:"sub"`
	SubjectType string `json:"sty"`
	SessionID   string `json:"sid"`
	Issuer      string `json:"iss"`
	IssuedAt    int64  `json:"iat"`
	ExpiresAt   int64  `json:"exp"`
}
```

`aud` is minted and verified but is not a field here: it is a verification input
rather than something a caller reads, and a `string` field would fail to decode
the equally legal JSON-array form. Two services sharing one signing key are told
apart by it — without an audience, every session of the first is a session of the
second ([[D-097]]).

Roles and permissions are **not** in it. They come from the rows on every
request, so a demotion takes effect on the next call.

`exp` is `min(now + AccessTTL, session.expires_at)`. The session's end is
absolute and no rotation moves it, so refreshing a second before it buys a
second — not another `AccessTTL` that nothing can take back ([[D-088]]).

`sty` is checked and not taken. A token minted for one kind of caller, presented
to another subject's guard, verifies its signature perfectly — the session is
genuine, nothing looks wrong, and the caller is simply somebody other than the
route assumed.

## Rotation

A refresh credential is spent by the call that uses it. The session row holds
the digest of the current one and of the one before it, and a rotation is a
compare-and-swap on the current digest. The replacement — the new credential, the
grants and the signed access token — exists **before** that swap runs, so a
rotation that cannot be answered has spent nothing ([[D-098]]).

Losing that swap is **two** situations, and telling them apart is the whole
design:

```go
func Classify(presented Presented, now time.Time, window Window) Outcome

type Window struct {
	Grace time.Duration // RefreshGrace
	Idle  time.Duration // session.idle_ttl; zero applies no idle deadline
}
```

| Outcome | When | What happens |
|---|---|---|
| `Rotate` | the digest is the current one | swap it, answer normally |
| `RotateAgain` | it is the previous one, within `Window.Grace` | two tabs refreshed at once — rotate again, answer normally |
| `Replay` | it is the previous one, after the grace | a spent credential came back: **close the lineage** |
| `Unusable` | anything else, or the session is closed, expired, or untouched for longer than `Window.Idle` | one refusal |

`Presented.LastUsedAt` is the session row's `last_used_at`, which is what the
idle arm reads.

`Classify` also decides the swap that changed no row. Two refreshes that both
read the row before either wrote it do not reach the table above on the first
pass — both see `Rotate`, and one of them loses. The loser re-reads the row by
id, classifies the credential it was given against what is there now, and
rotates again from the winner's digest; the winner's own credential is the
lineage's previous digest and stays usable for the grace window. Refusing it
instead would sign out a client for retrying a request the network dropped
([[D-098]]).

Refusing a concurrent refresh signs people out for having two windows open,
which is the failure people actually hit. Treating a replay as concurrent leaves
a stolen credential working. `Classify` is pure and takes no database, which is
the only reason both are pinned by tests rather than by argument.

On a replay the whole session is closed — the holder of the *newer* credential
loses it too. That is the point: one of the two parties is not the account's
owner, and there is no way to tell which from here. The row records
`ReasonRefreshReplayed`, which is the one revocation reason worth alerting on.

Every failed rotation gets the same refusal, whatever it was.

## The schema

`auth/access/accessjwt/migrations/00001_accessjwt.sql` adds two columns to the
`sessions` table access already owns:

| Column | Why |
|---|---|
| `previous_token_hash` | the digest replaced by the last rotation |
| `rotated_at` | when, so the grace window means something |

`last_used_at` and `expires_at` are access's own columns, and rotation reads
both: the first is the idle deadline, the second is the end `exp` is clamped to.

Separate from access's own migration because a deployment holding opaque
sessions never rotates and has no use for either. The session row *is* the
family: one sign-in, one lineage. Nothing holds a second id for it, because a
family that could outlive its session would be a second thing to close on
sign-out and a second thing to forget.
