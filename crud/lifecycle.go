package crud

import "context"

// ScopedRestore is the exact state an access-control policy approved before a
// tombstone is cleared. Storage keeps it in the same conditional UPDATE as the
// ids, repository scope and archived-row predicate.
type ScopedRestore[ID comparable] struct {
	IDs            []ID
	Scope          Predicate
	RelationScopes *RelationScopes
	Snapshots      map[ID]Predicate
}

// Restorer is the optional lifecycle capability implemented by a soft-deleting
// storage core and explicitly forwarded by decorators that preserve it.
// Restore is intentionally not Update: callers cannot clear a tombstone through
// a generic patch permission or DTO.
type Restorer[M any, ID comparable] interface {
	Restore(ctx context.Context, ids ...ID) (int64, error)
}

// ScopedRestorer is the storage half used by an access-control gate. It keeps
// policy inspection and statement execution atomic across bind-budget chunks.
type ScopedRestorer[M any, ID comparable] interface {
	RestoreScoped(ctx context.Context, restore *ScopedRestore[ID]) (int64, error)
}

// TombstoneLoader reads archived rows for policy inspection. It is an internal
// lifecycle seam rather than a widening query option: ordinary Get/GetAll can
// never be made to escape the live-row scope by request input.
type TombstoneLoader[M any, ID comparable] interface {
	LoadTombstones(ctx context.Context, ids []ID, scope Predicate, relations *RelationScopes) ([]M, error)
}

// RestoreSupport is the explicit probe for values whose concrete Go method set
// is wider than their configured lifecycle. The stock SQL core and typed Repo
// have one concrete type for hard- and soft-delete blueprints, so a Restorer
// type assertion alone cannot distinguish the two.
type RestoreSupport interface {
	SupportsRestore() bool
}

// SupportsRestore reports whether the exact layer presented to the caller
// preserves a real tombstone lifecycle. An explicit probe wins; custom cores
// that genuinely make Restore optional can rely on their method set.
func SupportsRestore[M any, ID comparable](core Core[M, ID]) bool {
	if core == nil {
		return false
	}
	if probe, ok := any(core).(RestoreSupport); ok {
		return probe.SupportsRestore()
	}
	_, ok := core.(Restorer[M, ID])
	return ok
}

// RestoreOf calls only the core it was handed. It never tunnels through an
// opaque decorator, because doing so could bypass authorisation, auditing or
// fault classification on a lifecycle write.
func RestoreOf[M any, ID comparable](core Core[M, ID], ctx context.Context, ids ...ID) (int64, error, bool) {
	restorer, ok := core.(Restorer[M, ID])
	if !ok {
		return 0, nil, false
	}
	n, err := restorer.Restore(ctx, ids...)
	return n, err, true
}

// RestoreScopedOf is RestoreOf's policy-narrowed storage counterpart.
func RestoreScopedOf[M any, ID comparable](core Core[M, ID], ctx context.Context, restore *ScopedRestore[ID]) (int64, error, bool) {
	if restore == nil {
		return 0, ErrBadRequest, true
	}
	restorer, ok := core.(ScopedRestorer[M, ID])
	if !ok {
		return 0, nil, false
	}
	n, err := restorer.RestoreScoped(ctx, restore)
	return n, err, true
}

// LoadTombstonesOf performs an exact, non-tunnelling capability call for the
// same reason RestoreOf does.
func LoadTombstonesOf[M any, ID comparable](core Core[M, ID], ctx context.Context, ids []ID, scope Predicate, relations *RelationScopes) ([]M, error, bool) {
	loader, ok := core.(TombstoneLoader[M, ID])
	if !ok {
		return nil, nil, false
	}
	rows, err := loader.LoadTombstones(ctx, ids, scope, relations)
	return rows, err, true
}

// Restore clears soft-delete state for ids. A plain hard-delete repository, or
// a decorator that does not explicitly preserve lifecycle writes, fails closed.
func (this *Repo[M, ID, U]) Restore(ctx context.Context, ids ...ID) (int64, error) {
	n, err, ok := RestoreOf(this.Core, ctx, ids...)
	if !ok {
		return 0, ErrNoTombstone
	}
	return n, err
}

// SupportsRestore preserves the optional lifecycle through the typed facade.
func (this *Repo[M, ID, U]) SupportsRestore() bool {
	return this != nil && SupportsRestore(this.Core)
}
