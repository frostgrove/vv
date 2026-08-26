# D-022 — The HTTP handler takes an interface, not a struct

**Status:** accepted
**Invariant:** a binding's `New` must accept anything satisfying its `Repository[M, ID, U]`, and the handler must never reach past that interface to a concrete repository type.

## The decision

`crudhttp.Repository[M, ID, U]` declares the eight methods the handler calls:
`Meta`, `GetByID`, `Get`, `GetAll`, `Save`, `Update`, `Delete`, `Count`.
`crud.Repo[M, ID, U]` satisfies it, `specs.Repo` satisfies it, and so does any
struct that embeds either. `Handler` stores the interface and nothing else.

`crudfiber.Repository`, `crudgin.Repository` and `crudnet.Repository` are type
aliases for it, not separate declarations ([[D-045]]). A generic alias is the
same type, so a service written for one binding satisfies the other two with no
change — which is what keeps "the repository is transport-neutral" true rather
than merely plausible. Phase 5 re-pointed all three at `port.Repository` and
nothing at a call site changed, which is the property this paragraph is about.

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

One override, thirteen promoted methods, and `crudfiber.New(UserService{…})`
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

## What phase 5 changed, and what it did not

The handler no longer holds the repository. It holds a `port.Service` built over
it, and the repository reaches the routes through that ([[D-045]], [[FL-015]]).

The invariant is unchanged and the reason is worth stating rather than inferring:
`New` still takes anything satisfying `Repository` in the first parameter
position, still infers all three type parameters from it, and still never reaches
past the interface. What moved is where the orchestration runs, not what the
handler is allowed to know. The seam this decision exists to keep open is now two
seams — a repository a service stands in for, and a service a binding stands in
front of — and `Serving` is the second one's door.

The fourth type parameter that arrived with `HandlerFor[M, ID, U, In]` is behind
an alias for exactly this decision's sake: `Handler[M, ID, U]` fills it in with
the model, so `New` keeps inferring three. A fourth parameter on `New` itself
would have been `cannot infer In` at every existing call site.

## Where it lives

- `port/repository.go:Repository` — the interface, with the reason on it.
- `crud/http/crudhttp/repository.go:Repository` — the alias that keeps the old
  address working.
- `port/service.go:Service` — the seam this decision predicted, made explicit.
- `crud/http/crudfiber/handler.go:Repository`, `crud/http/crudgin/handler.go:Repository`,
  `crud/http/crudnet/handler.go:Repository` — the per-binding aliases.
- `crud/http/crudfiber/handler.go:HandlerFor` / `crud/http/crudgin/handler.go:HandlerFor` /
  `crud/http/crudnet/handler.go:HandlerFor` — each holds `svc Service[M, ID, U]` and
  the mapper in front of it; `Handler[M, ID, U]` is the alias over it.
- `crud/http/crudfiber/handler.go:New` / `crud/http/crudgin/handler.go:New` — infer all
  three parameters. `:NewFor` infers a fourth from the mapper.
- `crud/http/crudfiber/options.go:Option` / `crud/http/crudgin/options.go:Option` —
  parameterised the same way so inline options infer.
- `crud/repo.go:Repo` — the struct that satisfies it and that a service embeds.
- `crud/decorators/specs/executor.go:Repo` — embeds `crud.Repo`, so it satisfies
  the interface too and adds the specification methods.
- `docs/usage-guides/ent.md` §12 and `docs/usage-guides/gorm.md` §12 — the
  service-layer pattern.

## Proven by

- `TestAServiceLayerCanStandInForTheRepository` in every binding's
  `handler_test.go` — the decision, stated as a test, once per binding.
- `TestHTTPServiceLayerIsHonoured` in `test/integration/http_test.go` and
  `TestGinHTTPServiceLayerIsHonoured` in `test/integration/http_gin_test.go` —
  the same thing end to end against a live database, so a service override that
  is bypassed by a route shows up. `TestNetHTTPServiceLayerIsHonoured` in
  `test/integration/http_net_test.go` is the third. All three mount the *same*
  `articleService`, which only compiles because the three `Repository` types are
  one type.
- `TestNewInfersItsTypeParametersFromTheRepository` in every binding's
  `options_test.go` — the inference half, and
  `TestNewForInfersItsInputFromTheMapper` beside it, which is the half phase 5
  could have broken.
- `TestRoutesMountEveryDocumentedEndpoint` and
  `TestRegisterMountsOnAnExistingRouter` in every binding's `handler_test.go`.
- `TestEveryRouteMapsARefusalTheSameWay` in every binding's `edge_test.go` — a
  route that reached past the interface would show up as a route that handles
  errors differently.

## See also

[[D-001]] [[D-012]] [[D-017]] [[D-021]] [[D-045]] [[FL-015]]
