package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/frostgrove/vv/crud"
)

type Config struct {
	Catalogs   *CatalogSet
	Writer     Writer
	Context    ContextResolver
	Privacy    PrivacyAdmission
	Semantics  SemanticDigester
	Identities IdentityKeyring
	Protector  Protector
	Tokenizer  Tokenizer
	Signer     Signer
	Clock      Clock
	IDs        IDSource
	Observer   Observer
}

type recorder struct {
	catalogs          *CatalogSet
	writer            Writer
	context           ContextResolver
	privacy           PrivacyAdmission
	semantics         SemanticDigester
	identities        IdentityKeyring
	protector         Protector
	tokenizer         Tokenizer
	signer            Signer
	clock             Clock
	ids               IDSource
	observer          Observer
	active            CatalogRef
	set               CatalogSetDigest
	identityKeys      []IdentityCommitmentDescription
	scopeIdentityKeys []IdentityCommitmentDescription
	deployment        DeploymentFingerprint
}

type Recorder struct {
	auditCRUDCarrier
	value *recorder
}

type recordOptions struct {
	operation      *OperationType
	idempotency    IdempotencyKey
	hasOperation   bool
	hasIdempotency bool
}

type RecordOption interface {
	auditRecordOption(*recordOptions)
}

type operationRecordOption struct{ operation *OperationType }

func (o operationRecordOption) auditRecordOption(options *recordOptions) {
	if options.hasOperation {
		options.operation = nil
		return
	}
	options.operation = o.operation
	options.hasOperation = true
}

type idempotencyRecordOption struct{ key IdempotencyKey }

func (o idempotencyRecordOption) auditRecordOption(options *recordOptions) {
	if options.hasIdempotency {
		options.idempotency = ""
		return
	}
	options.idempotency = o.key
	options.hasIdempotency = true
}

func InOperation(operation *OperationType) RecordOption {
	return operationRecordOption{operation: operation}
}

func WithIdempotencyKey(key IdempotencyKey) RecordOption {
	return idempotencyRecordOption{key: key}
}

func New(config Config) (*Recorder, error) {
	if config.Catalogs == nil || nilByReflection(config.Writer) || nilByReflection(config.Context) || nilByReflection(config.Semantics) || nilByReflection(config.Identities) {
		return nil, auditErrorAt(ErrInvalid, "config")
	}
	active := config.Catalogs.Active()
	set := config.Catalogs.Digest()
	if !active.valid() || set == (CatalogSetDigest{}) {
		return nil, auditErrorAt(ErrWrongCatalog, "catalogs")
	}
	state := config.Writer.Catalogs()
	if !state.HasActive() || state.Active() != active || state.SetDigest() != set {
		return nil, auditErrorAt(ErrWrongCatalog, "writer")
	}
	if config.Writer.BackingID() == (BackingID{}) || config.Writer.LogID() == (LogID{}) || !config.Writer.Backing().valueValid() {
		return nil, auditErrorAt(ErrWrongStore, "writer")
	}
	manifest, ok := config.Catalogs.Manifest(active)
	if !ok || !validManifest(manifest) {
		return nil, auditErrorAt(ErrWrongCatalog, "manifest")
	}
	if err := validateRuntimeCollaborators(config, manifest.View()); err != nil {
		return nil, err
	}
	if err := validateIdentityLineage(config.Catalogs, config.Identities); err != nil {
		return nil, err
	}
	clock := config.Clock
	if nilByReflection(clock) {
		clock = systemClock{}
	}
	ids := config.IDs
	if nilByReflection(ids) {
		ids = cryptoIDSource{}
	}
	identityKeys, scopeIdentityKeys := lineageIdentityDescriptions(config.Catalogs, config.Identities.ActiveDescription())
	value := &recorder{
		catalogs: config.Catalogs, writer: config.Writer, context: config.Context,
		privacy: config.Privacy, semantics: config.Semantics, identities: config.Identities,
		protector: config.Protector, tokenizer: config.Tokenizer, signer: config.Signer,
		clock: clock, ids: ids, observer: config.Observer, active: active, set: set,
		identityKeys: identityKeys, scopeIdentityKeys: scopeIdentityKeys,
	}
	value.deployment = deploymentFingerprint(config, manifest.View())
	result := &Recorder{value: value}
	result.auditCRUDCarrier = newAuditCRUDCarrier(result)
	return result, nil
}

func validateRuntimeCollaborators(config Config, manifest ManifestView) error {
	if config.Semantics.Description() != manifest.Semantics {
		return cryptoDescriptionError("semantic")
	}
	if config.Identities.ActiveDescription() != manifest.Identities {
		return cryptoDescriptionError("identity")
	}
	if manifest.Protection != (ProtectionDescription{}) {
		if nilByReflection(config.Protector) || config.Protector.Description() != manifest.Protection {
			return cryptoDescriptionError("protection")
		}
	} else if !nilByReflection(config.Protector) {
		return cryptoDescriptionError("protection")
	}
	if manifest.Tokens != (TokenDescription{}) {
		if nilByReflection(config.Tokenizer) || config.Tokenizer.ActiveDescription() != manifest.Tokens {
			return cryptoDescriptionError("token")
		}
	} else if !nilByReflection(config.Tokenizer) {
		return cryptoDescriptionError("token")
	}
	if manifest.Integrity.RequiresSignature {
		if nilByReflection(config.Signer) || config.Signer.Description() != manifest.Integrity.Signature {
			return cryptoDescriptionError("signature")
		}
	} else if !nilByReflection(config.Signer) {
		return cryptoDescriptionError("signature")
	}
	return nil
}

func validateIdentityLineage(catalogs *CatalogSet, identities IdentityKeyring) error {
	descriptions := identities.Descriptions()
	if len(descriptions) == 0 || len(descriptions) > MaxCatalogs {
		return cryptoDescriptionError("identity lineage")
	}
	available := make(map[IdentityCommitmentDescription]struct{}, len(descriptions))
	for _, description := range descriptions {
		if err := validateProviderDescription(description.Algorithm, description.Profile, description.KeyID); err != nil {
			return cryptoDescriptionError("identity lineage")
		}
		if _, duplicate := available[description]; duplicate {
			return cryptoDescriptionError("identity lineage")
		}
		available[description] = struct{}{}
	}
	for _, manifest := range catalogs.Manifests() {
		if _, ok := available[manifest.View().Identities]; !ok {
			return cryptoDescriptionError("identity lineage")
		}
	}
	return nil
}

func lineageIdentityDescriptions(catalogs *CatalogSet, active IdentityCommitmentDescription) ([]IdentityCommitmentDescription, []IdentityCommitmentDescription) {
	all := []IdentityCommitmentDescription{active}
	manifests := catalogs.Manifests()
	for _, manifest := range manifests {
		description := manifest.View().Identities
		if !slices.Contains(all, description) {
			all = append(all, description)
		}
	}
	anchor := manifests[0].View().Identities
	scope := []IdentityCommitmentDescription{anchor}
	for _, description := range all {
		if description != anchor {
			scope = append(scope, description)
		}
	}
	return all, scope
}

func deploymentFingerprint(config Config, manifest ManifestView) DeploymentFingerprint {
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/deployment/v1"))
	catalogSet := config.Catalogs.Digest()
	writeFrame(digest, catalogSet[:])
	writeProvider(digest, manifest.Semantics.Algorithm, manifest.Semantics.Profile, manifest.Semantics.KeyID)
	writeProvider(digest, manifest.Identities.Algorithm, manifest.Identities.Profile, manifest.Identities.KeyID)
	writeProvider(digest, manifest.Protection.Algorithm, manifest.Protection.Profile, manifest.Protection.KeyID)
	writeProvider(digest, manifest.Tokens.Algorithm, manifest.Tokens.Profile, manifest.Tokens.KeyID)
	writeProvider(digest, manifest.Integrity.Signature.Algorithm, manifest.Integrity.Signature.Profile, manifest.Integrity.Signature.KeyID)
	privacy := config.Privacy.Fingerprint()
	writeFrame(digest, privacy[:])
	var output DeploymentFingerprint
	copy(output[:], digest.Sum(nil))
	return output
}

func (r *Recorder) Record(ctx context.Context, draft Draft, options ...RecordOption) (RecordResult, error) {
	started := time.Now()
	if r == nil || r.value == nil || ctx == nil || draft.value.declaration == nil {
		return RecordResult{}, auditErrorAt(ErrInvalid, "record")
	}
	if frame, _ := ctx.Value(groupContextKey{}).(*groupFrame); frame != nil {
		return RecordResult{}, auditErrorAt(ErrTransaction, "record.group")
	}
	resolved, err := resolveRecordOptions(options)
	if err != nil {
		return RecordResult{}, err
	}
	operation, policy, err := r.value.selectOperation(draft.value, resolved.operation)
	if err != nil {
		return RecordResult{}, err
	}
	if err := r.value.checkStore(); err != nil {
		return RecordResult{}, err
	}
	contextValue, operationID, generated, err := r.value.resolve(ctx, policy)
	if err != nil {
		r.value.observe(started, ObservationRecord, ObservationResolve, failureClassOf(err), 0, 0, 0, 0)
		return RecordResult{}, err
	}
	if resolved.hasIdempotency && generated {
		return RecordResult{}, auditErrorAt(ErrInvalid, "idempotency.operation")
	}
	runtimeContext := valueFreeContext{Context: ctx}
	request, reconcile, err := r.value.prepareEvent(runtimeContext, draft.value, contextValue, operation, operationID, resolved)
	if err != nil {
		return RecordResult{}, err
	}
	result, err := r.value.writer.Append(runtimeContext, request)
	if err != nil {
		mapped := mapAppendError(err)
		token := retryFor(request, reconcile, mapped)
		record := RecordResult{value: recordResult{retry: token}}
		r.value.observe(started, ObservationRecord, ObservationStore, failureClassOf(mapped), 1, 0, 0, 0)
		if token.value.reconcile.valid() {
			return record, &retryCarrier{token: token, err: mapped}
		}
		return record, mapped
	}
	if err := validateAppendResult(request, result); err != nil {
		return RecordResult{}, auditErrorAt(ErrIntegrity, "append_result")
	}
	key, err := newReconcileKey(r.value.writer.BackingID(), result.Stored().Revision())
	if err != nil {
		return RecordResult{}, err
	}
	receipt := receiptFromAppend(result, Committed, key)
	r.value.observe(started, ObservationRecord, ObservationStore, NoFailure, 1, uint16(len(request.View().Revision.View().Items)), result.Disposition(), Committed)
	return RecordResult{value: recordResult{receipt: receipt}}, nil
}

func resolveRecordOptions(input []RecordOption) (recordOptions, error) {
	var output recordOptions
	if len(input) > 2 {
		return output, auditErrorAt(ErrInvalid, "options")
	}
	for _, option := range input {
		if nilByReflection(option) {
			return output, auditErrorAt(ErrInvalid, "options")
		}
		option.auditRecordOption(&output)
	}
	if output.hasOperation && (output.operation == nil || output.operation.value == nil) {
		return output, auditErrorAt(ErrInvalid, "operation")
	}
	if output.hasIdempotency && !validOpaqueReference(string(output.idempotency), MaxIdempotencyKeyBytes) {
		return output, auditErrorAt(ErrInvalid, "idempotency")
	}
	return output, nil
}

func (r *recorder) selectOperation(input draft, override *OperationType) (OperationName, ContextPolicy, error) {
	if !r.catalogContains(input.declaration) {
		return "", ContextPolicy{}, auditErrorAt(ErrWrongCatalog, "declaration")
	}
	if override == nil {
		return OperationName(input.descriptor.Action), input.descriptor.Context, nil
	}
	if !r.catalogContains(override) {
		return "", ContextPolicy{}, auditErrorAt(ErrWrongCatalog, "operation")
	}
	key := declarationMemberKey(input.descriptor.Resource, input.descriptor.Action)
	for _, member := range override.value.members {
		if member.key == key && member.declaration == input.declaration {
			return override.value.seal.description.Operation, contextPolicyFromDescription(override.value.seal.description.Context), nil
		}
	}
	return "", ContextPolicy{}, auditErrorAt(ErrInvalid, "operation.member")
}

func contextPolicyFromDescription(description ContextPolicyDescription) ContextPolicy {
	facts := make([]contextFactPolicy, len(description.Facts))
	for index, fact := range description.Facts {
		facts[index] = contextFactPolicy{
			kind: fact.Kind, presence: fact.Presence, allowed: slices.Clone(fact.Allowed),
			classification: fact.Classification, mode: fact.Mode, generated: fact.Generated,
		}
	}
	return ContextPolicy{value: contextPolicy{facts: facts}}
}

func (r *recorder) catalogContains(declaration Declaration) bool {
	return r.catalogs.accepts(declaration)
}

func (r *recorder) resolve(ctx context.Context, policy ContextPolicy) (Context, OperationID, bool, error) {
	resolved, err := r.context.ResolveAuditContext(ctx)
	if contextErr := ctx.Err(); contextErr != nil {
		return Context{}, OperationID{}, false, contextErr
	}
	if err != nil {
		return Context{}, OperationID{}, false, auditError(ErrInvalid, err)
	}
	validated, err := validateAndCopyContext(resolved)
	if err != nil {
		return Context{}, OperationID{}, false, err
	}
	filtered := Context{}
	generated := false
	var operation OperationID
	for _, fact := range policy.value.facts {
		if fact.kind == ActorChainContext {
			continue
		}
		if fact.generated {
			if _, present := validated.Operation.Get(); present {
				return Context{}, OperationID{}, false, auditErrorAt(ErrInvalid, "context.operation")
			}
			operation, err = r.ids.NewOperationID()
			if err != nil || operation == (OperationID{}) {
				return Context{}, OperationID{}, false, auditError(ErrBackend, err)
			}
			filtered.Operation, _ = NewContextValue(operation, ServerDerived)
			generated = true
			continue
		}
		present, provenance, value := contextFactValue(validated, fact.kind)
		if !present {
			if fact.presence == ContextRequired {
				return Context{}, OperationID{}, false, auditErrorAt(ErrInvalid, "context."+fact.kind.String())
			}
			continue
		}
		if !slices.Contains(fact.allowed, provenance) {
			return Context{}, OperationID{}, false, auditErrorAt(ErrInvalid, "context."+fact.kind.String())
		}
		setContextFact(&filtered, fact.kind, value, provenance)
		if fact.kind == OperationContext {
			operation = value.(OperationID)
		}
	}
	if actorFact, declared := contextPolicyFact(policy, ActorChainContext); declared {
		if len(validated.Actors) == 0 && actorFact.presence == ContextRequired {
			return Context{}, OperationID{}, false, auditErrorAt(ErrInvalid, "context.actors")
		}
		for _, actor := range validated.Actors {
			if !slices.Contains(actorFact.allowed, actor.Provenance) {
				return Context{}, OperationID{}, false, auditErrorAt(ErrInvalid, "context.actors")
			}
		}
		filtered.Actors = slices.Clone(validated.Actors)
	}
	if operation == (OperationID{}) {
		return Context{}, OperationID{}, false, auditErrorAt(ErrInvalid, "context.operation")
	}
	return filtered, operation, generated, nil
}

func contextFactValue(value Context, kind ContextFactKind) (bool, Provenance, any) {
	switch kind {
	case ScopeContext:
		v, ok := value.Scope.Get()
		return ok, value.Scope.Provenance(), v
	case ServiceContext:
		v, ok := value.Service.Get()
		return ok, value.Service.Provenance(), v
	case DeploymentContext:
		v, ok := value.Deployment.Get()
		return ok, value.Deployment.Provenance(), v
	case ClientContext:
		v, ok := value.Client.Get()
		return ok, value.Client.Provenance(), v
	case OperationContext:
		v, ok := value.Operation.Get()
		return ok, value.Operation.Provenance(), v
	case CorrelationContext:
		v, ok := value.Correlation.Get()
		return ok, value.Correlation.Provenance(), v
	case CausationContext:
		v, ok := value.Causation.Get()
		return ok, value.Causation.Provenance(), v
	case TraceContext:
		v, ok := value.Trace.Get()
		return ok, value.Trace.Provenance(), v
	case SourceContext:
		v, ok := value.Source.Get()
		return ok, value.Source.Provenance(), v
	default:
		return false, UnstatedProvenance, nil
	}
}

func setContextFact(target *Context, kind ContextFactKind, value any, provenance Provenance) {
	switch kind {
	case ScopeContext:
		target.Scope, _ = NewContextValue(value.(ScopedReference), provenance)
	case ServiceContext:
		target.Service, _ = NewContextValue(value.(Reference), provenance)
	case DeploymentContext:
		target.Deployment, _ = NewContextValue(value.(Reference), provenance)
	case ClientContext:
		target.Client, _ = NewContextValue(value.(ScopedReference), provenance)
	case OperationContext:
		target.Operation, _ = NewContextValue(value.(OperationID), provenance)
	case CorrelationContext:
		target.Correlation, _ = NewContextValue(value.(Reference), provenance)
	case CausationContext:
		target.Causation, _ = NewContextValue(value.(Reference), provenance)
	case TraceContext:
		target.Trace, _ = NewContextValue(value.(Reference), provenance)
	case SourceContext:
		target.Source, _ = NewContextValue(value.(Source), provenance)
	}
}

func (r *recorder) prepareEvent(ctx context.Context, input draft, resolved Context, operation OperationName, operationID OperationID, options recordOptions) (AppendRequest, ReconcileKey, error) {
	if err := r.checkStore(); err != nil {
		return AppendRequest{}, ReconcileKey{}, err
	}
	revisionID, err := r.ids.NewRevisionID()
	if err != nil || revisionID == (RevisionID{}) {
		return AppendRequest{}, ReconcileKey{}, auditError(ErrBackend, err)
	}
	observed := canonicalTime(r.clock.Now())
	if observed.IsZero() {
		return AppendRequest{}, ReconcileKey{}, auditErrorAt(ErrInvalid, "clock")
	}
	header := RevisionHeaderView{
		Format: revisionFormatV1, Log: r.writer.LogID(), Catalog: r.active, CatalogSet: r.set,
		Deployment: r.deployment, Operation: operation, OperationID: operationID, RevisionID: revisionID,
		ObservedAt: observed, Retention: input.descriptor.Retention, Consequence: input.descriptor.Consequence,
	}
	header.RetentionBasis = RetentionBasisDigest(auditSHA256("frostgrove.audit/retention-basis/v1", []byte(header.Retention), []byte(observed.Format(time.RFC3339Nano))))
	var idempotency IdentityCommitmentSet
	if options.hasIdempotency {
		request, err := idempotencyRequest(r.active, operation, operationID, options.idempotency, r.identityDescriptions())
		if err != nil {
			return AppendRequest{}, ReconcileKey{}, err
		}
		idempotency, err = r.identities.CommitIdentities(ctx, request)
		if err != nil {
			return AppendRequest{}, ReconcileKey{}, cryptoRuntimeError(err)
		}
		header.Idempotency, err = idempotencyTokenOf(idempotency)
		if err != nil {
			return AppendRequest{}, ReconcileKey{}, err
		}
		header.HasIdempotency = true
	}
	semanticInput := semanticDraftBytes(operation, operationID, resolved, input.descriptor.Context, []draft{input})
	header.Semantic, err = r.semantics.Digest(ctx, semanticInput)
	if err != nil || header.Semantic == (SemanticDigest{}) {
		return AppendRequest{}, ReconcileKey{}, cryptoRuntimeError(err)
	}
	tools := materializers{privacy: r.privacy, protector: r.protector, tokenizer: r.tokenizer}
	actors, facts, err := materializeContext(ctx, tools, header, resolved, input.descriptor.Context)
	if err != nil {
		return AppendRequest{}, ReconcileKey{}, err
	}
	item, err := materializeDraft(ctx, tools, header, 0, input)
	if err != nil {
		return AppendRequest{}, ReconcileKey{}, err
	}
	item.Leaf = leafDigestOf(header.Log, header.Catalog, header.RevisionID, item)
	header.Authorization = authorizationSummary([]ItemWireView{item}, actors, facts)
	view := RevisionWireView{Header: header, Actors: actors, Context: facts, Items: []ItemWireView{item}}
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
	appendRequest, err := newAppendRequest(revision, intent, idempotency)
	if err != nil {
		return AppendRequest{}, ReconcileKey{}, err
	}
	reconcile, err := newReconcileKey(r.writer.BackingID(), view.Header)
	if err != nil {
		return AppendRequest{}, ReconcileKey{}, err
	}
	return appendRequest, reconcile, nil
}

func (r *recorder) identityDescriptions() []IdentityCommitmentDescription {
	return r.identityKeys
}

func (r *recorder) scopeIdentityDescriptions() []IdentityCommitmentDescription {
	return r.scopeIdentityKeys
}

func (r *recorder) checkStore() error {
	state := r.writer.Catalogs()
	if !state.HasActive() || state.Active() != r.active || state.SetDigest() != r.set {
		return auditErrorAt(ErrStaleCatalog, "writer")
	}
	return nil
}

func (r *Recorder) Retry(ctx context.Context, token RetryToken) (RecordResult, error) {
	if r == nil || r.value == nil || ctx == nil || token.value.mode != RetryStandalone || !token.value.reconcile.valid() || !token.value.request.valid() {
		return RecordResult{}, auditErrorAt(ErrInvalid, "retry")
	}
	if err := ctx.Err(); err != nil {
		return RecordResult{}, err
	}
	if err := r.value.matchReconcile(token.value.reconcile); err != nil {
		return RecordResult{}, err
	}
	if token.value.state == PendingUnknown {
		lookup, err := r.Lookup(ctx, token.value.reconcile)
		if err != nil {
			return RecordResult{}, err
		}
		if receipt, found := lookup.Receipt(); found {
			return RecordResult{value: recordResult{receipt: receipt}}, nil
		}
	}
	result, err := r.value.writer.Append(valueFreeContext{Context: ctx}, token.value.request)
	if err != nil {
		mapped := mapAppendError(err)
		next := retryFor(token.value.request, token.value.reconcile, mapped)
		if next.value.reconcile.valid() {
			return RecordResult{value: recordResult{retry: next}}, &retryCarrier{token: next, err: mapped}
		}
		return RecordResult{}, mapped
	}
	if err := validateAppendResult(token.value.request, result); err != nil {
		return RecordResult{}, auditErrorAt(ErrIntegrity, "append_result")
	}
	reconcile, err := newReconcileKey(r.value.writer.BackingID(), result.Stored().Revision())
	if err != nil {
		return RecordResult{}, err
	}
	receipt := receiptFromAppend(result, Committed, reconcile)
	return RecordResult{value: recordResult{receipt: receipt}}, nil
}

func validateAppendResult(request AppendRequest, result AppendResult) error {
	requestView := request.View()
	if requestView.Attempt.Chain != (AttemptChainID{}) {
		return validateAttemptAppendResult(requestView, result)
	}
	candidate := requestView.Revision.View().Header
	actual := result.Stored().Revision()
	if actual.Log != candidate.Log || actual.Catalog != candidate.Catalog || actual.CatalogSet != candidate.CatalogSet || actual.Deployment != candidate.Deployment || actual.Format != candidate.Format || actual.Operation != candidate.Operation || actual.OperationID != candidate.OperationID || actual.Semantic != candidate.Semantic || actual.Retention != candidate.Retention || actual.Consequence != candidate.Consequence || !reflect.DeepEqual(actual.Authorization, candidate.Authorization) {
		return auditErrorAt(ErrMalformedEvidence, "append_result")
	}
	if actual.RevisionID == candidate.RevisionID {
		if result.Stored().Intent() != requestView.Intent || !reflect.DeepEqual(actual, candidate) {
			return auditErrorAt(ErrMalformedEvidence, "append_result")
		}
		return nil
	}
	if result.Disposition() != Replayed || !candidate.HasIdempotency || !actual.HasIdempotency || actual.Idempotency != candidate.Idempotency {
		return auditErrorAt(ErrMalformedEvidence, "append_result")
	}
	return nil
}

func validateAttemptAppendResult(request AppendRequestView, result AppendResult) error {
	revision := request.Revision.View()
	candidate := revision.Header
	actual := result.Stored().Revision()
	transition, present := result.AttemptTransition()
	projection, projected := result.AttemptProjection()
	if len(revision.Items) != 1 || len(request.Attempts) != 1 || !present || !projected {
		return auditErrorAt(ErrMalformedEvidence, "attempt.append_result")
	}
	expected := revision.Items[0].Attempt
	if actual.RevisionID == candidate.RevisionID {
		if actual.Log != candidate.Log || actual.Catalog != candidate.Catalog || actual.CatalogSet != candidate.CatalogSet || actual.Deployment != candidate.Deployment || !reflect.DeepEqual(actual, candidate) || result.Stored().Intent() != request.Intent || transition != expected || projection != request.Attempt.Candidate.Result {
			return auditErrorAt(ErrMalformedEvidence, "attempt.append_result")
		}
		return nil
	}
	if result.Disposition() != Replayed || actual.Log != candidate.Log || actual.Catalog.ID != candidate.Catalog.ID || actual.Catalog.Generation == 0 || actual.Catalog.Generation > candidate.Catalog.Generation || actual.Deployment != candidate.Deployment || actual.Format != candidate.Format || actual.Operation != candidate.Operation || actual.OperationID != candidate.OperationID || actual.Semantic != candidate.Semantic || actual.Retention != candidate.Retention || actual.Consequence != candidate.Consequence || !candidate.HasIdempotency || !actual.HasIdempotency || !attemptIdempotencyAliasContains(request.Idempotency, actual.Idempotency) {
		return auditErrorAt(ErrMalformedEvidence, "attempt.append_result")
	}
	if transition.Chain != request.Attempt.Chain || transition.Policy != expected.Policy || transition.Replay != expected.Replay || transition.Operation != expected.Operation || transition.OperationID != expected.OperationID || transition.Kind != expected.Kind || projection.Chain != transition.Chain || projection.Policy != transition.Policy || projection.Replay != transition.Replay || projection.Operation != transition.Operation || projection.OperationID != transition.OperationID || projection.Leaf == (LeafDigest{}) {
		return auditErrorAt(ErrMalformedEvidence, "attempt.append_result")
	}
	binding := request.Attempts[0]
	if binding.Chain() != projection.Chain || binding.Operation() != projection.Operation || binding.Policy() != projection.Policy || binding.Replay() != projection.Replay || binding.OperationID() != projection.OperationID || binding.TargetPresent() != projection.TargetPresent || binding.ScopePresent() != projection.ScopePresent || !identitySetContains(binding.OwnerCommitments(), projection.Owner) || projection.TargetPresent && !identitySetContains(binding.TargetCommitments(), projection.Target) || projection.ScopePresent && !identitySetContains(binding.ScopeCommitments(), projection.Scope) {
		return auditErrorAt(ErrMalformedEvidence, "attempt.append_result")
	}
	return nil
}

func attemptIdempotencyAliasContains(set IdentityCommitmentSet, token IdempotencyToken) bool {
	if set.Domain() != CommitIdempotency {
		return false
	}
	for _, alias := range set.Aliases() {
		value := alias.Bytes()
		if len(value) == len(token) && bytes.Equal(value, token[:]) {
			return true
		}
	}
	return false
}

func (r *Recorder) Lookup(ctx context.Context, key ReconcileKey) (LookupResult, error) {
	if r == nil || r.value == nil || ctx == nil || !key.valid() {
		return LookupResult{}, auditErrorAt(ErrInvalid, "lookup")
	}
	if err := ctx.Err(); err != nil {
		return LookupResult{}, err
	}
	if err := r.value.matchReconcile(key); err != nil {
		return LookupResult{}, err
	}
	result, err := r.value.writer.Lookup(valueFreeContext{Context: ctx}, newLookupRequest(key))
	if err != nil {
		return LookupResult{}, mapStoreError(err)
	}
	return result, nil
}

type valueFreeContext struct{ context.Context }

func (valueFreeContext) Value(any) any { return nil }

func (r *recorder) matchReconcile(key ReconcileKey) error {
	if key.value.backing != r.writer.BackingID() || key.value.log != r.writer.LogID() || key.value.catalog != r.active.ID {
		return auditErrorAt(ErrWrongStore, "reconcile_key")
	}
	return nil
}

func retryFor(request AppendRequest, key ReconcileKey, err error) RetryToken {
	state := RetryState(0)
	mode := RecoveryMode(0)
	switch {
	case errors.Is(err, ErrNotWritten):
		state, mode = CertainlyNotWritten, RetryStandalone
	case errors.Is(err, ErrUnconfirmed):
		state, mode = PendingUnknown, RetryStandalone
	default:
		return RetryToken{}
	}
	return RetryToken{value: retryToken{state: state, mode: mode, reconcile: key, request: request}}
}

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	outcome, ok := storeOutcomeOf(err)
	if !ok {
		return auditError(ErrBackend, err)
	}
	sentinel := ErrBackend
	switch outcome {
	case Conflict:
		sentinel = ErrConflict
	case Missing:
		sentinel = ErrNotFound
	case Corrupt:
		sentinel = ErrIntegrity
	case NotWritten:
		sentinel = ErrNotWritten
	case Unconfirmed:
		sentinel = ErrUnconfirmed
	case Closed:
		sentinel = ErrClosed
	case BadPosition:
		sentinel = ErrBadPosition
	case StaleCatalog:
		sentinel = ErrStaleCatalog
	case Refused:
		sentinel = ErrRefused
	}
	return auditError(sentinel, err)
}

func mapAppendError(err error) error {
	if err == nil {
		return nil
	}
	if _, classified := storeOutcomeOf(err); classified {
		return mapStoreError(err)
	}
	return auditError(ErrUnconfirmed, err)
}

func canonicalTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return time.Unix(0, value.UnixNano()).UTC()
}

func authorizationSummary(items []ItemWireView, actors []StoredActorView, facts []StoredContextFactView) RevisionAuthorizationSummaryView {
	resources := make([]Resource, 0, len(items))
	actions := make([]Action, 0, len(items))
	classes := make([]Classification, 0, len(items)*2+len(actors)+len(facts))
	for _, item := range items {
		resources = append(resources, item.Resource)
		actions = append(actions, item.Action)
		if item.Subject.Classification.Valid() {
			classes = append(classes, item.Subject.Classification)
		}
		if item.Target.Classification.Valid() {
			classes = append(classes, item.Target.Classification)
		}
		for _, value := range item.Values {
			classes = append(classes, value.Classification)
		}
		for _, change := range item.Changes {
			classes = append(classes, change.Before.Classification, change.After.Classification)
		}
	}
	for _, actor := range actors {
		classes = append(classes, actor.Classification)
	}
	for _, fact := range facts {
		classes = append(classes, fact.Classification)
	}
	slices.Sort(resources)
	resources = slices.Compact(resources)
	slices.Sort(actions)
	actions = slices.Compact(actions)
	slices.Sort(classes)
	classes = slices.Compact(classes)
	return RevisionAuthorizationSummaryView{Resources: resources, Actions: actions, Classifications: classes}
}

func materializeContext(ctx context.Context, tools materializers, header RevisionHeaderView, resolved Context, policy ContextPolicy) ([]StoredActorView, []StoredContextFactView, error) {
	actors := make([]StoredActorView, len(resolved.Actors))
	actorPolicy, actorsDeclared := contextPolicyFact(policy, ActorChainContext)
	if len(actors) > 0 && !actorsDeclared {
		return nil, nil, auditErrorAt(ErrInvalid, "context.actors")
	}
	for index, actor := range resolved.Actors {
		value := draftValue{codec: ReferenceText().Description(), classification: actorPolicy.classification, mode: actorPolicy.mode, state: ValuePresent, canonical: []byte(actor.Reference)}
		stored, err := tools.value(ctx, value, materializationAAD(header, uint16(index), "", "actor"))
		if err != nil {
			return nil, nil, err
		}
		actors[index] = StoredActorView{Ordinal: uint8(index), Kind: actor.Kind, Provenance: actor.Provenance, Classification: actorPolicy.classification, Mode: actorPolicy.mode, Reference: stored}
	}
	facts := make([]StoredContextFactView, 0, len(policy.value.facts))
	for _, factPolicy := range policy.value.facts {
		if factPolicy.kind == ActorChainContext {
			continue
		}
		present, provenance, raw := contextFactValue(resolved, factPolicy.kind)
		if !present {
			continue
		}
		encoded, err := contextValueBytes(raw)
		if err != nil {
			return nil, nil, err
		}
		value := draftValue{classification: factPolicy.classification, mode: factPolicy.mode, state: ValuePresent, canonical: encoded}
		stored, err := tools.value(ctx, value, materializationAAD(header, uint16(factPolicy.kind), "", "context"))
		if err != nil {
			return nil, nil, err
		}
		facts = append(facts, StoredContextFactView{
			Kind: factPolicy.kind, Provenance: provenance, Classification: factPolicy.classification,
			Mode: factPolicy.mode, Plaintext: bytes.Clone(stored.Plaintext), Redacted: stored.Redacted,
			Token: stored.Token, Protected: stored.Protected,
		})
	}
	return actors, facts, nil
}

func (r *recorder) observe(started time.Time, kind ObservationKind, phase ObservationPhase, failure FailureClass, calls uint32, items uint16, disposition AppendDisposition, settlement Settlement) {
	if nilByReflection(r.observer) {
		return
	}
	observe(r.observer, AuditObservation{
		Phase: phase, Kind: kind, Items: items,
		Duration: time.Since(started), Disposition: disposition, Settlement: settlement,
		Failure: failure, Work: AuditWorkView{StoreCalls: calls},
	})
}

func failureClassOf(err error) FailureClass {
	switch {
	case err == nil:
		return NoFailure
	case errors.Is(err, ErrDenied):
		return DeniedFailure
	case errors.Is(err, ErrConflict):
		return ConflictFailure
	case errors.Is(err, ErrNotWritten):
		return NotWrittenFailure
	case errors.Is(err, ErrUnconfirmed):
		return UnconfirmedFailure
	case errors.Is(err, ErrIntegrity), errors.Is(err, ErrMalformedEvidence):
		return UnreadableFailure
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrDeclaration), errors.Is(err, ErrAdmission):
		return InvalidFailure
	default:
		return BackendFailure
	}
}

func (b Backing) valueValid() bool {
	return comparableAuditIdentity(b.value.identity)
}

func (r *Recorder) CheckAtomicSource(source any) error {
	if r == nil || r.value == nil || nilByReflection(source) {
		return auditErrorAt(ErrInvalid, "source")
	}
	want := r.value.writer.TransactionSource()
	if nilByReflection(want) || !crud.SameDataSource(crud.KeyOf(want), crud.KeyOf(source)) {
		return auditErrorAt(ErrWrongStore, "source")
	}
	if r.value.writer.Capabilities().View().CrossSystemAtomic != SupportSupported {
		return auditErrorAt(ErrUnsupported, "cross_system_atomic")
	}
	return nil
}

func (r *Recorder) String() string {
	if r == nil || r.value == nil {
		return "[invalid audit recorder]"
	}
	return fmt.Sprintf("[audit recorder %s]", r.value.active.ID)
}
