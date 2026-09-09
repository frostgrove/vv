# FL-039 — An audit contract becomes an executable checkpoint

**Entry points:** `scripts/audit-trace.sh`,
`scripts/audit_trace_test.go:TestAuditTraceRegistry`, and the S0-only
`scripts/audit_trace_import_test.go:TestAuditTraceDesignImport`
**Implements:** the S0 traceability boundary for [[UC-034]]; no runtime audit
behavior
**Governed by:** [[D-020]] [[D-033]] [[D-121]] [[D-130]]

This is the path from the reviewed audit contract and implementation plan to a
tracked, cumulative checkpoint that can fail on a missing guarantee, test,
mutant, positive control, package route or semantic body. It is the only audit
flow implemented at S0. The audit kernel, stores, CRUD adapter, PostgreSQL module
and application integrations described at the end of this flow are owed by later
sections and are not current APIs.

## What S0 materializes

The source presentation is
`.agents/artifacts/usecases/AUDIT_USECASES.md` plus
`.agents/artifacts/plans/AUDIT_PLAN.md`. Those files are review artifacts rather
than a clean-checkout CI dependency. The build-tagged importer reads them once at
the S0 handoff and compares their complete normalized content with three tracked
authorities:

| Authority | Role |
|---|---|
| `scripts/testdata/audit_trace.tsv` | the sole normalized adjacency, package-route, reservation and activation authority |
| `scripts/testdata/audit_trace_semantics.json` | the sole executable text authority for every actor goal, happy requirement, edge requirement, invariant, test obligation, mutant defect and positive neighbour |
| `scripts/testdata/audit_trace_anchor.json` | an independent completeness authority over exact inventories, counts and per-record and aggregate digests |

The importer in `scripts/audit_trace_import_test.go` is guarded by
`//go:build audit_trace_import`. It parses the two review artifacts, derives the
canonical facts and semantic records, and compares them with the tracked files.
It never writes or regenerates an authority. Its tagged invocation is therefore
a deliberate S0 handoff or later explicit contract-reopen check, not an ordinary
test that makes `.agents` required in a release checkout.

After that comparison passes, `scripts/audit_trace_model_test.go`,
`scripts/audit_trace_test.go` and `scripts/audit-trace.sh` read only the tracked
TSV, semantics and anchor. A later
contract edit reopens S0: edit the reviewed presentation and tracked authority
together, rerun the compare-only importer, recompute the independent anchor, and
obtain fresh happy-path and adversarial review before dependent code moves.

## Canonical registry and independent anchor

`scripts/testdata/audit_trace.tsv` is ASCII, TAB-delimited, LF-terminated and
byte-sorted after its one schema row. One row holds one fact; lists and ranges
are not legal shortcuts. The closed fact grammar distinguishes nodes, section
delivery ranks, typed edges, semantics, concrete test reservations and package
execution routes.

The registry has ten node kinds with bijective prefixes: actor goals, happy
requirements, edge requirements, invariants, test obligations, implementation
sections, defect mutants, positive neighbours, test reservations and packages.
Every actor goal reaches at least one requirement. Every requirement has an
actor, proof obligation, implementation section and guarded mutant. Every mutant
has one primary killing obligation, one owning section, one mutant reservation
and one distinct positive neighbour. Every obligation has exactly one activation
section and one coverage reservation. Activation is independent of the sections
that implement requirements, so the registry never fabricates an obligation by
section cross-product.

`scripts/testdata/audit_trace_semantics.json` freezes the normalized text for
each semantic node. Whitespace and Unicode bytes in the tracked record are
significant. Its canonical JSON has fixed members, ordering and one terminal LF;
unknown or duplicate members, invalid UTF-8, trailing values and a missing or
duplicate semantic identity fail.

`scripts/testdata/audit_trace_anchor.json` independently inventories every node
kind and freezes the graph, node-set and semantic-set digests, every graph-fact
digest and every semantic-record digest. The anchor never hashes itself. This is
what catches deletion of an internally closed component or highest identifier:
the reduced graph could still be mutually consistent, but it cannot match the
independent inventory and digests.

The S0 materialization contains exactly:

| Measure | Count |
|---|---:|
| actor goals | 11 |
| happy requirements | 99 |
| edge requirements | 117 |
| invariants | 83 |
| test obligations | 18 |
| implementation sections | 8 |
| defect mutants | 70 |
| positive neighbours | 70 |
| test reservations | 158 |
| package routes | 8 |
| all nodes | 642 |
| semantic records | 468 |
| non-schema graph facts | 4,172 |

A changed count is not updated by editing this flow. It is a contract change:
the reviewed source, all three tracked authorities and the fresh review gate move
together.

## One section becomes one executable run

`scripts/audit-trace.sh` accepts one exact section label. It obtains delivery
order from registry section rows rather than comparing numeric suffixes. The
frozen order is S0, S1, S2, S4, S3, S5, S6, S7; this puts the useful CRUD and
composition alpha before advanced history and lifecycle work.

The script first runs the structural bootstrap from the repository root with the
fixed argument vector for `TestAuditTraceRegistry`. It consumes `go test -json`
and requires exactly one non-skipped passing event from the exact scripts package.
A successful process with no matching test is a failure.

It then selects every reservation whose activation rank is at or before the
requested checkpoint, groups reservations by their fixed package route, builds
an anchored regular expression from escaped exact test names, and runs the fixed
unit or integration profile for that route. It requires exactly one matching
pass for each declared package, test and role tuple. A skip, failure, missing or
duplicate pass, wrong package, lost integration tag or database setting,
undeclared in-scope audit trace test, deleted command, altered authority or
subprocess failure is a checkpoint failure.

Coverage, mutant-kill and positive-neighbour reservations are separate roles.
One broad passing test cannot impersonate all three. The default source
inventory excludes the build-tagged S0 importer, so the compare-only test is not
mistaken for an undeclared ordinary reservation.

At S0 the only active obligation is AT-017 in the scripts package. Its current
reservations are `TestAT017S0Contract`,
`TestAuditTraceCheckpointRejectsMissingTest`, and
`TestAuditTraceCheckpointPreservesCompleteGraph`. Later checkpoints retain those
passes and add the obligations activated by the section being delivered.

## Current S0 files

| File | What it holds |
|---|---|
| `docs/ai/decisions/D-130-audit-evidence-is-explicit-protected-and-transaction-honest.md` | the binding audit boundary and owed-runtime status |
| `docs/ai/usecases/modules/audit/UC-034-record-and-investigate-auditable-evidence.md` | the consumer-visible audit contract, with no implementation coordinates |
| `docs/ai/flows/FL-039-an-audit-contract-becomes-an-executable-checkpoint.md` | this implementation map and the current-versus-owed boundary |
| `docs/roadmaps/2026-09-01-audit-log-roadmap.md` | current topology, competitive evidence, delivery order and release gates |
| `scripts/testdata/audit_trace.tsv` | normalized graph and execution routing |
| `scripts/testdata/audit_trace_semantics.json` | exact behavior text |
| `scripts/testdata/audit_trace_anchor.json` | independent completeness anchor |
| `scripts/audit_trace_model_test.go` | strict TSV/JSON decoders, graph cardinality and node/fact validation, canonical encoders and all digest/anchor recomputation |
| `scripts/audit_trace_test.go` | section runner, package/source inventory, subprocess event controls and the three S0 reservations |
| `scripts/audit_trace_import_test.go` | build-tagged compare-only bridge from the reviewed design presentation |
| `scripts/audit-trace.sh` | bounded section runner over fixed command arguments and exact JSON pass events |

The three directory indexes are updated beside these files, but index rows are
navigation rather than another trace authority.

## Owed runtime paths

The package routes below are present in the registry so reservations already
have one stable destination. At S0 the directories are promises, not current
runtime APIs:

```text
audit/                             S1–S3 dependency-light kernel
audit/auditmemory/                 S2 complete concurrent memory store
audit/audittest/                   S2/S6 public conformance and policy proxies
audit/auditcrud/                   S4 sealed transaction-aware CRUD composition
audit/internal/auditcrudbridge/    S4 private one-use Gate-to-receiver bridge
audit/auditpg/                     S5 nested PostgreSQL module
test/auditflow/                    S4/S6 application-only Frostgrove integrations
```

S1 owes declarations, catalogues, codecs, context and privacy. S2 owes the
usable recorder/basic history core, memory store and conformance foundation. S4
then owes the sealed CRUD happy paths and the first authentication, tenancy,
fault, event, job, storage, error, telemetry and module-composition fixtures. S3
adds attempts and advanced history, reconstruction and lifecycle. S5 adds live
PostgreSQL. S6 closes the exhaustive integration, docs and example matrix. S7 is
the hostile-edge, concurrency, mutation and whole-tree hardening pass.

No path above is credited with behavior until its own activated reservations
pass. S0 proves that those obligations are complete and executable, not that the
future implementation already exists.

## Commands that walk this flow

The S0 completion checkpoint is:

```bash
git diff --check
go test ./scripts -run 'Docs|Decision|Usecase|Roadmap|Link' -count=1
go test -tags=audit_trace_import ./scripts -run '^TestAuditTraceDesignImport$' -count=1
./scripts/audit-trace.sh S0
```

The tagged importer is run only from a checkout that contains the reviewed
`.agents` artifacts. Ordinary and later clean-checkout checkpoints use the final
command for their section and consume only tracked authorities.

## Tests that walk this flow

- `TestAuditTraceDesignImport` compares, but never generates, the complete S0
  presentation and the tracked authorities.
- `TestAuditTraceRegistry` validates canonical bytes, schemas, inventories,
  digests, typed connectivity, activation, package routing and concrete
  reservations.
- `TestAT017S0Contract` proves the structural obligation is activated and
  executable at S0.
- `TestAuditTraceCheckpointRejectsMissingTest` kills deletion of its concrete
  mutant reservation. The adjacent trace-model and runner tests independently
  reject altered ranks, routes, counts, digests, roles, package settings and
  zero, skipped, duplicate or wrong-package subprocess events.
- `TestAuditTraceCheckpointPreservesCompleteGraph` is the independent legal
  neighbour: the untouched complete graph and exact passes survive the same
  runner.
- `TestTraceImporterAdversarialControlsRunInASubprocess` proves duplicate and
  contradictory source tables cannot overwrite keyed authorities silently.
- `TestTraceModelRejectsFactsWithoutNodes` refuses extra semantic, package and
  test facts and a missing S0 rank row even when their surrounding syntax is
  valid.
- `TestTraceRunnerRejectsAdversarialBootstrapEvents` drives real checkpoint
  subprocesses with missing, skipped, duplicate and wrong-package pass events.

## Traps

- The review artifacts are not regenerated from the tracked registry and the
  tracked registry is not regenerated silently from the review artifacts. Either
  direction would let one bug rewrite its own authority.
- A graph digest does not replace the independent anchor. A semantic digest does
  not prove that a legal edge has the right product meaning.
- Section labels are identities. S4 intentionally executes before S3 and only
  the declared delivery rank defines that order.
- A passing `go test` process is not proof. The runner checks exact non-skipped
  test events and package identities.
- Future package routes are reservations, not evidence that a package, exported
  symbol, database schema or integration currently exists.
