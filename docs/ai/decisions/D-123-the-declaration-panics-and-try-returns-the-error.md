# D-123 — The declaration panics, and `Try…` is what returns the error

**Status:** accepted
**Invariant:** `event.Define` and `event.Declare` panic on a malformed
declaration. `event.TryDefine` and `event.TryDeclare` return the same value
through the same code path, and the panicking pair is three lines over the
returning pair. There is no `MustDefine`: the short name is the one an
application writes, and the `Try…` name is the one a negative test writes.

## The decision

A declaration is written at package level:

```go
var Account = event.Define[Ledger, AccountID]("account", keyOf)
var Credit  = event.Declare(Account, "account.credited", event.From(event.JSON[Credited]()), creditLedger)
```

There is no `err` to handle at package level and nothing sensible to do with one
if there were. A malformed declaration is a programmer error found before the
process serves anything, so it panics — and the whole declaration class
(`ErrDeclaration`, `ErrSealed`, `ErrCodecType`) is panicked and never returned.

`crud/sqlrepo/blueprint.go:74:Define` / `:82:TryDefine` is the same pair and the
same argument, and [[D-021]] already cites it by line as an instance of
preferring the shape a caller writes over the shape Go orthodoxy expects.

## Why the sibling exists at all

**For the negative tests.** Fifteen triggers refuse a declaration outright — six
malformed aggregates and nine malformed facts — and seven more refuse a chain or
name the revision whose codec was refused. Asserting each one through a panicking
constructor is twenty-two `recover()` blocks and twenty-two chances to write one
that catches the wrong panic.

`TryDefine` and `TryDeclare` make that a table.

## Why `Define`/`MustDefine` is the wrong inversion

The phase-1 specification originally recorded the opposite — an error-returning
`Define` with a `MustDefine` beside it — and it is corrected here rather than
left to be rediscovered.

The name a consumer types a hundred times is the one that should be short, and
the shape a consumer writes at package level is the panicking one. `MustX` reads
as *the unusual choice*, and it is the usual one here. It also puts the
five-line `if err != nil` into every declaration in every application, at a
place where the only correct handler is a panic.

## Why the invariant survives it

The rule that no constructor does anything ([[D-121]]'s import-graph half, and
§INV-013 of the phase-1 specification) is about side effects, not about panics.
`Define` calls `TryDefine` and panics on its error; `Declare` calls `TryDeclare`
and panics on its error. Neither has a body of its own, so the two cannot
disagree about what is malformed — which is the failure mode a hand-written
panicking twin has, and the reason this pairing is worth writing down.

## What it forbids

- Do not add `MustDefine` or `MustDeclare`. The panicking name is the short one.
- Do not give the panicking constructor a body of its own. It calls the `Try…`
  one and panics on the error, and that is the whole of it.
- Do not return a declaration-class error from anywhere but a `Try…`
  constructor.
- Do not extend the pattern to a runtime call. A load, an append and a read
  return errors; a *declaration* is the only thing that panics, because it is
  the only thing that runs before the program serves anything.

## Where it lives

- `event/aggregate.go` — `Define`, `TryDefine`.
- `event/fact.go` — `Declare`, `TryDeclare`.
- `event/chain.go` — `From` and `Then`, which return neither: a chain carries its
  defect and the declaration that reads it is what raises it.

## Proven by

- `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` — every trigger, in
  a table, asserting the panicking form panics with the same error text the
  returning form returns, plus the control that the valid declaration in the
  same test builds.
- `TestTheSealRefusesALateFact` — the one trigger that is about time rather than
  shape, and the enumeration of the six readers that seal.

## See also

[[D-021]] [[D-121]] [[FL-036]] [[UC-032]]
