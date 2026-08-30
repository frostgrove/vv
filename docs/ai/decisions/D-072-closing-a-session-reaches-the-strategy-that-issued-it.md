# D-072 — Closing a session reaches the strategy that issued it

**Status:** accepted
**Invariant:** Every path that closes a session goes through `Deps.revoke`, and
`Deps.revoke` reads the rows before it writes them so the strategy that issued
them can be named. A strategy that cannot see a closed row from its own verifier
declares a `RevocationSink` on `Issued`, and `Mount` registers it against that
subject. The announcement never happens inside the transaction that wrote the
rows.

## The decision

Closing a session is two facts. The row is one, and for a strategy whose
verifier reads that row it is the whole of it: the next request reads what the
sign-out wrote, and "closed" is already true.

For a strategy whose verifier reads no row it is half. `accessjwt` checks a
signature against a key; a `revoked_at` it never looks at changes nothing, and
the credential stays good until it expires. The second fact — *these sessions are
closed* — has to be handed to it.

So `access.Issued` carries an optional `Revocations RevocationSink`, `Mount`
registers it under the subject being mounted, and the five closing paths
announce through it:

| Path | Use case |
|---|---|
| `POST /auth/logout` | `LogoutUseCase.Execute` |
| `POST /auth/logout-all` | `LogoutAllUseCase.Execute` |
| `DELETE /auth/sessions/{id}` | `LogoutAllUseCase.RevokeOne` |
| `POST /auth/password` | `ChangePasswordUseCase.Execute` |
| an administrator's reset | `SetPasswordUseCase.Execute` |

All five already funnelled through one helper, which is why this is one seam and
not five. `Deps.revoke` now reads the matching rows, writes them by id, and
answers both the count a caller is told and the ids a sink has to be told.

The sink is keyed by subject type, and `SessionsRevoked` takes no deadline: how
long the fact must be remembered is a property of the credential, which the
strategy knows and `access` does not.

## Why

**Because the failure is silent and looks like success.** The row is written, the
count comes back right, the endpoint answers 200 — and the token keeps working.
There is nothing to see from outside except a session closed everywhere except
where the next request will look. Two integration suites in a consuming
application caught this the first time a deployment moved from `OpaqueToken` to
`accessjwt`; nothing in this repository would have.

**Because the strategy is the only thing that knows.** `accessjwt` already wrote
to its deny-list from one place — the replay branch of a rotation — so the list
existed and held exactly the sessions a *stolen* credential had closed, and none
of the ones a person had. A consumer wiring the missing half in its own
transport layer is the same rule written once per binding, and the binding that
forgets it is the one that had a signed token outlive a sign-out.

**Because a read before the write is what a sink can be built from.** An
`UPDATE … WHERE` answers how many rows changed and never which. Two statements
on a path taken once a day buys the ids; the alternative was five call sites
each rebuilding the list from their own predicate, and the one that got it wrong
would fail the same silent way.

**Because a rollback must not leave a deny-list ahead of the database.** The two
password use cases revoke inside a transaction. Announcing there and then
rolling back leaves an entry refusing a session that is still live, and nothing
takes an entry back out. So they collect inside and announce after the commit.

**Because an unreachable sink must not fail a completed sign-out.** By the time
the announcement runs, the rows are committed and the caller is signed out.
Answering "could not sign you out" to somebody who is signed out is worse than
the window a failed announcement leaves, and that window is bounded by the
credential's own lifetime rather than open-ended. It is logged at error level
with the ids. This is the opposite of the *incoming* rule — `revokeredis.Revoked`
returns an error rather than a `false`, because a deny-list that cannot be read
must not admit everybody — and the two are not in tension: refusing to answer a
question is safe, refusing to finish a completed action is not.

## What it forbids

- Do not close a session by writing `revoked_at` from anywhere but
  `Deps.revoke`. A second spelling is a path no sink hears about.
- Do not collapse the read and the write back into one statement. The read is
  not a leftover; it is where the ids come from.
- Do not call `Deps.announce` inside a transaction.
- Do not turn a sink failure into a failed sign-out, and do not swallow it
  silently either — it is logged with the ids.
- Do not assign a strategy's `core` to `Issued.Revocations` unconditionally. A
  typed nil in that interface is not absence, and `Mount` rejects it rather
  than publishing a broken callback.
- Do not announce to every registered sink. A session belongs to one subject
  type, and a key in another deployment's deny-list is one nothing ever reads.

## Where it lives

| File | What it holds |
|---|---|
| `auth/access/access.strategy.go` | `RevocationSink`, `Issued.Revocations` |
| `auth/access/access.revocation.go` | `revocationSinks`, `revoked`, `Deps.announce` |
| `auth/access/access.runtime.go` | `Mount` registers the sink; `Runtime.SetPassword` reaches the same registry |
| `auth/access/usecase.logout-all.go` | `Deps.revoke` — the read, the write by id, the count |
| `auth/access/usecase.logout.go`, `usecase.change-password.go`, `usecase.set-password.go` | the announcing call sites |
| `auth/access/accessjwt/accessjwt.go` | `core.SessionsRevoked` — the deadline from `AccessTTL` |

## Proven by

- `access.TestSigningOutTellsTheStrategyWhichSessionClosed` — the seam at its
  smallest, with `TestAStrategyThatDeclaredNoSinkIsNeverAsked` as its control:
  an opaque deployment pays nothing and cannot fail on a sink it never had.
- `access.TestOnlyTheOwningSubjectsStrategyIsTold` — two subjects, two sinks.
- `access.TestASinkIsNotToldWhenTheTransactionRollsBack` — the ordering rule.
- `access.TestAFailingSinkDoesNotFailTheSignOut`.
- `access.TestClosingASessionReadsTheRowsBeforeItWritesThem` — what keeps
  somebody from collapsing the two statements.
- `access.TestMountRegistersTheStrategysRevocationSink`, with
  `TestMountRegistersNothingForAStrategyWithoutASink` as its control: the seam
  is worth nothing if `Mount` does not connect it.
