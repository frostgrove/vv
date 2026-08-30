# D-028 — A cursor is the sort tuple of a row, and it is refused unless the sort is unique

**Status:** accepted
**Invariant:** A cursor may only be used with the sort it was made for, and only when that sort ends in the primary key.

## The decision

`crud.After(token)` / `crud.Before(token)` page by position rather than by
offset. The token is base64url of `{"f":[…field names…],"v":[…JSON values…]}` —
the values of the columns the query sorts by, taken from the row at the edge of a
page. `PaginatedResponse` hands one back for each edge (`NextCursor`,
`PrevCursor`), on every paged read whose sort is unique, so a client can move
from offsets to cursors without the server changing.

The comparison is the lexicographic expansion, not SQL's row-value syntax:

```
(a > va) OR (a = va AND b < vb) OR (a = va AND b = vb AND id > vid)
```

Each column is compared in its own direction; `Before` inverts all of them, runs
the statement with the sort reversed so `LIMIT` takes the rows *nearest* the
cursor, and turns the page back over before returning it.

Two refusals, both at query time:

- the field names in the token must equal the resolved sort, in order;
- the resolved sort must contain the primary key.

A third capability rule is checked on both sides: a sort containing a nullable
Go field (`*T`, `utils.Opt[T]`, `database/sql.Null[T]`/legacy Null types, or a
wrapper embedding one) neither emits a cursor nor accepts a manually assembled
one. Offset paging over that sort remains available.

A cursor also skips the `COUNT`: `Total` is the length of the page and
`TotalPages` is zero.

## Why

Offset paging asks the database to walk and discard the rows it skips, and — the
part that actually bites — it asks a question whose answer changes underneath the
reader. "Skip 10, take 10" means something different after somebody inserts a row
above them, so a client walking a list sees one row twice and never sees another.
For an append-heavy table read newest-first, that is not an edge case, it is the
normal case.

**Why the field names travel with the values.** They are positional. Replaying a
token under a different sort would compare whatever happens to line up — a
timestamp against a name — and return a plausible, wrong page. Carrying the names
turns that into a refusal.

**Why the sort must be unique.** "After this row" only names one place if no two
rows share the tuple. A paged read already appends the primary key for the same
reason ([[D-014]] neighbours this: stable pagination). `sqlrepo.UnstablePagination`
removes the tiebreaker, and with it the ability to page by cursor — that is the
trade, stated rather than worked around.

**Why nullable columns are refused before issuance.** `NULL > 'x'` is unknown,
not false, so a boundary on a nullable column silently drops every row that has
one. Sorting by something total is the answer; guessing `NULLS FIRST` semantics
into the comparison would make the page depend on the engine. Most importantly,
the server never hands out a token its next request must reject.

**Why the expansion rather than `(a, b) > (va, vb)`.** Row values say the same
thing in one line, but only when every column sorts the same direction, and MySQL
will not use an index for the mixed case anyway. The expansion is portable and
every engine plans it.

**Why no COUNT.** The number a cursor walk cannot use is the page number, and
`TotalPages` without one is a pager a client cannot drive. Reporting a total
would invite it to render one.

## What it forbids

- Do not accept a cursor whose fields differ from the resolved sort, in any way,
  including order. Reinterpreting is worse than refusing.
- Do not emit a cursor from a sort that has no primary key in it. Handing one out
  is handing out a bug.
- Do not emit a cursor for a nullable sort. Refusing only when that token comes
  back makes an apparently valid pagination link self-invalidating.
- Do not add the cursor's comparison before the sort is final — `DISTINCT`
  rewrites the sort, and the comparison has to match the `ORDER BY` that runs.
- Do not combine a cursor with an offset. `Get` zeroes the offset when a cursor is
  present; honouring both would skip a page past the cursor.
- Do not make the token guessable-but-meaningful and then treat it as untrusted
  input elsewhere. It is opaque to the client and validated on the way in, and it
  carries no authorisation — the scope still applies on top of it.

## Where it lives

- `crud/cursor.go:EncodeCursor` — the token.
- `crud/cursor.go:decodeCursor` — the sort check.
- `crud/cursor.go:CursorPredicate` — the expansion, and the nullable refusal.
- `crud/cursor.go:CursorFieldSupported` — the shared issue/consume capability check.
- `crud/cursor.go:cursorStep` — one column, in its own direction.
- `crud/options.go:After` / `crud/options.go:Before` — the options, and the
  implied `NoTotal`.
- `crud/page.go:PaginatedResponse` — `NextCursor` / `PrevCursor`.
- `crud/sqlrepo/repository.go:cursorWhere` — the primary-key requirement.
- `crud/sqlrepo/repository.go:invertSort` — the backward read.
- `crud/sqlrepo/repository.go:setCursors` — the edges of a page.
- `crud/query/request.go:Request` — `after` / `before` on the wire.

## Proven by

- `TestACursorWalkIsNotDisturbedByAConcurrentInsert` in
  `test/integration/cursor_test.go` — the whole reason the feature exists, with
  the offset walk in the same test asserting that it *does* repeat a row, so the
  comparison is not vacuous.
- `TestACursorWalkVisitsEveryRowOnce` in `test/integration/cursor_test.go`.
- `TestPagingBackwardsReturnsTheRowsNearestTheCursor` in
  `test/integration/cursor_test.go` — the inversion.
- `TestACursorFollowsEachColumnsOwnDirection` in `test/integration/cursor_test.go`
  — a mixed sort, which is what a row-value comparison gets wrong.
- `TestACursorIsRefusedUnderADifferentSort` in `test/integration/cursor_test.go`.
- `TestACursorWalkOverTheWireDSL` in `test/integration/cursor_test.go` — both
  doors, and `totalPages` reported as zero.
- `TestCursorCapabilityRefusesEveryKnownNullableShape` and
  `TestANullableSortNeverAdvertisesACursorItsNextRequestWouldRefuse` — nullable
  tokens are refused before they can become links.

## See also

[[FL-001]] [[UC-002]] [[D-003]] [[D-014]]
