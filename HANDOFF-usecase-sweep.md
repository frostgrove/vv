# Handoff — the use-case release sweep

A run was started, got about half way and was stopped. This file is what the next
agent needs to finish it without re-deriving anything. Delete it when the sweep is done.

---

## 1. What this exercise is

The owner is about to tag a release of `vv`. Before that, every module gets a
**release-readiness sweep**: a catalogue of what a consumer actually wants from it,
written by imagining the consumer *first* and reading the code *second*, so the
distance between the two becomes visible. The point is to catch what the code cannot
do today — not to describe what it does.

Each sweep also proposes the **top-framework-level DX** the module should have (short
at the call site, still reachable when you need more) and gives a **verdict** on
whether today's code delivers it.

The order of work is the whole method and must not be relaxed:

1. read the module docs and the existing `UC-NNN` files — **no `.go` files**
2. invent the happy cases / edge cases from the consumer's chair
3. design the ideal call site
4. **only now** open the source, and fill in a verdict per case with `file:line` evidence
5. do not retro-fit the design to what the code turned out to be — that distance is the deliverable

---

## 2. State on disk

### Done — the restructure

`docs/ai/usecases/` was flat; it is now:

```
Index.md                 rewritten: new "## Layout" section, and the index table
                         gained "Lives in" / "Also constrains" columns
general/                 UC-001, UC-015  — the two no single module can deliver
modules/<module>/        the other 19 UC files, filed under their owner
```

The 18 module directories are: `crud sqlrepo query specs security faults errs port
crudhttp crudgrpc auth authhttp remote adapters crudtest codegen vvdb utils`.

All moves were `git mv`; links in `Index.md` were rewritten; `docs/Index.md` had a stale
`usecases/` link fixed to `ai/usecases/`. Nothing else in the repo referenced these paths
by path — the `[[UC-NNN]]` wiki-links are ID-based and did not move.

### Done — the happy half

All 19 sweep files exist and are complete enough to build on. Treat the happy half as
**finished**; do not re-run it.

| File | Happy cases |
|---|---|
| `general/General.md` | 30 |
| `modules/faults/Faults.md` | 28 |
| `modules/security/Security.md` | 27 |
| `modules/sqlrepo/Sqlrepo.md` | 27 |
| `modules/utils/Utils.md` | 26 |
| `modules/remote/Remote.md` | 25 |
| `modules/vvdb/Vvdb.md` | 25 |
| `modules/auth/Auth.md` | 24 |
| `modules/crud/Crud.md` | 24 |
| `modules/crudhttp/Crudhttp.md` | 24 |
| `modules/port/Port.md` | 22 |
| `modules/query/Query.md` | 22 |
| `modules/specs/Specs.md` | 22 |
| `modules/crudgrpc/Crudgrpc.md` | 21 |
| `modules/adapters/Adapters.md` | 20 |
| `modules/crudtest/Crudtest.md` | 20 |
| `modules/errs/Errs.md` | 18 |
| `modules/authhttp/Authhttp.md` | 17 |
| `modules/codegen/Codegen.md` | 13 |

`Codegen.md` is the one that lagged — 13 against a median of 22. If there is budget for
one extra happy round anywhere, spend it there.

16 of the 19 already read **not ready**. Only `Adapters`, `Crudhttp` and `Crudtest` say
"ready with gaps".

### Not done

- **the edge half — none of it.** No file contains a `## Edge cases` section.
- the cross-file audit (duplicates, contradictions, adequacy)
- the per-file fixes that audit would produce
- `docs/ai/usecases/Release-readiness.md` — the roll-up
- `docs/ai/usecases/modules/Index.md` and `docs/ai/usecases/general/Index.md`, and the
  section in `docs/ai/usecases/Index.md` that links all three

Nothing is half-written: each writer wrote its file in one pass. The only soft spot is the
`**Verdict:**` line at the top of files whose last review round did not close — it reflects
the text as of the previous revision.

---

## 3. What is left, in order

### Step 1 — the edge half, one agent per file, all 19 in parallel

Each agent **appends** to the existing file. It must not rewrite the happy half. The one
line above its own sections it may touch is `**Verdict:**` at the top.

Rounds: up to **3** per module, up to **5** for `General.md`. A round is one writer plus
three reviewers. Stop early when all three reviewers approve.

Prompt for the edge writer:

> You are adding the EDGE-CASE half of the release-readiness sweep for **\<module\>** in
> `vv`, a generic CRUD repository framework for Go. Repository root: `/home/user/ws/frostgrove/vv`.
> The file `<path>` already exists and holds the happy-path half. Read it first.
>
> **Append to that file. Do not rewrite the happy half.**
>
> STEP 1 — Read the happy half, the module docs `<docs>` and the `UC-*.md` files in the
> module's directory. Do not open `.go` files yet.
>
> STEP 2 — Invent the edge cases from the consumer's side, before you know what breaks.
> Aim for 10–16 cases (18–26 for General). Work these shapes deliberately, at least two of
> each that applies:
> - **boundary** — empty, one, the limit, the limit plus one, zero rows, the whole table
> - **adversarial input** — a client that is hostile, or merely wrong, or a typo away from right
> - **misuse** — the declaration a tired author writes at 6pm: a typo'd field name, a knob
>   set twice, two decorators in the wrong order, a nil where something was expected
> - **concurrency** — two requests, two writers, a shared cache filled by whoever is first
> - **degenerate declaration** — a model with no primary key, a composite key, a relation to
>   itself, a column the Go type cannot hold
> - **partial failure** — the database goes away mid-request, a context is cancelled, one row
>   of a batch fails
> - **scale** — a thousand of something the design assumed was a handful
> - **the seam** — this module used together with the one next to it, where neither owns the result
>
> STEP 3 — ONLY NOW open the source `<src>` and the tests beside it. For each case, decide
> what happens today and cite it. Pay attention to what is *untested* as much as to what is
> wrong: a guarantee with no test is a release risk even when the code looks right.
>
> STEP 4 — Append exactly this structure:
>
> ```markdown
> ## Edge cases
>
> ### E-<MODULE>-01 — <title>
> **Shape:** boundary | adversarial input | misuse | concurrency | degenerate declaration | partial failure | scale | seam
> **Setup:** <one sentence>
> **What the consumer does:** <one or two sentences>
> **What must happen:** <the outcome a reasonable consumer would call correct — and note that
> "refuses loudly at start-up" is very often the right answer here>
> **Today:** ✅ handled | 🟡 partial | ❌ wrong or unhandled | ❓ unverified
> **Evidence:** <file:line, the test that pins it, or "no test found">
> **Blast radius:** silent wrong answer | data leak | data loss | crash | confusing error | none
>
> ## Edge verdict
> <3–5 sentences. Which shapes of failure this module is genuinely closed against, and which
> it is open to. Name the worst one first.>
>
> ## Release blockers found here (edge)
> | # | What | Severity | Why it blocks |
> |---|---|---|---|
> ```
>
> Then update the `**Verdict:**` line at the top so it reflects both halves, and change
> `**Sweep:** happy paths · release readiness` to
> `**Sweep:** happy paths · edge cases · release readiness`.
>
> A silent wrong answer outranks a crash. A leak outranks both. Order every table that way.

### Step 2 — three reviewers per round, in parallel, distinct lenses

Reviewers **do not edit the file**. They return findings; the next round's writer applies them.
Each returns: `approved` (bool), `verdict` (one paragraph), `wrong[]` (claim / why / evidence),
`missing[]` (what / why), `dx_issues[]` (issue / suggestion), `duplicates[]`.

`approved` is **false by default on round 1** — a first draft of a document like this is never
finished. From round 2 on, approve when what remains is taste rather than substance.

**Lens `consumer`** — a staff engineer who has shipped three services on frameworks like this
and been burned by all three. Does the catalogue describe the job? What would a real
application hit that is not here at all? Which cases are showpieces nobody does? Is any
"Must hold" line unfalsifiable, or written in the library's vocabulary rather than the
consumer's? Is anything here really another module's business?

**Lens `code`** — every ✅/🟡/❌/❓ and every Evidence line is a claim about the source, and you
check it by opening the file. Does the cited `file:line` say what the document claims? Is
anything marked ✅ only *probably* true — no test, or a test that would still pass if the
feature were deleted? This repository treats a vacuous test as a liability (`[[D-020]]`); a
vacuous verdict is the same defect. Does the file name a symbol, option or flag that does not
exist? Grep for it — this is the most common and most damaging failure of a document like this.
Is anything marked ❌ that in fact works? Does it contradict an existing `UC-NNN`'s **Status**
or a gap already recorded in `docs/ai/usecases/Index.md`?

**Lens `dx`** — judge the proposed API, not the prose. Is the proposed call site actually
shorter, or just differently shaped? Count the lines and the concepts a newcomer must hold at
once, and say the numbers. Does "turning one knob" extend the short path or abandon it? A
framework where customising means dropping to a lower layer has a DX cliff, and that is the
thing worth catching before a tag. **Does the proposal contradict a binding decision in
`docs/ai/decisions/`?** Read the ones it touches; if it does and the file does not say so,
that is your most important finding. Is it implementable with no third-party dependency in the
root module (`[[D-036]]`), and does it respect package `crud` importing the standard library
only (`[[D-016]]`)? Would this shape still read well as the twentieth resource, not the first?

### Step 3 — cross-file audit, three agents in parallel, each reads all 19 files

- **duplicates** — the same case in two files. Nineteen writers worked without seeing each
  other, so the seams are where this happens: query and the HTTP bindings both describe the
  request arriving; security and sqlrepo both describe a narrowed write; errs, faults and port
  all describe a violation reaching a client. Name the canonical owner, and say what the other
  file keeps — usually a one-line pointer, not nothing.
- **contradictions** — two files disagreeing about the same code, or two DX proposals that
  cannot both be built. Resolve by reading the source; where neither is clearly right, say
  "unresolved" rather than guessing. **Also check the nineteen ideal APIs compose into one
  framework** — that they do not is itself the most important finding available here.
- **adequacy** — vacuous cases, verdicts with no evidence, symbols that do not exist (grep
  every symbol in a ✅ verdict), house-style failures, severity inflation and its opposite.

Each finding names **one** file and the concrete edit to make there.

### Step 4 — fixes, one agent per affected file, in parallel

Give each agent only its own file's findings. A finding is a judgement, not an order: if
applying one would make the file worse, check the source, leave it, and record the
disagreement in a `## Contested` section. Re-check the `**Verdict:**` line afterwards.

### Step 5 — `docs/ai/usecases/Release-readiness.md`

Read every sweep file's blocker tables, verdict lines and DX verdicts. Also read the **Gaps**
section of `docs/ai/usecases/Index.md` — it is the pre-existing hand-maintained list, and a
blocker already there is not news. A blocker NOT there is what this whole exercise was run to
find and must be marked new.

```markdown
# Release readiness

<3–5 sentences: what this is, how it was produced, how much to trust it. Say plainly that it
was generated by imagining consumer scenarios first and checking the code second, so a verdict
here is a lead to confirm rather than a finding to act on blind.>

## Verdict per module
| Module | Verdict | The one thing | Sweep |

## Blockers, worst first
| # | Module | What | Severity | New? | Why it blocks a tag |
<Merge duplicates across modules into one row naming both. Order by blast radius: silent wrong
answer, leak, data loss, crash, confusing error, ceremony.>

## DX, across the whole framework
<Where the framework is as short as it should be and where it is not. Name the DX cliffs —
the places where customising means abandoning the short path. Then the one or two changes with
the best ratio of consumer-visible improvement to work.>

## What this sweep could not settle
<the ❓ verdicts and the auditors' unresolved disagreements. Honest beats complete.>
```

A blocker is something that would embarrass the project in the first week after a tag: a wrong
answer nobody can see, a leak across tenants, a write on the wrong database. Ceremony is not a
blocker, however annoying.

### Step 6 — the indexes

- `docs/ai/usecases/modules/Index.md` — one row per module directory: module, import paths,
  sweep file, the `UC-NNN` files that live there (or "—"), and the verdict lifted from that
  file's `**Verdict:**` line. Sort by the reading order a newcomer needs — core, decorators,
  request path and transports, auth, errors, adapters, tooling, utilities — not alphabetically.
  Preamble says what separates a `UC-NNN` from a `<Module>.md`: a use case is a settled contract
  with a status; a sweep is a readiness pass that is allowed to be wrong and says where it is unsure.
- `docs/ai/usecases/general/Index.md` — same shape, for UC-001, UC-015 and General.md. Preamble
  says what makes a use case general: no single module could deliver it alone.
- `docs/ai/usecases/Index.md` — add a short section after "## Layout" linking all three plus
  `Release-readiness.md`. **Do not touch** the existing "## Index", "## Coverage map" or
  "## Gaps" sections; they are hand-maintained and still correct.

Every path written must exist. Check each one.

---

## 4. Constraints that bind every agent in this exercise

**House style.** Plain and direct. Short sentences. Never "simply", "just", "easily",
"seamlessly", "robust". Say why, never what — a sentence restating the one above it is worse
than no sentence. The good lines here name the failure mode that made something take its shape.
No marketing, no emoji outside the status glyphs the template asks for. Read `CLAUDE.md` and one
existing use case — `docs/ai/usecases/modules/sqlrepo/UC-003-partial-update-absent-vs-null.md` —
to hear the register.

**Decisions are binding.** `docs/ai/decisions/` outranks any DX proposal. A proposal may
challenge a `D-NNN`, but it must name it and say plainly that it is a challenge. A proposal that
silently violates a decision is the worst thing that can go in these files.

**A sweep is not a use case.** A `UC-NNN` names no file paths. A sweep may and should cite
`file:line` — but keep citations inside the Evidence fields, not in the consumer-facing prose.

**What may be run:** `grep` / `rg` / `find` / `sed` / `cat` / `ls`, and `go doc`. Read files
freely. Do **not** run `make`, `go test`, the test suites, or anything that starts a database —
many agents run at once and the containers are shared.

---

## 5. Where the original prompts live

The workflow script that produced the happy half, with the exact prompt text for every agent
role, is at:

```
/home/user/.claude/projects/-home-user-ws-frostgrove-vv/c1e8632f-3b17-4b53-80cd-626dff8e2f0d/workflows/scripts/usecase-release-sweep-wf_d3ff2240-b02.js
```

It also holds the `MODULES` table — for each module its remit, the module doc pages to read in
step 1, and the source directories to read in step 4. Read that table rather than guessing which
packages a sweep covers; several sweeps span more than one (`faults` covers faults, sqlfault,
probe and catalog; `crudhttp` covers crudhttp, crudnet, crudfiber and crudgin).
