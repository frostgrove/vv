# storage

`github.com/frostgrove/vv/storage` is the backend-neutral, streaming object
store used by both filesystem and MinIO adapters. The package has no external
dependency and starts no background work.

## What you get

- `Put`, `Open`, `Head` and idempotent `Delete` over opaque, validated `Key`s;
- explicit `CreateOnly` and `Replace` write modes, with `CreateOnly` as the
  zero-value default, and an `IfMatch` precondition on top of either;
- ranged reads through `ReadOptions`;
- bounded portable content type and metadata;
- `Stage`, `Promote`, `Abort` and `CleanupExpired` for uploads made before a UI
  form is confirmed;
- one download-only `TemporaryURL` method for both filesystem and MinIO;
- portable error classes usable with `errors.Is`.

Callers own every input reader. `Put` and `Stage` never close it. The caller
must close every body returned by `Open`.

## Construct a scoped store

Choose and configure a backend, then scope it to one static logical namespace:

```go
files, err := storage.New(&storage.Config{
    Namespace: "avatars",
    Backend:   backend,
})
```

The namespace and keys are not filesystem paths, bucket names or URLs. Parse a
key once at the application boundary and persist its validated value if the
domain record needs it:

```go
key, err := storage.ParseKey("users/01J.../avatar.png")
info, err := files.Put(ctx, key, source, storage.PutOptions{
    ContentType: "image/png",
    Metadata: storage.Metadata{"classification": "avatar"},
})
```

The default write is create-only. Replacement must be requested explicitly:

```go
info, err = files.Put(ctx, key, source, storage.PutOptions{
    Mode: storage.Replace,
})
```

### Conditional writes

`CreateOnly` guards only the first write of a key and `Replace` guards nothing,
so two callers who read, decided and wrote lose one of the two decisions with no
error on either side. `IfMatch` makes a write conditional on the `ETag` the
caller last saw:

```go
current, err := files.Head(ctx, key)
// ... decide something from the current object ...
_, err = files.Put(ctx, key, source, storage.PutOptions{
    Mode:    storage.Replace,
    IfMatch: storage.IfMatch(current.ETag),
})
// errors.Is(err, storage.ErrPreconditionFailed) — somebody else wrote first
```

It is available on `PutOptions`, `PromoteOptions` and `DeleteOptions`. A backend
that cannot honour it refuses the option with `ErrUnsupported` rather than
writing unconditionally, and says which it is through
`Capabilities().ConditionalWrite`. Both shipped backends support it.

On MinIO the comparison is S3's own `If-Match` and is atomic with the write. On
the filesystem it is a read immediately before the rename that publishes the
object, so the window is narrow rather than zero — what it buys there is that a
caller who lost a race is *told*, instead of silently discarding the other write.

### Ranged reads

`Open` takes a `ReadOptions`. Without one it reads the whole object, which is
what the zero value means:

```go
tail := int64(1 << 20)
body, info, err := files.Open(ctx, key, storage.ReadOptions{Offset: info.Size - tail, Length: &tail})
```

A backend that cannot honour a range refuses a non-zero one with
`ErrUnsupported` rather than widening it to the whole object, and says so through
`Capabilities().RangeRead`.

`Info.MetadataTruncated` reports that the object carried metadata this library
will not hand back — an entry past the portable budget, an invalid key, a value
with control bytes. A bucket holds objects this library did not write, so those
are dropped and the answer says it is partial, rather than the read failing.

## Upload before the form is confirmed

The first request streams bytes into private staging and returns an opaque ID:

```go
staged, err := files.Stage(ctx, upload, storage.StageOptions{
    ContentType: "image/png",
    ExpiresIn:   time.Hour,
})

// Return staged.ID.Value() to the UI and submit it with the final form.
```

`StageID` is an authorization-sensitive bearer value, not proof that the
current user owns an upload. Persist or bind it server-side to the authenticated
actor/form and verify that binding before `Promote` or `Abort`. Storage does not
add tenant or authorization policy implicitly.

After validating and committing the domain form, parse that value and promote
the already uploaded bytes to their final key:

```go
stageID, err := storage.ParseStageID(form.UploadID)
info, err := files.Promote(ctx, stageID, finalKey, storage.PromoteOptions{})
```

Promotion also defaults to create-only. A final-key collision leaves the staged
upload available for retry or `Abort`. Run bounded cleanup from the
application's scheduler; storage starts no hidden goroutine:

```go
result, err := files.CleanupExpired(ctx, storage.CleanupOptions{Limit: 100})
```

`Limit` bounds successful removals, not directory entries inspected. Give
maintenance calls a cancellable context with an application-chosen deadline so
a large or slow backend scan also has a wall-clock bound.

## Temporary download links

Both adapters expose the same call:

```go
link, err := files.TemporaryURL(ctx, key, storage.TemporaryURLOptions{
    ExpiresIn: 10 * time.Minute,
})
response.DownloadURL = link.URL()
```

`Link` is a bearer capability. Its ordinary string representation is redacted;
call `URL()` only at the response boundary. Filesystem links require the
signer/HTTP handler described in [storagefs](storagefs.md). MinIO uses its native
pre-signed GET support. Temporary-link TTLs are whole-second durations from one
second through seven days so both signers report the same portable lifetime.

## Errors and intentional boundaries

```go
switch {
case errors.Is(err, storage.ErrNotFound):
case errors.Is(err, storage.ErrAlreadyExists):
case errors.Is(err, storage.ErrExpired):
case errors.Is(err, storage.ErrTemporary):
}
```

Error text contains the operation and bounded class, never a key, root, bucket,
endpoint, signed URL or raw provider error. The retained cause remains available
through controlled `errors.Is`/`errors.As` diagnostics.

There is deliberately no generic `List`, recursive delete, automatic retry,
OTel, audit or tenant routing here. Indexing, authorization and tenant selection
stay in the application; optional cross-cutting integrations can wrap `Store`.

## See also

- [storagefs](storagefs.md) — local filesystem backend and signed-link handler
- [storageminio](storageminio.md) — MinIO SDK adapter in its own Go module
- [storage roadmap](../../roadmaps/2026-08-26-1558-storage-roadmap.md) — contract rationale and deferred capabilities
