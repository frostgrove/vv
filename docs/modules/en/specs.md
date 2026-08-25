# specs — Specifications and the Criteria API

```go
import "github.com/shardit-io/vv/repo/decorators/specs"
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
