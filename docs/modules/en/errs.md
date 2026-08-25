# errs — the error contract

```go
import "github.com/shardit-io/vv/errs"
```

**Module:** root, and it will get its own version line at the first tag
· **Depends on:** the standard library, and nothing else — not even `crud`
· **Contract manifest:** yes ([[D-048]])

What a failed operation *is*, as a set of value types and five interfaces. It is
the JPA half of the split: interfaces and conventions that anything can
implement, including a service with no database at all.

**Import it when** your own service layer produces validation errors, when you
declare codes of your own, or when you write a message catalogue. Everything in
the library below the transport already speaks it.

---

## The problem it removes

A client `POST`s a form with a taken email, a non-existent organisation and an
under-age user. Without this, that is three round trips — the database stops at
the first constraint it reaches, and the response echoes the driver:

```json
{"error":"conflict","message":"conflict: ERROR: duplicate key value violates unique constraint \"users_email_key\" (SQLSTATE 23505)"}
```

With it, one response:

```json
{
  "type": "error",
  "errors": {
    "validation": [
      { "field": ["user", "email"],  "error_code": "unique",      "message": "user with this email already exists" },
      { "field": ["user", "org_id"], "error_code": "foreign_key", "message": "the organisation does not exist" },
      { "field": ["user", "age"],    "error_code": "check",       "message": "age must be at least 18" }
    ]
  }
}
```

No constraint name, no table name, no column name, no SQLSTATE, no driver prefix
([[D-044]]). `field` is the path the **client** sent, `error_code` is stable and
machine-readable, `message` came from a catalogue ([[UC-017]]).

---

## Start here — a signup that fails

The most common case there is: somebody registers with an email that is already
taken. This is the whole of it, end to end.

### The model

A `users` table with a unique index on `email`.

```go
type User struct {
    ID    int64  `db:"id,pk,auto" json:"id"`
    Email string `db:"email"      json:"email"`
    Name  string `db:"name"       json:"name"`
}

var Users = basic.Define[User, int64, UserUpdate]("users")
```

### The wiring — two lines you would not otherwise write

```go
// 1. Say which engine is answering, so a refusal carries a code.
db := crudsql.Postgres(sqlDB, crudsql.WithFaults(sqlfault.New("postgres")))

// 2. Let the repository name the field the violation happened at.
users := Users.Bind(db, faults.Enrich[User, int64]())

mux := http.NewServeMux()
crudnet.New(users).Mount(mux, "/users")
http.ListenAndServe(":8080", crudnet.Errors()(mux))
```

That is the entire set-up. No catalogue, no probe, no vocabulary of your own —
those come later and each is optional.

### What the client gets

```http
POST /users
{"email": "ann@x.io", "name": "Ann"}
```

```http
409 Conflict

{"type":"error","errors":{"validation":[
  {"field":["email"],"error_code":"unique","message":"this value is already taken"}
]}}
```

Three things to notice, because they are the point:

- **`error_code` is `unique`** — stable, machine-readable, and the client can
  branch on it without parsing a sentence.
- **`field` is `["email"]`**, lowercase — the key *the client sent*, not the
  `Email` Go field and not the `users_email_key` constraint.
- **`message` is a default** from the standard vocabulary. Replace it with your
  own text whenever you like; see [Messages](#messages).

The status is 409 because `unique` maps to `KindConflict`. It appears under
`validation` rather than `general` because it **names a field** — so a form can
mark the input. The group says what the client can act on, not where the failure
came from.

### What your Go code sees

The same error, from `users.Save(ctx, &u)`:

```go
err := users.Save(ctx, &u)

// The branch you already had keeps working — this is additive ([[D-038]]).
if errors.Is(err, crud.ErrConflict) { … }

// And now there is more underneath it.
if f, ok := errs.AsFault(err); ok {
    f.Kind                    // errs.KindConflict
    f.Violations[0].Code      // errs.CodeUnique
    f.Violations[0].Path      // ["Email"] — the model field
    f.Violations[0].Origin    // errs.OriginState — it collided with a stored row
}
```

> **Why the path is `Email` here and `email` on the wire.** Each layer
> translates one hop and only its own ([[D-043]]). The repository knows the
> model, so it answers `Email`; the transport knows what the client sent, so it
> finishes the job. You do not write either hop for the simple case.

### Your own rules produce the same thing

A service-layer refusal is not a lesser kind of error — it lands in the same
list, with the same shape, and renders through the same envelope:

```go
func (s userService) Create(ctx context.Context, cmd port.CreateCommand[User]) (User, error) {
    if !strings.Contains(cmd.Model.Email, "@") {
        return User{}, errs.Validation().
            Field("Email").Code(errs.CodeInvalidFormat).
            Fault()
    }
    return s.DefaultService.Create(ctx, cmd)
}
```

```http
422 Unprocessable Entity

{"type":"error","errors":{"validation":[
  {"field":["email"],"error_code":"invalid_format","message":"this value is not in the expected format"}
]}}
```

422 rather than 409, because `invalid_format` maps to `KindValidation` — the
payload is wrong in itself, rather than colliding with something stored.

### Getting *all* the problems at once

Everything above reports **one** violation, because that is what a database
does: the first constraint it reaches ends the statement. To get the rest, add
the [catalog](catalog.md) and the [probe](probe.md) — two more lines, at
start-up:

```go
cat, err := catalog.Load(ctx, db)          // read the schema once
if err != nil { log.Fatal(err) }

users := Users.Bind(db, faults.Enrich[User, int64](
    faults.WithProbe(probe.Full(cat)),     // find the others
))
```

Now the same request that broke three constraints answers with all three, which
is the response at the top of this page.

### Where each piece lives

| You want | Add | Page |
|---|---|---|
| A code on a refused write | `crudsql.WithFaults(sqlfault.New(engine))` | [sqlfault](sqlfault.md) |
| The field it happened at | `faults.Enrich[M, ID]()` | [faults](faults.md) |
| Every violation, not the first | `probe.Full(cat)` + `catalog.Load` | [probe](probe.md) · [catalog](catalog.md) |
| Your own codes and statuses | `errs.Codes` | [below](#the-vocabulary) |
| Wording, per locale | `errs.LoadMessages` | [below](#messages) |
| The client's key instead of the model's | `cmd/vv -adapter` | [cmd/vv](vv-cli.md) |

**None of it is on by default.** Wire nothing and a 409 is still a 409 — you
just get no `error_code` and no `field`.

---

## The five value types

### `Code` — what a client branches on

A stable string. The constants are the standard set; **nothing here is closed** —
declare your own of the same type.

| Group | Codes |
|---|---|
| Integrity | `unique` `not_unique` `foreign_key` `restrict` `required` `check` `exclusion` |
| Data | `too_long` `out_of_range` `invalid_format` `invalid_enum` |
| Concurrency | `stale_version` `deadlock` `serialization_failure` `lock_timeout` `transaction_aborted` |
| Request | `malformed_body` `invalid_id` `unknown_field` `bad_query` |
| Coarse | `conflict` `not_found` `forbidden` `unauthenticated` `unavailable` `internal` |

A code is **never derived from anything a driver said** — a code built out of a
CHECK expression's source text carries the column names with it.

### `Kind` — the transport class

Eight values, and a transport maps **the kind and never the code**. That is what
lets a service declare fifty codes of its own without touching a status table
([[D-049]]).

| Kind | HTTP | gRPC |
|---|---|---|
| `KindInternal` | 500 | `Internal` |
| `KindNotFound` | 404 | `NotFound` |
| `KindUnauthorized` | 401 | `Unauthenticated` |
| `KindForbidden` | 403 | `PermissionDenied` |
| `KindRetryable` | 503 | `Unavailable` |
| `KindConflict` | 409 | `AlreadyExists` |
| `KindValidation` | 422 | `InvalidArgument` |
| `KindBadRequest` | 400 | `InvalidArgument` |

`KindInternal` is **zero on purpose**: a kind that lost its meaning says 500
rather than claiming a 4xx it cannot support.

### `Path` — where it happened, in the client's words

```go
errs.Path{errs.Named("user"), errs.Named("email")}   // ["user","email"]
errs.ParsePath("items[3].email")                     // ["items",3,"email"]
```

Three renderings, one value: `MarshalJSON` for the envelope, `String()` for a
log, `Pointer()` for an RFC 6901 pointer.

Names and positions, never columns. The translation from one to the other
belongs to the layer that performed the mapping, **one hop each** ([[D-043]]).

### `Violation` — one thing that was wrong

```go
type Violation struct {
    Path        Path
    Code        Code
    Origin      Origin       // OriginInput or OriginState
    Message     string
    Params      map[string]any  // feeds a template; stays server-side
    Source      Source          // storage provenance; internal, never rendered
    Approximate bool            // a hop could not be resolved and was not invented
}
```

A constraint the database refused to break and a rule a validator refused are
**the same type in the same list**, told apart by `Origin`. Merging them is the
entire point: a payload with a malformed email *and* a taken email is two
violations at one path, and a client making two round trips to learn that is the
problem this exists to remove.

`Origin` decides three things: the status (an input rule is 422, a collision with
stored state is 409), whether the offending value may ever be echoed (only
`OriginState` reveals something the caller could not already see), and whether
the probe runs (a payload already known bad is not probed).

### `Fault` — a classified failure with every violation under it

```go
type Fault struct {
    Kind       Kind
    Code       Code
    Message    string   // developer-facing. Never rendered
    Violations []Violation
    Op         string   // the repository verb: "Save", "Update"
    Entity     string
    Partial    bool     // a cap was hit; the set is incomplete
    Detail     Detail   // dialect, SQLSTATE, constraint, table — never rendered
}
```

**It wraps and never replaces** ([[D-038]]). A caller who wrote
`errors.Is(err, crud.ErrConflict)` before any of this existed keeps that branch,
and a caller who wants the list reaches it with `errors.As` — both on the same
value, through as many further wrappings as a service layer adds.

```go
if f, ok := errs.AsFault(err); ok {
    for _, v := range f.Violations {
        log.Printf("%s at %s", v.Code, v.Path)
    }
}
```

There is **no `Retryable` field**. `KindRetryable` already says it, and a second
spelling would make representable the one state [[D-040]] forbids: a conflict
that claims to be retryable, with no rule for which a transport should believe.

---

## Building one by hand

A service layer is a first-class producer of violations, not an afterthought.

```go
return errs.Validation().
    Field("Age").Code("too_young").Params(errs.P{"min": 18}).
    Field("Email").Code(errs.CodeInvalidFormat).
    Entity("User").Op("Save").
    Wrapping(crud.ErrConflict).
    Fault()
```

Nine entry points: `New(kind)`, `Validation()`, `BadRequest()`, `Conflict()`,
`NotFound()`, `Forbidden()`, `Unauthorized()`, `Retryable()`, `Internal()`.

Steps: `Field(name)` · `At(path)` · `General()` · `Code(c)` · `Message(s)` ·
`Params(p)` · `Origin(o)` · `Source(s)` · `Approximate(b)` · `Detail(d)` ·
`Op(s)` · `Entity(s)` · `Partial(b)` · `Wrapping(errs...)` · `Fault()`

**One rule resolves the chain.** `Code`, `Params` and `Message` apply to the
violation opened by the most recent `Field`, `At` or `General`; before any
violation is opened, `Code` and `Message` apply to the fault itself.

`Field` names the **model** field. Turning it into `["user","age"]` on the way
out is the job of the layer that performed that mapping and of no other.

---

## The vocabulary

`Codes` is a **value**, not a package-level table. Two libraries in one binary
may each declare `too_long`, and with a global registry whichever was linked
first would decide the other's status.

```go
codes := errs.StandardCodes()
codes.Add("too_young", errs.KindValidation, "must be at least {min}")
codes.Add("quota_exceeded", errs.KindForbidden, "your plan does not allow this")
```

Hand it to whatever needs it:

```go
crudnet.Errors(crudhttp.WithCodes(codes))
sqlfault.New("postgres", sqlfault.WithCodes(codes))
```

`Add` returns `errs.ErrCodeRedeclared` if the code is already declared with a
different kind. The zero value and a nil `*Codes` both read as empty rather than
panicking.

---

## Messages

### The catalogue

One flat JSON file per locale. Not a package, not a manifest entry — just files.

```
messages/
  default.json
  ru.json
  de.json
```

```json
{
  "unique": "this value is already taken",
  "user.email.unique": "somebody already signed up with that address",
  "email.unique": "that email address is taken",
  "too_long": "at most {max} characters"
}
```

```go
//go:embed messages
var messages embed.FS

cat, err := errs.LoadMessages(errs.StandardCodes(), messages, "messages")
```

### The lookup ladder

For a violation at `["user","email"]` with code `unique`:

```
user.email.unique  →  user.unique  →  email.unique  →  unique  →  the code's default
```

Shaped after Spring's `MessageSource`: an override is as narrow or as broad as
its author needs, with no configuration schema to learn.

**Only the first and last named steps take part**, so the ladder is four rungs
deep whatever the path is. A violation at `["order","items","email"]` reads
`order.email.unique → order.unique → email.unique → unique`, and a key spelling
the whole path is never consulted.

`Messages.Load(fsys, dir)` adds a locale at run time. `Locales()` lists them.
`Missing(locale)` reports which declared codes that locale does not cover — wire
it into a test and a half-translated catalogue fails the build.

The vocabulary is what the ladder falls through to, so a **partial catalogue is
the designed case**, not a broken one.

---

## The SPI — five interfaces a third party implements

| Interface | One method | Implemented by |
|---|---|---|
| `Classifier` | `Classify(error) (*Fault, bool)` | [sqlfault](sqlfault.md), or your ORM adapter |
| `Resolver` | `Resolve(Path) (Path, bool)` | a generated `<Model>Mapper`, `port.Fields`, a body index |
| `MessageSource` | `Message(ctx, Violation, locale) (string, bool)` | `errs.Messages`, or your i18n library |
| `CodeMapper` | `CodeFor(*Fault, Violation) (Code, bool)` | a service that wants `email_taken` where the classifier said `unique` |
| `FieldViolation` | `Namespace/Tag/Param/Value` | **go-playground/validator, structurally** |

`errs.Chain(resolvers...)` applies them in order. **If any hop declines, the path
is returned as transformed so far and the result is false** — the caller keeps
the partial translation and marks the violation `Approximate` rather than
shipping a guess.

### The validation bridge, at no dependency cost

`validator.FieldError` satisfies `errs.FieldViolation` **structurally**, so
neither package imports the other:

```go
if verrs, ok := err.(validator.ValidationErrors); ok {
    vs := errs.FromFieldViolations("CreateUserRequest", verrs...)
    return errs.Validation().Fault()   // …carrying vs
}
```

`Tag()` becomes the `Code`, `Namespace()` becomes the `Path` — with
`Items[3].Email` parsing straight into an index step — and `Param()` and
`Value()` go into `Params` for a message template.

> **Register validator's tag-name function**, or `Namespace()` reports Go field
> names and every path is quietly wrong. That is a start-up step, not a runtime
> surprise. `TestWithoutTheTagNameFuncEveryPathIsGoFieldNames` in `test/bridge/`
> pins exactly what you get if you forget.

---

## Sorting

`errs.SortViolations(vs)` puts a list in one stable order, so the fault and the
rendered body agree and a response is byte-identical run to run.

## See also

- [sqlerr](sqlerr.md) — a driver error becomes a `Code`
- [sqlfault](sqlfault.md) — the `Classifier` that assembles a `Fault`
- [probe](probe.md) — how the *other* violations get found
- [crudhttp](crudhttp.md) — the envelope and the status table
- [[UC-017]] every error for one payload at once · [[UC-015]] map a failure to the transport
- [[D-038]] a fault is additive · [[D-043]] one hop per layer · [[D-044]] the payload names nothing internal
