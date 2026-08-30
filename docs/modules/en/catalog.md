# catalog — the schema, read once

```go
import "github.com/frostgrove/vv/crud/catalog"
```

**Module:** root · **Depends on:** `crud`, and the standard library

One database's schema, read at start-up and answered from memory. Four dialects,
all through `crud.Source.Query` — the same two-method seam everything else uses.

**You need it when** you want a violation to name the columns the driver did not
(a composite unique on PostgreSQL, anything at all on SQLite), or when you run
the [probe](probe.md), which cannot work without it.

---

## Loading one

```go
cat, err := catalog.Load(ctx, crudsql.Postgres(sqlDB))
if err != nil {
    log.Fatalf("reading the schema: %v", err)   // fail here, not on the first collision
}
```

Everything after that call is memory. `Table` and `Constraint` take no context
and do no I/O — a signature that accepted one would be a lazy loader, and **a
lazy loader cannot fail at start-up** ([[D-041]]).

A failure is a failure: `Load` answers a nil `Catalog` and never a half-built one
beside a nil error. Degrading quietly to an empty schema would mean the feature
reading it is off in production and the only symptom is that it never reports
anything.

`Load` takes **no options and no timeout**. It runs once at declaration on a
context the application owns, and a default timeout there turns a slow but
healthy start-up into a fatal refusal.

## Looking things up

```go
type Catalog interface {
    Table(name string) (*Table, bool)
    Constraint(table, name string) (*Constraint, bool)
    Dialect() string     // "postgres", "mysql", "mariadb", "sqlite"
}
```

Qualified names use the optional structured capability that `Load` always
returns:

```go
qualified := cat.(catalog.QualifiedCatalog)
events, ok := qualified.TableByRef(crud.TableRef{
    Schema: "analytics",
    Name:   "events",
})
key, ok := qualified.ConstraintByRef(
    crud.TableRef{Schema: "analytics", Name: "events"}, "events_slug_key")
```

The components are looked up exactly; they are never joined into a dotted
string or allowed to fall back to `Table("events")`. A third-party `Catalog`
does not have to implement `QualifiedCatalog`, but `probe.Full` refuses a
qualified repository with `probe.ErrQualifiedCatalog` when it does not. That is
a declaration-time refusal rather than a probe against the wrong same-named
table.

`Constraint` takes the table **as well as** the name, because an index name is
unique per table on MySQL rather than per schema — every InnoDB table's primary
index is called `PRIMARY`, and MariaDB reports a duplicate key as
`for key 'PRIMARY'` with nothing naming the table.

`Dialect()` is **not** `crud.Dialect.Name()`, which answers `"mysql"` for MariaDB
and so cannot tell the two apart. This is the one place in the tree where the
engine is *measured* rather than declared.

Loader scope is intentionally engine-specific:

| Engine | Qualified lookup scope |
|---|---|
| PostgreSQL | every non-system schema on which the loading role has `USAGE`; `pg_table_is_visible` is recorded separately so legacy bare lookup still follows `search_path` |
| MySQL / MariaDB | the current `DATABASE()` only; an exact qualifier for that database works, another database is a miss |
| SQLite | `main` only; attached databases remain valid repository qualifiers, but this catalog does not introspect them |

Consequently a qualified MySQL database other than `DATABASE()` or an attached
SQLite database can be queried by `sqlrepo.DefineInSchema`, but wiring
`probe.Full` for it is refused with `probe.ErrUnknownTable`. There is no bare
fallback and no wrong constraint attribution.

---

## One catalog per physical handle

A catalog is a value the application holds, **never a package-level variable**
([[D-041]]). A global is right in every single-database test and wrong in the
deployment that matters, silently either way.

```go
var cats catalog.Set   // the zero value is ready

main,  err := cats.Load(ctx, mainDB)
events, err := cats.Load(ctx, analyticsDB)

if c, ok := cats.For(mainDB); ok { … }
```

`Set` keys with `crud.SameDataSource`, the same identity rule
`crud.WithExecutorFor` uses — a second implementation of that rule is exactly the
drift this exists to prevent. Entries live in a slice rather than a `map[any]`
because a datasource handle is a pointer *in practice* but nothing in the
contract says it must be, and an uncomparable map key panics at run time.

A handle that cannot be compared is refused with `catalog.ErrUncomparableHandle`
rather than being stored somewhere it can never be found again.

---

## What it holds

| Type | Carries |
|---|---|
| `Table` | name, schema, columns in engine order, primary key in key order, constraints in deterministic order |
| `Column` | name, 1-based position, the engine's own type text, nullable, default, max length, generated |
| `Constraint` | name, table, kind, columns in key order, expressions, prefixes, partial + predicate, `RefTable`/`RefColumns`, `OnDelete`/`OnUpdate`, deferrable |

Kinds: `KindPrimaryKey`, `KindUnique`, `KindUniqueIndex`, `KindForeignKey`,
`KindCheck`.

`KindUnique` and `KindUniqueIndex` are separate because two of the four engines
distinguish them and two do not — the `Kind` records which, rather than
inventing an answer.

Three fields are worth knowing about before you rely on them:

- **`Column.MaxLength` is always 0 on SQLite.** It records `VARCHAR(255)` in
  `Type` and enforces nothing; a catalog answering 255 would claim an
  enforcement that does not exist ([[D-019]]).
- **`Column.Default` is a pointer**, because MySQL reports `DEFAULT ''` as the
  empty string and a plain string cannot tell "no default" from "defaults to
  nothing".
- **`Constraint.Definition` is verbatim engine text and is parsed by nothing.**
  A CHECK is carried as its source and never evaluated: recovering the
  expression from that text is DDL parsing, which [[D-041]] forbids in as many
  words.

`Columns`, `Expressions` and `Prefixes` are **parallel by position** — entry *i*
of each describes key part *i*. A probe reads its results by column position, and
a map here would lose the order that carries the identity.

## Rolling migrations

A constraint name the catalog has never heard of is what a rolling migration
produces. The optional `Reloader` asks for one more look:

```go
if r, ok := cat.(catalog.Reloader); ok {
    _ = r.Reload(ctx, table, name)
}
```

What `Load` returns implements it; a `Catalog` from somewhere else may not, so
ask.

It is deliberately **not** part of `Catalog`: a lookup does no I/O and takes no
context, which is what makes `Load` the thing that fails, so the one call that
does I/O cannot sit on the same interface.

A negative cache with two guards keeps an unknown name from re-introspecting in
a loop: intervals start at 1s and double to 5 minutes, with a 1s floor between
passes. A name that turns up resets it.

## The inbound direction

```go
if r, ok := cat.(catalog.Referrers); ok {
    fks := r.ReferencedBy("orgs")   // which foreign keys point at this table
}
```

No lookup on `Catalog` can express this — a constraint is recorded on the table
that *declares* it. It is what a `restrict` probe needs, and a catalog that is
not a `Referrers` simply produces no restrict terms.
For an exact target, `QualifiedReferrers.ReferencedByRef(crud.TableRef{...})`
keeps foreign keys to same-named tables in different schemas separate.

## What it is not

Not an ORM's schema, not a migration tool, not a DDL parser. It reads what four
engines report and stops there. Every field it holds is either what a
[probe](probe.md) statement needs or what a [fault](sqlfault.md) needs to name a
column — and nothing is added on the grounds that a reader might want it.

## See also

- [probe](probe.md) — the main consumer
- [sqlfault](sqlfault.md) — `sqlfault.FromCatalog(cat)` fills in missing columns
- [[FL-016]] a schema becomes a catalog · [[D-041]] the catalog is per physical handle
