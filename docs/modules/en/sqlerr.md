# errs/sqlerr — a driver error becomes a code

```go
import "github.com/frostgrove/vv/errs/sqlerr"
```

**Module:** root · **Depends on:** `errs`, and the standard library. **No driver
imports, no database**

Pure functions, one table per dialect. Hand it what a driver said and it answers
what the failure *was* — `unique`, `foreign_key`, `required` — plus which table
and columns, where the engine bothered to say.

**You rarely call this directly.** [sqlfault](sqlfault.md) calls it for you, and
`crudsql.WithFaults` / `crudpgx.WithFaults` wire that in. Read this page to know
what your engine can and cannot tell you.

---

## The seam

```go
code, source, ok := sqlerr.Classify("postgres", &sqlerr.Err{
    SQLState: "23505",
    Fields:   map[string]string{"ConstraintName": "users_email_key", "TableName": "users"},
})
// code == errs.CodeUnique, source.Constraint == "users_email_key", ok == true
```

`dialect` is one of `"postgres"`, `"mysql"`, `"mariadb"`, `"sqlite"`.

**The third result being false means "no arm matched".** A caller must not turn
that into a code of its own: an unclassified error is a 500, and a guess is worse
than silence.

It is total — an unknown dialect and a nil error both answer false rather than
panicking.

## Three keys, because three of four engines break the obvious one

The classifier is keyed on `(dialect, sqlstate, native)` and **no arm of it is a
SQLSTATE-class test** ([[D-046]]).

| Engine | Keyed on |
|---|---|
| PostgreSQL | the whole SQLSTATE — `23505` |
| MySQL | the state **and** the number together — `1062` / `23000` |
| MariaDB | the same, and it is a **different table** from MySQL |
| SQLite | the number read as bytes; **there is no SQLSTATE at all** |

Why not the class:

- **MySQL** answers a failed CHECK with `3819 / HY000` and a missing default with
  `1364 / HY000`. Neither starts with `23`.
- **MariaDB** answers the same CHECK with `4025 / 23000` — inside class 23. The
  identical constraint on two engines sharing a driver, a dialect and a wire
  protocol needs two different arms.
- **SQLite reports no SQLSTATE at all.** For a quarter of the supported engines
  the class gate was simply absent — every SQLite constraint violation was an
  unclassified 500 until the corpus found it.
- **The same number can carry different classes.** MySQL's `1366` is `HY000` and
  MariaDB's is `22007`.

## What no arm may read

`Err.Message`, `Fields["Detail"]`, `Fields["Hint"]` and `Err.Type`.

PostgreSQL, MySQL and MariaDB all localise the first three through the session's
language setting, so a parser reading any of them classifies differently
depending on where the server was deployed ([[D-039]]). The corpus carries a
duplicate key captured from a server answering **in Russian** so that this is a
test rather than a promise.

---

## What each engine actually tells you

**Measured, not remembered.** Every cell was provoked against a live server and
is checked in under `errs/sqlerr/testdata/corpus/`: PostgreSQL 17, MySQL 8.4,
MariaDB 11.4, SQLite 3.53.

| Violation | PostgreSQL | MySQL | MariaDB | SQLite |
|---|---|---|---|---|
| unique | `23505`; constraint, table, schema | `1062`/`23000`; index name in the message | `1062`/`23000`; index name not table-prefixed | ext `2067`; names `table.column`, no constraint |
| primary key | `23505` on the pk index | `1062` on `PRIMARY` — the name on *every* InnoDB table | same | ext `1555` |
| FK, parent missing | `23503` | `1452`/`23000` | `1452` | ext `787`, **no detail at all** |
| FK, child referencing | `23503` — **not** `23001`; `TableName` is the *child* | `1451`/`23000` | `1451` | ext `787`, no detail |
| NOT NULL | `23502`; column and table | `1048`/`23000` | same | ext `1299`; names `table.column` |
| missing default | collapses into `23502` | **`1364`/`HY000`** — a distinct condition | same | collapses into ext `1299` |
| CHECK | `23514`; constraint | **`3819`/`HY000`** | **`4025`/`23000`** | ext `275`; unnamed → the expression source |
| exclusion | `23P01` — **no arm yet**, see the roadmap | — | — | — |
| too long | `22001` — **no column, no table** | `1406`/`22001`; column and row | same | **not enforced** |
| out of range | `22003` | `1264`/`22003` | same | **not enforced** |
| bad type syntax | `22P02` | `1366`/`HY000` | **`1366`/`22007`** | **not enforced** |
| deadlock | `40P01` | `1213`/`40001` | same | `5` — one writer, so no cycle |
| serialisation failure | `40001` | `1213`/`40001` — a lock cycle to InnoDB | same | `5` |
| lock timeout | `55P03` | `1205`/`HY000` | same | `5` |
| transaction aborted | `25P02` | n/a — statement-level rollback | n/a | n/a |

### Five consequences worth knowing before you design around it

1. **SQLite enforces neither width, nor range, nor declared type.** A
   `VARCHAR(8)` stores 27 characters, 99999 goes into a small column, and `'abc'`
   stays text in an INTEGER column. The same payload is 422 on the two servers
   and 200 on SQLite ([[D-019]]).
2. **SQLite's foreign-key error carries nothing** — `FOREIGN KEY constraint
   failed`, and that is the whole message. Only the [catalog](catalog.md) can say
   which key.
3. **PostgreSQL cannot tell the two foreign-key directions apart** from
   structured fields. Both are `23503` with the same constraint name, and for the
   child-referencing direction `TableName` is the **child** — a table the request
   never mentioned. So `restrict` is reported as `foreign_key` on PostgreSQL and
   SQLite, and stays `restrict` on MySQL and MariaDB.
4. **Even the value is untrustworthy.** MySQL's `1062` joins composite key values
   with `-`, so `('x-1','y')` reports `'x-1-y'`. On a prefix index the reported
   value is the *truncated prefix*, not what was sent.
5. **On MySQL, every structural fact beyond the number comes from the catalog.**
   `mysql.MySQLError` has `Number`, `SQLState` and `Message`, and nothing else.

---

## The corpus

`errs/sqlerr/testdata/corpus/{postgres,mysql,mariadb,sqlite}.json` — 20 cases per
engine, each one **provoked against a live server** and recorded verbatim, with
the statement that provoked it.

```json
{
  "name": "unique",
  "kind": "integrity",
  "want": "unique",
  "stmt": "INSERT INTO cx_users (email) VALUES ('taken@example.com')",
  "err": { "type": "*pgconn.PgError", "sqlstate": "23505", "native": 0,
           "message": "duplicate key value violates unique constraint …",
           "fields": { "ConstraintName": "cx_users_email_key" } }
}
```

The parsers are written against these entries and then run on them, so **nothing
separates the thing tested from the thing shipped**. A case with `"want": ""`
must stay unclassified — an undefined table, an access denial, a connection that
never reached a server. A case with `"unreachable"` records that this engine will
not produce that violation at all, and why.

`make corpus` recaptures. `sqlerr.Load(dir, engine)`, `Save`, `Path` and
`Corpus.Case(name)` read and write it.

The table this replaced was written from documentation and **half of it was
wrong**. Its first run found a live bug that had been shipping for as long as
SQLite had been supported.

## See also

- [sqlfault](sqlfault.md) — extraction, the integrity gate, and fault assembly
- [errs](errs.md) — the `Code` and `Kind` vocabulary this fills
- [[D-046]] the classifier is keyed on dialect, sqlstate, native
- [[D-039]] message text is not an interface · [[D-019]] dialect differences are enumerated
- [[FL-014]] a driver error becomes a public violation
