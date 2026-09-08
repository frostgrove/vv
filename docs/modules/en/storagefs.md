# storagefs

`github.com/frostgrove/vv/storage/storagefs` implements `storage.Backend` in an
explicit local directory using only the standard library.

## Construct it

```go
backend, err := storagefs.New(&storagefs.Config{
    Root: "/srv/app-data",
})
if err != nil { return err }
defer backend.Close()

avatars, err := storage.New(&storage.Config{
    Namespace: "avatars",
    Backend:   backend,
})
```

`Root` must be absolute. The backend opens it with `os.OpenRoot`, so validated
operations cannot follow a nested symlink outside the owned tree and a later
working-directory change cannot redirect it. Safe defaults are `0600` for files
and `0700` for directories; `FileMode` and `DirMode` can override them without
granting group/world write. The configured root must be a real directory with
owner access and no group/world write. Treat it as an adapter-exclusive,
trusted tree: containment prevents escapes, but an untrusted process able to
rewrite entries inside the root could still replace an in-root object.
The adapter is supported on Unix-family targets and requires a local filesystem
with same-filesystem atomic hard-link and rename semantics. Construction returns
`storage.ErrUnsupported` on Windows, Plan 9, js/wasm and WASI; network filesystems
or bind mounts with weaker semantics are outside the advertised atomicity.

Objects use a private versioned file representation below the root. Do not
construct paths into it or serve those files directly: the format keeps bytes,
content type and metadata in one atomically placed file. Writes stream through a
same-root work file. `CreateOnly` uses atomic exclusive placement and `Replace`
uses atomic rename; a failed/cancelled source never appears as a partial final
object. Physical key segments, StageIDs and work names use lowercase base32 so
distinct logical identities remain distinct on case-folding Unix filesystems.
`Sync` flushes the completed file and the leaf destination directory
before success. It does not sync every newly created ancestor directory, so it
is not a blanket crash-durability guarantee for the first write into a new key
path. Pre-create the directory tree or add deployment-specific durability
measures if that distinction matters.

## Enable temporary HTTP links

Configure the public URL at which the backend's handler will be mounted and a
random secret of at least 32 bytes:

```go
backend, err := storagefs.New(&storagefs.Config{
    Root:       "/srv/app-data",
    BaseURL:    "https://files.example.com/download",
    SigningKey: signingKey,
    MaxLinkTTL: time.Hour,
})

downloadMux.Handle("/download", backend.Handler())
```

`Store.TemporaryURL` now returns a URL carrying a namespace/key/expiry HMAC.
The handler answers a single-byte-range request with `206` and a `Content-Range`,
and advertises `Accept-Ranges: bytes`; a multi-range request is answered whole,
at `200` and without a `Content-Range`, so no client is told it received
something it did not.
The handler validates the signature and expiry before opening the private
object and never exposes a physical path. It forces download disposition and
sends `nosniff`, sandbox CSP and no-referrer headers even though it preserves the
stored content type. Serve `downloadMux` from a dedicated cookie-less origin,
not the application's authenticated origin; the headers are defense in depth,
not an origin-isolation substitute. The handler rejects a request whose `Host`
does not match `BaseURL`; a reverse proxy must preserve or restore that canonical
host before dispatch. Treat the URL as a bearer credential.
Without `BaseURL` and `SigningKey`, ordinary storage still works and
`TemporaryURL` returns `storage.ErrUnsupported`.
`MaxLinkTTL` and per-call expiry are whole-second durations.

## Staged uploads and maintenance

`Stage` writes below a private staging area with its expiry embedded in the
stored header. `Promote` atomically places that complete file at the final key.
`Delete` is idempotent, and so is `Abort` against a stage with no live claim —
against one being promoted right now it answers `ErrConflict`, which is the
refusal that stops two operations from consuming one stage.

`Config.StageClaimTTL` (five minutes by default) is how long one `Promote` or
`Abort` holds a stage before another may take it over. It bounds an
*interruption*: without it a process killed mid-promote wedged the `StageID`
until the stage's own TTL, days later, with `Promote` and `Abort` both answering
`ErrConflict` in the meantime. A takeover happens only when the stage file is
still there *and* the claim records no completed placement — placement is a
rename that happens before the unlinks that retire the stage, so a crash just
after it leaves the same two names on disk as a crash just before it, and the
destination recorded in the claim is what tells them apart.

`Delete` also unlinks the now-empty directories the key created, up to the
namespace root, best-effort. Every key creates one directory per segment and
nothing else reclaims them: there is no `List`, and `CleanupExpired` walks only
staging, so a namespace churning `uploads/<uuid>/original.png` used to grow a
directory per upload for the life of the deployment. `CleanupExpired` scans only valid owned
stage names and removes at most the requested batch; unrelated/corrupt entries
are left for an operator rather than guessed at. It also removes canonically
named same-root work files left by a process crash only after
`storage.MaxStageTTL` (seven days); fresh or differently named files are not
touched. These work residues count toward `CleanupResult.Removed`.

The stage TTL starts when `Stage` begins, not when a long upload finishes. Set it
above the maximum accepted upload duration; an upload that outlives its TTL may
return an ID that is already expired. The backend starts no cleanup loop. Call
cleanup from the application's existing scheduler with a deadline-bearing
context. `CleanupOptions.Limit` bounds removals, not entries inspected.
Give write and maintenance operations deadlines shorter than
`storage.MaxStageTTL`; this keeps genuinely active work younger than the orphan
threshold and bounds a stuck filesystem operation.

## See also

- [storage](storage.md) — common operations, errors and UI upload lifecycle
- [storageminio](storageminio.md) — the same contract over MinIO

## What `Info` means here

`ETag` is derived from the object's size and the nanosecond stamp of the write
that produced it. It is what `IfMatch` compares, and it changes on every write.

`ModifiedAt` is the moment the object became **visible**, not the moment its
bytes were written. That matters for a promoted object: its header was written
when it was staged, which can be days earlier, and reporting that would have made
this backend disagree with `storageminio` about the same object. `Promote`
restamps the header immediately before the rename that publishes it.
