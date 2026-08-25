# D-056 — An authentication failure is a fault that wraps a sentinel, and its reason never leaves the process

**Status:** accepted
**Invariant:** a 401 is built with `auth.Unauthenticated`, which produces an `errs.Fault` of `errs.KindUnauthorized` wrapping `auth.ErrUnauthenticated`. Nothing in `crud`, `port` or `errs` changes to carry it, and the reason it was refused travels in the wrapped error — never in `Fault.Message`, never in a body.

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
or valid for a tenant that no longer exists.

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
- `port/violations.go:55` — the synthesised violation that copies
  `Fault.Message`. Unchanged, and the reason this decision is written down.
- `http/authhttp/authhttp.go` — `Refuse`, and the paragraph on
  `WWW-Authenticate`.
- `auth/authjwt/parser.go` — `Parse`, where every verification failure collapses
  to one answer.
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
  by `http/authnet`, `http/authgin` and `http/authfiber`.
- `TestAStoreFailureIsNotARefusal` — `auth/apikey/apikey_test.go`: an outage is
  the store's own error and renders as the 500 it is, not as a bad key.

## See also

[[D-055]] [[D-008]] [[D-038]] [[D-044]] [[D-047]] [[D-015]] [[D-049]]
