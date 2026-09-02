# cache/cachememory

`cachememory` is the bounded in-process backend for `cache`. It is the default
storage kind for the `Hot` profile and implements both `cache.Backend` and
`cache.BatchReader` without adding a third-party dependency.

```go
backend, err := cachememory.New(cachememory.Limits{
	MaxEntries:   10_000,
	MaxBytes:     256 << 20,
	MaxItemBytes: 16 << 20,
})
```

All three limits are mandatory. An entry is charged by a public, versioned
model: `FixedEntryChargeBytes + len(value)`. `EntryCharge` lets deployment
validation use the same calculation. Values are copied on write and read, so a
caller cannot mutate stored state through a retained slice.

The backend keeps a strict LRU among live entries. A successful read promotes
an item; expired entries are removed before live capacity eviction. A put that
cannot fit is rejected before mutation. Batch reads deduplicate addresses,
respect item and total read limits and return owned byte slices. `Reset` clears
the store, `Close` clears it permanently, and `Stats` reports entries, charged
bytes, configured limits and closed state.

The backend answers the `HealthChecker` capability: `CheckBackend` passes while
the store is open and reports `cache.ErrClosed` afterwards, so `Cache.Check` has
something to consult. See [[D-093]] and [[D-096]].

`WithClock` supports deterministic expiry tests. `WithObserver` reports storage
operations, eviction reasons and charges. Callbacks run outside the backend
lock, panics are contained and re-entry is safe for this backend. Cancellation
is checked during bounded scans and eviction planning. A rejected or cancelled
put does not publish the requested value; expiry cleanup or LRU promotion that
already completed may still be observable when a later cancellation wins.

This is process-local disposable storage. It does not coordinate loaders and it
does not provide persistence, replication or a distributed lock; those belong
to the typed facade or another backend.
