# storageminio

`github.com/frostgrove/vv/storage/storageminio` implements `storage.Backend`
with `minio-go/v7`. It is a separate Go module, so filesystem-only applications
do not acquire the SDK or its dependencies.

## Install and construct it

```bash
go get github.com/frostgrove/vv/storage/storageminio
```

Create the MinIO client in application bootstrap, including endpoint,
credentials, TLS, transport and retry policy, then inject it:

```go
backend, err := storageminio.New(&storageminio.Config{
    Client:     minioClient,
    Bucket:     "app-files",
    Prefix:     "production", // optional, validated and bounded
    MaxLinkTTL: time.Hour,
})

avatars, err := storage.New(&storage.Config{
    Namespace: "avatars",
    Backend:   backend,
})
```

The adapter does not read environment credentials, create the client, contact
the server during construction or own client shutdown. Logical keys never
contain the endpoint, bucket or physical prefix.

## Making the bucket exist

`Backend.EnsureBucket(ctx)` creates the configured bucket when it is missing and
is the only call in this package that contacts the server outside an object
operation. Every other method assumes the bucket is there and reports a missing
one as a failed write, which reaches a user as data they could not save rather
than as a deployment that is not finished — so call it once at start-up.

A bucket this creates is not the bucket an operator would have provisioned: it
has no versioning, retention or replication, and it is indistinguishable from
one that has. Decide deliberately whether production calls it.

A bucket that appears between the existence check and the create is success:
two replicas starting together both wanted it to exist, and it does.

It needs the real client, so it is available on a backend built by `New` and
returns `storage.ErrInternal` on one built from a test double.

## Write and promotion semantics

`CreateOnly` sends `If-None-Match: *`; an existing object becomes
`storage.ErrAlreadyExists` and a concurrent conditional conflict remains
`storage.ErrConflict`. It always uses one conditional PUT and accepts at most
`storageminio.MaxCreateOnlySize` (5 GiB). A larger declared-size `CreateOnly`
fails with `storage.ErrUnsupported` before the source is read; an unknown-size
input can only be rejected after its private stage reveals the size. This bound
avoids a `minio-go/v7` limitation: the SDK does not preserve custom conditional
headers when completing multipart uploads. `Replace` is unconditional only
when requested and may use the SDK's native multipart path.

An unknown-length `CreateOnly` input first streams to a random private stage,
which may use the SDK's native multipart path. The adapter immediately opens
that stage, obtains its exact size, and streams it into the final conditional
single PUT. When an exact size is declared, a checking reader detects both
early EOF and byte N+1 before successful visibility; size zero is probed before
the SDK call. The adapter never closes the caller's source and does not replay
an uncertain final write automatically.

Stages live under a private prefix with reserved marker/expiry metadata.
Promotion streams the stage into a conditional final put, then removes the
stage best-effort. This is intentionally named `Promote`, not atomic rename:
final PUT plus stage deletion cannot provide filesystem rename semantics. A
destination collision keeps the stage. A cleanup residue is removed after its
TTL by `CleanupExpired`.

A private conditional claim elects exactly one active `Promote`/`Abort`
operation for each StageID, including concurrent promotions to different final
keys. A loser gets `storage.ErrConflict`. Every state transition writes a fresh
random body token with an exact `If-Match` ETag. If a transition commits but its
response is lost, a stale SDK retry therefore cannot overwrite a successor
that already acquired the claim.

Deterministic failures leave a reusable `retired` claim while the stage still
exists. After the stage is confirmed absent, the adapter CAS-transitions the
claim to non-reusable `terminal` and then deletes it. Acquisition checks stage
existence immediately before and after its conditional write, and operations
also post-check the stage. Consequently, even a delayed create or retried
terminal delete cannot promote the consumed StageID. Terminal cleanup residues
are retried by `CleanupExpired`; claims do not accumulate per completed upload.
Do not manually delete active/retired claims or apply a lifecycle expiration
rule to the completed claim prefix, because that would bypass the fencing
protocol.

If a deterministic claim transition fails, its cleanup failure is surfaced
instead of falsely promising an immediate retry. An uncertain final result, or
a committed final object whose stage could not be removed, retains the active
generation until bounded expiry so the same stage cannot be placed at a second
key.

The claim expiry is bounded by `storage.MaxStageTTL` (seven days). Give
`Promote`, `Abort` and cleanup calls shorter context deadlines; the one-active-
operation guarantee is designed for operations that finish before that safety
expiry, not a request left running for days.

The server must strictly enforce conditional single PUT for missing objects,
concurrent writes and read-quorum failures. MinIO deployments should use a
build containing [conditional-write fix #21653](https://github.com/minio/minio/pull/21653)
(`18f97e7`, 2025-10-24), which follows missing-object fix
[#21550](https://github.com/minio/minio/pull/21550), or an equivalent build that
passes these conformance cases. The SDK version alone cannot provide the
server-side guarantee.

Configure the bucket's lifecycle policy to abort incomplete multipart uploads.
The SDK attempts to abort its owned multipart session after a source failure,
but cancellation/deadline paths can leave a server-side incomplete upload that
`CleanupExpired` cannot list because it is not a completed object.

## Reads, metadata and links

`Open` uses an immediate GET, so missing/forbidden errors are returned by the
call rather than being deferred to the first body read. The caller owns the
returned body; deferred read/close failures are mapped to portable, redacted
storage errors. Only bounded portable user metadata reaches `storage.Info`;
reserved staging metadata and raw SDK headers stay inside the adapter.

`TemporaryURL` uses MinIO's native pre-signed GET and the same TTL bounds as the
common store. TTLs are whole-second durations, matching SigV4 precision; the
reported expiry is conservative. The returned `storage.Link` is sensitive and
redacts ordinary formatting.

## Testing against a server

The default unit/wire suite uses an injected SDK seam and performs no network
access. A deployment claiming MinIO compatibility should additionally run the
adapter's integration scenarios against the exact server/version it operates,
especially concurrent conditional single PUT, multipart staging/replace and
pre-signed GET. Include wrong-ETag and read-quorum-failure conditional PUT
cases. Verify the incomplete-multipart lifecycle rule as part of deployment
checks; it must target incomplete uploads, not completed claim objects.

## storageminiofx — the fx wiring

```go
import "github.com/frostgrove/vv/storage/storageminio/storageminiofx"

fx.Options(storageminiofx.Module(storageminiofx.Settings{
    Endpoint:  cfg.Storage.Endpoint,
    AccessKey: cfg.Storage.AccessKey,
    SecretKey: cfg.Storage.SecretKey,
    Bucket:    cfg.Storage.Bucket,
}))
```

**Module** — it takes uber/fx, so a consumer who constructs the backend by hand
never resolves one ([[D-074]]).

| | |
|---|---|
| `Settings` | endpoint, credentials, region, `Transport`, bucket, prefix, link TTL, `Bucketing` |
| `Module(settings)` | provides `*minio.Client`, `*storageminio.Backend` and `storage.Backend`, and makes the bucket exist at start-up when asked to |
| `NewClient(settings)` / `NewBackend(settings, client)` | the same two constructors, without fx |

It provides a `storage.Backend` and not a `Store`: a Store is scoped to one
namespace, and a namespace belongs to whichever bounded context owns those
objects. Credentials, the bucket and the connection are infrastructure; what is
kept in them is not.

**Both settings that could be wrong in the dangerous direction are named modes
whose zero value is the production answer.** `Transport` is TLS unless it says
`TransportPlaintext`; the `UseSSL bool` it replaced sent credentials over plain
HTTP for any `Settings` whose author forgot the field. `Bucketing` creates
nothing unless it says `BucketOnDemand`; the `SkipEnsureBucket bool` it replaced
made writing to somebody's object store the default and the refusal the opt-out.
A value neither constant names is refused by `NewClient` rather than resolved to
one of the two, so a typo cannot choose.

Creating the bucket is still worth asking for. The alternative — carry on and
find out on the first upload — turns a deployment that is not finished into a
failed write an hour later with a worse error, and the check also proves the
endpoint and the credentials.

## See also

- [storage](storage.md) — common operations, errors and UI upload lifecycle
- [storagefs](storagefs.md) — stdlib filesystem backend and HMAC link handler
