# vvcfg — a configuration struct, loaded and validated at start-up

```go
import "github.com/frostgrove/vv/utils/vvcfg"
```

```bash
go get github.com/frostgrove/vv/utils/vvcfg
```

**Module:** its own — cleanenv is a dependency and the root module takes none
([[D-033]], [[D-036]]) · **Requires:** `github.com/ilyakaznacheev/cleanenv`

The decoding is cleanenv's — YAML, TOML, JSON, `.env` and environment tags, all
of it. What this adds is what an application needs around it: **a stated
precedence** for finding the file, **an error return** rather than a panic,
**validation of every block** rather than the root alone, **a refusal for a key
no field declares**, and **a report saying where each value came from**
([[D-086]]).

---

## Using it

```go
type Config struct {
    Addr string      `yaml:"addr" env:"ADDR" env-default:":8080"`
    DB   vvdb.Config `yaml:"db"`
}

func (this *Config) ValidateSelf() error {
    if this.Addr == "" {
        return errors.New("addr is required")
    }
    return nil
}

func main() {
    cfg := vvcfg.MustLoad[Config]()
    …
}
```

Nothing forwards a `Validate` call into `cfg.DB`: the loader walks the tree.

| | |
|---|---|
| `Load[T](path)` | decode the file named, then validate the tree |
| `LoadStrict[T](path)` | the same, and refuse a key no field declares; answers a `*Report` |
| `LoadFrom[T](Source)` | the explicit form: resolution, strictness and rules on one value; answers a `*Report` |
| `MustLoad[T](path...)` | `DefaultSource()` and a panic; joins explicit path segments with `filepath.Join` |

`MustLoad` is the short way and `LoadFrom` is what it is short for.

## Validation is a tree, not a hook

```go
type SelfValidator interface{ ValidateSelf() error }
type CrossValidator interface{ ValidateCross() error }
type Validator     interface{ Validate() error }
```

`ValidateTree` — which every loader calls, and which is exported for a
configuration assembled in code — asks every node for `ValidateSelf` exactly
once, joins the failures so a file with two broken blocks reports both, and
prefixes each one with the path of the block it came from
(`db.replica: …`). Only then does it run every `ValidateCross`, so a rule
comparing two blocks may assume each block holds on its own.

**A node spelling the older `Validate` owns everything under it and the walk
stops there.** That method is as often a hand-written forwarder as a rule, and a
block that merges a fragment with its parent before checking it — `vvdb.Config`
and its replica — refuses that fragment when asked directly. Renaming
`Validate` to `ValidateSelf` is what opts a subtree into the walk.

Recursion is bounded by the address of every pointer and map already seen and by
depth, so a configuration that points at itself terminates.

A `*ValidationError` carries `Path` and the cause; `errors.Is` and `errors.As`
reach through the join.

## Strictness

```go
cfg, report, err := vvcfg.LoadStrict[Config]("/etc/app.yaml")
```

`adress:` sets nothing, and without a strict load the service starts on the
default. `LoadStrict` reads the file a second time as a document and refuses
what no field declares:

- `*UnknownKeysError` — the keys, each with its path (`billing.hsot`).
- `*UnusedEnvironmentError` — variables under a prefix the struct declares
  through `env-prefix` that no field reads. Only declared prefixes are
  candidates, and a prefix leading into a block that applies its own
  environment is excluded: that block owns names this walk cannot see.

A lenient `Load` reports the same findings in the report instead of refusing, so
a deployment can be cleaned up before strictness is turned on.

`.yaml`, `.yml`, `.json`, `.toml` and `.env` can be inspected this way. `.edn`
cannot: cleanenv decodes it and this package cannot read it back as a document.
A strict load of one is `ErrUnreadableFormat`. A lenient load still loads the
file, and says so instead of pretending: `Report.NotInspected` carries the
refusal, `Report.String` prints it as its own line, and every field is
`OriginUnknown` — not `OriginDefault`, which would claim nobody had written the
value down.

## The report

```go
cfg, report, err := vvcfg.LoadFrom[Config](vvcfg.DefaultSource())
if err != nil {
    log.Fatal(err)
}
log.Print(report)   // config: /etc/app.yaml (--config-path)
                    //   addr <- environment ADDR
                    //   db.host <- file
                    //   db.pool.max_open <- default
```

| | |
|---|---|
| `Report.Path`, `Report.PathOrigin` | which file booted this process and which source named it |
| `Report.NotInspected` | why the file could not be read as a document, when it could not; all origins are `OriginUnknown` then |
| `Report.Fields` | per field: `OriginFile`, `OriginEnvironment` (with the variable), `OriginDefault`, `OriginUnknown` |
| `Report.UnknownKeys`, `Report.UnusedEnvironment` | the strict findings, reported even when not refusing |
| `Report.Deprecated` | fields tagged `vvcfg:"deprecated=use addr"` that were actually set |
| `Report.OriginOf(path)` | one field's origin |

**The report never carries a value.** Not a redacted one: none. The line an
operator copies into an incident ticket is exactly the line a password would be
in, so where a value came from is all it says ([[D-086]]).

## The source

```go
source := vvcfg.Source{
    Arguments:          os.Args[1:],
    DefaultPath:        "./config/app.yml",
    AllowNoFile:        false,
    Strict:             true,
    RequireEnvironment: []string{"db.password"},
}
cfg, report, err := vvcfg.LoadFrom[Config](source)
```

| Field | |
|---|---|
| `Path` | an explicit file; wins over everything |
| `Arguments` | where `--config-path` is looked for; `DefaultSource` uses `os.Args[1:]` |
| `DefaultPath` | used when neither the flag nor `CONFIG_PATH` names a file |
| `AllowNoFile` | whether "no source named a file" is a legal deployment style or `ErrNoPath` |
| `Strict` | unknown keys and unused variables refuse the load |
| `RequireEnvironment` | paths that must arrive from the environment, so a password cannot be committed |

`Source.Resolve` answers the path and a `PathOrigin` — the caller, `--config-path`,
`CONFIG_PATH`, the default, or nothing — which is what a start-up line needs: a
flag is visible in the pod spec and a variable usually is not.

There is no package-level default path. Two configurations in one process
disagree by holding two `Source` values, and a test changes its own and nobody
else's.

## The precedence

```
Source.Path / MustLoad("config", "app.yml")   an explicit path
--config-path <path>                          the flag, read out of Source.Arguments
CONFIG_PATH=<path>                            the environment variable
Source.DefaultPath                            DefaultSource uses vvcfg.DefaultPath
AllowNoFile                                   environment-only configuration
```

Within the file, cleanenv layers the environment over what the file said, which
is why the report distinguishes the two.

## Sizes

An operator writes a limit as `25MiB`, not as `26214400`. `Bytes` is that field
type: an `int64` of bytes that decodes itself through `encoding.TextUnmarshaler`,
so the file, the environment and `env-default` all accept the same spelling.

```go
type Config struct {
    Upload vvcfg.Bytes `yaml:"upload" env:"UPLOAD_LIMIT" env-default:"1MiB"`
}
```

`KiB`, `MiB`, `GiB` are powers of two; `kB`, `MB`, `GB` are powers of ten; `B`
and a bare number are bytes. Case and surrounding space do not matter.
`ParseBytes` is the same parser as a value, and `Bytes.String` renders back into
the largest binary unit that divides evenly.

A refusal is `ErrNotASize` and **does not repeat the text it refused**: no error
this package constructs carries a configuration value, and a rule with one
exception is not a rule.

## See also

- [vvflag](vvflag.md) — how `--config-path` is read without owning the command line
- [vvdb](vvdb.md) — a database block that validates itself inside your config
