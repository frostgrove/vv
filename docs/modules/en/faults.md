# faults — the decorator that names the field

```go
import "github.com/shardit-io/vv/crud/decorators/faults"
```

**Module:** root · **Depends on:** `crud`, `errs`, `probe`

The adapters classify — a refused statement comes back an `errs.Fault` carrying a
code, a kind and one violation with whatever the driver named. What they cannot
fill is the **path**, because a column is meaningless without the table it
belongs to, and an adapter has no `crud.Meta`.

This decorator is that one hop ([[D-043]]). It is also where the
[probe](probe.md) plugs in.

---

## The minimum

```go
users := Users.Bind(db, faults.Enrich[User, int64]())
```

Now a 409 names the model field it happened at, instead of nothing.

Both type parameters have to be written at the call site, because nothing else in
the signature carries them.

## The whole thing

```go
cat, err := catalog.Load(ctx, db)
if err != nil { log.Fatal(err) }

users := Users.Bind(db,
    security.Gate(policy),
    faults.Enrich[User, int64](
        faults.WithProbe(probe.Full(cat)),
        faults.WithProbeError(func(op string, err error) {
            log.Printf("the %s probe failed: %v", op, err)
        }),
    ),
)
```

`WithProbe` is what turns **one** violation into **every** violation the payload
caused ([[UC-017]]).

## Options

| Option | Does |
|---|---|
| `WithProbe(h)` | wire a handler onto the two single-row writes — `Save` and `Update` |
| `WithProbeFor(op, h)` | wire one verb by name: `Save`, `SaveAll`, `Update`, `UpdateAll`, `Delete`, `DeleteAll` |
| `WithProbeError(fn)` | where a probe failure goes. **It is advisory — log it, never render it** |
| `WithSource(src)` | name the datasource the probe runs on. Only needed when this is not the innermost middleware |

The batch verbs keep the cheap answer. A batch is where the cost multiplies and
where a client is least likely to be a form, so `SaveAll` and the rest stay on
`probe.Simple` until `WithProbeFor` says otherwise.

`WithProbe` sets two verbs at once and `WithProbeFor` sets one, so **the last
option wins** — put the narrower one second.

```go
faults.Enrich[Doc, int64](
    faults.WithProbe(probe.Full(cat)),                       // Save and Update
    faults.WithProbeFor("SaveAll", probe.Full(cat)),         // …and batches too
)
```

---

## Order: it goes last

It is the **innermost** middleware — last in the `Bind` list, so it wraps the
repository directly.

```go
Users.Bind(db, security.Gate(policy), faults.Enrich[User, int64]())
//              ^ outer                ^ inner
```

Two reasons, both load-bearing:

- every driver error is enriched **before** anything above can see it, so a
  service layer's own wrapping does not have to know about faults;
- the gate's refusals pass through untouched — a 403 is not a driver error and
  there is nothing here to add to it.

If you put it somewhere else, the repository underneath is no longer
`crud.Sourced` and the declaration refuses. `faults.WithSource(db)` is the way
out.

## What it will not do

- **It never invents a fault.** An error that is not one is returned exactly as
  it arrived. A decorator that manufactured faults would turn every closed pool
  into a structured 500 that looked classified.
- **It never invents a path.** A column from another table, an unknown column, or
  no column at all leaves the path nil and marks the violation `Approximate`. A
  column name in `field` would be a live [[D-044]] breach — the path is the one
  thing that is rendered.

## Failing at declaration

With a probe wired in, `Bind` refuses at start-up rather than at request time
([[D-021]]): a table the catalog does not know, a primary key that is not a row
identity on its own, a `Skip` naming no constraint. See
[probe](probe.md#fails-at-start-up-not-at-request-time).

## The savepoint

Under `probe.WithSavepoints()` on a transaction vv owns, the decorator takes a
savepoint **before** the write — a savepoint cannot be taken after the fact — and
rolls back to it when the write fails, so a PostgreSQL transaction the failure
poisoned can still run the probe statement.

## See also

- [probe](probe.md) — what `WithProbe` takes, and its caps and controls
- [sqlfault](sqlfault.md) — where the fault comes from in the first place
- [catalog](catalog.md) — required by `probe.Full`
- [[FL-017]] a failed write becomes every violation · [[D-043]] one hop per layer
