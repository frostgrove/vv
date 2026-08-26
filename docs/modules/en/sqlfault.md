# sqlfault — a driver error becomes a fault

```go
import "github.com/frostgrove/vv/crud/sqlfault"
```

**Module:** root · **Depends on:** `errs`, `errs/sqlerr`, `crud`, `catalog`

The layer that assembles. It walks whatever tree the driver returned, decides
whether the database refused to break a constraint, asks
[sqlerr](sqlerr.md) which violation it was, fills in the columns the driver did
not name, and hands back an `errs.Fault` carrying `crud.ErrConflict` underneath.

**This is what `WithFaults` takes.** For most consumers it is one line.

---

## Wiring it

```go
cls := sqlfault.New("postgres")

db := crudsql.Postgres(sqlDB, crudsql.WithFaults(cls))
db := crudpgx.Open(pool,      crudpgx.WithFaults(cls))
```

Engine strings: `"postgres"`, `"mysql"`, `"mariadb"`, `"sqlite"`.

With the [catalog](catalog.md) wired in, a violation the driver reported without
column names gets them — a composite unique on PostgreSQL is the case that needs
it:

```go
cat, err := catalog.Load(ctx, crudsql.Postgres(sqlDB))
if err != nil { log.Fatal(err) }

cls := sqlfault.New("postgres", sqlfault.WithColumns(sqlfault.FromCatalog(cat)))
db  := crudsql.Postgres(sqlDB, crudsql.WithFaults(cls))
```

And with a vocabulary of your own:

```go
codes := errs.StandardCodes()
codes.Add("email_taken", errs.KindConflict, "that address is already registered")

cls := sqlfault.New("postgres", sqlfault.WithCodes(codes))
```

| Option | Does |
|---|---|
| `WithCodes(*errs.Codes)` | the vocabulary. Default `errs.StandardCodes()` |
| `WithColumns(Columns)` | fills in columns the driver did not name. `FromCatalog(cat)` is the usual one |
| `WithExtractor(Extractor)` | replace the by-shape tree walk, for a driver that hides its error |

---

## What you get without it

Nothing breaks. `crud.ErrConflict` still comes back and a 409 is still a 409 —
you just get no `error_code` and no `field`, so the client is told *that* the
write failed and not *what* about it.

## The engine is declared, never derived

`New` takes the engine as a plain string, and you pass a literal.

Nothing here inspects a `crud.Dialect`, because `crud.Dialect.Name()` answers
`"mysql"` for MariaDB — and a type switch over the dialect types is the same
derivation written differently. It would send MariaDB's `4025` through MySQL's
table ([[D-046]]).

The consequence is deliberate: **the only reachable failure is no code, never a
wrong one.**

> Passing a `crud.Dialect` to `crudsql.Open`, `crudsql.From` or `crudsql.Source`
> classifies the *status* and not the *code*. The four named constructors —
> `Postgres`, `MySQL`, `MariaDB`, `SQLite` — declare the engine. For a joined
> foreign transaction, `WithFaults` is the way to say which engine is answering.

---

## Two gates, and they answer different questions

| | Answers | Decides |
|---|---|---|
| `sqlfault.Integrity(err)` | did the database refuse to break a constraint? | `crud.ErrConflict` |
| `sqlerr.Classify(…)` | *which* violation was it? | `errs.Code` |

**`Integrity` is deliberately wider.** A class-23 number nobody provoked — MySQL's
`1216` and `1217` are two — and a SQLite low-byte-19 code whose high byte no probe
produced are conflicts here and unclassified there. Such a violation reaches a
caller as a **409 with no code** rather than as a 500.

It has three arms, because four engines answer in three different ways:

- **class 23** — the portable half, and PostgreSQL's whole answer;
- **`HY000`** — MySQL saying it has nothing more specific. Its CHECK and
  missing-default errors land there, so the *number* is the only thing separating
  them from an ordinary server error;
- **no state at all** — SQLite, which has none and never will.

The no-state arm reads a SQLite-specific number and not the merged one, because
the SQLSTATE is optional in MySQL's error packet: without that care, a refused
handshake (`1043`, whose low byte is 19) would answer 409 with the driver's
sentence in the body.

**A fault is built only when a code *and* its kind are both known.** `errs.Kind`'s
zero value is `KindInternal`, so a fault built from an unwired vocabulary would
claim 500 for a duplicate key — refusing to build is the only answer that cannot
lie.

## The pieces, if you need them directly

```go
sqlfault.Extract(err) *sqlerr.Err   // flatten a driver error tree, by shape
sqlfault.Integrity(err) bool        // the conflict gate
sqlfault.Wrap(cls, err) error       // what an adapter calls
```

`Extract` answers nil only for a nil error. Everything else produces an `Err`,
because "no SQLSTATE and no number" is a legitimate answer — a connection that
never reached a server is one — and a caller has to tell that from "nothing was
extracted".

`Wrap` is total and order-sensitive. An error already carrying a fault is
returned untouched, so a `crud.Source` wrapping another adapter's executor does
not classify twice. Then the classifier is asked. Then, **whatever it answered**,
`Integrity` decides the sentinel — so a third-party `errs.Classifier` can neither
forge a `crud.ErrConflict` nor drop one ([[D-038]]).

## Bringing your own

`Classifier` satisfies `errs.Classifier`, which is the interface a third party
writes against. An ORM that produces errors of its own implements that one
method and goes through the same `WithFaults` door.

`Columns` is one method and hands back names and nothing else — a third party can
supply a schema without importing `catalog`, and **cannot** hand back a
predicate, a definition or DDL text, none of which has any business near a
renderer ([[D-044]]).

## See also

- [sqlerr](sqlerr.md) — the four dialect tables underneath
- [catalog](catalog.md) — where the missing column names come from
- [errs](errs.md) — the `Fault` this builds
- [crudsql](crudsql.md) · [crudpgx](crudpgx.md) — the adapters that call it
- [[FL-014]] a driver error becomes a public violation · [[D-046]] the classifier key
