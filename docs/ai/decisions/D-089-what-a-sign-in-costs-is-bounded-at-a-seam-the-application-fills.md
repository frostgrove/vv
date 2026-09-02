# D-089 — What a sign-in costs is bounded at a seam the application fills

**Status:** accepted
**Invariant:** the number of sign-in attempts and the number of concurrent
password hashes are both bounded, the bound for an unknown identifier is the
bound for a known one, the cost a *stored* hash may ask for is bounded by the
same reasoning, and none of it drags a backing store into `access`.

## The decision

`access` owns three dependency-neutral seams and one in-process implementation of
each, and the deployment's real implementation is registered in the composition
root.

| Seam | What it is | What ships with it |
|---|---|---|
| `AttemptLimiter` | `Admit` before an attempt, `Record` after it | `MemoryLimiter`, per identifier and per IP |
| `AttemptObserver` | told every `succeeded` / `failed` / `refused` | nothing — telemetry is the consumer's |
| `Hasher` (already existed) | `Hash` / `Verify` | `BulkheadHasher`, a decorator with permits and a bounded queue |

Field bounds are configuration rather than a seam: `PasswordConfig.MaxLength` and
`LoginConfig.MaxIdentifierLength`, checked before anything is hashed or read.

The stored hash is bounded by the parser rather than by configuration. A PHC
string is read by an exact grammar — the six `$`-separated fields, `argon2id`,
`v=19`, then `m`, `t` and `p` in that order and nothing after them — and every
number has a floor and a ceiling: memory 8 KiB..1 GiB, 1..16 rounds, at least
one thread, a salt of 8..64 bytes and a digest of 16..64 bytes. Anything else is
`ErrSecretFormat`, the same unreadable-column fault a garbled string already
produced.

`Runtime` wraps the *default* hasher in `Bulkhead` and takes the limiter and the
observer from `RuntimeSpec.Protection`. `accessfx` supplies them as optional fx
dependencies, so an application that provides an `access.AttemptLimiter` — over
Redis, say — has it wired by declaring it and nothing else.

## Why

**Because `access` may not grow a Redis dependency to count attempts.** A
counter shared across replicas needs a store; a store is a third-party module
([[D-033]]). The base package therefore owns the interface and the composition
root owns the choice, which is the same shape `RevocationSink` already uses for
deny-lists ([[D-072]]).

**Because Argon2id is a denial-of-service primitive pointed at yourself.** A
hash configured at 64 MiB is 64 MiB per concurrent verification; a few hundred
simultaneous sign-ins is a machine that stops answering anything at all. The
bulkhead admits what the box can run and refuses the rest as `overloaded`
(retryable), because a queue with no ceiling converts a burst into an outage that
outlives it.

**Because an unbounded field is hashed before it is doubted.** `Verify` costs the
same for a ten-byte password and a ten-megabyte one only in the Argon2 core; the
copies before it are not free, and nothing needed the input in the first place.
The bounds are checked before the credential lookup and before the hash.

**Because a refusal must not become an oracle.** An over-long identifier and an
over-long password are both refused as `bad_credentials`, the same answer a wrong
password gets, and no bounded input can match a stored identifier anyway — the
enrolment bound is the same one. `DummyHash` keeps the ordinary miss
indistinguishable ([[FL-023]]); this keeps the refused-by-bound case from
becoming the distinguishable one.

**Because the stored hash is an input like any other, and `argon2.IDKey` is not
total.** The parameters used to verify come from the column, so the column
decides how much memory and CPU one sign-in costs — and two of its arguments are
not merely expensive but fatal: zero rounds panics, and a zero-length digest
makes `blake2b.New(0, nil)` hand back a nil hash that the derivation then
dereferences. A row an operator truncated, a bad restore, or anything that can
write that column therefore takes the process down rather than failing one
sign-in. Reading the digest's own length as the key length is the same class of
mistake in the other direction: the stored value decides how much of a match is
compared.

**Because a limiter nobody can see is a limiter nobody trusts.** The observer is
a separate interface from the limiter so that a deployment can watch attempts
without owning the counting, and so that the in-process limiter is not the only
thing that can report.

## What it forbids

- Do not import a store, a clock package or a metrics library into `access` for
  any of this. The seam is the extension point; a package named for a backend
  belongs to the backend.
- Do not make the limiter's refusal a different shape per transport. It is an
  `errs` fault with code `too_many_attempts`, kind retryable, exactly like every
  other refusal the use case can produce. **It answers 503, not 429**, because the
  error contract has no "too many requests" kind — a known gap, recorded against
  `port`, and not one to close by having `access` write a status behind the
  contract's back ([[D-049]]).
- Do not let `Record` failing fail a sign-in. A limiter that cannot write is
  logged; a caller with the right password is not refused because a counter was
  unreachable.
- Do not wrap a hasher the consumer supplied. `RuntimeSpec.Hasher` is theirs,
  including its concurrency; only the default hasher `Runtime` builds itself is
  wrapped.
- Do not raise the password ceiling to unbounded "because Argon2 does not care".
  The bytes are copied, and the ceiling is what makes that bounded.
- Do not read a stored hash with `fmt.Sscanf` or any grammar that accepts what it
  did not ask for, and do not pass a length taken from the column to
  `argon2.IDKey` without a floor. An unreadable hash is `ErrSecretFormat`; it is
  never a panic and never a `false` that looks like a wrong password.

## Where it lives

| File | What it holds |
|---|---|
| `auth/access/access.protection.go` | `Attempt`, `AttemptOutcome`, `AttemptLimiter`, `AttemptObserver`, `Protection`, `MemoryLimiter`, `BulkheadHasher`, the two refusal codes |
| `auth/access/access.secret.go` | `parseStoredHash`, the PHC grammar and every parameter bound `Verify` runs on |
| `auth/access/access.config.go` | `PasswordConfig.MaxLength`, `LoginConfig.MaxIdentifierLength` and their defaults |
| `auth/access/usecase.login.go` | `admit` before the attempt, the bounds, `recordAttempt` after |
| `auth/access/usecase.enroll.go` | `checkIdentifier` and the upper half of `checkPassword` |
| `auth/access/access.runtime.go` | `RuntimeSpec.Protection`, and the default hasher inside a bulkhead |
| `auth/access/accessfx/accessfx.go` | the optional fx dependencies an application fills |

## Proven by

`auth/access/access.protection_test.go` —
`TestSignInsStopReachingTheDatabaseOnceTheAttemptCeilingIsReached`,
`TestSigningInSuccessfullyGivesTheIdentifierItsBudgetBack`,
`TestAnOverLongIdentifierOrPasswordCostsNoHashAndNoStatement` (with the ordinary
miss as its control), `TestAPasswordPastTheCeilingIsRefusedAsAFieldViolation`,
`TestAnIdentifierPastTheCeilingIsRefusedAsAFieldViolation`,
`TestTheBulkheadRefusesWhatItHasNoRoomToQueue` and
`TestTheBulkheadPassesEveryCallThroughWhileItHasRoom`, which covers the explicit
constructor and the magic one together.

`auth/access/access.secret_test.go` —
`TestAStoredHashIsRefusedUnlessEveryParameterIsInBounds`, with the readable hash
as its control, and `FuzzAStoredHashIsEitherRefusedOrWithinItsBounds`, whose
seed corpus runs as an ordinary test and includes the empty digest that used to
panic.

## See also

[[D-033]] [[D-062]] [[D-072]] [[FL-023]] [[UC-023]]
