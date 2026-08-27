# utils/vvgoose — application-owned migration CLI

**Covers:** `github.com/frostgrove/vv/utils/vvgoose`
**Sweep:** happy paths · edge cases · release readiness
**Verdict:** ready for the first tag; PostgreSQL/MySQL/MariaDB execution uses
Goose's provider and mapped drivers, while the repository's self-contained live
lifecycle proof is SQLite.

## What a consumer is trying to do

They want `cmd/migrate/main.go` to reuse the configuration the service already
loads. Creating a file must work while the database is offline and while the
model package is temporarily unfinished. Applying files must use the primary,
never the read replica. In a terminal, ambiguous source models should be easy to
search; in CI the same ambiguity must be deterministic and non-blocking.

## Shipped paths

- no arguments: command help;
- `migration NAME`: source scan, safe model selection, dialect SQL and an
  exclusive timestamped file;
- `migrate`, `status`, `rollback [count]`: isolated Goose Provider operations;
- `fresh`: tracked Down operations followed by Up operations;
- YAML and environment defaults through the database's migration block.

## Release boundary

This is a satellite module because the feature necessarily carries Goose, the
terminal UI and SQL drivers. It has no `replace`; it is published with the
directory-prefixed tag in the same release as the root module it requires.

