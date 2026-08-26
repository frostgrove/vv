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
    cfg := vvcfg.Must(vvcfg.Auto[Config](os.Args[1:]))
    …
}
```

| | |
|---|---|
| `Find(args)` | the path: `--config-path` first, then `CONFIG_PATH` |
| `Load[T](path)` | decode, then call `Validate()` if the struct has one |
| `Auto[T](args)` | `Find` then `Load`, in the order a `main` wants them |
| `Must[T](cfg, err)` | panic on error, for a `main` that has nothing better to do |

`args` should not include the program name. `ErrNoPath` is what `Find` returns
when neither source names a file.

## The validation hook is the point

```go
type Validator interface{ Validate() error }
```

`Load` calls it after decoding and returns what it returns.

**A configuration that is wrong should stop the process at start-up**, not
surface as a confusing failure once traffic arrives ([[D-021]]). That is the same
rule `sqlrepo.Define` follows for a broken model mapping and `probe.Full` follows
for an unknown table.

## The precedence

```
--config-path <path>     the flag a deployment can override at the command line
CONFIG_PATH=<path>       the environment variable
                         …and nothing else. No search, no default location
```

There is no implicit `./config.yaml`, and that is deliberate: a config file found
by accident in a working directory is a deployment that behaves differently
depending on where it was started from.

## See also

- [vvflag](vvflag.md) — how `--config-path` is read without owning the command line
