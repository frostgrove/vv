# `tenancy/tenancyrow` + `crud/decorators/security` — Dimension 5 (data integrity of the SHARED-ROW topology) — AUDIT (2026-09-06)

> **Audit-window notes (added by the coordinator, confirmed):**
>
> 1. **A concurrent Claude Code session (a second `claude` process, not any of the auditors) was editing this
>    repository throughout the audit window.** That is the source of the mutation observed in
>    `tenancy/tenancyrow/row.go` (where `through.Apply` briefly returned `nil` unconditionally, then reverted)
>    and of the non-deterministic `go test ./tenancy/...` failures recorded below. The file-hash table in the
>    Map section pins the exact bytes every finding was read and probed against. Keep it.
> 2. **Re-verified against the tree as of 00:45:** `column.Narrow` still passes the raw ownership value to
>    `crud.Eq` with no nil guard (**GAP-5**), and `assign` still uses `ConvertibleTo` + `Convert` (feeding
>    **GAP-4**). **GAP-2** (`Through` over a to-many) and **GAP-1** (Options erasure) were verified against the
>    pinned hashes and the files have changed since — **re-confirm both before acting on them.**

## Map

Composition: `sqlrepo.Blueprint.Bind(source, mw...)` (`crud/sqlrepo/blueprint.go:218`) chains middleware over `sqlrepo.repository` and wraps the result in `crud.Repo`. `tenancyrow.Repository` (`row.go:267`) is one `crud.Middleware` = `security.Gate(tenancyrow.Policy(...))`. There is no container; the consumer wires it (`_examples/tenancy-sharedrow/main.go:83-85`).

Kernel/extension: `crud/decorators/security` is the kernel (mechanism: 37 methods on `gate[M,ID]`, `security.go`), `tenancyrow` is the policy extension. `tenancyrow.Policy` (`row.go:216-238`) fills exactly four fields — `Scope`, `RelationScopes`, `Inspect`, `Immutable` — and leaves `Requires`, `Authorize`, `InspectReads`, and all four `AllowUnscoped*` at zero. There are two `Ownership` implementations: `column[M]` (`row.go:64`) and `through[M]` (`row.go:144`); the interface (`row.go:21-27`) is the extension point.

State: `tenancyrow` holds **none** — `column[M]` and `through[M]` are immutable value types built at wiring, and `relations` is defensively copied (`row.go:61`). The narrowing is composed per call from the context scope. The mutable state that matters sits below: the process-global `crud.schemaCache` (`meta.go:173`) and `Relation.resolveDefaults` (`relation.go:532-547`), which `CLAUDE.md` already flags as a past race.

Layering: `Policy.Scope` and `Policy.Inspect` call `authority.Scope(ctx, class)` (`context.go:39-58`), which with `Spec.Revalidate` reaches the application's `Resolver` — i.e. the control plane — from inside every verb.

Tests: `tenancy/tenancyrow/{row,contract,revalidate,support}_test.go` (705 lines), `crud/decorators/security/*_test.go` (~130 KB), `test/integration/tenancy_test.go` (2 functions), `test/integration/gate_relscope_test.go` (with the documented "not declared" control). `go test ./tenancy/tenancyrow/... ./crud/decorators/security/... ./crud/` — **green** at the audited revision.

**Working-tree caveat:** the tree was modified by another process during this audit (`tenancy/tenancyrow/row.go` momentarily had `through.Apply` return `nil` unconditionally, then reverted; `go test ./tenancy/...` produced three different failure sets in three consecutive runs). Everything below was read and probed against these exact bytes:

```
779a259f20c26e31…  tenancy/tenancyrow/row.go              (269 lines)
e2d07a804174a76d…  crud/decorators/security/security.go   (1102 lines)
34636c8bb113285a…  crud/decorators/security/policies.go   (312 lines)
1ec0ee7f432608e7…  crud/options.go
77433ced1bb83a0e…  crud/scope.go
ffb694b10e797250…  crud/relation.go
b496c52a9e1fa4c3…  crud/sqlrepo/repository.go
```

## Project rules that override convention defaults (reported as rules, not defects)

| Rule | Where | Effect on this audit |
|---|---|---|
| "A relation the author did not declare a rule for" is out of scope; an undeclared relation is read whole, **deliberately** | `UC-004` "Out of scope"; `docs/modules/en/tenancy.md:180-188`; `row.go:43-49`; roadmap line 498 | The undeclared-preload leak is **not** reported as a defect. GAP-3 reports only the missing declaration-time completeness check. |
| `Tx` is inherited by [[D-030]]; an unscoped transaction opens and the first verb inside refuses | roadmap line 500 | GAP-9 reports only the *other* four empty-argument verbs. |
| A developer writing a raw SQL fragment is trusted input | `UC-004` "Out of scope" | GAP-1 is about a `crud.Option`, not a raw fragment; the exclusion does not cover it. |
| A pagination cursor minted under another tenant's scope is documented, not closed | roadmap line 500 | Not reported. |
| Comments only for genuinely complex 40+ line functions | `CLAUDE.md` | Comment findings limited to comments that are **false**, not to comment density. |

## Scorecard

| Law | Verdict | Evidence |
|---|---|---|
| Transaction ownership | **pass** | `saveTransaction` (`security.go:606-615`) joins an existing executor or opens one; repositories never open a tx of their own except `SaveScoped` when the dialect lacks `RETURNING` (`sqlrepo/repository.go:687-694`), which is documented atomicity, not a boundary grab. |
| Check-then-act / read-modify-write | **pass** | Every inspected write re-issues the inspection as a full-row snapshot predicate: `snapshotPredicate` (`security.go:713-731`) ANDs `Eq` for every column. Proven: `Update` emits `... AND "tenant_id"=$3 AND "id"=$4 AND "tenant_id"=$5 AND "number"=$6 AND "total"=$7`; `Delete`, `DeleteAll`, `UpdateAll`, `Restore` and `saveScopedUpdate` (`sqlrepo:943-958`) all do the same. This is optimistic CAS, correct under N replicas. |
| Read-your-writes / replica lag | **pass** | Every inspection read passes `crud.PrimaryOnly()` (`security.go:661, 846, 891, 1047, 1083`); `sqlrepo.read` honours it (`repository.go:110-118`). |
| Narrowing reaches every verb | **at risk** | 19/19 verbs narrow with default options (probe P9). But 11 verbs accept caller `crud.Option`s applied **after** the gate's own (`security.go:187`), and 5 of them then return foreign rows with `err=nil` (GAP-1). |
| `Through` ownership is exclusive | **fail** | `Through` accepts `has_many` and `many_to_many` and compiles to `EXISTS(≥1 child owned by me)` for reads **and** writes (GAP-2). |
| Ownership value typing | **fail** | `column` has no analogue of `reconcileFieldValue` (`policies.go:74-89`); 4 of 6 common owner column types are unusable and 2 of those fail with a **false** verdict (GAP-4, GAP-5). |
| Fail-closed on policy error | **at risk** | Fails closed, but unclassified: `Value`'s error text travels verbatim and maps to `KindInternal`, not `KindForbidden` (GAP-6). |
| Tautology guard | **pass** | `IsTautologyFor(meta, nil)=true`, `True()=true`; `Eq`/`IsNull`/undefined-`Opt` all `false` and none can be produced as a no-op narrowing by `tenancyrow` (probe P10). |
| Frozen / `Immutable` | **pass** | `index[M]` (`security.go:81-96`) and `DefinedFields` (`update.go:234-246`) both resolve to `Field.Name`; freezing holds for `Update`, `UpdateAll`, `Save`, `SaveOnly`, `SaveAll`, DTO-by-column and DTO-by-`Opt` (probe P5/P5d). |
| No external call inside the request/tx path | **fail** | With `Spec.Revalidate`, `Inspect` triggers one control-plane `Lookup` **per inspected row**: 101 for a 100-row `DeleteAll`, and inside an open `Tx` (GAP-7). |
| Bounded statements | **fail** | `deleteVictims` respects `crud.BindLimit` (`security.go:1030-1038`); `DeleteAll`/`UpdateAll` snapshot ORs do not (GAP-8). |
| Capability forwarding | **at risk** | `Create`/`Replace` silently disabled by the gate; fails closed (GAP-10). |
| "No verb runs without a verified scope" | **fail** | False for `Delete()`, `Restore()`, `InsertBatch(nil)`, `SaveAll(nil)` (GAP-9). |
| Universality (hardcode / fit-to-example) | **pass** | No domain literals, keyword lists, tuned regexes or sample paths in `tenancyrow/` or `security/`. `grep -n '"[^"]\{12,\}"' tenancy/tenancyrow/row.go crud/decorators/security/{security,policies}.go` returns only refusal messages and panic texts. The only implicit sample-fitting is the default `Value` assuming a `string` column (GAP-4). |

## Metrics

| Metric | Threshold | Actual | Worst offenders |
|---|---|---|---|
| Files > 400 lines in scope | 0 | 3 | `crud/sqlrepo/repository.go` (1920), `crud/decorators/security/security.go` (1102), `tenancy/tenancyrow/row_test.go` (416). `tenancyrow/row.go` = 269 ✔ |
| Functions > 50 lines | 0 | 4 | `gate.SaveAll` (90), `gate.save` (68), `gate.Delete` (61), `gate.Restore` (57) |
| Exported symbols in `tenancyrow` | ≤ 7 | 8 | `Mode, Ownership, Value, Relation, Column, Through, Policy, Repository` |
| Verbs accepting caller `crud.Option` | — | 11 | `grep -c 'func (this \*gate\[M, ID\]) [A-Z].*options \.\.\.crud.Option' security.go` → `11` |
| …of which leak silently on an Options-erasing option | 0 | **5** | `GetAll`, `Get`, `Count`, `Exists`, `Aggregate` (probe P1) |
| Owner Go types usable with the default `Value` | 6/6 | **1/6** | only `string`. `int64`, `*string`, `Opt[T]`, `sql.NullString`, `driver.Valuer` all fail (probe P6) |
| Owner types whose failure message is **false** | 0 | **2** | `sql.NullString`, `driver.Valuer` → "row is owned by a different tenant" for the caller's own row |
| Control-plane `Lookup`s per verb (`Revalidate`) | 1 (documented) | 1 / 2 / **101** | `GetAll`=1, `GetByID`=2, `Delete`/`DeleteAll`/`UpdateAll`/`InsertBatch` over 100 rows = **101** (probe P13) |
| Bind params, `DeleteAll` over 3000 rows | < 65535 | **12 001** (250 KB SQL) | fails at ~16 383 rows on a 4-column table, ~3 270 on a 20-column table (probe P11) |
| Verbs answering `nil` with **no** verified scope | 0 | **4** (+`Tx`, documented) | `Delete()`, `Restore()`, `InsertBatch(nil)`, `SaveAll(nil)` (probe P12) |
| `Through` tests covering a to-many | ≥1 | **0** | `grep -rn 'Through\[' tenancy/ test/` → one hit, `row_test.go:278`, a `belongs_to` |
| Tests covering an Options-mutating caller option through a gate | ≥1 | **0** | the pattern exists and is exercised in `crud/preload_edge_test.go:189,208` but never against `security.Gate` |

## The verb table — where the narrowing comes from, and what a conflicting caller option does

Probe P9 (`go test -run TestP9 -v`) exercised every verb against a `crudtest.Postgres()` recorder with a `Column[Invoice]("TenantID", Derive, nil)` policy. `NARROWED` = the emitted SQL carried `"tenant_id" = $n`.

| Verb | Narrowing path | Emitted SQL (abridged) | Caller can erase it? |
|---|---|---|---|
| `GetByID` | `loadScoped`→`writeScopes`→`Scope`, then `inspect(Read)` on the row | `... WHERE ("tenant_id"=$1 AND "id"=$2) LIMIT 1` | Yes, but `Inspect` catches it → `Denied` (403, not 404) |
| `Get` | `scoped()` (`security.go:175-188`) + narrowed page-total | `... WHERE "tenant_id"=$1 ORDER BY "id" LIMIT 20` and `SELECT count(*) ... WHERE "tenant_id"=$1` | **Yes — silent leak** |
| `Get` + cursor | same, cursor ANDed in `sqlrepo.findWithin` | `WHERE ("tenant_id"=$1 AND "id" > $2)` (probe P14) | **Yes — silent leak** |
| `GetAll` | `scoped()` | `... WHERE "tenant_id"=$1` | **Yes — silent leak** |
| `First` | `scoped()` + `inspect(Read)` (`security.go:323`) | `... WHERE "tenant_id"=$1 ORDER BY "id" LIMIT 1` | Yes, but `Inspect` catches it |
| `Aggregate` | `scoped()`; refuses only if `InspectReads` (`security.go:345`), which `tenancyrow` leaves false | `SELECT COUNT(*) FROM "invoices" WHERE "tenant_id"=$1` | **Yes — silent leak** |
| `Count` | `scoped()` | `SELECT count(*) ... WHERE "tenant_id"=$1` | **Yes — silent leak** |
| `Exists` | `scoped()` | `SELECT 1 ... WHERE "tenant_id"=$1 LIMIT 1` | **Yes — silent leak** |
| `ExistsUnscoped` | `writeScopes` then `ExistsUnscopedOf(this.Core, Where(scope), …)` — "unscoped" = caller's narrowing only, per [[D-115]] | `SELECT 1 ... WHERE "tenant_id"=$1 LIMIT 1` | Yes; leaks an existence bit |
| `InsertBatch` | refuses a scope-only policy (`security.go:412`); `inspect(Create)` per model → `Derive` stamps | `INSERT … VALUES ($1='acme-7f3c',…),($4='acme-7f3c',…)` | n/a (no `crud.Option`) |
| `SaveAll` (new) | `inspect(Create)` per model → stamped | same as above | n/a |
| `SaveAll` (assigned) | `saveTarget` scoped read → `checkImmutableSave` → `saveScopedOnly` with full snapshot guard, all inside `saveTransaction` | `UPDATE … WHERE ("tenant_id"=$4 AND "id"=$5 AND "tenant_id"=$6 AND "number"=$7 AND "total"=$8)` | n/a |
| `Save` (new) | `inspect(Create)` | `INSERT … RETURNING …` with `tenant_id` stamped | n/a |
| `Save` (assigned) | `saveTarget` + `ExistsUnscopedOf` probe (`security.go:676-685`) → 404 if hidden; `SaveScoped` snapshot CAS | scoped read then scoped `UPDATE … RETURNING` | n/a |
| `SaveOnly` | as `Save`, via `SaveScopedOnlyOf` | same without `RETURNING` | n/a |
| `Update` | immutability check on the DTO (`security.go:832-842`), `loadScopedWith(PrimaryOnly)`, snapshot, then `Where(scope)+rel+Where(inspected)` | `UPDATE … WHERE ("id"=$2 AND "tenant_id"=$3 AND "id"=$4 AND "tenant_id"=$5 AND "number"=$6 AND "total"=$7)` | Yes — but `Inspect` on the pre-read catches a foreign row |
| `UpdateAll` | immutability check + `scoped()` + tautology guard + snapshot OR | `UPDATE … WHERE ("tenant_id"=$2 AND ("id"=$3 AND … ))` | Yes — but the snapshot OR bounds the blast radius to inspected rows (probe P1b) |
| `Delete` | `deleteVictims` (chunked, `PrimaryOnly`) → per-victim `Inspect` → `DeleteScoped{Scope, Snapshots}` | `DELETE … WHERE ("tenant_id"=$1 AND "id"=$2 AND ("id"=$3 AND "tenant_id"=$4 …))` | n/a |
| `DeleteAll` | `scoped()` + tautology guard + snapshot OR | `DELETE … WHERE ("tenant_id"=$1 AND ("id"=$2 AND …))` | Yes — snapshot-bounded |
| `DeleteAll` (soft) | `sqlrepo.stamp` | `UPDATE "softs" SET "deleted_at"=$1 WHERE ("deleted_at" IS NULL AND "tenant_id"=$2 AND (…))` | Yes — snapshot-bounded |
| `Restore` (tombstone) | `LoadTombstonesOf(ids, scope, rel)` → `Inspect(Restore)` → `RestoreScoped{Scope, Snapshots}` | `UPDATE "softs" SET "deleted_at"=NULL WHERE ("deleted_at" IS NOT NULL AND "tenant_id"=$1 AND "id"=$2 AND (…))` | n/a |
| `Restore` (no tombstone) | `RestoreOf` → `ErrNoTombstone` | — | n/a |
| `Tx` | **not overridden** — inherited from the embedded `crud.Core`; verbs inside re-derive their own scope | — | documented, roadmap line 500 |
| `Create` (optional) | **not forwarded** — `ErrNoCreateSupport` | — | GAP-10 |
| `Replace` (optional) | **not forwarded** — `ErrNoReplaceSupport` | — | GAP-10 |
| `SaveScoped` / `SaveScopedOnly` / `DeleteScoped` / `RestoreScoped` / `LoadTombstones` (gate-over-gate) | **not implemented by the gate** → an outer gate gets `Denied("the storage core cannot perform a scoped upsert atomically")` | — | documented debt |

**Answer to the audit question:** with default options, a request bound to tenant A cannot read, count, infer, modify, delete or create a row belonging to tenant B **via `Column` ownership**. It can via three routes: an Options-erasing caller option (GAP-1, 5 verbs silently), a `Through` policy over a to-many relation (GAP-2, every verb), and a `Value` func that answers `nil` (GAP-5). Creates through `Through` are impossible at all (GAP-11).

---

## Findings

### GAP-1 [high][immediate] The gate's narrowing is a `crud.Option`, and any caller-supplied `crud.Option` applied afterwards erases it

- **Where:** `crud/decorators/security/security.go:187` (`return append([]crud.Option{crud.Where(p), rel}, options...)`), `:259-263`, `:390`, `:859`, `:883`; `crud/options.go:5-31` (all `Options` fields exported), `:43` (`type Option func(*Options)`); `crud/optiongroup.go:64-75` (`refused()` skips zero-valued fields, so an erased `Filter` is never rejected).
- **Scale:** systemic — 11 verbs (`grep -c 'func (this \*gate\[M, ID\]) [A-Z].*options \.\.\.crud.Option' crud/decorators/security/security.go` → `11`); 5 of them leak with `err=nil`.
- **Confidence:** CONFIRMED. Probe:
  ```go
  func erase() crud.Option { return func(o *crud.Options) { o.Filter = nil; o.RelScopes = nil } }
  ```
  ```
  GetAll    SQL: SELECT "id","tenant_id","number","total" FROM "invoices"        err=<nil>
  Count     SQL: SELECT count(*) FROM "invoices"                                 err=<nil>
  Exists    SQL: SELECT 1 FROM "invoices" LIMIT 1                                err=<nil>
  Get       SQL: SELECT ... FROM "invoices" ORDER BY "id" ASC LIMIT 20           err=<nil>
  Aggregate SQL: SELECT COUNT(*) FROM "invoices"                                 err=<nil>
  GetByID   SQL: SELECT ... FROM "invoices" LIMIT 1   err=security: forbidden: read: row is owned by a different tenant
  ```
- **What / Why this severity:** `repo.GetAll(ctx, myOption)` where `myOption` assigns rather than appends to `o.Filter` returns **every tenant's rows** with no error. The `Where`/`NarrowRelations` constructors append (`options.go:70-84`), but the type they produce is a public `func(*Options)` over a struct with 17 exported fields, and the repository's own tests use exactly this shape to *replace* a filter (`crud/preload_edge_test.go:189-190`, `:207-209`, named `replaceFilter`). The gate applies its narrowing **first** and the caller's options **last**, so last write wins. `GetByID` also loses its `id = $1` predicate and returns an arbitrary row. `DeleteAll`/`UpdateAll` are saved only by the snapshot OR (probe P1b: `DELETE ... WHERE ("id"=$1 AND "tenant_id"=$2 AND "number"=$3 AND "total"=$4)`), i.e. by a mechanism that exists for a different reason.
- **Reachability, stated honestly:** **not** remotely reachable. `query.Request.Compile` (`crud/query/compile.go:314-620`) only ever emits `crud.Where`, `OrderBy`, `Preload`, `Select`, `Limit`, `After`… — never a raw `Option`. This is an application-code hazard, not an unauthenticated one. `UC-004`'s out-of-scope list covers "a developer writing a raw SQL fragment"; it does not cover a raw `crud.Option`, and the erasure is not a predicate the closed AST can build — it is a mutation of the option struct.
- **Why this timing:** it is the shape of the seam's public contract. Closing it later means changing `crud.Options`/`crud.Option` (unexporting fields, or having the gate re-assert its predicate after building the caller's options), which every decorator and every consumer sees.
- **Close criteria:**
  - [ ] The gate re-asserts its scope after the caller's options are applied (e.g. build the caller's `Options` first, then `Where(scope)` last), for all 11 verbs.
  - [ ] A test per verb: a caller `crud.Option` that sets `o.Filter = nil` and `o.RelScopes = nil` still produces a statement containing the tenant predicate.
  - [ ] `security.Gate` documents whether an Options-mutating option is trusted input, and `UC-004` states it either way.

### GAP-2 [critical][immediate] `Through` accepts a to-many relation and compiles ownership to "at least one child is mine" — for reads *and* writes

- **Where:** `tenancy/tenancyrow/row.go:133-142` (the only guard is `local == ""`), `:151-157` (`Narrow` = `Eq(path+"."+field, v)`), `:169-176` (`Frozen` returns `this.local`), `:191-214` (`resolveRelation` returns the **first** segment's `LocalField` and never inspects `Kind`); `crud/relation.go:511-525` (`HasOne`/`HasMany`/`ManyToMany` set `LocalField = cmpOr(ref, "")`, so a `ref=` tag makes it non-empty); `crud/predicate.go:90-111` (a hop renders as `EXISTS (SELECT 1 …)`).
- **Scale:** local to `Through`, but it is one of only two shipped `Ownership` strategies and it affects every verb.
- **Confidence:** CONFIRMED. Probe P2/P2c/P2d against the real packages:
  ```go
  type Folder struct { ID int64 `db:"id,pk,auto"`; Title string `db:"title"`
                       Items []FolderItem `rel:"has_many,fk=FolderID,ref=ID"` }
  own := tenancyrow.Through[Folder]("Items", "TenantID", nil)   // accepted, no panic
  ```
  ```
  Folder.Items kind=has_many LocalField="ID"
  Through accepted. Frozen()=[ID]                       <-- the model's own primary key

  GetAll:    SELECT "id","title" FROM "folders" WHERE EXISTS (SELECT 1 FROM "folder_items" AS rx1
             WHERE rx1."folder_id" = "folders"."id" AND rx1."tenant_id" = $1 AND rx1."tenant_id" = $2)
  Count:     SELECT count(*) FROM "folders" WHERE EXISTS (... same ...)
  DeleteAll: DELETE FROM "folders" WHERE (EXISTS (... same ...) AND ("id"=$3 AND "title"=$4))
  Delete:    DELETE FROM "folders" WHERE (EXISTS (... same ...) AND "id"=$3 AND (...))
  Update:    UPDATE "folders" SET "title"=$1 WHERE ("id"=$2 AND EXISTS (... same ...) AND ...)
  ```
  many-to-many is identical:
  ```go
  Tags []Tag `rel:"many_to_many,join=post_tags,ref=ID"`
  Post.Tags kind=many_to_many LocalField="ID"; Through accepted, Frozen()=[ID]
  SELECT "id","title" FROM "posts" WHERE EXISTS (SELECT 1 FROM "tags" AS rx1
    JOIN "post_tags" AS rx2 ON rx2."tag_id" = rx1."id"
    WHERE rx2."post_id" = "posts"."id" AND rx1."tenant_id" = $1 AND rx1."tenant_id" = $2)
  ```
- **What / Why this severity:** concrete failure. A `posts` row is tagged with tag `T_A` (tenant A) and tag `T_B` (tenant B) — an ordinary state, since neither tenant's gate freezes the join table and each is allowed to attach its **own** tag. `EXISTS(tag owned by A)` is true, so tenant A reads, counts, updates and deletes that post. `EXISTS(tag owned by B)` is also true, so tenant B does too. Both see and destroy the same row. `Frozen()=[ID]` freezes the primary key — precisely the "nothing freezes the link that carries ownership" condition the panic at `row.go:139` exists to prevent, and the panic does not fire because `resolveDefaults` semantics make `LocalField` non-empty the moment a `ref=` tag is present. The docs never state a cardinality constraint: `docs/modules/en/tenancy.md:160` says only "a dotted path into the owner … the key that points at the owner".
- **Secondary, same site:** `Narrow` and `Relations` both produce the same predicate and both land inside the same `EXISTS` (via `writer.hopScope`, `predicate.go:114-125`), so every statement binds the tenant twice — `rx1."tenant_id" = $1 AND rx1."tenant_id" = $2`. Wasted binds against the budget in GAP-8, and a signal that the two are not meant to compose.
- **Related, non-blocking:** `resolveRelation` reads `relation.LocalField` from the **cached** `*Schema` without calling `resolveDefaults()` (`relation.go:532-547`). I verified that `Meta.Relation` binds a per-`Meta` copy (`relation.go:178-202`), so this particular read is currently stable — but `bind` returns the shared declaration when `relationContext` is nil, and `CLAUDE.md` records a past race in exactly this function. `Through`'s only safety guard therefore depends on a field of process-global mutable state.
- **Why this timing:** it is a wiring-time acceptance decision on a public constructor. Any deployment that already wired `Through` over a to-many is silently sharing rows now, and the fix (refuse `Kind.ToMany()`, or require the frozen key to be a foreign key rather than the PK) changes what compiles.
- **Close criteria:**
  - [ ] `Through[M]` panics at wiring when `relation.Kind.ToMany()` — with a message naming the EXISTS semantics.
  - [ ] `Through[M]` panics when the resolved `local` equals `Owner.PK.Name`, i.e. when the "key that points at the owner" is the model's own identity.
  - [ ] `resolveRelation` calls `Resolve()` (or `resolveDefaults`) explicitly rather than reading a lazily-populated field.
  - [ ] Tests: `Through` over `has_many`, `has_many,ref=…`, `has_one` and `many_to_many` are all refused; the existing `belongs_to` case still passes.
  - [ ] `Narrow` and `Relations` do not both apply for `through` (one bind, not two).

### GAP-3 [high][immediate] `Column` never checks that the model's relations were declared, so the documented mitigation is unenforced

- **Where:** `tenancy/tenancyrow/row.go:50-62` — `Column` validates each supplied `Relation` (`resolveRelation`) but never consults `schema.Relations` for the ones **not** supplied.
- **Scale:** systemic — every `Column` wiring over a model with relations. `tenancy/tenancyrow/row_test.go:35` and `_examples/tenancy-sharedrow/main.go` are the only two wirings in the repo; the test one declares none while `Invoice` has `Lines`.
- **Confidence:** CONFIRMED, remotely reachable. Probe P3c used a real `?preload=Lines` query string:
  ```
  compiled 2 options from ?preload=Lines with a nil Config
  GetAll err=<nil>
  SQL: SELECT "id","tenant_id","number","total" FROM "invoices" WHERE "tenant_id" = $1 ORDER BY "id" ASC LIMIT 100
  SQL: SELECT "id","invoice_id","tenant_id","text" FROM "lines" WHERE "invoice_id" IN ($1) LIMIT 1001
  LEAK: caller bound to acme-7f3c received line 9 owned by "other-tenant"
  ```
  The second statement carries no tenant predicate. Declaring the relation fixes it (probe P3b: `… AND "tenant_id" = $2`). `crud/query/compile.go:257-260` — `allowed(nil, x)` returns `true`, so a nil `query.Config` preloads everything.
- **What / Why this severity:** **the leak itself is a documented, deliberate contract** — `UC-004` "Out of scope", `docs/modules/en/tenancy.md:180-188`, `row.go:43-49`, roadmap line 498, and `test/integration/gate_relscope_test.go:81` keeps a "not declared" control that asserts the leak *is* there. I am **not** reporting the leak. What I am reporting is that the mitigation is an opt-in list with no completeness check, in a constructor whose own standard is to panic at wiring for an unknown field (`row.go:53`), an unknown relation (`row.go:196`) and a typo'd path (`row.go:208`). The schema knows the full relation set; nothing compares it to the declared set. The failure mode is silent and remote: an author adds a `has_many` to a tenanted model six months later and the `Column` call keeps compiling.
- **Why this timing:** universality law — a mechanism whose correctness depends on a human remembering to enumerate is not a mechanism. It is also cheap now and a breaking constructor change later.
- **Close criteria:**
  - [ ] `Column[M]` panics at wiring when `schema.Relations` contains a relation not covered by a `Relation{Path,…}` declaration or by an explicit acknowledgement (e.g. `tenancyrow.Unnarrowed("Lines")`).
  - [ ] The acknowledgement form exists and is documented alongside `Relation`.
  - [ ] A test: adding a relation to a tenanted model without touching the `Column` call fails the build/test, not production.

### GAP-4 [high][immediate] `tenancyrow.Column` performs no type reconciliation between the ownership value and the owner column, unlike `security.ScopeField`

- **Where:** `tenancy/tenancyrow/row.go:72-78` (`Narrow` passes the raw `any` straight to `crud.Eq`), `:99-116` (`Apply` compares with `crud.EqualValues`), `:118-131` (`assign` accepts anything `ConvertibleTo`). Compare `crud/decorators/security/policies.go:74-89` — `reconcileFieldValue` converts to `crud.ElemType(f.Type)` at policy-build time and denies otherwise, plus `safelyConvert` (`:91-140`) with overflow and float guards.
- **Scale:** systemic — affects every owner column whose Go type is not exactly the type `Value` returns.
- **Confidence:** CONFIRMED. Probe P6 over six owner types with the default `Value` (`reference.Value()`, a `string` — `tenancy/reference.go:23`):
  ```
  int64 column, default Value:
      GetAll  SQL: ... WHERE "tenant_id" = $1   args=[acme-7f3c]      <-- a string bound to a bigint
      Save    err=crud: .int64: an ownership value of type string cannot be stored in it
  *string column, default Value:
      Save    err=crud: .*string: an ownership value of type string cannot be stored in it
  Opt[string] column, default Value:
      Save    err=crud: .utils.Opt[string]: an ownership value of type string cannot be stored in it
      Save (owner supplied by caller)  err=<nil>                       <-- Validate works, Derive never does
  sql.NullString column, owner supplied CORRECTLY:
      Save    err=security: forbidden: create: row is owned by a different tenant   <-- FALSE
  driver.Valuer (type TID string) column, owner supplied CORRECTLY:
      Save    err=security: forbidden: create: row is owned by a different tenant   <-- FALSE
  ```
  And probe P7a, the asymmetric case (`Value` returns `int`, column is `int64`):
  ```
  GetAll  -> narrowed "tenant_id" = 7,   err=<nil>
  Save    -> INSERT ... VALUES (7, ...), err=<nil>
  Update of MY OWN row -> security: forbidden: update: row is owned by a different tenant
  ```
- **What / Why this severity:** no leak — every path fails closed — but the failure is late, inconsistent and mislabelled. Concrete: a deployment with `tenant_id BIGINT` and `Value` returning `int` (not `int64`) reads correctly, creates correctly, and refuses **every update and delete of its own rows** with a message that says the row belongs to another tenant. With `sql.NullString` or a `driver.Valuer` owner column the resource is unusable and the same false verdict is emitted. `crud.ElemValue` (`crud/access.go:99-117`) only unwraps `utils.Opt` and pointers; `crud.EqualValues` (`crud/update.go:292-309`) requires identical dynamic types (`ta != tb → false`). The gate's sibling constructor already solves this at policy-build time.
- **Why this timing:** it is a public contract defect on `tenancyrow.Value` and it produces verdicts without provenance (`restrictions.md`). Adding reconciliation later changes which wirings are legal.
- **Close criteria:**
  - [ ] `Column[M]` builds a reconciler from `crud.ElemType(owner.Type)` (reuse `security`'s, or lift it into `crud`) and applies it in `Narrow`, `Relations` and `Apply`.
  - [ ] A value that cannot be reconciled is refused with a message naming the type mismatch, never "owned by a different tenant".
  - [ ] `Derive` works for `*T` and `Opt[T]` owner columns, or `Column` refuses those column types at wiring.
  - [ ] Test matrix over `string`, `int64`, `*string`, `Opt[string]`, `sql.NullString`, a `driver.Valuer` type and `uuid`-shaped `[16]byte`, for `Narrow`, create, update and delete.

### GAP-5 [high][immediate] A `Value` that answers `nil` narrows to `IS NULL` instead of refusing, granting read+write over every un-owned row

- **Where:** `tenancy/tenancyrow/row.go:72-77` → `crud.Eq(field, nil)` → `nullNode` (`crud/predicate.go:406-408`); `row.go:109` — `EqualValues(nil, nil)` is `true` (`crud/update.go:293-294`), so `Apply` **permits**. Compare `crud/decorators/security/policies.go:77-79`: `if v == nil { return nil, Denied(Read, f.Name+" extractor answered no value") }`.
- **Scale:** systemic — three distinct `Value` returns hit it.
- **Confidence:** CONFIRMED. Probe P7c and P6:
  ```
  Value returns nil                 -> SELECT ... WHERE "tenant_id" IS NULL   err=<nil>
  Value returns crud.Null[string]() -> SELECT ... WHERE "tenant_id" IS NULL   err=<nil>
  Value returns (*string)(nil)      -> SELECT ... WHERE "tenant_id" IS NULL   err=<nil>
  Value returns crud.Undefined[string]() -> crud: Invoice.TenantID: an undefined Opt is not a comparison value  (fails closed ✔)

  and the write path, *string owner column, Value returns nil:
    SELECT ... FROM "ptr_owned" WHERE ("tenant_id" IS NULL AND "id" = $1) LIMIT 1
    UPDATE "ptr_owned" SET "tenant_id" = NULL, "name" = $2
      WHERE ("tenant_id" IS NULL AND "id" = $3 AND "tenant_id" IS NULL AND "name" = $4)   err=<nil>
  ```
- **What / Why this severity:** an ownership resolver of the ordinary shape `func(r tenancy.Reference) (any, error) { return directory[r.Value()], nil }` returns a nil interface for a tenant the directory has not loaded yet. Every such caller then shares one universe: they read, update and delete every row whose `tenant_id` is `NULL` — the "global"/"unassigned" rows that shared-row schemas routinely carry (templates, defaults, pre-migration data). The tautology guard cannot see this: `IsTautologyFor(meta, IsNull(f))` is `false` (probe P10), so the gate approves. `security.ScopeField` refuses exactly this input.
- **Why this timing:** hardcode/fail-closed class — `universality.md` says a step whose behaviour on a neighbouring input is neither handled nor loud is a gap, minimum `[high][immediate]`.
- **Close criteria:**
  - [ ] `column.Narrow`/`Relations`/`Apply` refuse a `nil`/`Opt`-null/typed-nil ownership value with a typed refusal.
  - [ ] `Ownership` documents that "no value" is a refusal, not a narrowing.
  - [ ] Tests for all four "no value" spellings across a read, a create and an update.

### GAP-6 [high][immediate] Errors from `Value`/`Narrow`/`Relations`/`Apply` are not `tenancy.Classify`-d — the text travels and the kind is wrong

- **Where:** `tenancy/tenancyrow/row.go:73-75, 85-87, 100-103, 153-155, 161-163, 226` — every `return err` is verbatim. `tenancy/errors.go:122-140` — `Classify` exists precisely for this and its comment says "**Every seam that calls application-supplied code — a resolver, a source factory, a fence — answers through this**". `tenancy/authority.go:85, 96` and `tenancy/context.go:62` apply it to the `Resolver`; nothing applies it to `Value`.
- **Scale:** systemic — 6 return sites in `row.go`, reachable from all 19 verbs.
- **Confidence:** CONFIRMED. Probe P7b:
  ```go
  boom := errors.New("tenant directory: no shard mapping for tenant acme-7f3c (db=pg-eu-3, user=svc_acme)")
  ```
  ```
  GetAll err = tenant directory: no shard mapping for tenant acme-7f3c (db=pg-eu-3, user=svc_acme)
  errors.Is(err, crud.ErrForbidden)      = false
  errors.Is(err, tenancy.ErrUnavailable) = false
  errors.Is(err, boom)                   = true
  ```
- **What / Why this severity:** two failures. (1) The `Value` callback is handed the `tenancy.Reference` and is the natural place for a directory lookup; its error names the tenant, the database and the credential and travels unaltered to every in-process caller and every log line. `tenancy.Reference` itself is redacted with care (`reference.go:25-35`: `String()` returns `[tenant reference]`, `MarshalJSON` refuses) — the callback that receives it has no such discipline. The privacy canary `TestAResolverFailureNeverTravelsBackAsText` (`tenancy/scope_test.go:144-174`) covers only `Authority.Verify`, yet roadmap gate 8 (line 513) records the property as "**met**" for the subsystem. (2) The error is neither `crud.ErrForbidden` nor a `tenancy` sentinel, so `port.KindOf` → `sentinelKind` → `errs.KindInternal` → HTTP 500. A tenant whose ownership value cannot be resolved gets an internal error instead of the documented refusal, and any caller distinguishing "denied" from "broken" by `errors.Is(err, crud.ErrForbidden)` gets the wrong answer.
- **Why this timing:** it is the error contract of a public seam; changing it later changes what consumers match on.
- **Close criteria:**
  - [ ] `tenancyrow.Policy` wraps every `ownership.*` call in `tenancy.Classify`.
  - [ ] A canary test mirroring `TestAResolverFailureNeverTravelsBackAsText`, driven through `tenancyrow.Column` with a leaking `Value`, asserting the four sentinels do not appear and that the refusal `errors.Is` a tenancy sentinel.
  - [ ] Roadmap gate 8 cites both tests or is reopened.

### GAP-7 [high][immediate] With `Spec.Revalidate`, the control plane is consulted **once per inspected row**, including inside an open transaction

- **Where:** `tenancy/tenancyrow/row.go:229-234` — `Inspect` calls `authority.Scope(ctx, classOf(action))` on **every invocation**; `tenancy/context.go:54-57` → `current()` → `resolver.Lookup`. The gate calls `Inspect` per row: `security.go:418-421` (`InsertBatch`), `:496` (`SaveAll`), `:902-906` (`UpdateAll`), `:942-956` (`Delete`), `:1094-1098` (`DeleteAll`), `:333-337` (`inspectAll`).
- **Scale:** systemic — 6 per-row loops.
- **Confidence:** CONFIRMED. Probe P13 with a counting `Resolver` and `Spec{Revalidate: true}`:
  ```
  GetAll (1 row)            control-plane Lookup calls during the verb: 1
  Count                     control-plane Lookup calls during the verb: 1
  GetByID                   control-plane Lookup calls during the verb: 2
  DeleteAll (100 victims)   control-plane Lookup calls during the verb: 101
  UpdateAll (100 victims)   control-plane Lookup calls during the verb: 101
  InsertBatch (100 models)  control-plane Lookup calls during the verb: 101
  Delete (100 ids)          control-plane Lookup calls during the verb: 101
  Tx{GetAll,GetAll}         control-plane Lookup calls during the verb: 2   (both inside the open tx)
  ```
- **What / Why this severity:** `data-integrity.md` forbids external calls inside a transaction outright, and `gate.SaveAll` (`security.go:501-516`) explicitly runs its per-model work inside `saveTransaction`. A `Resolver` that reaches a control-plane database or HTTP service therefore performs N round trips while holding row locks; a slow control plane converts a bulk delete into a long-held lock and a lock-wait cascade across replicas. Independently, `tenancy/context.go:34-38` documents the cost as "**once per statement**" — the measured cost is once per statement plus once per row, so an operator sizing the control plane from the documentation under-provisions by the batch size.
- **Why this timing:** it changes the shape of `Policy` (the scope must be resolved once per verb and threaded, not re-resolved per row), which is a public contract of the `Ownership` seam.
- **Close criteria:**
  - [ ] The verified scope is resolved once per verb and reused by `Inspect` (memoised on the request context, or `Inspect` receives the already-verified `Scope`).
  - [ ] A test asserting `Lookup` count == 1 for a 100-row `DeleteAll`/`InsertBatch`/`UpdateAll`/`Delete`.
  - [ ] `context.go:34-38` and the module docs state the real per-verb cost.

### GAP-8 [medium][immediate] `UpdateAll`/`DeleteAll` build one snapshot predicate per victim with no bind budget, while `deleteVictims` in the same file has one

- **Where:** `crud/decorators/security/security.go:898` and `:1090` (`snapshotPredicates` → one `Or` of full-row `And`s, unchunked) versus `:1030-1038` (`deleteVictims` computes `chunk = min(len(ids), crud.BindLimit(source.Dialect()), 4096)`).
- **Scale:** local — 2 sites.
- **Confidence:** CONFIRMED. Probe P11:
  ```
  DeleteAll over 3000 rows
  final statement: 12001 bind parameters, 249948 bytes of SQL   (PostgreSQL caps a statement at 65535 parameters)
  head: DELETE FROM "invoices" WHERE ("tenant_id" = $1 AND (("id" = $2 AND "tenant_id" = $3 AND "number" = $4 AND "total" = $5) OR ("id" = $6 AND ...
  occurrences of "tenant_id" in the DELETE: 3001
  ```
- **What / Why this severity:** a 4-column table exceeds PostgreSQL's 65 535-parameter limit at 16 383 rows; a 20-column table at ~3 270. Tenant offboarding (`DeleteAll` scoped to one tenant) and bulk `UpdateAll` therefore fail at production sizes with a driver error, and the failure appears only above a data-volume threshold nobody hits in tests. It fails closed, hence `[medium]`, but the inconsistency with `deleteVictims` twenty lines away means the author already knew the constraint.
- **Why this timing:** it will be re-discovered as a production incident and the fix (chunked, transactional multi-statement writes) changes the return semantics of `UpdateAll`/`DeleteAll` (`int64` affected across chunks).
- **Close criteria:**
  - [ ] `UpdateAll` and `DeleteAll` chunk the snapshot OR by `crud.BindLimit(source.Dialect())`, running the chunks in one transaction and summing `RowsAffected`.
  - [ ] A test at a row count that would exceed the dialect's bind limit.

### GAP-9 [medium][immediate] Four verbs answer `nil` with no verified tenant scope, contradicting the invariant and `Delete`'s own written rule

- **Where:** `crud/decorators/security/security.go:920-923` — `Delete` authorizes first (per the comment at `:913-916`) but takes the `len(ids)==0` shortcut **before** `writeScopes`; `:974-979` — `Restore` takes the shortcut **before** `authorize` as well, inverting the rule the comment states; `:398-400` (`InsertBatch`), `:431-433` (`SaveAll`).
- **Scale:** systemic — 4 sites (`Tx` is a fifth, documented at roadmap line 500 and excluded).
- **Confidence:** CONFIRMED. Probe P12 with `ctx = context.Background()` (no scope at all):
  ```
  Delete()      no ids     err=<nil>   ErrNoScope=false  statements=0
  Restore()     no ids     err=<nil>   ErrNoScope=false  statements=0
  InsertBatch() no models  err=<nil>   ErrNoScope=false  statements=0
  SaveAll()     no models  err=<nil>   ErrNoScope=false  statements=0
  Restore(1)               err=tenancy: no verified tenant scope: forbidden   ErrNoScope=true
  ```
- **What / Why this severity:** no statement reaches the database, so no leak. But `tenancy/tenancyrow/row_test.go:140` pins "no verb runs without a verified scope" and covers 15 verbs; the four above are outside it and the invariant is false for them. The comment at `security.go:913-916` states the reason precisely — "answering it 0 rows and no error told an anonymous caller the route was theirs to call" — and then applies it only to `Authorize`, which `tenancyrow.Policy` never sets. So for the exact policy shape this extension produces, the guard the comment describes does not run.
- **Why this timing:** it is a stated invariant that is measurably false; it is cheap now and it is the kind of hole a later refactor widens.
- **Close criteria:**
  - [ ] `Delete`, `Restore`, `InsertBatch` and `SaveAll` resolve `writeScopes` before their empty-argument shortcut.
  - [ ] `Restore` authorizes before its shortcut, like `Delete`.
  - [ ] `TestNoVerbRunsWithoutAVerifiedScope` covers all 19 verbs plus the empty-argument spellings, with `ExistsUnscoped`.

### GAP-10 [medium][immediate] The gate silently withdraws `Create` and `Replace`

- **Where:** `crud/decorators/security/security.go:98-102` — `gate` embeds `crud.Core[M,ID]`, which declares neither `Create` nor `Replace`, and the gate implements neither. `crud/write.go:13-29` — `CreateOf`/`ReplaceOf` do a bare type assertion on the outer core. `crud/sqlrepo/repository.go:625, 629` implements both.
- **Scale:** local — 2 capabilities (plus the already-documented `SaveScoped`/`DeleteScoped`/`RestoreScoped`/`LoadTombstones` gate-over-gate family).
- **Confidence:** CONFIRMED. Probe P9:
  ```
  Create (optional)   err=crud: repository has no insert-only create capability
  Replace (optional)  err=crud: repository has no version-aware replace capability
  ```
  Without the gate, the same blueprint supports both.
- **What / Why this severity:** fails closed, so no leak; but adding tenancy to a repository removes two verbs a consumer may already use, with an error message that says the *repository* lacks the capability when in fact the *decorator* dropped it. This is the failure class [[D-061]] ("a wrapper forwards what it wraps") was written for.
- **Close criteria:**
  - [ ] The gate implements `Create` and `Replace` with the same authorize+inspect+scope treatment as `Save`, or declares in `docs/ai/decisions/` that it deliberately withdraws them and why.
  - [ ] A test asserting the chosen behaviour for both.

### GAP-11 [medium][immediate] A `Through`-owned resource can never be created, and the documented escape hatch does not work

- **Where:** `tenancy/tenancyrow/row.go:178-189` — the comment says "An application that can check the owner supplies its own `Inspect` and composes the two policies with `security.Combine`"; `crud/decorators/security/policies.go:271-280` — `Combine` builds `out.Inspect` as a loop that returns the **first** error, i.e. a logical AND. Roadmap line 500 repeats the claim: "gate-over-gate assigned-key `Save`, for which `security.Combine` is the supported composition".
- **Scale:** local — 1 comment, 1 roadmap line, but it is the documented remedy for a hard refusal.
- **Confidence:** CONFIRMED. Probe P8:
  ```go
  combined := security.Combine(
      tenancyrow.Policy[Folder, int64](a, tenancyrow.Through[Folder]("Items","TenantID",nil)),
      security.Policy[Folder,int64]{Inspect: func(...) error { return nil }},  // "I checked the owner"
  )
  repo.Save(ctx, &Folder{Title: "new"})
  ```
  ```
  err=security: forbidden: create: ownership is held through a relation, so a create cannot be judged from the row alone
  ```
- **What / Why this severity:** `Save`, `SaveOnly`, `SaveAll` and `InsertBatch` of a new row are all unreachable for a `Through`-owned model, with no supported way to enable them. The documentation names a composition that cannot work by construction. A consumer following the docs writes the extra `Inspect`, ships, and discovers at runtime that creates are still refused.
- **Why this timing:** a false statement in a decision-adjacent comment is treated as a failing test by `CLAUDE.md`.
- **Close criteria:**
  - [ ] Either `Combine` gains an explicit override form for `Inspect` (e.g. a policy that *replaces* rather than conjoins), or `row.go:178-189` and the roadmap are corrected to say a `Through` create is unsupported and name the real alternative (a separate ungated create path, or a custom `Ownership`).
  - [ ] A test pinning whichever answer is chosen.

### GAP-12 [medium][deferred] `Through` at depth ≥ 2 freezes the first hop's key, and calls it "the key that points at the owner"

- **Where:** `tenancy/tenancyrow/row.go:191-214` — `resolveRelation` keeps only the first segment's `LocalField` (`if local == "" { local = relation.LocalField }`), with no comment saying why; `row.go:169-176` — the `Frozen` comment; `docs/modules/en/tenancy.md:161`.
- **Scale:** local.
- **Confidence:** CONFIRMED. Probe P4a:
  ```
  Through[Ticket]("Team.Org","TenantID").Frozen() = [TeamID]
  SELECT ... FROM "tickets" WHERE EXISTS (SELECT 1 FROM "teams" AS rx1 WHERE rx1."id" = "tickets"."team_id"
    AND EXISTS (SELECT 1 FROM "orgs" AS rx2 WHERE rx2."id" = rx1."org_id" AND rx2."tenant_id" = $1 AND rx2."tenant_id" = $2))
  ```
- **What:** the narrowing is exact (nested `EXISTS` over `belongs_to` hops — correct). But the frozen key is `Ticket.TeamID`, which points at a `Team`, not at the owner; the ownership link `Team.OrgID` lives on another model and this policy cannot freeze it. The comment's claim ("repointing it is how a row leaves one tenant for another") is only true at depth 1. At depth 2 a ticket changes tenant when the *team* is repointed, which this policy does not and cannot guard.
- **Close criteria:**
  - [ ] The comment and `docs/modules/en/tenancy.md:161` state that only the first hop is frozen and that the intermediate models need their own policies.
  - [ ] `Through` with a multi-segment path either warns/refuses, or the documentation names the required companion policy.

### GAP-13 [medium][deferred] `column.Relations` binds one ownership value to every declared `Relation.Field`, whatever that field is

- **Where:** `tenancy/tenancyrow/row.go:84-92` — one `value` is computed and reused for every `Relation`; `row.go:57-59` — `resolveRelation` checks only that the field *exists* on the target.
- **Scale:** local.
- **Confidence:** CONFIRMED. Probe P12b declared `{Path:"Team.Org", Field:"TenantID"}` and `{Path:"Team.Org", Field:"Name"}`:
  ```
  SELECT "id","tenant_id","name" FROM "orgs" WHERE "id" IN ($1) AND ("tenant_id" = $2 AND "name" = $3)
  args=[9 5 5]        <-- the same value 5 bound to tenant_id and to name
  ```
- **What:** `AtPath` merge behaviour for a shared path is correct (AND — `crud/scope.go:14-24`), and `RelationScopes.Resolve` refuses a relation scope that does not narrow (`crud/scope.go:82-84`). But a typo in `Field` that happens to name another existing column produces a silently wrong narrowing that passes both checks; and where the related table's owner column has a different type than the root's, the same value is bound to both with no reconciliation (same root cause as GAP-4).
- **Close criteria:**
  - [ ] `Relation` declarations reconcile the ownership value against the *target* field's type, or accept a per-relation `Value`.
  - [ ] `resolveRelation` refuses a `Field` whose type cannot hold the ownership value.

---

## What is genuinely right, and should not be traded away in the fixes

Stated because "silence is not evidence":

- **Inspect→write is not a TOCTOU.** Every inspected write re-issues the inspection as a full-row snapshot predicate (`security.go:713-731, 804-814`), and `sqlrepo.scopedSaveGuard` (`repository.go:943-958`) does the same for `Save`. Verified in the SQL for `Update`, `Save`, `SaveOnly`, `SaveAll`, `Delete`, `DeleteAll`, `UpdateAll` and `Restore`. This is correct optimistic concurrency under N replicas with no lock and no `version` column required.
- **Every inspection read is a primary read** (`crud.PrimaryOnly()` at `security.go:661, 846, 891, 1047, 1083`), so replica lag cannot admit a stale ownership decision.
- **`saveTarget`'s 404-vs-403 handling** (`security.go:659-687`): a scoped read miss is disambiguated with an *unscoped* existence probe and answered `ErrNotFound`, so an assigned-key `Save` is not an enumeration oracle. Consistent with [[D-008]] and [[D-115]].
- **The tautology guard is exact and fails closed**: `IsTautologyFor(meta, nil) == true` (probe P10), so a policy whose `Scope` returns `nil, nil` is denied rather than run unscoped.
- **Cursor pagination and the page-total query both narrow** (probe P14): `WHERE ("tenant_id"=$1 AND "id" > $2)` and `SELECT count(*) … WHERE "tenant_id"=$1`.
- **Freezing is robust across DTO spellings** — Go field name, `db:` column tag, and `Opt[T]` all resolve to the same `Field.Name` through `Schema.Field`'s name→column→fold chain (`meta.go:110-121`), and `DefinedFields` reports `Target.Name` (`update.go:242`). Probe P5d confirmed all three are refused.
- **`tenancyrow` holds no mutable state**, copies its `relations` slice defensively, and validates every name at wiring with a panic.

## Remediation order

Boundaries and contracts before internals; integrity before ergonomics.

1. **GAP-2 — `Through` cardinality guard.** S. Blast radius: `tenancyrow` only, but it invalidates any existing to-many `Through` wiring (which is the point). Do this first: it is the only confirmed silent cross-tenant **write**.
2. **GAP-5 — refuse a nil ownership value.** S. `row.go` only. Independent of everything else; do it with GAP-2.
3. **GAP-4 — type reconciliation in `Column`.** M. Depends on nothing, but GAP-13 and the false "owned by a different tenant" verdicts fold into it, so do it before touching `Apply` for any other reason. Blast radius: some currently-compiling wirings start failing loudly at wiring time — intended.
4. **GAP-6 — classify seam errors.** S. Must land with or after GAP-4/5, because those change *which* errors the seam produces.
5. **GAP-1 — make the narrowing un-erasable.** M/L. Touches `crud/decorators/security` (all 11 verbs) and possibly `crud.Options`/`crud.Option` visibility, so it is the widest blast radius in this list and must not be interleaved with the `tenancyrow` changes above.
6. **GAP-3 — declaration completeness for relations.** M. Needs a new public form (`Unnarrowed`), so it is a contract addition; schedule after GAP-1 so the two contract changes ship together.
7. **GAP-11 — fix the `Combine` claim (code or docs).** S. Decide before anyone builds on the documented composition.
8. **GAP-7 — resolve the scope once per verb.** M. Changes the `Ownership`/`Policy` calling convention; do after GAP-2/4/5 have settled `Apply`'s signature expectations.
9. **GAP-9 — empty-argument verbs.** S. `security.go` only, no contract change.
10. **GAP-10 — forward `Create`/`Replace`.** S. `security.go` only.
11. **GAP-8 — bind budget for `UpdateAll`/`DeleteAll`.** M. Changes the meaning of the returned count; independent of the rest.
12. **GAP-12, GAP-13 — documentation and per-relation typing.** S each, deferred.

## What I did not check

- **`tenancy/tenancydb`, `tenancyjobs`, `tenancystorage`, `tenancycache`** — out of the given scope (database-per-tenant is explicitly secondary).
- **`tenancy` core** — `Scope` HMAC binding, `Grant`, `Seal`, `Lifecycle`/`Admission`, `Epoch` rotation. I touched `Authority.Scope`/`Classify`/`Reference` only where `tenancyrow` calls them. **Note:** during this audit `go test ./tenancy/...` failed non-deterministically across three consecutive runs — `TestEveryPartOfAGrantIsInsideItsBinding` (3 subtests), then `TestARestoredGenerationReadsNoneOfThePreviousOnesValues`, then two others, then green. The tree was being edited concurrently by a second Claude Code session (`tenancy/tenancyrow/row.go` changed under me and reverted). Those failures are **not** attributed to anything in this report; they need a re-run on a quiescent tree and belong to whoever audits `tenancy` core.
- **Real PostgreSQL/MySQL execution.** Every probe ran against `crudtest.Recorder`, so I assert what SQL is *emitted*, not what a server does with it. In particular the mistyped bind in GAP-4 (`"tenant_id" = 'acme-7f3c'` against a `BIGINT`) is asserted to be emitted; I did not confirm PostgreSQL rejects it, and I did not run `make integration` (needs Docker).
- **`tenancydb`-style multi-database interactions with the gate**, and `crud.UnsafeExecFor` / `UnsafeBulkInserter` paths.
- **The HTTP/gRPC bindings end to end.** I proved that `query.ParseQuery` + `Request.Compile` with a nil `Config` yields the leaking preload option (GAP-3), but did not drive a live `crudfiber`/`crudgin`/`crudnet`/`crudgrpc` handler.
- **Whether GAP-6's leaked text reaches the HTTP response body.** I confirmed the error is `KindInternal` and that the text reaches every in-process caller; I did not render an envelope to see whether `porthttp` substitutes a generic message.
- **Mutation testing.** The brief for dimension 5 did not ask for it and I ran none; I did the stronger thing for these laws — wrote failing scenarios against the real packages. The plan's recorded "58 mutations, 32 survived" is unverified by me.
- **`crud/sqlrepo` beyond the paths the gate drives** — `Aggregate` group/having rendering, `InsertBatch` native vs portable, `catalog`, `probe`, `sqlfault`.
- **Concurrency of `Relation.resolveDefaults` under `-race`.** I established by reading and by probe P2 that `Meta.Relation` binds a per-`Meta` copy so `Through`'s guard is currently stable, but I did not run a race campaign against the two code paths where `relationContext` is nil (`crud/scope.go:92`).

**Reproducing the probes:** all fourteen live in `/tmp/claude-1000/-home-user-ws-gd-lease/82a92a15-ac6c-4104-9609-680bbbcbfeb1/scratchpad/probe/` as a separate Go module with `replace github.com/frostgrove/vv => /home/user/ws/gd/lease/frostgrove/framework`. `go test -v .` runs all of them; `go test -run TestP2c -v .` etc. runs one. They import the real packages and change nothing in the repository.
