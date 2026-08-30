# D-076 — A guard is idempotent only with itself

**Status:** accepted
**Invariant:** Consecutive mounts of one `*auth.Guard` authenticate once. A
different guard always performs its own authentication. Re-entering A after B
authenticated fails closed: auth has no assurance ordering from which it could
decide whether B is a step-up or a downgrade. Configuration that removes a
credential source or silently widens an API-key scheme fails where the guard or
authenticator is built. A published guard is an immutable, validated snapshot;
an option can neither retain it nor be applied to it later.

## The decision

A successful guard adds its identity and the exact principal-state node it
installed to an immutable marker chain in the request context. On a later call
it skips authentication only when that guard is the latest successful marker
and its principal-state node is still current. A marker from earlier in the
chain, or a principal replaced after the marker, returns an internal fault
wrapping `ErrAmbiguousGuardOrder`.

That makes both ambiguous directions safe. `ordinary -> step-up -> ordinary`
does not silently keep step-up on the assumption it is stronger;
`strict -> weak -> strict` does not silently keep weak under the same rule.
Both refuse before the handler. The supported cumulative composition is A -> B,
each mounted once; the handler sees B. Alternative credential kinds are one
guard over `auth.Chain`, not stacked guards. The marker chain is immutable
rather than a map because request contexts may be read by child goroutines.

There is no `Reauthenticate` option. Requiring an author to discover and opt
into the safe behaviour leaves every undiscovered composition vulnerable. A
different guard re-authenticates by default; accepting alternatives remains the
explicit `auth.Chain` composition.

`auth.Option` is an opaque declaration, not `func(*Guard)`. `NewGuard` applies
the declarations to a private `guardConfig`, then copies the completed values
into the guard it returns. Even an option retained inside this package can only
retain the discarded draft; it cannot mutate lookup, optionality or marker
identity after middleware publishes the guard. The ergonomic path remains
`NewGuard(authn, Optional(), Header(...))`; `Lookup` remains the explicit
low-level escape hatch for a source the declarations do not cover. A helper
that used to compose options by calling one with a `*Guard` returns the
underlying `Lookup`/`Header` declaration instead.

`NewGuard` also seals the value ready. `(*Guard).Validate` is the public
construction seam; every HTTP middleware and both gRPC interceptor constructors
call it. Nil and `new(auth.Guard)` therefore panic while the transport graph is
built. A consumer calling `Authenticate` directly receives an internal fault
wrapping `ErrGuardNotReady` rather than a nil-method panic on live traffic.

Credential-source APIs keep two distinct meanings:

- `auth.Header(name)` moves the existing Authorization parser. The named header
  still carries `Scheme token`.
- `apikey.Header(name)` reads a bare key and synthesises `ApiKey` as the internal
  credential scheme. It is the one-line path for `X-Api-Key: secret` and does not
  change the Authorization form `Authorization: ApiKey secret`.

The constructors reject declarations that cannot safely do what their names
say:

- `auth.NewGuard` rejects nil and interface-typed nil authenticators;
- `apikey.New` rejects nil and interface-typed nil stores;
- empty or whitespace-only `auth.Header` and `apikey.Header` names, a nil
  `auth.Lookup`, and empty or whitespace-only `apikey.Scheme` values panic while
  the graph is being built;
- `apikey.AnyScheme()` remains the only declaration that disables the scheme
  check. An empty setting is not an alias for it.

Nil at these seams means nil-like, not only an interface equal to nil. The one
`internal/nilvalue` predicate recognises typed-nil pointers, functions, maps,
slices, channels and nested interfaces. Guard construction, API-key store
construction and authentication, `WithPrincipal`/`PrincipalFrom`, and the
principal quantifiers all use it. A typed-nil principal is a refusal and never
an identity in a context.

`auth.Chain` snapshots its variadic input while it is built and removes every
nil-like authenticator. A member that reports success with a nil-like principal
is a refusal, and later alternatives still run. `apikey.TryStatic` takes a deep
snapshot of built-in `auth.Claims` slices and supported attribute containers and
materialises a fresh Claims value for every lookup. It refuses functions,
channels, unsafe pointers, unsafe map keys and structs with unexported state:
`bytes.Buffer` and `big.Int` cannot be shallow-copied soundly. `Static` is the
declarative fail-fast wrapper and panics on that construction error. Custom
Principal implementations have no enumeration surface from which to make a
copy, so `TryStatic` refuses them and `Static` fails fast. Their deliberately
caller-owned lifetime remains available through `Store`/`StoreFunc`. Literal
and typed-nil entries are refused at the same composition boundary rather than
becoming declared keys that can only answer unknown.

These are construction errors under [[D-021]], not request-time refusals. A
deployment typo must not look like a caller presenting a bad credential.

## Why

**Because a principal is not evidence of the check a route declared.** A global
optional or ordinary guard can establish a principal before an admin subtree.
If the subtree's guard treats that principal as its own work, it silently skips
the stricter issuer, audience, key store or step-up policy.

**Because pointer identity is the declaration identity the caller already
holds.** Keying on an authenticator would collapse two guards that intentionally
use the same verifier with different credential sources or optionality. Keying
on configuration fields would invent equality rules and make later options part
of a security protocol. The returned `*Guard` is already the unit mounted by
every transport.

**Because pointer identity does not define assurance order.** Remembering that
A ran says nothing about whether a later B is stronger or weaker. Returning the
current principal on A -> B -> A blesses both opposite interpretations with one
answer. Only adjacency is unambiguous without a declared order, so re-entry is
an internal configuration failure rather than a 401 blamed on the credential.

**Because an option is construction input, not a live control plane.** A
retained `func(*Guard)` could change an optional guard into a required one (or
replace its lookup) while requests were using it, race on its fields, and make
pointer identity describe changing security policy. Applying options to a
private draft keeps the declarative convenience without publishing a mutator.

**Because interface nil is not concrete nil.** A typed-nil Principal can satisfy
the interface assertion and then panic in `Subject`, `Has`, `In` or `Attr`.
Every boundary must answer the same absence decision; duplicating plain
`p == nil` checks recreates the bug at the next seam.

**Because a bare API-key header has no auth-scheme to parse.** Making
`auth.Header` sometimes parse and sometimes wrap a value would give the same API
two input languages. Keeping it scheme-shaped and putting the bare helper next
to the API-key authenticator leaves both call sites obvious.

**Because an empty scheme is not an explicit waiver.** `AnyScheme` exists to
make the credential-disclosure trade visible at the call site. Treating a blank
environment setting as that option defeats the reason it has a name.

## What it forbids

- Do not skip a guard merely because any principal is present.
- Do not key idempotence on the authenticator, scheme text or the last guard
  alone. It is the concrete guard instance, the latest marker and the principal
  state that marker installed.
- Do not add an opt-in reauthentication option as the primary fix. Safe
  composition is the default.
- Do not make `auth.Header` accept a bare value or make `apikey.Header` parse an
  Authorization scheme. The two APIs intentionally have different names and
  grammars.
- Do not let `Scheme("")` or `Scheme("   ")` mean `AnyScheme()`.
- Do not retain a nil-like extension dependency and wait for traffic to call it.
- Do not expose guard configuration through a callable `func(*Guard)` option or
  add a post-construction guard mutator.
- Do not let `(nil-like principal, nil)` terminate an authenticator chain as a
  success, and do not retain the caller's variadic chain slice.
- Do not describe `apikey.Static` as fixed if it shares built-in Claims maps or
  slices with either its declaration or another request.
- Do not shallow-copy an attribute type merely because reflection cannot set
  its unexported fields. Refuse it through `TryStatic`; `Static` fails fast.
- Do not retain a custom `Principal` in `Static`: its query-only interface
  cannot be snapshotted. Refuse it with `ErrUnsupportedStaticPrincipal`; custom
  identity lifetimes belong behind the explicit `Store`/`StoreFunc` seam.
- Do not accept a zero-value Guard in a transport constructor or defer its nil
  authenticator panic until a request.
- Do not assign implicit strength to A -> B -> A. Mount cumulative guards once,
  or use one `Chain` for alternatives.

## Where it lives

| File | What it holds |
|---|---|
| `auth/guard.go` | build-on-copy options, ready validation, latest-boundary idempotence and ambiguous re-entry refusal |
| `auth/guard_test.go`, `auth/guard_internal_test.go` | composition controls, invalid construction, retained-draft race proof |
| `auth/credential.go` | snapshotted authenticator chain and nil-like success handling |
| `auth/context.go`, `auth/principal.go` | fail-closed nil-like identity boundaries |
| `internal/nilvalue/nilvalue.go` | the single interface-aware nil predicate |
| `auth/apikey/apikey.go` | bare-header helper, `TryStatic`, sound/fallible per-request Claims snapshots |
| `auth/apikey/apikey_test.go` | end-to-end sources plus declaration/request mutation and race controls |
| `auth/http/authnet/middleware_test.go` | net/http composition proof |
| `auth/http/authgin/middleware_test.go` | Gin composition proof |
| `auth/http/authfiber/middleware_test.go` | Fiber composition proof |
| `auth/rpc/authgrpc/interceptor_test.go` | gRPC composition proof |

## Proven by

- `TestASecondGuardDoesNotAuthenticateAgain` and
  `TestADifferentGuardAuthenticatesAgain` preserve consecutive idempotence and
  A -> B composition. `TestAReenteredGuardAfterAnotherIdentityBoundaryFailsClosed`
  pins both `ordinary -> step-up -> ordinary` and its
  `strict -> weak -> strict` inverse without assigning either an order.
- `TestGuardValidateRejectsNilAndZeroValuesBeforeARequest` plus every
  transport's nil/zero construction cases pin the ready seam.
- `TestADoubleInstallAuthenticatesOnce` and
  `TestDifferentGuardsAuthenticateIndependently` in all three HTTP bindings,
  with the same two properties in gRPC vocabulary.
- `TestHeaderReadsABareKeyWithoutChangingTheAuthorizationAPI` in
  `auth/apikey/apikey_test.go`. Its control proves `apikey.Header` replaced the
  lookup, and its third arm proves the original `Authorization: ApiKey ...`
  route still works.
- `TestANilAuthenticatorRefusesToStart`, `TestAnEmptyCredentialSourceRefusesToStart`,
  `TestANilStoreRefusesToStart`, `TestAnEmptyBareHeaderRefusesToStart`, and the
  empty-scheme arms of `TestTheSchemeIsCheckedUnlessItIsWaived`.
- `TestNewGuardDoesNotPublishTheOptionDraft` mutates a retained construction
  draft concurrently with requests and proves the published guard is separate.
- `TestChainSnapshotsTheVariadicSlice` and the nil-like member/success arms of
  `TestChainAnswersTheFirstAuthenticatorThatSucceeds` prove a caller cannot
  rewrite a live chain and that an absent identity never wins it.
- `TestANilPrincipalIsNotStored` and the typed-nil quantifier/guard controls
  prove every neutral Principal boundary fails closed before invoking methods.
- `TestStaticSnapshotsClaimsAndReturnsAPerRequestCopy`,
  `TestStaticDoesNotRetainTheMutableClaimsDeclaration`, and
  `TestStaticClaimsAreIndependentAcrossConcurrentRequests` cover both sides of
  the Static snapshot under the race detector: retained declaration references
  and returned identities.
- `TestTryStaticRejectsMutableStateItCannotCopySoundly` covers `bytes.Buffer`,
  `big.Int` and a custom hidden-state struct; the exported-struct and cyclic
  controls prove supported values still arrive fresh per request.
  `TestTryStaticCopiesSupportedCyclesForEveryLookup` pins
  pointer-to-struct-to-pointer, pointer-to-interface-to-pointer and
  slice-to-interface-to-slice graphs across two independent lookups.
- `TestTryStaticRejectsCustomPrincipalItCannotSnapshot` and
  `TestStaticPanicsForCustomPrincipalItCannotSnapshot` keep a mutable custom
  implementation out of the safe fixed store; the `StoreFunc` control keeps
  the lower-level extension seam available.
- `TestTryStaticRejectsNilLikePrincipalsAtDeclaration` proves literal and typed
  nil entries fail before a store exists and never disclose their API key.
- gRPC stream tests mirror consecutive idempotence, distinct A -> B principals,
  and both ambiguous A -> B -> A assurance directions.

## See also

[[FL-019]] [[UC-019]] [[D-021]] [[D-055]] [[D-056]]
