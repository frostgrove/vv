# FL-026 — A file becomes a validated configuration

**Entry points:** `utils/vvcfg/vvcfg.go:Load` / `:LoadStrict` / `:LoadFrom` /
`:MustLoad`
**Governed by:** [[D-086]] [[D-021]] [[D-057]]
**Serves:** the `utils` sweep in `docs/ai/usecases/modules/utils/Utils.md`
(H-UTILS-03, H-UTILS-05, H-UTILS-08, H-UTILS-14, H-UTILS-22), and [[UC-021]]
where the block being validated is a database

Everything on this path happens while the process is starting. It opens no
connection and starts no goroutine, so reaching a refusal costs nothing
([[D-057]]).

Line numbers age; the symbol names are what has to still exist.

## The path

1. **`Source.Resolve`** — `utils/vvcfg/source.go`
   An explicit `Path` wins; then `--config-path` through `vvflag.Or` over
   `Source.Arguments`; then `CONFIG_PATH`; then `Source.DefaultPath`. If nothing
   names a file, `AllowNoFile` decides between an environment-only load and
   `ErrNoPath`. The answer carries a `PathOrigin`, which is what lets a start-up
   line say the path came from a variable nobody can see in the pod spec.

2. **`decodeInto`** — `utils/vvcfg/vvcfg.go`
   `os.Stat`, then `cleanenv.ReadConfig` — the file first, the `env:` tags after
   it — or `cleanenv.ReadEnv` when there is no file. A decoder error is wrapped
   by `hideDecodeCause`: some parsers quote the value that failed, and an
   unterminated `.env` password is a real case ([[D-081]]). The cause stays
   reachable through `errors.Is`.

3. **`inspectFile` → `readDocument`** — `utils/vvcfg/document.go`
   The same file read a second time as a plain document — YAML, JSON, TOML, or
   the key list of a `.env`. This is what makes an unknown key visible at all.
   A format that cannot be read this way is `ErrUnreadableFormat`: fatal for a
   strict load, and for a lenient one it travels into the report as
   `Report.NotInspected` instead of being dropped.

4. **`applyEnvironment`** — `utils/vvcfg/vvcfg.go`
   The reflective walk that hands each `EnvironmentApplier` /
   `PrefixedEnvironmentApplier` its accumulated `env-prefix`. It is how
   `vvdb.Config` fills a replica the tags cannot describe.

5. **`describe`** — `utils/vvcfg/schema.go`
   The declared shape: one node per exported field, its path in the file's own
   tag vocabulary, its environment names with prefixes applied, its deprecation,
   and whether it is a leaf. Descent stops at a type that decodes itself, at a
   type already on the path (a self-referential `Replica` is the case), and at
   `maximumTreeDepth`. A subtree whose type applies its own environment is
   marked: its inherited prefix is excluded from the unused-variable scan,
   because it owns names this walk cannot see.

6. **`buildReport`** — `utils/vvcfg/report.go`
   Per field: environment if one of its variables is set, file if the document
   has the key, otherwise default. When step 3 could not read the document at
   all, no field can be placed and every one of them is `OriginUnknown`, with the
   reason in `Report.NotInspected`. Plus the unknown keys, the deprecations that
   were actually set, and the variables under a declared prefix that no field
   reads. `Report.String` renders it as a start-up block and never prints a
   value.

7. **`Source.refusals`** — `utils/vvcfg/vvcfg.go`
   Under `Strict`, unknown keys become `*UnknownKeysError` and unused variables
   `*UnusedEnvironmentError`. `RequireEnvironment` turns provenance into a rule:
   a path that did not come from the environment is `*EnvironmentSourceError`,
   and a path no field declares is `ErrUndeclaredPath`.

8. **`ValidateTree`** — `utils/vvcfg/validate.go`
   Every node's `ValidateSelf` (or its older `Validate`, which owns its subtree
   and stops the descent), joined; then every `ValidateCross`, but only over a
   tree that passed the first pass. Each failure is a `*ValidationError` carrying
   the block's path. Pointer and map addresses already seen bound the walk, so a
   configuration that points at itself terminates.

Everything the seventh and eighth steps produce is joined into one error, so a
file with a misspelled key *and* a broken block reports both.

## Where the decisions bite

- **The root is not the tree.** Before [[D-086]] only the root's `Validate` ran,
  and every nested block needed a hand-written forwarder.
- **`Validate` stops the walk, `ValidateSelf` does not.** A block that merges a
  fragment before checking it — a database replica — refuses that fragment when
  asked on its own. The older name is treated as owning its subtree for exactly
  that reason.
- **No package state.** The default path is a field on `Source`; two
  configurations in one process disagree without racing.
- **The report has no values.** Not redacted values: none. See [[D-086]].
- **An origin the loader did not establish is not reported as a default.** A
  lenient load of a format it cannot inspect knows the struct and not the file,
  so it says `unknown` and names the reason.

## Files

| File | What it holds |
|---|---|
| `utils/vvcfg/vvcfg.go` | `Load`, `LoadStrict`, `LoadFrom`, `MustLoad`, decoding, the refusal assembly |
| `utils/vvcfg/source.go` | `Source`, `DefaultSource`, `Resolve`, `PathOrigin` |
| `utils/vvcfg/validate.go` | `ValidateTree`, `SelfValidator`, `CrossValidator`, `ValidationError` |
| `utils/vvcfg/schema.go` | the declared shape, environment names, prefixes, deprecations |
| `utils/vvcfg/document.go` | the file as a document, unknown-key comparison |
| `utils/vvcfg/report.go` | `Report`, `Origin`, `Report.NotInspected`, `UnknownKeysError`, `UnusedEnvironmentError`, `EnvironmentSourceError` |
| `utils/vvcfg/bytes.go` | `Bytes` and `ParseBytes` — a size an operator writes as `25MiB`, decoded through `encoding.TextUnmarshaler` |
| `utils/vvflag/vvflag.go` | `Or`, which reads `--config-path` without owning the command line |

## Tests that walk this flow

- `utils/vvcfg/validate_test.go` — the tree, the cycle bound, the cross pass and
  the subtree-ownership rule with its control.
- `utils/vvcfg/report_test.go` — unknown keys, provenance, the value-free
  rendering, the unused-variable scan and its control, `RequireEnvironment`,
  deprecations, and the uninspectable format in both directions: refused by a
  strict load, admitted in the report by a lenient one.
- `utils/vvcfg/bytes_test.go` — the units, the refusal that does not echo the
  text it refused, and a size arriving from the file, the environment and the
  declared default.
- `utils/vvcfg/vvcfg_test.go` — precedence and its origins, the environment-only
  load, and two sources with different defaults loaded concurrently.
