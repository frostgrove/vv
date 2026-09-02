# D-067 — An identifier is unique within a subject type, not across all of them

**Status:** accepted
**Invariant:** `credentials` is unique on `(subject_type, provider, identifier)`,
and a subject holds at most one `password` credential. Every lookup by identifier
carries the subject type in its predicate, and a sign-in that does not name one is
refused rather than answered.

## The decision

Two kinds of caller may both know an `ops@example.com`. The unique index spans
the subject type, `access.LoginCommand` carries a required `Subject`, and
`Store.CredentialFor` takes it as an argument rather than checking it after the
row comes back.

A sign-in surface is therefore mounted per subject —
`accesshttp.Surface.Prefix` — and it supplies the type from the surface rather
than from the request body.

A second index says the other half: `uq_credentials_password_subject`, unique on
`(subject_type, subject_id)` **where** `provider = 'password'`. An account has one
password, and the three use cases that touch one agree with the index rather than
picking a row: `EnrollUseCase` refuses a subject that already holds one,
`SetPasswordUseCase` and `ChangePasswordUseCase` refuse outright when they lock
more than one. Only `password` is constrained — an account may hold several OIDC
or API-key credentials, and those carry no secret a reset has to replace.

## Why

**Because two subject types are two domains.** A customer and a staff member
are different tables with different lifecycles, and making one of them rename
to register is a rule the database has no business inventing. That is the whole
point of keying on a subject rather than on a user.

**Because the alternative cannot be loosened later.** Global uniqueness →
per-type is an index migration. Per-type → global requires resolving the
duplicates already in the table, by hand, in production.

**Because the check has to be in the WHERE and not after it.** With per-type
uniqueness, `SELECT … WHERE provider = ? AND identifier = ?` has more than one
answer, and a repository returns whichever row the engine reached first. A
handler that then compared the subject type would refuse *sometimes* —
depending on physical row order — which is the failure mode that survives
review and every test with one row in the table.

**Because "reset the password" has to mean every password.** Without the partial
index a subject could hold two password rows under two identifiers, and a reset
rewrote whichever one the code reached first. The other identifier kept signing in
with its old secret — after an administrator had been told the account was
secured, which is worse than not having reset it. Sorting the locked rows made
which row won predictable; it did not make the other row stop working. Refusing in
code without the index leaves the second row creatable by a concurrent enrolment;
the index without the refusal turns an ordinary conflict into a driver error the
caller cannot read. Both, or neither.

**Because a prefixed route is a security boundary.** `/staff/auth/login` that
did not constrain the type would sign a customer in through the staff surface,
holding a staff-shaped session. The constraint lives in the surface, so no
binding can forget it.

## What it forbids

- Do not query `credentials` by `(provider, identifier)` alone.
- Do not restore the two-column unique index; a deployment that has run with
  this one may hold rows the narrower index cannot accept.
- Do not make a password use case pick `credentials[0]` when it locked more than
  one. Choosing deterministically among rows that must not both exist hides the
  state instead of reporting it.
- Do not add the partial index to a live deployment without first finding the
  duplicates: `SELECT subject_type, subject_id FROM credentials WHERE provider =
  'password' GROUP BY 1, 2 HAVING count(*) > 1`. Every extra row is a working
  sign-in somebody may not know about.
- Do not read the subject type off a request body. It comes from the mounted
  surface, which is what the prefix selects.
- Do not default `LoginCommand.Subject` to anything. An empty one is an error,
  not a lookup across every type.

## Where it lives

| File | What it holds |
|---|---|
| `auth/access/migrations/00001_access.sql` | `uq_credentials_subject_type_provider_identifier`, `uq_credentials_password_subject` |
| `auth/access/access.repo.go` | `Store.CredentialFor`, which takes the subject type; `ambiguousPassword` |
| `auth/access/access.command.go` | `LoginCommand.Subject` |
| `auth/access/usecase.login.go` | the refusal for an absent subject type |
| `auth/access/usecase.enroll.go` | `alreadyEnrolled` — the refusal of a second password |
| `auth/access/usecase.set-password.go`, `auth/access/usecase.change-password.go` | the refusal to write when more than one is locked |
| `auth/access/http/accesshttp/accesshttp.go` | `Surface.SignIn`, which supplies it |

## Proven by

`auth/access/access.protection_test.go` —
`TestEnrollingASecondPasswordForOneAccountIsRefusedBeforeItIsWritten` (with a
free-account control),
`TestAPasswordChangeRefusesAnAccountThatHoldsMoreThanOne` (with a
one-credential control) and
`TestAPasswordResetRefusesAnAccountThatHoldsMoreThanOne`.
