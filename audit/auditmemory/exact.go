package auditmemory

import (
	"context"
	"errors"
	"slices"

	"github.com/frostgrove/vv/audit"
)

func (s *Store) Inspect(ctx context.Context, query audit.ExactQuery) (audit.ExactResult, error) {
	if ctx == nil {
		return audit.ExactResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: exact context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return audit.ExactResult{}, err
	}
	if s == nil || s.log == nil {
		return audit.ExactResult{}, audit.Failure(audit.Closed, errors.New("auditmemory: store is closed"))
	}
	view := query.View()
	limits := s.log.limits.View()
	if view.Log != s.log.logID || len(view.Targets) == 0 || uint32(len(view.Targets)) > limits.ExactTargets || view.MaxBytes == 0 || view.MaxBytes > limits.ExactBytes || len(view.Catalogs) == 0 || len(view.Resources) == 0 || len(view.Actions) == 0 || len(view.Classifications) == 0 {
		return audit.ExactResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: exact query is invalid"))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return audit.ExactResult{}, audit.Failure(audit.Closed, errors.New("auditmemory: store is closed"))
	}
	s.log.mu.RLock()
	defer s.log.mu.RUnlock()
	for _, catalog := range view.Catalogs {
		if _, installed := s.log.manifests[catalog]; !installed {
			return audit.ExactResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: exact catalog is not installed"))
		}
	}
	entries := make([]audit.ExactEntryData, len(view.Targets))
	var total uint64
	for index, target := range view.Targets {
		if err := ctx.Err(); err != nil {
			return audit.ExactResult{}, err
		}
		entries[index] = audit.ExactEntryData{Target: target, State: audit.ExactMissing}
		reference := memoryExactRevision(target)
		record, found := s.log.revisions[reference.Revision]
		if !found {
			continue
		}
		revision := record.revision.View()
		if !memoryExactCovered(view, target, reference, revision) {
			continue
		}
		stored, err := audit.NewStoredRevision(audit.StoredRevisionData{
			Revision: revision, RecordedAt: record.stored.RecordedAt(), Position: record.stored.Position(),
		})
		if err != nil {
			return audit.ExactResult{}, audit.Failure(audit.Corrupt, err)
		}
		if stored.EncodedBytes() > view.MaxBytes-total {
			return audit.ExactResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: exact byte limit cannot hold the requested evidence"))
		}
		total += stored.EncodedBytes()
		entries[index].State = audit.ExactFound
		entries[index].Revision = stored
	}
	if err := ctx.Err(); err != nil {
		return audit.ExactResult{}, err
	}
	result, err := audit.NewExactResult(query, audit.ExactResultData{Entries: entries})
	if err != nil {
		return audit.ExactResult{}, audit.Failure(audit.Corrupt, err)
	}
	return result, nil
}

func memoryExactRevision(target audit.ExactTargetView) audit.RevisionRef {
	if target.Kind == audit.ExactItemTarget {
		return target.Item.Revision
	}
	return target.Revision
}

func memoryExactCovered(view audit.ExactQueryView, target audit.ExactTargetView, reference audit.RevisionRef, revision audit.RevisionWireView) bool {
	header := revision.Header
	if header.Log != view.Log || header.Catalog != reference.Catalog || header.RevisionID != reference.Revision || !matchesHistoryScope(view.Coordinates, revision.Context) {
		return false
	}
	if target.Kind == audit.ExactRevisionTarget {
		return historySubset(header.Authorization.Resources, view.Resources) && historySubset(header.Authorization.Actions, view.Actions) && historySubset(header.Authorization.Classifications, view.Classifications)
	}
	if target.Kind != audit.ExactItemTarget || int(target.Item.Ordinal) >= len(revision.Items) || revision.Items[target.Item.Ordinal].Ordinal != target.Item.Ordinal {
		return false
	}
	item := revision.Items[target.Item.Ordinal]
	return slices.Contains(view.Resources, item.Resource) && slices.Contains(view.Actions, item.Action) && memoryExactItemClasses(item, view.Classifications)
}

func memoryExactItemClasses(item audit.ItemWireView, ceiling []audit.Classification) bool {
	classes := make([]audit.Classification, 0, 2+len(item.Values)+len(item.Changes)*2)
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
	for _, class := range classes {
		if !class.Valid() || !slices.Contains(ceiling, class) {
			return false
		}
	}
	return true
}
