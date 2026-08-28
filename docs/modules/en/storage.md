# storage

`github.com/frostgrove/vv/storage` is the backend-neutral, streaming object
store used by both filesystem and MinIO adapters. The package has no external
dependency and starts no background work.

## What you get

- `Put`, `Open`, `Head` and idempotent `Delete` over opaque, validated `Key`s;
- explicit `CreateOnly` and `Replace` write modes, with `CreateOnly` as the
  zero-value default;
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
