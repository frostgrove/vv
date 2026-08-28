# D-065 — Structs have reference semantics unless they are values

**Status:** accepted

## Decision

An application-facing struct is passed and returned as a pointer by default.
This includes resources with a lifetime, configuration, transport calls and
operation contexts. Constructors return the pointer they created; decorators
and adapters keep passing that same pointer instead of copying a façade.

A struct stays a value only when copying is part of its meaning:

- an ID/value object or a deliberately immutable query AST node;
- a request/response snapshot or another data record where ownership is copied
  at the boundary;
- a type whose value receiver is required for safe JSON encoding (notably
  `errs.Violation`, `errs.Path` and `errs.Fault`).

That exception is semantic, not a size threshold. A two-word struct with an
identity is still a pointer; a larger immutable response record may still be a
value.

## Why

Passing a façade such as `crud.Repo` by value was safe only by accident: it
contained one interface today. The generated factory already returned a
pointer, while `specs.Executor` accepted a copy. That obscured ownership,
allowed future fields to be copied silently and made the public API alternate
between two conventions for the same object.

The same ambiguity appeared at other boundaries: a database configuration,
the remote call envelope and a probe request all carry shared references or
are enriched along a pipeline. A pointer makes both the identity and a nil
contract visible. It also prevents a new field from quietly making an existing
call copy a larger state object.

Making every struct a pointer would be a different bug. `Order` is a value the
caller combines into an AST; `Envelope` is a response snapshot; JSON needs a
value receiver for a value held in a field or map. Turning those into pointers
would change their contract, add nil states where none exist and can bypass the
very serialization guards they carry.

## Consequences

- New public constructors and operation seams use `*T` for non-value structs.
- A public value parameter needs a doc comment or a local design reason that
  says why copying is the contract.
- Nil is checked at public pointer boundaries when a useful error can be
  returned; constructors and terminal entrypoints keep their existing panic or
  exit policy.
- Generated bindings preserve a `type` alias to the value type, while their
  factories return `*Alias`, so consumers write `*ProductRepo`, never `**`.

## Where it lives

- `crud/repo.go`, `crud/sqlrepo/blueprint.go` and
  `crud/decorators/specs/executor.go` — repository composition.
- `remote/transport.go`, `crud/probe/probe.go` and `crud/executor.go` —
  operation contexts.
- `utils/vvdb`, `utils/vvdb/dbpgx`, `storage`, `storagefs`, `storageminio`,
  `internal/codegen` and `utils/vvgoose` — configuration and constructors.
- `errs`, `crud/predicate.go`, `crud/page.go` and `port/command.go` — the
  intentional value-semantic exceptions.
