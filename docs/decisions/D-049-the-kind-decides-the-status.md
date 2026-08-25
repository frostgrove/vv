# D-049 — The kind decides the status; the sentinel decides only when there is no fault

**Status:** accepted
**Invariant:** Where an error carries an `*errs.Fault`, the status comes from the fault's resolved `Kind`. The `crud` sentinel decides the status only when there is no fault. One error never produces two mappings.

## The decision

`§2`'s status table puts `required` and `check` in the **422** row. The adapters
wrap both in `crud.ErrConflict`, which [[D-015]]'s table maps to **409**. Both
cannot be true, and the roadmap says so: *"shipping both mappings — 409 from the
sentinel, 422 from the Kind — is the one outcome that must not happen."*

The kind wins. `crudhttp.StatusFor` is the §2 table written out arm by arm, and
`port.KindOf` resolves one kind from the fault plus its violations' codes. A refusal
with no fault falls through to the sentinel table, unchanged.

The observable consequence, measured:

| | status |
|---|---|
| a `NOT NULL` classified as `required` | **422** |
| the same `NOT NULL` unclassified | **409** |
| a bare `crud.ErrConflict` | 409 |
| a bare `crud.ErrNotFound` | 404 |

## Why

**Because narrowing the sentinel instead would turn an unclassified violation
into a 500.** The obvious alternative is to stop `sqlfault.Integrity` wrapping
`23502`, `23514`, `3819` and `1364` in `ErrConflict` at all. Then a NOT NULL that
*was* classified answers 422 and one that was not answers 500 — which is the
regression [[D-046]]'s deliberately-wider gate exists to prevent, and which
[[FL-011]]'s table promises against. The gate stays wide; the kind refines.

**Because the status table is not `min(Kind)`.** `errs/code.go` says the numeric
values of `Kind` are not API. A `min` over the constants would silently couple
the status to the declaration order, so one reordering — a thing the doc says is
allowed — would move every status at once. It is written out, arm by arm.

**Because an undeclared code contributes no kind.** A service that adds a code
and forgets `Codes.Add` would otherwise have its own 422 resolve to `KindInternal`
— zero — and become a 500 that says nothing. An unknown code is skipped, and the
fault's own kind decides.

**What it costs, and it is not free.** The status now depends on whether
classification succeeded, so it depends on which `crudsql` constructor declared
the engine. That is an observable dialect difference and [[D-019]] difference
10(b) is amended in the same change — the sentence "the status never moves; the
code does" was written before this decision and is now false. It is also the
sharpest argument for `crudsql.Open`, `From` and `Source` refusing to guess an
engine.

## What it forbids

- Do not map a kind and a sentinel for the same error. One error, one mapping.
- Do not narrow `sqlfault.Integrity` to make the two agree. That trades a wrong
  4xx for a silent 500.
- Do not implement `StatusFor` as an ordering over `Kind`'s numeric values.
- Do not let an undeclared code resolve to `KindInternal`. Skip it.
- Do not read this as the sentinel table being dead. It is what answers when
  nothing classified, which is every refusal the library raises itself.

## Where it lives

- `port/porthttp/errors.go:StatusFor` — the §2 table, arm by arm.
- `port/kind.go:KindOfWith` / `:KindOf` — the precedence resolution. It moved
  with the vocabulary at phase 5: a kind is not HTTP and a status is ([[D-045]]).
- `port/porthttp/errors.go:Status` — the seam whose signature is unchanged, now
  `StatusFor(port.KindOf(err))` ([[UC-015]] guarantee 8, [[D-045]]'s forbid).
- `port/kind.go:FaultOf` — what a non-fault error becomes.
- `crud/rpc/crudgrpc/status.go:CodeFor` — the same table in gRPC's words, added at
  phase 9. Two tables, one answer: the kind is resolved once and each transport
  spells it.

## Proven by

- `TestStatusMapsWhatItPromisesTo` — `crud/http/crudfiber/edge_test.go` and its two
  twins: the table, arm by arm, in all three HTTP bindings.
- `TestKindMapsToTheCodeItPromisesTo` — `crud/rpc/crudgrpc/status_test.go` — the
  second vocabulary over the same answer, which is what says the kind decides
  rather than the status table being the decision. Two of the eight kinds
  collapse into one `codes.Code` there, and that is the cost [[D-052]] records
  rather than a per-code refinement this decision forbids.
- `TestEveryRouteMapsARefusalTheSameWay` — a route that skipped the mapping
  would slip past a per-route test.
- `TestA500NeverEchoesTheInternalError` — the silence, extended to the envelope.
- `TestStatusIsTheKindTableOverThePortsAnswer` — `port/porthttp/errors_test.go` —
  the seam from the HTTP side, asserting the concrete status as well as the
  composition, so a table answering one status to everything cannot pass.
- `TestThePrecedenceTableResolvesAMixedFault` — `port/kind_test.go` — every
  adjacent pair of the order, in both build orders.

## See also

[[D-015]] [[D-019]] [[D-044]] [[D-046]] [[UC-015]] [[FL-011]]
