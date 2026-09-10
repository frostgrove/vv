package audit

import (
	"bytes"
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/frostgrove/vv/crud"
)

type GroupSpec struct {
	Operation      *OperationType
	IdempotencyKey IdempotencyKey
}

type groupContextKey struct{}

type groupFrame struct {
	recorder       *recorder
	operation      *OperationType
	operationID    OperationID
	context        Context
	policy         ContextPolicy
	execution      Execution
	idempotency    IdempotencyKey
	hasIdempotency bool
	mu             sync.Mutex
	cond           *sync.Cond
	closed         bool
	reservations   uint32
	poisoned       error
	events         []draft
	entities       []entityDraft
}

func (r *Recorder) Within(ctx context.Context, spec GroupSpec, fn func(context.Context) error) (GroupResult, error) {
	return r.within(ctx, spec, fn, false)
}

func (r *Recorder) within(ctx context.Context, spec GroupSpec, fn func(context.Context) error, implicit bool) (GroupResult, error) {
	started := time.Now()
	if r == nil || r.value == nil || ctx == nil || spec.Operation == nil || spec.Operation.value == nil || fn == nil {
		return GroupResult{}, auditErrorAt(ErrInvalid, "group")
	}
	if current, _ := ctx.Value(groupContextKey{}).(*groupFrame); current != nil {
		return r.joinGroup(ctx, current, spec, fn)
	}
	if !implicit && !r.value.catalogContains(spec.Operation) {
		return GroupResult{}, auditErrorAt(ErrWrongCatalog, "operation")
	}
	if spec.IdempotencyKey != "" && !validOpaqueReference(string(spec.IdempotencyKey), MaxIdempotencyKeyBytes) {
		return GroupResult{}, auditErrorAt(ErrInvalid, "idempotency")
	}
	if err := r.value.checkStore(); err != nil {
		return GroupResult{}, err
	}
	description := spec.Operation.value.seal.description
	policy := contextPolicyFromDescription(description.Context)
	resolved, operationID, generated, err := r.value.resolve(ctx, policy)
	if err != nil {
		r.value.observe(started, ObservationGroup, ObservationResolve, failureClassOf(err), 0, 0, 0, 0)
		return GroupResult{}, err
	}
	if spec.IdempotencyKey != "" && generated {
		return GroupResult{}, auditErrorAt(ErrInvalid, "idempotency.operation")
	}
	var execution Execution
	executor, found, err := crud.SourceBoundExecutorFor(ctx, r.value.writer.TransactionSource())
	if err != nil {
		return GroupResult{}, auditError(ErrTransaction, err)
	}
	if found {
		execution, err = r.value.writer.BindTransaction(executor)
		if err != nil || nilByReflection(execution) || !execution.Authority().Valid() {
			return GroupResult{}, auditError(ErrTransaction, err)
		}
	}
	frame := &groupFrame{
		recorder: r.value, operation: spec.Operation, operationID: operationID,
		context: resolved, policy: policy, execution: execution,
		idempotency: spec.IdempotencyKey, hasIdempotency: spec.IdempotencyKey != "",
	}
	frame.cond = sync.NewCond(&frame.mu)
	groupContext := context.WithValue(ctx, groupContextKey{}, frame)
	if err := runGroupOwner(frame, groupContext, fn); err != nil {
		frame.close()
		return GroupResult{}, err
	}
	events, entities, poison, err := frame.seal()
	if err != nil {
		return GroupResult{}, err
	}
	if poison != nil {
		return GroupResult{}, poison
	}
	if len(events) == 0 && len(entities) == 0 {
		return GroupResult{}, nil
	}
	runtimeContext := valueFreeContext{Context: ctx}
	request, reconcile, err := r.value.prepareGroup(runtimeContext, frame, events, entities)
	if err != nil {
		r.value.observe(started, ObservationGroup, ObservationPrepare, failureClassOf(err), 0, uint16(len(events)+len(entities)), 0, 0)
		return GroupResult{}, err
	}
	var appendResult AppendResult
	settlement := Committed
	if execution != nil {
		appendResult, err = execution.Append(runtimeContext, request)
		settlement = InCallerTransaction
	} else {
		appendResult, err = r.value.writer.Append(runtimeContext, request)
	}
	if err != nil {
		mapped := mapAppendError(err)
		r.value.observe(started, ObservationGroup, ObservationStore, failureClassOf(mapped), 1, uint16(len(events)+len(entities)), 0, settlement)
		if execution != nil {
			return GroupResult{value: groupResult{reconcile: reconcile}}, mapped
		}
		token := retryFor(request, reconcile, mapped)
		result := GroupResult{value: groupResult{retry: token, reconcile: reconcile}}
		if token.value.reconcile.valid() {
			return result, &retryCarrier{token: token, err: mapped}
		}
		return result, mapped
	}
	if err := validateAppendResult(request, appendResult); err != nil {
		r.value.observe(started, ObservationGroup, ObservationVerify, UnreadableFailure, 1, uint16(len(events)+len(entities)), appendResult.Disposition(), settlement)
		return GroupResult{value: groupResult{reconcile: reconcile}}, auditErrorAt(ErrIntegrity, "append_result")
	}
	if execution != nil && !SameAuthority(appendResult.Authority(), execution.Authority()) {
		r.value.observe(started, ObservationGroup, ObservationVerify, UnreadableFailure, 1, uint16(len(events)+len(entities)), appendResult.Disposition(), settlement)
		return GroupResult{value: groupResult{reconcile: reconcile}}, auditErrorAt(ErrWrongAuthority, "append")
	}
	actualReconcile, err := newReconcileKey(r.value.writer.BackingID(), appendResult.Stored().Revision())
	if err != nil {
		return GroupResult{value: groupResult{reconcile: reconcile}}, err
	}
	receipt := receiptFromAppend(appendResult, settlement, actualReconcile)
	r.value.observe(started, ObservationGroup, ObservationStore, NoFailure, 1, uint16(len(events)+len(entities)), appendResult.Disposition(), settlement)
	return GroupResult{value: groupResult{receipt: receipt, reconcile: actualReconcile}}, nil
}

func runGroupOwner(frame *groupFrame, ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			frame.close()
			panic(value)
		}
	}()
	return fn(ctx)
}

func (r *Recorder) joinGroup(ctx context.Context, frame *groupFrame, spec GroupSpec, fn func(context.Context) error) (GroupResult, error) {
	if frame.recorder != r.value || frame.operation != spec.Operation || frame.idempotency != spec.IdempotencyKey {
		return GroupResult{}, auditErrorAt(ErrTransaction, "nested_group")
	}
	if err := frame.reserve(); err != nil {
		return GroupResult{}, err
	}
	var callbackErr error
	defer func() {
		if value := recover(); value != nil {
			frame.release(ErrGroupPoisoned)
			panic(value)
		}
		frame.release(callbackErr)
	}()
	callbackErr = fn(ctx)
	if callbackErr != nil {
		return GroupResult{}, callbackErr
	}
	return GroupResult{value: groupResult{joined: true}}, nil
}

func (f *groupFrame) reserve() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrGroupClosed
	}
	if f.poisoned != nil {
		return ErrGroupPoisoned
	}
	if f.reservations >= MaxItems {
		return auditTooLarge("group.reservations", MaxItems)
	}
	f.reservations++
	return nil
}

func (f *groupFrame) release(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reservations > 0 {
		f.reservations--
	}
	if err != nil && f.poisoned == nil {
		f.poisoned = err
	}
	if f.reservations == 0 {
		f.cond.Broadcast()
	}
}

func (r *Recorder) Stage(ctx context.Context, value Draft) error {
	if r == nil || r.value == nil || ctx == nil || value.value.declaration == nil {
		return auditErrorAt(ErrInvalid, "stage")
	}
	frame, _ := ctx.Value(groupContextKey{}).(*groupFrame)
	if frame == nil || frame.recorder != r.value {
		return auditErrorAt(ErrTransaction, "group")
	}
	if !operationAccepts(frame.operation, value.value.declaration, value.value.descriptor.Resource, value.value.descriptor.Action) {
		return auditErrorAt(ErrInvalid, "operation.member")
	}
	return frame.addEvent(value.value)
}

func (r *Recorder) Capture(ctx context.Context, value Draft) (CaptureResult, error) {
	if r == nil || r.value == nil || ctx == nil || value.value.declaration == nil {
		return CaptureResult{}, auditErrorAt(ErrInvalid, "capture")
	}
	if frame, _ := ctx.Value(groupContextKey{}).(*groupFrame); frame != nil {
		if err := r.Stage(ctx, value); err != nil {
			return CaptureResult{}, err
		}
		return CaptureResult{value: captureResult{staged: true}}, nil
	}
	result, err := r.Record(ctx, value)
	if err != nil {
		return CaptureResult{value: captureResult{retry: result.value.retry}}, err
	}
	receipt, _ := result.Receipt()
	return CaptureResult{value: captureResult{receipt: receipt}}, nil
}

func (f *groupFrame) addEvent(value draft) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrGroupClosed
	}
	if f.poisoned != nil {
		return ErrGroupPoisoned
	}
	if len(f.events)+len(f.entities) >= MaxItems {
		return auditTooLarge("items", MaxItems)
	}
	f.events = append(f.events, cloneDraft(value))
	return nil
}

func (f *groupFrame) addEntities(values []EntityDraft) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrGroupClosed
	}
	if f.poisoned != nil {
		return ErrGroupPoisoned
	}
	if f.execution == nil {
		return auditErrorAt(ErrTransaction, "entity_group")
	}
	if len(f.events)+len(f.entities)+len(values) > MaxItems {
		return auditTooLarge("items", MaxItems)
	}
	for _, value := range values {
		if value.value.declaration == nil || !operationAccepts(f.operation, value.value.declaration, value.value.descriptor.Resource, Action(value.value.action)) {
			return auditErrorAt(ErrInvalid, "operation.member")
		}
		f.entities = append(f.entities, cloneEntityDraft(value.value))
	}
	return nil
}

func (f *groupFrame) poison(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.poisoned == nil {
		f.poisoned = err
	}
}

func (f *groupFrame) close() {
	f.mu.Lock()
	f.closed = true
	f.events = nil
	f.entities = nil
	for f.reservations != 0 {
		f.cond.Wait()
	}
	f.mu.Unlock()
}

func (f *groupFrame) seal() ([]draft, []entityDraft, error, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, nil, nil, ErrGroupClosed
	}
	f.closed = true
	if f.reservations != 0 {
		if f.poisoned == nil {
			f.poisoned = ErrGroupPoisoned
		}
		f.events = nil
		f.entities = nil
		for f.reservations != 0 {
			f.cond.Wait()
		}
		return nil, nil, f.poisoned, nil
	}
	events := make([]draft, len(f.events))
	for index, value := range f.events {
		events[index] = cloneDraft(value)
	}
	entities := make([]entityDraft, len(f.entities))
	for index, value := range f.entities {
		entities[index] = cloneEntityDraft(value)
	}
	return events, entities, f.poisoned, nil
}

func cloneDraft(value draft) draft {
	values := value.values
	value.values = make([]draftValue, len(values))
	for index, field := range values {
		value.values[index] = cloneDraftValue(field)
	}
	return value
}

func cloneEntityDraft(value entityDraft) entityDraft {
	values := value.values
	value.values = make([]draftValue, len(values))
	for index, field := range values {
		value.values[index] = cloneDraftValue(field)
	}
	changes := value.changes
	value.changes = make([]draftChange, len(changes))
	for index, change := range changes {
		value.changes[index] = draftChange{field: change.field, before: cloneDraftValue(change.before), after: cloneDraftValue(change.after)}
	}
	return value
}

func operationAccepts(operation *OperationType, declaration Declaration, resource Resource, action Action) bool {
	if operation == nil || operation.value == nil {
		return false
	}
	key := declarationMemberKey(resource, action)
	for _, member := range operation.value.members {
		if member.key == key && member.declaration == declaration {
			return true
		}
	}
	return false
}

func (r *recorder) prepareGroup(ctx context.Context, frame *groupFrame, events []draft, entities []entityDraft) (AppendRequest, ReconcileKey, error) {
	if err := r.checkStore(); err != nil {
		return AppendRequest{}, ReconcileKey{}, err
	}
	revisionID, err := r.ids.NewRevisionID()
	if err != nil || revisionID == (RevisionID{}) {
		return AppendRequest{}, ReconcileKey{}, auditError(ErrBackend, err)
	}
	description := frame.operation.value.seal.description
	observed := canonicalTime(r.clock.Now())
	header := RevisionHeaderView{
		Format: revisionFormatV1, Log: r.writer.LogID(), Catalog: r.active, CatalogSet: r.set,
		Deployment: r.deployment, Operation: description.Operation, OperationID: frame.operationID,
		RevisionID: revisionID, ObservedAt: observed, Retention: description.Retention, Consequence: description.Consequence,
	}
	header.RetentionBasis = RetentionBasisDigest(auditSHA256("frostgrove.audit/retention-basis/v1", []byte(header.Retention), []byte(observed.Format(time.RFC3339Nano))))
	var idempotency IdentityCommitmentSet
	if frame.hasIdempotency {
		identityRequest, requestErr := idempotencyRequest(r.active, header.Operation, header.OperationID, frame.idempotency, r.identityDescriptions())
		if requestErr != nil {
			return AppendRequest{}, ReconcileKey{}, requestErr
		}
		idempotency, err = r.identities.CommitIdentities(ctx, identityRequest)
		if err != nil {
			return AppendRequest{}, ReconcileKey{}, cryptoRuntimeError(err)
		}
		header.Idempotency, err = idempotencyTokenOf(idempotency)
		if err != nil {
			return AppendRequest{}, ReconcileKey{}, err
		}
		header.HasIdempotency = true
	}
	logical := groupSemanticBytes(header.Operation, header.OperationID, frame.context, frame.policy, events, entities)
	header.Semantic, err = r.semantics.Digest(ctx, logical)
	if err != nil || header.Semantic == (SemanticDigest{}) {
		return AppendRequest{}, ReconcileKey{}, cryptoRuntimeError(err)
	}
	tools := materializers{privacy: r.privacy, protector: r.protector, tokenizer: r.tokenizer}
	actors, facts, err := materializeContext(ctx, tools, header, frame.context, frame.policy)
	if err != nil {
		return AppendRequest{}, ReconcileKey{}, err
	}
	type pendingItem struct {
		kind   ItemKind
		event  draft
		entity entityDraft
		key    string
	}
	pending := make([]pendingItem, 0, len(events)+len(entities))
	for _, event := range events {
		pending = append(pending, pendingItem{kind: EventItem, event: event, key: "e\x00" + string(event.descriptor.Resource) + "\x00" + string(event.descriptor.Action) + "\x00" + string(event.target)})
	}
	for _, entity := range entities {
		pending = append(pending, pendingItem{kind: EntityItem, entity: entity, key: "r\x00" + string(entity.descriptor.Resource) + "\x00" + string(entity.action) + "\x00" + string(entity.subject)})
	}
	sort.SliceStable(pending, func(left, right int) bool { return pending[left].key < pending[right].key })
	items := make([]ItemWireView, len(pending))
	bindings := make([]EntityAliasBinding, 0, len(entities))
	seenChains := make(map[EntityChainID]struct{}, len(entities))
	scope, scopePresent, err := scopeCommitment(ctx, r.identities, r.active, frame.context, r.scopeIdentityDescriptions())
	if err != nil {
		return AppendRequest{}, ReconcileKey{}, err
	}
	for index, value := range pending {
		if value.kind == EventItem {
			items[index], err = materializeDraft(ctx, tools, header, uint16(index), value.event)
			if err != nil {
				return AppendRequest{}, ReconcileKey{}, err
			}
			items[index].Leaf = leafDigestOf(header.Log, header.Catalog, header.RevisionID, items[index])
			continue
		}
		identityRequest, requestErr := identitySubjectRequest(r.active, value.entity.descriptor.Resource, scope, scopePresent, value.entity.subject, r.identityDescriptions())
		if requestErr != nil {
			return AppendRequest{}, ReconcileKey{}, requestErr
		}
		commitments, requestErr := r.identities.CommitIdentities(ctx, identityRequest)
		if requestErr != nil {
			return AppendRequest{}, ReconcileKey{}, cryptoRuntimeError(requestErr)
		}
		candidate := entityChainCandidate(value.entity.descriptor.Resource, scope, scopePresent, commitments.Active())
		if _, duplicate := seenChains[candidate]; duplicate {
			return AppendRequest{}, ReconcileKey{}, auditErrorAt(ErrConflict, "entity.subject")
		}
		seenChains[candidate] = struct{}{}
		headRequest, requestErr := newEntityHeadRequest(EntityHeadRequestView{Resource: value.entity.descriptor.Resource, Candidate: candidate, ScopePresent: scopePresent, Scope: scope, Commitments: commitments})
		if requestErr != nil {
			return AppendRequest{}, ReconcileKey{}, requestErr
		}
		head, requestErr := entityHead(ctx, frame.execution, headRequest)
		if requestErr != nil {
			return AppendRequest{}, ReconcileKey{}, requestErr
		}
		if requestErr = validateEntityTransition(value.entity, head); requestErr != nil {
			return AppendRequest{}, ReconcileKey{}, requestErr
		}
		items[index], err = materializeEntityDraft(ctx, tools, header, uint16(index), value.entity, head)
		if err != nil {
			return AppendRequest{}, ReconcileKey{}, err
		}
		items[index].Leaf = leafDigestOf(header.Log, header.Catalog, header.RevisionID, items[index])
		binding, requestErr := newEntityAliasBinding(value.entity.descriptor.Resource, head.ChainID(), scope, scopePresent, commitments)
		if requestErr != nil {
			return AppendRequest{}, ReconcileKey{}, requestErr
		}
		bindings = append(bindings, binding)
	}
	header.Authorization = authorizationSummary(items, actors, facts)
	view := RevisionWireView{Header: header, Actors: actors, Context: facts, Items: items}
	view.Header.Envelope = envelopeDigestOf(view)
	view.Header.Integrity = integrityDigestOf(view.Header)
	if !nilByReflection(r.signer) {
		view.Header.Seal, err = r.signer.Sign(ctx, view.Header.Integrity)
		if err != nil {
			return AppendRequest{}, ReconcileKey{}, cryptoRuntimeError(err)
		}
	}
	revision, err := newRevision(view)
	if err != nil {
		return AppendRequest{}, ReconcileKey{}, err
	}
	intent := appendIntentDigestOf(revision.View())
	request, err := newAppendRequestWithEntities(revision, intent, idempotency, bindings)
	if err != nil {
		return AppendRequest{}, ReconcileKey{}, err
	}
	reconcile, err := newReconcileKey(r.writer.BackingID(), view.Header)
	if err != nil {
		return AppendRequest{}, ReconcileKey{}, err
	}
	return request, reconcile, nil
}

func groupSemanticBytes(operation OperationName, operationID OperationID, context Context, policy ContextPolicy, events []draft, entities []entityDraft) []byte {
	var output bytes.Buffer
	writeFrame(&output, []byte("frostgrove.audit/group-semantic/v1"))
	writeFrame(&output, []byte(operation))
	writeFrame(&output, operationID[:])
	writeSemanticContext(&output, context, policy)
	events = slices.Clone(events)
	entities = slices.Clone(entities)
	sort.SliceStable(events, func(left, right int) bool {
		leftKey := string(events[left].descriptor.Resource) + "\x00" + string(events[left].descriptor.Action) + "\x00" + string(events[left].target)
		rightKey := string(events[right].descriptor.Resource) + "\x00" + string(events[right].descriptor.Action) + "\x00" + string(events[right].target)
		return leftKey < rightKey
	})
	sort.SliceStable(entities, func(left, right int) bool {
		leftKey := string(entities[left].descriptor.Resource) + "\x00" + string(entities[left].action) + "\x00" + string(entities[left].subject)
		rightKey := string(entities[right].descriptor.Resource) + "\x00" + string(entities[right].action) + "\x00" + string(entities[right].subject)
		return leftKey < rightKey
	})
	writeUint32(&output, uint32(len(events)))
	for _, event := range events {
		writeDraft(&output, event)
	}
	writeUint32(&output, uint32(len(entities)))
	for _, entity := range entities {
		writeEntityDraft(&output, entity)
	}
	return output.Bytes()
}

func validateEntityTransition(value entityDraft, head EntityHeadResult) error {
	switch value.action {
	case EntityCreated:
		if head.State() != EntityGenesis {
			return auditErrorAt(ErrConflict, "entity.created")
		}
	case EntityChanged:
		if head.State() == EntityTerminal || head.State() == EntityGenesis && value.state != EntityFullState {
			return auditErrorAt(ErrConflict, "entity.changed")
		}
	case EntitySoftDeleted, EntityHardDeleted:
		if head.State() == EntityTerminal {
			return auditErrorAt(ErrConflict, "entity.deleted")
		}
	case EntityRestored:
		if head.State() == EntityTerminal {
			return auditErrorAt(ErrConflict, "entity.restored")
		}
	default:
		return auditErrorAt(ErrUnsupported, "entity.action")
	}
	return nil
}

func stageEntities(ctx context.Context, recorder *Recorder, values ...EntityDraft) error {
	if recorder == nil || recorder.value == nil || len(values) == 0 {
		return auditErrorAt(ErrInvalid, "entities")
	}
	frame, _ := ctx.Value(groupContextKey{}).(*groupFrame)
	if frame == nil || frame.recorder != recorder.value {
		return auditErrorAt(ErrTransaction, "group")
	}
	return frame.addEntities(values)
}

func groupExecution(ctx context.Context, recorder *Recorder) (Execution, bool) {
	frame, _ := ctx.Value(groupContextKey{}).(*groupFrame)
	if recorder == nil || recorder.value == nil || frame == nil || frame.recorder != recorder.value || frame.execution == nil {
		return nil, false
	}
	return frame.execution, true
}

func copyEntityDrafts(values []EntityDraft) []EntityDraft {
	return slices.Clone(values)
}
