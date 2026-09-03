# crudhttp — what is HTTP *and* CRUD

```go
import "github.com/frostgrove/vv/crud/http/crudhttp"
```

**Module:** root · **Depends on:** `crud`, `query`, `errs`, `port`, `port/porthttp`, `net/http`

The remainder after two splits: the request shapes a CRUD route has and nothing
else does, plus a compatibility surface over everything that left.

**You probably do not need to import it.** If you are mounting routes,
[crudnet](crudnet.md), [crudfiber](crudfiber.md) and [crudgin](crudgin.md)
re-export what you need. If you are rendering your own bodies or installing the
error middleware, the page you want is [porthttp](porthttp.md).

---

## Where everything went

| What | Where | Why |
|---|---|---|
| the commands, the `Service`, the `Mapper`, the code vocabulary, `port.Violations` | [port](port.md) | none of it is HTTP ([[D-045]]) |
| the status table, the envelope, the `Renderer` seam, `DecodeJSON`, the locale, the raw-body fallback | [porthttp](porthttp.md) | all of it is HTTP and none of it is CRUD ([[D-059]]) |
| the client transport | [remotehttp](remotehttp.md) | it is the client, not the server ([[D-058]]) |
| what is on this page | here | it is HTTP *and* CRUD |

Two tests, asked in that order:

1. **Could a non-HTTP transport implement this without importing `net/http`?**
   If not, it is not `port`'s — a `Renderer` returning an `http.Header` could
   not, which is why the seam is not in `errs`.
2. **Could a subsystem that is not CRUD take this without importing `crud`?**
   If yes, it is `porthttp`'s — the auth middleware answers a 401 through the
   same status table and the same envelope.

## What is actually here

| | |
|---|---|
| `Repository[M, ID, U]` | a generic alias for `port.Repository` — the interface every binding takes ([[D-022]]) |
| `BulkDeleteRequest[ID]` | the `{"ids":[…]}` body of `POST /bulk-delete` |
| `CoerceID[ID](raw string)` | a path parameter becomes the key type — which is why a uuid or a slug key works in a URL with no extra code |
| `NarrowForCount(*query.Request)` | drop everything that means nothing to a `COUNT` |
| `NarrowForEntity(*query.Request)` | keep shaping and eligibility filters; drop keyed-read paging and ordering |
| `Sanitize` · `ClearGenerated` | what a client may not choose on create: a generated key, a `generated` column |
| `Rules` | the five settings that say nothing about a transport, shared by all four bindings — an alias for [`port.Rules`](port.md#the-rules-a-binding-does-not-own) |

`CoerceID` through `Sanitize` · `ClearGenerated` are forwarders over
[port](port.md); they are exported here because an application that writes its
own create route calls them ([[D-045]]). `Rules` is not one of them — it is an
alias for a type that never lived here.

## The forwarders

`crud/http/crudhttp/porthttp.go` re-exports everything [[D-059]] moved:
`Renderer`, `EnvelopeRenderer`, `RenderOption`, `Envelope`, `Groups`,
`MaxViolations`, `DefaultRetryAfter`, `MaxKeptBody`, `MaxBody`, `ErrBadRequest`,
`NewRenderer`, the five `With…` options, `Internal`, `Status`, `StatusFor`,
`KindForStatus`, `KindOf`, `ParseEnvelope`, `BadRequest`, `BadRequestf`,
`BadRequestAs`, `MalformedBody`, `TooLarge`, `BodyResolver`, `DecodeJSON`,
`DecodeJSONKeep`, `DecodeJSONKeepLimit`,
`KeepBody`, `WithBody`, `BodyFrom`, `WithLocale`, `LocaleFrom` and
`AcceptLanguage`.

They are aliases and one-line calls with no behaviour, and the file says so at
the top: a symbol there that grows a body has stopped being a forwarder and
belongs on one side of the split or the other. Re-pointing an alias is not a
breaking change, which is the same trick [[D-034]] landed on.

New code should import [porthttp](porthttp.md) directly.

## Table — a resource's ten routes, said once

| | |
|---|---|
| `Table{Prefix, ReadOnly, Expose}` | where a resource is mounted, and which of the ten routes are there. `Expose` is `port.Operations` and mirrors the binding's `Exposing`, so the declaration is derived from the set the resource is actually mounted with |
| `Table.Routes()` | the same list `Register` walks: `Method`, `Path`, `Name`, `Action` |
| `Table.GuardedBy(policy)` | that list as `[]authhttp.Endpoint`, with the permissions read off the policy the repository is gated with |
| `Table.Guarded(read, write, del)` | the same, over three permissions you write, when the enforcement is somewhere else |
| `Policy` | `RequiredFor(crud.Action) ([]auth.Permission, bool)` — what `GuardedBy` asks; `security.Policy` answers it |
| `Route.Action` | `crud.ActionRead`, `crud.ActionCreate`, `crud.ActionUpdate`, `crud.ActionDelete` |
| route names | `List`, `Get`, `Count`, `CountQuery`, `Query`, `Create`, `Update`, `Replace`, `Delete`, `BulkDelete` |

```go
declared, err := crudhttp.Table{Prefix: "/roles"}.GuardedBy(RolePolicy())

crudhttp.Table{Prefix: "/permissions", ReadOnly: true}.Guarded(PermRoleRead, "", "")

// reads and both deletes, and no route that writes a column
crudhttp.Table{Prefix: "/contracts", Expose: port.Reads | port.Deletes}.GuardedBy(Policy())
```

`GuardedBy` refuses when a route the table mounts performs an action the policy
does not declare: the routes are named, and nothing is returned. An action
declared with no permissions becomes `authhttp.Authenticated`, which is what
`RequirePermission()` naming nothing means. `Guarded` maps create and update
onto `write` and both deletes onto `del`; `GuardedBy` does not collapse them.

Path parameters are spelled `:id`, which is Fiber's and Gin's spelling and the
canonical one here; `crudnet` rewrites it, the way `accessnet` does.

**The paths come from the table and the permissions come from the gate.** A
declaration written out by hand agrees with the router only until somebody adds
a route; one derived from the router agrees with it always, including when both
are wrong — so the paths are checked against the real routing table rather than
written twice ([[D-073]]). The permissions used to be the half worth stating
twice, because nothing could compare a route's declaration with the check that
runs behind it. `security.Policy` now carries its requirement as data, so
`GuardedBy` reads the enforced list instead of asking for a second copy of it
([[D-107]]).

A route that is not a CRUD resource has no table to read: it is guarded inside
the use case's own body, and `vv generate routes` derives its declaration from
that guard instead ([[D-109]], [cmd/vv](vv-cli.md)).

## See also

- [porthttp](porthttp.md) — the status table, the envelope, the renderer
- [port](port.md) — the transport-neutral half
- [remotehttp](remotehttp.md) — the client side
- [crudnet](crudnet.md) · [crudfiber](crudfiber.md) · [crudgin](crudgin.md) — the shells over this
- [[FL-013]] a request through another binding · [[FL-015]] a request through the port layer
- [[D-059]] the HTTP projection belongs to `port` · [[D-045]] the shared half is transport-neutral
- [[D-107]] a resource declares the permissions its gate enforces · [[FL-024]] the boot gate · [[FL-020]] where the policy's value comes from
