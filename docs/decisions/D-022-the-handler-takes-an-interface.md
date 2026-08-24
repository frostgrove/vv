# D-022 — The HTTP handler takes an interface, not a struct

**Status:** accepted
**Invariant:** a binding's `New` must accept anything satisfying its `Repository[M, ID, U]`, and the handler must never reach past that interface to a concrete repository type.

## The decision

`crudhttp.Repository[M, ID, U]` declares the eight methods the handler calls:
`Meta`, `GetByID`, `Get`, `GetAll`, `Save`, `Update`, `Delete`, `Count`.
`crud.Repo[M, ID, U]` satisfies it, `specs.Repo` satisfies it, and so does any
struct that embeds either. `Handler` stores the interface and nothing else.

`crudfiber.Repository` and `crudgin.Repository` are type aliases for it, not
separate declarations ([[D-034]]). A generic alias is the same type, so a service
written for one binding satisfies the other with no change — which is what keeps
"the repository is transport-neutral" true rather than merely plausible.

## Why

A service layer has to be able to take the repository's place without the
handler noticing. Embedding is how that works in Go:

```go
type UserService struct {
    crud.Repo[User, int64, UserUpdate]   // every method, promoted
    client *ent.Client
}

func (s UserService) Save(ctx context.Context, u *User) error {
    if !strings.Contains(u.Email, "@") {
        return fmt.Errorf("%w: email is not an address", crud.ErrForbidden)
    }
    return s.Repo.Save(ctx, u)
}
```

One override, ten promoted methods, and `crudfiber.New(UserService{…})`
compiles. Taking a concrete `crud.Repo` instead would mean either no service
layer at all, or a hand-written forwarder per method per service.

**Why the interface is narrow.** It lists what the handler calls, not what the
repository can do. `UpdateAll`, `DeleteAll`, `Exists` and `Tx` are absent
because no route uses them, and a narrow interface is a small thing to implement
by hand for anyone who wants to.

**Why `Update` is typed against `U` here but `any` on `crud.Core`.** The handler
binds a JSON body into a `U` and hands it over; typing it is free at that layer
and catches a mismatched DTO at compile time. `Core`'s erasure exists for
middleware inference ([[D-001]]) and does not reach this far up.

**Why all three type parameters are inferred.** `New[M, ID, U](repo …)` takes the
repository as its first argument, so Go infers all three from it. That is why
`crudfiber.New(articles).Routes()` and `crudgin.New(articles).Mount(r, "/articles")`
are each the whole mount. It is also why the option
type is `Option[M, ID, U]` rather than a plain closure — an option written inline
in the same call infers too, and `WithQuery[…]` only needs its parameters
spelled when the option is built separately.

## What it forbids

- Do not type-assert the repository to a concrete type inside the handler, or
  add a `Unwrap()` to the interface to get at one. That closes the seam.
- Do not widen `Repository` with a method no route calls. Every addition is a
  method every service layer has to supply.
- Do not change `New` to take the repository anywhere but the first parameter
  position in a way that breaks inference.
- Do not put business rules in the handler and call it a service layer.
  `BeforeSave` and `BeforeUpdate` exist for transport-shaped concerns; anything
  that must hold regardless of the transport belongs in the embedding struct or
  in a `security.Policy` ([[D-017]]).

## Where it lives

- `http/crudhttp/repository.go:Repository` — the interface, with the reason on
  it.
- `http/crudfiber/handler.go:Repository` / `http/crudgin/handler.go:Repository`
  — the per-binding aliases.
- `http/crudfiber/handler.go:Handler` / `http/crudgin/handler.go:Handler` — each
  holds `repo Repository[M, ID, U]`.
- `http/crudfiber/handler.go:New` / `http/crudgin/handler.go:New` — infer all
  three parameters.
- `http/crudfiber/options.go:Option` / `http/crudgin/options.go:Option` —
  parameterised the same way so inline options infer.
- `crud/repo.go:Repo` — the struct that satisfies it and that a service embeds.
- `repo/decorators/specs/executor.go:Repo` — embeds `crud.Repo`, so it satisfies
  the interface too and adds the specification methods.
- `docs/usage-guides/ent.md` §12 and `docs/usage-guides/gorm.md` §12 — the
  service-layer pattern.

## Proven by

- `TestAServiceLayerCanStandInForTheRepository` in
  `http/crudfiber/handler_test.go` and `http/crudgin/handler_test.go` — the
  decision, stated as a test, once per binding.
- `TestHTTPServiceLayerIsHonoured` in `test/integration/http_test.go` and
  `TestGinHTTPServiceLayerIsHonoured` in `test/integration/http_gin_test.go` —
  the same thing end to end against a live database, so a service override that
  is bypassed by a route shows up. Both mount the *same* `articleService`, which
  only compiles because the two `Repository` types are one type.
- `TestNewInfersItsTypeParametersFromTheRepository` in
  `http/crudfiber/options_test.go` and `http/crudgin/options_test.go` — the
  inference half.
- `TestRoutesMountEveryDocumentedEndpoint` and
  `TestRegisterMountsOnAnExistingRouter` in both bindings' `handler_test.go`.
- `TestEveryRouteMapsARefusalTheSameWay` in both bindings' `edge_test.go` — a
  route that reached past the interface would show up as a route that handles
  errors differently.

## See also

[[D-001]] [[D-012]] [[D-017]] [[D-021]] [[D-034]]
