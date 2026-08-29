# D-066 — access owns no identity and no identifier rule, and its core owns no route

**Status:** accepted
**Invariant:** No package under `auth/access/` names a subject type or a role
slug beyond `RoleAdmin`, creates an identity, or transforms an identifier. The
core imports no web framework. A subject type reaches this module only as a
value the caller passed in, and an identifier is stored and compared byte for
byte.

## The decision

`access` answers two questions — who is this caller, and may they do this — over
seven tables that point at a *subject*: a type and an id, the shape Laravel
gives `model_has_roles`. Three things that a first draft of such a module
usually contains are deliberately absent.

**No routes in the core.** `auth/access` and `auth/access/credentials` import
no web framework and declare no path. Routes exist, but as opt-in bindings —
`auth/access/http/access{net,gin,fiber}` over the shared `accesshttp` — the
same shape `crud` has. A consumer on Gin never takes Fiber, and a consumer that
wants neither mounts its own handlers over the use cases.

**No account creation.** `access.Directory` has three methods and all of them
read: `Active`, `Describe`, `Touch`. The consumer writes its own row in a
`Registrar[P]` over its own payload type, and `SignUpUseCase` does the rest of
the enrolment in the same transaction — joined through `crud.InTx`, so a
credential that cannot be written rolls the account back with it.

**No identifier normalisation.** `credentials.identifier` is written and read
exactly as supplied. The consumer applies its own rule on both sides.

Consequently there is no registration policy here either: whether a stranger may
open an account is a product question, and it is asked where the account is
created.

## Why

**Because a `Provision` method on the port cannot be written.** An account is
the consumer's row with the consumer's columns — a company id, a phone number, a
locale. A creation port thin enough for this module to describe is either a
fixed field list that fits nobody or a `map[string]any` that type-checks
nothing. Adding a field to a sign-up form must not be an edit to a library.

**Because a creation port drags a subject type in behind it.** The moment this
module creates identities, something here has to decide *which kind* — and every
answer is wrong: a constant spells the consumer's module name, and a config key
is that same coupling as a string that fails at run time. Both were tried in the
application this module was extracted from, and the config key is what made the
absence of a route visible as the actual defect. With creation gone the question
does not arise: the caller already holds the reference.

**Because a lowercasing call is right for one identifier and silently wrong for
the rest.** An email address folds; a Google `sub` is an opaque digit string that
must not; a SAML NameID is whatever the IdP says. The failure from folding the
wrong one is an account reachable under two spellings, and nothing reports it.
Only whoever issues an identifier knows its equality rule.

**Because the consumer already has to be trusted with both sides.** It supplies
the identifier to `EnrollUseCase` and to `LoginUseCase`, so applying one function
before each is the whole of the discipline — the same function it already needs
for its own column.

## What it forbids

- Do not add a method to `Directory` that writes. It reads, and the count of its
  methods is the load-bearing part.
- Do not add a subject-type constant, a role-slug constant beyond `RoleAdmin`,
  or a configuration key naming either.
- Do not call `strings.ToLower`, `TrimSpace` or any equivalent on an identifier
  anywhere under `auth/access/`.
- Do not import a web framework from `auth/access`. A binding lives under
  `auth/access/http/` and is a module of its own.
- Do not put the sign-up payload type on anything but the sign-up. `Endpoints`,
  `MountedSubject` and a binding's `Handler` are not generic, and the register
  handler is separate for exactly that reason — one endpoint of eight needs it,
  and threading it through the rest forced an `any` and an assertion back.
- Do not let a binding decide a subject type or fold an identifier. Both come
  off the `accesshttp.Surface` it was handed, which is what stops a prefixed
  sign-in route from signing somebody in as another subject type.
- Do not make `EnrollUseCase` open its own transaction with `InNewTx`. It joins,
  so a consumer's account write and the credential write commit together.

## Where it lives

| File | What it holds |
|---|---|
| `auth/access/access.api.go` | `Directory`, `SubjectRef`, `SubjectParam`, and the note where `NormalizeIdentifier` used to be |
| `auth/access/access.directory.go` | `NewDirectories`, which refuses a duplicate or unnamed subject type |
| `auth/access/access.config.go` | `Config` — sessions and a password floor, and nothing else |
| `auth/access/usecase.enroll.go` | the half of registration that is not the consumer's |
| `auth/access/usecase.signup.go` | `SignUpUseCase[P]`, the only generic thing in the module |
| `auth/access/access.subject.go` | `Subject` and `Registrar[P]` — what a consumer must supply, as types rather than as prose |
| `auth/access/access.runtime.go` | `RuntimeSpec` and `SubjectSpec[P]`, the two structs that are the whole of what a consumer fills in |
| `auth/access/http/accesshttp/` | the route table and the step between a decoded body and a use case |
| `auth/access/migrations/00001_access.sql` | the seven tables, as a file to copy rather than an embedded FS |

## Proven by

- `access.TestTwoDirectoriesForOneSubjectTypeRefuseToWire` and
  `TestADirectoryWithNoSubjectTypeRefusesToWire`, with
  `TestOneWellFormedDirectoryWires` as the control that keeps them from passing
  vacuously.
- `accessnet`, `accessgin` and `accessfiber` carry the same three test names,
  held by `make check-triplets`.
- The core compiles with no import of a web framework, no container and no
  subject-type literal; `go doc` over it names none.
- `access.TestMountRefusesADirectoryThatAnswersForAnotherType` and the other
  `Mount` refusals, with `TestMountRegistersAWellFormedSubject` as the control.

## See also

- [[D-067]] — why an identifier is unique within a subject type.
- [[D-068]] — why a strategy declares both ends and a guard is per subject.
