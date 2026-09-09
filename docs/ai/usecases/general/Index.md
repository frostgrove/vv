# General use cases and readiness sweep

`general` is for consumer promises that no one module can deliver alone: the
assembled CRUD API, its end-to-end error contract and cross-framework
observability. `UC-NNN` files are settled contracts; `General.md` is the
fallible, source-checked readiness sweep.

| Module | Import paths | Sweep | UC files | Verdict |
|---|---|---|---|---|
| general | `github.com/frostgrove/vv` and the assembled framework | [General](General.md) | [UC-001](UC-001-expose-a-crud-api-without-handlers.md) · [UC-015](UC-015-map-a-failure-to-the-transport.md) · [UC-030](UC-030-observe-the-framework-with-opentelemetry.md) | not ready |
