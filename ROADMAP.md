# Roadmap

Two documents, split because they answer different questions, change for
different reasons, and one of them has a deadline.

| | [The framework](ROADMAP-framework.md) | [The error subsystem](ROADMAP-errors.md) |
|---|---|---|
| **Answers** | where code lives, and what it may import | what a failed write tells the client |
| **Changes when** | a subsystem is added or a module boundary moves | the error contract or the probe changes |
| **Deadline** | **the first tag** — after `v0.1.0` module paths and names cost a deprecation cycle instead of a `sed` | none; it is phased work |
| **Size** | 12 sections | 16 sections, 9 phases — **phase 0 done** |

**Read the framework one first.** Every package the error roadmap proposes lands
somewhere in that structure, and the module decisions there are the ones that
stop being free.

---

## What is settled

- The repository is **`github.com/shardit-io/vv`** — done. `rx` meant ReactiveX
  to every Go search there is.
- **`errs` gets its own module and its own version line**, with [[D-033]] amended
  from *no external requirement* to *no third-party requirement* — but **not
  before the first tag**. [[D-036]] measured why: split earlier, every module
  requiring the root fails to walk its module graph, and no `replace` or
  workspace edit fixes it. It ships as a package until the tag.
- Packages are named for what they are; a prefix appears only to break a
  collision, and **the prefix names the subsystem** — `crudfiber`, `i18nfiber`,
  not `vvfiber`.
- A directory either roadmap names but has not implemented carries a `TODO.md`
  and nothing else, deleted in the same change that adds the first `.go` file.

- **Four engines**, not three: `mariadb:11.4` joined the compose file with error
  roadmap phase 0. `crud.MySQL` had claimed to target MariaDB since it was
  written and had never been run against it.
- **The classifier is keyed on `(dialect, sqlstate, native)`** — [[D-046]]. No
  arm of it is a SQLSTATE-class test, because three of the four engines break
  that assumption in three different ways.

## What is done that was open

1. ~~**The enforcement.**~~ `make check` — deps, tiers, TODO placeholders,
   per-module tidiness — each proven by breaking it. Framework roadmap §7.
2. ~~**Two live bugs.**~~ Both fixed, and the corpus found a third and larger
   one on its first run: **every SQLite constraint violation was an unclassified
   500**, all seven classes, for as long as the dialect had been supported.
   SQLite reports no SQLSTATE, and the one test that would have caught it walks
   a target list SQLite is not on.

## What is open, in priority order

1. **Error roadmap phase 1** — `errs/` itself: `Code`, `Kind`, `Path`,
   `Violation`, `Fault`, the SPI, the message source. Nothing blocks it.
2. **Whether the organisation is renamed too.** Framework roadmap §12.
3. **The dependency-diff gate** — snapshot `go list -deps` per public package and
   fail on any change, in the shape grpc-go uses. Framework roadmap §7 names it;
   it needs CI, and there is none.

## Conventions

Both documents use `§N` for their own sections and name the other document
explicitly when they cross. `[[D-NNN]]`, `[[UC-NNN]]` and `[[FL-NNN]]` point into
`docs/`, and a decision that does not exist yet is written as *the proposed
D-NNN* rather than as a link, so nobody follows it to nothing.

A decision may exist before the code it governs. Those carry **`in force from
phase N`** in their status line and head their evidence `Proven by (owed)`, so a
reader can tell a rule the tree obeys from a rule the tree owes — and so an agent
checking that every symbol a doc names still exists knows which absences are
deliberate. D-038 and D-041 through D-045 are the current set.
