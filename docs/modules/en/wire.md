# wire — the public body, and its road to the private one

```go
import "github.com/frostgrove/vv/crud/wire"
```

**Module:** root · **Depends on:** `crud`, `reflect`

Two one-method interfaces and three coverage assertions. That is the whole
package, and it exists because `<Model>Update` answers the wrong question when a
client is the one asking.

`<Model>Update` is the **persistence patch**: every column an `UPDATE` may
write, including the ones only your own code writes — a deactivation flag, a
last-login stamp. Parameterise a public PATCH binder with it and every one of
those becomes something a client may send. Take them out of it and your own code
loses them. The way out is a second type, and a total map between the two
([[D-105]]).

---

## The seam

| | |
|---|---|
| `PatchMapper[P, U]` | `Update(patch P) U` — the public PATCH body becomes the persistence DTO |
| `Presenter[M, R]` | `Response(model M) R` — the model becomes the answer body |
| `IdentityPatch[U]()` | a `PatchMapper[U, U]` that returns its argument |
| `IdentityPresenter[M]()` | a `Presenter[M, M]` that returns its argument |

The two identities are what every existing call site already gets:
`crudfiber.New`, `NewFor`, `Serving` and `ServingFor` fill them in, so a
resource mounted straight onto the model still takes `U` as its body and answers
`M`. `NewWire` and `ServingWire` are the explicit form underneath, and they take
all four — mapper, patcher, presenter and options ([[D-021]]).

```go
h := crudfiber.ServingWire(
    service,
    user.UserInputMapper{},
    user.UserPatchMapper{},
    user.UserPresenter{},
)
```

Nothing here knows about the generator. Hand-write a `PatchMapper` for one
resource and the binding is the same.

---

## The coverage assertions

| | |
|---|---|
| `CoversPatch[U, P](except ...string) error` | every field of the persistence DTO has a field of the same name and type in the public patch body, or is named as an exclusion |
| `CoversCreate[M, In](except ...string) error` | the same against `crud.Schema.Insert` — the columns an `INSERT` may carry |
| `CoversResponse[M, R](except ...string) error` | the same against `crud.Schema.Fields` — every column of the model |
| `MustCoverPatch` · `MustCoverCreate` · `MustCoverResponse` | the same, panicking, for an `init` |

A generated `vv_wire_gen.go` ends in one `init` calling all three
([[FL-029]]). A hand-written body can call them too, and the exclusion list is
where you say what you left out on purpose.

Four disagreements are reported, each by name:

| what | message |
|---|---|
| a column with no public field | `no field for LastLoginAt` |
| a public field the source does not carry | `a field for Nickname, which the model does not carry` |
| a type that disagrees | `Price carries *int64 where *int is expected` |
| an exclusion naming nothing | `an exclusion for Password, which the model does not carry` |

The last one is what stops the exclusion list becoming a graveyard: delete the
column and the declared exclusion refuses too, so somebody removes the line.

The comparison is deliberately made against the **compiled** model. The
generator reads source text; these read `reflect` and `crud.Schema`. A column
added to the model with nothing regenerated is a start-up panic naming the
column, which is [[D-050]]'s argument applied to the public half.

---

## Which fields a generated body carries

`vv generate resource` derives each body by **narrowing** and writes what it
chose into `resource.manifest.yml` beside the package:

| body | starts from | drops |
|---|---|---|
| create | the create field set — no relation, no `generated`, no lock, no database-owned key | `secret` |
| patch | the columns `<Model>Update` writes | `secret` |
| response | every column | `secret`, and anything `-skip` removed |

Taking a name out of `fields` in the manifest needs nothing. Putting one back
that the narrowing excluded requires `confirmed: true` beside it, and the
confirmation is bound to the derivation it was given for, so a model that
changes shape asks again. See [vv-cli](vv-cli.md#generate-resource) and
[[D-105]].

---

## See also

- [crudfiber](crudfiber.md) · [crudgin](crudgin.md) · [crudnet](crudnet.md) ·
  [crudgrpc](crudgrpc.md) — `NewWire` and `ServingWire`, the same names on all four
- [vv-cli](vv-cli.md) — `vv generate resource`, which produces the bodies and the mappers
- [port](port.md) — `Mapper[In, M]`, the create half, which is `port`'s because
  the service takes the model
- [[D-105]] · [[D-050]] · [[FL-029]] · [[FL-002]]
