# D-056 — An authentication failure is a fault that wraps a sentinel, and its reason never leaves the process

**Status:** accepted
**Invariant:** a 401 is built with `auth.Unauthenticated`, which produces an `errs.Fault` of `errs.KindUnauthorized` wrapping `auth.ErrUnauthenticated`. Nothing in `crud`, `port` or `errs` changes to carry it, and the reason it was refused travels in the wrapped error — never in `Fault.Message`, never in a body.
**Narrowed by:** [[D-078]] — inability to obtain verification keys is not an
authentication failure and remains a typed infrastructure error.

## The decision

```go
func Unauthenticated(reason string) error {
	return errs.Unauthorized().
		Code(errs.CodeUnauthenticated).
		Wrapping(fmt.Errorf("%w: %s", ErrUnauthenticated, reason)).
		Fault()
}
```

Three properties, and each one is load-bearing:

**The kind is already in the table.** `errs.KindUnauthorized` and
`errs.CodeUnauthenticated` were declared in phase 1 with nothing producing them;
`crudhttp.StatusFor` already answered 401 and `crudgrpc.CodeFor` already answered
`UNAUTHENTICATED`. So the 401 path needed no new arm anywhere. `port/kind.go`,
`errs/code.go` and `crud/errors.go` are untouched by this change.

**The sentinel is in `auth`, not in `crud`.** `crud.ErrForbidden` is there
because the repository layer raises it. Nothing in that layer authenticates, so
a `crud.ErrUnauthenticated` would be a sentinel `crud` never returns — and
adding one would edit a package `make check-tiers` seals, for no reader.

**`Wrapping` is what keeps it branchable.** It is the only door to
`Fault.Unwrap` ([[D-038]]), so `errors.Is(err, auth.ErrUnauthenticated)` is true
through as many further wrappings as a service layer adds, and [[D-015]] holds
without a type assertion.

## Why the reason is in the wrapped error and not in `Fault.Message`

Because `Fault.Message` is rendered, and the obvious reading of its
documentation says it is not.

`port/violations.go` synthesises one violation for a fault that carries none —
which is every 401 — and it copies the fault's message into it:

```go
vs = append(vs, errs.Violation{Code: code, Message: f.Message})
```

`port.message` then prefers that text over the code's declared default. So

```go
errs.Unauthorized().Code(errs.CodeUnauthenticated).Message("signature does not verify")
```

renders as

```json
{"type":"error","errors":{"general":[
  {"error_code":"unauthenticated","message":"signature does not verify"}]}}
```

and tells whoever is probing which half of the token to work on next. The
comment on `Fault.Message` — *"developer-facing. never rendered, and never in
Error()"* — is true of `Fault.Error` and of the violations a classifier
produces, and not of this path. That is the trap this decision exists to record.

**And a 401 that distinguishes its reasons is an oracle.** "No such user" and
"wrong password" as separate answers is user enumeration, which is the same
failure [[D-008]] describes for rows and answers the same way: one response for
every cause. Every reason renders as `unauthenticated` / *"authentication is
required"*, whether the token was absent, expired, forged, for another audience,
  or valid for a tenant that no longer exists. A key provider that did not
  answer has made no credential decision; [[D-078]] keeps that failure out of
  this equivalence class.

## What a caller can still find out

The reason is not destroyed, only kept inside. `errs.AsFault` then `Fault.Unwrap`
reaches it, which is what a log line that wants the diagnostic does. Note that
`errors.Unwrap` does **not** — a fault unwraps to a `[]error`, which the
single-error form does not walk — and that `Fault.Error()` will not print it
either, because [[D-047]] makes that string classification only.

## Where 401 sits among the other answers

`port/kind.go:rank` is unchanged and already ordered it: `Internal(0) <
NotFound(1) < Unauthorized(2) < Forbidden(3)`.

- A row hidden by a scope stays **404**, even for an unauthenticated caller.
  [[D-008]] outranks this decision and should.
- An authenticated caller who lacks a permission is **403**, through
  `security.Denied` and `crud.ErrForbidden` as before. `port.FaultOf` synthesises
  that fault with an empty message, so the denial's own reason does not leak
  either — which is why `security.Denied(action, reason)` was safe to keep
  as-is.
- An absent principal reaching a policy is **401** and not 403: nothing has been
  decided yet, and telling a caller "forbidden" for a request nobody
  authenticated sends them to the wrong fix.

## What it forbids

- Do not put a refusal's reason in `Fault.Message`, `Violation.Message` or
  `Violation.Params` on an authentication failure. It is rendered.
- Do not give a 401 more than one code. `errs.CodeUnauthenticated` is the whole
  vocabulary; a `token_expired` a client could branch on is the oracle again.
- Do not add `crud.ErrUnauthenticated`, and do not add an arm to
  `port.sentinelKind` or `port.FaultOf` for one. The fault carries its own kind.
- Do not set `WWW-Authenticate`. A `Basic` challenge makes a browser open a
  modal no API wants, and the bearer challenge's `error=` parameter exists
  precisely to say which check failed.
- Do not echo the caller's own input in a refusal — a `kid`, a key, a subject.
  `authjwt`'s "the key set has no key for this token" is deliberately not "no
  key with kid `abc123`".
- Do not report which authenticator in an `auth.Chain` refused, or how many were
  tried. That is a list of the schemes a deployment accepts.

## Where it lives

- `auth/errors.go` — `ErrUnauthenticated`, `Unauthenticated`,
  `Unauthenticatedf`.
- `port/violations.go:Violations` — the synthesised violation that copies
  `Fault.Message` when a fault carries none. Unchanged, and the reason this
  decision is written down.
- `auth/http/authhttp/authhttp.go` — `Refuse`, and the paragraph on
  `WWW-Authenticate`.
- `auth/authjwt/parser.go` — `Parse`, where every credential-verification
  failure collapses to one answer and typed key-source availability travels
  unchanged ([[D-078]]).
- `auth/apikey/apikey.go` — `Store`'s three results, which keep an outage from
  being reported as a bad key.

## Proven by

- `TestTheReasonForA401NeverReachesTheBody` — `auth/errors_test.go`. It carries
  the control that makes it mean something: a second subtest builds the same
  fault with the reason in `Fault.Message` and asserts the leak **is** there, so
  if `port.Violations` ever stops copying that field the control fails and says
  the positive test now proves nothing. Verified by putting `.Message(reason)`
  back into `Unauthenticated` and watching the positive arm fail with the reason
  in the rendered body.
- `TestAnAuthenticationFailureIsA401ThatWrapsTheSentinel` — same file:
  `errors.Is`, the status, the code, and the reason still reachable through
  `Fault.Unwrap`.
- `TestEveryRefusalIsTheSameAnswerToAClient` — `auth/authjwt/parser_test.go`.
- `TestTheRefusalBodyIsTheSharedEnvelopeAndNamesNoReason` — carried file-for-file
  by `auth/http/authnet`, `auth/http/authgin` and `auth/http/authfiber`.
- `TestARefusalThatWillNotEncodeIs500AndSaysNothing` in
  `auth/http/authhttp/refuse_test.go` — the branch below that one: when the
  envelope itself will not marshal, the client gets 500 and no sentence. Its
  control is the operator's line, which *does* name the reason — so the silent
  body is the guard rather than an empty fixture. Verified by replacing the
  `WriteHeader` with `http.Error(w, marshalErr.Error(), 500)` and watching the
  reason appear in the response.
- `TestARefusalCarriesEveryHeaderTheRendererAskedFor` — same file, with the
  control that no `WWW-Authenticate` is invented for a renderer that asked for
  none: a bearer challenge's `error=` parameter is this decision's disclosure
  in a header.
- `TestAStoreFailureIsNotARefusal` — `auth/apikey/apikey_test.go`: an outage is
  the store's own error and renders as the 500 it is, not as a bad key.

- `TestNamingTwoAudiencesRequiresBothOfThem` in `auth/authjwt/parser_test.go` —
  golang-jwt's `WithAudience` assigns the expected set and means "any of", so
  calling it once per audience left only the last one expected: `Audience("a","b")`
  accepted a token audienced to "b" alone and **rejected** one audienced to "a".
  Wrong in both directions at once. Every other test in the package passes one
  audience, which is why a single-audience test cannot see it and this one has
  three arms.
- `TestAnOutageAnywhereInAChainBeatsARefusal` in `auth/guard_test.go` — an
  authenticator distinguishes "this credential is wrong" from "I could not tell",
  which is what `apikey.Store`'s three results are for. `Chain` returned the last
  error, so that distinction survived only when the failing authenticator was
  wired last: a store outage became "your key is invalid", wrong for the client
  and invisible to whoever watches the 5xx rate.
- `TestALargeIntegerClaimSurvivesAtAnyDepth` in `auth/authjwt/claims_test.go` —
  two hops had to be right and one was. Without `WithJSONNumber` an id above 2^53
  is rounded by encoding/json before this package sees it; without a recursive
  `narrow` a nested claim comes back as `json.Number` and a caller comparing it to
  an int64 gets a scope that narrows to nothing. A tenant id off by one is a row
  belonging to the wrong tenant.
- `TestAJWKSWithNoURLRefusesToStart` in `auth/authjwt/jwks_test.go` — the reason
  for a refusal stays inside the process, so an empty key-set URL answers every
  request with exactly the 401 a forged token gets. It is the hardest
  misconfiguration in the package to diagnose from outside and now fails at
  declaration ([[D-021]]).
- `TestALeaderThatDisconnectsDoesNotFailTheWaiters` in the same file — the
  single-flight elects a leader, and the leader used to fetch on its own request
  context. Under net/http that is cancelled when that one client disconnects, so
  an abandoned request failed every waiter and suppressed the refetch for the
  whole `JWKSMinRefresh` window.
- `TestARefusedStreamIsClassifiedLikeARefusedCall` in
  `crud/rpc/crudgrpc/streamerrors_test.go` — `authgrpc.Stream` returns an
  unrendered fault and relies on something downstream, exactly as its unary twin
  does. There was no downstream: `Errors` is a unary interceptor and had no stream
  counterpart, so a refused stream answered `Unknown` where a refused call
  answered `Unauthenticated`. The first subtest is the control that this really
  was `Unknown`.

## See also

[[D-055]] [[D-008]] [[D-038]] [[D-044]] [[D-047]] [[D-015]] [[D-049]]
