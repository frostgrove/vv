# utils/vvdb · utils/vvdb/dbpgx — one configuration file becomes the handle the application owns

**Covers:** `github.com/frostgrove/vv/utils/vvdb`, `github.com/frostgrove/vv/utils/vvdb/dbpgx`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — the original sweep still contains unresolved connection-lifecycle and portability cases, while its secret-rendering and typed-TLS blockers are now closed by [[D-081]]. The detailed cases and blocker table below distinguish resolved rows from remaining work; they, rather than the historical source line numbers, are the current status.

> **Provenance during remediation:** the long-form narrative and unmarked
> present-tense findings were captured against the pre-remediation baseline.
> Only cases and table rows explicitly marked `✅`, `Closed` or `resolved` have
> been revalidated against the current worktree; every other row remains a
> work queue item until its feature batch receives the same code-and-review pass.

## What a consumer is actually trying to do

Somebody has a service that runs in three places — a laptop, staging, a
production cluster — and one file that describes it. They want the database in
that file, in fields an operator can read at three in the morning, and they want
the same file to work when the company moves the service from PostgreSQL to
MySQL because a different team already runs MySQL. They do not want the
connection string in Go, because the last time it was in Go somebody committed a
staging password to a public repository.

The failure they are trying to avoid is not an error message. It is the
connection that succeeds and is wrong. A password rotated to something with an
`@` in it, and half of it becomes the hostname. A time zone parameter with a
slash in it, and the driver decides the database is called `Moscow`. A
`connect_timeout` written as half a second, rounded down to zero, and zero means
wait forever. A host that renders empty from a Helm template, and the pod
connects to a database inside itself. Every one of those looks like a network
problem for a day.

They also believe the file is the description. It is not the only one. Every
PostgreSQL driver reads `PGHOST`, `PGSSLMODE`, `PGOPTIONS`, `PGSERVICE` and
`~/.pgpass` underneath whatever string it was handed, so a base image, a CI
runner or a laptop can change the connection without anybody editing the file.
A module whose signature refusal is "two sources of truth" ships a third one and
never mentions it.

Then the first week arrives and the handle has to do work. Migrations run
against the same database from the same file. On a shared cluster the service
does not own `public`, so the schema it lives in has to come from somewhere. The
process has to decide what to do when the database is not up yet, and it has to
shut down without panicking on the handle it never opened.

Then the second month arrives and the shape changes. The dashboard is hammering
the primary, so a replica appears. The job fleet opens two hundred workers
against a pool of twenty. The managed provider turns on TLS and hands over a CA
bundle, then fails over to a new node at two in the morning and leaves every
pooled socket pointing at the old one. The platform team says credentials now
come from a mounted file, not the config, and rotate every fifteen minutes.
Someone adds a second database for analytics. None of that should mean rewriting
`main`.

Underneath it all is one thing they will not trade: the handle is theirs. They
open it, they close it, they decide when the process gives up on a dead server.
A library that opens a connection behind their back cannot be adopted into a
service that already has one, and that is the whole reason this piece exists
separately from everything it feeds.

## Happy cases

### H-VVDB-01 — The first afternoon: the database is a file, not a constant
**Who:** a backend engineer standing up a new service on PostgreSQL
**Wants:** the connection described where everything else about the service is described
**Story:** They add a `db:` block to the YAML the service already loads, call one
function in `main`, and hand the handle to the repository. They leave `host` out
by accident while copying the block, and want the process to say which line to
edit rather than start.
**Must hold:**
1. The database block is ordinary YAML with ordinary keys, nested inside the application's own config rather than replacing it — and it decodes, including the durations and the engine name.
2. `main` grows one line for the handle and one for the hand-over, and closing it stays the application's business.
3. A field the engine cannot do without is refused at start-up and named.
4. Nothing is guessed: an engine spelled `postgresql` is a refusal, not a default.
5. A field left out is not filled in with a guess that connects. What is required is required.
**Today:** 🟡 partial — (2), (3) and (4) hold; (1) is unproven; (5) fails on `host`
**Evidence:** (2) `utils/vvdb/open.go:20` is `Open` and `:43` is `MustOpen`. (4)
`TestAnUnknownEngineIsRefused` (`utils/vvdb/dsn_test.go:245`) pins that
`postgresql`, `PostgreSQL` and `""` are all refusals, driving `vvdb.DSN` — the
function the consumer's path actually reaches.
(3) holds, and the evidence has to be named carefully. The route a consumer
takes is `Open`→`DSN`→`prepare`→`validateFields`, pinned by
`TestOpenRefusesBeforeItReachesTheDriver` (`utils/vvdb/open_test.go:83`) — which
asserts the sentinel and never reads the message. The test that proves the field
is *named*, `TestValidateNamesTheFieldThatIsWrong`
(`utils/vvdb/config_test.go:114`), drives `cfg.Validate()`, and nothing on a
consumer's path calls it (blocker 3). The refusal does name the field, because
`validateFields` is the same function; the proof runs on a path nobody executes.
(1) rests on struct tags and nothing else. `utils/vvdb/config.go:46-94` carries
`yaml` and `env` tags, and **nothing in this repository ever decodes a `db:`
block into a `vvdb.Config`.** `grep -rn vvcfg --include="*.go" .` reaches only
`utils/vvcfg/*` and one comment at `_examples/pgx-fiber/main.go:62`; there is no
YAML file anywhere under `_examples/`; all three examples hold the config as a Go
literal (`_examples/sql-nethttp/main.go:65`, `_examples/pgx-fiber/main.go:65`,
`_examples/gorm-mysql-gin/main.go:73`), which is the practice this case exists to
replace. `max_lifetime: 30m` into a `time.Duration`, `engine: postgres` into a
named string type and env-over-file precedence are all decoder behaviour, and
this is the same standard H-VVDB-08 refuses for SQLite: a rule this repository
wrote, agreeing with itself.
(5) fails on the field that costs the most. `validateFields` requires `name` and
nothing else for a server engine (`utils/vvdb/config.go:205-207`); `PostgresDSN`
substitutes `localhost` (`utils/vvdb/dsn.go:70-72`) and `mysqlish` does the same
(`:121-123`). A `db:` block with no `host` starts and connects to whatever is
listening inside the pod. Read with the sibling sweep's blocker 8 — an exported
`DB_HOST=` blanks what the file said
(`docs/ai/usecases/modules/utils/Utils.md`, blockers row 8) — a Helm template that renders an
empty value produces a production pod that boots, points at itself and says
nothing. A value that is present and *wrong* — a misspelled `name`, a stale
`host` — is nobody's here at all; the earliest it can be caught is a ping, which
is H-VVDB-14.
**If not ready:** it very likely decodes — cleanenv handles both — but the
module's headline is *one configuration file becomes the handle*, and the loading
half of that sentence has never been executed. One test that writes a temp YAML
holding every field including a duration and a `Secret`, loads it through
`vvcfg.Load` and compares the struct closes (1), with no container. The examples
are the second half: one of them should load a file. (5) is two lines in
`validateFields` — `host` is required for a server engine — or one sentence in
the module doc saying the default is `localhost`, and the first is better,
because the whole point of the closed sets is that nothing is guessed.

### H-VVDB-02 — The same binary in staging and production, configured only by the environment
**Who:** whoever writes the deployment manifest
**Wants:** no config file in the image; the platform's secrets become the connection
**Story:** The image ships with defaults for local development. In the cluster,
`DB_HOST`, `DB_USER` and `DB_PASSWORD` come from a secret. The managed provider
hands out one URL instead, so a second environment sets `DB_DSN` and expects it
to win. A month later the replica's hostname is rotated from the same secret
store.
**Must hold:**
1. Every field an operator would set has an environment name — including inside `params` and inside the replica.
2. The names follow one pattern, so an operator can write the manifest without reading the struct.
3. A provider's finished URL can be supplied whole, and what it would contradict is refused rather than half-applied.
4. What is set in the environment overrides what is in the file.
5. A deployment that has no file at all still starts.
**Today:** 🟡 partial — (3) and (4) hold; (1) fails three ways; (2) fails once; (5) is another module's and is broken there
**Evidence:** (3) `prepare` refuses a DSN set beside a field it would override
(`utils/vvdb/dsn.go:179-191`), pinned by
`TestADSNIsUsedAsGivenAndRefusesToShareTheJob` (`utils/vvdb/dsn_test.go:228`).
(4) is cleanenv's: the file pass runs first and the environment pass after
(`.../cleanenv@v1.5.0/cleanenv.go:97-104`).
(1) fails in three separate places, and they are three separate fixes:
- `Params` has **no** `env` tag (`utils/vvdb/config.go:69`), so `application_name`, `sslrootcert`, `search_path` and `statement_timeout` are file-only.
- `Replica` is a `*Config` and cleanenv walks struct fields but not pointer fields (`.../cleanenv@v1.5.0/cleanenv.go:337`). This is worse than "a replica cannot be declared from the environment": there is no environment name for the replica's host **even for a replica the YAML already declares**, so an operator rotating that hostname or its read-only credential through the platform's secret store has no route at all.
- The `env` names are fixed strings, which is H-VVDB-09's problem and is listed there.

(2) fails once and it is the kind of thing found by typing: four pool fields are
`DB_POOL_MAX_OPEN`, `DB_POOL_MAX_IDLE`, `DB_POOL_MAX_LIFETIME`,
`DB_POOL_MAX_IDLE_TIME` and the fifth is `DB_CONNECT_TIMEOUT`
(`utils/vvdb/config.go:89-93`). One field in the same struct breaking the prefix
means a manifest that sets `DB_POOL_CONNECT_TIMEOUT` is ignored in silence,
which is the failure shape everything else here is built to refuse.
(5) does not hold, and it is not this module's to hold: `vvdb` loads nothing.
The sibling sweep owns the fix site and rates it serious —
`docs/ai/usecases/modules/utils/Utils.md`, H-UTILS-05 ("Run the same binary in a container with no file at all"), blockers row 5
(`:482`). Recorded here because the premise of this case is "no config file in
the image", and a reader who stops at this file would otherwise conclude the
env-only deployment works.
There is also a live tension between (3) and (4): a file that carries
`host`/`name` for local development plus a production `DB_DSN` is `ErrConflict`,
so the escape hatch cannot be switched on by environment over a file that names
fields.
**If not ready:** the operator empties every field the file set, in the
environment, before `DB_DSN` will start — or maintains two files — and puts the
replica host back in Go. The `Params` gap is one tag plus cleanenv's map parsing.
The replica gap is not one tag: see **The DX this should have**, because the
obvious fix creates H-VVDB-09's collision inside a single config. The precedence
tension is a direct challenge to [[D-057]]'s "whole or absent" and needs the
decision amended rather than the code bent; it is stated as a challenge under
**What it must not break**.

### H-VVDB-03 — The password is rotated to something with punctuation in it
**Who:** the platform engineer running the secret manager
**Wants:** a generated password to reach the server unchanged, whatever it contains
**Story:** The rotation policy produces `p@ss/w:rd?&=#`. Nobody edits Go. The
service restarts and connects on PostgreSQL, and the same policy applies to the
MySQL service next door.
**Must hold:**
1. The password in the file is the password the server sees, on the engine the service is on.
2. A database name, a parameter or a file path containing a `/`, a `?` or a `#` does not move where the driver thinks the name ends.
3. A credential the config states is either delivered or refused, never dropped.
**Today:** 🟡 partial — proven on PostgreSQL and MySQL, unproven on SQLite, and one hole that is on the two engines that carry the traffic
**Evidence:** (1) and (2) hold on two of four engines, and hold at a level most
libraries never reach: `TestPgxReadsBackWhatVvdbWrote` and
`TestTheMySQLDriverReadsBackWhatVvdbWrote` (`test/dsn/dsn_test.go:24`, `:70`)
parse the string back with the parsers that decide — not with a rule this
repository wrote — with `TestAnUnescapedParameterIsWhyTheEscapingExists`
(`:125`) as the control that the naive form really is rejected. The MySQL colon
rule is `utils/vvdb/config.go:214-218`, pinned by `TestAMySQLUserCannotHoldAColon`
(`utils/vvdb/dsn_test.go:274`). This half is the strongest thing in the module.
Two holes:
- (2) is not proven for SQLite. `SQLiteDSN` concatenates `"file:" + c.Path` with no escaping at all (`utils/vvdb/dsn.go:161-174`) — `url.Values.Encode` is applied to `params` and nothing is applied to the path — so a path holding `?`, `#` or a space is handled by no rule. `grep -n sqlite test/dsn/dsn_test.go` is empty: no real parser has ever read a SQLite string back. The control at `:125` is MySQL-only; there is no pgx twin.
- (3) fails on a shape both syntaxes share, **on PostgreSQL and MySQL**: a password with no `user` is silently dropped. `PostgresDSN` writes userinfo only inside `if c.User != ""` (`utils/vvdb/dsn.go:51-56`) and `mysqlish` writes the password only inside the same guard (`:104-113`). `validateFields` requires `name` and `path` and never mentions `User` except for the colon rule (`utils/vvdb/config.go:192-220`). With `user` unset, libpq falls back to the process's OS user and may well connect. Worse with H-VVDB-19: the config's password is dropped, `~/.pgpass` supplies a different one, and the process connects successfully as somebody else. `grep -rn 'User: ""' utils/vvdb/ test/dsn/` is empty.
**If not ready:** the escaping needs nothing on the two engines that carry the
traffic. The SQLite path wants either `url.URL{Scheme:"file", Opaque:…}` or an
explicit sentence saying the path is written verbatim, plus one `test/dsn` case
that opens it. The dropped password is four lines in `validateFields`: a
password without a user is a contradiction, and refusing it by name is what
every other field here gets.

### H-VVDB-04 — Size the pool for a job fleet
**Who:** the engineer who runs two hundred workers against twenty connections
**Wants:** the pool described in the same file, and connection counts that match what the file says
**Story:** They set `max_open: 20` and a `connect_timeout: 5s`, deploy, and watch
the connection count on the server. Later they set `max_idle: -1` for a
PgBouncer deployment where retaining idle connections is the thing they are
trying to stop. Later still they onboard the two hundredth tenant and want to
know what the fleet costs the server.
**Must hold:**
1. The pool limits in the file reach the handle.
2. A limit left unset stays the driver's default and does not become a limit of zero.
3. A limit the file *does* state is applied, including the negative that means something.
4. A pool section that cannot mean what it says — an idle floor above the open ceiling — is refused at start-up like any other field.
5. What the deployment costs a server is computable from the file: pods times handles times `max_open` is a number, and the multiplications are named.
**Today:** 🟡 partial
**Evidence:** (1) `utils/vvdb/open.go:82-95`, pinned by `TestOpenSizesThePool`
(`utils/vvdb/open_test.go:51`) and end to end by
`TestOneConfigShapeOpensEveryEngine` (`test/integration/vvdb_test.go:74-76`). (2)
`TestAnUnsetPoolLimitIsLeftAlone` (`utils/vvdb/open_test.go:70`) is the control
and it holds.
(3) does not hold, and `max_idle` is where it bites: `SetMaxIdleConns(-1)` is a
real setting meaning "retain no idle connections"
(`$GOROOT/src/database/sql/sql.go:962-973`, `n < 0` returns 0), which is exactly
what a PgBouncer or serverless deployment asks for. The `> 0` guard
(`utils/vvdb/open.go:86-88`) drops it, and `database/sql` then applies its
default of **2** — the operator gets the opposite of what they wrote, in silence.
On pgx the same guard (`utils/vvdb/dbpgx/dbpgx.go:97`) leaves `MinConns` at
pgx's default of 0 (`.../pgxpool/pool.go:20`), so `-1` has a third meaning
depending on the opener.
(4) does not hold: nothing validates `Pool` at all. `Validate`
(`utils/vvdb/config.go:142-167`) never looks at it, so `max_idle: 50` beside
`max_open: 10` is accepted; `database/sql` clamps it, and pgx does not — see
H-VVDB-07.
(5) has no answer here at all, and one is not obviously this module's: `Pool` is
per config, and nothing sums. The multiplications are real and each is one line
of YAML: a replica inherits the primary's pool whole (H-VVDB-05), a tenant per
config multiplies by tenants (H-VVDB-12), and a Deployment multiplies by
replicas. Two hundred tenants at `max_open: 20` is four thousand connections
against a server configured for a hundred, and the first symptom is
`FATAL: sorry, too many clients already` from a tenant that was fine yesterday.
The module is where the number is written, which makes it at least the place to
say the multiplication exists.
There is a sharper edge inside (1): `max_open: 20` on its own leaves
`database/sql`'s idle count at **2**
(`$GOROOT/src/database/sql/sql.go:960`), so a burst to twenty retains two and
tears down eighteen, and every one of those eighteen is a new backend process on
PostgreSQL the next time the burst comes. `pool: { max_open: 20 }` with no
`max_idle` is exactly the snippet all three documents print
(`README.md:178`, `docs/usage-guides/ent.md:783`, `docs/usage-guides/gorm.md:720`)
— and the gorm guide is worse than that, see H-VVDB-24.
**If not ready:** the consumer sets `max_idle` beside every `max_open` and knows
why, or does not. The idle default deserves a sentence in the module doc at
minimum; making `max_open` imply `max_idle` would be a new default and needs a
decision. The negative and the ordering both want a `Pool.validate()`, and the
order the three fixes have to land in is stated once in **The DX this should
have** — validating a pool section nothing calls closes an item the consumer
still hits.

### H-VVDB-05 — A replica appears, and Go does not change
**Who:** the engineer whose dashboard is hurting the primary
**Wants:** one line of YAML to send reads elsewhere
**Story:** They add `replica: { host: replica.internal }`, restart, and expect
the credentials, the database name, the TLS setting and the pool sizing to come
along. Later they give the replica its own `max_open` because the dashboard is
heavier than the API.
**Must hold:**
1. The replica inherits every field it does not restate.
2. A replica that restates one field keeps the rest of the primary's — including inside the pool section.
3. Both handles open or neither, and the failure names which of the two is wrong.
4. Declaring no replica produces no second handle, rather than a second handle on the primary.
5. A replica that is not the same engine as the primary never opens.
6. A replica the file declares is either opened or refused. It is never ignored.
7. What the second handle costs the server is visible: adding a replica states its own pool, or says whose it inherits.
**Today:** 🟡 partial — (6) is the largest hole in the module
**Evidence:** (1) and (4) hold: `ReadReplica` (`utils/vvdb/config.go:229-287`),
pinned by `TestAReplicaInheritsEverythingItDoesNotRestate`
(`utils/vvdb/config_test.go:20`) and `TestNoReplicaIsNotAnEmptyReplica` (`:83`).
(6) does **not** hold, and this repository's own front page is where a consumer
meets it. `Open` never calls `ReadReplica` (`utils/vvdb/open.go:20-36`), so a
declared `replica:` produces no second handle, no error and no log line — every
read keeps going to the primary and the dashboard keeps hurting it. It is not
hypothetical: `README.md:179` is `replica: { host: replica.internal }` inside a
`db:` block, and `README.md:183-184` is `vvdb.MustOpen(&cfg.DB)` and
`dbpgx.MustConnect(ctx, &cfg.DB)`, neither of which reads it.
`docs/modules/en/vvdb.md:54-55` pairs the same YAML with `:67`, and
`OpenReadWrite` appears eleven lines later at `:75`. Must-hold (4) covers the
inverse and is tested; this direction is covered by nothing.
(2) does **not** hold: the pool is copied whole or not at all —
`if r.Pool != (Pool{}) { base.Pool = r.Pool }` (`utils/vvdb/config.go:273-275`) —
so a replica that states only `max_open` silently loses the primary's
`max_lifetime`, `max_idle_time` and `connect_timeout`. Every other field
inherits per field; this one does not, and no test covers it. A replica given a
whole `dsn:` inherits no pool either (`:236-242`), and the pool is not part of
the string, so those settings vanish with nothing to carry them.
(3) is **unproven**. `TestOpenReadWriteOpensBothOrNeither`
(`utils/vvdb/open_test.go:103-133`) makes two calls — a valid replica, then
`cfg.Replica = nil` — and never drives `Open(r)` to an error. The
`_ = primary.Close()` and the `"replica: %w"` prefix at
`utils/vvdb/open.go:72-76` can both be deleted and that test still passes, which
is the vacuous proof [[D-020]] rules out. The `"replica:"` prefix is exercised
only on the `Validate` path (`utils/vvdb/config_test.go:97-111`), which
`OpenReadWrite` does not take.
(5) holds only if somebody calls `Validate`, and on this path nobody does — see
H-VVDB-21 and blocker 3. `dbpgx.ConnectReadWrite` does not have this hole,
because `Connect` re-checks the engine (`utils/vvdb/dbpgx/dbpgx.go:34-36`).
(7) does not hold, and the inheritance is what hides it: a replica that states
no pool takes the primary's whole `Pool` by construction (`config.go:273-275`),
so `replica: { host: replica.internal }` doubles what one pod costs a server
with a fixed `max_connections`. Six pods at `max_open: 20` against a managed
instance capped at 100 is the difference between fitting and
`FATAL: sorry, too many clients already`, and the one line that caused it looks
like a hostname.
There is a shape the module doc gets wrong: a primary described by `dsn:` with a
field-described `replica:`. `ReadReplica` clears `base.DSN` (`config.go:245`)
and the primary's fields are empty by construction, so the merged replica has no
`name` and start-up dies with `replica: vvdb: missing: name` for a database whose
name is sitting inside the primary's URL. The failure is at least loud, but
`docs/modules/en/vvdb.md:128` ("inherits everything it does not restate") is
false in this shape, and Neon and RDS hand out a URL per endpoint.
**If not ready:** the author merges the pool by hand, or repeats every pool field
on the replica, and the reader of the README gets no replica at all until
somebody reads `open.go`. Per-field pool inheritance is eight lines beside the
eight already there, plus the test that pins it. The atomicity gap is one test
with a driver name that fails to register on the replica only. (6) is either
`Open` refusing a config that declares a replica — "use OpenReadWrite" — or the
README and module doc showing the pair; refusing is better, because a document
that fixes one snippet does not fix the config a consumer wrote from it.

### H-VVDB-06 — TLS to a managed provider that has its own certificate authority
**Who:** whoever moves the service onto RDS, Cloud SQL or a managed MySQL
**Wants:** verified TLS described in the same file, on whichever engine they are on
**Story:** The provider hands over a CA bundle and says connections must verify
it. They set the mode in the config, point at the bundle, and restart.
**Must hold:**
1. The TLS mode is one word in the config, spelled the same way for every engine.
2. A mode an engine cannot express is refused by name, never downgraded to something weaker that looks similar.
3. Nothing else in the file can quietly weaken what the mode states.
4. Pointing at a CA bundle is possible without leaving the configuration file.
5. The refusal message tells the operator what to do instead, and following it works.
**Today:** 🟡 partial — (1), (2), (3) and (5) hold; (4) is portable only on PostgreSQL
**Evidence:** `tlsParam` refuses MySQL `verify-ca` rather than silently mapping
it to `skip-verify`, and `Config.validateParams` reserves PostgreSQL `sslmode`
and MySQL/MariaDB `tls` plus `allowFallbackToPlaintext` even when the typed mode
is empty. Therefore `Params` cannot weaken the verified default or a stated mode;
`TestParamsCannotOverrideTypedConnectionSettings` pins both families.
PostgreSQL can still name a provider CA through `params.sslrootcert`. The MySQL
driver requires `mysql.RegisterTLSConfig`, which is executable Go state rather
than a portable file value; the refusal now gives the coherent low-level route:
register the config and use a complete raw `Config.DSN` naming `tls=<name>`.
**If not ready:** a future typed certificate field needs a portable ownership
and reload contract before it can replace that raw-DSN escape hatch. Until
then, the framework refuses to pretend that one file alone registered a MySQL
`tls.Config`.

### H-VVDB-07 — pgx behind a connection pooler, with tracing on
**Who:** a team on PgBouncer with OpenTelemetry already wired
**Wants:** the pool from the same config, plus the two pgx settings a pooler forces
**Story:** They swap `vvdb.Open` for `dbpgx.MustConnect` to get a tracer, add the
tracer and switch the query exec mode so PgBouncer's transaction pooling does not
trip over prepared statements. They expect the `pool:` block they already had to
keep meaning what it meant.
**Must hold:**
1. Everything the shared config describes reaches pgx, and means on pgx what it meant on `database/sql`.
2. Anything the shared config cannot describe is reachable without abandoning it.
3. A config naming another engine is refused rather than coerced.
**Today:** 🟡 partial — (2) and (3) hold; (1) reaches pgx and changes meaning on the way
**Evidence:** (2) `Option` (`utils/vvdb/dbpgx/dbpgx.go:27`) runs after vvdb's
fields and before the dial — the right shape, and it covers the tracer and the
exec mode exactly. (3) `dbpgx.go:34-36`, pinned by
`TestAnotherEnginesConfigIsRefused` (`dbpgx_test.go:69`).
(1) is where the module's headline strains. Every field arrives
(`dbpgx.go:93-113`, `TestTheConfigReachesPgx` at `dbpgx_test.go:24`, with
`TestAnUnsetPoolLeavesPgxsDefaults` at `:53` as the control) — but **`max_idle`
inverts.** On `database/sql` it is a ceiling on retained idle connections
(`utils/vvdb/open.go:86-88`); on pgx it becomes `MinConns`, a floor pgx actively
dials up to at start-up in a goroutine
(`dbpgx.go:97-103`; `.../pgx/v5@v5.10.0/pgxpool/pool.go:333-337`,
`createIdleResources` at `:568-595`). A team that moves the same YAML from
`vvdb.Open` to `dbpgx.MustConnect` silently changes their steady-state connection
count — against a managed server with a low `max_connections`, or a serverless one
billed per connection, that is a production change nobody wrote down.
The inversion **is** pinned by name — `dbpgx_test.go:36-38` asserts
`got.MinConns` and says "MaxIdle should land on MinConns" — so the mapping is
deliberate and tested. What is missing is the sentence in the consumer's `pool:`
vocabulary saying the knob changes meaning between openers: the comment at
`dbpgx.go:98-101` says it and `docs/modules/en/dbpgx.md` says it, and neither is
where somebody editing YAML looks. That makes this a documentation gap, not a
test gap, and the fix belongs in the module doc and the guides rather than in a
`_test.go`.
It compounds H-VVDB-04's missing pool validation: `max_open: 10` with
`max_idle: 50` gives `MinConns` 50 over `MaxConns` 10, and `pgxpool.NewWithConfig`
validates neither (`pool.go:219-240`). Fifty goroutines start; ten connections are
built. puddle refuses the rest before touching the network once
`len(p.allResources) >= maxSize` (`.../puddle/v2@v2.2.2/pool.go:607-623`) and
pgx maps that refusal to nil (`pool.go:576-579`) — so the boot is quiet and
wrong rather than a dial storm, and the background health check retries the
top-up that can never be reached, once a minute, forever
(`pool.go:490` onward).
**If not ready:** the consumer reads the mapping table in the module doc, or
discovers it from a connection graph. Closing it is either a rename that admits
the two are not the same knob (`min_idle` beside `max_idle`, with the engines
taking the one they mean) or an explicit paragraph in both module docs and both
usage guides. A rename is a config-format change and belongs to the owner.

### H-VVDB-08 — SQLite for the test binary and the single-node deployment
**Who:** the engineer who wants integration tests without a container, and the one shipping an appliance
**Wants:** the fourth engine to be a real engine, not a spelling
**Story:** Tests point the same config struct at a temp file with WAL and a busy
timeout. The appliance build points it at `/var/lib/app.db` with `max_open: 1`
because concurrent writers on SQLite return "database is locked".
**Must hold:**
1. The same config shape opens SQLite, with the fields that do not apply refused rather than dropped.
2. The file, the journal mode and the busy timeout are all describable in the file, for whichever of the two SQLite drivers the consumer imported.
3. A `SELECT 1` runs on a SQLite handle opened from the same config shape as the other three engines.
4. The driver name is the consumer's, because two SQLite drivers register two different names.
**Today:** 🟡 partial — (4) holds; (1) is two fields short; (2) is false for the default driver; (3) is unverified
**Evidence:** (4) holds: `DriverName` defaults to `sqlite` and the `driver:`
field covers mattn's `sqlite3` (`utils/vvdb/config.go:114-127`), pinned by
`TestDriverNameDefaultsPerEngineAndIsOverridable` (`config_test.go:133`).
(1) is nearly true and the exception is exactly where a reader assumes the list
is exhaustive. `validateFields` (`utils/vvdb/config.go:193-203`) refuses `host`,
`user`, `password`, `name` and `sslmode` and requires `path`, pinned by
`TestAFieldThatBelongsToAnotherEngineIsRefused` (`utils/vvdb/dsn_test.go:253`) —
which asserts `err != nil` and never reads the message. **`Port` is never
checked**, so `engine: sqlite` with `port: 5432` is accepted and ignored, and
`SQLiteDSN` (`dsn.go:161-174`) never reads `c.Pool`, so a `connect_timeout`
written beside it is dropped in silence while both other builders write it
(`dsn.go:78-80`, `:143-145`). Two fields dropped, in the case whose must-hold is
"refused rather than dropped".
(2) is **false for the driver the module defaults to**, and it fails in the
shape this case's own story names. `Params` is `map[string]string`
(`config.go:69`) and `SQLiteDSN` writes it with `url.Values.Set`
(`dsn.go:166-169`), so one key carries one value. `modernc.org/sqlite` — which
is what `DriverName` picks — takes pragmas as *repeated* `_pragma=` entries
(`.../sqlite@v1.54.0/sqlite.go:214-237`), so WAL **and** a busy timeout cannot
both be written. And `applyQueryParams` (`sqlite.go:207-293`) has no default
branch: mattn's `_busy_timeout=5000` spelling is accepted by the URL and
discarded by the driver without a word. The pragma spelling therefore differs
between the two drivers the `driver:` field exists to support, and nothing here
says so: `grep -rn '_pragma\|busy_timeout\|multiStatements' .` is empty.
(3) is **unverified**: nothing in this repository ever opens SQLite through
`vvdb`. The integration suite that does use SQLite calls `sql.Open("sqlite", …)`
directly (`test/integration/catalog_test.go:92-99`), the engine is absent from
`TestOneConfigShapeOpensEveryEngine` (`test/integration/vvdb_test.go:40-50`, three
rows), and no example uses it. The only evidence for a quarter of the module's
headline claim is a string comparison against a rule this repository wrote.
**If not ready:** the consumer writes the SQLite string by hand the first time
they need two pragmas, and finds out about the third-party spelling by getting a
default journal mode from a config that names WAL. One test in `test/integration`
that opens a temp file through `vvdb.Open` and runs `SELECT 1` closes (3), needs
no container, and would also give H-VVDB-03's unescaped path somewhere to be
caught. (2) needs either `Params map[string][]string`, a `pragmas:` list, or a
paragraph saying plainly that `params` is one value per key and which driver
spells pragmas which way — and the paragraph is H-VVDB-23's, because this is not
a SQLite problem, it is what `params` is.

### H-VVDB-09 — Two databases in one process
**Who:** the team whose service writes to the app database and reads an analytics one
**Wants:** the same struct twice, with the two never confusable
**Story:** They add `db:` and `analytics:` to the file, open two handles, bind
different repositories to each. In the cluster both are configured by
environment.
**Must hold:**
1. Two independent configurations coexist in one application config.
2. Each is configured separately from the environment; setting one never fills the other.
3. Getting it wrong is loud rather than a second handle quietly on the first server.
**Today:** 🟡 partial
**Evidence:** (1) holds — `Config` is a value struct with no global state
(`utils/vvdb/config.go:46`). (2) does not, by default: the `env` names are fixed
strings (`DB_HOST`, `DB_USER`, …, `:47-82`), so both nested copies read the same
variables and `DB_HOST` fills both. cleanenv supports an `env-prefix` tag on the
nested field (`.../cleanenv@v1.5.0/cleanenv.go:53`, applied at `:345` as
`sPrefix + prefix`), so ``Analytics vvdb.Config `yaml:"analytics" env-prefix:"ANALYTICS_"` ``
produces `ANALYTICS_DB_HOST` and works — but nothing in the module docs, the
README or either usage guide mentions it. (3) therefore fails in the exact shape
this module exists to prevent: a second handle connected successfully to the
wrong server, saying nothing.
**If not ready:** the consumer discovers `env-prefix` in cleanenv's README, or
does not. Two sentences in `docs/modules/en/vvdb.md` and a line in the vvcfg
usage guide close it; no code changes. **The tag is cleanenv's and is applied by
`vvcfg.Load`, so half this fix lands outside `vvdb`** — the sibling sweep carries
it (`docs/ai/usecases/modules/utils/Utils.md`, blockers row 24 — five cleanenv tags documented nowhere), so it is one fix in two
tables and should be closed once.

### H-VVDB-10 — Print the configuration at boot without printing the password
**Who:** every service that logs what it started with
**Wants:** `log.Printf("config: %+v", cfg)` to be safe
**Story:** On start-up the service logs its resolved configuration so support can
see what a pod actually got. The log ships to an aggregator that half the company
can read. A month later the same struct goes into an incident ticket as JSON, and
support asks somebody to paste the connection string the pod built.
**Must hold:**
1. Rendering the configuration — by any ordinary means, `%v`, `%+v`, `json.Marshal`, a slog attribute — does not reveal the credential.
2. That covers every field carrying one: `password`, the whole `dsn`, and any `params` key holding a secret.
3. The string the module builds can be shown to somebody without showing them the password inside it.
4. What is revealed is still useful: host, port, database, engine.
**Today:** ✅ holds
**Evidence:** `Config.Password` and `Config.DSN` are `Secret`; their `fmt`, JSON,
YAML/TOML text and `slog` projections return `[REDACTED]` while an explicit string
conversion still gives the connector its value (`utils/vvdb/secret.go`). The
named `Params` type redacts every display value because its open driver
vocabulary cannot prove which keys are public. `RedactedDSN` keeps the engine
target useful while removing userinfo and
secret query values. `TestSecretsStayOutOfOrdinaryRendering` drives `%v`, `%+v`,
`%#v`, JSON and a slog JSON handler; the vvcfg control proves YAML/env input
still loads and JSON/YAML/TOML output stays redacted; the two engine-shaped
`RedactedDSN` controls grep sentinel credentials out. [[D-081]] owns the
breaking value-type and safe-rendering contract.

### H-VVDB-11 — Expiring credentials and an instrumented driver, on a handle somebody else opened
**Who:** anyone on RDS IAM authentication, Vault-issued credentials, or `otelsql`
**Wants:** the password produced somewhere else, with everything else still coming from the file
**Story:** The platform mandates short-lived tokens minted per dial, so the
password is a function rather than a value. The host, database, pool sizes and
TLS mode stay in the file; only the credential comes from elsewhere. They also
want the driver wrapped so every query is a span.
**Must hold:**
1. The credential can be produced per connection while the rest of the configuration is still described once.
2. Reaching that far does not mean giving up the pool sizing the file describes.
3. The pool section applied by hand means the same thing it means inside `Open`.
4. A replica can have its own credential, because it usually has its own user.
**Today:** 🟡 partial — (1), (2) and (4) hold on pgx; `database/sql`
still has no connector/apply seam, and (3) remains partial there
**Evidence:** on pgx (1) and (2) are exactly what `Option` is for:
`pc.BeforeConnect` (`.../pgx/v5@v5.10.0/pgxpool/pool.go:123-125`, honoured at
`:236`) is reachable through `dbpgx.Connect(ctx, cfg, func(pc *pgxpool.Config) { … })`
(`utils/vvdb/dbpgx/dbpgx.go:33`). On `database/sql` there is no seam at all:
`Open` takes no options and builds one static string
(`utils/vvdb/open.go:20-36`), and the pool applier is unexported
(`:82`, lower-case `apply`). The consumer who needs a `driver.Connector` calls
`vvdb.DSN`, opens the handle themselves, and then has no supported way to apply
the `pool:` section they still have in their file.
(3) is not free either: `apply` covers four of `Pool`'s five fields, because
`ConnectTimeout` travels in the connection string (`utils/vvdb/dsn.go:78-80` and
`:143-145`), and a hand-built connector never saw a vvdb DSN. That is one
sentence, not three: **`connect_timeout` is the only pool field applied through
the string, so it is lost on every path where the string is not the one vvdb
built** — which is also why H-VVDB-15 loses it beside a supplied `dsn:`.
(4) now holds on pgx. `ConnectReadWrite` accepts only scoped
`ReadWriteOption`s: `Common(...)` is copied to the two independently parsed
configs first, then `Primary(...)` and `Replica(...)` are applied to their own
side (`utils/vvdb/dbpgx/dbpgx.go`). This lets tracing stay common while IAM or
role-changing hooks remain side-specific; a side-specific option deliberately
wins when both touch the same field. The declaration constructors snapshot the
caller's option slices, so later slice mutation cannot reconfigure either
pool. `TestReadWriteOptionsKeepCredentialsOnTheirDeclaredSide` pins the
separation and precedence, and
`TestReadWriteOptionConstructorsSnapshotTheirSlices` pins ownership.
**If not ready:** they re-implement four `SetMax…` calls from the config struct,
which is a copy of `apply` with the same zero-means-default rule that has to be
remembered rather than inherited. Exporting `func (p Pool) Apply(db *sql.DB) error`
in `vvdb` and `func Apply(pc *pgxpool.Config, p vvdb.Pool) error` in `dbpgx`
costs two renames and no dependency, and makes "the application opens the
connection" ([[D-057]]) something the module supports rather than merely permits.
The remaining work in this case is the `database/sql` connector/apply seam and
the `connect_timeout` part of (3); the pgx read/write credential cliff is
closed.

### H-VVDB-12 — One database per tenant, derived from one base configuration
**Who:** the SaaS with a database per customer
**Wants:** the base config in the file, the per-tenant part in code
**Story:** Onboarding a tenant means copying the config struct and changing the
database name. The rest — credentials, pool, TLS — must not be restated per
tenant. (The more common shape is a *schema* per tenant rather than a database,
and the config cannot describe that at all: that is H-VVDB-17.)
**Must hold:**
1. A configuration can be derived from another by changing one field, and deriving one does not mutate the one it came from.
2. Deriving two hundred of them is a number the operator can compute before the server refuses the two hundred and first.
**Today:** 🟡 partial
**Evidence:** (1) holds for every scalar and fails for one field: `Params` is a
map (`utils/vvdb/config.go:69`), so `c2 := cfg; c2.Params["application_name"] = tenant`
writes through to the original, and to every DSN built afterwards from either.
It does not reach handles already open — `sql.Open` freezes the string in a
`dsnConnector` value (`$GOROOT/src/database/sql/sql.go:879`, used at `:808-813`)
— so the blast radius is the next tenant onboarded, not the fleet.
`ReadReplica` gets this right for its own merge — it builds a new map
(`:276-285`) with `TestAReplicaOverridesRatherThanMerges`
(`config_test.go:44`) asserting the primary is untouched — so the rule is
understood and is not available to a caller.
(2) has no answer here at all, and it is the tenant instance of H-VVDB-04's
must-hold 5: `Pool` is per config and nothing sums.
**If not ready:** the consumer copies the map by hand, or does not, and finds out
when every tenant's `application_name` is the last one onboarded. A documented
sentence closes the aliasing, or a `func (c Config) With(...)` that copies the
map the way `ReadReplica` already does. The budget wants a paragraph in the
module doc rather than code — a per-tenant pool of one or two, and a pooler in
front.

### H-VVDB-13 — The line right after the hand-over: the engine is named a second time, in Go
**Who:** the engineer who moved the service from MySQL to MariaDB by editing one line of YAML
**Wants:** the promise UC-021 makes — moving engines is an edit to the file, not to the program
**Story:** `engine: mysql` becomes `engine: mariadb` in the config. The handle
opens against MariaDB. `main` still reads `crudsql.MySQL(db)`, because that line
was written a year ago and nothing points at it.
**Must hold:**
1. Naming the engine in the configuration is enough, or naming it twice is refused.
2. If the two can disagree, the disagreement is loud.
**Today:** ❌ missing
**Evidence:** the engine is named twice and nothing cross-checks the two.
`crudsql` writes its four engine strings as literals
(`crud/adapter/crudsql/crudsql.go:155-162`), the config writes its own
(`utils/vvdb/config.go:16-21`), and no code path sees both. This repository's own
integration test picks the constructor by hand beside the `vvdb.Engine` in the
same table literal (`test/integration/vvdb_test.go:47-49`), which is the
arrangement, not a slip.
The `mysql`/`mariadb` pair is where this is silent rather than loud. A
PostgreSQL dialect against MySQL fails on the first statement — `$1` is a syntax
error — but MySQL and MariaDB share a driver, a wire protocol and a dialect, so
`crudsql.MySQL` against a MariaDB server runs perfectly and classifies faults
with the wrong table. The two tables disagree on exactly two rows
(`errs/sqlerr/mariadb.go:5-18`): a failed CHECK is `23000`/`4025` on MariaDB and
`HY000`/`3819` on MySQL, and a bad column value is `22007`/`1366` against
`HY000`/`1366`. Classified with the wrong table those fall out of the map and
become a 500 where the contract says 422 — the failure shape this module's
opening paragraph is about, one line past its own boundary.
**If not ready:** nothing catches it, and [[D-057]] is why it is nobody's: `vvdb`
may not return a `crud.Source` and `crudsql` may not import `vvdb`, so neither
end can close it alone. The shape that would is `crudsql.For(engine string, db *sql.DB) (DB, error)`
taking a plain string — no import of `vvdb`, no relaxation of the closed set —
which the consumer feeds `string(cfg.DB.Engine)`. **That is a challenge to the
reasoning written beside the four constructors**, which cites [[D-046]] to say the
engine string "is a declaration and not something to be derived". It does not
touch D-046's invariant, which is about the classifier key. Raised, not
implemented around; **the fix site is `crud/adapter/crudsql`**, so this row
cannot be closed from this module. At minimum this module's doc should say the
engine is named twice and which line the second one is.

### H-VVDB-14 — The database is not up yet
**Who:** whoever owns the deployment: first deploy of the day, a compose stack starting in parallel, a managed instance restarting for a patch
**Wants:** to choose between "stop the process" and "wait for it", and to have the choice work the same on both transports
**Story:** They wire a readiness probe and expect start-up to be where a wrong
host or a stale password is discovered. Then the database is restarted for ten
seconds during a patch window and they need the pods to survive it.
**Must hold:**
1. There is one supported way to find out at boot that the server is not there.
2. It honours a timeout the config already states, and an unset timeout is not a deadline in the past.
3. It is the same shape on `database/sql` and on pgx, so moving between them does not mean unlearning a line.
4. "The configuration is wrong" and "the server is not up yet" are distinguishable, because one should crash and the other should be retried.
5. Which of "fail fast" and "wait with backoff" the module supports is stated, because the wrong one turns a ten-second restart into a crash-loop.
**Today:** ❌ missing, and on pgx it is worse than missing — see blocker 1
**Evidence:** on `database/sql` there is no helper: `Open` is documented as lazy
and points at `PingContext` (`utils/vvdb/open.go:17-19`). On pgx the opposite is
promised and is false. `pgxpool.NewWithConfig` creates the pool and starts
dialling in a **goroutine** — `.../pgx/v5@v5.10.0/pgxpool/pool.go:333-337`, with
`return p, nil` at `:339`; the same code in v5.7.6 at `:326` and `:332`, so it
has never been true in either version this repository can build. (The workspace
resolves v5.10.0, because `./test` requires it and `go.work` includes both;
`utils/vvdb/dbpgx/go.mod:8` pins v5.7.6 for a standalone consumer.) `Connect`
therefore returns a healthy-looking pool against a dead host, while
`dbpgx.go:31`, `docs/modules/en/dbpgx.md:26` and `:30`,
`docs/modules/ru/dbpgx.md:30-31` and [[FL-021]] (step 10 at `:75`, Traps at
`:139-141`) all say it dials. The module's own test connects to `127.0.0.1:1` and
discards the error (`dbpgx_test.go:16-29`), and the one test named for the
guarantee, `TestConnectRefusesBeforeItDials` (`dbpgx_test.go:78`), asserts a
*configuration* refusal and never reaches the network — so nothing catches the
drift.
(4) and (5) are unanswered in every document. Nothing says whether a transient
outage should stop a deployment, and the module doc's silence leaves a consumer
to invent it.
**If not ready:** the consumer writes the ping themselves — and the obvious
wiring, `context.WithTimeout(ctx, cfg.DB.Pool.ConnectTimeout)`, expires instantly
when the field is unset, because zero is a deadline in the past. On pgx they
write it having been told four times it was unnecessary. One method covers both
transports and imports nothing of pgx:

```go
func (p Pool) Verify(ctx context.Context, ping func(context.Context) error) error
```

`database/sql` callers pass `db.PingContext`, pgx callers pass `pool.Ping`. It
lives on `Pool` because `ConnectTimeout` is the only field it reads, and because
`Config.Verify` beside `Config.Validate` is one letter apart in the eye for two
questions that are not related. It makes **one attempt** with that timeout —
`DefaultVerifyTimeout` when the field is zero, and the constant exists only
because zero is a deadline in the past, not because there is a retry loop — and
it wraps an exported `ErrUnreachable`, so `errors.Is` is what decides crash
versus retry rather than string matching. The doc states the policy: fail fast,
waiting left to the platform's restart backoff, with the three-line retry loop
printed for a consumer who wants the other one. That is [[D-021]]'s line — "the
database is down right now" is not a wrong configuration — and it turns blocker 1
from "decide between a behaviour change and four document corrections" into
"correct four documents and point them at `Verify`".

### H-VVDB-15 — A managed provider hands out one URL, and the pool is still ours
**Who:** anyone on Neon, Supabase, Heroku or RDS-with-a-URL
**Wants:** the provider's string in `dsn:`, the pool sizing still in `pool:`
**Story:** The provider's dashboard gives one connection URL. They paste it into
`dsn:`, keep the `pool:` block they already had — `max_open: 20`,
`connect_timeout: 5s` — and deploy. Later the same config is opened through
`dbpgx` for a tracer.
**Must hold:**
1. A finished string and a pool section coexist: the string is the address, the pool is the sizing, and they do not contradict each other.
2. Everything in the `pool:` block is applied, or what cannot be applied is refused by name.
3. The same YAML means the same thing on both openers.
**Today:** 🟡 partial — (1) holds, and it is what makes (2) and (3) fail quietly
**Evidence:** (1) is deliberate: `fieldsBesideDSN`
(`utils/vvdb/config.go:170-190`) lists the eight fields that contradict a DSN and
does **not** list `Pool`, so `dsn:` beside `pool:` is accepted by design. (2)
does not hold for the fifth pool field, for the reason stated once in
H-VVDB-11(3): `connect_timeout` is the only pool field carried by the string, and
`prepare` returns the supplied string before `seconds(c.Pool.ConnectTimeout)` is
ever reached (`utils/vvdb/dsn.go:179-191` against `:78-80`), so on `database/sql`
a `connect_timeout: 5s` written beside a `dsn:` **vanishes**, unrefused. (3)
follows: `dbpgx` re-applies it onto the parsed config after the fact
(`dbpgx.go:110-112`), so the identical YAML has a five-second dial timeout on pgx
and none on `database/sql`. The four `SetMax…` limits do reach the handle on both.
Nothing catches it: the integration suite drops the pool entirely when a DSN is
supplied (`test/integration/vvdb_test.go:24-26`).
**If not ready:** the consumer appends `?connect_timeout=5` to the provider's URL
by hand, on the engine whose syntax they were trying not to learn, and does not
find out until a dial hangs. Two shapes close it and both are small: append the
timeout to the supplied string when it names none, or add `pool` to
`fieldsBesideDSN`'s refusal for `connect_timeout` only. The first is better —
the other four limits already work this way — and either needs a test in
`test/dsn`, because the string is what changes.

### H-VVDB-16 — Migrations, against the same database from the same file
**Who:** the engineer who owns the schema and runs golang-migrate, goose, atlas or dbmate at boot or in CI
**Wants:** the connection they already described to be usable by the tool that creates the tables
**Story:** The service's schema is versioned. At boot, or in a CI step, the
migration tool is pointed at the same database. On PostgreSQL they paste the
string vvdb builds and it works. On MySQL they paste it and the tool refuses it.
**Must hold:**
1. A document says which of these four forms the module's string is, per engine: a URL `psql` and golang-migrate take; a URL golang-migrate takes only with `mysql://` in front; the bare driver DSN goose wants; and the `-h/-u/-p` arguments the `mysql` client wants instead of any of them.
2. What a migration tool needs beyond a connection — MySQL's `multiStatements`, a migrations schema — is expressible in the same file.
**Today:** ❌ missing — not wrong, unaddressed
**Evidence:** the module never mentions migrations, and the two syntaxes are not
equally portable. `PostgresDSN` builds a URL
(`utils/vvdb/dsn.go:44-81`), which golang-migrate, `psql` and an ops runbook all
take. `mysqlish` builds go-sql-driver's own DSN — `user:pass@tcp(host:3306)/db?…`
(`utils/vvdb/dsn.go:97-155`) — which is not a URL, has no scheme, and is not what
the `mysql` client accepts at all: golang-migrate wants `mysql://` prefixed and
goose wants it bare. `test/dsn/dsn_test.go` proves the string against the two Go
drivers (`:24`, `:70`) and nothing else, so "the connection string every driver
wants" is proven for exactly the two consumers that are not migration tools.
`multiStatements` is reachable through `params`, is documented nowhere
(`grep -rn multiStatements .` is empty), and `params` has no `env` tag
(H-VVDB-02).
**If not ready:** the consumer writes the migration URL a second time, by hand,
which is the duplication this module exists to remove — and the second copy is
the one that drifts when the password rotates. The cheap fix is documentation: a
short section in `docs/modules/en/vvdb.md` and its Russian twin, spelling out the
four forms above and saying that `params: { multiStatements: true }` is the
answer for multi-statement migrations. A `MigrateURL(cfg)` is a second answer and
needs a decision, because a second string builder is a second thing to keep true.

### H-VVDB-17 — The schema the service actually lives in
**Who:** anyone on a shared PostgreSQL cluster where the service does not own `public`, and every schema-per-tenant SaaS
**Wants:** the schema named in the same file as everything else
**Story:** The platform gives the service a role and a schema. Every table is in
`orders`, not `public`, and the `search_path` is meant to arrive on the
connection. The engineer looks for a field, does not find one, and sets it on the
role instead — until a second service uses the same role. The SaaS next door has
the same problem two hundred times, once per tenant.
**Must hold:**
1. The schema is describable in the configuration, on the engine where it is a separate concept.
2. Setting it reaches every connection in the pool, not the first one.
3. Getting it wrong is loud, because the whole library's view of the database depends on it.
**Today:** 🟡 partial — reachable, undocumented, unreachable from the environment, untested
**Evidence:** `Config` has no schema field. The route that exists is
`params: { search_path: orders }`: pgx forwards any query parameter it does not
claim as a startup runtime parameter
(`.../pgx/v5@v5.10.0/pgconn/config.go:399-423` and `:433-438`), and lib/pq does
the same, so it does reach every connection the pool opens. Nothing says so —
`docs/modules/en/vvdb.md:35-56` shows `params` with `application_name` and stops
— and `params` has no `env` tag (`utils/vvdb/config.go:69`), so on the
environment-only deployment of H-VVDB-02 the schema cannot be set at all.
Schema-per-tenant is the more common multi-tenant shape and is the one the
config cannot describe.
(3) no longer depends on catalog visibility for explicitly qualified tables:
`crud/catalog` indexes every non-system schema the role may use by exact
`(schema, table)` and keeps `pg_table_is_visible` only for legacy bare lookup.
The connection's `search_path` still decides every unqualified SQL declaration,
so configuring it remains this module's responsibility. A wrong path is loud
for a qualified declaration and can still select the wrong table for a legacy
bare one ([[D-041]], [[D-080]]).
**If not ready:** the consumer sets `search_path` on the database role and hopes
no other service shares it, or discovers `params` by reading `dsn.go`. A
documented sentence is most of the fix; an `env` tag on `params` is the rest. A
first-class `schema:` field would be better and is a bigger decision, because it
means nothing on MySQL, where the schema is the database, and nothing at all on
SQLite.

### H-VVDB-18 — Shutting down, including the handle that was never opened
**Who:** every service with a `SIGTERM` handler, which is every service in a cluster
**Wants:** `defer close everything` to be correct on the config as written today and on the config with a replica added tomorrow
**Story:** `main` takes the pair back from `OpenReadWrite`, defers a close on
each, and ships. There is no replica in the file yet.
**Must hold:**
1. Closing what the module handed back is safe on every config it accepts, including the one with no replica.
2. Ownership on the failure path is stated: when one of the two fails to open, whoever holds the other knows it.
**Today:** ❌ missing
**Evidence:** no must-hold in the seventeen cases before this one says closing is
*safe*, and the introduction stakes the module on "the handle is theirs. They
open it, they close it". `OpenReadWrite` returns a nil `*sql.DB` when no replica
is declared (`utils/vvdb/open.go:68-71`) and `ConnectReadWrite` a nil
`*pgxpool.Pool` (`dbpgx.go:79-81`). The obvious
`defer primary.Close(); defer replica.Close()` panics on both: `(*sql.DB).Close`
dereferences `db.mu` and `(*pgxpool.Pool).Close` dereferences `p.closeOnce`. The
doc comment on `OpenReadWrite` (`open.go:51-62`) shows the nil check before
`crud.ReadWrite` and shows no close at all, so the one place a consumer copies
from teaches the guard for `Bind` and not for shutdown — where it will be found
in production rather than in a test. (2) is asymmetric and unstated: when the
replica fails to open, `OpenReadWrite` closes the primary for the caller
(`open.go:72-76`) and `ConnectReadWrite` does the same (`dbpgx.go:82-86`); when
it succeeds, both handles are the caller's. Nothing says either.
**If not ready:** the consumer writes `if replica != nil { defer replica.Close() }`,
or finds out on the first pod that shuts down. Two sentences in the doc comment
and the same two lines in the module docs' example close it, and cost nothing. A
nil-skipping `func Close(dbs ...*sql.DB) error` the caller invokes is worth
considering rather than dismissing: it is the same class as the `Pool.Apply` this
file argues for — a helper over handles the application already holds — and
[[D-057]]'s forbid list rules out "a function that returns a `crud.Source`, a
`Repo`, or anything else that removes the caller's line", which a close the
caller writes does not. It collapses the four-line nil dance to one. The reason
to hesitate is smaller than the reason given last round: two ways to close is two
things a reader has to reconcile, not a second lifetime owner.

### H-VVDB-19 — The driver reads its own environment, underneath the file
**Who:** whoever debugs why the same image connects differently on a laptop, in CI and in the cluster
**Wants:** the file to be the description, or to be told plainly that it is not
**Story:** A base image sets `PGAPPNAME`. A CI runner sets `PGSSLMODE=disable` so
its throwaway PostgreSQL works. A developer has `PGSERVICE` and a `~/.pgpass`
from another project. Nobody edits the config, and three environments connect
three different ways.
**Must hold:**
1. Which settings the driver reads from the process environment, underneath whatever string this module builds, is written down where the config is documented.
2. A setting the config states is not silently replaced by one the environment supplies.
3. A password the file states either reaches the server or is refused — it is never replaced by one from a file on disk nobody named.
**Today:** ✅ holds
**Evidence:** typed PostgreSQL is pgx-only and `PostgresDSN` renders every
portable connection fact and relevant empty default into the URI: host, port,
database, user, password, verified `sslmode`, timeouts, passfile, certificate
paths and safe empty runtime settings. pgx therefore cannot fill those from
`PG*` or `~/.pgpass`. An empty TimeZone is not safe — PostgreSQL rejects it — so
ambient `PGTZ` is refused unless a non-empty `Params.timezone` owns the value.
`PGSERVICE` and `PGSSLNEGOTIATION` are refused as second-document or driver-only
grammar. The protocol/authentication environment added in newer pgx releases
is also refused unless the matching `Params` key is explicit;
unconditionally emitting those keys would turn them into invalid PostgreSQL
startup parameters on the older pgx line declared by `dbpgx`. Real parser
tests prove both the portable empty defaults and the explicit newer-key opt-in.
The unit gate then reruns `dbpgx` outside `go.work`, resolves the unreleased root
from this tree, asserts that module selection is exactly pgx `v5.7.6`, and
proves no version-specific key becomes a runtime parameter there. Both
module-language docs enumerate pgx's environment vocabulary and distinguish
these typed guarantees from a complete raw DSN, whose author owns any
intentional ambient behavior.

### H-VVDB-20 — Nobody set `sslmode`, and the auditor asks
**Who:** the engineer answering "prove production connects encrypted", from the config file alone
**Wants:** the file to distinguish "we chose plaintext" from "nobody said"
**Story:** Compliance asks for evidence that the service's database traffic is
encrypted. The engineer opens the config the platform deployed. There is no
`sslmode` line, because the template everybody copied does not have one.
**Must hold:**
1. An absent TLS setting has a stated meaning, per engine, in the same document as the field.
2. A configuration that is silent about TLS is distinguishable from one that chose plaintext.
3. The template a consumer copies is one that answers the question.
**Today:** ✅ holds
**Evidence:** the typed builders own the empty-mode meaning instead of
delegating it to a driver: PostgreSQL writes `sslmode=verify-full`; MySQL and
MariaDB write `tls=true`. Plaintext is the explicit `sslmode: disable` waiver,
including on loopback, and raw `dsn:` remains the whole low-level escape hatch.
`TestTypedServerConnectionsUseVerifiedTLSByDefault` pins both families and the
explicit local waiver; the real pgx parser control proves ambient
`PGSSLMODE=disable` cannot replace the typed default. Both language module docs
state the rule beside the field, and local examples name `disable` rather than
depending on silence. [[D-081]] records the safe default.

### H-VVDB-21 — The application that follows the documentation validates nothing
**Who:** the engineer who wired the config exactly the way the module doc shows
**Wants:** a configuration that cannot mean what it says to stop the process
**Story:** They copy the four-line struct from the module doc — `DB vvdb.Config`
nested in the application's own config — load it with the loader the doc names,
and open the handle. They assume the refusals the package advertises are running.
**Must hold:**
1. On the loading path the documentation shows, a configuration the package would refuse is refused.
2. Whatever has to be written for that to be true is written in the documentation that shows the struct.
**Today:** ❌ missing — nothing validates anything on the documented path
**Evidence:** `vvcfg.Load` type-asserts the `Validator` interface on the
top-level struct it decoded (`utils/vvcfg/vvcfg.go:64-68`). `Config.Validate` is
a method on a *named field*, and a method on a named field is not promoted, so
`any(&App{}).(Validator)` fails unless the author writes
`func (a *App) Validate() error { return a.DB.Validate() }`. No document in this
repository writes it: `docs/modules/en/vvdb.md:61-64` shows the struct with no
method at all, and `docs/modules/en/vvcfg.md:29-34` shows a `Validate` on a
top-level struct that checks its own field and forwards to nothing.
`grep -rn "Validate() error"` outside `vvcfg` and `vvdb` finds only those two
docs. The second half is blocker 3: `Config.Validate` is called by nothing inside
the package either (`utils/vvdb/config.go:162` is its own recursion and the only
call site), so with no forwarder the replica engine cross-check runs nowhere at
all. The sibling sweep owns the loader half —
`docs/ai/usecases/modules/utils/Utils.md`, blockers row 6, "Nested
`Validate()` is never called" — and names the same consequence.
**If not ready:** every consumer writes the forwarding method, having first
worked out that they need one. Two fixes, and they are independent: `Open` and
`DSN` calling `Validate` makes the package's own refusals true whatever the
loader does (and is what the godoc already claims), and a `Validate` on the App
struct in `docs/modules/en/vvdb.md` makes the documented shape correct. The first
is the one that matters, because it is the only one that survives a consumer who
uses a different loader. It is also a behaviour change — configurations that
open today would stop booting — which is free before the tag and expensive after.

### H-VVDB-22 — The pool outlives the server
**Who:** anyone on a managed database, which fails over on the provider's schedule
**Wants:** a ten-minute patch window to cost ten minutes, not an afternoon
**Story:** The provider fails over at two in the morning. Or PgBouncer is
restarted. Or the idle sockets a firewall was holding are dropped. The pods are
up, the database is up, and every query fails for several minutes on connections
that point at something that no longer exists.
**Must hold:**
1. How long a pooled connection may live is describable in the file, and what happens when it is not set is stated.
2. The default is not "forever", or if it is, the document says so where the pool is documented.
3. A consumer can tell from the file what the recovery time after a failover will be.
**Today:** ❌ missing
**Evidence:** `max_lifetime` and `max_idle_time` both exist and both reach the
handle (`utils/vvdb/open.go:89-94`, `dbpgx.go:104-109`). Neither is set unless
the file sets it: the `> 0` guards leave `database/sql`'s default, which is no
limit at all — a connection lives until it errors. All three templates set
`max_open` alone (`README.md:178`, `docs/usage-guides/ent.md:783`,
`docs/usage-guides/gorm.md:720`), so the shape a consumer copies is the one with
no bound. `docs/modules/en/vvdb.md:48-53` lists the five pool keys and says
nothing about what any of them defaults to or what they are for. `grep -rn
"failover\|max_lifetime" docs/modules/en/vvdb.md` finds the key and no sentence.
Nothing in this repository ever exercises a connection outliving its server, and
nothing could without a container that restarts.
**If not ready:** the consumer learns it from an incident, and the incident reads
as a library bug for an afternoon because the database is demonstrably up. The
fix is documentation with a number in it: `max_lifetime` bounds how long a
failover can hurt, an unset one means forever, and thirty minutes is a starting
point. That belongs in the module doc beside the pool block, and one line in all
three templates makes the copied shape the right one.

### H-VVDB-23 — `params` has no owner
**Who:** everyone, eventually: it is where four other cases send them
**Wants:** to know what may go in `params`, per engine, and what it costs
**Story:** They need a CA bundle, or a `search_path`, or `multiStatements` for
migrations, or WAL on SQLite. Each time, the answer is `params`. Then the company
moves the service from PostgreSQL to MySQL, and the file that was supposed to be
the only thing that changed carries a `search_path` the MySQL server rejects at
connect.
**Must hold:**
1. What `params` is — a passthrough into one engine's connection string — is stated, with the per-engine keys that matter named at least once.
2. The cost is stated: a non-empty `params` is the one part of the file that does not survive changing `engine:`.
3. One key carries one value, and a driver that wants a key repeated is named as an exception.
**Today:** ❌ missing — `params` is documented by one example
**Evidence:** `docs/modules/en/vvdb.md:46-47` shows
`params: { application_name: orders }` and that is the whole documentation.
Four cases in this file route their answer through it: the CA bundle
(H-VVDB-06), `search_path` (H-VVDB-17), `multiStatements` (H-VVDB-16) and
SQLite's journal mode and busy timeout (H-VVDB-08). None of the four keys appears
anywhere in the repository: `grep -rn '_pragma\|busy_timeout\|multiStatements' .`
is empty and `sslrootcert` appears only in this file's own text. (2) is a direct
hit on [[UC-021]]'s first guarantee — "moving engines is an edit to the
configuration file, not to the program" — and nothing says the guarantee has an
exception. (3) fails on SQLite, where `Params map[string]string`
(`utils/vvdb/config.go:69`) written through `url.Values.Set` (`dsn.go:166-169`)
cannot express the repeated `_pragma=` the default driver requires; H-VVDB-08 has
the driver evidence. `params` is also the one field with no `env` tag, which is
blocker 20.
**If not ready:** the consumer reads `dsn.go`. A table in both module docs — key,
engine, what it does, where it goes in the string — costs a page and closes
H-VVDB-06(4), H-VVDB-16(2) and H-VVDB-17(1) at the same time, which is why it is
worth more than any single one of them. The engine-portability sentence belongs
in the same table's caption, and it belongs in [[UC-021]] too, because a numbered
guarantee with an unstated exception is worse than one that names it.

### H-VVDB-24 — The consumer on gorm, sqlx, or anything that takes the string
**Who:** the majority of this module's audience, and one of the two usage guides
**Wants:** the first level — a correct connection string — with the pool still theirs
**Story:** They use gorm. gorm takes a string and opens the handle itself. They
copy the guide's `db:` block, which has a `pool:` section, paste the guide's
`main`, and deploy.
**Must hold:**
1. The string level is a first-class level, not the fallback for consumers who could not use `Open`.
2. What that level does *not* do is stated where the level is taught: the `pool:` block is not applied by anything.
3. A consumer at that level can still apply the pool section to the handle their ORM opened.
**Today:** 🟡 partial — (1) holds, (2) and (3) do not
**Evidence:** (1) is the module's design and it is right: `PostgresDSN` and the
other three open nothing (`utils/vvdb/dsn.go:44`, `:89`, `:95`, `:161`), and
`docs/modules/en/vvdb.md:141-152` says an ORM needs no module because "each takes
either a `*sql.DB` or a string, and both already exist".
(2) fails in the guide itself. `docs/usage-guides/gorm.md:712-720` prints a `db:`
block whose last line is `pool: { max_open: 20 }`, and `:729-731` builds the
handle with `vvdb.PostgresDSN` into `gorm.Open` — so nothing ever applies the
pool section, and `max_open: 20` reaches nothing at all. The example does the
same (`_examples/gorm-mysql-gin/main.go:83`). The one pool field that would
survive the trip is `connect_timeout`, because it is in the string
(H-VVDB-11(3)), and the guide's block does not set it. The ORMs section of the
module doc has the same silence: it says both handles exist and does not say
which of the two loses the pool.
(3) has no answer: `apply` is unexported on both transports
(`utils/vvdb/open.go:82`, `dbpgx.go:93`), so a consumer holding the `*sql.DB`
gorm opened has to re-implement four `SetMax…` calls with the zero-means-default
rule remembered rather than inherited.
**If not ready:** the guide teaches a snippet where a block does nothing, which
is the failure this module's opening paragraph is about, in the module's own
documentation. Removing the `pool:` line from the gorm guide is the honest
one-line fix today; exporting `Pool.Apply` is what makes the line meaningful
instead, and this — not the IAM case — is the majority argument for it.

### H-VVDB-25 — The credential arrives as a mounted file
**Who:** any platform with a secrets CSI driver, a Vault Agent sidecar, or a policy against secrets in environment variables
**Wants:** the password read from `/run/secrets/db_password` at boot, everything else from the config
**Story:** Policy says environment variables leak through `/proc` and crash
dumps, and ConfigMaps are readable by too many people, so the platform projects
the password as a file that is rotated in place. The host, database, pool sizes
and TLS mode stay in the config file.
**Must hold:**
1. A password held in a file is describable in the configuration, not assembled in Go.
2. The file being absent or empty is a start-up refusal that names it, like any other missing field.
3. Whether a rotated file is re-read, or the process must restart, is stated.
**Today:** ❌ missing on both transports
**Evidence:** `Config` has `Password string` and nothing else
(`utils/vvdb/config.go:58`); there is no `password_file`, no
`Password func() (string, error)`, and no hook on the `database/sql` side at all
(`Open` takes no options, `utils/vvdb/open.go:20`). `grep -rn password_file .`
is empty. The password can come from YAML — which is the ConfigMap this module's
opening paragraph exists to get it out of — or from `DB_PASSWORD`, which is the
variable the policy forbids.
**If not ready:** the consumer reads the file in `main` and assigns
`cfg.DB.Password = string(b)`, putting the credential back in Go, which is the
regression this module exists to prevent — and doing it *after* `Validate` would
have run, so a missing file is a connection refused by the server rather than a
start-up failure that names it. This wants a decision before the tag, and the
must-holds above are what the decision is graded against: a `password_file:`
field is one more closed field and answers all three; a
`Password func() (string, error)` seam answers (1) and (3) and is not something a
YAML-shaped struct can carry; `BeforeConnect` on pgx answers it for one transport
and leaves the other. A deferred decision with no must-holds is a decision that
does not get taken.

## The DX this should have

### The call site

The whole thing, honestly — because the four lines of Go are not what a
consumer writes.

```yaml
# config.yaml
db:
  engine: postgres
  host: db.internal
  user: app
  name: app
  sslmode: verify-full        # H-VVDB-20: leaving it out is not the same as choosing
  pool: { max_open: 20, max_idle: 20, max_lifetime: 30m }
```

```go
import _ "github.com/jackc/pgx/v5/stdlib"   // the driver is your import, not vvdb's

type App struct {
    Addr string      `yaml:"addr" env:"ADDR"`
    DB   vvdb.Config `yaml:"db"`
}

// Without this, nothing validates anything: the loader asserts the interface on
// the top-level struct, and a method on a named field is not promoted.
func (a *App) Validate() error { return a.DB.Validate() }

func main() {
    cfg := vvcfg.Must(vvcfg.Auto[App](os.Args[1:]))

    db := vvdb.MustOpen(&cfg.DB)
    defer db.Close()

    src   := crudsql.Postgres(db)
    users := Users.Bind(src)
    ...
}
```

About ten lines of Go in two places plus a config block, and six or seven
concepts held at once: where the config file comes from, the blank import that
registers the driver, the closed engine set, the hand-over line [[D-057]] insists
stays visible, the forwarder without which no refusal runs, the fixed `DB_*`
environment names wherever the struct nests (H-VVDB-09), and — the day a replica
appears — that the second handle comes back nil and `Close` panics on it
(H-VVDB-18). Against a hand-written DSN in three `main` functions that is still a
good number. `src` is hoisted rather than inlined because the second resource is
the common case and `crudsql.Postgres(db)` builds a fresh classifier each time
(`crud/adapter/crudsql/crudsql.go:164-166`); both usage guides already hoist it
(`docs/usage-guides/gorm.md:732`, `docs/usage-guides/ent.md:488`).

The forwarder in the middle is the honest part. It is not in
`docs/modules/en/vvdb.md:61-64` and it is not in `docs/modules/en/vvcfg.md:29-34`,
and without it the block above validates nothing at all (H-VVDB-21).

The ownership rule is deliberately narrow: `vvcfg.Load` validates only the
top-level value that implements `vvcfg.Validator` (`utils/vvcfg/vvcfg.go:60-69`);
it must not grow a reflective nested-`Validate` walk. The application writes the
one explicit `App.Validate` forwarder above. `vvdb` owns `Config.Validate` and
must also call it from `DSN`/`Open`/the two read-write openers so its refusals do
not depend on a loader. The **Utils** documentation owns the generic loader and
top-level-validator rule; the **Vvdb** documentation owns the forwarder and the
database rules. No `vvdb.Load`, `ValidateAll`, or second configuration API is
needed.

It is also a shape **nothing in this repository executes.** `grep -rn vvcfg
--include="*.go" .` reaches `utils/vvcfg` and one comment; all three examples
hold a `vvdb.Config` as a Go literal with an `if err != nil { log.Fatal(err) }`
underneath (`_examples/sql-nethttp/main.go:65-78`). The `Bind` line is real
(`:83`); everything above it is compiled by no test, no example and no guide.

### Turning one knob

Each of these is the delta from the block above, not a replacement for it.

Replica presence is a configuration decision made **before** an opener is chosen:
`replica` omitted (`nil`) means no replica and uses the ordinary one-handle path;
`replica: {}` is present-but-empty and must be rejected as an incomplete replica;
a non-empty replica uses the two-handle path. That distinction prevents a Helm
default from silently doubling connections to the primary.

```go
// no `replica:` key: one handle, no nil secondary to close
primary := vvdb.MustOpen(&cfg.DB)
defer primary.Close()

// a non-empty replica: proposed validation has already rejected `replica: {}`.
primary, replica, err := vvdb.OpenReadWrite(&cfg.DB)
if err != nil { log.Fatal(err) }
defer primary.Close()
defer replica.Close()
```

The required implementation is a presence/emptiness check in `Config.Validate`,
called by `OpenReadWrite` and `ConnectReadWrite` before either pool is opened.
The current `ReadReplica` contract distinguishes only nil from non-nil
(`utils/vvdb/config.go:229-233`), so this is a pre-tag behaviour change, not a
claim about current code.

```go
// 1. a replica appears in the YAML.
// 3 statements become 7, and 4 of them are the nil guard.
primary, replica, err := vvdb.OpenReadWrite(&cfg.DB)   // and NOT MustOpen — H-VVDB-05
if err != nil {
    log.Fatal(err)
}
defer primary.Close()
if replica != nil {
    defer replica.Close()            // H-VVDB-18: nil today, and Close panics on nil
}
src := crudsql.Postgres(primary)
if replica != nil {
    src = crud.ReadWrite(src, crudsql.Postgres(replica))
}
```

The shrink is not all this module's to make. `crud.ReadWrite` already returns
`primary` unchanged for a nil replica (`crud/executor.go:83-86`); the branch
survives only because `crudsql.Postgres(nil)` is a non-nil `Source`. A
`crudsql.PostgresOrNil` — or a `Source` that reports itself empty — is what
collapses the last three lines to one, and it belongs to the adapters sweep. What
belongs here is `MustOpenReadWrite` and `dbpgx.MustConnectReadWrite`, so the two
transports keep the same arities, a nil-skipping `vvdb.Close(dbs ...*sql.DB)`
for the middle four lines (H-VVDB-18), and `Open` refusing a config that declares
a replica, so the wrong function is a start-up failure rather than a silent
single handle.

```go
// 2. start-up should be where a dead server is found. +4 lines, both transports.
if err := cfg.DB.Pool.Verify(ctx, db.PingContext); err != nil {   // H-VVDB-14
    if errors.Is(err, vvdb.ErrUnreachable) {
        log.Fatal("database not reachable; the platform will restart us: ", err)
    }
    log.Fatal(err)
}
// on pgx: cfg.DB.Pool.Verify(ctx, pool.Ping)
```

One attempt, `Pool.ConnectTimeout` as its deadline, `DefaultVerifyTimeout` when
that field is zero. A consumer who wants to wait writes the loop; the module does
not, and `ErrUnreachable` is what makes that a choice rather than string
matching.

```go
// 3. something else opened the handle: gorm, an instrumented driver, or a
// connector that mints an IAM token. +2 lines, and the pool section survives.
sqlDB, _ := gormDB.DB()
if err := cfg.DB.Pool.Apply(sqlDB); err != nil {             // H-VVDB-24, H-VVDB-11
    log.Fatal(err)
}
```

```go
// 3b. and the rung that does not exist: the connector, without leaving Open.
db, err := vvdb.OpenDB(cfg.DB, connector)     // database/sql/driver, still stdlib
```

`Apply` returns an error because it is where `max_idle: 50` beside `max_open: 10`
and the negatives finally have somewhere to be refused (H-VVDB-04). It applies
four of `Pool`'s five fields, and its doc has to say so: `connect_timeout`
travels in the connection string, so a connector built by hand never got it.
**The pgx twin returns an error too** — `func Apply(pc *pgxpool.Config, p vvdb.Pool) error`
— because pgx is the transport where the misconfiguration costs more: `MinConns`
50 over `MaxConns` 10 gives a health check that retries a top-up it can never
reach, once a minute, forever.

```go
// 4. the service logs what it started with. 0 extra lines; the type changes.
log.Printf("db: %+v", cfg.DB)      // prints the password today — H-VVDB-10
b, _ := json.Marshal(cfg)          // and so does this, which String cannot fix
log.Print(vvdb.RedactedDSN(cfg.DB))// and this is what support actually asks for
```

```go
// 5. pgx needs what one config cannot describe for four engines. +3 lines.
pool := dbpgx.MustConnect(ctx, cfg.DB, func(pc *pgxpool.Config) {
    pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec   // PgBouncer
    pc.ConnConfig.Tracer = tracer
})                                                     // exists, and is the right shape
```

### Why this shape

The two levels the module already has — a string, or a sized handle — are the
right two, and the ladder is right: a consumer on gorm takes the string, a
consumer on ent takes the handle, and neither pays for the other. What is missing
is the third rung, and it is the one every serious deployment reaches within a
month: **the handle somebody else opened.** The moment a tracer, a connector, an
ORM or a short-lived credential enters, `Open` has nothing to offer and the
caller loses the pool section as well — not because the pool section is coupled
to opening, but because `apply` happens to be lower-case. One rename turns "you
must leave" into "you may reach further".

`OpenDB(c, driver.Connector)` is the other half of that rung, and it is what
stops the `database/sql` side being a cliff rather than a paved one. `Pool.Apply`
serves the handle that already exists; `OpenDB` serves the handle the caller
wants vvdb to size for them. `database/sql/driver` is standard library, so
neither costs a dependency. The alternative — `Open(c, opts ...Option)` with a
`WithConnector` — is the shape the pgx half of this same module already chose,
and it puts zero words in front of the common case, because `Open(cfg)` still
compiles. Either is better than the status quo, which is `vvdb.DSN` plus
`sql.Open` plus a hand-copied `apply`: three statements where the short path has
one, and one of the three copied wrong.

`Verify` lives on `Pool` rather than on `Config` for two reasons. `ConnectTimeout`
is the only field it reads, so the receiver says what it contributes; and
`Config.Verify` sitting beside `Config.Validate` is two methods one letter apart
that answer unrelated questions, which a consumer skimming godoc will get wrong
once and believe forever. It is one method rather than two functions because the
question is one question: today `vvdb.Open` is lazy and `dbpgx.Connect` is
documented as eager, so the same YAML fails at a different moment depending on
which opener the consumer picked — and the eager half is not true, which is
blocker 1.

**The three pool fixes are one function in one order, and stating the order is
the point.** `Open` and `DSN` call `Validate` (blocker 3) — that is the change
that makes the godoc true and the replica engine check reachable. `Validate`
calls `Pool.validate()` (blocker 13). `Pool.Apply` calls the same
`Pool.validate()` for the handle the caller opened (blocker 14). Landed in any
other order, the validation ships inert: `Config.Validate` is on nobody's path
today, so a `Pool.validate()` hung off it would refuse nothing for the consumer
who calls `Open`.

### What it must not break

- **[[D-057]]** — nothing above returns a `crud.Source`, a `Repo` or anything
  that removes the caller's hand-over line. `Pool.Apply` and `OpenDB` move in the
  opposite direction: they take, or hand back, a handle the application owns and
  closes. `Verify` takes a ping function and pings it. The proposed
  `vvdb.Close(dbs ...*sql.DB)` is the one worth arguing about, and D-057's forbid
  list is what it is measured against: it removes no line the caller writes, it
  owns no lifetime, and the caller invokes it. Weighed and proposed, not assumed.
- **[[D-046]]** — vvdb owns the closed `Engine` declaration and builds the
  engine-specific DSN; the four named constructors remain separate precisely so
  an engine difference has a home. It must not infer an adapter/classifier or
  collapse PostgreSQL and MySQL behaviour merely because both arrive as
  `*sql.DB`; that mapping remains the adapters' ownership.
- **[[D-036]] / [[D-033]] / [[D-016]]** — `Verify`, `Apply`, `OpenDB` and the
  redaction are `database/sql`, `database/sql/driver`, `context`,
  `encoding/json` and `log/slog`. `vvdb` stays dependency-free and `dbpgx` stays
  one decision ([[D-051]]).
- **[[D-013]]** — the closed sets stay closed. Nothing above relaxes an engine
  name, a driver name or an `sslmode`. H-VVDB-06's `params` refusal narrows one:
  a `params` key that names TLS beside a stated `sslmode` becomes a conflict, in
  the same spirit as `fieldsBesideDSN`.
- **[[D-021]]** — refusals stay at start-up, and `Verify` is opt-in precisely
  because "the database is down right now" is not a wrong configuration. That
  settles the release-day question H-VVDB-14 raises: the fix for blocker 1 is to
  correct four documents and add `Verify`, **not** to put a `Ping` inside
  `Connect`. A library that decided otherwise would take a deployment policy away
  from the application.
  `replica: {}` and DSN/named-field disagreement are vvdb configuration refusals;
  an unavailable server remains an application-owned `Verify` policy.
- **[[D-032]]** — the replica pair is handed over as a pair; who routes what
  stays `crud.ReadWrite`'s. `MustOpenReadWrite` changes the arity of an error,
  not the ownership of a decision.
- **[[D-041]]** — H-VVDB-17's schema field, if it is ever added, is a
  connection-level setting only. The catalog resolves per handle and caches; a
  schema that could change after the handle exists would break that, and nothing
  above proposes one.
- **What these break on purpose, and only before the tag:** `Open` calling
  `Validate` stops a configuration that boots today (a replica of another engine,
  a nested `Validate` nobody forwarded). `Pool.Apply` refusing an idle floor
  above the open ceiling stops another (`database/sql` clamps that pair silently
  and always has), and the same rule fires inside `Open` for callers who never
  touch `Apply` — so `Open` must surface `Apply`'s error and close the handle it
  opened a line earlier, or leak it. `type Secret string` changes an exported
  field's type, so every `cfg.DB.Password = os.Getenv("PW")` in a consumer becomes
  `vvdb.Secret(...)`, and it adds a second named string type to a decode path
  nothing has ever executed (`Engine` is the first, `utils/vvdb/config.go:47`) —
  which is one more reason the temp-YAML test H-VVDB-01 asks for should cover it.
  All three are free now and expensive after the first tag.
- **Two deliberate challenges, neither implemented:**
  1. H-VVDB-02 wants an environment-supplied `DB_DSN` to outrank fields a file
     already set. [[D-057]] says the escape hatch is "whole or absent" and that
     accepting both is the failure shape this library refuses everywhere.
     Precedence by layer is not the same thing as two sources of truth, but it is
     close enough that it belongs to the owner.
  2. H-VVDB-13 wants `crudsql.For(engine string, db *sql.DB)` so the engine is
     named once. The reasoning beside the four constructors cites [[D-046]] to say
     that string "is a declaration and not something to be derived". D-046's
     invariant — the classifier key — is untouched; the sentence next to the
     constructors is not. Stated as a challenge, for the adapters sweep and the
     owner.

**The replica's environment names are not one struct tag, and saying so matters.**
Making `Replica` a value struct so cleanenv walks it, with a presence flag or a
loader that allocates, is only half. cleanenv pushes a nested struct with
`sPrefix + prefix` and defaults `prefix` to empty
(`.../cleanenv@v1.5.0/cleanenv.go:337-346`), and the nested `env` names are fixed
strings, so a bare value-typed `Replica` reads the *primary's* `DB_HOST`,
`DB_USER` and `DB_PASSWORD` and points both handles at one server — H-VVDB-09's
bug inside a single config. The whole fix is
``Replica Config `yaml:"replica" env-prefix:"REPLICA_"` `` plus the presence
flag, and the variable it produces is `REPLICA_DB_HOST`, not `DB_REPLICA_HOST`.
The test that pins it sets `REPLICA_DB_HOST` and asserts the primary's host is
untouched.

## DX verdict

Distance is **code distance**, and the second half of each cell says whose desk
it lands on: doc-only, code in this module, a config-format change the owner
owns, or another module.

| What the ideal asks for | Today | Distance |
|---|---|---|
| One YAML block, one line for the handle | Exactly that as an API — and nothing in this repository loads a `db:` block, so the decode is unproven | small · this module (a test) |
| The documented wiring actually validates | It does not. `vvcfg` asserts the interface on the top-level struct, no doc writes the forwarder, and `Open` never calls `Validate` either | small · this module + doc-only, behaviour change |
| A wrong field stops the process and is named | Exactly that on the primary. Inside a replica, only if the application calls `Validate` itself, which nothing shows it how to do | small · this module, behaviour change |
| A declared `replica:` produces a second handle | `MustOpen` ignores it in silence — and `README.md:179` prints that YAML two lines above `MustOpen` | small · this module + doc-only |
| A password with punctuation survives | Exactly that on PostgreSQL and MySQL, proven by the real parsers. Nothing proves SQLite, and a password with no `user` is dropped in silence — and `.pgpass` may then supply a different one | small · this module |
| The file is the description | It is not. Twenty-four `PG*` variables and `~/.pgpass` sit underneath every string, and no document mentions any of them | small · doc-only |
| A stated TLS mode cannot be weakened elsewhere in the file | Closed: TLS keys in `Params` conflict with the typed field/default on both syntaxes | none |
| Silence about TLS has a stated meaning | Closed: typed server declarations choose verified TLS; plaintext is an explicit `disable`, raw DSN is the escape hatch | none |
| The pool section reaches the handle | It does — and `max_open` alone still leaves two idle connections, `max_idle: -1` becomes 2 (or pgx's 0), and nothing refuses an idle floor above the open ceiling | small · this module + doc-only |
| The same `pool:` block means the same thing on both openers | It does not: `max_idle` is a ceiling on `database/sql` and a floor pgx dials up to at boot. Tested and deliberate; unsaid where a consumer edits YAML | small · doc-only, or a rename the owner owns |
| A connection does not outlive its server | `max_lifetime` unset means forever, and all three templates leave it unset | small · doc-only |
| `dsn:` from a provider, `pool:` still ours | Works, except `connect_timeout` — silently dropped on `database/sql`, honoured on pgx | small · this module |
| A replica is one line of YAML | It is — until it states a pool field, and then the primary's whole pool is dropped; and stating none doubles what the pod costs the server, unremarked | small · this module + doc-only |
| Start-up finds a dead server | Four hand-written lines on `database/sql`; on pgx four documents say it is already done and it never has been | large · this module + doc-only |
| Shutting down the pair | `defer replica.Close()` panics on the no-replica config, and the doc comment shows the nil guard only before `Bind` | small · doc-only, or a small helper |
| Size a handle I opened myself | The seam exists and is lower-case on both transports (`apply`, `dbpgx.apply`): one rename each, plus the error the refusals need | small · this module, behaviour change |
| Reach a connector or an instrumented driver | No seam at all. `vvdb.DSN` + `sql.Open` + a hand-copied `apply` — three statements where the short path has one | large · this module |
| Mint a credential per connection | Works on pgx through `Option`; nothing on `database/sql`; and on pgx the replica gets the primary's hook | large · this module (a signature the owner picks) |
| A credential from a mounted file | Nothing. `password_file` does not exist and neither does a `Password` seam | large · config format, owner |
| Logging the config safely | Closed: `Secret`, redacted `Params` and `RedactedDSN` cover ordinary formatters and support output | none |
| Naming the engine once | Named twice: the file and `crudsql.<Engine>(db)`. MySQL against MariaDB runs perfectly and misclassifies two faults | large · `crud/adapter/crudsql`, and a challenge |
| The schema the service lives in | An undocumented `params` key with no `env` name, which decides what the whole catalog can see | small · doc-only + one tag; a `schema:` field is the owner's |
| `params` documented as a thing rather than an example | One example key in one line of one doc, while four cases route their answer through it | small · doc-only |
| The migration tool takes the same string | On PostgreSQL yes; on MySQL it is the driver's DSN, not a URL, and nothing says so | small · doc-only |
| A second database in the same process | Works, and silently shares `DB_*` unless the consumer finds `env-prefix` in another project's README | small · doc-only, shared with `utils/vvcfg` |
| The ORM consumer's `pool:` block does something | It does nothing, and the gorm guide prints it anyway | small · doc-only until `Pool.Apply` exists |
| SQLite as a first-class fourth engine | String comparison only; nothing has ever opened it; the path is the one string here with no escaping; and two pragmas cannot be written at once | small · this module + doc-only |

**Overall:** the half of this module that builds a string is finished for the two
engines that carry the traffic, and it is finished at a level most libraries never
reach — the escaping is proven by the parsers that decide, with a control that
fails when the proof would be vacuous. The half that comes after the handle exists
is thinner than the first half implies, and the half *underneath* the string —
what the driver reads from the environment, what silence about TLS means — is not
addressed at all. Almost every gap sits on the same seam: the module hands back a
handle and stops, which is right, but it stops slightly too early. The pool
knowledge, the reachability check, the redaction and the engine's own name all
belong to the configuration and are all stranded on the wrong side of the return
statement. Customising splits by opener rather than averaging: on pgx one
`Option` covers the tracer, the exec mode and the IAM hook, and that rung extends
cleanly until a replica appears and takes the primary's hook with it; on
`database/sql` an instrumented driver, a connector or an ORM means leaving `Open`
entirely, which is the only real cliff in the module and the one two proposals
above close rather than pave.

## Release blockers found here

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | `dbpgx.Connect` does not dial — `pgxpool.NewWithConfig` dials in a goroutine in both v5.7.6 and v5.10.0 — while `dbpgx.go:31`, `docs/modules/en/dbpgx.md:26` and `:30`, `docs/modules/ru/dbpgx.md:30-31` and [[FL-021]] all promise an absent server fails at start-up | blocker | A stated start-up guarantee that has never been true; a deployment designed around it reports ready against a dead database. Four documents, not three — the Russian mirror is parallel by design |
| 2 | `Open` and `MustOpen` silently ignore a declared `replica:`; `README.md:179` prints that exact YAML and `:183-184` calls `MustOpen`/`MustConnect` on it | blocker | No second handle, no error, no log line: the reads the consumer moved keep hitting the primary. The largest happy-path hole here, and this repository's front page teaches it |
| 3 | `Config.Validate` is called by **nothing** in the package, while its own godoc (`config.go:138-141`) says "it is called by [DSN] and by [Open], so a caller who forgets it still cannot get a wrong connection" | blocker | The same defect class as row 1, shipped in the library's godoc. What the consumer's path genuinely skips is the replica engine cross-check (`config.go:153-158`) — `known(engine)` and `validateFields` are reached through `DSN` |
| 4 | On the loading path both module docs show, nothing validates anything: `vvcfg` asserts `Validator` on the top-level struct, a method on a named field is not promoted, and no document writes the forwarder | blocker | Every refusal this module advertises is off on the documented wiring. Shared with the `utils/vvcfg` sweep (`Utils.md`, row 6) — one fix, two tables |
| 5 | **Closed:** `Password` and raw `DSN` are `Secret`, all displayed `Params` values redact, and `RedactedDSN` preserves a support-useful target | resolved | Sentinel tests cover `fmt`, JSON, YAML, TOML, `slog`, copied secret values and both server DSN shapes |
| 6 | **Closed:** TLS keys and MySQL plaintext fallback in `Params` are reserved and cannot override either a stated mode or the verified empty-mode default | resolved | Conflict tests plus the real MySQL parser prove no `tls` override or plaintext retry reaches the driver |
| 7 | Because of row 3, a replica declaring another engine opens and is then addressed in the primary's dialect; the check and its test exist and the consumer's path does not run them | serious | Breaks UC-021.7 ("every refusal happens at start-up"). `config_test.go:89` passes; `OpenReadWrite` never reaches the code it covers |
| 8 | A replica that states any pool field loses the primary's whole pool section (`config.go:273-275`) | serious | Breaks UC-021.9 ("inherits everything it does not restate"); silently drops `connect_timeout` and `max_lifetime` on the replica, and no test covers it |
| 9 | `host` is optional for a server engine and becomes `localhost` (`dsn.go:70-72`, `:121-123`); only `name` is required | serious | With the sibling sweep's row 8 (`DB_HOST=` blanks the file), a Helm template that renders empty produces a pod that boots and connects to itself. The "connection that succeeds and is wrong" this module opens on |
| 10 | **Closed:** typed pgx URIs render portable owned/empty defaults, fail loud on undeclared version-specific ambient policy, and document the raw-DSN boundary | resolved | Real pgx controls prove ambient values cannot fill typed facts; an isolated `GOWORK=off` unit gate asserts pgx `v5.7.6` is selected and receives no version-specific server runtime parameter |
| 11 | The engine is named twice — in the file and in `crudsql.<Engine>(db)` — and nothing cross-checks them; `crudsql.MySQL` against MariaDB connects, runs, and misclassifies a failed CHECK and a bad column value | serious | Breaks UC-021.1 one line past this module's boundary. **Fix site: `crud/adapter/crudsql`** — this row cannot be closed from `utils/vvdb`, and it challenges the reasoning beside its four constructors |
| 12 | `docs/ai/usecases/Index.md:92` marks UC-021 "covered" and lists no vvdb gap, while rows 1–11 above each contradict a numbered "What must hold" | serious | An index that does not name a gap is trusted and stops the next agent looking. **Shared with the `utils/vvcfg` sweep** (`Utils.md`, row 6), so it is one Index row and should be edited once |
| 13 | Nothing validates the `pool:` section: `max_idle` above `max_open` is accepted (clamped on `database/sql`; on pgx a health check retries a top-up it can never reach, once a minute, forever), and `max_idle: -1` — a real setting — becomes 2 on one transport and pgx's 0 on the other | sharp edge | Every other field is refused by name at start-up; this one silently does the opposite of what the operator wrote. Must land *after* row 3, or it ships inert |
| 14 | `Pool.apply` is unexported on both transports, so a handle opened by gorm, an instrumented driver or an IAM connector cannot be sized from the config — and there is no connector rung at all | sharp edge | The ORM consumer is the majority path and loses the whole `pool:` block; reaching one step further means `DSN` + `sql.Open` + a hand-copied `apply` |
| 15 | `connect_timeout` beside a supplied `dsn:` is silently dropped on `database/sql` and honoured on pgx | sharp edge | It is the one pool field carried by the string, so it is lost wherever the string is not the one vvdb built — the provider-URL shape and the hand-built connector, both |
| 16 | `pool: { max_open: 20 }` leaves `database/sql` at two idle connections — and the gorm guide prints that YAML and then builds the handle from a string, so the block is inert | sharp edge | Eighteen of twenty connections re-dial per burst, each a new backend process on PostgreSQL; three documents print the snippet (`README.md:178`, `ent.md:783`, `gorm.md:720`) and one of them applies none of it |
| 17 | `max_lifetime` unset means a connection lives forever, and all three templates leave it unset | sharp edge | A managed failover or a PgBouncer restart costs several minutes of failed queries against a database that is up, and reads as a library bug for an afternoon |
| 18 | `defer replica.Close()` panics on the no-replica config, on both transports, and the doc comment shows the nil guard only before `Bind` | sharp edge | Found on the first pod that shuts down rather than in a test, in the module whose premise is that the lifetime is the caller's |
| 19 | A `password` set with no `user` is silently dropped on both syntaxes | sharp edge | libpq falls back to the OS user, `.pgpass` may supply a password, and the process connects as somebody else; nothing refuses the contradiction |
| 20 | `Params` has no `env` tag, and `DB_CONNECT_TIMEOUT` breaks the `DB_POOL_*` prefix its four siblings share | sharp edge | On an environment-only deployment no CA bundle, `search_path`, `application_name` or statement timeout can be set at all; and a manifest that guesses `DB_POOL_CONNECT_TIMEOUT` is ignored in silence |
| 21 | cleanenv does not walk pointer fields, so `Replica` has no environment names — not even for a replica the YAML declares | sharp edge | Rotating a replica hostname or its read-only credential through a secret store has no route; and the obvious fix reproduces row 22 inside one config unless `env-prefix` comes with it |
| 22 | A second `vvdb.Config` in one application silently shares `DB_HOST`, `DB_USER`, `DB_PASSWORD` with the first | sharp edge | Two handles connected successfully to the wrong server, saying nothing; the fix is `env-prefix`, documented nowhere. **Shared with the `utils/vvcfg` sweep** (`Utils.md`, row 24) |
| 23 | SQLite is never opened through `vvdb` by any test or example; `SQLiteDSN` writes the path with no escaping; `port` and `connect_timeout` are dropped rather than refused; and one `params` map cannot carry the two pragmas the default driver needs | sharp edge | A quarter of "four engines" rests on a string comparison against a rule this repository invented, and the story the case is built on — a temp file with WAL and a busy timeout — cannot be written |
| 24 | Nothing in this repository loads a `db:` block into a `vvdb.Config`: no YAML anywhere in `_examples`, no test in `vvdb`, `vvcfg` or `test/` | sharp edge | The module's headline is *one configuration file becomes the handle*, and the loading half of that sentence has never been executed |
| 25 | `params` is documented by one example key, while it is the answer to a CA bundle, a `search_path`, `multiStatements` and SQLite's pragmas — and a non-empty `params` is the one part of the file that does not survive changing `engine:` | sharp edge | UC-021.1's guarantee has an unstated exception, and four separate gaps share one undocumented mechanism |
| 26 | No `schema:` field; the `params: { search_path: … }` route is undocumented | sharp edge | On a shared cluster the connection this module opens decides what `crud/catalog` can resolve ([[D-041]]); getting it wrong reads as a migration problem for a day |
| 27 | **Closed:** absent `sslmode` on typed server declarations means verified TLS; plaintext is explicit `disable` | resolved | PostgreSQL writes `verify-full`, MySQL/MariaDB write `tls=true`; docs and parser-backed tests pin the rule |
| 28 | Migrations are unaddressed: the MySQL and MariaDB string is the driver's DSN, not a URL, and no document says which tools take it or how | sharp edge | The first thing a consumer does with the handle in week one, and the workaround is a second copy of the credentials that drifts |
| 29 | A `dsn:` primary with a field-described `replica:` cannot work — the merged replica loses the name inside the URL — while `docs/modules/en/vvdb.md:128` says it inherits everything it does not restate | sharp edge | Neon and RDS hand out a URL per endpoint, so the combination is ordinary; the failure is loud but the document is wrong |
| 30 | **Closed:** `dbpgx.ConnectReadWrite` accepts scoped `ReadWriteOption`s; common hooks are copied and primary/replica hooks are isolated, ordered and snapshotted | resolved | Separate credentials are expressed as `Primary(...)` and `Replica(...)`; focused tests pin side isolation, common-first precedence and caller-slice ownership |
| 31 | No `password_file:` and no credential seam on `database/sql` | sharp edge | The platform that forbids secrets in environment variables has to put the password back in Go, which is the regression this module exists to prevent |

## Contested

- **Reviewer: "Merge H-VVDB-12 — the schema half into H-VVDB-17 and the budget half into H-VVDB-04."** Half taken. The rated must-hold about schema is gone and is now one sentence of Story pointing at H-VVDB-17, which was the dilution. The connection budget stays a must-hold here *and* appears as H-VVDB-04(5) and H-VVDB-05(7), because the three multiplications are different one-line changes — a tenant, a replica, a pod — and an operator who is told about one still gets surprised by the others. Kept as three sightings of one number rather than one.
- **Reviewer: "Blocker 15 (a second `vvdb.Config` shares `DB_*`) is already owned by the vvcfg sweep — two tables listing one fix means it gets closed once and stays open in both."** Kept the row, marked it shared and named the sibling's line, and did the same for the Index row and the nested-`Validate` row. A vvdb consumer meets all three as a vvdb failure, and a blockers table for this module that omits them hands the owner a false all-clear. Marking the shared fix site is the part that stops the double-close.
- **Reviewer: "H-VVDB-18's refusal of a close helper reads as reflex."** Agreed and reversed: [[D-057]]'s forbid list is about a function that removes the caller's line, which a `Close` the caller invokes does not. It is now proposed rather than dismissed, with the real objection stated — two ways to close is two things to reconcile.
- **Reviewer: "`vvdb.Verify(ctx, db, cfg)` reuses `pool.connect_timeout` for the whole boot check, which is a per-dial timeout."** Kept the idea, and the shape has moved twice: it is now `func (p Pool) Verify(ctx, ping func(context.Context) error) error`, one attempt, wrapping `ErrUnreachable`. Reusing the config's own number is still right: a consumer who tuned a dial timeout has stated the only latency number this module knows, and inventing a second one is a number nobody wrote.
- **Reviewer: "The `database/sql` half of the start-up guarantee has no blocker row despite being rated large."** Still kept out of the blockers table. `Open` being lazy is documented accurately (`open.go:17-19`) and matches `sql.Open`; a missing convenience is a DX gap, not a shipped falsehood. The pgx half is a blocker because four documents say the opposite of what the code does. The distance rating and the severity answer different questions, and the Distance column now says which.

## Edge cases

### E-VVDB-01 — A port outside TCP's range reaches the driver
**Shape:** boundary
**Setup:** An environment template renders `DB_PORT=-1` or `65536` for a PostgreSQL or MySQL service.
**What the consumer does:** They call `Open` (or hand the same config to `dbpgx.Connect`) expecting the configuration boundary to name the bad port before a handle is returned.
**What must happen:** A port that cannot identify a TCP endpoint is refused at start-up with the `port` field named; it must not become a lazy driver failure or a retry against an impossible address.
**Today:** ❌ wrong or unhandled
**Evidence:** `validateFields` checks only engine-specific names, path, TLS and the MySQL user colon (`utils/vvdb/config.go:192-220`); it never bounds `Port`. Both builders pass any integer through `strconv.Itoa` and `net.JoinHostPort` (`utils/vvdb/dsn.go:59-74`, `:114-125`), and `Open` hands the resulting string to lazy `sql.Open` (`utils/vvdb/open.go:20-35`). `TestAPortLeftUnsetIsTheEnginesOwn` exercises only zero/defaulting (`utils/vvdb/dsn_test.go:55-74`); no out-of-range-port test exists.
**Blast radius:** confusing error

### E-VVDB-02 — An already-bracketed IPv6 literal is not a valid host field
**Shape:** boundary
**Setup:** An operator pastes `[2001:db8::1]` into `host`, as they would into a URL authority, rather than the bare literal `2001:db8::1`.
**What the consumer does:** They generate a PostgreSQL or MySQL DSN and start the service.
**What must happen:** The configuration either accepts both conventional spellings or refuses the bracketed one with an actionable `host` error before it reaches a driver.
**Today:** ❌ wrong or unhandled
**Evidence:** Both server builders pass `Host` directly to `net.JoinHostPort` without normalising or rejecting brackets (`utils/vvdb/dsn.go:59-74`, `:114-125`); `validateFields` has no host syntax check (`utils/vvdb/config.go:192-220`). The sole IPv6 test covers the bare spelling only (`utils/vvdb/dsn_test.go:169-179`), so it cannot pin the pasted-URL boundary.
**Blast radius:** confusing error

### E-VVDB-03 — A passthrough parameter replaces a PostgreSQL Unix socket
**Shape:** adversarial input
**Setup:** The primary config says `host: /var/run/postgresql`; a shared or generated `params` map also contains `host: other.internal`.
**What the consumer does:** They expect the explicit `host` field to select the local socket and `params` to carry only driver extras.
**What must happen:** A parameter that duplicates a connection-address field is refused as two sources of truth; it must never silently reroute the connection.
**Today:** ❌ wrong or unhandled
**Evidence:** The PostgreSQL builder first writes a socket directory as query `host` (`utils/vvdb/dsn.go:63-68`) and then lets every `Params` entry overwrite it via `q.Set` (`:81-83`). pgx parses the URI authority first and then overwrites settings from query parameters (`.../pgx/v5@v5.10.0/pgconn/config.go:619-677`), finally taking the endpoint from `settings["host"]` (`:442-481`). `fieldsBesideDSN` protects a whole DSN but does not inspect `Params` keys (`utils/vvdb/config.go:169-190`), and no local test combines a socket with `params.host`.
**Blast radius:** silent wrong answer

### E-VVDB-04 — A chart's empty `replica: {}` opens the primary twice
**Shape:** misuse
**Setup:** A Helm default emits `replica: {}` although the deployment has no read server yet.
**What the consumer does:** They call `OpenReadWrite` and wire its second result into `crud.ReadWrite` only when non-nil.
**What must happen:** An empty replica declaration is either treated as absent or refused as incomplete; it must not quietly create an independent second pool against the primary.
**Today:** ❌ wrong or unhandled
**Evidence:** Any non-nil pointer is a replica (`utils/vvdb/config.go:229-233`). With no fields to overlay, `ReadReplica` returns the copied primary unchanged (`:243-286`), and `OpenReadWrite` calls `Open` a second time whenever that result is present (`utils/vvdb/open.go:63-77`). `TestNoReplicaIsNotAnEmptyReplica` tests only a nil pointer (`utils/vvdb/config_test.go:83-87`), not `&vvdb.Config{}`.
**Blast radius:** silent wrong answer

### E-VVDB-05 — A replica of a replica is silently discarded
**Shape:** degenerate declaration
**Setup:** A generated configuration accidentally nests `replica:` under an already-declared replica.
**What the consumer does:** They start from the one configuration file and expect unsupported topology to stop start-up rather than be partially applied.
**What must happen:** The module must refuse a nested replica and name it, because its API describes one primary and one stale-read server.
**Today:** ❌ wrong or unhandled
**Evidence:** `ReadReplica` copies the first fragment and then clears `r.Replica` in both the DSN and field-merge paths (`utils/vvdb/config.go:233-241`, `:243-286`); `Validate` validates that already-flattened value (`:153-164`). The existing test explicitly says "a replica of a replica is not a thing this describes" but only asserts the result is nil (`utils/vvdb/config_test.go:20-41`), proving discard rather than refusal. No nested-declaration rejection test exists.
**Blast radius:** silent wrong answer

### E-VVDB-06 — A derived replica aliases the primary's parameters
**Shape:** aliasing
**Setup:** A caller obtains a field-described replica with no replica `params`, then adds a replica-only `application_name` or `search_path` to the returned config before opening it.
**What the consumer does:** They reasonably treat `ReadReplica` as a derived configuration, independent of the primary it came from.
**What must happen:** Mutating the returned configuration must not mutate the primary; configuration derivation must copy the map regardless of whether the overlay has parameters.
**Today:** ❌ wrong or unhandled
**Evidence:** `base := c` copies the map header, not its backing map (`utils/vvdb/config.go:243-245`). A new map is allocated only when `len(r.Params) > 0` (`:276-285`), so the normal host-only replica returns the primary's `Params` map unchanged. `TestAReplicaOverridesRatherThanMerges` covers only the allocating branch (`utils/vvdb/config_test.go:44-60`); no test mutates `Params` on a host-only derived replica.
**Blast radius:** silent wrong answer

### E-VVDB-07 — An in-memory SQLite test obtains two databases from one handle
**Shape:** scale
**Setup:** A test config uses `engine: sqlite`, `path: ":memory:"` and permits more than one open connection.
**What the consumer does:** It creates schema and data through one request, then a concurrent request acquires another connection and expects the same test database.
**What must happen:** The module must either make this shape share the store, restrict it to one connection, or document and refuse the unsafe combination; a test database must not fragment when it becomes concurrent.
**Today:** ❓ unverified
**Evidence:** `SQLiteDSN` produces `file::memory:` verbatim (`utils/vvdb/dsn.go:161-173`), while `Open` permits any positive `MaxOpen` (`utils/vvdb/open.go:20-35`, `:82-95`). The SQLite package has examples/tests using a shared-cache spelling and restricted connections, but this repository has no `vvdb` SQLite opener test (`utils/vvdb/open_test.go:32-142`). That external behaviour is a risk hypothesis, not a release claim until a local two-connection control measures it.
**Blast radius:** confusing error

### E-VVDB-08 — A SQLite read replica cannot read the primary's in-memory store
**Shape:** seam
**Setup:** A local test uses the same `path: ":memory:"` for SQLite and adds `replica: {}` to exercise read/write routing.
**What the consumer does:** They pass the two handles to `crud.ReadWrite` and expect reads to see a table a write just created.
**What must happen:** SQLite must reject replica topology, or the module must explicitly construct a safely shared in-memory URI; two independently opened in-memory handles cannot impersonate primary and replica.
**Today:** ❓ unverified
**Evidence:** A non-nil empty replica merges back to the primary configuration (`utils/vvdb/config.go:229-286`), and `OpenReadWrite` separately calls `Open` for primary and replica (`utils/vvdb/open.go:63-77`). Both receive the same `SQLiteDSN` result (`utils/vvdb/dsn.go:161-173`), but there is no SQLite `OpenReadWrite` control in this repository. Whether those two external-driver handles share the in-memory store is therefore unverified here.
**Blast radius:** silent wrong answer

### E-VVDB-09 — A cancelled pgx start-up context still yields a pool
**Shape:** partial failure
**Setup:** The application shuts down while a goroutine is still constructing its pgx pool and calls `Connect` with an already-cancelled context.
**What the consumer does:** It expects cancellation to return `context.Canceled` and no handle whose background work it must now remember to close.
**What must happen:** `Connect` must honour an already-cancelled context before transferring ownership of a pool, or document the deliberately different contract and make the caller check it.
**Today:** ❌ wrong or unhandled
**Evidence:** `Connect` forwards `ctx` directly to `pgxpool.NewWithConfig` and returns any pool it receives (`utils/vvdb/dbpgx/dbpgx.go:33-56`). `NewWithConfig` constructs the pool then starts initial resource creation in a goroutine before returning it (`.../pgx/v5@v5.10.0/pgxpool/pool.go:220-339`); its later health checks use `context.Background()` to create replacement connections (`:554-595`). The local pgx tests use only `context.Background()` (`utils/vvdb/dbpgx/dbpgx_test.go:24-83`), so cancellation ownership is untested.
**Blast radius:** confusing error

### E-VVDB-10 — A 64-bit pool limit overflows pgx's 32-bit configuration
**Shape:** scale
**Setup:** A generated deployment sets `pool.max_open: 2147483648` on a 64-bit host, thinking it has stated a very large but valid Go `int`.
**What the consumer does:** It chooses `dbpgx.Connect` for a PostgreSQL service and expects invalid capacity to be refused as a named config error.
**What must happen:** Limits outside pgx's `int32` range must be rejected before pool construction, with the offending field named.
**Today:** ❓ unverified
**Evidence:** `Pool.MaxOpen` is a machine-sized `int` (`utils/vvdb/config.go:88-94`) but `dbpgx.apply` narrows it without bounds checking (`utils/vvdb/dbpgx/dbpgx.go:93-102`). The downstream pgx/puddle source suggests an overflow can become a generic pool error, but `TestTheConfigReachesPgx` covers only `MaxOpen: 7` (`utils/vvdb/dbpgx/dbpgx_test.go:16-48`). A local overflow control is needed before asserting the exact failure or ownership outcome.
**Blast radius:** confusing error

### E-VVDB-11 — A bad second pgx configuration must not leak the first pool
**Shape:** partial failure
**Setup:** The primary PostgreSQL config parses, but a replica DSN is malformed so its pgx configuration fails after the first pool has been created.
**What the consumer does:** It retries `ConnectReadWrite` during startup and expects either both pools or no pool to remain owned by the failed attempt.
**What must happen:** The first pool must be closed, the returned pair must be nil, and the error must identify the replica; this needs a non-vacuous test that drives the second `Connect` failure.
**Today:** ❓ unverified
**Evidence:** The intended cleanup exists: `ConnectReadWrite` closes `primary` and returns `nil, nil` after its second `Connect` fails (`utils/vvdb/dbpgx/dbpgx.go:73-87`). There is no `ConnectReadWrite` test at all (`utils/vvdb/dbpgx/dbpgx_test.go:1-84`), and `TestConnectRefusesBeforeItDials` covers only a single malformed primary config (`:78-83`), so it would not fail if the pair cleanup disappeared.
**Blast radius:** crash

### E-VVDB-12 — A complete DSN disagrees with named credentials, TLS, or params
**Shape:** conflicting declaration
**Setup:** Operations supplies a complete `dsn:` while a chart or environment
also supplies one or more of `user`, `password`, `sslmode`, or `params`.
**What the consumer does:** They need one deterministic answer before opening:
either the finished DSN wins and every named connection field is refused, or a
documented precedence rule selects the named fields. A mix cannot be silently
accepted.
**What must happen:** The complete DSN is the sole connection declaration.
`DSN`, `Open`, and direct `Config.Validate` must refuse the first conflicting
named field in the fixed order `host`, `port`, `user`, `password`, `name`,
`sslmode`, `path`, `params`; `pool` remains deliberately separate because it
sizes a handle rather than changes its endpoint.
**Today:** 🟡 partial — deterministic implementation, but only the `host`
conflict has a focused control
**Evidence:** `fieldsBesideDSN` implements that exact ordered list
(`utils/vvdb/config.go:169-190`); both `prepare`, which every DSN builder uses,
and `Config.Validate` return its `ErrConflict` (`utils/vvdb/dsn.go:176-190`,
`utils/vvdb/config.go:142-151`); and `Open` reaches `DSN` before `sql.Open`
(`utils/vvdb/open.go:20-35`). `TestADSNIsUsedAsGivenAndRefusesToShareTheJob`
(`utils/vvdb/dsn_test.go:239-242`) pins only a `host` disagreement, not the
named credential/TLS/params arms.
**Blast radius:** silent wrong endpoint or security policy if the deterministic
refusal regresses

## Edge verdict

The worst new edge is a silent change of PostgreSQL endpoint: an ordinary `params.host` overwrites the socket address that the named `host` field supplied. Replica declarations are not a closed one-primary/one-secondary vocabulary: an empty object creates another primary pool, while a nested object is quietly erased, and an ordinary host-only derived replica aliases the primary's mutable `Params` map. A complete DSN is safely refused beside named connection fields in a deterministic order, but that guarantee has only a host-focused control. The lower-level malformed-port and bracketed-IPv6 gaps are source-established; SQLite in-memory and pgx pool-boundary outcomes remain unverified without local controls. pgx cancellation and pair-cleanup behavior also lack the consumer-level tests needed for a release claim, even though the latter cleanup branch looks intentional.

## Release blockers found here (edge)

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | `Params` overwrites the PostgreSQL socket's `host` after vvdb writes it; pgx gives URI query parameters final precedence | blocker | A field named `host` can silently connect the service to another server. This is the same two-sources-of-truth defect as the existing TLS row, but it changes the database endpoint rather than TLS policy |
| 2 | `replica: {}` is treated as a real replica of the primary, while a nested `replica:` is silently discarded | serious | One declarative topology either doubles primary connections or silently loses a server; neither outcome is what an operator declared |
| 3 | `ReadReplica` aliases the primary `Params` map whenever the replica has no parameter override | serious | A caller adding a read-only schema or session parameter mutates the next primary connection's configuration, producing a wrong-database or wrong-schema result without an error |
| 4 | `path: ":memory:"` with multiple SQLite connections or a read replica has no local vvdb control | sharp edge | The external-driver concern is real enough to require a measured two-handle fixture, but not yet precise enough to freeze a behavioural claim |
| 5 | Invalid TCP ports are not validated; pgx pool values above `int32` have no local boundary control | sharp edge | The former is source-established. The latter must be measured locally before the release documentation asserts an exact downstream failure |
