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
	activeManifest, found := h.value.runtime.recorder.value.catalogs.Manifest(h.value.runtime.recorder.value.catalogs.Active())
	if !found || !validManifest(activeManifest) {
		return StoredRevision{}, AccessGrantSpec{}, ExactQueryView{}, auditErrorAt(ErrWrongCatalog, "history.catalog")
	}
	if _, err := currentExactCeiling(activeManifest.View().Declarations, grant.Resources, grant.Actions, grant.Classifications); err != nil {
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
	if err := validateCurrentExactEvidence(activeManifest.View().Declarations, exactTarget, grant, entries[0].Revision.View().Revision); err != nil {
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
	retained, found := catalogs.Manifest(exactTargetRevision(exact).Catalog)
	if !found || !validManifest(retained) {
		return historyTarget{}, NormalizedQueryView{}, auditErrorAt(ErrWrongCatalog, "history.catalog")
	}
	mergeRetainedExactScope(&ceiling, retained.View().Declarations, resources, actions)
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

type exactResourceAction struct {
	resource Resource
	action   Action
}

func currentExactCeiling(declarations []DeclarationDescription, resources []Resource, actions []Action, classifications []Classification) (exactCeiling, error) {
	matchedResources := make(map[Resource]struct{}, len(resources))
	matchedActions := make(map[Action]struct{}, len(actions))
	allowedClassifications := make(map[Classification]struct{})
	fieldCoverage := make(map[FieldName]int)
	contextCoverage := make(map[ContextFactKind]int)
	applicable := 0
	var ceiling exactCeiling
	for _, declaration := range declarations {
		if !slices.Contains(resources, declaration.Resource) {
			continue
		}
		for _, action := range exactDeclarationActions(declaration) {
			if !slices.Contains(actions, action) {
				continue
			}
			applicable++
			matchedResources[declaration.Resource] = struct{}{}
			matchedActions[action] = struct{}{}
			if coordinate, present := exactDeclarationSubject(declaration, action); present {
				allowedClassifications[coordinate.Classification] = struct{}{}
			}
			if coordinate, present := exactDeclarationTarget(declaration, action); present {
				allowedClassifications[coordinate.Classification] = struct{}{}
			}
			for _, field := range exactDeclarationFields(declaration, action) {
				fieldCoverage[field.Name]++
				allowedClassifications[field.Classification] = struct{}{}
			}
			for _, fact := range declaration.Context.Facts {
				contextCoverage[fact.Kind]++
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
	}
	if applicable == 0 || len(matchedResources) != len(resources) || len(matchedActions) != len(actions) {
		return exactCeiling{}, auditErrorAt(ErrRefused, "history.exact_ceiling")
	}
	for _, classification := range classifications {
		if _, allowed := allowedClassifications[classification]; !allowed {
			return exactCeiling{}, auditErrorAt(ErrRefused, "history.exact_ceiling")
		}
	}
	for field, count := range fieldCoverage {
		if count == applicable {
			ceiling.fields = append(ceiling.fields, field)
		}
	}
	for fact, count := range contextCoverage {
		if count == applicable {
			ceiling.context = append(ceiling.context, fact)
		}
	}
	slices.Sort(ceiling.fields)
	slices.Sort(ceiling.context)
	return ceiling, nil
}

func mergeRetainedExactScope(ceiling *exactCeiling, declarations []DeclarationDescription, resources []Resource, actions []Action) {
	for _, declaration := range declarations {
		if !slices.Contains(resources, declaration.Resource) {
			continue
		}
		for _, action := range exactDeclarationActions(declaration) {
			if !slices.Contains(actions, action) {
				continue
			}
			for _, fact := range declaration.Context.Facts {
				if fact.Kind != ScopeContext {
					continue
				}
				if !ceiling.scopeDeclared {
					ceiling.scopeDeclared = true
					ceiling.scopeMode = fact.Mode
				} else if ceiling.scopeMode != fact.Mode {
					ceiling.scopeMode = AsProtected
				}
			}
		}
	}
}

func validateCurrentExactEvidence(declarations []DeclarationDescription, target ExactTargetView, grant AccessGrantSpec, revision RevisionWireView) error {
	allowed := make(map[exactResourceAction]exactPairPolicy)
	for _, declaration := range declarations {
		if !slices.Contains(grant.Resources, declaration.Resource) {
			continue
		}
		for _, action := range exactDeclarationActions(declaration) {
			if slices.Contains(grant.Actions, action) {
				allowed[exactResourceAction{resource: declaration.Resource, action: action}] = exactPairPolicy{declaration: declaration, action: action}
			}
		}
	}
	items := revision.Items
	if target.Kind == ExactItemTarget {
		if int(target.Item.Ordinal) >= len(items) || items[target.Item.Ordinal].Ordinal != target.Item.Ordinal {
			return auditErrorAt(ErrIntegrity, "history.exact_item")
		}
		items = items[target.Item.Ordinal : target.Item.Ordinal+1]
	}
	policies := make([]exactPairPolicy, 0, len(items))
	for _, item := range items {
		if !slices.Contains(grant.Resources, item.Resource) || !slices.Contains(grant.Actions, item.Action) {
			continue
		}
		policy, current := allowed[exactResourceAction{resource: item.Resource, action: item.Action}]
		if !current || !exactItemKindMatches(policy.declaration.Kind, item.Kind) {
			return auditErrorAt(ErrRefused, "history.exact_pair")
		}
		if err := validateCurrentExactItem(policy, item, grant); err != nil {
			return err
		}
		policies = append(policies, policy)
	}
	for _, fact := range revision.Context {
		if !slices.Contains(grant.Context, fact.Kind) || !slices.Contains(grant.Classifications, fact.Classification) {
			continue
		}
		for _, policy := range policies {
			current, found := exactContextFact(policy.declaration.Context, fact.Kind)
			if !found || current.Classification != fact.Classification || current.Mode != fact.Mode || !slices.Contains(current.Allowed, fact.Provenance) {
				return auditErrorAt(ErrRefused, "history.exact_context")
			}
		}
	}
	if slices.Contains(grant.Context, ActorChainContext) {
		for _, actor := range revision.Actors {
			if !slices.Contains(grant.Classifications, actor.Classification) {
				continue
			}
			for _, policy := range policies {
				current, found := exactContextFact(policy.declaration.Context, ActorChainContext)
				if !found || current.Classification != actor.Classification || current.Mode != actor.Mode || !slices.Contains(current.Allowed, actor.Provenance) {
					return auditErrorAt(ErrRefused, "history.exact_actors")
				}
			}
		}
	}
	return nil
}

type exactPairPolicy struct {
	declaration DeclarationDescription
	action      Action
}

func validateCurrentExactItem(policy exactPairPolicy, item ItemWireView, grant AccessGrantSpec) error {
	if storedValueProjected(item.Subject, grant) {
		current, present := exactDeclarationSubject(policy.declaration, policy.action)
		if !present || current.Classification != item.Subject.Classification || current.Mode != item.Subject.Mode {
			return auditErrorAt(ErrRefused, "history.exact_subject")
		}
	}
	if storedValueProjected(item.Target, grant) {
		current, present := exactDeclarationTarget(policy.declaration, policy.action)
		if !present || current.Classification != item.Target.Classification || current.Mode != item.Target.Mode {
			return auditErrorAt(ErrRefused, "history.exact_target")
		}
	}
	fields := exactDeclarationFields(policy.declaration, policy.action)
	for _, value := range item.Values {
		if storedFieldProjected(value, grant) && !exactFieldAllows(fields, value) {
			return auditErrorAt(ErrRefused, "history.exact_field")
		}
	}
	for _, change := range item.Changes {
		if storedFieldProjected(change.Before, grant) && !exactFieldAllows(fields, change.Before) || storedFieldProjected(change.After, grant) && !exactFieldAllows(fields, change.After) {
			return auditErrorAt(ErrRefused, "history.exact_field")
		}
	}
	return nil
}

func storedValueProjected(value StoredValueView, grant AccessGrantSpec) bool {
	return value.Classification.Valid() && slices.Contains(grant.Classifications, value.Classification)
}

func storedFieldProjected(value StoredValueView, grant AccessGrantSpec) bool {
	return slices.Contains(grant.Fields, value.Field) && storedValueProjected(value, grant)
}

func exactFieldAllows(fields []FieldDescription, stored StoredValueView) bool {
	for _, field := range fields {
		if field.Name == stored.Field && field.Classification == stored.Classification && field.Mode == stored.Mode && field.Codec.Name == stored.Codec.Name && slices.Contains(field.Codec.ReadVersions, stored.Codec.WriteVersion) {
			return true
		}
	}
	return false
}

func exactContextFact(policy ContextPolicyDescription, kind ContextFactKind) (ContextFactDescription, bool) {
	for _, fact := range policy.Facts {
		if fact.Kind == kind {
			return fact, true
		}
	}
	return ContextFactDescription{}, false
}

func exactItemKindMatches(declaration DeclarationKind, item ItemKind) bool {
	return declaration == ResourceDeclaration && item == EntityItem || declaration == EventDeclaration && item == EventItem || declaration == AttemptDeclaration && item == AttemptItem
}

func exactDeclarationActions(declaration DeclarationDescription) []Action {
	if declaration.Kind == AttemptDeclaration {
		return attemptActions()
	}
	if declaration.Action != "" {
		return []Action{declaration.Action}
	}
	result := make([]Action, len(declaration.Actions))
	for index, action := range declaration.Actions {
		result[index] = Action(action)
	}
	return result
}

func exactDeclarationSubject(declaration DeclarationDescription, _ Action) (SubjectDescription, bool) {
	if declaration.Kind == ResourceDeclaration && declaration.Subject.Classification.Valid() {
		return declaration.Subject, true
	}
	return SubjectDescription{}, false
}

func exactDeclarationTarget(declaration DeclarationDescription, action Action) (SubjectDescription, bool) {
	if declaration.Kind == EventDeclaration && declaration.TargetPresent {
		return declaration.Target, true
	}
	if declaration.Kind == AttemptDeclaration && action == AttemptStartedAction && declaration.Attempt.Start.TargetPresent {
		return declaration.Attempt.Start.Target, true
	}
	return SubjectDescription{}, false
}

func exactDeclarationFields(declaration DeclarationDescription, action Action) []FieldDescription {
	if declaration.Kind != AttemptDeclaration {
		return declaration.Fields
	}
	switch action {
	case AttemptStartedAction:
		return declaration.Attempt.Start.Fields
	case AttemptCheckpointAction:
		return declaration.Attempt.Checkpoint.Fields
	}
	transition := AttemptTransitionKind(0)
	switch action {
	case AttemptSucceededAction:
		transition = AttemptSucceededTransition
	case AttemptFailedAction:
		transition = AttemptFailedTransition
	case AttemptCancelledAction:
		transition = AttemptCancelledTransition
	}
	for _, phase := range declaration.Attempt.Finish {
		if phase.Transition == transition {
			return phase.Fields
		}
	}
	return nil
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
