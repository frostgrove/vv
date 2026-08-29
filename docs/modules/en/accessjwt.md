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
| `AccessTTL` | `5m` | token lifetime, and the revocation delay without a list |
| `RefreshTTL` | the session TTL | bounds the whole lineage |
| `RefreshGrace` | `10s` | how long a just-replaced credential is still a concurrent refresh |
| `Revocation` | none | see [revokeredis](revokeredis.md) |

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

Roles and permissions are **not** in it. They come from the rows on every
request, so a demotion takes effect on the next call.

`sty` is checked and not taken. A token minted for one kind of caller, presented
to another subject's guard, verifies its signature perfectly — the session is
genuine, nothing looks wrong, and the caller is simply somebody other than the
route assumed.

## Rotation

A refresh credential is spent by the call that uses it. The session row holds
the digest of the current one and of the one before it, and a rotation is a
compare-and-swap on the current digest. Losing that swap is **two** situations,
and telling them apart is the whole design:

```go
func Classify(presented Presented, now time.Time, grace time.Duration) Outcome
```

| Outcome | When | What happens |
|---|---|---|
| `Rotate` | the digest is the current one | swap it, answer normally |
| `RotateAgain` | it is the previous one, within `RefreshGrace` | two tabs refreshed at once — rotate again, answer normally |
| `Replay` | it is the previous one, after the grace | a spent credential came back: **close the lineage** |
| `Unusable` | anything else, or the session is closed or expired | one refusal |

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

Separate from access's own migration because a deployment holding opaque
sessions never rotates and has no use for either. The session row *is* the
family: one sign-in, one lineage. Nothing holds a second id for it, because a
family that could outlive its session would be a second thing to close on
sign-out and a second thing to forget.
