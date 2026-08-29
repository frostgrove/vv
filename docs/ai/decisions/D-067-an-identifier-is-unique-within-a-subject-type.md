# D-067 — An identifier is unique within a subject type, not across all of them

**Status:** accepted
**Invariant:** `credentials` is unique on `(subject_type, provider, identifier)`.
Every lookup by identifier carries the subject type in its predicate, and a
sign-in that does not name one is refused rather than answered.

## The decision

Two kinds of caller may both know an `ops@example.com`. The unique index spans
the subject type, `access.LoginCommand` carries a required `Subject`, and
`Store.CredentialFor` takes it as an argument rather than checking it after the
row comes back.

A sign-in surface is therefore mounted per subject —
`accesshttp.Surface.Prefix` — and it supplies the type from the surface rather
than from the request body.

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

**Because a prefixed route is a security boundary.** `/staff/auth/login` that
did not constrain the type would sign a customer in through the staff surface,
holding a staff-shaped session. The constraint lives in the surface, so no
binding can forget it.

## What it forbids

- Do not query `credentials` by `(provider, identifier)` alone.
- Do not restore the two-column unique index; a deployment that has run with
  this one may hold rows the narrower index cannot accept.
- Do not read the subject type off a request body. It comes from the mounted
  surface, which is what the prefix selects.
- Do not default `LoginCommand.Subject` to anything. An empty one is an error,
  not a lookup across every type.

## Where it lives

| File | What it holds |
|---|---|
| `auth/access/migrations/00001_access.sql` | `uq_credentials_subject_type_provider_identifier` |
| `auth/access/access.repo.go` | `Store.CredentialFor`, which takes the subject type |
| `auth/access/access.command.go` | `LoginCommand.Subject` |
| `auth/access/credentials/login.usecase.go` | the refusal for an absent subject type |
| `auth/access/http/accesshttp/accesshttp.go` | `Surface.SignIn`, which supplies it |
