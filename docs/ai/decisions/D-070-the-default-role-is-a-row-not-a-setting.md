# D-070 — The role a sign-up grants is a row, not a setting

**Status:** accepted
**Invariant:** What a self-service registration grants is read from
`subject_default_roles`, keyed by the subject type being registered. No
configuration key, environment variable or Go literal in a consumer names it,
and `Registrar[P]` has no `Role` method. Writing the binding resolves the slug
against `roles` first, so the column can only ever hold an id that exists.

## The decision

`SignUpUseCase.Execute` reads the default role for `Subject.Type` inside the
registration transaction and passes it to `EnrollUseCase` as the role to grant.
An absent row grants nothing and is not an error.

The write side is `Seeder.SetDefaultRole(ctx, subjectType, slug)`, which is what
a consumer's seed command calls. It resolves the slug, locks the existing
binding, and does nothing at all when the row already points at that role.

This replaced two earlier shapes, in order:

1. `Registrar.Role() auth.Role` returning a constant — the consumer spelling a
   role slug in Go.
2. The same method returning a configuration key
   (`ACCESS_REGISTRATION_DEFAULT_ROLE`) — the consumer spelling it in YAML.

Both are gone. The method is gone with them, so there is no seam left through
which a registrar can decide the answer.

## Why

**Because neither earlier shape could be wrong out loud.** A slug is only
meaningful against the `roles` table, and both a constant and a config key are
strings nothing compares to it until the first registration. The failure is not a
crash: `EnrollUseCase.grantRole` refuses a role that does not exist, so a
deployment with `default_role: "cleint"` answers 500 on its first sign-up — weeks
after the typo was deployed, to a stranger, on the one endpoint whose whole job
is a good first impression. With a foreign key and a slug resolved at seed time,
the same typo is refused by the command an operator is watching, at the moment
they run it.

**Because it is a fact about a deployment and not about a build.** Which role a
new account gets is changed by the people who run the product, on a running
system, without a deploy — and it has to be inspectable and auditable afterwards
("who changed this, and when"). `updated_at` on a row answers that. An
environment variable answers none of it, and answering it means reading a
container spec.

**Because the answer differs per kind of caller and a global key cannot say so.**
`access` serves several subject types ([[D-066]]); a person who signs up and a
service account that enrols are not given the same role. A single key would have
to grow a per-type spelling, which is a table encoded in a string.

**Because [[D-066]] already forbids the alternative here.** That decision says
this module must not carry a role-slug constant or a configuration key naming
one. The `Role()` method was the loophole: it moved the same string one package
out, into the consumer, where it remained unchecked. A row is the shape that
satisfies both — the module still names no slug, and the slug that exists is one
the database vouched for.

**Because a default was already being resolved against the table anyway.**
`grantRole` looks a role up by slug before granting it. Holding the id in a
column removes nothing and adds the referential integrity the lookup was
standing in for.

## What it forbids

- Do not add a `Role` method back to `Registrar[P]`, nor a `DefaultRole` field
  to `SubjectSpec` or `Config`. Both are the loophole this closed.
- Do not make `SetDefaultRole` accept a role id from a caller instead of a slug.
  Resolving the slug here is what makes the existence check unavoidable.
- Do not make `subject_default_roles.role_id` `ON DELETE CASCADE`. Deleting the
  role a sign-up grants must be refused, not silently turned into "new accounts
  get nothing" — nobody sees that until somebody registers and cannot work.
- Do not make an absent binding an error. A deployment where an administrator
  grants every role is supported, and refusing to start over it would make the
  invitation-only case impossible to express.
- Do not read the default outside the registration transaction. A seed command
  changing it mid-registration would otherwise grant a role that is no longer
  the default by the time the credential commits.
- Do not fold `SetDefaultRole` into `Sync`. Sync is the code's own facts and
  runs at every start; a default written there would revert an administrator's
  change on the next deploy.

## Where it lives

| File | What it holds |
|---|---|
| `auth/access/access.model.go` | `SubjectDefaultRole` — the row, and why it is keyed by subject type |
| `auth/access/access.defaults.go` | `Deps.DefaultRole` (the read), `Seeder` (the idempotent writes) |
| `auth/access/access.repo.go` | `Store.DefaultRoles`, `Store.DefaultRoleRow` |
| `auth/access/usecase.signup.go` | the read, inside the registration transaction |
| `auth/access/usecase.enroll.go` | `grantRole`, and the seam that lets the sign-up hand the row it already read |
| `auth/access/access.subject.go` | `Registrar[P]`, and the note where `Role()` used to be |
| `auth/access/migrations/00001_access.sql` | the table, the unique index on `subject_type`, the RESTRICT |

## Proven by

- `access.TestTheDefaultRoleIsWhateverTheTableSays` — the slug on the row is
  what reaches the enrolment, and the lookup carries the subject type.
- `access.TestASubjectTypeWithNoDefaultRoleGrantsNothing` — the control: no row
  is a state, not a failure. Without it the test above would pass on a function
  that always answered the first row it found.
- `access.TestASignUpReadsTheDefaultRoleBeforeItCreatesAnAccount` — the read is
  the first statement a registration runs.
- `access.TestSettingADefaultRoleThatDoesNotExistIsRefusedAndWritesNothing` —
  the typo is refused at seed time.
- `access.TestSettingTheDefaultRoleToWhatItAlreadyIsWritesNothing`, with
  `TestSettingTheDefaultRoleToADifferentRoleWrites` as the control that keeps it
  from passing on a `SetDefaultRole` that never writes at all.
- `access.TestAResolvedRoleIsGrantedWithoutASecondLookup` — the row the sign-up
  read is the row the enrolment grants, with the no-resolved-role arm as its
  control, and `TestAResolvedRoleForAnotherSlugIsLookedUpAnyway` for the arm
  that refuses to trust a mismatched one.

## See also

- [[D-066]] — the invariant this closes the last hole in.
- [[FL-023]] — where the read sits in the registration path.
