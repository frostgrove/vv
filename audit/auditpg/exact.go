package auditpg

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/frostgrove/vv/audit"
)

type exactReadContext struct{ context.Context }

func (exactReadContext) Value(any) any { return nil }

func (s *Store) Inspect(ctx context.Context, query audit.ExactQuery) (audit.ExactResult, error) {
	if ctx == nil {
		return audit.ExactResult{}, audit.Failure(audit.Refused, errors.New("auditpg: exact context is nil"))
	}
	ready, err := s.ready()
	if err != nil {
		return audit.ExactResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return audit.ExactResult{}, err
	}
	view := query.View()
	limits := s.value.configured.limits.View()
	if err := validateExactPGQuery(view, ready, limits); err != nil {
		return audit.ExactResult{}, err
	}
	runtimeContext := exactReadContext{Context: ctx}
	tx, err := s.value.configured.db.BeginTx(runtimeContext, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return audit.ExactResult{}, classifySQL(err, false)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockSearchReadiness(runtimeContext, tx, s.value.configured.schema, ready); err != nil {
		return audit.ExactResult{}, err
	}
	if err := inspectExactCatalogs(runtimeContext, tx, s.value.configured.schema, view.Catalogs); err != nil {
		return audit.ExactResult{}, err
	}
	ids := exactPGRevisionIDs(view.Targets)
	predicate, arguments := exactRevisionPredicate(ids)
	count, rawBytes, err := exactCohortSize(runtimeContext, tx, s.value.configured.schema, predicate, arguments, len(ids), limits)
	if err != nil {
		return audit.ExactResult{}, err
	}
	candidates := make(map[audit.RevisionID]searchCandidate, count)
	if count > 0 {
		candidates, err = readExactCandidates(runtimeContext, tx, s.value.configured.schema, predicate, arguments, count, rawBytes, limits)
		if err != nil {
			return audit.ExactResult{}, err
		}
	}
	entries, err := exactPGEntries(view, candidates)
	if err != nil {
		return audit.ExactResult{}, err
	}
	result, err := audit.NewExactResult(query, audit.ExactResultData{Entries: entries})
	if err != nil {
		return audit.ExactResult{}, audit.Failure(audit.Corrupt, err)
	}
	if err := runtimeContext.Err(); err != nil {
		return audit.ExactResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return audit.ExactResult{}, classifySQL(err, false)
	}
	return result, nil
}

func validateExactPGQuery(view audit.ExactQueryView, ready readiness, limits audit.LimitSpec) error {
	if !ready.catalogs.HasActive() || view.Log == (audit.LogID{}) || view.Log != ready.logID {
		return audit.Failure(audit.Refused, errors.New("auditpg: exact query belongs to another or inactive log"))
	}
	if len(view.Catalogs) == 0 || len(view.Catalogs) > audit.MaxCatalogs || len(view.Targets) == 0 || len(view.Targets) > audit.MaxExactTargets || uint32(len(view.Targets)) > limits.ExactTargets {
		return audit.Failure(audit.Refused, errors.New("auditpg: exact target bounds are invalid"))
	}
	if view.MaxBytes == 0 || view.MaxBytes > limits.ExactBytes {
		return audit.Failure(audit.Refused, errors.New("auditpg: exact byte bounds are invalid"))
	}
	if !validExactPGNames(view.Resources, audit.MaxQueryResources) || !validExactPGNames(view.Actions, audit.MaxQueryActions) || !validExactPGClassifications(view.Classifications) || !validExactPGCatalogs(view.Catalogs, ready.catalogs.Active().ID) || !validExactPGCoordinates(view.Coordinates) {
		return audit.Failure(audit.Refused, errors.New("auditpg: exact filters are invalid"))
	}
	seen := make(map[audit.ExactTargetView]struct{}, len(view.Targets))
	for _, target := range view.Targets {
		reference, ok := validExactPGTarget(target)
		if !ok || !slices.Contains(view.Catalogs, reference.Catalog) {
			return audit.Failure(audit.Refused, errors.New("auditpg: exact target is invalid"))
		}
		if _, duplicate := seen[target]; duplicate {
			return audit.Failure(audit.Refused, errors.New("auditpg: exact target is duplicated"))
		}
		seen[target] = struct{}{}
	}
	return nil
}

func validExactPGNames[T ~string](values []T, limit int) bool {
	if len(values) == 0 || len(values) > limit {
		return false
	}
	seen := make(map[T]struct{}, len(values))
	for _, value := range values {
		if !validSearchName(string(value)) {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validExactPGClassifications(values []audit.Classification) bool {
	if len(values) == 0 || len(values) > audit.MaxQueryClassifications {
		return false
	}
	seen := make(map[audit.Classification]struct{}, len(values))
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

func validExactPGCatalogs(catalogs []audit.CatalogRef, active audit.CatalogID) bool {
	seen := make(map[audit.CatalogRef]struct{}, len(catalogs))
	for _, catalog := range catalogs {
		if catalog.ID != active || !validSearchName(string(catalog.ID)) || catalog.Generation == 0 || catalog.Generation > math.MaxInt64 || catalog.Digest == (audit.CatalogDigest{}) {
			return false
		}
		if _, duplicate := seen[catalog]; duplicate {
			return false
		}
		seen[catalog] = struct{}{}
	}
	return true
}

func validExactPGCoordinates(coordinates []audit.QueryCoordinateView) bool {
	if len(coordinates) > audit.MaxQueryCoordinates {
		return false
	}
	seen := false
	for _, coordinate := range coordinates {
		if seen || coordinate.Kind != audit.QueryScope || coordinate.Match != audit.QueryCoordinateExact || coordinate.Mode != audit.AsPlaintext || coordinate.Classification != 0 || coordinate.Resource != "" || coordinate.Action != "" || coordinate.Field != "" || coordinate.ActorPosition != 0 || coordinate.ActorKind != 0 || coordinate.ContextKind != 0 || coordinate.EntitySide != 0 || len(coordinate.Alternatives) != 1 {
			return false
		}
		alternative := coordinate.Alternatives[0]
		if !validSearchScope(alternative.Plaintext) || len(alternative.Tokens) != 0 || alternative.Commitments.Domain() != 0 || len(alternative.Commitments.Aliases()) != 0 {
			return false
		}
		seen = true
	}
	return true
}

func validExactPGTarget(target audit.ExactTargetView) (audit.RevisionRef, bool) {
	switch target.Kind {
	case audit.ExactRevisionTarget:
		return target.Revision, target.Item == (audit.ItemRef{}) && validExactPGRevision(target.Revision)
	case audit.ExactItemTarget:
		return target.Item.Revision, target.Revision == (audit.RevisionRef{}) && int(target.Item.Ordinal) < audit.MaxItems && validExactPGRevision(target.Item.Revision)
	default:
		return audit.RevisionRef{}, false
	}
}

func validExactPGRevision(reference audit.RevisionRef) bool {
	return validSearchName(string(reference.Catalog.ID)) && reference.Catalog.Generation > 0 && reference.Catalog.Generation <= math.MaxInt64 && reference.Catalog.Digest != (audit.CatalogDigest{}) && reference.Revision != (audit.RevisionID{})
}

func inspectExactCatalogs(ctx context.Context, tx *sql.Tx, schema Schema, catalogs []audit.CatalogRef) error {
	predicate, arguments := exactCatalogPredicate(catalogs)
	rows, err := tx.QueryContext(ctx, `SELECT catalog_id, generation, digest FROM `+quoteIdentifier(schema.Name)+`.catalogs WHERE `+predicate+` LIMIT `+strconv.Itoa(len(catalogs)+1), arguments...)
	if err != nil {
		return classifySQL(err, false)
	}
	defer rows.Close()
	requested := make(map[audit.CatalogRef]struct{}, len(catalogs))
	for _, catalog := range catalogs {
		requested[catalog] = struct{}{}
	}
	seen := make(map[audit.CatalogRef]struct{}, len(catalogs))
	for rows.Next() {
		var id string
		var generation int64
		var digestBytes []byte
		if err := rows.Scan(&id, &generation, &digestBytes); err != nil {
			return classifySQL(err, false)
		}
		if generation <= 0 || len(digestBytes) != len(audit.CatalogDigest{}) {
			return audit.Failure(audit.Corrupt, errors.New("auditpg: exact catalog row is malformed"))
		}
		var digest audit.CatalogDigest
		copy(digest[:], digestBytes)
		reference := audit.CatalogRef{ID: audit.CatalogID(id), Generation: audit.CatalogGeneration(generation), Digest: digest}
		if _, expected := requested[reference]; !expected {
			return audit.Failure(audit.Corrupt, errors.New("auditpg: exact catalog lookup returned an unexpected row"))
		}
		if _, duplicate := seen[reference]; duplicate {
			return audit.Failure(audit.Corrupt, errors.New("auditpg: exact catalog lookup returned a duplicate row"))
		}
		seen[reference] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return classifySQL(err, false)
	}
	if len(seen) != len(catalogs) {
		return audit.Failure(audit.Refused, errors.New("auditpg: exact catalog is not installed"))
	}
	return nil
}

func exactCatalogPredicate(catalogs []audit.CatalogRef) (string, []any) {
	parts := make([]string, len(catalogs))
	arguments := make([]any, 0, len(catalogs)*3)
	for index, catalog := range catalogs {
		first := len(arguments) + 1
		parts[index] = `(catalog_id=$` + strconv.Itoa(first) + ` AND generation=$` + strconv.Itoa(first+1) + ` AND digest=$` + strconv.Itoa(first+2) + `)`
		arguments = append(arguments, string(catalog.ID), int64(catalog.Generation), bytes.Clone(catalog.Digest[:]))
	}
	return "(" + strings.Join(parts, " OR ") + ")", arguments
}

func exactPGRevisionIDs(targets []audit.ExactTargetView) []audit.RevisionID {
	result := make([]audit.RevisionID, 0, len(targets))
	seen := make(map[audit.RevisionID]struct{}, len(targets))
	for _, target := range targets {
		reference, _ := validExactPGTarget(target)
		if _, duplicate := seen[reference.Revision]; duplicate {
			continue
		}
		seen[reference.Revision] = struct{}{}
		result = append(result, reference.Revision)
	}
	return result
}

func exactRevisionPredicate(ids []audit.RevisionID) (string, []any) {
	placeholders := make([]string, len(ids))
	arguments := make([]any, len(ids))
	for index, id := range ids {
		placeholders[index] = "$" + strconv.Itoa(index+1)
		arguments[index] = bytes.Clone(id[:])
	}
	return "revision_id IN (" + strings.Join(placeholders, ",") + ")", arguments
}

func exactCohortSize(ctx context.Context, tx *sql.Tx, schema Schema, predicate string, arguments []any, expected int, limits audit.LimitSpec) (int, uint64, error) {
	var count, total, largest int64
	err := tx.QueryRowContext(ctx, `SELECT count(*)::bigint, COALESCE(sum(wire_bytes),0)::bigint, COALESCE(max(wire_bytes),0)::bigint
FROM (
	SELECT octet_length(wire)::bigint AS wire_bytes
	FROM `+quoteIdentifier(schema.Name)+`.revisions
	WHERE `+predicate+`
	LIMIT `+strconv.Itoa(expected+1)+`
) AS bounded`, arguments...).Scan(&count, &total, &largest)
	if err != nil {
		return 0, 0, classifySQL(err, false)
	}
	if count < 0 || total < 0 || largest < 0 || count > int64(expected) {
		return 0, 0, audit.Failure(audit.Corrupt, errors.New("auditpg: exact cohort measurement is invalid"))
	}
	if uint64(largest) > limits.RevisionBytes {
		return 0, 0, audit.Failure(audit.Corrupt, errors.New("auditpg: persisted exact revision exceeds configured limits"))
	}
	if uint64(total) > limits.ExactBytes {
		return 0, 0, audit.Failure(audit.Refused, errors.New("auditpg: exact candidate cohort exceeds configured byte limits"))
	}
	return int(count), uint64(total), nil
}

func readExactCandidates(ctx context.Context, tx *sql.Tx, schema Schema, predicate string, arguments []any, expected int, expectedBytes uint64, limits audit.LimitSpec) (map[audit.RevisionID]searchCandidate, error) {
	rows, err := tx.QueryContext(ctx, `SELECT revision_id, catalog_id, catalog_generation, operation, wire, recorded_at, position
FROM `+quoteIdentifier(schema.Name)+`.revisions
WHERE `+predicate+`
LIMIT `+strconv.Itoa(expected+1), arguments...)
	if err != nil {
		return nil, classifySQL(err, false)
	}
	defer rows.Close()
	result := make(map[audit.RevisionID]searchCandidate, expected)
	var total uint64
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate, err := scanSearchCandidate(rows, limits)
		if err != nil {
			return nil, err
		}
		id := candidate.observed.RevisionID
		if _, duplicate := result[id]; duplicate || len(result) >= expected || candidate.rawBytes > limits.ExactBytes-total {
			return nil, audit.Failure(audit.Corrupt, errors.New("auditpg: exact cohort changed beyond verified limits"))
		}
		result[id] = candidate
		total += candidate.rawBytes
	}
	if err := rows.Err(); err != nil {
		return nil, classifySQL(err, false)
	}
	if len(result) != expected || total != expectedBytes {
		return nil, audit.Failure(audit.Corrupt, errors.New("auditpg: exact cohort changed within one snapshot"))
	}
	return result, nil
}

func exactPGEntries(view audit.ExactQueryView, candidates map[audit.RevisionID]searchCandidate) ([]audit.ExactEntryData, error) {
	entries := make([]audit.ExactEntryData, len(view.Targets))
	var total uint64
	for index, target := range view.Targets {
		entries[index] = audit.ExactEntryData{Target: target, State: audit.ExactMissing}
		reference, _ := validExactPGTarget(target)
		candidate, found := candidates[reference.Revision]
		if !found || !exactPGCovered(view, target, reference, candidate.stored.View().Revision) {
			continue
		}
		size := candidate.stored.EncodedBytes()
		if size > view.MaxBytes-total {
			return nil, audit.Failure(audit.Refused, errors.New("auditpg: exact byte limit cannot hold the requested evidence"))
		}
		total += size
		entries[index].State = audit.ExactFound
		entries[index].Revision = candidate.stored
	}
	return entries, nil
}

func exactPGCovered(view audit.ExactQueryView, target audit.ExactTargetView, reference audit.RevisionRef, revision audit.RevisionWireView) bool {
	header := revision.Header
	if header.Log != view.Log || header.Catalog != reference.Catalog || header.RevisionID != reference.Revision || !matchesExactPGScope(view.Coordinates, revision.Context) {
		return false
	}
	if target.Kind == audit.ExactRevisionTarget {
		return searchSubset(header.Authorization.Resources, view.Resources) && searchSubset(header.Authorization.Actions, view.Actions) && searchSubset(header.Authorization.Classifications, view.Classifications)
	}
	if target.Kind != audit.ExactItemTarget || int(target.Item.Ordinal) >= len(revision.Items) || revision.Items[target.Item.Ordinal].Ordinal != target.Item.Ordinal {
		return false
	}
	item := revision.Items[target.Item.Ordinal]
	return slices.Contains(view.Resources, item.Resource) && slices.Contains(view.Actions, item.Action) && exactPGItemClassifications(item, view.Classifications)
}

func matchesExactPGScope(coordinates []audit.QueryCoordinateView, facts []audit.StoredContextFactView) bool {
	for _, coordinate := range coordinates {
		matched := false
		for _, fact := range facts {
			if fact.Kind == audit.ScopeContext && fact.Mode == audit.AsPlaintext && bytes.Equal(fact.Plaintext, coordinate.Alternatives[0].Plaintext) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func exactPGItemClassifications(item audit.ItemWireView, ceiling []audit.Classification) bool {
	values := make([]audit.Classification, 0, 2+len(item.Values)+len(item.Changes)*2)
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

var _ audit.ExactLog = (*Store)(nil)
