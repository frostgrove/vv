# FL-033 — A request becomes a tenant-bound statement, and a job becomes one again

**Entry point:** `tenancy.Authority.Bind` on the request path, and `tenancyjobs.IdentityRestorer` at durable execution
**Implements:** [[UC-004]] [[UC-012]] · **Governed by:** [[D-115]] [[D-116]] [[D-117]] [[D-008]] [[D-061]]

Two halves. On the request path a verified identity becomes a scope and the scope
becomes a predicate. In a worker the same scope is built again from scratch,
because the record that carried the work is a reference and not an authority.

## The request half

1. **The host authenticates first.** Membership is verified before any of this
   runs; `tenancy` imports no JWT, router or transport package and never sees a
   carrier. What it is handed is a `context.Context` the host has already made
   trustworthy.

2. **`Authority.Verify`** — `tenancy/authority.go:Verify`
   Calls the injected `Resolver`, which returns a `Resolution`: a reference, a
   lifecycle state and an epoch. Plain data. It does not return a `Scope`, and
   that is the reason a `Scope` cannot be manufactured by application code
   ([[D-117]]).

3. **`Authority.mint`** — `tenancy/authority.go:mint`
   Refuses a malformed resolution, refuses a lifecycle the deployment's
   `Admission` does not admit *for this operation class*, and otherwise stamps the
   scope with `bind(salt, origin, resolution)` — an HMAC over a salt this
   authority drew at construction. `accept` recomputes and compares it in constant
   time on the way back in, so a zero value, a scope from a second authority and a
   scope from another deployment all fail before anything reads a tenant from it.

4. **`Authority.Bind` → `With`** — `tenancy/context.go:Bind`
   Carries the scope. A second bind for a *different* tenant inside work that is
   already bound answers `ErrPinned` rather than switching, because the unit of
   work below has already chosen its narrowing or its datasource.

5. **`tenancyrow.Policy` → `security.Gate`** — `tenancy/tenancyrow/row.go:Policy`
   The row topology is a package of its own, because it is the one seam here that
   reaches `crud/decorators/security` and through it `auth` and `errs`; a
   deployment that partitions a cache and nothing else compiles none of that.
   It contributes a `security.Policy` and returns a `crud.Middleware`; it
   owns no repository facade. `Scope` reads the carried scope and asks the
   `Ownership` strategy for a predicate; `Inspect` asks it to derive-or-validate
   ownership; `Immutable` is what the strategy freezes. Everything after this is
   [[FL-007]] and [[FL-008]] — the gate's own read and write paths, which already
   carry the whole `crud.Core` matrix, the unscoped-bulk refusals and the
   404-not-403 rule.

   `Column` narrows on the row's own field, and on each relation the call
   declares — a preload is a second statement against a second table and the
   root's `WHERE` does not reach it ([[D-007]]), so an undeclared relation on a
   tenanted parent is a cross-tenant read reachable from unprivileged input. A
   strategy that declares none supplies no relation-scope function at all, rather
   than one returning nothing behind a disabled guard — and it says so through
   `NarrowsRelations`, not by being probed, because a probe answered with an error
   would silently mean "no narrowing". `Through` narrows on a
   dotted path into the owner, which compiles to the correlated `EXISTS` of
   [[FL-005]], and freezes the key that points at the owner — repointing it is how
   a row leaves one tenant without any column of its own changing. Its create is
   refused: the owner it would attach to has not been read.

   `Tx` is the one `Core` verb the gate inherits, for [[D-030]]'s written reason.
   An unscoped `Tx` therefore opens, and the first verb inside it refuses.

6. **The statement.** The predicate is prepended, so a caller cannot widen it with
   any option the public surface offers ([[D-004]]).

## The durable half

7. **`contextProvider.Capture`** — `tenancy/tenancyjobs/jobs.go:Capture`
   At enqueue, for a definition whose partition mode is `jobs.PartitionTenantRequired`,
   the producer's scope becomes a `jobs.ProducerPartition` (the reference) and a
   `jobs.ProtectedIdentityToken`. The token itself is built by the core —
   `tenancy/seal.go:Seal`, the generation, a MAC and the reference — so the queue
   this adapter knows about contributes only the fields the MAC is bound to, and
   the key never leaves the authority. Those fields are the queue, the definition,
   the invocation identifier and the payload's wire digest, which is why
   `jobs/queue.go:preparePlacement` encodes the payload and mints the identifier
   before it calls this: a token bound only to the queue and the definition is a
   valid token for every record on that queue ([[D-119]]). That key is `Spec.DurableKey`, which the
   producer and the worker share and the per-process scope salt cannot be, because
   the worker is a different process started later. Without it `Authority.Sealer`
   refuses and the seam is never constructed: the queue table is writable by
   whatever else reaches that database, and an unauthenticated reference in a
   record is tenant impersonation. A scope the authority did not mint is refused
   at the seal rather than written into a record another process will believe.

8. **`identityRestorer.RestoreIdentity`** — `tenancy/tenancyjobs/jobs.go:RestoreIdentity`
   At execution, the MAC is verified — `tenancy/seal.go:Unseal` answers a record
   nobody with the key wrote with `ErrUntrusted` before the control plane is even
   asked, and it answers a reference and a generation rather than a scope — and
   then the token is
   *disbelieved* anyway: `Authority.Lookup`
   asks the control plane what is true now, the generation must still match, and
   the lifecycle must still admit durable work. Suspended, deleted and rotated
   tenants refuse here. `jobs.RestoreTrustedIdentity`
   (`jobs/durable_context.go:RestoreTrustedIdentity`) then checks that the
   partition the restorer returned re-digests to the one the record travelled
   with, so a token naming another tenant does not match its own record. The
   invocation identifier and wire digest the request carries come from
   `jobs/worker_delivery.go` — `Invocation.ID()` and `RestoredDelivery.WireDigest()`
   — and a record whose payload does not re-derive that digest is already refused
   by `jobs/delivery_record.go` before this runs.

## Object namespaces and cache partitions

9. **`tenancystorage.Namespace`** — `tenancy/tenancystorage/storage.go:Namespace`
   Resolves the scope for the class first — holding a scope says an authority
   minted it once, not that this authority admits this class now — and then
   maps it to a fixed-width digest namespace over a caller-chosen prefix. It
   is a digest and not the reference, because a bucket path is quoted in provider
   errors and access logs; it is fixed width, so no tenant's namespace can be a
   prefix of another's. The generation is inside the digest, so a restored tenant
   reads none of the previous generation's objects.

10. **`tenancycache.Partition`** — `tenancy/tenancycache/cache.go:Partition`
    A cache partitions on the key it is handed rather than on a context, so
    `tenancycache.Keyed(ctx, authority, class, key)` is where the check happens and
    the key cannot be built without one. The partition takes as much of the
    digest as the cache's remaining key budget allows — that budget is what is
    left after the key itself, so demanding a full one would refuse every
    operation on a deployment with long keys.

## Selecting a database instead of a predicate

11. **`tenancydb.Directory.Borrow`** — `tenancy/tenancydb/database.go:Borrow`
    Asks the authority for the scope *for this class*, rather than reading the
    carried one — under database-per-tenant there is no row policy below to catch
    a lifecycle that admits reads and not writes — and resolves it to one
    caller-owned `crud.Source`, keyed on the tenant *and* the generation, before
    any statement. There is no default and no last-used
    fallback: an unmapped tenant answers `ErrUnmapped`, a full directory answers
    `ErrCapacity`, and an application-supplied `Fence` is what proves the source
    really is this tenant's — the directory cannot know that, and the schema
    generation it used to compare against is a different quantity that moves for
    unrelated reasons. The slot is reserved **before** the source is opened, so a
    burst of borrowers cannot each open a pool against a budget that was true for
    all of them, and borrowers for one tenant share one open. Eviction unlinks
    immediately and closes only when the last borrower gives the lease back.

## Failure modes

| What goes wrong | Where it is caught | What the caller sees |
|---|---|---|
| no scope in the context | `Authority.Scope` | `ErrNoScope`, no statement |
| a scope this authority did not mint, including a zero value | `Authority.accept` | `ErrUntrusted`, no statement |
| a lifecycle the class does not admit | `Authority.mint` / `accept` | `ErrInactive`, no statement |
| a generation that moved, when `Spec.Revalidate` is set | `Authority.current` | `ErrStale`, no statement |
| a durable record nobody with the deployment's key wrote | `Sealer.Unseal` | the handler is not entered, and the control plane is not asked |
| an honest token reattached to another record of the same queue and job | `Sealer.Unseal` | the handler is not entered, and the control plane is not asked ([[D-119]]) |
| cross-tenant work outside the classes the grant named | `Authority.permittedByGrant` | `ErrGrantRequired` |
| the resolver cannot answer | `tenancy/errors.go:Classify` | `ErrUnavailable` — never the resolver's own text |
| a second tenant bound inside bound work | `Authority.With` | `ErrPinned` |
| a create into another tenant, or a mutation moving a row | `Ownership.Apply`, and the frozen column | 403 |
| a foreign row named by id | the gate ([[FL-008]]) | **404**, never 403 ([[D-008]]) |
| a durable record whose tenant is suspended, deleted or rotated | `identityRestorer.RestoreIdentity` | the handler is not entered |
| a durable token that names a different tenant than its partition | `jobs.RestoreTrustedIdentity` | the handler is not entered |
| an unmapped, over-budget or schema-incompatible database | `Directory.Borrow` | `ErrUnmapped` / `ErrCapacity` / `ErrIncompatible` |
| cross-tenant work with no grant | `Authority.Each` | `ErrGrantRequired` |
| a cohort member that became non-active mid-run | `Authority.member` | that member is recorded and skipped; the rest still run |

## Files

The core is what a deployment takes to have tenants at all: the standard library
and `crud`, whose error taxonomy decides the status code a refusal renders as.
Every seam it could adapt is a package beside it, so one seam never costs
another — `tenancyrow` alone reaches `security`, `auth` and `errs`.

| File | Role |
|---|---|
| `tenancy/scope.go` | `Scope`, `Resolution`, `bind`, `boundTo`, `Digest` — the unforgeable value |
| `tenancy/authority.go` | `Resolver`, `Spec`, `New`, `Verify`, `Lookup`, `mint`, `accept`, `Fixed` |
| `tenancy/lifecycle.go` | `Lifecycle`, `Class`, `Admission` — the whitelist, per operation class |
| `tenancy/context.go` | `Bind`, `With`, `Scope`, `From` — explicit propagation and the pin |
| `tenancy/errors.go` | the refusal vocabulary, and `Classify`, which is what stops a resolver's text travelling |
| `tenancy/outcome.go` | `Outcome`, `OutcomeFor` — the closed set a signal may carry |
| `tenancy/seal.go` | `Sealer`, `Seal`, `Unseal` — a scope becomes a record another process can check |
| `tenancy/grant.go` | `Purpose`, `Grant`, `Member`, `Accept`, `Each` |
| `tenancy/tenancyrow/row.go` | `Ownership`, `Column`, `Through`, `Policy`, `Repository` |
| `tenancy/tenancydb/database.go` | `Sources`, `DirectorySpec`, `Directory`, `Lease` |
| `tenancy/tenancyjobs/jobs.go` | `ContextProvider`, `IdentityRestorer`, and `record` — what the token is bound to |
| `jobs/queue.go` | `preparePlacement`, `capturePlacementContext` — the payload and identifier settled before the capture |
| `jobs/durable_context.go` | `ContextCaptureRequest`, `IdentityRestoreRequest` — the two carriers of the record's identity |
| `tenancy/tenancystorage/storage.go` | `Namespace`, `Store` |
| `tenancy/tenancycache/cache.go` | `Key`, `Keyed`, `Partition`, `Partitioned` |
| `crud/decorators/security/security.go` | the gate the row strategy returns — every verb, already obligated by [[D-030]] |
| `crud/executor.go` | `ExistsUnscopedOf`, exact-outer since [[D-115]] |

## Tests that walk this flow

- `TestOnlyTheAuthorityThatMintedAScopeAcceptsIt` — `tenancy/scope_test.go`.
- `TestOnlyAnAdmittedLifecycleReachesWork` — same file, every state × every class.
- `TestAResolverFailureNeverTravelsBackAsText` — same file, the redaction.
- `TestOnlyAScopeThisAuthorityMintedIsSealed` — `tenancy/seal_test.go`.
- `TestBindingFieldsCannotBeSlidPastOneAnother` — same file, what ties a record to its queue.
- `TestTheDigestSeparatesTenantsAndGenerations` — same file, the derivation two seams share.
- `TestNoVerbRunsWithoutAVerifiedScope` — `tenancy/tenancyrow/row_test.go`, fifteen verbs and zero statements.
- `TestEveryVerbNarrowsToTheScopesOwnTenantAndNoOther` — same file.
- `TestATenantSeesEveryRowItOwns` — same file, the control that the narrowing narrows to the right thing rather than to nothing.
- `TestARowOwnedThroughARelationIsNarrowedByThatRelation` — same file.
- `TestWorkEnqueuedBeforeASuspensionDoesNotRunAfterIt` — `tenancy/tenancyjobs/durable_test.go`.
- `TestAForgedDurableRecordEntersNoHandler` — same file, four records nobody with the key wrote.
- `TestAnHonestTokenReattachedToAnotherRecordEntersNoHandler` — same file, the replay a key alone does not stop ([[D-119]]).
- `TestADurableRecordCannotBeReplayedIntoAnotherJobOrQueue` — same file, including onto another record of the same job.
- `TestAnObjectNamespaceIsInjectiveAndNoTenantsIsAPrefixOfAnothers` — `tenancy/tenancystorage/storage_test.go`, 500 tenants.
- `TestARestoredGenerationReadsNoneOfThePreviousOnesObjects` — same file.
- `TestARestoredGenerationReadsNoneOfThePreviousOnesValues` — `tenancy/tenancycache/cache_test.go`.
- `TestOneTenantsCachedValueIsNotReadByAnother` — same file, through a real `cache.Cache` over `cachememory` rather than by comparing two partitions, so `Partitioned` itself is walked.
- `TestARestoredGenerationDoesNotReadThePreviousOnesCachedValue` — same file, the same through the cache.
- `TestEvictionWaitsForTheLastBorrower` — `tenancy/tenancydb/database_test.go`.
- `TestANewcomerClosesTheIdlestBindingRatherThanBeingRefused` — same file, `MaxCached` as a connection budget rather than a tenant ceiling.
- `TestAnOperationalRefusalRendersAsRetryableRatherThanAsABug` — `tenancy/vocabulary_test.go`, 503 rather than 500.
- `TestACallerGoingAwayIsNotAControlPlaneOutage` — same file, with the redaction kept as its control.
- `TestOneCohortMemberFailingLeavesTheRestResumable` — `tenancy/grant_test.go`.
- `TestNoTenancyPackageCostsMoreThanTheSeamItNames` — `scripts/tenancy_test.go`, the layout above measured rather than asserted.
- `TestATenantOwnedRepositoryIsolatesTwoTenantsOnOneTable` — `test/integration/tenancy_test.go`, live PostgreSQL.
