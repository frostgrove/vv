# D-081 — Database secrets are values; typed server TLS is verified by default

**Status:** accepted
**Invariant:** An ordinary rendering of a database configuration never reveals
authentication material, and an omitted TLS mode on a typed server connection
never means plaintext or opportunistic encryption.

## Decision

`Config.Password` and `Config.DSN` are `Secret`, a string-backed value that can
still be converted explicitly at a connector boundary but renders as
`[REDACTED]` through value-rendering `fmt` verbs, JSON, YAML/TOML text
serialization and `slog`.
The standard library's `%p` still requires a pointer-like operand, and `%w` is
valid only in `fmt.Errorf` with an error operand.
`Params` redacts every
displayed value: its driver-specific vocabulary is open, so a key name cannot
prove that the value is public. `RedactedDSN` is the supported way to log the
resolved host/database target; it omits credentials, every query value and the
fragment. Third-party parser/driver text crosses a `RedactError` boundary,
preserving `errors.Is/As` identity without becoming log output.
If a raw DSN's structural credential/target grammar cannot be recognized
safely for the named engine, diagnostics return only `[REDACTED]` rather than
guessing a credential boundary. Driver-specific query values are omitted
wholesale from the diagnostic; their semantic validity remains the driver's
job when it opens the connection.

For field-based PostgreSQL, MySQL and MariaDB declarations, empty `sslmode`
means verified TLS: `verify-full` for PostgreSQL and `tls=true` for the MySQL
family. `sslmode: disable` is the explicit plaintext choice. `allow` and
`prefer` are explicit compatibility modes that permit fallback and are never
selected by omission. A raw `dsn:` is the whole low-level escape hatch and
therefore owns whatever TLS policy its author put into that string.

A Unix socket has no hostname to verify. Typed socket configurations therefore
require the explicit `sslmode: disable` waiver instead of silently turning a
verified default into either an unusable MySQL handshake or pgx plaintext.

## Why

Configuration structs are routinely printed at boot, embedded in a larger
application config, serialized into incident attachments and sent through
structured loggers. Protecting only vvdb's own error strings leaves all of
those ordinary paths open. The secret wrapper makes the safety property follow
the value rather than a particular logger.

Driver defaults are not a portable policy: PostgreSQL's historical `prefer`
allows plaintext fallback, while the MySQL driver uses plaintext when TLS is
unspecified. A framework default should express the safe intent once. Local
development remains one visible line and raw DSNs remain available for callers
who deliberately own lower-level behavior.

## What it forbids

- Do not change `Secret` display methods to reveal their value; use an explicit
  string conversion only at an integration boundary that needs the credential.
- Do not log `DSN`; use `RedactedDSN`.
- Do not let a new serializer bypass `Secret` or redacted `Params`.
- Do not restore driver-specific empty-TLS behavior. Plaintext or fallback
  requires an explicit mode, never an inference from a loopback hostname.
- Do not admit a driver parameter such as `allowFallbackToPlaintext` that
  weakens the typed TLS declaration through a second vocabulary.
- Do not parse and rewrite a raw DSN to impose framework policy; it is the
  documented whole-config escape hatch.

## Proven by

- `utils/vvdb/secret_test.go`
- `utils/vvcfg/vvcfg_test.go:TestVVDBSecretsLoadNormallyAndRenderRedacted`
- `test/dsn/dsn_test.go`

## See also

[[D-013]] [[D-021]] [[D-057]] [[FL-021]]
