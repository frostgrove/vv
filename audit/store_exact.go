package audit

import (
	"context"
	"slices"
)

type ExactLog interface {
	StoreInfo
	Inspect(context.Context, ExactQuery) (ExactResult, error)
}

type ExactTargetKind uint8

const (
	ExactRevisionTarget ExactTargetKind = iota + 1
	ExactItemTarget
)

type ExactTargetView struct {
	Kind     ExactTargetKind
	Revision RevisionRef
	Item     ItemRef
}

type ExactQueryView struct {
	Log             LogID
	Catalogs        []CatalogRef
	Targets         []ExactTargetView
	Resources       []Resource
	Actions         []Action
	Classifications []Classification
	Coordinates     []QueryCoordinateView
	MaxBytes        uint64
}

type exactQueryOrigin struct{ marker byte }

type exactQuery struct {
	view   ExactQueryView
	origin *exactQueryOrigin
}

type ExactQuery struct {
	value exactQuery
}

func (q ExactQuery) View() ExactQueryView {
	return cloneExactQueryView(q.value.view)
}

func newExactQuery(view ExactQueryView) (ExactQuery, error) {
	query := ExactQuery{value: exactQuery{view: cloneExactQueryView(view), origin: &exactQueryOrigin{}}}
	if err := validateExactQuery(query); err != nil {
		return ExactQuery{}, err
	}
	return query, nil
}

func cloneExactQueryView(view ExactQueryView) ExactQueryView {
	view.Catalogs = slices.Clone(view.Catalogs)
	view.Targets = slices.Clone(view.Targets)
	view.Resources = slices.Clone(view.Resources)
	view.Actions = slices.Clone(view.Actions)
	view.Classifications = slices.Clone(view.Classifications)
	view.Coordinates = cloneCoordinates(view.Coordinates)
	return view
}

type ExactEntryState uint8

const (
	ExactFound ExactEntryState = iota + 1
	ExactMissing
)

type ExactEntryData struct {
	Target   ExactTargetView
	State    ExactEntryState
	Revision StoredRevision
}

type ExactResultData struct {
	Entries []ExactEntryData
}

type exactResult struct {
	data   ExactResultData
	origin *exactQueryOrigin
}

type ExactResult struct {
	value exactResult
}

func NewExactResult(query ExactQuery, data ExactResultData) (ExactResult, error) {
	result := ExactResult{value: exactResult{data: cloneExactResultData(data), origin: query.value.origin}}
	if err := validateExactResult(query, result); err != nil {
		return ExactResult{}, err
	}
	return result, nil
}

func (r ExactResult) Entries() []ExactEntryData {
	return cloneExactResultData(r.value.data).Entries
}

func cloneExactResultData(data ExactResultData) ExactResultData {
	data.Entries = slices.Clone(data.Entries)
	for index := range data.Entries {
		stored := data.Entries[index].Revision
		if stored.valid() {
			data.Entries[index].Revision = cloneStoredRevisions([]StoredRevision{stored})[0]
		}
	}
	return data
}

func validateExactQuery(query ExactQuery) error {
	if query.value.origin == nil {
		return auditErrorAt(ErrIntegrity, "exact.query")
	}
	view := query.value.view
	if view.Log == (LogID{}) || len(view.Catalogs) == 0 || len(view.Catalogs) > MaxCatalogs || len(view.Targets) == 0 || len(view.Targets) > MaxExactTargets || view.MaxBytes == 0 || view.MaxBytes > MaxPageBytes {
		return auditErrorAt(ErrInvalid, "exact.query")
	}
	if !validExactNames(view.Resources, MaxQueryResources) || !validExactNames(view.Actions, MaxQueryActions) || !validExactClassifications(view.Classifications) || !validExactCatalogs(view.Catalogs) || !validExactCoordinates(view.Coordinates) {
		return auditErrorAt(ErrInvalid, "exact.query")
	}
	seen := make(map[ExactTargetView]struct{}, len(view.Targets))
	for _, target := range view.Targets {
		if !validExactTarget(target) || !slices.Contains(view.Catalogs, exactTargetRevision(target).Catalog) {
			return auditErrorAt(ErrInvalid, "exact.targets")
		}
		if _, duplicate := seen[target]; duplicate {
			return auditErrorAt(ErrInvalid, "exact.targets")
		}
		seen[target] = struct{}{}
	}
	return nil
}

func validateExactResult(query ExactQuery, result ExactResult) error {
	if err := validateExactQuery(query); err != nil || result.value.origin != query.value.origin {
		return auditErrorAt(ErrIntegrity, "exact.result")
	}
	view := query.value.view
	entries := result.value.data.Entries
	if len(entries) != len(view.Targets) {
		return auditErrorAt(ErrIntegrity, "exact.result")
	}
	var total uint64
	for index, entry := range entries {
		if entry.Target != view.Targets[index] || entry.State != ExactFound && entry.State != ExactMissing {
			return auditErrorAt(ErrIntegrity, "exact.result")
		}
		if entry.State == ExactMissing {
			if entry.Revision.valid() {
				return auditErrorAt(ErrIntegrity, "exact.result")
			}
			continue
		}
		if !entry.Revision.valid() || !exactStoredRevisionMatches(view, entry.Target, entry.Revision.View()) {
			return auditErrorAt(ErrIntegrity, "exact.result")
		}
		if entry.Revision.EncodedBytes() > view.MaxBytes-total {
			return auditErrorAt(ErrIntegrity, "exact.result")
		}
		total += entry.Revision.EncodedBytes()
	}
	return nil
}

func validExactTarget(target ExactTargetView) bool {
	switch target.Kind {
	case ExactRevisionTarget:
		return target.Revision.valid() && target.Item == (ItemRef{})
	case ExactItemTarget:
		return target.Revision == (RevisionRef{}) && target.Item.valid()
	default:
		return false
	}
}

func exactTargetRevision(target ExactTargetView) RevisionRef {
	if target.Kind == ExactItemTarget {
		return target.Item.Revision
	}
	return target.Revision
}

func exactStoredRevisionMatches(query ExactQueryView, target ExactTargetView, stored StoredRevisionView) bool {
	view := stored.Revision
	reference := exactTargetRevision(target)
	if view.Header.Log != query.Log || view.Header.Catalog != reference.Catalog || view.Header.RevisionID != reference.Revision {
		return false
	}
	if !matchesScope(query.Coordinates, view.Context) {
		return false
	}
	if target.Kind == ExactRevisionTarget {
		return subset(view.Header.Authorization.Resources, query.Resources) && subset(view.Header.Authorization.Actions, query.Actions) && subset(view.Header.Authorization.Classifications, query.Classifications)
	}
	if int(target.Item.Ordinal) >= len(view.Items) || view.Items[target.Item.Ordinal].Ordinal != target.Item.Ordinal {
		return false
	}
	item := view.Items[target.Item.Ordinal]
	return slices.Contains(query.Resources, item.Resource) && slices.Contains(query.Actions, item.Action) && exactItemClassificationsCovered(item, query.Classifications)
}

func exactItemClassificationsCovered(item ItemWireView, ceiling []Classification) bool {
	values := make([]Classification, 0, 2+len(item.Values)+len(item.Changes)*2)
	if item.Subject.Classification.Valid() {
		values = append(values, item.Subject.Classification)
	}
	if item.Target.Classification.Valid() {
		values = append(values, item.Target.Classification)
	}
	for _, value := range item.Values {
		values = append(values, value.Classification)
	}
	for _, change := range item.Changes {
		values = append(values, change.Before.Classification, change.After.Classification)
	}
	for _, value := range values {
		if !value.Valid() || !slices.Contains(ceiling, value) {
			return false
		}
	}
	return true
}

func validExactCatalogs(values []CatalogRef) bool {
	seen := make(map[CatalogRef]struct{}, len(values))
	for _, value := range values {
		if !value.valid() {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validExactNames[T ~string](values []T, limit int) bool {
	if len(values) == 0 || len(values) > limit {
		return false
	}
	seen := make(map[T]struct{}, len(values))
	for _, value := range values {
		if !validSemanticName(string(value)) {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validExactClassifications(values []Classification) bool {
	if len(values) == 0 || len(values) > MaxQueryClassifications {
		return false
	}
	seen := make(map[Classification]struct{}, len(values))
	for _, value := range values {
		if !value.Valid() {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validExactCoordinates(values []QueryCoordinateView) bool {
	if len(values) > MaxQueryCoordinates {
		return false
	}
	seen := make(map[QueryCoordinateKind]struct{}, len(values))
	for _, value := range values {
		if value.Kind != QueryScope || value.Match != QueryCoordinateExact || value.Mode != AsPlaintext || value.Classification != 0 || value.Resource != "" || value.Action != "" || value.Field != "" || value.ActorPosition != 0 || value.ActorKind != 0 || value.ContextKind != 0 || value.EntitySide != 0 || len(value.Alternatives) != 1 {
			return false
		}
		alternative := value.Alternatives[0]
		if len(alternative.Plaintext) == 0 || len(alternative.Plaintext) > MaxReferenceBytes*2+1 || len(alternative.Tokens) != 0 || alternative.Commitments.Domain() != 0 || len(alternative.Commitments.Aliases()) != 0 {
			return false
		}
		if _, duplicate := seen[value.Kind]; duplicate {
			return false
		}
		seen[value.Kind] = struct{}{}
	}
	return true
}
