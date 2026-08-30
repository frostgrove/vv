# crudtest — unit-test a repository with no database

```go
import "github.com/frostgrove/vv/crud/crudtest"
```

**Module:** root · **Depends on:** `crud`, and the standard library

A `crud.Source` that records the SQL a repository produces and replays canned
rows back at it. Statement shape, bind order, pagination arithmetic and decorator
composition — all checkable without Docker ([[UC-011]]).

The whole unit suite in this repository is built on it. The SQL is deterministic
by design ([[D-014]]), which is what makes this work at all.

---

## Asserting on the statement

```go
rec := crudtest.Postgres().Push(crudtest.Rows(
    []any{int64(1), int64(7), "ann@x.io", "Ann", 31, true, time.Now()},
))

u, err := Users.Bind(rec).GetByID(ctx, 1)

rec.Last().SQL   // SELECT "id", … FROM "users" WHERE "id" = $1 LIMIT 1
rec.Last().Args  // []any{int64(1)}
```

```go
crudtest.Postgres()          // *Recorder on crud.Postgres{}
crudtest.MySQL()             // …on crud.MySQL{}
crudtest.New(crud.SQLite{})  // …on any dialect
```

## Feeding it answers

```go
rec.Push(crudtest.Rows(
    []any{int64(1), "first"},
    []any{int64(2), "second"},
))
```

Values are assigned to scan destinations **positionally**, using the same
checked conversions as `database/sql.Rows.Scan` and honouring `sql.Scanner`.
That means overflow and signed/unsigned mistakes are refused and an integer
scanned into a string becomes decimal text, never a Go rune conversion. Queue
as many results as the call will make —
a paginated read is two statements, and a preload is one more per relation.

| | |
|---|---|
| `Push(results...)` | queue query answers |
| `ExecResult(res)` | the next `Exec`'s rows-affected and last-insert-id |
| `Fail(err)` | the next `Exec` reports `err` |
| `Rows(rows...)` | a successful result |
| `RowsFailing(err, rows...)` | rows, and *then* an error |

**`RowsFailing` is not a duplicate of a refused query.** The drivers this
doubles for report one failure in two places: `crudtest.Result{Err: err}` is
`Query` itself refusing, which is `database/sql`'s shape, and `RowsFailing` is
pgx's — a statement the server
refused arrives as a live `Rows` that yields what it has and *then* answers
`Err`. A double that could only express the first cannot drive the arm a read
has to end with, and a loop that never asks reads a truncated schema as a
complete one.

## Reading it back

| | |
|---|---|
| `Last()` | the most recent `Statement` |
| `Statements()` | every one, in order |
| `SQL()` | just the strings |
| `TxDepth()` | how deep the transaction nesting went |
| `Reset()` | clear everything |
| `Normalize(s)` | collapse whitespace, so a test compares statements without caring about formatting |

```go
if got := crudtest.Normalize(rec.Last().SQL); got != want {
    t.Fatalf("the update wrote the wrong columns:\n%s", got)
}
```

## Transactions

`Recorder` implements `crud.Beginner`, so `Tx` and `InTx` work and the nesting is
counted rather than executed. That is enough to assert that a decorator opened
one, or that a nested call joined rather than nested.

## Identity

A recorder implements `crud.Identified` and returns itself. `crud.Session`,
`crud.WithExecutorFor` and `catalog.Set` therefore treat one recorder as one
datasource; an owned transaction cannot leak into a second recorder ([[D-041]],
[[D-082]]).

## What it will not tell you

It doubles a driver, not a database. It cannot tell you whether the SQL is
*valid*, whether a constraint fires, or whether a dialect accepts a construct.
That is what `make integration` is for — and the two are deliberately different
jobs: this one pins the statement the library builds, and the integration suite
pins what four engines do with it.

## See also

- [crud](crud.md) — the `Source` interface this satisfies
- [sqlrepo](sqlrepo.md) — the repository whose statements you are pinning
- [[UC-011]] test repository behaviour without a database · [[D-014]] the SQL is deterministic · [[D-020]] tests are the specification
