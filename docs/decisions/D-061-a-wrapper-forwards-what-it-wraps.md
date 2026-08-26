# D-061 — A wrapper forwards what it wraps, and the library walks to find it

**Status:** accepted
**Invariant:** No optional interface is looked up with a bare type assertion on the layer directly below. A decorator says what it wraps with `Next()`; a `Source` wrapper says so with `UnwrapSource()`; and `crud.SourceOf`, `crud.BeginnerOf`, `crud.ReadSourceOf` and `crud.KeyOf` follow both. The order layers were listed in never decides whether a feature works.

## The decision

Go's embedding promotes only the embedded *interface's* own method set. A
decorator that embeds `crud.Core` therefore erases every method the wrapped value
had that `Core` does not name — silently, at compile time, with nothing to see.
The same is true one level down: a `Source` wrapper erases `Beginner`,
`ReadSourcer` and `Identified`.

Two one-method interfaces make the chain walkable, and four helpers walk it:

| Interface | Implemented by | Walked by |
|---|---|---|
| `crud.Nexter` — `Next() Core[M, ID]` | `crud.Base`, `security.gate`, `faults.enricher` | `crud.SourceOf` |
| `crud.SourceUnwrapper` — `UnwrapSource() Source` | any consumer wrapper | `crud.BeginnerOf`, `crud.ReadSourceOf`, `crud.KeyOf` |

Both walks are bounded at 64 steps: a chain is built once at start-up and is a
handful of layers deep, so a walk that long is following a cycle somebody built
by accident, and "not found" is a better answer than not returning.

## Why

**Because the failure was live and it made decorator order load-bearing without
saying so.** `faults` asserted `crud.Sourced` on the layer directly below it to
find the datasource its probe runs on. `security.gate` embeds `crud.Core` and so
does not forward `Source()`. So this bound:

```go
Docs.Bind(src, security.Gate(p), faults.Enrich(...))
```

and this, which differs only in the order of two arguments, panicked at start-up:

```go
Docs.Bind(src, faults.Enrich(...), security.Gate(p))
```

The chain always knew where the datasource was. Only the type system did not.

**Because the erasure downwards is worse, and only one third of it is loud.** A
consumer wrapping a `Source` to log or trace statements — the ordinary way to
instrument this library, and now the documented one ([[D-062]]) — loses three
things at once:

- `Beginner` — every `Tx` becomes `ErrNoTxSupport`. Loud.
- `Identified` — the catalog keyed on the physical handle no longer matches, and
  [[D-041]] refuses at start-up. Loud.
- `ReadSourcer` — every read goes to the primary. **Silent.** The replica sits
  idle and nothing anywhere connects that to the day somebody added a metrics
  wrapper.

A library that puts its magic's failures at start-up ([[D-021]]) cannot leave
that one at request time.

**Because the alternative — forwarding every optional interface from a generic
wrapper — is not expressible.** A wrapper type either has `Begin` or it does not;
Go has no conditional implementation. A wrapper that always has it and sometimes
refuses has lied about the pool it stands in front of, which is the reason
`crud.ReadWrite` returns two types rather than one. One method that says *what*
is wrapped is the only shape that composes.

**Because the walk is the honest answer and the assertion was not.** A layer that
implements neither `Sourced` nor `Nexter` still ends the walk, and `faults` still
refuses at Bind time. That refusal is now about the chain rather than about Go's
promotion rules, and its message says which.

## What it forbids

- Do not write `x.(crud.Beginner)`, `x.(crud.ReadSourcer)` or
  `x.(crud.Sourced)` on a value that may be wrapped. Use the helper.
- Do not add a decorator to this repository without a `Next()`. Embedding
  `crud.Base` gives one.
- Do not make either walk unbounded.
- Do not make `SourceOf` answer for a layer that says nothing about what it
  wraps. "I do not know" is a real answer and the start-up refusal depends on it.

## Where it lives

- `crud/executor.go` — `Nexter`, `SourceOf`, `SourceUnwrapper`, `unwrapSource`,
  `identityOf`, `BeginnerOf`, `ReadSourceOf`, `KeyOf`, `ownScope`,
  `readWrite.UnwrapSource`, `readWrite.DataSource`, `maxChainDepth`.
- `crud/repo.go:Base` — the pass-through that supplies `Next()`.
- `crud/decorators/security/security.go:Next`
- `crud/decorators/faults/faults.go:Next`
- `crud/decorators/faults/probe.go:declare` — the caller that used to assert.
- `crud/sqlrepo/repository.go:newRepository` — the replica lookup.

## The half that was missed, and what it cost

The walk was applied to `BeginnerOf`, `ReadSourceOf` and `KeyOf` and **not** to
`ownScope` or `readWrite.DataSource`, and the gap in `ownScope` was worse than the
bug it half-fixed.

`InTx` resolves the Beginner with `BeginnerOf`, which walks, and scopes the
binding it pushes with `ownScope`, which did not. So a wrapped source opened its
transaction and then bound it **unscoped** — the old, unconditional join — and
every repository in the process adopted it, including ones bound to another
database. That is [[D-027]]'s territory reached by accident, through the wrapper
[[D-062]] recommends, with nothing said. Before this decision existed the same
wrapper was refused at `InTx` with `ErrNoTxSupport`: wrong, but loud. Half a walk
turned a loud refusal into a silent cross-database write.

`readWrite` was the second gap and a different shape: it is itself a wrapper, and
it implemented no `UnwrapSource`, so it *ended* every walk that reached it. A
`ReadWrite` over an instrumented primary answered nil for its own identity and
lost its catalog, which keys on `Identified` directly.

Both are closed by one function. `identityOf` is the single walk `KeyOf`,
`ownScope` and `readWrite.DataSource` all call, so the three cannot disagree
about what a wrapped source is — which is exactly how they came to disagree.

## Proven by

- `TestAProbeFindsItsSourceThroughADecoratorAboveTheRepository` in
  `crud/decorators/faults/probe_test.go`. It replaces a test that pinned the old
  limitation, and it asserts the probe *runs* rather than merely binds — a source
  that resolved to something unusable would pass the binding half.
- `TestADeclaredProbeWithNoReachableSourceRefusesAtBindTime`, same file, over a
  deliberately opaque decorator: the control that the walk has not become "yes to
  anything".
- `TestAWrappedSourceKeepsWhatItWrapsWhenItSaysWhatItWraps` in
  `crud/wrapsource_test.go` — all three interfaces through a wrapper, with a
  wrapper that omits `UnwrapSource` as the control that the helpers are following
  a declaration rather than guessing.
- `TestInTxReachesTheBeginnerThroughAWrapper`, same file, with the same control.
- `TestAWrappedPrimaryIsStillTheDatabaseItNames` and
  `TestATransactionOnAWrappedSourceIsScopedToItsDatabase`, same file — the two
  gaps above. The second fails with the message "a repository on another database
  adopted this transaction" the moment `ownScope` goes back to asserting, and
  each carries the control that a binding scoped to a *different* database is
  still not matched, so the walk cannot have become "yes to anything".
- `TestADecoratorBuiltOnBaseIsStillWalkableToItsDatasource` and
  `TestAChainThatWalksBackIntoItselfEndsRatherThanHangs` in
  `crud/basenext_test.go` — `crud.Base` is what `docs/modules/*/crud.md` tells a
  consumer to embed so their decorator stays walkable, and until this it was
  advice with nothing behind it: the library's own decorators hand-roll `Next()`.
  The control is a decorator *not* built on `Base`, which ends the walk — which
  is what makes `Base` worth recommending. The second test is `maxChainDepth`:
  a chain built into a cycle returns "not found" rather than not returning.
- `TestEveryVerbOnTheSeamIsGatedOrHasAWrittenReason` in
  `crud/decorators/security/obligation_test.go` — the same erasure, seen from
  above: it is why [[D-030]] cannot be enforced by the compiler.

- `TestConcurrentFirstUseOfARelationDoesNotRace` in `crud/relation_test.go` — the
  same lazy-resolution shape one level down, and the one place it was a data race
  rather than a lost interface. Only `Target()` sat behind a `sync.Once`;
  `resolveDefaults` wrote `LocalField` and `TargetField` outside it, onto the
  `*Relation` held by the process-global schema cache that every repository over
  the model shares. So it was not a race between repositories but between any two
  concurrent requests that both happened to be first across that relation. Run
  under `-race`; removing the second `Once` reports `WARNING: DATA RACE`.

## See also

[[D-009]] [[D-021]] [[D-030]] [[D-041]] [[D-042]] [[D-062]] [[FL-009]] [[FL-017]]
