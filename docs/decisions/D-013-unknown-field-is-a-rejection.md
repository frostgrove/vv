# D-013 — An unknown field is a rejection, never a silently ignored clause

**Status:** accepted
**Invariant:** Every field path in a filter, sort, select, search or preload must resolve against the model, or the whole request fails before any SQL is sent.

## The decision

`Request.Compile` resolves every path through `Meta.FieldAt` / `Meta.RelationAt`
and returns a `*query.Error` naming the path on the first failure. Nothing
partial is emitted: a rejected document compiles to zero options. The
error carries the path and the reason and is safe to hand back verbatim, so the
client sees `filter.nope: unknown field "nope" on model User` with a 400.

The same rule holds one layer down. `writer.leaf` records an
`UnknownFieldError` on the builder and renders `1 = 0` rather than emitting a
bare name, and `SQL.Done` returns the error, so a predicate constructed in Go
against a field that does not exist also fails the statement rather than
producing one.

It also holds one layer *up*, which it did not at first. The rule started at the
paths inside the document and left the document's own keys to `encoding/json`,
which ignores what it does not recognise. `{"filtr": {…}}` therefore parsed into
a `Request` with no filter and answered 200 with the whole table — the exact
failure this decision exists to prevent, one level above where it was being
enforced. `Request.UnmarshalJSON` now decodes with `DisallowUnknownFields` and
turns the refusal into a `*query.Error` naming the key.

The query string cannot be closed the same way: a handler is free to read its own
parameters off the same URL, and `?includeArchived=1` driving a `WithScope`
option is the documented pattern. So an unknown parameter passes, and only one
that is a single typo — insertion, deletion, substitution or a transposition of
adjacent characters — away from one of ours is refused. `?filtr=` is not
somebody's parameter; it is ours, misspelled. Names shorter than four characters
are skipped, because `q` and `f` are one edit from most single letters.

## Why

A filter that is dropped when it does not resolve returns the whole table. That
is the failure mode, and it is silent in every direction:

- the status is 200;
- the response body is well-formed;
- there is more data than expected, which looks like a data problem, not a query
  problem;
- under a tenancy scope the *scope* still applies, so it looks plausible.

A typo in a client's filter name — `createdAT`, `owner_id` on a model with
`ownerID`, a field renamed in a refactor — becomes a data leak or a 30-second
query, discovered by whoever notices the row count. Rejecting makes it a
five-second fix at the call site.

The permissive alternative is usually chosen for forward compatibility: an older
server should tolerate a newer client's field. That trade is available and was
not taken, because "tolerate" here means "answer a different question than the
one asked" — and the version-skew problem is better solved by the allow-lists in
`query.Config`, which reject with a *reason*.

**Why the field resolver is forgiving about spelling but not about existence.**
`Schema.Field` matches by Go name, then column name, then case- and
separator-insensitively, so `createdAt`, `created_at` and `CreatedAt` are one
column and a TypeScript client keeps its own conventions. An alias that would be
ambiguous between two fields is registered as ambiguous and resolves to nothing —
picking a winner would silently filter the wrong column.

**Why the whole document is rejected rather than the bad clause.** A partially
applied filter is a filter nobody wrote.

**Why the message is safe to echo.** It names a path from the request and a model
name. It never carries a SQL string or a driver error — those go to 500 with an
empty body ([[D-015]]).

## What it forbids

- Do not skip an unresolvable clause, log it, or downgrade it to a warning.
- Do not decode `Request` with a plain `json.Unmarshal` into a copy that bypasses
  `Request.UnmarshalJSON`. The strictness lives in that method; a `type alias` or
  a hand-rolled decode elsewhere puts the hole straight back.
- Do not widen the query-string check into "reject every unknown parameter". The
  handler shares that URL with the application, and `WithScope` reads it.
- Do not let a rejected document emit the options it had already accumulated.
- Do not resolve an ambiguous folded alias by picking the first match.
- Do not render an unresolved name into SQL "so the database can report it". The
  database's error is a 500 with no detail, and it may be a syntax error rather
  than an unknown column.
- Do not make the allow-lists silently drop a denied path. A denied path is a
  named refusal too — `Name is not sortable`, not a missing `ORDER BY`.
- Do not let a preload's sub-filter or sub-sort skip the allow-list. It used to:
  a column the config never named was sortable through a relation, and a grant on
  the root's own `Body` silently authorised every preloaded relation's `Body`.
  `compiler.prefix` is what makes an allow-list entry spelled from the root
  (`Comments.Body`) mean the preload route and not the root one.

## Where it lives

- `query/request.go:Request.UnmarshalJSON` — the document's own keys, refused
  with `DisallowUnknownFields` before any path is looked at.
- `query/querystring.go:checkParams` / `query/querystring.go:isOneTypoAway` — the
  query string's narrow half: a near-miss of one of ours is a typo, anything else
  belongs to the application.
- `query/compile.go:Request.Compile` — every path, resolved before any option is
  emitted.
- `query/compile.go:compiler.path` — resolution plus the depth bound.
- `query/compile.go:allowed` — the allow-lists, with `*` and `Comments.*`.
- `query/compile.go:compiler.preloadOpts` — sub-filter and sub-sort compile
  against the *target* model but keep the root's allow-lists, qualified by
  `compiler.prefix`.
- `query/compile.go:Error` — path plus reason, safe to echo.
- `query/filter.go:compiler.condition` / `query/filter.go:compiler.operator` —
  an unknown operator is refused too, and a `null` operand is refused where it
  has no meaning (`{"contains": null}` used to become `LIKE '%%'`, and
  `{"notIn": null}` `NOT IN ()` — a narrowing the client asked for turning into
  no narrowing at all).
- `crud/meta.go:Schema.Field` — the forgiving lookup and the ambiguity guard.
- `crud/predicate.go:writer.leaf` — the Go-side half: record the error, render
  `1 = 0`, never a bare name.
- `crud/render.go:SQL.Done` — surfaces the first resolution failure.
- `http/crudfiber/options.go:Status` — `*query.Error`, `*crud.UnknownFieldError`
  and `*crud.SchemaError` all map to 400.

## Proven by

- `TestAMisspelledDocumentKeyIsRefused` in `query/strict_test.go` — the document's
  own keys, with `TestEveryDocumentKeyStillParses` as the control that the
  strictness did not simply reject everything, and
  `TestTheOfferedKeyListMatchesTheStruct` so the message cannot drift from the
  struct.
- `TestAMisspelledQueryParameterIsRefused` in `query/strict_test.go`, and its
  control `TestAnApplicationsOwnParametersArePassedThrough` — the rule is narrow
  on purpose, and without the second test the first could be satisfied by
  rejecting every parameter the handler does not own.
- `TestAMisspelledQueryKeyIs400` / `TestAMisspelledQueryParameterIs400` in
  `http/crudfiber/write_edge_test.go` — the refusal survives Fiber's binding and
  no repository call is made.
- `TestUnknownFieldNeverReachesTheDatabase` in `query/query_test.go`.
- `TestARejectedDocumentCompilesToNoOptions` in `query/hostile_test.go` — the
  "no partial output" half.
- `TestEveryRejectionNamesThePathThatWasWrong` in `query/edge_test.go`.
- `TestAQueryThatNamesSomethingTheModelLacksIsABadRequest` in
  `http/crudfiber/edge_test.go` — the status and the body.
- `TestAPredicateOnAnUnknownFieldBindsNothing` in `crud/edge_test.go` and
  `TestUnknownFieldIsReportedNotRendered` in `crud/render_test.go` — the Go-side
  half.
- `TestUnknownFieldIsAnError` in `repo/basic/repository_test.go`.
- `TestAnAmbiguousAliasResolvesToNothing` in `crud/schema_edge_test.go`.
- `TestADeniedColumnStaysDeniedHoweverItIsSpelled` in `query/hostile_test.go` —
  the folded-spelling attack on an allow-list.
- `TestClientSpellingNeverReachesTheStatement` in `query/edge_test.go`.
- `TestAPreloadSubFilterCostsTheSamePermissionAsTheFilterPath` and
  `TestAPreloadSortObeysTheSortableList` in `query/preload_allowlist_test.go`.
- `TestNullOperandsAreRefusedWhereTheyHaveNoMeaning` in `query/edge_test.go`.
- `TestUnknownOperatorIsRefusedOnBothDoors` in `query/querystring_test.go`.
- `TestAMalformedFilterParameterIsRefusedNotDropped` in `query/edge_test.go` —
  the query-string door, where "dropped" is the easy mistake.

## See also

[[D-003]] [[D-004]] [[D-015]] [[D-014]]
