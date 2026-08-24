# vv documentation

Four sections, three of them cross-linked. Start with the one that matches your
question rather than reading in order.

| I want to know | Go to |
|---|---|
| Why is it like this? May I change it? | [decisions/](decisions/Index.md) |
| What does a consumer need? What must hold? | [usecases/](usecases/Index.md) |
| Where does this happen? Which files? | [flows/](flows/Index.md) |
| How do I set this up in my project? | [usage-guides/](usage-guides/) |

## How the three sections relate

```
                 ┌─────────────┐
                 │  decisions  │  why it is this way, what must not change
                 │    D-NNN    │  ← binding
                 └──────┬──────┘
                        │ constrains
          ┌─────────────┴─────────────┐
          ▼                           ▼
   ┌─────────────┐            ┌─────────────┐
   │  usecases   │ ─covered→  │    flows    │
   │   UC-NNN    │    by      │   FL-NNN    │
   └─────────────┘            └─────────────┘
   what must hold,            where it happens,
   in the consumer's          in which files
   language
```

**A use case links only to flows** — no file paths, no symbols. That is what
lets a use case survive a refactor: a flow goes stale when a file moves, a use
case only when the product changes.

**A flow is the only place file paths and symbol names appear.** If you are
writing down where something lives, you are writing a flow.

**A decision is binding.** Most of them exist because the obvious alternative
was tried and produced a silent bug. If you think one is wrong, say so and name
it — do not implement around it.

## Working agreement

`CLAUDE.md` at the repository root carries the rules an agent follows: look here
before the code, and update these files in the same change as the code, without
being asked. Read it once at the start of a session.

## Usage guides

Task-oriented, for a consumer integrating the library into an existing project.
Both lead with what you get and only then how to set it up.

- [usage-guides/ent.md](usage-guides/ent.md) — ent's generated entity struct is
  the model, as-is
- [usage-guides/gorm.md](usage-guides/gorm.md) — your gorm struct is the model,
  `gorm.Model` and associations included
