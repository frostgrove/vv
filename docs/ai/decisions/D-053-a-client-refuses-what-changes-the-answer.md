# D-053 — A client refuses what changes the answer and documents what changes only its shape

**Status:** accepted
**Invariant:** a repository option a transport cannot carry is refused by name before anything is sent, unless it changes only the order or the freshness of the rows — and every option in that second set is written down.

## The decision

`remote.Resource` satisfies `port.Repository`, so it accepts `...crud.Option`.
The wire DSL is narrower than that interface. Every option therefore gets one of
three answers, and never a fourth:

| | options | answer |
|---|---|---|
| **translated** | `Page` `Limit` `Offset` `Sort` `Select` `Preload` `After` `Before` `Unpaged` `SkipTotal` `Distinct` `Where` | put on the wire |
| **refused** | `NarrowRelations` `ForUpdate` `Aggregate` / `GroupBy` | a `*remote.OptionError`, before the call |
| **documented** | `PrimaryOnly` `Unsorted` | accepted, cannot be honoured, named in the package doc |

The line between the second and third rows is the whole decision: **an option
that changes which rows come back is refused; one that changes only their order
or their freshness is documented.**

There is no fourth answer, and the one that is missing is the one that would
have been easiest to write: silently dropping the option and sending the rest.

## Why

**Because a dropped narrowing is invisible.** The response is well-formed, the
status is 200, and the extra rows look like data. Every other failure this
library has is loud — a 400 names the field, a 409 names the code, a 500 says
nothing but says it. A query that answers with more rows than were asked for
says nothing at all, and the caller has no way to find out. That is the same
argument `query.Request.UnmarshalJSON` already makes for refusing an unknown
document key: a client that writes `filtr` gets every row in the table, and it
is the one failure a client cannot see.

**Because `NarrowRelations` is a security boundary, not a performance hint.**
`crud.Where` constrains a statement's own `FROM` and nothing else. A relation
scope is what follows the narrowing into the tables a preload opens and a nested
filter's `EXISTS` opens, and it is how `crud/decorators/security` hides rows that
`Where` cannot reach. Stacking that gate over a remote resource has to fail. If
it did not, the gate would appear to work — the root table would be scoped, the
tests would pass — and the rows would leave through the preload.

**Because `PrimaryOnly` cannot be refused without breaking the composition the
paragraph above is protecting.** The security gate passes `crud.PrimaryOnly()`
on nearly every call it makes. Refusing it would make the gate unusable over a
transport, which would leave a consumer with the choice between no gate and a
gate that leaks — and the third row exists so that neither is necessary. What it
costs is real and is stated: a replica that lags is the remote service's
configuration, not the client's.

**Because `Unsorted` costs nothing that can be observed as data.** An empty sort
in the document means "the service decides", not "no order". The rows are the
same rows.

**Why not add the missing words to the DSL.** `unsorted`, `primary` and
`forUpdate` could all be document keys. Each is a promise the *service* would
then have to keep — a replica routing rule and a row lock are properties of a
connection, and a lock without a transaction to hold it is a lock that is
released before the caller sees the row. `ROADMAP` has no such request from a
consumer, and a key that means nothing is worse than an absent one.

## What it forbids

- Do not drop an option because the wire has no word for it. Refuse it, or move
  it into the documented row above with an argument for why the rows are the
  same rows.
- Do not accept `NarrowRelations` or `ForUpdate` on any transport, including one
  added later. If a future protocol really can carry a relation scope, that is a
  change to this table with its own argument, not a quiet exception.
- Do not refuse `PrimaryOnly`. It would break `security.Gate` over a client,
  which is the composition the refusals exist for.
- Do not grow the documented row without adding the option to the package
  documentation in the same change. An unenforceable option nobody wrote down is
  a dropped option with extra steps.
- Do not send `crud.Raw`. That refusal is [[D-054]]'s and is a stronger rule
  than this one: it is SQL, and it is refused even where a transport could carry
  the string.

## Where it lives

- `remote/options.go:ToRequest` — the three answers, in one function.
- `remote/options.go:OptionError` — what a refusal is, and why it is a plain
  error rather than a fault: nothing was sent, no status came back, and there is
  nothing for a form to display.
- `remote/resource.go:Resource.Update` — the same rule one level up. `Update`
  carries no query document on any route, so an option there is refused rather
  than ignored.
- `remote/remotehttp/transport.go:entityQuery` — one transport-specific refusal of
  the same shape: `GET /{id}` carries preload *paths* in a query string, so a
  narrowed preload has nowhere to put its filter and is refused. gRPC sends the
  whole document and does not need this ([[FL-013]]).
- `remote/dto.go:checkPatchable` — the start-up half. A `crud.Opt` field without
  `omitzero` marshals an undefined value as `null`, so a patch would empty every
  column the caller left alone. That is not an option and not droppable; it is a
  program that must not start.

## Proven by

- `TestAnOptionThatCannotCrossIsRefusedBeforeAnythingIsSent` in
  `remote/roundtrip_test.go` — the three refusals, each blamed by name, with the
  far side asserted to have received no call at all. Its control is the last
  block: `crud.Distinct()` and `crud.SkipTotal()` go through, so the test cannot
  pass for a client that refuses everything. Verified by dropping the
  `RelScopes` arm and watching the relation-scope subtest fail.
- `TestRawSQLIsNeverPutOnTheWire` in the same file — [[D-054]]'s refusal reached
  through a `crud.Option`, asserting the `*crud.PredicateError` blames
  `crud.Raw` and that nothing was sent.
- `TestAPatchDtoThatWouldEmptyAColumnIsRefusedAtStartup` in the same file — the
  DTO check, with two controls: the tag `cmd/vv` writes is accepted, and a
  field tagged `json:"-"` is not the check's business.
- `TestARemoteResourceMountsAsAGateway` in the same file — what the refusals are
  protecting. A remote resource mounted on a binding is a gateway, and a filter
  is asserted at the origin rather than on the response, so one that stopped at
  the gateway fails.

## See also

[[D-054]] [[D-045]] [[D-004]] [[UC-018]] [[FL-018]]
