# vvflag — one typed flag, before `flag.Parse` owns everything

```go
import "github.com/shardit-io/vv/utils/vvflag"
```

**Module:** root · **Depends on:** the standard library

Read a single named flag out of a slice of arguments, into a typed value. That
is the whole package.

**Why it exists:** the standard `flag` package means declaring every flag up
front through a `FlagSet`, which a library cannot do on the application's behalf.
This reads one flag and nothing else, so a library can look for `--config-path`
without claiming ownership of the command line.

---

## Using it

```go
port, err := vvflag.Or(os.Args[1:], "port", 8080)
addr, _  := vvflag.Or(os.Args[1:], "addr", ":8080")
debug, _ := vvflag.Or(os.Args[1:], "debug", false)
```

| | |
|---|---|
| `Parse(args, name, def)` | absent returns the default **and `ErrAbsent`** |
| `Or(args, name, def)` | absent folds into the default; malformed is still an error |
| `Lookup(name, def)` | `Or` over `os.Args` |

`args` should not include the program name.

## The three forms

```
--name=value
--name value      (except for bool, where the flag alone means true)
--name            (bool only)
```

A `--` argument ends flag parsing, and everything after it is positional.

## The one rule worth knowing

**Absent and malformed are different answers.**

A flag that is not there returns the default and `ErrAbsent`. A flag that *is*
there and will not parse returns the default and a parse error.

Collapsing the two is how a typo'd `--port=abc` silently starts a server on port
0. `Or` folds the absent case away for the common call site — and still returns
the parse error, because a flag someone typed wrong is not a flag they left out.

## See also

- [vvcfg](vvcfg.md) — the config loader that uses this to find `--config-path`
