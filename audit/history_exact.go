package audit

import (
	"context"
	"errors"
	"slices"
)

type ExactAccessQuery struct {
	Resources       []Resource
	Classifications []Classification
	Query           Query
}

type revisionResult struct{ revision RevisionView }
type RevisionResult struct{ value revisionResult }

func (r RevisionResult) Revision() RevisionView { return cloneRevisionRead(r.value.revision) }

type itemResult struct{ item ItemReadView }
type ItemResult struct{ value itemResult }

func (r ItemResult) Item() ItemReadView { return cloneItemRead(r.value.item) }

func (h *History) Revision(ctx context.Context, reference RevisionRef, input ExactAccessQuery) (RevisionResult, error) {
	target := ExactTargetView{Kind: ExactRevisionTarget, Revision: reference}
	stored, grant, query, err := h.inspectExact(ctx, target, input)
	if err != nil {
		return RevisionResult{}, err
	}
	read := revisionRead(stored.View(), grant, exactProjectionQuery(query))
	return RevisionResult{value: revisionResult{revision: read}}, nil
}

func (h *History) Item(ctx context.Context, reference ItemRef, input ExactAccessQuery) (ItemResult, error) {
	target := ExactTargetView{Kind: ExactItemTarget, Item: reference}
	stored, grant, query, err := h.inspectExact(ctx, target, input)
	if err != nil {
		return ItemResult{}, err
	}
	read := revisionRead(stored.View(), grant, exactProjectionQuery(query))
	for _, item := range read.Items {
		if item.Ordinal == reference.Ordinal {
			return ItemResult{value: itemResult{item: item}}, nil
		}
	}
	return ItemResult{}, auditErrorAt(ErrIntegrity, "history.item")
}

func validateHistoryExact(recorder *Recorder, exact ExactLog) error {
	if nilByReflection(exact) {
		return nil
	}
	writer := recorder.value.writer
	if exact.BackingID() != writer.BackingID() || exact.LogID() != writer.LogID() || !SameBacking(exact.Backing(), writer.Backing()) {
		return auditErrorAt(ErrWrongStore, "history.exact")
	}
	state := exact.Catalogs()
	if !state.HasActive() || state.Active() != recorder.value.active || state.SetDigest() != recorder.value.set {
		return auditErrorAt(ErrWrongCatalog, "history.exact")
	}
	if exact.Capabilities().View().ExactInspection != SupportSupported {
		return auditErrorAt(ErrUnsupported, "history.exact")
	}
	limits := exact.Limits().View()
	if limits.ExactTargets == 0 || limits.ExactTargets > MaxExactTargets || limits.ExactBytes == 0 || limits.ExactBytes > MaxPageBytes || limits.ExactBytes < limits.RevisionBytes {
		return auditErrorAt(ErrWrongStore, "history.exact_limits")
	}
	return nil
}

func (h *History) inspectExact(ctx context.Context, exactTarget ExactTargetView, input ExactAccessQuery) (StoredRevision, AccessGrantSpec, ExactQueryView, error) {
	if h == nil || h.value.runtime == nil || ctx == nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, auditErrorAt(ErrInvalid, "history.exact")
	}
	if nilByReflection(h.value.runtime.exact) {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, auditErrorAt(ErrUnsupported, "history.exact")
	}
	if !validExactTarget(exactTarget) {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, auditErrorAt(ErrInvalid, "history.exact_target")
	}
	if err := ctx.Err(); err != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, err
	}
	if err := h.validateExactRuntime(ctx); err != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, err
	}
	reference := exactTargetRevision(exactTarget)
	if _, found := h.value.runtime.recorder.value.catalogs.Manifest(reference.Catalog); !found {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, auditErrorAt(ErrRefused, "history.exact_target")
	}
	class := ExactRevisionHistoryQuery
	kind := AccessRevisionTarget
	if exactTarget.Kind == ExactItemTarget {
		class = ExactItemHistoryQuery
		kind = AccessItemTarget
	}
	target, normalized, err := normalizeExactAccess(h.value.runtime.recorder.value.catalogs, kind, exactTarget, class, input)
	if err != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, err
	}
	requester, err := h.resolveRequester(ctx)
	if err != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, err
	}
	if err := resolveQueryScope(&normalized, requester, target); err != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, err
	}
	request := newAccessRequest(AccessRequestView{
		Intent: HistoryDisclosureAccess, Requester: requester.View(), Target: target.view,
		Query: normalized, Catalogs: catalogRefs(h.value.runtime.recorder.value.catalogs),
	})
	runtimeContext := valueFreeContext{Context: ctx}
	decision, err := h.value.runtime.access.AuthorizeAudit(runtimeContext, request)
	if contextErr := ctx.Err(); contextErr != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, contextErr
	}
	if err != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, auditError(ErrDenied, err)
	}
	if decision.value.origin != request.value.origin || decision.value.view.Verdict != AccessAllowed {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, auditErrorAt(ErrDenied, "history.access")
	}
	grant := decision.View().Grant
	if err := validateBasicGrant(request.value.view, grant); err != nil {
		if errors.Is(err, ErrUnsupported) {
			return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, err
		}
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, auditErrorAt(ErrDenied, "history.access")
	}
	for _, classification := range grant.Classifications {
		if classification != Public {
			return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, auditErrorAt(ErrUnsupported, "history.access_evidence")
		}
	}
	now := canonicalTime(h.value.runtime.recorder.value.clock.Now())
	limits := h.value.runtime.exact.Limits().View()
	if !grant.ExpiresAt.After(now) || grant.MaxRevisions != 1 || grant.MaxBytes > limits.ExactBytes {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, auditErrorAt(ErrDenied, "history.access")
	}
	if err := h.validateExactRuntime(ctx); err != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, err
	}
	query, err := newExactQuery(ExactQueryView{
		Log: h.value.runtime.exact.LogID(), Catalogs: grant.Catalogs, Targets: []ExactTargetView{exactTarget},
		Resources: grant.Resources, Actions: grant.Actions, Classifications: grant.Classifications,
		Coordinates: targetCoordinates(target, normalized.Scope), MaxBytes: grant.MaxBytes,
	})
	if err != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, auditErrorAt(ErrIntegrity, "history.exact_query")
	}
	result, err := h.value.runtime.exact.Inspect(runtimeContext, query)
	if contextErr := ctx.Err(); contextErr != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, contextErr
	}
	if err != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, mapStoreError(err)
	}
	if err := h.validateExactRuntime(ctx); err != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, err
	}
	if err := validateExactResult(query, result); err != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, err
	}
	entries := result.Entries()
	if len(entries) != 1 {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, auditErrorAt(ErrIntegrity, "history.exact_result")
	}
	if entries[0].State == ExactMissing {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, auditErrorAt(ErrNotFound, "history.exact_target")
	}
	if err := verifyHistoryRevisionEvidence(runtimeContext, h.value.runtime.recorder.value.catalogs, h.value.runtime.verifier, entries[0].Revision); err != nil {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, err
	}
	return entries[0].Revision, grant, query.View(), nil
}

func (h *History) validateExactRuntime(ctx context.Context) error {
	if err := h.validateRuntime(ctx); err != nil {
		return err
	}
	return validateHistoryExact(h.value.runtime.recorder, h.value.runtime.exact)
}

func exactProjectionQuery(query ExactQueryView) StoreQueryView {
	return StoreQueryView{Resources: slices.Clone(query.Resources), Actions: slices.Clone(query.Actions), Coordinates: cloneCoordinates(query.Coordinates)}
}

func normalizeExactAccess(catalogs *CatalogSet, kind AccessTargetKind, exact ExactTargetView, class QueryClass, input ExactAccessQuery) (historyTarget, NormalizedQueryView, error) {
	resources, err := canonicalExactNames(input.Resources, MaxQueryResources, "query.resources")
	if err != nil {
		return historyTarget{}, NormalizedQueryView{}, err
	}
	actions, err := canonicalExactNames(input.Query.Actions, MaxQueryActions, "query.actions")
	if err != nil {
		return historyTarget{}, NormalizedQueryView{}, err
	}
	classifications, err := canonicalExactClassifications(input.Classifications)
	if err != nil {
		return historyTarget{}, NormalizedQueryView{}, err
	}
	manifest, found := catalogs.Manifest(catalogs.Active())
	if !found || !validManifest(manifest) {
		return historyTarget{}, NormalizedQueryView{}, auditErrorAt(ErrWrongCatalog, "history.catalog")
	}
	ceiling, err := currentExactCeiling(manifest.View().Declarations, resources, actions, classifications)
	if err != nil {
		return historyTarget{}, NormalizedQueryView{}, err
	}
	query, err := normalizeExactQuery(input.Query, class, actions, ceiling.fields, ceiling.context)
	if err != nil {
		return historyTarget{}, NormalizedQueryView{}, err
	}
	target := historyTarget{
		view: AccessTargetView{
			Kind: kind, Resources: resources, Actions: actions, Classifications: classifications,
			Fields: ceiling.fields, Context: ceiling.context,
		},
		scopeDeclared: ceiling.scopeDeclared,
		scopeMode:     ceiling.scopeMode,
	}
	if exact.Kind == ExactRevisionTarget {
		target.view.Revision = exact.Revision
	} else {
		target.view.Item = exact.Item
	}
	return target, query, nil
}

type exactCeiling struct {
	fields        []FieldName
	context       []ContextFactKind
	scopeDeclared bool
	scopeMode     StorageMode
}

func currentExactCeiling(declarations []DeclarationDescription, resources []Resource, actions []Action, classifications []Classification) (exactCeiling, error) {
	matchedResources := make(map[Resource]struct{}, len(resources))
	matchedActions := make(map[Action]struct{}, len(actions))
	allowedClassifications := make(map[Classification]struct{})
	var ceiling exactCeiling
	for _, declaration := range declarations {
		if !slices.Contains(resources, declaration.Resource) {
			continue
		}
		declarationActions := exactDeclarationActions(declaration)
		matched := false
		for _, action := range declarationActions {
			if !slices.Contains(actions, action) {
				continue
			}
			matched = true
			matchedActions[action] = struct{}{}
		}
		if !matched {
			continue
		}
		matchedResources[declaration.Resource] = struct{}{}
		if declaration.Subject.Classification.Valid() {
			allowedClassifications[declaration.Subject.Classification] = struct{}{}
		}
		if declaration.TargetPresent && declaration.Target.Classification.Valid() {
			allowedClassifications[declaration.Target.Classification] = struct{}{}
		}
		for _, field := range declaration.Fields {
			ceiling.fields = append(ceiling.fields, field.Name)
			allowedClassifications[field.Classification] = struct{}{}
		}
		for _, fact := range declaration.Context.Facts {
			ceiling.context = append(ceiling.context, fact.Kind)
			allowedClassifications[fact.Classification] = struct{}{}
			if fact.Kind == ScopeContext {
				ceiling.scopeDeclared = true
				if ceiling.scopeMode == 0 {
					ceiling.scopeMode = fact.Mode
				} else if ceiling.scopeMode != fact.Mode {
					ceiling.scopeMode = AsProtected
				}
			}
		}
	}
	if len(matchedResources) != len(resources) || len(matchedActions) != len(actions) {
		return exactCeiling{}, auditErrorAt(ErrRefused, "history.exact_ceiling")
	}
	for _, classification := range classifications {
		if _, allowed := allowedClassifications[classification]; !allowed {
			return exactCeiling{}, auditErrorAt(ErrRefused, "history.exact_ceiling")
		}
	}
	slices.Sort(ceiling.fields)
	ceiling.fields = slices.Compact(ceiling.fields)
	slices.Sort(ceiling.context)
	ceiling.context = slices.Compact(ceiling.context)
	return ceiling, nil
}

func exactDeclarationActions(declaration DeclarationDescription) []Action {
	if declaration.Action != "" {
		return []Action{declaration.Action}
	}
	result := make([]Action, len(declaration.Actions))
	for index, action := range declaration.Actions {
		result[index] = Action(action)
	}
	return result
}

func normalizeExactQuery(input Query, class QueryClass, actions []Action, fields []FieldName, facts []ContextFactKind) (NormalizedQueryView, error) {
	if !validSemanticName(string(input.Purpose)) || !validOpaqueReference(string(input.Role), MaxReferenceBytes) {
		return NormalizedQueryView{}, auditErrorAt(ErrInvalid, "query")
	}
	if input.Time.value != (TimeWindowView{}) || input.Changed.value.Match != 0 || len(input.Outcomes) != 0 || len(input.Reasons) != 0 || input.Where.configured || len(input.Cursor.value.wire) != 0 {
		return NormalizedQueryView{}, auditErrorAt(ErrUnsupported, "query")
	}
	if input.Limit > 1 {
		return NormalizedQueryView{}, auditTooLarge("query.limit", 1)
	}
	direction := input.Direction
	if direction == 0 {
		direction = OldestFirst
	}
	if direction != OldestFirst && direction != NewestFirst {
		return NormalizedQueryView{}, auditErrorAt(ErrInvalid, "query.direction")
	}
	scope := input.Scope.View()
	if scope.Kind != 0 && scope.Kind != ScopeCurrent && scope.Kind != ScopeExact {
		return NormalizedQueryView{}, auditErrorAt(ErrInvalid, "query.scope")
	}
	if scope.Kind == ScopeExact && (!validReferenceText(string(scope.Reference.Scope)) || !validReferenceText(string(scope.Reference.Reference))) {
		return NormalizedQueryView{}, auditErrorAt(ErrInvalid, "query.scope")
	}
	fieldProjection, err := normalizeFieldProjection(input.Fields, fields)
	if err != nil {
		return NormalizedQueryView{}, err
	}
	contextProjection, err := normalizeContextProjection(input.Context, facts)
	if err != nil {
		return NormalizedQueryView{}, err
	}
	return NormalizedQueryView{
		Purpose: input.Purpose, Role: input.Role, Scope: scope, Class: class,
		Actions: slices.Clone(actions), Fields: fieldProjection, Context: contextProjection,
		Direction: direction, Limit: 1,
	}, nil
}

func canonicalExactNames[T ~string](input []T, limit int, path string) ([]T, error) {
	if len(input) == 0 {
		return nil, auditErrorAt(ErrInvalid, path)
	}
	if len(input) > limit {
		return nil, auditTooLarge(path, limit)
	}
	result := slices.Clone(input)
	for _, value := range result {
		if !validSemanticName(string(value)) {
			return nil, auditErrorAt(ErrInvalid, path)
		}
	}
	slices.Sort(result)
	if len(slices.Compact(result)) != len(result) {
		return nil, auditErrorAt(ErrInvalid, path)
	}
	return result, nil
}

func canonicalExactClassifications(input []Classification) ([]Classification, error) {
	if len(input) == 0 {
		return nil, auditErrorAt(ErrInvalid, "query.classifications")
	}
	if len(input) > MaxQueryClassifications {
		return nil, auditTooLarge("query.classifications", MaxQueryClassifications)
	}
	result := slices.Clone(input)
	for _, value := range result {
		if !value.Valid() {
			return nil, auditErrorAt(ErrInvalid, "query.classifications")
		}
	}
	slices.Sort(result)
	if len(slices.Compact(result)) != len(result) {
		return nil, auditErrorAt(ErrInvalid, "query.classifications")
	}
	return result, nil
}
