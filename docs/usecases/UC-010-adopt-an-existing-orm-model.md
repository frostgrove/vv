# UC-010 — Adopt an existing ORM's model without changing it

**Actor:** the application author of a project already built on ent or gorm
**Covered by:** [[FL-004]] [[FL-003]] [[FL-009]] [[FL-010]]

## Scenario
The project has schemas, migrations and typed query builders that work. What it
does not have is twenty resources' worth of list/get/create/patch/delete
handlers. The author wants to keep the ORM for what it is good at and take the
mechanical layer from somewhere else — and the price of that has to be zero
changes to the model. Not "add a few tags", not "write a parallel struct": the
generated entity or the existing gorm struct, as it stands, with the tags it
already carries for somebody else.

## What must hold

1. An ORM's generated entity struct is usable as a model as it is: no second
   struct, no added tags, no edits to generated code.
2. A struct that carries another library's tags is not confused by them. Tags the
   library does not own are ignored, not misread.
3. Fields that are none of the library's business are skipped rather than
   mis-mapped: unexported fields, embedded all-unexported config structs, and
   struct-shaped fields that carry no relation tag. In particular an ORM's own
   eager-loading holder is ignored, so it is never mistaken for a column.
4. An embedded base struct — the ORM's own id/timestamps mixin — is flattened
   into its columns, and a composite type the ORM treats as one column (its
   soft-delete timestamp) stays one column.
5. Column names derive from the Go field names by the same convention the ORMs
   use, so for a normal schema the derived names and the ORM's own names agree.
6. Because they are *derived* and not read from the ORM, the author can prove
   they agree: the model's column list and table name are readable at build time
   and can be diffed against the ORM's own constants in a test. A schema field
   whose name does not follow the convention is then a failing test, not a
   failing query.
7. Pointer fields on the ORM's struct are recognised as nullable columns, and the
   generated update DTO gives them three states rather than two.
8. The two libraries share one connection and one transaction. A transaction the
   ORM opened is joined by a single call, in both directions, and a rollback takes
   both halves (UC-005).
9. Relations declared for this library sit alongside the ORM's own association
   tags on the same struct. The ORM ignores the one and this library ignores the
   other.
10. The primary key type is honoured whatever it is, including a uuid: the key is
    bindable, filterable, `in`-listable, indexable by a preload, and coercible
    from the string a client sends.
11. Where the ORM's key is generated in Go rather than by the database, a save
    with an unset key is refused rather than writing a zero key.

## Out of scope

- **The ORM's callbacks.** A write issued here is one statement sent to the
  driver; it does not pass through the ORM's builder, so the ORM's hooks, privacy
  rules, interceptors and **Go-side field defaults do not run**. This is the
  single largest thing to know before adopting, and it cuts both ways: a `created
  _at` defaulted in Go gets a zero value, and a `Default(true)` gets `false`. Put
  such defaults in the database, in a create hook, or keep creates on the ORM's
  builder.
- **The ORM's eager loading.** Its association holder is ignored (guarantee 3),
  so the ORM's edges are not this library's relations. A resource that needs
  relation filtering and preloading over the wire declares relations — on the
  same struct where that is possible, on a thin struct over the same table where
  the generated code cannot be touched.
- **The ORM's soft deletes.** They live in the ORM's builder, so they must be
  restated as a repository-level rule. That is UC-016.
- **Tags the ORM's generated struct cannot carry.** `immutable`, `generated` and
  the version column are declarations this library needs and a generated entity
  has nowhere to put. A resource that needs them needs a struct the author owns.
- **Migrations and schema ownership.** They stay with the ORM.
- **ORMs other than ent and gorm.** Anything speaking `database/sql` or pgx binds
  as a datasource, but "your struct is the model, as-is" is proven for these two.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-004]] | which Go fields become columns, which become relations, and which are skipped |
| [[FL-003]] | the write path that bypasses the ORM's builder, and what that means for defaults |
| [[FL-009]] | sharing the ORM's connection and its transaction |
| [[FL-010]] | generating the update DTO into the author's own package, next to the ORM's generated code rather than inside it |

## Status
**covered for ent and gorm.** Both are executed against live PostgreSQL and
MySQL: the generated entity and the gorm struct as models, the column list
diffed against each ORM's own constants, reads and writes through the DSL,
relations and preloads, and a shared transaction in both directions. The
uuid-keyed shape has its own suite, including the refusal in guarantee 11.

Two honest limits.

**The callback gap is proven on one side only.** That an ORM hook does not fire
on a write from here is executed for gorm — a real hook on a real model, counted
and observed not to run. For ent it is reasoned, not executed: no schema in the
test tree declares a hook, so the claim rests on it being the same code path. The
Go-side-default half *is* executed for both.

**Guarantee 6 is a recipe, not a mechanism.** Nothing detects a name that
disagrees with the ORM's; the library exposes what it derived and the author
writes the comparison, once per entity. A project that does not copy that test
finds out at runtime.
