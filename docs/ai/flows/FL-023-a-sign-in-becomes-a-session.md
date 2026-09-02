# FL-023 — A sign-in becomes a session

**Entry point:** `auth/access/access.runtime.go:New` and `auth/access/access.runtime.go:Mount`
**Implements:** [[UC-023]] · **Governed by:** [[D-066]] [[D-067]] [[D-068]] [[D-070]] [[D-072]] [[D-075]] [[D-033]] [[D-058]] [[D-088]] [[D-089]] [[D-097]] [[D-098]] [[D-099]]

## Wiring, once

1. **`New`** — `auth/access/access.runtime.go` — builds the shared half: the
   `Store` over the consumer's `crud.Source`, the hasher (argon2id behind a
   `Bulkhead`, unless a test or a consumer hands one in, in which case its
   concurrency is theirs — [[D-089]]), and this module's own `ModuleGrants`. It
   refuses a nil source and a nil logger; the library never writes to a
   process-wide one ([[D-062]]). `RuntimeSpec.Protection` is where the attempt
   limiter and the attempt observer arrive; `accessfx` takes both as optional fx
   dependencies, so an application registers a Redis-backed limiter by providing
   one.
2. **`Mount`** — `auth/access/access.runtime.go:Mount` — one call per kind of
   caller. It refuses a duplicate subject type, a duplicate prefix and a
   directory answering for another type, all three before anything is built.
3. **`NewDirectories`** — `auth/access/access.directory.go:NewDirectories` — re-indexed on
   every mount, because the resolver needs every directory and the last one is
   not registered until now.
4. **`Strategy.Build`** — `auth/access/access.strategy.go` — one value
   produces the issuer and the verifier together ([[D-068]]).
   `opaqueStrategy.Build` narrows a `SessionAuthenticator` to this subject with
   `For`; `accessjwt.strategy.Build` builds a parser scoped by issuer *and*
   audience — `Spec.Audience`, or the issuer when the spec names none, or no
   audience at all under `Spec.UnsafeAnyAudience` ([[D-097]]) — and runs
   `refuseLifetimesBelowZero` and then `checkLifetimes`: the first refuses a
   duration written below zero, before any default is put in its place, and the
   second refuses a lifetime matrix in which one bound outlives another — both
   before anything is mounted ([[D-088]]).
5. **`newEndpoints`** — `auth/access/access.endpoints.go:newEndpoints` — the seven
   operations every subject has. The sign-up is not among them: it is returned
   from `Mount` separately, carrying the consumer's payload type ([[D-066]]).

## Signing in

1. **the binding** — `auth/access/http/accessfiber/accessfiber.go:SignIn`,
   `accessgin`, `accessnet` — decode, read the delivery, call, write. The
   delivery is read *before* the password is checked, so a request nobody can
   answer costs no hash and mints no session.
2. **`Endpoints.SignIn`** — `auth/access/access.endpoints.go` — supplies the
   subject type and folds the identifier through `Subject.Identifier`. Both come
   from the surface and never from the body: with an identifier unique per type
   ([[D-067]]), a body-supplied type would sign a caller in to another domain.
3. **`Hasher.Verify`** — `auth/access/access.secret.go` — reads the stored PHC
   string with an exact grammar and refuses anything outside the bounds in
   [[D-089]]: the cost parameters, the salt length and the digest length all come
   from a column, and two of the values it can hold end the process rather than
   the sign-in.
4. **`LoginUseCase.Execute`** — `auth/access/usecase.login.go` — three things
   happen before the database is touched ([[D-089]]):
   `Deps.admit` asks the `AttemptLimiter` whether this identifier and this IP may
   try at all, `withinBounds` refuses an identifier or a password past its
   configured ceiling, and both refusals are the same `bad_credentials` an
   ordinary miss gets — an over-long input cannot match a stored identifier,
   because enrolment applies the same ceiling. Then the credential lookup, which
   carries the subject type in its predicate. The password is verified even when
   nothing was found, against `DummyHash`, so an unknown identifier costs what a
   known one does. `Deps.recordAttempt` tells the limiter and the observer how it
   went, after the transaction rather than inside it; a limiter that cannot write
   is logged and does not refuse a caller who had the right password.
5. **`Directory.Active`** — checked *after* the password: checking first tells
   somebody with a wrong password that the address is real and disabled.
6. **`SessionIssuer.Issue`** — `auth/access/access.strategy.go:opaqueIssuer` or
   `auth/access/accessjwt/accessjwt.go:Issue` — the only place a token of any
   kind comes into existence.
7. **`Directory.Touch`** — best effort, after the session exists. A directory
   that cannot record a sign-in has not stopped one.
8. **`Credentials.Answer`** — `auth/access/http/accesshttp/cookies.go` — splits
   the minted session between the body and the cookies, and clears the cookie a
   half delivered to the body did not go into.

## Delivering the credentials

Where a session's two credentials go, and the one place it is not the caller's
to say ([[D-075]]).

1. **`Credentials.Requested`** — `auth/access/http/accesshttp/delivery.go` —
   reads `X-Auth-Delivery` through the binding's own header getter. Silence takes
   the most closed delivery the deployment offers; a value nobody defined is
   `invalid_enum`, and so is a cookie asked of a surface with none configured.
2. **`NewCredentials`** — `auth/access/http/accesshttp/cookies.go` — built once
   per handler from the binding's `Delivering` option. It derives both cookie
   names from the subject's prefix and both paths from the API's, and refuses at
   start-up a policy a browser would silently discard.
3. **`Credentials.Answer`** — what goes into a cookie leaves the body, and what
   goes into the body clears its cookie.
4. **`accesshttp.Rotating`** — the rotating half is forced to the channel the
   presented credential arrived on; the request still decides the access token.
5. **`Cookie.HTTP`** — the net/http rendering `accessnet` and `accessgin` write
   through, with HttpOnly set here and nowhere else. `accessfiber` translates to
   Fiber's own cookie type in `accessfiber.go:jar.write`.
6. **`Credentials.Clear` / `ClearRefresh`** — a sign-out takes both cookies away;
   a refused rotation takes only the one that failed to rotate anything, because
   a rotation is anonymous and the caller may hold a good access cookie from a
   session it knows nothing about.
7. **`Credentials.Protect`** — `auth/access/http/accesshttp/crosssite.go` —
   called at the top of every unsafe handler in the three bindings, through each
   binding's own `jar.protect`. A write that carries one of this deployment's
   cookies, presents no `Authorization` header, and can say neither
   `Sec-Fetch-Site: same-origin`/`none` nor an `Origin` the deployment named is a
   403 `cross_site_request`. A read, a request with no cookie of ours and a
   header-borne credential are not checked at all — the check is about ambient
   authority and nothing else ([[D-102]]). `CrossSite.Unsafely` is the waiver,
   and it takes a sentence.
8. **the guard** — `auth/http/authhttp/cookie.go:Cookie` — the other half of the
   closed delivery: a browser holding its access token in a cookie sends no
   Authorization header, so the guard is given a lookup that reads the cookie and
   falls back to the header. One request uses one of the two; presenting both, or
   two cookies of that name, is refused ([[D-099]]). See [[FL-019]].

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
4. **`EnrollUseCase.execute`** — `auth/access/usecase.enroll.go` — bounds the
   identifier and the password before hashing ([[D-089]]), then locks the
   subject's password credentials inside the transaction and refuses if it finds
   one: an account has a single password, and a second row would keep signing in
   under its own identifier after a reset rewrote the other ([[D-067]]).
   `uq_credentials_password_subject` is the same rule in the schema, for the
   concurrent enrolment the lock cannot see.
5. **the session, after the commit** — opening it inside would let a failure
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

1. **`MountedSubject.Guard`** — `auth/access/access.runtime.go:MountedSubject.Guard` — per
   subject, mounted on that subject's group. `Runtime.AdminGuard` is a chain over
   the declared strategies, for routes under no prefix.
2. **`SessionAuthenticator.Authenticate`** — `auth/access/access.authenticator.go`
   — digest lookup, `Session.Live`, the subject-type check that `For` installed,
   `Directory.Active`, then `GrantsService.For`. Roles and permissions come from
   rows, never from the credential.
3. **`touch`** — at most once per `TouchInterval`, and its failure is logged and
   swallowed: a request that authenticated must not fail on a bookkeeping write.
4. **one clock** — `Config.Now` is what `NewAuthenticator` stores and what
   `Deps.Now` calls, so liveness, issuing, revocation stamps and the session
   listing all read the same clock. `Deps` holds no clock of its own and offers
   no way to set one: it asks `Config` on every call, which is what makes
   "the same clock" a property of the type rather than of the wiring. A frozen
   `Config.Clock` freezes the module rather than half of it.

## Rotating

1. **the binding** — the body is read first and the cookie is the fallback: a
   caller that sent a credential meant that one, and a cookie left over from a
   browser session must not quietly rotate a native client's lineage instead. A
   refusal clears the cookie when that is where the credential came from.
2. **`Endpoints.Refresh`** — mounted only for a strategy that rotates.
3. **`core.find`** — `auth/access/accessjwt/accessjwt.go` — the digest as the
   current credential, then as the previous one.
4. **`Classify`** — `auth/access/accessjwt/rotation.go` — pure, no database.
   `Rotate`, `RotateAgain` inside the grace window, `Replay` after it, `Unusable`
   otherwise — and `Unusable` also for a session left untouched longer than
   `SessionConfig.IdleTTL`, which is the deadline the opaque path applies through
   `Session.Live`. The `Window` it takes carries both durations, and `Presented`
   carries the row's `last_used_at`, so one strategy cannot disagree with the
   other about when a session is over ([[D-088]]).
5. **`core.rotate`** — mints the replacement, loads the grants and signs the
   access token *first*, then compare-and-swaps on `(id, token_hash, revoked_at
   IS NULL)`. Nothing that can fail sits between the swap and the answer, so a
   signing or grants failure leaves the presented credential spendable
   ([[D-098]]).
6. **the lost swap** — `core.reread` — a swap that changed no row is a race, not
   a verdict: the row is read again by id and the *presented* digest is
   classified against what is there now. `Rotate` or `RotateAgain` means another
   refresh won, and this one swaps again from the winner's digest, so both
   callers leave with a usable credential; anything else is the refusal it always
   was. Bounded by `rotationAttempts`. A read-then-write with no swap at all
   would issue two lineages from one session.
7. **`core.answer`** — mints the access token with
   `exp = min(now + AccessTTL, session.ExpiresAt)`. `expires_at` is absolute and
   no rotation moves it, so a refresh a second before it ends buys a second, not
   another `AccessTTL` ([[D-088]]). It carries `aud` unless the deployment waived
   it ([[D-097]]).
8. **`core.close`** — on a replay the whole lineage goes, and the revocation list
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
   is logged and does not fail the sign-out: the rows are committed and the
   caller is already out.
6. **`Deps.ReannounceRevocations`** — `auth/access/access.revocation.go`, over
   `Store.SessionsRevokedSince` — what closes the window step 5 leaves. It reads
   the rows revoked since a moment and not yet expired and tells the sinks again,
   returning the failure rather than logging it, because its caller is a worker.
   `Runtime.ReannounceRevocations` is where a composition root reaches it, and
   re-telling a sink about a session it already knows costs a duplicate entry and
   nothing else ([[D-072]]).

The registry itself is filled at **`Mount`** — `auth/access/access.runtime.go` —
from `Issued.Revocations`, and is shared by pointer with every `Deps` the runtime
builds, including the one behind `Runtime.SetPassword`.

## Files

| File | What it decides |
|---|---|
| `auth/access/access.runtime.go` | the factory, the mount refusals, the guards |
| `auth/access/access.strategy.go` | the strategy seam, `RevocationSink`, and the opaque implementation |
| `auth/access/access.revocation.go` | the sink registry, the announcement every closing path makes, and the replay of one that failed |
| `auth/access/access.repo.go` | `LiveSessionsOf` — unrevoked, unexpired and not idle out, against the configured clock — and `SessionsRevokedSince`, the journal the replay reads |
| `auth/access/http/accesshttp/crosssite.go` | `CrossSite`, `Protect`, `CodeCrossSite` — where a cookie-borne write says it came from ([[D-102]]) |
| `auth/access/access.subject.go` | `Subject`, `Registrar[P]` |
| `auth/access/access.defaults.go` | the default role: the read a sign-up makes, and the `Seeder` writes that arrange it |
| `auth/access/access.seed.go` | `Sync` — the start-up pass over what the code declared |
| `auth/access/access.endpoints.go` | the seven transport-neutral operations |
| `auth/access/access.authenticator.go` | a session row becomes a principal |
| `auth/access/access.protection.go` | the attempt limiter and observer seams, the in-process limiter, the Argon2 bulkhead |
| `auth/access/access.secret.go` | the hasher, the PHC grammar and its bounds, the session token and its digest |
| `auth/access/access.config.go` | the two session deadlines, the password bounds and the identifier ceiling, and `Config.Now` — the module's one clock |
| `auth/access/access.deps.go` | what a use case is built from, and `Deps.Now`, which is `Config.Now` and holds nothing of its own |
| `auth/access/usecase.*.go` | the use cases |
| `auth/access/http/accesshttp/accesshttp.go` | the route table and the endpoint names |
| `auth/access/http/accesshttp/delivery.go` | the three deliveries, what silence takes, and what a rotation is not allowed to move |
| `auth/access/http/accesshttp/cookies.go` | the cookie policy, the names and paths, and the split between body and cookie |
| `auth/access/http/access{net,gin,fiber}/` | decode, read the delivery, call, write, set the cookies |
| `auth/http/authhttp/cookie.go` | the guard's other end of a cookie-borne access token |
| `auth/access/accessjwt/rotation.go` | `Classify`, the pure half of rotation |
| `auth/access/accessjwt/accessjwt.go` | issuing, the audience, the answer-then-swap order, the re-read after a lost swap, the replay response |
| `auth/access/accessjwt/revokeredis/` | the deny-list |

## Tests that walk this flow

- `auth/access/access_runtime_test.go` — the mount refusals and their control,
  and that an enrolment refuses before it writes anything.
- `auth/access/access.defaults_test.go` — the default role is whatever the table
  says, an absent binding grants nothing, a sign-up reads it before it creates,
  and the seed writes are idempotent with their controls.
- `auth/access/accessjwt/rotation_test.go` — every arm of `Classify`, including
  the grace boundary on both sides and the idle deadline with its control.
- `auth/access/accessjwt/accessjwt_test.go` — the lifetime matrix `Build`
  refuses, the matrix it accepts, that a duration written below zero is refused
  by value where an omitted one takes the default, and that a rotation close to
  the session's end mints a token that ends with it.
- `auth/access/accessjwt/rotation_race_test.go` — the refresh that loses the
  compare-and-swap leaves with a usable credential rather than a 401, and a
  rotation that cannot mint its answer writes nothing, with the successful
  rotation as its control.
- `auth/access/accessjwt/audience_test.go` — a token is refused by a service it
  was not minted for, silence takes the issuer, the waiver is explicit, and a
  key-provider outage or a cancelled request is not an authentication refusal.
- `auth/access/access.secret_test.go` — a stored hash outside its bounds is an
  unreadable column and not a mismatch, with the readable hash as its control,
  and the fuzz target whose corpus includes the digest length that used to panic.
- `auth/access/access.protection_test.go` — the attempt ceiling and what a
  successful sign-in gives back, that an over-long field costs neither a hash nor
  a statement, the bulkhead's refusal and its control, and the three password use
  cases refusing an account that holds more than one credential.
- `auth/access/http/access{net,gin,fiber}/*_test.go` — the triplet: the prefix,
  the separately-mounted sign-up, that every named endpoint reaches a handler,
  that each transport reads the delivery header and writes a credential cookie
  with its attributes intact, and — in
  `TestACookieBorneWriteFromAnotherSiteIsRefusedByThisTransport` — that each one
  reads its own headers and cookie jar for the cross-site check and that its
  handlers actually ask.
- `auth/access/http/accesshttp/delivery_test.go` and `cookies_test.go` — the
  delivery rules and the cookie split, which is where the behaviour lives.
- `auth/access/http/accesshttp/crosssite_test.go` — the cross-site policy, with
  `TestNothingIsRefusedWhereThereIsNoAmbientCredentialToSpend` as the control
  that a read, a cookie-less request and a header-borne credential all pass.
- `auth/access/access.clock_test.go` — the configured clock decides liveness
  (`TestASessionIsWeighedAgainstTheClockTheRestOfTheModuleReads`) and stamps
  revocations, `TestDepsHasNoClockOfItsOwnToDriftFromTheConfiguredOne` replaces
  `Config` on a built `Deps` and reads the new clock back through `Deps.Now`, the
  session listing narrows on expiry and idleness, and a revocation the sink
  missed is replayed from the rows with the failure handed back rather than
  logged.
- `auth/http/authhttp/cookie_test.go` — the guard reads the cookie, still reads
  the Authorization header, and refuses a request that presents both.
