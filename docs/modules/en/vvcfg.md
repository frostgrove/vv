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
of it. What this adds is the three things an application actually needs around
it: **a stated precedence** for finding the file, **an error return** rather than
a panic, and **a validation hook** that runs before anything else starts.

---

## Using it

```go
type Config struct {
    Addr string `yaml:"addr" env:"ADDR" env-default:":8080"`
    DSN  string `yaml:"dsn"  env:"DSN"`
}

func (c *Config) Validate() error {
    if c.DSN == "" {
        return errors.New("dsn is required")
    }
    return nil
}

func main() {
    cfg := vvcfg.MustLoad[Config]()
    …
}
```

| | |
|---|---|
| `Load[T](path)` | decode, then call `Validate()` if the struct has one |
| `MustLoad[T](path...)` | load and panic on error; joins explicit path segments with `filepath.Join` |

Without an explicit path, `MustLoad` resolves the file itself.

## The validation hook is the point

```go
type Validator interface{ Validate() error }
```

`Load` calls it after decoding and returns what it returns.

File/environment decoder errors are a display-safe boundary: some parsers
quote the raw value that failed (an unterminated `.env` password is one real
case), so `Load` returns fixed text instead of echoing that diagnostic. The
original cause is still reachable through `errors.Is` and `errors.As`.
Errors from a typed `EnvironmentApplier` remain visible because they are
application-owned, actionable validation; implementations must name the field
or variable, never echo its value.

**A configuration that is wrong should stop the process at start-up**, not
surface as a confusing failure once traffic arrives ([[D-021]]). That is the same
rule `sqlrepo.Define` follows for a broken model mapping and `probe.Full` follows
for an unknown table.

## The precedence

```
MustLoad("config", "app.yml")  an explicit path, joined with filepath.Join
--config-path <path>            the flag a deployment can override at the command line
CONFIG_PATH=<path>              the environment variable
DefaultCfgPath                  `./config/app.yml` by default
DefaultCfgPath = ""             environment-only configuration
```

Set `vvcfg.DefaultCfgPath` before startup to use a different project default.

## See also

- [vvflag](vvflag.md) — how `--config-path` is read without owning the command line
