package audit

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"time"
)

type AttemptsProfile uint8

const RunOnlyAlpha AttemptsProfile = 1

type AttemptsConfig struct {
	Profile           AttemptsProfile
	Recorder          *Recorder
	State             AttemptLog
	Types             AttemptTypeState
	History           *History
	SettlementTimeout time.Duration
}

type attempts struct {
	profile    AttemptsProfile
	recorder   *Recorder
	state      AttemptLog
	types      AttemptTypeState
	history    *History
	settlement time.Duration
}

type Attempts struct{ value *attempts }

type AttemptRunSpec struct{ IdempotencyKey IdempotencyKey }
type AttemptBeginSpec struct{ IdempotencyKey IdempotencyKey }
type AttemptTransitionSpec struct{ IdempotencyKey IdempotencyKey }
type AttemptGroupSpec struct{ IdempotencyKey IdempotencyKey }

type AttemptAccessSpec struct {
	Purpose  Purpose
	Role     Reference
	Scope    ScopeSelector
	Target   Reference
	MaxBytes uint64
}

type attemptResult struct {
	receipt    Receipt
	retry      RetryToken
	transition AttemptTransitionWireView
	projection AttemptProjectionStateView
	hasAttempt bool
}

type AttemptResult struct{ value attemptResult }

func (r AttemptResult) ResultingState() (AttemptState, bool) {
	if !r.value.hasAttempt {
		return 0, false
	}
	return r.value.projection.State, true
}

func (r AttemptResult) Receipt() (Receipt, bool) {
	return r.value.receipt, r.value.receipt.value.revision != (RevisionID{})
}

func (r AttemptResult) RetryToken() (RetryToken, bool) {
	return r.value.retry, r.value.retry.value.reconcile.valid()
}

func (r AttemptResult) ReconcileKey() (ReconcileKey, bool) {
	if receipt, ok := r.Receipt(); ok {
		return receipt.ReconcileKey()
	}
	if retry, ok := r.RetryToken(); ok {
		return retry.ReconcileKey(), true
	}
	return ReconcileKey{}, false
}

func (r AttemptResult) Transition() (AttemptTransitionWireView, bool) {
	if _, ok := r.Receipt(); !ok || !r.value.hasAttempt {
		return AttemptTransitionWireView{}, false
	}
	return r.value.transition, true
}

func attemptResultFromAppend(result AppendResult, receipt Receipt) (AttemptResult, error) {
	transition, present := result.AttemptTransition()
	projection, projected := result.AttemptProjection()
	if !present || !projected {
		return AttemptResult{}, auditErrorAt(ErrIntegrity, "attempt.result")
	}
	return AttemptResult{value: attemptResult{
		receipt: receipt, transition: transition, projection: projection, hasAttempt: true,
	}}, nil
}

type attemptExecution struct {
	attempt AttemptResult
	appErr  error
	invoked bool
}

type AttemptExecution struct{ value attemptExecution }

func (r AttemptExecution) Attempt() AttemptResult  { return r.value.attempt }
func (r AttemptExecution) ApplicationError() error { return r.value.appErr }
func (r AttemptExecution) Invoked() bool           { return r.value.invoked }

type attemptCompletion[F any] struct {
	present    bool
	transition AttemptTransitionKind
	reason     Reason
	value      F
}

type AttemptCompletion[F any] struct{ value attemptCompletion[F] }

func AttemptSucceeded[F any](value F) AttemptCompletion[F] {
	return AttemptCompletion[F]{value: attemptCompletion[F]{present: true, transition: AttemptSucceededTransition, value: value}}
}

func AttemptFailed[F any](reason Reason, value F) AttemptCompletion[F] {
	return AttemptCompletion[F]{value: attemptCompletion[F]{present: true, transition: AttemptFailedTransition, reason: reason, value: value}}
}

func AttemptCancelled[F any](reason Reason, value F) AttemptCompletion[F] {
	return AttemptCompletion[F]{value: attemptCompletion[F]{present: true, transition: AttemptCancelledTransition, reason: reason, value: value}}
}

type attemptRun[C, F any] struct {
	mu          sync.Mutex
	attempts    *attempts
	operation   OperationName
	operationID OperationID
	chain       AttemptChainID
	policy      AttemptPolicyFingerprint
	replay      AttemptReplayFingerprint
	descriptor  AttemptDescriptor
	maxState    uint64
	finish      attemptFinishPolicy[F]
	context     Context
	binding     AttemptIdentityAliasBinding
	state       AttemptProjectionStateView
	consumed    bool
}

type AttemptRun[C, F any] struct{ value *attemptRun[C, F] }
type AttemptProgress[C any] struct{}

func NewAttempts(config AttemptsConfig) (*Attempts, error) {
	if config.Profile != RunOnlyAlpha || config.Recorder == nil || config.Recorder.value == nil || nilByReflection(config.State) || nilByReflection(config.Types) || config.History == nil || config.History.value.runtime == nil || config.SettlementTimeout <= 0 || config.SettlementTimeout > MaxAttemptSettlementTimeout {
		return nil, auditErrorAt(ErrInvalid, "attempts.config")
	}
	writer := config.Recorder.value.writer
	writerCapabilities := writer.Capabilities().View()
	if writerCapabilities.AttemptLifecycle != SupportSupported || writerCapabilities.Idempotency != SupportSupported || config.State.Capabilities().View().AttemptLifecycle != SupportSupported || config.State.Capabilities().View().Idempotency != SupportSupported || config.Types.Capabilities().View().AttemptLifecycle != SupportSupported || nilByReflection(config.History.value.runtime.exact) || config.History.value.runtime.exact.Capabilities().View().ExactInspection != SupportSupported {
		return nil, auditErrorAt(ErrUnsupported, "attempts.capabilities")
	}
	infos := []StoreInfo{config.State, config.Types, config.History.value.runtime.exact}
	for _, info := range infos {
		if info.BackingID() != writer.BackingID() || info.LogID() != writer.LogID() || !SameBacking(info.Backing(), writer.Backing()) {
			return nil, auditErrorAt(ErrWrongStore, "attempts.store")
		}
		state := info.Catalogs()
		if !state.HasActive() || state.Active() != config.Recorder.value.active || state.SetDigest() != config.Recorder.value.set {
			return nil, auditErrorAt(ErrWrongCatalog, "attempts.store")
		}
	}
	if config.History.value.runtime.recorder != config.Recorder {
		return nil, auditErrorAt(ErrWrongStore, "attempts.history")
	}
	if err := validateAttemptSignatureInventory(config.Recorder.value.catalogs, config.History.value.runtime.verifier); err != nil {
		return nil, err
	}
	if err := validateAttemptControlInventory(config.Recorder.value.catalogs, config.Recorder.value.active); err != nil {
		return nil, err
	}
	return &Attempts{value: &attempts{
		profile: config.Profile, recorder: config.Recorder, state: config.State,
		types: config.Types, history: config.History, settlement: config.SettlementTimeout,
	}}, nil
}

func validateAttemptControlInventory(catalogs *CatalogSet, active CatalogRef) error {
	manifest, found := catalogs.Manifest(active)
	if !found {
		return auditErrorAt(ErrWrongCatalog, "attempts.control")
	}
	control := manifest.View().Control
	if !slices.Contains(control.Actions, AttemptContinuationAuthorized) || !slices.Contains(control.Actions, AttemptAccessDenied) {
		return auditErrorAt(ErrDeclaration, "attempts.control")
	}
	for _, policy := range control.Reasons {
		if policy.Action == AttemptAccessDenied && len(policy.Codes) > 0 {
			return nil
		}
	}
	return auditErrorAt(ErrDeclaration, "attempts.control")
}

func validateAttemptSignatureInventory(catalogs *CatalogSet, verifier Verifier) error {
	for _, manifest := range catalogs.Manifests() {
		view := manifest.View()
		hasAttempt := false
		for _, declaration := range view.Declarations {
			hasAttempt = hasAttempt || declaration.Kind == AttemptDeclaration
		}
		if hasAttempt && !view.Integrity.RequiresSignature {
			return auditErrorAt(ErrDeclaration, "attempts.signature")
		}
	}
	return validateHistoryVerifier(catalogs, verifier)
}

func (a *AttemptType[S, C, F]) Run(ctx context.Context, attempts *Attempts, input S, spec AttemptRunSpec, callback func(context.Context, *AttemptProgress[C]) (AttemptCompletion[F], error)) (AttemptExecution, error) {
	if callback == nil {
		return AttemptExecution{}, auditErrorAt(ErrInvalid, "attempt.run")
	}
	run, started, err := a.Begin(ctx, attempts, input, AttemptBeginSpec{IdempotencyKey: spec.IdempotencyKey})
	if err != nil {
		return AttemptExecution{}, err
	}
	if run == nil {
		return AttemptExecution{value: attemptExecution{attempt: started}}, nil
	}
	execution := AttemptExecution{value: attemptExecution{attempt: started, invoked: true}}
	completion, appErr := callback(ctx, &AttemptProgress[C]{})
	execution.value.appErr = appErr
	if !completion.value.present {
		if appErr != nil {
			return execution, nil
		}
		return execution, auditErrorAt(ErrInvalid, "attempt.completion")
	}
	settlement, cancel := context.WithTimeout(context.Background(), attempts.value.settlement)
	defer cancel()
	finished, finishErr := run.finish(settlement, completion, AttemptTransitionSpec{IdempotencyKey: spec.IdempotencyKey})
	if _, ok := finished.Receipt(); ok {
		execution.value.attempt = finished
	} else if _, ok := finished.RetryToken(); ok {
		execution.value.attempt = finished
	}
	return execution, finishErr
}

func (a *AttemptType[S, C, F]) Begin(ctx context.Context, attempts *Attempts, input S, spec AttemptBeginSpec) (*AttemptRun[C, F], AttemptResult, error) {
	if err := validateAttemptDoor(a, attempts, ctx, spec.IdempotencyKey); err != nil {
		return nil, AttemptResult{}, err
	}
	if err := requireAttemptDurability(a.value.descriptor.Consequence, attempts.value); err != nil {
		return nil, AttemptResult{}, err
	}
	runtime := attempts.value
	if err := runtime.validate(); err != nil {
		return nil, AttemptResult{}, err
	}
	resolved, operationID, generated, err := runtime.recorder.value.resolve(ctx, a.value.descriptor.Context)
	if err != nil {
		return nil, AttemptResult{}, err
	}
	if generated {
		return nil, AttemptResult{}, auditErrorAt(ErrInvalid, "attempt.operation")
	}
	runtimeContext := valueFreeContext{Context: ctx}
	intent, err := prepareAttemptStart(runtimeContext, runtime, a.value, input, resolved, operationID, spec.IdempotencyKey)
	if err != nil {
		return nil, AttemptResult{}, err
	}
	outcome, err := runtime.appendAttemptTyped(runtimeContext, intent.selector, a.value.descriptor.Resource, func(typeState AttemptTypeProjectionStateView) (preparedAttempt, error) {
		intent.revision.typeExpected = typeState
		prepared, buildErr := runtime.buildAttemptRevision(runtimeContext, intent.revision)
		if buildErr != nil {
			return preparedAttempt{}, buildErr
		}
		prepared.operation, prepared.chain, prepared.replay, prepared.binding = intent.operation, intent.chain, intent.replay, intent.binding
		return prepared, nil
	})
	if err != nil {
		return nil, AttemptResult{}, err
	}
	prepared := outcome.prepared
	appendResult := outcome.result
	if outcome.appendErr != nil {
		mapped := mapAppendError(outcome.appendErr)
		retry := retryFor(prepared.request, prepared.reconcile, mapped)
		result := AttemptResult{value: attemptResult{retry: retry}}
		if retry.value.reconcile.valid() {
			return nil, result, &retryCarrier{token: retry, err: mapped}
		}
		return nil, result, mapped
	}
	if err := validateAppendResult(prepared.request, appendResult); err != nil {
		return nil, AttemptResult{}, auditErrorAt(ErrIntegrity, "attempt.append_result")
	}
	if appendResult.Disposition() == Replayed {
		current, stateErr := runtime.readAttemptState(runtimeContext, runtime.recorder.value.active.ID, operationID, prepared.chain)
		if stateErr != nil {
			return nil, AttemptResult{}, stateErr
		}
		if stateErr = runtime.verifyAttemptState(runtimeContext, current, a.value.descriptor.Resource); stateErr != nil {
			return nil, AttemptResult{}, stateErr
		}
		if stateErr = runtime.verifyAttemptReplay(runtimeContext, appendResult, current, a.value.descriptor.Resource, AttemptStartedTransition, false); stateErr != nil {
			return nil, AttemptResult{}, stateErr
		}
	}
	reconcile, err := newReconcileKey(runtime.recorder.value.writer.BackingID(), appendResult.Stored().Revision())
	if err != nil {
		return nil, AttemptResult{}, err
	}
	receipt := receiptFromAppend(appendResult, Committed, reconcile)
	result, err := attemptResultFromAppend(appendResult, receipt)
	if err != nil {
		return nil, AttemptResult{}, err
	}
	if appendResult.Disposition() == Replayed {
		return nil, result, nil
	}
	projection, ok := appendResult.AttemptProjection()
	if !ok || projection.State != AttemptOpenState {
		return nil, AttemptResult{}, auditErrorAt(ErrIntegrity, "attempt.projection")
	}
	run := &AttemptRun[C, F]{value: &attemptRun[C, F]{
		attempts: runtime, operation: prepared.operation, operationID: operationID,
		chain: prepared.chain, policy: a.value.fingerprint, replay: prepared.replay,
		descriptor: cloneAttemptDescriptor(a.value.descriptor), finish: cloneAttemptFinish(a.value.finish),
		maxState: a.value.maxStateBytes,
		context:  resolved, binding: prepared.binding, state: projection,
	}}
	return run, result, nil
}

func validateAttemptDoor[S, C, F any](attempt *AttemptType[S, C, F], attempts *Attempts, ctx context.Context, key IdempotencyKey) error {
	if attempt == nil || attempt.value == nil || attempts == nil || attempts.value == nil || ctx == nil || !validOpaqueReference(string(key), MaxIdempotencyKeyBytes) {
		return auditErrorAt(ErrInvalid, "attempt")
	}
	if !attempts.value.recorder.value.catalogContains(attempt) || !attempts.value.recorder.value.catalogContains(attempt.value.operation) {
		return auditErrorAt(ErrWrongCatalog, "attempt")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func requireAttemptDurability(consequence Consequence, runtime *attempts) error {
	if consequence != Required {
		return nil
	}
	stores := []StoreInfo{runtime.recorder.value.writer, runtime.state, runtime.types, runtime.history.value.runtime.exact}
	for _, store := range stores {
		capabilities := store.Capabilities().View()
		if capabilities.Persistence != SupportSupported || capabilities.Reconciliation != SupportSupported {
			return auditErrorAt(ErrUnsupported, "attempt.required")
		}
	}
	return nil
}

func (a *attempts) validate() error {
	if a == nil || a.recorder == nil || a.recorder.value == nil {
		return auditErrorAt(ErrInvalid, "attempts")
	}
	if err := a.recorder.value.checkStore(); err != nil {
		return err
	}
	for _, info := range []StoreInfo{a.state, a.types, a.history.value.runtime.exact} {
		if info.BackingID() != a.recorder.value.writer.BackingID() || info.LogID() != a.recorder.value.writer.LogID() || !SameBacking(info.Backing(), a.recorder.value.writer.Backing()) {
			return auditErrorAt(ErrWrongStore, "attempts")
		}
		state := info.Catalogs()
		if !state.HasActive() || state.Active() != a.recorder.value.active || state.SetDigest() != a.recorder.value.set {
			return auditErrorAt(ErrStaleCatalog, "attempts")
		}
	}
	return nil
}

func (a *AttemptType[S, C, F]) Resume(context.Context, *Attempts, OperationID, AttemptAccessSpec) (*AttemptRun[C, F], AttemptResult, error) {
	return nil, AttemptResult{}, auditErrorAt(ErrUnsupported, "attempt.resume")
}

func (a *AttemptType[S, C, F]) ResolveUnknown(context.Context, *Attempts, OperationID, AttemptAccessSpec, AttemptCompletion[F], AttemptTransitionSpec) (AttemptResult, error) {
	return AttemptResult{}, auditErrorAt(ErrUnsupported, "attempt.resolve_unknown")
}

func (a *AttemptType[S, C, F]) Abandon(context.Context, *Attempts, OperationID, AttemptAccessSpec, AttemptTransitionSpec) (AttemptResult, error) {
	return AttemptResult{}, auditErrorAt(ErrUnsupported, "attempt.abandon")
}

func (r *AttemptRun[C, F]) Checkpoint(context.Context, C, AttemptTransitionSpec) (AttemptResult, error) {
	return AttemptResult{}, auditErrorAt(ErrUnsupported, "attempt.checkpoint")
}

func (p *AttemptProgress[C]) Checkpoint(context.Context, C, AttemptTransitionSpec) (AttemptResult, error) {
	return AttemptResult{}, auditErrorAt(ErrUnsupported, "attempt.checkpoint")
}

func (r *AttemptRun[C, F]) Finish(ctx context.Context, completion AttemptCompletion[F], spec AttemptTransitionSpec) (AttemptResult, error) {
	if r == nil || r.value == nil || ctx == nil || !validOpaqueReference(string(spec.IdempotencyKey), MaxIdempotencyKeyBytes) || !completion.value.present {
		return AttemptResult{}, auditErrorAt(ErrInvalid, "attempt.finish")
	}
	if err := ctx.Err(); err != nil {
		return AttemptResult{}, err
	}
	return r.finish(ctx, completion, spec)
}

func (r *AttemptRun[C, F]) finish(ctx context.Context, completion AttemptCompletion[F], spec AttemptTransitionSpec) (AttemptResult, error) {
	r.value.mu.Lock()
	defer r.value.mu.Unlock()
	if r.value.consumed || r.value.state.State != AttemptOpenState {
		return AttemptResult{}, auditErrorAt(ErrConflict, "attempt.finish")
	}
	if !runOnlyFinishTransition(completion.value.transition) || !validAttemptCompletion(r.value.finish, completion) {
		return AttemptResult{}, auditErrorAt(ErrInvalid, "attempt.completion")
	}
	if err := requireAttemptDurability(r.value.descriptor.Consequence, r.value.attempts); err != nil {
		return AttemptResult{}, err
	}
	if err := r.value.attempts.validate(); err != nil {
		return AttemptResult{}, err
	}
	settlement, cancel := context.WithTimeout(ctx, r.value.attempts.settlement)
	defer cancel()
	runtimeContext := valueFreeContext{Context: settlement}
	intent, err := prepareAttemptFinishIntent(runtimeContext, r.value, completion, spec.IdempotencyKey)
	if err != nil {
		return AttemptResult{}, err
	}
	selector := AttemptTypeSelectorView{Catalog: r.value.attempts.recorder.value.active.ID, Operation: r.value.operation, Policy: r.value.policy, Replay: r.value.replay}
	outcome, err := r.value.attempts.appendAttemptTyped(runtimeContext, selector, r.value.descriptor.Resource, func(typeState AttemptTypeProjectionStateView) (preparedAttempt, error) {
		state, stateErr := r.value.attempts.readAttemptState(runtimeContext, r.value.attempts.recorder.value.active.ID, r.value.operationID, r.value.chain)
		if stateErr != nil {
			return preparedAttempt{}, stateErr
		}
		if stateErr = r.value.attempts.verifyAttemptState(runtimeContext, state, r.value.descriptor.Resource); stateErr != nil {
			return preparedAttempt{}, stateErr
		}
		if state.State() != r.value.state {
			return preparedAttempt{}, auditErrorAt(ErrConflict, "attempt.state")
		}
		prepared, buildErr := r.value.attempts.buildAttemptRevision(runtimeContext, attemptRevisionInput{
			operation: r.value.operation, operationID: r.value.operationID, chain: r.value.chain, policy: r.value.policy, replay: r.value.replay,
			descriptor: r.value.descriptor, maxStateBytes: r.value.maxState,
			resolved: r.value.context, targetCommitment: r.value.state.Target,
			targetPresent: r.value.state.TargetPresent, scopePresent: r.value.state.ScopePresent, scope: r.value.state.Scope, owner: r.value.state.Owner,
			transition: intent.transition, reason: intent.reason, values: intent.values,
			semantic: intent.semantic, expected: r.value.state, typeExpected: typeState, idempotency: intent.idempotency, binding: r.value.binding,
		})
		if buildErr != nil {
			return preparedAttempt{}, buildErr
		}
		prepared.operation, prepared.chain, prepared.replay, prepared.binding = r.value.operation, r.value.chain, r.value.replay, r.value.binding
		return prepared, nil
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			r.value.consumed = true
		}
		return AttemptResult{}, err
	}
	prepared := outcome.prepared
	appendResult := outcome.result
	if outcome.appendErr != nil {
		mapped := mapAppendError(outcome.appendErr)
		retry := retryFor(prepared.request, prepared.reconcile, mapped)
		if errors.Is(mapped, ErrUnconfirmed) {
			r.value.consumed = true
		}
		result := AttemptResult{value: attemptResult{retry: retry}}
		if retry.value.reconcile.valid() {
			return result, &retryCarrier{token: retry, err: mapped}
		}
		return result, mapped
	}
	if err := validateAppendResult(prepared.request, appendResult); err != nil {
		r.value.consumed = true
		return AttemptResult{}, auditErrorAt(ErrIntegrity, "attempt.append_result")
	}
	if appendResult.Disposition() == Replayed {
		current, stateErr := r.value.attempts.readAttemptState(runtimeContext, r.value.attempts.recorder.value.active.ID, r.value.operationID, r.value.chain)
		if stateErr != nil {
			r.value.consumed = true
			return AttemptResult{}, stateErr
		}
		if stateErr = r.value.attempts.verifyAttemptState(runtimeContext, current, r.value.descriptor.Resource); stateErr != nil {
			r.value.consumed = true
			return AttemptResult{}, stateErr
		}
		if stateErr = r.value.attempts.verifyAttemptReplay(runtimeContext, appendResult, current, r.value.descriptor.Resource, completion.value.transition, true); stateErr != nil {
			r.value.consumed = true
			return AttemptResult{}, stateErr
		}
	}
	reconcile, err := newReconcileKey(r.value.attempts.recorder.value.writer.BackingID(), appendResult.Stored().Revision())
	if err != nil {
		return AttemptResult{}, err
	}
	receipt := receiptFromAppend(appendResult, Committed, reconcile)
	result, err := attemptResultFromAppend(appendResult, receipt)
	if err != nil {
		r.value.consumed = true
		return AttemptResult{}, err
	}
	projection, ok := appendResult.AttemptProjection()
	if !ok || projection.State != attemptTerminalState(completion.value.transition) {
		r.value.consumed = true
		return AttemptResult{}, auditErrorAt(ErrIntegrity, "attempt.projection")
	}
	r.value.state = projection
	r.value.consumed = true
	return result, nil
}

func validAttemptCompletion[F any](policy attemptFinishPolicy[F], completion AttemptCompletion[F]) bool {
	if completion.value.transition == AttemptSucceededTransition {
		return completion.value.reason == ""
	}
	return validSemanticName(string(completion.value.reason)) && slices.Contains(policy.reasonsFor(completion.value.transition), completion.value.reason)
}

func (r *AttemptRun[C, F]) Within(context.Context, AttemptGroupSpec, func(context.Context) (AttemptCompletion[F], error)) (AttemptExecution, error) {
	return AttemptExecution{}, auditErrorAt(ErrUnsupported, "attempt.within")
}

func (r *AttemptRun[C, F]) OperationID() OperationID {
	if r == nil || r.value == nil {
		return OperationID{}
	}
	return r.value.operationID
}

func (r *AttemptRun[C, F]) State() (AttemptState, bool) {
	if r == nil || r.value == nil {
		return 0, false
	}
	r.value.mu.Lock()
	defer r.value.mu.Unlock()
	if r.value.consumed || !r.value.state.Present {
		return 0, false
	}
	return r.value.state.State, true
}

type preparedAttempt struct {
	request   AppendRequest
	reconcile ReconcileKey
	operation OperationName
	chain     AttemptChainID
	replay    AttemptReplayFingerprint
	binding   AttemptIdentityAliasBinding
}

type attemptStartIntent struct {
	selector  AttemptTypeSelectorView
	revision  attemptRevisionInput
	operation OperationName
	chain     AttemptChainID
	replay    AttemptReplayFingerprint
	binding   AttemptIdentityAliasBinding
}

func prepareAttemptStart[S, C, F any](ctx context.Context, runtime *attempts, declaration *attemptType[S, C, F], input S, resolved Context, operationID OperationID, key IdempotencyKey) (attemptStartIntent, error) {
	operation := declaration.operation.value.seal.description.Operation
	chain, err := AttemptChainCandidate(runtime.state.LogID(), runtime.recorder.value.active.ID, operationID)
	if err != nil {
		return attemptStartIntent{}, err
	}
	replay, err := runtime.attemptReplay(declaration.descriptor.Resource, operation, declaration.fingerprint)
	if err != nil {
		return attemptStartIntent{}, err
	}
	target, targetSet, targetCommitment, err := prepareAttemptTarget(ctx, runtime.recorder.value, declaration.start.target, input, operation)
	if err != nil {
		return attemptStartIntent{}, err
	}
	scopeSet, scopeCommitment, scopePresent, err := prepareAttemptScope(ctx, runtime.recorder.value, resolved)
	if err != nil {
		return attemptStartIntent{}, err
	}
	ownerSet, ownerCommitment, err := prepareAttemptOwner(ctx, runtime.recorder.value, operation, declaration.continuity, resolved)
	if err != nil {
		return attemptStartIntent{}, err
	}
	binding, err := newAttemptIdentityAliasBinding(operation, declaration.fingerprint, replay, operationID, chain, declaration.start.target.present, targetSet, scopePresent, scopeSet, ownerSet)
	if err != nil {
		return attemptStartIntent{}, err
	}
	state, err := runtime.readAttemptState(ctx, runtime.recorder.value.active.ID, operationID, chain)
	if err != nil {
		return attemptStartIntent{}, err
	}
	if state.State().Present {
		if err := runtime.verifyAttemptState(ctx, state, declaration.descriptor.Resource); err != nil {
			return attemptStartIntent{}, err
		}
		if err := matchAttemptIdentity(state.State(), operation, declaration.fingerprint, replay, targetCommitment, declaration.start.target.present, scopeCommitment, scopePresent, ownerSet, targetSet, scopeSet); err != nil {
			return attemptStartIntent{}, err
		}
		if state.State().TransitionBytes > declaration.maxStateBytes {
			return attemptStartIntent{}, auditErrorAt(ErrIntegrity, "attempt.state")
		}
		targetCommitment = state.State().Target
		scopeCommitment = state.State().Scope
		ownerCommitment = state.State().Owner
	}
	values, semanticValues, err := extractAttemptFields(declaration.start.fields, input)
	if err != nil {
		return attemptStartIntent{}, err
	}
	semanticTarget := AttemptSemanticTargetView{}
	if declaration.start.target.present {
		semanticTarget = AttemptSemanticTargetView{Present: true, Classification: declaration.start.target.classification, Mode: declaration.start.target.mode, Canonical: []byte(target)}
	}
	semantic, err := AttemptSemanticDigestOf(ctx, runtime.recorder.value.semantics, AttemptSemanticDigestInput{
		Catalog: runtime.recorder.value.active.ID, Policy: declaration.fingerprint, Replay: replay,
		Resource: declaration.descriptor.Resource, Operation: operation, OperationID: operationID, Chain: chain,
		ScopePresent: scopePresent, Scope: scopeCommitment, Owner: ownerCommitment,
		Transition: AttemptStartedTransition, Target: semanticTarget, Values: semanticValues,
	})
	if err != nil {
		return attemptStartIntent{}, err
	}
	idempotency, err := commitAttemptIdempotency(ctx, runtime.recorder.value, AttemptStartIdempotencyDomain, runtime.recorder.value.active.ID, AttemptChainID{}, key)
	if err != nil {
		return attemptStartIntent{}, err
	}
	selector := AttemptTypeSelectorView{Catalog: runtime.recorder.value.active.ID, Operation: operation, Policy: declaration.fingerprint, Replay: replay}
	return attemptStartIntent{selector: selector, operation: operation, chain: chain, replay: replay, binding: binding, revision: attemptRevisionInput{
		operation: operation, operationID: operationID, chain: chain, policy: declaration.fingerprint, replay: replay,
		descriptor: declaration.descriptor, maxOpen: declaration.maxOpen, maxStateBytes: declaration.maxStateBytes,
		openReserveBytes: declaration.openReserve,
		resolved:         resolved, targetPresent: declaration.start.target.present, target: target,
		targetPolicy: declaration.start.target, targetCommitment: targetCommitment,
		scopePresent: scopePresent, scope: scopeCommitment, owner: ownerCommitment,
		transition: AttemptStartedTransition, values: values, semantic: semantic,
		idempotency: idempotency, binding: binding,
	}}, nil
}

func (a *attempts) attemptReplay(resource Resource, operation OperationName, policy AttemptPolicyFingerprint) (AttemptReplayFingerprint, error) {
	manifest, found := a.recorder.value.catalogs.Manifest(a.recorder.value.active)
	if !found {
		return AttemptReplayFingerprint{}, auditErrorAt(ErrWrongCatalog, "attempt.replay")
	}
	var replay AttemptReplayFingerprint
	matches := 0
	for _, declaration := range manifest.View().Declarations {
		if declaration.Kind == AttemptDeclaration && declaration.Resource == resource && declaration.Attempt.Operation == operation && declaration.Attempt.Fingerprint == policy {
			replay = declaration.Attempt.Replay
			matches++
		}
	}
	expected, err := AttemptReplayFingerprintOf(policy, a.recorder.value.semantics.Description())
	if err != nil {
		return AttemptReplayFingerprint{}, err
	}
	if matches != 1 || replay == (AttemptReplayFingerprint{}) || replay != expected {
		return AttemptReplayFingerprint{}, auditErrorAt(ErrWrongCatalog, "attempt.replay")
	}
	return replay, nil
}

type attemptFinishIntent struct {
	transition  AttemptTransitionKind
	reason      Reason
	values      []draftValue
	semantic    SemanticDigest
	idempotency IdentityCommitmentSet
}

func prepareAttemptFinishIntent[C, F any](ctx context.Context, run *attemptRun[C, F], completion AttemptCompletion[F], key IdempotencyKey) (attemptFinishIntent, error) {
	runtime := run.attempts
	fields := run.finish.fieldsFor(completion.value.transition)
	values, semanticValues, err := extractAttemptFields(fields, completion.value.value)
	if err != nil {
		return attemptFinishIntent{}, err
	}
	semantic, err := AttemptSemanticDigestOf(ctx, runtime.recorder.value.semantics, AttemptSemanticDigestInput{
		Catalog: runtime.recorder.value.active.ID, Policy: run.policy, Replay: run.replay,
		Resource: run.descriptor.Resource, Operation: run.operation, OperationID: run.operationID, Chain: run.chain,
		ScopePresent: run.state.ScopePresent, Scope: run.state.Scope, Owner: run.state.Owner,
		Transition: completion.value.transition, Reason: completion.value.reason, Values: semanticValues,
	})
	if err != nil {
		return attemptFinishIntent{}, err
	}
	idempotency, err := commitAttemptIdempotency(ctx, runtime.recorder.value, AttemptFinishIdempotencyDomain, runtime.recorder.value.active.ID, run.chain, key)
	if err != nil {
		return attemptFinishIntent{}, err
	}
	frozen := make([]draftValue, len(values))
	for index, value := range values {
		frozen[index] = cloneDraftValue(value)
	}
	return attemptFinishIntent{
		transition: completion.value.transition, reason: completion.value.reason,
		values: frozen, semantic: semantic, idempotency: idempotency,
	}, nil
}

type attemptAppendOutcome struct {
	prepared  preparedAttempt
	result    AppendResult
	appendErr error
}

func (a *attempts) appendAttemptTyped(ctx context.Context, selector AttemptTypeSelectorView, resource Resource, build func(AttemptTypeProjectionStateView) (preparedAttempt, error)) (attemptAppendOutcome, error) {
	query, err := newAttemptTypeStateQuery(AttemptTypeStateQueryView{Log: a.types.LogID(), Catalogs: catalogRefs(a.recorder.value.catalogs), Type: selector})
	if err != nil {
		return attemptAppendOutcome{}, err
	}
	lease, err := a.types.LockAttemptType(ctx, query)
	if err != nil {
		return attemptAppendOutcome{}, mapStoreError(err)
	}
	if nilByReflection(lease) {
		return attemptAppendOutcome{}, auditErrorAt(ErrIntegrity, "attempt.type_lease")
	}
	defer lease.Release()
	typeState := lease.State()
	if err := validateAttemptTypeStateResult(query, typeState); err != nil {
		return attemptAppendOutcome{}, err
	}
	if err := a.verifyAttemptTypeState(ctx, typeState, resource); err != nil {
		return attemptAppendOutcome{}, err
	}
	prepared, err := build(typeState.State())
	if err != nil {
		return attemptAppendOutcome{}, err
	}
	result, appendErr := lease.Append(ctx, a.recorder.value.writer, prepared.request)
	return attemptAppendOutcome{prepared: prepared, result: result, appendErr: appendErr}, nil
}

func (a *attempts) readAttemptState(ctx context.Context, catalog CatalogID, operation OperationID, chain AttemptChainID) (AttemptStateResult, error) {
	limits := a.state.Limits().View()
	query, err := newAttemptStateQuery(AttemptStateQueryView{
		Log: a.state.LogID(), Catalogs: catalogRefs(a.recorder.value.catalogs), Catalog: catalog,
		OperationID: operation, Chain: chain, MaxTransitions: limits.AttemptTransitions, MaxBytes: limits.AttemptStateBytes,
	})
	if err != nil {
		return AttemptStateResult{}, err
	}
	result, err := a.state.AttemptState(ctx, query)
	if err != nil {
		return AttemptStateResult{}, mapStoreError(err)
	}
	if err := validateAttemptStateResult(query, result); err != nil {
		return AttemptStateResult{}, err
	}
	return result, nil
}

func (a *attempts) readAttemptTypeState(ctx context.Context, selector AttemptTypeSelectorView) (AttemptTypeStateResult, error) {
	query, err := newAttemptTypeStateQuery(AttemptTypeStateQueryView{Log: a.types.LogID(), Catalogs: catalogRefs(a.recorder.value.catalogs), Type: selector})
	if err != nil {
		return AttemptTypeStateResult{}, err
	}
	result, err := a.types.AttemptTypeState(ctx, query)
	if err != nil {
		return AttemptTypeStateResult{}, mapStoreError(err)
	}
	if err := validateAttemptTypeStateResult(query, result); err != nil {
		return AttemptTypeStateResult{}, err
	}
	return result, nil
}

func extractAttemptFields[P any](fields []attemptField[P], input P) ([]draftValue, []AttemptSemanticValueView, error) {
	values := make([]draftValue, len(fields))
	semantic := make([]AttemptSemanticValueView, len(fields))
	for index, field := range fields {
		value, canonical, err := field.extract(input)
		if err != nil {
			return nil, nil, err
		}
		values[index] = value
		semantic[index] = AttemptSemanticValueView{
			Field: value.field, Codec: value.codec, Classification: value.classification,
			Mode: value.mode, State: value.state, Canonical: bytes.Clone(canonical),
		}
	}
	return values, semantic, nil
}

func prepareAttemptTarget[S any](ctx context.Context, recorder *recorder, policy attemptTargetPolicy[S], input S, operation OperationName) (Reference, IdentityCommitmentSet, AttemptTargetCommitment, error) {
	if !policy.present {
		return "", IdentityCommitmentSet{}, AttemptTargetCommitment{}, nil
	}
	target, err := policy.reference(input)
	if err != nil {
		return "", IdentityCommitmentSet{}, AttemptTargetCommitment{}, err
	}
	request, err := AttemptTargetIdentityRequest(recorder.active.ID, operation, target)
	if err != nil {
		return "", IdentityCommitmentSet{}, AttemptTargetCommitment{}, err
	}
	request.value.required = slices.Clone(recorder.identityDescriptions())
	set, err := recorder.identities.CommitIdentities(ctx, request)
	if err != nil {
		return "", IdentityCommitmentSet{}, AttemptTargetCommitment{}, cryptoRuntimeError(err)
	}
	commitment, err := AttemptTargetCommitmentOf(set)
	return target, set, commitment, err
}

func prepareAttemptScope(ctx context.Context, recorder *recorder, resolved Context) (IdentityCommitmentSet, EvidenceScopeCommitment, bool, error) {
	scope, present := resolved.Scope.Get()
	if !present {
		return IdentityCommitmentSet{}, EvidenceScopeCommitment{}, false, nil
	}
	plaintext, err := contextValueBytes(scope)
	if err != nil {
		return IdentityCommitmentSet{}, EvidenceScopeCommitment{}, false, err
	}
	var aad bytes.Buffer
	writeFrame(&aad, []byte("frostgrove.audit/evidence-scope/v2"))
	writeFrame(&aad, []byte(recorder.active.ID))
	request, err := newIdentityCommitmentRequest(CommitEvidenceScope, plaintext, aad.Bytes(), recorder.scopeIdentityDescriptions())
	if err != nil {
		return IdentityCommitmentSet{}, EvidenceScopeCommitment{}, false, err
	}
	set, err := recorder.identities.CommitIdentities(ctx, request)
	if err != nil {
		return IdentityCommitmentSet{}, EvidenceScopeCommitment{}, false, cryptoRuntimeError(err)
	}
	wire := set.Active().Bytes()
	if len(wire) != 32 {
		return IdentityCommitmentSet{}, EvidenceScopeCommitment{}, false, auditErrorAt(ErrIntegrity, "attempt.scope")
	}
	var commitment EvidenceScopeCommitment
	copy(commitment[:], wire)
	return set, commitment, true, nil
}

func prepareAttemptOwner(ctx context.Context, recorder *recorder, operation OperationName, continuity attemptContinuityPolicy, resolved Context) (IdentityCommitmentSet, AttemptOwnerCommitment, error) {
	components, err := attemptOwnerComponents(continuity.facts, resolved.Actors)
	if err != nil {
		return IdentityCommitmentSet{}, AttemptOwnerCommitment{}, err
	}
	request, err := AttemptOwnerIdentityRequest(AttemptOwnerIdentityInput{Catalog: recorder.active.ID, Operation: operation, Components: components})
	if err != nil {
		return IdentityCommitmentSet{}, AttemptOwnerCommitment{}, err
	}
	request.value.required = slices.Clone(recorder.identityDescriptions())
	set, err := recorder.identities.CommitIdentities(ctx, request)
	if err != nil {
		return IdentityCommitmentSet{}, AttemptOwnerCommitment{}, cryptoRuntimeError(err)
	}
	commitment, err := AttemptOwnerCommitmentOf(set)
	return set, commitment, err
}

func attemptOwnerComponents(facts []AttemptOwnerFact, actors []Actor) ([]AttemptOwnerIdentityComponent, error) {
	result := make([]AttemptOwnerIdentityComponent, 0, len(facts))
	for _, fact := range facts {
		var matches []Actor
		switch fact {
		case AttemptEffectiveActorOwner:
			if len(actors) > 0 {
				matches = []Actor{actors[0]}
			}
		case AttemptWorkloadActorOwner:
			for _, actor := range actors {
				if actor.Kind == WorkloadActor {
					matches = append(matches, actor)
				}
			}
		case AttemptServiceOwner:
			for _, actor := range actors {
				if actor.Kind == ServiceActor {
					matches = append(matches, actor)
				}
			}
		default:
			return nil, auditErrorAt(ErrInvalid, "attempt.owner")
		}
		if len(matches) != 1 {
			return nil, auditErrorAt(ErrInvalid, "attempt.owner")
		}
		result = append(result, AttemptOwnerIdentityComponent{Fact: fact, ActorKind: matches[0].Kind, Reference: matches[0].Reference})
	}
	return result, nil
}

func commitAttemptIdempotency(ctx context.Context, recorder *recorder, kind IdempotencyDomainKind, catalog CatalogID, chain AttemptChainID, key IdempotencyKey) (IdentityCommitmentSet, error) {
	request, err := attemptIdempotencyRequest(kind, catalog, chain, key, recorder.identityDescriptions())
	if err != nil {
		return IdentityCommitmentSet{}, err
	}
	set, err := recorder.identities.CommitIdentities(ctx, request)
	if err != nil {
		return IdentityCommitmentSet{}, cryptoRuntimeError(err)
	}
	return set, nil
}

func matchAttemptIdentity(state AttemptProjectionStateView, operation OperationName, policy AttemptPolicyFingerprint, replay AttemptReplayFingerprint, target AttemptTargetCommitment, targetPresent bool, scope EvidenceScopeCommitment, scopePresent bool, owners, targets, scopes IdentityCommitmentSet) error {
	if state.Operation != operation || state.Policy != policy || state.Replay != replay || state.TargetPresent != targetPresent || state.ScopePresent != scopePresent || !identitySetContains(owners, state.Owner) || targetPresent && !identitySetContains(targets, state.Target) || scopePresent && !identitySetContains(scopes, state.Scope) {
		return auditErrorAt(ErrConflict, "attempt.identity")
	}
	if !targetPresent && target != (AttemptTargetCommitment{}) || !scopePresent && scope != (EvidenceScopeCommitment{}) {
		return auditErrorAt(ErrConflict, "attempt.identity")
	}
	return nil
}

func identitySetContains[T ~[32]byte](set IdentityCommitmentSet, value T) bool {
	for _, alias := range set.Aliases() {
		wire := alias.Bytes()
		if len(wire) == 32 && bytes.Equal(wire, value[:]) {
			return true
		}
	}
	return false
}

type attemptRevisionInput struct {
	operation        OperationName
	operationID      OperationID
	chain            AttemptChainID
	policy           AttemptPolicyFingerprint
	replay           AttemptReplayFingerprint
	descriptor       AttemptDescriptor
	maxOpen          time.Duration
	maxStateBytes    uint64
	openReserveBytes uint64
	resolved         Context
	targetPresent    bool
	target           Reference
	targetPolicy     any
	targetCommitment AttemptTargetCommitment
	scopePresent     bool
	scope            EvidenceScopeCommitment
	owner            AttemptOwnerCommitment
	transition       AttemptTransitionKind
	reason           Reason
	values           []draftValue
	semantic         SemanticDigest
	expected         AttemptProjectionStateView
	typeExpected     AttemptTypeProjectionStateView
	idempotency      IdentityCommitmentSet
	binding          AttemptIdentityAliasBinding
}

func (a *attempts) buildAttemptRevision(ctx context.Context, input attemptRevisionInput) (preparedAttempt, error) {
	if input.transition != AttemptStartedTransition && input.typeExpected.Unsettled == 0 {
		return preparedAttempt{}, auditErrorAt(ErrConflict, "attempt.type_state")
	}
	recorder := a.recorder.value
	revisionID, err := recorder.ids.NewRevisionID()
	if err != nil || revisionID == (RevisionID{}) {
		return preparedAttempt{}, auditError(ErrBackend, err)
	}
	observed := canonicalTime(recorder.clock.Now())
	if observed.IsZero() {
		return preparedAttempt{}, auditErrorAt(ErrInvalid, "clock")
	}
	expires := input.expected.ExpiresAt
	if input.transition == AttemptStartedTransition {
		expires = canonicalTime(observed.Add(input.maxOpen))
		if !expires.After(observed) {
			return preparedAttempt{}, auditErrorAt(ErrInvalid, "attempt.expires")
		}
	}
	header := RevisionHeaderView{
		Format: revisionFormatV1, Log: recorder.writer.LogID(), Catalog: recorder.active, CatalogSet: recorder.set,
		Deployment: recorder.deployment, Operation: input.operation, OperationID: input.operationID,
		RevisionID: revisionID, ObservedAt: observed, Retention: input.descriptor.Retention, Consequence: input.descriptor.Consequence,
		HasIdempotency: true, Semantic: input.semantic,
	}
	header.Idempotency, err = idempotencyTokenOf(input.idempotency)
	if err != nil {
		return preparedAttempt{}, err
	}
	header.RetentionBasis = RetentionBasisDigest(auditSHA256("frostgrove.audit/retention-basis/v1", []byte(header.Retention), []byte(observed.Format(time.RFC3339Nano))))
	tools := materializers{privacy: recorder.privacy, protector: recorder.protector, tokenizer: recorder.tokenizer}
	actors, facts, err := materializeContext(ctx, tools, header, input.resolved, input.descriptor.Context)
	if err != nil {
		return preparedAttempt{}, err
	}
	item := ItemWireView{Ordinal: 0, Kind: AttemptItem, Resource: input.descriptor.Resource, Reason: input.reason, Previous: input.expected.Leaf}
	item.Action, _ = attemptTransitionAction(input.transition)
	if input.transition == AttemptStartedTransition && input.targetPresent {
		policy, ok := input.targetPolicy.(interface {
			attemptTargetDescription() (Classification, StorageMode)
		})
		if !ok {
			return preparedAttempt{}, auditErrorAt(ErrInvalid, "attempt.target")
		}
		classification, mode := policy.attemptTargetDescription()
		target := draftValue{codec: ReferenceText().Description(), classification: classification, mode: mode, state: ValuePresent, canonical: []byte(input.target)}
		item.Target, err = tools.value(ctx, target, materializationAAD(header, 0, "", "attempt_target"))
		if err != nil {
			return preparedAttempt{}, err
		}
	}
	item.Values = make([]StoredValueView, len(input.values))
	for index, value := range input.values {
		item.Values[index], err = tools.value(ctx, value, materializationAAD(header, 0, value.field, "attempt_value"))
		if err != nil {
			return preparedAttempt{}, err
		}
	}
	ref := ItemRef{Revision: RevisionRef{Catalog: header.Catalog, Revision: header.RevisionID}, Ordinal: 0}
	next := attemptProjectionNext(input, ref, expires)
	typeNext := attemptTypeProjectionNext(input.typeExpected, ref, input.transition)
	transition := AttemptTransitionWireView{
		Chain: input.chain, Policy: input.policy, Replay: input.replay, Operation: input.operation,
		OperationID: input.operationID, ScopePresent: input.scopePresent, Scope: input.scope,
		Start: next.Start, Sequence: next.Sequence, CheckpointCount: next.CheckpointCount,
		Kind: input.transition, ExpiresAt: expires, Expected: input.expected, Result: next,
		TypeExpected: input.typeExpected, TypeResult: typeNext,
	}
	item.Attempt = transition
	var size byteCounter
	writeItem(&size, item, true)
	if uint64(size) > ^uint64(0)-input.expected.TransitionBytes {
		return preparedAttempt{}, auditErrorAt(ErrTooLarge, "attempt.state")
	}
	next.TransitionBytes = input.expected.TransitionBytes + uint64(size)
	limit := input.maxStateBytes
	if limit == 0 || limit > recorder.writer.Limits().View().AttemptStateBytes {
		limit = recorder.writer.Limits().View().AttemptStateBytes
	}
	if next.TransitionBytes > limit || input.transition == AttemptStartedTransition && (input.openReserveBytes > limit || next.TransitionBytes > limit-input.openReserveBytes) {
		return preparedAttempt{}, auditErrorAt(ErrTooLarge, "attempt.state")
	}
	item.Attempt.Result = next
	item.Leaf = leafDigestOf(header.Log, header.Catalog, header.RevisionID, item)
	header.Authorization = authorizationSummary([]ItemWireView{item}, actors, facts)
	view := RevisionWireView{Header: header, Actors: actors, Context: facts, Items: []ItemWireView{item}}
	view.Header.Envelope = envelopeDigestOf(view)
	view.Header.Integrity = integrityDigestOf(view.Header)
	if nilByReflection(recorder.signer) {
		return preparedAttempt{}, auditErrorAt(ErrDeclaration, "attempt.signer")
	}
	view.Header.Seal, err = recorder.signer.Sign(ctx, view.Header.Integrity)
	if err != nil {
		return preparedAttempt{}, cryptoRuntimeError(err)
	}
	revision, err := newRevision(view)
	if err != nil {
		return preparedAttempt{}, err
	}
	resultState := attemptStateFromNext(next, item.Leaf)
	typeResult := attemptTypeStateFromNext(typeNext, item.Leaf)
	conditional := AttemptConditionalAppendView{
		Chain: input.chain, Expected: input.expected, TypeExpected: input.typeExpected,
		Candidate: AttemptAppendCandidateView{Result: resultState, TypeResult: typeResult, Revision: revision},
	}
	request, err := newAttemptAppendRequest(revision, input.idempotency, conditional, input.binding)
	if err != nil {
		return preparedAttempt{}, err
	}
	reconcile, err := newReconcileKey(recorder.writer.BackingID(), view.Header)
	if err != nil {
		return preparedAttempt{}, err
	}
	return preparedAttempt{request: request, reconcile: reconcile}, nil
}

func attemptProjectionNext(input attemptRevisionInput, ref ItemRef, expires time.Time) AttemptProjectionNextView {
	if input.transition == AttemptStartedTransition {
		return AttemptProjectionNextView{
			Present: true, Chain: input.chain, Policy: input.policy, Replay: input.replay,
			Operation: input.operation, OperationID: input.operationID,
			TargetPresent: input.targetPresent, Target: input.targetCommitment,
			ScopePresent: input.scopePresent, Scope: input.scope, Owner: input.owner,
			State: AttemptOpenState, Sequence: 1, Start: ref, Head: ref, ExpiresAt: expires,
		}
	}
	expected := input.expected
	return AttemptProjectionNextView{
		Present: true, Chain: expected.Chain, Policy: expected.Policy, Replay: expected.Replay,
		Operation: expected.Operation, OperationID: expected.OperationID,
		TargetPresent: expected.TargetPresent, Target: expected.Target,
		ScopePresent: expected.ScopePresent, Scope: expected.Scope, Owner: expected.Owner,
		State: attemptTerminalState(input.transition), Sequence: expected.Sequence + 1,
		CheckpointCount: expected.CheckpointCount, Start: expected.Start, Head: ref, ExpiresAt: expected.ExpiresAt,
	}
}

func attemptTypeProjectionNext(expected AttemptTypeProjectionStateView, ref ItemRef, transition AttemptTransitionKind) AttemptTypeProjectionNextView {
	result := AttemptTypeProjectionNextView{
		Catalog: expected.Catalog, Operation: expected.Operation, Policy: expected.Policy, Replay: expected.Replay,
		Anchor: expected.Anchor, Unsettled: expected.Unsettled, Head: ref,
	}
	if transition == AttemptStartedTransition {
		result.Unsettled++
	} else {
		result.Unsettled--
	}
	return result
}

func (a *attempts) verifyAttemptState(ctx context.Context, result AttemptStateResult, resource Resource) error {
	state := result.State()
	refs := result.Transitions()
	if !state.Present || len(refs) == 0 {
		return auditErrorAt(ErrIntegrity, "attempt.state")
	}
	actions := attemptActions()
	classifications := attemptClassifications(a.recorder.value.catalogs, resource)
	targets := make([]ExactTargetView, len(refs))
	for index, reference := range refs {
		targets[index] = ExactTargetView{Kind: ExactItemTarget, Item: reference}
	}
	query, err := newExactQuery(ExactQueryView{
		Log: a.history.value.runtime.exact.LogID(), Catalogs: catalogRefs(a.recorder.value.catalogs),
		Targets: targets, Resources: []Resource{resource}, Actions: actions,
		Classifications: classifications, MaxBytes: a.history.value.runtime.exact.Limits().View().ExactBytes,
	})
	if err != nil {
		return err
	}
	exact, err := a.history.value.runtime.exact.Inspect(ctx, query)
	if err != nil {
		return mapStoreError(err)
	}
	if err := validateExactResult(query, exact); err != nil {
		return err
	}
	entries := exact.Entries()
	var folded AttemptProjectionStateView
	var bytesUsed uint64
	for index, entry := range entries {
		if entry.State != ExactFound {
			return auditErrorAt(ErrIntegrity, "attempt.state")
		}
		if err := verifyHistoryRevisionEvidence(ctx, a.recorder.value.catalogs, a.history.value.runtime.verifier, entry.Revision); err != nil {
			return err
		}
		revision := entry.Revision.View().Revision
		ordinal := refs[index].Ordinal
		if int(ordinal) >= len(revision.Items) {
			return auditErrorAt(ErrIntegrity, "attempt.state")
		}
		item := revision.Items[ordinal]
		if item.Kind != AttemptItem || item.Attempt.Expected != folded {
			return auditErrorAt(ErrIntegrity, "attempt.state")
		}
		var size byteCounter
		writeItem(&size, item, true)
		bytesUsed += uint64(size)
		if item.Attempt.Result.TransitionBytes != bytesUsed {
			return auditErrorAt(ErrIntegrity, "attempt.state")
		}
		folded = attemptStateFromNext(item.Attempt.Result, item.Leaf)
	}
	if folded != state {
		return auditErrorAt(ErrIntegrity, "attempt.state")
	}
	return nil
}

func (a *attempts) verifyAttemptTypeState(ctx context.Context, result AttemptTypeStateResult, resource Resource) error {
	state := result.State()
	if state.Head == (ItemRef{}) {
		if state.Anchor != (CatalogActivationGateDigest{}) || state.Unsettled != 0 || state.Leaf != (LeafDigest{}) {
			return auditErrorAt(ErrIntegrity, "attempt.type_state")
		}
		return nil
	}
	query, err := newExactQuery(ExactQueryView{
		Log: a.history.value.runtime.exact.LogID(), Catalogs: catalogRefs(a.recorder.value.catalogs),
		Targets: []ExactTargetView{{Kind: ExactItemTarget, Item: state.Head}}, Resources: []Resource{resource},
		Actions: attemptActions(), Classifications: attemptClassifications(a.recorder.value.catalogs, resource),
		MaxBytes: a.history.value.runtime.exact.Limits().View().ExactBytes,
	})
	if err != nil {
		return err
	}
	exact, err := a.history.value.runtime.exact.Inspect(ctx, query)
	if err != nil {
		return mapStoreError(err)
	}
	if err := validateExactResult(query, exact); err != nil {
		return err
	}
	entries := exact.Entries()
	if len(entries) != 1 || entries[0].State != ExactFound {
		return auditErrorAt(ErrIntegrity, "attempt.type_state")
	}
	if err := verifyHistoryRevisionEvidence(ctx, a.recorder.value.catalogs, a.history.value.runtime.verifier, entries[0].Revision); err != nil {
		return err
	}
	revision := entries[0].Revision.View().Revision
	if int(state.Head.Ordinal) >= len(revision.Items) {
		return auditErrorAt(ErrIntegrity, "attempt.type_state")
	}
	item := revision.Items[state.Head.Ordinal]
	if item.Kind != AttemptItem || attemptTypeStateFromNext(item.Attempt.TypeResult, item.Leaf) != state {
		return auditErrorAt(ErrIntegrity, "attempt.type_state")
	}
	return nil
}

func (a *attempts) verifyAttemptReplay(ctx context.Context, result AppendResult, current AttemptStateResult, resource Resource, transitionKind AttemptTransitionKind, requireHead bool) error {
	transition, present := result.AttemptTransition()
	projection, projected := result.AttemptProjection()
	header := result.Stored().Revision()
	reference := ItemRef{Revision: RevisionRef{Catalog: header.Catalog, Revision: header.RevisionID}}
	if !present || !projected || transition.Kind != transitionKind || projection != attemptStateFromNext(transition.Result, projection.Leaf) || !slices.Contains(current.Transitions(), reference) || requireHead && current.State() != projection {
		return auditErrorAt(ErrIntegrity, "attempt.replay")
	}
	query, err := newExactQuery(ExactQueryView{
		Log: a.history.value.runtime.exact.LogID(), Catalogs: catalogRefs(a.recorder.value.catalogs),
		Targets: []ExactTargetView{{Kind: ExactItemTarget, Item: reference}}, Resources: []Resource{resource},
		Actions: attemptActions(), Classifications: attemptClassifications(a.recorder.value.catalogs, resource),
		MaxBytes: a.history.value.runtime.exact.Limits().View().ExactBytes,
	})
	if err != nil {
		return err
	}
	exact, err := a.history.value.runtime.exact.Inspect(ctx, query)
	if err != nil {
		return mapStoreError(err)
	}
	if err := validateExactResult(query, exact); err != nil {
		return err
	}
	entries := exact.Entries()
	if len(entries) != 1 || entries[0].State != ExactFound {
		return auditErrorAt(ErrIntegrity, "attempt.replay")
	}
	if err := verifyHistoryRevisionEvidence(ctx, a.recorder.value.catalogs, a.history.value.runtime.verifier, entries[0].Revision); err != nil {
		return err
	}
	stored := entries[0].Revision.View()
	if !reflect.DeepEqual(stored.Revision.Header, header) || !stored.RecordedAt.Equal(result.Stored().RecordedAt()) || !bytes.Equal(stored.Position.Bytes(), result.Stored().Position().Bytes()) || len(stored.Revision.Items) != 1 || stored.Revision.Items[0].Attempt != transition || stored.Revision.Items[0].Leaf != projection.Leaf {
		return auditErrorAt(ErrIntegrity, "attempt.replay")
	}
	return nil
}

func attemptClassifications(catalogs *CatalogSet, resource Resource) []Classification {
	values := make([]Classification, 0, 8)
	for _, manifest := range catalogs.Manifests() {
		for _, declaration := range manifest.View().Declarations {
			if declaration.Kind != AttemptDeclaration || declaration.Resource != resource {
				continue
			}
			if declaration.Attempt.Start.TargetPresent {
				values = append(values, declaration.Attempt.Start.Target.Classification)
			}
			for _, field := range declaration.Attempt.Start.Fields {
				values = append(values, field.Classification)
			}
			for _, phase := range declaration.Attempt.Finish {
				for _, field := range phase.Fields {
					values = append(values, field.Classification)
				}
			}
			for _, fact := range declaration.Context.Facts {
				values = append(values, fact.Classification)
			}
		}
	}
	slices.Sort(values)
	values = slices.Compact(values)
	if len(values) == 0 {
		return []Classification{Public}
	}
	return values
}

func (p attemptTargetPolicy[S]) attemptTargetDescription() (Classification, StorageMode) {
	return p.classification, p.mode
}
