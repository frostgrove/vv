# TENANCY — verification of TENANCY_AUDIT.md

**What this is:** an independent check of ten findings in `TENANCY_AUDIT.md`
(C1–C5, H1, H2, H7, H9, H11). I audited the audit, not the codebase. I read only
the aggregate report before probing, never the per-dimension reports, so the
judgement below is independent of how each finding was argued.

**Method:** every verdict is backed by a Go program in a scratch module with
`replace github.com/frostgrove/vv => /home/user/ws/gd/lease/frostgrove/vv/framework`,
or by a mutation run against a private copy, or by a grep whose full output is
pasted. Probes live in
`/tmp/claude-1000/-home-user-ws-gd-lease/82a92a15-ac6c-4104-9609-680bbbcbfeb1/scratchpad/verify/`.

**Window:** 2026-09-06 00:55:25 → 01:11:14 +05:00. I took a private snapshot at
00:55:37 and re-pinned every file at 01:11:14. **No file I drew a conclusion from
changed during the check** — all hashes below are identical at both ends, so
nothing here needs re-running.

## Files pinned (SHA-256, identical at 00:55 and 01:11)

```
b17fa7a85a399ce7e1ead33b2e7c26ef62f3e7bad64d24d2bf0aa3cc7df54c40  tenancy/tenancyrow/row.go
65e5e14b4c194b98123f64012bc8e2440b076845d812addd7b65522fedd6298c  tenancy/authority.go
c46dbe948b428d75acbed098e46b4f660d61d594e699a00d25e5a3528321f1cc  tenancy/lifecycle.go
dcb09915946921e399761d5a11d95608d1cdcdca8884f582e4bcaf561bb566a3  tenancy/errors.go
cb899650b180532e75316ddd03481470882e900416dcbbcbee2580cfd387468d  tenancy/scope.go
bdf83782a3c4e3a5f1d17ee07ccb3a4b42d464d5a69e8708caaf95ce2e92dc0c  tenancy/tenancycache/cache.go
444758af17dc2d3c5730a5050773dce807119a2d7f26db21cd831b82169b0ab6  tenancy/tenancydb/database.go
4b308ab4f8f4c165b4be980823056d7ca53c8ff52f2c0182c15897360448ecd7  tenancy/tenancyrow/row_test.go
e2d07a804174a76d9cf057eee0af7f051f41e17f9c4abea824340e6c0219e6db  crud/decorators/security/security.go
34636c8bb113285a363cd05895d89d97a729742e8d2452b68cf95d7f6b4d14a0  crud/decorators/security/policies.go
87bab3d0bced00b3a2b4385e1e5b3cb33d724c6de55722b8840080b2255b9420  crud/predicate.go
e4794d2e5b982d73179218cb3e5718eb55ce5293d028482efbab6d1c5ca83402  cache/key.go
ca983a353189e2091885517488db414ebdcd540eb6f400dd9823e218404babf2  port/porthttp/render.go
3dc4a5aea1db95f6bba9a261bf4231bcbcb15d2ce2fd91e9ac1530f7973b6590  port/porthttp/errors.go
5fd915f56b1957bb2fe54169b0d5ec4f445e1cbaa61e9b1efdd289afab620e1b  docs/modules/en/tenancy.md
7b1364ef5408c382343d056a25eb22a98805a1f2818330cd2686310826415b6b  _examples/tenancy-sharedrow/main.go
c385fce67ab81a3713822b59eb1633b21c54b5a009244774082d2b77b7274f92  test/integration/tenancy_test.go
```

Audit's own snapshot, used to date two findings:
`cbfbec8e570d132018c0188dda0a2362713eef17bc192444e024ef7f3f5da89a  .agents/artifacts/audit/snapshot-0046/tenancy/tenancyrow/row_test.go`

**Headline:** eight of ten hold. One (**C3**) has a real mechanism behind a
**false reproduction** — the repro the audit publishes panics at wiring. One
(**H2**) has a real mechanism and a **wrong impact** — the HTTP body it claims to
leak is suppressed by the framework. Two of H11's five named mutations were
**fixed by the concurrent session between 00:46 and 00:52**, which I proved by
running them against the audit's own snapshot; that is not a false positive and
the audit could not have known.

---

## Per-finding verdicts

| id | verdict | evidence | note |
|---|---|---|---|
| **C1** | **CONFIRMED**, and understated | `verify/c1`, `verify/c1b`, `verify/sweep` | All three claimed conversions land verbatim. The *realistic* leak is stronger than the one the audit publishes. |
| **C2** | **CONFIRMED**, all four spellings | `verify/c2` | Severity framing is loose — see "overstates". |
| **C3** | **MECHANISM CONFIRMED / REPRO FALSE** | `verify/c3`, `verify/c3b`, `verify/c3c` | Plain `has_many` and **both** `many_to_many` spellings **panic** at wiring. Only `has_many,fk=…,ref=…` is accepted. `RE-CONFIRMED (grep shows Kind is never read)` is a grep inference presented as a reproduction. |
| **C4** | **CONFIRMED**, exactly as written | `verify/c4` | All three widening paths reproduce; the `1<<8` claim is arithmetically right and forward-looking. |
| **C5** | **CONFIRMED** | `verify/c5`, `verify/pgx` | Extended to `crudpgx.Executor` and `crud.readWriteTx`, which the audit did not name. |
| **H1** | **CONFIRMED**, and understated | `verify/h1` | Exactly the 5 verbs claimed, plus a **6th** the audit missed. Its "not remotely reachable" limiter is correct — I verified it. |
| **H2** | **HALF CONFIRMED** | `verify/h2` | Reference leak into the error value: real. "500 with the tenant name in the body": **false**. "D-008 requires 404": **false**. |
| **H7** | **CONFIRMED** end to end | `verify/h7` | Reproduced through a real `cachememory` backend, not by reading. |
| **H9** | **CONFIRMED**, every grep | greps below | 11/11 of the mechanical claims are exact. The "6 of 11 questions" count is a judgement I did not re-score. |
| **H11** | **CONFIRMED at 00:46; 2 of 5 since fixed** | mutation runs on two trees | `through.Frozen`, `LogValue`, `Lookup` guard still survive at 00:55. Relation-narrowing mutations now killed. |

---

### C1 — `assign` converts instead of validating — **CONFIRMED**

`tenancy/tenancyrow/row.go:139-152` (`assign`), the `ConvertibleTo`+`Convert` pair
at `:144` and `:150`. All three of the audit's measurements reproduce:

```
A create err=<nil>
A stmt: INSERT INTO "invoices" ("tenant_id", "number") VALUES ($1, $2) RETURNING ... args=[]interface {}{"*", "INV-1"}
A read stmt: SELECT "id", "tenant_id", "number" FROM "invoices" WHERE "tenant_id" = $1 args=[]interface {}{42}
B stmt: INSERT INTO "invoices" ("tenant_id", "number") VALUES ($1, $2) RETURNING ... args=[]interface {}{"A", "INV-2"}
C stmt: INSERT INTO "narrows" ("tenant_id") VALUES ($1) RETURNING "id", "tenant_id" args=[]interface {}{0x2c}
```

`int64(42)` writes `'*'` and reads `= 42`; reference `A` with value `int64(65)`
writes `'A'`; `int64(300)` into `uint8` writes `0x2c` = 44. Exactly as claimed.

**The audit undersells this.** Its examples need a contrived `Value`. The
realistic one needs only the `Value` **the module doc teaches verbatim**
(`docs/modules/en/tenancy.md:168-170`, `strconv.ParseInt(r.Value(), 10, 64)`)
against an ordinary `int32` owner column — `verify/c1b`:

```
attacker INSERT: INSERT INTO "docs" ("tenant_id", "body") VALUES ($1, $2) ... args=[]interface {}{1, "attacker row"}
victim GetAll err=<nil> rows=[{ID:1 TenantID:1 Body:attacker row}]
victim SELECT: SELECT "id", "tenant_id", "body" FROM "docs" WHERE "tenant_id" = $1 args=[]interface {}{1}
```

Tenant `4294967297` (2³²+1) truncates to `int32(1)` on write; tenant `1` reads the
row. Doc-taught wiring, stock column type, cross-tenant read.

The audit's structural point is exactly right and is the strongest thing in the
report: the sibling in the same repository, `security.reconcileFieldValue`
(`crud/decorators/security/policies.go:74-89` + `safelyConvert` `:91-135`),
refuses `nil` and range-checks every conversion with `OverflowInt`/`OverflowUint`.
`tenancyrow.Column` has neither.

Type sweep (`verify/sweep`), which substantiates the scorecard's "4 of 6 unusable":

```
int64      read=ok  [7]      create=ok  [7]                update-own-row=ok
int32      read=ok  [7]      create=ok  [7]                update-own-row=security: forbidden: update: row is owne…
uint64     read=ok  [7]      create=ok  [7]                update-own-row=security: forbidden: update: row is owne…
string     read=ok  [acme]   create=ok  [acme]             update-own-row=ok
[]byte     read=ok  [acme]   create=ok  [[97 99 109 101]]  update-own-row=security: forbidden: update: row is owne…
*string    read=ok  [acme]   create=crud: .*string: an ownership value of ty…  update-own-row=security: forbidden: field T is…
```

4 of 6 unusable; **3** fail with the false "owned by a different tenant", not 2.

### C2 — nil ownership value → `IS NULL` — **CONFIRMED**

The parent asked whether a `Value` func can realistically return an untyped nil
and reach `crud.Eq`. It can: `Value` is `func(tenancy.Reference) (any, error)`
(`row.go:49`), and `crud.Eq(field, nil)` returns `crud.nullNode`
(`crud/predicate.go:396-411`). All four spellings reproduce identically
(`verify/c2`); the directory-miss spelling is the realistic one:

```
crud.Eq(f, nil) type: crud.nullNode
[directory miss]   SELECT: SELECT "id","tenant_id","body" FROM "rows" WHERE "tenant_id" IS NULL args=[]interface {}(nil)
[directory miss] Save err=<nil>
[directory miss]   INSERT: INSERT INTO "rows" ("tenant_id","body") VALUES ($1,$2) ... args=[]interface {}{(*string)(nil), "new"}
[directory miss]   stmt: DELETE FROM "rows" WHERE ("tenant_id" IS NULL AND ("id" = $1 AND "tenant_id" IS NULL AND "body" = $2)) args=…
```

Read, insert and delete all land on the NULL universe with `err == nil`. The
audit's comparison with `security.ScopeField` is exact: `policies.go:77-79`
returns `Denied` on a nil value; `tenancyrow` does not.

### C3 — `Through` over a to-many — **MECHANISM CONFIRMED, REPRO FALSE**

This is the one the parent was right to be suspicious of. `Through` does **not**
accept a to-many in the shapes the audit names.

```
A.Kids   kind=has_many      ToMany=true  LocalField=""
B.Kids   kind=has_many      ToMany=true  LocalField="Code"     (spelled ref=Code)
C.Kids   kind=many_to_many  ToMany=true  LocalField=""
D.Kids   kind=many_to_many  ToMany=true  LocalField=""         (spelled joinFK/joinRef)
E.Kid    kind=has_one       ToMany=false LocalField=""
F.Owner  kind=belongs_to    ToMany=false LocalField="OwnerID"

  has_many   Through -> PANIC: tenancy: the relation Kids declares no local key, so nothing freezes the link that carries ownership
  hm+ref     Through -> ACCEPTED, Frozen()=[Code]
  m2m        Through -> PANIC: …
  m2m+fk     Through -> PANIC: …
  has_one    Through -> PANIC: …
  belongs_to Through -> ACCEPTED, Frozen()=[OwnerID]
```

Three specific claims are refuted:

- *"`Through` accepts a to-many relation"* — for a plain `has_many` it **panics**
  at `row.go:160`.
- *"`many_to_many` behaves identically"* — **false**, both `many_to_many`
  spellings panic. `LocalField` is empty for every `many_to_many`.
- *"`Frozen()` returns the model's own primary key, so the guard at `row.go:139`
  does not fire"* — the guard (actually `row.go:159-161`) **does** fire for 3 of
  the 4 to-many spellings. It fires because `local == ""`, which is the common
  case, not the exception.

The mechanism is real for the one spelling that survives —
`has_many,fk=…,ref=<local field>` (`verify/c3c`):

```
Through[Post] over has_many(ref=ID): Frozen()=[ID]  (Post's own PK: ID)
[acme]   SELECT "id","body" FROM "posts" WHERE EXISTS (SELECT 1 FROM "tags" AS rx1 WHERE rx1."post_id" = "posts"."id" AND rx1."tenant_id" = $1 AND rx1."tenant_id" = $2) args=["acme" "acme"]
[globex] SELECT "id","body" FROM "posts" WHERE EXISTS (SELECT 1 FROM "tags" AS rx1 WHERE rx1."post_id" = "posts"."id" AND rx1."tenant_id" = $1 AND rx1."tenant_id" = $2) args=["globex" "globex"]
[acme]   DELETE FROM "posts" WHERE (EXISTS (…$1…$2…) AND ("id" = $3 AND "body" = $4)) args=["acme" "acme" 1 "the shared post"]
```

A post carrying one acme tag and one globex tag satisfies both `EXISTS`
subqueries; both tenants read **and delete** it, and the snapshot predicate
reuses the same `EXISTS` so it does not stop it. So the finding should survive —
at reduced reach, and with the repro rewritten. As published it will be tried,
it will panic, and the finding will be dismissed as noise.

The tell is in the report itself: `**RE-CONFIRMED** (grep shows Kind is never
read)`. `Kind` is indeed never read — I confirm that — but "the code does not
check X" does not establish "the failure X would cause is reachable". A different
guard, `local == ""`, catches most of it by accident. The lead re-confirmed by
grep and labelled it a reproduction.

### C4 — mistyped `Admission` silently widened — **CONFIRMED**

`tenancy/lifecycle.go:65-76`, `tenancy/authority.go:41-44`. `verify/c4`:

```
Admit(ClassRead)  [no states]              IsZero=true   Active: read=true write=true durable=true   Bind(ClassWrite) err=<nil>
Admit(ClassRead, Active)                   IsZero=false  Active: read=true write=false durable=false Bind(ClassWrite) err=tenancy: tenant lifecycle does not admit this work: forbidden
Admit(Class(9), Active)  [bad class]       IsZero=true   Active: read=true write=true durable=true   Bind(ClassWrite) err=<nil>
Admit(ClassRead, Lifecycle(99)) [bad]      IsZero=true   Active: read=true write=true durable=true   Bind(ClassWrite) err=<nil>
Admission{} [genuinely unset]              IsZero=true   Active: read=true write=true durable=true   Bind(ClassWrite) err=<nil>

bit shift on the uint8 the bitset uses: 1<<6=64 1<<7=128 1<<8=0
```

An operator writing `tenancy.Admit(tenancy.ClassRead)` for a read-only deployment
gets read **and write and durable**, `err == nil`, silently. The row with
`Admit(ClassRead, Active)` is the control that proves the mechanism is the zero
value and not something else. The `1<<8 == 0` claim is correct on the declared
`[3]uint8`; it is a forward hazard (`Deleted = 6`, so states 7 and 8 are next),
not a live defect, and the audit says so.

### C5 — no production `crud.Source` implements `io.Closer` — **CONFIRMED**

`tenancy/tenancydb/database.go:280-284`. `verify/c5` and `verify/pgx`:

```
crudsql.Postgres(db)  [DB]             crudsql.DB              io.Closer=false
crudsql.Source(nil, crud.Postgres{})   crudsql.source          io.Closer=false
crudtest.Postgres()  [*Recorder]       *crudtest.Recorder      io.Closer=false
crud.ReadWrite(a,b)                    crud.readWriteTx        io.Closer=false
crudpgx.Open(nil)                      crudpgx.Executor        io.Closer=false

closeSource's type assertion FAILS -> the pool is dropped on the floor
```

The audit named four types; there are at least five — `crud.readWriteTx`, the
type a read/write-split deployment actually holds, is a sixth path to the same
place. The audit's sharpest observation here is also correct: the only
`Close() error` in the package's blast radius is the **test fixture**,
`tenancy/tenancydb/database_test.go:23`, so the mutation that would catch this is
killed by a type no production wiring resembles.

One nuance the audit does not state: `Sources` is consumer-supplied
(`database.go:16-24`), so a consumer *could* return a source that closes. Nothing
in the tree, the `Sources` doc comment or `docs/modules/en/tenancy.md` tells them
they must. That makes it a missing contract rather than an unconditional bug —
which is what the remediation entry ("give `crud.Source` a documented close
path") already says, so the finding and its fix are consistent.

### H1 — a caller `crud.Option` erases the gate's narrowing — **CONFIRMED**

`crud/decorators/security/security.go:187`:
`return append([]crud.Option{crud.Where(p), rel}, options...), p, nil` — gate
first, caller last. `crud.Options` (`crud/options.go:5-31`) has `Filter
[]Predicate` exported. `verify/h1`, using an option of exactly the shape
`crud.Where` itself uses:

```
GetAll     err=<nil>
           SELECT "id", "tenant_id", "number" FROM "invoices" args=[]interface {}(nil)
Get        err=<nil>
           SELECT "id", "tenant_id", "number" FROM "invoices" ORDER BY "id" ASC LIMIT 20 args=[]interface {}(nil)
Count      err=<nil>
           SELECT count(*) FROM "invoices" args=[]interface {}(nil)
Exists     err=<nil>
           SELECT 1 FROM "invoices" LIMIT 1 args=[]interface {}(nil)
First      err=security: forbidden: read: row is owned by a different tenant
UpdateAll  err=security: forbidden: update: row is owned by a different tenant
DeleteAll  err=security: forbidden: delete: row is owned by a different tenant
Aggregate  err=<nil>
           SELECT "tenant_id", COUNT(*) FROM "invoices" GROUP BY "tenant_id" args=[]interface {}(nil)
```

Exactly the five verbs claimed leak; exactly the three claimed are caught, and by
the mechanism the audit says (row-level `Inspect`, which exists for another
reason). This finding is precisely scoped.

**Its severity limiter is also correct**, and I checked it rather than taking it:
`crud/query/compile.go:314` `Request.Compile` appends only
`crud.Where`/`OrderBy`/`Select`/`PreloadCap`/`Page`/`Offset`/`Limit`/`After`/`Before`/`Unpaged`/`SkipTotal`/`Distinct`
— 16 append sites, every one a library constructor, never a caller-authored
closure. So H1 is an application-code hazard and not an unauthenticated one, as
the report says.

### H2 — `tenancyrow` does not `Classify` — **HALF CONFIRMED**

`grep -c Classify tenancy/tenancyrow/row.go` → `0`. Confirmed. The leak into the
error value is confirmed with the doc's own `Value` (`verify/h2`):

```
GetAll err = strconv.ParseInt: parsing "acme-7f3c": invalid syntax
  reference "acme-7f3c" present in the error text: true
  errors.Is(err, crud.ErrForbidden)=false ErrNotFound=false ErrBadRequest=false
  tenancy.Classify(err) would give: tenancy: the tenant capability is unavailable
porthttp.Status(err) = 500
```

That much stands, and it stands against the repo's own written rule:
`tenancy/errors.go:43-46` says "**Every** seam that calls application-supplied
code — a resolver, a source factory, a fence — answers through this". A `Value`
func is application-supplied code and does not.

**But the published impact is wrong.** See "overstates" below.

### H7 — `cache.Global` over a tenant-owned key — **CONFIRMED end to end**

`verify/h7`, through a real `cachememory` backend, one word changed between the
two runs:

```
[global]      globex Lookup(same logical key) -> value="acme-secret" state=1 err=<nil>
[partitioned] globex Lookup(same logical key) -> value=""            state=2 err=<nil>
```

`cache.GlobalPlan[tenancycache.Key[string]]()` type-checks, activates, and serves
tenant `acme`'s value to tenant `globex`. The audit's structural point is right:
the zero-scope guard lives in `Partition` (`tenancy/tenancycache/cache.go:45-47`),
i.e. inside the function `Global` does not call. `cache.Scope.valid()`
(`cache/key.go:96-98`) accepts `global` with no partitioner by design, so nothing
downstream catches it.

### H9 — documentation — **CONFIRMED, every mechanical claim**

Eight tenancy docs (`docs/modules/{en,ru}/tenancy.md`, D-115, D-116, D-117,
D-118, FL-033, UC-004):

```
ParseReference   0
NewEpoch         0
ParsePurpose     0
subdomain        0
X-Tenant         0
JWT              0
authfiber        0
authgin          0
authnet          0
CREATE INDEX     0
index            0
```

`grep -ci tenant docs/usage-guides/migrations.md` → `0`.
`ls docs/usage-guides/` → `ent.md gorm.md migrations.md model-generation.md
repository.md` — no tenancy page, which is where `CLAUDE.md`'s own lookup table
sends "How does a consumer set this up?".
`grep -rn "tenanc" auth/ port/ app/ crud/http/ runtime/ | wc -l` → `0`.

Every mechanical claim is exact. The "1 of 11 answered, 4 partial, 6 unanswered"
scoring is a judgement I did not re-derive; the evidence under it is sound.

### H11 — the test suite — **CONFIRMED at 00:46; two of five since fixed**

Baseline on my 00:55:37 private snapshot: **green** (`ok` × 6). The report's
"red again at 00:48" no longer holds; that is a live-tree artefact, not a defect.

Five mutations, each in a fresh copy of the 00:55 tree:

```
--- M1 through.Frozen() -> nil ---              ok (all 6 packages)     SURVIVED
--- M2' column.Relations names a foreign tenant ---
    --- FAIL: TestAPreloadOfADeclaredRelationCarriesTheTenant
        row_test.go:400: the preload narrows the child table to [1 attacker] rather than to the scope's own tenant   KILLED
--- M3' through.Narrow names a foreign tenant ---
    --- FAIL: TestARowOwnedThroughARelationIsNarrowedByThatRelation
        row_test.go:339: the correlated read binds [acme-7f3c attacker] — one of its conjuncts names another tenant  KILLED
--- M4 Authority.Lookup drops the reference echo guard ---   ok (all 6) SURVIVED
--- M5 Scope.LogValue returns the raw reference ---          ok (all 6) SURVIVED
```

M2' and M3' are now killed **by argument**, exactly the fix the audit asked for.
Before calling that a false positive I rebuilt the audit's own 00:46 tree from
`.agents/artifacts/audit/snapshot-0046/` and re-ran them:

```
--- [00:46 tree] M2' column.Relations names a foreign tenant ---   ok   SURVIVED
--- [00:46 tree] M3' through.Narrow names a foreign tenant ---     ok   SURVIVED
--- [00:46 tree] M1 through.Frozen() -> nil ---                    ok   SURVIVED
```

**The audit was right when it was written.** `snapshot-0046/…/row_test.go` is
416 lines, sha `cbfbec8e…`, and asserts with `strings.Contains(statement,
"tenant_id")` at lines 256, 286 and 343 — precisely as described. The live file
is 498 lines, sha `4b308ab4…`, and now reads:

```go
for _, argument := range statement.Args {
    if argument != any(secretReference) {
        t.Fatalf("the correlated read binds %v — one of its conjuncts names another tenant", statement.Args)
```

So: **fixed by the concurrent session between 00:46 and 00:52**, not a false
positive. `tenancy/tenancyrow/row.go` itself gained only a doc comment in that
window (`diff` shows a single added comment block above `Ownership[M]`), so
C1/C2/C3 remain live.

Still live at 00:55, and these are the ones to act on: `through.Frozen` → `nil`,
`Scope.LogValue` → raw reference, and the `resolution.Reference != reference`
guard in `Authority.Lookup`.

The coverage claims check out too:
`grep -rn 'tenancyrow.Through' test/ _examples/ | wc -l` → `0`; the only use
anywhere is `tenancy/tenancyrow/row_test.go:318`.
`grep -rn '\.Restore(ctx\|ExistsUnscoped' tenancy/ test/integration/tenancy_test.go` → `0`.
`test/integration/tenancy_test.go` is 165 lines, 2 top-level tests, 5 subtests.

---

## Findings the audit overstates

**C3's reachability.** Covered above. The mechanism is real; the published
reproduction panics. `many_to_many` is asserted to "behave identically" and does
the opposite. Rewrite the repro around `has_many,fk=…,ref=…` or the finding will
be dismissed by the first person who tries it. Its remediation entry (`#2`,
"`Through` panics at wiring on a to-many, and when `local` is the model's own
PK") is also half-done already: it panics on a to-many today, for a different
reason, and the fix that is actually needed is the `local == PK` half.

**H2's impact — two errors of kind.** The report says `porthttp` answers "**500
with the tenant name in the body**". It does not. `port/porthttp/render.go:79-81`:

```go
status := StatusFor(port.KindOfWith(err, this.codesOrNil()))
if status == http.StatusInternalServerError {
    return status, nil, Internal()
}
```

Measured:

```
EnvelopeRenderer -> 500 body={"type":"error","errors":{"general":[{"error_code":"internal"}]}}
  reference "acme-7f3c" present in the rendered body: false
```

The framework already suppresses the body on 500. The leak reaches the error
value and therefore the server-side log — real, and worth fixing — but not the
response. Second, "where D-008 requires 404" misreads a binding decision:
D-008's invariant is scoped to "**when a policy `Scope` hides a row**"
(`docs/ai/decisions/D-008-out-of-scope-is-404-not-403.md:4`). A `Value` function
that cannot parse its own reference is a broken deployment, not a hidden row;
500 is defensible and arguably correct. H2 is a **high** finding about redaction
and error vocabulary, not a status-code contract breach, and it should not be
paired with C1 in remediation step 3 on the strength of a 404 claim that does not
hold.

**C2's "every affected caller shares one universe" framing.** The SQL is exactly
as claimed and the finding is critical. But the phrasing implies two *legitimate*
tenants reading each other. What the probes show is narrower and should be stated
as what it is: a **fail-open on a lookup miss** — every tenant whose `Value`
answers nil lands in the same NULL universe and can read, insert into and
`DELETE` it. That is still critical (an unmapped tenant deleting the shared/global
rows is the worst case), but "tenant A reads tenant B's rows" is not the shape,
and a reader who tests for that shape will not find it.

**"Tests: fail" as a dimension verdict.** 70.4% kill with 21 survivors is not a
good score, but the suite killed both relation-narrowing mutations the moment the
assertions were tightened, and killed `M2'`/`M3'` with failure messages that name
the defect in plain words. The specific survivors are the finding; "fail" on the
whole dimension is broader than the evidence.

---

## Findings the audit understates

**H1 reaches a sixth verb, and it is the one that matters most.**
`gate.ExistsUnscoped` (`crud/decorators/security/security.go:381-395`) has the
same ordering:

```go
found, err, supported := crud.ExistsUnscopedOf(this.Core, ctx,
    append([]crud.Option{crud.Where(scope), relationNarrowing(rel)}, options...)...)
```

Measured through `crud.ExistsUnscopedOf(repo.Unwrap(), ctx, Unscoped())`:

```
ExistsUnscoped -> true err=<nil>
           SELECT 1 FROM "invoices" LIMIT 1 args=[]interface {}(nil)
```

This is worse than the other five because **D-115 exists specifically to make
this verb answer inside the gate's own scope** — its invariant is "a decorator
that narrows rows and forwards the verb answers within its own narrowing". A
caller option defeats a binding decision, and turns the verb back into the
enumeration oracle D-008 and D-115 jointly exist to close. Remediation item 8
says "all 11 verbs"; make sure `ExistsUnscoped` is counted and called out by name.

**C1's realistic trigger is the module doc, not a contrived `Value`.** Shown
above: the doc's own `strconv.ParseInt` plus an `int32` column is a cross-tenant
read. That belongs in the finding, because it changes who is exposed from
"someone who mis-wired" to "someone who copied the documentation".

**C5 covers more source types than it names.** `crud.readWriteTx` — what
`crud.ReadWrite(primary, replica)` returns — also has no `Close`, so a
read/write-split deployment leaks two pools per eviction, not one.

**C1's own scorecard undercounts.** "2 of them failing with a false 'owned by a
different tenant'" is **3** (`int32`, `uint64`, `[]byte`).

---

## What is missing

**1. The gate can be unwrapped in two public calls, and no dimension looked.**
This is the largest gap. `crud.Repo.Unwrap()` returns the gate; `gate.Next()`
(`crud/decorators/security/security.go:108`) is **exported** and returns the core
underneath it:

```
through the gate:  SELECT "id","tenant_id","number" FROM "invoices" WHERE "tenant_id" = $1 args=["acme"]
repo.Unwrap() -> *security.gate[main.Invoice,int64]
gate.Next()   -> *sqlrepo.repository[main.Invoice,int64,main.InvoiceUpdate]
raw.GetAll(background ctx) err=<nil> rows=[{ID:2 TenantID:globex Number:OTHER-1}]
bypassing the gate: SELECT "id","tenant_id","number" FROM "invoices" args=[]interface {}(nil)
```

No unsafe naming, no build tag, an unbound `context.Background()`, every tenant's
rows. I am **not** reporting this as a defect, because `Next()` is required by a
binding decision: D-061's table lists `security.gate` under `crud.Nexter` and its
"what it forbids" says "Do not add a decorator to this repository without a
`Next()`". It exists so `crud.SourceOf` can find the source through decorators.

What is missing is that **nothing reconciles it with D-117**, whose invariant
says in terms: "There is no default tenant, no ambient tenant, and *no path that
succeeds without a scope*" (`D-117…md:4`). There is such a path, it is public,
and it is two calls. Either D-117's invariant needs narrowing to "no path through
the repository handle", or D-061 needs a note that `Next()` on a policy-bearing
decorator hands out an unsupervised core. Ten dimensions covering microkernel,
building blocks, architecture and data integrity all missed the interaction
between two decisions each of them read.

**2. Nobody asked what a *second* gate does.** `security.Combine` is named twice
in the report (as a broken escape hatch for `Through`), but no dimension tested a
`tenancyrow.Repository` composed with a second `security.Gate` — the ordinary way
an application adds its own row policy. Given H1, the outer gate's options
arriving last is the same defect one layer up, and `Immutable`/`Frozen` sets from
two gates have to merge somehow. Untested, undocumented.

**3. No transaction-boundary check for the shared-row path.** H16 covers
`Revalidate` cost inside `SaveAll`'s transaction, but nothing asks the ACID
question the project's own `CLAUDE.md` puts first: does a scope bound outside
`saveTransaction` still hold inside it, is the inspect→snapshot CAS actually
`REPEATABLE READ`-safe, and does anything take `FOR UPDATE`? The report *praises*
the CAS ("correct optimistic CAS under N replicas with no lock and no version
column") without stating the isolation level it requires. Under `READ COMMITTED`
— PostgreSQL's default and what `docker-compose.yml` gives you — a full-row
snapshot predicate is sound, but that is an argument nobody made, and the
"genuinely right" section asserts the conclusion without it.

**4. `Aggregate` is treated as a leak surface but never as a disclosure surface.**
H1 shows `Aggregate` returning every tenant's rows. Nobody asked the narrower
question: with the gate intact, can `GROUP BY` on a non-owned column plus
`COUNT`/`MIN`/`MAX` disclose the *existence and shape* of other tenants' data
through a correctly-narrowed statement? That is the classic multi-tenant
aggregate leak and it is unexamined.

**5. Nothing checked the `ru` module doc for the same defects as the `en` one.**
The report says `docs/modules/ru/tenancy.md` was "checked for parity (faithful, 2
minor drifts) but not independently for correctness". Since C1's realistic
trigger *is* a code sample in the `en` doc, the same sample in `ru` carries the
same defect and needs the same fix — otherwise fixing one leaves the other
teaching the leak.

**6. No `-race` run over `tenancydb`, the one package with a mutex and a
condition variable.** The report says "no `-race` campaign over the whole
repository". `tenancydb` is where C5's orphaned-entry and wedged-slot claims live;
those are concurrency claims and `go test -race ./tenancy/tenancydb/` is one
command. (My own runs were without `-race`; see below.)

---

## What I could not verify and why

- **The 71-mutation, 21-survivor campaign.** I ran 5 mutations on two trees, not
  71. I confirmed the 3 survivors and 2 kills that H11 names specifically, and
  dated the 2 that changed. The 70.4% figure I neither confirm nor dispute.
- **Anything against a live database.** I did not start PostgreSQL and did not
  use the `55433` override. Every SQL result above is what `crudtest.Recorder`
  records as *emitted* — which settles what the library builds, and settles
  nothing about what a server does with it. This matters most for C1: whether
  PostgreSQL rejects `WHERE tenant_id = 42` against a `text` column, or coerces
  it, decides whether case A is a self-DoS or a silent leak. The audit flags this
  same gap in its own §6 and it is still open. My `int32` truncation case does
  not depend on it — both sides bind an integer.
- **`-race`.** All my `go test` runs were without `-race`, so the tenancydb
  concurrency half of C5 (orphaned successor entry, wedged slot, 380 ms mutex
  block) is unverified by me. I confirmed only the `io.Closer` half.
- **C4's `1<<8` future hazard.** Verified as arithmetic on the declared type. No
  8th `Lifecycle` state exists, so the failure is not reachable today.
- **H9's "6 of 11 adoption questions unanswered".** I verified all eleven greps
  underneath it, not the scoring. Someone should re-score it against a real
  adopter's question list.
- **H3–H6, H8, H10, H12–H16 and the whole medium list.** Out of scope for this
  pass. H14 is the one exception — I checked it in passing because it is one line:
  `_examples/tenancy-sharedrow/main.go:78` is
  `[]byte("replace-me-with-32-bytes-from-your-secret-store")`, **47 bytes**,
  against `MinDurableKeyBytes = 32` at `tenancy/authority.go:11`, and the example
  builds clean. **CONFIRMED**; remediation item 11 ("XS, do it today") is right.
- **Whether the concurrent session has changed anything since 01:11:14.** All
  hashes above were stable across my window. Anything written after that is
  outside this report.
