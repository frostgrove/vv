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
2. Running it with no arguments lists the available commands.
3. `migration <name>` creates a timestamped Goose SQL file and never requires a
   database connection.
4. A uniquely matching Go model supplies its mapped scalar fields, primary key,
   nullability and common SQL types; relation fields are excluded.
5. When several models are equally likely, an interactive terminal offers a
   searchable choice. Non-interactive execution creates an empty editable
   migration instead of guessing.
6. `--empty` skips model discovery, and `--model` makes the model choice explicit.
7. Generation speaks PostgreSQL, MySQL, MariaDB and SQLite syntax and never
   overwrites an existing migration created in the same second.
8. `migrate`, `status`, `rollback [count]` and `fresh` operate on the configured
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

**covered.** Source discovery, all CLI branches, dialect rendering, collision
handling and the non-interactive ambiguity rule are unit-tested. The full
migrate/status/rollback/fresh lifecycle runs against SQLite through the real
Goose provider; engine-to-provider mapping covers the other three engines.

