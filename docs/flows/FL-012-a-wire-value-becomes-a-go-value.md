# FL-012 — A wire value becomes a Go value

**Entry point:** `query/coerce.go:decodeValue` (JSON) and `query/coerce.go:coerceString` (query string)
**Implements:** [[UC-002]] [[UC-006]] · **Governed by:** [[D-013]] [[D-019]] [[D-003]]

Two front doors, one meaning. A value arrives as a JSON token or as text, and
has to end up as the Go type of the column it will be compared against — before
it is bound, because a `float64` where the column is `bigint` is a query the
planner may refuse to index.

## The JSON door

1. **`compiler.condition`** — `query/filter.go:119`
   Three shapes, decided by the first byte after trimming:
   - `{` → an operator object → `compiler.operators` (`filter.go:162`)
   - `[` → `decodeList` → `crud.In` (`filter.go:145`)
   - anything else → `null` becomes `crud.IsNull`, otherwise `decodeValue` →
     `crud.Eq` (`filter.go:150-157`)

2. **`compiler.operator`** — `query/filter.go:194`
   `normalizeOp` first, then the null guard (`filter.go:204`):
   ```go
   if isNull(trim(raw)) && (kind.unary() || kind.textual() || kind.multi()) {
       return nil, errf(where, "%s has no meaning with null", op)
   }
   ```
   `json.Unmarshal` reads `null` as "leave the destination alone", so a null
   operand used to arrive as the zero value of whatever the operator wanted:
   `{"contains": null}` became `LIKE '%%'` and `{"notIn": null}` became
   `NOT IN ()` — a narrowing the client asked for turning into no narrowing at
   all. Only a scalar comparison has an answer for null, and `crud.Eq` folds
   that to `IS NULL`.
   Then by class: unary reads a bool, textual reads a string **uncoerced** (a
   LIKE pattern is text, whatever the column is), multi goes through
   `decodeList`, everything else through `decodeValue`.

3. **`decodeValue`** — `query/coerce.go:19`
   `t := crud.ElemType(f.Type)` (`crud/meta.go:517`) — `Opt[int]` and `*int` both
   report `int` — then `json.Unmarshal` into a `reflect.New(t)`. So the bind
   argument has the column's own type, custom `UnmarshalJSON` included.
   One retry: when `t` is `time.Time` and the strict decode failed, the raw is
   re-read as a string and run through `parseTime`. Clients send date-only
   strings constantly.

4. **`decodeList`** — `query/coerce.go:38` — element by element, with `null`
   elements preserved as Go `nil` so `{"in": [1, null]}` keeps its NULL.

## The query-string door

1. **`ParseTerm`** — `query/querystring.go:28`
   `field:op:value`, split on the **first two** colons only, so a timestamp
   survives. Two segments mean `eq` — an implicit `contains` would be convenient
   and occasionally very wrong.

2. **`compiler.terms`** — `query/querystring.go:46`
   Same four classes as the JSON door:
   - unary: the value is read as a bool (`strconv.ParseBool`), so
     `f=deletedAt:isNull:false` means `IS NOT NULL`;
   - textual: the raw string, uncoerced;
   - multi: `coerceAll` over every value;
   - scalar: `coerceAll` over the first value only.

3. **`compiler.coerceAll`** — `query/querystring.go:118`
   The literal `null` becomes Go `nil`; everything else goes to `coerceString`
   against `crud.ElemType(f.Type)`. A value the column cannot hold is a 400 that
   names the value and the type.

4. **`coerceString`** — `query/coerce.go:85`
   Order matters, and the first two lines are the reason:
   ```go
   if t == reflect.TypeOf(time.Time{}) { return parseTime(s) }        // before TextUnmarshaler
   if reflect.PointerTo(t).Implements(textUnmarshaler) { … }          // uuid, enums, money
   switch t.Kind() { … }                                              // with overflow checks
   default: json.Unmarshal(strconv.Quote(s), ptr)                     // structs with their own decoder
   ```
   `time.Time` is checked **before** the `TextUnmarshaler` branch it would
   otherwise win: its own `UnmarshalText` takes RFC 3339 and nothing else, which
   would reject the date-only and space-separated forms the JSON door accepts.
   Integers go through `ParseInt`/`ParseUint` at 64 bits and then
   `OverflowInt`/`OverflowUint`, so a value too large for an `int32` column is
   an error and never a wrapped one. `[]byte` columns take the raw bytes.

5. **`Coerce`** — `query/coerce.go:80` — the exported wrapper. Transports need it
   for path parameters: `Handler.id` (`http/crudfiber/handler.go:373`) coerces
   `:id` to the repository's `ID` type through it, which is why a uuid key works
   in a URL with no extra code.

## `timeLayouts` — the accepted timestamp forms

`query/coerce.go:58`, tried in order:
`RFC3339Nano`, `RFC3339`, `2006-01-02T15:04:05`, `2006-01-02 15:04:05`,
`2006-01-02`. The two zoneless forms parse as UTC; an offset in the string is
preserved. Both doors use this list — `decodeValue` via its retry,
`coerceString` unconditionally — which is what makes
`?f=createdAt:gte:2026-01-01` and `{"createdAt":{"gte":"2026-01-01"}}` the same
query.

## `query/ops.go` — the table that keeps the doors together

- **`opNames`** — `query/ops.go:30` — every spelling a client may send maps to
  one `opKind`: `eq = equals is`, `ne neq not != <>`, `gte >= ge`, `nin notin`,
  `contains search`, `startswith prefix`, `isnull null`, and so on.
- **`normalizeOp`** — `query/ops.go:54` — strips a `$` prefix (Mongo style),
  tries the exact spelling, then the lowercased one. So `$gte`, `gte`, `GTE` and
  `>=` are one operator.
- **`textual()` / `multi()` / `unary()`** — `ops.go:65-83` — the classification
  both compilers branch on, so neither can decide on its own that `contains`
  takes a list.
- **`buildScalar` / `buildText` / `buildMulti`** — `query/filter.go:248`,
  `:265`, `:282` — the single place an operator becomes a predicate. Both doors
  call these three functions. That is the whole mechanism against drift: a new
  operator is added to `opNames`, classified once, and built once.

## Where the decisions bite

- **The bind argument carries the column's Go type.** Not `float64`, not
  `string`. `crud.ElemType` is what makes an `Opt[T]` or `*T` column resolve to
  `T` on both doors.
- **`time.Time` beats `TextUnmarshaler`.** Reordering those two lines silently
  narrows what timestamps the query string accepts.
- **A value that does not fit is a 400, never a zero.** `decodeValue` and
  `coerceString` both return errors rather than leaving the destination
  untouched — a zeroed operand is a filter the client did not write.
- **Both doors go through `opNames` and the three builders.** Any operator
  handled in only one compiler is a divergence between `GET` and `POST /query`
  that no test of one door would catch.
- **Patterns are bound, never concatenated.** `likeNode.render`
  (`crud/predicate.go:250`) binds the pattern, and `escapeLike`
  (`crud/predicate.go:485`) escapes `\`, `%` and `_` in the client's text, so a
  wildcard in a search term is a literal character.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| `{"views": "abc"}` | `decodeValue` (`coerce.go:32`) | 400 `Views expects int, got "abc"` |
| `?f=views:gte:abc` | `coerceAll` (`querystring.go:128`) | 400 `"abc" is not a valid int` |
| a value too large for the column's width | `OverflowInt`/`OverflowUint` (`coerce.go:114`, `:123`) | 400 `… does not fit in int32` |
| a timestamp in none of the five layouts | `parseTime` (`coerce.go:72`) | 400 `cannot parse … as a timestamp` |
| `{"contains": null}`, `{"in": null}`, `{"isNull": null}` | `compiler.operator` (`filter.go:204`) | 400 `… has no meaning with null` |
| `{"isNull": "yes"}` | `filter.go:211` | 400 `isNull expects true or false` |
| `{"between": [1,2,3]}` | `buildMulti` (`filter.go:287`) | 400 `between expects exactly two values` |
| unknown operator spelling | `normalizeOp` → `filter.go:197` / `querystring.go:62` | 400 `unknown operator "…"` |
| `?f=title` (one segment) | `ParseTerm` (`querystring.go:39`) | 400 `… is not field:op:value` |
| `:id` that does not coerce | `Handler.id` (`handler.go:374`) | 400 `"…" is not a valid id` |
| `{"in": []}` | not an error — `inNode.render` (`crud/predicate.go:197`) | `1 = 0`; `notIn` of nothing is `1 = 1` |

## Files

| File | Role |
|---|---|
| `query/coerce.go` | `decodeValue`, `decodeList`, `coerceString`, `Coerce`, `parseTime`, `timeLayouts` |
| `query/ops.go` | `opNames`, `normalizeOp`, the operator classes |
| `query/filter.go` | the JSON door; `buildScalar` / `buildText` / `buildMulti` |
| `query/querystring.go` | the query-string door; `ParseTerm`, `terms`, `coerceAll` |
| `crud/meta.go` | `ElemType` — the coercion target |
| `crud/predicate.go` | `escapeLike`, and the nodes that bind rather than concatenate |
| `http/crudfiber/handler.go` | `id` — the path-parameter user of `Coerce` |

## Tests that walk this flow

- `TestBothDoorsBindTheSameValue` — `query/coerce_test.go` — the anti-drift test.
- `TestEveryOperatorAliasMeansTheSameOnBothDoors` — `query/querystring_test.go` — the whole alias table.
- `TestUnknownOperatorIsRefusedOnBothDoors` — `query/querystring_test.go`.
- `TestNullMeansIsNullOnBothDoors` — `query/coerce_test.go`.
- `TestListsCoerceEveryElement` — `query/coerce_test.go`.
- `TestEveryTimestampLayoutIsAccepted` — `query/coerce_test.go`.
- `TestTimestampZonesSurviveCoercion` — `query/edge_test.go`.
- `TestCoerceHandlesEveryScalarKind` — `query/coerce_test.go`.
- `TestCoerceRefusesValuesTheColumnCannotHold` — `query/coerce_test.go`.
- `TestOverflowNeverWraps` — `query/coerce_test.go`.
- `TestBadValuesAreRejectedByBothDoors` — `query/coerce_test.go`.
- `TestUncoercibleValuesAreRejectedNotZeroed` — `query/edge_test.go`.
- `TestNullOperandsAreRefusedWhereTheyHaveNoMeaning` — `query/edge_test.go`.
- `TestValuesAreTypedByColumn` — `query/query_test.go`.
- `TestQueryStringTermKeepsColons` — `query/query_test.go`.
- `TestQueryStringTermEdges` — `query/edge_test.go`.
- `TestWildcardsInAPatternAreEscaped` — `query/hostile_test.go`.
- `TestPayloadsInValuePositionsAreBoundNotWritten` — `query/hostile_test.go`.
- `TestAnIDThatDoesNotParseIsRefusedBeforeTheRepository` — `http/crudfiber/edge_test.go`.
- `TestTheDSLCoercesAUUIDFromTheWire` — `test/integration/uuid_test.go` — the `TextUnmarshaler` branch, end to end.

## See also

[[FL-001]] [[FL-005]] [[FL-011]]
