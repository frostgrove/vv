package auditcrud

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/audit/internal/auditcrudbridge"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/security"
)

type terminal[M any, ID comparable] struct {
	gate    crud.Core[M, ID]
	meta    *crud.Meta
	source  crud.Source
	restore bool
}

type receiver[M any, ID comparable] struct {
	next       crud.Core[M, ID]
	meta       *crud.Meta
	source     crud.Source
	recorder   *audit.Recorder
	policy     *audit.ResourcePolicy[M, ID]
	operations map[audit.EntityAction]audit.OperationMember
	restore    bool
}

type transactionCallbackError struct {
	cause error
}

func (*transactionCallbackError) Error() string {
	return "auditcrud: transaction callback failed"
}

type transactionOutcomeError struct {
	classification error
	cause          error
	reconcile      audit.ReconcileKey
	hasReconcile   bool
}

func (failure *transactionOutcomeError) Error() string {
	return failure.classification.Error()
}

func (failure *transactionOutcomeError) Is(target error) bool {
	return errors.Is(failure.classification, target) || errors.Is(failure.cause, target)
}

func (failure *transactionOutcomeError) As(target any) bool {
	return errors.As(failure.classification, target)
}

func (failure *transactionOutcomeError) AuditReconcileKey() (audit.ReconcileKey, bool) {
	if !failure.hasReconcile {
		return audit.ReconcileKey{}, false
	}
	key, err := audit.ParseReconcileKey(failure.reconcile.Bytes())
	return key, err == nil
}

func Secured[M any, ID comparable](recorder *audit.Recorder, resource *audit.ResourcePolicy[M, ID], policy security.Policy[M, ID]) crud.Middleware[M, ID] {
	if recorder == nil || resource == nil {
		panic("auditcrud: Recorder and ResourcePolicy are required")
	}
	gate := security.Gate(policy)
	return func(next crud.Core[M, ID]) crud.Core[M, ID] {
		if nilValue(next) {
			panic("auditcrud: inner CRUD core is required")
		}
		meta := next.Meta()
		if err := resource.CheckModel(meta); err != nil {
			panic(fmt.Errorf("auditcrud: resource metadata: %w", err))
		}
		source, ok := crud.SourceOf(next)
		if !ok || nilValue(source) {
			panic("auditcrud: inner CRUD core does not expose its source")
		}
		if _, ok := crud.BeginnerOf(source); !ok {
			panic("auditcrud: inner CRUD source cannot open a root transaction")
		}
		if err := recorder.CheckAtomicSource(source); err != nil {
			panic(fmt.Errorf("auditcrud: transaction source: %w", err))
		}
		operations := requireOperations(resource, meta)
		requireEffects[M, ID](next, meta.Tombstone != nil)
		probe, err := auditcrudbridge.NewSpec(resource, operations[audit.EntityCreated], nil, true)
		if err != nil {
			panic(fmt.Errorf("auditcrud: catalog probe: %w", err))
		}
		if _, err := auditcrudbridge.Preflight(context.Background(), recorder, probe); err != nil {
			panic(fmt.Errorf("auditcrud: catalog: %w", err))
		}
		receiver := &receiver[M, ID]{
			next: next, meta: meta, source: source, recorder: recorder,
			policy: resource, operations: operations, restore: meta.Tombstone != nil,
		}
		secured := gate(receiver)
		return &terminal[M, ID]{gate: secured, meta: meta, source: source, restore: receiver.restore}
	}
}

func requireOperations[M any, ID comparable](resource *audit.ResourcePolicy[M, ID], meta *crud.Meta) map[audit.EntityAction]audit.OperationMember {
	description := resource.Description()
	want := []audit.EntityAction{audit.EntityCreated, audit.EntityChanged}
	if meta.Tombstone == nil {
		want = append(want, audit.EntityHardDeleted)
	} else {
		want = append(want, audit.EntitySoftDeleted, audit.EntityRestored)
	}
	declared := make(map[audit.EntityAction]struct{}, len(description.Actions))
	for _, action := range description.Actions {
		declared[action] = struct{}{}
	}
	result := make(map[audit.EntityAction]audit.OperationMember, len(want))
	for _, action := range want {
		if _, ok := declared[action]; !ok {
			panic(fmt.Sprintf("auditcrud: ResourcePolicy does not declare %s", action))
		}
		result[action] = resource.Action(action)
	}
	return result
}

func requireEffects[M any, ID comparable](next crud.Core[M, ID], restore bool) {
	if _, ok := next.(crud.ScopedSaver[M, ID]); !ok {
		panic("auditcrud: inner CRUD core does not preserve ScopedSave")
	}
	if _, ok := next.(crud.ScopedDeleter[M, ID]); !ok {
		panic("auditcrud: inner CRUD core does not preserve ScopedDelete")
	}
	if _, ok := next.(crud.UnscopedExister[M, ID]); !ok {
		panic("auditcrud: inner CRUD core does not preserve ExistsUnscoped")
	}
	if !restore {
		return
	}
	if !crud.SupportsRestore(next) {
		panic("auditcrud: tombstone resource does not preserve Restore")
	}
	if _, ok := next.(crud.ScopedRestorer[M, ID]); !ok {
		panic("auditcrud: inner CRUD core does not preserve ScopedRestore")
	}
	if _, ok := next.(crud.TombstoneLoader[M, ID]); !ok {
		panic("auditcrud: inner CRUD core does not preserve LoadTombstones")
	}
}

func (t *terminal[M, ID]) MutationBoundarySealed() {}
func (t *terminal[M, ID]) Meta() *crud.Meta        { return t.meta }
func (t *terminal[M, ID]) Source() crud.Source     { return t.source }

func (t *terminal[M, ID]) GetByID(ctx context.Context, id ID, options ...crud.Option) (M, error) {
	return t.gate.GetByID(ctx, id, options...)
}

func (t *terminal[M, ID]) Get(ctx context.Context, options ...crud.Option) (crud.PaginatedResponse[M], error) {
	return t.gate.Get(ctx, options...)
}

func (t *terminal[M, ID]) GetAll(ctx context.Context, options ...crud.Option) ([]M, error) {
	return t.gate.GetAll(ctx, options...)
}

func (t *terminal[M, ID]) First(ctx context.Context, options ...crud.Option) (M, error) {
	return t.gate.First(ctx, options...)
}

func (t *terminal[M, ID]) Save(ctx context.Context, model *M) (M, error) {
	return t.gate.Save(ctx, model)
}

func (t *terminal[M, ID]) SaveOnly(context.Context, *M) error { return audit.ErrUnsupported }

func (t *terminal[M, ID]) Update(ctx context.Context, id ID, dto any, options ...crud.Option) (M, error) {
	return t.gate.Update(ctx, id, dto, options...)
}

func (t *terminal[M, ID]) UpdateAll(context.Context, any, ...crud.Option) (int64, error) {
	return 0, audit.ErrUnsupported
}

func (t *terminal[M, ID]) Aggregate(ctx context.Context, options ...crud.Option) ([]crud.AggregateRow, error) {
	return t.gate.Aggregate(ctx, options...)
}

func (t *terminal[M, ID]) SaveAll(context.Context, []*M) error { return audit.ErrUnsupported }

func (t *terminal[M, ID]) Delete(ctx context.Context, ids ...ID) (int64, error) {
	return t.gate.Delete(ctx, ids...)
}

func (t *terminal[M, ID]) DeleteAll(context.Context, ...crud.Option) (int64, error) {
	return 0, audit.ErrUnsupported
}

func (t *terminal[M, ID]) Count(ctx context.Context, options ...crud.Option) (int64, error) {
	return t.gate.Count(ctx, options...)
}

func (t *terminal[M, ID]) Exists(ctx context.Context, options ...crud.Option) (bool, error) {
	return t.gate.Exists(ctx, options...)
}

func (t *terminal[M, ID]) Tx(ctx context.Context, fn func(context.Context) error) error {
	return t.gate.Tx(ctx, fn)
}

func (t *terminal[M, ID]) SupportsRestore() bool { return t.restore }

func (t *terminal[M, ID]) Restore(ctx context.Context, ids ...ID) (int64, error) {
	if !t.restore {
		return 0, crud.ErrNoTombstone
	}
	count, err, ok := crud.RestoreOf(t.gate, ctx, ids...)
	if !ok {
		return 0, crud.ErrNoTombstone
	}
	return count, err
}

func (t *terminal[M, ID]) ExistsUnscoped(ctx context.Context, options ...crud.Option) (bool, error) {
	found, err, ok := crud.ExistsUnscopedOf(t.gate, ctx, options...)
	if !ok {
		return false, crud.ErrNoUnscopedExists
	}
	return found, err
}

func (r *receiver[M, ID]) Meta() *crud.Meta    { return r.meta }
func (r *receiver[M, ID]) Source() crud.Source { return r.source }

func (r *receiver[M, ID]) GetByID(ctx context.Context, id ID, options ...crud.Option) (M, error) {
	return r.next.GetByID(ctx, id, options...)
}

func (r *receiver[M, ID]) Get(ctx context.Context, options ...crud.Option) (crud.PaginatedResponse[M], error) {
	return r.next.Get(ctx, options...)
}

func (r *receiver[M, ID]) GetAll(ctx context.Context, options ...crud.Option) ([]M, error) {
	return r.next.GetAll(ctx, options...)
}

func (r *receiver[M, ID]) First(ctx context.Context, options ...crud.Option) (M, error) {
	return r.next.First(ctx, options...)
}

func (r *receiver[M, ID]) Aggregate(ctx context.Context, options ...crud.Option) ([]crud.AggregateRow, error) {
	return r.next.Aggregate(ctx, options...)
}

func (r *receiver[M, ID]) Count(ctx context.Context, options ...crud.Option) (int64, error) {
	return r.next.Count(ctx, options...)
}

func (r *receiver[M, ID]) Exists(ctx context.Context, options ...crud.Option) (bool, error) {
	return r.next.Exists(ctx, options...)
}

func (r *receiver[M, ID]) Tx(ctx context.Context, fn func(context.Context) error) error {
	return r.next.Tx(ctx, fn)
}

func (r *receiver[M, ID]) SaveOnly(context.Context, *M) error { return audit.ErrUnsupported }

func (r *receiver[M, ID]) UpdateAll(context.Context, any, ...crud.Option) (int64, error) {
	return 0, audit.ErrUnsupported
}

func (r *receiver[M, ID]) SaveAll(context.Context, []*M) error { return audit.ErrUnsupported }

func (r *receiver[M, ID]) DeleteAll(context.Context, ...crud.Option) (int64, error) {
	return 0, audit.ErrUnsupported
}

func (r *receiver[M, ID]) Save(ctx context.Context, model *M) (M, error) {
	var zero M
	if model == nil {
		return zero, crud.ErrBadRequest
	}
	guard, err := r.guard(ctx, audit.EntityCreated, nil, true)
	if err != nil {
		return zero, err
	}
	input := *model
	var stored M
	err = r.supervise(ctx, guard, func(mutationContext context.Context) ([]audit.EntityDraft, error) {
		result, saveErr := r.next.Save(mutationContext, &input)
		if saveErr != nil {
			return nil, saveErr
		}
		stored = result
		draft, draftErr := r.policy.Created(&stored)
		if draftErr != nil {
			return nil, draftErr
		}
		return []audit.EntityDraft{draft}, nil
	})
	if err != nil {
		return zero, err
	}
	return stored, nil
}

func (r *receiver[M, ID]) SaveScoped(ctx context.Context, model *M, save *crud.ScopedSave[M]) error {
	if model == nil || save == nil {
		return crud.ErrBadRequest
	}
	input := *model
	copySave := *save
	if save.Previous != nil {
		previous := *save.Previous
		copySave.Previous = &previous
	}
	action := audit.EntityCreated
	if copySave.Previous != nil {
		action = audit.EntityChanged
	}
	id, err := modelID[M, ID](r.meta, &input)
	if err != nil {
		return err
	}
	guard, err := r.guard(ctx, action, []ID{id}, false)
	if err != nil {
		return err
	}
	err = r.supervise(ctx, guard, func(mutationContext context.Context) ([]audit.EntityDraft, error) {
		if saveErr, ok := crud.SaveScopedOf(r.next, mutationContext, &input, &copySave); !ok {
			return nil, audit.ErrUnsupported
		} else if saveErr != nil {
			return nil, saveErr
		}
		if copySave.Previous == nil {
			draft, draftErr := r.policy.Created(&input)
			if draftErr != nil {
				return nil, draftErr
			}
			return []audit.EntityDraft{draft}, nil
		}
		draft, changed, draftErr := r.policy.BaselineChanged(copySave.Previous, &input)
		if draftErr != nil || !changed {
			return nil, draftErr
		}
		return []audit.EntityDraft{draft}, nil
	})
	if err == nil {
		*model = input
	}
	return err
}

func (r *receiver[M, ID]) Update(ctx context.Context, id ID, dto any, options ...crud.Option) (M, error) {
	var zero M
	resolved, err := crud.MutationOptions.Build(r.meta.Name, options...)
	if err != nil {
		return zero, err
	}
	guard, err := r.guard(ctx, audit.EntityChanged, []ID{id}, false)
	if err != nil {
		return zero, err
	}
	var stored M
	err = r.supervise(ctx, guard, func(mutationContext context.Context) ([]audit.EntityDraft, error) {
		before, loadErr := r.next.GetByID(mutationContext, id,
			crud.With(resolved), crud.SelectAll(), crud.PrimaryOnly(), crud.ForUpdate())
		if loadErr != nil {
			return nil, loadErr
		}
		after, updateErr := r.next.Update(mutationContext, id, dto, crud.With(resolved))
		if updateErr != nil {
			return nil, updateErr
		}
		stored = after
		draft, changed, draftErr := r.policy.BaselineChanged(&before, &after)
		if draftErr != nil || !changed {
			return nil, draftErr
		}
		return []audit.EntityDraft{draft}, nil
	})
	if err != nil {
		return zero, err
	}
	return stored, nil
}

func (r *receiver[M, ID]) Delete(ctx context.Context, ids ...ID) (int64, error) {
	return r.deleteScoped(ctx, &crud.ScopedDelete[ID]{IDs: ids})
}

func (r *receiver[M, ID]) DeleteScoped(ctx context.Context, deletion *crud.ScopedDelete[ID]) (int64, error) {
	return r.deleteScoped(ctx, deletion)
}

func (r *receiver[M, ID]) deleteScoped(ctx context.Context, deletion *crud.ScopedDelete[ID]) (int64, error) {
	if deletion == nil {
		return 0, crud.ErrBadRequest
	}
	request := cloneDelete(deletion)
	request.IDs = unique(request.IDs)
	if len(request.IDs) == 0 {
		return 0, nil
	}
	action := audit.EntityHardDeleted
	if r.restore {
		action = audit.EntitySoftDeleted
	}
	guard, err := r.guard(ctx, action, request.IDs, false)
	if err != nil {
		return 0, err
	}
	var count int64
	err = r.supervise(ctx, guard, func(mutationContext context.Context) ([]audit.EntityDraft, error) {
		victims, loadErr := r.loadLive(mutationContext, request.IDs, request.Scope, request.RelationScopes, request.Snapshots)
		if loadErr != nil || len(victims) == 0 {
			return nil, loadErr
		}
		victimIDs, snapshots, snapshotErr := r.snapshots(victims, request.Snapshots)
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		exact := &crud.ScopedDelete[ID]{IDs: victimIDs, Scope: request.Scope, RelationScopes: request.RelationScopes, Snapshots: snapshots}
		written, deleteErr, ok := crud.DeleteScopedOf(r.next, mutationContext, exact)
		if !ok {
			return nil, audit.ErrUnsupported
		}
		if deleteErr != nil {
			return nil, deleteErr
		}
		if written != int64(len(victims)) {
			return nil, audit.ErrConflict
		}
		count = written
		if r.restore {
			var supported bool
			var tombstones []M
			tombstones, loadErr, supported = crud.LoadTombstonesOf(r.next, mutationContext, victimIDs, request.Scope, request.RelationScopes)
			if !supported {
				return nil, audit.ErrUnsupported
			}
			if loadErr != nil {
				return nil, loadErr
			}
			if err := sameModels(r.meta, victimIDs, tombstones); err != nil {
				return nil, err
			}
		}
		return r.deletedDrafts(victims)
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (r *receiver[M, ID]) SupportsRestore() bool { return r.restore }

func (r *receiver[M, ID]) Restore(ctx context.Context, ids ...ID) (int64, error) {
	return r.restoreScoped(ctx, &crud.ScopedRestore[ID]{IDs: ids})
}

func (r *receiver[M, ID]) RestoreScoped(ctx context.Context, restore *crud.ScopedRestore[ID]) (int64, error) {
	return r.restoreScoped(ctx, restore)
}

func (r *receiver[M, ID]) restoreScoped(ctx context.Context, restore *crud.ScopedRestore[ID]) (int64, error) {
	if !r.restore {
		return 0, crud.ErrNoTombstone
	}
	if restore == nil {
		return 0, crud.ErrBadRequest
	}
	request := cloneRestore(restore)
	request.IDs = unique(request.IDs)
	if len(request.IDs) == 0 {
		return 0, nil
	}
	guard, err := r.guard(ctx, audit.EntityRestored, request.IDs, false)
	if err != nil {
		return 0, err
	}
	var count int64
	err = r.supervise(ctx, guard, func(mutationContext context.Context) ([]audit.EntityDraft, error) {
		tombstones, loadErr, ok := crud.LoadTombstonesOf(r.next, mutationContext, request.IDs, request.Scope, request.RelationScopes)
		if !ok {
			return nil, audit.ErrUnsupported
		}
		if loadErr != nil || len(tombstones) == 0 {
			return nil, loadErr
		}
		ids, snapshots, snapshotErr := r.snapshots(tombstones, request.Snapshots)
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		exact := &crud.ScopedRestore[ID]{IDs: ids, Scope: request.Scope, RelationScopes: request.RelationScopes, Snapshots: snapshots}
		written, restoreErr, supported := crud.RestoreScopedOf(r.next, mutationContext, exact)
		if !supported {
			return nil, audit.ErrUnsupported
		}
		if restoreErr != nil {
			return nil, restoreErr
		}
		if written != int64(len(tombstones)) {
			return nil, audit.ErrConflict
		}
		count = written
		live, liveErr := r.loadLive(mutationContext, ids, request.Scope, request.RelationScopes, nil)
		if liveErr != nil {
			return nil, liveErr
		}
		if err := sameModels(r.meta, ids, live); err != nil {
			return nil, err
		}
		return r.restoredDrafts(live)
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (r *receiver[M, ID]) LoadTombstones(ctx context.Context, ids []ID, scope crud.Predicate, relations *crud.RelationScopes) ([]M, error) {
	rows, err, ok := crud.LoadTombstonesOf(r.next, ctx, ids, scope, relations)
	if !ok {
		return nil, crud.ErrNoTombstone
	}
	return rows, err
}

func (r *receiver[M, ID]) ExistsUnscoped(ctx context.Context, options ...crud.Option) (bool, error) {
	found, err, ok := crud.ExistsUnscopedOf(r.next, ctx, options...)
	if !ok {
		return false, crud.ErrNoUnscopedExists
	}
	return found, err
}

func (r *receiver[M, ID]) guard(ctx context.Context, action audit.EntityAction, ids []ID, generated bool) (auditcrudbridge.Guard, error) {
	operation, ok := r.operations[action]
	if !ok {
		return auditcrudbridge.Guard{}, audit.ErrUnsupported
	}
	values := make([]any, 0, len(ids))
	for _, id := range unique(ids) {
		subject, err := r.policy.SubjectRef(id)
		if err != nil {
			return auditcrudbridge.Guard{}, err
		}
		values = append(values, subject)
	}
	spec, err := auditcrudbridge.NewSpec(r.policy, operation, values, generated)
	if err != nil {
		return auditcrudbridge.Guard{}, err
	}
	return auditcrudbridge.Preflight(ctx, r.recorder, spec)
}

func (r *receiver[M, ID]) supervise(ctx context.Context, guard auditcrudbridge.Guard, mutation func(context.Context) ([]audit.EntityDraft, error)) error {
	_, bound, err := crud.SourceBoundExecutorFor(ctx, r.source)
	if err != nil {
		return err
	}
	var reconcile audit.ReconcileKey
	hasReconcile := false
	run := func(transactionContext context.Context) error {
		runErr := guard.Run(transactionContext, func(mutationContext context.Context) (auditcrudbridge.Batch, error) {
			entities, mutationErr := mutation(mutationContext)
			if mutationErr != nil {
				return auditcrudbridge.Batch{}, mutationErr
			}
			values := make([]any, len(entities))
			for index, entity := range entities {
				values[index] = entity
			}
			return auditcrudbridge.NewBatch(values...)
		})
		if recovered, ok := guard.TakeRecovery(); ok {
			key, valid := recovered.(audit.ReconcileKey)
			if !valid {
				return audit.ErrAdmission
			}
			copied, parseErr := audit.ParseReconcileKey(key.Bytes())
			if parseErr != nil {
				return audit.ErrAdmission
			}
			reconcile = copied
			hasReconcile = true
		}
		return runErr
	}
	if bound {
		return run(ctx)
	}
	called := false
	var callbackFailure *transactionCallbackError
	txErr := crud.InAtomic(ctx, r.source, func(transactionContext context.Context) error {
		if called {
			callbackFailure = &transactionCallbackError{cause: audit.ErrTransaction}
			return callbackFailure
		}
		called = true
		if _, bound, bindErr := crud.SourceBoundExecutorFor(transactionContext, r.source); bindErr != nil || !bound {
			callbackFailure = &transactionCallbackError{cause: audit.ErrTransaction}
			return callbackFailure
		}
		if err := run(transactionContext); err != nil {
			callbackFailure = &transactionCallbackError{cause: err}
			return callbackFailure
		}
		return nil
	})
	if callbackFailure != nil {
		if txErr == callbackFailure {
			return callbackFailure.cause
		}
		return &transactionOutcomeError{classification: audit.ErrRollbackUnconfirmed, cause: callbackFailure.cause, reconcile: reconcile, hasReconcile: hasReconcile}
	}
	if !called {
		if txErr != nil {
			return txErr
		}
		return audit.ErrTransaction
	}
	if txErr != nil {
		return &transactionOutcomeError{classification: audit.ErrCommitUnconfirmed, reconcile: reconcile, hasReconcile: hasReconcile}
	}
	return nil
}

func (r *receiver[M, ID]) loadLive(ctx context.Context, ids []ID, scope crud.Predicate, relations *crud.RelationScopes, snapshots map[ID]crud.Predicate) ([]M, error) {
	predicates := make([]crud.Predicate, 0, len(snapshots))
	for _, id := range ids {
		if snapshot, ok := snapshots[id]; ok {
			predicates = append(predicates, snapshot)
		}
	}
	within := crud.And(scope, idPredicate(r.meta.PK.Name, ids))
	if snapshots != nil {
		within = crud.And(within, crud.Or(predicates...))
	}
	return r.next.GetAll(ctx, crud.Where(within), crud.NarrowRelations(relations), crud.SelectAll(), crud.PrimaryOnly(), crud.ForUpdate(), crud.Unpaged(), crud.Unsorted())
}

func (r *receiver[M, ID]) snapshots(models []M, original map[ID]crud.Predicate) ([]ID, map[ID]crud.Predicate, error) {
	ids := make([]ID, len(models))
	result := make(map[ID]crud.Predicate, len(models))
	for index := range models {
		id, err := modelID[M, ID](r.meta, &models[index])
		if err != nil {
			return nil, nil, err
		}
		snapshot, err := snapshotPredicate(r.meta, &models[index])
		if err != nil {
			return nil, nil, err
		}
		if admitted, ok := original[id]; ok {
			snapshot = crud.And(admitted, snapshot)
		}
		ids[index] = id
		result[id] = snapshot
	}
	return ids, result, nil
}

func (r *receiver[M, ID]) deletedDrafts(models []M) ([]audit.EntityDraft, error) {
	result := make([]audit.EntityDraft, len(models))
	for index := range models {
		draft, err := r.policy.Deleted(&models[index])
		if err != nil {
			return nil, err
		}
		result[index] = draft
	}
	return result, nil
}

func (r *receiver[M, ID]) restoredDrafts(models []M) ([]audit.EntityDraft, error) {
	result := make([]audit.EntityDraft, len(models))
	for index := range models {
		draft, err := r.policy.Restored(&models[index])
		if err != nil {
			return nil, err
		}
		result[index] = draft
	}
	return result, nil
}

func cloneDelete[ID comparable](input *crud.ScopedDelete[ID]) crud.ScopedDelete[ID] {
	result := *input
	result.IDs = append([]ID(nil), input.IDs...)
	result.Snapshots = clonePredicates(input.Snapshots)
	return result
}

func cloneRestore[ID comparable](input *crud.ScopedRestore[ID]) crud.ScopedRestore[ID] {
	result := *input
	result.IDs = append([]ID(nil), input.IDs...)
	result.Snapshots = clonePredicates(input.Snapshots)
	return result
}

func clonePredicates[ID comparable](input map[ID]crud.Predicate) map[ID]crud.Predicate {
	if input == nil {
		return nil
	}
	result := make(map[ID]crud.Predicate, len(input))
	for id, predicate := range input {
		result[id] = predicate
	}
	return result
}

func unique[ID comparable](input []ID) []ID {
	result := make([]ID, 0, len(input))
	seen := make(map[ID]struct{}, len(input))
	for _, id := range input {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func idPredicate[ID comparable](name string, ids []ID) crud.Predicate {
	if len(ids) == 1 {
		return crud.Eq(name, ids[0])
	}
	return crud.InAny(name, ids)
}

func modelID[M any, ID comparable](meta *crud.Meta, model *M) (ID, error) {
	var zero ID
	raw, err := meta.ID(model)
	if err != nil {
		return zero, err
	}
	value, ok := crud.ElemValue(raw).(ID)
	if !ok {
		return zero, &crud.SchemaError{Model: meta.Name, Field: meta.PK.Name, Reason: fmt.Sprintf("identifier has type %T, expected repository ID", crud.ElemValue(raw))}
	}
	return value, nil
}

func sameModels[M any, ID comparable](meta *crud.Meta, expected []ID, models []M) error {
	if len(expected) != len(models) {
		return audit.ErrConflict
	}
	want := make(map[ID]struct{}, len(expected))
	for _, id := range expected {
		want[id] = struct{}{}
	}
	for index := range models {
		id, err := modelID[M, ID](meta, &models[index])
		if err != nil {
			return err
		}
		if _, ok := want[id]; !ok {
			return audit.ErrConflict
		}
		delete(want, id)
	}
	if len(want) != 0 {
		return audit.ErrConflict
	}
	return nil
}

func snapshotPredicate[M any](meta *crud.Meta, model *M) (crud.Predicate, error) {
	values, err := meta.Values(model, meta.Fields)
	if err != nil {
		return nil, err
	}
	predicates := make([]crud.Predicate, 0, len(meta.Fields))
	for index, field := range meta.Fields {
		value, null, err := snapshotValue(values[index])
		if err != nil {
			return nil, fmt.Errorf("auditcrud: snapshot %s.%s: %w", meta.Name, field.Name, err)
		}
		if null {
			predicates = append(predicates, crud.IsNull(field.Name))
		} else {
			predicates = append(predicates, crud.Eq(field.Name, value))
		}
	}
	return crud.And(predicates...), nil
}

func snapshotValue(value any) (any, bool, error) {
	valuer, ok := value.(driver.Valuer)
	if !ok && value != nil {
		reflected := reflect.ValueOf(value)
		if reflected.Kind() != reflect.Pointer {
			copy := reflect.New(reflected.Type())
			copy.Elem().Set(reflected)
			valuer, ok = copy.Interface().(driver.Valuer)
		}
	}
	if !ok {
		if value == nil {
			return nil, true, nil
		}
		reflected := reflect.ValueOf(value)
		if nilable(reflected.Kind()) && reflected.IsNil() {
			return nil, true, nil
		}
		return value, false, nil
	}
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.Pointer && reflected.IsNil() {
		return nil, true, nil
	}
	resolved, err := valuer.Value()
	if err != nil {
		return nil, false, err
	}
	if resolved == nil {
		return nil, true, nil
	}
	reflected = reflect.ValueOf(resolved)
	if nilable(reflected.Kind()) && reflected.IsNil() {
		return nil, true, nil
	}
	if !driver.IsValue(resolved) {
		return nil, false, fmt.Errorf("driver.Valuer returned unsupported type %T", resolved)
	}
	return resolved, false, nil
}

func nilable(kind reflect.Kind) bool {
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	default:
		return false
	}
}

func nilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	return nilable(reflected.Kind()) && reflected.IsNil()
}

var (
	_ crud.Core[struct{}, int] = (*terminal[struct{}, int])(nil)
	_ crud.Core[struct{}, int] = (*receiver[struct{}, int])(nil)
	_ crud.Sourced             = (*terminal[struct{}, int])(nil)
	_ crud.Sourced             = (*receiver[struct{}, int])(nil)
)
