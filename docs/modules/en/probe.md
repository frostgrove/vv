# probe — every violation, not just the first

```go
import "github.com/frostgrove/vv/crud/probe"
```

**Module:** root · **Depends on:** `crud`, `errs`, `catalog`

A database reports one violation at a time: the first constraint it reaches ends
the statement. The client fixes it, posts again, and is told about the next one.
`probe.Full` issues **one extra statement** — one boolean column per constraint
the write could have broken — and reports the rest.

You do not call this directly. [faults](faults.md) wires it into the repository.

---

## What it turns a response into

Without it, a form with three problems is three round trips:

```json
{ "errors": { "validation": [ {"field": ["email"], "error_code": "unique"} ] } }
```

With it, one:

```json
{ "errors": { "validation": [
  {"field": ["email"],  "error_code": "unique"},
  {"field": ["org_id"], "error_code": "foreign_key"},
  {"field": ["code"],   "error_code": "restrict"}
] } }
```

## Wiring it

```go
cat, err := catalog.Load(ctx, db)
if err != nil { log.Fatal(err) }

docs := Docs.Bind(db, faults.Enrich[Doc, int64](
    faults.WithProbe(probe.Full(cat)),
    faults.WithProbeError(func(op string, err error) {
        log.Printf("the %s probe failed: %v", op, err)   // advisory. Log it, never render it
    }),
))
```

`probe.Simple()` is the other handler: it wraps and returns, issues no statement
and finds nothing. That is the honest answer wherever a second statement is not
free.

---

## Two rules a signature cannot carry

Both are [[D-042]], and both matter if you build on this.

- **The fault is never nil when the request carried one.** A handler that found
  nothing, hit a cap or failed outright still hands back what the driver said.
  Suppressing it would turn a truthful 409 into silence.
- **The error is advisory.** It is for the log. A caller that turned it into a
  response would let the most failure-prone part of the design downgrade a
  correct 409 into an opaque 500 — and it *is* failure-prone, because it re-binds
  values from a statement that already failed.

**The index is the truth and the probe is advice.** Nothing is ever invented.

---

## What it probes

Three codes, and no others.

| Code | How |
|---|---|
| `unique` | `EXISTS` over this table, one term per unique key the write's own columns touch. An update excludes its own row |
| `foreign_key` | `NOT EXISTS` over the parent, guarded by every referencing column being non-null |
| `restrict` | the **inbound** direction: an update changing a column another table's FK points at, under `ON UPDATE RESTRICT`/`NO ACTION`, with children still pointing at the old value |

`restrict` needs the inbound half of the schema, which no lookup on
`catalog.Catalog` can express — so a catalog that is not a `catalog.Referrers`
simply produces no restrict terms.

### And one check with no statement at all

**Intra-payload duplicates**: two rows of the same insert carrying the same
email. The database reports one, both are wrong, and finding them takes a map.
It is unambiguously correct because it is a fact about the payload rather than
about the database — and it is only ever narrowing, since a collation equates
*more* values than byte equality does, never fewer.

## What it refuses to probe, and why

- **CHECK constraints are not evaluated.** The catalog carries a CHECK only as
  engine text, and the shape differs per engine —
  `CHECK ((qty > 0))` from PostgreSQL, the bare clause from
  `information_schema`, and nothing at all from SQLite. Recovering the expression
  from that text is DDL parsing, which [[D-041]] forbids in as many words.
- **NOT NULL, length, range and enum membership are not checked** — not in Go and
  not in SQL. MySQL makes the argument: under `STRICT_TRANS_TABLES` a too-long
  value is an error, and without it the same value is a warning and a silent
  truncation. Any rule re-derived here would be right on one deployment and
  wrong on another it cannot see.
- **There is no pre-flight mode.** Probing *before* the write costs a query on
  every happy-path request, and the TOCTOU window between the check and the
  insert makes a clean answer a lie under concurrency.

Every gap in any of those is a chance to report a violation the server would not
have raised, which is the one direction [[D-042]] rules out.

---

## The transaction matrix

PostgreSQL aborts the whole transaction on a constraint error, and nothing runs
until `ROLLBACK` or `ROLLBACK TO SAVEPOINT`.

| the write ran… | PostgreSQL (poisons) | MySQL · MariaDB · SQLite |
|---|---|---|
| outside any transaction | `Full` | `Full` |
| inside a transaction **vv opened** | `Simple`, unless `WithSavepoints()` | `Full` |
| inside a **foreign** transaction | `Simple`, always | `Full` |

A foreign transaction is **never** given a savepoint. An ent or gorm transaction
has its own savepoint stack and its own expectations, and
`ROLLBACK TO SAVEPOINT` in the middle of somebody else's unit of work can discard
work its owner has not finished with.

Which side of that table a dialect is on comes from `crud.StatementRollback`, not
from its name.

## Options

| Option | Default | Does |
|---|---|---|
| `WithSavepoints()` | off | take a savepoint before a write inside a vv-owned transaction, so a poisoning engine can still probe |
| `WithMaxConstraints(n)` | 16 | terms per request |
| `WithMaxRows(n)` | 50 | rows per batch |
| `WithMaxSavepoints(n)` | 32 | per transaction |
| `WithTimeout(d)` | 250ms | around the probe statement only |
| `Skip(names...)` | — | take constraints out by name. Refused at start-up if a name matches nothing |
| `WithScope(fn)` | — | narrow the unique terms with the security policy's own predicate |
| `WithValues()` | off | put the offending value in the payload |
| `CodeOnly()` | off | drop the path, keep the code |

### The caps, with the reasons

A cap without a number is not a cap.

- **16 constraints.** Relevance already narrows by written columns; sixteen
  *relevant* constraints on one write is already pathological.
- **50 rows.** A batch probe is one flat statement of one term per constraint per
  row, so 50 × 16 is 800 columns of one result row — inside PostgreSQL's
  1664-column limit with room to spare, and it bounds a hostile 10 000-row batch
  to a fixed cost.
- **250ms.** The write has already failed and the client is waiting.
- **32 savepoints.** PostgreSQL's subxid cache overflows at 64 per top-level
  transaction, and the overflow forces `pg_subtrans` lookups on **every reader in
  the cluster**. Half of the cliff.

Hitting any of them sets `Partial` on the fault, and the envelope renders
`"partial": true`. A partial answer presented as complete is worse than the one
violation it replaced.

---

## The oracle, and the four controls

The probe queries rows the caller may not be allowed to see, and a
unique-violation response reveals that a value exists. Four controls, **none of
which closes it**:

1. the value never reaches the payload unless `WithValues()` says so;
2. `Skip(name)` takes a constraint out entirely;
3. `WithScope(fn)` narrows the unique terms with the same predicate the security
   policy narrows reads with;
4. `CodeOnly()` drops the path and keeps the code.

`WithScope` narrows the **unique** terms and nothing else, and that limit is not
an oversight: a foreign-key term reads the parent table and a restrict term reads
the child, the model's own scope predicate names neither, and a predicate over
the wrong table would not compile. Where that matters, `Skip` is the control.

## Fails at start-up, not at request time

`probe.Full` reads the catalog and **never the database's schema at request
time**, so it refuses at declaration:

| Error | Means |
|---|---|
| `ErrUnknownTable` | the catalog does not know this table |
| `ErrKeyDoesNotIdentify` | the primary-key column is not a row identity on its own |
| `ErrUnknownConstraint` | a `Skip` names no constraint |

`ErrKeyDoesNotIdentify` is the composite-key refusal. `crud.Schema` has one `PK`
field, so a composite key is not declarable — the reachable harm is a model whose
single `pk` field is mapped onto a table whose *real* key is composite, where the
repository's own `WHERE pk = ?` is already wrong. The declaration refuses unless
the catalog confirms the key column identifies a row on its own.

## Determinism

Terms are produced in catalog order, rows in payload order, and the answer is
sorted with `errs.SortViolations` before it is returned. The same failing request
twice produces the same list ([[D-014]], one layer up).

## See also

- [faults](faults.md) — the decorator that wires this in
- [catalog](catalog.md) — required, and it is what makes start-up refusal possible
- [security](security.md) — where `WithScope`'s predicate comes from
- [[UC-017]] every error for one payload at once · [[FL-017]] a failed write becomes every violation
- [[D-042]] the probe is advisory
