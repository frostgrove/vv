# access — who the caller is, and what they may do

```go
import "github.com/frostgrove/vv/auth/access"
```

```bash
go get github.com/frostgrove/vv/auth/access
```

**Module:** separate · **Depends on:** `vv` (auth, crud, errs, port),
`google/uuid`, `golang.org/x/crypto`

`auth` is the contract — a `Principal` and a `Guard`. `access` is a working
implementation of one, the way `apikey` and `authjwt` are: seven tables, session
credentials, argon2id passwords, and RBAC with per-subject exceptions.

Import it when you want sign-in and authorization as rows in your database
rather than as claims in a token.

| Also | |
|---|---|
| [accessjwt](accessjwt.md) | signed access tokens over a rotating refresh credential |
| [revokeredis](revokeredis.md) | a revocation list for it, in Redis |
| [accessnet](accessnet.md) / [accessgin](accessgin.md) / [accessfiber](accessfiber.md) | the routes, one module per framework |

---

## The shape

Two calls. Everything else is built for you.

```go
runtime, err := access.New(access.RuntimeSpec{
	Source: source,
	Config: cfg.Access,
	Logger: logger,
})

users, signUp, err := access.Mount(runtime, access.SubjectSpec[SignUpForm]{
	Type:      "user",
	Prefix:    "",                   // "" mounts at /auth/login
	Directory: userDirectory,
	Normalize: strings.ToLower,
	Registrar: userRegistrar,
	Strategy:  access.OpaqueToken(),
})
```

`SubjectSpec` is the documentation: a field you leave out is a build failure
rather than a behaviour somebody has to know about. Nothing hands you a raw
`Store`, hasher or authenticator to wire yourself — those are the pieces whose
order and pairing matter, and a consumer that assembled them by hand would get
one wrong in a way that compiles.

`Mount` answers two values because a subject has two halves with different
shapes: the `*MountedSubject` every binding needs, and the `*SignUpUseCase[P]`
carrying **your** sign-up payload. Nothing erases that type and asserts it back.

## The subject

Nothing here names a user. Every row points at a **subject** — a type and an id,
the shape Laravel gives `model_has_roles`. The store behind a type is a
`Directory` you implement. Three methods, all reads:

```go
type Directory interface {
	SubjectType() SubjectType
	Active(ctx context.Context, id uuid.UUID) (bool, error)
	Describe(ctx context.Context, id uuid.UUID) (Profile, error)
	Touch(ctx context.Context, id uuid.UUID) error
}
```

`Active` is read on every authenticated request, which is what makes
deactivating an account lock it out on the next call rather than when its
session happens to expire.

`Mount` refuses at start-up on a subject type registered twice, two subjects
under one prefix, and a directory that answers for a type other than the spec's.
All three otherwise fail at run time as a caller authenticated against the wrong
store.

## Strategies

A **strategy** is how one kind of caller holds a session. One value declares
both ends — what is minted at sign-in and what is accepted on the next request —
because wired separately they agree until somebody changes one, and the failure
is either everybody logged out at once or a token minted for one subject type
accepted as another.

| | issued | verified | revocation |
|---|---|---|---|
| `OpaqueToken()` | 256 random bits, digest in `sessions` | one read by digest | the row (an allow-list) |
| [`accessjwt.Strategy(...)`](accessjwt.md) | short signed token + rotating refresh | signature, no session read | delayed by the access TTL |
| the same, with `Revocation` | " | signature + deny-list | immediate, by session id |

`OpaqueToken` is the default and usually the right answer. Every property a
product asks of a session is a property of a row: signing out closes it,
deactivating an account locks it, a demoted role bites on the next request. What
it costs is one read per request. Reach for `accessjwt` when a verifier cannot
reach the database — another service, another perimeter — and not before.

**Verification is per subject, not per API.** Each mounted subject has its own
guard, and the route group a request arrived on selects it before anything is
parsed:

```go
guard := users.Guard()          // this subject's verifier
admin := runtime.AdminGuard()   // a chain over the declared strategies, for shared routes
```

A deployment that issues one kind of token runs one verifier and answers "that
is not a valid token" rather than a 401 after several failed attempts.

## Three things this module does not do

All three are [[D-066]], and each one is yours.

**It creates no accounts.** There is no `Provision` on the port. Your row has
your columns; a port thin enough for a library to describe would be a field list
that fits nobody. You write the row in a `Registrar[P]` and the library does the
rest of the enrolment in the same transaction.

**It mounts no routes.** No web framework is imported under `auth/access`.
Routes exist as opt-in bindings, one module per framework.

**It normalises no identifiers.** `credentials.identifier` is stored and
compared byte for byte. An address folds, a Google `sub` must not, and only
whoever issues an identifier knows which — so `SubjectSpec.Normalize` is applied
by the library on both sides of the column, and you supply the rule once.

## Registering somebody

Implement `Registrar[P]` over your own payload. It runs inside the enrolment
transaction, so a duplicate refused by your own unique index rolls the
credential back with it:

```go
func (this *Registrar) Create(ctx context.Context, form SignUpForm) (uuid.UUID, string, error) {
	if !this.config.Enabled {
		return uuid.Nil, "", registrationClosed()
	}
	account, err := this.users.Save(ctx, &User{Email: form.Email, Name: form.Name, IsActive: true})
	if err != nil {
		return uuid.Nil, "", err
	}
	return account.ID, form.Email, nil        // id, and what it signs in with
}

func (this *Registrar) Password(form SignUpForm) string { return form.Password }
func (this *Registrar) Role() auth.Role                 { return "client" }
```

Adding a field to the sign-up form is an edit to `SignUpForm` and to `Create`,
and to nothing under `access`.

## Signing in

An identifier is unique **within** a subject type ([[D-067]]), so a sign-in says
which type it is for. The surface supplies it; you never pass it from a body.

```go
response, err := users.Endpoints().SignIn(ctx, access.SignInRequest{
	Email:    form.Email,          // folded by SubjectSpec.Normalize
	Password: form.Password,
}, agent)
```

Every refusal is the same refusal — an unknown identifier, a wrong password and
a deactivated account answer alike, and the password is verified against
`DummyHash()` when no credential was found so the response time says nothing
either.

## Declaring permissions

A module declares what it enforces beside the code that enforces it:

```go
runtime.Declare(access.ModuleGrants{
	Module:      "billing",
	Permissions: []access.PermissionDef{{Code: PermInvoiceRead, Name: "See invoices"}},
	Roles:       map[auth.Role][]auth.Permission{"accountant": {PermInvoiceRead}},
})
```

`runtime.Sync(ctx)` at start-up, before the server accepts anything. It adds and
never deletes, and recomputes `admin` to hold everything — including permissions
declared after it was seeded.

## Configuration

`access.Config` carries cleanenv tags, so keep one field for the whole context:

```go
type Config struct {
	Addr   string        `yaml:"addr" env:"ADDR"`
	Access access.Config `yaml:"access"`
}
```

| Key | Env | Default | What it bounds |
|---|---|---|---|
| `session.ttl` | `ACCESS_SESSION_TTL` | `720h` | absolute; nothing moves it |
| `session.idle_ttl` | `ACCESS_SESSION_IDLE_TTL` | `168h` | closes an unused session |
| `session.touch_interval` | `ACCESS_SESSION_TOUCH_INTERVAL` | `5m` | how stale `last_used_at` may get |
| `password.min_length` | `ACCESS_PASSWORD_MIN_LENGTH` | `10` | the floor, in characters |

Length and nothing else: a composition rule shortens the search space it claims
to widen. Check whatever else you like in your `Registrar`.

`Config.Clock` is not loaded from anywhere. It is the test seam — every expiry
rule here is otherwise untestable without sleeping.

## The schema

`auth/access/migrations/00001_access.sql` — copy it into your own migrations
directory and rename it with your own timestamp. It is a file to copy rather
than an embedded FS on purpose: a migration is a fact about *your* schema,
applied on your schedule.

Seven tables: `permissions`, `roles`, `role_permissions`, `subject_roles`,
`subject_permissions`, `credentials`, `sessions`. Adding columns of your own is
safe — a repository selects the columns its model names, so a column this module
has never heard of is yours alone.

`credentials` is unique on `(subject_type, provider, identifier)`: two
independent domains may both know an `ops@example.com` ([[D-067]]).

`subject_type`/`subject_id` cannot be a foreign key, and the file says so:
nothing at the database level removes a subject's grants when the subject goes.
Deactivate rather than delete, and the directory answers for the rest.
