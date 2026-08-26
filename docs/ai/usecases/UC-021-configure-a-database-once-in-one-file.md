# UC-021 — Configure a database once, in one file

**Actor:** the application author
**Covered by:** [[FL-021]]

## Scenario
The service is deployed three times — a developer's laptop, staging, production
— and each deployment points at a different server, with different credentials
and a different pool size. The author wants the database described in the same
configuration file as everything else the service is configured with, in fields
an operator can read, and wants the identical file to work whether the service
is on PostgreSQL, MySQL or MariaDB.

The failure this exists to prevent is not an error. A connection string
assembled by hand is wrong in ways that connect anyway: a password with a
punctuation mark in it that one engine's parser reads as a delimiter, a
parameter holding a slash that moves where the driver thinks the database name
ends, a timeout rounded down to zero that means "wait forever" rather than
"half a second".

## What must hold

1. One set of fields describes any of the four engines. Moving a service from
   PostgreSQL to MySQL is an edit to the configuration file, not to the program.
2. The configuration is an ordinary struct with YAML and environment tags, so
   the loader the service already uses fills it with no glue, and it nests
   inside the application's own configuration rather than replacing it.
3. A credential survives the trip whatever it contains. A password holding the
   characters that mean something to a parser reaches the server unchanged.
4. A parameter or a database name that would be misread is escaped as the
   engine's own parser expects, and the parser is what proves it rather than a
   rule this project invented.
5. A setting an engine cannot express is refused by name, before anything
   connects. It is never quietly dropped and never downgraded to something
   weaker that looks similar.
6. A configuration that names two contradictory sources for the same fact is
   refused and both are named.
7. Every refusal happens at start-up. A configuration that is wrong stops the
   process before it takes traffic.
8. The pool is part of the same description, and a limit left unset stays the
   default rather than becoming a limit of zero.
9. A read replica is described as the difference from the primary, and inherits
   everything it does not restate.
10. The library never opens a connection on the author's behalf as a side
    effect of anything else. The application opens it, holds it, and hands it
    over — which is what keeps the repository free of a connection lifetime it
    did not create.
11. None of this is required. A service that already builds its own handle
    passes it to an adapter exactly as before.
