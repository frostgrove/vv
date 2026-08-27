# UC-022 — Generate and run database migrations from the application config

**Actor:** the application author
**Covered by:** [[FL-022]]

## Scenario

The application already has one typed database block and Go structs describing
its rows. The author wants a tiny standalone migration command which uses that
same configuration, can create a migration without a running database, and can
apply or roll back the files without installing a second CLI globally.

For the common case, naming `users` should be enough to find `User` and produce
the columns. Automation must remain safe: CI cannot stop for a prompt, and two
equally plausible `User` structs must not turn directory order into schema.

## What must hold

1. The application's migration entrypoint consists of loading its existing
   configuration and passing the database block to one command function.
2. Running it with no arguments in a terminal opens a searchable command menu
   and asks for the selected command's inputs. Non-terminal execution and
   `--no-interactive` list the available commands without prompting.
3. `migration <name>` creates a timestamped editable Goose SQL file and never
   requires a database connection. Its optional `--tables` (or second
   positional list) makes model generation explicit in that one file.
4. `table <name[,name...]>` is the model-inference shortcut and creates one
   migration per requested model. A uniquely matching Go model supplies its
   mapped scalar fields, primary key, nullability and common SQL types; relation
   fields are excluded.
5. When several models are equally likely, an interactive terminal offers a
   searchable choice. Non-interactive execution creates an empty editable
   migration instead of guessing.
6. `--empty` skips table-model discovery, and `--model` makes the one-table
   model choice explicit.
7. Generation speaks PostgreSQL, MySQL, MariaDB and SQLite syntax and never
   overwrites an existing migration created in the same second.
8. `init` makes an all-model `*_init.sql` baseline and replaces its existing
   file while preserving its version. `migrate`, `status`, `rollback [count]`
   and `fresh` operate on the configured
   migration directory and history table. `fresh` means all tracked downs
   followed by all ups; it does not drop untracked tables.
9. Database commands always use the primary. A declared read replica is never
   opened for schema changes.
10. The migration directory, source roots and history table have useful
    defaults and can be supplied by YAML or environment like the rest of the
    database block.
11. A configuration-path flag already consumed by configuration loading remains
    valid when the migration CLI parses the same arguments.

## Status

**covered.** Source discovery, the no-argument interactive menu, all direct CLI
branches, dialect rendering, collision handling and the non-interactive
ambiguity rule are unit-tested. The full
migrate/status/rollback/fresh lifecycle runs against SQLite through the real
Goose provider; engine-to-provider mapping covers the other three engines.
