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
implementation of one, the way `apikey` and `authjwt` are: eight tables, session
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

Nil-like extension values are refused at the same boundary. A literal nil
`Strategy` deliberately selects `OpaqueToken`; an interface carrying a typed
nil strategy or directory is a configuration error rather than a request-time
panic. Literal nil `Registrar` deliberately omits self-service sign-up, while a
typed nil registrar is refused before the strategy builds or any route can be
advertised.

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

**A strategy whose verifier reads no row is told when one closes.** That is
`Issued.Revocations`, a `RevocationSink` the strategy declares and `Mount`
registers against its subject. `OpaqueToken` declares none and needs none: the
row its verifier reads on the next request is the row the sign-out wrote. A
signed token checks a signature, so a `revoked_at` it never looks at changes
nothing — without the sink, signing out closes the session everywhere except
where the next request will look. Every path that closes a session announces
through it, including the administrator's password reset behind
`Runtime.SetPassword`, and the announcement happens after the transaction
commits. See [[D-072]].

`Mount` builds a custom strategy against a private candidate resolver, then
validates the complete `Issued` result before publishing the subject. `Issuer`
and `Authenticator` are required and must not be literal or typed nil.
`Refresher` and `Revocations` are optional as literal nil; a typed nil is an
invalid advertised capability. A failed `Build` or validation publishes no
directory, grants resolver, revocation sink or mounted subject, so correcting
the same declaration and retrying is safe.

**Verification is per subject, not per API.** Each mounted subject has its own
guard, and the route group a request arrived on selects it before anything is
parsed:

```go
guard := users.Guard()          // this subject's verifier
admin := runtime.AdminGuard()   // a chain over the declared strategies, for shared routes
```

Both take `auth.Option`s, which is how a deployment says where the credential is
read from — see [Where the credentials go](#where-the-credentials-go).

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
```

Adding a field to the sign-up form is an edit to `SignUpForm` and to `Create`,
and to nothing under `access`.

**There is no `Role()` here, and that is [[D-070]].** What a registration grants
is a row in `subject_default_roles`, keyed by the subject type — not a constant,
and not a configuration key. Both of those are strings nothing compares to the
`roles` table until the first sign-up, where a typo answers 500 to a stranger
weeks after it was deployed. A row is resolved when it is written:

```go
seeder := runtime.Seeder()

seeder.EnsureRole(ctx, access.RoleSpec{
	Slug: "client", Name: "Client", System: true,
	Permissions: []auth.Permission{PermInvoiceRead},
})
seeder.SetDefaultRole(ctx, "user", "client")   // refused now if "client" does not exist
```

Both are idempotent, and both are for a seed command rather than for start-up —
see [Seeding](#seeding). No binding grants nothing, which is what an
invitation-only deployment wants.

## Provisioning somebody

An account an administrator created has a profile and no secret, so it cannot
sign in until access has a credential for it. `runtime.SetPassword()` is what
gives it one, and it is the same use case behind an administrator's reset:

```go
closed, err := runtime.SetPassword().Execute(ctx, access.SetPasswordCommand{
	Subject:  access.SubjectRef{Type: "user", ID: account.ID},
	Password: chosen,
})
```

The identifier is the directory's, never the caller's — an administrator who
could choose it could point a credential at an address they control. It closes
every session the subject held and reports how many, because the reason to
perform one is usually that somebody else may be holding one.

It is a method on the runtime and not a value, because it needs the resolver:
call it after the last `Mount`.

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

## Where the credentials go

A session has two credentials with opposite lifetimes, and where each of them
should go depends on what is calling. So it is the request's to say, in one
header, and a deployment serves a browser and a native client with one surface
([[D-075]]).

```go
handler := accessfiber.New(users, accessfiber.Delivering(accesshttp.Cookies{
    Prefix: "/api/v1",   // where this API is mounted
    Secure: true,        // false only on a workstation serving http://localhost
}))
```

| `X-Auth-Delivery` | access token | rotating credential | who asks |
|---|---|---|---|
| `cookies` | HttpOnly cookie | HttpOnly cookie | a browser |
| `refresh-cookie` | body | HttpOnly cookie | a page that sends its own header |
| `body` | body | body | a native client, a command-line tool |

**Silence takes the most closed delivery on offer** — both cookies where they are
configured, the body where they are not. A browser that forgets to ask is still a
browser that cannot read its own credentials; a native client that forgets finds
an empty body and no session, which is a failure somebody sees at once. A value
nobody defined is `invalid_enum` rather than the default, and so is a cookie
asked of a surface with none configured.

**What goes into a cookie leaves the body**, and what goes into the body clears
the cookie it did not go into. Two copies of a credential is one place too many,
and a stale access cookie is worse than that: a guard reading cookies prefers one
to the header, so the page would hold a fresh token and go on acting as the
session it just replaced.

**Rotation is not a choice.** The rotating credential comes back through the
channel it arrived on, whatever the request asks. A script injected into the page
cannot read an HttpOnly cookie but can make the browser send one — and an
endpoint that honoured "give it back in the body" would hand that script a
credential good for weeks from its own machine. What the request still decides
there is the access token.

Without `Delivering`, every credential travels in the body and a request that
asks for a cookie is refused. That is the honest answer from a deployment that
has not decided what `Secure` and `SameSite` should be.

### What the library decides, and you do not

| | |
|---|---|
| the names | `access` and `refresh`, prefixed with the subject's own prefix — `staff_access`. Two kinds of caller on one host would otherwise overwrite each other's access cookie, which is scoped to the whole API rather than to one endpoint |
| the paths | the access cookie to `Cookies.Prefix`, the rotating one to that subject's rotation endpoint alone. A credential attached to every request reaches every log and proxy in front of the API |
| `HttpOnly` | always. A credential cookie a script can read is what this is for avoiding, and an option to turn it off is an option somebody would find |
| `SameSite` | `Strict` unless `Cookies.SameSite` says otherwise. `None` without `Secure` panics at start-up, because a browser discards such a cookie and every session would end at the next request with nothing in any log to say why |

### The other end: a guard that reads the cookie

Both credentials in cookies means no `Authorization` header, so the guard has to
be told where to look. It is one option, and it falls back to the header — so the
same guard serves the browser and the native client:

```go
guard := users.Guard(authhttp.Cookie(accesshttp.For(users).AccessCookie()))
admin := runtime.AdminGuard(authhttp.Cookie(accesshttp.Table{}.AccessCookie()))
```

Under `accessfx`, the same option goes to `Module`, which hands it to the admin
guard. Forgetting it is a sign-in that looks perfectly successful followed by a
401 on everything after it.

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

## Seeding

Two passes, and they are not one on purpose.

| | `runtime.Sync(ctx)` | `runtime.Seeder()` |
|---|---|---|
| what it writes | what the **code** declares: permissions, the roles a `ModuleGrants` names, `admin` | what the **product** decided: the roles this deployment has, and what a sign-up grants |
| when | every start, before the first request | a seed command, when somebody runs it |
| why not the other | which permissions exist follows from which modules are compiled in | running it at every start would revert an administrator's change on the next deploy |

```go
seeder := runtime.Seeder()

_, err := seeder.EnsureRole(ctx, access.RoleSpec{
	Slug: "lawyer", Name: "Lawyer", System: true,
	Permissions: []auth.Permission{PermContractRead, PermContractWrite},
})
_, err = seeder.SetDefaultRole(ctx, "user", "client")
```

`EnsureRole` creates the role if the slug is free and attaches the permissions it
does not already hold. It does **not** overwrite an existing role's name or flag:
renaming a role somebody edited in the admin screen back to what a Go literal
says, on every run, is what makes people stop running the seed. A permission no
module declared is refused rather than attached — a code nothing enforces is a
row that reads like a grant and decides nothing.

`SetDefaultRole` resolves the slug, and writes nothing at all when the binding
already points there, so `updated_at` still answers "when did this last change".
`ClearDefaultRole` turns it off; `DefaultRole` reads it back.

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

Eight tables: `permissions`, `roles`, `role_permissions`, `subject_roles`,
`subject_permissions`, `subject_default_roles`, `credentials`, `sessions`.
Adding columns of your own is safe — a repository selects the columns its model
names, so a column this module has never heard of is yours alone.

`subject_default_roles` is unique on `subject_type` — one kind of caller has one
default — and its `role_id` is `ON DELETE RESTRICT`: deleting the role a sign-up
grants is refused rather than silently turned into "new accounts get nothing".

`credentials` is unique on `(subject_type, provider, identifier)`: two
independent domains may both know an `ops@example.com` ([[D-067]]).

`subject_type`/`subject_id` cannot be a foreign key, and the file says so:
nothing at the database level removes a subject's grants when the subject goes.
Deactivate rather than delete, and the directory answers for the rest.

## accessfx — the fx wiring

```go
import "github.com/frostgrove/vv/auth/access/accessfx"

fx.Options(
    accessfx.Module(configuration.Access),
    fx.Provide(
        accessfx.AsSubject(mountUsers),
        accessfx.AsGrants(whatUsersMayDo),
    ),
)
```

**Module** — it takes uber/fx, so a consumer who assembles the context by hand
never resolves one ([[D-074]]).

| | |
|---|---|
| `Module(config, guardOptions…)` | the runtime, the resolver, the admin guard, the three services, the administrative password reset, and the start-up sync |
| `AsSubject(ctor)` | a mounted subject joins the group |
| `AsGrants(ctor)` | an `access.ModuleGrants` joins the group |
| `Registered` | both groups, as an `fx.In` parameter object |

**The ordering is what this exists to express.** Everything that must know about
every subject — the resolver, the admin guard, the password reset — depends on
`Registered`, so fx cannot build any of them until each contributor has run. A
guard assembled before the last `Mount` would verify some credential formats and
silently refuse the rest; a use case built early would panic on its first call,
at a point where the wiring looks finished.

No subject at all is a misconfiguration and not an empty deployment: every request
would resolve to a principal with no profile, and every sign-in would refuse for a
reason nobody can find. It is refused at wiring.

It mounts **no routes** and names **no subject type** ([[D-066]]), and it provides
no `port.Fields`: where `AuthBodyPaths` has to be registered depends on the
transport, and a `port.Fields` in the graph would collide with the next module
that wanted one of its own ([[D-043]]).
