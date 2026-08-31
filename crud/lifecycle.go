package crud

import "context"

type ScopedRestore[ID comparable] struct {
	IDs            []ID
	Scope          Predicate
	RelationScopes *RelationScopes
	Snapshots      map[ID]Predicate
}

type Restorer[M any, ID comparable] interface {
	Restore(ctx context.Context, ids ...ID) (int64, error)
}

type ScopedRestorer[M any, ID comparable] interface {
	RestoreScoped(ctx context.Context, restore *ScopedRestore[ID]) (int64, error)
}

type TombstoneLoader[M any, ID comparable] interface {
	LoadTombstones(ctx context.Context, ids []ID, scope Predicate, relations *RelationScopes) ([]M, error)
}

type RestoreSupport interface {
	SupportsRestore() bool
}

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

func RestoreOf[M any, ID comparable](core Core[M, ID], ctx context.Context, ids ...ID) (int64, error, bool) {
	restorer, ok := core.(Restorer[M, ID])
	if !ok {
		return 0, nil, false
	}
	n, err := restorer.Restore(ctx, ids...)
	return n, err, true
}

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

func LoadTombstonesOf[M any, ID comparable](core Core[M, ID], ctx context.Context, ids []ID, scope Predicate, relations *RelationScopes) ([]M, error, bool) {
	loader, ok := core.(TombstoneLoader[M, ID])
	if !ok {
		return nil, nil, false
	}
	rows, err := loader.LoadTombstones(ctx, ids, scope, relations)
	return rows, err, true
}

func (this *Repo[M, ID, U]) Restore(ctx context.Context, ids ...ID) (int64, error) {
	n, err, ok := RestoreOf(this.Core, ctx, ids...)
	if !ok {
		return 0, ErrNoTombstone
	}
	return n, err
}

func (this *Repo[M, ID, U]) SupportsRestore() bool {
	return this != nil && SupportsRestore(this.Core)
}
