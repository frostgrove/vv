# `port` — not implemented

**Tier:** contract, on the manifest. Root module.

**What it owes:** the transport-neutral half — commands, `Service`, `Mapper`, and
the path chain that turns a column name back into the path the client sent.

**Open before any code:**

- Scope is **request/response transports**: three HTTP bindings and gRPC. A queue
  consumer has no request, no id in a path and no status, so it calls the service
  rather than the port.
- The constructor cannot gain a fourth type parameter: Go has no default type
  arguments, and `New[M, ID, U](repo)` must keep inferring (D-022). The answer is
  a second constructor, `NewFor[In, M, ID, U](repo, mapper)`.
- A generated service embeds the default `Service`, which makes `port` an
  implementation as much as a contract. If that stays true, it is not on the
  manifest and this file is wrong.
- It supersedes D-034, which says everything shared comes from `crudhttp`.

**Governed by:** [ROADMAP-errors.md](../ROADMAP-errors.md) §4 and §13, phase 5.
