package auditpg

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/frostgrove/vv/audit"
)

type searchContext struct{ context.Context }

func (searchContext) Value(any) any { return nil }

type searchCandidate struct {
	stored   audit.StoredRevision
	observed audit.RevisionHeaderView
	rawBytes uint64
}

func (s *Store) Search(ctx context.Context, query audit.StoreQuery) (audit.StoredPage, error) {
	if ctx == nil {
		return audit.StoredPage{}, audit.Failure(audit.Refused, errors.New("auditpg: history context is nil"))
	}
	ready, err := s.ready()
	if err != nil {
		return audit.StoredPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return audit.StoredPage{}, err
	}
	view := query.View()
	limits := s.value.configured.limits.View()
	if err := validateSearchQuery(view, ready, limits); err != nil {
		return audit.StoredPage{}, err
	}
	runtimeContext := searchContext{Context: ctx}
	tx, err := s.value.configured.db.BeginTx(runtimeContext, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return audit.StoredPage{}, classifySQL(err, false)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockSearchReadiness(runtimeContext, tx, s.value.configured.schema, ready); err != nil {
		return audit.StoredPage{}, err
	}
	predicate, arguments := searchPredicate(view)
	count, rawBytes, err := searchCohortSize(runtimeContext, tx, s.value.configured.schema, predicate, arguments, limits)
	if err != nil {
		return audit.StoredPage{}, err
	}
	var candidates []searchCandidate
	if count > 0 {
		candidates, err = readSearchCandidates(runtimeContext, tx, s.value.configured.schema, predicate, arguments, count, rawBytes, limits)
		if err != nil {
			return audit.StoredPage{}, err
		}
	}
	page, err := buildSearchPage(query, view, candidates)
	if err != nil {
		return audit.StoredPage{}, err
	}
	if err := runtimeContext.Err(); err != nil {
		return audit.StoredPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return audit.StoredPage{}, classifySQL(err, false)
	}
	return page, nil
}

func lockSearchReadiness(ctx context.Context, tx *sql.Tx, schema Schema, expected readiness) error {
	q := quoteIdentifier(schema.Name)
	var singleton bool
	if err := tx.QueryRowContext(ctx, `SELECT singleton FROM `+q+`.settings WHERE singleton FOR SHARE`).Scan(&singleton); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return audit.Failure(audit.Refused, ErrSchemaMismatch)
		}
		return classifySQL(err, false)
	}
	if !singleton {
		return audit.Failure(audit.Refused, errors.New("auditpg: history readiness lock failed"))
	}
	actual, err := readReady(ctx, tx, schema)
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	if !sameReady(actual, expected) {
		return audit.Failure(audit.Conflict, errors.New("auditpg: history store readiness changed"))
	}
	return nil
}

func searchCohortSize(ctx context.Context, tx *sql.Tx, schema Schema, predicate string, arguments []any, limits audit.LimitSpec) (int64, uint64, error) {
	q := quoteIdentifier(schema.Name)
	bounded := append(slices.Clone(arguments), int64(limits.SearchCohortRevisions)+1)
	var count, bytesTotal, largest int64
	err := tx.QueryRowContext(ctx, `SELECT count(*)::bigint,
	COALESCE(sum(wire_bytes), 0)::bigint, COALESCE(max(wire_bytes), 0)::bigint
FROM (
	SELECT octet_length(wire)::bigint AS wire_bytes
	FROM `+q+`.revisions
	WHERE `+predicate+`
	ORDER BY position
	LIMIT $`+fmt.Sprint(len(bounded))+`
) AS bounded`, bounded...).Scan(&count, &bytesTotal, &largest)
	if err != nil {
		return 0, 0, classifySQL(err, false)
	}
	if count < 0 || bytesTotal < 0 || largest < 0 {
		return 0, 0, audit.Failure(audit.Corrupt, errors.New("auditpg: history cohort measurement is invalid"))
	}
	if count > int64(limits.SearchCohortRevisions) || uint64(bytesTotal) > limits.SearchCohortBytes {
		return 0, 0, audit.Failure(audit.Refused, errors.New("auditpg: history candidate cohort exceeds configured limits"))
	}
	if uint64(largest) > limits.RevisionBytes {
		return 0, 0, audit.Failure(audit.Corrupt, errors.New("auditpg: persisted revision exceeds configured limits"))
	}
	return count, uint64(bytesTotal), nil
}

func readSearchCandidates(ctx context.Context, tx *sql.Tx, schema Schema, predicate string, arguments []any, expectedCount int64, expectedBytes uint64, limits audit.LimitSpec) ([]searchCandidate, error) {
	q := quoteIdentifier(schema.Name)
	bounded := append(slices.Clone(arguments), int64(limits.SearchCohortRevisions)+1)
	rows, err := tx.QueryContext(ctx, `SELECT revision_id, catalog_id, catalog_generation, operation,
	wire, recorded_at, position
FROM `+q+`.revisions
WHERE `+predicate+`
ORDER BY position
LIMIT $`+fmt.Sprint(len(bounded)), bounded...)
	if err != nil {
		return nil, classifySQL(err, false)
	}
	defer rows.Close()
	result := make([]searchCandidate, 0, expectedCount)
	var total uint64
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate, err := scanSearchCandidate(rows, limits)
		if err != nil {
			return nil, err
		}
		if uint64(len(result)) >= uint64(limits.SearchCohortRevisions) || candidate.rawBytes > limits.SearchCohortBytes-total {
			return nil, audit.Failure(audit.Corrupt, errors.New("auditpg: history cohort changed beyond verified limits"))
		}
		result = append(result, candidate)
		total += candidate.rawBytes
	}
	if err := rows.Err(); err != nil {
		return nil, classifySQL(err, false)
	}
	if int64(len(result)) != expectedCount || total != expectedBytes {
		return nil, audit.Failure(audit.Corrupt, errors.New("auditpg: history cohort changed within one snapshot"))
	}
	return result, nil
}

func scanSearchCandidate(row interface{ Scan(...any) error }, limits audit.LimitSpec) (searchCandidate, error) {
	var revisionBytes, wire []byte
	var catalogID, operation string
	var catalogGeneration, position int64
	var recordedAt sql.NullTime
	if err := row.Scan(&revisionBytes, &catalogID, &catalogGeneration, &operation, &wire, &recordedAt, &position); err != nil {
		return searchCandidate{}, classifySQL(err, false)
	}
	if len(wire) == 0 || uint64(len(wire)) > limits.RevisionBytes || !recordedAt.Valid {
		return searchCandidate{}, audit.Failure(audit.Corrupt, errors.New("auditpg: persisted history row is malformed"))
	}
	revisionID, ok := id16[audit.RevisionID](revisionBytes)
	if !ok || catalogGeneration <= 0 {
		return searchCandidate{}, audit.Failure(audit.Corrupt, errors.New("auditpg: persisted history identity is malformed"))
	}
	view, err := decodeEvidence(wire)
	if err != nil {
		return searchCandidate{}, audit.Failure(audit.Corrupt, err)
	}
	if view.Header.RevisionID != revisionID || string(view.Header.Catalog.ID) != catalogID || uint64(view.Header.Catalog.Generation) != uint64(catalogGeneration) || string(view.Header.Operation) != operation {
		return searchCandidate{}, audit.Failure(audit.Corrupt, errors.New("auditpg: persisted history metadata disagrees with evidence"))
	}
	storePosition, err := positionOf(position)
	if err != nil {
		return searchCandidate{}, audit.Failure(audit.Corrupt, err)
	}
	stored, err := audit.NewStoredRevision(audit.StoredRevisionData{
		Revision: view, RecordedAt: recordedAt.Time.UTC(), Position: storePosition,
	})
	if err != nil || stored.EncodedBytes() > limits.RevisionBytes {
		if err == nil {
			err = errors.New("decoded revision exceeds configured limits")
		}
		return searchCandidate{}, audit.Failure(audit.Corrupt, err)
	}
	return searchCandidate{stored: stored, observed: view.Header, rawBytes: uint64(len(wire))}, nil
}

func buildSearchPage(query audit.StoreQuery, view audit.StoreQueryView, candidates []searchCandidate) (audit.StoredPage, error) {
	matched := make([]searchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if matchesSearchQuery(view, candidate.stored.View().Revision) {
			matched = append(matched, candidate)
		}
	}
	slices.SortFunc(matched, func(left, right searchCandidate) int {
		comparison := left.observed.ObservedAt.Compare(right.observed.ObservedAt)
		if comparison == 0 {
			comparison = bytes.Compare(left.observed.RevisionID[:], right.observed.RevisionID[:])
		}
		if view.Direction == audit.NewestFirst {
			return -comparison
		}
		return comparison
	})
	revisions := make([]audit.StoredRevision, 0, min(len(matched), int(view.Limit)))
	var total uint64
	hasMore := false
	for index, candidate := range matched {
		if len(revisions) == int(view.Limit) {
			hasMore = true
			break
		}
		size := candidate.stored.EncodedBytes()
		if size > view.MaxBytes-total {
			if len(revisions) == 0 {
				return audit.StoredPage{}, audit.Failure(audit.Refused, errors.New("auditpg: page byte limit cannot hold a revision"))
			}
			hasMore = true
			break
		}
		revisions = append(revisions, candidate.stored)
		total += size
		if len(revisions) == int(view.Limit) && index+1 < len(matched) {
			hasMore = true
		}
	}
	var position audit.StorePosition
	if len(revisions) > 0 {
		position = revisions[len(revisions)-1].View().Position
	}
	page, err := audit.NewStoredPage(query, audit.StoredPageData{
		Revisions: revisions, Position: position, HasMore: hasMore,
		Progress: audit.SearchProgressView{Pages: 1, Revisions: uint32(len(revisions)), Bytes: total},
	})
	if err != nil {
		return audit.StoredPage{}, audit.Failure(audit.Corrupt, err)
	}
	return page, nil
}

func searchPredicate(view audit.StoreQueryView) (string, []any) {
	parts := make([]string, len(view.Catalogs))
	arguments := make([]any, 0, len(view.Catalogs)*2+1)
	for index, catalog := range view.Catalogs {
		first := len(arguments) + 1
		arguments = append(arguments, string(catalog.ID), int64(catalog.Generation))
		parts[index] = `(catalog_id=$` + fmt.Sprint(first) + ` AND catalog_generation=$` + fmt.Sprint(first+1) + `)`
	}
	predicate := "(" + strings.Join(parts, " OR ") + ")"
	if view.OperationName != "" {
		arguments = append(arguments, string(view.OperationName))
		predicate += ` AND operation=$` + fmt.Sprint(len(arguments))
	}
	return predicate, arguments
}

func validateSearchQuery(view audit.StoreQueryView, ready readiness, limits audit.LimitSpec) error {
	if !ready.catalogs.HasActive() || view.Log == (audit.LogID{}) || view.Log != ready.logID {
		return audit.Failure(audit.Refused, errors.New("auditpg: history query belongs to another or inactive log"))
	}
	if view.Limit == 0 || view.Limit > limits.PageRevisions || view.MaxBytes == 0 || view.MaxBytes > limits.PageBytes || view.Direction != audit.OldestFirst && view.Direction != audit.NewestFirst {
		return audit.Failure(audit.Refused, errors.New("auditpg: history page bounds are invalid"))
	}
	if len(view.Catalogs) == 0 || len(view.Catalogs) > audit.MaxCatalogs || len(view.Resources) == 0 || len(view.Resources) > audit.MaxQueryResources || len(view.Actions) == 0 || len(view.Actions) > audit.MaxQueryActions || len(view.Classifications) == 0 || len(view.Classifications) > audit.MaxQueryClassifications {
		return audit.Failure(audit.Refused, errors.New("auditpg: history filter bounds are invalid"))
	}
	if !validSearchCatalogs(view.Catalogs, ready.catalogs.Active().ID) || !validSearchNames(view.Resources) || !validSearchNames(view.Actions) || !onlyPublicClassifications(view.Classifications) {
		return audit.Failure(audit.Refused, errors.New("auditpg: history filters are invalid"))
	}
	if !validSearchFieldProjection(view.Fields) || !validSearchContextProjection(view.Context) {
		return audit.Failure(audit.Refused, errors.New("auditpg: history projection is invalid"))
	}
	if view.Time != (audit.TimeWindowView{}) || view.Changed.Match != 0 || len(view.Changed.Fields) != 0 || len(view.Changed.Excluded) != 0 || len(view.Outcomes) != 0 || len(view.Reasons) != 0 || view.Progress != (audit.SearchProgressView{}) || len(view.Snapshot) != 0 || !view.SnapshotExpiry.IsZero() || len(view.After.Bytes()) != 0 {
		return audit.Failure(audit.Refused, errors.New("auditpg: stable or advanced history query is unsupported"))
	}
	shape, err := validateSearchCoordinates(view.Coordinates)
	if err != nil {
		return err
	}
	if !validSearchClass(view, shape) {
		return audit.Failure(audit.Refused, errors.New("auditpg: history query class is inconsistent"))
	}
	return nil
}

func validSearchFieldProjection(projection audit.FieldProjectionView) bool {
	switch projection.Kind {
	case audit.ProjectionNone:
		return len(projection.Fields) == 0
	case audit.ProjectionAll, audit.ProjectionOnly:
		return len(projection.Fields) > 0 && len(projection.Fields) <= audit.MaxFieldsPerItem && validSearchNames(projection.Fields)
	default:
		return false
	}
}

func validSearchContextProjection(projection audit.ContextProjectionView) bool {
	if projection.Kind == audit.ProjectionNone {
		return len(projection.Facts) == 0
	}
	if projection.Kind != audit.ProjectionAll && projection.Kind != audit.ProjectionOnly || len(projection.Facts) == 0 || len(projection.Facts) > int(audit.SourceContext) {
		return false
	}
	seen := make(map[audit.ContextFactKind]struct{}, len(projection.Facts))
	for _, fact := range projection.Facts {
		if !fact.Valid() {
			return false
		}
		if _, duplicate := seen[fact]; duplicate {
			return false
		}
		seen[fact] = struct{}{}
	}
	return true
}

type searchCoordinateShape struct {
	scope   bool
	subject bool
	target  bool
}

func validateSearchCoordinates(coordinates []audit.QueryCoordinateView) (searchCoordinateShape, error) {
	if len(coordinates) > audit.MaxQueryCoordinates {
		return searchCoordinateShape{}, audit.Failure(audit.Refused, errors.New("auditpg: history coordinates exceed configured limits"))
	}
	var shape searchCoordinateShape
	for _, coordinate := range coordinates {
		if coordinate.Match != audit.QueryCoordinateExact || coordinate.Mode != audit.AsPlaintext || coordinate.Classification != 0 && coordinate.Classification != audit.Public || coordinate.Resource != "" || coordinate.Action != "" || coordinate.Field != "" || coordinate.ActorPosition != 0 || coordinate.ActorKind != 0 || coordinate.ContextKind != 0 || coordinate.EntitySide != 0 || len(coordinate.Alternatives) != 1 {
			return searchCoordinateShape{}, audit.Failure(audit.Refused, errors.New("auditpg: only exact public plaintext coordinates are supported"))
		}
		alternative := coordinate.Alternatives[0]
		if len(alternative.Tokens) != 0 || alternative.Commitments.Domain() != 0 || len(alternative.Commitments.Aliases()) != 0 {
			return searchCoordinateShape{}, audit.Failure(audit.Refused, errors.New("auditpg: protected history coordinates are unsupported"))
		}
		switch coordinate.Kind {
		case audit.QueryScope:
			if shape.scope || !validSearchScope(alternative.Plaintext) {
				return searchCoordinateShape{}, audit.Failure(audit.Refused, errors.New("auditpg: history scope coordinate is invalid"))
			}
			shape.scope = true
		case audit.QuerySubject:
			if shape.subject || !validSearchReference(alternative.Plaintext) {
				return searchCoordinateShape{}, audit.Failure(audit.Refused, errors.New("auditpg: history subject coordinate is invalid"))
			}
			shape.subject = true
		case audit.QueryTarget:
			if shape.target || !validSearchReference(alternative.Plaintext) {
				return searchCoordinateShape{}, audit.Failure(audit.Refused, errors.New("auditpg: history target coordinate is invalid"))
			}
			shape.target = true
		default:
			return searchCoordinateShape{}, audit.Failure(audit.Refused, errors.New("auditpg: history coordinate kind is unsupported"))
		}
	}
	return shape, nil
}

func validSearchClass(view audit.StoreQueryView, shape searchCoordinateShape) bool {
	operationName := view.OperationName != ""
	operation := view.Operation != (audit.OperationID{})
	switch view.Class {
	case audit.SubjectHistoryQuery:
		return shape.subject && !shape.target && !operationName && !operation
	case audit.ResourceHistoryQuery:
		return !shape.subject && !shape.target && !operationName && !operation
	case audit.EventTargetHistoryQuery:
		return shape.target && !shape.subject && !operationName && !operation
	case audit.EventTypeHistoryQuery:
		return !shape.subject && !shape.target && !operationName && !operation
	case audit.OperationTypeHistoryQuery:
		return !shape.subject && !shape.target && operationName && !operation && validSearchName(string(view.OperationName))
	case audit.OperationInstanceHistoryQuery:
		return !shape.subject && !shape.target && operationName && operation && validSearchName(string(view.OperationName))
	default:
		return false
	}
}

func validSearchCatalogs(catalogs []audit.CatalogRef, active audit.CatalogID) bool {
	seen := make(map[audit.CatalogRef]struct{}, len(catalogs))
	for _, catalog := range catalogs {
		if catalog.ID != active || !validSearchName(string(catalog.ID)) || catalog.Generation == 0 || catalog.Digest == (audit.CatalogDigest{}) {
			return false
		}
		if _, duplicate := seen[catalog]; duplicate {
			return false
		}
		seen[catalog] = struct{}{}
	}
	return true
}

func validSearchNames[T ~string](values []T) bool {
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

func onlyPublicClassifications(values []audit.Classification) bool {
	seen := false
	for _, value := range values {
		if value != audit.Public || seen {
			return false
		}
		seen = true
	}
	return seen
}

func validSearchName(value string) bool {
	if value == "" || len(value) > audit.MaxNameBytes {
		return false
	}
	segmentStart := true
	for _, current := range value {
		if segmentStart {
			if current < 'a' || current > 'z' {
				return false
			}
			segmentStart = false
			continue
		}
		switch {
		case current == '.':
			segmentStart = true
		case current >= 'a' && current <= 'z':
		case current >= '0' && current <= '9':
		case current == '_':
		default:
			return false
		}
	}
	return !segmentStart
}

func validSearchScope(value []byte) bool {
	parts := bytes.Split(value, []byte{0})
	return len(parts) == 2 && validSearchReference(parts[0]) && validSearchReference(parts[1])
}

func validSearchReference(value []byte) bool {
	if len(value) == 0 || len(value) > audit.MaxReferenceBytes || !utf8.Valid(value) {
		return false
	}
	for _, current := range string(value) {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}

func matchesSearchQuery(query audit.StoreQueryView, revision audit.RevisionWireView) bool {
	header := revision.Header
	if header.Log != query.Log || !slices.Contains(query.Catalogs, header.Catalog) || !searchSubset(header.Authorization.Resources, query.Resources) || !searchSubset(header.Authorization.Actions, query.Actions) || !searchSubset(header.Authorization.Classifications, query.Classifications) {
		return false
	}
	if query.OperationName != "" && header.Operation != query.OperationName || query.Operation != (audit.OperationID{}) && header.OperationID != query.Operation {
		return false
	}
	if !matchesSearchScope(query.Coordinates, revision.Context) {
		return false
	}
	for _, item := range revision.Items {
		if slices.Contains(query.Resources, item.Resource) && slices.Contains(query.Actions, item.Action) && matchesSearchItem(query.Coordinates, item) {
			return true
		}
	}
	return false
}

func matchesSearchScope(coordinates []audit.QueryCoordinateView, facts []audit.StoredContextFactView) bool {
	for _, coordinate := range coordinates {
		if coordinate.Kind != audit.QueryScope {
			continue
		}
		want := coordinate.Alternatives[0].Plaintext
		found := false
		for _, fact := range facts {
			if fact.Kind == audit.ScopeContext && fact.Classification == audit.Public && fact.Mode == audit.AsPlaintext && !fact.Redacted && bytes.Equal(fact.Plaintext, want) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func matchesSearchItem(coordinates []audit.QueryCoordinateView, item audit.ItemWireView) bool {
	for _, coordinate := range coordinates {
		want := coordinate.Alternatives[0].Plaintext
		switch coordinate.Kind {
		case audit.QueryScope:
			continue
		case audit.QuerySubject:
			if !matchesSearchValue(item.Subject, want) {
				return false
			}
		case audit.QueryTarget:
			if !matchesSearchValue(item.Target, want) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func matchesSearchValue(value audit.StoredValueView, want []byte) bool {
	return value.State == audit.ValuePresent && value.Classification == audit.Public && value.Mode == audit.AsPlaintext && !value.Redacted && bytes.Equal(value.Plaintext, want)
}

func searchSubset[T comparable](values, ceiling []T) bool {
	for _, value := range values {
		if !slices.Contains(ceiling, value) {
			return false
		}
	}
	return true
}

var _ audit.Log = (*Store)(nil)
