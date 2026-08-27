# D-016 — One published module; package `crud` stays stdlib-only

**Status:** superseded by [[D-033]] — the module half only. The stdlib rule stands.
**Invariant:** `go get github.com/frostgrove/vv` must be the whole installation — no `replace` directive — and no file in package `crud` outside `_test.go` may import anything but the standard library.

> **What changed.** The single-module half of this decision was reversed: an
> optional dependency now lives in its own module under the same repository, so
> a consumer takes only the bindings and adapters it imports. The reasoning that
> was wrong is the paragraph below headed *What was accepted in exchange* — the
> cost it prices as "paid only by consumers who pin one of two libraries" is in
> fact one MVS floor per optional dependency, paid by everybody. [[D-033]] has
> the new arrangement and answers the `replace` problem this file raises.
>
> **The second half is untouched and still binding:** no file in package `crud`
> outside `_test.go` may import anything but the standard library. [[D-033]]
> widens it to the whole root module rather than relaxing it.
>
> **The scope of that half was clarified at [[D-058]], not changed.** This was
> written when `crud/` held exactly one package, so "no file in `crud/`" and "no
> file in package `crud`" were the same sentence. The layout move put `sqlrepo`,
> `query`, `catalog`, `probe`, `sqlfault`, the decorators, the adapters and the
> bindings underneath it, and every one of them is allowed the dependencies this
> rule forbids — `crudpgx` imports pgx, that is what it is for. The rule is the
> package. `scripts/checks.sh:TIER0_STDLIB` lists `./crud`, never `./crud/...`, and that
> is where the reading is enforced rather than merely stated.

## The decision

One published module at the repository root. `crud/http/crudfiber` and
`crud/adapter/crudpgx` are packages inside it, not modules. `crud/adapter/crudpgx/go.mod`
and `crud/http/crudfiber/go.mod` were deleted to get here.

There is a second `go.mod` in the tree — `test/go.mod` — and it is deliberate and
different: the integration tests need ent, gorm, sqlx, sqlc, two drivers and
Docker, and none of that may become a dependency of the library. It is not
published, it uses a `replace` to reach the library from disk, and `go.work`
joins the two for local development.

Package `crud` itself imports only the standard library. Verified: within the
package, the only non-stdlib imports are in `_test.go` files. (The subtree below
it is a different question and a different answer — see the note above.)

## Why

**Why one module.** Three modules meant three `go get` lines and, before a tag
exists for each, a `replace` directive per consumer. A `replace` is not an
installation — it does not survive `go mod tidy` in a downstream consumer of
*your* module, it is invisible to `go get -u`, and it makes "which version am I
on" unanswerable. Three modules also means three tags per release and a window
after every release where a consumer can select an incoherent combination.

**What was accepted in exchange.** The root `go.mod` requires
`github.com/gofiber/fiber/v3` and `github.com/jackc/pgx/v5` whether or not a
consumer imports them. Module graph pruning keeps them out of a consumer's own
`go.mod` when they do not import them, but they still take part in minimal
version selection: a consumer pinning an older Fiber v3 will have MVS raise it
to the version vv asks for. That is a real cost and it is stated in the
README and in both usage guides, at the point where the reader is about to run
`go get`.

The trade was taken because the alternative cost is paid by every consumer on
every install, and this one is paid only by consumers who pin one of two
libraries.

**Why package `crud` is stdlib-only.** It is the package every other package depends on
and the one a third-party adapter has to import. If it grew a dependency, every
adapter and every consumer would inherit it, and the "only two things cross the
boundary — run this statement and give me rows" claim would stop being true at
the module level even while it stayed true at the API level. It is also what
makes `crud/adapter/crudsql`'s SQLSTATE classifier ask by shape rather than by type
([[D-015]]): it cannot name a driver's error type, so it reflects for a method
and a field instead.

## What it forbids

- Do not add a `go.mod` under `crud/adapter/`, `crud/http/` or anywhere else
  that would be published. If a dependency is heavy enough to want its own
  module, that is a new decision with its own file, and it has to answer the
  `replace` problem. (Superseded by [[D-033]], which says how to do it; the
  paragraph is kept as written.)
- Do not import anything outside the standard library from a non-test file in
  **package** `crud`. Not a UUID package, not a decimal package, not a logging
  package. Packages below `crud/` are not covered — [[D-058]].
- Do not move a driver, an ORM or a test helper into the root `go.mod` to make
  an integration test compile. That is what `test/go.mod` is for.
- Do not delete `test/go.mod` to "simplify". Its whole job is to keep ent, gorm,
  sqlx and two drivers out of the library's dependency graph.
- Do not raise the fiber or pgx requirement casually. Every bump is an MVS floor
  for every consumer, including ones who import neither.

## Where it lives

- `go.mod` — module `github.com/frostgrove/vv`, `go 1.26`, two direct
  requirements: `fiber/v3` and `pgx/v5`.
- `test/go.mod` — the unpublished test module, with the comment explaining why
  it exists and the `replace` to `../`.
- `go.work` — joins `.` and `./test`.
- `crud/executor.go` — the package doc that states the stdlib-only rule and the
  "only Exec and Query cross the boundary" principle it serves.
- `crud/sqlfault/extract.go:sqlState` — the visible consequence: a driver's SQLSTATE
  is reached by shape rather than by naming its type. It moved out of
  `crud/adapter/crudsql` at phase 3 and the rule did not: `crudsql` still imports no
  driver, and `make check-deps` is what proves it.
- `Makefile` — `tidy` tidies both modules; `unit` runs the library's tests with
  no database, `integration` runs the test module with `-tags=integration`.
- `README.md` — "Install", which states the trade.

## Proven by

Nothing in the test suite asserts the module layout — `go build` is the check,
and it is not a test that can be pointed at.

The stdlib-only rule is checkable and worth checking:

```
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./crud \
  | grep -v '^github.com/frostgrove/vv/crud$'
```

should print nothing. It is part of `make check-deps`, which closes the gap this
paragraph used to describe as open — for a *third-party* import. A first-party
one is invisible to it, because it filters on the module path prefix.

`scripts/checks.sh:TIER0_STDLIB`, checked by `make check-tiers`, is the arm that catches
that half, and it is the one the rule needs: `errs/doc.go` states that `crud` may
not import `errs` at all, since a library-origin error with two classification
paths would have them disagree. Verified by adding
`_ "github.com/frostgrove/vv/errs"` to `crud/errors.go` and watching the arm name
it, while `go build ./...` and `make check-deps` both stayed green — the manifest
arm above it filters every contract package out of its own result, so `errs` is
exactly what it cannot see.

The command given here before ended in `grep '\.'` and so matched
standard-library paths — it printed a page of them on a clean tree and could
never have failed. [[D-033]] carried the same defect.

Indirectly relevant: `scripts/modules.sh:unit` runs `go test ./...` with no database and no
Docker, which fails the moment the library picks up something the test module was
holding.

## See also

[[D-015]] [[D-009]] [[D-018]] [[D-020]] [[D-058]]
