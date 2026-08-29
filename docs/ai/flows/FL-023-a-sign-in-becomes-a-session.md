# FL-023 — A sign-in becomes a session

**Entry point:** `auth/access/access.runtime.go:New` and `auth/access/access.runtime.go:Mount`
**Implements:** [[UC-023]] · **Governed by:** [[D-066]] [[D-067]] [[D-068]] [[D-070]] [[D-072]] [[D-033]] [[D-058]]

## Wiring, once

1. **`New`** — `auth/access/access.runtime.go:48` — builds the shared half: the
   `Store` over the consumer's `crud.Source`, the hasher (argon2id unless a test
   hands in a cheap one), and this module's own `ModuleGrants`. It refuses a nil
   source and a nil logger; the library never writes to a process-wide one
   ([[D-062]]).
2. **`Mount`** — `auth/access/access.runtime.go:161` — one call per kind of
   caller. It refuses a duplicate subject type, a duplicate prefix and a
   directory answering for another type, all three before anything is built.
3. **`NewDirectories`** — `auth/access/access.directory.go:20` — re-indexed on
   every mount, because the resolver needs every directory and the last one is
   not registered until now.
4. **`Strategy.Build`** — `auth/access/access.strategy.go:33` — one value
   produces the issuer and the verifier together ([[D-068]]).
   `opaqueStrategy.Build` narrows a `SessionAuthenticator` to this subject with
   `For`; `accessjwt.strategy.Build` builds a parser scoped by issuer.
5. **`newEndpoints`** — `auth/access/access.endpoints.go:38` — the seven
   operations every subject has. The sign-up is not among them: it is returned
   from `Mount` separately, carrying the consumer's payload type ([[D-066]]).

## Signing in

1. **the binding** — `auth/access/http/accessfiber/accessfiber.go:SignIn`,
   `accessgin`, `accessnet` — decode, call, write, and nothing else.
2. **`Endpoints.SignIn`** — `auth/access/access.endpoints.go` — supplies the
   subject type and folds the identifier through `Subject.Identifier`. Both come
   from the surface and never from the body: with an identifier unique per type
   ([[D-067]]), a body-supplied type would sign a caller in to another domain.
3. **`LoginUseCase.Execute`** — `auth/access/usecase.login.go` —
   the credential lookup carries the subject type in its predicate. The password
   is verified even when nothing was found, against `DummyHash`, so an unknown
   identifier costs what a known one does.
4. **`Directory.Active`** — checked *after* the password: checking first tells
   somebody with a wrong password that the address is real and disabled.
5. **`SessionIssuer.Issue`** — `auth/access/access.strategy.go:opaqueIssuer` or
   `auth/access/accessjwt/accessjwt.go:Issue` — the only place a token of any
   kind comes into existence.
6. **`Directory.Touch`** — best effort, after the session exists. A directory
   that cannot record a sign-in has not stopped one.

## Registering

1. **`RegisterHandler`** — one per binding, generic over the payload and nothing
   else is.
2. **`SignUpUseCase.Execute`** — `auth/access/usecase.signup.go` —
   opens the transaction, calls `Registrar.Create`, then `EnrollUseCase` inside
   it. `crud.InTx` joins rather than nests, so the account row and the credential
   commit together.
3. **`Deps.DefaultRole`** — `auth/access/access.defaults.go` — the first
   statement, before anything is created: the role this subject type's sign-ups
   grant, read from `subject_default_roles` with the role preloaded ([[D-070]]).
   No row grants nothing. It is inside the transaction, so a seed command
   changing the binding mid-registration cannot grant a role that is no longer
   the default by the time the credential commits. The row travels on to
   `EnrollUseCase.grantRole`, which therefore does not look the slug up again —
   unless the two disagree, which is not a state any caller should be trusted in.
4. **the session, after the commit** — opening it inside would let a failure
   with nothing to do with signing in roll it back.

## Arranging what a sign-up grants

Not a request path — this is what a consumer's seed command runs, once, out of
band ([[D-070]]).

1. **`Runtime.Seeder`** — `auth/access/access.runtime.go` — the idempotent write
   half, over the same `Store`.
2. **`Seeder.EnsureRole`** — `auth/access/access.defaults.go` — creates the role
   if the slug is free and attaches the permissions it does not hold. An
   existing role is not overwritten with the spec, and a permission no module
   declared is refused rather than attached.
3. **`Seeder.SetDefaultRole`** — resolves the slug against `roles`, locks the
   binding with `FOR UPDATE`, and writes nothing when it already points there.
   The unique index on `subject_type` is what covers the absent-row race the
   lock cannot.
4. **`Runtime.Sync`** — `auth/access/access.seed.go` — the *other* pass, and not
   this one: it folds in what the code declares and runs at every start.
5. **`Runtime.SetPassword`** — `auth/access/access.runtime.go` — what a seed or
   an administration screen calls to make a provisioned account able to sign in.
   A method rather than a field: it needs the resolver, which does not exist
   until the last `Mount`.

## Verifying a request

1. **`MountedSubject.Guard`** — `auth/access/access.runtime.go:142` — per
   subject, mounted on that subject's group. `Runtime.AdminGuard` is a chain over
   the declared strategies, for routes under no prefix.
2. **`SessionAuthenticator.Authenticate`** — `auth/access/access.authenticator.go`
   — digest lookup, `Session.Live`, the subject-type check that `For` installed,
   `Directory.Active`, then `GrantsService.For`. Roles and permissions come from
   rows, never from the credential.
3. **`touch`** — at most once per `TouchInterval`, and its failure is logged and
   swallowed: a request that authenticated must not fail on a bookkeeping write.

## Rotating

1. **`Endpoints.Refresh`** — mounted only for a strategy that rotates.
2. **`core.find`** — `auth/access/accessjwt/accessjwt.go` — the digest as the
   current credential, then as the previous one.
3. **`Classify`** — `auth/access/accessjwt/rotation.go:71` — pure, no database.
   `Rotate`, `RotateAgain` inside the grace window, `Replay` after it, `Unusable`
   otherwise.
4. **`core.rotate`** — a compare-and-swap on the current digest. Two simultaneous
   refreshes: exactly one matches, the other lands in the previous-digest branch
   and is told apart there. A read-then-write would issue two lineages from one
   session.
5. **`core.close`** — on a replay the whole lineage goes, and the revocation list
   is written if one is configured.

## Closing a session

Five paths, one statement, and one announcement ([[D-072]]).

1. **the callers** — `LogoutUseCase.Execute`, `LogoutAllUseCase.Execute`,
   `LogoutAllUseCase.RevokeOne`, `ChangePasswordUseCase.Execute` and
   `SetPasswordUseCase.Execute`. Each supplies its own predicate and nothing
   else: "closed" is a shape — a timestamp and a reason — and two spellings of it
   drift the first time a column arrives.
2. **`Deps.revoke`** — `auth/access/usecase.logout-all.go` — reads the matching
   rows (`id`, `subject_type`) and then writes them by id. The read is what a
   sink is built from: an `UPDATE … WHERE` answers how many rows changed and
   never which. The write keeps `revoked_at IS NULL`, so a row somebody else
   closed in between drops out of the count.
3. **`Deps.announce`** — `auth/access/access.revocation.go` — groups the closed
   ids by subject type and hands each group to that subject's
   `RevocationSink`. Nothing happens when no strategy declared one, which is
   every opaque deployment.
4. **after the commit, never inside it** — the two password use cases revoke
   within `Store.Tx`; they collect there and announce once it returns. A
   rollback after a sink was told leaves a deny-list refusing a live session,
   and nothing takes an entry back out.
5. **`core.SessionsRevoked`** — `auth/access/accessjwt/accessjwt.go` — writes
   each id to the deny-list, holding it until `now + AccessTTL`. A failure here
   is logged with the ids and does not fail the sign-out: the rows are committed
   and the caller is already out.

The registry itself is filled at **`Mount`** — `auth/access/access.runtime.go` —
from `Issued.Revocations`, and is shared by pointer with every `Deps` the runtime
builds, including the one behind `Runtime.SetPassword`.

## Files

| File | What it decides |
|---|---|
| `auth/access/access.runtime.go` | the factory, the mount refusals, the guards |
| `auth/access/access.strategy.go` | the strategy seam, `RevocationSink`, and the opaque implementation |
| `auth/access/access.revocation.go` | the sink registry, and the announcement every closing path makes |
| `auth/access/access.subject.go` | `Subject`, `Registrar[P]` |
| `auth/access/access.defaults.go` | the default role: the read a sign-up makes, and the `Seeder` writes that arrange it |
| `auth/access/access.seed.go` | `Sync` — the start-up pass over what the code declared |
| `auth/access/access.endpoints.go` | the seven transport-neutral operations |
| `auth/access/access.authenticator.go` | a session row becomes a principal |
| `auth/access/usecase.*.go` | the use cases |
| `auth/access/http/accesshttp/` | the route table and the endpoint names |
| `auth/access/http/access{net,gin,fiber}/` | decode, call, write |
| `auth/access/accessjwt/rotation.go` | `Classify`, the pure half of rotation |
| `auth/access/accessjwt/accessjwt.go` | issuing, the CAS, the replay response |
| `auth/access/accessjwt/revokeredis/` | the deny-list |

## Tests that walk this flow

- `auth/access/access_runtime_test.go` — the mount refusals and their control,
  and that an enrolment refuses before it writes anything.
- `auth/access/access.defaults_test.go` — the default role is whatever the table
  says, an absent binding grants nothing, a sign-up reads it before it creates,
  and the seed writes are idempotent with their controls.
- `auth/access/accessjwt/rotation_test.go` — every arm of `Classify`, including
  the grace boundary on both sides.
- `auth/access/http/access{net,gin,fiber}/*_test.go` — the triplet: the prefix,
  the separately-mounted sign-up, and that every named endpoint reaches a
  handler.
