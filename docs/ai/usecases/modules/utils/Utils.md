# utils/vvflag · utils/vvcfg — start the process from a file an operator wrote, and refuse it here if it is wrong

**Covers:** `github.com/frostgrove/vv/utils/vvflag`, `github.com/frostgrove/vv/utils/vvcfg`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** not ready — a `--config-path` whose value is lost is reported as *absent*, so the process boots on whatever `CONFIG_PATH` says; `--migrate false` enables the boolean; an optional nested block never receives its environment fields; the first of two YAML or JSON documents is silently accepted; and a named FIFO can block start-up forever. The documented `main` also exits 2 under the application's own `flag.Parse()`, and `Load` merges process environment after the file rather than being a file-only fixture loader. Closed since the sweep by [[D-086]]: a mistyped key is now a refusal a strict load names (H-UTILS-03), every nested block validates itself and both failures are reported at once (H-UTILS-08), a container with no file boots when the author allowed it (H-UTILS-05), and the load says which file it read, which source named it and where each value came from (H-UTILS-14, H-UTILS-22).

**Status glyphs.** ✅ every must-hold holds today. 🟡 at least one holds and at
least one does not. ❌ none holds. The status answers "can a consumer do this
today", not "is it tested" — a missing test is a finding in the evidence line.

## What a consumer is actually trying to do

Someone has a service that runs in three places: a laptop, staging, production.
The difference between the three is a file. They want the first ten lines of
`main` to find that file, turn it into a struct with real types on it, and stop
the process right there if what it says cannot work. Nothing else in the program
should ever ask whether the configuration is sensible again.

The failure they are buying protection from is not a crash. It is a service that
starts. The address is empty so it listens on a port nobody routes to; the pool
size parsed as zero; the database block points at staging because a variable was
unset and something fell back. All of it comes up green, passes the health
check, and is discovered by a customer.

Most of what goes wrong goes wrong in the file, because a person edits it under
time pressure. A tab where the editor should have written spaces. One key
indented a level too deep, so a whole block belongs to the wrong parent. `port:
"eight thousand"`. A key renamed in the struct six months ago and never renamed
in production's copy. For all of those the operator wants one thing: refuse, and
say which line, so the fix comes off the log line without opening the pod.

The file is in git and the password is not. Hosts, ports, pool sizes and
timeouts are committed where a reviewer can see them; the database password and
the API secret arrive from the platform's secret store. So a value has to be able
to live only in the environment without appearing in the file — and a variable
the deployment happens to render empty must not blank what the file said.

The file is one file. The database goes in it, the listen address goes in it, the
token lifetime goes in it, and so does the list of origins the API answers. Each
of those blocks knows its own rules — a database configuration that names both a
full connection string and a host is a contradiction — and the author expects
those rules to run when the file loads, not the first time somebody opens a
connection. Underneath there is a smaller want: a program has one command line
and the application owns it, so reading the config path off it before anything
has decided what flags exist must not cost the author the ability to declare
their own.

Two questions arrive later and are asked by someone else. A new engineer runs the
binary with `--help` and wants to know what it can even be configured with. And
an hour into an incident, on-call wants the start-up log to say what this pod is
actually running with — not only which file it opened, because by then they have
learned that the filename does not decide the configuration.

## Happy cases

### H-UTILS-01 — Boot the service from a YAML file named on the command line
**Who:** the author of a public API, writing `main` for the first time
**Wants:** one line that turns `--config-path /etc/app.yaml` into a typed, validated struct.
**Story:** They declare a `Config` struct with `yaml:` tags, give it a `Validate()` that refuses an empty DSN, and call the loader in `main`. Deployment passes `--config-path`. A bad file must stop the process before the listener opens.
**Must hold:**
1. A path named on the command line *with a value* is the file that is read, and no other.
2. `Validate()` runs after decoding and before the call returns.
3. A validation failure is an error the caller can print and exit on, naming both the file and what was wrong with it.
4. Loading the configuration connects to nothing and starts no goroutine, so reaching the refusal costs nothing and cannot half-start the process.
**Today:** ✅ ready
**Evidence:** `utils/vvcfg/vvcfg.go:49-70`, pinned by `utils/vvcfg/vvcfg_test.go:36-48` and `:68-82`, with the vacuity control at `:84-91` — a config with no `Validate` loads, so a `Validate` that never ran could not masquerade as one that always passes. The "and no other" half of (1) is pinned by `TestResolutionPrefersTheFlagOverEnvironmentAndDefaultAndSaysWhichOneItUsed` (`utils/vvcfg/vvcfg_test.go`), which sets `CONFIG_PATH` and asserts the flag wins, then asserts the environment is the fallback and the source's default is the last resort — and asserts the reported origin each time, so answering the right file from the wrong source fails it. (4) holds: the whole path is a `Stat`, a decode and a method call, which is [[D-057]]'s rule one module earlier — and it is worth stating here because the sibling sweep's first blocker is the same promise broken one step later, where `dbpgx.Connect` dials in a goroutine (`docs/ai/usecases/modules/vvdb/Vvdb.md:876`). Must-hold 1 is narrowed on purpose: a flag whose value is missing is H-UTILS-18, and it is this sweep's worst finding. Two notes on the rest of the evidence, because it is weaker than it looks. The `Stat` at `:57-59` is unpinned and close to redundant — delete the whole block and the missing-file half still passes, because cleanenv's own `os.OpenFile` says `no such file or directory` (`cleanenv@v1.5.0/cleanenv.go:131`) — and the defect its comment names does not reach it, since `os.Stat` succeeds on a mode-0000 file. The directory half of `TestAMissingFileAndAnUnreadableOneAreDifferentMessages` (`vvcfg_test.go:58-65`) passes for a reason nobody wrote down: `os.Stat` succeeds on a directory too, so what refuses it is cleanenv's extension switch (`cleanenv.go:138,150`), reporting a directory as `file format '' doesn't supported by the parser`.
**If not ready:** —

### H-UTILS-02 — The operator broke the file
**Who:** whoever is editing production YAML at 18:00
**Wants:** the process to refuse and name the line.
**Story:** They add a second environment block and their editor writes a tab. Or one key sits a level too deep. Or `port:` now says `"eight thousand"`. The pod restarts.
**Must hold:**
1. A file that is not well-formed, or that holds a value the field cannot take, stops the load.
2. The refusal names the line, so it can be fixed from the log line alone.
3. A broken file and a missing file are distinguishable by something other than reading the message text.
**Today:** 🟡 partial
**Evidence:** (1) holds: `ParseYAML` is `yaml.NewDecoder(r).Decode(str)` (`cleanenv.go:159-160`) and its error is returned. (2) holds today, and only because yaml.v3 puts `line N:` in its own text — nothing in this repository asks for it and nothing pins it, so it is a dependency's property, not a guarantee this module makes. (3) does not hold: `parseFile` flattens the decoder's error with `%s` and `err.Error()` (`cleanenv.go:153`), so the `*yaml.TypeError` never reaches the caller as a value. `errors.As` finds nothing, `errors.Is` finds nothing, and a consumer who wants to tell "the file is broken" from "the file is gone" has string matching or nothing. Nothing in either package tests a malformed file at all.
**If not ready:** The consumer greps the message. Closing (3) means an `ErrMalformed` sentinel this package owns, and — for the typed decoder error underneath it — `vvcfg` owning the decode for the formats that have one, which is the same change H-UTILS-03 needs; closing (2) properly means pinning it with a test, so a dependency bump that drops line numbers is a failure and not a surprise.

### H-UTILS-03 — Catch a mistyped key in the file
**Who:** the same operator, ten minutes earlier
**Wants:** to be told they wrote `adress:` instead of `address:`.
**Story:** They rename a key while adding a second environment. The struct field never gets set. The service starts on the default.
**Must hold:**
1. A key in the file that matches no field in the struct stops the load.
2. The refusal names the key.
**Today:** ✅ ready
**Evidence:** a strict load reads the file a second time as a document and compares its keys with the declared shape, so a key no field declares is an `*UnknownKeysError` naming each key with its path — `utils/vvcfg/document.go:readDocument` and `:inspect`, `utils/vvcfg/schema.go:describe`, refused in `utils/vvcfg/vvcfg.go:Source.refusals` ([[D-086]], [[FL-026]]). Pinned by `TestAMistypedKeyStopsAStrictLoadAndNamesItself` and `TestANestedKeyNoFieldDeclaresIsNamedWithItsPath` (`utils/vvcfg/report_test.go`), each carrying its own control: a file whose keys all exist loads. A lenient `Load` reports the same keys instead of refusing (`TestAMistypedKeyIsReportedEvenWhenTheLoadIsNotStrict`), which is what lets a deployment be cleaned up before strictness is switched on. [[D-013]] records the same shape one subsystem over and is what this applies at start-up.
**Not covered by it:** `.edn` cannot be read as a document, so a strict load of one is `ErrUnreadableFormat` rather than a silent pass; that is stated in both module docs and pinned by `TestAFileFormatThatCannotBeInspectedIsRefusedOnlyWhenStrictnessNeedsIt`. A `.env` file is compared by declared variable name, not by key path.

### H-UTILS-04 — The file arrives where the platform puts it
**Who:** whoever writes the Helm chart
**Wants:** a ConfigMap mounted at `/etc/config/app` to load.
**Story:** The chart projects the config as a `subPath` mount, so the file has no suffix. Or the team calls it `app.conf`, the way every other service in the estate does.
**Must hold:**
1. Either it loads, or the refusal says in this project's words that the *name* decided the parser.
2. Which names work is written down where a consumer will look for it.
3. When the filename cannot carry the format, the caller can name it instead.
**Today:** ❌ missing
**Evidence:** cleanenv picks the decoder from `filepath.Ext` (`cleanenv.go:138`) and anything else — including the empty extension a `subPath` mount produces — returns `file format '' doesn't supported by the parser` (`:150`), which reaches the operator as `vvcfg: reading /etc/config/app: file format '' doesn't supported by the parser`: ungrammatical, behind no sentinel, and naming a library the consumer does not import. Neither `docs/modules/en/vvcfg.md:14` nor the package doc comment (`utils/vvcfg/vvcfg.go:3`) says the extension chooses the format; both list the formats as though any file could be any of them, and both omit `.edn`, which `cleanenv.go:138-148` dispatches. (3) has nothing at all: `Load` takes a path and no options (`utils/vvcfg/vvcfg.go:49`).
**If not ready:** The consumer renames the file to `.yaml` after their first deployment fails, having learned it from a sentence in somebody else's library. Closing it is an `ErrUnsupportedFormat` naming the supported extensions, two sentences in both module docs, and a `Format` option for the mount that cannot carry a suffix.

### H-UTILS-05 — Run the same binary with no config file at all
**Who:** whoever writes the deployment manifest, and the engineer who just cloned the repository
**Wants:** every value from environment variables in the cluster, and every value from the struct's own defaults on a laptop — no file either way, no code change.
**Story:** The image has no `/etc/app.yaml`. In the cluster the deployment sets `ADDR` and `DSN` exactly as the `env:` tags say. On a laptop `go run ./cmd/api` sets nothing at all, and `env-default:":8080"` is meant to be enough to see the thing run.
**Must hold:**
1. A deployment that names no file boots, filling the struct from `env:` tags and `env-default:` values.
2. A struct whose defaults alone are sufficient boots with no file and no variables.
3. `Validate()` still runs, so a missing required value still stops the process.
4. The author writes the same line in `main` for all three deployment styles, and whether "no file" is legal is the author's choice stated once, not a property of what the operator forgot.
**Today:** ✅ ready
**Evidence:** `Source.AllowNoFile` is the author's one statement that "no source named a file" is a deployment style rather than a mistake; with it the loader fills the struct from `env:` tags and `env-default:` values through cleanenv's environment-only door, and without it the same situation is still `ErrNoPath` (`utils/vvcfg/source.go:Source.Resolve`, `utils/vvcfg/vvcfg.go:decodeInto`, [[FL-026]]). Validation runs either way, over the whole tree. Pinned by `TestAConfigurationWithNoFileLoadsOnlyWhenTheAuthorAllowedIt` (`utils/vvcfg/vvcfg_test.go`), whose first half is the control: the same environment, the same struct, and a refusal when the author did not allow it. Must-hold 4 holds because the choice is a field on a value the author writes once, not a property of what the operator forgot.

### H-UTILS-06 — The file is committed and the password is not
**Who:** the team that reviews infrastructure changes in pull requests
**Wants:** hosts and pool sizes in git, credentials from the platform, one struct.
**Story:** `app.yaml` is in the repository with everything an outsider may read. `DB_PASSWORD` and the API secret come from a Kubernetes secret. Both halves land in the same struct at start-up.
**Must hold:**
1. A variable the deployment leaves empty does not blank what the file said.
2. What is layered on what is written down where the author declares the struct.
3. A field the struct author did not tag can still be supplied at load time, by the caller if not by the operator.
**Today:** 🟡 partial
**Evidence:** (1) does not hold: `readEnvVars` takes the value when `os.LookupEnv` reports the variable *present* (`cleanenv.go:425`) and applies it unconditionally (`:451`), so a Helm template that renders `value: ""` overwrites the file's host with the empty string. That is the same absent-versus-empty collapse this project prosecutes in [[D-002]] and [[UC-003]], on the path nobody has looked at. (2) does not: `docs/modules/en/vvcfg.md:65-71` presents "the precedence" as being about finding the *path* — "…and nothing else" — so a reader concludes the file is the configuration. It is not. (3) does not, and this is the vvcfg-owned half of the tag question: `Load` offers no override of any kind, so an untagged field is file-only for everyone. Which fields lack a tag is somebody else's struct and somebody else's sweep — `vvdb.Config.Params` is the live example and it is H-VVDB-02 must-hold 1 (`docs/ai/usecases/modules/vvdb/Vvdb.md:99`), not restated here.
**If not ready:** The consumer finds out that `DB_HOST=` blanks the host by having it happen. Closing (1) is a decision, not a patch — it is the reverse of the rule cleanenv chose — so the honest short-term move is the sentence in both module docs saying an exported-but-empty variable wins.

### H-UTILS-07 — Override one setting for a single run
**Who:** an engineer debugging staging
**Wants:** to raise the log level for one process without editing the deployed file.
**Story:** They set `LOG_LEVEL=debug` in the environment and restart the one pod. Later they do the same thing from the command line, by appending an override to the container's existing arguments.
**Must hold:**
1. An environment variable set at run time wins over the same value in the file. (This is the same one mechanism as H-UTILS-06's layering, seen from the other end; the evidence is shared.)
2. A value the file sets explicitly is not quietly replaced by a default.
3. Repeating a flag on the command line resolves the way the standard `flag` package resolves it, so an appended override wins.
**Today:** 🟡 partial
**Evidence:** (1) holds — `ReadConfig` runs the file first and the environment pass after (`cleanenv.go:97-104`), and a set variable is applied unconditionally (`:425,451`). (2) does not: `env-default` is applied whenever the field is still zero after decoding (`cleanenv.go:438-439`), so a file that says `debug: false` against `env-default:"true"` boots with debug on. (3) does not: `find` returns on the first match (`utils/vvflag/vvflag.go:72-90`), so `--port 1 --port 2` is 1, where the standard `flag` package gives 2 — and appending an override to an existing command line is how a container is usually overridden. That divergence is one of three (H-UTILS-19, H-UTILS-18) and none is written down.
**If not ready:** The consumer stops using `env-default` on any boolean or numeric field. They cannot put the defaults in a struct literal instead, because `Load` declares `var cfg T` itself (`utils/vvcfg/vvcfg.go:60`) and there is nowhere to hand one in — which is H-UTILS-25.

### H-UTILS-08 — Put the database block in the same file and have it check itself
**Who:** the author of a SaaS with a primary and a read replica
**Wants:** `db:` nested inside the application config, refusing its own contradictions at start-up.
**Story:** Their `Config` has a `DB vvdb.Config` field and an `analytics:` block beside it. The database configuration has real rules — a replica of another engine is not a replica — and those rules are already written. They expect them to run when the file loads.
**Must hold:**
1. A nested block that can refuse itself is asked to, without the author writing a forwarder.
2. The refusal names which block was wrong, not only what was wrong.
3. A file with two bad blocks reports both, so the operator does not learn their configuration one restart at a time.
**Today:** ✅ ready
**Evidence:** `utils/vvcfg/validate.go:ValidateTree` walks the exported shape, asks every node for `ValidateSelf` exactly once, prefixes each failure with the node's path and joins them, so all three must-holds are the same walk ([[D-086]], [[FL-026]]). Pinned by `TestEveryBlockThatCanRefuseItselfIsAskedWithoutAForwarder` and `TestAFileWithTwoBrokenBlocksReportsBoth` for (1) and (3), `TestAValidationFailureIsReachableAsAValueCarryingItsPath` for (2), and `TestANodeReachableTwiceIsAskedToValidateItselfOnce` for the bound that makes a self-referential config terminate (`utils/vvcfg/validate_test.go`). Cross-block rules are a second interface and a second pass — `TestCrossRulesRunAfterEveryNodeHasValidatedItselfAndNotOverABrokenTree`.
**The sharp edge, and it is deliberate:** a node spelling the older `Validate` owns its subtree and the walk stops there, because that method is as often a forwarder as a rule and because `vvdb.Config` validates a *merged* replica — asking the fragment directly refuses a legal file. `TestABlockSpellingTheOlderValidateOwnsWhatIsUnderIt` pins both halves, its control being the same shape spelling `ValidateSelf`, which is walked. An application that wants its own blocks walked renames the method.

### H-UTILS-09 — A required value the deployment forgot
**Who:** the author, on the day a new required field ships
**Wants:** a deployment that missed the new key to fail at start-up, loudly.
**Story:** They add `DSN` and tag it `env-required`. Staging has it, production's file was not updated. Production must not start.
**Must hold:**
1. A required value missing everywhere is a refusal, not a zero value.
2. `env-required` means what its name says.
**Today:** 🟡 partial
**Evidence:** (1) holds through the consumer's own `Validate()` (`utils/vvcfg/vvcfg.go:64-68`, pinned by `vvcfg_test.go:68-82`), which is the mechanism H-UTILS-01 rests on and the one to rely on. (2) does not. `env-required` fires only on `rawValue == nil && required && isFieldValueZero()` (`cleanenv.go:431-436`), so it means "required unless something already set it, or unless zero is a value you meant". A required `int` the file legitimately sets to `0` is refused; a required field the file set is not checked at all. Nothing in this repository pins the tag, and no module doc mentions it.
**If not ready:** The consumer uses `Validate()` and treats `env-required` as decoration. Worth one sentence in the module doc saying so, because the tag's name promises the opposite.

### H-UTILS-10 — Keep the application's own flags working
**Who:** the same author, adding `--addr` and `--migrate` a week later
**Wants:** `--config-path` and their own flags on one command line.
**Story:** They declare `flag.String("addr", …)` and call `flag.Parse()`, the way the examples in this repository do. Then they add the config loader. Deployment passes both flags.
**Must hold:**
1. Reading the config path does not require declaring it anywhere.
2. A command line carrying it survives the application's own `flag.Parse()`.
3. The author does not have to choose between this loader and the standard `flag` package.
**Today:** 🟡 partial
**Evidence:** (1) holds and is the package's whole reason for existing (`utils/vvflag/vvflag.go:39-65`). (2) fails whenever the flag actually appears ahead of the first positional argument: nothing exported removes the argument `vvflag` consumed, so the slice handed to `flag.Parse` still contains `--config-path`, which the `FlagSet` has never heard of — `flag provided but not defined` and `os.Exit(2)`. Three things make it survivable and none of them is documented: `flag` stops parsing at the first non-flag argument (`go doc flag`), so the subcommand shape in H-UTILS-11 escapes it; a deployment that uses `CONFIG_PATH` never touches `os.Args` (`utils/vvcfg/vvcfg.go:42-44`); and an author already using `flag` can declare `--config-path` properly and call `vvcfg.Load[T](*path)`, dropping one level. The repository shows both halves and never both at once — `_examples/pgx-fiber/main.go:71-72` declares a flag and parses, `docs/modules/en/vvcfg.md:36-40` is the other half — and nothing in either module doc mentions the collision.
**If not ready:** The consumer declares a dead `flag.String("config-path", "", "")` purely so `flag.Parse` tolerates it, or drops to `Find`+`Load`, which is the supported answer nobody has written down. Closing it is a function that hands back what it did not consume, plus a sentence in both module docs.

### H-UTILS-11 — One binary, several subcommands
**Who:** the author of a background job fleet
**Wants:** `worker run --config-path /etc/w.yaml` and `worker migrate --config-path /etc/w.yaml` to load the same file.
**Story:** The image ships one binary. The first argument selects what it does. Everything after it is flags.
**Must hold:**
1. A positional argument before the flag does not hide the flag.
2. `--` still ends the flags, so arguments meant for a child process are not read as configuration.
**Today:** ✅ ready
**Evidence:** `utils/vvflag/vvflag.go:72-90` scans the whole slice and does not stop at the first non-flag, and returns not-found at `--` (`:74`). Must-hold 2 is pinned by `utils/vvflag/vvflag_test.go:85-90`. Must-hold 1 — the premise of the case — is pinned by nothing: every `Parse` call in the test file starts with a flag, and the closest, `:80`, puts the positional *after* it. Nothing would fail if `find` were changed tomorrow to stop at the first non-flag, which is what the standard `flag` package does and what a contributor would reasonably assume.
**If not ready:** —

### H-UTILS-12 — A value read before anything has declared a flag
**Who:** the author of the service binary, adding a shutdown grace period
**Wants:** `--shutdown-grace 30s` read at the top of `main`, before the `FlagSet` exists.
**Story:** The value is needed by the same early code that finds the config file — the signal handler is wired before the application's flags are declared — so it cannot go through `flag`. They reach for the flag reader that is already in the module for exactly this.
**Must hold:**
1. A timeout reads as a duration from what an operator would type: `30s`, `2m`.
2. A named type over an integer works, because application code names its types.
3. A value that will not parse is an error, never a zero.
4. A number spelled the same way in the file and on the command line means the same number.
**Today:** 🟡 partial
**Evidence:** (2) and (3) hold and are well pinned — `coerce` switches on `reflect.Kind` precisely so a named type is not lost (`utils/vvflag/vvflag.go:103-108`, `vvflag_test.go:34-44`), and absent-versus-malformed is the package's headline invariant (`vvflag_test.go:10-32`). (1) does not hold at all. `time.Duration` has kind `Int64`, so `--shutdown-grace 30s` reaches `strconv.ParseInt` and comes back "not an integer" (`vvflag.go:118-123`); only `30000000000` works. There is no `encoding.TextUnmarshaler` branch (`:142-144`), where cleanenv has one (`cleanenv.go:473-475`). (4) does not: `vvflag` parses integers base 10 (`vvflag.go:119,125`) and cleanenv parses them base 0 (`cleanenv.go:503,519,528`), so `MODE=0755` — or `env-default:"0755"` — is 493 and `--mode 0755` is 755, with no error on either side. Two things make (1) worse than a missing feature: the standard `flag` package parses durations (`go doc flag`: "Duration flags accept any input valid for `time.ParseDuration`"), and so does the other half of this same module (`cleanenv.go:510-515`), so `connect_timeout: 5s` in the YAML works and `--connect-timeout 5s` on the command line does not.
**If not ready:** The consumer reads the flag as a string and calls `time.ParseDuration` themselves, losing the typed default. Closing (1) is two cases in the switch — `time.Duration` before the integer kinds, and `encoding.TextUnmarshaler` before the default. Closing (4) is one argument, and it is a choice: base 0 everywhere, or base 10 everywhere and a sentence saying so.

### H-UTILS-13 — Load a configuration fixture in a test
**Who:** the author, writing a test for the thing the config configures
**Wants:** a YAML fixture to produce the same struct on every machine.
**Story:** The test writes a file to a temp directory and loads it. It runs on a laptop, then in CI.
**Must hold:**
1. The same fixture file produces the same struct whatever the developer has exported in their shell, and whatever the CI runner exports.
2. The flag reader can be pointed at a slice rather than at `os.Args`.
**Today:** 🟡 partial
**Evidence:** (2) holds and carries its own control: `Parse`/`Or` take the slice (`utils/vvflag/vvflag.go:39,54`) and `vvflag_test.go:110-117` exists to fail if `Parse` ever goes back to reading `os.Args`. The package also exports `Lookup`, which *is* `Or` over `os.Args` (`vvflag.go:63-65`), so the door that control exists to close is exported beside it — deliberate, one call site, and worth a decision before the tag rather than after. (1) is false. `Load` calls `cleanenv.ReadConfig` (`utils/vvcfg/vvcfg.go:61`), which is `parseFile` **then** `readEnvVars(cfg, false)` (`cleanenv.go:97-104`), and that pass reads the process environment with `os.LookupEnv` (`:425`) and applies whatever it finds (`:451`). `Load(path)` is a two-source read with one argument. The package's own fixture type carries `env:"NAME"` and `env:"PORT"` (`utils/vvcfg/vvcfg_test.go:12-13`), so `NAME=zzz go test ./utils/vvcfg/` fails `TestLoadReadsThePathItIsGiven`. `PORT`, `ADDR` and `DB_HOST` are routinely exported by CI runners.
**If not ready:** The consumer clears the environment around every fixture load, or names their variables so nothing else could collide. Closing it is a way to say which layers fill the struct — the file-only door here, and its mirror, the environment-only door H-UTILS-05 needs.

### H-UTILS-14 — Which file booted this pod, and which source named it
**Who:** whoever is on call
**Wants:** one line in the start-up log naming the file and where the path came from.
**Story:** An hour into an incident the service is behaving like staging. They need to know what it read before they can know anything else.
**Must hold:**
1. The path the loader used is available to the caller.
2. Whether it came from the flag or from the environment is available to the caller.
**Today:** ✅ ready
**Evidence:** every load answers a report carrying the path it read and a `PathOrigin` — the caller, `--config-path`, `CONFIG_PATH`, the source's default, or nothing — and `Report.String` renders both as the first line of a start-up block (`utils/vvcfg/source.go:Source.Resolve`, `utils/vvcfg/report.go:Report.String`, [[FL-026]]). Pinned by `TestResolutionPrefersTheFlagOverEnvironmentAndDefaultAndSaysWhichOneItUsed` (`utils/vvcfg/vvcfg_test.go`), which asserts the origin as well as the file for all three sources, so a loader that answered the right path from the wrong source fails it. The half that matters is closed: a flag is visible in the pod spec and a variable is not, and the two are now distinguishable.

### H-UTILS-15 — What can I even configure?
**Who:** a new engineer, or an operator at 18:00
**Wants:** `--help` to list the keys and environment variables the binary reads.
**Story:** They run the binary with no arguments, then with `--help`, before opening any source.
**Must hold:**
1. The flag that decides everything appears in the binary's own usage output.
2. The environment variables the struct declares can be printed without importing anything the consumer did not choose.
3. So can the file keys, which is a different and larger job.
**Must not break:** printing this must not replace the application's own usage text.
**Today:** ❌ missing
**Evidence:** `--config-path` is never declared to any `FlagSet` — that is the point of `vvflag` (`utils/vvflag/vvflag.go:39-65`) — so it appears in no usage text anywhere. It is not invisible: a binary run with no arguments prints `ErrNoPath`, whose text names both sources (`utils/vvcfg/vvcfg.go:24`). What is missing is the listing, not the hint. For (2), cleanenv generates it from the `env-description` tag (`cleanenv.go:44`, `GetDescription` at `:569`, `Usage` at `:616`, `FUsage` at `:622`) and `vvcfg` exposes no door to any of it, so a service that wants it imports cleanenv directly — the same encapsulation break H-UTILS-05 costs. (3) is not covered by that machinery at all: `GetDescription` skips every field with no `env` tag (`cleanenv.go:586-588`) and returns `""` when none has one, so a `yaml:`-only key prints nowhere and `FUsage` prints an empty line. Wrapping cleanenv closes (2) and leaves (3) needing its own walk over `yaml:` tags.
**If not ready:** The consumer writes the usage text by hand and it goes stale, or imports cleanenv. Closing (1) and (2) is one exported function that *chains* rather than replaces; declaring (3) out of scope is also an answer, but it has to be an answer, because after the tag `Usage` is a new exported name.

### H-UTILS-16 — Keep the password out of the refusal
**Who:** anyone who has to pass an audit
**Wants:** a failed start-up that says what is wrong without printing the credential.
**Story:** The config holds a database password. Something in it is wrong and the process refuses to start. The refusal goes to stdout in a container, and stdout ships to an aggregator that is not in the secure zone.
**Must hold:**
1. No error `vvcfg` itself constructs contains a field value that could be a secret.
2. Every place in either package where a configuration value *can* reach an error message is listed in `docs/modules/en/vvflag.md` and `docs/modules/en/vvcfg.md` — `vvflag`'s parse error is on that list and nothing else is.
**Today:** 🟡 partial
**Evidence:** (1) holds by construction and nothing pins it — the three wrapping sites carry the path and nothing else (`utils/vvcfg/vvcfg.go:58,62,66`). (2) has no list: neither module doc mentions the subject. Two places put a value in a message. cleanenv's environment pass reports `parsing field %v env %v: %v` (`cleanenv.go:452`) and the last verb is `strconv`'s own error, whose text is `strconv.ParseInt: parsing "<the value>": invalid syntax` (`:503,519,528`) — so a malformed value echoes itself. And `vvflag` echoes the raw value by design: `fmt.Errorf("vvflag: --%s=%q: %w", name, raw, err)` (`utils/vvflag/vvflag.go:46`), which is right for `--port=abc` and wrong the moment anybody reads a secret-bearing flag through `Or`. The struct half of this — that `%+v` on a `vvdb.Config` prints the password — is H-VVDB-10 in the sibling sweep and belongs there; it is also what decides whether H-UTILS-22 can exist.
**If not ready:** The consumer does not read secrets through `vvflag`, and does not learn that from anything. One sentence in `docs/modules/en/vvflag.md` under "the one rule worth knowing" — the value is in the message on purpose — a matching sentence in `vvcfg.md`, and a test that pins (1) so a fourth wrapping site fails it.

### H-UTILS-17 — The refusal is a line, not a stack
**Who:** the operator reading the pod's logs
**Wants:** one sentence saying what is wrong with the file.
**Story:** The config fails to load. What the container prints is the only thing they get.
**Must hold:**
1. A start-up refusal on the documented path is a message, not a goroutine dump.
**Today:** ❌ missing
**Evidence:** the documented `main` is `cfg := vvcfg.Must(vvcfg.Auto[Config](os.Args[1:]))` in four module docs (`docs/modules/en/vvcfg.md:37`, `docs/modules/ru/vvcfg.md:37`, `docs/modules/en/vvdb.md:66`, `docs/modules/ru/vvdb.md:66`), in the package's own godoc (`utils/vvcfg/vvcfg.go:82-85`) and in the sibling sweep's ideal call site (`docs/ai/usecases/modules/vvdb/Vvdb.md:690`), and `Must` panics (`utils/vvcfg/vvcfg.go:86-90`, pinned at `vvcfg_test.go:128-135`), so the operator gets a stack wrapped around one useful sentence. Six places, and the godoc is the one a consumer sees without opening `docs/`: `go doc github.com/frostgrove/vv/utils/vvcfg` prints it, and after a tag it is the shipped recommendation.
**If not ready:** The consumer writes `cfg, err := …; if err != nil { log.Fatal(err) }` — three lines more than the docs show, and what every real `main` should do. Closing it either way is a documentation decision plus, if the short path is to stay one line, a `MustLoad` that prints one line and calls `os.Exit(1)`.

### H-UTILS-18 — Fail loudly when the deployment forgets the file
**Who:** whoever is on call
**Wants:** a service that names no config file to refuse, not to invent one.
**Story:** A chart change drops the volume mount, or renders `--config-path $CONFIG` with `CONFIG` unset — sometimes as the last argument, sometimes with `--addr :8080` after it.
**Must hold:**
1. No file is searched for by convention. There is no implicit `./config.yaml`.
2. A path that was named and does not exist says so, differently from a file that cannot be read.
3. A path that was *meant* to be named and arrived empty is not treated as though nothing was named.
4. A flag whose value went missing does not silently adopt the next argument as the filename.
**Today:** 🟡 partial
**Evidence:** (1) holds and is pinned (`utils/vvcfg/vvcfg.go:42-45`, `vvcfg_test.go:107-115`). (2) holds (`vvcfg_test.go:50-66`), with the caveat on its evidence recorded in H-UTILS-01. (3) does not, and this is the worst finding in this file. `--config-path` as the last argument, with its value lost to an unset shell variable, is reported as *absent* — `find` returns not-found when nothing follows a value-taking flag (`utils/vvflag/vvflag.go:86-89`), deliberately, pinned by `vvflag_test.go:92-97`. `Or` then folds absence into `""` (`vvflag.go:54-60`) and `Find` re-derives absence from `p != ""` (`utils/vvcfg/vvcfg.go:37-44`) and falls through to `CONFIG_PATH`. In a container where `CONFIG_PATH` is also set — a docker-compose that exports it globally, a second vv service on the same host — the process starts on a file the operator did not ask for, silently. `--config-path=` takes the same fall-through from the other direction (`vvflag.go:77-79`). (4) does not either, and it is the commoner shape, because a flag is rarely last on a real command line: `find` returns `args[i+1]` for anything except `--` (`vvflag.go:86-88`), on purpose, so a negative number can be passed at all (`vvflag_test.go:46-56`). `--config-path $CONFIG --addr :8080` with `CONFIG` unset makes the path `--addr`, and the operator gets `vvcfg: stat --addr: no such file or directory` for a chart that dropped a variable. That one fails loudly, which is better, but it points at the wrong thing.
**If not ready:** Nothing the consumer can write catches (3), because they cannot see the difference either. Part of it closes inside `vvcfg` with no decision at all: `Find` should call `Parse` and branch on `errors.Is(err, ErrAbsent)` instead of re-deriving absence from an empty string, so `--config-path=` stops being indistinguishable from an unnamed path. The value-taking flag named with nothing after it is a third answer — neither absent nor malformed — and needs a name (`ErrMissingValue`) and a decision doc, because changing it contradicts a test that pins the current behaviour on purpose. (4) is separable from both: refusing a following argument that begins with `--` is a two-line change to `find` that does not touch the negative-number case.

### H-UTILS-19 — The flag spelled the way Go binaries are usually spelled
**Who:** whoever writes the Kubernetes manifest or the Dockerfile `CMD`
**Wants:** `-config-path /etc/app.yaml`, one dash, to work.
**Story:** They have deployed a dozen Go services and every one of them takes single-dash flags, because that is what `flag.PrintDefaults` prints. They write the manifest the same way and the pod comes up.
**Must hold:**
1. Either both spellings work, or the one that does not is refused rather than ignored.
2. Which spellings work is written down where a consumer will look for it.
**Today:** ❌ missing
**Evidence:** `find` matches only `"--"+name` (`utils/vvflag/vvflag.go:71,77,80`), so `-config-path` matches nothing and `Parse` returns `ErrAbsent`. `Find` then falls through to `CONFIG_PATH` (`utils/vvcfg/vvcfg.go:42-44`) — bit for bit the failure H-UTILS-18 must-hold 3 describes, reached by a spelling rather than by a lost variable: with `CONFIG_PATH` set, the process boots on the wrong file and says nothing; without it, `ErrNoPath` names a flag the operator believes they passed. The package doc lists three forms and all of them are double-dash (`utils/vvflag/vvflag.go:8-13`, `docs/modules/en/vvflag.md:35-43`), which is a description, not a warning — nothing says the single-dash form is not one of them, and the standard package a reader is coming from accepts both.
**If not ready:** The consumer learns it from a deployment that read the wrong file. Closing it is either accepting `-name` in `find` — which makes `-x` ambiguous with a combined short-flag convention this package does not have, so it is cheap — or one sentence in both module docs and the package comment saying double dash only.

### H-UTILS-20 — Rely on the tag vocabulary the wrapper inherited
**Who:** the author declaring the struct
**Wants:** to know which tags do something and which are decoration.
**Story:** They read cleanenv's README, tag a field `env-upd` so it can be reloaded, add an `Update()` method they saw in the same README, and give a list field an `env-separator`.
**Must hold:**
1. Which tags are honoured, and which formats work, is written down by this project, not inherited silently.
2. Two hooks that both run at load have a stated order.
**Today:** 🟡 partial
**Evidence:** the module doc names `yaml:`, `env:` and `env-default:` by example and stops (`docs/modules/en/vvcfg.md:24-27`); `env-upd`, `env-prefix`, `env-required`, `env-description` and `env-separator` appear nowhere in this repository at all. The format list is not merely thin, it is wrong: `docs/modules/en/vvcfg.md:14` and `utils/vvcfg/vvcfg.go:3` both say "YAML, TOML, JSON, `.env`", and `parseFile` dispatches five extensions including `.edn` (`cleanenv.go:138-148`). Three inherited behaviours are unstated. `env-upd` (`cleanenv.go:47`) is honoured only by `UpdateEnv` (`:112`), which `vvcfg` never calls, so a field tagged for reload gets silence — see H-UTILS-24. `readEnvVars` calls `Update()` on any config implementing cleanenv's `Updater` (`:410-414`) — a second undocumented hook that runs *before* the environment pass and therefore before `Validate`, and nothing states that order. And `env-prefix` (`:53`, applied at `:345`) is the only thing that stops two nested `vvdb.Config` blocks reading the same `DB_HOST`, which is H-VVDB-09 in the sibling sweep and is mentioned in no doc here either.
**If not ready:** The consumer reads cleanenv's source. Closing it is a table in `docs/modules/en/vvcfg.md` and its Russian twin: the tags this project supports, the five extensions that pick a parser, and the two hooks in the order they run.

### H-UTILS-21 — A list-valued setting
**Who:** the author of the same public API
**Wants:** `allowed_origins`, seed brokers, trusted proxies — a list in the file, overridable from the environment.
**Story:** The file carries three origins. In one environment the platform team needs a fourth, so they set `ALLOWED_ORIGINS` in the deployment. Later a template renders it empty by accident.
**Must hold:**
1. A slice or map field fills from the file and from the environment, and how the environment spells a list is written down.
2. An empty variable does not turn a populated allowlist into an empty one.
3. A list can be read from the command line too, or it is stated that it cannot.
**Today:** ❌ missing
**Evidence:** (1) works and is undocumented: `parseValue` dispatches `reflect.Slice` and `reflect.Map` to `parseSlice`/`parseMap` (`cleanenv.go:543-556`), splitting on `env-separator` or the default `,` (`:364-367`, `:25-26`) — and `env-separator` is one of the five tags this repository never mentions (H-UTILS-20). (2) is H-UTILS-06's empty-variable collapse landing where the blast radius is worst: `ALLOWED_ORIGINS=` is *present*, so `readEnvVars` applies it (`:425,451`) and `parseSlice` returns an empty slice for a blank value (`:198-213`). The file said three origins; the struct says none, and whether that opens the API or closes it depends on how the consumer wrote the check. (3) is a flat no: `coerce` sends `reflect.Slice` to `unsupported kind slice` (`utils/vvflag/vvflag.go:142-143`), so `vvflag` cannot read a list at all — and a repeated `--origin` flag is the only way a command line spells one, which `find` also cannot do (H-UTILS-07 must-hold 3).
**If not ready:** The consumer discovers the separator by reading cleanenv, and discovers the empty-variable rule by having an allowlist emptied. Closing (1) is documentation; (2) is the same decision as H-UTILS-06; (3) is a decision to state the limit rather than a feature to add — a repeated flag is what `flag.Value` is for, and that is the standard package's job.

### H-UTILS-22 — What is this pod actually running with
**Who:** whoever is on call, and whoever writes the incident ticket afterwards
**Wants:** one block in the start-up log showing the configuration the process resolved, credentials withheld.
**Story:** The service is behaving like staging. They already have H-UTILS-14's line: the file is `/etc/app.yaml`, from the flag. It is the right file. It is still behaving like staging.
**Must hold:**
1. The resolved values — after file, environment and defaults — are available to the caller in a form fit for a log line.
2. Fields carrying a credential are withheld from it.
3. It says where each value came from, or at least admits that the file is not the whole story.
**Today:** 🟡 partial, and the missing half is a refusal
**Evidence:** (3) holds and is the point: the report says, per field, whether the value came from the file, from a named environment variable, or from a declared default, so the case this row exists for — a correct filename and one stray variable — reads off the start-up block (`utils/vvcfg/report.go:buildReport`, pinned by `TestTheReportSaysWhereEachValueCameFrom`). (2) holds by construction rather than by review, which is why (1) does not: **the report carries no values at all**, redacted or otherwise ([[D-086]]). A redaction list is one forgetful field type away from a leak, and the line on-call copies into a ticket is exactly the line a password would be in. `TestTheReportNamesWhereAValueCameFromAndNeverTheValue` is the pin, with a sentinel password in both the file and the environment and an assertion that the rendered block contains neither while still naming `DB_PASSWORD` as the source. The "or at least admits that the file is not the whole story" half of (3) is now literal: a format the loader cannot read back as a document — `.edn` — makes every origin `OriginUnknown` and puts the reason in `Report.NotInspected`, which the rendered block prints, rather than calling a value the file set a default (`TestAFileTheLoaderCouldNotInspectIsSaidSoAndItsValuesAreNotCalledDefaults`, with an inspectable file as its control).
**If not ready:** an operator who needs the resolved values themselves prints the struct, which is where the field types' own redaction — `vvdb.Secret` and its siblings ([[D-081]]) — is the protection. Whether this module should ever render values is a decision, not an omission.

### H-UTILS-23 — The file is there and says nothing
**Who:** whoever writes the Helm chart, on the day a template renders to nothing
**Wants:** a present-but-empty config file to fail in a way that names the file, not the parser.
**Story:** A ConfigMap key renders empty, or a volume is read a moment before the file lands. The path exists. The file has no content, or only comments.
**Must hold:**
1. An empty file is a third state, distinguished from "not there" and from "malformed".
2. Whatever the outcome, the struct's own `Validate()` is asked, so a missing required field is named rather than reported as a parse failure.
**Today:** 🟡 partial
**Evidence:** the process does refuse, which is the right direction: yaml.v3 returns `io.EOF` for a document with no content, so `ParseYAML` fails (`cleanenv.go:159-160`) and `parseFile` flattens it at `:153` into `vvcfg: reading /etc/app.yaml: config file parsing error: EOF`. "EOF" tells an operator nothing about a file they are looking at. (2) does not hold: `Load` returns at `:62` and never reaches the `Validator` assertion at `:64`, so a struct that would have said "dsn is required" says "EOF" instead. The neighbouring case is worse and is not covered here: a file that parses but sets nothing — `# nothing yet` is *not* EOF for every format — reaches `Validate` with a zero struct, which is the correct behaviour and is what H-UTILS-05 must-hold 2 wants generalised.
**If not ready:** The consumer reads the pod's file by hand to find out it is empty. Closing it is the same `ErrMalformed` H-UTILS-02 needs, with the empty case named separately because the fix an operator applies is a different one.

### H-UTILS-24 — Change one setting without restarting the pod
**Who:** the operator, the day after start-up validation starts working
**Wants:** to raise the log level, or rotate a signing secret, without a restart.
**Story:** They tag a field `env-upd`, following cleanenv's README, and add an `Update()` method. Then they change the value and wait.
**Must hold:**
1. Either reload works, or this project says it does not and why, in the place the author is reading.
**Today:** ❌ missing
**Evidence:** `vvcfg` calls `ReadConfig` only (`utils/vvcfg/vvcfg.go:61`), never `UpdateEnv` (`cleanenv.go:112`), so `env-upd` (`:47`) does nothing and the field it marks is silent. `Update()` *is* called — by `readEnvVars` on any config implementing cleanenv's `Updater` (`:410-414`) — but at load, once, before the environment pass, which is not reload and is nowhere documented (H-UTILS-20). Nothing in `docs/modules/en/vvcfg.md` mentions reload in either direction. An author who reads cleanenv's README, which is what this case's Story has them do, will believe it works. `docs/roadmaps/2026-08-26-1522-product-roadmap.md:2675-2745` is where the real answer lives (F-29: snapshots, debounce, per-component opt-in) and it is a roadmap item, not a shipped feature.
**If not ready:** The consumer tags a field and waits for something that never happens. Closing it before the tag is one sentence — "configuration is read once, at start-up; `env-upd` is inherited and inert; reload is F-29" — and that sentence is worth more than the feature, because silence beside an inherited tag that promises otherwise is what makes it a bug report.

### H-UTILS-25 — A base file and a per-environment overlay
**Who:** the platform team running the same service in five environments
**Wants:** `app.yaml` for everything shared, `app.prod.yaml` for the six lines that differ.
**Story:** They keep one committed base file and a small overlay per environment, the way they already do for every other tool in the estate. They expect to name both and get one struct.
**Must hold:**
1. Two files can fill one struct, later overriding earlier, field by field.
2. Defaults expressed as a Go struct literal are a legal first layer, so the author is not forced into `env-default:` strings.
3. `Validate()` runs once, on the result, not once per layer.
**Today:** ❌ missing
**Evidence:** `Load` declares `var cfg T` itself (`utils/vvcfg/vvcfg.go:60`) and returns a pointer to it, so nothing can hand in a struct to decode over — even though `cleanenv.ReadConfig` would decode into an already-populated one happily, which is exactly how the environment pass layers over the file. There is no exported entry point that takes a `*T`. (2) fails for the same reason and is the same gap H-UTILS-07 hits from the defaults side; (3) is unreachable because (1) is.
**If not ready:** The consumer calls `cleanenv.ReadConfig` twice themselves — a direct dependency on the wrapped library, and they have to re-implement the `Validator` hop after it. Closing all three is one function that takes a destination: `LoadInto[T](path string, cfg *T, opts ...Option) error`, with `Load` becoming its one-line caller.

### H-UTILS-26 — The validation hook is actually wired
**Who:** the author, six months in, renaming things
**Wants:** to know that the `Validate` they wrote is the one the loader calls.
**Story:** They move `Validate` onto a nested type, or rename it, or add a parameter, or write it on a type that is loaded by value somewhere else. Nothing complains. The service boots.
**Must hold:**
1. A configuration whose author intended it to be validated cannot load unvalidated in silence.
**Today:** ❌ missing
**Evidence:** `Load` reaches the hook through an optional type assertion with no else branch (`utils/vvcfg/vvcfg.go:64-68`). A method that does not exactly satisfy `Validator` — a rename, a stray argument, a receiver on the wrong type — produces a config that loads clean, and the package's stated point (`utils/vvcfg/vvcfg.go:9-11`, `docs/modules/en/vvcfg.md:52`) quietly does not happen. The repository already treats this shape as first-class on the test side: `TestAConfigWithoutValidateIsLoadedAsIs` (`vvcfg_test.go:84-91`) exists precisely because a `Validate` that never ran looks identical to one that always passes. Nothing offers the consumer-facing version of that control — there is no way for an author to say "this config must be validated". It is the same class as H-UTILS-09's `env-required` and cheaper to close.
**If not ready:** The consumer finds out when a bad configuration boots. Closing it is a `MustValidate()` option that turns a missing hook into a load error, or — free, and worth doing anyway — two sentences in both module docs saying the hook is an optional interface and what an author should assert in their own test.

## The DX this should have

### The call site

```go
type Config struct {
    Addr string      `yaml:"addr" env:"ADDR" env-default:":8080"`
    DB   vvdb.Config `yaml:"db"`
}

func main() {
    cfg, err := vvcfg.Auto[Config](os.Args[1:])
    if err != nil {
        log.Fatal(err) // one line for the operator, no stack
    }
    ...
}
```

`Auto` keeps two results forever. That is not a detail: `Must[T](cfg *T, err
error) *T` is a two-result adapter, so a three-result `Auto` deletes the one-line
form that six places in this repository lead with. Nothing in that block says how
the file was found, and nothing forwards a `Validate` call into `cfg.DB`. Both
happen because the shape of the struct already said they should.

### Turning one knob

```go
opts := []vvcfg.Option{
    vvcfg.Named("conf", "APP_CONFIG"), // both ends of the precedence, renamed together
    vvcfg.Optional(),                  // no source named a file is a deployment style
    vvcfg.Format(vvcfg.YAML),          // the subPath mount has no suffix — H-UTILS-04
}

// Every early flag is taken out of the line before flag.Parse sees it, and the
// last remainder is what flag gets. Order matters: flag.Parse exits 2 on an
// argument no FlagSet was told about.
cfg, rest, err := vvcfg.Take[Config](os.Args[1:], opts...)   // H-UTILS-10
if err != nil {
    log.Fatal(err)
}
grace, rest, err := vvflag.TakeOr(rest, "shutdown-grace", 30*time.Second) // H-UTILS-12
if err != nil {
    log.Fatal(err)
}

flag.Usage = vvcfg.Usage[Config](flag.PrintDefaults)  // adds to it, never replaces — H-UTILS-15
flag.CommandLine.Parse(rest)

// Dropping a level keeps the options, because this is Take's own body.
path, origin, err := vvcfg.Find(os.Args[1:], opts...)  // origin: FromFlag, FromEnv, FromNothing
cfg, err = vvcfg.Load[Config](path, opts...)
log.Printf("config: %s (%s)\n%s", path, origin, vvcfg.Effective(cfg))  // H-UTILS-14, H-UTILS-22
```

### Why this shape

**The now-or-never list is short, and it is the only part of this section that
has to be decided before the tag.** `docs/api/surface.md:872-877` records
`Find(args []string) (string, error)`, `Auto[T](args []string) (*T, error)`,
`Load[T](path string) (*T, error)` and `Must[T](cfg *T, err error) *T`. Adding a
variadic `...Option` to the three is source-compatible. Adding `Take`, `LoadInto`,
`Usage`, `Effective` and the sentinels later is source-compatible. **`Find`
gaining a second result is not**, and neither is anything that changes `Auto`'s
arity, and neither is removing `vvflag.Lookup` (`utils/vvflag/vvflag.go:63-65`),
which is the one exported door that reads `os.Args` and therefore the one that
undoes H-UTILS-13's control. Those three questions are the release gate. The rest
is scheduling.

**The options go on all three levels, and an option a level cannot honour is an
error rather than a shrug.** `Named` is a property of `Find` and `Format` is a
property of `Load`, so a caller who assembles one slice and drops a level will
hand `Load` an option it cannot use. Two option types would catch that at compile
time and would also make the assembled slice unusable at any single level, which
is the ergonomic the drop-a-level path exists for. So: one `Option`, and `Load`
returns an error naming an option it cannot honour. That is [[D-021]]'s own rule
for the analogous case — an unknown option in a `db` tag is a `SchemaError`,
"because a typo'd option is indistinguishable from a missing feature otherwise"
(`docs/ai/decisions/D-021…md:65-66`) — applied at start-up, which is where
everything in this package fails anyway.

**`Named` renames both ends or neither.** A binary that spells its flag `--conf`
does not spell its variable `CONFIG_PATH`, and `CONFIG_PATH` is a name that two
vv services on one host — or one docker-compose that exports it globally —
collide on. One knob, two names, and the module doc's precedence block renders
the configured ones.

**`Take` is where the remainder comes from, not `Auto`.** A `Without` the caller
names by hand can drift from the flag `vvcfg` actually read, and it cannot know
the arity: `--verbose` is one argument and `--config-path x` is two, and the only
thing that decides is `isBool(def)` (`vvflag.go:101`), which a by-name stripper
has no access to. Getting that wrong leaves a stray value as the first
positional, and `flag` stops parsing at the first positional — so every later
flag is silently ignored, on the happy path, in the function whose purpose is
stopping a silent misconfiguration. One scan returns the value and what is left,
and they cannot disagree. `vvflag.Take` is the same operation one level down and
is `Parse`-shaped: it returns `ErrAbsent` alongside the default and the untouched
remainder, and `TakeOr` folds absence away the way `Or` does. In a package whose
headline invariant is that absent and malformed are different answers, a new
entry point that leaves absence unstated is the one thing it cannot do.

**The `Take` chain is for one or two values, not four.** Each link rebinds `rest`,
and a missed rebind silently drops a later flag. Two early values read before
anything has declared what flags exist is the case this package is for; a third
is a sign the application should declare a `FlagSet` and let `flag` do it, which
is also where `-h` comes from.

**`Optional()` and H-UTILS-18 have to be ordered explicitly, or they cancel.**
`Optional()` means *no source named a file*. A path that was named and is missing
stays fatal, and a flag that was named with its value lost is a refusal reached
*before* `Optional()` is consulted. Without that ordering the most-wanted knob in
this document makes its worst defect worse: today a lost value at least boots on
`CONFIG_PATH`; under a permissive `Optional()` it would boot on `env-default`
values with no file and no message. Whether it then means "the environment is
enough" or "the defaults are enough" is H-UTILS-05's must-holds 1 and 2, and it
is one line in the option's doc comment — the honest answer is both, because they
are one code path.

**Three sentinels, because the house rule is `errors.Is` and this package has
none for its three sharpest failures.** `ErrMalformed` for a file that will not
parse (H-UTILS-02, H-UTILS-23), `ErrUnsupportedFormat` for the extension that
picked no parser (H-UTILS-04), `ErrMissingValue` for a flag named with its value
lost (H-UTILS-18). Both packages already export one each, so the pattern needs no
argument — only the names. [[D-015]] is the precedent rather than the authority:
it governs package `crud`'s sentinels, and CLAUDE.md's "compare errors with
`errors.Is` against the exported sentinels, never by string" is what reaches here.

**The validation walk needs its rule written down or must-hold 2 of H-UTILS-08 is
not checkable.** Depth-first over exported struct fields and non-nil pointers to
structs; children before the parent; the parent's own `Validate` last, and
skipped when the walk reached it by promotion, so an embedded block is not
validated twice; slice and map elements visited by index and key; each block
visited once, memoised by address, so a hand-written forwarder left over from
today does not report the same failure twice. Each failure is prefixed with the
key the operator typed, taken from the tag of the format that was actually
loaded and falling back to the Go field name — `db.replica: replica engine
"mysql" differs from "postgres"`, `backends[2]: …`. That fallback is not
pedantry: a TOML consumer has no `yaml:` tag, a `.env` consumer has no nesting,
and a value that arrived from `DB_HOST` under an env-only deployment should be
named `DB_HOST`, because that is what the operator typed. Bare `errors.Join`
produces no block name at all, so a file with a `db:` and an `analytics:` block
yields two identical `unknown engine` lines and the operator cannot tell which to
fix. A nil `*Config` is skipped and never dereferenced: `vvdb.Config.Validate`
has a value receiver, so a walk that dereferences an absent optional block panics
on a configuration that is correct — a loader that panics on a block you
deliberately left out is worse than one that never walked.

**Joining every *validation* failure rather than returning the first** is the
same promise the error subsystem makes on the wire. An operator fixing a config
file one error per restart is the same person as a client fixing a payload one
422 per request. The scope is validation and nothing more: the YAML decode is one
`Decode` call and the environment pass returns on the first field that will not
parse (`cleanenv.go:451-453`), so joining across all three passes means owning
both, which is the same large change as H-UTILS-03.

**The division with the standard `flag` package stays where the module doc puts
it.** `vvflag` is for a value that must be read *before* anything has declared
what flags exist; `flag` is for the application's own flags, and it gets `-h` for
free. The duration gap is not a request to compete with `flag` — it is that a
duration is exactly the kind of value read early, that `flag` parses one, and
that cleanenv parses one in the other half of this same module.

### What it must not break

- **[[D-013]] is the precedent for H-UTILS-03, not the authority.** Its invariant
  is about field paths in a request that become SQL, and its "why" is that a
  dropped filter returns the whole table. An unknown YAML key answers no
  question and leaks no rows. Refusing one is a **new decision** with a
  defensible opposite — a shared file read by two binaries, a rolling deploy
  where the key lands in the file before the new image does — and under this
  repository's own rule it goes in front of the owner as a decision, not settled
  in a sweep. That is also why this file no longer pre-forbids an
  `AllowUnknownKeys()` knob: whether one is needed is part of the same decision.
- **[[D-057]] — the application opens the connection.** Nothing proposed here
  lets `vvcfg` connect to anything, and `Optional()` returns a struct exactly as
  `Auto` does now. H-UTILS-01 must-hold 4 is the same promise stated as a
  guarantee a consumer can check.
- **[[D-036]] and [[D-051]] — the dependency question.** Everything proposed for
  `vvflag` is standard library: `Take` needs nothing, the duration branch needs
  `time`, `TextUnmarshaler` needs `encoding`. `vvflag` is in the root module,
  whose `go.mod` has no requires at all, and stays there. The validation walk and
  `Effective` are `vvcfg`'s and need `reflect`, which is stdlib in a module that
  already has four requires — no bearing on the root module's promise either way.
  Closing H-UTILS-03 is the one that costs: `vvcfg` would import
  `gopkg.in/yaml.v3` directly — and `github.com/BurntSushi/toml`, if TOML closes
  too — where both are `// indirect` today (`utils/vvcfg/go.mod`). Under D-051
  that is one decision and not three, because nobody takes cleanenv without both,
  but it is exactly the check D-051 asks a reader to apply, so it is written here
  rather than discovered in a `go.mod` diff.
- **The `utils/` boundary, which is [[D-058]]'s and does not mention `errs`.**
  The line is "nothing under `utils/` imports `crud/`, `auth/`, `port/` or
  `remote/`" (`docs/ai/decisions/D-058…md:119-121`), and `scripts/checks.sh:SUBSYSTEMS` is
  the four names `check-utils` greps. An `errs` import from `vvcfg` would pass
  that check. What forbids it is [[D-057]] — "It imports nothing of vv. Not
  `crud`, not `errs`" — written about `vvdb`, not about `vvcfg`. So aggregating
  validation failures with `errors.Join` from the standard library is the right
  call for consistency with its neighbour, and it is a consistency argument, not
  a rule. Saying otherwise sends a reader to a decision that does not say it.
- **The "no search, no default location" rule** in the module doc. `Optional()`
  does not look for a file; it accepts that none was named. The rule forbids
  *guessing* a path, and a deployment that names none has not been guessed at.
- **[[D-021]], and the house-style rule that magic stays concentrated.** The walk
  is reflection in `utils/`, which is the part of the tree defined by what it is
  not, so it is argued rather than assumed: it is opt-in by the presence of a
  method, it uses only `reflect`, it fails at start-up and never at request time,
  and it removes a per-block forwarding method that otherwise grows forever.
  That is the trade D-021 describes, and it is the timing clause that carries it.
  D-021 also forbids adding an explicit type parameter to a call site that
  currently infers, which is why `LoadInto[T](path, &cfg)` is preferred to a
  second `LoadFile[T](path)` door for the fixture case: the destination infers.
- **The `Must` recommendation is decided in six places or in none.**
  `docs/modules/en/vvcfg.md:37`, `docs/modules/ru/vvcfg.md:37`,
  `docs/modules/en/vvdb.md:66`, `docs/modules/ru/vvdb.md:66`, the package's own
  godoc (`utils/vvcfg/vvcfg.go:82-85`) and the sibling sweep's ideal call site
  (`docs/ai/usecases/modules/vvdb/Vvdb.md:690`). The godoc is the one that ships.
- **Recommending against `Must` does not indict `vvdb.MustOpen` or
  `dbpgx.MustConnect`.** The panic-at-start-up shape is deliberate here —
  [[D-021]] records `sqlrepo.Define` panicking on a broken declaration as part of
  the trade that makes the reflection safe. The distinction is what the input is.
  A broken model declaration is a programmer error, fixed in CI, and a stack is
  the right output because a programmer is reading it. A broken config file is
  operator input arriving at 18:00, and the reader is not a programmer. That is
  the whole argument, and it applies to exactly one of the three `Must`s.
- **[[UC-021]] is where the only stated guarantee about this loader lives.** Its
  must-hold 2 says the database config "nests inside the application's own
  configuration", must-hold 7 says "every refusal happens at start-up", and its
  index status is `covered` (`docs/ai/usecases/Index.md:92`). H-UTILS-08 is the
  case that tests both. Either UC-021 drops to partially covered, or it says why
  a refusal that happens at `Open` rather than at load still satisfies 7 — and it
  cannot say that for the replica engine check, which nothing but `Validate`
  runs.
- **Roadmap F-29 already proposes a different option vocabulary for this package
  and has to be reconciled, not ignored.**
  `docs/roadmaps/2026-08-26-1522-product-roadmap.md:2690-2700` shows
  `vvcfg.Watch[Config]("config.yaml", vvcfg.Validate(), vvcfg.Debounce(time.Second))`
  — where `Validate()` is an *option* and this file keeps `Validator` as an
  interface, and where the path is a literal argument rather than something
  `Find` produced. Two option vocabularies in one package is exactly what a tag
  freezes. The reconciliation this file proposes: `Validator` stays an interface
  because it is already shipped and already the point of the package, F-29's
  `Validate()` option becomes `MustValidate()` (H-UTILS-26) with the opposite
  meaning it needs, and `Watch` is post-tag. F-29 also owns the reload question
  H-UTILS-24 raises and the redaction invariant H-UTILS-22 needs, so a sentence
  pointing at it is the cheapest half of both.

## DX verdict

Distance measures the size of the fix, not the size of the damage; the blocker
number in the middle column is the damage.

| What the ideal asks for | Today | Distance |
|---|---|---|
| One line in `main` for a validated config | `vvcfg.Must(vvcfg.Auto[Config](os.Args[1:]))` — one line, and it panics (#19); the shape this file recommends is four, because this repository writes a one-line `if err != nil` exactly once and it is inside a doc comment | none in mechanism, three lines in ceremony |
| Four composable levels (`Find`/`Load`/`Auto`/`Must`) | all four exported, each usable alone | none |
| Exported surface | 27 lines in `vvcfg` since [[D-086]] (`docs/api/surface.md:2196-2222`), 4 in `vvflag` | already past the ideal's ~16; what a tag still freezes is the shape of `Load`, `MustLoad` and `LoadFrom`, not the count |
| A path that was named is the path that is read | falls through to `CONFIG_PATH` when the value is lost (#1), when the next argument is another flag (#11), or when the flag is spelled with one dash (#10) | small in `Find`, a decision in `vvflag` |
| A fixture that loads the same on every machine | `Load` reads the environment too, undocumented, with no file-only door (#4) | small |
| No file is a legal deployment | ~~not reachable~~ `Source{AllowNoFile: true}` is the author saying so once, and the struct is filled from the environment alone (#5, closed by [[D-086]]) | none |
| The app keeps its own flags | reachable three undocumented ways — put the flag after a positional, use `CONFIG_PATH`, or declare the flag and call `Load` — and the documented pair exits 2 (#2) | small |
| Nested blocks validate themselves | ~~one forwarding method per nested block~~ `ValidateTree` asks every node for `ValidateSelf` once and names each failure by its path (#6, closed by [[D-086]]) | none, except that a node spelling the older `Validate` still owns its subtree on purpose |
| Every *validation* failure at once | ~~first failure only~~ joined, one `*ValidationError` per block (#6, closed by [[D-086]]) | none for validation; the decode and environment passes stay first-failure unless `vvcfg` owns them |
| Unknown key in the file is refused | ~~dropped in silence~~ `LoadStrict` refuses and names each key with its path; a lenient load carries the same finding in the report (#3, closed by [[D-086]]) | none for YAML, JSON, TOML and dotenv; `.edn` cannot be inspected and says so |
| A broken or empty file is a typed error | flattened to text; `errors.As` finds nothing, and an empty file says `EOF` (#14, #22) | large, same change as the row above |
| A duration flag, and one integer syntax | read it as a string and call `time.ParseDuration`, losing the typed default (#9); `0755` means two different numbers depending on which half read it | small |
| Two files, or defaults in Go, filling one struct | impossible; `Load` owns the struct it decodes into (#23) | small |
| The start-up log says what the process is running with | the report names the file, which source named it, and per field whether the value came from the file, a named variable or a default (#17 closed by [[D-086]], #13 half) | none for provenance; rendering the values themselves stays a decision |
| `--help` lists what the binary reads | nothing; the flag that decides everything is declared to no `FlagSet` (#16) | small for the variables, large for the file keys |
| A list-valued setting | works from the file, undocumented from the environment, emptied by a blank variable, unreadable from the command line (#21) | small (documentation) plus #8's decision |
| A `Validate` that is wired is provably wired | an optional type assertion with no else branch (#12) | small |

**Overall:** The short path is genuinely short and the four levels are a good
spine; nothing here proposes changing it. The problem is the second step. Every
"large" row and most of the small ones are a case where turning one knob means
leaving the package — writing your own `Find`, importing cleanenv directly,
maintaining a forwarding `Validate` by hand — because the package is four
functions with no options at all, so wanting something slightly different means
stopping. What has changed since the last pass is the shape of the worst news:
it is not that the code is short of features, it is that several of its stated
guarantees are not the ones it delivers. `Load(path)` reads two sources.
`env-required` means required-unless-already-set. A named path can be read as an
unnamed one, three different ways. A `Validate` that is not called looks exactly
like one that passed. Each of those is a sentence in a doc or a signature change,
and all of them are cheaper before a tag than after.

## Release blockers found here

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | `--config-path` with a lost value is reported as absent, so `Find` falls through to `CONFIG_PATH` and the process boots on a file nobody named | blocker | Silent wrong-environment start-up in exactly the deployment shape the package is for; the collapse of absent and malformed is what `vvflag` says it exists to prevent, and `vvflag_test.go:92-97` pins the collapse |
| 2 | The documented `main` exits 2 under `flag.Parse()` whenever the flag precedes a positional argument, and the three ways out are all undocumented | blocker | Both halves are shown in this repository's own docs and example; a consumer following them ships a binary that dies on its own flag |
| 3 | ~~An unknown key in the YAML is silently ignored~~ **closed by [[D-086]]** | serious | A renamed key boots on defaults with no way to see it; [[D-013]] records the identical shape one subsystem over, though extending it here is a decision the owner has to make |
| 4 | `Load(path)` also reads the process environment, and nothing exported reads the file alone | serious | An exported `PORT` or `DB_HOST` on a laptop or a CI runner silently changes what a fixture loads; neither module doc says the environment is read at all |
| 5 | ~~A deployment with no config file cannot boot~~ **closed by [[D-086]]**: `Source.AllowNoFile` is the author's statement, and the sibling sweep's stale pointer at `Vvdb.md:108` is the remainder | serious | Environment-only is an ordinary container deployment and defaults-only is what a fresh clone runs; the escape requires importing cleanenv directly, undoing the encapsulation the module exists for. The sibling sweep defers this here and its pointer back names the wrong case, so closing it means fixing `Vvdb.md:108` too |
| 6 | ~~Nested `Validate()` is never called, and a file with two bad blocks reports one~~ **closed by [[D-086]]**: the loader walks the tree, joins failures and prefixes each with its path | serious | The replica engine-mismatch check lives only there, so a replica declaring another engine opens and gets addressed in the primary's dialect; [[UC-021]] is marked `covered` on a guarantee this contradicts. The wiring half of the same consequence is the sibling sweep's blocker 2 (`Vvdb.md:877`) and the fixes are different — this row is the walk, that row is `vvdb` calling its own `Validate` |
| 7 | `env-default` overwrites a value the file set to a zero — `debug: false` boots as true | serious | The absent-versus-zero collapse this project polices everywhere else ([[D-002]], [[UC-003]]), inherited from the dependency and undocumented |
| 8 | An exported-but-empty variable blanks what the file said: `DB_HOST=` wins over `host: db.prod`, and `ALLOWED_ORIGINS=` empties an allowlist | serious | A Helm template that renders an empty value is ordinary; the failure is a service that starts, pointed nowhere or open to nobody, which is the exact shape this module exists to prevent |
| 9 | `time.Duration` does not parse in `vvflag`: `--timeout 30s` is "not an integer" while `complex128` parses; and integers are base 10 here and base 0 in the other half | serious | The standard `flag` package parses durations and so does cleanenv in the same module, so the one reader that does not is the one a consumer reaches for first — and `0755` silently means 755 on the command line and 493 from the environment |
| 10 | `-config-path` with one dash matches nothing and falls through to `CONFIG_PATH` | serious | It is the spelling `flag.PrintDefaults` prints and the spelling most Kubernetes manifests for Go binaries use; the failure is #1's, reached by writing the flag the ordinary way |
| 11 | `--config-path $VAR --addr :8080` with `VAR` unset makes the path `--addr` | serious | A chart that drops a variable gets `stat --addr: no such file or directory`, which points at the wrong thing; separable from #1, because excluding a following `--` argument does not touch the negative-number case |
| 12 | A `Validate` that does not exactly satisfy `Validator` is silence — no validation, no warning, a process that boots | serious | The hook is the package's stated point, reached by an optional assertion with no else branch; a rename or a stray argument turns the guarantee off and nothing says so |
| 13 | Nothing renders the effective *values* — **half closed by [[D-086]]**: the report names, per field, whether the value came from the file, a named variable or a default, and deliberately carries no values | serious | With #7, #8 and #11 live, the filename does not determine what the process is running with; on-call reads the file name, believes it, and looks in the wrong place |
| 14 | A malformed file reaches the caller as flattened text; the decoder's typed error is destroyed by `%s` | sharp edge | The most common configuration failure there is, and a consumer cannot tell a broken file from a missing one except by matching strings |
| 15 | The filename extension picks the parser; an extensionless ConfigMap mount fails with `file format '' doesn't supported by the parser` | sharp edge | An ordinary Kubernetes mount breaks a deployment for a reason no document here mentions, in a dependency's ungrammatical sentence, with no option to name the format instead |
| 16 | `--config-path` appears in no `--help` anywhere, and nothing exposes the keys or variables the struct declares | sharp edge | A new engineer cannot find out what the binary reads without opening the source; cleanenv generates the environment half and `vvcfg` exposes no door to it |
| 17 | ~~`Find` discards which source named the path~~ **closed by [[D-086]]**: `Source.Resolve` answers a `PathOrigin` and the report carries both | sharp edge | The start-up line that would make #1 survivable in production cannot be written — and `Find`'s signature is the one thing here that cannot change after the tag |
| 18 | `vvflag` differs from the standard `flag` package three times without saying so: a repeated flag takes the first, `-name` with one dash is not a flag, and a value-taking flag with nothing after it is absent rather than an error | sharp edge | All three spellings are what a deployment actually types; the third is the mechanism of #1 and the second is #10, so the divergences are not cosmetic |
| 19 | `Must` panics, and six places lead with it — four module docs, the package godoc, and the sibling sweep | sharp edge | The operator gets a goroutine dump wrapped around one useful sentence, and the godoc is the copy that ships in the artefact |
| 20 | `env-required` means "required unless something already set it, and unless zero is what you meant"; nothing pins it and no doc mentions it | sharp edge | A tag whose name promises a guarantee it does not make, used as one of two mechanisms for the case that stops a bad deployment |
| 21 | List and map fields work from the environment through `env-separator`, which this project documents nowhere, and cannot be read from the command line at all | sharp edge | Every real service config has a list; the author cannot find out how the environment spells one, and `coerce` reports `unsupported kind slice` |
| 22 | A present-but-empty config file refuses with `EOF`, and `Validate()` never runs | sharp edge | The ordinary ConfigMap-rendered-to-nothing shape; the message names no file and the struct that would have named the missing field is never asked |
| 23 | No entry point takes a destination struct, so two files cannot fill one struct and defaults cannot be a Go literal | sharp edge | The base-plus-overlay layout is standard practice, and the workaround is calling the wrapped library directly and re-implementing the validation hop |
| 24 | The tag vocabulary and the format list are undocumented and wrong: five tags appear nowhere in this repository, `.edn` is missing from both the module doc and the package comment, and `env-upd` marks a field for a reload nothing performs | sharp edge | An author reading cleanenv's README tags a field and waits for a reload that will never happen; F-29 is the real answer and nothing points at it |
| 25 | `vvflag` echoes the raw flag value into its error by design, and nothing pins that `vvcfg`'s three wrapping sites do not | sharp edge | A secret read through `Or` reaches the log aggregator; the rule is right for `--port=abc` and unstated everywhere |
| 26 | Neither package owns a use case, a running example or any integration coverage — **the flow half is closed**: [[FL-026]] | sharp edge | The only stated guarantee about this loader lives in [[UC-021]], another module's use case, where nobody changing `vvcfg` will look; `grep -rn vvcfg docs/ai/flows/` is empty and `_examples` names it in one comment |

**Housekeeping found on the way, not a blocker:** `gofmt -l` is not silent, and
it was five files, not three (`utils/vvcfg/vvcfg.go` is clean since [[D-086]]): `utils/vvdb/dbpgx/dbpgx.go`,
`utils/vvdb/dbpgx/dbpgx_test.go`, `test/bridge/fieldviolation_test.go` and
`test/dsn/dsn_test.go`. All five have two `github.com/…` import specs with no
blank line between them, in the wrong order, so gofmt reorders them; the
crudfiber and crudgin blocks that look similar are separated by a blank line and
are clean. Fixing only the `utils/` three leaves the gate red.

## Contested

- **H-UTILS-01 stays ✅.** A reviewer asked for 🟡 because blocker #1 falsifies
  "the path named on the command line is the file that is read". The guarantee was
  narrowed instead — a path named *with a value* — and the unnarrowed version is
  now H-UTILS-18's must-hold 3, where the blocker is. Two cases making opposite
  claims about one input was the real defect; downgrading both would have hidden
  which half works.
- **H-UTILS-11 stays ✅ although must-hold 1 is pinned by nothing.** Two reviewers
  read that as the vacuity defect this file prosecutes elsewhere. The status
  answers "can a consumer do this today", and they can; the missing test is a
  separate finding and is in the evidence line. The glyph legend under the header
  now says so once, so the argument does not have to be made per case.
- **`Auto` keeps two results; the remainder comes from `vvcfg.Take`.** The two
  reviewers who caught the arity contradiction proposed opposite fixes — one
  wanted `Auto` fixed at three results, one at two. Two wins, because `Must` is a
  two-result adapter and six places in this repository lead with
  `Must(Auto(...))`. A three-result `Auto` deletes that line as a side effect of
  fixing an unrelated defect, which is the change nobody would have chosen if it
  had been proposed on its own.
- **Blocker #6 stays, and so does the sibling sweep's blocker 2.** A reviewer
  called them one consequence counted twice in two release lists. They are, and
  both rows are still right: the roots and the fixes differ, and closing either
  one alone leaves the other's path broken. The row now names which document owns
  which half rather than leaving the owner to discover it.
- **The duration gap stays `serious`, and H-UTILS-12 was rewritten around the
  value the case is really about.** A reviewer noted the original Who — a
  forty-line script — is what the standard `flag` package is for, and gets `-h`
  for free. Agreed, and the script is gone. It stays serious because `flag`
  parses durations and cleanenv parses durations in the other half of this same
  module, so the gap is an inconsistency inside one release, not a missing
  feature; the integer-base divergence found alongside it is the sharper form of
  the same complaint.
- **The `flag.Parse` collision stays a blocker although the sentence was wrong.**
  Reviewers were right that `CONFIG_PATH` and the subcommand shape both escape it;
  the case and the table say so. It stays a blocker because all three escapes
  are undocumented and the pair that *is* documented — `Must(Auto(os.Args[1:]))`
  and `flag.Parse()` — is the pair that exits 2.
- **H-UTILS-19's number was reused rather than left as a hole.** The old
  H-UTILS-19 (boot on struct defaults alone) is merged into H-UTILS-05 as
  must-hold 2, which all three reviewers asked for; the number now carries the
  single-dash flag case. Anyone holding a round-1 reference to H-UTILS-19 should
  read H-UTILS-05.

## Edge cases

### E-UTILS-01 — A boolean written as `false` must not turn an operation on
**Shape:** misuse
**Setup:** A deployment writes `--migrate false`, `--dangerous false`, or another boolean flag in the space-separated form it uses for every non-boolean setting.
**What the consumer does:** It expects that spelling either to set the flag false or to refuse as an unsupported spelling before the process acts.
**What must happen:** The loader must never silently interpret a following literal `false` as a positional argument while treating the flag itself as true.
**Today:** ❌ wrong or unhandled
**Evidence:** An exact boolean flag returns `"true"` before `find` looks at the following argument (`utils/vvflag/vvflag.go:80-85`); only `--name=false` reaches `strconv.ParseBool` (`:77-79,106-117`). `TestABoolFlagStandsAlone` deliberately pins that a following positional is not consumed (`utils/vvflag/vvflag_test.go:70-83`), but no test covers `--name false` or requires an error. The docs list the standalone boolean form (`docs/modules/en/vvflag.md:35-43`) but do not call this destructive-looking near miss out.
**Blast radius:** silent wrong answer

### E-UTILS-02 — A child process's `--config-path` stays positional after `--`
**Shape:** boundary
**Setup:** A wrapper starts `worker exec -- --config-path child.yaml`, while the wrapper itself supplies `CONFIG_PATH=/etc/worker.yaml`.
**What the consumer does:** It uses `--` to give the child ownership of every following argument.
**What must happen:** The child argument must not override the wrapper's configuration; the wrapper may fall back to `CONFIG_PATH`, or return `ErrNoPath` when it is unset.
**Today:** ❓ unverified
**Evidence:** `find` returns not-found immediately at the end marker (`utils/vvflag/vvflag.go:72-76`), after which `Find` consults only `CONFIG_PATH` (`utils/vvcfg/vvcfg.go:37-45`). The flag-layer behaviour is pinned by `TestDoubleDashEndsTheFlags` (`utils/vvflag/vvflag_test.go:85-90`), but no end-to-end `Find` control combines `--` with the environment fallback.
**Blast radius:** none

### E-UTILS-03 — An upper-case extension still selects the intended decoder
**Shape:** boundary
**Setup:** A Windows-built artifact or a copied ConfigMap is named `APP.YAML` rather than `app.yaml`.
**What the consumer does:** It passes that exact path to `Load` and expects YAML, not an unsupported-format error caused only by casing.
**What must happen:** Format recognition must be case-insensitive, or the error must say that casing is significant.
**Today:** ❓ unverified
**Evidence:** `vvcfg.Load` delegates the supplied path unchanged (`utils/vvcfg/vvcfg.go:49-63`), and cleanenv lower-cases `filepath.Ext(path)` before its YAML/JSON/TOML dispatch (`cleanenv@v1.5.0/cleanenv.go:129-150`). Neither `utils/vvcfg/vvcfg_test.go` nor the dependency's format tests exercise an upper-case filename, so the composed consumer guarantee is not pinned.
**Blast radius:** none

### E-UTILS-04 — A lower-case configuration field must not disappear silently
**Shape:** misuse
**Setup:** A tired author writes `dsn string \`env:"DSN"\`` instead of exporting `DSN`, and supplies the secret only through the environment.
**What the consumer does:** It starts the service without a parent `Validate`, assuming the tagged field was filled.
**What must happen:** The loader must reject an unfillable tagged field, or document and detect that exported fields are required; it must not return a successful zero-valued configuration.
**Today:** ❌ wrong or unhandled
**Evidence:** cleanenv skips every field that cannot be set (`cleanenv@v1.5.0/cleanenv.go:355-358`), so it neither reads nor reports the `env:"DSN"` tag. `vvcfg.Load` returns the zero-valued config when `&cfg` does not implement the optional `Validator` interface (`utils/vvcfg/vvcfg.go:60-69`), and its tests deliberately show an unvalidated config loading successfully (`utils/vvcfg/vvcfg_test.go:84-91`). No test covers a tagged unexported field.
**Blast radius:** silent wrong answer

### E-UTILS-05 — An optional nested pointer supplied only through environment must not stay nil
**Shape:** degenerate declaration
**Setup:** A service declares `TLS *TLSConfig \`yaml:"tls" env-prefix:"TLS_"\`` and sets `TLS_CERT` and `TLS_KEY` in its deployment, with no `tls:` block in the file.
**What the consumer does:** It treats the pointer as an optional block and expects its environment-tagged fields to make it present, or a clear refusal that pointer nesting is unsupported.
**What must happen:** The loader must allocate and fill the nested block, or fail before returning a config that silently has TLS disabled.
**Today:** ❌ wrong or unhandled
**Evidence:** cleanenv recurses into nested values only when `fld.Kind() == reflect.Struct` (`cleanenv@v1.5.0/cleanenv.go:336-346`); a `*TLSConfig` is not traversed, so its fields and their `env:` tags never reach the `os.LookupEnv` loop (`:416-443`). With no parent validator, `vvcfg.Load` returns that nil pointer successfully (`utils/vvcfg/vvcfg.go:60-69`). No adjacent test covers pointer-valued nested configuration.
**Blast radius:** silent wrong answer

### E-UTILS-06 — An empty preferred environment alias must not mask a usable fallback
**Shape:** seam
**Setup:** A field uses cleanenv's alternative names, `env:"DATABASE_URL,DB_URL"`; a platform injects `DATABASE_URL=` but the application provides a valid `DB_URL`.
**What the consumer does:** It expects the usable fallback to win, or an explicit error naming the empty preferred source.
**What must happen:** An empty first alias must not silently blank a string setting or prevent a later valid alias from being considered without documentation of that precedence.
**Today:** ❌ wrong or unhandled
**Evidence:** cleanenv splits the names in tag order (`cleanenv@v1.5.0/cleanenv.go:374-382`) and stops at the first one that is merely present (`:424-428`); it applies that empty value as-is (`:442-452`) and never looks at `DB_URL`. `vvcfg` offers no origin or layer policy around `ReadConfig` (`utils/vvcfg/vvcfg.go:49-69`), and no test covers alternative environment names, especially the present-but-empty case.
**Blast radius:** silent wrong answer

### E-UTILS-07 — The generic argument must not make a pointer config a dependency error
**Shape:** misuse
**Setup:** Seeing that `Load` returns `*T`, an author writes `vvcfg.Load[*Config](path)` rather than `vvcfg.Load[Config](path)`.
**What the consumer does:** It expects a clear wrapper-level rejection of the unsupported shape, before deployment, rather than a cleanenv reflection message after the file has been decoded.
**What must happen:** The public generic entry point must either support pointer `T` consistently or reject it with a typed `vvcfg` error explaining the correct instantiation.
**Today:** 🟡 partial
**Evidence:** `Load` declares `var cfg T` and passes `&cfg` to cleanenv (`utils/vvcfg/vvcfg.go:49-63`), so pointer `T` supplies `**Config`. cleanenv unwraps only one pointer and then rejects any non-struct kind as `wrong type ptr` (`cleanenv@v1.5.0/cleanenv.go:301-323`); the error is merely wrapped as `vvcfg: reading <path>` (`utils/vvcfg/vvcfg.go:61-63`). The API docs show only `Auto[Config]` (`docs/modules/en/vvcfg.md:23-40`), and no test pins this misleading but compilable call.
**Blast radius:** confusing error

### E-UTILS-08 — A FIFO must not be able to hold start-up forever
**Shape:** partial failure
**Setup:** `CONFIG_PATH` resolves to a named pipe left by an init or sidecar process whose writer never opens it.
**What the consumer does:** It starts the service and needs either a prompt refusal that the path is not a regular configuration file or a caller-controlled deadline.
**What must happen:** A special file must be rejected before blocking, or `Load` must accept a context/deadline that can end the wait.
**Today:** ❌ wrong or unhandled
**Evidence:** `Load` uses `os.Stat` only to establish that the path exists (`utils/vvcfg/vvcfg.go:53-59`), then hands the path to `cleanenv.ReadConfig` (`:60-63`); cleanenv opens it with `os.OpenFile` (`cleanenv@v1.5.0/cleanenv.go:129-135`). Neither exported function takes a context, an `io.Reader`, or a file-kind policy. The adjacent test checks a directory but not a FIFO (`utils/vvcfg/vvcfg_test.go:50-66`).
**Blast radius:** crash

### E-UTILS-09 — The file inspected must be the file decoded
**Shape:** concurrency
**Setup:** A ConfigMap-style writer atomically replaces the configuration path, or an attacker able to write its directory swaps a symlink, between the loader's existence check and its open.
**What the consumer does:** It expects the loader either to decode the object it inspected or to avoid claiming that a separate preflight check establishes what will be loaded.
**What must happen:** The stat/open sequence must use one opened descriptor or have no TOCTOU pre-check; otherwise the configuration provenance is not a stable fact.
**Today:** ❌ wrong or unhandled
**Evidence:** `vvcfg.Load` calls `os.Stat(path)` (`utils/vvcfg/vvcfg.go:53-59`) and only afterwards asks cleanenv to reopen the pathname (`:60-63`, `cleanenv@v1.5.0/cleanenv.go:129-135`). No file descriptor crosses that boundary, and no test changes a path between those calls.
**Blast radius:** silent wrong answer

### E-UTILS-10 — A second configuration document must not be ignored
**Shape:** adversarial input
**Setup:** A YAML file contains an approved first document followed by `---` and an unapproved second document; JSON has two adjacent top-level objects after a concatenation mistake.
**What the consumer does:** It expects either every supplied document to have an explicit merge rule or the file to be refused as ambiguous.
**What must happen:** Trailing documents and trailing non-whitespace must be rejected; accepting only the first makes an operator believe a setting was applied when it was not.
**Today:** ❌ wrong or unhandled
**Evidence:** cleanenv's YAML and JSON parsers each call `Decode` exactly once (`cleanenv@v1.5.0/cleanenv.go:158-166`) and do not attempt a second decode to require `io.EOF`; `vvcfg.Load` exposes no strict-decoding option (`utils/vvcfg/vvcfg.go:49-63`). The dependency tests cover one valid document per format (`cleanenv@v1.5.0/cleanenv_test.go:1324-1440`), and no adjacent test supplies a second document or trailing JSON object.
**Blast radius:** silent wrong answer

### E-UTILS-11 — A huge mounted configuration needs a bounded read
**Shape:** scale
**Setup:** A broken mount serves a multi-gigabyte YAML file, or an otherwise valid file contains a huge list that exceeds the process's intended start-up memory budget.
**What the consumer does:** It wants to set a reasonable maximum input size and receive one startup error rather than let configuration parsing consume unbounded memory or time.
**What must happen:** The loader must provide a caller-selectable byte limit (and report the path and limit) before handing input to a decoder.
**Today:** ❌ wrong or unhandled
**Evidence:** `vvcfg.Load` accepts only a pathname and immediately delegates it (`utils/vvcfg/vvcfg.go:49-63`); cleanenv opens that path and passes the unbounded file reader directly to the chosen decoder (`cleanenv@v1.5.0/cleanenv.go:129-160`). There is no `io.LimitReader`, limit option, or size-oriented test in either adjacent test file.
**Blast radius:** crash

### E-UTILS-12 — Pointer: process environment is part of the declared load input
**Owner:** H-UTILS-13 owns fixture isolation and the missing file-only/environment-snapshot API. This module supports one start-up contract: establish the process environment before calling `Load`, then do not mutate it while loading.
**Setup:** An application loads its configuration after deployment has established the process environment.
**What the consumer does:** It treats the file and the already-established environment as the two documented inputs; tests that mutate environment concurrently are outside that contract and need H-UTILS-13's explicit isolation route.
**What must happen:** Documentation must say that `Load` merges environment after parsing the file, so callers do not mistake `Load(path)` for file-only input.
**Today:** 🟡 partial
**Evidence:** every `Load` calls `cleanenv.ReadConfig` (`utils/vvcfg/vvcfg.go:60-63`), which parses then reads process environment (`cleanenv@v1.5.0/cleanenv.go:97-104`) field by field through `os.LookupEnv` (`:416-452`). The package fixture already has `env:"NAME"` and `env:"PORT"` (`utils/vvcfg/vvcfg_test.go:11-13`), but the module documentation presents `Load(path)` without saying environment is a second input (`docs/modules/en/vvcfg.md:42-52`).
**Blast radius:** confusing error

### E-UTILS-13 — A misspelled YAML or JSON key must not vanish
**Shape:** adversarial input | misuse
**Setup:** An operator writes `adress: ":8080"` instead of `addr:` in YAML, or ships the same misspelling in JSON after a field rename.
**What the consumer does:** They expect start-up to refuse the unknown key and identify it, rather than run with a default or zero value while the reviewed file appears to set it.
**What must happen:** YAML and JSON key strictness must have one explicit policy: refuse unknown keys, or deliberately permit them under a named compatibility policy. Until that decision is made, the silent default is not release-ready.
**Today:** ❌ wrong or unhandled
**Evidence:** cleanenv invokes plain `yaml.NewDecoder(r).Decode(str)` and `json.NewDecoder(r).Decode(str)` with neither `KnownFields(true)` nor `DisallowUnknownFields()` (`cleanenv@v1.5.0/cleanenv.go:158-166`); `vvcfg.Load` adds no decoder policy around `ReadConfig` (`utils/vvcfg/vvcfg.go:49-63`). No adjacent `vvcfg` test supplies an unknown YAML or JSON key.
**Blast radius:** silent wrong answer

## Edge verdict

The worst edge failures are silent configuration changes: a separated boolean `false` enables the operation, an unexported field or optional pointer block remains at zero despite its tags, an empty primary environment alias defeats its valid fallback, an unknown YAML/JSON key vanishes, and a second document is ignored. The end-marker/environment and upper-case-extension journeys have plausible component evidence but lack exact end-to-end controls. On the availability side, a valid-looking FIFO has no cancellation path and large input has no bound; the preliminary `Stat` also gives a false sense of path provenance because a separate open follows it. Process environment is a supported start-up input, not a promised concurrent snapshot; fixture isolation remains H-UTILS-13.

## Release blockers found here (edge)

| # | What | Severity | Why it blocks |
|---|---|---|---|
| 1 | `--migrate false` (and every other spaced boolean false) silently becomes true | serious | A common manifest spelling can enable a destructive action while leaving `false` as an ignored positional argument |
| 2 | An unexported tagged field and an environment-only nested pointer block both return as zero/nil without a refusal | serious | A one-character declaration mistake or an ordinary optional TLS block can boot a service without the secret or security setting the deployment supplied |
| 3 | An empty first alternative environment variable masks a valid fallback | serious | Shared platform variables routinely exist empty; the process reads a blank connection setting while the usable value is present |
| 4 | YAML/JSON decoders accept the first document and ignore an appended configuration document | serious | An operator can ship a change that the pod silently does not use, then diagnose the running service from the wrong file content |
| 5 | A FIFO at `CONFIG_PATH` blocks `Load` without a deadline or regular-file check | serious | A stalled init/sidecar path prevents the service from starting and cannot be cancelled through this API |
| 6 | `Stat` and decoder open are separate pathname operations | sharp edge | A replacement or symlink swap can make the loader inspect one object and run another, invalidating path provenance |
| 7 | No caller-set configuration size limit exists | sharp edge | A broken or unexpectedly huge mount can exhaust start-up resources before the service has a chance to report a normal refusal |
| 8 | `Load[*Config]` compiles but reaches a dependency's `wrong type ptr` error | sharp edge | The public generic shape invites the instantiation and gives the author no wrapper-level explanation or test |
| 9 | ~~Unknown YAML/JSON keys are silently dropped~~ **closed by [[D-086]]**: strict refusal, and the same finding in the report when lenient | serious | A spelling left behind by a field rename can make a production service boot with a default while its reviewed configuration appears to set a different value. |

## Edge DX constraints

The recommended start-up spelling is error-first: `cfg, err :=
vvcfg.Auto[Config](os.Args[1:]); if err != nil { log.Fatal(err) }`. `Must(Auto(...))`
remains available but must not lead the package documentation for operator input.
`Validator` remains the shipped optional interface; whether a `MustValidate()`
option is added is a separate declaration-time policy decision, not a competing
`Validate()` option vocabulary. Unknown YAML/JSON keys are likewise a decision
gap: strict refusal protects renamed fields, while a named compatibility mode may
be needed for shared files and rolling deploys; do not silently choose either
policy through the decoder. Generic nesting and the validation walk belong to
vvcfg once; Vvdb owns only its `Config.Validate` rules (including replica
semantics) and must not grow a second loader or environment policy. Today the
interface assertion is optional (`utils/vvcfg/vvcfg.go:64-68`) and the decoders
are non-strict (`cleanenv@v1.5.0/cleanenv.go:158-166`).
