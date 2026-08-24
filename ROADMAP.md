# Roadmap

Two documents, split because they answer different questions, change for
different reasons, and one of them has a deadline.

| | [The framework](ROADMAP-framework.md) | [The error subsystem](ROADMAP-errors.md) |
|---|---|---|
| **Answers** | where code lives, and what it may import | what a failed write tells the client |
| **Changes when** | a subsystem is added or a module boundary moves | the error contract or the probe changes |
| **Deadline** | **the first tag** — after `v0.1.0` module paths and names cost a deprecation cycle instead of a `sed` | none; it is phased work |
| **Size** | 12 sections | 16 sections, 9 phases |

**Read the framework one first.** Every package the error roadmap proposes lands
somewhere in that structure, and the module decisions there are the ones that
stop being free.

---

## What is settled

- The repository becomes **`github.com/shardit-io/ordo`** — decided, not yet
  executed. `rx` means ReactiveX to every Go search there is.
- **`errs` gets its own module and its own version line**, with [[D-033]] amended
  from *no external requirement* to *no third-party requirement*.
- Packages are named for what they are; a prefix appears only to break a
  collision, and **the prefix names the subsystem** — `crudfiber`, `i18nfiber`,
  not `ordofiber`.
- A directory either roadmap names but has not implemented carries a `TODO.md`
  and nothing else, deleted in the same change that adds the first `.go` file.

## What is open, in priority order

1. **The enforcement, before anything is built.** [[D-033]]'s own proof command
   prints 17 lines on a clean tree, and `go.work` hides a root-module dependency
   leak from `make unit` and `make vet` entirely. Framework roadmap §7.
2. **Two live bugs this work uncovered**, neither of them roadmap items: a MySQL
   `CHECK` violation is `HY000`, so `crudsql` never classifies it and a client
   gets a bare 500 where [[FL-011]] promises 409; and `test/go.mod` is untidy —
   it will not build with `GOWORK=off`.
3. **Whether the organisation is renamed too.** Framework roadmap §12.

## Conventions

Both documents use `§N` for their own sections and name the other document
explicitly when they cross. `[[D-NNN]]`, `[[UC-NNN]]` and `[[FL-NNN]]` point into
`docs/`, and a decision that does not exist yet is written as *the proposed
D-NNN* rather than as a link, so nobody follows it to nothing.
