# D-069 — A shared stdlib-only tier sits under everything, and `crud` may import it

**Status:** accepted — supersedes the stdlib half of [[D-016]]
**Invariant:** `SHARED` is a tier of first-party packages that import the
standard library and nothing else. Package `crud` and every contract package may
import a `SHARED` package and nothing else of this repository. A package joins
`SHARED` only by being stdlib-only, and the check proves that rather than
trusting it.

## The decision

[[D-016]] said package `crud` may import "anything but the standard library" —
absolutely. That was written when `Opt[T]` lived in `crud`, so the rule and the
code agreed by construction.

`Opt` has since moved to `github.com/frostgrove/vv/utils`, deliberately: a
three-state value that distinguishes absent, null and set is a primitive of the
model and of the wire, not of CRUD. `crud/opt.go` keeps deprecated aliases and
`crud/access.go` reads an optional through `utils.Inspect`, so `crud` imports it
— and the rule as written called that a violation while the code and its own
deprecation notes called it the intended direction.

The rule is what changes. `SHARED` is the tier that resolves it:
`scripts/checks.sh:SHARED` names it, `TIER0` lets every contract package import
it, and `TIER0_STDLIB` includes both `crud` and every `SHARED` package — so the
"stdlib-only" property that makes the tier safe is checked on its members, not
assumed.

## Why

**Because the reason for D-016's rule survives and its wording does not.** What
the rule protects is a consumer's dependency graph: `go get` of the root module
must not pull a third-party library. A first-party package that imports only the
standard library adds nothing to that graph. The absolute wording was a cheap
way to guarantee it when there was nothing first-party to import.

**Because the alternative moves a primitive to the wrong place.** Putting `Opt`
back in `crud` would make `port`, `porthttp` and every generated update DTO
import the CRUD package for a type that has nothing to do with CRUD — and
`utils` may not alias it back, since [[D-058]] forbids `utils/` importing a
subsystem. The type would be named for the one consumer that happened to need it
first.

**Because the guarantee is now proven rather than assumed.** Before, "utils is
stdlib-only" was a fact somebody would have to keep true by remembering. Listing
`SHARED` in `TIER0_STDLIB` makes the first third-party import inside `utils`
fail `make check` instead.

## What it forbids

- Do not add a package to `SHARED` without adding it to `TIER0_STDLIB` in the
  same change. The tier is safe only because its members are checked.
- Do not let a `SHARED` package import `crud/`, `auth/`, `port/` or `remote/`.
  That is [[D-058]]'s boundary and `make check-utils` holds it for `utils/`.
- Do not read this as relaxing [[D-033]]. The root *module* still takes no
  third-party dependency; a package that needs one becomes a module.
- Do not put behaviour in `SHARED`. It is for primitives a model or a wire is
  made of. A package with a subsystem's logic in it belongs to that subsystem.

## Where it lives

| File | What it holds |
|---|---|
| `scripts/checks.sh` | `SHARED`, and the `TIER0` / `TIER0_STDLIB` arms that check it |
| `utils/optional.go` | `Opt`, `Optional`, `Inspect` — the tier's only member today |
| `crud/opt.go` | the deprecated aliases that keep `crud.Opt` working |

## Proven by

`make check-tiers`. Adding a third-party import to `utils` fails it, as does a
`crud` import of anything outside `SHARED`.
