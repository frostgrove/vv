# specs — Specifications and the Criteria API

```go
import "github.com/shardit-io/vv/crud/decorators/specs"
```

**Module:** root · **Depends on:** `crud`, and the standard library

JPA's `Specification<T>` and `CriteriaBuilder`, plus a generated metamodel that
the compiler checks. Compose reusable query fragments, name them, and hand them
around as ordinary values.

**Reach for it when** the same filter appears in three handlers, when a query is
assembled from parts a caller chooses, or when you want a renamed column to break
the build rather than a request.

---

## The mapping

| JPA | vv |
|---|---|
| `Specification<T>` | `specs.Specification[M]` |
| `Root<T>`, `CriteriaBuilder` | `specs.Root[M]`, `specs.Builder` |
| `Specification.where(a).and(b).or(c).not()` | `specs.Where(a).And(b).Or(c).Not()` |
| `JpaSpecificationExecutor<T>` | `specs.Executor(repo)` |
| generated `User_` metamodel | `specs.Metamodel[User, userAttrs]()` |

---

## Two ways to write one

### The literal form

```go
func IsActive() specs.Specification[User] {
    return specs.Of[User](func(root specs.Root[User], cb specs.Builder) crud.Predicate {
        return cb.Equal(root.Get("Active"), true)
    })
}

adults := specs.Where(IsActive()).And(specs.Of[User](
    func(r specs.Root[User], cb specs.Builder) crud.Predicate {
        return cb.GreaterThanOrEqualTo(r.Get("Age"), 18)
    }))
```

`Root.Get` takes a Go field name — a column name works too — and may cross a
relation: `root.Get("Author.Name")`.

### The metamodel form

Same result, checked by the compiler. [`cmd/vv`](vv-cli.md) writes the attribute
struct for you.

```go
type userAttrs struct {
    ID        specs.Ord[User, int64]
    Email     specs.Str[User]
    Age       specs.Ord[User, int]
    Active    specs.Attr[User, bool]
    CreatedAt specs.Cmp[User, time.Time]
}

var User_ = specs.Metamodel[User, userAttrs]()   // validated at package init

adults := specs.Where(User_.Active.Eq(true)).And(User_.Age.Gte(18))
```

A renamed column fails at initialisation, naming the field. A wrong value type
fails at compile time.

The generated metamodel expands through relations too, to `-depth` (2 by
default), and never walks back into a model already on the path:

```go
Article_.Views.Gte(100)              // "views" >= $1
Article_.Author.Name.Eq("Ann")       // EXISTS (… authors … name = $1)
Article_.Comments.Approved.Eq(true)  // EXISTS (… comments … approved = $1)
Article_.Author.Name.Desc()          // ORDER BY (SELECT … LIMIT 1) DESC
```

---

## The attribute types

Pick the narrowest one — it decides which methods exist.

| Type | Constructor | Adds |
|---|---|---|
| `Attr[M, T]` | `Attribute[M, T](field)` | `Eq` `Ne` `In` `NotIn` `IsNull` `NotNull` `Asc` `Desc` |
| `Cmp[M, T]` | `Comparable[M, T](field)` | the above, plus `Gt` `Gte` `Lt` `Lte` `Between` for non-`cmp.Ordered` types like `time.Time` |
| `Ord[M, T]` | `Ordered[M, T](field)` | the above, for `cmp.Ordered` |
| `Str[M]` | `Text[M](field)` | the above, plus `Like` `NotLike` `LikeIgnoreCase` `Contains` `StartsWith` `EndsWith` |

Every method returns a `Specification[M]`, so they compose with `Where`, `AllOf`,
`AnyOf` and `Not`.

`Name()` on any attribute answers the canonical model field name it bound to.
That is what the settings and options taking a *name* rather than a predicate
are addressed by:

```go
sqlrepo.SoftDelete(Doc_.DeletedAt.Name())
crud.GroupBy(Order_.Status.Name())
crud.Sum("total", Order_.Amount.Name())
security.Freeze[Doc, int64](Doc_.TenantID.Name())
```

## Relation handles

A generated relation group carries its own path, so a relation is addressed by an
identifier too:

```go
Article_.Comments.Path()          // "Comments"
Article_.Comments.Author.Path()   // "Comments.Author"
```

That is what `sqlrepo.RelationScope`, `crud.Preload`, `crud.PreloadWhere` and
`security.ScopeRelationField` take instead of a literal. The handle also records
the model the path lands on, so pointing one at the wrong model fails at package
initialisation rather than narrowing the wrong table.

`Path`, `RelPath` and `String` all answer the same string, and the reason there
are three is the embedding. The handle is embedded in the group, so `Path` is
promoted one level out, while every column of the *target* model is a field of
that same group one level nearer — and Go resolves the nearer one. A target with
a column called `Path` therefore shadows the method, and `Folder_.Files.Path()`
stops compiling for that one relation. The generated file says so in that group's
doc comment; `RelPath()` is the spelling nothing shadows.

A group only exists where the generator expanded the relation, which `-depth`
controls, and a relation whose target model lives in another package is not
expanded at all ([[UC-007]]).

### The far side of a relation is a different metamodel

A relation scope's predicate is written against the *target* model, so it comes
from the target's own metamodel:

```go
sqlrepo.RelationScope(
    Article_.Comments.Path(),                    // "Comments"
    specs.Predicate(Comment_.Approved.Eq(true))) // "approved" = $1
```

Not `Article_.Comments.Approved` — that is an attribute of an *Article*, bound to
`Comments.Approved`, and it filters articles by their comments as a correlated
`EXISTS` ([[D-005]]). Both are useful; they answer different questions.

## Composing

```go
specs.Where(a).And(b).Or(c).Not()
specs.AllOf(a, b, c)     // AND
specs.AnyOf(a, b, c)     // OR
specs.Not(a)
specs.Lift[User](crud.Eq("Email", "ann@x.io"))   // a plain predicate becomes a specification
```

## The Criteria builder

`specs.CB` is a shared instance; the zero `Builder` works too.

```
Equal        NotEqual     EqualTo      In           NotIn
GreaterThan  GreaterThanOrEqualTo      LessThan     LessThanOrEqualTo
Between      IsNull       IsNotNull
Like         NotLike      LikeIgnoreCase
And          Or           Not          Conjunction  Disjunction   Raw
```

---

## Querying with it

```go
sp := specs.Executor(Users.Bind(db))

one,   err := sp.FindOne(ctx, User_.Email.Eq("ann@x.io"))   // ErrNotFound / ErrNotUnique
first, err := sp.FindFirst(ctx, adults, crud.OrderBy(User_.Age.Desc()))
list,  err := sp.FindAll(ctx, adults, crud.OrderBy(User_.Age.Desc()))
page,  err := sp.FindPage(ctx, adults, crud.Page(2), crud.Limit(20))
n,     err := sp.CountBy(ctx, adults)
ok,    err := sp.ExistsBy(ctx, User_.Email.Eq("ann@x.io"))
n,     err  = sp.UpdateBy(ctx, adults, UserUpdate{Active: ptr(true)})
n,     err  = sp.DeleteBy(ctx, User_.Active.Eq(false))
```

`count`, `exists` and `delete` carry a `By` suffix because Go has no overloading
and the plain names are already taken by the repository this one embeds.

`specs.Executor` **embeds** the plain repository, so `GetByID`, `Save`, `Update`
and everything else still work on the same value.

`FindOne` returns `specs.ErrNotUnique` — which wraps `crud.ErrConflict` — when
more than one row matches. `FindFirst` takes the first instead.

## You are never forced into the decorator

A specification is also a plain option:

```go
users.Get(ctx, specs.As(adults), crud.Page(2))
crud.Where(specs.Predicate(adults))
```

## See also

- [cmd/vv](vv-cli.md) — generates the attribute struct and the metamodel
- [crud](crud.md) — the predicate AST underneath
- [[UC-007]] write typed, compile-checked queries
- [[D-018]] DTOs and metamodels are generated
